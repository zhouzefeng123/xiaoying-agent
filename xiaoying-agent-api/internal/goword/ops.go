package goword

import (
	"fmt"
	"strings"
	"sync/atomic"
)

type OperationType string

const (
	OperationReplaceText          OperationType = "replace_text"
	OperationInsertParagraph      OperationType = "insert_paragraph"
	OperationInsertParagraphBreak OperationType = "insert_paragraph_break"
	OperationInsertLineBreak      OperationType = "insert_line_break"
	OperationInsertTable          OperationType = "insert_table"
	OperationInsertTableRow       OperationType = "insert_table_row"
	OperationDeleteTableRow       OperationType = "delete_table_row"
	OperationInsertTableColumn    OperationType = "insert_table_column"
	OperationDeleteTableColumn    OperationType = "delete_table_column"
	OperationUpdateTableCell      OperationType = "update_table_cell"
	OperationUpdateTextStyle      OperationType = "update_text_style"
	OperationUpdateParagraphStyle OperationType = "update_paragraph_style"
	OperationUpdateHeaderFooter   OperationType = "update_header_footer"
	OperationUpdateSectionLayout  OperationType = "update_section_layout"
	OperationDeleteParagraph      OperationType = "delete_paragraph"
	OperationScrollTo             OperationType = "scroll_to"
)

type Operation struct {
	Type                OperationType      `json:"type"`
	ParagraphID         string             `json:"paragraph_id,omitempty"`
	AnchorID            string             `json:"anchor_id,omitempty"`
	TableID             string             `json:"table_id,omitempty"`
	Position            string             `json:"position,omitempty"`
	RowIndex            int                `json:"row_index,omitempty"`
	CellIndex           int                `json:"cell_index,omitempty"`
	StartOffset         int                `json:"start_offset,omitempty"`
	EndOffset           int                `json:"end_offset,omitempty"`
	BeforeText          string             `json:"before_text,omitempty"`
	Text                string             `json:"text,omitempty"`
	StylePatch          TextStyle          `json:"style_patch,omitempty"`
	ParagraphStylePatch ParagraphStyle     `json:"paragraph_style_patch,omitempty"`
	Paragraph           *ParagraphInput    `json:"paragraph,omitempty"`
	Table               *TableInput        `json:"table,omitempty"`
	TableRow            *TableRowInput     `json:"table_row,omitempty"`
	TableColumn         *TableColumnInput  `json:"table_column,omitempty"`
	TableCell           *TableCellInput    `json:"table_cell,omitempty"`
	HeaderFooter        *HeaderFooterInput `json:"header_footer,omitempty"`
	SectionLayoutPatch  SectionLayout      `json:"section_layout_patch,omitempty"`
	TrackChanges        bool               `json:"track_changes,omitempty"`
}

type ParagraphInput struct {
	Text  string         `json:"text"`
	Runs  []Run          `json:"runs,omitempty"`
	Style ParagraphStyle `json:"style,omitempty"`
}

type TableInput struct {
	PropertiesXML string          `json:"properties_xml,omitempty"`
	GridXML       string          `json:"grid_xml,omitempty"`
	Rows          []TableRowInput `json:"rows"`
}

type TableRowInput struct {
	PropertiesXML   string           `json:"properties_xml,omitempty"`
	RepeatHeader    bool             `json:"repeat_header,omitempty"`
	RepeatHeaderSet bool             `json:"repeat_header_set,omitempty"`
	CantSplit       bool             `json:"cant_split,omitempty"`
	CantSplitSet    bool             `json:"cant_split_set,omitempty"`
	Cells           []TableCellInput `json:"cells"`
}

type TableColumnInput struct {
	Cells []TableCellInput `json:"cells"`
}

type TableCellInput struct {
	PropertiesXML string           `json:"properties_xml,omitempty"`
	Paragraphs    []ParagraphInput `json:"paragraphs,omitempty"`
}

type BlockInput struct {
	Type      BlockType       `json:"type"`
	Paragraph *ParagraphInput `json:"paragraph,omitempty"`
	Table     *TableInput     `json:"table,omitempty"`
}

type HeaderFooterInput struct {
	Kind    string       `json:"kind"`
	Variant string       `json:"variant,omitempty"`
	Blocks  []BlockInput `json:"blocks,omitempty"`
}

type SectionLayout struct {
	BreakType    string `json:"break_type,omitempty"`
	PageWidth    int    `json:"page_width,omitempty"`
	PageHeight   int    `json:"page_height,omitempty"`
	Orientation  string `json:"orientation,omitempty"`
	TitlePage    bool   `json:"title_page,omitempty"`
	TitlePageSet bool   `json:"title_page_set,omitempty"`
	PageNumStart int    `json:"page_number_start,omitempty"`
	PageNumFmt   string `json:"page_number_format,omitempty"`
	MarginTop    int    `json:"margin_top,omitempty"`
	MarginRight  int    `json:"margin_right,omitempty"`
	MarginBottom int    `json:"margin_bottom,omitempty"`
	MarginLeft   int    `json:"margin_left,omitempty"`
	MarginHeader int    `json:"margin_header,omitempty"`
	MarginFooter int    `json:"margin_footer,omitempty"`
	MarginGutter int    `json:"margin_gutter,omitempty"`
}

type ApplyResult struct {
	Document *Document       `json:"document"`
	Diffs    []ParagraphDiff `json:"diffs"`
}

var idCounter uint64

func nextID(prefix string) string {
	value := atomic.AddUint64(&idCounter, 1)
	return fmt.Sprintf("%s_%d", prefix, value)
}

func ApplyOperations(document *Document, operations []Operation) error {
	_, err := ApplyOperationsWithHints(document, operations)
	return err
}

func ApplyOperationsWithHints(document *Document, operations []Operation) ([]ParagraphChangeHint, error) {
	if document == nil {
		return nil, fmt.Errorf("document is nil")
	}
	document.ensureBlockStructure()
	hints := make([]ParagraphChangeHint, 0)
	changed := false

	for _, operation := range operations {
		switch operation.Type {
		case OperationReplaceText:
			changed = true
			hint, err := applyReplaceText(document, operation)
			if err != nil {
				return nil, err
			}
			if hint != nil {
				hints = append(hints, *hint)
			}
		case OperationInsertParagraph:
			changed = true
			if err := applyInsertParagraph(document, operation); err != nil {
				return nil, err
			}
		case OperationInsertParagraphBreak:
			changed = true
			hint, err := applyInsertParagraphBreak(document, operation)
			if err != nil {
				return nil, err
			}
			if hint != nil {
				hints = append(hints, *hint)
			}
		case OperationInsertLineBreak:
			changed = true
			hint, err := applyInsertLineBreak(document, operation)
			if err != nil {
				return nil, err
			}
			if hint != nil {
				hints = append(hints, *hint)
			}
		case OperationInsertTable:
			changed = true
			if err := applyInsertTable(document, operation); err != nil {
				return nil, err
			}
		case OperationInsertTableRow:
			changed = true
			if err := applyInsertTableRow(document, operation); err != nil {
				return nil, err
			}
		case OperationDeleteTableRow:
			changed = true
			if err := applyDeleteTableRow(document, operation); err != nil {
				return nil, err
			}
		case OperationInsertTableColumn:
			changed = true
			if err := applyInsertTableColumn(document, operation); err != nil {
				return nil, err
			}
		case OperationDeleteTableColumn:
			changed = true
			if err := applyDeleteTableColumn(document, operation); err != nil {
				return nil, err
			}
		case OperationUpdateTableCell:
			changed = true
			if err := applyUpdateTableCell(document, operation); err != nil {
				return nil, err
			}
		case OperationUpdateTextStyle:
			changed = true
			hint, err := applyUpdateTextStyle(document, operation)
			if err != nil {
				return nil, err
			}
			if hint != nil {
				hints = append(hints, *hint)
			}
		case OperationUpdateParagraphStyle:
			changed = true
			hint, err := applyUpdateParagraphStyle(document, operation)
			if err != nil {
				return nil, err
			}
			if hint != nil {
				hints = append(hints, *hint)
			}
		case OperationUpdateHeaderFooter:
			changed = true
			if err := applyUpdateHeaderFooter(document, operation); err != nil {
				return nil, err
			}
		case OperationUpdateSectionLayout:
			changed = true
			if err := applyUpdateSectionLayout(document, operation); err != nil {
				return nil, err
			}
		case OperationDeleteParagraph:
			changed = true
			if err := applyDeleteParagraph(document, operation); err != nil {
				return nil, err
			}
		case OperationScrollTo:
			continue
		default:
			return nil, fmt.Errorf("unsupported operation type: %s", operation.Type)
		}
	}

	if !changed {
		return hints, nil
	}

	document.rebuildParagraphs()
	document.Revision++
	return hints, nil
}

func validateOperationRangeBeforeText(content string, operation Operation, startOffset, endOffset int, operationLabel string) error {
	if operation.BeforeText == "" {
		return nil
	}

	runes := []rune(content)
	if startOffset < 0 || endOffset < startOffset || endOffset > len(runes) {
		return fmt.Errorf("invalid %s offsets for paragraph %s", operationLabel, operation.ParagraphID)
	}

	actual := string(runes[startOffset:endOffset])
	if actual != operation.BeforeText {
		return fmt.Errorf("stale %s target for paragraph %s: expected %q, got %q", operationLabel, operation.ParagraphID, operation.BeforeText, actual)
	}
	return nil
}

func requireOperationRangeBeforeText(operation Operation, operationLabel string) error {
	if operation.BeforeText == "" {
		return fmt.Errorf("before_text is required for %s on paragraph %s", operationLabel, operation.ParagraphID)
	}
	return nil
}

func applyReplaceText(document *Document, operation Operation) (*ParagraphChangeHint, error) {
	paragraph, _, err := findParagraph(document, operation.ParagraphID)
	if err != nil {
		return nil, err
	}

	content := paragraph.PlainText()
	if operation.StartOffset < 0 || operation.EndOffset < operation.StartOffset || operation.EndOffset > len([]rune(content)) {
		return nil, fmt.Errorf("invalid replace offsets for paragraph %s", operation.ParagraphID)
	}
	if operation.EndOffset > operation.StartOffset {
		if err := requireOperationRangeBeforeText(operation, "replace_text"); err != nil {
			return nil, err
		}
	}
	if err := validateOperationRangeBeforeText(content, operation, operation.StartOffset, operation.EndOffset, "replace_text"); err != nil {
		return nil, err
	}
	runes := []rune(content)
	beforeText := string(runes[operation.StartOffset:operation.EndOffset])
	paragraph.Runs = replaceRunsText(paragraph.Runs, operation.StartOffset, operation.EndOffset, operation.Text)
	return &ParagraphChangeHint{
		ParagraphID: operation.ParagraphID,
		CharacterRanges: []CharacterRangeDiff{{
			Type:        resolveRangeType(beforeText, operation.Text),
			BeforeStart: operation.StartOffset,
			BeforeEnd:   operation.EndOffset,
			AfterStart:  operation.StartOffset,
			AfterEnd:    operation.StartOffset + len([]rune(operation.Text)),
			BeforeText:  beforeText,
			AfterText:   operation.Text,
		}},
	}, nil
}

func applyUpdateTextStyle(document *Document, operation Operation) (*ParagraphChangeHint, error) {
	paragraph, _, err := findParagraph(document, operation.ParagraphID)
	if err != nil {
		return nil, err
	}
	if !hasTextStylePatch(operation.StylePatch) {
		return nil, fmt.Errorf("style_patch is required for update_text_style")
	}

	content := paragraph.PlainText()
	if operation.StartOffset < 0 || operation.EndOffset < operation.StartOffset || operation.EndOffset > len([]rune(content)) {
		return nil, fmt.Errorf("invalid style offsets for paragraph %s", operation.ParagraphID)
	}
	if operation.StartOffset == operation.EndOffset {
		return nil, fmt.Errorf("update_text_style requires a non-empty range")
	}
	if err := requireOperationRangeBeforeText(operation, "update_text_style"); err != nil {
		return nil, err
	}
	if err := validateOperationRangeBeforeText(content, operation, operation.StartOffset, operation.EndOffset, "update_text_style"); err != nil {
		return nil, err
	}

	runes := []rune(content)
	beforeText := string(runes[operation.StartOffset:operation.EndOffset])
	paragraph.Runs = patchRunsStyle(paragraph.Runs, operation.StartOffset, operation.EndOffset, operation.StylePatch)
	return &ParagraphChangeHint{
		ParagraphID: operation.ParagraphID,
		CharacterRanges: []CharacterRangeDiff{{
			Type:        DiffUpdated,
			BeforeStart: operation.StartOffset,
			BeforeEnd:   operation.EndOffset,
			AfterStart:  operation.StartOffset,
			AfterEnd:    operation.EndOffset,
			BeforeText:  beforeText,
			AfterText:   beforeText,
		}},
	}, nil
}

func applyUpdateParagraphStyle(document *Document, operation Operation) (*ParagraphChangeHint, error) {
	paragraph, _, err := findParagraph(document, operation.ParagraphID)
	if err != nil {
		return nil, err
	}
	if !hasParagraphStylePatch(operation.ParagraphStylePatch) {
		return nil, fmt.Errorf("paragraph_style_patch is required for update_paragraph_style")
	}
	mergeParagraphStyle(&paragraph.Style, operation.ParagraphStylePatch)
	return &ParagraphChangeHint{ParagraphID: operation.ParagraphID}, nil
}

func replaceRunsText(runs []Run, startOffset, endOffset int, replacement string) []Run {
	if len(runs) == 0 {
		return buildRunsFromText(replacement, TextStyle{})
	}
	replacementStyle := firstTextRunStyleInVisibleRange(runs, startOffset, endOffset)
	out := make([]Run, 0, len(runs)+1)
	currentOffset := 0
	inserted := false

	for _, run := range runs {
		visibleRunes := []rune(runVisibleText(run))
		runStart := currentOffset
		runEnd := currentOffset + len(visibleRunes)
		currentOffset = runEnd

		if !isTextRun(run) {
			out = append(out, cloneRun(run))
			continue
		}

		runes := []rune(run.Text)

		switch {
		case endOffset <= runStart || startOffset >= runEnd:
			out = append(out, cloneRun(run))
		default:
			if startOffset > runStart {
				leftText := string(runes[:startOffset-runStart])
				if leftText != "" {
					out = append(out, cloneRunWithText(run, leftText))
				}
			}
			if !inserted {
				if replacement != "" || startOffset != endOffset {
					out = append(out, buildRunsFromText(replacement, replacementStyle)...)
				}
				inserted = true
			}
			if endOffset < runEnd {
				rightText := string(runes[endOffset-runStart:])
				if rightText != "" {
					out = append(out, cloneRunWithText(run, rightText))
				}
			}
		}

	}

	if !inserted && replacement != "" {
		out = append(out, buildRunsFromText(replacement, replacementStyle)...)
	}
	if len(out) == 0 {
		out = buildRunsFromText("", replacementStyle)
	}
	return out
}

func patchRunsStyle(runs []Run, startOffset, endOffset int, patch TextStyle) []Run {
	if len(runs) == 0 {
		return runs
	}

	out := make([]Run, 0, len(runs)+2)
	currentOffset := 0

	for _, run := range runs {
		visibleRunes := []rune(runVisibleText(run))
		runStart := currentOffset
		runEnd := currentOffset + len(visibleRunes)
		currentOffset = runEnd

		if !isTextRun(run) {
			out = append(out, cloneRun(run))
			continue
		}

		runes := []rune(run.Text)

		if endOffset <= runStart || startOffset >= runEnd {
			out = append(out, cloneRun(run))
			continue
		}

		middleStart := 0
		if startOffset > runStart {
			leftText := string(runes[:startOffset-runStart])
			if leftText != "" {
				out = append(out, cloneRunWithText(run, leftText))
			}
			middleStart = startOffset - runStart
		}

		middleEnd := len(runes)
		if endOffset < runEnd {
			middleEnd = endOffset - runStart
		}
		middleText := string(runes[middleStart:middleEnd])
		if middleText != "" {
			patchedRun := cloneRunWithText(run, middleText)
			if patch.FontFamily != "" {
				patchedRun.Style.ThemeFontASCII = ""
				patchedRun.Style.ThemeFontEastAsia = ""
			}
			if patch.Color != "" {
				patchedRun.Style.ThemeColor = ""
			}
			mergeTextStyle(&patchedRun.Style, patch)
			out = append(out, patchedRun)
		}

		if endOffset < runEnd {
			rightText := string(runes[middleEnd:])
			if rightText != "" {
				out = append(out, cloneRunWithText(run, rightText))
			}
		}
	}

	if len(out) == 0 {
		return runs
	}
	return out
}

func isTextRun(run Run) bool {
	return strings.TrimSpace(run.DrawingXML) == "" && run.Image == nil && run.BreakType == ""
}

func firstTextRunStyle(runs []Run) TextStyle {
	for _, run := range runs {
		if isTextRun(run) {
			return run.Style
		}
	}
	if len(runs) > 0 {
		return runs[0].Style
	}
	return TextStyle{}
}

func firstTextRunStyleInVisibleRange(runs []Run, startOffset, endOffset int) TextStyle {
	if len(runs) == 0 {
		return TextStyle{}
	}
	currentOffset := 0
	for _, run := range runs {
		visibleRunes := []rune(runVisibleText(run))
		runStart := currentOffset
		runEnd := currentOffset + len(visibleRunes)
		currentOffset = runEnd

		if !isTextRun(run) {
			continue
		}
		if startOffset == endOffset {
			if startOffset >= runStart && startOffset <= runEnd {
				return run.Style
			}
			continue
		}
		if startOffset < runEnd && endOffset > runStart {
			return run.Style
		}
	}
	return firstTextRunStyle(runs)
}

func buildRunsFromText(text string, style TextStyle) []Run {
	normalized := normalizeTextLineEndings(text)
	parts := strings.Split(normalized, "\n")
	runs := make([]Run, 0, len(parts)*2)
	for index, part := range parts {
		if part != "" || len(parts) == 1 {
			runs = append(runs, Run{ID: nextID("run"), Text: part, Style: style})
		}
		if index < len(parts)-1 {
			runs = append(runs, Run{ID: nextID("run"), BreakType: RunBreakTypeLine, Style: style})
		}
	}
	if len(runs) == 0 {
		return []Run{{ID: nextID("run"), Text: "", Style: style}}
	}
	return runs
}

func normalizeTextLineEndings(text string) string {
	normalized := strings.ReplaceAll(text, "\r\n", "\n")
	return strings.ReplaceAll(normalized, "\r", "\n")
}

func hasTextStylePatch(style TextStyle) bool {
	return style.StyleID != "" ||
		style.FontFamily != "" ||
		style.ThemeFontASCII != "" ||
		style.ThemeFontEastAsia != "" ||
		style.FontSizeHP > 0 ||
		style.BoldSet || style.Bold ||
		style.ItalicSet || style.Italic ||
		style.UnderlineSet || style.Underline ||
		style.Color != "" ||
		style.ThemeColor != ""
}

func hasParagraphStylePatch(style ParagraphStyle) bool {
	return style.StyleID != "" ||
		style.Alignment != "" ||
		style.SpacingBefore != 0 ||
		style.SpacingAfter != 0 ||
		style.LineSpacing != 0 ||
		style.LineSpacingRule != "" ||
		style.FirstLineIndent != 0 ||
		style.LeftIndent != 0 ||
		style.KeepNextSet || style.KeepNext ||
		style.KeepLinesSet || style.KeepLines ||
		style.PageBreakBeforeSet || style.PageBreakBefore ||
		style.WidowControlSet || style.WidowControl ||
		style.Numbering != nil
}

func cloneRunWithText(run Run, text string) Run {
	cloned := cloneRun(run)
	cloned.ID = nextID("run")
	cloned.Text = text
	cloned.DrawingXML = ""
	cloned.Image = nil
	return cloned
}

func applyInsertParagraph(document *Document, operation Operation) error {
	if operation.Paragraph == nil {
		return fmt.Errorf("paragraph payload is required")
	}

	paragraph := buildParagraphFromInput(*operation.Paragraph)

	if operation.AnchorID == "" {
		document.Blocks = append(document.Blocks, Block{Type: BlockTypeParagraph, Paragraph: &paragraph})
		return nil
	}

	location, err := findParagraphLocation(document, operation.AnchorID)
	if err != nil {
		return err
	}

	insertIndex := location.blockIndex
	if strings.EqualFold(operation.Position, "after") || operation.Position == "" {
		insertIndex = location.blockIndex + 1
	}

	blocks := *location.blocks
	blocks = append(blocks, Block{})
	copy(blocks[insertIndex+1:], blocks[insertIndex:])
	blocks[insertIndex] = Block{Type: BlockTypeParagraph, Paragraph: &paragraph}
	*location.blocks = blocks
	return nil
}

func applyInsertParagraphBreak(document *Document, operation Operation) (*ParagraphChangeHint, error) {
	location, err := findParagraphLocation(document, operation.ParagraphID)
	if err != nil {
		return nil, err
	}

	content := location.paragraph.PlainText()
	contentLength := len([]rune(content))
	anchorStart := operation.StartOffset
	anchorEnd := operation.EndOffset
	breakOffset := operation.StartOffset
	if anchorEnd > anchorStart {
		if err := requireOperationRangeBeforeText(operation, "insert_paragraph_break"); err != nil {
			return nil, err
		}
		if anchorEnd < anchorStart {
			return nil, fmt.Errorf("invalid paragraph break anchor offsets for paragraph %s", operation.ParagraphID)
		}
		if err := validateOperationRangeBeforeText(content, operation, anchorStart, anchorEnd, "insert_paragraph_break"); err != nil {
			return nil, err
		}
		if strings.EqualFold(operation.Position, "after") {
			breakOffset = anchorEnd
		} else {
			breakOffset = anchorStart
		}
	}
	if breakOffset < 0 || breakOffset > contentLength {
		return nil, fmt.Errorf("invalid paragraph break offset for paragraph %s", operation.ParagraphID)
	}

	original := location.paragraph.clone()
	leftRuns, rightRuns := splitRunsAtVisibleOffset(original.Runs, breakOffset)
	textStyle := firstTextRunStyle(original.Runs)
	location.paragraph.Runs = ensureParagraphRuns(leftRuns, textStyle)
	location.paragraph.SectionBreakXML = ""
	location.paragraph.SectionBreakLayout = nil

	newParagraph := original.clone()
	newParagraph.ID = nextID("para")
	newParagraph.Runs = ensureParagraphRuns(rightRuns, textStyle)

	blocks := *location.blocks
	insertIndex := location.blockIndex + 1
	blocks = append(blocks, Block{})
	copy(blocks[insertIndex+1:], blocks[insertIndex:])
	blocks[insertIndex] = Block{Type: BlockTypeParagraph, Paragraph: &newParagraph}
	*location.blocks = blocks

	return &ParagraphChangeHint{
		ParagraphID: location.paragraph.ID,
		CharacterRanges: []CharacterRangeDiff{{
			Type:        DiffInserted,
			BeforeStart: breakOffset,
			BeforeEnd:   breakOffset,
			AfterStart:  breakOffset,
			AfterEnd:    breakOffset + 1,
			AfterText:   "\n",
		}},
	}, nil
}

func applyInsertLineBreak(document *Document, operation Operation) (*ParagraphChangeHint, error) {
	paragraph, _, err := findParagraph(document, operation.ParagraphID)
	if err != nil {
		return nil, err
	}

	content := paragraph.PlainText()
	contentLength := len([]rune(content))
	anchorStart := operation.StartOffset
	anchorEnd := operation.EndOffset
	breakOffset := operation.StartOffset
	if anchorEnd > anchorStart {
		if err := requireOperationRangeBeforeText(operation, "insert_line_break"); err != nil {
			return nil, err
		}
		if anchorEnd < anchorStart {
			return nil, fmt.Errorf("invalid line break anchor offsets for paragraph %s", operation.ParagraphID)
		}
		if err := validateOperationRangeBeforeText(content, operation, anchorStart, anchorEnd, "insert_line_break"); err != nil {
			return nil, err
		}
		if strings.EqualFold(operation.Position, "after") {
			breakOffset = anchorEnd
		} else {
			breakOffset = anchorStart
		}
	}
	if breakOffset < 0 || breakOffset > contentLength {
		return nil, fmt.Errorf("invalid line break offset for paragraph %s", operation.ParagraphID)
	}

	paragraph.Runs = insertRunBreak(paragraph.Runs, breakOffset, RunBreakTypeLine)
	return &ParagraphChangeHint{
		ParagraphID: paragraph.ID,
		CharacterRanges: []CharacterRangeDiff{{
			Type:        DiffInserted,
			BeforeStart: breakOffset,
			BeforeEnd:   breakOffset,
			AfterStart:  breakOffset,
			AfterEnd:    breakOffset + 1,
			AfterText:   "\n",
		}},
	}, nil
}

func insertRunBreak(runs []Run, offset int, breakType RunBreakType) []Run {
	leftRuns, rightRuns := splitRunsAtVisibleOffset(runs, offset)
	breakRun := Run{
		ID:        nextID("run"),
		BreakType: breakType,
		Style:     firstTextRunStyle(runs),
	}
	result := append(leftRuns, breakRun)
	result = append(result, rightRuns...)
	return result
}

func splitRunsAtVisibleOffset(runs []Run, offset int) ([]Run, []Run) {
	leftRuns := make([]Run, 0, len(runs))
	rightRuns := make([]Run, 0, len(runs))
	currentOffset := 0
	splitDone := false

	for _, run := range runs {
		visibleRunes := []rune(runVisibleText(run))
		runStart := currentOffset
		runEnd := currentOffset + len(visibleRunes)
		currentOffset = runEnd

		if splitDone {
			rightRuns = append(rightRuns, cloneRun(run))
			continue
		}

		if offset <= runStart {
			rightRuns = append(rightRuns, cloneRun(run))
			splitDone = true
			continue
		}

		if offset >= runEnd {
			leftRuns = append(leftRuns, cloneRun(run))
			continue
		}

		if !isTextRun(run) {
			rightRuns = append(rightRuns, cloneRun(run))
			splitDone = true
			continue
		}

		runes := []rune(run.Text)
		splitIndex := offset - runStart
		if splitIndex > 0 {
			leftText := string(runes[:splitIndex])
			if leftText != "" {
				leftRuns = append(leftRuns, cloneRunWithText(run, leftText))
			}
		}
		if splitIndex < len(runes) {
			rightText := string(runes[splitIndex:])
			if rightText != "" {
				rightRuns = append(rightRuns, cloneRunWithText(run, rightText))
			}
		}
		splitDone = true
	}

	return leftRuns, rightRuns
}

func ensureParagraphRuns(runs []Run, style TextStyle) []Run {
	if len(runs) > 0 {
		return runs
	}
	return []Run{{ID: nextID("run"), Text: "", Style: style}}
}

func applyInsertTable(document *Document, operation Operation) error {
	if operation.Table == nil {
		return fmt.Errorf("table payload is required")
	}
	if len(operation.Table.Rows) == 0 {
		return fmt.Errorf("table rows are required")
	}

	table := buildTableFromInput(*operation.Table)
	block := Block{Type: BlockTypeTable, Table: &table}

	if operation.AnchorID == "" {
		document.Blocks = append(document.Blocks, block)
		return nil
	}

	location, err := findParagraphLocation(document, operation.AnchorID)
	if err != nil {
		return err
	}

	insertIndex := location.blockIndex
	if strings.EqualFold(operation.Position, "after") || operation.Position == "" {
		insertIndex = location.blockIndex + 1
	}

	blocks := *location.blocks
	blocks = append(blocks, Block{})
	copy(blocks[insertIndex+1:], blocks[insertIndex:])
	blocks[insertIndex] = block
	*location.blocks = blocks
	return nil
}

func buildParagraphFromInput(input ParagraphInput) Paragraph {
	paragraph := Paragraph{
		ID:    nextID("para"),
		Style: cloneParagraphStyle(input.Style),
	}
	if len(input.Runs) > 0 {
		paragraph.Runs = make([]Run, 0, len(input.Runs))
		for _, run := range input.Runs {
			cloned := cloneRun(run)
			if strings.TrimSpace(cloned.ID) == "" {
				cloned.ID = nextID("run")
			}
			paragraph.Runs = append(paragraph.Runs, cloned)
		}
	} else {
		paragraph.Runs = []Run{{ID: nextID("run"), Text: input.Text}}
	}
	if len(paragraph.Runs) == 0 {
		paragraph.Runs = []Run{{ID: nextID("run"), Text: input.Text}}
	}
	return paragraph
}

func buildTableFromInput(input TableInput) Table {
	table := Table{
		ID:            nextID("tbl"),
		PropertiesXML: strings.TrimSpace(input.PropertiesXML),
		GridXML:       strings.TrimSpace(input.GridXML),
		Rows:          make([]TableRow, 0, len(input.Rows)),
	}
	maxColumns := 0
	for _, rowInput := range input.Rows {
		if len(rowInput.Cells) > maxColumns {
			maxColumns = len(rowInput.Cells)
		}
		row := TableRow{
			ID:            nextID("row"),
			PropertiesXML: strings.TrimSpace(rowInput.PropertiesXML),
			Cells:         make([]TableCell, 0, len(rowInput.Cells)),
		}
		for _, cellInput := range rowInput.Cells {
			cell := TableCell{
				ID:            nextID("cell"),
				PropertiesXML: strings.TrimSpace(cellInput.PropertiesXML),
			}
			if len(cellInput.Paragraphs) == 0 {
				paragraph := buildParagraphFromInput(ParagraphInput{})
				cell.Blocks = []Block{{Type: BlockTypeParagraph, Paragraph: &paragraph}}
			} else {
				cell.Blocks = make([]Block, 0, len(cellInput.Paragraphs))
				for _, paragraphInput := range cellInput.Paragraphs {
					paragraph := buildParagraphFromInput(paragraphInput)
					cell.Blocks = append(cell.Blocks, Block{Type: BlockTypeParagraph, Paragraph: &paragraph})
				}
			}
			row.Cells = append(row.Cells, cell)
		}
		table.Rows = append(table.Rows, row)
	}
	if table.PropertiesXML == "" {
		table.PropertiesXML = `<w:tblStyle w:val="TableGrid"/><w:tblW w:type="auto" w:w="0"/>`
	}
	if table.GridXML == "" && maxColumns > 0 {
		var builder strings.Builder
		for column := 0; column < maxColumns; column++ {
			builder.WriteString(`<w:gridCol w:w="2400"/>`)
		}
		table.GridXML = builder.String()
	}
	return table
}

func applyDeleteParagraph(document *Document, operation Operation) error {
	location, err := findParagraphLocation(document, operation.ParagraphID)
	if err != nil {
		return err
	}

	blocks := *location.blocks
	*location.blocks = append(blocks[:location.blockIndex], blocks[location.blockIndex+1:]...)
	return nil
}

func findParagraph(document *Document, paragraphID string) (*Paragraph, int, error) {
	location, err := findParagraphLocation(document, paragraphID)
	if err != nil {
		return nil, -1, err
	}
	return location.paragraph, location.blockIndex, nil
}

type paragraphLocation struct {
	paragraph  *Paragraph
	blocks     *[]Block
	blockIndex int
}

func findParagraphLocation(document *Document, paragraphID string) (*paragraphLocation, error) {
	if document == nil {
		return nil, fmt.Errorf("document is nil")
	}
	document.ensureBlockStructure()
	location := findParagraphInBlocks(&document.Blocks, paragraphID)
	if location == nil {
		return nil, fmt.Errorf("paragraph not found: %s", paragraphID)
	}
	return location, nil
}

func findParagraphInBlocks(blocks *[]Block, paragraphID string) *paragraphLocation {
	if blocks == nil {
		return nil
	}
	for index := range *blocks {
		block := &(*blocks)[index]
		switch block.Type {
		case BlockTypeParagraph:
			if block.Paragraph != nil && block.Paragraph.ID == paragraphID {
				return &paragraphLocation{paragraph: block.Paragraph, blocks: blocks, blockIndex: index}
			}
		case BlockTypeTable:
			if block.Table == nil {
				continue
			}
			for rowIndex := range block.Table.Rows {
				for cellIndex := range block.Table.Rows[rowIndex].Cells {
					location := findParagraphInBlocks(&block.Table.Rows[rowIndex].Cells[cellIndex].Blocks, paragraphID)
					if location != nil {
						return location
					}
				}
			}
		}
	}
	return nil
}
