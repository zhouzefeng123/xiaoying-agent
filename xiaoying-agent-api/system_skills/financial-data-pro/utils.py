import pandas as pd
from typing import List

# Helper: Normalize symbol for Tushare (e.g. 600519 -> 600519.SH)
def normalize_symbol(symbol: str) -> str:
    if not symbol: return ""
    s = symbol.strip().upper()
    if "." in s:
        return s
    
    # Guess suffix
    if s.startswith('6'):
        return s + ".SH"
    elif s.startswith('0') or s.startswith('3'):
        return s + ".SZ"
    elif s.startswith('8') or s.startswith('4'):
        return s + ".BJ"
    
    return s

def calculate_indicators(df: pd.DataFrame, indicators: List[str]) -> pd.DataFrame:
    """Calculate technical indicators using pandas."""
    try:
        # Tushare returns date descending usually, sort ascending for calculation
        df = df.sort_values('trade_date').reset_index(drop=True)
        
        close = df['close']
        high = df['high']
        low = df['low']

        # MA
        if 'ma' in indicators or 'all' in indicators:
            for n in [5, 10, 20, 30, 60]:
                df[f'ma{n}'] = close.rolling(window=n).mean().round(2)

        # MACD (12, 26, 9)
        if 'macd' in indicators or 'all' in indicators:
            ema12 = close.ewm(span=12, adjust=False).mean()
            ema26 = close.ewm(span=26, adjust=False).mean()
            df['diff'] = ema12 - ema26
            df['dea'] = df['diff'].ewm(span=9, adjust=False).mean()
            df['macd'] = (2 * (df['diff'] - df['dea']))
            # Rounding
            df['diff'] = df['diff'].round(3)
            df['dea'] = df['dea'].round(3)
            df['macd'] = df['macd'].round(3)

        # BOLL (20, 2)
        if 'boll' in indicators or 'all' in indicators:
            df['boll_mid'] = close.rolling(window=20).mean()
            df['boll_std'] = close.rolling(window=20).std()
            df['boll_up'] = df['boll_mid'] + 2 * df['boll_std']
            df['boll_low'] = df['boll_mid'] - 2 * df['boll_std']
            # Rounding
            df['boll_mid'] = df['boll_mid'].round(2)
            df['boll_up'] = df['boll_up'].round(2)
            df['boll_low'] = df['boll_low'].round(2)
            if 'boll_std' in df.columns: df.drop(columns=['boll_std'], inplace=True)

        # RSI (6, 12, 24)
        if 'rsi' in indicators or 'all' in indicators:
            delta = close.diff()
            gain = (delta.where(delta > 0, 0))
            loss = (-delta.where(delta < 0, 0))
            
            for n in [6, 12, 24]:
                avg_gain = gain.ewm(com=n-1, min_periods=n).mean()
                avg_loss = loss.ewm(com=n-1, min_periods=n).mean()
                rs = avg_gain / avg_loss
                df[f'rsi{n}'] = (100 - (100 / (1 + rs))).round(2)

        # KDJ (9, 3, 3)
        if 'kdj' in indicators or 'all' in indicators:
            low_min = low.rolling(window=9).min()
            high_max = high.rolling(window=9).max()
            rsv = (close - low_min) / (high_max - low_min) * 100
            
            k_list = []
            d_list = []
            k = 50
            d = 50
            for val in rsv:
                if pd.isna(val):
                    k_list.append(None)
                    d_list.append(None)
                    continue
                k = 2/3 * k + 1/3 * val
                d = 2/3 * d + 1/3 * k
                k_list.append(k)
                d_list.append(d)
            
            df['k'] = pd.Series(k_list, dtype='float64')
            df['d'] = pd.Series(d_list, dtype='float64')
            df['j'] = 3 * df['k'] - 2 * df['d']
            
            df['k'] = df['k'].round(2)
            df['d'] = df['d'].round(2)
            df['j'] = df['j'].round(2)

        return df.where(pd.notnull(df), None)
    except Exception as e:
        return df
