package common

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/auramate/auramate/internal/config"
)

const (
	AuraAPIBaseURL  = "https://auth.chaocetec.cn/api"
	AuraLLMProxyURL = "https://service.chaocetec.cn/RSedyMDOsQgaBKFc/ai/proxy"
)

// GetDynamicAPIKey 动态API密钥
func GetDynamicAPIKey() (string, int, error) {
	token := strings.TrimSpace(config.GetAuraAuthToken())
	if token == "" {
		return "", http.StatusUnauthorized, fmt.Errorf("unauthorized: no token found")
	}
	return token, http.StatusOK, nil
}
