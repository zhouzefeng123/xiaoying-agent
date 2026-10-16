---
name: skill-generator
description: 核心元技能(Meta-Skill)。必须用于：新建技能、保存为技能、生成技能、修改/更新技能、删除技能、列出技能。此工具负责将代码持久化为可重用的系统技能。
language: python
script_path: generate_skill.py
parameters:
  skill_name:
    type: string
    description: "技能名称，使用连字符命名（如 weather-checker）。"
    required: true
  skill_description:
    type: string
    description: "技能的功能描述。"
    required: true
  code:
    type: string
    description: "完整的 Python 技能代码。与 content 二选一；提供 code 时创建可执行技能。"
    required: false
  content:
    type: string
    description: "纯文本/Markdown 技能正文。与 code 二选一；提供 content 时创建 document-only 技能。"
    required: false
  parameters:
    type: object
    description: "技能参数定义对象。格式：{ param_name: { type: string, description: desc, required: true } }。对于可执行技能，强烈建议显式提供；纯文本技能通常可以省略。"
    required: false
  requirements:
    type: string
    description: "详细的功能需求（仅用于生成文档）。"
    required: false
  reference_urls:
    type: array
    description: "参考网页链接列表（仅用于生成文档）。"
    required: false
  action:
    type: string
    description: "操作类型：'create' (默认), 'delete', 'list'。"
    required: false
---

# skill-generator

专用于部署 AuraMate 系统技能的元技能。既支持接收 Agent 编写好的 Python 代码并生成可执行技能，也支持直接创建不带脚本的纯文本 document-only 技能，统一保存到 backend/system_skills 目录。

## 核心能力
1. **代码部署**: 接收 `code` 参数，自动处理目录结构、元数据文件并保存可执行技能。
2. **纯文本技能创建**: 接收 `content` 参数，创建只包含 `SKILL.md` 的 document-only 技能，不生成脚本文件。
3. **强制规范化**: 自动生成符合 AuraMate 系统要求的 `SKILL.md`（包含参数定义），这是技能被系统加载的**唯一凭证**。
4. **全生命周期管理**: 支持创建、更新、删除和列出技能。

## 重要说明
- **SKILL.md 是必须的**: AuraMate 系统**仅识别**包含 `SKILL.md` 的技能包。任何没有 `SKILL.md` 的脚本文件都将被系统忽略。
- **请勿手动创建**: 务必使用此 `skill-generator` 工具来创建技能。它会自动为你生成正确的 `SKILL.md` 和目录结构。
- **参数定义**: 强烈建议在调用时通过 `parameters` 参数显式提供技能参数定义，以确保生成的 `SKILL.md` 准确无误。

## 参数说明
- `skill_name`: (必填) 技能名称，使用连字符命名（如 weather-checker）。
- `skill_description`: (必填) 技能的功能描述。
- `code`: (可选) 完整的 Python 技能代码。与 `content` 二选一；提供后创建可执行技能。
- `content`: (可选) 纯文本/Markdown 技能正文。与 `code` 二选一；提供后创建 document-only 技能。
- `parameters`: (可选) 技能参数定义对象。格式：`{ "param_name": { "type": "string", "description": "desc", "required": true } }`。如果不提供，系统将尝试从代码中自动扫描参数。
- `requirements`: (可选) 详细的功能需求（仅用于生成文档）。
- `reference_urls`: (可选) 参考网页链接列表（仅用于生成文档）。
- `action`: (可选) 'create' (默认), 'delete', 'list'。

## 技能统一规范 (Skill Standard)
AuraMate 系统要求所有技能必须遵循以下统一格式：

1. **目录结构规范**:
   - 每个技能必须位于 `system_skills` 下的独立子目录中。
   - 目录名推荐使用 kebab-case（如 `my-skill-name`）。

2. **核心文件规范**:
  - `SKILL.md`: (必须) 技能的元数据定义文件，包含 YAML Frontmatter。
  - 实现脚本: (可选) 如 `my_skill.py`，仅可执行技能需要；纯文本技能可以只有 `SKILL.md`。

3. **元数据规范 (SKILL.md)**:
   - 必须包含 YAML Frontmatter（即文件开头的 `--- ... ---` 块）。
   - 必须定义 `name` (技能名), `description` (描述), `parameters` (参数定义)。
  - 可执行技能必须定义 `language` (如 `python`) 和 `script_path` (脚本文件名，如 `my_skill.py`)。
  - 纯文本技能应定义 `language: markdown` 与 `call_format: document`，且不应写入 `script_path`。
   - 参数定义必须包含 `type` 和 `description`，以便 LLM 正确调用。

## 技能目录与修改说明
1. **技能存储位置**: 所有生成的技能将保存在 `system_skills` 目录下。
   - 目录结构: `system_skills/{skill_name}/`
   - 描述文件: `system_skills/{skill_name}/SKILL.md`
  - 主脚本文件: `system_skills/{skill_name}/{skill_name_underscore}.py`（仅可执行技能）

2. **如何修改技能**:
  - **更新可执行技能**: 再次调用此 `skill-generator` 工具，使用相同的 `skill_name` 和新的 `code`，工具将覆盖旧的脚本文件。
  - **更新纯文本技能**: 使用相同的 `skill_name` 和新的 `content`，工具将覆盖旧的 `SKILL.md` 内容。

## 使用示例
1. **部署已编写的代码**: `skill-generator(skill_name="my-skill", skill_description="我的技能", code="import ...")`
2. **创建纯文本技能**: `skill-generator(skill_name="my-guide", skill_description="操作手册", content="# 操作步骤\n\n1. 先...\n2. 再...")`
3. **删除技能**: `skill-generator(action="delete", skill_name="my-skill")`

## 触发关键词
- 生成技能
- 新建技能
- 创建技能
- 保存技能
- 修改技能
- 更新技能
- 删除技能
- 列出技能
