import sys
import json
import tushare as ts

# Tushare Pro Configuration
# Users can override this by passing 'token' in the function arguments.
DEFAULT_TOKEN = '9524f403969c13294a68c470b32026cdb639539f3fdd377f9b8930ae' 
API_URL = 'http://118.89.66.41:8010/'

def get_pro_client(user_token: str = None):
    token = user_token if user_token else DEFAULT_TOKEN
    pro = ts.pro_api(token)
    
    # Configure Proxy/URL if needed (Usually for specific environment)
    pro._DataApi__http_url = API_URL
    pro._DataApi__token = token
    return pro
