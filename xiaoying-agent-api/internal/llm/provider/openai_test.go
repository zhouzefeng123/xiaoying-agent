package provider

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/message"
	"github.com/openai/openai-go"
)

type aliyunPayloadTestTool struct{}

func (aliyunPayloadTestTool) Info() tools.ToolInfo {
	return tools.ToolInfo{Name: "test_tool"}
}

func (aliyunPayloadTestTool) Run(ctx context.Context, params tools.ToolCall) (tools.ToolResponse, error) {
	return tools.NewTextResponse("ok"), nil
}

func TestAppendStreamingToolCallDeltasEmitsStartOnce(t *testing.T) {
	events := make(chan ProviderEvent, 4)
	active := map[int]*toolCallAccumulator{}

	appendStreamingToolCallDeltas([]streamingToolCallDelta{{Index: 0, ID: "call-1"}}, active, events)
	select {
	case event := <-events:
		t.Fatalf("did not expect event before tool name is known, got %v", event.Type)
	default:
	}

	appendStreamingToolCallDeltas([]streamingToolCallDelta{{Index: 0, Name: "edit"}}, active, events)
	select {
	case event := <-events:
		if event.Type != EventToolUseStart {
			t.Fatalf("expected tool use start, got %v", event.Type)
		}
		if event.ToolCall == nil || event.ToolCall.ID != "call-1" || event.ToolCall.Name != "edit" {
			t.Fatalf("unexpected tool call payload: %#v", event.ToolCall)
		}
		if event.ToolCall.Input != "" {
			t.Fatalf("expected early tool start to have empty input, got %q", event.ToolCall.Input)
		}
	default:
		t.Fatalf("expected a tool use start event once id and name are known")
	}

	appendStreamingToolCallDeltas([]streamingToolCallDelta{{Index: 0, Arguments: `{"file_path":"backend/main.go"}`}}, active, events)
	select {
	case event := <-events:
		if event.Type != EventToolUseDelta {
			t.Fatalf("expected tool use delta, got %v", event.Type)
		}
		if event.ToolCall == nil || event.ToolCall.Input != `{"file_path":"backend/main.go"}` {
			t.Fatalf("unexpected delta payload: %#v", event.ToolCall)
		}
	default:
		t.Fatalf("expected a tool use delta event when arguments stream in")
	}

	select {
	case event := <-events:
		t.Fatalf("expected no duplicate tool start after delta, got %v", event.Type)
	default:
	}

	finalToolCalls := finalizeStreamingToolCalls(active)
	if len(finalToolCalls) != 1 {
		t.Fatalf("expected 1 final tool call, got %d", len(finalToolCalls))
	}
	if finalToolCalls[0].Input != `{"file_path":"backend/main.go"}` {
		t.Fatalf("unexpected aggregated tool input: %q", finalToolCalls[0].Input)
	}
}

func TestAppendStreamingToolCallDeltasSeparatesIndexlessCompleteCalls(t *testing.T) {
	events := make(chan ProviderEvent, 8)
	active := map[int]*toolCallAccumulator{}

	appendStreamingToolCallDeltas([]streamingToolCallDelta{{
		Index:     0,
		Name:      "ls",
		Arguments: `{"path":"workspace"}`,
	}}, active, events)
	appendStreamingToolCallDeltas([]streamingToolCallDelta{{
		Index:     0,
		Name:      "ls",
		Arguments: `{"path":"repo","pattern":"**/README.md"}`,
	}}, active, events)

	finalToolCalls := finalizeStreamingToolCalls(active)
	if len(finalToolCalls) != 2 {
		t.Fatalf("expected 2 final tool calls, got %d", len(finalToolCalls))
	}

	if finalToolCalls[0].Input != `{"path":"workspace"}` {
		t.Fatalf("unexpected first tool input: %q", finalToolCalls[0].Input)
	}
	if finalToolCalls[1].Input != `{"path":"repo","pattern":"**/README.md"}` {
		t.Fatalf("unexpected second tool input: %q", finalToolCalls[1].Input)
	}
}

func TestAppendStreamingToolCallDeltasRoutesIndexlessFollowUpToLatestIncompleteCall(t *testing.T) {
	events := make(chan ProviderEvent, 8)
	active := map[int]*toolCallAccumulator{}

	appendStreamingToolCallDeltas([]streamingToolCallDelta{{
		Index:     0,
		Name:      "ls",
		Arguments: `{"path":"workspace"}`,
	}}, active, events)
	appendStreamingToolCallDeltas([]streamingToolCallDelta{{
		Index: 0,
		Name:  "read",
	}}, active, events)
	appendStreamingToolCallDeltas([]streamingToolCallDelta{{
		Index:     0,
		Arguments: `{"file_path":"backend/`,
	}}, active, events)
	appendStreamingToolCallDeltas([]streamingToolCallDelta{{
		Index:     0,
		Arguments: `README.md"}`,
	}}, active, events)

	finalToolCalls := finalizeStreamingToolCalls(active)
	if len(finalToolCalls) != 2 {
		t.Fatalf("expected 2 final tool calls, got %d", len(finalToolCalls))
	}

	if finalToolCalls[1].Name != "read" {
		t.Fatalf("expected second tool to be read, got %q", finalToolCalls[1].Name)
	}
	if finalToolCalls[1].Input != `{"file_path":"backend/README.md"}` {
		t.Fatalf("unexpected second tool input: %q", finalToolCalls[1].Input)
	}
}

func TestBuildAliyunCompatiblePayloadSkipsIncompleteAssistantToolCalls(t *testing.T) {
	client := newOpenAIClient(providerClientOptions{}).(*openaiClient)
	messages := []message.Message{
		{
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.ToolCall{ID: "tool-partial", Index: 0, Name: "grep", Input: `{"pattern":"README`, Type: "function", Finished: false},
			},
		},
		{
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.ToolCall{ID: "tool-final", Index: 1, Name: "ls", Input: `{"path":"workspace"}`, Type: "function", Finished: true},
			},
		},
	}

	payload := client.buildAliyunCompatiblePayload(messages, nil, false)
	encodedMessages, ok := payload["messages"].([]map[string]interface{})
	if !ok {
		t.Fatalf("expected encoded messages slice, got %#v", payload["messages"])
	}

	assistantMessagesWithToolCalls := make([]map[string]interface{}, 0, len(encodedMessages))
	for _, item := range encodedMessages {
		if item["role"] != "assistant" || item["tool_calls"] == nil {
			continue
		}
		assistantMessagesWithToolCalls = append(assistantMessagesWithToolCalls, item)
	}

	if len(assistantMessagesWithToolCalls) != 1 {
		t.Fatalf("expected only one assistant tool-call history message, got %d", len(assistantMessagesWithToolCalls))
	}

	toolCalls, ok := assistantMessagesWithToolCalls[0]["tool_calls"].([]map[string]interface{})
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("expected exactly one serialized tool call, got %#v", assistantMessagesWithToolCalls[0]["tool_calls"])
	}

	function, ok := toolCalls[0]["function"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected function payload, got %#v", toolCalls[0])
	}
	if function["arguments"] != `{"path":"workspace"}` {
		t.Fatalf("unexpected serialized arguments: %#v", function["arguments"])
	}
}

// TestOpenAI_Stream_EmptyChoices verifies that the provider handles
// an empty choices response gracefully without panicking.
func TestOpenAI_Stream_EmptyChoices(t *testing.T) {
	// Initialize config to avoid panic
	if _, err := config.Load(".", false); err != nil {
		// Ignore error as we just need cfg to be non-nil
		// But better to log it
		t.Logf("Config load warning: %v", err)
	}

	// 1. Setup Mock Server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify request
		if r.URL.Path != "/chat/completions" {
			t.Errorf("Expected path /chat/completions, got %s", r.URL.Path)
		}

		// Set headers for SSE
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")

		// Send a chunk with empty choices (or just DONE immediately without choices)
		// Simulating a case where the stream ends but no content was accrued?
		// Or a chunk with empty choices list?
		// The panic was `acc.ChatCompletion.Choices[0]`.
		// `acc` accumulates chunks.
		// If we send nothing but DONE, `acc` will be empty.
		// Let's send a chunk that has no choices, or just DONE.

		// Case 1: Send [DONE] immediately.
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	// 2. Setup Provider
	// We use the internal constructor or just NewProvider with options
	// But NewProvider parses config. We can use `newOpenAIClient` if exported, or just NewProvider with options.
	// `newOpenAIClient` is private.
	// `NewProvider` is public.

	opts := []ProviderClientOption{
		WithAPIKey("test-key"),
		WithModel(models.Model{
			ID:       "test-model",
			Provider: models.ProviderOpenAI,
			APIModel: "gpt-3.5-turbo",
		}),
		WithOpenAIOptions(WithOpenAIBaseURL(server.URL + "/")), // openai-go adds v1? No, usually BaseURL is root.
		// Wait, openai-go might append /chat/completions.
		// The mock server handles /chat/completions.
	}

	// We need to ensure the BaseURL is correctly propagated.
	// In `newOpenAIClient`: option.WithBaseURL(openaiOpts.baseURL)

	p, err := NewProvider(models.ProviderOpenAI, opts...)
	if err != nil {
		t.Fatalf("Failed to create provider: %v", err)
	}

	// 3. Run Stream
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	msgs := []message.Message{
		{
			Role: message.User,
			Parts: []message.ContentPart{
				message.TextContent{Text: "Hello"},
			},
		},
	}

	ch := p.StreamResponse(ctx, msgs, []tools.BaseTool{})

	// 4. Consume Channel
	for event := range ch {
		t.Logf("Received event: %v", event.Type)
		if event.Type == EventError {
			t.Logf("Error: %v", event.Error)
		}
		if event.Type == EventComplete {
			t.Log("Received Complete")
			if event.Response == nil {
				t.Error("Response is nil")
			} else {
				// Verify it's empty but valid
				if event.Response.FinishReason != message.FinishReasonEndTurn { // Changed from Stop to EndTurn
					t.Errorf("Expected EndTurn, got %s", event.Response.FinishReason)
				}
			}
		}
	}
}

func TestFindAliyunStableEndIndexSkipsTrailingDynamicSystemMessage(t *testing.T) {
	messages := []message.Message{
		{
			Role:  message.System,
			Parts: []message.ContentPart{message.TextContent{Text: "static prompt"}},
		},
		{
			Role:  message.System,
			Parts: []message.ContentPart{message.TextContent{Text: "project context"}},
		},
		{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: "hello"}},
		},
		{
			Role:  message.Assistant,
			Parts: []message.ContentPart{message.TextContent{Text: "ok"}},
		},
		{
			Role:  message.System,
			Parts: []message.ContentPart{message.TextContent{Text: "[TURN CONTEXT]\nfoo\n\n[SYSTEM INFO]\nbar"}},
		},
	}

	stableEndIndex := findAliyunStableEndIndex(messages, 2)
	if stableEndIndex != 3 {
		t.Fatalf("expected stable end index 3, got %d", stableEndIndex)
	}
}

func TestOpenAI_StreamAliyunMarksSystemPromptAndSkipsDynamicTailCheckpoint(t *testing.T) {
	if _, err := config.Load(".", false); err != nil {
		t.Logf("Config load warning: %v", err)
	}

	requestBodies := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("failed to parse request body: %v", err)
		}
		requestBodies <- payload

		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":1,\"total_tokens\":11,\"prompt_tokens_details\":{\"cached_tokens\":5}}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	client := newOpenAIClient(providerClientOptions{
		apiKey: "test-key",
		model: models.Model{
			ID:       models.QwenPlus,
			Name:     "Qwen Plus",
			Provider: models.ProviderAliyun,
			APIModel: "qwen3.5-plus",
		},
		openaiOptions: []OpenAIOption{WithOpenAIBaseURL(server.URL)},
	}).(*openaiClient)

	messages := []message.Message{
		{
			Role:  message.System,
			Parts: []message.ContentPart{message.TextContent{Text: "static prompt"}},
		},
		{
			Role:  message.System,
			Parts: []message.ContentPart{message.TextContent{Text: "project context"}},
		},
		{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: "hello"}},
		},
		{
			Role:  message.Assistant,
			Parts: []message.ContentPart{message.TextContent{Text: "stable answer"}},
		},
		{
			Role:  message.System,
			Parts: []message.ContentPart{message.TextContent{Text: "[TURN CONTEXT]\nfoo\n\n[SYSTEM INFO]\nbar"}},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	for range client.streamAliyun(ctx, messages, nil, true) {
	}

	var payload map[string]any
	select {
	case payload = <-requestBodies:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for captured request body")
	}

	rawMessages, ok := payload["messages"].([]any)
	if !ok {
		t.Fatalf("expected messages array, got %#v", payload["messages"])
	}
	if len(rawMessages) != 5 {
		t.Fatalf("expected 5 payload messages with separate leading system checkpoints, got %d", len(rawMessages))
	}

	firstMsg, ok := rawMessages[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first message object, got %#v", rawMessages[0])
	}
	firstContent, ok := firstMsg["content"].([]any)
	if !ok || len(firstContent) != 1 {
		t.Fatalf("expected checkpointed first system message, got %#v", firstMsg["content"])
	}
	firstPart, ok := firstContent[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first system content part object, got %#v", firstContent[0])
	}
	if _, ok := firstPart["cache_control"].(map[string]any); !ok {
		t.Fatalf("expected first leading system message to include cache_control, got %#v", firstPart)
	}

	secondMsg, ok := rawMessages[1].(map[string]any)
	if !ok {
		t.Fatalf("expected second message object, got %#v", rawMessages[1])
	}
	secondContent, ok := secondMsg["content"].([]any)
	if !ok || len(secondContent) != 1 {
		t.Fatalf("expected checkpointed second system message, got %#v", secondMsg["content"])
	}
	secondPart, ok := secondContent[0].(map[string]any)
	if !ok {
		t.Fatalf("expected second system content part object, got %#v", secondContent[0])
	}
	if _, ok := secondPart["cache_control"].(map[string]any); !ok {
		t.Fatalf("expected second leading system message to include cache_control, got %#v", secondPart)
	}

	stableMsg, ok := rawMessages[3].(map[string]any)
	if !ok {
		t.Fatalf("expected stable message object, got %#v", rawMessages[3])
	}
	stableContent, ok := stableMsg["content"].([]any)
	if !ok || len(stableContent) != 1 {
		t.Fatalf("expected stable message to be checkpointed array, got %#v", stableMsg["content"])
	}

	dynamicMsg, ok := rawMessages[4].(map[string]any)
	if !ok {
		t.Fatalf("expected dynamic tail message object, got %#v", rawMessages[4])
	}
	if _, ok := dynamicMsg["content"].([]any); ok {
		t.Fatalf("expected trailing dynamic context to remain uncached, got %#v", dynamicMsg["content"])
	}
}

func TestOpenAI_ShouldUseAliyunCompatiblePathUsesActualAPIModel(t *testing.T) {
	client := newOpenAIClient(providerClientOptions{
		model: models.Model{
			ID:       models.QwenPlus,
			Name:     "Qwen Plus",
			Provider: models.ProviderOpenAI,
			APIModel: "glm-5",
		},
		openaiOptions: []OpenAIOption{WithOpenAIBaseURL("https://coding.dashscope.aliyuncs.com/v1")},
	}).(*openaiClient)

	useCompatiblePath, enableCacheHeader := client.shouldUseAliyunCompatiblePath()
	if useCompatiblePath {
		t.Fatal("expected glm-5 wire model to skip Aliyun compatible path")
	}
	if enableCacheHeader {
		t.Fatal("expected cache header to remain disabled when compatible path is skipped")
	}
}

func TestOpenAI_ShouldUseAliyunCompatiblePathKeepsQwenCustomEndpoints(t *testing.T) {
	client := newOpenAIClient(providerClientOptions{
		model: models.Model{
			ID:       models.QwenPlus,
			Name:     "Qwen Plus",
			Provider: models.ProviderOpenAI,
			APIModel: "qwen3.5-plus",
		},
		openaiOptions: []OpenAIOption{WithOpenAIBaseURL("https://coding.dashscope.aliyuncs.com/v1")},
	}).(*openaiClient)

	useCompatiblePath, enableCacheHeader := client.shouldUseAliyunCompatiblePath()
	if !useCompatiblePath {
		t.Fatal("expected qwen custom endpoint to keep Aliyun compatible path")
	}
	if enableCacheHeader {
		t.Fatal("expected custom OpenAI endpoint not to enable Aliyun cache header")
	}
}

func TestOpenAI_StreamAliyunExtractsRawJSONErrorFromEOFFragment(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(`{"code":1403,"message":"quota exceeded"}`))
	}))
	defer server.Close()

	client := newOpenAIClient(providerClientOptions{
		apiKey: "test-key",
		model: models.Model{
			ID:       models.QwenPlus,
			Name:     "Qwen Plus",
			Provider: models.ProviderAliyun,
			APIModel: "qwen3.5-plus",
		},
		openaiOptions: []OpenAIOption{WithOpenAIBaseURL(server.URL)},
	}).(*openaiClient)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var gotErr error
	for event := range client.streamAliyun(ctx, []message.Message{{
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "hello"},
		},
	}}, nil, true) {
		if event.Type == EventError {
			gotErr = event.Error
		}
	}

	if gotErr == nil {
		t.Fatal("expected streamAliyun to surface a parsed raw JSON error")
	}
	if !strings.Contains(gotErr.Error(), "raw stream error 1403: quota exceeded") {
		t.Fatalf("unexpected error: %v", gotErr)
	}
}

func TestOpenAI_SendAliyunRetriesEOFAndSucceeds(t *testing.T) {
	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "0")
		if attempts < 3 {
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"summary ok","reasoning_content":"","tool_calls":[]},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"prompt_tokens_details":{"cached_tokens":0}}}`))
	}))
	defer server.Close()

	client := newOpenAIClient(providerClientOptions{
		apiKey: "test-key",
		model: models.Model{
			ID:       models.QwenPlus,
			Name:     "Qwen Plus",
			Provider: models.ProviderAliyun,
			APIModel: "qwen3.5-plus",
		},
		openaiOptions: []OpenAIOption{WithOpenAIBaseURL(server.URL)},
	}).(*openaiClient)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := client.sendAliyun(ctx, []message.Message{{
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "hello"},
		},
	}}, nil, true)
	if err != nil {
		t.Fatalf("expected EOF retries to recover, got %v", err)
	}
	if resp == nil || resp.Content != "summary ok" {
		t.Fatalf("unexpected response: %#v", resp)
	}
	if attempts != 3 {
		t.Fatalf("expected 3 attempts before success, got %d", attempts)
	}
}

func TestOpenAI_StreamAliyunExtractsSSEStringErrorFrame(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"error\":\"资源使用超限\"}\n\n"))
	}))
	defer server.Close()

	client := newOpenAIClient(providerClientOptions{
		apiKey: "test-key",
		model: models.Model{
			ID:       models.QwenPlus,
			Name:     "Qwen Plus",
			Provider: models.ProviderAliyun,
			APIModel: "qwen3.5-plus",
		},
		openaiOptions: []OpenAIOption{WithOpenAIBaseURL(server.URL)},
	}).(*openaiClient)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	var gotErr error
	for event := range client.streamAliyun(ctx, []message.Message{{
		Role: message.User,
		Parts: []message.ContentPart{
			message.TextContent{Text: "hello"},
		},
	}}, nil, true) {
		if event.Type == EventError {
			gotErr = event.Error
		}
	}

	if gotErr == nil {
		t.Fatal("expected SSE error frame to surface as provider error")
	}
	if !strings.Contains(gotErr.Error(), "raw stream error: 资源使用超限") {
		t.Fatalf("unexpected error: %v", gotErr)
	}
}

func TestBuildAliyunStreamErrorIgnoresNullFields(t *testing.T) {
	err := buildAliyunStreamError(403, `{"error":{"code":null,"message":null},"request_id":"req-1"}`)
	if err == nil {
		t.Fatal("expected buildAliyunStreamError to return an error")
	}
	if strings.Contains(err.Error(), "null: null") {
		t.Fatalf("expected null fields to be ignored, got %v", err)
	}
	if !strings.Contains(err.Error(), `{"error":{"code":null,"message":null},"request_id":"req-1"}`) {
		t.Fatalf("expected raw error payload fallback, got %v", err)
	}
}

func TestBuildAliyunStreamErrorAcceptsStringErrorField(t *testing.T) {
	err := buildAliyunStreamError(0, `{"error":"资源使用超限"}`)
	if err == nil {
		t.Fatal("expected buildAliyunStreamError to return an error")
	}
	if !strings.Contains(err.Error(), "raw stream error: 资源使用超限") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOpenAI_SendAliyunUsesCompatiblePayloadAndParsesCachedTokens(t *testing.T) {
	if _, err := config.Load(".", false); err != nil {
		t.Logf("Config load warning: %v", err)
	}

	requestBodies := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("failed to read request body: %v", err)
		}

		var payload map[string]any
		if err := json.Unmarshal(body, &payload); err != nil {
			t.Fatalf("failed to parse request body: %v", err)
		}
		requestBodies <- payload

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":120,"completion_tokens":8,"total_tokens":128,"prompt_tokens_details":{"cached_tokens":48}}}`))
	}))
	defer server.Close()

	client := newOpenAIClient(providerClientOptions{
		apiKey: "test-key",
		model: models.Model{
			ID:       models.QwenPlus,
			Name:     "Qwen Plus",
			Provider: models.ProviderAliyun,
			APIModel: "qwen3.5-plus",
		},
		openaiOptions: []OpenAIOption{WithOpenAIBaseURL(server.URL)},
	}).(*openaiClient)

	messages := []message.Message{
		{
			Role:  message.System,
			Parts: []message.ContentPart{message.TextContent{Text: "static prompt"}},
		},
		{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: "hello"}},
		},
		{
			Role:  message.Assistant,
			Parts: []message.ContentPart{message.TextContent{Text: "stable answer"}},
		},
		{
			Role:  message.System,
			Parts: []message.ContentPart{message.TextContent{Text: "[TURN CONTEXT]\nfoo\n\n[SYSTEM INFO]\nbar"}},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, err := client.send(ctx, messages, nil)
	if err != nil {
		t.Fatalf("send failed: %v", err)
	}
	if resp == nil {
		t.Fatal("expected response")
	}
	if resp.Usage.CacheReadTokens != 48 {
		t.Fatalf("expected 48 cached tokens, got %d", resp.Usage.CacheReadTokens)
	}
	if resp.Usage.InputTokens != 72 {
		t.Fatalf("expected 72 uncached input tokens, got %d", resp.Usage.InputTokens)
	}

	var payload map[string]any
	select {
	case payload = <-requestBodies:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for captured request body")
	}

	if stream, ok := payload["stream"].(bool); !ok || stream {
		t.Fatalf("expected non-stream payload, got %#v", payload["stream"])
	}

	rawMessages, ok := payload["messages"].([]any)
	if !ok {
		t.Fatalf("expected messages array, got %#v", payload["messages"])
	}
	if len(rawMessages) != 4 {
		t.Fatalf("expected 4 payload messages, got %d", len(rawMessages))
	}

	firstMsg, ok := rawMessages[0].(map[string]any)
	if !ok {
		t.Fatalf("expected first message object, got %#v", rawMessages[0])
	}
	firstContent, ok := firstMsg["content"].([]any)
	if !ok || len(firstContent) != 1 {
		t.Fatalf("expected single leading system content part, got %#v", firstMsg["content"])
	}
	firstPart, ok := firstContent[0].(map[string]any)
	if !ok {
		t.Fatalf("expected leading system content part object, got %#v", firstContent[0])
	}
	if _, ok := firstPart["cache_control"].(map[string]any); !ok {
		t.Fatalf("expected leading static system prompt to include cache_control, got %#v", firstPart)
	}

	dynamicMsg, ok := rawMessages[3].(map[string]any)
	if !ok {
		t.Fatalf("expected dynamic tail message object, got %#v", rawMessages[3])
	}
	if _, ok := dynamicMsg["content"].([]any); ok {
		t.Fatalf("expected trailing dynamic context to remain uncached, got %#v", dynamicMsg["content"])
	}
}

func TestOpenAIUsageFallsBackToPromptCacheHitTokens(t *testing.T) {
	var completion openai.ChatCompletion
	raw := []byte(`{"choices":[{"message":{"content":"OK"},"finish_reason":"stop"}],"usage":{"prompt_tokens":120,"completion_tokens":8,"prompt_cache_hit_tokens":48}}`)
	if err := json.Unmarshal(raw, &completion); err != nil {
		t.Fatalf("unmarshal completion: %v", err)
	}

	client := &openaiClient{}
	usage := client.usage(completion)
	if usage.CacheReadTokens != 48 {
		t.Fatalf("expected 48 cached tokens, got %d", usage.CacheReadTokens)
	}
	if usage.InputTokens != 72 {
		t.Fatalf("expected 72 uncached input tokens, got %d", usage.InputTokens)
	}
}

func TestOpenAI_BuildAliyunCompatiblePayloadSplitsStableAndVolatileLeadingSystemBlocks(t *testing.T) {
	client := newOpenAIClient(providerClientOptions{
		apiKey: "test-key",
		model: models.Model{
			ID:       models.QwenPlus,
			Name:     "Qwen Plus",
			Provider: models.ProviderAliyun,
			APIModel: "qwen3.5-plus",
		},
	}).(*openaiClient)

	messages := []message.Message{
		{
			Role:  message.System,
			Parts: []message.ContentPart{message.TextContent{Text: "static prompt"}},
		},
		{
			Role:  message.System,
			Parts: []message.ContentPart{message.TextContent{Text: "project context"}},
		},
		{
			Role:  message.System,
			Parts: []message.ContentPart{message.TextContent{Text: "[MEMORY CAPSULE]\nremember user preference"}},
		},
		{
			Role:  message.System,
			Parts: []message.ContentPart{message.TextContent{Text: "[WORKING STATE]\nCurrent focus: cache tuning"}},
		},
		{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: "hello"}},
		},
	}

	payload := client.buildAliyunCompatiblePayload(messages, nil, false)
	rawMessages, ok := payload["messages"].([]map[string]interface{})
	if !ok {
		t.Fatalf("expected typed messages slice, got %#v", payload["messages"])
	}
	if len(rawMessages) != 5 {
		t.Fatalf("expected 4 leading system messages plus one user message, got %d messages", len(rawMessages))
	}

	firstContent, ok := rawMessages[0]["content"].([]map[string]interface{})
	if !ok {
		t.Fatalf("expected first leading system message to be checkpointed, got %#v", rawMessages[0]["content"])
	}
	if len(firstContent) != 1 {
		t.Fatalf("expected first leading system message to contain one cached part, got %d", len(firstContent))
	}
	if _, ok := firstContent[0]["cache_control"].(map[string]interface{}); !ok {
		t.Fatalf("expected first stable leading message to be cached, got %#v", firstContent[0])
	}

	secondContent, ok := rawMessages[1]["content"].([]map[string]interface{})
	if !ok {
		t.Fatalf("expected second leading system message to be checkpointed, got %#v", rawMessages[1]["content"])
	}
	if len(secondContent) != 1 {
		t.Fatalf("expected second leading system message to contain one cached part, got %d", len(secondContent))
	}
	if _, ok := secondContent[0]["cache_control"].(map[string]interface{}); !ok {
		t.Fatalf("expected second stable leading message to be cached, got %#v", secondContent[0])
	}

	if _, ok := rawMessages[2]["content"].([]map[string]interface{}); ok {
		t.Fatalf("expected memory capsule message to remain uncached, got %#v", rawMessages[2]["content"])
	}
	if _, ok := rawMessages[3]["content"].([]map[string]interface{}); ok {
		t.Fatalf("expected working state message to remain uncached, got %#v", rawMessages[3]["content"])
	}
}

func TestOpenAI_BuildAliyunCompatiblePayloadDisablesThinkingForAliyunNonReasoningModel(t *testing.T) {
	client := newOpenAIClient(providerClientOptions{
		apiKey: "test-key",
		model: models.Model{
			ID:        models.Qwen36Plus,
			Name:      "Qwen 3.6 Plus",
			Provider:  models.ProviderAliyun,
			APIModel:  "qwen3.6-plus",
			CanReason: false,
		},
	}).(*openaiClient)

	payload := client.buildAliyunCompatiblePayload([]message.Message{{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "hello"}},
	}}, nil, false)

	if got, ok := payload["enable_thinking"].(bool); !ok || got {
		t.Fatalf("expected enable_thinking=false for non-reasoning Aliyun model, got %#v", payload["enable_thinking"])
	}
}

func TestOpenAI_BuildAliyunCompatiblePayloadDisablesThinkingForToolRequests(t *testing.T) {
	client := newOpenAIClient(providerClientOptions{
		apiKey: "test-key",
		model: models.Model{
			ID:        models.KimiK26,
			Name:      "kimi-k2.6",
			Provider:  models.ProviderAliyun,
			APIModel:  "kimi-k2.6",
			CanReason: true,
		},
	}).(*openaiClient)

	payload := client.buildAliyunCompatiblePayload([]message.Message{{
		Role:  message.User,
		Parts: []message.ContentPart{message.TextContent{Text: "write a file"}},
	}}, []tools.BaseTool{aliyunPayloadTestTool{}}, false)

	if got, ok := payload["enable_thinking"].(bool); !ok || got {
		t.Fatalf("expected enable_thinking=false for tool requests, got %#v", payload["enable_thinking"])
	}
}

func TestOpenAI_BuildAliyunCompatiblePayloadReplaysReasoningWhenThinkingEnabled(t *testing.T) {
	client := newOpenAIClient(providerClientOptions{
		apiKey: "test-key",
		model: models.Model{
			ID:        models.KimiK26,
			Name:      "kimi-k2.6",
			Provider:  models.ProviderAliyun,
			APIModel:  "kimi-k2.6",
			CanReason: true,
		},
	}).(*openaiClient)

	payload := client.buildAliyunCompatiblePayload([]message.Message{
		{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: "hello"}},
		},
		{
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.ReasoningContent{Thinking: "internal reasoning"},
				message.TextContent{Text: "visible answer"},
			},
		},
		{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: "continue"}},
		},
	}, nil, false)

	if got, ok := payload["enable_thinking"].(bool); !ok || !got {
		t.Fatalf("expected enable_thinking=true when reasoning replay is complete, got %#v", payload["enable_thinking"])
	}
	rawMessages := payload["messages"].([]map[string]interface{})
	if got := rawMessages[1]["reasoning_content"]; got != "internal reasoning" {
		t.Fatalf("expected reasoning_content to be replayed, got %#v", got)
	}
}

func TestOpenAI_BuildAliyunCompatiblePayloadReplaysReasoningEvenWhenThinkingDisabled(t *testing.T) {
	client := newOpenAIClient(providerClientOptions{
		apiKey: "test-key",
		model: models.Model{
			ID:        models.KimiK26,
			Name:      "kimi-k2.6",
			Provider:  models.ProviderAliyun,
			APIModel:  "kimi-k2.6",
			CanReason: true,
		},
	}).(*openaiClient)

	payload := client.buildAliyunCompatiblePayload([]message.Message{
		{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: "write a file"}},
		},
		{
			Role: message.Assistant,
			Parts: []message.ContentPart{
				message.ReasoningContent{Thinking: "internal reasoning"},
				message.TextContent{Text: "visible answer"},
			},
		},
		{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: "summarize"}},
		},
	}, []tools.BaseTool{aliyunPayloadTestTool{}}, false)

	if got, ok := payload["enable_thinking"].(bool); !ok || got {
		t.Fatalf("expected enable_thinking=false for tool requests, got %#v", payload["enable_thinking"])
	}
	rawMessages := payload["messages"].([]map[string]interface{})
	if got := rawMessages[1]["reasoning_content"]; got != "internal reasoning" {
		t.Fatalf("expected reasoning_content to be replayed even when thinking is disabled, got %#v", got)
	}
}

func TestOpenAI_BuildAliyunCompatiblePayloadMarksBoundaryBeforeLatestUserTurn(t *testing.T) {
	client := newOpenAIClient(providerClientOptions{
		apiKey: "test-key",
		model: models.Model{
			ID:       models.QwenPlus,
			Name:     "Qwen Plus",
			Provider: models.ProviderAliyun,
			APIModel: "qwen3.5-plus",
		},
	}).(*openaiClient)

	messages := []message.Message{
		{
			Role:  message.System,
			Parts: []message.ContentPart{message.TextContent{Text: "static prompt"}},
		},
		{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: "turn 1"}},
		},
		{
			Role:  message.Assistant,
			Parts: []message.ContentPart{message.TextContent{Text: "answer 1"}},
		},
		{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: "turn 2"}},
		},
		{
			Role:  message.System,
			Parts: []message.ContentPart{message.TextContent{Text: "[TURN CONTEXT]\nfoo\n\n[SYSTEM INFO]\nbar"}},
		},
	}

	payload := client.buildAliyunCompatiblePayload(messages, nil, false)
	rawMessages, ok := payload["messages"].([]map[string]interface{})
	if !ok {
		t.Fatalf("expected typed messages slice, got %#v", payload["messages"])
	}
	if len(rawMessages) != 5 {
		t.Fatalf("expected 5 payload messages, got %d", len(rawMessages))
	}

	assistantContent, ok := rawMessages[2]["content"].([]map[string]interface{})
	if !ok || len(assistantContent) != 1 {
		t.Fatalf("expected assistant message before latest user turn to be checkpointed, got %#v", rawMessages[2]["content"])
	}
	if _, ok := assistantContent[0]["cache_control"].(map[string]interface{}); !ok {
		t.Fatalf("expected assistant boundary checkpoint to include cache_control, got %#v", assistantContent[0])
	}

	latestUserContent, ok := rawMessages[3]["content"].([]map[string]interface{})
	if !ok || len(latestUserContent) != 1 {
		t.Fatalf("expected latest user turn to remain checkpointed for exact replay, got %#v", rawMessages[3]["content"])
	}
	if _, ok := latestUserContent[0]["cache_control"].(map[string]interface{}); !ok {
		t.Fatalf("expected latest user checkpoint to include cache_control, got %#v", latestUserContent[0])
	}

	if _, ok := rawMessages[4]["content"].([]map[string]interface{}); ok {
		t.Fatalf("expected trailing dynamic system message to remain uncached, got %#v", rawMessages[4]["content"])
	}
}

func TestOpenAI_BuildAliyunCompatiblePayloadSkipsWorkingStateTailCheckpoint(t *testing.T) {
	client := newOpenAIClient(providerClientOptions{
		apiKey: "test-key",
		model: models.Model{
			ID:       models.QwenPlus,
			Name:     "Qwen Plus",
			Provider: models.ProviderAliyun,
			APIModel: "qwen3.5-plus",
		},
	}).(*openaiClient)

	messages := []message.Message{
		{
			Role:  message.System,
			Parts: []message.ContentPart{message.TextContent{Text: "static prompt"}},
		},
		{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: "turn 1"}},
		},
		{
			Role:  message.Assistant,
			Parts: []message.ContentPart{message.TextContent{Text: "answer 1"}},
		},
		{
			Role:  message.User,
			Parts: []message.ContentPart{message.TextContent{Text: "turn 2"}},
		},
		{
			Role:  message.System,
			Parts: []message.ContentPart{message.TextContent{Text: "[WORKING STATE]\nCurrent focus: cache tuning"}},
		},
		{
			Role:  message.System,
			Parts: []message.ContentPart{message.TextContent{Text: "[TURN CONTEXT]\nfoo\n\n[SYSTEM INFO]\nbar"}},
		},
	}

	payload := client.buildAliyunCompatiblePayload(messages, nil, false)
	rawMessages, ok := payload["messages"].([]map[string]interface{})
	if !ok {
		t.Fatalf("expected typed messages slice, got %#v", payload["messages"])
	}
	if len(rawMessages) != 6 {
		t.Fatalf("expected 6 payload messages, got %d", len(rawMessages))
	}

	assistantContent, ok := rawMessages[2]["content"].([]map[string]interface{})
	if !ok || len(assistantContent) != 1 {
		t.Fatalf("expected assistant message before latest user turn to remain checkpointed, got %#v", rawMessages[2]["content"])
	}
	if _, ok := assistantContent[0]["cache_control"].(map[string]interface{}); !ok {
		t.Fatalf("expected assistant boundary checkpoint to include cache_control, got %#v", assistantContent[0])
	}

	latestUserContent, ok := rawMessages[3]["content"].([]map[string]interface{})
	if !ok || len(latestUserContent) != 1 {
		t.Fatalf("expected latest user turn to remain checkpointed, got %#v", rawMessages[3]["content"])
	}
	if _, ok := latestUserContent[0]["cache_control"].(map[string]interface{}); !ok {
		t.Fatalf("expected latest user checkpoint to include cache_control, got %#v", latestUserContent[0])
	}

	if _, ok := rawMessages[4]["content"].([]map[string]interface{}); ok {
		t.Fatalf("expected working state tail message to remain uncached, got %#v", rawMessages[4]["content"])
	}
	if _, ok := rawMessages[5]["content"].([]map[string]interface{}); ok {
		t.Fatalf("expected trailing dynamic system message to remain uncached, got %#v", rawMessages[5]["content"])
	}

	stableEndIndex := findAliyunStableEndIndex(messages, 1)
	if stableEndIndex != 3 {
		t.Fatalf("expected working state tail to be skipped when finding stable end index, got %d", stableEndIndex)
	}
}
