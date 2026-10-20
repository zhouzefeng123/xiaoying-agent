package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/permission"
)

const (
	DownloadFileToolName    = "download_file"
	downloadFileDescription = `Downloads a file from a URL to the local filesystem with smart mirror switching and retry support.
Usage:
- url: The primary URL of the file to download.
- mirrors: Optional list of alternative URLs to try if the primary fails or is too slow.
- destination: The local path to save the file to.
- timeout: Optional timeout in seconds (default: 600).
- min_speed: Optional minimum speed in bytes/sec. If download falls below this for 10s, it switches mirror. (default: 1024 = 1KB/s)
`
)

type DownloadFileParams struct {
	URL         string   `json:"url"`
	Mirrors     []string `json:"mirrors,omitempty"`
	Destination string   `json:"destination"`
	Timeout     int      `json:"timeout,omitempty"`
	MinSpeed    int      `json:"min_speed,omitempty"`
}

type downloadFileTool struct {
	permissions permission.Service
}

func NewDownloadFileTool(permissions permission.Service) BaseTool {
	return &downloadFileTool{
		permissions: permissions,
	}
}

func (t *downloadFileTool) Info() ToolInfo {
	return ToolInfo{
		Name:        DownloadFileToolName,
		Description: downloadFileDescription,
		Parameters: map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The primary URL to download.",
			},
			"mirrors": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string"},
				"description": "List of mirror URLs.",
			},
			"destination": map[string]any{
				"type":        "string",
				"description": "The local destination path.",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in seconds. Default 600.",
			},
			"min_speed": map[string]any{
				"type":        "integer",
				"description": "Minimum speed (bytes/s) before switching mirror. Default 1024.",
			},
		},
		Required: []string{"url", "destination"},
	}
}

func (t *downloadFileTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params DownloadFileParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("Invalid JSON: %v", err)), nil
	}

	if params.URL == "" {
		return NewTextErrorResponse("url is required"), nil
	}
	if params.Destination == "" {
		return NewTextErrorResponse("destination is required"), nil
	}

	// Prepare URL list (primary + mirrors)
	urls := append([]string{params.URL}, params.Mirrors...)

	// Resolve destination path
	destPath := params.Destination
	if !filepath.IsAbs(destPath) {
		cwd := config.WorkingDirectoryFor(ctx)
		destPath = filepath.Join(cwd, destPath)
	}

	// Create directory if not exists
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("Failed to create directory: %v", err)), nil
	}

	// Set overall timeout
	timeoutDuration := 600 * time.Second
	if params.Timeout > 0 {
		timeoutDuration = time.Duration(params.Timeout) * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeoutDuration)
	defer cancel()

	minSpeed := int64(1024) // 1KB/s default
	if params.MinSpeed > 0 {
		minSpeed = int64(params.MinSpeed)
	}

	var lastErr error
	for i, currentURL := range urls {
		// Attempt download
		// We use a per-URL retry of 1 (just try once per mirror, rely on mirrors for redundancy)
		// Or maybe small retry for network blips.
		err := t.downloadWithSmartLogic(ctx, currentURL, destPath, minSpeed)
		if err == nil {
			info, _ := os.Stat(destPath)
			return NewTextResponse(fmt.Sprintf("Successfully downloaded from %s (Mirror #%d) to %s (%d bytes)", currentURL, i+1, destPath, info.Size())), nil
		}
		lastErr = err
		// If failed, loop to next mirror
	}

	return NewTextErrorResponse(fmt.Sprintf("All mirrors failed. Last error: %v", lastErr)), nil
}

// SpeedMonitorReader wraps an io.Reader to monitor speed
type SpeedMonitorReader struct {
	Reader    io.Reader
	TotalRead int64
	StartTime time.Time
	LastCheck time.Time
	LastRead  int64
	MinSpeed  int64
	Ctx       context.Context
	Cancel    context.CancelFunc
}

func (r *SpeedMonitorReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.TotalRead += int64(n)

	now := time.Now()
	if now.Sub(r.LastCheck) > 5*time.Second {
		// Check speed every 5 seconds
		duration := now.Sub(r.LastCheck).Seconds()
		bytesSince := r.TotalRead - r.LastRead
		speed := float64(bytesSince) / duration

		if speed < float64(r.MinSpeed) && r.TotalRead > 0 { // Ignore start lag
			// Too slow!
			return n, fmt.Errorf("speed too low: %.2f B/s < %d B/s", speed, r.MinSpeed)
		}

		r.LastCheck = now
		r.LastRead = r.TotalRead
	}

	return n, err
}

func (t *downloadFileTool) downloadWithSmartLogic(ctx context.Context, url string, destPath string, minSpeed int64) error {
	// Resume logic same as before
	var startByte int64 = 0
	info, err := os.Stat(destPath)
	if err == nil {
		startByte = info.Size()
	}

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	if startByte > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", startByte))
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable {
		return nil // Assume done
	}

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusPartialContent {
		if startByte > 0 && resp.StatusCode == http.StatusOK {
			startByte = 0
		} else {
			return fmt.Errorf("server returned status: %s", resp.Status)
		}
	}

	// Open file
	flags := os.O_WRONLY | os.O_CREATE
	if startByte > 0 && resp.StatusCode == http.StatusPartialContent {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}

	out, err := os.OpenFile(destPath, flags, 0644)
	if err != nil {
		return fmt.Errorf("failed to open file: %w", err)
	}
	defer out.Close()

	// Wrap body with Speed Monitor
	// Note: We don't cancel the main context, just return error from Read to break copy
	monitor := &SpeedMonitorReader{
		Reader:    resp.Body,
		StartTime: time.Now(),
		LastCheck: time.Now(),
		MinSpeed:  minSpeed,
	}

	_, err = io.Copy(out, monitor)
	if err != nil {
		return err // Could be speed error or network error
	}

	// [FIX] Verify file existence and size to prevent hallucinations
	info, err = os.Stat(destPath)
	if err != nil {
		return fmt.Errorf("download completed but verification failed: %w", err)
	}
	if info.Size() == 0 {
		return fmt.Errorf("download completed but file is empty (0 bytes)")
	}

	return nil
}
