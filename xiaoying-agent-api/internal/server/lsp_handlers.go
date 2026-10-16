package server

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/lsp"
)

type LSPStatusResponse struct {
	Servers map[string]LSPServerStatus `json:"servers"`
}

type LSPServerStatus struct {
	Configured          bool   `json:"configured"`
	Installed           bool   `json:"installed"`
	Running             bool   `json:"running"`
	State               string `json:"state,omitempty"`
	Message             string `json:"message,omitempty"`
	LastError           string `json:"last_error,omitempty"`
	ResolvedCommand     string `json:"resolved_command,omitempty"`
	NextRetryAt         string `json:"next_retry_at,omitempty"`
	ConsecutiveFailures int    `json:"consecutive_failures,omitempty"`
	Command             string `json:"command"`
	InstallCommand      string `json:"install_command"`
	DocsURL             string `json:"docs_url"`
}

type LSPStartRequest struct {
	Language string `json:"language"`
}

type LSPStartResponse struct {
	Status   string `json:"status"`
	Language string `json:"language"`
}

func (s *Server) handleLSPStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	cfg := config.Get()
	effectiveLSPConfigs := config.EffectiveLSPConfigs(cfg)
	response := LSPStatusResponse{
		Servers: make(map[string]LSPServerStatus),
	}
	knownServers := lsp.KnownServerInfos()

	getInstallInfo := func(name string) (string, string) {
		if info, ok := knownServers[name]; ok {
			return info.InstallCommand, info.DocsURL
		}
		return "", ""
	}

	// 1. Check Configured Servers
	for name, lspCfg := range effectiveLSPConfigs {
		installCmd, docs := getInstallInfo(name)
		status := LSPServerStatus{
			Configured:     true,
			Command:        lspCfg.Command,
			InstallCommand: installCmd,
			DocsURL:        docs,
		}

		// Check if running
		if client, ok := s.app.LSPRegistry.Get(name); ok {
			status.Running = client.GetServerState() == lsp.StateReady || client.GetServerState() == lsp.StateStarting
		}
		if runtimeStatus, ok := s.app.LSPRegistry.Status(name); ok {
			status.State = string(runtimeStatus.Phase)
			status.Message = runtimeStatus.Message
			status.LastError = runtimeStatus.LastError
			status.ResolvedCommand = runtimeStatus.ResolvedCommand
			status.ConsecutiveFailures = runtimeStatus.ConsecutiveFailures
			if !runtimeStatus.NextRetryAt.IsZero() {
				status.NextRetryAt = runtimeStatus.NextRetryAt.Format(time.RFC3339)
			}
		}

		// Check if installed.
		if lsp.IsServerInstalled(context.Background(), name, lspCfg.Command, lspCfg.Args) {
			status.Installed = true
		}

		response.Servers[name] = status
	}

	// 2. Check Known Servers (if not configured)
	for name, info := range knownServers {
		if _, exists := response.Servers[name]; !exists {
			status := LSPServerStatus{
				Configured:     false,
				Command:        info.Command,
				Running:        false,
				InstallCommand: info.InstallCommand,
				DocsURL:        info.DocsURL,
			}
			if runtimeStatus, ok := s.app.LSPRegistry.Status(name); ok {
				status.State = string(runtimeStatus.Phase)
				status.Message = runtimeStatus.Message
				status.LastError = runtimeStatus.LastError
				status.ResolvedCommand = runtimeStatus.ResolvedCommand
				status.ConsecutiveFailures = runtimeStatus.ConsecutiveFailures
				if !runtimeStatus.NextRetryAt.IsZero() {
					status.NextRetryAt = runtimeStatus.NextRetryAt.Format(time.RFC3339)
				}
			}
			// Check if installed
			if lsp.IsServerInstalled(context.Background(), name, info.Command, nil) {
				status.Installed = true
			}
			response.Servers[name] = status
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleLSPStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req LSPStartRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	language := lsp.ServerKeyForLanguage(req.Language)
	if language == "" {
		http.Error(w, "Unsupported LSP language", http.StatusBadRequest)
		return
	}

	s.app.StartLSPClientsForLanguages(r.Context(), []string{language})

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(LSPStartResponse{
		Status:   "started",
		Language: language,
	})
}
