# AuraMate Input Token 构成与优化分析报告 (修正版 v5)

## 1. 现状与问题
在 PPT 生成场景中，Input Token 高达 **54,165**。

## 2. 根因深度定位

### 2.1 全量工具定义注入 (确认)
经代码核查 (`agent.go` 第 1203 行)，系统确实将 **所有注册的工具** 的完整 JSON Schema 注入到了 System Prompt 中。
*   代码：`for _, t := range a.allTools { currentTools = append(currentTools, t) }`
*   **没有按需加载**：当前逻辑没有实现"仅注入核心工具，其他工具按需加载"的机制。所有注册的工具（含 MCP 工具、系统技能等）都会被完整地发送给模型。

### 2.2 Skill 工具注入方式 (Python Parser 分析)
经核查 `parser.go`，AuraMate 的 Python Skills (如 `ppt-maker-pro`) **不是** 注入整个 Python 源代码，而是解析 **Docstring** 来生成 JSON Schema。
*   **解析逻辑**：`PythonParser.Parse` 读取文件头部的 Docstring (TOOL, DESCRIPTION, ARGS)。
*   **注入内容**：仅注入提取出的 `Name`, `Description`, `Parameters` (JSON Schema)。
*   **结论**：虽然没有注入源代码，但如果有大量 Skill，每个 Skill 的 Schema 依然占用大量 Token。
    *   例如 `ppt-maker-pro` 可能有复杂的参数定义。
    *   如果注册了 30+ 个 Skill，Schema 总大小依然可观 (约 20k+ tokens)。

### 2.3 工具输出截断 (Read Tool)
用户提到的 "只读工具的前200个字符" 实际上是 **Tool Output** 的截断逻辑，而非 **Tool Definition** 的截断。
*   **Read Tool**: 在 `read_tool.go` 中，确实有 `MaxLineLength = 2000` (字符) 的限制，以及 `DefaultReadLines = 2000` (行) 的限制。这意味着读取文件内容时有保护机制。
*   **Context Pruning**: 在 `agent.go` 中，对于**历史记录**中的旧 Tool Output，有压缩逻辑（如果 > 500 字符且非最近消息，则替换为摘要）。
*   **关键区别**：
    *   **Input Token 膨胀** (5.4k) 发生在 **System Prompt** 阶段（工具定义）。
    *   **截断逻辑** 作用于 **Tool Execution** 阶段（工具输出）。
    *   因此，现有的截断逻辑无法解决 System Prompt 过大的问题。

## 3. 为什么 System Prompt 这么大？
假设 AuraMate 注册了 30+ 个工具（包括各种 MCP server 和 Python Skills），每个工具的 JSON Schema 平均 500-1000 tokens。
*   30 tools * 800 tokens = **24,000 tokens**。
*   加上 Project Context (可能 10k+) 和其他 System Prompt，达到 5.4w 是合理的。

## 4. 优化方案探讨 (已修正)

### 4.1 原始方案：工具定义按需加载 (Lazy Loading)
*   **策略**：仅注入核心工具，其他工具仅保留名称和简述，模型需先调用 `load_tool` 获取完整定义。
*   **目的**：大幅降低 Input Token。

### 4.2 用户反馈与深度分析 (性能权衡)
用户指出：*全量注入虽然 Token 多，但能保留性能；按需加载可能会丢失性能、无法调用完整工具或影响执行效率。*

**分析结论：用户的担忧是完全正确的。**

#### 全量注入 (现状) vs. 按需加载 (优化) 对比

| 维度 | 全量注入 (Current) | 按需加载 (Proposed Lazy Loading) |
| :--- | :--- | :--- |
| **首字延迟 (TTFT)** | **高** (需处理 54k+ tokens) | **低** (仅处理核心 tokens) |
| **单次任务交互轮数** | **少** (直接调用工具) | **多** (需 1. 搜索/加载工具 -> 2. 调用工具) |
| **工具调用成功率** | **高** (模型拥有完整上下文) | **中** (依赖模型检索能力，可能产生幻觉或参数错误) |
| **思考/推理开销** | **低** (直接规划) | **高** (需额外规划"查找工具"的步骤) |
| **Token 成本** | **高** (Input Token 爆炸) | **低** (Input Token 节省，但 Output Token 略增) |

#### 潜在风险
1.  **无法调用完整工具**: 如果简略描述 (Lite Schema) 写得不好，模型可能不知道该工具能解决当前问题，从而完全忽略它。
2.  **影响执行效率**: 简单的 "生成PPT" 任务可能从 `1 轮对话` 变为 `3 轮对话` (思考 -> 加载 -> 调用)，导致用户等待时间变长。
3.  **认知负荷**: 模型需要时刻维护 "我有哪些工具未加载" 的状态，增加了 Prompt Engineering 的难度。

## 5. 最终建议 (Hybrid Strategy)
鉴于 AuraMate 定位为高性能 Agent，且当前模型 (如 Gemini 1.5 Pro, GPT-4o) 上下文窗口较大 (128k+)，**盲目追求低 Input Token 而牺牲智能性是不可取的**。

**建议方案调整**:
1.  **默认保持全量注入**: 对于 50 个以内的工具，优先保证模型能直接看到完整定义，确保任务一次性成功。
2.  **智能截断 (Context Pruning)**: 重点优化 **历史对话** (History) 的 Token 占用，而非系统提示词 (System Prompt)。
    *   *现有逻辑已包含*: 压缩旧的 Tool Output。
    *   *加强*: 定期总结长对话，释放 History Token。
3.  **按需加载仅作为备选**: 仅当注册工具数量极多 (>100) 或 Token 溢出时，才对 **非核心/低频** 工具启用 Lazy Loading。

**结论**: 在当前 Token 量级 (54k) 下，如果模型支持长上下文，**保持现状 (全量注入)** 是保障任务执行效率和成功率的最佳选择。

---

## 6. 工具性能与 Token 优化 (2026-02-20 更新)

针对用户反馈的 "fetch/search 工具限制导致无结果" 问题，进行了以下优化与测试：

### 6.1 优化措施
*   **WebSearch Tool**:
    *   Timeout: 10s -> **30s**
    *   Max Results: 5 -> **20**
    *   Snippet Limit: 200 chars -> **500 chars** (平衡内容与 Token)
    *   Output Limit: 2000 chars -> **20000 chars** (允许更多结果返回)
*   **Fetch Tool**:
    *   Timeout: 10s -> **30s**
    *   Max Size: 50KB -> **2MB** (防止大页面被截断)
    *   Cleaning Threshold: 放宽 HTML 清洗策略，保留更多正文内容。

### 6.2 性能测试结果 (Tool Benchmark)
使用 `cmd/tool_benchmark` 对优化后的工具进行了基准测试：

1.  **Web Search**:
    *   **耗时**: ~550ms (极快)
    *   **内容长度**: ~1700 chars
    *   **Token 消耗**: ~424 tokens
    *   **结论**: 搜索结果丰富且相关，Token 消耗控制在极低水平 (400 tokens)，性价比极高。

2.  **Fetch URL**:
    *   **耗时**: ~825ms
    *   **内容长度**: ~5431 chars
    *   **Token 消耗**: ~1357 tokens
    *   **结论**: 能完整抓取页面核心内容，Token 消耗适中 (1.3k tokens)，远低于原始 HTML 的消耗。

### 6.3 工作目录锁定 (Workspace Locking)
为了确保 Agent 文件操作的一致性和前端兼容性，已实施强制工作目录策略：

1.  **策略**: 无论是在开发环境还是打包后的环境，工作目录始终锁定在 **应用根目录下的 workspace 文件夹**。
2.  **实现**:
    *   **Dev Mode**: `.../backend/workspace`
    *   **Packaged**: `.../app_root/workspace`
    *   **Frontend**: 前端请求 `/workspace/` 路径时，后端自动映射到上述锁定目录。
3.  **验证**:
    *   Benchmark 测试显示 Working Directory 为: `E:\auramate_hub\auramate-0.1\auramate-0.93\auramate\backend\workspace`
    *   系统启动时会自动创建该目录，无需人工干预。

### 6.4 统一目录结构 (Unified Directory Structure) (2026-02-20 更新)

为了进一步规范 Agent 的资源管理，已将工具 (Tools) 和技能 (Skills) 的下载/存储目录进行了统一配置：

1.  **核心逻辑 (AppRoot)**:
    *   `config.go` 集中管理所有关键路径，基于 `AppRoot` 动态计算。
    *   **Dev Mode**: `AppRoot` = `.../backend`
    *   **Packaged**: `AppRoot` = `.../app_root`

2.  **统一路径**:
    *   **System Skills**: 锁定为 `AppRoot/system_skills` (Dev: `backend/system_skills`)。
    *   **MCP Tools**: 锁定为 `AppRoot/tools` (Dev: `backend/tools`)。
    *   **Workspace**: 锁定为 `AppRoot/workspace` (Dev: `backend/workspace`)。

3.  **环境透传**:
    *   新增环境变量 `AURAMATE_SKILLS_DIR` 和 `AURAMATE_TOOLS_DIR`。
    *   所有子进程 (如 `skill-generator`) 自动继承这些变量，确保生成的 Skill 或下载的 Tool 落入正确的统一目录，无需硬编码路径。

4.  **验证**:
    *   Benchmark 工具已更新，输出了统一后的环境变量和配置路径，确认无误。
