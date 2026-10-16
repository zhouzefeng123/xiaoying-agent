from __future__ import annotations

import argparse
import json
import os
import sys
import time
import uuid
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any

import requests


DEFAULT_BASE_URL = os.getenv("AURAMATE_BASE_URL", "http://localhost:3000")
DEFAULT_MODEL = os.getenv("AURAMATE_MODEL", "aura-coder")
DEFAULT_REPORT_PATH = "backend/tests/python/reports/memory_dialogue_closure_report.json"
REQUEST_TIMEOUT_SECONDS = 600
REPO_ROOT = Path(__file__).resolve().parents[3]
WORKSPACE_DIR = REPO_ROOT / "workspace"


@dataclass
class UsageSnapshot:
    prompt_tokens: int = 0
    completion_tokens: int = 0
    total_tokens: int = 0
    cached_tokens: int = 0


@dataclass
class ToolCallRecord:
    index: int
    name: str
    arguments: str
    raw: dict[str, Any] = field(default_factory=dict)

    def parsed_argument_items(self) -> list[dict[str, Any]]:
        return parse_json_objects(self.arguments)

    def parsed_arguments(self) -> dict[str, Any]:
        items = self.parsed_argument_items()
        if items:
            return items[0]
        return {}


@dataclass
class TurnResult:
    label: str
    session_id: str
    prompt: str
    duration_seconds: float
    response_id: str
    final_usage: UsageSnapshot
    usage_events: list[UsageSnapshot]
    tool_calls: list[ToolCallRecord]
    content: str
    context_stats: list[dict[str, Any]]

    def final_answer(self) -> str:
        return extract_final_answer(self.content)


@dataclass
class AssertionResult:
    name: str
    passed: bool
    detail: str
    severity: str = "required"


@dataclass
class SuiteResult:
    name: str
    session_id: str
    turns: list[TurnResult]
    assertions: list[AssertionResult]
    recommendations: list[str] = field(default_factory=list)

    @property
    def passed(self) -> bool:
        return all(item.passed for item in self.assertions if item.severity == "required")


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="AuraMate 记忆系统全链路对话闭环验证")
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL, help="后端地址，例如 http://localhost:3000")
    parser.add_argument("--model", default=DEFAULT_MODEL, help="测试时使用的模型 ID")
    parser.add_argument(
        "--only",
        default="all",
        help="仅运行指定测试组，逗号分隔：explicit,auto,overwrite,reject,isolation,reflection",
    )
    parser.add_argument("--timeout", type=int, default=REQUEST_TIMEOUT_SECONDS, help="单轮请求超时秒数")
    parser.add_argument("--report-path", default=DEFAULT_REPORT_PATH, help="结构化报告输出路径")
    parser.add_argument("--no-report", action="store_true", help="不写入 JSON 报告")
    return parser.parse_args()


def normalize_usage(raw_usage: dict[str, Any]) -> UsageSnapshot:
    prompt_tokens = int(raw_usage.get("prompt_tokens", raw_usage.get("input_tokens", 0)) or 0)
    completion_tokens = int(raw_usage.get("completion_tokens", raw_usage.get("output_tokens", 0)) or 0)
    total_tokens = int(raw_usage.get("total_tokens", prompt_tokens + completion_tokens) or 0)
    details = raw_usage.get("prompt_tokens_details") or raw_usage.get("input_tokens_details") or {}
    cached_tokens = 0
    if isinstance(details, dict):
        cached_tokens = int(details.get("cached_tokens", 0) or 0)
    if not cached_tokens:
        cached_tokens = int(raw_usage.get("prompt_cache_hit_tokens", 0) or raw_usage.get("cached_tokens", 0) or 0)
    return UsageSnapshot(
        prompt_tokens=prompt_tokens,
        completion_tokens=completion_tokens,
        total_tokens=total_tokens,
        cached_tokens=cached_tokens,
    )


def ensure_backend_ready(base_url: str) -> None:
    response = requests.get(f"{base_url}/v1/models", timeout=10)
    response.raise_for_status()


def build_payload(prompt: str, session_id: str, model: str) -> dict[str, Any]:
    return {
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
        "stream": True,
        "temperature": 0.0,
        "session_id": session_id,
    }


def parse_json_objects(text: str) -> list[dict[str, Any]]:
    decoder = json.JSONDecoder()
    index = 0
    text = text.strip()
    items: list[dict[str, Any]] = []
    while index < len(text):
        while index < len(text) and text[index].isspace():
            index += 1
        if index >= len(text):
            break
        try:
            payload, next_index = decoder.raw_decode(text, index)
        except json.JSONDecodeError:
            return []
        if isinstance(payload, dict):
            items.append(payload)
        index = next_index
    return items


def extract_final_answer(text: str) -> str:
    stripped = text.strip()
    if not stripped:
        return ""
    if "</thinking>" not in stripped:
        return stripped
    final = stripped.rsplit("</thinking>", 1)[-1].strip()
    return final or stripped


def normalize_tool_calls(buffers: dict[int, dict[str, Any]]) -> list[ToolCallRecord]:
    records: list[ToolCallRecord] = []
    for index in sorted(buffers):
        entry = buffers[index]
        name = str(entry.get("name", "") or "").strip()
        arguments = "".join(entry.get("arguments", [])).strip()
        if not name:
            continue
        parsed_items = parse_json_objects(arguments)
        if len(parsed_items) > 1:
            for offset, payload in enumerate(parsed_items):
                payload_text = json.dumps(payload, ensure_ascii=False)
                records.append(
                    ToolCallRecord(
                        index=index + offset,
                        name=name,
                        arguments=payload_text,
                        raw={
                            "id": entry.get("id", ""),
                            "name": name,
                            "arguments": payload_text,
                        },
                    )
                )
            continue
        records.append(
            ToolCallRecord(
                index=index,
                name=name,
                arguments=arguments,
                raw={
                    "id": entry.get("id", ""),
                    "name": name,
                    "arguments": arguments,
                },
            )
        )
    return records


def run_stream_turn(
    base_url: str,
    model: str,
    session_id: str,
    label: str,
    prompt: str,
    timeout_seconds: int,
) -> TurnResult:
    url = f"{base_url}/v1/chat/completions"
    payload = build_payload(prompt=prompt, session_id=session_id, model=model)
    usage_events: list[UsageSnapshot] = []
    content_parts: list[str] = []
    context_stats: list[dict[str, Any]] = []
    response_id = ""
    tool_buffers: dict[int, dict[str, Any]] = {}
    started_at = time.time()

    response = requests.post(
        url,
        headers={"Content-Type": "application/json"},
        data=json.dumps(payload),
        stream=True,
        timeout=timeout_seconds,
    )
    response.raise_for_status()

    for raw_line in response.iter_lines():
        if not raw_line:
            continue
        line = raw_line.decode("utf-8")
        if not line.startswith("data: "):
            continue
        data_str = line[6:]
        if data_str == "[DONE]":
            break

        chunk = json.loads(data_str)
        if not response_id and chunk.get("id"):
            response_id = str(chunk.get("id"))
        if chunk.get("usage"):
            usage_events.append(normalize_usage(chunk["usage"]))
        if chunk.get("context_stats"):
            context_stats.append(chunk["context_stats"])

        choices = chunk.get("choices") or []
        if not choices:
            continue
        delta = choices[0].get("delta") or {}
        content = delta.get("content")
        if content is not None and content != "":
            content_parts.append(content)

        for tool_call in delta.get("tool_calls") or []:
            index = int(tool_call.get("index", len(tool_buffers)))
            buffer = tool_buffers.setdefault(index, {"name": "", "arguments": [], "id": ""})
            if tool_call.get("id"):
                buffer["id"] = str(tool_call.get("id"))
            function = tool_call.get("function") or {}
            if function.get("name"):
                buffer["name"] = str(function.get("name"))
            arguments = function.get("arguments", "")
            if isinstance(arguments, dict):
                arguments = json.dumps(arguments, ensure_ascii=False)
            if arguments:
                buffer["arguments"].append(str(arguments))

    final_usage = usage_events[-1] if usage_events else UsageSnapshot()
    tool_calls = normalize_tool_calls(tool_buffers)
    return TurnResult(
        label=label,
        session_id=session_id,
        prompt=prompt,
        duration_seconds=time.time() - started_at,
        response_id=response_id,
        final_usage=final_usage,
        usage_events=usage_events,
        tool_calls=tool_calls,
        content="".join(content_parts).strip(),
        context_stats=context_stats,
    )


def run_sequence(
    base_url: str,
    model: str,
    session_id: str,
    timeout_seconds: int,
    steps: list[tuple[str, str]],
) -> list[TurnResult]:
    turns: list[TurnResult] = []
    for label, prompt in steps:
        turns.append(run_stream_turn(base_url, model, session_id, label, prompt, timeout_seconds))
    return turns


def usage_presence_assertions(turns: list[TurnResult]) -> list[AssertionResult]:
    assertions: list[AssertionResult] = []
    for turn in turns:
        usage = turn.final_usage
        ok = usage.total_tokens > 0 or usage.prompt_tokens > 0 or usage.completion_tokens > 0
        assertions.append(
            AssertionResult(
                name=f"{turn.label} usage 可观测",
                passed=ok,
                detail=(
                    f"prompt={usage.prompt_tokens}, completion={usage.completion_tokens}, "
                    f"total={usage.total_tokens}, cached={usage.cached_tokens}"
                ),
            )
        )
    return assertions


def build_scenario() -> dict[str, str]:
    seed = uuid.uuid4().hex.upper()
    return {
        "seed": seed,
        "explicit_marker": f"MEM_EXPLICIT_{seed[:10]}",
        "auto_marker": f"MEM_AUTO_{seed[10:20]}",
        "overwrite_title": f"response_style_{seed[20:28].lower()}",
        "overwrite_old": f"OVERWRITE_OLD_{seed[28:36]}",
        "overwrite_new": f"OVERWRITE_NEW_{seed[4:12]}",
        "reject_title": f"response_density_{seed[12:20].lower()}",
        "reject_keep": f"REJECT_KEEP_{seed[20:28]}",
        "reject_drop": f"REJECT_DROP_{seed[28:36]}",
        "noise_marker": f"NOISE_{seed[:12]}",
        "reflection_file": f"memory_missing_{seed[8:18].lower()}.txt",
    }


def find_memory_tool_calls(turn: TurnResult, action: str | None = None) -> list[ToolCallRecord]:
    matches: list[ToolCallRecord] = []
    for call in turn.tool_calls:
        if call.name != "manage_core_memory":
            continue
        if action is None:
            matches.append(call)
            continue
        if call.parsed_arguments().get("action") == action:
            matches.append(call)
    return matches


def final_answer_contains_all(turn: TurnResult, *parts: str) -> bool:
    return contains_all(turn.final_answer(), *parts)


def final_answer_contains_any(turn: TurnResult, *parts: str) -> bool:
    lowered = turn.final_answer().lower()
    return any(part.lower() in lowered for part in parts)


def contains_all(text: str, *parts: str) -> bool:
    lowered = text.lower()
    return all(part.lower() in lowered for part in parts)


def generate_recommendations(result: SuiteResult) -> list[str]:
    recommendations: list[str] = []
    failed = [item for item in result.assertions if not item.passed]
    for item in failed:
        if result.name == "显式写入与跨会话检索":
            if "搜索" in item.name or "跨会话" in item.name:
                recommendations.append("检查 manage_core_memory 的 search/list 调用是否被模型稳定执行，以及检索结果是否被当前提示词正确引用。")
            if "add" in item.name:
                recommendations.append("检查显式记忆写入提示是否足够强，必要时约束最终响应前必须完成 manage_core_memory add。")
        elif result.name == "自动稳定记忆捕获":
            recommendations.append("检查 createUserMessage 中的 persistStableUserMemory 是否命中当前表达，并确认自动写入后 invalidateMemoryRetrievalCache 已执行。")
        elif result.name == "高信任冲突覆盖":
            recommendations.append("检查 memory policy 中 conflict key 提取是否稳定，以及 user_input 对 Reflection 的 source trust 是否保持更高优先级。")
        elif result.name == "低信任冲突拒绝":
            recommendations.append("检查 shouldOverwriteExistingMemory 与 conflict reject 分支，确保低信任来源不会覆盖用户明确偏好。")
        elif result.name == "会话隔离与无关记忆抑制":
            recommendations.append("检查记忆检索提示词是否过度主动；当用户明确禁止搜索长期记忆时，不应出现 manage_core_memory 调用或无关记忆泄漏。")
        elif result.name == "失败反思经验写回":
            recommendations.append("检查 shouldRunCompletionReflection 触发条件、8 秒反思窗口，以及反思 JSON 解析后是否真正写入 experience/fact。")
    deduped: list[str] = []
    for item in recommendations:
        if item not in deduped:
            deduped.append(item)
    return deduped


def run_explicit_roundtrip_suite(base_url: str, model: str, timeout_seconds: int, scenario: dict[str, str]) -> SuiteResult:
    session_id = f"memory-explicit-{uuid.uuid4().hex[:8]}"
    retrieval_session = f"memory-explicit-read-{uuid.uuid4().hex[:8]}"
    marker = scenario["explicit_marker"]
    steps = [
        (
            "explicit-add",
            (
                "请严格按顺序执行，不要省略步骤：\n"
                    f"1. 调用 manage_core_memory，action=add，category=instruction，title={marker.lower()}，source=user_input，"
                f"content=默认先给结论再解释，标识 {marker}。\n"
                f"2. 调用 manage_core_memory，action=search，query={marker}。\n"
                "3. 最终只回复：OK_EXPLICIT。"
            ),
        ),
    ]
    turns = run_sequence(base_url, model, session_id, timeout_seconds, steps)
    retrieval_turn = run_stream_turn(
        base_url,
        model,
        retrieval_session,
        "explicit-recall",
        (
            f"请只调用 manage_core_memory 搜索 {marker}，然后只回复搜到的那条长期记忆原文。"
            "不要编造，不要附加解释。"
        ),
        timeout_seconds,
    )
    turns.append(retrieval_turn)

    assertions = usage_presence_assertions(turns)
    add_calls = find_memory_tool_calls(turns[0], "add")
    search_calls = find_memory_tool_calls(turns[0], "search")
    recall_search_calls = find_memory_tool_calls(retrieval_turn, "search")
    assertions.extend(
        [
            AssertionResult(
                name="显式 add 已触发",
                passed=bool(add_calls),
                detail=f"tool_calls={[call.arguments for call in add_calls]}",
            ),
            AssertionResult(
                name="首轮显式 search 已触发",
                passed=bool(search_calls),
                detail=f"tool_calls={[call.arguments for call in search_calls]}",
            ),
            AssertionResult(
                name="跨会话 search 已触发",
                passed=bool(recall_search_calls),
                detail=f"tool_calls={[call.arguments for call in recall_search_calls]}",
            ),
            AssertionResult(
                name="跨会话检索到显式长期记忆",
                passed=final_answer_contains_all(retrieval_turn, marker, "先给结论再解释"),
                detail=f"response={retrieval_turn.final_answer()!r}",
            ),
        ]
    )
    result = SuiteResult(name="显式写入与跨会话检索", session_id=session_id, turns=turns, assertions=assertions)
    result.recommendations = generate_recommendations(result)
    return result


def run_auto_capture_suite(base_url: str, model: str, timeout_seconds: int, scenario: dict[str, str]) -> SuiteResult:
    session_id = f"memory-auto-{uuid.uuid4().hex[:8]}"
    retrieval_session = f"memory-auto-read-{uuid.uuid4().hex[:8]}"
    marker = scenario["auto_marker"]
    turns = run_sequence(
        base_url,
        model,
        session_id,
        timeout_seconds,
        [
            (
                "auto-capture",
                (
                    f"以后默认这样做：先给结论再解释，条目尽量短，标识 {marker}。"
                    "不要调用无关工具，最终只回复：ACK_AUTO。"
                ),
            )
        ],
    )
    retrieval_turn = run_stream_turn(
        base_url,
        model,
        retrieval_session,
        "auto-recall",
        (
            "请只调用 manage_core_memory 搜索“先给结论再解释 条目尽量短”，category=instruction，"
            "然后用一句中文复述这条长期规则。"
        ),
        timeout_seconds,
    )
    turns.append(retrieval_turn)

    first_turn = turns[0]
    add_calls = find_memory_tool_calls(first_turn, "add")
    recall_search_calls = find_memory_tool_calls(retrieval_turn, "search")
    assertions = usage_presence_assertions(turns)
    assertions.extend(
        [
            AssertionResult(
                name="自动记忆首轮响应正常",
                passed="ACK_AUTO" in first_turn.final_answer(),
                detail=f"response={first_turn.final_answer()!r}",
            ),
            AssertionResult(
                name="自动记忆可选工具写入",
                passed=True,
                detail=f"manage_core_memory add 次数={len(add_calls)}；允许为 0，因为存在 createUserMessage 的确定性持久化。",
                severity="informational",
            ),
            AssertionResult(
                name="跨会话自动记忆 search 已触发",
                passed=bool(recall_search_calls),
                detail=f"tool_calls={[call.arguments for call in recall_search_calls]}",
            ),
            AssertionResult(
                name="跨会话检索到自动捕获的长期规则",
                passed=(
                    final_answer_contains_any(retrieval_turn, "结论")
                    and final_answer_contains_any(retrieval_turn, "解释")
                    and final_answer_contains_any(retrieval_turn, "条目尽量短", "条目尽量简短", "简洁", "简短")
                ),
                detail=f"response={retrieval_turn.final_answer()!r}",
            ),
        ]
    )
    result = SuiteResult(name="自动稳定记忆捕获", session_id=session_id, turns=turns, assertions=assertions)
    result.recommendations = generate_recommendations(result)
    return result


def run_overwrite_suite(base_url: str, model: str, timeout_seconds: int, scenario: dict[str, str]) -> SuiteResult:
    session_id = f"memory-overwrite-{uuid.uuid4().hex[:8]}"
    title = scenario["overwrite_title"]
    old_marker = scenario["overwrite_old"]
    new_marker = scenario["overwrite_new"]
    turns = run_sequence(
        base_url,
        model,
        session_id,
        timeout_seconds,
        [
            (
                "overwrite-seed",
                (
                    "请严格执行：\n"
                    f"1. 调用 manage_core_memory，action=add，category=preference，title={title}，source=Reflection，"
                    f"content=回答尽量简洁，标识 {old_marker}。\n"
                    "2. 最终只回复：ACK_OVERWRITE_1。"
                ),
            ),
            (
                "overwrite-apply",
                (
                    "请严格执行：\n"
                    f"1. 调用 manage_core_memory，action=add，category=preference，title={title}，source=user_input，"
                    f"content=回答尽量详细，标识 {new_marker}。\n"
                    "2. 最终只回复：ACK_OVERWRITE_2。"
                ),
            ),
            (
                "overwrite-verify",
                (
                    f"请只调用 manage_core_memory 列出 category=preference 的记忆，然后只回复标题 {title} 对应的完整内容。"
                    "不要附加说明。"
                ),
            ),
        ],
    )
    assertions = usage_presence_assertions(turns)
    assertions.extend(
        [
            AssertionResult(
                name="高信任覆盖后保留新值",
                passed=final_answer_contains_all(turns[-1], new_marker, "回答尽量详细") and old_marker not in turns[-1].final_answer(),
                detail=f"response={turns[-1].final_answer()!r}",
            ),
            AssertionResult(
                name="覆盖轮次使用 user_input 来源",
                passed=any(call.parsed_arguments().get("source") == "user_input" for call in find_memory_tool_calls(turns[1], "add")),
                detail=f"tool_calls={[call.arguments for call in find_memory_tool_calls(turns[1], 'add')]}",
            ),
        ]
    )
    result = SuiteResult(name="高信任冲突覆盖", session_id=session_id, turns=turns, assertions=assertions)
    result.recommendations = generate_recommendations(result)
    return result


def run_reject_suite(base_url: str, model: str, timeout_seconds: int, scenario: dict[str, str]) -> SuiteResult:
    session_id = f"memory-reject-{uuid.uuid4().hex[:8]}"
    title = scenario["reject_title"]
    keep_marker = scenario["reject_keep"]
    drop_marker = scenario["reject_drop"]
    turns = run_sequence(
        base_url,
        model,
        session_id,
        timeout_seconds,
        [
            (
                "reject-seed",
                (
                    "请严格执行：\n"
                    f"1. 调用 manage_core_memory，action=add，category=preference，title={title}，source=user_input，"
                    f"content=回答尽量简洁，标识 {keep_marker}。\n"
                    "2. 最终只回复：ACK_REJECT_1。"
                ),
            ),
            (
                "reject-attempt",
                (
                    "请严格执行：\n"
                    f"1. 调用 manage_core_memory，action=add，category=preference，title={title}，source=Reflection，"
                    f"content=回答尽量详细，标识 {drop_marker}。\n"
                    "2. 最终只回复：ACK_REJECT_2。"
                ),
            ),
            (
                "reject-verify",
                (
                    f"请只调用 manage_core_memory 列出 category=preference 的记忆，然后只回复标题 {title} 对应的完整内容。"
                    "不要附加说明。"
                ),
            ),
        ],
    )
    assertions = usage_presence_assertions(turns)
    assertions.extend(
        [
            AssertionResult(
                name="低信任冲突不会覆盖原值",
                passed=final_answer_contains_all(turns[-1], keep_marker, "回答尽量简洁") and drop_marker not in turns[-1].final_answer(),
                detail=f"response={turns[-1].final_answer()!r}",
            ),
            AssertionResult(
                name="冲突尝试轮次使用 Reflection 来源",
                passed=any(call.parsed_arguments().get("source") == "Reflection" for call in find_memory_tool_calls(turns[1], "add")),
                detail=f"tool_calls={[call.arguments for call in find_memory_tool_calls(turns[1], 'add')]}",
            ),
        ]
    )
    result = SuiteResult(name="低信任冲突拒绝", session_id=session_id, turns=turns, assertions=assertions)
    result.recommendations = generate_recommendations(result)
    return result


def run_isolation_suite(base_url: str, model: str, timeout_seconds: int, scenario: dict[str, str]) -> SuiteResult:
    seed_session = f"memory-isolation-seed-{uuid.uuid4().hex[:8]}"
    probe_session = f"memory-isolation-probe-{uuid.uuid4().hex[:8]}"
    noise_marker = scenario["noise_marker"]
    turns: list[TurnResult] = []
    turns.extend(
        run_sequence(
            base_url,
            model,
            seed_session,
            timeout_seconds,
            [
                (
                    "isolation-seed",
                    (
                        "请严格执行：\n"
                        f"1. 调用 manage_core_memory，action=add，category=fact，source=user_input，content=无关隔离标识 {noise_marker}。\n"
                        "2. 最终只回复：ACK_NOISE。"
                    ),
                )
            ],
        )
    )
    probe_turn = run_stream_turn(
        base_url,
        model,
        probe_session,
        "isolation-probe",
        (
            "不要调用 manage_core_memory，也不要搜索长期记忆。"
            "请仅输出我在当前会话更早的用户消息里给过你的那个无关隔离标识；"
            "如果当前会话更早的消息里没有这个标识，输出 NONE。只回复一个词。"
        ),
        timeout_seconds,
    )
    turns.append(probe_turn)

    assertions = usage_presence_assertions(turns)
    assertions.extend(
        [
            AssertionResult(
                name="隔离探测轮不调用长期记忆工具",
                passed=not find_memory_tool_calls(probe_turn),
                detail=f"tool_calls={[call.arguments for call in find_memory_tool_calls(probe_turn)]}",
            ),
            AssertionResult(
                name="无关长期记忆不会主动泄漏",
                passed=probe_turn.final_answer().strip().upper() == "NONE",
                detail=f"response={probe_turn.final_answer()!r}",
            ),
        ]
    )
    result = SuiteResult(name="会话隔离与无关记忆抑制", session_id=probe_session, turns=turns, assertions=assertions)
    result.recommendations = generate_recommendations(result)
    return result


def run_reflection_suite(base_url: str, model: str, timeout_seconds: int, scenario: dict[str, str]) -> SuiteResult:
    session_id = f"memory-reflection-{uuid.uuid4().hex[:8]}"
    recall_session = f"memory-reflection-read-{uuid.uuid4().hex[:8]}"
    missing_path = WORKSPACE_DIR / scenario["reflection_file"]
    turns = run_sequence(
        base_url,
        model,
        session_id,
        timeout_seconds,
        [
            (
                "reflection-failure",
                (
                    f"请只使用 read 工具读取不存在的文件 {missing_path.as_posix()}。"
                    "如果失败，不要重试，不要调用其他工具，直接用一句中文说明失败原因。"
                ),
            )
        ],
    )

    time.sleep(10)

    recall_turn = run_stream_turn(
        base_url,
        model,
        recall_session,
        "reflection-recall",
        (
            f"请只调用 manage_core_memory 搜索 {missing_path.name} 或相关失败经验，然后只回复找到的那条经验；"
            "如果没有找到，回复 NONE。"
        ),
        timeout_seconds,
    )
    turns.append(recall_turn)

    failure_turn = turns[0]
    assertions = usage_presence_assertions(turns)
    assertions.extend(
        [
            AssertionResult(
                name="失败轮次产生工具错误前提",
                passed=any(call.name == "read" for call in failure_turn.tool_calls) and (
                    "失败" in failure_turn.final_answer() or "不存在" in failure_turn.final_answer() or "error" in failure_turn.final_answer().lower()
                ),
                detail=f"tool_calls={[call.name for call in failure_turn.tool_calls]}, response={failure_turn.final_answer()!r}",
            ),
            AssertionResult(
                name="反思后可检索到失败经验",
                passed=recall_turn.final_answer().strip().upper() != "NONE" and (
                    missing_path.name.lower() in recall_turn.final_answer().lower()
                    or "不存在" in recall_turn.final_answer()
                    or "文件读取失败" in recall_turn.final_answer()
                    or "read" in recall_turn.final_answer().lower()
                ),
                detail=f"response={recall_turn.final_answer()!r}",
            ),
        ]
    )
    result = SuiteResult(name="失败反思经验写回", session_id=session_id, turns=turns, assertions=assertions)
    result.recommendations = generate_recommendations(result)
    return result


def selected_suites(value: str) -> list[str]:
    if value.strip().lower() == "all":
        return ["explicit", "auto", "overwrite", "reject", "isolation", "reflection"]
    return [item.strip().lower() for item in value.split(",") if item.strip()]


def print_header(title: str) -> None:
    print(f"\n{'=' * 88}\n{title}\n{'=' * 88}")


def print_usage_table(turns: list[TurnResult]) -> None:
    print(f"{'轮次':<24}{'Prompt':>12}{'Completion':>14}{'Total':>12}{'Cached':>12}{'Duration(s)':>14}")
    print("-" * 92)
    for turn in turns:
        usage = turn.final_usage
        print(
            f"{turn.label:<24}"
            f"{usage.prompt_tokens:>12}"
            f"{usage.completion_tokens:>14}"
            f"{usage.total_tokens:>12}"
            f"{usage.cached_tokens:>12}"
            f"{turn.duration_seconds:>14.2f}"
        )


def print_turn_detail(turn: TurnResult) -> None:
    usage = turn.final_usage
    print(f"\n[{turn.label}] session={turn.session_id} response_id={turn.response_id or '-'}")
    print(
        f"usage: prompt={usage.prompt_tokens} completion={usage.completion_tokens} "
        f"total={usage.total_tokens} cached={usage.cached_tokens} duration={turn.duration_seconds:.2f}s"
    )
    if turn.tool_calls:
        print("tool_calls:")
        for call in turn.tool_calls:
            print(f"  - #{call.index:02d} {call.name} {call.arguments}")
    else:
        print("tool_calls: []")
    preview = turn.content.strip()
    if len(preview) > 240:
        preview = preview[:240] + "..."
    print(f"content: {preview!r}")
    print(f"final_answer: {turn.final_answer()!r}")


def print_suite_result(result: SuiteResult) -> None:
    print_header(f"{result.name} | Session: {result.session_id}")
    print_usage_table(result.turns)
    for turn in result.turns:
        print_turn_detail(turn)
    print("\n断言结果：")
    for assertion in result.assertions:
        status = "PASS" if assertion.passed else "FAIL"
        suffix = "" if assertion.severity == "required" else f" ({assertion.severity})"
        print(f"- [{status}] {assertion.name}{suffix} | {assertion.detail}")
    if result.recommendations:
        print("\n优化建议：")
        for item in result.recommendations:
            print(f"- {item}")


def build_result_payload(args: argparse.Namespace, suites: list[str], results: list[SuiteResult], scenario: dict[str, str]) -> dict[str, Any]:
    total_assertions = sum(len(result.assertions) for result in results)
    passed_assertions = sum(1 for result in results for assertion in result.assertions if assertion.passed)
    failed_suites = [result.name for result in results if not result.passed]
    return {
        "generated_at": time.strftime("%Y-%m-%d %H:%M:%S"),
        "base_url": args.base_url,
        "model": args.model,
        "selected_suites": suites,
        "scenario": scenario,
        "passed": not failed_suites,
        "summary": {
            "suite_count": len(results),
            "total_assertions": total_assertions,
            "passed_assertions": passed_assertions,
            "failed_suites": failed_suites,
        },
        "results": [asdict(result) for result in results],
    }


def save_result(payload: dict[str, Any], report_path: str) -> Path:
    target = Path(report_path)
    if not target.is_absolute():
        target = REPO_ROOT / target
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
    return target


def main() -> int:
    args = parse_args()
    suites = selected_suites(args.only)
    ensure_backend_ready(args.base_url)
    scenario = build_scenario()

    print_header("测试配置")
    print(f"Base URL : {args.base_url}")
    print(f"Model    : {args.model}")
    print(f"Workspace: {WORKSPACE_DIR}")
    print(f"Suites   : {suites}")

    results: list[SuiteResult] = []
    for suite in suites:
        if suite == "explicit":
            results.append(run_explicit_roundtrip_suite(args.base_url, args.model, args.timeout, scenario))
        elif suite == "auto":
            results.append(run_auto_capture_suite(args.base_url, args.model, args.timeout, scenario))
        elif suite == "overwrite":
            results.append(run_overwrite_suite(args.base_url, args.model, args.timeout, scenario))
        elif suite == "reject":
            results.append(run_reject_suite(args.base_url, args.model, args.timeout, scenario))
        elif suite == "isolation":
            results.append(run_isolation_suite(args.base_url, args.model, args.timeout, scenario))
        elif suite == "reflection":
            results.append(run_reflection_suite(args.base_url, args.model, args.timeout, scenario))
        else:
            print(f"[WARN] 未知测试组，已跳过: {suite}")

    for result in results:
        print_suite_result(result)

    total_assertions = sum(len(result.assertions) for result in results)
    passed_assertions = sum(1 for result in results for assertion in result.assertions if assertion.passed)
    failed_suites = [result.name for result in results if not result.passed]

    print_header("汇总")
    print(f"断言通过数 : {passed_assertions}/{total_assertions}")
    print(f"测试组结果 : {[(result.name, 'PASS' if result.passed else 'FAIL') for result in results]}")
    if failed_suites:
        print(f"失败测试组 : {failed_suites}")
    else:
        print("全部关键测试组通过。")

    if not args.no_report:
        payload = build_result_payload(args, suites, results, scenario)
        saved = save_result(payload, args.report_path)
        print(f"结构化报告 : {saved}")

    return 1 if failed_suites else 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except requests.RequestException as error:
        print(f"请求失败: {error}")
        sys.exit(2)
    except KeyboardInterrupt:
        print("用户中断测试。")
        sys.exit(130)