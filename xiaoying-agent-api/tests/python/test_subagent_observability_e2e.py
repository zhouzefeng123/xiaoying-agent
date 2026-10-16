from __future__ import annotations

import argparse
import sys
import threading
import time
import uuid
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any

import requests

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
	ensure_backend_ready,
	ensure_expected_file_parents,
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
	worker_role: str
	required_tools: list[str]
	expected_files: list[ExpectedFile]
	required_argument_snippets: list[tuple[str, str]] = field(default_factory=list)
	forbidden_argument_snippets: list[tuple[str, str]] = field(default_factory=list)
	ordered_tools: list[str] = field(default_factory=list)
	forbidden_tools: list[str] = field(default_factory=list)
	min_manage_worker_calls: int = 1
	max_delegate_task_calls: int = 1
	min_retry_count: int = 0
	require_recovery_note: bool = False
	require_structured_summary: bool = True
	external_wait_via_snapshots: bool = False
	external_wait_timeout_seconds: int = 0
	accepted_terminal_statuses: list[str] = field(default_factory=lambda: ["completed"])
	allow_missing_primary_artifact_on_failed_terminal: bool = False
	max_duration_seconds: int = 420


@dataclass
class SnapshotObservation:
	matched_worker_role: str
	matched_worker_id: str = ""
	status_sequence: list[str] = field(default_factory=list)
	total_samples: int = 0
	matching_samples: int = 0
	max_summary_total: int = 0
	max_summary_running: int = 0
	max_summary_archived: int = 0
	observed_retry_count: int = 0
	observed_structured_summary: bool = False
	observed_result_preview: bool = False
	observed_recovery_note: bool = False
	last_recovery_note: str = ""
	last_structured_summary: str = ""
	last_matching_snapshot: dict[str, Any] = field(default_factory=dict)


def parse_args() -> argparse.Namespace:
	parser = argparse.ArgumentParser(description="AuraMate 子智能体 snapshots 可观测性端到端测试")
	parser.add_argument("--base-url", default=DEFAULT_BASE_URL)
	parser.add_argument("--model", default=DEFAULT_MODEL)
	parser.add_argument("--timeout", type=int, default=REQUEST_TIMEOUT_SECONDS)
	parser.add_argument("--scenario", choices=("all", "baseline", "recovery"), default="all")
	parser.add_argument("--report-path", default="")
	return parser.parse_args()


def build_scenarios(run_id: str) -> list[tuple[Scenario, Path]]:
	target_root = WORKSPACE_DIR / f"subagent_observability_{run_id}"

	baseline_role = f"snapshot_analyst_{run_id}"
	baseline_root = target_root / "baseline"
	baseline_worker_note = baseline_root / "worker_snapshot_note.md"
	baseline_local_prep = baseline_root / "local_prep.md"
	baseline_final_report = baseline_root / "final_report.md"
	baseline = Scenario(
		name="异步委派 snapshots 可观测性",
		worker_role=baseline_role,
		prompt=(
			"你必须严格按以下顺序执行，且不要省略工具调用。"
			f"第一步：调用 delegate_task，必须设置 async=true，role='{baseline_role}'。"
			f"委派任务内容：先调用 run_command 执行 `powershell -NoProfile -Command \"Start-Sleep -Seconds 5\"`，"
			f"然后直接写入 {baseline_worker_note}。该文件所在目录已经存在，不要创建目录。"
			f"该文件必须包含精确标题 `## 运行态`、`## 恢复策略`、`## 快照字段`，并且正文中必须出现字符串 `{run_id}`。"
			f"第二步：在不等待 worker 完成的前提下，主智能体立刻自己写入 {baseline_local_prep}，"
			f"内容必须包含 `run_id={run_id}`、`worker_role={baseline_role}`、`phase=pre-wait`。"
			"第三步：调用 manage_worker(action='wait', timeout=300) 等待该 worker 完成。"
			"如果第一次 wait 仍然超时，你必须再次调用 manage_worker(action='wait', timeout=300) 继续等待，直到 worker completed。"
			"在 worker completed 之前，禁止调用 read 检查 worker 文件，也禁止提前补写 worker 结果文件。"
			f"第四步：主智能体写入 {baseline_final_report}，必须包含精确标题 `## local_prep`、`## worker_result`、`## observability`、`## 结论`，"
			f"并在正文中包含 `{baseline_role}` 与 `{run_id}`。"
			"只有在明确收到 worker completed 且结果文件仍缺失时，主智能体才允许补写缺失文件。最终只输出简短总结。"
		),
		required_tools=["delegate_task", "manage_worker", "write"],
		expected_files=[
			ExpectedFile(baseline_worker_note, ["## 运行态", "## 恢复策略", "## 快照字段", run_id]),
			ExpectedFile(baseline_local_prep, [f"run_id={run_id}", f"worker_role={baseline_role}", "phase=pre-wait"]),
			ExpectedFile(baseline_final_report, ["## local_prep", "## worker_result", "## observability", "## 结论", baseline_role, run_id]),
		],
		required_argument_snippets=[
			("delegate_task", '"async":true'),
			("delegate_task", baseline_role),
			("manage_worker", '"action":"wait"'),
			("manage_worker", '"timeout":300'),
		],
		ordered_tools=["delegate_task", "write", "manage_worker", "write"],
		max_duration_seconds=180,
	)

	recovery_role = f"snapshot_recovery_{run_id}"
	recovery_root = target_root / "recovery"
	recovery_worker_note = recovery_root / "worker_recovery_note.md"
	recovery_local_prep = recovery_root / "local_prep.md"
	recovery = Scenario(
		name="失败恢复 snapshots 可观测性",
		worker_role=recovery_role,
		prompt=(
			"你必须严格按以下顺序执行，且不要省略工具调用。"
			f"第一步：调用 delegate_task，必须设置 async=true，role='{recovery_role}'。"
			f"委派任务内容：先调用 run_command 执行一个必定失败的命令 `powershell -NoProfile -Command \"Write-Error forced-recovery; exit 1\"`，"
			f"确认失败后继续同一任务，直接写入 {recovery_worker_note}。该文件所在目录已经存在，不要创建目录。"
			f"最终文档必须包含 `## RECOVERY_NOTE`、`## 运行态`、`## 快照字段`，正文中必须出现 `{run_id}`，并明确写出已从 forced-recovery 继续完成任务。"
			f"第二步：在不等待 worker 完成的前提下，主智能体立刻自己写入 {recovery_local_prep}，内容必须包含 `run_id={run_id}`、`worker_role={recovery_role}`、`phase=pre-wait`。"
			"第三步：到此停止，不要调用 manage_worker，不要 stop worker，不要重新调用 delegate_task 生成第二个 worker。整个场景中只能有一个 delegate_task。"
			"在 worker 完成之前，禁止调用 read 检查 worker 文件，也禁止主智能体代替 worker 提前补写 worker 文档。"
			"最后只输出一句简短派发确认，不要再执行额外工具。"
		),
		required_tools=["delegate_task", "write"],
		expected_files=[
			ExpectedFile(recovery_worker_note, ["## RECOVERY_NOTE", "## 运行态", "## 快照字段", "forced-recovery", run_id]),
			ExpectedFile(recovery_local_prep, [f"run_id={run_id}", f"worker_role={recovery_role}", "phase=pre-wait"]),
		],
		required_argument_snippets=[
			("delegate_task", '"async":true'),
			("delegate_task", recovery_role),
		],
		ordered_tools=["delegate_task", "write"],
		forbidden_tools=["read", "manage_worker"],
		min_manage_worker_calls=0,
		min_retry_count=1,
		require_recovery_note=True,
		external_wait_via_snapshots=True,
		require_structured_summary=False,
		external_wait_timeout_seconds=180,
		accepted_terminal_statuses=["completed"],
		max_duration_seconds=180,
	)

	return [(baseline, baseline_root), (recovery, recovery_root)]


def select_scenarios(
	all_scenarios: list[tuple[Scenario, Path]],
	scenario_name: str,
) -> list[tuple[Scenario, Path]]:
	if scenario_name == "all":
		return all_scenarios
	selected: list[tuple[Scenario, Path]] = []
	for scenario, scenario_root in all_scenarios:
		is_baseline = scenario.name == "异步委派 snapshots 可观测性"
		is_recovery = scenario.name == "失败恢复 snapshots 可观测性"
		if scenario_name == "baseline" and is_baseline:
			selected.append((scenario, scenario_root))
		if scenario_name == "recovery" and is_recovery:
			selected.append((scenario, scenario_root))
	return selected


def fetch_snapshots(base_url: str, limit: int = 100) -> dict[str, Any]:
	response = requests.get(f"{base_url}/v1/swarm/snapshots", params={"limit": limit}, timeout=10)
	response.raise_for_status()
	payload = response.json()
	if not isinstance(payload, dict):
		return {"snapshots": [], "summary": {}}
	return payload


def observe_matching_snapshot(payload: dict[str, Any], worker_role: str, observation: SnapshotObservation) -> None:
	snapshots = payload.get("snapshots")
	if not isinstance(snapshots, list):
		snapshots = []
	observation.total_samples += 1
	summary = payload.get("summary")
	if isinstance(summary, dict):
		observation.max_summary_total = max(observation.max_summary_total, int(summary.get("total", 0) or 0))
		observation.max_summary_running = max(observation.max_summary_running, int(summary.get("running", 0) or 0))
		observation.max_summary_archived = max(observation.max_summary_archived, int(summary.get("archived", 0) or 0))
	for item in snapshots:
		if not isinstance(item, dict):
			continue
		if str(item.get("role", "")) != worker_role:
			continue
		observation.matching_samples += 1
		observation.matched_worker_id = str(item.get("worker_id", "") or observation.matched_worker_id)
		status = str(item.get("status", "") or "")
		if status:
			observation.status_sequence.append(status)
		observation.last_matching_snapshot = item
		provider_retries = int(item.get("provider_retry_count", 0) or 0)
		output_retries = int(item.get("output_retry_count", 0) or 0)
		tool_retries = int(item.get("tool_retry_count", 0) or 0)
		observation.observed_retry_count = max(observation.observed_retry_count, provider_retries + output_retries + tool_retries)
		structured_summary = str(item.get("structured_summary", "") or "").strip()
		if structured_summary:
			observation.observed_structured_summary = True
			observation.last_structured_summary = structured_summary
		if str(item.get("last_result_preview", "") or "").strip():
			observation.observed_result_preview = True
		recovery_note = str(item.get("last_recovery_note", "") or "").strip()
		if recovery_note:
			observation.observed_recovery_note = True
			observation.last_recovery_note = recovery_note


def start_snapshot_poller(base_url: str, worker_role: str, stop_event: threading.Event, poll_interval: float = 0.75) -> tuple[SnapshotObservation, threading.Thread]:
	observation = SnapshotObservation(matched_worker_role=worker_role)

	def loop() -> None:
		while not stop_event.is_set():
			try:
				payload = fetch_snapshots(base_url)
				observe_matching_snapshot(payload, worker_role, observation)
			except requests.RequestException:
				pass
			stop_event.wait(poll_interval)

	thread = threading.Thread(target=loop, name=f"snapshot-poller-{worker_role}", daemon=True)
	thread.start()
	return observation, thread


def wait_for_snapshot_completion(base_url: str, scenario: Scenario, observation: SnapshotObservation) -> str:
	if not scenario.external_wait_via_snapshots or scenario.external_wait_timeout_seconds <= 0:
		return ""
	deadline = time.time() + scenario.external_wait_timeout_seconds
	next_heartbeat = time.time()
	accepted_terminal_statuses = {item.strip().lower() for item in scenario.accepted_terminal_statuses if item.strip()}
	last_status = str(observation.last_matching_snapshot.get("status", "") or "").strip().lower()
	while time.time() < deadline:
		try:
			payload = fetch_snapshots(base_url)
			observe_matching_snapshot(payload, scenario.worker_role, observation)
		except requests.RequestException:
			pass
		last_status = str(observation.last_matching_snapshot.get("status", "") or "").strip().lower()
		if last_status in accepted_terminal_statuses:
			return ""
		now = time.time()
		if now >= next_heartbeat:
			elapsed = int(scenario.external_wait_timeout_seconds - max(deadline - now, 0))
			print(
				f"[WAIT] role={scenario.worker_role} elapsed={elapsed}s status={last_status or 'unknown'} retries={observation.observed_retry_count} recovery_note={'yes' if observation.observed_recovery_note else 'no'}"
			)
			next_heartbeat = now + 15
		time.sleep(1.5)
	return f"external snapshots wait timed out after {scenario.external_wait_timeout_seconds}s (last_status={last_status or 'unknown'})"


def evaluate_result(base_url: str, scenario: Scenario, raw: dict[str, Any], observation: SnapshotObservation) -> dict[str, Any]:
	missing_tools = [tool for tool in scenario.required_tools if tool not in raw["tool_names"]]
	forbidden_tool_hits = [tool for tool in scenario.forbidden_tools if tool in raw["tool_names"]]
	missing_files, invalid_files = verify_files(scenario.expected_files)
	missing_args = [
		f"{tool}:{snippet}"
		for tool, snippet in scenario.required_argument_snippets
		if not has_tool_argument_snippet(raw["tool_calls"], tool, snippet)
	]
	forbidden_args = [
		f"{tool}:{snippet}"
		for tool, snippet in scenario.forbidden_argument_snippets
		if has_tool_argument_snippet(raw["tool_calls"], tool, snippet)
	]
	ordering_error = validate_order(raw["tool_names"], scenario.ordered_tools)
	manage_worker_count = count_tool_calls(raw["tool_calls"], "manage_worker")
	delegate_task_count = count_tool_calls(raw["tool_calls"], "delegate_task")
	manage_worker_error = ""
	if manage_worker_count < scenario.min_manage_worker_calls:
		manage_worker_error = f"manage_worker 调用次数不足: {manage_worker_count} < {scenario.min_manage_worker_calls}"
	delegate_task_error = ""
	if delegate_task_count > scenario.max_delegate_task_calls:
		delegate_task_error = f"delegate_task 调用次数过多: {delegate_task_count} > {scenario.max_delegate_task_calls}"
	duration_error = ""
	if raw["duration_seconds"] > scenario.max_duration_seconds:
		duration_error = f"耗时过长: {raw['duration_seconds']:.2f}s > {scenario.max_duration_seconds}s"

	post_payload_error = ""
	try:
		final_payload = fetch_snapshots(base_url)
		observe_matching_snapshot(final_payload, scenario.worker_role, observation)
	except requests.RequestException as exc:
		final_payload = {}
		post_payload_error = str(exc)
	last_status = str(observation.last_matching_snapshot.get("status", "") or "").strip().lower()
	accepted_terminal_statuses = {item.strip().lower() for item in scenario.accepted_terminal_statuses if item.strip()}
	if (
		scenario.allow_missing_primary_artifact_on_failed_terminal
		and last_status == "failed"
		and scenario.expected_files
	):
		primary_artifact = str(scenario.expected_files[0].path)
		missing_files = [path for path in missing_files if path != primary_artifact]
		invalid_files = [item for item in invalid_files if item.get("path") != primary_artifact]

	observation_errors: list[str] = []
	if observation.matching_samples == 0:
		observation_errors.append("轮询期间未观测到目标 worker 的 snapshots 记录")
	if not observation.matched_worker_id:
		observation_errors.append("未捕获目标 worker_id")
	if observation.max_summary_total <= 0:
		observation_errors.append("snapshots summary.total 始终为 0")
	if observation.max_summary_running <= 0:
		observation_errors.append("snapshots summary.running 未体现运行中 worker")
	if scenario.require_structured_summary and not observation.observed_structured_summary:
		observation_errors.append("未观测到 structured_summary")
	elif not observation.observed_structured_summary and not observation.observed_result_preview:
		observation_errors.append("未观测到 structured_summary 或 last_result_preview")
	if observation.observed_retry_count < scenario.min_retry_count:
		observation_errors.append(
			f"观测到的 retry_count 不足: {observation.observed_retry_count} < {scenario.min_retry_count}"
		)
	if scenario.require_recovery_note and not observation.observed_recovery_note:
		observation_errors.append("未观测到 last_recovery_note")
	if not last_status:
		observation_errors.append("未观测到最终 worker 状态")
	elif accepted_terminal_statuses and last_status not in accepted_terminal_statuses:
		observation_errors.append(
			f"最终观测到的 worker 状态不在允许集合 {sorted(accepted_terminal_statuses)}: {last_status}"
		)
	if post_payload_error:
		observation_errors.append(f"最终 snapshots 请求失败: {post_payload_error}")

	success = not any(
		[
			missing_tools,
			forbidden_tool_hits,
			missing_files,
			invalid_files,
			missing_args,
			forbidden_args,
			ordering_error,
			manage_worker_error,
			delegate_task_error,
			duration_error,
			observation_errors,
		]
	)

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
		"forbidden_tool_hits": forbidden_tool_hits,
		"missing_files": missing_files,
		"invalid_files": invalid_files,
		"missing_args": missing_args,
		"forbidden_args": forbidden_args,
		"ordering_error": ordering_error,
		"manage_worker_error": manage_worker_error,
		"delegate_task_error": delegate_task_error,
		"duration_error": duration_error,
		"observation_errors": observation_errors,
		"snapshot_observation": asdict(observation),
		"content": raw["content"],
		"final_snapshots": final_payload,
	}


def run_scenario(base_url: str, model: str, scenario: Scenario, scenario_root: Path, timeout_seconds: int) -> dict[str, Any]:
	cleanup_path(scenario_root)
	scenario_root.mkdir(parents=True, exist_ok=True)
	ensure_expected_file_parents(scenario.expected_files)
	print_step(f"执行场景: {scenario.name}")
	stop_event = threading.Event()
	observation, thread = start_snapshot_poller(base_url, scenario.worker_role, stop_event)
	try:
		raw = collect_stream_result(base_url, model, scenario.prompt, timeout_seconds)
		external_wait_error = wait_for_snapshot_completion(base_url, scenario, observation)
	finally:
		stop_event.set()
		thread.join(timeout=2)
	result = evaluate_result(base_url, scenario, raw, observation)
	if external_wait_error:
		result["observation_errors"].append(external_wait_error)
		result["success"] = False
	print(f"\n[INFO] TTFT={result['ttft_seconds']:.2f}s Duration={result['duration_seconds']:.2f}s")
	print(f"[INFO] Tools={result['tool_names']}")
	print(f"[INFO] Snapshot observation={result['snapshot_observation']}")
	if result["success"]:
		print(f"✅ 场景通过: {scenario.name}")
	else:
		print(f"❌ 场景失败: {scenario.name}")
	return result


def main() -> int:
	args = parse_args()
	ensure_backend_ready(args.base_url)
	run_id = uuid.uuid4().hex[:8]
	selected_scenarios = select_scenarios(build_scenarios(run_id), args.scenario)
	results = [
		run_scenario(args.base_url, args.model, scenario, scenario_root, args.timeout)
		for scenario, scenario_root in selected_scenarios
	]
	passed = all(bool(result["success"]) for result in results)
	failure_reasons: list[str] = []
	for result in results:
		for key in ("missing_tools", "forbidden_tool_hits", "missing_files", "invalid_files", "missing_args", "forbidden_args", "observation_errors"):
			value = result.get(key)
			if value:
				failure_reasons.append(f"{result['name']}:{key}={value}")
		for key in ("ordering_error", "manage_worker_error", "delegate_task_error", "duration_error"):
			value = str(result.get(key, "") or "").strip()
			if value:
				failure_reasons.append(f"{result['name']}:{value}")

	report_path = Path(args.report_path) if args.report_path else REPORT_DIR / f"subagent_observability_e2e_report_{run_id}.json"
	payload = {
		"passed": passed,
		"base_url": args.base_url,
		"model": args.model,
		"results": results,
		"diagnostics": [
			f"scenarios={len(results)}",
			f"passed={sum(1 for result in results if result['success'])}",
			*[f"{result['name']}:matching_snapshot_samples={result['snapshot_observation']['matching_samples']}" for result in results],
			*[f"{result['name']}:max_summary_running={result['snapshot_observation']['max_summary_running']}" for result in results],
		],
		"failure_reasons": failure_reasons,
	}
	write_standard_report(report_path, payload)
	print_step("子智能体 snapshots 端到端评估汇总")
	print(report_path)
	return 0 if passed else 1


if __name__ == "__main__":
	sys.exit(main())
