package shell

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/sys"
)

type StreamCallback func(content string)
type streamContextKey string

const StreamContextKey streamContextKey = "shell_stream_output"

type PersistentShell struct {
	cmd          *exec.Cmd
	job          sys.JobHandle
	stdin        *os.File
	isAlive      bool
	cwd          string
	mu           sync.Mutex
	commandQueue chan *commandExecution
	doneChan     chan struct{}
}

type commandExecution struct {
	command    string
	timeout    time.Duration
	resultChan chan commandResult
	ctx        context.Context
}

type commandResult struct {
	stdout      string
	stderr      string
	exitCode    int
	interrupted bool
	err         error
}

var (
	shellInstance     *PersistentShell
	shellInstanceOnce sync.Once
)

func GetPersistentShell(workingDir string) *PersistentShell {
	shellInstanceOnce.Do(func() {
		shellInstance = newPersistentShell(workingDir)
	})

	if shellInstance == nil {
		shellInstance = newPersistentShell(workingDir)
	} else if !shellInstance.isAlive {
		shellInstance = newPersistentShell(shellInstance.cwd)
	}

	return shellInstance
}

func newPersistentShell(cwd string) *PersistentShell {
	// Get shell configuration from config
	cfg := config.Get()

	// Default to environment variable if config is not set or nil
	var shellPath string
	var shellArgs []string

	if cfg != nil {
		shellPath = cfg.Shell.Path
		shellArgs = cfg.Shell.Args
	}

	if shellPath == "" {
		shellPath = os.Getenv("SHELL")
		if shellPath == "" {
			if path, err := exec.LookPath("bash"); err == nil {
				shellPath = path
			} else {
				shellPath = "/bin/bash"
			}
		}
	}

	// Default shell args
	if len(shellArgs) == 0 {
		shellArgs = []string{"-l"}
	}

	cmd := exec.Command(shellPath, shellArgs...)
	cmd.Dir = cwd

	// Create Job Object for Windows to ensure process tree cleanup
	var jobHandle sys.JobHandle
	if runtime.GOOS == "windows" {
		var err error
		jobHandle, err = sys.CreateJobObject()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to create job object for shell: %v\n", err)
			return nil
		}
	}

	stdinPipe, err := cmd.StdinPipe()
	if err != nil {
		if jobHandle != 0 {
			sys.CloseJobObject(jobHandle)
		}
		return nil
	}

	cmd.Env = append(os.Environ(), "GIT_EDITOR=true")

	err = cmd.Start()
	if err != nil {
		if jobHandle != 0 {
			sys.CloseJobObject(jobHandle)
		}
		return nil
	}

	// Assign process to job object
	if jobHandle != 0 {
		if err := sys.AssignProcessToJobObject(jobHandle, cmd.Process.Pid); err != nil {
			cmd.Process.Kill()
			sys.CloseJobObject(jobHandle)
			fmt.Fprintf(os.Stderr, "Failed to assign shell process to job object: %v\n", err)
			return nil
		}
	}

	shell := &PersistentShell{
		cmd:          cmd,
		job:          jobHandle,
		stdin:        stdinPipe.(*os.File),
		isAlive:      true,
		cwd:          cwd,
		commandQueue: make(chan *commandExecution, 10),
		doneChan:     make(chan struct{}),
	}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "Panic in shell command processor: %v\n", r)
				shell.isAlive = false
				// close(shell.commandQueue) // DO NOT close commandQueue
			}
		}()
		shell.processCommands()
	}()

	go func() {
		err := cmd.Wait()
		if err != nil {
			// Log the error if needed
		}
		
		// Close Job Object when process exits
		if jobHandle != 0 {
			sys.CloseJobObject(jobHandle)
		}

		shell.mu.Lock()
		if shell.isAlive {
			shell.isAlive = false
			close(shell.doneChan)
		}
		shell.mu.Unlock()
	}()

	return shell
}

func (s *PersistentShell) processCommands() {
	for {
		select {
		case cmd := <-s.commandQueue:
			result := s.execCommand(cmd.command, cmd.timeout, cmd.ctx)
			cmd.resultChan <- result
		case <-s.doneChan:
			return
		}
	}
}

func (s *PersistentShell) execCommand(command string, timeout time.Duration, ctx context.Context) commandResult {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isAlive {
		return commandResult{
			stderr:   "Shell is not alive",
			exitCode: 1,
			err:      errors.New("shell is not alive"),
		}
	}

	// Get stream callback
	var streamCallback StreamCallback
	if ctx != nil {
		if v := ctx.Value(StreamContextKey); v != nil {
			if cb, ok := v.(StreamCallback); ok {
				streamCallback = cb
			}
		}
	}

	tempDir := filepath.ToSlash(os.TempDir())
	stdoutFile := filepath.ToSlash(filepath.Join(tempDir, fmt.Sprintf("opencode-stdout-%d", time.Now().UnixNano())))
	stderrFile := filepath.ToSlash(filepath.Join(tempDir, fmt.Sprintf("opencode-stderr-%d", time.Now().UnixNano())))
	statusFile := filepath.ToSlash(filepath.Join(tempDir, fmt.Sprintf("opencode-status-%d", time.Now().UnixNano())))
	cwdFile := filepath.ToSlash(filepath.Join(tempDir, fmt.Sprintf("opencode-cwd-%d", time.Now().UnixNano())))

	defer func() {
		os.Remove(stdoutFile)
		os.Remove(stderrFile)
		os.Remove(statusFile)
		os.Remove(cwdFile)
	}()

	fullCommand := fmt.Sprintf(`
eval %s < /dev/null > %s 2> %s
EXEC_EXIT_CODE=$?
pwd > %s
echo $EXEC_EXIT_CODE > %s
`,
		shellQuote(command),
		shellQuote(stdoutFile),
		shellQuote(stderrFile),
		shellQuote(cwdFile),
		shellQuote(statusFile),
	)

	_, err := s.stdin.Write([]byte(fullCommand + "\n"))
	if err != nil {
		return commandResult{
			stderr:   fmt.Sprintf("Failed to write command to shell: %v", err),
			exitCode: 1,
			err:      err,
		}
	}

	interrupted := false

	startTime := time.Now()

	done := make(chan bool)
	go func() {
		var lastStdoutSize int64
		var lastStderrSize int64

		for {
			select {
			case <-ctx.Done():
				s.killChildren()
				interrupted = true
				done <- true
				return

			case <-time.After(100 * time.Millisecond):
				// Stream output
				if streamCallback != nil {
					var content string
					var newSize int64

					content, newSize = readDelta(stdoutFile, lastStdoutSize)
					if content != "" {
						streamCallback(content)
						lastStdoutSize = newSize
					}

					content, newSize = readDelta(stderrFile, lastStderrSize)
					if content != "" {
						streamCallback(content)
						lastStderrSize = newSize
					}
				}

				if fileExists(statusFile) && fileSize(statusFile) > 0 {
					// Final read to ensure we got everything
					if streamCallback != nil {
						content, newSize := readDelta(stdoutFile, lastStdoutSize)
						if content != "" {
							streamCallback(content)
							lastStdoutSize = newSize
						}

						content, newSize = readDelta(stderrFile, lastStderrSize)
						if content != "" {
							streamCallback(content)
							lastStderrSize = newSize
						}
					}

					done <- true
					return
				}

				if timeout > 0 {
					elapsed := time.Since(startTime)
					if elapsed > timeout {
						s.killChildren()
						interrupted = true
						done <- true
						return
					}
				}
			}
		}
	}()

	<-done

	stdout := readFileOrEmpty(stdoutFile)
	stderr := readFileOrEmpty(stderrFile)
	exitCodeStr := readFileOrEmpty(statusFile)
	newCwd := readFileOrEmpty(cwdFile)

	exitCode := 0
	if exitCodeStr != "" {
		fmt.Sscanf(exitCodeStr, "%d", &exitCode)
	} else if interrupted {
		exitCode = 143
		stderr += "\nCommand execution timed out or was interrupted"
	}

	if newCwd != "" {
		s.cwd = strings.TrimSpace(newCwd)
	}

	return commandResult{
		stdout:      stdout,
		stderr:      stderr,
		exitCode:    exitCode,
		interrupted: interrupted,
	}
}

func (s *PersistentShell) killChildren() {
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}

	// On Windows, pgrep is not available. Use taskkill.
	if runtime.GOOS == "windows" {
		exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", s.cmd.Process.Pid)).Run()
		return
	}

	pgrepCmd := exec.Command("pgrep", "-P", fmt.Sprintf("%d", s.cmd.Process.Pid))
	output, err := pgrepCmd.Output()
	if err != nil {
		return
	}

	for pidStr := range strings.SplitSeq(string(output), "\n") {
		if pidStr = strings.TrimSpace(pidStr); pidStr != "" {
			var pid int
			fmt.Sscanf(pidStr, "%d", &pid)
			if pid > 0 {
				proc, err := os.FindProcess(pid)
				if err == nil {
					proc.Signal(syscall.SIGTERM)
				}
			}
		}
	}
}

func (s *PersistentShell) Exec(ctx context.Context, command string, timeoutMs int) (string, string, int, bool, error) {
	if !s.isAlive {
		return "", "Shell is not alive", 1, false, errors.New("shell is not alive")
	}

	timeout := time.Duration(timeoutMs) * time.Millisecond

	resultChan := make(chan commandResult)

	select {
	case s.commandQueue <- &commandExecution{
		command:    command,
		timeout:    timeout,
		resultChan: resultChan,
		ctx:        ctx,
	}:
	case <-s.doneChan:
		return "", "Shell process exited", 1, false, errors.New("shell process exited")
	}

	result := <-resultChan
	return result.stdout, result.stderr, result.exitCode, result.interrupted, result.err
}

func (s *PersistentShell) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isAlive {
		return
	}

	s.stdin.Write([]byte("exit\n"))

	s.cmd.Process.Kill()
	s.isAlive = false
}

func readDelta(filePath string, offset int64) (string, int64) {
	f, err := os.Open(filePath)
	if err != nil {
		return "", offset
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", offset
	}

	if info.Size() <= offset {
		return "", offset
	}

	newSize := info.Size()
	buf := make([]byte, newSize-offset)
	_, err = f.ReadAt(buf, offset)
	if err != nil {
		return "", offset
	}

	return string(buf), newSize
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func readFileOrEmpty(path string) string {
	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(content)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}
