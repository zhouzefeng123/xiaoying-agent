import json, time, uuid, requests

headers = {"Content-Type": "application/json", "Authorization": "Bearer test-key"}
marker = f"KV_PREFIX_{uuid.uuid4().hex[:8].upper()}"
prefix = (f"{marker}: This is a DeepSeek KV-cache benchmark system prefix. Keep answers short. ") * 20

print(f"=== Testing with large prefix (len={len(prefix)}, multiplier=20) ===")
for i in range(2):
    data = {
        "model": "deepseek-v4-flash",
        "messages": [{"role": "system", "content": prefix}, {"role": "user", "content": "Reply: OK"}],
        "stream": False, "temperature": 0,
    }
    label = f"REQ{i+1}"
    print(f"[{label}] Sending...")
    t0 = time.time()
    try:
        r = requests.post("http://localhost:3000/v1/chat/completions", headers=headers, json=data, timeout=180)
        elapsed = time.time() - t0
        print(f"[{label}] Status: {r.status_code}, time: {elapsed:.1f}s")
        if r.status_code == 200:
            j = r.json()
            u = j.get("usage", {})
            pt = u.get("prompt_tokens", 0)
            ct = u.get("prompt_cache_hit_tokens", 0) or 0
            cm = u.get("prompt_cache_miss_tokens", 0) or 0
            print(f"[{label}] prompt={pt}, cached={ct}, miss={cm}, hit={ct/pt*100 if pt else 0:.1f}%")
    except Exception as e:
        print(f"[{label}] Error: {e}")
    time.sleep(2)
print("Done")