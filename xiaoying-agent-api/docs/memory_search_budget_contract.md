# 记忆检索预算与退化路径契约

## 1. 目的

本文档定义 `internal/memory/service.go` 中记忆检索请求的运行时契约，统一以下问题的口径：

- 请求预算字段如何归一化。
- FTS / Vector / List Fallback 在什么条件下触发。
- `MemorySearchDecision` 中各关键字段应如何解释。
- 离线评测与回归门禁当前已经锁定了哪些行为。

适用范围：

- `SearchWithPolicy(...)`
- `SearchByCategoryWithPolicy(...)`
- `DefaultSearchRequest(...)`
- `DefaultCategorySearchRequest(...)`

---

## 2. 决策输出字段

所有策略化检索最终返回 `MemorySearchResult`，其中 `Decision` 字段是唯一的可观测契约出口。

关键字段含义如下：

| 字段 | 含义 | 说明 |
| :--- | :--- | :--- |
| `Contract` | 契约版本 | 当前固定为 `v1` |
| `Stage` | 检索阶段 | 调用方传入，用于排障和评测分组 |
| `Category` | 类别过滤 | 为空表示全局搜索 |
| `Limit` | 归一化后的最终结果上限 | 不是原始输入值，而是修正后的运行时值 |
| `FTSCandidateLimit` | FTS 候选数预算 | 修正后真正传入 FTS 搜索的候选上限 |
| `VecCandidateLimit` | 向量候选数预算 | 修正后真正传入向量搜索的候选上限 |
| `UsedChannels` | 实际参与产出结果的通道 | 只记录最终结果来源，不记录“尝试过但未产出”的通道 |
| `FallbackPath` | 最终退化路径 | 为空表示未发生退化 |
| `ReasonCodes` | 决策理由码 | 用于回归门禁和线上日志解释 |
| `DurationMs` | 本次检索耗时 | 仅观测用途，不参与行为判定 |

---

## 3. 预算归一化规则

以下规则按 `searchWithPolicy(...)` 的当前实现顺序生效。

### 3.1 `Limit`

| 输入 | 归一化结果 |
| :--- | :--- |
| `Limit <= 0` | 强制修正为 `10` |
| `Limit > 0` | 保持原值 |

说明：

- `Limit` 是最终返回结果上限，也是后续候选预算修正的基准值。
- 任何“非正数 limit”不会报错，而是自动进入默认行为。

### 3.2 `MaxFTSCandidates`

| 输入条件 | 归一化结果 |
| :--- | :--- |
| `< Limit` | 修正为 `Limit * 2` |
| `>= Limit` | 保持原值 |

说明：

- 这里不是“至少等于 `Limit`”，而是直接提升到 `Limit * 2`。
- 负数、零、以及小于 `Limit` 的任意正数都落入同一归一化分支。

### 3.3 `MaxVectorCandidates`

| 输入条件 | 归一化结果 |
| :--- | :--- |
| `< Limit` | 修正为 `Limit * 2` |
| `>= Limit` | 保持原值 |

说明：

- 向量预算与 FTS 预算采用完全一致的归一化规则。

### 3.4 `WaitForCache`

| 输入 | 归一化结果 |
| :--- | :--- |
| `<= 0` | 修正为 `2s` |
| `> 0` | 保持原值 |

### 3.5 `AllowFTS` / `AllowVector`

| 输入组合 | 归一化结果 |
| :--- | :--- |
| 两者都为 `false` | 强制打开 `AllowFTS=true` |
| 其他情况 | 保持原值 |

说明：

- 当前实现不允许两个检索通道同时被彻底禁用。
- 当调用方同时关闭两者时，系统会静默回到 “至少保留 FTS” 的安全路径。

### 3.6 `VectorScoreThreshold`

| 输入条件 | 归一化结果 |
| :--- | :--- |
| `<= 0` 且有 `Category` | 修正为 `0.25` |
| `<= 0` 且无 `Category` | 修正为 `0.3` |
| `> 0` | 保持原值 |

说明：

- 类别内检索比全局检索使用更宽松的向量阈值。

---

## 4. 退化路径契约

### 4.1 通道合并优先级

当 FTS 和向量结果都存在时：

- `UsedChannels = ["fts", "vector"]`
- 使用 RRF 融合排序
- `FallbackPath = ""`

当只有向量结果存在时：

- `UsedChannels = ["vector"]`
- 若 FTS 失败或无结果，`FallbackPath = "vector_only"`
- `ReasonCodes` 追加 `fts_fallback`

当只有 FTS 结果存在时：

- `UsedChannels = ["fts"]`
- 若向量失败或无结果，`FallbackPath = "fts_only"`

### 4.2 类别列表回退

当满足以下条件时会触发 `category_list`：

1. FTS 与向量都没有产出结果。
2. 请求包含 `Category`。
3. `AllowListFallback = true`。

触发后的行为：

- 调用 `ListByCategory(...)`。
- 若结果数量超过 `Limit`，只保留排序后的前 `Limit` 条。
- `FallbackPath = "category_list"`。
- `ReasonCodes` 追加 `list_fallback`。
- `UsedChannels` 保持为空数组，因为最终结果不来自 FTS/Vector 通道。

### 4.3 无结果路径

若最终结果为空：

- `ReasonCodes` 追加 `no_results`
- `FallbackPath` 可能为空，也可能已记录某个退化分支但仍未命中结果

---

## 5. 类别列表排序契约

`category_list` 回退的结果不是“数据库原顺序”，而是稳定排序后的结果。

当前排序规则：

1. 先按类别专用打分排序。
2. 分数相同时按 `UpdatedAt` 降序。
3. 再按 `CreatedAt` 降序。
4. 最后按 `ID` 升序，作为稳定 tie-break。

对 `fact` 等默认类别，核心效果是：

- 更新更晚的记录优先。
- 在时间戳完全相同的情况下，按 `ID` 字典序稳定输出。

这也是为什么预算截断发生在 `category_list` 之后时，返回结果是“排序后的前 N 条”，而不是任意 N 条。

---

## 6. Reason Code 解释

| Code | 含义 |
| :--- | :--- |
| `contract_applied` | 已进入策略化检索契约 |
| `budget_applied` | 已完成预算归一化 |
| `fts_used` | FTS 通道被启用且成功完成调用 |
| `vector_used` | 向量通道参与最终结果产出 |
| `vector_unavailable` | 当前没有可用向量检索能力 |
| `cache_wait_timed_out` | 等待缓存超时 |
| `cache_wait_cancelled` | 等待缓存被取消 |
| `fts_fallback` | FTS 失效后回退为向量单通道 |
| `list_fallback` | 进入类别列表回退 |
| `category_filtered` | 本次搜索带类别约束 |
| `no_results` | 最终没有任何结果 |

注意：

- `fts_used` 表示 FTS 被真正调用成功，不等价于“FTS 一定提供了最终结果”。
- `UsedChannels` 才表示哪些通道最终参与了产出。

---

## 7. 离线评测覆盖面

当前数据驱动评测位于：

- `internal/memory/policy_eval_test.go`
- `internal/memory/testdata/policy_eval_cases.json`

已锁定的预算与退化行为包括：

1. `limit <= 0` 时默认化为 `10`。
2. `max_fts_candidates < limit` 时提升为 `limit * 2`。
3. `max_vector_candidates < limit` 时提升为 `limit * 2`。
4. 候选数已不低于 `limit` 时保持不变。
5. 类别列表回退会在排序后再按 `limit` 截断。
6. 同分条件下类别列表按 `ID` 稳定排序。

建议回归命令：

```bat
test_memory_regression.bat
```

若在类 Unix 环境中使用 Makefile：

```bash
make test-memory-regression
```

---

## 8. 排障建议

### 8.1 看到“候选数比请求值大”时

优先检查是否触发了预算归一化：

- 请求 `limit` 是否被修正为 `10`
- 候选数是否小于修正后的 `limit`

### 8.2 看到 `UsedChannels=[]` 但 `fts_used` 存在时

这通常表示：

- FTS 被成功执行过
- 但最终结果来自 `category_list` 回退，而不是 FTS/Vector 产出

这属于当前契约的预期行为，不是日志矛盾。

### 8.3 看到 `fallback_path=fts_only` 时

说明最终只有 FTS 产出结果。常见原因：

- 当前环境没有 embedder
- 向量通道返回空结果
- 向量通道报错

---

## 9. 变更约束

未来如果修改以下任一行为，必须同步更新：

1. `searchWithPolicy(...)` 中的预算归一化逻辑
2. `MemorySearchDecision` 的字段语义
3. `policy_eval_cases.json` 中的预算/回退样例
4. 本文档

否则会出现“代码行为、评测样例、排障文档”三者口径不一致的问题。