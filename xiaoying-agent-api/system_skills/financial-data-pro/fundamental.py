import pandas as pd
from typing import List, Dict, Any
from utils import normalize_symbol

def fetch_income(pro, symbol: str, start_date: str, end_date: str, period: str) -> List[Dict[str, Any]]:
    ts_code = normalize_symbol(symbol)
    df = pro.income(ts_code=ts_code, start_date=start_date, end_date=end_date, period=period)
    if df.empty: return [{"error": f"No income data found for {ts_code}"}]
    return df.where(pd.notnull(df), None).to_dict(orient='records')

def fetch_balance(pro, symbol: str, start_date: str, end_date: str, period: str) -> List[Dict[str, Any]]:
    ts_code = normalize_symbol(symbol)
    df = pro.balancesheet(ts_code=ts_code, start_date=start_date, end_date=end_date, period=period)
    if df.empty: return [{"error": f"No balance sheet data found for {ts_code}"}]
    return df.where(pd.notnull(df), None).to_dict(orient='records')

def fetch_cashflow(pro, symbol: str, start_date: str, end_date: str, period: str) -> List[Dict[str, Any]]:
    ts_code = normalize_symbol(symbol)
    df = pro.cashflow(ts_code=ts_code, start_date=start_date, end_date=end_date, period=period)
    if df.empty: return [{"error": f"No cashflow data found for {ts_code}"}]
    return df.where(pd.notnull(df), None).to_dict(orient='records')

def fetch_daily_basic(pro, symbol: str, trade_date: str) -> List[Dict[str, Any]]:
    import datetime
    ts_code = normalize_symbol(symbol)
    if not trade_date: trade_date = (datetime.datetime.now() - datetime.timedelta(days=1)).strftime("%Y%m%d")
    
    for i in range(5):
        d = (datetime.datetime.strptime(trade_date, "%Y%m%d") - datetime.timedelta(days=i)).strftime("%Y%m%d")
        df = pro.daily_basic(ts_code=ts_code, trade_date=d)
        if not df.empty:
            break
    
    if df.empty: return [{"error": f"No daily basic data found for {ts_code}"}]
    return df.where(pd.notnull(df), None).to_dict(orient='records')

def fetch_adj_factor(pro, symbol: str, start_date: str, end_date: str) -> List[Dict[str, Any]]:
    import datetime
    ts_code = normalize_symbol(symbol)
    if not start_date: start_date = "20000101"
    if not end_date: end_date = datetime.datetime.now().strftime("%Y%m%d")
    df = pro.adj_factor(ts_code=ts_code, start_date=start_date, end_date=end_date)
    if df.empty: return [{"error": f"No adjustment factor data found for {ts_code}"}]
    return df.where(pd.notnull(df), None).to_dict(orient='records')

def fetch_stk_managers(pro, symbol: str) -> List[Dict[str, Any]]:
    ts_code = normalize_symbol(symbol)
    df = pro.stk_managers(ts_code=ts_code)
    if df.empty: return [{"error": f"No manager data found for {ts_code}"}]
    return df.where(pd.notnull(df), None).to_dict(orient='records')
