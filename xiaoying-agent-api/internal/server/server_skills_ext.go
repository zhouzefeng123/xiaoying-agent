package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

func (s *Server) handleSkillItem(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// CORS headers
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	name := strings.TrimPrefix(r.URL.Path, "/v1/skills/")
	if name == "" {
		http.Error(w, "Skill name required", http.StatusBadRequest)
		return
	}

	if r.Method == "DELETE" {
		err := s.app.Skills.DeleteSkill(name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "message": "Skill deleted"})
		return
	}

	if r.Method == "POST" {
		// Toggle action is removed as per user request.
		// Skills are now auto-discovered and always available (or managed internally).
		// We return 410 Gone or 400 Bad Request to indicate this feature is deprecated.
		http.Error(w, "Skill toggling is deprecated. Skills are auto-managed.", http.StatusBadRequest)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}
