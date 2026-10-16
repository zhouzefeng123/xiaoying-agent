import pandas as pd
import datetime
from typing import List, Dict, Any
from utils import normalize_symbol

def fetch_info(pro, symbol: str) -> Dict[str, Any]:
    ts_code = normalize_symbol(symbol)
    df = pro.stock_basic(ts_code=ts_code)
    if df.empty:
        return {"error": f"Stock basic info not found for {ts_code}"}
    return df.iloc[0].to_dict()

def fetch_news(pro, symbol: str = None, end_date: str = "", limit: int = 20) -> List[Dict[str, Any]]:
    try:
        date_str = end_date or datetime.datetime.now().strftime("%Y%m%d")
        ts_code = normalize_symbol(symbol) if symbol else ""
        
        # 1. Try CCTV news first (general market news)
        if not ts_code:
            try:
                df = pro.cctv_news(date=date_str)
                if not df.empty:
                    return df.head(limit).to_dict(orient='records')
            except:
                pass
        
        # 2. Try individual stock news or general news with Sina source
        start_dt = (datetime.datetime.strptime(date_str, "%Y%m%d") - datetime.timedelta(days=1)).strftime("%Y%m%d %H:%M:%S")
        end_dt = (datetime.datetime.strptime(date_str, "%Y%m%d") + datetime.timedelta(days=1)).strftime("%Y%m%d %H:%M:%S")
        
        try:
            # If symbol is provided, use it. Otherwise fetch general news but limited.
            if ts_code:
                # Tushare news interface with symbol can be slow or return empty if no news
                # Let's add a timeout or check if we can use a faster way
                df = pro.news(symbol=ts_code, start_date=start_dt, end_date=end_dt)
            else:
                df = pro.news(src='sina', start_date=start_dt, end_date=end_dt)
                
            if not df.empty:
                return df.head(limit).to_dict(orient='records')
        except:
            pass

        return [{"error": "News fetch failed: No data found for the given criteria."}]
    except Exception as e:
        return [{"error": f"News fetch failed: {str(e)}"}]
