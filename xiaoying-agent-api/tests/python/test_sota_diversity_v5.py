import requests
import json
import time
import os
import sys
import shutil

BASE_URL = "http://localhost:3000"
# Adjust workspace to be one level up from backend, matching the agent's likely workspace
# WORKSPACE_DIR = os.path.join(os.getcwd(), "workspace")
WORKSPACE_DIR = os.path.abspath(os.path.join(os.path.dirname(__file__), "../../../workspace"))

def print_step(message):
    print(f"\n{'='*50}\n[STEP] {message}\n{'='*50}")

def run_test_scenario(name, prompt, setup_fn=None, verify_fn=None):
    print_step(f"Running Scenario: {name}")
    
    if setup_fn:
        try:
            setup_fn()
        except Exception as e:
            print(f"[SETUP FAIL] {e}")
            return False, {}

    url = f"{BASE_URL}/v1/chat/completions"
    headers = {"Content-Type": "application/json"}
    
    data = {
        "model": "gpt-4o",
        "messages": [{"role": "user", "content": prompt}],
        "stream": True,
        "temperature": 0
    }

    print(f"Sending prompt: {prompt[:100]}...")
    start_time = time.time()
    first_token_time = None
    
    content = ""
    input_tokens = 0
    output_tokens = 0
    cached_tokens = 0
    
    try:
        response = requests.post(url, headers=headers, data=json.dumps(data), stream=True)
        response.raise_for_status()
        
        for line in response.iter_lines():
            if line:
                decoded_line = line.decode('utf-8')
                if decoded_line.startswith("data: "):
                    json_str = decoded_line[6:]
                    if json_str == "[DONE]":
                        break
                    try:
                        chunk = json.loads(json_str)
                        
                        # Extract Usage if available
                        if 'usage' in chunk and chunk['usage']:
                            usage = chunk['usage']
                            input_tokens = usage.get('prompt_tokens', 0)
                            output_tokens = usage.get('completion_tokens', 0)
                            
                            # Cache Hit Logic (DeepSeek & OpenAI Compatible)
                            if 'prompt_tokens_details' in usage and usage['prompt_tokens_details']:
                                cached_tokens = usage['prompt_tokens_details'].get('cached_tokens', 0)
                            elif 'prompt_cache_hit_tokens' in usage:
                                cached_tokens = usage.get('prompt_cache_hit_tokens', 0)
                            elif 'cached_tokens' in usage:
                                cached_tokens = usage.get('cached_tokens', 0)

                        delta = chunk['choices'][0]['delta']
                        if 'content' in delta and delta['content']:
                            c = delta['content']
                            if first_token_time is None:
                                first_token_time = time.time()
                            content += c
                            print(c, end="", flush=True)
                    except:
                        pass
        
        end_time = time.time()
        if first_token_time is None:
            first_token_time = end_time # Fallback
            
        ttft = first_token_time - start_time
        task_time = end_time - start_time
        
        print(f"\n\n[INFO] TTFT: {ttft:.4f}s | Task Time: {task_time:.4f}s")
        print(f"[INFO] Tokens: In={input_tokens}, Out={output_tokens}, Cache={cached_tokens}")
        
        metrics = {
            "ttft": ttft,
            "task_time": task_time,
            "input_tokens": input_tokens,
            "output_tokens": output_tokens,
            "cached_tokens": cached_tokens
        }
        
        if verify_fn:
            verify_fn(content)
            print(f"[PASS] Scenario '{name}' Verified")
        else:
            print(f"[WARN] No verification function provided for '{name}'")
        
    except Exception as e:
        print(f"\n[FAIL] Scenario '{name}' Failed: {e}")
        return False, {}
    
    return True, metrics

# --- Verification Functions ---

def verify_system(content):
    f = os.path.join(WORKSPACE_DIR, "system_monitor.csv")
    if not os.path.exists(f):
        raise Exception(f"File {f} not found")
    with open(f, 'r') as file:
        lines = file.readlines()
        if len(lines) < 2: # Header + at least 1 row
            raise Exception("CSV file seems empty or malformed")

def verify_archive(content):
    archive = os.path.join(WORKSPACE_DIR, "Backup", "archive.zip")
    if not os.path.exists(archive):
        # Check if in root workspace
        archive_root = os.path.join(WORKSPACE_DIR, "archive.zip")
        if os.path.exists(archive_root):
            print("[WARN] Archive found in workspace root, not in Backup folder")
            return
        raise Exception(f"Archive {archive} not found")

def verify_web_auto(content):
    f = os.path.join(WORKSPACE_DIR, "python_latest.txt")
    if not os.path.exists(f):
        raise Exception(f"File {f} not found")
    
    # Try reading with different encodings to handle potential BOMs from PowerShell
    content_read = ""
    try:
        with open(f, 'r', encoding='utf-8') as file:
            content_read = file.read()
    except UnicodeDecodeError:
        try:
            with open(f, 'r', encoding='utf-16') as file:
                content_read = file.read()
        except UnicodeDecodeError:
            with open(f, 'r', encoding='gbk', errors='ignore') as file:
                content_read = file.read()

    if "Python" not in content_read and "3." not in content_read:
        raise Exception(f"Content does not look like Python version info: {content_read[:100]}")

def verify_finance(content):
    f = os.path.join(WORKSPACE_DIR, "currency_conversion.txt")
    if not os.path.exists(f):
        raise Exception(f"File {f} not found")
    c = ""
    for enc in ("utf-8", "utf-16", "gbk"):
        try:
            with open(f, 'r', encoding=enc) as file:
                c = file.read()
            break
        except UnicodeDecodeError:
            continue
    if not c:
        raise Exception("Failed to decode currency_conversion.txt")
    if "JPY" not in c and "USD" not in c:
        raise Exception("Content seems irrelevant")

def verify_parallel_network(content):
    a = os.path.join(WORKSPACE_DIR, "demo_net_a.py")
    b = os.path.join(WORKSPACE_DIR, "demo_net_b.py")
    if not os.path.exists(a):
        raise Exception(f"File {a} not found")
    if not os.path.exists(b):
        raise Exception(f"File {b} not found")
    with open(a, "r", encoding="utf-8", errors="ignore") as fa:
        ca = fa.read()
    with open(b, "r", encoding="utf-8", errors="ignore") as fb:
        cb = fb.read()
    if "print" not in ca:
        raise Exception("demo_net_a.py content mismatch")
    if "print" not in cb:
        raise Exception("demo_net_b.py content mismatch")

def verify_parallel_failure_fallback(content):
    a = os.path.join(WORKSPACE_DIR, "demo_fail_a.py")
    b = os.path.join(WORKSPACE_DIR, "demo_fail_b.py")
    if not os.path.exists(a):
        raise Exception(f"File {a} not found")
    if not os.path.exists(b):
        raise Exception(f"File {b} not found")
    with open(a, "r", encoding="utf-8", errors="ignore") as fa:
        ca = fa.read()
    with open(b, "r", encoding="utf-8", errors="ignore") as fb:
        cb = fb.read()
    if "print" not in ca:
        raise Exception("demo_fail_a.py content mismatch")
    if "print" not in cb:
        raise Exception("demo_fail_b.py content mismatch")
    fallback_signals = ["failed", "error", "timeout", "network", "失败", "连接"]
    lowered = (content or "").lower()
    if not any(s in lowered for s in fallback_signals):
        print("[WARN] Response does not explicitly contain failure signal, but fallback artifacts exist.")

# --- Setup Functions ---

def setup_system():
    p = os.path.join(WORKSPACE_DIR, "system_monitor.csv")
    if os.path.exists(p):
        os.remove(p)

def setup_archive():
    # Create dummy files
    dummy_dir = os.path.join(WORKSPACE_DIR, "dummy_files")
    if os.path.exists(dummy_dir):
        shutil.rmtree(dummy_dir)
    os.makedirs(dummy_dir)
    for i in range(5):
        with open(os.path.join(dummy_dir, f"test_{i}.txt"), "w") as f:
            f.write(f"This is test file {i}")
    
    backup_dir = os.path.join(WORKSPACE_DIR, "Backup")
    if os.path.exists(backup_dir):
        shutil.rmtree(backup_dir)

def setup_web_auto():
    p = os.path.join(WORKSPACE_DIR, "python_latest.txt")
    if os.path.exists(p):
        os.remove(p)

def setup_finance():
    for name in ("currency_conversion.txt", "currency_script.py"):
        p = os.path.join(WORKSPACE_DIR, name)
        if os.path.exists(p):
            os.remove(p)

def setup_parallel_network():
    for name in ("demo_net_a.py", "demo_net_b.py"):
        p = os.path.join(WORKSPACE_DIR, name)
        if os.path.exists(p):
            os.remove(p)

def setup_parallel_failure_fallback():
    for name in ("demo_fail_a.py", "demo_fail_b.py"):
        p = os.path.join(WORKSPACE_DIR, name)
        if os.path.exists(p):
            os.remove(p)

def main():
    if not os.path.exists(WORKSPACE_DIR):
        os.makedirs(WORKSPACE_DIR)
        
    results = {}
    metrics_data = {}

    # Scenario 0: Parallel Sub-agent with Network Available
    status, metric = run_test_scenario(
        "Parallel Network",
        "请使用 batch_delegate_task 并行启动两个子代理并都依赖网络：任务1访问 https://www.python.org/ 提取页面标题后创建 demo_net_a.py（包含 print 语句）；任务2访问 https://open.er-api.com/v6/latest/USD 提取 USD/JPY 后创建 demo_net_b.py（包含 print 语句）。然后调用 wait_for_workers(wait_mode='all_finished')。若任一子代理失败，必须同轮直接用 write 工具补齐缺失文件。最终输出简短总结。",
        setup_fn=setup_parallel_network,
        verify_fn=verify_parallel_network
    )
    results["ParallelNetwork"] = status
    metrics_data["ParallelNetwork"] = metric

    # Scenario 1: Parallel Sub-agent with Intentional Failure + Fallback
    status, metric = run_test_scenario(
        "Parallel Failure Fallback",
        "请使用 batch_delegate_task 并行启动两个子代理：任务1故意请求不可达地址 https://127.0.0.1:1 并尝试生成 demo_fail_a.py；任务2正常创建 demo_fail_b.py（包含 print 语句）。随后调用 wait_for_workers(wait_mode='all_finished')。如果任务1失败，禁止继续等待，必须在同一轮直接使用 write 工具创建或修复 demo_fail_a.py。最终输出包含失败原因和修复结果的简短总结。",
        setup_fn=setup_parallel_failure_fallback,
        verify_fn=verify_parallel_failure_fallback
    )
    results["ParallelFailureFallback"] = status
    metrics_data["ParallelFailureFallback"] = metric
    
    # Scenario 2: System Monitoring (Coding/System)
    status, metric = run_test_scenario(
        "System Monitor",
        "Write a Python script to monitor CPU and Memory usage every 1 second for 5 seconds. Save the data (Timestamp, CPU%, Memory%) to 'system_monitor.csv' in the workspace. Use 'psutil' library.",
        setup_fn=setup_system,
        verify_fn=verify_system
    )
    results["System"] = status
    metrics_data["System"] = metric
    
    # Scenario 3: File Archiving (File Org)
    status, metric = run_test_scenario(
        "File Archiving",
        "1. Create a folder 'dummy_files' in workspace with 5 text files (if not exists). 2. Compress all .txt files in 'dummy_files' into 'archive.zip'. 3. Create a 'Backup' folder in workspace and move 'archive.zip' there.",
        setup_fn=setup_archive,
        verify_fn=verify_archive
    )
    results["Archive"] = status
    metrics_data["Archive"] = metric
    
    # Scenario 4: Web Automation (Browser Use Verification)
    status, metric = run_test_scenario(
        "Web Automation",
        "Use browser automation to visit 'https://www.python.org/downloads/' and extract the latest Python 3 version number (e.g., 'Python 3.x.x'). Save it to 'python_latest.txt' in workspace.",
        setup_fn=setup_web_auto,
        verify_fn=verify_web_auto
    )
    results["WebAuto"] = status
    metrics_data["WebAuto"] = metric
    
    # Scenario 5: Finance Calculation (Finance/Web)
    status, metric = run_test_scenario(
        "Currency Converter",
        "Find the current exchange rate for 1 USD to JPY using web search. Calculate how much 5000 JPY is in USD. Save the result (Rate and Final Amount) to 'currency_conversion.txt' in workspace.",
        setup_fn=setup_finance,
        verify_fn=verify_finance
    )
    results["Finance"] = status
    metrics_data["Finance"] = metric
    
    print("\n" + "="*100)
    print(f"{'SCENARIO':<20} | {'STATUS':<6} | {'TTFT(s)':<8} | {'TASK(s)':<8} | {'IN TOK':<8} | {'OUT TOK':<8} | {'CACHE':<8}")
    print("-" * 100)
    
    for name, passed in results.items():
        status = "PASS" if passed else "FAIL"
        m = metrics_data.get(name, {})
        ttft = f"{m.get('ttft', 0):.2f}"
        task = f"{m.get('task_time', 0):.2f}"
        in_tok = m.get('input_tokens', 0)
        out_tok = m.get('output_tokens', 0)
        cache = m.get('cached_tokens', 0)
        
        print(f"{name:<20} | {status:<6} | {ttft:<8} | {task:<8} | {in_tok:<8} | {out_tok:<8} | {cache:<8}")
    print("="*100)

if __name__ == "__main__":
    main()
