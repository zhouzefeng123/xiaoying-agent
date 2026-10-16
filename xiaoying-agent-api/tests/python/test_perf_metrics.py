import requests
import json
import time
import os
import sys

# Configuration
BASE_URL = "http://localhost:3000"
MODEL = "gpt-4o"

def print_separator(title):
    print(f"\n{'='*50}\n[SCENARIO] {title}\n{'='*50}")

def run_test_scenario(name, prompt, session_id=None, temperature=0.0):
    """
    Runs a chat completion request and measures performance metrics.
    """
    print_separator(name)
    
    url = f"{BASE_URL}/v1/chat/completions"
    headers = {"Content-Type": "application/json"}
    
    # Use a unique session ID if not provided (to simulate cold start or new session)
    if not session_id:
        session_id = f"perf-test-{int(time.time())}"
    
    data = {
        "model": MODEL,
        "messages": [{"role": "user", "content": prompt}],
        "stream": True,
        "temperature": temperature,
        "session_id": session_id
    }

    print(f"Sending request... (Session ID: {session_id})")
    print(f"Prompt length: {len(prompt)} chars")
    
    start_time = time.time()
    first_token_time = None
    last_token_time = None
    
    input_tokens = 0
    output_tokens = 0
    cached_tokens = 0
    content_length = 0
    chunk_count = 0
    
    try:
        response = requests.post(url, headers=headers, data=json.dumps(data), stream=True)
        response.raise_for_status()
        
        for line in response.iter_lines():
            if line:
                decoded_line = line.decode('utf-8')
                if decoded_line.startswith("data: "):
                    json_str = decoded_line[6:]
                    if json_str == "[DONE]":
                        break
                    try:
                        chunk = json.loads(json_str)
                        current_time = time.time()
                        
                        # 1. Measure TTFT (Time To First Token)
                        # Check if this chunk actually contains content delta
                        has_content = False
                        if 'choices' in chunk and len(chunk['choices']) > 0:
                            delta = chunk['choices'][0].get('delta', {})
                            if 'content' in delta and delta['content']:
                                has_content = True
                                content_length += len(delta['content'])
                                # Print first few chars to show it's working
                                if chunk_count < 3:
                                    sys.stdout.write(delta['content'])
                                    sys.stdout.flush()

                        if has_content:
                            chunk_count += 1
                            if first_token_time is None:
                                first_token_time = current_time
                            last_token_time = current_time

                        # 2. Capture Usage Stats
                        if 'usage' in chunk and chunk['usage']:
                            usage = chunk['usage']
                            # Accumulate max seen values as usage often comes incrementally or at end
                            input_tokens = max(input_tokens, usage.get('prompt_tokens', 0))
                            output_tokens = max(output_tokens, usage.get('completion_tokens', 0))
                            
                            # Handle different cache field locations (OpenAI vs Anthropic style)
                            current_cached = 0
                            if 'prompt_tokens_details' in usage:
                                current_cached = usage['prompt_tokens_details'].get('cached_tokens', 0)
                            elif 'cached_tokens' in usage:
                                current_cached = usage.get('cached_tokens', 0)
                            cached_tokens = max(cached_tokens, current_cached)

                    except json.JSONDecodeError:
                        pass
        
        end_time = time.time()
        
        # Calculations
        total_duration = end_time - start_time
        ttft = (first_token_time - start_time) if first_token_time else total_duration
        generation_time = (last_token_time - first_token_time) if (last_token_time and first_token_time) else 0
        tps = (output_tokens / generation_time) if generation_time > 0 else 0
        
        # Display Metrics
        print("\n\n" + "-"*30)
        print("PERFORMANCE METRICS")
        print("-"*30)
        print(f"Total Duration:   {total_duration:.4f} s")
        print(f"TTFT (First Token): {ttft:.4f} s")
        if generation_time > 0:
            print(f"Generation Time:  {generation_time:.4f} s")
            print(f"Tokens Per Second: {tps:.2f} t/s (Output)")
        
        print("-"*30)
        print("TOKEN USAGE")
        print("-"*30)
        print(f"Input Tokens:     {input_tokens}")
        print(f"Output Tokens:    {output_tokens}")
        print(f"Cached Tokens:    {cached_tokens}")
        
        hit_rate = 0.0
        # Correct hit rate calculation: cached / (input + cached) if input doesn't include cached
        # Or cached / total_input if input includes cached.
        # Assuming input_tokens includes cached tokens for now based on standard OpenAI API behavior?
        # Actually, usually prompt_tokens = cache_miss_tokens + cached_tokens.
        # So hit_rate = cached_tokens / prompt_tokens.
        
        if input_tokens > 0:
            # Check if input_tokens < cached_tokens (Anomaly seen in logs: Input 872, Cached 19774)
            # This suggests 'prompt_tokens' might be NET input (miss), not TOTAL input.
            if cached_tokens > input_tokens:
                 # It seems for this provider/proxy, prompt_tokens excludes cached?
                 # Let's adjust hit rate calculation to be safe
                 total_input = input_tokens + cached_tokens
                 hit_rate = (cached_tokens / total_input) * 100
                 print(f"Cache Hit Rate:   {hit_rate:.2f}% (Adjusted: Cached > Input)")
            else:
                 hit_rate = (cached_tokens / input_tokens) * 100
                 print(f"Cache Hit Rate:   {hit_rate:.2f}%")
        
        return {
            "ttft": ttft,
            "duration": total_duration,
            "input": input_tokens,
            "cached": cached_tokens,
            "hit_rate": hit_rate
        }

    except Exception as e:
        print(f"\n[FAIL] Request Failed: {e}")
        return None

def main():
    print("Starting Performance Benchmark...")
    print(f"Target: {BASE_URL}")
    
    results = []
    
    # 1. Cold Start Test (Short)
    # New session ID ensures no previous context cache
    res1 = run_test_scenario(
        "1. Cold Start (Short Prompt)",
        "Calculate 123 * 456. Only output the result."
    )
    if res1: results.append(("Cold Start", res1))
    
    # 2. Hot Cache Test (Short)
    # Using SAME session ID and SAME prompt should trigger cache if implemented correctly
    # Or even different session ID but same System Prompt prefix should trigger Prefix Caching
    res2 = run_test_scenario(
        "2. Hot Cache (Same Prompt, New Session)",
        "Calculate 123 * 456. Only output the result."
    )
    if res2: results.append(("Hot Cache", res2))

    # 3. Long Context Test (Cold)
    # Create a long prompt to make caching obvious
    long_context = "You are a helpful assistant. " + "The quick brown fox jumps over the lazy dog. " * 500 + "What is the third word of this sentence?"
    res3 = run_test_scenario(
        "3. Long Context (Cold)",
        long_context
    )
    if res3: results.append(("Long Cold", res3))

    # 4. Long Context Test (Hot)
    # Same prompt again
    res4 = run_test_scenario(
        "4. Long Context (Hot)",
        long_context
    )
    if res4: results.append(("Long Hot", res4))

    # Final Summary
    print("\n" + "="*60)
    print(f"{'SCENARIO':<30} | {'TTFT (s)':<10} | {'HIT RATE':<10} | {'INPUT':<8}")
    print("-" * 60)
    for name, metrics in results:
        print(f"{name:<30} | {metrics['ttft']:.4f}     | {metrics['hit_rate']:.1f}%      | {metrics['input']:<8}")
    print("="*60)

if __name__ == "__main__":
    main()
