package goword

import (
	"archive/zip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type Session struct {
	ID              string                `json:"id"`
	Document        *Document             `json:"document"`
	Previous        *Document             `json:"-"`
	LastChangeHints []ParagraphChangeHint `json:"-"`
	Checkpoints     []SessionCheckpoint   `json:"checkpoints,omitempty"`
	UpdatedAt       time.Time             `json:"updated_at"`
}

type SaveResult struct {
	Path      string    `json:"path"`
	Document  *Document `json:"document"`
	UpdatedAt time.Time `json:"updated_at"`
}

type sessionSnapshotPayload struct {
	Document        *Document             `json:"document"`
	Previous        *Document             `json:"previous,omitempty"`
	LastChangeHints []ParagraphChangeHint `json:"last_change_hints,omitempty"`
	UpdatedAt       time.Time             `json:"updated_at,omitempty"`
}

type SearchRequest struct {
	Query         string `json:"query"`
	Scope         string `json:"scope,omitempty"`
	Mode          string `json:"mode,omitempty"`
	CaseSensitive bool   `json:"case_sensitive,omitempty"`
	Limit         int    `json:"limit,omitempty"`
}

type SearchHit struct {
	Type                string `json:"type"`
	ParagraphID         string `json:"paragraph_id,omitempty"`
	TableID             string `json:"table_id,omitempty"`
	Region              string `json:"region,omitempty"`
	HeaderFooterKind    string `json:"header_footer_kind,omitempty"`
	HeaderFooterVariant string `json:"header_footer_variant,omitempty"`
	RowIndex            int    `json:"row_index,omitempty"`
	CellIndex           int    `json:"cell_index,omitempty"`
	LineIndex           int    `json:"line_index,omitempty"`
	Text                string `json:"text"`
	MatchStart          int    `json:"match_start,omitempty"`
	MatchEnd            int    `json:"match_end,omitempty"`
}

type SearchResult struct {
	Query     string      `json:"query"`
	Scope     string      `json:"scope"`
	Mode      string      `json:"mode"`
	Total     int         `json:"total"`
	Limit     int         `json:"limit"`
	Truncated bool        `json:"truncated"`
	Hits      []SearchHit `json:"hits"`
}

type ApplyOptions struct {
	ExpectedRevision int64
}

type CreateDocumentRequest struct {
	Name                  string                `json:"name,omitempty"`
	Blocks                []BlockInput          `json:"blocks,omitempty"`
	HeaderFooters         []HeaderFooterInput   `json:"header_footers,omitempty"`
	SectionLayout         SectionLayout         `json:"section_layout,omitempty"`
	Styles                []DocumentStyle       `json:"styles,omitempty"`
	Numbering             []NumberingDefinition `json:"numbering,omitempty"`
	DefaultParagraphStyle *ParagraphStyle       `json:"default_paragraph_style,omitempty"`
	DefaultTextStyle      *TextStyle            `json:"default_text_style,omitempty"`
	Theme                 *DocumentTheme        `json:"theme,omitempty"`
}

type Service struct {
	mu           sync.RWMutex
	sessions     map[string]*Session
	batches      map[string]*ActionBatch
	rootDir      string
	snapshotDir  string
	layoutEngine LayoutEngine
}

func NewService(rootDir string) *Service {
	snapshotDir := filepath.Join(rootDir, ".goword_snapshots")
	_ = os.MkdirAll(snapshotDir, 0o755)

	return &Service{
		sessions:     make(map[string]*Session),
		batches:      make(map[string]*ActionBatch),
		rootDir:      filepath.Clean(rootDir),
		snapshotDir:  snapshotDir,
		layoutEngine: NewSemanticLayoutEngine(),
	}
}

func (s *Service) SetLayoutEngine(engine LayoutEngine) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if engine == nil {
		s.layoutEngine = NewSemanticLayoutEngine()
		return
	}
	s.layoutEngine = engine
}

func (s *Service) OpenDocument(path string) (*Session, error) {
	if !SupportsPath(path) {
		return nil, fmt.Errorf("unsupported document path: %s", path)
	}

	cleanPath := filepath.Clean(path)

	s.mu.Lock()
	for _, session := range s.sessions {
		if session == nil || session.Document == nil {
			continue
		}
		sourcePath := strings.TrimSpace(session.Document.SourcePath)
		if sourcePath == "" {
			continue
		}
		if filepath.Clean(sourcePath) == cleanPath {
			s.mu.Unlock()
			return cloneSession(session), nil
		}
	}
	s.mu.Unlock()

	document, err := OpenDOCX(cleanPath)
	if err != nil {
		return nil, err
	}

	return s.CreateSession(document)
}

func (s *Service) CreateDocumentSession(req CreateDocumentRequest) (*Session, error) {
	document, err := buildDocumentFromCreateRequest(req)
	if err != nil {
		return nil, err
	}
	return s.CreateSession(document)
}

func buildDocumentFromCreateRequest(req CreateDocumentRequest) (*Document, error) {
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "untitled.docx"
	}
	sectionXML := defaultSectionXML()
	if hasSectionLayoutPatch(req.SectionLayout) {
		merged, err := mergeSectionLayout("", req.SectionLayout)
		if err != nil {
			return nil, err
		}
		sectionXML = merged
	}
	document := &Document{
		ID:       nextID("doc"),
		Name:     name,
		Revision: 1,
		Blocks:   buildBlocksFromInputs(req.Blocks),
		Meta: DocumentMeta{
			Format:        "docx",
			SectionXML:    sectionXML,
			SectionLayout: parseSectionLayout(sectionXML),
			Styles:        append([]DocumentStyle(nil), req.Styles...),
			Numbering:     append([]NumberingDefinition(nil), req.Numbering...),
			Theme:         cloneDocumentTheme(req.Theme),
		},
	}
	if req.DefaultParagraphStyle != nil {
		style := cloneParagraphStyle(*req.DefaultParagraphStyle)
		document.Meta.DefaultParagraphStyle = &style
	}
	if req.DefaultTextStyle != nil {
		style := *req.DefaultTextStyle
		document.Meta.DefaultTextStyle = &style
	}
	if len(req.HeaderFooters) > 0 {
		document.Meta.HeaderFooters = make([]HeaderFooterPart, 0, len(req.HeaderFooters))
		for _, input := range req.HeaderFooters {
			part, err := buildHeaderFooterPartFromInput(input)
			if err != nil {
				return nil, err
			}
			document.Meta.HeaderFooters = append(document.Meta.HeaderFooters, part)
		}
		updatedSectionXML, err := ensureSectionHeaderFooterReferences(document.Meta.SectionXML, document.Meta.HeaderFooters)
		if err != nil {
			return nil, err
		}
		document.Meta.SectionXML = updatedSectionXML
		document.Meta.SectionLayout = parseSectionLayout(updatedSectionXML)
	}
	document.rebuildParagraphs()
	applyResolvedNumbering(document)
	return document, nil
}

func cloneDocumentTheme(theme *DocumentTheme) *DocumentTheme {
	if theme == nil {
		return nil
	}
	cloned := &DocumentTheme{}
	if len(theme.Colors) > 0 {
		cloned.Colors = make(map[string]string, len(theme.Colors))
		for key, value := range theme.Colors {
			cloned.Colors[key] = value
		}
	}
	if len(theme.Fonts) > 0 {
		cloned.Fonts = make(map[string]string, len(theme.Fonts))
		for key, value := range theme.Fonts {
			cloned.Fonts[key] = value
		}
	}
	return cloned
}

func (s *Service) CreateSession(document *Document) (*Session, error) {
	if document == nil {
		return nil, fmt.Errorf("document is nil")
	}
	working := document.Clone()
	applyResolvedNumbering(working)

	s.mu.Lock()
	defer s.mu.Unlock()

	session := &Session{
		ID:          uuid.NewString(),
		Document:    working,
		Previous:    working.Clone(),
		Checkpoints: nil,
		UpdatedAt:   time.Now(),
	}
	s.sessions[session.ID] = session
	if err := s.SaveSnapshot(session); err != nil {
		delete(s.sessions, session.ID)
		return nil, fmt.Errorf("persist initial session snapshot: %w", err)
	}
	return cloneSession(session), nil
}

func (s *Service) Snapshot(sessionID string) (*Session, error) {
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.getOrRestoreSessionLocked(trimmedSessionID)
	if err != nil {
		return nil, err
	}
	return cloneSession(session), nil
}

func (s *Service) Apply(sessionID string, operations []Operation) (*ApplyResult, error) {
	snapshot, err := s.Snapshot(sessionID)
	if err != nil {
		return nil, err
	}
	if snapshot.Document == nil {
		return nil, fmt.Errorf("document is nil")
	}
	enrichedOperations := enrichOperationsForStrictApply(snapshot.Document, operations)
	return s.ApplyWithOptions(sessionID, enrichedOperations, ApplyOptions{ExpectedRevision: snapshot.Document.Revision})
}

func (s *Service) ApplyWithOptions(sessionID string, operations []Operation, options ApplyOptions) (*ApplyResult, error) {
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.getOrRestoreSessionLocked(trimmedSessionID)
	if err != nil {
		return nil, err
	}
	if requiresStrictApply(operations) && options.ExpectedRevision <= 0 {
		return nil, fmt.Errorf("expected_revision is required for apply")
	}
	if options.ExpectedRevision > 0 && session.Document != nil && session.Document.Revision != options.ExpectedRevision {
		return nil, fmt.Errorf("document revision changed: expected %d, current %d", options.ExpectedRevision, session.Document.Revision)
	}

	before := session.Document.Clone()
	previousBefore := session.Previous.Clone()
	working := session.Document.Clone()
	hints, err := ApplyOperationsWithHints(working, operations)
	if err != nil {
		return nil, err
	}
	applyResolvedNumbering(working)
	session.Document = working
	session.Previous = before
	session.LastChangeHints = hints
	session.UpdatedAt = time.Now()
	diffs := BuildParagraphDiffs(before, session.Document)

	// 自动保存快照
	if err := s.SaveSnapshot(session); err != nil {
		session.Document = before
		session.Previous = previousBefore
		session.LastChangeHints = nil
		session.UpdatedAt = time.Now()
		return nil, fmt.Errorf("persist session snapshot after apply: %w", err)
	}

	return &ApplyResult{
		Document: session.Document.Clone(),
		Diffs:    ApplyParagraphChangeHints(diffs, hints),
	}, nil
}

func (s *Service) RollbackToPrevious(sessionID string) (*Document, error) {
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.getOrRestoreSessionLocked(trimmedSessionID)
	if err != nil {
		return nil, err
	}
	if session.Previous == nil {
		return nil, fmt.Errorf("no previous document snapshot to rollback")
	}

	before := session.Document.Clone()
	rolledBack := session.Previous.Clone()
	session.Document = rolledBack
	session.Previous = before
	session.LastChangeHints = nil
	session.UpdatedAt = time.Now()
	if err := s.SaveSnapshot(session); err != nil {
		session.Document = before
		session.Previous = rolledBack
		session.UpdatedAt = time.Now()
		return nil, fmt.Errorf("persist session snapshot after rollback: %w", err)
	}

	return session.Document.Clone(), nil
}

func requiresStrictApply(operations []Operation) bool {
	for _, operation := range operations {
		if operation.Type != OperationScrollTo {
			return true
		}
	}
	return false
}

func enrichOperationsForStrictApply(document *Document, operations []Operation) []Operation {
	if document == nil || len(operations) == 0 {
		return operations
	}

	enriched := make([]Operation, len(operations))
	copy(enriched, operations)

	for index, operation := range enriched {
		if operation.BeforeText != "" || strings.TrimSpace(operation.ParagraphID) == "" {
			continue
		}

		paragraph, _, err := findParagraph(document, operation.ParagraphID)
		if err != nil || paragraph == nil {
			continue
		}

		contentRunes := []rune(paragraph.PlainText())
		if operation.StartOffset < 0 || operation.EndOffset < operation.StartOffset || operation.EndOffset > len(contentRunes) {
			continue
		}

		switch operation.Type {
		case OperationReplaceText, OperationUpdateTextStyle, OperationInsertParagraphBreak, OperationInsertLineBreak:
			if operation.EndOffset > operation.StartOffset {
				operation.BeforeText = string(contentRunes[operation.StartOffset:operation.EndOffset])
				enriched[index] = operation
			}
		}
	}

	return enriched
}

func (s *Service) Diff(sessionID string) ([]ParagraphDiff, error) {
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.getOrRestoreSessionLocked(trimmedSessionID)
	if err != nil {
		return nil, err
	}

	diffs := BuildParagraphDiffs(session.Previous, session.Document)
	return ApplyParagraphChangeHints(diffs, session.LastChangeHints), nil
}

func (s *Service) LayoutSnapshot(sessionID string, options LayoutOptions) (*DocumentLayout, error) {
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.getOrRestoreSessionLocked(trimmedSessionID)
	if err != nil {
		return nil, err
	}
	if s.layoutEngine == nil {
		s.layoutEngine = NewSemanticLayoutEngine()
	}

	layout, err := s.layoutEngine.Layout(session.Document.Clone(), options)
	if err != nil {
		return nil, err
	}
	layout.SessionID = trimmedSessionID
	return layout, nil
}

func (s *Service) Close(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, sessionID)
}

func (s *Service) Save(sessionID, requestedPath string) (*SaveResult, error) {
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	session, err := s.getOrRestoreSessionLocked(trimmedSessionID)
	if err != nil {
		return nil, err
	}

	targetPath, err := s.resolveSavePath(session, requestedPath)
	if err != nil {
		return nil, err
	}

	if documentSaveIsNoOp(session) {
		if err := copyDocumentPackage(session.Document.SourcePath, targetPath); err != nil {
			return nil, err
		}
	} else {
		document := session.Document.Clone()
		if err := SaveDOCX(document, targetPath); err != nil {
			return nil, err
		}
	}

	session.Document.SourcePath = targetPath
	session.Document.Name = filepath.Base(targetPath)
	session.Previous = session.Document.Clone()
	session.LastChangeHints = nil
	session.UpdatedAt = time.Now()

	// 自动保存快照
	if err := s.SaveSnapshot(session); err != nil {
		return nil, fmt.Errorf("document saved to %s but persist session snapshot failed: %w", targetPath, err)
	}

	return &SaveResult{
		Path:      targetPath,
		Document:  session.Document.Clone(),
		UpdatedAt: session.UpdatedAt,
	}, nil
}

func documentSaveIsNoOp(session *Session) bool {
	if session == nil || session.Document == nil || session.Previous == nil {
		return false
	}
	sourcePath := strings.TrimSpace(session.Document.SourcePath)
	if sourcePath == "" {
		return false
	}
	if _, err := os.Stat(filepath.Clean(sourcePath)); err != nil {
		return false
	}
	return reflect.DeepEqual(session.Document, session.Previous)
}

func copyDocumentPackage(sourcePath, targetPath string) error {
	source := filepath.Clean(strings.TrimSpace(sourcePath))
	target := filepath.Clean(strings.TrimSpace(targetPath))
	if source == "" || target == "" {
		return fmt.Errorf("sourcePath 和 targetPath 不能为空")
	}
	if source == target {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	sourceFile, err := os.Open(source)
	if err != nil {
		return err
	}
	defer sourceFile.Close()
	targetFile, err := os.Create(target)
	if err != nil {
		return err
	}
	defer targetFile.Close()
	if _, err := io.Copy(targetFile, sourceFile); err != nil {
		return err
	}
	return targetFile.Close()
}

// SaveSnapshot 将当前会话状态保存为 json 文件，文件名带 session id 和时间戳。
func (s *Service) SaveSnapshot(session *Session) error {
	if session == nil || session.Document == nil {
		return fmt.Errorf("empty session or document")
	}

	now := time.Now()
	ts := fmt.Sprintf("%s_%09d", now.Format("20060102_150405"), now.Nanosecond())
	for suffix := 0; ; suffix++ {
		fileName := fmt.Sprintf("%s_%s.json", session.ID, ts)
		if suffix > 0 {
			fileName = fmt.Sprintf("%s_%s_%03d.json", session.ID, ts, suffix)
		}
		filePath := filepath.Join(s.snapshotDir, fileName)

		file, err := os.OpenFile(filePath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err != nil {
			if os.IsExist(err) {
				continue
			}
			return err
		}

		payload := sessionSnapshotPayload{
			Document:        session.Document.Clone(),
			Previous:        session.Previous.Clone(),
			LastChangeHints: cloneParagraphChangeHints(session.LastChangeHints),
			UpdatedAt:       session.UpdatedAt,
		}

		encoder := json.NewEncoder(file)
		encoder.SetIndent("", "  ")
		encodeErr := encoder.Encode(payload)
		closeErr := file.Close()
		if encodeErr != nil {
			return encodeErr
		}
		if closeErr != nil {
			return closeErr
		}
		return nil
	}
}

func (s *Service) getOrRestoreSessionLocked(sessionID string) (*Session, error) {
	session, ok := s.sessions[sessionID]
	if ok {
		return session, nil
	}

	return s.restoreSessionLocked(sessionID)
}

func (s *Service) restoreSessionLocked(sessionID string) (*Session, error) {
	snapshotPath, snapshotTime, err := s.findLatestSnapshotPath(sessionID)
	if err != nil {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}

	raw, err := os.ReadFile(snapshotPath)
	if err != nil {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	payload, err := decodeSessionSnapshot(raw)
	if err != nil {
		return nil, fmt.Errorf("session not found: %s", sessionID)
	}
	document := payload.Document.Clone()
	previous := document.Clone()
	if payload.Previous != nil {
		previous = payload.Previous.Clone()
	}
	applyResolvedNumbering(document)
	applyResolvedNumbering(previous)
	updatedAt := snapshotTime
	if !payload.UpdatedAt.IsZero() {
		updatedAt = payload.UpdatedAt
	}

	session := &Session{
		ID:              sessionID,
		Document:        document,
		Previous:        previous,
		LastChangeHints: cloneParagraphChangeHints(payload.LastChangeHints),
		Checkpoints:     s.listSessionCheckpointsLocked(sessionID),
		UpdatedAt:       updatedAt,
	}
	s.sessions[sessionID] = session
	return session, nil
}

func decodeSessionSnapshot(raw []byte) (*sessionSnapshotPayload, error) {
	var payload sessionSnapshotPayload
	if err := json.Unmarshal(raw, &payload); err == nil && payload.Document != nil {
		payload.Document.ensureBlockStructure()
		if payload.Previous != nil {
			payload.Previous.ensureBlockStructure()
		}
		return &payload, nil
	}

	var legacy Document
	if err := json.Unmarshal(raw, &legacy); err != nil {
		return nil, err
	}
	legacy.ensureBlockStructure()
	return &sessionSnapshotPayload{Document: &legacy}, nil
}

func (s *Service) findLatestSnapshotPath(sessionID string) (string, time.Time, error) {
	entries, err := os.ReadDir(s.snapshotDir)
	if err != nil {
		return "", time.Time{}, err
	}

	prefix := sessionID + "_"
	latestName := ""
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".json") {
			continue
		}
		if latestName == "" || name > latestName {
			latestName = name
		}
	}
	if latestName == "" {
		return "", time.Time{}, fmt.Errorf("snapshot not found")
	}

	latestPath := filepath.Join(s.snapshotDir, latestName)
	info, err := os.Stat(latestPath)
	if err != nil {
		return "", time.Time{}, err
	}

	return latestPath, info.ModTime(), nil
}

// LoadSnapshot 从快照 json 恢复 session，返回新创建的会话。
func (s *Service) LoadSnapshot(snapshotPath string) (*Session, error) {
	file, err := os.Open(snapshotPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	var document Document
	if err := json.NewDecoder(file).Decode(&document); err != nil {
		return nil, err
	}

	return s.CreateSession(&document)
}

// ListSnapshots 返回快照文件列表。
func (s *Service) ListSnapshots() ([]string, error) {
	entries, err := os.ReadDir(s.snapshotDir)
	if err != nil {
		return nil, err
	}

	paths := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".json") {
			paths = append(paths, filepath.Join(s.snapshotDir, entry.Name()))
		}
	}

	return paths, nil
}

func (s *Service) Search(sessionID string, request SearchRequest) (*SearchResult, error) {
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}

	query := strings.TrimSpace(request.Query)
	if query == "" {
		return nil, fmt.Errorf("query is required")
	}

	scope := normalizeSearchScope(request.Scope)
	mode := normalizeSearchMode(request.Mode)
	limit := request.Limit
	if limit <= 0 {
		limit = 100
	}

	s.mu.Lock()
	session, err := s.getOrRestoreSessionLocked(trimmedSessionID)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	document := session.Document.Clone()
	s.mu.Unlock()

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

	return &SearchResult{
		Query:     query,
		Scope:     scope,
		Mode:      mode,
		Total:     len(hits),
		Limit:     limit,
		Truncated: truncated,
		Hits:      hits,
	}, nil
}

func normalizeSearchScope(scope string) string {
	trimmed := strings.ToLower(strings.TrimSpace(scope))
	switch trimmed {
	case "line", "paragraph", "table", "all":
		return trimmed
	default:
		return "all"
	}
}

func normalizeSearchMode(mode string) string {
	trimmed := strings.ToLower(strings.TrimSpace(mode))
	switch trimmed {
	case "exact", "contains":
		return trimmed
	default:
		return "contains"
	}
}

func collectSearchHitsFromBlocks(blocks []Block, query, scope, mode string, caseSensitive bool, appendHit func(hit SearchHit)) {
	for _, block := range blocks {
		switch block.Type {
		case BlockTypeTable:
			if block.Table == nil {
				continue
			}
			for rowIndex, row := range block.Table.Rows {
				for cellIndex, cell := range row.Cells {
					collectSearchHitsFromCell(cell, block.Table.ID, "body", "", "", rowIndex, cellIndex, query, scope, mode, caseSensitive, appendHit)
				}
			}
			if scope == "all" || scope == "table" {
				collectTableSearchHits(*block.Table, "body", "", "", query, mode, caseSensitive, appendHit)
			}
		default:
			if block.Paragraph == nil {
				continue
			}
			collectParagraphSearchHits(*block.Paragraph, "", "body", "", "", 0, 0, query, scope, mode, caseSensitive, appendHit)
		}
	}
}

func collectSearchHitsFromHeaderFooters(parts []HeaderFooterPart, query, scope, mode string, caseSensitive bool, appendHit func(hit SearchHit)) {
	for _, part := range parts {
		region := strings.TrimSpace(strings.ToLower(part.Kind))
		if region == "" {
			region = "header_footer"
		}
		collectSearchHitsFromBlocksWithRegion(part.Blocks, region, part.Kind, part.Variant, query, scope, mode, caseSensitive, appendHit)
	}
}

func collectSearchHitsFromBlocksWithRegion(blocks []Block, region, headerFooterKind, headerFooterVariant, query, scope, mode string, caseSensitive bool, appendHit func(hit SearchHit)) {
	for _, block := range blocks {
		switch block.Type {
		case BlockTypeTable:
			if block.Table == nil {
				continue
			}
			for rowIndex, row := range block.Table.Rows {
				for cellIndex, cell := range row.Cells {
					collectSearchHitsFromCell(cell, block.Table.ID, region, headerFooterKind, headerFooterVariant, rowIndex, cellIndex, query, scope, mode, caseSensitive, appendHit)
				}
			}
			if scope == "all" || scope == "table" {
				collectTableSearchHits(*block.Table, region, headerFooterKind, headerFooterVariant, query, mode, caseSensitive, appendHit)
			}
		default:
			if block.Paragraph == nil {
				continue
			}
			collectParagraphSearchHits(*block.Paragraph, "", region, headerFooterKind, headerFooterVariant, 0, 0, query, scope, mode, caseSensitive, appendHit)
		}
	}
}

func collectSearchHitsFromCell(cell TableCell, tableID, region, headerFooterKind, headerFooterVariant string, rowIndex, cellIndex int, query, scope, mode string, caseSensitive bool, appendHit func(hit SearchHit)) {
	for _, block := range cell.Blocks {
		switch block.Type {
		case BlockTypeTable:
			if block.Table == nil {
				continue
			}
			for childRowIndex, childRow := range block.Table.Rows {
				for childCellIndex, childCell := range childRow.Cells {
					collectSearchHitsFromCell(childCell, block.Table.ID, region, headerFooterKind, headerFooterVariant, childRowIndex, childCellIndex, query, scope, mode, caseSensitive, appendHit)
				}
			}
			if scope == "all" || scope == "table" {
				collectTableSearchHits(*block.Table, region, headerFooterKind, headerFooterVariant, query, mode, caseSensitive, appendHit)
			}
		default:
			if block.Paragraph == nil {
				continue
			}
			collectParagraphSearchHits(*block.Paragraph, tableID, region, headerFooterKind, headerFooterVariant, rowIndex, cellIndex, query, scope, mode, caseSensitive, appendHit)
		}
	}
}

func collectParagraphSearchHits(paragraph Paragraph, tableID, region, headerFooterKind, headerFooterVariant string, rowIndex, cellIndex int, query, scope, mode string, caseSensitive bool, appendHit func(hit SearchHit)) {
	text := paragraph.PlainText()
	if text == "" {
		return
	}

	if scope == "all" || scope == "paragraph" {
		matchStart, matchEnd, matched := matchTextRange(text, query, mode, caseSensitive)
		if matched {
			appendHit(SearchHit{
				Type:                "paragraph",
				ParagraphID:         paragraph.ID,
				TableID:             tableID,
				Region:              region,
				HeaderFooterKind:    headerFooterKind,
				HeaderFooterVariant: headerFooterVariant,
				RowIndex:            rowIndex,
				CellIndex:           cellIndex,
				Text:                text,
				MatchStart:          matchStart,
				MatchEnd:            matchEnd,
			})
		}
	}

	if scope == "all" || scope == "line" {
		lines := strings.Split(text, "\n")
		for lineIndex, line := range lines {
			matchStart, matchEnd, matched := matchTextRange(line, query, mode, caseSensitive)
			if !matched {
				continue
			}
			appendHit(SearchHit{
				Type:                "line",
				ParagraphID:         paragraph.ID,
				TableID:             tableID,
				Region:              region,
				HeaderFooterKind:    headerFooterKind,
				HeaderFooterVariant: headerFooterVariant,
				RowIndex:            rowIndex,
				CellIndex:           cellIndex,
				LineIndex:           lineIndex,
				Text:                line,
				MatchStart:          matchStart,
				MatchEnd:            matchEnd,
			})
		}
	}

	if scope == "table" && strings.TrimSpace(tableID) != "" {
		matchStart, matchEnd, matched := matchTextRange(text, query, mode, caseSensitive)
		if matched {
			appendHit(SearchHit{
				Type:                "table_cell",
				ParagraphID:         paragraph.ID,
				TableID:             tableID,
				Region:              region,
				HeaderFooterKind:    headerFooterKind,
				HeaderFooterVariant: headerFooterVariant,
				RowIndex:            rowIndex,
				CellIndex:           cellIndex,
				Text:                text,
				MatchStart:          matchStart,
				MatchEnd:            matchEnd,
			})
		}
	}
}

func collectTableSearchHits(table Table, region, headerFooterKind, headerFooterVariant, query, mode string, caseSensitive bool, appendHit func(hit SearchHit)) {
	rowTexts := make([]string, 0, len(table.Rows))
	for _, row := range table.Rows {
		cellTexts := make([]string, 0, len(row.Cells))
		for _, cell := range row.Cells {
			cellTexts = append(cellTexts, strings.TrimSpace(tableCellPlainText(cell)))
		}
		rowTexts = append(rowTexts, strings.Join(cellTexts, " | "))
	}
	tableText := strings.TrimSpace(strings.Join(rowTexts, "\n"))
	if tableText == "" {
		return
	}
	matchStart, matchEnd, matched := matchTextRange(tableText, query, mode, caseSensitive)
	if !matched {
		return
	}
	appendHit(SearchHit{
		Type:                "table",
		TableID:             table.ID,
		Region:              region,
		HeaderFooterKind:    headerFooterKind,
		HeaderFooterVariant: headerFooterVariant,
		Text:                tableText,
		MatchStart:          matchStart,
		MatchEnd:            matchEnd,
	})
}

func tableCellPlainText(cell TableCell) string {
	parts := make([]string, 0)
	for _, block := range cell.Blocks {
		switch block.Type {
		case BlockTypeTable:
			if block.Table == nil {
				continue
			}
			parts = append(parts, strings.TrimSpace(tablePlainText(*block.Table)))
		default:
			if block.Paragraph == nil {
				continue
			}
			text := strings.TrimSpace(block.Paragraph.PlainText())
			if text != "" {
				parts = append(parts, text)
			}
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func tablePlainText(table Table) string {
	rowTexts := make([]string, 0, len(table.Rows))
	for _, row := range table.Rows {
		cellTexts := make([]string, 0, len(row.Cells))
		for _, cell := range row.Cells {
			cellTexts = append(cellTexts, strings.TrimSpace(tableCellPlainText(cell)))
		}
		rowTexts = append(rowTexts, strings.Join(cellTexts, " | "))
	}
	return strings.TrimSpace(strings.Join(rowTexts, "\n"))
}

func matchTextRange(text, query, mode string, caseSensitive bool) (int, int, bool) {
	source := text
	needle := query
	if !caseSensitive {
		source = strings.ToLower(source)
		needle = strings.ToLower(needle)
	}

	sourceRunes := []rune(source)
	needleRunes := []rune(needle)
	originalRunes := []rune(text)
	if len(needleRunes) == 0 || len(sourceRunes) < len(needleRunes) {
		return 0, 0, false
	}

	if mode == "exact" {
		if strings.TrimSpace(source) != strings.TrimSpace(needle) {
			return 0, 0, false
		}
		return 0, len(originalRunes), true
	}

	for index := 0; index <= len(sourceRunes)-len(needleRunes); index++ {
		matched := true
		for offset := 0; offset < len(needleRunes); offset++ {
			if sourceRunes[index+offset] != needleRunes[offset] {
				matched = false
				break
			}
		}
		if matched {
			return index, index + len(needleRunes), true
		}
	}

	return 0, 0, false
}

func cloneSession(session *Session) *Session {
	if session == nil {
		return nil
	}
	return &Session{
		ID:              session.ID,
		Document:        session.Document.Clone(),
		Previous:        session.Previous.Clone(),
		LastChangeHints: cloneParagraphChangeHints(session.LastChangeHints),
		Checkpoints:     cloneSessionCheckpoints(session.Checkpoints),
		UpdatedAt:       session.UpdatedAt,
	}
}

func cloneSessionCheckpoints(checkpoints []SessionCheckpoint) []SessionCheckpoint {
	if len(checkpoints) == 0 {
		return nil
	}
	cloned := make([]SessionCheckpoint, len(checkpoints))
	copy(cloned, checkpoints)
	return cloned
}

func cloneParagraphChangeHints(hints []ParagraphChangeHint) []ParagraphChangeHint {
	if len(hints) == 0 {
		return nil
	}
	cloned := make([]ParagraphChangeHint, len(hints))
	for index, hint := range hints {
		cloned[index] = ParagraphChangeHint{ParagraphID: hint.ParagraphID}
		if len(hint.CharacterRanges) > 0 {
			cloned[index].CharacterRanges = append([]CharacterRangeDiff(nil), hint.CharacterRanges...)
		}
	}
	return cloned
}

func (s *Service) resolveSavePath(session *Session, requestedPath string) (string, error) {
	trimmed := strings.TrimSpace(requestedPath)
	if trimmed == "" {
		if sourcePath := strings.TrimSpace(session.Document.SourcePath); sourcePath != "" {
			return normalizeDOCXPath(sourcePath)
		}

		fileName := strings.TrimSpace(session.Document.Name)
		if fileName == "" {
			fileName = "document.docx"
		}
		return normalizeDOCXPath(filepath.Join(s.rootDir, fileName))
	}

	if filepath.IsAbs(trimmed) {
		return normalizeDOCXPath(trimmed)
	}

	root := filepath.Clean(s.rootDir)
	joined := filepath.Clean(filepath.Join(root, trimmed))
	rel, err := filepath.Rel(root, joined)
	if err != nil {
		return "", fmt.Errorf("resolve save path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("save path escapes workspace root: %s", requestedPath)
	}

	return normalizeDOCXPath(joined)
}

func normalizeDOCXPath(path string) (string, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "", fmt.Errorf("path is required")
	}

	ext := strings.ToLower(filepath.Ext(trimmed))
	if ext == "" {
		trimmed += ".docx"
	} else if ext != ".docx" {
		return "", fmt.Errorf("save path must end with .docx: %s", path)
	}

	return filepath.Clean(trimmed), nil
}

func (s *Service) ReadImage(sessionID, relationshipID, target string) ([]byte, string, error) {
	trimmedSessionID := strings.TrimSpace(sessionID)
	if trimmedSessionID == "" {
		return nil, "", fmt.Errorf("session_id is required")
	}

	trimmedRelationshipID := strings.TrimSpace(relationshipID)
	trimmedTarget := strings.TrimSpace(target)
	if trimmedRelationshipID == "" && trimmedTarget == "" {
		return nil, "", fmt.Errorf("relationship_id or target is required")
	}

	s.mu.Lock()
	session, err := s.getOrRestoreSessionLocked(trimmedSessionID)
	if err != nil {
		s.mu.Unlock()
		return nil, "", err
	}
	sourcePath := strings.TrimSpace(session.Document.SourcePath)
	document := session.Document.Clone()
	s.mu.Unlock()

	if sourcePath == "" {
		return nil, "", fmt.Errorf("document source path is unavailable")
	}

	resolvedTarget := trimmedTarget
	resolvedContentType := ""
	if trimmedRelationshipID != "" {
		image := findImageByRelationshipIDInDocument(document, trimmedRelationshipID)
		if image != nil {
			if resolvedTarget == "" {
				resolvedTarget = strings.TrimSpace(image.Target)
			}
			resolvedContentType = strings.TrimSpace(image.ContentType)
		} else if resolvedTarget == "" {
			return nil, "", fmt.Errorf("image relationship not found: %s", trimmedRelationshipID)
		}
	}

	if resolvedTarget == "" {
		return nil, "", fmt.Errorf("image target is empty")
	}

	zipEntryName := normalizeImageZipEntry(resolvedTarget)
	if zipEntryName == "" {
		return nil, "", fmt.Errorf("invalid image target: %s", resolvedTarget)
	}

	reader, err := zip.OpenReader(sourcePath)
	if err != nil {
		return nil, "", fmt.Errorf("open source docx: %w", err)
	}
	defer reader.Close()

	for _, file := range reader.File {
		if normalizeZipName(file.Name) != zipEntryName {
			continue
		}
		content, err := readZipFile(file)
		if err != nil {
			return nil, "", err
		}
		if resolvedContentType == "" {
			resolvedContentType = inferImageContentType(zipEntryName)
		}
		if resolvedContentType == "" {
			resolvedContentType = "application/octet-stream"
		}
		return content, resolvedContentType, nil
	}

	return nil, "", fmt.Errorf("image not found in source package: %s", zipEntryName)
}

func findImageByRelationshipID(blocks []Block, relationshipID string) *RunImage {
	for _, block := range blocks {
		switch block.Type {
		case BlockTypeTable:
			if block.Table == nil {
				continue
			}
			for _, row := range block.Table.Rows {
				for _, cell := range row.Cells {
					if image := findImageByRelationshipID(cell.Blocks, relationshipID); image != nil {
						return image
					}
				}
			}
		default:
			if block.Paragraph == nil {
				continue
			}
			for _, run := range block.Paragraph.Runs {
				if run.Image == nil {
					continue
				}
				if strings.TrimSpace(run.Image.RelationshipID) == relationshipID {
					image := *run.Image
					return &image
				}
			}
		}
	}
	return nil
}

func findImageByRelationshipIDInDocument(document *Document, relationshipID string) *RunImage {
	if document == nil {
		return nil
	}
	if image := findImageByRelationshipID(document.Blocks, relationshipID); image != nil {
		return image
	}
	if image := findImageByRelationshipIDInParts(document.Meta.HeaderFooters, relationshipID); image != nil {
		return image
	}
	for _, section := range document.Meta.Sections {
		if image := findImageByRelationshipIDInParts(section.HeaderFooters, relationshipID); image != nil {
			return image
		}
	}
	return nil
}

func findImageByRelationshipIDInParts(parts []HeaderFooterPart, relationshipID string) *RunImage {
	for _, part := range parts {
		if image := findImageByRelationshipID(part.Blocks, relationshipID); image != nil {
			return image
		}
		for _, paragraph := range part.Paragraphs {
			for _, run := range paragraph.Runs {
				if run.Image == nil {
					continue
				}
				if strings.TrimSpace(run.Image.RelationshipID) == relationshipID {
					image := *run.Image
					return &image
				}
			}
		}
	}
	return nil
}

func normalizeImageZipEntry(target string) string {
	normalized := normalizeZipName(target)
	if normalized == "" {
		return ""
	}
	if strings.HasPrefix(normalized, "word/") {
		return normalized
	}
	return normalizeZipName("word/" + normalized)
}

func normalizeZipName(name string) string {
	normalized := strings.TrimSpace(strings.ReplaceAll(name, `\\`, "/"))
	normalized = strings.TrimPrefix(normalized, "/")
	normalized = strings.TrimPrefix(normalized, "./")
	normalized = filepath.ToSlash(filepath.Clean(normalized))
	if normalized == "." {
		return ""
	}
	return normalized
}

func inferImageContentType(zipEntry string) string {
	switch strings.ToLower(filepath.Ext(zipEntry)) {
	case ".png":
		return "image/png"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".gif":
		return "image/gif"
	case ".webp":
		return "image/webp"
	case ".bmp":
		return "image/bmp"
	case ".svg":
		return "image/svg+xml"
	default:
		return ""
	}
}
