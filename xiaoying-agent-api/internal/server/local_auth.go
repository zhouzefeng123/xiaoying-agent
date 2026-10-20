package server

import (
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"mime"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/auramate/auramate/internal/logging"
)

const (
	localCodeTTL      = 5 * time.Minute
	localCodeCooldown = 60 * time.Second
	localCodeAttempts = 5
	localSessionTTL   = 7 * 24 * time.Hour
	localAuthCookie   = "xiaoying_session"
)

type localEmailCode struct {
	Code      string
	ExpiresAt time.Time
	SentAt    time.Time
	Attempts  int
}

type localAuthSession struct {
	Email     string
	ExpiresAt time.Time
}

var localAuthStore = struct {
	sync.Mutex
	codes    map[string]localEmailCode
	sessions map[string]localAuthSession
}{
	codes:    make(map[string]localEmailCode),
	sessions: make(map[string]localAuthSession),
}

type localSMTPConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
	Mode     string
}

func localAuthAllowedEmails() []string {
	raw := strings.TrimSpace(os.Getenv("AURAMATE_AUTH_ALLOWED_EMAILS"))
	if raw == "" {
		raw = strings.TrimSpace(os.Getenv("AURAMATE_AUTH_EMAIL"))
	}
	fields := strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\n' || r == '\t'
	})
	seen := make(map[string]struct{}, len(fields))
	allowed := make([]string, 0, len(fields))
	for _, field := range fields {
		email := strings.ToLower(strings.TrimSpace(field))
		if email == "" {
			continue
		}
		if _, ok := seen[email]; ok {
			continue
		}
		seen[email] = struct{}{}
		allowed = append(allowed, email)
	}
	return allowed
}

func localAuthEmailAllowed(email string) bool {
	email = strings.ToLower(strings.TrimSpace(email))
	for _, allowed := range localAuthAllowedEmails() {
		if allowed == email {
			return true
		}
		if allowed == "*" || allowed == "*@*" {
			return true
		}
		if strings.HasPrefix(allowed, "*@") {
			domain := strings.TrimPrefix(allowed, "*@")
			if domain != "" && strings.HasSuffix(email, "@"+domain) {
				return true
			}
		}
		if strings.HasPrefix(allowed, "@") {
			domain := strings.TrimPrefix(allowed, "@")
			if domain != "" && strings.HasSuffix(email, "@"+domain) {
				return true
			}
		}
	}
	return false
}

func normalizeLocalEmail(raw string) (string, error) {
	email := strings.ToLower(strings.TrimSpace(raw))
	if email == "" {
		return "", fmt.Errorf("请输入邮箱地址")
	}
	address, err := mail.ParseAddress(email)
	if err != nil || !strings.EqualFold(address.Address, email) {
		return "", fmt.Errorf("请输入有效的邮箱地址")
	}
	return email, nil
}

func localSMTPFromEnv() (localSMTPConfig, error) {
	host := strings.TrimSpace(os.Getenv("AURAMATE_SMTP_HOST"))
	user := strings.TrimSpace(os.Getenv("AURAMATE_SMTP_USER"))
	password := os.Getenv("AURAMATE_SMTP_PASSWORD")
	from := strings.TrimSpace(os.Getenv("AURAMATE_SMTP_FROM"))
	if from == "" {
		from = user
	}

	port := 465
	if rawPort := strings.TrimSpace(os.Getenv("AURAMATE_SMTP_PORT")); rawPort != "" {
		parsed, err := strconv.Atoi(rawPort)
		if err != nil || parsed <= 0 || parsed > 65535 {
			return localSMTPConfig{}, fmt.Errorf("AURAMATE_SMTP_PORT 无效")
		}
		port = parsed
	}

	mode := strings.ToLower(strings.TrimSpace(os.Getenv("AURAMATE_SMTP_TLS")))
	if mode == "" {
		if port == 587 {
			mode = "starttls"
		} else {
			mode = "ssl"
		}
	}
	switch mode {
	case "ssl", "starttls", "none":
	default:
		return localSMTPConfig{}, fmt.Errorf("AURAMATE_SMTP_TLS 仅支持 ssl/starttls/none")
	}

	if host == "" || user == "" || password == "" || from == "" {
		return localSMTPConfig{}, fmt.Errorf("SMTP 未配置")
	}

	return localSMTPConfig{
		Host:     host,
		Port:     port,
		User:     user,
		Password: password,
		From:     from,
		Mode:     mode,
	}, nil
}

func generateLocalCode() (string, error) {
	value, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%06d", value.Int64()), nil
}

func generateLocalSessionToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return "local_" + hex.EncodeToString(buf), nil
}

func maskLocalEmail(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) != 2 || parts[0] == "" {
		return "***"
	}
	local := parts[0]
	if len(local) <= 2 {
		return local[:1] + "***@" + parts[1]
	}
	return local[:2] + "***@" + parts[1]
}

func localUserPayload(email string) map[string]any {
	return map[string]any{
		"id":            "local:" + email,
		"username":      email,
		"nickname":      email,
		"email":         email,
		"member_usable": true,
	}
}

func writeLocalAuthJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeLocalAuthError(w http.ResponseWriter, status int, message string) {
	writeLocalAuthJSON(w, status, map[string]any{
		"status":  "fail",
		"message": message,
		"error":   message,
	})
}

func localAuthSessionEmailFromToken(token string) (string, bool) {
	token = strings.TrimSpace(token)
	if token == "" {
		return "", false
	}
	now := time.Now()
	localAuthStore.Lock()
	session, ok := localAuthStore.sessions[token]
	if ok && now.After(session.ExpiresAt) {
		delete(localAuthStore.sessions, token)
		ok = false
	}
	localAuthStore.Unlock()
	if !ok {
		return "", false
	}
	return session.Email, true
}

func localAuthTokenFromRequest(r *http.Request) string {
	if r == nil {
		return ""
	}
	if cookie, err := r.Cookie(localAuthCookie); err == nil {
		if token := strings.TrimSpace(cookie.Value); token != "" {
			return token
		}
	}
	authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
	if len(authHeader) >= len("Bearer ") && strings.EqualFold(authHeader[:len("Bearer ")], "Bearer ") {
		return strings.TrimSpace(authHeader[len("Bearer "):])
	}
	return ""
}

func localAuthCookieSecure(r *http.Request) bool {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("AURAMATE_COOKIE_SECURE")), "true") {
		return true
	}
	if r != nil {
		if strings.EqualFold(strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")), "https") {
			return true
		}
		return r.TLS != nil
	}
	return false
}

func setLocalAuthCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     localAuthCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(localSessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   localAuthCookieSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func clearLocalAuthCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     localAuthCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   localAuthCookieSecure(r),
		SameSite: http.SameSiteLaxMode,
	})
}

func captchaParamPresent(raw json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed != "" && trimmed != "null" && trimmed != `""`
}

func sendLocalSMTPMail(cfg localSMTPConfig, to, subject, body string) error {
	address := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	dialer := &net.Dialer{Timeout: 15 * time.Second}

	var conn net.Conn
	var err error
	if cfg.Mode == "ssl" {
		conn, err = tls.DialWithDialer(dialer, "tcp", address, &tls.Config{
			ServerName: cfg.Host,
			MinVersion: tls.VersionTLS12,
		})
	} else {
		conn, err = dialer.Dial("tcp", address)
	}
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))

	client, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return err
	}
	defer client.Close()

	if err := client.Hello(cfg.Host); err != nil {
		return err
	}
	if cfg.Mode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("SMTP 服务器不支持 STARTTLS")
		}
		if err := client.StartTLS(&tls.Config{
			ServerName: cfg.Host,
			MinVersion: tls.VersionTLS12,
		}); err != nil {
			return err
		}
	}

	auth := smtp.PlainAuth("", cfg.User, cfg.Password, cfg.Host)
	if err := client.Auth(auth); err != nil {
		return err
	}
	if err := client.Mail(cfg.From); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil {
		return err
	}

	writer, err := client.Data()
	if err != nil {
		return err
	}
	message := strings.Join([]string{
		"From: " + cfg.From,
		"To: " + to,
		"Subject: " + mime.QEncoding.Encode("UTF-8", subject),
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"Content-Transfer-Encoding: base64",
		"",
		base64.StdEncoding.EncodeToString([]byte(body)),
		"",
	}, "\r\n")
	if _, err := writer.Write([]byte(message)); err != nil {
		_ = writer.Close()
		return err
	}
	if err := writer.Close(); err != nil {
		return err
	}
	_ = client.Quit()
	return nil
}

func (s *Server) handleLocalSendEmailCode(w http.ResponseWriter, rawEmail string) {
	email, err := normalizeLocalEmail(rawEmail)
	if err != nil {
		writeLocalAuthError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(localAuthAllowedEmails()) == 0 {
		writeLocalAuthError(w, http.StatusServiceUnavailable, "本地登录未配置允许的邮箱，请设置 AURAMATE_AUTH_ALLOWED_EMAILS")
		return
	}
	if !localAuthEmailAllowed(email) {
		writeLocalAuthError(w, http.StatusForbidden, "该邮箱不允许登录")
		return
	}

	smtpCfg, err := localSMTPFromEnv()
	if err != nil {
		writeLocalAuthError(w, http.StatusServiceUnavailable, "邮件服务未配置："+err.Error())
		return
	}

	now := time.Now()
	localAuthStore.Lock()
	if existing, ok := localAuthStore.codes[email]; ok && now.Sub(existing.SentAt) < localCodeCooldown {
		localAuthStore.Unlock()
		writeLocalAuthError(w, http.StatusTooManyRequests, "发送过于频繁，请稍后再试")
		return
	}
	localAuthStore.Unlock()

	code, err := generateLocalCode()
	if err != nil {
		writeLocalAuthError(w, http.StatusInternalServerError, "生成验证码失败")
		return
	}

	localAuthStore.Lock()
	localAuthStore.codes[email] = localEmailCode{
		Code:      code,
		ExpiresAt: now.Add(localCodeTTL),
		SentAt:    now,
	}
	localAuthStore.Unlock()

	subject := "XiaoYing 登录验证码"
	body := fmt.Sprintf("您的 XiaoYing 登录验证码是：%s\n\n验证码 %d 分钟内有效。如果这不是您本人的操作，请忽略这封邮件。\n", code, int(localCodeTTL.Minutes()))
	if err := sendLocalSMTPMail(smtpCfg, email, subject, body); err != nil {
		localAuthStore.Lock()
		delete(localAuthStore.codes, email)
		localAuthStore.Unlock()
		logging.Error("Failed to send local login code", "email", maskLocalEmail(email), "error", err)
		writeLocalAuthError(w, http.StatusBadGateway, "验证码邮件发送失败，请检查 SMTP 配置")
		return
	}

	logging.Info("Sent local login code", "email", maskLocalEmail(email))
	writeLocalAuthJSON(w, http.StatusOK, map[string]any{
		"code":    0,
		"status":  "success",
		"send_at": now.Unix(),
	})
}

func (s *Server) handleLocalLogin(w http.ResponseWriter, r *http.Request, body []byte) bool {
	var req struct {
		Username string `json:"username"`
		Email    string `json:"email"`
		Code     string `json:"code"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return false
	}

	rawEmail := strings.TrimSpace(req.Email)
	if rawEmail == "" {
		rawEmail = strings.TrimSpace(req.Username)
	}
	email, err := normalizeLocalEmail(rawEmail)
	if err != nil {
		return false
	}
	if !localAuthEmailAllowed(email) {
		return false
	}

	code := strings.TrimSpace(req.Code)
	if code == "" {
		writeLocalAuthError(w, http.StatusBadRequest, "请输入验证码")
		return true
	}

	now := time.Now()
	localAuthStore.Lock()
	entry, ok := localAuthStore.codes[email]
	if !ok {
		localAuthStore.Unlock()
		writeLocalAuthError(w, http.StatusBadRequest, "验证码已过期，请重新获取")
		return true
	}
	if now.After(entry.ExpiresAt) {
		delete(localAuthStore.codes, email)
		localAuthStore.Unlock()
		writeLocalAuthError(w, http.StatusBadRequest, "验证码已过期，请重新获取")
		return true
	}
	entry.Attempts++
	if entry.Attempts > localCodeAttempts {
		delete(localAuthStore.codes, email)
		localAuthStore.Unlock()
		writeLocalAuthError(w, http.StatusTooManyRequests, "验证码尝试次数过多，请重新获取")
		return true
	}
	localAuthStore.codes[email] = entry
	localAuthStore.Unlock()

	if subtle.ConstantTimeCompare([]byte(entry.Code), []byte(code)) != 1 {
		writeLocalAuthError(w, http.StatusBadRequest, "验证码错误")
		return true
	}

	token, err := generateLocalSessionToken()
	if err != nil {
		writeLocalAuthError(w, http.StatusInternalServerError, "创建登录会话失败")
		return true
	}

	localAuthStore.Lock()
	delete(localAuthStore.codes, email)
	localAuthStore.sessions[token] = localAuthSession{
		Email:     email,
		ExpiresAt: now.Add(localSessionTTL),
	}
	localAuthStore.Unlock()

	setLocalAuthCookie(w, r, token)
	writeLocalAuthJSON(w, http.StatusOK, map[string]any{
		"code":   0,
		"status": "success",
		"data": map[string]any{
			"token": token,
			"user":  localUserPayload(email),
		},
	})
	return true
}

func (s *Server) handleLocalCurrent(w http.ResponseWriter, r *http.Request) bool {
	token := localAuthTokenFromRequest(r)
	if token == "" {
		return false
	}

	email, ok := localAuthSessionEmailFromToken(token)
	if !ok {
		return false
	}

	writeLocalAuthJSON(w, http.StatusOK, map[string]any{
		"code":   0,
		"status": "success",
		"data":   localUserPayload(email),
	})
	return true
}

func (s *Server) handleLocalLogout(w http.ResponseWriter, r *http.Request) {
	token := localAuthTokenFromRequest(r)
	if token != "" {
		localAuthStore.Lock()
		delete(localAuthStore.sessions, token)
		localAuthStore.Unlock()
	}
	clearLocalAuthCookie(w, r)
	writeLocalAuthJSON(w, http.StatusOK, map[string]any{
		"code":   0,
		"status": "success",
	})
}
