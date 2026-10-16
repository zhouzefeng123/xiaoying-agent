package mcp

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/version"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

// Service manages MCP clients and connections
type Service interface {
	// GetClient returns an active client for the given server name.
	// It initializes the client if it's not already running.
	GetClient(ctx context.Context, name string) (Client, error)
	// CloseAll closes all active clients.
	CloseAll()
	// GetTools returns all tools from all configured servers.
	GetTools(ctx context.Context) (map[string][]mcp.Tool, error)
}

// Client wraps the mcp-go client
type Client interface {
	Initialize(ctx context.Context, request mcp.InitializeRequest) (*mcp.InitializeResult, error)
	ListTools(ctx context.Context, request mcp.ListToolsRequest) (*mcp.ListToolsResult, error)
	CallTool(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)
	Close() error
}

type service struct {
	clients              map[string]Client
	mu                   sync.RWMutex
	mcpConfigPath        string
	lastMCPConfigModTime time.Time
}

func NewService() Service {
	return &service{
		clients:       make(map[string]Client),
		mcpConfigPath: config.MCPConfigPath(),
	}
}

func (s *service) GetClient(ctx context.Context, name string) (Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if c, ok := s.clients[name]; ok {
		// TODO: Check if client is still alive?
		return c, nil
	}

	cfg := config.Get()
	serverConfig, ok := cfg.MCPServers[name]
	if !ok {
		return nil, fmt.Errorf("mcp server config not found: %s", name)
	}

	var c Client
	var err error

	switch serverConfig.Type {
	case config.MCPStdio:
		cmdPath := serverConfig.Command
		// Check if command exists in McpToolsDir
		if cfg.McpToolsDir != "" {
			possiblePath := filepath.Join(cfg.McpToolsDir, cmdPath)
			if _, err := os.Stat(possiblePath); err == nil {
				cmdPath = possiblePath
			} else if runtime.GOOS == "windows" {
				// Try with .exe on Windows
				if filepath.Ext(cmdPath) == "" {
					if _, err := os.Stat(possiblePath + ".exe"); err == nil {
						cmdPath = possiblePath + ".exe"
					}
				}
			}
		}

		var env []string
		for k, v := range serverConfig.Env {
			env = append(env, fmt.Sprintf("%s=%s", k, v))
		}

		c, err = client.NewStdioMCPClient(
			cmdPath,
			env,
			serverConfig.Args...,
		)
	case config.MCPSse:
		c, err = client.NewSSEMCPClient(
			serverConfig.URL,
			client.WithHeaders(serverConfig.Headers),
		)
	default:
		return nil, fmt.Errorf("unsupported mcp type: %s", serverConfig.Type)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to create mcp client: %w", err)
	}

	if starter, ok := c.(interface{ Start(context.Context) error }); ok {
		startErr := make(chan error, 1)
		go func() {
			startErr <- starter.Start(context.Background())
		}()

		timer := time.NewTimer(10 * time.Second)
		defer timer.Stop()
		select {
		case err := <-startErr:
			if err != nil {
				c.Close()
				return nil, fmt.Errorf("failed to start mcp client: %w", err)
			}
		case <-timer.C:
			c.Close()
			return nil, fmt.Errorf("failed to start mcp client: timed out waiting for SSE endpoint")
		case <-ctx.Done():
			c.Close()
			return nil, fmt.Errorf("failed to start mcp client: %w", ctx.Err())
		}
	}

	// Initialize the client
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{
		Name:    "OpenCode",
		Version: version.Version,
	}

	// Set a timeout for initialization
	initCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	_, err = c.Initialize(initCtx, initRequest)
	if err != nil {
		c.Close()
		return nil, fmt.Errorf("failed to initialize mcp client: %w", err)
	}

	s.clients[name] = c
	return c, nil
}

func (s *service) CloseAll() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for name, c := range s.clients {
		if err := c.Close(); err != nil {
			logging.Error("Failed to close mcp client", "name", name, "error", err)
		}
	}
	s.clients = make(map[string]Client)
}

func (s *service) maybeReloadMCPConfig() {
	// Always re-resolve path from current runtime config/env to avoid stale path
	// captured during early startup (before config is fully loaded).
	path := config.MCPConfigPath()
	if path == "" {
		path = s.mcpConfigPath
	}
	s.mcpConfigPath = path

	fi, err := os.Stat(path)
	if err != nil {
		return
	}
	mod := fi.ModTime()

	s.mu.RLock()
	last := s.lastMCPConfigModTime
	s.mu.RUnlock()
	if !last.IsZero() && !mod.After(last) {
		return
	}

	if err := config.ReloadMCPServers(); err != nil {
		logging.Warn("failed to reload MCP config", "path", path, "error", err)
		return
	}

	s.mu.Lock()
	for name, c := range s.clients {
		if err := c.Close(); err != nil {
			logging.Warn("failed to close stale MCP client", "name", name, "error", err)
		}
		delete(s.clients, name)
	}
	s.lastMCPConfigModTime = mod
	s.mu.Unlock()

	logging.Info("Hot reloaded MCP config", "path", path)
}

func (s *service) GetTools(ctx context.Context) (map[string][]mcp.Tool, error) {
	s.maybeReloadMCPConfig()
	cfg := config.Get()
	result := make(map[string][]mcp.Tool)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for name, serverConfig := range cfg.MCPServers {
		// Skip disabled MCP servers
		if serverConfig.Disabled {
			continue
		}

		wg.Add(1)
		go func(serverName string) {
			defer wg.Done()

			// Use a shorter timeout for initialization/connection if possible,
			// but ListTools needs its own context.
			// Note: GetClient might take time if it needs to start the server.

			// We use the parent context but with a reasonable timeout for the whole operation per server
			serverCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
			defer cancel()

			c, err := s.GetClient(serverCtx, serverName)
			if err != nil {
				logging.Error("Failed to get mcp client", "name", serverName, "error", err)
				return
			}

			toolsResp, err := c.ListTools(serverCtx, mcp.ListToolsRequest{})
			if err != nil {
				logging.Error("Failed to list tools", "name", serverName, "error", err)
				return
			}

			mu.Lock()
			result[serverName] = toolsResp.Tools
			mu.Unlock()
		}(name)
	}

	wg.Wait()
	return result, nil
}
