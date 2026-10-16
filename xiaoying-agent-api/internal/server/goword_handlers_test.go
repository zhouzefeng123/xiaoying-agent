package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auramate/auramate/internal/goword"
)

type goWordOpenResponse struct {
	ID       string          `json:"id"`
	Document goword.Document `json:"document"`
}

type goWordCreateResponse struct {
	ID       string          `json:"id"`
	Document goword.Document `json:"document"`
}

type goWordApplyResponse struct {
	Document goword.Document `json:"document"`
}

type goWordSaveResponse struct {
	Path string `json:"path"`
}

type goWordSearchResponse struct {
	Query string `json:"query"`
	Hits  []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"hits"`
}

type goWordLayoutResponse struct {
	SessionID string `json:"session_id"`
	Engine    struct {
		Name string `json:"name"`
	} `json:"engine"`
	Pages []struct {
		Blocks []struct {
			Type      goword.BlockType `json:"type"`
			Paragraph *struct {
				LineCount int `json:"line_count"`
			} `json:"paragraph"`
			Table *struct {
				RowCount int `json:"row_count"`
			} `json:"table"`
		} `json:"blocks"`
	} `json:"pages"`
}

type goWordIRResponse struct {
	DocumentID string `json:"document_id"`
	Revision   int64  `json:"revision"`
	Nodes      []struct {
		NodeID             string   `json:"node_id"`
		Type               string   `json:"type"`
		ParagraphID        string   `json:"paragraph_id"`
		ParentNodeID       string   `json:"parent_node_id"`
		ChildNodeIDs       []string `json:"child_node_ids"`
		StyleSignature     string   `json:"style_signature"`
		NumberingSignature string   `json:"numbering_signature"`
		SectionSignature   string   `json:"section_signature"`
		CharRange          *struct {
			Start int `json:"start"`
			End   int `json:"end"`
		} `json:"char_range"`
	} `json:"nodes"`
}

type goWordIRQueryResponse struct {
	DocumentID string `json:"document_id"`
	Revision   int64  `json:"revision"`
	MatchCount int    `json:"match_count"`
	Nodes      []struct {
		NodeID       string `json:"node_id"`
		Type         string `json:"type"`
		ParagraphID  string `json:"paragraph_id"`
		ParentNodeID string `json:"parent_node_id"`
		TableID      string `json:"table_id"`
	} `json:"nodes"`
}

type goWordPlanResponse struct {
	Status           string `json:"status"`
	StatusText       string `json:"status_text"`
	ExpectedRevision int64  `json:"expected_revision"`
	ReplayAction     struct {
		PreferredParagraphID string `json:"preferred_paragraph_id"`
		TableID              string `json:"table_id"`
		RowIndex             int    `json:"row_index"`
		CellIndex            int    `json:"cell_index"`
		TargetSelector       *struct {
			Type         string `json:"type"`
			ParagraphID  string `json:"paragraph_id"`
			TableID      string `json:"table_id"`
			RowIndex     int    `json:"row_index"`
			CellIndex    int    `json:"cell_index"`
			HeadingLevel int    `json:"heading_level"`
			Ordinal      int    `json:"ordinal"`
			Chapter      string `json:"chapter"`
			TextEquals   string `json:"text_equals"`
			Region       string `json:"region"`
		} `json:"target_selector"`
	} `json:"replay_action"`
	Diagnostics struct {
		ErrorCode      string `json:"error_code"`
		CandidateCount int    `json:"candidate_count"`
		Candidates     []struct {
			ParagraphID string `json:"paragraph_id"`
			TableID     string `json:"table_id"`
			RowIndex    int    `json:"row_index"`
			CellIndex   int    `json:"cell_index"`
		} `json:"candidates"`
	} `json:"diagnostics"`
}

type goWordExecutePlanResponse struct {
	Status     string          `json:"status"`
	StatusText string          `json:"status_text"`
	Document   goword.Document `json:"document"`
	Validation struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	} `json:"validation"`
	ReplayAction struct {
		PreferredParagraphID string `json:"preferred_paragraph_id"`
	} `json:"replay_action"`
	Diagnostics struct {
		ErrorCode string `json:"error_code"`
	} `json:"diagnostics"`
}

type goWordCheckpointResponse struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Revision int64  `json:"revision"`
}

type goWordCheckpointListResponse struct {
	Checkpoints []goword.SessionCheckpoint `json:"checkpoints"`
}

type goWordRestoreCheckpointResponse struct {
	Document goword.Document `json:"document"`
}

type goWordBatchResponse struct {
	ID              string `json:"id"`
	Status          string `json:"status"`
	InitialRevision int64  `json:"initial_revision"`
	PlannedRevision int64  `json:"planned_revision"`
	CheckpointID    string `json:"checkpoint_id"`
	FailureStep     int    `json:"failure_step"`
	RecoveryPlan    struct {
		PrimaryStrategy string `json:"primary_strategy"`
	} `json:"recovery_plan"`
	Results []struct {
		Index      int    `json:"index"`
		Status     string `json:"status"`
		StatusText string `json:"status_text"`
	} `json:"results"`
}

func TestGoWordHandlersOpenApplySaveCloseFlow(t *testing.T) {
	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "integration.docx")

	seed := &goword.Document{
		ID:       "doc_integration",
		Name:     "integration.docx",
		Revision: 1,
		Paragraphs: []goword.Paragraph{
			{ID: "p1", Runs: []goword.Run{{ID: "r1", Text: "原始内容"}}},
		},
		Meta: goword.DocumentMeta{Format: "docx"},
	}
	if err := goword.SaveDOCX(seed, inputPath); err != nil {
		t.Fatalf("prepare input docx: %v", err)
	}

	s := &Server{goWord: goword.NewService(tempDir)}

	openReqBody := map[string]string{"path": inputPath}
	openRecorder := httptest.NewRecorder()
	s.handleGoWordOpen(openRecorder, newJSONRequest(t, http.MethodPost, "/v1/goword/open", openReqBody))
	if openRecorder.Code != http.StatusOK {
		t.Fatalf("open status = %d, body=%s", openRecorder.Code, openRecorder.Body.String())
	}

	var openResp goWordOpenResponse
	if err := json.Unmarshal(openRecorder.Body.Bytes(), &openResp); err != nil {
		t.Fatalf("decode open response: %v", err)
	}
	if openResp.ID == "" {
		t.Fatalf("open response missing session id")
	}
	if len(openResp.Document.Paragraphs) == 0 {
		t.Fatalf("open response missing paragraphs")
	}

	paragraphID := openResp.Document.Paragraphs[0].ID
	applyReqBody := map[string]any{
		"session_id":        openResp.ID,
		"expected_revision": openResp.Document.Revision,
		"operations": []map[string]any{
			{
				"type":         "replace_text",
				"paragraph_id": paragraphID,
				"start_offset": 0,
				"end_offset":   len([]rune("原始内容")),
				"before_text":  "原始内容",
				"text":         "已编辑内容",
			},
		},
	}
	applyRecorder := httptest.NewRecorder()
	s.handleGoWordApply(applyRecorder, newJSONRequest(t, http.MethodPost, "/v1/goword/apply", applyReqBody))
	if applyRecorder.Code != http.StatusOK {
		t.Fatalf("apply status = %d, body=%s", applyRecorder.Code, applyRecorder.Body.String())
	}

	var applyResp goWordApplyResponse
	if err := json.Unmarshal(applyRecorder.Body.Bytes(), &applyResp); err != nil {
		t.Fatalf("decode apply response: %v", err)
	}
	if got := applyResp.Document.Paragraphs[0].PlainText(); got != "已编辑内容" {
		t.Fatalf("apply result text mismatch: got %q", got)
	}

	saveRecorder := httptest.NewRecorder()
	s.handleGoWordSave(saveRecorder, newJSONRequest(t, http.MethodPost, "/v1/goword/save", map[string]string{"session_id": openResp.ID, "path": ""}))
	if saveRecorder.Code != http.StatusOK {
		t.Fatalf("save status = %d, body=%s", saveRecorder.Code, saveRecorder.Body.String())
	}

	var saveResp goWordSaveResponse
	if err := json.Unmarshal(saveRecorder.Body.Bytes(), &saveResp); err != nil {
		t.Fatalf("decode save response: %v", err)
	}
	if saveResp.Path == "" {
		t.Fatalf("save response path empty")
	}

	reopened, err := goword.OpenDOCX(saveResp.Path)
	if err != nil {
		t.Fatalf("reopen saved docx: %v", err)
	}
	if got := reopened.Paragraphs[0].PlainText(); got != "已编辑内容" {
		t.Fatalf("saved doc text mismatch: got %q", got)
	}

	closeRecorder := httptest.NewRecorder()
	s.handleGoWordClose(closeRecorder, newJSONRequest(t, http.MethodPost, "/v1/goword/close", map[string]string{"session_id": openResp.ID}))
	if closeRecorder.Code != http.StatusOK {
		t.Fatalf("close status = %d, body=%s", closeRecorder.Code, closeRecorder.Body.String())
	}
}

func TestGoWordHandlerCreateCanStartBlankDocumentSessionAndSave(t *testing.T) {
	tempDir := t.TempDir()
	s := &Server{goWord: goword.NewService(tempDir)}

	createRecorder := httptest.NewRecorder()
	s.handleGoWordCreate(createRecorder, newJSONRequest(t, http.MethodPost, "/v1/goword/create", map[string]any{
		"name": "created-api.docx",
		"blocks": []map[string]any{
			{"type": "paragraph", "paragraph": map[string]any{"text": "项目方案"}},
			{"type": "table", "table": map[string]any{"rows": []map[string]any{{"cells": []map[string]any{{"paragraphs": []map[string]any{{"text": "项目"}}}, {"paragraphs": []map[string]any{{"text": "金额"}}}}}}}},
		},
		"header_footers": []map[string]any{{"kind": "header", "blocks": []map[string]any{{"type": "paragraph", "paragraph": map[string]any{"text": "Auramate"}}}}},
		"section_layout": map[string]any{"orientation": "landscape"},
	}))
	if createRecorder.Code != http.StatusOK {
		t.Fatalf("create status = %d, body=%s", createRecorder.Code, createRecorder.Body.String())
	}

	var createResp goWordCreateResponse
	if err := json.Unmarshal(createRecorder.Body.Bytes(), &createResp); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	if createResp.ID == "" || len(createResp.Document.Blocks) < 2 {
		t.Fatalf("unexpected create response: %+v", createResp)
	}

	savePath := filepath.Join(tempDir, "created-api.docx")
	saveRecorder := httptest.NewRecorder()
	s.handleGoWordSave(saveRecorder, newJSONRequest(t, http.MethodPost, "/v1/goword/save", map[string]string{"session_id": createResp.ID, "path": savePath}))
	if saveRecorder.Code != http.StatusOK {
		t.Fatalf("save status = %d, body=%s", saveRecorder.Code, saveRecorder.Body.String())
	}

	assertZipEntryContains(t, savePath, "word/document.xml", "项目方案")
	assertZipEntryContains(t, savePath, "word/header-default.xml", "Auramate")
}

func assertZipEntryContains(t *testing.T, path, entryName, expected string) {
	t.Helper()
	archive, err := zip.OpenReader(path)
	if err != nil {
		t.Fatalf("open zip %s: %v", path, err)
	}
	defer archive.Close()
	for _, file := range archive.File {
		if file.Name != entryName {
			continue
		}
		reader, err := file.Open()
		if err != nil {
			t.Fatalf("open entry %s: %v", entryName, err)
		}
		defer reader.Close()
		data, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read entry %s: %v", entryName, err)
		}
		if !strings.Contains(string(data), expected) {
			t.Fatalf("entry %s missing %q", entryName, expected)
		}
		return
	}
	t.Fatalf("entry %s not found in %s", entryName, path)
}

func TestGoWordHandlerImageFlow(t *testing.T) {
	tempDir := t.TempDir()
	docPath := filepath.Join(tempDir, "image-handler.docx")
	parts := map[string][]byte{
		"[Content_Types].xml":          []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Default Extension="png" ContentType="image/png"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`),
		"_rels/.rels":                  []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`),
		"word/_rels/document.xml.rels": []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rIdImage1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/></Relationships>`),
		"word/document.xml":            []byte(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><w:body><w:p><w:r><w:drawing><wp:inline xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"><a:graphic xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main"><a:graphicData uri="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:pic xmlns:pic="http://schemas.openxmlformats.org/drawingml/2006/picture"><pic:blipFill><a:blip r:embed="rIdImage1"/></pic:blipFill></pic:pic></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p><w:sectPr><w:pgSz w:w="11906" w:h="16838"/></w:sectPr></w:body></w:document>`),
		"word/media/image1.png":        testMinimalPNG(),
	}
	createDOCXForServerTest(t, docPath, parts)

	s := &Server{goWord: goword.NewService(tempDir)}
	openRecorder := httptest.NewRecorder()
	s.handleGoWordOpen(openRecorder, newJSONRequest(t, http.MethodPost, "/v1/goword/open", map[string]string{"path": docPath}))
	if openRecorder.Code != http.StatusOK {
		t.Fatalf("open status = %d, body=%s", openRecorder.Code, openRecorder.Body.String())
	}

	var openResp goWordOpenResponse
	if err := json.Unmarshal(openRecorder.Body.Bytes(), &openResp); err != nil {
		t.Fatalf("decode open response: %v", err)
	}

	imageReq := httptest.NewRequest(http.MethodGet, "/v1/goword/image?session_id="+openResp.ID+"&relationship_id=rIdImage1", nil)
	imageRecorder := httptest.NewRecorder()
	s.handleGoWordImage(imageRecorder, imageReq)
	if imageRecorder.Code != http.StatusOK {
		t.Fatalf("image status = %d, body=%s", imageRecorder.Code, imageRecorder.Body.String())
	}
	if got := imageRecorder.Header().Get("Content-Type"); got != "image/png" {
		t.Fatalf("unexpected image content-type: %s", got)
	}
	if !bytes.Equal(imageRecorder.Body.Bytes(), testMinimalPNG()) {
		t.Fatalf("unexpected image response bytes")
	}
}

func TestGoWordHandlerSearchFlow(t *testing.T) {
	service := goword.NewService(t.TempDir())
	session, err := service.CreateSession(&goword.Document{
		ID:       "doc_search_handler",
		Name:     "search-handler.docx",
		Revision: 1,
		Blocks: []goword.Block{
			{Type: goword.BlockTypeParagraph, Paragraph: &goword.Paragraph{ID: "p1", Runs: []goword.Run{{ID: "r1", Text: "第一段内容"}}}},
		},
		Meta: goword.DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create search session: %v", err)
	}

	s := &Server{goWord: service}
	recorder := httptest.NewRecorder()
	s.handleGoWordSearch(recorder, newJSONRequest(t, http.MethodPost, "/v1/goword/search", map[string]any{
		"session_id": session.ID,
		"query":      "第一段",
		"scope":      "paragraph",
	}))

	if recorder.Code != http.StatusOK {
		t.Fatalf("search status = %d, body=%s", recorder.Code, recorder.Body.String())
	}

	var response goWordSearchResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode search response: %v", err)
	}
	if response.Query != "第一段" {
		t.Fatalf("unexpected search query: %s", response.Query)
	}
	if len(response.Hits) == 0 || response.Hits[0].Type != "paragraph" {
		t.Fatalf("expected paragraph hit, got %+v", response.Hits)
	}
}

func TestGoWordHandlerRecoverSessionFromSnapshotOnSave(t *testing.T) {
	tempDir := t.TempDir()
	inputPath := filepath.Join(tempDir, "recover-save.docx")

	seed := &goword.Document{
		ID:       "doc_recover_save",
		Name:     "recover-save.docx",
		Revision: 1,
		Paragraphs: []goword.Paragraph{
			{ID: "p1", Runs: []goword.Run{{ID: "r1", Text: "原始内容"}}},
		},
		Meta: goword.DocumentMeta{Format: "docx"},
	}
	if err := goword.SaveDOCX(seed, inputPath); err != nil {
		t.Fatalf("prepare input docx: %v", err)
	}

	s := &Server{goWord: goword.NewService(tempDir)}
	openRecorder := httptest.NewRecorder()
	s.handleGoWordOpen(openRecorder, newJSONRequest(t, http.MethodPost, "/v1/goword/open", map[string]string{"path": inputPath}))
	if openRecorder.Code != http.StatusOK {
		t.Fatalf("open status = %d, body=%s", openRecorder.Code, openRecorder.Body.String())
	}

	var openResp goWordOpenResponse
	if err := json.Unmarshal(openRecorder.Body.Bytes(), &openResp); err != nil {
		t.Fatalf("decode open response: %v", err)
	}
	paragraphID := openResp.Document.Paragraphs[0].ID

	applyRecorder := httptest.NewRecorder()
	s.handleGoWordApply(applyRecorder, newJSONRequest(t, http.MethodPost, "/v1/goword/apply", map[string]any{
		"session_id":        openResp.ID,
		"expected_revision": openResp.Document.Revision,
		"operations": []map[string]any{{
			"type":         "replace_text",
			"paragraph_id": paragraphID,
			"start_offset": 0,
			"end_offset":   len([]rune("原始内容")),
			"before_text":  "原始内容",
			"text":         "恢复后内容",
		}},
	}))
	if applyRecorder.Code != http.StatusOK {
		t.Fatalf("apply status = %d, body=%s", applyRecorder.Code, applyRecorder.Body.String())
	}

	s.goWord.Close(openResp.ID)

	saveRecorder := httptest.NewRecorder()
	s.handleGoWordSave(saveRecorder, newJSONRequest(t, http.MethodPost, "/v1/goword/save", map[string]string{"session_id": openResp.ID, "path": ""}))
	if saveRecorder.Code != http.StatusOK {
		t.Fatalf("save-after-recover status = %d, body=%s", saveRecorder.Code, saveRecorder.Body.String())
	}

	var saveResp goWordSaveResponse
	if err := json.Unmarshal(saveRecorder.Body.Bytes(), &saveResp); err != nil {
		t.Fatalf("decode save response: %v", err)
	}

	reopened, err := goword.OpenDOCX(saveResp.Path)
	if err != nil {
		t.Fatalf("reopen saved docx: %v", err)
	}
	if got := reopened.Paragraphs[0].PlainText(); got != "恢复后内容" {
		t.Fatalf("saved doc text mismatch after recover: got %q", got)
	}
}

func TestGoWordHandlerApplyRejectsStaleRevision(t *testing.T) {
	service := goword.NewService(t.TempDir())
	session, err := service.CreateSession(&goword.Document{
		ID:       "doc_apply_revision_guard",
		Name:     "apply-revision-guard.docx",
		Revision: 7,
		Paragraphs: []goword.Paragraph{{
			ID:   "p1",
			Runs: []goword.Run{{ID: "r1", Text: "原始内容"}},
		}},
		Meta: goword.DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	s := &Server{goWord: service}
	recorder := httptest.NewRecorder()
	s.handleGoWordApply(recorder, newJSONRequest(t, http.MethodPost, "/v1/goword/apply", map[string]any{
		"session_id":        session.ID,
		"expected_revision": session.Document.Revision - 1,
		"operations": []map[string]any{{
			"type":         "replace_text",
			"paragraph_id": session.Document.Paragraphs[0].ID,
			"start_offset": 0,
			"end_offset":   len([]rune("原始内容")),
			"before_text":  "原始内容",
			"text":         "不会生效",
		}},
	}))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected stale revision apply to fail, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); !strings.Contains(body, "document revision changed") {
		t.Fatalf("expected revision guard message, got %q", body)
	}
}

func TestGoWordHandlerApplyRequiresExpectedRevision(t *testing.T) {
	service := goword.NewService(t.TempDir())
	session, err := service.CreateSession(&goword.Document{
		ID:       "doc_apply_missing_revision_guard",
		Name:     "apply-missing-revision-guard.docx",
		Revision: 2,
		Paragraphs: []goword.Paragraph{{
			ID:   "p1",
			Runs: []goword.Run{{ID: "r1", Text: "原始内容"}},
		}},
		Meta: goword.DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	s := &Server{goWord: service}
	recorder := httptest.NewRecorder()
	s.handleGoWordApply(recorder, newJSONRequest(t, http.MethodPost, "/v1/goword/apply", map[string]any{
		"session_id": session.ID,
		"operations": []map[string]any{{
			"type":         "replace_text",
			"paragraph_id": session.Document.Paragraphs[0].ID,
			"start_offset": 0,
			"end_offset":   len([]rune("原始内容")),
			"before_text":  "原始内容",
			"text":         "不会生效",
		}},
	}))

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected missing expected_revision apply to fail, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if body := recorder.Body.String(); !strings.Contains(body, "expected_revision is required") {
		t.Fatalf("expected missing revision guard message, got %q", body)
	}
}

func TestGoWordHandlerLayoutFlow(t *testing.T) {
	service := goword.NewService(t.TempDir())
	session, err := service.CreateSession(&goword.Document{
		ID:       "doc_layout_handler",
		Name:     "layout-handler.docx",
		Revision: 2,
		Blocks: []goword.Block{
			{Type: goword.BlockTypeParagraph, Paragraph: &goword.Paragraph{ID: "p1", Runs: []goword.Run{{ID: "r1", Text: "第一页段落"}}}},
			{Type: goword.BlockTypeTable, Table: &goword.Table{ID: "t1", Rows: []goword.TableRow{{
				ID: "row1",
				Cells: []goword.TableCell{{
					ID:     "cell1",
					Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: &goword.Paragraph{ID: "p2", Runs: []goword.Run{{ID: "r2", Text: "表格段落"}}}}},
				}},
			}}}},
		},
		Meta: goword.DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	s := &Server{goWord: service}
	recorder := httptest.NewRecorder()
	s.handleGoWordLayout(recorder, newJSONRequest(t, http.MethodPost, "/v1/goword/layout", map[string]any{
		"session_id": session.ID,
		"options": map[string]any{
			"start_page": 3,
		},
	}))

	if recorder.Code != http.StatusOK {
		t.Fatalf("layout status = %d, body=%s", recorder.Code, recorder.Body.String())
	}

	var response goWordLayoutResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode layout response: %v", err)
	}
	if response.SessionID != session.ID {
		t.Fatalf("unexpected session id: %s", response.SessionID)
	}
	if response.Engine.Name != "semantic-layout" {
		t.Fatalf("unexpected layout engine: %+v", response.Engine)
	}
	if len(response.Pages) == 0 || len(response.Pages[0].Blocks) != 2 {
		t.Fatalf("unexpected pages payload: %+v", response.Pages)
	}
	if response.Pages[0].Blocks[0].Paragraph == nil || response.Pages[0].Blocks[0].Paragraph.LineCount == 0 {
		t.Fatalf("expected paragraph layout block, got %+v", response.Pages[0].Blocks[0])
	}
	if response.Pages[0].Blocks[1].Table == nil || response.Pages[0].Blocks[1].Table.RowCount != 1 {
		t.Fatalf("expected table layout block, got %+v", response.Pages[0].Blocks[1])
	}
}

func TestGoWordHandlerIRPlanAndExecuteFlow(t *testing.T) {
	service := goword.NewService(t.TempDir())
	session, err := service.CreateSession(&goword.Document{
		ID:       "doc_editor_runtime",
		Name:     "editor-runtime.docx",
		Revision: 3,
		Blocks: []goword.Block{
			{Type: goword.BlockTypeParagraph, Paragraph: &goword.Paragraph{ID: "h1", Style: goword.ParagraphStyle{StyleID: "Heading1"}, Runs: []goword.Run{{ID: "r1", Text: "一、现状分析"}}}},
			{Type: goword.BlockTypeParagraph, Paragraph: &goword.Paragraph{ID: "p1", Runs: []goword.Run{{ID: "r2", Text: "原始内容"}}}},
		},
		Meta: goword.DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	s := &Server{goWord: service}

	irRecorder := httptest.NewRecorder()
	irReq := httptest.NewRequest(http.MethodGet, "/v1/goword/ir?session_id="+session.ID, nil)
	s.handleGoWordIR(irRecorder, irReq)
	if irRecorder.Code != http.StatusOK {
		t.Fatalf("ir status = %d, body=%s", irRecorder.Code, irRecorder.Body.String())
	}
	var irResp goWordIRResponse
	if err := json.Unmarshal(irRecorder.Body.Bytes(), &irResp); err != nil {
		t.Fatalf("decode ir response: %v", err)
	}
	if irResp.DocumentID != session.Document.ID || len(irResp.Nodes) < 2 {
		t.Fatalf("unexpected ir payload: %+v", irResp)
	}
	if irResp.Revision != session.Document.Revision {
		t.Fatalf("unexpected ir revision: %+v", irResp)
	}
	if irResp.Nodes[0].StyleSignature == "" {
		t.Fatalf("expected paragraph style signature, got %+v", irResp.Nodes[0])
	}
	if irResp.Nodes[0].CharRange == nil || irResp.Nodes[0].CharRange.End <= 0 {
		t.Fatalf("expected char range in ir node, got %+v", irResp.Nodes[0])
	}

	planRecorder := httptest.NewRecorder()
	s.handleGoWordPlan(planRecorder, newJSONRequest(t, http.MethodPost, "/v1/goword/plan", map[string]any{
		"session_id": session.ID,
		"action": map[string]any{
			"action":                 "replace",
			"target_text":            "原始内容",
			"new_text":               "已改写内容",
			"reader_context_summary": "正文首段需要改写",
			"format_basis":           "保持正文样式不变",
		},
	}))
	if planRecorder.Code != http.StatusOK {
		t.Fatalf("plan status = %d, body=%s", planRecorder.Code, planRecorder.Body.String())
	}
	var planResp goWordPlanResponse
	if err := json.Unmarshal(planRecorder.Body.Bytes(), &planResp); err != nil {
		t.Fatalf("decode plan response: %v", err)
	}
	if planResp.Status != "ready" {
		t.Fatalf("expected ready plan, got %+v", planResp)
	}
	if planResp.ExpectedRevision != session.Document.Revision {
		t.Fatalf("unexpected expected revision: %+v", planResp)
	}
	if planResp.ReplayAction.PreferredParagraphID != "p1" {
		t.Fatalf("expected preferred paragraph id to be resolved, got %+v", planResp)
	}
	if planResp.ReplayAction.TargetSelector == nil || planResp.ReplayAction.TargetSelector.Type != "paragraph_id_selector" || planResp.ReplayAction.TargetSelector.ParagraphID != "p1" {
		t.Fatalf("expected replay action to expose canonical paragraph selector, got %+v", planResp.ReplayAction)
	}

	executeRecorder := httptest.NewRecorder()
	s.handleGoWordExecutePlan(executeRecorder, newJSONRequest(t, http.MethodPost, "/v1/goword/execute_plan", map[string]any{
		"session_id": session.ID,
		"action": map[string]any{
			"action":                 "replace",
			"target_text":            "原始内容",
			"new_text":               "已改写内容",
			"reader_context_summary": "正文首段需要改写",
			"format_basis":           "保持正文样式不变",
		},
	}))
	if executeRecorder.Code != http.StatusOK {
		t.Fatalf("execute status = %d, body=%s", executeRecorder.Code, executeRecorder.Body.String())
	}
	var executeResp goWordExecutePlanResponse
	if err := json.Unmarshal(executeRecorder.Body.Bytes(), &executeResp); err != nil {
		t.Fatalf("decode execute response: %v", err)
	}
	if executeResp.Status != "applied" {
		t.Fatalf("expected applied execute result, got %+v", executeResp)
	}
	if got := executeResp.Document.Paragraphs[1].PlainText(); got != "已改写内容" {
		t.Fatalf("unexpected execute text: %q", got)
	}
	if !executeResp.Validation.OK {
		t.Fatalf("expected validation ok, got %+v", executeResp.Validation)
	}
}

func TestGoWordHandlerIRQueryReturnsStructuredMatches(t *testing.T) {
	service := goword.NewService(t.TempDir())
	session, err := service.CreateSession(&goword.Document{
		ID:       "doc_ir_query",
		Name:     "ir-query.docx",
		Revision: 7,
		Blocks: []goword.Block{
			{Type: goword.BlockTypeParagraph, Paragraph: &goword.Paragraph{ID: "h1", Style: goword.ParagraphStyle{StyleID: "Heading1"}, Runs: []goword.Run{{ID: "r1", Text: "第一章 项目背景"}}}},
			{Type: goword.BlockTypeTable, Table: &goword.Table{
				ID: "tbl_budget",
				Rows: []goword.TableRow{{
					ID: "row_1",
					Cells: []goword.TableCell{{
						ID: "cell_budget",
						Blocks: []goword.Block{{
							Type:      goword.BlockTypeParagraph,
							Paragraph: &goword.Paragraph{ID: "p_budget", Runs: []goword.Run{{ID: "r2", Text: "预算总额"}}},
						}},
					}},
				}},
			}},
		},
		Meta: goword.DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	s := &Server{goWord: service}
	queryRecorder := httptest.NewRecorder()
	s.handleGoWordIRQuery(queryRecorder, newJSONRequest(t, http.MethodPost, "/v1/goword/ir/query", map[string]any{
		"session_id": session.ID,
		"query": map[string]any{
			"query": "预算",
			"type":  "table_cell",
			"limit": 5,
		},
	}))
	if queryRecorder.Code != http.StatusOK {
		t.Fatalf("ir query status = %d, body=%s", queryRecorder.Code, queryRecorder.Body.String())
	}

	var response goWordIRQueryResponse
	if err := json.Unmarshal(queryRecorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode ir query response: %v", err)
	}
	if response.DocumentID != session.Document.ID || response.Revision != session.Document.Revision {
		t.Fatalf("unexpected ir query metadata: %+v", response)
	}
	if response.MatchCount != 1 || len(response.Nodes) != 1 {
		t.Fatalf("expected exactly one ir query hit, got %+v", response)
	}
	if response.Nodes[0].NodeID != "cell_budget" || response.Nodes[0].ParentNodeID != "tbl_budget" {
		t.Fatalf("expected cell node hierarchy in ir query response, got %+v", response.Nodes[0])
	}
	if response.Nodes[0].TableID != "tbl_budget" {
		t.Fatalf("expected table id in ir query response, got %+v", response.Nodes[0])
	}

	irRecorder := httptest.NewRecorder()
	irReq := httptest.NewRequest(http.MethodGet, "/v1/goword/ir?session_id="+session.ID, nil)
	s.handleGoWordIR(irRecorder, irReq)
	if irRecorder.Code != http.StatusOK {
		t.Fatalf("ir status = %d, body=%s", irRecorder.Code, irRecorder.Body.String())
	}
	var irResp goWordIRResponse
	if err := json.Unmarshal(irRecorder.Body.Bytes(), &irResp); err != nil {
		t.Fatalf("decode ir response: %v", err)
	}
	var tableNode struct {
		NodeID       string
		ChildNodeIDs []string
	}
	var cellNode struct {
		NodeID       string
		ParentNodeID string
		ChildNodeIDs []string
	}
	for _, node := range irResp.Nodes {
		if node.NodeID == "tbl_budget" {
			tableNode.NodeID = node.NodeID
			tableNode.ChildNodeIDs = node.ChildNodeIDs
		}
		if node.NodeID == "cell_budget" {
			cellNode.NodeID = node.NodeID
			cellNode.ParentNodeID = node.ParentNodeID
			cellNode.ChildNodeIDs = node.ChildNodeIDs
		}
	}
	if tableNode.NodeID == "" || len(tableNode.ChildNodeIDs) != 1 || tableNode.ChildNodeIDs[0] != "cell_budget" {
		t.Fatalf("expected table node child ids, got %+v", tableNode)
	}
	if cellNode.NodeID == "" || cellNode.ParentNodeID != "tbl_budget" || len(cellNode.ChildNodeIDs) != 1 || cellNode.ChildNodeIDs[0] != "p_budget" {
		t.Fatalf("expected cell node hierarchy, got %+v", cellNode)
	}
}

func TestGoWordHandlerPlanReturnsAmbiguousCandidates(t *testing.T) {
	service := goword.NewService(t.TempDir())
	session, err := service.CreateSession(&goword.Document{
		ID:       "doc_editor_ambiguous",
		Name:     "editor-ambiguous.docx",
		Revision: 1,
		Blocks: []goword.Block{
			{Type: goword.BlockTypeParagraph, Paragraph: &goword.Paragraph{ID: "p1", Runs: []goword.Run{{ID: "r1", Text: "一、现状分析"}}}},
			{Type: goword.BlockTypeParagraph, Paragraph: &goword.Paragraph{ID: "p2", Runs: []goword.Run{{ID: "r2", Text: "二、现状分析"}}}},
		},
		Meta: goword.DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	s := &Server{goWord: service}
	recorder := httptest.NewRecorder()
	s.handleGoWordPlan(recorder, newJSONRequest(t, http.MethodPost, "/v1/goword/plan", map[string]any{
		"session_id": session.ID,
		"action": map[string]any{
			"action":                 "replace",
			"target_text":            "现状分析",
			"new_text":               "方案分析",
			"reader_context_summary": "需要修改现状分析标题",
			"format_basis":           "保持标题样式不变",
		},
	}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("plan status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response goWordPlanResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode ambiguous plan response: %v", err)
	}
	if response.Status != "failed" {
		t.Fatalf("expected failed plan, got %+v", response)
	}
	if response.Diagnostics.ErrorCode != "AMBIGUOUS_TARGET" {
		t.Fatalf("expected ambiguous target error, got %+v", response.Diagnostics)
	}
	if response.Diagnostics.CandidateCount < 2 {
		t.Fatalf("expected multiple candidates, got %+v", response.Diagnostics)
	}
}

func TestGoWordHandlerPlanSupportsHeadingScopeSelector(t *testing.T) {
	service := goword.NewService(t.TempDir())
	session, err := service.CreateSession(&goword.Document{
		ID:       "doc_editor_heading_selector",
		Name:     "editor-heading-selector.docx",
		Revision: 2,
		Blocks: []goword.Block{
			{Type: goword.BlockTypeParagraph, Paragraph: &goword.Paragraph{ID: "p1", Style: goword.ParagraphStyle{StyleID: "Heading1"}, Runs: []goword.Run{{ID: "r1", Text: "一、现状分析"}}}},
			{Type: goword.BlockTypeParagraph, Paragraph: &goword.Paragraph{ID: "p2", Style: goword.ParagraphStyle{StyleID: "Heading1"}, Runs: []goword.Run{{ID: "r2", Text: "二、现状分析"}}}},
		},
		Meta: goword.DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	s := &Server{goWord: service}
	recorder := httptest.NewRecorder()
	s.handleGoWordPlan(recorder, newJSONRequest(t, http.MethodPost, "/v1/goword/plan", map[string]any{
		"session_id": session.ID,
		"action": map[string]any{
			"action":      "replace",
			"target_text": "现状分析",
			"new_text":    "风险分析",
			"target_selector": map[string]any{
				"type":          "heading_scope_selector",
				"heading_level": 1,
				"ordinal":       2,
				"text_equals":   "现状分析",
			},
		},
	}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("plan status = %d, body=%s", recorder.Code, recorder.Body.String())
	}

	var response goWordPlanResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode heading selector plan response: %v", err)
	}
	if response.Status != "ready" {
		t.Fatalf("expected ready plan from heading selector, got %+v", response)
	}
	if response.ReplayAction.PreferredParagraphID != "p2" {
		t.Fatalf("expected selector to resolve second heading, got %+v", response.ReplayAction)
	}
	if response.ReplayAction.TargetSelector == nil || response.ReplayAction.TargetSelector.Type != "paragraph_id_selector" || response.ReplayAction.TargetSelector.ParagraphID != "p2" {
		t.Fatalf("expected replay action to be canonicalized to paragraph_id_selector p2, got %+v", response.ReplayAction)
	}
	if response.ReplayAction.TargetSelector.HeadingLevel != 0 || response.ReplayAction.TargetSelector.Ordinal != 0 || response.ReplayAction.TargetSelector.TextEquals != "" || response.ReplayAction.TargetSelector.Region != "" {
		t.Fatalf("expected replay paragraph selector to clear heading-only fields, got %+v", response.ReplayAction.TargetSelector)
	}
}

func TestGoWordHandlerPlanSupportsTableCellSelector(t *testing.T) {
	service := goword.NewService(t.TempDir())
	session, err := service.CreateSession(&goword.Document{
		ID:       "doc_editor_table_selector",
		Name:     "editor-table-selector.docx",
		Revision: 3,
		Blocks: []goword.Block{{
			Type: goword.BlockTypeTable,
			Table: &goword.Table{
				ID: "tbl_main",
				Rows: []goword.TableRow{
					{
						ID: "row_1",
						Cells: []goword.TableCell{{
							ID: "cell_1",
							Blocks: []goword.Block{{
								Type:      goword.BlockTypeParagraph,
								Paragraph: &goword.Paragraph{ID: "p1", Runs: []goword.Run{{ID: "r1", Text: "预算"}}},
							}},
						}},
					},
					{
						ID: "row_2",
						Cells: []goword.TableCell{{
							ID: "cell_2",
							Blocks: []goword.Block{{
								Type:      goword.BlockTypeParagraph,
								Paragraph: &goword.Paragraph{ID: "p2", Runs: []goword.Run{{ID: "r2", Text: "100"}}},
							}},
						}},
					},
				},
			},
		}},
		Meta: goword.DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	s := &Server{goWord: service}
	recorder := httptest.NewRecorder()
	s.handleGoWordPlan(recorder, newJSONRequest(t, http.MethodPost, "/v1/goword/plan", map[string]any{
		"session_id": session.ID,
		"action": map[string]any{
			"action": "update_table_cell",
			"table_cell": map[string]any{
				"paragraphs": []map[string]any{{"text": "200"}},
			},
			"target_selector": map[string]any{
				"type":       "table_cell_selector",
				"table_id":   "tbl_main",
				"row_index":  1,
				"cell_index": 0,
			},
		},
	}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("plan status = %d, body=%s", recorder.Code, recorder.Body.String())
	}

	var response goWordPlanResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode table selector plan response: %v", err)
	}
	if response.Status != "ready" {
		t.Fatalf("expected ready plan from table selector, got %+v", response)
	}
	if response.ReplayAction.TableID != "tbl_main" || response.ReplayAction.RowIndex != 1 || response.ReplayAction.CellIndex != 0 {
		t.Fatalf("expected selector to resolve explicit table cell target, got %+v", response.ReplayAction)
	}
	if response.ReplayAction.TargetSelector == nil || response.ReplayAction.TargetSelector.Type != "table_cell_selector" || response.ReplayAction.TargetSelector.TableID != "tbl_main" || response.ReplayAction.TargetSelector.RowIndex != 1 || response.ReplayAction.TargetSelector.CellIndex != 0 {
		t.Fatalf("expected replay action to preserve canonical table cell selector, got %+v", response.ReplayAction)
	}
	if response.ReplayAction.TargetSelector.ParagraphID != "" || response.ReplayAction.TargetSelector.HeadingLevel != 0 || response.ReplayAction.TargetSelector.Ordinal != 0 || response.ReplayAction.TargetSelector.Chapter != "" || response.ReplayAction.TargetSelector.TextEquals != "" || response.ReplayAction.TargetSelector.Region != "" {
		t.Fatalf("expected replay table selector to clear paragraph and heading fields, got %+v", response.ReplayAction.TargetSelector)
	}
}

func TestGoWordHandlerPlanReturnsTableStructureConflict(t *testing.T) {
	service := goword.NewService(t.TempDir())
	session, err := service.CreateSession(&goword.Document{
		ID:       "doc_handler_table_conflict",
		Name:     "handler-table-conflict.docx",
		Revision: 1,
		Blocks: []goword.Block{{
			Type: goword.BlockTypeTable,
			Table: &goword.Table{
				ID: "tbl_single_col",
				Rows: []goword.TableRow{{
					ID: "row1",
					Cells: []goword.TableCell{{
						ID:     "cell1",
						Blocks: []goword.Block{{Type: goword.BlockTypeParagraph, Paragraph: &goword.Paragraph{ID: "p1", Runs: []goword.Run{{ID: "r1", Text: "预算"}}}}},
					}},
				}},
			},
		}},
		Meta: goword.DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	s := &Server{goWord: service}
	recorder := httptest.NewRecorder()
	s.handleGoWordPlan(recorder, newJSONRequest(t, http.MethodPost, "/v1/goword/plan", map[string]any{
		"session_id": session.ID,
		"action": map[string]any{
			"action":     "delete_table_column",
			"table_id":   "tbl_single_col",
			"cell_index": 0,
		},
	}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("plan status = %d, body=%s", recorder.Code, recorder.Body.String())
	}

	var response goWordPlanResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode table structure conflict response: %v", err)
	}
	if response.Status != "failed" || response.Diagnostics.ErrorCode != "TABLE_STRUCTURE_CONFLICT" {
		t.Fatalf("expected table structure conflict, got %+v", response)
	}
}

func TestGoWordHandlerExecutePlanSucceedsAfterChoosingCandidate(t *testing.T) {
	service := goword.NewService(t.TempDir())
	session, err := service.CreateSession(&goword.Document{
		ID:       "doc_editor_retry_success",
		Name:     "editor-retry-success.docx",
		Revision: 1,
		Blocks: []goword.Block{
			{Type: goword.BlockTypeParagraph, Paragraph: &goword.Paragraph{ID: "p1", Runs: []goword.Run{{ID: "r1", Text: "一、现状分析"}}}},
			{Type: goword.BlockTypeParagraph, Paragraph: &goword.Paragraph{ID: "p2", Runs: []goword.Run{{ID: "r2", Text: "二、现状分析"}}}},
		},
		Meta: goword.DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	s := &Server{goWord: service}
	planRecorder := httptest.NewRecorder()
	s.handleGoWordPlan(planRecorder, newJSONRequest(t, http.MethodPost, "/v1/goword/plan", map[string]any{
		"session_id": session.ID,
		"action": map[string]any{
			"action":                 "replace",
			"target_text":            "现状分析",
			"new_text":               "方案分析",
			"reader_context_summary": "需要修改第一处现状分析标题",
			"format_basis":           "保持标题样式不变",
		},
	}))
	if planRecorder.Code != http.StatusOK {
		t.Fatalf("plan status = %d, body=%s", planRecorder.Code, planRecorder.Body.String())
	}

	var planResp goWordPlanResponse
	if err := json.Unmarshal(planRecorder.Body.Bytes(), &planResp); err != nil {
		t.Fatalf("decode plan response: %v", err)
	}
	if planResp.Status != "failed" || planResp.Diagnostics.ErrorCode != "AMBIGUOUS_TARGET" {
		t.Fatalf("expected ambiguous plan result, got %+v", planResp)
	}
	if len(planResp.Diagnostics.Candidates) < 2 {
		t.Fatalf("expected at least two plan candidates, got %+v", planResp.Diagnostics)
	}
	chosenParagraphID := planResp.Diagnostics.Candidates[0].ParagraphID
	if chosenParagraphID == "" {
		t.Fatalf("expected paragraph candidate id, got %+v", planResp.Diagnostics.Candidates[0])
	}

	executeRecorder := httptest.NewRecorder()
	s.handleGoWordExecutePlan(executeRecorder, newJSONRequest(t, http.MethodPost, "/v1/goword/execute_plan", map[string]any{
		"session_id": session.ID,
		"action": map[string]any{
			"action":                 "replace",
			"target_text":            "现状分析",
			"new_text":               "方案分析",
			"preferred_paragraph_id": chosenParagraphID,
			"reader_context_summary": "根据已选候选目标重试第一处现状分析标题",
			"format_basis":           "保持标题样式不变",
		},
	}))
	if executeRecorder.Code != http.StatusOK {
		t.Fatalf("execute status = %d, body=%s", executeRecorder.Code, executeRecorder.Body.String())
	}

	var executeResp goWordExecutePlanResponse
	if err := json.Unmarshal(executeRecorder.Body.Bytes(), &executeResp); err != nil {
		t.Fatalf("decode execute response: %v", err)
	}
	if executeResp.Status != "applied" {
		t.Fatalf("expected applied execute result, got %+v", executeResp)
	}
	if executeResp.ReplayAction.PreferredParagraphID != chosenParagraphID {
		t.Fatalf("expected replay action paragraph id %q, got %+v", chosenParagraphID, executeResp.ReplayAction)
	}
	if !executeResp.Validation.OK {
		t.Fatalf("expected validation ok, got %+v", executeResp.Validation)
	}
	if got := executeResp.Document.Paragraphs[0].PlainText(); got != "一、方案分析" {
		t.Fatalf("expected first paragraph updated, got %q", got)
	}
	if got := executeResp.Document.Paragraphs[1].PlainText(); got != "二、现状分析" {
		t.Fatalf("expected second paragraph unchanged, got %q", got)
	}
}

func TestGoWordHandlerExecutePlanFallsBackForMissingChosenCandidate(t *testing.T) {
	service := goword.NewService(t.TempDir())
	session, err := service.CreateSession(&goword.Document{
		ID:       "doc_editor_retry_failure",
		Name:     "editor-retry-failure.docx",
		Revision: 1,
		Blocks: []goword.Block{
			{Type: goword.BlockTypeParagraph, Paragraph: &goword.Paragraph{ID: "p1", Runs: []goword.Run{{ID: "r1", Text: "一、现状分析"}}}},
		},
		Meta: goword.DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	s := &Server{goWord: service}
	executeRecorder := httptest.NewRecorder()
	s.handleGoWordExecutePlan(executeRecorder, newJSONRequest(t, http.MethodPost, "/v1/goword/execute_plan", map[string]any{
		"session_id": session.ID,
		"action": map[string]any{
			"action":                 "replace",
			"target_text":            "现状分析",
			"new_text":               "方案分析",
			"preferred_paragraph_id": "p_missing",
			"reader_context_summary": "根据已选候选目标重试失败场景",
			"format_basis":           "保持标题样式不变",
		},
	}))
	if executeRecorder.Code != http.StatusOK {
		t.Fatalf("execute status = %d, body=%s", executeRecorder.Code, executeRecorder.Body.String())
	}

	var executeResp goWordExecutePlanResponse
	if err := json.Unmarshal(executeRecorder.Body.Bytes(), &executeResp); err != nil {
		t.Fatalf("decode execute response: %v", err)
	}
	if executeResp.Status != "applied" {
		t.Fatalf("expected applied execute result after fallback, got %+v", executeResp)
	}
	if executeResp.Diagnostics.ErrorCode != "" {
		t.Fatalf("expected empty diagnostics after fallback success, got %+v", executeResp.Diagnostics)
	}
	if !executeResp.Validation.OK {
		t.Fatalf("expected validation success after fallback apply, got %+v", executeResp.Validation)
	}
	if len(executeResp.Document.Paragraphs) == 0 || executeResp.Document.Paragraphs[0].PlainText() != "一、方案分析" {
		t.Fatalf("expected document to be updated after fallback apply, got %+v", executeResp.Document)
	}
}

func TestGoWordHandlerValidateFlow(t *testing.T) {
	result := goword.ApplyResult{
		Document: &goword.Document{
			ID:       "doc_validate",
			Name:     "validate.docx",
			Revision: 2,
			Paragraphs: []goword.Paragraph{{
				ID:   "p1",
				Runs: []goword.Run{{ID: "r1", Text: "新内容"}},
			}},
		},
		Diffs: []goword.ParagraphDiff{{
			ParagraphID: "p1",
			Type:        goword.DiffUpdated,
			BeforeText:  "原内容",
			AfterText:   "新内容",
		}},
	}

	s := &Server{goWord: goword.NewService(t.TempDir())}
	recorder := httptest.NewRecorder()
	s.handleGoWordValidate(recorder, newJSONRequest(t, http.MethodPost, "/v1/goword/validate", map[string]any{
		"action": map[string]any{
			"action":      "replace",
			"target_text": "原内容",
			"new_text":    "新内容",
		},
		"result":              result,
		"active_paragraph_id": "p1",
	}))
	if recorder.Code != http.StatusOK {
		t.Fatalf("validate status = %d, body=%s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		OK      bool   `json:"ok"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode validate response: %v", err)
	}
	if !response.OK {
		t.Fatalf("expected validate ok, got %+v", response)
	}
}

func TestGoWordHandlerCheckpointAndBatchEndpoints(t *testing.T) {
	service := goword.NewService(t.TempDir())
	session, err := service.CreateSession(&goword.Document{
		ID:       "doc_handler_p1",
		Name:     "handler-p1.docx",
		Revision: 1,
		Blocks: []goword.Block{
			{Type: goword.BlockTypeParagraph, Paragraph: &goword.Paragraph{ID: "p1", Runs: []goword.Run{{ID: "r1", Text: "第一段"}}}},
			{Type: goword.BlockTypeParagraph, Paragraph: &goword.Paragraph{ID: "p2", Runs: []goword.Run{{ID: "r2", Text: "第二段"}}}},
		},
		Meta: goword.DocumentMeta{Format: "docx"},
	})
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	s := &Server{goWord: service}
	checkpointRecorder := httptest.NewRecorder()
	s.handleGoWordSaveCheckpoint(checkpointRecorder, newJSONRequest(t, http.MethodPost, "/v1/goword/checkpoint", map[string]any{"session_id": session.ID, "label": "start"}))
	if checkpointRecorder.Code != http.StatusOK {
		t.Fatalf("checkpoint status = %d, body=%s", checkpointRecorder.Code, checkpointRecorder.Body.String())
	}
	var checkpointResp goWordCheckpointResponse
	if err := json.Unmarshal(checkpointRecorder.Body.Bytes(), &checkpointResp); err != nil {
		t.Fatalf("decode checkpoint response: %v", err)
	}
	if checkpointResp.ID == "" || checkpointResp.Revision != 1 {
		t.Fatalf("unexpected checkpoint response: %+v", checkpointResp)
	}

	if _, err := service.ExecutePlannedAction(session.ID, goword.EditorAction{Action: "replace", TargetText: "第一段", NewText: "第一段-外部修改"}); err != nil {
		t.Fatalf("mutate session before restore: %v", err)
	}

	listRecorder := httptest.NewRecorder()
	listReq := httptest.NewRequest(http.MethodGet, "/v1/goword/checkpoints?session_id="+session.ID, nil)
	s.handleGoWordListCheckpoints(listRecorder, listReq)
	if listRecorder.Code != http.StatusOK {
		t.Fatalf("list checkpoints status = %d, body=%s", listRecorder.Code, listRecorder.Body.String())
	}
	var listResp goWordCheckpointListResponse
	if err := json.Unmarshal(listRecorder.Body.Bytes(), &listResp); err != nil {
		t.Fatalf("decode checkpoints list: %v", err)
	}
	if len(listResp.Checkpoints) != 1 {
		t.Fatalf("expected one checkpoint, got %+v", listResp)
	}

	restoreRecorder := httptest.NewRecorder()
	s.handleGoWordRestoreCheckpoint(restoreRecorder, newJSONRequest(t, http.MethodPost, "/v1/goword/checkpoint/restore", map[string]any{"session_id": session.ID, "checkpoint_id": checkpointResp.ID}))
	if restoreRecorder.Code != http.StatusOK {
		t.Fatalf("restore checkpoint status = %d, body=%s", restoreRecorder.Code, restoreRecorder.Body.String())
	}
	var restoreResp goWordRestoreCheckpointResponse
	if err := json.Unmarshal(restoreRecorder.Body.Bytes(), &restoreResp); err != nil {
		t.Fatalf("decode restore response: %v", err)
	}
	if got := restoreResp.Document.Paragraphs[0].PlainText(); got != "第一段" {
		t.Fatalf("expected checkpoint restore to recover 第一段, got %q", got)
	}

	planBatchRecorder := httptest.NewRecorder()
	s.handleGoWordPlanBatch(planBatchRecorder, newJSONRequest(t, http.MethodPost, "/v1/goword/batch/plan", map[string]any{
		"session_id": session.ID,
		"actions": []map[string]any{
			{"action": "replace", "target_text": "第一段", "new_text": "第一段-批量修改"},
			{"action": "replace", "target_text": "第二段", "new_text": "第二段-批量修改"},
		},
	}))
	if planBatchRecorder.Code != http.StatusOK {
		t.Fatalf("plan batch status = %d, body=%s", planBatchRecorder.Code, planBatchRecorder.Body.String())
	}
	var planBatchResp goWordBatchResponse
	if err := json.Unmarshal(planBatchRecorder.Body.Bytes(), &planBatchResp); err != nil {
		t.Fatalf("decode batch plan response: %v", err)
	}
	if planBatchResp.ID == "" || planBatchResp.Status != "ready" {
		t.Fatalf("expected ready batch response, got %+v", planBatchResp)
	}

	if _, err := service.ExecutePlannedAction(session.ID, goword.EditorAction{Action: "replace", TargetText: "第二段", NewText: "第二段-外部修改"}); err != nil {
		t.Fatalf("mutate session before batch execute: %v", err)
	}

	executeBatchRecorder := httptest.NewRecorder()
	s.handleGoWordExecuteBatch(executeBatchRecorder, newJSONRequest(t, http.MethodPost, "/v1/goword/batch/execute", map[string]any{"session_id": session.ID, "batch_id": planBatchResp.ID}))
	if executeBatchRecorder.Code != http.StatusOK {
		t.Fatalf("execute batch status = %d, body=%s", executeBatchRecorder.Code, executeBatchRecorder.Body.String())
	}
	var executeBatchResp goWordBatchResponse
	if err := json.Unmarshal(executeBatchRecorder.Body.Bytes(), &executeBatchResp); err != nil {
		t.Fatalf("decode batch execute response: %v", err)
	}
	if executeBatchResp.Status != "failed" {
		t.Fatalf("expected failed batch execute due to revision drift, got %+v", executeBatchResp)
	}
	if executeBatchResp.RecoveryPlan.PrimaryStrategy != "reload_and_replan" {
		t.Fatalf("expected reload_and_replan recovery strategy, got %+v", executeBatchResp.RecoveryPlan)
	}
}

func newJSONRequest(t *testing.T, method, target string, body any) *http.Request {
	t.Helper()
	payload, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body: %v", err)
	}
	req := httptest.NewRequest(method, target, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func createDOCXForServerTest(t *testing.T, path string, parts map[string][]byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create docx dir: %v", err)
	}
	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create docx file: %v", err)
	}
	defer file.Close()

	writer := zip.NewWriter(file)
	for name, content := range parts {
		entry, err := writer.Create(name)
		if err != nil {
			_ = writer.Close()
			t.Fatalf("create zip entry %s: %v", name, err)
		}
		if _, err := entry.Write(content); err != nil {
			_ = writer.Close()
			t.Fatalf("write zip entry %s: %v", name, err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close zip writer: %v", err)
	}
}

func testMinimalPNG() []byte {
	return []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52, 0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01, 0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53, 0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41, 0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00, 0x00, 0x03, 0x01, 0x01, 0x00, 0x18, 0xDD, 0x8D, 0xB1, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45, 0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82}
}
