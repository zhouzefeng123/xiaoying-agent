import json
import os
import statistics
import sys
import time
import uuid

import requests

BASE_URL = "http://localhost:3000/v1/chat/completions"
MODEL = "qwen3.5-plus"
AUTH_TOKEN = "test-key"


def stream_chat(user_prompt, session_id=None):
    headers = {
        "Content-Type": "application/json",
        "Authorization": f"Bearer {AUTH_TOKEN}",
    }
    data = {
        "model": MODEL,
        "messages": [{"role": "user", "content": user_prompt}],
        "stream": True,
        "temperature": 0,
    }
    if session_id:
        data["session_id"] = session_id

    start = time.time()
    first_token_time = None
    response_text = []
    usage = {}
    context_stats = {}
    resolved_session_id = session_id

    response = requests.post(BASE_URL, headers=headers, data=json.dumps(data), stream=True, timeout=180)
    if response.status_code != 200:
        return {
            "ok": False,
            "error": f"{response.status_code} {response.text}",
            "session_id": resolved_session_id,
        }

    for raw in response.iter_lines():
        if not raw:
            continue
        line = raw.decode("utf-8", errors="ignore")
        if not line.startswith("data: "):
            continue
        payload = line[6:]
        if payload == "[DONE]":
            break
        try:
            chunk = json.loads(payload)
        except Exception:
            continue

        if not resolved_session_id and chunk.get("id"):
            resolved_session_id = chunk.get("id")

        if "context_stats" in chunk and chunk["context_stats"]:
            context_stats = chunk["context_stats"]
        if "usage" in chunk and chunk["usage"]:
            usage = chunk["usage"]

        choices = chunk.get("choices") or []
        if choices:
            delta = choices[0].get("delta", {})
            content = delta.get("content", "")
            if content:
                if first_token_time is None:
                    first_token_time = time.time()
                response_text.append(content)

    end = time.time()
    if first_token_time is None:
        first_token_time = end

    prompt_tokens = usage.get("prompt_tokens", 0)
    cached_tokens = 0
    details = usage.get("prompt_tokens_details") or {}
    if isinstance(details, dict):
        cached_tokens = details.get("cached_tokens", 0)
    if not cached_tokens:
        cached_tokens = usage.get("prompt_cache_hit_tokens", 0) or usage.get("cached_tokens", 0)

    return {
        "ok": True,
        "session_id": resolved_session_id,
        "text": "".join(response_text),
        "ttft": first_token_time - start,
        "total_duration": end - start,
        "prompt_tokens": prompt_tokens,
        "cached_tokens": cached_tokens,
        "context_stats": context_stats,
    }


def run_noise_session():
    marker = "NOISE_" + uuid.uuid4().hex[:10].upper()
    prompt = f"记住这个隔离标识：{marker}。只回复: STORED"
    result = stream_chat(prompt, None)
    return marker, result


def build_scenario():
    seed = uuid.uuid4().hex.upper()
    return {
        "project": f"PROJ_{seed[:10]}",
        "file_initial": f"f_{seed[10:16].lower()}.go",
        "file_updated": f"f_{seed[16:22].lower()}_ctx.go",
        "risk": f"R{seed[22:30]}",
        "task": f"TASK_{seed[4:12]}",
        "checkpoint": f"CP_{seed[12:20]}",
        "note": f"NOTE_{seed[20:28]}",
    }


def build_turns(s):
    return [
        (f"请记住三个常量：PROJECT={s['project']}, FILE={s['file_initial']}, RISK={s['risk']}。只回复ACK1", "ACK1", False),
        ("把上一条三个常量按 JSON 输出，键名 project/file/risk", s["project"], True),
        (f"现在新增 TASK={s['task']}。只回复ACK2", "ACK2", False),
        ("输出 project 与 task，格式 project=<x>;task=<y>", s["task"], True),
        (f"新增 CHECKPOINT={s['checkpoint']}。只回复ACK3", "ACK3", False),
        ("请输出 risk 与 checkpoint，格式 risk=<x>;checkpoint=<y>", s["risk"], True),
        (f"把 file 改成 {s['file_updated']}，并只回复ACK4", "ACK4", False),
        ("现在输出 file 与 task，格式 file=<x>;task=<y>", s["file_updated"], True),
        (f"新增 NOTE={s['note']}，只回复ACK5", "ACK5", False),
        ("请总结 project/task/note 三项，用逗号分隔", s["note"], True),
        ("请仅输出你在当前会话中看到的 NOISE_ 标识原文；如果没有看到，输出 NONE。只回复一个词。", None, False),
        ("最后请输出 project/file/risk/task/note 五项", s["project"], True),
    ]


def evaluate_suite(suite_name, turns, keep_session, noise_marker):
    session_id = None
    results = []
    recall_checks = 0
    recall_pass = 0
    isolation_pass = True

    print(f"\n=== Running Suite: {suite_name} ===")
    for idx, (prompt, expected, check_recall) in enumerate(turns, start=1):
        turn_session = session_id if keep_session else None
        res = stream_chat(prompt, turn_session)
        if not res["ok"]:
            return {"ok": False, "error": f"Turn {idx} failed: {res['error']}"}
        if keep_session:
            session_id = res["session_id"]
        elif idx == 1:
            session_id = res["session_id"]

        text = (res["text"] or "").strip()
        matched = True if expected is None else (expected.lower() in text.lower())
        if check_recall:
            recall_checks += 1
            if matched:
                recall_pass += 1
        if idx == 11:
            isolation_pass = noise_marker not in text

        ctx = res.get("context_stats") or {}
        current_tokens = ctx.get("current_tokens", 0)
        max_tokens = ctx.get("max_tokens", 0)
        msg_count = ctx.get("message_count", 0)
        util = (current_tokens / max_tokens) if max_tokens else 0.0

        results.append(
            {
                "turn": idx,
                "prompt_tokens": res["prompt_tokens"],
                "cached_tokens": res["cached_tokens"],
                "ttft": res["ttft"],
                "duration": res["total_duration"],
                "current_tokens": current_tokens,
                "max_tokens": max_tokens,
                "utilization": util,
                "message_count": msg_count,
                "matched": matched,
                "response_preview": text[:120],
            }
        )
        print(
            f"[{suite_name} TURN {idx:02d}] ttft={res['ttft']:.2f}s prompt={res['prompt_tokens']} cached={res['cached_tokens']} "
            f"ctx={current_tokens}/{max_tokens} util={util:.2%} matched={matched}"
        )

    prompt_tokens_series = [r["prompt_tokens"] for r in results if r["prompt_tokens"] > 0]
    context_series = [r["current_tokens"] for r in results if r["current_tokens"] > 0]
    util_series = [r["utilization"] for r in results if r["max_tokens"] > 0]
    ttft_series = [r["ttft"] for r in results]
    cache_ratio_series = [(r["cached_tokens"] / r["prompt_tokens"]) for r in results if r["prompt_tokens"] > 0]

    recall_rate = (recall_pass / recall_checks) if recall_checks else 0.0
    context_growth = (context_series[-1] - context_series[0]) if len(context_series) >= 2 else 0
    avg_ttft = statistics.mean(ttft_series) if ttft_series else 0.0
    avg_util = statistics.mean(util_series) if util_series else 0.0
    avg_cache_ratio = statistics.mean(cache_ratio_series) if cache_ratio_series else 0.0
    max_prompt_tokens = max(prompt_tokens_series) if prompt_tokens_series else 0

    health = {
        "recall_ok": recall_rate >= 0.80,
        "isolation_ok": isolation_pass,
        "util_ok": avg_util < 0.75,
        "ttft_ok": avg_ttft < 8.0,
        "growth_ok": context_growth < 12000,
    }
    passed = all(health.values())

    summary = {
        "suite": suite_name,
        "session_id": session_id,
        "keep_session": keep_session,
        "recall_rate": recall_rate,
        "isolation_pass": isolation_pass,
        "avg_ttft": avg_ttft,
        "avg_context_utilization": avg_util,
        "avg_cache_ratio": avg_cache_ratio,
        "context_growth": context_growth,
        "max_prompt_tokens": max_prompt_tokens,
        "health": health,
        "passed": passed,
    }
    return {"ok": True, "summary": summary, "turns": results}


def print_summary(summary):
    print(f"Suite: {summary['suite']}")
    print(f"Session ID: {summary['session_id']}")
    print(f"Recall rate: {summary['recall_rate']:.2%}")
    print(f"Isolation pass: {summary['isolation_pass']}")
    print(f"Avg TTFT: {summary['avg_ttft']:.2f}s")
    print(f"Avg context utilization: {summary['avg_context_utilization']:.2%}")
    print(f"Avg cache ratio: {summary['avg_cache_ratio']:.2%}")
    print(f"Context growth: {summary['context_growth']}")
    print(f"Max prompt tokens: {summary['max_prompt_tokens']}")
    print(f"Health checks: {summary['health']}")
    print(f"Overall: {'PASS' if summary['passed'] else 'FAIL'}")


def main():
    print("=== Multi-turn Optimization Validation (A/B) ===")
    print(f"Target: {BASE_URL}")
    scenario = build_scenario()

    noise_marker, noise_res = run_noise_session()
    if not noise_res["ok"]:
        print(f"[FAIL] Noise session failed: {noise_res['error']}")
        sys.exit(1)
    print(f"[INFO] Noise marker prepared: {noise_marker}")

    turns = build_turns(scenario)
    suite_a = evaluate_suite("A_KEEP_SESSION", turns, True, noise_marker)
    if not suite_a["ok"]:
        print(f"[FAIL] {suite_a['error']}")
        sys.exit(1)
    suite_b = evaluate_suite("B_NEW_SESSION_EACH_TURN", turns, False, noise_marker)
    if not suite_b["ok"]:
        print(f"[FAIL] {suite_b['error']}")
        sys.exit(1)

    print("\n=== Summary A ===")
    print_summary(suite_a["summary"])
    print("\n=== Summary B ===")
    print_summary(suite_b["summary"])

    compare = {
        "recall_rate_delta": suite_a["summary"]["recall_rate"] - suite_b["summary"]["recall_rate"],
        "ttft_delta_seconds": suite_a["summary"]["avg_ttft"] - suite_b["summary"]["avg_ttft"],
        "cache_ratio_delta": suite_a["summary"]["avg_cache_ratio"] - suite_b["summary"]["avg_cache_ratio"],
        "context_util_delta": suite_a["summary"]["avg_context_utilization"] - suite_b["summary"]["avg_context_utilization"],
        "max_prompt_tokens_delta": suite_a["summary"]["max_prompt_tokens"] - suite_b["summary"]["max_prompt_tokens"],
    }
    compare_signals = {
        "a_cache_better": compare["cache_ratio_delta"] > 0.05,
        "a_ttft_not_worse": compare["ttft_delta_seconds"] <= 0.05,
        "a_recall_not_worse": compare["recall_rate_delta"] >= -0.05,
    }
    ab_pass = suite_a["summary"]["passed"] and suite_b["summary"]["passed"]
    print("\n=== A/B Compare ===")
    print(f"Recall delta (A-B): {compare['recall_rate_delta']:.2%}")
    print(f"TTFT delta (A-B): {compare['ttft_delta_seconds']:.3f}s")
    print(f"Cache ratio delta (A-B): {compare['cache_ratio_delta']:.2%}")
    print(f"Context util delta (A-B): {compare['context_util_delta']:.2%}")
    print(f"Max prompt tokens delta (A-B): {compare['max_prompt_tokens_delta']}")
    print(f"Signals: {compare_signals}")
    print(f"A/B conclusion: {'PASS' if ab_pass else 'FAIL'}")

    report = {
        "noise_marker": noise_marker,
        "scenario": scenario,
        "suite_a": {"summary": suite_a["summary"], "turns": suite_a["turns"]},
        "suite_b": {"summary": suite_b["summary"], "turns": suite_b["turns"]},
        "compare": compare,
        "compare_signals": compare_signals,
        "ab_pass": ab_pass,
    }
    out_path = "tests/python/reports/multi_turn_optimization_ab_report.json"
    try:
        os.makedirs("tests/python/reports", exist_ok=True)
        with open(out_path, "w", encoding="utf-8") as f:
            json.dump(report, f, ensure_ascii=False, indent=2)
        print(f"Report saved: {out_path}")
    except Exception as e:
        print(f"Report save skipped: {e}")

    if not ab_pass:
        sys.exit(2)


if __name__ == "__main__":
    main()
