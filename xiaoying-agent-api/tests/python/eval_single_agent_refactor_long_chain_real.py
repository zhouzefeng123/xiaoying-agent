from __future__ import annotations

import argparse
import asyncio
import json
import shutil
import subprocess
import sys
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
DEFAULT_TIMEOUT_SECONDS = 420
REPO_ROOT = Path(__file__).resolve().parents[3]
WORKSPACE_DIR = REPO_ROOT / "workspace" / "single_agent_refactor_long_chain_eval"
REPORTS_DIR = Path(__file__).resolve().parent / "reports"
FORBIDDEN_TOOLS = {"delegate_task", "manage_worker", "wait_for_workers"}


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
    duration_seconds: float = 0.0
    final_text: str = ""
    tool_calls: list[ToolCallRecord] = field(default_factory=list)
    tool_results: list[ToolResultRecord] = field(default_factory=list)
    thought_events: list[str] = field(default_factory=list)
    done_received: bool = False
    error_event: str = ""


@dataclass
class EvaluationReport:
    scenario: str
    fixture_root: str
    session_id: str
    ws_url: str
    model: str
    mode: str
    duration_seconds: float
    final_text: str
    passed: bool
    failure_reasons: list[str] = field(default_factory=list)
    diagnostics: list[str] = field(default_factory=list)
    turn_reports: list[TurnReport] = field(default_factory=list)
    tool_calls: list[ToolCallRecord] = field(default_factory=list)
    tool_results: list[ToolResultRecord] = field(default_factory=list)
    files: dict[str, str] = field(default_factory=dict)
    unittest_return_code: int = -1
    unittest_stdout: str = ""
    unittest_stderr: str = ""
    session_reused: bool = False


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="单主 agent 纯代码重构长链路真实评估")
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL)
    parser.add_argument("--model", default=DEFAULT_MODEL)
    parser.add_argument("--mode", default=DEFAULT_MODE)
    parser.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT_SECONDS)
    parser.add_argument("--keep-fixture", action="store_true")
    parser.add_argument("--report-path", default="")
    return parser.parse_args()


def ensure_backend_ready(base_url: str) -> None:
    with urlopen(f"{base_url}/v1/models", timeout=10) as response:
        if response.status != 200:
            raise RuntimeError(f"backend not ready: {response.status}")


def build_ws_url(base_url: str) -> str:
    if base_url.startswith("https://"):
        return "wss://" + base_url[len("https://") :] + "/v1/ws"
    if base_url.startswith("http://"):
        return "ws://" + base_url[len("http://") :] + "/v1/ws"
    return base_url.rstrip("/") + "/v1/ws"


def write_fixture_file(root: Path, relative_path: str, content: str) -> None:
    target = root / relative_path
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(content.strip() + "\n", encoding="utf-8")


def create_fixture(root: Path) -> None:
    write_fixture_file(root, "src/__init__.py", "")
    write_fixture_file(
        root,
        "src/invoice.py",
        """
from decimal import Decimal, ROUND_HALF_UP


def _money(value):
    return Decimal(str(value)).quantize(Decimal("0.01"), rounding=ROUND_HALF_UP)


def invoice_total(lines, discount_rate):
    if not 0 <= discount_rate < 1:
        raise ValueError("discount_rate must be between 0 and 1")
    subtotal = sum(Decimal(str(qty)) * Decimal(str(unit_price)) for qty, unit_price in lines)
    discounted = subtotal * (Decimal("1") - Decimal(str(discount_rate)))
    tax = discounted * Decimal("0.08")
    return _money(discounted + tax)
""",
    )
    write_fixture_file(
        root,
        "src/quote.py",
        """
from decimal import Decimal, ROUND_HALF_UP


def _money(value):
    return Decimal(str(value)).quantize(Decimal("0.01"), rounding=ROUND_HALF_UP)


def quote_total(lines, discount_rate):
    if not 0 <= discount_rate < 1:
        raise ValueError("discount_rate must be between 0 and 1")
    subtotal = sum(Decimal(str(qty)) * Decimal(str(unit_price)) for qty, unit_price in lines)
    discounted = subtotal * (Decimal("1") - Decimal(str(discount_rate)))
    tax = discounted * Decimal("0.08")
    return _money(discounted + tax)
""",
    )
    write_fixture_file(
        root,
        "src/reporting.py",
        """
from decimal import Decimal, ROUND_HALF_UP


def _money(value):
    return Decimal(str(value)).quantize(Decimal("0.01"), rounding=ROUND_HALF_UP)


def build_totals(lines, discount_rate):
    if not 0 <= discount_rate < 1:
        raise ValueError("discount_rate must be between 0 and 1")
    subtotal = sum(Decimal(str(qty)) * Decimal(str(unit_price)) for qty, unit_price in lines)
    discounted = subtotal * (Decimal("1") - Decimal(str(discount_rate)))
    tax = discounted * Decimal("0.08")
    return {
        "subtotal": _money(discounted),
        "tax": _money(tax),
        "grand_total": _money(discounted + tax),
    }
""",
    )
    write_fixture_file(
        root,
        "tests/test_pricing_flow.py",
        """
import unittest

from src.invoice import invoice_total
from src.quote import quote_total
from src.reporting import build_totals


class PricingFlowTests(unittest.TestCase):
    def test_invoice_total(self):
        self.assertEqual(str(invoice_total([(2, "19.99")], 0.10)), "38.86")

    def test_quote_total(self):
        self.assertEqual(str(quote_total([(3, "10.00")], 0.05)), "30.78")

    def test_reporting_totals(self):
        self.assertEqual(
            {key: str(value) for key, value in build_totals([(1, "100.00")], 0.20).items()},
            {"subtotal": "80.00", "tax": "6.40", "grand_total": "86.40"},
        )


if __name__ == "__main__":
    unittest.main()
""",
    )


def build_initial_prompt(root: Path) -> str:
    return (
        f"请在单主 agent 模式下重构这个 Python 小项目：{root}。"
        "目标是把 invoice、quote、reporting 三个模块中重复的金额舍入、折扣校验、税额计算抽到新的 `src/pricing.py`。"
        "要求："
        "1. 保持 `invoice_total`、`quote_total`、`build_totals` 的公开函数签名不变；"
        "2. 在 `src/pricing.py` 中提供 `money`、`discounted_subtotal`、`tax_amount`、`grand_total` 四个共享函数；"
        "3. 更新三个调用方改用共享 helper，移除重复实现；"
        "4. 在 `tests/test_pricing_flow.py` 补充 `test_negative_discount_rejected` 与 `test_half_up_rounding`；"
        "5. 运行 `python -m unittest discover -s tests -q` 并修到通过；"
        "6. 不要使用 delegate_task，不要改公开 API。"
    )


def build_follow_up_prompt() -> str:
    return (
        "继续。复查是否还有重复公式或重复校验残留；"
        "如果还有，继续收敛到 `src/pricing.py`。"
        "然后重新运行 `python -m unittest discover -s tests -q`，"
        "最后给出精炼摘要，说明变更文件和验证结果。不要使用子 agent。"
    )


def build_payload(prompt: str, model: str, mode: str, session_id: str = "") -> dict[str, Any]:
    payload: dict[str, Any] = {"type": "chat", "content": prompt, "model": model, "mode": mode}
    if session_id:
        payload["session_id"] = session_id
    return payload


async def run_turn(args: argparse.Namespace, prompt: str, label: str, session_id: str = "") -> TurnReport:
    ws_url = build_ws_url(args.base_url)
    turn = TurnReport(label=label, prompt=prompt, session_id=session_id)
    started_at = time.time()

    async with websockets.connect(ws_url, max_size=8 * 1024 * 1024) as websocket:
        await websocket.send(json.dumps(build_payload(prompt, args.model, args.mode, session_id), ensure_ascii=False))
        deadline = time.time() + args.timeout
        while time.time() < deadline:
            try:
                raw = await asyncio.wait_for(websocket.recv(), timeout=min(2.0, max(0.1, deadline - time.time())))
            except asyncio.TimeoutError:
                continue
            event = json.loads(raw)
            event_type = event.get("type", "")
            event_session_id = str(event.get("session_id", "") or "")
            if event_session_id and not turn.session_id:
                turn.session_id = event_session_id
            if event_type == "token":
                turn.final_text += str(event.get("content", ""))
            elif event_type == "thought":
                data = event.get("data") or {}
                content = str(data.get("content", "")).strip()
                if content:
                    turn.thought_events.append(content)
            elif event_type == "tool_call":
                try:
                    tool_calls = json.loads(event.get("content", "[]"))
                except json.JSONDecodeError:
                    tool_calls = []
                for tool_call in tool_calls:
                    turn.tool_calls.append(
                        ToolCallRecord(
                            name=str(tool_call.get("name", "") or ""),
                            input=str(tool_call.get("input", "") or ""),
                            tool_execution_id=str(event.get("tool_execution_id", "") or ""),
                        )
                    )
            elif event_type == "tool_result":
                try:
                    data = json.loads(event.get("content", "{}"))
                except json.JSONDecodeError:
                    data = {}
                turn.tool_results.append(
                    ToolResultRecord(
                        name=str(data.get("name", "") or ""),
                        content=str(data.get("content", "") or ""),
                        is_error=bool(data.get("is_error", False)),
                        tool_execution_id=str(event.get("tool_execution_id", "") or ""),
                    )
                )
            elif event_type == "done":
                turn.done_received = True
                break
            elif event_type == "error":
                turn.error_event = str(event.get("content", "") or event.get("error", "") or "unknown error")
                break
    turn.duration_seconds = time.time() - started_at
    return turn


def read_files(root: Path) -> dict[str, str]:
    files = {}
    for relative_path in (
        "src/pricing.py",
        "src/invoice.py",
        "src/quote.py",
        "src/reporting.py",
        "tests/test_pricing_flow.py",
    ):
        path = root / relative_path
        files[relative_path] = path.read_text(encoding="utf-8") if path.exists() else ""
    return files


def run_unittest(root: Path) -> subprocess.CompletedProcess[str]:
    return subprocess.run(
        [sys.executable, "-m", "unittest", "discover", "-s", "tests", "-q"],
        cwd=root,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
    )


def evaluate_report(report: EvaluationReport) -> EvaluationReport:
    report.tool_calls = [call for turn in report.turn_reports for call in turn.tool_calls]
    report.tool_results = [result for turn in report.turn_reports for result in turn.tool_results]
    report.session_reused = (
        len(report.turn_reports) > 1
        and bool(report.session_id)
        and all(turn.session_id == report.session_id for turn in report.turn_reports[1:])
    )
    report.files = read_files(Path(report.fixture_root))

    if not report.turn_reports or not all(turn.done_received for turn in report.turn_reports):
        report.failure_reasons.append("未收到全部 done 事件")
    if any(turn.error_event for turn in report.turn_reports):
        report.failure_reasons.append("收到 error 事件")
    if len(report.turn_reports) > 1 and not report.session_reused:
        report.failure_reasons.append("继续轮未复用同一 session")
    forbidden = sorted({call.name for call in report.tool_calls if call.name in FORBIDDEN_TOOLS})
    if forbidden:
        report.failure_reasons.append(f"单主 agent 场景出现禁止工具: {forbidden}")
    if report.duration_seconds > 420:
        report.failure_reasons.append(f"单主 agent 纯重构链路耗时过长: {report.duration_seconds:.2f}s > 420s")

    pricing = report.files["src/pricing.py"]
    for marker in ("def money", "def discounted_subtotal", "def tax_amount", "def grand_total"):
        if marker not in pricing:
            report.failure_reasons.append(f"pricing helper 缺少: {marker}")

    for relative_path in ("src/invoice.py", "src/quote.py", "src/reporting.py"):
        content = report.files[relative_path]
        if "from .pricing import" not in content and "from src.pricing import" not in content:
            report.failure_reasons.append(f"{relative_path} 未改用共享 pricing helper")
        if "ROUND_HALF_UP" in content or "discount_rate must be between 0 and 1" in content:
            report.failure_reasons.append(f"{relative_path} 仍保留重复金额/校验逻辑")

    tests = report.files["tests/test_pricing_flow.py"]
    for marker in ("test_negative_discount_rejected", "test_half_up_rounding"):
        if marker not in tests:
            report.failure_reasons.append(f"测试缺少: {marker}")

    completed = run_unittest(Path(report.fixture_root))
    report.unittest_return_code = completed.returncode
    report.unittest_stdout = completed.stdout
    report.unittest_stderr = completed.stderr
    if completed.returncode != 0:
        report.failure_reasons.append("最终 unittest 未通过")

    run_command_calls = [call for call in report.tool_calls if call.name in {"run_command", "bash"}]
    if not run_command_calls:
        report.failure_reasons.append("未观察到命令行验证")
    if len(report.tool_calls) < 6:
        report.failure_reasons.append(f"工具调用次数不足: {len(report.tool_calls)} < 6")

    report.passed = not report.failure_reasons
    return report


def save_report(path: Path, report: EvaluationReport) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(asdict(report), ensure_ascii=False, indent=2), encoding="utf-8")


async def run_evaluation(args: argparse.Namespace) -> EvaluationReport:
    run_id = uuid.uuid4().hex[:8]
    fixture_root = WORKSPACE_DIR / f"refactor_{run_id}"
    if fixture_root.exists():
        shutil.rmtree(fixture_root)
    create_fixture(fixture_root)

    started_at = time.time()
    first_turn = await run_turn(args, build_initial_prompt(fixture_root), "initial")
    follow_up = await run_turn(args, build_follow_up_prompt(), "follow_up", first_turn.session_id)
    final_text = first_turn.final_text + "\n" + follow_up.final_text
    report = EvaluationReport(
        scenario="single_agent_refactor_long_chain",
        fixture_root=str(fixture_root),
        session_id=first_turn.session_id,
        ws_url=build_ws_url(args.base_url),
        model=args.model,
        mode=args.mode,
        duration_seconds=time.time() - started_at,
        final_text=final_text,
        passed=False,
        turn_reports=[first_turn, follow_up],
    )
    report = evaluate_report(report)
    if not args.keep_fixture and report.passed:
        shutil.rmtree(fixture_root, ignore_errors=True)
    return report


def main() -> int:
    args = parse_args()
    ensure_backend_ready(args.base_url)
    report = asyncio.run(run_evaluation(args))
    target = Path(args.report_path) if args.report_path else REPORTS_DIR / f"single_agent_refactor_long_chain_real_{uuid.uuid4().hex[:8]}.json"
    save_report(target, report)
    print(target)
    if report.failure_reasons:
        for item in report.failure_reasons:
            print(f"- {item}")
    return 0 if report.passed else 1


if __name__ == "__main__":
    raise SystemExit(main())
