package agent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/llm/provider"
	llmtools "github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/message"
	"github.com/auramate/auramate/internal/pubsub"
	"github.com/auramate/auramate/internal/session"
)

func TestCompactWorkerResultPrefersSummary(t *testing.T) {
	input := "prefix\n<summary>final concise answer</summary>\ntrailing"
	if got := compactWorkerResult(input); got != "final concise answer" {
		t.Fatalf("expected summary extraction, got %q", got)
	}
}

func TestCompactWorkerResultTruncatesLargePayload(t *testing.T) {
	input := strings.Repeat("A", 7000)
	got := compactWorkerResult(input)
	if len(got) >= len(input) {
		t.Fatalf("expected truncated result, got len=%d original=%d", len(got), len(input))
	}
	if !strings.Contains(got, "worker result truncated") {
		t.Fatalf("expected truncation marker, got %q", got)
	}
}

func TestRecordWorkerResultClearsRecoveredToolErrorOnCompletedResult(t *testing.T) {
	o := &Orchestrator{
		workerMeta: map[string]*workerRuntime{
			"worker-1": {
				Status:           "running",
				LastError:        "Error: tool error Output: file changed after it was read.",
				FailureType:      "tool_error",
				Retryable:        true,
				LastRecoveryNote: "Tool edit failed; continue from latest verified state.",
				Checkpoint: &WorkerCheckpoint{
					Status:           "running",
					LastError:        "Error: tool error Output: file changed after it was read.",
					FailureType:      "tool_error",
					Retryable:        true,
					LastRecoveryNote: "Tool edit failed; continue from latest verified state.",
					ResumeHint:       "retry edit after reading file again",
					PendingGoal:      "finish task",
				},
			},
		},
	}

	o.recordWorkerResult("worker-1", buildWorkerResultEnvelope(workerResultEnvelope{
		Status:  "completed",
		Summary: "task recovered and completed",
	}))

	meta := o.workerMeta["worker-1"]
	if meta.LastError != "" || meta.FailureType != "" || meta.Retryable || meta.LastRecoveryNote != "" {
		t.Fatalf("expected completed result to clear recovered error state, got meta=%+v", meta)
	}
	if meta.Checkpoint == nil {
		t.Fatal("expected checkpoint to remain available")
	}
	if meta.Checkpoint.LastError != "" || meta.Checkpoint.FailureType != "" || meta.Checkpoint.Retryable || meta.Checkpoint.LastRecoveryNote != "" || meta.Checkpoint.ResumeHint != "" || meta.Checkpoint.PendingGoal != "" {
		t.Fatalf("expected completed result to clear checkpoint error state, got checkpoint=%+v", meta.Checkpoint)
	}
	if meta.StructuredStatus != "completed" || meta.StructuredSummary != "task recovered and completed" {
		t.Fatalf("expected completed structured result to be recorded, got status=%q summary=%q", meta.StructuredStatus, meta.StructuredSummary)
	}
}

type artifactValidationProvider struct {
	artifactPath string
	calls        int
}

func (p *artifactValidationProvider) SendMessages(ctx context.Context, messages []message.Message, tools []llmtools.BaseTool) (*provider.ProviderResponse, error) {
	p.calls++
	lastMsg := messages[len(messages)-1].Content().Text
	if strings.Contains(lastMsg, "SYSTEM VALIDATION:") {
		return &provider.ProviderResponse{
			ToolCalls: []message.ToolCall{{ID: "call-write", Name: "fake_write", Input: p.artifactPath, Type: "function", Finished: true}},
		}, nil
	}
	if hasToolResult(messages, "fake_write") {
		return &provider.ProviderResponse{
			Content:      fmt.Sprintf("<worker_result>{\"status\":\"completed\",\"summary\":\"artifact created\",\"artifacts\":[%q],\"retryable\":false,\"failure_type\":\"\",\"evidence_refs\":[]}</worker_result>", p.artifactPath),
			FinishReason: message.FinishReasonEndTurn,
		}, nil
	}
	return &provider.ProviderResponse{
		Content:      fmt.Sprintf("<worker_result>{\"status\":\"completed\",\"summary\":\"claimed before write\",\"artifacts\":[%q],\"retryable\":false,\"failure_type\":\"\",\"evidence_refs\":[]}</worker_result>", p.artifactPath),
		FinishReason: message.FinishReasonEndTurn,
	}, nil
}

type retrySequenceProvider struct {
	responses []retrySequenceStep
	calls     int
	requests  [][]message.Message
}

type retrySequenceStep struct {
	response *provider.ProviderResponse
	err      error
}

type streamingSequenceProvider struct {
	streamResponses [][]provider.ProviderEvent
	streamCalls     int
	sendCalls       int
	requests        [][]message.Message
}

type workerTestSessionService struct {
	*pubsub.Broker[session.Session]
	createCalls         int
	createTaskCalls     int
	lastParentSessionID string
	lastTitle           string
}

func (s *workerTestSessionService) Create(ctx context.Context, title string) (session.Session, error) {
	s.createCalls++
	s.lastTitle = title
	return session.Session{ID: "worker-test-" + title, Title: title}, nil
}

func (s *workerTestSessionService) CreateTaskSession(ctx context.Context, toolCallID, parentSessionID, title string) (session.Session, error) {
	s.createTaskCalls++
	s.lastParentSessionID = parentSessionID
	s.lastTitle = title
	return session.Session{ID: toolCallID, ParentSessionID: parentSessionID, Title: title}, nil
}

func (s *workerTestSessionService) Get(ctx context.Context, id string) (session.Session, error) {
	return session.Session{}, nil
}

func (s *workerTestSessionService) List(ctx context.Context) ([]session.Session, error) {
	return nil, nil
}

func (s *workerTestSessionService) Save(ctx context.Context, sess session.Session) (session.Session, error) {
	return sess, nil
}

func (s *workerTestSessionService) Delete(ctx context.Context, id string) error {
	return nil
}

func (p *retrySequenceProvider) SendMessages(ctx context.Context, messages []message.Message, tools []llmtools.BaseTool) (*provider.ProviderResponse, error) {
	copied := make([]message.Message, len(messages))
	copy(copied, messages)
	p.requests = append(p.requests, copied)
	if p.calls >= len(p.responses) {
		return &provider.ProviderResponse{Content: buildWorkerFailureEnvelope("unexpected extra call", "test_error", false), FinishReason: message.FinishReasonEndTurn}, nil
	}
	step := p.responses[p.calls]
	p.calls++
	return step.response, step.err
}

func (p *retrySequenceProvider) StreamResponse(ctx context.Context, messages []message.Message, tools []llmtools.BaseTool) <-chan provider.ProviderEvent {
	return nil
}

func (p *streamingSequenceProvider) SendMessages(ctx context.Context, messages []message.Message, tools []llmtools.BaseTool) (*provider.ProviderResponse, error) {
	p.sendCalls++
	return nil, fmt.Errorf("SendMessages should not be used when streaming is available")
}

func (p *streamingSequenceProvider) StreamResponse(ctx context.Context, messages []message.Message, tools []llmtools.BaseTool) <-chan provider.ProviderEvent {
	copied := make([]message.Message, len(messages))
	copy(copied, messages)
	p.requests = append(p.requests, copied)
	callIndex := p.streamCalls
	p.streamCalls++
	events := []provider.ProviderEvent{{
		Type: provider.EventComplete,
		Response: &provider.ProviderResponse{
			Content:      buildWorkerFailureEnvelope("unexpected extra stream call", "test_error", false),
			FinishReason: message.FinishReasonEndTurn,
		},
	}}
	if callIndex < len(p.streamResponses) {
		events = p.streamResponses[callIndex]
	}
	ch := make(chan provider.ProviderEvent, len(events))
	go func() {
		defer close(ch)
		for _, event := range events {
			select {
			case <-ctx.Done():
				return
			case ch <- event:
			}
		}
	}()
	return ch
}

func (p *streamingSequenceProvider) Model() models.Model {
	return models.Model{ID: "streaming-sequence-provider", Provider: models.ProviderMock}
}

func (p *streamingSequenceProvider) Transcribe(ctx context.Context, audio []byte, filename string) (string, error) {
	return "", nil
}

func (p *streamingSequenceProvider) GenerateSpeech(ctx context.Context, text string, voice string) ([]byte, error) {
	return nil, nil
}

func (p *retrySequenceProvider) Model() models.Model {
	return models.Model{ID: "retry-sequence-provider", Provider: models.ProviderMock}
}

func (p *retrySequenceProvider) Transcribe(ctx context.Context, audio []byte, filename string) (string, error) {
	return "", nil
}

func (p *retrySequenceProvider) GenerateSpeech(ctx context.Context, text string, voice string) ([]byte, error) {
	return nil, nil
}

func (p *artifactValidationProvider) StreamResponse(ctx context.Context, messages []message.Message, tools []llmtools.BaseTool) <-chan provider.ProviderEvent {
	return nil
}

func (p *artifactValidationProvider) Model() models.Model {
	return models.Model{ID: "artifact-validation-provider", Provider: models.ProviderMock}
}

func (p *artifactValidationProvider) Transcribe(ctx context.Context, audio []byte, filename string) (string, error) {
	return "", nil
}

func (p *artifactValidationProvider) GenerateSpeech(ctx context.Context, text string, voice string) ([]byte, error) {
	return nil, nil
}

type fakeWriteTool struct{}

func (t *fakeWriteTool) Info() llmtools.ToolInfo {
	return llmtools.ToolInfo{Name: "fake_write"}
}

func (t *fakeWriteTool) Run(ctx context.Context, call llmtools.ToolCall) (llmtools.ToolResponse, error) {
	if err := os.MkdirAll(filepath.Dir(call.Input), 0o755); err != nil {
		return llmtools.ToolResponse{}, err
	}
	if err := os.WriteFile(call.Input, []byte("ok\n"), 0o644); err != nil {
		return llmtools.ToolResponse{}, err
	}
	return llmtools.NewTextResponse("written"), nil
}

func hasToolResult(messages []message.Message, toolName string) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != message.Tool {
			continue
		}
		for _, result := range messages[i].ToolResults() {
			if result.Name == toolName && !result.IsError {
				return true
			}
		}
	}
	return false
}

func TestWorkerReplyValidatesDeclaredArtifactsBeforeCompleting(t *testing.T) {
	tempDir := t.TempDir()
	artifactPath := filepath.Join(tempDir, "consumer_report.md")
	provider := &artifactValidationProvider{artifactPath: artifactPath}
	worker := newWorker("worker-1", "tester", "system prompt", provider, pubsub.NewBroker[MultiAgentEvent](), []llmtools.BaseTool{&fakeWriteTool{}})
	worker.history = append(worker.history, message.NewTextMessage(message.System, "system prompt"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	worker.reply(workerTask{ctx: ctx, replyTo: "Orchestrator", taskID: "task-1"})

	if provider.calls < 3 {
		t.Fatalf("expected provider to receive validation retry loop, got %d calls", provider.calls)
	}
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("expected artifact to exist after validation retry, err=%v", err)
	}
	result := worker.GetLastResult()
	structured, ok := parseWorkerResultEnvelope(result)
	if !ok {
		t.Fatalf("expected final worker result envelope, got %q", result)
	}
	if structured.Summary != "artifact created" {
		t.Fatalf("expected final summary to reflect rewritten artifact, got %#v", structured)
	}
	if len(structured.Artifacts) != 1 || structured.Artifacts[0] != artifactPath {
		t.Fatalf("unexpected final artifacts: %#v", structured.Artifacts)
	}
}

func TestWorkerReplyPreservesProviderThinkingInHistory(t *testing.T) {
	provider := &retrySequenceProvider{responses: []retrySequenceStep{
		{response: &provider.ProviderResponse{
			Thinking:     "need to write the file",
			ToolCalls:    []message.ToolCall{{ID: "call-write", Name: "fake_write", Input: filepath.Join(t.TempDir(), "out.txt"), Type: "function", Finished: true}},
			FinishReason: message.FinishReasonToolUse,
		}},
		{response: &provider.ProviderResponse{
			Content:      buildWorkerResultEnvelope(workerResultEnvelope{Status: "completed", Summary: "done"}),
			FinishReason: message.FinishReasonEndTurn,
		}},
	}}
	worker := newWorker("worker-thinking", "tester", "system prompt", provider, pubsub.NewBroker[MultiAgentEvent](), []llmtools.BaseTool{&fakeWriteTool{}})
	worker.history = append(worker.history, message.NewTextMessage(message.System, "system prompt"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	worker.reply(workerTask{ctx: ctx, replyTo: "Orchestrator", taskID: "task-1"})

	if len(provider.requests) < 2 {
		t.Fatalf("expected worker to issue a second provider request, got %d", len(provider.requests))
	}
	found := false
	for _, msg := range provider.requests[1] {
		if msg.Role == message.Assistant && msg.ReasoningContent().Thinking == "need to write the file" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected second provider request to preserve assistant reasoning content, got %#v", provider.requests[1])
	}
}

func TestWorkerReplyUsesStreamingResponseAndExecutesStreamedToolCall(t *testing.T) {
	artifactPath := filepath.Join(t.TempDir(), "streamed-out.txt")
	result := buildWorkerResultEnvelope(workerResultEnvelope{Status: "completed", Summary: "streamed worker completed", Artifacts: []string{artifactPath}})
	provider := &streamingSequenceProvider{streamResponses: [][]provider.ProviderEvent{
		{
			{Type: provider.EventToolUseStart, ToolCall: &message.ToolCall{ID: "call-write", Index: 0, Name: "fake_write", Type: "function"}},
			{Type: provider.EventToolUseDelta, ToolCall: &message.ToolCall{ID: "call-write", Index: 0, Name: "fake_write", Input: artifactPath, Type: "function"}},
			{Type: provider.EventComplete, Response: &provider.ProviderResponse{FinishReason: message.FinishReasonToolUse}},
		},
		{
			{Type: provider.EventContentDelta, Content: result},
			{Type: provider.EventComplete, Response: &provider.ProviderResponse{FinishReason: message.FinishReasonEndTurn}},
		},
	}}
	worker := newWorker("worker-stream", "tester", "system prompt", provider, pubsub.NewBroker[MultiAgentEvent](), []llmtools.BaseTool{&fakeWriteTool{}})
	worker.history = append(worker.history, message.NewTextMessage(message.System, "system prompt"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	worker.reply(workerTask{ctx: ctx, replyTo: "Orchestrator", taskID: "task-stream"})

	if provider.sendCalls != 0 {
		t.Fatalf("expected streaming path only, got %d SendMessages calls", provider.sendCalls)
	}
	if provider.streamCalls != 2 {
		t.Fatalf("expected two streaming provider calls, got %d", provider.streamCalls)
	}
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("expected streamed tool call to create artifact, err=%v", err)
	}
	if got := worker.GetLastResult(); !strings.Contains(got, "streamed worker completed") {
		t.Fatalf("expected final streamed worker result, got %q", got)
	}
}

func TestWorkerReplyRetriesRetryableProviderErrorAndRecovers(t *testing.T) {
	provider := &retrySequenceProvider{responses: []retrySequenceStep{
		{err: fmt.Errorf("503 service unavailable")},
		{response: &provider.ProviderResponse{Content: buildWorkerResultEnvelope(workerResultEnvelope{Status: "completed", Summary: "recovered after retry"}), FinishReason: message.FinishReasonEndTurn}},
	}}
	worker := newWorker("worker-retry", "tester", "system prompt", provider, pubsub.NewBroker[MultiAgentEvent](), nil)
	worker.history = append(worker.history, message.NewTextMessage(message.System, "system prompt"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	worker.reply(workerTask{ctx: ctx, replyTo: "Orchestrator", taskID: "task-retry"})

	if provider.calls != 2 {
		t.Fatalf("expected provider retry, got %d calls", provider.calls)
	}
	if got := worker.GetLastResult(); !strings.Contains(got, "recovered after retry") {
		t.Fatalf("expected recovered result, got %q", got)
	}
	if len(worker.GetHistory()) < 3 {
		t.Fatalf("expected retry control message to be appended to history")
	}
}

func TestWorkerReplyRetriesOutputTruncationAndRecovers(t *testing.T) {
	provider := &retrySequenceProvider{responses: []retrySequenceStep{
		{response: &provider.ProviderResponse{Content: "partial output", FinishReason: message.FinishReasonMaxTokens}},
		{response: &provider.ProviderResponse{Content: buildWorkerResultEnvelope(workerResultEnvelope{Status: "completed", Summary: "finished after truncation retry"}), FinishReason: message.FinishReasonEndTurn}},
	}}
	worker := newWorker("worker-output", "tester", "system prompt", provider, pubsub.NewBroker[MultiAgentEvent](), nil)
	worker.history = append(worker.history, message.NewTextMessage(message.System, "system prompt"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	worker.reply(workerTask{ctx: ctx, replyTo: "Orchestrator", taskID: "task-output"})

	if provider.calls != 2 {
		t.Fatalf("expected output truncation retry, got %d calls", provider.calls)
	}
	if got := worker.GetLastResult(); !strings.Contains(got, "finished after truncation retry") {
		t.Fatalf("expected recovered result, got %q", got)
	}
}

func TestWorkerReplyFailsStructuredOnEmptyProviderResponse(t *testing.T) {
	provider := &retrySequenceProvider{responses: []retrySequenceStep{
		{response: &provider.ProviderResponse{FinishReason: message.FinishReasonEndTurn}},
	}}
	worker := newWorker("worker-empty", "tester", "system prompt", provider, pubsub.NewBroker[MultiAgentEvent](), nil)
	worker.history = append(worker.history, message.NewTextMessage(message.System, "system prompt"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	worker.reply(workerTask{ctx: ctx, replyTo: "Orchestrator", taskID: "task-empty"})

	structured, ok := parseWorkerResultEnvelope(worker.GetLastResult())
	if !ok {
		t.Fatalf("expected structured failure result, got %q", worker.GetLastResult())
	}
	if structured.Status != "failed" || structured.FailureType != "empty_response" {
		t.Fatalf("expected empty_response failure, got %#v", structured)
	}
}

func TestWorkerReplyFailsStructuredOnMaxTurnsExceeded(t *testing.T) {
	responses := make([]retrySequenceStep, 10)
	for turn := range responses {
		responses[turn] = retrySequenceStep{response: &provider.ProviderResponse{
			ToolCalls: []message.ToolCall{{ID: fmt.Sprintf("call-%d", turn), Name: "missing_tool", Type: "function", Finished: true}},
		}}
	}
	provider := &retrySequenceProvider{responses: responses}
	worker := newWorker("worker-max-turns", "tester", "system prompt", provider, pubsub.NewBroker[MultiAgentEvent](), nil)
	worker.history = append(worker.history, message.NewTextMessage(message.System, "system prompt"))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	worker.reply(workerTask{ctx: ctx, replyTo: "Orchestrator", taskID: "task-max-turns"})

	if provider.calls != 10 {
		t.Fatalf("expected provider calls to stop at max turns, got %d", provider.calls)
	}
	structured, ok := parseWorkerResultEnvelope(worker.GetLastResult())
	if !ok {
		t.Fatalf("expected structured failure result, got %q", worker.GetLastResult())
	}
	if structured.Status != "failed" || structured.FailureType != "max_turns_exceeded" {
		t.Fatalf("expected max_turns_exceeded failure, got %#v", structured)
	}
}

func TestOrchestratorSpawnAgentWithTaskContextPassesRequestModelOverride(t *testing.T) {
	mockSessions := &workerTestSessionService{Broker: pubsub.NewBroker[session.Session]()}
	expectedCandidates := []string{"custom-main-model"}
	var seenCandidates []string
	factory := func(ctx context.Context, role, systemPrompt string) (provider.Provider, []llmtools.BaseTool, error) {
		if candidates, ok := ctx.Value(llmtools.ModelOverrideCandidatesContextKey).([]string); ok {
			seenCandidates = append([]string(nil), candidates...)
		}
		return &retrySequenceProvider{}, nil, nil
	}
	orch := NewOrchestrator(factory, mockSessions)

	ctx := context.WithValue(context.Background(), llmtools.ModelOverrideCandidatesContextKey, expectedCandidates)
	id, err := orch.SpawnAgentWithTaskContext(ctx, "tester", "system prompt", "parent", "task-1", time.Minute)
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	orch.KillAgent(id)

	if strings.Join(seenCandidates, ",") != strings.Join(expectedCandidates, ",") {
		t.Fatalf("expected worker factory to receive model candidates %v, got %v", expectedCandidates, seenCandidates)
	}
}

func TestOrchestratorSpawnAgentWithTaskContextCreatesChildSession(t *testing.T) {
	mockSessions := &workerTestSessionService{Broker: pubsub.NewBroker[session.Session]()}
	factory := func(ctx context.Context, role, systemPrompt string) (provider.Provider, []llmtools.BaseTool, error) {
		return &retrySequenceProvider{}, nil, nil
	}
	orch := NewOrchestrator(factory, mockSessions)

	id, err := orch.SpawnAgentWithTaskContext(context.Background(), "tester", "system prompt", "parent-session", "task-1", time.Minute)
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	orch.KillAgent(id)

	if mockSessions.createTaskCalls != 1 {
		t.Fatalf("expected worker to be created as child session, got createTaskCalls=%d", mockSessions.createTaskCalls)
	}
	if mockSessions.createCalls != 0 {
		t.Fatalf("expected no top-level session creation, got createCalls=%d", mockSessions.createCalls)
	}
	if mockSessions.lastParentSessionID != "parent-session" {
		t.Fatalf("expected parent session to be preserved, got %q", mockSessions.lastParentSessionID)
	}
	if mockSessions.lastTitle != "Worker: tester" {
		t.Fatalf("expected worker title to be preserved, got %q", mockSessions.lastTitle)
	}
}

func TestOrchestratorArchivesWorkerFailureSnapshot(t *testing.T) {
	mockSessions := &workerTestSessionService{Broker: pubsub.NewBroker[session.Session]()}
	prov := &retrySequenceProvider{responses: []retrySequenceStep{{err: fmt.Errorf("connection refused")}, {err: fmt.Errorf("connection refused")}, {err: fmt.Errorf("connection refused")}}}
	factory := func(ctx context.Context, role, systemPrompt string) (provider.Provider, []llmtools.BaseTool, error) {
		return prov, nil, nil
	}
	orch := NewOrchestrator(factory, mockSessions)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := orch.Start(ctx); err != nil {
		t.Fatalf("failed to start orchestrator: %v", err)
	}
	id, err := orch.SpawnAgent("tester", "system prompt", "parent")
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	result, err := orch.SendAndAwaitTask(ctx, id, "task-1", "do work")
	if err != nil && !strings.Contains(err.Error(), "context canceled") {
		// SendAndAwaitTask should return the failure payload as normal content.
	}
	if !strings.Contains(result, "status=failed") && !strings.Contains(result, "worker_result") {
		t.Fatalf("expected structured failure result, got %q", result)
	}
	orch.KillAgent(id)
	snapshot, err := orch.GetWorkerRuntime(id)
	if err != nil {
		t.Fatalf("expected archived snapshot, got err=%v", err)
	}
	if !snapshot.Archived {
		t.Fatalf("expected archived snapshot, got %#v", snapshot)
	}
	if snapshot.FailureType == "" {
		t.Fatalf("expected failure type in snapshot, got %#v", snapshot)
	}
	if snapshot.LastError == "" {
		t.Fatalf("expected last error in snapshot, got %#v", snapshot)
	}
	if snapshot.Checkpoint == nil {
		t.Fatalf("expected checkpoint in archived snapshot, got %#v", snapshot)
	}
	if snapshot.Checkpoint.OriginalTask != "do work" {
		t.Fatalf("expected original task in checkpoint, got %#v", snapshot.Checkpoint)
	}
	if snapshot.Checkpoint.FailureType == "" || snapshot.Checkpoint.LastError == "" {
		t.Fatalf("expected failure details in checkpoint, got %#v", snapshot.Checkpoint)
	}
	if snapshot.Checkpoint.PendingGoal == "" || snapshot.Checkpoint.ResumeHint == "" {
		t.Fatalf("expected pending goal and resume hint in checkpoint, got %#v", snapshot.Checkpoint)
	}
	if len(orch.ListWorkerRuntimeSnapshots(10)) == 0 {
		t.Fatalf("expected list snapshots to include archived worker")
	}
}

func TestOrchestratorResumeWorkerFromCheckpoint(t *testing.T) {
	mockSessions := &workerTestSessionService{Broker: pubsub.NewBroker[session.Session]()}
	providerQueue := []*retrySequenceProvider{
		{responses: []retrySequenceStep{{err: fmt.Errorf("503 service unavailable")}, {err: fmt.Errorf("503 service unavailable")}, {err: fmt.Errorf("503 service unavailable")}}},
		{responses: []retrySequenceStep{{response: &provider.ProviderResponse{Content: buildWorkerResultEnvelope(workerResultEnvelope{Status: "completed", Summary: "resumed and finished"}), FinishReason: message.FinishReasonEndTurn}}}},
	}
	queueIndex := 0
	factory := func(ctx context.Context, role, systemPrompt string) (provider.Provider, []llmtools.BaseTool, error) {
		if queueIndex >= len(providerQueue) {
			return &retrySequenceProvider{}, nil, nil
		}
		prov := providerQueue[queueIndex]
		queueIndex++
		return prov, nil, nil
	}
	orch := NewOrchestrator(factory, mockSessions)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := orch.Start(ctx); err != nil {
		t.Fatalf("failed to start orchestrator: %v", err)
	}
	id, err := orch.SpawnAgent("tester", "system prompt", "parent")
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	_, _ = orch.SendAndAwaitTask(ctx, id, "task-1", "collect evidence and continue")
	orch.KillAgent(id)

	resumedID, err := orch.ResumeWorkerFromCheckpoint(ctx, id, "")
	if err != nil {
		t.Fatalf("resume failed: %v", err)
	}
	if waitErr := orch.WaitWorker(ctx, resumedID, 3); waitErr != nil {
		t.Fatalf("expected resumed worker to finish, got %v", waitErr)
	}
	busy, result, err := orch.GetWorkerState(resumedID)
	if err != nil {
		t.Fatalf("expected resumed worker state, got %v", err)
	}
	if busy {
		t.Fatalf("expected resumed worker to be idle after completion")
	}
	if !strings.Contains(result, "resumed and finished") {
		t.Fatalf("expected resumed result, got %q", result)
	}
	snapshot, err := orch.GetWorkerRuntime(resumedID)
	if err != nil {
		t.Fatalf("expected resumed runtime snapshot, got %v", err)
	}
	if snapshot.Checkpoint == nil {
		t.Fatalf("expected checkpoint on resumed worker, got %#v", snapshot)
	}
	if snapshot.Checkpoint.OriginalTask != "collect evidence and continue" {
		t.Fatalf("expected original task to survive resume, got %#v", snapshot.Checkpoint)
	}
	if len(snapshot.Checkpoint.HistoryTail) == 0 {
		t.Fatalf("expected history tail on resumed checkpoint, got %#v", snapshot.Checkpoint)
	}
	if snapshot.Checkpoint.Summary != "resumed and finished" {
		t.Fatalf("expected resumed summary in checkpoint, got %#v", snapshot.Checkpoint)
	}
	if snapshot.Checkpoint.PendingGoal != "" {
		t.Fatalf("expected completed resumed checkpoint to clear pending goal, got %#v", snapshot.Checkpoint)
	}
	if !strings.Contains(buildWorkerResumePrompt(snapshot.Checkpoint, ""), "Original task: collect evidence and continue") {
		t.Fatalf("expected resume prompt to preserve original task, got %#v", snapshot.Checkpoint)
	}
	orch.KillAgent(resumedID)
}

func TestOrchestratorResumeWorkerFromCheckpointRejectsRunningWorker(t *testing.T) {
	orch := NewOrchestrator(nil, nil)
	orch.workers["worker-1"] = newWorker("worker-1", "tester", "system prompt", nil, orch.bus, nil)
	orch.workerMeta["worker-1"] = &workerRuntime{
		Status:          "running",
		LastActivityAt:  time.Now(),
		TimeoutDuration: time.Minute,
		Checkpoint:      newWorkerCheckpoint("tester", "system prompt", "task-1", "parent", "running"),
	}

	if _, err := orch.ResumeWorkerFromCheckpoint(context.Background(), "worker-1", ""); err == nil || !strings.Contains(err.Error(), "still running") {
		t.Fatalf("expected running worker resume to be rejected, got %v", err)
	}
}

func TestBuildWorkerResumePromptReportsMissingArtifacts(t *testing.T) {
	checkpoint := &WorkerCheckpoint{
		OriginalTask:     "resume artifacts",
		Artifacts:        []string{filepath.Join(t.TempDir(), "missing.txt")},
		MissingArtifacts: nil,
	}
	checkpoint.ValidArtifacts, checkpoint.MissingArtifacts = validateWorkerCheckpointArtifacts(checkpoint.Artifacts)
	text := buildWorkerResumePrompt(checkpoint, "")
	if !strings.Contains(text, "Missing artifacts to recreate or revalidate:") || !strings.Contains(text, "missing.txt") {
		t.Fatalf("expected missing artifact guidance in resume prompt, got %q", text)
	}
}

func TestArchivedKilledWorkerSnapshotNormalizesTerminalFailure(t *testing.T) {
	mockSessions := &workerTestSessionService{Broker: pubsub.NewBroker[session.Session]()}
	prov := &retrySequenceProvider{responses: []retrySequenceStep{{response: &provider.ProviderResponse{Content: "noop", FinishReason: message.FinishReasonEndTurn}}}}
	factory := func(ctx context.Context, role, systemPrompt string) (provider.Provider, []llmtools.BaseTool, error) {
		return prov, nil, nil
	}
	orch := NewOrchestrator(factory, mockSessions)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := orch.Start(ctx); err != nil {
		t.Fatalf("failed to start orchestrator: %v", err)
	}
	id, err := orch.SpawnAgent("tester", "system prompt", "parent")
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	orch.recordWorkerRetry(id, "timeout", "504 Gateway Timeout", "retry same task", true, 1, 0)
	orch.KillAgent(id)

	snapshot, err := orch.GetWorkerRuntime(id)
	if err != nil {
		t.Fatalf("expected archived snapshot, got err=%v", err)
	}
	if snapshot.Status != "failed" {
		t.Fatalf("expected failed archived status, got %#v", snapshot)
	}
	if snapshot.StructuredStatus != "failed" {
		t.Fatalf("expected failed structured status, got %#v", snapshot)
	}
	if snapshot.StructuredSummary == "" {
		t.Fatalf("expected structured summary fallback, got %#v", snapshot)
	}
	if snapshot.FailureType == "" {
		t.Fatalf("expected failure type to be preserved or synthesized, got %#v", snapshot)
	}
	if snapshot.LastRecoveryNote == "" {
		t.Fatalf("expected recovery note to survive archive, got %#v", snapshot)
	}
}

func TestRecordWorkerToolRecoveryPublishesRecoverableSnapshotState(t *testing.T) {
	orch := NewOrchestrator(nil, nil)
	workerID := "worker-tool-recovery"
	orch.workerMeta[workerID] = &workerRuntime{
		Status:     "running",
		Checkpoint: newWorkerCheckpoint("tester", "system prompt", "task-1", "parent", "running"),
	}

	orch.recordWorkerToolRecovery(workerID, "run_command", "Error: exit status 1")

	meta := orch.workerMeta[workerID]
	if meta.ToolRetries != 1 {
		t.Fatalf("expected one tool recovery count, got %d", meta.ToolRetries)
	}
	if meta.LastRecoveryNote == "" {
		t.Fatalf("expected tool recovery note")
	}
	if meta.Checkpoint == nil || meta.Checkpoint.ToolRetryCount != 1 {
		t.Fatalf("expected checkpoint tool retry count to be recorded, got %#v", meta.Checkpoint)
	}
	if meta.Checkpoint.FailureType != "tool_error" {
		t.Fatalf("expected tool_error checkpoint failure type, got %#v", meta.Checkpoint)
	}
}

func TestSpawnAgentSwitchesSnapshotScopePastTerminalWorkers(t *testing.T) {
	mockSessions := &workerTestSessionService{Broker: pubsub.NewBroker[session.Session]()}
	prov := &retrySequenceProvider{responses: []retrySequenceStep{{response: &provider.ProviderResponse{Content: "noop", FinishReason: message.FinishReasonEndTurn}}}}
	factory := func(ctx context.Context, role, systemPrompt string) (provider.Provider, []llmtools.BaseTool, error) {
		return prov, nil, nil
	}
	orch := NewOrchestrator(factory, mockSessions)
	orch.currentSnapshotScope = "parent-a"
	orch.workerMeta["completed-a"] = &workerRuntime{
		ParentSessionID: "parent-a",
		Status:          "completed",
	}

	if _, err := orch.SpawnAgentWithTask("tester", "system prompt", "parent-b", "task-b", time.Minute); err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	if orch.currentSnapshotScope != "parent-b" {
		t.Fatalf("expected snapshot scope to switch past terminal workers, got %q", orch.currentSnapshotScope)
	}
}

func TestWaitWorkerReturnsAfterArchivedKill(t *testing.T) {
	mockSessions := &workerTestSessionService{Broker: pubsub.NewBroker[session.Session]()}
	prov := &retrySequenceProvider{responses: []retrySequenceStep{{response: &provider.ProviderResponse{Content: "noop", FinishReason: message.FinishReasonEndTurn}}}}
	factory := func(ctx context.Context, role, systemPrompt string) (provider.Provider, []llmtools.BaseTool, error) {
		return prov, nil, nil
	}
	orch := NewOrchestrator(factory, mockSessions)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := orch.Start(ctx); err != nil {
		t.Fatalf("failed to start orchestrator: %v", err)
	}
	id, err := orch.SpawnAgent("tester", "system prompt", "parent")
	if err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	orch.recordWorkerRetry(id, "timeout", "504 Gateway Timeout", "retry same task", true, 1, 0)

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- orch.WaitWorker(context.Background(), id, 2)
	}()

	time.Sleep(200 * time.Millisecond)
	orch.KillAgent(id)

	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("expected WaitWorker to stop after archive, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WaitWorker did not return after worker was archived")
	}
}

func TestWaitWorkerDoesNotReturnWhileWorkerStillBusy(t *testing.T) {
	orch := NewOrchestrator(nil, nil)
	workerID := "worker-busy"
	now := time.Now()
	completedAt := now

	orch.workers[workerID] = &worker{
		id:        workerID,
		role:      "tester",
		taskQueue: make(chan workerTask, 1),
		busy:      true,
	}
	orch.workerMeta[workerID] = &workerRuntime{
		Status:          "completed",
		LastActivityAt:  now,
		CompletedAt:     &completedAt,
		TimeoutDuration: time.Minute,
	}

	resultCh := make(chan error, 1)
	go func() {
		resultCh <- orch.WaitWorker(context.Background(), workerID, 2)
	}()

	time.Sleep(300 * time.Millisecond)
	select {
	case err := <-resultCh:
		t.Fatalf("expected WaitWorker to keep waiting while worker is busy, got %v", err)
	default:
	}

	orch.workers[workerID].mu.Lock()
	orch.workers[workerID].busy = false
	orch.workers[workerID].lastResult = "done"
	orch.workers[workerID].mu.Unlock()

	select {
	case err := <-resultCh:
		if err != nil {
			t.Fatalf("expected WaitWorker to return once worker becomes idle, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("WaitWorker did not return after worker became idle")
	}
}

func TestWaitForWorkersDoesNotReturnWhileWorkerStillBusy(t *testing.T) {
	orch := NewOrchestrator(nil, nil)
	workerID := "worker-busy-tool"
	now := time.Now()
	completedAt := now

	orch.workers[workerID] = &worker{
		id:         workerID,
		role:       "tester",
		taskQueue:  make(chan workerTask, 1),
		busy:       true,
		lastResult: "partial",
	}
	orch.workerMeta[workerID] = &workerRuntime{
		Status:          "completed",
		LastActivityAt:  now,
		CompletedAt:     &completedAt,
		TimeoutDuration: time.Minute,
	}

	tool := &waitForWorkersTool{orchestrator: orch}
	respCh := make(chan llmtools.ToolResponse, 1)
	errCh := make(chan error, 1)
	go func() {
		resp, err := tool.Run(context.Background(), llmtools.ToolCall{Input: `{"worker_ids":["worker-busy-tool"],"timeout":2}`})
		if err != nil {
			errCh <- err
			return
		}
		respCh <- resp
	}()

	time.Sleep(300 * time.Millisecond)
	select {
	case err := <-errCh:
		t.Fatalf("wait_for_workers returned unexpected error while worker was busy: %v", err)
	case resp := <-respCh:
		t.Fatalf("expected wait_for_workers to keep waiting while worker is busy, got %q", resp.Content)
	default:
	}

	orch.workers[workerID].mu.Lock()
	orch.workers[workerID].busy = false
	orch.workers[workerID].lastResult = "final result"
	orch.workers[workerID].mu.Unlock()

	select {
	case err := <-errCh:
		t.Fatalf("wait_for_workers returned unexpected error after worker became idle: %v", err)
	case resp := <-respCh:
		if !strings.Contains(resp.Content, "final result") {
			t.Fatalf("expected final worker result after idle, got %q", resp.Content)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wait_for_workers did not return after worker became idle")
	}
}
