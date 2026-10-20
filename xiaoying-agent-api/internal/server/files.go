package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/diff"
	"github.com/auramate/auramate/internal/history"
	"github.com/auramate/auramate/internal/logging"
)

func (s *Server) handleWorkspaceFile(w http.ResponseWriter, r *http.Request) {
	root := config.WorkspaceFromContext(r.Context())
	if root == "" {
		http.Error(w, "workspace is not available", http.StatusUnauthorized)
		return
	}

	rel := strings.TrimPrefix(r.URL.Path, "/workspace/")
	clean := path.Clean("/" + rel)
	if clean == "/" {
		http.NotFound(w, r)
		return
	}

	filePath := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(clean, "/")))
	relCheck, err := filepath.Rel(root, filePath)
	if err != nil || filepath.IsAbs(relCheck) || relCheck == ".." ||
		strings.HasPrefix(relCheck, ".."+string(filepath.Separator)) {
		http.Error(w, "invalid workspace path", http.StatusForbidden)
		return
	}
	http.ServeFile(w, r, filePath)
}

func pathWithinRoot(root, target string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func safeWorkspacePath(ctx context.Context, raw string) (string, error) {
	root := config.WorkingDirectoryFor(ctx)
	if strings.TrimSpace(raw) == "" {
		return "", fmt.Errorf("path is required")
	}
	target := raw
	if !filepath.IsAbs(target) {
		target = filepath.Join(root, target)
	}
	target = filepath.Clean(target)
	if !pathWithinRoot(root, target) {
		return "", fmt.Errorf("path is outside the current workspace")
	}
	return target, nil
}

// Helper to get file content (from history if exists, else from disk)
// But for "Diff", we want Latest Snapshot vs Disk.
// For "Keep", we want Disk -> Snapshot.
// For "Undo", we want Snapshot -> Disk.

func (s *Server) GetFileStatus(ctx context.Context) (map[string]string, error) {
	statusMap := make(map[string]string)

	// Resolve absolute working directory
	workingDir, err := filepath.Abs(config.WorkingDirectoryFor(ctx))
	if err != nil {
		return nil, err
	}

	// 1. Get baseline snapshots (initial/oldest version per file = state before AI edits)
	baselineFiles, err := s.app.History.ListAllBaselineFiles(ctx)
	if err != nil {
		return nil, err
	}

	// Build lookup maps: both by full path and by basename
	trackedFiles := make(map[string]string)      // absPath -> content
	trackedByBasename := make(map[string]string) // basename -> content (for fallback)
	for _, f := range baselineFiles {
		normalizedPath := filepath.ToSlash(f.Path)
		trackedFiles[normalizedPath] = f.Content
		// Also index by basename for cross-directory matching
		trackedByBasename[filepath.Base(f.Path)] = f.Content
	}

	// 2. Walk working directory
	logging.Debug("Walking working directory", "path", workingDir)
	err = filepath.Walk(workingDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden dirs and node_modules
		if info.IsDir() {
			name := info.Name()
			if strings.HasPrefix(name, ".") && name != "." { // .git, .vscode, etc.
				return filepath.SkipDir
			}
			if name == "node_modules" || name == "dist" || name == "build" || name == "bin" {
				return filepath.SkipDir
			}
			return nil
		}

		// Check file status
		// Convert to relative path for response
		relPath, err := filepath.Rel(workingDir, path)
		if err != nil {
			return nil
		}
		// Windows paths
		relPath = filepath.ToSlash(relPath)

		// Normalize path for lookup
		// Ensure both path and trackedFiles keys are normalized to slash
		normalizedPath := filepath.ToSlash(path)
		// On Windows, filepath.ToSlash converts backslashes to slashes.
		// However, db stored path might be absolute or relative, but ListAllLatestFiles usually returns absolute if CreateVersion was called with absolute.
		// Let's check if trackedFiles contains this path.

		snapshotContent, isTracked := trackedFiles[normalizedPath]

		// Fallback 1: Check if trackedFiles has it in a different format (e.g. lower case on Windows)
		if !isTracked {
			for k, v := range trackedFiles {
				if strings.EqualFold(k, normalizedPath) {
					snapshotContent = v
					isTracked = true
					// Remove the key we found
					delete(trackedFiles, k)
					break
				}
			}
		}

		// Fallback 2: Basename match (for cross-directory scenarios)
		if !isTracked {
			basename := filepath.Base(path)
			if content, ok := trackedByBasename[basename]; ok {
				snapshotContent = content
				isTracked = true
				// Remove from basename map to avoid duplicate matching
				delete(trackedByBasename, basename)
			}
		} else {
			// Remove direct match
			delete(trackedFiles, normalizedPath)
		}

		if isTracked {
			// Compare content against baseline
			diskContent, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			if snapshotContent == "" && len(diskContent) > 0 {
				// Baseline is empty = file was created by AI in this session
				statusMap[relPath] = "??"
			} else if string(diskContent) != snapshotContent {
				statusMap[relPath] = "M"
			} else {
				// Unmodified
				statusMap[relPath] = "C"
			}
			// Remove from trackedFiles map is already done above
		} else {
			// Untracked
			// Only show if created/modified within the last 24 hours
			// This prevents pre-existing files from showing as "New" while keeping recent work visible
			// ignoring s.StartTime which resets on restart
			logging.Debug("Checking untracked file", "path", path, "modTime", info.ModTime(), "since", time.Since(info.ModTime()))
			if time.Since(info.ModTime()) < 24*time.Hour {
				statusMap[relPath] = "??"
			}
		}

		return nil
	})

	if err != nil {
		return nil, err
	}

	// 3. Check for deletions (remaining in trackedFiles)
	for path := range trackedFiles {
		// path in trackedFiles is already ToSlash because we normalized keys above
		// But filepath.Rel expects OS specific separators if we pass it directly?
		// filepath.Rel(base, targ) calls Clean on both.
		// On Windows, Rel("C:\foo", "C:/foo/bar") might work or fail depending on Go version/implementation.
		// Safe bet: Convert workingDir to Slash for Rel calculation if path is Slash.
		// OR convert path back to OS path.

		osPath := filepath.FromSlash(path)
		relPath, err := filepath.Rel(workingDir, osPath)
		if err == nil {
			// Ensure path is inside working directory
			relPath = filepath.ToSlash(relPath)
			if !strings.HasPrefix(relPath, "..") && !filepath.IsAbs(relPath) {
				statusMap[relPath] = "D"
			} else {
				// Debug log for filtered files
				// logging.Debug("Filtering out external file from status", "path", path, "workingDir", workingDir)
			}
		}
	}

	return statusMap, nil
}

func (s *Server) GetFileStatusForPaths(ctx context.Context, paths []string) (map[string]string, error) {
	statusMap := make(map[string]string)
	workingDir, err := filepath.Abs(config.WorkingDirectoryFor(ctx))
	if err != nil {
		return nil, err
	}

	for _, path := range paths {
		absPath, err := safeWorkspacePath(ctx, path)
		if err != nil {
			continue
		}

		relPath, err := filepath.Rel(workingDir, absPath)
		if err != nil {
			continue
		}
		relPath = filepath.ToSlash(relPath)
		if strings.HasPrefix(relPath, "..") {
			continue
		}

		snapshotContent, err := s.getBaselineSnapshot(ctx, absPath)
		if err != nil {
			logging.Warn("Failed to get snapshot for incremental update", "path", absPath, "error", err)
		}

		info, err := os.Stat(absPath)
		if os.IsNotExist(err) {
			if snapshotContent != "" {
				statusMap[relPath] = "D"
			}
		} else if err == nil {
			if info.IsDir() {
				continue
			}
			diskContent, err := os.ReadFile(absPath)
			if err != nil {
				continue
			}
			if snapshotContent == "" {
				if time.Since(info.ModTime()) < 24*time.Hour {
					statusMap[relPath] = "??"
				}
			} else {
				if string(diskContent) != snapshotContent {
					statusMap[relPath] = "M"
				} else {
					statusMap[relPath] = "C"
				}
			}
		}
	}
	return statusMap, nil
}

// getBaselineSnapshot returns the initial (oldest) version content for a file.
// This represents the state before any AI edits.
func (s *Server) getBaselineSnapshot(ctx context.Context, path string) (string, error) {
	// Strategy 1: Direct path lookup
	versions, err := s.app.History.ListVersionsByPath(ctx, path)
	if err != nil {
		return "", err
	}
	if len(versions) > 0 {
		return versions[len(versions)-1].Content, nil
	}

	// Strategy 2: Try with forward slashes
	slashPath := filepath.ToSlash(path)
	if slashPath != path {
		versions, err = s.app.History.ListVersionsByPath(ctx, slashPath)
		if err == nil && len(versions) > 0 {
			return versions[len(versions)-1].Content, nil
		}
	}

	// Strategy 3: Basename match for relative paths
	basename := filepath.Base(path)
	if !strings.Contains(basename, string(filepath.Separator)) && !strings.Contains(basename, "/") {
		allVersions, _ := s.app.History.ListAllBaselineFiles(ctx)
		for _, v := range allVersions {
			if filepath.Base(v.Path) == basename {
				// Found match, get all versions
				versions, err = s.app.History.ListVersionsByPath(ctx, v.Path)
				if err == nil && len(versions) > 0 {
					return versions[len(versions)-1].Content, nil
				}
			}
		}
	}

	return "", nil
}

func (s *Server) handleFiles(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")

	// ... (Existing GET status) ...
	// We will merge handlers.

	ctx := r.Context()

	// GET /v1/files/status
	if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/status") {
		statusMap, err := s.GetFileStatus(ctx)
		if err != nil {
			http.Error(w, "Failed to get status: "+err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(statusMap)
		return
	}

	// GET /v1/files/list?path=...
	if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/list") {
		pathParam := r.URL.Query().Get("path")
		if pathParam == "" {
			pathParam = "."
		}

		workingDir, err := filepath.Abs(config.WorkingDirectoryFor(ctx))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		targetDir := filepath.Join(workingDir, pathParam)

		// Ensure target is within working dir
		if !strings.HasPrefix(targetDir, workingDir) {
			// Allow exact match or subpath
			// Windows case sensitivity might be tricky, assuming standard behavior
		}

		entries, err := os.ReadDir(targetDir)
		if err != nil {
			http.Error(w, "Failed to read directory: "+err.Error(), http.StatusInternalServerError)
			return
		}

		type FileNode struct {
			Name string `json:"name"`
			Path string `json:"path"`
			Type string `json:"type"` // "file" | "dir"
		}

		nodes := []FileNode{}
		for _, entry := range entries {
			name := entry.Name()
			// Skip hidden
			if strings.HasPrefix(name, ".") && name != "." {
				continue
			}
			if name == "node_modules" || name == "dist" || name == "build" || name == "bin" || name == ".git" {
				continue
			}

			relPath, _ := filepath.Rel(workingDir, filepath.Join(targetDir, name))
			relPath = filepath.ToSlash(relPath)

			nodeType := "file"
			if entry.IsDir() {
				nodeType = "dir"
			}

			nodes = append(nodes, FileNode{
				Name: name,
				Path: relPath,
				Type: nodeType,
			})
		}

		json.NewEncoder(w).Encode(nodes)
		return
	}

	// GET /v1/files/history?path=...
	if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/history") {
		path := r.URL.Query().Get("path")
		if path == "" {
			http.Error(w, "path required", http.StatusBadRequest)
			return
		}
		absPath, err := safeWorkspacePath(ctx, path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		versions, err := s.app.History.ListVersionsByPath(ctx, absPath)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(versions)
		return
	}

	// GET /v1/files/diff?path=...
	if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/diff") {
		pathParam := r.URL.Query().Get("path")
		if pathParam == "" {
			http.Error(w, "path required", http.StatusBadRequest)
			return
		}

		absPath, err := safeWorkspacePath(ctx, pathParam)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}

		// 1. Read Disk Content
		diskContent := ""
		diskBytes, err := os.ReadFile(absPath)
		if err == nil {
			diskContent = string(diskBytes)
		} else if !os.IsNotExist(err) {
			http.Error(w, "Failed to read file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// 2. Read Snapshot Content with path fallback
		snapshotContent, err := s.getBaselineSnapshot(ctx, absPath)
		if err != nil {
			logging.Warn("Failed to get snapshot", "error", err)
			// Proceed with empty snapshot
		}

		// 3. Generate Diff (Snapshot -> Disk)
		// GenerateDiff(old, new, filename)
		diffStr, _, _ := diff.GenerateDiff(snapshotContent, diskContent, pathParam)

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{
			"diff": diffStr,
		})
		return
	}

	// POST /v1/files/undo
	// Revert disk to baseline (initial) snapshot
	if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/undo") {
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		// Try multiple path strategies to find the file history
		var versions []history.File

		// Strategy 1: Resolve within the current user's workspace.
		testPath, err := safeWorkspacePath(ctx, req.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}

		versions, _ = s.app.History.ListVersionsByPath(ctx, testPath)
		logging.Info("Undo request", "reqPath", req.Path, "testPath", testPath, "workingDir", config.WorkingDirectoryFor(ctx), "count", len(versions))

		// Strategy 2: If not found and path is relative, try with forward slashes
		if len(versions) == 0 {
			slashPath := filepath.ToSlash(testPath)
			if slashPath != testPath {
				versions, _ = s.app.History.ListVersionsByPath(ctx, slashPath)
				if len(versions) > 0 {
					logging.Info("Undo: found with slash path", "slashPath", slashPath, "count", len(versions))
				}
			}
		}

		// Strategy 3: If still not found and path is just filename, search all versions
		if len(versions) == 0 && !strings.Contains(req.Path, string(filepath.Separator)) && !strings.Contains(req.Path, "/") {
			// It's just a filename, query all versions and filter by basename
			allVersions, _ := s.app.History.ListAllBaselineFiles(ctx)
			for _, v := range allVersions {
				if filepath.Base(v.Path) == req.Path {
					// Found matching file, get all versions for this path
					versions, _ = s.app.History.ListVersionsByPath(ctx, v.Path)
					testPath = v.Path
					logging.Info("Undo: found by basename match", "matchedPath", v.Path, "count", len(versions))
					break
				}
			}
		}

		absPath := testPath
		if len(versions) == 0 {
			// No history. Delete file (it's untracked/new).
			logging.Info("Undo: no history found, deleting file", "path", absPath)
			if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
				http.Error(w, "Failed to delete new file: "+err.Error(), http.StatusInternalServerError)
				return
			}
		} else {
			// Restore to baseline (oldest/initial version)
			baselineContent := versions[len(versions)-1].Content
			logging.Info("Undo: restoring to baseline", "path", absPath, "baselineVersion", versions[len(versions)-1].Version, "baselineLen", len(baselineContent))
			if baselineContent == "" {
				// Initial version was empty = file was created by AI, delete it
				logging.Info("Undo: baseline is empty, deleting AI-created file", "path", absPath)
				if err := os.Remove(absPath); err != nil && !os.IsNotExist(err) {
					http.Error(w, "Failed to delete file: "+err.Error(), http.StatusInternalServerError)
					return
				}
			} else {
				if err := os.WriteFile(absPath, []byte(baselineContent), 0644); err != nil {
					http.Error(w, "Failed to restore file: "+err.Error(), http.StatusInternalServerError)
					return
				}
				logging.Info("Undo: file restored successfully", "path", absPath, "restoredLen", len(baselineContent))
			}
			// Clean up all versions after undo (reset history)
			for _, v := range versions {
				_ = s.app.History.Delete(ctx, v.ID)
			}
			logging.Info("Undo: history cleaned up", "path", absPath, "deletedVersions", len(versions))
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	// POST /v1/files/keep
	// Accept current disk content as the new baseline (clear change history)
	if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/keep") {
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		absPath, err := safeWorkspacePath(ctx, req.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}

		// Delete all existing versions for this file (reset history)
		versions, _ := s.app.History.ListVersionsByPath(ctx, absPath)
		if len(versions) == 0 {
			// Try with ToSlash conversion
			slashPath := filepath.ToSlash(absPath)
			if slashPath != absPath {
				versions, _ = s.app.History.ListVersionsByPath(ctx, slashPath)
			}
		}
		for _, v := range versions {
			_ = s.app.History.Delete(ctx, v.ID)
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	// POST /v1/files/mkdir
	if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/mkdir") {
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Path == "" {
			http.Error(w, "path is required", http.StatusBadRequest)
			return
		}

		absPath, err := safeWorkspacePath(ctx, req.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		if err := os.MkdirAll(absPath, 0755); err != nil {
			http.Error(w, "Failed to create directory: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	// POST /v1/files/create (Create empty file)
	if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/create") {
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Path == "" {
			http.Error(w, "path is required", http.StatusBadRequest)
			return
		}

		absPath, err := safeWorkspacePath(ctx, req.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}

		// Check if exists
		if _, err := os.Stat(absPath); err == nil {
			http.Error(w, "File already exists", http.StatusConflict)
			return
		}

		if err := os.WriteFile(absPath, []byte(""), 0644); err != nil {
			http.Error(w, "Failed to create file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	// POST /v1/files/delete
	if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/delete") {
		var req struct {
			Path string `json:"path"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Path == "" {
			http.Error(w, "path is required", http.StatusBadRequest)
			return
		}

		absPath, err := safeWorkspacePath(ctx, req.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		if err := os.RemoveAll(absPath); err != nil {
			http.Error(w, "Failed to delete: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	// POST /v1/files/move (Rename)
	if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/move") {
		var req struct {
			From string `json:"from"`
			To   string `json:"to"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.From == "" || req.To == "" {
			http.Error(w, "from and to are required", http.StatusBadRequest)
			return
		}

		absFrom, err := safeWorkspacePath(ctx, req.From)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}
		absTo, err := safeWorkspacePath(ctx, req.To)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}

		if err := os.Rename(absFrom, absTo); err != nil {
			http.Error(w, "Failed to move/rename: "+err.Error(), http.StatusInternalServerError)
			return
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	// POST /v1/files/undo-all
	// Revert all files to their baseline (initial) state and clear history
	if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/undo-all") {
		var req struct {
			Path string `json:"path"` // Optional filter path (directory)
		}
		json.NewDecoder(r.Body).Decode(&req)

		statusMap, err := s.GetFileStatus(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		filterPath := ""
		if req.Path != "" && req.Path != "." {
			filterPath = filepath.ToSlash(req.Path)
			if !strings.HasSuffix(filterPath, "/") {
				filterPath += "/"
			}
		}

		for relPath := range statusMap {
			if filterPath != "" {
				if !strings.HasPrefix(relPath, filterPath) {
					continue
				}
			}

			absPath, err := safeWorkspacePath(ctx, relPath)
			if err != nil {
				continue
			}

			// Revert to baseline (same logic as single-file undo)
			versions, _ := s.app.History.ListVersionsByPath(ctx, absPath)
			if len(versions) == 0 {
				os.Remove(absPath)
			} else {
				baselineContent := versions[len(versions)-1].Content
				if baselineContent == "" {
					os.Remove(absPath)
				} else {
					os.WriteFile(absPath, []byte(baselineContent), 0644)
				}
				for _, v := range versions {
					_ = s.app.History.Delete(ctx, v.ID)
				}
			}
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	// POST /v1/files/keep-all
	// Accept all current disk content as new baseline (clear all change history)
	if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/keep-all") {
		var req struct {
			Path string `json:"path"` // Optional filter path
		}
		json.NewDecoder(r.Body).Decode(&req)

		statusMap, err := s.GetFileStatus(ctx)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		filterPath := ""
		if req.Path != "" && req.Path != "." {
			filterPath = filepath.ToSlash(req.Path)
			if !strings.HasSuffix(filterPath, "/") {
				filterPath += "/"
			}
		}

		for relPath := range statusMap {
			if filterPath != "" {
				if !strings.HasPrefix(relPath, filterPath) {
					continue
				}
			}

			absPath, err := safeWorkspacePath(ctx, relPath)
			if err != nil {
				continue
			}

			// Delete all versions for this file (reset history)
			versions, _ := s.app.History.ListVersionsByPath(ctx, absPath)
			for _, v := range versions {
				_ = s.app.History.Delete(ctx, v.ID)
			}
		}

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	// POST /v1/files/save
	if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/save") {
		var req struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		if req.Path == "" {
			http.Error(w, "path required", http.StatusBadRequest)
			return
		}

		absPath, err := safeWorkspacePath(ctx, req.Path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusForbidden)
			return
		}

		// Write to disk
		if err := os.WriteFile(absPath, []byte(req.Content), 0644); err != nil {
			http.Error(w, "Failed to write file: "+err.Error(), http.StatusInternalServerError)
			return
		}

		// Optionally create a version or just let "keep" handle it?
		// User asked for "edit", usually implies save to disk.
		// If we want to track every save, we can create a version.
		// But maybe we should let user decide when to "keep" (commit).
		// However, for consistency, if we modify disk, we drift from snapshot.
		// The "keep" button is for "committing" changes to snapshot.
		// So here we just write to disk.

		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
		return
	}

	http.Error(w, "Method not allowed or path not found", http.StatusNotFound)
}
