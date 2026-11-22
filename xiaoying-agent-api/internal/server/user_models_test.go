package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/routing"
	"github.com/auramate/auramate/internal/message"
)

func TestUserModelsAccountLifecycle(t *testing.T) {
	// Config.Load is a singleton shared with unrelated server tests.
	if os.Getenv("AURAMATE_USER_MODELS_TEST_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestUserModelsAccountLifecycle$")
		cmd.Env = append(os.Environ(), "AURAMATE_USER_MODELS_TEST_CHILD=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated model lifecycle: %v\n%s", err, output)
		}
		return
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".auramate.json")
	if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AURAMATE_CONFIG_FILE", path)
	t.Setenv("OPENAI_API_KEY", "unrelated-environment-key")
	if _, err := config.Load(dir, false); err != nil {
		t.Fatal(err)
	}
	s := &Server{}
	owner := config.ContextWithUserEmail(context.Background(), "alice@example.com")
	other := config.ContextWithUserEmail(context.Background(), "bob@example.com")
	call := func(ctx context.Context, method, route, body string, want int) config.UserModelSettings {
		t.Helper()
		w := httptest.NewRecorder()
		s.handleUserModels(w, httptest.NewRequest(method, route, strings.NewReader(body)).WithContext(ctx))
		if w.Code != want {
			t.Fatalf("%s %s returned %d, wanted %d: %s", method, route, w.Code, want, w.Body.String())
		}
		if strings.Contains(w.Body.String(), "secret-") || strings.Contains(w.Body.String(), `"api_key"`) {
			t.Fatal("API exposes model credentials")
		}
		var result config.UserModelSettings
		if want == 200 {
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
		}
		return result
	}
	empty := call(owner, "GET", "/v1/user-models", "", 200)
	if empty.Models == nil || len(empty.Models) != 0 || empty.SelectedID != "" {
		t.Fatal("new account must have an empty model list")
	}
	if _, err := config.ResolveUserRequestModel(owner, ""); !errors.Is(err, config.ErrUserModelRequired) {
		t.Fatal("new account unexpectedly resolves a model")
	}
	noModel := httptest.NewRecorder()
	s.handleChatCompletions(noModel, httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"messages":[{"role":"user","content":"hello"}]}`)).WithContext(owner))
	if noModel.Code != 400 {
		t.Fatal("chat should stop before using any service when no model exists")
	}
	call(context.Background(), "GET", "/v1/user-models", "", 401)

	type observed struct{ model, auth, path string }
	calls := make(chan observed, 8)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&payload)
		calls <- observed{payload.Model, r.Header.Get("Authorization"), r.URL.Path}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"test","object":"chat.completion","model":%q,"choices":[{"index":0,"message":{"role":"assistant","content":"configured model works"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, payload.Model)
	}))
	defer upstream.Close()
	add := func(provider, model, key, suffix string) config.UserModelSettings {
		t.Helper()
		body, _ := json.Marshal(map[string]any{"name": model, "provider": provider, "model": model, "api_key": key, "base_url": upstream.URL + suffix, "supports_images": true})
		return call(owner, "POST", "/v1/user-models", string(body), 200)
	}
	first := add("openai", "first-model", "secret-first", "/first/v1")
	firstID := first.Models[0].ID
	second := add("custom", "second-model", "secret-second", "/second/v1")
	secondID := second.Models[1].ID
	if second.SelectedID != firstID {
		t.Fatal("adding a second model should preserve selection")
	}
	snapshot, err := config.ResolveUserRequestModel(owner, firstID)
	if err != nil {
		t.Fatal(err)
	}
	selected := call(owner, "PATCH", "/v1/user-models/selection", fmt.Sprintf(`{"selected_id":%q}`, secondID), 200)
	if selected.SelectedID != secondID {
		t.Fatal("selection was not applied")
	}
	send := func(ctx context.Context, requested, expectedModel, expectedAuth, expectedPath string) {
		t.Helper()
		resolved, err := config.ResolveUserRequestModel(ctx, requested)
		if err != nil {
			t.Fatal(err)
		}
		p, err := routing.BuildProviderForContext(resolved, config.AgentNormal, "", 0)
		if err != nil {
			t.Fatal(err)
		}
		response, err := p.SendMessages(resolved, []message.Message{{Role: message.User, Parts: []message.ContentPart{message.TextContent{Text: "hello"}}}}, nil)
		if err != nil {
			t.Fatalf("configured provider failed: %v", err)
		}
		if response == nil || response.Content != "configured model works" {
			t.Fatal("unexpected provider response")
		}
		seen := <-calls
		if seen.model != expectedModel || seen.auth != expectedAuth || seen.path != expectedPath {
			t.Fatal("request used incorrect model, credentials, or endpoint")
		}
	}
	send(owner, "", "second-model", "Bearer secret-second", "/second/v1/chat/completions")
	send(owner, firstID, "first-model", "Bearer secret-first", "/first/v1/chat/completions")
	savedSnapshot, ok := config.UserRequestModel(snapshot)
	if !ok || savedSnapshot.APIModel != "first-model" {
		t.Fatal("switching must not modify an in-flight model snapshot")
	}
	list := httptest.NewRecorder()
	s.handleModels(list, httptest.NewRequest("GET", "/v1/models", nil).WithContext(owner))
	if strings.Contains(list.Body.String(), "secret-") || !strings.Contains(list.Body.String(), secondID) {
		t.Fatal("model listing should contain account models without credentials")
	}
	bob := call(other, "GET", "/v1/user-models", "", 200)
	if len(bob.Models) != 0 {
		t.Fatal("another account inherited models")
	}
	call(other, "PATCH", "/v1/user-models/selection", fmt.Sprintf(`{"selected_id":%q}`, firstID), 404)
	call(other, "DELETE", "/v1/user-models/"+firstID, "", 404)
	if _, err := config.ResolveUserRequestModel(other, firstID); err == nil {
		t.Fatal("another account can resolve a private model")
	}
	// Omitted key preserves the saved secret; an explicit empty key clears it.
	update, _ := json.Marshal(map[string]any{"name": "renamed", "provider": "openai", "model": "renamed-model", "base_url": upstream.URL + "/renamed/v1"})
	call(owner, "PUT", "/v1/user-models/"+firstID, string(update), 200)
	send(owner, firstID, "renamed-model", "Bearer secret-first", "/renamed/v1/chat/completions")
	update, _ = json.Marshal(map[string]any{"provider": "ollama", "model": "keyless-model", "api_key": "", "base_url": upstream.URL + "/local/v1"})
	call(owner, "PUT", "/v1/user-models/"+firstID, string(update), 200)
	send(owner, firstID, "keyless-model", "", "/local/v1/chat/completions")
	persisted, err := config.LoadUserModels("alice@example.com")
	if err != nil || len(persisted.Models) != 2 || persisted.SelectedID != secondID {
		t.Fatal("settings were not persisted")
	}
	afterDelete := call(owner, "DELETE", "/v1/user-models/"+secondID, "", 200)
	if afterDelete.SelectedID != firstID {
		t.Fatal("deleting selected model should select the remaining model")
	}
	last := call(owner, "DELETE", "/v1/user-models/"+firstID, "", 200)
	if len(last.Models) != 0 || last.SelectedID != "" {
		t.Fatal("deleting all models should restore empty state")
	}
	if _, err := config.ResolveUserRequestModel(owner, ""); !errors.Is(err, config.ErrUserModelRequired) {
		t.Fatal("removed models still resolve")
	}
	if _, err := routing.BuildProviderForContext(owner, config.AgentNormal, "", 0); err == nil {
		t.Fatal("empty account fell back to a global provider")
	}
}
