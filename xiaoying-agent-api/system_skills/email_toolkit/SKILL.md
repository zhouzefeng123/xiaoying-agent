---
name: email-toolkit
description: 统一邮箱收发工具：支持 SMTP 发信（含附件）与 POP3 收信（自动解码、正文提取、附件检测）
language: python
script_path: email_toolkit.py
parameters:
  action:
    type: string
    description: "操作类型：'send' (发送) 或 'fetch' (接收)。"
    required: true
  to:
    type: string
    description: "收件人邮箱地址（仅发送时必需）。"
    required: false
  subject:
    type: string
    description: "邮件主题（发送时可选）。"
    required: false
  body:
    type: string
    description: "邮件正文内容（发送时可选）。"
    required: false
  attachments:
    type: array
    description: "附件文件路径列表（发送时可选）。"
    required: false
  limit:
    type: integer
    description: "接收邮件数量限制（仅接收时可选，默认 1）。"
    required: false
---

# Email Toolkit

统一的邮件处理工具，支持发送和接收邮件。

## 参数

- `action` (required): 操作类型，'send' (发送) 或 'fetch' (接收)。
- `to` (required for send): 收件人邮箱地址。
- `subject` (optional): 邮件主题。
- `body` (optional): 邮件正文内容。
- `attachments` (optional): 附件文件路径列表。
- `limit` (optional for fetch): 接收邮件数量限制，默认 1。

## 配置
工具会优先读取环境变量 `EMAIL_CONFIG`，如果不存在则读取同目录下的 `config.json`。

`config.json` 格式：
```json
{
  "user": "example@test.com",
  "app_password": "your_app_password",
  "smtp_server": "smtp.example.com",
  "smtp_port": 465,
  "pop_server": "pop.example.com",
  "pop_port": 995
}
```

### 自动配置（Agent 指引）
如果 `config.json` 文件内容为空或缺少配置项，或者在执行时遇到认证错误：
1. **必须** 询问用户提供邮箱配置信息：
   - 邮箱地址 (`user`)
   - 应用专用密码/授权码 (`app_password`)
   - SMTP 服务器地址 (`smtp_server`) 和端口 (默认 465)
   - POP3 服务器地址 (`pop_server`) 和端口 (默认 995)
2. 获得用户输入后，使用 `Write` 工具将信息写入 `backend/system_skills/email_toolkit/config.json` 文件中。
3. 写入完成后，再次尝试执行用户的请求。

## 示例

### 发送邮件
```python
email-toolkit(action="send", to="example@test.com", subject="Hello", body="This is a test email.")
```

### 接收邮件
```python
email-toolkit(action="fetch", limit=5)
```
