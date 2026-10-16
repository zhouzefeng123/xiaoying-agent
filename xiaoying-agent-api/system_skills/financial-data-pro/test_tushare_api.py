#!/usr/bin/env python3
# 简单测试 get_pro_client() 是否可用并调用常用接口
import sys
import json
import traceback

# 将模块目录加入路径，以便导入同目录下的 config.py
sys.path.insert(0, 'backend/system_skills/financial-data-pro')

try:
    import config
except Exception as e:
    print('IMPORT_ERROR', repr(e))
    sys.exit(2)

def main():
    try:
        pro = config.get_pro_client()
    except Exception as e:
        print('CLIENT_ERROR', repr(e))
        traceback.print_exc()
        sys.exit(3)

    try:
        # 优先调用 stock_basic（常见接口），若不存在则报告
        if hasattr(pro, 'stock_basic'):
            df = pro.stock_basic(exchange='', list_status='L', fields='ts_code,name,area,industry,list_date', limit=1)
            try:
                print('OK')
                print(json.dumps({'records': df.to_dict(orient='records')}, ensure_ascii=False))
            except Exception:
                print('OK (DataFrame repr)')
                print(repr(df))
        else:
            print('NO_METHOD', 'pro has no stock_basic method')
    except Exception as e:
        print('CALL_ERROR', repr(e))
        traceback.print_exc()
        sys.exit(4)

if __name__ == '__main__':
    main()
