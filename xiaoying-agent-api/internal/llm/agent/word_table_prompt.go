package agent

import "strings"

func hasWordTableEditCue(currentInput string) bool {
	if !isWordDocumentEditingContext(currentInput) {
		return false
	}
	cleaned := stripWordContextBlock(currentInput)
	if cleaned == "" {
		return false
	}
	for _, task := range deriveWordEditSubtasks(currentInput) {
		if task.Kind == "table_update" {
			return true
		}
	}
	for _, marker := range []string{"表格", "单元格", "那格", "这一格", "右侧结果", "通过率", "金额", "总计", "数量", "占比", "比率"} {
		if strings.Contains(cleaned, marker) {
			return true
		}
	}
	return false
}

func buildWordTableEditPreferenceMessage(currentInput string) string {
	if !hasWordTableEditCue(currentInput) {
		return ""
	}
	return "SYSTEM NUDGE: 当前请求包含 Word 表格单元格编辑。优先让 auramate-word-copilot 输出 action=update_table_cell；若 table_id、row_index、cell_index 已知且只是整格单段改写，优先使用 new_text 简写并回填 reader_session_id，不要退回 replace，也不要手写冗长 table_cell，除非该单元格需要多段或复杂结构重建。注意：即使使用 new_text 简写，target_text 仍然必填，必须写成当前单元格里真实存在的原文或原值。表格相关的 insert / update_table_cell 即使在简单整格改写时可放宽 format_basis，也仍必须显式填写 reader_context_summary；如果是“那格”“右侧结果”“同格补一句”这类依赖表格定位或局部展开的动作，还必须带上 exploration_summary，写清 table_id、row_index、cell_index 以及同一行/同一格的确认依据。"
}
