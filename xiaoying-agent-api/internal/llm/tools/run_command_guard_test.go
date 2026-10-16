package tools

import (
	"strings"
	"testing"
)

func TestNewProtectedRunCommandTargetsIncludesCurrentAndOwnerPID(t *testing.T) {
	targets := newProtectedRunCommandTargets(4321, "5678")
	if !containsString(targets.pids, "4321") {
		t.Fatalf("expected current backend pid to be protected, got %#v", targets.pids)
	}
	if !containsString(targets.pids, "5678") {
		t.Fatalf("expected owner pid to be protected, got %#v", targets.pids)
	}
	if !containsString(targets.names, "auramate-server.exe") {
		t.Fatalf("expected auramate-server.exe to be protected, got %#v", targets.names)
	}
	if !containsString(targets.names, "electron.exe") {
		t.Fatalf("expected electron.exe to be protected, got %#v", targets.names)
	}
}

func TestProtectedRunCommandGuardBlocksProtectedPIDCommands(t *testing.T) {
	targets := newProtectedRunCommandTargets(4321, "5678")

	tests := []string{
		"taskkill /pid 4321 /f",
		"Stop-Process -Id 5678 -Force",
		"cmd /c taskkill /pid 5678 /f",
	}

	for _, command := range tests {
		reason := protectedRunCommandGuardReason(command, targets)
		if reason == "" {
			t.Fatalf("expected protected pid command %q to be blocked", command)
		}
		if !strings.Contains(reason, "protected PID") {
			t.Fatalf("expected pid-focused reason for %q, got %q", command, reason)
		}
	}
}

func TestProtectedRunCommandGuardBlocksProtectedProcessAndServiceNames(t *testing.T) {
	targets := newProtectedRunCommandTargets(4321, "5678")

	tests := []string{
		"taskkill /im auramate-server.exe /f",
		"Get-Process electron | Stop-Process -Force",
		"sc stop auramate-server",
		"Stop-Service -Name electron",
	}

	for _, command := range tests {
		reason := protectedRunCommandGuardReason(command, targets)
		if reason == "" {
			t.Fatalf("expected protected process command %q to be blocked", command)
		}
		if !strings.Contains(reason, "protected") {
			t.Fatalf("expected protected-process reason for %q, got %q", command, reason)
		}
	}
}

func TestProtectedRunCommandGuardAllowsNonProtectedCommands(t *testing.T) {
	targets := newProtectedRunCommandTargets(4321, "5678")

	tests := []string{
		"go test ./...",
		"taskkill /pid 99999 /f",
		"sc stop unrelated-service",
		"echo taskkill /im electron.exe",
	}

	for _, command := range tests {
		if reason := protectedRunCommandGuardReason(command, targets); reason != "" {
			t.Fatalf("expected command %q to be allowed, got %q", command, reason)
		}
	}
}

func TestNormalizePowerShellAndAndRewritesOnlyUnquotedOperators(t *testing.T) {
	command := `go test ./... && npm run build && Write-Output "literal && stays"`
	normalized, changed := normalizePowerShellAndAnd(command)
	if !changed {
		t.Fatalf("expected unquoted && to be normalized")
	}
	if strings.Contains(normalized, "go test ./... && npm") {
		t.Fatalf("expected command separator to be rewritten, got %q", normalized)
	}
	if !strings.Contains(normalized, `Write-Output "literal && stays"`) {
		t.Fatalf("expected quoted && to be preserved, got %q", normalized)
	}
	if strings.Count(normalized, "if (-not $?) { exit 1 }") != 2 {
		t.Fatalf("expected fail-fast guards for both separators, got %q", normalized)
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
