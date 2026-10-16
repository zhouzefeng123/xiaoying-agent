# GitHub 大仓复杂重构任务：Flask 静态文件发送路径

在 Flask 仓库中重构静态文件发送逻辑。目标是消除 `Flask.send_static_file()` 和 `Blueprint.send_static_file()` 中重复的 `static_folder` 校验、`max_age` 计算和 `send_from_directory()` 调用，同时保持公开 API 和行为不变。

必须完成：

1. 先阅读这些文件再写入：`src/flask/helpers.py`、`src/flask/app.py`、`src/flask/blueprints.py`、`tests/test_helpers.py`、`tests/test_blueprints.py`。
2. 在 `src/flask/helpers.py` 中新增私有 helper `_prepare_static_file_response(static_owner, filename)`。
   - 当 `static_owner.has_static_folder` 为假时，抛出原来相同的 `RuntimeError("'static_folder' must be set to serve static_files.")`。
   - 通过 `static_owner.get_send_file_max_age(filename)` 计算 `max_age`。
   - 复用现有 `send_from_directory()` 返回响应。
3. 更新 `src/flask/app.py` 和 `src/flask/blueprints.py` 中的 `send_static_file()`，让它们委托给 `_prepare_static_file_response(self, filename)`。
4. 添加回归测试：
   - `tests/test_helpers.py::TestSendfile::test_send_static_file_shared_helper`
   - `tests/test_blueprints.py::test_blueprint_static_uses_shared_helper`
5. 保持公开 API、异常文本、响应头语义不变。不要重排无关代码，不要改文档或项目配置。

完成后必须运行：

```powershell
python -m py_compile src/flask/helpers.py src/flask/app.py src/flask/blueprints.py tests/test_helpers.py tests/test_blueprints.py
python -m pytest tests/test_helpers.py::TestSendfile::test_send_static_file_shared_helper tests/test_blueprints.py::test_blueprint_static_uses_shared_helper -q
git diff --check
```
