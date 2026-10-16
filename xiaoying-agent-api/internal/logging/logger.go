package logging

import (
	"fmt"
	"log/slog"
	"os"

	// "path/filepath"
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"gopkg.in/natefinch/lumberjack.v2"
)

var (
	logFile *lumberjack.Logger
)

func Init(logPath string, debug bool) {
	if logPath == "" {
		if envPath := os.Getenv("AURAMATE_LOG_PATH"); envPath != "" {
			logPath = envPath
		} else {
			home, _ := os.UserHomeDir()
			logPath = filepath.Join(home, ".auramate", "logs", "auramate.log")
		}
	}

	// Ensure directory exists
	if err := os.MkdirAll(filepath.Dir(logPath), 0755); err != nil {
		fmt.Printf("Failed to create log directory: %v. Falling back to local directory.\n", err)
		// Fallback to local directory
		logPath = "auramate.log"
	}

	logFile = &lumberjack.Logger{
		Filename:   logPath,
		MaxSize:    10, // megabytes
		MaxBackups: 5,
		MaxAge:     28,   // days
		Compress:   true, // disabled by default
	}

	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}

	// Create a multi-writer to write to both stdout and file
	// Note: slog doesn't support multi-handler natively in a simple way for different formats,
	// but we can just use JSON handler for file and Text for stdout if we wanted,
	// or just write to both.
	// For simplicity, let's keep using the default handler which might be set externally,
	// OR we enforce a new handler here.

	// Let's replace the default logger with one that writes to file (JSON) and stdout (Text)
	// Actually, standard practice for CLI/Server is often just stdout and let systemd/docker handle rotation.
	// But user asked for log rotation *in system stability*.

	// We will use a custom handler or just set output to the lumberjack logger if running in background?
	// For now, let's just make sure we log to file as well.

	opts := &slog.HandlerOptions{
		Level:     level,
		AddSource: true,
	}

	// We'll use JSON for file log
	fileHandler := slog.NewJSONHandler(logFile, opts)

	// And Text for stdout
	stdoutHandler := slog.NewTextHandler(os.Stdout, opts)

	// Combine them (simple fan-out)
	handler := &MultiHandler{
		handlers: []slog.Handler{fileHandler, stdoutHandler},
	}

	slog.SetDefault(slog.New(handler))
	Info("Logging initialized", "path", logPath, "debug", debug)
}

type MultiHandler struct {
	handlers []slog.Handler
}

func (h *MultiHandler) Enabled(ctx context.Context, level slog.Level) bool {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, level) {
			return true
		}
	}
	return false
}

func (h *MultiHandler) Handle(ctx context.Context, r slog.Record) error {
	for _, handler := range h.handlers {
		if handler.Enabled(ctx, r.Level) {
			if err := handler.Handle(ctx, r); err != nil {
				return err
			}
		}
	}
	return nil
}

func (h *MultiHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	newHandlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		newHandlers[i] = handler.WithAttrs(attrs)
	}
	return &MultiHandler{handlers: newHandlers}
}

func (h *MultiHandler) WithGroup(name string) slog.Handler {
	newHandlers := make([]slog.Handler, len(h.handlers))
	for i, handler := range h.handlers {
		newHandlers[i] = handler.WithGroup(name)
	}
	return &MultiHandler{handlers: newHandlers}
}

func Stack() string {
	return string(debug.Stack())
}

func getCaller() string {
	var caller string
	if _, file, line, ok := runtime.Caller(2); ok {
		// caller = fmt.Sprintf("%s:%d", filepath.Base(file), line)
		caller = fmt.Sprintf("%s:%d", file, line)
	} else {
		caller = "unknown"
	}
	return caller
}
func Info(msg string, args ...any) {
	source := getCaller()
	slog.Info(msg, append([]any{"source", source}, args...)...)
}

func Debug(msg string, args ...any) {
	// slog.Debug(msg, args...)
	source := getCaller()
	slog.Debug(msg, append([]any{"source", source}, args...)...)
}

func Warn(msg string, args ...any) {
	slog.Warn(msg, args...)
}

func Error(msg string, args ...any) {
	slog.Error(msg, args...)
}

func InfoPersist(msg string, args ...any) {
	args = append(args, persistKeyArg, true)
	slog.Info(msg, args...)
}

func DebugPersist(msg string, args ...any) {
	args = append(args, persistKeyArg, true)
	slog.Debug(msg, args...)
}

func WarnPersist(msg string, args ...any) {
	args = append(args, persistKeyArg, true)
	slog.Warn(msg, args...)
}

func ErrorPersist(msg string, args ...any) {
	args = append(args, persistKeyArg, true)
	slog.Error(msg, args...)
}

// RecoverPanic is a common function to handle panics gracefully.
// It logs the error, creates a panic log file with stack trace,
// and executes an optional cleanup function before returning.
func RecoverPanic(name string, cleanup func()) {
	if r := recover(); r != nil {
		// Log the panic
		ErrorPersist(fmt.Sprintf("Panic in %s: %v", name, r))

		// Create a timestamped panic log file
		timestamp := time.Now().Format("20060102-150405")
		filename := fmt.Sprintf("opencode-panic-%s-%s.log", name, timestamp)

		file, err := os.Create(filename)
		if err != nil {
			ErrorPersist(fmt.Sprintf("Failed to create panic log: %v", err))
		} else {
			defer file.Close()

			// Write panic information and stack trace
			fmt.Fprintf(file, "Panic in %s: %v\n\n", name, r)
			fmt.Fprintf(file, "Time: %s\n\n", time.Now().Format(time.RFC3339))
			fmt.Fprintf(file, "Stack Trace:\n%s\n", debug.Stack())

			InfoPersist(fmt.Sprintf("Panic details written to %s", filename))
		}

		// Execute cleanup function if provided
		if cleanup != nil {
			cleanup()
		}
	}
}

// Message Logging for Debug
var MessageDir string

func GetSessionPrefix(sessionId string) string {
	return sessionId[:8]
}

var sessionLogMutex sync.Mutex

func AppendToSessionLogFile(sessionId string, filename string, content string) string {
	if MessageDir == "" || sessionId == "" {
		return ""
	}
	sessionPrefix := GetSessionPrefix(sessionId)

	sessionLogMutex.Lock()
	defer sessionLogMutex.Unlock()

	sessionPath := fmt.Sprintf("%s/%s", MessageDir, sessionPrefix)
	if _, err := os.Stat(sessionPath); os.IsNotExist(err) {
		if err := os.MkdirAll(sessionPath, 0o766); err != nil {
			Error("Failed to create session directory", "dirpath", sessionPath, "error", err)
			return ""
		}
	}

	filePath := fmt.Sprintf("%s/%s", sessionPath, filename)

	f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		Error("Failed to open session log file", "filepath", filePath, "error", err)
		return ""
	}
	defer f.Close()

	// Append chunk to file
	_, err = f.WriteString(content)
	if err != nil {
		Error("Failed to write chunk to session log file", "filepath", filePath, "error", err)
		return ""
	}
	return filePath
}

func WriteRequestMessageJson(sessionId string, requestSeqId int, message any) string {
	if MessageDir == "" || sessionId == "" || requestSeqId <= 0 {
		return ""
	}
	msgJson, err := json.Marshal(message)
	if err != nil {
		Error("Failed to marshal message", "session_id", sessionId, "request_seq_id", requestSeqId, "error", err)
		return ""
	}
	return WriteRequestMessage(sessionId, requestSeqId, string(msgJson))
}

func WriteRequestMessage(sessionId string, requestSeqId int, message string) string {
	if MessageDir == "" || sessionId == "" || requestSeqId <= 0 {
		return ""
	}
	filename := fmt.Sprintf("%d_request.json", requestSeqId)

	return AppendToSessionLogFile(sessionId, filename, message)
}

func AppendToStreamSessionLogJson(sessionId string, requestSeqId int, jsonableChunk any) string {
	if MessageDir == "" || sessionId == "" || requestSeqId <= 0 {
		return ""
	}
	chunkJson, err := json.Marshal(jsonableChunk)
	if err != nil {
		Error("Failed to marshal message", "session_id", sessionId, "request_seq_id", requestSeqId, "error", err)
		return ""
	}
	return AppendToStreamSessionLog(sessionId, requestSeqId, string(chunkJson))
}

func AppendToStreamSessionLog(sessionId string, requestSeqId int, chunk string) string {
	if MessageDir == "" || sessionId == "" || requestSeqId <= 0 {
		return ""
	}
	filename := fmt.Sprintf("%d_response_stream.log", requestSeqId)
	return AppendToSessionLogFile(sessionId, filename, chunk)
}

func WriteChatResponseJson(sessionId string, requestSeqId int, response any) string {
	if MessageDir == "" || sessionId == "" || requestSeqId <= 0 {
		return ""
	}
	responseJson, err := json.Marshal(response)
	if err != nil {
		Error("Failed to marshal response", "session_id", sessionId, "request_seq_id", requestSeqId, "error", err)
		return ""
	}
	filename := fmt.Sprintf("%d_response.json", requestSeqId)

	return AppendToSessionLogFile(sessionId, filename, string(responseJson))
}

func WriteToolResultsJson(sessionId string, requestSeqId int, toolResults any) string {
	if MessageDir == "" || sessionId == "" || requestSeqId <= 0 {
		return ""
	}
	toolResultsJson, err := json.Marshal(toolResults)
	if err != nil {
		Error("Failed to marshal tool results", "session_id", sessionId, "request_seq_id", requestSeqId, "error", err)
		return ""
	}
	filename := fmt.Sprintf("%d_tool_results.json", requestSeqId)
	return AppendToSessionLogFile(sessionId, filename, string(toolResultsJson))
}
