from __future__ import annotations

import argparse
import asyncio
import json
import time
import uuid
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any
from urllib.request import urlopen

import websockets


DEFAULT_BASE_URL = "http://localhost:3000"
DEFAULT_MODEL = "aura-coder"
DEFAULT_MODE = "coding"
DEFAULT_TIMEOUT_SECONDS = 240
REPORTS_DIR = Path(__file__).resolve().parent / "reports"
SCENARIO_SESSION_CONTINUITY = "session_continuity"
SCENARIO_AUTONOMOUS_LIFECYCLE = "autonomous_lifecycle"


@dataclass
class ToolCallRecord:
    name: str
    input: str
    tool_execution_id: str = ""


@dataclass
class ToolResultRecord:
    name: str
    content: str
    is_error: bool
    tool_execution_id: str = ""


@dataclass
class TurnReport:
    label: str
    prompt: str
    session_id: str = ""
    final_text: str = ""
    tool_calls: list[ToolCallRecord] = field(default_factory=list)
    tool_results: list[ToolResultRecord] = field(default_factory=list)
    done_received: bool = False
    error_event: str = ""
    duration_seconds: float = 0.0


@dataclass
class EvaluationReport:
    scenario: str
    plan_filename: str
    session_id: str = ""
    ws_url: str = ""
    model: str = ""
    mode: str = ""
    passed: bool = False
    session_reused: bool = False
    duration_seconds: float = 0.0
    failure_reasons: list[str] = field(default_factory=list)
    diagnostics: list[str] = field(default_factory=list)
    turn_reports: list[TurnReport] = field(default_factory=list)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="验证 manage_plan 的 session continuity、id-first 更新与自然生命周期闭环")
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL, help="后端地址，例如 http://localhost:3000")
    parser.add_argument("--model", default=DEFAULT_MODEL, help="评估使用的模型")
    parser.add_argument("--mode", default=DEFAULT_MODE, help="会话模式，默认 coding")
    parser.add_argument(
        "--scenario",
        choices=(SCENARIO_SESSION_CONTINUITY, SCENARIO_AUTONOMOUS_LIFECYCLE),
        default=SCENARIO_SESSION_CONTINUITY,
        help="评测场景：显式续接或自然生命周期",
    )
    parser.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT_SECONDS, help="单轮超时时间（秒）")
    parser.add_argument("--report-path", default="", help="可选：指定报告输出路径")
    return parser.parse_args()


def ensure_backend_ready(base_url: str) -> None:
    with urlopen(f"{base_url}/v1/models", timeout=10) as response:
        if response.status != 200:
            raise RuntimeError(f"后端未就绪，状态码: {response.status}")


def build_ws_url(base_url: str) -> str:
    if base_url.startswith("https://"):
        return "wss://" + base_url[len("https://") :] + "/v1/ws"
    if base_url.startswith("http://"):
        return "ws://" + base_url[len("http://") :] + "/v1/ws"
    return base_url.rstrip("/") + "/v1/ws"


def make_report_path(custom_path: str, scenario: str) -> Path:
    if custom_path:
        return Path(custom_path)
    REPORTS_DIR.mkdir(parents=True, exist_ok=True)
    timestamp = time.strftime("%Y%m%d_%H%M%S")
    if scenario == SCENARIO_AUTONOMOUS_LIFECYCLE:
        return REPORTS_DIR / f"manage_plan_autonomous_lifecycle_real_{timestamp}.json"
    return REPORTS_DIR / f"manage_plan_session_real_{timestamp}.json"


def parse_tool_input(raw: str) -> dict[str, Any]:
    try:
        return json.loads(raw) if raw.strip() else {}
    except json.JSONDecodeError:
        return {}


def normalize_tool_calls(calls: list[ToolCallRecord]) -> list[ToolCallRecord]:
    normalized: list[ToolCallRecord] = []
    by_execution_id: dict[str, int] = {}
    for call in calls:
        key = call.tool_execution_id or f"{call.name}:{len(normalized)}"
        existing_index = by_execution_id.get(key)
        if existing_index is None:
            normalized.append(call)
            by_execution_id[key] = len(normalized) - 1
            continue
        existing = normalized[existing_index]
        if not existing.input.strip() and call.input.strip():
            normalized[existing_index] = call
    return normalized


def classify_empty_manage_plan_calls(turn: TurnReport) -> tuple[list[ToolCallRecord], list[ToolCallRecord]]:
    real_empty_calls: list[ToolCallRecord] = []
    aborted_shell_calls: list[ToolCallRecord] = []
    tool_results_by_execution_id = {
        result.tool_execution_id
        for result in turn.tool_results
        if result.name == "manage_plan" and result.tool_execution_id
    }

    for call in normalize_tool_calls(turn.tool_calls):
        if call.name != "manage_plan" or call.input.strip():
            continue
        if call.tool_execution_id and call.tool_execution_id not in tool_results_by_execution_id:
            aborted_shell_calls.append(call)
            continue
        real_empty_calls.append(call)

    return real_empty_calls, aborted_shell_calls


def initial_prompt(plan_filename: str) -> str:
    return (
        "这是一个复杂任务，但这一轮只建立计划；允许修改这份计划文件，但不要修改业务代码或其他项目文件。"
        f"请使用 manage_plan 在名为 {plan_filename}.md 的计划中创建 3 个任务：梳理现状、实现修复、验证回归。"
        "创建完成后可以调用 list 确认，但不要创建默认 TODO。"
        "最后只用一句话说明计划已建立。"
    )


def follow_up_prompt() -> str:
    return (
        "继续沿用刚才的计划上下文，不要重复 filename，不要创建默认 TODO。"
        "先调用 manage_plan(action='list') 获取条目 id，再把“实现修复”那一项更新为 in_progress。"
        "更新时必须使用 id，不要使用 index。最后再列出当前计划。"
    )


def second_follow_up_prompt() -> str:
    return (
        "继续沿用当前计划上下文，不要重复 filename，不要创建默认 TODO。"
        "把“梳理现状”那一项标记为 completed，并说明是否发生了自动归档。"
        "更新时必须使用 id，不要使用 index，最后给出剩余任务。"
    )


def final_follow_up_prompt() -> str:
    return (
        "继续沿用当前计划上下文，不要重复 filename，不要创建默认 TODO。"
        "把剩余两项都完成；如果已经没有活动项，再归档当前计划。"
        "更新和完成都必须使用 id，不要使用 index。最后说明计划是否已经收尾。"
    )


def autonomous_initial_prompt() -> str:
    return (
        "请全面审查当前仓库里的计划管理链路，至少覆盖四层："
        "后端工具能力、提示注入、会话续接、前端计划进度展示。"
        "不要先问我问题，直接完成审查，并说明当前最薄弱的一处闭环问题。"
    )


def autonomous_follow_up_prompt() -> str:
    return "继续，把刚才的审查收束成最终结论，说明问题根因和最值得优先修的点。"


async def run_turn(args: argparse.Namespace, prompt: str, label: str, session_id: str = "") -> TurnReport:
    ws_url = build_ws_url(args.base_url)
    started_at = time.time()
    turn = TurnReport(label=label, prompt=prompt, session_id=session_id)
    payload: dict[str, Any] = {"type": "chat", "content": prompt, "model": args.model, "mode": args.mode}
    if session_id:
        payload["session_id"] = session_id

    async with websockets.connect(ws_url, max_size=8 * 1024 * 1024) as websocket:
        await websocket.send(json.dumps(payload, ensure_ascii=False))
        deadline = time.time() + args.timeout
        while time.time() < deadline:
            timeout_left = max(0.1, deadline - time.time())
            try:
                raw = await asyncio.wait_for(websocket.recv(), timeout=min(2.0, timeout_left))
            except asyncio.TimeoutError:
                continue

            event = json.loads(raw)
            event_type = event.get("type", "")
            event_session_id = str(event.get("session_id", "") or "")
            if event_session_id and not turn.session_id:
                turn.session_id = event_session_id

            if event_type == "token":
                turn.final_text += event.get("content", "")
                continue

            if event_type == "tool_call":
                try:
                    tool_calls = json.loads(event.get("content", "[]"))
                except json.JSONDecodeError:
                    tool_calls = []
                for tool_call in tool_calls:
                    turn.tool_calls.append(
                        ToolCallRecord(
                            name=str(tool_call.get("name", "")),
                            input=str(tool_call.get("input", "")),
                            tool_execution_id=str(event.get("tool_execution_id", "")),
                        )
                    )
                continue

            if event_type == "tool_result":
                try:
                    tool_result = json.loads(event.get("content", "{}"))
                except json.JSONDecodeError:
                    tool_result = {}
                turn.tool_results.append(
                    ToolResultRecord(
                        name=str(tool_result.get("name", "")),
                        content=str(tool_result.get("content", "")),
                        is_error=bool(tool_result.get("is_error", False)),
                        tool_execution_id=str(event.get("tool_execution_id", "")),
                    )
                )
                continue

            if event_type == "error":
                turn.error_event = str(event.get("content", ""))
                break

            if event_type == "done":
                turn.done_received = True
                break

    turn.duration_seconds = time.time() - started_at
    return turn


def manage_plan_payloads(turns: list[TurnReport]) -> list[dict[str, Any]]:
    payloads: list[dict[str, Any]] = []
    for turn in turns:
        for call in normalize_tool_calls(turn.tool_calls):
            if call.name == "manage_plan":
                payloads.append(parse_tool_input(call.input))
    return payloads


def evaluate_common_report(report: EvaluationReport, expected_turns: int) -> bool:
    if len(report.turn_reports) != expected_turns:
        report.failure_reasons.append(f"评测轮数异常，期望 {expected_turns} 轮，实际 {len(report.turn_reports)} 轮")
        return False

    first_turn = report.turn_reports[0]
    report.session_id = first_turn.session_id
    report.session_reused = bool(report.session_id) and all(turn.session_id == report.session_id for turn in report.turn_reports[1:])
    if not report.session_reused:
        report.failure_reasons.append("继续轮未复用同一 session")
    if not all(turn.done_received for turn in report.turn_reports):
        report.failure_reasons.append("至少有一轮未收到 done 事件")
    if any(turn.error_event for turn in report.turn_reports):
        report.failure_reasons.append("至少有一轮收到了 error 事件")
    return True


def append_empty_call_diagnostics(report: EvaluationReport) -> None:
    empty_manage_calls: list[ToolCallRecord] = []
    aborted_shell_calls: list[ToolCallRecord] = []
    for turn in report.turn_reports:
        real_empty_calls, turn_aborted_shell_calls = classify_empty_manage_plan_calls(turn)
        empty_manage_calls.extend(real_empty_calls)
        aborted_shell_calls.extend(turn_aborted_shell_calls)
    if empty_manage_calls:
        report.failure_reasons.append(f"出现空参数 manage_plan 调用，共 {len(empty_manage_calls)} 次")
    if aborted_shell_calls:
        report.diagnostics.append(f"检测到未执行的空参数 manage_plan 流式壳事件，共 {len(aborted_shell_calls)} 次")


def evaluate_session_continuity_report(report: EvaluationReport) -> EvaluationReport:
    if not evaluate_common_report(report, expected_turns=4):
        return report

    first_turn, second_turn, third_turn, fourth_turn = report.turn_reports
    first_manage_calls = [call for call in normalize_tool_calls(first_turn.tool_calls) if call.name == "manage_plan"]
    if len(first_manage_calls) < 3:
        report.failure_reasons.append(f"首轮 manage_plan 调用过少，实际 {len(first_manage_calls)} 次")
    expected_filename = f"{report.plan_filename}.md"
    if not any(parse_tool_input(call.input).get("filename") == expected_filename for call in first_manage_calls):
        report.failure_reasons.append("首轮没有显式把非默认计划文件名传给 manage_plan")

    for turn, label in ((second_turn, "第二轮"), (third_turn, "第三轮"), (fourth_turn, "第四轮")):
        manage_calls = [call for call in normalize_tool_calls(turn.tool_calls) if call.name == "manage_plan"]
        if not manage_calls:
            report.failure_reasons.append(f"{label}没有调用 manage_plan")
            continue
        if not any("filename" not in parse_tool_input(call.input) for call in manage_calls):
            report.failure_reasons.append(f"{label}没有出现省略 filename 的 manage_plan 调用")

    second_updates = [parse_tool_input(call.input) for call in normalize_tool_calls(second_turn.tool_calls) if call.name == "manage_plan"]
    if not any(item.get("action") == "update" and item.get("id") and not item.get("index") for item in second_updates):
        report.failure_reasons.append("第二轮没有出现按 id 更新且未依赖 index 的 manage_plan 调用")

    third_updates = [parse_tool_input(call.input) for call in normalize_tool_calls(third_turn.tool_calls) if call.name == "manage_plan"]
    if not any(item.get("action") in {"complete", "update"} and item.get("id") and not item.get("index") for item in third_updates):
        report.failure_reasons.append("第三轮没有出现按 id 完成/更新且未依赖 index 的 manage_plan 调用")

    fourth_updates = [parse_tool_input(call.input) for call in normalize_tool_calls(fourth_turn.tool_calls) if call.name == "manage_plan"]
    if not any(item.get("action") in {"complete", "update"} and item.get("id") and not item.get("index") for item in fourth_updates):
        report.failure_reasons.append("第四轮没有出现按 id 完成/更新且未依赖 index 的 manage_plan 调用")
    if not any(item.get("action") == "archive" for item in fourth_updates):
        report.failure_reasons.append("第四轮没有在全部完成后归档计划")

    combined_follow_up_results = "\n".join(result.content for turn in (second_turn, third_turn, fourth_turn) for result in turn.tool_results)
    expected_plan_label = f"{report.plan_filename}.md"
    if expected_plan_label not in combined_follow_up_results:
        report.failure_reasons.append("继续轮的工具结果没有命中会话绑定的计划文件")

    append_empty_call_diagnostics(report)
    report.duration_seconds = sum(turn.duration_seconds for turn in report.turn_reports)
    report.passed = not report.failure_reasons
    return report


def evaluate_autonomous_lifecycle_report(report: EvaluationReport) -> EvaluationReport:
    if not evaluate_common_report(report, expected_turns=2):
        return report

    payloads = manage_plan_payloads(report.turn_reports)
    if not any(item.get("action") == "add" for item in payloads):
        report.failure_reasons.append("自然场景没有自主创建计划")
    if not any(item.get("action") == "update" and item.get("status") == "in_progress" and item.get("id") and not item.get("index") for item in payloads):
        report.failure_reasons.append("自然场景没有按 id 把活动项更新为 in_progress")
    if not any(item.get("action") in {"complete", "update"} and item.get("id") and not item.get("index") for item in payloads):
        report.failure_reasons.append("自然场景没有出现按 id 的完成/状态更新")
    if not any(item.get("action") == "archive" for item in payloads):
        report.failure_reasons.append("自然场景没有在任务结束时归档计划")
    if any(item.get("action") in {"update", "complete"} and item.get("index") for item in payloads):
        report.failure_reasons.append("自然场景出现了依赖 index 的更新/完成调用")

    first_payloads = manage_plan_payloads([report.turn_reports[0]])
    if not any(item.get("action") == "add" for item in first_payloads):
        report.failure_reasons.append("自然场景首轮没有自主建计划")

    append_empty_call_diagnostics(report)
    report.duration_seconds = sum(turn.duration_seconds for turn in report.turn_reports)
    report.passed = not report.failure_reasons
    return report


def evaluate_report(report: EvaluationReport) -> EvaluationReport:
    if report.scenario == SCENARIO_AUTONOMOUS_LIFECYCLE:
        return evaluate_autonomous_lifecycle_report(report)
    return evaluate_session_continuity_report(report)


def build_prompts(scenario: str, plan_filename: str) -> list[tuple[str, str]]:
    if scenario == SCENARIO_AUTONOMOUS_LIFECYCLE:
        return [
            ("initial", autonomous_initial_prompt()),
            ("follow_up", autonomous_follow_up_prompt()),
        ]
    return [
        ("initial", initial_prompt(plan_filename)),
        ("follow_up", follow_up_prompt()),
        ("second_follow_up", second_follow_up_prompt()),
        ("final_follow_up", final_follow_up_prompt()),
    ]


async def run_evaluation(args: argparse.Namespace, plan_filename: str) -> EvaluationReport:
    report = EvaluationReport(
        scenario=args.scenario,
        plan_filename=plan_filename,
        ws_url=build_ws_url(args.base_url),
        model=args.model,
        mode=args.mode,
    )

    prompts = build_prompts(args.scenario, plan_filename)
    session_id = ""
    for label, prompt in prompts:
        turn = await run_turn(args=args, prompt=prompt, label=label, session_id=session_id)
        report.turn_reports.append(turn)
        if turn.session_id and not session_id:
            session_id = turn.session_id

    return evaluate_report(report)


def save_report(report: EvaluationReport, report_path: Path) -> None:
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(asdict(report), ensure_ascii=False, indent=2), encoding="utf-8")


def print_summary(report: EvaluationReport, report_path: Path) -> None:
    print("=" * 80)
    print("Manage Plan Eval")
    print("=" * 80)
    print(f"Scenario    : {report.scenario}")
    print(f"Plan File   : {report.plan_filename + '.md' if report.plan_filename else '<auto>'}")
    print(f"Session ID  : {report.session_id}")
    print(f"Duration    : {report.duration_seconds:.2f}s")
    print(f"SessionReuse: {report.session_reused}")
    print(f"Report Path : {report_path}")
    for turn in report.turn_reports:
        print(
            f"- {turn.label}: session={turn.session_id or '<empty>'} "
            f"tool_calls={len(turn.tool_calls)} done={turn.done_received} error={bool(turn.error_event)}"
        )
    if report.passed:
        print("PASS: manage_plan scenario verified")
    else:
        print("FAIL: manage_plan scenario failed")
        for reason in report.failure_reasons:
            print(f"- {reason}")


async def async_main() -> int:
    args = parse_args()
    ensure_backend_ready(args.base_url)
    plan_filename = ""
    if args.scenario == SCENARIO_SESSION_CONTINUITY:
        plan_filename = f"release-plan-e2e-{uuid.uuid4().hex[:8]}"
    report = await run_evaluation(args, plan_filename)
    report_path = make_report_path(args.report_path, args.scenario)
    save_report(report, report_path)
    print_summary(report, report_path)
    return 0 if report.passed else 1


def main() -> int:
    try:
        return asyncio.run(async_main())
    except KeyboardInterrupt:
        print("\n[INFO] 用户中断评估")
        return 130
    except Exception as exc:
        print(f"\n[ERROR] 评估失败: {exc}")
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
