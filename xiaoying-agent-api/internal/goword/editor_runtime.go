package goword

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type EditorAction struct {
	Action               string                `json:"action"`
	TargetText           string                `json:"target_text"`
	TargetSelector       *ActionTargetSelector `json:"target_selector,omitempty"`
	PreferredParagraphID string                `json:"preferred_paragraph_id,omitempty"`
	TableID              string                `json:"table_id,omitempty"`
	RowIndex             int                   `json:"row_index,omitempty"`
	CellIndex            int                   `json:"cell_index,omitempty"`
	StylePatch           TextStyle             `json:"style_patch,omitempty"`
	ParagraphStylePatch  ParagraphStyle        `json:"paragraph_style_patch,omitempty"`
	TableSpec            *TableInput           `json:"table_spec,omitempty"`
	TableRow             *TableRowInput        `json:"table_row,omitempty"`
	TableColumn          *TableColumnInput     `json:"table_column,omitempty"`
	TableCell            *TableCellInput       `json:"table_cell,omitempty"`
	HeaderFooter         *HeaderFooterInput    `json:"header_footer,omitempty"`
	SectionLayoutPatch   SectionLayout         `json:"section_layout_patch,omitempty"`
	NewText              string                `json:"new_text,omitempty"`
	Position             string                `json:"position,omitempty"`
	EnableTrackChanges   bool                  `json:"enable_track_changes,omitempty"`
	EditIntent           string                `json:"edit_intent,omitempty"`
	ContextScope         string                `json:"context_scope,omitempty"`
	Constraints          *ActionConstraints    `json:"constraints,omitempty"`
	FailurePolicy        *ActionFailurePolicy  `json:"failure_policy,omitempty"`
	PreviewOnly          bool                  `json:"preview_only,omitempty"`
	RetrievalQuery       string                `json:"retrieval_query,omitempty"`
	RequireRetrieval     bool                  `json:"require_retrieval,omitempty"`
	ReaderSessionID      string                `json:"reader_session_id,omitempty"`
	ReaderContextSummary string                `json:"reader_context_summary,omitempty"`
	FormatBasis          string                `json:"format_basis,omitempty"`
	ExplorationSummary   string                `json:"exploration_summary,omitempty"`
}

type ActionTargetSelector struct {
	Type         string `json:"type"`
	ParagraphID  string `json:"paragraph_id,omitempty"`
	TableID      string `json:"table_id,omitempty"`
	RowIndex     int    `json:"row_index,omitempty"`
	CellIndex    int    `json:"cell_index,omitempty"`
	HeadingLevel int    `json:"heading_level,omitempty"`
	Ordinal      int    `json:"ordinal,omitempty"`
	Chapter      string `json:"chapter,omitempty"`
	TextEquals   string `json:"text_equals,omitempty"`
	Region       string `json:"region,omitempty"`
}

type ActionConstraints struct {
	RawText           string `json:"raw_text,omitempty"`
	MustBeUnique      *bool  `json:"must_be_unique,omitempty"`
	PreserveStyle     *bool  `json:"preserve_style,omitempty"`
	PreserveNumbering *bool  `json:"preserve_numbering,omitempty"`
}

type ActionFailurePolicy struct {
	OnAmbiguous        string `json:"on_ambiguous,omitempty"`
	OnValidationFailed string `json:"on_validation_failed,omitempty"`
}

func (constraints *ActionConstraints) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*constraints = ActionConstraints{}
		return nil
	}
	if strings.HasPrefix(trimmed, "\"") {
		var raw string
		if err := json.Unmarshal(data, &raw); err != nil {
			return err
		}
		*constraints = ActionConstraints{RawText: strings.TrimSpace(raw)}
		return nil
	}
	type alias ActionConstraints
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	decoded.RawText = strings.TrimSpace(decoded.RawText)
	*constraints = ActionConstraints(decoded)
	return nil
}

func (constraints ActionConstraints) MarshalJSON() ([]byte, error) {
	normalized := normalizeActionConstraints(&constraints)
	if normalized == nil {
		return []byte("null"), nil
	}
	if !normalized.hasStructuredFields() && normalized.RawText != "" {
		return json.Marshal(normalized.RawText)
	}
	type alias ActionConstraints
	return json.Marshal(alias(*normalized))
}

func (constraints *ActionConstraints) hasStructuredFields() bool {
	if constraints == nil {
		return false
	}
	return constraints.MustBeUnique != nil || constraints.PreserveStyle != nil || constraints.PreserveNumbering != nil
}

func (constraints *ActionConstraints) preserveStyleEnabled() bool {
	if constraints == nil || constraints.PreserveStyle == nil {
		return true
	}
	return *constraints.PreserveStyle
}

func (constraints *ActionConstraints) preserveNumberingEnabled() bool {
	if constraints == nil || constraints.PreserveNumbering == nil {
		return true
	}
	return *constraints.PreserveNumbering
}

func actionMustBeUnique(action EditorAction) bool {
	return action.Constraints != nil && action.Constraints.MustBeUnique != nil && *action.Constraints.MustBeUnique
}

func actionAmbiguousPolicy(action EditorAction) string {
	if action.FailurePolicy == nil || strings.TrimSpace(action.FailurePolicy.OnAmbiguous) == "" {
		return "return_candidates"
	}
	return action.FailurePolicy.OnAmbiguous
}

func allowBestCandidateResolution(action EditorAction) bool {
	return !actionMustBeUnique(action) && actionAmbiguousPolicy(action) == "use_best_candidate"
}

func shouldReturnAmbiguityCandidates(action EditorAction) bool {
	return actionAmbiguousPolicy(action) != "return_error"
}

type IRDocument struct {
	DocumentID string          `json:"document_id"`
	Name       string          `json:"name"`
	Revision   int64           `json:"revision"`
	Nodes      []IRNode        `json:"nodes"`
	Outline    []IROutlineNode `json:"outline,omitempty"`
}

type IRTextRange struct {
	Start int `json:"start"`
	End   int `json:"end"`
}

type IRNode struct {
	NodeID             string       `json:"node_id"`
	Type               string       `json:"type"`
	Region             string       `json:"region,omitempty"`
	Path               []string     `json:"path,omitempty"`
	ParentNodeID       string       `json:"parent_node_id,omitempty"`
	ChildNodeIDs       []string     `json:"child_node_ids,omitempty"`
	HeadingScope       []string     `json:"heading_scope,omitempty"`
	ParagraphID        string       `json:"paragraph_id,omitempty"`
	TableID            string       `json:"table_id,omitempty"`
	RowIndex           int          `json:"row_index,omitempty"`
	CellIndex          int          `json:"cell_index,omitempty"`
	HeadingLevel       int          `json:"heading_level,omitempty"`
	StyleID            string       `json:"style_id,omitempty"`
	StyleSignature     string       `json:"style_signature,omitempty"`
	NumberingText      string       `json:"numbering_text,omitempty"`
	NumberingSignature string       `json:"numbering_signature,omitempty"`
	SectionSignature   string       `json:"section_signature,omitempty"`
	Text               string       `json:"text,omitempty"`
	CharRange          *IRTextRange `json:"char_range,omitempty"`
	Preview            string       `json:"preview,omitempty"`
}

type IROutlineNode struct {
	ParagraphID   string   `json:"paragraph_id,omitempty"`
	Text          string   `json:"text,omitempty"`
	HeadingLevel  int      `json:"heading_level,omitempty"`
	Path          []string `json:"path,omitempty"`
	HeadingScope  []string `json:"heading_scope,omitempty"`
	StyleID       string   `json:"style_id,omitempty"`
	NumberingText string   `json:"numbering_text,omitempty"`
}

type IRQueryRequest struct {
	Query        string `json:"query,omitempty"`
	Type         string `json:"type,omitempty"`
	Region       string `json:"region,omitempty"`
	HeadingScope string `json:"heading_scope,omitempty"`
	NodeID       string `json:"node_id,omitempty"`
	ParagraphID  string `json:"paragraph_id,omitempty"`
	TableID      string `json:"table_id,omitempty"`
	Limit        int    `json:"limit,omitempty"`
}

type IRQueryResponse struct {
	DocumentID   string   `json:"document_id"`
	Revision     int64    `json:"revision"`
	Query        string   `json:"query,omitempty"`
	Type         string   `json:"type,omitempty"`
	Region       string   `json:"region,omitempty"`
	HeadingScope string   `json:"heading_scope,omitempty"`
	MatchCount   int      `json:"match_count"`
	Nodes        []IRNode `json:"nodes"`
}

type ActionCandidate struct {
	TargetKind   string   `json:"target_kind,omitempty"`
	ParagraphID  string   `json:"paragraph_id,omitempty"`
	TableID      string   `json:"table_id,omitempty"`
	RowIndex     int      `json:"row_index,omitempty"`
	CellIndex    int      `json:"cell_index,omitempty"`
	Region       string   `json:"region,omitempty"`
	Path         []string `json:"path,omitempty"`
	HeadingScope []string `json:"heading_scope,omitempty"`
	Preview      string   `json:"preview,omitempty"`
	MatchedText  string   `json:"matched_text,omitempty"`
	MatchStart   int      `json:"match_start,omitempty"`
	MatchEnd     int      `json:"match_end,omitempty"`
	Score        int      `json:"score,omitempty"`
}

type ActionDiagnostics struct {
	ErrorCode           string               `json:"error_code,omitempty"`
	Message             string               `json:"message,omitempty"`
	TargetKind          string               `json:"target_kind,omitempty"`
	Query               string               `json:"query,omitempty"`
	CandidateCount      int                  `json:"candidate_count,omitempty"`
	Candidates          []ActionCandidate    `json:"candidates,omitempty"`
	Scope               string               `json:"scope,omitempty"`
	AttemptedStrategies []string             `json:"attempted_strategies,omitempty"`
	RecommendedActions  []string             `json:"recommended_actions,omitempty"`
	Retryable           bool                 `json:"retryable,omitempty"`
	RetryAction         *EditorAction        `json:"retry_action,omitempty"`
	RetryPrompt         string               `json:"retry_prompt,omitempty"`
	RecoveryPlan        *FailureRecoveryPlan `json:"recovery_plan,omitempty"`
}

type ResolvedTarget struct {
	TargetKind   string   `json:"target_kind,omitempty"`
	ParagraphID  string   `json:"paragraph_id,omitempty"`
	TableID      string   `json:"table_id,omitempty"`
	RowIndex     int      `json:"row_index,omitempty"`
	CellIndex    int      `json:"cell_index,omitempty"`
	Region       string   `json:"region,omitempty"`
	Path         []string `json:"path,omitempty"`
	HeadingScope []string `json:"heading_scope,omitempty"`
	MatchStart   int      `json:"match_start,omitempty"`
	MatchEnd     int      `json:"match_end,omitempty"`
	Preview      string   `json:"preview,omitempty"`
}

type ActionPlanResult struct {
	Status            string             `json:"status"`
	StatusText        string             `json:"status_text,omitempty"`
	ExpectedRevision  int64              `json:"expected_revision,omitempty"`
	Operations        []Operation        `json:"operations,omitempty"`
	ActiveParagraphID string             `json:"active_paragraph_id,omitempty"`
	ReplayAction      EditorAction       `json:"replay_action"`
	ResolvedTarget    *ResolvedTarget    `json:"resolved_target,omitempty"`
	Diagnostics       *ActionDiagnostics `json:"diagnostics,omitempty"`
	AnchorHealth      *AnchorHealth      `json:"anchor_health,omitempty"`
}

type ActionValidation struct {
	OK          bool               `json:"ok"`
	Message     string             `json:"message,omitempty"`
	ErrorCode   string             `json:"error_code,omitempty"`
	Diagnostics *ActionDiagnostics `json:"diagnostics,omitempty"`
}

type ActionExecuteResult struct {
	Status            string             `json:"status"`
	StatusText        string             `json:"status_text,omitempty"`
	Document          *Document          `json:"document,omitempty"`
	Diffs             []ParagraphDiff    `json:"diffs,omitempty"`
	ActiveParagraphID string             `json:"active_paragraph_id,omitempty"`
	ReplayAction      EditorAction       `json:"replay_action"`
	Plan              *ActionPlanResult  `json:"plan,omitempty"`
	Validation        ActionValidation   `json:"validation"`
	Diagnostics       *ActionDiagnostics `json:"diagnostics,omitempty"`
	AnchorHealth      *AnchorHealth      `json:"anchor_health,omitempty"`
}

type resolvedParagraphTarget struct {
	paragraph  *Paragraph
	hit        *SearchHit
	rangeStart int
	rangeEnd   int
	resolved   ResolvedTarget
}

func (s *Service) BuildIR(sessionID string) (*IRDocument, error) {
	snapshot, err := s.Snapshot(sessionID)
	if err != nil {
		return nil, err
	}
	if snapshot == nil || snapshot.Document == nil {
		return nil, fmt.Errorf("document is nil")
	}
	return BuildDocumentIR(snapshot.Document), nil
}

func (s *Service) QueryIR(sessionID string, req IRQueryRequest) (*IRQueryResponse, error) {
	ir, err := s.BuildIR(sessionID)
	if err != nil {
		return nil, err
	}
	return QueryDocumentIR(ir, req), nil
}

func BuildDocumentIR(document *Document) *IRDocument {
	if document == nil {
		return nil
	}
	document.ensureBlockStructure()
	ir := &IRDocument{
		DocumentID: document.ID,
		Name:       document.Name,
		Revision:   document.Revision,
		Nodes:      make([]IRNode, 0),
		Outline:    make([]IROutlineNode, 0),
	}
	appendIRNodesFromBlocks(&ir.Nodes, &ir.Outline, document, document.Blocks, "body", []string{"body"}, nil, "")
	for partIndex, part := range document.Meta.HeaderFooters {
		region := strings.TrimSpace(part.Kind)
		if region == "" {
			region = "header_footer"
		}
		appendIRNodesFromBlocks(&ir.Nodes, &ir.Outline, document, part.Blocks, region, []string{region, fmt.Sprintf("part_%d", partIndex)}, nil, "")
	}
	return ir
}

func QueryDocumentIR(ir *IRDocument, req IRQueryRequest) *IRQueryResponse {
	if ir == nil {
		return nil
	}
	normalized := normalizeIRQueryRequest(req)
	response := &IRQueryResponse{
		DocumentID:   ir.DocumentID,
		Revision:     ir.Revision,
		Query:        normalized.Query,
		Type:         normalized.Type,
		Region:       normalized.Region,
		HeadingScope: normalized.HeadingScope,
		Nodes:        make([]IRNode, 0),
	}
	matched := make([]IRNode, 0)
	for _, node := range ir.Nodes {
		if !irNodeMatchesQuery(node, normalized) {
			continue
		}
		matched = append(matched, node)
	}
	response.MatchCount = len(matched)
	if normalized.Limit > 0 && len(matched) > normalized.Limit {
		matched = matched[:normalized.Limit]
	}
	response.Nodes = matched
	return response
}

func (s *Service) PlanAction(sessionID string, action EditorAction) (*ActionPlanResult, error) {
	snapshot, err := s.Snapshot(sessionID)
	if err != nil {
		return nil, err
	}
	if snapshot == nil || snapshot.Document == nil {
		return nil, fmt.Errorf("document is nil")
	}
	plan := buildActionPlan(snapshot.Document, action)
	if plan.ExpectedRevision == 0 {
		plan.ExpectedRevision = snapshot.Document.Revision
	}
	plan.AnchorHealth = diagnoseAnchorHealth(action, plan)
	return finalizeActionPlanResult(plan), nil
}

func (s *Service) ExecutePlannedAction(sessionID string, action EditorAction) (*ActionExecuteResult, error) {
	plan, err := s.PlanAction(sessionID, action)
	if err != nil {
		return nil, err
	}
	anchorHealth := cloneAnchorHealth(plan.AnchorHealth)
	if plan.Status == "failed" {
		return finalizeActionExecuteResult(&ActionExecuteResult{
			Status:       "failed",
			StatusText:   plan.StatusText,
			ReplayAction: plan.ReplayAction,
			Plan:         plan,
			Diagnostics:  plan.Diagnostics,
			AnchorHealth: anchorHealth,
			Validation: ActionValidation{
				OK:          false,
				Message:     diagnosticMessage(plan.Diagnostics, plan.StatusText),
				ErrorCode:   diagnosticCode(plan.Diagnostics),
				Diagnostics: plan.Diagnostics,
			},
		}), nil
	}
	if plan.Status == "noop" {
		return finalizeActionExecuteResult(&ActionExecuteResult{
			Status:            "noop",
			StatusText:        plan.StatusText,
			ActiveParagraphID: plan.ActiveParagraphID,
			ReplayAction:      plan.ReplayAction,
			Plan:              plan,
			AnchorHealth:      anchorHealth,
			Validation:        ActionValidation{OK: true, Message: plan.StatusText},
		}), nil
	}
	if action.PreviewOnly {
		return finalizeActionExecuteResult(&ActionExecuteResult{
			Status:            "preview",
			StatusText:        plan.StatusText,
			ActiveParagraphID: plan.ActiveParagraphID,
			ReplayAction:      plan.ReplayAction,
			Plan:              plan,
			AnchorHealth:      anchorHealth,
			Validation:        ActionValidation{OK: true, Message: plan.StatusText},
		}), nil
	}
	result, err := s.ApplyWithOptions(sessionID, plan.Operations, ApplyOptions{ExpectedRevision: plan.ExpectedRevision})
	if err != nil {
		diagnostics := buildExecutionDiagnostics(err, plan)
		if recovered := s.attemptExecuteRecovery(sessionID, action, plan, diagnostics, anchorHealth); recovered != nil {
			return finalizeActionExecuteResult(recovered), nil
		}
		return finalizeActionExecuteResult(&ActionExecuteResult{
			Status:       "failed",
			StatusText:   err.Error(),
			ReplayAction: plan.ReplayAction,
			Plan:         plan,
			Diagnostics:  diagnostics,
			AnchorHealth: anchorHealth,
			Validation:   ActionValidation{OK: false, Message: err.Error(), ErrorCode: diagnosticCode(diagnostics), Diagnostics: diagnostics},
		}), nil
	}
	activeParagraphID := plan.ActiveParagraphID
	if activeParagraphID == "" {
		activeParagraphID = resolveFocusParagraphID(result.Diffs, "")
	}
	validation := ValidateActionResult(result, plan.ReplayAction, activeParagraphID)
	if !validation.OK && plan.ReplayAction.FailurePolicy != nil && plan.ReplayAction.FailurePolicy.OnValidationFailed == "rollback_and_report" {
		rolledBackDocument, rollbackErr := s.RollbackToPrevious(sessionID)
		rollbackMessage := validation.Message
		if rollbackErr != nil {
			rollbackMessage = fmt.Sprintf("%s；尝试回滚到执行前版本失败：%v", validation.Message, rollbackErr)
		} else {
			rollbackMessage = fmt.Sprintf("%s；已按 failure_policy 回滚到执行前版本", validation.Message)
		}
		diagnostics := mergeValidationDiagnostics(validation.Diagnostics, rollbackMessage)
		validation.Message = rollbackMessage
		validation.Diagnostics = diagnostics
		return finalizeActionExecuteResult(&ActionExecuteResult{
			Status:            "failed",
			StatusText:        rollbackMessage,
			Document:          rolledBackDocument,
			ActiveParagraphID: activeParagraphID,
			ReplayAction:      plan.ReplayAction,
			Plan:              plan,
			Validation:        validation,
			Diagnostics:       diagnostics,
			AnchorHealth:      anchorHealth,
		}), nil
	}
	status := "applied"
	statusText := plan.StatusText
	if !validation.OK {
		status = "applied_with_warning"
		statusText = buildValidationWarningStatus(plan.StatusText, validation.Message)
	}
	return finalizeActionExecuteResult(&ActionExecuteResult{
		Status:            status,
		StatusText:        statusText,
		Document:          result.Document,
		Diffs:             result.Diffs,
		ActiveParagraphID: activeParagraphID,
		ReplayAction:      plan.ReplayAction,
		Plan:              plan,
		Validation:        validation,
		AnchorHealth:      anchorHealth,
	}), nil
}

func buildValidationWarningStatus(planStatusText string, validationMessage string) string {
	trimmedValidationMessage := strings.TrimSpace(validationMessage)
	if trimmedValidationMessage == "" {
		trimmedValidationMessage = "自动验证未通过，请人工复核"
	}
	trimmedPlanStatusText := strings.TrimSpace(planStatusText)
	if trimmedPlanStatusText == "" {
		return trimmedValidationMessage
	}
	return fmt.Sprintf("已尝试执行修改，但自动验证未通过：%s", trimmedValidationMessage)
}

func ValidateActionResult(result *ApplyResult, action EditorAction, activeParagraphID string) ActionValidation {
	if result == nil || result.Document == nil {
		return validationFailure(action, "缺少执行结果")
	}
	relevantDiff := firstRelevantDiff(result.Diffs, activeParagraphID)
	relevantParagraph := locateParagraphByID(result.Document, activeParagraphID)
	afterText := ""
	beforeText := ""
	if relevantDiff != nil {
		afterText = relevantDiff.AfterText
		beforeText = relevantDiff.BeforeText
	}
	if afterText == "" && relevantParagraph != nil {
		afterText = relevantParagraph.PlainText()
	}
	normalizedTarget := normalizeEditorText(action.TargetText)
	normalizedNew := normalizeEditorText(action.NewText)
	normalizedAfter := normalizeEditorText(afterText)
	normalizedBefore := normalizeEditorText(beforeText)

	switch action.Action {
	case "insert_line_break":
		if relevantDiff != nil && hasInsertedCharacter(relevantDiff.CharacterRanges, "\n") {
			return ActionValidation{OK: true, Message: "自动验证通过：已插入行内换行"}
		}
		return validationFailure(action, "自动验证未通过：未确认行内换行结果，建议自动重读文档后重规划")
	case "insert_paragraph_break":
		insertedParagraph := false
		for _, diff := range result.Diffs {
			if diff.Type == DiffInserted {
				insertedParagraph = true
				break
			}
		}
		if relevantDiff != nil && hasInsertedCharacter(relevantDiff.CharacterRanges, "\n") && insertedParagraph {
			return ActionValidation{OK: true, Message: "自动验证通过：已插入段落断开并生成新段落"}
		}
		return validationFailure(action, "自动验证未通过：未确认段落断开结果，建议自动重读文档后重规划")
	case "replace":
		if validateTextTransitionByDiff(relevantDiff, action.TargetText, action.NewText) {
			return ActionValidation{OK: true, Message: "自动验证通过：目标范围已按计划替换"}
		}
		if normalizedNew != "" && strings.Contains(normalizedAfter, normalizedNew) {
			return ActionValidation{OK: true, Message: "自动验证通过：新内容已写入文档"}
		}
		if normalizedTarget != "" && strings.Contains(normalizedBefore, normalizedTarget) && !strings.Contains(normalizedAfter, normalizedTarget) {
			return ActionValidation{OK: true, Message: "自动验证通过：原内容已被替换"}
		}
		return validationFailure(action, "自动验证未通过：未确认替换结果，建议自动重读文档后重规划")
	case "delete":
		if validateTextTransitionByDiff(relevantDiff, action.TargetText, "") {
			return ActionValidation{OK: true, Message: "自动验证通过：目标范围已按计划删除"}
		}
		if normalizedTarget != "" && strings.Contains(normalizedBefore, normalizedTarget) && !strings.Contains(normalizedAfter, normalizedTarget) {
			return ActionValidation{OK: true, Message: "自动验证通过：目标内容已删除"}
		}
		return validationFailure(action, "自动验证未通过：未确认删除结果，建议自动重读文档后重规划")
	case "insert":
		if validateInsertedParagraphStructure(result.Document, action, activeParagraphID) {
			return ActionValidation{OK: true, Message: "自动验证通过：新增段落已按计划落在目标位置"}
		}
		allText := normalizeEditorText(joinParagraphTexts(result.Document.Paragraphs))
		if normalizedNew != "" && strings.Contains(allText, normalizedNew) {
			return ActionValidation{OK: true, Message: "自动验证通过：新增内容已出现在文档中"}
		}
		return validationFailure(action, "自动验证未通过：未确认插入结果，建议自动重读文档后重规划")
	case "insert_table":
		if validateInsertedTableStructure(result.Document, action, activeParagraphID) {
			return ActionValidation{OK: true, Message: "自动验证通过：新表格已按计划插入到目标位置"}
		}
		if hasDiffType(result.Diffs, DiffInserted) {
			return ActionValidation{OK: true, Message: "自动验证通过：表格内容已插入文档"}
		}
		return validationFailure(action, "自动验证未通过：未确认表格插入结果，建议自动重读文档后重规划")
	case "insert_table_row", "insert_table_column":
		if hasDiffType(result.Diffs, DiffInserted) {
			return ActionValidation{OK: true, Message: "自动验证通过：表格结构已新增"}
		}
		return validationFailure(action, "自动验证未通过：未确认表格结构新增结果，建议自动重读文档后重规划")
	case "delete_table_row", "delete_table_column":
		if hasDiffType(result.Diffs, DiffDeleted) {
			return ActionValidation{OK: true, Message: "自动验证通过：表格结构已删除"}
		}
		return validationFailure(action, "自动验证未通过：未确认表格结构删除结果，建议自动重读文档后重规划")
	case "update_table_cell":
		if validateTableCellStructure(result.Document, action) {
			return ActionValidation{OK: true, Message: "自动验证通过：目标单元格内容与计划一致"}
		}
		if hasDiffType(result.Diffs, DiffUpdated) || hasDiffType(result.Diffs, DiffInserted) {
			return ActionValidation{OK: true, Message: "自动验证通过：单元格内容已更新"}
		}
		return validationFailure(action, "自动验证未通过：未确认单元格更新结果，建议自动重读文档后重规划")
	case "format":
		if relevantDiff != nil && relevantDiff.Type == DiffUpdated {
			return ActionValidation{OK: true, Message: "自动验证通过：目标文本样式已更新"}
		}
		return validationFailure(action, "自动验证未通过：未确认样式修改结果，建议自动重读文档后重规划")
	case "format_paragraph":
		if relevantDiff != nil && relevantDiff.Type == DiffUpdated {
			return ActionValidation{OK: true, Message: "自动验证通过：段落样式已更新"}
		}
		return validationFailure(action, "自动验证未通过：未确认段落样式修改结果，建议自动重读文档后重规划")
	case "format_section":
		if validateSectionLayoutStructure(result.Document, action.SectionLayoutPatch) {
			return ActionValidation{OK: true, Message: "自动验证通过：页面版式结构已按计划更新"}
		}
		return validationFailure(action, "自动验证未通过：未确认页面版式结构结果，建议自动重读文档后重规划")
	case "update_header_footer":
		if validateHeaderFooterStructure(result.Document, action.HeaderFooter) {
			return ActionValidation{OK: true, Message: "自动验证通过：页眉页脚结构已按计划更新"}
		}
		if findHeaderFooterMatch(result.Document, action.HeaderFooter) != nil {
			return ActionValidation{OK: true, Message: "自动验证通过：页眉页脚内容已更新"}
		}
		return validationFailure(action, "自动验证未通过：未确认页眉页脚更新结果，建议自动重读文档后重规划")
	default:
		return ActionValidation{OK: true, Message: "无需额外验证"}
	}
}

func buildActionPlan(document *Document, action EditorAction) *ActionPlanResult {
	if document == nil {
		return &ActionPlanResult{Status: "failed", ReplayAction: action, Diagnostics: &ActionDiagnostics{ErrorCode: "DOCUMENT_NOT_FOUND", Message: "文档快照为空"}}
	}
	document.ensureBlockStructure()
	action = normalizeEditorAction(action)
	plan := &ActionPlanResult{Status: "ready", ReplayAction: action, ExpectedRevision: document.Revision}

	switch action.Action {
	case "scroll":
		resolved, diagnostics := resolveParagraphTarget(document, action, false)
		if diagnostics != nil && resolved == nil {
			plan.Status = "failed"
			plan.StatusText = diagnosticMessage(diagnostics, "定位失败")
			plan.Diagnostics = diagnostics
			return plan
		}
		if resolved != nil {
			plan.ActiveParagraphID = resolved.paragraph.ID
			plan.ResolvedTarget = &resolved.resolved
			plan.ReplayAction = canonicalizeReplayActionTarget(action, plan.ResolvedTarget)
			plan.Status = "noop"
			plan.StatusText = fmt.Sprintf("已定位到“%s”所在段落", action.TargetText)
			return plan
		}
		plan.Status = "noop"
		plan.StatusText = fmt.Sprintf("未找到“%s”", action.TargetText)
		return plan
	case "format_section":
		if !hasAnySectionLayoutPatch(action.SectionLayoutPatch) {
			return failedPlan(action, "INVALID_ACTION", "页面版式修改缺少 section_layout_patch", "section")
		}
		if diagnostics := preflightSectionLayoutAction(document, action); diagnostics != nil {
			plan.Status = "failed"
			plan.StatusText = diagnosticMessage(diagnostics, "页面版式预检失败")
			plan.Diagnostics = diagnostics
			return plan
		}
		plan.Operations = []Operation{{Type: OperationUpdateSectionLayout, SectionLayoutPatch: action.SectionLayoutPatch, TrackChanges: action.EnableTrackChanges}}
		plan.StatusText = fmt.Sprintf("已更新文档的%s", describeSectionLayoutPatch(action.SectionLayoutPatch))
		return plan
	case "update_header_footer":
		if action.HeaderFooter == nil || !hasHeaderFooterInput(action.HeaderFooter) {
			return failedPlan(action, "INVALID_ACTION", "页眉页脚修改缺少 header_footer", "header_footer")
		}
		if diagnostics := preflightHeaderFooterAction(document, action); diagnostics != nil {
			plan.Status = "failed"
			plan.StatusText = diagnosticMessage(diagnostics, "页眉页脚预检失败")
			plan.Diagnostics = diagnostics
			return plan
		}
		plan.Operations = []Operation{{Type: OperationUpdateHeaderFooter, HeaderFooter: action.HeaderFooter, TrackChanges: action.EnableTrackChanges}}
		plan.StatusText = fmt.Sprintf("已更新%s", describeHeaderFooterInput(action.HeaderFooter))
		return plan
	case "insert_table_row", "delete_table_row", "insert_table_column", "delete_table_column", "update_table_cell":
		resolved, diagnostics := resolveTableTarget(document, action)
		if diagnostics != nil {
			plan.Status = "failed"
			plan.StatusText = diagnosticMessage(diagnostics, "表格定位失败")
			plan.Diagnostics = diagnostics
			return plan
		}
		plan.ResolvedTarget = &resolved.resolved
		plan.ActiveParagraphID = resolved.resolved.ParagraphID
		if diagnostics := preflightTableStructureAction(document, action, resolved.table); diagnostics != nil {
			plan.Status = "failed"
			plan.StatusText = diagnosticMessage(diagnostics, "表格结构预检失败")
			plan.Diagnostics = diagnostics
			return plan
		}
		replayAction := canonicalizeReplayActionTarget(action, plan.ResolvedTarget)
		plan.ReplayAction = replayAction
		switch action.Action {
		case "insert_table_row":
			if action.TableRow == nil || len(action.TableRow.Cells) == 0 {
				return failedPlan(action, "INVALID_ACTION", "插入表格行缺少 table_row", "table")
			}
			plan.Operations = []Operation{{Type: OperationInsertTableRow, TableID: resolved.table.ID, RowIndex: resolved.rowIndex, Position: normalizedPosition(action.Position), TableRow: action.TableRow, TrackChanges: action.EnableTrackChanges}}
			plan.StatusText = fmt.Sprintf("已在表格 %s 的第 %d 行%s插入新行", resolved.table.ID, resolved.rowIndex+1, beforeAfterLabel(action.Position))
		case "delete_table_row":
			plan.Operations = []Operation{{Type: OperationDeleteTableRow, TableID: resolved.table.ID, RowIndex: resolved.rowIndex, TrackChanges: action.EnableTrackChanges}}
			plan.StatusText = fmt.Sprintf("已删除表格 %s 的第 %d 行", resolved.table.ID, resolved.rowIndex+1)
		case "insert_table_column":
			if action.TableColumn == nil {
				return failedPlan(action, "INVALID_ACTION", "插入表格列缺少 table_column", "table")
			}
			plan.Operations = []Operation{{Type: OperationInsertTableColumn, TableID: resolved.table.ID, CellIndex: resolved.cellIndex, Position: normalizedPosition(action.Position), TableColumn: action.TableColumn, TrackChanges: action.EnableTrackChanges}}
			plan.StatusText = fmt.Sprintf("已在表格 %s 的第 %d 列%s插入新列", resolved.table.ID, resolved.cellIndex+1, beforeAfterLabel(action.Position))
		case "delete_table_column":
			plan.Operations = []Operation{{Type: OperationDeleteTableColumn, TableID: resolved.table.ID, CellIndex: resolved.cellIndex, TrackChanges: action.EnableTrackChanges}}
			plan.StatusText = fmt.Sprintf("已删除表格 %s 的第 %d 列", resolved.table.ID, resolved.cellIndex+1)
		case "update_table_cell":
			if action.TableCell == nil {
				return failedPlan(action, "INVALID_ACTION", "更新单元格缺少 table_cell", "table_cell")
			}
			plan.Operations = []Operation{{Type: OperationUpdateTableCell, TableID: resolved.table.ID, RowIndex: resolved.rowIndex, CellIndex: resolved.cellIndex, TableCell: action.TableCell, TrackChanges: action.EnableTrackChanges}}
			plan.StatusText = fmt.Sprintf("已更新表格 %s 第 %d 行第 %d 列的内容", resolved.table.ID, resolved.rowIndex+1, resolved.cellIndex+1)
		}
		return plan
	case "insert_table":
		if action.TableSpec == nil || len(action.TableSpec.Rows) == 0 {
			return failedPlan(action, "INVALID_ACTION", "插入表格缺少 table_spec", "table")
		}
		resolved, diagnostics := resolveParagraphTarget(document, action, false)
		if diagnostics != nil && resolved == nil && action.TargetText != "" {
			if noopPlan := buildAlreadyAppliedNoopPlan(document, action); noopPlan != nil {
				return noopPlan
			}
			plan.Status = "failed"
			plan.StatusText = diagnosticMessage(diagnostics, "插入表格锚点定位失败")
			plan.Diagnostics = diagnostics
			return plan
		}
		anchorID := ""
		if resolved != nil {
			anchorID = resolved.paragraph.ID
			plan.ActiveParagraphID = anchorID
			plan.ResolvedTarget = &resolved.resolved
			plan.ReplayAction = canonicalizeReplayActionTarget(action, plan.ResolvedTarget)
		}
		plan.Operations = []Operation{{Type: OperationInsertTable, AnchorID: anchorID, Position: normalizedPosition(action.Position), Table: action.TableSpec, TrackChanges: action.EnableTrackChanges}}
		if anchorID == "" {
			plan.StatusText = fmt.Sprintf("已在文档末尾追加 %d 行表格", len(action.TableSpec.Rows))
		} else {
			plan.StatusText = fmt.Sprintf("已在目标段落%s插入 %d 行表格", beforeAfterLabel(action.Position), len(action.TableSpec.Rows))
		}
		return plan
	}

	resolved, diagnostics := resolveParagraphTarget(document, action, true)
	if diagnostics != nil {
		if noopPlan := buildAlreadyAppliedNoopPlan(document, action); noopPlan != nil {
			return noopPlan
		}
		plan.Status = "failed"
		plan.StatusText = diagnosticMessage(diagnostics, "定位失败")
		plan.Diagnostics = diagnostics
		return plan
	}
	if resolved == nil {
		if noopPlan := buildAlreadyAppliedNoopPlan(document, action); noopPlan != nil {
			return noopPlan
		}
		plan.Status = "failed"
		plan.StatusText = "未找到目标内容"
		plan.Diagnostics = &ActionDiagnostics{ErrorCode: "TARGET_NOT_FOUND", Message: "未找到目标内容", TargetKind: "paragraph", Retryable: true}
		return plan
	}
	plan.ActiveParagraphID = resolved.paragraph.ID
	plan.ResolvedTarget = &resolved.resolved
	replayAction := canonicalizeReplayActionTarget(action, plan.ResolvedTarget)
	plan.ReplayAction = replayAction
	if diagnostics := preflightCrossChapterScope(document, action); diagnostics != nil {
		plan.Status = "failed"
		plan.StatusText = diagnosticMessage(diagnostics, "范围预检失败")
		plan.Diagnostics = diagnostics
		return plan
	}

	switch action.Action {
	case "insert":
		if strings.TrimSpace(action.NewText) == "" {
			return failedPlan(action, "INVALID_ACTION", "插入操作缺少 new_text", "paragraph")
		}
		templateParagraph, templateRole := resolveInsertTemplate(document, resolved.paragraph, action)
		if diagnostics := preflightInsertAction(document, action, templateParagraph, templateRole); diagnostics != nil {
			plan.Status = "failed"
			plan.StatusText = diagnosticMessage(diagnostics, "插入预检失败")
			plan.Diagnostics = diagnostics
			return plan
		}
		paragraphStyle := mergeTemplateParagraphStyleWithConstraints(templateParagraph, action.ParagraphStylePatch, action.Constraints)
		runStyle := firstParagraphTextRunStyleWithConstraints(templateParagraph, action.Constraints)
		if (action.Constraints == nil || action.Constraints.preserveStyleEnabled()) && templateParagraph != nil && resolved.paragraph != nil && templateParagraph.ID == resolved.paragraph.ID {
			matchStart, matchEnd := resolved.rangeStart, resolved.rangeEnd
			if matchStart == 0 && matchEnd == 0 && strings.TrimSpace(action.TargetText) != "" {
				if start, end, ok := resolveMatchedTargetRangeWithPolicy(resolved.paragraph, action, nil); ok {
					matchStart, matchEnd = start, end
				}
			}
			runStyle = firstTextRunStyleInVisibleRange(resolved.paragraph.Runs, matchStart, matchEnd)
		}
		plan.Operations = []Operation{{
			Type:     OperationInsertParagraph,
			AnchorID: resolved.paragraph.ID,
			Position: normalizedPosition(action.Position),
			Paragraph: &ParagraphInput{
				Text:  action.NewText,
				Style: paragraphStyle,
				Runs:  []Run{{ID: buildInsertRunID(action.NewText), Text: action.NewText, Style: runStyle}},
			},
			TrackChanges: action.EnableTrackChanges,
		}}
		plan.StatusText = fmt.Sprintf("已在目标段落%s插入新段落，已沿用%s样式模板", beforeAfterLabel(action.Position), templateRole)
	case "insert_paragraph_break", "insert_line_break":
		if resolved.rangeEnd < resolved.rangeStart {
			return failedPlan(action, "INVALID_ACTION", "无法定位断开位置", "paragraph")
		}
		beforeText := extractRuneSlice(resolved.paragraph.PlainText(), resolved.rangeStart, resolved.rangeEnd)
		opType := OperationInsertParagraphBreak
		label := "段落断开"
		if action.Action == "insert_line_break" {
			opType = OperationInsertLineBreak
			label = "行内换行"
		}
		plan.Operations = []Operation{{
			Type:         opType,
			ParagraphID:  resolved.paragraph.ID,
			Position:     normalizedPosition(action.Position),
			StartOffset:  resolved.rangeStart,
			EndOffset:    resolved.rangeEnd,
			BeforeText:   beforeText,
			TrackChanges: action.EnableTrackChanges,
		}}
		plan.StatusText = fmt.Sprintf("已在目标文本%s插入%s", beforeAfterLabel(action.Position), label)
	case "delete":
		beforeText := extractRuneSlice(resolved.paragraph.PlainText(), resolved.rangeStart, resolved.rangeEnd)
		if shouldDeleteWholeParagraph(document, resolved.paragraph, resolved.rangeStart, resolved.rangeEnd) {
			plan.Operations = []Operation{{Type: OperationDeleteParagraph, ParagraphID: resolved.paragraph.ID, TrackChanges: action.EnableTrackChanges}}
			plan.StatusText = fmt.Sprintf("已删除整段“%s”", action.TargetText)
			break
		}
		plan.Operations = []Operation{{Type: OperationReplaceText, ParagraphID: resolved.paragraph.ID, StartOffset: resolved.rangeStart, EndOffset: resolved.rangeEnd, BeforeText: beforeText, Text: "", TrackChanges: action.EnableTrackChanges}}
		plan.StatusText = fmt.Sprintf("已删除“%s”", action.TargetText)
	case "format":
		if !hasTextStylePatch(action.StylePatch) {
			return failedPlan(action, "INVALID_ACTION", "格式修改缺少 style_patch", "paragraph")
		}
		beforeText := extractRuneSlice(resolved.paragraph.PlainText(), resolved.rangeStart, resolved.rangeEnd)
		plan.Operations = []Operation{{Type: OperationUpdateTextStyle, ParagraphID: resolved.paragraph.ID, StartOffset: resolved.rangeStart, EndOffset: resolved.rangeEnd, BeforeText: beforeText, StylePatch: action.StylePatch, TrackChanges: action.EnableTrackChanges}}
		plan.StatusText = fmt.Sprintf("已更新“%s”的文本样式", action.TargetText)
	case "format_paragraph":
		if !hasParagraphStylePatch(action.ParagraphStylePatch) {
			return failedPlan(action, "INVALID_ACTION", "段落格式修改缺少 paragraph_style_patch", "paragraph")
		}
		plan.Operations = []Operation{{Type: OperationUpdateParagraphStyle, ParagraphID: resolved.paragraph.ID, ParagraphStylePatch: action.ParagraphStylePatch, TrackChanges: action.EnableTrackChanges}}
		plan.StatusText = fmt.Sprintf("已更新“%s”所在段落的样式", action.TargetText)
	case "replace":
		beforeText := extractRuneSlice(resolved.paragraph.PlainText(), resolved.rangeStart, resolved.rangeEnd)
		if shouldExpandReplaceIntoParagraphs(document, resolved.paragraph, resolved.rangeStart, resolved.rangeEnd, action.NewText) {
			paragraphTexts := splitReplacementParagraphTexts(action.NewText)
			runStyle := firstTextRunStyleInVisibleRange(resolved.paragraph.Runs, resolved.rangeStart, resolved.rangeEnd)
			paragraphStyle := cloneParagraphStyle(resolved.paragraph.Style)
			operations := make([]Operation, 0, len(paragraphTexts))
			operations = append(operations, Operation{Type: OperationReplaceText, ParagraphID: resolved.paragraph.ID, StartOffset: resolved.rangeStart, EndOffset: resolved.rangeEnd, BeforeText: beforeText, Text: paragraphTexts[0], TrackChanges: action.EnableTrackChanges})
			for index := len(paragraphTexts) - 1; index >= 1; index-- {
				operations = append(operations, Operation{
					Type:     OperationInsertParagraph,
					AnchorID: resolved.paragraph.ID,
					Position: "after",
					Paragraph: &ParagraphInput{
						Style: paragraphStyle,
						Runs:  buildRunsFromText(paragraphTexts[index], runStyle),
					},
					TrackChanges: action.EnableTrackChanges,
				})
			}
			plan.Operations = operations
			plan.StatusText = fmt.Sprintf("已按原段落样式将“%s”重写为多段内容", action.TargetText)
			break
		}
		plan.Operations = []Operation{{Type: OperationReplaceText, ParagraphID: resolved.paragraph.ID, StartOffset: resolved.rangeStart, EndOffset: resolved.rangeEnd, BeforeText: beforeText, Text: action.NewText, TrackChanges: action.EnableTrackChanges}}
		plan.StatusText = fmt.Sprintf("已将“%s”替换为“%s”", action.TargetText, action.NewText)
	default:
		return failedPlan(action, "UNSUPPORTED_EDIT_KIND", fmt.Sprintf("暂不支持的动作类型：%s", action.Action), "unknown")
	}

	return plan
}

func shouldDeleteWholeParagraph(document *Document, paragraph *Paragraph, rangeStart, rangeEnd int) bool {
	if document == nil || paragraph == nil {
		return false
	}
	plainText := []rune(paragraph.PlainText())
	if len(plainText) == 0 {
		return false
	}
	if rangeStart != 0 || rangeEnd != len(plainText) {
		return false
	}
	if paragraph.SectionBreakLayout != nil || strings.TrimSpace(paragraph.SectionBreakXML) != "" {
		return false
	}
	return isTopLevelParagraphBlock(document, paragraph.ID)
}

func shouldExpandReplaceIntoParagraphs(document *Document, paragraph *Paragraph, rangeStart, rangeEnd int, replacement string) bool {
	if document == nil || paragraph == nil {
		return false
	}
	plainText := []rune(paragraph.PlainText())
	if rangeStart != 0 || rangeEnd != len(plainText) {
		return false
	}
	if paragraph.SectionBreakLayout != nil || strings.TrimSpace(paragraph.SectionBreakXML) != "" {
		return false
	}
	return strings.Contains(normalizeTextLineEndings(replacement), "\n\n")
}

func splitReplacementParagraphTexts(text string) []string {
	normalized := normalizeTextLineEndings(text)
	if normalized == "" {
		return []string{""}
	}
	parts := make([]string, 0, 2)
	var current strings.Builder
	newlineCount := 0
	flushParagraph := func() {
		parts = append(parts, current.String())
		current.Reset()
	}
	for _, char := range normalized {
		if char != '\n' {
			if newlineCount == 1 {
				current.WriteRune('\n')
			} else if newlineCount >= 2 {
				flushParagraph()
				for extra := 0; extra < newlineCount-2; extra++ {
					parts = append(parts, "")
				}
			}
			newlineCount = 0
			current.WriteRune(char)
			continue
		}
		newlineCount++
	}
	if newlineCount == 1 {
		current.WriteRune('\n')
	} else if newlineCount >= 2 {
		flushParagraph()
		for extra := 0; extra < newlineCount-2; extra++ {
			parts = append(parts, "")
		}
		return parts
	}
	parts = append(parts, current.String())
	return parts
}

func isTopLevelParagraphBlock(document *Document, paragraphID string) bool {
	if document == nil || strings.TrimSpace(paragraphID) == "" {
		return false
	}
	for _, block := range document.Blocks {
		if block.Type == BlockTypeParagraph && block.Paragraph != nil && block.Paragraph.ID == paragraphID {
			return true
		}
	}
	return false
}

func appendIRNodesFromBlocks(nodes *[]IRNode, outline *[]IROutlineNode, document *Document, blocks []Block, region string, pathPrefix []string, headingScope []string, parentNodeID string) []string {
	currentNodeIDs := make([]string, 0)
	currentHeadingScope := clonePath(headingScope)
	for blockIndex, block := range blocks {
		blockPath := append(clonePath(pathPrefix), fmt.Sprintf("block_%d", blockIndex))
		switch block.Type {
		case BlockTypeParagraph:
			if block.Paragraph == nil {
				continue
			}
			paragraph := block.Paragraph
			text := paragraph.PlainText()
			path := append(clonePath(blockPath), paragraph.ID)
			scope := buildHeadingScopeForParagraph(document, paragraph.ID)
			if len(scope) > 0 {
				currentHeadingScope = clonePath(scope)
			} else {
				scope = clonePath(currentHeadingScope)
			}
			level := extractHeadingLevel(paragraph)
			node := IRNode{
				NodeID:             paragraph.ID,
				Type:               "paragraph",
				Region:             region,
				Path:               path,
				ParentNodeID:       parentNodeID,
				HeadingScope:       scope,
				ParagraphID:        paragraph.ID,
				HeadingLevel:       level,
				StyleID:            paragraph.Style.StyleID,
				StyleSignature:     paragraphStyleSignature(paragraph),
				NumberingText:      paragraph.NumberingText,
				NumberingSignature: paragraphNumberingSignature(paragraph),
				SectionSignature:   paragraphSectionSignature(document, paragraph),
				Text:               text,
				CharRange:          buildIRTextRange(text),
				Preview:            previewText(text),
			}
			*nodes = append(*nodes, node)
			currentNodeIDs = append(currentNodeIDs, node.NodeID)
			if level > 0 {
				*outline = append(*outline, IROutlineNode{ParagraphID: paragraph.ID, Text: text, HeadingLevel: level, Path: path, HeadingScope: scope, StyleID: paragraph.Style.StyleID, NumberingText: paragraph.NumberingText})
			}
		case BlockTypeTable:
			if block.Table == nil {
				continue
			}
			tablePath := append(clonePath(blockPath), block.Table.ID)
			tableText := tablePlainText(*block.Table)
			tableIndex := len(*nodes)
			*nodes = append(*nodes, IRNode{NodeID: block.Table.ID, Type: "table", Region: region, Path: tablePath, ParentNodeID: parentNodeID, TableID: block.Table.ID, HeadingScope: clonePath(currentHeadingScope), SectionSignature: documentSectionSignature(document), Preview: previewText(tableText), Text: tableText, CharRange: buildIRTextRange(tableText)})
			currentNodeIDs = append(currentNodeIDs, block.Table.ID)
			cellChildIDs := make([]string, 0)
			for rowIndex, row := range block.Table.Rows {
				for cellIndex, cell := range row.Cells {
					cellPath := append(clonePath(tablePath), fmt.Sprintf("row_%d", rowIndex), fmt.Sprintf("cell_%d", cellIndex))
					cellText := tableCellPlainText(cell)
					cellIndexInNodes := len(*nodes)
					*nodes = append(*nodes, IRNode{NodeID: cell.ID, Type: "table_cell", Region: region, Path: cellPath, ParentNodeID: block.Table.ID, TableID: block.Table.ID, RowIndex: rowIndex, CellIndex: cellIndex, HeadingScope: clonePath(currentHeadingScope), SectionSignature: documentSectionSignature(document), Preview: previewText(cellText), Text: cellText, CharRange: buildIRTextRange(cellText)})
					cellChildIDs = append(cellChildIDs, cell.ID)
					childIDs := appendIRNodesFromBlocks(nodes, outline, document, cell.Blocks, region, cellPath, currentHeadingScope, cell.ID)
					if len(childIDs) > 0 {
						(*nodes)[cellIndexInNodes].ChildNodeIDs = childIDs
					}
				}
			}
			if len(cellChildIDs) > 0 {
				(*nodes)[tableIndex].ChildNodeIDs = cellChildIDs
			}
		}
	}
	return currentNodeIDs
}

func normalizeIRQueryRequest(req IRQueryRequest) IRQueryRequest {
	req.Query = strings.TrimSpace(req.Query)
	req.Type = strings.ToLower(strings.TrimSpace(req.Type))
	req.Region = strings.ToLower(strings.TrimSpace(req.Region))
	req.HeadingScope = strings.TrimSpace(req.HeadingScope)
	req.NodeID = strings.TrimSpace(req.NodeID)
	req.ParagraphID = strings.TrimSpace(req.ParagraphID)
	req.TableID = strings.TrimSpace(req.TableID)
	if req.Limit < 0 {
		req.Limit = 0
	}
	if req.Limit > 200 {
		req.Limit = 200
	}
	return req
}

func irNodeMatchesQuery(node IRNode, req IRQueryRequest) bool {
	if req.NodeID != "" && node.NodeID != req.NodeID {
		return false
	}
	if req.ParagraphID != "" && node.ParagraphID != req.ParagraphID {
		return false
	}
	if req.TableID != "" && node.TableID != req.TableID {
		return false
	}
	if req.Type != "" && strings.ToLower(strings.TrimSpace(node.Type)) != req.Type {
		return false
	}
	if req.Region != "" && strings.ToLower(strings.TrimSpace(node.Region)) != req.Region {
		return false
	}
	if req.HeadingScope != "" {
		headingText := strings.ToLower(strings.Join(node.HeadingScope, " / "))
		if !strings.Contains(headingText, strings.ToLower(req.HeadingScope)) {
			return false
		}
	}
	if req.Query == "" {
		return true
	}
	query := strings.ToLower(req.Query)
	searchSpace := strings.ToLower(strings.Join([]string{
		node.Text,
		node.Preview,
		strings.Join(node.Path, " / "),
		strings.Join(node.HeadingScope, " / "),
		node.StyleID,
		node.NumberingText,
	}, "\n"))
	return strings.Contains(searchSpace, query)
}

func buildIRTextRange(text string) *IRTextRange {
	if strings.TrimSpace(text) == "" {
		return nil
	}
	return &IRTextRange{Start: 0, End: utf8.RuneCountInString(text)}
}

func paragraphStyleSignature(paragraph *Paragraph) string {
	if paragraph == nil {
		return ""
	}
	style := paragraph.Style
	parts := make([]string, 0)
	if style.StyleID != "" {
		parts = append(parts, "style:"+style.StyleID)
	}
	if style.Alignment != "" {
		parts = append(parts, "align:"+style.Alignment)
	}
	if style.LineSpacing != 0 {
		parts = append(parts, fmt.Sprintf("line:%d", style.LineSpacing))
	}
	if style.LineSpacingRule != "" {
		parts = append(parts, "line_rule:"+style.LineSpacingRule)
	}
	if style.SpacingBefore != 0 || style.SpacingAfter != 0 {
		parts = append(parts, fmt.Sprintf("spacing:%d/%d", style.SpacingBefore, style.SpacingAfter))
	}
	if style.FirstLineIndent != 0 || style.LeftIndent != 0 {
		parts = append(parts, fmt.Sprintf("indent:%d/%d", style.FirstLineIndent, style.LeftIndent))
	}
	if style.KeepNextSet {
		parts = append(parts, fmt.Sprintf("keep_next:%t", style.KeepNext))
	}
	if style.PageBreakBeforeSet {
		parts = append(parts, fmt.Sprintf("page_break_before:%t", style.PageBreakBefore))
	}
	return strings.Join(parts, "|")
}

func paragraphNumberingSignature(paragraph *Paragraph) string {
	if paragraph == nil {
		return ""
	}
	parts := make([]string, 0)
	if paragraph.NumberingText != "" {
		parts = append(parts, "text:"+paragraph.NumberingText)
	}
	if paragraph.Style.Numbering != nil {
		parts = append(parts, fmt.Sprintf("ref:%s:%d", paragraph.Style.Numbering.NumID, paragraph.Style.Numbering.Level))
	}
	return strings.Join(parts, "|")
}

func paragraphSectionSignature(document *Document, paragraph *Paragraph) string {
	if paragraph != nil && paragraph.SectionBreakLayout != nil {
		return formatSectionSignature(*paragraph.SectionBreakLayout)
	}
	return documentSectionSignature(document)
}

func documentSectionSignature(document *Document) string {
	if document == nil {
		return ""
	}
	return formatSectionSignature(document.Meta.SectionLayout)
}

func formatSectionSignature(layout SectionLayout) string {
	parts := make([]string, 0)
	if layout.BreakType != "" {
		parts = append(parts, "break:"+layout.BreakType)
	}
	if layout.PageWidth != 0 || layout.PageHeight != 0 {
		parts = append(parts, fmt.Sprintf("page:%dx%d", layout.PageWidth, layout.PageHeight))
	}
	if layout.Orientation != "" {
		parts = append(parts, "orientation:"+layout.Orientation)
	}
	if layout.MarginTop != 0 || layout.MarginRight != 0 || layout.MarginBottom != 0 || layout.MarginLeft != 0 {
		parts = append(parts, fmt.Sprintf("margin:%d/%d/%d/%d", layout.MarginTop, layout.MarginRight, layout.MarginBottom, layout.MarginLeft))
	}
	if layout.PageNumStart != 0 || layout.PageNumFmt != "" {
		parts = append(parts, fmt.Sprintf("page_num:%d/%s", layout.PageNumStart, layout.PageNumFmt))
	}
	return strings.Join(parts, "|")
}

func normalizeEditorAction(action EditorAction) EditorAction {
	action.Action = strings.ToLower(strings.TrimSpace(action.Action))
	action.TargetText = strings.TrimSpace(action.TargetText)
	action.TargetSelector = normalizeActionTargetSelector(action.TargetSelector)
	action.Constraints = normalizeActionConstraints(action.Constraints)
	action.FailurePolicy = normalizeActionFailurePolicy(action.FailurePolicy)
	action.PreferredParagraphID = strings.TrimSpace(action.PreferredParagraphID)
	action.TableID = strings.TrimSpace(action.TableID)
	action.Position = normalizedPosition(action.Position)
	action.EditIntent = strings.TrimSpace(action.EditIntent)
	action.ContextScope = normalizedScope(action.ContextScope)
	action.RetrievalQuery = strings.TrimSpace(action.RetrievalQuery)
	action.ReaderSessionID = strings.TrimSpace(action.ReaderSessionID)
	action.ReaderContextSummary = strings.TrimSpace(action.ReaderContextSummary)
	action.FormatBasis = strings.TrimSpace(action.FormatBasis)
	action.ExplorationSummary = strings.TrimSpace(action.ExplorationSummary)
	action = applyExplicitSelectorFields(action)
	if action.RetrievalQuery == "" {
		action.RetrievalQuery = selectorFallbackQuery(action.TargetSelector, action.TargetText)
	}
	if action.Action == "replace" || action.Action == "insert" || action.Action == "insert_paragraph_break" || action.Action == "insert_line_break" || action.Action == "insert_table" || action.Action == "insert_table_row" || action.Action == "delete_table_row" || action.Action == "insert_table_column" || action.Action == "delete_table_column" || action.Action == "update_table_cell" || action.Action == "delete" || action.Action == "format" || action.Action == "format_paragraph" {
		action.RequireRetrieval = true
	}
	return action
}

func normalizeActionConstraints(constraints *ActionConstraints) *ActionConstraints {
	if constraints == nil {
		return nil
	}
	normalized := *constraints
	normalized.RawText = strings.TrimSpace(normalized.RawText)
	if !normalized.hasStructuredFields() && normalized.RawText == "" {
		return nil
	}
	return &normalized
}

func normalizeActionFailurePolicy(policy *ActionFailurePolicy) *ActionFailurePolicy {
	if policy == nil {
		return nil
	}
	normalized := *policy
	normalized.OnAmbiguous = strings.ToLower(strings.TrimSpace(normalized.OnAmbiguous))
	normalized.OnValidationFailed = strings.ToLower(strings.TrimSpace(normalized.OnValidationFailed))
	if normalized.OnAmbiguous == "" {
		normalized.OnAmbiguous = "return_candidates"
	}
	if normalized.OnValidationFailed == "" {
		normalized.OnValidationFailed = "report_warning"
	}
	return &normalized
}

func normalizeActionTargetSelector(selector *ActionTargetSelector) *ActionTargetSelector {
	if selector == nil {
		return nil
	}
	normalized := *selector
	normalized.Type = strings.ToLower(strings.TrimSpace(normalized.Type))
	normalized.ParagraphID = strings.TrimSpace(normalized.ParagraphID)
	normalized.TableID = strings.TrimSpace(normalized.TableID)
	normalized.Chapter = strings.TrimSpace(normalized.Chapter)
	normalized.TextEquals = strings.TrimSpace(normalized.TextEquals)
	normalized.Region = strings.ToLower(strings.TrimSpace(normalized.Region))
	if normalized.RowIndex < 0 {
		normalized.RowIndex = 0
	}
	if normalized.CellIndex < 0 {
		normalized.CellIndex = 0
	}
	if normalized.HeadingLevel < 0 {
		normalized.HeadingLevel = 0
	}
	if normalized.Ordinal < 0 {
		normalized.Ordinal = 0
	}
	switch normalized.Type {
	case "paragraph_id_selector":
		normalized.TableID = ""
		normalized.RowIndex = 0
		normalized.CellIndex = 0
		normalized.HeadingLevel = 0
		normalized.Ordinal = 0
		normalized.Chapter = ""
		normalized.TextEquals = ""
		normalized.Region = ""
	case "heading_scope_selector":
		normalized.ParagraphID = ""
		normalized.TableID = ""
		normalized.RowIndex = 0
		normalized.CellIndex = 0
	case "table_selector":
		normalized.ParagraphID = ""
		normalized.RowIndex = 0
		normalized.CellIndex = 0
		normalized.HeadingLevel = 0
		normalized.Ordinal = 0
		normalized.Chapter = ""
		normalized.TextEquals = ""
		normalized.Region = ""
	case "table_cell_selector":
		normalized.ParagraphID = ""
		normalized.HeadingLevel = 0
		normalized.Ordinal = 0
		normalized.Chapter = ""
		normalized.TextEquals = ""
		normalized.Region = ""
	}
	return &normalized
}

func applyExplicitSelectorFields(action EditorAction) EditorAction {
	if action.TargetSelector == nil {
		return action
	}
	switch action.TargetSelector.Type {
	case "paragraph_id_selector":
		action.PreferredParagraphID = action.TargetSelector.ParagraphID
		action.TableID = ""
		action.RowIndex = 0
		action.CellIndex = 0
	case "heading_scope_selector":
		action.PreferredParagraphID = ""
		action.TableID = ""
		action.RowIndex = 0
		action.CellIndex = 0
	case "table_selector":
		action.PreferredParagraphID = ""
		action.TableID = action.TargetSelector.TableID
		action.RowIndex = 0
		action.CellIndex = 0
	case "table_cell_selector":
		action.PreferredParagraphID = ""
		action.TableID = action.TargetSelector.TableID
		action.RowIndex = action.TargetSelector.RowIndex
		action.CellIndex = action.TargetSelector.CellIndex
	}
	return action
}

func selectorFallbackQuery(selector *ActionTargetSelector, targetText string) string {
	if strings.TrimSpace(targetText) != "" {
		return strings.TrimSpace(targetText)
	}
	if selector == nil {
		return ""
	}
	if strings.TrimSpace(selector.TextEquals) != "" {
		return strings.TrimSpace(selector.TextEquals)
	}
	if strings.TrimSpace(selector.Chapter) != "" {
		return strings.TrimSpace(selector.Chapter)
	}
	return ""
}

func normalizedPosition(position string) string {
	if strings.EqualFold(strings.TrimSpace(position), "before") {
		return "before"
	}
	return "after"
}

func normalizedScope(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "line", "paragraph", "table", "all":
		return strings.ToLower(strings.TrimSpace(scope))
	default:
		return "paragraph"
	}
}

func requiresPreciseAnchor(action EditorAction) bool {
	return action.RequireRetrieval && action.Action != "scroll" && action.Action != "insert_table_row" && action.Action != "delete_table_row" && action.Action != "insert_table_column" && action.Action != "delete_table_column" && action.Action != "update_table_cell"
}

func buildSearchQueryCandidates(action EditorAction) []string {
	rawCandidates := []string{}
	if strings.TrimSpace(action.RetrievalQuery) != "" {
		rawCandidates = append(rawCandidates, strings.TrimSpace(action.RetrievalQuery))
	}
	if strings.TrimSpace(action.TargetText) != "" {
		rawCandidates = append(rawCandidates, strings.TrimSpace(action.TargetText))
	}
	expanded := make([]string, 0, len(rawCandidates))
	for _, candidate := range rawCandidates {
		expanded = append(expanded, candidate)
		fragments := strings.FieldsFunc(candidate, func(r rune) bool {
			switch r {
			case '\r', '\n', '，', '。', '；', '：', '、', ',', '.', '!', '?', '！', '？', '(', ')', '（', '）', '[', ']', '【', '】', '<', '>', '《', '》', '“', '”', '"', '\'', '‘', '’':
				return true
			default:
				return false
			}
		})
		sort.SliceStable(fragments, func(i, j int) bool {
			return len([]rune(strings.TrimSpace(fragments[i]))) > len([]rune(strings.TrimSpace(fragments[j])))
		})
		for _, fragment := range fragments {
			trimmed := strings.TrimSpace(fragment)
			if len([]rune(trimmed)) >= 2 && trimmed != candidate {
				expanded = append(expanded, trimmed)
			}
		}
	}
	seen := make(map[string]struct{})
	result := make([]string, 0, len(expanded))
	for _, candidate := range expanded {
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		result = append(result, candidate)
		if len(result) >= 8 {
			break
		}
	}
	return result
}

func buildScopeCandidates(scope string) []string {
	requested := normalizedScope(scope)
	candidates := []string{requested}
	if requested == "line" {
		candidates = append(candidates, "paragraph")
	}
	if requested != "all" {
		candidates = append(candidates, "all")
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(candidates))
	for _, item := range candidates {
		if _, ok := seen[item]; ok {
			continue
		}
		seen[item] = struct{}{}
		result = append(result, item)
	}
	return result
}

func resolveParagraphTarget(document *Document, action EditorAction, needRange bool) (*resolvedParagraphTarget, *ActionDiagnostics) {
	if document == nil {
		return nil, &ActionDiagnostics{ErrorCode: "DOCUMENT_NOT_FOUND", Message: "文档快照为空", TargetKind: "paragraph"}
	}
	attempted := make([]string, 0)
	if action.TargetSelector != nil {
		selector := action.TargetSelector
		if selector.Type == "paragraph_id_selector" {
			selectorParagraphID := strings.TrimSpace(selector.ParagraphID)
			if selectorParagraphID == "" {
				attempted = append(attempted, "selector=paragraph_id_selector missing paragraph_id")
				if strings.TrimSpace(action.TargetText) == "" {
					return nil, &ActionDiagnostics{ErrorCode: "TARGET_NOT_FOUND", Message: "paragraph_id_selector 缺少 paragraph_id", TargetKind: "paragraph", Query: fallbackQuery(action), Scope: normalizedScope(action.ContextScope), AttemptedStrategies: attempted, RecommendedActions: []string{"补充 paragraph_id", "改用 preferred_paragraph_id 锁定目标段落", "先重新读取文档结构后再编辑"}, Retryable: true}
				}
			} else {
				paragraph := locateParagraphByID(document, selectorParagraphID)
				if paragraph != nil {
					resolved := buildResolvedParagraphTarget(document, paragraph, nil, action)
					if !needRange || action.Action == "insert" || action.Action == "insert_table" || action.Action == "format_paragraph" {
						return resolved, nil
					}
					start, end, ok := resolveMatchedTargetRangeWithPolicy(paragraph, action, nil)
					if !ok {
						return nil, applyAmbiguityPolicy(action, &ActionDiagnostics{ErrorCode: "AMBIGUOUS_TARGET", Message: "selector 已锁定段落，但段内目标文本无法唯一定位", TargetKind: "paragraph", Query: fallbackQuery(action), CandidateCount: 1, Candidates: []ActionCandidate{buildParagraphCandidate(document, paragraph, nil, 1)}, RecommendedActions: []string{"把 target_text 改成更具体的原文片段", "若要整段替换，请显式提供完整 target_text"}})
					}
					resolved.rangeStart = start
					resolved.rangeEnd = end
					resolved.resolved.MatchStart = start
					resolved.resolved.MatchEnd = end
					return resolved, nil
				}
				attempted = append(attempted, fmt.Sprintf("selector paragraph_id missing: %s", selectorParagraphID))
				if strings.TrimSpace(action.TargetText) == "" {
					return nil, &ActionDiagnostics{ErrorCode: "TARGET_NOT_FOUND", Message: fmt.Sprintf("未找到 selector 指定的段落 ID：%s", selectorParagraphID), TargetKind: "paragraph", Query: fallbackQuery(action), Scope: normalizedScope(action.ContextScope), AttemptedStrategies: attempted, RecommendedActions: []string{"重新获取最新段落 ID", "重新读取文档结构后再执行"}, Retryable: true}
				}
			}
		} else {
			resolved, diagnostics, handled := resolveParagraphTargetBySelector(document, action, needRange)
			if handled {
				return resolved, diagnostics
			}
		}
	}
	if action.PreferredParagraphID != "" {
		paragraph := locateParagraphByID(document, action.PreferredParagraphID)
		if paragraph == nil {
			attempted = append(attempted, fmt.Sprintf("preferred_paragraph_id missing: %s", action.PreferredParagraphID))
			if strings.TrimSpace(action.TargetText) == "" {
				return nil, &ActionDiagnostics{ErrorCode: "TARGET_NOT_FOUND", Message: fmt.Sprintf("未找到指定段落 ID：%s", action.PreferredParagraphID), TargetKind: "paragraph", Query: fallbackQuery(action), Scope: normalizedScope(action.ContextScope), AttemptedStrategies: attempted, RecommendedActions: []string{"重新获取最新段落 ID", "重新执行一次 reader 以锁定目标段落"}, Retryable: true}
			}
		} else {
			resolved := buildResolvedParagraphTarget(document, paragraph, nil, action)
			if !needRange || action.Action == "insert" || action.Action == "insert_table" || action.Action == "format_paragraph" {
				return resolved, nil
			}
			start, end, ok := resolveMatchedTargetRangeWithPolicy(paragraph, action, nil)
			if !ok {
				return nil, applyAmbiguityPolicy(action, &ActionDiagnostics{ErrorCode: "AMBIGUOUS_TARGET", Message: "段落已锁定，但目标文本在段内无法唯一定位", TargetKind: "paragraph", Query: action.TargetText, CandidateCount: 1, Candidates: []ActionCandidate{buildParagraphCandidate(document, paragraph, nil, 0)}, RecommendedActions: []string{"把 target_text 改成更具体的原文片段", "补充更精确的段内范围说明"}})
			}
			resolved.rangeStart = start
			resolved.rangeEnd = end
			resolved.resolved.MatchStart = start
			resolved.resolved.MatchEnd = end
			return resolved, nil
		}
	}

	if action.TargetText == "" && (action.Action == "insert" || action.Action == "insert_table") {
		return nil, nil
	}

	if requiresPreciseAnchor(action) {
		if paragraph := locateUniqueParagraph(document, action.TargetText); paragraph != nil {
			resolved := buildResolvedParagraphTarget(document, paragraph, nil, action)
			if !needRange || action.Action == "insert" || action.Action == "format_paragraph" {
				return resolved, nil
			}
			start, end, ok := resolveMatchedTargetRangeWithPolicy(paragraph, action, nil)
			if ok {
				resolved.rangeStart = start
				resolved.rangeEnd = end
				resolved.resolved.MatchStart = start
				resolved.resolved.MatchEnd = end
				return resolved, nil
			}
		}
	}

	queryCandidates := buildSearchQueryCandidates(action)
	scopeCandidates := buildScopeCandidates(action.ContextScope)
	for _, query := range queryCandidates {
		for _, scope := range scopeCandidates {
			searchResult := searchDocument(document, SearchRequest{Query: query, Scope: scope, Mode: "contains", Limit: 20})
			paragraphHits := filterParagraphHits(searchResult.Hits)
			attempted = append(attempted, fmt.Sprintf("query=%s scope=%s hits=%d", query, scope, len(paragraphHits)))
			if len(paragraphHits) == 0 {
				continue
			}
			if len(paragraphHits) > 1 {
				if paragraph := locateUniqueParagraph(document, action.TargetText); paragraph != nil {
					for _, hit := range paragraphHits {
						if hit.ParagraphID == paragraph.ID {
							resolved := buildResolvedParagraphTarget(document, paragraph, &hit, action)
							if !needRange || action.Action == "insert" || action.Action == "format_paragraph" {
								return resolved, nil
							}
							start, end, ok := resolveMatchedTargetRangeWithPolicy(paragraph, action, &hit)
							if ok {
								resolved.rangeStart = start
								resolved.rangeEnd = end
								resolved.resolved.MatchStart = start
								resolved.resolved.MatchEnd = end
								return resolved, nil
							}
							break
						}
					}
				}
				if resolved := selectBestParagraphTargetFromHits(document, action, paragraphHits, needRange); resolved != nil {
					return resolved, nil
				}
				return nil, ambiguousParagraphDiagnostics(document, action, query, scope, paragraphHits, attempted)
			}
			hit := paragraphHits[0]
			paragraph := locateParagraphByID(document, hit.ParagraphID)
			if paragraph == nil {
				continue
			}
			resolved := buildResolvedParagraphTarget(document, paragraph, &hit, action)
			if !needRange || action.Action == "insert" || action.Action == "insert_table" || action.Action == "format_paragraph" {
				return resolved, nil
			}
			start, end, ok := resolveMatchedTargetRangeWithPolicy(paragraph, action, &hit)
			if !ok {
				return nil, applyAmbiguityPolicy(action, &ActionDiagnostics{ErrorCode: "AMBIGUOUS_TARGET", Message: "已定位到目标段落，但段内目标文本无法唯一定位", TargetKind: "paragraph", Query: action.TargetText, Scope: scope, CandidateCount: 1, Candidates: []ActionCandidate{buildParagraphCandidate(document, paragraph, &hit, 0)}, AttemptedStrategies: attempted, RecommendedActions: []string{"把 target_text 改成更具体的原文片段", "若已知段落 ID，可补 preferred_paragraph_id"}})
			}
			resolved.rangeStart = start
			resolved.rangeEnd = end
			resolved.resolved.MatchStart = start
			resolved.resolved.MatchEnd = end
			return resolved, nil
		}
	}

	return nil, &ActionDiagnostics{ErrorCode: "TARGET_NOT_FOUND", Message: fmt.Sprintf("未找到可安全编辑的目标：%s", fallbackTargetText(action)), TargetKind: "paragraph", Query: fallbackQuery(action), Scope: normalizedScope(action.ContextScope), AttemptedStrategies: attempted, RecommendedActions: []string{"补充章节范围或段落 ID", "改用文档中真实存在的原文片段作为 target_text", "先执行 reader 锁定目标再编辑"}, Retryable: true}
}

func resolveParagraphTargetBySelector(document *Document, action EditorAction, needRange bool) (*resolvedParagraphTarget, *ActionDiagnostics, bool) {
	selector := action.TargetSelector
	if selector == nil {
		return nil, nil, false
	}
	switch selector.Type {
	case "paragraph_id_selector":
		return nil, nil, false
	case "heading_scope_selector":
		matches := findParagraphsByHeadingSelector(document, selector)
		if len(matches) == 0 {
			return nil, &ActionDiagnostics{ErrorCode: "TARGET_NOT_FOUND", Message: "未找到符合 selector 条件的标题节点", TargetKind: "heading", Query: selectorFallbackQuery(selector, action.TargetText), Scope: selector.Chapter, RecommendedActions: []string{"补充 chapter、heading_level 或 ordinal", "改用更具体的 text_equals 锁定标题"}, Retryable: true}, true
		}
		if selector.Ordinal > 0 {
			ordinalIndex := selector.Ordinal - 1
			if ordinalIndex < 0 || ordinalIndex >= len(matches) {
				return nil, &ActionDiagnostics{ErrorCode: "TARGET_NOT_FOUND", Message: fmt.Sprintf("heading_scope_selector 指定的 ordinal=%d 超出范围", selector.Ordinal), TargetKind: "heading", Query: selectorFallbackQuery(selector, action.TargetText), Scope: selector.Chapter, CandidateCount: len(matches), RecommendedActions: []string{"降低 ordinal 或补充更精确的 chapter/text_equals"}, Retryable: true}, true
			}
			matches = []*Paragraph{matches[ordinalIndex]}
		}
		if len(matches) > 1 {
			if allowBestCandidateResolution(action) {
				paragraph := matches[0]
				resolved := buildResolvedParagraphTarget(document, paragraph, nil, action)
				if !needRange || action.Action == "insert" || action.Action == "insert_table" || action.Action == "format_paragraph" {
					return resolved, nil, true
				}
				start, end, ok := resolveMatchedTargetRangeWithPolicy(paragraph, action, nil)
				if ok {
					resolved.rangeStart = start
					resolved.rangeEnd = end
					resolved.resolved.MatchStart = start
					resolved.resolved.MatchEnd = end
					return resolved, nil, true
				}
			}
			candidates := make([]ActionCandidate, 0, len(matches))
			for index, paragraph := range matches {
				candidates = append(candidates, buildParagraphCandidate(document, paragraph, nil, len(matches)-index))
			}
			if len(candidates) > 3 {
				candidates = candidates[:3]
			}
			return nil, applyAmbiguityPolicy(action, &ActionDiagnostics{ErrorCode: "AMBIGUOUS_TARGET", Message: fmt.Sprintf("selector 命中 %d 个标题节点，无法安全直接修改", len(matches)), TargetKind: "heading", Query: selectorFallbackQuery(selector, action.TargetText), Scope: selector.Chapter, CandidateCount: len(matches), Candidates: candidates, RecommendedActions: []string{"补充 ordinal", "补充 text_equals", "改用 paragraph_id_selector 精确锁定"}}), true
		}
		paragraph := matches[0]
		resolved := buildResolvedParagraphTarget(document, paragraph, nil, action)
		if !needRange || action.Action == "insert" || action.Action == "insert_table" || action.Action == "format_paragraph" {
			return resolved, nil, true
		}
		start, end, ok := resolveMatchedTargetRangeWithPolicy(paragraph, action, nil)
		if !ok {
			return nil, applyAmbiguityPolicy(action, &ActionDiagnostics{ErrorCode: "AMBIGUOUS_TARGET", Message: "selector 已锁定标题，但段内目标文本无法唯一定位", TargetKind: "heading", Query: fallbackQuery(action), Scope: selector.Chapter, CandidateCount: 1, Candidates: []ActionCandidate{buildParagraphCandidate(document, paragraph, nil, 1)}, RecommendedActions: []string{"把 target_text 改成完整标题文本", "或直接使用 paragraph_id_selector 锁定标题后修改"}}), true
		}
		resolved.rangeStart = start
		resolved.rangeEnd = end
		resolved.resolved.MatchStart = start
		resolved.resolved.MatchEnd = end
		return resolved, nil, true
	default:
		return nil, nil, false
	}
}

type resolvedTableTarget struct {
	table     *Table
	rowIndex  int
	cellIndex int
	resolved  ResolvedTarget
}

func resolvedTableTargetKind(action EditorAction) string {
	if action.Action == "update_table_cell" {
		return "table_cell"
	}
	return "table"
}

func resolveTableTarget(document *Document, action EditorAction) (*resolvedTableTarget, *ActionDiagnostics) {
	attempted := make([]string, 0)
	if action.TargetSelector != nil {
		selector := action.TargetSelector
		if selector.Type == "table_selector" || selector.Type == "table_cell_selector" {
			selectorTableID := strings.TrimSpace(selector.TableID)
			if selectorTableID == "" {
				attempted = append(attempted, fmt.Sprintf("selector=%s missing table_id", selector.Type))
				if strings.TrimSpace(action.TargetText) == "" {
					return nil, &ActionDiagnostics{ErrorCode: "TARGET_NOT_FOUND", Message: "table selector 缺少 table_id", TargetKind: "table", Query: fallbackQuery(action), AttemptedStrategies: attempted, RecommendedActions: []string{"补充 table_id", "先通过 IR 查询锁定目标表格"}, Retryable: true}
				}
			} else {
				table, err := findTable(document, selectorTableID)
				if err == nil {
					rowIndex := selector.RowIndex
					cellIndex := selector.CellIndex
					if rowIndex < 0 {
						rowIndex = 0
					}
					if cellIndex < 0 {
						cellIndex = 0
					}
					return &resolvedTableTarget{table: table, rowIndex: rowIndex, cellIndex: cellIndex, resolved: ResolvedTarget{TargetKind: resolvedTableTargetKind(action), TableID: table.ID, RowIndex: rowIndex, CellIndex: cellIndex, Path: buildTablePath(document, table.ID), Preview: previewText(tablePlainText(*table))}}, nil
				}
				attempted = append(attempted, fmt.Sprintf("selector table_id missing: %s", selectorTableID))
				if strings.TrimSpace(action.TargetText) == "" {
					return nil, &ActionDiagnostics{ErrorCode: "TARGET_NOT_FOUND", Message: err.Error(), TargetKind: "table", Query: fallbackQuery(action), AttemptedStrategies: attempted, RecommendedActions: []string{"重新获取最新 table_id", "若表格位置已变化，请重新读取结构后再执行"}, Retryable: true}
				}
			}
		} else {
			resolved, diagnostics, handled := resolveTableTargetBySelector(document, action.TargetSelector)
			if handled {
				return resolved, diagnostics
			}
		}
	}
	if action.TableID != "" {
		table, err := findTable(document, action.TableID)
		if err == nil {
			rowIndex := action.RowIndex
			cellIndex := action.CellIndex
			if rowIndex < 0 {
				rowIndex = 0
			}
			if cellIndex < 0 {
				cellIndex = 0
			}
			return &resolvedTableTarget{table: table, rowIndex: rowIndex, cellIndex: cellIndex, resolved: ResolvedTarget{TargetKind: resolvedTableTargetKind(action), TableID: table.ID, RowIndex: rowIndex, CellIndex: cellIndex, Path: buildTablePath(document, table.ID), Preview: previewText(tablePlainText(*table))}}, nil
		}
		attempted = append(attempted, fmt.Sprintf("table_id missing: %s", action.TableID))
		if strings.TrimSpace(action.TargetText) == "" {
			return nil, &ActionDiagnostics{ErrorCode: "TARGET_NOT_FOUND", Message: err.Error(), TargetKind: "table", Query: fallbackQuery(action), AttemptedStrategies: attempted, RecommendedActions: []string{"重新获取 table_id", "若文档中只有一个表格，可改为更明确的目标描述"}, Retryable: true}
		}
	}
	query := strings.TrimSpace(action.TargetText)
	if query == "" {
		return nil, &ActionDiagnostics{ErrorCode: "TARGET_NOT_FOUND", Message: "表格操作缺少 table_id，且 target_text 为空", TargetKind: "table", AttemptedStrategies: attempted, RecommendedActions: []string{"补充 table_id", "把 target_text 改成表格中可检索的真实文本"}, Retryable: true}
	}
	searchResult := searchDocument(document, SearchRequest{Query: query, Scope: "table", Mode: "contains", Limit: 20})
	tableHits := make([]SearchHit, 0)
	for _, hit := range searchResult.Hits {
		if hit.Type == "table" || hit.TableID != "" {
			tableHits = append(tableHits, hit)
		}
	}
	tableHits = preferPreciseTableHits(tableHits)
	if len(tableHits) == 0 {
		return nil, &ActionDiagnostics{ErrorCode: "TARGET_NOT_FOUND", Message: fmt.Sprintf("未找到包含“%s”的目标表格", query), TargetKind: "table", Query: query, AttemptedStrategies: attempted, RecommendedActions: []string{"把 target_text 改成表格中更具体的内容", "先通过 reader 获取真实 table_id"}, Retryable: true}
	}
	if len(tableHits) > 1 {
		if allowBestCandidateResolution(action) {
			bestHit := tableHits[0]
			table, err := findTable(document, bestHit.TableID)
			if err == nil {
				rowIndex, cellIndex := resolveSearchHitTableCoordinates(bestHit, action)
				return &resolvedTableTarget{table: table, rowIndex: rowIndex, cellIndex: cellIndex, resolved: ResolvedTarget{TargetKind: resolvedTableTargetKind(action), TableID: table.ID, RowIndex: rowIndex, CellIndex: cellIndex, Path: buildTablePath(document, table.ID), Preview: previewText(tablePlainText(*table))}}, nil
			}
		}
		candidates := make([]ActionCandidate, 0, len(tableHits))
		for _, hit := range tableHits {
			candidates = append(candidates, ActionCandidate{TargetKind: "table", TableID: hit.TableID, Path: buildTablePath(document, hit.TableID), Preview: previewText(hit.Text), MatchedText: hit.Text})
		}
		return nil, applyAmbiguityPolicy(action, &ActionDiagnostics{ErrorCode: "AMBIGUOUS_TARGET", Message: fmt.Sprintf("命中 %d 个候选表格，无法安全直接修改", len(tableHits)), TargetKind: "table", Query: query, CandidateCount: len(candidates), Candidates: candidates, RecommendedActions: []string{"补充更具体的单元格内容", "先通过 reader 锁定 table_id 再编辑"}})
	}
	bestHit := tableHits[0]
	table, err := findTable(document, bestHit.TableID)
	if err != nil {
		attempted = append(attempted, fmt.Sprintf("search hit table missing: %s", bestHit.TableID))
		return nil, &ActionDiagnostics{ErrorCode: "TARGET_NOT_FOUND", Message: err.Error(), TargetKind: "table", Query: query, AttemptedStrategies: attempted, RecommendedActions: []string{"重新读取表格结构", "重新执行一次 IR 查询确认 table_id"}, Retryable: true}
	}
	rowIndex, cellIndex := resolveSearchHitTableCoordinates(bestHit, action)
	return &resolvedTableTarget{table: table, rowIndex: rowIndex, cellIndex: cellIndex, resolved: ResolvedTarget{TargetKind: resolvedTableTargetKind(action), TableID: table.ID, RowIndex: rowIndex, CellIndex: cellIndex, Path: buildTablePath(document, table.ID), Preview: previewText(tablePlainText(*table))}}, nil
}

func resolveSearchHitTableCoordinates(hit SearchHit, action EditorAction) (int, int) {
	if hit.Type == "table_cell" {
		rowIndex := hit.RowIndex
		cellIndex := hit.CellIndex
		if action.RowIndex > 0 {
			rowIndex = action.RowIndex
		}
		if action.CellIndex > 0 {
			cellIndex = action.CellIndex
		}
		return rowIndex, cellIndex
	}
	return action.RowIndex, action.CellIndex
}

func preferPreciseTableHits(hits []SearchHit) []SearchHit {
	precise := make([]SearchHit, 0, len(hits))
	for _, hit := range hits {
		if hit.Type == "table_cell" {
			precise = append(precise, hit)
		}
	}
	if len(precise) > 0 {
		return precise
	}
	return hits
}

func resolveTableTargetBySelector(document *Document, selector *ActionTargetSelector) (*resolvedTableTarget, *ActionDiagnostics, bool) {
	if selector == nil {
		return nil, nil, false
	}
	switch selector.Type {
	case "table_selector", "table_cell_selector":
		return nil, nil, false
	default:
		return nil, nil, false
	}
}

func canonicalizeReplayActionTarget(action EditorAction, resolved *ResolvedTarget) EditorAction {
	if resolved == nil {
		return action
	}
	next := action
	switch resolved.TargetKind {
	case "paragraph", "heading":
		if resolved.ParagraphID != "" {
			next.PreferredParagraphID = resolved.ParagraphID
		}
		next.TableID = ""
		next.RowIndex = 0
		next.CellIndex = 0
		next.TargetSelector = &ActionTargetSelector{Type: "paragraph_id_selector", ParagraphID: resolved.ParagraphID}
	case "table", "table_cell":
		next.PreferredParagraphID = ""
		if resolved.TableID != "" {
			next.TableID = resolved.TableID
		}
		next.RowIndex = resolved.RowIndex
		next.CellIndex = resolved.CellIndex
		if resolved.TargetKind == "table_cell" {
			next.TargetSelector = &ActionTargetSelector{Type: "table_cell_selector", TableID: resolved.TableID, RowIndex: resolved.RowIndex, CellIndex: resolved.CellIndex}
		} else {
			next.TargetSelector = &ActionTargetSelector{Type: "table_selector", TableID: resolved.TableID}
		}
	}
	return next
}

func normalizedDocumentMatchText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), "")
}

func documentContainsNormalizedText(document *Document, text string) bool {
	normalizedText := normalizedDocumentMatchText(text)
	if document == nil || normalizedText == "" {
		return false
	}
	for _, paragraph := range document.Paragraphs {
		if strings.Contains(normalizedDocumentMatchText(paragraph.PlainText()), normalizedText) {
			return true
		}
	}
	return false
}

func locateParagraphByNormalizedText(document *Document, text string) *Paragraph {
	normalizedText := normalizedDocumentMatchText(text)
	if document == nil || normalizedText == "" {
		return nil
	}
	for index := range document.Paragraphs {
		paragraph := &document.Paragraphs[index]
		if strings.Contains(normalizedDocumentMatchText(paragraph.PlainText()), normalizedText) {
			return paragraph
		}
	}
	return nil
}

func buildAlreadyAppliedNoopPlan(document *Document, action EditorAction) *ActionPlanResult {
	if document == nil {
		return nil
	}
	switch action.Action {
	case "replace":
		if action.TargetText == "" || action.NewText == "" {
			return nil
		}
		if !documentContainsNormalizedText(document, action.TargetText) && documentContainsNormalizedText(document, action.NewText) {
			paragraph := locateParagraphByNormalizedText(document, action.NewText)
			plan := &ActionPlanResult{
				Status:           "noop",
				StatusText:       fmt.Sprintf("目标“%s”在当前会话中已不存在，疑似已替换为“%s”，本次跳过重复修改", action.TargetText, action.NewText),
				ExpectedRevision: document.Revision,
				ReplayAction:     action,
			}
			if paragraph != nil {
				plan.ActiveParagraphID = paragraph.ID
				resolved := buildResolvedParagraphTarget(document, paragraph, nil, action)
				plan.ResolvedTarget = &resolved.resolved
				plan.ReplayAction = canonicalizeReplayActionTarget(action, plan.ResolvedTarget)
			}
			return plan
		}
	case "delete":
		if action.TargetText == "" {
			return nil
		}
		if !documentContainsNormalizedText(document, action.TargetText) {
			return &ActionPlanResult{
				Status:           "noop",
				StatusText:       fmt.Sprintf("目标“%s”在当前会话中已不存在，本次跳过重复删除", action.TargetText),
				ExpectedRevision: document.Revision,
				ReplayAction:     action,
			}
		}
	}
	return nil
}

func searchDocument(document *Document, request SearchRequest) *SearchResult {
	query := strings.TrimSpace(request.Query)
	if document == nil || query == "" {
		return &SearchResult{Query: query, Scope: normalizeSearchScope(request.Scope), Mode: normalizeSearchMode(request.Mode), Limit: request.Limit}
	}
	scope := normalizeSearchScope(request.Scope)
	mode := normalizeSearchMode(request.Mode)
	limit := request.Limit
	if limit <= 0 {
		limit = 100
	}
	hits := make([]SearchHit, 0)
	truncated := false
	appendHit := func(hit SearchHit) {
		if truncated {
			return
		}
		if len(hits) >= limit {
			truncated = true
			return
		}
		hits = append(hits, hit)
	}
	collectSearchHitsFromBlocks(document.Blocks, query, scope, mode, request.CaseSensitive, appendHit)
	collectSearchHitsFromHeaderFooters(document.Meta.HeaderFooters, query, scope, mode, request.CaseSensitive, appendHit)
	return &SearchResult{Query: query, Scope: scope, Mode: mode, Total: len(hits), Limit: limit, Truncated: truncated, Hits: hits}
}

func filterParagraphHits(hits []SearchHit) []SearchHit {
	filtered := make([]SearchHit, 0, len(hits))
	for _, hit := range hits {
		if hit.ParagraphID != "" {
			filtered = append(filtered, hit)
		}
	}
	return filtered
}

func locateParagraphByID(document *Document, paragraphID string) *Paragraph {
	if strings.TrimSpace(paragraphID) == "" {
		return nil
	}
	paragraph, _, err := findParagraph(document, paragraphID)
	if err != nil {
		return nil
	}
	return paragraph
}

func locateParagraph(document *Document, targetText string) *Paragraph {
	trimmed := strings.TrimSpace(targetText)
	if trimmed == "" {
		if len(document.Paragraphs) == 0 {
			return nil
		}
		return &document.Paragraphs[len(document.Paragraphs)-1]
	}
	for index := range document.Paragraphs {
		if strings.Contains(document.Paragraphs[index].PlainText(), trimmed) {
			return &document.Paragraphs[index]
		}
	}
	compact := normalizeEditorText(trimmed)
	for index := range document.Paragraphs {
		if strings.Contains(normalizeEditorText(document.Paragraphs[index].PlainText()), compact) {
			return &document.Paragraphs[index]
		}
	}
	return nil
}

func locateUniqueParagraph(document *Document, targetText string) *Paragraph {
	trimmed := strings.TrimSpace(targetText)
	if trimmed == "" {
		return nil
	}
	exactMatches := make([]*Paragraph, 0)
	for index := range document.Paragraphs {
		if strings.Contains(document.Paragraphs[index].PlainText(), trimmed) {
			exactMatches = append(exactMatches, &document.Paragraphs[index])
		}
	}
	if len(exactMatches) == 1 {
		return exactMatches[0]
	}
	if len(exactMatches) > 1 {
		return nil
	}
	compact := normalizeEditorText(trimmed)
	normalizedMatches := make([]*Paragraph, 0)
	for index := range document.Paragraphs {
		if strings.Contains(normalizeEditorText(document.Paragraphs[index].PlainText()), compact) {
			normalizedMatches = append(normalizedMatches, &document.Paragraphs[index])
		}
	}
	if len(normalizedMatches) == 1 {
		return normalizedMatches[0]
	}
	return nil
}

func resolveMatchedTargetRangeWithPolicy(paragraph *Paragraph, action EditorAction, matchedHit *SearchHit) (int, int, bool) {
	if paragraph == nil {
		return 0, 0, false
	}
	paragraphText := paragraph.PlainText()
	if matchedHit != nil && matchedHit.ParagraphID == paragraph.ID && matchedHit.MatchEnd > matchedHit.MatchStart {
		baseOffset := 0
		if matchedHit.Type == "line" && matchedHit.LineIndex > 0 {
			lines := strings.Split(paragraphText, "\n")
			safeLineIndex := matchedHit.LineIndex
			if safeLineIndex >= len(lines) {
				safeLineIndex = len(lines) - 1
			}
			for index := 0; index < safeLineIndex; index++ {
				baseOffset += len([]rune(lines[index])) + 1
			}
		}
		paragraphLength := len([]rune(paragraphText))
		start := clampInt(baseOffset+matchedHit.MatchStart, 0, paragraphLength)
		end := clampInt(baseOffset+matchedHit.MatchEnd, 0, paragraphLength)
		if end > start {
			return start, end, true
		}
	}
	candidateTexts := make([]string, 0, 2)
	if strings.TrimSpace(action.TargetText) != "" {
		candidateTexts = append(candidateTexts, strings.TrimSpace(action.TargetText))
	}
	if strings.TrimSpace(action.TargetText) == "" && strings.TrimSpace(action.RetrievalQuery) != "" {
		candidateTexts = append(candidateTexts, strings.TrimSpace(action.RetrievalQuery))
	}
	seen := map[string]struct{}{}
	for _, candidate := range candidateTexts {
		if _, ok := seen[candidate]; ok {
			continue
		}
		seen[candidate] = struct{}{}
		ranges := collectRuneRanges(paragraphText, candidate)
		if len(ranges) == 1 {
			return ranges[0].start, ranges[0].end, true
		}
		if len(ranges) > 1 {
			if allowBestCandidateResolution(action) {
				return ranges[0].start, ranges[0].end, true
			}
			return 0, 0, false
		}
		normalized := collectNormalizedRuneRanges(paragraphText, candidate)
		if len(normalized) == 1 {
			return normalized[0].start, normalized[0].end, true
		}
		if len(normalized) > 1 {
			if allowBestCandidateResolution(action) {
				return normalized[0].start, normalized[0].end, true
			}
			return 0, 0, false
		}
	}
	return 0, 0, false
}

type runeRange struct{ start, end int }

func collectRuneRanges(content, target string) []runeRange {
	source := []rune(content)
	needle := []rune(target)
	if len(needle) == 0 || len(source) < len(needle) {
		return nil
	}
	ranges := make([]runeRange, 0)
outer:
	for index := 0; index <= len(source)-len(needle); index++ {
		for offset := 0; offset < len(needle); offset++ {
			if source[index+offset] != needle[offset] {
				continue outer
			}
		}
		ranges = append(ranges, runeRange{start: index, end: index + len(needle)})
	}
	return ranges
}

func collectNormalizedRuneRanges(content, target string) []runeRange {
	source := []rune(content)
	compactSource := make([]rune, 0, len(source))
	compactIndexes := make([]int, 0, len(source))
	for index, character := range source {
		if character == ' ' || character == '\n' || character == '\r' || character == '\t' {
			continue
		}
		compactSource = append(compactSource, character)
		compactIndexes = append(compactIndexes, index)
	}
	compactTarget := []rune(normalizeEditorText(target))
	if len(compactTarget) == 0 || len(compactSource) < len(compactTarget) {
		return nil
	}
	ranges := make([]runeRange, 0)
outer:
	for index := 0; index <= len(compactSource)-len(compactTarget); index++ {
		for offset := 0; offset < len(compactTarget); offset++ {
			if compactSource[index+offset] != compactTarget[offset] {
				continue outer
			}
		}
		start := compactIndexes[index]
		end := compactIndexes[index+len(compactTarget)-1] + 1
		ranges = append(ranges, runeRange{start: start, end: end})
	}
	return ranges
}

func normalizeEditorText(text string) string {
	trimmed := strings.TrimSpace(strings.ToLower(text))
	if trimmed == "" {
		return ""
	}
	parts := strings.Fields(trimmed)
	return strings.Join(parts, "")
}

func extractRuneSlice(content string, start, end int) string {
	runes := []rune(content)
	if start < 0 {
		start = 0
	}
	if end > len(runes) {
		end = len(runes)
	}
	if end < start {
		end = start
	}
	return string(runes[start:end])
}

func buildResolvedParagraphTarget(document *Document, paragraph *Paragraph, hit *SearchHit, action EditorAction) *resolvedParagraphTarget {
	resolved := &resolvedParagraphTarget{paragraph: paragraph, hit: hit}
	resolved.resolved = ResolvedTarget{TargetKind: "paragraph", ParagraphID: paragraph.ID, Region: paragraphRegion(hit), Path: buildParagraphPath(document, paragraph.ID), HeadingScope: buildHeadingScopeForParagraph(document, paragraph.ID), Preview: previewText(paragraph.PlainText())}
	return resolved
}

func findParagraphsByHeadingSelector(document *Document, selector *ActionTargetSelector) []*Paragraph {
	if document == nil || selector == nil {
		return nil
	}
	matched := make([]*Paragraph, 0)
	chapterNeedle := normalizeEditorText(selector.Chapter)
	textNeedle := normalizeEditorText(selector.TextEquals)
	regionNeedle := strings.ToLower(strings.TrimSpace(selector.Region))
	for index := range document.Paragraphs {
		paragraph := &document.Paragraphs[index]
		level := extractHeadingLevel(paragraph)
		if level <= 0 {
			continue
		}
		if selector.HeadingLevel > 0 && level != selector.HeadingLevel {
			continue
		}
		if chapterNeedle != "" {
			scope := buildHeadingScopeForParagraph(document, paragraph.ID)
			if !containsNormalizedPath(scope, chapterNeedle) {
				continue
			}
		}
		if textNeedle != "" {
			paragraphText := strings.TrimSpace(paragraph.PlainText())
			if paragraph.NumberingText != "" && !strings.HasPrefix(paragraphText, paragraph.NumberingText) {
				paragraphText = strings.TrimSpace(paragraph.NumberingText + " " + paragraphText)
			}
			if !strings.Contains(normalizeEditorText(paragraphText), textNeedle) {
				continue
			}
		}
		if regionNeedle != "" {
			path := buildParagraphPath(document, paragraph.ID)
			if len(path) == 0 || strings.ToLower(strings.TrimSpace(path[0])) != regionNeedle {
				continue
			}
		}
		matched = append(matched, paragraph)
	}
	return matched
}

func containsNormalizedPath(values []string, needle string) bool {
	for _, value := range values {
		if strings.Contains(normalizeEditorText(value), needle) {
			return true
		}
	}
	return false
}

func buildParagraphCandidate(document *Document, paragraph *Paragraph, hit *SearchHit, score int) ActionCandidate {
	if paragraph == nil {
		return ActionCandidate{TargetKind: "paragraph", Score: score}
	}
	candidate := ActionCandidate{TargetKind: "paragraph", ParagraphID: paragraph.ID, Region: paragraphRegion(hit), Path: buildParagraphPath(document, paragraph.ID), HeadingScope: buildHeadingScopeForParagraph(document, paragraph.ID), Preview: previewText(paragraph.PlainText()), Score: score}
	if hit != nil {
		candidate.MatchedText = hit.Text
		candidate.MatchStart = hit.MatchStart
		candidate.MatchEnd = hit.MatchEnd
	}
	return candidate
}

func paragraphRegion(hit *SearchHit) string {
	if hit == nil || strings.TrimSpace(hit.Region) == "" {
		return "body"
	}
	return hit.Region
}

func buildParagraphPath(document *Document, paragraphID string) []string {
	path, ok := findParagraphPathInBlocks(document.Blocks, paragraphID, []string{"body"})
	if ok {
		return path
	}
	for partIndex, part := range document.Meta.HeaderFooters {
		region := strings.TrimSpace(part.Kind)
		if region == "" {
			region = "header_footer"
		}
		path, ok = findParagraphPathInBlocks(part.Blocks, paragraphID, []string{region, fmt.Sprintf("part_%d", partIndex)})
		if ok {
			return path
		}
	}
	return []string{"paragraph", paragraphID}
}

func findParagraphPathInBlocks(blocks []Block, paragraphID string, prefix []string) ([]string, bool) {
	for blockIndex, block := range blocks {
		blockPath := append(clonePath(prefix), fmt.Sprintf("block_%d", blockIndex))
		switch block.Type {
		case BlockTypeParagraph:
			if block.Paragraph != nil && block.Paragraph.ID == paragraphID {
				return append(blockPath, block.Paragraph.ID), true
			}
		case BlockTypeTable:
			if block.Table == nil {
				continue
			}
			tablePath := append(clonePath(blockPath), block.Table.ID)
			for rowIndex, row := range block.Table.Rows {
				for cellIndex, cell := range row.Cells {
					cellPath := append(clonePath(tablePath), fmt.Sprintf("row_%d", rowIndex), fmt.Sprintf("cell_%d", cellIndex))
					if path, ok := findParagraphPathInBlocks(cell.Blocks, paragraphID, cellPath); ok {
						return path, true
					}
				}
			}
		}
	}
	return nil, false
}

func buildTablePath(document *Document, tableID string) []string {
	path, ok := findTablePathInBlocks(document.Blocks, tableID, []string{"body"})
	if ok {
		return path
	}
	for partIndex, part := range document.Meta.HeaderFooters {
		region := strings.TrimSpace(part.Kind)
		if region == "" {
			region = "header_footer"
		}
		path, ok = findTablePathInBlocks(part.Blocks, tableID, []string{region, fmt.Sprintf("part_%d", partIndex)})
		if ok {
			return path
		}
	}
	return []string{"table", tableID}
}

func findTablePathInBlocks(blocks []Block, tableID string, prefix []string) ([]string, bool) {
	for blockIndex, block := range blocks {
		blockPath := append(clonePath(prefix), fmt.Sprintf("block_%d", blockIndex))
		if block.Type != BlockTypeTable || block.Table == nil {
			continue
		}
		tablePath := append(clonePath(blockPath), block.Table.ID)
		if block.Table.ID == tableID {
			return tablePath, true
		}
		for rowIndex, row := range block.Table.Rows {
			for cellIndex, cell := range row.Cells {
				cellPath := append(clonePath(tablePath), fmt.Sprintf("row_%d", rowIndex), fmt.Sprintf("cell_%d", cellIndex))
				if path, ok := findTablePathInBlocks(cell.Blocks, tableID, cellPath); ok {
					return path, true
				}
			}
		}
	}
	return nil, false
}

func buildHeadingScopeForParagraph(document *Document, paragraphID string) []string {
	if document == nil {
		return nil
	}
	index := -1
	for paragraphIndex := range document.Paragraphs {
		if document.Paragraphs[paragraphIndex].ID == paragraphID {
			index = paragraphIndex
			break
		}
	}
	if index < 0 {
		return nil
	}
	latestByLevel := make(map[int]string)
	maxLevel := 0
	for current := 0; current <= index; current++ {
		paragraph := document.Paragraphs[current]
		level := extractHeadingLevel(&paragraph)
		if level <= 0 {
			continue
		}
		for removeLevel := level; removeLevel <= maxLevel; removeLevel++ {
			delete(latestByLevel, removeLevel)
		}
		text := strings.TrimSpace(paragraph.PlainText())
		if paragraph.NumberingText != "" && !strings.HasPrefix(text, paragraph.NumberingText) {
			text = strings.TrimSpace(paragraph.NumberingText + " " + text)
		}
		latestByLevel[level] = text
		if level > maxLevel {
			maxLevel = level
		}
	}
	if len(latestByLevel) == 0 {
		return nil
	}
	scope := make([]string, 0, len(latestByLevel))
	for level := 1; level <= maxLevel; level++ {
		if text, ok := latestByLevel[level]; ok && text != "" {
			scope = append(scope, text)
		}
	}
	return scope
}

func extractHeadingLevel(paragraph *Paragraph) int {
	if paragraph == nil {
		return 0
	}
	styleID := strings.ToLower(strings.TrimSpace(paragraph.Style.StyleID))
	if styleID == "title" {
		return 1
	}
	if strings.HasPrefix(styleID, "heading") {
		value := strings.TrimPrefix(styleID, "heading")
		if value == "" {
			return 1
		}
		var level int
		_, err := fmt.Sscanf(value, "%d", &level)
		if err == nil && level > 0 {
			return level
		}
	}
	text := strings.TrimSpace(paragraph.PlainText())
	if textLooksLikeHeading(text) {
		return 1
	}
	return 0
}

func textLooksLikeHeading(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}
	if strings.HasPrefix(trimmed, "一、") || strings.HasPrefix(trimmed, "二、") || strings.HasPrefix(trimmed, "三、") || strings.HasPrefix(trimmed, "四、") || strings.HasPrefix(trimmed, "五、") {
		return true
	}
	if strings.HasPrefix(trimmed, "第") && strings.Contains(trimmed, "章") {
		return true
	}
	if strings.HasPrefix(trimmed, "1.") || strings.HasPrefix(trimmed, "2.") || strings.HasPrefix(trimmed, "3.") {
		return true
	}
	return false
}

func previewText(text string) string {
	trimmed := strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	runes := []rune(trimmed)
	if len(runes) <= 48 {
		return trimmed
	}
	return string(runes[:48]) + "..."
}

func clonePath(path []string) []string {
	if len(path) == 0 {
		return nil
	}
	cloned := make([]string, len(path))
	copy(cloned, path)
	return cloned
}

func joinParagraphTexts(paragraphs []Paragraph) string {
	parts := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		parts = append(parts, paragraph.PlainText())
	}
	return strings.Join(parts, "\n")
}

func firstRelevantDiff(diffs []ParagraphDiff, activeParagraphID string) *ParagraphDiff {
	if activeParagraphID != "" {
		for index := range diffs {
			if diffs[index].ParagraphID == activeParagraphID {
				return &diffs[index]
			}
		}
	}
	if len(diffs) == 0 {
		return nil
	}
	return &diffs[0]
}

func hasInsertedCharacter(ranges []CharacterRangeDiff, value string) bool {
	for _, rng := range ranges {
		if rng.Type == DiffInserted && rng.AfterText == value {
			return true
		}
	}
	return false
}

func hasDiffType(diffs []ParagraphDiff, diffType DiffType) bool {
	for _, diff := range diffs {
		if diff.Type == diffType {
			return true
		}
	}
	return false
}

func validateInsertedParagraphStructure(document *Document, action EditorAction, activeParagraphID string) bool {
	if document == nil || strings.TrimSpace(action.NewText) == "" {
		return false
	}
	paragraph := locateInsertedParagraph(document, activeParagraphID, action.Position)
	if paragraph == nil {
		return false
	}
	if normalizeEditorText(paragraph.PlainText()) != normalizeEditorText(action.NewText) {
		return false
	}
	anchor := locateParagraphByID(document, activeParagraphID)
	if anchor == nil {
		return true
	}
	templateParagraph, _ := resolveInsertTemplate(document, anchor, action)
	expectedStyle := mergeTemplateParagraphStyleWithConstraints(templateParagraph, action.ParagraphStylePatch, action.Constraints)
	return paragraphStyleMatchesExpectation(paragraph.Style, expectedStyle)
}

func locateInsertedParagraph(document *Document, activeParagraphID string, position string) *Paragraph {
	if document == nil {
		return nil
	}
	trimmedAnchorID := strings.TrimSpace(activeParagraphID)
	if trimmedAnchorID == "" {
		for index := len(document.Blocks) - 1; index >= 0; index-- {
			if document.Blocks[index].Type == BlockTypeParagraph && document.Blocks[index].Paragraph != nil {
				return document.Blocks[index].Paragraph
			}
		}
		return nil
	}
	location, err := findParagraphLocation(document, trimmedAnchorID)
	if err != nil {
		return nil
	}
	insertIndex := location.blockIndex + 1
	if strings.EqualFold(position, "before") {
		insertIndex = location.blockIndex - 1
	}
	if insertIndex < 0 || insertIndex >= len(*location.blocks) {
		return nil
	}
	block := (*location.blocks)[insertIndex]
	if block.Type != BlockTypeParagraph || block.Paragraph == nil {
		return nil
	}
	return block.Paragraph
}

func paragraphStyleMatchesExpectation(actual ParagraphStyle, expected ParagraphStyle) bool {
	if strings.TrimSpace(expected.StyleID) != "" && strings.TrimSpace(actual.StyleID) != strings.TrimSpace(expected.StyleID) {
		return false
	}
	if expected.Numbering != nil {
		return sameNumberingRef(actual.Numbering, expected.Numbering)
	}
	return true
}

func validateTextTransitionByDiff(diff *ParagraphDiff, beforeText, afterText string) bool {
	if diff == nil {
		return false
	}
	for _, rng := range diff.CharacterRanges {
		if textTransitionMatches(rng.BeforeText, rng.AfterText, beforeText, afterText) {
			return true
		}
	}
	return textTransitionMatches(diff.BeforeText, diff.AfterText, beforeText, afterText)
}

func textTransitionMatches(beforeText, afterText, expectedBefore, expectedAfter string) bool {
	normalizedExpectedBefore := normalizeEditorText(expectedBefore)
	normalizedExpectedAfter := normalizeEditorText(expectedAfter)
	normalizedBefore := normalizeEditorText(beforeText)
	normalizedAfter := normalizeEditorText(afterText)
	if normalizedExpectedBefore == "" && normalizedExpectedAfter == "" {
		return false
	}
	if normalizedExpectedBefore != "" && !strings.Contains(normalizedBefore, normalizedExpectedBefore) {
		return false
	}
	if normalizedExpectedAfter != "" && !strings.Contains(normalizedAfter, normalizedExpectedAfter) {
		return false
	}
	if normalizedExpectedBefore != "" && normalizedExpectedAfter == "" {
		return normalizedAfter == "" || !strings.Contains(normalizedAfter, normalizedExpectedBefore)
	}
	if normalizedExpectedBefore == normalizedExpectedAfter {
		return normalizedBefore != normalizedAfter
	}
	return normalizedBefore != normalizedAfter
}

func validateTableCellStructure(document *Document, action EditorAction) bool {
	if document == nil || action.TableCell == nil {
		return false
	}
	tableID, rowIndex, cellIndex, ok := resolveValidationTableCellTarget(action)
	if !ok {
		return false
	}
	table, err := findTable(document, tableID)
	if err != nil || rowIndex < 0 || rowIndex >= len(table.Rows) {
		return false
	}
	if cellIndex < 0 || cellIndex >= len(table.Rows[rowIndex].Cells) {
		return false
	}
	expected := normalizeEditorText(tableCellInputPlainText(*action.TableCell))
	actual := normalizeEditorText(tableCellPlainText(table.Rows[rowIndex].Cells[cellIndex]))
	if expected == "" {
		return actual == ""
	}
	return actual == expected
}

func validateInsertedTableStructure(document *Document, action EditorAction, activeParagraphID string) bool {
	if document == nil || action.TableSpec == nil || len(action.TableSpec.Rows) == 0 {
		return false
	}
	table := locateInsertedTable(document, activeParagraphID, action.Position)
	if table == nil {
		return false
	}
	return tableInputMatchesTable(*action.TableSpec, *table)
}

func locateInsertedTable(document *Document, activeParagraphID string, position string) *Table {
	if document == nil {
		return nil
	}
	trimmedAnchorID := strings.TrimSpace(activeParagraphID)
	if trimmedAnchorID == "" {
		for index := len(document.Blocks) - 1; index >= 0; index-- {
			if document.Blocks[index].Type == BlockTypeTable && document.Blocks[index].Table != nil {
				return document.Blocks[index].Table
			}
		}
		return nil
	}
	location, err := findParagraphLocation(document, trimmedAnchorID)
	if err != nil {
		return nil
	}
	insertIndex := location.blockIndex + 1
	if strings.EqualFold(position, "before") {
		insertIndex = location.blockIndex - 1
	}
	if insertIndex < 0 || insertIndex >= len(*location.blocks) {
		return nil
	}
	block := (*location.blocks)[insertIndex]
	if block.Type != BlockTypeTable || block.Table == nil {
		return nil
	}
	return block.Table
}

func tableInputMatchesTable(input TableInput, table Table) bool {
	if len(input.Rows) != len(table.Rows) {
		return false
	}
	for rowIndex, rowInput := range input.Rows {
		if len(rowInput.Cells) != len(table.Rows[rowIndex].Cells) {
			return false
		}
		for cellIndex, cellInput := range rowInput.Cells {
			expected := normalizeEditorText(tableCellInputPlainText(cellInput))
			actual := normalizeEditorText(tableCellPlainText(table.Rows[rowIndex].Cells[cellIndex]))
			if expected != actual {
				return false
			}
		}
	}
	return true
}

func validateSectionLayoutStructure(document *Document, patch SectionLayout) bool {
	if document == nil {
		return false
	}
	actual := document.Meta.SectionLayout
	if patch.PageWidth > 0 && actual.PageWidth != patch.PageWidth {
		return false
	}
	if patch.PageHeight > 0 && actual.PageHeight != patch.PageHeight {
		return false
	}
	if strings.TrimSpace(patch.Orientation) != "" && !strings.EqualFold(actual.Orientation, patch.Orientation) {
		return false
	}
	if strings.TrimSpace(patch.BreakType) != "" && !strings.EqualFold(actual.BreakType, patch.BreakType) {
		return false
	}
	if patch.MarginTop > 0 && actual.MarginTop != patch.MarginTop {
		return false
	}
	if patch.MarginRight > 0 && actual.MarginRight != patch.MarginRight {
		return false
	}
	if patch.MarginBottom > 0 && actual.MarginBottom != patch.MarginBottom {
		return false
	}
	if patch.MarginLeft > 0 && actual.MarginLeft != patch.MarginLeft {
		return false
	}
	if patch.MarginHeader > 0 && actual.MarginHeader != patch.MarginHeader {
		return false
	}
	if patch.MarginFooter > 0 && actual.MarginFooter != patch.MarginFooter {
		return false
	}
	if patch.MarginGutter > 0 && actual.MarginGutter != patch.MarginGutter {
		return false
	}
	if patch.PageNumStart > 0 && actual.PageNumStart != patch.PageNumStart {
		return false
	}
	if strings.TrimSpace(patch.PageNumFmt) != "" && !strings.EqualFold(actual.PageNumFmt, patch.PageNumFmt) {
		return false
	}
	if patch.TitlePageSet && actual.TitlePage != patch.TitlePage {
		return false
	}
	return true
}

func validateHeaderFooterStructure(document *Document, headerFooter *HeaderFooterInput) bool {
	if document == nil || headerFooter == nil {
		return false
	}
	actual := findHeaderFooterMatch(document, headerFooter)
	if actual == nil {
		return false
	}
	expected, err := buildHeaderFooterPartFromInput(*headerFooter)
	if err != nil {
		return false
	}
	return blocksStructureSignature(actual.Blocks) == blocksStructureSignature(expected.Blocks)
}

func resolveValidationTableCellTarget(action EditorAction) (string, int, int, bool) {
	if action.TargetSelector != nil && strings.EqualFold(action.TargetSelector.Type, "table_cell_selector") {
		tableID := strings.TrimSpace(action.TargetSelector.TableID)
		if tableID != "" {
			return tableID, action.TargetSelector.RowIndex, action.TargetSelector.CellIndex, true
		}
	}
	tableID := strings.TrimSpace(action.TableID)
	if tableID == "" {
		return "", 0, 0, false
	}
	return tableID, action.RowIndex, action.CellIndex, true
}

func tableCellInputPlainText(cell TableCellInput) string {
	parts := make([]string, 0, len(cell.Paragraphs))
	for _, paragraph := range cell.Paragraphs {
		text := strings.TrimSpace(paragraph.Text)
		if text == "" && len(paragraph.Runs) > 0 {
			text = strings.TrimSpace(Paragraph{Runs: paragraph.Runs}.PlainText())
		}
		if text != "" {
			parts = append(parts, text)
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func findHeaderFooterMatch(document *Document, headerFooter *HeaderFooterInput) *HeaderFooterPart {
	if document == nil || headerFooter == nil {
		return nil
	}
	variant := headerFooter.Variant
	if variant == "" {
		variant = "default"
	}
	for index := range document.Meta.HeaderFooters {
		part := &document.Meta.HeaderFooters[index]
		partVariant := part.Variant
		if partVariant == "" {
			partVariant = "default"
		}
		if strings.EqualFold(part.Kind, headerFooter.Kind) && strings.EqualFold(partVariant, variant) {
			return part
		}
	}
	return nil
}

func resolveFocusParagraphID(diffs []ParagraphDiff, fallback string) string {
	for _, diff := range diffs {
		if diff.Type == DiffInserted || diff.Type == DiffUpdated {
			return diff.ParagraphID
		}
	}
	if len(diffs) > 0 {
		return diffs[0].ParagraphID
	}
	return fallback
}

func fallbackTargetText(action EditorAction) string {
	if strings.TrimSpace(action.TargetText) != "" {
		return action.TargetText
	}
	if strings.TrimSpace(action.RetrievalQuery) != "" {
		return action.RetrievalQuery
	}
	return action.Action
}

func fallbackQuery(action EditorAction) string {
	if strings.TrimSpace(action.RetrievalQuery) != "" {
		return action.RetrievalQuery
	}
	return action.TargetText
}

func ambiguousParagraphDiagnostics(document *Document, action EditorAction, query string, scope string, hits []SearchHit, attempted []string) *ActionDiagnostics {
	candidates := make([]ActionCandidate, 0, len(hits))
	for index, hit := range hits {
		paragraph := locateParagraphByID(document, hit.ParagraphID)
		candidates = append(candidates, buildParagraphCandidate(document, paragraph, &hit, len(hits)-index))
	}
	if len(candidates) > 3 {
		candidates = candidates[:3]
	}
	return applyAmbiguityPolicy(action, &ActionDiagnostics{ErrorCode: "AMBIGUOUS_TARGET", Message: fmt.Sprintf("命中 %d 个候选段落，无法安全直接修改", len(hits)), TargetKind: "paragraph", Query: query, Scope: scope, CandidateCount: len(hits), Candidates: candidates, AttemptedStrategies: attempted, RecommendedActions: []string{"补充章节范围", "指定 preferred_paragraph_id", "把 target_text 改成文档中更完整的原文片段"}})
}

func applyAmbiguityPolicy(action EditorAction, diagnostics *ActionDiagnostics) *ActionDiagnostics {
	if diagnostics == nil || diagnostics.ErrorCode != "AMBIGUOUS_TARGET" {
		return diagnostics
	}
	if shouldReturnAmbiguityCandidates(action) {
		cloned := *diagnostics
		cloned.Retryable = true
		return &cloned
	}
	cloned := *diagnostics
	cloned.Candidates = nil
	cloned.Retryable = false
	if strings.TrimSpace(cloned.Message) == "" {
		cloned.Message = "目标存在歧义，请补充更精确的结构范围后重试"
	}
	return &cloned
}

func selectBestParagraphTargetFromHits(document *Document, action EditorAction, hits []SearchHit, needRange bool) *resolvedParagraphTarget {
	if !allowBestCandidateResolution(action) {
		return nil
	}
	for _, hit := range hits {
		paragraph := locateParagraphByID(document, hit.ParagraphID)
		if paragraph == nil {
			continue
		}
		resolved := buildResolvedParagraphTarget(document, paragraph, &hit, action)
		if !needRange || action.Action == "insert" || action.Action == "insert_table" || action.Action == "format_paragraph" {
			return resolved
		}
		start, end, ok := resolveMatchedTargetRangeWithPolicy(paragraph, action, &hit)
		if !ok {
			continue
		}
		resolved.rangeStart = start
		resolved.rangeEnd = end
		resolved.resolved.MatchStart = start
		resolved.resolved.MatchEnd = end
		return resolved
	}
	return nil
}

func validationFailure(action EditorAction, message string) ActionValidation {
	diagnostics := &ActionDiagnostics{
		ErrorCode:          "VALIDATION_FAILED",
		Message:            message,
		TargetKind:         validationTargetKind(action),
		Query:              fallbackQuery(action),
		RecommendedActions: validationRecommendedActions(action),
		Retryable:          action.FailurePolicy == nil || action.FailurePolicy.OnValidationFailed != "rollback_and_report",
	}
	return ActionValidation{OK: false, Message: message, ErrorCode: "VALIDATION_FAILED", Diagnostics: diagnostics}
}

func validationTargetKind(action EditorAction) string {
	switch action.Action {
	case "insert_table", "insert_table_row", "delete_table_row", "insert_table_column", "delete_table_column":
		return "table"
	case "update_table_cell":
		return "table_cell"
	case "update_header_footer":
		return "header_footer"
	case "format_section":
		return "section"
	default:
		return "paragraph"
	}
}

func validationRecommendedActions(action EditorAction) []string {
	actions := []string{"自动重读最新文档并重新规划当前步骤", "若目标结构已变化，优先使用 selector 与范围证据重新定位"}
	if action.FailurePolicy != nil && action.FailurePolicy.OnValidationFailed == "rollback_and_report" {
		actions = append([]string{"按 failure_policy 回滚到执行前版本并返回诊断"}, actions...)
	}
	return actions
}

func preflightTableStructureAction(document *Document, action EditorAction, table *Table) *ActionDiagnostics {
	conflict := func(message string, targetKind string, recommended []string, retryable bool) *ActionDiagnostics {
		return &ActionDiagnostics{
			ErrorCode:          "TABLE_STRUCTURE_CONFLICT",
			Message:            message,
			TargetKind:         targetKind,
			RecommendedActions: recommended,
			Retryable:          retryable,
		}
	}
	if table == nil {
		return conflict("目标表格为空，无法执行结构化编辑", "table", []string{"重新获取最新 table_id", "先通过 IR 查询确认目标表格结构"}, true)
	}
	rowCount := len(table.Rows)
	columnCount := maxTableColumns(table)
	if rowCount == 0 {
		return conflict(fmt.Sprintf("表格 %s 没有可编辑的行结构", table.ID), "table", []string{"先修复表格结构后再编辑", "改用插入表格重新建立结构"}, false)
	}
	if action.Action == "insert_table_row" || action.Action == "delete_table_row" || action.Action == "insert_table_column" || action.Action == "delete_table_column" {
		if tableRequiresLayoutGuard(document, table) {
			return &ActionDiagnostics{
				ErrorCode:  "TABLE_LAYOUT_CONFLICT",
				Message:    fmt.Sprintf("表格 %s 疑似跨页或带有分页敏感行属性，当前结构修改风险过高", table.ID),
				TargetKind: "table",
				RecommendedActions: []string{
					"先通过 layout/IR 确认目标表格是否跨页或包含 repeat_header/cant_split 行",
					"优先改为 update_table_cell 等非结构修改",
					"若必须改结构，请先缩小到明确表格并重新规划行列变更",
				},
				Retryable: false,
			}
		}
	}
	switch action.Action {
	case "delete_table_row":
		if rowCount <= 1 {
			return conflict(fmt.Sprintf("表格 %s 仅剩 1 行，不能再删除", table.ID), "table", []string{"至少保留一行表格", "若需清空内容，可改为更新单元格为空"}, false)
		}
		if action.RowIndex < 0 || action.RowIndex >= rowCount {
			return conflict(fmt.Sprintf("表格 %s 的 row_index=%d 超出范围", table.ID, action.RowIndex), "table", []string{"补充正确的 row_index", "先通过 IR 查询确认表格行数"}, true)
		}
	case "insert_table_row":
		if action.RowIndex < 0 || action.RowIndex >= rowCount {
			return conflict(fmt.Sprintf("表格 %s 的 row_index=%d 超出范围，无法确定插入位置", table.ID, action.RowIndex), "table", []string{"补充正确的 row_index", "先通过 IR 查询确认表格行数"}, true)
		}
	case "delete_table_column":
		if tableHasUnsupportedColumnStructure(table) {
			return conflict(fmt.Sprintf("表格 %s 包含合并单元格或不规则列结构，当前不支持安全删除列", table.ID), "table", []string{"改为 update_table_cell 等非结构修改", "若必须删列，请先将合并单元格展开为规则表格"}, false)
		}
		if columnCount <= 1 {
			return conflict(fmt.Sprintf("表格 %s 仅剩 1 列，不能再删除", table.ID), "table", []string{"至少保留一列表格", "若需清空内容，可改为更新单元格为空"}, false)
		}
		if action.CellIndex < 0 || action.CellIndex >= columnCount {
			return conflict(fmt.Sprintf("表格 %s 的 cell_index=%d 超出范围", table.ID, action.CellIndex), "table", []string{"补充正确的 cell_index", "先通过 IR 查询确认表格列数"}, true)
		}
	case "insert_table_column":
		if tableHasUnsupportedColumnStructure(table) {
			return conflict(fmt.Sprintf("表格 %s 包含合并单元格或不规则列结构，当前不支持安全插入列", table.ID), "table", []string{"改为 update_table_cell 等非结构修改", "若必须插列，请先将合并单元格展开为规则表格"}, false)
		}
		if action.CellIndex < 0 || action.CellIndex >= columnCount {
			return conflict(fmt.Sprintf("表格 %s 的 cell_index=%d 超出范围，无法确定插入位置", table.ID, action.CellIndex), "table", []string{"补充正确的 cell_index", "先通过 IR 查询确认表格列数"}, true)
		}
		if action.TableColumn != nil {
			cellCount := len(action.TableColumn.Cells)
			if cellCount != 0 && cellCount != 1 && cellCount != rowCount {
				return conflict(fmt.Sprintf("table_column 提供了 %d 个单元格模板，但目标表格有 %d 行", cellCount, rowCount), "table", []string{"让 table_column.cells 为空", "只提供 1 个通用单元格模板", "或按表格行数提供等长 cells"}, true)
			}
		}
	case "update_table_cell":
		if action.RowIndex < 0 || action.RowIndex >= rowCount {
			return conflict(fmt.Sprintf("表格 %s 的 row_index=%d 超出范围", table.ID, action.RowIndex), "table_cell", []string{"补充正确的 row_index", "先通过 IR 查询确认表格行数"}, true)
		}
		if action.CellIndex < 0 || action.CellIndex >= len(table.Rows[action.RowIndex].Cells) {
			return conflict(fmt.Sprintf("表格 %s 第 %d 行的 cell_index=%d 超出范围", table.ID, action.RowIndex+1, action.CellIndex), "table_cell", []string{"补充正确的 cell_index", "先通过 IR 查询确认目标行的单元格数量"}, true)
		}
	}
	return nil
}

func tableRequiresLayoutGuard(document *Document, table *Table) bool {
	if table == nil {
		return false
	}
	pageContentHeight := layoutContentHeight(document)
	_, tableHeight := buildSemanticTableLayout(*table, 0)
	if pageContentHeight > 0 && tableHeight > pageContentHeight {
		return true
	}
	for _, row := range table.Rows {
		if row.RepeatHeaderSet || row.RepeatHeader || row.CantSplitSet || row.CantSplit {
			return true
		}
		for _, cell := range row.Cells {
			if blocksContainPaginationRisk(cell.Blocks) || blocksContainNestedTable(cell.Blocks) {
				return true
			}
		}
	}
	return false
}

func layoutContentHeight(document *Document) int {
	layout := SectionLayout{PageWidth: 11906, PageHeight: 16838, MarginTop: 1440, MarginBottom: 1440}
	if document != nil && document.Meta.SectionLayout.PageHeight != 0 {
		layout = document.Meta.SectionLayout
	}
	height := layout.PageHeight - layout.MarginTop - layout.MarginBottom
	if height <= 0 {
		return 0
	}
	return height
}

func blocksContainNestedTable(blocks []Block) bool {
	for _, block := range blocks {
		if block.Type == BlockTypeTable && block.Table != nil {
			return true
		}
	}
	return false
}

func blocksContainPaginationRisk(blocks []Block) bool {
	for _, block := range blocks {
		if block.Type != BlockTypeParagraph || block.Paragraph == nil {
			continue
		}
		paragraph := block.Paragraph
		if paragraph.Style.PageBreakBeforeSet || paragraph.Style.PageBreakBefore {
			return true
		}
		for _, run := range paragraph.Runs {
			if run.BreakType == RunBreakTypePage || run.BreakType == RunBreakTypeRenderedPage {
				return true
			}
		}
	}
	return false
}

func preflightHeaderFooterAction(document *Document, action EditorAction) *ActionDiagnostics {
	if document == nil || action.HeaderFooter == nil {
		return nil
	}
	kind := normalizeHeaderFooterKind(action.HeaderFooter.Kind)
	variant := normalizeHeaderFooterVariant(action.HeaderFooter.Variant)
	if kind == "" {
		return nil
	}
	matched := collectMatchingHeaderFooterSignatures(document, kind, variant)
	if len(matched) <= 1 {
		return nil
	}
	return &ActionDiagnostics{
		ErrorCode:  "HEADER_FOOTER_CONFLICT",
		Message:    fmt.Sprintf("文档存在 %d 组不同的%s绑定，当前 payload 无法安全确定要覆盖哪一组", len(matched), describeHeaderFooterKindVariant(kind, variant)),
		TargetKind: "header_footer",
		Scope:      variant,
		RecommendedActions: []string{
			"先通过 IR 或结构视图确认具体 section 的页眉页脚绑定",
			"补充 section 级范围后再编辑",
			"若确需统一替换，请先把所有 section 绑定统一收敛到单一目标后再执行",
		},
		Retryable: false,
	}
}

func preflightSectionLayoutAction(document *Document, action EditorAction) *ActionDiagnostics {
	if document == nil {
		return nil
	}
	signatures := collectDistinctSectionLayoutSignatures(document)
	if len(signatures) <= 1 {
		return nil
	}
	return &ActionDiagnostics{
		ErrorCode:  "INSUFFICIENT_SCOPE",
		Message:    fmt.Sprintf("文档包含 %d 种 section 布局，当前 section_layout_patch 缺少明确 section 范围", len(signatures)),
		TargetKind: "section",
		RecommendedActions: []string{
			"先通过 IR 查看各 section_signature",
			"补充 section 级 selector 或范围后再执行",
			"若确需统一调整，请先让系统按统一布局目标重规划后再执行",
		},
		Retryable: false,
	}
}

func preflightInsertAction(document *Document, action EditorAction, templateParagraph *Paragraph, templateRole string) *ActionDiagnostics {
	if action.Action != "insert" || action.Constraints == nil || !action.Constraints.preserveNumberingEnabled() {
		return nil
	}
	if templateParagraph == nil || templateParagraph.Style.Numbering == nil || action.ParagraphStylePatch.Numbering == nil {
		if templateParagraph != nil && templateParagraph.Style.Numbering != nil && numberingChainIsComplex(document, templateParagraph.ID) {
			return &ActionDiagnostics{
				ErrorCode:  "NUMBERING_CONFLICT",
				Message:    fmt.Sprintf("当前插入位置位于复杂编号链中，仅凭%s模板无法安全推断新段落应继承哪一条编号链", templateRole),
				TargetKind: "paragraph",
				RecommendedActions: []string{
					"补充明确的 paragraph_style_patch.numbering 或关闭 preserve_numbering",
					"先缩小到单一编号链范围后再执行插入",
					"必要时先读取前后编号段落，确认新段落应接入的层级与 num_id",
				},
				Retryable: false,
			}
		}
		return nil
	}
	if sameNumberingRef(templateParagraph.Style.Numbering, action.ParagraphStylePatch.Numbering) {
		return nil
	}
	return &ActionDiagnostics{
		ErrorCode:  "NUMBERING_CONFLICT",
		Message:    fmt.Sprintf("当前插入计划要求继承%s模板编号，但 paragraph_style_patch 又显式指定了不同的编号引用", templateRole),
		TargetKind: "paragraph",
		RecommendedActions: []string{
			"移除 paragraph_style_patch.numbering，直接继承模板编号",
			"或将 preserve_numbering 设为 false 后再显式指定新编号",
			"确认目标段落应沿用原编号链还是切换到新的编号定义",
		},
		Retryable: false,
	}
}

func numberingChainIsComplex(document *Document, paragraphID string) bool {
	if document == nil || strings.TrimSpace(paragraphID) == "" {
		return false
	}
	index := -1
	for paragraphIndex := range document.Paragraphs {
		if document.Paragraphs[paragraphIndex].ID == paragraphID {
			index = paragraphIndex
			break
		}
	}
	if index < 0 {
		return false
	}
	current := document.Paragraphs[index].Style.Numbering
	if current == nil {
		return false
	}
	neighbors := make([]*NumberingRef, 0, 2)
	if index > 0 {
		neighbors = append(neighbors, document.Paragraphs[index-1].Style.Numbering)
	}
	if index+1 < len(document.Paragraphs) {
		neighbors = append(neighbors, document.Paragraphs[index+1].Style.Numbering)
	}
	for _, neighbor := range neighbors {
		if neighbor == nil {
			continue
		}
		if !sameNumberingRef(current, neighbor) {
			return true
		}
	}
	return false
}

func preflightCrossChapterScope(document *Document, action EditorAction) *ActionDiagnostics {
	if document == nil || !allowBestCandidateResolution(action) {
		return nil
	}
	if action.TargetSelector != nil || strings.TrimSpace(action.PreferredParagraphID) != "" || strings.TrimSpace(action.ContextScope) != "" {
		return nil
	}
	query := fallbackQuery(action)
	if strings.TrimSpace(query) == "" {
		return nil
	}
	searchResult := searchDocument(document, SearchRequest{Query: query, Scope: "body", Mode: "contains", Limit: 20})
	hits := filterParagraphHits(searchResult.Hits)
	if len(hits) <= 1 {
		return nil
	}
	scopes := make(map[string]struct{})
	for _, hit := range hits {
		scope := strings.Join(buildHeadingScopeForParagraph(document, hit.ParagraphID), " / ")
		scope = strings.TrimSpace(scope)
		if scope == "" {
			scope = "body"
		}
		scopes[scope] = struct{}{}
	}
	if len(scopes) <= 1 {
		return nil
	}
	return &ActionDiagnostics{
		ErrorCode:  "INSUFFICIENT_SCOPE",
		Message:    fmt.Sprintf("查询“%s”跨越多个章节范围，当前自动择优策略风险过高", query),
		TargetKind: "paragraph",
		Query:      query,
		RecommendedActions: []string{
			"补充 chapter、heading_scope_selector 或更具体的 target_selector",
			"先通过 IR/搜索确认唯一章节后再执行",
			"不要在跨章节命中场景下直接使用 use_best_candidate",
		},
		Retryable: true,
	}
}

func blocksStructureSignature(blocks []Block) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case BlockTypeTable:
			if block.Table != nil {
				parts = append(parts, tableStructureSignature(*block.Table))
			}
		default:
			if block.Paragraph != nil {
				parts = append(parts, "p:"+normalizeEditorText(block.Paragraph.PlainText()))
			}
		}
	}
	return strings.Join(parts, "||")
}

func tableStructureSignature(table Table) string {
	parts := []string{fmt.Sprintf("tbl:%d", len(table.Rows))}
	for _, row := range table.Rows {
		parts = append(parts, fmt.Sprintf("row:%d", len(row.Cells)))
		for _, cell := range row.Cells {
			parts = append(parts, "cell:"+blocksStructureSignature(cell.Blocks))
		}
	}
	return strings.Join(parts, "|")
}

func collectMatchingHeaderFooterSignatures(document *Document, kind string, variant string) []string {
	seen := make(map[string]struct{})
	ordered := make([]string, 0)
	appendSignature := func(part HeaderFooterPart) {
		if normalizeHeaderFooterKind(part.Kind) != kind || normalizeHeaderFooterVariant(part.Variant) != variant {
			return
		}
		signature := strings.Join([]string{
			kind,
			variant,
			strings.TrimSpace(part.RelationshipID),
			strings.TrimSpace(part.Target),
			previewText(joinParagraphTexts(part.Paragraphs)),
		}, "|")
		if _, ok := seen[signature]; ok {
			return
		}
		seen[signature] = struct{}{}
		ordered = append(ordered, signature)
	}
	for _, part := range document.Meta.HeaderFooters {
		appendSignature(part)
	}
	for _, section := range document.Meta.Sections {
		for _, part := range section.HeaderFooters {
			appendSignature(part)
		}
	}
	return ordered
}

func collectDistinctSectionLayoutSignatures(document *Document) []string {
	seen := make(map[string]struct{})
	ordered := make([]string, 0)
	appendSignature := func(layout SectionLayout) {
		signature := formatSectionSignature(layout)
		if strings.TrimSpace(signature) == "" {
			return
		}
		if _, ok := seen[signature]; ok {
			return
		}
		seen[signature] = struct{}{}
		ordered = append(ordered, signature)
	}
	appendSignature(document.Meta.SectionLayout)
	for _, section := range document.Meta.Sections {
		appendSignature(section.Layout)
	}
	for _, paragraph := range document.Paragraphs {
		if paragraph.SectionBreakLayout != nil {
			appendSignature(*paragraph.SectionBreakLayout)
		}
	}
	return ordered
}

func sameNumberingRef(left *NumberingRef, right *NumberingRef) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return strings.TrimSpace(left.NumID) == strings.TrimSpace(right.NumID) && left.Level == right.Level
}

func describeHeaderFooterKindVariant(kind string, variant string) string {
	if variant == "default" || strings.TrimSpace(variant) == "" {
		if kind == "footer" {
			return "默认页脚"
		}
		return "默认页眉"
	}
	label := "页眉"
	if kind == "footer" {
		label = "页脚"
	}
	return variant + label
}

func mergeValidationDiagnostics(diagnostics *ActionDiagnostics, message string) *ActionDiagnostics {
	if diagnostics == nil {
		return &ActionDiagnostics{ErrorCode: "VALIDATION_FAILED", Message: message}
	}
	cloned := *diagnostics
	cloned.Message = message
	if cloned.ErrorCode == "" {
		cloned.ErrorCode = "VALIDATION_FAILED"
	}
	return &cloned
}

func failedPlan(action EditorAction, errorCode string, message string, targetKind string) *ActionPlanResult {
	return &ActionPlanResult{Status: "failed", StatusText: message, ReplayAction: action, Diagnostics: &ActionDiagnostics{ErrorCode: errorCode, Message: message, TargetKind: targetKind}}
}

func finalizeActionPlanResult(plan *ActionPlanResult) *ActionPlanResult {
	if plan == nil {
		return nil
	}
	if plan.AnchorHealth == nil {
		plan.AnchorHealth = diagnoseAnchorHealth(plan.ReplayAction, plan)
	}
	plan.Diagnostics = withRetryProtocol(plan.Diagnostics, plan.ReplayAction)
	if plan.Diagnostics != nil && plan.Diagnostics.RecoveryPlan == nil {
		plan.Diagnostics.RecoveryPlan = buildFailureRecoveryPlan(plan.Diagnostics, plan.ReplayAction, plan.AnchorHealth)
	}
	return plan
}

func finalizeActionExecuteResult(result *ActionExecuteResult) *ActionExecuteResult {
	if result == nil {
		return nil
	}
	result.Plan = finalizeActionPlanResult(result.Plan)
	if result.AnchorHealth == nil {
		if result.Plan != nil && result.Plan.AnchorHealth != nil {
			result.AnchorHealth = cloneAnchorHealth(result.Plan.AnchorHealth)
		} else {
			result.AnchorHealth = diagnoseAnchorHealth(result.ReplayAction, result.Plan)
		}
	}
	result.Diagnostics = withRetryProtocol(result.Diagnostics, result.ReplayAction)
	result.Validation.Diagnostics = withRetryProtocol(result.Validation.Diagnostics, result.ReplayAction)
	if result.Diagnostics != nil && result.Diagnostics.RecoveryPlan == nil {
		result.Diagnostics.RecoveryPlan = buildFailureRecoveryPlan(result.Diagnostics, result.ReplayAction, result.AnchorHealth)
	}
	if result.Validation.Diagnostics != nil && result.Validation.Diagnostics.RecoveryPlan == nil {
		result.Validation.Diagnostics.RecoveryPlan = buildFailureRecoveryPlan(result.Validation.Diagnostics, result.ReplayAction, result.AnchorHealth)
	}
	return result
}

func withRetryProtocol(diagnostics *ActionDiagnostics, replayAction EditorAction) *ActionDiagnostics {
	if diagnostics == nil {
		return nil
	}
	copyDiagnostics := *diagnostics
	if copyDiagnostics.RetryAction == nil {
		actionCopy := replayAction
		copyDiagnostics.RetryAction = &actionCopy
	}
	if strings.TrimSpace(copyDiagnostics.RetryPrompt) == "" {
		copyDiagnostics.RetryPrompt = buildRetryPrompt(replayAction, &copyDiagnostics)
	}
	return &copyDiagnostics
}

func buildRetryPrompt(action EditorAction, diagnostics *ActionDiagnostics) string {
	if diagnostics == nil {
		return ""
	}
	actionJSON, err := json.Marshal(action)
	if err != nil {
		actionJSON = []byte(`{"action":"` + strings.TrimSpace(action.Action) + `"}`)
	}
	candidateLines := make([]string, 0, min(5, len(diagnostics.Candidates)))
	for index, candidate := range diagnostics.Candidates {
		if index >= 5 {
			break
		}
		candidateLines = append(candidateLines, summarizeRetryCandidate(candidate, index))
	}
	recommendedActions := make([]string, 0, min(5, len(diagnostics.RecommendedActions)))
	for index, item := range diagnostics.RecommendedActions {
		trimmed := normalizeRetryText(item)
		if trimmed == "" {
			continue
		}
		recommendedActions = append(recommendedActions, fmt.Sprintf("%d. %s", len(recommendedActions)+1, trimmed))
		if index >= 4 {
			break
		}
	}
	attempted := make([]string, 0, len(diagnostics.AttemptedStrategies))
	for _, item := range diagnostics.AttemptedStrategies {
		trimmed := normalizeRetryText(item)
		if trimmed != "" {
			attempted = append(attempted, trimmed)
		}
	}
	lines := []string{
		"[WORD_AUTO_RELOCATE]",
		"上一条 Word 编辑动作在前端实际落地时失败。不要把之前 auramate-word-copilot 返回的成功信号当成真正成功；请以这次失败诊断为准，继续完成同一个编辑目标。",
		"frontend_apply_status: failed",
		"previous_success_signal_invalidated: true",
		fmt.Sprintf("error_code: %s", fallbackRetryField(diagnostics.ErrorCode, "UNKNOWN")),
		fmt.Sprintf("error_message: %s", fallbackRetryField(diagnostics.Message, "未提供错误信息")),
		fmt.Sprintf("target_kind: %s", fallbackRetryField(diagnostics.TargetKind, "unknown")),
		fmt.Sprintf("query: %s", fallbackRetryField(diagnostics.Query, "none")),
		fmt.Sprintf("scope: %s", fallbackRetryField(diagnostics.Scope, "none")),
		fmt.Sprintf("retryable: %t", diagnostics.Retryable),
		fmt.Sprintf("candidate_count: %d", max(len(diagnostics.Candidates), diagnostics.CandidateCount)),
		fmt.Sprintf("retry_action: %s", string(actionJSON)),
	}
	if len(attempted) > 0 {
		lines = append(lines, "attempted_strategies: "+strings.Join(attempted, "、"))
	} else {
		lines = append(lines, "attempted_strategies: none")
	}
	if len(candidateLines) > 0 {
		lines = append(lines, "candidate_summaries:")
		lines = append(lines, candidateLines...)
	} else {
		lines = append(lines, "candidate_summaries: none")
	}
	if len(recommendedActions) > 0 {
		lines = append(lines, "recommended_actions:")
		lines = append(lines, recommendedActions...)
	} else {
		lines = append(lines, "recommended_actions: none")
	}
	lines = append(lines, "instructions:")
	lines = append(lines, buildRetryInstructions(diagnostics)...)
	lines = append(lines, "[/WORD_AUTO_RELOCATE]")
	return strings.Join(lines, "\n")
}

func summarizeRetryCandidate(candidate ActionCandidate, index int) string {
	preview := normalizeRetryText(retryFirstNonEmpty(candidate.Preview, candidate.MatchedText))
	if preview == "" {
		preview = "未提供预览"
	}
	locator := make([]string, 0, 4)
	if trimmed := strings.TrimSpace(candidate.ParagraphID); trimmed != "" {
		locator = append(locator, "paragraph_id="+trimmed)
	}
	if trimmed := strings.TrimSpace(candidate.TableID); trimmed != "" {
		locator = append(locator, "table_id="+trimmed)
	}
	if candidate.RowIndex >= 0 {
		locator = append(locator, fmt.Sprintf("row_index=%d", candidate.RowIndex))
	}
	if candidate.CellIndex >= 0 {
		locator = append(locator, fmt.Sprintf("cell_index=%d", candidate.CellIndex))
	}
	parts := []string{fmt.Sprintf("%d. %s", index+1, preview)}
	if len(candidate.Path) > 0 {
		parts = append(parts, "路径="+strings.Join(candidate.Path, " / "))
	}
	if len(candidate.HeadingScope) > 0 {
		parts = append(parts, "作用域="+strings.Join(candidate.HeadingScope, " / "))
	}
	if len(locator) > 0 {
		parts = append(parts, "锚点="+strings.Join(locator, ", "))
	}
	return strings.Join(parts, " | ")
}

func buildRetryInstructions(diagnostics *ActionDiagnostics) []string {
	if diagnostics == nil {
		return []string{"1. 先重新读取当前文档上下文，再基于最新证据继续执行。"}
	}
	switch strings.ToUpper(strings.TrimSpace(diagnostics.ErrorCode)) {
	case "AMBIGUOUS_TARGET":
		return []string{
			"1. 先用 read 获取全文或结构上下文；如果还不够唯一，再结合 active_document_session_id 调 auramate-word-reader。",
			"2. 只使用后端给出的候选与 recommended_actions 继续定位，不要把候选列表转成交给用户点击的确认步骤。",
			"3. 基于最新证据重新发出新的 auramate-word-copilot Word 编辑动作；若连续重定位后仍无法唯一定位，再明确说明缺失的锚点信息。",
		}
	case "TABLE_STRUCTURE_CONFLICT":
		return []string{
			"1. 必须重新读取目标表格的最新结构，不要复用旧的 row_index、cell_index、table_id 假设。",
			"2. 先用 read 获取当前文档上下文；若仍不能唯一确认表格或单元格，再用 auramate-word-reader 或 IR/结构信息确认最新 table_id、行数、列数以及目标单元格位置。",
			"3. 若目标表格结构已变化，生成一条修正后的最小 Word 动作；若用户请求本身已不可能执行，要明确说明原因，但不要把这次失败误报成成功。",
		}
	case "TARGET_NOT_FOUND":
		return []string{
			"1. 把这次失败视为定位失效，重新读取当前文档内容，不要继续依赖旧 paragraph_id 或旧 target_text 假设。",
			"2. 若需要精确锚点，重新调用 auramate-word-reader 锁定最新 paragraph_id 或 table_id。",
			"3. 基于最新证据重发 Word 编辑动作；如果目标内容确实已不存在，再说明缺失了哪类锚点。",
		}
	case "EXECUTION_FAILED":
		return []string{
			"1. 先确认当前文档 revision、会话和结构是否已变化，再继续编辑。",
			"2. 如有必要，重新打开或重新读取当前文档上下文后再生成新的 Word 动作。",
			"3. 不要沿用已经失败的执行结果；只有新的动作真正成功后才能结束任务。",
		}
	case "VALIDATION_FAILED":
		return []string{
			"1. 当前写入和预期不一致，必须重新核对文档现状与目标效果。",
			"2. 结合最新文档内容重新决定是否需要重写、回滚后重写，或改用更精确的结构化动作。",
			"3. 不要复述之前的成功信号；以最新文档状态为准继续完成任务。",
		}
	default:
		return []string{
			"1. 先重新读取当前文档上下文，确认失败发生后的最新状态。",
			"2. 若缺少精确锚点或结构证据，调用 auramate-word-reader 或结构信息接口补齐。",
			"3. 基于最新证据重新生成单条最小 Word 编辑动作；若请求本身不可执行，再明确说明原因。",
		}
	}
}

func normalizeRetryText(value string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
}

func fallbackRetryField(value string, fallback string) string {
	trimmed := normalizeRetryText(value)
	if trimmed == "" {
		return fallback
	}
	return trimmed
}

func retryFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func max(left int, right int) int {
	if left > right {
		return left
	}
	return right
}

func min(left int, right int) int {
	if left < right {
		return left
	}
	return right
}

func diagnosticMessage(diagnostics *ActionDiagnostics, fallback string) string {
	if diagnostics != nil && strings.TrimSpace(diagnostics.Message) != "" {
		return diagnostics.Message
	}
	return fallback
}

func diagnosticCode(diagnostics *ActionDiagnostics) string {
	if diagnostics == nil {
		return ""
	}
	return diagnostics.ErrorCode
}

func (plan *ActionPlanResult) ResolvedTargetKind() string {
	if plan == nil || plan.ResolvedTarget == nil {
		return ""
	}
	return plan.ResolvedTarget.TargetKind
}

func describeSectionLayoutPatch(patch SectionLayout) string {
	labels := make([]string, 0)
	if patch.Orientation != "" {
		if patch.Orientation == "landscape" {
			labels = append(labels, "横向页面")
		} else {
			labels = append(labels, "纵向页面")
		}
	}
	if patch.PageWidth > 0 && patch.PageHeight > 0 {
		labels = append(labels, fmt.Sprintf("页面尺寸 %d x %d", patch.PageWidth, patch.PageHeight))
	}
	if patch.MarginLeft > 0 || patch.MarginRight > 0 {
		labels = append(labels, "左右页边距")
	}
	if patch.MarginTop > 0 || patch.MarginBottom > 0 {
		labels = append(labels, "上下页边距")
	}
	if patch.MarginHeader > 0 || patch.MarginFooter > 0 {
		labels = append(labels, "页眉页脚边距")
	}
	if len(labels) == 0 {
		return "页面版式"
	}
	return strings.Join(labels, "、")
}

func describeHeaderFooterInput(input *HeaderFooterInput) string {
	if input == nil {
		return "页眉页脚"
	}
	kind := "页眉"
	if strings.EqualFold(input.Kind, "footer") {
		kind = "页脚"
	}
	variant := strings.ToLower(strings.TrimSpace(input.Variant))
	switch variant {
	case "first":
		return "首页" + kind
	case "even":
		return "偶数页" + kind
	default:
		return "默认" + kind
	}
}

func hasAnySectionLayoutPatch(patch SectionLayout) bool {
	return patch.PageWidth > 0 || patch.PageHeight > 0 || patch.Orientation != "" || patch.MarginTop > 0 || patch.MarginRight > 0 || patch.MarginBottom > 0 || patch.MarginLeft > 0 || patch.MarginHeader > 0 || patch.MarginFooter > 0 || patch.MarginGutter > 0
}

func hasHeaderFooterInput(input *HeaderFooterInput) bool {
	return input != nil && strings.TrimSpace(input.Kind) != "" && len(input.Blocks) > 0
}

func beforeAfterLabel(position string) string {
	if normalizedPosition(position) == "before" {
		return "前"
	}
	return "后"
}

func mergeTemplateParagraphStyle(templateParagraph *Paragraph, patch ParagraphStyle) ParagraphStyle {
	base := ParagraphStyle{}
	if templateParagraph != nil {
		base = templateParagraph.Style
	}
	mergeParagraphStyle(&base, patch)
	return base
}

func mergeTemplateParagraphStyleWithConstraints(templateParagraph *Paragraph, patch ParagraphStyle, constraints *ActionConstraints) ParagraphStyle {
	preserveStyle := constraints == nil || constraints.preserveStyleEnabled()
	preserveNumbering := constraints == nil || constraints.preserveNumberingEnabled()
	base := ParagraphStyle{}
	if preserveStyle && templateParagraph != nil {
		base = templateParagraph.Style
	} else if preserveNumbering && templateParagraph != nil && templateParagraph.Style.Numbering != nil {
		numbering := *templateParagraph.Style.Numbering
		base.Numbering = &numbering
	}
	mergeParagraphStyle(&base, patch)
	if !preserveNumbering && patch.Numbering == nil {
		base.Numbering = nil
	}
	return base
}

func firstParagraphTextRunStyle(paragraph *Paragraph) TextStyle {
	if paragraph == nil {
		return TextStyle{}
	}
	for _, run := range paragraph.Runs {
		if strings.TrimSpace(run.Text) != "" {
			return run.Style
		}
	}
	if len(paragraph.Runs) > 0 {
		return paragraph.Runs[0].Style
	}
	return TextStyle{}
}

func firstParagraphTextRunStyleWithConstraints(paragraph *Paragraph, constraints *ActionConstraints) TextStyle {
	if constraints != nil && !constraints.preserveStyleEnabled() {
		return TextStyle{}
	}
	return firstParagraphTextRunStyle(paragraph)
}

func buildInsertRunID(text string) string {
	normalized := normalizeEditorText(text)
	if len(normalized) > 24 {
		normalized = normalized[:24]
	}
	if normalized == "" {
		normalized = "content"
	}
	return "insert-run-" + normalized
}

func resolveInsertTemplate(document *Document, anchorParagraph *Paragraph, action EditorAction) (*Paragraph, string) {
	role := "正文"
	if shouldUseHeadingTemplate(action) {
		role = "标题"
		if anchorParagraph != nil && extractHeadingLevel(anchorParagraph) > 0 {
			return anchorParagraph, role
		}
		for index := range document.Paragraphs {
			if extractHeadingLevel(&document.Paragraphs[index]) > 0 {
				return &document.Paragraphs[index], role
			}
		}
	}
	if anchorParagraph != nil && shouldPreferSameTableCellBodyTemplate(document, anchorParagraph, action) {
		if candidate := resolveSameTableCellBodyTemplate(document, anchorParagraph, action); candidate != nil {
			return candidate, role
		}
	}
	if anchorParagraph != nil && shouldPreferAdjacentBodyTemplate(anchorParagraph, action) {
		if candidate := resolveAdjacentBodyTemplate(document, anchorParagraph, action); candidate != nil {
			return candidate, role
		}
	}
	if anchorParagraph != nil && extractHeadingLevel(anchorParagraph) == 0 {
		return anchorParagraph, role
	}
	if anchorParagraph != nil {
		anchorIndex := -1
		for index := range document.Paragraphs {
			if document.Paragraphs[index].ID == anchorParagraph.ID {
				anchorIndex = index
				break
			}
		}
		if anchorIndex >= 0 {
			if normalizedPosition(action.Position) == "before" {
				for current := anchorIndex - 1; current >= 0; current-- {
					if extractHeadingLevel(&document.Paragraphs[current]) == 0 {
						return &document.Paragraphs[current], role
					}
				}
			} else {
				for current := anchorIndex + 1; current < len(document.Paragraphs); current++ {
					if extractHeadingLevel(&document.Paragraphs[current]) == 0 {
						return &document.Paragraphs[current], role
					}
				}
			}
		}
	}
	for index := range document.Paragraphs {
		if extractHeadingLevel(&document.Paragraphs[index]) == 0 {
			return &document.Paragraphs[index], role
		}
	}
	if len(document.Paragraphs) > 0 {
		return &document.Paragraphs[len(document.Paragraphs)-1], role
	}
	return nil, role
}

func shouldPreferSameTableCellBodyTemplate(document *Document, anchorParagraph *Paragraph, action EditorAction) bool {
	if document == nil || anchorParagraph == nil {
		return false
	}
	if _, _, _, ok := resolveParagraphTableCellContext(document, anchorParagraph.ID); !ok {
		return false
	}
	anchorStyleID := strings.ToLower(strings.TrimSpace(anchorParagraph.Style.StyleID))
	desiredStyleID := strings.ToLower(strings.TrimSpace(action.ParagraphStylePatch.StyleID))
	if desiredStyleID != "" && desiredStyleID != anchorStyleID {
		return true
	}
	return shouldPreferBodyInsertTemplate(action) && paragraphLooksLikeTableLabel(anchorParagraph)
}

func shouldPreferBodyInsertTemplate(action EditorAction) bool {
	styleID := strings.ToLower(strings.TrimSpace(action.ParagraphStylePatch.StyleID))
	if styleID == "tablebody" || styleID == "normal" || styleID == "bodytext" {
		return true
	}
	cueText := strings.ToLower(strings.TrimSpace(action.FormatBasis + " " + action.ReaderContextSummary + " " + action.NewText))
	return strings.Contains(cueText, "正文") || strings.Contains(cueText, "说明") || strings.Contains(cueText, "tablebody") || strings.Contains(cueText, " body") || strings.Contains(cueText, "body ")
}

func paragraphLooksLikeTableLabel(paragraph *Paragraph) bool {
	if paragraph == nil {
		return false
	}
	styleID := strings.ToLower(strings.TrimSpace(paragraph.Style.StyleID))
	if strings.Contains(styleID, "header") || strings.Contains(styleID, "title") || strings.Contains(styleID, "label") {
		return true
	}
	text := strings.TrimSpace(paragraph.PlainText())
	if text == "" || len([]rune(text)) > 12 || strings.ContainsAny(text, "，,。；;：:！？!?") {
		return false
	}
	style := firstParagraphTextRunStyle(paragraph)
	return style.BoldSet && style.Bold
}

func shouldPreferAdjacentBodyTemplate(anchorParagraph *Paragraph, action EditorAction) bool {
	if anchorParagraph == nil {
		return false
	}
	return shouldPreferBodyInsertTemplate(action) && paragraphLooksLikeTableLabel(anchorParagraph)
}

func resolveAdjacentBodyTemplate(document *Document, anchorParagraph *Paragraph, action EditorAction) *Paragraph {
	if document == nil || anchorParagraph == nil {
		return nil
	}
	anchorIndex := -1
	for index := range document.Paragraphs {
		if document.Paragraphs[index].ID == anchorParagraph.ID {
			anchorIndex = index
			break
		}
	}
	if anchorIndex < 0 {
		return nil
	}
	if normalizedPosition(action.Position) == "before" {
		for current := anchorIndex - 1; current >= 0; current-- {
			if paragraphMatchesBodyTemplate(&document.Paragraphs[current], action) {
				return &document.Paragraphs[current]
			}
		}
		for current := anchorIndex + 1; current < len(document.Paragraphs); current++ {
			if paragraphMatchesBodyTemplate(&document.Paragraphs[current], action) {
				return &document.Paragraphs[current]
			}
		}
		return nil
	}
	for current := anchorIndex + 1; current < len(document.Paragraphs); current++ {
		if paragraphMatchesBodyTemplate(&document.Paragraphs[current], action) {
			return &document.Paragraphs[current]
		}
	}
	for current := anchorIndex - 1; current >= 0; current-- {
		if paragraphMatchesBodyTemplate(&document.Paragraphs[current], action) {
			return &document.Paragraphs[current]
		}
	}
	return nil
}

func resolveSameTableCellBodyTemplate(document *Document, anchorParagraph *Paragraph, action EditorAction) *Paragraph {
	paragraphs := paragraphsInSameTableCell(document, anchorParagraph.ID)
	if len(paragraphs) == 0 {
		return nil
	}
	anchorIndex := -1
	for index, paragraph := range paragraphs {
		if paragraph != nil && paragraph.ID == anchorParagraph.ID {
			anchorIndex = index
			break
		}
	}
	if anchorIndex < 0 {
		return nil
	}
	if normalizedPosition(action.Position) == "before" {
		for current := anchorIndex - 1; current >= 0; current-- {
			if paragraphMatchesBodyTemplate(paragraphs[current], action) {
				return paragraphs[current]
			}
		}
		for current := anchorIndex + 1; current < len(paragraphs); current++ {
			if paragraphMatchesBodyTemplate(paragraphs[current], action) {
				return paragraphs[current]
			}
		}
		return nil
	}
	for current := anchorIndex + 1; current < len(paragraphs); current++ {
		if paragraphMatchesBodyTemplate(paragraphs[current], action) {
			return paragraphs[current]
		}
	}
	for current := anchorIndex - 1; current >= 0; current-- {
		if paragraphMatchesBodyTemplate(paragraphs[current], action) {
			return paragraphs[current]
		}
	}
	return nil
}

func paragraphMatchesBodyTemplate(paragraph *Paragraph, action EditorAction) bool {
	if paragraph == nil || strings.TrimSpace(paragraph.PlainText()) == "" {
		return false
	}
	styleID := strings.ToLower(strings.TrimSpace(paragraph.Style.StyleID))
	desiredStyleID := strings.ToLower(strings.TrimSpace(action.ParagraphStylePatch.StyleID))
	if desiredStyleID != "" {
		return styleID == desiredStyleID
	}
	if styleID == "tablebody" || styleID == "normal" || styleID == "bodytext" {
		return true
	}
	return !paragraphLooksLikeTableLabel(paragraph)
}

func paragraphsInSameTableCell(document *Document, paragraphID string) []*Paragraph {
	tableID, rowIndex, cellIndex, ok := resolveParagraphTableCellContext(document, paragraphID)
	if !ok {
		return nil
	}
	table, err := findTable(document, tableID)
	if err != nil || table == nil {
		return nil
	}
	if rowIndex < 0 || rowIndex >= len(table.Rows) {
		return nil
	}
	if cellIndex < 0 || cellIndex >= len(table.Rows[rowIndex].Cells) {
		return nil
	}
	paragraphs := make([]*Paragraph, 0)
	collectParagraphPointers(table.Rows[rowIndex].Cells[cellIndex].Blocks, &paragraphs)
	return paragraphs
}

func collectParagraphPointers(blocks []Block, paragraphs *[]*Paragraph) {
	for index := range blocks {
		block := &blocks[index]
		switch block.Type {
		case BlockTypeParagraph:
			if block.Paragraph != nil {
				*paragraphs = append(*paragraphs, block.Paragraph)
			}
		case BlockTypeTable:
			if block.Table == nil {
				continue
			}
			for rowIndex := range block.Table.Rows {
				for cellIndex := range block.Table.Rows[rowIndex].Cells {
					collectParagraphPointers(block.Table.Rows[rowIndex].Cells[cellIndex].Blocks, paragraphs)
				}
			}
		}
	}
}

func resolveParagraphTableCellContext(document *Document, paragraphID string) (string, int, int, bool) {
	if document == nil || strings.TrimSpace(paragraphID) == "" {
		return "", 0, 0, false
	}
	path := buildParagraphPath(document, paragraphID)
	for index := 0; index < len(path)-2; index++ {
		if !strings.HasPrefix(path[index+1], "row_") || !strings.HasPrefix(path[index+2], "cell_") {
			continue
		}
		rowIndex, rowErr := strconv.Atoi(strings.TrimPrefix(path[index+1], "row_"))
		cellIndex, cellErr := strconv.Atoi(strings.TrimPrefix(path[index+2], "cell_"))
		if rowErr != nil || cellErr != nil {
			continue
		}
		return path[index], rowIndex, cellIndex, true
	}
	return "", 0, 0, false
}

func shouldUseHeadingTemplate(action EditorAction) bool {
	styleID := strings.ToLower(strings.TrimSpace(action.ParagraphStylePatch.StyleID))
	if strings.HasPrefix(styleID, "heading") || styleID == "title" {
		return true
	}
	cueText := strings.ToLower(strings.TrimSpace(action.FormatBasis + " " + action.ReaderContextSummary + " " + action.NewText))
	return strings.Contains(cueText, "标题") || strings.Contains(cueText, "heading")
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
