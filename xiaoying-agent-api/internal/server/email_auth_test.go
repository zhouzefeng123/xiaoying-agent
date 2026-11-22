package server

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/textproto"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/auramate/auramate/internal/config"
)

// Capture a message sent through the real SMTP transport, without external mail.
func captureLoginMail(t *testing.T) <-chan string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	t.Setenv("AURAMATE_SMTP_HOST", "127.0.0.1")
	t.Setenv("AURAMATE_SMTP_PORT", strconv.Itoa(listener.Addr().(*net.TCPAddr).Port))
	t.Setenv("AURAMATE_SMTP_USER", "sender@example.com")
	t.Setenv("AURAMATE_SMTP_PASSWORD", "test-password")
	t.Setenv("AURAMATE_SMTP_FROM", "sender@example.com")
	t.Setenv("AURAMATE_SMTP_TLS", "none")
	messages := make(chan string, 1)
	go func() {
		defer close(messages)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		protocol := textproto.NewConn(conn)
		_ = protocol.PrintfLine("220 localhost ESMTP")
		for {
			line, err := protocol.ReadLine()
			if err != nil {
				return
			}
			switch {
			case strings.HasPrefix(line, "EHLO"):
				_ = protocol.PrintfLine("250-localhost")
				_ = protocol.PrintfLine("250 AUTH PLAIN")
			case strings.HasPrefix(line, "AUTH PLAIN"):
				_ = protocol.PrintfLine("235 Authentication successful")
			case line == "DATA":
				_ = protocol.PrintfLine("354 Send message")
				body, err := io.ReadAll(protocol.DotReader())
				if err != nil {
					return
				}
				messages <- string(body)
				_ = protocol.PrintfLine("250 Accepted")
			case line == "QUIT":
				_ = protocol.PrintfLine("221 Bye")
				return
			default:
				_ = protocol.PrintfLine("250 OK")
			}
		}
	}()
	return messages
}

func TestDirectEmailCodeLogin(t *testing.T) {
	// config.Load is a singleton; isolate workspace configuration from other tests.
	if os.Getenv("AURAMATE_EMAIL_AUTH_TEST_CHILD") != "1" {
		cmd := exec.Command(os.Args[0], "-test.run=^TestDirectEmailCodeLogin$")
		cmd.Env = append(os.Environ(), "AURAMATE_EMAIL_AUTH_TEST_CHILD=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("email auth test: %v\n%s", err, output)
		}
		return
	}
	dir := t.TempDir()
	path := filepath.Join(dir, ".auramate.json")
	payload, _ := json.Marshal(map[string]any{"wd": dir})
	if err := os.WriteFile(path, payload, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AURAMATE_CONFIG_FILE", path)
	cfg, err := config.Load(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	cfg.WorkingDir = dir
	t.Setenv("AURAMATE_AUTH_ALLOWED_EMAILS", "user@example.com")
	resetLocalAuthStore()
	t.Cleanup(resetLocalAuthStore)
	mailMessages := captureLoginMail(t)
	s := &Server{}
	mode := httptest.NewRecorder()
	s.handleAuthConfig(mode, httptest.NewRequest(http.MethodGet, "/v1/auth/config", nil))
	if !strings.Contains(mode.Body.String(), "\"mode\":\"local\"") {
		t.Fatal("expected direct email authentication")
	}
	send := httptest.NewRecorder()
	s.handleSendEmailCode(send, httptest.NewRequest(http.MethodPost, "/v1/auth/send-email-code", strings.NewReader(`{"email":" User@Example.com "}`)))
	if send.Code != http.StatusOK {
		t.Fatalf("send failed: %d %s", send.Code, send.Body.String())
	}
	var rawMessage string
	select {
	case rawMessage = <-mailMessages:
	case <-time.After(10 * time.Second):
		t.Fatal("SMTP message was not received")
	}
	message, err := mail.ReadMessage(bufio.NewReader(strings.NewReader(rawMessage)))
	if err != nil {
		t.Fatal(err)
	}
	if message.Header.Get("To") != "user@example.com" {
		t.Fatal("incorrect recipient")
	}
	content, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, message.Body))
	if err != nil {
		t.Fatal(err)
	}
	code := regexp.MustCompile(`[0-9]{6}`).FindString(string(content))
	if code == "" {
		t.Fatal("missing six-digit email code")
	}
	if strings.Contains(send.Body.String(), code) {
		t.Fatal("code leaked in HTTP response")
	}
	retry := httptest.NewRecorder()
	s.handleSendEmailCode(retry, httptest.NewRequest(http.MethodPost, "/v1/auth/send-email-code", strings.NewReader(`{"email":"user@example.com"}`)))
	if retry.Code != http.StatusTooManyRequests {
		t.Fatal("send cooldown was not enforced")
	}
	wrongCode := "000000"
	if code == wrongCode {
		wrongCode = "111111"
	}
	wrong := httptest.NewRecorder()
	s.handleLogin(wrong, httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(`{"username":"user@example.com","code":"`+wrongCode+`"}`)))
	if wrong.Code != http.StatusBadRequest || len(wrong.Result().Cookies()) != 0 {
		t.Fatal("wrong code received a session")
	}
	loginBody := `{"username":"user@example.com","code":"` + code + `"}`
	login := httptest.NewRecorder()
	s.handleLogin(login, httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(loginBody)))
	if login.Code != http.StatusOK {
		t.Fatalf("login failed: %d %s", login.Code, login.Body.String())
	}
	var response struct{ Data struct{ Token string } }
	if err := json.Unmarshal(login.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(response.Data.Token, "local_") {
		t.Fatal("missing local session")
	}
	cookies := login.Result().Cookies()
	if len(cookies) == 0 || !cookies[0].HttpOnly {
		t.Fatal("missing secure session cookie")
	}
	reached := false
	protected := s.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		if config.UserEmailFromContext(r.Context()) != "user@example.com" {
			t.Error("incorrect workspace owner")
		}
		w.WriteHeader(http.StatusOK)
	}))
	request := httptest.NewRequest(http.MethodGet, "/v1/config", nil)
	request.AddCookie(cookies[0])
	result := httptest.NewRecorder()
	protected.ServeHTTP(result, request)
	if result.Code != http.StatusOK || !reached {
		t.Fatal("authenticated user could not access application")
	}
	current := httptest.NewRecorder()
	s.handleCurrent(current, request)
	if current.Code != http.StatusOK || !strings.Contains(current.Body.String(), "user@example.com") {
		t.Fatal("current user lost session")
	}
	replay := httptest.NewRecorder()
	s.handleLogin(replay, httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(loginBody)))
	if replay.Code != http.StatusBadRequest {
		t.Fatal("used code was accepted again")
	}
	s.handleLocalLogout(httptest.NewRecorder(), request)
	rejected := httptest.NewRecorder()
	protected.ServeHTTP(rejected, request)
	if rejected.Code != http.StatusUnauthorized {
		t.Fatal("logout did not revoke session")
	}
}

func TestDirectEmailCodeMissingSMTP(t *testing.T) {
	t.Setenv("AURAMATE_AUTH_ALLOWED_EMAILS", "user@example.com")
	t.Setenv("AURAMATE_SMTP_HOST", "")
	t.Setenv("AURAMATE_SMTP_USER", "")
	t.Setenv("AURAMATE_SMTP_PASSWORD", "")
	recorder := httptest.NewRecorder()
	(&Server{}).handleSendEmailCode(recorder, httptest.NewRequest(http.MethodPost, "/v1/auth/send-email-code", strings.NewReader(`{"email":"user@example.com"}`)))
	if recorder.Code != http.StatusServiceUnavailable || !strings.Contains(recorder.Body.String(), "SMTP") {
		t.Fatal("missing mail configuration was not reported")
	}
}

func TestDirectEmailCodeIsConsumedOnceConcurrently(t *testing.T) {
	t.Setenv("AURAMATE_AUTH_ALLOWED_EMAILS", "user@example.com")
	resetLocalAuthStore()
	t.Cleanup(resetLocalAuthStore)
	localAuthStore.Lock()
	localAuthStore.codes["user@example.com"] = localEmailCode{Code: "123456", ExpiresAt: time.Now().Add(time.Minute)}
	localAuthStore.Unlock()
	results := make(chan int, 10)
	var pending sync.WaitGroup
	for i := 0; i < 10; i++ {
		pending.Add(1)
		go func() {
			defer pending.Done()
			w := httptest.NewRecorder()
			(&Server{}).handleLogin(w, httptest.NewRequest(http.MethodPost, "/v1/auth/login", strings.NewReader(`{"username":"user@example.com","code":"123456"}`)))
			results <- w.Code
		}()
	}
	pending.Wait()
	close(results)
	successes := 0
	for status := range results {
		if status == http.StatusOK {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("one code created %d sessions", successes)
	}
}
