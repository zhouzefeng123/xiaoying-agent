# Agent 基准测试指南

## 目标

这份文档是 backend/tests 的唯一测试操作说明。当前目录只保留基准测试体系相关脚本，后续任何回归、评估、调优、复测，都以这里的规则为准。

## 当前保留范围

### 统一入口

- backend/tests/python/agent_benchmark_suite.py
- backend/tests/python/run_agent_benchmark_suite.py

### 核心基准脚本

- backend/tests/python/test_agent_optimization.py
- backend/tests/python/eval_correction_loop_handoff_real.py
- backend/tests/python/eval_silent_turn_recovery_real.py
- backend/tests/python/eval_mixed_long_chain_real.py
- backend/tests/python/eval_single_agent_refactor_long_chain_real.py

### 扩展基准脚本

- backend/tests/python/eval_manage_plan_session_real.py
- backend/tests/python/eval_github_repo_refactor_real.py
- backend/tests/python/test_sota_diversity_v5.py
- backend/tests/python/test_cache_hit_analysis.py
- backend/tests/python/test_cache_optimization.py
- backend/tests/python/test_multi_turn_optimization.py
- backend/tests/python/test_perf_metrics.py
- backend/tests/python/test_performance_forgetfulness.py
- backend/tests/python/test_context_flood.py
- backend/tests/python/test_encoding_robustness.py
- backend/tests/python/test_github_search.py
- backend/tests/python/test_browser_use.py
- backend/tests/python/test_subagent_delegation.py
- backend/tests/python/test_subagent_parallel_modes.py
- backend/tests/python/test_subagent_observability_e2e.py
- backend/tests/python/test_subagent_dependency_reuse.py
- backend/tests/python/run_subagent_coverage_suite.py
- backend/tests/python/test_raiden3_token_efficiency.py

### 已删除范围

目录里所有不属于基准矩阵的调试脚本、一次性实验脚本、历史重复脚本，都已移除，不再作为测试入口。

## 分类规则

当前基准分两层。

### 1. core

用于日常回归和每次调优后的第一轮验证，重点看是否出现能力退化。

- context_window：boundary_reach、boundary_exceed
- tool_routing：overlap_guard
- execution_hygiene：silent_execution
- correction_recovery：correction_loop_handoff
- reasoning_read_only：cross_sheet_audit
- reasoning_long_chain：long_chain_audit
- reasoning_mixed_long_chain：mixed_release_triage

### 2. extended

用于完整基线、专项能力对比和版本发布前复测，覆盖更广但不作为最小必跑集。

- general_capability：sota_diversity_v5
- cache_efficiency：cache_hit_analysis、cache_optimization
- multi_turn_memory：multi_turn_optimization
- performance：performance_metrics、performance_forgetfulness
- robustness：context_flood、encoding_robustness
- integration：github_search、browser_use
- orchestration：subagent_delegation、subagent_parallel_modes、subagent_observability_e2e、subagent_dependency_reuse、subagent_coverage_suite
- coding_long_chain：single_agent_refactor_long_chain、raiden3_token_efficiency
- coding_real_repo：github_large_repo_refactor
- task_management：manage_plan_autonomous_lifecycle

### 3. planned

planned 只保留在矩阵里做占位，不是可执行基准，不参与当前目录的脚本保留范围。

## 标准运行方式

### 查看全部基准

```powershell
python backend/tests/python/run_agent_benchmark_suite.py --list --include-planned
```

### 查看当前保留集与删除集

```powershell
python backend/tests/python/run_agent_benchmark_suite.py --list-cleanup
```

### 只跑核心基准

```powershell
python backend/tests/python/run_agent_benchmark_suite.py --tier core
```

### 只跑扩展基准

```powershell
python backend/tests/python/run_agent_benchmark_suite.py --tier extended
```

### 跑完整 ready 基准并产出额外汇总报告

```powershell
python backend/tests/python/run_agent_benchmark_suite.py --report-path backend/tests/python/reports/benchmark_suite_latest.json
```

### 只跑某个专项

```powershell
python backend/tests/python/run_agent_benchmark_suite.py --case mixed_release_triage
python backend/tests/python/run_agent_benchmark_suite.py --case correction_loop_handoff
python backend/tests/python/run_agent_benchmark_suite.py --category reasoning_mixed_long_chain
python backend/tests/python/run_agent_benchmark_suite.py --tag cache
python backend/tests/python/run_agent_benchmark_suite.py --case github_large_repo_refactor
python backend/tests/python/run_agent_benchmark_suite.py --case manage_plan_autonomous_lifecycle
```

### 只预演，不真正执行

```powershell
python backend/tests/python/run_agent_benchmark_suite.py --dry-run --tier core
```

### 单独跑 GitHub 大仓真实重构

```powershell
python backend/tests/python/run_agent_benchmark_suite.py --case github_large_repo_refactor
```

默认 ready profile 位于：

- backend/tests/python/profiles/github_large_repo_refactor/django_http_allowed_hosts.json
- backend/tests/python/profiles/github_large_repo_refactor/task.md
- backend/tests/python/profiles/github_large_repo_refactor/flask_static_sendfile.json
- backend/tests/python/profiles/github_large_repo_refactor/flask_static_sendfile_task.md
- backend/tests/python/profiles/github_large_repo_refactor/flask_async_dispatch_deep_chain.json
- backend/tests/python/profiles/github_large_repo_refactor/flask_async_dispatch_deep_chain_task.md
- backend/tests/python/profiles/github_large_repo_refactor/PROFILE_DESIGN.md

这组 profile 会按固定 commit 拉取真实 Django 仓库，把裸仓缓存到 `workspace/github_repo_refactor_eval/cache/`，再在 `workspace/github_repo_refactor_eval/worktrees/` 下创建独立 worktree 做重构验证。脚本会记录 `source_commit`、修改文件、验证命令结果和完整 JSON 报告。

`github_large_repo_refactor` 是默认较小 Django profile，适合烟测 GitHub 下载和基础重构闭环。`github_large_repo_refactor_flask_static_sendfile` 是复杂 Flask profile，要求同时修改 helper、两个调用方和两个测试文件，并启用允许变更路径与最大变更文件数检查。

`github_large_repo_refactor_flask_async_dispatch_deep_chain` 是严格长链 profile，要求三轮会话、8 个文件、每轮主动验证、完整读取覆盖、严格改动边界和最终审查。它用于判断模型是否具备完整长链重构闭环能力；通过它仍不等于对所有仓库绝对保证，但失败原因会尽量指向具体能力缺口。

如果当前网络可用，建议先预热固定 ref：

```powershell
python backend/tests/python/eval_github_repo_refactor_real.py `
  --profile backend/tests/python/profiles/github_large_repo_refactor/django_http_allowed_hosts.json `
  --prepare-cache-only
```

复杂 Flask profile 可单独预热：

```powershell
python backend/tests/python/eval_github_repo_refactor_real.py `
  --profile backend/tests/python/profiles/github_large_repo_refactor/flask_static_sendfile.json `
  --prepare-cache-only
```

完整长链 Flask profile 可复用同一个 Flask 缓存：

```powershell
python backend/tests/python/eval_github_repo_refactor_real.py `
  --profile backend/tests/python/profiles/github_large_repo_refactor/flask_async_dispatch_deep_chain.json `
  --prepare-cache-only
```

之后可在离线或发布前复测时只复用本地缓存：

```powershell
python backend/tests/python/eval_github_repo_refactor_real.py `
  --profile backend/tests/python/profiles/github_large_repo_refactor/django_http_allowed_hosts.json `
  --offline
```

`--offline` 会禁止远端 fetch；如果本地缓存还没有目标 ref，会直接给出明确失败，而不是把网络状态混进 agent 评分。

新增第二条及后续大仓 profile 前，先按 `PROFILE_DESIGN.md` 里的门槛筛题，确保它真的覆盖跨文件重构能力，而不是只做更换一处局部实现。

如果要评估别的仓库，可以直接调用：

```powershell
python backend/tests/python/eval_github_repo_refactor_real.py `
  --repo-url https://github.com/owner/repo.git `
  --repo-ref <commit-or-tag> `
  --task-file path/to/task.md `
  --verify-command "python -m py_compile path/to/file.py" `
  --expected-path path/to/file.py
```

## 统一日志与报告输出

每次执行都会生成一个 run_id，对应一套固定目录。

### 日志目录

- backend/tests/python/logs/benchmark_suite/<run_id>/<case_id>.stdout.log
- backend/tests/python/logs/benchmark_suite/<run_id>/<case_id>.stderr.log

这里保存的是脚本完整标准输出和标准错误，用于排查脚本失败、服务异常、工具调用异常。
日志文件统一按 UTF-8 写入；如果在 PowerShell 中直接查看，建议显式使用 `Get-Content -Encoding utf8`。

### 报告目录

- backend/tests/python/reports/benchmark_suite/<run_id>/suite_report.json
- backend/tests/python/reports/benchmark_suite/<run_id>/<case_id>.json
- backend/tests/python/reports/benchmark_suite/<run_id>/<case_id>.upstream.json

说明如下：

- suite_report.json：整次运行的总报告，是第一阅读入口。
- <case_id>.json：runner 统一生成的标准化 case 报告，每个 case 都有。
- <case_id>.upstream.json：原始脚本自己产出的结构化报告。只有支持 `--report-path` 的脚本才会有。

## 如何读日志

### 第一步：先看 suite_report.json

重点关注这些字段：

- case_count：本次跑了多少个 case。
- category_summary：本次按类别的覆盖情况。
- tier_summary：本次 core / extended / planned 的分布。
- executions：每个 case 的最终状态。

### 第二步：定位失败 case

在 executions 里优先看：

- status：passed / failed / dry-run / planned
- return_code：脚本退出码
- passed：如果脚本本身有结构化 pass/fail，这里会反映
- diagnostics：脚本给出的诊断信息
- failure_reasons：脚本给出的失败原因
- stdout_log_path / stderr_log_path：对应原始日志位置
- source_report_path：对应原生 JSON 报告位置

### 第三步：看 case 级报告

<case_id>.json 是 runner 统一标准化后的结果。它适合做机器对比、历史归档和基线追踪。

如果存在 <case_id>.upstream.json，则优先结合它一起看，因为它通常包含更细的领域判断，例如 strict/semantic 命中统计、根因/级联错误匹配、工具调用详情等。

### 第四步：必要时回看 stdout/stderr

遇到下面这些情况，直接去看 stdout/stderr：

- 退出码非 0
- 没拿到 upstream 报告
- status=failed 但 diagnostics 不够具体
- 怀疑是服务不稳定、连接超时、参数不兼容

## 如何进行评价

### 1. core 回归类

评价标准是“是否退化”。

- boundary_reach：未越界前不能过早折叠
- boundary_exceed：越界后必须开始折叠
- overlap_guard：不能重复读、不能高重叠抖动
- silent_execution：工具之间必须静默，最后统一总结

这些 case 以断言是否通过为主，出现失败就视为调优引入退化。

### 2. 真实长链评估类

重点看以下指标：

- matched_findings / missing_findings
- semantic_matched_findings / semantic_missing_findings
- minimum_root_matches / minimum_cascade_matches 是否达标
- strict 命中是否稳定优先于 semantic 命中
- 是否覆盖全部关键文件 / 工作表

对 mixed_release_triage 这类场景，优先关注：

- strict 证据串是否保真
- 最终答案是否漏关键文件
- thought 事件是否异常偏多

### 3. 纠错收口回归类

对 `correction_loop_handoff` 重点看三件事：

- 纠错轮是否先完成真实工具复核，而不是空转
- 第 6 次成功只读复核后，当前轮是否直接结束并进入 `waiting_user`
- 最终可见回复是否向用户索取新证据，而不是继续复述原始指令

### 4. extended 专项类

这类 case 更适合做横向对比，不一定要求每次都全跑，但做版本发布前建议至少跑一轮。

评价时以三件事为主：

- 是否稳定跑通
- 是否输出该 case 设计时要求的核心指标或产物
- 是否出现明显退化，比如 token 成本暴涨、缓存命中下降、外部工具触发失败、乱码、上下文崩溃等

### 5. 真实仓库重构类

对 `github_large_repo_refactor` 这类 case，额外看四件事：

- `repo_ref` 是否固定、`source_commit` 是否记录
- `changed_files` 是否命中预期路径，而不是只产生无关 diff
- `verification_results` 是否全部通过
- 继续轮是否复用同一 session，并在真实代码库里完成收口
- 首次写入前是否已完整读取预期修改文件
- agent 是否主动执行配置的验证命令
- 继续轮是否继续执行并重新验证，而不是只做口头复述
- profile 声明的必要结构是否出现、禁止结构是否消失
- 对 deep-chain profile，还必须检查 `verification_turn_labels` 覆盖全部轮次、`read_expected_paths` 覆盖全部预期路径、`unexpected_changed_files` 为空

### 6. 计划管理生命周期类

对 `manage_plan_autonomous_lifecycle` 重点看三件事：

- 用户没有点名 `manage_plan` 时，模型是否仍会在复杂任务里自主建计划
- 后续推进是否使用稳定 `id` 更新 `in_progress` / `completed`
- 任务已经结束时，是否会自然执行 `archive` 完成收尾

## 推荐测试流程

### 日常调优后

1. 先跑 `--tier core`
2. 如果 core 全绿，再跑目标专项，比如 `--category reasoning_mixed_long_chain`
3. 需要做版本基线时，再跑完整 ready 套件

### 发版前

1. 跑 core
2. 跑 extended
3. 对 reasoning_long_chain 和 reasoning_mixed_long_chain 至少重复 3 次，看通过率而不是只看单次结果
4. 如果本轮涉及代码能力或工具链调整，额外跑一次 `github_large_repo_refactor`

## 如何新增基准

新增脚本之前，先问三个问题：

1. 这个能力是否已经被现有 case 覆盖？
2. 这个脚本是否能稳定重复运行，而不是一次性验证？
3. 这个脚本是否能接入统一 runner 和统一输出目录？

只有答案都为“是”，才允许新增。

新增步骤固定如下：

1. 把脚本放到 backend/tests/python/
2. 在 backend/tests/python/agent_benchmark_suite.py 注册 case
3. 尽量支持 `--report-path`
4. 用 runner 执行一次，确认 logs/benchmark_suite 和 reports/benchmark_suite 输出完整
5. 更新本指南中的分类和运行说明
