import requests
import json
import sys

BASE_URL = "http://localhost:3000"

def test_github_search():
    print("Testing GitHub Search via Web Search...")
    url = f"{BASE_URL}/v1/chat/completions"
    headers = {"Content-Type": "application/json"}
    
    # A specific GitHub query
    prompt = "请在 GitHub 上搜索 'mcp-server-go' 相关的仓库。直接使用 web_search 工具，查询词使用 'site:github.com mcp-server-go'。"
    
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
        tool_output_seen = False
        
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
                            
                            if "[Tool Output" in content:
                                tool_output_seen = True
                    except:
                        pass
                        
        print("\n\n--------------------------------")
        if tool_output_seen:
            print("✅ [SUCCESS] Search tool was called!")
        else:
            print("⚠️ [WARNING] Search tool might not have been called.")
            
    except Exception as e:
        print(f"Error: {e}")

if __name__ == "__main__":
    test_github_search()
