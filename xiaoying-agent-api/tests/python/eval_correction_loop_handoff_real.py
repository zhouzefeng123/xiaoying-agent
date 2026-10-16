from __future__ import annotations

import argparse
import asyncio
import json
import sqlite3
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
MAX_VERIFICATION_TURNS = 6
REPORTS_DIR = Path(__file__).resolve().parent / "reports"
REPO_ROOT = Path(__file__).resolve().parents[3]
WORKSPACE_DIR = REPO_ROOT / "workspace" / "correction_loop_eval"
DATABASE_PATH = REPO_ROOT / "backend" / "database" / "database.db"
HANDOFF_PHRASE = "请把最新一次失败时的页面截图、完整报错文案，或可以稳定复现的操作步骤发我"


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
    thought_events: list[str] = field(default_factory=list)
    task_state_snapshots: list[dict[str, Any]] = field(default_factory=list)
    done_received: bool = False
    error_event: str = ""
    duration_seconds: float = 0.0


@dataclass
class EvaluationReport:
    session_id: str = ""
    ws_url: str = ""
    model: str = ""
    mode: str = ""
    marker: str = ""
    fixture_paths: list[str] = field(default_factory=list)
    passed: bool = False
    duration_seconds: float = 0.0
    final_task_state: dict[str, Any] = field(default_factory=dict)
    failure_reasons: list[str] = field(default_factory=list)
    diagnostics: list[str] = field(default_factory=list)
    turn_reports: list[TurnReport] = field(default_factory=list)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="验证纠错模式重复复核达到边界后会真实结束，而不是继续循环。")
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL, help="后端地址，例如 http://localhost:3000")
    parser.add_argument("--model", default=DEFAULT_MODEL, help="评估使用的模型")
    parser.add_argument("--mode", default=DEFAULT_MODE, help="会话模式，默认 coding")
    parser.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT_SECONDS, help="单轮超时时间（秒）")
    parser.add_argument("--database-path", default=str(DATABASE_PATH), help="后端 sqlite 数据库路径")
    parser.add_argument("--report-path", default="", help="可选：指定报告输出路径")
    parser.add_argument("--keep-fixture", action="store_true", help="保留自动生成的测试文件")
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


def make_report_path(custom_path: str) -> Path:
    if custom_path:
        return Path(custom_path)
    REPORTS_DIR.mkdir(parents=True, exist_ok=True)
    timestamp = time.strftime("%Y%m%d_%H%M%S")
    return REPORTS_DIR / f"correction_loop_handoff_real_{timestamp}.json"


def relative_workspace_path(path: Path) -> str:
    return path.relative_to(REPO_ROOT).as_posix()


def create_fixture_chain(marker: str) -> list[Path]:
    WORKSPACE_DIR.mkdir(parents=True, exist_ok=True)
    paths = [
        WORKSPACE_DIR / f"step_{index}_{uuid.uuid4().hex[:8]}.txt"
        for index in range(1, MAX_VERIFICATION_TURNS + 1)
    ]
    for index, path in enumerate(paths, start=1):
        lines = [
            "Correction loop regression evidence",
            f"step={index}",
        ]
        if index < len(paths):
            lines.append(f"next_path={paths[index]}")
        else:
            lines.extend(["terminal=done", f"marker={marker}"])
        path.write_text("\n".join(lines), encoding="utf-8")
    return paths


def build_initial_prompt() -> str:
    return "我们先建立一个会话。只回复 READY。"


def build_correction_prompt(first_path: Path) -> str:
    return (
        "不是这样，你刚才的判断有误。"
        f"请从 `{relative_workspace_path(first_path)}` 开始读取。"
        "每个文件都会给出下一个文件的完整路径；必须先读完当前文件，再按 next_path 继续读取下一个。"
        "当你读到 terminal=done 时停止，不要猜测任何尚未读取的文件内容。"
        "最后只告诉我最终 marker。"
    )


async def run_turn(
    args: argparse.Namespace,
    prompt: str,
    label: str,
    session_id: str = "",
    database_path: Path | None = None,
) -> TurnReport:
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
                turn.final_text += str(event.get("content", ""))
                continue

            if event_type == "thought":
                data = event.get("data") or {}
                content = str(data.get("content", "")).strip()
                if content:
                    turn.thought_events.append(content)
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
                if database_path is not None and turn.session_id:
                    state = load_task_state(database_path, turn.session_id)
                    turn.task_state_snapshots.append(
                        {
                            "status": state.get("status", ""),
                            "correction_mode": state.get("correction_mode", False),
                            "correction_verification_turns": state.get("correction_verification_turns", 0),
                            "last_tool": state.get("last_tool", ""),
                        }
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


def load_task_state(database_path: Path, session_id: str) -> dict[str, Any]:
    with sqlite3.connect(database_path) as conn:
        row = conn.execute(
            "SELECT value FROM cognitive_state WHERE key = ? LIMIT 1",
            (f"task_state:{session_id}",),
        ).fetchone()
    if row is None or not row[0]:
        return {}
    payload = json.loads(row[0])
    if not isinstance(payload, dict):
        raise RuntimeError("task state is not an object")
    return payload


def evaluate_report(report: EvaluationReport) -> EvaluationReport:
    if len(report.turn_reports) != 2:
        report.failure_reasons.append(f"评估轮数异常，期望 2 轮，实际 {len(report.turn_reports)} 轮")
        return report

    _, correction_turn = report.turn_reports
    successful_results = [result for result in correction_turn.tool_results if not result.is_error]
    if not report.session_id:
        report.failure_reasons.append("没有拿到 session_id")
    if not all(turn.session_id == report.session_id for turn in report.turn_reports if turn.session_id):
        report.failure_reasons.append("多轮请求未复用同一 session")
    if not all(turn.done_received for turn in report.turn_reports):
        report.failure_reasons.append("至少有一轮未收到 done 事件")
    if any(turn.error_event for turn in report.turn_reports):
        report.failure_reasons.append("至少有一轮收到了 error 事件")
    if len(successful_results) < MAX_VERIFICATION_TURNS:
        report.failure_reasons.append(
            f"纠错轮成功工具结果不足，实际 {len(successful_results)} 次，要求至少 {MAX_VERIFICATION_TURNS} 次"
        )
    if HANDOFF_PHRASE not in correction_turn.final_text:
        report.failure_reasons.append("纠错轮没有输出重复复核后的收口提示")
    if report.final_task_state.get("status") != "waiting_user":
        report.failure_reasons.append("纠错轮后 task_state.status 没有进入 waiting_user")
    if report.final_task_state.get("correction_verification_turns") != MAX_VERIFICATION_TURNS:
        report.failure_reasons.append(
            "纠错轮后 correction_verification_turns 没有达到 "
            f"{MAX_VERIFICATION_TURNS}"
        )
    if report.marker in correction_turn.final_text:
        report.failure_reasons.append("纠错轮在达到收口边界后仍继续回答了原任务")
    if correction_turn.final_text.count("不是这样，你刚才的判断有误") > 0:
        report.failure_reasons.append("最终可见回复仍在重复用户原始指令")

    report.passed = not report.failure_reasons
    return report


async def run_evaluation(args: argparse.Namespace) -> EvaluationReport:
    marker = f"LOOP_{uuid.uuid4().hex[:10].upper()}"
    fixture_paths = create_fixture_chain(marker)
    database_path = Path(args.database_path)
    report = EvaluationReport(
        ws_url=build_ws_url(args.base_url),
        model=args.model,
        mode=args.mode,
        marker=marker,
        fixture_paths=[str(path) for path in fixture_paths],
    )

    started_at = time.time()
    initial_turn = await run_turn(args, build_initial_prompt(), "initial", database_path=database_path)
    report.turn_reports.append(initial_turn)
    report.session_id = initial_turn.session_id

    correction_turn = await run_turn(
        args,
        build_correction_prompt(fixture_paths[0]),
        "correction_chain",
        session_id=report.session_id,
        database_path=database_path,
    )
    report.turn_reports.append(correction_turn)
    report.duration_seconds = time.time() - started_at
    if report.session_id:
        report.final_task_state = load_task_state(database_path, report.session_id)
    return evaluate_report(report)


def save_report(report: EvaluationReport, report_path: Path) -> None:
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(asdict(report), ensure_ascii=False, indent=2), encoding="utf-8")


def cleanup_fixtures(paths: list[str]) -> None:
    for raw_path in paths:
        path = Path(raw_path)
        if path.exists():
            path.unlink()


def print_summary(report: EvaluationReport, report_path: Path) -> None:
    print("=" * 80)
    print("Correction Loop Handoff Eval")
    print("=" * 80)
    print(f"Session ID  : {report.session_id}")
    print(f"Duration    : {report.duration_seconds:.2f}s")
    print(f"Marker      : {report.marker}")
    print(f"Report Path : {report_path}")
    for turn in report.turn_reports:
        print(
            f"- {turn.label}: session={turn.session_id or '<empty>'} "
            f"tool_calls={len(turn.tool_calls)} tool_results={len(turn.tool_results)} "
            f"done={turn.done_received} thoughts={len(turn.thought_events)} error={bool(turn.error_event)}"
        )
    if report.passed:
        print("PASS: correction loop handoff verified")
    else:
        print("FAIL: correction loop handoff failed")
        for reason in report.failure_reasons:
            print(f"- {reason}")


async def async_main() -> int:
    args = parse_args()
    ensure_backend_ready(args.base_url)
    report_path = make_report_path(args.report_path)
    report = await run_evaluation(args)
    save_report(report, report_path)
    print_summary(report, report_path)
    if not args.keep_fixture:
        cleanup_fixtures(report.fixture_paths)
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
