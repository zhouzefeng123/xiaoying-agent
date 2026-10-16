package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/auramate/auramate/internal/llm/models"
	"github.com/spf13/viper"
)

func resetConfigGlobalsForTest(t *testing.T) {
	t.Helper()
	cfg = nil
	viper.Reset()
	t.Cleanup(func() {
		cfg = nil
		viper.Reset()
	})
}

func TestLoadPrefersExplicitConfigFileOverWorkingDirLocalConfig(t *testing.T) {
	resetConfigGlobalsForTest(t)

	workingDir := t.TempDir()
	explicitDir := filepath.Join(t.TempDir(), "user-data")
	explicitConfig := filepath.Join(explicitDir, ".auramate.json")
	legacyConfig := filepath.Join(workingDir, ".auramate.json")

	if err := os.MkdirAll(explicitDir, 0o755); err != nil {
		t.Fatalf("mkdir explicit dir: %v", err)
	}
	if err := os.WriteFile(explicitConfig, []byte("{\n  \"auraAuth\": {\n    \"token\": \"user-token\"\n  }\n}"), 0o644); err != nil {
		t.Fatalf("write explicit config: %v", err)
	}
	if err := os.WriteFile(legacyConfig, []byte("{\n  \"auraAuth\": {\n    \"token\": \"legacy-token\"\n  }\n}"), 0o644); err != nil {
		t.Fatalf("write working dir config: %v", err)
	}

	t.Setenv("AURAMATE_CONFIG_FILE", explicitConfig)

	loaded, err := Load(workingDir, false)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if loaded.AuraAuth == nil || loaded.AuraAuth.Token != "user-token" {
		t.Fatalf("expected explicit config token to win, got %+v", loaded.AuraAuth)
	}
	if got := os.Getenv("AURAMATE_CONFIG_FILE"); got != explicitConfig {
		t.Fatalf("expected explicit config path to be preserved, got %q", got)
	}
}

func TestMainConfigPathPrefersExplicitConfigFile(t *testing.T) {
	resetConfigGlobalsForTest(t)

	explicitConfig := filepath.Join(t.TempDir(), "user-data", ".auramate.json")
	t.Setenv("AURAMATE_CONFIG_FILE", explicitConfig)

	if got := MainConfigPath(); got != explicitConfig {
		t.Fatalf("expected explicit config path %q, got %q", explicitConfig, got)
	}
}

func TestMainConfigPathPrefersBackendConfigInRepoLayout(t *testing.T) {
	resetConfigGlobalsForTest(t)

	repoRoot := t.TempDir()
	backendDir := filepath.Join(repoRoot, "backend")
	backendConfig := filepath.Join(backendDir, ".auramate.json")

	if err := os.MkdirAll(backendDir, 0o755); err != nil {
		t.Fatalf("mkdir backend dir: %v", err)
	}
	if err := os.WriteFile(backendConfig, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write backend config: %v", err)
	}

	cfg = &Config{AppRoot: repoRoot}

	if got := MainConfigPath(); got != backendConfig {
		t.Fatalf("expected backend config path %q, got %q", backendConfig, got)
	}
}

func TestLoadPrefersBackendConfigInRepoLayoutWhenWorkingDirOverridesLocalMerge(t *testing.T) {
	resetConfigGlobalsForTest(t)

	repoRoot := t.TempDir()
	backendDir := filepath.Join(repoRoot, "backend")
	workspaceDir := filepath.Join(repoRoot, "workspace")
	rootConfig := filepath.Join(repoRoot, ".auramate.json")
	backendConfig := filepath.Join(backendDir, ".auramate.json")

	if err := os.MkdirAll(backendDir, 0o755); err != nil {
		t.Fatalf("mkdir backend dir: %v", err)
	}
	if err := os.MkdirAll(workspaceDir, 0o755); err != nil {
		t.Fatalf("mkdir workspace dir: %v", err)
	}
	if err := os.WriteFile(rootConfig, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write root config: %v", err)
	}
	if err := os.WriteFile(backendConfig, []byte("{\n  \"agents\": {\n    \"coder\": {\n      \"model\": \"qwen3.6-plus\",\n      \"maxTokens\": 8192,\n      \"reasoningEffort\": \"\"\n    }\n  }\n}"), 0o644); err != nil {
		t.Fatalf("write backend config: %v", err)
	}

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(backendDir); err != nil {
		t.Fatalf("chdir backend dir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	loaded, err := Load(workspaceDir, false)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if _, ok := loaded.Agents[AgentCoder]; !ok {
		t.Fatalf("expected coder agent to be loaded from backend config, got %+v", loaded.Agents)
	}
	if loaded.Agents[AgentCoder].Model != models.Qwen36Plus {
		t.Fatalf("expected coder model %q, got %q", models.Qwen36Plus, loaded.Agents[AgentCoder].Model)
	}
	if got := os.Getenv("AURAMATE_CONFIG_FILE"); got != backendConfig {
		t.Fatalf("expected backend config path to be exported, got %q", got)
	}
}

func TestLoadMergesPackagedDefaultsIntoExplicitUserConfig(t *testing.T) {
	resetConfigGlobalsForTest(t)

	repoRoot := t.TempDir()
	userDataDir := filepath.Join(t.TempDir(), "userdata")
	explicitConfig := filepath.Join(userDataDir, ".auramate.json")
	defaultsConfig := filepath.Join(repoRoot, ".auramate.defaults.json")

	if err := os.MkdirAll(userDataDir, 0o755); err != nil {
		t.Fatalf("mkdir userData dir: %v", err)
	}
	if err := os.WriteFile(explicitConfig, []byte("{}"), 0o644); err != nil {
		t.Fatalf("write explicit config: %v", err)
	}
	if err := os.WriteFile(defaultsConfig, []byte("{\n  \"agents\": {\n    \"coder\": {\n      \"model\": \"qwen3.6-plus\",\n      \"maxTokens\": 8192,\n      \"reasoningEffort\": \"\"\n    }\n  }\n}"), 0o644); err != nil {
		t.Fatalf("write defaults config: %v", err)
	}

	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(repoRoot); err != nil {
		t.Fatalf("chdir repo root: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chdir(originalWD)
	})

	t.Setenv("AURAMATE_CONFIG_FILE", explicitConfig)

	loaded, err := Load(filepath.Join(repoRoot, "workspace"), false)
	if err != nil {
		t.Fatalf("Load returned error: %v", err)
	}
	if _, ok := loaded.Agents[AgentCoder]; !ok {
		t.Fatalf("expected coder agent to be backfilled from defaults, got %+v", loaded.Agents)
	}
	if loaded.Agents[AgentCoder].Model != models.Qwen36Plus {
		t.Fatalf("expected coder model %q, got %q", models.Qwen36Plus, loaded.Agents[AgentCoder].Model)
	}
}
