package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

const (
	ReloadEnvToolName    = "reload_environment"
	reloadEnvDescription = `Reloads system and user environment variables (e.g., PATH) from the Windows Registry.
Usage:
- Call this tool after installing new software or modifying environment variables to make them available to subsequent 'run_script' calls without restarting the session.
`
)

type ReloadEnvParams struct{}

type reloadEnvTool struct{}

func NewReloadEnvTool() BaseTool {
	return &reloadEnvTool{}
}

func (t *reloadEnvTool) Info() ToolInfo {
	return ToolInfo{
		Name:        ReloadEnvToolName,
		Description: reloadEnvDescription,
		Parameters: map[string]any{
			"properties": map[string]any{}, // No parameters needed
		},
	}
}

func (t *reloadEnvTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	// 1. Get User Environment Variables
	userEnv, err := getEnvFromPowerShell("User")
	if err != nil {
		return NewTextErrorResponse(fmt.Sprintf("Failed to get User environment: %v", err)), nil
	}

	// 2. Get Machine Environment Variables
	machineEnv, err := getEnvFromPowerShell("Machine")
	if err != nil {
		return NewTextErrorResponse(fmt.Sprintf("Failed to get Machine environment: %v", err)), nil
	}

	// 3. Merge and Set (User overrides Machine)
	// We first apply Machine envs, then User envs
	count := 0
	
	// Apply Machine Env
	for k, v := range machineEnv {
		// Special handling for PATH: merge them
		if strings.ToUpper(k) == "PATH" {
			continue // Handle PATH separately
		}
		os.Setenv(k, v)
		count++
	}

	// Apply User Env
	for k, v := range userEnv {
		if strings.ToUpper(k) == "PATH" {
			continue // Handle PATH separately
		}
		os.Setenv(k, v)
		count++
	}

	// Handle PATH merging
	// Windows PATH logic: System PATH + User PATH
	sysPath := machineEnv["Path"]
	if sysPath == "" {
		sysPath = machineEnv["PATH"]
	}
	userPath := userEnv["Path"]
	if userPath == "" {
		userPath = userEnv["PATH"]
	}

	newPath := sysPath
	if userPath != "" {
		if newPath != "" && !strings.HasSuffix(newPath, ";") {
			newPath += ";"
		}
		newPath += userPath
	}

	if newPath != "" {
		os.Setenv("PATH", newPath)
		os.Setenv("Path", newPath) // Set both casing just in case
		count++
	}

	return NewTextResponse(fmt.Sprintf("Successfully reloaded %d environment variables. PATH updated.", count)), nil
}

func getEnvFromPowerShell(target string) (map[string]string, error) {
	// PowerShell command to get env vars as JSON
	psCmd := fmt.Sprintf(`[Environment]::GetEnvironmentVariables("%s") | ConvertTo-Json -Depth 1`, target)
	cmd := exec.Command("powershell", "-NoProfile", "-Command", psCmd)
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var envMap map[string]string
	if err := json.Unmarshal(output, &envMap); err != nil {
		return nil, fmt.Errorf("json unmarshal: %v, output: %s", err, string(output))
	}
	return envMap, nil
}
