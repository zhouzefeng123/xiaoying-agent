import requests
import json
import sys
import time

BASE_URL = "http://localhost:3000"

def test_browser_use():
    print("Testing Browser-Use Skill...")
    url = f"{BASE_URL}/v1/chat/completions"
    headers = {"Content-Type": "application/json"}
    
    # A simple browser task
    prompt = "请使用 browser-use 技能，访问 'https://www.bing.com' 并搜索 'OpenAI'，然后总结第一条结果。"
    
    data = {
        "model": "aura-coder",
        "messages": [{"role": "user", "content": prompt}],
        "stream": True,
        "temperature": 0
    }

    try:
        response = requests.post(url, headers=headers, data=json.dumps(data), stream=True)
        response.raise_for_status()
        
        full_content = ""
        tool_called = False
        
        for line in response.iter_lines():
            if line:
                decoded_line = line.decode('utf-8')
                if decoded_line.startswith("data: "):
                    json_str = decoded_line[6:]
                    if json_str == "[DONE]":
                        break
                    try:
                        chunk = json.loads(json_str)
                        delta = chunk['choices'][0]['delta']
                        
                        if 'content' in delta:
                            content = delta['content']
                            full_content += content
                            print(content, end="", flush=True)
                        
                        if 'tool_calls' in delta:
                            tool_called = True
                            print("\n[Tool Call Detected]")
                            
                    except:
                        pass
                        
        print("\n\n--------------------------------")
        if tool_called or "browser-use" in full_content:
            print("✅ [SUCCESS] Browser-use skill interaction detected!")
        else:
            print("⚠️ [WARNING] Browser-use skill might not have been triggered.")
            
    except Exception as e:
        print(f"Error: {e}")

if __name__ == "__main__":
    test_browser_use()
