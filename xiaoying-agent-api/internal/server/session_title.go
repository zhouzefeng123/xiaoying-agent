package server

import (
	"context"
	"strings"

	"github.com/auramate/auramate/internal/session"
)

const maxSessionTitleRunes = 80

func sessionTitleFromUserMessage(content string, fallback string) string {
	title := strings.Join(strings.Fields(content), " ")
	if title == "" {
		title = strings.TrimSpace(fallback)
	}
	if title == "" {
		return "未命名对话"
	}

	runes := []rune(title)
	if len(runes) <= maxSessionTitleRunes {
		return title
	}
	return string(runes[:maxSessionTitleRunes]) + "..."
}

func isPlaceholderSessionTitle(title string) bool {
	switch strings.ToLower(strings.TrimSpace(title)) {
	case "", "new chat", "未命名对话", "normal", "coder", "life", "chat", "coding":
		return true
	default:
		return false
	}
}

func updatePlaceholderSessionTitleFromUserMessage(ctx context.Context, sessions session.Service, sessionID string, content string) error {
	if sessions == nil || strings.TrimSpace(sessionID) == "" || strings.TrimSpace(content) == "" {
		return nil
	}

	sess, err := sessions.Get(ctx, sessionID)
	if err != nil {
		return err
	}
	if !isPlaceholderSessionTitle(sess.Title) {
		return nil
	}

	nextTitle := sessionTitleFromUserMessage(content, sess.Title)
	if strings.TrimSpace(nextTitle) == "" || nextTitle == sess.Title {
		return nil
	}
	sess.Title = nextTitle
	_, err = sessions.Save(ctx, sess)
	return err
}
