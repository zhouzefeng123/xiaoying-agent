package lsp

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

type RuntimePhase string

const (
	RuntimePhaseIdle       RuntimePhase = "idle"
	RuntimePhaseInstalling RuntimePhase = "installing"
	RuntimePhaseStarting   RuntimePhase = "starting"
	RuntimePhaseReady      RuntimePhase = "ready"
	RuntimePhaseFailed     RuntimePhase = "failed"
	RuntimePhaseBackoff    RuntimePhase = "backoff"
)

const (
	retryBaseDelay = 30 * time.Second
	retryMaxDelay  = 10 * time.Minute
)

type ClientRuntimeStatus struct {
	Language            string       `json:"language"`
	Phase               RuntimePhase `json:"phase"`
	Message             string       `json:"message,omitempty"`
	LastError           string       `json:"last_error,omitempty"`
	Command             string       `json:"command,omitempty"`
	ResolvedCommand     string       `json:"resolved_command,omitempty"`
	InstallCommand      string       `json:"install_command,omitempty"`
	DocsURL             string       `json:"docs_url,omitempty"`
	LastAttemptAt       time.Time    `json:"last_attempt_at,omitempty"`
	LastSuccessAt       time.Time    `json:"last_success_at,omitempty"`
	NextRetryAt         time.Time    `json:"next_retry_at,omitempty"`
	ConsecutiveFailures int          `json:"consecutive_failures,omitempty"`
}

func (s ClientRuntimeStatus) Summary() string {
	language := strings.TrimSpace(s.Language)
	if language == "" {
		language = "unknown"
	}

	switch s.Phase {
	case RuntimePhaseInstalling:
		return fmt.Sprintf("%s LSP is installing dependencies", language)
	case RuntimePhaseStarting:
		return fmt.Sprintf("%s LSP is starting", language)
	case RuntimePhaseReady:
		return fmt.Sprintf("%s LSP is ready", language)
	case RuntimePhaseBackoff, RuntimePhaseFailed:
		summary := fmt.Sprintf("%s LSP failed to start", language)
		if s.LastError != "" {
			summary += ": " + s.LastError
		}
		if !s.NextRetryAt.IsZero() {
			summary += fmt.Sprintf(". Next retry after %s", s.NextRetryAt.Format(time.RFC3339))
		}
		return summary
	default:
		return fmt.Sprintf("%s LSP has not started yet", language)
	}
}

type Registry struct {
	mu       sync.RWMutex
	clients  map[string]*Client
	statuses map[string]ClientRuntimeStatus
}

func NewRegistry() *Registry {
	return &Registry{
		clients:  make(map[string]*Client),
		statuses: make(map[string]ClientRuntimeStatus),
	}
}

func (r *Registry) Snapshot() map[string]*Client {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	cloned := make(map[string]*Client, len(r.clients))
	for name, client := range r.clients {
		cloned[name] = client
	}
	return cloned
}

func (r *Registry) Get(name string) (*Client, bool) {
	if r == nil {
		return nil, false
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	client, ok := r.clients[name]
	return client, ok
}

func (r *Registry) Set(name string, client *Client) {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	r.clients[name] = client
}

func (r *Registry) Delete(name string) {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.clients, name)
}

func (r *Registry) Status(name string) (ClientRuntimeStatus, bool) {
	if r == nil {
		return ClientRuntimeStatus{}, false
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	status, ok := r.statuses[name]
	return status, ok
}

func (r *Registry) StatusSnapshot() map[string]ClientRuntimeStatus {
	if r == nil {
		return nil
	}

	r.mu.RLock()
	defer r.mu.RUnlock()

	cloned := make(map[string]ClientRuntimeStatus, len(r.statuses))
	for name, status := range r.statuses {
		cloned[name] = status
	}
	return cloned
}

func (r *Registry) TryBeginStart(name string, command string) (bool, ClientRuntimeStatus) {
	if r == nil {
		return false, ClientRuntimeStatus{}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	status := r.ensureStatusLocked(name, command)
	if client, exists := r.clients[name]; exists {
		switch client.GetServerState() {
		case StateReady:
			status.Phase = RuntimePhaseReady
			status.Message = fmt.Sprintf("%s LSP client is already available", name)
			r.statuses[name] = status
			return false, status
		case StateStarting:
			status.Phase = RuntimePhaseStarting
			status.Message = fmt.Sprintf("%s LSP is starting", name)
			r.statuses[name] = status
			return false, status
		default:
			delete(r.clients, name)
		}
	}

	now := time.Now()
	if status.Phase == RuntimePhaseInstalling || status.Phase == RuntimePhaseStarting {
		return false, status
	}

	if !status.NextRetryAt.IsZero() && now.Before(status.NextRetryAt) {
		status.Phase = RuntimePhaseBackoff
		status.Message = status.Summary()
		r.statuses[name] = status
		return false, status
	}

	status.Phase = RuntimePhaseStarting
	status.Message = fmt.Sprintf("%s LSP startup scheduled", name)
	status.Command = firstNonEmpty(command, status.Command)
	status.LastAttemptAt = now
	r.statuses[name] = status
	return true, status
}

func (r *Registry) MarkInstalling(name string, command string) {
	r.updateStatus(name, func(status ClientRuntimeStatus) ClientRuntimeStatus {
		status.Phase = RuntimePhaseInstalling
		status.Message = fmt.Sprintf("%s LSP is installing dependencies", name)
		status.Command = firstNonEmpty(command, status.Command)
		status.LastAttemptAt = time.Now()
		return status
	})
}

func (r *Registry) MarkStarting(name string, command string, resolvedCommand string) {
	r.updateStatus(name, func(status ClientRuntimeStatus) ClientRuntimeStatus {
		status.Phase = RuntimePhaseStarting
		status.Message = fmt.Sprintf("%s LSP is starting", name)
		status.Command = firstNonEmpty(command, status.Command)
		status.ResolvedCommand = firstNonEmpty(resolvedCommand, status.ResolvedCommand)
		status.LastAttemptAt = time.Now()
		return status
	})
}

func (r *Registry) MarkReady(name string, command string, resolvedCommand string) {
	r.updateStatus(name, func(status ClientRuntimeStatus) ClientRuntimeStatus {
		status.Phase = RuntimePhaseReady
		status.Message = fmt.Sprintf("%s LSP is ready", name)
		status.LastError = ""
		status.Command = firstNonEmpty(command, status.Command)
		status.ResolvedCommand = firstNonEmpty(resolvedCommand, status.ResolvedCommand)
		status.LastSuccessAt = time.Now()
		status.NextRetryAt = time.Time{}
		status.ConsecutiveFailures = 0
		return status
	})
}

func (r *Registry) MarkFailure(name string, command string, err error) ClientRuntimeStatus {
	if r == nil {
		return ClientRuntimeStatus{}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	status := r.ensureStatusLocked(name, command)
	status.Phase = RuntimePhaseFailed
	status.Command = firstNonEmpty(command, status.Command)
	status.LastAttemptAt = time.Now()
	status.ConsecutiveFailures++
	if err != nil {
		status.LastError = err.Error()
	}
	status.NextRetryAt = status.LastAttemptAt.Add(backoffDelay(status.ConsecutiveFailures))
	status.Message = status.Summary()
	r.statuses[name] = status
	return status
}

func (r *Registry) ensureStatusLocked(name string, command string) ClientRuntimeStatus {
	status, ok := r.statuses[name]
	if !ok {
		status = ClientRuntimeStatus{
			Language: name,
			Phase:    RuntimePhaseIdle,
		}
		if info, ok := KnownServerInfoForLanguage(name); ok {
			status.Command = info.Command
			status.InstallCommand = info.InstallCommand
			status.DocsURL = info.DocsURL
		}
	}
	if command != "" {
		status.Command = command
	}
	if status.Message == "" {
		status.Message = status.Summary()
	}
	return status
}

func (r *Registry) updateStatus(name string, update func(ClientRuntimeStatus) ClientRuntimeStatus) {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	status := r.ensureStatusLocked(name, "")
	status = update(status)
	if status.Message == "" {
		status.Message = status.Summary()
	}
	r.statuses[name] = status
}

func backoffDelay(failures int) time.Duration {
	if failures <= 0 {
		return retryBaseDelay
	}

	delay := retryBaseDelay
	for i := 1; i < failures; i++ {
		delay *= 2
		if delay >= retryMaxDelay {
			return retryMaxDelay
		}
	}
	if delay > retryMaxDelay {
		return retryMaxDelay
	}
	return delay
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func KnownOrConfiguredStatus(language string, registry *Registry) (ClientRuntimeStatus, bool) {
	language = strings.ToLower(strings.TrimSpace(language))
	if language == "" {
		return ClientRuntimeStatus{}, false
	}
	if registry != nil {
		if status, ok := registry.Status(language); ok {
			return status, true
		}
	}
	if info, ok := KnownServerInfoForLanguage(language); ok {
		return ClientRuntimeStatus{
			Language:       language,
			Phase:          RuntimePhaseIdle,
			Message:        fmt.Sprintf("%s LSP has not started yet", language),
			Command:        info.Command,
			InstallCommand: info.InstallCommand,
			DocsURL:        info.DocsURL,
		}, true
	}
	return ClientRuntimeStatus{}, false
}

func KnownOrConfiguredStatuses(registry *Registry, configured map[string]bool) []ClientRuntimeStatus {
	ordered := make([]string, 0, len(configured))
	seen := make(map[string]bool, len(configured))
	for language := range configured {
		language = strings.ToLower(strings.TrimSpace(language))
		if language == "" || seen[language] {
			continue
		}
		seen[language] = true
		ordered = append(ordered, language)
	}
	if registry != nil {
		for language := range registry.StatusSnapshot() {
			if seen[language] {
				continue
			}
			seen[language] = true
			ordered = append(ordered, language)
		}
	}
	sort.Strings(ordered)

	statuses := make([]ClientRuntimeStatus, 0, len(ordered))
	for _, language := range ordered {
		if status, ok := KnownOrConfiguredStatus(language, registry); ok {
			statuses = append(statuses, status)
		}
	}
	return statuses
}

func ServerKeyForFile(filePath string) string {
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".ts", ".tsx", ".js", ".jsx", ".mts", ".cts":
		return "typescript"
	case ".rs":
		return "rust"
	default:
		return ""
	}
}

func ServerKeyForLanguage(language string) string {
	switch strings.ToLower(strings.TrimSpace(language)) {
	case "go", "golang":
		return "go"
	case "python", "py":
		return "python"
	case "typescript", "ts", "tsx", "javascript", "js", "jsx", "mts", "cts":
		return "typescript"
	case "rust", "rs":
		return "rust"
	default:
		return ""
	}
}
