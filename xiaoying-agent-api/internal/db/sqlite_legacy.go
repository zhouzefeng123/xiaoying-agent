package db

// The backend runs on MySQL now, but the SQLite driver stays linked in so the
// remaining in-memory unit test fixtures and the one-off cmd/migrate_db import
// tool can still open SQLite files.
import (
	_ "github.com/ncruces/go-sqlite3/driver"
	_ "github.com/ncruces/go-sqlite3/embed"
)
