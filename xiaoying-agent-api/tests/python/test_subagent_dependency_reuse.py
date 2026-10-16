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
    count_tool_calls,
    ensure_expected_file_parents,
    ensure_backend_ready,
    has_tool_argument_snippet,
    print_step,
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
    min_tool_counts: dict[str, int] = field(default_factory=dict)
    forbidden_tools: list[str] = field(default_factory=list)
    max_duration_seconds: int = 260


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="AuraMate 子智能体依赖链与复用覆盖测试")
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL)
    parser.add_argument("--model", default=DEFAULT_MODEL)
    parser.add_argument("--timeout", type=int, default=REQUEST_TIMEOUT_SECONDS)
    parser.add_argument("--report-path", default="")
    return parser.parse_args()


def build_scenarios(run_id: str) -> list[tuple[Scenario, Path]]:
    target_root = WORKSPACE_DIR / f"subagent_dependency_reuse_{run_id}"
    delegate_file = (REPO_ROOT / "backend" / "internal" / "llm" / "agent" / "delegate_tool.go").resolve()
    wait_file = (REPO_ROOT / "backend" / "internal" / "llm" / "agent" / "wait_worker_tool.go").resolve()
    tools_file = (REPO_ROOT / "backend" / "internal" / "llm" / "agent" / "tools.go").resolve()

    dep_root = target_root / "depends_on"
    dep_notes = dep_root / "notes"
    dep_a = dep_notes / "step_a.md"
    dep_b = dep_notes / "step_b.md"
    dep_c = dep_notes / "step_c.md"
    dep_report = dep_root / "depends_report.md"
    scenario_dep = Scenario(
        name="依赖链顺序执行",
        prompt=(
            "你必须调用一次 batch_delegate_task，并且 tool arguments 里的 tasks 数组必须显式包含 step_a、step_b、step_c 三个逻辑任务。"
            "严格要求如下：step_a 不带 depends_on；step_b 必须显式包含 depends_on:[\"step_a\"]；step_c 必须显式包含 depends_on:[\"step_b\"]。"
            "如果 batch_delegate_task 的 JSON 参数里没有真的写出 depends_on 字段，本任务视为失败。"
            f"step_a：读取绝对路径 {delegate_file}，提炼 3 条要点后写入 {dep_a}，标题必须是 `## step_a`。"
            f"step_b：读取绝对路径 {wait_file}，提炼 3 条要点后写入 {dep_b}，标题必须是 `## step_b`。"
            f"step_c：读取绝对路径 {tools_file}，提炼 3 条要点后写入 {dep_c}，标题必须是 `## step_c`。"
            "随后调用 wait_for_workers(wait_mode='all_finished')。"
            f"最后写入 {dep_report}，标题必须包含 `## 顺序验证` 和 `## 结论`。"
            "如果任一步骤失败，主智能体必须在同一轮本地补齐缺失文件，并在报告里说明补齐原因。"
            "最终只输出简短总结。"
        ),
        required_tools=["batch_delegate_task", "wait_for_workers"],
        expected_files=[
            ExpectedFile(dep_a, ["## step_a"]),
            ExpectedFile(dep_b, ["## step_b"]),
            ExpectedFile(dep_c, ["## step_c"]),
            ExpectedFile(dep_report, ["## 顺序验证", "## 结论"]),
        ],
        required_argument_snippets=[
            ("batch_delegate_task", '"depends_on"'),
            ("batch_delegate_task", '"step_a"'),
            ("batch_delegate_task", '"step_b"'),
            ("wait_for_workers", '"all_finished"'),
        ],
        max_duration_seconds=240,
    )

    reuse_root = target_root / "worker_reuse"
    reuse_round1 = reuse_root / "round1.md"
    reuse_round2 = reuse_root / "round2.md"
    reuse_report = reuse_root / "reuse_report.md"
    scenario_reuse = Scenario(
        name="worker复用多轮执行",
        prompt=(
            "你必须先调用 delegate_task，设置 async=true，role='analyst'，让 worker 读取一个文件后写入第一轮结果。"
            f"第一轮任务：读取绝对路径 {delegate_file}，提炼关键结构后写入 {reuse_round1}，标题包含 `## ROUND1` 和 `## 结论`。"
            "拿到 worker_id 后，必须立刻调用 manage_worker(action='status')。"
            "然后必须调用 manage_worker(action='wait', timeout=180)，先等待第一轮完成。"
            f"之后必须再次调用 delegate_task，并显式传入同一个 worker_id，第二轮任务读取绝对路径 {wait_file} 与 {tools_file}，提炼关键关系后写入 {reuse_round2}，标题包含 `## ROUND2` 和 `## 结论`。"
            "第二轮发送后必须再次调用 manage_worker(action='wait', timeout=180) 等待完成。"
            f"最后写入 {reuse_report}，标题包含 `## 复用证明` 和 `## 结论`，明确说明你复用了同一个 worker。"
            "如果复用失败，主智能体必须本地补写缺失文件。最终只输出简短总结。"
        ),
        required_tools=["delegate_task", "manage_worker"],
        expected_files=[
            ExpectedFile(reuse_round1, ["## ROUND1", "## 结论"]),
            ExpectedFile(reuse_round2, ["## ROUND2", "## 结论"]),
            ExpectedFile(reuse_report, ["## 复用证明", "## 结论"]),
        ],
        required_argument_snippets=[
            ("delegate_task", '"async":true'),
            ("manage_worker", '"status"'),
            ("manage_worker", '"wait"'),
            ("delegate_task", '"worker_id"'),
        ],
        min_tool_counts={"delegate_task": 2, "manage_worker": 3},
        max_duration_seconds=420,
    )

    artifact_root = target_root / "artifact_selection"
    artifact_source_report = artifact_root / "source_report.md"
    artifact_selected = artifact_root / "selected.md"
    artifact_ignored = artifact_root / "ignored.md"
    artifact_consumer_report = artifact_root / "consumer_report.md"
    selected_token = f"SELECTED-{run_id}"
    ignored_token = f"IGNORED-{run_id}"
    scenario_artifact_selection = Scenario(
        name="依赖产物精细选择",
        prompt=(
            "你必须且只能调用一次 batch_delegate_task，tasks 数组必须显式包含 source 与 consumer 两个逻辑任务。"
            "source 不带 depends_on；consumer 必须使用对象形式 depends_on，且明确写出 task_id、artifacts、include_summary、include_evidence 四个字段。"
            "consumer 的 depends_on 必须精确等于 [{\"task_id\":\"source\",\"artifacts\":[\"selected.md\"],\"include_summary\":false,\"include_evidence\":false}]。"
            "如果 batch_delegate_task 参数中没有真的写出这个对象结构，本场景直接判失败。"
            f"source 任务：在 {artifact_selected} 写入标题 `## SELECTED` 和标记 `{selected_token}`；"
            f"在 {artifact_ignored} 写入标题 `## IGNORED` 和标记 `{ignored_token}`；"
            f"然后在 {artifact_source_report} 写入标题 `## SOURCE`，并在正文逐字包含 {artifact_selected.name}、{artifact_ignored.name}、SOURCE-SUMMARY、SOURCE-EVIDENCE。"
            "source 任务最终回复必须使用 <worker_result>{...}</worker_result>，其中 status=completed，summary 必须包含 SOURCE-SUMMARY，"
            f"artifacts 必须同时包含 {artifact_selected} 与 {artifact_ignored}，evidence_refs 必须包含 SOURCE-EVIDENCE。"
            "consumer 任务禁止自行读取 source 生成的任何文件，也禁止重新扫描目录；只能根据注入的 dependency_context 写结果。"
            f"consumer 必须写入 {artifact_consumer_report}，并且文件内容必须严格限制为以下四行，不得添加任何其他说明、标题级别变化或解释文本："
            '第一行 `## CONSUMER`；第二行 `selected.md`；第三行 `SUMMARY=MISSING`；第四行 `EVIDENCE=MISSING`。'
            f"文件中不得出现 {artifact_ignored.name}、SOURCE-SUMMARY、SOURCE-EVIDENCE 这些字符串本身。"
            "随后调用 wait_for_workers(wait_mode='all_finished')。"
            "如果任一步骤失败，主智能体必须同轮补齐缺失文件并说明原因。最终只输出简短总结。"
        ),
        required_tools=["batch_delegate_task", "wait_for_workers"],
        forbidden_tools=["write"],
        expected_files=[
            ExpectedFile(artifact_selected, ["## SELECTED", selected_token]),
            ExpectedFile(artifact_ignored, ["## IGNORED", ignored_token]),
            ExpectedFile(artifact_source_report, ["## SOURCE", "SOURCE-SUMMARY", "SOURCE-EVIDENCE"]),
            ExpectedFile(
                artifact_consumer_report,
                ["## CONSUMER", artifact_selected.name, "SUMMARY=MISSING", "EVIDENCE=MISSING"],
                forbidden_snippets=[artifact_ignored.name, "SOURCE-SUMMARY", "SOURCE-EVIDENCE"],
            ),
        ],
        required_argument_snippets=[
            ("batch_delegate_task", '"task_id":"source"'),
            ("batch_delegate_task", '"artifacts":["selected.md"]'),
            ("batch_delegate_task", '"include_summary":false'),
            ("batch_delegate_task", '"include_evidence":false'),
            ("wait_for_workers", '"all_finished"'),
        ],
        max_duration_seconds=300,
    )

    return [
        (scenario_dep, dep_root),
        (scenario_reuse, reuse_root),
        (scenario_artifact_selection, artifact_root),
    ]


def run_scenario(base_url: str, model: str, scenario: Scenario, scenario_root: Path, timeout_seconds: int) -> dict[str, Any]:
    cleanup_path(scenario_root)
    scenario_root.mkdir(parents=True, exist_ok=True)
    ensure_expected_file_parents(scenario.expected_files)
    print_step(f"执行场景: {scenario.name}")
    raw = collect_stream_result(base_url, model, scenario.prompt, timeout_seconds)

    missing_tools = [tool for tool in scenario.required_tools if tool not in raw["tool_names"]]
    forbidden_tools = [tool for tool in scenario.forbidden_tools if tool in raw["tool_names"]]
    missing_files, invalid_files = verify_files(scenario.expected_files)
    missing_args = [f"{tool}:{snippet}" for tool, snippet in scenario.required_argument_snippets if not has_tool_argument_snippet(raw["tool_calls"], tool, snippet)]
    count_errors = []
    for tool_name, minimum in scenario.min_tool_counts.items():
        actual = count_tool_calls(raw["tool_calls"], tool_name)
        if actual < minimum:
            count_errors.append(f"{tool_name}<{minimum}, actual={actual}")
    duration_error = ""
    if raw["duration_seconds"] > scenario.max_duration_seconds:
        duration_error = f"耗时过长: {raw['duration_seconds']:.2f}s > {scenario.max_duration_seconds}s"

    success = not any([missing_tools, forbidden_tools, missing_files, invalid_files, missing_args, count_errors, duration_error])
    print(f"\n[INFO] TTFT={raw['ttft_seconds']:.2f}s Duration={raw['duration_seconds']:.2f}s")
    print(f"[INFO] Tools={raw['tool_names']}")
    if success:
        print(f"✅ 场景通过: {scenario.name}")
    else:
        print(f"❌ 场景失败: {scenario.name}")

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
        "forbidden_tools": forbidden_tools,
        "missing_files": missing_files,
        "invalid_files": invalid_files,
        "missing_args": missing_args,
        "count_errors": count_errors,
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
        if item["forbidden_tools"]:
            failure_reasons.append(f"{item['name']}: forbidden_tools={item['forbidden_tools']}")
        if item["missing_files"]:
            failure_reasons.append(f"{item['name']}: missing_files={item['missing_files']}")
        if item["invalid_files"]:
            failure_reasons.append(f"{item['name']}: invalid_files={item['invalid_files']}")
        if item["missing_args"]:
            failure_reasons.append(f"{item['name']}: missing_args={item['missing_args']}")
        if item["count_errors"]:
            failure_reasons.append(f"{item['name']}: count_errors={item['count_errors']}")
        if item["duration_error"]:
            failure_reasons.append(f"{item['name']}: {item['duration_error']}")

    report_path = Path(args.report_path) if args.report_path else REPORT_DIR / f"subagent_dependency_reuse_report_{run_id}.json"
    payload = {
        "passed": passed,
        "base_url": args.base_url,
        "model": args.model,
        "results": results,
        "diagnostics": [f"scenarios={len(results)}", f"passed={sum(1 for item in results if item['success'])}"],
        "failure_reasons": failure_reasons,
    }
    write_standard_report(report_path, payload)
    print_step("依赖链与复用覆盖汇总")
    print(report_path)
    return 0 if passed else 1


if __name__ == "__main__":
    sys.exit(main())