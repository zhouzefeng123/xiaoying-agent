package skills

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/llm/tools"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/sys"
)

// Force reload for skill updates
var progressRegex = regexp.MustCompile(`(\d{1,3}(?:\.\d+)?)%`)

func appendPythonUTF8Env(env []string) []string {
	return append(env,
		"PYTHONIOENCODING=utf-8",
		"PYTHONUTF8=1",
	)
}

type Manager struct {
	skillDirs []string
	parsers   []Parser
	skills    map[string]*SkillDefinition
	mu        sync.RWMutex
}

// Refresh triggers a manual scan of all skill directories
func (m *Manager) Refresh() {
	logging.Info("Manually refreshing skills...")
	m.Scan()
	logging.Info("Skills refresh complete", "count", len(m.skills))
}

func NewManager(skillsDirs ...string) *Manager {
	// Automatically add the user's workspace custom_skills directory
	// And standard locations: .trae/skills

	// 1. .trae/skills (Standard Local Skills) - REMOVED for release
	// We rely on relative paths and standard config locations now.

	// 2. workspace/custom_skills (User Defined)
	// Check if config is initialized to avoid panic
	cfg := config.Get()
	wd := "."
	if cfg != nil && cfg.WorkingDir != "" {
		wd = cfg.WorkingDir
	}

	// Unified Skills Directory
	// All skills (system and custom) are located in the 'system_skills' directory
	// Use configured SystemSkillsDir if available, AND fallback to finding it
	if cfg != nil && cfg.SystemSkillsDir != "" {
		skillsDirs = append(skillsDirs, cfg.SystemSkillsDir)
		logging.Debug("Added configured system skills directory", "path", cfg.SystemSkillsDir)
	}

	// Also try to find it via heuristic to support legacy paths or dev environment quirks
	// NOTE: We only look for "system_skills", not ".trae/skills" to avoid loading Trae IDE internal skills
	foundDir := findSkillsDirectory(wd, "system_skills")
	if foundDir != "" && (cfg == nil || foundDir != cfg.SystemSkillsDir) {
		skillsDirs = append(skillsDirs, foundDir)
		logging.Debug("Added discovered system skills directory", "path", foundDir)
	}

	return &Manager{
		skillDirs: skillsDirs,
		parsers: []Parser{
			&ClaudeSkillParser{},
			// &PythonParser{}, // Removed legacy parser
			// &PowerShellParser{}, // Removed legacy parser
		},
		skills: make(map[string]*SkillDefinition),
	}
}

func (m *Manager) resolveSkillKeyUnlocked(name string) (string, *SkillDefinition, bool) {
	if skill, ok := m.skills[name]; ok {
		return name, skill, true
	}

	lowerName := strings.ToLower(name)
	for key, skill := range m.skills {
		if strings.ToLower(key) == lowerName {
			return key, skill, true
		}
	}

	for key, skill := range m.skills {
		baseDir := filepath.Base(filepath.Dir(skill.ScriptPath))
		if strings.EqualFold(baseDir, name) {
			return key, skill, true
		}
	}

	return "", nil, false
}

func (m *Manager) resolveSkillKey(name string) (string, *SkillDefinition, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.resolveSkillKeyUnlocked(name)
}

// DeleteSkill removes a skill by name and deletes its source file
func (m *Manager) DeleteSkill(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	key, skill, exists := m.resolveSkillKeyUnlocked(name)
	if !exists {
		return fmt.Errorf("skill not found: %s", name)
	}

	// For SOTA skills, if ScriptPath points to skill.md, we should delete the parent directory
	// If it's a single script file, just delete the file.
	// We distinguish by checking if the file is named "skill.md" or "SKILL.md"
	base := strings.ToLower(filepath.Base(skill.ScriptPath))
	if base == "skill.md" {
		// It's a skill package, delete the directory
		dir := filepath.Dir(skill.ScriptPath)
		if err := os.RemoveAll(dir); err != nil {
			return fmt.Errorf("failed to delete skill directory: %w", err)
		}
		logging.Info("Deleted skill directory", "name", name, "path", dir)
	} else {
		// Single file script
		if err := os.Remove(skill.ScriptPath); err != nil {
			return fmt.Errorf("failed to delete skill file: %w", err)
		}
		logging.Info("Deleted skill file", "name", name, "path", skill.ScriptPath)
	}

	// Remove from memory
	delete(m.skills, key)

	return nil
}

// StartWatching starts scanning and watching the skills directory
func (m *Manager) StartWatching(ctx context.Context) {
	// Ensure directories exist
	for _, dir := range m.skillDirs {
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			os.MkdirAll(dir, 0755)
		}
	}

	// Initial scan
	m.Scan()

	// Simple polling watcher for now (every 10 seconds)
	// In production, fsnotify would be better
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				m.Scan()
			}
		}
	}()
}

func (m *Manager) Scan() {
	newSkills := make(map[string]*SkillDefinition)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, dir := range m.skillDirs {
		f, _ := os.OpenFile("scan_debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		fmt.Fprintf(f, "Scanning skills dir: %s\n", dir)
		f.Close()

		// We walk sequentially because filepath.WalkDir is not thread-safe if we modify shared state
		// But we can parse files in parallel if we collect paths first
		var filesToParse []string

		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}

			if d.IsDir() {
				// Skip hidden directories like .git or __pycache__
				if strings.HasPrefix(d.Name(), ".") || d.Name() == "__pycache__" {
					return filepath.SkipDir
				}

				// SOTA Check: Look for skill.md inside this directory
				// If found, treat this directory as a Skill Package
				// NOTE: We prioritize SKILL.md and treat the directory as a package.
				// This means we will parse SKILL.md and IGNORE other .py files in this directory
				// to avoid duplicate definitions (e.g. if SKILL.md points to script.py, we shouldn't parse script.py separately).

				skillMdPath := filepath.Join(path, "skill.md")
				foundPackage := false
				if _, err := os.Stat(skillMdPath); err == nil {
					// Found a skill package!
					filesToParse = append(filesToParse, skillMdPath)
					foundPackage = true
				}

				// Also check for SKILL.md (case insensitive on Windows but good practice)
				if !foundPackage {
					skillMdPathUpper := filepath.Join(path, "SKILL.md")
					if _, err := os.Stat(skillMdPathUpper); err == nil {
						// Check if we already added this path via skill.md (case insensitive check)
						alreadyAdded := false
						for _, existing := range filesToParse {
							if strings.EqualFold(existing, skillMdPathUpper) {
								alreadyAdded = true
								break
							}
						}
						if !alreadyAdded {
							filesToParse = append(filesToParse, skillMdPathUpper)
							foundPackage = true
						}
					}
				}

				if foundPackage {
					// Skip scanning inside this directory to avoid duplicate definitions from scripts
					return filepath.SkipDir
				}

				return nil
			}

			// Only add if not already covered by a skill package logic above
			// (which returns SkipDir so we won't reach here for files inside a package)
			// filesToParse = append(filesToParse, path)
			// As user requested: ONLY load skill.md files, do NOT load standalone scripts.
			if strings.EqualFold(d.Name(), "skill.md") {
				// Check for duplicates before adding
				for _, existing := range filesToParse {
					if strings.EqualFold(existing, path) {
						return nil
					}
				}
				filesToParse = append(filesToParse, path)
			}
			return nil
		})

		if err != nil {
			logging.Error("Failed to walk skills directory", "dir", dir, "error", err)
			continue
		}

		for _, path := range filesToParse {
			wg.Add(1)
			go func(p string) {
				defer wg.Done()
				for _, parser := range m.parsers {
					if parser.CanParse(p) {
						{
							f, _ := os.OpenFile("scan_debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
							fmt.Fprintf(f, "Parsing file: %s\n", p)
							f.Close()
						}

						skill, err := parser.Parse(p)
						if err != nil {
							{
								f, _ := os.OpenFile("scan_debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
								fmt.Fprintf(f, "Failed to parse skill %s: %v\n", p, err)
								f.Close()
							}
							// logging.Warn("Failed to parse skill", "path", p, "error", err)
							return
						}
						mu.Lock()
						// If duplicate name, overwrite (or log warning)
						newSkills[skill.Name] = skill
						{
							f, _ := os.OpenFile("scan_debug.log", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
							fmt.Fprintf(f, "Loaded skill: %s\n", skill.Name)
							f.Close()
						}
						mu.Unlock()
						// logging.Debug("Loaded skill", "name", skill.Name, "path", p)
						break
					}
				}
			}(path)
		}
		wg.Wait()
	}

	m.mu.Lock()
	m.skills = newSkills
	m.mu.Unlock()
}

// ToggleSkill toggles the default active state of a skill
func (m *Manager) ToggleSkill(name string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	skill, exists := m.skills[name]
	if !exists {
		return false, fmt.Errorf("skill not found: %s", name)
	}

	// Toggle state
	skill.DefaultActive = !skill.DefaultActive
	newState := skill.DefaultActive

	logging.Info("Toggled skill active state", "name", name, "active", newState)
	return newState, nil
}

// GetDefaultActiveSkills returns a list of skill names that should be active by default
func (m *Manager) GetDefaultActiveSkills() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var active []string
	for _, skill := range m.skills {
		if skill.DefaultActive {
			active = append(active, skill.Name)
		}
	}
	return active
}

// GetSkill returns the raw skill definition
func (m *Manager) GetSkill(name string) (*SkillDefinition, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	skill, ok := m.skills[name]
	return skill, ok
}

// GetTools returns all skills as tools
func (m *Manager) GetTools() []tools.BaseTool {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var toolList []tools.BaseTool
	for _, skill := range m.skills {
		toolList = append(toolList, &SkillTool{
			def: skill,
		})
	}
	return toolList
}

func (m *Manager) LoadTool(name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Since we scan everything automatically, "load" might just be a check
	// Or we could implement lazy loading. For now, we just check if it exists in scanned skills
	if _, ok := m.skills[name]; !ok {
		return fmt.Errorf("skill '%s' not found", name)
	}
	return nil
}

func (m *Manager) GetTool(name string) (tools.BaseTool, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	skill, ok := m.skills[name]
	if !ok {
		return nil, false
	}
	return &SkillTool{def: skill}, true
}

func (m *Manager) ListAvailableTools() []tools.ToolInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	var infos []tools.ToolInfo
	for _, skill := range m.skills {
		info := skill.ToToolInfo()
		info.Active = skill.DefaultActive
		infos = append(infos, info)
	}
	return infos
}

func (m *Manager) InspectTool(name string) (string, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	skill, ok := m.skills[name]
	if !ok {
		return "", fmt.Errorf("skill '%s' not found", name)
	}
	return fmt.Sprintf("Name: %s\nDescription: %s\nPath: %s\nLanguage: %s\n", skill.Name, skill.Description, skill.ScriptPath, skill.Language), nil
}

func (m *Manager) IsLoaded(name string) bool {
	// In this simple manager, everything scanned is "loaded" (available)
	m.mu.RLock()
	defer m.mu.RUnlock()
	_, ok := m.skills[name]
	return ok
}

// SkillTool adapts a SkillDefinition to the BaseTool interface
type SkillTool struct {
	def *SkillDefinition
}

func (t *SkillTool) Info() tools.ToolInfo {
	return t.def.ToToolInfo()
}

func (t *SkillTool) Run(ctx context.Context, params tools.ToolCall) (tools.ToolResponse, error) {
	// Construct command to run the script
	var cmd *exec.Cmd

	// Convert params JSON input to command line arguments
	// For simplicity, we'll pass arguments as environment variables or positional args
	// Here we use a simple convention: --argName value

	// Parse input JSON to map
	// Note: params.Input is a JSON string
	// But BaseTool.Run signature in tools.go receives ToolCall which has Input string
	// We need to parse it if we want to extract args.
	// However, for this MVP, let's assume we pass named arguments.

	// Better approach: pass the entire JSON as a single argument or via stdin
	// Let's use stdin or an env var for the input JSON

	// If it's a document-only skill, just return the instruction manual to the Agent
	if t.def.Language == "markdown" || t.def.CallFormat == "document" {
		instruction := fmt.Sprintf(
			"=== SKILL DOCUMENTATION: %s ===\n\n"+
				"This is a DOCUMENT-ONLY skill. The framework does not execute it for you.\n"+
				"You MUST read the following documentation and use your own atomic tools (like RunCommand, WriteFile, HttpRequest, etc.) to accomplish the user's request.\n\n"+
				"%s",
			t.def.Name, t.def.Content)
		return tools.NewTextResponse(instruction), nil
	}

	// Prepare arguments
	var args []string
	if t.def.Language == "python" {
		args = append(args, t.def.ScriptPath)
	} else if t.def.Language == "powershell" {
		args = append(args, "-File", t.def.ScriptPath)
	} else {
		return tools.NewTextErrorResponse("Unsupported language"), nil
	}

	// Parse parameters and add to args
	if params.Input != "" {
		var inputMap map[string]interface{}
		// Try parsing as JSON object
		if err := json.Unmarshal([]byte(params.Input), &inputMap); err == nil {
			for k, v := range inputMap {
				// Handle different types
				switch val := v.(type) {
				case bool:
					// Determine argument style based on detection or default to flag
					style := "flag"
					if t.def.ArgStyles != nil {
						if s, ok := t.def.ArgStyles[k]; ok {
							style = s
						}
					}

					if style == "value" {
						// Explicit value passing: --key true or --key false
						// Used when script has type=bool or expects a string value
						args = append(args, fmt.Sprintf("--%s", k))
						if val {
							args = append(args, "true")
						} else {
							args = append(args, "false")
						}
					} else {
						// CLI Flag Convention (store_true / is_flag=True)
						// Only append flag if true, omit if false.
						if val {
							args = append(args, fmt.Sprintf("--%s", k))
						}
					}
				case string:
					args = append(args, fmt.Sprintf("--%s", k))
					args = append(args, val)
				case float64:
					args = append(args, fmt.Sprintf("--%s", k))
					// Handle integers displayed as floats in JSON
					if val == float64(int64(val)) {
						args = append(args, fmt.Sprintf("%d", int64(val)))
					} else {
						args = append(args, fmt.Sprintf("%f", val))
					}
				case []interface{}, map[string]interface{}:
					args = append(args, fmt.Sprintf("--%s", k))
					// For complex types (arrays/objects), serialize back to JSON string
					jsonBytes, _ := json.Marshal(val)
					args = append(args, string(jsonBytes))
				default:
					args = append(args, fmt.Sprintf("--%s", k))
					args = append(args, fmt.Sprintf("%v", val))
				}
			}
		} else {
			// If not JSON object, maybe just pass raw input?
			// But for tool calls, Input IS JSON. If unmarshal fails, it's likely malformed.
			logging.Warn("Failed to parse tool input as JSON map", "input", params.Input)
		}
	}

	if t.def.Language == "python" {
		cmd = exec.Command("python", args...)
	} else if t.def.Language == "powershell" {
		cmd = exec.Command("powershell", args...)
	}

	// Prepare environment variables
	env := os.Environ()
	env = append(env, fmt.Sprintf("SKILL_INPUT=%s", params.Input))
	if t.def.Language == "python" {
		env = appendPythonUTF8Env(env)
	}

	// Inject API keys from config
	cfg := config.Get()
	if cfg != nil {
		if p, ok := cfg.Providers["aliyun"]; ok && p.APIKey != "" {
			env = append(env, fmt.Sprintf("DASHSCOPE_API_KEY=%s", p.APIKey))
			env = append(env, fmt.Sprintf("ALIYUN_API_KEY=%s", p.APIKey))
		}
		if p, ok := cfg.Providers["openai"]; ok && p.APIKey != "" {
			env = append(env, fmt.Sprintf("OPENAI_API_KEY=%s", p.APIKey))
		}
		if p, ok := cfg.Providers["anthropic"]; ok && p.APIKey != "" {
			env = append(env, fmt.Sprintf("ANTHROPIC_API_KEY=%s", p.APIKey))
		}
		if p, ok := cfg.Providers["deepseek"]; ok && p.APIKey != "" {
			env = append(env, fmt.Sprintf("DEEPSEEK_API_KEY=%s", p.APIKey))
		}
	}

	cmd.Env = env

	// Setup pipes for streaming
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return tools.NewTextErrorResponse(fmt.Sprintf("Failed to get stdout pipe: %v", err)), nil
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return tools.NewTextErrorResponse(fmt.Sprintf("Failed to get stderr pipe: %v", err)), nil
	}

	if err := cmd.Start(); err != nil {
		return tools.NewTextErrorResponse(fmt.Sprintf("Script execution failed to start: %v", err)), nil
	}

	// === Generic Process Tree Termination (Phase 5) ===
	// Some skills (e.g. app-launcher) are expected to intentionally launch
	// long-lived GUI apps. For those, we skip job-object binding to avoid
	// immediately terminating the launched app when the tool call returns.
	shouldBindJob := !strings.EqualFold(t.def.Name, "app-launcher")

	var job sys.JobHandle
	if runtime.GOOS == "windows" && shouldBindJob {
		job, _ = sys.CreateJobObject()
		if job != 0 {
			sys.AssignProcessToJobObject(job, cmd.Process.Pid)
		}
	}
	defer func() {
		if job != 0 {
			// Explicitly terminate the entire job before close so child processes
			// do not leak and keep skill files/directories locked on Windows.
			_ = sys.TerminateJobObject(job, 0)
			_ = sys.CloseJobObject(job)
		}
	}()
	// ==================================================

	// Progress Callback
	var progressCb tools.ProgressCallback
	if cb, ok := ctx.Value(tools.ProgressContextKey).(tools.ProgressCallback); ok {
		progressCb = cb
	}

	// Output collection
	var outputBuilder strings.Builder
	var outputMu sync.Mutex
	var wg sync.WaitGroup

	// Stream reader function
	readPipe := func(p io.ReadCloser) {
		defer wg.Done()
		scanner := bufio.NewScanner(p)
		for scanner.Scan() {
			line := scanner.Text()

			outputMu.Lock()
			outputBuilder.WriteString(line + "\n")
			outputMu.Unlock()

			// Parse Progress
			if progressCb != nil {
				matches := progressRegex.FindStringSubmatch(line)
				if len(matches) > 1 {
					var p float64
					fmt.Sscanf(matches[1], "%f", &p)
					if p >= 0 && p <= 100 {
						// Extract simple details (truncated line)
						details := strings.TrimSpace(line)
						if len(details) > 50 {
							details = details[:47] + "..."
						}
						progressCb(p, details)
					}
				}
			}
		}
	}

	wg.Add(2)
	go readPipe(stdoutPipe)
	go readPipe(stderrPipe)

	// Handle context cancellation and kill process tree
	resultChan := make(chan error, 1)
	go func() {
		resultChan <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		// Context cancelled, kill process tree immediately
		if job != 0 {
			sys.TerminateJobObject(job, 1)
			sys.CloseJobObject(job)
			job = 0 // Prevent double kill in defer
		} else if cmd.Process != nil {
			cmd.Process.Kill()
		}

		// Wait for Wait() to return (it might return error because we killed it)
		<-resultChan

		// Wait for pipe readers to finish to capture any last output
		wg.Wait()

		output := outputBuilder.String()
		if ctx.Err() == context.DeadlineExceeded {
			return tools.NewTextErrorResponse(fmt.Sprintf("Script execution timed out.\nOutput: %s", output)), nil
		}
		return tools.NewTextErrorResponse(fmt.Sprintf("Script execution was canceled.\nOutput: %s", output)), nil

	case err = <-resultChan:
		// Process finished normally (or failed on its own)
		wg.Wait()
	}

	output := outputBuilder.String()

	if err != nil {
		return tools.NewTextErrorResponse(fmt.Sprintf("Script execution failed: %v\nOutput: %s", err, output)), nil
	}

	// 技能脚本成功执行但无输出时，返回明确的完成信息
	if strings.TrimSpace(output) == "" {
		return tools.NewTextResponse(fmt.Sprintf("技能 '%s' 已成功执行（脚本无输出）。操作完成，请告知用户。", t.def.Name)), nil
	}

	return tools.NewTextResponse(output), nil
}

func findSkillsDirectory(workingDir, dirName string) string {
	candidatePaths := []string{
		filepath.Join(workingDir, dirName),
	}

	// Add CWD check
	if cwd, err := os.Getwd(); err == nil {
		candidatePaths = append(candidatePaths, filepath.Join(cwd, dirName))
		candidatePaths = append(candidatePaths, filepath.Join(cwd, "backend", dirName))
	}

	// Only try adding "backend" if we are not already in a directory named "backend"
	// This prevents searching in .../backend/backend/system_skills which is confusing (though harmless)
	if filepath.Base(workingDir) != "backend" {
		candidatePaths = append(candidatePaths, filepath.Join(workingDir, "backend", dirName))
	}

	if exePath, err := os.Executable(); err == nil {
		candidatePaths = append(candidatePaths, filepath.Join(filepath.Dir(exePath), dirName))
	}

	for _, path := range candidatePaths {
		if _, err := os.Stat(path); err == nil {
			logging.Info("Found "+dirName+" directory", "path", path)
			return path
		}
	}

	// Fallback logic
	if filepath.Base(workingDir) == "backend" {
		return filepath.Join(workingDir, dirName)
	}
	return filepath.Join(workingDir, "backend", dirName)
}
