package app

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/agent"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/message"
	"github.com/auramate/auramate/internal/pubsub"
)

type startupTestAgent struct{}

func (a *startupTestAgent) Subscribe(ctx context.Context) <-chan pubsub.Event[agent.AgentEvent] {
	ch := make(chan pubsub.Event[agent.AgentEvent])
	close(ch)
	return ch
}

func (a *startupTestAgent) Model() models.Model { return models.Model{ID: "qwen3.6-plus"} }

func (a *startupTestAgent) Run(ctx context.Context, sessionID string, content string, mode string, attachments ...message.Attachment) (<-chan agent.AgentEvent, error) {
	ch := make(chan agent.AgentEvent)
	close(ch)
	return ch, nil
}

func (a *startupTestAgent) Cancel(sessionID string) {}

func (a *startupTestAgent) CancelTool(sessionID string, executionID string) {}

func (a *startupTestAgent) IsSessionBusy(sessionID string) bool { return false }

func (a *startupTestAgent) IsBusy() bool { return false }

func (a *startupTestAgent) Update(agentName config.AgentName, modelID models.ModelID) (models.Model, error) {
	return models.Model{ID: modelID}, nil
}

func (a *startupTestAgent) Summarize(ctx context.Context, sessionID string) error { return nil }

func (a *startupTestAgent) SetReadOnlyMode(ctx context.Context, sessionID string, enabled bool) error {
	return nil
}

func TestCreateStartupAgentRecoversAfterLogin(t *testing.T) {
	loggedIn := false
	attempts := 0

	svc, err := createStartupAgent(config.AgentCoder, nil, fmt.Errorf("请先登录后再使用 Qwen 3.6 Plus"), func() (agent.Service, error) {
		attempts++
		if !loggedIn {
			return nil, fmt.Errorf("请先登录后再使用 Qwen 3.6 Plus")
		}
		return &startupTestAgent{}, nil
	})
	if err != nil {
		t.Fatalf("createStartupAgent returned error: %v", err)
	}

	if _, err := svc.Run(context.Background(), "session-1", "hi", "coding"); err == nil || !strings.Contains(err.Error(), "请先登录后再使用") {
		t.Fatalf("expected login-required error before login, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected one init attempt before login, got %d", attempts)
	}

	loggedIn = true
	ch, err := svc.Run(context.Background(), "session-1", "hi", "coding")
	if err != nil {
		t.Fatalf("expected deferred agent to recover after login, got %v", err)
	}
	for range ch {
	}
	if attempts != 2 {
		t.Fatalf("expected second init attempt after login, got %d", attempts)
	}

	if _, err := svc.Update(config.AgentCoder, "qwen3.6-plus"); err != nil {
		t.Fatalf("expected recovered agent to handle updates, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected recovered agent to be reused, got %d init attempts", attempts)
	}
}

func TestCreateStartupAgentKeepsFatalErrorsFatal(t *testing.T) {
	_, err := createStartupAgent(config.AgentCoder, nil, fmt.Errorf("failed to initialize embedder"), func() (agent.Service, error) {
		return &startupTestAgent{}, nil
	})
	if err == nil || !strings.Contains(err.Error(), "failed to initialize embedder") {
		t.Fatalf("expected fatal startup error to be returned, got %v", err)
	}
}
