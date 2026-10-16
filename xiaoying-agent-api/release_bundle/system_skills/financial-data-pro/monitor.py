import pandas as pd
import datetime
import requests
import json
import concurrent.futures
from typing import List, Dict, Any
from utils import normalize_symbol

def fetch_daily_limit(pro, symbol: str, trade_date: str) -> List[Dict[str, Any]]:
    ts_code = normalize_symbol(symbol) if symbol else ""
    if not trade_date: trade_date = (datetime.datetime.now() - datetime.timedelta(days=1)).strftime("%Y%m%d")
    
    for i in range(10):
        d = (datetime.datetime.strptime(trade_date, "%Y%m%d") - datetime.timedelta(days=i)).strftime("%Y%m%d")
        try:
            df = pro.limit_list(trade_date=d, ts_code=ts_code)
            if not df.empty:
                if 'trade_date' not in df.columns: df['trade_date'] = d
                break
        except:
            continue
            
    if df.empty: return [{"error": f"No limit list data found for {trade_date} (checked 10 days back)"}]
    return df.where(pd.notnull(df), None).to_dict(orient='records')

def fetch_moneyflow(pro, symbol: str, start_date: str, end_date: str) -> List[Dict[str, Any]]:
    ts_code = normalize_symbol(symbol)
    if not start_date: start_date = (datetime.datetime.now() - datetime.timedelta(days=30)).strftime("%Y%m%d")
    if not end_date: end_date = datetime.datetime.now().strftime("%Y%m%d")
    df = pro.moneyflow(ts_code=ts_code, start_date=start_date, end_date=end_date)
    if df.empty: return [{"error": f"No moneyflow data found for {ts_code}"}]
    return df.where(pd.notnull(df), None).to_dict(orient='records')

def fetch_futures_holding(pro, symbol: str, start_date: str, end_date: str) -> List[Dict[str, Any]]:
    if not start_date: start_date = (datetime.datetime.now() - datetime.timedelta(days=7)).strftime("%Y%m%d")
    if not end_date: end_date = datetime.datetime.now().strftime("%Y%m%d")
    df = pro.fut_holding(symbol=symbol, start_date=start_date, end_date=end_date)
    if df.empty: return [{"error": f"No futures holding data found for {symbol}"}]
    return df.where(pd.notnull(df), None).to_dict(orient='records')

def fetch_trade_cal(pro, start_date: str, end_date: str) -> List[Dict[str, Any]]:
    if not start_date: start_date = datetime.datetime.now().strftime("%Y%m%d")
    if not end_date: end_date = (datetime.datetime.now() + datetime.timedelta(days=30)).strftime("%Y%m%d")
    
    df = pro.trade_cal(exchange='', start_date=start_date, end_date=end_date)
    return df.to_dict(orient='records')


def fetch_realtime_page(page: int, num: int = 80) -> List[Dict[str, Any]]:
    """Helper to fetch a single page of realtime data."""
    url = f"http://vip.stock.finance.sina.com.cn/quotes_service/api/json_v2.php/Market_Center.getHQNodeData?page={page}&num={num}&sort=changepercent&asc=0&node=hs_a&symbol=&_s_r_a=init"
    headers = {'Referer': 'http://finance.sina.com.cn'}
    try:
        resp = requests.get(url, headers=headers, timeout=5)
        if resp.status_code == 200:
            data = resp.json()
            if not data: return []
            
            result = []
            for item in data:
                try:
                    result.append({
                        "ts_code": item.get('code'), 
                        "name": item.get('name'),
                        "price": float(item.get('trade', 0)),
                        "pct_chg": float(item.get('changepercent', 0)),
                        "change": float(item.get('pricechange', 0)),
                        "volume": int(item.get('volume', 0)),
                        "amount": float(item.get('amount', 0)),
                        "high": float(item.get('high', 0)),
                        "low": float(item.get('low', 0)),
                        "open": float(item.get('open', 0)),
                        "close": float(item.get('trade', 0)),
                        "turnover_rate": float(item.get('turnoverratio', 0)),
                        "pe": float(item.get('per', 0)),
                        "pb": float(item.get('pb', 0)),
                        "total_mv": float(item.get('mktcap', 0)) * 10000, 
                        "circ_mv": float(item.get('nmc', 0)) * 10000
                    })
                except:
                    continue
            return result
    except:
        return []
    return []

def fetch_full_realtime_data(pro, pages: int = 30) -> List[Dict[str, Any]]:
    """
    Fetch all A-share realtime data using concurrent threads.
    Redundancy: Try Sina first, fallback to Tushare if Sina fails or is blocked.
    """
    all_data = []
    # Try Sina Brute-force first
    try:
        with concurrent.futures.ThreadPoolExecutor(max_workers=20) as executor:
            futures = [executor.submit(fetch_realtime_page, p, 200) for p in range(1, pages + 1)]
            for future in concurrent.futures.as_completed(futures):
                data = future.result()
                if data:
                    all_data.extend(data)
        
        if len(all_data) > 2000: # Heuristic: if we got at least half of the market, consider it a success
            return all_data
    except Exception as e:
        print(f"Sina brute-force failed: {e}. Falling back to Tushare.")
    
    # Fallback to Tushare (Reliable backup)
    try:
        # Tushare pro.daily(trade_date=...) gets all stocks for a date in ONE call
        for i in range(5):
            d = (datetime.datetime.now() - datetime.timedelta(days=i)).strftime("%Y%m%d")
            df = pro.daily(trade_date=d)
            if not df.empty:
                # Need daily_basic for extra fields like turnover, pe, pb, mv
                df_basic = pro.daily_basic(trade_date=d)
                if not df_basic.empty:
                    df = pd.merge(df, df_basic, on='ts_code', suffixes=('', '_b'))
                
                # Normalize to match Sina format
                result = []
                for _, row in df.iterrows():
                    result.append({
                        "ts_code": row.get('ts_code'),
                        "name": "", # Tushare daily doesn't have names, but it's acceptable for filtering
                        "price": float(row.get('close', 0)),
                        "pct_chg": float(row.get('pct_chg', 0)),
                        "change": float(row.get('change', 0)),
                        "volume": int(row.get('vol', 0)),
                        "amount": float(row.get('amount', 0)) * 1000, # Tushare amount is in thousands
                        "high": float(row.get('high', 0)),
                        "low": float(row.get('low', 0)),
                        "open": float(row.get('open', 0)),
                        "close": float(row.get('close', 0)),
                        "turnover_rate": float(row.get('turnover_rate', 0)),
                        "pe": float(row.get('pe', 0)),
                        "pb": float(row.get('pb', 0)),
                        "total_mv": float(row.get('total_mv', 0)) * 10000,
                        "circ_mv": float(row.get('circ_mv', 0)) * 10000
                    })
                return result
    except Exception as e:
        print(f"Tushare fallback failed: {e}")
        
    return all_data

def fetch_realtime_list(src: str = 'sina') -> List[Dict[str, Any]]:
    """Fetch realtime rank list using Sina JSON API (Free Crawler)."""
    # For backward compatibility, just fetch page 1
    return fetch_realtime_page(1, 80)

