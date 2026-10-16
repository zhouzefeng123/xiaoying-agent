package config

import (
	"reflect"
	"testing"
)

func TestEffectiveLSPConfigsIncludeBuiltinDefaults(t *testing.T) {
	effective := EffectiveLSPConfigs(&Config{LSP: map[string]LSPConfig{}})

	tests := []struct {
		language string
		command  string
		args     []string
	}{
		{language: "go", command: "gopls"},
		{language: "python", command: "pyright-langserver", args: []string{"--stdio"}},
		{language: "typescript", command: "typescript-language-server", args: []string{"--stdio"}},
		{language: "rust", command: "rust-analyzer"},
	}

	for _, tt := range tests {
		cfg, ok := effective[tt.language]
		if !ok {
			t.Fatalf("expected builtin LSP config for %s", tt.language)
		}
		if cfg.Command != tt.command {
			t.Fatalf("command mismatch for %s: got %q want %q", tt.language, cfg.Command, tt.command)
		}
		if !reflect.DeepEqual(cfg.Args, tt.args) {
			t.Fatalf("args mismatch for %s: got %v want %v", tt.language, cfg.Args, tt.args)
		}
		if cfg.Disabled {
			t.Fatalf("expected builtin config for %s to be enabled", tt.language)
		}
	}
}

func TestEffectiveLSPConfigsApplyOverrides(t *testing.T) {
	effective := EffectiveLSPConfigs(&Config{LSP: map[string]LSPConfig{
		"python": {
			Disabled: true,
		},
		"custom": {
			Command: "custom-lsp",
			Args:    []string{"--socket"},
		},
	}})

	pythonCfg, ok := effective["python"]
	if !ok {
		t.Fatalf("expected python config to exist")
	}
	if !pythonCfg.Disabled {
		t.Fatalf("expected python config override to remain disabled")
	}
	if pythonCfg.Command != "pyright-langserver" {
		t.Fatalf("expected builtin python command to be filled, got %q", pythonCfg.Command)
	}
	if !reflect.DeepEqual(pythonCfg.Args, []string{"--stdio"}) {
		t.Fatalf("expected builtin python args to be filled, got %v", pythonCfg.Args)
	}

	customCfg, ok := effective["custom"]
	if !ok {
		t.Fatalf("expected custom config to exist")
	}
	if customCfg.Command != "custom-lsp" {
		t.Fatalf("expected custom command to be preserved, got %q", customCfg.Command)
	}
	if !reflect.DeepEqual(customCfg.Args, []string{"--socket"}) {
		t.Fatalf("expected custom args to be preserved, got %v", customCfg.Args)
	}
}
