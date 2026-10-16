# 前端临时 Token 统计优化报告

## 问题背景
用户反馈前端在展示实时 Token 消耗时，数据统计不完整，未能包含所有返回的 Token（例如工具调用的消耗或中间步骤的消耗）。

## 问题分析
经过排查，发现问题主要存在于后端 Agent 的 Token 统计和事件分发逻辑中：
1. **单步统计局限性**：原有的 `streamAndHandleEvents` 函数仅处理当前单次流式响应的 Token 消耗，并将其直接发送给前端。在一个多步骤（如 ReAct 循环或工具调用）的请求中，前端收到的仅仅是当前步骤的消耗，而非整个请求的累积消耗。
2. **事件覆盖**：前端通常会用最新收到的 `usage` 事件覆盖旧值。如果后端分别发送 Step 1 (100 tokens) 和 Step 2 (50 tokens)，前端最终只显示 50 tokens，导致总消耗被低估。
3. **测试环境异常**：在尝试验证修复时，发现 `usage_event_test.go` 中的 `TestUsageEventEmission` 测试因配置初始化缺失导致 panic，阻碍了验证流程。

## 修复方案

### 1. 后端累积消耗逻辑 (Backend Cumulative Usage)
修改了 `internal/llm/agent/agent.go` 中的核心生成逻辑，引入了会话级（Session-level）的 Token 累积器。

- **`processGeneration` 函数**：
  - 在主循环外初始化 `sessionUsage` 变量，用于跨步骤累积 Token。
  - 在每次调用 `streamAndHandleEvents` 时，将当前的累积值 `sessionUsage` 传入。
  - 在每一步骤完成后，根据数据库更新的 Session 差值（`dIn`, `dOut`），更新 `sessionUsage`。

- **`streamAndHandleEvents` 函数**：
  - 增加 `previousUsage` 参数。
  - 在接收到 Provider 的 `EventUsage` 或 `EventComplete` 事件时，不再直接转发，而是将 `当前事件消耗 + previousUsage` 计算出总消耗后，作为 `AgentEventTypeUsage` 事件发送给前端。

### 2. 测试环境修复 (Test Fix)
修复了 `internal/llm/agent/usage_event_test.go` 中的 Panic 问题。
- 在 `TestUsageEventEmission` 测试开始前，显式调用 `config.Load` 并手动填充 Mock 配置，确保 `createAgentProvider` 能够正常初始化。

## 验证结果
- **单元测试**：`TestUsageEventEmission` 测试通过，验证了 Usage 事件的正常流转。
- **逻辑验证**：通过代码审查确认，现在发送给前端的 Usage 事件包含了当前请求中所有已完成步骤的累积消耗加上当前正在进行的步骤消耗，确保前端展示的是完整的总消耗。

## 后续建议
- 前端应继续使用“覆盖更新”策略处理 `usage` 事件，因为后端现在发送的是累积值。
- 建议在后续增加集成测试，模拟多步骤工具调用场景，以自动化验证累积逻辑的准确性。
