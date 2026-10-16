"""
DeepSeek KV Cache Benchmark & Optimization Script.

DeepSeek KV Cache 机制:
- 服务端自动缓存, 基于 prompt prefix 精确匹配
- 相同前缀的连续请求可命中缓存, 报告 prompt_cache_hit_tokens
- 缓存有时效性, 通常几分钟内有效
- 不需要客户端发送特殊 header (不同于 Aliyun DashScope 的 X-DashScope-WorkFlow)

使用方法:
1. 确保后端已启动 (air 在 3000 端口)
2. 修改下方的 MODEL 为你的 DeepSeek 自定义模型名称
3. python test_cache_optimization.py
"""

import json
import sys
import time
import uuid

import requests

# ============================================================
# 配置区 - 修改此处
# ============================================================
BASE_URL = "http://localhost:3000/v1/chat/completions"
MODEL = "deepseek-v4-flash"  # 你的 DeepSeek 自定义模型名称
AUTH_TOKEN = "test-key"

# 冷却期: 两次相同请求之间的等待秒数 (确保缓存不过期)
COOLDOWN_S = 1.0
# 大前缀长度 (模拟真实 agent 注入的 system prompt)
LARGE_PREFIX_MULTIPLIER = 80
# ============================================================


def banner(text):
    print(f"\n{'#' * 64}\n#  {text}\n{'#' * 64}")


def sep(text=""):
    print(f"\n{'-' * 48} {text}")


def extract_usage(usage):
    """从 SSE chunk 的 usage 字段提取缓存信息"""
    prompt_tokens = int(usage.get("prompt_tokens", 0) or 0)
    details = usage.get("prompt_tokens_details") or {}
    cached_tokens = int(details.get("cached_tokens", 0) or 0)

    if not cached_tokens:
        cached_tokens = int(usage.get("prompt_cache_hit_tokens", 0) or 0)

    miss_tokens = max(prompt_tokens - cached_tokens, 0) if prompt_tokens else 0
    hit_ratio = (cached_tokens / prompt_tokens) if prompt_tokens else 0.0
    return prompt_tokens, cached_tokens, miss_tokens, hit_ratio


def chat(messages, session_id=None, label=""):
    """发送 chat completion 请求, 返回指标字典"""
    headers = {
        "Content-Type": "application/json",
        "Authorization": f"Bearer {AUTH_TOKEN}",
    }
    data = {
        "model": MODEL,
        "messages": messages,
        "stream": True,
        "temperature": 0,
    }
    if session_id:
        data["session_id"] = session_id

    msg_len = len(json.dumps(messages, ensure_ascii=False))
    print(f"  [{label}] sending {len(messages)} msgs ({msg_len} chars)...", end=" ", flush=True)

    start = time.time()
    ttft = None
    content_parts = []
    resp_session_id = session_id
    prompt_tokens = 0
    cached_tokens = 0
    miss_tokens = 0
    hit_ratio = 0.0

    try:
        resp = requests.post(
            BASE_URL, headers=headers,
            data=json.dumps(data, ensure_ascii=False),
            stream=True, timeout=300,
        )
        if resp.status_code != 200:
            print(f"FAIL HTTP {resp.status_code}: {resp.text[:200]}")
            return None

        for line in resp.iter_lines():
            if not line:
                continue
            decoded = line.decode("utf-8", errors="ignore")
            if not decoded.startswith("data: "):
                continue
            payload = decoded[6:]
            if payload == "[DONE]":
                break
            try:
                chunk = json.loads(payload)
            except Exception:
                continue

            if not resp_session_id and chunk.get("id"):
                resp_session_id = chunk["id"]

            usage = chunk.get("usage")
            if usage:
                prompt_tokens, cached_tokens, miss_tokens, hit_ratio = extract_usage(usage)

            choices = chunk.get("choices") or []
            if not choices:
                continue
            delta = choices[0].get("delta", {})
            content = delta.get("content", "")
            if content:
                if ttft is None:
                    ttft = time.time()
                content_parts.append(content)

        elapsed = time.time() - start
        if ttft is None:
            ttft = time.time()
        ttft_val = ttft - start

        text = "".join(content_parts)
        preview = text[:100].replace("\n", "\\n")
        print(f"OK, ttft={ttft_val:.2f}s, total={elapsed:.1f}s")
        sep()
        print(f"  Prompt:  {prompt_tokens:>6} tokens")
        print(f"  Cached:  {cached_tokens:>6} tokens  ({hit_ratio:.1%})")
        print(f"  Miss:    {miss_tokens:>6} tokens")
        print(f"  Output:  {len(text):>6} chars")
        if resp_session_id:
            print(f"  Session: {resp_session_id}")
        print(f"  Preview: {preview}")

        return {
            "ttft": ttft_val,
            "total_duration": elapsed,
            "content": text,
            "session_id": resp_session_id,
            "prompt_tokens": prompt_tokens,
            "cached_tokens": cached_tokens,
            "miss_tokens": miss_tokens,
            "hit_ratio": hit_ratio,
        }
    except Exception as e:
        print(f"FAIL: {e}")
        return None


def build_prefix(marker=None):
    """构造稳定的 shared system prefix"""
    if marker is None:
        marker = f"KV_PREFIX_{uuid.uuid4().hex[:8].upper()}"
    block = (
        f"{marker}: This is a DeepSeek KV-cache benchmark system prefix. "
        "Treat it as immutable background context. Keep answers short and deterministic. "
    ) * LARGE_PREFIX_MULTIPLIER
    return marker, block


def summary_table(results):
    """打印结果汇总表格"""
    print(f"\n{'=' * 80}")
    print(f"{'Case':<4} {'Description':<38} {'Prompt':>7} {'Cached':>7} {'Hit%':>7} {'TTFT':>7} {'Total':>7}")
    print(f"{'-' * 80}")
    for row in results:
        if row is None:
            continue
        label, desc, r = row
        if r is None:
            print(f"{label:<4} {desc:<38} {'ERROR':>7}")
            continue
        print(
            f"{label:<4} {desc:<38} "
            f"{r['prompt_tokens']:>7} {r['cached_tokens']:>7} "
            f"{r['hit_ratio']:>7.1%} {r['ttft']:>6.2f}s {r['total_duration']:>6.2f}s"
        )
    print(f"{'=' * 80}")


def main():
    banner("DeepSeek KV Cache Benchmark")
    print(f"Model:  {MODEL}")
    print(f"Target: {BASE_URL}")
    print(f"Prefix size multiplier: {LARGE_PREFIX_MULTIPLIER}")

    # ---- Phase 1: Basic Cold/Warm Cache Validation ----
    banner("Phase 1: Cold → Warm (Same Message Exact Replay)")

    marker, prefix = build_prefix()
    print(f"Prefix marker: {marker}  ({len(prefix)} chars)")

    exact_msgs = [
        {"role": "system", "content": prefix},
        {"role": "user", "content": "Reply with exactly: OK"},
    ]

    r1 = chat(exact_msgs, label="COLD")
    if not r1:
        print("\n[FATAL] Cold request failed. Check backend and model configuration.")
        sys.exit(1)
    time.sleep(COOLDOWN_S)

    r2 = chat(exact_msgs, label="WARM")
    if not r2:
        sys.exit(1)

    results = [
        ("C1", "Cold (first request)", r1),
        ("C2", "Warm (exact replay)", r2),
    ]

    # ---- Phase 2: Prefix Stability ----
    banner("Phase 2: Prefix Stability (Same Prefix, Different Suffix)")

    diff_user_short = [
        {"role": "system", "content": prefix},
        {"role": "user", "content": "What is 2+2? Answer in one word."},
    ]

    diff_user_long = [
        {"role": "system", "content": prefix},
        {
            "role": "user",
            "content": (
                "Explain the concept of recursion in one sentence. "
                "Keep it under 20 words."
            ),
        },
    ]

    r3 = chat(diff_user_short, label="P-SHORT")
    if not r3:
        sys.exit(1)
    time.sleep(COOLDOWN_S)

    r4 = chat(diff_user_long, label="P-LONG")
    if not r4:
        sys.exit(1)

    results.extend([
        ("P1", "Same prefix, short user msg", r3),
        ("P2", "Same prefix, long user msg", r4),
    ])

    # ---- Phase 3: Cross-Session ----
    banner("Phase 3: Cross-Session (Different Session ID, Same Prefix)")

    r5 = chat(exact_msgs, label="XS-1")
    if not r5:
        sys.exit(1)
    time.sleep(COOLDOWN_S)

    r6 = chat(exact_msgs, label="XS-2")
    if not r6:
        sys.exit(1)
    time.sleep(COOLDOWN_S)

    r7 = chat(exact_msgs, label="XS-3")
    if not r7:
        sys.exit(1)

    results.extend([
        ("X1", "Cross-session, new session ID", r5),
        ("X2", "Cross-session, another new ID", r6),
        ("X3", "Cross-session, yet another ID", r7),
    ])

    # ---- Phase 4: Stateful Multi-Turn ----
    banner("Phase 4: Stateful Multi-Turn (Single Session, Accumulating Context)")

    turn1_msgs = [
        {"role": "system", "content": prefix},
        {
            "role": "user",
            "content": (
                "Remember: PROJECT=ALPHA, VERSION=2.0, ENV=PROD. "
                "Reply with ACK1 only."
            ),
        },
    ]

    t1 = chat(turn1_msgs, label="TURN-1")
    if not t1:
        sys.exit(1)
    sess_id = t1["session_id"]
    time.sleep(COOLDOWN_S)

    # Turn 2: Only send the new user message, backend carries history via session_id
    t2 = chat(
        [{"role": "user", "content": "What is PROJECT and ENV? Reply as JSON."}],
        session_id=sess_id, label="TURN-2",
    )
    if not t2:
        sys.exit(1)
    time.sleep(COOLDOWN_S)

    t3 = chat(
        [{"role": "user", "content": "What was the VERSION? Reply in one word."}],
        session_id=sess_id, label="TURN-3",
    )
    if not t3:
        sys.exit(1)

    results.extend([
        ("T1", "Multi-turn: turn 1 (init)", t1),
        ("T2", "Multi-turn: turn 2 (query)", t2),
        ("T3", "Multi-turn: turn 3 (query)", t3),
    ])

    # ---- Phase 5: Intent Drift Diagnostics ----
    banner("Phase 5: Intent Drift Diagnostics")

    drift_code = [
        {"role": "system", "content": prefix},
        {
            "role": "user",
            "content": (
                "Review this code for issues in one short sentence:\n"
                "def fib(n):\n"
                "    if n <= 1: return n\n"
                "    return fib(n-1) + fib(n-2)"
            ),
        },
    ]

    drift_creative = [
        {"role": "system", "content": prefix},
        {
            "role": "user",
            "content": "Write a one-line poem about programming.",
        },
    ]

    r8 = chat(drift_code, label="DRIFT-CODE")
    if not r8:
        sys.exit(1)
    time.sleep(COOLDOWN_S)

    r9 = chat(drift_creative, label="DRIFT-CREATIVE")
    if not r9:
        sys.exit(1)

    results.extend([
        ("D1", "Intent drift: code review", r8),
        ("D2", "Intent drift: creative", r9),
    ])

    # ---- 汇总分析 ----
    banner("RESULTS SUMMARY")
    summary_table(results)

    banner("ANALYSIS")
    all_results = [r for _, _, r in results if r is not None]

    cold_hit = r1["hit_ratio"]
    warm_hit = r2["hit_ratio"]
    cross_session_hits = [r["hit_ratio"] for r in [r5, r6, r7]]

    print()
    if warm_hit > 0.30:
        print(f"✅ [COLD→WARM] Cache hit improved: {cold_hit:.1%} → {warm_hit:.1%}")
        if warm_hit > 0.60:
            print("   Excellent cache reuse on exact replay. Prefix is stable.")
        else:
            print("   Partial cache hit. Agent may inject dynamic blocks before the "
                  "checkpoint that differ between calls.")
    else:
        print(f"❌ [COLD→WARM] No significant cache hit: {cold_hit:.1%} → {warm_hit:.1%}")
        print("   Possible causes:")
        print("   1. Agent injects unique/dynamic system prompt each call")
        print("   2. Model not routed through cache-enabled path")
        print("   3. DeepSeek cache TTL expired (wait time too long)")
        print("   4. Model provider configuration issue")

    avg_cross = sum(cross_session_hits) / len(cross_session_hits) if cross_session_hits else 0
    if avg_cross > 0.20:
        print(f"✅ [CROSS-SESSION] Average cache hit across sessions: {avg_cross:.1%}")
        print("   DeepSeek server-side cache persists across different session IDs.")
    else:
        print(f"ℹ️  [CROSS-SESSION] Average cache hit across sessions: {avg_cross:.1%}")
        print("   Cross-session cache may be lower if agent context varies per session.")

    same_prefix_hits = [r3["hit_ratio"], r4["hit_ratio"]]
    avg_prefix = sum(same_prefix_hits) / len(same_prefix_hits)
    if avg_prefix > 0.20:
        print(f"✅ [PREFIX STABILITY] Same prefix, different suffix: avg {avg_prefix:.1%} hit")
    else:
        print(f"ℹ️  [PREFIX STABILITY] Same prefix, different suffix: avg {avg_prefix:.1%} hit")
        print("   The prefix before the user message may differ between calls "
              "(dynamic system blocks, tool lists, etc.)")

    print()
    print("DeepSeek KV Cache Optimization Checklist:")
    print("  1. System prompt should be IDENTICAL between calls (same bytes)")
    print("  2. Dynamic content (RAG, memory, tool lists) placed AFTER static prefix")
    print("  3. cache_control markers (if supported) on static blocks")
    print("  4. Short cooldown between requests (< 60s for best cache hit)")
    print("  5. Same model routing path each call")


if __name__ == "__main__":
    main()