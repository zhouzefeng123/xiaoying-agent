package app

import (
	"context"
	"strings"
	"sync"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/agent"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/message"
	"github.com/auramate/auramate/internal/pubsub"
)

func createStartupAgent(agentName config.AgentName, svc agent.Service, err error, factory func() (agent.Service, error)) (agent.Service, error) {
	if err == nil {
		return svc, nil
	}
	if !isRecoverableStartupAgentError(err) {
		return nil, err
	}

	logging.Warn("Agent unavailable during startup; backend will retry after login", "agent", agentName, "error", err)
	return &deferredAgent{
		agentName: agentName,
		init:      factory,
	}, nil
}

func isRecoverableStartupAgentError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "请先登录后再使用")
}

type deferredAgent struct {
	agentName config.AgentName
	init      func() (agent.Service, error)

	mu      sync.RWMutex
	service agent.Service
}

func (a *deferredAgent) ensureInitialized() (agent.Service, error) {
	a.mu.RLock()
	service := a.service
	a.mu.RUnlock()
	if service != nil {
		return service, nil
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	if a.service != nil {
		return a.service, nil
	}

	service, err := a.init()
	if err != nil {
		return nil, err
	}

	a.service = service
	logging.Info("Recovered deferred startup agent", "agent", a.agentName)
	return a.service, nil
}

func (a *deferredAgent) Subscribe(ctx context.Context) <-chan pubsub.Event[agent.AgentEvent] {
	service, err := a.ensureInitialized()
	if err != nil {
		ch := make(chan pubsub.Event[agent.AgentEvent])
		close(ch)
		return ch
	}
	return service.Subscribe(ctx)
}

func (a *deferredAgent) Model() models.Model {
	service, err := a.ensureInitialized()
	if err != nil {
		return models.Model{}
	}
	return service.Model()
}

func (a *deferredAgent) Run(ctx context.Context, sessionID string, content string, mode string, attachments ...message.Attachment) (<-chan agent.AgentEvent, error) {
	service, err := a.ensureInitialized()
	if err != nil {
		return nil, err
	}
	return service.Run(ctx, sessionID, content, mode, attachments...)
}

func (a *deferredAgent) Cancel(sessionID string) {
	service, err := a.ensureInitialized()
	if err != nil {
		return
	}
	service.Cancel(sessionID)
}

func (a *deferredAgent) CancelTool(sessionID string, executionID string) {
	service, err := a.ensureInitialized()
	if err != nil {
		return
	}
	service.CancelTool(sessionID, executionID)
}

func (a *deferredAgent) IsSessionBusy(sessionID string) bool {
	service, err := a.ensureInitialized()
	if err != nil {
		return false
	}
	return service.IsSessionBusy(sessionID)
}

func (a *deferredAgent) IsBusy() bool {
	service, err := a.ensureInitialized()
	if err != nil {
		return false
	}
	return service.IsBusy()
}

func (a *deferredAgent) Update(agentName config.AgentName, modelID models.ModelID) (models.Model, error) {
	service, err := a.ensureInitialized()
	if err != nil {
		return models.Model{}, err
	}
	return service.Update(agentName, modelID)
}

func (a *deferredAgent) Summarize(ctx context.Context, sessionID string) error {
	service, err := a.ensureInitialized()
	if err != nil {
		return err
	}
	return service.Summarize(ctx, sessionID)
}

func (a *deferredAgent) SetReadOnlyMode(ctx context.Context, sessionID string, enabled bool) error {
	service, err := a.ensureInitialized()
	if err != nil {
		return err
	}
	return service.SetReadOnlyMode(ctx, sessionID, enabled)
}
