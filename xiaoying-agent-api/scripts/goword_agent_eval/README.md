# go-word Agent 真实文档评估器

这个脚本用于验证 Auramate 当前 Word 编辑链路在真实 DOCX 上的表现，不依赖手工起后端，也不依赖浏览器界面。

它会自动完成以下步骤：

1. 生成一个带标题层级、正文和表格的真实 DOCX。
2. 挂一个最小 go-word HTTP 服务，提供 open/document/search/apply/save/close 接口。
3. 真实调用当前生产中的原生 Go auramate-word-reader 和 auramate-word-copilot 工具。
4. 执行多场景编辑与保存验证。
5. 根据失败场景输出自动调优建议；部分场景会自动增强 format_basis、补齐 preferred_paragraph_id、切换样式模板或扩大检索范围后重试。
6. 对企业级多步骤工作流执行保存复开后的完整性校验，确保未修改区域的标题、列表、表格结构不被污染。
7. 可选地接入固定测试模型，直接跑真实 normal-agent 对话链路，验证模型是否会先调用 word-reader / word-copilot，再输出可执行的 word_editor_action。

## 覆盖场景

- reader 读取真实文档结构与 style_catalog
- copilot 对 reader_context_summary / format_basis 的强约束
- 正文替换
- 标题后插入正文并保持正文样式
- 同级标题插入并保持标题样式
- 多处“说明”类段落下的精确锚点改写，验证 preferred_paragraph_id 对模糊 query 的纠偏能力
- copilot 只在 summary 中给出 paragraph_id / reader_session_id 时，后端 signal 归一化仍能自动补齐锚点并在模糊 query 下正确落点
- 真实对话式“中间那条说明”改写，验证 agent 能否结合人工复核等语义线索而不是机械取第一条命中
- 真实对话式“第三方费用那条后面补一句”插入，验证 agent 能否在模糊 query 下准确落到正确费用条款
- 真实对话式“最后那条备注改正式一点”改写，验证 agent 能否正确利用末尾序位词命中附录说明
- 真实对话式“风险提示那格下面补一句”插入，验证 agent 能否把“那格”理解为目标表格单元格并保持表格正文样式
- 真实对话式“把风险提示那格里的正文补完整但保持同一格单段正文”扩写，验证 agent 能否先确认单元格邻域，再使用 update_table_cell 保持同格单段结构
- 真实对话式“把验证说明补完整但保持同一段”扩写，验证 agent 能否先精读邻域并坚持同段 replace，而不是误插换行或拆段
- 真实对话式“把交付说明扩成两段并补完整”扩写，验证 agent 能否先判断结构变化，再使用 insert_paragraph_break + replace 完成拆段扩写
- 正文短语强调，验证字体、字号、加粗、颜色等真实样式修改能力
- 表格单元格文本替换
- 表格单元格内插入新段落并保持同一单元格正文样式
- 编号列表后插入非编号说明正文并避免继承编号
- 企业级多步骤真实工作流与文档完整性校验，覆盖正文改写、重点强调、标题插入、表格替换和列表说明插入
- 关闭会话后从 snapshot 恢复保存
- 可选的真实大模型端到端对话评估，当前内置 14 个口语化 Word 编辑场景，并统计固定模型的通过率

## 运行方式

在 backend 目录执行：

```powershell
go run ./scripts/goword_agent_eval
```

如果需要启用真实大模型端到端对话测试：

```powershell
go run ./scripts/goword_agent_eval --e2e-model ark-code-latest --e2e-timeout 180
```

如果需要按失败场景自动缩小范围循环复测：

```powershell
python ./scripts/run_llm_e2e_loop.py --model ark-code-latest --max-rounds 3
```

可选参数：

- --artifacts: 产物目录，默认写到 backend/workspace/goword-agent-eval
- --report: JSON 报告路径
- --python: 兼容保留参数，当前原生 Go 评测链路通常不需要
- --keep-outputs: 是否保留生成的 DOCX 产物
- --e2e-model: 固定测试模型 ID；非空时启用真实大模型端到端对话评估
- --e2e-timeout: 真实大模型端到端单场景超时时间，单位秒

循环脚本会把每轮报告写到 `backend/workspace/goword-agent-eval-loop/round_XX/report.json`，并在根目录额外生成 `loop-summary.json`。默认策略是：先按给定场景运行一轮；若仍有失败，则只重跑失败场景；若连续两轮失败集合完全一致则提前停止，避免空转。

## 输出

脚本会在标准输出打印 JSON 报告，并默认写入：

- backend/workspace/goword-agent-eval/report.json

当有失败场景时，脚本退出码为 1，便于接入 CI 或批处理脚本。

当前默认夹具会构造一个企业文档常见的复合结构：封面标题、导语、章节标题、正文、编号步骤、交付说明、费用条款、表格指标区、附录说明。评估场景强调“用户话术可以不精确，但 agent 必须能先浏览文档、识别上下文，再做最小且正确的编辑”，并对编辑后的文本内容、样式继承、编号状态、表格单元格归属、模糊语义命中位置以及保存复开后的完整性做闭环验证。

启用 --e2e-model 后，脚本仍然复用同一份真实 DOCX 和 go-word 服务，但会额外起真实 Auramate normal-agent，无需手工启动后端。模型需要自行判断先调用 read 还是 auramate-word-reader，再按需调用 auramate-word-copilot；评估器会把返回的 word_editor_action 接回现有 execute/save/reopen 校验链路，因此能够真正统计固定模型在真实对话下的首轮成功率和误改情况。deterministic 场景与 LLM e2e 场景现在共用同一套 go-word service/session 绑定，避免再出现评测器和真实 agent 各用一套 session 空间导致的假失败。

当启用 --e2e-model 时，报告中的每个 LLM 场景还会额外输出 tool_trace 和 tool_call_summary：前者记录每次 read / auramate-word-reader / auramate-word-copilot 的结构化输入摘要、结果摘要和阶段归属，后者汇总重复调用、reader query 序列、copilot 动作序列与各工具调用次数，便于直接定位“为什么会过度读取/重复试探 copilot”。