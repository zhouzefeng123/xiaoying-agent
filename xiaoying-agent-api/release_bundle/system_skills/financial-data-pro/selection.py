import pandas as pd
import datetime
import concurrent.futures
from typing import List, Dict, Any
from utils import normalize_symbol, calculate_indicators
from monitor import fetch_full_realtime_data
from market import fetch_sina_mins

def fetch_stock_selection(pro, trade_date: str, filters: Dict[str, str], limit: int = 50) -> List[Dict[str, Any]]:
    if not trade_date:
        trade_date = (datetime.datetime.now() - datetime.timedelta(days=1)).strftime("%Y%m%d")
    
    df_daily = pd.DataFrame()
    for i in range(10):
        d = (datetime.datetime.strptime(trade_date, "%Y%m%d") - datetime.timedelta(days=i)).strftime("%Y%m%d")
        try:
            df_daily = pro.daily_basic(trade_date=d, fields='ts_code,trade_date,close,turnover_rate,pe,pb,total_mv,circ_mv')
            if not df_daily.empty:
                break
        except:
            continue
            
    if df_daily.empty:
        return [{"error": f"Could not fetch daily_basic data near {trade_date}"}]

    try:
        df_basic = pro.stock_basic(exchange='', list_status='L', fields='ts_code,symbol,name,area,industry,market')
    except Exception as e:
        return [{"error": f"Failed to fetch stock_basic: {str(e)}"}]

    df = pd.merge(df_daily, df_basic, on='ts_code')

    try:
        if 'pe_range' in filters and filters['pe_range']:
             min_v, max_v = map(float, filters['pe_range'].split(','))
             df = df[(df['pe'] >= min_v) & (df['pe'] <= max_v)]
        
        if 'pb_range' in filters and filters['pb_range']:
             min_v, max_v = map(float, filters['pb_range'].split(','))
             df = df[(df['pb'] >= min_v) & (df['pb'] <= max_v)]
        
        if 'mv_range' in filters and filters['mv_range']:
             min_v, max_v = map(float, filters['mv_range'].split(','))
             df = df[(df['total_mv'] >= min_v) & (df['total_mv'] <= max_v)]
        
        if 'market' in filters and filters['market']: 
            df = df[df['market'].str.contains(filters['market'])]
            
        if 'industry' in filters and filters['industry']:
            df = df[df['industry'].str.contains(filters['industry'])]
            
        if 'area' in filters and filters['area']:
            df = df[df['area'].str.contains(filters['area'])]

    except Exception as e:
        return [{"error": f"Filtering failed (check number format): {str(e)}"}]

    df = df.sort_values('total_mv', ascending=False)
    return df.head(limit).where(pd.notnull(df), None).to_dict(orient='records')

def fetch_tech_select(pro, pool: str, indicators: str, condition: str, limit: int = 50) -> List[Dict[str, Any]]:
    stock_list = []
    if pool == 'all':
         df_basic = pro.stock_basic(exchange='', list_status='L', fields='ts_code,total_mv')
         df_basic = df_basic.sort_values('total_mv', ascending=False).head(300)
         stock_list = df_basic['ts_code'].tolist()
    elif ',' in pool:
         stock_list = [x.strip() for x in pool.split(',')]
    elif pool.isdigit():
         try:
             df_const = pro.index_weight(index_code=normalize_symbol(pool), start_date=(datetime.datetime.now()-datetime.timedelta(days=60)).strftime("%Y%m%d"))
             if not df_const.empty:
                 stock_list = df_const['con_code'].unique().tolist()
         except:
             pass
         if not stock_list:
             stock_list = [normalize_symbol(pool)]
    else:
         stock_list = [normalize_symbol(pool)]
         
    if not stock_list: return [{"error": "Empty stock pool"}]
    
    results = []
    end_date = datetime.datetime.now().strftime("%Y%m%d")
    start_date = (datetime.datetime.now() - datetime.timedelta(days=100)).strftime("%Y%m%d")
    
    batch_size = 50
    for i in range(0, len(stock_list), batch_size):
        batch = stock_list[i:i+batch_size]
        try:
            df_daily = pro.daily(ts_code=','.join(batch), start_date=start_date, end_date=end_date)
            if df_daily.empty: continue
            
            for code, df_stock in df_daily.groupby('ts_code'):
                df_stock = df_stock.sort_values('trade_date').reset_index(drop=True)
                if len(df_stock) < 30: continue
                
                df_ind = calculate_indicators(df_stock, indicators.split(','))
                latest = df_ind.iloc[-1]
                
                try:
                    ctx = latest.to_dict()
                    safe_ctx = {k: v for k, v in ctx.items() if isinstance(v, (int, float, str)) and k in condition}
                    if eval(condition, {}, ctx):
                        results.append(latest.to_dict())
                except Exception as e:
                    pass
        except Exception as e:
            continue
            
        if len(results) >= limit: break
            
    return results[:limit]

def fetch_realtime_select(pro, condition: str, limit: int = 20) -> List[Dict[str, Any]]:
    if not condition: return [{"error": "Missing 'condition' expression"}]
    
    # Fetch all realtime data (Brute-force scan 6000 stocks)
    try:
        all_data = fetch_full_realtime_data(pro, pages=30) 
    except Exception as e:
        return [{"error": f"Failed to fetch realtime data: {str(e)}"}]
    
    results = []
    for item in all_data:
        try:
            if item.get('price', 0) == 0: continue
            
            # Use eval to check condition
            # Available variables: price, pct_chg, turnover_rate, pe, pb, volume, amount, total_mv, circ_mv
            # Create a clean context with just the item fields
            ctx = {k: v for k, v in item.items() if isinstance(v, (int, float, str))}
            
            if eval(condition, {}, ctx):
                results.append(item)
        except Exception:
            continue
            
    # Sort by pct_chg descending by default
    results.sort(key=lambda x: x.get('pct_chg', 0), reverse=True)
    
    return results[:limit]

def fetch_intraday_select(pro, strategy: str = 'late_pull', pool: str = 'gainers', limit: int = 20, condition: str = "") -> List[Dict[str, Any]]:
    """
    Intraday strategy selection using minute data.
    Back to Tushare: Uses Tushare daily data for candidate selection (reliable & no limits).
    """
    candidates = []
    trade_date = ""
    
    # 1. Find latest trading date and candidates using Tushare (Fast & Bulk)
    try:
        # Try to get today's or latest daily data
        for i in range(5):
            d = (datetime.datetime.now() - datetime.timedelta(days=i)).strftime("%Y%m%d")
            df_daily = pro.daily(trade_date=d, fields='ts_code,pct_chg,amount,close')
            if not df_daily.empty:
                trade_date = d
                break
        
        if df_daily.empty:
            return [{"error": "Could not fetch latest daily data from Tushare"}]

        if pool == 'gainers':
            # Sort by pct_chg descending, take top 150 for deep analysis
            candidates = df_daily.sort_values('pct_chg', ascending=False).head(150)['ts_code'].tolist()
            
        elif pool == 'turnover':
             # Need daily_basic for turnover_rate
             df_basic = pro.daily_basic(trade_date=trade_date, fields='ts_code,turnover_rate')
             df_merged = pd.merge(df_daily, df_basic, on='ts_code')
             candidates = df_merged.sort_values('turnover_rate', ascending=False).head(150)['ts_code'].tolist()
             
        else:
             # Default to top MV
             df_basic = pro.stock_basic(exchange='', list_status='L', fields='ts_code,total_mv')
             df_basic = df_basic.sort_values('total_mv', ascending=False).head(150)
             candidates = df_basic['ts_code'].tolist()
             
    except Exception as e:
        return [{"error": f"Tushare pool fetch failed: {str(e)}"}]
        
    if not candidates:
        return [{"error": "No candidates found for selection"}]

    # 2. Analyze Intraday (Concurrent) - Expanded to 100 for comprehensive coverage
    candidates = candidates[:100] 
    results = []
    
    def analyze_stock(ts_code):
        # Fetch 60min data (enough to cover recent days)
        # Using Sina API (Free & Fast)
        mins = fetch_sina_mins(ts_code, scale=60, datalen=24) # 24 hours = ~6 days
        if not mins: return None
        
        # Convert trade_time to datetime
        # Sina format: "2023-10-27 15:00:00"
        # We need to find the data for 'trade_date' (YYYYMMDD)
        # trade_date is from step 1.
        
        target_date_str = f"{trade_date[:4]}-{trade_date[4:6]}-{trade_date[6:]}"
        
        # Find 15:00 bar for this date
        # The bar time is the close time of the bar.
        # 14:00-15:00 bar is labeled "15:00:00"
        
        day_mins = [m for m in mins if m['trade_time'].startswith(target_date_str)]
        if not day_mins: return None
        
        bar_1500 = next((m for m in day_mins if m['trade_time'].endswith("15:00:00")), None)
        if not bar_1500: return None
        
        # Calculate indicators
        # 1. Late Pull: (Close_1500 - Open_1500) / Open_1500
        # Note: Open_1500 is the price at 14:00.
        
        pct_chg_last_hour = (bar_1500['close'] - bar_1500['open']) / bar_1500['open'] * 100
        
        # 2. Volume Ratio: Vol_1500 / Total_Day_Vol
        total_vol = sum(m['vol'] for m in day_mins)
        vol_ratio = bar_1500['vol'] / total_vol if total_vol > 0 else 0
        
        # Strategy Logic
        if strategy == 'late_pull':
            # Criteria: 
            # 1. Last hour gain > 1.5% (significant pull)
            # 2. Volume ratio > 0.20 (active trading, normal is 0.25 for 4 hours, but last hour is usually active)
            
            # Prepare context for evaluation
            ctx = {
                "last_hour_chg": round(pct_chg_last_hour, 2),
                "last_hour_vol_ratio": round(vol_ratio, 3) # Use 0.20 format
            }
            
            is_match = False
            if condition:
                try:
                    if eval(condition, {}, ctx):
                        is_match = True
                except:
                    pass
            else:
                # Default thresholds
                if pct_chg_last_hour > 1.5 and vol_ratio > 0.20:
                    is_match = True
            
            if is_match:
                return {
                    "ts_code": ts_code,
                    "trade_date": trade_date,
                    "close": bar_1500['close'],
                    "last_hour_chg": ctx["last_hour_chg"],
                    "last_hour_vol_ratio": round(vol_ratio * 100, 2), # Return percentage for display
                    "note": "Late Pull Detected"
                }
        
        return None

    with concurrent.futures.ThreadPoolExecutor(max_workers=20) as executor:
        futures = [executor.submit(analyze_stock, code) for code in candidates]
        for future in concurrent.futures.as_completed(futures):
            try:
                res = future.result()
                if res: results.append(res)
            except:
                pass
                
    # Sort results
    if strategy == 'late_pull':
        results.sort(key=lambda x: x['last_hour_chg'], reverse=True)
        
    return results[:limit]
