package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/auramate/auramate/internal/app"
	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/logging"
	"github.com/spf13/cobra"
)

var memoryDedupCmd = &cobra.Command{
	Use:   "memory-dedup",
	Short: "Run offline memory deduplication",
	RunE: func(cmd *cobra.Command, args []string) error {
		debug, _ := cmd.Flags().GetBool("debug")
		cwd, _ := cmd.Flags().GetString("cwd")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		threshold, _ := cmd.Flags().GetFloat32("threshold")

		if cwd != "" {
			if err := os.Chdir(cwd); err != nil {
				return fmt.Errorf("failed to change directory: %v", err)
			}
		}

		absCwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current working directory: %v", err)
		}

		_, err = config.Load(absCwd, debug)
		if err != nil {
			return err
		}
		logging.Init("", debug)

		conn, err := db.Connect()
		if err != nil {
			return err
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		application, err := app.New(ctx, conn)
		if err != nil {
			return err
		}
		defer application.Shutdown()

		stats, err := application.Memories.Deduplicate(ctx, threshold, dryRun)
		if err != nil {
			return err
		}

		fmt.Printf("mode=%s\n", map[bool]string{true: "dry-run", false: "apply"}[dryRun])
		fmt.Printf("threshold=%.3f\n", threshold)
		fmt.Printf("total_memories=%d\n", stats.TotalMemories)
		fmt.Printf("scanned_categories=%d\n", stats.ScannedCategories)
		fmt.Printf("duplicate_by_text=%d\n", stats.DuplicateByText)
		fmt.Printf("duplicate_by_semantic=%d\n", stats.DuplicateBySemantic)
		fmt.Printf("deleted=%d\n", stats.Deleted)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(memoryDedupCmd)
	memoryDedupCmd.Flags().Bool("debug", false, "Enable debug logging")
	memoryDedupCmd.Flags().String("cwd", "", "Working directory")
	memoryDedupCmd.Flags().Bool("dry-run", true, "Run without deleting duplicate memories")
	memoryDedupCmd.Flags().Float32("threshold", 0.92, "Cosine similarity threshold for semantic dedup")
}
