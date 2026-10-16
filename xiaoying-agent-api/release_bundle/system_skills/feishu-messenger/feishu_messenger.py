# -*- coding: utf-8 -*-
"""
@tool feishu-messenger
@description Sends or retrieves messages using Feishu/Lark Open API.
Automatically loads credentials (app_id, app_secret) from `config.json` located in the same directory as this script (`auramate/backend/system_skills/feishu-messenger/config.json`).
@param app_id string (Optional) Feishu App ID. Defaults to value in `config.json`.
@param app_secret string (Optional) Feishu App Secret. Defaults to value in `config.json`.
@param action string (Optional) Action to perform: 'send', 'list', 'get', 'list_chats', 'download_files'. Default: 'send'.
@param receive_id_type string (Optional) [Send] Type of receiver ID: open_id, user_id, union_id, email, chat_id. Default: open_id.
@param receive_id string (Optional) [Send] The ID of the receiver. Required if action is 'send'.
@param msg_type string (Optional) [Send] Message type: text, post, image, file, etc. Default: text.
@param content string (Optional) [Send] JSON string or text content of the message. Required if action is 'send'.
@param container_id_type string (Optional) [List/Download] Container type: chat, thread. Default: chat.
@param container_id string (Optional) [List/Download] Container ID (e.g., chat_id). Required if action is 'list' or 'download_files'.
@param sort_type string (Optional) [List] Sort order: ByCreateTimeAsc, ByCreateTimeDesc. Default: ByCreateTimeDesc.
@param page_size int (Optional) [List] Number of messages per page. Default: 20.
@param message_id string (Optional) [Get] Message ID. Required if action is 'get'.
@param save_dir string (Optional) [Download] Directory to save downloaded files. Default: 'downloads'.
"""
import os
import argparse
import json
import requests
import sys

# Force UTF-8 output for JSON
sys.stdout.reconfigure(encoding='utf-8')

# Define workspace path for downloads
CURRENT_DIR = os.path.dirname(os.path.abspath(__file__))
PROJECT_ROOT = os.path.abspath(os.path.join(CURRENT_DIR, "..", "..", ".."))
WORKSPACE_ROOT = os.path.join(PROJECT_ROOT, "workspace")

def find_config_file():
    """Search for config.json in multiple locations with priority order."""
    # Priority 1: Same directory as this script
    script_dir = os.path.dirname(os.path.abspath(__file__))
    paths_to_try = [
        os.path.join(script_dir, "config.json"),
        # Priority 2: Workspace root
        os.path.join(WORKSPACE_ROOT, "config.json"),
        # Priority 3: System skills directory
        os.path.join(PROJECT_ROOT, "system_skills", "feishu-messenger", "config.json"),
        # Priority 4: Parent directory
        os.path.join(os.path.dirname(script_dir), "config.json"),
        # Priority 5: Current working directory
        os.path.join(os.getcwd(), "config.json"),
    ]
    
    for path in paths_to_try:
        if os.path.exists(path):
            return path
    
    return None

CONFIG_FILE = find_config_file()

def load_config():
    """Load app_id and app_secret from config.json if it exists."""
    config_path = find_config_file()  # Re-search each time in case file was created
    if config_path:
        try:
            with open(config_path, 'r', encoding='utf-8') as f:
                return json.load(f)
        except Exception as e:
            print(f"Warning: Failed to load config file from {config_path}: {e}", file=sys.stderr)
    return {}

def get_tenant_access_token(app_id, app_secret):
    url = "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal"
    headers = {"Content-Type": "application/json; charset=utf-8"}
    payload = {
        "app_id": app_id,
        "app_secret": app_secret
    }
    try:
        resp = requests.post(url, headers=headers, json=payload, timeout=10)
        resp.raise_for_status()
        data = resp.json()
        if data.get("code") != 0:
            return None, f"Feishu API Error: {data.get('msg')}"
        return data.get("tenant_access_token"), None
    except Exception as e:
        return None, str(e)

def send_message(token, receive_id_type, receive_id, msg_type, content):
    url = f"https://open.feishu.cn/open-apis/im/v1/messages?receive_id_type={receive_id_type}"
    headers = {
        "Authorization": "Bearer " + token,
        "Content-Type": "application/json; charset=utf-8"
    }
    
    # Handle content parsing
    if msg_type == "text":
        try:
            # Try parsing as JSON first
            json_content = json.loads(content)
            if isinstance(json_content, dict) and "text" in json_content:
                 content_obj = json_content
            else:
                 content_obj = {"text": content}
        except:
            content_obj = {"text": content}
        
        final_content = json.dumps(content_obj)
    else:
        # For other types, assume content is already a valid JSON string
        final_content = content

    payload = {
        "receive_id": receive_id,
        "msg_type": msg_type,
        "content": final_content
    }

    try:
        resp = requests.post(url, headers=headers, json=payload, timeout=10)
        # Check for JSON error response even if status code is 4xx/5xx
        if not resp.ok:
            try:
                error_data = resp.json()
                return {"status": "error", "message": f"Feishu API Error ({resp.status_code}): {json.dumps(error_data, ensure_ascii=False)}"}
            except:
                pass # Fallback to raise_for_status
        resp.raise_for_status()
        return resp.json()
    except Exception as e:
        return {"status": "error", "message": str(e)}

def list_messages(token, container_id_type, container_id, page_size=20, sort_type="ByCreateTimeDesc"):
    url = "https://open.feishu.cn/open-apis/im/v1/messages"
    headers = {
        "Authorization": "Bearer " + token
    }
    params = {
        "container_id_type": container_id_type,
        "container_id": container_id,
        "page_size": page_size,
        "sort_type": sort_type
    }
    try:
        resp = requests.get(url, headers=headers, params=params, timeout=10)
        # Check for JSON error response even if status code is 4xx/5xx
        if not resp.ok:
            try:
                error_data = resp.json()
                return {"status": "error", "message": f"Feishu API Error ({resp.status_code}): {json.dumps(error_data, ensure_ascii=False)}"}
            except:
                pass # Fallback to raise_for_status
        resp.raise_for_status()
        return resp.json()
    except Exception as e:
        return {"status": "error", "message": str(e)}

def get_message(token, message_id):
    url = f"https://open.feishu.cn/open-apis/im/v1/messages/{message_id}"
    headers = {
        "Authorization": "Bearer " + token
    }
    try:
        resp = requests.get(url, headers=headers, timeout=10)
        # Check for JSON error response even if status code is 4xx/5xx
        if not resp.ok:
            try:
                error_data = resp.json()
                return {"status": "error", "message": f"Feishu API Error ({resp.status_code}): {json.dumps(error_data, ensure_ascii=False)}"}
            except:
                pass # Fallback to raise_for_status
        resp.raise_for_status()
        return resp.json()
    except Exception as e:
        return {"status": "error", "message": str(e)}

def list_chats(token):
    url = "https://open.feishu.cn/open-apis/im/v1/chats"
    headers = {
        "Authorization": "Bearer " + token
    }
    params = {
        "page_size": 20
    }
    try:
        resp = requests.get(url, headers=headers, params=params, timeout=10)
        if not resp.ok:
            try:
                error_data = resp.json()
                return {"status": "error", "message": f"Feishu API Error ({resp.status_code}): {json.dumps(error_data, ensure_ascii=False)}"}
            except:
                pass
        resp.raise_for_status()
        return resp.json()
    except Exception as e:
        return {"status": "error", "message": str(e)}

def download_resource(token, message_id, file_key, type, save_path):
    # https://open.feishu.cn/open-apis/im/v1/messages/:message_id/resources/:file_key
    url = f"https://open.feishu.cn/open-apis/im/v1/messages/{message_id}/resources/{file_key}"
    headers = {
        "Authorization": "Bearer " + token
    }
    params = {
        "type": type # image or file
    }
    try:
        resp = requests.get(url, headers=headers, params=params, stream=True, timeout=30)
        if not resp.ok:
            try:
                error_data = resp.json()
                return {"status": "error", "message": f"Feishu API Error ({resp.status_code}): {json.dumps(error_data, ensure_ascii=False)}"}
            except:
                pass
        resp.raise_for_status()
        
        with open(save_path, 'wb') as f:
            for chunk in resp.iter_content(chunk_size=8192):
                f.write(chunk)
        return {"status": "success", "path": save_path}
    except Exception as e:
        return {"status": "error", "message": str(e)}

def download_files_from_chat(token, container_id_type, container_id, save_dir_name="downloads"):
    # 1. List messages
    list_res = list_messages(token, container_id_type, container_id, page_size=50)
    if "data" not in list_res or "items" not in list_res["data"]:
        return {"status": "error", "message": "Failed to list messages or no messages found.", "details": list_res}
    
    items = list_res["data"]["items"]
    downloaded_files = []
    
    # 2. Prepare save directory
    save_dir = os.path.join(WORKSPACE_ROOT, save_dir_name)
    os.makedirs(save_dir, exist_ok=True)
    
    for item in items:
        msg_type = item.get("msg_type")
        message_id = item.get("message_id")
        content_str = item.get("body", {}).get("content", "{}")
        
        try:
            content = json.loads(content_str)
        except:
            continue
            
        file_key = None
        file_name = None
        res_type = None
        
        if msg_type == "image":
            file_key = content.get("image_key")
            file_name = f"{file_key}.png" # Default to png for images if name unknown
            res_type = "image"
        elif msg_type == "file":
            file_key = content.get("file_key")
            file_name = content.get("file_name", f"{file_key}.bin")
            res_type = "file"
        elif msg_type == "media": # Video
             file_key = content.get("file_key")
             file_name = content.get("file_name", f"{file_key}.mp4")
             res_type = "file" # Use 'file' type for media download in some contexts, or check API docs. Usually 'file'.

        if file_key and message_id:
            save_path = os.path.join(save_dir, file_name)
            # Avoid overwriting or re-downloading if needed? For now, just overwrite.
            print(f"Downloading {file_name}...", file=sys.stderr)
            res = download_resource(token, message_id, file_key, res_type, save_path)
            if res.get("status") == "success":
                downloaded_files.append(res["path"])
            else:
                print(f"Failed to download {file_name}: {res.get('message')}", file=sys.stderr)

    return {"status": "success", "downloaded_files": downloaded_files, "count": len(downloaded_files)}

def main():
    parser = argparse.ArgumentParser(description="Feishu Messenger Skill")
    parser.add_argument("--app_id", help="Feishu App ID", required=False)
    parser.add_argument("--app_secret", help="Feishu App Secret", required=False)
    
    # Action
    parser.add_argument("--action", help="Action: send, list, get", default="send")
    
    # Send Params
    parser.add_argument("--receive_id_type", help="Receiver ID Type", default="open_id")
    parser.add_argument("--receive_id", help="Receiver ID (for send)")
    parser.add_argument("--msg_type", help="Message Type", default="text")
    parser.add_argument("--content", help="Message Content")
    
    # List Params
    parser.add_argument("--container_id_type", help="Container ID Type (chat/thread)", default="chat")
    parser.add_argument("--container_id", help="Container ID (chat_id)")
    parser.add_argument("--page_size", help="Page size", type=int, default=20)
    parser.add_argument("--sort_type", help="Sort type (ByCreateTimeAsc/ByCreateTimeDesc)", default="ByCreateTimeDesc")
    
    # Get Params
    parser.add_argument("--message_id", help="Message ID")

    # Download Params
    parser.add_argument("--save_dir", help="Directory name to save downloads", default="downloads")
    
    args = parser.parse_args()
    
    # Load config
    config = load_config()
    
    # Priority: CLI args > Config file
    app_id = args.app_id or config.get("app_id")
    app_secret = args.app_secret or config.get("app_secret")
    
    if not app_id or not app_secret:
        print(json.dumps({
            "status": "error", 
            "message": "Missing app_id or app_secret. Please provide them via CLI or config.json."
        }, ensure_ascii=False))
        sys.exit(1)
    
    # Get Token
    token, error = get_tenant_access_token(app_id, app_secret)
    if error:
        print(json.dumps({"status": "error", "message": f"Failed to get access token: {error}"}, ensure_ascii=False))
        sys.exit(1)

    result = {}
    if args.action == "send":
        if not args.receive_id or not args.content:
             result = {"status": "error", "message": "receive_id and content are required for 'send' action"}
        else:
             result = send_message(token, args.receive_id_type, args.receive_id, args.msg_type, args.content)
    
    elif args.action == "list":
        if not args.container_id:
            result = {"status": "error", "message": "container_id is required for 'list' action"}
        else:
            result = list_messages(token, args.container_id_type, args.container_id, args.page_size, args.sort_type)
            
    elif args.action == "get":
        if not args.message_id:
            result = {"status": "error", "message": "message_id is required for 'get' action"}
        else:
            result = get_message(token, args.message_id)

    elif args.action == "list_chats":
        result = list_chats(token)
        # Enhanced output for human readability
        if result.get("code") == 0 and "data" in result:
             items = result["data"].get("items", [])
             if not items:
                 print("No chats found. Please add the bot to a group chat first.", file=sys.stderr)
             else:
                 print(f"Found {len(items)} chats:", file=sys.stderr)
                 for item in items:
                      print(f"  - {item.get('name', 'Unknown')}: {item.get('chat_id')}", file=sys.stderr)

    elif args.action == "download_files":
        if not args.container_id:
            result = {"status": "error", "message": "container_id is required for 'download_files' action"}
        else:
            result = download_files_from_chat(token, args.container_id_type, args.container_id, args.save_dir)
            
    else:
        result = {"status": "error", "message": f"Unknown action: {args.action}"}

    print(json.dumps(result, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
