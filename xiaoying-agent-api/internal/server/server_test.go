package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/auramate/auramate/internal/app"
	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/agent"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/llm/routing"
	"github.com/auramate/auramate/internal/message"
	"github.com/auramate/auramate/internal/pubsub"
)

type stopTestAgent struct {
	cancelCount int32
}

func (a *stopTestAgent) Model() models.Model { return models.Model{} }

func (a *stopTestAgent) Run(ctx context.Context, sessionID string, content string, mode string, attachments ...message.Attachment) (<-chan agent.AgentEvent, error) {
	ch := make(chan agent.AgentEvent)
	close(ch)
	return ch, nil
}

func (a *stopTestAgent) Cancel(sessionID string) {
	atomic.AddInt32(&a.cancelCount, 1)
}

func (a *stopTestAgent) CancelTool(sessionID string, executionID string) {}

func (a *stopTestAgent) IsSessionBusy(sessionID string) bool { return false }

func (a *stopTestAgent) IsBusy() bool { return false }

func (a *stopTestAgent) Update(agentName config.AgentName, modelID models.ModelID) (models.Model, error) {
	return models.Model{}, nil
}

func (a *stopTestAgent) Summarize(ctx context.Context, sessionID string) error { return nil }

func (a *stopTestAgent) SetReadOnlyMode(ctx context.Context, sessionID string, enabled bool) error {
	return nil
}

func (a *stopTestAgent) Subscribe(ctx context.Context) <-chan pubsub.Event[agent.AgentEvent] {
	return nil
}

func TestProcessThinkingTaggedTokenTracksOnlyUserFacingAnswer(t *testing.T) {
	tests := []struct {
		name         string
		chunks       []string
		wantAnswer   bool
		wantThoughts []string
	}{
		{
			name:         "split thinking block is not final answer",
			chunks:       []string{"<think", "ing>inspect files", "</thinking>"},
			wantAnswer:   false,
			wantThoughts: []string{"inspect files"},
		},
		{
			name:         "answer after thinking counts as final answer",
			chunks:       []string{"<thinking>inspect files</thinking>", "修复完成"},
			wantAnswer:   true,
			wantThoughts: []string{"inspect files"},
		},
		{
			name:         "visible text before thinking counts as answer",
			chunks:       []string{"先给结论", "<thinking>internal"},
			wantAnswer:   true,
			wantThoughts: []string{"internal"},
		},
		{
			name:         "partial thinking opener is not mistaken for answer",
			chunks:       []string{"<thi", "nking>internal</thinking>"},
			wantAnswer:   false,
			wantThoughts: []string{"internal"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var pending string
			var isThinking bool
			var gotAnswer bool
			var gotThoughts []string

			for _, chunk := range tt.chunks {
				if processThinkingTaggedToken(&pending, &isThinking, chunk, func(thought string) {
					gotThoughts = append(gotThoughts, thought)
				}) {
					gotAnswer = true
				}
			}

			if gotAnswer != tt.wantAnswer {
				t.Fatalf("answer visibility mismatch: got %v want %v", gotAnswer, tt.wantAnswer)
			}
			if strings.Join(gotThoughts, "|") != strings.Join(tt.wantThoughts, "|") {
				t.Fatalf("thoughts mismatch: got %#v want %#v", gotThoughts, tt.wantThoughts)
			}
		})
	}
}

func TestResolveHTTPChatPlan(t *testing.T) {
	autoModelID := models.DeepSeekV4Flash
	originalModel, existed := models.SupportedModels[autoModelID]
	models.SupportedModels[autoModelID] = models.Model{
		ID:       autoModelID,
		Provider: models.ProviderOpenAI,
		APIModel: string(autoModelID),
	}
	defer func() {
		if existed {
			models.SupportedModels[autoModelID] = originalModel
			return
		}
		delete(models.SupportedModels, autoModelID)
	}()

	agents := map[config.AgentName]config.Agent{
		config.AgentCoder:  {Model: models.GLM51},
		config.AgentLife:   {Model: models.GLM51},
		config.AgentNormal: {Model: models.GLM51},
	}

	tests := []struct {
		name         string
		model        string
		wantAgent    config.AgentName
		wantMode     string
		wantPrimary  string
		wantStartLSP bool
	}{
		{
			name:         "aura coder alias",
			model:        "aura-coder",
			wantAgent:    config.AgentCoder,
			wantMode:     "coding",
			wantPrimary:  "deepseek-v4-flash",
			wantStartLSP: true,
		},
		{
			name:         "concrete model keeps coder agent with override",
			model:        "glm-5.1",
			wantAgent:    config.AgentCoder,
			wantMode:     "coding",
			wantPrimary:  "glm-5.1",
			wantStartLSP: true,
		},
		{
			name:         "life alias prefers custom main before qwen fallback",
			model:        "aura-life",
			wantAgent:    config.AgentLife,
			wantMode:     "autonomous",
			wantPrimary:  "deepseek-v4-flash",
			wantStartLSP: false,
		},
		{
			name:         "normal alias",
			model:        "aura-normal",
			wantAgent:    config.AgentNormal,
			wantMode:     "normal",
			wantPrimary:  "deepseek-v4-flash",
			wantStartLSP: false,
		},
		{
			name:         "unknown model falls back to coder",
			model:        "unknown-model",
			wantAgent:    config.AgentCoder,
			wantMode:     "coding",
			wantPrimary:  "deepseek-v4-flash",
			wantStartLSP: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plan := routing.ResolveHTTPChatPlan(tt.model, agents)
			if plan.AgentName != tt.wantAgent {
				t.Fatalf("agent mismatch: got %s want %s", plan.AgentName, tt.wantAgent)
			}
			if plan.Mode != tt.wantMode {
				t.Fatalf("mode mismatch: got %s want %s", plan.Mode, tt.wantMode)
			}
			if len(plan.ModelCandidates) == 0 {
				t.Fatalf("expected candidates for %s", tt.model)
			}
			if plan.ModelCandidates[0] != tt.wantPrimary {
				t.Fatalf("primary model mismatch: got %q want %q", plan.ModelCandidates[0], tt.wantPrimary)
			}
			if plan.StartLSP != tt.wantStartLSP {
				t.Fatalf("startLSP mismatch: got %t want %t", plan.StartLSP, tt.wantStartLSP)
			}
		})
	}
}

func TestValidateRequestedChatModel(t *testing.T) {
	tests := []struct {
		name    string
		model   string
		wantErr bool
	}{
		{name: "empty allowed", model: "", wantErr: false},
		{name: "legacy alias allowed", model: "auramate", wantErr: false},
		{name: "agent alias allowed", model: "aura-coder", wantErr: false},
		{name: "supported model allowed", model: "glm-5.1", wantErr: false},
		{name: "built-in volc explicit model allowed", model: "deepseek-v4-flash", wantErr: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateRequestedChatModel(tt.model)
			if tt.wantErr && err == nil {
				t.Fatalf("expected error for model %q", tt.model)
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("did not expect error for model %q, got %v", tt.model, err)
			}
		})
	}
}

func TestShouldStartLSPForClientMessage(t *testing.T) {
	tests := []struct {
		name string
		msg  ClientMessage
		want bool
	}{
		{
			name: "coding mode starts lsp",
			msg:  ClientMessage{Mode: "coding", Model: "glm-5.1"},
			want: true,
		},
		{
			name: "coder alias starts lsp",
			msg:  ClientMessage{Model: "aura-coder"},
			want: true,
		},
		{
			name: "normal mode does not start lsp",
			msg:  ClientMessage{Mode: "normal", Model: "glm-5.1"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldStartLSPForClientMessage(tt.msg); got != tt.want {
				t.Fatalf("shouldStartLSPForClientMessage() = %t, want %t", got, tt.want)
			}
		})
	}
}

func TestInferLSPLanguagesFromClientMessage(t *testing.T) {
	tests := []struct {
		name string
		msg  ClientMessage
		want []string
	}{
		{
			name: "non coding request does not infer languages",
			msg:  ClientMessage{Mode: "normal", Content: "please inspect main.go"},
			want: nil,
		},
		{
			name: "file path mention starts matching lsp only",
			msg:  ClientMessage{Mode: "coding", Content: "please fix backend/main.go"},
			want: []string{"go"},
		},
		{
			name: "code fence language starts matching lsp only",
			msg:  ClientMessage{Model: "aura-coder", Content: "```python\nprint('hi')\n```"},
			want: []string{"python"},
		},
		{
			name: "attachments participate in language inference",
			msg: ClientMessage{
				Mode: "coding",
				Attachments: []message.Attachment{
					{FileName: "src/App.tsx"},
					{FilePath: `C:\work\demo\main.rs`},
				},
			},
			want: []string{"rust", "typescript"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := inferLSPLanguagesFromClientMessage(tt.msg, ""); !equalStringSlices(got, tt.want) {
				t.Fatalf("inferLSPLanguagesFromClientMessage() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestInferLSPLanguagesFromWorkspaceMarkers(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "package.json"), []byte("{}"), 0o644); err != nil {
		t.Fatalf("failed to write package marker: %v", err)
	}

	msg := ClientMessage{Mode: "coding", Content: "help me with this project"}
	if got := inferLSPLanguagesFromClientMessage(msg, dir); !equalStringSlices(got, []string{"typescript"}) {
		t.Fatalf("inferLSPLanguagesFromClientMessage() = %v, want [typescript]", got)
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestResolveExplicitRequestedChatModel(t *testing.T) {
	tests := []struct {
		name  string
		model string
		want  string
	}{
		{name: "empty", model: "", want: ""},
		{name: "alias is not explicit", model: "aura-coder", want: ""},
		{name: "supported explicit model", model: "glm-5.1", want: "glm-5.1"},
		{name: "built-in volc explicit model", model: "deepseek-v4-flash", want: "deepseek-v4-flash"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := resolveExplicitRequestedChatModel(tt.model); got != tt.want {
				t.Fatalf("resolveExplicitRequestedChatModel(%q) = %q, want %q", tt.model, got, tt.want)
			}
		})
	}
}

func TestBuildPersistedMainModelConfig(t *testing.T) {
	t.Run("drops disabled main model config", func(t *testing.T) {
		mainModel, enabled := buildPersistedMainModelConfig(nil, "aliyun", "", "", "", 256000)
		if enabled {
			t.Fatalf("expected disabled main model config to be dropped, got %#v", mainModel)
		}
	})

	t.Run("preserves explicit custom route", func(t *testing.T) {
		mainModel, enabled := buildPersistedMainModelConfig(nil, "openai", "Qwen3.6-35B-A3B", "", "http://220.197.184.190:18080/v1", 256000)
		if !enabled {
			t.Fatal("expected custom main model config to persist")
		}
		if got := mainModel["provider"]; got != "openai" {
			t.Fatalf("provider mismatch: got %v", got)
		}
		if got := mainModel["model"]; got != "Qwen3.6-35B-A3B" {
			t.Fatalf("model mismatch: got %v", got)
		}
		if got := mainModel["baseURL"]; got != "http://220.197.184.190:18080/v1" {
			t.Fatalf("baseURL mismatch: got %v", got)
		}
		if got := mainModel["maxTokens"]; got != int64(256000) {
			t.Fatalf("maxTokens mismatch: got %v", got)
		}
	})

	t.Run("keeps existing max tokens when request omits them", func(t *testing.T) {
		existing := map[string]interface{}{
			"provider":  "openai",
			"model":     "Qwen3.6-35B-A3B",
			"baseURL":   "http://220.197.184.190:18080/v1",
			"maxTokens": float64(256000),
		}
		mainModel, enabled := buildPersistedMainModelConfig(existing, "openai", "Qwen3.6-35B-A3B", "", "http://220.197.184.190:18080/v1", 0)
		if !enabled {
			t.Fatal("expected config to remain enabled")
		}
		if got := mainModel["maxTokens"]; got != float64(256000) {
			t.Fatalf("expected existing maxTokens to be preserved, got %v", got)
		}
	})
}

func TestStageAttachmentsForWorkspace(t *testing.T) {
	workspaceDir := t.TempDir()
	sourceDir := t.TempDir()

	docPath := filepath.Join(sourceDir, "医院跟进记录.xlsx")
	if err := os.WriteFile(docPath, []byte("xlsx-content"), 0644); err != nil {
		t.Fatalf("write doc attachment: %v", err)
	}

	imagePath := filepath.Join(sourceDir, "示意图.png")
	if err := os.WriteFile(imagePath, []byte("png-content"), 0644); err != nil {
		t.Fatalf("write image attachment: %v", err)
	}

	dirPath := filepath.Join(sourceDir, "微信文件夹")
	if err := os.MkdirAll(dirPath, 0755); err != nil {
		t.Fatalf("create directory attachment: %v", err)
	}

	s := &Server{
		app: &app.App{
			Config: &config.Config{WorkingDir: workspaceDir},
		},
	}

	attachments := []message.Attachment{
		{
			FilePath: docPath,
			FileName: filepath.Base(docPath),
			MimeType: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
		},
		{
			FilePath: imagePath,
			FileName: filepath.Base(imagePath),
			MimeType: "image/png",
		},
		{
			FilePath: dirPath,
			FileName: filepath.Base(dirPath),
			MimeType: "",
		},
	}

	staged := s.stageAttachmentsForWorkspace("session-1", attachments)
	if len(staged) != 3 {
		t.Fatalf("expected 3 attachments with directory preserved, got %d", len(staged))
	}

	stagedDoc := staged[0]
	if stagedDoc.FilePath == docPath {
		t.Fatalf("expected non-image attachment to be copied into workspace, got original path %s", stagedDoc.FilePath)
	}
	if !strings.HasPrefix(stagedDoc.FilePath, filepath.Join(workspaceDir, ".auramate", "attachments")) {
		t.Fatalf("expected staged doc path inside workspace attachments dir, got %s", stagedDoc.FilePath)
	}
	if filepath.Base(stagedDoc.FilePath) == filepath.Base(docPath) {
		t.Fatalf("expected staged doc file name to avoid original Chinese name, got %s", filepath.Base(stagedDoc.FilePath))
	}
	if string(stagedDoc.Content) != "xlsx-content" {
		t.Fatalf("expected staged doc content to be loaded, got %q", string(stagedDoc.Content))
	}

	stagedDir := staged[2]
	if stagedDir.FilePath != dirPath {
		t.Fatalf("expected directory attachment path to stay original, got %s", stagedDir.FilePath)
	}
	if stagedDir.MimeType != message.DirectoryAttachmentMimeType {
		t.Fatalf("expected directory attachment mime type %q, got %q", message.DirectoryAttachmentMimeType, stagedDir.MimeType)
	}
	if len(stagedDir.Content) != 0 {
		t.Fatalf("expected directory attachment content to stay empty, got %d bytes", len(stagedDir.Content))
	}
	if _, err := os.Stat(stagedDoc.FilePath); err != nil {
		t.Fatalf("expected staged doc file to exist: %v", err)
	}

	stagedImage := staged[1]
	if stagedImage.FilePath == imagePath {
		t.Fatalf("expected image attachment to be copied into workspace, got original path %s", stagedImage.FilePath)
	}
	if !strings.HasPrefix(stagedImage.FilePath, filepath.Join(workspaceDir, ".auramate", "attachments")) {
		t.Fatalf("expected staged image path inside workspace attachments dir, got %s", stagedImage.FilePath)
	}
	if filepath.Base(stagedImage.FilePath) == filepath.Base(imagePath) {
		t.Fatalf("expected staged image file name to avoid original Chinese name, got %s", filepath.Base(stagedImage.FilePath))
	}
	if string(stagedImage.Content) != "png-content" {
		t.Fatalf("expected staged image content to be loaded, got %q", string(stagedImage.Content))
	}
	if _, err := os.Stat(stagedImage.FilePath); err != nil {
		t.Fatalf("expected staged image file to exist: %v", err)
	}
}

func TestStopSessionCancelsActiveGenerationAndAgents(t *testing.T) {
	lifeAgent := &stopTestAgent{}
	coderAgent := &stopTestAgent{}
	normalAgent := &stopTestAgent{}

	var canceled int32
	s := &Server{
		app: &app.App{
			LifeAgent:   lifeAgent,
			CoderAgent:  coderAgent,
			NormalAgent: normalAgent,
		},
		activeGens: map[string]context.CancelFunc{
			"session-1": func() {
				atomic.AddInt32(&canceled, 1)
			},
		},
	}

	s.stopSession("session-1")

	if got := atomic.LoadInt32(&canceled); got != 1 {
		t.Fatalf("expected active generation cancel to be called once, got %d", got)
	}
	if _, ok := s.activeGens["session-1"]; ok {
		t.Fatalf("expected session to be removed from activeGens")
	}
	if got := atomic.LoadInt32(&lifeAgent.cancelCount); got != 1 {
		t.Fatalf("expected life agent cancel once, got %d", got)
	}
	if got := atomic.LoadInt32(&coderAgent.cancelCount); got != 1 {
		t.Fatalf("expected coder agent cancel once, got %d", got)
	}
	if got := atomic.LoadInt32(&normalAgent.cancelCount); got != 1 {
		t.Fatalf("expected normal agent cancel once, got %d", got)
	}
}
