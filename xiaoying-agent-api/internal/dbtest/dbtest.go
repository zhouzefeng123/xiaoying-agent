// Package dbtest hands unit tests a real MySQL schema with the production
// migrations applied.
//
// Tests that only exercise portable SQL can keep using the in-memory SQLite
// fixtures, but anything that depends on a MySQL feature (the ngram FULLTEXT
// index backing knowledge search, for example) has to run against MySQL.
//
// Connection settings are taken from AURAMATE_TEST_DB_* environment variables,
// falling back to the `database` block of the nearest .auramate.json. The
// schema name always defaults to auramate_test so a test run can never touch
// the live data. When the server is unreachable the calling test is skipped.
package dbtest

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/pressly/goose/v3"

	"github.com/auramate/auramate/internal/db"
)

const defaultTestSchema = "auramate_test"

type settings struct {
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

// OpenMySQL returns a pooled connection to the test schema with every table
// empty and the migrations applied. The caller must not close it; cleanup is
// registered with t.Cleanup.
func OpenMySQL(t *testing.T) *sql.DB {
	t.Helper()

	cfg := resolveSettings(t)

	if err := ensureSchema(t, cfg); err != nil {
		t.Skipf("MySQL test database unavailable (%v); set AURAMATE_TEST_DB_* to run this test", err)
	}

	conn, err := sql.Open("mysql", dsn(cfg))
	if err != nil {
		t.Fatalf("open mysql: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	goose.SetBaseFS(db.FS)
	if err := goose.SetDialect("mysql"); err != nil {
		t.Fatalf("set goose dialect: %v", err)
	}
	if err := goose.UpContext(context.Background(), conn, "migrations"); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}

	// Same step db.Connect performs: the message counter triggers are created
	// outside the migrations.
	if err := db.EnsureTriggers(conn); err != nil {
		t.Fatalf("ensure triggers: %v", err)
	}

	truncateAll(t, conn, cfg)

	return conn
}

func resolveSettings(t *testing.T) settings {
	t.Helper()

	cfg := settings{
		Host: "127.0.0.1",
		Port: 3306,
		User: "root",
		Name: defaultTestSchema,
	}

	// The project config sits a few levels above the package under test.
	if raw := findConfigFile(); raw != nil {
		var parsed struct {
			Database settings `json:"database"`
		}
		if err := json.Unmarshal(raw, &parsed); err == nil {
			if parsed.Database.Host != "" {
				cfg.Host = parsed.Database.Host
			}
			if parsed.Database.Port != 0 {
				cfg.Port = parsed.Database.Port
			}
			if parsed.Database.User != "" {
				cfg.User = parsed.Database.User
			}
			cfg.Password = parsed.Database.Password
		}
	}

	if v := strings.TrimSpace(os.Getenv("AURAMATE_TEST_DB_HOST")); v != "" {
		cfg.Host = v
	}
	if v := strings.TrimSpace(os.Getenv("AURAMATE_TEST_DB_PORT")); v != "" {
		if port, err := strconv.Atoi(v); err == nil && port > 0 {
			cfg.Port = port
		}
	}
	if v := strings.TrimSpace(os.Getenv("AURAMATE_TEST_DB_USER")); v != "" {
		cfg.User = v
	}
	if v, ok := os.LookupEnv("AURAMATE_TEST_DB_PASSWORD"); ok && v != "" {
		cfg.Password = v
	}
	cfg.Name = defaultTestSchema
	if v := strings.TrimSpace(os.Getenv("AURAMATE_TEST_DB_NAME")); v != "" {
		cfg.Name = v
	}

	if cfg.Name == "" {
		cfg.Name = defaultTestSchema
	}

	return cfg
}

func findConfigFile() []byte {
	dir, err := os.Getwd()
	if err != nil {
		return nil
	}

	for depth := 0; depth < 4; depth++ {
		candidate := filepath.Join(dir, ".auramate.json")
		if data, err := os.ReadFile(candidate); err == nil {
			return data
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}

	return nil
}

func dsn(cfg settings) string {
	mc := mysql.NewConfig()
	mc.User = cfg.User
	mc.Passwd = cfg.Password
	mc.Net = "tcp"
	mc.Addr = net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	mc.DBName = cfg.Name
	mc.Timeout = 5 * time.Second
	mc.ParseTime = true
	mc.Params = map[string]string{"charset": "utf8mb4"}
	return mc.FormatDSN()
}

func ensureSchema(t *testing.T, cfg settings) error {
	t.Helper()

	adminCfg := cfg
	adminCfg.Name = ""
	admin, err := sql.Open("mysql", dsn(adminCfg))
	if err != nil {
		return err
	}
	defer admin.Close()

	if err := admin.Ping(); err != nil {
		return err
	}

	safeName := strings.ReplaceAll(cfg.Name, "`", "")
	_, err = admin.Exec(fmt.Sprintf(
		"CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci",
		safeName,
	))
	return err
}

// truncateAll empties every table (except goose's bookkeeping table) so each
// test starts from a clean schema.
func truncateAll(t *testing.T, conn *sql.DB, cfg settings) {
	t.Helper()

	rows, err := conn.Query(
		"SELECT table_name FROM information_schema.tables WHERE table_schema = ? AND table_name <> 'goose_db_version'",
		cfg.Name,
	)
	if err != nil {
		t.Fatalf("list test tables: %v", err)
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table name: %v", err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table names: %v", err)
	}
	if len(tables) == 0 {
		return
	}

	if _, err := conn.Exec("SET FOREIGN_KEY_CHECKS = 0"); err != nil {
		t.Fatalf("disable foreign key checks: %v", err)
	}
	for _, table := range tables {
		if _, err := conn.Exec("TRUNCATE TABLE `" + strings.ReplaceAll(table, "`", "") + "`"); err != nil {
			t.Fatalf("truncate %s: %v", table, err)
		}
	}
	if _, err := conn.Exec("SET FOREIGN_KEY_CHECKS = 1"); err != nil {
		t.Fatalf("re-enable foreign key checks: %v", err)
	}
}
