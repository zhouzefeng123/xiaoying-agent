import json, time, requests
headers = {"Content-Type": "application/json", "Authorization": "Bearer test-key"}
data = {"model": "deepseek-v4-flash", "messages": [{"role": "user", "content": "Reply: OK"}], "stream": False, "temperature": 0}
for i in range(3):
    print(f"Request {i+1}...")
    t0 = time.time()
    r = requests.post("http://localhost:3000/v1/chat/completions", headers=headers, json=data, timeout=120)
    print(f"  Status: {r.status_code}, time: {time.time()-t0:.1f}s")
    if r.status_code == 200:
        j = r.json()
        u = j.get("usage", {})
        print(f"  prompt_tokens={u.get('prompt_tokens')}, cached={u.get('prompt_cache_hit_tokens')}, miss={u.get('prompt_cache_miss_tokens')}")
        content = j.get("choices", [{}])[0].get("message", {}).get("content", "")[:100]
        print(f"  content: {content}")
    time.sleep(2)
print("Done")