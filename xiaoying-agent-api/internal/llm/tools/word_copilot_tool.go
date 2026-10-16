package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/auramate/auramate/internal/goword"
)

const WordCopilotToolName = "auramate-word-copilot"

var (
	wordCopilotSupportedActions = map[string]struct{}{
		"replace":                {},
		"insert":                 {},
		"insert_paragraph_break": {},
		"insert_line_break":      {},
		"insert_table":           {},
		"insert_table_row":       {},
		"delete_table_row":       {},
		"insert_table_column":    {},
		"delete_table_column":    {},
		"update_table_cell":      {},
		"delete":                 {},
		"scroll":                 {},
		"format":                 {},
		"format_paragraph":       {},
		"format_section":         {},
		"update_header_footer":   {},
	}
	wordCopilotBreakActions = map[string]struct{}{
		"insert_paragraph_break": {},
		"insert_line_break":      {},
	}
	wordCopilotTableRowActions = map[string]struct{}{
		"insert_table_row": {},
		"delete_table_row": {},
	}
	wordCopilotTableColumnActions = map[string]struct{}{
		"insert_table_column": {},
		"delete_table_column": {},
	}
	wordCopilotTableCellActions = map[string]struct{}{
		"update_table_cell": {},
	}
	wordCopilotRetrievalRequiredActions = map[string]struct{}{
		"replace":                {},
		"insert":                 {},
		"insert_paragraph_break": {},
		"insert_line_break":      {},
		"insert_table":           {},
		"insert_table_row":       {},
		"delete_table_row":       {},
		"insert_table_column":    {},
		"delete_table_column":    {},
		"update_table_cell":      {},
		"delete":                 {},
		"format":                 {},
		"format_paragraph":       {},
	}
	wordCopilotLiteralPipeCues              = []string{"竖线", "竖杠", "管道符", "pipe", "vertical bar"}
	wordCopilotPageAnchorCues               = []string{"本页", "上一页", "下一页", "前一页", "后一页"}
	wordCopilotGenericFormatBasisCues       = []string{"保持原格式", "保持原有格式", "保持一致", "沿用原格式", "沿用原有格式", "按原格式"}
	wordCopilotConcreteFormatBasisCues      = []string{"style_id", "style_catalog", "query_hits", "outline", "font", "bold", "italic", "字号", "字体", "对齐", "缩进", "段前", "段后", "normal", "heading", "title", "命中", "段落", "正文", "标题", "列表", "条目", "单元格", "所在行", "表头", "页", "page", "layout"}
	wordCopilotExpandedRewriteStructureCues = []string{"同一段", "同段", "段内", "保持单段", "不改换行", "不新增换行", "不另起段", "不新增段落", "保持当前段落结构", "保持原段落结构", "沿用本段", "按原段落节奏", "按现有换行规则", "段落边界", "换行判断", "断段判断", "同一单元格", "单元格内", "保持单元格内段落结构", "insert_line_break", "insert_paragraph_break"}
	wordCopilotRelativeAnchorCues           = []string{"中间", "最后", "末尾", "那条", "这条", "那段", "这段", "那格", "这格", "上面那条", "下面那条", "倒数"}
	wordCopilotTableValueCues               = []string{"通过率", "总价", "总计", "合计", "金额", "单价", "税额", "右侧结果", "右边结果", "右侧那格", "右边那个值", "左边那个值", "对应值", "这一行", "那一行", "这一列", "那一列", "最后一行", "最后一列"}
	wordCopilotBreakIntentCues              = []string{"换行", "换一行", "行内换行", "另起一段", "另起段落", "新起一段", "回车", "enter", "shift+enter", "段落断开", "拆成两段", "分成两段", "断成两段"}
	wordCopilotHeadingInsertCues            = []string{"同级标题", "新增标题", "插入标题", "章节标题", "小标题", "一级标题", "二级标题", "三级标题", "四级标题", "heading", "title", "style_id=heading", "style_id=title"}
	wordCopilotBodyInsertCues               = []string{"正文", "正文首段", "正文段落", "说明正文", "说明段落", "编号列表", "列表项", "条目", "body", "normal", "style_id=normal", "附加说明", "补一句"}
	wordCopilotPageAnchorPattern            = regexp.MustCompile(`第\s*[0-9一二三四五六七八九十百千万]+\s*页`)
	wordCopilotLinePrefixedPipePattern      = regexp.MustCompile(`(?m)^\s*\d+\|`)
	wordCopilotChapterPattern               = regexp.MustCompile(`^第[一二三四五六七八九十百千万0-9]+章`)
	wordCopilotAppendixPattern              = regexp.MustCompile(`^附录[一二三四五六七八九十百千万0-9A-Za-z]*`)
	wordCopilotNumberedPattern              = regexp.MustCompile(`^([一二三四五六七八九十百千万0-9]+)[、.．]\s*(.+)$`)
)

type wordCopilotTool struct{}

type wordCopilotSignalEnvelope struct {
	EventType string                   `json:"event_type"`
	Timestamp string                   `json:"timestamp,omitempty"`
	Data      wordCopilotActionPayload `json:"data"`
}

type wordCopilotActionPayload struct {
	Action               string                      `json:"action"`
	TargetText           string                      `json:"target_text"`
	PreferredParagraphID string                      `json:"preferred_paragraph_id,omitempty"`
	TableID              string                      `json:"table_id,omitempty"`
	RowIndex             *int                        `json:"row_index,omitempty"`
	CellIndex            *int                        `json:"cell_index,omitempty"`
	NewText              string                      `json:"new_text,omitempty"`
	Position             string                      `json:"position,omitempty"`
	EnableTrackChanges   bool                        `json:"enable_track_changes,omitempty"`
	EditIntent           string                      `json:"edit_intent,omitempty"`
	ContextScope         string                      `json:"context_scope,omitempty"`
	Constraints          *goword.ActionConstraints   `json:"constraints,omitempty"`
	FailurePolicy        *goword.ActionFailurePolicy `json:"failure_policy,omitempty"`
	PreviewOnly          bool                        `json:"preview_only,omitempty"`
	RetrievalQuery       string                      `json:"retrieval_query,omitempty"`
	RequireRetrieval     bool                        `json:"require_retrieval,omitempty"`
	ReaderSessionID      string                      `json:"reader_session_id,omitempty"`
	ReaderContextSummary string                      `json:"reader_context_summary,omitempty"`
	FormatBasis          string                      `json:"format_basis,omitempty"`
	ExplorationSummary   string                      `json:"exploration_summary,omitempty"`
	StylePatch           *goword.TextStyle           `json:"style_patch,omitempty"`
	ParagraphStylePatch  *goword.ParagraphStyle      `json:"paragraph_style_patch,omitempty"`
	TableSpec            *goword.TableInput          `json:"table_spec,omitempty"`
	TableRow             *goword.TableRowInput       `json:"table_row,omitempty"`
	TableColumn          *goword.TableColumnInput    `json:"table_column,omitempty"`
	TableCell            *goword.TableCellInput      `json:"table_cell,omitempty"`
	SectionLayoutPatch   *goword.SectionLayout       `json:"section_layout_patch,omitempty"`
	HeaderFooter         *goword.HeaderFooterInput   `json:"header_footer,omitempty"`
}

func NewWordCopilotTool() BaseTool {
	return &wordCopilotTool{}
}

func (t *wordCopilotTool) Info() ToolInfo {
	return ToolInfo{
		Name: WordCopilotToolName,
		Description: strings.Join([]string{
			"XiaoYing for Word 的精确编辑工具。它不会直接改写 docx XML，而是输出结构化 word_editor_action 信号给 go-word 内核与前端 Word 模块。",
			"推荐顺序是 read -> 按需 auramate-word-reader -> auramate-word-copilot。先用 read 获取全文角色、结构语境和模糊搜索结果；只有在仍需要真实 paragraph_id/table_id、当前 go-word 会话定位、相对或页级定位消歧、表格值单元格确认或样式证据时，再补 auramate-word-reader。",
			"对 replace、insert、delete、format_paragraph、update_table_cell 等结构化编辑动作，通常必须先补齐 reader_context_summary 与 format_basis，明确说明目标内容在全文中的角色，以及将沿用或目标化设置的样式、字体、字号和段落格式。",
			"对已显式锁定 table_id、row_index、cell_index 的 update_table_cell，如果只是整格单段改写，可以直接填写 new_text，bridge 会自动封装成 table_cell 并沿用现有单元格样式；但 target_text 仍然必填，必须写成当前单元格里真实存在的原文或原值。显式坐标下的简单表格值 replace 也会优先归一化为 update_table_cell。即使这种简单整格改写允许放宽 format_basis，也仍必须保留 reader_context_summary，说明目标格在全文中的角色，以及本次是整格改写还是同格插入/扩写。",
			"当用户使用‘中间/最后/那条/那段/那格’或‘第几页/上一页/下一页’这类相对或页级定位描述，或是在表格里改金额、总计、数量等值时，不能只靠 target_text 模糊编辑；必须带回 preferred_paragraph_id、table_id、row_index、cell_index，并在需要时填写 exploration_summary。",
			"如果本次写入依赖 auramate-word-reader 返回的精确锚点、表格坐标或 exploration 结果，必须同时回填 reader_session_id，确保读取、定位和写入发生在同一 go-word 工作态。",
			"当修改会把原句、原段或原单元格改长时，先判断是继续保持同一段或同一格，还是必须显式改成 insert_line_break、insert_paragraph_break 或多条最小动作；不要把结构变化硬塞进一条模糊 replace。",
			"insert 不要把换行、多段内容，或表格单元格里的‘标题段 + 正文段’直接塞进 new_text；需要多段时拆成多条最小动作，单元格内混合标题/正文请改用 update_table_cell 并显式填写 table_cell.paragraphs。",
			"当同一份 reader 证据已经足够锁定目标时，应立刻输出一条最小 word_editor_action；不要拿同一份 evidence 或同一套 payload 连续重复调用 auramate-word-copilot 试探，只有 checklist 里仍有未完成的独立编辑项时才继续下一条。",
			"update_header_footer 必须显式提供完整 header_footer 结构；表格单元格内的 insert/replace 若已经依赖单元格 exploration，也必须同时带回 table_id、row_index、cell_index，禁止只给 target_text 让执行层猜位置。",
		}, "\n\n"),
		Parameters: map[string]any{
			"action":                 map[string]any{"type": "string", "description": "执行的编辑操作类型。", "enum": []string{"replace", "insert", "insert_paragraph_break", "insert_line_break", "insert_table", "insert_table_row", "delete_table_row", "insert_table_column", "delete_table_column", "update_table_cell", "delete", "scroll", "format", "format_paragraph", "format_section", "update_header_footer"}},
			"target_text":            map[string]any{"type": "string", "description": "需要查找或修改的目标原文片段。"},
			"preferred_paragraph_id": map[string]any{"type": "string", "description": "可选：来自 auramate-word-reader query_hits 的 paragraph_id。"},
			"table_id":               map[string]any{"type": "string", "description": "表格动作可选：来自 auramate-word-reader query_hits 的 table_id。"},
			"row_index":              map[string]any{"type": "integer", "description": "表格行索引，0-based。对 insert_table_row 而言，默认表示现有锚点行；若要在最后一行后追加，可填写最后现有行索引并配合 position=after，也兼容直接填写末尾插入位置。"},
			"cell_index":             map[string]any{"type": "integer", "description": "表格列索引，0-based。对 insert_table_column 而言，默认表示现有锚点列；若要在最后一列后追加，可填写最后现有列索引并配合 position=after，也兼容直接填写末尾插入位置。"},
			"new_text":               map[string]any{"type": "string", "description": "需要替换成的新文本内容。若为删除操作，传空字符串。对已显式锁定 table_id/row_index/cell_index 的 update_table_cell，也可把 new_text 作为整格单段内容简写。"},
			"position":               map[string]any{"type": "string", "enum": []string{"before", "after"}, "description": "insert / insert_paragraph_break / insert_line_break 场景的锚点位置。"},
			"enable_track_changes":   map[string]any{"type": "boolean", "description": "是否开启修订模式，默认 true。"},
			"edit_intent":            map[string]any{"type": "string", "description": "编辑意图描述。"},
			"context_scope":          map[string]any{"type": "string", "enum": []string{"line", "paragraph", "table", "all"}, "description": "上下文理解范围。默认 paragraph。"},
			"constraints":            map[string]any{"type": []string{"string", "object"}, "description": "必须保留或遵守的约束。支持纯文本或结构化对象。"},
			"preview_only":           map[string]any{"type": "boolean", "description": "是否仅预览候选修改而不立即写入文档。"},
			"retrieval_query":        map[string]any{"type": "string", "description": "检索查询词。为空时默认回落到 target_text。"},
			"reader_session_id":      map[string]any{"type": "string", "description": "可选但强烈推荐：当前 go-word 活动会话 ID。"},
			"reader_context_summary": map[string]any{"type": "string", "description": "基于 read 和/或 auramate-word-reader 返回结果提炼的上下文摘要。"},
			"format_basis":           map[string]any{"type": "string", "description": "格式依据。需要精确样式或落点时优先基于 auramate-word-reader 的 style_catalog 和 query_hits/outline 提炼。"},
			"exploration_summary":    map[string]any{"type": "string", "description": "模糊定位或表格值定位场景必填的落点确认摘要。"},
			"style_patch":            map[string]any{"type": []string{"string", "object"}, "description": "仅 format 场景使用的样式 patch。"},
			"paragraph_style_patch":  map[string]any{"type": []string{"string", "object"}, "description": "insert/format_paragraph 场景使用的段落样式 patch。若是在表格单元格正文里新增整段内容，必须显式填写命中正文段的样式，例如 style_id=TableBody。"},
			"table_spec":             map[string]any{"type": []string{"string", "object"}, "description": "仅 insert_table 场景使用。"},
			"table_row":              map[string]any{"type": []string{"string", "object"}, "description": "仅 insert_table_row 场景使用。"},
			"table_column":           map[string]any{"type": []string{"string", "object"}, "description": "仅 insert_table_column 场景使用。"},
			"table_cell":             map[string]any{"type": []string{"string", "object"}, "description": "仅 update_table_cell 场景使用。通常应填写更新后的完整单元格内容；如果单元格内同时有标题段和正文段，需要把保留段与改写后的正文段一起写入。若目标格本身只有单段正文，也可直接用 new_text 简写。"},
			"section_layout_patch":   map[string]any{"type": []string{"string", "object"}, "description": "仅 format_section 场景使用，且为必填；必须显式提供结构化节布局 patch。页码设置示例：{\"page_number_start\":5,\"page_number_format\":\"upperRoman\"}；页面版式示例：{\"orientation\":\"landscape\",\"margin_left\":1200,\"margin_right\":1200}。"},
			"header_footer":          map[string]any{"type": []string{"string", "object"}, "description": "仅 update_header_footer 场景使用，且为必填；必须显式提供 kind/variant/blocks 的完整结构化内容。"},
			"failure_policy":         map[string]any{"type": []string{"string", "object"}, "description": "可选：失败策略。"},
		},
		Required: []string{"action", "target_text"},
	}
}

func (t *wordCopilotTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	params := map[string]any{}
	if strings.TrimSpace(call.Input) != "" {
		if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
			return NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %v", err)), nil
		}
	}
	action := wordCopilotNormalizeAction(wordCopilotStringParam(params, "action"))
	targetText := strings.TrimSpace(wordCopilotStringParam(params, "target_text"))
	tableID := strings.TrimSpace(wordCopilotStringParam(params, "table_id"))
	newText := wordCopilotStringParam(params, "new_text")
	rowIndex := wordCopilotOptionalIntParam(params, "row_index")
	cellIndex := wordCopilotOptionalIntParam(params, "cell_index")
	if action == "" && targetText == "" {
		return NewTextResponse("auramate-word-copilot 已就绪。请提供 action 与 target_text，并补齐必要的上下文证据后再生成结构化 Word 编辑指令。"), nil
	}
	if action == "" || (targetText == "" && !wordCopilotAllowsOmittedTargetText(action, tableID, rowIndex, cellIndex, newText, params["table_cell"])) {
		return NewTextErrorResponse("action 和 target_text 为必填字段"), nil
	}
	position := strings.ToLower(strings.TrimSpace(wordCopilotStringParam(params, "position")))
	if position == "" {
		position = "after"
	}
	preferredParagraphID := strings.TrimSpace(wordCopilotStringParam(params, "preferred_paragraph_id"))
	enableTrackChanges := wordCopilotBoolParam(params, "enable_track_changes", true)
	editIntent := strings.TrimSpace(wordCopilotStringParam(params, "edit_intent"))
	contextScope := wordCopilotNormalizeContextScope(wordCopilotStringParam(params, "context_scope"))
	previewOnly := wordCopilotBoolParam(params, "preview_only", false)
	retrievalQuery := strings.TrimSpace(wordCopilotStringParam(params, "retrieval_query"))
	readerSessionID := strings.TrimSpace(wordCopilotStringParam(params, "reader_session_id"))
	readerContextSummary := strings.TrimSpace(wordCopilotStringParam(params, "reader_context_summary"))
	formatBasis := strings.TrimSpace(wordCopilotStringParam(params, "format_basis"))
	explorationSummary := strings.TrimSpace(wordCopilotStringParam(params, "exploration_summary"))

	stylePatch, err := wordCopilotDecodeObjectField[goword.TextStyle](params["style_patch"], "style_patch", `{"font_size_hp":28,"bold":true,"bold_set":true}`)
	if err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}
	paragraphStylePatch, err := wordCopilotDecodeObjectField[goword.ParagraphStyle](params["paragraph_style_patch"], "paragraph_style_patch", `{"style_id":"Heading2","alignment":"center"}`)
	if err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}
	tableSpec, err := wordCopilotDecodeTableSpecField(params["table_spec"], "table_spec", `{"rows":[{"cells":[{"paragraphs":[{"text":"项目"}]},{"paragraphs":[{"text":"金额"}]}]}]}`)
	if err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}
	tableRow, err := wordCopilotDecodeTableRowField(params["table_row"], "table_row", `{"cells":[{"paragraphs":[{"text":"阶段"}]},{"paragraphs":[{"text":"状态"}]}]}`)
	if err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}
	tableColumn, err := wordCopilotDecodeTableColumnField(params["table_column"], "table_column", `{"cells":[{"paragraphs":[{"text":"负责人"}]},{"paragraphs":[{"text":"张三"}]}]}`)
	if err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}
	tableCell, err := wordCopilotDecodeTableCellField(params["table_cell"], "table_cell", `{"paragraphs":[{"text":"新的单元格内容"}]}`)
	if err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}
	sectionLayoutPatch, err := wordCopilotDecodeObjectField[goword.SectionLayout](params["section_layout_patch"], "section_layout_patch", `{"orientation":"landscape","margin_left":1200,"margin_right":1200}`)
	if err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}
	headerFooter, err := wordCopilotDecodeObjectField[goword.HeaderFooterInput](params["header_footer"], "header_footer", `{"kind":"header","variant":"default","blocks":[{"type":"paragraph","paragraph":{"text":"XiaoYing 内部资料"}}]}`)
	if err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}
	constraints, err := wordCopilotDecodeConstraints(params["constraints"])
	if err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}
	failurePolicy, err := wordCopilotDecodeObjectField[goword.ActionFailurePolicy](params["failure_policy"], "failure_policy", `{"on_ambiguous":"return_candidates","on_validation_failed":"rollback_and_report"}`)
	if err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}

	if tableSpec != nil && len(tableSpec.Rows) == 0 {
		return NewTextErrorResponse("指令生成失败：table_spec 必须包含至少一行 rows"), nil
	}
	if tableRow != nil && len(tableRow.Cells) == 0 {
		return NewTextErrorResponse("指令生成失败：table_row 必须包含至少一个 cells 项"), nil
	}
	if action == "insert_table_row" && tableRow != nil && wordCopilotTableRowStructurallyEmpty(tableRow) {
		return NewTextErrorResponse("指令生成失败：table_row 的 cells 不能都是空壳；请为每个单元格提供 paragraphs，或使用兼容格式 {\"cells\":[{\"text\":\"...\"}]}。"), nil
	}
	if tableColumn != nil && tableColumn.Cells == nil {
		return NewTextErrorResponse("指令生成失败：table_column 必须包含 cells 数组"), nil
	}
	if headerFooter != nil {
		headerFooter.Kind = strings.ToLower(strings.TrimSpace(headerFooter.Kind))
		headerFooter.Variant = strings.ToLower(strings.TrimSpace(headerFooter.Variant))
		if headerFooter.Kind != "header" && headerFooter.Kind != "footer" {
			return NewTextErrorResponse("指令生成失败：header_footer.kind 必须是 header 或 footer"), nil
		}
		if headerFooter.Variant == "" {
			headerFooter.Variant = "default"
		}
		if headerFooter.Variant != "default" && headerFooter.Variant != "first" && headerFooter.Variant != "even" {
			return NewTextErrorResponse("指令生成失败：header_footer.variant 必须是 default / first / even"), nil
		}
		if len(headerFooter.Blocks) == 0 {
			return NewTextErrorResponse("指令生成失败：header_footer 必须包含至少一个 blocks 项"), nil
		}
	}

	constraintsText := wordCopilotConstraintsText(constraints)
	action, newText, tableCell = wordCopilotNormalizeTableCellAction(action, preferredParagraphID, tableID, rowIndex, cellIndex, contextScope, targetText, newText, editIntent, constraintsText, readerContextSummary, formatBasis, explorationSummary, tableCell)
	if err := wordCopilotValidateActionContract(action, targetText, newText, position, contextScope, tableID, editIntent, constraintsText, readerContextSummary, formatBasis, paragraphStylePatch); err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}
	if err := wordCopilotValidateReaderEvidence(action, targetText, newText, preferredParagraphID, tableID, rowIndex, cellIndex, contextScope, readerContextSummary, formatBasis, explorationSummary); err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}
	if err := wordCopilotValidateTableActionPayload(action, tableID, rowIndex, cellIndex, tableRow, tableColumn, tableCell); err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}
	if err := wordCopilotValidateTableCellContent(action, targetText, tableCell); err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}
	if err := wordCopilotValidateTableCellAnchor(action, tableID, rowIndex, cellIndex, contextScope, readerContextSummary, formatBasis, explorationSummary); err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}
	if err := wordCopilotValidateHeaderFooterPayload(action, headerFooter); err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}
	if err := wordCopilotValidateSectionLayoutPayload(action, sectionLayoutPatch); err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}
	if err := wordCopilotValidateTableValueActionContract(action, targetText, preferredParagraphID, tableID, rowIndex, cellIndex, contextScope, editIntent, constraintsText, readerContextSummary, formatBasis, explorationSummary); err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}
	if err := wordCopilotValidatePreciseAnchor(action, preferredParagraphID, tableID, editIntent, constraintsText, readerContextSummary); err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}
	if err := wordCopilotValidateExplorationEvidence(action, preferredParagraphID, tableID, contextScope, editIntent, constraintsText, readerContextSummary, explorationSummary); err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}
	if err := wordCopilotValidateExpandedRewriteEvidence(action, targetText, newText, editIntent, constraintsText, readerContextSummary, formatBasis); err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}
	if err := wordCopilotValidateReaderSessionAlignment(action, readerSessionID, preferredParagraphID, tableID, explorationSummary, rowIndex, cellIndex); err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}
	if err := wordCopilotValidateTextPayloadContract(action, editIntent, constraintsText, readerContextSummary, formatBasis, newText, stylePatch, paragraphStylePatch, tableSpec, tableRow, tableColumn, tableCell, headerFooter); err != nil {
		return NewTextErrorResponse("指令生成失败：" + err.Error()), nil
	}

	payload := wordCopilotActionPayload{
		Action:               action,
		TargetText:           targetText,
		PreferredParagraphID: preferredParagraphID,
		TableID:              tableID,
		RowIndex:             rowIndex,
		CellIndex:            cellIndex,
		NewText:              newText,
		Position:             position,
		EnableTrackChanges:   enableTrackChanges,
		EditIntent:           wordCopilotDefaultIfEmpty(editIntent, "精准修改"),
		ContextScope:         contextScope,
		Constraints:          constraints,
		FailurePolicy:        failurePolicy,
		PreviewOnly:          previewOnly,
		RetrievalQuery:       wordCopilotDefaultIfEmpty(retrievalQuery, targetText),
		RequireRetrieval:     wordCopilotRequiresRetrieval(action),
		ReaderSessionID:      readerSessionID,
		ReaderContextSummary: readerContextSummary,
		FormatBasis:          formatBasis,
		ExplorationSummary:   explorationSummary,
		StylePatch:           stylePatch,
		ParagraphStylePatch:  paragraphStylePatch,
		TableSpec:            tableSpec,
		TableRow:             tableRow,
		TableColumn:          tableColumn,
		TableCell:            tableCell,
		SectionLayoutPatch:   sectionLayoutPatch,
		HeaderFooter:         headerFooter,
	}
	envelope := wordCopilotSignalEnvelope{EventType: "word_editor_action", Timestamp: "auto", Data: payload}
	bytes, err := json.Marshal(envelope)
	if err != nil {
		return NewTextErrorResponse(err.Error()), nil
	}
	message := fmt.Sprintf("__WORD_EDITOR_SIG__:%s\n\n[已成功向 go-word 发送 %s 指令。系统将定位到 '%s' 并展示变更 diff。]", string(bytes), action, wordCopilotPreviewTarget(targetText, 10))
	return NewTextResponse(message), nil
}

func wordCopilotStringParam(params map[string]any, key string) string {
	value, ok := params[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	default:
		return ""
	}
}

func wordCopilotOptionalIntParam(params map[string]any, key string) *int {
	value, ok := params[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case float64:
		result := int(typed)
		return &result
	case int:
		result := typed
		return &result
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return nil
		}
		result := int(parsed)
		return &result
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return nil
		}
		var parsed int
		if _, err := fmt.Sscanf(trimmed, "%d", &parsed); err != nil {
			return nil
		}
		return &parsed
	default:
		return nil
	}
}

func wordCopilotBoolParam(params map[string]any, key string, defaultValue bool) bool {
	value, ok := params[key]
	if !ok || value == nil {
		return defaultValue
	}
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		trimmed := strings.ToLower(strings.TrimSpace(typed))
		return trimmed == "1" || trimmed == "true" || trimmed == "yes" || trimmed == "on"
	default:
		return defaultValue
	}
}

func wordCopilotDecodeConstraints(value any) (*goword.ActionConstraints, error) {
	if value == nil {
		return nil, nil
	}
	if raw, ok := value.(string); ok {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return nil, nil
		}
		return &goword.ActionConstraints{RawText: trimmed}, nil
	}
	decoded, err := wordCopilotDecodeObjectField[goword.ActionConstraints](value, "constraints", `{"raw_text":"不要继承标题样式","preserve_style":false}`)
	if err != nil {
		return nil, err
	}
	return decoded, nil
}

func wordCopilotDecodeTableSpecField(value any, fieldName, example string) (*goword.TableInput, error) {
	normalized, passthrough := wordCopilotNormalizeLegacyTablePayload(value, true, false)
	if passthrough {
		return wordCopilotDecodeObjectField[goword.TableInput](value, fieldName, example)
	}
	return wordCopilotDecodeObjectField[goword.TableInput](normalized, fieldName, example)
}

func wordCopilotDecodeTableRowField(value any, fieldName, example string) (*goword.TableRowInput, error) {
	normalized, passthrough := wordCopilotNormalizeLegacyTablePayload(value, false, false)
	if passthrough {
		return wordCopilotDecodeObjectField[goword.TableRowInput](value, fieldName, example)
	}
	return wordCopilotDecodeObjectField[goword.TableRowInput](normalized, fieldName, example)
}

func wordCopilotDecodeTableColumnField(value any, fieldName, example string) (*goword.TableColumnInput, error) {
	normalized, passthrough := wordCopilotNormalizeLegacyTablePayload(value, false, false)
	if passthrough {
		return wordCopilotDecodeObjectField[goword.TableColumnInput](value, fieldName, example)
	}
	return wordCopilotDecodeObjectField[goword.TableColumnInput](normalized, fieldName, example)
}

func wordCopilotDecodeTableCellField(value any, fieldName, example string) (*goword.TableCellInput, error) {
	normalized, passthrough := wordCopilotNormalizeLegacyTablePayload(value, false, true)
	if passthrough {
		return wordCopilotDecodeObjectField[goword.TableCellInput](value, fieldName, example)
	}
	return wordCopilotDecodeObjectField[goword.TableCellInput](normalized, fieldName, example)
}

func wordCopilotNormalizeLegacyTablePayload(value any, expectRows bool, expectCell bool) (any, bool) {
	if value == nil {
		return nil, true
	}
	parsed := value
	if raw, ok := value.(string); ok {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return nil, true
		}
		if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
			return value, true
		}
	}
	normalized, ok := parsed.(map[string]any)
	if !ok {
		return value, true
	}
	if expectCell {
		return wordCopilotNormalizeLegacyTableCellMap(normalized), false
	}
	if expectRows {
		if rows, ok := normalized["rows"].([]any); ok {
			for index, rowValue := range rows {
				rowMap, ok := rowValue.(map[string]any)
				if !ok {
					continue
				}
				rowMap["cells"] = wordCopilotNormalizeLegacyTableCells(rowMap["cells"])
				rows[index] = rowMap
			}
			normalized["rows"] = rows
		}
		return normalized, false
	}
	normalized["cells"] = wordCopilotNormalizeLegacyTableCells(normalized["cells"])
	return normalized, false
}

func wordCopilotNormalizeLegacyTableCells(value any) any {
	cells, ok := value.([]any)
	if !ok {
		return value
	}
	for index, cellValue := range cells {
		cellMap, ok := cellValue.(map[string]any)
		if !ok {
			continue
		}
		cells[index] = wordCopilotNormalizeLegacyTableCellMap(cellMap)
	}
	return cells
}

func wordCopilotNormalizeLegacyTableCellMap(cell map[string]any) map[string]any {
	if cell == nil {
		return nil
	}
	if _, ok := cell["paragraphs"]; ok {
		return cell
	}
	rawText, ok := cell["text"]
	if !ok {
		return cell
	}
	text, ok := rawText.(string)
	if !ok || strings.TrimSpace(text) == "" {
		return cell
	}
	cell["paragraphs"] = []map[string]any{{"text": text}}
	return cell
}

func wordCopilotDecodeObjectField[T any](value any, fieldName, example string) (*T, error) {
	if value == nil {
		return nil, nil
	}
	var bytes []byte
	switch typed := value.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed == "" {
			return nil, nil
		}
		bytes = []byte(trimmed)
	default:
		marshaled, err := json.Marshal(typed)
		if err != nil {
			return nil, fmt.Errorf("%s 必须是合法 JSON：%v", fieldName, err)
		}
		bytes = marshaled
	}
	var decoded T
	if err := json.Unmarshal(bytes, &decoded); err != nil {
		return nil, fmt.Errorf("%s 必须是合法 JSON：%v", fieldName, err)
	}
	return &decoded, nil
}

func wordCopilotTableRowStructurallyEmpty(row *goword.TableRowInput) bool {
	if row == nil || len(row.Cells) == 0 {
		return true
	}
	for _, cell := range row.Cells {
		if strings.TrimSpace(cell.PropertiesXML) != "" {
			return false
		}
		for _, paragraph := range cell.Paragraphs {
			if strings.TrimSpace(paragraph.Text) != "" {
				return false
			}
			for _, run := range paragraph.Runs {
				if strings.TrimSpace(run.Text) != "" {
					return false
				}
			}
		}
	}
	return true
}

func wordCopilotNormalizeAction(action string) string {
	return strings.ToLower(strings.TrimSpace(action))
}

func wordCopilotNormalizeContextScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "line", "paragraph", "table", "all":
		return strings.ToLower(strings.TrimSpace(scope))
	default:
		return "paragraph"
	}
}

func wordCopilotAllowsOmittedTargetText(action, tableID string, rowIndex, cellIndex *int, newText string, rawTableCell any) bool {
	if wordCopilotNormalizeAction(action) != "update_table_cell" {
		return false
	}
	if strings.TrimSpace(tableID) == "" || rowIndex == nil || cellIndex == nil {
		return false
	}
	if wordCopilotNormalizeText(newText) != "" {
		return true
	}
	return rawTableCell != nil
}

func wordCopilotRequiresRetrieval(action string) bool {
	_, ok := wordCopilotRetrievalRequiredActions[wordCopilotNormalizeAction(action)]
	return ok
}

func wordCopilotNormalizeText(value string) string {
	return strings.TrimSpace(value)
}

func wordCopilotContainsLineBreak(text string) bool {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	return strings.Contains(normalized, "\n")
}

func wordCopilotHasLiteralPipeCue(parts ...string) bool {
	merged := strings.ToLower(strings.Join(wordCopilotNonEmptyParts(parts...), " "))
	for _, cue := range wordCopilotLiteralPipeCues {
		if strings.Contains(merged, cue) {
			return true
		}
	}
	return false
}

func wordCopilotContainsSuspiciousPipeMarker(text string) bool {
	normalized := wordCopilotNormalizeText(text)
	if normalized == "" {
		return false
	}
	if wordCopilotLinePrefixedPipePattern.MatchString(normalized) {
		return true
	}
	return strings.Contains(normalized, "|")
}

func wordCopilotHasRelativeAnchorCue(parts ...string) bool {
	merged := strings.Join(wordCopilotNonEmptyParts(parts...), " ")
	for _, cue := range wordCopilotRelativeAnchorCues {
		if strings.Contains(merged, cue) {
			return true
		}
	}
	return false
}

func wordCopilotHasPageAnchorCue(parts ...string) bool {
	merged := strings.Join(wordCopilotNonEmptyParts(parts...), " ")
	for _, cue := range wordCopilotPageAnchorCues {
		if strings.Contains(merged, cue) {
			return true
		}
	}
	return wordCopilotPageAnchorPattern.MatchString(merged)
}

func wordCopilotHasTableValueCue(parts ...string) bool {
	merged := strings.Join(wordCopilotNonEmptyParts(parts...), " ")
	for _, cue := range wordCopilotTableValueCues {
		if strings.Contains(merged, cue) {
			return true
		}
	}
	return false
}

func wordCopilotHasBreakIntentCue(parts ...string) bool {
	merged := strings.Join(wordCopilotNonEmptyParts(parts...), " ")
	for _, cue := range wordCopilotBreakIntentCues {
		if strings.Contains(merged, cue) {
			return true
		}
	}
	return false
}

func wordCopilotIsExpandedRewrite(targetText, newText string) bool {
	original := wordCopilotNormalizeText(targetText)
	updated := wordCopilotNormalizeText(newText)
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

func wordCopilotHasExpandedRewriteStructureEvidence(parts ...string) bool {
	merged := strings.Join(wordCopilotNonEmptyParts(parts...), " ")
	for _, cue := range wordCopilotExpandedRewriteStructureCues {
		if strings.Contains(merged, cue) {
			return true
		}
	}
	return false
}

func wordCopilotTextLooksLikeHeading(text string) bool {
	trimmed := wordCopilotNormalizeText(text)
	if trimmed == "" {
		return false
	}
	if wordCopilotChapterPattern.MatchString(trimmed) || wordCopilotAppendixPattern.MatchString(trimmed) {
		return true
	}
	matches := wordCopilotNumberedPattern.FindStringSubmatch(trimmed)
	if len(matches) != 3 {
		return false
	}
	content := strings.TrimSpace(matches[2])
	if content == "" {
		return true
	}
	if strings.ContainsAny(content, "：:，,。；;！？!?") {
		return false
	}
	return len([]rune(content)) <= 20
}

func wordCopilotHasHeadingInsertCue(parts ...string) bool {
	merged := strings.Join(wordCopilotNonEmptyParts(parts...), " ")
	for _, cue := range wordCopilotHeadingInsertCues {
		if strings.Contains(merged, cue) {
			return true
		}
	}
	return false
}

func wordCopilotHasBodyInsertCue(parts ...string) bool {
	merged := strings.Join(wordCopilotNonEmptyParts(parts...), " ")
	for _, cue := range wordCopilotBodyInsertCues {
		if strings.Contains(merged, cue) {
			return true
		}
	}
	return false
}

func wordCopilotRequiresTableBodyParagraphStylePatch(action string, parts ...string) bool {
	if wordCopilotNormalizeAction(action) != "insert" {
		return false
	}
	if !wordCopilotHasTableValueCue(parts...) {
		return false
	}
	return wordCopilotHasBodyInsertCue(parts...)
}

func wordCopilotMentionsTableBody(parts ...string) bool {
	merged := strings.ToLower(strings.Join(wordCopilotNonEmptyParts(parts...), " "))
	return strings.Contains(merged, "tablebody") || strings.Contains(merged, "style_id=tablebody")
}

func wordCopilotNormalizeStyleID(styleID string) string {
	return strings.ToLower(strings.TrimSpace(styleID))
}

func wordCopilotIsHeadingStyleID(styleID string) bool {
	normalized := wordCopilotNormalizeStyleID(styleID)
	return normalized == "title" || strings.HasPrefix(normalized, "heading")
}

func wordCopilotRequiresHeadingParagraphStylePatch(newText, editIntent, constraints, readerContextSummary, formatBasis string) bool {
	if wordCopilotHasBodyInsertCue(editIntent, constraints, readerContextSummary, formatBasis) {
		return false
	}
	return wordCopilotHasHeadingInsertCue(newText, editIntent, constraints, readerContextSummary, formatBasis) || wordCopilotTextLooksLikeHeading(newText)
}

func wordCopilotFormatBasisIsTooGeneric(formatBasis string) bool {
	normalized := wordCopilotNormalizeText(formatBasis)
	if normalized == "" {
		return false
	}
	hasGenericCue := false
	for _, cue := range wordCopilotGenericFormatBasisCues {
		if strings.Contains(normalized, cue) {
			hasGenericCue = true
			break
		}
	}
	if !hasGenericCue {
		return false
	}
	lowered := strings.ToLower(normalized)
	for _, cue := range wordCopilotConcreteFormatBasisCues {
		if strings.Contains(lowered, cue) {
			return false
		}
	}
	return true
}

func wordCopilotBuildPlainTextTableCellInput(newText string) *goword.TableCellInput {
	trimmed := wordCopilotNormalizeText(newText)
	if trimmed == "" || wordCopilotContainsLineBreak(trimmed) {
		return nil
	}
	return &goword.TableCellInput{Paragraphs: []goword.ParagraphInput{{Text: trimmed}}}
}

func wordCopilotLooksLikeWholeCellPlainText(text string) bool {
	normalized := wordCopilotNormalizeText(text)
	if normalized == "" || wordCopilotContainsLineBreak(normalized) {
		return false
	}
	if len([]rune(normalized)) > 40 {
		return false
	}
	if strings.ContainsAny(normalized, "。！？；\n") {
		return false
	}
	return true
}

func wordCopilotLooksLikeSimpleTableValueText(text string) bool {
	normalized := wordCopilotNormalizeText(text)
	if normalized == "" || wordCopilotContainsLineBreak(normalized) {
		return false
	}
	if len([]rune(normalized)) > 16 {
		return false
	}
	if strings.ContainsAny(normalized, "。！？；,，") {
		return false
	}
	return strings.ContainsAny(normalized, "0123456789%％￥$元万亿")
}

func wordCopilotHasExplicitTableContext(contextScope, tableID string, rowIndex, cellIndex *int, values ...string) bool {
	if wordCopilotNormalizeContextScope(contextScope) == "table" {
		return true
	}
	if strings.TrimSpace(tableID) != "" || rowIndex != nil || cellIndex != nil {
		return true
	}
	return wordCopilotMentionsTableAnchor(values...)
}

func wordCopilotCanCoerceSimpleTableReplaceToCellUpdate(action, preferredParagraphID, tableID string, rowIndex, cellIndex *int, contextScope, targetText, newText, editIntent, constraints, readerContextSummary, formatBasis, explorationSummary string, tableCell *goword.TableCellInput) bool {
	if wordCopilotNormalizeAction(action) != "replace" {
		return false
	}
	if tableCell != nil || strings.TrimSpace(preferredParagraphID) != "" {
		return false
	}
	if strings.TrimSpace(tableID) == "" || rowIndex == nil || cellIndex == nil {
		return false
	}
	if !wordCopilotHasExplicitTableContext(contextScope, tableID, rowIndex, cellIndex, readerContextSummary, formatBasis, explorationSummary) {
		return false
	}
	if !wordCopilotLooksLikeWholeCellPlainText(targetText) || !wordCopilotLooksLikeWholeCellPlainText(newText) {
		return false
	}
	if !wordCopilotHasTableValueCue(targetText, editIntent, constraints, readerContextSummary) &&
		!(wordCopilotLooksLikeSimpleTableValueText(targetText) && wordCopilotLooksLikeSimpleTableValueText(newText)) {
		return false
	}
	return true
}

func wordCopilotNormalizeTableCellAction(action, preferredParagraphID, tableID string, rowIndex, cellIndex *int, contextScope, targetText, newText, editIntent, constraints, readerContextSummary, formatBasis, explorationSummary string, tableCell *goword.TableCellInput) (string, string, *goword.TableCellInput) {
	normalizedAction := wordCopilotNormalizeAction(action)
	if normalizedAction == "update_table_cell" && tableCell == nil {
		if shorthand := wordCopilotBuildPlainTextTableCellInput(newText); shorthand != nil {
			return normalizedAction, "", shorthand
		}
	}
	if wordCopilotCanCoerceSimpleTableReplaceToCellUpdate(normalizedAction, preferredParagraphID, tableID, rowIndex, cellIndex, contextScope, targetText, newText, editIntent, constraints, readerContextSummary, formatBasis, explorationSummary, tableCell) {
		if shorthand := wordCopilotBuildPlainTextTableCellInput(newText); shorthand != nil {
			return "update_table_cell", "", shorthand
		}
	}
	return normalizedAction, newText, tableCell
}

func wordCopilotAllowsRelaxedFormatBasisForAnchoredReplace(action, targetText, newText, preferredParagraphID, tableID string, rowIndex, cellIndex *int, contextScope, readerContextSummary, formatBasis, explorationSummary string) bool {
	if wordCopilotNormalizeAction(action) != "replace" {
		return false
	}
	if strings.TrimSpace(preferredParagraphID) == "" {
		return false
	}
	if wordCopilotHasExplicitTableContext(contextScope, tableID, rowIndex, cellIndex, readerContextSummary, formatBasis, explorationSummary) {
		return false
	}
	return !wordCopilotIsExpandedRewrite(targetText, newText)
}

func wordCopilotAllowsRelaxedFormatBasisForExplicitTableCellUpdate(action, tableID string, rowIndex, cellIndex *int, contextScope, readerContextSummary, explorationSummary string) bool {
	if wordCopilotNormalizeAction(action) != "update_table_cell" {
		return false
	}
	if strings.TrimSpace(tableID) == "" || rowIndex == nil || cellIndex == nil {
		return false
	}
	return wordCopilotHasExplicitTableContext(contextScope, tableID, rowIndex, cellIndex, readerContextSummary, explorationSummary)
}

func wordCopilotValidateReaderEvidence(action, targetText, newText, preferredParagraphID, tableID string, rowIndex, cellIndex *int, contextScope, readerContextSummary, formatBasis, explorationSummary string) error {
	if !wordCopilotRequiresRetrieval(action) {
		return nil
	}
	if len([]rune(wordCopilotNormalizeText(readerContextSummary))) < 8 {
		return fmt.Errorf("replace/insert/insert_paragraph_break/insert_line_break/delete/insert_table/insert_table_row/delete_table_row/insert_table_column/delete_table_column/update_table_cell/format_paragraph 前必须先补齐 reader_context_summary。应先用 read 获取全文角色与修改语境；若需要精确落点，再结合 auramate-word-reader 结果说明目标内容在全文中的位置和角色。")
	}
	allowRelaxedFormatBasis := wordCopilotAllowsRelaxedFormatBasisForAnchoredReplace(action, targetText, newText, preferredParagraphID, tableID, rowIndex, cellIndex, contextScope, readerContextSummary, formatBasis, explorationSummary) ||
		wordCopilotAllowsRelaxedFormatBasisForExplicitTableCellUpdate(action, tableID, rowIndex, cellIndex, contextScope, readerContextSummary, explorationSummary)
	if len([]rune(wordCopilotNormalizeText(formatBasis))) < 8 {
		if allowRelaxedFormatBasis {
			return nil
		}
		return fmt.Errorf("replace/insert/insert_paragraph_break/insert_line_break/delete/insert_table/insert_table_row/delete_table_row/insert_table_column/delete_table_column/update_table_cell/format_paragraph 前必须先补齐 format_basis。若需要真实样式、字体、字号或精确落点依据，应使用 auramate-word-reader 的 style_catalog、query_hits、outline 或 exploration 结果。")
	}
	if wordCopilotFormatBasisIsTooGeneric(formatBasis) && !allowRelaxedFormatBasis {
		return fmt.Errorf("format_basis 不能只写‘保持原格式/保持一致’这类空泛描述；至少要明确参考对象或样式证据，例如命中段落的 style_id、字体字号、对齐缩进，或命中单元格/页级 layout 依据。")
	}
	return nil
}

func wordCopilotValidateExpandedRewriteEvidence(action, targetText, newText, editIntent, constraints, readerContextSummary, formatBasis string) error {
	if wordCopilotNormalizeAction(action) != "replace" {
		return nil
	}
	if !wordCopilotIsExpandedRewrite(targetText, newText) {
		return nil
	}
	if wordCopilotHasExpandedRewriteStructureEvidence(editIntent, constraints, readerContextSummary, formatBasis) {
		return nil
	}
	return fmt.Errorf("replace 在把内容改长时，必须先精读目标附近上下文，并在 reader_context_summary 或 format_basis 中明确说明保持同段还是需要换行/断段，以及参考了哪些相邻段落、句子节奏或单元格结构；不能只给样式名后直接扩写。")
}

func wordCopilotValidateActionContract(action, targetText, newText, position, contextScope, tableID, editIntent, constraints, readerContextSummary, formatBasis string, paragraphStylePatch *goword.ParagraphStyle) error {
	if _, ok := wordCopilotSupportedActions[action]; !ok {
		return fmt.Errorf("不支持的 action: %s", action)
	}
	if (action == "insert" || action == "insert_paragraph_break" || action == "insert_line_break" || action == "insert_table" || action == "insert_table_row" || action == "insert_table_column") && position != "before" && position != "after" {
		return fmt.Errorf("position 只能是 before 或 after")
	}
	if _, ok := wordCopilotBreakActions[action]; ok && wordCopilotNormalizeText(newText) != "" {
		return fmt.Errorf("insert_paragraph_break/insert_line_break 只负责插入断开，不能携带 new_text；如需新增整段内容，请改用 insert。")
	}
	if action == "insert" && wordCopilotHasBreakIntentCue(editIntent, constraints, readerContextSummary) {
		return fmt.Errorf("当前意图是在现有段落内插入回车或换行，请改用 insert_paragraph_break 或 insert_line_break；insert 只用于在锚点前后新增整段内容。")
	}
	if action == "insert" && wordCopilotContainsLineBreak(newText) {
		if wordCopilotNormalizeContextScope(contextScope) == "table" || strings.TrimSpace(tableID) != "" || wordCopilotMentionsTableAnchor(readerContextSummary, formatBasis) {
			return fmt.Errorf("表格单元格 insert 不能把标题段、正文段或多段内容直接塞进 new_text；如需在同一格内同时新增标题和内容，请改用 update_table_cell，并在 table_cell.paragraphs 中显式写出各段。若只是断行，请改用 insert_paragraph_break 或 insert_line_break。")
		}
		return fmt.Errorf("insert 的 new_text 不能直接携带换行或多段内容；如需断行请改用 insert_paragraph_break / insert_line_break，如需新增多段请拆成多条最小动作。")
	}
	if action == "insert" && wordCopilotRequiresHeadingParagraphStylePatch(newText, editIntent, constraints, readerContextSummary, formatBasis) {
		if paragraphStylePatch == nil {
			return fmt.Errorf("当前插入内容属于标题场景，必须显式提供 paragraph_style_patch，至少包含 style_id=HeadingN 或 Title，禁止只靠 format_basis 猜测标题样式。")
		}
		if !wordCopilotIsHeadingStyleID(paragraphStylePatch.StyleID) {
			return fmt.Errorf("标题插入时 paragraph_style_patch.style_id 必须是 HeadingN 或 Title，禁止继续沿用空样式或正文样式。")
		}
	}
	if wordCopilotRequiresTableBodyParagraphStylePatch(action, targetText, newText, editIntent, constraints, readerContextSummary, formatBasis) {
		if paragraphStylePatch == nil {
			return fmt.Errorf("表格单元格正文插入必须显式提供 paragraph_style_patch，至少写明 style_id=TableBody，禁止让新增句默认回退成 Normal 或继承标题段样式。")
		}
		styleID := wordCopilotNormalizeStyleID(paragraphStylePatch.StyleID)
		if styleID == "" {
			return fmt.Errorf("表格单元格正文插入时 paragraph_style_patch.style_id 不能为空；请直接复用 reader/exploration 命中的正文段样式，例如 TableBody。")
		}
		if wordCopilotIsHeadingStyleID(styleID) {
			return fmt.Errorf("表格单元格正文插入时 paragraph_style_patch.style_id 不能是标题样式；请改为正文样式，例如 TableBody。")
		}
		if wordCopilotMentionsTableBody(targetText, editIntent, constraints, readerContextSummary, formatBasis) && styleID != "tablebody" {
			return fmt.Errorf("当前 format_basis/reader_context_summary 已说明应沿用同格 TableBody 正文样式，因此 paragraph_style_patch.style_id 必须写成 TableBody。")
		}
	}
	_ = targetText
	return nil
}

func wordCopilotValidatePreciseAnchor(action, preferredParagraphID, tableID, editIntent, constraints, readerContextSummary string) error {
	if !wordCopilotRequiresRetrieval(action) {
		return nil
	}
	if strings.TrimSpace(preferredParagraphID) != "" || strings.TrimSpace(tableID) != "" {
		return nil
	}
	if !wordCopilotHasRelativeAnchorCue(editIntent, constraints, readerContextSummary) && !wordCopilotHasPageAnchorCue(editIntent, constraints, readerContextSummary) {
		return nil
	}
	return fmt.Errorf("当用户要求使用‘中间/最后/那条/那段/那格’或‘第几页/上一页/下一页’这类相对或页级定位描述时，必须把 auramate-word-reader 命中的 preferred_paragraph_id 或 table_id 一并传入，禁止仅靠 target_text 模糊定位。")
}

func wordCopilotValidateExplorationEvidence(action, preferredParagraphID, tableID, contextScope, editIntent, constraints, readerContextSummary, explorationSummary string) error {
	if !wordCopilotRequiresRetrieval(action) {
		return nil
	}
	needsExploration := wordCopilotHasRelativeAnchorCue(editIntent, constraints, readerContextSummary) || wordCopilotHasPageAnchorCue(editIntent, constraints, readerContextSummary)
	if !needsExploration && action == "update_table_cell" {
		needsExploration = wordCopilotHasTableValueCue(editIntent, constraints, readerContextSummary)
	}
	if !needsExploration && wordCopilotNormalizeContextScope(contextScope) == "table" {
		needsExploration = wordCopilotHasTableValueCue(editIntent, constraints, readerContextSummary)
	}
	if !needsExploration {
		return nil
	}
	if len([]rune(wordCopilotNormalizeText(explorationSummary))) < 12 {
		return fmt.Errorf("当前修改属于模糊定位或表格值定位；如果首次 read 或 query_hits 仍不能唯一确认目标，必须调用 auramate-word-reader 做 exploration 二次展开，并填写 exploration_summary，说明最终锁定的是哪一段、哪一格以及依据了哪些邻域上下文。")
	}
	if strings.TrimSpace(preferredParagraphID) == "" && strings.TrimSpace(tableID) == "" {
		return fmt.Errorf("当前修改需要 exploration 证据时，必须同时传入 preferred_paragraph_id 或 table_id，禁止只写 exploration_summary 而不提供最终精确锚点。")
	}
	return nil
}

func wordCopilotValidateReaderSessionAlignment(action, readerSessionID, preferredParagraphID, tableID, explorationSummary string, rowIndex, cellIndex *int) error {
	if !wordCopilotRequiresRetrieval(action) {
		return nil
	}
	hasPreciseReaderArtifact := strings.TrimSpace(preferredParagraphID) != "" || strings.TrimSpace(tableID) != "" || strings.TrimSpace(explorationSummary) != ""
	if _, ok := wordCopilotTableRowActions[action]; ok && rowIndex != nil {
		hasPreciseReaderArtifact = true
	}
	if _, ok := wordCopilotTableCellActions[action]; ok && rowIndex != nil {
		hasPreciseReaderArtifact = true
	}
	if _, ok := wordCopilotTableColumnActions[action]; ok && cellIndex != nil {
		hasPreciseReaderArtifact = true
	}
	if _, ok := wordCopilotTableCellActions[action]; ok && cellIndex != nil {
		hasPreciseReaderArtifact = true
	}
	if !hasPreciseReaderArtifact || strings.TrimSpace(readerSessionID) != "" {
		return nil
	}
	return fmt.Errorf("当前修改已经依赖 auramate-word-reader 的精确锚点、表格坐标或 exploration 结果，必须同时回填 reader_session_id，确保写入发生在同一 go-word 工作态。")
}

func wordCopilotValidateTableActionPayload(action, tableID string, rowIndex, cellIndex *int, tableRow *goword.TableRowInput, tableColumn *goword.TableColumnInput, tableCell *goword.TableCellInput) error {
	if _, ok := wordCopilotTableRowActions[action]; ok {
		if strings.TrimSpace(tableID) == "" {
			return fmt.Errorf("表格行编辑缺少 table_id；请先从 auramate-word-reader 的 query_hits 或 exploration 中拿到真实 table_id。")
		}
		if rowIndex == nil {
			return fmt.Errorf("表格行编辑缺少 row_index；请使用 auramate-word-reader 返回的真实行索引，而不是默认写第 0 行。")
		}
		if action == "insert_table_row" && tableRow == nil {
			return fmt.Errorf("insert_table_row 缺少 table_row；请提供要插入行的结构化内容。")
		}
	}
	if _, ok := wordCopilotTableColumnActions[action]; ok {
		if strings.TrimSpace(tableID) == "" {
			return fmt.Errorf("表格列编辑缺少 table_id；请先从 auramate-word-reader 的 query_hits 或 exploration 中拿到真实 table_id。")
		}
		if cellIndex == nil {
			return fmt.Errorf("表格列编辑缺少 cell_index；请使用 auramate-word-reader 返回的真实列索引，而不是默认写第 0 列。")
		}
		if action == "insert_table_column" && tableColumn == nil {
			return fmt.Errorf("insert_table_column 缺少 table_column；请提供要插入列的结构化内容。")
		}
	}
	if action == "update_table_cell" {
		if strings.TrimSpace(tableID) == "" {
			return fmt.Errorf("update_table_cell 缺少 table_id；请先从 auramate-word-reader 的 query_hits 或 exploration 中拿到真实 table_id，再更新目标单元格。")
		}
		if rowIndex == nil {
			return fmt.Errorf("update_table_cell 缺少 row_index；必须使用 auramate-word-reader query_hits 或 exploration 返回的真实行索引，禁止默认写第 0 行。")
		}
		if cellIndex == nil {
			return fmt.Errorf("update_table_cell 缺少 cell_index；必须使用 auramate-word-reader query_hits 或 exploration 返回的真实列索引，禁止默认写第 0 列。")
		}
		if tableCell == nil {
			return fmt.Errorf("update_table_cell 缺少 table_cell；请提供要写入目标单元格的结构化内容。")
		}
	}
	return nil
}

func wordCopilotValidateTableCellAnchor(action, tableID string, rowIndex, cellIndex *int, contextScope, readerContextSummary, formatBasis, explorationSummary string) error {
	if action != "insert" && action != "replace" && action != "delete" && action != "format" && action != "format_paragraph" {
		return nil
	}
	needsTableAnchor := wordCopilotNormalizeContextScope(contextScope) == "table" || wordCopilotMentionsTableAnchor(readerContextSummary, formatBasis, explorationSummary)
	if !needsTableAnchor {
		return nil
	}
	if strings.TrimSpace(tableID) == "" {
		return fmt.Errorf("当前动作已明确依赖表格单元格定位；必须回填 table_id，禁止只给 target_text 让执行层猜目标格。")
	}
	if rowIndex == nil {
		return fmt.Errorf("当前动作已明确依赖表格单元格定位；必须回填 row_index，禁止默认写第 0 行。")
	}
	if cellIndex == nil {
		return fmt.Errorf("当前动作已明确依赖表格单元格定位；必须回填 cell_index，禁止默认写第 0 列。")
	}
	return nil
}

func wordCopilotValidateHeaderFooterPayload(action string, headerFooter *goword.HeaderFooterInput) error {
	if action != "update_header_footer" {
		return nil
	}
	if headerFooter == nil {
		return fmt.Errorf("update_header_footer 缺少 header_footer；必须显式提供 kind/variant/blocks 的完整结构化内容，不能只给 target_text。")
	}
	return nil
}

func wordCopilotValidateSectionLayoutPayload(action string, sectionLayoutPatch *goword.SectionLayout) error {
	if action != "format_section" {
		return nil
	}
	if !wordCopilotHasSectionLayoutPatch(sectionLayoutPatch) {
		return fmt.Errorf("format_section 必须显式提供 section_layout_patch，例如 {\"page_number_start\":5,\"page_number_format\":\"upperRoman\"} 或 {\"orientation\":\"landscape\",\"margin_left\":1200}")
	}
	return nil
}

func wordCopilotHasSectionLayoutPatch(sectionLayoutPatch *goword.SectionLayout) bool {
	if sectionLayoutPatch == nil {
		return false
	}
	return strings.TrimSpace(sectionLayoutPatch.BreakType) != "" ||
		sectionLayoutPatch.PageWidth > 0 ||
		sectionLayoutPatch.PageHeight > 0 ||
		strings.TrimSpace(sectionLayoutPatch.Orientation) != "" ||
		sectionLayoutPatch.TitlePageSet ||
		sectionLayoutPatch.TitlePage ||
		sectionLayoutPatch.PageNumStart > 0 ||
		strings.TrimSpace(sectionLayoutPatch.PageNumFmt) != "" ||
		sectionLayoutPatch.MarginTop > 0 ||
		sectionLayoutPatch.MarginRight > 0 ||
		sectionLayoutPatch.MarginBottom > 0 ||
		sectionLayoutPatch.MarginLeft > 0 ||
		sectionLayoutPatch.MarginHeader > 0 ||
		sectionLayoutPatch.MarginFooter > 0 ||
		sectionLayoutPatch.MarginGutter > 0
}

func wordCopilotValidateTableCellContent(action, targetText string, tableCell *goword.TableCellInput) error {
	if action != "update_table_cell" || tableCell == nil {
		return nil
	}
	target := wordCopilotNormalizeText(targetText)
	hasText := false
	hasUpdatedContent := target == ""
	for _, paragraph := range tableCell.Paragraphs {
		text := wordCopilotNormalizeText(paragraph.Text)
		if text == "" {
			continue
		}
		hasText = true
		if text != target {
			hasUpdatedContent = true
		}
	}
	if !hasText {
		return fmt.Errorf("update_table_cell 的 table_cell 必须包含至少一段最终单元格内容，不能只给坐标或空结构。")
	}
	if !hasUpdatedContent {
		return fmt.Errorf("update_table_cell 的 table_cell 不能只回填 target_text 原文；必须提供更新后的完整单元格内容。若单元格内还有需保留的标题段，也要一并写入 table_cell。")
	}
	return nil
}

func wordCopilotValidateTableValueActionContract(action, targetText, preferredParagraphID, tableID string, rowIndex, cellIndex *int, contextScope, editIntent, constraints, readerContextSummary, formatBasis, explorationSummary string) error {
	if action != "replace" {
		return nil
	}
	if !wordCopilotHasExplicitTableContext(contextScope, tableID, rowIndex, cellIndex, readerContextSummary, formatBasis, explorationSummary) {
		return nil
	}
	if !wordCopilotHasTableValueCue(targetText, editIntent, constraints, readerContextSummary) {
		return nil
	}
	_ = preferredParagraphID
	return fmt.Errorf("表格值或右侧结果这类修改必须使用 update_table_cell，禁止退化为 replace。")
}

func wordCopilotValidateTextPayloadContract(action, editIntent, constraints, readerContextSummary, formatBasis, newText string, stylePatch *goword.TextStyle, paragraphStylePatch *goword.ParagraphStyle, tableSpec *goword.TableInput, tableRow *goword.TableRowInput, tableColumn *goword.TableColumnInput, tableCell *goword.TableCellInput, headerFooter *goword.HeaderFooterInput) error {
	if action != "replace" && action != "insert" && action != "insert_table" && action != "insert_table_row" && action != "insert_table_column" && action != "update_table_cell" && action != "update_header_footer" {
		return nil
	}
	if wordCopilotHasLiteralPipeCue(editIntent, constraints, readerContextSummary, formatBasis) {
		return nil
	}
	textValues := []string{newText}
	textValues = append(textValues, wordCopilotCollectTextValues(stylePatch)...)
	textValues = append(textValues, wordCopilotCollectTextValues(paragraphStylePatch)...)
	textValues = append(textValues, wordCopilotCollectTextValues(tableSpec)...)
	textValues = append(textValues, wordCopilotCollectTextValues(tableRow)...)
	textValues = append(textValues, wordCopilotCollectTextValues(tableColumn)...)
	textValues = append(textValues, wordCopilotCollectTextValues(tableCell)...)
	textValues = append(textValues, wordCopilotCollectTextValues(headerFooter)...)
	for _, text := range textValues {
		if wordCopilotContainsSuspiciousPipeMarker(text) {
			return fmt.Errorf("检测到正文内容包含疑似定位分隔符“|”。Word 编辑不支持用“|”表达定位、换行或断段；如需换行/断段请改用 insert_line_break 或 insert_paragraph_break，如需写入字面量竖线请在 edit_intent 或 constraints 中明确说明“插入竖线字符”。")
		}
	}
	return nil
}

func wordCopilotMentionsTableAnchor(values ...string) bool {
	hasExplicitNonTableCue := false
	for _, value := range values {
		normalized := wordCopilotNormalizeText(value)
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

func wordCopilotCollectTextValues(value any) []string {
	if value == nil {
		return nil
	}
	marshaled, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	var decoded any
	if err := json.Unmarshal(marshaled, &decoded); err != nil {
		return nil
	}
	results := make([]string, 0)
	var walk func(any)
	walk = func(current any) {
		switch typed := current.(type) {
		case string:
			trimmed := strings.TrimSpace(typed)
			if trimmed != "" {
				results = append(results, trimmed)
			}
		case []any:
			for _, item := range typed {
				walk(item)
			}
		case map[string]any:
			for key, item := range typed {
				if key == "text" {
					walk(item)
					continue
				}
				walk(item)
			}
		}
	}
	walk(decoded)
	return results
}

func wordCopilotDefaultIfEmpty(value, defaultValue string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return defaultValue
	}
	return trimmed
}

func wordCopilotPreviewTarget(value string, limit int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= limit {
		return string(runes)
	}
	return string(runes[:limit]) + "..."
}

func wordCopilotConstraintsText(constraints *goword.ActionConstraints) string {
	if constraints == nil {
		return ""
	}
	return strings.TrimSpace(constraints.RawText)
}

func wordCopilotNonEmptyParts(parts ...string) []string {
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.ToLower(strings.TrimSpace(part))
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
