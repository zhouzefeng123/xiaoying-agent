package db

import (
	"database/sql"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/pressly/goose/v3"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/logging"
)

const (
	defaultMySQLMaxOpenConns = 50
	defaultMySQLMaxIdleConns = 25
)

// Connect opens the configured MySQL database, creates the schema if needed,
// applies the goose migrations and makes sure the auxiliary triggers exist.
//
// NOTE: this used to be a SQLite file connection (data.directory/database.db).
// The schema in internal/db/migrations is MySQL flavoured now, so an existing
// SQLite database has to be imported once with cmd/migrate_db.
func Connect() (*sql.DB, error) {
	cfg := config.Get()
	if cfg == nil {
		return nil, fmt.Errorf("config is not loaded")
	}

	dbCfg := cfg.Database

	if err := ensureDatabase(dbCfg); err != nil {
		return nil, fmt.Errorf("failed to ensure database exists: %w", err)
	}

	db, err := openWithRetry(dbCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to open database after retries: %w", err)
	}

	goose.SetBaseFS(FS)

	if err := goose.SetDialect("mysql"); err != nil {
		_ = db.Close()
		logging.Error("Failed to set dialect", "error", err)
		return nil, fmt.Errorf("failed to set dialect: %w", err)
	}

	if err := goose.Up(db, "migrations"); err != nil {
		_ = db.Close()
		logging.Error("Failed to apply migrations", "error", err)
		return nil, fmt.Errorf("failed to apply migrations: %w", err)
	}

	if err := ensureLegacySessionOwners(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to assign legacy session owners: %w", err)
	}

	if err := EnsureTriggers(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("failed to ensure triggers: %w", err)
	}

	// "extreme speed" requires handling multiple concurrent agents.
	db.SetMaxOpenConns(defaultMySQLMaxOpenConns)
	db.SetMaxIdleConns(defaultMySQLMaxIdleConns)
	db.SetConnMaxLifetime(time.Hour)

	logging.Info("Connected to MySQL",
		"host", dbCfg.Host,
		"port", dbCfg.Port,
		"database", dbCfg.Name,
		"user", dbCfg.User,
	)

	return db, nil
}

func ensureLegacySessionOwners(db *sql.DB) error {
	legacyOwner := config.LegacyOwnerEmail()
	if legacyOwner == "" {
		return nil
	}
	for _, table := range []string{"sessions", "knowledge_tree", "scheduled_tasks", "saved_news", "memories"} {
		if _, err := db.Exec("UPDATE "+table+" SET owner_email = ? WHERE owner_email = ''", legacyOwner); err != nil {
			return err
		}
	}
	return nil
}

// mysqlDSN builds the DSN for the configured MySQL server. When includeSchema
// is false the connection targets the server itself (used to CREATE DATABASE).
func mysqlDSN(dbCfg config.DatabaseConfig, includeSchema bool) string {
	mc := mysql.NewConfig()
	mc.User = dbCfg.User
	mc.Passwd = dbCfg.Password
	mc.Net = "tcp"
	mc.Addr = net.JoinHostPort(dbCfg.Host, strconv.Itoa(dbCfg.Port))
	mc.Timeout = 10 * time.Second
	mc.ParseTime = true
	mc.Loc = time.Local
	mc.Params = map[string]string{"charset": "utf8mb4"}
	if includeSchema {
		mc.DBName = dbCfg.Name
	}
	return mc.FormatDSN()
}

// ensureDatabase creates the target schema when it does not exist yet so a
// fresh machine only needs a running MySQL plus credentials.
func ensureDatabase(dbCfg config.DatabaseConfig) error {
	name := strings.TrimSpace(dbCfg.Name)
	if name == "" {
		return fmt.Errorf("database.name is not set")
	}

	admin, err := sql.Open("mysql", mysqlDSN(dbCfg, false))
	if err != nil {
		return err
	}
	defer admin.Close()

	// Only the identifier is interpolated and backticks are stripped first, so
	// the name cannot escape the identifier position.
	safeName := strings.ReplaceAll(name, "`", "")
	stmt := fmt.Sprintf(
		"CREATE DATABASE IF NOT EXISTS `%s` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci",
		safeName,
	)
	if _, err := admin.Exec(stmt); err != nil {
		return err
	}

	return nil
}

// openWithRetry attempts to open a pooled connection and verify it. MySQL is
// usually already up, but a cold container may need a couple of seconds.
func openWithRetry(dbCfg config.DatabaseConfig) (*sql.DB, error) {
	dsn := mysqlDSN(dbCfg, true)

	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		db, err := sql.Open("mysql", dsn)
		if err == nil {
			db.SetMaxOpenConns(defaultMySQLMaxOpenConns)
			db.SetMaxIdleConns(defaultMySQLMaxIdleConns)
			if err = db.Ping(); err == nil {
				return db, nil
			}
			_ = db.Close()
		}
		lastErr = err
		logging.Warn("MySQL connection attempt failed, retrying", "attempt", attempt, "error", err)
		time.Sleep(time.Duration(attempt) * time.Second)
	}

	return nil, lastErr
}

// EnsureTriggers creates the cross-table triggers that keep denormalised
// counters in sync. They are created here rather than in the goose migrations
// because sqlc cannot parse CREATE TRIGGER while reading the schema files.
// Test fixtures that apply the migrations themselves must call this too.
func EnsureTriggers(db *sql.DB) error {
	triggers := []struct {
		name string
		ddl  string
	}{
		{
			name: "update_session_message_count_on_insert",
			ddl: "CREATE TRIGGER update_session_message_count_on_insert AFTER INSERT ON messages " +
				"FOR EACH ROW UPDATE sessions SET message_count = message_count + 1 WHERE id = NEW.session_id",
		},
		{
			name: "update_session_message_count_on_delete",
			ddl: "CREATE TRIGGER update_session_message_count_on_delete AFTER DELETE ON messages " +
				"FOR EACH ROW UPDATE sessions SET message_count = message_count - 1 WHERE id = OLD.session_id",
		},
	}

	for _, trigger := range triggers {
		var existing int
		if err := db.QueryRow(
			"SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE TRIGGER_SCHEMA = DATABASE() AND TRIGGER_NAME = ?",
			trigger.name,
		).Scan(&existing); err != nil {
			return fmt.Errorf("failed to inspect trigger %s: %w", trigger.name, err)
		}
		if existing > 0 {
			continue
		}
		if _, err := db.Exec(trigger.ddl); err != nil {
			return fmt.Errorf("failed to create trigger %s: %w", trigger.name, err)
		}
		logging.Info("Created MySQL trigger", "trigger", trigger.name)
	}

	return nil
}
