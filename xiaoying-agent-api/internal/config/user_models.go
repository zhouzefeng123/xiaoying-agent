package config

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/auramate/auramate/internal/llm/models"
)

const UserModelPrefix = "user-model-"

var ErrUserModelRequired = errors.New("请先点击右下角的“模型配置”添加模型，再开始对话")
var userModelsMu sync.Mutex

// UserModel contains only this account's explicit connection settings.
type UserModel struct {
	ID             string               `json:"id"`
	Name           string               `json:"name"`
	Provider       models.ModelProvider `json:"provider"`
	Model          string               `json:"model"`
	APIKey         string               `json:"api_key,omitempty"`
	BaseURL        string               `json:"base_url"`
	ContextWindow  int64                `json:"context_window"`
	SupportsImages bool                 `json:"supports_images"`
}

type UserModelSettings struct {
	Models     []UserModel `json:"models"`
	SelectedID string      `json:"selected_id"`
}

func userModelSettingsPath(email string) (string, error) {
	if strings.TrimSpace(email) == "" {
		return "", errors.New("请先登录")
	}
	// Keep credentials outside the workspace exposed to agent file tools.
	return filepath.Join(filepath.Dir(MainConfigPath()), "user-models", WorkspaceID(email)+".json"), nil
}

func readUserModelSettings(email string) (UserModelSettings, error) {
	settings := UserModelSettings{Models: []UserModel{}}
	path, err := userModelSettingsPath(email)
	if err != nil {
		return settings, err
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return settings, nil
	}
	if err != nil {
		return settings, err
	}
	if err = json.Unmarshal(data, &settings); err != nil {
		return settings, err
	}
	if settings.Models == nil {
		settings.Models = []UserModel{}
	}
	return settings, nil
}

func LoadUserModels(email string) (UserModelSettings, error) {
	userModelsMu.Lock()
	defer userModelsMu.Unlock()
	return readUserModelSettings(email)
}

// MutateUserModels serializes read/modify/write and persists before publishing.
func MutateUserModels(email string, update func(*UserModelSettings) error) (UserModelSettings, error) {
	userModelsMu.Lock()
	defer userModelsMu.Unlock()
	settings, err := readUserModelSettings(email)
	if err != nil {
		return settings, err
	}
	if err = update(&settings); err != nil {
		return settings, err
	}
	path, err := userModelSettingsPath(email)
	if err != nil {
		return settings, err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return settings, err
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return settings, err
	}
	file, err := os.CreateTemp(filepath.Dir(path), ".models-*.tmp")
	if err != nil {
		return settings, err
	}
	defer os.Remove(file.Name())
	if err = file.Chmod(0600); err == nil {
		_, err = file.Write(data)
	}
	if err == nil {
		err = file.Sync()
	}
	closeErr := file.Close()
	if err != nil {
		return settings, err
	}
	if closeErr != nil {
		return settings, closeErr
	}
	if err = os.Rename(file.Name(), path); err != nil {
		return settings, err
	}
	return settings, nil
}

func (m UserModel) Resolved() models.Model {
	supports := m.SupportsImages
	resolved := resolveMainModelOverride(&MainModelConfig{Provider: m.Provider, Model: m.Model, APIKey: m.APIKey, BaseURL: m.BaseURL, MaxTokens: m.ContextWindow, SupportsAttachments: &supports})
	resolved.ID = models.ModelID(m.ID)
	resolved.Name = m.Name
	return resolved
}

type userRequestModelKey struct{}

// ResolveUserRequestModel rejects IDs belonging to another account, and never
// imports a global preset into a newly registered account.
func ResolveUserRequestModel(ctx context.Context, requested string) (context.Context, error) {
	email := UserEmailFromContext(ctx)
	if email == "" {
		return ctx, errors.New("请先登录")
	}
	settings, err := LoadUserModels(email)
	if err != nil {
		return ctx, fmt.Errorf("读取模型配置失败: %w", err)
	}
	requested = strings.TrimSpace(requested)
	if !strings.HasPrefix(requested, UserModelPrefix) {
		switch requested {
		case "", "auto", "custom-main-model", "auramate", "aura-coder", "coder", "aura-normal", "normal", "aura-life", "life":
			requested = settings.SelectedID
		default:
			return ctx, errors.New("所选模型不属于当前账号，请重新选择模型")
		}
	}
	for _, m := range settings.Models {
		if m.ID == requested {
			return context.WithValue(ctx, userRequestModelKey{}, m.Resolved()), nil
		}
	}
	return ctx, ErrUserModelRequired
}

func UserRequestModel(ctx context.Context) (models.Model, bool) {
	if ctx == nil {
		return models.Model{}, false
	}
	m, ok := ctx.Value(userRequestModelKey{}).(models.Model)
	return m, ok
}
