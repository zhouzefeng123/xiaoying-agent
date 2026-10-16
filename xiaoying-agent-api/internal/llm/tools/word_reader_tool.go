package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"sync"

	"github.com/auramate/auramate/internal/goword"
)

const (
	WordReaderToolName                 = "auramate-word-reader"
	wordReaderDefaultMaxChars          = 0
	wordReaderDefaultMaxHits           = 16
	wordReaderDefaultOutlineItems      = 48
	wordReaderDefaultStyleCatalogItems = 16
	wordReaderDefaultQueryStyleItems   = 10
	wordReaderDefaultTableContextCells = 6
	wordReaderDefaultExplorationRadius = 2
	wordReaderDefaultLayoutPageItems   = 8
	wordReaderDefaultLayoutPageBlocks  = 6
)

var (
	wordReaderToolGoWordMu      sync.RWMutex
	wordReaderToolGoWordService *goword.Service
	wordReaderPageAnchorPattern = regexp.MustCompile(`第\s*[0-9一二三四五六七八九十百千万]+\s*页`)
	wordReaderChapterPattern    = regexp.MustCompile(`^第[一二三四五六七八九十百千万0-9]+章`)
	wordReaderAppendixPattern   = regexp.MustCompile(`^附录[一二三四五六七八九十百千万0-9A-Za-z]*`)
	wordReaderNumberedPattern   = regexp.MustCompile(`^([一二三四五六七八九十百千万0-9]+)[、.．]\s*(.+)$`)
	wordReaderCommonStyles      = map[string]struct{}{
		"normal":        {},
		"title":         {},
		"subtitle":      {},
		"heading1":      {},
		"heading2":      {},
		"heading3":      {},
		"heading4":      {},
		"heading5":      {},
		"heading6":      {},
		"listparagraph": {},
		"listbullet":    {},
		"listnumber":    {},
	}
)

type wordReaderTool struct{}

type wordReaderParagraphContext struct {
	Paragraph           goword.Paragraph
	HostBlocks          []goword.Block
	BlockIndex          int
	Region              string
	HeaderFooterKind    string
	HeaderFooterVariant string
	TableID             string
	RowIndex            *int
	CellIndex           *int
}

type wordReaderTableContext struct {
	Table               goword.Table
	HostBlocks          []goword.Block
	BlockIndex          int
	Region              string
	HeaderFooterKind    string
	HeaderFooterVariant string
	ParentTableID       string
	ParentRowIndex      *int
	ParentCellIndex     *int
}

func SetWordReaderToolGoWordService(service *goword.Service) {
	wordReaderToolGoWordMu.Lock()
	defer wordReaderToolGoWordMu.Unlock()
	wordReaderToolGoWordService = service
}

func getWordReaderToolGoWordService() *goword.Service {
	wordReaderToolGoWordMu.RLock()
	defer wordReaderToolGoWordMu.RUnlock()
	return wordReaderToolGoWordService
}

func NewWordReaderTool() BaseTool {
	return &wordReaderTool{}
}

func (t *wordReaderTool) Info() ToolInfo {
	return ToolInfo{
		Name: WordReaderToolName,
		Description: strings.Join([]string{
			"XiaoYing for Word 的精确定位工具。只读，不负责执行任何写入动作。",
			"推荐顺序是 read -> 按需 auramate-word-reader -> auramate-word-copilot。若只是做 docx 全文理解、结构通读或模糊搜索，优先使用 read；只有在还需要真实 paragraph_id/table_id、当前 go-word 会话定位、相对或页级定位消歧、表格值单元格确认或样式证据时，再调用本工具。",
			"默认是纯定位模式：会返回 document、query_hits、style_catalog、guidance 等证据；只有显式传 include_full_text/include_outline/include_layout 时，才会额外返回全文摘录、结构摘要或页级布局摘要。",
			"query 支持换行或 || 一次提供多个候选检索词。若第一次命中后仍不够唯一，必须继续使用 focus_hit_index、focus_paragraph_id、focus_table_id、focus_row_index、focus_cell_index、focus_page_index、expand_radius 做 exploration 二次展开。",
			"对表格里的通过率、金额、总计、右侧结果这类值定位，第一次调用应优先走最小表格读取路径：context_scope=table，并显式保持 include_full_text=false、include_outline=false；命中后再优先用 focus_hit_index=0 或 focus_table_id 展开整表/目标格，避免全文重读。",
			"处理‘中间那条’‘最后那段’‘这一行右边那个值’‘第几页/上一页/下一页’这类相对或页级定位时，应优先结合 include_layout 或局部 focus，再确认最终 paragraph_id、table_id、row_index、cell_index 或页级证据。",
			"当 include_full_text=true 且提供 max_chars 时，full_text_excerpt 以及 exploration/query/layout 中的大文本证据会按上限折叠；裁剪信息会通过 full_text_truncated 和 full_text_omitted_chars 回传。",
		}, "\n\n"),
		DisableAutoSummary: true,
		Parameters: map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "要读取的 Word 文档路径。支持绝对路径或工作区相对路径。与 session_id 二选一即可。",
			},
			"file_path": map[string]any{
				"type":        "string",
				"description": "兼容别名：等同于 path。若模型习惯传 file_path，可直接复用。",
			},
			"session_id": map[string]any{
				"type":        "string",
				"description": "已存在的 go-word 会话 ID。若已打开 Word 文档，优先传此值避免重复打开。",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "可选检索词。支持换行或 || 一次提供多个候选检索词。",
			},
			"focus_hit_index": map[string]any{
				"type":        "integer",
				"description": "可选：基于本次 query 返回的 hits 索引继续展开局部上下文。",
			},
			"focus_paragraph_id": map[string]any{
				"type":        "string",
				"description": "可选：围绕指定 paragraph_id 继续读取相邻段落或表格。",
			},
			"focus_table_id": map[string]any{
				"type":        "string",
				"description": "可选：围绕指定 table_id 继续读取表格局部上下文。",
			},
			"focus_row_index": map[string]any{
				"type":        "integer",
				"description": "可选：配合 focus_table_id 使用，聚焦具体表格行。",
			},
			"focus_cell_index": map[string]any{
				"type":        "integer",
				"description": "可选：配合 focus_table_id 使用，聚焦具体表格列/单元格。",
			},
			"expand_radius": map[string]any{
				"type":        "integer",
				"description": "可选：局部展开半径。默认 2。",
			},
			"context_scope": map[string]any{
				"type":        "string",
				"enum":        []string{"line", "paragraph", "table", "all"},
				"description": "检索范围，默认 all。",
			},
			"max_chars": map[string]any{
				"type":        "integer",
				"description": "可选：限制全文摘录和定位证据文本字段的最大字符数。0 表示不裁剪。",
			},
			"max_hits": map[string]any{
				"type":        "integer",
				"description": "最多返回多少条检索命中，默认 16。",
			},
			"include_layout": map[string]any{
				"type":        "boolean",
				"description": "是否返回紧凑分页/布局摘要。适合处理第几页、上一页、下一页等页级定位。",
			},
			"focus_page_index": map[string]any{
				"type":        "integer",
				"description": "可选：配合 include_layout 使用，聚焦某一页，0-based。",
			},
			"include_full_text": map[string]any{
				"type":        "boolean",
				"description": "是否返回全文摘录。默认 false，只有需要全文证据时才显式打开。",
			},
			"include_outline": map[string]any{
				"type":        "boolean",
				"description": "是否返回结构摘要。默认 false，只有需要结构证据时才显式打开。",
			},
		},
		Required: []string{},
	}
}

func (t *wordReaderTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	params := map[string]any{}
	if strings.TrimSpace(call.Input) != "" {
		if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
			return NewTextErrorResponse(fmt.Sprintf("error parsing parameters: %v", err)), nil
		}
	}

	path := strings.TrimSpace(wordReaderStringParam(params, "path"))
	if path == "" {
		path = strings.TrimSpace(wordReaderStringParam(params, "file_path"))
	}
	sessionID := strings.TrimSpace(wordReaderStringParam(params, "session_id"))
	if path == "" && sessionID == "" {
		return wordReaderJSONResponse(wordReaderReadyPayload()), nil
	}

	service := getWordReaderToolGoWordService()
	if service == nil {
		return NewTextErrorResponse("auramate-word-reader 尚未绑定 go-word service"), nil
	}

	query := strings.TrimSpace(wordReaderStringParam(params, "query"))
	scope := wordReaderNormalizeScope(wordReaderStringParam(params, "context_scope"))
	maxChars := wordReaderIntParam(params, "max_chars", wordReaderDefaultMaxChars)
	maxHits := wordReaderIntParam(params, "max_hits", wordReaderDefaultMaxHits)
	focusHitIndex := wordReaderOptionalIntParam(params, "focus_hit_index")
	focusParagraphID := strings.TrimSpace(wordReaderStringParam(params, "focus_paragraph_id"))
	focusTableID := strings.TrimSpace(wordReaderStringParam(params, "focus_table_id"))
	focusRowIndex := wordReaderOptionalIntParam(params, "focus_row_index")
	focusCellIndex := wordReaderOptionalIntParam(params, "focus_cell_index")
	expandRadius := wordReaderIntParam(params, "expand_radius", wordReaderDefaultExplorationRadius)
	focusPageIndex := wordReaderOptionalIntParam(params, "focus_page_index")
	includeLayout := wordReaderBoolParam(params, "include_layout", false)
	includeFullTextRaw := wordReaderOptionalBoolParam(params, "include_full_text")
	includeOutlineRaw := wordReaderOptionalBoolParam(params, "include_outline")
	includeFullText, includeOutline := wordReaderResolveOutputOptions(includeFullTextRaw, includeOutlineRaw, query, scope)

	var session *goword.Session
	var err error
	if sessionID != "" {
		session, err = service.Snapshot(sessionID)
	} else {
		resolvedPath := ResolvePath(path)
		document, openErr := goword.OpenDOCX(resolvedPath)
		if openErr != nil {
			return NewTextErrorResponse(fmt.Sprintf("无法打开 Word 文档: %v", openErr)), nil
		}
		session, err = service.CreateSession(document)
	}
	if err != nil {
		return NewTextErrorResponse(err.Error()), nil
	}

	response, buildErr := wordReaderBuildResponse(service, session, query, scope, maxChars, maxHits, includeLayout, includeFullText, includeOutline, focusHitIndex, focusParagraphID, focusTableID, focusRowIndex, focusCellIndex, focusPageIndex, expandRadius)
	if buildErr != nil {
		return NewTextErrorResponse(buildErr.Error()), nil
	}
	return wordReaderJSONResponse(response), nil
}

func wordReaderJSONResponse(payload any) ToolResponse {
	bytes, err := json.Marshal(payload)
	if err != nil {
		return NewTextErrorResponse(err.Error())
	}
	return NewTextResponse(string(bytes))
}

func wordReaderReadyPayload() map[string]any {
	return map[string]any{
		"status":           "ready",
		"message":          "auramate-word-reader 已就绪。若只需读取 docx 内容，优先使用 read；若需要精确定位，请提供 path 或 session_id，并结合 query / focus 参数。",
		"supported_scopes": []string{"line", "paragraph", "table", "all"},
		"supported_focus":  []string{"focus_hit_index", "focus_paragraph_id", "focus_table_id", "focus_page_index"},
	}
}

func wordReaderBuildResponse(service *goword.Service, session *goword.Session, query, scope string, maxChars, maxHits int, includeLayout, includeFullText, includeOutline bool, focusHitIndex *int, focusParagraphID, focusTableID string, focusRowIndex, focusCellIndex, focusPageIndex *int, expandRadius int) (map[string]any, error) {
	document := session.Document
	if document == nil {
		return nil, fmt.Errorf("session document is nil")
	}

	var (
		outline []map[string]any
		stats   map[string]int
	)
	if includeOutline {
		outline, stats = wordReaderBuildOutline(document, wordReaderDefaultOutlineItems)
	} else {
		stats = wordReaderCollectDocumentStats(document)
	}

	fullTextCharCount := wordReaderComputeFullTextCharCount(document)
	fullTextExcerpt := any(nil)
	if includeFullText {
		fullText := wordReaderBuildFullText(document)
		fullTextExcerpt = fullText
		fullTextCharCount = len([]rune(fullText))
	}

	queryTerms := wordReaderParseQueryTerms(query)
	queryHits := make([]map[string]any, 0)
	rawHits := make([]goword.SearchHit, 0)
	focusStyleIDs := make(map[string]struct{})
	searchTruncated := false
	hitCountByTerm := map[string]int{}
	if len(queryTerms) > 0 {
		mergedHits, matchedQueriesByKey, mergedTruncated, counts, err := wordReaderMergeSearchHits(service, session.ID, queryTerms, scope, maxHits)
		if err != nil {
			return nil, err
		}
		rawHits = mergedHits
		searchTruncated = mergedTruncated
		hitCountByTerm = counts
		for hitIndex, hit := range rawHits {
			paragraphSummary := map[string]any(nil)
			styleID := ""
			if strings.TrimSpace(hit.ParagraphID) != "" {
				paragraphSummary = wordReaderBuildHitParagraphSummary(document, hit.ParagraphID)
				if value, ok := paragraphSummary["style_id"].(string); ok {
					styleID = value
				}
			}
			if styleID != "" {
				focusStyleIDs[styleID] = struct{}{}
			}
			entry := map[string]any{
				"hit_index":       hitIndex,
				"type":            hit.Type,
				"paragraph_id":    wordReaderNilIfEmpty(hit.ParagraphID),
				"table_id":        wordReaderNilIfEmpty(hit.TableID),
				"matched_queries": wordReaderMatchedQueries(hit, matchedQueriesByKey, queryTerms),
				"preview":         wordReaderCompactInlineText(hit.Text, 180),
			}
			region := strings.TrimSpace(hit.Region)
			if region == "" && paragraphSummary != nil {
				if value, ok := paragraphSummary["region"].(string); ok {
					region = value
				}
			}
			if region != "" {
				entry["region"] = region
			}
			kind := strings.TrimSpace(hit.HeaderFooterKind)
			if kind == "" && paragraphSummary != nil {
				if value, ok := paragraphSummary["header_footer_kind"].(string); ok {
					kind = value
				}
			}
			if kind != "" {
				entry["header_footer_kind"] = kind
			}
			variant := strings.TrimSpace(hit.HeaderFooterVariant)
			if variant == "" && paragraphSummary != nil {
				if value, ok := paragraphSummary["header_footer_variant"].(string); ok {
					variant = value
				}
			}
			if variant != "" {
				entry["header_footer_variant"] = variant
			}
			if hit.TableID != "" {
				entry["row_index"] = hit.RowIndex
				entry["cell_index"] = hit.CellIndex
			}
			if hit.Type == "line" || hit.LineIndex > 0 {
				entry["line_index"] = hit.LineIndex
			}
			if hit.MatchStart > 0 || hit.MatchEnd > 0 || hit.MatchEnd > hit.MatchStart {
				entry["match_start"] = hit.MatchStart
				entry["match_end"] = hit.MatchEnd
			}
			if styleID != "" {
				entry["style_id"] = styleID
			}
			if paragraphSummary != nil {
				if value, ok := paragraphSummary["paragraph_style"]; ok {
					entry["paragraph_style"] = value
				}
				if value, ok := paragraphSummary["dominant_text_style"]; ok {
					entry["dominant_text_style"] = value
				}
			}
			for key, value := range wordReaderBuildTableHitContext(document, hit.TableID, hit.RowIndex, hit.CellIndex) {
				entry[key] = value
			}
			queryHits = append(queryHits, entry)
		}
	}

	styleCatalog := wordReaderBuildStyleCatalog(document, focusStyleIDs)
	exploration := wordReaderBuildExploration(document, rawHits, focusHitIndex, focusParagraphID, focusTableID, focusRowIndex, focusCellIndex, maxInt(expandRadius, 1))

	var (
		rawLayout *goword.DocumentLayout
		layout    any
	)
	if includeLayout {
		layoutSnapshot, err := service.LayoutSnapshot(session.ID, goword.LayoutOptions{})
		if err != nil {
			return nil, err
		}
		rawLayout = layoutSnapshot
		layout = wordReaderBuildLayoutSummary(rawLayout, focusPageIndex)
	}
	if rawLayout != nil {
		for _, hit := range queryHits {
			paragraphID, _ := hit["paragraph_id"].(string)
			tableID, _ := hit["table_id"].(string)
			pageNumbers := wordReaderFindLayoutPageNumbers(rawLayout, paragraphID, tableID)
			if len(pageNumbers) > 0 {
				hit["page_numbers"] = pageNumbers
			}
		}
		if explorationMap, ok := exploration.(map[string]any); ok {
			if resolved, ok := explorationMap["resolved"].(map[string]any); ok {
				paragraphID, _ := resolved["paragraph_id"].(string)
				tableID, _ := resolved["table_id"].(string)
				pageNumbers := wordReaderFindLayoutPageNumbers(rawLayout, paragraphID, tableID)
				if len(pageNumbers) > 0 {
					explorationMap["page_numbers"] = pageNumbers
				}
			}
		}
	}

	guidance := wordReaderBuildLocatorGuidance(stats, queryTerms, queryHits, exploration, layout)
	response := map[string]any{
		"status": "ok",
		"document": map[string]any{
			"name":                 document.Name,
			"source_path":          wordReaderNilIfEmpty(document.SourcePath),
			"revision":             document.Revision,
			"block_count":          len(document.Blocks),
			"full_text_char_count": fullTextCharCount,
			"paragraph_count":      stats["paragraph_count"],
			"table_count":          stats["table_count"],
			"heading_count":        stats["heading_count"],
			"header_footer_count":  stats["header_footer_count"],
		},
		"style_catalog":           styleCatalog,
		"outline":                 wordReaderOutlineOrEmpty(includeOutline, outline),
		"full_text_excerpt":       fullTextExcerpt,
		"full_text_truncated":     false,
		"full_text_omitted_chars": 0,
		"layout":                  layout,
		"exploration":             exploration,
		"guidance":                guidance,
		"query": map[string]any{
			"text":              wordReaderNilIfEmpty(query),
			"terms":             queryTerms,
			"scope":             scope,
			"hit_count":         len(queryHits),
			"hit_count_by_term": hitCountByTerm,
			"hits":              queryHits,
			"truncated":         searchTruncated,
		},
		"session": map[string]any{
			"id":         session.ID,
			"updated_at": session.UpdatedAt,
		},
	}
	wordReaderApplyMaxCharsContract(response, maxChars)
	return response, nil
}

func wordReaderApplyMaxCharsContract(response map[string]any, maxChars int) {
	if maxChars <= 0 {
		return
	}

	if fullText, ok := response["full_text_excerpt"].(string); ok && fullText != "" {
		folded, omitted := wordReaderFoldTextToLimit(fullText, maxChars)
		response["full_text_excerpt"] = folded
		response["full_text_truncated"] = omitted > 0
		response["full_text_omitted_chars"] = omitted
	}

	if outline, ok := response["outline"].([]map[string]any); ok {
		for _, item := range outline {
			wordReaderClampMapStringField(item, "preview", maxChars)
		}
	}

	queryPayload, _ := response["query"].(map[string]any)
	if hits, ok := queryPayload["hits"].([]map[string]any); ok {
		for _, hit := range hits {
			wordReaderClampMapStringField(hit, "preview", maxChars)
			wordReaderClampMapStringField(hit, "cell_text", maxChars)
			wordReaderClampMapStringField(hit, "row_preview", maxChars)
			wordReaderClampMapStringField(hit, "row_header", maxChars)
			wordReaderClampMapStringField(hit, "column_header", maxChars)
			if rowCells, ok := hit["row_cells"].([]map[string]any); ok {
				for _, cell := range rowCells {
					wordReaderClampMapStringField(cell, "text", maxChars)
				}
			}
		}
	}

	if exploration, ok := response["exploration"].(map[string]any); ok {
		wordReaderClampNestedTextFields(exploration, maxChars, map[string]struct{}{
			"preview":       {},
			"text":          {},
			"row_preview":   {},
			"cell_text":     {},
			"row_header":    {},
			"column_header": {},
		})
	}

	if layout, ok := response["layout"].(map[string]any); ok {
		wordReaderClampNestedTextFields(layout, maxChars, map[string]struct{}{
			"preview": {},
		})
	}
}

func wordReaderClampMapStringField(values map[string]any, key string, maxChars int) {
	if values == nil {
		return
	}
	text, ok := values[key].(string)
	if !ok || text == "" {
		return
	}
	folded, _ := wordReaderFoldTextToLimit(text, maxChars)
	values[key] = folded
}

func wordReaderClampNestedTextFields(value any, maxChars int, keys map[string]struct{}) {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			if text, ok := child.(string); ok {
				if _, clamp := keys[key]; clamp {
					folded, _ := wordReaderFoldTextToLimit(text, maxChars)
					typed[key] = folded
				}
				continue
			}
			wordReaderClampNestedTextFields(child, maxChars, keys)
		}
	case []map[string]any:
		for _, item := range typed {
			wordReaderClampNestedTextFields(item, maxChars, keys)
		}
	case []any:
		for _, item := range typed {
			wordReaderClampNestedTextFields(item, maxChars, keys)
		}
	}
}

func wordReaderFoldTextToLimit(text string, maxChars int) (string, int) {
	if maxChars <= 0 {
		return text, 0
	}
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text, 0
	}
	if maxChars <= 16 {
		return string(runes[:maxChars]), len(runes) - maxChars
	}
	marker := "...[truncated]..."
	markerRunes := []rune(marker)
	keep := maxChars - len(markerRunes)
	if keep <= 0 {
		return string(runes[:maxChars]), len(runes) - maxChars
	}
	headKeep := keep/2 + keep%2
	tailKeep := keep / 2
	omitted := len(runes) - headKeep - tailKeep
	if omitted <= 0 {
		return string(runes[:maxChars]), len(runes) - maxChars
	}
	return string(runes[:headKeep]) + marker + string(runes[len(runes)-tailKeep:]), omitted
}

func wordReaderOutlineOrEmpty(includeOutline bool, outline []map[string]any) any {
	if includeOutline {
		return outline
	}
	return []any{}
}

func wordReaderStringParam(params map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := params[key]; ok {
			switch typed := value.(type) {
			case string:
				return typed
			case json.Number:
				return typed.String()
			}
		}
	}
	return ""
}

func wordReaderIntParam(params map[string]any, key string, defaultValue int) int {
	if value := wordReaderOptionalIntParam(params, key); value != nil {
		return *value
	}
	return defaultValue
}

func wordReaderOptionalIntParam(params map[string]any, key string) *int {
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

func wordReaderBoolParam(params map[string]any, key string, defaultValue bool) bool {
	if value := wordReaderOptionalBoolParam(params, key); value != nil {
		return *value
	}
	return defaultValue
}

func wordReaderOptionalBoolParam(params map[string]any, key string) *bool {
	value, ok := params[key]
	if !ok || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case bool:
		result := typed
		return &result
	case string:
		trimmed := strings.ToLower(strings.TrimSpace(typed))
		if trimmed == "" {
			return nil
		}
		result := trimmed == "1" || trimmed == "true" || trimmed == "yes" || trimmed == "on"
		return &result
	default:
		return nil
	}
}

func wordReaderNormalizeScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "line", "paragraph", "table", "all":
		return strings.ToLower(strings.TrimSpace(scope))
	default:
		return "all"
	}
}

func wordReaderResolveOutputOptions(includeFullText, includeOutline *bool, query, scope string) (bool, bool) {
	_ = query
	_ = scope
	resolvedFullText := false
	resolvedOutline := false
	if includeFullText != nil {
		resolvedFullText = *includeFullText
	}
	if includeOutline != nil {
		resolvedOutline = *includeOutline
	}
	return resolvedFullText, resolvedOutline
}

func wordReaderParseQueryTerms(query string) []string {
	raw := strings.ReplaceAll(query, "｜｜", "||")
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	seen := make(map[string]struct{})
	terms := make([]string, 0)
	for _, line := range strings.Split(raw, "\n") {
		for _, part := range strings.Split(line, "||") {
			trimmed := strings.TrimSpace(part)
			if trimmed == "" {
				continue
			}
			if _, ok := seen[trimmed]; ok {
				continue
			}
			seen[trimmed] = struct{}{}
			terms = append(terms, trimmed)
		}
	}
	return terms
}

func wordReaderMergeSearchHits(service *goword.Service, sessionID string, queryTerms []string, scope string, maxHits int) ([]goword.SearchHit, map[string][]string, bool, map[string]int, error) {
	mergedHits := make([]goword.SearchHit, 0)
	matchedQueries := make(map[string][]string)
	hitIndex := make(map[string]int)
	hitCountByTerm := make(map[string]int)
	truncated := false
	for _, term := range queryTerms {
		result, err := service.Search(sessionID, goword.SearchRequest{Query: term, Scope: scope, Mode: "contains", Limit: maxHits})
		if err != nil {
			return nil, nil, false, nil, err
		}
		hitCountByTerm[term] = len(result.Hits)
		if result.Truncated {
			truncated = true
		}
		for _, hit := range result.Hits {
			key := wordReaderSearchHitKey(hit)
			if idx, ok := hitIndex[key]; ok {
				matchedQueries[key] = appendUniqueString(matchedQueries[key], term)
				_ = idx
				continue
			}
			if len(mergedHits) >= maxHits {
				truncated = true
				continue
			}
			hitIndex[key] = len(mergedHits)
			matchedQueries[key] = []string{term}
			mergedHits = append(mergedHits, hit)
		}
	}
	return mergedHits, matchedQueries, truncated, hitCountByTerm, nil
}

func wordReaderSearchHitKey(hit goword.SearchHit) string {
	return fmt.Sprintf("%s|%s|%s|%s|%s|%s|%d|%d|%d|%s|%d|%d", hit.Type, hit.Region, hit.HeaderFooterKind, hit.HeaderFooterVariant, hit.ParagraphID, hit.TableID, hit.RowIndex, hit.CellIndex, hit.LineIndex, hit.Text, hit.MatchStart, hit.MatchEnd)
}

func wordReaderMatchedQueries(hit goword.SearchHit, matchedQueriesByKey map[string][]string, queryTerms []string) []string {
	if matchedQueriesByKey != nil {
		if matched := matchedQueriesByKey[wordReaderSearchHitKey(hit)]; len(matched) > 0 {
			return append([]string(nil), matched...)
		}
	}
	matched := make([]string, 0, len(queryTerms))
	text := hit.Text
	for _, term := range queryTerms {
		if strings.Contains(text, term) {
			matched = append(matched, term)
		}
	}
	if len(matched) == 0 && len(queryTerms) > 0 {
		matched = append(matched, queryTerms[0])
	}
	return matched
}

func wordReaderParagraphText(paragraph goword.Paragraph) string {
	var builder strings.Builder
	for _, run := range paragraph.Runs {
		builder.WriteString(run.Text)
	}
	return strings.TrimSpace(builder.String())
}

func wordReaderTableCellText(cell goword.TableCell) string {
	parts := make([]string, 0)
	for _, block := range cell.Blocks {
		switch block.Type {
		case goword.BlockTypeParagraph:
			if block.Paragraph != nil {
				text := wordReaderParagraphText(*block.Paragraph)
				if text != "" {
					parts = append(parts, text)
				}
			}
		case goword.BlockTypeTable:
			if block.Table != nil {
				text := wordReaderTableText(*block.Table)
				if text != "" {
					parts = append(parts, text)
				}
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func wordReaderTableText(table goword.Table) string {
	rowTexts := make([]string, 0, len(table.Rows))
	for _, row := range table.Rows {
		cells := make([]string, 0, len(row.Cells))
		for _, cell := range row.Cells {
			text := wordReaderTableCellText(cell)
			if text != "" {
				cells = append(cells, text)
			}
		}
		if len(cells) > 0 {
			rowTexts = append(rowTexts, strings.Join(cells, " | "))
		}
	}
	return strings.TrimSpace(strings.Join(rowTexts, "\n"))
}

func wordReaderTableDimensions(table goword.Table) map[string]any {
	columnCount := 0
	for _, row := range table.Rows {
		if len(row.Cells) > columnCount {
			columnCount = len(row.Cells)
		}
	}
	return map[string]any{"row_count": len(table.Rows), "column_count": columnCount}
}

func wordReaderCompactInlineText(text string, limit int) string {
	normalized := strings.Join(strings.Fields(text), " ")
	runes := []rune(normalized)
	if len(runes) <= limit {
		return normalized
	}
	return string(runes[:limit]) + "..."
}

func wordReaderCompactParagraphStyle(style goword.ParagraphStyle) map[string]any {
	result := map[string]any{
		"style_id":          wordReaderNilIfEmpty(style.StyleID),
		"alignment":         wordReaderNilIfEmpty(strings.ToLower(strings.TrimSpace(style.Alignment))),
		"spacing_before":    style.SpacingBefore,
		"spacing_after":     style.SpacingAfter,
		"line_spacing":      style.LineSpacing,
		"first_line_indent": style.FirstLineIndent,
		"left_indent":       style.LeftIndent,
	}
	if style.Numbering != nil {
		result["numbering"] = map[string]any{"num_id": style.Numbering.NumID, "level": style.Numbering.Level}
	}
	return result
}

func wordReaderCompactTextStyle(style goword.TextStyle) map[string]any {
	return map[string]any{
		"style_id":             wordReaderNilIfEmpty(style.StyleID),
		"font_family":          wordReaderNilIfEmpty(style.FontFamily),
		"theme_font_ascii":     wordReaderNilIfEmpty(style.ThemeFontASCII),
		"theme_font_east_asia": wordReaderNilIfEmpty(style.ThemeFontEastAsia),
		"font_size_hp":         style.FontSizeHP,
		"bold":                 style.Bold,
		"italic":               style.Italic,
		"underline":            style.Underline,
		"color":                wordReaderNilIfEmpty(style.Color),
		"theme_color":          wordReaderNilIfEmpty(style.ThemeColor),
	}
}

func wordReaderDominantRunStyle(paragraph goword.Paragraph) map[string]any {
	for _, run := range paragraph.Runs {
		if strings.TrimSpace(run.Text) != "" {
			return wordReaderCompactTextStyle(run.Style)
		}
	}
	if len(paragraph.Runs) > 0 {
		return wordReaderCompactTextStyle(paragraph.Runs[0].Style)
	}
	return wordReaderCompactTextStyle(goword.TextStyle{})
}

func wordReaderBuildBlockSnapshot(block goword.Block, previewLimit int) map[string]any {
	switch block.Type {
	case goword.BlockTypeParagraph:
		if block.Paragraph == nil {
			return nil
		}
		text := wordReaderParagraphText(*block.Paragraph)
		if text == "" {
			return nil
		}
		return map[string]any{
			"type":                "paragraph",
			"paragraph_id":        wordReaderNilIfEmpty(block.Paragraph.ID),
			"style_id":            wordReaderNilIfEmpty(block.Paragraph.Style.StyleID),
			"paragraph_style":     wordReaderCompactParagraphStyle(block.Paragraph.Style),
			"dominant_text_style": wordReaderDominantRunStyle(*block.Paragraph),
			"text_length":         len([]rune(text)),
			"preview":             wordReaderCompactInlineText(text, previewLimit),
		}
	case goword.BlockTypeTable:
		if block.Table == nil {
			return nil
		}
		return map[string]any{
			"type":       "table",
			"table_id":   wordReaderNilIfEmpty(block.Table.ID),
			"dimensions": wordReaderTableDimensions(*block.Table),
			"preview":    wordReaderCompactInlineText(wordReaderTableText(*block.Table), previewLimit),
		}
	default:
		return nil
	}
}

func wordReaderBuildNeighborBlocks(blocks []goword.Block, blockIndex, radius int) map[string]any {
	before := make([]map[string]any, 0)
	after := make([]map[string]any, 0)
	start := maxInt(0, blockIndex-radius)
	end := minInt(len(blocks), blockIndex+radius+1)
	for idx := start; idx < blockIndex; idx++ {
		snapshot := wordReaderBuildBlockSnapshot(blocks[idx], 180)
		if snapshot == nil {
			continue
		}
		snapshot["relative_index"] = idx - blockIndex
		before = append(before, snapshot)
	}
	for idx := blockIndex + 1; idx < end; idx++ {
		snapshot := wordReaderBuildBlockSnapshot(blocks[idx], 180)
		if snapshot == nil {
			continue
		}
		snapshot["relative_index"] = idx - blockIndex
		after = append(after, snapshot)
	}
	return map[string]any{"before": before, "after": after}
}

func wordReaderFindParagraphContextInBlocks(blocks []goword.Block, paragraphID, region, headerFooterKind, headerFooterVariant string, tableID string, rowIndex, cellIndex *int) *wordReaderParagraphContext {
	trimmedID := strings.TrimSpace(paragraphID)
	if trimmedID == "" {
		return nil
	}
	for blockIndex, block := range blocks {
		switch block.Type {
		case goword.BlockTypeParagraph:
			if block.Paragraph != nil && block.Paragraph.ID == trimmedID {
				return &wordReaderParagraphContext{
					Paragraph:           *block.Paragraph,
					HostBlocks:          blocks,
					BlockIndex:          blockIndex,
					Region:              wordReaderNormalizeRegion(region, headerFooterKind),
					HeaderFooterKind:    strings.ToLower(strings.TrimSpace(headerFooterKind)),
					HeaderFooterVariant: strings.ToLower(strings.TrimSpace(headerFooterVariant)),
					TableID:             tableID,
					RowIndex:            rowIndex,
					CellIndex:           cellIndex,
				}
			}
		case goword.BlockTypeTable:
			if block.Table == nil {
				continue
			}
			for rIndex, row := range block.Table.Rows {
				for cIndex, cell := range row.Cells {
					r := rIndex
					c := cIndex
					context := wordReaderFindParagraphContextInBlocks(cell.Blocks, trimmedID, region, headerFooterKind, headerFooterVariant, block.Table.ID, &r, &c)
					if context != nil {
						return context
					}
				}
			}
		}
	}
	return nil
}

func wordReaderFindParagraphContext(document *goword.Document, paragraphID string) *wordReaderParagraphContext {
	if document == nil {
		return nil
	}
	if context := wordReaderFindParagraphContextInBlocks(document.Blocks, paragraphID, "body", "", "", "", nil, nil); context != nil {
		return context
	}
	for _, part := range document.Meta.HeaderFooters {
		if context := wordReaderFindParagraphContextInBlocks(part.Blocks, paragraphID, part.Kind, part.Kind, part.Variant, "", nil, nil); context != nil {
			return context
		}
	}
	return nil
}

func wordReaderFindTableContextInBlocks(blocks []goword.Block, tableID, region, headerFooterKind, headerFooterVariant, parentTableID string, parentRowIndex, parentCellIndex *int) *wordReaderTableContext {
	trimmedID := strings.TrimSpace(tableID)
	if trimmedID == "" {
		return nil
	}
	for blockIndex, block := range blocks {
		if block.Type != goword.BlockTypeTable || block.Table == nil {
			continue
		}
		if block.Table.ID == trimmedID {
			return &wordReaderTableContext{
				Table:               *block.Table,
				HostBlocks:          blocks,
				BlockIndex:          blockIndex,
				Region:              wordReaderNormalizeRegion(region, headerFooterKind),
				HeaderFooterKind:    strings.ToLower(strings.TrimSpace(headerFooterKind)),
				HeaderFooterVariant: strings.ToLower(strings.TrimSpace(headerFooterVariant)),
				ParentTableID:       parentTableID,
				ParentRowIndex:      parentRowIndex,
				ParentCellIndex:     parentCellIndex,
			}
		}
		for rIndex, row := range block.Table.Rows {
			for cIndex, cell := range row.Cells {
				r := rIndex
				c := cIndex
				context := wordReaderFindTableContextInBlocks(cell.Blocks, trimmedID, region, headerFooterKind, headerFooterVariant, block.Table.ID, &r, &c)
				if context != nil {
					return context
				}
			}
		}
	}
	return nil
}

func wordReaderFindTableContext(document *goword.Document, tableID string) *wordReaderTableContext {
	if document == nil {
		return nil
	}
	if context := wordReaderFindTableContextInBlocks(document.Blocks, tableID, "body", "", "", "", nil, nil); context != nil {
		return context
	}
	for _, part := range document.Meta.HeaderFooters {
		if context := wordReaderFindTableContextInBlocks(part.Blocks, tableID, part.Kind, part.Kind, part.Variant, "", nil, nil); context != nil {
			return context
		}
	}
	return nil
}

func wordReaderBuildTableRowSnapshot(row goword.TableRow, rowIndex int, focusCellIndex *int) map[string]any {
	cells := make([]map[string]any, 0, len(row.Cells))
	for cellIndex, cell := range row.Cells {
		entry := map[string]any{
			"cell_index":    cellIndex,
			"text":          wordReaderCompactInlineText(wordReaderTableCellText(cell), 160),
			"is_focus_cell": focusCellIndex != nil && *focusCellIndex == cellIndex,
		}
		cells = append(cells, entry)
	}
	return map[string]any{"row_index": rowIndex, "cells": cells}
}

func wordReaderBuildTableExploration(document *goword.Document, tableID string, rowIndex, cellIndex *int, radius int) map[string]any {
	context := wordReaderFindTableContext(document, tableID)
	if context == nil {
		return nil
	}
	rows := context.Table.Rows
	normalizedRowIndex := rowIndex
	normalizedCellIndex := cellIndex
	if normalizedRowIndex != nil && (*normalizedRowIndex < 0 || *normalizedRowIndex >= len(rows)) {
		normalizedRowIndex = nil
	}
	returningFullTable := normalizedRowIndex == nil && normalizedCellIndex == nil
	rowWindowStart := 0
	rowWindowEnd := len(rows)
	if !returningFullTable {
		rowWindowEnd = minInt(len(rows), maxInt(radius*2+1, 1))
	}
	if normalizedRowIndex != nil {
		rowWindowStart = maxInt(0, *normalizedRowIndex-radius)
		rowWindowEnd = minInt(len(rows), *normalizedRowIndex+radius+1)
	}
	rowWindow := make([]map[string]any, 0, rowWindowEnd-rowWindowStart)
	for idx := rowWindowStart; idx < rowWindowEnd; idx++ {
		var focusIndex *int
		if normalizedRowIndex != nil && *normalizedRowIndex == idx {
			focusIndex = normalizedCellIndex
		}
		rowWindow = append(rowWindow, wordReaderBuildTableRowSnapshot(rows[idx], idx, focusIndex))
	}
	columnCells := make([]map[string]any, 0)
	if normalizedCellIndex != nil {
		for idx := rowWindowStart; idx < rowWindowEnd; idx++ {
			if *normalizedCellIndex < 0 || *normalizedCellIndex >= len(rows[idx].Cells) {
				continue
			}
			columnCells = append(columnCells, map[string]any{
				"row_index":     idx,
				"text":          wordReaderCompactInlineText(wordReaderTableCellText(rows[idx].Cells[*normalizedCellIndex]), 160),
				"is_focus_cell": normalizedRowIndex != nil && *normalizedRowIndex == idx,
			})
		}
	}
	var focusCell map[string]any
	if normalizedRowIndex != nil && *normalizedRowIndex >= 0 && *normalizedRowIndex < len(rows) {
		row := rows[*normalizedRowIndex]
		if normalizedCellIndex != nil && *normalizedCellIndex >= 0 && *normalizedCellIndex < len(row.Cells) {
			focusCell = map[string]any{
				"row_index":  *normalizedRowIndex,
				"cell_index": *normalizedCellIndex,
				"text":       wordReaderTableCellText(row.Cells[*normalizedCellIndex]),
			}
			for key, value := range wordReaderBuildTableHitContext(document, tableID, *normalizedRowIndex, *normalizedCellIndex) {
				focusCell[key] = value
			}
		}
	}
	result := map[string]any{
		"table_id":              context.Table.ID,
		"region":                context.Region,
		"header_footer_kind":    wordReaderNilIfEmpty(context.HeaderFooterKind),
		"header_footer_variant": wordReaderNilIfEmpty(context.HeaderFooterVariant),
		"dimensions":            wordReaderTableDimensions(context.Table),
		"full_table":            returningFullTable,
		"focus_row_index":       wordReaderMaybeInt(normalizedRowIndex),
		"focus_cell_index":      wordReaderMaybeInt(normalizedCellIndex),
		"row_window":            rowWindow,
		"adjacent_blocks":       wordReaderBuildNeighborBlocks(context.HostBlocks, context.BlockIndex, radius),
	}
	if context.ParentTableID != "" {
		result["parent_table_id"] = context.ParentTableID
		result["parent_row_index"] = wordReaderMaybeInt(context.ParentRowIndex)
		result["parent_cell_index"] = wordReaderMaybeInt(context.ParentCellIndex)
	}
	if len(columnCells) > 0 {
		result["column_cells"] = columnCells
	}
	if focusCell != nil {
		result["focus_cell"] = focusCell
	}
	return result
}

func wordReaderBuildParagraphExploration(document *goword.Document, paragraphID string, radius int) map[string]any {
	context := wordReaderFindParagraphContext(document, paragraphID)
	if context == nil {
		return nil
	}
	result := map[string]any{
		"paragraph_id":          context.Paragraph.ID,
		"text":                  wordReaderParagraphText(context.Paragraph),
		"region":                context.Region,
		"header_footer_kind":    wordReaderNilIfEmpty(context.HeaderFooterKind),
		"header_footer_variant": wordReaderNilIfEmpty(context.HeaderFooterVariant),
		"paragraph_style":       wordReaderCompactParagraphStyle(context.Paragraph.Style),
		"dominant_text_style":   wordReaderDominantRunStyle(context.Paragraph),
		"adjacent_blocks":       wordReaderBuildNeighborBlocks(context.HostBlocks, context.BlockIndex, radius),
	}
	if context.TableID != "" {
		result["table_id"] = context.TableID
		result["row_index"] = wordReaderMaybeInt(context.RowIndex)
		result["cell_index"] = wordReaderMaybeInt(context.CellIndex)
		result["table_context"] = wordReaderBuildTableExploration(document, context.TableID, context.RowIndex, context.CellIndex, radius)
	}
	return result
}

func wordReaderBuildExploration(document *goword.Document, rawHits []goword.SearchHit, focusHitIndex *int, focusParagraphID, focusTableID string, focusRowIndex, focusCellIndex *int, radius int) any {
	resolvedParagraphID := strings.TrimSpace(focusParagraphID)
	resolvedTableID := strings.TrimSpace(focusTableID)
	resolvedRowIndex := focusRowIndex
	resolvedCellIndex := focusCellIndex
	warnings := make([]string, 0)
	var resolvedHit map[string]any
	if focusHitIndex != nil {
		if *focusHitIndex >= 0 && *focusHitIndex < len(rawHits) {
			hit := rawHits[*focusHitIndex]
			resolvedHit = map[string]any{
				"hit_index":    *focusHitIndex,
				"type":         hit.Type,
				"paragraph_id": wordReaderNilIfEmpty(hit.ParagraphID),
				"table_id":     wordReaderNilIfEmpty(hit.TableID),
				"row_index":    hit.RowIndex,
				"cell_index":   hit.CellIndex,
				"preview":      wordReaderCompactInlineText(hit.Text, 180),
			}
			if resolvedParagraphID == "" {
				resolvedParagraphID = strings.TrimSpace(hit.ParagraphID)
			}
			if resolvedTableID == "" {
				resolvedTableID = strings.TrimSpace(hit.TableID)
			}
			if resolvedRowIndex == nil && hit.TableID != "" {
				value := hit.RowIndex
				resolvedRowIndex = &value
			}
			if resolvedCellIndex == nil && hit.TableID != "" {
				value := hit.CellIndex
				resolvedCellIndex = &value
			}
		} else {
			warnings = append(warnings, fmt.Sprintf("focus_hit_index=%d 超出当前 query hits 范围", *focusHitIndex))
		}
	}
	if resolvedParagraphID == "" && resolvedTableID == "" {
		return nil
	}
	exploration := map[string]any{
		"requested": map[string]any{
			"focus_hit_index":    wordReaderMaybeInt(focusHitIndex),
			"focus_paragraph_id": wordReaderNilIfEmpty(focusParagraphID),
			"focus_table_id":     wordReaderNilIfEmpty(focusTableID),
			"focus_row_index":    wordReaderMaybeInt(resolvedRowIndex),
			"focus_cell_index":   wordReaderMaybeInt(resolvedCellIndex),
			"expand_radius":      radius,
		},
		"resolved": map[string]any{
			"paragraph_id": wordReaderNilIfEmpty(resolvedParagraphID),
			"table_id":     wordReaderNilIfEmpty(resolvedTableID),
			"row_index":    wordReaderMaybeInt(resolvedRowIndex),
			"cell_index":   wordReaderMaybeInt(resolvedCellIndex),
		},
	}
	if resolvedHit != nil {
		exploration["resolved_hit"] = resolvedHit
	}
	if resolvedParagraphID != "" {
		paragraphContext := wordReaderBuildParagraphExploration(document, resolvedParagraphID, radius)
		if paragraphContext != nil {
			exploration["paragraph"] = paragraphContext
			if resolvedTableID == "" {
				if tableID, ok := paragraphContext["table_id"].(string); ok && tableID != "" {
					resolvedTableID = tableID
					exploration["resolved"].(map[string]any)["table_id"] = tableID
					if row, ok := paragraphContext["row_index"].(int); ok {
						resolvedRowIndex = &row
						exploration["resolved"].(map[string]any)["row_index"] = row
					}
					if cell, ok := paragraphContext["cell_index"].(int); ok {
						resolvedCellIndex = &cell
						exploration["resolved"].(map[string]any)["cell_index"] = cell
					}
				}
			}
		} else {
			warnings = append(warnings, fmt.Sprintf("未找到 paragraph_id=%s 对应的上下文", resolvedParagraphID))
		}
	}
	if resolvedTableID != "" {
		tableContext := wordReaderBuildTableExploration(document, resolvedTableID, resolvedRowIndex, resolvedCellIndex, radius)
		if tableContext != nil {
			exploration["table"] = tableContext
		} else {
			warnings = append(warnings, fmt.Sprintf("未找到 table_id=%s 对应的上下文", resolvedTableID))
		}
	}
	if len(warnings) > 0 {
		exploration["warnings"] = warnings
	}
	return exploration
}

func wordReaderBuildLayoutBlockPreview(block goword.LayoutBlock) map[string]any {
	switch block.Type {
	case goword.BlockTypeParagraph:
		if block.Paragraph == nil {
			return nil
		}
		parts := make([]string, 0, len(block.Paragraph.Lines))
		for _, line := range block.Paragraph.Lines {
			trimmed := strings.TrimSpace(line.Text)
			if trimmed != "" {
				parts = append(parts, trimmed)
			}
		}
		return map[string]any{
			"type":         "paragraph",
			"paragraph_id": wordReaderNilIfEmpty(block.Paragraph.ParagraphID),
			"line_count":   maxInt(block.Paragraph.LineCount, len(block.Paragraph.Lines)),
			"preview":      wordReaderCompactInlineText(strings.Join(parts, " "), 160),
		}
	case goword.BlockTypeTable:
		if block.Table == nil {
			return nil
		}
		previewParts := make([]string, 0)
		for _, row := range block.Table.Rows {
			rowCells := make([]string, 0)
			for _, cell := range row.Cells {
				paragraphTexts := make([]string, 0)
				for _, paragraph := range cell.Paragraphs {
					lineTexts := make([]string, 0)
					for _, line := range paragraph.Lines {
						trimmed := strings.TrimSpace(line.Text)
						if trimmed != "" {
							lineTexts = append(lineTexts, trimmed)
						}
					}
					joined := strings.Join(lineTexts, " ")
					if joined != "" {
						paragraphTexts = append(paragraphTexts, joined)
					}
				}
				cellText := wordReaderCompactInlineText(strings.Join(paragraphTexts, " "), 80)
				if cellText != "" {
					rowCells = append(rowCells, cellText)
				}
			}
			if len(rowCells) > 0 {
				previewParts = append(previewParts, strings.Join(rowCells, " | "))
			}
			if len(previewParts) >= 2 {
				break
			}
		}
		return map[string]any{
			"type":      "table",
			"table_id":  wordReaderNilIfEmpty(block.Table.TableID),
			"row_count": block.Table.RowCount,
			"preview":   wordReaderCompactInlineText(strings.Join(previewParts, " / "), 160),
		}
	default:
		return nil
	}
}

func wordReaderLayoutBlockMatchesTarget(block goword.LayoutBlock, paragraphID, tableID string) bool {
	if paragraphID != "" && block.Paragraph != nil && block.Paragraph.ParagraphID == paragraphID {
		return true
	}
	if tableID != "" && block.Table != nil && block.Table.TableID == tableID {
		return true
	}
	if paragraphID != "" && block.Table != nil {
		for _, row := range block.Table.Rows {
			for _, cell := range row.Cells {
				for _, paragraph := range cell.Paragraphs {
					if paragraph.ParagraphID == paragraphID {
						return true
					}
				}
			}
		}
	}
	return false
}

func wordReaderFindLayoutPageNumbers(layout *goword.DocumentLayout, paragraphID, tableID string) []int {
	if layout == nil {
		return nil
	}
	pageNumbers := make([]int, 0)
	seen := make(map[int]struct{})
	for _, page := range layout.Pages {
		for _, block := range page.Blocks {
			if !wordReaderLayoutBlockMatchesTarget(block, paragraphID, tableID) {
				continue
			}
			if _, ok := seen[page.Number]; !ok {
				seen[page.Number] = struct{}{}
				pageNumbers = append(pageNumbers, page.Number)
			}
			break
		}
	}
	return pageNumbers
}

func wordReaderBuildLayoutSummary(layout *goword.DocumentLayout, focusPageIndex *int) map[string]any {
	pageSummaries := make([]map[string]any, 0)
	for _, page := range layout.Pages {
		if len(pageSummaries) >= wordReaderDefaultLayoutPageItems {
			break
		}
		previewBlocks := make([]map[string]any, 0)
		paragraphBlocks := 0
		tableBlocks := 0
		for _, block := range page.Blocks {
			preview := wordReaderBuildLayoutBlockPreview(block)
			if preview == nil {
				continue
			}
			previewBlocks = append(previewBlocks, preview)
			if preview["type"] == "paragraph" {
				paragraphBlocks++
			} else if preview["type"] == "table" {
				tableBlocks++
			}
		}
		if len(previewBlocks) > 3 {
			previewBlocks = previewBlocks[:3]
		}
		pageSummaries = append(pageSummaries, map[string]any{
			"page_index":            page.Index,
			"page_number":           page.Number,
			"block_count":           len(page.Blocks),
			"paragraph_block_count": paragraphBlocks,
			"table_block_count":     tableBlocks,
			"preview_blocks":        previewBlocks,
		})
	}
	var focusPage any
	if focusPageIndex != nil && *focusPageIndex >= 0 && *focusPageIndex < len(layout.Pages) {
		page := layout.Pages[*focusPageIndex]
		blocks := make([]map[string]any, 0)
		for idx, block := range page.Blocks {
			if idx >= wordReaderDefaultLayoutPageBlocks {
				break
			}
			preview := wordReaderBuildLayoutBlockPreview(block)
			if preview != nil {
				blocks = append(blocks, preview)
			}
		}
		focusPage = map[string]any{
			"page_index":  page.Index,
			"page_number": page.Number,
			"block_count": len(page.Blocks),
			"blocks":      blocks,
		}
	}
	return map[string]any{
		"engine": map[string]any{
			"name":    layout.Engine.Name,
			"version": wordReaderNilIfEmpty(layout.Engine.Version),
		},
		"page_spec": map[string]any{
			"width_twips":           layout.PageSpec.WidthTwips,
			"height_twips":          layout.PageSpec.HeightTwips,
			"margin_top_twips":      layout.PageSpec.MarginTopTwips,
			"margin_right_twips":    layout.PageSpec.MarginRightTwips,
			"margin_bottom_twips":   layout.PageSpec.MarginBottomTwips,
			"margin_left_twips":     layout.PageSpec.MarginLeftTwips,
			"header_distance_twips": layout.PageSpec.HeaderDistanceTwips,
			"footer_distance_twips": layout.PageSpec.FooterDistanceTwips,
		},
		"page_count": len(layout.Pages),
		"warnings":   layout.Warnings,
		"pages":      pageSummaries,
		"focus_page": focusPage,
	}
}

func wordReaderBuildLocatorGuidance(stats map[string]int, queryTerms []string, queryHits []map[string]any, exploration, layout any) map[string]any {
	recommendations := make([]string, 0)
	signals := make([]string, 0)
	if len(queryTerms) == 0 && exploration == nil {
		recommendations = append(recommendations, "先提供 query 做首次定位；若用户按页描述目标，可同时传 include_layout=true。")
	}
	if len(queryTerms) > 0 && len(queryHits) == 0 {
		signals = append(signals, "no_hits")
		if stats["table_count"] > 0 {
			recommendations = append(recommendations, "当前 query 没命中；若目标在表格中，优先改用 context_scope=table，并尝试多个候选词。")
		} else {
			recommendations = append(recommendations, "当前 query 没命中；可换同义词，或显式打开 include_outline=true 查看结构后再二次检索。")
		}
	}
	if len(queryHits) > 1 {
		signals = append(signals, "ambiguous_hits")
		recommendations = append(recommendations, fmt.Sprintf("当前有 %d 条命中，优先用 focus_hit_index 继续展开，再决定 paragraph_id/table_id。", len(queryHits)))
	}
	if len(queryHits) == 1 {
		hit := queryHits[0]
		if wordReaderMapString(hit, "table_id") != "" {
			signals = append(signals, "single_table_hit")
			recommendations = append(recommendations, "已锁到单表命中；可直接用 focus_hit_index=0 或 focus_table_id 继续展开整表/目标行列。")
		} else if wordReaderMapString(hit, "paragraph_id") != "" {
			signals = append(signals, "single_paragraph_hit")
			recommendations = append(recommendations, "已锁到单段命中；可直接用 focus_paragraph_id 读取相邻段落与格式依据。")
		}
	}
	if explorationMap, ok := exploration.(map[string]any); ok {
		if warnings, ok := explorationMap["warnings"].([]string); ok && len(warnings) > 0 {
			signals = append(signals, "exploration_warning")
			recommendations = append(recommendations, "exploration 已返回 warning；优先按 warning 调整 focus 参数，不要直接下修改动作。")
		}
	}
	if layoutMap, ok := layout.(map[string]any); ok {
		if pageCount, ok := layoutMap["page_count"].(int); ok && pageCount > 1 {
			recommendations = append(recommendations, "若用户按页描述目标，可结合 layout.pages 与 focus_page_index 做页级消歧。")
		}
	}
	if len(recommendations) > 4 {
		recommendations = recommendations[:4]
	}
	return map[string]any{"signals": signals, "recommendations": recommendations}
}

func wordReaderBuildTableHitContext(document *goword.Document, tableID string, rowIndex, cellIndex int) map[string]any {
	table := wordReaderFindTableByID(document, tableID)
	if table == nil {
		return map[string]any{}
	}
	if rowIndex < 0 || rowIndex >= len(table.Rows) {
		return map[string]any{"table_dimensions": wordReaderTableDimensions(*table)}
	}
	row := table.Rows[rowIndex]
	cellText := ""
	if cellIndex >= 0 && cellIndex < len(row.Cells) {
		cellText = wordReaderCompactInlineText(wordReaderTableCellText(row.Cells[cellIndex]), 48)
	}
	rowCells := make([]map[string]any, 0)
	for idx, rowCell := range row.Cells {
		preview := wordReaderCompactInlineText(wordReaderTableCellText(rowCell), 48)
		if preview == "" {
			continue
		}
		rowCells = append(rowCells, map[string]any{"cell_index": idx, "text": preview})
		if len(rowCells) >= wordReaderDefaultTableContextCells {
			break
		}
	}
	rowHeader := ""
	if cellIndex != 0 && len(row.Cells) > 0 {
		rowHeader = wordReaderCompactInlineText(wordReaderTableCellText(row.Cells[0]), 48)
	}
	columnHeader := ""
	if cellIndex >= 0 && len(table.Rows) > 0 && cellIndex < len(table.Rows[0].Cells) {
		columnHeader = wordReaderCompactInlineText(wordReaderTableCellText(table.Rows[0].Cells[cellIndex]), 48)
	}
	context := map[string]any{
		"table_dimensions": wordReaderTableDimensions(*table),
		"row_preview":      wordReaderRowPreview(rowCells),
		"row_cells":        rowCells,
	}
	if cellText != "" {
		context["cell_text"] = cellText
	}
	if rowHeader != "" && rowHeader != cellText {
		context["row_header"] = rowHeader
	}
	if columnHeader != "" && columnHeader != cellText {
		context["column_header"] = columnHeader
	}
	return context
}

func wordReaderRowPreview(rowCells []map[string]any) string {
	parts := make([]string, 0, len(rowCells))
	for _, cell := range rowCells {
		if text, ok := cell["text"].(string); ok && text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, " | ")
}

func wordReaderFindTableByID(document *goword.Document, tableID string) *goword.Table {
	trimmedID := strings.TrimSpace(tableID)
	if document == nil || trimmedID == "" {
		return nil
	}
	if table := wordReaderFindTableInBlocks(document.Blocks, trimmedID); table != nil {
		return table
	}
	for _, part := range document.Meta.HeaderFooters {
		if table := wordReaderFindTableInBlocks(part.Blocks, trimmedID); table != nil {
			return table
		}
	}
	return nil
}

func wordReaderFindTableInBlocks(blocks []goword.Block, tableID string) *goword.Table {
	for _, block := range blocks {
		if block.Type != goword.BlockTypeTable || block.Table == nil {
			continue
		}
		if block.Table.ID == tableID {
			return block.Table
		}
		for _, row := range block.Table.Rows {
			for _, cell := range row.Cells {
				if table := wordReaderFindTableInBlocks(cell.Blocks, tableID); table != nil {
					return table
				}
			}
		}
	}
	return nil
}

func wordReaderNormalizeRegion(region, headerFooterKind string) string {
	value := strings.ToLower(strings.TrimSpace(region))
	if value == "body" || value == "header" || value == "footer" {
		return value
	}
	kind := strings.ToLower(strings.TrimSpace(headerFooterKind))
	if kind == "header" || kind == "footer" {
		return kind
	}
	return "body"
}

func wordReaderHeaderFooterLabel(kind, variant string) string {
	normalizedKind := strings.ToLower(strings.TrimSpace(kind))
	normalizedVariant := strings.ToLower(strings.TrimSpace(variant))
	if normalizedVariant == "" {
		normalizedVariant = "default"
	}
	kindLabel := "页眉"
	if normalizedKind == "footer" {
		kindLabel = "页脚"
	}
	variantLabel := map[string]string{"first": "首页", "even": "偶数页", "default": "默认"}[normalizedVariant]
	if variantLabel == "" {
		variantLabel = normalizedVariant
	}
	return fmt.Sprintf("[%s%s]", variantLabel, kindLabel)
}

func wordReaderBuildFullText(document *goword.Document) string {
	parts := make([]string, 0)
	appendBlocks := func(blocks []goword.Block, prefix string) {
		segments := make([]string, 0)
		if prefix != "" {
			segments = append(segments, prefix)
		}
		for _, block := range blocks {
			switch block.Type {
			case goword.BlockTypeParagraph:
				if block.Paragraph != nil {
					text := wordReaderParagraphText(*block.Paragraph)
					if text != "" {
						segments = append(segments, text)
					}
				}
			case goword.BlockTypeTable:
				if block.Table != nil {
					text := wordReaderTableText(*block.Table)
					if text != "" {
						segments = append(segments, "[表格]\n"+text)
					}
				}
			}
		}
		if len(segments) > 0 {
			parts = append(parts, strings.Join(segments, "\n\n"))
		}
	}
	appendBlocks(document.Blocks, "")
	for _, part := range document.Meta.HeaderFooters {
		appendBlocks(part.Blocks, wordReaderHeaderFooterLabel(part.Kind, part.Variant))
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func wordReaderHeadingLike(styleID, text string) bool {
	lowered := strings.ToLower(strings.TrimSpace(styleID))
	if strings.HasPrefix(lowered, "heading") || lowered == "title" {
		return true
	}
	trimmed := strings.TrimSpace(text)
	if len([]rune(trimmed)) <= 24 {
		for _, prefix := range []string{"一、", "二、", "三、", "四、", "五、", "1.", "2.", "3."} {
			if strings.HasPrefix(trimmed, prefix) {
				return true
			}
		}
	}
	if wordReaderChapterPattern.MatchString(trimmed) || wordReaderAppendixPattern.MatchString(trimmed) {
		return true
	}
	if matches := wordReaderNumberedPattern.FindStringSubmatch(trimmed); len(matches) == 3 {
		content := strings.TrimSpace(matches[2])
		if content == "" {
			return true
		}
		if !strings.ContainsAny(content, "：:，,。；;！？!?") && len([]rune(content)) <= 20 {
			return true
		}
	}
	return false
}

func wordReaderAppendOutlineItems(items *[]map[string]any, stats map[string]int, blocks []goword.Block, maxItems int, region, headerFooterKind, headerFooterVariant string) {
	for index, block := range blocks {
		switch block.Type {
		case goword.BlockTypeParagraph:
			if block.Paragraph == nil {
				continue
			}
			stats["paragraph_count"]++
			text := wordReaderParagraphText(*block.Paragraph)
			if text == "" {
				continue
			}
			itemType := "paragraph"
			if wordReaderHeadingLike(block.Paragraph.Style.StyleID, text) {
				itemType = "heading"
				stats["heading_count"]++
			}
			if len(*items) >= maxItems {
				continue
			}
			*items = append(*items, map[string]any{
				"index":                 index,
				"type":                  itemType,
				"region":                wordReaderNormalizeRegion(region, headerFooterKind),
				"header_footer_kind":    wordReaderNilIfEmpty(strings.ToLower(strings.TrimSpace(headerFooterKind))),
				"header_footer_variant": wordReaderNilIfEmpty(strings.ToLower(strings.TrimSpace(headerFooterVariant))),
				"paragraph_id":          block.Paragraph.ID,
				"style_id":              wordReaderNilIfEmpty(block.Paragraph.Style.StyleID),
				"paragraph_style":       wordReaderCompactParagraphStyle(block.Paragraph.Style),
				"dominant_text_style":   wordReaderDominantRunStyle(*block.Paragraph),
				"text_length":           len([]rune(text)),
				"preview":               wordReaderCompactInlineText(text, 160),
			})
		case goword.BlockTypeTable:
			if block.Table == nil {
				continue
			}
			stats["table_count"]++
			if len(*items) >= maxItems {
				continue
			}
			*items = append(*items, map[string]any{
				"index":                 index,
				"type":                  "table",
				"region":                wordReaderNormalizeRegion(region, headerFooterKind),
				"header_footer_kind":    wordReaderNilIfEmpty(strings.ToLower(strings.TrimSpace(headerFooterKind))),
				"header_footer_variant": wordReaderNilIfEmpty(strings.ToLower(strings.TrimSpace(headerFooterVariant))),
				"table_id":              block.Table.ID,
				"dimensions":            wordReaderTableDimensions(*block.Table),
				"preview":               wordReaderCompactInlineText(wordReaderTableText(*block.Table), 160),
			})
		}
	}
}

func wordReaderBuildOutline(document *goword.Document, maxItems int) ([]map[string]any, map[string]int) {
	items := make([]map[string]any, 0)
	stats := map[string]int{"paragraph_count": 0, "table_count": 0, "heading_count": 0, "header_footer_count": 0}
	wordReaderAppendOutlineItems(&items, stats, document.Blocks, maxItems, "body", "", "")
	for _, part := range document.Meta.HeaderFooters {
		stats["header_footer_count"]++
		wordReaderAppendOutlineItems(&items, stats, part.Blocks, maxItems, part.Kind, part.Kind, part.Variant)
	}
	return items, stats
}

func wordReaderCollectDocumentStats(document *goword.Document) map[string]int {
	stats := map[string]int{"paragraph_count": 0, "table_count": 0, "heading_count": 0, "header_footer_count": 0}
	var collect func([]goword.Block)
	collect = func(blocks []goword.Block) {
		for _, block := range blocks {
			switch block.Type {
			case goword.BlockTypeParagraph:
				if block.Paragraph == nil {
					continue
				}
				stats["paragraph_count"]++
				text := wordReaderParagraphText(*block.Paragraph)
				if text != "" && wordReaderHeadingLike(block.Paragraph.Style.StyleID, text) {
					stats["heading_count"]++
				}
			case goword.BlockTypeTable:
				if block.Table != nil {
					stats["table_count"]++
				}
			}
		}
	}
	collect(document.Blocks)
	for _, part := range document.Meta.HeaderFooters {
		stats["header_footer_count"]++
		collect(part.Blocks)
	}
	return stats
}

func wordReaderBuildHitParagraphSummary(document *goword.Document, paragraphID string) map[string]any {
	context := wordReaderFindParagraphContext(document, paragraphID)
	if context == nil {
		return nil
	}
	return map[string]any{
		"region":                context.Region,
		"header_footer_kind":    wordReaderNilIfEmpty(context.HeaderFooterKind),
		"header_footer_variant": wordReaderNilIfEmpty(context.HeaderFooterVariant),
		"paragraph_id":          context.Paragraph.ID,
		"style_id":              context.Paragraph.Style.StyleID,
		"paragraph_style":       wordReaderCompactParagraphStyle(context.Paragraph.Style),
		"dominant_text_style":   wordReaderDominantRunStyle(context.Paragraph),
	}
}

func wordReaderComputeFullTextCharCount(document *goword.Document) int {
	return len([]rune(wordReaderBuildFullText(document)))
}

func wordReaderBuildStyleCatalog(document *goword.Document, focusStyleIDs map[string]struct{}) map[string]any {
	rawStyles := document.Meta.Styles
	type styleEntry struct {
		ID        string         `json:"id,omitempty"`
		Name      string         `json:"name,omitempty"`
		Type      string         `json:"type,omitempty"`
		BasedOn   string         `json:"based_on,omitempty"`
		IsDefault bool           `json:"is_default,omitempty"`
		Paragraph map[string]any `json:"paragraph,omitempty"`
		Text      map[string]any `json:"text,omitempty"`
	}
	styles := make([]styleEntry, 0)
	seen := make(map[string]struct{})
	appendStyle := func(item goword.DocumentStyle) {
		key := fmt.Sprintf("%s|%s|%s", item.ID, item.Name, item.Type)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		styles = append(styles, styleEntry{
			ID:        item.ID,
			Name:      item.Name,
			Type:      item.Type,
			BasedOn:   item.BasedOn,
			IsDefault: item.IsDefault,
			Paragraph: wordReaderCompactParagraphStyle(item.Paragraph),
			Text:      wordReaderCompactTextStyle(item.Text),
		})
	}
	focusTokens := make(map[string]struct{})
	for styleID := range focusStyleIDs {
		normalized := wordReaderNormalizeStyleToken(styleID)
		if normalized != "" {
			focusTokens[normalized] = struct{}{}
		}
	}
	styleMatchesFocus := func(item goword.DocumentStyle) bool {
		if len(focusTokens) == 0 {
			return false
		}
		for _, candidate := range []string{item.ID, item.Name, item.BasedOn} {
			normalized := wordReaderNormalizeStyleToken(candidate)
			if normalized == "" {
				continue
			}
			if _, ok := focusTokens[normalized]; ok {
				return true
			}
		}
		return false
	}
	sortedStyles := append([]goword.DocumentStyle(nil), rawStyles...)
	sort.Slice(sortedStyles, func(i, j int) bool {
		left := sortedStyles[i]
		right := sortedStyles[j]
		leftToken := wordReaderNormalizeStyleToken(left.ID)
		rightToken := wordReaderNormalizeStyleToken(right.ID)
		leftName := wordReaderNormalizeStyleToken(left.Name)
		rightName := wordReaderNormalizeStyleToken(right.Name)
		leftCommon := wordReaderStyleIsCommon(leftToken, leftName)
		rightCommon := wordReaderStyleIsCommon(rightToken, rightName)
		if left.IsDefault != right.IsDefault {
			return left.IsDefault
		}
		if strings.ToLower(left.Type) != strings.ToLower(right.Type) {
			return strings.ToLower(left.Type) < strings.ToLower(right.Type)
		}
		if leftCommon != rightCommon {
			return leftCommon
		}
		if leftName != rightName {
			return leftName < rightName
		}
		return leftToken < rightToken
	})
	limit := wordReaderDefaultStyleCatalogItems
	if len(focusTokens) > 0 {
		limit = wordReaderDefaultQueryStyleItems
	}
	for _, item := range rawStyles {
		if styleMatchesFocus(item) {
			appendStyle(item)
		}
	}
	for _, item := range sortedStyles {
		if len(styles) >= limit {
			break
		}
		if len(focusTokens) > 0 {
			itemType := strings.ToLower(strings.TrimSpace(item.Type))
			if itemType == "numbering" || itemType == "table" {
				continue
			}
		}
		appendStyle(item)
	}
	focusIDs := make([]string, 0, len(focusStyleIDs))
	for styleID := range focusStyleIDs {
		if strings.TrimSpace(styleID) != "" {
			focusIDs = append(focusIDs, styleID)
		}
	}
	sort.Strings(focusIDs)
	if len(focusIDs) > 8 {
		focusIDs = focusIDs[:8]
	}
	return map[string]any{
		"default_paragraph_style": wordReaderCompactParagraphStyle(valueOrDefaultParagraphStyle(document.Meta.DefaultParagraphStyle)),
		"default_text_style":      wordReaderCompactTextStyle(valueOrDefaultTextStyle(document.Meta.DefaultTextStyle)),
		"styles":                  styles,
		"summary": map[string]any{
			"strategy":             ternaryString(len(focusTokens) > 0, "query_focused", "compact"),
			"total_style_count":    len(rawStyles),
			"returned_style_count": len(styles),
			"focus_style_ids":      focusIDs,
		},
	}
}

func valueOrDefaultParagraphStyle(style *goword.ParagraphStyle) goword.ParagraphStyle {
	if style == nil {
		return goword.ParagraphStyle{}
	}
	return *style
}

func valueOrDefaultTextStyle(style *goword.TextStyle) goword.TextStyle {
	if style == nil {
		return goword.TextStyle{}
	}
	return *style
}

func wordReaderNormalizeStyleToken(value string) string {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	for _, r := range trimmed {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
		}
	}
	return builder.String()
}

func wordReaderStyleIsCommon(styleIDToken, nameToken string) bool {
	_, ok := wordReaderCommonStyles[styleIDToken]
	if ok {
		return true
	}
	_, ok = wordReaderCommonStyles[nameToken]
	return ok
}

func wordReaderNilIfEmpty(value string) any {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	return trimmed
}

func wordReaderMaybeInt(value *int) any {
	if value == nil {
		return nil
	}
	return *value
}

func wordReaderMapString(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, ok := values[key]
	if !ok || value == nil {
		return ""
	}
	stringValue, _ := value.(string)
	return stringValue
}

func appendUniqueString(items []string, value string) []string {
	for _, item := range items {
		if item == value {
			return items
		}
	}
	return append(items, value)
}

func ternaryString(condition bool, trueValue, falseValue string) string {
	if condition {
		return trueValue
	}
	return falseValue
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
