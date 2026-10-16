import requests
import json
import time
import os
import sys

BASE_URL = "http://localhost:3000"


def extract_cached_tokens(usage):
    details = usage.get("prompt_tokens_details") or usage.get("input_tokens_details") or {}
    cached_tokens = details.get("cached_tokens")
    if cached_tokens is None:
        cached_tokens = usage.get("prompt_cache_hit_tokens", 0) or usage.get("cached_tokens", 0)
    return int(cached_tokens or 0)


def extract_effective_prompt_tokens(usage, cached_tokens):
    prompt_tokens = int(usage.get("prompt_tokens", 0) or 0)
    if "prompt_cache_hit_tokens" in usage or "prompt_cache_miss_tokens" in usage:
        return prompt_tokens + cached_tokens
    return prompt_tokens

def run_chat(session_id, messages):
    url = f"{BASE_URL}/v1/chat/completions"
    headers = {"Content-Type": "application/json"}
    
    data = {
        "model": "gpt-4o", # Ensure we use a model that supports caching/high-throughput
        "messages": messages,
        "stream": False, 
        "session_id": session_id,
        "temperature": 0.0 # Deterministic for better cache chance
    }
    
    try:
        print(f"\n[Request] Sending {len(messages)} messages to session {session_id}...")
        start = time.time()
        response = requests.post(url, headers=headers, data=json.dumps(data))
        duration = time.time() - start
        
        if response.status_code != 200:
            print(f"Error: {response.status_code} - {response.text}")
            return None

        result = response.json()
        usage = result.get("usage", {})
        cached_tokens = extract_cached_tokens(usage)
        effective_prompt_tokens = extract_effective_prompt_tokens(usage, cached_tokens)
        
        print(f"[Response] Duration: {duration:.2f}s")
        print(f"  Input Tokens: {usage.get('prompt_tokens', 0)}")
        if effective_prompt_tokens != int(usage.get('prompt_tokens', 0) or 0):
            print(f"  Effective Prompt Tokens: {effective_prompt_tokens}")
        print(f"  Output Tokens: {usage.get('completion_tokens', 0)}")
        print(f"  Cached Tokens: {cached_tokens}")
        print(f"  Total Tokens: {usage.get('total_tokens', 0)}")
        
        if effective_prompt_tokens > 0:
            hit_rate = (cached_tokens / effective_prompt_tokens) * 100
            print(f"  Cache Hit Rate: {hit_rate:.2f}%")
            
        return result
        
    except Exception as e:
        print(f"Exception: {e}")
        return None

def main():
    session_id = "cache-test-session-" + str(int(time.time()))
    history = []
    
    # 1. Turn 1: Establish Context
    # We need enough tokens to trigger caching (>1024 usually).
    # We'll inject a large "context" document.
    
    large_context = "This is a large context block to ensure we exceed minimum cache thresholds. " * 200 # ~2000 tokens
    
    print("\n=== Turn 1: Initialization (Cold Cache) ===")
    history.append({"role": "user", "content": f"Here is some background information:\n{large_context}\n\nPlease acknowledge."})
    
    resp1 = run_chat(session_id, history)
    if resp1:
        history.append(resp1["choices"][0]["message"])
        print(f"Assistant: {resp1['choices'][0]['message']['content'][:50]}...")

    # 2. Turn 2: Follow up (Should hit cache)
    # The System Prompt + Turn 1 History should be cached.
    print("\n=== Turn 2: Follow Up (Warm Cache Check) ===")
    history.append({"role": "user", "content": "What was the first sentence of the background info?"})
    
    resp2 = run_chat(session_id, history)
    if resp2:
        history.append(resp2["choices"][0]["message"])
        print(f"Assistant: {resp2['choices'][0]['message']['content'][:50]}...")

    # 3. Turn 3: Another Follow up
    print("\n=== Turn 3: Stability Check ===")
    history.append({"role": "user", "content": "Tell me a joke about caching."})
    
    resp3 = run_chat(session_id, history)
    if resp3:
        history.append(resp3["choices"][0]["message"])
        print(f"Assistant: {resp3['choices'][0]['message']['content'][:50]}...")

if __name__ == "__main__":
    main()
