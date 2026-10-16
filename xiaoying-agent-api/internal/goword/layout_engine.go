package goword

import (
	"fmt"
	"strings"
	"time"
)

type LayoutEngine interface {
	Name() string
	Version() string
	Layout(document *Document, options LayoutOptions) (*DocumentLayout, error)
}

type semanticLayoutEngine struct{}

func NewSemanticLayoutEngine() LayoutEngine {
	return semanticLayoutEngine{}
}

func (semanticLayoutEngine) Name() string {
	return "semantic-layout"
}

func (semanticLayoutEngine) Version() string {
	return "stage1"
}

func (semanticLayoutEngine) Layout(document *Document, options LayoutOptions) (*DocumentLayout, error) {
	if document == nil {
		return nil, fmt.Errorf("document is nil")
	}

	document.ensureBlockStructure()
	options = options.normalized(document.Meta.SectionLayout)
	contentTop := options.Page.MarginTopTwips
	contentBottom := options.Page.HeightTwips - options.Page.MarginBottomTwips
	if contentBottom <= contentTop {
		return nil, fmt.Errorf("invalid page box")
	}

	currentPage := LayoutPage{
		Index:              0,
		Number:             options.StartPage,
		WidthTwips:         options.Page.WidthTwips,
		HeightTwips:        options.Page.HeightTwips,
		ContentTopTwips:    contentTop,
		ContentBottomTwips: contentBottom,
	}
	pages := make([]LayoutPage, 0, 1)
	cursor := contentTop
	warnings := make([]string, 0)
	startNewPage := func() {
		pages = append(pages, currentPage)
		currentPage = LayoutPage{
			Index:              len(pages),
			Number:             options.StartPage + len(pages),
			WidthTwips:         options.Page.WidthTwips,
			HeightTwips:        options.Page.HeightTwips,
			ContentTopTwips:    contentTop,
			ContentBottomTwips: contentBottom,
		}
		cursor = contentTop
	}

	for blockIndex, block := range document.Blocks {
		if block.Type == BlockTypeTable && block.Table != nil {
			rowLayouts := buildSemanticTableRowLayouts(*block.Table)
			headerRows := collectSemanticRepeatHeaderRows(rowLayouts)
			rowStart := 0
			firstChunk := true
			for rowStart < len(rowLayouts) {
				repeatHeader := !firstChunk && len(headerRows) > 0
				minChunkHeight := minimumSemanticTableChunkHeight(rowLayouts, headerRows, rowStart, repeatHeader)
				if len(currentPage.Blocks) > 0 && cursor+minChunkHeight > contentBottom {
					startNewPage()
					repeatHeader = !firstChunk && len(headerRows) > 0
				}
				blockLayout, nextRowStart, warning := buildSemanticTableChunkLayout(block.Table.ID, blockIndex, rowLayouts, headerRows, rowStart, cursor, contentBottom-cursor, repeatHeader)
				if warning != "" {
					warnings = append(warnings, warning)
				}
				if nextRowStart <= rowStart {
					warnings = append(warnings, fmt.Sprintf("table %s could not advance pagination at row %d", block.Table.ID, rowStart))
					break
				}
				currentPage.Blocks = append(currentPage.Blocks, blockLayout)
				cursor = blockLayout.TopTwips + blockLayout.HeightTwips + 120
				rowStart = nextRowStart
				if rowStart < len(rowLayouts) {
					startNewPage()
				}
				firstChunk = false
			}
			continue
		}
		blockLayout := buildSemanticBlockLayout(block, blockIndex, cursor)
		blockBottom := blockLayout.TopTwips + blockLayout.HeightTwips
		if len(currentPage.Blocks) > 0 && blockBottom > contentBottom {
			startNewPage()
			blockLayout = buildSemanticBlockLayout(block, blockIndex, cursor)
			blockBottom = blockLayout.TopTwips + blockLayout.HeightTwips
		}
		if blockLayout.HeightTwips > contentBottom-contentTop {
			warnings = append(warnings, fmt.Sprintf("block %d exceeds page content height", blockIndex))
		}
		currentPage.Blocks = append(currentPage.Blocks, blockLayout)
		cursor = blockBottom + 120
	}

	pages = append(pages, currentPage)

	return &DocumentLayout{
		DocumentID:  document.ID,
		Revision:    document.Revision,
		GeneratedAt: time.Now(),
		PageSpec:    options.Page,
		Engine: LayoutEngineInfo{
			Name:    (semanticLayoutEngine{}).Name(),
			Version: (semanticLayoutEngine{}).Version(),
		},
		Pages:    pages,
		Warnings: warnings,
	}, nil
}

func buildSemanticTableRowLayouts(table Table) []TableRowLayout {
	rowTop := 0
	rows := make([]TableRowLayout, 0, len(table.Rows))
	for _, row := range table.Rows {
		cellLayouts := make([]TableCellLayout, 0, len(row.Cells))
		rowHeight := 0
		for _, cell := range row.Cells {
			paragraphLayouts := make([]ParagraphLayout, 0)
			cellHeight := 0
			for _, block := range cell.Blocks {
				if block.Type != BlockTypeParagraph || block.Paragraph == nil {
					continue
				}
				paragraphLayout, paragraphHeight := buildSemanticParagraphLayout(*block.Paragraph, rowTop+cellHeight)
				paragraphLayouts = append(paragraphLayouts, paragraphLayout)
				cellHeight += paragraphHeight
			}
			if cellHeight == 0 {
				cellHeight = 360
			}
			if cellHeight > rowHeight {
				rowHeight = cellHeight
			}
			cellLayouts = append(cellLayouts, TableCellLayout{
				CellID:     cell.ID,
				Paragraphs: paragraphLayouts,
				BlockCount: len(cell.Blocks),
			})
		}
		if rowHeight == 0 {
			rowHeight = 360
		}
		rows = append(rows, TableRowLayout{
			RowID:        row.ID,
			TopTwips:     rowTop,
			HeightTwips:  rowHeight,
			RepeatHeader: row.RepeatHeader,
			CantSplit:    row.CantSplit,
			Cells:        cellLayouts,
		})
		rowTop += rowHeight
	}
	return rows
}

func collectSemanticRepeatHeaderRows(rows []TableRowLayout) []TableRowLayout {
	count := 0
	for _, row := range rows {
		if !row.RepeatHeader {
			break
		}
		count++
	}
	if count == 0 {
		return nil
	}
	headers := make([]TableRowLayout, count)
	copy(headers, rows[:count])
	return headers
}

func minimumSemanticTableChunkHeight(rows, headerRows []TableRowLayout, rowStart int, repeatHeader bool) int {
	height := 120
	if repeatHeader {
		height += semanticTableRowsHeight(headerRows)
	}
	for rowStart < len(rows) {
		if repeatHeader && rowStart < len(headerRows) {
			rowStart++
			continue
		}
		height += rows[rowStart].HeightTwips
		break
	}
	return height
}

func buildSemanticTableChunkLayout(tableID string, blockIndex int, rows, headerRows []TableRowLayout, rowStart, topTwips, availableHeight int, repeatHeader bool) (LayoutBlock, int, string) {
	chunkRows := make([]TableRowLayout, 0)
	usedHeight := 0
	warning := ""
	appendRow := func(row TableRowLayout) {
		shifted := shiftSemanticTableRowLayout(row, topTwips+usedHeight-row.TopTwips)
		chunkRows = append(chunkRows, shifted)
		usedHeight += row.HeightTwips
	}
	if repeatHeader && len(headerRows) > 0 {
		for _, headerRow := range headerRows {
			appendRow(headerRow)
		}
	}
	for rowStart < len(rows) {
		if repeatHeader && rowStart < len(headerRows) {
			rowStart++
			continue
		}
		row := rows[rowStart]
		requiredHeight := usedHeight + row.HeightTwips + 120
		if len(chunkRows) > 0 && requiredHeight > availableHeight {
			break
		}
		appendRow(row)
		rowStart++
		if usedHeight+120 > availableHeight {
			warning = fmt.Sprintf("table %s row %s exceeds remaining page content height", tableID, row.RowID)
			break
		}
	}
	if len(chunkRows) == 0 && rowStart < len(rows) {
		row := rows[rowStart]
		appendRow(row)
		rowStart++
		warning = fmt.Sprintf("table %s row %s exceeds page content height", tableID, row.RowID)
	}
	return LayoutBlock{
		ID:               tableID,
		Type:             BlockTypeTable,
		SourceBlockIndex: blockIndex,
		TopTwips:         topTwips,
		HeightTwips:      usedHeight + 120,
		Table: &TableLayout{
			TableID:  tableID,
			RowCount: len(chunkRows),
			Rows:     chunkRows,
		},
	}, rowStart, warning
}

func semanticTableRowsHeight(rows []TableRowLayout) int {
	total := 0
	for _, row := range rows {
		total += row.HeightTwips
	}
	return total
}

func shiftSemanticTableRowLayout(row TableRowLayout, delta int) TableRowLayout {
	shifted := row
	shifted.TopTwips += delta
	if len(row.Cells) == 0 {
		return shifted
	}
	shifted.Cells = make([]TableCellLayout, len(row.Cells))
	for cellIndex, cell := range row.Cells {
		shiftedCell := cell
		if len(cell.Paragraphs) > 0 {
			shiftedCell.Paragraphs = make([]ParagraphLayout, len(cell.Paragraphs))
			for paragraphIndex, paragraph := range cell.Paragraphs {
				shiftedParagraph := paragraph
				if len(paragraph.Lines) > 0 {
					shiftedParagraph.Lines = make([]LineLayout, len(paragraph.Lines))
					for lineIndex, line := range paragraph.Lines {
						shiftedLine := line
						shiftedLine.TopTwips += delta
						if len(line.Runs) > 0 {
							shiftedLine.Runs = append([]RunLayout(nil), line.Runs...)
						}
						shiftedParagraph.Lines[lineIndex] = shiftedLine
					}
				}
				shiftedCell.Paragraphs[paragraphIndex] = shiftedParagraph
			}
		}
		shifted.Cells[cellIndex] = shiftedCell
	}
	return shifted
}

func buildSemanticBlockLayout(block Block, blockIndex, topTwips int) LayoutBlock {
	layout := LayoutBlock{
		Type:             block.Type,
		SourceBlockIndex: blockIndex,
		TopTwips:         topTwips,
	}

	switch block.Type {
	case BlockTypeTable:
		if block.Table != nil {
			layout.ID = block.Table.ID
			tableLayout, height := buildSemanticTableLayout(*block.Table, topTwips)
			layout.Table = &tableLayout
			layout.HeightTwips = height
			return layout
		}
	default:
		if block.Paragraph != nil {
			layout.ID = block.Paragraph.ID
			paragraphLayout, height := buildSemanticParagraphLayout(*block.Paragraph, topTwips)
			layout.Paragraph = &paragraphLayout
			layout.HeightTwips = height
			return layout
		}
	}

	layout.HeightTwips = 240
	return layout
}

func buildSemanticParagraphLayout(paragraph Paragraph, topTwips int) (ParagraphLayout, int) {
	lineHeight := 360
	lines := make([]LineLayout, 0, 1)
	runs := make([]RunLayout, 0, len(paragraph.Runs))
	textParts := make([]string, 0, len(paragraph.Runs))
	runOffset := 0
	for _, run := range paragraph.Runs {
		width := estimateRunWidthTwips(run)
		runs = append(runs, RunLayout{
			RunID:      run.ID,
			Text:       run.Text,
			BreakType:  run.BreakType,
			WidthTwips: width,
			StartTwips: runOffset,
			HasDrawing: run.DrawingXML != "" || run.Image != nil,
		})
		runOffset += width
		if run.Text != "" {
			textParts = append(textParts, run.Text)
		}
	}
	lines = append(lines, LineLayout{
		Index:       0,
		Text:        strings.Join(textParts, ""),
		TopTwips:    topTwips,
		HeightTwips: lineHeight,
		Runs:        runs,
	})

	return ParagraphLayout{
		ParagraphID: paragraph.ID,
		LineCount:   len(lines),
		Lines:       lines,
	}, lineHeight + maxLayoutInt(paragraph.Style.SpacingAfter, 120)
}

func buildSemanticTableLayout(table Table, topTwips int) (TableLayout, int) {
	rows := buildSemanticTableRowLayouts(table)
	totalHeight := semanticTableRowsHeight(rows)
	shiftedRows := make([]TableRowLayout, 0, len(rows))
	for _, row := range rows {
		shiftedRows = append(shiftedRows, shiftSemanticTableRowLayout(row, topTwips-row.TopTwips))
	}

	return TableLayout{
		TableID:  table.ID,
		RowCount: len(shiftedRows),
		Rows:     shiftedRows,
	}, totalHeight + 120
}

func estimateRunWidthTwips(run Run) int {
	if run.Image != nil {
		if run.Image.WidthEMU > 0 {
			return int(run.Image.WidthEMU / 635)
		}
		return 1440
	}
	textLength := len([]rune(run.Text))
	if textLength == 0 {
		if run.BreakType != "" {
			return 0
		}
		return 120
	}
	return textLength * 120
}

func maxLayoutInt(values ...int) int {
	max := 0
	for _, value := range values {
		if value > max {
			max = value
		}
	}
	return max
}
