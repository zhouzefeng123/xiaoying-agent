package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	appcore "github.com/auramate/auramate/internal/app"
	"github.com/auramate/auramate/internal/llm/agent"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/llm/provider"
	llmtools "github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/message"
	"github.com/auramate/auramate/internal/pubsub"
	"github.com/auramate/auramate/internal/session"
)

type snapshotTestSessionService struct {
	*pubsub.Broker[session.Session]
}

var snapshotSessionCounter uint64

func (s *snapshotTestSessionService) Create(ctx context.Context, title string) (session.Session, error) {
	return session.Session{ID: fmt.Sprintf("snapshot-test-%s-%d", title, atomic.AddUint64(&snapshotSessionCounter, 1))}, nil
}

func (s *snapshotTestSessionService) CreateTaskSession(ctx context.Context, toolCallID, parentSessionID, title string) (session.Session, error) {
	return session.Session{ID: toolCallID, ParentSessionID: parentSessionID, Title: title}, nil
}

func (s *snapshotTestSessionService) Get(ctx context.Context, id string) (session.Session, error) {
	return session.Session{}, nil
}

func (s *snapshotTestSessionService) List(ctx context.Context) ([]session.Session, error) {
	return nil, nil
}
func (s *snapshotTestSessionService) Save(ctx context.Context, sess session.Session) (session.Session, error) {
	return sess, nil
}
func (s *snapshotTestSessionService) Delete(ctx context.Context, id string) error { return nil }

type snapshotScriptedProvider struct {
	content string
}

func (p *snapshotScriptedProvider) SendMessages(ctx context.Context, messages []message.Message, tools []llmtools.BaseTool) (*provider.ProviderResponse, error) {
	return &provider.ProviderResponse{Content: p.content, FinishReason: message.FinishReasonEndTurn}, nil
}

func (p *snapshotScriptedProvider) StreamResponse(ctx context.Context, messages []message.Message, tools []llmtools.BaseTool) <-chan provider.ProviderEvent {
	return nil
}

func (p *snapshotScriptedProvider) Model() models.Model {
	return models.Model{ID: "snapshot-scripted", Provider: models.ProviderMock}
}

func (p *snapshotScriptedProvider) Transcribe(ctx context.Context, audio []byte, filename string) (string, error) {
	return "", nil
}

func (p *snapshotScriptedProvider) GenerateSpeech(ctx context.Context, text string, voice string) ([]byte, error) {
	return nil, nil
}

func TestHandleSwarmSnapshotsReturnsRuntimeSummary(t *testing.T) {
	factory := func(ctx context.Context, role, systemPrompt string) (provider.Provider, []llmtools.BaseTool, error) {
		return &snapshotScriptedProvider{content: buildWorkerResultEnvelope(agentWorkerResult("completed", "ok summary", "", false))}, nil, nil
	}
	orchestrator := agent.NewOrchestrator(factory, &snapshotTestSessionService{Broker: pubsub.NewBroker[session.Session]()})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := orchestrator.Start(ctx); err != nil {
		t.Fatalf("failed to start orchestrator: %v", err)
	}
	orchestrator.BeginWorkerSnapshotRound("parent")
	id, err := orchestrator.SpawnAgentWithTask("worker", "system", "parent", "task-1", time.Minute)
	if err != nil {
		t.Fatalf("failed to spawn worker: %v", err)
	}
	if _, err := orchestrator.SendAndAwaitTask(ctx, id, "task-1", "do work"); err != nil {
		t.Fatalf("failed to complete worker task: %v", err)
	}
	orchestrator.KillAgent(id)

	server := &Server{app: &appcore.App{Orchestrator: orchestrator}}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/swarm/snapshots?limit=10", nil)

	server.handleSwarmSnapshots(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	var resp workerSnapshotsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Summary.Total == 0 {
		t.Fatalf("expected snapshots in response, got %#v", resp)
	}
	if resp.Summary.Archived == 0 {
		t.Fatalf("expected archived worker count, got %#v", resp.Summary)
	}
	if len(resp.Snapshots) == 0 || resp.Snapshots[0].WorkerID == "" {
		t.Fatalf("expected snapshot payload, got %#v", resp.Snapshots)
	}
}

func TestHandleSwarmSnapshotsSummaryUsesCurrentTaskScope(t *testing.T) {
	factory := func(ctx context.Context, role, systemPrompt string) (provider.Provider, []llmtools.BaseTool, error) {
		return &snapshotScriptedProvider{content: buildWorkerResultEnvelope(agentWorkerResult("completed", "ok summary", "", false))}, nil, nil
	}
	orchestrator := agent.NewOrchestrator(factory, &snapshotTestSessionService{Broker: pubsub.NewBroker[session.Session]()})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := orchestrator.Start(ctx); err != nil {
		t.Fatalf("failed to start orchestrator: %v", err)
	}
	orchestrator.BeginWorkerSnapshotRound("parent")

	firstID, err := orchestrator.SpawnAgentWithTask("worker", "system", "parent", "task-1", time.Minute)
	if err != nil {
		t.Fatalf("failed to spawn first worker: %v", err)
	}
	if _, err := orchestrator.SendAndAwaitTask(ctx, firstID, "task-1", "do first work"); err != nil {
		t.Fatalf("failed to complete first worker task: %v", err)
	}
	orchestrator.KillAgent(firstID)
	orchestrator.BeginWorkerSnapshotRound("parent")

	secondID, err := orchestrator.SpawnAgentWithTask("worker", "system", "parent", "task-2", time.Minute)
	if err != nil {
		t.Fatalf("failed to spawn second worker: %v", err)
	}
	if _, err := orchestrator.SendAndAwaitTask(ctx, secondID, "task-2", "do second work"); err != nil {
		t.Fatalf("failed to complete second worker task: %v", err)
	}
	orchestrator.KillAgent(secondID)

	server := &Server{app: &appcore.App{Orchestrator: orchestrator}}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/swarm/snapshots?limit=10", nil)

	server.handleSwarmSnapshots(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	var resp workerSnapshotsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Summary.Total != 1 {
		t.Fatalf("expected only current task snapshot in summary, got %#v", resp.Summary)
	}
	if len(resp.Snapshots) != 1 {
		t.Fatalf("expected only current task snapshot payload, got %#v", resp.Snapshots)
	}
	if resp.Snapshots[0].TaskID != "task-2" {
		t.Fatalf("expected current task snapshot only, got %#v", resp.Snapshots)
	}
}

func TestHandleSwarmSnapshotsFallsForwardWhenPreviousScopeOnlyArchived(t *testing.T) {
	factory := func(ctx context.Context, role, systemPrompt string) (provider.Provider, []llmtools.BaseTool, error) {
		return &snapshotScriptedProvider{content: buildWorkerResultEnvelope(agentWorkerResult("completed", "ok summary", "", false))}, nil, nil
	}
	orchestrator := agent.NewOrchestrator(factory, &snapshotTestSessionService{Broker: pubsub.NewBroker[session.Session]()})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := orchestrator.Start(ctx); err != nil {
		t.Fatalf("failed to start orchestrator: %v", err)
	}
	orchestrator.BeginWorkerSnapshotRound("parent-1")

	firstID, err := orchestrator.SpawnAgentWithTask("worker", "system", "parent-1", "task-1", time.Minute)
	if err != nil {
		t.Fatalf("failed to spawn first worker: %v", err)
	}
	if _, err := orchestrator.SendAndAwaitTask(ctx, firstID, "task-1", "do first work"); err != nil {
		t.Fatalf("failed to complete first worker task: %v", err)
	}
	orchestrator.KillAgent(firstID)

	secondID, err := orchestrator.SpawnAgentWithTask("worker", "system", "parent-2", "task-2", time.Minute)
	if err != nil {
		t.Fatalf("failed to spawn second worker: %v", err)
	}
	defer orchestrator.KillAgent(secondID)

	server := &Server{app: &appcore.App{Orchestrator: orchestrator}}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/swarm/snapshots?limit=10", nil)

	server.handleSwarmSnapshots(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	var resp workerSnapshotsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Summary.Total != 1 {
		t.Fatalf("expected only new scope snapshot in summary, got %#v", resp.Summary)
	}
	if len(resp.Snapshots) != 1 {
		t.Fatalf("expected only new scope snapshot payload, got %#v", resp.Snapshots)
	}
	if resp.Snapshots[0].WorkerID != secondID {
		t.Fatalf("expected current snapshot to move to new worker, got %#v", resp.Snapshots[0])
	}
	if resp.Snapshots[0].TaskID != "task-2" {
		t.Fatalf("expected new scope task, got %#v", resp.Snapshots[0])
	}
	if resp.Snapshots[0].ParentSessionID != "parent-2" {
		t.Fatalf("expected new scope parent session, got %#v", resp.Snapshots[0])
	}
}

func TestHandleSwarmSnapshotsSummaryIgnoresLimitForCurrentTaskTotal(t *testing.T) {
	factory := func(ctx context.Context, role, systemPrompt string) (provider.Provider, []llmtools.BaseTool, error) {
		return &snapshotScriptedProvider{content: buildWorkerResultEnvelope(agentWorkerResult("completed", "ok summary", "", false))}, nil, nil
	}
	orchestrator := agent.NewOrchestrator(factory, &snapshotTestSessionService{Broker: pubsub.NewBroker[session.Session]()})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := orchestrator.Start(ctx); err != nil {
		t.Fatalf("failed to start orchestrator: %v", err)
	}
	orchestrator.BeginWorkerSnapshotRound("parent")

	for _, taskID := range []string{"batch-scope:one", "batch-scope:two"} {
		id, err := orchestrator.SpawnAgentWithTask("worker", "system", "parent", taskID, time.Minute)
		if err != nil {
			t.Fatalf("failed to spawn worker for %s: %v", taskID, err)
		}
		if _, err := orchestrator.SendAndAwaitTask(ctx, id, taskID, "do work"); err != nil {
			t.Fatalf("failed to complete worker task for %s: %v", taskID, err)
		}
		orchestrator.KillAgent(id)
	}

	server := &Server{app: &appcore.App{Orchestrator: orchestrator}}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/swarm/snapshots?limit=1", nil)

	server.handleSwarmSnapshots(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", recorder.Code)
	}
	var resp workerSnapshotsResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}
	if resp.Summary.Total != 2 {
		t.Fatalf("expected full current-task total regardless of limit, got %#v", resp.Summary)
	}
	if len(resp.Snapshots) != 1 {
		t.Fatalf("expected payload to respect limit, got %#v", resp.Snapshots)
	}
}

func TestHandleSwarmSnapshotsRejectsInvalidLimit(t *testing.T) {
	server := &Server{app: &appcore.App{Orchestrator: &agent.Orchestrator{}}}
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/swarm/snapshots?limit=oops", nil)

	server.handleSwarmSnapshots(recorder, req)

	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d", recorder.Code)
	}
}

func agentWorkerResult(status, summary, failureType string, retryable bool) interface{} {
	return map[string]any{
		"status":        status,
		"summary":       summary,
		"artifacts":     []string{},
		"retryable":     retryable,
		"failure_type":  failureType,
		"evidence_refs": []string{},
	}
}

func buildWorkerResultEnvelope(payload interface{}) string {
	encoded, _ := json.Marshal(payload)
	return "<worker_result>" + string(encoded) + "</worker_result>"
}
