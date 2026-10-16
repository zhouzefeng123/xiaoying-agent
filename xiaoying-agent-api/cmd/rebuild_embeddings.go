package cmd

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/llm/embedding"
	"github.com/auramate/auramate/internal/logging"
	"github.com/auramate/auramate/internal/message"
	"github.com/spf13/cobra"
)

type rebuildEmbeddingsStats struct {
	MessageScanned   int
	MessageUpdated   int
	MessageCleared   int
	MessageSkipped   int
	MessageErrored   int
	KnowledgeScanned int
	KnowledgeUpdated int
	KnowledgeCleared int
	KnowledgeSkipped int
	KnowledgeErrored int
}

type messageEmbeddingRow struct {
	ID    string
	Parts string
}

type knowledgeEmbeddingRow struct {
	ID      string
	Content string
}

type messageEmbeddingJob struct {
	ID   string
	Text string
}

type knowledgeEmbeddingJob struct {
	ID   string
	Text string
}

var rebuildEmbeddingsCmd = &cobra.Command{
	Use:   "rebuild-embeddings",
	Short: "Rebuild stored embeddings with the current embeddings provider",
	RunE: func(cmd *cobra.Command, args []string) error {
		debug, _ := cmd.Flags().GetBool("debug")
		cwd, _ := cmd.Flags().GetString("cwd")
		dryRun, _ := cmd.Flags().GetBool("dry-run")
		includeMessages, _ := cmd.Flags().GetBool("messages")
		includeKnowledge, _ := cmd.Flags().GetBool("knowledge")
		clearEmpty, _ := cmd.Flags().GetBool("clear-empty")
		allRows, _ := cmd.Flags().GetBool("all")
		batchSize, _ := cmd.Flags().GetInt("batch-size")

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
		logging.Init("", debug)

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
		conn.SetMaxOpenConns(1)
		conn.SetMaxIdleConns(1)
		if _, err := conn.ExecContext(context.Background(), "PRAGMA busy_timeout = 5000;"); err != nil {
			logging.Warn("Failed to set busy_timeout for rebuild command", "error", err)
		}

		ctx := context.Background()
		stats := rebuildEmbeddingsStats{}

		if includeMessages {
			if err := rebuildMessageEmbeddings(ctx, conn, embedder, dryRun, clearEmpty, allRows, batchSize, &stats); err != nil {
				return err
			}
		}
		if includeKnowledge {
			if err := rebuildKnowledgeEmbeddings(ctx, conn, embedder, dryRun, clearEmpty, allRows, batchSize, &stats); err != nil {
				return err
			}
		}

		fmt.Printf("mode=%s\n", map[bool]string{true: "dry-run", false: "apply"}[dryRun])
		fmt.Printf("provider=%s\n", embedding.FixedEmbeddingProvider)
		fmt.Printf("model=%s\n", embedding.FixedEmbeddingModel)
		fmt.Printf("messages_scanned=%d\n", stats.MessageScanned)
		fmt.Printf("messages_updated=%d\n", stats.MessageUpdated)
		fmt.Printf("messages_cleared=%d\n", stats.MessageCleared)
		fmt.Printf("messages_skipped=%d\n", stats.MessageSkipped)
		fmt.Printf("messages_errored=%d\n", stats.MessageErrored)
		fmt.Printf("knowledge_scanned=%d\n", stats.KnowledgeScanned)
		fmt.Printf("knowledge_updated=%d\n", stats.KnowledgeUpdated)
		fmt.Printf("knowledge_cleared=%d\n", stats.KnowledgeCleared)
		fmt.Printf("knowledge_skipped=%d\n", stats.KnowledgeSkipped)
		fmt.Printf("knowledge_errored=%d\n", stats.KnowledgeErrored)
		return nil
	},
}

func rebuildMessageEmbeddings(ctx context.Context, conn *sql.DB, embedder embedding.Embedder, dryRun bool, clearEmpty bool, allRows bool, batchSize int, stats *rebuildEmbeddingsStats) error {
	items, err := loadMessageEmbeddingRows(ctx, conn, allRows)
	if err != nil {
		return err
	}
	if batchSize < 1 {
		batchSize = 1
	}

	pending := make([]messageEmbeddingJob, 0, batchSize)

	for _, item := range items {
		id := item.ID
		parts := item.Parts

		stats.MessageScanned++
		text := strings.TrimSpace(message.ExtractEmbeddingTextFromJSON(parts))
		if len(text) > 8000 {
			text = text[:8000]
		}

		if text == "" {
			if clearEmpty {
				stats.MessageCleared++
				if !dryRun {
					if _, err := conn.ExecContext(ctx, `
						UPDATE messages
						SET embedding = NULL, updated_at = (strftime('%s', 'now') * 1000)
						WHERE id = ?
					`, id); err != nil {
						stats.MessageErrored++
						logging.Error("Failed to clear message embedding", "id", id, "error", err)
					}
				}
			} else {
				stats.MessageSkipped++
			}
			continue
		}

		pending = append(pending, messageEmbeddingJob{ID: id, Text: text})
		if len(pending) < batchSize {
			continue
		}
		if err := persistMessageEmbeddingBatch(ctx, conn, embedder, pending, dryRun, stats); err != nil {
			return err
		}
		pending = pending[:0]
	}

	if len(pending) > 0 {
		if err := persistMessageEmbeddingBatch(ctx, conn, embedder, pending, dryRun, stats); err != nil {
			return err
		}
	}

	return nil
}

func persistMessageEmbeddingBatch(ctx context.Context, conn *sql.DB, embedder embedding.Embedder, jobs []messageEmbeddingJob, dryRun bool, stats *rebuildEmbeddingsStats) error {
	texts := make([]string, 0, len(jobs))
	for _, job := range jobs {
		texts = append(texts, job.Text)
	}

	vectors, err := embedder.Embed(ctx, texts)
	if err != nil || len(vectors) != len(jobs) {
		for _, job := range jobs {
			if err := persistSingleMessageEmbedding(ctx, conn, embedder, job, dryRun, stats); err != nil {
				return err
			}
		}
		return nil
	}

	for index, job := range jobs {
		payload, err := json.Marshal(vectors[index])
		if err != nil {
			stats.MessageErrored++
			logging.Error("Failed to marshal message embedding", "id", job.ID, "error", err)
			continue
		}

		stats.MessageUpdated++
		if dryRun {
			continue
		}

		if _, err := conn.ExecContext(ctx, `
			UPDATE messages
			SET embedding = ?, updated_at = (strftime('%s', 'now') * 1000)
			WHERE id = ?
		`, string(payload), job.ID); err != nil {
			stats.MessageErrored++
			logging.Error("Failed to persist message embedding", "id", job.ID, "error", err)
		}
	}

	return nil
}

func persistSingleMessageEmbedding(ctx context.Context, conn *sql.DB, embedder embedding.Embedder, job messageEmbeddingJob, dryRun bool, stats *rebuildEmbeddingsStats) error {
	vectors, err := embedder.Embed(ctx, []string{job.Text})
	if err != nil {
		stats.MessageErrored++
		logging.Error("Failed to rebuild message embedding", "id", job.ID, "error", err)
		return nil
	}
	if len(vectors) == 0 {
		stats.MessageErrored++
		logging.Error("Embedder returned no vectors for message", "id", job.ID)
		return nil
	}

	payload, err := json.Marshal(vectors[0])
	if err != nil {
		stats.MessageErrored++
		logging.Error("Failed to marshal message embedding", "id", job.ID, "error", err)
		return nil
	}

	stats.MessageUpdated++
	if dryRun {
		return nil
	}

	if _, err := conn.ExecContext(ctx, `
		UPDATE messages
		SET embedding = ?, updated_at = (strftime('%s', 'now') * 1000)
		WHERE id = ?
	`, string(payload), job.ID); err != nil {
		stats.MessageErrored++
		logging.Error("Failed to persist message embedding", "id", job.ID, "error", err)
	}
	return nil
}

func loadMessageEmbeddingRows(ctx context.Context, conn *sql.DB, allRows bool) ([]messageEmbeddingRow, error) {
	query := `
		SELECT id, parts
		FROM messages
		WHERE role IN ('user', 'assistant')
	`
	if !allRows {
		query += " AND embedding IS NOT NULL AND trim(embedding) != ''"
	}
	query += " ORDER BY created_at ASC, rowid ASC"

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query messages: %w", err)
	}
	defer rows.Close()

	items := make([]messageEmbeddingRow, 0, 256)
	for rows.Next() {
		var item messageEmbeddingRow
		if err := rows.Scan(&item.ID, &item.Parts); err != nil {
			return nil, fmt.Errorf("failed to scan message row: %w", err)
		}
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate messages: %w", err)
	}
	return items, nil
}

func rebuildKnowledgeEmbeddings(ctx context.Context, conn *sql.DB, embedder embedding.Embedder, dryRun bool, clearEmpty bool, allRows bool, batchSize int, stats *rebuildEmbeddingsStats) error {
	items, err := loadKnowledgeEmbeddingRows(ctx, conn, allRows)
	if err != nil {
		return err
	}
	if batchSize < 1 {
		batchSize = 1
	}

	pending := make([]knowledgeEmbeddingJob, 0, batchSize)

	for _, item := range items {
		id := item.ID
		content := item.Content

		stats.KnowledgeScanned++
		text := strings.TrimSpace(content)
		if text == "" {
			if clearEmpty {
				stats.KnowledgeCleared++
				if !dryRun {
					if _, err := conn.ExecContext(ctx, `
						UPDATE knowledge_tree
						SET embedding = NULL, updated_at = (strftime('%s', 'now') * 1000)
						WHERE id = ?
					`, id); err != nil {
						stats.KnowledgeErrored++
						logging.Error("Failed to clear knowledge embedding", "id", id, "error", err)
					}
				}
			} else {
				stats.KnowledgeSkipped++
			}
			continue
		}

		pending = append(pending, knowledgeEmbeddingJob{ID: id, Text: text})
		if len(pending) < batchSize {
			continue
		}
		if err := persistKnowledgeEmbeddingBatch(ctx, conn, embedder, pending, dryRun, stats); err != nil {
			return err
		}
		pending = pending[:0]
	}

	if len(pending) > 0 {
		if err := persistKnowledgeEmbeddingBatch(ctx, conn, embedder, pending, dryRun, stats); err != nil {
			return err
		}
	}

	return nil
}

func persistKnowledgeEmbeddingBatch(ctx context.Context, conn *sql.DB, embedder embedding.Embedder, jobs []knowledgeEmbeddingJob, dryRun bool, stats *rebuildEmbeddingsStats) error {
	texts := make([]string, 0, len(jobs))
	for _, job := range jobs {
		texts = append(texts, job.Text)
	}

	vectors, err := embedder.Embed(ctx, texts)
	if err != nil || len(vectors) != len(jobs) {
		for _, job := range jobs {
			if err := persistSingleKnowledgeEmbedding(ctx, conn, embedder, job, dryRun, stats); err != nil {
				return err
			}
		}
		return nil
	}

	for index, job := range jobs {
		payload, err := json.Marshal(vectors[index])
		if err != nil {
			stats.KnowledgeErrored++
			logging.Error("Failed to marshal knowledge embedding", "id", job.ID, "error", err)
			continue
		}

		stats.KnowledgeUpdated++
		if dryRun {
			continue
		}

		if _, err := conn.ExecContext(ctx, `
			UPDATE knowledge_tree
			SET embedding = ?, updated_at = (strftime('%s', 'now') * 1000)
			WHERE id = ?
		`, string(payload), job.ID); err != nil {
			stats.KnowledgeErrored++
			logging.Error("Failed to persist knowledge embedding", "id", job.ID, "error", err)
		}
	}

	return nil
}

func persistSingleKnowledgeEmbedding(ctx context.Context, conn *sql.DB, embedder embedding.Embedder, job knowledgeEmbeddingJob, dryRun bool, stats *rebuildEmbeddingsStats) error {
	vectors, err := embedder.Embed(ctx, []string{job.Text})
	if err != nil {
		stats.KnowledgeErrored++
		logging.Error("Failed to rebuild knowledge embedding", "id", job.ID, "error", err)
		return nil
	}
	if len(vectors) == 0 {
		stats.KnowledgeErrored++
		logging.Error("Embedder returned no vectors for knowledge", "id", job.ID)
		return nil
	}

	payload, err := json.Marshal(vectors[0])
	if err != nil {
		stats.KnowledgeErrored++
		logging.Error("Failed to marshal knowledge embedding", "id", job.ID, "error", err)
		return nil
	}

	stats.KnowledgeUpdated++
	if dryRun {
		return nil
	}

	if _, err := conn.ExecContext(ctx, `
		UPDATE knowledge_tree
		SET embedding = ?, updated_at = (strftime('%s', 'now') * 1000)
		WHERE id = ?
	`, string(payload), job.ID); err != nil {
		stats.KnowledgeErrored++
		logging.Error("Failed to persist knowledge embedding", "id", job.ID, "error", err)
	}
	return nil
}

func loadKnowledgeEmbeddingRows(ctx context.Context, conn *sql.DB, allRows bool) ([]knowledgeEmbeddingRow, error) {
	query := `
		SELECT id, content
		FROM knowledge_tree
	`
	if !allRows {
		query += " WHERE embedding IS NOT NULL AND trim(embedding) != ''"
	}
	query += " ORDER BY created_at ASC, rowid ASC"

	rows, err := conn.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query knowledge tree: %w", err)
	}
	defer rows.Close()

	items := make([]knowledgeEmbeddingRow, 0, 128)
	for rows.Next() {
		var item knowledgeEmbeddingRow
		if err := rows.Scan(&item.ID, &item.Content); err != nil {
			return nil, fmt.Errorf("failed to scan knowledge row: %w", err)
		}
		items = append(items, item)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate knowledge rows: %w", err)
	}
	return items, nil
}

func init() {
	rootCmd.AddCommand(rebuildEmbeddingsCmd)
	rebuildEmbeddingsCmd.Flags().Bool("debug", false, "Enable debug logging")
	rebuildEmbeddingsCmd.Flags().String("cwd", "", "Working directory")
	rebuildEmbeddingsCmd.Flags().Bool("dry-run", true, "Run without writing changes")
	rebuildEmbeddingsCmd.Flags().Bool("messages", true, "Rebuild message embeddings")
	rebuildEmbeddingsCmd.Flags().Bool("knowledge", true, "Rebuild knowledge embeddings")
	rebuildEmbeddingsCmd.Flags().Bool("clear-empty", false, "Clear stored embeddings for empty content")
	rebuildEmbeddingsCmd.Flags().Bool("all", false, "Process all eligible rows instead of only rows that already have embeddings")
	rebuildEmbeddingsCmd.Flags().Int("batch-size", 16, "Number of rows to embed per batch")
}
