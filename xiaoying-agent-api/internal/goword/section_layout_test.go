package goword

import "testing"

func TestParseSectionLayout(t *testing.T) {
	layout := parseSectionLayout(`<w:titlePg/><w:pgNumType w:start="5" w:fmt="upperRoman"/><w:pgSz w:w="16838" w:h="11906" w:orient="landscape"/><w:pgMar w:top="1440" w:right="1200" w:bottom="960" w:left="1200" w:header="480" w:footer="360" w:gutter="0"/>`)

	if layout.PageWidth != 16838 || layout.PageHeight != 11906 {
		t.Fatalf("unexpected page size: %+v", layout)
	}
	if layout.Orientation != "landscape" {
		t.Fatalf("expected landscape orientation, got %+v", layout)
	}
	if layout.MarginTop != 1440 || layout.MarginRight != 1200 || layout.MarginBottom != 960 || layout.MarginLeft != 1200 {
		t.Fatalf("unexpected page margins: %+v", layout)
	}
	if layout.MarginHeader != 480 || layout.MarginFooter != 360 || layout.MarginGutter != 0 {
		t.Fatalf("unexpected header/footer margins: %+v", layout)
	}
	if !layout.TitlePageSet || !layout.TitlePage {
		t.Fatalf("expected title page flag, got %+v", layout)
	}
	if layout.PageNumStart != 5 || layout.PageNumFmt != "upperRoman" {
		t.Fatalf("unexpected page number settings: %+v", layout)
	}
}
