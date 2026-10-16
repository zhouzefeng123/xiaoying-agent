from __future__ import annotations

import argparse
import json
import time
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any

import requests


DEFAULT_PROMPT = (
    "先列出工作区根目录，再搜索 README.md，然后读取 backend/README.md 的前 20 行，最后用一句话总结。"
)


@dataclass
class StreamProbeReport:
    passed: bool
    diagnostics: list[str] = field(default_factory=list)
    failure_reasons: list[str] = field(default_factory=list)
    base_url: str = ""
    model: str = ""
    session_id: str = ""
    response_id: str = ""
    duration_seconds: float = 0.0
    prompt: str = ""
    tool_call_count: int = 0
    tool_calls: list[dict[str, Any]] = field(default_factory=list)
    usage_events: list[dict[str, Any]] = field(default_factory=list)
    context_stats: list[dict[str, Any]] = field(default_factory=list)
    content_preview: str = ""
    final_content: str = ""
    raw_events_tail: list[dict[str, Any]] = field(default_factory=list)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="glm5 流式兼容性接口探针")
    parser.add_argument("--base-url", default="http://localhost:3000", help="后端地址")
    parser.add_argument("--model", default="glm5", help="要测试的模型")
    parser.add_argument("--prompt", default=DEFAULT_PROMPT, help="测试提示词")
    parser.add_argument("--timeout-seconds", type=int, default=180, help="请求超时")
    parser.add_argument("--report-path", default="", help="输出 JSON 报告路径")
    return parser.parse_args()


def build_payload(model: str, prompt: str, session_id: str) -> dict[str, Any]:
    return {
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
        "stream": True,
        "temperature": 0.0,
        "session_id": session_id,
    }


def trim_text(text: str, limit: int = 1200) -> str:
    if len(text) <= limit:
        return text
    return text[:limit] + "\n...<truncated>..."


def run_probe(base_url: str, model: str, prompt: str, timeout_seconds: int) -> StreamProbeReport:
    session_id = f"glm5_stream_probe_{int(time.time() * 1000)}"
    started_at = time.time()
    response_id = ""
    content_parts: list[str] = []
    tool_calls: list[dict[str, Any]] = []
    usage_events: list[dict[str, Any]] = []
    context_stats: list[dict[str, Any]] = []
    raw_events: list[dict[str, Any]] = []
    diagnostics: list[str] = []
    failure_reasons: list[str] = []

    response = requests.post(
        f"{base_url}/v1/chat/completions",
        headers={"Content-Type": "application/json; charset=utf-8"},
        data=json.dumps(build_payload(model=model, prompt=prompt, session_id=session_id), ensure_ascii=False),
        stream=True,
        timeout=timeout_seconds,
    )
    response.raise_for_status()

    for raw_line in response.iter_lines(decode_unicode=False):
        if not raw_line:
            continue
        line = raw_line.decode("utf-8", errors="replace")
        if not line.startswith("data: "):
            continue

        payload = line[6:]
        if payload == "[DONE]":
            break

        chunk = json.loads(payload)
        raw_events.append(chunk)
        if not response_id and chunk.get("id"):
            response_id = str(chunk["id"])

        if chunk.get("usage"):
            usage_events.append(chunk["usage"])
        if chunk.get("context_stats"):
            context_stats.append(chunk["context_stats"])

        choices = chunk.get("choices") or []
        if not choices:
            continue

        delta = choices[0].get("delta") or {}
        content = delta.get("content")
        if isinstance(content, str) and content:
            content_parts.append(content)

        for tool_call in delta.get("tool_calls") or []:
            function = tool_call.get("function") or {}
            tool_calls.append(
                {
                    "id": tool_call.get("id", ""),
                    "index": tool_call.get("index"),
                    "type": tool_call.get("type", ""),
                    "name": function.get("name", ""),
                    "arguments": function.get("arguments", ""),
                }
            )

    final_content = "".join(content_parts)
    lower_content = final_content.lower()

    if not tool_calls:
        failure_reasons.append("没有观察到任何 tool_calls 流式事件。")
    if len(tool_calls) < 2:
        failure_reasons.append(f"tool_calls 数量不足，期望至少 2 个，实际 {len(tool_calls)} 个。")
    if "invalid_parameter_error" in lower_content or "function.arguments" in lower_content:
        failure_reasons.append("流式工具调用在后续轮次触发了 function.arguments JSON 非法错误。")
    if "[error]:" in lower_content:
        failure_reasons.append("流式内容中包含错误块。")

    diagnostics.append(f"response_id={response_id or 'N/A'}")
    diagnostics.append(f"tool_call_count={len(tool_calls)}")
    diagnostics.append(f"usage_event_count={len(usage_events)}")
    diagnostics.append(f"context_stats_count={len(context_stats)}")
    if tool_calls:
        diagnostics.append("tool_names=" + ", ".join(str(item.get("name") or "") for item in tool_calls))
    if usage_events:
        diagnostics.append("last_usage=" + json.dumps(usage_events[-1], ensure_ascii=False))

    return StreamProbeReport(
        passed=not failure_reasons,
        diagnostics=diagnostics,
        failure_reasons=failure_reasons,
        base_url=base_url,
        model=model,
        session_id=session_id,
        response_id=response_id,
        duration_seconds=time.time() - started_at,
        prompt=prompt,
        tool_call_count=len(tool_calls),
        tool_calls=tool_calls,
        usage_events=usage_events,
        context_stats=context_stats,
        content_preview=trim_text(final_content),
        final_content=final_content,
        raw_events_tail=raw_events[-12:],
    )


def write_report(report_path: str, report: StreamProbeReport) -> Path:
    target = Path(report_path)
    if not target.is_absolute():
        target = Path.cwd() / target
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(json.dumps(asdict(report), ensure_ascii=False, indent=2), encoding="utf-8")
    return target


def main() -> int:
    args = parse_args()
    report = run_probe(
        base_url=args.base_url.rstrip("/"),
        model=args.model,
        prompt=args.prompt,
        timeout_seconds=args.timeout_seconds,
    )

    print(f"模型: {report.model}")
    print(f"会话: {report.session_id}")
    print(f"耗时: {report.duration_seconds:.2f}s")
    print(f"tool_calls: {report.tool_call_count}")
    for item in report.diagnostics:
        print(f"DIAG: {item}")
    if report.failure_reasons:
        for item in report.failure_reasons:
            print(f"FAIL: {item}")
    else:
        print("PASS: glm5 流式工具调用兼容")

    if args.report_path:
        saved = write_report(args.report_path, report)
        print(f"报告: {saved}")

    return 0 if report.passed else 1


if __name__ == "__main__":
    raise SystemExit(main())