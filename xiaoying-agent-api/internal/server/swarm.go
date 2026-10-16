package server

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/llm/agent"
	"github.com/auramate/auramate/internal/logging"
	"github.com/gorilla/websocket"
)

var trackerOnce sync.Once

// SwarmSpawnRequest defines the payload for spawning a new agent
type SwarmSpawnRequest struct {
	Role         string `json:"role"`
	SystemPrompt string `json:"system_prompt"`
}

// SwarmSpawnResponse returns the ID of the spawned agent
type SwarmSpawnResponse struct {
	AgentID string `json:"agent_id"`
}

// SwarmMessageRequest defines the payload for sending a message
type SwarmMessageRequest struct {
	To      string `json:"to"` // Agent ID or "*"
	Content string `json:"content"`
}

func (s *Server) handleSwarmSpawn(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SwarmSpawnRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Role == "" {
		http.Error(w, "Role is required", http.StatusBadRequest)
		return
	}

	agentID, err := s.app.Orchestrator.SpawnAgent(req.Role, req.SystemPrompt, "")
	if err != nil {
		logging.Error("Failed to spawn agent", "error", err)
		http.Error(w, "Failed to spawn agent", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(SwarmSpawnResponse{AgentID: agentID})
}

func (s *Server) handleSwarmMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SwarmMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.To == "" {
		req.To = "*" // Default to broadcast
	}

	if req.To == "*" {
		s.app.Orchestrator.Broadcast(req.Content)
	} else {
		s.app.Orchestrator.Send(req.To, req.Content)
	}

	w.WriteHeader(http.StatusOK)
}

func (s *Server) handleSwarmBroadcast(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SwarmMessageRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	s.app.Orchestrator.Broadcast(req.Content)
	w.WriteHeader(http.StatusOK)
}

// SwarmExecuteRequest defines the payload for high-level execution
type SwarmExecuteRequest struct {
	Task string `json:"task"`
}

func (s *Server) handleSwarmExecute(w http.ResponseWriter, r *http.Request) {
	// Ensure tracker is running
	s.ensureSwarmTracker()

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req SwarmExecuteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Task == "" {
		http.Error(w, "Task is required", http.StatusBadRequest)
		return
	}

	// 1. Analyze and Plan (using a Planner Agent)
	// For simplicity, we can spawn a temporary "Planner" agent or just use the orchestrator's capability if we had one.
	// Here we will spawn a hidden "Planner" to break down the task, then spawn workers.

	// Note: In a real system, this logic should be in the orchestrator or a service layer.
	// For now, we put it here to satisfy the user's request for "Backend Intelligent Analysis".

	go func() {
		// Ensure basic workers exist or spawn them if not
		// We want generic "Worker" agents that can do anything (full tools).
		// Let's spawn 3 generic workers if they don't exist.
		workers := s.app.Orchestrator.GetWorkers()
		workerCount := 0
		for _, role := range workers {
			if role == "worker" {
				workerCount++
			}
		}

		// Spawn unique workers if needed (up to 5)
		// We use "worker-1", "worker-2", etc.
		for i := 1; i <= 5; i++ {
			role := fmt.Sprintf("worker-%d", i)
			// Check if exists
			exists := false
			for _, r := range workers {
				if r == role {
					exists = true
					break
				}
			}
			if !exists {
				systemPrompt := fmt.Sprintf("You are %s, a capable intelligent worker agent. You have access to tools. You must ONLY reply if the Planner explicitly mentions your name '%s' or addresses 'Worker' generically (but prefer waiting for specific assignment). When assigned a task, execute it and report back.\n\nTOOL USAGE RULES:\n1. Use ABSOLUTE paths for file operations to avoid recursion (e.g. do NOT use 'backend/backend/'). Root is current directory.\n2. When writing files, use UTF-8 encoding.", role, role)
				s.app.Orchestrator.SpawnAgent(role, systemPrompt, "")
			}
		}

		// Spawn a Planner (if not exists, or just use a one-off)
		// We'll use a specific "Planner" role.
		plannerID, _ := s.app.Orchestrator.SpawnAgent("planner", `You are the Swarm Planner. 
Your goal is to complete the user's task by utilizing the available worker agents.

CRITICAL ARCHITECTURE NOTE:
- The workers are ISOLATED. Worker A CANNOT see what Worker B said.
- You are the ONLY one who sees everything.
- You MUST utilize a "Shared Memory" pattern for collaboration.

STRATEGY:
1. Analyze the user's task.
2. Break it down into logical subtasks.
3. Broadcast the plan to the workers.
4. You MUST assign tasks to specific workers by their name to avoid broadcast storms.
   Available Agents: worker-1, worker-2, worker-3, worker-4, worker-5.

SHARED MEMORY PROTOCOL (HIERARCHICAL):
- Because workers are isolated, if Worker A needs to pass information to Worker B (or to the final summary), Worker A must write it to a file.
- Define a shared file, e.g., "swarm_shared_context.md".
- OPTIMIZATION: Do NOT dump large raw content (like full file lists or code) into "swarm_shared_context.md".
- INSTEAD:
  1. Write the raw detailed content to a separate file, e.g., "logs/worker-1-files.txt".
  2. Append ONLY a SUMMARY and a REFERENCE to "swarm_shared_context.md".
     - Example: "Worker-1 listed files in 'backend'. Found 50 files. Full list in 'logs/worker-1-files.txt'."
- Instruct subsequent workers to READ "swarm_shared_context.md" first.
- Only if a worker truly needs the raw detail (e.g. to grep specific files), should they read the referenced log file.

EXECUTION RULES:
1. Assign tasks specifically: "worker-1, do X..."
2. For data gathering: "Worker-1, list files, save FULL output to 'logs/w1.txt', and append a ONE-LINE summary to 'swarm_shared_context.md'."
3. For summary: "Worker-5, read 'swarm_shared_context.md', then generate the final report."
4. PATH SAFETY: Explicitly instruct workers to use ABSOLUTE paths or check CWD to avoid recursive path errors (e.g. "backend/backend/").

Your output should be a clear plan followed by directives to specific workers.`, "")

		// Wait a bit for agents to come online (simulated)
		time.Sleep(500 * time.Millisecond)

		// Send the task to the Planner
		s.app.Orchestrator.Send(plannerID, fmt.Sprintf("User Task: %s\n\nPlease analyze and coordinate the execution.", req.Task))

		// CLEANUP: Kill the planner after a timeout to prevent memory leaks
		// In a real system, we should track task completion.
		time.Sleep(2 * time.Minute)
		s.app.Orchestrator.KillAgent(plannerID)
	}()

	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"started", "message":"Task submitted for intelligent execution"}`))
}

func (s *Server) handleSwarmEvents(w http.ResponseWriter, r *http.Request) {
	// Upgrade to WebSocket
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		logging.Error("Failed to upgrade websocket", "error", err)
		return
	}
	defer conn.Close()

	// Subscribe to bus
	sub := s.app.Orchestrator.Subscribe(r.Context())

	// Send initial connection success message
	conn.WriteJSON(map[string]string{"type": "connected", "message": "Listening to Swarm Events"})

	// Loop to send events
	for {
		select {
		case <-r.Context().Done():
			return
		case event, ok := <-sub:
			if !ok {
				return
			}
			// Forward event to websocket
			if err := conn.WriteJSON(event.Payload); err != nil {
				logging.Error("Failed to write to websocket", "error", err)
				return
			}
		case <-time.After(30 * time.Second):
			// Ping to keep alive
			if err := conn.WriteMessage(websocket.PingMessage, []byte{}); err != nil {
				return
			}
		}
	}
}

func (s *Server) ensureSwarmTracker() {
	trackerOnce.Do(func() {
		go s.StartSwarmTracker(context.Background())
	})
}

func (s *Server) StartSwarmTracker(ctx context.Context) {
	// Subscribe to all events
	sub := s.app.Orchestrator.Subscribe(ctx)
	logging.Info("Swarm Token Tracker started")

	for event := range sub {
		// We only care about events that have Usage info
		if event.Payload.Usage != nil {
			s.recordTokenUsage(context.Background(), event.Payload)
		}
	}
}

func (s *Server) recordTokenUsage(ctx context.Context, event agent.MultiAgentEvent) {
	// We use a dummy parent session ID to hide these entries from the main chat list
	// but still allow them to be counted in global stats.
	hiddenParentID := "swarm-hidden-root"

	// Cost calculation (simplified estimation, e.g. $0.50/1M input, $1.50/1M output)
	// This is just a placeholder.
	cost := 0.0

	params := db.CreateSessionParams{
		ID:               event.ID,
		ParentSessionID:  sql.NullString{String: hiddenParentID, Valid: true},
		Title:            fmt.Sprintf("Swarm: %s", event.From),
		MessageCount:     1,
		PromptTokens:     event.Usage.InputTokens,
		CompletionTokens: event.Usage.OutputTokens,
		Cost:             cost,
	}

	_, err := s.app.Queries.CreateSession(ctx, params)
	if err != nil {
		logging.Error("Failed to record swarm token usage", "error", err)
	} else {
		logging.Debug("Recorded swarm token usage", "agent", event.From, "input", event.Usage.InputTokens, "output", event.Usage.OutputTokens)
	}
}
