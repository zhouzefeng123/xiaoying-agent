package server

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/auramate/auramate/internal/config"
)

func (s *Server) requireSessionAccess(ctx context.Context, sessionID string) error {
	if strings.TrimSpace(sessionID) == "" {
		return nil
	}
	_, err := s.app.Sessions.Get(ctx, sessionID)
	return err
}

func isPublicAPIPath(path string) bool {
	switch {
	case path == "/health":
		return true
	case strings.HasPrefix(path, "/v1/auth/"):
		return true
	case path == "/v1/user/current":
		return true
	default:
		return false
	}
}

func (s *Server) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions || isPublicAPIPath(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}

		token := localAuthTokenFromRequest(r)
		email, ok := localAuthSessionEmailFromToken(token)
		if !ok {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":  "fail",
				"message": "未登录或会话已过期",
			})
			return
		}

		workspace, err := config.EnsureUserWorkspace(email)
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusInternalServerError)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"status":  "fail",
				"message": "无法创建用户工作区",
			})
			return
		}

		ctx := config.ContextWithUserEmail(r.Context(), email)
		ctx = config.ContextWithWorkspace(ctx, workspace)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
