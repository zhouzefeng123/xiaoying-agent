package lsp

import (
	"testing"

	"github.com/auramate/auramate/internal/lsp/protocol"
)

func TestGetDiagnosticsReturnsSnapshot(t *testing.T) {
	uri := protocol.DocumentUri("file:///tmp/demo.go")
	client := &Client{
		diagnostics: map[protocol.DocumentUri][]protocol.Diagnostic{
			uri: {
				{Message: "original"},
			},
		},
	}

	snapshot := client.GetDiagnostics()
	snapshot[uri][0].Message = "changed"
	snapshot[protocol.DocumentUri("file:///tmp/other.go")] = []protocol.Diagnostic{{Message: "new"}}

	got := client.GetDiagnostics()
	if got[uri][0].Message != "original" {
		t.Fatalf("expected internal diagnostics to remain unchanged, got %q", got[uri][0].Message)
	}
	if _, exists := got[protocol.DocumentUri("file:///tmp/other.go")]; exists {
		t.Fatalf("expected snapshot map mutation not to affect internal diagnostics")
	}
}

func TestGetFileDiagnosticsReturnsSnapshot(t *testing.T) {
	uri := protocol.DocumentUri("file:///tmp/demo.go")
	client := &Client{
		diagnostics: map[protocol.DocumentUri][]protocol.Diagnostic{
			uri: {
				{Message: "original"},
			},
		},
	}

	snapshot := client.GetFileDiagnostics(uri)
	snapshot[0].Message = "changed"

	got := client.GetFileDiagnostics(uri)
	if got[0].Message != "original" {
		t.Fatalf("expected internal file diagnostics to remain unchanged, got %q", got[0].Message)
	}
}
