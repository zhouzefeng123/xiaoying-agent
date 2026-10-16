---
name: pptx-toolkit
description: 专业 PowerPoint (PPTX) 处理工具包，用于解包、打包、编辑和处理PPTX演示文稿。
language: python
script_path: pptx_dispatch.py
parameters:
  action:
    type: string
    description: "操作类型：'unpack' (解包), 'pack' (打包), 'clean' (清理), 'add_slide' (添加幻灯片), 'thumbnail' (生成缩略图)。"
    required: true
  input_path:
    type: string
    description: "输入文件路径（PPTX 或解包后的 XML 目录）。"
    required: true
  output_path:
    type: string
    description: "输出文件路径。"
    required: false
  merge_runs:
    type: boolean
    description: "(解包) 合并相邻的相同格式文本 runs。默认 true。"
    required: false
  simplify_redlines:
    type: boolean
    description: "(解包) 简化修订模式标记。默认 true。"
    required: false
  original:
    type: string
    description: "(打包) 原始 PPTX 文件路径（用于验证/修复）。"
    required: false
  validate:
    type: boolean
    description: "(打包) 验证 XML 结构。默认 true。"
    required: false
  source:
    type: string
    description: "(add_slide) 源幻灯片文件（如 'slide2.xml'）或布局文件（如 'slideLayout2.xml'）。"
    required: false
  cols:
    type: integer
    description: "(thumbnail) 缩略图网格列数。默认 3。"
    required: false
---

# PPTX Toolkit (SOTA)

Professional PowerPoint file processing skill supporting PPTX creation, editing, parsing, and modification via XML manipulation.

## Dependencies

- **Python**: `python-pptx`, `lxml`
- **System**: LibreOffice (required for `thumbnail` action only)

## Scope and Design Philosophy

**Why no 'create' action?**
This toolkit is designed for **low-level manipulation** (XML unpacking/packing) and **processing** of existing PPTX files.
For creating new presentations from scratch, it is highly recommended to write a Python script using the `python-pptx` library directly, as it offers far greater flexibility for content generation (text, images, layouts) than a command-line interface could provide.

**Recommended Workflow**:
1.  **Create**: Use `python-pptx` in a Python script to generate the initial draft (structure, text, images).
2.  **Refine**: Use `pptx-toolkit` to unpack the PPTX, make precise XML edits (if needed), or manage assets.
3.  **Process**: Use `pptx-toolkit` to generate thumbnails or validate the structure.

## Usage Examples

### Unpack PPTX
```python
pptx_toolkit(
    action="unpack",
    input_path="presentation.pptx",
    output_path="unpacked/"
)
```

### Add Slide
```python
pptx_toolkit(
    action="add_slide",
    input_path="unpacked/",
    source="slideLayout2.xml"
)
```

### Pack PPTX
```python
pptx_toolkit(
    action="pack",
    input_path="unpacked/",
    output_path="output.pptx",
    original="presentation.pptx"
)
```

### Generate Thumbnails
```python
pptx_toolkit(
    action="thumbnail",
    input_path="presentation.pptx",
    cols=4
)
```

## Features

- **Unpack**: Extract PPTX to XML files for editing.
- **Pack**: Re-package XML files into a valid PPTX, with validation.
- **Clean**: Remove unused slides and resources from unpacked directory.
- **Add Slide**: Duplicate slides or create new ones from layouts.
- **Thumbnail**: Generate image grids of slides for preview.

## Design Guidelines

### Color Schemes
- Midnight Executive: `#1E2761` (Navy) + `#CADCFC` (Ice Blue) + `#FFFFFF` (White)
- Forest & Moss: `#2C5F2D` (Forest) + `#97BC62` (Moss) + `#F5F5F5` (Cream)
- Coral Energy: `#F96167` (Coral) + `#F9E795` (Gold) + `#2F3C7E` (Navy)

### Fonts
- Title: Georgia/Arial Black/Calibri (36-44pt Bold)
- Body: Calibri/Arial (14-16pt)
