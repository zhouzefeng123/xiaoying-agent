---
name: docx-toolkit
description: 专业 Word (DOCX) 处理工具包，用于解包、编辑和重新打包 DOCX 文档。支持 XML 验证、修订模式合并、运行合并等功能。
language: python
script_path: docx_dispatch.py
parameters:
  action:
    type: string
    description: "操作类型：'unpack' (解包), 'pack' (打包), 'validate' (验证)。"
    required: true
  input_path:
    type: string
    description: "输入文件路径（解包/打包时为 .docx 文件，验证时为目录）。"
    required: true
  output_path:
    type: string
    description: "输出路径（unpack 时必需，为目录；pack 时必需，为 .docx 文件；validate 时可选）。"
    required: false
    note: "unpack 和 pack 操作时必需，validate 操作时可选"
  original:
    type: string
    description: "原始 DOCX 文件路径（pack/validate 可用，用于结构对比和修订校验）。"
    required: false
    note: "pack 和 validate 操作可用"
  merge_runs:
    type: boolean
    description: "解包时是否合并相邻的相同格式文本 runs。"
    required: false
    default: true
    note: "仅 unpack 操作时使用"
  simplify_redlines:
    type: boolean
    description: "解包时是否简化修订模式标记（tracked changes）。"
    required: false
    default: true
    note: "仅 unpack 操作时使用"
  validate:
    type: boolean
    description: "打包时是否验证 XML 结构。"
    required: false
    default: true
    note: "仅 pack 操作时使用"
  auto_repair:
    type: boolean
    description: "验证时是否自动修复常见结构问题。"
    required: false
    default: false
    note: "仅 validate 操作时使用"
  author:
    type: string
    description: "修订校验时使用的作者名。"
    required: false
    default: Claude
    note: "仅 validate 操作时使用"
---

# DOCX Toolkit 技能文档

## 功能概述

DOCX Toolkit 是专业的 Word 文档处理工具包，提供以下核心功能：

1. **解包 (Unpack)**: 将 DOCX 文件解包为可编辑的目录结构
2. **打包 (Pack)**: 将编辑后的目录重新打包为 DOCX 文件，并可联动校验
3. **验证 (Validate)**: 验证 DOCX 结构是否符合 OpenXML 规范，并可自动修复常见问题

## 使用方式

### Python 调用

```python
from docx_dispatch import unpack, pack

# 解包 DOCX
_, message = unpack("document.docx", "unpacked/", merge_runs=True, simplify_redlines=True)

# 打包 DOCX
_, message = pack("unpacked/", "output.docx", original_file="document.docx", validate=True)
```

### 命令行调用

```bash
# 解包
python docx_dispatch.py --action unpack --input_path document.docx --output_path unpacked/

# 打包
python docx_dispatch.py --action pack --input_path unpacked/ --output_path output.docx --original document.docx

# 验证
python docx_dispatch.py --action validate --input_path unpacked/ --auto_repair true --original document.docx
```

## 核心功能

### 1. 解包 (Unpack)

- 提取 DOCX 中的所有文件（ZIP 格式）
- 美化打印 XML 文件，便于编辑
- 自动合并相邻的 runs（相同格式）
- 简化修订模式（tracked changes）

参数：
- `merge_runs`: 合并相邻 runs（默认 true）
- `simplify_redlines`: 简化修订（默认 true）

### 2. 打包 (Pack)

- 验证 XML 结构（可选）
- 压缩 XML 文件，减小体积
- 重新打包为标准 DOCX 格式

参数：
- `original`: 原始 DOCX 文件（用于验证对比）
- `validate`: 是否验证 XML（默认 true）

### 3. 验证 (Validate)

- 验证 DOCX 结构是否符合 OpenXML 规范
- 检查 XML schema 合规性
- 可选自动修复空白保留、durableId 等常见问题

参数：
- `original`: 原始 DOCX 文件（用于修订校验）
- `auto_repair`: 是否自动修复（默认 false）
- `author`: 修订校验作者名（默认 Claude）

## 文件结构

```
docx-toolkit/
├── skill.md              # 技能文档
├── docx_dispatch.py      # 统一入口
└── scripts/
    └── office/
        ├── unpack.py     # 解包逻辑
        ├── pack.py       # 打包逻辑
        ├── validate.py   # 验证逻辑
        ├── validators/   # 验证器
        │   ├── docx.py
        │   └── base.py
        └── helpers/      # 辅助工具
            ├── merge_runs.py
            └── simplify_redlines.py
```

## 注意事项

1. DOCX 文件本质上是 ZIP 压缩包
2. 修改 XML 后必须重新打包才能被 Word 识别
3. 验证步骤可确保生成的文件不会损坏
4. 修订模式（Track Changes）需要特殊处理

## 依赖

- Python 3.8+
- defusedxml
- 标准库：zipfile, pathlib, shutil
