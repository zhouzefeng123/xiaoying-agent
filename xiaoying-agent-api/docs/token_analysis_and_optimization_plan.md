# Token 统计分析与优化方案报告

## 1. 现状分析 (Current State Analysis)

基于对 `test_sota_diversity_v4.py` 脚本及后端 Agent 逻辑的深度审查，我们对当前的 Token 统计机制进行了以下评估：

### 1.1 统计逻辑准确性验证
脚本采用了流式累积的统计方式：
```python
scenario_input = max(scenario_input, usage.get('prompt_tokens', 0))
scenario_output = max(scenario_output, usage.get('completion_tokens', 0))
```
- **合理性**：后端 Agent 已被修复为发送**会话级累积**的 Token Usage（即 `prompt_tokens` 包含了当前步骤 + 之前所有步骤的消耗）。因此，脚本中使用 `max()` 获取流中出现的最大值，能够准确捕获该场景下的最终总消耗。
- **缓存计算**：
  - 脚本正确处理了 `prompt_tokens_details.cached_tokens` 和扁平化 `cached_tokens` 两种格式。
  - 命中率公式 `(TOTAL_CACHED_TOKENS / TOTAL_INPUT_TOKENS) * 100` 是准确的，其中 `TOTAL_INPUT_TOKENS` (Prompt Tokens) 按照 OpenAI 标准定义已包含 Cached Tokens，分母定义正确。

### 1.2 现有指标的局限性
虽然当前的统计逻辑准确，但在全面评估 SOTA（State of the Art）性能方面存在缺失：
1.  **缺乏成本维度**：仅统计了 Token 数量，未结合模型单价计算实际成本。不同模型（如 Qwen-Turbo vs Qwen-Max）的 Cached/Uncached 价格差异巨大，单纯看 Token 数无法反映“降本”效果。
2.  **缺乏延迟指标**：
    - **TTFT (Time To First Token)**：首字延迟是衡量用户体验的关键指标，脚本目前未统计。
    - **TPS (Tokens Per Second)**：生成速度（Output Tokens / Duration）未统计。
3.  **场景覆盖不足**：
    - 当前 4 个场景（PPT, Excel, Image, Travel）均为**单轮对话**（Single-turn）。
    - **缓存机制的短板**：单轮对话主要测试 System Prompt 的缓存命中。真正的 Context Caching 优势在于**多轮对话**（Multi-turn）中对历史消息的缓存。当前测试无法验证长对话下的缓存收益。

---

## 2. 优化方案 (Optimization Plan)

为了更全面地评估 Agent 的性能与成本效益，建议对测试脚本进行以下维度的优化。

### 2.1 指标增强 (Metric Enhancement)

#### A. 引入成本计算 (Cost Estimation)
在脚本中内置主流模型的定价表（以 Qwen 为例），实时计算预估成本。

**新增统计项**：
- `Cost (Input)`: Uncached Input * Price_In
- `Cost (Output)`: Output * Price_Out
- `Cost (Cache)`: Cached Input * Price_Cache_Read + (New Cache Creation * Price_Cache_Create)
- **`Total Cost`**: 上述之和。
- **`Cost Savings`**: (Total Input * Price_In) - (Actual Input Cost)，即展示缓存带来的节省金额。

#### B. 引入性能指标 (Performance Metrics)
利用流式响应的特性，精确采集时间戳。

**新增统计项**：
- **TTFT (ms)**：从发送请求到收到第一个 `data: ` 块的时间差。
- **TPS (Tokens/s)**：`Total Output Tokens / (Total Duration - TTFT)`。反映纯生成阶段的速度。

### 2.2 场景扩展 (Scenario Expansion)

#### C. 增加“多轮对话”测试场景 (Multi-turn Scenario)
为了验证 Context Caching (History) 的效果，必须增加一个包含上下文依赖的测试。

**建议新增场景**：
- **名称**：`Coding Debug Session`
- **流程**：
    1.  User: "这里有一段 Python 代码 [代码块...]，请解释它的作用。" (Step 1)
    2.  User: "这段代码有一个 Bug，请修复它。" (Step 2 - 此时应命中 Step 1 的历史缓存)
    3.  User: "请为修复后的代码编写单元测试。" (Step 3 - 此时应命中 Step 1+2 的历史缓存)
- **预期结果**：Step 2 和 Step 3 的 Cache Hit Rate 应显著高于 Step 1。

### 2.3 输出优化 (Reporting)

#### D. CSV 报告生成
将测试结果追加写入 `performance_report.csv`，包含时间戳、模型版本、各项 Token 指标和成本。这有助于长期追踪优化效果（Regression Testing）。

---

## 3. 实施计划 (Implementation Steps)

1.  **配置定价表**：在脚本头部定义 `PRICING_MODEL` 常量字典。
2.  **重构 `run_test_scenario`**：
    - 增加 `first_token_time` 变量记录 TTFT。
    - 增加 `step_usage_list` 记录多步交互的明细（针对多轮对话场景）。
3.  **新增 `Scenario 5: Multi-turn Coding`**：编写多轮对话的测试逻辑。
4.  **生成 CSV**：使用 Python `csv` 模块输出结构化数据。

## 4. 预期效果示例

优化后的输出将包含如下关键信息：

```text
[SCENARIO] Coding Debug Session (Multi-turn)
- Step 1: Input 500, Cached 0 (0%), Cost $0.002, TTFT 800ms
- Step 2: Input 800, Cached 500 (62%), Cost $0.001, TTFT 300ms (Cache Hit!)
- Step 3: Input 1200, Cached 1100 (91%), Cost $0.001, TTFT 250ms (Cache Hit!)
------------------------------------------------
Total Cost: $0.004 (Saved 45% via Caching)
Avg TPS: 45.2 tokens/s
```
