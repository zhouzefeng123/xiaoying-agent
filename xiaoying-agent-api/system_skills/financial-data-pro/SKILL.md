---
name: financial-data-pro
description: 基于 Tushare Pro 接口的综合金融数据工具。已针对速度进行深度优化：K线数据（kline）和实时行情（realtime_...）优先使用极速新浪接口。支持个股全维度并发分析（full_analysis）、多维度选股（量化筛选/技术指标筛选）及实时爬虫接口。
language: python
script_path: financial_data_pro.py
parameters:
  action:
    type: string
    description: "数据接口类型。如 'full_analysis' (个股分析), 'kline' (K线), 'realtime_quote' (实时行情), 'select' (选股) 等。"
    required: true
  symbol:
    type: string
    description: "标的代码 (如 '600519.SH')。大部分接口必填。"
    required: false
  period:
    type: string
    description: "周期 ('daily', 'weekly', 'monthly')。默认 'daily'。"
    required: false
  start_date:
    type: string
    description: "开始日期 (YYYYMMDD)。"
    required: false
  end_date:
    type: string
    description: "结束日期 (YYYYMMDD)。"
    required: false
  freq:
    type: string
    description: "分时频率 (如 '5min')。仅 'mins' 接口需要。"
    required: false
  indicators:
    type: string
    description: "技术指标列表 (如 'ma,macd')。仅 'kline' 和 'tech_select' 需要。"
    required: false
  condition:
    type: string
    description: "筛选条件表达式 (如 'ma5 > ma10')。仅选股接口需要。"
    required: false
  pool:
    type: string
    description: "股票池 (如 '000300.SH' 或 'all')。仅选股接口需要。"
    required: false
  limit:
    type: integer
    description: "返回结果数量限制。默认 20。"
    required: false
  src:
    type: string
    description: "数据源 ('sina', 'dc')。仅实时接口需要。"
    required: false
  token:
    type: string
    description: "Tushare Token (可选)。"
    required: false
---

# 金融数据大师 (Financial Data Pro)

本技能提供经过深度性能优化的金融数据接口，旨在提供秒级的个股分析和选股体验。

## 性能与调用指南 (Performance & Best Practices)
- **个股分析 (唯一推荐)**: 如果需要分析**单只**股票，**必须且仅需**调用 `action='full_analysis'`。该接口已内置并发获取实时行情、K线、资金流、每日指标。**严禁**在调用 `full_analysis` 的同时并行调用 `kline` 或 `realtime_quote`，这会导致严重的数据冗余和处理延迟。
- **严禁误用**: 请勿使用 `tech_select` 或 `intraday_select` 来分析单只股票。这些工具是为全市场/股票池筛选设计的，会产生巨大的网络开销和延迟。
- **极速响应**: K线数据 (`action='kline'`) 和实时行情已切换至新浪财经直连接口，单次请求响应时间通常 < 1s。
- **高可靠暴力扫描**: `realtime_select` 支持全市场 6000+ 股票的秒级暴力筛选。该接口具备**冗余备份机制**：优先使用极速新浪接口，若新浪受限则自动无缝切换至 Tushare 接口，确保选股功能永不掉线。

## 参数说明

- `action` (必填): 要调用的数据接口。
    - **个股综合分析**: `full_analysis` (一键获取实时盘口、K线、资金流向及每日指标。**单股分析唯一推荐接口**，严禁与其他单股接口并行使用)。
    - **市场行情**: `kline` (日线K线), `index` (指数), `futures` (期货), `fund` (基金), `mins` (分时线)。
    - **实时接口**: `realtime_quote` (实时盘口, 免费), `realtime_list` (实时涨跌幅榜, 免费)。
    - **基本面**: `daily_basic` (每日指标), `income` (利润表), `balance` (资产负债表), `cashflow` (现金流量表), `info` (基础信息)。
    - **市场监控**: `moneyflow` (资金流向), `limit_list` (涨跌停), `trade_cal` (交易日历)。
    - **智能选股 (股票池筛选)**: `intraday_select` (分时策略选股), `tech_select` (技术指标筛选), `select` (基本面筛选)。
    - **资讯**: `info` (个股信息), `news` (新闻), `concept` (概念)。
- `token` (选填): 用户自定义 Tushare Token。
- `symbol` (选填): 标的代码 (如 '600519.SH', '000001.SH', 'IF.CFX', 'USDCNH.FX', 'gold'); 大部分接口必填。
- `src` (选填): 仅用于实时爬虫接口 (`realtime_...`)。'sina' (默认) 或 'dc' (东方财富)。
- `period` (选填): 用于财报或K线周期: 'daily', 'weekly', 'monthly' 或报告期 (如 '20231231')。默认 'daily'。
- `start_date` (选填): 开始日期 (格式 YYYYMMDD)。默认: 最近。
- `end_date` (选填): 结束日期 (格式 YYYYMMDD)。默认: 当前。
- `freq` (选填): 用于 `mins`，例如 '1min', '5min', '30min' (默认 '1min')。
- `indicators` (选填): 
    - 用于 `kline`: 计算的技术指标列表 (如 'ma,macd,rsi,kdj,boll') 或 'all'。
    - 用于 `tech_select`: 需要计算并用于筛选的指标。
- `condition` (选填): 筛选条件表达式 (Python语法)。
    - 用于 `tech_select`: 支持 `ma5`, `ma10`, `macd`, `rsi6`, `close` 等。示例: `'ma5 > ma10 and rsi6 < 30'`。
    - 用于 `realtime_select`: 支持 `pct_chg`, `turnover_rate`, `price`, `pe`, `total_mv` 等。示例: `'pct_chg > 5 and turnover_rate > 3'`。
    - 用于 `intraday_select`: 
        - 策略 `late_pull`: 支持 `last_hour_chg` (最后1小时涨幅%), `last_hour_vol_ratio` (最后1小时量比, 0-1)。
        - 示例: `'last_hour_chg > 2.0 and last_hour_vol_ratio > 0.25'` (筛选尾盘涨幅超2%且量比超25%的股票)。
- `limit` (选填): 返回结果数量限制 (默认 20)。

### 选股参数 (用于 `action='select'`)
- `pe_range` (选填): 市盈率 (TTM) 范围 'min,max' (如 '0,20')。
- `pb_range` (选填): 市净率范围 'min,max' (如 '0,3')。
- `mv_range` (选填): 总市值范围 (单位: 万元) 'min,max' (如 '1000000,5000000' 代表100亿-500亿)。
- `market` (选填): 市场板块 (如 '主板', '创业板', '科创板')。
- `industry` (选填): 行业名称 (如 '软件服务', '银行')。
- `area` (选填): 地域 (如 '深圳', '上海')。

### 智能选股参数 (用于 `action='intraday_select'` 等)
- `pool` (选填): 股票池。
    - For `intraday_select`: 'gainers' (默认，涨幅榜), 'turnover' (换手率榜), 'all' (大市值)。
- `strategy` (选填): 选股策略。
    - For `intraday_select`: 'late_pull' (默认，尾盘拉升)。
- `condition` (选填): 筛选条件表达式 (Python语法)。
    - For `intraday_select`: 自定义策略阈值。支持变量见上文。

### 技术选股参数 (用于 `action='tech_select'`)
- `pool` (必填): 股票池。可以是指数代码 (如 '000300.SH' 沪深300), 'all' (全市场前300市值), 或逗号分隔的代码列表 ('600519.SH,000001.SZ')。
- `condition` (必填): 筛选条件表达式 (Python语法)。支持指标变量: `close`, `open`, `ma5`, `ma10`, `macd`, `rsi6`, `k`, `d`, `j` 等。
    - 示例: `'ma5 > ma10 and rsi6 < 30'` (均线金叉且RSI超卖)
- `indicators` (必填): 需要计算的指标列表 (如 'ma,rsi')，必须包含 condition 中用到的指标。

## 技术指标 (本地计算)
支持的指标 (用于 `indicators` 参数):
- **MA (移动平均)**: MA5, MA10, MA20, MA30, MA60。
- **MACD (指数平滑异同移动平均)**: DIFF, DEA, MACD (12, 26, 9)。
- **RSI (相对强弱指标)**: RSI6, RSI12, RSI24。
- **KDJ (随机指标)**: K, D, J (9, 3, 3)。
- **BOLL (布林带)**: MID, UP, LOW (20, 2)。

## 使用示例

### 1. 个股全维度分析 (推荐)
```python
# 唯一推荐方式：一键获取单股所有维度数据。无需再调用 kline 或 realtime_quote。
financial-data-pro(action="full_analysis", symbol="600519.SH")
```

### 2. 实时行情
```python
# 获取茅台实时盘口 (免费)
financial-data-pro(action="realtime_quote", symbol="600519.SH")

# 获取实时涨跌幅榜 (免费)
financial-data-pro(action="realtime_list", src="sina")

# 获取实时分钟线 (免费，新浪接口)
financial-data-pro(action="mins", symbol="600519.SH", freq="5min")
```

### 3. 市场行情 (Tushare Pro)
```python
# 获取茅台日线数据，并计算 MACD 和 MA
financial-data-pro(action="kline", symbol="600519.SH", indicators="ma,macd")

# 获取上证指数日线
financial-data-pro(action="index", symbol="000001.SH")
```

### 4. 智能选股 (基本面/量化)
```python
# 选出 PE < 20, PB < 3, 市值 > 100亿, 软件服务行业的股票
financial-data-pro(
    action="select", 
    pe_range="0,20", 
    pb_range="0,3", 
    mv_range="1000000,99999999", 
    industry="软件服务"
)
```

### 5. 技术指标选股 (实时计算)
```python
# 在沪深300成分股中，筛选 MA5 > MA10 且 RSI6 < 30 的股票
financial-data-pro(
    action="tech_select",
    pool="000300.SH",
    indicators="ma,rsi",
    condition="ma5 > ma10 and rsi6 < 30"
)
```

### 6. 智能选股 (新功能)
```python
# 实时选股: 涨幅>5, 换手>3, 价格<20
financial-data-pro(
    action="realtime_select",
    condition="pct_chg > 5 and turnover_rate > 3 and price < 20"
)

# 尾盘拉升策略: 在涨幅榜前列中寻找尾盘(14:00后)放量拉升的股票
# 可使用 condition 自定义阈值 (默认 > 1.5% 且 > 0.20)
financial-data-pro(
    action="intraday_select",
    strategy="late_pull",
    pool="gainers",
    condition="last_hour_chg > 2.0 and last_hour_vol_ratio > 0.25"
)
```


