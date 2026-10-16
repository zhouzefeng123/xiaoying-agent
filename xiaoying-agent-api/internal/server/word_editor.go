package server

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/auramate/auramate/internal/goword"
)

const wordEditorSignalPrefix = "__WORD_EDITOR_SIG__:"

var wordEditorParagraphIDPattern = regexp.MustCompile(`(?i)(?:段落\s*ID|paragraph[_\s-]*id)\s*[:：]\s*([A-Za-z0-9_-]+)`)
var wordEditorTableIDPattern = regexp.MustCompile(`(?i)(?:表格\s*ID|table[_\s-]*id)\s*[:：]\s*([A-Za-z0-9_-]+)`)
var wordEditorSessionIDPattern = regexp.MustCompile(`(?i)(?:reader[_\s-]*session[_\s-]*id|session[_\s-]*id)\s*[:：]\s*([0-9a-f-]{36})`)
var wordEditorNumericTableIDPattern = regexp.MustCompile(`^\d+$`)
var wordEditorLinePrefixedMarkerPattern = regexp.MustCompile(`(?m)^\s*\d+\|`)

type WordEditorActionPayload struct {
	Action               string                       `json:"action"`
	TargetText           string                       `json:"target_text"`
	TargetSelector       *goword.ActionTargetSelector `json:"target_selector,omitempty"`
	PreferredParagraphID string                       `json:"preferred_paragraph_id,omitempty"`
	TableID              string                       `json:"table_id,omitempty"`
	RowIndex             int                          `json:"row_index,omitempty"`
	CellIndex            int                          `json:"cell_index,omitempty"`
	StylePatch           goword.TextStyle             `json:"style_patch,omitempty"`
	ParagraphStylePatch  goword.ParagraphStyle        `json:"paragraph_style_patch,omitempty"`
	TableSpec            *goword.TableInput           `json:"table_spec,omitempty"`
	TableRow             *goword.TableRowInput        `json:"table_row,omitempty"`
	TableColumn          *goword.TableColumnInput     `json:"table_column,omitempty"`
	TableCell            *goword.TableCellInput       `json:"table_cell,omitempty"`
	HeaderFooter         *goword.HeaderFooterInput    `json:"header_footer,omitempty"`
	SectionLayoutPatch   goword.SectionLayout         `json:"section_layout_patch,omitempty"`
	NewText              string                       `json:"new_text,omitempty"`
	Position             string                       `json:"position,omitempty"`
	EnableTrackChanges   bool                         `json:"enable_track_changes,omitempty"`
	EditIntent           string                       `json:"edit_intent,omitempty"`
	ContextScope         string                       `json:"context_scope,omitempty"`
	Constraints          *goword.ActionConstraints    `json:"constraints,omitempty"`
	FailurePolicy        *goword.ActionFailurePolicy  `json:"failure_policy,omitempty"`
	PreviewOnly          bool                         `json:"preview_only,omitempty"`
	RetrievalQuery       string                       `json:"retrieval_query,omitempty"`
	RequireRetrieval     bool                         `json:"require_retrieval,omitempty"`
	ReaderSessionID      string                       `json:"reader_session_id,omitempty"`
	ReaderContextSummary string                       `json:"reader_context_summary,omitempty"`
	FormatBasis          string                       `json:"format_basis,omitempty"`
	ExplorationSummary   string                       `json:"exploration_summary,omitempty"`
}

type wordEditorSignalEnvelope struct {
	EventType string                  `json:"event_type"`
	Timestamp string                  `json:"timestamp,omitempty"`
	Data      WordEditorActionPayload `json:"data"`
}

func extractWordEditorSignal(content string) (*WordEditorActionPayload, string) {
	payloads, cleaned := extractWordEditorSignals(content)
	if len(payloads) == 0 {
		return nil, cleaned
	}
	return payloads[len(payloads)-1], cleaned
}

func extractWordEditorSignals(content string) ([]*WordEditorActionPayload, string) {
	trimmedContent := strings.TrimSpace(content)
	if trimmedContent == "" {
		return nil, ""
	}

	lines := strings.Split(content, "\n")
	cleanedLines := make([]string, 0, len(lines))
	payloads := make([]*WordEditorActionPayload, 0, len(lines))

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, wordEditorSignalPrefix) {
			cleanedLines = append(cleanedLines, line)
			continue
		}

		rawPayload := strings.TrimSpace(strings.TrimPrefix(trimmed, wordEditorSignalPrefix))
		if rawPayload == "" {
			continue
		}

		var envelope wordEditorSignalEnvelope
		if err := json.Unmarshal([]byte(rawPayload), &envelope); err == nil && strings.TrimSpace(envelope.Data.Action) != "" {
			normalizeWordEditorPayload(&envelope.Data)
			payloadCopy := envelope.Data
			payloads = append(payloads, &payloadCopy)
			continue
		}

		var direct WordEditorActionPayload
		if err := json.Unmarshal([]byte(rawPayload), &direct); err == nil && strings.TrimSpace(direct.Action) != "" {
			normalizeWordEditorPayload(&direct)
			payloadCopy := direct
			payloads = append(payloads, &payloadCopy)
			continue
		}

		cleanedLines = append(cleanedLines, line)
	}

	return payloads, strings.TrimSpace(strings.Join(cleanedLines, "\n"))
}

// ExtractWordEditorSignal exposes the production signal parsing and normalization
// path for script-based regression evaluators.
func ExtractWordEditorSignal(content string) (*WordEditorActionPayload, string) {
	return extractWordEditorSignal(content)
}

// ExtractWordEditorSignals exposes all parsed signals in original order for
// batch-style evaluators and multi-action consumers.
func ExtractWordEditorSignals(content string) ([]*WordEditorActionPayload, string) {
	return extractWordEditorSignals(content)
}

func normalizeWordEditorPayload(payload *WordEditorActionPayload) {
	if payload == nil {
		return
	}

	payload.Action = strings.ToLower(strings.TrimSpace(payload.Action))
	payload.TargetText = strings.TrimSpace(payload.TargetText)
	if payload.TargetSelector != nil {
		payload.TargetSelector.Type = strings.ToLower(strings.TrimSpace(payload.TargetSelector.Type))
		payload.TargetSelector.ParagraphID = strings.TrimSpace(payload.TargetSelector.ParagraphID)
		payload.TargetSelector.TableID = normalizeWordEditorTableID(payload.TargetSelector.TableID)
		payload.TargetSelector.Chapter = strings.TrimSpace(payload.TargetSelector.Chapter)
		payload.TargetSelector.TextEquals = strings.TrimSpace(payload.TargetSelector.TextEquals)
		payload.TargetSelector.Region = strings.ToLower(strings.TrimSpace(payload.TargetSelector.Region))
	}
	payload.PreferredParagraphID = strings.TrimSpace(payload.PreferredParagraphID)
	payload.TableID = normalizeWordEditorTableID(payload.TableID)
	payload.StylePatch.StyleID = strings.TrimSpace(payload.StylePatch.StyleID)
	payload.StylePatch.FontFamily = strings.TrimSpace(payload.StylePatch.FontFamily)
	payload.StylePatch.ThemeFontASCII = strings.TrimSpace(payload.StylePatch.ThemeFontASCII)
	payload.StylePatch.ThemeFontEastAsia = strings.TrimSpace(payload.StylePatch.ThemeFontEastAsia)
	payload.StylePatch.Color = strings.TrimSpace(payload.StylePatch.Color)
	payload.StylePatch.ThemeColor = strings.TrimSpace(payload.StylePatch.ThemeColor)
	payload.ParagraphStylePatch.StyleID = strings.TrimSpace(payload.ParagraphStylePatch.StyleID)
	payload.ParagraphStylePatch.Alignment = strings.ToLower(strings.TrimSpace(payload.ParagraphStylePatch.Alignment))
	if payload.ParagraphStylePatch.Numbering != nil {
		payload.ParagraphStylePatch.Numbering.NumID = strings.TrimSpace(payload.ParagraphStylePatch.Numbering.NumID)
	}
	if payload.TableSpec != nil {
		normalizeWordEditorTableSpec(payload.TableSpec)
	}
	if payload.TableRow != nil {
		normalizeWordEditorTableRow(payload.TableRow)
	}
	if payload.TableColumn != nil {
		normalizeWordEditorTableColumn(payload.TableColumn)
	}
	if payload.TableCell != nil {
		normalizeWordEditorTableCell(payload.TableCell)
	}
	if payload.HeaderFooter != nil {
		payload.HeaderFooter.Kind = strings.ToLower(strings.TrimSpace(payload.HeaderFooter.Kind))
		payload.HeaderFooter.Variant = strings.ToLower(strings.TrimSpace(payload.HeaderFooter.Variant))
		for blockIndex := range payload.HeaderFooter.Blocks {
			block := &payload.HeaderFooter.Blocks[blockIndex]
			block.Type = goword.BlockType(strings.ToLower(strings.TrimSpace(string(block.Type))))
			if block.Paragraph != nil {
				block.Paragraph.Text = strings.TrimSpace(block.Paragraph.Text)
				block.Paragraph.Style.StyleID = strings.TrimSpace(block.Paragraph.Style.StyleID)
				block.Paragraph.Style.Alignment = strings.ToLower(strings.TrimSpace(block.Paragraph.Style.Alignment))
			}
			if block.Table != nil {
				normalizeWordEditorTableSpec(block.Table)
			}
		}
	}
	payload.SectionLayoutPatch.Orientation = strings.ToLower(strings.TrimSpace(payload.SectionLayoutPatch.Orientation))
	payload.Position = strings.ToLower(strings.TrimSpace(payload.Position))
	payload.EditIntent = strings.TrimSpace(payload.EditIntent)
	payload.ContextScope = strings.ToLower(strings.TrimSpace(payload.ContextScope))
	if payload.Constraints != nil {
		payload.Constraints.RawText = strings.TrimSpace(payload.Constraints.RawText)
	}
	if payload.FailurePolicy != nil {
		payload.FailurePolicy.OnAmbiguous = strings.ToLower(strings.TrimSpace(payload.FailurePolicy.OnAmbiguous))
		payload.FailurePolicy.OnValidationFailed = strings.ToLower(strings.TrimSpace(payload.FailurePolicy.OnValidationFailed))
	}
	payload.RetrievalQuery = strings.TrimSpace(payload.RetrievalQuery)
	payload.ReaderSessionID = strings.TrimSpace(payload.ReaderSessionID)
	payload.ReaderContextSummary = strings.TrimSpace(payload.ReaderContextSummary)
	payload.FormatBasis = strings.TrimSpace(payload.FormatBasis)
	payload.ExplorationSummary = strings.TrimSpace(payload.ExplorationSummary)

	if payload.PreferredParagraphID == "" {
		payload.PreferredParagraphID = extractWordEditorContextID(wordEditorParagraphIDPattern, payload.ReaderContextSummary, payload.ExplorationSummary)
	}
	if payload.PreferredParagraphID == "" && payload.TargetSelector != nil && payload.TargetSelector.Type == "paragraph_id_selector" {
		payload.PreferredParagraphID = payload.TargetSelector.ParagraphID
	}
	if payload.TableID == "" {
		payload.TableID = normalizeWordEditorTableID(extractWordEditorContextID(wordEditorTableIDPattern, payload.ReaderContextSummary, payload.ExplorationSummary))
	}
	if payload.TableID == "" && payload.TargetSelector != nil && (payload.TargetSelector.Type == "table_selector" || payload.TargetSelector.Type == "table_cell_selector") {
		payload.TableID = payload.TargetSelector.TableID
	}
	if payload.ReaderSessionID == "" {
		payload.ReaderSessionID = extractWordEditorContextID(wordEditorSessionIDPattern, payload.ReaderContextSummary, payload.ExplorationSummary)
	}

	if payload.Position != "before" && payload.Position != "after" {
		payload.Position = "after"
	}

	switch payload.ContextScope {
	case "line", "paragraph", "table", "all":
	default:
		payload.ContextScope = "paragraph"
	}

	if payload.EditIntent == "" {
		payload.EditIntent = "精准修改"
	}

	if payload.RetrievalQuery == "" {
		payload.RetrievalQuery = payload.TargetText
	}

	if payload.Action == "replace" || payload.Action == "insert" || payload.Action == "insert_paragraph_break" || payload.Action == "insert_line_break" || payload.Action == "insert_table" || payload.Action == "insert_table_row" || payload.Action == "delete_table_row" || payload.Action == "insert_table_column" || payload.Action == "delete_table_column" || payload.Action == "update_table_cell" || payload.Action == "delete" || payload.Action == "format" || payload.Action == "format_paragraph" {
		payload.RequireRetrieval = true
	}
}

func normalizeWordEditorTableID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	if wordEditorNumericTableIDPattern.MatchString(trimmed) {
		return "tbl_" + trimmed
	}
	return trimmed
}

func wordEditorNormalizeText(value string) string {
	return strings.TrimSpace(value)
}

func wordEditorMentionsTableAnchor(values ...string) bool {
	hasExplicitNonTableCue := false
	for _, value := range values {
		normalized := wordEditorNormalizeText(value)
		if normalized == "" {
			continue
		}
		if strings.Contains(normalized, "不是表格单元格") || strings.Contains(normalized, "不属于任何表格单元格") || strings.Contains(normalized, "纯文本段落") || strings.Contains(normalized, "独立正文段落") {
			hasExplicitNonTableCue = true
			continue
		}
		if strings.Contains(normalized, "row_index") || strings.Contains(normalized, "cell_index") || strings.Contains(normalized, "单元格") || strings.Contains(normalized, "同格") || strings.Contains(normalized, "同行") {
			return true
		}
	}
	if hasExplicitNonTableCue {
		return false
	}
	return false
}

func wordEditorHasExplicitTableContext(payload *WordEditorActionPayload) bool {
	if payload == nil {
		return false
	}
	if payload.ContextScope == "table" {
		return true
	}
	if payload.TableID != "" {
		return true
	}
	return wordEditorMentionsTableAnchor(payload.ReaderContextSummary, payload.FormatBasis, payload.ExplorationSummary)
}

func wordEditorIsExpandedRewrite(targetText, newText string) bool {
	original := wordEditorNormalizeText(targetText)
	updated := wordEditorNormalizeText(newText)
	if original == "" || updated == "" {
		return false
	}
	originalLen := len([]rune(original))
	updatedLen := len([]rune(updated))
	if updatedLen <= originalLen {
		return false
	}
	growth := updatedLen - originalLen
	return growth >= 8 || updatedLen >= int(float64(originalLen)*1.2)
}

func wordEditorAllowsRelaxedFormatBasis(payload *WordEditorActionPayload) bool {
	if payload == nil {
		return false
	}
	switch payload.Action {
	case "replace":
		if payload.PreferredParagraphID == "" {
			return false
		}
		if wordEditorHasExplicitTableContext(payload) {
			return false
		}
		return !wordEditorIsExpandedRewrite(payload.TargetText, payload.NewText)
	case "update_table_cell":
		if payload.TableID == "" || payload.TableCell == nil || payload.ReaderSessionID == "" {
			return false
		}
		return wordEditorHasExplicitTableContext(payload)
	default:
		return false
	}
}

func extractWordEditorContextID(pattern *regexp.Regexp, candidates ...string) string {
	for _, candidate := range candidates {
		trimmed := strings.TrimSpace(candidate)
		if trimmed == "" {
			continue
		}
		match := pattern.FindStringSubmatch(trimmed)
		if len(match) >= 2 {
			return strings.TrimSpace(match[1])
		}
	}
	return ""
}

func normalizeWordEditorTableSpec(table *goword.TableInput) {
	if table == nil {
		return
	}
	table.PropertiesXML = strings.TrimSpace(table.PropertiesXML)
	table.GridXML = strings.TrimSpace(table.GridXML)
	for rowIndex := range table.Rows {
		normalizeWordEditorTableRow(&table.Rows[rowIndex])
	}
}

func normalizeWordEditorTableRow(row *goword.TableRowInput) {
	if row == nil {
		return
	}
	row.PropertiesXML = strings.TrimSpace(row.PropertiesXML)
	for cellIndex := range row.Cells {
		normalizeWordEditorTableCell(&row.Cells[cellIndex])
	}
}

func normalizeWordEditorTableColumn(column *goword.TableColumnInput) {
	if column == nil {
		return
	}
	for cellIndex := range column.Cells {
		normalizeWordEditorTableCell(&column.Cells[cellIndex])
	}
}

func normalizeWordEditorTableCell(cell *goword.TableCellInput) {
	if cell == nil {
		return
	}
	cell.PropertiesXML = strings.TrimSpace(cell.PropertiesXML)
	for paragraphIndex := range cell.Paragraphs {
		paragraph := &cell.Paragraphs[paragraphIndex]
		paragraph.Text = strings.TrimSpace(paragraph.Text)
		paragraph.Style.StyleID = strings.TrimSpace(paragraph.Style.StyleID)
		paragraph.Style.Alignment = strings.ToLower(strings.TrimSpace(paragraph.Style.Alignment))
		for runIndex := range paragraph.Runs {
			paragraph.Runs[runIndex].Text = strings.TrimSpace(paragraph.Runs[runIndex].Text)
			paragraph.Runs[runIndex].Style.StyleID = strings.TrimSpace(paragraph.Runs[runIndex].Style.StyleID)
			paragraph.Runs[runIndex].Style.FontFamily = strings.TrimSpace(paragraph.Runs[runIndex].Style.FontFamily)
		}
	}
}

func validateWordEditorPayload(payload *WordEditorActionPayload) error {
	if payload == nil {
		return fmt.Errorf("word editor payload is nil")
	}
	if hasSuspiciousWordEditorPipeMarker(payload) {
		return fmt.Errorf("Word 编辑动作 %q 包含疑似定位分隔符“|”；请改用 insert_line_break / insert_paragraph_break 表达换行或断段，如需写入字面量竖线请在意图中明确说明“插入竖线字符”", payload.Action)
	}
	if !payload.RequireRetrieval {
		return nil
	}
	if len(strings.TrimSpace(payload.ReaderContextSummary)) < 8 {
		return fmt.Errorf("Word 编辑动作 %q 缺少 reader_context_summary；应先用 read 获取全文角色与修改语境，必要时再结合 auramate-word-reader 说明精确落点", payload.Action)
	}
	if len(strings.TrimSpace(payload.FormatBasis)) < 8 && !wordEditorAllowsRelaxedFormatBasis(payload) {
		return fmt.Errorf("Word 编辑动作 %q 缺少 format_basis；应先基于 read 和/或 auramate-word-reader 提供样式或格式依据", payload.Action)
	}
	return nil
}

func hasSuspiciousWordEditorPipeMarker(payload *WordEditorActionPayload) bool {
	if payload == nil {
		return false
	}
	if hasExplicitLiteralPipeCue(payload.EditIntent, payload.ReaderContextSummary, payload.FormatBasis, payload.ExplorationSummary, payload.TargetText) {
		return false
	}
	for _, candidate := range collectWordEditorPayloadTexts(payload) {
		if containsSuspiciousWordEditorPipeMarker(candidate) {
			return true
		}
	}
	return false
}

func hasExplicitLiteralPipeCue(parts ...string) bool {
	for _, part := range parts {
		lower := strings.ToLower(strings.TrimSpace(part))
		if lower == "" {
			continue
		}
		if strings.Contains(lower, "竖线") || strings.Contains(lower, "竖杠") || strings.Contains(lower, "管道符") || strings.Contains(lower, "vertical bar") || strings.Contains(lower, "pipe") {
			return true
		}
	}
	return false
}

func containsSuspiciousWordEditorPipeMarker(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	if wordEditorLinePrefixedMarkerPattern.MatchString(trimmed) {
		return true
	}
	return strings.Contains(trimmed, "|")
}

func collectWordEditorPayloadTexts(payload *WordEditorActionPayload) []string {
	texts := make([]string, 0, 8)
	appendWordEditorPayloadText := func(value string) {
		trimmed := strings.TrimSpace(value)
		if trimmed != "" {
			texts = append(texts, trimmed)
		}
	}

	appendWordEditorPayloadText(payload.NewText)
	collectTableInputTexts(payload.TableSpec, appendWordEditorPayloadText)
	collectTableRowInputTexts(payload.TableRow, appendWordEditorPayloadText)
	collectTableColumnInputTexts(payload.TableColumn, appendWordEditorPayloadText)
	collectTableCellInputTexts(payload.TableCell, appendWordEditorPayloadText)
	collectHeaderFooterInputTexts(payload.HeaderFooter, appendWordEditorPayloadText)
	return texts
}

func collectTableInputTexts(table *goword.TableInput, appendText func(string)) {
	if table == nil {
		return
	}
	for rowIndex := range table.Rows {
		collectTableRowInputTexts(&table.Rows[rowIndex], appendText)
	}
}

func collectTableRowInputTexts(row *goword.TableRowInput, appendText func(string)) {
	if row == nil {
		return
	}
	for cellIndex := range row.Cells {
		collectTableCellInputTexts(&row.Cells[cellIndex], appendText)
	}
}

func collectTableColumnInputTexts(column *goword.TableColumnInput, appendText func(string)) {
	if column == nil {
		return
	}
	for cellIndex := range column.Cells {
		collectTableCellInputTexts(&column.Cells[cellIndex], appendText)
	}
}

func collectTableCellInputTexts(cell *goword.TableCellInput, appendText func(string)) {
	if cell == nil {
		return
	}
	for paragraphIndex := range cell.Paragraphs {
		collectParagraphInputTexts(&cell.Paragraphs[paragraphIndex], appendText)
	}
}

func collectParagraphInputTexts(paragraph *goword.ParagraphInput, appendText func(string)) {
	if paragraph == nil {
		return
	}
	appendText(paragraph.Text)
	for runIndex := range paragraph.Runs {
		appendText(paragraph.Runs[runIndex].Text)
	}
}

func collectHeaderFooterInputTexts(headerFooter *goword.HeaderFooterInput, appendText func(string)) {
	if headerFooter == nil {
		return
	}
	for blockIndex := range headerFooter.Blocks {
		block := &headerFooter.Blocks[blockIndex]
		if block.Paragraph != nil {
			collectParagraphInputTexts(block.Paragraph, appendText)
		}
		if block.Table != nil {
			collectTableInputTexts(block.Table, appendText)
		}
	}
}

func (s *Server) shouldBroadcastWordEditorAction(sessionID string, payload *WordEditorActionPayload) bool {
	if s == nil || payload == nil {
		return true
	}

	signatureBytes, err := json.Marshal(payload)
	if err != nil {
		return true
	}
	signature := string(signatureBytes)
	now := time.Now()

	s.wordEditorMu.Lock()
	defer s.wordEditorMu.Unlock()

	if s.wordEditorLastEmit == nil {
		s.wordEditorLastEmit = make(map[string]wordEditorEmission)
	}

	if previous, ok := s.wordEditorLastEmit[sessionID]; ok && previous.signature == signature && now.Sub(previous.emittedAt) <= 2*time.Second {
		return false
	}

	s.wordEditorLastEmit[sessionID] = wordEditorEmission{
		signature: signature,
		emittedAt: now,
	}
	return true
}

func (s *Server) broadcastWordEditorAction(sessionID string, payload *WordEditorActionPayload) error {
	if payload == nil {
		return nil
	}
	if err := validateWordEditorPayload(payload); err != nil {
		return err
	}
	if !s.shouldBroadcastWordEditorAction(sessionID, payload) {
		return nil
	}

	s.Broadcast(Event{
		Type:      EventTypeWordEditorAction,
		SessionID: sessionID,
		Data:      payload,
	})
	return nil
}
