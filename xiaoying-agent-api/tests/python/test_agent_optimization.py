from __future__ import annotations

import argparse
import json
import os
import re
import statistics
import sys
import time
import uuid
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any

import requests


DEFAULT_BASE_URL = os.getenv("AURAMATE_BASE_URL", "http://localhost:3000")
DEFAULT_MODEL = os.getenv("AURAMATE_MODEL", "aura-coder")
REQUEST_TIMEOUT_SECONDS = 600
HOT_CONTEXT_THRESHOLD_CHARS = 150000
BOUNDARY_SEGMENT_LINES = 300
BOUNDARY_FILE_LINES = 3000
REPO_ROOT = Path(__file__).resolve().parents[3]
WORKSPACE_DIR = REPO_ROOT / "workspace"
REAL_LARGE_FILE = WORKSPACE_DIR / "raiden3_game.html"


@dataclass
class UsageSnapshot:
    prompt_tokens: int = 0
    completion_tokens: int = 0
    total_tokens: int = 0
    cached_tokens: int = 0


@dataclass
class ToolCallRecord:
    name: str
    arguments: str
    raw: dict[str, Any] = field(default_factory=dict)


@dataclass
class ReadOperation:
    file_path: str
    offset: int
    limit: int
    index: int
    raw_arguments: str

    @property
    def start_line(self) -> int:
        return self.offset

    @property
    def end_line(self) -> int:
        if self.limit <= 0:
            return self.offset
        return self.offset + self.limit - 1

    @property
    def key(self) -> tuple[str, int, int]:
        return self.file_path, self.offset, self.limit


@dataclass
class TurnResult:
    label: str
    prompt: str
    session_id: str
    duration_seconds: float
    response_id: str
    final_usage: UsageSnapshot
    usage_events: list[UsageSnapshot]
    tool_calls: list[ToolCallRecord]
    content: str
    timeline: list[dict[str, Any]]
    context_stats: list[dict[str, Any]]


@dataclass
class AssertionResult:
    name: str
    passed: bool
    detail: str


@dataclass
class SuiteResult:
    name: str
    session_id: str
    turns: list[TurnResult]
    assertions: list[AssertionResult]

    @property
    def passed(self) -> bool:
        return all(item.passed for item in self.assertions)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="AuraMate Agent 优化自动化验证脚本")
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL, help="后端地址，例如 http://localhost:3000")
    parser.add_argument("--model", default=DEFAULT_MODEL, help="测试时使用的模型 ID")
    parser.add_argument(
        "--only",
        default="all",
        help="仅运行指定测试组，使用逗号分隔：boundary_reach,boundary_exceed,overlap,silence",
    )
    parser.add_argument("--timeout", type=int, default=REQUEST_TIMEOUT_SECONDS, help="单轮请求超时秒数")
    parser.add_argument("--no-cleanup", action="store_true", help="保留脚本生成的临时文件")
    parser.add_argument("--report-path", default="", help="可选：将本次结果写入 JSON 文件")
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


def run_stream_turn(
    base_url: str,
    model: str,
    session_id: str,
    label: str,
    prompt: str,
    timeout_seconds: int,
) -> TurnResult:
    headers = {"Content-Type": "application/json"}
    url = f"{base_url}/v1/chat/completions"
    payload = build_payload(prompt=prompt, session_id=session_id, model=model)
    usage_events: list[UsageSnapshot] = []
    tool_calls: list[ToolCallRecord] = []
    content_parts: list[str] = []
    timeline: list[dict[str, Any]] = []
    context_stats: list[dict[str, Any]] = []
    response_id = ""
    started_at = time.time()

    response = requests.post(url, headers=headers, data=json.dumps(payload), stream=True, timeout=timeout_seconds)
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
            timeline.append({"type": "content", "text": content})
            # print(f"DEBUG content: {content!r}") # 可以解开注释看实时输出
        for tool_call in delta.get("tool_calls") or []:
            function = tool_call.get("function") or {}
            name = str(function.get("name", "") or "")
            arguments = function.get("arguments", "")
            if not name:
                continue
            if isinstance(arguments, dict):
                arguments = json.dumps(arguments, ensure_ascii=False)
            arguments = str(arguments or "")
            tool_calls.append(ToolCallRecord(name=name, arguments=arguments, raw=tool_call))
            timeline.append({"type": "tool", "name": name, "arguments": arguments})

    final_usage = usage_events[-1] if usage_events else UsageSnapshot()
    normalized_tool_calls = compact_tool_calls(tool_calls)
    return TurnResult(
        label=label,
        prompt=prompt,
        session_id=session_id,
        duration_seconds=time.time() - started_at,
        response_id=response_id,
        final_usage=final_usage,
        usage_events=usage_events,
        tool_calls=normalized_tool_calls,
        content="".join(content_parts),
        timeline=timeline,
        context_stats=context_stats,
    )


def create_multiline_boundary_file(path: Path, line_count: int = BOUNDARY_FILE_LINES) -> int:
    WORKSPACE_DIR.mkdir(parents=True, exist_ok=True)
    lines: list[str] = []
    total_chars = 0
    seed = "用于验证 Agent 热区折叠、冷区裁剪、读取顺序与静默执行行为。"
    for index in range(1, line_count + 1):
        target_length = 120 + (index % 81)
        prefix = f"LINE {index:04d} | BLOCK {index % 12:02d} | "
        text = prefix
        while len(text) < target_length:
            text += seed
        text = text[:target_length]
        lines.append(text)
        total_chars += len(text)
    path.write_text("\n".join(lines), encoding="utf-8")
    return total_chars


def build_boundary_prompt(file_path: Path, start_line: int, end_line: int, turn_index: int) -> str:
    limit = end_line - start_line + 1
    return (
        f"请只使用 read 工具读取文件 {file_path.as_posix()} 的第 {start_line} 行到第 {end_line} 行。"
        f"本轮必须显式传入 file_path、offset={start_line}、limit={limit}。"
        "不要读取其他文件，不要修改文件，不要输出正文内容。"
        f"最终只回复：第{turn_index}段已读取。"
    )


def print_header(title: str) -> None:
    print(f"\n{'=' * 80}\n{title}\n{'=' * 80}")


def print_usage_table(turns: list[TurnResult]) -> None:
    print(f"{'轮次':<18}{'Prompt':>12}{'Completion':>14}{'Total':>12}{'Cached':>12}{'Duration(s)':>14}")
    print("-" * 82)
    for turn in turns:
        usage = turn.final_usage
        print(
            f"{turn.label:<18}"
            f"{usage.prompt_tokens:>12}"
            f"{usage.completion_tokens:>14}"
            f"{usage.total_tokens:>12}"
            f"{usage.cached_tokens:>12}"
            f"{turn.duration_seconds:>14.2f}"
        )


def prompt_token_growths(turns: list[TurnResult]) -> list[int]:
    growths: list[int] = []
    for previous, current in zip(turns, turns[1:]):
        growths.append(current.final_usage.prompt_tokens - previous.final_usage.prompt_tokens)
    return growths


def latest_effective_context_tokens(turn: TurnResult) -> int:
    for stats in reversed(turn.context_stats):
        value = stats.get("current_tokens")
        if isinstance(value, int) and value > 0:
            return value
    return turn.final_usage.prompt_tokens


def effective_context_growths(turns: list[TurnResult]) -> list[int]:
    growths: list[int] = []
    for previous, current in zip(turns, turns[1:]):
        growths.append(latest_effective_context_tokens(current) - latest_effective_context_tokens(previous))
    return growths


def usage_presence_assertions(turns: list[TurnResult]) -> list[AssertionResult]:
    results: list[AssertionResult] = []
    for turn in turns:
        usage = turn.final_usage
        present = usage.total_tokens > 0 or usage.prompt_tokens > 0 or usage.completion_tokens > 0
        detail = (
            f"{turn.label} final usage="
            f"(prompt={usage.prompt_tokens}, completion={usage.completion_tokens}, total={usage.total_tokens}, cached={usage.cached_tokens})"
        )
        results.append(AssertionResult(name=f"{turn.label} final usage", passed=present, detail=detail))
    return results


def run_boundary_reach_suite(base_url: str, model: str, timeout_seconds: int, file_path: Path) -> SuiteResult:
    session_id = f"agent-opt-reach-{uuid.uuid4().hex[:8]}"
    turns: list[TurnResult] = []
    for turn_index in range(3):
        start_line = turn_index * BOUNDARY_SEGMENT_LINES + 1
        end_line = start_line + BOUNDARY_SEGMENT_LINES - 1
        prompt = build_boundary_prompt(file_path=file_path, start_line=start_line, end_line=end_line, turn_index=turn_index + 1)
        turns.append(
            run_stream_turn(
                base_url=base_url,
                model=model,
                session_id=session_id,
                label=f"reach-turn-{turn_index + 1}",
                prompt=prompt,
                timeout_seconds=timeout_seconds,
            )
        )
    growths = effective_context_growths(turns)
    baseline = growths[0] if growths else 0
    last_growth = growths[-1] if growths else 0
    no_early_fold = bool(growths) and last_growth >= max(int(baseline * 0.65), 1)
    assertions = usage_presence_assertions(turns)
    assertions.append(
        AssertionResult(
            name="达到边界不应过早折叠",
            passed=no_early_fold,
            detail=f"effective_context_tokens 增量={growths}，要求最后一段增量不低于首段增量的 65%。",
        )
    )
    return SuiteResult(name="达到边界", session_id=session_id, turns=turns, assertions=assertions)


def run_boundary_exceed_suite(base_url: str, model: str, timeout_seconds: int, file_path: Path) -> SuiteResult:
    session_id = f"agent-opt-exceed-{uuid.uuid4().hex[:8]}"
    turns: list[TurnResult] = []
    for turn_index in range(5):
        start_line = turn_index * BOUNDARY_SEGMENT_LINES + 1
        end_line = start_line + BOUNDARY_SEGMENT_LINES - 1
        prompt = build_boundary_prompt(file_path=file_path, start_line=start_line, end_line=end_line, turn_index=turn_index + 1)
        turns.append(
            run_stream_turn(
                base_url=base_url,
                model=model,
                session_id=session_id,
                label=f"exceed-turn-{turn_index + 1}",
                prompt=prompt,
                timeout_seconds=timeout_seconds,
            )
        )
    growths = effective_context_growths(turns)
    baseline_growths = growths[:2]
    post_boundary_growths = growths[2:]
    baseline = statistics.mean(baseline_growths) if baseline_growths else 0
    post = statistics.mean(post_boundary_growths) if post_boundary_growths else 0
    fold_started = bool(post_boundary_growths) and post <= baseline * 0.80
    assertions = usage_presence_assertions(turns)
    assertions.append(
        AssertionResult(
            name="超过边界必须开始折叠",
            passed=fold_started,
            detail=f"effective_context_tokens 前段增量={baseline_growths}，越界后增量={post_boundary_growths}，要求越界后均值不高于前段均值的 80%。",
        )
    )
    return SuiteResult(name="超过边界", session_id=session_id, turns=turns, assertions=assertions)


def safe_json_object(text: str) -> dict[str, Any]:
    if not text:
        return {}
    try:
        data = json.loads(text)
        if isinstance(data, dict):
            return data
    except json.JSONDecodeError:
        return {}
    return {}


def normalize_arguments(arguments: str) -> str:
    text = arguments.strip()
    if not text:
        return ""
    try:
        value = json.loads(text)
    except json.JSONDecodeError:
        return " ".join(text.split())
    return json.dumps(value, ensure_ascii=False, sort_keys=True)


def compact_tool_calls(tool_calls: list[ToolCallRecord]) -> list[ToolCallRecord]:
    compacted: list[ToolCallRecord] = []
    for index, call in enumerate(tool_calls):
        normalized_arguments = normalize_arguments(call.arguments)
        if not normalized_arguments:
            has_later_explicit_call = any(
                later.name == call.name and normalize_arguments(later.arguments)
                for later in tool_calls[index + 1:]
            )
            if has_later_explicit_call:
                continue
        normalized_key = (call.name, normalized_arguments)
        if compacted:
            previous_key = (compacted[-1].name, normalize_arguments(compacted[-1].arguments))
            if normalized_key == previous_key:
                continue
        compacted.append(
            ToolCallRecord(
                name=call.name,
                arguments=call.arguments.strip(),
                raw=call.raw,
            )
        )
    return compacted


def extract_read_operations(tool_calls: list[ToolCallRecord]) -> list[ReadOperation]:
    operations: list[ReadOperation] = []
    for index, call in enumerate(tool_calls):
        if call.name != "read":
            continue
        payload = safe_json_object(call.arguments)
        file_path = str(payload.get("file_path") or payload.get("path") or payload.get("file_name") or "").strip()
        offset = int(payload.get("offset", 1) or 1)
        limit = int(payload.get("limit", 0) or 0)
        operations.append(
            ReadOperation(
                file_path=file_path.replace("\\", "/"),
                offset=offset,
                limit=limit,
                index=index,
                raw_arguments=call.arguments,
            )
        )
    return operations


def overlap_ratio(left: ReadOperation, right: ReadOperation) -> float:
    if left.file_path != right.file_path or left.limit <= 0 or right.limit <= 0:
        return 0.0
    start = max(left.start_line, right.start_line)
    end = min(left.end_line, right.end_line)
    if end < start:
        return 0.0
    intersection = end - start + 1
    minimum = min(left.limit, right.limit)
    if minimum <= 0:
        return 0.0
    return intersection / minimum


def analyze_read_sequence(reads: list[ReadOperation]) -> tuple[list[str], list[str], list[str]]:
    exact_duplicates: list[str] = []
    strong_overlaps: list[str] = []
    order_anomalies: list[str] = []
    for previous, current in zip(reads, reads[1:]):
        if current.key == previous.key:
            exact_duplicates.append(
                f"#{previous.index}->{current.index} {current.file_path} offset={current.offset} limit={current.limit}"
            )
        ratio = overlap_ratio(previous, current)
        if ratio >= 0.8 and current.key != previous.key:
            strong_overlaps.append(
                f"#{previous.index}->{current.index} {current.file_path} [{previous.start_line},{previous.end_line}] vs [{current.start_line},{current.end_line}] overlap={ratio:.2%}"
            )
        if (
            current.file_path == previous.file_path
            and current.offset < previous.offset
            and current.key != previous.key
        ):
            order_anomalies.append(
                f"#{previous.index}->{current.index} {current.file_path} offset {previous.offset} -> {current.offset}"
            )
    return exact_duplicates, strong_overlaps, order_anomalies


def run_overlap_suite(base_url: str, model: str, timeout_seconds: int) -> SuiteResult:
    session_id = f"agent-opt-overlap-{uuid.uuid4().hex[:8]}"
    prompt = (
        f"请只使用 read 工具研究这个大文件：{REAL_LARGE_FILE.as_posix()}。"
        "目标是定位玩家子弹碰撞逻辑。不要使用 grep，不要修改文件。"
        "如果没找到，请只把 offset 向后移动 20 到 40 行继续读取附近区域，不要大跳。"
        "如果你意识到自己在重复或高度重叠地读取，请立刻停下并直接说明。"
        "最终只给出结论。"
    )
    turn = run_stream_turn(
        base_url=base_url,
        model=model,
        session_id=session_id,
        label="overlap-turn-1",
        prompt=prompt,
        timeout_seconds=timeout_seconds,
    )
    reads = extract_read_operations(turn.tool_calls)
    exact_duplicates, strong_overlaps, order_anomalies = analyze_read_sequence(reads)
    graceful_halt_texts = ["高度重叠区间", "先停下来", "避免继续空耗上下文", "read loop", "重复读取"]
    halted_gracefully = any(text in turn.content for text in graceful_halt_texts)
    assertions = usage_presence_assertions([turn])
    assertions.append(
        AssertionResult(
            name="读取次数不超过 8 次",
            passed=len(reads) <= 8,
            detail=f"read 调用次数={len(reads)}",
        )
    )
    assertions.append(
        AssertionResult(
            name="不出现完全重复读取",
            passed=not exact_duplicates,
            detail="；".join(exact_duplicates) if exact_duplicates else "未发现完全重复读取。",
        )
    )
    overlap_passed = not strong_overlaps or halted_gracefully
    assertions.append(
        AssertionResult(
            name="高度重叠读取可被识别并优雅停机",
            passed=overlap_passed,
            detail=(
                f"高度重叠={strong_overlaps if strong_overlaps else '无'}，"
                f"优雅停机={'是' if halted_gracefully else '否'}。"
            ),
        )
    )
    assertions.append(
        AssertionResult(
            name="读取顺序不反向抖动",
            passed=not order_anomalies,
            detail="；".join(order_anomalies) if order_anomalies else "未发现 offset 回退。",
        )
    )
    return SuiteResult(name="重叠区间", session_id=session_id, turns=[turn], assertions=assertions)


def extract_content_between_tools(timeline: list[dict[str, Any]]) -> str:
    first_tool_index = next((index for index, item in enumerate(timeline) if item["type"] == "tool"), None)
    last_tool_index = next((index for index in range(len(timeline) - 1, -1, -1) if timeline[index]["type"] == "tool"), None)
    if first_tool_index is None or last_tool_index is None or first_tool_index >= last_tool_index:
        return ""
    parts: list[str] = []
    for item in timeline[first_tool_index + 1:last_tool_index]:
        if item["type"] == "content":
            parts.append(item["text"])
    return strip_non_narrative_content("".join(parts))


def extract_content_after_last_tool(timeline: list[dict[str, Any]]) -> str:
    last_tool_index = next((index for index in range(len(timeline) - 1, -1, -1) if timeline[index]["type"] == "tool"), None)
    if last_tool_index is None:
        return ""
    parts: list[str] = []
    for item in timeline[last_tool_index + 1:]:
        if item["type"] == "content":
            parts.append(item["text"])
    res = strip_non_narrative_content("".join(parts))
    # print(f"DEBUG extract_content_after_last_tool: parts={parts!r}, res={res!r}, timeline={timeline}")
    return res


def strip_non_narrative_content(text: str) -> str:
    without_tool_output = re.sub(r"\[Tool Output:[\s\S]*?(?=<thinking>|$)", "", text, flags=re.S)
    without_thinking = re.sub(r"<thinking>[\s\S]*?</thinking>", "", without_tool_output, flags=re.S)
    cleaned = without_thinking.replace("<result>", "").replace("</result>", "")
    return cleaned.strip()


def run_silence_suite(base_url: str, model: str, timeout_seconds: int) -> SuiteResult:
    session_id = f"agent-opt-silence-{uuid.uuid4().hex[:8]}"
    loop_file = WORKSPACE_DIR / "agent_validation_loop.py"
    prompt = (
        f"请在 {loop_file.as_posix()} 创建一个 Python 文件，内容是死循环。"
        f"然后只使用 1 次 run_command 工具执行 `python {loop_file.name}`（设置 cwd 为 `{WORKSPACE_DIR.as_posix()}`，blocking 为 false）。"
        "不管它是否卡住，立刻调用 stop_command 停掉它。"
        "要求：在工具调用之间绝对不要输出任何过渡说明！在所有的工具都执行完之后，你必须用一句中文进行最终总结。"
    )
    turn = run_stream_turn(
        base_url=base_url,
        model=model,
        session_id=session_id,
        label="silence-turn-1",
        prompt=prompt,
        timeout_seconds=timeout_seconds,
    )
    tool_names = [call.name for call in turn.tool_calls]
    creation_tools = {"write", "edit", "patch", "batch_write"}
    between_tools = extract_content_between_tools(turn.timeline)
    after_last_tool = extract_content_after_last_tool(turn.timeline)
    
    assertions = usage_presence_assertions([turn])
    assertions.append(
        AssertionResult(
            name="包含文件创建工具",
            passed=any(name in creation_tools for name in tool_names),
            detail=f"工具序列={tool_names}",
        )
    )
    assertions.append(
        AssertionResult(
            name="包含 run_command 与 stop_command",
            passed="run_command" in tool_names and "stop_command" in tool_names,
            detail=f"工具序列={tool_names}",
        )
    )
    assertions.append(
        AssertionResult(
            name="工具调用间保持静默",
            passed=between_tools.strip() == "",
            detail=f"工具调用间正文={between_tools!r}",
        )
    )
    assertions.append(
        AssertionResult(
            name="最后统一输出总结",
            passed=len(after_last_tool.strip()) >= 5 and any(word in after_last_tool for word in ["已", "完成", "成功", "停止", "结束", "任务已执行完毕"]),
            detail=f"最后输出={after_last_tool.strip()!r}",
        )
    )
    return SuiteResult(name="静默执行", session_id=session_id, turns=[turn], assertions=assertions)


def run_coding_suite(base_url: str, model: str, timeout_seconds: int) -> SuiteResult:
    session_id = f"agent-opt-coding-{uuid.uuid4().hex[:8]}"
    
    # 复杂的重构与新功能任务：
    # 1. 从巨型 HTML 中把 CSS 提取到独立的 style.css
    # 2. 从 HTML 中把 JS (至少包含 initStars 和 drawStars 函数) 提取到独立的 stars.js
    # 3. 在 HTML 中引入这两个文件
    # 4. 在 JS 提取过程中，增加一个 `const STAR_COLOR = "rgba(0, 255, 255, 0.8)";`，并修改 drawStars 让星星变成青色。
    
    prompt = (
        f"这是一项针对巨型单文件 {REAL_LARGE_FILE.as_posix()} 的重构任务，要求你分多步完成：\n"
        f"1. **分离 CSS**：读取 HTML，将其中的 `<style>...</style>` 内部的所有内容提取出来，写入到同目录下新建的 `style.css` 文件中。然后在 HTML 中将原本的 `<style>` 块替换为 `<link rel=\"stylesheet\" href=\"style.css\">`。\n"
        f"2. **分离部分 JS**：读取 HTML 中的 `<script>` 块，找到 `initStars` 和 `drawStars` 这两个函数的完整定义。将它们提取出来，写入到同目录下新建的 `stars.js` 文件中。然后在 HTML 中原本这两个函数的位置将它们删除，并在 `<head>` 中增加 `<script src=\"stars.js\"></script>`。\n"
        f"3. **修改逻辑**：在新建的 `stars.js` 中，增加一行全局常量 `const STAR_COLOR = \"rgba(0, 255, 255, 0.8)\";`，并修改 `drawStars` 函数，让它使用这个颜色来绘制星星（替换原来的 rgba 随机白色）。\n"
        "由于文件极其巨大（数万行），请先使用 grep 或 read 结合 offset 仔细定位目标代码。提取和删除时必须极其精确。不要破坏其他游戏逻辑。\n"
        "任务完全完成后，请用一句中文总结你做了什么。"
    )
    
    turn = run_stream_turn(
        base_url=base_url,
        model=model,
        session_id=session_id,
        label="coding-complex-turn-1",
        prompt=prompt,
        timeout_seconds=timeout_seconds,
    )
    
    tool_names = [call.name for call in turn.tool_calls]
    assertions = usage_presence_assertions([turn])
    
    # Verify CSS extraction
    css_file = WORKSPACE_DIR / "style.css"
    css_created = css_file.exists() and "background: #000;" in css_file.read_text(encoding="utf-8")
    
    # Verify JS extraction & modification
    js_file = WORKSPACE_DIR / "stars.js"
    js_created = False
    js_modified = False
    if js_file.exists():
        js_content = js_file.read_text(encoding="utf-8")
        js_created = "function drawStars()" in js_content
        js_modified = "STAR_COLOR" in js_content and "rgba(0, 255, 255" in js_content
        
    # Verify HTML modifications
    html_content = REAL_LARGE_FILE.read_text(encoding="utf-8") if REAL_LARGE_FILE.exists() else ""
    html_linked_css = "href=\"style.css\"" in html_content or "href='style.css'" in html_content
    html_linked_js = "src=\"stars.js\"" in html_content or "src='stars.js'" in html_content
    html_removed_old_style = "<style>" not in html_content
    
    assertions.append(
        AssertionResult(
            name="调用了修改与新建工具",
            passed=any(name in {"write", "edit", "search_replace", "batch_write"} for name in tool_names),
            detail=f"工具序列={tool_names}",
        )
    )
    assertions.append(
        AssertionResult(
            name="成功提取 CSS 文件",
            passed=css_created and html_linked_css,
            detail=f"CSS 文件存在={css_created}, HTML 引入={html_linked_css}",
        )
    )
    assertions.append(
        AssertionResult(
            name="成功提取并修改 JS 文件",
            passed=js_created and js_modified and html_linked_js,
            detail=f"JS 函数存在={js_created}, 常量修改={js_modified}, HTML 引入={html_linked_js}",
        )
    )
    
    # Cleanup (Optional)
    # if css_file.exists(): css_file.unlink()
    # if js_file.exists(): js_file.unlink()
        
    return SuiteResult(name="复杂重构编码", session_id=session_id, turns=[turn], assertions=assertions)


def print_turn_detail(turn: TurnResult) -> None:
    usage = turn.final_usage
    print(f"\n[{turn.label}] session={turn.session_id} response_id={turn.response_id or '-'}")
    print(
        f"usage: prompt={usage.prompt_tokens} completion={usage.completion_tokens} "
        f"total={usage.total_tokens} cached={usage.cached_tokens} duration={turn.duration_seconds:.2f}s"
    )
    if turn.tool_calls:
        print("tool_calls:")
        for index, tool_call in enumerate(turn.tool_calls, start=1):
            print(f"  {index:02d}. {tool_call.name} {tool_call.arguments}")
    else:
        print("tool_calls: []")
    content_preview = turn.content.strip()
    if len(content_preview) > 240:
        content_preview = f"{content_preview[:240]}..."
    print(f"content: {content_preview!r}")


def print_suite_result(result: SuiteResult) -> None:
    print_header(f"{result.name} | Session: {result.session_id}")
    print_usage_table(result.turns)
    for turn in result.turns:
        print_turn_detail(turn)
    print("\n断言结果：")
    for assertion in result.assertions:
        status = "PASS" if assertion.passed else "FAIL"
        print(f"- [{status}] {assertion.name} | {assertion.detail}")


def selected_suites(value: str) -> set[str]:
    if value.strip().lower() == "all":
        return {"boundary_reach", "boundary_exceed", "overlap", "silence", "coding"}
    return {item.strip().lower() for item in value.split(",") if item.strip()}


def cleanup_files(paths: list[Path]) -> None:
    for path in paths:
        if path.exists():
            path.unlink()


def build_result_payload(args: argparse.Namespace, suites: set[str], results: list[SuiteResult]) -> dict[str, Any]:
    total_assertions = sum(len(result.assertions) for result in results)
    passed_assertions = sum(1 for result in results for assertion in result.assertions if assertion.passed)
    failed_suites = [result.name for result in results if not result.passed]
    return {
        "generated_at": time.strftime("%Y-%m-%d %H:%M:%S"),
        "base_url": args.base_url,
        "model": args.model,
        "selected_suites": sorted(suites),
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

    boundary_file = WORKSPACE_DIR / "agent_boundary_multiline.txt"
    boundary_chars = create_multiline_boundary_file(boundary_file)
    expected_reach_chars = BOUNDARY_SEGMENT_LINES * 3 * 160
    expected_exceed_chars = BOUNDARY_SEGMENT_LINES * 5 * 160

    print_header("测试配置")
    print(f"Base URL     : {args.base_url}")
    print(f"Model        : {args.model}")
    print(f"Workspace    : {WORKSPACE_DIR}")
    print(f"真实大文件    : {REAL_LARGE_FILE}")
    print(f"边界文件      : {boundary_file}")
    print(f"边界文件字符数 : {boundary_chars}")
    print(f"热区阈值       : {HOT_CONTEXT_THRESHOLD_CHARS}")
    print(f"达到边界组预计注入字符数 : 约 {expected_reach_chars}")
    print(f"超过边界组预计注入字符数 : 约 {expected_exceed_chars}")

    results: list[SuiteResult] = []

    if "boundary_reach" in suites:
        results.append(run_boundary_reach_suite(args.base_url, args.model, args.timeout, boundary_file))
    if "boundary_exceed" in suites:
        results.append(run_boundary_exceed_suite(args.base_url, args.model, args.timeout, boundary_file))
    if "overlap" in suites:
        results.append(run_overlap_suite(args.base_url, args.model, args.timeout))
    if "silence" in suites:
        results.append(run_silence_suite(args.base_url, args.model, args.timeout))
    if "coding" in suites:
        results.append(run_coding_suite(args.base_url, args.model, args.timeout))

    for result in results:
        print_suite_result(result)

    total_assertions = sum(len(result.assertions) for result in results)
    passed_assertions = sum(1 for result in results for assertion in result.assertions if assertion.passed)
    failed_suites = [result.name for result in results if not result.passed]

    print_header("汇总")
    print(f"断言通过数 : {passed_assertions}/{total_assertions}")
    print(f"测试组结果 : {[(result.name, 'PASS' if result.passed else 'FAIL') for result in results]}")

    if args.report_path:
        payload = build_result_payload(args, suites, results)
        saved = save_result(payload, args.report_path)
        print(f"结构化报告 : {saved}")

    if not args.no_cleanup:
        cleanup_files([boundary_file])

    if failed_suites:
        print(f"失败测试组 : {failed_suites}")
        return 1
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except requests.RequestException as error:
        print(f"请求失败: {error}")
        sys.exit(2)
    except KeyboardInterrupt:
        print("用户中断测试。")
        sys.exit(130)
