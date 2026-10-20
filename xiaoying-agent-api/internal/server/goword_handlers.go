package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/auramate/auramate/internal/goword"
)

func (s *Server) handleGoWordCreate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req goword.CreateDocumentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	session, err := s.goWord.CreateDocumentSession(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(session)
}

func (s *Server) handleGoWordOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	resolvedPath := strings.TrimSpace(req.Path)
	if resolvedPath == "" {
		http.Error(w, "path is required", http.StatusBadRequest)
		return
	}
	if !strings.HasPrefix(resolvedPath, "/") && !strings.Contains(resolvedPath, `:\`) {
		resolvedPath = s.resolveAttachmentPath(r.Context(), resolvedPath)
	}

	session, err := s.goWord.OpenDocument(resolvedPath)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(session)
}

func (s *Server) handleGoWordDocument(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}

	session, err := s.goWord.Snapshot(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(session)
}

func (s *Server) handleGoWordApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID        string             `json:"session_id"`
		ExpectedRevision int64              `json:"expected_revision,omitempty"`
		Operations       []goword.Operation `json:"operations"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}
	if req.ExpectedRevision <= 0 {
		http.Error(w, "expected_revision is required", http.StatusBadRequest)
		return
	}

	result, err := s.goWord.ApplyWithOptions(req.SessionID, req.Operations, goword.ApplyOptions{ExpectedRevision: req.ExpectedRevision})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleGoWordDiff(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}

	diffs, err := s.goWord.Diff(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"diffs": diffs})
}

func (s *Server) handleGoWordClose(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}

	s.goWord.Close(req.SessionID)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleGoWordSave(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
		Path      string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}

	result, err := s.goWord.Save(req.SessionID, req.Path)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleGoWordImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	relationshipID := strings.TrimSpace(r.URL.Query().Get("relationship_id"))
	target := strings.TrimSpace(r.URL.Query().Get("target"))
	if sessionID == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}

	content, contentType, err := s.goWord.ReadImage(sessionID, relationshipID, target)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (s *Server) handleGoWordSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID     string `json:"session_id"`
		Query         string `json:"query"`
		Scope         string `json:"scope,omitempty"`
		Mode          string `json:"mode,omitempty"`
		CaseSensitive bool   `json:"case_sensitive,omitempty"`
		Limit         int    `json:"limit,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}

	result, err := s.goWord.Search(req.SessionID, goword.SearchRequest{
		Query:         req.Query,
		Scope:         req.Scope,
		Mode:          req.Mode,
		CaseSensitive: req.CaseSensitive,
		Limit:         req.Limit,
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleGoWordLayout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string               `json:"session_id"`
		Options   goword.LayoutOptions `json:"options"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}

	layout, err := s.goWord.LayoutSnapshot(req.SessionID, req.Options)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(layout)
}

func (s *Server) handleGoWordIR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}

	ir, err := s.goWord.BuildIR(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(ir)
}

func (s *Server) handleGoWordIRQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string                `json:"session_id"`
		Query     goword.IRQueryRequest `json:"query"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}

	response, err := s.goWord.QueryIR(req.SessionID, req.Query)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleGoWordPlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string                  `json:"session_id"`
		Action    WordEditorActionPayload `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}

	normalizeWordEditorPayload(&req.Action)
	plan, err := s.goWord.PlanAction(req.SessionID, toGoWordEditorAction(req.Action))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(plan)
}

func (s *Server) handleGoWordExecutePlan(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string                  `json:"session_id"`
		Action    WordEditorActionPayload `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}

	normalizeWordEditorPayload(&req.Action)
	result, err := s.goWord.ExecutePlannedAction(req.SessionID, toGoWordEditorAction(req.Action))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(result)
}

func (s *Server) handleGoWordSaveCheckpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
		Label     string `json:"label,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}

	checkpoint, err := s.goWord.SaveCheckpoint(req.SessionID, req.Label)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(checkpoint)
}

func (s *Server) handleGoWordListCheckpoints(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	sessionID := strings.TrimSpace(r.URL.Query().Get("session_id"))
	if sessionID == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}

	checkpoints, err := s.goWord.ListCheckpoints(sessionID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"checkpoints": checkpoints})
}

func (s *Server) handleGoWordRestoreCheckpoint(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID    string `json:"session_id"`
		CheckpointID string `json:"checkpoint_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.SessionID) == "" || strings.TrimSpace(req.CheckpointID) == "" {
		http.Error(w, "session_id and checkpoint_id are required", http.StatusBadRequest)
		return
	}

	document, err := s.goWord.RestoreCheckpoint(req.SessionID, req.CheckpointID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"document": document})
}

func (s *Server) handleGoWordPlanBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string                    `json:"session_id"`
		Actions   []WordEditorActionPayload `json:"actions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.SessionID) == "" {
		http.Error(w, "session_id is required", http.StatusBadRequest)
		return
	}
	actions := make([]goword.EditorAction, 0, len(req.Actions))
	for index := range req.Actions {
		normalizeWordEditorPayload(&req.Actions[index])
		actions = append(actions, toGoWordEditorAction(req.Actions[index]))
	}

	batch, err := s.goWord.PlanActionBatch(req.SessionID, actions)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(batch)
}

func (s *Server) handleGoWordExecuteBatch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		SessionID string `json:"session_id"`
		BatchID   string `json:"batch_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.SessionID) == "" || strings.TrimSpace(req.BatchID) == "" {
		http.Error(w, "session_id and batch_id are required", http.StatusBadRequest)
		return
	}

	batch, err := s.goWord.ExecuteActionBatch(req.SessionID, req.BatchID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(batch)
}

func (s *Server) handleGoWordValidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Action            WordEditorActionPayload `json:"action"`
		Result            goword.ApplyResult      `json:"result"`
		ActiveParagraphID string                  `json:"active_paragraph_id,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	normalizeWordEditorPayload(&req.Action)
	validation := goword.ValidateActionResult(&req.Result, toGoWordEditorAction(req.Action), strings.TrimSpace(req.ActiveParagraphID))

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(validation)
}

func toGoWordEditorAction(payload WordEditorActionPayload) goword.EditorAction {
	return goword.EditorAction{
		Action:               payload.Action,
		TargetText:           payload.TargetText,
		TargetSelector:       payload.TargetSelector,
		PreferredParagraphID: payload.PreferredParagraphID,
		TableID:              payload.TableID,
		RowIndex:             payload.RowIndex,
		CellIndex:            payload.CellIndex,
		StylePatch:           payload.StylePatch,
		ParagraphStylePatch:  payload.ParagraphStylePatch,
		TableSpec:            payload.TableSpec,
		TableRow:             payload.TableRow,
		TableColumn:          payload.TableColumn,
		TableCell:            payload.TableCell,
		HeaderFooter:         payload.HeaderFooter,
		SectionLayoutPatch:   payload.SectionLayoutPatch,
		NewText:              payload.NewText,
		Position:             payload.Position,
		EnableTrackChanges:   payload.EnableTrackChanges,
		EditIntent:           payload.EditIntent,
		ContextScope:         payload.ContextScope,
		Constraints:          payload.Constraints,
		FailurePolicy:        payload.FailurePolicy,
		PreviewOnly:          payload.PreviewOnly,
		RetrievalQuery:       payload.RetrievalQuery,
		RequireRetrieval:     payload.RequireRetrieval,
		ReaderSessionID:      payload.ReaderSessionID,
		ReaderContextSummary: payload.ReaderContextSummary,
		FormatBasis:          payload.FormatBasis,
		ExplorationSummary:   payload.ExplorationSummary,
	}
}
