package lsp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/auramate/auramate/internal/logging"
)

var dependencyLookPath = exec.LookPath

var dependencyRunCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	return cmd.CombinedOutput()
}

type KnownServerInfo struct {
	Command        string
	InstallCommand string
	DocsURL        string
}

func KnownServerInfos() map[string]KnownServerInfo {
	pythonInstallCommand := "python3 -m pip install --user pyright"
	if runtime.GOOS == "windows" {
		pythonInstallCommand = "py -m pip install --user pyright"
	}

	return map[string]KnownServerInfo{
		"go": {
			Command:        "gopls",
			InstallCommand: "go install golang.org/x/tools/gopls@latest",
			DocsURL:        "https://github.com/golang/tools/blob/master/gopls/README.md",
		},
		"python": {
			Command:        "pyright-langserver",
			InstallCommand: pythonInstallCommand,
			DocsURL:        "https://github.com/microsoft/pyright",
		},
		"typescript": {
			Command:        "typescript-language-server",
			InstallCommand: "npm install -g typescript-language-server typescript",
			DocsURL:        "https://github.com/typescript-language-server/typescript-language-server",
		},
		"rust": {
			Command:        "rust-analyzer",
			InstallCommand: "rustup component add rust-analyzer",
			DocsURL:        "https://rust-analyzer.github.io/",
		},
	}
}

func KnownServerInfoForLanguage(name string) (KnownServerInfo, bool) {
	info, ok := KnownServerInfos()[strings.ToLower(strings.TrimSpace(name))]
	return info, ok
}

// ResolveServerCommand normalizes known LSP launch commands before the client process starts.
// For known LSP servers, it can auto-install missing dependencies and resolve a runnable command.
func ResolveServerCommand(ctx context.Context, name string, command string, args []string) (string, []string, error) {
	kind := knownServerKind(name, command, args)
	if kind == "" {
		return command, args, nil
	}
	if kind != "python" {
		return resolveKnownBinaryServerCommand(ctx, kind, command, args)
	}

	if isPyrightModuleCommand(command, args) {
		return resolvePyrightModuleCommand(ctx, command, args)
	}

	if commandExists(command) {
		return command, args, nil
	}

	pythonCommand, err := findPythonCommand(command, args)
	if err != nil {
		return command, args, fmt.Errorf("pyright langserver not found and no usable Python runtime is available: %w", err)
	}

	if !pyrightModuleAvailable(ctx, pythonCommand) {
		logging.Info("Pyright missing. Attempting automatic install", "python", pythonCommand)
		if err := installPyright(ctx, pythonCommand); err != nil {
			return command, args, err
		}
	}

	if commandExists(command) {
		return command, args, nil
	}

	resolvedArgs := normalizePyrightModuleArgs(args)
	logging.Info("Using Python module fallback for Pyright LSP", "python", pythonCommand, "args", resolvedArgs)
	return pythonCommand, resolvedArgs, nil
}

func IsServerInstalled(ctx context.Context, name string, command string, args []string) bool {
	kind := knownServerKind(name, command, args)
	if kind == "" {
		return commandExists(command)
	}
	if kind != "python" {
		return knownBinaryServerInstalled(ctx, kind, command)
	}

	if isPyrightModuleCommand(command, args) {
		pythonCommand, err := findPythonCommand(command, args)
		if err != nil {
			return false
		}
		return pyrightModuleAvailable(ctx, pythonCommand)
	}

	if commandExists(command) {
		return true
	}

	if !isPyrightServer(name, command, args) {
		return false
	}

	pythonCommand, err := findPythonCommand(command, args)
	if err != nil {
		return false
	}
	return pyrightModuleAvailable(ctx, pythonCommand)
}

func knownServerKind(name string, command string, args []string) string {
	trimmedName := strings.ToLower(strings.TrimSpace(name))
	if _, ok := KnownServerInfoForLanguage(trimmedName); ok {
		return trimmedName
	}

	base := normalizedCommandBase(command)
	switch {
	case strings.Contains(base, "pyright") || isPyrightModuleCommand(command, args):
		return "python"
	case base == "gopls":
		return "go"
	case base == "typescript-language-server":
		return "typescript"
	case base == "rust-analyzer":
		return "rust"
	default:
		return ""
	}
}

func resolveKnownBinaryServerCommand(ctx context.Context, kind string, command string, args []string) (string, []string, error) {
	if commandExists(command) {
		return command, args, nil
	}

	if resolvedPath, ok := resolveKnownBinaryServerPath(ctx, kind); ok {
		return resolvedPath, args, nil
	}

	logging.Info("LSP server missing. Attempting automatic install", "language", kind, "command", command)
	if err := installKnownBinaryServer(ctx, kind); err != nil {
		return command, args, err
	}

	if commandExists(command) {
		return command, args, nil
	}
	if resolvedPath, ok := resolveKnownBinaryServerPath(ctx, kind); ok {
		return resolvedPath, args, nil
	}

	return command, args, fmt.Errorf("%s LSP install completed but command %q is still not runnable", kind, command)
}

func knownBinaryServerInstalled(ctx context.Context, kind string, command string) bool {
	if commandExists(command) {
		return true
	}
	_, ok := resolveKnownBinaryServerPath(ctx, kind)
	return ok
}

func installKnownBinaryServer(ctx context.Context, kind string) error {
	installCtx, cancel := context.WithTimeout(withContext(ctx), 10*time.Minute)
	defer cancel()

	var (
		output []byte
		err    error
	)

	switch kind {
	case "go":
		output, err = dependencyRunCommand(installCtx, "go", "install", "golang.org/x/tools/gopls@latest")
	case "typescript":
		output, err = dependencyRunCommand(installCtx, "npm", "install", "-g", "typescript-language-server", "typescript")
	case "rust":
		output, err = dependencyRunCommand(installCtx, "rustup", "component", "add", "rust-analyzer")
	default:
		return fmt.Errorf("automatic install is not supported for language %q", kind)
	}

	if err != nil {
		return fmt.Errorf("failed to install %s LSP dependencies: %w: %s", kind, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func resolveKnownBinaryServerPath(ctx context.Context, kind string) (string, bool) {
	switch kind {
	case "go":
		return resolveGoLanguageServerPath(ctx)
	case "typescript":
		return resolveTypeScriptLanguageServerPath(ctx)
	case "rust":
		return resolveRustAnalyzerPath(ctx)
	default:
		return "", false
	}
}

func isPyrightServer(name string, command string, args []string) bool {
	base := normalizedCommandBase(command)
	if strings.Contains(base, "pyright") {
		return true
	}
	if isPyrightModuleCommand(command, args) {
		return true
	}
	return strings.EqualFold(name, "python") && strings.Contains(strings.ToLower(strings.Join(args, " ")), "pyright")
}

func isPyrightModuleCommand(command string, args []string) bool {
	if !isPythonExecutable(command) {
		return false
	}
	for i := 0; i < len(args); i++ {
		if args[i] == "-m" && i+1 < len(args) && strings.EqualFold(args[i+1], "pyright.langserver") {
			return true
		}
	}
	for _, arg := range args {
		if strings.Contains(strings.ToLower(arg), "pyright.langserver") {
			return true
		}
	}
	return false
}

func resolvePyrightModuleCommand(ctx context.Context, command string, args []string) (string, []string, error) {
	pythonCommand, err := findPythonCommand(command, args)
	if err != nil {
		return command, args, err
	}

	if !pyrightModuleAvailable(ctx, pythonCommand) {
		logging.Info("Pyright module missing. Attempting automatic install", "python", pythonCommand)
		if err := installPyright(ctx, pythonCommand); err != nil {
			return command, args, err
		}
	}

	return pythonCommand, normalizePyrightModuleArgs(args), nil
}

func commandExists(command string) bool {
	path, err := dependencyLookPath(command)
	return err == nil && path != ""
}

func findPythonCommand(command string, args []string) (string, error) {
	if isPythonExecutable(command) && commandExists(command) {
		return command, nil
	}

	for _, candidate := range pythonCandidates() {
		if commandExists(candidate) {
			return candidate, nil
		}
	}

	return "", exec.ErrNotFound
}

func pythonCandidates() []string {
	if runtime.GOOS == "windows" {
		return []string{"python", "py"}
	}
	return []string{"python3", "python"}
}

func installPyright(ctx context.Context, pythonCommand string) error {
	installCtx, cancel := context.WithTimeout(withContext(ctx), 10*time.Minute)
	defer cancel()

	output, err := dependencyRunCommand(installCtx, pythonCommand, "-m", "pip", "install", "--user", "--disable-pip-version-check", "pyright")
	if err != nil {
		return fmt.Errorf("failed to install pyright via %s: %w: %s", pythonCommand, err, strings.TrimSpace(string(output)))
	}
	return nil
}

func pyrightModuleAvailable(ctx context.Context, pythonCommand string) bool {
	checkCtx, cancel := context.WithTimeout(withContext(ctx), 5*time.Second)
	defer cancel()

	output, err := dependencyRunCommand(checkCtx, pythonCommand, "-c", `import importlib.util, sys; sys.exit(0 if importlib.util.find_spec("pyright.langserver") else 1)`)
	if err != nil {
		logging.Debug("Pyright module probe failed", "python", pythonCommand, "error", err, "output", strings.TrimSpace(string(output)))
		return false
	}
	return true
}

func normalizePyrightModuleArgs(args []string) []string {
	resolvedArgs := append([]string(nil), args...)
	if !isPyrightModuleArgs(resolvedArgs) {
		resolvedArgs = append([]string{"-m", "pyright.langserver"}, resolvedArgs...)
	}
	if !hasArg(resolvedArgs, "--stdio") {
		resolvedArgs = append(resolvedArgs, "--stdio")
	}
	return resolvedArgs
}

func isPyrightModuleArgs(args []string) bool {
	for i := 0; i < len(args); i++ {
		if args[i] == "-m" && i+1 < len(args) && strings.EqualFold(args[i+1], "pyright.langserver") {
			return true
		}
	}
	return false
}

func hasArg(args []string, target string) bool {
	for _, arg := range args {
		if arg == target {
			return true
		}
	}
	return false
}

func isPythonExecutable(command string) bool {
	base := normalizedCommandBase(command)
	return base == "python" || base == "python3" || base == "py"
}

func resolveGoLanguageServerPath(ctx context.Context) (string, bool) {
	for _, envName := range []string{"GOBIN", "GOPATH"} {
		output, err := dependencyRunCommand(withContext(ctx), "go", "env", envName)
		if err != nil {
			continue
		}
		value := strings.TrimSpace(string(output))
		if value == "" {
			continue
		}
		if envName == "GOPATH" {
			value = filepath.Join(value, "bin")
		}
		candidate := filepath.Join(value, executableName("gopls"))
		if fileExists(candidate) {
			return candidate, true
		}
	}
	return "", false
}

func resolveTypeScriptLanguageServerPath(ctx context.Context) (string, bool) {
	output, err := dependencyRunCommand(withContext(ctx), "npm", "prefix", "-g")
	if err != nil {
		return "", false
	}
	prefix := strings.TrimSpace(string(output))
	if prefix == "" {
		return "", false
	}

	binDir := prefix
	if runtime.GOOS != "windows" {
		binDir = filepath.Join(prefix, "bin")
	}
	candidate := filepath.Join(binDir, executableName("typescript-language-server"))
	if fileExists(candidate) {
		return candidate, true
	}
	return "", false
}

func resolveRustAnalyzerPath(ctx context.Context) (string, bool) {
	output, err := dependencyRunCommand(withContext(ctx), "rustup", "which", "rust-analyzer")
	if err != nil {
		return "", false
	}
	candidate := strings.TrimSpace(string(output))
	if candidate == "" || !fileExists(candidate) {
		return "", false
	}
	return candidate, true
}

func executableName(name string) string {
	if runtime.GOOS == "windows" {
		switch name {
		case "typescript-language-server":
			return name + ".cmd"
		default:
			return name + ".exe"
		}
	}
	return name
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func normalizedCommandBase(command string) string {
	base := strings.ToLower(filepath.Base(command))
	return strings.TrimSuffix(base, filepath.Ext(base))
}

func withContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}
