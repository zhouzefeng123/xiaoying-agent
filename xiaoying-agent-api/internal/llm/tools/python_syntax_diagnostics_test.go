package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetPythonSyntaxDiagnosticsReportsSyntaxErrors(t *testing.T) {
	if _, err := exec.LookPath("python"); err != nil {
		t.Skip("python not available")
	}
	path := filepath.Join(t.TempDir(), "bad.py")
	if err := os.WriteFile(path, []byte("def f():\n  return 1\n    return 2\n"), 0o600); err != nil {
		t.Fatalf("write bad python: %v", err)
	}

	got := getPythonSyntaxDiagnostics(context.Background(), path)
	if !strings.Contains(got, "py_compile failed") || !strings.Contains(got, "IndentationError") {
		t.Fatalf("expected syntax diagnostics, got %q", got)
	}
}
