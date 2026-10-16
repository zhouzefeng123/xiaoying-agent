# 无限对话上下文优化方案 (Infinite Context Optimization Plan)

## 1. 核心目标
在不修改底层数据库结构的前提下，通过优化应用层逻辑，实现**无限长度对话**的支持，同时将 Token 消耗控制在稳定水平（约 4k-8k），并解决“遗忘”问题。

## 2. 现状分析
- **当前架构**：
    - `agent.go` 每次加载**全部**历史消息 (`a.messages.List`)。
    - 依赖简单的滑动窗口（最近 30 条）和工具输出折叠（Folding）来压缩上下文。
    - 数据库 `messages` 表已支持 `embedding` 字段，且有 `memories` 表支持长期记忆。
    - 已经实现了基于 Intent（意图）的 RAG 分层加载逻辑 (`retrieveRAGContext`)，但主要用于检索 `memories`，对历史消息的检索还比较初步。

- **存在问题**：
    - 随着会话变长，`ListMessagesBySession` 返回的数据量线性增长，导致数据库 IO 和内存压力增大。
    - 简单的滑动窗口会直接丢弃早期的关键指令（如“请始终使用中文”）。
    - 缺乏自动摘要机制，长期对话后难以把握全局上下文。

## 3. 优化方案：动态分层上下文 (Dynamic Tiered Context)

本方案完全基于现有架构，无需修改数据库 Schema。

### 3.1 架构设计
我们将 Prompt 上下文划分为三个区域，动态组装：

```text
[System Prompt] (静态 + 动态后缀)
   |
[Cold Context: Summary] (全局摘要 - 长期记忆)
   |
[Warm Context: RAG] (相关历史 - 语义召回)
   |
[Hot Context: Recent] (最近 N 轮对话 - 短期记忆)
```

### 3.2 详细实施策略

#### 第一层：Hot Context (热上下文 - 必须保留)
- **策略**：始终保留最近 **10-15 轮**（约 20-30 条消息）的完整对话。
- **目的**：保证当前的对话连贯性，支持指代词（如“修改它”、“上一个文件”）的解析。
- **优化点**：
    - 现有逻辑已包含滑动窗口，需确保窗口大小是基于 Token 估算而非仅基于消息数量。

#### 第二层：Warm Context (温上下文 - 语义召回)
- **策略**：当用户提问时，利用 Embedding 检索**之前被滑出窗口**的相关历史消息。
- **实现**：
    - 利用现有的 `retrieveRAGContext` 函数。
    - 增强 `a.messages.Search`：不仅检索 `memories`，还要检索 `messages` 表中 `role='user'` 且 `id` 不在 Hot Context 中的记录。
    - **关键逻辑**：检索到的历史消息不直接插入对话流，而是以 `<relevant_history>` 块的形式注入到 System Prompt 或 User Prompt 之前。
- **触发条件**：
    - 仅在 Intent 为 `Task` (执行任务) 或 `Search` (搜索) 时触发，避免闲聊时浪费资源。

#### 第三层：Cold Context (冷上下文 - 滚动摘要)
- **策略**：每隔一定轮次（如 20 轮），后台自动生成一段“阶段性摘要”，存入 `memories` 表。
- **实现**：
    - **触发器**：在 `agent.go` 的主循环中，检测 `len(msgHistory) % 20 == 0`。
    - **动作**：启动一个后台 Goroutine，调用 LLM 对最近 20 轮对话进行摘要。
    - **存储**：使用 `a.memories.Create` 存入数据库，Category 设为 `session_summary`。
    - **使用**：在构建 System Prompt 时，查询最近的 `session_summary` 并注入。
- **摘要内容**：
    - 用户偏好（如“使用 Python”）。
    - 关键决策（如“决定使用 React 框架”）。
    - 当前任务状态（如“正在重构 Auth 模块”）。

### 3.3 具体的代码逻辑调整点 (无需修改代码，仅作为方案说明)

1.  **消息加载逻辑 (`prepareConversationContext`)**：
    - **现状**：加载所有消息 -> 截断。
    - **优化**：
        - 仅从数据库加载最近 50 条消息（`LIMIT 50`）。
        - 如果总消息数 > 50，则自动触发“摘要加载”逻辑，从 `memories` 表获取之前的摘要。

2.  **Embedding 生成 (异步)**：
    - 确保 `CreateMessage` 后，立即触发异步任务计算该消息的 Embedding 并更新数据库（`UpdateMessage`）。这对于 Warm Context 的检索至关重要。

3.  **Token 预算管理**：
    - System Prompt: ~1k tokens
    - Summary (Cold): ~500 tokens
    - RAG (Warm): ~1000 tokens (约 5-10 条相关记录)
    - Recent History (Hot): ~4k tokens
    - **总计**：~6.5k tokens（非常安全，远低于 32k/128k 限制）。

## 4. 预期效果
- **无限对话**：无论对话进行多久，加载到 LLM 的 Token 量始终稳定在 6.5k 左右。
- **记忆增强**：通过 RAG 和 Summary，Agent 能“记起”很久之前的约定和决策。
- **性能提升**：减少了每次请求的 Token 处理量，降低延迟和成本。
