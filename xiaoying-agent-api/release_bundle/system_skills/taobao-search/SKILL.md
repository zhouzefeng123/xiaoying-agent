---
name: 淘宝搜索
description: 使用淘宝网页版搜索商品，支持自动登录与本地 Cookie 复用，可获取商品价格、销量、评价、店铺、商品链接等信息。
language: python
script_path: taobao_search.py
parameters:
  query:
    type: string
    description: 搜索查询内容，如"iPhone 15"、"运动鞋"、"笔记本电脑"。
    required: true
  max-results:
    type: integer
    description: 最大返回结果数量，默认 10。
    required: false
  timeout:
    type: integer
    description: 页面加载超时毫秒，默认 120000。
    required: false
  login-timeout:
    type: integer
    description: 等待人工登录超时毫秒，默认 300000。
    required: false
  headless:
    type: boolean
    description: 是否使用无头模式，默认 false（有头，便于人工扫码登录）。
    required: false
  enable_detail_enrich:
    type: boolean
    description: 是否启用详情补全（默认 false=关闭，即不进入商品详情页；true=进入商品详情页抓取额外信息）。
    required: false
  resolve-click-count:
    type: integer
    description: 点击卡片解析详情链接数量，默认 3。
    required: false
  resolve-url-count:
    type: integer
    description: 直接解析广告链接数量，默认 2。
    required: false
  sort:
    type: string
    description: 排序方式。可选：default（综合）、sales_desc（销量降序）、price_asc（价格升序）、price_desc（价格降序）。
    required: false
  min-interval-ms:
    type: integer
    description: 请求最小间隔毫秒（限流），默认 900。
    required: false
  retry-max-attempts:
    type: integer
    description: 最大重试次数（含首次），默认 3。
    required: false
  retry-base-delay-ms:
    type: integer
    description: 重试基础退避毫秒，默认 900。
    required: false
  retry-max-delay-ms:
    type: integer
    description: 重试最大退避毫秒，默认 7000。
    required: false
  circuit-fail-threshold:
    type: integer
    description: 连续失败达到阈值后触发熔断，默认 5。
    required: false
  circuit-cooldown-ms:
    type: integer
    description: 熔断冷却时间毫秒，默认 30000。
    required: false
  anti-risk-level:
    type: string
    description: 风控保护级别。可选：off（关闭保护）、low（低）、medium（中，默认）、high（高）。
    required: false
  anti-risk-jitter-ms:
    type: integer
    description: 每次请求附加随机抖动上限毫秒，默认 600（最终在 0~该值之间随机）。
    required: false
  anti-risk-break-seconds:
    type: integer
    description: 单次任务达到一定请求量后主动短暂休眠秒数，默认 8。
    required: false
  anti-risk-break-every:
    type: integer
    description: 每处理多少次页面/详情请求触发一次主动休眠，默认 12。
    required: false
  analysis-scope:
    type: string
    description: 分析范围。可选：all_results（全部结果）、detail_only（仅详情可抓取结果）。
    required: false
  analysis-output-mode:
    type: string
    description: 分析输出方式。可选：inline（仅返回JSON内）、separate（仅单独文件）、both（两者都输出，默认）。
    required: false
  analysis-output:
    type: string
    description: 分析结果输出文件路径（当 output-mode=separate/both 生效）。
    required: false
  output:
    type: string
    description: 完整搜索结果输出文件路径（JSON）。等价参数：results-output。
    required: false
  results-output:
    type: string
    description: 完整搜索结果输出文件路径（JSON）。等价参数：output。
    required: false
  price-bins:
    type: string
    description: 自定义价格区间，格式如 0-10,10-30,30-50,50-999999。
    required: false
  sales-bins:
    type: string
    description: 自定义销量区间，格式如 0-100,100-1000,1000-10000,10000-999999999。
    required: false
---

# 淘宝商品搜索 (Taobao Product Search)

这是一个基于淘宝网页版的商品搜索工具，支持自动登录和商品信息提取。

## 核心功能

*   **自动登录**：首次使用自动弹出浏览器窗口，扫码登录后保存 Cookie。
*   **商品搜索**：搜索淘宝商品，获取标题、价格、销量、评价、店铺、商品链接等信息。
*   **链接质量优化**：自动标注 `link_source`（detail/ad/store/chat/other），并避免将店铺/旺旺链接误当作可抓详情链接。
*   **智能认证**：自动处理登录状态，支持扫码登录。
*   **稳定性控制**：内置合规稳定层（限流、指数退避重试、熔断）并输出运行指标。
*   **风控防护**：支持风控保护级别、随机抖动与主动休眠，降低连续高频访问触发校验/拦截的概率。

## 使用场景

*   **商品比价**：搜索特定商品，比较不同店铺的价格。
*   **市场调研**：获取某类商品的销量、价格分布等信息。
*   **店铺分析**：查看特定店铺的商品列表和评价。

## 示例与参数说明

### 1. 简单搜索
```python
taobao_search(query="iPhone 15 手机壳")
```

### 2. 指定结果数量
```python
taobao_search(query="运动鞋", max_results=20)
```

### 3. 极速模式（关闭详情补全）
```python
taobao_search(query="机械键盘 青轴", max_results=15, enable_detail_enrich=False)
```

### 4. 更稳模式（增加链接解析尝试）
```python
taobao_search(
  query="手机壳",
  max_results=12,
  resolve_click_count=6,
  resolve_url_count=6,
  timeout=180000,
  login_timeout=600000
)
```

### 5. 排序模式
```python
# 销量从高到低
taobao_search(query="手机壳", sort="sales_desc")

# 价格从低到高
taobao_search(query="手机壳", sort="price_asc")

# 价格从高到低
taobao_search(query="手机壳", sort="price_desc")
```

### 6. 稳定模式（限流 + 重试 + 熔断）
```python
taobao_search(
  query="手机壳",
  max_results=12,
  min_interval_ms=1200,
  retry_max_attempts=3,
  retry_base_delay_ms=1000,
  retry_max_delay_ms=8000,
  circuit_fail_threshold=5,
  circuit_cooldown_ms=30000
)
```

### 7. 风控友好模式（推荐线上默认）
```python
taobao_search(
  query="手机壳",
  max_results=12,
  anti_risk_level="medium",
  anti_risk_jitter_ms=600,
  anti_risk_break_every=12,
  anti_risk_break_seconds=8,
  min_interval_ms=1200,
  retry_max_attempts=3,
  retry_base_delay_ms=1000,
  retry_max_delay_ms=8000,
  circuit_fail_threshold=5,
  circuit_cooldown_ms=30000
)
```

### 8. 全面分析模式（含摘要/区间/Top/异常值）
```python
taobao_search(
  query="手机壳",
  max_results=30,
  sort="sales_desc",
  analysis-scope="all_results",
  analysis-output-mode="both",
  analysis-output="./taobao_analysis.json",
  price-bins="0-10,10-30,30-50,50-100,100-999999",
  sales-bins="0-100,100-1000,1000-10000,10000-100000,100000-999999999"
)
```

### 9. 自定义保存完整搜索结果
```python
taobao_search(
  query="手机壳",
  max_results=20,
  output="./taobao_results.json"
)
```

### 10. 无头模式（已登录且不需要人工交互时）
```python
taobao_search(query="蓝牙耳机", headless=True)
```

## 风控参数建议（重要）

为降低触发风控（滑块/验证码/短时拦截）的概率，建议按以下档位使用：

- `anti-risk-level=low`：本地低频调试（偶发查询）。
- `anti-risk-level=medium`：日常使用推荐默认（平衡速度与稳定）。
- `anti-risk-level=high`：连续任务/批量场景（更慢但更稳）。
- `anti-risk-level=off`：仅用于可控环境压测，不建议生产使用。

建议组合（生产默认）：

- `min-interval-ms >= 1200`
- `retry-max-attempts = 2~3`
- `retry-base-delay-ms >= 1000`
- `retry-max-delay-ms >= 8000`
- `anti-risk-jitter-ms = 400~800`
- `anti-risk-break-every = 10~15`
- `anti_risk_break_seconds = 6~12`
- `circuit_fail_threshold = 4~6`
- `circuit_cooldown_ms = 30000~60000`

## 注意事项

1. 首次使用会弹出浏览器窗口，需要扫码登录淘宝账号。
2. 登录成功后 Cookie 会自动保存，后续无需重复登录。
3. 如果遇到验证码，请在浏览器中手动完成验证后点击"我已完成登录"。
4. 若短时间内频繁出现验证码/访问受限，请提高 `anti_risk_level`，增大 `min_interval_ms` 与 `anti_risk_break_seconds`，并适当降低 `max_results`。
5. 运行结果中会包含 `stability.metrics`，可用于观察稳定性：如 `retries`、`failures`、`circuit_open_rejects`、`rate_wait_ms`。