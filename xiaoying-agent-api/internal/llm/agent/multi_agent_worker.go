package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/auramate/auramate/internal/llm/provider"
	"github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/message"
	"github.com/auramate/auramate/internal/pubsub"
	"github.com/google/uuid"
)

type workerTask struct {
	ctx     context.Context
	replyTo string
	taskID  string
}

type worker struct {
	id              string
	role            string
	systemPrompt    string
	provider        provider.Provider
	runtimeObserver *Orchestrator
	bus             *pubsub.Broker[MultiAgentEvent]
	history         []message.Message
	tools           []tools.BaseTool
	taskQueue       chan workerTask
	cancel          context.CancelFunc
	mu              sync.Mutex
	busy            bool
	lastResult      string
}

const (
	workerProviderRetryLimit = 2
	workerOutputRetryLimit   = 2
)

func compactWorkerResult(full string) string {
	trimmed := strings.TrimSpace(full)
	if trimmed == "" {
		return ""
	}
	if start := strings.Index(trimmed, "<summary>"); start != -1 {
		if end := strings.Index(trimmed, "</summary>"); end != -1 && end > start {
			return strings.TrimSpace(trimmed[start+9 : end])
		}
	}
	const maxResultChars = 6000
	if len(trimmed) <= maxResultChars {
		return trimmed
	}
	const headChars = 3600
	const tailChars = 1800
	return strings.TrimSpace(trimmed[:headChars] + "\n\n[... worker result truncated ...]\n\n" + trimmed[len(trimmed)-tailChars:])
}

func buildArtifactValidationMessage(missing []string) string {
	if len(missing) == 0 {
		return ""
	}
	var builder strings.Builder
	builder.WriteString("SYSTEM VALIDATION: 你在 <worker_result> 中声明的以下 artifact 当前不存在，不能视为任务已完成：\n")
	for _, artifact := range missing {
		builder.WriteString("- ")
		builder.WriteString(artifact)
		builder.WriteString("\n")
	}
	builder.WriteString("请继续同一任务：若这些文件应当存在，立即调用合适的文件工具完成落盘；若无法生成，请返回 status=failed 或 partial，并在 summary 中说明原因。只有当声明的 artifact 真实存在时，才能返回 status=completed。")
	return builder.String()
}

func isRetryableWorkerProviderError(err error) (string, bool) {
	if err == nil {
		return "", false
	}
	lower := strings.ToLower(err.Error())
	markers := []struct {
		marker string
		kind   string
	}{
		{"rate limit", "rate_limit"},
		{"timeout", "timeout"},
		{"temporarily unavailable", "temporary_unavailable"},
		{"connection refused", "connection_error"},
		{"connection reset", "connection_error"},
		{"eof", "connection_error"},
		{"502", "server_error"},
		{"503", "server_error"},
		{"504", "server_error"},
		{"overloaded", "server_error"},
	}
	for _, marker := range markers {
		if strings.Contains(lower, marker.marker) {
			return marker.kind, true
		}
	}
	return "provider_error", false
}

func buildWorkerRetryMessage(kind string, err error, outputLimit bool) string {
	if outputLimit {
		return buildOutputLimitRecoveryText(true, false)
	}
	errText := "provider request failed"
	if err != nil {
		errText = normalizeWorkerSummaryText(err.Error(), 220)
	}
	return "SYSTEM WARNING: The previous provider call failed with a retryable error (" + kind + "): " + errText + ". Continue the SAME task from the current state. Do not repeat prior output. If the remaining output may be large, reduce it to a concise summary or write artifacts to files and report only the file paths."
}

func publishWorkerTerminalMessage(w *worker, replyTo, taskID string, content string) {
	if strings.TrimSpace(content) != "" {
		w.bus.Publish(pubsub.EventType(MAEventMessage), MultiAgentEvent{
			ID:        uuid.New().String(),
			Type:      MAEventMessage,
			From:      w.id,
			To:        replyTo,
			TaskID:    taskID,
			Content:   content,
			Timestamp: time.Now(),
		})
	}
	w.bus.Publish(pubsub.EventType(MAEventTurnComplete), MultiAgentEvent{
		ID:        uuid.New().String(),
		Type:      MAEventTurnComplete,
		From:      w.id,
		To:        replyTo,
		TaskID:    taskID,
		Timestamp: time.Now(),
	})
}

func isMutatingWorkerTool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "edit", "patch", "write", "batch_write", "delete", "run_command", "bash", "cleanup", "manage_plan", "manage_schedule", "schedule_task", "stop_command":
		return true
	default:
		return false
	}
}

func appendWorkerSystemMessage(w *worker, msgs []message.Message, text string) []message.Message {
	msg := message.NewTextMessage(message.System, text)
	w.mu.Lock()
	w.history = append(w.history, msg)
	w.mu.Unlock()
	return append(msgs, msg)
}

func (w *worker) failTask(replyTo, taskID, failureType, summary string, retryable bool, accumulatedResult *strings.Builder, providerRetries, outputRetries int) {
	failurePayload := buildWorkerFailureEnvelope(summary, failureType, retryable)
	if accumulatedResult.Len() > 0 {
		accumulatedResult.WriteString("\n")
	}
	accumulatedResult.WriteString(failurePayload)
	if w.runtimeObserver != nil {
		w.runtimeObserver.recordWorkerFailure(w.id, failureType, summary, retryable, providerRetries, outputRetries)
		w.runtimeObserver.recordWorkerResult(w.id, failurePayload)
		w.runtimeObserver.finishWorkerTask(w.id, failurePayload)
	}
	publishWorkerTerminalMessage(w, replyTo, taskID, failurePayload)
}

func newWorker(id string, role string, systemPrompt string, p provider.Provider, bus *pubsub.Broker[MultiAgentEvent], tools []tools.BaseTool) *worker {
	return &worker{
		id:           id,
		role:         role,
		systemPrompt: systemPrompt,
		provider:     p,
		bus:          bus,
		tools:        tools,
		taskQueue:    make(chan workerTask, 10), // Buffer size 10
		history:      make([]message.Message, 0),
	}
}

func (w *worker) ID() string {
	return w.id
}

func (w *worker) Role() string {
	return w.role
}

func (w *worker) IsBusy() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.busy || len(w.taskQueue) > 0
}

func (w *worker) GetLastResult() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.lastResult
}

func (w *worker) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	w.cancel = cancel

	// Subscribe to the bus
	sub := w.bus.Subscribe(ctx)
	logging.Info("Agent started", "id", w.id, "role", w.role)

	// Initial system message
	w.history = append(w.history, message.Message{
		Role: message.System,
		Parts: []message.ContentPart{
			message.TextContent{Text: w.systemPrompt},
		},
	})

	go func() {
		defer cancel()
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-sub:
				if !ok {
					return
				}
				w.processEvent(ctx, event)
			}
		}
	}()

	// Start task processor
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case task := <-w.taskQueue:
				w.reply(task)
			}
		}
	}()

	return nil
}

func (w *worker) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	// Explicitly clear history to help GC release memory for long-running processes
	w.mu.Lock()
	w.history = nil
	w.mu.Unlock()
}

func (w *worker) processEvent(ctx context.Context, event pubsub.Event[MultiAgentEvent]) {
	payload := event.Payload

	// Ignore messages from self
	if payload.From == w.id {
		return
	}

	// Filter messages: only process if addressed to me or broadcast (*)
	if payload.To != w.id && payload.To != "*" {
		return
	}

	// For broadcast messages, apply stricter filtering to reduce context bloat
	if payload.To == "*" {
		contentLower := strings.ToLower(payload.Content)
		roleLower := strings.ToLower(w.role)

		// Only accept broadcast if it mentions my role or is a general announcement
		// This prevents every worker from storing every other worker's output
		if !strings.Contains(contentLower, roleLower) &&
			!strings.Contains(contentLower, "everyone") &&
			!strings.Contains(contentLower, "all agents") {
			return
		}
	}

	logging.Info("Agent received message", "agent", w.id, "from", payload.From, "content", payload.Content)

	// Add to history
	w.mu.Lock()
	w.history = append(w.history, message.Message{
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: fmt.Sprintf("[From %s]: %s", payload.From, payload.Content)},
		},
	})
	w.mu.Unlock()

	// Trigger LLM response only if directly addressed or if it's a broadcast that implies a request
	// For simplicity, we reply to everything addressed to us.
	shouldReply := payload.To == w.id

	if !shouldReply && payload.To == "*" {
		// Heuristic: check if content mentions my role (case-insensitive)
		// e.g. "Researcher, please check..."
		contentLower := strings.ToLower(payload.Content)
		roleLower := strings.ToLower(w.role)

		// Simple check: if my role name appears in the broadcast message, I should pay attention
		if strings.Contains(contentLower, roleLower) {
			shouldReply = true
		}

		// Also check if content explicitly mentions "Worker" and I am a worker
		// NOTE: We now use worker-N roles. The logic below is for legacy generic workers.
		// For worker-N, the strings.Contains(contentLower, roleLower) check above handles it.
		// e.g. role="worker-1", content="worker-1, please..." -> match.
		if w.role == "worker" && strings.Contains(contentLower, "worker") {
			shouldReply = true
		}
	}

	if shouldReply {
		// Use a longer context for multi-turn task execution
		// Note: We use the parent context, not the event loop context, to avoid cancellation if event loop blocks
		// But here we are just pushing to queue.
		w.taskQueue <- workerTask{ctx: ctx, replyTo: payload.From, taskID: payload.TaskID}
	}
}

func (w *worker) GetHistory() []message.Message {
	w.mu.Lock()
	defer w.mu.Unlock()
	msgs := make([]message.Message, len(w.history))
	copy(msgs, w.history)
	return msgs
}

// optimizeContext applies transient optimizations to the message history
// to reduce token usage without modifying the permanent history.
// It performs:
// 1. CoT (Chain of Thought) stripping from older Assistant messages.
// 2. Tool output compression (truncation) for large outputs.
func (w *worker) optimizeContext(history []message.Message) []message.Message {
	// Create new slice to avoid modifying original
	optimized := make([]message.Message, 0, len(history))

	for i, msg := range history {
		// Deep copy message (especially Parts)
		newMsg := msg
		if len(msg.Parts) > 0 {
			newMsg.Parts = make([]message.ContentPart, len(msg.Parts))
			copy(newMsg.Parts, msg.Parts)
		}

		// 1. CoT Stripping (reuse reThinking from agent.go)
		// Keep only last 2 turns (approx 4 messages) intact
		if msg.Role == message.Assistant && i < len(history)-4 {
			content := msg.Content().String()
			cleanContent := reThinking.ReplaceAllString(content, "")
			if len(cleanContent) != len(content) {
				preservedParts := []message.ContentPart{
					message.TextContent{Text: cleanContent},
				}
				for _, p := range msg.Parts {
					if _, ok := p.(message.TextContent); !ok {
						preservedParts = append(preservedParts, p)
					}
				}
				newMsg.Parts = preservedParts
			}
		}

		// 2. Tool Output Compression (reuse truncateToolOutput from agent_context.go)
		if msg.Role == message.Tool {
			newParts := make([]message.ContentPart, 0, len(newMsg.Parts))
			for _, p := range newMsg.Parts {
				switch v := p.(type) {
				case message.TextContent:
					truncated := tools.TruncateOutput(v.Text)
					newParts = append(newParts, message.TextContent{Text: truncated})
				case message.ToolResult:
					// ToolResult content might be large
					if len(v.Content) > 0 {
						truncated := tools.TruncateOutput(v.Content)
						// Create new ToolResult with truncated content
						newParts = append(newParts, message.ToolResult{
							ToolCallID: v.ToolCallID,
							Name:       v.Name,
							Content:    truncated,
							IsError:    v.IsError,
						})
					} else {
						newParts = append(newParts, v)
					}
				default:
					newParts = append(newParts, p)
				}
			}
			newMsg.Parts = newParts
		}

		optimized = append(optimized, newMsg)
	}
	return optimized
}

func (w *worker) generateProviderResponse(ctx context.Context, msgs []message.Message) (*provider.ProviderResponse, error) {
	eventChan := w.provider.StreamResponse(ctx, msgs, w.tools)
	if eventChan == nil {
		logging.Debug("Worker provider stream unavailable; falling back to non-streaming response", "worker", w.id)
		return w.provider.SendMessages(ctx, msgs, w.tools)
	}

	resp, err := collectWorkerStreamResponse(ctx, eventChan)
	if err != nil {
		return nil, err
	}
	return resp, nil
}

func collectWorkerStreamResponse(ctx context.Context, eventChan <-chan provider.ProviderEvent) (*provider.ProviderResponse, error) {
	var content strings.Builder
	var thinking strings.Builder
	var latestUsage provider.TokenUsage
	hasUsage := false
	sawEvent := false
	streamMsg := message.Message{Role: message.Assistant}

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case event, ok := <-eventChan:
			if !ok {
				if !sawEvent {
					return nil, fmt.Errorf("worker provider stream closed without events")
				}
				resp := provider.ProviderResponse{
					Content:      content.String(),
					Thinking:     thinking.String(),
					ToolCalls:    finalizedWorkerStreamToolCalls(streamMsg.ToolCalls()),
					FinishReason: message.FinishReasonEndTurn,
				}
				if hasUsage {
					resp.Usage = latestUsage
				}
				if len(resp.ToolCalls) > 0 {
					resp.FinishReason = message.FinishReasonToolUse
				}
				return &resp, nil
			}

			sawEvent = true
			switch event.Type {
			case provider.EventContentDelta:
				content.WriteString(event.Content)
			case provider.EventThinkingDelta:
				thinking.WriteString(event.Thinking)
			case provider.EventUsage:
				if event.Usage != nil {
					latestUsage = *event.Usage
					hasUsage = true
				}
			case provider.EventToolUseStart, provider.EventToolUseDelta:
				if event.ToolCall != nil {
					streamMsg.AppendToolCallDelta(event.ToolCall.Index, event.ToolCall.ID, event.ToolCall.Name, event.ToolCall.Input)
				}
			case provider.EventError:
				if event.Error != nil {
					return nil, event.Error
				}
				return nil, fmt.Errorf("worker provider stream returned an error event")
			case provider.EventComplete:
				resp := provider.ProviderResponse{}
				if event.Response != nil {
					resp = *event.Response
				}
				if resp.Content == "" {
					resp.Content = content.String()
				}
				if resp.Thinking == "" {
					resp.Thinking = thinking.String()
				}
				if len(resp.ToolCalls) == 0 {
					resp.ToolCalls = finalizedWorkerStreamToolCalls(streamMsg.ToolCalls())
				}
				if hasUsage && resp.Usage == (provider.TokenUsage{}) {
					resp.Usage = latestUsage
				}
				if len(resp.ToolCalls) > 0 {
					resp.FinishReason = message.FinishReasonToolUse
				} else if resp.FinishReason == "" {
					resp.FinishReason = message.FinishReasonEndTurn
				}
				return &resp, nil
			}
		}
	}
}

func finalizedWorkerStreamToolCalls(toolCalls []message.ToolCall) []message.ToolCall {
	if len(toolCalls) == 0 {
		return nil
	}
	finalized := make([]message.ToolCall, 0, len(toolCalls))
	for _, tc := range toolCalls {
		if tc.Type == "" {
			tc.Type = "function"
		}
		tc.Finished = true
		finalized = append(finalized, tc)
	}
	return finalized
}

func (w *worker) reply(task workerTask) {
	ctx := task.ctx
	replyTo := task.replyTo
	taskID := task.taskID
	if replyTo == "" {
		replyTo = "*"
	}

	// Prevent concurrent replies for the same worker
	w.mu.Lock()
	w.busy = true
	w.lastResult = ""
	msgs := make([]message.Message, len(w.history))
	copy(msgs, w.history)
	w.mu.Unlock()

	var accumulatedResult strings.Builder

	defer func() {
		finalResult := compactWorkerResult(accumulatedResult.String())
		w.mu.Lock()
		w.busy = false
		w.lastResult = finalResult
		w.mu.Unlock()
		if w.runtimeObserver != nil && finalResult != "" {
			w.runtimeObserver.recordWorkerResult(w.id, accumulatedResult.String())
		}
	}()

	// Use a longer context for multi-turn task execution
	// We derive from task.ctx (worker context) so that if worker stops, task stops.
	genCtx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()

	// Inject session context for tools (e.g. write tool requires session_id)
	genCtx = tools.SetContextValues(genCtx, w.id, "swarm-exec")
	genCtx = tools.SetImplicitPlanReuse(genCtx, false)

	heartbeatCtx, heartbeatCancel := context.WithCancel(genCtx)
	defer heartbeatCancel()
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				w.bus.Publish(pubsub.EventType(MAEventHeartbeat), MultiAgentEvent{
					ID:        uuid.New().String(),
					Type:      MAEventHeartbeat,
					From:      w.id,
					To:        replyTo,
					TaskID:    taskID,
					Content:   "heartbeat",
					Timestamp: time.Now(),
				})
			}
		}
	}()

	// Limit the number of turns to prevent infinite loops
	maxTurns := 10
	currentTurn := 0
	providerRetries := 0
	outputRetries := 0
	if w.runtimeObserver != nil {
		w.runtimeObserver.resetWorkerExecutionState(w.id)
	}

	for {
		currentTurn++
		if currentTurn > maxTurns {
			logging.Error("Worker reached max turns limit", "worker", w.id)
			errMsg := fmt.Sprintf("worker reached maximum turn limit (%d)", maxTurns)
			w.failTask(replyTo, taskID, "max_turns_exceeded", errMsg, false, &accumulatedResult, providerRetries, outputRetries)
			break
		}

		// Apply Context Optimization before sending to LLM
		// This ensures we don't send massive history or redundant CoT to the provider
		contextMsgs := w.optimizeContext(msgs)

		resp, err := w.generateProviderResponse(genCtx, contextMsgs)
		if err != nil {
			logging.Error("Agent failed to generate response", "agent", w.id, "error", err)
			failureType, retryable := isRetryableWorkerProviderError(err)
			if isLikelyOutputLimitError(err) {
				failureType = "output_limit"
				retryable = true
				if outputRetries < workerOutputRetryLimit {
					outputRetries++
					recovery := buildWorkerRetryMessage(failureType, err, true)
					msgs = appendWorkerSystemMessage(w, msgs, recovery)
					if w.runtimeObserver != nil {
						w.runtimeObserver.recordWorkerRetry(w.id, failureType, err.Error(), recovery, true, providerRetries, outputRetries)
					}
					continue
				}
			} else if retryable && providerRetries < workerProviderRetryLimit {
				providerRetries++
				recovery := buildWorkerRetryMessage(failureType, err, false)
				msgs = appendWorkerSystemMessage(w, msgs, recovery)
				if w.runtimeObserver != nil {
					w.runtimeObserver.recordWorkerRetry(w.id, failureType, err.Error(), recovery, true, providerRetries, outputRetries)
				}
				continue
			}
			w.failTask(replyTo, taskID, failureType, fmt.Sprintf("worker provider failed: %v", err), retryable, &accumulatedResult, providerRetries, outputRetries)
			break
		}

		// Create assistant message
		assistantMsg := message.Message{
			Role: message.Assistant,
		}

		if resp.Thinking != "" {
			assistantMsg.Parts = append(assistantMsg.Parts, message.ReasoningContent{Thinking: resp.Thinking})
		}

		if resp.Content != "" {
			if w.runtimeObserver != nil {
				w.runtimeObserver.markWorkerProgress(w.id)
			}
			if accumulatedResult.Len() > 0 {
				accumulatedResult.WriteString("\n")
			}
			accumulatedResult.WriteString(resp.Content)

			assistantMsg.Parts = append(assistantMsg.Parts, message.TextContent{Text: resp.Content})

			// Publish content
			w.bus.Publish(pubsub.EventType(MAEventMessage), MultiAgentEvent{
				ID:        uuid.New().String(),
				Type:      MAEventMessage,
				From:      w.id,
				To:        replyTo,
				TaskID:    taskID,
				Content:   resp.Content,
				Usage:     &resp.Usage,
				Timestamp: time.Now(),
			})
		} else if len(resp.ToolCalls) == 0 {
			// No content and no tool calls - avoid infinite loop
			logging.Warn("Worker generated empty response without tool calls", "worker", w.id)
			w.failTask(replyTo, taskID, "empty_response", "worker provider returned empty response without tool calls", false, &accumulatedResult, providerRetries, outputRetries)
			break
		}

		for _, tc := range resp.ToolCalls {
			assistantMsg.Parts = append(assistantMsg.Parts, tc)
		}

		// Update history and local msgs
		w.mu.Lock()
		w.history = append(w.history, assistantMsg)
		w.mu.Unlock()
		msgs = append(msgs, assistantMsg)

		if resp.FinishReason == message.FinishReasonMaxTokens {
			if structured, ok := parseWorkerResultEnvelope(resp.Content); ok && len(missingAbsoluteArtifacts(structured)) == 0 {
				if w.runtimeObserver != nil {
					w.runtimeObserver.recordWorkerResult(w.id, resp.Content)
				}
			} else if outputRetries < workerOutputRetryLimit {
				outputRetries++
				recovery := buildOutputLimitRecoveryText(true, false)
				msgs = appendWorkerSystemMessage(w, msgs, recovery)
				if w.runtimeObserver != nil {
					w.runtimeObserver.recordWorkerRetry(w.id, "output_limit", "finish_reason=max_tokens", recovery, true, providerRetries, outputRetries)
				}
				continue
			} else {
				w.failTask(replyTo, taskID, "output_limit", "worker output remained truncated after retry budget was exhausted", true, &accumulatedResult, providerRetries, outputRetries)
				break
			}
		}

		if len(resp.ToolCalls) == 0 {
			if structured, ok := parseWorkerResultEnvelope(resp.Content); ok {
				if missing := missingAbsoluteArtifacts(structured); len(missing) > 0 {
					validationMsg := message.NewTextMessage(message.System, buildArtifactValidationMessage(missing))
					w.mu.Lock()
					w.history = append(w.history, validationMsg)
					w.mu.Unlock()
					msgs = append(msgs, validationMsg)
					continue
				}
			}
		}

		if len(resp.ToolCalls) > 0 {
			// Notify about tool calls
			if w.runtimeObserver != nil {
				w.runtimeObserver.markWorkerProgress(w.id)
			}
			w.bus.Publish(pubsub.EventType(MAEventToolCall), MultiAgentEvent{
				ID:        uuid.New().String(),
				Type:      MAEventToolCall,
				From:      w.id,
				To:        "*",
				TaskID:    taskID,
				ToolCalls: resp.ToolCalls,
				Timestamp: time.Now(),
			})

			// Create tool results message
			toolMsg := message.Message{
				Role: message.Tool,
			}

			truncatedToolCall := false
			for _, tc := range resp.ToolCalls {
				if truncationMessage, ok := truncatedToolCallError(tc.Input); ok {
					truncatedToolCall = true
					content := buildOutputLimitRecoveryText(false, true) + " Original provider notice: " + truncationMessage
					tr := message.ToolResult{ToolCallID: tc.ID, Name: tc.Name, Content: content, IsError: true}
					toolMsg.Parts = append(toolMsg.Parts, tr)
					w.bus.Publish(pubsub.EventType(MAEventToolResult), MultiAgentEvent{ID: uuid.New().String(), Type: MAEventToolResult, From: w.id, To: replyTo, TaskID: taskID, ToolResult: &tr, Timestamp: time.Now()})
				}
			}
			if truncatedToolCall {
				outputRetries++
				if w.runtimeObserver != nil {
					w.runtimeObserver.recordWorkerRetry(w.id, "output_limit", "tool call arguments truncated", buildOutputLimitRecoveryText(false, true), true, providerRetries, outputRetries)
				}
				w.mu.Lock()
				w.history = append(w.history, toolMsg)
				w.mu.Unlock()
				msgs = append(msgs, toolMsg)
				if outputRetries > workerOutputRetryLimit {
					w.failTask(replyTo, taskID, "output_limit", "worker tool call arguments kept being truncated after retry budget was exhausted", true, &accumulatedResult, providerRetries, outputRetries)
					break
				}
				continue
			}

			type toolResultContainer struct {
				idx    int
				tc     message.ToolCall
				result string
				err    error
			}

			runToolCall := func(index int, toolCall message.ToolCall) toolResultContainer {

				var tool tools.BaseTool
				for _, t := range w.tools {
					if t.Info().Name == toolCall.Name {
						tool = t
						break
					}
				}

				var result string
				var err error

				if tool != nil {
					tcInput := tools.ToolCall{
						ID:    toolCall.ID,
						Name:  toolCall.Name,
						Input: toolCall.Input,
					}
					// Execute tool
					toolResp, runErr := tool.Run(genCtx, tcInput)
					if runErr != nil {
						logging.Error("Worker tool execution failed", "worker", w.id, "tool", toolCall.Name, "error", runErr)
						result = fmt.Sprintf("Error executing tool: %v", runErr)
						err = runErr
					} else {
						logging.Info("Worker tool execution success", "worker", w.id, "tool", toolCall.Name)
						result = toolResp.Content
						if toolResp.IsError {
							err = fmt.Errorf("tool error")
						}
					}
				} else {
					result = fmt.Sprintf("Tool not found: %s", toolCall.Name)
					err = fmt.Errorf("tool not found")
				}

				return toolResultContainer{
					idx:    index,
					tc:     toolCall,
					result: result,
					err:    err,
				}
			}

			results := make([]toolResultContainer, len(resp.ToolCalls))
			var parallelCalls []struct {
				index int
				tc    message.ToolCall
			}
			for i, tc := range resp.ToolCalls {
				if isMutatingWorkerTool(tc.Name) {
					results[i] = runToolCall(i, tc)
					continue
				}
				parallelCalls = append(parallelCalls, struct {
					index int
					tc    message.ToolCall
				}{index: i, tc: tc})
			}
			if len(parallelCalls) > 0 {
				resultChan := make(chan toolResultContainer, len(parallelCalls))
				var wg sync.WaitGroup
				for _, call := range parallelCalls {
					wg.Add(1)
					go func(index int, toolCall message.ToolCall) {
						defer wg.Done()
						resultChan <- runToolCall(index, toolCall)
					}(call.index, call.tc)
				}
				wg.Wait()
				close(resultChan)
				for r := range resultChan {
					results[r.idx] = r
				}
			}

			for _, r := range results {
				finalResult := r.result
				if r.err != nil {
					finalResult = fmt.Sprintf("Error: %v\nOutput: %s", r.err, r.result)
				}

				// Accumulate tool result into final output
				if accumulatedResult.Len() > 0 {
					accumulatedResult.WriteString("\n")
				}
				accumulatedResult.WriteString(fmt.Sprintf("[Tool Result: %s]\n%s", r.tc.Name, tools.TruncateOutput(finalResult)))

				tr := message.ToolResult{
					ToolCallID: r.tc.ID,
					Name:       r.tc.Name,
					Content:    finalResult,
					IsError:    r.err != nil,
				}
				toolMsg.Parts = append(toolMsg.Parts, tr)

				// Publish tool result
				w.bus.Publish(pubsub.EventType(MAEventToolResult), MultiAgentEvent{
					ID:         uuid.New().String(),
					Type:       MAEventToolResult,
					From:       w.id,
					To:         replyTo,
					TaskID:     taskID,
					ToolResult: &tr,
					Timestamp:  time.Now(),
				})
				if w.runtimeObserver != nil {
					w.runtimeObserver.recordWorkerToolExecution(w.id, r.tc.Name, r.tc.Input, finalResult, r.err != nil)
					if r.err != nil {
						w.runtimeObserver.recordWorkerToolRecovery(w.id, r.tc.Name, finalResult)
					}
				}
			}

			// Update history and local msgs
			w.mu.Lock()
			w.history = append(w.history, toolMsg)
			w.mu.Unlock()
			msgs = append(msgs, toolMsg)

			// Continue loop to get next response from LLM
			continue
		}

		// No tool calls, finished this turn
		// Emit TurnComplete event
		logging.Info("Worker publishing TurnComplete", "worker", w.id, "to", replyTo)
		if w.runtimeObserver != nil {
			w.runtimeObserver.finishWorkerTask(w.id, accumulatedResult.String())
		}
		w.bus.Publish(pubsub.EventType(MAEventTurnComplete), MultiAgentEvent{
			ID:        uuid.New().String(),
			Type:      MAEventTurnComplete,
			From:      w.id,
			To:        replyTo,
			TaskID:    taskID,
			Timestamp: time.Now(),
		})
		break
	}
}
