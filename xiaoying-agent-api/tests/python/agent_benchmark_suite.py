from __future__ import annotations

from dataclasses import asdict, dataclass, field
from pathlib import Path
from typing import Any


REPO_ROOT = Path(__file__).resolve().parents[3]


@dataclass(frozen=True)
class ScriptTarget:
    script: str
    extra_args: tuple[str, ...] = ()
    report_glob: str = ""
    pass_common_args: bool = False
    report_arg_supported: bool = False


@dataclass(frozen=True)
class BenchmarkCase:
    case_id: str
    name: str
    category: str
    scenario_type: str
    automation_level: str
    objective: str
    tuning_focus: tuple[str, ...]
    tags: tuple[str, ...]
    acceptance_criteria: tuple[str, ...]
    failure_signals: tuple[str, ...]
    estimated_minutes: int
    prompt_summary: str
    expected_outputs: tuple[str, ...] = ()
    script_target: ScriptTarget | None = None
    notes: str = ""

    def to_dict(self) -> dict[str, Any]:
        payload = asdict(self)
        if self.script_target is not None:
            payload["script_target"]["script"] = self.script_path.as_posix()
        return payload

    @property
    def script_path(self) -> Path:
        if self.script_target is None:
            raise ValueError(f"case {self.case_id} does not have a script target")
        return REPO_ROOT / self.script_target.script


SUITE_DIMENSIONS: tuple[str, ...] = (
    "功能准确性",
    "工程鲁棒性",
    "资源效率",
    "工具调用质量",
    "长链稳定性",
    "环境适应性",
    "产物可用性",
)


READY_CASES: tuple[BenchmarkCase, ...] = (
    BenchmarkCase(
        case_id="correction_loop_handoff",
        name="纠错模式复核收口",
        category="correction_recovery",
        scenario_type="真实场景",
        automation_level="ready",
        objective="验证模型在纠错模式下连续复核达到边界后，会停止重复排查并向用户索取新的证据。",
        tuning_focus=("纠错模式", "重复复核收口", "多轮会话闭环"),
        tags=("correction", "continuation", "handoff", "regression"),
        acceptance_criteria=(
            "真实纠错轮会发生成功工具调用",
            "第 6 次成功只读复核后返回收口提示并结束当前轮",
            "task_state 进入 waiting_user 而不是继续 active",
        ),
        failure_signals=(
            "同一纠错路径继续无限复核",
            "最终回复继续复述用户指令",
            "边界轮没有 done 或没有 waiting_user",
        ),
        estimated_minutes=3,
        prompt_summary="通过真实 WebSocket 会话、真实工具调用和边界状态推进，验证纠错循环能在第 6 次复核时稳定收口。",
        expected_outputs=("JSON 报告", "多轮会话轨迹", "最终 task_state"),
        script_target=ScriptTarget(
            script="backend/tests/python/eval_correction_loop_handoff_real.py",
            report_glob="backend/tests/python/reports/correction_loop_handoff_real_*.json",
            pass_common_args=True,
            report_arg_supported=True,
        ),
    ),
    BenchmarkCase(
        case_id="boundary_reach",
        name="热区边界不过早折叠",
        category="context_window",
        scenario_type="专项回归",
        automation_level="ready",
        objective="验证上下文逼近热区阈值时不会过早裁剪，保证长任务阅读仍可线性推进。",
        tuning_focus=("热区裁剪", "提示保真", "长上下文增长曲线"),
        tags=("token", "read", "regression", "long-context"),
        acceptance_criteria=(
            "达到阈值前 prompt_tokens 增量不低于首段增量的 65%",
            "read 轨迹连续且不存在异常回退",
        ),
        failure_signals=(
            "上下文在未越界前提前塌缩",
            "长文件阅读段数异常减少",
        ),
        estimated_minutes=3,
        prompt_summary="读取多段大文件并验证热区边界前后的 token 增量曲线。",
        expected_outputs=("控制台 token 表", "异常读取轨迹"),
        script_target=ScriptTarget(
            script="backend/tests/python/test_agent_optimization.py",
            extra_args=("--only", "boundary_reach"),
            pass_common_args=True,
            report_arg_supported=True,
        ),
    ),
    BenchmarkCase(
        case_id="boundary_exceed",
        name="越界后开始折叠",
        category="context_window",
        scenario_type="专项回归",
        automation_level="ready",
        objective="验证超过热区预算后上下文增长速率显著放缓，避免无限膨胀。",
        tuning_focus=("热区折叠", "冷区裁剪", "成本控制"),
        tags=("token", "budget", "long-context", "regression"),
        acceptance_criteria=(
            "越界后 prompt_tokens 增量均值不高于越界前均值的 80%",
            "单轮总 token 不出现异常爆炸",
        ),
        failure_signals=(
            "越界后上下文仍近线性暴涨",
            "成本控制失效",
        ),
        estimated_minutes=3,
        prompt_summary="持续追加读取超过热区预算的大文件，观察折叠是否按预期触发。",
        expected_outputs=("控制台 token 表", "越界前后增量对比"),
        script_target=ScriptTarget(
            script="backend/tests/python/test_agent_optimization.py",
            extra_args=("--only", "boundary_exceed"),
            pass_common_args=True,
            report_arg_supported=True,
        ),
    ),
    BenchmarkCase(
        case_id="overlap_guard",
        name="重叠读取防抖",
        category="tool_routing",
        scenario_type="专项回归",
        automation_level="ready",
        objective="验证 read 调用的高度重叠、完全重复、顺序回退都能被识别并收敛。",
        tuning_focus=("读取规划", "抖动治理", "工具调用质量"),
        tags=("read", "overlap", "debounce", "regression"),
        acceptance_criteria=(
            "完全重复读取为 0",
            "高度重叠读取出现时必须及时停机或收敛",
            "read 次数不超过脚本阈值",
        ),
        failure_signals=(
            "同一区间反复读取",
            "offset 出现明显回退抖动",
        ),
        estimated_minutes=3,
        prompt_summary="故意制造大文件相邻区间高重叠诱因，验证读取规划与优雅停机。",
        expected_outputs=("读取轨迹表", "异常分类结果"),
        script_target=ScriptTarget(
            script="backend/tests/python/test_agent_optimization.py",
            extra_args=("--only", "overlap"),
            pass_common_args=True,
            report_arg_supported=True,
        ),
    ),
    BenchmarkCase(
        case_id="silent_execution",
        name="静默执行链路",
        category="execution_hygiene",
        scenario_type="专项回归",
        automation_level="ready",
        objective="验证多步工具执行之间没有插入无效正文，确保执行链路简洁可控。",
        tuning_focus=("静默执行", "工具间无正文", "执行链路"),
        tags=("silence", "tool", "execution", "regression"),
        acceptance_criteria=(
            "首个工具调用与最后一个工具调用之间正文为空",
            "创建、运行、停止命令都能被正确记录",
        ),
        failure_signals=(
            "工具之间插入过渡性废话",
            "执行中断后无法统一总结",
        ),
        estimated_minutes=3,
        prompt_summary="创建并运行可停止脚本，验证写入、运行、停止之间保持静默。",
        expected_outputs=("时间线日志", "静默断言结果"),
        script_target=ScriptTarget(
            script="backend/tests/python/test_agent_optimization.py",
            extra_args=("--only", "silence"),
            pass_common_args=True,
            report_arg_supported=True,
        ),
    ),
    BenchmarkCase(
        case_id="cross_sheet_audit",
        name="跨表根因审计",
        category="reasoning_read_only",
        scenario_type="真实场景",
        automation_level="ready",
        objective="验证只读附件审计时，模型能区分根因错误与级联错误，而不是只报结论。",
        tuning_focus=("值与定位", "只读诊断", "附件推理"),
        tags=("xlsx", "read-only", "root-cause", "multi-sheet"),
        acceptance_criteria=(
            "至少命中 1 个根因错误和 3 个级联错误",
            "最终回答覆盖所有核心工作表",
        ),
        failure_signals=(
            "值正确但单元格定位错误",
            "发现第一处错误后提前结束",
        ),
        estimated_minutes=5,
        prompt_summary="对多张相互引用的 Excel 表进行财务式审计，输出根因与级联影响。",
        expected_outputs=("JSON 报告", "strict/semantic 命中统计"),
        script_target=ScriptTarget(
            script="backend/tests/python/eval_silent_turn_recovery_real.py",
            extra_args=("--scenario", "cross_sheet_audit"),
            report_glob="backend/tests/python/reports/silent_turn_recovery_complex_real_*.json",
            pass_common_args=True,
            report_arg_supported=True,
        ),
    ),
    BenchmarkCase(
        case_id="long_chain_audit",
        name="多表长链经营复盘",
        category="reasoning_long_chain",
        scenario_type="真实场景",
        automation_level="ready",
        objective="验证长任务连续分析能力，关注长链持续性、跨表修复路径和多轮继续能力。",
        tuning_focus=("长链稳定性", "多轮继续", "只读诊断"),
        tags=("xlsx", "long-chain", "continuation", "multi-turn", "single-agent"),
        acceptance_criteria=(
            "至少命中 2 个根因错误和 8 个级联错误",
            "继续轮带来新增有效命中",
        ),
        failure_signals=(
            "长链中途放弃下游表",
            "继续轮只复述首轮内容",
        ),
        estimated_minutes=8,
        prompt_summary="对 6 张以上互相引用的业务表做长链经营复盘，验证继续轮增量分析。",
        expected_outputs=("JSON 报告", "首轮/继续轮增量统计"),
        script_target=ScriptTarget(
            script="backend/tests/python/eval_silent_turn_recovery_real.py",
            extra_args=("--scenario", "long_chain_audit"),
            report_glob="backend/tests/python/reports/silent_turn_recovery_long_chain_real_*.json",
            pass_common_args=True,
            report_arg_supported=True,
        ),
    ),
    BenchmarkCase(
        case_id="mixed_release_triage",
        name="长混合发布事故排查",
        category="reasoning_mixed_long_chain",
        scenario_type="真实场景",
        automation_level="ready",
        objective="验证代码、配置、日志、文档、测试、分析产物混合场景下的只读长链排查质量。",
        tuning_focus=("证据保真", "关键文件覆盖", "混合任务稳定性"),
        tags=("logs", "config", "docs", "tests", "long-chain", "mixed", "single-agent"),
        acceptance_criteria=(
            "至少命中 2 个根因和 6 个级联影响",
            "最终回答覆盖全部关键文件",
            "严格命中优先于语义命中",
        ),
        failure_signals=(
            "logs/order_sync.log 严格标记未保留",
            "ops/risk_isolation_notes.md 未进入最终答案",
            "thought 事件数量过高",
        ),
        estimated_minutes=10,
        prompt_summary="跨代码、配置、日志、文档与测试基线的长链发布事故排查。",
        expected_outputs=("JSON 报告", "strict/semantic 差异", "tool_call 统计"),
        script_target=ScriptTarget(
            script="backend/tests/python/eval_mixed_long_chain_real.py",
            report_glob="backend/tests/python/reports/mixed_long_chain_real_*.json",
            pass_common_args=True,
            report_arg_supported=True,
        ),
        notes="当前残留不稳定点主要集中在 strict marker 保真与关键文件覆盖完整性。",
    ),
    BenchmarkCase(
        case_id="single_agent_refactor_long_chain",
        name="单主链纯代码重构",
        category="coding_long_chain",
        scenario_type="真实场景",
        automation_level="ready",
        objective="验证单主 agent 在真实多文件重构任务中的持续推进能力，而不是只用审计/排障任务间接代表代码重构能力。",
        tuning_focus=("多文件重构", "单主链持续性", "验证闭环"),
        tags=("coding", "refactor", "long-chain", "single-agent", "multi-turn"),
        acceptance_criteria=(
            "真实完成共享 helper 提取并更新多个调用方",
            "补齐边界测试并通过最终 unittest",
            "全程不调用子 agent 工具",
        ),
        failure_signals=(
            "只描述方案但未实际改文件",
            "重构后仍保留重复逻辑",
            "继续轮未复用同一 session 或未重新验证",
        ),
        estimated_minutes=8,
        prompt_summary="在一个小型 Python 项目中执行跨模块 helper 提取、调用方改造和测试补齐，验证单主 agent 的长链重构能力。",
        expected_outputs=("JSON 报告", "重构后代码文件", "最终 unittest 结果"),
        script_target=ScriptTarget(
            script="backend/tests/python/eval_single_agent_refactor_long_chain_real.py",
            report_glob="backend/tests/python/reports/single_agent_refactor_long_chain_real_*.json",
            pass_common_args=True,
            report_arg_supported=True,
        ),
    ),
)


LEGACY_READY_CASES: tuple[BenchmarkCase, ...] = (
    BenchmarkCase(
        case_id="manage_plan_autonomous_lifecycle",
        name="计划管理自然生命周期",
        category="task_management",
        scenario_type="专项回归",
        automation_level="ready",
        objective="验证模型在未被显式要求调用 manage_plan 时，能否自主创建计划、推进状态并在任务结束时归档。",
        tuning_focus=("计划生命周期", "会话续接", "工具调用质量"),
        tags=("plan", "todo", "continuation", "regression"),
        acceptance_criteria=(
            "首轮自然创建计划",
            "出现按 id 的 in_progress 与完成更新",
            "最终自然归档计划",
        ),
        failure_signals=(
            "只创建计划但不推进状态",
            "完成后未归档",
            "后续更新依赖 index 而不是稳定 id",
        ),
        estimated_minutes=4,
        prompt_summary="在不点名 manage_plan 的真实审查任务中，验证计划从创建、推进到收尾的自然闭环。",
        expected_outputs=("JSON 报告", "自然计划生命周期轨迹"),
        script_target=ScriptTarget(
            script="backend/tests/python/eval_manage_plan_session_real.py",
            extra_args=("--scenario", "autonomous_lifecycle"),
            report_glob="backend/tests/python/reports/manage_plan_autonomous_lifecycle_real_*.json",
            pass_common_args=True,
            report_arg_supported=True,
        ),
    ),
    BenchmarkCase(
        case_id="github_large_repo_refactor",
        name="GitHub 大仓真实重构",
        category="coding_real_repo",
        scenario_type="真实仓库",
        automation_level="ready",
        objective="验证 Agent 能否在从 GitHub 拉取的真实大型代码仓库中完成可验证的重构闭环。",
        tuning_focus=("真实仓库理解", "多文件重构", "验证闭环"),
        tags=("github", "real-repo", "refactor", "coding", "single-agent"),
        acceptance_criteria=(
            "仓库按固定 ref 拉取并记录 source commit",
            "产生可审计 diff 且命中预期路径",
            "完成配置的验证命令并保留日志",
        ),
        failure_signals=(
            "只修改局部文本但未完成真实重构",
            "仓库来源或 commit 未固定，导致结果不可复现",
            "没有验证命令，无法判断行为正确性",
        ),
        estimated_minutes=20,
        prompt_summary="从 GitHub 下载固定 ref 的真实代码仓库，执行重构任务并保留 diff、验证日志和结构化报告。",
        expected_outputs=("JSON 报告", "git diff", "验证命令结果"),
        script_target=ScriptTarget(
            script="backend/tests/python/eval_github_repo_refactor_real.py",
            extra_args=(
                "--profile",
                "backend/tests/python/profiles/github_large_repo_refactor/django_http_allowed_hosts.json",
            ),
            report_glob="backend/tests/python/reports/github_large_repo_refactor_*.json",
            pass_common_args=True,
            report_arg_supported=True,
        ),
    ),
    BenchmarkCase(
        case_id="github_large_repo_refactor_flask_static_sendfile",
        name="GitHub 大仓复杂静态文件重构",
        category="coding_real_repo",
        scenario_type="真实仓库",
        automation_level="ready",
        objective="验证 Agent 能否在真实 Flask 仓库中完成跨 helper、两个调用方和多测试文件的可验证重构。",
        tuning_focus=("真实仓库理解", "跨模块重构", "行为验证", "改动边界控制"),
        tags=("github", "real-repo", "refactor", "coding", "single-agent", "complex"),
        acceptance_criteria=(
            "固定 Flask commit 并记录 source commit",
            "提取共享静态文件 helper 并更新 App 与 Blueprint 调用方",
            "只修改 profile 允许的 5 个文件",
            "新增 pytest 回归并完成全部验证命令",
        ),
        failure_signals=(
            "只改一个调用方，重复逻辑仍保留",
            "修改了无关文件或扩大公开 API",
            "新增测试缺失或未主动运行验证命令",
        ),
        estimated_minutes=25,
        prompt_summary="从 Flask 固定 commit 执行静态文件发送路径重构，覆盖 helper、App、Blueprint 和两处回归测试。",
        expected_outputs=("JSON 报告", "git diff", "pytest 验证结果", "未允许修改路径检查"),
        script_target=ScriptTarget(
            script="backend/tests/python/eval_github_repo_refactor_real.py",
            extra_args=(
                "--profile",
                "backend/tests/python/profiles/github_large_repo_refactor/flask_static_sendfile.json",
            ),
            report_glob="backend/tests/python/reports/github_large_repo_refactor_flask_static_sendfile_*.json",
            pass_common_args=True,
            report_arg_supported=True,
        ),
    ),
    BenchmarkCase(
        case_id="github_large_repo_refactor_flask_async_dispatch_deep_chain",
        name="GitHub 大仓完整长链 async dispatch 重构",
        category="coding_real_repo",
        scenario_type="真实仓库",
        automation_level="ready",
        objective="验证 Agent 能否在真实 Flask 仓库中跨请求分发、上下文、模板、信号和视图层完成三轮长链重构闭环。",
        tuning_focus=("完整长链重构", "跨模块调用链", "严格结果审查", "多轮续接验证"),
        tags=("github", "real-repo", "refactor", "coding", "single-agent", "deep-chain", "strict"),
        acceptance_criteria=(
            "固定 Flask commit 并记录 source commit",
            "三轮会话都复用同一 session 且主动重新验证",
            "修改正好 8 个允许文件并覆盖全部预期读取路径",
            "新增共享私有 helper 并收敛 async dispatch 调用链",
            "pytest、py_compile 和 diff 检查全部通过",
        ),
        failure_signals=(
            "续接轮只总结不继续检查和验证",
            "只做局部替换，ctx、templating 或 views 调用链遗漏",
            "修改无关文件或扩大公开 API",
            "marker 命中但行为测试失败",
        ),
        estimated_minutes=45,
        prompt_summary="从 Flask 固定 commit 执行三轮 async dispatch 调用链重构，覆盖 8 个文件、严格改动边界、每轮验证和最终审查。",
        expected_outputs=("JSON 报告", "三轮 tool/verification 轨迹", "git diff", "pytest 验证结果", "严格失败原因"),
        script_target=ScriptTarget(
            script="backend/tests/python/eval_github_repo_refactor_real.py",
            extra_args=(
                "--profile",
                "backend/tests/python/profiles/github_large_repo_refactor/flask_async_dispatch_deep_chain.json",
            ),
            report_glob="backend/tests/python/reports/github_large_repo_refactor_flask_async_dispatch_deep_chain_*.json",
            pass_common_args=True,
            report_arg_supported=True,
        ),
    ),
    BenchmarkCase(
        case_id="sota_diversity_v5",
        name="SOTA 多样化综合验证 v5",
        category="general_capability",
        scenario_type="历史代表场景",
        automation_level="ready",
        objective="保留 SOTA 综合验证里最完整的一版，用来覆盖多工具、多任务、多产物输出能力。",
        tuning_focus=("综合任务", "多工具编排", "产物落地"),
        tags=("sota", "general", "multi-tool", "legacy-curated"),
        acceptance_criteria=(
            "脚本正常完成并返回 0",
            "核心场景都能产出可验证结果",
        ),
        failure_signals=(
            "综合场景中途崩溃",
            "核心产物缺失",
        ),
        estimated_minutes=10,
        prompt_summary="保留 SOTA 系列中最完整的综合能力回归脚本。",
        expected_outputs=("控制台结果", "场景验证输出"),
        script_target=ScriptTarget(script="backend/tests/python/test_sota_diversity_v5.py"),
    ),
    BenchmarkCase(
        case_id="cache_hit_analysis",
        name="缓存命中率分析",
        category="cache_efficiency",
        scenario_type="专项回归",
        automation_level="ready",
        objective="分析多轮请求的缓存命中情况，作为成本控制和上下文复用指标。",
        tuning_focus=("缓存复用", "成本控制", "多轮上下文"),
        tags=("cache", "token", "efficiency", "legacy-curated"),
        acceptance_criteria=(
            "脚本正常完成并打印缓存分析",
            "能够输出缓存命中相关指标",
        ),
        failure_signals=(
            "无法获取 usage 或缓存字段",
            "多轮缓存分析失真",
        ),
        estimated_minutes=4,
        prompt_summary="保留缓存分析脚本，覆盖 cache hit 观察能力。",
        expected_outputs=("缓存命中率分析", "多轮 token 指标"),
        script_target=ScriptTarget(script="backend/tests/python/test_cache_hit_analysis.py"),
    ),
    BenchmarkCase(
        case_id="cache_optimization",
        name="缓存优化回归",
        category="cache_efficiency",
        scenario_type="专项回归",
        automation_level="ready",
        objective="保留缓存优化验证脚本，覆盖多步任务中的上下文复用收益。",
        tuning_focus=("缓存复用", "重复读取抑制", "多步任务"),
        tags=("cache", "optimization", "multi-step", "legacy-curated"),
        acceptance_criteria=(
            "脚本正常完成并输出缓存指标",
            "多轮输入 token 不出现异常膨胀",
        ),
        failure_signals=(
            "缓存未命中导致成本暴涨",
            "同一上下文反复重复注入",
        ),
        estimated_minutes=4,
        prompt_summary="保留缓存优化脚本，验证多步任务的缓存复用。",
        expected_outputs=("缓存优化指标", "多步任务日志"),
        script_target=ScriptTarget(script="backend/tests/python/test_cache_optimization.py"),
    ),
    BenchmarkCase(
        case_id="multi_turn_optimization",
        name="多轮会话优化对比",
        category="multi_turn_memory",
        scenario_type="专项回归",
        automation_level="ready",
        objective="保留多轮 A/B 脚本，验证 keep-session 与每轮新 session 的记忆和缓存差异。",
        tuning_focus=("多轮记忆", "会话隔离", "缓存命中"),
        tags=("multi-turn", "memory", "cache", "legacy-curated"),
        acceptance_criteria=(
            "脚本正常完成并输出 A/B 比较结果",
            "能区分 recall、isolation、utilization 等健康项",
        ),
        failure_signals=(
            "多轮会话记忆丢失",
            "跨 session 隔离失效",
        ),
        estimated_minutes=6,
        prompt_summary="保留多轮优化脚本，作为多轮记忆与隔离验证入口。",
        expected_outputs=("A/B 报告", "多轮健康指标"),
        script_target=ScriptTarget(script="backend/tests/python/test_multi_turn_optimization.py"),
    ),
    BenchmarkCase(
        case_id="performance_metrics",
        name="基础性能指标回归",
        category="performance",
        scenario_type="专项回归",
        automation_level="ready",
        objective="保留 TTFT、总耗时与 token 指标的基础性能测量。",
        tuning_focus=("TTFT", "总耗时", "token 成本"),
        tags=("performance", "ttft", "token", "legacy-curated"),
        acceptance_criteria=(
            "脚本正常完成并输出性能指标",
            "关键指标字段齐全",
        ),
        failure_signals=(
            "性能指标无法解析",
            "首 token 延迟异常且无诊断信息",
        ),
        estimated_minutes=4,
        prompt_summary="保留基础性能指标脚本，用于快速回归。",
        expected_outputs=("TTFT 指标", "总耗时", "token 使用量"),
        script_target=ScriptTarget(script="backend/tests/python/test_perf_metrics.py"),
    ),
    BenchmarkCase(
        case_id="performance_forgetfulness",
        name="长链健忘性验证",
        category="performance",
        scenario_type="专项回归",
        automation_level="ready",
        objective="验证长链上下文中是否出现前文遗忘或上下文保留失衡。",
        tuning_focus=("长链保留", "上下文遗忘", "性能稳定性"),
        tags=("performance", "forgetfulness", "long-chain", "legacy-curated"),
        acceptance_criteria=(
            "脚本正常完成并输出遗忘性观察",
            "长链中关键标记不被错误遗忘",
        ),
        failure_signals=(
            "后续轮次完全遗忘早期信息",
            "上下文增长与保留不成比例",
        ),
        estimated_minutes=5,
        prompt_summary="保留长链健忘性脚本，覆盖记忆保持问题。",
        expected_outputs=("遗忘性观察结果", "多轮 token 曲线"),
        script_target=ScriptTarget(script="backend/tests/python/test_performance_forgetfulness.py"),
    ),
    BenchmarkCase(
        case_id="context_flood",
        name="上下文洪泛压力测试",
        category="robustness",
        scenario_type="压力测试",
        automation_level="ready",
        objective="验证上下文极度膨胀时，Agent 仍能维持基本稳定性。",
        tuning_focus=("上下文压力", "鲁棒性", "长文本处理"),
        tags=("context", "stress", "robustness", "legacy-curated"),
        acceptance_criteria=(
            "脚本正常完成",
            "无明显崩溃或协议级失败",
        ),
        failure_signals=(
            "上下文堆积后服务失稳",
            "响应明显退化且无恢复路径",
        ),
        estimated_minutes=5,
        prompt_summary="保留上下文洪泛脚本，作为极限上下文压力项。",
        expected_outputs=("压力测试输出", "稳定性观察"),
        script_target=ScriptTarget(script="backend/tests/python/test_context_flood.py"),
    ),
    BenchmarkCase(
        case_id="encoding_robustness",
        name="编码鲁棒性测试",
        category="robustness",
        scenario_type="专项回归",
        automation_level="ready",
        objective="覆盖多编码、多字符集输入输出下的鲁棒性。",
        tuning_focus=("编码兼容", "字符集鲁棒性", "工具输出稳定性"),
        tags=("encoding", "robustness", "i18n", "legacy-curated"),
        acceptance_criteria=(
            "脚本正常完成",
            "无明显乱码或解码失败",
        ),
        failure_signals=(
            "工具输出乱码",
            "编码异常导致流程中断",
        ),
        estimated_minutes=4,
        prompt_summary="保留编码鲁棒性脚本，覆盖 UTF-8/本地编码兼容问题。",
        expected_outputs=("编码验证结果", "异常提示"),
        script_target=ScriptTarget(script="backend/tests/python/test_encoding_robustness.py"),
    ),
    BenchmarkCase(
        case_id="github_search",
        name="GitHub 搜索集成测试",
        category="integration",
        scenario_type="工具集成",
        automation_level="ready",
        objective="验证 GitHub 仓库搜索类工具是否能正确触发和返回结果。",
        tuning_focus=("外部工具集成", "仓库搜索", "工具触发"),
        tags=("github", "integration", "search", "legacy-curated"),
        acceptance_criteria=(
            "脚本正常完成",
            "能观测到搜索类交互结果",
        ),
        failure_signals=(
            "工具未触发",
            "外部搜索流程失效",
        ),
        estimated_minutes=4,
        prompt_summary="保留 GitHub 搜索脚本，覆盖外部仓库搜索集成。",
        expected_outputs=("搜索交互日志", "结果摘要"),
        script_target=ScriptTarget(script="backend/tests/python/test_github_search.py"),
    ),
    BenchmarkCase(
        case_id="browser_use",
        name="浏览器工具基础测试",
        category="integration",
        scenario_type="工具集成",
        automation_level="ready",
        objective="验证浏览器工具是否能在简单检索任务中被正确触发。",
        tuning_focus=("browser-use", "外部工具", "页面检索"),
        tags=("browser", "integration", "tool", "legacy-curated"),
        acceptance_criteria=(
            "脚本正常完成",
            "能检测到浏览器工具调用或等价交互",
        ),
        failure_signals=(
            "浏览器工具未触发",
            "页面交互失败",
        ),
        estimated_minutes=4,
        prompt_summary="保留浏览器基础测试脚本，作为通用浏览器能力入口。",
        expected_outputs=("浏览器交互日志", "结果摘要"),
        script_target=ScriptTarget(script="backend/tests/python/test_browser_use.py"),
    ),
    BenchmarkCase(
        case_id="subagent_delegation",
        name="子代理委派验证",
        category="orchestration",
        scenario_type="工具集成",
        automation_level="ready",
        objective="验证子代理委派能力是否能在简单任务里稳定触发和完成。",
        tuning_focus=("委派触发", "任务拆分", "结果回收"),
        tags=("subagent", "delegation", "orchestration", "legacy-curated"),
        acceptance_criteria=(
            "脚本正常完成",
            "能验证子代理或等价委派路径",
        ),
        failure_signals=(
            "未触发委派",
            "委派结果未落地",
        ),
        estimated_minutes=4,
        prompt_summary="保留子代理委派脚本，覆盖轻量级委派能力验证。",
        expected_outputs=("委派日志", "文件产物"),
        script_target=ScriptTarget(script="backend/tests/python/test_subagent_delegation.py"),
    ),
    BenchmarkCase(
        case_id="subagent_parallel_modes",
        name="子代理并行模式覆盖",
        category="orchestration",
        scenario_type="专项回归",
        automation_level="ready",
        objective="覆盖四路并行与 any_finished/all_finished 双阶段等待，验证并行等待模式的速度与准确性。",
        tuning_focus=("并行 fan-out", "wait_mode", "结果回收"),
        tags=("subagent", "parallel", "wait-mode", "coverage"),
        acceptance_criteria=(
            "脚本正常完成",
            "同时命中 any_finished 与 all_finished",
            "并行文件产物全部落地",
        ),
        failure_signals=(
            "竞速等待未触发",
            "四路并行结果缺失",
        ),
        estimated_minutes=6,
        prompt_summary="覆盖四路并行聚合与双阶段 wait mode 回收。",
        expected_outputs=("JSON 报告", "并行模式文件产物"),
        script_target=ScriptTarget(
            script="backend/tests/python/test_subagent_parallel_modes.py",
            pass_common_args=True,
            report_arg_supported=True,
        ),
    ),
    BenchmarkCase(
        case_id="subagent_observability_e2e",
        name="子代理运行态快照端到端",
        category="orchestration",
        scenario_type="端到端回归",
        automation_level="ready",
        objective="验证异步委派、manage_worker 等待、/v1/swarm/snapshots 运行态观测，以及失败恢复后的 structured_summary/last_recovery_note 能在真实链路里协同工作。",
        tuning_focus=("异步委派", "快照观测", "失败恢复", "结果回收"),
        tags=("subagent", "observability", "snapshots", "e2e"),
        acceptance_criteria=(
            "基线场景必须命中 delegate_task、manage_worker 与本地 write",
            "轮询期间能观测到目标 worker 的 snapshots 记录与 running 汇总",
            "恢复场景必须观测到 retry_count、structured_summary 与 last_recovery_note",
            "最终文件与结构化报告全部落地",
        ),
        failure_signals=(
            "snapshots 接口始终观测不到目标 worker",
            "恢复场景未触发或未记录恢复痕迹",
            "manage_worker 等待后仍缺少 worker 结果摘要",
            "异步委派链路未真正触发",
        ),
        estimated_minutes=9,
        prompt_summary="执行基线运行态场景和恢复场景，并并行轮询 snapshots，验证运行态、等待、重试与结果汇总闭环。",
        expected_outputs=("JSON 报告", "snapshots 观测数据", "恢复痕迹", "文件产物"),
        script_target=ScriptTarget(
            script="backend/tests/python/test_subagent_observability_e2e.py",
            pass_common_args=True,
            report_arg_supported=True,
        ),
    ),
    BenchmarkCase(
        case_id="subagent_dependency_reuse",
        name="子代理依赖链与复用覆盖",
        category="orchestration",
        scenario_type="专项回归",
        automation_level="ready",
        objective="覆盖 depends_on 顺序链和 worker_id 复用路径，验证复杂委派控制流。",
        tuning_focus=("依赖顺序", "worker 复用", "状态管理"),
        tags=("subagent", "depends_on", "worker-reuse", "coverage"),
        acceptance_criteria=(
            "脚本正常完成",
            "命中 depends_on 参数",
            "命中 worker_id 复用路径",
        ),
        failure_signals=(
            "依赖链未按顺序执行",
            "worker 复用未被触发",
        ),
        estimated_minutes=6,
        prompt_summary="覆盖顺序依赖和多轮复用 worker 的控制流。",
        expected_outputs=("JSON 报告", "依赖链与复用产物"),
        script_target=ScriptTarget(
            script="backend/tests/python/test_subagent_dependency_reuse.py",
            pass_common_args=True,
            report_arg_supported=True,
        ),
    ),
    BenchmarkCase(
        case_id="subagent_coverage_suite",
        name="子代理覆盖套件",
        category="orchestration",
        scenario_type="综合任务",
        automation_level="ready",
        objective="统一执行子代理确定性回归验证，并可按需附带高波动压力套件，形成稳定覆盖入口。",
        tuning_focus=("自动化回归", "覆盖矩阵", "稳定性趋势"),
        tags=("subagent", "coverage", "suite", "automation"),
        acceptance_criteria=(
            "套件脚本正常完成",
            "所有阻塞型子案例返回通过",
            "能输出统一报告",
        ),
        failure_signals=(
            "任一阻塞型子案例失败",
            "统一报告缺失",
        ),
        estimated_minutes=12,
        prompt_summary="一键执行子代理稳定覆盖测试，必要时附带压力观察项。",
        expected_outputs=("统一 JSON 报告", "各子案例报告路径"),
        script_target=ScriptTarget(
            script="backend/tests/python/run_subagent_coverage_suite.py",
            pass_common_args=True,
            report_arg_supported=True,
        ),
    ),
    BenchmarkCase(
        case_id="raiden3_token_efficiency",
        name="Raiden3 长链 token 效率",
        category="coding_long_chain",
        scenario_type="历史代表场景",
        automation_level="ready",
        objective="保留 Raiden3 系列中的 token 效率脚本，覆盖长链代码生成与验证中的成本控制。",
        tuning_focus=("代码长链", "token 效率", "多工具执行"),
        tags=("raiden3", "coding", "token", "legacy-curated"),
        acceptance_criteria=(
            "脚本正常完成",
            "能输出长链 token 曲线或分析结果",
        ),
        failure_signals=(
            "长链编码任务成本失控",
            "多步工具执行缺乏收敛",
        ),
        estimated_minutes=8,
        prompt_summary="保留 Raiden3 token 效率脚本，覆盖长链代码类 benchmark。",
        expected_outputs=("token 分析", "长链执行日志"),
        script_target=ScriptTarget(script="backend/tests/python/test_raiden3_token_efficiency.py"),
    ),
)


BENCHMARK_ENTRY_FILES: tuple[str, ...] = (
    "backend/tests/python/agent_benchmark_suite.py",
    "backend/tests/python/run_agent_benchmark_suite.py",
    "backend/tests/python/eval_github_repo_refactor_real.py",
)


REMOVED_SCRIPTS: tuple[str, ...] = (
    "backend/tests/python/capture_realtime_markdown_stream.py",
    "backend/tests/python/reproduce_hang.py",
    "backend/tests/python/test_business_tasks.py",
    "backend/tests/python/test_code_reading_perf.py",
    "backend/tests/python/test_complex_workflow.py",
    "backend/tests/python/test_deepseek_direct.py",
    "backend/tests/python/test_delegation_protocol.py",
    "backend/tests/python/test_empty_tool_output_loop.py",
    "backend/tests/python/test_extreme_code_context.py",
    "backend/tests/python/test_frontend_integration.py",
    "backend/tests/python/test_identical_tool_loop.py",
    "backend/tests/python/test_lsp_optimization.py",
    "backend/tests/python/test_multistep_cache.py",
    "backend/tests/python/test_perf_dynamic.py",
    "backend/tests/python/test_raiden3_step2.py",
    "backend/tests/python/test_raiden3_step3.py",
    "backend/tests/python/test_raiden3_step4.py",
    "backend/tests/python/test_real_world_bug_fix.py",
    "backend/tests/python/test_silence.py",
    "backend/tests/python/test_skills_integration.py",
    "backend/tests/python/test_sota_advanced_ops.py",
    "backend/tests/python/test_sota_comprehensive.py",
    "backend/tests/python/test_sota_optimization.py",
    "backend/tests/python/test_sota_orchestration.py",
    "backend/tests/python/test_sota_simple.py",
    "backend/tests/python/test_tesla_browser.py",
    "backend/tests/python/test_worker_skill.py",
    "backend/tests/python/verify_task_4.py",
)


PLANNED_CASES: tuple[BenchmarkCase, ...] = (
    BenchmarkCase(
        case_id="backend_concurrency_fix",
        name="后端并发修复闭环",
        category="coding_backend",
        scenario_type="综合任务",
        automation_level="planned",
        objective="让 Agent 在真实 Go 服务中定位并修复并发缺陷，并完成测试与构建闭环。",
        tuning_focus=("并发安全", "根因修复", "编译验证"),
        tags=("go", "race", "build", "test"),
        acceptance_criteria=(
            "明确根因并给出最小修复",
            "go test 与 go build 全部通过",
            "最终说明包含风险面与回归点",
        ),
        failure_signals=(
            "只做表面补丁未修根因",
            "修改后未完成编译验证",
        ),
        estimated_minutes=12,
        prompt_summary="在真实 Go 后端中定位并修复并发或取消控制问题。",
        expected_outputs=("代码补丁", "测试日志", "构建结果"),
    ),
    BenchmarkCase(
        case_id="frontend_intentional_ui",
        name="前端高意图界面改造",
        category="coding_frontend",
        scenario_type="综合任务",
        automation_level="planned",
        objective="验证 Agent 在保留既有设计系统前提下完成非模板化 UI 改造的能力。",
        tuning_focus=("前端实现", "视觉一致性", "中文界面"),
        tags=("react", "css", "ui", "frontend"),
        acceptance_criteria=(
            "界面文案全中文",
            "桌面和移动端均可用",
            "npm build 通过",
        ),
        failure_signals=(
            "生成同质化模板 UI",
            "未验证构建结果",
        ),
        estimated_minutes=15,
        prompt_summary="在现有前端页面中完成有明确视觉方向的功能改造。",
        expected_outputs=("前端代码修改", "构建结果", "界面截图或说明"),
    ),
    BenchmarkCase(
        case_id="env_self_healing_windows",
        name="Windows 环境自修复",
        category="environment_ops",
        scenario_type="综合任务",
        automation_level="planned",
        objective="验证 Agent 感知当前 Windows/PowerShell 环境并给出依赖修复或自配置闭环。",
        tuning_focus=("环境探测", "依赖修复", "最小入侵"),
        tags=("windows", "powershell", "env", "install"),
        acceptance_criteria=(
            "先探测再执行",
            "安装或修复命令准确",
            "失败时给出可执行替代路径",
        ),
        failure_signals=(
            "未探测环境直接执行破坏性命令",
            "安装建议与实际平台不匹配",
        ),
        estimated_minutes=8,
        prompt_summary="处理缺包、Git 配置或 Python 解释器错配等环境问题。",
        expected_outputs=("探测结果", "修复命令", "验证结果"),
    ),
    BenchmarkCase(
        case_id="browser_research_reporting",
        name="外网调研与报告生成",
        category="research_reporting",
        scenario_type="综合任务",
        automation_level="planned",
        objective="验证搜索、去噪、归纳、引用和结构化报告生成能力。",
        tuning_focus=("外部检索", "信息综合", "引用保真"),
        tags=("web", "report", "citation", "research"),
        acceptance_criteria=(
            "至少引用 5 个外部来源",
            "观点与来源映射清晰",
            "报告结构完整",
        ),
        failure_signals=(
            "无来源或来源不可追溯",
            "长文总结存在明显幻觉",
        ),
        estimated_minutes=15,
        prompt_summary="围绕给定主题做研究并产出可直接使用的中文调研报告。",
        expected_outputs=("Markdown 报告", "来源清单"),
    ),
    BenchmarkCase(
        case_id="document_generation_suite",
        name="办公文档与演示稿生成",
        category="office_automation",
        scenario_type="综合任务",
        automation_level="planned",
        objective="验证 Markdown、PPT、图表等办公产物的结构化生成质量。",
        tuning_focus=("文档结构", "图表生成", "版式质量"),
        tags=("ppt", "markdown", "report", "office"),
        acceptance_criteria=(
            "产物结构完整",
            "图文页与结论页逻辑一致",
            "无需人工大改即可交付",
        ),
        failure_signals=(
            "只生成空骨架",
            "图表与正文数据不一致",
        ),
        estimated_minutes=12,
        prompt_summary="基于已有调研结果生成演示文稿与配套说明文档。",
        expected_outputs=("PPTX 文件", "Markdown 摘要", "图表资源"),
    ),
    BenchmarkCase(
        case_id="subagent_delegation_complex",
        name="子代理委派复杂任务",
        category="orchestration",
        scenario_type="综合任务",
        automation_level="planned",
        objective="验证主 Agent 对复杂检索/分析任务的分治拆解与委派质量。",
        tuning_focus=("任务拆分", "委派边界", "结果整合"),
        tags=("subagent", "delegation", "orchestration"),
        acceptance_criteria=(
            "委派前目标定义清晰",
            "回收结果后能继续推进主任务",
            "无无效往返委派",
        ),
        failure_signals=(
            "委派描述过于模糊",
            "结果回收后未被消费",
        ),
        estimated_minutes=10,
        prompt_summary="在复杂代码探索和跨目录分析中使用子代理完成分治。",
        expected_outputs=("委派记录", "主任务最终结果"),
    ),
    BenchmarkCase(
        case_id="memory_recall_consistency",
        name="跨轮记忆与约束一致性",
        category="memory_consistency",
        scenario_type="综合任务",
        automation_level="planned",
        objective="验证多轮会话中，Agent 是否能保持用户约束、上下文设定与先前结论的一致性。",
        tuning_focus=("记忆命中", "跨轮一致性", "限制遵守"),
        tags=("memory", "multi-turn", "consistency"),
        acceptance_criteria=(
            "跨轮不丢约束",
            "旧结论被正确继承或纠正",
            "无自相矛盾输出",
        ),
        failure_signals=(
            "多轮中遗忘用户关键约束",
            "后续回答与前轮结论冲突",
        ),
        estimated_minutes=8,
        prompt_summary="跨多轮和跨任务保持既定约束、风格和关键结论一致。",
        expected_outputs=("多轮记录", "一致性评估"),
    ),
    BenchmarkCase(
        case_id="chaos_tool_failure_recovery",
        name="工具失败与降级恢复",
        category="chaos_recovery",
        scenario_type="综合任务",
        automation_level="planned",
        objective="验证工具报错、权限拒绝、路径错误等异常下的恢复与降级行为。",
        tuning_focus=("恢复策略", "降级路径", "错误解释"),
        tags=("chaos", "tool-error", "recovery"),
        acceptance_criteria=(
            "遇错能解释并改用替代手段",
            "不会在同一错误上无效重试",
            "保留用户目标导向",
        ),
        failure_signals=(
            "同一失败路径无限循环",
            "遇到权限问题直接停摆",
        ),
        estimated_minutes=10,
        prompt_summary="通过故意制造工具失败，验证 Agent 的恢复与降级处理能力。",
        expected_outputs=("错误时间线", "替代路径记录"),
    ),
)


ACTIVE_READY_CASES: tuple[BenchmarkCase, ...] = READY_CASES + LEGACY_READY_CASES


ALL_CASES: tuple[BenchmarkCase, ...] = ACTIVE_READY_CASES + PLANNED_CASES


CORE_READY_CASE_IDS: frozenset[str] = frozenset(case.case_id for case in READY_CASES)
EXTENDED_READY_CASE_IDS: frozenset[str] = frozenset(case.case_id for case in LEGACY_READY_CASES)


def get_case_tier(case: BenchmarkCase) -> str:
    if case.case_id in CORE_READY_CASE_IDS:
        return "core"
    if case.case_id in EXTENDED_READY_CASE_IDS:
        return "extended"
    return "planned"


def get_script_inventory() -> dict[str, Any]:
    benchmark_case_scripts = sorted(
        {
            case.script_target.script
            for case in ACTIVE_READY_CASES
            if case.script_target is not None
        }
    )
    return {
        "benchmark_entry_files": list(BENCHMARK_ENTRY_FILES),
        "benchmark_case_scripts": benchmark_case_scripts,
        "removed_scripts": list(REMOVED_SCRIPTS),
    }


def get_suite_dimensions() -> tuple[str, ...]:
    return SUITE_DIMENSIONS


def get_cases(include_planned: bool = True) -> tuple[BenchmarkCase, ...]:
    if include_planned:
        return ALL_CASES
    return ACTIVE_READY_CASES


def get_cleanup_plan() -> dict[str, Any]:
    inventory = get_script_inventory()
    return {
        "benchmark_entry_files": inventory["benchmark_entry_files"],
        "benchmark_case_scripts": inventory["benchmark_case_scripts"],
        "removed_scripts": inventory["removed_scripts"],
        "active_ready_case_ids": [case.case_id for case in ACTIVE_READY_CASES],
        "tier_summary": summarize_by_tier(list(ACTIVE_READY_CASES)),
    }


def get_case(case_id: str) -> BenchmarkCase:
    for case in ALL_CASES:
        if case.case_id == case_id:
            return case
    raise KeyError(f"unknown benchmark case: {case_id}")


def filter_cases(
    *,
    case_ids: set[str] | None = None,
    categories: set[str] | None = None,
    tags: set[str] | None = None,
    tiers: set[str] | None = None,
    include_planned: bool = True,
) -> list[BenchmarkCase]:
    selected: list[BenchmarkCase] = []
    for case in get_cases(include_planned=include_planned):
        if case_ids and case.case_id not in case_ids:
            continue
        if categories and case.category not in categories:
            continue
        if tags and not tags.intersection(case.tags):
            continue
        if tiers and get_case_tier(case) not in tiers:
            continue
        selected.append(case)
    return selected


def summarize_by_category(cases: list[BenchmarkCase]) -> dict[str, int]:
    summary: dict[str, int] = {}
    for case in cases:
        summary[case.category] = summary.get(case.category, 0) + 1
    return dict(sorted(summary.items()))


def summarize_by_tier(cases: list[BenchmarkCase]) -> dict[str, int]:
    summary: dict[str, int] = {}
    for case in cases:
        tier = get_case_tier(case)
        summary[tier] = summary.get(tier, 0) + 1
    return dict(sorted(summary.items()))


def build_suite_manifest(include_planned: bool = True) -> dict[str, Any]:
    selected_cases = list(get_cases(include_planned=include_planned))
    cases = [case.to_dict() for case in selected_cases]
    return {
        "dimensions": list(SUITE_DIMENSIONS),
        "case_count": len(cases),
        "ready_case_count": len(ACTIVE_READY_CASES),
        "core_ready_case_count": len(READY_CASES),
        "extended_ready_case_count": len(LEGACY_READY_CASES),
        "planned_case_count": len(PLANNED_CASES),
        "tier_summary": summarize_by_tier(selected_cases),
        "script_inventory": get_script_inventory(),
        "cleanup_plan": get_cleanup_plan(),
        "cases": cases,
    }


def render_case_table(cases: list[BenchmarkCase]) -> str:
    headers = ("case_id", "tier", "category", "level", "minutes", "name")
    rows = [headers]
    for case in cases:
        rows.append(
            (
                case.case_id,
                get_case_tier(case),
                case.category,
                case.automation_level,
                str(case.estimated_minutes),
                case.name,
            )
        )
    widths = [max(len(row[index]) for row in rows) for index in range(len(headers))]
    output_lines: list[str] = []
    for row_index, row in enumerate(rows):
        output_lines.append("  ".join(value.ljust(widths[index]) for index, value in enumerate(row)))
        if row_index == 0:
            output_lines.append("  ".join("-" * width for width in widths))
    return "\n".join(output_lines)


__all__ = [
    "BenchmarkCase",
    "ScriptTarget",
    "REPO_ROOT",
    "SUITE_DIMENSIONS",
    "build_suite_manifest",
    "filter_cases",
    "get_case",
    "get_case_tier",
    "get_cases",
    "get_cleanup_plan",
    "get_script_inventory",
    "get_suite_dimensions",
    "render_case_table",
    "summarize_by_category",
    "summarize_by_tier",
]
