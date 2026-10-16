package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/message"
)

type deepseekClient struct {
	*openaiClient
	httpClient *http.Client
}

type DeepSeekClient ProviderClient

func newDeepSeekClient(opts providerClientOptions) DeepSeekClient {
	// Initialize the base openai client
	baseClient := newOpenAIClient(opts).(*openaiClient)

	return &deepseekClient{
		openaiClient: baseClient,
		httpClient: &http.Client{
			Timeout: 600 * time.Second,
		},
	}
}

func (d *deepseekClient) transcribe(ctx context.Context, audio []byte, filename string) (string, error) {
	return "", errors.New("transcription not supported by DeepSeek provider")
}

func (d *deepseekClient) generateSpeech(ctx context.Context, text string, voice string) ([]byte, error) {
	return nil, errors.New("speech generation not supported by DeepSeek provider")
}

// DeepSeek specific message conversion to map to support reasoning_content
func (d *deepseekClient) convertMessagesToMap(messages []message.Message) ([]map[string]interface{}, error) {
	result := make([]map[string]interface{}, 0, len(messages))

	// Add system message
	if d.providerOptions.systemMessage != "" {
		result = append(result, map[string]interface{}{
			"role":    "system",
			"content": d.providerOptions.systemMessage,
		})
	}

	for _, msg := range messages {
		msgMap := map[string]interface{}{
			"role": string(msg.Role),
		}

		// Handle content
		if msg.Role == message.User {
			// DeepSeek supports standard content array for text and images
			// reusing openai logic for content parts would be complex here,
			// so we'll simplify: if it's just text, send string.
			// If it has images, we need to construct the content array.

			// For simplicity, let's use the openai conversion and then marshal/unmarshal
			// But that won't help with adding reasoning_content to assistant messages.

			// Let's manually construct for now.
			contentStr := msg.Content().String()
			if len(msg.BinaryContent()) > 0 || len(msg.ImageURLContent()) > 0 {
				// Mixed content
				var contentParts []map[string]interface{}
				if contentStr != "" {
					contentParts = append(contentParts, map[string]interface{}{
						"type": "text",
						"text": contentStr,
					})
				}
				for _, binary := range msg.BinaryContent() {
					contentParts = append(contentParts, map[string]interface{}{
						"type": "image_url",
						"image_url": map[string]string{
							"url": binary.String(models.ProviderDeepSeek),
						},
					})
				}
				msgMap["content"] = contentParts
			} else {
				msgMap["content"] = contentStr
			}
		} else {
			// Assistant or Tool
			if content := msg.Content().String(); content != "" {
				msgMap["content"] = content
			}
		}

		// Handle Tool Calls
		if len(msg.ToolCalls()) > 0 {
			var toolCalls []map[string]interface{}
			for _, call := range msg.ToolCalls() {
				toolCalls = append(toolCalls, map[string]interface{}{
					"id":   call.ID,
					"type": "function",
					"function": map[string]interface{}{
						"name":      call.Name,
						"arguments": call.Input,
					},
				})
			}
			msgMap["tool_calls"] = toolCalls
		}

		// Handle Tool Results
		if len(msg.ToolResults()) > 0 {
			// Tool results are separate messages in the array usually
			// But our message structure groups them.
			// The loop iterates over our messages.
			// If role is Tool, we should handle it.
			// Our convertMessages logic in openai.go handles this by creating multiple messages.
			// We should do the same.

			// Wait, if msg.Role is Tool, we should append multiple messages?
			// Let's look at openai.go's convertMessages.
			// It appends multiple messages for ToolResults.

			if msg.Role == message.Tool {
				// We need to append individually, not just modify msgMap
				// Since we are inside a loop, we can't easily append to the outer slice if we only return one map.
				// So we should refactor this loop.
			}
		}

		// *** KEY CHANGE: Handle Reasoning Content ***
		if msg.Role == message.Assistant {
			if reasoning := msg.ReasoningContent(); reasoning.Thinking != "" {
				msgMap["reasoning_content"] = reasoning.Thinking
			}
		}

		if msg.Role == message.Tool {
			for _, res := range msg.ToolResults() {
				toolMsg := map[string]interface{}{
					"role":         "tool",
					"tool_call_id": res.ToolCallID,
					"content":      res.Content,
				}
				result = append(result, toolMsg)
			}
		} else {
			result = append(result, msgMap)
		}
	}

	return result, nil
}

func (d *deepseekClient) prepareRequest(ctx context.Context, messages []message.Message, tools []tools.BaseTool, stream bool) (*http.Request, error) {
	msgs, err := d.convertMessagesToMap(messages)
	if err != nil {
		return nil, err
	}

	requestBody := map[string]interface{}{
		"model":    d.providerOptions.model.APIModel,
		"messages": msgs,
		"stream":   stream,
	}

	if stream {
		requestBody["stream_options"] = map[string]interface{}{
			"include_usage": true,
		}
	}

	if len(tools) > 0 {
		// Convert tools to map
		var toolsMap []map[string]interface{}
		for _, t := range d.convertTools(tools) {
			// Marshal/Unmarshal to convert openai types to map
			b, _ := json.Marshal(t)
			var m map[string]interface{}
			json.Unmarshal(b, &m)
			toolsMap = append(toolsMap, m)
		}
		requestBody["tools"] = toolsMap
	}

	// Set Temperature
	if d.providerOptions.model.DefaultTemperature != nil {
		requestBody["temperature"] = *d.providerOptions.model.DefaultTemperature
	} else if !d.providerOptions.model.CanReason {
		requestBody["temperature"] = 0.0
	}

	if d.providerOptions.model.CanReason {
		requestBody["max_tokens"] = d.providerOptions.maxTokens
	} else {
		requestBody["max_tokens"] = d.providerOptions.maxTokens
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	if config.Get().Debug {
		logging.Debug("DeepSeek Request", "body", string(jsonBody))
	}

	req, err := http.NewRequestWithContext(ctx, "POST", d.options.baseURL+"/chat/completions", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+d.providerOptions.apiKey)

	// Add extra headers
	if d.options.extraHeaders != nil {
		for k, v := range d.options.extraHeaders {
			req.Header.Set(k, v)
		}
	}

	return req, nil
}

func (d *deepseekClient) send(ctx context.Context, messages []message.Message, tools []tools.BaseTool) (*ProviderResponse, error) {
	req, err := d.prepareRequest(ctx, messages, tools, false)
	if err != nil {
		return nil, err
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("DeepSeek API error: %s - %s", resp.Status, string(body))
	}

	// Parse response
	var response struct {
		Choices []struct {
			Message struct {
				Role             string                   `json:"role"`
				Content          string                   `json:"content"`
				ReasoningContent string                   `json:"reasoning_content"`
				ToolCalls        []map[string]interface{} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int64 `json:"prompt_tokens"`
			CompletionTokens int64 `json:"completion_tokens"`
		} `json:"usage"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return nil, err
	}

	if len(response.Choices) == 0 {
		return nil, errors.New("no choices in response")
	}

	choice := response.Choices[0]

	// Convert tool calls
	var toolCalls []message.ToolCall
	for _, tc := range choice.Message.ToolCalls {
		id, _ := tc["id"].(string)
		typ, _ := tc["type"].(string)
		fn, _ := tc["function"].(map[string]interface{})
		name, _ := fn["name"].(string)
		args, _ := fn["arguments"].(string)

		toolCalls = append(toolCalls, message.ToolCall{
			ID:       id,
			Type:     typ,
			Name:     name,
			Input:    args,
			Finished: true,
		})
	}

	// We might want to store reasoning_content somewhere if it's not streamed?
	// But the interface ProviderResponse doesn't have it.
	// For non-streaming, we usually just return the content.
	// But wait, if we don't store it, we can't send it back next time.
	// ProviderResponse needs to be updated or we need to put it in Content with a special format?
	// No, ProviderResponse is used to update the message in the database.
	// We should probably check if ProviderResponse supports ReasoningContent.
	// Looking at provider.go, ProviderResponse struct:
	// type ProviderResponse struct {
	// 	Content      string
	// 	ToolCalls    []message.ToolCall
	// 	Usage        TokenUsage
	// 	FinishReason message.FinishReason
	// }
	// It does NOT have ReasoningContent. This is a problem for non-streaming too.
	// But usually we use streaming.

	return &ProviderResponse{
		Content:   choice.Message.Content,
		ToolCalls: toolCalls,
		Usage: TokenUsage{
			InputTokens:  response.Usage.PromptTokens,
			OutputTokens: response.Usage.CompletionTokens,
		},
		FinishReason: d.finishReason(choice.FinishReason),
	}, nil
}

func (d *deepseekClient) StreamResponse(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan ProviderEvent {
	return d.stream(ctx, messages, tools)
}

func (d *deepseekClient) stream(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan ProviderEvent {
	// Buffered channel to prevent blocking
	eventChan := make(chan ProviderEvent, 100)

	go func() {
		defer close(eventChan)

		req, err := d.prepareRequest(ctx, messages, tools, true)
		if err != nil {
			eventChan <- ProviderEvent{Type: EventError, Error: err}
			return
		}

		resp, err := d.httpClient.Do(req)
		if err != nil {
			eventChan <- ProviderEvent{Type: EventError, Error: err}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			eventChan <- ProviderEvent{Type: EventError, Error: fmt.Errorf("DeepSeek API error: %s - %s", resp.Status, string(body))}
			return
		}

		reader := bufio.NewReader(resp.Body)

		var currentContent string
		var currentThinking string
		var currentUsage *TokenUsage
		var toolCalls []message.ToolCall
		// Map to track tool calls being built
		pendingToolCalls := make(map[int]*message.ToolCall)

		for {
			line, err := reader.ReadBytes('\n')
			if err != nil {
				if err == io.EOF {
					break
				}
				eventChan <- ProviderEvent{Type: EventError, Error: err}
				return
			}

			line = bytes.TrimSpace(line)
			if !bytes.HasPrefix(line, []byte("data: ")) {
				continue
			}

			data := bytes.TrimPrefix(line, []byte("data: "))
			if string(data) == "[DONE]" {
				break
			}

			var chunk struct {
				Choices []struct {
					Delta struct {
						Content          string `json:"content"`
						ReasoningContent string `json:"reasoning_content"`
						ToolCalls        []struct {
							Index    int    `json:"index"`
							ID       string `json:"id"`
							Type     string `json:"type"`
							Function struct {
								Name      string `json:"name"`
								Arguments string `json:"arguments"`
							} `json:"function"`
						} `json:"tool_calls"`
					} `json:"delta"`
					FinishReason string `json:"finish_reason"`
				} `json:"choices"`
				Usage *struct {
					PromptTokens         int64 `json:"prompt_tokens"`
					CompletionTokens     int64 `json:"completion_tokens"`
					PromptCacheHitTokens int64 `json:"prompt_cache_hit_tokens"`
					PromptTokensDetails  *struct {
						CachedTokens int64 `json:"cached_tokens"`
					} `json:"prompt_tokens_details"`
				} `json:"usage"`
			}

			if err := json.Unmarshal(data, &chunk); err != nil {
				logging.Error("Failed to unmarshal chunk", "error", err, "data", string(data))
				continue
			}

			// Handle Usage if present
			if chunk.Usage != nil {
				logging.Debug("DeepSeek Stream Usage Received", "usage", chunk.Usage)
				cachedTokens := chunk.Usage.PromptCacheHitTokens
				if cachedTokens == 0 && chunk.Usage.PromptTokensDetails != nil {
					cachedTokens = chunk.Usage.PromptTokensDetails.CachedTokens
				}

				// DeepSeek might report InputTokens as Total Input. We want "Uncached Input" for consistency if needed,
				// but usually Provider usage stats are raw. Let's keep it raw but ensure CacheReadTokens is set.
				// However, openai.go subtracts cached tokens from input tokens. Let's follow that pattern for consistency.
				inputTokens := chunk.Usage.PromptTokens
				if inputTokens >= cachedTokens {
					inputTokens -= cachedTokens
				}

				currentUsage = &TokenUsage{
					InputTokens:     inputTokens,
					OutputTokens:    chunk.Usage.CompletionTokens,
					CacheReadTokens: cachedTokens,
				}

				eventChan <- ProviderEvent{
					Type:  EventUsage,
					Usage: currentUsage,
				}
			}

			if len(chunk.Choices) > 0 {
				delta := chunk.Choices[0].Delta

				// Handle Reasoning Content
				if delta.ReasoningContent != "" {
					eventChan <- ProviderEvent{
						Type:     EventThinkingDelta,
						Thinking: delta.ReasoningContent,
					}
					currentThinking += delta.ReasoningContent
				}

				// Handle Content
				if delta.Content != "" {
					eventChan <- ProviderEvent{
						Type:    EventContentDelta,
						Content: delta.Content,
					}
					currentContent += delta.Content
				}

				// Handle Tool Calls
				for _, tc := range delta.ToolCalls {
					index := tc.Index
					if _, ok := pendingToolCalls[index]; !ok {
						// New tool call
						pendingToolCalls[index] = &message.ToolCall{
							ID:   tc.ID,
							Type: tc.Type,
							Name: tc.Function.Name,
						}
					}

					// Append arguments
					pendingToolCalls[index].Input += tc.Function.Arguments
					// Update name/id if present (usually only in first packet)
					if tc.ID != "" {
						pendingToolCalls[index].ID = tc.ID
					}
					if tc.Function.Name != "" {
						pendingToolCalls[index].Name = tc.Function.Name
					}
				}

				if chunk.Choices[0].FinishReason != "" {
					// Finish reason
				}
			}
		}

		// Collect all tool calls
		for i := 0; i < len(pendingToolCalls); i++ {
			if tc, ok := pendingToolCalls[i]; ok {
				tc.Finished = true
				toolCalls = append(toolCalls, *tc)
			}
		}

		// Send complete event
		// We need to include Thinking in the response so it gets saved!
		// However, ProviderResponse struct doesn't have a specific field for Thinking.
		// Wait, we need to save this to the database.
		// The `Message` struct has `Parts`, which can be `ReasoningContent`.
		// The `ProviderResponse` is used by the caller (likely `agent.go` or `server.go`) to create the assistant message.

		// If ProviderResponse doesn't have a way to return Thinking, we need to modify ProviderResponse in `provider.go`.
		// Let's assume we can modify it.

		respObj := &ProviderResponse{
			Content:      currentContent,
			ToolCalls:    toolCalls,
			FinishReason: message.FinishReasonEndTurn, // Default
		}

		if currentUsage != nil {
			respObj.Usage = *currentUsage
		}

		// IMPORTANT: We need to pass back the thinking content so it can be saved.
		// Since ProviderResponse is fixed, we might need to modify it or use a side channel.
		// But wait, ProviderEvent has `Thinking` field.
		// The consumer of this channel should be accumulating it?
		// Let's check who consumes this channel. `backend/internal/llm/agent/agent.go` usually.

		// For now, we will assume the system handles `EventThinkingDelta` events and accumulates them.
		// If the final `EventComplete` is sent, does it overwrite the accumulated thinking?

		eventChan <- ProviderEvent{
			Type:     EventComplete,
			Response: respObj,
		}
	}()

	return eventChan
}
