package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/models"
	"github.com/google/uuid"
)

func writeUserModelSettings(w http.ResponseWriter, settings config.UserModelSettings) {
	list := make([]map[string]any, 0, len(settings.Models))
	for _, m := range settings.Models {
		list = append(list, map[string]any{"id": m.ID, "name": m.Name, "provider": m.Provider, "model": m.Model, "base_url": m.BaseURL, "context_window": m.ContextWindow, "supports_images": m.SupportsImages, "has_api_key": m.APIKey != ""})
	}
	writeLocalAuthJSON(w, http.StatusOK, map[string]any{"models": list, "selected_id": settings.SelectedID})
}

func (s *Server) handleUserModels(w http.ResponseWriter, r *http.Request) {
	email := config.UserEmailFromContext(r.Context())
	if email == "" {
		writeLocalAuthError(w, http.StatusUnauthorized, "请先登录")
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v1/user-models")
	id = strings.TrimPrefix(id, "/")
	if r.Method == http.MethodGet && id == "" {
		settings, err := config.LoadUserModels(email)
		if err != nil {
			writeLocalAuthError(w, 500, "读取模型配置失败")
			return
		}
		writeUserModelSettings(w, settings)
		return
	}
	if r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodDelete && r.Method != http.MethodPatch {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Name           string               `json:"name"`
		Provider       models.ModelProvider `json:"provider"`
		Model          string               `json:"model"`
		APIKey         *string              `json:"api_key"`
		BaseURL        string               `json:"base_url"`
		ContextWindow  int64                `json:"context_window"`
		SupportsImages bool                 `json:"supports_images"`
		SelectedID     string               `json:"selected_id"`
	}
	if r.Method != http.MethodDelete {
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32768)).Decode(&req); err != nil {
			writeLocalAuthError(w, 400, "模型配置格式无效")
			return
		}
	}
	if r.Method == http.MethodPost || r.Method == http.MethodPut {
		req.Name, req.Model, req.BaseURL = strings.TrimSpace(req.Name), strings.TrimSpace(req.Model), strings.TrimSpace(req.BaseURL)
		if req.Name == "" {
			req.Name = req.Model
		}
		if req.Model == "" || len(req.Model) > 200 || len(req.Name) > 120 {
			writeLocalAuthError(w, 400, "请填写有效的模型名称和显示名称")
			return
		}
		if req.Provider != models.ProviderOpenAI && req.Provider != models.ProviderCustom && req.Provider != models.ProviderOllama {
			writeLocalAuthError(w, 400, "请选择 OpenAI 兼容接口或 Ollama")
			return
		}
		if req.BaseURL != "" {
			u, err := url.Parse(req.BaseURL)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
				writeLocalAuthError(w, 400, "接口地址需为有效的 HTTP 或 HTTPS 地址")
				return
			}
		} else if req.Provider == models.ProviderCustom {
			writeLocalAuthError(w, 400, "请填写自定义接口地址")
			return
		}
		if req.ContextWindow < 0 || req.ContextWindow > 10000000 {
			writeLocalAuthError(w, 400, "上下文长度无效")
			return
		}
	}
	status := http.StatusBadRequest
	settings, err := config.MutateUserModels(email, func(settings *config.UserModelSettings) error {
		index := -1
		for i, m := range settings.Models {
			if m.ID == id {
				index = i
				break
			}
		}
		switch r.Method {
		case http.MethodPatch:
			if id != "selection" {
				return fmt.Errorf("无效的模型操作")
			}
			for _, m := range settings.Models {
				if m.ID == req.SelectedID {
					settings.SelectedID = m.ID
					return nil
				}
			}
			status = 404
			return fmt.Errorf("模型不存在，请重新选择")
		case http.MethodDelete:
			if index < 0 {
				status = 404
				return fmt.Errorf("模型不存在")
			}
			settings.Models = append(settings.Models[:index], settings.Models[index+1:]...)
			if settings.SelectedID == id {
				settings.SelectedID = ""
				if len(settings.Models) > 0 {
					settings.SelectedID = settings.Models[0].ID
				}
			}
			return nil
		case http.MethodPost:
			if id != "" {
				return fmt.Errorf("无效的模型操作")
			}
			if len(settings.Models) >= 50 {
				return fmt.Errorf("最多可配置 50 个模型")
			}
			id = config.UserModelPrefix + uuid.NewString()
		case http.MethodPut:
			if index < 0 {
				status = 404
				return fmt.Errorf("模型不存在")
			}
		}
		key := ""
		if index >= 0 {
			key = settings.Models[index].APIKey
		}
		if req.APIKey != nil {
			key = strings.TrimSpace(*req.APIKey)
		}
		if req.Provider == models.ProviderOllama {
			key = ""
		}
		m := config.UserModel{ID: id, Name: req.Name, Provider: req.Provider, Model: req.Model, APIKey: key, BaseURL: req.BaseURL, ContextWindow: req.ContextWindow, SupportsImages: req.SupportsImages}
		if index >= 0 {
			settings.Models[index] = m
		} else {
			settings.Models = append(settings.Models, m)
		}
		if settings.SelectedID == "" {
			settings.SelectedID = id
		}
		return nil
	})
	if err != nil {
		writeLocalAuthError(w, status, err.Error())
		return
	}
	writeUserModelSettings(w, settings)
}
