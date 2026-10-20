package config

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

type tenantContextKey string

const (
	tenantUserEmailKey tenantContextKey = "tenant_user_email"
	tenantWorkspaceKey tenantContextKey = "tenant_workspace_root"
)

func ContextWithUserEmail(ctx context.Context, email string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, tenantUserEmailKey, strings.ToLower(strings.TrimSpace(email)))
}

func UserEmailFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	email, _ := ctx.Value(tenantUserEmailKey).(string)
	return strings.ToLower(strings.TrimSpace(email))
}

func ContextWithWorkspace(ctx context.Context, root string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, tenantWorkspaceKey, strings.TrimSpace(root))
}

func WorkspaceFromContext(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	root, _ := ctx.Value(tenantWorkspaceKey).(string)
	return strings.TrimSpace(root)
}

// WorkingDirectoryFor returns the current user's workspace when one is present
// in the context, otherwise the configured global workspace.
func WorkingDirectoryFor(ctx context.Context) string {
	if root := WorkspaceFromContext(ctx); root != "" {
		return root
	}
	return WorkingDirectory()
}

func LegacyOwnerEmail() string {
	return strings.ToLower(strings.TrimSpace(os.Getenv("AURAMATE_LEGACY_OWNER_EMAIL")))
}

func EffectiveOwnerEmail(ctx context.Context) string {
	if email := UserEmailFromContext(ctx); email != "" {
		return email
	}
	if systemOwner := strings.ToLower(strings.TrimSpace(os.Getenv("AURAMATE_SYSTEM_OWNER_EMAIL"))); systemOwner != "" {
		return systemOwner
	}
	return "system"
}

func WorkspaceID(email string) string {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" {
		return "anonymous"
	}
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:8])
}

func CognitiveStateKey(ctx context.Context, key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	return WorkspaceID(EffectiveOwnerEmail(ctx)) + ":" + key
}

func UserWorkspace(email string) string {
	cfg := Get()
	if cfg == nil || strings.TrimSpace(cfg.WorkingDir) == "" {
		return ""
	}
	return filepath.Join(cfg.WorkingDir, "users", WorkspaceID(email))
}

func EnsureUserWorkspace(email string) (string, error) {
	root := UserWorkspace(email)
	if root == "" {
		return "", os.ErrNotExist
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return "", err
	}
	return root, nil
}

var sessionWorkspaceRegistry sync.Map

func RegisterSessionWorkspace(sessionID, root string) {
	sessionID = strings.TrimSpace(sessionID)
	root = strings.TrimSpace(root)
	if sessionID == "" || root == "" {
		return
	}
	sessionWorkspaceRegistry.Store(sessionID, root)
}

func SessionWorkspace(sessionID string) string {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return ""
	}
	if root, ok := sessionWorkspaceRegistry.Load(sessionID); ok {
		if value, ok := root.(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
