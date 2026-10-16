package agent

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/llm/provider"
	llmtools "github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/message"
	"github.com/auramate/auramate/internal/pubsub"
	"github.com/auramate/auramate/internal/session"
	"github.com/google/uuid"
)

type integrationMockSessionService struct {
	*pubsub.Broker[session.Session]
}

func (s *integrationMockSessionService) Create(ctx context.Context, title string) (session.Session, error) {
	return session.Session{ID: "test-session-" + uuid.NewString(), Title: title}, nil
}

func (s *integrationMockSessionService) CreateTaskSession(ctx context.Context, toolCallID, parentSessionID, title string) (session.Session, error) {
	return session.Session{ID: toolCallID, ParentSessionID: parentSessionID, Title: title}, nil
}

func (s *integrationMockSessionService) Get(ctx context.Context, id string) (session.Session, error) {
	return session.Session{ID: id}, nil
}

func (s *integrationMockSessionService) List(ctx context.Context) ([]session.Session, error) {
	return nil, nil
}

func (s *integrationMockSessionService) Save(ctx context.Context, sess session.Session) (session.Session, error) {
	return sess, nil
}

func (s *integrationMockSessionService) Delete(ctx context.Context, id string) error {
	return nil
}

func TestBatchDelegateWorkerPromptSeparatesProjectRootAndWorkspace(t *testing.T) {
	tool := &batchDelegateTaskTool{workingDir: `C:\repo\workspace`}
	prompt := tool.buildWorkerPrompt("")

	for _, expected := range []string{
		"Project Root (Absolute):",
		"Workspace Directory (Scratch/Artifacts):",
		"resolve relative paths under \"Project Root (Absolute)\" first",
		"Do not prepend it to repo paths like backend/... or client/...",
		"separate commands with ';' instead of '&&'",
		"read the target file again before retrying the edit",
	} {
		if !strings.Contains(prompt, expected) {
			t.Fatalf("expected worker prompt to contain %q", expected)
		}
	}
}

type scriptedBatchProvider struct{}

func (p *scriptedBatchProvider) SendMessages(ctx context.Context, messages []message.Message, tools []llmtools.BaseTool) (*provider.ProviderResponse, error) {
	lastUserContent := latestUserContent(messages)

	switch {
	case strings.Contains(lastUserContent, "TASK_SLOW_SOURCE"):
		time.Sleep(1500 * time.Millisecond)
		return scriptedProviderResponse(taggedWorkerResult(workerResultEnvelope{
			Status:    "completed",
			Summary:   "slow source done",
			Artifacts: []string{"/tmp/slow-source.json"},
		}))
	case strings.Contains(lastUserContent, "TASK_ALPHA"):
		return scriptedProviderResponse(taggedWorkerResult(workerResultEnvelope{
			Status:       "completed",
			Summary:      "alpha summary",
			Artifacts:    []string{"/tmp/alpha.json"},
			EvidenceRefs: []string{"workspace/alpha-input.txt"},
		}))
	case strings.Contains(lastUserContent, "TASK_BETA"):
		return scriptedProviderResponse(taggedWorkerResult(workerResultEnvelope{
			Status:       "completed",
			Summary:      "beta summary",
			Artifacts:    []string{"/tmp/beta.json"},
			EvidenceRefs: []string{"workspace/beta-input.txt"},
		}))
	case strings.Contains(lastUserContent, "TASK_GAMMA"):
		sawAlpha := strings.Contains(lastUserContent, "task_id: alpha") && strings.Contains(lastUserContent, "alpha summary") && strings.Contains(lastUserContent, "/tmp/alpha.json")
		sawBeta := strings.Contains(lastUserContent, "task_id: beta") && strings.Contains(lastUserContent, "beta summary") && strings.Contains(lastUserContent, "/tmp/beta.json")
		if sawAlpha && sawBeta && strings.Contains(lastUserContent, "<dependency_context>") {
			return scriptedProviderResponse(taggedWorkerResult(workerResultEnvelope{
				Status:       "completed",
				Summary:      "gamma saw dependency context",
				Artifacts:    []string{"/tmp/gamma.json"},
				EvidenceRefs: []string{"workspace/gamma-output.txt"},
			}))
		}
		return scriptedProviderResponse(taggedWorkerResult(workerResultEnvelope{
			Status:      "failed",
			Summary:     "gamma missing dependency context",
			FailureType: "missing_dependency_context",
		}))
	case strings.Contains(lastUserContent, "TASK_MULTI"):
		return scriptedProviderResponse(taggedWorkerResult(workerResultEnvelope{
			Status:       "completed",
			Summary:      "multi summary",
			Artifacts:    []string{"/tmp/workspace/spec.md", "/tmp/workspace/plan.md", "/tmp/workspace/other.json"},
			EvidenceRefs: []string{"workspace/multi-input.txt"},
		}))
	case strings.Contains(lastUserContent, "TASK_FILTER"):
		onlySpec := strings.Contains(lastUserContent, "spec.md")
		leakedPlan := strings.Contains(lastUserContent, "plan.md")
		leakedOther := strings.Contains(lastUserContent, "other.json")
		leakedEvidence := strings.Contains(lastUserContent, "workspace/multi-input.txt")
		summaryDropped := !strings.Contains(lastUserContent, "multi summary")
		if onlySpec && !leakedPlan && !leakedOther && !leakedEvidence && summaryDropped {
			return scriptedProviderResponse(taggedWorkerResult(workerResultEnvelope{
				Status:  "completed",
				Summary: "filter honored",
			}))
		}
		return scriptedProviderResponse(taggedWorkerResult(workerResultEnvelope{
			Status:      "failed",
			Summary:     "filter spec not honored",
			FailureType: "artifact_filter_violation",
		}))
	case strings.Contains(lastUserContent, "TASK_PENDING_CONSUMER"):
		if strings.Contains(lastUserContent, "task_id: slow") && strings.Contains(lastUserContent, "/tmp/slow-source.json") {
			return scriptedProviderResponse(taggedWorkerResult(workerResultEnvelope{
				Status:  "completed",
				Summary: "pending consumer eventually ran",
			}))
		}
		return scriptedProviderResponse(taggedWorkerResult(workerResultEnvelope{
			Status:      "failed",
			Summary:     "pending consumer missing dependency context",
			FailureType: "missing_dependency_context",
		}))
	case strings.Contains(lastUserContent, "TASK_FAIL"):
		return scriptedProviderResponse(taggedWorkerResult(workerResultEnvelope{
			Status:      "failed",
			Summary:     "upstream dependency failed",
			FailureType: "synthetic_failure",
		}))
	case strings.Contains(lastUserContent, "上游依赖失败，跳过执行"):
		return scriptedProviderResponse(taggedWorkerResult(workerResultEnvelope{
			Status:  "completed",
			Summary: "downstream skip acknowledged",
		}))
	case strings.Contains(lastUserContent, "TASK_SHOULD_NOT_RUN"):
		return scriptedProviderResponse(taggedWorkerResult(workerResultEnvelope{
			Status:  "completed",
			Summary: "unexpected original task execution",
		}))
	default:
		return scriptedProviderResponse(taggedWorkerResult(workerResultEnvelope{
			Status:  "completed",
			Summary: "default completion",
		}))
	}
}

func (p *scriptedBatchProvider) StreamResponse(ctx context.Context, messages []message.Message, tools []llmtools.BaseTool) <-chan provider.ProviderEvent {
	return nil
}

func (p *scriptedBatchProvider) Model() models.Model {
	return models.Model{ID: "scripted-batch-provider", Provider: models.ProviderMock}
}

func (p *scriptedBatchProvider) Transcribe(ctx context.Context, audio []byte, filename string) (string, error) {
	return "", nil
}

func (p *scriptedBatchProvider) GenerateSpeech(ctx context.Context, text string, voice string) ([]byte, error) {
	return nil, nil
}

func scriptedProviderResponse(content string) (*provider.ProviderResponse, error) {
	return &provider.ProviderResponse{
		Content:      content,
		FinishReason: message.FinishReasonEndTurn,
	}, nil
}

func taggedWorkerResult(result workerResultEnvelope) string {
	payload, err := json.Marshal(result)
	if err != nil {
		panic(err)
	}
	return "<worker_result>" + string(payload) + "</worker_result>"
}

func latestUserContent(messages []message.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == message.User {
			return messages[i].Content().String()
		}
	}
	return ""
}

func newBatchIntegrationHarness(t *testing.T) (*Orchestrator, llmtools.BaseTool) {
	t.Helper()
	if config.Get() == nil {
		if _, err := config.Load("", false); err != nil {
			t.Fatalf("failed to initialize config for integration harness: %v", err)
		}
	}

	mockSessions := &integrationMockSessionService{Broker: pubsub.NewBroker[session.Session]()}
	factory := func(ctx context.Context, role, systemPrompt string) (provider.Provider, []llmtools.BaseTool, error) {
		return &scriptedBatchProvider{}, nil, nil
	}

	orch := NewOrchestrator(factory, mockSessions)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	if err := orch.Start(ctx); err != nil {
		t.Fatalf("failed to start orchestrator: %v", err)
	}
	t.Cleanup(orch.Shutdown)

	return orch, NewBatchDelegateTaskTool(orch, "")
}

func runAsyncBatch(t *testing.T, tool llmtools.BaseTool, params BatchDelegateParams) map[string]string {
	t.Helper()

	input, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("failed to marshal batch params: %v", err)
	}
	resp, err := tool.Run(context.Background(), llmtools.ToolCall{
		ID:    "test-batch-call",
		Name:  "batch_delegate_task",
		Input: string(input),
	})
	if err != nil {
		t.Fatalf("batch tool run failed: %v", err)
	}
	if resp.IsError {
		t.Fatalf("batch tool returned error response: %s", resp.Content)
	}

	return parseWorkerMapping(t, resp.Content)
}

func parseWorkerMapping(t *testing.T, content string) map[string]string {
	t.Helper()

	matches := regexp.MustCompile(`- ([^\s]+) => worker=([^\s]+) task_id=`).FindAllStringSubmatch(content, -1)
	if len(matches) == 0 {
		t.Fatalf("expected worker mapping in response, got %q", content)
	}
	mapping := make(map[string]string, len(matches))
	for _, match := range matches {
		mapping[match[1]] = match[2]
	}
	return mapping
}

func waitForWorkers(t *testing.T, orch *Orchestrator, workerIDs map[string]string) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	for taskID, workerID := range workerIDs {
		if err := orch.WaitWorker(ctx, workerID, 5); err != nil {
			t.Fatalf("wait worker failed for %s (%s): %v", taskID, workerID, err)
		}
	}
}

func historyContentsByRole(history []message.Message, role message.MessageRole) []string {
	contents := make([]string, 0)
	for _, msg := range history {
		if msg.Role == role {
			contents = append(contents, msg.Content().String())
		}
	}
	return contents
}

func TestBatchDelegateTaskInjectsDependencyContextForMultipleDependencies(t *testing.T) {
	orch, batchTool := newBatchIntegrationHarness(t)
	asyncMode := true
	workerIDs := runAsyncBatch(t, batchTool, BatchDelegateParams{
		Tasks: []batchTaskItem{
			{ID: "alpha", Task: "TASK_ALPHA", Role: "worker"},
			{ID: "beta", Task: "TASK_BETA", Role: "worker"},
			{ID: "gamma", Task: "TASK_GAMMA", Role: "worker", DependsOn: batchDependencyList{{TaskID: "alpha"}, {TaskID: "beta"}}},
		},
		Async: &asyncMode,
	})

	waitForWorkers(t, orch, workerIDs)

	gammaHistory, err := orch.GetWorkerHistory(workerIDs["gamma"])
	if err != nil {
		t.Fatalf("failed to read gamma history: %v", err)
	}
	userMessages := strings.Join(historyContentsByRole(gammaHistory, message.User), "\n")
	for _, snippet := range []string{"<dependency_context>", "task_id: alpha", "alpha summary", "/tmp/alpha.json", "task_id: beta", "beta summary", "/tmp/beta.json", "当前任务：", "TASK_GAMMA"} {
		if !strings.Contains(userMessages, snippet) {
			t.Fatalf("expected gamma history to contain %q, got %q", snippet, userMessages)
		}
	}

	_, gammaResult, err := orch.GetWorkerState(workerIDs["gamma"])
	if err != nil {
		t.Fatalf("failed to read gamma result: %v", err)
	}
	if !strings.Contains(gammaResult, "gamma saw dependency context") {
		t.Fatalf("expected gamma result to confirm dependency context, got %q", gammaResult)
	}
}

func TestBatchDelegateTaskSkipsOriginalTaskWhenDependencyFails(t *testing.T) {
	orch, batchTool := newBatchIntegrationHarness(t)
	asyncMode := true
	workerIDs := runAsyncBatch(t, batchTool, BatchDelegateParams{
		Tasks: []batchTaskItem{
			{ID: "failing", Task: "TASK_FAIL", Role: "worker"},
			{ID: "downstream", Task: "TASK_SHOULD_NOT_RUN", Role: "worker", DependsOn: batchDependencyList{{TaskID: "failing"}}},
		},
		Async: &asyncMode,
	})

	waitForWorkers(t, orch, workerIDs)

	downstreamHistory, err := orch.GetWorkerHistory(workerIDs["downstream"])
	if err != nil {
		t.Fatalf("failed to read downstream history: %v", err)
	}
	userMessages := strings.Join(historyContentsByRole(downstreamHistory, message.User), "\n")
	if !strings.Contains(userMessages, "上游依赖失败，跳过执行") {
		t.Fatalf("expected downstream to receive skip message, got %q", userMessages)
	}
	if strings.Contains(userMessages, "TASK_SHOULD_NOT_RUN") {
		t.Fatalf("expected downstream original task to be withheld after failure, got %q", userMessages)
	}

	_, downstreamResult, err := orch.GetWorkerState(workerIDs["downstream"])
	if err != nil {
		t.Fatalf("failed to read downstream result: %v", err)
	}
	if strings.Contains(downstreamResult, "unexpected original task execution") {
		t.Fatalf("expected downstream to skip original task, got %q", downstreamResult)
	}
	if !strings.Contains(downstreamResult, "downstream skip acknowledged") {
		t.Fatalf("expected downstream skip acknowledgement, got %q", downstreamResult)
	}

	_, upstreamResult, err := orch.GetWorkerState(workerIDs["failing"])
	if err != nil {
		t.Fatalf("failed to read upstream result: %v", err)
	}
	if !strings.Contains(upstreamResult, "upstream dependency failed") {
		t.Fatalf("expected upstream synthetic failure result, got %q", upstreamResult)
	}
}

func TestBatchDelegateTaskRespectsArtifactSelection(t *testing.T) {
	orch, batchTool := newBatchIntegrationHarness(t)
	asyncMode := true
	no := false
	workerIDs := runAsyncBatch(t, batchTool, BatchDelegateParams{
		Tasks: []batchTaskItem{
			{ID: "multi", Task: "TASK_MULTI", Role: "worker"},
			{ID: "filter", Task: "TASK_FILTER", Role: "worker", DependsOn: batchDependencyList{{
				TaskID:          "multi",
				Artifacts:       []string{"spec.md"},
				IncludeSummary:  &no,
				IncludeEvidence: &no,
			}}},
		},
		Async: &asyncMode,
	})

	waitForWorkers(t, orch, workerIDs)

	filterHistory, err := orch.GetWorkerHistory(workerIDs["filter"])
	if err != nil {
		t.Fatalf("failed to read filter history: %v", err)
	}
	userMessages := strings.Join(historyContentsByRole(filterHistory, message.User), "\n")
	if !strings.Contains(userMessages, "spec.md") {
		t.Fatalf("expected filtered artifact spec.md in filter task content, got %q", userMessages)
	}
	for _, leaked := range []string{"plan.md", "other.json", "multi summary", "workspace/multi-input.txt"} {
		if strings.Contains(userMessages, leaked) {
			t.Fatalf("filtered content leaked %q: %q", leaked, userMessages)
		}
	}

	_, filterResult, err := orch.GetWorkerState(workerIDs["filter"])
	if err != nil {
		t.Fatalf("failed to read filter result: %v", err)
	}
	if !strings.Contains(filterResult, "filter honored") {
		t.Fatalf("expected filter task to confirm filtered context, got %q", filterResult)
	}
}

func TestWaitWorkerDoesNotTreatPendingDependencyAsCompleted(t *testing.T) {
	orch, batchTool := newBatchIntegrationHarness(t)
	asyncMode := true
	workerIDs := runAsyncBatch(t, batchTool, BatchDelegateParams{
		Tasks: []batchTaskItem{
			{ID: "slow", Task: "TASK_SLOW_SOURCE", Role: "worker"},
			{ID: "consumer", Task: "TASK_PENDING_CONSUMER", Role: "worker", DependsOn: batchDependencyList{{TaskID: "slow"}}},
		},
		Async: &asyncMode,
	})

	waitCtx, cancel := context.WithTimeout(context.Background(), 1200*time.Millisecond)
	defer cancel()
	err := orch.WaitWorker(waitCtx, workerIDs["consumer"], 1)
	if err == nil {
		t.Fatalf("expected pending consumer wait to time out before source finished")
	}
	if !strings.Contains(err.Error(), "wait timed out") && !strings.Contains(err.Error(), "deadline exceeded") {
		t.Fatalf("expected timeout-related error, got %v", err)
	}

	waitForWorkers(t, orch, workerIDs)
	_, result, err := orch.GetWorkerState(workerIDs["consumer"])
	if err != nil {
		t.Fatalf("failed to read consumer result: %v", err)
	}
	if !strings.Contains(result, "pending consumer eventually ran") {
		t.Fatalf("expected pending consumer to eventually execute, got %q", result)
	}
}
