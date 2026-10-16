from __future__ import annotations

import argparse
import asyncio
import json
import re
import shutil
import time
import uuid
from collections import Counter
from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any
from urllib.request import urlopen

import websockets


DEFAULT_BASE_URL = "http://localhost:3000"
DEFAULT_MODEL = "aura-coder"
DEFAULT_MODE = "coding"
DEFAULT_TIMEOUT_SECONDS = 0
REPO_ROOT = Path(__file__).resolve().parents[3]
WORKSPACE_DIR = REPO_ROOT / "workspace" / "mixed_long_chain_eval"
REPORTS_DIR = Path(__file__).resolve().parent / "reports"
FAKE_COMPLETION_TEXT = "任务已执行完毕。我已完成上述操作。"
RECOVERY_EXHAUSTED_TEXT = "我连续多次收到没有有效内容的回复，暂时无法继续自动恢复。"


@dataclass(frozen=True)
class ExpectedFinding:
    artifact: str
    marker: str
    current_value: int | float | str
    correct_value: int | float | str
    error_type: str
    label: str


@dataclass(frozen=True)
class ScenarioDefinition:
    name: str
    report_prefix: str
    description: str
    required_artifacts: tuple[str, ...]
    required_phrases: tuple[str, ...]
    expected_findings: tuple[ExpectedFinding, ...]
    minimum_tool_calls: int
    minimum_distinct_tools: int
    minimum_total_matches: int
    minimum_root_matches: int
    minimum_cascade_matches: int
    follow_up_prompt: str
    second_follow_up_prompt: str
    forbidden_tool_names: tuple[str, ...]
    max_total_duration_seconds: int
    recommended_timeout_seconds: int


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
class TurnEvaluationReport:
    label: str
    prompt: str
    session_id: str = ""
    duration_seconds: float = 0.0
    final_text: str = ""
    answer_text: str = ""
    matched_findings: list[str] = field(default_factory=list)
    missing_findings: list[str] = field(default_factory=list)
    semantic_matched_findings: list[str] = field(default_factory=list)
    semantic_missing_findings: list[str] = field(default_factory=list)
    tool_calls: list[ToolCallRecord] = field(default_factory=list)
    tool_results: list[ToolResultRecord] = field(default_factory=list)
    thought_events: list[str] = field(default_factory=list)
    done_received: bool = False
    error_event: str = ""


@dataclass
class EvaluationReport:
    scenario: str
    session_id: str
    ws_url: str
    model: str
    mode: str
    duration_seconds: float
    fixture_root: str
    final_text: str
    answer_text: str
    passed: bool
    failure_reasons: list[str] = field(default_factory=list)
    diagnostics: list[str] = field(default_factory=list)
    matched_findings: list[str] = field(default_factory=list)
    missing_findings: list[str] = field(default_factory=list)
    semantic_matched_findings: list[str] = field(default_factory=list)
    semantic_missing_findings: list[str] = field(default_factory=list)
    tool_call_counts: dict[str, int] = field(default_factory=dict)
    tool_calls: list[ToolCallRecord] = field(default_factory=list)
    tool_results: list[ToolResultRecord] = field(default_factory=list)
    thought_events: list[str] = field(default_factory=list)
    turn_reports: list[TurnEvaluationReport] = field(default_factory=list)
    done_received: bool = False
    error_event: str = ""
    session_reused: bool = False
    first_turn_semantic_matched_count: int = 0
    final_turn_semantic_matched_count: int = 0


SCENARIO = ScenarioDefinition(
    name="mixed_release_triage",
    report_prefix="mixed_long_chain_real",
    description="跨代码、配置、日志、文档与测试基线的长链发布事故排查场景。",
    required_artifacts=(
        "service/order_sync.py",
        "config/prod.yaml",
        "logs/order_sync.log",
        "dashboard/kpi_snapshot.json",
        "docs/release_runbook.md",
        "tests/failing_checks.txt",
        "ops/risk_isolation_notes.md",
    ),
    required_phrases=("根因", "修复顺序", "回归验证", "风险隔离"),
    expected_findings=(
        ExpectedFinding("service/order_sync.py", "REQUEST_TIMEOUT_SECONDS", 30, 300, "root", "同步超时阈值过低"),
        ExpectedFinding("config/prod.yaml", "sync_endpoint", "/v1/orders/sync", "/v2/orders/sync", "root", "生产同步端点仍指向 v1"),
        ExpectedFinding("logs/order_sync.log", "timed out after", "30s", "300s", "cascade", "日志暴露超时阈值错误"),
        ExpectedFinding("logs/order_sync.log", "POST", "/v1/orders/sync", "/v2/orders/sync", "cascade", "日志暴露错误接口版本"),
        ExpectedFinding("dashboard/kpi_snapshot.json", "api_version", "v1", "v2", "cascade", "仪表盘版本仍停留在 v1"),
        ExpectedFinding("dashboard/kpi_snapshot.json", "synced_orders", 12480, 13100, "cascade", "仪表盘同步订单数被低估"),
        ExpectedFinding("docs/release_runbook.md", "curl", "/v1/orders/sync", "/v2/orders/sync", "cascade", "运行手册仍指导旧接口"),
        ExpectedFinding("tests/failing_checks.txt", "timeout_sla_seconds", 30, 300, "cascade", "测试基线仍使用旧超时阈值"),
        ExpectedFinding("tests/failing_checks.txt", "expected_synced_orders", 12480, 13100, "cascade", "测试基线仍使用过期订单数"),
        ExpectedFinding("analytics/daily_sync_report.md", "api_version", "v1", "v2", "cascade", "日报仍引用旧版本"),
    ),
    minimum_tool_calls=8,
    minimum_distinct_tools=2,
    minimum_total_matches=8,
    minimum_root_matches=2,
    minimum_cascade_matches=6,
    follow_up_prompt="继续，把修复顺序、风险隔离和回归验证清单补全，不要只重复根因，不要请求修改授权；保留原有结构，并保留覆盖检查，确保每个关键文件都有一句结论。",
    second_follow_up_prompt="继续，不要重复前文，也不要请求授权；在保留原有章节和覆盖检查的前提下，补充回滚触发条件、灰度观察指标，以及一张“证据来源 -> 结论”的对应表，明确哪些结论来自 logs/config/service，哪些来自 dashboard/docs/tests/analytics/ops，尤其不要漏掉 logs/order_sync.log 和 ops/risk_isolation_notes.md。",
    forbidden_tool_names=("delegate_task", "manage_worker", "wait_for_workers"),
    max_total_duration_seconds=480,
    recommended_timeout_seconds=540,
)


def build_artifact_checklist_text() -> str:
    return "；".join(f"{index + 1}. {artifact}" for index, artifact in enumerate(SCENARIO.required_artifacts))


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="真实评估超长混合任务链优化效果")
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL, help="后端地址，例如 http://localhost:3000")
    parser.add_argument("--model", default=DEFAULT_MODEL, help="评估使用的模型")
    parser.add_argument("--mode", default=DEFAULT_MODE, help="会话模式，默认 coding")
    parser.add_argument("--timeout", type=int, default=DEFAULT_TIMEOUT_SECONDS, help="单次评估超时时间（秒），传 0 表示使用场景默认值")
    parser.add_argument("--disable-follow-up", action="store_true", help="只执行首轮，不验证同 session 的继续轮")
    parser.add_argument("--follow-up-prompt", default="", help="覆盖场景默认的继续轮提示词")
    parser.add_argument("--second-follow-up-prompt", default="", help="可选：覆盖场景默认的第二个继续轮提示词")
    parser.add_argument("--keep-fixture", action="store_true", help="保留自动生成的测试目录")
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


def write_fixture_file(root: Path, relative_path: str, content: str) -> None:
    target = root / relative_path
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(content.strip() + "\n", encoding="utf-8")


def create_fixture(root: Path) -> None:
    write_fixture_file(
        root,
        "service/order_sync.py",
        """
REQUEST_TIMEOUT_SECONDS = 30
MAX_RETRIES = 1


def sync_orders(client, payload, endpoint):
    try:
        return client.post(endpoint, json=payload, timeout=REQUEST_TIMEOUT_SECONDS)
    except TimeoutError:
        return {
            "status": "partial",
            "source": "cache-fallback",
            "synced_orders": 12480,
            "api_version": "v1",
        }
""",
    )
    write_fixture_file(
        root,
        "config/prod.yaml",
        """
environment: production
sync_endpoint: /v1/orders/sync
dashboard_source_version: v1
expected_batch_delta: 620
""",
    )
    write_fixture_file(
        root,
        "logs/order_sync.log",
        """
2026-04-12T09:14:02Z ERROR batch=A17 POST /v1/orders/sync timed out after 30s
2026-04-12T09:14:02Z WARN batch=A17 expected_orders=620 lost_due_to_timeout=620
2026-04-12T09:14:03Z WARN cache fallback reused snapshot synced_orders=12480 api_version=v1
2026-04-12T09:14:05Z INFO downstream dashboard left unchanged to avoid schema mismatch
""",
    )
    write_fixture_file(
        root,
        "dashboard/kpi_snapshot.json",
        json.dumps(
            {
                "api_version": "v1",
                "synced_orders": 12480,
                "data_freshness": "cache-fallback",
                "last_batch": "A17",
            },
            ensure_ascii=False,
            indent=2,
        ),
    )
    write_fixture_file(
        root,
        "docs/release_runbook.md",
        """
# Release Runbook

1. 使用 curl 校验接口：curl https://prod.example.com/v1/orders/sync
2. 仪表盘同步订单数应保持 12480
3. 如果超时超过 30 秒，允许直接回退到缓存快照
""",
    )
    write_fixture_file(
        root,
        "tests/failing_checks.txt",
        """
timeout_sla_seconds=30
expected_synced_orders=12480
expected_api_version=v1
expected_endpoint=/v1/orders/sync
""",
    )
    write_fixture_file(
        root,
        "analytics/daily_sync_report.md",
        """
# Daily Sync Report

- synced_orders: 12480
- api_version: v1
- data_freshness: cache-fallback
""",
    )
    write_fixture_file(
        root,
        "ops/risk_isolation_notes.md",
        """
# Risk Isolation Notes

- 当前事故在订单同步链路，不要扩大到其他写路径
- 修复后必须单独验证 v2 接口、超时阈值、仪表盘恢复和回退策略
""",
    )


def build_prompt(fixture_root: Path) -> str:
    root_text = fixture_root.as_posix()
    artifact_checklist = build_artifact_checklist_text()
    return (
        f"我在工作区里放了一个线上事故排查包，根目录是 {root_text}。"
        "这是一个混合长任务，请你跨代码、配置、日志、文档和测试基线完成完整排查，不要在发现第一处问题后停止。"
        "这是只读评估，禁止修改排查包内任何文件，也不要新建修复脚本、计划文档或补丁文件。"
        "你的任务是："
        "1. 先从 logs 和 config 找出真正的根因；"
        "2. 再回看 service 代码解释为什么会出现缓存回退和数据陈旧；"
        "3. 再核对 dashboard、docs、tests、analytics 这些下游产物受到了哪些污染；"
        "4. 最终至少明确 2 个根因和 6 个级联影响；"
        "5. 每个问题都要给出：文件、字段或符号、当前值、正确值、判断依据；"
        "5.1 日志和测试基线类问题请尽量保留原 marker，例如 timed out after、POST、timeout_sla_seconds、expected_synced_orders，不要改写成泛化描述；"
        "5.2 对 logs/order_sync.log，最终答案里要显式保留两条严格 evidence：POST /v1/orders/sync -> /v2/orders/sync，以及 timed out after 30s -> 300s；"
        "5.3 对 tests/failing_checks.txt，最终答案里要显式保留 timeout_sla_seconds=30 -> 300 和 expected_synced_orders=12480 -> 13100；"
        "6. 最后写出修复顺序、风险隔离方式和回归验证清单。"
        "6.1 最终回答必须包含这些章节标题：根因分析、级联影响、修复顺序、风险隔离、回归验证、覆盖检查。"
        "6.2 根因分析和级联影响优先使用紧凑表格或逐条清单；每条必须同时出现：文件路径、marker 或字段、当前值、正确值、证据原文。"
        "6.3 风险隔离部分必须显式引用 ops/risk_isolation_notes.md 的结论，不能只给泛化建议。"
        f"6.4 覆盖检查必须逐项点名并给出一句结论：{artifact_checklist}。"
        "6.5 当正确值已经能从证据直接推导时，不要写“建议更高超时”或“建议升级 v2”这类软化表述，必须给出精确 corrected value。"
        "不要只给摘要；如果某个工具失败，请自行切换其他可行方式继续完成。"
    )


def make_report_path(custom_path: str) -> Path:
    if custom_path:
        return Path(custom_path)
    REPORTS_DIR.mkdir(parents=True, exist_ok=True)
    timestamp = time.strftime("%Y%m%d_%H%M%S")
    return REPORTS_DIR / f"{SCENARIO.report_prefix}_{timestamp}.json"


def extract_answer_text(text: str) -> str:
    without_thinking = re.sub(r"<thinking>.*?</thinking>", "", text, flags=re.IGNORECASE | re.DOTALL)
    without_tags = re.sub(r"</?[^>]+>", "", without_thinking)
    normalized = re.sub(r"\n{3,}", "\n\n", without_tags)
    return normalized.strip()


def normalize_text(text: str) -> str:
    return re.sub(r"\s+", " ", text.strip())


def collapse_thought_events(thought_events: list[str]) -> list[str]:
    collapsed: list[str] = []
    buffer: list[str] = []
    ignored_prefixes = (
        "Calling ",
        "<file_content>",
        "[Tool Output",
        "[读取范围",
        "dreaming",
        "进入梦境",
        "Consolidating",
        "Applying decay",
        "梦境整合完成",
        "遗忘完成",
        "检测到 ",
    )

    def flush_buffer() -> None:
        if not buffer:
            return
        combined = normalize_text(" ".join(buffer))
        buffer.clear()
        if not combined:
            return
        if collapsed and (combined == collapsed[-1] or combined in collapsed[-1] or collapsed[-1] in combined):
            collapsed[-1] = combined if len(combined) > len(collapsed[-1]) else collapsed[-1]
            return
        collapsed.append(combined)

    for raw in thought_events:
        text = normalize_text(raw)
        if not text or text == "💓":
            continue
        if text.startswith(ignored_prefixes):
            continue
        if len(text) <= 2 and not any(ch.isalnum() for ch in text):
            continue

        buffer.append(text)
        combined = normalize_text(" ".join(buffer))
        should_flush = (
            len(combined) >= 140
            or text.endswith(("。", "！", "？", ":", "："))
            or text.startswith(("根因", "影响", "修复", "回滚", "灰度", "证据"))
        )
        if should_flush:
            flush_buffer()

    flush_buffer()
    return collapsed


def numeric_aliases(value: int | float | str) -> tuple[str, ...]:
    if isinstance(value, str):
        return (value,)
    numeric = float(value)
    aliases = {
        format(numeric, ".10g"),
        format(numeric, ".4f"),
        format(numeric, ".2f"),
        format(numeric, ".1f"),
    }
    if numeric.is_integer():
        aliases.add(str(int(numeric)))
        aliases.add(f"{int(numeric)}.0")
    normalized_aliases: set[str] = set()
    for alias in aliases:
        cleaned = alias.rstrip("0").rstrip(".") if "." in alias else alias
        if cleaned:
            normalized_aliases.add(cleaned)
        normalized_aliases.add(alias)
    return tuple(sorted(alias for alias in normalized_aliases if alias))


def finding_present(text: str, finding: ExpectedFinding, semantic: bool = False) -> bool:
    current_aliases = numeric_aliases(finding.current_value)
    correct_aliases = numeric_aliases(finding.correct_value)
    lines = [line.strip() for line in text.splitlines() if line.strip()]

    for index in range(len(lines)):
        window = " ".join(lines[index : index + 4])
        if finding.artifact not in window:
            continue
        if semantic:
            if any(alias in window for alias in current_aliases) and any(alias in window for alias in correct_aliases):
                return True
            continue
        if finding.marker in window and any(alias in window for alias in correct_aliases):
            return True

    normalized = normalize_text(text)
    for match in re.finditer(re.escape(finding.artifact), normalized):
        segment = normalized[match.start() : match.start() + 260]
        if semantic:
            if any(alias in segment for alias in current_aliases) and any(alias in segment for alias in correct_aliases):
                return True
            continue
        if finding.marker in segment and any(alias in segment for alias in correct_aliases):
            return True

    return False


def collect_finding_matches(text: str, semantic: bool = False) -> tuple[list[str], list[str], int, int]:
    matched_findings: list[str] = []
    missing_findings: list[str] = []
    root_matches = 0
    cascade_matches = 0

    for finding in SCENARIO.expected_findings:
        matched = finding_present(text, finding, semantic=semantic)
        label = f"{finding.artifact}::{finding.marker} -> {finding.correct_value} ({finding.label})"
        if matched:
            matched_findings.append(label)
            if finding.error_type == "root":
                root_matches += 1
            else:
                cascade_matches += 1
        else:
            missing_findings.append(label)

    return matched_findings, missing_findings, root_matches, cascade_matches


def evaluate_turn(turn: TurnEvaluationReport) -> TurnEvaluationReport:
    raw_text = turn.final_text.strip()
    turn.answer_text = extract_answer_text(raw_text)
    matched, missing, _, _ = collect_finding_matches(turn.answer_text, semantic=False)
    semantic_matched, semantic_missing, _, _ = collect_finding_matches(turn.answer_text, semantic=True)
    turn.matched_findings = matched
    turn.missing_findings = missing
    turn.semantic_matched_findings = semantic_matched
    turn.semantic_missing_findings = semantic_missing
    return turn


def diagnose_tooling(report: EvaluationReport) -> None:
    matched_execution_ids = {
        result.tool_execution_id
        for result in report.tool_results
        if result.tool_execution_id
    }
    executed_tool_calls = [
        call
        for call in report.tool_calls
        if not call.tool_execution_id or call.tool_execution_id in matched_execution_ids
    ]

    tool_call_counts = Counter(call.name for call in executed_tool_calls)
    report.tool_call_counts = dict(tool_call_counts)
    if len(tool_call_counts) < SCENARIO.minimum_distinct_tools:
        report.failure_reasons.append(
            f"混合任务使用的工具类型不足，实际 {len(tool_call_counts)} 种，要求至少 {SCENARIO.minimum_distinct_tools} 种"
        )

    failed_results = [result for result in report.tool_results if result.is_error]
    if failed_results:
        report.diagnostics.append(f"工具调用存在失败结果 {len(failed_results)} 次")

    empty_calls = [call for call in executed_tool_calls if not call.input.strip()]
    if len(empty_calls) >= 3:
        report.diagnostics.append(f"空参数工具调用偏多，共 {len(empty_calls)} 次")

    if tool_call_counts.get("run_command", 0) >= 5:
        report.diagnostics.append("命令行回退偏重，说明原生结构化工具链仍有提升空间")

    collapsed_thoughts = collapse_thought_events(report.thought_events)
    if len(collapsed_thoughts) >= 30:
        report.diagnostics.append(f"长思考事件较多，共 {len(collapsed_thoughts)} 条，任务链偏长")


def detect_fixture_mutations(report: EvaluationReport) -> list[ToolCallRecord]:
    fixture_root = report.fixture_root.replace("\\", "/")
    mutating_tools = {"write", "edit", "patch", "delete", "mv", "cp"}
    mutated_calls: list[ToolCallRecord] = []
    for call in report.tool_calls:
        normalized_input = call.input.replace("\\", "/")
        if call.name in mutating_tools and fixture_root in normalized_input:
            mutated_calls.append(call)
    return mutated_calls


def evaluate_report(report: EvaluationReport) -> EvaluationReport:
    for index, turn in enumerate(report.turn_reports):
        report.turn_reports[index] = evaluate_turn(turn)

    if report.turn_reports:
        report.first_turn_semantic_matched_count = len(report.turn_reports[0].semantic_matched_findings)
        report.final_turn_semantic_matched_count = len(report.turn_reports[-1].semantic_matched_findings)

    if len(report.turn_reports) > 1 and report.session_id:
        report.session_reused = all(turn.session_id == report.session_id for turn in report.turn_reports[1:])

    report.done_received = bool(report.turn_reports) and all(turn.done_received for turn in report.turn_reports)
    report.error_event = " | ".join(turn.error_event for turn in report.turn_reports if turn.error_event)
    report.answer_text = extract_answer_text(report.final_text.strip())
    text = report.answer_text

    if not report.done_received:
        report.failure_reasons.append("未收到 done 事件，评估超时或链路中断")
    if report.error_event:
        report.failure_reasons.append(f"收到 error 事件: {report.error_event}")
    if FAKE_COMPLETION_TEXT in report.final_text:
        report.failure_reasons.append("最终回答仍出现旧的假完成文案")
    if RECOVERY_EXHAUSTED_TEXT in report.final_text:
        report.failure_reasons.append("触发了 silent turn 恢复耗尽兜底")
    if len(report.tool_calls) < SCENARIO.minimum_tool_calls:
        report.failure_reasons.append(
            f"工具调用次数少于场景要求，实际 {len(report.tool_calls)} 次，要求至少 {SCENARIO.minimum_tool_calls} 次"
        )
    if not any(not result.is_error and result.content.strip() for result in report.tool_results):
        report.failure_reasons.append("没有拿到任何成功的工具结果，无法证明真实完成了分析")
    forbidden_tools = sorted({call.name for call in report.tool_calls if call.name in SCENARIO.forbidden_tool_names})
    if forbidden_tools:
        report.failure_reasons.append(f"单主 agent 场景出现禁止工具: {forbidden_tools}")
    if report.duration_seconds > SCENARIO.max_total_duration_seconds:
        report.failure_reasons.append(
            f"单主 agent 长链路耗时过长: {report.duration_seconds:.2f}s > {SCENARIO.max_total_duration_seconds}s"
        )
    if not text.strip():
        report.failure_reasons.append("最终可见回答为空")

    covered_artifacts = [artifact for artifact in SCENARIO.required_artifacts if artifact in text]
    if len(covered_artifacts) < len(SCENARIO.required_artifacts):
        report.failure_reasons.append(
            f"最终回答覆盖关键文件不完整，命中 {len(covered_artifacts)}/{len(SCENARIO.required_artifacts)} 个关键文件"
        )
    if "覆盖检查" not in text:
        report.diagnostics.append("最终回答未显式输出覆盖检查")

    for phrase in SCENARIO.required_phrases:
        if phrase not in text:
            report.failure_reasons.append(f"最终回答缺少关键表述：{phrase}")

    strict_matched, strict_missing, _, _ = collect_finding_matches(text, semantic=False)
    semantic_matched, semantic_missing, root_matches, cascade_matches = collect_finding_matches(text, semantic=True)
    report.matched_findings = strict_matched
    report.missing_findings = strict_missing
    report.semantic_matched_findings = semantic_matched
    report.semantic_missing_findings = semantic_missing

    if len(semantic_matched) < SCENARIO.minimum_total_matches:
        report.failure_reasons.append(
            f"关键错误语义命中不足，实际命中 {len(semantic_matched)} 条，要求至少 {SCENARIO.minimum_total_matches} 条"
        )
    if root_matches < SCENARIO.minimum_root_matches:
        report.failure_reasons.append(
            f"根因错误语义命中不足，实际命中 {root_matches} 条，要求至少 {SCENARIO.minimum_root_matches} 条"
        )
    if cascade_matches < SCENARIO.minimum_cascade_matches:
        report.failure_reasons.append(
            f"级联错误语义命中不足，实际命中 {cascade_matches} 条，要求至少 {SCENARIO.minimum_cascade_matches} 条"
        )

    if len(strict_matched) < len(semantic_matched):
        report.diagnostics.append(
            f"存在 {len(semantic_matched) - len(strict_matched)} 条值正确但定位未严格命中的结果"
        )
    if len(report.turn_reports) > 1 and not report.session_reused:
        report.failure_reasons.append("继续轮未复用同一 session，无法验证长链连续性")

    mutated_calls = detect_fixture_mutations(report)
    if mutated_calls:
        report.failure_reasons.append(
            f"检测到对排查夹具的修改行为，共 {len(mutated_calls)} 次，违背只读评估约束"
        )

    diagnose_tooling(report)
    report.passed = not report.failure_reasons
    return report


async def run_turn(
    args: argparse.Namespace,
    prompt: str,
    label: str,
    session_id: str = "",
) -> TurnEvaluationReport:
    ws_url = build_ws_url(args.base_url)
    started_at = time.time()
    turn = TurnEvaluationReport(label=label, prompt=prompt, session_id=session_id)
    payload: dict[str, Any] = {"type": "chat", "content": prompt, "model": args.model, "mode": args.mode}
    if session_id:
        payload["session_id"] = session_id

    print(f"[INFO] Connecting to {ws_url}")
    print(f"[INFO] Scenario: {SCENARIO.name} | {SCENARIO.description}")
    print(f"[INFO] Turn: {label}")
    print(f"[INFO] Session ID: {session_id or '<auto-create>'}")

    async with websockets.connect(ws_url, max_size=8 * 1024 * 1024) as websocket:
        await websocket.send(json.dumps(payload, ensure_ascii=False))
        timeout_seconds = args.timeout or SCENARIO.recommended_timeout_seconds
        deadline = time.time() + timeout_seconds
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
                print(f"\n[INFO] Server session id: {turn.session_id}")

            if event_type == "token":
                token = event.get("content", "")
                turn.final_text += token
                print(token, end="", flush=True)
                continue

            if event_type == "thought":
                data = event.get("data") or {}
                thought_content = str(data.get("content", "")).strip()
                if thought_content:
                    turn.thought_events.append(thought_content)
                    print(f"\n[THOUGHT] {thought_content[:200]}")
                continue

            if event_type == "tool_call":
                try:
                    tool_calls = json.loads(event.get("content", "[]"))
                except json.JSONDecodeError:
                    tool_calls = []
                for tool_call in tool_calls:
                    record = ToolCallRecord(
                        name=str(tool_call.get("name", "")),
                        input=str(tool_call.get("input", "")),
                        tool_execution_id=str(event.get("tool_execution_id", "")),
                    )
                    turn.tool_calls.append(record)
                    print(f"\n[TOOL_CALL] {record.name} | {record.input[:240]}")
                continue

            if event_type == "tool_result":
                try:
                    tool_result = json.loads(event.get("content", "{}"))
                except json.JSONDecodeError:
                    tool_result = {}
                record = ToolResultRecord(
                    name=str(tool_result.get("name", "")),
                    content=str(tool_result.get("content", "")),
                    is_error=bool(tool_result.get("is_error", False)),
                    tool_execution_id=str(event.get("tool_execution_id", "")),
                )
                turn.tool_results.append(record)
                print(f"\n[TOOL_RESULT] {record.name} | error={record.is_error} | {record.content.replace(chr(10), ' ')[:240]}")
                continue

            if event_type == "error":
                turn.error_event = str(event.get("content", ""))
                print(f"\n[ERROR] {turn.error_event}")
                break

            if event_type == "done":
                turn.done_received = True
                print("\n[INFO] Received done event")
                break

            print(f"\n[EVENT] {event_type}: {event}")

    turn.duration_seconds = time.time() - started_at
    return evaluate_turn(turn)


def merge_turn_into_report(report: EvaluationReport, turn: TurnEvaluationReport) -> None:
    report.turn_reports.append(turn)
    if not report.session_id and turn.session_id:
        report.session_id = turn.session_id
    report.final_text = "\n\n".join(part for part in (report.final_text, turn.final_text) if part)
    report.tool_calls.extend(turn.tool_calls)
    report.tool_results.extend(turn.tool_results)
    report.thought_events.extend(turn.thought_events)


async def run_evaluation(args: argparse.Namespace, fixture_root: Path, follow_up_prompt: str) -> EvaluationReport:
    report = EvaluationReport(
        scenario=SCENARIO.name,
        session_id="",
        ws_url=build_ws_url(args.base_url),
        model=args.model,
        mode=args.mode,
        duration_seconds=0.0,
        fixture_root=str(fixture_root),
        final_text="",
        answer_text="",
        passed=False,
    )

    started_at = time.time()
    initial_turn = await run_turn(args=args, prompt=build_prompt(fixture_root), label="initial")
    merge_turn_into_report(report, initial_turn)

    if follow_up_prompt and report.session_id:
        follow_up_turn = await run_turn(args=args, prompt=follow_up_prompt, label="follow_up", session_id=report.session_id)
        merge_turn_into_report(report, follow_up_turn)

    second_follow_up_prompt = ""
    if not args.disable_follow_up:
        second_follow_up_prompt = args.second_follow_up_prompt or SCENARIO.second_follow_up_prompt
    if second_follow_up_prompt and report.session_id:
        second_follow_up_turn = await run_turn(
            args=args,
            prompt=second_follow_up_prompt,
            label="second_follow_up",
            session_id=report.session_id,
        )
        merge_turn_into_report(report, second_follow_up_turn)

    report.duration_seconds = time.time() - started_at
    return evaluate_report(report)


def save_report(report: EvaluationReport, report_path: Path) -> None:
    report_path.parent.mkdir(parents=True, exist_ok=True)
    report_path.write_text(json.dumps(asdict(report), ensure_ascii=False, indent=2), encoding="utf-8")


def print_summary(report: EvaluationReport, report_path: Path) -> None:
    print("\n" + "=" * 80)
    print("评估结果")
    print("=" * 80)
    print(f"Scenario    : {report.scenario}")
    print(f"Session ID  : {report.session_id}")
    print(f"Duration    : {report.duration_seconds:.2f}s")
    print(f"Tool Calls  : {len(report.tool_calls)}")
    print(f"Tool Results: {len(report.tool_results)}")
    print(f"Done Event  : {report.done_received}")
    print(f"Report Path : {report_path}")
    print(f"Matched     : strict={len(report.matched_findings)} semantic={len(report.semantic_matched_findings)}")
    if report.turn_reports:
        print("Turns       :")
        for turn in report.turn_reports:
            print(
                f"- {turn.label}: session={turn.session_id or '<empty>'} "
                f"strict={len(turn.matched_findings)} semantic={len(turn.semantic_matched_findings)} "
                f"tool_calls={len(turn.tool_calls)} done={turn.done_received}"
            )
    print(f"SessionReuse: {report.session_reused}")
    print(f"FirstMatched: semantic={report.first_turn_semantic_matched_count}")
    print(f"FinalMatched: semantic={report.final_turn_semantic_matched_count}")
    if report.tool_call_counts:
        print(f"Tool Stats  : {report.tool_call_counts}")
    if report.diagnostics:
        print("Diagnostics :")
        for diagnostic in report.diagnostics:
            print(f"- {diagnostic}")
    if report.passed:
        print("\nPASS: 场景评估通过")
    else:
        print("\nFAIL: 场景评估未通过")
        for reason in report.failure_reasons:
            print(f"- {reason}")
        if report.semantic_missing_findings:
            print("Missing Findings:")
            for finding in report.semantic_missing_findings[:12]:
                print(f"- {finding}")


async def async_main() -> int:
    args = parse_args()
    ensure_backend_ready(args.base_url)
    report_path = make_report_path(args.report_path)
    follow_up_prompt = "" if args.disable_follow_up else (args.follow_up_prompt or SCENARIO.follow_up_prompt)

    fixture_root = WORKSPACE_DIR / f"{SCENARIO.name}_{uuid.uuid4().hex[:8]}"
    fixture_root.mkdir(parents=True, exist_ok=True)
    create_fixture(fixture_root)

    try:
        report = await run_evaluation(args, fixture_root, follow_up_prompt)
        save_report(report, report_path)
        print_summary(report, report_path)
        return 0 if report.passed else 1
    finally:
        if not args.keep_fixture and fixture_root.exists():
            shutil.rmtree(fixture_root)


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
