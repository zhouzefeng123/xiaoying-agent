package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"

	"github.com/auramate/auramate/internal/llm/agent"
)

type workerSnapshotsResponse struct {
	Snapshots []agent.WorkerRuntimeSnapshot `json:"snapshots"`
	Summary   workerSnapshotsSummary        `json:"summary"`
}

type workerSnapshotsSummary struct {
	Total     int `json:"total"`
	Running   int `json:"running"`
	Pending   int `json:"pending"`
	Completed int `json:"completed"`
	Failed    int `json:"failed"`
	Archived  int `json:"archived"`
}

func summarizeWorkerSnapshots(snapshots []agent.WorkerRuntimeSnapshot) workerSnapshotsSummary {
	summary := workerSnapshotsSummary{Total: len(snapshots)}
	for _, snapshot := range snapshots {
		if snapshot.Archived {
			summary.Archived++
		}
		switch strings.ToLower(strings.TrimSpace(snapshot.Status)) {
		case "running":
			summary.Running++
		case "pending":
			summary.Pending++
		case "completed":
			summary.Completed++
		case "failed":
			summary.Failed++
		}
	}
	return summary
}

func (s *Server) handleSwarmSnapshots(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	limit := 50
	if rawLimit := strings.TrimSpace(r.URL.Query().Get("limit")); rawLimit != "" {
		parsed, err := strconv.Atoi(rawLimit)
		if err != nil || parsed <= 0 {
			http.Error(w, "invalid limit", http.StatusBadRequest)
			return
		}
		if parsed > 200 {
			parsed = 200
		}
		limit = parsed
	}

	activeOnly := strings.EqualFold(strings.TrimSpace(r.URL.Query().Get("active_only")), "true")

	var allSnapshots []agent.WorkerRuntimeSnapshot
	if activeOnly {
		allSnapshots = s.app.Orchestrator.ListCurrentActiveWorkerRuntimeSnapshots(0)
	} else {
		allSnapshots = s.app.Orchestrator.ListCurrentWorkerRuntimeSnapshots(0)
	}
	snapshots := allSnapshots
	if limit > 0 && len(snapshots) > limit {
		snapshots = append([]agent.WorkerRuntimeSnapshot(nil), snapshots[:limit]...)
	}
	if snapshots == nil {
		snapshots = make([]agent.WorkerRuntimeSnapshot, 0)
	}
	if allSnapshots == nil {
		allSnapshots = make([]agent.WorkerRuntimeSnapshot, 0)
	}
	response := workerSnapshotsResponse{
		Snapshots: snapshots,
		Summary:   summarizeWorkerSnapshots(allSnapshots),
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}
