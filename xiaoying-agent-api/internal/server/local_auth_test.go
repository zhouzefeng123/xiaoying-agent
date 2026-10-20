package server

import (
	"bufio"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func resetLocalAuthStore() {
	localAuthStore.Lock()
	localAuthStore.codes = make(map[string]localEmailCode)
	localAuthStore.sessions = make(map[string]localAuthSession)
	localAuthStore.Unlock()
}

func TestNormalizeLocalEmail(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    string
		wantErr bool
	}{
		{name: "valid", input: " User@Example.COM ", want: "user@example.com"},
		{name: "empty", input: " ", wantErr: true},
		{name: "invalid", input: "not-an-email", wantErr: true},
		{name: "display name rejected", input: "User <user@example.com>", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeLocalEmail(tt.input)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCaptchaParamPresent(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{name: "empty", raw: ``, want: false},
		{name: "null", raw: `null`, want: false},
		{name: "empty string", raw: `""`, want: false},
		{name: "value", raw: `"captcha-value"`, want: true},
		{name: "object", raw: `{"x":1}`, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := captchaParamPresent(json.RawMessage(tt.raw)); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHandleLocalLoginAndCurrent(t *testing.T) {
	t.Setenv("AURAMATE_AUTH_ALLOWED_EMAILS", "user@example.com")
	resetLocalAuthStore()
	defer resetLocalAuthStore()

	localAuthStore.Lock()
	localAuthStore.codes["user@example.com"] = localEmailCode{
		Code:      "123456",
		ExpiresAt: time.Now().Add(time.Minute),
		SentAt:    time.Now(),
	}
	localAuthStore.Unlock()

	s := &Server{}
	recorder := httptest.NewRecorder()
	body := []byte(`{"username":"user@example.com","code":"123456"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	if !s.handleLocalLogin(recorder, req, body) {
		t.Fatal("expected local login to handle the request")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("login status = %d, body=%s", recorder.Code, recorder.Body.String())
	}

	var loginResp struct {
		Data struct {
			Token string `json:"token"`
			User  struct {
				Email string `json:"email"`
			} `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &loginResp); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	if !strings.HasPrefix(loginResp.Data.Token, "local_") {
		t.Fatalf("expected local token, got %q", loginResp.Data.Token)
	}
	if loginResp.Data.User.Email != "user@example.com" {
		t.Fatalf("unexpected user email: %q", loginResp.Data.User.Email)
	}

	currentReq := httptest.NewRequest(http.MethodGet, "/v1/user/current", nil)
	currentReq.Header.Set("Authorization", "Bearer "+loginResp.Data.Token)
	currentRecorder := httptest.NewRecorder()
	if !s.handleLocalCurrent(currentRecorder, currentReq) {
		t.Fatal("expected local current user handler to handle the request")
	}
	if currentRecorder.Code != http.StatusOK {
		t.Fatalf("current status = %d, body=%s", currentRecorder.Code, currentRecorder.Body.String())
	}
	if !strings.Contains(currentRecorder.Body.String(), "user@example.com") {
		t.Fatalf("current response missing email: %s", currentRecorder.Body.String())
	}
}

func TestHandleLocalLoginRejectsWrongCode(t *testing.T) {
	t.Setenv("AURAMATE_AUTH_ALLOWED_EMAILS", "user@example.com")
	resetLocalAuthStore()
	defer resetLocalAuthStore()

	localAuthStore.Lock()
	localAuthStore.codes["user@example.com"] = localEmailCode{
		Code:      "123456",
		ExpiresAt: time.Now().Add(time.Minute),
		SentAt:    time.Now(),
	}
	localAuthStore.Unlock()

	recorder := httptest.NewRecorder()
	body := []byte(`{"username":"user@example.com","code":"000000"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	if !(&Server{}).handleLocalLogin(recorder, req, body) {
		t.Fatal("expected local login to handle the request")
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestHandleLocalLoginWithoutCode(t *testing.T) {
	t.Setenv("AURAMATE_AUTH_ALLOWED_EMAILS", "user@example.com")
	resetLocalAuthStore()
	defer resetLocalAuthStore()

	recorder := httptest.NewRecorder()
	body := []byte(`{"username":"user@example.com","code":"123456"}`)
	req := httptest.NewRequest(http.MethodPost, "/v1/auth/login", nil)
	if !(&Server{}).handleLocalLogin(recorder, req, body) {
		t.Fatal("expected local login to handle the request")
	}
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", recorder.Code)
	}
}

func TestHandleLocalSendEmailCodeRequiresSMTP(t *testing.T) {
	t.Setenv("AURAMATE_AUTH_ALLOWED_EMAILS", "user@example.com")
	t.Setenv("AURAMATE_SMTP_HOST", "")
	t.Setenv("AURAMATE_SMTP_USER", "")
	t.Setenv("AURAMATE_SMTP_PASSWORD", "")
	resetLocalAuthStore()
	defer resetLocalAuthStore()

	recorder := httptest.NewRecorder()
	(&Server{}).handleLocalSendEmailCode(recorder, "user@example.com")
	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503", recorder.Code)
	}
}

func TestLocalAuthAllowedEmails(t *testing.T) {
	t.Setenv("AURAMATE_AUTH_ALLOWED_EMAILS", "User@Example.com, second@example.com; user@example.com")
	allowed := localAuthAllowedEmails()
	if len(allowed) != 2 {
		t.Fatalf("allowed = %v, want 2 entries", allowed)
	}
	if !localAuthEmailAllowed("user@example.com") || !localAuthEmailAllowed("second@example.com") {
		t.Fatalf("expected both emails to be allowed: %v", allowed)
	}
	if localAuthEmailAllowed("other@example.com") {
		t.Fatal("unexpected allowed email")
	}
}

func TestLocalAuthEmailWildcardDomains(t *testing.T) {
	t.Setenv("AURAMATE_AUTH_ALLOWED_EMAILS", "*@qq.com,*@vip.qq.com,@foxmail.com")

	for _, email := range []string{
		"first@qq.com",
		"second@vip.qq.com",
		"third@foxmail.com",
	} {
		if !localAuthEmailAllowed(email) {
			t.Fatalf("expected %s to be allowed", email)
		}
	}

	for _, email := range []string{
		"someone@evilqq.com",
		"someone@qq.com.evil.test",
		"someone@gmail.com",
	} {
		if localAuthEmailAllowed(email) {
			t.Fatalf("expected %s to be rejected", email)
		}
	}
}

func TestSendLocalSMTPMail(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	serverDone := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.Close()

		reader := bufio.NewReader(conn)
		writer := bufio.NewWriter(conn)
		writeLine := func(line string) {
			_, _ = writer.WriteString(line + "\r\n")
			_ = writer.Flush()
		}

		writeLine("220 localhost ESMTP")
		inData := false
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				serverDone <- err
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if inData {
				if line == "." {
					inData = false
					writeLine("250 OK")
				}
				continue
			}

			upper := strings.ToUpper(line)
			switch {
			case strings.HasPrefix(upper, "EHLO"):
				writeLine("250-localhost")
				writeLine("250 AUTH PLAIN")
			case strings.HasPrefix(upper, "HELO"):
				writeLine("250 localhost")
			case strings.HasPrefix(upper, "AUTH PLAIN"):
				writeLine("235 Authentication successful")
			case strings.HasPrefix(upper, "MAIL FROM"):
				writeLine("250 OK")
			case strings.HasPrefix(upper, "RCPT TO"):
				writeLine("250 OK")
			case upper == "DATA":
				inData = true
				writeLine("354 End data with <CR><LF>.<CR><LF>")
			case upper == "QUIT":
				writeLine("221 Bye")
				serverDone <- nil
				return
			default:
				writeLine("250 OK")
			}
		}
	}()

	cfg := localSMTPConfig{
		Host:     "127.0.0.1",
		Port:     listener.Addr().(*net.TCPAddr).Port,
		User:     "user@example.com",
		Password: "password",
		From:     "user@example.com",
		Mode:     "none",
	}
	if err := sendLocalSMTPMail(cfg, "to@example.com", "XiaoYing 登录验证码", "验证码：123456"); err != nil {
		t.Fatalf("send mail: %v", err)
	}
	if err := <-serverDone; err != nil {
		t.Fatalf("fake smtp server: %v", err)
	}
}
