package goword

import (
	"fmt"
	"regexp"
	"strings"
)

var placeholderTableIDPattern = regexp.MustCompile(`(?i)^(?:t|tbl|table)[_-]?\d+$`)

func applyInsertTableRow(document *Document, operation Operation) error {
	table, err := findTable(document, operation.TableID)
	if err != nil {
		return err
	}
	columnCount := maxTableColumns(table)
	rowInput := TableRowInput{}
	if operation.TableRow != nil {
		rowInput = *operation.TableRow
	}
	expectedColumns := columnCount
	if len(rowInput.Cells) > expectedColumns {
		expectedColumns = len(rowInput.Cells)
	}
	if expectedColumns == 0 {
		expectedColumns = 1
	}
	ensureTableColumnCount(table, expectedColumns)
	templateRow := resolveTableRowTemplate(table, operation.RowIndex, operation.Position)
	row := buildTableRowFromInputWithTemplate(rowInput, expectedColumns, templateRow)
	insertIndex := operation.RowIndex
	if operation.Position == "" || operation.Position == "after" {
		insertIndex = operation.RowIndex + 1
		if operation.RowIndex == len(table.Rows) {
			insertIndex = len(table.Rows)
		}
	}
	if insertIndex < 0 || insertIndex > len(table.Rows) {
		return fmt.Errorf("invalid row index %d for table %s", operation.RowIndex, operation.TableID)
	}
	table.Rows = append(table.Rows, TableRow{})
	copy(table.Rows[insertIndex+1:], table.Rows[insertIndex:])
	table.Rows[insertIndex] = row
	ensureTableGridMatchesWidth(table, expectedColumns)
	return nil
}

func applyDeleteTableRow(document *Document, operation Operation) error {
	table, err := findTable(document, operation.TableID)
	if err != nil {
		return err
	}
	if len(table.Rows) <= 1 {
		return fmt.Errorf("table %s must keep at least one row", operation.TableID)
	}
	if operation.RowIndex < 0 || operation.RowIndex >= len(table.Rows) {
		return fmt.Errorf("invalid row index %d for table %s", operation.RowIndex, operation.TableID)
	}
	table.Rows = append(table.Rows[:operation.RowIndex], table.Rows[operation.RowIndex+1:]...)
	ensureTableGridMatchesWidth(table, maxTableColumns(table))
	return nil
}

func applyInsertTableColumn(document *Document, operation Operation) error {
	table, err := findTable(document, operation.TableID)
	if err != nil {
		return err
	}
	if tableHasUnsupportedColumnStructure(table) {
		return fmt.Errorf("table %s contains merged cells or irregular column structure; column edits are not supported", operation.TableID)
	}
	if len(table.Rows) == 0 {
		return fmt.Errorf("table %s has no rows", operation.TableID)
	}
	columnCount := maxTableColumns(table)
	if columnCount == 0 {
		columnCount = 1
	}
	ensureTableColumnCount(table, columnCount)
	insertIndex := operation.CellIndex
	if operation.Position == "" || operation.Position == "after" {
		insertIndex = operation.CellIndex + 1
		if operation.CellIndex == columnCount {
			insertIndex = columnCount
		}
	}
	if insertIndex < 0 || insertIndex > columnCount {
		return fmt.Errorf("invalid column index %d for table %s", operation.CellIndex, operation.TableID)
	}
	templateCells := []TableCellInput(nil)
	if operation.TableColumn != nil {
		templateCells = operation.TableColumn.Cells
		if len(templateCells) != 0 && len(templateCells) != 1 && len(templateCells) != len(table.Rows) {
			return fmt.Errorf("table_column cells must be empty, one cell, or match table row count")
		}
	}
	for rowIndex := range table.Rows {
		cellInput := TableCellInput{}
		if len(templateCells) == 1 {
			cellInput = templateCells[0]
		} else if len(templateCells) == len(table.Rows) {
			cellInput = templateCells[rowIndex]
		}
		templateCell := resolveTableColumnTemplateCell(&table.Rows[rowIndex], operation.CellIndex, operation.Position)
		cell := buildTableCellFromInputWithTemplate(cellInput, templateCell)
		row := &table.Rows[rowIndex]
		row.Cells = append(row.Cells, TableCell{})
		copy(row.Cells[insertIndex+1:], row.Cells[insertIndex:])
		row.Cells[insertIndex] = cell
	}
	ensureTableGridMatchesWidth(table, columnCount+1)
	return nil
}

func applyDeleteTableColumn(document *Document, operation Operation) error {
	table, err := findTable(document, operation.TableID)
	if err != nil {
		return err
	}
	if tableHasUnsupportedColumnStructure(table) {
		return fmt.Errorf("table %s contains merged cells or irregular column structure; column edits are not supported", operation.TableID)
	}
	columnCount := maxTableColumns(table)
	if columnCount <= 1 {
		return fmt.Errorf("table %s must keep at least one column", operation.TableID)
	}
	if operation.CellIndex < 0 || operation.CellIndex >= columnCount {
		return fmt.Errorf("invalid column index %d for table %s", operation.CellIndex, operation.TableID)
	}
	ensureTableColumnCount(table, columnCount)
	for rowIndex := range table.Rows {
		row := &table.Rows[rowIndex]
		row.Cells = append(row.Cells[:operation.CellIndex], row.Cells[operation.CellIndex+1:]...)
	}
	ensureTableGridMatchesWidth(table, columnCount-1)
	return nil
}

func applyUpdateTableCell(document *Document, operation Operation) error {
	table, err := findTable(document, operation.TableID)
	if err != nil {
		return err
	}
	if operation.TableCell == nil {
		return fmt.Errorf("table_cell payload is required")
	}
	if operation.RowIndex < 0 || operation.RowIndex >= len(table.Rows) {
		return fmt.Errorf("invalid row index %d for table %s", operation.RowIndex, operation.TableID)
	}
	row := &table.Rows[operation.RowIndex]
	if operation.CellIndex < 0 || operation.CellIndex >= len(row.Cells) {
		return fmt.Errorf("invalid cell index %d for table %s row %d", operation.CellIndex, operation.TableID, operation.RowIndex)
	}
	template := row.Cells[operation.CellIndex]
	replacement := buildTableCellFromInputWithTemplate(*operation.TableCell, &template)
	replacement.ID = template.ID
	if replacement.PropertiesXML == "" {
		replacement.PropertiesXML = template.PropertiesXML
	}
	row.Cells[operation.CellIndex] = replacement
	return nil
}

func findTable(document *Document, tableID string) (*Table, error) {
	if document == nil {
		return nil, fmt.Errorf("document is nil")
	}
	document.ensureBlockStructure()
	table := findTableInBlocks(document.Blocks, tableID)
	if table == nil {
		if resolved := resolveUniquePlaceholderTable(document.Blocks, tableID); resolved != nil {
			return resolved, nil
		}
	}
	if table == nil {
		return nil, fmt.Errorf("未找到目标表格: %s", tableID)
	}
	return table, nil
}

func resolveUniquePlaceholderTable(blocks []Block, tableID string) *Table {
	trimmedTableID := strings.TrimSpace(tableID)
	if trimmedTableID == "" || !placeholderTableIDPattern.MatchString(trimmedTableID) {
		return nil
	}
	tables := collectTablesInBlocks(blocks, nil)
	if len(tables) != 1 {
		return nil
	}
	return tables[0]
}

func collectTablesInBlocks(blocks []Block, tables []*Table) []*Table {
	for index := range blocks {
		block := &blocks[index]
		if block.Type != BlockTypeTable || block.Table == nil {
			continue
		}
		tables = append(tables, block.Table)
		for rowIndex := range block.Table.Rows {
			for cellIndex := range block.Table.Rows[rowIndex].Cells {
				tables = collectTablesInBlocks(block.Table.Rows[rowIndex].Cells[cellIndex].Blocks, tables)
			}
		}
	}
	return tables
}

func findTableInBlocks(blocks []Block, tableID string) *Table {
	for index := range blocks {
		block := &blocks[index]
		switch block.Type {
		case BlockTypeTable:
			if block.Table != nil && block.Table.ID == tableID {
				return block.Table
			}
			if block.Table == nil {
				continue
			}
			for rowIndex := range block.Table.Rows {
				for cellIndex := range block.Table.Rows[rowIndex].Cells {
					if nested := findTableInBlocks(block.Table.Rows[rowIndex].Cells[cellIndex].Blocks, tableID); nested != nil {
						return nested
					}
				}
			}
		case BlockTypeParagraph:
			continue
		}
	}
	return nil
}

func maxTableColumns(table *Table) int {
	maxColumns := 0
	for _, row := range table.Rows {
		if len(row.Cells) > maxColumns {
			maxColumns = len(row.Cells)
		}
	}
	return maxColumns
}

func ensureTableColumnCount(table *Table, expectedColumns int) {
	for rowIndex := range table.Rows {
		for len(table.Rows[rowIndex].Cells) < expectedColumns {
			table.Rows[rowIndex].Cells = append(table.Rows[rowIndex].Cells, buildTableCellFromInput(TableCellInput{}))
		}
	}
	ensureTableGridMatchesWidth(table, expectedColumns)
}

func ensureTableGridMatchesWidth(table *Table, columnCount int) {
	if columnCount <= 0 {
		return
	}
	if table.GridXML == "" || countGridColumns(table.GridXML) != columnCount {
		table.GridXML = buildTableFromInput(TableInput{Rows: []TableRowInput{{Cells: make([]TableCellInput, columnCount)}}}).GridXML
	}
	if table.PropertiesXML == "" {
		table.PropertiesXML = `<w:tblStyle w:val="TableGrid"/><w:tblW w:type="auto" w:w="0"/>`
	}
}

func tableHasUnsupportedColumnStructure(table *Table) bool {
	if table == nil || len(table.Rows) == 0 {
		return false
	}
	expectedCellCount := len(table.Rows[0].Cells)
	for _, row := range table.Rows {
		if len(row.Cells) != expectedCellCount {
			return true
		}
		for _, cell := range row.Cells {
			props := strings.ToLower(cell.PropertiesXML)
			if strings.Contains(props, "gridspan") || strings.Contains(props, "hmerge") || strings.Contains(props, "vmerge") {
				return true
			}
		}
	}
	return false
}

func countGridColumns(gridXML string) int {
	if gridXML == "" {
		return 0
	}
	return countOccurrences(gridXML, "<w:gridCol")
}

func countOccurrences(value, token string) int {
	count := 0
	for {
		index := len(value)
		if next := findToken(value, token); next >= 0 {
			count++
			index = next + len(token)
		}
		if index >= len(value) {
			break
		}
		value = value[index:]
	}
	return count
}

func findToken(value, token string) int {
	for index := 0; index+len(token) <= len(value); index++ {
		if value[index:index+len(token)] == token {
			return index
		}
	}
	return -1
}

func buildTableRowFromInput(input TableRowInput, expectedColumns int) TableRow {
	row := TableRow{
		ID:              nextID("row"),
		PropertiesXML:   input.PropertiesXML,
		RepeatHeader:    input.RepeatHeader,
		RepeatHeaderSet: input.RepeatHeaderSet,
		CantSplit:       input.CantSplit,
		CantSplitSet:    input.CantSplitSet,
		Cells:           make([]TableCell, 0, maxTableInputColumns(expectedColumns, len(input.Cells))),
	}
	for _, cellInput := range input.Cells {
		row.Cells = append(row.Cells, buildTableCellFromInput(cellInput))
	}
	for len(row.Cells) < expectedColumns {
		row.Cells = append(row.Cells, buildTableCellFromInput(TableCellInput{}))
	}
	return row
}

func buildTableRowFromInputWithTemplate(input TableRowInput, expectedColumns int, template *TableRow) TableRow {
	row := TableRow{
		ID:              nextID("row"),
		PropertiesXML:   strings.TrimSpace(input.PropertiesXML),
		RepeatHeader:    input.RepeatHeader,
		RepeatHeaderSet: input.RepeatHeaderSet,
		CantSplit:       input.CantSplit,
		CantSplitSet:    input.CantSplitSet,
		Cells:           make([]TableCell, 0, maxTableInputColumns(expectedColumns, len(input.Cells))),
	}
	if template != nil {
		if row.PropertiesXML == "" {
			row.PropertiesXML = template.PropertiesXML
		}
		if !row.RepeatHeaderSet && template.RepeatHeaderSet {
			row.RepeatHeaderSet = true
			row.RepeatHeader = template.RepeatHeader
		}
		if !row.CantSplitSet && template.CantSplitSet {
			row.CantSplitSet = true
			row.CantSplit = template.CantSplit
		}
	}
	templateCells := []TableCell(nil)
	if template != nil {
		templateCells = template.Cells
	}
	for index, cellInput := range input.Cells {
		row.Cells = append(row.Cells, buildTableCellFromInputWithTemplate(cellInput, tableCellTemplateAt(templateCells, index)))
	}
	for len(row.Cells) < expectedColumns {
		row.Cells = append(row.Cells, buildTableCellFromInputWithTemplate(TableCellInput{}, tableCellTemplateAt(templateCells, len(row.Cells))))
	}
	return row
}

func buildTableCellFromInput(input TableCellInput) TableCell {
	cell := TableCell{
		ID:            nextID("cell"),
		PropertiesXML: input.PropertiesXML,
	}
	if len(input.Paragraphs) == 0 {
		paragraph := buildParagraphFromInput(ParagraphInput{})
		cell.Blocks = []Block{{Type: BlockTypeParagraph, Paragraph: &paragraph}}
		return cell
	}
	cell.Blocks = make([]Block, 0, len(input.Paragraphs))
	for _, paragraphInput := range input.Paragraphs {
		paragraph := buildParagraphFromInput(paragraphInput)
		cell.Blocks = append(cell.Blocks, Block{Type: BlockTypeParagraph, Paragraph: &paragraph})
	}
	return cell
}

func buildTableCellFromInputWithTemplate(input TableCellInput, template *TableCell) TableCell {
	cell := TableCell{
		ID:            nextID("cell"),
		PropertiesXML: strings.TrimSpace(input.PropertiesXML),
	}
	if cell.PropertiesXML == "" && template != nil {
		cell.PropertiesXML = template.PropertiesXML
	}
	templateParagraphs := templateCellParagraphs(template)
	if len(input.Paragraphs) == 0 {
		paragraph := buildParagraphFromInputWithTemplate(ParagraphInput{}, paragraphTemplateAt(templateParagraphs, 0))
		cell.Blocks = []Block{{Type: BlockTypeParagraph, Paragraph: &paragraph}}
		return cell
	}
	cell.Blocks = make([]Block, 0, len(input.Paragraphs))
	for index, paragraphInput := range input.Paragraphs {
		paragraph := buildParagraphFromInputWithTemplate(paragraphInput, paragraphTemplateAt(templateParagraphs, index))
		cell.Blocks = append(cell.Blocks, Block{Type: BlockTypeParagraph, Paragraph: &paragraph})
	}
	return cell
}

func templateCellParagraphs(template *TableCell) []*Paragraph {
	if template == nil || len(template.Blocks) == 0 {
		return nil
	}
	paragraphs := make([]*Paragraph, 0, len(template.Blocks))
	for index := range template.Blocks {
		block := &template.Blocks[index]
		if block.Type == BlockTypeParagraph && block.Paragraph != nil {
			paragraphs = append(paragraphs, block.Paragraph)
		}
	}
	return paragraphs
}

func tableCellTemplateAt(cells []TableCell, index int) *TableCell {
	if index < 0 || index >= len(cells) {
		return nil
	}
	return &cells[index]
}

func resolveTableRowTemplate(table *Table, rowIndex int, position string) *TableRow {
	if table == nil || len(table.Rows) == 0 {
		return nil
	}
	if rowIndex < 0 || rowIndex >= len(table.Rows) {
		if strings.EqualFold(strings.TrimSpace(position), "before") {
			return &table.Rows[0]
		}
		return &table.Rows[len(table.Rows)-1]
	}
	anchor := &table.Rows[rowIndex]
	if !tableRowLooksLikeHeader(anchor) {
		return anchor
	}
	if strings.EqualFold(strings.TrimSpace(position), "before") {
		if candidate := findNearestNonHeaderTableRow(table, rowIndex-1, -1); candidate != nil {
			return candidate
		}
		if candidate := findNearestNonHeaderTableRow(table, rowIndex+1, 1); candidate != nil {
			return candidate
		}
		return anchor
	}
	if candidate := findNearestNonHeaderTableRow(table, rowIndex+1, 1); candidate != nil {
		return candidate
	}
	if candidate := findNearestNonHeaderTableRow(table, rowIndex-1, -1); candidate != nil {
		return candidate
	}
	return anchor
}

func findNearestNonHeaderTableRow(table *Table, startIndex int, step int) *TableRow {
	if table == nil || step == 0 {
		return nil
	}
	for index := startIndex; index >= 0 && index < len(table.Rows); index += step {
		if !tableRowLooksLikeHeader(&table.Rows[index]) {
			return &table.Rows[index]
		}
	}
	return nil
}

func tableRowLooksLikeHeader(row *TableRow) bool {
	if row == nil {
		return false
	}
	return row.RepeatHeaderSet || row.RepeatHeader
}

func resolveTableColumnTemplateCell(row *TableRow, cellIndex int, position string) *TableCell {
	if row == nil || len(row.Cells) == 0 {
		return nil
	}
	if cellIndex < 0 {
		return &row.Cells[0]
	}
	if cellIndex >= len(row.Cells) {
		if strings.EqualFold(strings.TrimSpace(position), "before") {
			return &row.Cells[0]
		}
		return &row.Cells[len(row.Cells)-1]
	}
	return &row.Cells[cellIndex]
}

func paragraphTemplateAt(paragraphs []*Paragraph, index int) *Paragraph {
	if index < 0 || index >= len(paragraphs) {
		return nil
	}
	return paragraphs[index]
}

func buildParagraphFromInputWithTemplate(input ParagraphInput, template *Paragraph) Paragraph {
	paragraph := Paragraph{ID: nextID("para")}
	if template != nil {
		paragraph.Style = cloneParagraphStyle(template.Style)
	}
	mergeParagraphStyle(&paragraph.Style, input.Style)

	if len(input.Runs) > 0 {
		paragraph.Runs = buildRunsFromInputWithTemplate(input.Runs, template)
	} else {
		baseStyle := TextStyle{}
		if template != nil {
			baseStyle = firstTextRunStyle(template.Runs)
		}
		paragraph.Runs = []Run{{ID: nextID("run"), Text: input.Text, Style: baseStyle}}
	}
	if len(paragraph.Runs) == 0 {
		baseStyle := TextStyle{}
		if template != nil {
			baseStyle = firstTextRunStyle(template.Runs)
		}
		paragraph.Runs = []Run{{ID: nextID("run"), Text: input.Text, Style: baseStyle}}
	}
	return paragraph
}

func buildRunsFromInputWithTemplate(inputRuns []Run, template *Paragraph) []Run {
	if len(inputRuns) == 0 {
		return nil
	}
	templateRuns := []Run(nil)
	defaultStyle := TextStyle{}
	if template != nil {
		templateRuns = template.Runs
		defaultStyle = firstTextRunStyle(template.Runs)
	}
	runs := make([]Run, 0, len(inputRuns))
	for index, inputRun := range inputRuns {
		cloned := cloneRun(inputRun)
		if strings.TrimSpace(cloned.ID) == "" {
			cloned.ID = nextID("run")
		}
		baseStyle := defaultStyle
		if index < len(templateRuns) {
			baseStyle = templateRuns[index].Style
		}
		mergeTextStyle(&baseStyle, cloned.Style)
		cloned.Style = baseStyle
		runs = append(runs, cloned)
	}
	return runs
}

func maxTableInputColumns(left, right int) int {
	if left > right {
		return left
	}
	return right
}
