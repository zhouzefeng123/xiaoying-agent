"""@tool financial-data-pro
@description Enhanced financial data interface using Tushare Pro API. Supports real-time A-share index quotes, stock K-line data with technical indicators (MA, MACD, RSI, KDJ, BOLL), news, index, futures, and currency.
@param symbol str (Optional) Stock/index/futures/currency symbol (e.g., '600519.SH', '000001.SH', 'IF.CFX', 'USDCNH.FX'); required for most actions.
@param action str (Required) One of: 'index', 'stock', 'kline', 'info', 'news', 'trade_cal', 'concept', 'futures', 'currency', 'income', 'balance', 'cashflow', 'options', 'fund', 'daily_basic', 'futures_holding', 'metals', 'adj_factor', 'limit_list', 'moneyflow', 'stk_managers', 'select', 'tech_select', 'mins', 'realtime_quote', 'realtime_tick', 'realtime_list', 'realtime_select', 'intraday_select'.
@param token str (Optional) Tushare Pro API Token. If provided, uses this token instead of default.
@param src str (Optional) For realtime actions: 'sina' or 'dc' (default 'sina').
@param period str (Optional) For 'kline'/'income'/'balance'/'cashflow': 'daily', 'weekly', 'monthly' or report period. Default 'daily'.
@param start_date str (Optional) For 'kline'/'futures'/'currency': 'YYYYMMDD'. Default: recent.
@param end_date str (Optional) For 'kline'/'futures'/'currency'/'limit_list'/'select': 'YYYYMMDD'. Default: current.
@param indicators str (Optional) For 'kline'/'tech_select': comma-separated list of indicators to calculate (e.g., 'ma,macd,rsi,kdj,boll') or 'all'.
@param pe_range str (Optional) For 'select': PE range 'min,max' (e.g. '0,20').
@param pb_range str (Optional) For 'select': PB range 'min,max' (e.g. '0,3').
@param mv_range str (Optional) For 'select': Market Value (10k) range 'min,max'.
@param market str (Optional) For 'select': Market type (e.g. '主板', '创业板').
@param industry str (Optional) For 'select': Industry name.
@param area str (Optional) For 'select': Region.
@param pool str (Optional) For 'tech_select'/'intraday_select': Stock pool.
- For 'tech_select': '000300.SH', 'all', '600519.SH,000001.SZ'.
- For 'intraday_select': 'gainers' (default), 'turnover', 'all'.
@param condition str (Optional) For 'tech_select'/'realtime_select': Condition expression.
- For 'tech_select': 'ma5 > ma10 and rsi6 < 30'.
- For 'realtime_select': 'pct_chg > 5 and turnover_rate > 3 and price < 20'.
- For 'intraday_select': (Currently unused, strategy controlled by action logic).
@param limit int (Optional) Limit results (default 20).

# Financial Data Pro Skill (Tushare Edition)

This skill provides a robust financial data interface using Tushare Pro.
Features:
- **Index/Stock Daily**: Historical OHLCV data (`action='kline'` or `'stock'`).
- **Index Daily**: Index K-line data (`action='index'`).
- **Info**: Individual stock basic information (`action='info'`).
- **News**: Financial news (`action='news'`).
- **Trade Calendar**: Exchange trade calendar (`action='trade_cal'`).
- **Concept**: Concept stocks (`action='concept'`).
- **Futures**: Futures daily data (`action='futures'`).
- **Currency**: Forex daily data (`action='currency'`).

## Technical Indicators
Supported indicators (use in `indicators` parameter):
- **MA (Moving Average)**: Calculates MA5, MA10, MA20, MA30, MA60.
- **MACD (Moving Average Convergence Divergence)**: Calculates DIFF, DEA, MACD (12, 26, 9).
- **RSI (Relative Strength Index)**: Calculates RSI6, RSI12, RSI24.
- **KDJ (Stochastic Oscillator)**: Calculates K, D, J (9, 3, 3).
- **BOLL (Bollinger Bands)**: Calculates BOLL_MID, BOLL_UP, BOLL_LOW (20, 2).

## Usage
`financial-data-pro(action="kline", symbol="600519.SH", indicators="ma,macd")`
`financial-data-pro(action="index", symbol="000001.SH")`
`financial-data-pro(action="futures", symbol="IF.CFX", start_date="20240101")`
`financial-data-pro(action="currency", symbol="USDCNH.FX", start_date="20240101")`

## Returns
A JSON object with keys: "status", "data", and "message".
"""

import sys
import json
import os
from typing import Dict, Any, Optional

# Set default encoding to UTF-8 for Windows compatibility
sys.stdout.reconfigure(encoding='utf-8')

# Import local modules
try:
    from config import get_pro_client
    from market import (
        fetch_kline, fetch_index_daily, fetch_futures_daily, fetch_currency_daily,
        fetch_opt_daily, fetch_fund_daily, fetch_gold_daily, fetch_mins,
        fetch_realtime_quote
    )
    from fundamental import (
        fetch_income, fetch_balance, fetch_cashflow, fetch_daily_basic,
        fetch_adj_factor, fetch_stk_managers
    )
    from monitor import (
        fetch_daily_limit, fetch_moneyflow, fetch_futures_holding, fetch_trade_cal,
        fetch_realtime_list
    )
    from selection import fetch_stock_selection, fetch_tech_select, fetch_realtime_select, fetch_intraday_select
    from info import fetch_info, fetch_news
except ImportError as e:
    print(json.dumps({
        "status": "error",
        "message": f"Module import error: {str(e)}. Ensure all script files are in the same directory."
    }))
    sys.exit(0)

def financial_data_pro(
    action: str,
    symbol: Optional[str] = None,
    period: str = "daily",
    start_date: str = "",
    end_date: str = "",
    indicators: str = "",
    freq: str = "1min", # Added freq for mins/rt_min
    pe_range: str = "",
    pb_range: str = "",
    mv_range: str = "",
    market: str = "",
    industry: str = "",
    area: str = "",
    pool: str = "",
    condition: str = "",
    token: str = "",
    src: str = "sina",
    limit: int = 20,
    strategy: str = "late_pull" # Added strategy parameter
) -> Dict[str, Any]:
    try:
        pro = get_pro_client(token)
        
        if action == "realtime_quote":
            if not symbol: return {"status": "error", "message": "Missing 'symbol' (e.g. 600519.SH)."}
            data = fetch_realtime_quote(symbol, src)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]:
                 return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}

        elif action == "realtime_list":
            data = fetch_realtime_list(src)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]:
                 return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}

        elif action == "kline" or action == "stock":
            if not symbol: return {"status": "error", "message": "Missing 'symbol'."}
            data = fetch_kline(pro, symbol, start_date, end_date, indicators)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]:
                 return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}
        
        elif action == "index":
            if not symbol: return {"status": "error", "message": "Missing 'symbol' (e.g. 000001.SH)."}
            data = fetch_index_daily(pro, symbol, start_date, end_date)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]:
                 return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}

        elif action == "futures":
            if not symbol: return {"status": "error", "message": "Missing 'symbol' (e.g. IF.CFX)."}
            data = fetch_futures_daily(pro, symbol, start_date, end_date)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]:
                 return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}

        elif action == "currency":
            if not symbol: return {"status": "error", "message": "Missing 'symbol' (e.g. USDCNH.FX)."}
            data = fetch_currency_daily(pro, symbol, start_date, end_date)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]:
                 return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}

        elif action == "full_analysis":
            if not symbol: return {"status": "error", "message": "Missing 'symbol'."}
            import concurrent.futures
            
            # Run data fetching tasks in parallel for maximum speed
            with concurrent.futures.ThreadPoolExecutor(max_workers=4) as executor:
                f_quote = executor.submit(fetch_realtime_quote, symbol, src)
                f_kline = executor.submit(fetch_kline, pro, symbol, start_date, end_date, indicators)
                f_basic = executor.submit(fetch_daily_basic, pro, symbol, end_date)
                f_money = executor.submit(fetch_moneyflow, pro, symbol, start_date, end_date)
                
                results = {
                    "realtime": f_quote.result(),
                    "kline": f_kline.result(),
                    "daily_basic": f_basic.result(),
                    "moneyflow": f_money.result()
                }
            return {"status": "success", "data": results}

        elif action == "income":
            if not symbol: return {"status": "error", "message": "Missing 'symbol'."}
            data = fetch_income(pro, symbol, start_date, end_date, period)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]: return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}

        elif action == "balance":
            if not symbol: return {"status": "error", "message": "Missing 'symbol'."}
            data = fetch_balance(pro, symbol, start_date, end_date, period)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]: return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}

        elif action == "cashflow":
            if not symbol: return {"status": "error", "message": "Missing 'symbol'."}
            data = fetch_cashflow(pro, symbol, start_date, end_date, period)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]: return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}

        elif action == "options":
            if not symbol: return {"status": "error", "message": "Missing 'symbol'."}
            data = fetch_opt_daily(pro, symbol, start_date, end_date)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]: return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}

        elif action == "fund":
            if not symbol: return {"status": "error", "message": "Missing 'symbol'."}
            data = fetch_fund_daily(pro, symbol, start_date, end_date)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]: return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}

        elif action == "daily_basic":
            if not symbol: return {"status": "error", "message": "Missing 'symbol'."}
            data = fetch_daily_basic(pro, symbol, end_date) # use end_date as trade_date
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]: return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}

        elif action == "futures_holding":
            if not symbol: return {"status": "error", "message": "Missing 'symbol'."}
            data = fetch_futures_holding(pro, symbol, start_date, end_date)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]: return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}

        elif action == "metals":
             data = fetch_gold_daily(pro, symbol if symbol else "gold", start_date, end_date)
             if isinstance(data, list) and len(data) > 0 and "error" in data[0]: return {"status": "error", "message": data[0]['error']}
             return {"status": "success", "data": data}

        elif action == "info":
            if not symbol: return {"status": "error", "message": "Missing 'symbol'."}
            data = fetch_info(pro, symbol)
            if "error" in data: return {"status": "error", "message": data['error']}
            return {"status": "success", "data": data}
            
        elif action == "trade_cal":
            data = fetch_trade_cal(pro, start_date, end_date)
            return {"status": "success", "data": data}
            
        elif action == "news":
            data = fetch_news(pro, symbol, end_date, limit)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]: return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}
        
        elif action == "adj_factor":
            if not symbol: return {"status": "error", "message": "Missing 'symbol'."}
            data = fetch_adj_factor(pro, symbol, start_date, end_date)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]: return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}

        elif action == "limit_list":
            data = fetch_daily_limit(pro, symbol, end_date)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]: return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}

        elif action == "moneyflow":
            if not symbol: return {"status": "error", "message": "Missing 'symbol'."}
            data = fetch_moneyflow(pro, symbol, start_date, end_date)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]: return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}

        elif action == "stk_managers":
            if not symbol: return {"status": "error", "message": "Missing 'symbol'."}
            data = fetch_stk_managers(pro, symbol)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]: return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}

        elif action == "select":
            filters = {
                'pe_range': pe_range,
                'pb_range': pb_range,
                'mv_range': mv_range,
                'market': market,
                'industry': industry,
                'area': area
            }
            data = fetch_stock_selection(pro, end_date, filters, limit)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]: return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}

        elif action == "tech_select":
            if not pool: return {"status": "error", "message": "Missing 'pool'."}
            if not indicators: return {"status": "error", "message": "Missing 'indicators'."}
            if not condition: return {"status": "error", "message": "Missing 'condition'."}
            data = fetch_tech_select(pro, pool, indicators, condition, limit)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]: return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}

        elif action == "realtime_select":
            if not condition: return {"status": "error", "message": "Missing 'condition' (e.g. 'pct_chg > 5')."}
            data = fetch_realtime_select(pro, condition, limit)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]: return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}

        elif action == "intraday_select":
            # Default pool to 'gainers' if not provided
            if not pool: pool = 'gainers'
            # Strategy parameter is now available
            if not strategy: strategy = 'late_pull'
            data = fetch_intraday_select(pro, strategy=strategy, pool=pool, limit=limit, condition=condition)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]: return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}

        elif action == "l2_quote":
            return {"status": "error", "message": "Action 'l2_quote' removed due to permission restrictions. Use 'mins' for minute data."}

        elif action == "mins":
            if not symbol: return {"status": "error", "message": "Missing 'symbol'."}
            data = fetch_mins(pro, symbol, start_date, end_date)
            if isinstance(data, list) and len(data) > 0 and "error" in data[0]: return {"status": "error", "message": data[0]['error']}
            return {"status": "success", "data": data}
        
        else:
             return {"status": "error", "message": f"Unsupported action: '{action}'"}

    except Exception as e:
        return {"status": "error", "message": f"Tushare execution error: {str(e)}"}

if __name__ == "__main__":
    try:
        input_raw = os.getenv("SKILL_INPUT", "{}")
        # For testing without env var
        if input_raw == "{}":
            # input_raw = json.dumps({"action": "kline", "symbol": "600519", "indicators": "ma"})
            pass
            
        input_data = json.loads(input_raw)

        action = input_data.get("action")
        if not action: 
            pass

        if action:
            symbol = input_data.get("symbol")
            period = input_data.get("period", "daily")
            start_date = input_data.get("start_date", "")
            end_date = input_data.get("end_date", "")
            indicators = input_data.get("indicators", "")
            pe_range = input_data.get("pe_range", "")
            pb_range = input_data.get("pb_range", "")
            mv_range = input_data.get("mv_range", "")
            market = input_data.get("market", "")
            industry = input_data.get("industry", "")
            area = input_data.get("area", "")
            pool = input_data.get("pool", "")
            condition = input_data.get("condition", "")
            token = input_data.get("token", "")
            limit = int(input_data.get("limit", 20))
            strategy = input_data.get("strategy", "late_pull")

            result = financial_data_pro(
                action=action, 
                symbol=symbol, 
                period=period, 
                start_date=start_date, 
                end_date=end_date, 
                indicators=indicators, 
                pe_range=pe_range,
                pb_range=pb_range,
                mv_range=mv_range,
                market=market,
                industry=industry,
                area=area,
                pool=pool,
                condition=condition,
                token=token,
                limit=limit,
                strategy=strategy
            )
            print(json.dumps(result, ensure_ascii=False))
    except Exception as e:
        print(json.dumps({"status": "error", "message": f"Skill wrapper error: {str(e)}"}, ensure_ascii=False))
