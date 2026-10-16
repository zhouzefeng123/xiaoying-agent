# 最终验证收口阶段

继续最终收口，不要只复述。

请做严格结果审查：

1. 查看最终 `git diff`，确认只修改允许的 8 个文件。
2. 确认新增 helper 是私有 helper，没有扩大公开 API。
3. 确认 async route、async error handler、async request hooks、signals、templates、class-based views 都被测试覆盖，且三个测试文件都有 diff。
4. 逐条运行完整验证命令；如果任意命令失败，先读失败位置和当前 diff，修复后重新运行完整命令。
5. 最终回答必须列出修改文件、验证命令结果、仍然保留的风险；如果有任何验证失败，明确说失败并继续修复。

如果三个测试文件中任意一个没有 diff，本轮必须先补测试；这不是可选项。
