from __future__ import annotations

import argparse
import json
import os
import subprocess
import sys
import time
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any

from subagent_test_lib import DEFAULT_BASE_URL, DEFAULT_MODEL, REPO_ROOT, REPORT_DIR, write_standard_report


@dataclass
class SuiteCase:
    case_id: str
    script: str
    extra_args: tuple[str, ...] = ()
    report_glob: str = ""
    report_arg_supported: bool = False
    blocking: bool = True


@dataclass
class SuiteExecution:
    case_id: str
    blocking: bool
    status: str
    duration_seconds: float
    return_code: int
    report_path: str = ""
    stdout_tail: str = ""
    stderr_tail: str = ""
    failure_reasons: list[str] = field(default_factory=list)


BLOCKING_CASES: tuple[SuiteCase, ...] = (
    SuiteCase(
        case_id="subagent_delegation",
        script="backend/tests/python/test_subagent_delegation.py",
        report_glob="subagent_delegation_report_*.json",
        report_arg_supported=True,
    ),
    SuiteCase(
        case_id="subagent_parallel_modes",
        script="backend/tests/python/test_subagent_parallel_modes.py",
        report_glob="subagent_parallel_modes_report_*.json",
        report_arg_supported=True,
    ),
    SuiteCase(
        case_id="subagent_dependency_reuse",
        script="backend/tests/python/test_subagent_dependency_reuse.py",
        report_glob="subagent_dependency_reuse_report_*.json",
        report_arg_supported=True,
    ),
)


OPTIONAL_CASES: tuple[SuiteCase, ...] = (
    SuiteCase(
        case_id="subagent_stress_suite",
        script="backend/tests/python/test_subagent_stress_suite.py",
        extra_args=("--iterations", "1", "--timeout", "600"),
        report_glob="subagent_stress_report_*.json",
        report_arg_supported=True,
        blocking=False,
    ),
)


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="AuraMate 子智能体覆盖套件运行器")
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL)
    parser.add_argument("--model", default=DEFAULT_MODEL)
    parser.add_argument("--python-executable", default=sys.executable)
    parser.add_argument("--report-path", default="")
    parser.add_argument("--force", action="store_true", help="强制重新执行，不复用最近报告")
    parser.add_argument("--include-stress", action="store_true", help="附带执行高波动压力套件，并将结果记为观察项")
    parser.add_argument("--reuse-latest-seconds", type=int, default=0, help="优先复用最近成功/失败的单项报告，0 表示不复用")
    return parser.parse_args()


def tail_text(text: str, limit: int = 3000) -> str:
    if len(text) <= limit:
        return text
    return text[-limit:]


def parse_failure_reasons(report_path: Path) -> list[str]:
    try:
        payload = json.loads(report_path.read_text(encoding="utf-8"))
    except Exception:
        return []
    raw = payload.get("failure_reasons") or []
    return [str(item) for item in raw]


def parse_report_status(report_path: Path) -> str:
    try:
        payload = json.loads(report_path.read_text(encoding="utf-8"))
    except Exception:
        return "failed"
    if "passed" in payload:
        return "passed" if bool(payload.get("passed")) else "failed"
    results = payload.get("results") or []
    if results and all(bool(item.get("success")) for item in results):
        return "passed"
    return "failed"


def find_latest_report(glob_pattern: str, started_at: float) -> Path | None:
    candidates = [path for path in REPORT_DIR.glob(glob_pattern) if path.is_file() and path.stat().st_mtime >= started_at - 1]
    if not candidates:
        return None
    return max(candidates, key=lambda item: item.stat().st_mtime)


def find_recent_report(glob_pattern: str, freshness_seconds: int) -> Path | None:
    cutoff = time.time() - freshness_seconds
    candidates = [
        path
        for path in REPORT_DIR.glob(glob_pattern)
        if path.is_file() and path.stat().st_mtime >= cutoff and parse_report_status(path) == "passed"
    ]
    if not candidates:
        return None
    return max(candidates, key=lambda item: item.stat().st_mtime)


def run_case(case: SuiteCase, args: argparse.Namespace) -> SuiteExecution:
    if not args.force and args.reuse_latest_seconds > 0 and case.report_glob:
        recent_report = find_recent_report(case.report_glob, args.reuse_latest_seconds)
        if recent_report is not None:
            return SuiteExecution(
                case_id=case.case_id,
                blocking=case.blocking,
                status=parse_report_status(recent_report),
                duration_seconds=0.0,
                return_code=0,
                report_path=str(recent_report),
                stdout_tail=f"reused_report={recent_report.name}",
                stderr_tail="",
                failure_reasons=parse_failure_reasons(recent_report),
            )

    started_at = time.time()
    report_path = REPORT_DIR / f"{case.case_id}_suite_report_{time.time_ns()}.json" if case.report_arg_supported else None
    command = [args.python_executable, str(REPO_ROOT / case.script), *case.extra_args]
    if case.report_arg_supported and report_path is not None:
        command.extend(["--base-url", args.base_url, "--model", args.model, "--report-path", str(report_path)])

    completed = subprocess.run(
        command,
        cwd=REPO_ROOT,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        env={**os.environ, "AURAMATE_BASE_URL": args.base_url, "AURAMATE_MODEL": args.model, "PYTHONUTF8": "1"},
    )
    duration_seconds = time.time() - started_at

    final_report_path = report_path
    if final_report_path is None and case.report_glob:
        final_report_path = find_latest_report(case.report_glob, started_at)

    failure_reasons = parse_failure_reasons(final_report_path) if final_report_path else []
    status = "passed" if completed.returncode == 0 else "failed"
    if not failure_reasons and completed.returncode != 0:
        failure_reasons = [f"return_code={completed.returncode}"]
    return SuiteExecution(
        case_id=case.case_id,
        blocking=case.blocking,
        status=status,
        duration_seconds=round(duration_seconds, 4),
        return_code=completed.returncode,
        report_path=str(final_report_path) if final_report_path else "",
        stdout_tail=tail_text(completed.stdout),
        stderr_tail=tail_text(completed.stderr),
        failure_reasons=failure_reasons,
    )


def main() -> int:
    args = parse_args()
    selected_cases = [*BLOCKING_CASES]
    if args.include_stress:
        selected_cases.extend(OPTIONAL_CASES)

    results = [run_case(case, args) for case in selected_cases]
    blocking_results = [item for item in results if item.blocking]
    optional_results = [item for item in results if not item.blocking]
    passed = all(item.status == "passed" for item in blocking_results)
    summary = {
        "total_cases": len(results),
        "blocking_cases": len(blocking_results),
        "optional_cases": len(optional_results),
        "passed_cases": sum(1 for item in results if item.status == "passed"),
        "passed_blocking_cases": sum(1 for item in blocking_results if item.status == "passed"),
        "failed_blocking_cases": [item.case_id for item in blocking_results if item.status != "passed"],
        "failed_optional_cases": [item.case_id for item in optional_results if item.status != "passed"],
        "avg_duration_seconds": round(sum(item.duration_seconds for item in results) / len(results), 4),
        "include_stress": args.include_stress,
    }
    payload = {
        "passed": passed,
        "base_url": args.base_url,
        "model": args.model,
        "report_schema": "subagent_coverage_suite/v2",
        "force": args.force,
        "reuse_latest_seconds": args.reuse_latest_seconds,
        "summary": summary,
        "results": [asdict(item) for item in results],
        "diagnostics": [
            f"passed_blocking_cases={summary['passed_blocking_cases']}",
            f"blocking_cases={summary['blocking_cases']}",
            f"optional_cases={summary['optional_cases']}",
            f"avg_duration_seconds={summary['avg_duration_seconds']}",
        ],
        "failure_reasons": [reason for item in blocking_results for reason in item.failure_reasons],
        "optional_failure_reasons": [reason for item in optional_results for reason in item.failure_reasons],
    }
    report_path = Path(args.report_path) if args.report_path else REPORT_DIR / f"subagent_coverage_suite_report_{time.time_ns()}.json"
    write_standard_report(report_path, payload)
    print(report_path)
    return 0 if passed else 1


if __name__ == "__main__":
    sys.exit(main())
