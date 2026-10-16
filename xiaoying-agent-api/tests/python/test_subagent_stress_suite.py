from __future__ import annotations

import argparse
import json
import os
import shutil
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
REPO_ROOT = Path(__file__).resolve().parents[3]
WORKSPACE_DIR = REPO_ROOT / "workspace"
REPORT_DIR = REPO_ROOT / "backend" / "tests" / "reports"


@dataclass
class ExpectedFile:
    path: Path
    snippets: list[str] = field(default_factory=list)


@dataclass
class Scenario:
    name: str
    prompt: str
    required_tools: list[str]
    expected_files: list[ExpectedFile]
    required_content_snippets: list[str] = field(default_factory=list)
    max_duration_seconds: int = 240
    ordered_tools: list[str] = field(default_factory=list)
    min_tool_calls: int = 0


@dataclass
class ScenarioResult:
    name: str
    iteration: int
    success: bool
    duration_seconds: float
    ttft_seconds: float
    prompt_tokens: int
    completion_tokens: int
    tool_calls: list[str]
    content: str
    missing_tools: list[str]
    missing_files: list[str]
    invalid_files: list[dict[str, str]]
    missing_content: list[str]
    ordering_error: str
    count_error: str
    duration_error: str


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="AuraMate 子智能体长任务压力测试")
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL, help="后端地址，例如 http://localhost:3000")
    parser.add_argument("--model", default=DEFAULT_MODEL, help="测试时使用的模型 ID")
    parser.add_argument("--iterations", type=int, default=2, help="每个场景重复次数")
    parser.add_argument("--timeout", type=int, default=REQUEST_TIMEOUT_SECONDS, help="单轮超时秒数")
    parser.add_argument("--report-path", default="", help="结构化报告输出路径")
    return parser.parse_args()


def ensure_backend_ready(base_url: str) -> None:
    response = requests.get(f"{base_url}/v1/models", timeout=10)
    response.raise_for_status()


def print_step(message: str) -> None:
    print(f"\n{'=' * 88}\n{message}\n{'=' * 88}")


def cleanup_path(path: Path) -> None:
    if path.exists():
        shutil.rmtree(path)


def ensure_expected_file_parents(expected_files: list[ExpectedFile]) -> None:
    for item in expected_files:
        item.path.parent.mkdir(parents=True, exist_ok=True)


def collect_stream_result(base_url: str, model: str, prompt: str, timeout_seconds: int) -> dict[str, Any]:
    url = f"{base_url}/v1/chat/completions"
    payload = {
        "model": model,
        "messages": [{"role": "user", "content": prompt}],
        "stream": True,
        "temperature": 0,
    }
    headers = {"Content-Type": "application/json"}

    start_time = time.time()
    first_token_time = None
    content_parts: list[str] = []
    tool_calls: list[str] = []
    usage = {"prompt_tokens": 0, "completion_tokens": 0}

    response = requests.post(
        url,
        headers=headers,
        data=json.dumps(payload, ensure_ascii=False),
        stream=True,
        timeout=(30, timeout_seconds),
    )
    response.raise_for_status()

    try:
        for line in response.iter_lines():
            if not line:
                continue
            decoded_line = line.decode("utf-8")
            if not decoded_line.startswith("data: "):
                continue
            json_str = decoded_line[6:]
            if json_str == "[DONE]":
                break
            try:
                chunk = json.loads(json_str)
            except json.JSONDecodeError:
                continue

            raw_usage = chunk.get("usage") or {}
            usage["prompt_tokens"] = int(raw_usage.get("prompt_tokens", usage["prompt_tokens"]) or usage["prompt_tokens"])
            usage["completion_tokens"] = int(raw_usage.get("completion_tokens", usage["completion_tokens"]) or usage["completion_tokens"])

            choices = chunk.get("choices") or []
            if not choices:
                continue
            delta = choices[0].get("delta") or {}

            text = delta.get("content")
            if text:
                if first_token_time is None:
                    first_token_time = time.time()
                content_parts.append(text)
                print(text, end="", flush=True)

            for tool_call in delta.get("tool_calls") or []:
                function_data = tool_call.get("function") or {}
                tool_name = function_data.get("name")
                if tool_name:
                    tool_calls.append(tool_name)
                    print(f"\n[TOOL CALL] {tool_name}")
    except (requests.exceptions.ConnectionError, requests.exceptions.ReadTimeout) as exc:
        if not content_parts and not tool_calls:
            raise
        print(f"\n[WARN] 流式响应提前结束，使用已收集内容继续评估: {exc}", flush=True)

    end_time = time.time()
    return {
        "content": "".join(content_parts),
        "tool_calls": tool_calls,
        "ttft_seconds": round((first_token_time - start_time) if first_token_time else 0, 4),
        "duration_seconds": round(end_time - start_time, 4),
        "prompt_tokens": usage["prompt_tokens"],
        "completion_tokens": usage["completion_tokens"],
    }


def verify_files(expected_files: list[ExpectedFile]) -> tuple[list[str], list[dict[str, str]]]:
    missing: list[str] = []
    invalid: list[dict[str, str]] = []
    for item in expected_files:
        if not item.path.exists():
            missing.append(str(item.path))
            continue
        content = item.path.read_text(encoding="utf-8")
        for snippet in item.snippets:
            if snippet not in content:
                invalid.append({"path": str(item.path), "snippet": snippet})
    return missing, invalid


def verify_scenario_specific_constraints(scenario: Scenario) -> list[dict[str, str]]:
    invalid: list[dict[str, str]] = []
    if scenario.name == "主子并行交错执行":
        checklist_path = next((item.path for item in scenario.expected_files if item.path.name == "main_checklist.md"), None)
        if checklist_path is not None and checklist_path.exists():
            content = checklist_path.read_text(encoding="utf-8")
            if content.count("验证项") < 3:
                invalid.append({"path": str(checklist_path), "snippet": "至少 3 个验证项"})
    return invalid


def validate_order(tool_calls: list[str], ordered_tools: list[str]) -> str:
    if not ordered_tools:
        return ""
    position = 0
    for tool in tool_calls:
        if tool == ordered_tools[position]:
            position += 1
            if position == len(ordered_tools):
                return ""
    return f"期望工具顺序 {ordered_tools}，实际 {tool_calls}"


def evaluate_result(scenario: Scenario, iteration: int, raw_result: dict[str, Any]) -> ScenarioResult:
    missing_tools = [tool for tool in scenario.required_tools if tool not in raw_result["tool_calls"]]
    missing_files, invalid_files = verify_files(scenario.expected_files)
    invalid_files.extend(verify_scenario_specific_constraints(scenario))
    missing_content = [snippet for snippet in scenario.required_content_snippets if snippet not in raw_result["content"]]
    ordering_error = validate_order(raw_result["tool_calls"], scenario.ordered_tools)
    count_error = ""
    if scenario.min_tool_calls and len(raw_result["tool_calls"]) < scenario.min_tool_calls:
        count_error = f"工具调用过少: 期望至少 {scenario.min_tool_calls}，实际 {len(raw_result['tool_calls'])}"
    duration_error = ""
    if scenario.max_duration_seconds and raw_result["duration_seconds"] > scenario.max_duration_seconds:
        duration_error = (
            f"耗时过长: {raw_result['duration_seconds']:.2f}s > {scenario.max_duration_seconds}s"
        )
    success = not any(
        [missing_tools, missing_files, invalid_files, missing_content, ordering_error, count_error, duration_error]
    )
    return ScenarioResult(
        name=scenario.name,
        iteration=iteration,
        success=success,
        duration_seconds=raw_result["duration_seconds"],
        ttft_seconds=raw_result["ttft_seconds"],
        prompt_tokens=raw_result["prompt_tokens"],
        completion_tokens=raw_result["completion_tokens"],
        tool_calls=raw_result["tool_calls"],
        content=raw_result["content"],
        missing_tools=missing_tools,
        missing_files=missing_files,
        invalid_files=invalid_files,
        missing_content=missing_content,
        ordering_error=ordering_error,
        count_error=count_error,
        duration_error=duration_error,
    )


def build_scenarios(run_id: str) -> list[tuple[Scenario, Path]]:
    target_root = WORKSPACE_DIR / f"subagent_stress_{run_id}"
    tools_file = (REPO_ROOT / "backend" / "internal" / "llm" / "agent" / "tools.go").resolve()
    delegate_file = (REPO_ROOT / "backend" / "internal" / "llm" / "agent" / "delegate_tool.go").resolve()
    wait_file = (REPO_ROOT / "backend" / "internal" / "llm" / "agent" / "wait_worker_tool.go").resolve()
    orchestrator_file = (REPO_ROOT / "backend" / "internal" / "llm" / "agent" / "multi_agent_orchestrator.go").resolve()

    scenario_a_root = target_root / "fanout"
    scenario_a_notes = scenario_a_root / "notes"
    report_a = scenario_a_root / "fanout_report.md"
    note_tools = scenario_a_notes / "tools_summary.md"
    note_delegate = scenario_a_notes / "delegate_summary.md"
    note_wait = scenario_a_notes / "wait_summary.md"
    scenario_a = Scenario(
        name="并行扇出代码审计",
        prompt=(
            "你必须用 batch_delegate_task 并行启动三个子智能体，且三个子任务互相独立。"
            "不要在 batch_delegate_task 之前调用 read、ls、run_command、manage_worker 或其他预检查工具，三个子任务自己读取源文件。"
            "batch_delegate_task 之后，下一条协调工具必须是 wait_for_workers(wait_mode='all_finished')，禁止用 manage_worker、run_command 或 Python 脚本代替批量等待。"
            f"子任务1：读取绝对路径 {tools_file}，写入 {note_tools}，并且必须使用精确章节标题 `## 标题`、`## 子智能体工具名`、`## 风险`。如果没有逐字写出 `## 标题` 这一行，就视为失败，不能用其他近似标题替代。"
            f"子任务2：读取绝对路径 {delegate_file}，写入 {note_delegate}，并且必须使用精确章节标题 `## 适用场景`、`## 异步行为`、`## 注意事项`。"
            f"子任务3：读取绝对路径 {wait_file}，写入 {note_wait}，并且必须使用精确章节标题 `## 等待模式`、`## 超时处理`、`## 失败回退`。"
            f"然后必须调用 wait_for_workers(wait_mode='all_finished')，最后由主智能体写入 {report_a}，并且使用精确章节标题 `## tools_summary`、`## delegate_summary`、`## wait_summary`、`## 总评` 汇总三份笔记。"
            "如果任一子智能体失败，或者虽然写出了文件但没有满足精确章节标题，主智能体都必须在同一轮直接补齐或重写该文件。最终仅输出简短总结。"
        ),
        required_tools=["batch_delegate_task", "wait_for_workers"],
        expected_files=[
            ExpectedFile(note_tools, ["## 标题", "## 子智能体工具名", "## 风险"]),
            ExpectedFile(note_delegate, ["## 适用场景", "## 异步行为", "## 注意事项"]),
            ExpectedFile(note_wait, ["## 等待模式", "## 超时处理", "## 失败回退"]),
            ExpectedFile(report_a, ["## tools_summary", "## delegate_summary", "## wait_summary", "## 总评"]),
        ],
        max_duration_seconds=220,
        min_tool_calls=2,
    )

    scenario_b_root = target_root / "overlap"
    checklist_b = scenario_b_root / "main_checklist.md"
    metadata_b = scenario_b_root / "main_metadata.json"
    worker_summary_b = scenario_b_root / "worker_orchestrator_summary.md"
    report_b = scenario_b_root / "overlap_report.md"
    scenario_b = Scenario(
        name="主子并行交错执行",
        prompt=(
            "你必须先调用 delegate_task，设置 async=true，role='analyst'。"
            "第一条 tool call 必须是 delegate_task；在这次 delegate_task 之前，禁止调用 write、read、ls、run_command 或其他工具。"
            f"委派任务：读取绝对路径 {orchestrator_file}，在 {worker_summary_b} 中写一份摘要，且必须使用精确章节标题 `## 生命周期`、`## 等待机制`、`## 清理机制`。"
            f"拿到 worker_id 之后、等待该 worker 完成之前，主智能体必须立刻自己写入 {checklist_b} 和 {metadata_b}。"
            f"其中 {checklist_b} 必须包含精确条目 `- 验证项1`、`- 验证项2`、`- 验证项3`，{metadata_b} 必须是 JSON 且包含键 scenario、mode、requires_wait。"
            "完成这两个本地文件后，再调用 manage_worker(action='wait')。"
            f"最后写入 {report_b}，并使用精确章节标题 `## 本地准备`、`## worker`、`## 等待`、`## 结论` 合并主智能体和子智能体的结果，同时明确说明你是在 worker 运行期间先完成了本地准备。"
            "如果 worker 失败，主智能体必须直接补写缺失摘要文件。最终仅输出简短总结。"
        ),
        required_tools=["delegate_task", "manage_worker"],
        expected_files=[
            ExpectedFile(checklist_b, ["验证项"]),
            ExpectedFile(metadata_b, ['"scenario"', '"mode"', '"requires_wait"']),
            ExpectedFile(worker_summary_b, ["## 生命周期", "## 等待机制", "## 清理机制"]),
            ExpectedFile(report_b, ["## 本地准备", "## worker", "## 等待", "## 结论"]),
        ],
        required_content_snippets=["delegate_task", "manage_worker"],
        max_duration_seconds=220,
        ordered_tools=["delegate_task", "write", "manage_worker"],
        min_tool_calls=3,
    )

    scenario_c_root = target_root / "recovery"
    recovery_notes = scenario_c_root / "notes"
    ok_one = recovery_notes / "ok_delegate.md"
    ok_two = recovery_notes / "ok_wait.md"
    repaired = recovery_notes / "fallback_repair.md"
    report_c = scenario_c_root / "recovery_report.md"
    missing_path = (REPO_ROOT / "backend" / "internal" / "llm" / "agent" / "definitely_missing_file.go").resolve()
    scenario_c = Scenario(
        name="失败恢复与本地补位",
        prompt=(
            "你必须使用 batch_delegate_task 并行启动三个子智能体。"
            "batch_delegate_task 之后必须直接调用 wait_for_workers(wait_mode='all_finished')，禁止改用 manage_worker 逐个轮询、run_command 或额外脚本等待。"
            f"子任务1：读取不存在的绝对路径 {missing_path}，尝试写入 {repaired}，如果失败就明确说明失败原因。"
            f"子任务2：读取绝对路径 {delegate_file}，写入 {ok_one}，并且必须使用精确章节标题 `## 目的`、`## 参数`、`## 注意点`。"
            f"子任务3：读取绝对路径 {wait_file}，写入 {ok_two}，并且必须使用精确章节标题 `## 超时`、`## 诊断`、`## 回退`。"
            "随后必须调用 wait_for_workers(wait_mode='all_finished')。"
            f"如果 {repaired}、{ok_one}、{ok_two} 任一文件缺失，主智能体必须立刻在同一轮本地补齐，不要额外做 ls、manage_worker 或二次调查。"
            f"其中 {repaired} 必须使用精确章节标题 `## 失败原因`、`## 补位策略`、`## 最终状态`；如果子任务1自行成功生成，也必须满足这三个标题。"
            f"最后写入 {report_c}，并且使用精确章节标题 `## 成功`、`## 失败`、`## 接管` 总结哪些任务成功、哪些失败、主智能体如何接管。最终仅输出简短总结。"
        ),
        required_tools=["batch_delegate_task", "wait_for_workers", "write"],
        expected_files=[
            ExpectedFile(ok_one, ["## 目的", "## 参数", "## 注意点"]),
            ExpectedFile(ok_two, ["## 超时", "## 诊断", "## 回退"]),
            ExpectedFile(repaired, ["## 失败原因", "## 补位策略", "## 最终状态"]),
            ExpectedFile(report_c, ["## 成功", "## 失败", "## 接管"]),
        ],
        required_content_snippets=["失败", "补位"],
        max_duration_seconds=240,
        min_tool_calls=3,
    )

    return [
        (scenario_a, scenario_a_root),
        (scenario_b, scenario_b_root),
        (scenario_c, scenario_c_root),
    ]


def run_scenario(base_url: str, model: str, scenario: Scenario, scenario_root: Path, iteration: int, timeout_seconds: int) -> ScenarioResult:
    cleanup_path(scenario_root)
    scenario_root.mkdir(parents=True, exist_ok=True)
    ensure_expected_file_parents(scenario.expected_files)
    print_step(f"[迭代 {iteration}] 执行场景: {scenario.name}")
    raw_result = collect_stream_result(base_url, model, scenario.prompt, timeout_seconds)
    result = evaluate_result(scenario, iteration, raw_result)

    print(f"\n[INFO] TTFT={result.ttft_seconds:.2f}s Duration={result.duration_seconds:.2f}s")
    print(f"[INFO] Tokens In={result.prompt_tokens} Out={result.completion_tokens}")
    print(f"[INFO] Tools={result.tool_calls}")
    if result.success:
        print(f"✅ 场景通过: {scenario.name}")
    else:
        print(f"❌ 场景失败: {scenario.name}")
        if result.missing_tools:
            print(f"  缺失工具: {result.missing_tools}")
        if result.missing_files:
            print(f"  缺失文件: {result.missing_files}")
        if result.invalid_files:
            print(f"  文件内容缺失: {result.invalid_files}")
        if result.missing_content:
            print(f"  输出缺失内容: {result.missing_content}")
        if result.ordering_error:
            print(f"  顺序问题: {result.ordering_error}")
        if result.count_error:
            print(f"  调用数量问题: {result.count_error}")
        if result.duration_error:
            print(f"  时延问题: {result.duration_error}")
    return result


def summarize(results: list[ScenarioResult]) -> dict[str, Any]:
    grouped: dict[str, list[ScenarioResult]] = {}
    for item in results:
        grouped.setdefault(item.name, []).append(item)

    scenario_summary: dict[str, Any] = {}
    recommendations: list[str] = []
    for name, items in grouped.items():
        passes = sum(1 for item in items if item.success)
        avg_duration = round(sum(item.duration_seconds for item in items) / len(items), 4)
        avg_ttft = round(sum(item.ttft_seconds for item in items) / len(items), 4)
        scenario_summary[name] = {
            "passes": passes,
            "runs": len(items),
            "pass_rate": round(passes / len(items), 4),
            "avg_duration_seconds": avg_duration,
            "avg_ttft_seconds": avg_ttft,
        }
        if passes < len(items):
            recommendations.append(f"{name}: 通过率不足，需要继续分析失败样本与工具顺序。")
        if avg_duration > 180:
            recommendations.append(f"{name}: 平均耗时偏高，建议继续压缩 wait 路径和 worker 输出。")

    if not recommendations:
        recommendations.append("当前三类复杂长任务均稳定通过，下一步应继续扩大 fan-out 数量和迭代次数。")

    return {
        "total_runs": len(results),
        "passed_runs": sum(1 for item in results if item.success),
        "scenario_summary": scenario_summary,
        "recommendations": recommendations,
    }


def write_report(run_id: str, base_url: str, model: str, results: list[ScenarioResult], summary: dict[str, Any], report_path: str = "") -> Path:
    REPORT_DIR.mkdir(parents=True, exist_ok=True)
    target = Path(report_path) if report_path else REPORT_DIR / f"subagent_stress_report_{run_id}.json"
    target.parent.mkdir(parents=True, exist_ok=True)
    payload = {
        "run_id": run_id,
        "base_url": base_url,
        "model": model,
        "summary": summary,
        "results": [asdict(item) for item in results],
    }
    target.write_text(json.dumps(payload, ensure_ascii=False, indent=2), encoding="utf-8")
    return target


def main() -> int:
    args = parse_args()
    ensure_backend_ready(args.base_url)
    WORKSPACE_DIR.mkdir(parents=True, exist_ok=True)
    run_id = uuid.uuid4().hex[:8]

    results: list[ScenarioResult] = []
    scenarios = build_scenarios(run_id)
    for iteration in range(1, args.iterations + 1):
        for scenario, scenario_root in scenarios:
            results.append(run_scenario(args.base_url, args.model, scenario, scenario_root, iteration, args.timeout))

    summary = summarize(results)
    report_path = write_report(run_id, args.base_url, args.model, results, summary, args.report_path)

    print_step("压力测试汇总")
    print(json.dumps(summary, ensure_ascii=False, indent=2))
    print(f"\n[REPORT] {report_path}")

    return 0 if summary["passed_runs"] == summary["total_runs"] else 1


if __name__ == "__main__":
    sys.exit(main())
