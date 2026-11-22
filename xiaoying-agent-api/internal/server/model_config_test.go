package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auramate/auramate/internal/config"
)

func TestModelConfigAppliesWithoutRestart(t *testing.T) {
	// Config.Load is a process singleton; isolate it from workspace tests.
	if os.Getenv("AURAMATE_MODEL_CONFIG_TEST_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestModelConfigAppliesWithoutRestart$")
		cmd.Env = append(os.Environ(), "AURAMATE_MODEL_CONFIG_TEST_CHILD=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated model config test: %v\n%s", err, output)
		}
		return
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".auramate.json")
	if err := os.WriteFile(path, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AURAMATE_CONFIG_FILE", path)
	if _, err := config.Load(dir, false); err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	// Saving settings needs no platform membership or auth token in the model route.
	body := `{"main_model":{"provider":"custom","model":"own-model","api_key":"own-key","base_url":"https://own.example/v1","supports_attachments":true}}`
	w := httptest.NewRecorder()
	s.handleConfig(w, httptest.NewRequest(http.MethodPost, "/v1/config", strings.NewReader(body)))
	if w.Code != http.StatusOK {
		t.Fatalf("save failed: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "needs_restart") {
		t.Fatal("model save must apply immediately")
	}
	model, ok := config.CurrentMainModel()
	if !ok || model.APIModel != "own-model" || model.APIKey != "own-key" || model.BaseURL != "https://own.example/v1" || !model.SupportsAttachments {
		t.Fatal("saved model was not applied")
	}
	persisted, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(persisted), "own-model") {
		t.Fatal("model was not persisted")
	}
	list := httptest.NewRecorder()
	s.handleModels(list, httptest.NewRequest(http.MethodGet, "/v1/models", nil))
	if strings.Contains(list.Body.String(), "own-key") || !strings.Contains(list.Body.String(), "custom-main-model") {
		t.Fatal("model list exposes credentials or omits configured model")
	}
	// Clearing a key for a local endpoint must not restore the preceding key.
	body = `{"main_model":{"provider":"ollama","model":"local-model","api_key":"","base_url":"http://localhost:11435/v1","supports_attachments":false}}`
	w = httptest.NewRecorder()
	s.handleConfig(w, httptest.NewRequest(http.MethodPost, "/v1/config", strings.NewReader(body)))
	model, ok = config.CurrentMainModel()
	if w.Code != 200 || !ok || model.APIModel != "local-model" || model.APIKey != "" || model.SupportsAttachments {
		t.Fatal("replacement settings not applied")
	}
	// Existing account protection must still guard configuration and its API key.
	protected := httptest.NewRecorder()
	s.authMiddleware(http.HandlerFunc(s.handleConfig)).ServeHTTP(protected, httptest.NewRequest(http.MethodGet, "/v1/config", nil))
	if protected.Code != http.StatusUnauthorized {
		t.Fatal("configuration must require login")
	}
}

func TestModelConfigRejectsInvalidInput(t *testing.T) {
	for _, model := range []map[string]any{
		{"provider": "openai", "model": " "},
		{"provider": "custom", "model": "own-model"},
		{"provider": "openai", "model": "own-model", "base_url": "file:///tmp/model"},
		{"provider": "openai", "model": "own-model", "base_url": "https://"},
		{"provider": "invalid", "model": "own-model"},
	} {
		body, _ := json.Marshal(map[string]any{"main_model": model})
		w := httptest.NewRecorder()
		(&Server{}).handleConfig(w, httptest.NewRequest(http.MethodPost, "/v1/config", strings.NewReader(string(body))))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("invalid config returned %d", w.Code)
		}
	}
}
