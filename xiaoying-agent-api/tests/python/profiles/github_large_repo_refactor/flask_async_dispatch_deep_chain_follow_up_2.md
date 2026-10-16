# 测试覆盖阶段

本轮不要继续泛泛审查源码。先完成测试覆盖，缺一不可：

1. 修改 `tests/test_async.py`，补强 async route / async error handler / async request hook 与 `ensure_sync` 调用链相关覆盖。
2. 修改 `tests/test_signals.py`，补强 async signal wrapper 覆盖，至少覆盖 template/request/appcontext 相关信号之一。
3. 修改 `tests/test_views.py`，补强 class-based async view 覆盖。
4. 如果源码仍有语法错误，可以先做最小修复；否则本轮必须优先改三个测试文件。
5. 本轮结束前运行完整验证命令，不能只运行单个文件的 py_compile。

如果任意一个测试文件没有 diff，本轮就是未完成。
