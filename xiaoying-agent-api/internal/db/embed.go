package db

import "embed"

//go:embed migrations/*.sql migrations_ikts/*.sql
var FS embed.FS
