package server

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSessionTitleFromUserMessageUsesUserText(t *testing.T) {
	got := sessionTitleFromUserMessage("  现在先 new chat\n显示用户发送的消息  ", "New Chat")
	want := "现在先 new chat 显示用户发送的消息"
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestSessionTitleFromUserMessageFallsBackForEmptyText(t *testing.T) {
	got := sessionTitleFromUserMessage("   ", "未命名对话")
	if got != "未命名对话" {
		t.Fatalf("expected fallback title, got %q", got)
	}
}

func TestSessionTitleFromUserMessageTruncatesByRunes(t *testing.T) {
	got := sessionTitleFromUserMessage(strings.Repeat("测", maxSessionTitleRunes+10), "New Chat")
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("expected ellipsis suffix, got %q", got)
	}
	if utf8.RuneCountInString(strings.TrimSuffix(got, "...")) != maxSessionTitleRunes {
		t.Fatalf("expected %d title runes before ellipsis, got %d", maxSessionTitleRunes, utf8.RuneCountInString(strings.TrimSuffix(got, "...")))
	}
}

func TestIsPlaceholderSessionTitle(t *testing.T) {
	for _, title := range []string{"", "New Chat", "未命名对话", "normal", "coder", "life"} {
		if !isPlaceholderSessionTitle(title) {
			t.Fatalf("expected %q to be placeholder", title)
		}
	}
	if isPlaceholderSessionTitle("用户真实问题") {
		t.Fatal("did not expect user title to be placeholder")
	}
}
