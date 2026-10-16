package server

import (
	"strings"
	"testing"

	"github.com/auramate/auramate/internal/goword"
)

func TestExtractWordEditorSignalEnvelope(t *testing.T) {
	content := "__WORD_EDITOR_SIG__:{\"event_type\":\"word_editor_action\",\"data\":{\"action\":\"replace\",\"target_text\":\"原文\",\"new_text\":\"新文\",\"enable_track_changes\":true}}"

	payload, cleaned := extractWordEditorSignal(content)
	if payload == nil {
		t.Fatalf("expected payload, got nil")
	}
	if payload.Action != "replace" {
		t.Fatalf("expected action replace, got %q", payload.Action)
	}
	if payload.TargetText != "原文" || payload.NewText != "新文" {
		t.Fatalf("unexpected payload content: %+v", *payload)
	}
	if !payload.EnableTrackChanges {
		t.Fatalf("expected EnableTrackChanges=true")
	}
	if cleaned != "" {
		t.Fatalf("expected cleaned content empty, got %q", cleaned)
	}
}

func TestExtractWordEditorSignalDirectPayload(t *testing.T) {
	content := "__WORD_EDITOR_SIG__:{\"action\":\"insert\",\"target_text\":\"锚点\",\"new_text\":\"新增段落\",\"position\":\"after\"}"

	payload, cleaned := extractWordEditorSignal(content)
	if payload == nil {
		t.Fatalf("expected payload, got nil")
	}
	if payload.Action != "insert" || payload.Position != "after" {
		t.Fatalf("unexpected payload: %+v", *payload)
	}
	if cleaned != "" {
		t.Fatalf("expected cleaned content empty, got %q", cleaned)
	}
}

func TestExtractWordEditorSignalPreservesInsertLeadingSpace(t *testing.T) {
	content := "__WORD_EDITOR_SIG__:{\"action\":\"insert\",\"target_text\":\"基本原则\",\"new_text\":\" 新增一条：坚持闭环验收。\",\"position\":\"after\"}"

	payload, cleaned := extractWordEditorSignal(content)
	if payload == nil {
		t.Fatalf("expected payload, got nil")
	}
	if payload.NewText != " 新增一条：坚持闭环验收。" {
		t.Fatalf("expected leading space to be preserved, got %q", payload.NewText)
	}
	if cleaned != "" {
		t.Fatalf("expected cleaned content empty, got %q", cleaned)
	}
}

func TestExtractWordEditorSignalMixedContent(t *testing.T) {
	content := "请先执行以下操作\n__WORD_EDITOR_SIG__:{\"action\":\"delete\",\"target_text\":\"待删文本\"}\n然后继续总结"

	payload, cleaned := extractWordEditorSignal(content)
	if payload == nil {
		t.Fatalf("expected payload, got nil")
	}
	if payload.Action != "delete" || payload.TargetText != "待删文本" {
		t.Fatalf("unexpected payload: %+v", *payload)
	}
	want := "请先执行以下操作\n然后继续总结"
	if cleaned != want {
		t.Fatalf("unexpected cleaned content:\n got: %q\nwant: %q", cleaned, want)
	}
}

func TestExtractWordEditorSignalInvalidSignalPreserved(t *testing.T) {
	content := "__WORD_EDITOR_SIG__:{not-json}\n普通文本"

	payload, cleaned := extractWordEditorSignal(content)
	if payload != nil {
		t.Fatalf("expected nil payload for invalid json, got %+v", *payload)
	}
	if cleaned != content {
		t.Fatalf("expected invalid signal to be preserved, got %q", cleaned)
	}
}

func TestExtractWordEditorSignalReturnsLastSignalWhenMultiple(t *testing.T) {
	content := "__WORD_EDITOR_SIG__:{\"action\":\"scroll\",\"target_text\":\"第一段\"}\n__WORD_EDITOR_SIG__:{\"action\":\"replace\",\"target_text\":\"第二段\",\"new_text\":\"完成\"}"

	payload, cleaned := extractWordEditorSignal(content)
	if payload == nil {
		t.Fatalf("expected payload, got nil")
	}
	if payload.Action != "replace" || payload.TargetText != "第二段" || payload.NewText != "完成" {
		t.Fatalf("unexpected payload: %+v", *payload)
	}
	if cleaned != "" {
		t.Fatalf("expected cleaned content empty, got %q", cleaned)
	}
}

func TestExtractWordEditorSignalsReturnsAllSignalsInOrder(t *testing.T) {
	content := "__WORD_EDITOR_SIG__:{\"action\":\"update_table_cell\",\"target_text\":\"85%\",\"table_id\":\"tbl_metrics\",\"row_index\":1,\"cell_index\":1,\"table_cell\":{\"paragraphs\":[{\"text\":\"92%\"}]}}\n__WORD_EDITOR_SIG__:{\"action\":\"update_table_cell\",\"target_text\":\"需人工复核\",\"table_id\":\"tbl_metrics\",\"row_index\":2,\"cell_index\":1,\"table_cell\":{\"paragraphs\":[{\"text\":\"无需人工复核\"}]}}"

	payloads, cleaned := extractWordEditorSignals(content)
	if len(payloads) != 2 {
		t.Fatalf("expected 2 payloads, got %d", len(payloads))
	}
	if payloads[0].TargetText != "85%" || payloads[1].TargetText != "需人工复核" {
		t.Fatalf("unexpected payload order: %+v", payloads)
	}
	if cleaned != "" {
		t.Fatalf("expected cleaned content empty, got %q", cleaned)
	}
}

func TestExtractWordEditorSignalAppliesDefaultsForUnderstandingFields(t *testing.T) {
	content := "__WORD_EDITOR_SIG__:{\"action\":\"replace\",\"target_text\":\"  第二段  \",\"position\":\"invalid\"}"

	payload, cleaned := extractWordEditorSignal(content)
	if payload == nil {
		t.Fatalf("expected payload, got nil")
	}
	if payload.Action != "replace" {
		t.Fatalf("expected normalized action replace, got %q", payload.Action)
	}
	if payload.TargetText != "第二段" {
		t.Fatalf("expected trimmed target_text, got %q", payload.TargetText)
	}
	if payload.Position != "after" {
		t.Fatalf("expected default position after, got %q", payload.Position)
	}
	if payload.ContextScope != "paragraph" {
		t.Fatalf("expected default context_scope paragraph, got %q", payload.ContextScope)
	}
	if payload.EditIntent != "精准修改" {
		t.Fatalf("expected default edit_intent 精准修改, got %q", payload.EditIntent)
	}
	if payload.RetrievalQuery != "第二段" {
		t.Fatalf("expected retrieval_query fallback to target_text, got %q", payload.RetrievalQuery)
	}
	if !payload.RequireRetrieval {
		t.Fatalf("expected require_retrieval=true for replace action")
	}
	if cleaned != "" {
		t.Fatalf("expected cleaned content empty, got %q", cleaned)
	}
}

func TestExtractWordEditorSignalPreservesReaderEvidence(t *testing.T) {
	content := "__WORD_EDITOR_SIG__:{\"action\":\"insert\",\"target_text\":\"小标题\",\"new_text\":\"新增正文\",\"reader_session_id\":\"  goword-session-1  \",\"reader_context_summary\":\"  第二章标题后的正文首段  \",\"format_basis\":\"  style_catalog 默认正文样式，命中段落字体宋体、小四  \",\"exploration_summary\":\"  已围绕命中标题展开前后邻段，确认新增内容应落在标题后的正文区域  \"}"

	payload, cleaned := extractWordEditorSignal(content)
	if payload == nil {
		t.Fatalf("expected payload, got nil")
	}
	if payload.ReaderSessionID != "goword-session-1" {
		t.Fatalf("expected trimmed reader_session_id, got %q", payload.ReaderSessionID)
	}
	if payload.ReaderContextSummary != "第二章标题后的正文首段" {
		t.Fatalf("expected trimmed reader_context_summary, got %q", payload.ReaderContextSummary)
	}
	if payload.FormatBasis != "style_catalog 默认正文样式，命中段落字体宋体、小四" {
		t.Fatalf("expected trimmed format_basis, got %q", payload.FormatBasis)
	}
	if payload.ExplorationSummary != "已围绕命中标题展开前后邻段，确认新增内容应落在标题后的正文区域" {
		t.Fatalf("expected trimmed exploration_summary, got %q", payload.ExplorationSummary)
	}
	if cleaned != "" {
		t.Fatalf("expected cleaned content empty, got %q", cleaned)
	}
}

func TestExtractWordEditorSignalPreservesPreferredParagraphID(t *testing.T) {
	content := "__WORD_EDITOR_SIG__:{\"action\":\"insert\",\"target_text\":\"小标题\",\"preferred_paragraph_id\":\"  para-42  \",\"new_text\":\"新增正文\"}"

	payload, cleaned := extractWordEditorSignal(content)
	if payload == nil {
		t.Fatalf("expected payload, got nil")
	}
	if payload.PreferredParagraphID != "para-42" {
		t.Fatalf("expected trimmed preferred_paragraph_id, got %q", payload.PreferredParagraphID)
	}
	if cleaned != "" {
		t.Fatalf("expected cleaned content empty, got %q", cleaned)
	}
}

func TestExtractWordEditorSignalInfersAnchorIDsFromReaderEvidence(t *testing.T) {
	content := "__WORD_EDITOR_SIG__:{\"action\":\"replace\",\"target_text\":\"邮箱\",\"new_text\":\"14848909@qq.com\",\"reader_context_summary\":\"联系方式段落，段落 ID: para_404\",\"exploration_summary\":\"reader_session_id: 123e4567-e89b-12d3-a456-426614174000\"}"

	payload, cleaned := extractWordEditorSignal(content)
	if payload == nil {
		t.Fatalf("expected payload, got nil")
	}
	if payload.PreferredParagraphID != "para_404" {
		t.Fatalf("expected inferred preferred_paragraph_id para_404, got %q", payload.PreferredParagraphID)
	}
	if payload.ReaderSessionID != "123e4567-e89b-12d3-a456-426614174000" {
		t.Fatalf("expected inferred reader_session_id, got %q", payload.ReaderSessionID)
	}
	if cleaned != "" {
		t.Fatalf("expected cleaned content empty, got %q", cleaned)
	}
}

func TestExtractWordEditorSignalInfersTableIDFromExplorationSummary(t *testing.T) {
	content := "__WORD_EDITOR_SIG__:{\"action\":\"update_table_cell\",\"target_text\":\"检测项\",\"table_cell\":{\"paragraphs\":[{\"text\":\"新值\"}]},\"exploration_summary\":\"已确认命中表格，table_id: tbl_631，定位到目标单元格\"}"

	payload, cleaned := extractWordEditorSignal(content)
	if payload == nil {
		t.Fatalf("expected payload, got nil")
	}
	if payload.TableID != "tbl_631" {
		t.Fatalf("expected inferred table_id tbl_631, got %q", payload.TableID)
	}
	if cleaned != "" {
		t.Fatalf("expected cleaned content empty, got %q", cleaned)
	}
}

func TestExtractWordEditorSignalNormalizesNumericTableID(t *testing.T) {
	content := "__WORD_EDITOR_SIG__:{\"action\":\"update_table_cell\",\"target_text\":\"通过率\",\"table_id\":\" 1 \",\"row_index\":1,\"cell_index\":1,\"table_cell\":{\"paragraphs\":[{\"text\":\"92%\"}]}}"

	payload, cleaned := extractWordEditorSignal(content)
	if payload == nil {
		t.Fatalf("expected payload, got nil")
	}
	if payload.TableID != "tbl_1" {
		t.Fatalf("expected normalized table_id tbl_1, got %q", payload.TableID)
	}
	if cleaned != "" {
		t.Fatalf("expected cleaned content empty, got %q", cleaned)
	}
}

func TestExtractWordEditorSignalPreservesStylePatch(t *testing.T) {
	content := "__WORD_EDITOR_SIG__:{\"action\":\"format\",\"target_text\":\"重点内容\",\"style_patch\":{\"font_family\":\"  Microsoft YaHei  \",\"font_size_hp\":28,\"bold\":false,\"bold_set\":true}}"

	payload, cleaned := extractWordEditorSignal(content)
	if payload == nil {
		t.Fatalf("expected payload, got nil")
	}
	if payload.Action != "format" {
		t.Fatalf("expected action format, got %q", payload.Action)
	}
	if payload.StylePatch.FontFamily != "Microsoft YaHei" {
		t.Fatalf("expected trimmed font family, got %q", payload.StylePatch.FontFamily)
	}
	if payload.StylePatch.FontSizeHP != 28 || payload.StylePatch.Bold || !payload.StylePatch.BoldSet {
		t.Fatalf("unexpected style patch: %+v", payload.StylePatch)
	}
	if !payload.RequireRetrieval {
		t.Fatalf("expected require_retrieval=true for format action")
	}
	if cleaned != "" {
		t.Fatalf("expected cleaned content empty, got %q", cleaned)
	}
}

func TestExtractWordEditorSignalSupportsStructuredConstraintsAndFailurePolicy(t *testing.T) {
	content := "__WORD_EDITOR_SIG__:{\"action\":\"insert\",\"target_text\":\"现状分析\",\"new_text\":\"新增正文\",\"constraints\":{\"raw_text\":\"新增内容不要继承标题样式\",\"preserve_style\":false,\"preserve_numbering\":false},\"failure_policy\":{\"on_ambiguous\":\"return_candidates\",\"on_validation_failed\":\"rollback_and_report\"}}"

	payload, cleaned := extractWordEditorSignal(content)
	if payload == nil {
		t.Fatalf("expected payload, got nil")
	}
	if payload.Constraints == nil || payload.Constraints.PreserveStyle == nil || *payload.Constraints.PreserveStyle {
		t.Fatalf("expected preserve_style=false constraint, got %+v", payload.Constraints)
	}
	if payload.Constraints.PreserveNumbering == nil || *payload.Constraints.PreserveNumbering {
		t.Fatalf("expected preserve_numbering=false constraint, got %+v", payload.Constraints)
	}
	if payload.Constraints.RawText != "新增内容不要继承标题样式" {
		t.Fatalf("unexpected constraint raw text: %+v", payload.Constraints)
	}
	if payload.FailurePolicy == nil || payload.FailurePolicy.OnValidationFailed != "rollback_and_report" {
		t.Fatalf("expected failure policy to be parsed, got %+v", payload.FailurePolicy)
	}
	if cleaned != "" {
		t.Fatalf("expected cleaned content empty, got %q", cleaned)
	}
}

func TestExtractWordEditorSignalPreservesParagraphStylePatchAndTableSpec(t *testing.T) {
	content := "__WORD_EDITOR_SIG__:{\"action\":\"insert_table\",\"target_text\":\"预算说明\",\"paragraph_style_patch\":{\"style_id\":\"  Heading2  \",\"alignment\":\"  CENTER  \"},\"table_spec\":{\"rows\":[{\"cells\":[{\"paragraphs\":[{\"text\":\"  项目  \"}]},{\"paragraphs\":[{\"text\":\"  金额  \"}]}]}]}}"

	payload, cleaned := extractWordEditorSignal(content)
	if payload == nil {
		t.Fatalf("expected payload, got nil")
	}
	if payload.Action != "insert_table" {
		t.Fatalf("expected action insert_table, got %q", payload.Action)
	}
	if payload.ParagraphStylePatch.StyleID != "Heading2" || payload.ParagraphStylePatch.Alignment != "center" {
		t.Fatalf("unexpected paragraph_style_patch: %+v", payload.ParagraphStylePatch)
	}
	if payload.TableSpec == nil || len(payload.TableSpec.Rows) != 1 || len(payload.TableSpec.Rows[0].Cells) != 2 {
		t.Fatalf("unexpected table_spec: %+v", payload.TableSpec)
	}
	if payload.TableSpec.Rows[0].Cells[0].Paragraphs[0].Text != "项目" {
		t.Fatalf("expected trimmed table cell text, got %+v", payload.TableSpec.Rows[0].Cells[0].Paragraphs)
	}
	if !payload.RequireRetrieval {
		t.Fatalf("expected require_retrieval=true for insert_table action")
	}
	if cleaned != "" {
		t.Fatalf("expected cleaned content empty, got %q", cleaned)
	}
}

func TestExtractWordEditorSignalPreservesTableLocatorAndSectionLayoutPatch(t *testing.T) {
	content := "__WORD_EDITOR_SIG__:{\"action\":\"format_section\",\"target_text\":\"全文\",\"table_id\":\"  tbl_main  \",\"row_index\":1,\"cell_index\":2,\"section_layout_patch\":{\"orientation\":\"  landscape  \",\"margin_left\":1200,\"margin_right\":1200}}"

	payload, cleaned := extractWordEditorSignal(content)
	if payload == nil {
		t.Fatalf("expected payload, got nil")
	}
	if payload.Action != "format_section" {
		t.Fatalf("expected action format_section, got %q", payload.Action)
	}
	if payload.TableID != "tbl_main" || payload.RowIndex != 1 || payload.CellIndex != 2 {
		t.Fatalf("unexpected table locator fields: %+v", *payload)
	}
	if payload.SectionLayoutPatch.Orientation != "landscape" || payload.SectionLayoutPatch.MarginLeft != 1200 || payload.SectionLayoutPatch.MarginRight != 1200 {
		t.Fatalf("unexpected section_layout_patch: %+v", payload.SectionLayoutPatch)
	}
	if payload.RequireRetrieval {
		t.Fatalf("expected require_retrieval=false for format_section action")
	}
	if cleaned != "" {
		t.Fatalf("expected cleaned content empty, got %q", cleaned)
	}
}

func TestExtractWordEditorSignalPreservesHeaderFooterPayload(t *testing.T) {
	content := "__WORD_EDITOR_SIG__:{\"action\":\"update_header_footer\",\"target_text\":\"页眉\",\"header_footer\":{\"kind\":\"  HEADER  \",\"variant\":\"  DEFAULT  \",\"blocks\":[{\"type\":\"paragraph\",\"paragraph\":{\"text\":\"  Auramate 内部资料  \"}}]}}"

	payload, cleaned := extractWordEditorSignal(content)
	if payload == nil {
		t.Fatalf("expected payload, got nil")
	}
	if payload.Action != "update_header_footer" {
		t.Fatalf("expected action update_header_footer, got %q", payload.Action)
	}
	if payload.HeaderFooter == nil {
		t.Fatalf("expected header_footer payload")
	}
	if payload.HeaderFooter.Kind != "header" || payload.HeaderFooter.Variant != "default" {
		t.Fatalf("unexpected header_footer selector: %+v", payload.HeaderFooter)
	}
	if len(payload.HeaderFooter.Blocks) != 1 || payload.HeaderFooter.Blocks[0].Paragraph == nil || payload.HeaderFooter.Blocks[0].Paragraph.Text != "Auramate 内部资料" {
		t.Fatalf("unexpected header_footer blocks: %+v", payload.HeaderFooter.Blocks)
	}
	if payload.RequireRetrieval {
		t.Fatalf("expected require_retrieval=false for update_header_footer action")
	}
	if cleaned != "" {
		t.Fatalf("expected cleaned content empty, got %q", cleaned)
	}
}

func TestExtractWordEditorSignalSupportsWordBreakActions(t *testing.T) {
	content := "__WORD_EDITOR_SIG__:{\"action\":\"insert_line_break\",\"target_text\":\"第二行前\",\"position\":\"before\"}"

	payload, cleaned := extractWordEditorSignal(content)
	if payload == nil {
		t.Fatalf("expected payload, got nil")
	}
	if payload.Action != "insert_line_break" {
		t.Fatalf("expected action insert_line_break, got %q", payload.Action)
	}
	if payload.Position != "before" {
		t.Fatalf("expected position before, got %q", payload.Position)
	}
	if !payload.RequireRetrieval {
		t.Fatalf("expected require_retrieval=true for insert_line_break")
	}
	if cleaned != "" {
		t.Fatalf("expected cleaned content empty, got %q", cleaned)
	}

	content = "__WORD_EDITOR_SIG__:{\"action\":\"insert_paragraph_break\",\"target_text\":\"结论段\"}"
	payload, cleaned = extractWordEditorSignal(content)
	if payload == nil {
		t.Fatalf("expected payload, got nil")
	}
	if payload.Action != "insert_paragraph_break" {
		t.Fatalf("expected action insert_paragraph_break, got %q", payload.Action)
	}
	if !payload.RequireRetrieval {
		t.Fatalf("expected require_retrieval=true for insert_paragraph_break")
	}
	if cleaned != "" {
		t.Fatalf("expected cleaned content empty, got %q", cleaned)
	}
}

func TestValidateWordEditorPayloadAllowsRetrievalActionsWithoutReaderSession(t *testing.T) {
	payload := &WordEditorActionPayload{
		Action:               "replace",
		RequireRetrieval:     true,
		ReaderContextSummary: "第二章正文首段，已由 read 确认是标题后的第一段正文",
		FormatBasis:          "沿用当前正文样式，宋体小四，首行缩进保持不变",
	}

	if err := validateWordEditorPayload(payload); err != nil {
		t.Fatalf("expected validation success, got %v", err)
	}
}

func TestValidateWordEditorPayloadRequiresSummariesForRetrievalActions(t *testing.T) {
	payload := &WordEditorActionPayload{
		Action:               "replace",
		RequireRetrieval:     true,
		ReaderSessionID:      "goword-session-1",
		ReaderContextSummary: "正文首段",
		FormatBasis:          "样式",
	}

	if err := validateWordEditorPayload(payload); err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

func TestValidateWordEditorPayloadAllowsAnchoredReplaceWithoutFormatBasis(t *testing.T) {
	payload := &WordEditorActionPayload{
		Action:               "replace",
		TargetText:           "项目总投资475万元",
		NewText:              "项目总投资500万元",
		PreferredParagraphID: "para_59",
		RequireRetrieval:     true,
		ReaderSessionID:      "goword-session-1",
		ReaderContextSummary: "已锁定摘要正文段 para_59，不属于任何表格单元格，此处金额需要与第六章投资估算表总计保持一致。",
	}

	if err := validateWordEditorPayload(payload); err != nil {
		t.Fatalf("expected anchored replace without format_basis to pass, got %v", err)
	}
}

func TestValidateWordEditorPayloadAllowsExplicitTableCellUpdateWithoutFormatBasis(t *testing.T) {
	payload := &WordEditorActionPayload{
		Action:               "update_table_cell",
		TargetText:           "85%",
		TableID:              "tbl_budget",
		RowIndex:             1,
		CellIndex:            1,
		TableCell:            &goword.TableCellInput{Paragraphs: []goword.ParagraphInput{{Text: "92%"}}},
		ContextScope:         "table",
		RequireRetrieval:     true,
		ReaderSessionID:      "goword-session-1",
		ReaderContextSummary: "已锁定预算表第二行第二列的目标单元格，当前需要把 85% 更新为 92%。",
		ExplorationSummary:   "已确认目标位于同一格，继续保持整格单段正文结构。",
	}

	if err := validateWordEditorPayload(payload); err != nil {
		t.Fatalf("expected explicit table cell update without format_basis to pass, got %v", err)
	}
}

func TestValidateWordEditorPayloadRejectsSuspiciousPipeMarker(t *testing.T) {
	payload := &WordEditorActionPayload{
		Action:               "insert",
		NewText:              "第一行 | 第二行",
		RequireRetrieval:     true,
		ReaderContextSummary: "目标位于正文首段后，需要追加一句补充说明。",
		FormatBasis:          "沿用正文 Normal 样式，宋体小四，首行缩进不变。",
	}

	if err := validateWordEditorPayload(payload); err == nil || !strings.Contains(err.Error(), "疑似定位分隔符") {
		t.Fatalf("expected suspicious pipe marker error, got %v", err)
	}
}

func TestValidateWordEditorPayloadAllowsExplicitLiteralPipeIntent(t *testing.T) {
	payload := &WordEditorActionPayload{
		Action:               "insert",
		NewText:              "A | B",
		EditIntent:           "在正文中插入竖线字符作为分隔展示",
		RequireRetrieval:     true,
		ReaderContextSummary: "目标位于正文首段后，需要追加一条带竖线字符的展示文本。",
		FormatBasis:          "沿用正文 Normal 样式，宋体小四，首行缩进不变。",
	}

	if err := validateWordEditorPayload(payload); err != nil {
		t.Fatalf("expected validation success, got %v", err)
	}
}
