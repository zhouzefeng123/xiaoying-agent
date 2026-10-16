# Agent 上下文治理推进 Todo

- 日期：2026-05-16
- 基线文档：backend/docs/agent_context_management_best_practice_plan.md
- 当前状态：P1 / P2 主链完成，进入验证与审查

## 已完成

- [x] 拆分 Critical / Deep memory cache freshness 边界
- [x] 收紧 Warm Context 作用域到 current -> parent -> ancestor，默认不再隐式 global fallback
- [x] 为 context_stats 增加 ContextAssemblyReport，并在前端上下文窗口展示装配观测
- [x] 收敛 Task State prompt surface，删除未接入主链的包装入口

## 当前轮 P0

- [x] 生成执行 todo 文档，固化当前进度与下一步切片
- [x] 修复 auto 模型在首选模型不可用时不能自动切换的问题
  - 目标：自动模式下，若首个候选模型在首 token 之前失败，则允许继续尝试下一个候选模型
  - 落点：client/src/App.tsx、client/src/lib/chat-models.ts、client/src/lib/chat-models.test.ts
  - 验收：1403 / 服务不可用类早期失败不再阻断 auto 候选链
- [x] 把 ListWindow / session fallback 的历史退化信号纳入 ContextAssemblyReport
  - 目标：显式暴露窗口化失败是否退回全量历史，避免性能退化被静默掩盖
  - 落点：backend/internal/llm/agent/agent.go、backend/internal/llm/agent/agent_context.go、client/src/components/context/ContextWindowButton.tsx

## 当前阶段 P1：摘要 authority checkpoint 化

- [x] 定义 task_checkpoint 与 session_checkpoint 的职责边界
- [x] 在 summary 输入中显式引入 sessionTaskState，而不是只基于消息切片摘要
- [x] 让 session_summary 只承担 continuity，不再隐式承担 current task state
- [x] 让 project summary memory 从 checkpoint 派生，而不是从自由文本摘要派生
- [x] 用上下文压力、任务阶段、mutation density、checkpoint freshness 联合决定摘要触发

## 后续阶段 P2：worker 可恢复检查点

- [x] 定义 WorkerCheckpoint：最近 delegated task、最近成功动作、最近失败动作、关键 artifacts、未完成子目标、恢复提示
- [x] archive 保存 checkpoint，而不仅是 preview / summary
- [x] provider retry exhaustion、output-limit exhaustion、manual kill 三类故障支持 checkpoint 级恢复
- [x] 让 parent session 的 Working State 能看到子 worker 的真实卡点与可恢复性

## 本轮验证结果

- [x] backend/internal/llm/agent 定向测试通过
  - TestSummarizePreservesFirstUserMessageWhenCutoffExcludesIt
  - TestListRecentSessionSummaryMemoriesUsesSummaryMessages
  - TestShouldAutoSummarizeSkipsFreshCheckpointUnderPressure
  - TestShouldAutoSummarizeTriggersForMutationHeavyPressure
  - TestBuildTaskStatePromptLayersIncludeChildWorkerCheckpointLayer
  - TestBuildTaskStatePromptLayersDoNotAdvertiseResumeForRunningWorker
  - TestOrchestratorArchivesWorkerFailureSnapshot
  - TestOrchestratorResumeWorkerFromCheckpoint
- [x] backend go build ./... 通过
- [x] client npm run build 通过
- [ ] go test ./internal/llm/agent 全包通过
  - 当前剩余 TestExecuteAssistantToolCallsStartsDisjointEditsTogether 在 Windows 环境下稳定触发 1s milestone 超时，需单独治理，不归因于本轮 P1 / P2 主链改动

## 备注

- auto 模型问题不改变显式选型的严格语义；只修复 auto 候选链在早期失败时的继续尝试。
- 摘要 authority checkpoint 与 worker resume checkpoint 已落到主链；后续重点转为全包稳定性和更高层 E2E 观测。