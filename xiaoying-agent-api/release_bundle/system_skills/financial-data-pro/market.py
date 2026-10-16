import datetime
import pandas as pd
import requests
import json
import re
from typing import List, Dict, Any
from utils import normalize_symbol, calculate_indicators

def fetch_index_daily(pro, symbol: str, start_date: str, end_date: str) -> List[Dict[str, Any]]:
    ts_code = normalize_symbol(symbol)
    if not start_date:
        start_date = (datetime.datetime.now() - datetime.timedelta(days=365)).strftime("%Y%m%d")
    if not end_date:
        end_date = datetime.datetime.now().strftime("%Y%m%d")
    df = pro.index_daily(ts_code=ts_code, start_date=start_date, end_date=end_date)
    if df.empty: return [{"error": f"No index data found for {ts_code}"}]
    return df.where(pd.notnull(df), None).to_dict(orient='records')

def fetch_futures_daily(pro, symbol: str, start_date: str, end_date: str) -> List[Dict[str, Any]]:
    ts_code = symbol.upper()
    if not start_date: start_date = (datetime.datetime.now() - datetime.timedelta(days=365)).strftime("%Y%m%d")
    if not end_date: end_date = datetime.datetime.now().strftime("%Y%m%d")
    df = pro.fut_daily(ts_code=ts_code, start_date=start_date, end_date=end_date)
    if df.empty: return [{"error": f"No futures data found for {ts_code}"}]
    return df.where(pd.notnull(df), None).to_dict(orient='records')

def fetch_currency_daily(pro, symbol: str, start_date: str, end_date: str) -> List[Dict[str, Any]]:
    ts_code = symbol.upper()
    if not start_date: start_date = (datetime.datetime.now() - datetime.timedelta(days=365)).strftime("%Y%m%d")
    if not end_date: end_date = datetime.datetime.now().strftime("%Y%m%d")
    try:
        df = pro.fx_daily(ts_code=ts_code, start_date=start_date, end_date=end_date)
        if df.empty: return [{"error": f"No currency data found for {ts_code}"}]
        return df.where(pd.notnull(df), None).to_dict(orient='records')
    except Exception as e:
         return [{"error": f"Currency fetch failed (check permissions): {str(e)}"}]

def fetch_kline(pro, symbol: str, start_date: str, end_date: str, indicators: str) -> List[Dict[str, Any]]:
    """Fetch K-line data. Use Sina as primary (fast), Tushare as fallback."""
    ts_code = normalize_symbol(symbol)
    
    # Try Sina first for speed
    try:
        # Map to Sina symbol
        code = ts_code.lower().replace('.', '')
        if ts_code.endswith('.SH'): code = 'sh' + ts_code[:6]
        elif ts_code.endswith('.SZ'): code = 'sz' + ts_code[:6]
        
        # Calculate needed length
        datalen = 240 # Default to about 1 year of trading days
        if start_date:
            try:
                sd = datetime.datetime.strptime(start_date, "%Y%m%d")
                days = (datetime.datetime.now() - sd).days
                datalen = int(days * 0.7) + 5 # Rough estimate of trading days
            except: pass
            
        url = f"http://money.finance.sina.com.cn/quotes_service/api/json_v2.php/CN_MarketData.getKLineData?symbol={code}&scale=240&ma=no&datalen={datalen}"
        headers = {'Referer': 'http://finance.sina.com.cn'}
        resp = requests.get(url, headers=headers, timeout=5)
        
        if resp.status_code == 200:
            data = resp.json()
            if data and isinstance(data, list):
                # Convert Sina format to Tushare-like format for compatibility
                # Sina: {"day":"2024-03-01 15:00:00","open":"1466.990","high":"...","low":"...","close":"...","volume":"..."}
                df = pd.DataFrame(data)
                df = df.rename(columns={
                    'day': 'trade_date',
                    'open': 'open',
                    'high': 'high',
                    'low': 'low',
                    'close': 'close',
                    'volume': 'vol'
                })
                # Clean date: "2024-03-01 15:00:00" -> "20240301"
                df['trade_date'] = df['trade_date'].apply(lambda x: x.split(' ')[0].replace('-', ''))
                # Ensure numeric
                for col in ['open', 'high', 'low', 'close', 'vol']:
                    df[col] = pd.to_numeric(df[col], errors='coerce')
                
                # Sort descending like Tushare usually does
                df = df.sort_values('trade_date', ascending=False).reset_index(drop=True)
                
                if indicators and indicators.lower() != 'none':
                    ind_list = [x.strip().lower() for x in indicators.split(',')]
                    df = calculate_indicators(df, ind_list)
                
                return df.where(pd.notnull(df), None).to_dict(orient='records')
    except Exception as e:
        # Fallback to Tushare if Sina fails
        pass

    # Tushare Fallback (Original Logic)
    if not start_date:
        # Reduced from 365 to 60 days to prevent proxy timeout
        start_date = (datetime.datetime.now() - datetime.timedelta(days=60)).strftime("%Y%m%d")
    if not end_date:
        end_date = datetime.datetime.now().strftime("%Y%m%d")
        
    try:
        df = pro.daily(ts_code=ts_code, start_date=start_date, end_date=end_date)
        if df.empty:
            return [{"error": f"No data found for {ts_code}"}]
            
        if indicators and indicators.lower() != 'none':
            ind_list = [x.strip().lower() for x in indicators.split(',')]
            df = calculate_indicators(df, ind_list)
        
        df = df.where(pd.notnull(df), None)
        return df.to_dict(orient='records')
    except Exception as e:
        return [{"error": f"K-line fetch failed: {str(e)}"}]

def fetch_opt_daily(pro, symbol: str, start_date: str, end_date: str) -> List[Dict[str, Any]]:
    ts_code = symbol.upper()
    if not start_date: start_date = (datetime.datetime.now() - datetime.timedelta(days=30)).strftime("%Y%m%d")
    if not end_date: end_date = datetime.datetime.now().strftime("%Y%m%d")
    df = pro.opt_daily(ts_code=ts_code, start_date=start_date, end_date=end_date)
    if df.empty: return [{"error": f"No options data found for {ts_code}"}]
    return df.where(pd.notnull(df), None).to_dict(orient='records')

def fetch_fund_daily(pro, symbol: str, start_date: str, end_date: str) -> List[Dict[str, Any]]:
    ts_code = normalize_symbol(symbol)
    if not start_date: start_date = (datetime.datetime.now() - datetime.timedelta(days=30)).strftime("%Y%m%d")
    if not end_date: end_date = datetime.datetime.now().strftime("%Y%m%d")
    df = pro.fund_daily(ts_code=ts_code, start_date=start_date, end_date=end_date)
    if df.empty: return [{"error": f"No fund data found for {ts_code}"}]
    return df.where(pd.notnull(df), None).to_dict(orient='records')

def fetch_gold_daily(pro, symbol: str, start_date: str, end_date: str) -> List[Dict[str, Any]]:
    target_symbol = symbol
    is_etf_proxy = False
    
    if symbol.lower() == "gold": 
        target_symbol = "518880.SH"
        is_etf_proxy = True
    elif symbol.lower() == "silver": 
        target_symbol = "159937.SZ"
        is_etf_proxy = True
        
    if is_etf_proxy:
        if not start_date: start_date = (datetime.datetime.now() - datetime.timedelta(days=365)).strftime("%Y%m%d")
        if not end_date: end_date = datetime.datetime.now().strftime("%Y%m%d")
        df = pro.fund_daily(ts_code=target_symbol, start_date=start_date, end_date=end_date)
        if df.empty: return [{"error": f"No proxy data found for {symbol} (using {target_symbol})"}]
        records = df.where(pd.notnull(df), None).to_dict(orient='records')
        for r in records: r['_note'] = f"Data proxied via ETF {target_symbol}"
        return records

    return fetch_futures_daily(pro, symbol, start_date, end_date)

def fetch_l2_snapshot(pro, symbol: str) -> List[Dict[str, Any]]:
    ts_code = normalize_symbol(symbol)
    trade_date = datetime.datetime.now().strftime("%Y%m%d")
    try:
        df = pro.l2_transaction(ts_code=ts_code, trade_date=trade_date, limit=100)
        if not df.empty:
             return df.to_dict(orient='records')
    except Exception as e:
        err_msg = str(e)
        if "请指定正确的接口名" in err_msg or "Permission" in err_msg or "权限" in err_msg:
            return [{"error": f"L2 Data Permission Denied. You need a Tushare token with 5000+ points. Use 'mins' as fallback. Error: {err_msg}"}]
        else:
            return [{"error": f"L2 Transaction Error: {err_msg}"}]

    return [{"error": "L2 Data fetch failed. Ensure you have 5000+ points and use action='mins' for 1-minute data if L2 Tick is unavailable."}]

def fetch_mins(pro, symbol: str, start_date: str, end_date: str, freq: str = '1min') -> List[Dict[str, Any]]:
    # Use Sina free API for minute data instead of Tushare Pro
    # Map freq: '1min' -> 1, '5min' -> 5, '15min' -> 15, '30min' -> 30, '60min' -> 60
    scale_map = {
        '1min': 1, '1m': 1,
        '5min': 5, '5m': 5,
        '15min': 15, '15m': 15,
        '30min': 30, '30m': 30,
        '60min': 60, '60m': 60
    }
    scale = scale_map.get(freq.lower(), 60)
    
    # datalen: Estimate needed points based on date range? 
    # For simplicity, fetch last 200 points which covers about 1-2 days of 5min data
    # or just use a fixed large number like 240 (4 hours of 1min)
    return fetch_sina_mins(symbol, scale=scale, datalen=240)


def fetch_realtime_quote(symbol: str, src: str = 'sina') -> List[Dict[str, Any]]:
    """Fetch realtime quote using Sina API (Free Crawler)."""
    ts_code = normalize_symbol(symbol)
    # Convert 600519.SH -> sh600519
    code = ts_code.lower().replace('.', '')
    # Handle specific suffixes if needed (Sina uses sh/sz prefix)
    if ts_code.endswith('.SH'): code = 'sh' + ts_code[:6]
    elif ts_code.endswith('.SZ'): code = 'sz' + ts_code[:6]
    elif ts_code.endswith('.BJ'): code = 'bj' + ts_code[:6] # Sina supports BJ? Check.
    
    url = f"http://hq.sinajs.cn/list={code}"
    headers = {'Referer': 'http://finance.sina.com.cn'}
    
    try:
        resp = requests.get(url, headers=headers, timeout=5)
        if resp.status_code != 200:
             return [{"error": f"Sina API Error: {resp.status_code}"}]
        
        # Parse: var hq_str_sh600519="贵州茅台,1466.990,..."
        text = resp.text
        match = re.search(r'="(.*?)";', text)
        if not match or not match.group(1):
             return [{"error": f"No data found for {ts_code} (Code might be wrong or market closed)"}]
             
        data = match.group(1).split(',')
        if len(data) < 30:
             return [{"error": f"Invalid data format for {ts_code}"}]
             
        # Map fields (Standard Sina Format)
        record = {
            "name": data[0],
            "open": float(data[1]),
            "pre_close": float(data[2]),
            "price": float(data[3]),
            "high": float(data[4]),
            "low": float(data[5]),
            "bid": float(data[6]),
            "ask": float(data[7]),
            "volume": int(data[8]),
            "amount": float(data[9]),
            "b1_v": int(data[10]), "b1_p": float(data[11]),
            "b2_v": int(data[12]), "b2_p": float(data[13]),
            "b3_v": int(data[14]), "b3_p": float(data[15]),
            "b4_v": int(data[16]), "b4_p": float(data[17]),
            "b5_v": int(data[18]), "b5_p": float(data[19]),
            "a1_v": int(data[20]), "a1_p": float(data[21]),
            "a2_v": int(data[22]), "a2_p": float(data[23]),
            "a3_v": int(data[24]), "a3_p": float(data[25]),
            "a4_v": int(data[26]), "a4_p": float(data[27]),
            "a5_v": int(data[28]), "a5_p": float(data[29]),
            "date": data[30],
            "time": data[31],
            "ts_code": ts_code
        }
        return [record]
        
    except Exception as e:
        return [{"error": f"Realtime quote failed: {str(e)}"}]

def fetch_sina_mins(symbol: str, scale: int = 60, datalen: int = 100) -> List[Dict[str, Any]]:
    """
    Fetch minute data from Sina (Free).
    symbol: e.g. 600519.SH, 000001.SZ
    scale: 5, 15, 30, 60 (minutes)
    datalen: number of points
    """
    ts_code = normalize_symbol(symbol)
    code = ts_code.lower().replace('.', '')
    # Handle suffixes
    if ts_code.endswith('.SH'): code = 'sh' + ts_code[:6]
    elif ts_code.endswith('.SZ'): code = 'sz' + ts_code[:6]
        
    url = f"https://quotes.sina.cn/cn/api/json_v2.php/CN_MarketData.getKLineData?symbol={code}&scale={scale}&ma=no&datalen={datalen}"
    
    try:
        resp = requests.get(url, timeout=5)
        if resp.status_code != 200: return []
        
        data = resp.json()
        if not data: return []
        
        # Format: [{"day":"2023-10-27 15:00:00","open":"10.1","high":"10.2","low":"10.0","close":"10.1","volume":"123456"}]
        # Convert to standardized format
        results = []
        for item in data:
            results.append({
                "ts_code": ts_code,
                "trade_time": item['day'],
                "open": float(item['open']),
                "high": float(item['high']),
                "low": float(item['low']),
                "close": float(item['close']),
                "vol": float(item['volume'])
            })
        return results
    except:
        return []
