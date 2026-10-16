package skills

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/auramate/auramate/internal/llm/tools"
)

// TestSkillE2E simulates the full lifecycle:
// 1. Create a skill directory and files (simulating skill-generator)
// 2. Initialize Manager and Scan
// 3. Verify skill is loaded correctly
// 4. Run the skill and verify output
func TestSkillE2E(t *testing.T) {
	// 1. Setup Environment
	tmpDir, err := os.MkdirTemp("", "auramate_skills_test_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	skillName := "test-e2e-skill"
	skillDir := filepath.Join(tmpDir, skillName)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}

	// Create Python Script
	scriptContent := `
import argparse
import sys

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--name", required=True, help="Name to greet")
    parser.add_argument("--loud", action="store_true", help="Loud greeting")
    args = parser.parse_args()
    
    greeting = f"Hello, {args.name}"
    if args.loud:
        greeting = greeting.upper() + "!!!"
    else:
        greeting = greeting + "."
        
    print(greeting)
`
	scriptPath := filepath.Join(skillDir, "test_script.py")
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create SKILL.md
	skillMdContent := `---
name: test-e2e-skill
description: A test skill for E2E validation
language: python
script_path: test_script.py
parameters:
  name:
    type: string
    description: Name to greet
    required: true
  loud:
    type: boolean
    description: Loud greeting flag
    required: false
---

# Test E2E Skill
This is a test skill.
`
	skillMdPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillMdPath, []byte(skillMdContent), 0644); err != nil {
		t.Fatal(err)
	}

	// 2. Initialize Manager
	mgr := NewManager(tmpDir)
	mgr.Scan()

	// 3. Verify Loading
	if !mgr.IsLoaded(skillName) {
		t.Fatalf("Skill '%s' not loaded", skillName)
	}

	tool, found := mgr.GetTool(skillName)
	if !found {
		t.Fatalf("GetTool failed for '%s'", skillName)
	}

	info := tool.Info()
	if info.Name != skillName {
		t.Errorf("Expected name '%s', got '%s'", skillName, info.Name)
	}

	// Check parameters
	props, ok := info.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatal("Parameters properties missing or invalid type")
	}
	if _, ok := props["name"]; !ok {
		t.Error("Parameter 'name' missing")
	}
	if _, ok := props["loud"]; !ok {
		t.Error("Parameter 'loud' missing")
	}

	// 4. Run Skill
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Case A: Normal run
	resp, err := tool.Run(ctx, tools.ToolCall{
		Input: `{"name": "World", "loud": false}`,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if !resp.IsError && resp.Content != "Hello, World.\n" {
		// Output might contain extra newlines depending on python print
		if resp.Content != "Hello, World.\r\n" && resp.Content != "Hello, World." {
			// Relaxed check for whitespace
			t.Errorf("Expected 'Hello, World.', got %q", resp.Content)
		}
	}

	// Case B: Loud run (Boolean flag)
	resp2, err := tool.Run(ctx, tools.ToolCall{
		Input: `{"name": "World", "loud": true}`,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	// Relaxed check
	if !strings.Contains(resp2.Content, "HELLO, WORLD!!!") {
		t.Errorf("Expected 'HELLO, WORLD!!!', got %q", resp2.Content)
	}
}

func TestSkillE2EBooleanValueArgStyleFromFrontmatter(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "auramate_skills_bool_value_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	skillName := "test-bool-value-skill"
	skillDir := filepath.Join(tmpDir, skillName)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}

	scriptContent := `
import argparse

if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--name", required=True)
    parser.add_argument("--enabled", type=lambda x: str(x).lower() == "true", default=True)
    args = parser.parse_args()
    print(f"{args.name}:{args.enabled}")
`
	scriptPath := filepath.Join(skillDir, "test_script.py")
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0644); err != nil {
		t.Fatal(err)
	}

	skillMdContent := `---
name: test-bool-value-skill
description: A test skill for boolean value style
language: python
script_path: test_script.py
parameters:
  name:
    type: string
    description: Name label
    required: true
  enabled:
    type: boolean
    description: Enabled switch
    required: false
---

# Test Bool Value Skill
This is a test skill.
`
	skillMdPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillMdPath, []byte(skillMdContent), 0644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(tmpDir)
	mgr.Scan()

	tool, found := mgr.GetTool(skillName)
	if !found {
		t.Fatalf("GetTool failed for '%s'", skillName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	respTrue, err := tool.Run(ctx, tools.ToolCall{
		Input: `{"name": "demo", "enabled": true}`,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if respTrue.IsError || !strings.Contains(respTrue.Content, "demo:True") {
		t.Fatalf("expected boolean true to be passed as value, got %q", respTrue.Content)
	}

	respFalse, err := tool.Run(ctx, tools.ToolCall{
		Input: `{"name": "demo", "enabled": false}`,
	})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if respFalse.IsError || !strings.Contains(respFalse.Content, "demo:False") {
		t.Fatalf("expected boolean false to be passed as value, got %q", respFalse.Content)
	}
}

func TestPythonSkillReceivesUTF8Environment(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "auramate_skills_python_utf8_*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpDir)

	skillName := "test-python-utf8-env-skill"
	skillDir := filepath.Join(tmpDir, skillName)
	if err := os.MkdirAll(skillDir, 0755); err != nil {
		t.Fatal(err)
	}

	scriptContent := `
import os

if __name__ == "__main__":
    print(f"PYTHONIOENCODING={os.environ.get('PYTHONIOENCODING', '')}")
    print(f"PYTHONUTF8={os.environ.get('PYTHONUTF8', '')}")
`
	scriptPath := filepath.Join(skillDir, "test_script.py")
	if err := os.WriteFile(scriptPath, []byte(scriptContent), 0644); err != nil {
		t.Fatal(err)
	}

	skillMdContent := `---
name: test-python-utf8-env-skill
description: Verifies Python skills receive UTF-8 environment variables
language: python
script_path: test_script.py
parameters: {}
---

# Test Python UTF-8 Env Skill
`
	skillMdPath := filepath.Join(skillDir, "SKILL.md")
	if err := os.WriteFile(skillMdPath, []byte(skillMdContent), 0644); err != nil {
		t.Fatal(err)
	}

	mgr := NewManager(tmpDir)
	mgr.Scan()

	tool, found := mgr.GetTool(skillName)
	if !found {
		t.Fatalf("GetTool failed for '%s'", skillName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, err := tool.Run(ctx, tools.ToolCall{Input: `{}`})
	if err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if resp.IsError {
		t.Fatalf("expected success response, got error: %q", resp.Content)
	}
	if !strings.Contains(resp.Content, "PYTHONIOENCODING=utf-8") {
		t.Fatalf("expected PYTHONIOENCODING=utf-8, got %q", resp.Content)
	}
	if !strings.Contains(resp.Content, "PYTHONUTF8=1") {
		t.Fatalf("expected PYTHONUTF8=1, got %q", resp.Content)
	}
}
