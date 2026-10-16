from __future__ import annotations

import argparse
import sys
import uuid
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any

from subagent_test_lib import (
    DEFAULT_BASE_URL,
    DEFAULT_MODEL,
    REPORT_DIR,
    REQUEST_TIMEOUT_SECONDS,
    REPO_ROOT,
    WORKSPACE_DIR,
    ExpectedFile,
    cleanup_path,
    collect_stream_result,
    ensure_expected_file_parents,
    ensure_backend_ready,
    has_tool_argument_snippet,
    print_step,
    validate_order,
    verify_files,
    write_standard_report,
)


@dataclass
class Scenario:
    name: str
    prompt: str
    required_tools: list[str]
    expected_files: list[ExpectedFile]
    required_argument_snippets: list[tuple[str, str]] = field(default_factory=list)
    ordered_tools: list[str] = field(default_factory=list)
    max_duration_seconds: int = 600


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="AuraMate 子智能体并行模式覆盖测试")
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL)
    parser.add_argument("--model", default=DEFAULT_MODEL)
    parser.add_argument("--timeout", type=int, default=REQUEST_TIMEOUT_SECONDS)
    parser.add_argument("--report-path", default="")
    return parser.parse_args()


def build_scenarios(run_id: str) -> list[tuple[Scenario, Path]]:
    target_root = WORKSPACE_DIR / f"subagent_parallel_modes_{run_id}"
    tools_file = (REPO_ROOT / "backend" / "internal" / "llm" / "agent" / "tools.go").resolve()
    wait_file = (REPO_ROOT / "backend" / "internal" / "llm" / "agent" / "wait_worker_tool.go").resolve()
    orchestrator_file = (REPO_ROOT / "backend" / "internal" / "llm" / "agent" / "multi_agent_orchestrator.go").resolve()

    scale_root = target_root / "parallel_scale"
    scale_notes = scale_root / "notes"
    scale_report = scale_root / "parallel_scale_report.md"
    scale_files = [scale_notes / f"scale_{name}.md" for name in ("a", "b", "c", "d")]
    scenario_scale = Scenario(
        name="四路并行聚合",
        prompt=(
            "你必须使用 batch_delegate_task 并行启动四个子智能体。"
            f"任务A：读取绝对路径 {tools_file}，写入 {scale_files[0]}，使用标题 `## A` 和 `## 结论`。"
            f"任务B：读取绝对路径 {wait_file}，写入 {scale_files[1]}，使用标题 `## B` 和 `## 结论`。"
            f"任务C：读取绝对路径 {orchestrator_file}，写入 {scale_files[2]}，使用标题 `## C` 和 `## 结论`。"
            f"任务D：读取绝对路径 {tools_file} 和 {wait_file}，写入 {scale_files[3]}，使用标题 `## D` 和 `## 结论`。"
            "随后必须调用 wait_for_workers(wait_mode='all_finished')。"
            f"最后主智能体写入 {scale_report}，使用标题 `## 汇总` 和 `## 速度评估`。"
            "如果任一任务失败，必须在同一轮本地补齐缺失文件。最终只输出简短总结。"
        ),
        required_tools=["batch_delegate_task", "wait_for_workers", "write"],
        expected_files=[
            ExpectedFile(scale_files[0], ["## A", "## 结论"]),
            ExpectedFile(scale_files[1], ["## B", "## 结论"]),
            ExpectedFile(scale_files[2], ["## C", "## 结论"]),
            ExpectedFile(scale_files[3], ["## D", "## 结论"]),
            ExpectedFile(scale_report, ["## 汇总", "## 速度评估"]),
        ],
        required_argument_snippets=[("wait_for_workers", '"all_finished"')],
        max_duration_seconds=600,
    )

    race_root = target_root / "wait_modes"
    race_report = race_root / "wait_modes_report.md"
    fast_file = race_root / "fast_result.md"
    slow_file = race_root / "slow_result.md"
    scenario_race = Scenario(
        name="竞速等待双阶段回收",
        prompt=(
            "你必须使用 batch_delegate_task 启动两个子智能体。"
            f"快任务：创建 {fast_file}，使用标题 `## FAST` 和 `## DONE`。"
            f"慢任务：读取绝对路径 {orchestrator_file}，再写入 {slow_file}，使用标题 `## SLOW` 和 `## DONE`。"
            "启动后必须先调用 wait_for_workers(wait_mode='any_finished') 获取首个完成结果，再调用 wait_for_workers(wait_mode='all_finished') 回收剩余结果。"
            f"最后写入 {race_report}，使用标题 `## first_finished`、`## final_merge`、`## 结论`。"
            "最终只输出简短总结。"
        ),
        required_tools=["batch_delegate_task", "wait_for_workers", "write"],
        expected_files=[
            ExpectedFile(fast_file, ["## FAST", "## DONE"]),
            ExpectedFile(slow_file, ["## SLOW", "## DONE"]),
            ExpectedFile(race_report, ["## first_finished", "## final_merge", "## 结论"]),
        ],
        required_argument_snippets=[("wait_for_workers", '"any_finished"'), ("wait_for_workers", '"all_finished"')],
        ordered_tools=["batch_delegate_task", "wait_for_workers", "wait_for_workers", "write"],
        max_duration_seconds=360,
    )

    return [(scenario_scale, scale_root), (scenario_race, race_root)]


def run_scenario(base_url: str, model: str, scenario: Scenario, scenario_root: Path, timeout_seconds: int) -> dict[str, Any]:
    cleanup_path(scenario_root)
    scenario_root.mkdir(parents=True, exist_ok=True)
    ensure_expected_file_parents(scenario.expected_files)
    print_step(f"执行场景: {scenario.name}")
    raw = collect_stream_result(base_url, model, scenario.prompt, timeout_seconds)

    missing_tools = [tool for tool in scenario.required_tools if tool not in raw["tool_names"]]
    missing_files, invalid_files = verify_files(scenario.expected_files)
    missing_args = [f"{tool}:{snippet}" for tool, snippet in scenario.required_argument_snippets if not has_tool_argument_snippet(raw["tool_calls"], tool, snippet)]
    ordering_error = validate_order(raw["tool_names"], scenario.ordered_tools)
    duration_error = ""
    if raw["duration_seconds"] > scenario.max_duration_seconds:
        duration_error = f"耗时过长: {raw['duration_seconds']:.2f}s > {scenario.max_duration_seconds}s"

    success = not any([missing_tools, missing_files, invalid_files, missing_args, ordering_error, duration_error])
    print(f"\n[INFO] TTFT={raw['ttft_seconds']:.2f}s Duration={raw['duration_seconds']:.2f}s")
    print(f"[INFO] Tools={raw['tool_names']}")
    if success:
        print(f"[PASS] 场景通过: {scenario.name}")
    else:
        print(f"[FAIL] 场景失败: {scenario.name}")

    return {
        "name": scenario.name,
        "success": success,
        "tool_names": raw["tool_names"],
        "tool_calls": [asdict(item) for item in raw["tool_calls"]],
        "duration_seconds": raw["duration_seconds"],
        "ttft_seconds": raw["ttft_seconds"],
        "prompt_tokens": raw["prompt_tokens"],
        "completion_tokens": raw["completion_tokens"],
        "missing_tools": missing_tools,
        "missing_files": missing_files,
        "invalid_files": invalid_files,
        "missing_args": missing_args,
        "ordering_error": ordering_error,
        "duration_error": duration_error,
        "content": raw["content"],
    }


def main() -> int:
    args = parse_args()
    ensure_backend_ready(args.base_url)
    run_id = uuid.uuid4().hex[:8]
    results = []
    for scenario, root in build_scenarios(run_id):
        results.append(run_scenario(args.base_url, args.model, scenario, root, args.timeout))

    passed = all(item["success"] for item in results)
    failure_reasons: list[str] = []
    for item in results:
        if item["missing_tools"]:
            failure_reasons.append(f"{item['name']}: missing_tools={item['missing_tools']}")
        if item["missing_args"]:
            failure_reasons.append(f"{item['name']}: missing_args={item['missing_args']}")
        if item["ordering_error"]:
            failure_reasons.append(f"{item['name']}: {item['ordering_error']}")
        if item["duration_error"]:
            failure_reasons.append(f"{item['name']}: {item['duration_error']}")

    report_path = Path(args.report_path) if args.report_path else REPORT_DIR / f"subagent_parallel_modes_report_{run_id}.json"
    payload = {
        "passed": passed,
        "base_url": args.base_url,
        "model": args.model,
        "results": results,
        "diagnostics": [f"scenarios={len(results)}", f"passed={sum(1 for item in results if item['success'])}"],
        "failure_reasons": failure_reasons,
    }
    write_standard_report(report_path, payload)
    print_step("并行模式覆盖汇总")
    print(report_path)
    return 0 if passed else 1


if __name__ == "__main__":
    sys.exit(main())
