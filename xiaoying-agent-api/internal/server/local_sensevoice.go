package server

import (
	"encoding/json"
	"net/http"

	"github.com/auramate/auramate/internal/llm/provider"
)

func (s *Server) handleLocalSenseVoiceStatus(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	json.NewEncoder(w).Encode(provider.GetLocalSenseVoiceStatus())
}
