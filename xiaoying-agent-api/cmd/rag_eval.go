package cmd

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/llm/embedding"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/memory"
	"github.com/auramate/auramate/internal/message"
	"github.com/spf13/cobra"
)

type ragEvalCase struct {
	Query    string
	Expected string
}

type ragEvalSummary struct {
	Cases     int
	Top1Hits  int
	Top3Hits  int
	NoResults int
	Errors    int
}

var ragEvalCmd = &cobra.Command{
	Use:   "rag-eval",
	Short: "Evaluate RAG recall hit rate against the real database",
	Long: `Evaluate current RAG recall quality against the real database.

Each case is a pair of:
  query => expected substring

The command reports Top1 / Top3 hit rate for memory recall, message recall, or both.
Use '||' inside expected text to provide multiple acceptable substrings.
Examples:
  --case "redis endpoint 是什么=>redis://localhost:6379"
  --case "默认回答风格=>简洁||先给结论"`,
	RunE: func(cmd *cobra.Command, args []string) error {
		debug, _ := cmd.Flags().GetBool("debug")
		cwd, _ := cmd.Flags().GetString("cwd")
		dataDirOverride, _ := cmd.Flags().GetString("data-dir")
		mode, _ := cmd.Flags().GetString("mode")
		caseSpecs, _ := cmd.Flags().GetStringArray("case")
		casesFile, _ := cmd.Flags().GetString("cases-file")
		limit, _ := cmd.Flags().GetInt("limit")
		category, _ := cmd.Flags().GetString("category")
		sessionID, _ := cmd.Flags().GetString("session-id")

		mode = strings.ToLower(strings.TrimSpace(mode))
		switch mode {
		case "memory", "message", "both":
		default:
			return fmt.Errorf("unsupported mode %q, expected one of: memory, message, both", mode)
		}
		if limit < 1 {
			return fmt.Errorf("limit must be >= 1")
		}

		if cwd != "" {
			if err := os.Chdir(cwd); err != nil {
				return fmt.Errorf("failed to change directory: %v", err)
			}
		}

		absCwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("failed to get current working directory: %v", err)
		}

		cfg, err := config.Load(absCwd, debug)
		if err != nil {
			return err
		}
		if strings.TrimSpace(dataDirOverride) != "" {
			cfg.Data.Directory = strings.TrimSpace(dataDirOverride)
			config.Get().Data.Directory = cfg.Data.Directory
		}
		logging.Init("", debug)

		cases, err := loadRAGEvalCases(casesFile, caseSpecs)
		if err != nil {
			return err
		}
		if len(cases) == 0 {
			return fmt.Errorf("no evaluation cases were provided")
		}

		embedder, err := embedding.NewConfiguredEmbedder(cfg)
		if err != nil {
			return fmt.Errorf("failed to initialize embedder: %w", err)
		}
		if embedder == nil {
			return fmt.Errorf("no embeddings provider is configured")
		}

		conn, err := db.Connect()
		if err != nil {
			return err
		}
		defer conn.Close()

		queries := db.New(conn)
		memories := memory.NewService(queries, conn, embedder)
		messages := message.NewService(queries, embedder)

		ctx := context.Background()
		dbPath, err := filepath.Abs(filepath.Join(cfg.Data.Directory, "database.db"))
		if err != nil {
			return fmt.Errorf("failed to resolve database path: %w", err)
		}
		fmt.Printf("provider=%s\n", embedding.FixedEmbeddingProvider)
		fmt.Printf("model=%s\n", embedding.FixedEmbeddingModel)
		fmt.Printf("data_dir=%s\n", cfg.Data.Directory)
		fmt.Printf("db_path=%s\n", dbPath)
		fmt.Printf("mode=%s\n", mode)
		fmt.Printf("cases=%d\n", len(cases))
		fmt.Printf("limit=%d\n", limit)
		if sessionID != "" {
			fmt.Printf("session_id=%s\n", sessionID)
		}
		if category != "" {
			fmt.Printf("category=%s\n", category)
		}

		if mode == "memory" || mode == "both" {
			memorySummary := evaluateMemoryCases(ctx, memories, cases, limit, category)
			printSummary("memory", memorySummary)
		}
		if mode == "message" || mode == "both" {
			messageSummary := evaluateMessageCases(ctx, messages, cases, limit, sessionID)
			printSummary("message", messageSummary)
		}

		return nil
	},
}

func evaluateMemoryCases(ctx context.Context, svc memory.Service, cases []ragEvalCase, limit int, category string) ragEvalSummary {
	summary := ragEvalSummary{}
	fmt.Println("=== memory ===")
	for index, item := range cases {
		summary.Cases++
		var (
			result *memory.MemorySearchResult
			err    error
		)
		if strings.TrimSpace(category) != "" {
			result, err = svc.SearchByCategoryWithPolicy(ctx, memory.DefaultCategorySearchRequest(item.Query, category, limit))
		} else {
			result, err = svc.SearchWithPolicy(ctx, memory.DefaultSearchRequest(item.Query, limit))
		}

		fmt.Printf("case=%d\n", index+1)
		fmt.Printf("query=%s\n", item.Query)
		fmt.Printf("expected=%s\n", item.Expected)
		if err != nil {
			summary.Errors++
			fmt.Printf("error=%v\n\n", err)
			continue
		}

		fmt.Printf("channels=%v\n", result.Decision.UsedChannels)
		fmt.Printf("fallback=%s\n", result.Decision.FallbackPath)
		if len(result.Memories) == 0 {
			summary.NoResults++
			fmt.Println("top1_hit=false")
			fmt.Println("top3_hit=false")
			fmt.Println("results=0")
			fmt.Println()
			continue
		}

		needles := splitExpectedNeedles(item.Expected)
		top1Hit := memoryHitAt(result.Memories, needles, 1)
		top3Hit := memoryHitAt(result.Memories, needles, min(limit, 3))
		if top1Hit {
			summary.Top1Hits++
		}
		if top3Hit {
			summary.Top3Hits++
		}
		fmt.Printf("top1_hit=%t\n", top1Hit)
		fmt.Printf("top3_hit=%t\n", top3Hit)
		fmt.Printf("results=%d\n", len(result.Memories))
		for rank, mem := range result.Memories {
			fmt.Printf("%d. [%s] %s\n", rank+1, mem.Category, summarizeEvalText(mem.Content, 180))
		}
		fmt.Println()
	}
	return summary
}

func evaluateMessageCases(ctx context.Context, svc message.Service, cases []ragEvalCase, limit int, sessionID string) ragEvalSummary {
	summary := ragEvalSummary{}
	fmt.Println("=== message ===")
	for index, item := range cases {
		summary.Cases++
		var (
			results []message.Message
			err     error
		)
		if strings.TrimSpace(sessionID) != "" {
			results, err = svc.SearchInSession(ctx, sessionID, item.Query, limit)
		} else {
			results, err = svc.Search(ctx, item.Query, limit)
		}

		fmt.Printf("case=%d\n", index+1)
		fmt.Printf("query=%s\n", item.Query)
		fmt.Printf("expected=%s\n", item.Expected)
		if err != nil {
			summary.Errors++
			fmt.Printf("error=%v\n\n", err)
			continue
		}
		if len(results) == 0 {
			summary.NoResults++
			fmt.Println("top1_hit=false")
			fmt.Println("top3_hit=false")
			fmt.Println("results=0")
			fmt.Println()
			continue
		}

		needles := splitExpectedNeedles(item.Expected)
		top1Hit := messageHitAt(results, needles, 1)
		top3Hit := messageHitAt(results, needles, min(limit, 3))
		if top1Hit {
			summary.Top1Hits++
		}
		if top3Hit {
			summary.Top3Hits++
		}
		fmt.Printf("top1_hit=%t\n", top1Hit)
		fmt.Printf("top3_hit=%t\n", top3Hit)
		fmt.Printf("results=%d\n", len(results))
		for rank, msg := range results {
			fmt.Printf("%d. [%s] %s\n", rank+1, msg.Role, summarizeEvalText(msg.Content().String(), 180))
		}
		fmt.Println()
	}
	return summary
}

func printSummary(name string, summary ragEvalSummary) {
	if summary.Cases == 0 {
		return
	}
	denom := float64(summary.Cases)
	fmt.Printf("%s_cases=%d\n", name, summary.Cases)
	fmt.Printf("%s_top1_hits=%d\n", name, summary.Top1Hits)
	fmt.Printf("%s_top3_hits=%d\n", name, summary.Top3Hits)
	fmt.Printf("%s_no_results=%d\n", name, summary.NoResults)
	fmt.Printf("%s_errors=%d\n", name, summary.Errors)
	fmt.Printf("%s_top1_rate=%.2f%%\n", name, float64(summary.Top1Hits)*100/denom)
	fmt.Printf("%s_top3_rate=%.2f%%\n", name, float64(summary.Top3Hits)*100/denom)
}

func loadRAGEvalCases(casesFile string, specs []string) ([]ragEvalCase, error) {
	var cases []ragEvalCase
	for _, spec := range specs {
		item, err := parseRAGEvalCaseLine(spec)
		if err != nil {
			return nil, err
		}
		cases = append(cases, item)
	}

	if strings.TrimSpace(casesFile) == "" {
		return cases, nil
	}

	data, err := os.ReadFile(casesFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read cases file: %w", err)
	}
	for lineNo, raw := range strings.Split(string(data), "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		item, err := parseRAGEvalCaseLine(line)
		if err != nil {
			return nil, fmt.Errorf("cases file line %d: %w", lineNo+1, err)
		}
		cases = append(cases, item)
	}
	return cases, nil
}

func parseRAGEvalCaseLine(line string) (ragEvalCase, error) {
	line = strings.TrimSpace(line)
	if line == "" {
		return ragEvalCase{}, fmt.Errorf("empty case line")
	}

	var parts []string
	if strings.Contains(line, "\t") {
		parts = strings.SplitN(line, "\t", 2)
	} else if strings.Contains(line, "=>") {
		parts = strings.SplitN(line, "=>", 2)
	} else {
		return ragEvalCase{}, fmt.Errorf("invalid case format %q, expected 'query=>expected' or tab-separated pair", line)
	}

	query := strings.TrimSpace(parts[0])
	expected := strings.TrimSpace(parts[1])
	if query == "" || expected == "" {
		return ragEvalCase{}, fmt.Errorf("invalid case %q, query and expected must both be non-empty", line)
	}
	return ragEvalCase{Query: query, Expected: expected}, nil
}

func splitExpectedNeedles(expected string) []string {
	parts := strings.Split(expected, "||")
	needles := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			needles = append(needles, strings.ToLower(trimmed))
		}
	}
	if len(needles) == 0 {
		needles = append(needles, strings.ToLower(strings.TrimSpace(expected)))
	}
	return needles
}

func memoryHitAt(memories []db.Memory, needles []string, topN int) bool {
	for index := 0; index < len(memories) && index < topN; index++ {
		text := strings.ToLower(memories[index].Content)
		for _, needle := range needles {
			if strings.Contains(text, needle) {
				return true
			}
		}
	}
	return false
}

func messageHitAt(messages []message.Message, needles []string, topN int) bool {
	for index := 0; index < len(messages) && index < topN; index++ {
		text := strings.ToLower(messages[index].Content().String())
		for _, needle := range needles {
			if strings.Contains(text, needle) {
				return true
			}
		}
	}
	return false
}

func summarizeEvalText(text string, limit int) string {
	text = strings.TrimSpace(strings.ReplaceAll(text, "\n", " "))
	if limit <= 0 || len(text) <= limit {
		return text
	}
	return text[:limit] + "..."
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func init() {
	rootCmd.AddCommand(ragEvalCmd)
	ragEvalCmd.Flags().Bool("debug", false, "Enable debug logging")
	ragEvalCmd.Flags().String("cwd", "", "Working directory")
	ragEvalCmd.Flags().String("data-dir", "", "Override data directory for the evaluation database")
	ragEvalCmd.Flags().String("mode", "memory", "Evaluation mode: memory, message, or both")
	ragEvalCmd.Flags().StringArray("case", nil, "Single evaluation case in the form 'query=>expected'")
	ragEvalCmd.Flags().String("cases-file", "", "Path to a file containing cases, one per line as 'query=>expected' or tab-separated")
	ragEvalCmd.Flags().Int("limit", 3, "How many top results to evaluate and print")
	ragEvalCmd.Flags().String("category", "", "Optional memory category filter for memory mode")
	ragEvalCmd.Flags().String("session-id", "", "Optional session id for session-scoped message recall")
}
