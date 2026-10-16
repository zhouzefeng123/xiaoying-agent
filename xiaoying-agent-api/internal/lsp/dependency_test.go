package lsp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestResolveServerCommandKeepsExistingPyrightBinary(t *testing.T) {
	originalLookPath := dependencyLookPath
	originalRunCommand := dependencyRunCommand
	defer func() {
		dependencyLookPath = originalLookPath
		dependencyRunCommand = originalRunCommand
	}()

	dependencyLookPath = func(command string) (string, error) {
		if command == "pyright-langserver" {
			return command, nil
		}
		return "", exec.ErrNotFound
	}
	dependencyRunCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		t.Fatalf("did not expect any installation command, got %s %v", command, args)
		return nil, nil
	}

	command, args, err := ResolveServerCommand(context.Background(), "python", "pyright-langserver", []string{"--stdio"})
	if err != nil {
		t.Fatalf("ResolveServerCommand returned error: %v", err)
	}
	if command != "pyright-langserver" {
		t.Fatalf("expected original command, got %q", command)
	}
	if !reflect.DeepEqual(args, []string{"--stdio"}) {
		t.Fatalf("expected original args, got %v", args)
	}
}

func TestResolveServerCommandInstallsPyrightAndFallsBackToPythonModule(t *testing.T) {
	originalLookPath := dependencyLookPath
	originalRunCommand := dependencyRunCommand
	defer func() {
		dependencyLookPath = originalLookPath
		dependencyRunCommand = originalRunCommand
	}()

	installed := false
	dependencyLookPath = func(command string) (string, error) {
		switch command {
		case "python":
			return command, nil
		case "pyright-langserver":
			return "", exec.ErrNotFound
		default:
			return "", exec.ErrNotFound
		}
	}
	dependencyRunCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command != "python" {
			t.Fatalf("expected python runner, got %s", command)
		}
		if reflect.DeepEqual(args, []string{"-m", "pip", "install", "--user", "--disable-pip-version-check", "pyright"}) {
			installed = true
			return []byte("installed"), nil
		}
		if len(args) == 2 && args[0] == "-c" {
			if installed {
				return nil, nil
			}
			return nil, errors.New("module missing")
		}
		return nil, errors.New("unexpected command")
	}

	command, args, err := ResolveServerCommand(context.Background(), "python", "pyright-langserver", nil)
	if err != nil {
		t.Fatalf("ResolveServerCommand returned error: %v", err)
	}
	if command != "python" {
		t.Fatalf("expected python module fallback, got %q", command)
	}
	if !reflect.DeepEqual(args, []string{"-m", "pyright.langserver", "--stdio"}) {
		t.Fatalf("unexpected fallback args: %v", args)
	}
	if !installed {
		t.Fatalf("expected pyright installation to be attempted")
	}
}

func TestIsServerInstalledDetectsPyrightModuleWithoutBinary(t *testing.T) {
	originalLookPath := dependencyLookPath
	originalRunCommand := dependencyRunCommand
	defer func() {
		dependencyLookPath = originalLookPath
		dependencyRunCommand = originalRunCommand
	}()

	dependencyLookPath = func(command string) (string, error) {
		switch command {
		case "python":
			return command, nil
		case "pyright-langserver":
			return "", exec.ErrNotFound
		default:
			return "", exec.ErrNotFound
		}
	}
	dependencyRunCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command != "python" {
			t.Fatalf("expected python runner, got %s", command)
		}
		if len(args) == 2 && args[0] == "-c" {
			return nil, nil
		}
		return nil, errors.New("unexpected command")
	}

	if !IsServerInstalled(context.Background(), "python", "pyright-langserver", nil) {
		t.Fatalf("expected IsServerInstalled to detect module-based pyright installation")
	}
}

func TestResolveServerCommandInstallsKnownBinaryServers(t *testing.T) {
	tests := []struct {
		name           string
		language       string
		command        string
		args           []string
		installCommand string
		installArgs    []string
		resolveProbe   func(command string, args []string) (string, bool)
	}{
		{
			name:           "go",
			language:       "go",
			command:        "gopls",
			installCommand: "go",
			installArgs:    []string{"install", "golang.org/x/tools/gopls@latest"},
			resolveProbe: func(command string, args []string) (string, bool) {
				if command == "go" && len(args) == 2 && args[0] == "env" && args[1] == "GOBIN" {
					return filepath.Join("C:/tools", executableName("gopls")), true
				}
				return "", false
			},
		},
		{
			name:           "typescript",
			language:       "typescript",
			command:        "typescript-language-server",
			args:           []string{"--stdio"},
			installCommand: "npm",
			installArgs:    []string{"install", "-g", "typescript-language-server", "typescript"},
			resolveProbe: func(command string, args []string) (string, bool) {
				if command == "npm" && len(args) == 2 && args[0] == "prefix" && args[1] == "-g" {
					prefix := "/usr/local"
					if strings.EqualFold(filepath.VolumeName(prefix), "") && executableName("typescript-language-server") == "typescript-language-server.cmd" {
						prefix = "C:/npm"
					}
					binDir := prefix
					if executableName("typescript-language-server") != "typescript-language-server.cmd" {
						binDir = filepath.Join(prefix, "bin")
					}
					return filepath.Join(binDir, executableName("typescript-language-server")), true
				}
				return "", false
			},
		},
		{
			name:           "rust",
			language:       "rust",
			command:        "rust-analyzer",
			installCommand: "rustup",
			installArgs:    []string{"component", "add", "rust-analyzer"},
			resolveProbe: func(command string, args []string) (string, bool) {
				if command == "rustup" && len(args) == 2 && args[0] == "which" && args[1] == "rust-analyzer" {
					return filepath.Join("/tmp", executableName("rust-analyzer")), true
				}
				return "", false
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			originalLookPath := dependencyLookPath
			originalRunCommand := dependencyRunCommand
			defer func() {
				dependencyLookPath = originalLookPath
				dependencyRunCommand = originalRunCommand
			}()

			installed := false
			dependencyLookPath = func(command string) (string, error) {
				switch command {
				case tc.installCommand:
					return command, nil
				case tc.command:
					if installed {
						return command, nil
					}
					return "", exec.ErrNotFound
				default:
					return "", exec.ErrNotFound
				}
			}

			dependencyRunCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
				if command == tc.installCommand && reflect.DeepEqual(args, tc.installArgs) {
					installed = true
					return []byte("installed"), nil
				}
				if output, ok := tc.resolveProbe(command, args); ok {
					return []byte(output), nil
				}
				return nil, errors.New("unexpected command")
			}

			resolvedCommand, resolvedArgs, err := ResolveServerCommand(context.Background(), tc.language, tc.command, tc.args)
			if err != nil {
				t.Fatalf("ResolveServerCommand returned error: %v", err)
			}
			if resolvedCommand != tc.command {
				t.Fatalf("expected original command %q, got %q", tc.command, resolvedCommand)
			}
			if !reflect.DeepEqual(resolvedArgs, tc.args) {
				t.Fatalf("expected args %v, got %v", tc.args, resolvedArgs)
			}
			if !installed {
				t.Fatalf("expected install command to run for %s", tc.language)
			}
		})
	}
}

func TestIsServerInstalledDetectsKnownBinaryOutsidePath(t *testing.T) {
	originalLookPath := dependencyLookPath
	originalRunCommand := dependencyRunCommand
	defer func() {
		dependencyLookPath = originalLookPath
		dependencyRunCommand = originalRunCommand
	}()

	tempDir := t.TempDir()
	binaryPath := filepath.Join(tempDir, executableName("gopls"))
	if err := os.WriteFile(binaryPath, []byte("stub"), 0o755); err != nil {
		t.Fatalf("failed to create stub gopls binary: %v", err)
	}

	dependencyLookPath = func(command string) (string, error) {
		switch command {
		case "go":
			return command, nil
		case "gopls":
			return "", exec.ErrNotFound
		default:
			return "", exec.ErrNotFound
		}
	}
	dependencyRunCommand = func(ctx context.Context, command string, args ...string) ([]byte, error) {
		if command == "go" && len(args) == 2 && args[0] == "env" && args[1] == "GOBIN" {
			return []byte(tempDir), nil
		}
		return nil, errors.New("unexpected command")
	}

	if !IsServerInstalled(context.Background(), "go", "gopls", nil) {
		t.Fatalf("expected IsServerInstalled to detect gopls outside PATH")
	}
}
