package goword

import "time"

const (
	defaultLayoutPageWidthTwips    = 11906
	defaultLayoutPageHeightTwips   = 16838
	defaultLayoutMarginTopTwips    = 1440
	defaultLayoutMarginRightTwips  = 1440
	defaultLayoutMarginBottomTwips = 1440
	defaultLayoutMarginLeftTwips   = 1440
	defaultLayoutHeaderTwips       = 720
	defaultLayoutFooterTwips       = 720
)

type LayoutOptions struct {
	Page         LayoutPageSpec `json:"page,omitempty"`
	StartPage    int            `json:"start_page,omitempty"`
	IncludeDebug bool           `json:"include_debug,omitempty"`
}

type LayoutPageSpec struct {
	WidthTwips          int `json:"width_twips,omitempty"`
	HeightTwips         int `json:"height_twips,omitempty"`
	MarginTopTwips      int `json:"margin_top_twips,omitempty"`
	MarginRightTwips    int `json:"margin_right_twips,omitempty"`
	MarginBottomTwips   int `json:"margin_bottom_twips,omitempty"`
	MarginLeftTwips     int `json:"margin_left_twips,omitempty"`
	HeaderDistanceTwips int `json:"header_distance_twips,omitempty"`
	FooterDistanceTwips int `json:"footer_distance_twips,omitempty"`
}

type LayoutEngineInfo struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

type DocumentLayout struct {
	SessionID   string           `json:"session_id,omitempty"`
	DocumentID  string           `json:"document_id,omitempty"`
	Revision    int64            `json:"revision,omitempty"`
	GeneratedAt time.Time        `json:"generated_at"`
	PageSpec    LayoutPageSpec   `json:"page_spec"`
	Engine      LayoutEngineInfo `json:"engine"`
	Pages       []LayoutPage     `json:"pages,omitempty"`
	Warnings    []string         `json:"warnings,omitempty"`
}

type LayoutPage struct {
	Index              int           `json:"index"`
	Number             int           `json:"number"`
	WidthTwips         int           `json:"width_twips"`
	HeightTwips        int           `json:"height_twips"`
	ContentTopTwips    int           `json:"content_top_twips"`
	ContentBottomTwips int           `json:"content_bottom_twips"`
	Blocks             []LayoutBlock `json:"blocks,omitempty"`
}

type LayoutBlock struct {
	ID               string           `json:"id,omitempty"`
	Type             BlockType        `json:"type"`
	SourceBlockIndex int              `json:"source_block_index"`
	TopTwips         int              `json:"top_twips,omitempty"`
	HeightTwips      int              `json:"height_twips,omitempty"`
	Paragraph        *ParagraphLayout `json:"paragraph,omitempty"`
	Table            *TableLayout     `json:"table,omitempty"`
}

type ParagraphLayout struct {
	ParagraphID string       `json:"paragraph_id,omitempty"`
	LineCount   int          `json:"line_count,omitempty"`
	Lines       []LineLayout `json:"lines,omitempty"`
}

type LineLayout struct {
	Index       int         `json:"index"`
	Text        string      `json:"text,omitempty"`
	TopTwips    int         `json:"top_twips,omitempty"`
	HeightTwips int         `json:"height_twips,omitempty"`
	Runs        []RunLayout `json:"runs,omitempty"`
}

type RunLayout struct {
	RunID      string       `json:"run_id,omitempty"`
	Text       string       `json:"text,omitempty"`
	BreakType  RunBreakType `json:"break_type,omitempty"`
	WidthTwips int          `json:"width_twips,omitempty"`
	StartTwips int          `json:"start_twips,omitempty"`
	HasDrawing bool         `json:"has_drawing,omitempty"`
}

type TableLayout struct {
	TableID  string           `json:"table_id,omitempty"`
	RowCount int              `json:"row_count,omitempty"`
	Rows     []TableRowLayout `json:"rows,omitempty"`
}

type TableRowLayout struct {
	RowID        string            `json:"row_id,omitempty"`
	TopTwips     int               `json:"top_twips,omitempty"`
	HeightTwips  int               `json:"height_twips,omitempty"`
	RepeatHeader bool              `json:"repeat_header,omitempty"`
	CantSplit    bool              `json:"cant_split,omitempty"`
	Cells        []TableCellLayout `json:"cells,omitempty"`
}

type TableCellLayout struct {
	CellID     string            `json:"cell_id,omitempty"`
	Paragraphs []ParagraphLayout `json:"paragraphs,omitempty"`
	BlockCount int               `json:"block_count,omitempty"`
}

func (options LayoutOptions) normalized(defaults SectionLayout) LayoutOptions {
	normalized := options
	if normalized.StartPage <= 0 {
		normalized.StartPage = 1
	}
	normalized.Page = normalizeLayoutPageSpec(normalized.Page, defaults)
	return normalized
}

func normalizeLayoutPageSpec(spec LayoutPageSpec, defaults SectionLayout) LayoutPageSpec {
	if spec.WidthTwips == 0 {
		spec.WidthTwips = defaults.PageWidth
	}
	if spec.HeightTwips == 0 {
		spec.HeightTwips = defaults.PageHeight
	}
	if spec.MarginTopTwips == 0 {
		spec.MarginTopTwips = defaults.MarginTop
	}
	if spec.MarginRightTwips == 0 {
		spec.MarginRightTwips = defaults.MarginRight
	}
	if spec.MarginBottomTwips == 0 {
		spec.MarginBottomTwips = defaults.MarginBottom
	}
	if spec.MarginLeftTwips == 0 {
		spec.MarginLeftTwips = defaults.MarginLeft
	}
	if spec.HeaderDistanceTwips == 0 {
		spec.HeaderDistanceTwips = defaults.MarginHeader
	}
	if spec.FooterDistanceTwips == 0 {
		spec.FooterDistanceTwips = defaults.MarginFooter
	}

	if spec.WidthTwips == 0 {
		spec.WidthTwips = defaultLayoutPageWidthTwips
	}
	if spec.HeightTwips == 0 {
		spec.HeightTwips = defaultLayoutPageHeightTwips
	}
	if spec.MarginTopTwips == 0 {
		spec.MarginTopTwips = defaultLayoutMarginTopTwips
	}
	if spec.MarginRightTwips == 0 {
		spec.MarginRightTwips = defaultLayoutMarginRightTwips
	}
	if spec.MarginBottomTwips == 0 {
		spec.MarginBottomTwips = defaultLayoutMarginBottomTwips
	}
	if spec.MarginLeftTwips == 0 {
		spec.MarginLeftTwips = defaultLayoutMarginLeftTwips
	}
	if spec.HeaderDistanceTwips == 0 {
		spec.HeaderDistanceTwips = defaultLayoutHeaderTwips
	}
	if spec.FooterDistanceTwips == 0 {
		spec.FooterDistanceTwips = defaultLayoutFooterTwips
	}

	return spec
}
