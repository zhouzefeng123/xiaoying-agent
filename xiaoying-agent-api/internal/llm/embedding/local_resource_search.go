package embedding

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

func resolveLocalFileResource(envKeys []string, names []string, searchPatterns []string, notFoundMessage string) (string, error) {
	for _, envKey := range envKeys {
		if envPath := os.Getenv(envKey); envPath != "" {
			if info, err := os.Stat(envPath); err == nil && !info.IsDir() {
				return filepath.Clean(envPath), nil
			}
		}
	}

	for _, root := range localResourceSearchRoots() {
		for _, pattern := range searchPatterns {
			for _, name := range names {
				candidate := filepath.Join(root, fmt.Sprintf(pattern, name))
				if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
					return candidate, nil
				}
			}
		}
	}

	return "", errors.New(notFoundMessage)
}

func resolveLocalDirResource(envKeys []string, searchPaths []string, notFoundMessage string) (string, error) {
	for _, envKey := range envKeys {
		if envPath := os.Getenv(envKey); envPath != "" {
			if info, err := os.Stat(envPath); err == nil && info.IsDir() {
				return filepath.Clean(envPath), nil
			}
		}
	}

	for _, root := range localResourceSearchRoots() {
		for _, searchPath := range searchPaths {
			candidate := filepath.Join(root, searchPath)
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				return candidate, nil
			}
		}
	}

	return "", errors.New(notFoundMessage)
}

func localResourceSearchRoots() []string {
	roots := make([]string, 0, 6)
	seen := map[string]struct{}{}
	appendRoot := func(root string) {
		if root == "" {
			return
		}
		clean := filepath.Clean(root)
		if _, ok := seen[clean]; ok {
			return
		}
		seen[clean] = struct{}{}
		roots = append(roots, clean)
	}

	if exePath, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exePath)
		appendRoot(exeDir)
		appendRoot(filepath.Dir(exeDir))
		appendRoot(filepath.Dir(filepath.Dir(exeDir)))
	}

	if workingDir, err := os.Getwd(); err == nil {
		appendRoot(workingDir)
		appendRoot(filepath.Dir(workingDir))
	}

	return roots
}
