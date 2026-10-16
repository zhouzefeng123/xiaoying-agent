package server

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/auramate/auramate/internal/logging"
	"github.com/fsnotify/fsnotify"
)

type GitStatus struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}

type GitStatusUpdatePayload struct {
	Paths   []string    `json:"paths"`
	Updates []GitStatus `json:"updates"`
}

func (w *Watcher) Start() {
	go func() {
		for {
			select {
			case event, ok := <-w.watcher.Events:
				if !ok {
					return
				}
				// Ignore git directory changes to avoid loops or excessive noise
				if strings.Contains(event.Name, ".git") {
					continue
				}
				// Filter for relevant operations
				if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
					// Check if it's a new directory creation
					if event.Op&fsnotify.Create == fsnotify.Create {
						info, err := os.Stat(event.Name)
						if err == nil && info.IsDir() {
							logging.Info("New directory detected, adding to watcher", "path", event.Name)
							if err := w.addRecursive(event.Name); err != nil {
								logging.Error("Failed to watch new directory", "path", event.Name, "error", err)
							}
						}
					}
					w.debounceTrigger(event.Name)
				}
			case err, ok := <-w.watcher.Errors:
				if !ok {
					return
				}
				logging.Error("Watcher error", "error", err)
			}
		}
	}()

	// Add working directory
	// Watch recursively by walking and adding all subdirectories
	if err := w.addRecursive(w.server.app.Config.WorkingDir); err != nil {
		logging.Error("Failed to watch working directory", "error", err)
	}
}

func (w *Watcher) addRecursive(root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") && name != "." { // Skip hidden dirs like .git
				return filepath.SkipDir
			}
			if name == "node_modules" || name == "dist" || name == "build" || name == "bin" {
				return filepath.SkipDir
			}
			if err := w.watcher.Add(path); err != nil {
				// Don't fail on single dir add failure, just log
				logging.Warn("Failed to watch directory", "path", path, "error", err)
			}
		}
		return nil
	})
}

func (w *Watcher) UpdatePath(path string) {
	// Add new path to watcher recursively
	// Ideally we should remove old path, but fsnotify makes it hard to track all sub-watches.
	// For now, we just add the new one.
	if err := w.addRecursive(path); err != nil {
		logging.Error("Failed to update watcher path", "path", path, "error", err)
	} else {
		logging.Info("Watcher updated to new path", "path", path)
	}
}

func (w *Watcher) debounceTrigger(path string) {
	w.debounceMu.Lock()
	defer w.debounceMu.Unlock()

	w.pendingPaths[path] = true

	if w.timer != nil {
		w.timer.Stop()
	}

	w.timer = time.AfterFunc(500*time.Millisecond, func() {
		w.broadcastUpdates()
	})
}

func (w *Watcher) broadcastUpdates() {
	w.debounceMu.Lock()
	if len(w.pendingPaths) == 0 {
		w.debounceMu.Unlock()
		return
	}
	paths := make([]string, 0, len(w.pendingPaths))
	for p := range w.pendingPaths {
		paths = append(paths, p)
	}
	// Clear map
	w.pendingPaths = make(map[string]bool)
	w.debounceMu.Unlock()

	workingDir, err := filepath.Abs(w.server.app.Config.WorkingDir)
	if err != nil {
		logging.Error("Watcher failed to resolve working directory", "error", err)
		return
	}

	relPathSet := make(map[string]struct{}, len(paths))
	relativePaths := make([]string, 0, len(paths))
	for _, path := range paths {
		relPath, err := filepath.Rel(workingDir, path)
		if err != nil {
			continue
		}
		relPath = filepath.ToSlash(relPath)
		if strings.HasPrefix(relPath, "..") || filepath.IsAbs(relPath) {
			continue
		}
		if _, exists := relPathSet[relPath]; exists {
			continue
		}
		relPathSet[relPath] = struct{}{}
		relativePaths = append(relativePaths, relPath)
	}

	statusMap, err := w.server.GetFileStatusForPaths(context.Background(), paths)
	if err != nil {
		logging.Error("Watcher failed to get file status", "error", err)
	}

	// Initialize slice to avoid "null" in JSON
	statuses := []GitStatus{}
	for path, code := range statusMap {
		statuses = append(statuses, GitStatus{
			Path:   path,
			Status: code,
		})
	}

	if len(relativePaths) > 0 || len(statuses) > 0 {
		event := Event{
			Type: "git_status_update",
			Data: GitStatusUpdatePayload{
				Paths:   relativePaths,
				Updates: statuses,
			},
		}
		w.server.Broadcast(event)
	}
}

func (w *Watcher) Close() {
	w.watcher.Close()
}
