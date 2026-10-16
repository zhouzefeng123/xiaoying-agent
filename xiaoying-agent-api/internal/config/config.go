// Package config manages application configuration from various sources.
package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/auramate/auramate/internal/llm/models"
	"github.com/auramate/auramate/internal/logging"
	"github.com/spf13/viper"
)

// MCPType defines the type of MCP (Model Control Protocol) server.
type MCPType string

// Supported MCP types
const (
	MCPStdio MCPType = "stdio"
	MCPSse   MCPType = "sse"
)

// MCPServer defines the configuration for a Model Control Protocol server.
type MCPServer struct {
	Command  string            `json:"command"`
	Env      map[string]string `json:"env"` // Change to map to support map structure in config file
	Args     []string          `json:"args"`
	Type     MCPType           `json:"type"`
	URL      string            `json:"url"`
	Headers  map[string]string `json:"headers"`
	Disabled bool              `json:"disabled"` // Add disabled flag
}

type AgentName string

const (
	AgentCoder      AgentName = "coder"
	AgentSummarizer AgentName = "summarizer"
	AgentTask       AgentName = "task"
	AgentTitle      AgentName = "title"
	AgentVisual     AgentName = "visual"
	AgentLife       AgentName = "life"
	AgentNormal     AgentName = "normal"
)

// Agent defines configuration for different LLM models and their token limits.
type Agent struct {
	Model           models.ModelID `json:"model"`
	MaxTokens       int64          `json:"maxTokens"`
	ReasoningEffort string         `json:"reasoningEffort"` // For openai models low,medium,heigh
	DynamicDenoise  *bool          `json:"dynamicDenoise,omitempty" mapstructure:"dynamicDenoise"`
}

func (a Agent) DynamicDenoiseEnabled() bool {
	if a.DynamicDenoise == nil {
		return true
	}
	return *a.DynamicDenoise
}

// Provider defines configuration for an LLM provider.
type Provider struct {
	APIKey   string `json:"apiKey"`
	BaseURL  string `json:"baseURL,omitempty"`
	Disabled bool   `json:"disabled"`
}

// EmbeddingConfig defines configuration for vector embedding models.
type EmbeddingConfig struct {
	Provider models.ModelProvider `json:"provider" mapstructure:"provider"`
	Model    string               `json:"model" mapstructure:"model"`
	APIKey   string               `json:"apiKey,omitempty" mapstructure:"apiKey"`
	BaseURL  string               `json:"baseURL,omitempty" mapstructure:"baseURL"`
}

// MainModelConfig defines configuration for the primary LLM used by agents.
type MainModelConfig struct {
	Provider  models.ModelProvider `json:"provider" mapstructure:"provider"`
	Model     string               `json:"model" mapstructure:"model"`
	APIKey    string               `json:"apiKey,omitempty" mapstructure:"apiKey"`
	BaseURL   string               `json:"baseURL,omitempty" mapstructure:"baseURL"`
	MaxTokens int64                `json:"maxTokens,omitempty" mapstructure:"maxTokens"`
}

const mainModelOverrideID models.ModelID = "custom-main-model"
const mainModelFixedAPIModel = "deepseek-v4-pro"

func isMainModelOverrideEnabled(mainModel *MainModelConfig) bool {
	if mainModel == nil {
		return false
	}

	return strings.TrimSpace(mainModel.Model) != "" ||
		strings.TrimSpace(mainModel.APIKey) != "" ||
		strings.TrimSpace(mainModel.BaseURL) != ""
}

func resolveMainModelBaseModel(mainModel *MainModelConfig) (models.Model, bool) {
	configuredModelID := models.ModelID(strings.TrimSpace(mainModel.Model))
	if configuredModelID != "" {
		if baseModel, ok := models.SupportedModels[configuredModelID]; ok {
			return baseModel, true
		}
	}

	baseModel, ok := models.SupportedModels[models.ModelID(mainModelFixedAPIModel)]
	return baseModel, ok
}

func inferMainModelProviderFromBaseURL(baseURL string) models.ModelProvider {
	if baseURL == "" {
		return models.ProviderOpenAI
	}
	if strings.Contains(baseURL, "localhost") || strings.Contains(baseURL, "127.0.0.1") {
		return models.ProviderOllama
	}
	return models.ProviderCustom
}

func resolveMainModelProvider(mainModel *MainModelConfig, baseModel models.Model, hasBaseModel bool) models.ModelProvider {
	if mainModel.Provider != "" {
		return mainModel.Provider
	}
	if hasBaseModel && baseModel.Provider != "" {
		return baseModel.Provider
	}
	return inferMainModelProviderFromBaseURL(mainModel.BaseURL)
}

func applyMainModelExplicitOverrides(model models.Model, mainModel *MainModelConfig, hasBaseModel bool) models.Model {
	configuredAPIModel := strings.TrimSpace(mainModel.Model)
	configuredModelMatched := configuredAPIModel != "" && model.ID == models.ModelID(configuredAPIModel)
	model.ID = mainModelOverrideID
	model.Name = "Main Model (Custom)"
	if configuredAPIModel != "" {
		model.APIModel = configuredAPIModel
	} else if strings.TrimSpace(model.APIModel) == "" {
		model.APIModel = mainModelFixedAPIModel
	}

	if mainModel.APIKey != "" {
		model.APIKey = mainModel.APIKey
	}
	if mainModel.BaseURL != "" {
		model.BaseURL = mainModel.BaseURL
	}
	if model.DefaultMaxTokens <= 0 {
		model.DefaultMaxTokens = 8192
	}
	if configuredAPIModel != "" && !configuredModelMatched {
		model.CanReason = false
	}
	if mainModel.MaxTokens > 0 {
		model.ContextWindow = mainModel.MaxTokens
	}

	return model
}

func resolveMainModelOverride(mainModel *MainModelConfig) models.Model {
	baseModel, hasBaseModel := resolveMainModelBaseModel(mainModel)
	baseModel.Provider = resolveMainModelProvider(mainModel, baseModel, hasBaseModel)
	return applyMainModelExplicitOverrides(baseModel, mainModel, hasBaseModel)
}

// Data defines storage configuration.
type Data struct {
	Directory string `json:"directory,omitempty" mapstructure:"directory"`
}

// DatabaseConfig defines the relational database the backend persists to.
// The project now targets MySQL; the SQLite era settings (data.directory) are
// kept only for logs and other file based state.
type DatabaseConfig struct {
	Host     string `json:"host" mapstructure:"host"`
	Port     int    `json:"port" mapstructure:"port"`
	User     string `json:"user" mapstructure:"user"`
	Password string `json:"password" mapstructure:"password"`
	Name     string `json:"name" mapstructure:"name"`
}

// LSPConfig defines configuration for Language Server Protocol integration.
type LSPConfig struct {
	Disabled bool     `json:"enabled"`
	Command  string   `json:"command"`
	Args     []string `json:"args"`
	Options  any      `json:"options"`
}

var builtinDefaultLSPConfigs = map[string]LSPConfig{
	"go": {
		Command: "gopls",
	},
	"python": {
		Command: "pyright-langserver",
		Args:    []string{"--stdio"},
	},
	"typescript": {
		Command: "typescript-language-server",
		Args:    []string{"--stdio"},
	},
	"rust": {
		Command: "rust-analyzer",
	},
}

func normalizeLSPKey(language string) string {
	return strings.ToLower(strings.TrimSpace(language))
}

func cloneLSPConfig(cfg LSPConfig) LSPConfig {
	cloned := cfg
	if len(cfg.Args) > 0 {
		cloned.Args = append([]string(nil), cfg.Args...)
	}
	return cloned
}

func BuiltinDefaultLSPConfig(language string) (LSPConfig, bool) {
	defaultCfg, ok := builtinDefaultLSPConfigs[normalizeLSPKey(language)]
	if !ok {
		return LSPConfig{}, false
	}
	return cloneLSPConfig(defaultCfg), true
}

func EffectiveLSPConfigs(cfg *Config) map[string]LSPConfig {
	effective := make(map[string]LSPConfig, len(builtinDefaultLSPConfigs))
	for language, defaultCfg := range builtinDefaultLSPConfigs {
		effective[language] = cloneLSPConfig(defaultCfg)
	}

	if cfg == nil {
		return effective
	}

	for language, lspCfg := range cfg.LSP {
		normalizedLanguage := normalizeLSPKey(language)
		if normalizedLanguage == "" {
			continue
		}

		if defaultCfg, ok := BuiltinDefaultLSPConfig(normalizedLanguage); ok {
			if strings.TrimSpace(lspCfg.Command) == "" {
				lspCfg.Command = defaultCfg.Command
			}
			if len(lspCfg.Args) == 0 && len(defaultCfg.Args) > 0 {
				lspCfg.Args = append([]string(nil), defaultCfg.Args...)
			}
		}

		effective[normalizedLanguage] = cloneLSPConfig(lspCfg)
	}

	return effective
}

func EffectiveLSPConfigForLanguage(cfg *Config, language string) (LSPConfig, bool) {
	effectiveCfg, ok := EffectiveLSPConfigs(cfg)[normalizeLSPKey(language)]
	return effectiveCfg, ok
}

// TUIConfig defines the configuration for the Terminal User Interface.
type TUIConfig struct {
	Theme string `json:"theme,omitempty"`
}

// ShellConfig defines the configuration for the shell used by the bash tool.
type ShellConfig struct {
	Path string   `json:"path,omitempty"`
	Args []string `json:"args,omitempty"`
}

// SecurityConfig defines runtime security switches for tool execution.
// NOTE: Defaults are set in setDefaults().
type SecurityConfig struct {
	// AllowAllTools bypasses permission checks for all tools.
	// This is convenient for local/dev, but unsafe for production.
	AllowAllTools bool `json:"allowAllTools,omitempty" mapstructure:"allowAllTools"`
}

// LLMRuntimeConfig defines runtime knobs for context/token budgeting.
type LLMRuntimeConfig struct {
	// ContextWindowCap is a safety cap for context window budgeting (tokens).
	// If 0, defaults to a conservative value (see setDefaults()).
	ContextWindowCap int64 `json:"contextWindowCap,omitempty" mapstructure:"contextWindowCap"`

	// CharPerToken is a heuristic used when token counts are unavailable.
	// For CJK-heavy content, a smaller value is safer (tokens per char is higher).
	// If 0, defaults to a conservative value (see setDefaults()).
	CharPerToken float64 `json:"charPerToken,omitempty" mapstructure:"charPerToken"`
}

// Config is the main configuration structure for the application.
type Config struct {
	Data            Data                              `json:"data"`
	Database        DatabaseConfig                    `json:"database" mapstructure:"database"`
	WorkingDir      string                            `json:"wd,omitempty"`
	ProjectRoot     string                            `json:"projectRoot,omitempty"` // Root of the project (if detected)
	AppRoot         string                            `json:"-"`                     // Internal use only
	SystemSkillsDir string                            `json:"-"`                     // Internal use only
	McpToolsDir     string                            `json:"-"`                     // Internal use only
	MCPServers      map[string]MCPServer              `json:"mcpServers,omitempty" mapstructure:"mcpServers"`
	Providers       map[models.ModelProvider]Provider `json:"providers,omitempty" mapstructure:"providers"`
	MainModel       *MainModelConfig                  `json:"main_model,omitempty" mapstructure:"main_model"`
	Embeddings      *EmbeddingConfig                  `json:"embeddings,omitempty" mapstructure:"embeddings"`
	LSP             map[string]LSPConfig              `json:"lsp,omitempty" mapstructure:"lsp"`
	Agents          map[AgentName]Agent               `json:"agents,omitempty"`
	Models          map[models.ModelID]models.Model   `json:"models,omitempty"`
	Debug           bool                              `json:"debug,omitempty"`
	DebugLSP        bool                              `json:"debugLSP,omitempty"`
	ContextPaths    []string                          `json:"contextPaths,omitempty"`
	TUI             TUIConfig                         `json:"tui"`
	Shell           ShellConfig                       `json:"shell,omitempty"`
	AutoCompact     bool                              `json:"autoCompact,omitempty"`
	AuraAuth        *AuraAuthConfig                   `json:"auraAuth,omitempty"`
	Security        SecurityConfig                    `json:"security,omitempty" mapstructure:"security"`
	LLM             LLMRuntimeConfig                  `json:"llm,omitempty" mapstructure:"llm"`
}

// AuraAuthConfig defines the configuration for Aura Auth API.
type AuraAuthConfig struct {
	Token string `json:"token,omitempty"`
}

func preferredConfigPath(appRoot string) string {
	backendConfigPath := filepath.Join(appRoot, "backend", fmt.Sprintf(".%s.json", appName))
	if _, err := os.Stat(backendConfigPath); err == nil {
		return backendConfigPath
	}
	return filepath.Join(appRoot, fmt.Sprintf(".%s.json", appName))
}

// MainConfigPath returns the effective main config file path.
func MainConfigPath() string {
	if cfgFile := strings.TrimSpace(os.Getenv("AURAMATE_CONFIG_FILE")); cfgFile != "" {
		return cfgFile
	}

	if cfg != nil && cfg.AppRoot != "" {
		return preferredConfigPath(cfg.AppRoot)
	}

	cwd, _ := os.Getwd()
	if filepath.Base(cwd) == "backend" {
		return filepath.Join(cwd, fmt.Sprintf(".%s.json", appName))
	}
	if _, err := os.Stat(filepath.Join(cwd, "backend", fmt.Sprintf(".%s.json", appName))); err == nil {
		return filepath.Join(cwd, "backend", fmt.Sprintf(".%s.json", appName))
	}
	return filepath.Join(cwd, fmt.Sprintf(".%s.json", appName))
}

func resolveDefaultsConfigPath(configPath string, appRoot string) string {
	candidates := []string{
		filepath.Join(filepath.Dir(configPath), fmt.Sprintf(".%s.defaults.json", appName)),
		filepath.Join(appRoot, fmt.Sprintf(".%s.defaults.json", appName)),
	}

	seen := make(map[string]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, exists := seen[candidate]; exists {
			continue
		}
		seen[candidate] = struct{}{}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}

	return candidates[0]
}

func mergeConfigFile(path string, allowMissing bool, description string) error {
	source := viper.New()
	source.SetConfigFile(path)
	if err := source.ReadInConfig(); err != nil {
		if allowMissing {
			if _, ok := err.(viper.ConfigFileNotFoundError); ok {
				return nil
			}
			if os.IsNotExist(err) {
				return nil
			}
		}
		return fmt.Errorf("failed to read %s: %w", description, err)
	}
	viper.MergeConfigMap(source.AllSettings())
	return nil
}

// Application constants
const (
	defaultDataDirectory = "database"
	defaultLogLevel      = "warn"
	appName              = "auramate"
	mcpConfigFileName    = ".auramate.mcp.json"

	MaxTokensFallbackDefault = 16384
)

var defaultContextPaths = []string{
	".github/copilot-instructions.md",
	".cursorrules",
	".cursor/rules/",
	"CLAUDE.md",
	"CLAUDE.local.md",
	"auramate.md",
	"auramate.local.md",
	"AuraMate.md",
	"AuraMate.local.md",
	"AURAMATE.md",
	"AURAMATE.local.md",
	"xiaoying.md",
	"xiaoying.local.md",
	"XiaoYing.md",
	"XiaoYing.local.md",
	"XIAOYING.md",
	"XIAOYING.local.md",
}

// Global configuration instance
var cfg *Config

// GetAgent returns the agent configuration by name.
func GetAgent(name AgentName) Agent {
	if cfg == nil {
		return Agent{}
	}
	if agent, ok := cfg.Agents[name]; ok {
		return agent
	}
	return Agent{}
}

// Load initializes the configuration from environment variables and config files.
// If debug is true, debug mode is enabled and log level is set to debug.
// It returns an error if configuration loading fails.
func Load(workingDir string, debug bool) (*Config, error) {
	if cfg != nil {
		return cfg, nil
	}

	cfg = &Config{
		// Default to empty, will be populated by Load logic or Viper
		// IMPORTANT: Do NOT set WorkingDir to `workingDir` argument blindly if it is just CWD.
		// We want to apply our heuristic below.
		// However, if `workingDir` argument was explicitly passed (e.g. via --cwd flag), we should respect it?
		// Usually `Load` is called with `os.Getwd()`.
		// Let's assume `workingDir` param IS the CWD.
		// We leave cfg.WorkingDir empty here so the logic below triggers.
		// But wait, if config file sets `wd`, Viper will overwrite it.
		// If flag sets `wd`, Viper will overwrite it.
		// So initialization to "" is correct.
		WorkingDir: "",
		MCPServers: make(map[string]MCPServer),
		Providers:  make(map[models.ModelProvider]Provider),
		LSP:        make(map[string]LSPConfig),
	}

	// 1. Determine Root (Always)
	cwd, _ := os.Getwd()
	appRoot := cwd
	exePath, err := os.Executable()
	if err == nil {
		// If not running in temp (go run), use executable dir
		// Simple check for temp dir
		tempDir := os.TempDir()
		if !strings.Contains(strings.ToLower(exePath), strings.ToLower(tempDir)) && !strings.Contains(exePath, "go-build") {
			appRoot = filepath.Dir(exePath)

			// Handle Air (dev) tmp directory: if binary is in ./tmp, use parent as root
			if filepath.Base(appRoot) == "tmp" {
				parent := filepath.Dir(appRoot)
				// Check if parent has go.mod, indicating we are in dev mode inside backend
				if _, err := os.Stat(filepath.Join(parent, "go.mod")); err == nil {
					appRoot = parent
				}
			}
		}
	}

	// If appRoot is "backend", move up to project root to ensure workspace is at the top level
	if filepath.Base(appRoot) == "backend" {
		appRoot = filepath.Dir(appRoot)
	}

	// Initialize Config File from Defaults if needed
	legacyConfigPath := preferredConfigPath(appRoot)
	configPath := strings.TrimSpace(os.Getenv("AURAMATE_CONFIG_FILE"))
	if configPath == "" {
		configPath = legacyConfigPath
	}
	defaultsPath := resolveDefaultsConfigPath(configPath, appRoot)

	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return cfg, fmt.Errorf("failed to prepare config directory: %w", err)
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		initialized := false
		if configPath != legacyConfigPath {
			if legacyInput, legacyErr := os.ReadFile(legacyConfigPath); legacyErr == nil {
				logging.Info("Migrating configuration from legacy path", "source", legacyConfigPath, "target", configPath)
				if writeErr := os.WriteFile(configPath, legacyInput, 0o644); writeErr != nil {
					return cfg, fmt.Errorf("failed to migrate config file: %w", writeErr)
				}
				initialized = true
			}
		}

		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			if _, err := os.Stat(defaultsPath); err == nil {
				logging.Info("Initializing configuration from defaults", "source", defaultsPath, "target", configPath)
				input, err := os.ReadFile(defaultsPath)
				if err == nil {
					if writeErr := os.WriteFile(configPath, input, 0o644); writeErr != nil {
						return cfg, fmt.Errorf("failed to initialize config file: %w", writeErr)
					}
					initialized = true
				} else {
					logging.Error("Failed to read defaults file", "error", err)
				}
			}
		}

		if !initialized {
			logging.Info("Initializing empty configuration", "target", configPath)
			if writeErr := os.WriteFile(configPath, []byte("{}"), 0o644); writeErr != nil {
				return cfg, fmt.Errorf("failed to initialize empty config file: %w", writeErr)
			}
		}
	}

	configureViper()
	viper.SetConfigFile(configPath)
	setDefaults(debug)
	if err := mergeConfigFile(defaultsPath, true, "defaults config"); err != nil {
		return cfg, err
	}

	// Read global config
	if err := mergeConfigFile(configPath, false, "config"); err != nil {
		return cfg, err
	}

	// Load and merge local config
	mergeLocalConfig(workingDir)

	// Apply configuration to the struct BEFORE setting defaults based on environment
	// This ensures that if the config file has values, they are respected
	if err := viper.Unmarshal(cfg); err != nil {
		return cfg, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	setProviderDefaults()

	cfg.AppRoot = appRoot
	cfg.ProjectRoot = appRoot // Expose for LLM Context

	// MCP config is now stored in a dedicated file and merged at runtime.
	if err := bootstrapMCPServers(appRoot); err != nil {
		logging.Warn("failed to bootstrap MCP config file", "error", err)
	}

	// Check if we are in Repo Root (has "backend" subdir)
	inRepoRoot := false
	if _, err := os.Stat(filepath.Join(appRoot, "backend")); err == nil {
		inRepoRoot = true
	}

	// 2. Set Default Paths based on AppRoot
	if cfg.WorkingDir == "" || cfg.WorkingDir == cwd {
		// ALWAYS use project root workspace, regardless of dev or prod
		cfg.WorkingDir = filepath.Join(appRoot, "workspace")

		// Allow Environment Override (Highest Priority)
		if envWD := os.Getenv("AURAMATE_WORKSPACE"); envWD != "" {
			cfg.WorkingDir = envWD
		}

		// Ensure it exists
		if err := os.MkdirAll(cfg.WorkingDir, 0755); err != nil {
			logging.Warn("Failed to create workspace directory, falling back to temp", "path", cfg.WorkingDir, "error", err)
			cfg.WorkingDir = filepath.Join(os.TempDir(), "auramate_workspace")
			os.MkdirAll(cfg.WorkingDir, 0755)
		}
	} else {
		// STRONG CONSTRAINT: Ensure explicitly provided WorkingDir exists
		os.MkdirAll(cfg.WorkingDir, 0755)
	}

	// 3. Set SystemSkillsDir and McpToolsDir based on AppRoot structure
	if inRepoRoot {
		cfg.SystemSkillsDir = filepath.Join(appRoot, "backend", "system_skills")
		cfg.McpToolsDir = filepath.Join(appRoot, "backend", "tools")
	} else {
		cfg.SystemSkillsDir = filepath.Join(appRoot, "system_skills")
		cfg.McpToolsDir = filepath.Join(appRoot, "tools")
	}

	// Ensure directories exist (optional, but good for consistency)
	os.MkdirAll(cfg.SystemSkillsDir, 0755)
	os.MkdirAll(cfg.McpToolsDir, 0755)

	// Set AURAMATE_WD env var to ensure subprocesses (like tools) inherit this
	os.Setenv("AURAMATE_WD", cfg.WorkingDir)
	os.Setenv("AURAMATE_SKILLS_DIR", cfg.SystemSkillsDir)
	os.Setenv("AURAMATE_TOOLS_DIR", cfg.McpToolsDir)
	os.Setenv("AURAMATE_CONFIG_FILE", configPath) // Pass the actual resolved config file path

	// Apply configuration to the struct AGAIN to pick up defaults
	if err := viper.Unmarshal(cfg); err != nil {
		return cfg, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	applyDatabaseEnvOverrides(cfg)

	// fmt.Printf("DEBUG: Providers: %+v\n", cfg.Providers)
	// fmt.Printf("DEBUG: Agents: %+v\n", cfg.Agents)

	// Register custom models defined in configuration
	for id, model := range cfg.Models {
		if model.ID == "" {
			model.ID = id
		}

		// If the custom model has no provider, check if we have a built-in one
		if model.Provider == "" {
			if existing, ok := models.SupportedModels[id]; ok && existing.Provider != "" {
				logging.Warn("Ignoring custom model with empty provider, using built-in", "id", id)
				continue
			}
		}

		// Overwrite or add the model to SupportedModels
		models.SupportedModels[id] = model
		logging.Info("Registered custom model", "id", id, "name", model.Name, "api_model", model.APIModel)
	}

	// Register Main Model only when the custom route is actually configured.
	// Keeping an empty main_model block should not silently hijack preset model routing.
	if isMainModelOverrideEnabled(cfg.MainModel) {
		id := mainModelOverrideID
		model := resolveMainModelOverride(cfg.MainModel)

		models.SupportedModels[id] = model
		logging.Info("Registered main model override", "id", id, "api_model", model.APIModel, "provider", model.Provider, "max_tokens", cfg.MainModel.MaxTokens)
	}

	applyDefaultValues()
	defaultLevel := slog.LevelWarn
	if cfg.Debug {
		defaultLevel = slog.LevelWarn
	}
	if os.Getenv("AURAMATE_DEV_DEBUG") == "true" {
		loggingFile := fmt.Sprintf("%s/%s", cfg.Data.Directory, "debug.log")
		messagesPath := fmt.Sprintf("%s/%s", cfg.Data.Directory, "messages")

		// if file does not exist create it
		if _, err := os.Stat(loggingFile); os.IsNotExist(err) {
			if err := os.MkdirAll(cfg.Data.Directory, 0o755); err != nil {
				return cfg, fmt.Errorf("failed to create directory: %w", err)
			}
			if _, err := os.Create(loggingFile); err != nil {
				return cfg, fmt.Errorf("failed to create log file: %w", err)
			}
		}

		if _, err := os.Stat(messagesPath); os.IsNotExist(err) {
			if err := os.MkdirAll(messagesPath, 0o756); err != nil {
				return cfg, fmt.Errorf("failed to create directory: %w", err)
			}
		}
		logging.MessageDir = messagesPath

		sloggingFileWriter, err := os.OpenFile(loggingFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o666)
		if err != nil {
			return cfg, fmt.Errorf("failed to open log file: %w", err)
		}
		// Configure logger
		logger := slog.New(slog.NewTextHandler(sloggingFileWriter, &slog.HandlerOptions{
			Level: defaultLevel,
		}))
		slog.SetDefault(logger)
	} else {
		// Configure logger
		logger := slog.New(slog.NewTextHandler(logging.NewWriter(), &slog.HandlerOptions{
			Level: defaultLevel,
		}))
		slog.SetDefault(logger)
	}

	// Validate configuration
	if err := Validate(); err != nil {
		return cfg, fmt.Errorf("config validation failed: %w", err)
	}

	if cfg.Agents == nil {
		cfg.Agents = make(map[AgentName]Agent)
	}

	// Override the max tokens for title agent
	cfg.Agents[AgentTitle] = Agent{
		Model:     cfg.Agents[AgentTitle].Model,
		MaxTokens: 80,
	}
	return cfg, nil
}

// configureViper sets up viper's configuration paths and environment variables.
func configureViper() {
	viper.SetConfigName(fmt.Sprintf(".%s", appName))
	viper.SetConfigType("json")
	viper.AddConfigPath(".")
	viper.AddConfigPath("$HOME")
	viper.AddConfigPath(fmt.Sprintf("$XDG_CONFIG_HOME/%s", appName))
	viper.AddConfigPath(fmt.Sprintf("$HOME/.config/%s", appName))
	viper.SetEnvPrefix(strings.ToUpper(appName))
	viper.AutomaticEnv()
}

// setDefaults configures default values for configuration options.
func setDefaults(debug bool) {
	viper.SetDefault("data.directory", "database")

	// Database defaults (MySQL). Override per environment with
	// AURAMATE_DB_HOST / AURAMATE_DB_PORT / AURAMATE_DB_USER /
	// AURAMATE_DB_PASSWORD / AURAMATE_DB_NAME, or via the `database` block in
	// the config file.
	viper.SetDefault("database.host", "127.0.0.1")
	viper.SetDefault("database.port", 3306)
	viper.SetDefault("database.user", "root")
	viper.SetDefault("database.password", "")
	viper.SetDefault("database.name", "auramate")

	viper.SetDefault("contextPaths", defaultContextPaths)
	viper.SetDefault("tui.theme", "auramate")
	viper.SetDefault("autoCompact", true)

	// Security defaults
	// Backward compatible default: allow all tools (matches current permission service behavior).
	// For production deployments, set `security.allowAllTools=false`.
	viper.SetDefault("security.allowAllTools", true)

	// LLM runtime defaults (conservative)
	// Context window budgeting cap: default 256k tokens for stability; can be raised for 1M models.
	viper.SetDefault("llm.contextWindowCap", int64(256000))
	// Char/token heuristic: 1 token ~= 1.86 chars (EN) but worse for CJK; 1.2 is safer than 2.0.
	viper.SetDefault("llm.charPerToken", 3.5)

	// Set default shell from environment or fallback
	shellPath := os.Getenv("SHELL")
	if shellPath == "" {
		if runtime.GOOS == "windows" {
			shellPath = "powershell"
		} else {
			shellPath = "/bin/bash"
		}
	}
	viper.SetDefault("shell.path", shellPath)
	if runtime.GOOS == "windows" {
		viper.SetDefault("shell.args", []string{"-NoLogo", "-NoExit"})
	} else {
		viper.SetDefault("shell.args", []string{"-l"})
	}

	if debug {
		viper.SetDefault("debug", true)
		viper.Set("log.level", "debug")
	} else {
		viper.SetDefault("debug", false)
		viper.SetDefault("log.level", defaultLogLevel)
	}
}

// applyDatabaseEnvOverrides lets explicit AURAMATE_DB_* environment variables
// override whatever the config file (or its defaults) supplied.
func applyDatabaseEnvOverrides(cfg *Config) {
	if cfg == nil {
		return
	}

	if v := strings.TrimSpace(os.Getenv("AURAMATE_DB_HOST")); v != "" {
		cfg.Database.Host = v
	}
	if v := strings.TrimSpace(os.Getenv("AURAMATE_DB_PORT")); v != "" {
		if port, err := strconv.Atoi(v); err == nil && port > 0 {
			cfg.Database.Port = port
		} else {
			logging.Warn("Invalid AURAMATE_DB_PORT, keeping configured value", "value", v)
		}
	}
	if v := strings.TrimSpace(os.Getenv("AURAMATE_DB_USER")); v != "" {
		cfg.Database.User = v
	}
	if v, ok := os.LookupEnv("AURAMATE_DB_PASSWORD"); ok && v != "" {
		cfg.Database.Password = v
	}
	if v := strings.TrimSpace(os.Getenv("AURAMATE_DB_NAME")); v != "" {
		cfg.Database.Name = v
	}

	// A partially configured file should still be able to connect.
	if strings.TrimSpace(cfg.Database.Host) == "" {
		cfg.Database.Host = "127.0.0.1"
	}
	if cfg.Database.Port == 0 {
		cfg.Database.Port = 3306
	}
	if strings.TrimSpace(cfg.Database.User) == "" {
		cfg.Database.User = "root"
	}
	if strings.TrimSpace(cfg.Database.Name) == "" {
		cfg.Database.Name = "auramate"
	}
}

// setProviderDefaults configures LLM provider defaults based on provider provided by
// environment variables and configuration file.
func setProviderDefaults() {
	// Set all API keys we can find in the environment
	// Note: Viper does not default if the json apiKey is ""
	if apiKey := os.Getenv("ANTHROPIC_API_KEY"); apiKey != "" {
		viper.SetDefault("providers.anthropic.apiKey", apiKey)
	}
	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		viper.SetDefault("providers.openai.apiKey", apiKey)
	}
	if apiKey := os.Getenv("GEMINI_API_KEY"); apiKey != "" {
		viper.SetDefault("providers.gemini.apiKey", apiKey)
	}
	if apiKey := os.Getenv("DEEPSEEK_API_KEY"); apiKey != "" {
		viper.SetDefault("providers.deepseek.apiKey", apiKey)
	}
	if apiKey := os.Getenv("ALIYUN_API_KEY"); apiKey != "" {
		viper.SetDefault("providers.aliyun.apiKey", apiKey)
	}
	if apiKey := os.Getenv("DASHSCOPE_API_KEY"); apiKey != "" {
		// Fallback/Alias for Aliyun
		if viper.GetString("providers.aliyun.apiKey") == "" {
			viper.SetDefault("providers.aliyun.apiKey", apiKey)
		}
	}
	if apiKey := os.Getenv("GROQ_API_KEY"); apiKey != "" {
		viper.SetDefault("providers.groq.apiKey", apiKey)
	}
	if apiKey := os.Getenv("OPENROUTER_API_KEY"); apiKey != "" {
		viper.SetDefault("providers.openrouter.apiKey", apiKey)
	}
	if apiKey := os.Getenv("XAI_API_KEY"); apiKey != "" {
		viper.SetDefault("providers.xai.apiKey", apiKey)
	}
	if apiKey := os.Getenv("AZURE_OPENAI_ENDPOINT"); apiKey != "" {
		// api-key may be empty when using Entra ID credentials �?that's okay
		viper.SetDefault("providers.azure.apiKey", os.Getenv("AZURE_OPENAI_API_KEY"))
	}
	if apiKey, err := LoadGitHubToken(); err == nil && apiKey != "" {
		viper.SetDefault("providers.copilot.apiKey", apiKey)
		if viper.GetString("providers.copilot.apiKey") == "" {
			viper.Set("providers.copilot.apiKey", apiKey)
		}
	}

	// Use this order to set the default models
	// 1. Copilot
	// 2. Anthropic
	// 3. OpenAI
	// 4. Google Gemini
	// 5. Groq
	// 6. OpenRouter
	// 7. AWS Bedrock
	// 8. Azure
	// 9. Google Cloud VertexAI

	// copilot configuration
	if key := viper.GetString("providers.copilot.apiKey"); strings.TrimSpace(key) != "" {
		viper.SetDefault("agents.coder.model", models.CopilotGPT4o)
		viper.SetDefault("agents.summarizer.model", models.CopilotGPT4o)
		viper.SetDefault("agents.task.model", models.CopilotGPT4o)
		viper.SetDefault("agents.title.model", models.CopilotGPT4o)
		return
	}

	// Anthropic configuration
	if key := viper.GetString("providers.anthropic.apiKey"); strings.TrimSpace(key) != "" {
		viper.SetDefault("agents.coder.model", models.Claude4Sonnet)
		viper.SetDefault("agents.summarizer.model", models.Claude4Sonnet)
		viper.SetDefault("agents.task.model", models.Claude4Sonnet)
		viper.SetDefault("agents.title.model", models.Claude4Sonnet)
		return
	}

	// OpenAI configuration
	if key := viper.GetString("providers.openai.apiKey"); strings.TrimSpace(key) != "" {
		viper.SetDefault("agents.coder.model", models.GPT41)
		viper.SetDefault("agents.summarizer.model", models.GPT41)
		viper.SetDefault("agents.task.model", models.GPT41Mini)
		viper.SetDefault("agents.title.model", models.GPT41Mini)
		return
	}

	// Google Gemini configuration
	if key := viper.GetString("providers.gemini.apiKey"); strings.TrimSpace(key) != "" {
		viper.SetDefault("agents.coder.model", models.Gemini25)
		viper.SetDefault("agents.summarizer.model", models.Gemini25)
		viper.SetDefault("agents.task.model", models.Gemini25Flash)
		viper.SetDefault("agents.title.model", models.Gemini25Flash)
		return
	}

	// Groq configuration
	if key := viper.GetString("providers.groq.apiKey"); strings.TrimSpace(key) != "" {
		viper.SetDefault("agents.coder.model", models.QWENQwq)
		viper.SetDefault("agents.summarizer.model", models.QWENQwq)
		viper.SetDefault("agents.task.model", models.QWENQwq)
		viper.SetDefault("agents.title.model", models.QWENQwq)
		viper.SetDefault("agents.life.model", models.QWENQwq)
		viper.SetDefault("agents.normal.model", models.QWENQwq)
		return
	}

	// DeepSeek configuration
	if key := viper.GetString("providers.deepseek.apiKey"); strings.TrimSpace(key) != "" {
		viper.SetDefault("agents.coder.model", models.DeepSeekV3)
		viper.SetDefault("agents.summarizer.model", models.DeepSeekV3)
		viper.SetDefault("agents.task.model", models.DeepSeekV3)
		viper.SetDefault("agents.title.model", models.DeepSeekV3)
		viper.SetDefault("agents.life.model", models.DeepSeekV3)
		viper.SetDefault("agents.normal.model", models.DeepSeekV3)
		return
	}

	// Aliyun configuration
	if key := viper.GetString("providers.aliyun.apiKey"); strings.TrimSpace(key) != "" {
		// Use QwenMax as the default high-capability model for Aliyun
		viper.SetDefault("agents.coder.model", models.QwenMax)
		viper.SetDefault("agents.summarizer.model", models.Qwen36Plus)
		viper.SetDefault("agents.task.model", models.QwenMax)
		viper.SetDefault("agents.title.model", models.Qwen36Plus)
		viper.SetDefault("agents.life.model", models.Qwen36Plus)
		viper.SetDefault("agents.normal.model", models.Qwen36Plus)
		return
	}

	// OpenRouter configuration
	if key := viper.GetString("providers.openrouter.apiKey"); strings.TrimSpace(key) != "" {
		viper.SetDefault("agents.coder.model", models.OpenRouterClaude37Sonnet)
		viper.SetDefault("agents.summarizer.model", models.OpenRouterClaude37Sonnet)
		viper.SetDefault("agents.task.model", models.OpenRouterClaude37Sonnet)
		viper.SetDefault("agents.title.model", models.OpenRouterClaude35Haiku)
		viper.SetDefault("agents.life.model", models.OpenRouterClaude35Sonnet)
		viper.SetDefault("agents.normal.model", models.OpenRouterClaude35Sonnet)
		return
	}

	// XAI configuration
	if key := viper.GetString("providers.xai.apiKey"); strings.TrimSpace(key) != "" {
		viper.SetDefault("agents.coder.model", models.XAIGrok3Beta)
		viper.SetDefault("agents.summarizer.model", models.XAIGrok3Beta)
		viper.SetDefault("agents.task.model", models.XAIGrok3Beta)
		viper.SetDefault("agents.title.model", models.XAiGrok3MiniFastBeta)
		viper.SetDefault("agents.life.model", models.XAIGrok3Beta)
		viper.SetDefault("agents.normal.model", models.XAIGrok3Beta)
		return
	}

	// AWS Bedrock configuration
	if hasAWSCredentials() {
		viper.SetDefault("agents.coder.model", models.BedrockClaude37Sonnet)
		viper.SetDefault("agents.summarizer.model", models.BedrockClaude37Sonnet)
		viper.SetDefault("agents.task.model", models.BedrockClaude37Sonnet)
		viper.SetDefault("agents.title.model", models.BedrockClaude37Sonnet)
		viper.SetDefault("agents.life.model", models.BedrockClaude37Sonnet)
		viper.SetDefault("agents.normal.model", models.BedrockClaude37Sonnet)
		return
	}

	// Azure OpenAI configuration
	if os.Getenv("AZURE_OPENAI_ENDPOINT") != "" {
		viper.SetDefault("agents.coder.model", models.AzureGPT41)
		viper.SetDefault("agents.summarizer.model", models.AzureGPT41)
		viper.SetDefault("agents.task.model", models.AzureGPT41Mini)
		viper.SetDefault("agents.title.model", models.AzureGPT41Mini)
		viper.SetDefault("agents.life.model", models.AzureGPT41)
		viper.SetDefault("agents.normal.model", models.AzureGPT41)
		return
	}

	// Google Cloud VertexAI configuration
	if hasVertexAICredentials() {
		viper.SetDefault("agents.coder.model", models.VertexAIGemini25)
		viper.SetDefault("agents.summarizer.model", models.VertexAIGemini25)
		viper.SetDefault("agents.task.model", models.VertexAIGemini25Flash)
		viper.SetDefault("agents.title.model", models.VertexAIGemini25Flash)
		viper.SetDefault("agents.life.model", models.VertexAIGemini25)
		viper.SetDefault("agents.normal.model", models.VertexAIGemini25)
		return
	}

	// Default Fallback: If no provider is configured, default to OpenAI GPT-4o
	// This ensures the application starts even without configuration.
	// The user will need to provide an API key via the UI or config file.
	// viper.SetDefault("agents.coder.model", models.GPT41)
	// viper.SetDefault("agents.summarizer.model", models.GPT41)
	// viper.SetDefault("agents.task.model", models.GPT41Mini)
	// viper.SetDefault("agents.title.model", models.GPT41Mini)
	// viper.SetDefault("agents.life.model", models.GPT41)
	// viper.SetDefault("agents.normal.model", models.GPT41)
	// viper.SetDefault("agents.visual.model", models.GPT41)
	// DISABLED: User requested removal of hardcoded fallback logic.
	// If no model is configured, it will be empty and fail validation.
}

// hasAWSCredentials checks if AWS credentials are available in the environment.
func hasAWSCredentials() bool {
	// Check for explicit AWS credentials
	if os.Getenv("AWS_ACCESS_KEY_ID") != "" && os.Getenv("AWS_SECRET_ACCESS_KEY") != "" {
		return true
	}

	// Check for AWS profile
	if os.Getenv("AWS_PROFILE") != "" || os.Getenv("AWS_DEFAULT_PROFILE") != "" {
		return true
	}

	// Check for AWS region
	if os.Getenv("AWS_REGION") != "" || os.Getenv("AWS_DEFAULT_REGION") != "" {
		return true
	}

	// Check if running on EC2 with instance profile
	if os.Getenv("AWS_CONTAINER_CREDENTIALS_RELATIVE_URI") != "" ||
		os.Getenv("AWS_CONTAINER_CREDENTIALS_FULL_URI") != "" {
		return true
	}

	return false
}

// hasVertexAICredentials checks if VertexAI credentials are available in the environment.
func hasVertexAICredentials() bool {
	// Check for explicit VertexAI parameters
	if os.Getenv("VERTEXAI_PROJECT") != "" && os.Getenv("VERTEXAI_LOCATION") != "" {
		return true
	}
	// Check for Google Cloud project and location
	if os.Getenv("GOOGLE_CLOUD_PROJECT") != "" && (os.Getenv("GOOGLE_CLOUD_REGION") != "" || os.Getenv("GOOGLE_CLOUD_LOCATION") != "") {
		return true
	}
	return false
}

func hasCopilotCredentials() bool {
	// Check for explicit Copilot parameters
	if token, _ := LoadGitHubToken(); token != "" {
		return true
	}
	return false
}

// readConfig handles the result of reading a configuration file.
func readConfig(err error) error {
	if err == nil {
		return nil
	}

	// It's okay if the config file doesn't exist
	if _, ok := err.(viper.ConfigFileNotFoundError); ok {
		return nil
	}

	return fmt.Errorf("failed to read config: %w", err)
}

// mergeLocalConfig loads and merges configuration from the local directory.
func mergeLocalConfig(workingDir string) {
	if strings.TrimSpace(os.Getenv("AURAMATE_CONFIG_FILE")) != "" {
		return
	}

	local := viper.New()
	local.SetConfigName(fmt.Sprintf(".%s", appName))
	local.SetConfigType("json")
	local.AddConfigPath(workingDir)
	// Also check backend directory (for dev/monorepo structures)
	local.AddConfigPath(filepath.Join(workingDir, "backend"))

	// Merge local config if it exists
	if err := local.ReadInConfig(); err == nil {
		viper.MergeConfigMap(local.AllSettings())
	}
}

// applyDefaultValues sets default values for configuration fields that need processing.
func applyDefaultValues() {
	// Set default MCP type if not specified
	for k, v := range cfg.MCPServers {
		if v.Type == "" {
			v.Type = MCPStdio
			cfg.MCPServers[k] = v
		}
	}
}

// MCPConfigPath returns the dedicated MCP config file path.
func MCPConfigPath() string {
	// 1) Prefer backend colocated config in repo/dev layout.
	if cfg != nil && cfg.AppRoot != "" {
		backendDir := filepath.Join(cfg.AppRoot, "backend")
		if _, err := os.Stat(filepath.Join(backendDir, fmt.Sprintf(".%s.json", appName))); err == nil {
			return filepath.Join(backendDir, mcpConfigFileName)
		}
	}

	// 2) Then colocate with the effective main config file (packaged/runtime).
	if cfgFile := strings.TrimSpace(os.Getenv("AURAMATE_CONFIG_FILE")); cfgFile != "" {
		return filepath.Join(filepath.Dir(cfgFile), mcpConfigFileName)
	}

	if cfg != nil && cfg.AppRoot != "" {
		return filepath.Join(cfg.AppRoot, mcpConfigFileName)
	}

	cwd, _ := os.Getwd()
	if filepath.Base(cwd) == "backend" {
		return filepath.Join(cwd, mcpConfigFileName)
	}
	if _, err := os.Stat(filepath.Join(cwd, "backend", fmt.Sprintf(".%s.json", appName))); err == nil {
		return filepath.Join(cwd, "backend", mcpConfigFileName)
	}
	return filepath.Join(cwd, mcpConfigFileName)
}

// loadMCPServersFromFile reads MCP servers from the dedicated MCP config file.
func loadMCPServersFromFile(path string) (map[string]MCPServer, error) {
	result := make(map[string]MCPServer)
	data, err := os.ReadFile(path)
	if err != nil {
		return result, err
	}
	if len(strings.TrimSpace(string(data))) == 0 {
		return result, nil
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	for k, v := range result {
		if v.Type == "" {
			v.Type = MCPStdio
			result[k] = v
		}
	}
	return result, nil
}

// SaveMCPServers persists MCP servers into the dedicated MCP config file.
func SaveMCPServers(servers map[string]MCPServer) error {
	path := MCPConfigPath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if servers == nil {
		servers = map[string]MCPServer{}
	}
	for k, v := range servers {
		if v.Type == "" {
			v.Type = MCPStdio
			servers[k] = v
		}
	}
	out, err := json.MarshalIndent(servers, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		return err
	}
	if cfg != nil {
		cfg.MCPServers = servers
	}
	return nil
}

// ReloadMCPServers reloads MCP server configs from the dedicated MCP file into memory.
func ReloadMCPServers() error {
	path := MCPConfigPath()
	servers, err := loadMCPServersFromFile(path)
	if err != nil {
		return err
	}
	if cfg != nil {
		cfg.MCPServers = servers
	}
	return nil
}

func bootstrapMCPServers(appRoot string) error {
	mcpPath := MCPConfigPath()
	if _, err := os.Stat(mcpPath); os.IsNotExist(err) {
		seed := map[string]MCPServer{}
		if cfg != nil && len(cfg.MCPServers) > 0 {
			seed = cfg.MCPServers
		}
		if err := SaveMCPServers(seed); err != nil {
			return err
		}
	}
	return ReloadMCPServers()
}

// It validates model IDs and providers, ensuring they are supported.
func validateAgent(cfg *Config, name AgentName, agent Agent) error {
	// Check if model exists
	model, modelExists := models.SupportedModels[agent.Model]
	if !modelExists {
		logging.Warn("unsupported model configured, reverting to default",
			"agent", name,
			"configured_model", agent.Model)

		// Set default model based on available providers
		if setDefaultModelForAgent(name) {
			logging.Info("set default model for agent", "agent", name, "model", cfg.Agents[name].Model)
			// Re-fetch the model info after setting default
			agent = cfg.Agents[name]
			model = models.SupportedModels[agent.Model]
		} else {
			return fmt.Errorf("model %s not supported and no valid default found", agent.Model)
		}
	}

	// Check if provider for the model is configured
	provider := model.Provider
	providerCfg, providerExists := cfg.Providers[provider]

	// If provider exists in config but disabled is explicitly false, we trust it (e.g. from .opencode.json)
	// If provider exists but has no API key, check env var

	if !providerExists {
		// ... (rest of the logic for non-existent provider config)
		apiKey := getProviderAPIKey(provider)
		if apiKey != "" {
			cfg.Providers[provider] = Provider{APIKey: apiKey}
		} else if setDefaultModelForAgent(name) {
			logging.Info("switched default model for agent due to missing provider", "agent", name, "new_model", cfg.Agents[name].Model)
			// Re-fetch the model info
			agent = cfg.Agents[name]
			// We need to re-validate this new agent/model pair?
			// But this function returns error.
			// Let's just assume setDefaultModelForAgent picks a valid one.
		} else {
			// Instead of creating empty provider, we now error out or warn heavily
			// User requested removal of hardcoded fallback logic.
			// However, if we error here, the app won't start if a provider is missing.
			// Maybe we should just log a warning and let it fail later at runtime?
			// But createAgentProvider will fail if provider is missing.
			logging.Warn("provider not configured for model", "agent", name, "model", agent.Model, "provider", provider)

			// We MUST create an entry or createAgentProvider will panic/fail immediately.
			// But since we want to avoid "silent fallback to OpenAI", we just leave it as missing?
			// No, createAgentProvider checks cfg.Providers[model.Provider].
			// If we return nil here, the app starts, but that agent will fail when used.
			// This is better than silently switching to OpenAI.
			return nil
		}
	} else if providerCfg.Disabled {
		// ...
	} else if providerCfg.APIKey == "" {
		// Provider enabled in config but no key - check env
		apiKey := getProviderAPIKey(provider)
		if apiKey != "" {
			providerCfg.APIKey = apiKey
			cfg.Providers[provider] = providerCfg
		} else {
			// Disable if no key found anywhere
			logging.Warn("provider enabled but no API key found", "provider", provider)
			// We don't necessarily fail here, maybe user wants to provide it later?
			// But for now let's fallback if possible
		}
	}

	// Validate max tokens
	if agent.MaxTokens <= 0 {
		logging.Warn("invalid max tokens, setting to default",
			"agent", name,
			"model", agent.Model,
			"max_tokens", agent.MaxTokens)

		// Update the agent with default max tokens
		updatedAgent := cfg.Agents[name]
		if model.DefaultMaxTokens > 0 {
			updatedAgent.MaxTokens = model.DefaultMaxTokens
		} else {
			updatedAgent.MaxTokens = MaxTokensFallbackDefault
		}
		cfg.Agents[name] = updatedAgent
	} else if model.ContextWindow > 0 && agent.MaxTokens > model.ContextWindow/2 {
		// Ensure max tokens doesn't exceed half the context window (reasonable limit)
		logging.Warn("max tokens exceeds half the context window, adjusting",
			"agent", name,
			"model", agent.Model,
			"max_tokens", agent.MaxTokens,
			"context_window", model.ContextWindow)

		// Update the agent with adjusted max tokens
		updatedAgent := cfg.Agents[name]
		updatedAgent.MaxTokens = model.ContextWindow / 2
		cfg.Agents[name] = updatedAgent
	}

	// Validate reasoning effort for models that support reasoning
	if model.CanReason && provider == models.ProviderOpenAI || provider == models.ProviderLocal {
		if agent.ReasoningEffort == "" {
			// Set default reasoning effort for models that support it
			logging.Info("setting default reasoning effort for model that supports reasoning",
				"agent", name,
				"model", agent.Model)

			// Update the agent with default reasoning effort
			updatedAgent := cfg.Agents[name]
			updatedAgent.ReasoningEffort = "medium"
			cfg.Agents[name] = updatedAgent
		} else {
			// Check if reasoning effort is valid (low, medium, high)
			effort := strings.ToLower(agent.ReasoningEffort)
			if effort != "low" && effort != "medium" && effort != "high" {
				logging.Warn("invalid reasoning effort, setting to medium",
					"agent", name,
					"model", agent.Model,
					"reasoning_effort", agent.ReasoningEffort)

				// Update the agent with valid reasoning effort
				updatedAgent := cfg.Agents[name]
				updatedAgent.ReasoningEffort = "medium"
				cfg.Agents[name] = updatedAgent
			}
		}
	} else if !model.CanReason && agent.ReasoningEffort != "" {
		// Model doesn't support reasoning but reasoning effort is set
		logging.Warn("model doesn't support reasoning but reasoning effort is set, ignoring",
			"agent", name,
			"model", agent.Model,
			"reasoning_effort", agent.ReasoningEffort)

		// Update the agent to remove reasoning effort
		updatedAgent := cfg.Agents[name]
		updatedAgent.ReasoningEffort = ""
		cfg.Agents[name] = updatedAgent
	}

	return nil
}

// Validate checks if the configuration is valid and applies defaults where needed.
func Validate() error {
	if cfg == nil {
		return fmt.Errorf("config not loaded")
	}

	// Validate agent models
	for name, agent := range cfg.Agents {
		if err := validateAgent(cfg, name, agent); err != nil {
			return err
		}
	}

	// Validate providers
	for provider, providerCfg := range cfg.Providers {
		if providerCfg.APIKey == "" && !providerCfg.Disabled {
			// Do NOT disable provider if API key is missing.
			// This allows the application to start and lets the user configure the key in the UI.
			logging.Warn("provider has no API key, but keeping enabled for runtime configuration", "provider", provider)
		}
	}

	// Validate LSP configurations
	for language, lspConfig := range cfg.LSP {
		if lspConfig.Command == "" && !lspConfig.Disabled {
			if defaultCfg, ok := BuiltinDefaultLSPConfig(language); ok {
				lspConfig.Command = defaultCfg.Command
				if len(lspConfig.Args) == 0 && len(defaultCfg.Args) > 0 {
					lspConfig.Args = append([]string(nil), defaultCfg.Args...)
				}
				cfg.LSP[language] = lspConfig
				continue
			}

			logging.Warn("LSP configuration has no command, marking as disabled", "language", language)
			lspConfig.Disabled = true
			cfg.LSP[language] = lspConfig
		}
	}

	return nil
}

// getProviderAPIKey gets the API key for a provider from environment variables
func getProviderAPIKey(provider models.ModelProvider) string {
	switch provider {
	case models.ProviderAnthropic:
		return os.Getenv("ANTHROPIC_API_KEY")
	case models.ProviderOpenAI:
		return os.Getenv("OPENAI_API_KEY")
	case models.ProviderGemini:
		return os.Getenv("GEMINI_API_KEY")
	case models.ProviderGROQ:
		return os.Getenv("GROQ_API_KEY")
	case models.ProviderAzure:
		return os.Getenv("AZURE_OPENAI_API_KEY")
	case models.ProviderOpenRouter:
		return os.Getenv("OPENROUTER_API_KEY")
	case models.ProviderBedrock:
		if hasAWSCredentials() {
			return "aws-credentials-available"
		}
	case models.ProviderVertexAI:
		if hasVertexAICredentials() {
			return "vertex-ai-credentials-available"
		}
	}
	return ""
}

// setDefaultModelForAgent sets a default model for an agent based on available providers
func setDefaultModelForAgent(agent AgentName) bool {
	if hasCopilotCredentials() {
		maxTokens := int64(5000)
		if agent == AgentTitle {
			maxTokens = 80
		}

		cfg.Agents[agent] = Agent{
			Model:     models.CopilotGPT4o,
			MaxTokens: maxTokens,
		}
		return true
	}
	// Check providers in order of preference
	if apiKey := os.Getenv("ANTHROPIC_API_KEY"); apiKey != "" {
		maxTokens := int64(5000)
		if agent == AgentTitle {
			maxTokens = 80
		}
		cfg.Agents[agent] = Agent{
			Model:     models.Claude37Sonnet,
			MaxTokens: maxTokens,
		}
		return true
	}

	if apiKey := os.Getenv("OPENAI_API_KEY"); apiKey != "" {
		var model models.ModelID
		maxTokens := int64(5000)
		reasoningEffort := ""

		switch agent {
		case AgentTitle:
			model = models.GPT41Mini
			maxTokens = 80
		case AgentTask:
			model = models.GPT41Mini
		default:
			model = models.GPT41
		}

		// Check if model supports reasoning
		if modelInfo, ok := models.SupportedModels[model]; ok && modelInfo.CanReason {
			reasoningEffort = "medium"
		}

		cfg.Agents[agent] = Agent{
			Model:           model,
			MaxTokens:       maxTokens,
			ReasoningEffort: reasoningEffort,
		}
		return true
	}

	if apiKey := os.Getenv("OPENROUTER_API_KEY"); apiKey != "" {
		var model models.ModelID
		maxTokens := int64(5000)
		reasoningEffort := ""

		switch agent {
		case AgentTitle:
			model = models.OpenRouterClaude35Haiku
			maxTokens = 80
		case AgentTask:
			model = models.OpenRouterClaude37Sonnet
		case AgentLife, AgentNormal:
			model = models.OpenRouterClaude35Sonnet
		default:
			model = models.OpenRouterClaude37Sonnet
		}

		// Check if model supports reasoning
		if modelInfo, ok := models.SupportedModels[model]; ok && modelInfo.CanReason {
			reasoningEffort = "medium"
		}

		cfg.Agents[agent] = Agent{
			Model:           model,
			MaxTokens:       maxTokens,
			ReasoningEffort: reasoningEffort,
		}
		return true
	}

	if apiKey := os.Getenv("GEMINI_API_KEY"); apiKey != "" {
		var model models.ModelID
		maxTokens := int64(5000)

		if agent == AgentTitle {
			model = models.Gemini25Flash
			maxTokens = 80
		} else {
			model = models.Gemini25
		}

		cfg.Agents[agent] = Agent{
			Model:     model,
			MaxTokens: maxTokens,
		}
		return true
	}

	if apiKey := os.Getenv("GROQ_API_KEY"); apiKey != "" {
		maxTokens := int64(5000)
		if agent == AgentTitle {
			maxTokens = 80
		}

		cfg.Agents[agent] = Agent{
			Model:     models.QWENQwq,
			MaxTokens: maxTokens,
		}
		return true
	}

	if hasAWSCredentials() {
		maxTokens := int64(5000)
		if agent == AgentTitle {
			maxTokens = 80
		}

		cfg.Agents[agent] = Agent{
			Model:           models.BedrockClaude37Sonnet,
			MaxTokens:       maxTokens,
			ReasoningEffort: "medium", // Claude models support reasoning
		}
		return true
	}

	if hasVertexAICredentials() {
		var model models.ModelID
		maxTokens := int64(5000)

		if agent == AgentTitle {
			model = models.VertexAIGemini25Flash
			maxTokens = 80
		} else {
			model = models.VertexAIGemini25
		}

		cfg.Agents[agent] = Agent{
			Model:     model,
			MaxTokens: maxTokens,
		}
		return true
	}

	// Aliyun Check (Add this block)
	if apiKey := os.Getenv("ALIYUN_API_KEY"); apiKey != "" {
		var model models.ModelID
		maxTokens := int64(5000)

		switch agent {
		case AgentTitle:
			model = models.Qwen36Plus
			maxTokens = 80
		case AgentCoder, AgentTask:
			model = models.QwenMax
		default:
			model = models.Qwen36Plus
		}

		cfg.Agents[agent] = Agent{
			Model:     model,
			MaxTokens: maxTokens,
		}
		return true
	}

	// DeepSeek Check (Add this block)
	if apiKey := os.Getenv("DEEPSEEK_API_KEY"); apiKey != "" {
		maxTokens := int64(5000)
		if agent == AgentTitle {
			maxTokens = 80
		}
		cfg.Agents[agent] = Agent{
			Model:     models.DeepSeekV3,
			MaxTokens: maxTokens,
		}
		return true
	}

	return false
}

func updateCfgFile(updateCfg func(config *Config)) error {
	if cfg == nil {
		return fmt.Errorf("config not loaded")
	}

	// Get the config file path
	// Prioritize local config file if WorkingDir is set and file exists/we are in local context
	configFile := viper.ConfigFileUsed()

	if cfg.WorkingDir != "" {
		localPath := filepath.Join(cfg.WorkingDir, fmt.Sprintf(".%s.json", appName))
		// Check if local file exists, or if configFile is empty (meaning no config file loaded yet)
		// we default to creating/using the local one if we have a WorkingDir.
		if _, err := os.Stat(localPath); err == nil {
			configFile = localPath
		} else if configFile == "" {
			configFile = localPath
		}
	}

	var configData []byte
	if configFile == "" {
		// Should not happen if WorkingDir logic above works, but as fallback
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
		configFile = filepath.Join(homeDir, fmt.Sprintf(".%s.json", appName))
		logging.Info("config file not found, creating new one", "path", configFile)
		configData = []byte(`{}`)
	} else {
		// Read the existing config file if it exists
		if _, err := os.Stat(configFile); os.IsNotExist(err) {
			logging.Info("config file does not exist, creating new one", "path", configFile)
			configData = []byte(`{}`)
		} else {
			data, err := os.ReadFile(configFile)
			if err != nil {
				return fmt.Errorf("failed to read config file: %w", err)
			}
			configData = data
		}
	}

	// Parse the JSON
	var userCfg *Config
	if err := json.Unmarshal(configData, &userCfg); err != nil {
		return fmt.Errorf("failed to parse config file: %w", err)
	}

	updateCfg(userCfg)

	// Write the updated config back to file
	updatedData, err := json.MarshalIndent(userCfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	if err := os.WriteFile(configFile, updatedData, 0o644); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// Get returns the current configuration.
// It's safe to call this function multiple times.
func Get() *Config {
	return cfg
}

// WorkingDirectory returns the current working directory from the configuration.
func WorkingDirectory() string {
	if cfg == nil {
		// Fallback for tests or uninitialized state
		if wd, err := os.Getwd(); err == nil {
			return wd
		}
		return "."
	}
	return cfg.WorkingDir
}

func UpdateAgentModel(agentName AgentName, modelID models.ModelID) error {
	if cfg == nil {
		panic("config not loaded")
	}

	existingAgentCfg := cfg.Agents[agentName]

	model, ok := models.SupportedModels[modelID]
	if !ok {
		return fmt.Errorf("model %s not supported", modelID)
	}

	maxTokens := existingAgentCfg.MaxTokens
	if model.DefaultMaxTokens > 0 {
		maxTokens = model.DefaultMaxTokens
	}

	newAgentCfg := Agent{
		Model:           modelID,
		MaxTokens:       maxTokens,
		ReasoningEffort: existingAgentCfg.ReasoningEffort,
	}
	cfg.Agents[agentName] = newAgentCfg

	if err := validateAgent(cfg, agentName, newAgentCfg); err != nil {
		// revert config update on failure
		cfg.Agents[agentName] = existingAgentCfg
		return fmt.Errorf("failed to update agent model: %w", err)
	}

	return updateCfgFile(func(config *Config) {
		if config.Agents == nil {
			config.Agents = make(map[AgentName]Agent)
		}
		config.Agents[agentName] = newAgentCfg
	})
}

// UpdateTheme updates the theme in the configuration and writes it to the config file.
func UpdateTheme(themeName string) error {
	if cfg == nil {
		return fmt.Errorf("config not loaded")
	}

	// Update the in-memory config
	cfg.TUI.Theme = themeName

	// Update the file config
	return updateCfgFile(func(config *Config) {
		config.TUI.Theme = themeName
	})
}

func GetAuraAuthToken() string {
	if cfg == nil || cfg.AuraAuth == nil {
		return ""
	}
	return cfg.AuraAuth.Token
}

// UpdateAuraAuthToken updates the Aura Auth token in the configuration and writes it to the config file.
func UpdateAuraAuthToken(token string) error {
	if cfg == nil {
		return fmt.Errorf("config not loaded")
	}

	// Update the in-memory config
	if cfg.AuraAuth == nil {
		cfg.AuraAuth = &AuraAuthConfig{
			Token: token,
		}
	} else {
		cfg.AuraAuth.Token = token
	}

	// Update the file config
	return updateCfgFile(func(config *Config) {
		if config.AuraAuth == nil {
			config.AuraAuth = &AuraAuthConfig{
				Token: token,
			}
		} else {
			config.AuraAuth.Token = token
		}
	})
}

// InitializeMock initializes a mock configuration for testing purposes.
func InitializeMock() {
	cfg = &Config{
		Data: Data{
			Directory: os.TempDir(),
		},
		WorkingDir: os.TempDir(),
		Agents: map[AgentName]Agent{
			AgentNormal: {
				Model:     "mock-model",
				MaxTokens: 4096,
			},
		},
		Providers: map[models.ModelProvider]Provider{
			"mock": {APIKey: "mock-key"},
		},
		Models: map[models.ModelID]models.Model{
			"mock-model": {
				ID:       "mock-model",
				Provider: "mock",
			},
		},
	}
}

// Tries to load Github token from all possible locations
func LoadGitHubToken() (string, error) {
	// First check environment variable
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token, nil
	}

	// Get config directory
	var configDir string
	if xdgConfig := os.Getenv("XDG_CONFIG_HOME"); xdgConfig != "" {
		configDir = xdgConfig
	} else if runtime.GOOS == "windows" {
		if localAppData := os.Getenv("LOCALAPPDATA"); localAppData != "" {
			configDir = localAppData
		} else {
			configDir = filepath.Join(os.Getenv("HOME"), "AppData", "Local")
		}
	} else {
		configDir = filepath.Join(os.Getenv("HOME"), ".config")
	}

	// Try both hosts.json and apps.json files
	filePaths := []string{
		filepath.Join(configDir, "github-copilot", "hosts.json"),
		filepath.Join(configDir, "github-copilot", "apps.json"),
	}

	for _, filePath := range filePaths {
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		var config map[string]map[string]interface{}
		if err := json.Unmarshal(data, &config); err != nil {
			continue
		}

		for key, value := range config {
			if strings.Contains(key, "github.com") {
				if oauthToken, ok := value["oauth_token"].(string); ok {
					return oauthToken, nil
				}
			}
		}
	}

	return "", fmt.Errorf("GitHub token not found in standard locations")
}
