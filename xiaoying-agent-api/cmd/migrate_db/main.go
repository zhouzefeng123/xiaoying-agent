// Command migrate_db copies a legacy SQLite database (data.directory/database.db)
// into the MySQL schema the backend now runs on.
//
//	go run ./cmd/migrate_db                 # uses database/database.db
//	go run ./cmd/migrate_db -src other.db   # explicit source file
//
// The copy is idempotent: rows that already exist in MySQL are skipped
// (INSERT IGNORE), so the command can be re-run safely.
package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"os"
	"strings"

	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/logging"
)

// Copied in foreign-key order so InnoDB never sees a dangling reference.
var copyOrder = []string{
	"sessions",
	"files",
	"messages",
	"memories",
	"knowledge_tree",
	"knowledge_edges",
	"cognitive_state",
	"scheduled_tasks",
	"saved_news",
}

func main() {
	src := flag.String("src", `database\database.db`, "path to the legacy SQLite database file")
	flag.Parse()

	cwd, err := os.Getwd()
	if err != nil {
		fail("failed to resolve working directory: %v", err)
	}

	if _, err := config.Load(cwd, false); err != nil {
		fail("failed to load config: %v", err)
	}
	logging.Init("", false)

	if _, err := os.Stat(*src); err != nil {
		fail("source SQLite database not found at %s: %v", *src, err)
	}

	mysqlDB, err := db.Connect()
	if err != nil {
		fail("failed to connect to MySQL: %v", err)
	}
	defer mysqlDB.Close()

	sqliteDB, err := sql.Open("sqlite3", *src)
	if err != nil {
		fail("failed to open SQLite database: %v", err)
	}
	defer sqliteDB.Close()

	ctx := context.Background()

	total := 0
	for _, table := range copyOrder {
		copied, err := copyTable(ctx, sqliteDB, mysqlDB, table)
		if err != nil {
			fail("failed to copy %s: %v", table, err)
		}
		total += copied
		fmt.Printf("%-18s %6d rows\n", table, copied)
	}

	// The messages trigger only fires for new rows, so recompute the
	// denormalised counter from the imported data instead of trusting the
	// value that came out of SQLite.
	if _, err := mysqlDB.ExecContext(ctx, `
		UPDATE sessions s
		SET message_count = (SELECT COUNT(*) FROM messages m WHERE m.session_id = s.id)`); err != nil {
		fail("failed to recompute sessions.message_count: %v", err)
	}

	fmt.Printf("\nImported %d rows into MySQL database %q.\n", total, config.Get().Database.Name)
}

// copyTable copies every column that exists on both sides of the move.
func copyTable(ctx context.Context, sqliteDB, mysqlDB *sql.DB, table string) (int, error) {
	sourceCols, err := sqliteColumns(ctx, sqliteDB, table)
	if err != nil {
		return 0, err
	}
	if len(sourceCols) == 0 {
		fmt.Printf("%-18s (source table missing, skipped)\n", table)
		return 0, nil
	}

	targetCols, err := mysqlColumns(ctx, mysqlDB, table)
	if err != nil {
		return 0, err
	}
	if len(targetCols) == 0 {
		return 0, fmt.Errorf("target table %s does not exist", table)
	}

	targetSet := make(map[string]struct{}, len(targetCols))
	for _, col := range targetCols {
		targetSet[strings.ToLower(col)] = struct{}{}
	}

	cols := make([]string, 0, len(sourceCols))
	for _, col := range sourceCols {
		if _, ok := targetSet[strings.ToLower(col)]; ok {
			cols = append(cols, col)
		}
	}
	if len(cols) == 0 {
		return 0, fmt.Errorf("no shared columns between SQLite and MySQL for %s", table)
	}

	quoted := make([]string, len(cols))
	placeholders := make([]string, len(cols))
	for i, col := range cols {
		quoted[i] = "`" + strings.ReplaceAll(col, "`", "") + "`"
		placeholders[i] = "?"
	}

	selectSQL := fmt.Sprintf("SELECT %s FROM %s", strings.Join(quoted, ", "), "`"+table+"`")
	rows, err := sqliteDB.QueryContext(ctx, selectSQL)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	insertSQL := fmt.Sprintf(
		"INSERT IGNORE INTO `%s` (%s) VALUES (%s)",
		table,
		strings.Join(quoted, ", "),
		strings.Join(placeholders, ", "),
	)

	tx, err := mysqlDB.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, insertSQL)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()

	copied := 0
	for rows.Next() {
		values := make([]any, len(cols))
		pointers := make([]any, len(cols))
		for i := range values {
			pointers[i] = &values[i]
		}

		if err := rows.Scan(pointers...); err != nil {
			return copied, err
		}

		result, err := stmt.ExecContext(ctx, values...)
		if err != nil {
			return copied, err
		}
		if affected, err := result.RowsAffected(); err == nil {
			copied += int(affected)
		}
	}
	if err := rows.Err(); err != nil {
		return copied, err
	}

	if err := tx.Commit(); err != nil {
		return copied, err
	}

	return copied, nil
}

func sqliteColumns(ctx context.Context, sqliteDB *sql.DB, table string) ([]string, error) {
	rows, err := sqliteDB.QueryContext(ctx, "SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

func mysqlColumns(ctx context.Context, mysqlDB *sql.DB, table string) ([]string, error) {
	rows, err := mysqlDB.QueryContext(ctx,
		"SELECT column_name FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ?",
		table,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var cols []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		cols = append(cols, name)
	}
	return cols, rows.Err()
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "migrate_db: "+format+"\n", args...)
	os.Exit(1)
}
