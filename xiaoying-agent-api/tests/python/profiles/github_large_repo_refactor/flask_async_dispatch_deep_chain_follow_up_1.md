继续第一轮重构，不要只总结。

请做一次遗漏调用方审查：

1. 在 `src/flask` 中搜索 `ensure_sync(`、`_async_wrapper=`、`template_rendered.send`、`request_started.send`、`appcontext_` signal 相关调用。
2. 判断哪些仍属于本次 async dispatch 调用链，哪些必须保留。
3. 如果仍有应收敛但未收敛的直接调用，在允许的 8 个文件内继续修正。
4. 如果 `tests/test_async.py`、`tests/test_signals.py`、`tests/test_views.py` 还没有改动，先补齐测试覆盖。
5. 重新运行完整验证命令。
6. 在摘要中明确说明：保留的直接调用为什么必须保留。

`flash`/`message_flashed` 不属于本次 async dispatch 调用链，除非测试失败直接指向它，否则不要为了“清零 grep”去改它。
