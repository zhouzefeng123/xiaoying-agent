package tools

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auramate/auramate/internal/goword"
)

func TestWordReaderToolReadyPayload(t *testing.T) {
	response, err := NewWordReaderTool().Run(context.Background(), ToolCall{})
	if err != nil {
		t.Fatalf("run word reader tool: %v", err)
	}
	if response.IsError {
		t.Fatalf("expected ready response, got error: %s", response.Content)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(response.Content), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if payload["status"] != "ready" {
		t.Fatalf("expected ready status, got %#v", payload["status"])
	}
}

func TestNativeWordToolsExposeSelfContainedDescriptions(t *testing.T) {
	readerDescription := NewWordReaderTool().Info().Description
	if !strings.Contains(readerDescription, "推荐顺序是 read -> 按需 auramate-word-reader -> auramate-word-copilot") {
		t.Fatalf("expected reader description to contain integrated workflow guidance, got %q", readerDescription)
	}
	if !strings.Contains(readerDescription, "第一次调用应优先走最小表格读取路径") {
		t.Fatalf("expected reader description to mention minimal table-first guidance, got %q", readerDescription)
	}
	if !strings.Contains(readerDescription, "full_text_truncated") {
		t.Fatalf("expected reader description to mention truncation contract, got %q", readerDescription)
	}

	copilotDescription := NewWordCopilotTool().Info().Description
	if !strings.Contains(copilotDescription, "reader_context_summary") {
		t.Fatalf("expected copilot description to mention evidence requirements, got %q", copilotDescription)
	}
	if !strings.Contains(copilotDescription, "reader_session_id") {
		t.Fatalf("expected copilot description to mention shared session requirements, got %q", copilotDescription)
	}
	if !strings.Contains(copilotDescription, "不要拿同一份 evidence 或同一套 payload 连续重复调用 auramate-word-copilot 试探") {
		t.Fatalf("expected copilot description to mention duplicate-copilot guard, got %q", copilotDescription)
	}
	if !strings.Contains(copilotDescription, "update_header_footer 必须显式提供完整 header_footer 结构") {
		t.Fatalf("expected copilot description to mention structured header/footer payloads, got %q", copilotDescription)
	}
}

func TestWordCopilotToolRejectsFormatSectionWithoutSectionLayoutPatch(t *testing.T) {
	input, err := json.Marshal(map[string]any{
		"action":      "format_section",
		"target_text": "全文",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordCopilotTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word copilot tool: %v", err)
	}
	if !response.IsError {
		t.Fatalf("expected format_section without section_layout_patch to fail, got success: %s", response.Content)
	}
	if !strings.Contains(response.Content, "section_layout_patch") {
		t.Fatalf("expected error to mention section_layout_patch, got %q", response.Content)
	}
}

func TestWordCopilotToolAcceptsFormatSectionWithPageNumberPatch(t *testing.T) {
	input, err := json.Marshal(map[string]any{
		"action":      "format_section",
		"target_text": "全文",
		"section_layout_patch": map[string]any{
			"page_number_start":  5,
			"page_number_format": "upperRoman",
		},
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordCopilotTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word copilot tool: %v", err)
	}
	if response.IsError {
		t.Fatalf("expected valid format_section payload to succeed, got error: %s", response.Content)
	}
	if !strings.Contains(response.Content, "__WORD_EDITOR_SIG__") {
		t.Fatalf("expected word editor signal, got %q", response.Content)
	}
}

func TestWordReaderToolReturnsStructuredQueryAndExploration(t *testing.T) {
	service := goword.NewService(t.TempDir())
	SetWordReaderToolGoWordService(service)
	t.Cleanup(func() {
		SetWordReaderToolGoWordService(nil)
	})

	document := &goword.Document{
		Name:       "reader.docx",
		SourcePath: "reader.docx",
		Revision:   1,
		Blocks: []goword.Block{
			{
				Type: goword.BlockTypeParagraph,
				Paragraph: &goword.Paragraph{
					ID:    "p-title",
					Style: goword.ParagraphStyle{StyleID: "Heading1"},
					Runs:  []goword.Run{{ID: "r-title", Text: "执行摘要"}},
				},
			},
			{
				Type:  goword.BlockTypeTable,
				Table: testWordReaderTable(),
			},
		},
		Meta: goword.DocumentMeta{Format: "docx"},
	}

	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	input, err := json.Marshal(map[string]any{
		"session_id":      session.ID,
		"query":           "总计",
		"focus_hit_index": 0,
		"include_outline": true,
		"context_scope":   "all",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordReaderTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word reader tool: %v", err)
	}
	if response.IsError {
		t.Fatalf("expected successful response, got error: %s", response.Content)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(response.Content), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if payload["status"] != "ok" {
		t.Fatalf("expected ok status, got %#v", payload["status"])
	}
	queryPayload, ok := payload["query"].(map[string]any)
	if !ok {
		t.Fatalf("expected query payload, got %#v", payload["query"])
	}
	hits, ok := queryPayload["hits"].([]any)
	if !ok || len(hits) == 0 {
		t.Fatalf("expected non-empty hits, got %#v", queryPayload["hits"])
	}
	firstHit, ok := hits[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first hit map, got %#v", hits[0])
	}
	if firstHit["table_id"] != "tbl-budget" {
		t.Fatalf("expected hit table_id tbl-budget, got %#v", firstHit["table_id"])
	}
	exploration, ok := payload["exploration"].(map[string]any)
	if !ok {
		t.Fatalf("expected exploration payload, got %#v", payload["exploration"])
	}
	tableContext, ok := exploration["table"].(map[string]any)
	if !ok {
		t.Fatalf("expected table exploration, got %#v", exploration["table"])
	}
	if tableContext["table_id"] != "tbl-budget" {
		t.Fatalf("expected exploration table_id tbl-budget, got %#v", tableContext["table_id"])
	}
	styleCatalog, ok := payload["style_catalog"].(map[string]any)
	if !ok {
		t.Fatalf("expected style_catalog map, got %#v", payload["style_catalog"])
	}
	if _, ok := styleCatalog["styles"].([]any); !ok {
		t.Fatalf("expected styles array in style_catalog, got %#v", styleCatalog["styles"])
	}
}

func TestWordReaderToolReturnsExactMatchedQueries(t *testing.T) {
	service := goword.NewService(t.TempDir())
	SetWordReaderToolGoWordService(service)
	t.Cleanup(func() {
		SetWordReaderToolGoWordService(nil)
	})

	document := &goword.Document{
		Name:       "reader.docx",
		SourcePath: "reader.docx",
		Revision:   1,
		Blocks: []goword.Block{
			{
				Type: goword.BlockTypeParagraph,
				Paragraph: &goword.Paragraph{
					ID:    "p-title",
					Style: goword.ParagraphStyle{StyleID: "Heading1"},
					Runs:  []goword.Run{{ID: "r-title", Text: "执行摘要"}},
				},
			},
			{
				Type:  goword.BlockTypeTable,
				Table: testWordReaderTable(),
			},
		},
		Meta: goword.DocumentMeta{Format: "docx"},
	}

	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	input, err := json.Marshal(map[string]any{
		"session_id":    session.ID,
		"query":         "金额||总计",
		"context_scope": "all",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordReaderTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word reader tool: %v", err)
	}
	if response.IsError {
		t.Fatalf("expected successful response, got error: %s", response.Content)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(response.Content), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	queryPayload, ok := payload["query"].(map[string]any)
	if !ok {
		t.Fatalf("expected query payload, got %#v", payload["query"])
	}
	hits, ok := queryPayload["hits"].([]any)
	if !ok || len(hits) < 2 {
		t.Fatalf("expected at least two hits, got %#v", queryPayload["hits"])
	}

	matchedByPreview := make(map[string][]string)
	for _, rawHit := range hits {
		hit, ok := rawHit.(map[string]any)
		if !ok {
			continue
		}
		preview, _ := hit["preview"].(string)
		matchedRaw, _ := hit["matched_queries"].([]any)
		matched := make([]string, 0, len(matchedRaw))
		for _, item := range matchedRaw {
			if value, ok := item.(string); ok {
				matched = append(matched, value)
			}
		}
		matchedByPreview[preview] = matched
	}

	if got := matchedByPreview["金额"]; len(got) != 1 || got[0] != "金额" {
		t.Fatalf("expected 金额 hit to match only 金额, got %#v", got)
	}
	if got := matchedByPreview["总计"]; len(got) != 1 || got[0] != "总计" {
		t.Fatalf("expected 总计 hit to match only 总计, got %#v", got)
	}
}

func TestWordReaderToolKeepsCreatedSessionReusable(t *testing.T) {
	service := goword.NewService(t.TempDir())
	SetWordReaderToolGoWordService(service)
	t.Cleanup(func() {
		SetWordReaderToolGoWordService(nil)
	})

	document := &goword.Document{
		Name:       "reader.docx",
		SourcePath: "reader.docx",
		Revision:   1,
		Blocks: []goword.Block{
			{
				Type: goword.BlockTypeParagraph,
				Paragraph: &goword.Paragraph{
					ID:    "p-title",
					Style: goword.ParagraphStyle{StyleID: "Heading1"},
					Runs:  []goword.Run{{ID: "r-title", Text: "执行摘要"}},
				},
			},
			{
				Type:  goword.BlockTypeTable,
				Table: testWordReaderTable(),
			},
		},
		Meta: goword.DocumentMeta{Format: "docx"},
	}

	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	outputPath := filepath.Join(t.TempDir(), "reader.docx")
	if _, err := service.Save(session.ID, outputPath); err != nil {
		t.Fatalf("save docx: %v", err)
	}

	input, err := json.Marshal(map[string]any{
		"path":  outputPath,
		"query": "总计",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordReaderTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word reader tool: %v", err)
	}
	if response.IsError {
		t.Fatalf("expected successful response, got error: %s", response.Content)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(response.Content), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	sessionPayload, ok := payload["session"].(map[string]any)
	if !ok {
		t.Fatalf("expected session payload, got %#v", payload["session"])
	}
	returnedSessionID, _ := sessionPayload["id"].(string)
	if strings.TrimSpace(returnedSessionID) == "" {
		t.Fatalf("expected returned session id, got %#v", sessionPayload["id"])
	}
	if _, err := service.Snapshot(returnedSessionID); err != nil {
		t.Fatalf("expected returned session to remain reusable, got snapshot error: %v", err)
	}
}

func TestWordReaderToolAcceptsFilePathAlias(t *testing.T) {
	service := goword.NewService(t.TempDir())
	SetWordReaderToolGoWordService(service)
	t.Cleanup(func() {
		SetWordReaderToolGoWordService(nil)
	})

	document := &goword.Document{
		Name:       "reader.docx",
		SourcePath: "reader.docx",
		Revision:   1,
		Blocks: []goword.Block{{
			Type: goword.BlockTypeParagraph,
			Paragraph: &goword.Paragraph{
				ID:    "p-1",
				Style: goword.ParagraphStyle{StyleID: "Normal"},
				Runs:  []goword.Run{{ID: "r-1", Text: "附录说明"}},
			},
		}},
		Meta: goword.DocumentMeta{Format: "docx"},
	}

	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	outputPath := filepath.Join(t.TempDir(), "reader.docx")
	if _, err := service.Save(session.ID, outputPath); err != nil {
		t.Fatalf("save docx: %v", err)
	}

	input, err := json.Marshal(map[string]any{
		"file_path": outputPath,
		"query":     "附录说明",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordReaderTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word reader tool: %v", err)
	}
	if response.IsError {
		t.Fatalf("expected successful response, got error: %s", response.Content)
	}
	if !strings.Contains(response.Content, "\"status\":\"ok\"") {
		t.Fatalf("expected ok response for file_path alias, got %s", response.Content)
	}
}

func TestWordReaderToolAppliesMaxCharsContract(t *testing.T) {
	service := goword.NewService(t.TempDir())
	SetWordReaderToolGoWordService(service)
	t.Cleanup(func() {
		SetWordReaderToolGoWordService(nil)
	})

	longText := strings.Repeat("扩写内容", 24)
	document := &goword.Document{
		Name:       "reader.docx",
		SourcePath: "reader.docx",
		Revision:   1,
		Blocks: []goword.Block{
			{
				Type: goword.BlockTypeParagraph,
				Paragraph: &goword.Paragraph{
					ID:    "p-long",
					Style: goword.ParagraphStyle{StyleID: "Normal"},
					Runs:  []goword.Run{{ID: "r-long", Text: longText}},
				},
			},
		},
		Meta: goword.DocumentMeta{Format: "docx"},
	}

	session, err := service.CreateSession(document)
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	input, err := json.Marshal(map[string]any{
		"session_id":         session.ID,
		"query":              "扩写内容",
		"focus_paragraph_id": "p-long",
		"include_full_text":  true,
		"max_chars":          40,
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordReaderTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word reader tool: %v", err)
	}
	if response.IsError {
		t.Fatalf("expected successful response, got error: %s", response.Content)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(response.Content), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	fullText, _ := payload["full_text_excerpt"].(string)
	if len([]rune(fullText)) > 40 {
		t.Fatalf("expected full_text_excerpt to respect max_chars, got length %d", len([]rune(fullText)))
	}
	if truncated, _ := payload["full_text_truncated"].(bool); !truncated {
		t.Fatalf("expected full_text_truncated to be true")
	}
	if omitted, _ := payload["full_text_omitted_chars"].(float64); omitted <= 0 {
		t.Fatalf("expected omitted chars to be recorded, got %#v", payload["full_text_omitted_chars"])
	}
	exploration, ok := payload["exploration"].(map[string]any)
	if !ok {
		t.Fatalf("expected exploration payload, got %#v", payload["exploration"])
	}
	paragraph, ok := exploration["paragraph"].(map[string]any)
	if !ok {
		t.Fatalf("expected paragraph exploration, got %#v", exploration["paragraph"])
	}
	paragraphText, _ := paragraph["text"].(string)
	if len([]rune(paragraphText)) > 40 {
		t.Fatalf("expected exploration paragraph text to respect max_chars, got length %d", len([]rune(paragraphText)))
	}
}

func TestWordCopilotToolRequiresReaderEvidenceForReplace(t *testing.T) {
	input, err := json.Marshal(map[string]any{
		"action":      "replace",
		"target_text": "原文段落",
		"new_text":    "新文段落",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordCopilotTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word copilot tool: %v", err)
	}
	if !response.IsError {
		t.Fatalf("expected validation error, got success: %s", response.Content)
	}
	if !strings.Contains(response.Content, "reader_context_summary") {
		t.Fatalf("expected reader evidence hint, got %s", response.Content)
	}
}

func TestWordCopilotToolEmitsSignalForUpdateTableCell(t *testing.T) {
	input, err := json.Marshal(map[string]any{
		"action":                 "update_table_cell",
		"target_text":            "1000",
		"table_id":               "tbl-budget",
		"row_index":              1,
		"cell_index":             1,
		"table_cell":             map[string]any{"paragraphs": []map[string]any{{"text": "1200"}}},
		"reader_session_id":      "session-1",
		"reader_context_summary": "命中预算表第二行第二列，当前单元格内容为 1000。",
		"format_basis":           "沿用命中单元格的现有正文样式，保持表格内部字号和对齐一致。",
		"edit_intent":            "把预算值更新为 1200。",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordCopilotTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word copilot tool: %v", err)
	}
	if response.IsError {
		t.Fatalf("expected successful response, got error: %s", response.Content)
	}
	if !strings.Contains(response.Content, "__WORD_EDITOR_SIG__:") {
		t.Fatalf("expected word editor signal, got %s", response.Content)
	}
	if !strings.Contains(response.Content, "update_table_cell") {
		t.Fatalf("expected action in signal response, got %s", response.Content)
	}
}

func TestWordCopilotToolAllowsUpdateTableCellWithNewTextShorthand(t *testing.T) {
	input, err := json.Marshal(map[string]any{
		"action":                 "update_table_cell",
		"target_text":            "85%",
		"new_text":               "92%",
		"table_id":               "tbl-budget",
		"row_index":              1,
		"cell_index":             1,
		"reader_session_id":      "session-1",
		"reader_context_summary": "命中预算表第二行第二列，当前单元格内容为 85%。",
		"edit_intent":            "把表格里的 85% 更新为 92%。",
		"context_scope":          "table",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordCopilotTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word copilot tool: %v", err)
	}
	if response.IsError {
		t.Fatalf("expected shorthand update_table_cell to succeed, got error: %s", response.Content)
	}
	if !strings.Contains(response.Content, "\"action\":\"update_table_cell\"") {
		t.Fatalf("expected update_table_cell action in signal response, got %s", response.Content)
	}
	if !strings.Contains(response.Content, "\"table_cell\":{\"paragraphs\":[{\"text\":\"92%\"") {
		t.Fatalf("expected shorthand new_text to be normalized into table_cell, got %s", response.Content)
	}
	if strings.Contains(response.Content, "\"new_text\":\"92%\"") {
		t.Fatalf("expected shorthand new_text to be consumed into canonical table_cell payload, got %s", response.Content)
	}
}

func TestWordCopilotToolAllowsUpdateTableCellWithoutTargetTextWhenCoordinatesExplicit(t *testing.T) {
	input, err := json.Marshal(map[string]any{
		"action":                 "update_table_cell",
		"new_text":               "92%",
		"table_id":               "tbl-budget",
		"row_index":              1,
		"cell_index":             1,
		"reader_session_id":      "session-1",
		"reader_context_summary": "命中预算表第二行第二列，当前目标是右侧值单元格。",
		"edit_intent":            "把表格里的当前值更新为 92%。",
		"context_scope":          "table",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordCopilotTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word copilot tool: %v", err)
	}
	if response.IsError {
		t.Fatalf("expected explicit coordinate update_table_cell without target_text to succeed, got error: %s", response.Content)
	}
	if !strings.Contains(response.Content, "\"action\":\"update_table_cell\"") {
		t.Fatalf("expected update_table_cell action in signal response, got %s", response.Content)
	}
	if !strings.Contains(response.Content, "\"target_text\":\"\"") {
		t.Fatalf("expected empty target_text to be tolerated in canonical payload, got %s", response.Content)
	}
	if !strings.Contains(response.Content, "\"table_cell\":{\"paragraphs\":[{\"text\":\"92%\"") {
		t.Fatalf("expected shorthand new_text to still normalize into table_cell, got %s", response.Content)
	}
}

func TestWordCopilotToolCoercesSimpleTableReplaceToUpdateTableCell(t *testing.T) {
	input, err := json.Marshal(map[string]any{
		"action":                 "replace",
		"target_text":            "85%",
		"new_text":               "92%",
		"table_id":               "tbl-budget",
		"row_index":              1,
		"cell_index":             1,
		"reader_session_id":      "session-1",
		"reader_context_summary": "命中预算表第二行第二列，当前单元格内容为 85%。",
		"edit_intent":            "把表格里的 85% 更新为 92%。",
		"context_scope":          "table",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordCopilotTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word copilot tool: %v", err)
	}
	if response.IsError {
		t.Fatalf("expected simple table replace to be coerced into update_table_cell, got error: %s", response.Content)
	}
	if !strings.Contains(response.Content, "\"action\":\"update_table_cell\"") {
		t.Fatalf("expected replace to be normalized into update_table_cell, got %s", response.Content)
	}
	if !strings.Contains(response.Content, "\"table_cell\":{\"paragraphs\":[{\"text\":\"92%\"") {
		t.Fatalf("expected coerced table update payload, got %s", response.Content)
	}
}

func TestWordCopilotToolDoesNotCoerceSentenceTableReplaceIntoCellOverwrite(t *testing.T) {
	input, err := json.Marshal(map[string]any{
		"action":                 "replace",
		"target_text":            "当前为灰度验证阶段。",
		"new_text":               "当前为灰度验证阶段，上线前需同步确认回滚预案。",
		"table_id":               "tbl-risk",
		"row_index":              2,
		"cell_index":             0,
		"reader_session_id":      "session-1",
		"reader_context_summary": "已锁定风险提示所在单元格，当前单元格内还有标题段“风险提示”，这里只想改正文句子。",
		"edit_intent":            "只扩写风险提示单元格内的正文，不改标题段。",
		"context_scope":          "table",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordCopilotTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word copilot tool: %v", err)
	}
	if !response.IsError {
		t.Fatalf("expected sentence-level table replace to remain rejected without explicit table_cell, got success: %s", response.Content)
	}
	if !strings.Contains(response.Content, "format_basis") {
		t.Fatalf("expected non-coerced sentence replace to continue requiring richer evidence, got %s", response.Content)
	}
}

func TestWordCopilotToolCoercesLegacyTableRowCellTextShape(t *testing.T) {
	input, err := json.Marshal(map[string]any{
		"action":                 "insert_table_row",
		"target_text":            "一、考核维度与权重",
		"table_id":               "tbl_203",
		"row_index":              4,
		"position":               "after",
		"table_row":              `{"cells":[{"text":"安全合规"},{"text":"5%"},{"text":"月度"},{"text":"安全规范执行、数据保密措施"}]}`,
		"reader_session_id":      "session-1",
		"reader_context_summary": "已锁定考核维度与权重表格（tbl_203），需要在最后一行后插入新行。",
		"format_basis":           "沿用现有表格正文样式，保持 4 列结构。",
		"exploration_summary":    "已通过表格 exploration 确认目标表为 tbl_203，当前最后一行位于 row_index=4，插入后仍保持 4 列。",
		"edit_intent":            "在考核维度与权重表格中新增安全合规一行。",
		"context_scope":          "table",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordCopilotTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word copilot tool: %v", err)
	}
	if response.IsError {
		t.Fatalf("expected successful response, got error: %s", response.Content)
	}
	if strings.Contains(response.Content, `"cells":[{"text":"安全合规"}`) {
		t.Fatalf("expected legacy cell text shape to be eliminated from signal, got %s", response.Content)
	}
	if !strings.Contains(response.Content, `"paragraphs":[{"text":"安全合规"`) {
		t.Fatalf("expected legacy cell text shape to be coerced into paragraphs, got %s", response.Content)
	}
	if !strings.Contains(response.Content, `"安全规范执行、数据保密措施"`) {
		t.Fatalf("expected inserted row content to survive signal encoding, got %s", response.Content)
	}
}

func TestWordCopilotToolRejectsStructurallyEmptyInsertTableRow(t *testing.T) {
	input, err := json.Marshal(map[string]any{
		"action":                 "insert_table_row",
		"target_text":            "一、考核维度与权重",
		"table_id":               "tbl_203",
		"row_index":              4,
		"position":               "after",
		"table_row":              map[string]any{"cells": []map[string]any{{}, {}, {}, {}}},
		"reader_session_id":      "session-1",
		"reader_context_summary": "已锁定考核维度与权重表格（tbl_203），需要在最后一行后插入新行。",
		"format_basis":           "沿用现有表格正文样式，保持 4 列结构。",
		"exploration_summary":    "已通过表格 exploration 确认目标表为 tbl_203，当前最后一行位于 row_index=4，插入后仍保持 4 列。",
		"edit_intent":            "在考核维度与权重表格中新增安全合规一行。",
		"context_scope":          "table",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordCopilotTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word copilot tool: %v", err)
	}
	if !response.IsError {
		t.Fatalf("expected empty table_row to fail, got success: %s", response.Content)
	}
	if !strings.Contains(response.Content, "table_row 的 cells 不能都是空壳") {
		t.Fatalf("expected structural empty table_row hint, got %s", response.Content)
	}
}

func TestWordCopilotToolRequiresHeaderFooterPayload(t *testing.T) {
	input, err := json.Marshal(map[string]any{
		"action":                 "update_header_footer",
		"target_text":            "Auramate 内部资料",
		"reader_session_id":      "session-1",
		"reader_context_summary": "已锁定默认页眉段落。",
		"format_basis":           "沿用 Header 样式。",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordCopilotTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word copilot tool: %v", err)
	}
	if !response.IsError {
		t.Fatalf("expected validation error, got success: %s", response.Content)
	}
	if !strings.Contains(response.Content, "header_footer") {
		t.Fatalf("expected header_footer hint, got %s", response.Content)
	}
}

func TestWordCopilotToolRequiresTableAnchorForTableScopedInsert(t *testing.T) {
	input, err := json.Marshal(map[string]any{
		"action":                 "insert",
		"target_text":            "当前为灰度验证阶段。",
		"new_text":               "上线前先确认灰度范围。",
		"reader_session_id":      "session-1",
		"context_scope":          "table",
		"reader_context_summary": "已锁定风险提示所在单元格，目标位于同一格正文段。",
		"format_basis":           "沿用同格 TableBody 正文样式。",
		"exploration_summary":    "已确认目标单元格位于 row_index=2, cell_index=0，同一行右侧为需人工复核。",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordCopilotTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word copilot tool: %v", err)
	}
	if !response.IsError {
		t.Fatalf("expected validation error, got success: %s", response.Content)
	}
	if !strings.Contains(response.Content, "table_id") {
		t.Fatalf("expected table anchor hint, got %s", response.Content)
	}
}

func TestWordCopilotToolRejectsInsertWithMultiParagraphNewText(t *testing.T) {
	input, err := json.Marshal(map[string]any{
		"action":                 "insert",
		"target_text":            "二、实施步骤",
		"new_text":               "三、质量门禁\n发布前完成回归验证。",
		"position":               "after",
		"preferred_paragraph_id": "para_8542",
		"reader_session_id":      "session-1",
		"reader_context_summary": "已定位到二级标题“二、实施步骤”，用户这次想新增标题和标题下正文。",
		"format_basis":           "已命中 Heading2 标题样式与后续正文样式；需要分别处理标题段和正文段。",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordCopilotTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word copilot tool: %v", err)
	}
	if !response.IsError {
		t.Fatalf("expected validation error, got success: %s", response.Content)
	}
	if !strings.Contains(response.Content, "new_text 不能直接携带换行或多段内容") {
		t.Fatalf("expected multi-paragraph insert hint, got %s", response.Content)
	}
}

func TestWordCopilotToolRejectsTableInsertWithTitleAndBodyInNewText(t *testing.T) {
	input, err := json.Marshal(map[string]any{
		"action":                 "insert",
		"target_text":            "当前为灰度验证阶段。",
		"new_text":               "补充说明\n上线前需同步确认回滚预案。",
		"position":               "after",
		"table_id":               "tbl_10110",
		"row_index":              2,
		"cell_index":             0,
		"reader_session_id":      "session-1",
		"context_scope":          "table",
		"reader_context_summary": "已锁定风险提示所在单元格，当前单元格内已有标题段“风险提示”和正文段“当前为灰度验证阶段。”。",
		"format_basis":           "该单元格第1段为 TableHeader 标题样式，第2段为 TableBody 正文样式。",
		"exploration_summary":    "已确认目标单元格位于 row_index=2, cell_index=0，同一行右侧为需人工复核。",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordCopilotTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word copilot tool: %v", err)
	}
	if !response.IsError {
		t.Fatalf("expected validation error, got success: %s", response.Content)
	}
	if !strings.Contains(response.Content, "update_table_cell") {
		t.Fatalf("expected update_table_cell hint, got %s", response.Content)
	}
}

func TestWordCopilotToolAllowsParagraphReplaceNearTableContext(t *testing.T) {
	input, err := json.Marshal(map[string]any{
		"action":                 "replace",
		"target_text":            "附录说明：本报告用于自动化回归。",
		"new_text":               "附录说明（归档备注）：本报告用于自动化回归。",
		"preferred_paragraph_id": "para_2724",
		"reader_session_id":      "session-1",
		"context_scope":          "paragraph",
		"reader_context_summary": "目标为文档末尾独立正文段落，仅需修改前缀标识，保持同一段落，不属于任何表格单元格。",
		"format_basis":           "沿用原有段落 Normal 样式，字体字号与段落间距不变。",
		"exploration_summary":    "目标位于文档末尾，紧邻 table_id=tbl_2700 的表格之后，但当前目标是纯文本段落，不是表格单元格。",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordCopilotTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word copilot tool: %v", err)
	}
	if response.IsError {
		t.Fatalf("expected successful response, got error: %s", response.Content)
	}
	if !strings.Contains(response.Content, "__WORD_EDITOR_SIG__:") {
		t.Fatalf("expected word editor signal, got %s", response.Content)
	}
	if !strings.Contains(response.Content, "\"action\":\"replace\"") {
		t.Fatalf("expected replace action in signal response, got %s", response.Content)
	}
}

func TestWordCopilotToolAllowsAnchoredParagraphReplaceWithoutFormatBasis(t *testing.T) {
	input, err := json.Marshal(map[string]any{
		"action":                 "replace",
		"target_text":            "项目总投资475万元",
		"new_text":               "项目总投资500万元",
		"preferred_paragraph_id": "para_59",
		"reader_session_id":      "session-1",
		"context_scope":          "paragraph",
		"reader_context_summary": "已锁定摘要正文段 para_59，不属于任何表格单元格，此处金额需要与第六章投资估算表总计保持一致。",
		"edit_intent":            "统一摘要中的投资金额，使其与第六章投资估算表总计保持一致。",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordCopilotTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word copilot tool: %v", err)
	}
	if response.IsError {
		t.Fatalf("expected anchored replace without format_basis to succeed, got error: %s", response.Content)
	}
	if !strings.Contains(response.Content, "__WORD_EDITOR_SIG__:") {
		t.Fatalf("expected word editor signal, got %s", response.Content)
	}
	if !strings.Contains(response.Content, "\"action\":\"replace\"") {
		t.Fatalf("expected replace action in signal response, got %s", response.Content)
	}
}

func TestWordCopilotToolAllowsAnchoredParagraphReplaceWithGenericFormatBasis(t *testing.T) {
	input, err := json.Marshal(map[string]any{
		"action":                 "replace",
		"target_text":            "项目总投资475万元",
		"new_text":               "项目总投资500万元",
		"preferred_paragraph_id": "para_59",
		"reader_session_id":      "session-1",
		"context_scope":          "paragraph",
		"reader_context_summary": "已锁定摘要正文段 para_59，不属于任何表格单元格，此处金额需要与第六章投资估算表总计保持一致。",
		"format_basis":           "保持原格式",
		"edit_intent":            "统一摘要中的投资金额，使其与第六章投资估算表总计保持一致。",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordCopilotTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word copilot tool: %v", err)
	}
	if response.IsError {
		t.Fatalf("expected anchored replace with generic format_basis to succeed, got error: %s", response.Content)
	}
	if !strings.Contains(response.Content, "__WORD_EDITOR_SIG__:") {
		t.Fatalf("expected word editor signal, got %s", response.Content)
	}
	if !strings.Contains(response.Content, "\"action\":\"replace\"") {
		t.Fatalf("expected replace action in signal response, got %s", response.Content)
	}
}

func TestWordCopilotToolStillRequiresFormatBasisForUnanchoredReplace(t *testing.T) {
	input, err := json.Marshal(map[string]any{
		"action":                 "replace",
		"target_text":            "项目总投资475万元",
		"new_text":               "项目总投资500万元",
		"reader_session_id":      "session-1",
		"context_scope":          "paragraph",
		"reader_context_summary": "已锁定摘要正文段，当前仅知道要修改摘要里的金额，但还没有精确段落锚点。",
		"edit_intent":            "统一摘要中的投资金额，使其与第六章投资估算表总计保持一致。",
	})
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}

	response, err := NewWordCopilotTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run word copilot tool: %v", err)
	}
	if !response.IsError {
		t.Fatalf("expected unanchored replace without format_basis to fail, got success: %s", response.Content)
	}
	if !strings.Contains(response.Content, "format_basis") {
		t.Fatalf("expected format_basis hint, got %s", response.Content)
	}
}

func testWordReaderTable() *goword.Table {
	return &goword.Table{
		ID: "tbl-budget",
		Rows: []goword.TableRow{
			{
				Cells: []goword.TableCell{
					{Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: &goword.Paragraph{ID: "p-h1", Style: goword.ParagraphStyle{StyleID: "Normal"}, Runs: []goword.Run{{ID: "r-h1", Text: "项目"}}}}}},
					{Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: &goword.Paragraph{ID: "p-h2", Style: goword.ParagraphStyle{StyleID: "Normal"}, Runs: []goword.Run{{ID: "r-h2", Text: "金额"}}}}}},
				},
			},
			{
				Cells: []goword.TableCell{
					{Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: &goword.Paragraph{ID: "p-r1c1", Style: goword.ParagraphStyle{StyleID: "Normal"}, Runs: []goword.Run{{ID: "r-r1c1", Text: "总计"}}}}}},
					{Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: &goword.Paragraph{ID: "p-r1c2", Style: goword.ParagraphStyle{StyleID: "Normal"}, Runs: []goword.Run{{ID: "r-r1c2", Text: "1000"}}}}}},
				},
			},
		},
	}
}
