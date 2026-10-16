---
name: pdf-maker-pro
description: 专业商务PDF生成器：将Markdown文本自动转换为排版精美的PDF文档。完全支持中文字体，支持图片插入与自动缩放。
language: python
script_path: pdf-maker-pro.py
parameters:
  markdown_text:
    type: string
    description: "输入的Markdown格式字符串（支持 #、##、-、正文、![]()图片插入）。"
    required: true
  output_filename:
    type: string
    description: "输出文件路径。默认自动生成。"
    required: false
  template:
    type: string
    description: "模板风格，可选值：'business_clean' (默认), 'report_formal'。"
    required: false
---

# PDF Maker Pro Skill

## Description
专业商务PDF生成器：将Markdown文本自动转换为排版精美的PDF文档。
完全支持中文字体（自动检测系统字体），支持图片插入与自动缩放，适用于生成报告、企划书、会议纪要等商务场景。

## Parameters

| Name | Type | Required | Description |
| :--- | :--- | :--- | :--- |
| `markdown_text` | `string` | Yes | 输入的Markdown格式字符串（支持 `#`、`##`、`-`、正文、`![](path)`图片插入）。 |
| `output_filename` | `string` | No | 输出文件路径（相对或绝对路径）。如果不提供，将自动生成在当前目录下，格式为 `document_YYYYMMDD_HHMMSS.pdf`。 |
| `template` | `string` | No | 模板风格，可选值：<br>- `"business_clean"`（默认）：简洁商务风格<br>- `"report_formal"`：正式报告风格（预留） |

## Usage Examples

### 1. 基础用法
最简单的调用方式，只需提供 Markdown 内容。

```python
pdf_maker_pro(
    markdown_text="# 项目周报\n## 本周进展\n- 完成模块A开发\n- 修复Bug #123"
)
```

### 2. 指定输出文件名
您可以指定生成的文件名。

```python
pdf_maker_pro(
    markdown_text="# 会议纪要\n本次会议讨论了...",
    output_filename="meeting_notes.pdf"
)
```

### 3. 插入图片与高级排版
支持插入本地图片，图片会自动缩放以适应页面宽度。

```python
markdown_content = """
# 季度销售分析
## 核心数据
本季度销售额增长 **15%**。

## 趋势图
![销售趋势图](C:/Users/Admin/Desktop/sales_chart.png)

## 结论
建议加大市场投入。
"""

pdf_maker_pro(
    markdown_text=markdown_content,
    output_filename="sales_report.pdf",
    template="business_clean"
)
```

## Returns
返回一个 JSON 对象，包含生成状态和文件路径。

```json
{
    "status": "success",
    "file_path": "E:\\project\\sales_report.pdf",
    "message": "Successfully created PDF: sales_report.pdf"
}
```

## Notes
- **依赖安装**：运行前请确保当前技能解释器已预装 `reportlab`、`emoji`、`Pillow`。技能不会在运行时自动执行 `pip install`；如果缺依赖，会直接返回明确错误和建议安装命令。
- **字体支持**：自动优先使用 **微软雅黑 (Microsoft YaHei)**，如果未找到则回退到 SimHei 或 SimSun，确保中文不乱码。
- **图片路径**：⚠️ **必须使用绝对路径**（如 `C:/Users/Admin/images/photo.jpg` 或 `E:/workspace/images/photo.jpg`），不支持相对路径。Windows 路径建议使用正斜杠 `/` 而非反斜杠 `\`。若出现"Image path not found"警告，请检查路径是否为绝对路径且文件真实存在。
- **Emoji**：支持基础 Emoji 显示（取决于系统字体支持）。
