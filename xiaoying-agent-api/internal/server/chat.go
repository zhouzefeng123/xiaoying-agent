package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/agent"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/llm/provider"
	"github.com/auramate/auramate/internal/llm/routing"
	"github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/message"
	"github.com/auramate/auramate/internal/session"
	"github.com/auramate/auramate/internal/utils"
)

type ChatCompletionRequest struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	} `json:"messages"`
	Stream          bool   `json:"stream"`
	SessionID       string `json:"session_id"` // Optional: Reuse session
	EnableSubagents *bool  `json:"enable_subagents,omitempty"`
}

func (s *Server) selectAgentService(agentName config.AgentName, enableSubagents bool) agent.Service {
	switch agentName {
	case config.AgentCoder:
		if !enableSubagents && s.app.CoderAgentNoSubagents != nil {
			return s.app.CoderAgentNoSubagents
		}
		return s.app.CoderAgent
	case config.AgentLife:
		return s.app.LifeAgent
	default:
		if !enableSubagents && s.app.NormalAgentNoSubagents != nil {
			return s.app.NormalAgentNoSubagents
		}
		return s.app.NormalAgent
	}
}

func validateRequestedChatModel(model string) error {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return nil
	}

	switch trimmed {
	case "auto", "auramate", "aura-coder", "coder", "aura-life", "life", "aura-normal", "normal":
		return nil
	}

	if _, ok := models.SupportedModels[models.ModelID(trimmed)]; ok {
		return nil
	}

	return fmt.Errorf("所选模型 %q 未在当前后端实例中加载，已阻止回退到其他模型。请确认当前连接的是目标后端，并检查 /v1/models 或 /v1/config 的生效配置", trimmed)
}

func resolveExplicitRequestedChatModel(model string) string {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return ""
	}

	if _, ok := models.SupportedModels[models.ModelID(trimmed)]; ok {
		return trimmed
	}

	return ""
}

func (s *Server) handleChatCompletions(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req ChatCompletionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Sanitize all message content for encoding issues
	for i := range req.Messages {
		sanitized := utils.SanitizeUTF8(req.Messages[i].Content)
		if sanitized.WasModified {
			logging.Info("Sanitized HTTP message encoding", "index", i, "detected_type", sanitized.DetectedType)
		}
		req.Messages[i].Content = sanitized.Content
	}

	if err := validateRequestedChatModel(req.Model); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Select Agent
	plan := routing.ResolveHTTPChatPlan(req.Model, s.app.Config.Agents)
	agentName, mode, startLSP := plan.AgentName, plan.Mode, plan.StartLSP
	enableSubagents := req.EnableSubagents == nil || *req.EnableSubagents
	targetAgent := s.selectAgentService(agentName, enableSubagents)

	if startLSP {
		// Lazy start only the LSPs that match the language being handled.
		s.app.StartLSPClientsForLanguages(r.Context(), inferLSPLanguagesFromChatRequest(req, mode, req.Model, config.WorkingDirectory()))
	}

	if targetAgent == nil {
		http.Error(w, fmt.Sprintf("Agent %s not initialized", req.Model), http.StatusInternalServerError)
		return
	}

	// Create or Get Session
	ctx := r.Context()
	if len(plan.ModelCandidates) > 0 {
		ctx = context.WithValue(ctx, tools.ModelOverrideCandidatesContextKey, plan.ModelCandidates)
	}
	if explicitModel := resolveExplicitRequestedChatModel(req.Model); explicitModel != "" {
		ctx = context.WithValue(ctx, tools.ModelOverrideContextKey, explicitModel)
	}

	prompt := ""
	if len(req.Messages) > 0 {
		lastMsg := req.Messages[len(req.Messages)-1]
		if lastMsg.Role == "user" {
			prompt = lastMsg.Content
		}
	}

	var sess session.Session
	var err error

	if req.SessionID != "" {
		sess, err = s.app.Sessions.Get(ctx, req.SessionID)
		if err != nil {
			// Fallback to create if not found? Or error?
			// Let's create if not found for convenience in testing
			sess, err = s.app.Sessions.Create(ctx, sessionTitleFromUserMessage(prompt, string(agentName)))
		}
	} else {
		sess, err = s.app.Sessions.Create(ctx, sessionTitleFromUserMessage(prompt, string(agentName)))
	}

	if err != nil {
		http.Error(w, "Failed to create/get session: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if err := updatePlaceholderSessionTitleFromUserMessage(ctx, s.app.Sessions, sess.ID, prompt); err != nil {
		logging.Warn("Failed to update placeholder session title", "session_id", sess.ID, "error", err)
	}

	// Populate History if messages > 1 (excluding the last one which is prompt)
	if len(req.Messages) > 1 {
		// INBOUND HISTORY FORCE FOLDING (Server-Side Optimization)
		// Clients (like stateless Python scripts) send full history every time.
		// We MUST fold historical tool outputs here to prevent context explosion.

		historyMsgs := req.Messages[:len(req.Messages)-1]

		// Regex to find embedded tool outputs in text content
		// Pattern matches: \n[Tool Output: ...]: ... \n or similar variations
		// Since regex in Go is slow for large texts, we use simple string manipulation if possible
		// or pre-compile regex if needed. But here we just use a simple heuristic loop.

		for _, m := range historyMsgs {
			var role message.MessageRole
			switch m.Role {
			case "assistant":
				role = message.Assistant
			case "system":
				role = message.System
			case "tool":
				role = message.Tool
			default:
				role = message.User
			}

			content := m.Content

			// FOLDING LOGIC 1: Standard Tool Role
			if role == message.Tool && len(content) > 200 {
				originalLen := len(content)
				content = fmt.Sprintf("... [History Folded: Tool Output (%d chars) hidden. Agent has processed this.] ...", originalLen)
				logging.Info("Force folded inbound historical tool output (Role=Tool)", "original_len", originalLen)
			}

			// FOLDING LOGIC 2: Embedded Tool Output in Assistant Text (Common in streamed clients)
			// Look for markers like "[Tool Output]: " or "[Tool Output: name]: "
			// FIX: Removed strict dependency on leading newline (\n) to handle cases where text is concatenated differently.
			if role == message.Assistant && len(content) > 500 {
				// We look for large blocks starting with [Tool Output
				// Strategy: If we find a tool output marker, check the length of the segment following it.

				// Case A: Generic "[Tool Output]: "
				// Use a more flexible search
				if idx := strings.Index(content, "[Tool Output]: "); idx != -1 {
					// Check length of content after marker
					suffix := content[idx:]
					if len(suffix) > 300 {
						// Fold it!
						prefix := content[:idx]
						folded := fmt.Sprintf("\n[Tool Output]: ... [History Folded: %d chars hidden] ...", len(suffix))
						content = prefix + folded
						logging.Info("Force folded inbound embedded tool output (Generic)", "original_len", len(suffix))
					}
				}

				// Case B: Named "[Tool Output: name]: "
				// We split by "[Tool Output" instead of "\n[Tool Output"

				parts := strings.Split(content, "[Tool Output")
				if len(parts) > 1 {
					newContent := parts[0]
					for i := 1; i < len(parts); i++ {
						// Recover the split delimiter
						part := "[Tool Output" + parts[i]

						// Find the end of the header "]: "
						headerEnd := strings.Index(part, "]: ")
						if headerEnd != -1 {
							// This is a valid tool output block
							// header is part[:headerEnd+3]
							// body is part[headerEnd+3:]
							header := part[:headerEnd+3]
							body := part[headerEnd+3:]

							if len(body) > 200 {
								body = fmt.Sprintf("... [History Folded: %d chars hidden] ...\n", len(body))
								logging.Info("Force folded inbound embedded tool output (Named)", "original_len", len(body))
							}
							newContent += header + body
						} else {
							// Not a match, just append
							newContent += part
						}
					}
					content = newContent
				}
			}

			// FOLDING LOGIC 3: Massive User Input
			if role == message.User && len(content) > 2000 {
				originalLen := len(content)
				head := content[:500]
				content = fmt.Sprintf("%s\n... [History Folded: User Input (%d chars) hidden] ...", head, originalLen)
				logging.Info("Force folded inbound historical user input", "original_len", originalLen)
			}

			// Create Params
			parts := []message.ContentPart{message.TextContent{Text: content}}

			params := message.CreateMessageParams{
				Role:  role,
				Parts: parts,
				Model: models.ModelID(req.Model),
			}

			s.app.Messages.Create(ctx, sess.ID, params)
		}
	}

	// FIX: Allow empty prompt if it's a "Continue" action or session restoration.
	// However, agent.Run expects content.
	// If prompt is empty, we set a default placeholder to avoid "No user prompt found" error,
	// allowing the Agent's internal "Continue" logic (added in agent.go) to handle it.
	if prompt == "" {
		if req.SessionID != "" {
			// If session exists, empty prompt means "Continue"
			prompt = " " // Use space to bypass check, Agent will trim and see it's empty
		} else {
			// New session must have a prompt
			http.Error(w, "No user prompt found", http.StatusBadRequest)
			return
		}
	}

	// Broadcast User Message to Thought Stream (Real-time)
	s.Broadcast(Event{
		Type: "thought",
		Data: map[string]string{
			"type":    "observation",
			"content": fmt.Sprintf("User: %s", prompt),
			"source":  req.Model,
		},
	})

	// Run Agent
	// Mode "coding" is default for Coder, "chat" for others?
	// Agent.Run signature: Run(ctx, sessionID, content, mode, attachments...)

	// WAKE UP: Explicitly transition to Interactive state when user talks to ANY Agent
	// This ensures the cognitive loop (heartbeat, proactive checks) is active and UI updates.
	go s.app.Cognitive.TransitionTo(context.Background(), "interactive")

	eventChan, err := targetAgent.Run(ctx, sess.ID, prompt, mode)
	if err != nil {
		http.Error(w, "Failed to start agent: "+err.Error(), http.StatusInternalServerError)
		return
	}

	if !req.Stream {
		w.Header().Set("Content-Type", "application/json")

		var fullContent string
		var toolCalls []map[string]any
		var usageMap map[string]int64
		var contextStats any
		var lastEventType agent.AgentEventType
		var lastUsageSnapshot provider.TokenUsage
		var hasUsageSnapshot bool

		f, _ := os.OpenFile("debug_events.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		defer f.Close()

		for event := range eventChan {
			if f != nil {
				loggedContent := event.Content
				if event.Type == agent.AgentEventTypeError {
					loggedContent = agentEventErrorText(event)
				}
				fmt.Fprintf(f, "Event: %s, Content: %q\n", event.Type, loggedContent)
			}
			switch event.Type {
			case agent.AgentEventTypeToken:
				fullContent += event.Content

				// Broadcast Thought if present (simple heuristic)
				// Since we are buffering, we might as well broadcast tokens as "thoughts" if in Life mode
				// But extracting <thinking> is better.
				if strings.Contains(event.Content, "<thinking>") || strings.Contains(event.Content, "</thinking>") {
					s.Broadcast(Event{
						Type: "thought",
						Data: map[string]string{
							"type":    "thought",
							"content": event.Content, // Raw token for now, parsing is harder in non-streaming loop without buffer state
							"source":  req.Model,
						},
					})
				}
				lastEventType = event.Type

			case agent.AgentEventTypeToolUse:
				if event.ToolCall != nil {
					toolCalls = append(toolCalls, map[string]any{
						"id":   event.ToolCall.ID,
						"type": "function",
						"function": map[string]any{
							"name":      event.ToolCall.Name,
							"arguments": event.ToolCall.Input,
						},
					})

					// Broadcast Action
					s.Broadcast(Event{
						Type: "thought",
						Data: map[string]string{
							"type":    "action",
							"content": fmt.Sprintf("Calling %s(%s)", event.ToolCall.Name, event.ToolCall.Input),
							"source":  req.Model,
						},
					})
				}
				lastEventType = event.Type

			case agent.AgentEventTypeContextStats:
				contextStats = event.Data

			case agent.AgentEventTypeToolOutput:
				if lastEventType != agent.AgentEventTypeToolOutput {
					fullContent += "\n[Tool Output]: "
				}
				fullContent += event.Content

				// Broadcast Observation
				s.Broadcast(Event{
					Type: "thought",
					Data: map[string]string{
						"type":    "observation",
						"content": event.Content,
						"source":  req.Model,
					},
				})
				lastEventType = event.Type

			case agent.AgentEventTypeError:
				errMsg := agentEventErrorText(event)
				if lastEventType != agent.AgentEventTypeError {
					fullContent += "\n[Error]: "
				}
				fullContent += errMsg

				// Broadcast Error
				s.Broadcast(Event{
					Type: "thought",
					Data: map[string]string{
						"type":    "thought",
						"content": fmt.Sprintf("[Error] %s", errMsg),
						"source":  req.Model,
					},
				})
				lastEventType = event.Type
			}

			if event.Usage != nil {
				currentUsage := *event.Usage
				deltaUsage := currentUsage
				if hasUsageSnapshot {
					deltaUsage = provider.UsageDelta(lastUsageSnapshot, currentUsage)
				}
				hasUsageSnapshot = true
				lastUsageSnapshot = currentUsage

				logging.Info("DIAG KV-CACHE ACTUAL (non-stream)",
					"session", sess.ID,
					"prompt_tokens", currentUsage.PromptTokens(),
					"cache_hit_tokens", currentUsage.CacheReadTokens,
					"cache_miss_tokens", currentUsage.InputTokens,
					"completion_tokens", currentUsage.OutputTokens,
				)
				if req.Model == "aura-life" || req.Model == "life" {
					total := deltaUsage.TotalTokens()
					if total > 0 {
						if _, err := s.app.Cognitive.ConsumeLifeTokens(ctx, total); err != nil {
							logging.Error("Failed to consume Life mode tokens", "error", err)
						}
					}
				}
				if usageMap == nil {
					usageMap = make(map[string]int64)
				}
				usageMap["prompt_tokens"] = currentUsage.PromptTokens()
				usageMap["completion_tokens"] = currentUsage.OutputTokens
				usageMap["total_tokens"] = currentUsage.TotalTokens()
				usageMap["prompt_cache_hit_tokens"] = currentUsage.CacheReadTokens
				usageMap["prompt_cache_miss_tokens"] = currentUsage.InputTokens
			}
		}

		// Ensure final assistant content is visible in Internal Consciousness stream,
		// even in non-stream mode where token-level thought events may be sparse.
		if strings.TrimSpace(fullContent) != "" {
			s.Broadcast(Event{
				Type: "thought",
				Data: map[string]string{
					"type":    "thought",
					"content": fullContent,
					"source":  req.Model,
				},
			})
		}

		resp := map[string]any{
			"id":      sess.ID,
			"object":  "chat.completion",
			"created": time.Now().Unix(),
			"model":   req.Model,
			"choices": []map[string]any{
				{
					"index": 0,
					"message": map[string]any{
						"role":       "assistant",
						"content":    fullContent,
						"tool_calls": toolCalls,
					},
					"finish_reason": "stop",
				},
			},
			"usage":         usageMap,
			"context_stats": contextStats,
		}
		json.NewEncoder(w).Encode(resp)
		return
	}

	// Stream Response
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming not supported", http.StatusInternalServerError)
		return
	}

	var thinkingBuffer strings.Builder
	var isThinking bool

	var thoughtBuffer strings.Builder
	var lastThoughtFlush = time.Now()
	const thoughtFlushInterval = 800 * time.Millisecond
	const thoughtFlushMinChars = 160
	var lastUsageSnapshot provider.TokenUsage
	var hasUsageSnapshot bool

	var tokenBuffer strings.Builder
	var lastTokenFlush = time.Now()
	const tokenFlushInterval = 32 * time.Millisecond

	// Asynchronous broadcast queue to avoid blocking SSE streaming loop.
	broadcastQueue := make(chan Event, 256)
	enqueueBroadcast := func(evt Event) {
		select {
		case broadcastQueue <- evt:
		default:
			logging.Warn("Dropping broadcast event due to full queue", "type", evt.Type, "session_id", evt.SessionID)
		}
	}
	defer close(broadcastQueue)
	go func() {
		for evt := range broadcastQueue {
			s.Broadcast(evt)
		}
	}()

	flushTokenBuffer := func() {
		if tokenBuffer.Len() == 0 {
			return
		}
		chunk := map[string]any{
			"id":      sess.ID,
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   req.Model,
			"choices": []map[string]any{
				{
					"index": 0,
					"delta": map[string]any{
						"content": tokenBuffer.String(),
					},
				},
			},
		}
		data, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", data)
		flusher.Flush()
		tokenBuffer.Reset()
		lastTokenFlush = time.Now()
	}

	flushThoughtBuffer := func() {
		if thoughtBuffer.Len() == 0 {
			return
		}
		content := strings.TrimSpace(thoughtBuffer.String())
		if content == "" {
			thoughtBuffer.Reset()
			lastThoughtFlush = time.Now()
			return
		}
		enqueueBroadcast(Event{
			Type: "thought",
			Data: map[string]string{
				"type":    "thought",
				"content": content,
				"source":  req.Model,
			},
		})
		thoughtBuffer.Reset()
		lastThoughtFlush = time.Now()
	}

	for event := range eventChan {
		chunk := map[string]any{
			"id":      sess.ID,
			"object":  "chat.completion.chunk",
			"created": time.Now().Unix(),
			"model":   req.Model,
			"choices": []map[string]any{
				{
					"index": 0,
					"delta": map[string]any{},
				},
			},
		}

		delta := chunk["choices"].([]map[string]any)[0]["delta"].(map[string]any)

		switch event.Type {
		case agent.AgentEventTypeToken:
			if event.Content != "" {
				tokenBuffer.WriteString(event.Content)
				// Real-time Thought Broadcasting
				token := event.Content
				if strings.Contains(token, "<thinking>") {
					isThinking = true
					token = strings.ReplaceAll(token, "<thinking>", "")
				}

				if isThinking {
					if strings.Contains(token, "</thinking>") {
						isThinking = false
						parts := strings.Split(token, "</thinking>")
						if len(parts) > 0 {
							thinkingBuffer.WriteString(parts[0])
						}

						if thinkingBuffer.Len() > 0 {
							thoughtBuffer.WriteString(thinkingBuffer.String())
							thinkingBuffer.Reset()
							if thoughtBuffer.Len() >= thoughtFlushMinChars || time.Since(lastThoughtFlush) >= thoughtFlushInterval {
								flushThoughtBuffer()
							}
						}
					} else {
						thinkingBuffer.WriteString(token)
						// Broadcast on newline to create stream effect in UI
						if strings.Contains(token, "\n") {
							thoughtBuffer.WriteString(thinkingBuffer.String())
							thinkingBuffer.Reset()
							if thoughtBuffer.Len() >= thoughtFlushMinChars || time.Since(lastThoughtFlush) >= thoughtFlushInterval {
								flushThoughtBuffer()
							}
						}
					}
				}

				// Ensure token gets flushed immediately for critical paths or at intervals
				if time.Since(lastTokenFlush) >= tokenFlushInterval {
					flushTokenBuffer()
				}
			}
		case agent.AgentEventTypeResponse:
			// Final response, maybe redundant if tokens are streamed?
			// Just ignore or handle if needed.
		case agent.AgentEventTypeToolUse:
			flushTokenBuffer()
			flushThoughtBuffer()
			if event.ToolCall != nil && strings.TrimSpace(event.ToolCall.Name) != "" && strings.TrimSpace(event.ToolCall.Input) != "" {
				delta["tool_calls"] = []map[string]any{
					{
						"index": event.ToolCall.Index,
						"id":    event.ToolCall.ID,
						"type":  "function",
						"function": map[string]any{
							"name":      event.ToolCall.Name,
							"arguments": event.ToolCall.Input,
						},
					},
				}

				// Broadcast Action
				enqueueBroadcast(Event{
					Type: "thought",
					Data: map[string]string{
						"type":    "action",
						"content": fmt.Sprintf("Calling %s(%s)", event.ToolCall.Name, event.ToolCall.Input),
						"source":  req.Model,
					},
				})
			}
		case agent.AgentEventTypeToolOutput:
			flushTokenBuffer()
			flushThoughtBuffer()
			// Stream tool output as content so we can see it in logs
			content := event.Content
			wordActions, cleanedContent := extractWordEditorSignals(content)
			wordActionErrors := make([]string, 0, len(wordActions))
			if len(wordActions) > 0 {
				for _, wordAction := range wordActions {
					if err := s.broadcastWordEditorAction(req.SessionID, wordAction); err != nil {
						logging.Warn("Blocked invalid word editor action from tool output", "session_id", req.SessionID, "error", err)
						wordActionErrors = append(wordActionErrors, err.Error())
					}
				}
				content = cleanedContent
				if len(wordActionErrors) > 0 {
					if strings.TrimSpace(content) == "" {
						content = strings.Join(wordActionErrors, "\n")
					} else {
						content += "\n" + strings.Join(wordActionErrors, "\n")
					}
				}
			}
			if content != "" {
				delta["content"] = fmt.Sprintf("\n[Tool Output]: %s\n", content)

				// Broadcast Observation
				enqueueBroadcast(Event{
					Type: "thought",
					Data: map[string]string{
						"type":    "observation",
						"content": content,
						"source":  req.Model,
					},
				})
			}
		case agent.AgentEventTypeToolResult:
			flushTokenBuffer()
			flushThoughtBuffer()
			// Handle structured ToolResult event
			if event.ToolResult != nil && event.ToolResult.Content != "" {
				content := event.ToolResult.Content
				wordActions, cleanedContent := extractWordEditorSignals(content)
				wordActionErrors := make([]string, 0, len(wordActions))
				if len(wordActions) > 0 {
					for _, wordAction := range wordActions {
						if err := s.broadcastWordEditorAction(req.SessionID, wordAction); err != nil {
							logging.Warn("Blocked invalid word editor action from tool result", "session_id", req.SessionID, "error", err)
							wordActionErrors = append(wordActionErrors, err.Error())
						}
					}
					content = cleanedContent
					if len(wordActionErrors) > 0 {
						if strings.TrimSpace(content) == "" {
							content = strings.Join(wordActionErrors, "\n")
						} else {
							content += "\n" + strings.Join(wordActionErrors, "\n")
						}
					}
				}
				if strings.TrimSpace(content) == "" {
					break
				}
				// If it's an error, prefix it
				if event.ToolResult.IsError {
					content = fmt.Sprintf("Error: %s", content)
				}
				delta["content"] = fmt.Sprintf("\n[Tool Output: %s]: %s\n", event.ToolResult.Name, content)

				// Broadcast Observation
				enqueueBroadcast(Event{
					Type: "thought",
					Data: map[string]string{
						"type":    "observation",
						"content": fmt.Sprintf("[%s] %s", event.ToolResult.Name, content),
						"source":  req.Model,
					},
				})
			}
		case agent.AgentEventTypeError:
			flushTokenBuffer()
			flushThoughtBuffer()
			errMsg := agentEventErrorText(event)
			delta["content"] = fmt.Sprintf("\n[Error]: %s\n", errMsg)

			// Broadcast Error
			enqueueBroadcast(Event{
				Type: "thought",
				Data: map[string]string{
					"type":    "thought",
					"content": fmt.Sprintf("[Error] %s", errMsg),
					"source":  req.Model,
				},
			})
		case agent.AgentEventTypeProgress:
			flushTokenBuffer()
			flushThoughtBuffer()
			enqueueBroadcast(Event{
				Type:      EventTypeProgress,
				Data:      event.Data,
				SessionID: sess.ID,
			})
		case agent.AgentEventTypeContextStats:
			flushTokenBuffer()
			flushThoughtBuffer()
			logging.Info("DEBUG: Received Context Stats Event", "stats", event.Data)
			chunk["context_stats"] = event.Data
		case agent.AgentEventTypeUsage:
			flushTokenBuffer()
			flushThoughtBuffer()
			if event.Usage != nil {
				currentUsage := *event.Usage
				deltaUsage := currentUsage
				if hasUsageSnapshot {
					deltaUsage = provider.UsageDelta(lastUsageSnapshot, currentUsage)
				}
				hasUsageSnapshot = true
				lastUsageSnapshot = currentUsage

				if req.Model == "aura-life" || req.Model == "life" {
					total := deltaUsage.TotalTokens()
					if total > 0 {
						if _, err := s.app.Cognitive.ConsumeLifeTokens(ctx, total); err != nil {
							logging.Error("Failed to consume Life mode tokens", "error", err)
						}
					}
				}
				logging.Debug("Chat handler sending usage", "usage", event.Usage)
				logging.Info("DIAG KV-CACHE ACTUAL",
					"session", sess.ID,
					"prompt_tokens", currentUsage.PromptTokens(),
					"cache_hit_tokens", currentUsage.CacheReadTokens,
					"cache_miss_tokens", currentUsage.InputTokens,
					"completion_tokens", currentUsage.OutputTokens,
				)
				// Standard OpenAI Usage Format
				// FIX: prompt_tokens should include cached tokens to match total_tokens expectation
				// and API behavior. InputTokens in our struct is "Net Input" (Uncached).
				promptTokens := currentUsage.PromptTokens()
				chunk["usage"] = map[string]any{
					"prompt_tokens":     promptTokens,
					"completion_tokens": currentUsage.OutputTokens,
					"total_tokens":      currentUsage.TotalTokens(),
					"prompt_tokens_details": map[string]any{
						"cached_tokens": currentUsage.CacheReadTokens,
					},
					"prompt_cache_hit_tokens":  currentUsage.CacheReadTokens,
					"prompt_cache_miss_tokens": currentUsage.InputTokens,
				}
				// Aliyun/OpenAI standard: usage is usually sent in the final chunk before [DONE]
				// or with an empty delta.
				// Since we are already initializing empty delta above, this is fine.
			}
		}

		if len(delta) > 0 || chunk["context_stats"] != nil || chunk["usage"] != nil {
			data, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}
	}

	// Make sure everything is flushed
	flushTokenBuffer()
	flushThoughtBuffer()

	// SOTA: Give a tiny bit of time for asynchronous channel events to drain before sending DONE
	time.Sleep(100 * time.Millisecond)

	// Try one more time to flush anything that might have been queued right at the end
	flushTokenBuffer()
	flushThoughtBuffer()

	fmt.Fprintf(w, "data: [DONE]\n\n")
	flusher.Flush()
}
