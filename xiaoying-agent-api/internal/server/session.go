package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/agent"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/llm/provider"
	"github.com/auramate/auramate/internal/llm/routing"
	"github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/message"
	"github.com/auramate/auramate/internal/session"
)

type sessionContextStatsProvider interface {
	BuildSessionContextStats(ctx context.Context, sessionID string, mode string) (agent.ContextStats, error)
}

type SessionWithSummary struct {
	session.Session
	Summary string `json:"summary,omitempty"`
}

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "GET" {
		ctx := r.Context()
		sessions, err := s.app.Sessions.List(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		response := make([]SessionWithSummary, len(sessions))
		for i, sess := range sessions {
			summary := ""
			if sess.SummaryMessageID != "" {
				msg, err := s.app.Messages.Get(ctx, sess.SummaryMessageID)
				if err == nil {
					summary = msg.Content().Text
				}
			}
			response[i] = SessionWithSummary{
				Session: sess,
				Summary: summary,
			}
		}

		json.NewEncoder(w).Encode(response)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleSessionItem(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Parse ID and potential action (summary)
	path := strings.TrimPrefix(r.URL.Path, "/v1/sessions/")
	parts := strings.Split(path, "/")

	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "ID required", http.StatusBadRequest)
		return
	}

	id := parts[0]
	ctx := r.Context()
	if _, err := s.app.Sessions.Get(ctx, id); err != nil {
		http.Error(w, "Session not found", http.StatusNotFound)
		return
	}

	// Check for sub-resources
	if len(parts) > 1 {
		if parts[1] == "summary" && r.Method == "POST" {
			s.handleGenerateSessionSummary(w, r, id)
			return
		}
		if parts[1] == "messages" && r.Method == "GET" {
			s.handleSessionMessages(w, r, id)
			return
		}
		if parts[1] == "context-stats" && r.Method == "GET" {
			s.handleSessionContextStats(w, r, id)
			return
		}
		if parts[1] == "plan-snapshot" && r.Method == "GET" {
			s.handleSessionPlanSnapshot(w, r, id)
			return
		}
	}

	if r.Method == "GET" {
		sess, err := s.app.Sessions.Get(ctx, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}

		summary := ""
		if sess.SummaryMessageID != "" {
			msg, err := s.app.Messages.Get(ctx, sess.SummaryMessageID)
			if err == nil {
				summary = msg.Content().Text
			}
		}

		resp := SessionWithSummary{
			Session: sess,
			Summary: summary,
		}
		json.NewEncoder(w).Encode(resp)
		return
	}

	if r.Method == "DELETE" {
		// Best effort delete messages
		_ = s.app.Messages.DeleteSessionMessages(ctx, id)

		err := s.app.Sessions.Delete(ctx, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleGenerateSessionSummary(w http.ResponseWriter, r *http.Request, sessionID string) {
	ctx := r.Context()

	// 1. Get Session Messages
	messages, err := s.app.Messages.List(ctx, sessionID)
	if err != nil {
		http.Error(w, "Failed to list messages: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if len(messages) == 0 {
		http.Error(w, "No messages to summarize", http.StatusBadRequest)
		return
	}

	// 2. Prepare Context for LLM
	var transcript strings.Builder
	for _, msg := range messages {
		// Skip existing summary messages or system messages if needed
		if msg.Role == message.System {
			continue
		}
		transcript.WriteString(fmt.Sprintf("%s: %s\n", msg.Role, msg.Content().Text))
	}

	prompt := fmt.Sprintf("请简要总结以下对话的内容：\n\n%s", transcript.String())

	// 3. Generate Summary using a Provider directly
	apiKey := os.Getenv("OPENAI_API_KEY")
	providerName := models.ProviderOpenAI
	modelID := models.GPT4oMini

	// Fallback logic
	if apiKey == "" {
		apiKey = os.Getenv("DASHSCOPE_API_KEY")
		if apiKey == "" {
			apiKey = os.Getenv("ALIYUN_API_KEY")
		}
		if apiKey != "" {
			providerName = models.ProviderAliyun
			modelID = models.QwenTurbo
		}
	}

	if apiKey == "" {
		// Try to use app config providers if available
		// s.app.Config.Providers
		if p, ok := s.app.Config.Providers[models.ProviderOpenAI]; ok && p.APIKey != "" {
			apiKey = p.APIKey
			providerName = models.ProviderOpenAI
			modelID = models.GPT4oMini
		} else if p, ok := s.app.Config.Providers[models.ProviderAliyun]; ok && p.APIKey != "" {
			apiKey = p.APIKey
			providerName = models.ProviderAliyun
			modelID = models.QwenTurbo
		} else if p, ok := s.app.Config.Providers["dashscope"]; ok && p.APIKey != "" {
			apiKey = p.APIKey
			providerName = models.ProviderAliyun
			modelID = models.QwenTurbo
		}
	}

	if apiKey == "" {
		http.Error(w, "No API key available for summarization", http.StatusInternalServerError)
		return
	}

	p, err := provider.NewProvider(providerName, provider.WithAPIKey(apiKey))
	if err != nil {
		http.Error(w, "Failed to create provider: "+err.Error(), http.StatusInternalServerError)
		return
	}

	resp, err := p.SendMessages(ctx, []message.Message{
		{
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: prompt},
			},
		},
	}, nil)
	if err != nil {
		http.Error(w, "Failed to generate summary: "+err.Error(), http.StatusInternalServerError)
		return
	}

	summaryText := resp.Content

	// 4. Save Summary as a Message
	summaryMsg, err := s.app.Messages.Create(ctx, sessionID, message.CreateMessageParams{
		Role:  message.Assistant,
		Model: modelID,
		Parts: []message.ContentPart{
			message.TextContent{Text: summaryText},
		},
	})
	if err != nil {
		http.Error(w, "Failed to save summary message: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// 5. Update Session
	sess, err := s.app.Sessions.Get(ctx, sessionID)
	if err != nil {
		http.Error(w, "Failed to get session: "+err.Error(), http.StatusInternalServerError)
		return
	}

	sess.SummaryMessageID = summaryMsg.ID
	_, err = s.app.Sessions.Save(ctx, sess)
	if err != nil {
		http.Error(w, "Failed to update session: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Return the summary
	json.NewEncoder(w).Encode(map[string]string{
		"summary":    summaryText,
		"message_id": summaryMsg.ID,
	})
}

func (s *Server) handleSessionMessages(w http.ResponseWriter, r *http.Request, sessionID string) {
	ctx := r.Context()

	// Use raw DB query to get Parts as JSON string which preserves types
	// If we used s.app.Messages.List, it would unmarshal Parts into ContentPart interfaces
	// which lose the "type" wrapper when re-marshaled to JSON (unless we implemented custom marshaler).
	// By returning the raw DB string, the frontend can parse it and see the types.
	messages, err := s.app.Queries.ListMessagesBySession(ctx, sessionID)
	if err != nil {
		http.Error(w, "Failed to list messages: "+err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(messages)
}

func (s *Server) handleSessionContextStats(w http.ResponseWriter, r *http.Request, sessionID string) {
	ctx := r.Context()
	query := r.URL.Query()

	mode := strings.TrimSpace(query.Get("mode"))
	if mode == "" {
		mode = "normal"
	}

	enableSubagents := true
	if raw := strings.TrimSpace(query.Get("enable_subagents")); raw != "" {
		parsed, err := strconv.ParseBool(raw)
		if err != nil {
			http.Error(w, "Invalid enable_subagents value", http.StatusBadRequest)
			return
		}
		enableSubagents = parsed
	}

	targetAgentName := config.AgentNormal
	targetAgentService := s.selectAgentService(config.AgentNormal, enableSubagents)
	if mode == "coding" {
		targetAgentName = config.AgentCoder
		targetAgentService = s.selectAgentService(config.AgentCoder, enableSubagents)
	} else if mode == "autonomous" {
		targetAgentName = config.AgentLife
		targetAgentService = s.selectAgentService(config.AgentLife, enableSubagents)
	}

	if targetAgentService == nil {
		http.Error(w, "Agent not initialized", http.StatusInternalServerError)
		return
	}

	model := strings.TrimSpace(query.Get("model"))
	if err := validateRequestedChatModel(model); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if model != "" {
		modelCandidates := routing.ResolveAgentModelCandidates(targetAgentName, model, s.app.Config.Agents)
		if len(modelCandidates) > 0 {
			ctx = context.WithValue(ctx, tools.ModelOverrideCandidatesContextKey, modelCandidates)
		}
		if explicitModel := resolveExplicitRequestedChatModel(model); explicitModel != "" {
			ctx = context.WithValue(ctx, tools.ModelOverrideContextKey, explicitModel)
		}
	}
	ctx = context.WithValue(ctx, tools.SessionIDContextKey, sessionID)

	statsProvider, ok := targetAgentService.(sessionContextStatsProvider)
	if !ok {
		http.Error(w, "Agent does not expose context stats", http.StatusInternalServerError)
		return
	}

	stats, err := statsProvider.BuildSessionContextStats(ctx, sessionID, mode)
	if err != nil {
		http.Error(w, "Failed to build context stats: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(stats)
}

func (s *Server) handleSessionPlanSnapshot(w http.ResponseWriter, r *http.Request, sessionID string) {
	metadata, ok, err := tools.LoadActivePlanSnapshotMetadata(sessionID)
	if err != nil {
		http.Error(w, "Failed to load session plan snapshot: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if !ok {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	payload, ok := parsePlanSnapshotMetadata(metadata)
	if !ok {
		http.Error(w, "Failed to parse session plan snapshot", http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(payload)
}
