package routing

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/auramate/auramate/internal/common"
	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/llm/provider"
	llmtools "github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/message"
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

func TestResolveAuraAuthProxyModelRoutesVolcModelViaAuraProxy(t *testing.T) {
	loadRoutingTestConfigWithAuraToken(t, "test-aura-token")

	resolved, err := resolveAuraAuthProxyModel(models.Model{
		ID:       models.DeepSeekV4Flash,
		Name:     "deepseek-v4-flash",
		Provider: models.ProviderOpenAI,
		APIModel: string(models.DeepSeekV4Flash),
		APIKey:   "direct-key",
		BaseURL:  "https://ark.example.com/v1",
	})
	if err != nil {
		t.Fatalf("resolveAuraAuthProxyModel returned error: %v", err)
	}
	if resolved.Provider != models.ProviderCustom {
		t.Fatalf("expected volc route to use custom proxy provider, got %s", resolved.Provider)
	}
	if resolved.APIKey != "test-aura-token" {
		t.Fatalf("expected aura token to replace direct api key, got %q", resolved.APIKey)
	}
	if resolved.BaseURL != common.AuraLLMProxyURL {
		t.Fatalf("expected aura auth proxy URL, got %q", resolved.BaseURL)
	}
}

func TestResolveAuraAuthProxyModelRoutesDoubaoVisionModelViaAuraProxy(t *testing.T) {
	loadRoutingTestConfigWithAuraToken(t, "test-aura-token")

	resolved, err := resolveAuraAuthProxyModel(models.Model{
		ID:       models.DoubaoSeed20Pro,
		Name:     "Doubao Seed 2.0 Pro",
		Provider: models.ProviderCustom,
		APIModel: string(models.DoubaoSeed20Pro),
	})
	if err != nil {
		t.Fatalf("resolveAuraAuthProxyModel returned error: %v", err)
	}
	if resolved.Provider != models.ProviderCustom {
		t.Fatalf("expected doubao route to use custom proxy provider, got %s", resolved.Provider)
	}
	if resolved.APIKey != "test-aura-token" {
		t.Fatalf("expected aura token to be applied, got %q", resolved.APIKey)
	}
	if resolved.BaseURL != common.AuraLLMProxyURL {
		t.Fatalf("expected aura auth proxy URL, got %q", resolved.BaseURL)
	}
}

func TestResolveAuraAuthProxyModelRoutesCustomMainVolcModelViaAuraProxy(t *testing.T) {
	loadRoutingTestConfigWithAuraToken(t, "test-aura-token")

	resolved, err := resolveAuraAuthProxyModel(models.Model{
		ID:       CustomMainModelID,
		Name:     "Main Model (Custom)",
		Provider: models.ProviderOpenAI,
		APIModel: string(models.DeepSeekV4Pro),
	})
	if err != nil {
		t.Fatalf("resolveAuraAuthProxyModel returned error: %v", err)
	}
	if resolved.Provider != models.ProviderCustom {
		t.Fatalf("expected custom-main-model volc route to use custom proxy provider, got %s", resolved.Provider)
	}
	if resolved.APIKey != "test-aura-token" {
		t.Fatalf("expected aura token to be applied, got %q", resolved.APIKey)
	}
	if resolved.BaseURL != common.AuraLLMProxyURL {
		t.Fatalf("expected aura auth proxy URL, got %q", resolved.BaseURL)
	}
	if resolved.APIModel != string(models.DeepSeekV4Pro) {
		t.Fatalf("expected volc api model to be preserved, got %q", resolved.APIModel)
	}
}

func TestResolveAuraAuthProxyModelRejectsVolcModelWithoutAuraToken(t *testing.T) {
	loadRoutingTestConfigWithAuraToken(t, "")

	resolved, err := resolveAuraAuthProxyModel(models.Model{
		ID:       models.GLM51,
		Name:     "glm-5.1",
		Provider: models.ProviderCustom,
		APIModel: string(models.GLM51),
	})
	if err == nil {
		t.Fatalf("expected missing aura token to reject volc route, got resolved=%+v", resolved)
	}
	if resolved != (models.Model{}) {
		t.Fatalf("expected zero-value model on rejection, got %+v", resolved)
	}
	if err.Error() != "请先登录后再使用 glm-5.1" {
		t.Fatalf("unexpected error message: %v", err)
	}
}

func loadRoutingTestConfigWithAuraToken(t *testing.T, token string) {
	t.Helper()

	configDir := t.TempDir()
	configPath := filepath.Join(configDir, ".auramate.json")
	content := "{\n  \"auraAuth\": {\n    \"token\": \"" + token + "\"\n  }\n}"
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	t.Setenv("AURAMATE_CONFIG_FILE", configPath)
	loaded, err := config.Load(configDir, false)
	if err != nil {
		t.Fatalf("load test config: %v", err)
	}
	if loaded.AuraAuth == nil {
		loaded.AuraAuth = &config.AuraAuthConfig{Token: token}
		return
	}
	loaded.AuraAuth.Token = token
}
