package permission

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/pubsub"
	"github.com/google/uuid"
)

var ErrorPermissionDenied = errors.New("permission denied")

type CreatePermissionRequest struct {
	SessionID   string `json:"session_id"`
	ToolName    string `json:"tool_name"`
	Description string `json:"description"`
	Action      string `json:"action"`
	Params      any    `json:"params"`
	Path        string `json:"path"`
}

type PermissionRequest struct {
	ID          string `json:"id"`
	SessionID   string `json:"session_id"`
	ToolName    string `json:"tool_name"`
	Description string `json:"description"`
	Action      string `json:"action"`
	Params      any    `json:"params"`
	Path        string `json:"path"`
}

type Service interface {
	pubsub.Suscriber[PermissionRequest]
	GrantPersistant(permission PermissionRequest)
	Grant(permission PermissionRequest)
	Deny(permission PermissionRequest)
	Request(opts CreatePermissionRequest) bool
	AutoApproveSession(sessionID string)
}

type permissionService struct {
	*pubsub.Broker[PermissionRequest]

	sessionPermissions  []PermissionRequest
	pendingRequests     sync.Map
	autoApproveSessions []string
}

func (s *permissionService) GrantPersistant(permission PermissionRequest) {
	respCh, ok := s.pendingRequests.Load(permission.ID)
	if ok {
		respCh.(chan bool) <- true
	}
	s.sessionPermissions = append(s.sessionPermissions, permission)
}

func (s *permissionService) Grant(permission PermissionRequest) {
	respCh, ok := s.pendingRequests.Load(permission.ID)
	if ok {
		respCh.(chan bool) <- true
	}
}

func (s *permissionService) Deny(permission PermissionRequest) {
	respCh, ok := s.pendingRequests.Load(permission.ID)
	if ok {
		respCh.(chan bool) <- false
	}
}

func (s *permissionService) Request(opts CreatePermissionRequest) bool {
	// Environment override (highest priority)
	if os.Getenv("AURAMATE_ALLOW_ALL_TOOLS") == "true" {
		return true
	}

	// Config switch (backward-compatible default is true)
	if cfg := config.Get(); cfg != nil && cfg.Security.AllowAllTools {
		return true
	}

	// Minimal safe allowlist (read-only & introspection tools).
	// Everything else requires explicit approval.
	readOnlyTools := []string{
		"read", "read_file", "grep", "ls", "repo_map", "lookup_symbol", "get_diagnostics", "view_history",
	}
	if slices.Contains(readOnlyTools, opts.ToolName) {
		return true
	}

	// Auto-approve for session if enabled
	if slices.Contains(s.autoApproveSessions, opts.SessionID) {
		return true
	}

	dir := filepath.Dir(opts.Path)
	if dir == "." {
		dir = config.WorkingDirectory()
	}
	permission := PermissionRequest{
		ID:          uuid.New().String(),
		Path:        dir,
		SessionID:   opts.SessionID,
		ToolName:    opts.ToolName,
		Description: opts.Description,
		Action:      opts.Action,
		Params:      opts.Params,
	}

	for _, p := range s.sessionPermissions {
		if p.ToolName == permission.ToolName && p.Action == permission.Action && p.SessionID == permission.SessionID && p.Path == permission.Path {
			return true
		}
	}

	respCh := make(chan bool, 1)

	s.pendingRequests.Store(permission.ID, respCh)
	defer s.pendingRequests.Delete(permission.ID)

	s.Publish(pubsub.CreatedEvent, permission)

	// Wait for the response (UI should grant/deny)
	resp := <-respCh
	return resp
}

func (s *permissionService) AutoApproveSession(sessionID string) {
	s.autoApproveSessions = append(s.autoApproveSessions, sessionID)
}

func NewPermissionService() Service {
	return &permissionService{
		Broker:             pubsub.NewBroker[PermissionRequest](),
		sessionPermissions: make([]PermissionRequest, 0),
	}
}
