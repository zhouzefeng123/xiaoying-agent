package agent

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/auramate/auramate/internal/config"
)

func workerProjectRoot(workingDir string) string {
	if cfg := config.Get(); cfg != nil && strings.TrimSpace(cfg.ProjectRoot) != "" {
		return filepath.Clean(cfg.ProjectRoot)
	}

	workingDir = strings.TrimSpace(workingDir)
	if workingDir != "" {
		cleaned := filepath.Clean(workingDir)
		if strings.EqualFold(filepath.Base(cleaned), "workspace") {
			return filepath.Dir(cleaned)
		}
		return cleaned
	}

	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}
