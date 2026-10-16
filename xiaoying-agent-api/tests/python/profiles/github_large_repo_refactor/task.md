# GitHub 大仓真实重构任务

在 Django 仓库中重构 `django/utils/http.py`：

1. 把 `url_has_allowed_host_and_scheme()` 内对 `allowed_hosts` 的归一化逻辑提取成私有 helper `_normalize_allowed_hosts()`，减少主函数里的分支噪音。
2. 保持公开 API 和现有行为不变。
3. 在 `tests/utils_tests/test_http.py` 中新增 `test_allowed_hosts_normalization_inputs`，覆盖 `None`、字符串和集合输入，证明重构后行为未变。
4. 只做完成任务所需的最小改动，不要顺手重排无关代码。
