# GitHub 大仓完整长链重构任务：Flask async dispatch 调用链

在 Flask 仓库中做一次完整长链重构。目标是把散落在请求分发、错误处理、上下文、模板信号和 class-based views 中的“先 `ensure_sync` 再调用”以及“发送信号时传 `_async_wrapper=...ensure_sync`”逻辑收敛到共享私有 helper，保持公开 API 和行为不变。

这是长链评估，不是局部替换。你必须先读完整调用链，再做最小改动。

必须先阅读这些文件：

- `src/flask/helpers.py`
- `src/flask/app.py`
- `src/flask/ctx.py`
- `src/flask/templating.py`
- `src/flask/views.py`
- `tests/test_async.py`
- `tests/test_signals.py`
- `tests/test_views.py`

必须完成：

1. 在 `src/flask/helpers.py` 中新增私有 helper：
   - `_ensure_sync_call(app, func, *args, **kwargs)`：调用 `app.ensure_sync(func)(*args, **kwargs)`。
   - `_send_signal(app, signal, **kwargs)`：调用 `signal.send(app, _async_wrapper=app.ensure_sync, **kwargs)`。
2. 在 `src/flask/app.py` 中，把错误处理、请求分发、before/after/teardown、request signals 里直接拼接 `self.ensure_sync(...)(...)` 或 `_async_wrapper=self.ensure_sync` 的地方改为共享 helper。
3. 在 `src/flask/ctx.py`、`src/flask/templating.py`、`src/flask/views.py` 中同步接入共享 helper。
4. 添加或补强回归测试，证明 async route、async error handler、async before/after request、signal wrapper、template signal、class-based async view 仍然工作。
5. 必须在 `tests/test_async.py`、`tests/test_signals.py`、`tests/test_views.py` 中落地测试改动，不能只阅读这些测试文件。
6. 只修改 profile 允许的 8 个文件。不要改公开 API，不要移动无关代码，不要改文档或项目配置。

注意：`flash`/`message_flashed` 不是本次 async dispatch 调用链目标，除非现有测试明确失败并要求修复，否则不要改它。

完成后必须运行：

```powershell
python -m py_compile src/flask/helpers.py src/flask/app.py src/flask/ctx.py src/flask/templating.py src/flask/views.py tests/test_async.py tests/test_signals.py tests/test_views.py
python -m pytest tests/test_async.py tests/test_signals.py tests/test_templating.py tests/test_views.py -q
python -m pytest tests/test_basic.py::test_before_after_request_order tests/test_basic.py::test_teardown_request_handler_error -q
git diff --check
```
