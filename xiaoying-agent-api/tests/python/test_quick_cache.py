import time, uuid, requests

BASE_URL = "http://localhost:3000/v1/chat/completions"
MODEL = "deepseek-v4-flash"
READ_TIMEOUT_SECONDS = 300
WARM_DELAY_SECONDS = 3
PREFIX_REPEAT = 4

headers = {"Content-Type": "application/json", "Authorization": "Bearer test-key"}
marker = f"KV_PREFIX_{uuid.uuid4().hex[:8].upper()}"
prefix = (f"{marker}: This is a DeepSeek KV-cache benchmark system prefix. Keep answers short. ") * PREFIX_REPEAT


def do_chat(label, msg):
    data = {
        "model": MODEL,
        "messages": [{"role": "system", "content": prefix}, {"role": "user", "content": msg}],
        "stream": False,
        "temperature": 0,
        "max_tokens": 16,
    }
    print(f"[{label}] Sending non-streaming...")
    t0 = time.time()
    try:
        r = requests.post(BASE_URL, headers=headers, json=data, timeout=(10, READ_TIMEOUT_SECONDS))
        elapsed = time.time() - t0
        print(f"[{label}] Status: {r.status_code}, time: {elapsed:.1f}s")
        if r.status_code == 200:
            j = r.json()
            u = j.get("usage", {})
            pt = u.get("prompt_tokens", 0)
            ct = u.get("prompt_cache_hit_tokens", 0) or 0
            cm = u.get("prompt_cache_miss_tokens")
            if cm is None:
                cm = max(pt - ct, 0)
            hit = ct / pt if pt else 0
            content = j.get("choices", [{}])[0].get("message", {}).get("content", "")[:200]
            print(f"[{label}] prompt={pt}, cache_hit={ct}, cache_miss={cm}, hit_rate={hit:.1%}")
            print(f"[{label}] usage={u}")
            print(f"[{label}] content: {content}")
            return pt, ct, cm, hit
        body = r.text[:500].replace("\n", " ")
        print(f"[{label}] non-200 body: {body}")
    except Exception as e:
        print(f"[{label}] Error: {type(e).__name__}: {e}")
    return 0, 0, 0, 0

print(f"Marker: {marker}")
print("=== COLD ===")
do_chat("COLD", "Reply with exactly: OK")
time.sleep(WARM_DELAY_SECONDS)
print("=== WARM ===")
do_chat("WARM", "Reply with exactly: OK")
print("Done!")