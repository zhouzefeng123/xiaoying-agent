package server

import (
	"context"
	"crypto/sha1"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/auramate/auramate/internal/app"
	"github.com/auramate/auramate/internal/cognitive"
	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/goword"
	"github.com/auramate/auramate/internal/llm/agent"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/llm/provider"
	"github.com/auramate/auramate/internal/llm/routing"
	"github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/message"
	"github.com/auramate/auramate/internal/permission"
	"github.com/auramate/auramate/internal/utils"
	"github.com/gorilla/websocket"
)

func parsePlanSnapshotMetadata(metadata string) (map[string]any, bool) {
	if strings.TrimSpace(metadata) == "" {
		return nil, false
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(metadata), &payload); err != nil {
		return nil, false
	}
	if payloadType, _ := payload["type"].(string); payloadType != "plan_snapshot" {
		return nil, false
	}
	return payload, true
}

type wordEditorEmission struct {
	signature string
	emittedAt time.Time
}

type Server struct {
	app      *app.App
	server   *http.Server
	upgrader websocket.Upgrader

	gensMutex  sync.Mutex
	activeGens map[string]context.CancelFunc

	clients            map[*websocket.Conn]*sync.Mutex
	clientsMutex       sync.Mutex
	wordEditorMu       sync.Mutex
	wordEditorLastEmit map[string]wordEditorEmission

	pendingNotifications []Event

	watcher *Watcher
	goWord  *goword.Service

	StartTime time.Time
}

func (s *Server) resolveFixedWorkspaceRoot() string {
	if envWD := strings.TrimSpace(os.Getenv("AURAMATE_WORKSPACE")); envWD != "" {
		return envWD
	}

	if s != nil && s.app != nil && s.app.Config != nil {
		if appRoot := strings.TrimSpace(s.app.Config.AppRoot); appRoot != "" {
			return filepath.Join(appRoot, "workspace")
		}
		if projectRoot := strings.TrimSpace(s.app.Config.ProjectRoot); projectRoot != "" {
			return filepath.Join(projectRoot, "workspace")
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	if filepath.Base(cwd) == "backend" {
		cwd = filepath.Dir(cwd)
	}
	return filepath.Join(cwd, "workspace")
}

func sanitizeAttachmentName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "attachment"
	}

	var builder strings.Builder
	lastUnderscore := false
	for _, ch := range trimmed {
		isASCIIAlphaNum := (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9')
		isAllowedPunctuation := ch == '.' || ch == '-' || ch == '_'
		if isASCIIAlphaNum || isAllowedPunctuation {
			builder.WriteRune(ch)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}

	sanitized := strings.Trim(builder.String(), "._ ")
	if sanitized == "" {
		return "attachment"
	}

	return sanitized
}

func stagedAttachmentFileName(batchID int64, idx int, originalName string) string {
	hash := sha1.Sum([]byte(originalName))
	ext := sanitizeAttachmentName(filepath.Ext(originalName))
	if ext != "" && !strings.HasPrefix(ext, ".") {
		ext = "." + ext
	}

	return fmt.Sprintf("%d_%03d_%x%s", batchID, idx, hash[:6], ext)
}

func stringConfigValue(value interface{}) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func isMainModelConfigEnabled(mainModel map[string]interface{}) bool {
	if mainModel == nil {
		return false
	}

	return stringConfigValue(mainModel["model"]) != "" ||
		stringConfigValue(mainModel["apiKey"]) != "" ||
		stringConfigValue(mainModel["baseURL"]) != ""
}

func buildPersistedMainModelConfig(existing map[string]interface{}, provider string, model string, apiKey string, baseURL string, maxTokens int64) (map[string]interface{}, bool) {
	normalizedProvider := strings.TrimSpace(provider)
	normalizedModel := strings.TrimSpace(model)
	normalizedAPIKey := strings.TrimSpace(apiKey)
	normalizedBaseURL := strings.TrimSpace(baseURL)

	if normalizedProvider == "aliyun" {
		normalizedModel = ""
		normalizedAPIKey = ""
		normalizedBaseURL = ""
	}

	mainModel := make(map[string]interface{})
	if normalizedProvider != "" {
		mainModel["provider"] = normalizedProvider
	}
	if normalizedModel != "" {
		mainModel["model"] = normalizedModel
	}
	if normalizedAPIKey != "" {
		mainModel["apiKey"] = normalizedAPIKey
	}
	if normalizedBaseURL != "" {
		mainModel["baseURL"] = normalizedBaseURL
	}

	switch {
	case maxTokens > 0:
		mainModel["maxTokens"] = maxTokens
	case maxTokens == 0 && existing != nil:
		if existingMaxTokens, ok := existing["maxTokens"]; ok {
			mainModel["maxTokens"] = existingMaxTokens
		}
	}

	if !isMainModelConfigEnabled(mainModel) {
		return nil, false
	}

	return mainModel, true
}

func (s *Server) stopSession(sessionID string) {
	logging.Info("Received stop request", "session_id", sessionID)

	s.gensMutex.Lock()
	if cancel, ok := s.activeGens[sessionID]; ok {
		cancel()
		delete(s.activeGens, sessionID)
		logging.Info("Stopped generation", "session_id", sessionID)
	}
	s.gensMutex.Unlock()

	if s.app == nil {
		return
	}

	if s.app.LifeAgent != nil {
		s.app.LifeAgent.Cancel(sessionID)
	}
	if s.app.CoderAgent != nil {
		s.app.CoderAgent.Cancel(sessionID)
	}
	if s.app.NormalAgent != nil {
		s.app.NormalAgent.Cancel(sessionID)
	}
}

func (s *Server) setSessionReadOnlyMode(ctx context.Context, sessionID string, enabled bool) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return fmt.Errorf("session_id is required")
	}
	services := []agent.Service{
		s.app.NormalAgent,
		s.app.NormalAgentNoSubagents,
		s.app.CoderAgent,
		s.app.CoderAgentNoSubagents,
		s.app.LifeAgent,
	}
	var lastErr error
	for _, service := range services {
		if service == nil {
			continue
		}
		if err := service.SetReadOnlyMode(ctx, sessionID, enabled); err != nil {
			lastErr = err
			continue
		}
		logging.Info("Updated session read-only mode", "session_id", sessionID, "enabled", enabled)
		return nil
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("no agent service available")
}

func (s *Server) resolveAttachmentPath(filePath string) string {
	if filePath == "" {
		return ""
	}

	if filepath.IsAbs(filePath) {
		return filepath.Clean(filePath)
	}

	return filepath.Join(s.app.Config.WorkingDir, filePath)
}

func (s *Server) stageAttachmentsForWorkspace(sessionID string, attachments []message.Attachment) []message.Attachment {
	if len(attachments) == 0 {
		return attachments
	}

	stagedDir := filepath.Join(
		s.app.Config.WorkingDir,
		".auramate",
		"attachments",
		sanitizeAttachmentName(sessionID),
	)
	staged := make([]message.Attachment, 0, len(attachments))
	batchID := time.Now().UnixNano()

	for idx, attachment := range attachments {
		resolvedPath := s.resolveAttachmentPath(attachment.FilePath)

		if resolvedPath != "" {
			info, err := os.Stat(resolvedPath)
			if err == nil && info.IsDir() {
				if attachment.FileName == "" {
					attachment.FileName = filepath.Base(resolvedPath)
				}
				attachment.FilePath = resolvedPath
				attachment.MimeType = message.DirectoryAttachmentMimeType
				attachment.Content = nil
				staged = append(staged, attachment)
				logging.Info("Preserving directory attachment", "session_id", sessionID, "path", resolvedPath)
				continue
			}
		}

		if attachment.FileName == "" {
			if resolvedPath != "" {
				attachment.FileName = filepath.Base(resolvedPath)
			} else {
				attachment.FileName = fmt.Sprintf("attachment_%d", idx+1)
			}
		}

		if err := os.MkdirAll(stagedDir, 0755); err != nil {
			logging.Warn("Failed to prepare attachment staging directory", "dir", stagedDir, "error", err)
			continue
		}

		var data []byte
		switch {
		case len(attachment.Content) > 0:
			data = attachment.Content
		case resolvedPath != "":
			content, err := os.ReadFile(resolvedPath)
			if err != nil {
				logging.Warn("Failed to stage attachment from source path", "path", resolvedPath, "error", err)
				continue
			}
			data = content
		default:
			logging.Warn("Skipping attachment without source path or content", "file_name", attachment.FileName)
			continue
		}

		stagedPath := filepath.Join(stagedDir, stagedAttachmentFileName(batchID, idx, attachment.FileName))
		if err := os.WriteFile(stagedPath, data, 0644); err != nil {
			logging.Warn("Failed to write staged attachment", "path", stagedPath, "error", err)
			continue
		}

		attachment.FilePath = stagedPath
		attachment.Content = data
		staged = append(staged, attachment)

		logging.Info(
			"Staged attachment into workspace",
			"session_id", sessionID,
			"source_path", resolvedPath,
			"staged_path", stagedPath,
		)
	}

	return staged
}

func New(app *app.App, port int) *Server {
	s := &Server{
		app: app,
		server: &http.Server{
			Addr:              fmt.Sprintf(":%d", port),
			ReadHeaderTimeout: 10 * time.Second,
		},
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true // Allow all origins for local development (Sidecar mode)
			},
		},
		activeGens:         make(map[string]context.CancelFunc),
		clients:            make(map[*websocket.Conn]*sync.Mutex),
		wordEditorLastEmit: make(map[string]wordEditorEmission),
		goWord:             app.GoWord,
		StartTime:          time.Now(),
	}

	// Initialize File Watcher
	watcher, err := NewWatcher(s)
	if err != nil {
		logging.Error("Failed to initialize file watcher", "error", err)
	} else {
		s.watcher = watcher
		s.watcher.Start()
		logging.Info("File watcher started", "path", app.Config.WorkingDir)
	}

	// Set Cognitive Engine Notification Callback
	app.Cognitive.SetNotificationCallback(func(title, content, type_ string) {
		s.sendOrQueueNotification(title, content, type_)
	})

	// Set Cognitive Engine Thought Callback
	app.Cognitive.SetThoughtCallback(func(type_, content string) {
		event := Event{
			Type: "thought",
			Data: map[string]string{
				"type":    type_, // "instinct", "thought", "action", "observation"
				"content": content,
				"source":  "life", // Explicitly mark as life agent
			},
		}
		s.Broadcast(event)
	})

	return s
}

// Broadcast sends an event to all connected clients and returns the number of successful sends
func (s *Server) Broadcast(event Event) int {
	s.clientsMutex.Lock()
	defer s.clientsMutex.Unlock()

	sentCount := 0
	for client, mu := range s.clients {
		mu.Lock()
		err := client.WriteJSON(event)
		mu.Unlock()

		if err != nil {
			logging.Warn("Failed to broadcast to client", "error", err)
			client.Close()
			delete(s.clients, client)
		} else {
			sentCount++
		}
	}
	return sentCount
}

func (s *Server) sendOrQueueNotification(title, content, type_ string) {
	normalizedTitle := strings.TrimSpace(title)
	normalizedContent := strings.TrimSpace(content)

	// Drop empty notifications to prevent blank client popups.
	if normalizedTitle == "" && normalizedContent == "" {
		logging.Debug("Skipping empty notification payload")
		return
	}

	if normalizedTitle == "" {
		normalizedTitle = "通知"
	}

	event := Event{
		Type: EventTypeNotification,
		Data: map[string]string{
			"title":   normalizedTitle,
			"content": normalizedContent,
			"type":    type_,
		},
	}

	sentCount := s.Broadcast(event)
	if sentCount == 0 {
		s.clientsMutex.Lock()
		s.pendingNotifications = append(s.pendingNotifications, event)
		s.clientsMutex.Unlock()
	}
}

// Notify implements the scheduler.Notifier interface
func (s *Server) Notify(title, content string) error {
	s.sendOrQueueNotification(title, content, "info")
	return nil
}

func processThinkingTaggedToken(pendingBuffer *string, isThinking *bool, token string, onThought func(string)) bool {
	if token == "" {
		return false
	}

	*pendingBuffer += token
	hasAnswer := false

	for len(*pendingBuffer) > 0 {
		if *isThinking {
			idx := strings.Index(*pendingBuffer, "</thinking>")
			thinkIdx := strings.Index(*pendingBuffer, "</think>")
			if idx == -1 {
				idx = thinkIdx
			} else if thinkIdx != -1 && thinkIdx < idx {
				idx = thinkIdx
			}

			if idx != -1 {
				thoughtContent := (*pendingBuffer)[:idx]
				if thoughtContent != "" && onThought != nil {
					onThought(thoughtContent)
				}
				*isThinking = false
				if strings.HasPrefix((*pendingBuffer)[idx:], "</thinking>") {
					*pendingBuffer = (*pendingBuffer)[idx+len("</thinking>"):]
				} else {
					*pendingBuffer = (*pendingBuffer)[idx+len("</think>"):]
				}
				continue
			}

			partialMatch := false
			for _, tag := range []string{"</thinking>", "</think>"} {
				for i := 1; i < len(tag); i++ {
					if strings.HasSuffix(*pendingBuffer, tag[:i]) {
						partialMatch = true
						break
					}
				}
				if partialMatch {
					break
				}
			}
			if partialMatch {
				break
			}
			if onThought != nil {
				onThought(*pendingBuffer)
			}
			*pendingBuffer = ""
			continue
		}

		idx := strings.Index(*pendingBuffer, "<thinking>")
		thinkIdx := strings.Index(*pendingBuffer, "<think>")
		if idx == -1 {
			idx = thinkIdx
		} else if thinkIdx != -1 && thinkIdx < idx {
			idx = thinkIdx
		}

		if idx != -1 {
			if strings.TrimSpace((*pendingBuffer)[:idx]) != "" {
				hasAnswer = true
			}
			*isThinking = true
			if strings.HasPrefix((*pendingBuffer)[idx:], "<thinking>") {
				*pendingBuffer = (*pendingBuffer)[idx+len("<thinking>"):]
			} else {
				*pendingBuffer = (*pendingBuffer)[idx+len("<think>"):]
			}
			continue
		}

		partialMatch := false
		for _, tag := range []string{"<thinking>", "<think>"} {
			for i := 1; i < len(tag); i++ {
				if strings.HasSuffix(*pendingBuffer, tag[:i]) {
					partialMatch = true
					break
				}
			}
			if partialMatch {
				break
			}
		}
		if partialMatch {
			break
		}
		if strings.TrimSpace(*pendingBuffer) != "" {
			hasAnswer = true
		}
		*pendingBuffer = ""
	}

	return hasAnswer
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/ws", s.handleWebSocket)
	mux.HandleFunc("/v1/models", s.handleModels)
	mux.HandleFunc("/v1/debug/agent", s.handleDebugAgent)

	mux.HandleFunc("/v1/transcribe", s.handleTranscribe)
	mux.HandleFunc("/v1/stt/local-sensevoice/status", s.handleLocalSenseVoiceStatus)
	mux.HandleFunc("/v1/speech", s.handleGenerateSpeech)
	mux.HandleFunc("/v1/memories", s.handleMemories)
	mux.HandleFunc("/v1/memories/dedup", s.handleMemoryDedup)
	mux.HandleFunc("/v1/memories/graph", s.handleMemoryGraph) // New Graph Endpoint
	mux.HandleFunc("/v1/memories/", s.handleMemoryItem)       // For /v1/memories/{id}
	mux.HandleFunc("/v1/news", s.handleNews)
	mux.HandleFunc("/v1/tasks", s.handleTasks)
	mux.HandleFunc("/v1/tasks/", s.handleTaskItem) // For /v1/tasks/{id}
	mux.HandleFunc("/v1/sessions", s.handleSessions)
	mux.HandleFunc("/v1/sessions/", s.handleSessionItem) // For /v1/sessions/{id} and /v1/sessions/{id}/summary
	mux.HandleFunc("/v1/skills", s.handleSkills)
	mux.HandleFunc("/v1/skills/", s.handleSkillItem) // For DELETE /v1/skills/{name}
	mux.HandleFunc("/v1/skills/refresh", s.handleRefreshSkills)

	// Swarm API
	mux.HandleFunc("/v1/swarm/spawn", s.handleSwarmSpawn)
	mux.HandleFunc("/v1/swarm/message", s.handleSwarmMessage)
	mux.HandleFunc("/v1/swarm/broadcast", s.handleSwarmBroadcast) // Added broadcast endpoint
	mux.HandleFunc("/v1/swarm/events", s.handleSwarmEvents)
	mux.HandleFunc("/v1/swarm/execute", s.handleSwarmExecute) // New endpoint for high-level execution
	mux.HandleFunc("/v1/swarm/snapshots", s.handleSwarmSnapshots)

	mux.HandleFunc("/v1/stats/tokens", s.handleTokenStats)
	mux.HandleFunc("/v1/system/open", s.handleSystemOpen)
	mux.HandleFunc("/v1/system/cwd", s.handleSystemCwd)
	mux.HandleFunc("/v1/upload", s.handleUpload) // Handle file uploads
	mux.HandleFunc("/v1/files", s.handleFiles)   // Base endpoint
	mux.HandleFunc("/v1/files/", s.handleFiles)  // For sub-resources like /v1/files/status
	mux.HandleFunc("/v1/goword/create", s.handleGoWordCreate)
	mux.HandleFunc("/v1/goword/open", s.handleGoWordOpen)
	mux.HandleFunc("/v1/goword/document", s.handleGoWordDocument)
	mux.HandleFunc("/v1/goword/apply", s.handleGoWordApply)
	mux.HandleFunc("/v1/goword/diff", s.handleGoWordDiff)
	mux.HandleFunc("/v1/goword/save", s.handleGoWordSave)
	mux.HandleFunc("/v1/goword/close", s.handleGoWordClose)
	mux.HandleFunc("/v1/goword/image", s.handleGoWordImage)
	mux.HandleFunc("/v1/goword/search", s.handleGoWordSearch)
	mux.HandleFunc("/v1/goword/layout", s.handleGoWordLayout)
	mux.HandleFunc("/v1/goword/ir", s.handleGoWordIR)
	mux.HandleFunc("/v1/goword/ir/query", s.handleGoWordIRQuery)
	mux.HandleFunc("/v1/goword/plan", s.handleGoWordPlan)
	mux.HandleFunc("/v1/goword/execute_plan", s.handleGoWordExecutePlan)
	mux.HandleFunc("/v1/goword/checkpoint", s.handleGoWordSaveCheckpoint)
	mux.HandleFunc("/v1/goword/checkpoints", s.handleGoWordListCheckpoints)
	mux.HandleFunc("/v1/goword/checkpoint/restore", s.handleGoWordRestoreCheckpoint)
	mux.HandleFunc("/v1/goword/batch/plan", s.handleGoWordPlanBatch)
	mux.HandleFunc("/v1/goword/batch/execute", s.handleGoWordExecuteBatch)
	mux.HandleFunc("/v1/goword/validate", s.handleGoWordValidate)
	mux.HandleFunc("/v1/config", s.handleConfig)        // New endpoint for getting/updating config
	mux.HandleFunc("/v1/lsp/status", s.handleLSPStatus) // New endpoint for LSP status
	mux.HandleFunc("/v1/lsp/start", s.handleLSPStart)
	mux.HandleFunc("/v1/chat/completions", s.handleChatCompletions)

	// Aura Auth API
	mux.HandleFunc("/v1/auth/send-email-code", s.handleSendEmailCode)
	mux.HandleFunc("/v1/auth/login", s.handleLogin)
	mux.HandleFunc("/v1/user/current", s.handleCurrent)

	// Cognitive Engine APIs
	mux.HandleFunc("/v1/cognitive/state", s.handleCognitiveState)
	mux.HandleFunc("/v1/cognitive/feed", s.handleCognitiveFeed)
	mux.HandleFunc("/v1/cognitive/switch", s.handleCognitiveSwitch)
	mux.HandleFunc("/v1/settings/persona", s.handleSettingsPersona)

	// Static file serving for workspace (images, etc.)
	workspaceDir := s.app.Config.WorkingDir
	if _, err := os.Stat(workspaceDir); os.IsNotExist(err) {
		os.MkdirAll(workspaceDir, 0755)
	}
	mux.Handle("/workspace/", http.StripPrefix("/workspace/", http.FileServer(http.Dir(workspaceDir))))

	// Health check
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	s.server.Handler = mux

	logging.Info("Starting server on " + s.server.Addr)

	// Add CORS middleware
	handler := s.corsMiddleware(mux)
	s.server.Handler = handler

	if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) handleConfig(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	getConfigPath := func() string {
		return config.MainConfigPath()
	}

	if r.Method == "GET" {
		configFile := getConfigPath()
		data, err := os.ReadFile(configFile)
		if err != nil {
			logging.Error("Failed to read config file", "error", err)
			http.Error(w, "Failed to read config file", http.StatusInternalServerError)
			return
		}

		var configMap map[string]interface{}
		if err := json.Unmarshal(data, &configMap); err != nil {
			logging.Error("Failed to parse config file", "error", err)
			http.Error(w, "Failed to parse config file", http.StatusInternalServerError)
			return
		}

		// Extract only necessary info to return (avoid exposing everything if not needed)
		// For now, return provider keys
		resp := make(map[string]interface{})
		if providers, ok := configMap["providers"].(map[string]interface{}); ok {
			if aliyun, ok := providers["aliyun"].(map[string]interface{}); ok {
				resp["aliyun_api_key"] = aliyun["apiKey"]
			}
		}

		// Return Main Model Config
		if mainModel, ok := configMap["main_model"].(map[string]interface{}); ok && isMainModelConfigEnabled(mainModel) {
			resp["main_model"] = mainModel
		}

		// Return Agents Config
		if agents, ok := configMap["agents"].(map[string]interface{}); ok {
			resp["agents"] = agents
		}

		// Return Custom Models Config
		if models, ok := configMap["models"].(map[string]interface{}); ok {
			resp["models"] = models
		}

		// Return MCP Servers Config from dedicated MCP file
		if err := config.ReloadMCPServers(); err != nil {
			logging.Warn("failed to reload dedicated MCP config", "error", err)
		}
		mcpMap := make(map[string]interface{})
		for id, s := range config.Get().MCPServers {
			serverCfg := map[string]interface{}{
				"type":     s.Type,
				"command":  s.Command,
				"args":     s.Args,
				"env":      s.Env,
				"url":      s.URL,
				"headers":  s.Headers,
				"disabled": s.Disabled,
			}
			mcpMap[id] = serverCfg
		}
		resp["mcpServers"] = mcpMap

		json.NewEncoder(w).Encode(resp)
		return
	}

	if r.Method == "POST" {
		var req struct {
			Provider string `json:"provider"`
			APIKey   string `json:"api_key"`
			// Optional: Update Main Model Config
			MainModel *struct {
				Provider  string `json:"provider"`
				Model     string `json:"model"`
				APIKey    string `json:"api_key"`
				BaseURL   string `json:"base_url"`
				MaxTokens int64  `json:"max_tokens"`
			} `json:"main_model"`
			// Optional: Update Agents Config
			Agents map[string]struct {
				Model string `json:"model"`
			} `json:"agents"`
			// Optional: Update Custom Models
			Models map[string]struct {
				ID       string `json:"id"`
				Name     string `json:"name"`
				Provider string `json:"provider"`
				APIModel string `json:"api_model"`
				APIKey   string `json:"api_key"`
				BaseURL  string `json:"base_url"`
			} `json:"models"`
			// Optional: Update MCP Servers
			McpServers map[string]struct {
				Type     string            `json:"type"`
				Command  string            `json:"command,omitempty"`
				Args     []string          `json:"args,omitempty"`
				Env      map[string]string `json:"env,omitempty"` // Changed from []string to map[string]string
				URL      string            `json:"url,omitempty"`
				Headers  map[string]string `json:"headers,omitempty"`
				Disabled bool              `json:"disabled,omitempty"`
			} `json:"mcpServers"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		configFile := getConfigPath()
		data, err := os.ReadFile(configFile)
		if err != nil {
			logging.Error("Failed to read config file", "error", err)
			http.Error(w, "Failed to read config file", http.StatusInternalServerError)
			return
		}

		var configMap map[string]interface{}
		if err := json.Unmarshal(data, &configMap); err != nil {
			logging.Error("Failed to parse config file", "error", err)
			http.Error(w, "Failed to parse config file", http.StatusInternalServerError)
			return
		}

		shouldRestart := false

		// Update Provider API Key if provided
		if req.Provider != "" {
			shouldRestart = true
			providers, ok := configMap["providers"].(map[string]interface{})
			if !ok {
				providers = make(map[string]interface{})
				configMap["providers"] = providers
			}

			providerConfig, ok := providers[req.Provider].(map[string]interface{})
			if !ok {
				providerConfig = make(map[string]interface{})
				providers[req.Provider] = providerConfig
			}
			providerConfig["apiKey"] = req.APIKey
		}

		delete(configMap, "embeddings")

		// Update Main Model Config if provided
		if req.MainModel != nil {
			shouldRestart = true
			var existingMainModel map[string]interface{}
			if existing, ok := configMap["main_model"].(map[string]interface{}); ok {
				existingMainModel = existing
			}

			mainModel, enabled := buildPersistedMainModelConfig(existingMainModel, req.MainModel.Provider, req.MainModel.Model, req.MainModel.APIKey, req.MainModel.BaseURL, req.MainModel.MaxTokens)
			if enabled {
				configMap["main_model"] = mainModel
			} else {
				delete(configMap, "main_model")
			}
		}

		// Update Agents Config if provided
		if req.Agents != nil {
			shouldRestart = true
			agents, ok := configMap["agents"].(map[string]interface{})
			if !ok {
				agents = make(map[string]interface{})
				configMap["agents"] = agents
			}
			for name, agentCfg := range req.Agents {
				if existingAgent, ok := agents[name].(map[string]interface{}); ok {
					existingAgent["model"] = agentCfg.Model
				} else {
					agents[name] = map[string]interface{}{
						"model": agentCfg.Model,
					}
				}
			}
		}

		// Update Custom Models if provided
		if req.Models != nil {
			shouldRestart = true
			// Replace entire models map or merge?
			// Frontend sends full state usually. Let's replace to allow deletions.
			// But we need to convert struct to map[string]interface{}
			modelsMap := make(map[string]interface{})
			for id, m := range req.Models {
				modelsMap[id] = map[string]interface{}{
					"id":        m.ID,
					"name":      m.Name,
					"provider":  m.Provider,
					"api_model": m.APIModel,
					"api_key":   m.APIKey,
					"base_url":  m.BaseURL,
				}
			}
			configMap["models"] = modelsMap
		}

		// Update MCP Servers in dedicated MCP config file if provided
		if req.McpServers != nil {
			newMCP := make(map[string]config.MCPServer)
			for id, s := range req.McpServers {
				cfg := config.MCPServer{
					Type:     config.MCPType(s.Type),
					Command:  s.Command,
					Args:     s.Args,
					Env:      s.Env,
					URL:      s.URL,
					Headers:  s.Headers,
					Disabled: s.Disabled,
				}
				if cfg.Type == "" {
					cfg.Type = config.MCPStdio
				}
				newMCP[id] = cfg
			}
			if err := config.SaveMCPServers(newMCP); err != nil {
				logging.Error("Failed to save dedicated MCP config", "error", err)
				http.Error(w, "Failed to save MCP config", http.StatusInternalServerError)
				return
			}
		}

		// Save back to file
		updatedData, err := json.MarshalIndent(configMap, "", "  ")
		if err != nil {
			logging.Error("Failed to marshal config", "error", err)
			http.Error(w, "Failed to marshal config", http.StatusInternalServerError)
			return
		}

		if err := os.WriteFile(configFile, updatedData, 0644); err != nil {
			logging.Error("Failed to write config file", "error", err)
			http.Error(w, "Failed to write config file", http.StatusInternalServerError)
			return
		}

		if shouldRestart {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]string{"status": "success", "needs_restart": "true", "message": "Config updated. Restart required."})
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "success", "message": "MCP config updated (hot-reloaded)."})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleSettingsPersona(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	key := "agent_persona_life"

	if r.Method == "GET" {
		state, err := s.app.Queries.GetCognitiveState(r.Context(), key)
		if err != nil {
			if err == sql.ErrNoRows {
				// Return default if not set
				json.NewEncoder(w).Encode(map[string]string{"persona": ""})
				return
			}
			http.Error(w, "Database error: "+err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"persona": state.Value.String})
		return
	}

	if r.Method == "POST" {
		var req struct {
			Persona string `json:"persona"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		params := db.SetCognitiveStateParams{
			Key:       key,
			Value:     sql.NullString{String: req.Persona, Valid: true},
			UpdatedAt: time.Now().Unix(),
		}

		_, err := s.app.Queries.SetCognitiveState(r.Context(), params)
		if err != nil {
			http.Error(w, "Failed to save persona: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form (50MB limit)
	if err := r.ParseMultipartForm(50 << 20); err != nil {
		http.Error(w, "Failed to parse form: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "File is required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Create workspace/images directory
	imagesDir := filepath.Join(s.app.Config.WorkingDir, "images")
	if err := os.MkdirAll(imagesDir, 0755); err != nil {
		http.Error(w, "Failed to create directory: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Generate safe filename (UUID or timestamp + original name)
	// Simple approach: timestamp_name
	ext := filepath.Ext(header.Filename)
	name := strings.TrimSuffix(header.Filename, ext)
	filename := fmt.Sprintf("%s_%d%s", name, time.Now().Unix(), ext)
	filename = strings.ReplaceAll(filename, " ", "_") // Basic sanitation

	targetPath := filepath.Join(imagesDir, filename)

	// Create file
	dst, err := os.Create(targetPath)
	if err != nil {
		http.Error(w, "Failed to create file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer dst.Close()

	// Copy content
	if _, err := io.Copy(dst, file); err != nil {
		http.Error(w, "Failed to save file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	// Return path relative to working dir
	relPath := filepath.Join("workspace", "images", filename)
	// Windows path separator handling
	relPath = filepath.ToSlash(relPath)

	// URL for frontend display
	url := fmt.Sprintf("http://%s/%s", r.Host, relPath)

	response := map[string]interface{}{
		"path": relPath,
		"url":  url,
	}

	json.NewEncoder(w).Encode(response)
}

func (s *Server) handleNews(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "GET" {
		ctx := r.Context()
		limit := int32(20)

		news, err := s.app.Queries.ListSavedNews(ctx, limit)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(news)
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleTasks(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "GET" {
		ctx := r.Context()
		tasks, err := s.app.Queries.ListScheduledTasks(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(tasks)
		return
	}

	if r.Method == "POST" {
		// Create new task
		var req struct {
			TaskType           string `json:"task_type"`
			ScheduleExpression string `json:"schedule_expression"`
			Parameters         string `json:"parameters"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "Invalid request body", http.StatusBadRequest)
			return
		}

		if req.TaskType == "" {
			http.Error(w, "task_type is required", http.StatusBadRequest)
			return
		}

		ctx := r.Context()
		now := time.Now().Unix()

		// Calculate next run time based on schedule expression
		var nextRunTime time.Time
		if req.ScheduleExpression != "" {
			// Parse "daily:HH:MM" format
			var hour, minute int
			if n, err := fmt.Sscanf(req.ScheduleExpression, "daily:%d:%d", &hour, &minute); err == nil && n == 2 {
				// Daily schedule
				nextRunTime = time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), hour, minute, 0, 0, time.Now().Location())
				if !nextRunTime.After(time.Now()) {
					nextRunTime = nextRunTime.Add(24 * time.Hour)
				}
			} else if duration, err := time.ParseDuration(req.ScheduleExpression); err == nil {
				// Duration-based schedule (e.g., "1h", "30m")
				nextRunTime = time.Now().Add(duration)
			} else {
				// Fallback: 1 hour
				nextRunTime = time.Now().Add(time.Hour)
			}
		} else {
			// One-time task: run immediately
			nextRunTime = time.Now()
		}

		// Validate task type
		validTypes := map[string]bool{
			"general_agent":    true,
			"general_reminder": true,
		}
		if !validTypes[req.TaskType] {
			http.Error(w, fmt.Sprintf("Invalid task_type: %s. Supported: general_agent, general_reminder", req.TaskType), http.StatusBadRequest)
			return
		}

		params := db.CreateScheduledTaskParams{
			ID:                 fmt.Sprintf("task_%d", now),
			TaskType:           req.TaskType,
			Parameters:         req.Parameters,
			ScheduleExpression: sql.NullString{String: req.ScheduleExpression, Valid: req.ScheduleExpression != ""},
			LastRunAt:          sql.NullInt64{Int64: 0, Valid: false},
			NextRunAt:          sql.NullInt64{Int64: nextRunTime.Unix(), Valid: true},
			IsActive:           sql.NullBool{Bool: true, Valid: true},
			CreatedAt:          now,
		}

		task, err := s.app.Queries.CreateScheduledTask(ctx, params)
		if err != nil {
			http.Error(w, fmt.Sprintf("Failed to create task: %v", err), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(task)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleTaskItem(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	path := strings.TrimPrefix(r.URL.Path, "/v1/tasks/")
	if path == "" {
		http.Error(w, "ID required", http.StatusBadRequest)
		return
	}
	id := path
	ctx := r.Context()

	if r.Method == "DELETE" {
		err := s.app.Queries.DeleteScheduledTask(ctx, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
		return
	}

	if r.Method == "POST" {
		// Used for toggle (active/inactive) or rescheduling
		// Simple toggle for now: ?action=toggle
		action := r.URL.Query().Get("action")
		if action == "toggle" {
			// Get current status
			task, err := s.app.Queries.GetScheduledTask(ctx, id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}

			newState := true
			if task.IsActive.Valid && task.IsActive.Bool {
				newState = false
			}

			// Update
			// We need full params for UpdateScheduledTask
			params := db.UpdateScheduledTaskParams{
				ID:                 task.ID,
				TaskType:           task.TaskType,
				Parameters:         task.Parameters,
				ScheduleExpression: task.ScheduleExpression,
				IsActive:           sql.NullBool{Bool: newState, Valid: true},
				LastRunAt:          task.LastRunAt,
				NextRunAt:          task.NextRunAt,
			}
			// If re-enabling, recalculate NextRunAt?
			// Scheduler usually handles "missed" runs or calculates next run on startup/check.
			// But setting it explicitly is safer if it was null.
			// For simplicity, we just toggle IsActive. Scheduler should pick it up if NextRunAt is valid.
			// If NextRunAt is in the past, scheduler will run it immediately (catch up) or we should reset it.

			_, err = s.app.Queries.UpdateScheduledTask(ctx, params)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
			return
		} else if action == "update" {
			// Update schedule or parameters
			var req struct {
				ScheduleExpression string `json:"schedule_expression"`
				Parameters         string `json:"parameters"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, "Invalid request body", http.StatusBadRequest)
				return
			}

			task, err := s.app.Queries.GetScheduledTask(ctx, id)
			if err != nil {
				http.Error(w, err.Error(), http.StatusNotFound)
				return
			}

			params := db.UpdateScheduledTaskParams{
				ID:                 task.ID,
				TaskType:           task.TaskType,
				Parameters:         task.Parameters,
				ScheduleExpression: task.ScheduleExpression,
				IsActive:           task.IsActive,
				LastRunAt:          task.LastRunAt,
				NextRunAt:          task.NextRunAt,
			}

			if req.ScheduleExpression != "" {
				params.ScheduleExpression = sql.NullString{String: req.ScheduleExpression, Valid: true}
				// Recalculate NextRunAt based on new schedule
				var nextRunTime time.Time
				var hour, minute int
				if n, err := fmt.Sscanf(req.ScheduleExpression, "daily:%d:%d", &hour, &minute); err == nil && n == 2 {
					// Daily schedule
					nextRunTime = time.Date(time.Now().Year(), time.Now().Month(), time.Now().Day(), hour, minute, 0, 0, time.Now().Location())
					if !nextRunTime.After(time.Now()) {
						nextRunTime = nextRunTime.Add(24 * time.Hour)
					}
				} else if duration, err := time.ParseDuration(req.ScheduleExpression); err == nil {
					// Duration-based schedule
					nextRunTime = time.Now().Add(duration)
				} else {
					// Fallback: 1 hour
					nextRunTime = time.Now().Add(time.Hour)
				}
				params.NextRunAt = sql.NullInt64{Int64: nextRunTime.Unix(), Valid: true}
			}
			if req.Parameters != "" {
				params.Parameters = req.Parameters
			}

			_, err = s.app.Queries.UpdateScheduledTask(ctx, params)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"status":"ok"}`))
			return
		}
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleSkills(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "GET" {
		skills := s.app.Skills.ListAvailableTools()
		json.NewEncoder(w).Encode(skills)
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleRefreshSkills(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "POST" {
		s.app.Skills.Refresh()

		skills := s.app.Skills.ListAvailableTools()
		json.NewEncoder(w).Encode(skills)
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleTokenStats(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "GET" {
		ctx := r.Context()

		stats, err := s.app.Queries.GetTokenStats(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		input := stats.InputTokens
		output := stats.OutputTokens
		total := input + output

		json.NewEncoder(w).Encode(map[string]int64{
			"input_tokens":  input,
			"output_tokens": output,
			"total_tokens":  total,
		})
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) Shutdown(ctx context.Context) error {
	if err := s.server.Shutdown(ctx); err != nil {
		return err
	}
	if s.watcher != nil {
		s.watcher.Close()
	}
	return nil
}

func (s *Server) handleModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	// Convert map to list for frontend
	modelList := make([]models.Model, 0, len(models.SupportedModels))
	for _, m := range models.SupportedModels {
		modelList = append(modelList, m)
	}
	json.NewEncoder(w).Encode(modelList)
}

func (s *Server) handleSystemOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Determine the path to open
	var path string
	if req.Path == "workspace" {
		// Always resolve the fixed workspace root instead of the mutable current WorkingDir.
		path = s.resolveFixedWorkspaceRoot()
		if path == "" {
			var err error
			path, err = os.Getwd()
			if err != nil {
				http.Error(w, "Failed to determine working directory", http.StatusInternalServerError)
				return
			}
		}
		if err := os.MkdirAll(path, 0o755); err != nil {
			http.Error(w, fmt.Sprintf("Failed to prepare workspace path: %v", err), http.StatusInternalServerError)
			return
		}
	} else {
		// Absolute path or relative to CWD
		path = req.Path
		if !filepath.IsAbs(path) {
			cwd, _ := os.Getwd()
			path = filepath.Join(cwd, path)
		}
	}

	logging.Info("Opening system path", "path", path)

	// Use 'start' command on Windows, 'open' on Mac, 'xdg-open' on Linux
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", path)
	case "darwin":
		cmd = exec.Command("open", path)
	default:
		cmd = exec.Command("xdg-open", path)
	}

	if err := cmd.Start(); err != nil {
		logging.Error("Failed to open path", "path", path, "error", err)
		http.Error(w, fmt.Sprintf("Failed to open path: %v", err), http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (s *Server) handleSystemCwd(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"path":           s.app.Config.WorkingDir,
			"workspace_path": s.resolveFixedWorkspaceRoot(),
		})
		return
	}

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Path string `json:"path"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if req.Path == "" {
		http.Error(w, "Path is required", http.StatusBadRequest)
		return
	}

	// Update working directory in config
	s.app.Config.WorkingDir = req.Path
	logging.Info("Working directory updated via API", "path", req.Path)

	// Update watcher if active
	if s.watcher != nil {
		s.watcher.UpdatePath(req.Path)
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(map[string]string{
		"status":  "success",
		"path":    req.Path,
		"message": "Working directory updated",
	})
}

func (s *Server) handleMemories(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "GET" {
		ctx := r.Context()
		// Parse query params if needed, e.g. ?category=fact
		category := r.URL.Query().Get("category")
		query := r.URL.Query().Get("q")

		var memories []db.Memory
		var err error

		if query != "" {
			memories, err = s.app.Memories.Search(ctx, query, 50)
		} else if category != "" {
			memories, err = s.app.Memories.ListByCategory(ctx, category)
		} else {
			memories, err = s.app.Memories.List(ctx)
		}

		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		if query != "" || category == "" {
			memories = filterVisibleMemoriesForDefaultView(memories)
		}

		json.NewEncoder(w).Encode(memories)
		return
	}

	if r.Method == "POST" {
		var req struct {
			Category string `json:"category"`
			Content  string `json:"content"`
			Source   string `json:"source"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.Source == "" {
			req.Source = "user_input"
		}

		ctx := r.Context()
		m, err := s.app.Memories.Add(ctx, req.Category, req.Content, req.Source)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(m)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func isHiddenMemoryCategoryForDefaultView(category string) bool {
	switch strings.ToLower(strings.TrimSpace(category)) {
	case "todo_snapshot", "plan_snapshot", "session_summary":
		return true
	default:
		return false
	}
}

func filterVisibleMemoriesForDefaultView(memories []db.Memory) []db.Memory {
	filtered := make([]db.Memory, 0, len(memories))
	for _, memory := range memories {
		if isHiddenMemoryCategoryForDefaultView(memory.Category) {
			continue
		}
		filtered = append(filtered, memory)
	}
	return filtered
}

func (s *Server) handleMemoryDedup(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Threshold float32 `json:"threshold"`
		DryRun    bool    `json:"dry_run"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if req.Threshold <= 0 {
		req.Threshold = 0.92
	}

	stats, err := s.app.Memories.Deduplicate(r.Context(), req.Threshold, req.DryRun)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	json.NewEncoder(w).Encode(stats)
}

func (s *Server) handleMemoryGraph(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method == "GET" {
		ctx := r.Context()

		// 1. Get all memories (Nodes)
		// Assuming List returns all nodes efficiently
		memories, err := s.app.Memories.List(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		memories = filterVisibleMemoriesForDefaultView(memories)

		// 2. Get all edges
		// We need a method to get all edges or iterate.
		// Since we don't have a GetAllEdges method yet, we can iterate memories and GetRelated
		// This is O(N) queries, which is bad for large graphs but okay for MVP (<1000 nodes).
		// Better: Add GetAllEdges to Service/DB.
		// For now, let's try to fetch edges for each node.

		type Edge struct {
			Source string  `json:"source"`
			Target string  `json:"target"`
			Value  float64 `json:"value"`
		}

		var edges []Edge

		// Optimization: Use a map to avoid duplicate edges if undirected, but here edges are directed
		for _, m := range memories {
			related, err := s.app.Memories.GetRelated(ctx, m.ID)
			if err == nil {
				for _, r := range related {
					if isHiddenMemoryCategoryForDefaultView(r.Memory.Category) {
						continue
					}
					edges = append(edges, Edge{
						Source: m.ID,
						Target: r.Memory.ID,
						Value:  r.Weight,
					})
				}
			}
		}

		response := struct {
			Nodes []db.Memory `json:"nodes"`
			Links []Edge      `json:"links"`
		}{
			Nodes: memories,
			Links: edges,
		}

		json.NewEncoder(w).Encode(response)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleMemoryItem(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// Extract ID from path
	// Path is /v1/memories/{id}
	path := strings.TrimPrefix(r.URL.Path, "/v1/memories/")
	if path == "" {
		http.Error(w, "ID required", http.StatusBadRequest)
		return
	}
	id := path

	ctx := r.Context()

	if r.Method == "GET" {
		m, err := s.app.Memories.Get(ctx, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		json.NewEncoder(w).Encode(m)
		return
	}

	if r.Method == "DELETE" {
		err := s.app.Memories.Delete(ctx, id)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"status":"ok"}`))
		return
	}

	if r.Method == "PUT" {
		var req struct {
			Category string `json:"category"`
			Content  string `json:"content"`
			Source   string `json:"source"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		m, err := s.app.Memories.Update(ctx, id, req.Category, req.Content, req.Source)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(m)
		return
	}

	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleDebugAgent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "GET" {
		cfg := s.app.Config

		resp := map[string]interface{}{
			"main_model_config": cfg.MainModel,
			"agents":            map[string]interface{}{},
		}

		targetAgents := []config.AgentName{
			config.AgentCoder,
			config.AgentTask,
			config.AgentSummarizer,
			config.AgentTitle,
			config.AgentLife,
			config.AgentNormal,
		}

		agentsMap := resp["agents"].(map[string]interface{})
		for _, name := range targetAgents {
			if agentCfg, ok := cfg.Agents[name]; ok {
				// Get resolved model info
				modelID := agentCfg.Model
				modelInfo, exists := models.SupportedModels[modelID]

				agentInfo := map[string]interface{}{
					"configured_model_id": modelID,
					"resolved_model":      nil,
				}

				if exists {
					agentInfo["resolved_model"] = map[string]interface{}{
						"id":        modelInfo.ID,
						"provider":  modelInfo.Provider,
						"api_model": modelInfo.APIModel,
						"base_url":  modelInfo.BaseURL,
						// "api_key": "***", // Do not expose full key
					}
				}
				agentsMap[string(name)] = agentInfo
			}
		}

		json.NewEncoder(w).Encode(resp)
		return
	}
	http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		logging.Error("Failed to upgrade websocket", err)
		return
	}
	defer func() {
		s.clientsMutex.Lock()
		delete(s.clients, conn)
		s.clientsMutex.Unlock()
		conn.Close()
	}()

	// Note: We don't use defer conn.Close() here because it's handled in the defer above

	// Mutex to ensure thread-safe writes to websocket
	// Create it here so we can register it with the client
	writeMutex := &sync.Mutex{}

	// Register client
	s.clientsMutex.Lock()
	s.clients[conn] = writeMutex

	// Flush pending notifications
	var pending []Event
	if len(s.pendingNotifications) > 0 {
		pending = make([]Event, len(s.pendingNotifications))
		copy(pending, s.pendingNotifications)
		s.pendingNotifications = nil // Clear pending
	}
	s.clientsMutex.Unlock()

	// Send pending notifications
	// We use the mutex here just to be safe/consistent, though theoretically we are the only writer yet
	// (Broadcast holds clientsMutex, and we held it above, but now we released it)
	// Actually, once we release clientsMutex, Broadcast can find us and write to us!
	// So we MUST use the mutex here if we want to write safely.
	for _, p := range pending {
		writeMutex.Lock()
		if err := conn.WriteJSON(p); err != nil {
			logging.Warn("Failed to send pending notification", "error", err)
		}
		writeMutex.Unlock()
	}

	logging.Info("New websocket connection established")

	writeJSON := func(v interface{}) error {
		writeMutex.Lock()
		defer writeMutex.Unlock()
		return conn.WriteJSON(v)
	}

	// Create a context for this connection
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Subscribe to permission requests
	// Note: pubsub.Subscribe automatically handles unsubscription when context is done
	permEventChan := s.app.Permissions.Subscribe(ctx)

	// Handle permission forwarding in a goroutine
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-permEventChan:
				if !ok {
					return
				}
				perm := event.Payload
				logging.Info("Forwarding permission request to client", "id", perm.ID)
				writeJSON(Event{
					Type: EventTypePermission,
					Data: perm,
				})
			}
		}
	}()

	for {
		var msg ClientMessage
		err := conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				logging.Error("Websocket error", err)
			}
			break
		}

		// Handle Permission Response
		if msg.Type == ClientMessageTypePermission {
			logging.Info("Received permission response", "id", msg.PermissionID, "action", msg.Action)
			permReq := permission.PermissionRequest{ID: msg.PermissionID}
			if msg.Action == "grant" {
				s.app.Permissions.Grant(permReq)
			} else {
				s.app.Permissions.Deny(permReq)
			}
			continue
		}

		// Handle Stop Request
		if msg.Type == ClientMessageTypeStop {
			s.stopSession(msg.SessionID)
			continue
		}

		if msg.Type == ClientMessageTypeReadOnly {
			if msg.ReadOnlyMode == nil {
				writeJSON(Event{Type: EventTypeError, Content: "read_only_mode is required", SessionID: msg.SessionID})
				continue
			}
			if strings.TrimSpace(msg.SessionID) == "" {
				sess, err := s.app.Sessions.Create(ctx, "Read-only session")
				if err != nil {
					logging.Error("Failed to create read-only session", err)
					writeJSON(Event{Type: EventTypeError, Content: "Failed to create session"})
					continue
				}
				msg.SessionID = sess.ID
			}
			if err := s.setSessionReadOnlyMode(ctx, msg.SessionID, *msg.ReadOnlyMode); err != nil {
				writeJSON(Event{Type: EventTypeError, Content: err.Error(), SessionID: msg.SessionID})
				continue
			}
			writeJSON(Event{
				Type:      EventTypeReadOnlyUpdated,
				SessionID: msg.SessionID,
				Data: map[string]any{
					"read_only_mode": *msg.ReadOnlyMode,
				},
			})
			continue
		}

		// Handle Tool Interruption Request (Phase 5)
		if msg.Type == ClientMessageTypeCancelTool {
			logging.Info("Received tool cancel request", "session_id", msg.SessionID, "execution_id", msg.ToolExecutionID)
			// Determine which agent is active for this session
			// Since we don't track agent per session in server yet, we try all of them
			// or we could track it in activeGens.
			// For now, let's just use the LifeAgent as it's the most common for autonomous tools.
			// Actually, we should probably check which agent is running.
			// But CancelTool is session-specific in the agent anyway.
			s.app.LifeAgent.CancelTool(msg.SessionID, msg.ToolExecutionID)
			s.app.CoderAgent.CancelTool(msg.SessionID, msg.ToolExecutionID)
			s.app.NormalAgent.CancelTool(msg.SessionID, msg.ToolExecutionID)
			continue
		}

		// Default to Chat if type is missing or "chat"
		if msg.Type == "" || msg.Type == ClientMessageTypeChat {
			// Sanitize user input for encoding issues
			sanitized := utils.SanitizeUTF8(msg.Content)
			if sanitized.WasModified {
				logging.Info("Sanitized user input encoding", "detected_type", sanitized.DetectedType)
			}
			msg.Content = sanitized.Content

			enableSubagents := msg.EnableSubagents == nil || *msg.EnableSubagents
			logging.Info("Received chat request", "content", msg.Content, "session_id", msg.SessionID, "enable_subagents", enableSubagents)

			// Ensure SessionID
			if msg.SessionID == "" {
				sess, err := s.app.Sessions.Create(ctx, sessionTitleFromUserMessage(msg.Content, "未命名对话"))
				if err != nil {
					logging.Error("Failed to create session", err)
					writeJSON(Event{
						Type:    EventTypeError,
						Content: "Failed to create session",
					})
					continue
				}
				msg.SessionID = sess.ID
			}
			if err := updatePlaceholderSessionTitleFromUserMessage(ctx, s.app.Sessions, msg.SessionID, msg.Content); err != nil {
				logging.Warn("Failed to update placeholder session title", "session_id", msg.SessionID, "error", err)
			}
			// Create cancellable context for this generation
			genCtx, genCancel := context.WithCancel(ctx)

			// Phase 5: Check for message injection
			isInjection := false
			s.gensMutex.Lock()
			if _, ok := s.activeGens[msg.SessionID]; ok {
				// Check if agents are actually busy with this session
				if s.app.LifeAgent.IsSessionBusy(msg.SessionID) ||
					s.app.CoderAgent.IsSessionBusy(msg.SessionID) ||
					s.app.NormalAgent.IsSessionBusy(msg.SessionID) {
					isInjection = true
					logging.Info("Detected active session, proceeding with message injection", "session_id", msg.SessionID)
				}
			}

			if !isInjection {
				if s.app != nil && s.app.Orchestrator != nil {
					s.app.Orchestrator.BeginWorkerSnapshotRound(msg.SessionID)
				}
				if oldCancel, ok := s.activeGens[msg.SessionID]; ok {
					oldCancel()
				}
				s.activeGens[msg.SessionID] = genCancel
			}
			s.gensMutex.Unlock()

			msg.Attachments = s.stageAttachmentsForWorkspace(msg.SessionID, msg.Attachments)

			// Resolve attachments content if path is provided but content is empty
			// This supports the path-only flow where agent reads file by path
			for i, att := range msg.Attachments {
				if message.AttachmentIsDirectory(att) {
					continue
				}
				if len(att.Content) == 0 && att.FilePath != "" {
					// Resolve absolute path
					var absPath string
					if filepath.IsAbs(att.FilePath) {
						// Use absolute path directly (e.g., C:\Users\xxx\file.pdf)
						absPath = att.FilePath
					} else {
						// Resolve relative path against working directory
						absPath = filepath.Join(s.app.Config.WorkingDir, att.FilePath)
					}

					content, err := os.ReadFile(absPath)
					if err == nil {
						msg.Attachments[i].Content = content
						logging.Info("Loaded attachment content from file", "path", absPath, "size", len(content))
					} else {
						logging.Warn("Failed to load attachment content", "path", absPath, "error", err)
					}
				}
			}

			if err := validateRequestedChatModel(msg.Model); err != nil {
				writeJSON(Event{
					Type:      EventTypeError,
					Content:   err.Error(),
					SessionID: msg.SessionID,
					Data: map[string]any{
						"code":  1400,
						"model": msg.Model,
					},
				})
				continue
			}

			// Handle request asynchronously
			go func(m ClientMessage, runCtx context.Context, cancelFunc context.CancelFunc, isInjected bool) {
				defer func() {
					if !isInjected {
						s.gensMutex.Lock()
						// Only delete if it's still our cancel function (not replaced by a newer one)
						if current, ok := s.activeGens[m.SessionID]; ok && fmt.Sprintf("%p", current) == fmt.Sprintf("%p", cancelFunc) {
							delete(s.activeGens, m.SessionID)
						}
						s.gensMutex.Unlock()
					}
					cancelFunc()
				}()

				runCtx = context.WithValue(runCtx, tools.SessionIDContextKey, m.SessionID)

				// Determine which Agent to use based on Cognitive State
				var eventChan <-chan agent.AgentEvent
				var err error

				cognitiveState, _, _, _ := s.app.Cognitive.GetState(runCtx)
				targetAgentName := config.AgentNormal
				targetAgentService := s.selectAgentService(config.AgentNormal, enableSubagents)

				// Determine which Agent to use based on Cognitive State

				if m.Mode == "coding" {
					logging.Info("Using CoderAgent (Coding Mode - Force)", "state", cognitiveState)
					targetAgentName = config.AgentCoder
					targetAgentService = s.selectAgentService(config.AgentCoder, enableSubagents)
				} else if m.Mode == "normal" {
					// Normal Mode: Always use NormalAgent, regardless of cognitive state
					logging.Info("Using NormalAgent (Normal Mode)", "state", cognitiveState)
					targetAgentName = config.AgentNormal
					targetAgentService = s.selectAgentService(config.AgentNormal, enableSubagents)
				} else {
					// Life Mode or Default
					if cognitiveState != string(cognitive.StateDormant) {
						// FIX: If in Standby (Low Power), wake up to Interactive on user input
						if cognitiveState == string(cognitive.StateStandby) {
							logging.Info("Waking up Life Agent from Standby state due to user interaction")
							s.app.Cognitive.TransitionTo(runCtx, string(cognitive.StateInteractive))
							// Update local state to avoid immediate standby checks if any
							cognitiveState = string(cognitive.StateInteractive)
						}

						logging.Info("Using LifeAgent (Life Mode)", "state", cognitiveState)
						targetAgentName = config.AgentLife
						targetAgentService = s.app.LifeAgent
					} else {
						// Default to NormalAgent for "normal" or any other unknown mode
						logging.Info("Using NormalAgent (Normal Mode)", "state", cognitiveState)
						targetAgentName = config.AgentNormal
						targetAgentService = s.selectAgentService(config.AgentNormal, enableSubagents)
					}
				}

				if m.Model != "" {
					logging.Info("Overriding model via unified routing", "session_id", m.SessionID, "model", m.Model, "agent", targetAgentName)
					modelCandidates := routing.ResolveAgentModelCandidates(targetAgentName, m.Model, s.app.Config.Agents)
					if len(modelCandidates) > 0 {
						runCtx = context.WithValue(runCtx, tools.ModelOverrideCandidatesContextKey, modelCandidates)
					}
					if explicitModel := resolveExplicitRequestedChatModel(m.Model); explicitModel != "" {
						runCtx = context.WithValue(runCtx, tools.ModelOverrideContextKey, explicitModel)
					}
				}

				if shouldStartLSPForClientMessage(m) {
					s.app.StartLSPClientsForLanguages(runCtx, inferLSPLanguagesFromClientMessage(m, config.WorkingDirectory()))
				}

				eventChan, err = targetAgentService.Run(runCtx, m.SessionID, m.Content, m.Mode, m.Attachments...)

				if err != nil {
					writeJSON(Event{
						Type:      EventTypeError,
						Content:   err.Error(),
						SessionID: m.SessionID,
					})
					return
				}

				// State for parsing <thinking> tags (Enable for ALL agents to populate Monitor)
				// Previously restricted to Life Agent, but User wants to see thoughts in Normal mode too.
				isThinkingParsingEnabled := true
				var isThinking bool
				var pendingBuffer string
				var hasStreamedAnswerTokens bool
				emittedToolCalls := make(map[string]struct{})
				var lastUsageSnapshot provider.TokenUsage
				var hasUsageSnapshot bool

				// Token Batching State
				var tokenBatchBuffer string
				lastBatchFlush := time.Now()
				const tokenBatchMaxBytes = 4
				const tokenBatchMaxDelay = 20 * time.Millisecond
				toolCallEventKey := func(tc message.ToolCall) string {
					if tc.ID != "" {
						return tc.ID
					}
					return fmt.Sprintf("%s|%s", tc.Name, tc.Input)
				}

				logging.Info("Starting agent execution loop", "mode", m.Mode, "session_id", m.SessionID)

				for agentEvent := range eventChan {
					logging.Info("Received agent event", "type", agentEvent.Type, "content_len", len(agentEvent.Content))
					var eventType EventType
					var content string
					var usage any

					switch agentEvent.Type {
					case agent.AgentEventTypeToken:
						if agentEvent.Content != "" {
							// Keep a single, lightweight server-side batch to avoid websocket flood
							// while still making the stream feel continuous on the client.
							tokenBatchBuffer += agentEvent.Content

							// Flush condition: small chunk OR short delay.
							if len(tokenBatchBuffer) >= tokenBatchMaxBytes || time.Since(lastBatchFlush) >= tokenBatchMaxDelay {
								writeJSON(Event{
									Type:      EventTypeToken,
									Content:   tokenBatchBuffer,
									SessionID: m.SessionID,
								})
								tokenBatchBuffer = ""
								lastBatchFlush = time.Now()
							}
						}

						if isThinkingParsingEnabled {
							if processThinkingTaggedToken(&pendingBuffer, &isThinking, agentEvent.Content, func(thoughtContent string) {
								writeJSON(Event{
									Type: EventTypeThought,
									Data: map[string]string{
										"type":    "thought",
										"content": thoughtContent,
									},
									SessionID: m.SessionID,
								})
							}) {
								hasStreamedAnswerTokens = true
							}
						}
						// Continue to next event (handled above)
						continue

					case agent.AgentEventTypeMessageSplit:
						writeJSON(Event{
							Type:      "split",
							SessionID: m.SessionID,
						})

					case agent.AgentEventTypeNotification:
						eventType = EventTypeNotification // Ensure this constant exists or use string "notification"
						// Map AgentEvent content (JSON) to Server Event content
						content = agentEvent.Content
					case agent.AgentEventTypeUsage:
						// Handle usage event: Consume tokens from Cognitive Bucket
						if agentEvent.Usage != nil {
							currentUsage := *agentEvent.Usage
							deltaUsage := currentUsage
							if hasUsageSnapshot {
								deltaUsage = provider.UsageDelta(lastUsageSnapshot, currentUsage)
							}
							hasUsageSnapshot = true
							lastUsageSnapshot = currentUsage
							total := deltaUsage.TotalTokens()
							if m.Model == "aura-life" || m.Model == "life" {
								if total > 0 {
									if _, err := s.app.Cognitive.ConsumeLifeTokens(runCtx, total); err != nil {
										logging.Error("Failed to consume Life mode tokens", "error", err)
									}
								}
							}

							// Also forward usage to frontend if needed
							eventType = EventTypeUsage // Ensure this exists or use "usage"
							usage = map[string]int64{
								"input_tokens":            currentUsage.InputTokens,
								"output_tokens":           currentUsage.OutputTokens,
								"total_tokens":            currentUsage.TotalTokens(),
								"prompt_cache_hit_tokens": currentUsage.CacheReadTokens,
							}
							usageBytes, _ := json.Marshal(agentEvent.Usage)
							content = string(usageBytes)
						}
					case agent.AgentEventTypeContextStats:
						writeJSON(Event{
							Type:      EventTypeContextStats,
							Data:      agentEvent.Data,
							SessionID: m.SessionID,
						})
						continue
					case agent.AgentEventTypeReadOnlyMode:
						writeJSON(Event{
							Type:      EventTypeReadOnlyUpdated,
							Data:      agentEvent.Data,
							SessionID: m.SessionID,
						})
						continue
					case agent.AgentEventTypeResponse:
						// Only handle Tool Calls in the final response, as content is streamed via tokens
						if len(agentEvent.Message.ToolCalls()) > 0 {
							filteredToolCalls := make([]message.ToolCall, 0, len(agentEvent.Message.ToolCalls()))
							for _, tc := range agentEvent.Message.ToolCalls() {
								key := toolCallEventKey(tc)
								if _, exists := emittedToolCalls[key]; exists {
									continue
								}
								emittedToolCalls[key] = struct{}{}
								filteredToolCalls = append(filteredToolCalls, tc)
							}

							if len(filteredToolCalls) == 0 {
								continue
							}

							eventType = EventTypeToolCall
							// Serialize tool calls to content or data field
							toolCallData, _ := json.Marshal(filteredToolCalls)
							content = string(toolCallData)
						} else {
							// Check if there is reasoning/thought content to broadcast to Monitor
							// But skip if it's the final answer to user (which is streamed via Token events)
							// Usually Response event comes AFTER streaming tokens.

							text := agentEvent.Message.Content().Text

							// Broadcast "thought" event for Monitor if content is not empty
							// This allows the Cognitive Monitor to see the final response text as a "thought" bubble
							if text != "" {
								writeJSON(Event{
									Type: EventTypeThought, // "thought"
									Data: map[string]string{
										"type":    "thought",
										"content": text,
									},
									SessionID: m.SessionID,
								})
							}

							// Check if this is the Final Summary (which is NOT streamed)
							// The Summary is sent as a single Response block with a specific prefix.
							// We must send it as a Token event so it appears in the Chat UI.
							isSummary := strings.Contains(text, "**📝 Final Summary**:")

							// SAFETY FIX: If no user-facing answer tokens were streamed, we MUST send the content now.
							// Thinking/progress tokens do not count as the final answer.
							// This handles non-streaming models or cases where streaming failed.
							// We also send if it is a Summary (explicit non-streamed block).
							if isSummary || !hasStreamedAnswerTokens || agentEvent.ForceSendContent {
								if text != "" {
									eventType = EventTypeToken
									content = text
								} else {
									continue
								}
							} else {
								// Skip standard chat processing as it was already streamed
								continue
							}
						}
					case agent.AgentEventTypeError:
						eventType = EventTypeError
						content = agentEventErrorText(agentEvent)
					case agent.AgentEventTypeToolOutput:
						wordActions, cleanedContent := extractWordEditorSignals(agentEvent.Content)
						if len(wordActions) > 0 {
							for _, wordAction := range wordActions {
								if err := s.broadcastWordEditorAction(m.SessionID, wordAction); err != nil {
									logging.Warn("Blocked invalid word editor action from tool output", "session_id", m.SessionID, "error", err)
									writeJSON(Event{Type: EventTypeError, Content: err.Error(), SessionID: m.SessionID})
								}
							}
						}
						if cleanedContent == "" && len(wordActions) > 0 {
							continue
						}
						eventType = EventTypeToolOutput
						content = cleanedContent
					case agent.AgentEventTypeToolUse:
						eventType = EventTypeToolCall
						// Serialize tool call to content or data field
						// We wrap it in a list to match existing frontend expectation if needed,
						// or just send the single tool call.
						emittedToolCalls[toolCallEventKey(*agentEvent.ToolCall)] = struct{}{}
						toolCalls := []message.ToolCall{*agentEvent.ToolCall}
						toolCallData, _ := json.Marshal(toolCalls)
						content = string(toolCallData)

						// FIX: Broadcast "action" event for Monitor
						writeJSON(Event{
							Type: EventTypeThought, // "thought"
							Data: map[string]string{
								"type":    "action",
								"content": fmt.Sprintf("Calling %s(%s)", agentEvent.ToolCall.Name, agentEvent.ToolCall.Input),
							},
							SessionID: m.SessionID,
						})

						// FIX: Send EventTypeToolCall explicitly to frontend here
						// The original logic relied on falling through to default writeJSON at end of loop.
						// But to be consistent with other blocks, we can send it here and continue.
						writeJSON(Event{
							Type:            EventTypeToolCall,
							Content:         content,
							SessionID:       m.SessionID,
							ToolExecutionID: agentEvent.ToolExecutionID, // Phase 5
						})
						continue

					case agent.AgentEventTypeCodeStream:
						// Phase 4: Code Stream Fusion
						// Stream partial code generation to frontend
						// Add tool_call_id to Data for frontend to identify correct tool
						var toolCallID string
						var toolIndex int
						var toolName string
						var toolInput string
						if agentEvent.ToolCall != nil {
							toolCallID = agentEvent.ToolCall.ID
							toolIndex = agentEvent.ToolCall.Index
							toolName = agentEvent.ToolCall.Name
							toolInput = agentEvent.ToolCall.Input
						}

						writeJSON(Event{
							Type:            "code_stream",
							Content:         agentEvent.Content,
							SessionID:       m.SessionID,
							ToolExecutionID: agentEvent.ToolExecutionID,
							Data: map[string]interface{}{
								"tool_call_id": toolCallID,
								"tool_index":   toolIndex,
								"tool_name":    toolName,
								"tool_input":   toolInput,
							},
						})
						continue
					case agent.AgentEventTypeToolResult:
						if agentEvent.ToolResult == nil {
							continue
						}

						toolResult := *agentEvent.ToolResult
						wordActions, cleanedContent := extractWordEditorSignals(toolResult.Content)
						if len(wordActions) > 0 {
							for _, wordAction := range wordActions {
								if err := s.broadcastWordEditorAction(m.SessionID, wordAction); err != nil {
									logging.Warn("Blocked invalid word editor action from tool result", "session_id", m.SessionID, "error", err)
									writeJSON(Event{Type: EventTypeError, Content: err.Error(), SessionID: m.SessionID})
								}
							}
						}
						if cleanedContent == "" && len(wordActions) > 0 {
							continue
						}

						toolResult.Content = cleanedContent
						eventType = EventTypeToolResult
						// Serialize tool result
						toolResultData, _ := json.Marshal(toolResult)
						content = string(toolResultData)

						// FIX: Broadcast "observation" event for Monitor
						writeJSON(Event{
							Type: EventTypeThought, // "thought"
							Data: map[string]string{
								"type":    "observation",
								"content": toolResult.Content,
							},
							SessionID: m.SessionID,
						})

						// Phase 5: Pass ToolExecutionID to frontend
						writeJSON(Event{
							Type:            EventTypeToolResult,
							Content:         content,
							SessionID:       m.SessionID,
							ToolExecutionID: agentEvent.ToolExecutionID,
						})
						if snapshot, ok := parsePlanSnapshotMetadata(toolResult.Metadata); ok {
							writeJSON(Event{
								Type:            EventTypePlanSnapshot,
								SessionID:       m.SessionID,
								ToolExecutionID: agentEvent.ToolExecutionID,
								Data:            snapshot,
							})
						}
						continue
					case agent.AgentEventTypeSummarize:
						continue // Skip summarize events for now

					}

					if eventType != "" {
						writeJSON(Event{
							Type:      eventType,
							Content:   content,
							SessionID: m.SessionID,
							Usage:     usage,
						})
					}
				}

				// Flush pending token buffer
				if len(tokenBatchBuffer) > 0 {
					writeJSON(Event{
						Type:      EventTypeToken,
						Content:   tokenBatchBuffer,
						SessionID: m.SessionID,
					})
				}

				// Flush pending buffer after loop ends
				if len(pendingBuffer) > 0 {
					if isThinking {
						writeJSON(Event{
							Type: EventTypeThought,
							Data: map[string]string{
								"type":    "thought",
								"content": pendingBuffer,
							},
							SessionID: m.SessionID,
						})
					} else {
						writeJSON(Event{
							Type:      EventTypeToken,
							Content:   pendingBuffer,
							SessionID: m.SessionID,
						})
					}
				}

				// Send Done event
				writeJSON(Event{
					Type:      EventTypeDone,
					SessionID: m.SessionID,
				})
			}(msg, genCtx, genCancel, isInjection)
		}
	}
}

func (s *Server) handleTranscribe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse multipart form (10MB limit)
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		http.Error(w, "Failed to parse form: "+err.Error(), http.StatusBadRequest)
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "File is required", http.StatusBadRequest)
		return
	}
	defer file.Close()

	// Read file content
	audioData, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "Failed to read file: "+err.Error(), http.StatusInternalServerError)
		return
	}

	rawModelID := strings.TrimSpace(r.FormValue("model"))
	modelID := models.ModelID(rawModelID)
	if modelID == "" || rawModelID == "local-whisper" {
		modelID = models.LocalSenseVoice
	}

	if modelID != models.LocalSenseVoice {
		http.Error(w, "Unsupported transcription model: "+string(modelID), http.StatusBadRequest)
		return
	}

	logging.Info("Transcribe request", "model_id", modelID)

	text, err := s.transcribeLocalAudio(r.Context(), modelID, audioData, header.Filename)
	if err != nil {
		logging.Error("Transcription failed", "error", err)
		http.Error(w, "Transcription failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	json.NewEncoder(w).Encode(map[string]string{
		"text": text,
	})
}

func (s *Server) handleGenerateSpeech(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req struct {
		Text     string `json:"text"`
		Voice    string `json:"voice"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Default provider to Aliyun for "beautiful female voice" (longxiaochun)
	providerName := models.ProviderAliyun
	if req.Provider != "" {
		providerName = models.ModelProvider(req.Provider)
	}

	// Get API Key
	apiKey := ""
	if s.app.Config != nil {
		if p, ok := s.app.Config.Providers[providerName]; ok {
			apiKey = p.APIKey
		}
	}
	if apiKey == "" {
		if providerName == models.ProviderAliyun {
			apiKey = os.Getenv("ALIYUN_API_KEY")
			if apiKey == "" {
				apiKey = os.Getenv("DASHSCOPE_API_KEY")
			}
		} else if providerName == models.ProviderOpenAI {
			apiKey = os.Getenv("OPENAI_API_KEY")
		}
	}

	opts := []provider.ProviderClientOption{
		provider.WithAPIKey(apiKey),
	}
	if req.Model != "" {
		opts = append(opts, provider.WithModel(models.Model{
			ID:       models.ModelID(req.Model),
			APIModel: req.Model,
			Provider: providerName,
		}))
	}

	p, err := provider.NewProvider(providerName, opts...)
	if err != nil {
		http.Error(w, "Failed to init provider: "+err.Error(), http.StatusInternalServerError)
		return
	}

	audioData, err := p.GenerateSpeech(r.Context(), req.Text, req.Voice)
	if err != nil {
		logging.Error("TTS failed", "error", err)
		http.Error(w, "TTS failed: "+err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "audio/mpeg")
	w.Write(audioData)
}
