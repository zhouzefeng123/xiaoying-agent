---
name: feishu-messenger
description: 使用飞书/Lark 开放平台 API 接收和发送消息。
language: python
script_path: feishu_messenger.py
parameters:
  action:
    type: string
    description: "操作类型：'send' (发送), 'list' (列出消息), 'get' (获取消息), 'list_chats' (列出群组), 'download_files' (下载文件)。"
    required: true
  receive_id_type:
    type: string
    description: "接收者 ID 类型 ('open_id', 'user_id', 'union_id', 'email', 'chat_id')。默认 'open_id'。"
    required: false
  receive_id:
    type: string
    description: "接收者 ID。"
    required: false
  msg_type:
    type: string
    description: "消息类型 ('text', 'post', 'image', 'file')。默认 'text'。"
    required: false
  content:
    type: string
    description: "消息内容（文本或 JSON 字符串）。"
    required: false
  container_id_type:
    type: string
    description: "容器类型 ('chat')。仅 'list' 和 'download_files' 需要。"
    required: false
  container_id:
    type: string
    description: "容器 ID（如 chat_id）。仅 'list' 和 'download_files' 需要。"
    required: false
  message_id:
    type: string
    description: "消息 ID。仅 'get' 需要。"
    required: false
  save_dir:
    type: string
    description: "文件保存目录（相对于 workspace）。仅 'download_files' 需要。"
    required: false
---

# Feishu Messenger

Sends messages using Feishu/Lark Open API.

## Parameters

- `app_id` (string): Feishu App ID. (Optional if configured in config.json)
- `app_secret` (string): Feishu App Secret. (Optional if configured in config.json)
- `action` (string): Action to perform (`send`, `list`, `get`). Default: `send`.

### Send Message (`action="send"`)
- `receive_id_type` (string): Type of receiver ID (open_id, user_id, union_id, email, chat_id). Default: open_id.
- `receive_id` (string): The ID of the receiver.
- `msg_type` (string): Message type (text, post, image, file, etc.). Default: text.
- `content` (string): JSON string or text content of the message.

### List Messages (`action="list"`)
- `container_id_type` (string): Container type (`chat`, `thread`). Default: chat.
- `container_id` (string): Container ID (e.g., `chat_id`).
- `page_size` (int): Number of messages to return. Default: 20. (Use `--page_size` via CLI if supported, or modify code)

### Get Message (`action="get"`)
- `message_id` (string): The ID of the message to retrieve.

### List Chats (`action="list_chats"`)
Lists the chats/groups the bot is a member of. Useful for finding `chat_id`.

### Download Files (`action="download_files"`)
Downloads all images and files from the recent messages of a chat.
- `container_id` (string): Chat ID.
- `save_dir` (string): Directory name under workspace to save files. Default: `downloads`.

## Configuration

To use this skill, you must configure your Feishu/Lark App credentials.

1.  **File Location**: `config.json` in the same directory as this script (`auramate/backend/system_skills/feishu-messenger/config.json`).
2.  **File Format**:

```json
{
  "app_id": "cli_xxx",
  "app_secret": "xxx"
}
```

### Auto-Configuration (Agent Instructions)
If the `config.json` is missing, empty, or lacks `app_id`/`app_secret`, or if you encounter an authentication error:
1.  **MUST** ask the user for their Feishu/Lark `App ID` and `App Secret`.
2.  Once provided, use the `Write` tool to save them to `backend/system_skills/feishu-messenger/config.json`.
3.  Retry the operation.

> **Note**: You can also pass `app_id` and `app_secret` as command line arguments, but using `config.json` is recommended for security and convenience.

## Usage

**Send Message:**
```bash
python feishu_messenger.py --receive_id ou_xxx --content "Hello World"
```

**List Messages (History):**
```bash
python feishu_messenger.py --action list --container_id oc_xxx
```

**Get Message Detail:**
```bash
python feishu_messenger.py --action get --message_id om_xxx
```
