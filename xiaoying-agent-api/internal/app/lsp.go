package app

import (
	"context"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/lsp"
	"github.com/auramate/auramate/internal/lsp/watcher"
)

type pendingLSPClient struct {
	name   string
	config config.LSPConfig
}

func (app *App) initLSPClients(ctx context.Context) {
	// Lazy load: Do nothing on startup.
	// LSP clients will be started when Coder Agent is activated or on explicit user request.
	logging.Info("LSP clients lazy initialization enabled. Waiting for trigger.")
}

// StartLSPClients manually triggers the start of all configured LSP clients
func (app *App) StartLSPClients(ctx context.Context) {
	baseCtx := app.lspContext(ctx)
	cfg := config.Get()
	effectiveLSPConfigs := config.EffectiveLSPConfigs(cfg)
	pending := make([]pendingLSPClient, 0, len(effectiveLSPConfigs))

	for name, clientConfig := range effectiveLSPConfigs {
		if clientConfig.Disabled {
			continue
		}

		allowed, status := app.LSPRegistry.TryBeginStart(name, clientConfig.Command)
		if !allowed {
			logging.Debug("Skipping LSP startup trigger", "name", name, "phase", status.Phase, "message", status.Message)
			continue
		}
		pending = append(pending, pendingLSPClient{name: name, config: clientConfig})
	}

	for _, client := range pending {
		// Start each client initialization in its own goroutine
		go app.createAndStartLSPClient(baseCtx, client.name, client.config.Command, client.config.Args...)
	}
	logging.Info("LSP clients startup triggered", "count", len(pending))
}

// StartLSPClientsForLanguages starts only the configured LSP clients needed for the
// languages currently being handled.
func (app *App) StartLSPClientsForLanguages(ctx context.Context, languages []string) {
	baseCtx := app.lspContext(ctx)
	cfg := config.Get()
	effectiveLSPConfigs := config.EffectiveLSPConfigs(cfg)
	pending := make([]pendingLSPClient, 0, len(languages))
	seen := map[string]bool{}

	for _, language := range languages {
		name := lsp.ServerKeyForLanguage(language)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true

		clientConfig, ok := effectiveLSPConfigs[name]
		if !ok || clientConfig.Disabled {
			continue
		}

		allowed, status := app.LSPRegistry.TryBeginStart(name, clientConfig.Command)
		if !allowed {
			logging.Debug("Skipping language-scoped LSP startup trigger", "name", name, "phase", status.Phase, "message", status.Message)
			continue
		}
		pending = append(pending, pendingLSPClient{name: name, config: clientConfig})
	}

	for _, client := range pending {
		go app.createAndStartLSPClient(baseCtx, client.name, client.config.Command, client.config.Args...)
	}
	logging.Info("Language-scoped LSP clients startup triggered", "languages", languages, "count", len(pending))
}

func (app *App) lspContext(requestCtx context.Context) context.Context {
	if app.lspRootCtx != nil {
		return app.lspRootCtx
	}
	if requestCtx != nil {
		return requestCtx
	}
	return context.Background()
}

// createAndStartLSPClient creates a new LSP client, initializes it, and starts its workspace watcher
func (app *App) createAndStartLSPClient(ctx context.Context, name string, command string, args ...string) {
	if !lsp.IsServerInstalled(ctx, name, command, args) {
		app.LSPRegistry.MarkInstalling(name, command)
	}

	resolvedCommand, resolvedArgs, err := lsp.ResolveServerCommand(ctx, name, command, args)
	if err != nil {
		status := app.LSPRegistry.MarkFailure(name, command, err)
		logging.Error("Failed to prepare LSP client command", "name", name, "error", err)
		logging.Warn("LSP startup moved into backoff", "name", name, "nextRetryAt", status.NextRetryAt)
		return
	}
	app.LSPRegistry.MarkStarting(name, command, resolvedCommand)

	// Create a specific context for initialization with a timeout
	logging.Info("Creating LSP client", "name", name, "command", resolvedCommand, "args", resolvedArgs)

	// Create the LSP client
	lspClient, err := lsp.NewClient(ctx, resolvedCommand, resolvedArgs...)
	if err != nil {
		status := app.LSPRegistry.MarkFailure(name, resolvedCommand, err)
		logging.Error("Failed to create LSP client for", name, err)
		logging.Warn("LSP startup moved into backoff", "name", name, "nextRetryAt", status.NextRetryAt)
		return
	}

	// Create a longer timeout for initialization (some servers take time to start)
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Initialize with the initialization context
	_, err = lspClient.InitializeLSPClient(initCtx, config.WorkingDirectory())
	if err != nil {
		status := app.LSPRegistry.MarkFailure(name, resolvedCommand, err)
		logging.Error("Initialize failed", "name", name, "error", err)
		// Clean up the client to prevent resource leaks
		lspClient.Close()
		logging.Warn("LSP startup moved into backoff", "name", name, "nextRetryAt", status.NextRetryAt)
		return
	}

	// Wait for the server to be ready
	if err := lspClient.WaitForServerReady(initCtx); err != nil {
		status := app.LSPRegistry.MarkFailure(name, resolvedCommand, err)
		logging.Error("Server failed to become ready", "name", name, "error", err)
		// We'll continue anyway, as some functionality might still work
		lspClient.SetServerState(lsp.StateError)
		logging.Warn("LSP server is running but not ready", "name", name, "nextRetryAt", status.NextRetryAt)
	} else {
		logging.Info("LSP server is ready", "name", name)
		lspClient.SetServerState(lsp.StateReady)
		app.LSPRegistry.MarkReady(name, command, resolvedCommand)
	}

	logging.Info("LSP client initialized", "name", name)

	// Create a child context that can be canceled when the app is shutting down
	watchCtx, cancelFunc := context.WithCancel(ctx)

	// Create a context with the server name for better identification
	watchCtx = context.WithValue(watchCtx, "serverName", name)

	// Create the workspace watcher
	workspaceWatcher := watcher.NewWorkspaceWatcher(lspClient)

	// Store the cancel function to be called during cleanup
	app.cancelFuncsMutex.Lock()
	app.watcherCancelFuncs = append(app.watcherCancelFuncs, cancelFunc)
	app.cancelFuncsMutex.Unlock()

	// Add the watcher to a WaitGroup to track active goroutines
	app.watcherWG.Add(1)

	app.LSPRegistry.Set(name, lspClient)

	go app.runWorkspaceWatcher(watchCtx, name, workspaceWatcher)
}

// runWorkspaceWatcher executes the workspace watcher for an LSP client
func (app *App) runWorkspaceWatcher(ctx context.Context, name string, workspaceWatcher *watcher.WorkspaceWatcher) {
	defer app.watcherWG.Done()
	defer logging.RecoverPanic("LSP-"+name, func() {
		// Try to restart the client
		app.restartLSPClient(ctx, name)
	})

	workspaceWatcher.WatchWorkspace(ctx, config.WorkingDirectory())
	logging.Info("Workspace watcher stopped", "client", name)
}

// restartLSPClient attempts to restart a crashed or failed LSP client
func (app *App) restartLSPClient(ctx context.Context, name string) {
	// Get the original configuration
	cfg := config.Get()
	clientConfig, exists := config.EffectiveLSPConfigForLanguage(cfg, name)
	if !exists {
		logging.Error("Cannot restart client, configuration not found", "client", name)
		return
	}

	oldClient, exists := app.LSPRegistry.Get(name)
	if exists {
		app.LSPRegistry.Delete(name)
	}

	allowed, status := app.LSPRegistry.TryBeginStart(name, clientConfig.Command)
	if !allowed {
		logging.Debug("Skipping LSP restart trigger", "name", name, "phase", status.Phase, "message", status.Message)
		return
	}

	if exists && oldClient != nil {
		// Try to shut it down gracefully, but don't block on errors
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = oldClient.Shutdown(shutdownCtx)
		cancel()
	}

	// Create a new client using the shared function
	app.createAndStartLSPClient(ctx, name, clientConfig.Command, clientConfig.Args...)
	logging.Info("Successfully restarted LSP client", "client", name)
}
