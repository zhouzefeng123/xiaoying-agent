package server

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/auramate/auramate/internal/common"
	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/logging"
)

var AuraAPIBaseURL = "https://auth.chaocetec.cn/api"

func injectMachineCodeIntoJSONBody(body []byte) []byte {
	machineCode := strings.TrimSpace(common.GetMachineCode())
	if machineCode == "" {
		return body
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}

	payload["machine_code"] = machineCode
	updatedBody, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return updatedBody
}

func buildAuraCurrentUserURL(inboundQuery url.Values) string {
	parsedURL, err := url.Parse(fmt.Sprintf("%s/user/current", AuraAPIBaseURL))
	if err != nil {
		return fmt.Sprintf("%s/user/current", AuraAPIBaseURL)
	}

	query := parsedURL.Query()
	for key, values := range inboundQuery {
		if strings.EqualFold(key, "machine_code") {
			continue
		}
		for _, value := range values {
			query.Add(key, value)
		}
	}

	if machineCode := strings.TrimSpace(common.GetMachineCode()); machineCode != "" {
		query.Set("machine_code", machineCode)
	}

	parsedURL.RawQuery = query.Encode()
	return parsedURL.String()
}

// handleSendEmailCode 发送邮箱验证码
func (s *Server) handleSendEmailCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1. Parse request parameters
	var req struct {
		Email              string          `json:"email"`
		CaptchaVerifyParam json.RawMessage `json:"captchaVerifyParam"`
	}

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		logging.Error("Failed to read body", "error", err)
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	bodyBytes = injectMachineCodeIntoJSONBody(bodyBytes)

	logging.Info("Received send-email-code request", "body", string(bodyBytes))

	if err := json.Unmarshal(bodyBytes, &req); err != nil {
		logging.Error("Invalid request body", "error", err, "body", string(bodyBytes))
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	if !captchaParamPresent(req.CaptchaVerifyParam) {
		s.handleLocalSendEmailCode(w, req.Email)
		return
	}

	logging.Info("Forwarding captcha-protected email code request to Aura")

	// 2. Call Aura Auth API
	apiURL := fmt.Sprintf("%s/system/send-email-code", AuraAPIBaseURL)
	// We use the original bodyBytes to forward exactly what we received
	proxyReq, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		http.Error(w, "Failed to create proxy request", http.StatusInternalServerError)
		return
	}
	proxyReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(proxyReq)
	if err != nil {
		logging.Error("Failed to call Aura API", "error", err)
		http.Error(w, "Failed to call external API", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// 3. Respond
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	io.Copy(w, resp.Body)
}

// handleLogin 登录
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// 1. Parse parameters (optional, mostly for validation)
	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "Failed to read body", http.StatusBadRequest)
		return
	}
	bodyBytes = injectMachineCodeIntoJSONBody(bodyBytes)

	if s.handleLocalLogin(w, r, bodyBytes) {
		return
	}

	// 2. Call Login API
	apiURL := fmt.Sprintf("%s/system/email-login", AuraAPIBaseURL)
	proxyReq, err := http.NewRequest("POST", apiURL, bytes.NewBuffer(bodyBytes))
	if err != nil {
		http.Error(w, "Failed to create proxy request", http.StatusInternalServerError)
		return
	}
	proxyReq.Header.Set("Content-Type", "application/json")

	client := &http.Client{}
	resp, err := client.Do(proxyReq)
	if err != nil {
		logging.Error("Failed to call Aura API", "error", err)
		http.Error(w, "Failed to call external API", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "Failed to read response", http.StatusInternalServerError)
		return
	}

	// 3. If success, save token
	// Try to extract token from various possible structures to be robust
	if resp.StatusCode == http.StatusOK {
		var genericResp map[string]interface{}
		if err := json.Unmarshal(respBody, &genericResp); err == nil {
			// Try to find token in data.token, or root.token
			var token string

			if data, ok := genericResp["data"].(map[string]interface{}); ok {
				if t, ok := data["token"].(string); ok {
					token = t
				}
			}

			if token == "" {
				if t, ok := genericResp["token"].(string); ok {
					token = t
				}
			}

			if token != "" {
				if err := config.UpdateAuraAuthToken(token); err != nil {
					logging.Error("Failed to save auth token", "error", err)
					// Don't fail the request just because we couldn't save locally, let frontend handle it
				}
			} else {
				logging.Warn("Login response contained no token", "body", string(respBody))
			}
		}
	}

	// 4. Respond
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}

// handleCurrent 获取当前登录用户信息
func (s *Server) handleCurrent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if s.handleLocalCurrent(w, r) {
		return
	}

	apiURL := buildAuraCurrentUserURL(r.URL.Query())
	proxyReq, err := http.NewRequest(http.MethodGet, apiURL, nil)
	if err != nil {
		http.Error(w, "Failed to create proxy request", http.StatusInternalServerError)
		return
	}

	if authHeader := strings.TrimSpace(r.Header.Get("Authorization")); authHeader != "" {
		proxyReq.Header.Set("Authorization", authHeader)
	}

	client := &http.Client{}
	resp, err := client.Do(proxyReq)
	if err != nil {
		logging.Error("Failed to call Aura current user API", "error", err)
		http.Error(w, "Failed to call external API", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		http.Error(w, "Failed to read response", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(resp.StatusCode)
	w.Write(respBody)
}
