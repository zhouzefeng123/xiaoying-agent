package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	http "github.com/bogdanfinn/fhttp"
	tls_client "github.com/bogdanfinn/tls-client"
	"github.com/bogdanfinn/tls-client/profiles"

	md "github.com/JohannesKaufmann/html-to-markdown"
	"github.com/PuerkitoBio/goquery"
	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/permission"
)

const (
	FetchToolName        = "fetch"
	fetchToolDescription = `Fetches content from a URL.
Usage:
- url: The URL to fetch.
- format: Output format (text, markdown, html).
- timeout: Optional timeout in seconds (max 120).
- save_path: Optional path to save the content to a file (if set, content is streamed to file).
- summary: Optional. If true, return concise core summary instead of full content.
- focus: Optional. Focus hint for summary extraction.
Features:
- Handles HTTP redirects.
- Converts HTML to Markdown (if format=markdown).
- Streams large files if save_path is provided.
- Auto-summarizes very large HTML responses to reduce context pollution.
Limitations:
- Max size 20MB (for in-memory fetch only).
- No auth/cookies.
`
)

type FetchParams struct {
	URL      string `json:"url"`
	Format   string `json:"format"`
	Timeout  int    `json:"timeout,omitempty"`
	SavePath string `json:"save_path,omitempty"`
	Summary  *bool  `json:"summary,omitempty"`
	Focus    string `json:"focus,omitempty"`
}

type FetchPermissionsParams struct {
	URL      string `json:"url"`
	Format   string `json:"format"`
	Timeout  int    `json:"timeout,omitempty"`
	SavePath string `json:"save_path,omitempty"`
	Summary  *bool  `json:"summary,omitempty"`
	Focus    string `json:"focus,omitempty"`
}

type fetchTool struct {
	client      tls_client.HttpClient
	permissions permission.Service
}

func NewFetchTool(permissions permission.Service) BaseTool {
	options := []tls_client.HttpClientOption{
		tls_client.WithTimeoutSeconds(30),
		tls_client.WithClientProfile(profiles.Chrome_120),
		// Do not automatically follow redirects unconditionally to avoid loops,
		// but let tls_client handle standard ones. We can configure cookie jar later.
	}
	client, _ := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)

	return &fetchTool{
		client:      client,
		permissions: permissions,
	}
}

func (t *fetchTool) Info() ToolInfo {
	return ToolInfo{
		Name:        FetchToolName,
		Description: fetchToolDescription,
		Parameters: map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The URL to fetch content from",
			},
			"format": map[string]any{
				"type":        "string",
				"description": "The format to return the content in (text, markdown, or html)",
				"enum":        []string{"text", "markdown", "html"},
			},
			"timeout": map[string]any{
				"type":        "number",
				"description": "Optional timeout in seconds (max 120)",
			},
			"save_path": map[string]any{
				"type":        "string",
				"description": "Optional absolute or relative path to save full content to a file",
			},
			"summary": map[string]any{
				"type":        "boolean",
				"description": "Optional. If true, return concise core summary instead of full content",
			},
			"focus": map[string]any{
				"type":        "string",
				"description": "Optional. Focus hint for summary, e.g. specific facts or topics",
			},
		},
		Required: []string{"url", "format"},
	}
}

// Helper to remove tags with regex
func removeTag(content, tagName string) string {
	// Robust tag removal handling attributes (case-insensitive)
	// Matches <tagName ...> ... </tagName> OR <tagName ... />
	// Note: Nested tags of same type might still be tricky with regex, but for script/style/svg it's usually fine.

	// Use regexp to match: <tagName(\s[^>]*)?>.*?</tagName>
	// We construct regex on the fly. Since tool calls aren't high-frequency, this is acceptable.

	// Import "regexp" at file level is needed. But wait, I can't add imports easily with SearchReplace if they are missing.
	// Check imports: "context", "encoding/json", "fmt", "io", "net/http", "os", "path/filepath", "strings", "time".
	// "regexp" is MISSING.
	// I must add it to imports first. Or implement a state machine parser.

	// Let's implement a simple state machine parser for robustness without adding "regexp" dependency to avoid import errors.

	lowerContent := strings.ToLower(content)
	lowerTagName := strings.ToLower(tagName)
	var sb strings.Builder
	cursor := 0
	length := len(content)

	for cursor < length {
		// Find next start tag: <tagName
		// We look for "<" + tagName + (space or >)

		startIdx := -1

		// Search loop
		searchPos := cursor
		for {
			idx := strings.Index(lowerContent[searchPos:], "<"+lowerTagName)
			if idx == -1 {
				break
			}
			idx += searchPos

			// Check what comes after tagName
			afterTag := idx + 1 + len(lowerTagName)
			if afterTag >= length {
				break
			}
			char := lowerContent[afterTag]
			if char == ' ' || char == '>' || char == '/' || char == '\t' || char == '\n' {
				startIdx = idx
				break
			}
			// False positive (e.g. <scripture)
			searchPos = idx + 1
		}

		if startIdx == -1 {
			sb.WriteString(content[cursor:])
			break
		}

		// Append everything before the tag
		sb.WriteString(content[cursor:startIdx])

		// Find the end of this tag's opening block ">"
		openTagEnd := strings.Index(content[startIdx:], ">")
		if openTagEnd == -1 {
			// Malformed, just skip this <
			sb.WriteString("<")
			cursor = startIdx + 1
			continue
		}
		openTagEnd += startIdx

		// Check if self-closing "/>"
		if content[openTagEnd-1] == '/' {
			cursor = openTagEnd + 1
			continue
		}

		// Find closing tag </tagName>
		closeTagStr := "</" + lowerTagName + ">"
		closeIdx := strings.Index(lowerContent[openTagEnd:], closeTagStr)

		if closeIdx == -1 {
			// No closing tag found? Maybe strip just the opening tag?
			// Or maybe the whole rest is script?
			// Let's assume it ends at the next same tag start or end of file?
			// Safest is to just skip the opening tag and treat content as text (bad for script)
			// OR remove everything until end.
			// For script/style, usually we want to remove until end if missing.
			cursor = openTagEnd + 1
			continue
		}
		closeIdx += openTagEnd

		// Advance cursor past the closing tag
		cursor = closeIdx + len(closeTagStr)
	}

	return sb.String()
}

func (t *fetchTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params FetchParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse("Failed to parse fetch parameters: " + err.Error()), nil
	}

	if params.URL == "" {
		return NewTextErrorResponse("URL parameter is required"), nil
	}

	format := strings.ToLower(params.Format)
	if format != "text" && format != "markdown" && format != "html" {
		return NewTextErrorResponse("Format must be one of: text, markdown, html"), nil
	}

	if !strings.HasPrefix(params.URL, "http://") && !strings.HasPrefix(params.URL, "https://") {
		return NewTextErrorResponse("URL must start with http:// or https://"), nil
	}

	sessionID, messageID := GetContextValues(ctx)
	if sessionID == "" || messageID == "" {
		return ToolResponse{}, fmt.Errorf("session ID and message ID are required for creating a new file")
	}

	p := t.permissions.Request(
		permission.CreatePermissionRequest{
			SessionID:   sessionID,
			Path:        config.WorkingDirectory(),
			ToolName:    FetchToolName,
			Action:      "fetch",
			Description: fmt.Sprintf("Fetch content from URL: %s", params.URL),
			Params:      FetchPermissionsParams(params),
		},
	)

	if !p {
		return ToolResponse{}, permission.ErrorPermissionDenied
	}

	client := t.client

	// If a custom timeout is requested, we need to create a clone/new client
	// because tls_client instances hold persistent connection pools.
	if params.Timeout > 0 {
		maxTimeout := 300 // 5 minutes
		if params.Timeout > maxTimeout {
			params.Timeout = maxTimeout
		}

		options := []tls_client.HttpClientOption{
			tls_client.WithTimeoutSeconds(params.Timeout),
			tls_client.WithClientProfile(profiles.Chrome_120),
		}

		customClient, err := tls_client.NewHttpClient(tls_client.NewNoopLogger(), options...)
		if err == nil {
			client = customClient
		}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", params.URL, nil)
	if err != nil {
		return ToolResponse{}, fmt.Errorf("failed to create request: %w", err)
	}

	// Use a modern browser User-Agent and headers to avoid blocking
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,image/avif,image/webp,image/apng,*/*;q=0.8,application/signed-exchange;v=b3;q=0.7")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Referer", "https://www.google.com/")
	req.Header.Set("Cache-Control", "max-age=0")
	req.Header.Set("Connection", "keep-alive")
	req.Header.Set("Sec-Ch-Ua", `"Not_A Brand";v="8", "Chromium";v="120", "Google Chrome";v="120"`)
	req.Header.Set("Sec-Ch-Ua-Mobile", "?0")
	req.Header.Set("Sec-Ch-Ua-Platform", `"Windows"`)
	req.Header.Set("Sec-Fetch-Dest", "document")
	req.Header.Set("Sec-Fetch-Mode", "navigate")
	req.Header.Set("Sec-Fetch-Site", "none")
	req.Header.Set("Sec-Fetch-User", "?1")
	req.Header.Set("Upgrade-Insecure-Requests", "1")

	resp, err := client.Do(req)
	if err != nil {
		return ToolResponse{}, fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return NewTextErrorResponse(fmt.Sprintf("Request failed with status code: %d", resp.StatusCode)), nil
	}

	// Progress Reporting
	contentLength := resp.ContentLength
	streamCallback, _ := ctx.Value(StreamOutputContextKey).(StreamOutputCallback)

	reader := &ProgressReader{
		Reader:         resp.Body,
		Total:          contentLength,
		Callback:       streamCallback,
		ReportInterval: 500 * time.Millisecond, // Report every 500ms
		LastReport:     time.Now(),
	}

	// Handle Save to File
	if params.SavePath != "" {
		// Validate path (basic check, permissions already handled)
		if !filepath.IsAbs(params.SavePath) {
			params.SavePath = filepath.Join(config.WorkingDirectory(), params.SavePath)
		}

		// Create file
		out, err := os.Create(params.SavePath)
		if err != nil {
			return NewTextErrorResponse("Failed to create file: " + err.Error()), nil
		}
		defer out.Close()

		// Stream to file
		_, err = io.Copy(out, reader)
		if err != nil {
			return NewTextErrorResponse("Failed to save content to file: " + err.Error()), nil
		}

		return NewTextResponse(fmt.Sprintf("Successfully saved content to %s", params.SavePath)), nil
	}

	maxSize := int64(20 * 1024 * 1024) // 20MB
	body, err := io.ReadAll(io.LimitReader(reader, maxSize))
	if err != nil {
		return NewTextErrorResponse("Failed to read response body: " + err.Error()), nil
	}

	content := string(body)
	contentType := resp.Header.Get("Content-Type")

	// OPTIMIZATION: Tool-side Compression for HTML content
	// DISABLED by User Request: "Cancel all token saving methods..."
	/*
		// Only clean up if content is extremely large (> 50000 chars)
		if (strings.Contains(contentType, "text/html") || strings.Contains(content, "<html")) && len(content) > 50000 {
			// Simple cleaning logic (similar to agent middleware)
			// 1. Remove Scripts
			content = removeTag(content, "script")
			// 2. Remove Styles
			content = removeTag(content, "style")
			// 3. Remove SVG paths (often huge)
			content = removeTag(content, "svg")
			// 4. Remove Navigation, Footer, Header, Iframe (Noise)
			content = removeTag(content, "nav")
			content = removeTag(content, "footer")
			content = removeTag(content, "header")
			content = removeTag(content, "iframe")
			content = removeTag(content, "noscript")

			// If it's still huge (> 200000 chars) and format is NOT markdown, force partial markdown conversion
			// or truncation to avoid context explosion.
			if len(content) > 200000 && format != "markdown" {
				converter := md.NewConverter("", true, nil)
				if markdown, err := converter.ConvertString(content); err == nil {
					content = "[System: Content converted to Markdown for brevity]\n" + markdown
					format = "markdown" // Switch format to hint the agent
				}
			}
		}
	*/

	// Truncation disabled by user request: "Cancel all token saving methods..."
	// We allow full content pass-through, relying on the Agent's global 64k token circuit breaker.
	/*
		const MaxContentChars = 50000
		if len(content) > MaxContentChars {
			originalLen := len(content)
			content = content[:MaxContentChars]
			warning := fmt.Sprintf("\n\n... [Content truncated by system. Original size: %d chars. Shown: %d chars.]\n[System Advice: This content is too large to display fully. If you need the full content, please use the 'fetch' tool again with the 'save_path' parameter to save it to a file, and then use 'read_tool' with 'offset' and 'limit' to read it in chunks, or use 'grep' to search for specific information.]", originalLen, MaxContentChars)
			content += warning
		}
	*/

	isHTML := strings.Contains(contentType, "text/html") || strings.Contains(strings.ToLower(content), "<html")
	shouldSummarize := false
	if params.Summary != nil {
		shouldSummarize = *params.Summary
	} else if params.SavePath == "" && isHTML && len(content) > 12000 {
		// Disable auto-summary for "smart" agent unless explicit
		// shouldSummarize = true
	}

	if shouldSummarize {
		summary := buildFetchCoreSummary(content, contentType, params.Focus)
		header := "Core summary only to reduce context pollution. Use save_path for full content."
		if params.Focus != "" {
			header += "\nFocus: " + params.Focus
		}
		return NewTextResponse(header + "\n\n" + summary), nil
	}

	switch format {
	case "text":
		if strings.Contains(contentType, "text/html") {
			text, err := extractTextFromHTML(content)
			if err != nil {
				return NewTextErrorResponse("Failed to extract text from HTML: " + err.Error()), nil
			}
			return NewTextResponse(text), nil
		}
		return NewTextResponse(content), nil

	case "markdown":
		if strings.Contains(contentType, "text/html") {
			markdown, err := convertHTMLToMarkdown(content)
			if err != nil {
				return NewTextErrorResponse("Failed to convert HTML to Markdown: " + err.Error()), nil
			}
			return NewTextResponse(markdown), nil
		}

		return NewTextResponse("```\n" + content + "\n```"), nil

	case "html":
		return NewTextResponse(content), nil

	default:
		return NewTextResponse(content), nil
	}
}

func extractTextFromHTML(html string) (string, error) {
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		return "", err
	}

	// Optimization: Remove noise to save tokens
	// We remove script, style, iframe, noscript, and also layout elements like nav, footer, header
	// which usually contain boilerplate text not relevant to the main content.
	doc.Find("script, style, iframe, noscript, nav, footer, header, svg, link, meta").Remove()

	text := doc.Text()
	// Normalize whitespace: replace multiple spaces/newlines with single space
	text = strings.Join(strings.Fields(text), " ")

	return text, nil
}

func convertHTMLToMarkdown(html string) (string, error) {
	converter := md.NewConverter("", true, nil)

	markdown, err := converter.ConvertString(html)
	if err != nil {
		return "", err
	}

	return markdown, nil
}

func buildFetchCoreSummary(content, contentType, focus string) string {
	if strings.Contains(contentType, "text/html") || strings.Contains(strings.ToLower(content), "<html") {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(content))
		if err == nil {
			doc.Find("script, style, iframe, noscript, nav, footer, header, svg, link, meta").Remove()

			title := strings.TrimSpace(doc.Find("title").First().Text())
			h1 := strings.TrimSpace(doc.Find("h1").First().Text())

			var headings []string
			doc.Find("h2, h3").EachWithBreak(func(i int, s *goquery.Selection) bool {
				if len(headings) >= 8 {
					return false
				}
				t := strings.TrimSpace(s.Text())
				if t != "" {
					headings = append(headings, t)
				}
				return true
			})

			var paragraphs []string
			doc.Find("article p, main p, p").EachWithBreak(func(i int, s *goquery.Selection) bool {
				if len(paragraphs) >= 6 {
					return false
				}
				p := strings.Join(strings.Fields(strings.TrimSpace(s.Text())), " ")
				if p == "" {
					return true
				}
				if len(p) > 320 {
					p = p[:320] + "..."
				}
				paragraphs = append(paragraphs, p)
				return true
			})

			var sb strings.Builder
			if title != "" {
				sb.WriteString("Title: " + title + "\n")
			}
			if h1 != "" && h1 != title {
				sb.WriteString("Main topic: " + h1 + "\n")
			}
			if focus != "" {
				sb.WriteString("Requested focus: " + focus + "\n")
			}
			if len(headings) > 0 {
				sb.WriteString("\nKey sections:\n")
				for _, h := range headings {
					sb.WriteString("- " + h + "\n")
				}
			}
			if len(paragraphs) > 0 {
				sb.WriteString("\nCore points:\n")
				for _, p := range paragraphs {
					sb.WriteString("- " + p + "\n")
				}
			}
			if sb.Len() > 0 {
				return sb.String()
			}
		}
	}

	lines := strings.Split(content, "\n")
	var cleaned []string
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.Join(strings.Fields(line), " ")
		cleaned = append(cleaned, line)
		if len(cleaned) >= 25 {
			break
		}
	}
	if len(cleaned) == 0 {
		return "(no meaningful content extracted)"
	}

	var sb strings.Builder
	if focus != "" {
		sb.WriteString("Requested focus: " + focus + "\n\n")
	}
	sb.WriteString("Core points:\n")
	for _, line := range cleaned {
		if len(line) > 320 {
			line = line[:320] + "..."
		}
		sb.WriteString("- " + line + "\n")
	}
	return sb.String()
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}

// ProgressReader wraps an io.Reader to report progress
type ProgressReader struct {
	Reader         io.Reader
	Total          int64
	Current        int64
	Callback       StreamOutputCallback
	ReportInterval time.Duration
	LastReport     time.Time
}

func (pr *ProgressReader) Read(p []byte) (int, error) {
	n, err := pr.Reader.Read(p)
	pr.Current += int64(n)

	if pr.Callback != nil && time.Since(pr.LastReport) > pr.ReportInterval {
		pr.LastReport = time.Now()
		if pr.Total > 0 {
			percent := float64(pr.Current) / float64(pr.Total) * 100
			pr.Callback(fmt.Sprintf("Downloading: %.1f%% (%s / %s)", percent, formatBytes(pr.Current), formatBytes(pr.Total)))
		} else {
			pr.Callback(fmt.Sprintf("Downloading: %s", formatBytes(pr.Current)))
		}
	}

	return n, err
}
