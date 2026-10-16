from __future__ import annotations

import argparse
import asyncio
import json
import re
import sys
import time
import uuid
import zipfile
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
DEFAULT_SCENARIO = "cross_sheet_audit"
REPO_ROOT = Path(__file__).resolve().parents[3]
WORKSPACE_DIR = REPO_ROOT / "workspace" / "silent_turn_eval"
REPORTS_DIR = Path(__file__).resolve().parent / "reports"
FAKE_COMPLETION_TEXT = "任务已执行完毕。我已完成上述操作。"
RECOVERY_EXHAUSTED_TEXT = "我连续多次收到没有有效内容的回复，暂时无法继续自动恢复。"


@dataclass(frozen=True)
class SheetSpec:
    name: str
    rows: tuple[tuple[object, ...], ...]


@dataclass(frozen=True)
class ExpectedFinding:
    sheet: str
    cell: str
    current_value: int | float | str
    correct_value: int | float | str
    error_type: str
    label: str


@dataclass(frozen=True)
class ScenarioDefinition:
    name: str
    report_prefix: str
    description: str
    required_sheets: tuple[str, ...]
    required_phrases: tuple[str, ...]
    required_intermediate_phrases: tuple[str, ...]
    expected_findings: tuple[ExpectedFinding, ...]
    minimum_tool_calls: int
    minimum_total_matches: int
    minimum_root_matches: int
    minimum_cascade_matches: int
    follow_up_prompt: str
    minimum_new_matches_after_follow_up: int
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
    xlsx_path: str
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
    first_turn_matched_count: int = 0
    final_turn_matched_count: int = 0
    incremental_matches_after_follow_up: int = 0
    first_turn_semantic_matched_count: int = 0
    final_turn_semantic_matched_count: int = 0
    incremental_semantic_matches_after_follow_up: int = 0


def parse_args() -> argparse.Namespace:
    parser = argparse.ArgumentParser(description="真实评估 silent turn 修复效果（支持复杂长任务场景）")
    parser.add_argument("--base-url", default=DEFAULT_BASE_URL, help="后端地址，例如 http://localhost:3000")
    parser.add_argument("--model", default=DEFAULT_MODEL, help="评估使用的模型")
    parser.add_argument("--mode", default=DEFAULT_MODE, help="会话模式，默认 coding")
    parser.add_argument(
        "--scenario",
        choices=("cross_sheet_audit", "long_chain_audit"),
        default=DEFAULT_SCENARIO,
        help="评估场景，默认 cross_sheet_audit",
    )
    parser.add_argument(
        "--timeout",
        type=int,
        default=DEFAULT_TIMEOUT_SECONDS,
        help="单次评估超时时间（秒），传 0 表示使用场景默认值",
    )
    parser.add_argument(
        "--xlsx-path",
        default="",
        help="保留参数；精确校验依赖内置夹具，当前不支持自定义 xlsx",
    )
    parser.add_argument("--disable-follow-up", action="store_true", help="只执行首轮，不验证同 session 的继续轮")
    parser.add_argument("--follow-up-prompt", default="", help="覆盖场景默认的继续轮提示词")
    parser.add_argument("--keep-fixture", action="store_true", help="保留自动生成的测试文件")
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


def xml_escape(value: str) -> str:
    return (
        value.replace("&", "&amp;")
        .replace("<", "&lt;")
        .replace(">", "&gt;")
        .replace('"', "&quot;")
        .replace("'", "&apos;")
    )


def inline_str_cell(cell_ref: str, value: str) -> str:
    return f'<c r="{cell_ref}" t="inlineStr"><is><t>{xml_escape(value)}</t></is></c>'


def format_numeric(value: int | float) -> str:
    if isinstance(value, int):
        return str(value)
    numeric = float(value)
    if numeric.is_integer():
        return str(int(numeric))
    return format(numeric, ".10g")


def number_cell(cell_ref: str, value: int | float) -> str:
    return f'<c r="{cell_ref}"><v>{format_numeric(value)}</v></c>'


def build_worksheet_xml(dimension: str, rows: list[str]) -> str:
    return """<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
    <dimension ref="%s"/>
    <sheetViews><sheetView workbookViewId="0"/></sheetViews>
    <sheetFormatPr defaultRowHeight="15"/>
    <sheetData>
        %s
    </sheetData>
</worksheet>""" % (dimension, "\n    ".join(rows))


def column_letter(index: int) -> str:
    letters = ""
    current = index
    while current > 0:
        current, remainder = divmod(current - 1, 26)
        letters = chr(ord("A") + remainder) + letters
    return letters


def build_sheet_xml(rows: tuple[tuple[object, ...], ...]) -> str:
    row_xml: list[str] = []
    max_columns = 0
    for row_index, row in enumerate(rows, start=1):
        max_columns = max(max_columns, len(row))
        cells: list[str] = []
        for column_index, value in enumerate(row, start=1):
            if value is None:
                continue
            cell_ref = f"{column_letter(column_index)}{row_index}"
            if isinstance(value, (int, float)) and not isinstance(value, bool):
                cells.append(number_cell(cell_ref, value))
            else:
                cells.append(inline_str_cell(cell_ref, str(value)))
        row_xml.append(f'<row r="{row_index}">' + "".join(cells) + "</row>")
    dimension = f"A1:{column_letter(max_columns)}{len(rows)}"
    return build_worksheet_xml(dimension, row_xml)


def create_workbook_xlsx(path: Path, sheets: tuple[SheetSpec, ...]) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    sheet_override_xml = "\n  ".join(
        f'<Override PartName="/xl/worksheets/sheet{index}.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>'
        for index, _ in enumerate(sheets, start=1)
    )
    workbook_sheet_xml = "\n        ".join(
        f'<sheet name="{xml_escape(sheet.name)}" sheetId="{index}" r:id="rId{index}"/>'
        for index, sheet in enumerate(sheets, start=1)
    )
    workbook_rel_xml = "\n  ".join(
        f'<Relationship Id="rId{index}" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet{index}.xml"/>'
        for index, _ in enumerate(sheets, start=1)
    )

    content_types = f"""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/>
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>
  {sheet_override_xml}
  <Override PartName="/docProps/core.xml" ContentType="application/vnd.openxmlformats-package.core-properties+xml"/>
  <Override PartName="/docProps/app.xml" ContentType="application/vnd.openxmlformats-officedocument.extended-properties+xml"/>
</Types>"""

    rels = """<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/>
  <Relationship Id="rId2" Type="http://schemas.openxmlformats.org/package/2006/relationships/metadata/core-properties" Target="docProps/core.xml"/>
  <Relationship Id="rId3" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/extended-properties" Target="docProps/app.xml"/>
</Relationships>"""

    workbook = f"""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships">
  <sheets>
        {workbook_sheet_xml}
  </sheets>
</workbook>"""

    workbook_rels = f"""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  {workbook_rel_xml}
</Relationships>"""

    created = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
    core = f"""<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<cp:coreProperties xmlns:cp="http://schemas.openxmlformats.org/package/2006/metadata/core-properties" xmlns:dc="http://purl.org/dc/elements/1.1/" xmlns:dcterms="http://purl.org/dc/terms/" xmlns:dcmitype="http://purl.org/dc/dcmitype/" xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance">
  <dc:creator>AuraMate</dc:creator>
  <cp:lastModifiedBy>AuraMate</cp:lastModifiedBy>
  <dcterms:created xsi:type="dcterms:W3CDTF">{created}</dcterms:created>
  <dcterms:modified xsi:type="dcterms:W3CDTF">{created}</dcterms:modified>
</cp:coreProperties>"""

    app = """<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Properties xmlns="http://schemas.openxmlformats.org/officeDocument/2006/extended-properties" xmlns:vt="http://schemas.openxmlformats.org/officeDocument/2006/docPropsVTypes">
  <Application>Microsoft Excel</Application>
</Properties>"""

    with zipfile.ZipFile(path, "w", compression=zipfile.ZIP_DEFLATED) as workbook_zip:
        workbook_zip.writestr("[Content_Types].xml", content_types)
        workbook_zip.writestr("_rels/.rels", rels)
        workbook_zip.writestr("xl/workbook.xml", workbook)
        workbook_zip.writestr("xl/_rels/workbook.xml.rels", workbook_rels)
        for index, sheet in enumerate(sheets, start=1):
            workbook_zip.writestr(f"xl/worksheets/sheet{index}.xml", build_sheet_xml(sheet.rows))
        workbook_zip.writestr("docProps/core.xml", core)
        workbook_zip.writestr("docProps/app.xml", app)


def create_cross_sheet_fixture_xlsx(path: Path) -> None:
    sheets = (
        SheetSpec(
            name="SalesDetail",
            rows=(
                ("OrderID", "Region", "Product", "Qty", "UnitPrice", "Discount", "NetAmount"),
                ("SO-001", "North", "Monitor", 5, 800, 0.05, 3800),
                ("SO-002", "East", "Keyboard", 12, 100, 0.10, 1800),
                ("SO-003", "East", "Mouse", 20, 35, 0, 700),
                ("SO-004", "South", "Dock", 4, 260, 0, 1040),
                ("SO-005", "North", "Headset", 6, 150, 0.20, 720),
                ("Rule", "NetAmount = Qty * UnitPrice * (1 - Discount)", None, None, None, None, None),
            ),
        ),
        SheetSpec(
            name="RegionSummary",
            rows=(
                ("Region", "Revenue"),
                ("North", 4520),
                ("East", 2500),
                ("South", 1040),
                ("GrandTotal", 8060),
                ("Check", "Compare this sheet with SalesDetail totals by region"),
            ),
        ),
        SheetSpec(
            name="MarginReview",
            rows=(
                ("Product", "Revenue", "Cost", "MarginRate"),
                ("Monitor", 3800, 3000, 0.2105),
                ("Keyboard", 1800, 900, 0.5),
                ("Mouse", 700, 300, 0.5714),
                ("Dock", 1040, 760, 0.2692),
                ("Headset", 720, 420, 0.4167),
                ("Overall", 8060, 5380, 0.3325),
                ("Hint", "Revenue and margin should stay consistent with SalesDetail and RegionSummary", None, None),
            ),
        ),
        SheetSpec(
            name="AuditNotes",
            rows=(
                ("ReviewGoal",),
                ("Find the root-cause error and every downstream impacted cell across all sheets.",),
                ("Do not stop after the first mismatch. Reconcile detail, regional totals, and margin analysis.",),
                ("Expected output should separate root cause from cascade impacts.",),
            ),
        ),
    )
    create_workbook_xlsx(path, sheets)


def create_long_chain_fixture_xlsx(path: Path) -> None:
    sheets = (
        SheetSpec(
            name="OrderDetail",
            rows=(
                ("OrderID", "Region", "Channel", "Product", "Qty", "UnitPrice", "Discount", "NetAmount"),
                ("ORD-001", "North", "Online", "Monitor", 3, 1000, 0.05, 2850),
                ("ORD-002", "East", "Online", "Keyboard", 18, 100, 0.10, 2160),
                ("ORD-003", "East", "Retail", "Mouse", 25, 40, 0, 1000),
                ("ORD-004", "South", "Retail", "Dock", 6, 250, 0.05, 1425),
                ("ORD-005", "North", "Online", "Headset", 10, 180, 0.15, 1530),
                ("ORD-006", "West", "Retail", "Camera", 4, 500, 0.10, 2000),
                ("ORD-007", "West", "Online", "Stand", 8, 120, 0, 960),
                ("Rule", "NetAmount = Qty * UnitPrice * (1 - Discount)", None, None, None, None, None, None),
            ),
        ),
        SheetSpec(
            name="ChannelSummary",
            rows=(
                ("Channel", "Revenue"),
                ("Online", 7500),
                ("Retail", 4425),
                ("GrandTotal", 11925),
                ("Check", "Must reconcile to OrderDetail and ExecutiveDashboard"),
            ),
        ),
        SheetSpec(
            name="RegionalRollup",
            rows=(
                ("Region", "Revenue"),
                ("North", 4380),
                ("East", 3160),
                ("South", 1425),
                ("West", 2960),
                ("GrandTotal", 11925),
            ),
        ),
        SheetSpec(
            name="ProductMargin",
            rows=(
                ("Product", "Revenue", "Cost", "MarginRate"),
                ("Monitor", 2850, 2100, 0.2632),
                ("Keyboard", 2160, 1260, 0.4167),
                ("Mouse", 1000, 430, 0.57),
                ("Dock", 1425, 900, 0.3684),
                ("Headset", 1530, 900, 0.4118),
                ("Camera", 2000, 1200, 0.4),
                ("Stand", 960, 500, 0.4792),
                ("Overall", 11925, 7290, 0.3887),
            ),
        ),
        SheetSpec(
            name="CashForecast",
            rows=(
                ("Month", "Receipts"),
                ("April", 6010),
                ("May", 2955),
                ("June", 2960),
                ("GrandTotal", 11925),
            ),
        ),
        SheetSpec(
            name="ExecutiveDashboard",
            rows=(
                ("Metric", "Value"),
                ("TotalRevenue", 11925),
                ("EastRevenue", 3160),
                ("OnlineRevenueShare", 0.6289),
                ("GrossMarginRate", 0.3887),
                ("RootCauseCount", 2),
            ),
        ),
        SheetSpec(
            name="AuditInstructions",
            rows=(
                ("Instruction",),
                ("Identify 2 root-cause errors first, then enumerate all downstream impacts.",),
                ("Do not stop after summary tables; continue to KPI and cash impact sheets.",),
                ("Final answer must include repair order and affected KPI recovery path.",),
            ),
        ),
    )
    create_workbook_xlsx(path, sheets)


CROSS_SHEET_SCENARIO = ScenarioDefinition(
    name="cross_sheet_audit",
    report_prefix="silent_turn_recovery_complex_real",
    description="复杂跨表附件场景，验证单根因与级联影响识别。",
    required_sheets=("SalesDetail", "RegionSummary", "MarginReview"),
    required_phrases=("根因", "同步恢复"),
    required_intermediate_phrases=("## 结构化核对", "### 明细核算", "### 区域汇总核对", "### 级联链路核对"),
    expected_findings=(
        ExpectedFinding("SalesDetail", "G3", 1800, 1080, "root", "SalesDetail 键盘净额根因错误"),
        ExpectedFinding("RegionSummary", "B3", 2500, 1780, "cascade", "East 区域汇总被污染"),
        ExpectedFinding("RegionSummary", "B5", 8060, 7340, "cascade", "总计被污染"),
        ExpectedFinding("MarginReview", "B3", 1800, 1080, "cascade", "Keyboard 收入被污染"),
        ExpectedFinding("MarginReview", "B7", 8060, 7340, "cascade", "Overall 收入被污染"),
        ExpectedFinding("MarginReview", "D7", 0.3325, 0.267, "cascade", "Overall 利润率被污染"),
    ),
    minimum_tool_calls=2,
    minimum_total_matches=4,
    minimum_root_matches=1,
    minimum_cascade_matches=3,
    follow_up_prompt="继续。若首轮已经完整，只补一行完整性确认和一行同步恢复结论；若仍有遗漏，再补齐尚未列出的错误单元格。不要重复整份报告，也不要询问下一步。",
    minimum_new_matches_after_follow_up=1,
    forbidden_tool_names=("delegate_task", "manage_worker", "wait_for_workers"),
    max_total_duration_seconds=360,
    recommended_timeout_seconds=300,
)


LONG_CHAIN_SCENARIO = ScenarioDefinition(
    name="long_chain_audit",
    report_prefix="silent_turn_recovery_long_chain_real",
    description="多工作表长链路经营复盘场景，验证长任务持续分析、跨表修复路径与长上下文稳定性。",
    required_sheets=(
        "OrderDetail",
        "ChannelSummary",
        "RegionalRollup",
        "ProductMargin",
        "CashForecast",
        "ExecutiveDashboard",
    ),
    required_phrases=("根因", "修复顺序", "同步恢复"),
    required_intermediate_phrases=(),
    expected_findings=(
        ExpectedFinding("OrderDetail", "H3", 2160, 1620, "root", "Keyboard 明细根因错误"),
        ExpectedFinding("OrderDetail", "H7", 2000, 1800, "root", "Camera 明细根因错误"),
        ExpectedFinding("ChannelSummary", "B2", 7500, 6960, "cascade", "Online 汇总被污染"),
        ExpectedFinding("ChannelSummary", "B3", 4425, 4225, "cascade", "Retail 汇总被污染"),
        ExpectedFinding("ChannelSummary", "B4", 11925, 11185, "cascade", "渠道总计被污染"),
        ExpectedFinding("RegionalRollup", "B3", 3160, 2620, "cascade", "East 区域被污染"),
        ExpectedFinding("RegionalRollup", "B5", 2960, 2760, "cascade", "West 区域被污染"),
        ExpectedFinding("RegionalRollup", "B6", 11925, 11185, "cascade", "区域总计被污染"),
        ExpectedFinding("ProductMargin", "B3", 2160, 1620, "cascade", "Keyboard 收入被污染"),
        ExpectedFinding("ProductMargin", "D3", 0.4167, 0.2222, "cascade", "Keyboard 利润率被污染"),
        ExpectedFinding("ProductMargin", "B7", 2000, 1800, "cascade", "Camera 收入被污染"),
        ExpectedFinding("ProductMargin", "D7", 0.4, 0.3333, "cascade", "Camera 利润率被污染"),
        ExpectedFinding("ProductMargin", "B9", 11925, 11185, "cascade", "产品总收入被污染"),
        ExpectedFinding("ProductMargin", "D9", 0.3887, 0.3482, "cascade", "总体毛利率被污染"),
        ExpectedFinding("CashForecast", "B2", 6010, 5470, "cascade", "四月回款被污染"),
        ExpectedFinding("CashForecast", "B4", 2960, 2760, "cascade", "六月回款被污染"),
        ExpectedFinding("CashForecast", "B5", 11925, 11185, "cascade", "回款总计被污染"),
        ExpectedFinding("ExecutiveDashboard", "B2", 11925, 11185, "cascade", "总营收 KPI 被污染"),
        ExpectedFinding("ExecutiveDashboard", "B3", 3160, 2620, "cascade", "East KPI 被污染"),
        ExpectedFinding("ExecutiveDashboard", "B4", 0.6289, 0.6223, "cascade", "线上收入占比被污染"),
        ExpectedFinding("ExecutiveDashboard", "B5", 0.3887, 0.3482, "cascade", "毛利率 KPI 被污染"),
    ),
    minimum_tool_calls=4,
    minimum_total_matches=10,
    minimum_root_matches=2,
    minimum_cascade_matches=8,
    follow_up_prompt="继续",
    minimum_new_matches_after_follow_up=2,
    forbidden_tool_names=("delegate_task", "manage_worker", "wait_for_workers"),
    max_total_duration_seconds=420,
    recommended_timeout_seconds=480,
)


SCENARIOS = {
    CROSS_SHEET_SCENARIO.name: CROSS_SHEET_SCENARIO,
    LONG_CHAIN_SCENARIO.name: LONG_CHAIN_SCENARIO,
}


def build_prompt(scenario: ScenarioDefinition, file_name: str) -> str:
    if scenario.name == "cross_sheet_audit":
        return (
            f"我附上了一个 Excel 审核包 {file_name}，里面至少有 3 张需要交叉核对的业务表。"
            "请像财务复核一样完成完整分析，不要在发现第一处错误后停止。"
            "你的任务是："
            "1. 先检查 SalesDetail 的明细计算逻辑；"
            "2. 再核对 RegionSummary 是否与明细汇总一致；"
            "3. 再核对 MarginReview 是否被上游错误连带污染；"
            "4. 最终至少列出 4 个错误单元格，并明确区分根因错误与级联错误；"
            "5. 对每个错误给出：工作表名、单元格、当前值、正确值、计算依据；"
            "6. 在最终结论前，必须先输出 `## 结构化核对`，并依次包含 `### 明细核算`、`### 区域汇总核对`、`### 级联链路核对` 三个小节；"
            "   - `明细核算` 必须逐行写出订单、实际单元格坐标、当前值、按公式重算值；"
            "   - `区域汇总核对` 必须逐项写出 RegionSummary 中 East 与 GrandTotal 的实际单元格坐标、当前值、按修正后明细重算值；"
            "   - `级联链路核对` 必须逐项写出 MarginReview 中 Keyboard 与 Overall 的实际单元格坐标、当前值、按修正后明细重算值；"
            "   - 所有坐标都必须以工作表真实行号为准，不能按肉眼顺序猜测；"
            "   - 三个小节都只写紧凑核对表，不要展开长篇推导；"
            "7. 最后用一句中文说明修正根因后哪些汇总指标会同步恢复。"
            "不要只输出思考过程；如果 Excel 工具失败，请自行改用其他可行方式继续完成分析。"
        )
    return (
        f"我附上了一个 Excel 经营复盘包 {file_name}，其中有 6 张以上互相引用的业务表。"
        "请把它当成长任务处理，不要因为发现局部错误就提前结束。"
        "你的任务是："
        "1. 先逐行检查 OrderDetail 的 NetAmount；"
        "2. 再核对 ChannelSummary 与 RegionalRollup；"
        "3. 再核对 ProductMargin 的收入与利润率；"
        "4. 再核对 CashForecast 与 ExecutiveDashboard 的 KPI；"
        "5. 明确指出 2 个根因错误，以及至少 8 个级联错误；"
        "6. 每个错误都要给出：工作表、单元格、当前值、正确值、计算依据；"
        "7. 最后写出修复顺序，并用一句中文说明修复根因后哪些 KPI 会同步恢复。"
        "不要只停留在摘要表；必须继续查到现金流和仪表盘层。"
        "如果某个工具失败，请自行切换到其他可行手段继续完成。"
    )


def build_payload(prompt: str, model: str, mode: str, session_id: str = "", xlsx_path: Path | None = None) -> dict[str, Any]:
    payload: dict[str, Any] = {
        "type": "chat",
        "content": prompt,
        "model": model,
        "mode": mode,
    }
    if session_id:
        payload["session_id"] = session_id
    if xlsx_path is not None:
        payload["attachments"] = [
            {
                "FilePath": str(xlsx_path),
                "FileName": xlsx_path.name,
                "MimeType": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
            }
        ]
    return payload


def make_report_path(custom_path: str, scenario: ScenarioDefinition) -> Path:
    if custom_path:
        return Path(custom_path)
    REPORTS_DIR.mkdir(parents=True, exist_ok=True)
    timestamp = time.strftime("%Y%m%d_%H%M%S")
    return REPORTS_DIR / f"{scenario.report_prefix}_{timestamp}.json"


def extract_answer_text(text: str) -> str:
    without_thinking = re.sub(r"<thinking>.*?</thinking>", "", text, flags=re.IGNORECASE | re.DOTALL)
    without_tags = re.sub(r"</?[^>]+>", "", without_thinking)
    normalized = re.sub(r"\n{3,}", "\n\n", without_tags)
    return normalized.strip()


def contains_any(text: str, markers: tuple[str, ...]) -> bool:
    return any(marker in text for marker in markers)


def normalize_text(text: str) -> str:
    return re.sub(r"\s+", " ", text.strip())


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
        aliases.add(f"{int(numeric)}.00")
    normalized_aliases: set[str] = set()
    for alias in aliases:
        cleaned = alias.rstrip("0").rstrip(".") if "." in alias else alias
        if cleaned:
            normalized_aliases.add(cleaned)
        normalized_aliases.add(alias)
    if 0 < abs(numeric) < 1:
        percent = numeric * 100
        percent_aliases = {
            f"{percent:.1f}%",
            f"{percent:.2f}%",
            f"{format(percent, '.10g')}%",
        }
        normalized_aliases.update(percent_aliases)
        normalized_aliases.update(alias.rstrip("0").rstrip(".") for alias in percent_aliases)
    return tuple(sorted(alias for alias in normalized_aliases if alias))


def finding_present(text: str, finding: ExpectedFinding) -> bool:
    aliases = numeric_aliases(finding.correct_value)
    lines = [line.strip() for line in text.splitlines() if line.strip()]
    combined_ref = f"{finding.sheet}!{finding.cell}"
    if combined_ref in text and any(alias in text for alias in aliases):
        return True

    for index in range(len(lines)):
        window = " ".join(lines[index : index + 3])
        if finding.sheet in window and finding.cell in window and any(alias in window for alias in aliases):
            return True

    normalized = normalize_text(text)
    patterns = (
        rf"{re.escape(finding.sheet)}.{{0,120}}{re.escape(finding.cell)}",
        rf"{re.escape(finding.cell)}.{{0,120}}{re.escape(finding.sheet)}",
    )
    for pattern in patterns:
        match = re.search(pattern, normalized)
        if match:
            segment = normalized[max(0, match.start() - 60) : match.end() + 120]
            if any(alias in segment for alias in aliases):
                return True
    return False


def finding_semantic_present(text: str, finding: ExpectedFinding) -> bool:
    current_aliases = numeric_aliases(finding.current_value)
    correct_aliases = numeric_aliases(finding.correct_value)
    lines = [line.strip() for line in text.splitlines() if line.strip()]

    for index in range(len(lines)):
        window = " ".join(lines[index : index + 4])
        if finding.sheet in window and any(alias in window for alias in current_aliases) and any(
            alias in window for alias in correct_aliases
        ):
            return True

    normalized = normalize_text(text)
    for match in re.finditer(re.escape(finding.sheet), normalized):
        segment = normalized[match.start() : match.start() + 240]
        if any(alias in segment for alias in current_aliases) and any(alias in segment for alias in correct_aliases):
            return True

    return False


def scenario_sheet_coverage(text: str, scenario: ScenarioDefinition) -> list[str]:
    return [sheet for sheet in scenario.required_sheets if sheet in text]


def collect_finding_matches(text: str, scenario: ScenarioDefinition, semantic: bool = False) -> tuple[list[str], list[str], int, int]:
    matched_findings: list[str] = []
    missing_findings: list[str] = []
    root_matches = 0
    cascade_matches = 0

    for finding in scenario.expected_findings:
        matched = finding_semantic_present(text, finding) if semantic else finding_present(text, finding)
        label = f"{finding.sheet}!{finding.cell} -> {finding.correct_value} ({finding.label})"
        if matched:
            matched_findings.append(label)
            if finding.error_type == "root":
                root_matches += 1
            else:
                cascade_matches += 1
        else:
            missing_findings.append(label)

    return matched_findings, missing_findings, root_matches, cascade_matches


def evaluate_turn(turn: TurnEvaluationReport, scenario: ScenarioDefinition) -> TurnEvaluationReport:
    raw_text = turn.final_text.strip()
    turn.answer_text = extract_answer_text(raw_text)
    matched, missing, _, _ = collect_finding_matches(turn.answer_text, scenario)
    semantic_matched, semantic_missing, _, _ = collect_finding_matches(turn.answer_text, scenario, semantic=True)
    turn.matched_findings = matched
    turn.missing_findings = missing
    turn.semantic_matched_findings = semantic_matched
    turn.semantic_missing_findings = semantic_missing
    return turn


def diagnose_tooling(report: EvaluationReport) -> None:
    tool_call_counts = Counter(call.name for call in report.tool_calls)
    report.tool_call_counts = dict(tool_call_counts)

    failed_results = [result for result in report.tool_results if result.is_error]
    if failed_results:
        report.diagnostics.append(f"工具调用存在失败结果 {len(failed_results)} 次")

    empty_calls = [call for call in report.tool_calls if not call.input.strip()]
    if len(empty_calls) >= 3:
        report.diagnostics.append(f"空参数工具调用偏多，共 {len(empty_calls)} 次")

    if tool_call_counts.get("run_command", 0) >= 3 and tool_call_counts.get("xlsx", 0) == 0:
        report.diagnostics.append("主要依赖命令行回退，未成功利用 xlsx 工具")
    elif tool_call_counts.get("run_command", 0) >= 4 and tool_call_counts.get("xlsx", 0) >= 1:
        report.diagnostics.append("出现较重的命令行回退，说明原生表格工具仍不稳定")

    if len(report.thought_events) >= 20:
        report.diagnostics.append(f"长思考事件较多，共 {len(report.thought_events)} 条，可能存在任务拖长现象")


def evaluate_report(report: EvaluationReport, scenario: ScenarioDefinition) -> EvaluationReport:
    for index, turn in enumerate(report.turn_reports):
        report.turn_reports[index] = evaluate_turn(turn, scenario)

    if report.turn_reports:
        report.first_turn_matched_count = len(report.turn_reports[0].matched_findings)
        report.final_turn_matched_count = len(report.turn_reports[-1].matched_findings)
        report.first_turn_semantic_matched_count = len(report.turn_reports[0].semantic_matched_findings)
        report.final_turn_semantic_matched_count = len(report.turn_reports[-1].semantic_matched_findings)

    if len(report.turn_reports) > 1 and report.session_id:
        report.session_reused = all(turn.session_id == report.session_id for turn in report.turn_reports[1:])

    report.done_received = bool(report.turn_reports) and all(turn.done_received for turn in report.turn_reports)
    report.error_event = " | ".join(turn.error_event for turn in report.turn_reports if turn.error_event)

    raw_text = report.final_text.strip()
    report.answer_text = extract_answer_text(raw_text)
    text = report.answer_text.strip()

    if not report.done_received:
        report.failure_reasons.append("未收到 done 事件，评估超时或链路中断")
    if report.error_event:
        report.failure_reasons.append(f"收到 error 事件: {report.error_event}")
    if FAKE_COMPLETION_TEXT in raw_text:
        report.failure_reasons.append("最终回答仍出现旧的假完成文案")
    if RECOVERY_EXHAUSTED_TEXT in raw_text:
        report.failure_reasons.append("触发了 silent turn 恢复耗尽兜底")
    if not report.tool_calls:
        report.failure_reasons.append("真实附件场景下没有发生任何工具调用")
    if len(report.tool_calls) < scenario.minimum_tool_calls:
        report.failure_reasons.append(
            f"工具调用次数少于场景要求，实际 {len(report.tool_calls)} 次，要求至少 {scenario.minimum_tool_calls} 次"
        )
    if not any(not result.is_error and result.content.strip() for result in report.tool_results):
        report.failure_reasons.append("没有拿到任何成功的工具结果，无法证明真实完成了分析")
    forbidden_tools = sorted({call.name for call in report.tool_calls if call.name in scenario.forbidden_tool_names})
    if forbidden_tools:
        report.failure_reasons.append(f"单主 agent 场景出现禁止工具: {forbidden_tools}")
    if report.duration_seconds > scenario.max_total_duration_seconds:
        report.failure_reasons.append(
            f"单主 agent 长链路耗时过长: {report.duration_seconds:.2f}s > {scenario.max_total_duration_seconds}s"
        )
    if text == "":
        report.failure_reasons.append("最终可见回答为空")

    covered_sheets = scenario_sheet_coverage(text, scenario)
    if len(covered_sheets) < len(scenario.required_sheets):
        report.failure_reasons.append(
            f"最终回答覆盖工作表不完整，命中 {len(covered_sheets)}/{len(scenario.required_sheets)} 张核心工作表"
        )

    for phrase in scenario.required_phrases:
        if phrase not in text:
            report.failure_reasons.append(f"最终回答缺少关键表述：{phrase}")
    for phrase in scenario.required_intermediate_phrases:
        if phrase not in text:
            report.failure_reasons.append(f"最终回答缺少结构化中间检查：{phrase}")

    matched_findings, missing_findings, _, _ = collect_finding_matches(text, scenario)
    semantic_matched_findings, semantic_missing_findings, root_matches, cascade_matches = collect_finding_matches(
        text, scenario, semantic=True
    )
    report.matched_findings = matched_findings
    report.missing_findings = missing_findings
    report.semantic_matched_findings = semantic_matched_findings
    report.semantic_missing_findings = semantic_missing_findings

    total_matches = len(report.semantic_matched_findings)
    if total_matches < scenario.minimum_total_matches:
        report.failure_reasons.append(
            f"关键错误语义命中不足，实际命中 {total_matches} 条，要求至少 {scenario.minimum_total_matches} 条"
        )
    if root_matches < scenario.minimum_root_matches:
        report.failure_reasons.append(
            f"根因错误语义命中不足，实际命中 {root_matches} 条，要求至少 {scenario.minimum_root_matches} 条"
        )
    if cascade_matches < scenario.minimum_cascade_matches:
        report.failure_reasons.append(
            f"级联错误语义命中不足，实际命中 {cascade_matches} 条，要求至少 {scenario.minimum_cascade_matches} 条"
        )

    strict_total_matches = len(report.matched_findings)
    if strict_total_matches < total_matches:
        report.diagnostics.append(
            f"存在 {total_matches - strict_total_matches} 条值正确但坐标未严格命中的结果，建议结合语义命中和严格命中一起评估"
        )

    if len(report.turn_reports) > 1:
        first_turn = report.turn_reports[0]
        first_turn_complete = (
            len(first_turn.semantic_matched_findings) >= scenario.minimum_total_matches
            and len(scenario_sheet_coverage(first_turn.answer_text, scenario)) >= len(scenario.required_sheets)
            and all(phrase in first_turn.answer_text for phrase in scenario.required_phrases)
        )
        recovered_required_phrase = any(
            phrase not in first_turn.answer_text and phrase in text for phrase in scenario.required_phrases
        )
        report.incremental_matches_after_follow_up = max(0, strict_total_matches - len(first_turn.matched_findings))
        report.incremental_semantic_matches_after_follow_up = max(
            0, total_matches - len(first_turn.semantic_matched_findings)
        )

        if not report.session_reused:
            report.failure_reasons.append("继续轮未复用同一 session，无法验证长链连续性")

        if not first_turn_complete and not report.turn_reports[-1].answer_text.strip():
            report.failure_reasons.append("继续轮没有产生可见回答，无法验证连续推进能力")

        if (
            not first_turn_complete
            and report.incremental_semantic_matches_after_follow_up < scenario.minimum_new_matches_after_follow_up
            and not recovered_required_phrase
        ):
            report.failure_reasons.append(
                "继续轮未体现足够增量，"
                f"首轮语义命中 {len(first_turn.semantic_matched_findings)} 条，"
                f"合并后语义命中 {total_matches} 条，"
                f"新增 {report.incremental_semantic_matches_after_follow_up} 条，"
                f"要求至少新增 {scenario.minimum_new_matches_after_follow_up} 条或补齐缺失关键表述"
            )

    diagnose_tooling(report)
    report.passed = not report.failure_reasons
    return report


async def run_turn(
    args: argparse.Namespace,
    scenario: ScenarioDefinition,
    prompt: str,
    label: str,
    session_id: str = "",
    xlsx_path: Path | None = None,
) -> TurnEvaluationReport:
    ws_url = build_ws_url(args.base_url)
    started_at = time.time()

    turn = TurnEvaluationReport(label=label, prompt=prompt, session_id=session_id)
    payload = build_payload(prompt=prompt, model=args.model, mode=args.mode, session_id=session_id, xlsx_path=xlsx_path)

    print(f"[INFO] Connecting to {ws_url}")
    print(f"[INFO] Scenario: {scenario.name} | {scenario.description}")
    print(f"[INFO] Turn: {label}")
    if session_id:
        print(f"[INFO] Session ID: {session_id}")
    else:
        print("[INFO] Session ID: <auto-create>")
    if xlsx_path is not None:
        print(f"[INFO] Attachment: {xlsx_path}")

    async with websockets.connect(ws_url, max_size=8 * 1024 * 1024) as websocket:
        await websocket.send(json.dumps(payload, ensure_ascii=False))

        timeout_seconds = args.timeout or scenario.recommended_timeout_seconds
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
                preview = record.content.replace("\n", " ")[:240]
                print(f"\n[TOOL_RESULT] {record.name} | error={record.is_error} | {preview}")
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
    return evaluate_turn(turn, scenario)


def merge_turn_into_report(report: EvaluationReport, turn: TurnEvaluationReport) -> None:
    report.turn_reports.append(turn)
    if not report.session_id and turn.session_id:
        report.session_id = turn.session_id
    report.final_text = "\n\n".join(part for part in (report.final_text, turn.final_text) if part)
    report.tool_calls.extend(turn.tool_calls)
    report.tool_results.extend(turn.tool_results)
    report.thought_events.extend(turn.thought_events)


async def run_evaluation(args: argparse.Namespace, scenario: ScenarioDefinition, xlsx_path: Path, follow_up_prompt: str) -> EvaluationReport:
    ws_url = build_ws_url(args.base_url)
    initial_prompt = build_prompt(scenario, xlsx_path.name)

    report = EvaluationReport(
        scenario=scenario.name,
        session_id="",
        ws_url=ws_url,
        model=args.model,
        mode=args.mode,
        duration_seconds=0.0,
        xlsx_path=str(xlsx_path),
        final_text="",
        answer_text="",
        passed=False,
    )

    started_at = time.time()
    initial_turn = await run_turn(
        args=args,
        scenario=scenario,
        prompt=initial_prompt,
        label="initial",
        xlsx_path=xlsx_path,
    )
    merge_turn_into_report(report, initial_turn)

    if follow_up_prompt:
        if report.session_id:
            follow_up_turn = await run_turn(
                args=args,
                scenario=scenario,
                prompt=follow_up_prompt,
                label="follow_up",
                session_id=report.session_id,
            )
            merge_turn_into_report(report, follow_up_turn)
        else:
            report.diagnostics.append("首轮未返回 session_id，已跳过继续轮验证")

    report.duration_seconds = time.time() - started_at
    return evaluate_report(report, scenario)


def save_report(report: EvaluationReport, report_path: Path) -> None:
    report_path.parent.mkdir(parents=True, exist_ok=True)
    serializable = asdict(report)
    report_path.write_text(json.dumps(serializable, ensure_ascii=False, indent=2), encoding="utf-8")


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
    if len(report.turn_reports) > 1:
        print(f"SessionReuse: {report.session_reused}")
        print(f"FirstMatched: strict={report.first_turn_matched_count} semantic={report.first_turn_semantic_matched_count}")
        print(f"FinalMatched: strict={report.final_turn_matched_count} semantic={report.final_turn_semantic_matched_count}")
        print(
            f"MatchDelta  : strict={report.incremental_matches_after_follow_up} "
            f"semantic={report.incremental_semantic_matches_after_follow_up}"
        )
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
        if report.missing_findings:
            print("Missing Findings:")
            for finding in report.missing_findings[:12]:
                print(f"- {finding}")


def create_fixture_for_scenario(path: Path, scenario: ScenarioDefinition) -> None:
    if scenario.name == CROSS_SHEET_SCENARIO.name:
        create_cross_sheet_fixture_xlsx(path)
        return
    create_long_chain_fixture_xlsx(path)


def resolve_xlsx_path(custom_path: str, scenario: ScenarioDefinition) -> tuple[Path, bool]:
    if custom_path:
        raise ValueError("精确校验依赖内置夹具真值，当前脚本不支持 --xlsx-path")

    WORKSPACE_DIR.mkdir(parents=True, exist_ok=True)
    path = WORKSPACE_DIR / f"{scenario.name}_{uuid.uuid4().hex[:8]}.xlsx"
    create_fixture_for_scenario(path, scenario)
    return path, True


async def async_main() -> int:
    args = parse_args()
    scenario = SCENARIOS[args.scenario]
    follow_up_prompt = ""
    if not args.disable_follow_up:
        follow_up_prompt = args.follow_up_prompt or scenario.follow_up_prompt
    ensure_backend_ready(args.base_url)
    xlsx_path, generated = resolve_xlsx_path(args.xlsx_path, scenario)
    report_path = make_report_path(args.report_path, scenario)

    try:
        report = await run_evaluation(args, scenario, xlsx_path, follow_up_prompt)
        save_report(report, report_path)
        print_summary(report, report_path)
        return 0 if report.passed else 1
    finally:
        if generated and not args.keep_fixture and xlsx_path.exists():
            xlsx_path.unlink()


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
