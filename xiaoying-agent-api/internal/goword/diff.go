package goword

import "strings"

type DiffType string

const (
	DiffUnchanged DiffType = "unchanged"
	DiffInserted  DiffType = "inserted"
	DiffDeleted   DiffType = "deleted"
	DiffUpdated   DiffType = "updated"
)

type RunSnapshotKind string

const (
	RunSnapshotText  RunSnapshotKind = "text"
	RunSnapshotImage RunSnapshotKind = "image"
)

type CharacterRangeDiff struct {
	Type        DiffType `json:"type"`
	BeforeStart int      `json:"before_start,omitempty"`
	BeforeEnd   int      `json:"before_end,omitempty"`
	AfterStart  int      `json:"after_start,omitempty"`
	AfterEnd    int      `json:"after_end,omitempty"`
	BeforeText  string   `json:"before_text,omitempty"`
	AfterText   string   `json:"after_text,omitempty"`
}

type ParagraphRunSnapshot struct {
	RunID string          `json:"run_id,omitempty"`
	Kind  RunSnapshotKind `json:"kind"`
	Text  string          `json:"text,omitempty"`
	Style TextStyle       `json:"style,omitempty"`
	Image *RunImage       `json:"image,omitempty"`
}

type ParagraphChangeHint struct {
	ParagraphID     string               `json:"paragraph_id"`
	CharacterRanges []CharacterRangeDiff `json:"character_ranges,omitempty"`
}

type ParagraphDiff struct {
	ParagraphID     string                 `json:"paragraph_id"`
	Type            DiffType               `json:"type"`
	BeforeText      string                 `json:"before_text,omitempty"`
	AfterText       string                 `json:"after_text,omitempty"`
	BeforeRuns      []ParagraphRunSnapshot `json:"before_runs,omitempty"`
	AfterRuns       []ParagraphRunSnapshot `json:"after_runs,omitempty"`
	CharacterRanges []CharacterRangeDiff   `json:"character_ranges,omitempty"`
}

func BuildParagraphDiffs(previous, current *Document) []ParagraphDiff {
	prevParagraphs := previousParagraphs(previous)
	currParagraphs := previousParagraphs(current)
	prevMap := make(map[string]Paragraph, len(prevParagraphs))
	currMap := make(map[string]Paragraph, len(currParagraphs))

	for _, paragraph := range prevParagraphs {
		prevMap[paragraph.ID] = paragraph
	}
	for _, paragraph := range currParagraphs {
		currMap[paragraph.ID] = paragraph
	}

	diffs := make([]ParagraphDiff, 0)
	for _, paragraph := range currParagraphs {
		beforeParagraph, existed := prevMap[paragraph.ID]
		after := paragraph.PlainText()
		switch {
		case !existed:
			diffs = append(diffs, ParagraphDiff{
				ParagraphID:     paragraph.ID,
				Type:            DiffInserted,
				AfterText:       after,
				AfterRuns:       snapshotParagraphRuns(paragraph),
				CharacterRanges: buildCharacterRangeDiffs("", after),
			})
		case !paragraphContentEqual(beforeParagraph, paragraph):
			beforeText := beforeParagraph.PlainText()
			diffs = append(diffs, ParagraphDiff{
				ParagraphID:     paragraph.ID,
				Type:            DiffUpdated,
				BeforeText:      beforeText,
				AfterText:       after,
				BeforeRuns:      snapshotParagraphRuns(beforeParagraph),
				AfterRuns:       snapshotParagraphRuns(paragraph),
				CharacterRanges: buildCharacterRangeDiffs(beforeText, after),
			})
		}
	}

	for _, paragraph := range prevParagraphs {
		if _, exists := currMap[paragraph.ID]; !exists {
			before := paragraph.PlainText()
			diffs = append(diffs, ParagraphDiff{
				ParagraphID:     paragraph.ID,
				Type:            DiffDeleted,
				BeforeText:      before,
				BeforeRuns:      snapshotParagraphRuns(paragraph),
				CharacterRanges: buildCharacterRangeDiffs(before, ""),
			})
		}
	}

	return diffs
}

func ApplyParagraphChangeHints(diffs []ParagraphDiff, hints []ParagraphChangeHint) []ParagraphDiff {
	if len(diffs) == 0 || len(hints) == 0 {
		return diffs
	}
	hintMap := make(map[string]ParagraphChangeHint, len(hints))
	for _, hint := range hints {
		if hint.ParagraphID != "" {
			hintMap[hint.ParagraphID] = hint
		}
	}
	for index := range diffs {
		hint, ok := hintMap[diffs[index].ParagraphID]
		if !ok || len(hint.CharacterRanges) == 0 {
			continue
		}
		diffs[index].CharacterRanges = hint.CharacterRanges
	}
	return diffs
}

func paragraphContentEqual(before, after Paragraph) bool {
	if before.PlainText() != after.PlainText() {
		return false
	}
	if !paragraphStyleEqual(before.Style, after.Style) {
		return false
	}
	beforeRuns := snapshotParagraphRuns(before)
	afterRuns := snapshotParagraphRuns(after)
	if len(beforeRuns) != len(afterRuns) {
		return false
	}
	for index := range beforeRuns {
		if !runSnapshotEqual(beforeRuns[index], afterRuns[index]) {
			return false
		}
	}
	return true
}

func paragraphStyleEqual(left, right ParagraphStyle) bool {
	if left.StyleID != right.StyleID ||
		left.Alignment != right.Alignment ||
		left.SpacingBefore != right.SpacingBefore ||
		left.SpacingAfter != right.SpacingAfter ||
		left.LineSpacing != right.LineSpacing ||
		left.LineSpacingRule != right.LineSpacingRule ||
		left.FirstLineIndent != right.FirstLineIndent ||
		left.LeftIndent != right.LeftIndent {
		return false
	}
	if left.Numbering == nil || right.Numbering == nil {
		return left.Numbering == nil && right.Numbering == nil
	}
	return left.Numbering.NumID == right.Numbering.NumID && left.Numbering.Level == right.Numbering.Level
}

func snapshotParagraphRuns(paragraph Paragraph) []ParagraphRunSnapshot {
	if len(paragraph.Runs) == 0 {
		return nil
	}
	runs := make([]ParagraphRunSnapshot, 0, len(paragraph.Runs))
	for _, run := range paragraph.Runs {
		snapshot := ParagraphRunSnapshot{RunID: run.ID}
		if run.Image != nil || strings.TrimSpace(run.DrawingXML) != "" {
			snapshot.Kind = RunSnapshotImage
			if run.Image != nil {
				image := *run.Image
				snapshot.Image = &image
			}
		} else {
			snapshot.Kind = RunSnapshotText
			snapshot.Text = run.Text
			snapshot.Style = run.Style
		}
		runs = append(runs, snapshot)
	}
	return runs
}

func runSnapshotEqual(before, after ParagraphRunSnapshot) bool {
	if before.Kind != after.Kind || before.Text != after.Text || !textStyleEqual(before.Style, after.Style) {
		return false
	}
	if before.Image == nil || after.Image == nil {
		return before.Image == nil && after.Image == nil
	}
	return before.Image.RelationshipID == after.Image.RelationshipID &&
		before.Image.Target == after.Image.Target &&
		before.Image.Filename == after.Image.Filename &&
		before.Image.ContentType == after.Image.ContentType &&
		before.Image.Name == after.Image.Name &&
		before.Image.WidthEMU == after.Image.WidthEMU &&
		before.Image.HeightEMU == after.Image.HeightEMU &&
		before.Image.Inline == after.Image.Inline
}

func textStyleEqual(left, right TextStyle) bool {
	return left.StyleID == right.StyleID &&
		left.FontFamily == right.FontFamily &&
		left.ThemeFontASCII == right.ThemeFontASCII &&
		left.ThemeFontEastAsia == right.ThemeFontEastAsia &&
		left.FontSizeHP == right.FontSizeHP &&
		left.Bold == right.Bold &&
		left.BoldSet == right.BoldSet &&
		left.Italic == right.Italic &&
		left.ItalicSet == right.ItalicSet &&
		left.Underline == right.Underline &&
		left.UnderlineSet == right.UnderlineSet &&
		left.Color == right.Color &&
		left.ThemeColor == right.ThemeColor
}

func buildCharacterRangeDiffs(before, after string) []CharacterRangeDiff {
	beforeRunes := []rune(before)
	afterRunes := []rune(after)
	if string(beforeRunes) == string(afterRunes) {
		return nil
	}
	if len(beforeRunes) == 0 {
		return []CharacterRangeDiff{{Type: DiffInserted, AfterStart: 0, AfterEnd: len(afterRunes), AfterText: after}}
	}
	if len(afterRunes) == 0 {
		return []CharacterRangeDiff{{Type: DiffDeleted, BeforeStart: 0, BeforeEnd: len(beforeRunes), BeforeText: before}}
	}
	if len(beforeRunes)*len(afterRunes) > 24000 {
		return buildFallbackCharacterRangeDiffs(beforeRunes, afterRunes)
	}

	type pendingRange struct {
		beforeStart int
		afterStart  int
		beforeText  []rune
		afterText   []rune
	}
	flush := func(ranges []CharacterRangeDiff, current *pendingRange) []CharacterRangeDiff {
		if current == nil {
			return ranges
		}
		rangeType := DiffUpdated
		if len(current.beforeText) == 0 {
			rangeType = DiffInserted
		} else if len(current.afterText) == 0 {
			rangeType = DiffDeleted
		}
		return append(ranges, CharacterRangeDiff{
			Type:        rangeType,
			BeforeStart: current.beforeStart,
			BeforeEnd:   current.beforeStart + len(current.beforeText),
			AfterStart:  current.afterStart,
			AfterEnd:    current.afterStart + len(current.afterText),
			BeforeText:  string(current.beforeText),
			AfterText:   string(current.afterText),
		})
	}

	dp := make([][]int, len(beforeRunes)+1)
	for i := range dp {
		dp[i] = make([]int, len(afterRunes)+1)
	}
	for i := len(beforeRunes) - 1; i >= 0; i-- {
		for j := len(afterRunes) - 1; j >= 0; j-- {
			if beforeRunes[i] == afterRunes[j] {
				dp[i][j] = dp[i+1][j+1] + 1
				continue
			}
			if dp[i+1][j] >= dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	ranges := make([]CharacterRangeDiff, 0)
	var current *pendingRange
	beforePos := 0
	afterPos := 0
	i, j := 0, 0
	for i < len(beforeRunes) && j < len(afterRunes) {
		if beforeRunes[i] == afterRunes[j] {
			ranges = flush(ranges, current)
			current = nil
			i++
			j++
			beforePos++
			afterPos++
			continue
		}
		if current == nil {
			current = &pendingRange{beforeStart: beforePos, afterStart: afterPos}
		}
		if dp[i+1][j] >= dp[i][j+1] {
			current.beforeText = append(current.beforeText, beforeRunes[i])
			i++
			beforePos++
			continue
		}
		current.afterText = append(current.afterText, afterRunes[j])
		j++
		afterPos++
	}
	for i < len(beforeRunes) {
		if current == nil {
			current = &pendingRange{beforeStart: beforePos, afterStart: afterPos}
		}
		current.beforeText = append(current.beforeText, beforeRunes[i])
		i++
		beforePos++
	}
	for j < len(afterRunes) {
		if current == nil {
			current = &pendingRange{beforeStart: beforePos, afterStart: afterPos}
		}
		current.afterText = append(current.afterText, afterRunes[j])
		j++
		afterPos++
	}
	ranges = flush(ranges, current)
	return normalizeCharacterRanges(ranges)
}

func buildFallbackCharacterRangeDiffs(beforeRunes, afterRunes []rune) []CharacterRangeDiff {
	prefix := 0
	for prefix < len(beforeRunes) && prefix < len(afterRunes) && beforeRunes[prefix] == afterRunes[prefix] {
		prefix++
	}
	beforeSuffix := len(beforeRunes)
	afterSuffix := len(afterRunes)
	for beforeSuffix > prefix && afterSuffix > prefix && beforeRunes[beforeSuffix-1] == afterRunes[afterSuffix-1] {
		beforeSuffix--
		afterSuffix--
	}
	beforeText := string(beforeRunes[prefix:beforeSuffix])
	afterText := string(afterRunes[prefix:afterSuffix])
	rangeType := DiffUpdated
	if beforeText == "" {
		rangeType = DiffInserted
	} else if afterText == "" {
		rangeType = DiffDeleted
	}
	return normalizeCharacterRanges([]CharacterRangeDiff{{
		Type:        rangeType,
		BeforeStart: prefix,
		BeforeEnd:   beforeSuffix,
		AfterStart:  prefix,
		AfterEnd:    afterSuffix,
		BeforeText:  beforeText,
		AfterText:   afterText,
	}})
}

func normalizeCharacterRanges(ranges []CharacterRangeDiff) []CharacterRangeDiff {
	if len(ranges) <= 1 {
		return ranges
	}
	normalized := make([]CharacterRangeDiff, 0, len(ranges))
	for _, current := range ranges {
		if len(normalized) == 0 {
			normalized = append(normalized, current)
			continue
		}
		last := &normalized[len(normalized)-1]
		if !rangesAreMergeable(*last, current) {
			normalized = append(normalized, current)
			continue
		}
		last.BeforeEnd = maxInt(last.BeforeEnd, current.BeforeEnd)
		last.AfterEnd = maxInt(last.AfterEnd, current.AfterEnd)
		last.BeforeText += current.BeforeText
		last.AfterText += current.AfterText
		last.Type = resolveRangeType(last.BeforeText, last.AfterText)
	}
	return normalized
}

func rangesAreMergeable(left, right CharacterRangeDiff) bool {
	return right.BeforeStart <= left.BeforeEnd && right.AfterStart <= left.AfterEnd ||
		(right.BeforeStart == left.BeforeEnd && right.AfterStart == left.AfterEnd) ||
		(right.BeforeStart == left.BeforeStart && right.AfterStart == left.AfterEnd) ||
		(right.BeforeStart == left.BeforeEnd && right.AfterStart == left.AfterStart)
}

func resolveRangeType(beforeText, afterText string) DiffType {
	if beforeText == "" {
		return DiffInserted
	}
	if afterText == "" {
		return DiffDeleted
	}
	return DiffUpdated
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func previousParagraphs(document *Document) []Paragraph {
	if document == nil {
		return nil
	}
	if len(document.Blocks) > 0 {
		return flattenParagraphs(document.Blocks)
	}
	return document.Paragraphs
}
