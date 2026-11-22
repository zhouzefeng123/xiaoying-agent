package server

import (
	"encoding/json"
	"io"
	"net/http"
)

func (s *Server) handleAuthConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeLocalAuthJSON(w, http.StatusOK, map[string]string{"mode": "local"})
}

// handleSendEmailCode sends a locally generated code by SMTP, without a captcha.
func (s *Server) handleSendEmailCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Email string `json:"email"`
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil || json.Unmarshal(body, &req) != nil {
		writeLocalAuthError(w, http.StatusBadRequest, "验证码请求格式无效")
		return
	}
	s.handleLocalSendEmailCode(w, req.Email)
}

// handleLogin verifies the email code before issuing a local session.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4096))
	if err != nil {
		writeLocalAuthError(w, http.StatusBadRequest, "登录请求格式无效")
		return
	}
	if !s.handleLocalLogin(w, r, body) {
		writeLocalAuthError(w, http.StatusBadRequest, "邮箱地址无效或不允许登录")
	}
}

func (s *Server) handleCurrent(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.handleLocalCurrent(w, r) {
		writeLocalAuthError(w, http.StatusUnauthorized, "未登录或会话已过期")
	}
}
