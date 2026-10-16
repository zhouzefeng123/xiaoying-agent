package goword

import "strings"

type BlockType string

const (
	BlockTypeParagraph BlockType = "paragraph"
	BlockTypeTable     BlockType = "table"
)

type Document struct {
	ID         string       `json:"id"`
	Name       string       `json:"name"`
	SourcePath string       `json:"source_path,omitempty"`
	Revision   int64        `json:"revision"`
	Blocks     []Block      `json:"blocks,omitempty"`
	Paragraphs []Paragraph  `json:"paragraphs"`
	Meta       DocumentMeta `json:"meta"`
}

type Block struct {
	Type      BlockType  `json:"type"`
	Paragraph *Paragraph `json:"paragraph,omitempty"`
	Table     *Table     `json:"table,omitempty"`
}

type Table struct {
	ID            string     `json:"id"`
	PropertiesXML string     `json:"properties_xml,omitempty"`
	GridXML       string     `json:"grid_xml,omitempty"`
	Rows          []TableRow `json:"rows"`
}

type TableRow struct {
	ID              string      `json:"id"`
	PropertiesXML   string      `json:"properties_xml,omitempty"`
	RepeatHeader    bool        `json:"repeat_header,omitempty"`
	RepeatHeaderSet bool        `json:"repeat_header_set,omitempty"`
	CantSplit       bool        `json:"cant_split,omitempty"`
	CantSplitSet    bool        `json:"cant_split_set,omitempty"`
	Cells           []TableCell `json:"cells"`
}

type TableCell struct {
	ID            string  `json:"id"`
	PropertiesXML string  `json:"properties_xml,omitempty"`
	Blocks        []Block `json:"blocks,omitempty"`
}

type HeaderFooterPart struct {
	Kind           string      `json:"kind"`
	Variant        string      `json:"variant,omitempty"`
	RelationshipID string      `json:"relationship_id,omitempty"`
	Target         string      `json:"target,omitempty"`
	Blocks         []Block     `json:"blocks,omitempty"`
	Paragraphs     []Paragraph `json:"paragraphs,omitempty"`
}

type DocumentMeta struct {
	Format                string                `json:"format,omitempty"`
	SectionXML            string                `json:"-"`
	SectionLayout         SectionLayout         `json:"section_layout,omitempty"`
	Sections              []DocumentSection     `json:"sections,omitempty"`
	HeaderFooters         []HeaderFooterPart    `json:"header_footers,omitempty"`
	ObjectSummary         *OOXMLObjectSummary   `json:"object_summary,omitempty"`
	DefaultParagraphStyle *ParagraphStyle       `json:"default_paragraph_style,omitempty"`
	DefaultTextStyle      *TextStyle            `json:"default_text_style,omitempty"`
	Theme                 *DocumentTheme        `json:"theme,omitempty"`
	Styles                []DocumentStyle       `json:"styles,omitempty"`
	Numbering             []NumberingDefinition `json:"numbering,omitempty"`
}

type OOXMLObjectSummary struct {
	ComplexTables      int `json:"complex_tables,omitempty"`
	MergedTableCells   int `json:"merged_table_cells,omitempty"`
	NestedTables       int `json:"nested_tables,omitempty"`
	InlineImages       int `json:"inline_images,omitempty"`
	AnchoredImages     int `json:"anchored_images,omitempty"`
	LegacyImages       int `json:"legacy_images,omitempty"`
	LinkedImages       int `json:"linked_images,omitempty"`
	TextBoxes          int `json:"text_boxes,omitempty"`
	Shapes             int `json:"shapes,omitempty"`
	Hyperlinks         int `json:"hyperlinks,omitempty"`
	Bookmarks          int `json:"bookmarks,omitempty"`
	SimpleFields       int `json:"simple_fields,omitempty"`
	ComplexFields      int `json:"complex_fields,omitempty"`
	FootnoteReferences int `json:"footnote_references,omitempty"`
	EndnoteReferences  int `json:"endnote_references,omitempty"`
	CommentReferences  int `json:"comment_references,omitempty"`
	Insertions         int `json:"insertions,omitempty"`
	Deletions          int `json:"deletions,omitempty"`
}

type DocumentSection struct {
	StartBlockIndex int                `json:"start_block_index,omitempty"`
	EndBlockIndex   int                `json:"end_block_index,omitempty"`
	Layout          SectionLayout      `json:"layout,omitempty"`
	HeaderFooters   []HeaderFooterPart `json:"header_footers,omitempty"`
}

type DocumentTheme struct {
	Colors map[string]string `json:"colors,omitempty"`
	Fonts  map[string]string `json:"fonts,omitempty"`
}

type DocumentStyle struct {
	ID        string         `json:"id"`
	Name      string         `json:"name,omitempty"`
	Type      string         `json:"type,omitempty"`
	BasedOn   string         `json:"based_on,omitempty"`
	IsDefault bool           `json:"is_default,omitempty"`
	Paragraph ParagraphStyle `json:"paragraph,omitempty"`
	Text      TextStyle      `json:"text,omitempty"`
}

type NumberingDefinition struct {
	NumID      string           `json:"num_id"`
	AbstractID string           `json:"abstract_id,omitempty"`
	Levels     []NumberingLevel `json:"levels,omitempty"`
}

type NumberingLevel struct {
	Level     int            `json:"level"`
	Start     int            `json:"start,omitempty"`
	Format    string         `json:"format,omitempty"`
	Text      string         `json:"text,omitempty"`
	Paragraph ParagraphStyle `json:"paragraph,omitempty"`
	TextStyle TextStyle      `json:"text_style,omitempty"`
}

type Paragraph struct {
	ID                 string         `json:"id"`
	Runs               []Run          `json:"runs"`
	Style              ParagraphStyle `json:"style"`
	NumberingText      string         `json:"numbering_text,omitempty"`
	SectionBreakXML    string         `json:"-"`
	SectionBreakLayout *SectionLayout `json:"section_break_layout,omitempty"`
}

type RunBreakType string

const (
	RunBreakTypeLine         RunBreakType = "line"
	RunBreakTypePage         RunBreakType = "page"
	RunBreakTypeRenderedPage RunBreakType = "renderedPage"
	RunBreakTypeTextWrapping RunBreakType = "textWrapping"
)

type Run struct {
	ID         string       `json:"id"`
	Text       string       `json:"text,omitempty"`
	BreakType  RunBreakType `json:"break_type,omitempty"`
	Style      TextStyle    `json:"style"`
	DrawingXML string       `json:"drawing_xml,omitempty"`
	Image      *RunImage    `json:"image,omitempty"`
}

type RunImage struct {
	RelationshipID string `json:"relationship_id,omitempty"`
	Target         string `json:"target,omitempty"`
	Filename       string `json:"filename,omitempty"`
	ContentType    string `json:"content_type,omitempty"`
	Name           string `json:"name,omitempty"`
	WidthEMU       int64  `json:"width_emu,omitempty"`
	HeightEMU      int64  `json:"height_emu,omitempty"`
	Inline         bool   `json:"inline,omitempty"`
}

type TextStyle struct {
	StyleID           string `json:"style_id,omitempty"`
	FontFamily        string `json:"font_family,omitempty"`
	ThemeFontASCII    string `json:"theme_font_ascii,omitempty"`
	ThemeFontEastAsia string `json:"theme_font_east_asia,omitempty"`
	FontSizeHP        int    `json:"font_size_hp,omitempty"`
	Bold              bool   `json:"bold,omitempty"`
	BoldSet           bool   `json:"bold_set,omitempty"`
	Italic            bool   `json:"italic,omitempty"`
	ItalicSet         bool   `json:"italic_set,omitempty"`
	Underline         bool   `json:"underline,omitempty"`
	UnderlineSet      bool   `json:"underline_set,omitempty"`
	Color             string `json:"color,omitempty"`
	ThemeColor        string `json:"theme_color,omitempty"`
}

type ParagraphStyle struct {
	StyleID            string        `json:"style_id,omitempty"`
	Alignment          string        `json:"alignment,omitempty"`
	SpacingBefore      int           `json:"spacing_before,omitempty"`
	SpacingAfter       int           `json:"spacing_after,omitempty"`
	LineSpacing        int           `json:"line_spacing,omitempty"`
	LineSpacingRule    string        `json:"line_spacing_rule,omitempty"`
	FirstLineIndent    int           `json:"first_line_indent,omitempty"`
	LeftIndent         int           `json:"left_indent,omitempty"`
	KeepNext           bool          `json:"keep_next,omitempty"`
	KeepNextSet        bool          `json:"keep_next_set,omitempty"`
	KeepLines          bool          `json:"keep_lines,omitempty"`
	KeepLinesSet       bool          `json:"keep_lines_set,omitempty"`
	PageBreakBefore    bool          `json:"page_break_before,omitempty"`
	PageBreakBeforeSet bool          `json:"page_break_before_set,omitempty"`
	WidowControl       bool          `json:"widow_control,omitempty"`
	WidowControlSet    bool          `json:"widow_control_set,omitempty"`
	Numbering          *NumberingRef `json:"numbering,omitempty"`
}

type NumberingRef struct {
	NumID string `json:"num_id"`
	Level int    `json:"level,omitempty"`
}

func (d *Document) Clone() *Document {
	if d == nil {
		return nil
	}

	cloned := &Document{
		ID:         d.ID,
		Name:       d.Name,
		SourcePath: d.SourcePath,
		Revision:   d.Revision,
		Meta:       cloneDocumentMeta(d.Meta),
	}
	if len(d.Blocks) > 0 {
		cloned.Blocks = cloneBlocks(d.Blocks)
		cloned.rebuildParagraphs()
		return cloned
	}

	cloned.Paragraphs = make([]Paragraph, len(d.Paragraphs))
	cloned.Blocks = make([]Block, 0, len(d.Paragraphs))
	for index, paragraph := range d.Paragraphs {
		clonedParagraph := paragraph.clone()
		cloned.Paragraphs[index] = clonedParagraph
		cloned.Blocks = append(cloned.Blocks, Block{Type: BlockTypeParagraph, Paragraph: &clonedParagraph})
	}

	return cloned
}

func (d *Document) ensureBlockStructure() {
	if d == nil || len(d.Blocks) > 0 {
		return
	}
	if len(d.Paragraphs) == 0 {
		return
	}
	d.Blocks = make([]Block, 0, len(d.Paragraphs))
	for _, paragraph := range d.Paragraphs {
		clonedParagraph := paragraph.clone()
		d.Blocks = append(d.Blocks, Block{Type: BlockTypeParagraph, Paragraph: &clonedParagraph})
	}
}

func (d *Document) rebuildParagraphs() {
	if d == nil {
		return
	}
	d.ensureBlockStructure()
	d.Paragraphs = flattenParagraphs(d.Blocks)
}

func (p Paragraph) clone() Paragraph {
	cloned := Paragraph{
		ID:              p.ID,
		Style:           cloneParagraphStyle(p.Style),
		NumberingText:   p.NumberingText,
		Runs:            make([]Run, len(p.Runs)),
		SectionBreakXML: p.SectionBreakXML,
	}
	if p.SectionBreakLayout != nil {
		layout := *p.SectionBreakLayout
		cloned.SectionBreakLayout = &layout
	}
	for index, run := range p.Runs {
		cloned.Runs[index] = cloneRun(run)
	}
	return cloned
}

func (p Paragraph) PlainText() string {
	var builder strings.Builder
	for _, run := range p.Runs {
		builder.WriteString(runVisibleText(run))
	}
	return builder.String()
}

func runVisibleText(run Run) string {
	switch run.BreakType {
	case RunBreakTypeLine, RunBreakTypeTextWrapping:
		return "\n"
	case RunBreakTypePage, RunBreakTypeRenderedPage:
		return "\f"
	default:
		return run.Text
	}
}

func cloneBlocks(blocks []Block) []Block {
	if len(blocks) == 0 {
		return nil
	}
	cloned := make([]Block, len(blocks))
	for index, block := range blocks {
		cloned[index] = block.clone()
	}
	return cloned
}

func (b Block) clone() Block {
	cloned := Block{Type: b.Type}
	if b.Paragraph != nil {
		paragraph := b.Paragraph.clone()
		cloned.Paragraph = &paragraph
	}
	if b.Table != nil {
		table := b.Table.clone()
		cloned.Table = &table
	}
	return cloned
}

func (t Table) clone() Table {
	cloned := Table{
		ID:            t.ID,
		PropertiesXML: t.PropertiesXML,
		GridXML:       t.GridXML,
		Rows:          make([]TableRow, len(t.Rows)),
	}
	for index, row := range t.Rows {
		cloned.Rows[index] = row.clone()
	}
	return cloned
}

func (r TableRow) clone() TableRow {
	cloned := TableRow{
		ID:              r.ID,
		PropertiesXML:   r.PropertiesXML,
		RepeatHeader:    r.RepeatHeader,
		RepeatHeaderSet: r.RepeatHeaderSet,
		CantSplit:       r.CantSplit,
		CantSplitSet:    r.CantSplitSet,
		Cells:           make([]TableCell, len(r.Cells)),
	}
	for index, cell := range r.Cells {
		cloned.Cells[index] = cell.clone()
	}
	return cloned
}

func (c TableCell) clone() TableCell {
	return TableCell{
		ID:            c.ID,
		PropertiesXML: c.PropertiesXML,
		Blocks:        cloneBlocks(c.Blocks),
	}
}

func (p HeaderFooterPart) clone() HeaderFooterPart {
	cloned := HeaderFooterPart{
		Kind:           p.Kind,
		Variant:        p.Variant,
		RelationshipID: p.RelationshipID,
		Target:         p.Target,
		Blocks:         cloneBlocks(p.Blocks),
	}
	cloned.rebuildParagraphs()
	return cloned
}

func (p *HeaderFooterPart) rebuildParagraphs() {
	if p == nil {
		return
	}
	p.Paragraphs = flattenParagraphs(p.Blocks)
}

func flattenParagraphs(blocks []Block) []Paragraph {
	if len(blocks) == 0 {
		return nil
	}
	paragraphs := make([]Paragraph, 0)
	appendParagraphsFromBlocks(&paragraphs, blocks)
	return paragraphs
}

func appendParagraphsFromBlocks(dst *[]Paragraph, blocks []Block) {
	for _, block := range blocks {
		switch block.Type {
		case BlockTypeParagraph:
			if block.Paragraph != nil {
				*dst = append(*dst, block.Paragraph.clone())
			}
		case BlockTypeTable:
			if block.Table == nil {
				continue
			}
			for _, row := range block.Table.Rows {
				for _, cell := range row.Cells {
					appendParagraphsFromBlocks(dst, cell.Blocks)
				}
			}
		}
	}
}

func cloneRun(run Run) Run {
	cloned := run
	if run.Image != nil {
		image := *run.Image
		cloned.Image = &image
	}
	return cloned
}

func cloneDocumentMeta(meta DocumentMeta) DocumentMeta {
	cloned := meta
	if len(meta.HeaderFooters) > 0 {
		cloned.HeaderFooters = make([]HeaderFooterPart, len(meta.HeaderFooters))
		for index, part := range meta.HeaderFooters {
			cloned.HeaderFooters[index] = part.clone()
		}
	}
	if len(meta.Sections) > 0 {
		cloned.Sections = make([]DocumentSection, len(meta.Sections))
		for index, section := range meta.Sections {
			clonedSection := DocumentSection{
				StartBlockIndex: section.StartBlockIndex,
				EndBlockIndex:   section.EndBlockIndex,
				Layout:          section.Layout,
			}
			if len(section.HeaderFooters) > 0 {
				clonedSection.HeaderFooters = make([]HeaderFooterPart, len(section.HeaderFooters))
				for partIndex, part := range section.HeaderFooters {
					clonedSection.HeaderFooters[partIndex] = part.clone()
				}
			}
			cloned.Sections[index] = clonedSection
		}
	}
	if meta.DefaultParagraphStyle != nil {
		paragraphStyle := cloneParagraphStyle(*meta.DefaultParagraphStyle)
		cloned.DefaultParagraphStyle = &paragraphStyle
	}
	if meta.ObjectSummary != nil {
		summary := *meta.ObjectSummary
		cloned.ObjectSummary = &summary
	}
	if meta.DefaultTextStyle != nil {
		textStyle := *meta.DefaultTextStyle
		cloned.DefaultTextStyle = &textStyle
	}
	if meta.Theme != nil {
		theme := &DocumentTheme{}
		if len(meta.Theme.Colors) > 0 {
			theme.Colors = make(map[string]string, len(meta.Theme.Colors))
			for key, value := range meta.Theme.Colors {
				theme.Colors[key] = value
			}
		}
		if len(meta.Theme.Fonts) > 0 {
			theme.Fonts = make(map[string]string, len(meta.Theme.Fonts))
			for key, value := range meta.Theme.Fonts {
				theme.Fonts[key] = value
			}
		}
		cloned.Theme = theme
	}
	if len(meta.Styles) > 0 {
		cloned.Styles = make([]DocumentStyle, len(meta.Styles))
		for index, style := range meta.Styles {
			cloned.Styles[index] = DocumentStyle{
				ID:        style.ID,
				Name:      style.Name,
				Type:      style.Type,
				BasedOn:   style.BasedOn,
				IsDefault: style.IsDefault,
				Paragraph: cloneParagraphStyle(style.Paragraph),
				Text:      style.Text,
			}
		}
	}
	if len(meta.Numbering) > 0 {
		cloned.Numbering = make([]NumberingDefinition, len(meta.Numbering))
		for index, numbering := range meta.Numbering {
			clonedNumbering := NumberingDefinition{
				NumID:      numbering.NumID,
				AbstractID: numbering.AbstractID,
			}
			if len(numbering.Levels) > 0 {
				clonedNumbering.Levels = make([]NumberingLevel, len(numbering.Levels))
				for levelIndex, level := range numbering.Levels {
					clonedNumbering.Levels[levelIndex] = NumberingLevel{
						Level:     level.Level,
						Start:     level.Start,
						Format:    level.Format,
						Text:      level.Text,
						Paragraph: cloneParagraphStyle(level.Paragraph),
						TextStyle: level.TextStyle,
					}
				}
			}
			cloned.Numbering[index] = clonedNumbering
		}
	}
	return cloned
}

func cloneParagraphStyle(style ParagraphStyle) ParagraphStyle {
	cloned := style
	if style.Numbering != nil {
		copyValue := *style.Numbering
		cloned.Numbering = &copyValue
	}
	return cloned
}
