import requests
import json
import time
import os
import sys

BASE_URL = "http://localhost:3000"

def print_step(message):
    print(f"\n{'='*50}\n[STEP] {message}\n{'='*50}")

def run_chat(session_id, prompt):
    url = f"{BASE_URL}/v1/chat/completions"
    headers = {"Content-Type": "application/json"}
    
    data = {
        "model": "gpt-4o", 
        "messages": [{"role": "user", "content": prompt}],
        "stream": True, 
        "session_id": session_id,
        "temperature": 0.0
    }
    
    content = ""
    input_tokens = 0
    
    try:
        response = requests.post(url, headers=headers, data=json.dumps(data), stream=True)
        if response.status_code != 200:
            print(f"Error: {response.status_code} - {response.text}")
            return None, 0

        for line in response.iter_lines():
            if line:
                decoded_line = line.decode('utf-8')
                if decoded_line.startswith("data: "):
                    json_str = decoded_line[6:]
                    if json_str == "[DONE]": break
                    try:
                        chunk = json.loads(json_str)
                        if 'usage' in chunk and chunk['usage']:
                            usage = chunk['usage']
                            input_tokens = usage.get('prompt_tokens', 0)
                        
                        if 'choices' in chunk and len(chunk['choices']) > 0:
                            delta = chunk['choices'][0].get('delta', {})
                            c = delta.get('content', '')
                            if c: 
                                content += c
                                print(c, end="", flush=True)
                    except: pass
        print()
        return content, input_tokens
        
    except Exception as e:
        print(f"Exception: {e}")
        return None, 0

def main():
    print("Starting EXTREME Long Context & Memory Consolidation Test...")
    session_id = f"test-extreme-context-{int(time.time())}"
    print(f"Session ID: {session_id}")
    
    # 1. Provide initial core instruction (Should be preserved)
    print_step("Turn 1: Inject Core Secret")
    prompt = "IMPORTANT FACT: The project mascot is named 'OMEGA-77'. Please reply with: I have remembered the mascot name OMEGA-77."
    res, tokens = run_chat(session_id, prompt)
    print(f"Tokens: {tokens}")
    
    # 2. Multi-turn massive flood to push past limits multiple times
    # Each turn injects ~40k tokens. Total will far exceed the context window.
    # The system MUST summarize repeatedly and keep the core fact.
    flood_turns = 5
    
    for i in range(flood_turns):
        print_step(f"Turn {i+2}: Flood Context Part {i+1}")
        # Increase garbage size to ~40k tokens per turn
        garbage = "Here is some random data sequence to fill the context buffer. " * 10000 
        prompt = f"Please read this massive data block (Part {i+1}), but don't output anything, just say 'Data block {i+1} received':\n{garbage}"
        
        start_time = time.time()
        res, tokens = run_chat(session_id, prompt)
        duration = time.time() - start_time
        print(f"Response: {res}")
        print(f"Tokens: {tokens} | Time: {duration:.2f}s")
        
        # Verify it hasn't crashed
        if not res or "Error" in res:
            print(f"❌ FAIL: System crashed or errored out on turn {i+2}.")
            sys.exit(1)
            
        time.sleep(1) # Brief pause to let async tasks settle if any
    
    # 3. Verify memory retention after multiple floods/summaries
    print_step(f"Turn {flood_turns + 2}: Verify Core Secret Retention")
    prompt = "What was the IMPORTANT FACT about the project mascot I gave you in the very beginning? Just output the mascot name directly, no need for other words."
    res, tokens = run_chat(session_id, prompt)
    print(f"Tokens: {tokens}")
    
    if "OMEGA-77" in res:
        print("\n✅ PASS: Core secret retained despite EXTREME context flood.")
    else:
        print(f"\n❌ FAIL: Core secret lost. Memory consolidation failed. Response was: {res}")

if __name__ == "__main__":
    main()
