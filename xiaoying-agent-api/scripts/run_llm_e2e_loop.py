import argparse
import json
import subprocess
import sys
from dataclasses import dataclass
from pathlib import Path


@dataclass
class RoundResult:
    round_index: int
    report_path: Path
    passed: int
    failed: int
    failed_scenarios: list[dict]


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="循环运行 go-word LLM e2e 评测，并按失败场景自动缩小重测范围。")
    parser.add_argument("--backend-dir", default=str(Path(__file__).resolve().parents[1]), help="backend 目录路径")
    parser.add_argument("--model", required=True, help="LLM e2e 模型 ID，例如 ark-code-latest")
    parser.add_argument("--timeout", type=int, default=180, help="单场景 e2e 超时秒数")
    parser.add_argument("--scenario", default="", help="初始场景过滤关键字，多个用逗号分隔")
    parser.add_argument("--max-rounds", type=int, default=3, help="最多循环轮数")
    parser.add_argument("--artifacts-root", default="workspace/goword-agent-eval-loop", help="循环评测产物根目录，相对 backend")
    parser.add_argument("--keep-going", action="store_true", help="即使失败集合未变化，也继续跑满轮数")
    return parser.parse_args()


def run_eval(backend_dir: Path, model: str, timeout_seconds: int, scenario_filter: str, round_index: int, artifacts_root: Path) -> RoundResult:
    round_dir = artifacts_root / f"round_{round_index:02d}"
    round_dir.mkdir(parents=True, exist_ok=True)
    report_path = round_dir / "report.json"

    command = [
        "go",
        "run",
        "./scripts/goword_agent_eval",
        "--e2e-model",
        model,
        "--e2e-timeout",
        str(timeout_seconds),
        "--artifacts",
        str(round_dir),
        "--report",
        str(report_path),
    ]
    if scenario_filter.strip():
        command.extend(["--llm-scenario", scenario_filter.strip()])

    print(f"\n=== Round {round_index} ===")
    print("Command:", " ".join(command))
    completed = subprocess.run(command, cwd=backend_dir, text=True)
    if not report_path.exists():
        raise RuntimeError(f"round {round_index} 未生成报告: {report_path}")

    report = json.loads(report_path.read_text(encoding="utf-8"))
    failed_scenarios = [
        {
            "name": scenario.get("name", ""),
            "message": scenario.get("message", ""),
            "error": scenario.get("error", ""),
        }
        for scenario in report.get("scenarios", [])
        if not scenario.get("passed", False)
    ]
    print(f"Round {round_index} result: passed={report.get('passed', 0)} failed={report.get('failed', 0)} exit={completed.returncode}")
    for scenario in failed_scenarios:
        message = scenario["error"] or scenario["message"]
        print(f"- FAIL {scenario['name']}: {message}")

    return RoundResult(
        round_index=round_index,
        report_path=report_path,
        passed=int(report.get("passed", 0)),
        failed=int(report.get("failed", 0)),
        failed_scenarios=failed_scenarios,
    )


def failure_signature(result: RoundResult) -> list[tuple[str, str]]:
    signature = []
    for scenario in result.failed_scenarios:
        signature.append((scenario["name"], scenario["error"] or scenario["message"]))
    signature.sort()
    return signature


def main() -> int:
    args = parse_args()
    backend_dir = Path(args.backend_dir).resolve()
    artifacts_root = (backend_dir / args.artifacts_root).resolve()
    artifacts_root.mkdir(parents=True, exist_ok=True)

    round_results: list[RoundResult] = []
    scenario_filter = args.scenario
    previous_signature: list[tuple[str, str]] | None = None

    for round_index in range(1, args.max_rounds + 1):
        result = run_eval(backend_dir, args.model, args.timeout, scenario_filter, round_index, artifacts_root)
        round_results.append(result)

        if result.failed == 0:
            break

        current_signature = failure_signature(result)
        if previous_signature == current_signature and not args.keep_going:
            print("Failure set unchanged; stop early to avoid empty retries.")
            break

        scenario_filter = ",".join(scenario["name"] for scenario in result.failed_scenarios if scenario["name"])
        previous_signature = current_signature

    summary = {
        "model": args.model,
        "round_count": len(round_results),
        "all_passed": bool(round_results and round_results[-1].failed == 0),
        "rounds": [
            {
                "round": result.round_index,
                "report_path": str(result.report_path),
                "passed": result.passed,
                "failed": result.failed,
                "failed_scenarios": result.failed_scenarios,
            }
            for result in round_results
        ],
    }
    summary_path = artifacts_root / "loop-summary.json"
    summary_path.write_text(json.dumps(summary, ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"\nSummary written to: {summary_path}")
    return 0 if summary["all_passed"] else 1


if __name__ == "__main__":
    sys.exit(main())