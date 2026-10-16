from __future__ import annotations

import argparse
import json
import os
import shutil
import subprocess
import sys
import time
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any

from agent_benchmark_suite import (
    REPO_ROOT,
    BenchmarkCase,
    build_suite_manifest,
    filter_cases,
    get_case_tier,
    get_cleanup_plan,
    render_case_table,
    summarize_by_category,
    summarize_by_tier,
)


BENCHMARK_ROOT = Path(__file__).resolve().parent
LOGS_ROOT = BENCHMARK_ROOT / "logs" / "benchmark_suite"
REPORTS_ROOT = BENCHMARK_ROOT / "reports" / "benchmark_suite"


@dataclass
class BenchmarkExecution:
    case_id: str
    name: str
    tier: str
    automation_level: str
    status: str
    duration_seconds: float = 0.0
    command: list[str] = field(default_factory=list)
    return_code: int | None = None
    report_path: str = ""
    source_report_path: str = ""
    stdout_log_path: str = ""
    stderr_log_path: str = ""
    passed: bool | None = None
    diagnostics: list[str] = field(default_factory=list)
    failure_reasons: list[str] = field(default_factory=list)
    stdout_tail: str = ""
    stderr_tail: str = ""


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="AuraMate Agent 综合基准套件运行器")
    parser.add_argument("--list", action="store_true", help="只列出当前测试矩阵，不执行")
    parser.add_argument("--list-cleanup", action="store_true", help="列出保留、淘汰和待人工确认的测试文件清单")
    parser.add_argument("--dry-run", action="store_true", help="打印将要执行的命令，但不真正运行")
    parser.add_argument("--case", default="", help="只运行指定 case_id，多个用逗号分隔")
    parser.add_argument("--tier", default="", help="按层级筛选：core、extended、planned，多个用逗号分隔")
    parser.add_argument("--category", default="", help="只运行指定类别，多个用逗号分隔")
    parser.add_argument("--tag", default="", help="只运行包含指定标签的 case，多个用逗号分隔")
    parser.add_argument("--include-planned", action="store_true", help="列表或导出时包含规划中的场景")
    parser.add_argument("--base-url", default="http://localhost:3000", help="后端地址")
    parser.add_argument("--model", default="aura-coder", help="执行 ready 场景时使用的模型")
    parser.add_argument("--python-executable", default=sys.executable, help="执行脚本使用的 Python 解释器")
    parser.add_argument("--report-path", default="", help="将本次套件结果写入 JSON 文件")
    parser.add_argument("--export-manifest", default="", help="导出完整基准矩阵 JSON，不执行 case")
    parser.add_argument("--max-cases", type=int, default=0, help="最多执行前 N 个筛选结果，0 表示不限制")
    parser.add_argument("--run-label", default="", help="可选：指定本次运行标签，默认使用时间戳")
    return parser.parse_args()


def split_csv(raw: str) -> set[str]:
    return {item.strip() for item in raw.split(",") if item.strip()}


def build_run_id(run_label: str) -> str:
    if run_label.strip():
        return run_label.strip().replace(" ", "_")
    return time.strftime("%Y%m%d_%H%M%S")


def prepare_run_directories(run_id: str) -> tuple[Path, Path]:
    log_dir = LOGS_ROOT / run_id
    report_dir = REPORTS_ROOT / run_id
    log_dir.mkdir(parents=True, exist_ok=True)
    report_dir.mkdir(parents=True, exist_ok=True)
    return log_dir, report_dir


def find_latest_report(pattern: str, started_at: float) -> Path | None:
    pattern_root, pattern_name = split_glob(pattern)
    flattened = sorted((REPO_ROOT / pattern_root).glob(pattern_name))
    valid = [path for path in flattened if path.is_file() and path.stat().st_mtime >= started_at - 1]
    if not valid:
        return None
    return max(valid, key=lambda item: item.stat().st_mtime)


def split_glob(pattern: str) -> tuple[str, str]:
    normalized = pattern.replace("\\", "/")
    slash_index = normalized.rfind("/")
    if slash_index < 0:
        return ".", normalized
    return normalized[:slash_index], normalized[slash_index + 1 :]


def tail_text(text: str, limit: int = 4000) -> str:
    if len(text) <= limit:
        return text
    return text[-limit:]


def build_command(case: BenchmarkCase, python_executable: str, base_url: str, model: str, report_path: Path | None = None) -> list[str]:
    if case.script_target is None:
        raise ValueError(f"case {case.case_id} is not executable")
    command = [python_executable, str(case.script_path)]
    command.extend(case.script_target.extra_args)
    if case.script_target.pass_common_args:
        command.extend(["--base-url", base_url, "--model", model])
    if case.script_target.report_arg_supported and report_path is not None:
        command.extend(["--report-path", str(report_path)])
    return command


def format_cleanup_plan() -> str:
    cleanup = get_cleanup_plan()
    lines = ["保留的 ready case:"]
    for case_id in cleanup["active_ready_case_ids"]:
        lines.append(f"- {case_id}")
    lines.append("")
    lines.append("保留的框架入口:")
    for path in cleanup["benchmark_entry_files"]:
        lines.append(f"- {path}")
    lines.append("")
    lines.append("保留的 benchmark 脚本:")
    for path in cleanup["benchmark_case_scripts"]:
        lines.append(f"- {path}")
    lines.append("")
    lines.append("本次删除的非基准脚本:")
    for path in cleanup["removed_scripts"]:
        lines.append(f"- {path}")
    return "\n".join(lines)


def write_text_file(path: Path, content: str) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(content, encoding="utf-8")


def write_json_file(path: Path, payload: dict[str, Any]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")


def parse_json_report(report_path: Path) -> tuple[bool | None, list[str], list[str]]:
    try:
        payload = json.loads(report_path.read_text(encoding="utf-8"))
    except Exception:
        return None, [], []
    passed = payload.get("passed")
    diagnostics = payload.get("diagnostics") or []
    failure_reasons = payload.get("failure_reasons") or []
    return passed if isinstance(passed, bool) else None, list(diagnostics), list(failure_reasons)


def execute_case(
    case: BenchmarkCase,
    python_executable: str,
    base_url: str,
    model: str,
    dry_run: bool,
    run_log_dir: Path,
    run_report_dir: Path,
) -> BenchmarkExecution:
    if case.script_target is None:
        return BenchmarkExecution(
            case_id=case.case_id,
            name=case.name,
            tier=get_case_tier(case),
            automation_level=case.automation_level,
            status="planned",
            passed=None,
        )

    stdout_log_path = run_log_dir / f"{case.case_id}.stdout.log"
    stderr_log_path = run_log_dir / f"{case.case_id}.stderr.log"
    upstream_report_path = run_report_dir / f"{case.case_id}.upstream.json" if case.script_target.report_arg_supported else None
    case_report_path = run_report_dir / f"{case.case_id}.json"
    command = build_command(
        case,
        python_executable=python_executable,
        base_url=base_url,
        model=model,
        report_path=upstream_report_path,
    )
    if dry_run:
        return BenchmarkExecution(
            case_id=case.case_id,
            name=case.name,
            tier=get_case_tier(case),
            automation_level=case.automation_level,
            status="dry-run",
            command=command,
            report_path=str(case_report_path),
            passed=None,
        )

    started_at = time.time()
    completed = subprocess.run(
        command,
        cwd=REPO_ROOT,
        capture_output=True,
        text=True,
        encoding="utf-8",
        errors="replace",
        env={
            **os.environ,
            "PYTHONUTF8": "1",
            "PYTHONIOENCODING": "utf-8",
        },
    )
    duration_seconds = time.time() - started_at

    write_text_file(stdout_log_path, completed.stdout)
    write_text_file(stderr_log_path, completed.stderr)

    report_path = None
    passed = None
    diagnostics: list[str] = []
    failure_reasons: list[str] = []
    source_report_path = ""
    if upstream_report_path is not None and upstream_report_path.exists():
        report_path = upstream_report_path
        source_report_path = str(upstream_report_path)
        passed, diagnostics, failure_reasons = parse_json_report(upstream_report_path)
    elif case.script_target.report_glob:
        report_path = find_latest_report(case.script_target.report_glob, started_at)
        if report_path is not None:
            passed, diagnostics, failure_reasons = parse_json_report(report_path)
            source_report_path = str(report_path)
            if upstream_report_path is not None and report_path != upstream_report_path:
                shutil.copyfile(report_path, upstream_report_path)

    status = "passed"
    if completed.returncode != 0:
        status = "failed"
    elif passed is False:
        status = "failed"

    execution = BenchmarkExecution(
        case_id=case.case_id,
        name=case.name,
        tier=get_case_tier(case),
        automation_level=case.automation_level,
        status=status,
        duration_seconds=duration_seconds,
        command=command,
        return_code=completed.returncode,
        report_path=str(case_report_path),
        source_report_path=source_report_path,
        stdout_log_path=str(stdout_log_path),
        stderr_log_path=str(stderr_log_path),
        passed=passed,
        diagnostics=diagnostics,
        failure_reasons=failure_reasons,
        stdout_tail=tail_text(completed.stdout),
        stderr_tail=tail_text(completed.stderr),
    )

    write_json_file(case_report_path, asdict(execution))
    return execution


def format_summary(executions: list[BenchmarkExecution]) -> str:
    headers = ("case_id", "status", "seconds", "report")
    rows = [headers]
    for item in executions:
        rows.append(
            (
                item.case_id,
                item.status,
                f"{item.duration_seconds:.1f}",
                Path(item.report_path).name if item.report_path else "-",
            )
        )
    widths = [max(len(row[index]) for row in rows) for index in range(len(headers))]
    lines: list[str] = []
    for row_index, row in enumerate(rows):
        lines.append("  ".join(value.ljust(widths[index]) for index, value in enumerate(row)))
        if row_index == 0:
            lines.append("  ".join("-" * width for width in widths))
    return "\n".join(lines)


def build_result_payload(cases: list[BenchmarkCase], executions: list[BenchmarkExecution], args: argparse.Namespace) -> dict[str, Any]:
    return {
        "generated_at": time.strftime("%Y-%m-%d %H:%M:%S"),
        "base_url": args.base_url,
        "model": args.model,
        "case_count": len(cases),
        "category_summary": summarize_by_category(cases),
        "tier_summary": summarize_by_tier(cases),
        "cleanup_plan": get_cleanup_plan(),
        "cases": [case.to_dict() for case in cases],
        "executions": [asdict(item) for item in executions],
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
    if args.list_cleanup:
        print(format_cleanup_plan())
        return 0
    if args.export_manifest:
        manifest = build_suite_manifest(include_planned=args.include_planned)
        target = save_result(manifest, args.export_manifest)
        print(target)
        return 0

    selected = filter_cases(
        case_ids=split_csv(args.case) or None,
        tiers=split_csv(args.tier) or None,
        categories=split_csv(args.category) or None,
        tags=split_csv(args.tag) or None,
        include_planned=args.include_planned or args.list,
    )
    if args.max_cases > 0:
        selected = selected[: args.max_cases]

    if not selected:
        print("未找到匹配的测试场景。")
        return 1

    print("当前筛选结果：")
    print(render_case_table(selected))

    if args.list:
        return 0

    run_id = build_run_id(args.run_label)
    run_log_dir, run_report_dir = prepare_run_directories(run_id)
    print(f"统一日志目录: {run_log_dir}")
    print(f"统一报告目录: {run_report_dir}")

    executions: list[BenchmarkExecution] = []
    for case in selected:
        print(f"\n[RUN] {case.case_id} - {case.name}")
        execution = execute_case(
            case,
            python_executable=args.python_executable,
            base_url=args.base_url,
            model=args.model,
            dry_run=args.dry_run,
            run_log_dir=run_log_dir,
            run_report_dir=run_report_dir,
        )
        executions.append(execution)
        print(f"状态: {execution.status}")
        if execution.command:
            print("命令:")
            print(" ".join(execution.command))
        if execution.report_path:
            print(f"统一 case 报告: {execution.report_path}")
        if execution.source_report_path:
            print(f"原始结构化报告: {execution.source_report_path}")
        if execution.stdout_log_path:
            print(f"stdout 日志: {execution.stdout_log_path}")
        if execution.stderr_log_path:
            print(f"stderr 日志: {execution.stderr_log_path}")
        if execution.diagnostics:
            print("诊断:")
            for item in execution.diagnostics:
                print(f"- {item}")
        if execution.failure_reasons:
            print("失败原因:")
            for item in execution.failure_reasons:
                print(f"- {item}")
        if execution.status == "failed" and execution.stderr_tail:
            print("stderr 尾部:")
            print(execution.stderr_tail)

    print("\n执行汇总：")
    print(format_summary(executions))

    payload = build_result_payload(selected, executions, args)
    suite_report_path = run_report_dir / "suite_report.json"
    saved_suite_report = save_result(payload, str(suite_report_path))
    print(f"\n统一套件报告: {saved_suite_report}")
    if args.report_path:
        saved = save_result(payload, args.report_path)
        print(f"\n已写入套件报告: {saved}")

    has_failed = any(item.status == "failed" for item in executions)
    return 1 if has_failed else 0


if __name__ == "__main__":
    raise SystemExit(main())
