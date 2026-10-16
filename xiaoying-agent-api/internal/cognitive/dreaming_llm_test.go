package cognitive

import (
	"context"
	"testing"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/agent"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/message"
	"github.com/auramate/auramate/internal/pubsub"
)

type dreamingFallbackTestAgent struct {
	events []agent.AgentEvent
}

func (a *dreamingFallbackTestAgent) Model() models.Model { return models.Model{} }

func (a *dreamingFallbackTestAgent) Run(ctx context.Context, sessionID string, content string, mode string, attachments ...message.Attachment) (<-chan agent.AgentEvent, error) {
	ch := make(chan agent.AgentEvent, len(a.events))
	for _, ev := range a.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

func (a *dreamingFallbackTestAgent) Cancel(sessionID string) {}

func (a *dreamingFallbackTestAgent) CancelTool(sessionID string, executionID string) {}

func (a *dreamingFallbackTestAgent) IsSessionBusy(sessionID string) bool { return false }

func (a *dreamingFallbackTestAgent) IsBusy() bool { return false }

func (a *dreamingFallbackTestAgent) Update(agentName config.AgentName, modelID models.ModelID) (models.Model, error) {
	return models.Model{}, nil
}

func (a *dreamingFallbackTestAgent) Summarize(ctx context.Context, sessionID string) error {
	return nil
}

func (a *dreamingFallbackTestAgent) SetReadOnlyMode(ctx context.Context, sessionID string, enabled bool) error {
	return nil
}

func (a *dreamingFallbackTestAgent) Subscribe(ctx context.Context) <-chan pubsub.Event[agent.AgentEvent] {
	return nil
}

func TestExtractDreamingJSONCandidate_StripsMarkdownFence(t *testing.T) {
	raw := "```json\n{\n  \"new_memories\": [],\n  \"obsolete_ids\": [\"1\"]\n}\n```"
	got := extractDreamingJSONCandidate(raw)
	want := "{\n  \"new_memories\": [],\n  \"obsolete_ids\": [\"1\"]\n}"
	if got != want {
		t.Fatalf("unexpected extracted json\nwant: %s\ngot: %s", want, got)
	}
}

func TestExtractDreamingJSONCandidate_TrimsPrefixSuffixNoise(t *testing.T) {
	raw := "Here is the result:\n{\"new_memories\":[],\"obsolete_ids\":[\"a\"]}\nExtra text"
	got := extractDreamingJSONCandidate(raw)
	want := "{\"new_memories\":[],\"obsolete_ids\":[\"a\"]}"
	if got != want {
		t.Fatalf("unexpected extracted json\nwant: %s\ngot: %s", want, got)
	}
}

func TestExtractDreamingJSONCandidate_KeepsPartialObjectForRepair(t *testing.T) {
	raw := "prefix {\"new_memories\":[{\"layer\":3,\"category\":\"summary\""
	got := extractDreamingJSONCandidate(raw)
	want := "{\"new_memories\":[{\"layer\":3,\"category\":\"summary\""
	if got != want {
		t.Fatalf("unexpected extracted json\nwant: %s\ngot: %s", want, got)
	}
}

func TestRunDreamingViaAgent_UsesResponseContentFallback(t *testing.T) {
	engine, _, conn := newReflectionTestEngine(t)
	defer conn.Close()
	engine.SetAgent(&dreamingFallbackTestAgent{events: []agent.AgentEvent{{
		Type:    agent.AgentEventTypeResponse,
		Content: `{"new_memories":[],"obsolete_ids":["frag-1"]}`,
	}}})

	got, err := engine.runDreamingViaAgent(context.Background(), "test prompt")
	if err != nil {
		t.Fatalf("runDreamingViaAgent returned error: %v", err)
	}
	if got != `{"new_memories":[],"obsolete_ids":["frag-1"]}` {
		t.Fatalf("unexpected fallback response: %s", got)
	}
}

func TestRunDreamingViaAgent_EmptyResponseReturnsError(t *testing.T) {
	engine, _, conn := newReflectionTestEngine(t)
	defer conn.Close()
	engine.SetAgent(&dreamingFallbackTestAgent{events: []agent.AgentEvent{{
		Type: agent.AgentEventTypeResponse,
	}}})

	if _, err := engine.runDreamingViaAgent(context.Background(), "test prompt"); err == nil {
		t.Fatal("expected empty fallback response to return an error")
	}
}
