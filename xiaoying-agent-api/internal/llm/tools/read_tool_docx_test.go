package tools

import (
	"archive/zip"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auramate/auramate/internal/goword"
)

func TestReadToolReadsDOCXAsStructuredText(t *testing.T) {
	docPath := createTestDOCX(t, map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>第一段</w:t></w:r></w:p><w:tbl><w:tr><w:tc><w:p><w:r><w:t>项目</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>金额</w:t></w:r></w:p></w:tc></w:tr><w:tr><w:tc><w:p><w:r><w:t>总计</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>1000</w:t></w:r></w:p></w:tc></w:tr></w:tbl><w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`,
	})

	input, err := json.Marshal(ReadParams{FilePath: docPath, Limit: 20})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	response, err := NewReadTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run read tool: %v", err)
	}
	if response.IsError {
		t.Fatalf("expected successful response, got error: %s", response.Content)
	}
	if !strings.Contains(response.Content, "第一段") {
		t.Fatalf("expected paragraph text in response, got %s", response.Content)
	}
	if !strings.Contains(response.Content, "[table tbl_") {
		t.Fatalf("expected table marker in response, got %s", response.Content)
	}
	if !strings.Contains(response.Content, "项目 | 金额") {
		t.Fatalf("expected table header row in response, got %s", response.Content)
	}
	if !strings.Contains(response.Content, "总计 | 1000") {
		t.Fatalf("expected table body row in response, got %s", response.Content)
	}
}

func TestReadToolSearchTermWorksOnDOCXContent(t *testing.T) {
	docPath := createTestDOCX(t, map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>封面说明</w:t></w:r></w:p><w:p><w:r><w:t>执行摘要</w:t></w:r></w:p><w:tbl><w:tr><w:tc><w:p><w:r><w:t>风险项</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>需复核</w:t></w:r></w:p></w:tc></w:tr></w:tbl><w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`,
	})

	input, err := json.Marshal(ReadParams{FilePath: docPath, Limit: 5, SearchTerm: "风险项"})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	response, err := NewReadTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run read tool: %v", err)
	}
	if response.IsError {
		t.Fatalf("expected successful response, got error: %s", response.Content)
	}
	if strings.Contains(response.Content, "封面说明") {
		t.Fatalf("expected response to start from matched content, got %s", response.Content)
	}
	if !strings.Contains(response.Content, "风险项 | 需复核") {
		t.Fatalf("expected matched table row in response, got %s", response.Content)
	}
	if !strings.Contains(response.Content, "<word_locator_hint>") {
		t.Fatalf("expected word locator hint in response, got %s", response.Content)
	}
	if !strings.Contains(response.Content, "auramate-word-reader") {
		t.Fatalf("expected handoff tool name in response, got %s", response.Content)
	}
}

func TestReadToolDoesNotAppendWordLocatorHintForSkeleton(t *testing.T) {
	docPath := createTestDOCX(t, map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>正文提要</w:t></w:r></w:p><w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`,
	})

	input, err := json.Marshal(ReadParams{FilePath: docPath, Limit: 20, Mode: "skeleton"})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	response, err := NewReadTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run read tool: %v", err)
	}
	if response.IsError {
		t.Fatalf("expected successful response, got error: %s", response.Content)
	}
	if strings.Contains(response.Content, "<word_locator_hint>") {
		t.Fatalf("did not expect word locator hint in skeleton response, got %s", response.Content)
	}
}

func TestReadToolPrefersGoWordSessionSnapshotForDOCX(t *testing.T) {
	docPath := createTestDOCX(t, map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>磁盘原文</w:t></w:r></w:p><w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`,
	})

	service := goword.NewService(t.TempDir())
	SetReadToolGoWordService(service)
	t.Cleanup(func() {
		SetReadToolGoWordService(nil)
	})

	session, err := service.CreateSession(&goword.Document{
		Name:       filepath.Base(docPath),
		SourcePath: docPath,
		Revision:   2,
		Blocks: []goword.Block{
			{
				Type: goword.BlockTypeParagraph,
				Paragraph: &goword.Paragraph{
					ID:   "p1",
					Runs: []goword.Run{{ID: "r1", Text: "会话新内容"}},
				},
			},
		},
		Paragraphs: []goword.Paragraph{{ID: "p1", Runs: []goword.Run{{ID: "r1", Text: "会话新内容"}}}},
		Meta:       goword.DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	input, err := json.Marshal(ReadParams{SessionID: session.ID, Limit: 20})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	response, err := NewReadTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run read tool: %v", err)
	}
	if response.IsError {
		t.Fatalf("expected successful response, got error: %s", response.Content)
	}
	if !strings.Contains(response.Content, "会话新内容") {
		t.Fatalf("expected session snapshot content in response, got %s", response.Content)
	}
	if strings.Contains(response.Content, "磁盘原文") {
		t.Fatalf("expected session snapshot to take priority over disk content, got %s", response.Content)
	}
}

func TestReadToolReadsDOCXHeaderFooterContent(t *testing.T) {
	docPath := createTestDOCX(t, map[string]string{
		"[Content_Types].xml":          `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/header1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.header+xml"/><Override PartName="/word/footer1.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.footer+xml"/></Types>`,
		"_rels/.rels":                  `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/_rels/document.xml.rels": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rIdHeaderDefault" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/header" Target="header1.xml"/><Relationship Id="rIdFooterDefault" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/footer" Target="footer1.xml"/></Relationships>`,
		"word/document.xml":            `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body><w:p><w:r><w:t>正文首段</w:t></w:r></w:p><w:sectPr><w:headerReference w:type="default" r:id="rIdHeaderDefault"/><w:footerReference w:type="default" r:id="rIdFooterDefault"/><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`,
		"word/header1.xml":             `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:hdr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:r><w:t>默认页眉文本</w:t></w:r></w:p></w:hdr>`,
		"word/footer1.xml":             `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:ftr xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:p><w:r><w:t>默认页脚文本</w:t></w:r></w:p></w:ftr>`,
	})

	input, err := json.Marshal(ReadParams{FilePath: docPath, Limit: 20})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	response, err := NewReadTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run read tool: %v", err)
	}
	if response.IsError {
		t.Fatalf("expected successful response, got error: %s", response.Content)
	}
	if !strings.Contains(response.Content, "正文首段") {
		t.Fatalf("expected body content in response, got %s", response.Content)
	}
	if !strings.Contains(response.Content, "[header default]") {
		t.Fatalf("expected header marker in response, got %s", response.Content)
	}
	if !strings.Contains(response.Content, "默认页眉文本") {
		t.Fatalf("expected header text in response, got %s", response.Content)
	}
	if !strings.Contains(response.Content, "[footer default]") {
		t.Fatalf("expected footer marker in response, got %s", response.Content)
	}
	if !strings.Contains(response.Content, "默认页脚文本") {
		t.Fatalf("expected footer text in response, got %s", response.Content)
	}
}

func TestReadToolSkeletonIncludesSessionHeaderFooterSummary(t *testing.T) {
	service := goword.NewService(t.TempDir())
	SetReadToolGoWordService(service)
	t.Cleanup(func() {
		SetReadToolGoWordService(nil)
	})

	session, err := service.CreateSession(&goword.Document{
		Name:       "header-footer.docx",
		SourcePath: "header-footer.docx",
		Revision:   3,
		Blocks: []goword.Block{
			{
				Type: goword.BlockTypeParagraph,
				Paragraph: &goword.Paragraph{
					ID:    "p-body",
					Style: goword.ParagraphStyle{StyleID: "Normal"},
					Runs:  []goword.Run{{ID: "r-body", Text: "正文内容"}},
				},
			},
		},
		Paragraphs: []goword.Paragraph{{ID: "p-body", Style: goword.ParagraphStyle{StyleID: "Normal"}, Runs: []goword.Run{{ID: "r-body", Text: "正文内容"}}}},
		Meta: goword.DocumentMeta{
			Format: "docx",
			HeaderFooters: []goword.HeaderFooterPart{
				{
					Kind:    "header",
					Variant: "default",
					Blocks: []goword.Block{{
						Type: goword.BlockTypeParagraph,
						Paragraph: &goword.Paragraph{
							ID:    "p-header",
							Style: goword.ParagraphStyle{StyleID: "Header"},
							Runs:  []goword.Run{{ID: "r-header", Text: "会话页眉"}},
						},
					}},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	input, err := json.Marshal(ReadParams{SessionID: session.ID, Limit: 20, Mode: "skeleton"})
	if err != nil {
		t.Fatalf("marshal params: %v", err)
	}

	response, err := NewReadTool().Run(context.Background(), ToolCall{Input: string(input)})
	if err != nil {
		t.Fatalf("run read tool: %v", err)
	}
	if response.IsError {
		t.Fatalf("expected successful response, got error: %s", response.Content)
	}
	if !strings.Contains(response.Content, "paragraph style=Normal text=正文内容") {
		t.Fatalf("expected body skeleton in response, got %s", response.Content)
	}
	if !strings.Contains(response.Content, "header default paragraph style=Header text=会话页眉") {
		t.Fatalf("expected header skeleton summary in response, got %s", response.Content)
	}
}

func createTestDOCX(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "sample.docx")
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create docx: %v", err)
	}
	defer file.Close()

	zipWriter := zip.NewWriter(file)
	for name, content := range files {
		writer, err := zipWriter.Create(name)
		if err != nil {
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
	return path
}
