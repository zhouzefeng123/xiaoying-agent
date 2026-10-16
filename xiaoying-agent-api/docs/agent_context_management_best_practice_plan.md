# Agent 上下文管理最佳实践一次性优化计划

- 版本：v1.0
- 日期：2026-05-16
- 状态：待实施
- 适用范围：backend/internal/llm/agent、backend/internal/memory、backend/internal/message、多智能体长链执行链路
- 文档定位：以当前代码实现为准的最新基线方案；用于取代对旧现状描述已经漂移的历史优化文档

## 1. 目标

本计划的目标不是继续堆叠零散补丁，而是一次性完成 Agent 上下文管理主链的统一治理，直接服务两个核心能力：

1. 提升大型项目代码重构能力：在长会话、多文件、多轮验证下，保持任务目标、关键事实、修改焦点和验证结论稳定，不因摘要、裁剪或检索退化而漂移。
2. 提升长链任务自主能力：让 Agent 与子智能体在超长执行链中具备可观测、可恢复、可回放、可灰度的上下文治理能力，而不是仅靠自然语言连续性“碰运气”。

### 1.1 成功标准

- 500 轮以上编码会话下，单轮上下文装配耗时不再随总历史长度线性恶化。
- 当前任务状态成为单一权威来源，摘要、检索、长链恢复都围绕它对齐，而不是各写各的。
- Warm Context 跨会话污染率降到可度量且可控，默认场景下接近零。
- 摘要从“固定阈值压缩”升级为“结构化连续性检查点”，不再和 Working State、Findings Ledger、Execution Journal 重复叙述。
- 子智能体失败后不仅能看到运行快照，还能恢复到有意义的执行检查点。

### 1.2 非目标

- 不追求把所有自然语言历史做成信息论意义上的无损压缩。
- 不在第一阶段重写底层数据库 Schema。
- 不把全部问题转化为更多 prompt，而是优先收敛上下文契约、状态结构和检索边界。

## 2. 当前实现基线

以下结论基于当前代码，而不是历史设计文档。

### 2.1 历史窗口与预算

- `effectiveModelContextWindowForModel` 会先取模型窗口，再受 `llm.contextWindowCap` 限制；默认部署下 1M 模型也会被压到 256k 档。
- `loadSessionHistoryWindowForModel` 已优先走 `messages.ListWindow(sessionID, summaryMessageID, limit)`，失败时回退全量 `List`。
- `prepareConversationContextForModelWithReservedTokens` 已实现反向单次遍历、热区保护、旧工具输出折叠、历史 read 结果去重、历史截断提示插入。
- `squeezePreparedHistoryForPromptBudget` 已会把系统层、工具定义、任务态等固定开销纳入 reserved tokens，再对历史做二次收缩。

### 2.2 当前连续性承载面

当前至少有以下连续性载体同时存在：

1. `SummaryMessageID` 锚定的会话摘要消息。
2. `session_summary` 伪记忆与 milestone summary。
3. `sessionTaskState` 持久化的 Working State。
4. `StructuredFacts`、`FindingsLedger`、`ExecutionJournal`。
5. `project summary memory` 自动提取记忆。
6. 多智能体 `WorkerRuntimeSnapshot` 归档快照。

这说明系统已经不是“没有状态”，而是“状态很多，但尚未统一主从关系”。

### 2.3 记忆与检索

- `retrieveRAGContext` 已按 L1/L2/L3/L4/L5 分层装配：用户偏好、经验/指令、知识检索、Warm Context 历史消息、会话连续性摘要。
- `SearchWithPolicy` 已有 exact / FTS / vector / category list fallback 契约，预算归一化和 reason code 已文档化。
- `AddWithPolicy` 已有 exact duplicate、冲突键、semantic dedup 三层写入去重。

### 2.4 长链自治与恢复

- worker 已具备 retryable provider error 恢复、output-limit 恢复、统一失败 envelope。
- orchestrator 已记录 `status`、`last_error`、`failure_type`、`provider/output retry`、`structured_status/summary`、`last_result_preview`。
- 但当前快照仍主要是“运行态观测快照”，还不是“可恢复执行检查点”。

## 3. 风险与不合理点

以下问题按严重度排序。

### 3.1 高风险：Critical / Deep 记忆缓存共用 freshness 时间戳，失效边界耦合

证据：

- `backend/internal/llm/agent/agent.go` 中只有一个 `memoryCacheLoadedAt`，同时存在 `memoryCriticalVersion` 和 `memoryDynamicVersion` 字段，但当前实现并未让版本参与真实失效判定。
- `backend/internal/llm/agent/agent_context.go` 在 critical cache 与 deep cache 的 TTL 判断中都读取同一个 `memoryCacheLoadedAt`。
- deep cache 刷新时也会回写同一个 `memoryCacheLoadedAt`。

风险：

- query-specific 的 deep recall 会“顺带延长” critical memory 的 freshness。
- 关键偏好/指令缓存与临时知识缓存耦合，后者的频繁刷新会掩盖前者的真实陈旧度。
- 字段命名上已经表达了 critical / dynamic 分层，但运行时契约并未真正实现分离，属于典型的“概念已分层，状态机未分层”。

结论：这是当前上下文治理里最需要先拆开的缓存边界问题。

### 3.2 高风险：Warm Context 在 session miss 后直接回退全局消息搜索，长链重构任务会被跨会话噪声污染

证据：

- `backend/internal/llm/agent/agent_context.go` 中，`SearchInSession` 命中为空后会直接回退到全局 `a.messages.Search`。
- `backend/internal/message/message.go` 中，session 内搜索与全局搜索走的是不同候选域；全局搜索不再保证当前会话或当前项目边界。

风险：

- 相似历史任务的用户提问会被错误注入当前长链上下文。
- 大型项目重构场景中，跨会话“相似但不同”的路径、命令、文件名极易误导当前修复路线。
- 当前 fallback 更像“没有结果时强行补点语义近邻”，而不是“严格作用域内的安全召回”。

结论：Warm Context 的 fallback 必须从“全局兜底”改成“显式作用域策略”。

### 3.3 高风险：连续性载体过多，缺少单一权威来源，摘要与任务态存在双轨漂移风险

证据：

- `summary.go` 负责 Summary Message、session summary、project summary memory。
- `session_summary_scope.go` 负责 continuity summaries 与 milestone summary。
- `agent.go` 中 `updateTaskStateFromUserInput`、`updateTaskStateFromToolResults`、`updateTaskStateFromAssistantMessage` 又在维护独立的 Working State、Facts、Findings、Journal。
- `buildWorkingStateSystemMessage` 已经把 Working State 压成一条大 prompt，而摘要链仍在做另一套自然语言压缩。

风险：

- 同一事实可能同时出现在 Summary、Working State、session_summary memory、project memory 中，但新鲜度不同。
- 一旦摘要和任务态不同步，Agent 会在不同层读取到冲突的“当前状态”。
- 长链自治时，真正应该持续精确保留的是任务状态，不是摘要文本；当前系统仍然是“摘要很多，单一状态主线不够硬”。

结论：必须定义“谁是当前态、谁是历史态、谁是长期态”的单一主从关系。

### 3.4 高风险：多智能体运行快照可观测但不可恢复，不足以支撑真正的长链自治

证据：

- `multi_agent_worker.go` 已能做 provider retry、output-limit retry 和结构化失败封装。
- `multi_agent_orchestrator.go` 的 `WorkerRuntimeSnapshot` 记录的是状态摘要、错误、preview、structured summary，而不是可恢复的工具执行检查点。

风险：

- worker 失败后，系统只能“解释失败过什么”，还不能“从上一次有效 checkpoint 继续”。
- 对长链复杂任务而言，这意味着恢复仍然主要依赖语言模型重新理解自然语言总结，而不是恢复结构化执行态。

结论：当前更像故障可观测系统，而不是真正的自治恢复系统。

### 3.5 中风险：窗口化失败会静默回退全量历史，性能退化没有被提升成一等告警

证据：

- `loadSessionHistoryWindowForModel` 在 session 获取失败或 `ListWindow` 出错时直接回退 `messages.List`。

风险：

- 一旦底层窗口查询回归，系统会重新进入“先全量、后裁剪”的高成本路径。
- 当前策略偏向可用性，但可观测性不够，容易把性能退化伪装成正常运行。

结论：应该保留 fallback，但必须让 fallback 次数、触发原因、触发会话进入监控与发布门禁。

### 3.6 中风险：Deep RAG 的当前轮等待窗口过短，复杂任务首轮经常只能拿到“半套上下文”

证据：

- `retrieveRAGContext` 对 critical context 最多等 300ms/800ms，对 deep context 只等 120ms/220ms，超时就让它“对下一轮有益”。

风险：

- 大型项目重构与长链自治最依赖第一轮就拿到关键上下文；而当前策略更偏 TTFT，复杂任务容易首轮信息不足。
- 这会把高价值任务的首轮质量押注到“后续回合自己补回来”。

结论：需要按任务类型、阶段、首轮/续轮动态配置等待预算，而不是固定毫秒数。

### 3.7 中风险：Task State prompt surface 已收敛到 Working State，但仍保留多组未统一接入的 prompt builder

证据：

- `buildStructuredFactsPromptMessage`、`buildEvidenceGroundingPromptMessage`、`buildDiagnosticModePromptMessage`、`buildFindingsLedgerPromptMessage`、`buildExecutionJournalPromptMessage` 目前主要是包装器。
- 主链真正注入的是 `buildTaskStatePromptLayers` 与 `buildWorkingStateSystemMessage`。

风险：

- 后续维护者容易在“看起来能注入”的函数上继续加规则，但主链根本没走这里。
- 这会继续制造 prompt surface 漂移与隐性死代码。

结论：必须把 prompt layer builder 收敛成单入口，并删除或下沉不再需要的包装层。

### 3.8 中风险：记忆写入链路对大类别仍是线性扫描，缺少离线重整与热路径候选裁剪

证据：

- `AddWithPolicy` 会先 `ListByCategory`，再做 exact duplicate、conflict key、semantic dedup 的逐条比较。

风险：

- 长链大项目会快速累积 `fact`、`experience`、`instruction` 类记忆。
- 线性写入去重会逐渐把写路径变成新热点。

结论：当前写策略在功能上可用，但还不是大规模长链的最终形态。

### 3.9 中风险：历史文档与现代码存在明显代际漂移，已经影响工程判断

证据：

- `context_optimization_plan.md` 仍描述“每次加载全部历史消息”和“固定轮次摘要”。
- `infinite_session_sota_p0_plan.md` 虽然比旧文档更接近现状，但其中部分“当前风险/当前实现”也已被后续代码演进覆盖。
- `memory_system_optimization_plan.md` 仍以更早期的 memory 演进视角描述系统，未反映当前 `SearchWithPolicy` 契约。

风险：

- 工程师继续按过时文档做判断，会把已经解决的问题当作现状，把真正的新瓶颈忽略掉。

结论：需要新的单一权威文档，并把旧文档显式标成 legacy。

## 4. 目标架构

目标不是增加更多层，而是把现有能力重组为清晰、稳定、可度量的 6 层上下文合同。

### 4.1 L0 稳定约束层

内容：系统 Prompt、用户长期偏好、编码规范、安全边界、模型/工具能力说明。

要求：

- 尽量稳定，保证缓存友好。
- 只允许低频变更。
- 与任务态、摘要态彻底分离。

### 4.2 L1 权威任务态层

内容：`sessionTaskState`，包括 Goal、Current Focus、Status、Next Step、Recent Progress、Outstanding Issues、Structured Facts、Findings、Execution Journal 的压缩表达。

要求：

- 成为当前任务状态的唯一权威来源。
- 摘要、检索、worker 恢复都只能围绕它再组织，不得各自再维护独立“当前态”。

### 4.3 L2 热区原始历史层

内容：最近真实消息与关键工具交互。

要求：

- 只解决指代、临近编辑、最近验证。
- 不承担长期连续性的主要责任。

### 4.4 L3 连续性检查点层

内容：会话摘要、任务阶段检查点、milestone summary。

要求：

- 从“自然语言摘要”升级为“服务于恢复与裁剪的结构化 checkpoint”。
- 明确它是历史连续性层，而不是当前工作态层。

### 4.5 L4 目标化召回层

内容：Memory Capsule、Turn Context、Warm Context、同会话历史召回、同项目召回、必要时的受控跨会话召回。

要求：

- 默认严格作用域。
- 每条召回结果都带来源与 scope 标签，便于解释为什么被注入。
- 召回策略必须与任务类型、首轮/续轮、自治模式相关联。

### 4.6 L5 自治执行恢复层

内容：worker 执行检查点、最近 delegation journal、关键 artifacts、最近恢复提示、已验证状态。

要求：

- 不再只保存 preview，而是保存可恢复的最小执行态。
- 失败恢复应优先恢复 checkpoint，而不是单纯把错误总结再喂回模型。

## 5. 一次性实施方案

本计划按 5 个工作流实施，但目标是形成一次统一上线的上下文治理体系，而不是互相独立的小优化。

### 5.1 工作流 A：统一上下文合同与观测面

目标：建立“当前到底注入了什么”的统一账本，杜绝后续再靠猜。

落点：

- `backend/internal/llm/agent/agent_context.go`
  - `buildPromptContextLayers`
  - `squeezePreparedHistoryForPromptBudget`
  - `buildContextStats`
- `backend/internal/llm/agent/agent.go`
  - `emitSessionContextStats`

实施项：

1. 新增 `ContextAssemblyReport`，记录每层来源、token 预算、命中数量、fallback 情况、scope 标签。
2. 区分“当前态注入”“历史态注入”“召回态注入”“恢复态注入”的 token 占比。
3. 将 `ListWindow fallback`、global message fallback、summary injection、worker snapshot injection 提升为显式可观测事件。

验收：

- 任意一次会话请求都能从日志或调试面看到完整装配结果。
- 能区分是“热历史太大”还是“召回层太大”导致的 prompt 压力。

### 5.2 工作流 B：检索与缓存边界硬化

目标：把“检索能跑”升级为“检索可信且可控”。

落点：

- `backend/internal/llm/agent/agent.go`
  - `invalidateMemoryRetrievalCache`
  - memory cache 字段定义
- `backend/internal/llm/agent/agent_context.go`
  - `retrieveRAGContext`
- `backend/internal/message/message.go`
  - `Search`
  - `SearchInSession`
  - `ListWindow`

实施项：

1. 拆分 critical cache 与 deep cache 的 freshness 时间戳和版本号。
2. 明确 cache version 的更新时机：memory write、summary write、task checkpoint merge、manual invalidation。
3. 把 Warm Context fallback 改为三段式：同 session -> 同 parent/project scope -> 显式 global recall；默认不再直接跳全局。
4. 为每条 Warm Context 注入附带来源 scope 与召回原因。
5. 首轮复杂任务与 autonomous 模式提高 deep retrieval 等待预算；闲聊保持现有低延迟策略。

验收：

- cross-session 污染率进入可观测指标，并在默认模式显著下降。
- critical cache 不再被 deep cache 的 query-specific 刷新续命。

### 5.3 工作流 C：把摘要系统改造成连续性检查点系统

目标：不再让 Summary 与 Working State 两套“当前态”并行演化。

落点：

- `backend/internal/llm/agent/summary.go`
  - `Summarize`
  - `prepareSummaryContext`
  - `triggerTreeSummarization`
- `backend/internal/llm/agent/session_summary_scope.go`
  - `listContinuityMemories`
  - `saveMilestoneSummary`
- `backend/internal/llm/prompt/summarizer.go`

实施项：

1. 定义两种摘要产物：
   - `task_checkpoint`：服务当前任务连续性，只保存与当前 goal 直接相关的阶段状态。
   - `session_checkpoint`：服务长会话裁剪与历史 continuity。
2. 摘要生成时显式读取 `sessionTaskState`，把 Working State 作为摘要输入之一，而不是只基于消息切片。
3. 让 `session_summary` 只承担 continuity，不再隐式承担“当前任务态”。
4. `project summary memory` 继续保留，但明确只从 checkpoint 派生，不再从自由文本摘要派生。
5. 摘要触发从“消息数 + 当前输入 tokens 阈值”升级为“上下文压力 + 任务阶段 + mutation density + checkpoint 陈旧度”联合判断。

验收：

- 同一事实不再同时由 Summary 和 Working State 用不同表述重复承担。
- 恢复后系统首先读取 checkpoint，而不是先依赖自然语言摘要兜底。

### 5.4 工作流 D：收敛 Task State prompt surface，删除冗余入口

目标：让 prompt 层只有一个真实入口，避免继续在无效 builder 上加逻辑。

落点：

- `backend/internal/llm/agent/agent.go`
  - `buildTaskStatePromptLayers`
  - `buildWorkingStateSystemMessage`
  - 各类 `build...PromptMessage` 包装器
- `backend/internal/llm/agent/agent_context.go`
  - `buildPromptContextLayers`

实施项：

1. 明确 `buildTaskStatePromptLayers` 是唯一 Task State prompt 入口。
2. 把事实、证据、诊断、journal 的注入策略收敛进统一 composer。
3. 删除或下沉当前未接入主链的包装器，避免继续漂移。
4. 让 mutation-heavy 模式、correction mode、diagnostic mode 都经由同一个 prompt surface 生成。

验收：

- 新规则只需要改一个 composer，不再存在“改了但主链没走”的假入口。

### 5.5 工作流 E：把 worker runtime snapshot 升级为可恢复执行检查点

目标：让长链自治不止“看见失败”，而是“能够接着做”。

落点：

- `backend/internal/llm/agent/multi_agent_worker.go`
- `backend/internal/llm/agent/multi_agent_orchestrator.go`
- 与 worker 管理相关的 tool / API 暴露层

实施项：

1. 新增 `WorkerCheckpoint`：最近 delegated task、最近成功工具动作、最近失败动作、关键 artifacts、未完成子目标、最后恢复提示。
2. 让 archive 保存 checkpoint，而不仅是 preview 和 summary。
3. 增加 resume path：worker 在 retry budget 用尽后可从 checkpoint 重启，而不是只能回失败 envelope。
4. 为当前 scope 的 worker snapshots 增加“可恢复 / 不可恢复”标记和原因。
5. 将 parent session 的 Working State 与 worker checkpoint 建立弱一致联动，避免主链完全不知道子链卡在哪。

验收：

- 至少对 provider retry exhaustion、output-limit exhaustion、manual kill 三类故障具备 checkpoint 级恢复能力。

## 6. 代码落点清单

| 模块 | 核心文件 | 必改函数 / 结构 | 改造目标 |
| :--- | :--- | :--- | :--- |
| 历史窗口 | `backend/internal/llm/agent/agent.go` | `loadSessionHistoryWindowForModel` | 保留 fallback，但把退化显式观测化 |
| 上下文装配 | `backend/internal/llm/agent/agent_context.go` | `buildPromptContextLayers`、`squeezePreparedHistoryForPromptBudget`、`prepareConversationContextForModelWithReservedTokens` | 统一层级装配与预算说明 |
| RAG / Memory | `backend/internal/llm/agent/agent_context.go` | `retrieveRAGContext` | 拆 cache、收紧 scope、标注来源 |
| 消息检索 | `backend/internal/message/message.go` | `Search`、`SearchInSession`、`ListWindow` | 统一 session / project / global recall 策略 |
| Task State | `backend/internal/llm/agent/agent.go` | `sessionTaskState`、`loadTaskState`、`saveTaskState`、`updateTaskStateFrom...`、`buildWorkingStateSystemMessage` | 让 Working State 成为单一权威当前态 |
| 摘要 | `backend/internal/llm/agent/summary.go` | `Summarize`、`prepareSummaryContext` | 摘要升级为 checkpoint 体系 |
| Continuity | `backend/internal/llm/agent/session_summary_scope.go` | `listContinuityMemories`、`saveMilestoneSummary` | 明确 continuity 与 current state 分工 |
| Memory Policy | `backend/internal/memory/service.go` | `AddWithPolicy`、`searchWithPolicy` | 控制写入复杂度与读取来源解释 |
| Worker Runtime | `backend/internal/llm/agent/multi_agent_worker.go` | worker retry / fail / result path | 引入 checkpoint-based recovery |
| Worker Snapshot | `backend/internal/llm/agent/multi_agent_orchestrator.go` | `WorkerRuntimeSnapshot`、`archiveWorkerLocked`、`GetWorkerRuntime` | 从运行态快照升级为恢复快照 |

## 7. 实施顺序

建议按以下顺序推进，避免一开始就把摘要、检索、worker 全部同时改乱：

### 阶段 0：建立观测与契约

- 输出 `ContextAssemblyReport`
- 打通 fallback、scope、cache、summary freshness 指标
- 先不改行为，只让现状透明

### 阶段 1：检索与缓存边界硬化

- 拆分 cache freshness
- 收紧 Warm Context 作用域
- 调整首轮 / autonomous 的 retrieval 等待策略

### 阶段 2：Task State 与摘要体系合并

- 把 Working State 定义为当前态权威源
- 摘要改成 checkpoint
- 清理重复承载职责

### 阶段 3：worker checkpoint 化

- 扩展 worker archive
- 增加 resume 能力
- 让主链可感知子链真实卡点

### 阶段 4：清理冗余代码与 legacy 文档

- 删除未接主链的 prompt builder 包装层
- 将旧文档显式标记为 legacy / superseded

## 8. 验收指标

### 8.1 质量指标

- 大型代码重构基准集的通过率不低于当前基线，且长链场景提升明显。
- 连续 200 / 500 / 1000 轮后，任务 goal、current focus、open issues 仍与真实状态一致。
- correction mode 场景中，重复追加同类改动的比例显著下降。

### 8.2 检索指标

- Warm Context 默认跨会话注入占比降到接近 0。
- 首轮编码任务的深层召回命中率显著高于当前实现。
- 召回结果来源可解释：session / parent-session / project / global 占比可见。

### 8.3 摘要指标

- Summary 与 Working State 的冲突率进入可观测面，并持续下降。
- checkpoint 的 freshness lag 可度量，例如“落后当前执行多少轮”。

### 8.4 自治指标

- worker 在 provider retry exhaustion 与 output-limit exhaustion 下的恢复成功率提升。
- 当前 scope 的 snapshots 不再只返回“unknown / preview”，而能返回可恢复状态说明。

## 9. 测试矩阵

### 9.1 单元测试

- `backend/internal/llm/agent`
  - cache 边界测试
  - context assembly report 测试
  - summary checkpoint 合并测试
  - task state / correction / mutation-heavy / diagnostic 模式测试
- `backend/internal/memory`
  - search policy 预算契约
  - add policy 去重与复杂度控制
- `backend/internal/message`
  - `ListWindow`、session search、scope fallback 测试
- `backend/internal/llm/agent` 多智能体
  - worker checkpoint archive / resume 测试

### 9.2 集成测试

- 200 / 500 / 1000 轮长会话压测
- 大型代码重构多文件任务
- summary 触发后继续执行与恢复
- correction mode 连续否认反馈
- worker provider error / output limit / manual kill 恢复

### 9.3 回归命令

- `go test ./internal/llm/agent/...`
- `go test ./internal/memory/...`
- `go test ./internal/message/...`
- `go test ./internal/llm/provider/...`
- `go build ./...`
- `npm run build`

## 10. 灰度与回滚

建议全部通过 feature flag 灰度，至少拆成以下开关：

1. `context_contract_v2`
2. `rag_scope_guard_v1`
3. `continuity_checkpoint_v1`
4. `worker_checkpoint_resume_v1`

回滚原则：

- 任一工作流出现质量退化，可单独回滚，不影响其它层。
- fallback 永远保留，但 fallback 的触发必须进入监控面。
- 新旧摘要体系、worker checkpoint 与旧 preview archive 在灰度期并存，直到评测完成。

## 11. 本计划的关键取舍

1. 优先统一契约，不优先继续补更多 prompt。
2. 优先让 Working State 成为唯一当前态，不再让摘要承担当前态责任。
3. 优先把召回作用域做对，再继续放大召回能力。
4. 优先把 worker 恢复做成 checkpoint，再继续扩展自治链长度。

如果这四个取舍不先落地，继续增加摘要、记忆、子智能体能力，只会把当前的多源漂移放大。