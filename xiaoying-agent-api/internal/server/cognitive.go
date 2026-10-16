package server

import (
	"encoding/json"
	"net/http"

	"github.com/auramate/auramate/internal/cognitive"
	"github.com/auramate/auramate/internal/logging"
)

type CognitiveStateResponse struct {
	State           string `json:"state"`
	TokenUsage      int64  `json:"token_usage"`
	TokenLimit      int64  `json:"token_limit"`
	RemainingTokens int64  `json:"remaining_tokens"`
	PointsLeft      int64  `json:"points_left"` // RemainingTokens / 1000
}

func (s *Server) handleCognitiveState(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	state, usage, limit, err := s.app.Cognitive.GetState(r.Context())
	if err != nil {
		http.Error(w, "Failed to get state: "+err.Error(), http.StatusInternalServerError)
		return
	}

	remainingTokens := limit - usage
	if remainingTokens < 0 {
		remainingTokens = 0
	}
	pointsLeft := remainingTokens / 1000

	resp := CognitiveStateResponse{
		State:           string(state),
		TokenUsage:      usage,
		TokenLimit:      limit,
		RemainingTokens: remainingTokens,
		PointsLeft:      pointsLeft,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}

func (s *Server) handleCognitiveFeed(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Points int64 `json:"points"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Points <= 0 {
		http.Error(w, "Points must be positive", http.StatusBadRequest)
		return
	}

	if err := s.app.Cognitive.Feed(r.Context(), req.Points); err != nil {
		http.Error(w, "Failed to feed engine: "+err.Error(), http.StatusInternalServerError)
		return
	}

	logging.Info("User fed computing power", "points", req.Points)
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) handleCognitiveSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		TargetState string `json:"target_state"` // "active" or "dormant"
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	logging.Info("Cognitive Switch Request", "target", req.TargetState)

	switch req.TargetState {
	case "active":
		s.app.Cognitive.TransitionTo(r.Context(), string(cognitive.StateWaking))
	case "dormant":
		s.app.Cognitive.TransitionTo(r.Context(), string(cognitive.StateDormant))
	default:
		http.Error(w, "Invalid target state (active/dormant)", http.StatusBadRequest)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}
