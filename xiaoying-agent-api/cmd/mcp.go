package cmd

import (
	"fmt"

	"github.com/auramate/auramate/internal/mcp/servers/chrome"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Run internal MCP servers",
	Long:  `Run various internal MCP servers provided by OpenCode.`,
}

var chromeMcpCmd = &cobra.Command{
	Use:   "chrome",
	Short: "Run Chrome DevTools MCP Server",
	Long:  `Starts the Chrome DevTools MCP server for browser automation.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		fmt.Println("Starting Chrome MCP Server...")
		s := chrome.NewServer()
		s.Start()
		return nil
	},
}

func init() {
	rootCmd.AddCommand(mcpCmd)
	mcpCmd.AddCommand(chromeMcpCmd)
}
