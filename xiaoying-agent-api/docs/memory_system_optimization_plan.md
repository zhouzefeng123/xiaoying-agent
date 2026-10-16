# 记忆系统优化与路线图

本文档概述了 AuraMate 集成知识树系统 (IKTS) 的优化方案，旨在提升检索效率、相关性和上下文质量。

## 现状分析

当前系统采用了 **分层意图驱动的 RAG (Layered Intent-Driven RAG)** 方法：
- **L1 (核心身份/偏好)**：始终加载 (静态获取)。
- **L2 (经验/安全)**：针对 `Task` (任务) 和 `Coding` (编程) 意图加载 (静态获取)。
- **L3 (知识)**：针对 `Search` (搜索)、`Chat` (闲聊) 和 `Coding` (编程) 意图，通过语义搜索加载 (限制 5 条)。
- **L4 (历史)**：基于对话上下文自适应加载。

**已识别的瓶颈：**
1.  **静态加载扩展性问题**：`ListByCategory` 会获取某一类别下的 *所有* 条目。随着用户数据增长 (例如 >50 条偏好或 >100 条经验)，这将降低性能并消耗过多的 Token。
2.  **相关性衰减**：陈旧、可能已过时的记忆与最近的记忆具有相同的权重。
3.  **图谱利用率不足**：现有的 `knowledge_edges` 表虽然已填充数据，但在检索过程中未被主动使用，导致缺失“关联性”上下文。

---

## 第一阶段：动态阈值与混合过滤 (高优先级)

**目标**：在保持关键规则高召回率的同时，防止随着记忆增长导致的上下文膨胀。

### 策略
1.  **带语义回退的硬限制**：
    - 定义一个 `MaxCategoryLoad` 阈值 (例如 20 条)。
    - 如果 `Count(Category) <= Threshold`：加载全部 (维持现状，快速且全面)。
    - 如果 `Count(Category) > Threshold`：切换到该类别内的 **语义搜索**，使用当前用户查询进行检索。
    
2.  **实现逻辑 (伪代码)**：
    ```go
    // agent_context.go 伪代码
    const MaxCategoryItems = 20
    
    func (a *agent) loadCategory(ctx context.Context, category string, query string) []Memory {
        count, _ := a.memories.CountByCategory(ctx, category)
        
        if count <= MaxCategoryItems {
            // 快速路径：加载全部
            return a.memories.ListByCategory(ctx, category)
        } else {
            // 回退路径：类别范围内的语义搜索
            // 需要在 Search() 中添加过滤参数
            return a.memories.Search(ctx, query, MaxCategoryItems, WithCategory(category))
        }
    }
    ```

### 预期成果
- **Token 用量稳定**：无论数据库规模如何，上下文大小保持可预测。
- **检索相关性**：当用户拥有大量经验时，仅加载相关的经验。

---

## 第二阶段：时间衰减相关性评分 (中优先级)

**目标**：优先考虑最近且频繁访问的记忆，而非陈旧、停滞的记忆。

### 策略
1.  **评分公式**：
    当前的排名主要依赖向量相似度 (Cosine)。我们将引入 `RecencyBoost` (新鲜度提升)。
    $$ Score = (VectorSimilarity \times 0.7) + (RecencyScore \times 0.2) + (AccessCountScore \times 0.1) $$
    
    *   `RecencyScore`: $ \frac{1}{1 + \log(HoursSinceCreation)} $
    *   `AccessCountScore`: `access_count` 的对数缩放。

2.  **实现**：
    - 更新 `internal/memory/service.go` 的 `Search` 函数，在获取向量候选后，将这些因素纳入排名逻辑。

### 预期成果
- **新鲜度**：Agent 更倾向于最近的项目上下文，而不是一年前已废弃的规则。
- **适应性**：频繁使用的规则权重更高。

---

## 第三阶段：GraphRAG 图谱扩展 (高级)

**目标**：利用 `knowledge_edges` 表发现那些与查询不直接相似但逻辑上相关的“隐式”上下文。

### 策略
1.  **两步检索**：
    - **步骤 1**：执行标准向量搜索以找到“锚点节点” (直接命中)。
    - **步骤 2**：查询 `knowledge_edges` 以找到与锚点节点相连的节点。
    
2.  **扩展逻辑**：
    - 对于每个 Top-3 锚点节点：
        - 获取出边 (例如 `related_to`, `requires`, `part_of`)。
        - 如果 `EdgeWeight > Threshold` (例如 0.8)，则包含目标节点。
    
    ```sql
    -- 扩展示例查询
    SELECT t.* FROM knowledge_tree t
    JOIN knowledge_edges e ON e.target_id = t.id
    WHERE e.source_id = ? AND e.weight > 0.8
    ```

3.  **上下文注入**：
    - 在系统提示词中将其呈现为“相关上下文”：
      > "Found relevant memory: 'Use Gin for HTTP'.
      >  -> Related: 'Gin requires Go 1.18+' (via 'requires' edge)."

### 预期成果
- **深度上下文**：解决“未知的未知”问题——找到用户未明确询问但在知识图谱中存在关联的依赖项或先决条件。

---

## 行动计划总结

| 阶段 | 功能 | 复杂度 | 影响 | 状态 |
| :--- | :--- | :--- | :--- | :--- |
| **1** | **动态阈值** | 低 | 高 (可扩展性) | 已提议 |
| **2** | **时间衰减评分** | 中 | 中 (质量) | 已提议 |
| **3** | **GraphRAG 扩展** | 高 | 高 (推理能力) | 已提议 |

**后续步骤**：
1.  批准第一阶段的实施。
2.  重构 `memory.Service` 以支持带范围的语义搜索 (`SearchByCategory`)。
