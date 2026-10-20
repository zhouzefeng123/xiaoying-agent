package tools

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
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/permission"
	"github.com/auramate/auramate/internal/sys"
	"github.com/google/uuid"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/encoding/korean"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/traditionalchinese"
	xunicode "golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

// wrapReader normalizes Windows command output to UTF-8.
// PowerShell 5.1 and many native Windows programs still emit ACP/OEM bytes when stdout/stderr
// are redirected, even after setting OutputEncoding or chcp inside the shell.
func wrapReader(r io.Reader) io.Reader {
	if runtime.GOOS == "windows" {
		return newCommandOutputReader(r)
	}
	return r
}

const (
	RunCommandToolName    = "run_command"
	runCommandDescription = `Executes a shell command on the host system.
Usage:
- command: The command to execute.
- cwd: Optional working directory.
- timeout: Optional timeout in milliseconds (default: 300000).
- blocking: Whether to wait for the command to finish. Set to 'false' for long-running processes.
- wait_for_pattern: (Regex) If provided with blocking=false, waits until this pattern appears in stdout/stderr before returning. Useful for servers (e.g. "Listening on port").
- wait_for_timeout: Timeout for wait_for_pattern in ms (default: 10000).

Capabilities:
- **Stateful Shell**: Supports 'cd'.
- **Pattern Waiting**: Can wait for server startup confirmation.
- **Background Execution**: Use blocking: false.
- **Windows PowerShell**: The tool accepts accidental unquoted '&&' and rewrites it to a PowerShell-compatible fail-fast separator.
`
)

type RunCommandParams struct {
	Command        string `json:"command"`
	Cwd            string `json:"cwd,omitempty"`
	Timeout        int    `json:"timeout,omitempty"`
	Blocking       *bool  `json:"blocking,omitempty"`
	WaitForPattern string `json:"wait_for_pattern,omitempty"`
	WaitForTimeout int    `json:"wait_for_timeout,omitempty"`
	StreamParse    *bool  `json:"stream_parse,omitempty"`
	IdleTimeout    int    `json:"idle_timeout,omitempty"`
}

type runCommandTool struct {
	permissions permission.Service
}

type protectedRunCommandTargets struct {
	pids  []string
	names []string
}

// Background Command Manager
type BackgroundCommand struct {
	ID        string
	SessionID string
	Cmd       *exec.Cmd
	Job       sys.JobHandle
	Stdout    string
	Stderr    string
	Running   bool
	StartTime time.Time
	mu        sync.Mutex

	// Pattern Matching
	patternRegex *regexp.Regexp
	patternFound chan bool
	patternOnce  sync.Once

	// Progress Tracking
	StreamParse      bool
	Progress         float64 // 0-100
	ProgressDetails  string  // e.g. "2MB/s, ETA 10s"
	ProgressCallback ProgressCallback
	Cleanup          func()
}

type preparedWindowsInlinePythonCommand struct {
	Executable string
	Args       []string
	Env        map[string]string
	ScriptPath string
	Cleanup    func()
}

var (
	bgCommands                     = make(map[string]*BackgroundCommand)
	bgMu                           sync.Mutex
	sessionCwds                    = make(map[string]string) // SessionID -> Current Working Directory
	cwdMu                          sync.Mutex
	leadingPowerShellEnvAssignment = regexp.MustCompile(`^\s*\$env:([A-Za-z_][A-Za-z0-9_]*)\s*=\s*("(?:\\.|[^"])*"|'(?:\\.|[^'])*'|[^;\s]+)\s*;\s*`)
)

type commandOutputReader struct {
	src        io.Reader
	decoder    *commandOutputDecoder
	pending    []byte
	readBuf    []byte
	sourceDone bool
	sourceErr  error
}

type commandOutputDecoder struct {
	decided     bool
	encoding    string
	transformer transform.Transformer
	pendingRaw  []byte
}

func newCommandOutputReader(r io.Reader) io.Reader {
	return &commandOutputReader{
		src:     r,
		decoder: &commandOutputDecoder{},
		readBuf: make([]byte, 4096),
	}
}

func (r *commandOutputReader) Read(p []byte) (int, error) {
	for {
		if len(r.pending) > 0 {
			n := copy(p, r.pending)
			r.pending = r.pending[n:]
			return n, nil
		}

		if r.sourceDone {
			flushed := r.decoder.Append(nil, true)
			if len(flushed) > 0 {
				r.pending = append(r.pending, flushed...)
				continue
			}
			if r.sourceErr != nil && r.sourceErr != io.EOF {
				return 0, r.sourceErr
			}
			return 0, io.EOF
		}

		n, err := r.src.Read(r.readBuf)
		if n > 0 {
			decoded := r.decoder.Append(r.readBuf[:n], false)
			if len(decoded) > 0 {
				r.pending = append(r.pending, decoded...)
			}
		}
		if err != nil {
			r.sourceDone = true
			r.sourceErr = err
		}
	}
}

func (d *commandOutputDecoder) Append(chunk []byte, atEOF bool) []byte {
	if len(chunk) > 0 {
		d.pendingRaw = append(d.pendingRaw, chunk...)
	}

	if len(d.pendingRaw) == 0 {
		return nil
	}

	if !d.decided {
		if isASCIIOnlyBytes(d.pendingRaw) {
			output := append([]byte(nil), d.pendingRaw...)
			d.pendingRaw = d.pendingRaw[:0]
			if atEOF {
				d.decided = true
				d.encoding = "utf-8"
			}
			return output
		}

		encodingName, enc, bomLen, decided := detectCommandOutputEncoding(d.pendingRaw, atEOF)
		if !decided {
			return nil
		}

		d.decided = true
		d.encoding = encodingName
		if bomLen > 0 && len(d.pendingRaw) >= bomLen {
			d.pendingRaw = d.pendingRaw[bomLen:]
		}
		if enc != nil {
			d.transformer = enc.NewDecoder()
		}
	}

	if d.transformer == nil {
		return d.flushUTF8(atEOF)
	}

	return d.flushTransformed(atEOF)
}

func (d *commandOutputDecoder) flushUTF8(atEOF bool) []byte {
	if len(d.pendingRaw) == 0 {
		return nil
	}

	if atEOF || utf8.Valid(d.pendingRaw) {
		output := append([]byte(nil), d.pendingRaw...)
		d.pendingRaw = d.pendingRaw[:0]
		return output
	}

	prefixLen, ok := utf8ReadyPrefixLength(d.pendingRaw)
	if !ok || prefixLen == 0 {
		return nil
	}

	output := append([]byte(nil), d.pendingRaw[:prefixLen]...)
	remaining := append([]byte(nil), d.pendingRaw[prefixLen:]...)
	d.pendingRaw = remaining
	return output
}

func (d *commandOutputDecoder) flushTransformed(atEOF bool) []byte {
	if len(d.pendingRaw) == 0 {
		return nil
	}

	var output []byte
	for len(d.pendingRaw) > 0 {
		dst := make([]byte, len(d.pendingRaw)*4+16)
		nDst, nSrc, err := d.transformer.Transform(dst, d.pendingRaw, atEOF)
		if nDst > 0 {
			output = append(output, dst[:nDst]...)
		}
		if nSrc > 0 {
			d.pendingRaw = d.pendingRaw[nSrc:]
		}

		switch err {
		case nil:
			return output
		case transform.ErrShortDst:
			continue
		case transform.ErrShortSrc:
			if atEOF {
				output = append(output, []byte(strings.ToValidUTF8(string(d.pendingRaw), "�"))...)
				d.pendingRaw = d.pendingRaw[:0]
			}
			return output
		default:
			output = append(output, []byte(strings.ToValidUTF8(string(d.pendingRaw), "�"))...)
			d.pendingRaw = d.pendingRaw[:0]
			return output
		}
	}

	return output
}

func detectCommandOutputEncoding(data []byte, atEOF bool) (string, encoding.Encoding, int, bool) {
	if len(data) == 0 {
		return "", nil, 0, false
	}

	if len(data) >= 3 && data[0] == 0xEF && data[1] == 0xBB && data[2] == 0xBF {
		return "utf-8", nil, 3, true
	}
	if len(data) >= 2 && data[0] == 0xFF && data[1] == 0xFE {
		return "utf-16le", xunicode.UTF16(xunicode.LittleEndian, xunicode.IgnoreBOM), 2, true
	}
	if len(data) >= 2 && data[0] == 0xFE && data[1] == 0xFF {
		return "utf-16be", xunicode.UTF16(xunicode.BigEndian, xunicode.IgnoreBOM), 2, true
	}

	if utf16LEWithoutBOM(data) {
		return "utf-16le", xunicode.UTF16(xunicode.LittleEndian, xunicode.IgnoreBOM), 0, true
	}
	if utf16BEWithoutBOM(data) {
		return "utf-16be", xunicode.UTF16(xunicode.BigEndian, xunicode.IgnoreBOM), 0, true
	}

	if utf8.Valid(data) {
		return "utf-8", nil, 0, true
	}

	if !atEOF {
		if _, ok := utf8ReadyPrefixLength(data); ok {
			return "", nil, 0, false
		}
	}

	if encName, enc := detectLegacyWindowsEncoding(data); enc != nil {
		return encName, enc, 0, true
	}

	return "utf-8", nil, 0, atEOF
}

func utf8ReadyPrefixLength(data []byte) (int, bool) {
	if utf8.Valid(data) {
		return len(data), true
	}

	maxKeep := 3
	if len(data) < maxKeep {
		maxKeep = len(data)
	}

	for keep := 1; keep <= maxKeep; keep++ {
		prefixLen := len(data) - keep
		if prefixLen < 0 {
			continue
		}
		prefix := data[:prefixLen]
		tail := data[prefixLen:]
		if utf8.Valid(prefix) && !utf8.FullRune(tail) {
			return prefixLen, true
		}
	}

	return 0, false
}

func isASCIIOnlyBytes(data []byte) bool {
	for _, b := range data {
		if b == 0 || b >= 0x80 {
			return false
		}
	}
	return true
}

func utf16LEWithoutBOM(data []byte) bool {
	return looksLikeUTF16(data, true)
}

func utf16BEWithoutBOM(data []byte) bool {
	return looksLikeUTF16(data, false)
}

func looksLikeUTF16(data []byte, littleEndian bool) bool {
	if len(data) < 4 || len(data)%2 != 0 {
		return false
	}

	sample := len(data)
	if sample > 64 {
		sample = 64
		sample -= sample % 2
	}

	zeroMatches := 0
	pairs := 0
	for i := 0; i+1 < sample; i += 2 {
		pairs++
		if littleEndian {
			if data[i+1] == 0 {
				zeroMatches++
			}
		} else if data[i] == 0 {
			zeroMatches++
		}
	}

	return pairs >= 2 && zeroMatches*2 >= pairs
}

func detectLegacyWindowsEncoding(data []byte) (string, encoding.Encoding) {
	for _, cp := range preferredWindowsCodePages() {
		if encName, enc, ok := windowsCodePageEncoding(cp); ok {
			if enc == nil {
				continue
			}
			return encName, enc
		}
	}

	if bytesLikelyGBK(data) {
		return "gbk", simplifiedchinese.GBK
	}
	if bytesLikelyBIG5(data) {
		return "big5", traditionalchinese.Big5
	}

	return "", nil
}

func windowsCodePageEncoding(codePage uint32) (string, encoding.Encoding, bool) {
	switch codePage {
	case 65001:
		return "utf-8", nil, true
	case 936:
		return "gbk", simplifiedchinese.GBK, true
	case 950:
		return "big5", traditionalchinese.Big5, true
	case 932:
		return "shift-jis", japanese.ShiftJIS, true
	case 949:
		return "euc-kr", korean.EUCKR, true
	case 1250:
		return "windows-1250", charmap.Windows1250, true
	case 1251:
		return "windows-1251", charmap.Windows1251, true
	case 1252:
		return "windows-1252", charmap.Windows1252, true
	case 1253:
		return "windows-1253", charmap.Windows1253, true
	case 1254:
		return "windows-1254", charmap.Windows1254, true
	case 1255:
		return "windows-1255", charmap.Windows1255, true
	case 1256:
		return "windows-1256", charmap.Windows1256, true
	case 1257:
		return "windows-1257", charmap.Windows1257, true
	case 1258:
		return "windows-1258", charmap.Windows1258, true
	default:
		return "", nil, false
	}
}

func bytesLikelyGBK(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	likelyPairs := 0
	totalPairs := 0
	for i := 0; i < len(data); {
		if data[i] <= 0x7F {
			i++
			continue
		}
		if i+1 >= len(data) {
			break
		}
		b1, b2 := data[i], data[i+1]
		totalPairs++
		if (b1 >= 0x81 && b1 <= 0xFE) && ((b2 >= 0x40 && b2 <= 0x7E) || (b2 >= 0xA1 && b2 <= 0xFE)) {
			likelyPairs++
			i += 2
			continue
		}
		i++
	}

	return totalPairs > 0 && float64(likelyPairs)/float64(totalPairs) > 0.7
}

func bytesLikelyBIG5(data []byte) bool {
	if len(data) == 0 {
		return false
	}

	likelyPairs := 0
	totalPairs := 0
	for i := 0; i < len(data); {
		if data[i] <= 0x7F {
			i++
			continue
		}
		if i+1 >= len(data) {
			break
		}
		b1, b2 := data[i], data[i+1]
		totalPairs++
		if (b1 >= 0x81 && b1 <= 0xFE) && ((b2 >= 0x40 && b2 <= 0x7E) || (b2 >= 0xA1 && b2 <= 0xFE)) {
			likelyPairs++
			i += 2
			continue
		}
		i++
	}

	return totalPairs > 0 && float64(likelyPairs)/float64(totalPairs) > 0.7
}

func NewRunCommandTool(permissions permission.Service) BaseTool {
	return &runCommandTool{
		permissions: permissions,
	}
}

func (t *runCommandTool) Info() ToolInfo {
	return ToolInfo{
		Name:        RunCommandToolName,
		Description: runCommandDescription,
		Parameters: map[string]any{
			"command": map[string]any{
				"type":        "string",
				"description": "The shell command to execute.",
			},
			"cwd": map[string]any{
				"type":        "string",
				"description": "Optional working directory.",
			},
			"timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout in milliseconds.",
			},
			"blocking": map[string]any{
				"type":        "boolean",
				"description": "If false, runs in background. Default: true.",
			},
			"wait_for_pattern": map[string]any{
				"type":        "string",
				"description": "Regex pattern to wait for (only if blocking=false).",
			},
			"wait_for_timeout": map[string]any{
				"type":        "integer",
				"description": "Timeout for pattern waiting in ms.",
			},
			"stream_parse": map[string]any{
				"type":        "boolean",
				"description": "Enable real-time progress parsing (e.g. for curl/wget). Default: false.",
			},
			"idle_timeout": map[string]any{
				"type":        "integer",
				"description": "If > 0, kills blocking command if no output for N ms. Extends 'timeout' as long as output flows.",
			},
		},
		Required: []string{"command"},
	}
}

func (t *runCommandTool) Run(ctx context.Context, call ToolCall) (ToolResponse, error) {
	var params RunCommandParams
	if err := json.Unmarshal([]byte(call.Input), &params); err != nil {
		return NewTextErrorResponse(fmt.Sprintf("Invalid JSON: %v", err)), nil
	}

	if params.Command == "" {
		return NewTextErrorResponse("command is required"), nil
	}

	sessionID, _ := GetContextValues(ctx)
	defaultCwd := config.WorkingDirectoryFor(ctx)

	// Resolve CWD
	var currentCwd string
	if params.Cwd != "" {
		currentCwd = params.Cwd
	} else {
		cwdMu.Lock()
		if val, ok := sessionCwds[sessionID]; ok {
			currentCwd = val
		} else {
			currentCwd = defaultCwd
		}
		cwdMu.Unlock()
	}

	// Handle 'cd' command
	// FIX: Handle chained commands like "cd path && command" or "cd path; command"
	// We need to persist the directory change even if it's part of a chain.
	trimmedCmd := strings.TrimSpace(params.Command)

	// Windows Compatibility Patch for 'curl'
	// PowerShell aliases 'curl' to 'Invoke-WebRequest', which has different syntax.
	// We detect if user is trying to use standard curl syntax and redirect to curl.exe if available.
	if runtime.GOOS == "windows" {
		if strings.HasPrefix(trimmedCmd, "curl ") {
			// Check if it looks like standard curl flags are used (-s, -X, -H, etc)
			// Invoke-WebRequest uses -Uri, -Method, -Headers.
			// If we see single dash flags, it's likely standard curl.
			if strings.Contains(trimmedCmd, " -") {
				// Try to use curl.exe explicitly to bypass PowerShell alias
				// We assume curl.exe is in PATH (Git Bash or Windows 10+ built-in)
				// Replace "curl " with "curl.exe "
				// params.Command = strings.Replace(params.Command, "curl ", "curl.exe ", 1)
				// Better: Construct a safe replacement
				params.Command = "curl.exe" + params.Command[4:]
				trimmedCmd = params.Command // Update trimmed for subsequent checks
			}
		}
	}

	if runtime.GOOS == "windows" {
		if normalized, changed := normalizePowerShellAndAnd(params.Command); changed {
			params.Command = normalized
			trimmedCmd = strings.TrimSpace(params.Command)
		}
	}

	if reason := protectedRunCommandGuardReason(params.Command, loadProtectedRunCommandTargets()); reason != "" {
		return NewTextErrorResponse(fmt.Sprintf("Blocked command: %s. Aura host lifecycle is protected from run_command.", reason)), nil
	}

	blocking := true
	if params.Blocking != nil {
		blocking = *params.Blocking
	}

	// Detect if command starts with 'cd'
	if strings.HasPrefix(trimmedCmd, "cd ") {
		// Heuristic: Split by common chain separators (&&, ;)
		// We only look at the first segment for the 'cd' command.

		var firstSegment string
		if idx := strings.Index(trimmedCmd, "&&"); idx != -1 {
			firstSegment = trimmedCmd[:idx]
		} else if idx := strings.Index(trimmedCmd, ";"); idx != -1 {
			firstSegment = trimmedCmd[:idx]
		} else {
			firstSegment = trimmedCmd
		}

		cdPart := strings.TrimSpace(firstSegment)

		if strings.HasPrefix(cdPart, "cd ") {
			targetDir := strings.TrimSpace(strings.TrimPrefix(cdPart, "cd "))
			// Remove quotes
			targetDir = strings.Trim(targetDir, "\"'")

			// Resolve absolute path
			if !filepath.IsAbs(targetDir) {
				targetDir = filepath.Join(currentCwd, targetDir)
			}

			// Verify persistence (optional, but good for safety)
			// But for chained commands, we might want to let the shell handle execution first?
			// No, we need to persist it for SUBSEQUENT commands.
			// The current command execution (cmd.Dir) is already set to 'currentCwd'.
			// The 'cd' in the command string will affect the shell's process.
			// But we need to update 'sessionCwds' for the NEXT tool call.

			// Check existence
			info, err := os.Stat(targetDir)
			if err == nil && info.IsDir() {
				cwdMu.Lock()
				sessionCwds[sessionID] = targetDir
				cwdMu.Unlock()
				// We don't return here, we proceed to execute the full chain!
			}
		}

		// If the command was JUST 'cd path', we can return early or let it run (shell returns nothing).
		// But 'runCommandTool' usually returns stdout.
		// If we let it run, PowerShell/Bash handles it.
		// BUT 'exec.Command' starts a NEW shell. 'cd' inside it won't affect parent.
		// So persistence in 'sessionCwds' is the ONLY way to make it stick across calls.

		// Optimization: If command is ONLY 'cd path', we can skip shell execution and just return success.
		if !strings.Contains(trimmedCmd, "&&") && !strings.Contains(trimmedCmd, ";") {
			targetDir := strings.TrimSpace(strings.TrimPrefix(trimmedCmd, "cd "))
			targetDir = strings.Trim(targetDir, "\"'")
			if !filepath.IsAbs(targetDir) {
				targetDir = filepath.Join(currentCwd, targetDir)
			}
			info, err := os.Stat(targetDir)
			if err != nil {
				return NewTextErrorResponse(fmt.Sprintf("cd: %v", err)), nil
			}
			if !info.IsDir() {
				return NewTextErrorResponse(fmt.Sprintf("cd: %s is not a directory", targetDir)), nil
			}
			cwdMu.Lock()
			sessionCwds[sessionID] = targetDir
			cwdMu.Unlock()
			return NewTextResponse(fmt.Sprintf("已切换工作目录到: %s。后续命令将在此目录执行。操作完成。", targetDir)), nil
		}
	}

	var preparedPython *preparedWindowsInlinePythonCommand
	if runtime.GOOS == "windows" {
		prepared, err := prepareWindowsInlinePythonCommand(ctx, params.Command, currentCwd)
		if err != nil {
			return NewTextErrorResponse(fmt.Sprintf("Failed to prepare inline python command: %v", err)), nil
		}
		preparedPython = prepared
	}
	if blocking && preparedPython != nil && preparedPython.Cleanup != nil {
		defer preparedPython.Cleanup()
	}

	var cmd *exec.Cmd
	var cancelCmd context.CancelFunc

	// Best effort producer-side normalization on Windows.
	// PowerShell 5.1 still often emits ACP bytes to redirected stdout/stderr, so wrapReader remains the authoritative fix.
	utf8Prefix := `[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false); [Console]::InputEncoding = [System.Text.UTF8Encoding]::new($false); $OutputEncoding = [System.Text.UTF8Encoding]::new($false); [System.Environment]::SetEnvironmentVariable('PYTHONIOENCODING', 'utf-8', 'Process'); [System.Environment]::SetEnvironmentVariable('PYTHONUTF8', '1', 'Process'); chcp 65001 >$null; `

	if blocking {
		// Calculate timeout for context
		timeoutDuration := time.Duration(DefaultTimeout) * time.Millisecond
		if params.Timeout > 0 {
			timeoutDuration = time.Duration(params.Timeout) * time.Millisecond
		}
		// Create context with timeout to ensure command is killed on timeout or request cancellation
		var cmdCtx context.Context
		cmdCtx, cancelCmd = context.WithTimeout(ctx, timeoutDuration)
		defer cancelCmd()

		if preparedPython != nil {
			cmd = exec.CommandContext(cmdCtx, preparedPython.Executable, preparedPython.Args...)
			applyCommandEnv(cmd, preparedPython.Env)
		} else if runtime.GOOS == "windows" {
			cmd = exec.CommandContext(cmdCtx, "powershell", "-NoProfile", "-NonInteractive", "-Command", utf8Prefix+params.Command)
			applyCommandEnv(cmd, nil)
		} else {
			cmd = exec.CommandContext(cmdCtx, "/bin/bash", "-c", params.Command)
		}
	} else {
		if preparedPython != nil {
			cmd = exec.Command(preparedPython.Executable, preparedPython.Args...)
			applyCommandEnv(cmd, preparedPython.Env)
		} else if runtime.GOOS == "windows" {
			cmd = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", utf8Prefix+params.Command)
			applyCommandEnv(cmd, nil)
		} else {
			cmd = exec.Command("/bin/bash", "-c", params.Command)
		}
	}

	cmd.Dir = currentCwd

	// === Non-Blocking Execution (Background) ===
	if !blocking {
		commandID := uuid.New().String()

		// Create Job Object for Windows
		var jobHandle sys.JobHandle
		if runtime.GOOS == "windows" {
			var err error
			jobHandle, err = sys.CreateJobObject()
			if err != nil {
				return NewTextErrorResponse(fmt.Sprintf("Failed to create job object: %v", err)), nil
			}
		}

		stdoutPipe, err := cmd.StdoutPipe()
		if err != nil {
			if preparedPython != nil && preparedPython.Cleanup != nil {
				preparedPython.Cleanup()
			}
			if jobHandle != 0 {
				sys.CloseJobObject(jobHandle)
			}
			return NewTextErrorResponse(fmt.Sprintf("Failed to get stdout pipe: %v", err)), nil
		}
		stderrPipe, err := cmd.StderrPipe()
		if err != nil {
			if preparedPython != nil && preparedPython.Cleanup != nil {
				preparedPython.Cleanup()
			}
			if jobHandle != 0 {
				sys.CloseJobObject(jobHandle)
			}
			return NewTextErrorResponse(fmt.Sprintf("Failed to get stderr pipe: %v", err)), nil
		}

		if err := cmd.Start(); err != nil {
			if preparedPython != nil && preparedPython.Cleanup != nil {
				preparedPython.Cleanup()
			}
			if jobHandle != 0 {
				sys.CloseJobObject(jobHandle)
			}
			return NewTextErrorResponse(fmt.Sprintf("Failed to start command: %v", err)), nil
		}

		// Assign process to job object
		if jobHandle != 0 {
			if err := sys.AssignProcessToJobObject(jobHandle, cmd.Process.Pid); err != nil {
				cmd.Process.Kill()
				sys.CloseJobObject(jobHandle)
				return NewTextErrorResponse(fmt.Sprintf("Failed to assign process to job object: %v", err)), nil
			}
		}

		bgCmd := &BackgroundCommand{
			ID:        commandID,
			SessionID: sessionID,
			Cmd:       cmd,
			Job:       jobHandle,
			Running:   true,
			StartTime: time.Now(),
			Cleanup:   nil,
		}
		if preparedPython != nil {
			bgCmd.Cleanup = preparedPython.Cleanup
			preparedPython.Cleanup = nil
		}

		if cb, ok := ctx.Value(ProgressContextKey).(ProgressCallback); ok {
			bgCmd.ProgressCallback = cb
		}

		if params.StreamParse != nil && *params.StreamParse {
			bgCmd.StreamParse = true
		}

		// Setup Pattern Waiting
		if params.WaitForPattern != "" {
			re, err := regexp.Compile(params.WaitForPattern)
			if err != nil {
				return NewTextErrorResponse(fmt.Sprintf("Invalid wait_for_pattern regex: %v", err)), nil
			}
			bgCmd.patternRegex = re
			bgCmd.patternFound = make(chan bool, 1)
		}

		bgMu.Lock()
		bgCommands[commandID] = bgCmd
		bgMu.Unlock()

		// Stream output
		var bgWg sync.WaitGroup
		bgWg.Add(2)
		go func() {
			defer bgWg.Done()
			streamOutput(wrapReader(stdoutPipe), bgCmd, false)
		}()
		go func() {
			defer bgWg.Done()
			streamOutput(wrapReader(stderrPipe), bgCmd, true)
		}()

		// Cleanup old commands
		go cleanupBackgroundCommands()

		// Wait for completion
		go func() {
			bgWg.Wait() // Wait for streamOutput to finish reading
			err := cmd.Wait()
			bgCmd.mu.Lock()
			defer bgCmd.mu.Unlock()
			bgCmd.Running = false

			// Close Job Object when process finishes
			if bgCmd.Job != 0 {
				sys.CloseJobObject(bgCmd.Job)
				bgCmd.Job = 0
			}

			if err != nil {
				bgCmd.Stderr += fmt.Sprintf("\n[Process exited with error: %v]", err)
			} else {
				bgCmd.Stdout += "\n[Process exited successfully]"
			}
			if bgCmd.Cleanup != nil {
				bgCmd.Cleanup()
				bgCmd.Cleanup = nil
			}
			if bgCmd.patternFound != nil {
				bgCmd.patternOnce.Do(func() { close(bgCmd.patternFound) })
			}
		}()

		// Wait for pattern if requested
		if params.WaitForPattern != "" {
			waitTimeout := 10 * time.Second
			if params.WaitForTimeout > 0 {
				waitTimeout = time.Duration(params.WaitForTimeout) * time.Millisecond
			}

			select {
			case <-bgCmd.patternFound:
				return NewTextResponse(fmt.Sprintf("Command started and pattern '%s' matched.\nCommand ID: %s", params.WaitForPattern, commandID)), nil
			case <-time.After(waitTimeout):
				// Timeout
				return NewTextResponse(fmt.Sprintf("Command started but timed out waiting for pattern '%s' after %v.\nCommand ID: %s\nCheck status for output.", params.WaitForPattern, waitTimeout, commandID)), nil
			}
		}

		return NewTextResponse(fmt.Sprintf("Command started in background.\nCommand ID: %s\nUse 'check_command_status' to monitor or 'stop_command' to terminate.", commandID)), nil
	}

	// === Blocking Execution (Foreground) ===
	timeout := 60 * time.Second
	if params.Timeout > 0 {
		timeout = time.Duration(params.Timeout) * time.Millisecond
	}

	// Check for streaming callback (Phase 4: Live Tool Status)
	streamCallback, hasStreamCallback := ctx.Value(StreamOutputContextKey).(StreamOutputCallback)

	// Create temp file for logs (Unified approach for ALL blocking commands)
	logDir := filepath.Join(config.WorkingDirectoryFor(ctx), ".auramate", "logs")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		logDir = os.TempDir()
	}
	logFile, err := os.CreateTemp(logDir, "cmd_*.log")
	if err != nil {
		return NewTextErrorResponse(fmt.Sprintf("Failed to create log file: %v", err)), nil
	}
	defer logFile.Close()
	logPath := logFile.Name()

	// Use LimitBuffer with Head+Tail strategy
	const MaxOutputLength = 100000
	limitBuf := &LimitBuffer{Limit: MaxOutputLength}

	// Create pipes for manual reading (Required for streaming callback & idle timeout)
	// We use pipes + manual copying instead of MultiWriter to support:
	// 1. Live streaming callbacks
	// 2. Idle timeout detection
	// 3. Writing to log file
	// 4. Writing to LimitBuffer

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return NewTextErrorResponse(fmt.Sprintf("Failed to get stdout: %v", err)), nil
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return NewTextErrorResponse(fmt.Sprintf("Failed to get stderr: %v", err)), nil
	}

	// Create and assign Job Object
	var jobHandle sys.JobHandle
	if runtime.GOOS == "windows" {
		jobHandle, _ = sys.CreateJobObject()
	}

	if err := cmd.Start(); err != nil {
		if jobHandle != 0 {
			sys.CloseJobObject(jobHandle)
		}
		return NewTextErrorResponse(fmt.Sprintf("Failed to start: %v", err)), nil
	}

	if jobHandle != 0 {
		sys.AssignProcessToJobObject(jobHandle, cmd.Process.Pid)
	}

	// Cleanup Job Object on exit
	defer func() {
		if jobHandle != 0 {
			sys.CloseJobObject(jobHandle)
		}
	}()

	// Output coordination
	var wg sync.WaitGroup

	// Channels for signals
	activity := make(chan bool, 1)
	done := make(chan error, 1)

	// Stream Reader Function
	readPipe := func(p io.Reader) {
		defer wg.Done()
		buf := make([]byte, 1024)
		for {
			if ctx.Err() != nil {
				return
			} // Stop on context cancel

			n, err := p.Read(buf)
			if n > 0 {
				chunk := buf[:n]

				// Write to Log File (Full history)
				logFile.Write(chunk)

				// Write to LimitBuffer (Memory efficient preview)
				limitBuf.Write(chunk)

				// Callback
				if hasStreamCallback {
					streamCallback(string(chunk))
				}

				// Activity Signal
				if params.IdleTimeout > 0 {
					select {
					case activity <- true:
					default:
					}
				}
			}
			if err != nil {
				break
			}
		}
	}

	wg.Add(2)
	// Apply wrapReader for encoding fixes on Windows (e.g. GBK -> UTF8)
	go readPipe(wrapReader(stdoutPipe))
	go readPipe(wrapReader(stderrPipe))

	go func() {
		wg.Wait() // Wait for pipes to close and drain before calling Wait
		err := cmd.Wait()
		done <- err
	}()

	// Timers
	idleTimeoutDuration := 24 * time.Hour
	if params.IdleTimeout > 0 {
		idleTimeoutDuration = time.Duration(params.IdleTimeout) * time.Millisecond
	}
	timer := time.NewTimer(idleTimeoutDuration)
	hardLimit := time.NewTimer(timeout)
	defer timer.Stop()
	defer hardLimit.Stop()

	if params.IdleTimeout <= 0 {
		timer.Stop()
	}

	// Wait Loop
	var waitErr error
	completed := false

	for !completed {
		select {
		case <-ctx.Done():
			// Manual/Context Cancel
			if jobHandle != 0 {
				sys.TerminateJobObject(jobHandle, 1)
			} else if cmd.Process != nil {
				cmd.Process.Kill()
			}
			return NewTextErrorResponse(fmt.Sprintf("Command interrupted.\nPartial Output:\n%s", limitBuf.String())), ctx.Err()

		case <-activity:
			if params.IdleTimeout > 0 {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(idleTimeoutDuration)
			}

		case <-timer.C:
			if jobHandle != 0 {
				sys.TerminateJobObject(jobHandle, 1)
			} else if cmd.Process != nil {
				cmd.Process.Kill()
			}
			return NewTextErrorResponse(fmt.Sprintf("Killed due to idle timeout (%v).\nPartial Output:\n%s", idleTimeoutDuration, limitBuf.String())), nil

		case <-hardLimit.C:
			if jobHandle != 0 {
				sys.TerminateJobObject(jobHandle, 1)
			} else if cmd.Process != nil {
				cmd.Process.Kill()
			}
			return NewTextErrorResponse(fmt.Sprintf("Killed due to timeout (%v).\nPartial Output:\n%s", timeout, limitBuf.String())), nil

		case err := <-done:
			waitErr = err
			completed = true
		}
	}

	// Post-processing
	output := limitBuf.String()
	truncated := limitBuf.Truncated

	if waitErr != nil {
		errMsg := fmt.Sprintf("Command failed: %v", waitErr)
		if exitErr, ok := waitErr.(*exec.ExitError); ok {
			errMsg = fmt.Sprintf("Command exited with code %d", exitErr.ExitCode())
		}
		if output != "" {
			errMsg += "\nOutput:\n" + output
		}
		return NewTextErrorResponse(errMsg), nil
	}

	// Smart Response
	const RefOutputThreshold = 20000
	if truncated || len(output) > RefOutputThreshold {
		preview := output
		if len(preview) > 500 {
			preview = preview[:500] + "..."
		}
		displayPath := logPath
		if rel, err := filepath.Rel(config.WorkingDirectoryFor(ctx), logPath); err == nil {
			displayPath = rel
		}
		summary := fmt.Sprintf("Success. Output size: %d chars.", limitBuf.TotalWritten)
		return NewReferenceResponse(summary, displayPath, preview), nil
	}

	if strings.TrimSpace(output) == "" {
		return NewTextResponse(fmt.Sprintf("命令 '%s' 已成功执行（无输出）。操作完成。", params.Command)), nil
	}

	return NewTextResponse(output), nil
}

// Helper: LimitBuffer captures Head + Tail
type LimitBuffer struct {
	buf          strings.Builder
	tailBuf      []byte // Circular buffer for tail would be ideal, but simple slice is easier
	Limit        int
	TotalWritten int64
	Truncated    bool
	mu           sync.Mutex
}

func (b *LimitBuffer) Write(p []byte) (n int, err error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	n = len(p)
	b.TotalWritten += int64(n)

	// Strategy:
	// 1. If total < Limit, just append to buf
	// 2. If total > Limit, we need to ensure we keep Head (Limit/2) and Tail (Limit/2)

	headLimit := b.Limit / 2

	if b.buf.Len() < headLimit {
		// Filling head
		toWrite := headLimit - b.buf.Len()
		if n <= toWrite {
			b.buf.Write(p)
			if n == toWrite {
				b.Truncated = true // Only matters if we ever write more
			}
			return n, nil
		}
		// Write part to head, rest to tail
		b.buf.Write(p[:toWrite])
		p = p[toWrite:]
		b.Truncated = true
	}

	// Update tail (keep last N bytes)
	tailLimit := b.Limit - headLimit

	// Append new data to tailBuf
	if b.tailBuf == nil {
		b.tailBuf = make([]byte, 0, tailLimit*2)
	}
	b.tailBuf = append(b.tailBuf, p...)

	// Trim tail if too long
	if len(b.tailBuf) > tailLimit {
		// Keep last tailLimit bytes
		b.tailBuf = b.tailBuf[len(b.tailBuf)-tailLimit:]
	}

	b.Truncated = true
	return n, nil
}

func (b *LimitBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.Truncated || b.TotalWritten <= int64(b.Limit) {
		return b.buf.String() + string(b.tailBuf)
	}

	return fmt.Sprintf("%s\n... [Truncated %d bytes] ...\n%s",
		b.buf.String(),
		b.TotalWritten-int64(b.buf.Len())-int64(len(b.tailBuf)),
		string(b.tailBuf))
}

func streamOutput(pipe io.Reader, bgCmd *BackgroundCommand, isStderr bool) {
	scanner := bufio.NewScanner(pipe)

	// Use custom splitter if parsing enabled to handle \r updates
	if bgCmd.StreamParse {
		scanner.Split(scanCarriageReturn)
	}

	for scanner.Scan() {
		line := scanner.Text()
		bgCmd.mu.Lock()

		// Limit output size (keep last 100KB)
		const maxOutputSize = 100 * 1024

		if isStderr {
			bgCmd.Stderr += line + "\n"
			if len(bgCmd.Stderr) > maxOutputSize {
				bgCmd.Stderr = bgCmd.Stderr[len(bgCmd.Stderr)-maxOutputSize:]
				// Optional: Add indicator
				if !strings.HasPrefix(bgCmd.Stderr, "...[Truncated]") {
					bgCmd.Stderr = "...[Truncated]\n" + bgCmd.Stderr
				}
			}
		} else {
			bgCmd.Stdout += line + "\n"
			if len(bgCmd.Stdout) > maxOutputSize {
				bgCmd.Stdout = bgCmd.Stdout[len(bgCmd.Stdout)-maxOutputSize:]
				if !strings.HasPrefix(bgCmd.Stdout, "...[Truncated]") {
					bgCmd.Stdout = "...[Truncated]\n" + bgCmd.Stdout
				}
			}
		}

		// Progress Parsing
		if bgCmd.StreamParse {
			bgCmd.parseProgress(line)
		}

		// Check pattern
		if bgCmd.patternRegex != nil {
			// Check if channel is closed or full
			select {
			case <-bgCmd.patternFound:
				// Already found/closed, push back to keep non-blocking logic simple?
				// actually if closed, receiving yields zero value.
				// simpler: check if we should signal
				// If we haven't signaled yet:
			default:
				// Channel is open and empty
				if bgCmd.patternRegex.MatchString(line) {
					bgCmd.patternRegex = nil // Stop checking
					bgCmd.patternFound <- true
					if bgCmd.patternFound != nil {
						bgCmd.patternOnce.Do(func() { close(bgCmd.patternFound) })
					}
				}
			}
		}
		bgCmd.mu.Unlock()
	}
}

func prepareWindowsInlinePythonCommand(ctx context.Context, command string, currentCwd string) (*preparedWindowsInlinePythonCommand, error) {
	if runtime.GOOS != "windows" {
		return nil, nil
	}

	envVars, remaining, err := extractLeadingPowerShellEnvAssignments(command)
	if err != nil {
		return nil, err
	}
	if containsUnquotedPowerShellControlOperator(remaining) {
		return nil, nil
	}

	tokens, err := tokenizeShellLikeCommand(remaining)
	if err != nil {
		return nil, err
	}
	if len(tokens) < 3 || !looksLikePythonExecutable(tokens[0]) {
		return nil, nil
	}

	codeIndex := -1
	for i := 1; i < len(tokens); i++ {
		if tokens[i] == "-c" {
			codeIndex = i
			break
		}
	}
	if codeIndex == -1 || codeIndex+1 >= len(tokens) {
		return nil, nil
	}

	code := tokens[codeIndex+1]
	if strings.TrimSpace(code) == "" {
		return nil, nil
	}

	scriptPath, cleanup, err := createInlinePythonScript(ctx, code, currentCwd)
	if err != nil {
		return nil, err
	}

	args := make([]string, 0, len(tokens)-1)
	args = append(args, tokens[1:codeIndex]...)
	args = append(args, scriptPath)
	if codeIndex+2 < len(tokens) {
		args = append(args, tokens[codeIndex+2:]...)
	}

	return &preparedWindowsInlinePythonCommand{
		Executable: tokens[0],
		Args:       args,
		Env:        envVars,
		ScriptPath: scriptPath,
		Cleanup:    cleanup,
	}, nil
}

func extractLeadingPowerShellEnvAssignments(command string) (map[string]string, string, error) {
	envVars := make(map[string]string)
	remaining := command

	for {
		match := leadingPowerShellEnvAssignment.FindStringSubmatch(remaining)
		if len(match) == 0 {
			break
		}
		name := match[1]
		value, err := stripShellQuotes(match[2])
		if err != nil {
			return nil, "", err
		}
		envVars[name] = value
		remaining = remaining[len(match[0]):]
	}

	return envVars, strings.TrimSpace(remaining), nil
}

func stripShellQuotes(value string) (string, error) {
	if len(value) < 2 {
		return value, nil
	}
	quote := value[0]
	if (quote != '\'' && quote != '"') || value[len(value)-1] != quote {
		return value, nil
	}

	var builder strings.Builder
	inner := value[1 : len(value)-1]
	for index := 0; index < len(inner); index++ {
		if inner[index] == '\\' && index+1 < len(inner) && inner[index+1] == quote {
			builder.WriteByte(quote)
			index++
			continue
		}
		builder.WriteByte(inner[index])
	}
	return builder.String(), nil
}

func containsUnquotedPowerShellControlOperator(command string) bool {
	var quote byte
	for index := 0; index < len(command); index++ {
		ch := command[index]
		if quote != 0 {
			if ch == '\\' && index+1 < len(command) && command[index+1] == quote {
				index++
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}

		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}

		switch ch {
		case ';', '|', '>', '<':
			return true
		case '&':
			return true
		}
	}
	return false
}

func normalizePowerShellAndAnd(command string) (string, bool) {
	var quote byte
	var builder strings.Builder
	changed := false

	for index := 0; index < len(command); index++ {
		ch := command[index]
		if quote != 0 {
			builder.WriteByte(ch)
			if ch == '`' && index+1 < len(command) {
				index++
				builder.WriteByte(command[index])
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}

		if ch == '\'' || ch == '"' {
			quote = ch
			builder.WriteByte(ch)
			continue
		}

		if ch == '&' && index+1 < len(command) && command[index+1] == '&' {
			builder.WriteString("; if (-not $?) { exit 1 };")
			index++
			changed = true
			continue
		}

		builder.WriteByte(ch)
	}

	if !changed {
		return command, false
	}
	return builder.String(), true
}

func tokenizeShellLikeCommand(command string) ([]string, error) {
	tokens := make([]string, 0, 8)
	for index := 0; index < len(command); {
		for index < len(command) && unicode.IsSpace(rune(command[index])) {
			index++
		}
		if index >= len(command) {
			break
		}

		if command[index] == '\'' || command[index] == '"' {
			quote := command[index]
			index++
			var builder strings.Builder
			closed := false
			for index < len(command) {
				ch := command[index]
				if ch == '\\' && index+1 < len(command) && command[index+1] == quote {
					builder.WriteByte(quote)
					index += 2
					continue
				}
				if ch == quote {
					index++
					closed = true
					break
				}
				builder.WriteByte(ch)
				index++
			}
			if !closed {
				return nil, fmt.Errorf("unterminated quoted token")
			}
			tokens = append(tokens, builder.String())
			continue
		}

		start := index
		for index < len(command) && !unicode.IsSpace(rune(command[index])) {
			index++
		}
		tokens = append(tokens, command[start:index])
	}
	return tokens, nil
}

func looksLikePythonExecutable(executable string) bool {
	base := strings.ToLower(filepath.Base(executable))
	switch base {
	case "python", "python.exe", "python3", "python3.exe", "py", "py.exe":
		return true
	default:
		return false
	}
}

func createInlinePythonScript(ctx context.Context, code string, currentCwd string) (string, func(), error) {
	tempDir := filepath.Join(config.WorkingDirectoryFor(ctx), ".auramate", "tmp")
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		tempDir = os.TempDir()
	}
	if currentCwd != "" {
		candidate := filepath.Join(currentCwd, ".auramate", "tmp")
		if err := os.MkdirAll(candidate, 0755); err == nil {
			tempDir = candidate
		}
	}

	file, err := os.CreateTemp(tempDir, "run_command_inline_*.py")
	if err != nil {
		return "", nil, err
	}
	scriptPath := file.Name()
	if _, err := file.WriteString(code); err != nil {
		file.Close()
		_ = os.Remove(scriptPath)
		return "", nil, err
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(scriptPath)
		return "", nil, err
	}

	cleanup := func() {
		_ = os.Remove(scriptPath)
	}
	return scriptPath, cleanup, nil
}

func applyCommandEnv(cmd *exec.Cmd, extra map[string]string) {
	defaults := defaultCommandEnv()
	if len(defaults) == 0 && len(extra) == 0 {
		return
	}
	merged := make(map[string]string)
	for _, item := range os.Environ() {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) == 2 {
			merged[parts[0]] = parts[1]
		}
	}
	for key, value := range defaults {
		if _, exists := merged[key]; !exists {
			merged[key] = value
		}
	}
	for key, value := range extra {
		merged[key] = value
	}
	cmd.Env = make([]string, 0, len(merged))
	for key, value := range merged {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
}

func loadProtectedRunCommandTargets() protectedRunCommandTargets {
	return newProtectedRunCommandTargets(os.Getpid(), os.Getenv("AURAMATE_OWNER_PID"))
}

func newProtectedRunCommandTargets(currentPID int, ownerPID string) protectedRunCommandTargets {
	pidSet := make(map[string]struct{}, 2)
	addPID := func(pid int) {
		if pid <= 0 {
			return
		}
		pidSet[strconv.Itoa(pid)] = struct{}{}
	}
	addPID(currentPID)
	if parsedOwnerPID, err := strconv.Atoi(strings.TrimSpace(ownerPID)); err == nil {
		addPID(parsedOwnerPID)
	}

	nameSet := map[string]struct{}{
		"auramate-server":     {},
		"auramate-server.exe": {},
		"electron":            {},
		"electron.exe":        {},
	}

	targets := protectedRunCommandTargets{
		pids:  make([]string, 0, len(pidSet)),
		names: make([]string, 0, len(nameSet)),
	}
	for pid := range pidSet {
		targets.pids = append(targets.pids, pid)
	}
	for name := range nameSet {
		targets.names = append(targets.names, name)
	}
	return targets
}

func protectedRunCommandGuardReason(command string, targets protectedRunCommandTargets) string {
	return protectedRunCommandGuardReasonRecursive(command, command, targets, 0)
}

func protectedRunCommandGuardReasonRecursive(command string, fullCommand string, targets protectedRunCommandTargets, depth int) string {
	if depth > 2 {
		return ""
	}
	for _, segment := range splitUnquotedShellSegments(command) {
		if reason := protectedRunCommandGuardReasonForSegment(segment, fullCommand, targets); reason != "" {
			return reason
		}
		if nested := unwrapNestedShellCommand(segment); nested != "" && nested != segment {
			if reason := protectedRunCommandGuardReasonRecursive(nested, nested, targets, depth+1); reason != "" {
				return reason
			}
		}
	}
	return ""
}

func protectedRunCommandGuardReasonForSegment(segment string, fullCommand string, targets protectedRunCommandTargets) string {
	tokens, err := tokenizeShellLikeCommand(segment)
	if err != nil || len(tokens) == 0 {
		return ""
	}

	commandName := normalizeShellCommandToken(tokens[0])
	lowerSegment := strings.ToLower(segment)
	lowerFullCommand := strings.ToLower(fullCommand)

	switch commandName {
	case "taskkill", "taskkill.exe", "kill", "kill.exe":
		if pid := firstProtectedPIDReference(lowerSegment, targets.pids); pid != "" {
			return fmt.Sprintf("segment %q targets protected PID %s", segment, pid)
		}
		if name := firstProtectedNameReference(lowerSegment, targets.names); name != "" {
			return fmt.Sprintf("segment %q targets protected process %q", segment, name)
		}
	case "pkill", "killall":
		if name := firstProtectedNameReference(lowerSegment, targets.names); name != "" {
			return fmt.Sprintf("segment %q targets protected process %q", segment, name)
		}
	case "stop-process":
		if pid := firstProtectedPIDReference(lowerSegment, targets.pids); pid != "" {
			return fmt.Sprintf("segment %q targets protected PID %s", segment, pid)
		}
		if name := firstProtectedNameReference(lowerSegment, targets.names); name != "" {
			return fmt.Sprintf("segment %q targets protected process %q", segment, name)
		}
		if strings.Contains(fullCommand, "|") {
			if pid := firstProtectedPIDReference(lowerFullCommand, targets.pids); pid != "" {
				return fmt.Sprintf("pipeline %q targets protected PID %s", fullCommand, pid)
			}
			if name := firstProtectedNameReference(lowerFullCommand, targets.names); name != "" {
				return fmt.Sprintf("pipeline %q targets protected process %q", fullCommand, name)
			}
		}
	case "stop-service":
		if name := firstProtectedNameReference(lowerSegment, targets.names); name != "" {
			return fmt.Sprintf("segment %q targets protected service %q", segment, name)
		}
		if strings.Contains(fullCommand, "|") {
			if name := firstProtectedNameReference(lowerFullCommand, targets.names); name != "" {
				return fmt.Sprintf("pipeline %q targets protected service %q", fullCommand, name)
			}
		}
	case "sc", "sc.exe":
		if len(tokens) < 2 || !strings.EqualFold(tokens[1], "stop") {
			return ""
		}
		if name := firstProtectedNameReference(lowerSegment, targets.names); name != "" {
			return fmt.Sprintf("segment %q targets protected service %q", segment, name)
		}
	case "net":
		if len(tokens) < 2 || !strings.EqualFold(tokens[1], "stop") {
			return ""
		}
		if name := firstProtectedNameReference(lowerSegment, targets.names); name != "" {
			return fmt.Sprintf("segment %q targets protected service %q", segment, name)
		}
	case "wmic":
		if !strings.Contains(lowerSegment, " delete") && !strings.Contains(lowerSegment, "terminate") {
			return ""
		}
		if pid := firstProtectedPIDReference(lowerSegment, targets.pids); pid != "" {
			return fmt.Sprintf("segment %q targets protected PID %s", segment, pid)
		}
		if name := firstProtectedNameReference(lowerSegment, targets.names); name != "" {
			return fmt.Sprintf("segment %q targets protected process %q", segment, name)
		}
	}

	return ""
}

func splitUnquotedShellSegments(command string) []string {
	segments := make([]string, 0, 4)
	start := 0
	var quote byte

	for index := 0; index < len(command); index++ {
		ch := command[index]
		if quote != 0 {
			if ch == '\\' && index+1 < len(command) && command[index+1] == quote {
				index++
				continue
			}
			if ch == quote {
				quote = 0
			}
			continue
		}

		if ch == '\'' || ch == '"' {
			quote = ch
			continue
		}

		if ch != ';' && ch != '|' && ch != '&' {
			continue
		}

		segment := strings.TrimSpace(command[start:index])
		if segment != "" {
			segments = append(segments, segment)
		}
		if (ch == '|' || ch == '&') && index+1 < len(command) && command[index+1] == ch {
			index++
		}
		start = index + 1
	}

	if tail := strings.TrimSpace(command[start:]); tail != "" {
		segments = append(segments, tail)
	}
	return segments
}

func unwrapNestedShellCommand(segment string) string {
	tokens, err := tokenizeShellLikeCommand(segment)
	if err != nil || len(tokens) < 3 {
		return ""
	}

	commandName := normalizeShellCommandToken(tokens[0])
	for index := 1; index < len(tokens)-1; index++ {
		switch commandName {
		case "cmd", "cmd.exe":
			if strings.EqualFold(tokens[index], "/c") || strings.EqualFold(tokens[index], "/k") {
				return strings.Join(tokens[index+1:], " ")
			}
		case "powershell", "powershell.exe", "pwsh", "pwsh.exe", "bash", "bash.exe", "sh", "zsh":
			if strings.EqualFold(tokens[index], "-command") || strings.EqualFold(tokens[index], "-c") {
				return strings.Join(tokens[index+1:], " ")
			}
		}
	}
	return ""
}

func normalizeShellCommandToken(token string) string {
	trimmed := strings.TrimSpace(strings.Trim(token, "\"'"))
	trimmed = strings.ReplaceAll(trimmed, "\\", "/")
	if idx := strings.LastIndex(trimmed, "/"); idx >= 0 {
		trimmed = trimmed[idx+1:]
	}
	return strings.ToLower(trimmed)
}

func firstProtectedPIDReference(command string, protectedPIDs []string) string {
	for _, pid := range protectedPIDs {
		if containsStandaloneNumericReference(command, pid) {
			return pid
		}
	}
	return ""
}

func firstProtectedNameReference(command string, protectedNames []string) string {
	for _, name := range protectedNames {
		if containsProcessNameReference(command, name) {
			return name
		}
	}
	return ""
}

func containsStandaloneNumericReference(command string, value string) bool {
	start := 0
	for {
		index := strings.Index(command[start:], value)
		if index < 0 {
			return false
		}
		index += start
		beforeOK := index == 0 || (command[index-1] < '0' || command[index-1] > '9')
		afterIndex := index + len(value)
		afterOK := afterIndex == len(command) || (command[afterIndex] < '0' || command[afterIndex] > '9')
		if beforeOK && afterOK {
			return true
		}
		start = index + 1
	}
}

func containsProcessNameReference(command string, name string) bool {
	start := 0
	for {
		index := strings.Index(command[start:], name)
		if index < 0 {
			return false
		}
		index += start
		beforeOK := index == 0 || !isProcessNameChar(command[index-1])
		afterIndex := index + len(name)
		afterOK := afterIndex == len(command) || !isProcessNameChar(command[afterIndex])
		if beforeOK && afterOK {
			return true
		}
		start = index + 1
	}
}

func isProcessNameChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') || ch == '.' || ch == '_' || ch == '-'
}

func defaultCommandEnv() map[string]string {
	if runtime.GOOS != "windows" {
		return nil
	}

	return map[string]string{
		"PYTHONIOENCODING": "utf-8",
		"PYTHONUTF8":       "1",
	}
}

// scanCarriageReturn splits data on '\r' or '\n'.
// This allows capturing progress updates from tools like curl/wget that use '\r' to overwrite lines.
func scanCarriageReturn(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}

	// Search for \r or \n
	for i := 0; i < len(data); i++ {
		if data[i] == '\r' {
			// Check for \r\n
			if i+1 < len(data) && data[i+1] == '\n' {
				return i + 2, data[0:i], nil
			}
			return i + 1, data[0:i], nil
		}
		if data[i] == '\n' {
			return i + 1, data[0:i], nil
		}
	}

	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// Basic regex to find "XX%" or "XX.X%"
var progressRegex = regexp.MustCompile(`(\d{1,3}(?:\.\d+)?)%`)

func (cmd *BackgroundCommand) parseProgress(line string) {
	// 1. Extract Percentage
	matches := progressRegex.FindStringSubmatch(line)
	if len(matches) > 1 {
		var p float64
		// Use Sscanf or ParseFloat
		// Sscanf is safer for simple string
		fmt.Sscanf(matches[1], "%f", &p)
		if p >= 0 && p <= 100 {
			cmd.Progress = p
		}
	}

	// 2. Extract Speed/ETA (Simple Heuristics)
	matches = regexp.MustCompile(`(\d+(?:\.\d+)?\s*[kKmMgG]B/s)`).FindStringSubmatch(line)
	if len(matches) > 1 {
		cmd.ProgressDetails = matches[1]
	} else {
		cleanLine := strings.TrimSpace(line)
		if len(cleanLine) > 50 {
			cleanLine = cleanLine[:47] + "..."
		}
		cmd.ProgressDetails = cleanLine
	}

	if cmd.ProgressCallback != nil {
		cmd.ProgressCallback(cmd.Progress, cmd.ProgressDetails)
	}
}

// KillSessionCommands terminates all background commands associated with a session.
func KillSessionCommands(sessionID string) {
	bgMu.Lock()
	for id, cmd := range bgCommands {
		if cmd.SessionID == sessionID {
			cmd.mu.Lock()
			if cmd.Running {
				// Kill process
				if cmd.Cmd.Process != nil {
					cmd.Cmd.Process.Kill()
				}
				// Terminate Job Object (kills entire process tree)
				if cmd.Job != 0 {
					sys.TerminateJobObject(cmd.Job, 1) // Force terminate
					sys.CloseJobObject(cmd.Job)
					cmd.Job = 0
				}
				cmd.Running = false
				cmd.Stderr += "\n[Process killed by session cancellation]"
			}
			cmd.mu.Unlock()
			delete(bgCommands, id)
		}
	}
	bgMu.Unlock()
}
