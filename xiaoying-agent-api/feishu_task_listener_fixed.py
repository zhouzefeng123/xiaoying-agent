#!/usr/bin/env python3
"""
飞书任务监听器 - XiaoYing 双向通信 (GUI 版本)
从飞书技术研发群读取指令，执行后将结果发送回飞书
"""

import os
import sys
import json
import time
import hashlib
import threading
import shutil
import requests
import tkinter as tk
from tkinter import ttk, scrolledtext, messagebox, filedialog
from pathlib import Path
from datetime import datetime

import re

# ==================== 配置 ====================
# 配置将从 config.json 读取，不再硬编码
CONFIG_FILE = Path(__file__).parent / "feishu_config.json"

# 默认配置
DEFAULT_CONFIG = {
    "app_id": "",
    "app_secret": "",
    "target_chat_id": ""
}

def load_config():
    if CONFIG_FILE.exists():
        try:
            with open(CONFIG_FILE, 'r', encoding='utf-8') as f:
                return json.load(f)
        except Exception as e:
            print(f"读取配置文件失败: {e}")
    return DEFAULT_CONFIG.copy()

def save_config(config):
    try:
        with open(CONFIG_FILE, 'w', encoding='utf-8') as f:
            json.dump(config, f, indent=4, ensure_ascii=False)
        return True
    except Exception as e:
        print(f"保存配置文件失败: {e}")
        return False

# 全局配置变量
config = load_config()
APP_ID = config.get("app_id", "")
APP_SECRET = config.get("app_secret", "")
TARGET_CHAT_ID = config.get("target_chat_id", "")

# XiaoYing 后端地址
AURAMATE_API_URL = "http://localhost:3000/v1/chat/completions"
CACHE_FILE = Path(__file__).parent / "feishu_message_cache.json"
TASK_OUTPUT_DIR = Path(__file__).parent / "feishu_tasks"
RESULT_OUTPUT_DIR = Path(__file__).parent / "feishu_results"

# 确保目录存在
TASK_OUTPUT_DIR.mkdir(exist_ok=True)
RESULT_OUTPUT_DIR.mkdir(exist_ok=True)


# ==================== 飞书 API 客户端 ====================
class FeishuAPI:
    """飞书 API 客户端"""
    
    def __init__(self, app_id, app_secret):
        self.app_id = app_id
        self.app_secret = app_secret
        self.access_token = None
        self.token_expire_time = 0
        self.base_url = "https://open.feishu.cn/open-apis"
        
    def update_credentials(self, app_id, app_secret):
        """更新凭证"""
        self.app_id = app_id
        self.app_secret = app_secret
        # 强制过期 Token 以便下次重新获取
        self.token_expire_time = 0
    
    def get_access_token(self):
        """获取访问令牌"""
        if self.access_token and time.time() < self.token_expire_time:
            return self.access_token
        
        url = f"{self.base_url}/auth/v3/tenant_access_token/internal"
        payload = {
            "app_id": self.app_id,
            "app_secret": self.app_secret
        }
        
        try:
            response = requests.post(url, json=payload, timeout=10)
            data = response.json()
            
            if data.get("code") == 0:
                self.access_token = data["tenant_access_token"]
                self.token_expire_time = time.time() + data["expire"] - 60
                return self.access_token
            else:
                print(f"获取 token 失败：{data}")
                return None
        except Exception as e:
            print(f"请求错误：{e}")
            return None
    
    def get_headers(self):
        """获取请求头"""
        token = self.get_access_token()
        if not token:
            return None
        return {
            "Authorization": f"Bearer {token}",
            "Content-Type": "application/json"
        }
    
    def send_message(self, chat_id, content, msg_type="text"):
        """发送消息到指定聊天"""
        url = f"{self.base_url}/im/v1/messages"
        headers = self.get_headers()
        if not headers:
            return None, "无法获取访问令牌"
        
        payload = {
            "receive_id": chat_id,
            "msg_type": msg_type,
            "content": json.dumps(content, ensure_ascii=False) if isinstance(content, dict) else content
        }
        
        params = {"receive_id_type": "chat_id"}
        
        try:
            response = requests.post(url, headers=headers, params=params, json=payload, timeout=10)
            data = response.json()
            if data.get("code") == 0:
                return data.get("data"), None
            else:
                print(f"发送消息失败：{data}")
                return None, data.get("msg", "未知错误")
        except Exception as e:
            print(f"请求错误：{e}")
            return None, str(e)
    
    def get_messages(self, chat_id, page_size=20):
        """获取聊天消息"""
        url = f"{self.base_url}/im/v1/messages"
        headers = self.get_headers()
        if not headers:
            return None
        
        params = {
            "container_id": chat_id,
            "container_id_type": "chat",
            "page_size": page_size,
            "sort_type": "ByCreateTimeDesc" # 回滚：使用降序，获取最新的消息（items[0]即为最新）
        }
        
        try:
            # print(f"DEBUG: Fetching messages from {url} with params {params}")
            response = requests.get(url, headers=headers, params=params, timeout=10)
            data = response.json()
            
            if data.get("code") == 0:
                items = data.get("data", {}).get("items", [])
                # print(f"DEBUG: Got {len(items)} messages")
                if len(items) > 0:
                    # print(f"DEBUG: First message (Newest) raw: {json.dumps(items[0], ensure_ascii=False)[:200]}...")
                    pass
                return items
            else:
                print(f"获取消息失败：{data}")
                return None
        except Exception as e:
            print(f"请求错误：{e}")
            return None


# ==================== 任务监听器 ====================
    def get_groups(self):
        """获取机器人所在的群组列表"""
        url = f"{self.base_url}/im/v1/chats"
        headers = self.get_headers()
        if not headers:
            return []
        
        # 参数：分页大小，且仅获取群聊
        params = {
            "page_size": 20,
            "sort_type": "ByCreateTimeAsc" # 修正：ByCreateTime 是无效的，必须是 ByCreateTimeAsc
        }
        
        try:
            response = requests.get(url, headers=headers, params=params, timeout=10)
            data = response.json()
            if data.get("code") == 0:
                return data.get("data", {}).get("items", [])
            else:
                print(f"获取群组列表失败：{data}")
                return []
        except Exception as e:
            print(f"请求错误：{e}")
            return []

class TaskListener:
    """任务监听器"""
    
    def __init__(self, api):
        self.api = api
        self.last_message_id = None
        self.last_message_hash = None
        self.processed_messages = set()
        self.load_cache()
    
    def load_cache(self):
        """加载缓存"""
        if CACHE_FILE.exists():
            try:
                with open(CACHE_FILE, 'r', encoding='utf-8') as f:
                    cache = json.load(f)
                    self.last_message_id = cache.get("last_message_id")
                    self.last_message_hash = cache.get("last_message_hash")
                    self.processed_messages = set(cache.get("processed_messages", []))
            except Exception as e:
                print(f"加载缓存失败：{e}")
    
    def save_cache(self):
        """保存缓存"""
        try:
            with open(CACHE_FILE, 'w', encoding='utf-8') as f:
                json.dump({
                    "last_message_id": self.last_message_id,
                    "last_message_hash": self.last_message_hash,
                    "processed_messages": list(self.processed_messages)
                }, f, ensure_ascii=False, indent=2)
        except Exception as e:
            print(f"保存缓存失败：{e}")
    
    def get_message_hash(self, message):
        """生成消息的唯一哈希"""
        content = message.get("content", "")
        sender = message.get("sender_id", "")
        create_time = message.get("create_time", "")
        text = f"{sender}:{create_time}:{content}"
        return hashlib.md5(text.encode()).hexdigest()
    
    def parse_message_content(self, message):
        """解析消息内容"""
        # 修正：内容在 body.content 中
        body = message.get("body", {})
        content_raw = body.get("content", "")
        
        # 兼容旧逻辑：如果顶层有 content
        if not content_raw:
            content_raw = message.get("content", "")
            
        if not content_raw:
            return ""
        
        try:
            # 飞书消息内容通常是 JSON 字符串，例如 '{"text": "你好"}'
            content_json = json.loads(content_raw)
            if "text" in content_json:
                return content_json["text"]
            elif "image_key" in content_json:
                return "[图片]"
            elif "file_key" in content_json:
                return f"[文件] {content_json.get('file_name', '')}"
            # 处理富文本 post
            elif "post" in content_json:
                # 简单提取 post 中的文本
                post_content = content_json.get("post", {}).get("zh_cn", {}).get("content", [])
                texts = []
                for paragraph in post_content:
                    for item in paragraph:
                        if item.get("tag") == "text":
                            texts.append(item.get("text", ""))
                return "\n".join(texts)
            else:
                # 尝试直接返回整个 JSON（如果是其他类型）
                return str(content_json)
        except json.JSONDecodeError:
            # 如果不是 JSON，直接返回原始字符串
            return content_raw
        except Exception as e:
            print(f"解析消息内容失败：{e}")
            return content_raw
    
    def save_task(self, message):
        """将消息保存为任务文件"""
        timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
        task_file = TASK_OUTPUT_DIR / f"task_{timestamp}.json"
        
        # 增强提取逻辑
        content = self.parse_message_content(message)
        
        # 获取 raw content 用于调试
        body = message.get("body", {})
        content_raw = body.get("content", "")
        if not content_raw:
            content_raw = message.get("content", "")
            
        # 如果提取失败，尝试回退到 content_raw
        if not content or not content.strip():
            print(f"⚠️ 解析内容为空，回退到原始内容: {content_raw}")
            content = content_raw
            
        task_data = {
            "message_id": message.get("message_id"),
            "sender_id": message.get("sender", {}).get("sender_id", {}).get("user_id", "unknown"), # 修正 sender_id 提取，注意层级
            "sender_type": message.get("sender", {}).get("sender_type", "unknown"), 
            "msg_type": message.get("msg_type", "text"),
            "content": content,
            "content_raw": content_raw,
            "create_time": message.get("create_time"),
            "chat_id": message.get("chat_id"),
            "received_at": datetime.now().isoformat(),
            "status": "pending"
        }
        
        # 二次检查 sender_id，有些 API 版本可能直接在 sender 下
        if task_data["sender_id"] == "unknown":
             task_data["sender_id"] = message.get("sender", {}).get("id", "unknown")
        
        with open(task_file, 'w', encoding='utf-8') as f:
            json.dump(task_data, f, ensure_ascii=False, indent=2)
        
        return task_file, task_data
    
    def check_new_messages(self):
        """检查新消息 - 只读取群里其他人发送的消息，忽略 bot 自己的消息"""
        messages = self.api.get_messages(TARGET_CHAT_ID)
        if not messages:
            return None, "未找到消息"
        
        # 修正：ByCreateTimeDesc 返回的是 [Newest, ..., Oldest]
        # 但是！如果 API 返回的是一页数据，通常分页逻辑是从最新往前翻。
        # 如果我们只处理第一条，那万一用户一次发了两条，第二条（稍旧一点的）就会被漏掉吗？
        # 不会，因为下次轮询时，最新的那条已经被标记为 processed 了。
        # 所以我们只需要找到 "最新的、未处理的" 消息。
        
        # 遍历消息（从最新的开始）
        # 新增逻辑：如果是首次启动（或缓存丢失），为了防止处理积压的历史消息，
        # 我们只处理最新的一条，并把其余的都标记为已处理。
        is_first_run = len(self.processed_messages) == 0
        
        for i, message in enumerate(messages):
            sender = message.get("sender", {})
            sender_type = sender.get("sender_type", "")
            
            # 跳过 app 发送的消息（包括自己）
            if sender_type == "app":
                continue
            
            message_id = message.get("message_id")
            
            # 检查是否已处理
            if message_id in self.processed_messages:
                # 遇到已处理的消息，说明这条以及更旧的都处理过了，直接停止
                return None, "无新消息"
            
            # 这是一个新消息！
            
            # 如果是首次运行且不是最新的那一条（i > 0），则直接跳过并标记为已处理
            # 注意：messages[0] 是最新的。
            # 但是这里有一个问题：如果我们只处理 messages[0]，那如果用户刚才连发了两条怎么办？
            # 比如 A(old), B(new)。首次启动，我们处理 B。A 就被漏掉了。
            # 这通常是可以接受的，因为“启动前的历史消息”通常不需要再执行。
            # 但为了严谨，我们可以在这里做一个判断：只处理 i==0 的那条（如果 is_first_run）
            
            if is_first_run and i > 0:
                # 标记为已处理，但不执行
                self.processed_messages.add(message_id)
                continue

            self.last_message_id = message_id
            self.processed_messages.add(message_id)
            self.save_cache()
            
            task_file, task_data = self.save_task(message)
            
            # 如果是首次运行，处理完第一条（最新的）后，应该把剩下的旧消息也标记一下，防止下次启动又处理？
            # 不需要，因为下次启动会有缓存。
            # 但是，如果本次循环继续，就会处理第二新的消息。
            # 如果我们想“只处理最新的一条”，那么在这里 return 即可。
            if is_first_run:
                # 首次运行，只处理最新的一条，后续的旧消息在下一次循环或者上面的 i>0 逻辑中会被标记
                # 但为了确保 processed_messages 完整，我们最好不要直接 return，而是让循环继续跑完（标记 i>0 的）
                # 不过 save_task 已经返回了，外部逻辑只支持返回一个 task。
                # 所以我们必须 return。
                # 那么，剩下的旧消息怎么办？
                # 我们应该在 return 前，把剩余的所有消息 ID 都加入 processed_messages
                for old_msg in messages[i+1:]:
                     self.processed_messages.add(old_msg.get("message_id"))
                self.save_cache()
                return message, f"新任务已保存（首次启动，忽略旧消息）：{task_file}"
            
            return message, f"新任务已保存：{task_file}"
        
        return None, "无新消息（或全是 bot 消息）"


# ==================== 任务执行器 ====================
class TaskExecutor:
    """任务执行器"""
    
    def __init__(self, api, listener, log_callback=None):
        self.api = api
        self.listener = listener
        self.log_callback = log_callback
        self.executed_tasks = set()
    
    def log(self, message, level="info"):
        """日志输出"""
        if self.log_callback:
            # 检查 callback 是否接受 level 参数
            try:
                self.log_callback(message, level)
            except TypeError:
                self.log_callback(message)
        else:
            print(f"[{datetime.now().strftime('%H:%M:%S')}] [{level.upper()}] {message}")
    
    def execute_task(self, task_file):
        """执行单个任务"""
        try:
            # 再次检查文件是否存在（可能被重命名了）
            if not task_file.exists():
                return False
                
            with open(task_file, 'r', encoding='utf-8') as f:
                task_data = json.load(f)
            
            task_id = task_data.get("message_id")
            content = task_data.get("content", "")
            
            # 检查 task_id 是否有效
            if not task_id:
                self.log("⚠️ 任务 ID 为空，跳过")
                # 标记为无效
                self.archive_task(task_file, "invalid")
                return False
            
            if task_id in self.executed_tasks:
                # 已经执行过，直接归档
                self.archive_task(task_file, "duplicate")
                return False
            
            if task_data.get("status") in ["completed", "failed"]:
                self.executed_tasks.add(task_id)
                self.archive_task(task_file, task_data.get("status"))
                return False
            
            # 如果是正在执行中，跳过
            if task_data.get("status") == "executing":
                return False
            
            self.log(f"\n{'='*50}")
            self.log(f"📋 执行任务：{task_id[:10]}...")
            self.log(f"📝 内容：{content[:100] if content else '无内容'}...")
            self.log(f"{'='*50}")
            
            # 更新状态为执行中
            if task_data.get("status") == "pending":
                confirm_msg = {"text": f"✅ 已收到任务，正在执行...\n\n任务内容：{content[:100]}"}
                self.api.send_message(TARGET_CHAT_ID, confirm_msg)
                
                task_data["status"] = "executing"
                with open(task_file, 'w', encoding='utf-8') as f:
                    json.dump(task_data, f, ensure_ascii=False, indent=2)
            
            # 调用后端
            result = self.run_task(content)
            
            # 记录 Agent 回复到 GUI 日志
            self.log(f"🤖 Agent 回复:\n{result[:200]}...", "info")
            
            task_data["status"] = "completed"
            task_data["result"] = result
            
            # 移除回复中的 <thinking>...</thinking> 内容
            clean_result = re.sub(r'<thinking>.*?</thinking>', '', result, flags=re.DOTALL).strip()
            
            result_msg = {"text": f"✅ 任务执行完成\n\n执行结果：\n{clean_result[:1500]}"}
            self.api.send_message(TARGET_CHAT_ID, result_msg)
            
            self.executed_tasks.add(task_id)
            
            # 保存结果并归档任务文件
            result_file = RESULT_OUTPUT_DIR / f"result_{task_id}.json"
            with open(result_file, 'w', encoding='utf-8') as f:
                json.dump({"task": task_data, "result": result}, f, ensure_ascii=False, indent=2)
            
            # 归档任务文件（重命名为 .done）
            self.archive_task(task_file, "completed")
            
            return True
            
        except Exception as e:
            error_msg = f"❌ 任务执行失败：{str(e)}"
            self.log(error_msg, "error")
            try:
                # 尝试更新状态
                if task_file.exists():
                    with open(task_file, 'r', encoding='utf-8') as f:
                        task_data = json.load(f)
                    if task_data.get("status") != "failed":
                        self.api.send_message(TARGET_CHAT_ID, {"text": error_msg})
                        task_data["status"] = "failed"
                        task_data["error"] = str(e)
                        with open(task_file, 'w', encoding='utf-8') as f:
                            json.dump(task_data, f, ensure_ascii=False, indent=2)
                    
                    # 失败的任务也归档，避免死循环
                    self.archive_task(task_file, "failed")
            except:
                pass
            return False

    def archive_task(self, task_file, status):
        """归档任务文件"""
        try:
            if not task_file.exists():
                return
            
            # 方案：移动到 archived 子目录，保持目录整洁
            archive_dir = TASK_OUTPUT_DIR / "archived"
            archive_dir.mkdir(exist_ok=True)
            
            # 提取基础文件名 (task_YYYYMMDD_HHMMSS)
            # 假设文件名以 task_ 开头，且第一个点之前的部分是基础名
            base_name = task_file.name.split('.')[0]
            
            # 构造新路径
            new_path = archive_dir / f"{base_name}.{status}.json"
            
            # 移动文件 (replace 会覆盖已存在的目标文件)
            # 注意：如果源文件路径过长，这里可能还是会报错，但我们无法处理已损坏的文件
            # 只能确保新产生的文件不会这样
            import shutil
            shutil.move(str(task_file), str(new_path))
            
        except Exception as e:
            self.log(f"归档任务失败: {e}", "error")

    def call_auramate_backend(self, user_message):
        """调用 XiaoYing 后端 API"""
        url = AURAMATE_API_URL
        
        # 确保 user_message 不为空
        if not user_message or not str(user_message).strip():
            return "❌ 错误：消息内容为空，无法调用后端。"
            
        # 构造符合 OpenAI 标准的 Payload
        payload = {
            "model": "auramate",
            "messages": [
                {"role": "user", "content": str(user_message)}
            ],
            "stream": False
        }
        
        # 使用 json.dumps 显式序列化，确保格式正确 (ensure_ascii=False 以支持中文)
        data = json.dumps(payload, ensure_ascii=False)
        headers = {"Content-Type": "application/json"}
        
        try:
            self.log(f"📡 调用 XiaoYing 后端：{url}")
            # self.log(f"📤 Payload: {data[:200]}...") # Debug log
            
            # 使用 data 参数发送原始 JSON 字符串，与成功示例保持一致
            # 注意：requests 会自动计算 Content-Length
            response = requests.post(url, data=data.encode('utf-8'), headers=headers, timeout=300)
            
            if response.status_code == 200:
                result_data = response.json()
                if "choices" in result_data and len(result_data["choices"]) > 0:
                    return result_data["choices"][0]["message"]["content"]
                elif "content" in result_data:
                    return result_data["content"]
                else:
                    return f"后端响应格式异常：{json.dumps(result_data, ensure_ascii=False)[:500]}"
            else:
                return f"后端调用失败，状态码：{response.status_code}\n响应：{response.text[:500]}"
                
        except requests.exceptions.Timeout:
            return "⏱ 后端调用超时（>5 分钟）"
        except requests.exceptions.ConnectionError:
            return "❌ 无法连接到 XiaoYing 后端，请确认后端服务已启动（localhost:3000）"
        except Exception as e:
            return f"❌ 调用后端时出错：{str(e)}"
    
    def run_task(self, content):
        """执行任务"""
        self.log("🔄 正在调用 XiaoYing 后端处理任务...")
        result = self.call_auramate_backend(content)
        return result
    
    def monitor_tasks(self):
        """监控任务目录"""
        task_files = list(TASK_OUTPUT_DIR.glob("task_*.json"))
        executed = False
        
        # 编译正则，只匹配标准格式：task_20240101_120000.json
        # 防止任何其他后缀的文件被处理
        pattern = re.compile(r"^task_\d{8}_\d{6}\.json$")
        
        for task_file in task_files:
            # 严格过滤：文件名必须完全符合标准格式
            if not pattern.match(task_file.name):
                # self.log(f"跳过非标准文件：{task_file.name}", "debug")
                continue

            if task_file.stem not in [f"task_{t}" for t in self.executed_tasks]:
                self.log(f"发现新任务：{task_file.name}")
                if self.execute_task(task_file):
                    executed = True
        
        return executed


# ==================== GUI 主程序 ====================
class FeishuTaskGUI:
    """飞书任务监听器 GUI"""
    
    def __init__(self, root):
        self.root = root
        self.root.title("📬 飞书任务监听器 - XiaoYing")
        self.root.geometry("800x650")
        self.root.minsize(700, 500)
        
        # 设置图标（如果有的话）
        try:
            self.root.iconbitmap(default='')
        except:
            pass
        
        self.api = None
        self.listener = None
        self.executor = None
        self.is_running = False
        self.listen_thread = None
        self.check_interval = 5
        
        # UI 变量
        self.app_id_var = tk.StringVar()
        self.app_secret_var = tk.StringVar()
        
        self.setup_ui()
        
        # 尝试加载配置
        self.app_id_var.set(APP_ID)
        self.app_secret_var.set(APP_SECRET)
        
        if APP_ID and APP_SECRET:
            self.init_backend()
        else:
            self.log("⚠️ 请输入飞书应用凭证并保存", "warning")

    def init_backend(self):
        """初始化后端"""
        try:
            self.api = FeishuAPI(APP_ID, APP_SECRET)
            self.listener = TaskListener(self.api)
            self.load_groups()
            self.log("应用程序已启动 (后端已连接)", "info")
        except Exception as e:
            self.log(f"初始化失败: {e}", "error")

    def save_credentials(self):
        """保存凭证"""
        new_app_id = self.app_id_var.get().strip()
        new_app_secret = self.app_secret_var.get().strip()
        
        if not new_app_id or not new_app_secret:
            messagebox.showerror("错误", "App ID 和 App Secret 不能为空")
            return
            
        global APP_ID, APP_SECRET
        APP_ID = new_app_id
        APP_SECRET = new_app_secret
        
        # 保存到文件
        config = load_config()
        config["app_id"] = APP_ID
        config["app_secret"] = APP_SECRET
        save_config(config)
        
        self.log("✅ 配置已保存", "success")
        self.init_backend()
    
    def setup_ui(self):
        """设置界面"""
        # 0. 配置区
        config_frame = ttk.LabelFrame(self.root, text="飞书配置", padding="5")
        config_frame.grid(row=0, column=0, sticky=(tk.W, tk.E), padx=10, pady=(10, 0))
        
        ttk.Label(config_frame, text="App ID:").pack(side=tk.LEFT, padx=5)
        ttk.Entry(config_frame, textvariable=self.app_id_var, width=20).pack(side=tk.LEFT, padx=5)
        
        ttk.Label(config_frame, text="Secret:").pack(side=tk.LEFT, padx=5)
        ttk.Entry(config_frame, textvariable=self.app_secret_var, width=20, show="*").pack(side=tk.LEFT, padx=5)
        
        ttk.Button(config_frame, text="💾 保存", command=self.save_credentials).pack(side=tk.LEFT, padx=5)

        # 主框架
        main_frame = ttk.Frame(self.root, padding="10")
        main_frame.grid(row=1, column=0, sticky=(tk.W, tk.E, tk.N, tk.S))
        self.root.columnconfigure(0, weight=1)
        self.root.rowconfigure(1, weight=1)
        
        # 标题
        title_frame = ttk.Frame(main_frame)
        title_frame.grid(row=0, column=0, sticky=(tk.W, tk.E), pady=(0, 10))
        
        title_label = ttk.Label(
            title_frame,
            text="📬 飞书任务监听器",
            font=("Microsoft YaHei", 18, "bold")
        )
        title_label.pack(side=tk.LEFT)
        
        subtitle_label = ttk.Label(
            title_frame,
            text="| 技术研发群 • XiaoYing 双向通信",
            font=("Microsoft YaHei", 10),
            foreground="gray"
        )
        subtitle_label.pack(side=tk.LEFT, padx=(10, 0), pady=(5, 0))
        
        # 状态面板
        status_frame = ttk.LabelFrame(main_frame, text="📊 状态", padding="10")
        status_frame.grid(row=1, column=0, sticky=(tk.W, tk.E), pady=(0, 10))
        
        self.status_indicator = ttk.Label(status_frame, text="● 已停止", font=("Microsoft YaHei", 11, "bold"), foreground="gray")
        self.status_indicator.grid(row=0, column=0, sticky=tk.W)
        
        self.last_check_label = ttk.Label(status_frame, text="上次检查：从未", foreground="gray")
        self.last_check_label.grid(row=0, column=1, padx=(20, 0))
        
        self.task_count_label = ttk.Label(status_frame, text="已处理任务：0", foreground="gray")
        self.task_count_label.grid(row=0, column=2, padx=(20, 0))
        
        # 控制按钮
        btn_frame = ttk.Frame(main_frame)
        btn_frame.grid(row=2, column=0, sticky=(tk.W, tk.E), pady=(0, 10))
        
        ttk.Label(btn_frame, text="监听群组:").pack(side=tk.LEFT, padx=(0, 5))
        
        self.group_combo = ttk.Combobox(btn_frame, state="readonly", width=20)
        self.group_combo.pack(side=tk.LEFT, padx=(0, 5))
        self.group_combo.bind("<<ComboboxSelected>>", self.on_group_selected)
        
        ttk.Button(btn_frame, text="🔄", width=3, command=self.load_groups).pack(side=tk.LEFT, padx=(0, 10))
        
        self.start_btn = ttk.Button(btn_frame, text="▶ 开始监听", command=self.toggle_listening, width=15)
        self.start_btn.pack(side=tk.LEFT, padx=(0, 5))
        
        self.check_now_btn = ttk.Button(btn_frame, text="🔄 立即检查", command=self.check_now, width=15)
        self.check_now_btn.pack(side=tk.LEFT, padx=(0, 5))
        
        self.clear_cache_btn = ttk.Button(btn_frame, text="🗑 清空缓存", command=self.clear_cache, width=15)
        self.clear_cache_btn.pack(side=tk.LEFT, padx=(0, 5))
        
        self.open_task_dir_btn = ttk.Button(btn_frame, text="📁 任务目录", command=self.open_task_dir, width=15)
        self.open_task_dir_btn.pack(side=tk.LEFT, padx=(0, 5))
        
        self.open_result_dir_btn = ttk.Button(btn_frame, text="📁 结果目录", command=self.open_result_dir, width=15)
        self.open_result_dir_btn.pack(side=tk.LEFT)
        
        # 日志区域
        log_frame = ttk.LabelFrame(main_frame, text="📝 运行日志", padding="10")
        log_frame.grid(row=3, column=0, sticky=(tk.W, tk.E, tk.N, tk.S), pady=(0, 10))
        main_frame.rowconfigure(3, weight=1)
        main_frame.columnconfigure(0, weight=1)
        
        self.log_text = scrolledtext.ScrolledText(log_frame, height=20, wrap=tk.WORD, font=("Consolas", 9))
        self.log_text.pack(fill=tk.BOTH, expand=True)
        
        # 配置日志标签颜色
        self.log_text.tag_config("info", foreground="black")
        self.log_text.tag_config("success", foreground="green")
        self.log_text.tag_config("warning", foreground="orange")
        self.log_text.tag_config("error", foreground="red")
        self.log_text.tag_config("debug", foreground="gray")
        
        # 底部信息
        info_frame = ttk.Frame(main_frame)
        info_frame.grid(row=4, column=0, sticky=(tk.W, tk.E))
        
        config_text = f"App ID: {APP_ID}  |  群聊 ID: {TARGET_CHAT_ID[:30]}...  |  检查间隔：{self.check_interval}秒"
        info_label = ttk.Label(info_frame, text=config_text, font=("Microsoft YaHei", 8), foreground="gray")
        info_label.pack(side=tk.LEFT)
    
    def log(self, message, level="info"):
        """添加日志"""
        timestamp = datetime.now().strftime("%H:%M:%S")
        log_line = f"[{timestamp}] {message}\n"
        
        self.log_text.insert(tk.END, log_line, level)
        self.log_text.see(tk.END)
    
    def toggle_listening(self):
        """切换监听状态"""
        if self.is_running:
            self.stop_listening()
        else:
            self.start_listening()
    
    def load_groups(self):
        """加载群组列表"""
        self.log("正在获取群组列表...", "info")
        groups = self.api.get_groups()
        if not groups:
            self.log("❌ 未获取到任何群组，请确保机器人已加入群聊", "error")
            return
            
        self.groups_map = {g["name"]: g["chat_id"] for g in groups}
        group_names = list(self.groups_map.keys())
        self.group_combo["values"] = group_names
        
        if group_names:
            self.group_combo.current(0)
            self.on_group_selected(None)
            self.log(f"✅ 获取到 {len(groups)} 个群组", "success")
        else:
            self.log("⚠️ 机器人未加入任何群组", "warning")

    def on_group_selected(self, event):
        """选择群组回调"""
        name = self.group_combo.get()
        chat_id = self.groups_map.get(name)
        if chat_id:
            global TARGET_CHAT_ID
            TARGET_CHAT_ID = chat_id
            
            # 自动保存
            config = load_config()
            config["target_chat_id"] = TARGET_CHAT_ID
            save_config(config)
            
            self.log(f"已切换监听群组: {name} ({chat_id})", "info")

    def start_listening(self):
        """开始监听"""
        if not TARGET_CHAT_ID:
            messagebox.showerror("错误", "请先选择一个群组")
            return
            
        self.executor = TaskExecutor(self.api, self.listener, log_callback=self.log)
        self.is_running = True
        self.start_btn.config(text="⏹ 停止监听")
        self.status_indicator.config(text="● 运行中", foreground="green")
        self.log("开始监听飞书消息...", "success")
        
        self.listen_thread = threading.Thread(target=self.listen_loop, daemon=True)
        self.listen_thread.start()
    
    def stop_listening(self):
        """停止监听"""
        self.is_running = False
        self.start_btn.config(text="▶ 开始监听")
        self.status_indicator.config(text="● 已停止", foreground="gray")
        self.log("监听已停止", "warning")
    
    def listen_loop(self):
        """监听循环"""
        while self.is_running:
            try:
                message, status = self.listener.check_new_messages()
                
                if message:
                    sender = message.get('sender_id', '未知')
                    content = message.get('content', '')
                    self.log(f"✅ 收到新任务！发送者：{sender}", "success")
                    self.log(f"   内容：{content[:80]}...")
                    
                    self.executor.monitor_tasks()
                else:
                    self.executor.monitor_tasks()
                
                # 更新 UI
                task_count = len(self.executor.executed_tasks)
                self.task_count_label.config(text=f"已处理任务：{task_count}")
                self.last_check_label.config(text=f"上次检查：{datetime.now().strftime('%H:%M:%S')}")
                
            except Exception as e:
                self.log(f"❌ 错误：{e}", "error")
                time.sleep(10)
            
            time.sleep(self.check_interval)
    
    def check_now(self):
        """立即检查"""
        def check():
            try:
                self.log("正在检查新消息...", "info")
                message, status = self.listener.check_new_messages()
                
                if message:
                    sender = message.get('sender_id', '未知')
                    content = message.get('content', '')
                    self.log(f"✅ 收到新任务！发送者：{sender}", "success")
                    self.log(f"   完整内容：{content}", "info")
                    
                    if self.executor:
                        self.executor.monitor_tasks()
                    
                    messagebox.showinfo("新任务", f"收到新任务：\n\n{content[:300]}")
                else:
                    self.log("暂无新消息", "info")
                    messagebox.showinfo("检查完成", "暂无新消息")
                
                self.last_check_label.config(text=f"上次检查：{datetime.now().strftime('%H:%M:%S')}")
                
            except Exception as e:
                self.log(f"❌ 错误：{e}", "error")
                messagebox.showerror("错误", str(e))
        
        threading.Thread(target=check, daemon=True).start()
    
    def clear_cache(self):
        """清空缓存"""
        if messagebox.askyesno("确认", "确定要清空消息缓存吗？\n这将导致重新处理所有消息。"):
            try:
                if CACHE_FILE.exists():
                    CACHE_FILE.unlink()
                self.listener.processed_messages = set()
                self.listener.last_message_id = None
                self.listener.last_message_hash = None
                self.log("缓存已清空", "success")
                messagebox.showinfo("完成", "缓存已清空")
            except Exception as e:
                self.log(f"清空缓存失败：{e}", "error")
                messagebox.showerror("错误", str(e))
    
    def open_task_dir(self):
        """打开任务目录"""
        os.startfile(str(TASK_OUTPUT_DIR))
    
    def open_result_dir(self):
        """打开结果目录"""
        os.startfile(str(RESULT_OUTPUT_DIR))
    
    def on_closing(self):
        """窗口关闭事件"""
        if self.is_running:
            if messagebox.askokcancel("退出", "监听正在运行，确定要退出吗？"):
                self.stop_listening()
                self.root.destroy()
        else:
            self.root.destroy()


if __name__ == "__main__":
    # 如果是直接运行，启动 GUI
    # 注意：之前的测试代码在这里，但现在我们要启动主界面
    
    # 检查是否是测试模式（例如命令行参数）
    if len(sys.argv) > 1 and sys.argv[1] == "--test":
        print("Running in Test Mode...")
        # ... (保留之前的测试逻辑用于调试)
        if not APP_ID or not APP_SECRET:
             print("❌ 错误：请先在 feishu_config.json 中配置 app_id 和 app_secret")
             sys.exit(1)
        
        api = FeishuAPI(APP_ID, APP_SECRET)
        listener = TaskListener(api)
        executor = TaskExecutor(api, listener)
        # ...
    else:
        # 启动 GUI
        root = tk.Tk()
        app = FeishuTaskGUI(root)
        root.mainloop()
