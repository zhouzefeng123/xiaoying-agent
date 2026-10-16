package vision

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/models"
	llmtools "github.com/auramate/auramate/internal/llm/tools"
)

func TestBuildVisualAnalysisProviderIgnoresTextModelOverrideCandidates(t *testing.T) {
	configDir := t.TempDir()
	configPath := filepath.Join(configDir, ".auramate.json")
	content := "{\n  \"auraAuth\": {\n    \"token\": \"test-aura-token\"\n  }\n}"
	if err := os.WriteFile(configPath, []byte(content), 0644); err != nil {
		t.Fatalf("write test config: %v", err)
	}
	t.Setenv("AURAMATE_CONFIG_FILE", configPath)
	if _, err := config.Load(configDir, false); err != nil {
		t.Fatalf("load test config: %v", err)
	}

	ctx := context.WithValue(context.Background(), llmtools.ModelOverrideCandidatesContextKey, []string{string(models.DeepSeekV4Flash)})

	provider, err := buildVisualAnalysisProvider(ctx, "")
	if err != nil {
		t.Fatalf("buildVisualAnalysisProvider returned error: %v", err)
	}
	if provider.Model().ID != models.DoubaoSeed20Pro {
		t.Fatalf("expected visual provider to use doubao route, got %s", provider.Model().ID)
	}
}
