"""
Feishu Bitable - 飞书多维表格读取工具

用于读取飞书多维表格 (Bitable) 数据的技能。
支持获取表格列表、读取记录、获取字段信息等。
"""
import os
import sys
import json
import argparse
from urllib.parse import urlparse, parse_qs

# Force UTF-8 output
sys.stdout.reconfigure(encoding='utf-8')

# Feishu API endpoints
FEISHU_AUTH_URL = "https://open.feishu.cn/open-apis/auth/v3/tenant_access_token/internal"
FEISHU_BITABLE_LIST_URL = "https://open.feishu.cn/open-apis/bitable/v1/apps/{}/app_tables"
FEISHU_BITABLE_RECORDS_URL = "https://open.feishu.cn/open-apis/bitable/v1/apps/{}/tables/{}/records"
FEISHU_BITABLE_INFO_URL = "https://open.feishu.cn/open-apis/bitable/v1/apps/{}/tables/{}"


def load_config():
    """Load config from config.json"""
    config_paths = [
        os.path.join(os.path.dirname(os.path.abspath(__file__)), "config.json"),
        os.path.join(os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__)))), "config.json"),
    ]
    
    for config_path in config_paths:
        if os.path.exists(config_path):
            with open(config_path, 'r', encoding='utf-8') as f:
                return json.load(f)
    
    return {}


def get_tenant_access_token(app_id, app_secret):
    """Get tenant access token from Feishu"""
    import urllib.request
    import urllib.error
    
    data = json.dumps({"app_id": app_id, "app_secret": app_secret}).encode('utf-8')
    req = urllib.request.Request(FEISHU_AUTH_URL, data=data, headers={"Content-Type": "application/json"})
    
    try:
        with urllib.request.urlopen(req, timeout=10) as response:
            result = json.loads(response.read().decode('utf-8'))
            if result.get("code") == 0:
                return result.get("tenant_access_token")
            else:
                print(f"Error getting access token: {result}")
                return None
    except Exception as e:
        print(f"Error: {str(e)}")
        return None


def list_tables(app_token, app_id, app_secret, output_format="text"):
    """List all tables in the bitable app"""
    import urllib.request
    import urllib.error
    
    token = get_tenant_access_token(app_id, app_secret)
    if not token:
        return {"code": -1, "msg": "Failed to get access token"}
    
    url = FEISHU_BITABLE_LIST_URL.format(app_token)
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"})
    
    try:
        with urllib.request.urlopen(req, timeout=10) as response:
            result = json.loads(response.read().decode('utf-8'))
            
            if output_format == "json":
                return result
            else:
                if result.get("code") == 0:
                    tables = result.get("data", {}).get("tables", [])
                    output = "表格列表:\n"
                    for table in tables:
                        output += f"- {table.get('name')} (ID: {table.get('table_id')})\n"
                    return output
                else:
                    return f"Error: {result.get('msg', 'Unknown error')}"
    except Exception as e:
        return {"code": -1, "msg": str(e)}


def get_table_info(app_token, table_id, app_id, app_secret, output_format="text"):
    """Get table structure and field info"""
    import urllib.request
    import urllib.error
    
    token = get_tenant_access_token(app_id, app_secret)
    if not token:
        return {"code": -1, "msg": "Failed to get access token"}
    
    url = FEISHU_BITABLE_INFO_URL.format(app_token, table_id)
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"})
    
    try:
        with urllib.request.urlopen(req, timeout=10) as response:
            result = json.loads(response.read().decode('utf-8'))
            
            if output_format == "json":
                return result
            else:
                if result.get("code") == 0:
                    table_data = result.get("data", {})
                    output = f"表格信息:\n"
                    output += f"表格名：{table_data.get('name')}\n"
                    output += f"表格 ID: {table_data.get('table_id')}\n"
                    
                    fields = table_data.get("fields", [])
                    output += f"\n字段列表 ({len(fields)} 个):\n"
                    for field in fields:
                        output += f"- {field.get('name')} [{field.get('type')}] (ID: {field.get('field_id')})\n"
                    return output
                else:
                    return f"Error: {result.get('msg', 'Unknown error')}"
    except Exception as e:
        return {"code": -1, "msg": str(e)}


def list_records(app_token, table_id, app_id, app_secret, page_size=100, page_token=None, field_names=None, output_format="text"):
    """List records from a table"""
    import urllib.request
    import urllib.error
    
    token = get_tenant_access_token(app_id, app_secret)
    if not token:
        return {"code": -1, "msg": "Failed to get access token"}
    
    # Build URL with query parameters
    url = FEISHU_BITABLE_RECORDS_URL.format(app_token, table_id)
    params = f"?page_size={page_size}"
    if page_token:
        params += f"&page_token={page_token}"
    if field_names:
        params += f"&field_names={field_names}"
    
    url += params
    req = urllib.request.Request(url, headers={"Authorization": f"Bearer {token}"})
    
    try:
        with urllib.request.urlopen(req, timeout=30) as response:
            result = json.loads(response.read().decode('utf-8'))
            
            if output_format == "json":
                return result
            else:
                if result.get("code") == 0:
                    data = result.get("data", {})
                    records = data.get("items", [])
                    has_more = data.get("has_more", False)
                    next_page_token = data.get("page_token", "")
                    
                    output = f"记录列表 (共 {len(records)} 条):\n"
                    output += "=" * 60 + "\n"
                    
                    for i, record in enumerate(records, 1):
                        output += f"\n[{i}] 记录 ID: {record.get('id')}\n"
                        fields = record.get("fields", {})
                        for field_name, field_value in fields.items():
                            output += f"    {field_name}: {field_value}\n"
                        output += "-" * 40 + "\n"
                    
                    if has_more:
                        output += f"\n还有更多数据，使用 page_token='{next_page_token}' 继续加载\n"
                    
                    return output
                else:
                    return f"Error: {result.get('msg', 'Unknown error')}"
    except Exception as e:
        return {"code": -1, "msg": str(e)}


def main():
    parser = argparse.ArgumentParser(description="Feishu Bitable Reader")
    parser.add_argument("--action", type=str, required=True, 
                        help="操作类型：'list_tables', 'list_records', 'get_table_info'")
    parser.add_argument("--app_token", type=str, required=True,
                        help="多维表格的 app_token")
    parser.add_argument("--table_id", type=str, default=None,
                        help="表格 ID（仅 list_records 和 get_table_info 需要）")
    parser.add_argument("--page_size", type=int, default=100,
                        help="每页记录数，默认：100")
    parser.add_argument("--page_token", type=str, default=None,
                        help="分页 token")
    parser.add_argument("--field_names", type=str, default=None,
                        help="逗号分隔的字段名列表")
    parser.add_argument("--output_format", type=str, default="text",
                        help="输出格式：'json' 或 'text'")
    
    args = parser.parse_args()
    
    # Load config
    config = load_config()
    app_id = config.get("app_id")
    app_secret = config.get("app_secret")
    
    if not app_id or not app_secret:
        print("Error: Missing app_id or app_secret in config.json")
        sys.exit(1)
    
    # Dispatch action
    if args.action == "list_tables":
        result = list_tables(args.app_token, app_id, app_secret, args.output_format)
    elif args.action == "list_records":
        if not args.table_id:
            print("Error: table_id is required for list_records action")
            sys.exit(1)
        result = list_records(args.app_token, args.table_id, app_id, app_secret,
                             args.page_size, args.page_token, args.field_names, args.output_format)
    elif args.action == "get_table_info":
        if not args.table_id:
            print("Error: table_id is required for get_table_info action")
            sys.exit(1)
        result = get_table_info(args.app_token, args.table_id, app_id, app_secret, args.output_format)
    else:
        print(f"Error: Unknown action '{args.action}'")
        sys.exit(1)
    
    # Output result
    if isinstance(result, dict):
        print(json.dumps(result, ensure_ascii=False, indent=2))
    else:
        print(result)


if __name__ == "__main__":
    main()
