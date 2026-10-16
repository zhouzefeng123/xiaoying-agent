---
name: feishu-bitable
description: 读取飞书多维表格 (Bitable) 数据的工具。支持获取表格列表、读取记录、获取字段信息等。
language: python
script_path: feishu_bitable.py
parameters:
  action:
    type: string
    description: "操作类型：'list_tables' (获取表格列表), 'list_records' (读取记录), 'get_table_info' (获取表格信息)。"
    required: true
  app_token:
    type: string
    description: "多维表格的 app_token（从 URL 获取，如 'Mjnjbde0paUDt9sLkK8chODMnIg'）。"
    required: true
  table_id:
    type: string
    description: "表格 ID（从 URL 获取，如 'tbl69er6BcNOhwJ8'）。仅 'list_records' 和 'get_table_info' 需要。"
    required: false
  page_size:
    type: integer
    description: "每页记录数，默认：100。"
    required: false
  page_token:
    type: string
    description: "分页 token（用于翻页）。"
    required: false
  field_names:
    type: string
    description: "逗号分隔的字段名列表（用于筛选返回字段）。"
    required: false
  output_format:
    type: string
    description: "输出格式 ('json', 'text')，默认：'text'。"
    required: false
---

# Feishu Bitable - 飞书多维表格读取工具

用于读取飞书多维表格 (Bitable) 数据的技能。

## 功能

- `list_tables`: 获取多维表格列表
- `list_records`: 读取表格中的记录数据
- `get_table_info`: 获取表格结构和字段信息

## 参数

### 通用参数
- `app_id` (string): 飞书 App ID（可选，默认从 config.json 读取）
- `app_secret` (string): 飞书 App Secret（可选，默认从 config.json 读取）
- `action` (string): 操作类型 (`list_tables`, `list_records`, `get_table_info`)，默认：`list_records`
- `output_format` (string): 输出格式 (`json`, `text`)，默认：`text`

### 读取记录 (`action="list_records"`)
- `app_token` (string): **必需**，多维表格的 app_token（从 URL 获取，如 `Mjnjbde0paUDt9sLkK8chODMnIg`）
- `table_id` (string): **必需**，表格 ID（从 URL 获取，如 `tbl69er6BcNOhwJ8`）
- `page_size` (int): 每页记录数，默认：100
- `page_token` (string): 分页 token（用于翻页）
- `field_names` (string): 逗号分隔的字段名列表（用于筛选返回字段）

### 获取表格列表 (`action="list_tables"`)
- `app_token` (string): 可选，指定 app_token 获取该应用下的表格列表

### 获取表格信息 (`action="get_table_info"`)
- `app_token` (string): **必需**，多维表格的 app_token
- `table_id` (string): **必需**，表格 ID

## 使用示例

### 从 URL 提取参数
飞书多维表格 URL 格式：
```
https://bqtr2h7vrqe.feishu.cn/base/Mjnjbde0paUDt9sLkK8chODMnIg?table=tbl69er6BcNOhwJ8&view=vewXxBNTOK
```
- `app_token`: `Mjnjbde0paUDt9sLkK8chODMnIg`
- `table_id`: `tbl69er6BcNOhwJ8`

### 读取表格记录
```bash
python feishu_bitable.py --action list_records --app_token Mjnjbde0paUDt9sLkK8chODMnIg --table_id tbl69er6BcNOhwJ8 --page_size 100
```

### 获取表格列表
```bash
python feishu_bitable.py --action list_tables
```

### 获取表格结构
```bash
python feishu_bitable.py --action get_table_info --app_token Mjnjbde0paUDt9sLkK8chODMnIg --table_id tbl69er6BcNOhwJ8
```

## 配置

技能会自动从以下位置查找 `config.json`：
1. 技能同目录 (`feishu-bitable/config.json`)
2. 工作空间根目录 (`workspace/config.json`)
3. 飞书消息技能目录 (`feishu-messenger/config.json`)

`config.json` 格式：
```json
{
  "app_id": "cli_xxx",
  "app_secret": "xxx"
}
```

### 自动配置（Agent 指引）
如果 `config.json` 文件内容为空或缺少 `app_id` / `app_secret`，或者在执行时遇到认证错误：
1. **必须** 询问用户提供飞书开放平台的 `App ID` 和 `App Secret`。
2. 获得用户输入后，使用 `Write` 工具将信息写入 `backend/system_skills/feishu-bitable/config.json` 文件中。
3. 写入完成后，再次尝试执行用户的请求。

## 输出格式

### JSON 输出
完整的数据结构，包含：
- `code`: 状态码（0 表示成功）
- `data`: 数据内容
- `msg`: 消息

### Text 输出
人类可读的格式化输出，适合直接查看。

## 注意事项

1. 飞书应用需要开通多维表格 API 权限
2. 确保应用有访问目标多维表格的权限
3. 大量数据时注意分页（使用 `page_token`）
