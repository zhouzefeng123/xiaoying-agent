package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/message"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/shared"
)

type openaiOptions struct {
	baseURL         string
	disableCache    bool
	reasoningEffort string
	extraHeaders    map[string]string
	extraQuery      map[string]string
}

type OpenAIOption func(*openaiOptions)

type openaiClient struct {
	providerOptions providerClientOptions
	options         openaiOptions
	client          openai.Client
}

type OpenAIClient ProviderClient

type streamingToolCallDelta struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

type toolCallAccumulator struct {
	Index        int
	ID           string
	Name         string
	Args         string
	StartEmitted bool
}

var requestMetricsMu sync.Mutex

type requestMessageMetric struct {
	Index        int    `json:"index"`
	Role         string `json:"role"`
	ContentChars int    `json:"content_chars"`
	JSONBytes    int    `json:"json_bytes"`
}

type requestMetricsRecord struct {
	AssistantMessageCharsTotal int                    `json:"assistant_message_chars_total"`
	MergedSystemChars          int                    `json:"merged_system_chars"`
	MergedSystemJSONBytes      int                    `json:"merged_system_json_bytes"`
	MessageCount               int                    `json:"message_count"`
	MessageMetrics             []requestMessageMetric `json:"message_metrics"`
	MessagesJSONBytesTotal     int                    `json:"messages_json_bytes_total"`
	Model                      string                 `json:"model"`
	RequestKind                string                 `json:"request_kind"`
	SessionID                  string                 `json:"session_id"`
	ToolCount                  int                    `json:"tool_count"`
	ToolMessageCharsTotal      int                    `json:"tool_message_chars_total"`
	ToolSchemaJSONBytes        int                    `json:"tool_schema_json_bytes"`
	TS                         string                 `json:"ts"`
	UserMessageCharsTotal      int                    `json:"user_message_chars_total"`
	WireFormat                 string                 `json:"wire_format"`
}

func requestMetricsLogPath() string {
	candidates := []string{
		filepath.Join("database", "openai_request_metrics.log"),
	}

	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		candidates = append(candidates,
			filepath.Join(exeDir, "database", "openai_request_metrics.log"),
			filepath.Join(exeDir, "..", "database", "openai_request_metrics.log"),
		)
	}

	for _, candidate := range candidates {
		if _, err := os.Stat(filepath.Dir(candidate)); err == nil {
			return filepath.Clean(candidate)
		}
	}

	return filepath.Clean(candidates[0])
}

func requestKindFromContext(ctx context.Context) string {
	requestKind := tools.GetRequestKind(ctx)
	if requestKind != "" {
		return requestKind
	}
	if sessionID, _ := ctx.Value(tools.SessionIDContextKey).(string); strings.TrimSpace(sessionID) != "" {
		return "main_chat"
	}
	return "unknown"
}

func sessionIDFromContext(ctx context.Context) string {
	sessionID, _ := ctx.Value(tools.SessionIDContextKey).(string)
	return strings.TrimSpace(sessionID)
}

func requestToolSchemaPayload(toolList []tools.BaseTool) []map[string]interface{} {
	if len(toolList) == 0 {
		return nil
	}

	payload := make([]map[string]interface{}, 0, len(toolList))
	for _, tool := range toolList {
		info := tool.Info()
		required := info.Required
		if required == nil {
			required = []string{}
		}
		payload = append(payload, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        info.Name,
				"description": info.Description,
				"parameters":  normalizedToolObjectSchema(info.Parameters, required),
			},
		})
	}

	return payload
}

func buildRequestMetricsRecord(ctx context.Context, model string, wireFormat string, messages []message.Message, toolList []tools.BaseTool) requestMetricsRecord {
	resolvedModel := strings.TrimSpace(model)
	if resolvedModel == "" {
		resolvedModel = "unknown"
	}

	record := requestMetricsRecord{
		MessageCount:   len(messages),
		MessageMetrics: make([]requestMessageMetric, 0, len(messages)),
		Model:          resolvedModel,
		RequestKind:    requestKindFromContext(ctx),
		SessionID:      sessionIDFromContext(ctx),
		ToolCount:      len(toolList),
		TS:             time.Now().Format(time.RFC3339Nano),
		WireFormat:     wireFormat,
	}

	for index, msg := range messages {
		content := msg.Content().String()
		metric := requestMessageMetric{
			Index:        index,
			Role:         string(msg.Role),
			ContentChars: len(content),
		}
		payloadBytes, _ := json.Marshal(map[string]interface{}{
			"role":    string(msg.Role),
			"content": content,
		})
		metric.JSONBytes = len(payloadBytes)
		record.MessagesJSONBytesTotal += metric.JSONBytes

		switch msg.Role {
		case message.System:
			record.MergedSystemChars += metric.ContentChars
			record.MergedSystemJSONBytes += metric.JSONBytes
		case message.User:
			record.UserMessageCharsTotal += metric.ContentChars
		case message.Assistant:
			record.AssistantMessageCharsTotal += metric.ContentChars
		case message.Tool:
			record.ToolMessageCharsTotal += metric.ContentChars
		}

		record.MessageMetrics = append(record.MessageMetrics, metric)
	}

	if len(toolList) > 0 {
		toolSchemaBytes, _ := json.Marshal(requestToolSchemaPayload(toolList))
		record.ToolSchemaJSONBytes = len(toolSchemaBytes)
	}

	return record
}

func appendRequestMetricsRecord(ctx context.Context, model string, wireFormat string, messages []message.Message, toolList []tools.BaseTool) {
	record := buildRequestMetricsRecord(ctx, model, wireFormat, messages, toolList)
	data, err := json.Marshal(record)
	if err != nil {
		logging.Warn("Failed to marshal request metrics", "error", err)
		return
	}

	logPath := requestMetricsLogPath()
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		logging.Warn("Failed to create request metrics directory", "path", logPath, "error", err)
		return
	}

	requestMetricsMu.Lock()
	defer requestMetricsMu.Unlock()

	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		logging.Warn("Failed to open request metrics log", "path", logPath, "error", err)
		return
	}
	defer file.Close()

	if _, err := file.Write(append(data, '\n')); err != nil {
		logging.Warn("Failed to write request metrics log", "path", logPath, "error", err)
	}
}

func isCompletedStreamingToolCall(acc *toolCallAccumulator) bool {
	if acc == nil {
		return false
	}

	trimmed := strings.TrimSpace(acc.Args)
	if trimmed == "" {
		return false
	}

	return json.Valid([]byte(trimmed))
}

func nextStreamingToolCallIndex(active map[int]*toolCallAccumulator) int {
	nextIndex := 0
	for index := range active {
		if index >= nextIndex {
			nextIndex = index + 1
		}
	}
	return nextIndex
}

func findLatestIncompleteStreamingToolCall(active map[int]*toolCallAccumulator, requestedIndex int, delta streamingToolCallDelta) (int, bool) {
	latestIndex := -1
	for index, acc := range active {
		if index == requestedIndex {
			continue
		}
		if isCompletedStreamingToolCall(acc) {
			continue
		}
		if strings.TrimSpace(delta.Name) != "" && acc.Name != "" && acc.Name != delta.Name {
			continue
		}
		if index > latestIndex {
			latestIndex = index
		}
	}

	if latestIndex >= 0 {
		return latestIndex, true
	}

	return 0, false
}

func shouldAllocateNewStreamingToolCall(requested *toolCallAccumulator, delta streamingToolCallDelta) bool {
	if !isCompletedStreamingToolCall(requested) {
		return false
	}

	if strings.TrimSpace(delta.Name) != "" {
		return true
	}

	trimmedArgs := strings.TrimSpace(delta.Arguments)
	return trimmedArgs != "" && json.Valid([]byte(trimmedArgs))
}

func appendAliyunStreamErrorSnippet(existing string, raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return existing
	}

	if existing == "" {
		if len(trimmed) > 512 {
			return trimmed[:512]
		}
		return trimmed
	}

	remaining := 512 - len(existing)
	if remaining <= 1 {
		return existing
	}

	appended := trimmed
	if len(appended) > remaining-1 {
		appended = appended[:remaining-1]
	}

	return existing + "\n" + appended
}

func stringifyAliyunStreamErrorValue(value interface{}) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		trimmed := strings.TrimSpace(typed)
		if strings.EqualFold(trimmed, "null") {
			return ""
		}
		return trimmed
	case fmt.Stringer:
		trimmed := strings.TrimSpace(typed.String())
		if strings.EqualFold(trimmed, "null") {
			return ""
		}
		return trimmed
	case json.Number:
		if typed.String() == "null" {
			return ""
		}
		return typed.String()
	case float64:
		if typed == float64(int64(typed)) {
			return fmt.Sprintf("%d", int64(typed))
		}
		return fmt.Sprintf("%v", typed)
	case float32:
		if typed == float32(int64(typed)) {
			return fmt.Sprintf("%d", int64(typed))
		}
		return fmt.Sprintf("%v", typed)
	case int, int8, int16, int32, int64:
		return fmt.Sprintf("%d", typed)
	case uint, uint8, uint16, uint32, uint64:
		return fmt.Sprintf("%d", typed)
	case bool:
		return fmt.Sprintf("%t", typed)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		trimmed := strings.TrimSpace(string(encoded))
		if strings.EqualFold(trimmed, "null") {
			return ""
		}
		return trimmed
	}
}

func extractAliyunStreamErrorDetails(raw string) (string, string, bool) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", "", false
	}

	decoder := json.NewDecoder(strings.NewReader(trimmed))
	decoder.UseNumber()

	var payload interface{}
	if err := decoder.Decode(&payload); err != nil {
		return "", "", false
	}

	var walk func(interface{}) (string, string, bool)
	walk = func(value interface{}) (string, string, bool) {
		record, ok := value.(map[string]interface{})
		if !ok {
			return "", "", false
		}

		code := ""
		message := ""

		for _, key := range []string{"code", "error_code", "status", "errno"} {
			if candidate := stringifyAliyunStreamErrorValue(record[key]); candidate != "" {
				code = candidate
				break
			}
		}

		for _, key := range []string{"message", "msg", "detail"} {
			if candidate := stringifyAliyunStreamErrorValue(record[key]); candidate != "" {
				message = candidate
				break
			}
		}

		switch nested := record["error"].(type) {
		case map[string]interface{}:
			nestedCode, nestedMessage, found := walk(nested)
			if code == "" {
				code = nestedCode
			}
			if message == "" {
				message = nestedMessage
			}
			if found && (code != "" || message != "") {
				return code, message, true
			}
		default:
			if message == "" {
				message = stringifyAliyunStreamErrorValue(nested)
			}
		}

		if code != "" || message != "" {
			return code, message, true
		}

		if nested, ok := record["data"].(map[string]interface{}); ok {
			return walk(nested)
		}

		return "", "", false
	}

	return walk(payload)
}

func buildAliyunStreamError(statusCode int, raw string) error {
	if code, message, ok := extractAliyunStreamErrorDetails(raw); ok {
		if code != "" && message != "" {
			return fmt.Errorf("raw stream error %s: %s", code, message)
		}
		if code != "" {
			return fmt.Errorf("raw stream error %s", code)
		}
		if message != "" {
			return fmt.Errorf("raw stream error: %s", message)
		}
	}

	trimmed := strings.TrimSpace(raw)
	if trimmed != "" {
		if statusCode > 0 {
			return fmt.Errorf("raw stream error %d: %s", statusCode, trimmed)
		}
		return fmt.Errorf("raw stream error: %s", trimmed)
	}

	if statusCode > 0 {
		return fmt.Errorf("raw stream error %d", statusCode)
	}

	return fmt.Errorf("raw stream ended without any parsable chunks")
}

func debugLogAliyunStreamPayload(stage string, statusCode int, raw string) {
	if !config.Get().Debug {
		return
	}

	fields := []any{
		"stage", stage,
		"status_code", statusCode,
		"raw", strings.TrimSpace(raw),
	}

	if code, message, ok := extractAliyunStreamErrorDetails(raw); ok {
		fields = append(fields,
			"parsed_code", code,
			"parsed_message", message,
		)
	}

	logging.Debug("Aliyun stream error payload", fields...)
}

func debugLogOpenAIStreamError(err error, attempts int, modelID string) {
	if !config.Get().Debug || err == nil {
		return
	}

	fields := []any{
		"attempt", attempts,
		"model", modelID,
		"error", err.Error(),
	}

	var apierr *openai.Error
	if errors.As(err, &apierr) {
		fields = append(fields,
			"status_code", apierr.StatusCode,
			"code", apierr.Code,
			"type", apierr.Type,
			"param", apierr.Param,
			"message", apierr.Message,
			"raw_json", strings.TrimSpace(apierr.RawJSON()),
		)
		if apierr.Request != nil && apierr.Request.URL != nil {
			fields = append(fields,
				"method", apierr.Request.Method,
				"url", apierr.Request.URL.String(),
			)
		}
	}

	logging.Debug("OpenAI stream terminal error", fields...)
}

func resolveStreamingToolCallIndex(delta streamingToolCallDelta, active map[int]*toolCallAccumulator) int {
	if delta.ID != "" {
		for index, acc := range active {
			if acc != nil && acc.ID == delta.ID {
				return index
			}
		}
	}

	requestedIndex := delta.Index
	if requested, ok := active[requestedIndex]; ok {
		if isCompletedStreamingToolCall(requested) {
			if latestIndex, found := findLatestIncompleteStreamingToolCall(active, requestedIndex, delta); found {
				return latestIndex
			}
			if shouldAllocateNewStreamingToolCall(requested, delta) {
				return nextStreamingToolCallIndex(active)
			}
		}
		return requestedIndex
	}

	if requestedIndex == 0 {
		if latestIndex, found := findLatestIncompleteStreamingToolCall(active, requestedIndex, delta); found {
			return latestIndex
		}
	}

	return requestedIndex
}

func appendStreamingToolCallDeltas(deltas []streamingToolCallDelta, active map[int]*toolCallAccumulator, eventChan chan<- ProviderEvent) {
	for _, delta := range deltas {
		resolvedIndex := resolveStreamingToolCallIndex(delta, active)
		acc, ok := active[resolvedIndex]
		if !ok {
			acc = &toolCallAccumulator{Index: resolvedIndex}
			active[resolvedIndex] = acc
		}

		if delta.ID != "" {
			acc.ID = delta.ID
		}
		if delta.Name != "" {
			acc.Name = delta.Name
		}
		if !acc.StartEmitted && acc.ID != "" && acc.Name != "" {
			acc.StartEmitted = true
			eventChan <- ProviderEvent{
				Type: EventToolUseStart,
				ToolCall: &message.ToolCall{
					ID:       acc.ID,
					Index:    acc.Index,
					Name:     acc.Name,
					Input:    "",
					Finished: false,
				},
			}
		}
		if delta.Arguments != "" {
			acc.Args += delta.Arguments
			eventChan <- ProviderEvent{
				Type: EventToolUseDelta,
				ToolCall: &message.ToolCall{
					ID:       acc.ID,
					Index:    acc.Index,
					Name:     acc.Name,
					Input:    delta.Arguments,
					Finished: false,
				},
			}
		}
	}
}

func finalizeStreamingToolCalls(active map[int]*toolCallAccumulator) []message.ToolCall {
	if len(active) == 0 {
		return nil
	}

	indices := make([]int, 0, len(active))
	for index := range active {
		indices = append(indices, index)
	}
	sort.Ints(indices)

	toolCalls := make([]message.ToolCall, 0, len(indices))
	for _, index := range indices {
		acc := active[index]
		args := acc.Args
		if !json.Valid([]byte(args)) {
			logging.Warn("Truncated tool call JSON detected", "tool", acc.Name, "args_len", len(args))
			args = fmt.Sprintf(`{"_truncated_error": "Tool call arguments were truncated at %d chars due to output token limit. The content is too large for a single tool call. Use 'edit' or 'patch' for targeted chunked changes, or reduce the payload and retry with smaller semantic chunks."}`, len(args))
		}
		toolCalls = append(toolCalls, message.ToolCall{
			ID:       acc.ID,
			Index:    acc.Index,
			Name:     acc.Name,
			Input:    args,
			Type:     "function",
			Finished: true,
		})
	}

	return toolCalls
}

func normalizeToolCallArguments(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return `{}`
	}
	if json.Valid([]byte(trimmed)) {
		return trimmed
	}

	normalizedPayload, err := json.Marshal(map[string]string{
		"_normalized_error": "invalid tool call arguments were normalized before replay",
		"_raw_arguments":    trimmed,
	})
	if err != nil {
		logging.Warn("Failed to normalize tool call arguments", "error", err)
		return `{}`
	}

	logging.Warn("Normalized invalid tool call arguments before provider replay", "raw_len", len(trimmed))
	return string(normalizedPayload)
}

func normalizeToolCallForHistory(tc message.ToolCall) message.ToolCall {
	tc.Input = normalizeToolCallArguments(tc.Input)
	if strings.TrimSpace(tc.Type) == "" {
		tc.Type = "function"
	}
	if tc.ID == "" {
		tc.ID = fmt.Sprintf("tool-history-%d-%s", tc.Index, tc.Name)
	}
	return tc
}

func finishedToolCallsForHistory(toolCalls []message.ToolCall) []message.ToolCall {
	if len(toolCalls) == 0 {
		return nil
	}

	filtered := make([]message.ToolCall, 0, len(toolCalls))
	seen := make(map[string]struct{}, len(toolCalls))
	for _, tc := range toolCalls {
		if !tc.Finished {
			continue
		}
		normalized := normalizeToolCallForHistory(tc)
		key := normalized.ID
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		filtered = append(filtered, normalized)
	}

	return filtered
}

func newOpenAIClient(opts providerClientOptions) OpenAIClient {
	openaiOpts := openaiOptions{
		reasoningEffort: "medium",
	}
	for _, o := range opts.openaiOptions {
		o(&openaiOpts)
	}

	openaiClientOptions := []option.RequestOption{}
	if opts.apiKey != "" {
		openaiClientOptions = append(openaiClientOptions, option.WithAPIKey(opts.apiKey))
	}
	if openaiOpts.baseURL != "" {
		openaiClientOptions = append(openaiClientOptions, option.WithBaseURL(openaiOpts.baseURL))
	}

	if openaiOpts.extraHeaders != nil {
		for key, value := range openaiOpts.extraHeaders {
			openaiClientOptions = append(openaiClientOptions, option.WithHeader(key, value))
		}
	}

	if openaiOpts.extraQuery != nil {
		for key, value := range openaiOpts.extraQuery {
			openaiClientOptions = append(openaiClientOptions, option.WithQuery(key, value))
		}
	}

	client := openai.NewClient(openaiClientOptions...)
	return &openaiClient{
		providerOptions: opts,
		options:         openaiOpts,
		client:          client,
	}
}

func (o *openaiClient) chatCompletionsURL() string {
	endpoint := strings.TrimRight(o.options.baseURL, "/") + "/chat/completions"
	if len(o.options.extraQuery) == 0 {
		return endpoint
	}

	parsedURL, err := url.Parse(endpoint)
	if err != nil {
		return endpoint
	}

	query := parsedURL.Query()
	for key, value := range o.options.extraQuery {
		if strings.TrimSpace(value) == "" {
			continue
		}
		query.Set(key, value)
	}
	parsedURL.RawQuery = query.Encode()
	return parsedURL.String()
}

type localSenseVoiceCLIResponse struct {
	Text string `json:"text"`
}

func extractSenseVoiceTranscript(output []byte) (string, error) {
	scanner := bufio.NewScanner(bytes.NewReader(output))
	scanner.Buffer(make([]byte, 0, 1024), 10<<20)

	var lastText string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "{") || !strings.Contains(line, `"text"`) {
			continue
		}

		var response localSenseVoiceCLIResponse
		if err := json.Unmarshal([]byte(line), &response); err == nil && strings.TrimSpace(response.Text) != "" {
			lastText = strings.TrimSpace(response.Text)
		}
	}

	if err := scanner.Err(); err != nil {
		return "", err
	}

	if lastText == "" {
		return "", errors.New("未从 SenseVoice 输出中解析到转写文本")
	}

	return lastText, nil
}

func (o *openaiClient) transcribeLocalSenseVoice(ctx context.Context, audio []byte, filename string) (string, error) {
	binaryPath, err := resolveSenseVoiceBinaryPath()
	if err != nil {
		return "", err
	}

	modelPath, err := resolveSenseVoiceModelPath()
	if err != nil {
		return "", err
	}

	tokensPath, err := resolveSenseVoiceTokensPath()
	if err != nil {
		return "", err
	}

	tempDir, err := os.MkdirTemp("", "auramate_sensevoice_")
	if err != nil {
		return "", fmt.Errorf("failed to create sensevoice temp dir: %w", err)
	}
	defer os.RemoveAll(tempDir)

	ext := filepath.Ext(filename)
	if ext == "" {
		ext = ".wav"
	}

	inputPath := filepath.Join(tempDir, "input"+ext)
	if err := os.WriteFile(inputPath, audio, 0600); err != nil {
		return "", fmt.Errorf("failed to write sensevoice input: %w", err)
	}

	cmd := exec.CommandContext(ctx, binaryPath,
		"--tokens="+tokensPath,
		"--sense-voice-model="+modelPath,
		"--sense-voice-language="+localSenseVoiceDefaultLanguage,
		"--sense-voice-use-itn=1",
		"--provider=cpu",
		"--num-threads=2",
		"--debug=0",
		inputPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		trimmedOutput := strings.TrimSpace(string(output))
		if trimmedOutput == "" {
			trimmedOutput = "无输出"
		}
		return "", fmt.Errorf("SenseVoice ONNX 转写失败: %w, output: %s", err, trimmedOutput)
	}

	transcript, err := extractSenseVoiceTranscript(output)
	if err != nil {
		trimmedOutput := strings.TrimSpace(string(output))
		if trimmedOutput == "" {
			trimmedOutput = "无输出"
		}
		return "", fmt.Errorf("SenseVoice ONNX 未生成可用转写结果: %w, output: %s", err, trimmedOutput)
	}

	return transcript, nil
}

func (o *openaiClient) transcribeNativeWindows(ctx context.Context, audio []byte, filename string) (string, error) {
	// Save audio to temp file
	tmpFile, err := os.CreateTemp("", "speech_*.wav")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(audio); err != nil {
		return "", fmt.Errorf("failed to write temp file: %w", err)
	}
	tmpFile.Close()

	// Locate the PowerShell script
	// Assume we are running from root or backend.
	// We need to find "backend/scripts/win_stt.ps1"
	// Best effort search
	scriptPath := "backend/scripts/win_stt.ps1"
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		// Try absolute path if we know it (e.g. from env)
		// Or try relative to executable?
		// Fallback to absolute path for this environment
		scriptPath = "e:\\auramate_hub\\auramate-0.1\\auramate\\backend\\scripts\\win_stt.ps1"
	}

	// Execute PowerShell
	cmd := exec.Command("powershell", "-ExecutionPolicy", "Bypass", "-File", scriptPath, "-InputFile", tmpFile.Name())

	// Capture output
	output, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("native stt failed: %s, output: %s", err, string(output))
	}

	text := strings.TrimSpace(string(output))
	if text == "" {
		// If empty, it might be no speech detected
		return "", nil
	}

	return text, nil
}

func (o *openaiClient) convertMessages(messages []message.Message) (openaiMessages []openai.ChatCompletionMessageParamUnion) {
	// 1. Collect and Merge Initial System Messages
	var systemParts []string

	// Add global system message first if provided
	if o.providerOptions.systemMessage != "" {
		systemParts = append(systemParts, o.providerOptions.systemMessage)
	}

	// Identify and merge consecutive leading system messages from the history
	startIdx := 0
	for i, msg := range messages {
		if msg.Role == message.System {
			content := msg.Content().String()
			if content != "" {
				systemParts = append(systemParts, content)
			}
			startIdx = i + 1
		} else {
			// Stop at the first non-system message
			break
		}
	}

	// Create the single merged system message if we have content
	if len(systemParts) > 0 {
		mergedContent := strings.Join(systemParts, "\n\n")
		openaiMessages = append(openaiMessages, openai.SystemMessage(mergedContent))
	}

	var pendingToolCalls []string

	// Process remaining messages
	for i := startIdx; i < len(messages); i++ {
		msg := messages[i]

		// If we encounter a non-tool message while we have pending tool calls,
		// we must close them with a dummy error response to maintain API invariants.
		if msg.Role != message.Tool && len(pendingToolCalls) > 0 {
			for _, id := range pendingToolCalls {
				openaiMessages = append(openaiMessages,
					openai.ToolMessage("Tool execution missing or interrupted.", id),
				)
			}
			pendingToolCalls = nil
		}

		switch msg.Role {
		case message.System:
			// Treat late-bound system messages as User messages or merge them?
			// STRICT TEMPLATE COMPATIBILITY:
			// If a system message appears in the middle of conversation (e.g. RAG context injection),
			// many models (Qwen, Llama3) will REJECT it if passed as 'role: system'.
			// We must convert it to 'role: user' or 'role: assistant' to satisfy the template.
			// Converting to User is safer for context injection.
			// We add a prefix to indicate it's a system injection.

			// For OpenRouter/Qwen/Llama3/Chaocetec etc., System messages MUST be at the beginning.
			// Any system message after the first non-system message is considered "middle".
			// Since we already merged initial system messages at the top, any System message here (in the loop) IS a middle one.

			content := msg.Content().String()
			if content != "" {
				// FORCE USER ROLE for intermediate system messages
				openaiMessages = append(openaiMessages, openai.UserMessage(
					"[System Injection]\n"+content,
				))
			}

		case message.User:
			var content []openai.ChatCompletionContentPartUnionParam
			textBlock := openai.ChatCompletionContentPartTextParam{Text: msg.Content().String()}
			content = append(content, openai.ChatCompletionContentPartUnionParam{OfText: &textBlock})
			for _, binaryContent := range msg.BinaryContent() {
				imageURL := openai.ChatCompletionContentPartImageImageURLParam{URL: binaryContent.String(models.ProviderOpenAI)}
				imageBlock := openai.ChatCompletionContentPartImageParam{ImageURL: imageURL}

				content = append(content, openai.ChatCompletionContentPartUnionParam{OfImageURL: &imageBlock})
			}

			openaiMessages = append(openaiMessages, openai.UserMessage(content))

		case message.Assistant:
			assistantMsg := openai.ChatCompletionAssistantMessageParam{
				Role: "assistant",
			}
			finishedToolCalls := finishedToolCallsForHistory(msg.ToolCalls())

			// Ensure content is set only if it's not empty.
			// Aliyun/DashScope example shows "content": null when tool_calls are present.
			// Setting it to "" might cause issues or be treated as text.
			if msg.Content().String() != "" {
				assistantMsg.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
					OfString: openai.String(msg.Content().String()),
				}
			}

			if len(finishedToolCalls) > 0 {
				assistantMsg.ToolCalls = make([]openai.ChatCompletionMessageToolCallParam, len(finishedToolCalls))
				for i, call := range finishedToolCalls {
					assistantMsg.ToolCalls[i] = openai.ChatCompletionMessageToolCallParam{
						ID:   call.ID,
						Type: "function",
						Function: openai.ChatCompletionMessageToolCallFunctionParam{
							Name:      call.Name,
							Arguments: call.Input,
						},
					}
					pendingToolCalls = append(pendingToolCalls, call.ID)
				}
			}

			// Skip empty assistant messages (e.g. pure reasoning messages that are not supported in history)
			// We use msg.Content() because assistantMsg.Content is a struct and cannot be compared to nil directly.
			if msg.Content().String() == "" && len(finishedToolCalls) == 0 {
				continue
			}

			openaiMessages = append(openaiMessages, openai.ChatCompletionMessageParamUnion{
				OfAssistant: &assistantMsg,
			})

		case message.Tool:
			for _, result := range msg.ToolResults() {
				// Check if this tool call is pending
				isPending := false
				for _, id := range pendingToolCalls {
					if id == result.ToolCallID {
						isPending = true
						break
					}
				}

				if !isPending {
					// Skip orphaned tool result (already closed by interruption or invalid)
					continue
				}

				openaiMessages = append(openaiMessages,
					openai.ToolMessage(result.Content, result.ToolCallID),
				)
				// Remove processed tool call from pending list
				newPending := make([]string, 0, len(pendingToolCalls))
				for _, id := range pendingToolCalls {
					if id != result.ToolCallID {
						newPending = append(newPending, id)
					}
				}
				pendingToolCalls = newPending
			}
		}
	}

	// Final check: if we end with pending tool calls, close them.
	if len(pendingToolCalls) > 0 {
		for _, id := range pendingToolCalls {
			openaiMessages = append(openaiMessages,
				openai.ToolMessage("Tool execution missing or interrupted.", id),
			)
		}
	}

	// DEBUG: Log final message roles
	if config.Get().Debug {
		var roles []string
		for i, m := range openaiMessages {
			// Extract role using reflection or JSON marshal since it's a Union type
			b, _ := json.Marshal(m)
			var temp map[string]interface{}
			json.Unmarshal(b, &temp)
			if r, ok := temp["role"].(string); ok {
				roles = append(roles, fmt.Sprintf("%d:%s", i, r))
			} else {
				roles = append(roles, fmt.Sprintf("%d:unknown", i))
			}
		}
		logging.Info("DEBUG: Final OpenAI Messages Roles", "roles", strings.Join(roles, ", "))
	}

	return
}

func (o *openaiClient) convertTools(tools []tools.BaseTool) []openai.ChatCompletionToolParam {
	// Sort tools by name to ensure deterministic order for caching
	sort.Slice(tools, func(i, j int) bool {
		return tools[i].Info().Name < tools[j].Info().Name
	})

	openaiTools := make([]openai.ChatCompletionToolParam, len(tools))

	for i, tool := range tools {
		info := tool.Info()
		required := info.Required
		if required == nil {
			required = []string{}
		}
		openaiTools[i] = openai.ChatCompletionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        info.Name,
				Description: openai.String(info.Description),
				Parameters:  openai.FunctionParameters(normalizedToolObjectSchema(info.Parameters, required)),
			},
		}
	}

	return openaiTools
}

func (o *openaiClient) finishReason(reason string) message.FinishReason {
	switch reason {
	case "stop":
		return message.FinishReasonEndTurn
	case "length":
		return message.FinishReasonMaxTokens
	case "tool_calls":
		return message.FinishReasonToolUse
	default:
		return message.FinishReasonUnknown
	}
}

func (o *openaiClient) preparedParams(messages []openai.ChatCompletionMessageParamUnion, tools []openai.ChatCompletionToolParam) openai.ChatCompletionNewParams {
	// DEBUG LOGGING
	logging.Info("DEBUG: Preparing OpenAI request",
		"model", o.providerOptions.model.APIModel,
		"baseURL", o.options.baseURL,
		"tool_count", len(tools),
	)

	// Log first few messages for context
	if len(messages) > 0 {
		firstMsg, _ := json.Marshal(messages[0])
		logging.Info("DEBUG: First Message", "content", string(firstMsg))
	}

	// Log tool definitions if present
	if len(tools) > 0 {
		toolJson, _ := json.Marshal(tools)
		logging.Info("DEBUG: Tools Definition", "tools", string(toolJson))
	}

	params := openai.ChatCompletionNewParams{
		Model:    openai.ChatModel(o.providerOptions.model.APIModel),
		Messages: messages,
	}

	// Note: Do NOT set response_format for llama.cpp compatibility
	// llama.cpp doesn't support response_format and will error with:
	// "JSON schema conversion failed: Unrecognized schema: object"
	// Default behavior (no response_format) works for all providers including llama.cpp

	if len(tools) > 0 {
		params.Tools = tools
	}

	// For Aliyun compatibility, ensure model is passed correctly
	// The openai library uses json:"model" tag which should work
	// However, if APIModel is empty, it will be empty string
	if o.providerOptions.model.APIModel == "" {
		logging.Error("APIModel is empty in preparedParams", "provider", o.providerOptions.model.Provider, "id", o.providerOptions.model.ID)
	} else {
		logging.Debug("Using APIModel", "model", o.providerOptions.model.APIModel)
	}

	if o.providerOptions.model.CanReason == true {
		// Only OpenAI o1/o3 support MaxCompletionTokens + ReasoningEffort
		// DeepSeek R1 and others use standard MaxTokens
		if strings.Contains(strings.ToLower(string(o.providerOptions.model.ID)), "o1") ||
			strings.Contains(strings.ToLower(string(o.providerOptions.model.ID)), "o3") {
			params.MaxCompletionTokens = openai.Int(o.providerOptions.maxTokens)
			switch o.options.reasoningEffort {
			case "low":
				params.ReasoningEffort = shared.ReasoningEffortLow
			case "medium":
				params.ReasoningEffort = shared.ReasoningEffortMedium
			case "high":
				params.ReasoningEffort = shared.ReasoningEffortHigh
			default:
				params.ReasoningEffort = shared.ReasoningEffortMedium
			}
		} else {
			// For DeepSeek R1 and other reasoning models that are not OpenAI o-series
			// Use standard parameters
			params.MaxTokens = openai.Int(o.providerOptions.maxTokens)
			// DeepSeek R1 usually prefers Temperature 0.6
			params.Temperature = openai.Float(0.6)
		}
	} else {
		params.MaxTokens = openai.Int(o.providerOptions.maxTokens)
		if o.providerOptions.model.DefaultTemperature != nil {
			params.Temperature = openai.Float(*o.providerOptions.model.DefaultTemperature)
		} else {
			// Set Temperature to 0 for coding/factual tasks (unless reasoning model)
			params.Temperature = openai.Float(0.0)
		}
	}

	return params
}

func (o *openaiClient) shouldUseAliyunCompatiblePath() (bool, bool) {
	apiModel := strings.ToLower(strings.TrimSpace(o.providerOptions.model.APIModel))
	provider := o.providerOptions.model.Provider
	isAliyunCompatibleWireModel := func(value string) bool {
		return strings.Contains(value, "qwen") || strings.Contains(value, "deepseek") || strings.Contains(value, "kimi")
	}

	if apiModel != "" {
		if !isAliyunCompatibleWireModel(apiModel) {
			symbolicMarkers := strings.ToLower(strings.Join([]string{
				string(o.providerOptions.model.ID),
				o.providerOptions.model.Name,
			}, " "))
			if isAliyunCompatibleWireModel(symbolicMarkers) {
				logging.Debug("Skipping Aliyun compatible path because API model uses a different wire format",
					"api_model", o.providerOptions.model.APIModel,
					"model_id", string(o.providerOptions.model.ID),
					"provider", provider,
				)
			}
			return false, false
		}

		if provider == models.ProviderAliyun {
			return true, true
		}
		if (provider == models.ProviderOpenAI || provider == models.ProviderCustom) && o.options.baseURL != "" {
			return true, false
		}
		return false, false
	}

	symbolicMarkers := strings.ToLower(strings.Join([]string{
		string(o.providerOptions.model.ID),
		o.providerOptions.model.Name,
	}, " "))
	if !isAliyunCompatibleWireModel(symbolicMarkers) {
		return false, false
	}

	if provider == models.ProviderAliyun {
		return true, true
	}
	if (provider == models.ProviderOpenAI || provider == models.ProviderCustom) && o.options.baseURL != "" {
		return true, false
	}

	return false, false
}

func (o *openaiClient) send(ctx context.Context, messages []message.Message, tools []tools.BaseTool) (response *ProviderResponse, err error) {
	if useAliyunCompatiblePath, enableAliyunCacheHeader := o.shouldUseAliyunCompatiblePath(); useAliyunCompatiblePath {
		return o.sendAliyun(ctx, messages, tools, enableAliyunCacheHeader)
	}

	params := o.preparedParams(o.convertMessages(messages), o.convertTools(tools))
	appendRequestMetricsRecord(ctx, o.providerOptions.model.APIModel, "openai-chat-completions", messages, tools)
	cfg := config.Get()
	if cfg.Debug {
		jsonData, _ := json.Marshal(params)
		logging.Debug("Prepared messages", "messages", string(jsonData))
	}
	attempts := 0
	for {
		attempts++
		openaiResponse, err := o.client.Chat.Completions.New(
			ctx,
			params,
		)
		// If there is an error we are going to see if we can retry the call
		if err != nil {
			retry, after, retryErr := o.shouldRetry(attempts, err)
			if retryErr != nil {
				return nil, retryErr
			}
			if retry {
				logging.WarnPersist(fmt.Sprintf("Retrying due to rate limit... attempt %d of %d", attempts, maxRetries), logging.PersistTimeArg, time.Millisecond*time.Duration(after+100))
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(after) * time.Millisecond):
					continue
				}
			}
			return nil, retryErr
		}

		content := ""
		if openaiResponse.Choices[0].Message.Content != "" {
			content = openaiResponse.Choices[0].Message.Content
		}

		toolCalls := o.toolCalls(*openaiResponse)
		finishReason := o.finishReason(string(openaiResponse.Choices[0].FinishReason))

		if len(toolCalls) > 0 {
			finishReason = message.FinishReasonToolUse
		}

		return &ProviderResponse{
			Content:      content,
			ToolCalls:    toolCalls,
			Usage:        o.usage(*openaiResponse),
			FinishReason: finishReason,
		}, nil
	}
}

func (o *openaiClient) stream(ctx context.Context, messages []message.Message, tools []tools.BaseTool) <-chan ProviderEvent {
	if useAliyunCompatiblePath, enableAliyunCacheHeader := o.shouldUseAliyunCompatiblePath(); useAliyunCompatiblePath {
		return o.streamAliyun(ctx, messages, tools, enableAliyunCacheHeader)
	}

	params := o.preparedParams(o.convertMessages(messages), o.convertTools(tools))
	appendRequestMetricsRecord(ctx, o.providerOptions.model.APIModel, "openai-chat-completions", messages, tools)
	// Always enable stream options for better usage tracking, unless explicitly disabled by model quirk
	params.StreamOptions = openai.ChatCompletionStreamOptionsParam{
		IncludeUsage: openai.Bool(true),
	}

	// [FIX] Add explicit HTTP Client with longer timeout for DeepSeek/Reasoning models
	// Reasoning models often "pause" the stream for long periods while thinking, causing default HTTP timeouts.
	// We override the transport to disable response timeout (or set it very high) and rely on TCP KeepAlive.

	// Check if this is a DeepSeek or reasoning-heavy model
	isReasoning := strings.Contains(strings.ToLower(string(o.providerOptions.model.ID)), "reasoner") ||
		strings.Contains(strings.ToLower(string(o.providerOptions.model.ID)), "deepseek") ||
		o.options.reasoningEffort == "high"

	if isReasoning {
		// Clone the client options to inject a custom HTTP client if not already present
		// Actually, openai-go client is immutable. We might need to recreate it or just hope the default transport is good.
		// The default openai-go client has a default timeout.
		// Let's rely on Context for timeout control, but we must ensure the underlying transport doesn't time out on "idle" stream.
		// The 'NewStreaming' call accepts RequestOptions.
		// We can add a custom timeout option for this specific request.

		// NOTE: openai-go doesn't easily expose transport timeout per request.
		// However, we can use context.WithTimeout to extend the DEADLINE.
		// But "stall" timeout (read timeout) is handled by http.Client.

		// For now, let's just log that we are in reasoning mode.
		logging.Debug("Starting reasoning stream", "model", o.providerOptions.model.ID)
	}

	cfg := config.Get()
	if cfg.Debug {
		jsonData, _ := json.Marshal(params)
		logging.Debug("Prepared messages", "messages", string(jsonData))
	}

	attempts := 0
	eventChan := make(chan ProviderEvent)

	go func() {
		for {
			attempts++
			openaiStream := o.client.Chat.Completions.NewStreaming(
				ctx,
				params,
			)

			acc := openai.ChatCompletionAccumulator{}
			currentContent := ""
			toolCalls := make([]message.ToolCall, 0)
			activeToolCalls := map[int]*toolCallAccumulator{}
			var capturedUsage *TokenUsage

			for openaiStream.Next() {
				chunk := openaiStream.Current()
				acc.AddChunk(chunk)

				// DEBUG LOG
				if config.Get().Debug {
					chunkBytes, _ := json.Marshal(chunk)
					logging.Debug("OpenAI Stream Chunk", "chunk", string(chunkBytes))
				}

				// [Usage Check] Emit usage event if present in chunk (e.g. final chunk or incremental)
				if chunk.Usage.TotalTokens > 0 {
					logging.Info("DEBUG: OpenAI Stream Usage Received", "total", chunk.Usage.TotalTokens, "prompt", chunk.Usage.PromptTokens, "completion", chunk.Usage.CompletionTokens)
					cachedTokens := int64(chunk.Usage.PromptTokensDetails.CachedTokens)

					// DeepSeek support: check prompt_cache_hit_tokens in ExtraFields
					if cachedTokens == 0 {
						if val, ok := chunk.Usage.JSON.ExtraFields["prompt_cache_hit_tokens"]; ok {
							logging.Info("DEBUG: Found prompt_cache_hit_tokens in ExtraFields", "val_type", reflect.TypeOf(val))
							// val is respjson.Field (struct), so we can't type assert directly.
							// Use reflection to access 'raw' field which should contain the JSON string value.
							v := reflect.ValueOf(val)
							if v.Kind() == reflect.Struct {
								f := v.FieldByName("raw")
								if f.IsValid() && f.Kind() == reflect.String {
									rawStr := f.String()
									logging.Info("DEBUG: Extracted raw prompt_cache_hit_tokens", "raw", rawStr)
									// Try to unmarshal as number
									var num float64
									if err := json.Unmarshal([]byte(rawStr), &num); err == nil {
										cachedTokens = int64(num)
									} else {
										logging.Warn("Failed to unmarshal prompt_cache_hit_tokens", "raw", rawStr)
									}
								} else {
									logging.Warn("Field 'raw' invalid or not string", "valid", f.IsValid(), "kind", f.Kind())
								}
							} else {
								logging.Warn("prompt_cache_hit_tokens is not a struct", "kind", v.Kind())
							}
						}
					}

					inputTokens := int64(chunk.Usage.PromptTokens) - cachedTokens

					// Ensure non-negative
					if inputTokens < 0 {
						inputTokens = 0
					}

					usage := &TokenUsage{
						InputTokens:         inputTokens,
						OutputTokens:        int64(chunk.Usage.CompletionTokens),
						CacheReadTokens:     cachedTokens,
						CacheCreationTokens: 0,
					}
					capturedUsage = usage

					eventChan <- ProviderEvent{
						Type:  EventUsage,
						Usage: usage,
					}
				}

				// [FIX] Reset a "keep-alive" timer here if we were implementing a watchdog?
				// For now, just processing chunks.

				for _, choice := range chunk.Choices {
					// Check for Reasoning Content (DeepSeek/o1) via ExtraFields
					if val, ok := choice.Delta.JSON.ExtraFields["reasoning_content"]; ok {
						if config.Get().Debug {
							logging.Debug("Found reasoning_content in ExtraFields", "val_type", reflect.TypeOf(val), "val", val)
						}

						// Try direct string assertion first (simplest case)
						// FIX: val is respjson.Field struct, not interface{}, so we cannot type assert directly if the map is strongly typed.
						// We use reflection directly.

						// Use reflection to access unexported 'raw' field of respjson.Field
						// This is for openai-go's internal representation
						v := reflect.ValueOf(val)
						if v.Kind() == reflect.String {
							// Sometimes it might just be a string value wrapped in interface
							eventChan <- ProviderEvent{
								Type:     EventThinkingDelta,
								Thinking: v.String(),
							}
						} else if v.Kind() == reflect.Struct {
							// Iterate to find 'raw' field just in case
							f := v.FieldByName("raw")
							if f.IsValid() && f.Kind() == reflect.String {
								if config.Get().Debug {
									logging.Debug("Extracted raw reasoning content via reflection", "raw", f.String())
								}
								// raw is a JSON string (e.g. "thinking..."), we need to unmarshal it to get the actual text
								var thinking string
								if err := json.Unmarshal([]byte(f.String()), &thinking); err == nil {
									eventChan <- ProviderEvent{
										Type:     EventThinkingDelta,
										Thinking: thinking,
									}
								} else {
									// If unmarshal fails, maybe it is the raw string itself?
									// Try sending raw if unmarshal fails, or log error
									logging.Error("Failed to unmarshal reasoning content", "error", err, "raw", f.String())
									// Fallback: send raw, stripped of quotes if possible
									eventChan <- ProviderEvent{
										Type:     EventThinkingDelta,
										Thinking: strings.Trim(f.String(), "\""),
									}
								}
							} else {
								logging.Warn("Field 'raw' invalid or not string", "valid", f.IsValid(), "kind", f.Kind(), "struct_type", v.Type().String())
							}
						} else {
							logging.Warn("Unknown reasoning_content type", "kind", v.Kind(), "value", val)
						}
					}

					if choice.Delta.Content != "" {
						eventChan <- ProviderEvent{
							Type:    EventContentDelta,
							Content: choice.Delta.Content,
						}
						currentContent += choice.Delta.Content
					}

					if len(choice.Delta.ToolCalls) > 0 {
						deltas := make([]streamingToolCallDelta, 0, len(choice.Delta.ToolCalls))
						for _, tc := range choice.Delta.ToolCalls {
							deltas = append(deltas, streamingToolCallDelta{
								Index:     int(tc.Index),
								ID:        tc.ID,
								Name:      tc.Function.Name,
								Arguments: tc.Function.Arguments,
							})
						}
						appendStreamingToolCallDeltas(deltas, activeToolCalls, eventChan)
					}
				}
			}

			err := openaiStream.Err()
			if err == nil || errors.Is(err, io.EOF) {
				// Stream completed successfully

				// SAFETY CHECK: Ensure we have at least one choice or usage event
				// Some models might return empty streams on filter or error.
				// If we received NO content and NO usage, this is likely a silent failure.
				if currentContent == "" && capturedUsage == nil && len(toolCalls) == 0 {
					logging.Warn("OpenAI Stream completed with EMPTY content and NO usage", "attempts", attempts)
					// We should probably NOT retry here if it's a valid 200 OK empty response,
					// unless we suspect network drop.
					// But if it's consistently empty, the Agent loop will catch it.
				}

				if len(acc.ChatCompletion.Choices) == 0 {
					logging.Warn("OpenAI stream completed with no choices", "attempts", attempts)
					// If we have content, we can still return it?
					// But currentContent was built from choices.
					// If choices is empty, currentContent is empty.
					// We should probably return an error or empty response.
					// But we must NOT panic.

					eventChan <- ProviderEvent{
						Type: EventComplete,
						Response: &ProviderResponse{
							Content:   currentContent,
							ToolCalls: toolCalls,
							// Usage might still be valid if present in stream options?
							// But if choices is empty, likely usage is empty too.
							Usage:        o.usage(acc.ChatCompletion),
							FinishReason: message.FinishReasonEndTurn, // Default
						},
					}
					close(eventChan)
					return
				}

				finishReason := o.finishReason(string(acc.ChatCompletion.Choices[0].FinishReason))
				finalToolCalls := finalizeStreamingToolCalls(activeToolCalls)
				if len(finalToolCalls) > 0 {
					toolCalls = append(toolCalls, finalToolCalls...)
				} else if len(acc.ChatCompletion.Choices[0].Message.ToolCalls) > 0 {
					toolCalls = append(toolCalls, o.toolCalls(acc.ChatCompletion)...)
				}
				if len(toolCalls) > 0 {
					finishReason = message.FinishReasonToolUse
				}

				finalUsage := o.usage(acc.ChatCompletion)
				if capturedUsage != nil && (finalUsage.OutputTokens == 0 || capturedUsage.OutputTokens > finalUsage.OutputTokens) {
					finalUsage = *capturedUsage
				}

				eventChan <- ProviderEvent{
					Type: EventComplete,
					Response: &ProviderResponse{
						Content:      currentContent,
						ToolCalls:    toolCalls,
						Usage:        finalUsage,
						FinishReason: finishReason,
					},
				}
				close(eventChan)
				return
			}

			// If there is an error we are going to see if we can retry the call
			retry, after, retryErr := o.shouldRetry(attempts, err)

			// LOG THE ERROR DETAILS
			logging.Error("OpenAI Stream Error", "error", err, "attempt", attempts)
			debugLogOpenAIStreamError(err, attempts, string(o.providerOptions.model.ID))

			if retryErr != nil {
				eventChan <- ProviderEvent{Type: EventError, Error: retryErr}
				close(eventChan)
				return
			}
			if retry {
				logging.WarnPersist(fmt.Sprintf("Retrying due to rate limit... attempt %d of %d", attempts, maxRetries), logging.PersistTimeArg, time.Millisecond*time.Duration(after+100))
				select {
				case <-ctx.Done():
					// context cancelled
					if ctx.Err() == nil {
						eventChan <- ProviderEvent{Type: EventError, Error: ctx.Err()}
					}
					close(eventChan)
					return
				case <-time.After(time.Duration(after) * time.Millisecond):
					continue
				}
			}
			eventChan <- ProviderEvent{Type: EventError, Error: retryErr}
			close(eventChan)
			return
		}
	}()

	return eventChan
}

func (o *openaiClient) transcribe(ctx context.Context, audio []byte, filename string) (string, error) {
	if o.providerOptions.model.ID == models.LocalSenseVoice || o.providerOptions.model.APIModel == string(models.LocalSenseVoice) {
		return o.transcribeLocalSenseVoice(ctx, audio, filename)
	}

	// 1. Check if model is "native-windows"
	if o.providerOptions.model.ID == "native-windows" || o.providerOptions.model.APIModel == "native-windows" {
		return o.transcribeNativeWindows(ctx, audio, filename)
	}

	if o.providerOptions.model.Provider == models.ProviderAliyun {
		return o.transcribeAliyun(ctx, audio, filename)
	}

	params := openai.AudioTranscriptionNewParams{
		File:  bytes.NewReader(audio),
		Model: openai.AudioModel(o.providerOptions.model.APIModel),
	}

	resp, err := o.client.Audio.Transcriptions.New(ctx, params)
	if err != nil {
		return "", err
	}
	return resp.Text, nil
}

func (o *openaiClient) transcribeAliyun(ctx context.Context, audio []byte, filename string) (string, error) {
	// Always use qwen3-asr-flash for transcription as it is the most reliable ASR model for Aliyun
	// and supports the multimodal endpoint with base64 data URIs.
	model := "qwen3-asr-flash"

	// Determine endpoint type
	// 1. Native ASR Endpoint (Paraformer, SenseVoice)
	isNativeASR := strings.Contains(strings.ToLower(model), "paraformer") || strings.Contains(strings.ToLower(model), "sensevoice")

	// 2. Multimodal ASR (Qwen-Audio, Qwen3-ASR) - Uses MultiModalConversation
	// Note: Qwen-Audio needs a prompt, Qwen3-ASR usually doesn't or accepts empty system/user content structure.

	// Detect MIME type (Common for all)
	mimeType := "audio/wav" // Default fallback
	ext := strings.ToLower(filename)
	if strings.HasSuffix(ext, ".mp3") {
		mimeType = "audio/mp3"
	} else if strings.HasSuffix(ext, ".webm") {
		mimeType = "audio/webm"
	} else if strings.HasSuffix(ext, ".m4a") {
		mimeType = "audio/m4a"
	} else if strings.HasSuffix(ext, ".aac") {
		mimeType = "audio/aac"
	}

	encoded := base64.StdEncoding.EncodeToString(audio)
	// Native ASR expects raw base64 or file URL, NOT data URI
	// Multimodal expects data URI usually, but let's check docs.
	// For Qwen-Audio/VL, data URI is standard.

	// BUT for Native ASR (SenseVoice), "audio" field is URL or base64?
	// Docs for SenseVoice: https://help.aliyun.com/zh/model-studio/developer-reference/sensevoice-api
	// Input.Audio: "Supports HTTP/HTTPS URL or Base64 encoded audio data."
	// If Base64, it should NOT include the data prefix for this specific endpoint structure.

	dataURI := fmt.Sprintf("data:%s;base64,%s", mimeType, encoded)

	var url string
	var payload interface{}

	if isNativeASR {
		// Native ASR Endpoint
		url = "https://dashscope.aliyuncs.com/api/v1/services/audio/asr/sentence-recognition/generation"
		payload = map[string]interface{}{
			"model": model,
			"input": map[string]interface{}{
				// For Native ASR, if using base64, we might need just the raw string or specific format?
				// Checking docs: usually raw base64 string without data: prefix is safer if we say parameters.format matches.
				// However, some APIs accept data URI.
				// Let's try raw base64 first as that is more standard for "audio content" fields.
				"audio": encoded, // Changed from dataURI to raw encoded
			},
			"parameters": map[string]interface{}{
				// We must specify format if we send raw bytes
				// Since we record as webm in frontend, we should say webm?
				// Frontend sends "recording.webm".
				// Let's infer format from extension or mimeType.
				"format": strings.TrimPrefix(mimeType, "audio/"),
			},
		}
	} else {
		// Multimodal Endpoint (Qwen-Audio, Qwen3-ASR)
		url = "https://dashscope.aliyuncs.com/api/v1/services/aigc/multimodal-generation/generation"

		messages := []map[string]interface{}{}

		// Qwen3-ASR supports 'system' role for context
		if strings.Contains(model, "qwen3-asr") {
			messages = append(messages, map[string]interface{}{
				"role": "system",
				"content": []map[string]interface{}{
					{"text": ""}, // Empty context by default
				},
			})
		}

		userContent := []map[string]interface{}{
			{
				"audio": dataURI,
			},
		}

		// Only add text prompt for generic Qwen-Audio, Qwen3-ASR usually doesn't need it or might get confused
		if !strings.Contains(model, "qwen3-asr") {
			userContent = append(userContent, map[string]interface{}{
				"text": "Transcribe this audio verbatim.",
			})
		}

		messages = append(messages, map[string]interface{}{
			"role":    "user",
			"content": userContent,
		})

		params := map[string]interface{}{
			"result_format": "message",
		}

		// Add asr_options for Qwen3-ASR
		if strings.Contains(model, "qwen3-asr") {
			// Using generic map structure to allow easy extension
			params["asr_options"] = map[string]interface{}{
				"enable_lid": true,
				"enable_itn": false,
			}
		}

		payload = map[string]interface{}{
			"model": model,
			"input": map[string]interface{}{
				"messages": messages,
			},
			"parameters": params,
		}
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return "", fmt.Errorf("failed to marshal payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+o.providerOptions.apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to send request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("aliyun api error (status %d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Output struct {
			// For Multimodal
			Choices []struct {
				Message struct {
					Content []struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"message"`
			} `json:"choices"`
			// For ASR
			Sentences []struct {
				Text string `json:"text"`
			} `json:"sentences"`
		} `json:"output"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	// Handle ASR Response
	if isNativeASR {
		if len(result.Output.Sentences) > 0 {
			var sb strings.Builder
			for _, s := range result.Output.Sentences {
				sb.WriteString(s.Text)
			}
			return sb.String(), nil
		}
		return "", nil
	}

	// Handle Multimodal Response
	if len(result.Output.Choices) > 0 && len(result.Output.Choices[0].Message.Content) > 0 {
		return result.Output.Choices[0].Message.Content[0].Text, nil
	}

	return "", fmt.Errorf("empty response from aliyun")
}

func (o *openaiClient) generateSpeech(ctx context.Context, text string, voice string) ([]byte, error) {
	if o.providerOptions.model.Provider == models.ProviderAliyun {
		return o.generateSpeechAliyun(ctx, text, voice)
	}

	params := openai.AudioSpeechNewParams{
		Model: openai.SpeechModelTTS1,
		Input: text,
		Voice: openai.AudioSpeechNewParamsVoiceAlloy,
	}

	// Map voices if provided
	if voice != "" {
		switch voice {
		case "alloy":
			params.Voice = openai.AudioSpeechNewParamsVoiceAlloy
		case "echo":
			params.Voice = openai.AudioSpeechNewParamsVoiceEcho
		case "fable":
			// params.Voice = openai.AudioSpeechNewParamsVoiceFable
			params.Voice = openai.AudioSpeechNewParamsVoice("fable")
		case "onyx":
			// params.Voice = openai.AudioSpeechNewParamsVoiceOnyx
			params.Voice = openai.AudioSpeechNewParamsVoice("onyx")
		case "nova":
			// params.Voice = openai.AudioSpeechNewParamsVoiceNova
			params.Voice = openai.AudioSpeechNewParamsVoice("nova")
		case "shimmer":
			params.Voice = openai.AudioSpeechNewParamsVoiceShimmer
		}
	}

	resp, err := o.client.Audio.Speech.New(ctx, params)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

func isAliyunDynamicContextMessage(msg message.Message) bool {
	if msg.Role != message.System {
		return false
	}

	text := msg.Content().String()
	if text == "" {
		return false
	}

	return strings.Contains(text, "[SYSTEM INFO]") ||
		strings.Contains(text, "[TURN CONTEXT]") ||
		strings.Contains(text, "[WORKING STATE]") ||
		strings.Contains(text, "[CONTEXT INFO]")
}

func isAliyunVolatileLeadingSystemMessage(msg message.Message) bool {
	if msg.Role != message.System {
		return false
	}

	text := msg.Content().String()
	if text == "" {
		return false
	}

	return isAliyunDynamicContextMessage(msg) ||
		strings.Contains(text, "[MEMORY CAPSULE]") ||
		strings.Contains(text, "[WORKING STATE]")
}

func aliyunTextPart(text string, cache bool) map[string]interface{} {
	part := map[string]interface{}{
		"type": "text",
		"text": text,
	}
	if cache {
		part["cache_control"] = map[string]interface{}{
			"type": "ephemeral",
		}
	}
	return part
}

func buildAliyunLeadingSystemMessages(messages []message.Message, markerBudget int) ([]map[string]interface{}, int, int) {
	leadingMsgs := make([]map[string]interface{}, 0, 4)
	startIdx := 0
	markersUsed := 0

	for i, msg := range messages {
		if msg.Role != message.System {
			break
		}

		content := msg.Content().String()
		if content != "" {
			cacheMsg := !isAliyunVolatileLeadingSystemMessage(msg) && markersUsed < markerBudget
			if cacheMsg {
				markersUsed++
				leadingMsgs = append(leadingMsgs, map[string]interface{}{
					"role":    "system",
					"content": []map[string]interface{}{aliyunTextPart(content, true)},
				})
			} else {
				leadingMsgs = append(leadingMsgs, map[string]interface{}{
					"role":    "system",
					"content": content,
				})
			}
		}
		startIdx = i + 1
	}

	return leadingMsgs, startIdx, markersUsed
}

func findAliyunStableEndIndex(messages []message.Message, startIdx int) int {
	if len(messages) == 0 {
		return 0
	}

	stableEndIndex := len(messages) - 1
	for stableEndIndex >= startIdx && isAliyunDynamicContextMessage(messages[stableEndIndex]) {
		stableEndIndex--
	}

	if stableEndIndex < startIdx {
		return -1
	}

	return stableEndIndex
}

func findAliyunHistoryBoundaryIndex(messages []message.Message, startIdx int, stableEndIndex int) int {
	if stableEndIndex <= startIdx {
		return -1
	}

	latestUserIndex := -1
	for i := stableEndIndex; i >= startIdx; i-- {
		if messages[i].Role == message.User {
			latestUserIndex = i
			break
		}
	}

	if latestUserIndex <= startIdx {
		return -1
	}

	return latestUserIndex - 1
}

func (o *openaiClient) shouldEnableAliyunNativeThinking(messages []message.Message, tools []tools.BaseTool) bool {
	if !o.providerOptions.model.CanReason {
		return false
	}
	if len(tools) > 0 {
		return false
	}
	for _, msg := range messages {
		if msg.Role != message.Assistant {
			continue
		}
		hasVisibleAssistantState := strings.TrimSpace(msg.Content().String()) != "" || len(finishedToolCallsForHistory(msg.ToolCalls())) > 0
		if hasVisibleAssistantState && strings.TrimSpace(msg.ReasoningContent().Thinking) == "" {
			return false
		}
	}
	return true
}

func (o *openaiClient) shouldSendAliyunThinkingSwitch() bool {
	if o.providerOptions.model.Provider == models.ProviderAliyun {
		return true
	}
	baseURL := strings.ToLower(strings.TrimSpace(o.options.baseURL))
	return strings.Contains(baseURL, "dashscope.aliyuncs.com") || strings.Contains(baseURL, "aliyun")
}

func (o *openaiClient) buildAliyunCompatiblePayload(messages []message.Message, tools []tools.BaseTool, stream bool) map[string]interface{} {
	const aliyunMarkerBudget = 4
	enableThinking := o.shouldEnableAliyunNativeThinking(messages, tools)

	payload := map[string]interface{}{
		"model":    o.providerOptions.model.APIModel,
		"stream":   stream,
		"messages": []map[string]interface{}{},
	}
	if stream {
		payload["stream_options"] = map[string]interface{}{
			"include_usage": true,
		}
	}

	if o.providerOptions.maxTokens > 0 {
		payload["max_tokens"] = o.providerOptions.maxTokens
	}
	if enableThinking {
		payload["enable_thinking"] = true
	} else if o.shouldSendAliyunThinkingSwitch() {
		payload["enable_thinking"] = false
	}
	if o.providerOptions.model.DefaultTemperature != nil {
		payload["temperature"] = *o.providerOptions.model.DefaultTemperature
	} else {
		payload["temperature"] = 0.7
	}

	leadingSystemMsgs, startIdx, markersUsed := buildAliyunLeadingSystemMessages(messages, aliyunMarkerBudget)
	msgs := append([]map[string]interface{}{}, leadingSystemMsgs...)

	stableEndIndex := findAliyunStableEndIndex(messages, startIdx)
	checkpointsToMark := make(map[int]bool)
	remainingMarkers := aliyunMarkerBudget - markersUsed
	historyBoundaryIndex := findAliyunHistoryBoundaryIndex(messages, startIdx, stableEndIndex)
	if remainingMarkers > 0 && historyBoundaryIndex >= startIdx {
		checkpointsToMark[historyBoundaryIndex] = true
		remainingMarkers--
	}
	if remainingMarkers > 0 && stableEndIndex >= startIdx && !checkpointsToMark[stableEndIndex] {
		checkpointsToMark[stableEndIndex] = true
		remainingMarkers--
	}

	type toolMsgInfo struct {
		Index  int
		Length int
	}
	var largeToolMsgs []toolMsgInfo
	for i := startIdx; i < stableEndIndex; i++ {
		msg := messages[i]
		if msg.Role == message.Tool && i != stableEndIndex {
			contentLen := len(msg.Content().String())
			if contentLen > 512 {
				largeToolMsgs = append(largeToolMsgs, toolMsgInfo{Index: i, Length: contentLen})
			}
		}
	}
	sort.Slice(largeToolMsgs, func(i, j int) bool {
		return largeToolMsgs[i].Length > largeToolMsgs[j].Length
	})
	for i := 0; i < len(largeToolMsgs) && i < remainingMarkers; i++ {
		checkpointsToMark[largeToolMsgs[i].Index] = true
		logging.Info("Caching Large Tool Output", "index", largeToolMsgs[i].Index, "len", largeToolMsgs[i].Length)
	}

	pendingToolCalls := make(map[string]bool)
	for i := startIdx; i < len(messages); i++ {
		msg := messages[i]
		if msg.Role != message.Tool && len(pendingToolCalls) > 0 {
			for id := range pendingToolCalls {
				msgs = append(msgs, map[string]interface{}{
					"role":         "tool",
					"tool_call_id": id,
					"content":      "Tool execution missing or interrupted.",
				})
			}
			pendingToolCalls = make(map[string]bool)
		}

		role := string(msg.Role)
		contentStr := msg.Content().String()
		if msg.Role == message.System {
			role = "user"
			contentStr = "[System Injection]\n" + contentStr
		}

		m := map[string]interface{}{
			"role": role,
		}
		if checkpointsToMark[i] {
			m["content"] = []map[string]interface{}{aliyunTextPart(contentStr, true)}
		} else {
			m["content"] = contentStr
		}
		if msg.Role == message.Assistant {
			if reasoning := strings.TrimSpace(msg.ReasoningContent().Thinking); reasoning != "" {
				m["reasoning_content"] = reasoning
			}
		}

		finishedToolCalls := finishedToolCallsForHistory(msg.ToolCalls())
		if len(finishedToolCalls) > 0 {
			tcs := []map[string]interface{}{}
			for _, tc := range finishedToolCalls {
				tcs = append(tcs, map[string]interface{}{
					"id":   tc.ID,
					"type": "function",
					"function": map[string]interface{}{
						"name":      tc.Name,
						"arguments": tc.Input,
					},
				})
				pendingToolCalls[tc.ID] = true
			}
			m["tool_calls"] = tcs
		}

		if msg.Role == message.Assistant && len(msg.ToolCalls()) > 0 && len(finishedToolCalls) == 0 && strings.TrimSpace(contentStr) == "" {
			continue
		}

		if msg.Role == message.Tool {
			for _, res := range msg.ToolResults() {
				if !pendingToolCalls[res.ToolCallID] {
					continue
				}
				delete(pendingToolCalls, res.ToolCallID)
				tm := map[string]interface{}{
					"role":         "tool",
					"tool_call_id": res.ToolCallID,
				}
				if checkpointsToMark[i] {
					tm["content"] = []map[string]interface{}{aliyunTextPart(res.Content, true)}
				} else {
					tm["content"] = res.Content
				}
				msgs = append(msgs, tm)
			}
			continue
		}

		msgs = append(msgs, m)
	}

	if len(pendingToolCalls) > 0 {
		for id := range pendingToolCalls {
			msgs = append(msgs, map[string]interface{}{
				"role":         "tool",
				"tool_call_id": id,
				"content":      "Tool execution missing or interrupted.",
			})
		}
	}

	payload["messages"] = msgs

	if len(tools) > 0 {
		ts := []map[string]interface{}{}
		for _, t := range tools {
			info := t.Info()
			required := info.Required
			if required == nil {
				required = []string{}
			}
			ts = append(ts, map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name":        info.Name,
					"description": info.Description,
					"parameters":  normalizedToolObjectSchema(info.Parameters, required),
				},
			})
		}
		payload["tools"] = ts
	}

	return payload
}

func (o *openaiClient) sendAliyun(ctx context.Context, messages []message.Message, tools []tools.BaseTool, enableAliyunCacheHeader bool) (*ProviderResponse, error) {
	payload := o.buildAliyunCompatiblePayload(messages, tools, false)
	appendRequestMetricsRecord(ctx, o.providerOptions.model.APIModel, "aliyun-compatible-chat-completions", messages, tools)
	bodyBytes, _ := json.Marshal(payload)

	if cfg := config.Get(); cfg != nil && cfg.Debug {
		logging.Debug("Raw Compatible Request", "body_len", len(bodyBytes))
	}

	httpClient := &http.Client{}
	attempts := 0

	var completion struct {
		Choices []struct {
			Message struct {
				Content          string `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens          int64 `json:"prompt_tokens"`
			CompletionTokens      int64 `json:"completion_tokens"`
			PromptCacheHitTokens  int64 `json:"prompt_cache_hit_tokens"`
			PromptCacheMissTokens int64 `json:"prompt_cache_miss_tokens"`
			PromptTokensDetails   struct {
				CachedTokens int64 `json:"cached_tokens"`
			} `json:"prompt_tokens_details"`
		} `json:"usage"`
	}

	for {
		attempts++

		req, err := http.NewRequestWithContext(ctx, "POST", o.chatCompletionsURL(), bytes.NewBuffer(bodyBytes))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Authorization", "Bearer "+o.providerOptions.apiKey)
		req.Header.Set("Content-Type", "application/json")
		if enableAliyunCacheHeader {
			req.Header.Set("X-DashScope-WorkFlow", "enable_cache")
		}

		resp, err := httpClient.Do(req)
		if err != nil {
			retry, after, retryErr := o.shouldRetryAliyunCompatible(attempts, 0, nil, err)
			if retryErr != nil {
				return nil, retryErr
			}
			if retry {
				logging.WarnPersist(fmt.Sprintf("Retrying Aliyun compatible request... attempt %d of %d", attempts, maxRetries), logging.PersistTimeArg, time.Millisecond*time.Duration(after+100))
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(after) * time.Millisecond):
					continue
				}
			}
			return nil, retryErr
		}

		if resp.StatusCode != http.StatusOK {
			errBody, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			err = fmt.Errorf("raw completion error %d: %s", resp.StatusCode, string(errBody))
			retry, after, retryErr := o.shouldRetryAliyunCompatible(attempts, resp.StatusCode, resp.Header, err)
			if retryErr != nil {
				return nil, retryErr
			}
			if retry {
				logging.WarnPersist(fmt.Sprintf("Retrying Aliyun compatible request... attempt %d of %d", attempts, maxRetries), logging.PersistTimeArg, time.Millisecond*time.Duration(after+100))
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(after) * time.Millisecond):
					continue
				}
			}
			return nil, retryErr
		}

		completion = struct {
			Choices []struct {
				Message struct {
					Content          string `json:"content"`
					ReasoningContent string `json:"reasoning_content"`
					ToolCalls        []struct {
						ID       string `json:"id"`
						Type     string `json:"type"`
						Function struct {
							Name      string `json:"name"`
							Arguments string `json:"arguments"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
			Usage struct {
				PromptTokens          int64 `json:"prompt_tokens"`
				CompletionTokens      int64 `json:"completion_tokens"`
				PromptCacheHitTokens  int64 `json:"prompt_cache_hit_tokens"`
				PromptCacheMissTokens int64 `json:"prompt_cache_miss_tokens"`
				PromptTokensDetails   struct {
					CachedTokens int64 `json:"cached_tokens"`
				} `json:"prompt_tokens_details"`
			} `json:"usage"`
		}{}

		if err := json.NewDecoder(resp.Body).Decode(&completion); err != nil {
			_ = resp.Body.Close()
			retry, after, retryErr := o.shouldRetryAliyunCompatible(attempts, resp.StatusCode, resp.Header, err)
			if retryErr != nil {
				return nil, retryErr
			}
			if retry {
				logging.WarnPersist(fmt.Sprintf("Retrying Aliyun compatible request... attempt %d of %d", attempts, maxRetries), logging.PersistTimeArg, time.Millisecond*time.Duration(after+100))
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(time.Duration(after) * time.Millisecond):
					continue
				}
			}
			return nil, retryErr
		}
		_ = resp.Body.Close()
		break
	}

	if len(completion.Choices) == 0 {
		return nil, fmt.Errorf("raw completion returned no choices")
	}

	choice := completion.Choices[0]
	toolCalls := make([]message.ToolCall, 0, len(choice.Message.ToolCalls))
	for _, tc := range choice.Message.ToolCalls {
		callType := tc.Type
		if callType == "" {
			callType = "function"
		}
		toolCalls = append(toolCalls, message.ToolCall{
			ID:       tc.ID,
			Name:     tc.Function.Name,
			Input:    tc.Function.Arguments,
			Type:     callType,
			Finished: true,
		})
	}

	finishReason := o.finishReason(choice.FinishReason)
	if len(toolCalls) > 0 {
		finishReason = message.FinishReasonToolUse
	}

	cachedTokens := completion.Usage.PromptTokensDetails.CachedTokens
	if cachedTokens == 0 {
		cachedTokens = completion.Usage.PromptCacheHitTokens
	}

	usageBytes, _ := json.Marshal(completion.Usage)
	logging.Info("Aliyun Compatible Non-Stream Usage", "json", string(usageBytes), "cached_tokens", cachedTokens)

	return &ProviderResponse{
		Content:   choice.Message.Content,
		Thinking:  choice.Message.ReasoningContent,
		ToolCalls: toolCalls,
		Usage: TokenUsage{
			InputTokens:     completion.Usage.PromptTokens - cachedTokens,
			OutputTokens:    completion.Usage.CompletionTokens,
			CacheReadTokens: cachedTokens,
		},
		FinishReason: finishReason,
	}, nil
}

// streamAliyun handles raw streaming for Aliyun to support cache_control
func (o *openaiClient) streamAliyun(ctx context.Context, messages []message.Message, tools []tools.BaseTool, enableAliyunCacheHeader bool) <-chan ProviderEvent {
	// Buffered channel to prevent blocking the reader loop
	eventChan := make(chan ProviderEvent, 100)
	appendRequestMetricsRecord(ctx, o.providerOptions.model.APIModel, "aliyun-compatible-chat-completions", messages, tools)

	go func() {
		defer close(eventChan)

		payload := o.buildAliyunCompatiblePayload(messages, tools, true)

		// Convert Tools
		if len(tools) > 0 {
			ts := []map[string]interface{}{}
			for _, t := range tools {
				info := t.Info()
				required := info.Required
				if required == nil {
					required = []string{}
				}
				ts = append(ts, map[string]interface{}{
					"type": "function",
					"function": map[string]interface{}{
						"name":        info.Name,
						"description": info.Description,
						"parameters":  normalizedToolObjectSchema(info.Parameters, required),
					},
				})
			}
			payload["tools"] = ts
		}

		// 2. Send Request
		bodyBytes, _ := json.Marshal(payload)

		// Debug Log
		if cfg := config.Get(); cfg != nil && cfg.Debug {
			logging.Debug("Raw Compatible Request", "body_len", len(bodyBytes))
		}

		req, err := http.NewRequestWithContext(ctx, "POST", o.chatCompletionsURL(), bytes.NewBuffer(bodyBytes))
		if err != nil {
			eventChan <- ProviderEvent{Type: EventError, Error: err}
			return
		}

		req.Header.Set("Authorization", "Bearer "+o.providerOptions.apiKey)
		req.Header.Set("Content-Type", "application/json")

		if enableAliyunCacheHeader {
			req.Header.Set("X-DashScope-WorkFlow", "enable_cache")
		}

		client := &http.Client{} // Or use o.client.HTTPClient if accessible (it's hidden in openai-go)
		resp, err := client.Do(req)
		if err != nil {
			eventChan <- ProviderEvent{Type: EventError, Error: err}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			errBody, _ := io.ReadAll(resp.Body)
			rawErrBody := string(errBody)
			debugLogAliyunStreamPayload("http_status", resp.StatusCode, rawErrBody)
			eventChan <- ProviderEvent{Type: EventError, Error: buildAliyunStreamError(resp.StatusCode, rawErrBody)}
			return
		}

		// 3. Stream Response
		reader := bufio.NewReader(resp.Body)
		currentContent := ""
		currentThinking := ""
		// toolCalls := []message.ToolCall{} // Accumulator for tool calls?
		// Note: Raw stream parsing for tool calls is complex (delta accumulation).
		// For this hotfix, we mainly care about text content and usage.
		// If tool calls are split across chunks, we need to reconstruct them.
		activeToolCalls := map[int]*toolCallAccumulator{}

		var finalUsage *TokenUsage
		finalFinishReason := message.FinishReasonEndTurn
		parsedChunkCount := 0
		rawErrorSnippet := ""
		nextAliyunData := func(line string) (string, bool) {
			line = strings.TrimSpace(line)
			if line == "" {
				return "", false
			}

			if !strings.HasPrefix(line, "data:") {
				rawErrorSnippet = appendAliyunStreamErrorSnippet(rawErrorSnippet, line)
				return "", false
			}

			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "" {
				return "", false
			}
			if data == "[DONE]" {
				return "", true
			}

			return data, false
		}
		handleAliyunData := func(data string) error {
			var chunk struct {
				Choices []struct {
					Delta struct {
						Content          string `json:"content"`
						ReasoningContent string `json:"reasoning_content"`
						ToolCalls        []struct {
							Index    int    `json:"index"`
							ID       string `json:"id"`
							Function struct {
								Name      string `json:"name"`
								Arguments string `json:"arguments"`
							} `json:"function"`
						} `json:"tool_calls"`
					} `json:"delta"`
					FinishReason string `json:"finish_reason"`
				} `json:"choices"`
				Usage *struct {
					PromptTokens          int `json:"prompt_tokens"`
					CompletionTokens      int `json:"completion_tokens"`
					TotalTokens           int `json:"total_tokens"`
					PromptCacheHitTokens  int `json:"prompt_cache_hit_tokens"`
					PromptCacheMissTokens int `json:"prompt_cache_miss_tokens"`
					PromptTokensDetails   struct {
						CachedTokens int `json:"cached_tokens"`
					} `json:"prompt_tokens_details"`
				} `json:"usage"`
			}

			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				logging.Warn("Failed to parse Aliyun chunk", "error", err, "data", data)
				rawErrorSnippet = appendAliyunStreamErrorSnippet(rawErrorSnippet, data)
				return nil
			}

			if len(chunk.Choices) == 0 {
				if _, _, ok := extractAliyunStreamErrorDetails(data); ok {
					debugLogAliyunStreamPayload("sse_data", 0, data)
					return buildAliyunStreamError(0, data)
				}
			}
			parsedChunkCount++

			if chunk.Usage != nil {
				usageBytes, _ := json.Marshal(chunk.Usage)
				logging.Info("Aliyun Raw Usage", "json", string(usageBytes))

				cachedTokens := int64(chunk.Usage.PromptTokensDetails.CachedTokens)
				if cachedTokens == 0 {
					cachedTokens = int64(chunk.Usage.PromptCacheHitTokens)
				}

				usage := &TokenUsage{
					InputTokens:     int64(chunk.Usage.PromptTokens) - cachedTokens,
					OutputTokens:    int64(chunk.Usage.CompletionTokens),
					CacheReadTokens: cachedTokens,
				}
				finalUsage = usage
			}

			if len(chunk.Choices) == 0 {
				return nil
			}

			choice := chunk.Choices[0]
			delta := choice.Delta
			if choice.FinishReason != "" {
				finalFinishReason = o.finishReason(choice.FinishReason)
			}

			reasoning := delta.ReasoningContent
			if reasoning == "" {
				var m map[string]interface{}
				if err := json.Unmarshal([]byte(data), &m); err == nil {
					if choices, ok := m["choices"].([]interface{}); ok && len(choices) > 0 {
						if choiceMap, ok := choices[0].(map[string]interface{}); ok {
							if deltaMap, ok := choiceMap["delta"].(map[string]interface{}); ok {
								if rc, ok := deltaMap["reasoning_content"].(string); ok {
									reasoning = rc
								}
								if reasoning == "" {
									if rc, ok := deltaMap["reasoning"].(string); ok {
										reasoning = rc
									}
								}
							}
						}
					}
				}
			}

			if reasoning != "" {
				currentThinking += reasoning
				eventChan <- ProviderEvent{Type: EventThinkingDelta, Thinking: reasoning}
			}

			if delta.Content != "" {
				currentContent += delta.Content
				eventChan <- ProviderEvent{Type: EventContentDelta, Content: delta.Content}
			}

			if len(delta.ToolCalls) > 0 {
				deltas := make([]streamingToolCallDelta, 0, len(delta.ToolCalls))
				for _, tc := range delta.ToolCalls {
					deltas = append(deltas, streamingToolCallDelta{
						Index:     tc.Index,
						ID:        tc.ID,
						Name:      tc.Function.Name,
						Arguments: tc.Function.Arguments,
					})
				}
				appendStreamingToolCallDeltas(deltas, activeToolCalls, eventChan)
			}

			return nil
		}
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					data, done := nextAliyunData(line)
					if data != "" {
						if handleErr := handleAliyunData(data); handleErr != nil {
							eventChan <- ProviderEvent{Type: EventError, Error: handleErr}
							return
						}
					}
					if done {
						break
					}
					break
				}
				eventChan <- ProviderEvent{Type: EventError, Error: err}
				return
			}

			data, done := nextAliyunData(line)
			if done {
				break
			}
			if data == "" {
				continue
			}
			if handleErr := handleAliyunData(data); handleErr != nil {
				eventChan <- ProviderEvent{Type: EventError, Error: handleErr}
				return
			}
		}

		if parsedChunkCount == 0 {
			debugLogAliyunStreamPayload("raw_snippet", 0, rawErrorSnippet)
			eventChan <- ProviderEvent{
				Type:  EventError,
				Error: buildAliyunStreamError(0, rawErrorSnippet),
			}
			return
		}

		finalToolCalls := []message.ToolCall{}
		for _, tc := range activeToolCalls {
			args := tc.Args
			if !json.Valid([]byte(args)) {
				logging.Warn("Truncated tool call JSON detected", "tool", tc.Name, "args_len", len(args))
				args = fmt.Sprintf(`{"_truncated_error": "Tool call arguments were truncated at %d chars due to output token limit. The content is too large for a single tool call. Use 'edit' or 'patch' for targeted chunked changes, or reduce the payload and retry with smaller semantic chunks."}`, len(args))
			}
			finalToolCalls = append(finalToolCalls, message.ToolCall{
				ID:       tc.ID,
				Name:     tc.Name,
				Input:    args,
				Type:     "function",
				Finished: true,
			})
		}
		if len(finalToolCalls) > 0 {
			finalFinishReason = message.FinishReasonToolUse
		}

		usage := TokenUsage{}
		if finalUsage != nil {
			usage = *finalUsage
		}

		eventChan <- ProviderEvent{
			Type: EventComplete,
			Response: &ProviderResponse{
				Content:      currentContent,
				Thinking:     currentThinking,
				ToolCalls:    finalToolCalls,
				Usage:        usage,
				FinishReason: finalFinishReason,
			},
		}
	}()

	return eventChan
}

func (o *openaiClient) generateSpeechAliyun(ctx context.Context, text string, voice string) ([]byte, error) {
	// Aliyun CosyVoice WebSocket API
	wsURL := "wss://dashscope.aliyuncs.com/api-ws/v1/inference/"
	apiKey := o.providerOptions.apiKey

	model := "cosyvoice-v1"
	if o.providerOptions.model.APIModel != "" {
		model = o.providerOptions.model.APIModel
	}
	if voice == "" {
		if strings.Contains(model, "v3") {
			voice = "longanhuan"
		} else {
			voice = "longxiaochun" // Default to beautiful female voice
		}
	} else if strings.Contains(model, "v3") && voice == "longxiaochun" {
		// Auto-upgrade longxiaochun to longanhuan for v3 if passed explicitly (legacy frontend compatibility)
		voice = "longanhuan"
	}

	// Connect to WebSocket
	header := http.Header{}
	header.Set("Authorization", "Bearer "+apiKey)
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to aliyun websocket: %w", err)
	}
	defer conn.Close()

	taskID := strings.ReplaceAll(uuid.New().String(), "-", "")

	// 1. Send run-task
	runTaskPayload := map[string]interface{}{
		"header": map[string]interface{}{
			"action":    "run-task",
			"task_id":   taskID,
			"streaming": "duplex",
		},
		"payload": map[string]interface{}{
			"task_group": "audio",
			"task":       "tts",
			"function":   "SpeechSynthesizer",
			"model":      model,
			"parameters": map[string]interface{}{
				"text_type": "PlainText",
				"voice":     voice,
				"format":    "mp3",
			},
			"input": map[string]interface{}{
				"text": text,
			},
		},
	}
	// Log the run-task payload for debugging
	runTaskBytes, _ := json.Marshal(runTaskPayload)
	logging.Debug(fmt.Sprintf("Sending run-task: %s", string(runTaskBytes)))

	if err := conn.WriteJSON(runTaskPayload); err != nil {
		return nil, fmt.Errorf("failed to send run-task: %w", err)
	}

	// 2. Wait for task-started
	var startedMsg struct {
		Header struct {
			Event        string `json:"event"`
			ErrorMessage string `json:"error_message"`
		} `json:"header"`
	}
	// Read until we get task-started or error
	if err := conn.ReadJSON(&startedMsg); err != nil {
		return nil, fmt.Errorf("failed to read task-started: %w", err)
	}
	if startedMsg.Header.Event != "task-started" {
		errMsg := startedMsg.Header.ErrorMessage
		if errMsg == "" {
			errMsg = "unknown error"
		}
		return nil, fmt.Errorf("expected task-started, got %s: %s", startedMsg.Header.Event, errMsg)
	}

	// 3. Send finish-task
	finishTaskPayload := map[string]interface{}{
		"header": map[string]interface{}{
			"action":    "finish-task",
			"task_id":   taskID,
			"streaming": "duplex",
		},
		"payload": map[string]interface{}{
			"input": map[string]interface{}{},
		},
	}
	if err := conn.WriteJSON(finishTaskPayload); err != nil {
		return nil, fmt.Errorf("failed to send finish-task: %w", err)
	}

	// 5. Read audio loop
	var audioBuffer bytes.Buffer
	for {
		messageType, p, err := conn.ReadMessage()
		if err != nil {
			return nil, fmt.Errorf("read error: %w", err)
		}

		if messageType == websocket.BinaryMessage {
			audioBuffer.Write(p)
		} else if messageType == websocket.TextMessage {
			var msg struct {
				Header struct {
					Event        string `json:"event"`
					ErrorMessage string `json:"error_message"`
				} `json:"header"`
			}
			if err := json.Unmarshal(p, &msg); err != nil {
				continue // Ignore malformed JSON
			}

			if msg.Header.Event == "task-finished" {
				break
			}
			if msg.Header.Event == "task-failed" {
				return nil, fmt.Errorf("task failed: %s", msg.Header.ErrorMessage)
			}
		}
	}

	return audioBuffer.Bytes(), nil
}

func (o *openaiClient) shouldRetry(attempts int, err error) (bool, int64, error) {
	var apierr *openai.Error
	if !errors.As(err, &apierr) {
		return false, 0, err
	}

	if apierr.StatusCode != 429 && apierr.StatusCode != 500 {
		return false, 0, err
	}

	if attempts > maxRetries {
		return false, 0, fmt.Errorf("maximum retry attempts reached for rate limit: %d retries", maxRetries)
	}

	retryMs := 0
	retryAfterValues := apierr.Response.Header.Values("Retry-After")

	backoffMs := 2000 * (1 << (attempts - 1))
	jitterMs := int(float64(backoffMs) * 0.2)
	retryMs = backoffMs + jitterMs
	if len(retryAfterValues) > 0 {
		if _, err := fmt.Sscanf(retryAfterValues[0], "%d", &retryMs); err == nil {
			retryMs = retryMs * 1000
		}
	}
	return true, int64(retryMs), nil
}

func (o *openaiClient) shouldRetryAliyunCompatible(attempts int, statusCode int, headers http.Header, err error) (bool, int64, error) {
	retryableStatus := statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
	retryableError := shouldRetryAliyunTransportError(err)
	if !retryableStatus && !retryableError {
		return false, 0, err
	}

	if attempts > maxRetries {
		if err != nil {
			return false, 0, fmt.Errorf("maximum retry attempts reached for Aliyun compatible request: %d retries: %w", maxRetries, err)
		}
		return false, 0, fmt.Errorf("maximum retry attempts reached for Aliyun compatible request: %d retries", maxRetries)
	}

	retryMs := 2000 * (1 << (attempts - 1))
	jitterMs := int(float64(retryMs) * 0.2)
	retryMs += jitterMs
	if len(headers) > 0 {
		retryAfterValues := headers.Values("Retry-After")
		if len(retryAfterValues) > 0 {
			if _, scanErr := fmt.Sscanf(retryAfterValues[0], "%d", &retryMs); scanErr == nil {
				retryMs = retryMs * 1000
			}
		}
	}

	return true, int64(retryMs), nil
}

func shouldRetryAliyunTransportError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	lower := strings.ToLower(strings.TrimSpace(err.Error()))
	return strings.Contains(lower, "unexpected eof") ||
		strings.Contains(lower, "connection reset") ||
		strings.Contains(lower, "broken pipe") ||
		strings.Contains(lower, "server closed idle connection")
}

func (o *openaiClient) toolCalls(completion openai.ChatCompletion) []message.ToolCall {
	var toolCalls []message.ToolCall

	if len(completion.Choices) > 0 && len(completion.Choices[0].Message.ToolCalls) > 0 {
		for _, call := range completion.Choices[0].Message.ToolCalls {
			toolCall := message.ToolCall{
				ID:       call.ID,
				Name:     call.Function.Name,
				Input:    call.Function.Arguments,
				Type:     "function",
				Finished: true,
			}
			toolCalls = append(toolCalls, toolCall)
		}
	}

	return toolCalls
}

func (o *openaiClient) usage(completion openai.ChatCompletion) TokenUsage {
	// DEBUG: Log raw usage to investigate Aliyun Cache fields
	usageBytes, _ := json.Marshal(completion.Usage)
	logging.Debug("Raw Usage Data", "json", string(usageBytes))

	cachedTokens := completion.Usage.PromptTokensDetails.CachedTokens
	if cachedTokens == 0 {
		if val, ok := completion.Usage.JSON.ExtraFields["prompt_cache_hit_tokens"]; ok {
			v := reflect.ValueOf(val)
			if v.Kind() == reflect.Struct {
				f := v.FieldByName("raw")
				if f.IsValid() && f.Kind() == reflect.String {
					var num float64
					if err := json.Unmarshal([]byte(f.String()), &num); err == nil {
						cachedTokens = int64(num)
					}
				}
			}
		}
	}
	inputTokens := completion.Usage.PromptTokens - cachedTokens
	if inputTokens < 0 {
		inputTokens = 0
	}

	return TokenUsage{
		InputTokens:         inputTokens,
		OutputTokens:        completion.Usage.CompletionTokens,
		CacheCreationTokens: 0, // OpenAI doesn't provide this directly
		CacheReadTokens:     cachedTokens,
	}
}

func WithOpenAIBaseURL(baseURL string) OpenAIOption {
	return func(options *openaiOptions) {
		options.baseURL = baseURL
	}
}

func WithOpenAIExtraHeaders(headers map[string]string) OpenAIOption {
	return func(options *openaiOptions) {
		options.extraHeaders = headers
	}
}

func WithOpenAIExtraQuery(query map[string]string) OpenAIOption {
	return func(options *openaiOptions) {
		options.extraQuery = query
	}
}

func WithOpenAIDisableCache() OpenAIOption {
	return func(options *openaiOptions) {
		options.disableCache = true
	}
}

func WithReasoningEffort(effort string) OpenAIOption {
	return func(options *openaiOptions) {
		defaultReasoningEffort := "medium"
		switch effort {
		case "low", "medium", "high":
			defaultReasoningEffort = effort
		default:
			logging.Warn("Invalid reasoning effort, using default: medium")
		}
		options.reasoningEffort = defaultReasoningEffort
	}
}
