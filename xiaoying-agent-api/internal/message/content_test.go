package message

import "testing"

func TestAppendToolCallDeltaUsesActualToolIndex(t *testing.T) {
	msg := Message{}

	msg.AppendToolCallDelta(2, "tool-read", "read", "")
	msg.AppendToolCallDelta(0, "tool-ls", "ls", `{"path":"workspace"}`)

	toolCalls := msg.ToolCalls()
	if len(toolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(toolCalls))
	}

	if toolCalls[0].Index != 2 || toolCalls[0].Name != "read" {
		t.Fatalf("unexpected first tool call: %#v", toolCalls[0])
	}
	if toolCalls[1].Index != 0 || toolCalls[1].Name != "ls" {
		t.Fatalf("unexpected second tool call: %#v", toolCalls[1])
	}
}

func TestAppendToolCallDeltaAppendsToMatchingIndex(t *testing.T) {
	msg := Message{}

	msg.AppendToolCallDelta(2, "tool-read", "read", `{"file_path":"backend/`)
	msg.AppendToolCallDelta(0, "tool-ls", "ls", `{"path":"workspace"}`)
	msg.AppendToolCallDelta(2, "tool-read", "", `README.md"}`)

	toolCalls := msg.ToolCalls()
	if len(toolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(toolCalls))
	}

	if toolCalls[0].Input != `{"file_path":"backend/README.md"}` {
		t.Fatalf("unexpected read input: %q", toolCalls[0].Input)
	}
}
