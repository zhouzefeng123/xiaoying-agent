package prompt

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/models"
)

func CoderPrompt(provider models.ModelProvider) string {
	// Unified optimized prompt for all providers
	// ORDER: [Standard Base] -> [Tooling/Standards] -> [Coder Specific Protocols] -> [LSP] -> [Dynamic]

	// Standard Base (Role, Thinking, Metacognition, Loyalty, Performance, Cleanup)
	base := GetBaseSystemPrompt()

	// Tooling & Safety (Shared with Worker)
	base += "\n\n" + ToolingAndSafety
	base += "\n\n" + CodingStandards

	// Coder Specific Protocols (Not Shared)
	// Add back protocols that were removed from Base but are critical for Coder
	base += "\n\n" + VerificationProtocol
	base += "\n\n" + DebuggingProtocol // Critical for anti-paralysis

	base += "\n\n" + TodoProtocol
	base += "\n\n" + InstallationProtocol
	base += "\n\n" + MemoryProtocol
	base += "\n\n" + ManagerProtocol
	base += "\n\n" + DelegateProtocol

	// Environment info (Time, CWD, OS) is dynamic, so it MUST go last.
	// REFACTOR: Moved to GetDynamicInstruction in dynamic.go to support Static Prefix Caching.
	return fmt.Sprintf("%s\n\n%s\n\n**FINAL CHECK**:\n1. Does the user's input contain a stable preference, standing instruction, project convention, or default workflow? If yes, call 'manage_core_memory' FIRST.\n2. Did you just learn a reusable pitfall, fix, or environment constraint from trial and error? If yes, record it as 'experience' via 'manage_core_memory' before finishing.\n3. Do not store temporary one-off details or noisy logs as memory.\n4. If the user explicitly provides a durable secret, credential, code, or ID that is likely needed again, you may store it as a 'fact'.", base, GetLSPInformation())
}

const ToolingAndSafety = `
# Skill Creation (Advanced)
- **Trigger**: "frequently used" or "reusable" logic.
- **Action**: Use 'skill-generator' to create permanent skills in SYSTEM dir.
  1. Write Python code.
  2. Call 'manage_tools(load, skill-generator)'.
  3. Call 'skill-generator'.`

const CodingStandards = `
# Standards
- **Code**: Complete (No TODOs). Handle errors. Modern style.
- **Robustness**: Adaptive Strategy (e.g. IMAP -> POP3 on failure).
- **Tool Limits**: Before generating code or patch payloads, estimate the payload size first. To prevent API JSON truncation errors, any single edit/write payload MUST stay within 50% of the output token limit. Treat 8192 max output tokens as a hard planning cap of 4096 tokens per payload. You MUST NOT generate more than 500 lines of code in a single 'write' or 'patch' argument. If you need to write/edit a large file, you MUST split the work into multiple smaller 'patch'/'edit' calls or other chunked operations.
- **Read Efficiency**: Pay attention to the line range hints in 'read' output (e.g. [读取范围: 第 1-200 行]). Do NOT re-read lines you have already seen. If you need specific content, use 'grep' to locate it first, then read the exact range. If the system tells you content was hidden because you already read it, trust it and move on.
- **Large Refactors**: For multi-file refactors or prompts that list target files, make an explicit checklist from the request, inspect every target file before the first write, and keep going until every required path is changed or explicitly ruled out with evidence.
- **Verification Loop**: Run the user's requested verification commands yourself, preserving each command exactly unless the shell requires only a path separator adjustment. For Python or other syntax-sensitive edits, run a fast syntax check before finalizing. Do not report success after failed or skipped verification; fix and rerun, or report the exact blocker.
- **Shell Discipline**: On Windows PowerShell, separate commands with ';' instead of '&&'. After any failed verification, read the reported code area and current diff before editing again; repeated blind single-line edits are not acceptable.`

var (
	envCacheMu sync.RWMutex
	envCache   *envCacheData
)

// ensureStrongInitialEnvInjection blocks briefly on first requests if env cache
// is still using placeholder values. This guarantees initial prompt injection
// has real time/location context whenever possible.
func ensureStrongInitialEnvInjection() {
	envCacheMu.RLock()
	needsRefresh := envCache == nil || envCache.location == "Unknown" || envCache.shell == "Unknown"
	envCacheMu.RUnlock()

	if !needsRefresh {
		return
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		updateEnvCache()
	}()

	select {
	case <-done:
	case <-time.After(1500 * time.Millisecond):
		// Non-fatal timeout: keep serving with best-effort cached values.
	}
}

type envCacheData struct {
	osVersion string
	shell     string
	location  string
	tools     string
	fetchedAt time.Time
}

func init() {
	// Initialize cache with safe defaults
	envCache = &envCacheData{
		osVersion: runtime.GOOS,
		shell:     "Unknown",
		location:  "Unknown",
		tools:     "Unknown",
		fetchedAt: time.Now(),
	}

	// Trigger async update immediately
	go updateEnvCache()
}

func updateEnvCache() {
	// 1. Get OS Version (Heavy on Windows)
	osVer := runtime.GOOS
	switch runtime.GOOS {
	case "windows":
		// Optimization: Use cmd.exe which is faster than PowerShell startup
		out, err := exec.Command("cmd", "/c", "ver").Output()
		if err == nil {
			osVer = strings.TrimSpace(string(out))
		}
	case "darwin":
		out, err := exec.Command("sw_vers", "-productVersion").Output()
		if err == nil {
			osVer = "macOS " + strings.TrimSpace(string(out))
		}
	case "linux":
		out, err := exec.Command("uname", "-sr").Output()
		if err == nil {
			osVer = strings.TrimSpace(string(out))
		}
	}

	// 2. Get Shell
	shell := os.Getenv("SHELL")
	if shell == "" {
		if runtime.GOOS == "windows" {
			shell = "PowerShell/CMD"
		} else {
			shell = "Unknown"
		}
	}

	// 3. Get Location (Network call with fallback)
	location := fetchLocation()

	// 4. Detect Core Tools
	detectedTools := detectCoreTools()

	// Update Cache safely
	envCacheMu.Lock()
	envCache = &envCacheData{
		osVersion: osVer,
		shell:     shell,
		location:  location,
		tools:     detectedTools,
		fetchedAt: time.Now(),
	}
	envCacheMu.Unlock()
}

func detectCoreTools() string {
	toolsToCheck := []string{
		"python", "python3", "pip", "pip3",
		"node", "npm", "yarn", "pnpm",
		"go", "git", "docker", "make", "gcc", "g++", "java", "javac", "mvn", "gradle", "rustc", "cargo",
	}

	var foundTools []string
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, tool := range toolsToCheck {
		wg.Add(1)
		go func(t string) {
			defer wg.Done()
			if _, err := exec.LookPath(t); err == nil {
				mu.Lock()
				foundTools = append(foundTools, t)
				mu.Unlock()
			}
		}(tool)
	}

	wg.Wait()

	if len(foundTools) == 0 {
		return "None detected"
	}

	// Sort to ensure stable output for prompt caching
	sort.Strings(foundTools)
	return strings.Join(foundTools, ", ")
}

func fetchLocation() string {
	client := http.Client{Timeout: 5 * time.Second}

	// 1. Try ip-api.com (Good for China/Global, supports language)
	// We try this first as it is often faster/unblocked in CN compared to ipinfo
	{
		var ipInfo struct {
			City    string `json:"city"`
			Region  string `json:"regionName"`
			Country string `json:"country"`
		}
		resp, err := client.Get("http://ip-api.com/json/?lang=zh-CN")
		if err == nil {
			defer resp.Body.Close()
			if json.NewDecoder(resp.Body).Decode(&ipInfo) == nil {
				return fmt.Sprintf("%s, %s, %s", ipInfo.City, ipInfo.Region, ipInfo.Country)
			}
		}
	}

	// 2. Try ipinfo.io (Standard)
	{
		var ipInfo struct {
			City    string `json:"city"`
			Region  string `json:"region"`
			Country string `json:"country"`
		}
		resp, err := client.Get("https://ipinfo.io/json")
		if err == nil {
			defer resp.Body.Close()
			if json.NewDecoder(resp.Body).Decode(&ipInfo) == nil {
				return fmt.Sprintf("%s, %s, %s", ipInfo.City, ipInfo.Region, ipInfo.Country)
			}
		}
	}

	// 3. Try ip.sb (Backup)
	{
		var ipInfo struct {
			City    string `json:"city"`
			Region  string `json:"region"`
			Country string `json:"country"`
		}
		resp, err := client.Get("https://api.ip.sb/geoip")
		if err == nil {
			defer resp.Body.Close()
			if json.NewDecoder(resp.Body).Decode(&ipInfo) == nil {
				return fmt.Sprintf("%s, %s, %s", ipInfo.City, ipInfo.Region, ipInfo.Country)
			}
		}
	}

	return "Unknown"
}

func GetEnvironmentInfo() string {
	return GetEnvironmentInfoForWorkspace(config.WorkingDirectory())
}

func GetEnvironmentInfoForWorkspace(workspace string) string {
	// Strong initial injection: try to refresh env cache before first prompt usage.
	ensureStrongInitialEnvInjection()

	cwd := workspace
	if strings.TrimSpace(cwd) == "" {
		cwd = config.WorkingDirectory()
	}
	now := time.Now()
	zoneName, zoneOffset := now.Zone()
	offsetHours := zoneOffset / 3600
	offsetMinutes := zoneOffset % 3600
	if offsetMinutes < 0 {
		offsetMinutes = -offsetMinutes
	}
	offsetMinutes = offsetMinutes / 60

	username := os.Getenv("USERNAME")
	if username == "" {
		username = os.Getenv("USER")
	}

	envCacheMu.RLock()
	osVersion := envCache.osVersion
	shell := envCache.shell
	location := envCache.location
	installedTools := envCache.tools
	envCacheMu.RUnlock()

	return fmt.Sprintf(`Environment:
<env>
OS: %s
Shell: %s
CWD: %s
ProjectRoot: %s
User: %s
Time: %s
Timezone: %s (UTC%+03d:%02d)
Loc: %s
Installed Tools: %s
</env>

%s

**CRITICAL RULE: Working Directory**
- You MUST execute all commands and file operations relative to 'CWD' (<env>CWD</env>).
- DO NOT assume you are in 'ProjectRoot' if 'CWD' is different.
- DO NOT use 'cd' to change directory unless explicitly necessary. Use absolute paths or paths relative to CWD.
- When creating files, ensure the target directory exists.
`, osVersion, shell, cwd, config.Get().ProjectRoot, username, now.Format("2006-01-02 15:04:05"), zoneName, offsetHours, offsetMinutes, location, installedTools, getOSSpecificRules(osVersion))
}

func getOSSpecificRules(osVer string) string {
	osVer = strings.ToLower(osVer)
	if strings.Contains(osVer, "windows") {
		return `**Windows Environment Rules (MANDATORY)**
1. **Shell**: Use PowerShell syntax ('$env:VAR="val"'; 'Invoke-Item').
2. **Path**: Use forward slashes '/' or escaped '\\'.
3. **Python**: Set '$env:PYTHONIOENCODING="utf-8"' before running scripts.
4. **GUI**: 'Start-Process' opens in background. If user says "Not Opened", check taskbar. DO NOT modify file content.
5. **Cleanup**: Run 'Remove-Item "test_*", "tmp_*"' to delete your temp scripts (Ensure you named them with prefix!).`
	} else if strings.Contains(osVer, "darwin") {
		return `**macOS Environment Rules**
1. **Open**: Use 'open' command.
2. **Shell**: Zsh/Bash compatible.
3. **Cleanup**: Run 'rm test_* tmp_*'.`
	} else {
		return `**Linux Environment Rules**
1. **Open**: Use 'xdg-open' (if available).
2. **Shell**: Bash compatible.
3. **Cleanup**: Run 'rm test_* tmp_*'.`
	}
}

func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

func GetLSPInformation() string {
	effectiveLSPConfigs := config.EffectiveLSPConfigs(config.Get())
	hasLSP := false
	for _, v := range effectiveLSPConfigs {
		if !v.Disabled {
			hasLSP = true
			break
		}
	}
	if !hasLSP {
		return ""
	}
	return `# LSP Information
Tools that support it will also include useful diagnostics such as linting and typechecking.
- These diagnostics will be automatically enabled when you run the tool, and will be displayed in the output at the bottom within the <file_diagnostics></file_diagnostics> and <project_diagnostics></project_diagnostics> tags.
- Take necessary actions to fix the issues.
- You should ignore diagnostics of files that you did not change or are not related or caused by your changes unless the user explicitly asks you to fix them.
`
}

func boolToYesNo(b bool) string {
	if b {
		return "Yes"
	}
	return "No"
}
