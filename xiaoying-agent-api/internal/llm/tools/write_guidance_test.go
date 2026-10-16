package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestWriteToolOversizeGuidanceAvoidsSmallerWrites(t *testing.T) {
	payload, err := json.Marshal(map[string]string{
		"file_path": "docs/huge.txt",
		"content":   strings.Repeat("x", MaxWriteContentChars+1),
	})
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}

	resp, runErr := (&writeTool{}).Run(context.Background(), ToolCall{Name: WriteToolName, Input: string(payload)})
	if runErr != nil {
		t.Fatalf("write tool returned error: %v", runErr)
	}
	if !resp.IsError {
		t.Fatalf("expected oversized write to return error response")
	}
	if strings.Contains(resp.Content, "smaller writes") {
		t.Fatalf("expected oversized write guidance to avoid impossible smaller writes advice, got %q", resp.Content)
	}
	if !strings.Contains(resp.Content, "patch") || !strings.Contains(resp.Content, "edit") {
		t.Fatalf("expected oversized write guidance to mention chunked edit tools, got %q", resp.Content)
	}
	if !strings.Contains(resp.Content, "scaffold") {
		t.Fatalf("expected oversized write guidance to mention scaffold strategy, got %q", resp.Content)
	}
}
