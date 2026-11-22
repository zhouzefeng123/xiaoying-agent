package routing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"encoding/json"
	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/llm/provider"
	llmtools "github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/message"
	"net/http"
	"net/http/httptest"
)

type stubProvider struct {
	model    models.Model
	events   []provider.ProviderEvent
	sendResp *provider.ProviderResponse
	sendErr  error
}

func (p *stubProvider) SendMessages(context.Context, []message.Message, []llmtools.BaseTool) (*provider.ProviderResponse, error) {
	if p.sendErr != nil {
		return nil, p.sendErr
	}
	return p.sendResp, nil
}

func (p *stubProvider) StreamResponse(context.Context, []message.Message, []llmtools.BaseTool) <-chan provider.ProviderEvent {
	out := make(chan provider.ProviderEvent, len(p.events))
	go func() {
		defer close(out)
		for _, event := range p.events {
			out <- event
		}
	}()
	return out
}

func (p *stubProvider) Model() models.Model {
	return p.model
}

func (p *stubProvider) Transcribe(context.Context, []byte, string) (string, error) {
	return "", nil
}

func (p *stubProvider) GenerateSpeech(context.Context, string, string) ([]byte, error) {
	return nil, nil
}

func TestFallbackProviderRetriesBeforeSubstantiveOutput(t *testing.T) {
	first := &stubProvider{
		model: models.Model{ID: models.ModelID("qwen3.5-plus")},
		events: []provider.ProviderEvent{
			{Type: provider.EventThinkingDelta, Thinking: "thinking"},
			{Type: provider.EventError, Error: errors.New("401 unauthorized")},
		},
	}
	second := &stubProvider{
		model: models.Model{ID: models.ModelID("kimi-k2.5")},
		events: []provider.ProviderEvent{
			{Type: provider.EventContentDelta, Content: "OK"},
			{Type: provider.EventComplete, Response: &provider.ProviderResponse{Content: "OK"}},
		},
	}

	wrapped := newFallbackProvider([]provider.Provider{first, second}, []string{"qwen3.5-plus", "kimi-k2.5"})
	collected := make([]provider.ProviderEvent, 0, 4)
	for event := range wrapped.StreamResponse(context.Background(), nil, nil) {
		collected = append(collected, event)
	}

	if len(collected) != 2 {
		t.Fatalf("expected 2 events from fallback success path, got %d", len(collected))
	}
	if collected[0].Type != provider.EventContentDelta || collected[0].Content != "OK" {
		t.Fatalf("unexpected first collected event: %#v", collected[0])
	}
	if collected[1].Type != provider.EventComplete {
		t.Fatalf("expected completion event from second provider, got %#v", collected[1])
	}
}

func TestFallbackProviderStopsAfterSubstantiveOutput(t *testing.T) {
	first := &stubProvider{
		model: models.Model{ID: models.ModelID("qwen3.5-plus")},
		events: []provider.ProviderEvent{
			{Type: provider.EventContentDelta, Content: "partial"},
			{Type: provider.EventError, Error: errors.New("downstream error")},
		},
	}
	second := &stubProvider{
		model: models.Model{ID: models.ModelID("kimi-k2.5")},
		events: []provider.ProviderEvent{
			{Type: provider.EventContentDelta, Content: "should-not-run"},
		},
	}

	wrapped := newFallbackProvider([]provider.Provider{first, second}, []string{"qwen3.5-plus", "kimi-k2.5"})
	collected := make([]provider.ProviderEvent, 0, 4)
	for event := range wrapped.StreamResponse(context.Background(), nil, nil) {
		collected = append(collected, event)
	}

	if len(collected) != 2 {
		t.Fatalf("expected substantive first provider events to pass through without fallback, got %d", len(collected))
	}
	if collected[0].Content != "partial" {
		t.Fatalf("unexpected first content: %#v", collected[0])
	}
	if collected[1].Type != provider.EventError {
		t.Fatalf("expected first provider error to be emitted after substantive output, got %#v", collected[1])
	}
}

func TestConfiguredModelUsesOwnEndpointAndKeyWithoutMembership(t *testing.T) {
	var seen bool
	expectedModel, expectedAuthorization := string(models.DeepSeekV4Pro), "Bearer own-key"
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = true
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != expectedAuthorization {
			t.Error("own API key was not used")
		}
		if r.URL.Query().Get("machine_code") != "" {
			t.Error("unexpected platform machine code")
		}
		var body struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != expectedModel {
			t.Errorf("model name changed: %s", body.Model)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"test","object":"chat.completion","model":"deepseek-v4-pro","choices":[{"index":0,"message":{"role":"assistant","content":"own model works"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":3,"total_tokens":4}}`))
	}))
	defer upstream.Close()
	dir := t.TempDir()
	path := filepath.Join(dir, ".auramate.json")
	payload, _ := json.Marshal(map[string]any{"main_model": map[string]any{"provider": "openai", "model": string(models.DeepSeekV4Pro), "apiKey": "own-key", "baseURL": upstream.URL + "/v1"}})
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AURAMATE_CONFIG_FILE", path)
	if _, err := config.Load(dir, false); err != nil {
		t.Fatal(err)
	}
	p, _, err := BuildProviderForAgent(config.AgentNormal, "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	response, err := p.SendMessages(context.Background(), []message.Message{{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hello"}}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !seen || response.Content != "own model works" {
		t.Fatalf("unexpected response: %+v", response)
	}
	config.ApplyMainModel(&config.MainModelConfig{Provider: models.ProviderCustom, Model: "updated-model", APIKey: "updated-key", BaseURL: upstream.URL + "/v1"})
	next, _, err := BuildProviderForAgent(config.AgentTask, "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if next.Model().APIModel != "updated-model" || next.Model().APIKey != "updated-key" {
		t.Fatal("new requests did not use saved configuration")
	}
	// A keyless local server must use its configured port and ignore global keys.
	t.Setenv("OPENAI_API_KEY", "unrelated-environment-key")
	expectedModel, expectedAuthorization = "own-local-model", ""
	config.ApplyMainModel(&config.MainModelConfig{Provider: models.ProviderOllama, Model: expectedModel, BaseURL: upstream.URL + "/v1"})
	local, _, err := BuildProviderForAgent(config.AgentNormal, "", "", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := local.SendMessages(context.Background(), []message.Message{{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hello"}}}}, nil); err != nil {
		t.Fatal(err)
	}
	config.ApplyMainModel(nil)
	if _, _, err := BuildProviderForAgent(config.AgentNormal, "", "", 0); err == nil {
		t.Fatal("missing settings must not fall back to a platform model")
	}
}
