package chrome

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Server implements a Chrome DevTools MCP server
type Server struct {
	*server.MCPServer
	ctx        context.Context
	cancel     context.CancelFunc
	allocCtx   context.Context
	allocCancel context.CancelFunc
	mu          sync.Mutex
	started     bool
}

func NewServer() *Server {
	s := &Server{
		MCPServer: server.NewMCPServer(
			"chrome-mcp",
			"1.0.0",
		),
	}

	// Register tools
	s.AddTool(mcp.Tool{
		Name:        "navigate",
		Description: "Navigate to a URL",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"url": map[string]any{
					"type":        "string",
					"description": "The URL to navigate to",
				},
			},
			Required: []string{"url"},
		},
	}, s.navigate)

	s.AddTool(mcp.Tool{
		Name:        "screenshot",
		Description: "Take a screenshot of the current page",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"selector": map[string]any{
					"type":        "string",
					"description": "Optional CSS selector to capture specific element. If omitted, captures full page.",
				},
			},
		},
	}, s.screenshot)

	s.AddTool(mcp.Tool{
		Name:        "evaluate",
		Description: "Evaluate JavaScript on the current page",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"expression": map[string]any{
					"type":        "string",
					"description": "JavaScript expression to evaluate",
				},
			},
			Required: []string{"expression"},
		},
	}, s.evaluate)

	s.AddTool(mcp.Tool{
		Name:        "click",
		Description: "Click an element",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"selector": map[string]any{
					"type":        "string",
					"description": "CSS selector of the element to click",
				},
			},
			Required: []string{"selector"},
		},
	}, s.click)

	s.AddTool(mcp.Tool{
		Name:        "fill",
		Description: "Fill an input field",
		InputSchema: mcp.ToolInputSchema{
			Type: "object",
			Properties: map[string]any{
				"selector": map[string]any{
					"type":        "string",
					"description": "CSS selector of the input element",
				},
				"value": map[string]any{
					"type":        "string",
					"description": "Value to type",
				},
			},
			Required: []string{"selector", "value"},
		},
	}, s.fill)

	return s
}

// Start starts the chrome instance and the MCP server
func (s *Server) Start() {
	// Initialize chrome with real-user emulation
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.Flag("headless", false), // Must be false for anti-detection
		chromedp.Flag("disable-gpu", false),
		chromedp.Flag("enable-automation", false), // Hide "Chrome is being controlled by automated test software"
		chromedp.Flag("disable-extensions", false),
		// Set a real User-Agent
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"),
		// Additional flags to mimic a real browser
		chromedp.Flag("disable-blink-features", "AutomationControlled"), // Key flag to hide webdriver
		chromedp.Flag("window-size", "1920,1080"),
		chromedp.Flag("start-maximized", true),
	)

	s.allocCtx, s.allocCancel = chromedp.NewExecAllocator(context.Background(), opts...)
	s.ctx, s.cancel = chromedp.NewContext(s.allocCtx)

	// Note: We do NOT explicitly run chromedp.Run(s.ctx) here.
	// This ensures the browser is only launched when the first tool (like navigate) is actually called.
	// This implements the "lazy loading" behavior requested by the user.

	// Start MCP server via Stdio
	if err := server.ServeStdio(s.MCPServer); err != nil {
		fmt.Fprintf(os.Stderr, "Server error: %v\n", err)
	}
}

func (s *Server) ensureBrowser() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ctx == nil {
		return fmt.Errorf("browser context not initialized")
	}

	if s.started {
		return nil
	}

	// Lazy start the browser by running a dummy task on the main context
	var res int
	if err := chromedp.Run(s.ctx, chromedp.Evaluate("1+1", &res)); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to lazy start browser: %v\n", err)
		return fmt.Errorf("failed to start browser: %w", err)
	}

	s.started = true
	return nil
}

func (s *Server) navigate(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.ensureBrowser(); err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Type: "text",
					Text: err.Error(),
				},
			},
			IsError: true,
		}, nil
	}

	url, ok := request.Params.Arguments["url"].(string)
	if !ok {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Type: "text",
					Text: "url must be a string",
				},
			},
			IsError: true,
		}, nil
	}

	// Set timeout for navigation
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()

	if err := chromedp.Run(ctx, chromedp.Navigate(url)); err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Type: "text",
					Text: fmt.Sprintf("failed to navigate: %v", err),
				},
			},
			IsError: true,
		}, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Navigated to %s", url),
			},
		},
	}, nil
}

func (s *Server) screenshot(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.ensureBrowser(); err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Type: "text",
					Text: err.Error(),
				},
			},
			IsError: true,
		}, nil
	}

	selector, _ := request.Params.Arguments["selector"].(string)

	var buf []byte
	var err error

	// Set timeout
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	if selector != "" {
		err = chromedp.Run(ctx, chromedp.Screenshot(selector, &buf, chromedp.NodeVisible))
	} else {
		err = chromedp.Run(ctx, chromedp.FullScreenshot(&buf, 90))
	}

	if err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Type: "text",
					Text: fmt.Sprintf("failed to take screenshot: %v", err),
				},
			},
			IsError: true,
		}, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: "Screenshot taken (image return not fully implemented in this MVP)",
			},
		},
	}, nil
}

func (s *Server) evaluate(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.ensureBrowser(); err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Type: "text",
					Text: err.Error(),
				},
			},
			IsError: true,
		}, nil
	}

	expression, ok := request.Params.Arguments["expression"].(string)
	if !ok {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Type: "text",
					Text: "expression must be a string",
				},
			},
			IsError: true,
		}, nil
	}

	var result any
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()

	if err := chromedp.Run(ctx, chromedp.Evaluate(expression, &result)); err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Type: "text",
					Text: fmt.Sprintf("evaluation failed: %v", err),
				},
			},
			IsError: true,
		}, nil
	}

	resBytes, _ := json.Marshal(result)
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: string(resBytes),
			},
		},
	}, nil
}

func (s *Server) click(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.ensureBrowser(); err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Type: "text",
					Text: err.Error(),
				},
			},
			IsError: true,
		}, nil
	}

	selector, ok := request.Params.Arguments["selector"].(string)
	if !ok {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Type: "text",
					Text: "selector must be a string",
				},
			},
			IsError: true,
		}, nil
	}

	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()

	if err := chromedp.Run(ctx, chromedp.Click(selector)); err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Type: "text",
					Text: fmt.Sprintf("click failed: %v", err),
				},
			},
			IsError: true,
		}, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Clicked %s", selector),
			},
		},
	}, nil
}

func (s *Server) fill(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	if err := s.ensureBrowser(); err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Type: "text",
					Text: err.Error(),
				},
			},
			IsError: true,
		}, nil
	}

	selector, ok := request.Params.Arguments["selector"].(string)
	if !ok {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Type: "text",
					Text: "selector must be a string",
				},
			},
			IsError: true,
		}, nil
	}
	value, ok := request.Params.Arguments["value"].(string)
	if !ok {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Type: "text",
					Text: "value must be a string",
				},
			},
			IsError: true,
		}, nil
	}

	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	defer cancel()

	if err := chromedp.Run(ctx, chromedp.SendKeys(selector, value)); err != nil {
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{
					Type: "text",
					Text: fmt.Sprintf("fill failed: %v", err),
				},
			},
			IsError: true,
		}, nil
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: fmt.Sprintf("Filled %s with value", selector),
			},
		},
	}, nil
}
