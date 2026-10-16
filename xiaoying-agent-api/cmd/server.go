package cmd

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"syscall"
	"time"

	"github.com/auramate/auramate/internal/app"
	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/server"
	"github.com/spf13/cobra"
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Run XiaoYing in server mode (Sidecar) or manage service",
	Long:  `Starts the XiaoYing backend server for handling WebSocket connections. Can also manage Windows Service.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		// Flags
		debug, _ := cmd.Flags().GetBool("debug")
		cwd, _ := cmd.Flags().GetString("cwd")
		port, _ := cmd.Flags().GetInt("port")

		if cwd != "" {
			err := os.Chdir(cwd)
			if err != nil {
				return fmt.Errorf("failed to change directory: %v", err)
			}
		}

		absCwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current working directory: %v", err)
		}

		// Normal Server Run (Foreground)
		_, err = config.Load(absCwd, debug)
		if err != nil {
			return err
		}

		// Configure logging
		logging.Init("", debug)

		conn, err := db.Connect()
		if err != nil {
			return err
		}

		// Phase 3: Startup Self-Check - Cleanup Orphaned Processes
		// Windows Job Objects (Phase 2) handle this for crashes, but we double-check for legacy processes.
		cleanupOrphanedProcesses(debug)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		application, err := app.New(ctx, conn)
		if err != nil {
			return err
		}
		defer func() {
			logging.Info("Shutting down application resources...")
			if err := application.Shutdown(); err != nil {
				logging.Error("Failed to shutdown app", "error", err)
			}
		}()

		srv := server.New(application, port)
		application.Scheduler.SetNotifier(srv)

		logging.Info("Running in interactive mode")

		go func() {
			if err := srv.Start(); err != nil {
				logging.Error("Server failed", err)
				cancel()
			}
		}()

		// Monitor Owner PID if provided
		ownerPidStr := os.Getenv("AURAMATE_OWNER_PID")
		if ownerPidStr != "" {
			ownerPid, err := strconv.Atoi(ownerPidStr)
			if err == nil && ownerPid > 0 {
				logging.Info("Monitoring owner process", "pid", ownerPid)
				proc, err := os.FindProcess(ownerPid)
				if err == nil {
					go func() {
						if runtime.GOOS == "windows" {
							// On Windows, Wait() blocks until the process exits
							_, _ = proc.Wait()
							logging.Info("Owner process died, exiting...")
							cancel()
						} else {
							// On Unix, we poll with Signal(0)
							for {
								time.Sleep(2 * time.Second)
								err := proc.Signal(syscall.Signal(0))
								if err != nil {
									logging.Info("Owner process died, exiting...")
									cancel()
									return
								}
							}
						}
					}()
				}
			}
		}

		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

		select {
		case <-quit:
			logging.Info("Shutting down server...")
		case <-ctx.Done():
			logging.Info("Context cancelled, shutting down...")
		}

		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer shutdownCancel()

		return srv.Shutdown(shutdownCtx)
	},
}

func init() {
	rootCmd.AddCommand(serverCmd)
	serverCmd.Flags().IntP("port", "P", 3000, "Port to run the server on")
	serverCmd.Flags().Bool("debug", false, "Enable debug logging")
	serverCmd.Flags().String("cwd", "", "Working directory")
}

func cleanupOrphanedProcesses(debug bool) {
	// Only run on Windows for now as we use PowerShell
	// In a real production env, we should check runtime.GOOS
	logging.Info("Startup Check: Scanning for orphaned processes...")

	// Kill python processes running 'server.py' (common in this project)
	// We use a non-blocking approach so we don't hang startup if PS is slow
	go func() {
		// Define patterns to kill
		// 1. Python running server.py
		// 2. Node processes (if any specific ones are known, e.g. 'index.js')
		// 3. Persistent Shells (bash.exe/cmd.exe) are harder to distinguish from user's own shells,
		//    so we rely on Job Objects for those.

		patterns := []string{
			"*server.py*",
			"*auramate_agent_service*", // Self-cleanup of old services?
		}

		for _, p := range patterns {
			// Construct PowerShell command to find and kill
			// Get-CimInstance Win32_Process | Where-Object {$_.CommandLine -like "PATTERN"} | Stop-Process -Force
			cmdStr := fmt.Sprintf(`Get-CimInstance Win32_Process | Where-Object {$_.CommandLine -like "%s"} | Stop-Process -Force`, p)

			// We use exec.Command directly here to avoid circular dependency with internal/llm/tools
			// or just use a simple exec
			cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", cmdStr)
			if debug {
				logging.Debug("Executing cleanup", "command", cmdStr)
			}
			// Ignore errors (process might not exist)
			_ = cmd.Run()
		}
		logging.Info("Startup Check: Orphaned process cleanup completed")
	}()
}
