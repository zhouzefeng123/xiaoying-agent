package history

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/pubsub"
	"github.com/google/uuid"
)

const (
	InitialVersion = "initial"
)

type File struct {
	ID        string
	SessionID string
	Path      string
	Content   string
	Version   string
	CreatedAt int64
	UpdatedAt int64
}

type Service interface {
	pubsub.Suscriber[File]
	Create(ctx context.Context, sessionID, path, content string) (File, error)
	CreateVersion(ctx context.Context, sessionID, path, content string) (File, error)
	Get(ctx context.Context, id string) (File, error)
	GetByPathAndSession(ctx context.Context, path, sessionID string) (File, error)
	ListBySession(ctx context.Context, sessionID string) ([]File, error)
	ListLatestSessionFiles(ctx context.Context, sessionID string) ([]File, error)
	ListAllLatestFiles(ctx context.Context) ([]File, error)
	ListAllBaselineFiles(ctx context.Context) ([]File, error)
	ListVersionsByPath(ctx context.Context, path string) ([]File, error)
	GetByPathAndVersion(ctx context.Context, path, version string) (File, error)
	Update(ctx context.Context, file File) (File, error)
	Delete(ctx context.Context, id string) error
	DeleteSessionFiles(ctx context.Context, sessionID string) error
}

type service struct {
	*pubsub.Broker[File]
	db *sql.DB
	q  *db.Queries
}

func NewService(q *db.Queries, db *sql.DB) Service {
	return &service{
		Broker: pubsub.NewBroker[File](),
		q:      q,
		db:     db,
	}
}

func (s *service) Create(ctx context.Context, sessionID, path, content string) (File, error) {
	return s.createWithVersion(ctx, sessionID, path, content, InitialVersion)
}

func (s *service) CreateVersion(ctx context.Context, sessionID, path, content string) (File, error) {
	// Get the latest version for this path
	files, err := s.q.ListFilesByPath(ctx, path)
	if err != nil {
		return File{}, err
	}

	if len(files) == 0 {
		// No previous versions, create initial
		return s.Create(ctx, sessionID, path, content)
	}

	// Get the latest version
	latestFile := files[0] // Files are ordered by created_at DESC
	latestVersion := latestFile.Version

	// Generate the next version
	var nextVersion string
	if latestVersion == InitialVersion {
		nextVersion = "v1"
	} else if strings.HasPrefix(latestVersion, "v") {
		versionNum, err := strconv.Atoi(latestVersion[1:])
		if err != nil {
			// If we can't parse the version, just use a timestamp-based version
			nextVersion = fmt.Sprintf("v%d", latestFile.CreatedAt)
		} else {
			nextVersion = fmt.Sprintf("v%d", versionNum+1)
		}
	} else {
		// If the version format is unexpected, use a timestamp-based version
		nextVersion = fmt.Sprintf("v%d", latestFile.CreatedAt)
	}

	return s.createWithVersion(ctx, sessionID, path, content, nextVersion)
}

func (s *service) createWithVersion(ctx context.Context, sessionID, path, content, version string) (File, error) {
	// Maximum number of retries for transaction conflicts
	const maxRetries = 3
	var file File
	var err error

	// Retry loop for transaction conflicts
	for attempt := range maxRetries {
		// Start a transaction
		tx, txErr := s.db.Begin()
		if txErr != nil {
			return File{}, fmt.Errorf("failed to begin transaction: %w", txErr)
		}

		// Create a new queries instance with the transaction
		qtx := s.q.WithTx(tx)

		// Try to create the file within the transaction
		dbFile, txErr := qtx.CreateFile(ctx, db.CreateFileParams{
			ID:        uuid.New().String(),
			SessionID: sessionID,
			Path:      path,
			Content:   content,
			Version:   version,
		})
		if txErr != nil {
			// Rollback the transaction
			tx.Rollback()

			// Check if this is a uniqueness constraint violation
			if strings.Contains(txErr.Error(), "UNIQUE constraint failed") {
				if attempt < maxRetries-1 {
					// If we have retries left, generate a new version and try again
					if strings.HasPrefix(version, "v") {
						versionNum, parseErr := strconv.Atoi(version[1:])
						if parseErr == nil {
							version = fmt.Sprintf("v%d", versionNum+1)
							continue
						}
					}
					// If we can't parse the version, use a timestamp-based version
					version = fmt.Sprintf("v%d", time.Now().Unix())
					continue
				}
			}
			return File{}, txErr
		}

		// Commit the transaction
		if txErr = tx.Commit(); txErr != nil {
			return File{}, fmt.Errorf("failed to commit transaction: %w", txErr)
		}

		file = s.fromDBItem(dbFile)
		s.Publish(pubsub.CreatedEvent, file)
		return file, nil
	}

	return file, err
}

func (s *service) Get(ctx context.Context, id string) (File, error) {
	dbFile, err := s.q.GetFile(ctx, id)
	if err != nil {
		return File{}, err
	}
	return s.fromDBItem(dbFile), nil
}

func (s *service) GetByPathAndSession(ctx context.Context, path, sessionID string) (File, error) {
	dbFile, err := s.q.GetFileByPathAndSession(ctx, db.GetFileByPathAndSessionParams{
		Path:      path,
		SessionID: sessionID,
	})
	if err != nil {
		return File{}, err
	}
	return s.fromDBItem(dbFile), nil
}

func (s *service) ListBySession(ctx context.Context, sessionID string) ([]File, error) {
	dbFiles, err := s.q.ListFilesBySession(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	files := make([]File, len(dbFiles))
	for i, dbFile := range dbFiles {
		files[i] = s.fromDBItem(dbFile)
	}
	return files, nil
}

func (s *service) ListAllLatestFiles(ctx context.Context) ([]File, error) {
	query := `
		SELECT f.id, f.session_id, f.path, f.content, f.version, f.created_at, f.updated_at
		FROM files f
		INNER JOIN (
			SELECT path, MAX(created_at) as max_date
			FROM files
			GROUP BY path
		) latest ON f.path = latest.path AND f.created_at = latest.max_date
	`
	return s.queryFiles(ctx, query)
}

// ListAllBaselineFiles returns the initial (oldest) version of each file path.
// This represents the original state before any AI edits in the current session.
func (s *service) ListAllBaselineFiles(ctx context.Context) ([]File, error) {
	query := `
		SELECT f.id, f.session_id, f.path, f.content, f.version, f.created_at, f.updated_at
		FROM files f
		INNER JOIN (
			SELECT path, MIN(created_at) as min_date
			FROM files
			GROUP BY path
		) baseline ON f.path = baseline.path AND f.created_at = baseline.min_date
	`
	return s.queryFiles(ctx, query)
}

func (s *service) queryFiles(ctx context.Context, query string) ([]File, error) {
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []File
	for rows.Next() {
		var f File
		if err := rows.Scan(
			&f.ID,
			&f.SessionID,
			&f.Path,
			&f.Content,
			&f.Version,
			&f.CreatedAt,
			&f.UpdatedAt,
		); err != nil {
			return nil, err
		}
		files = append(files, f)
	}
	return files, nil
}

func (s *service) ListLatestSessionFiles(ctx context.Context, sessionID string) ([]File, error) {
	dbFiles, err := s.q.ListLatestSessionFiles(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	files := make([]File, len(dbFiles))
	for i, dbFile := range dbFiles {
		files[i] = s.fromDBItem(dbFile)
	}
	return files, nil
}

func (s *service) ListVersionsByPath(ctx context.Context, path string) ([]File, error) {
	dbFiles, err := s.q.ListFilesByPath(ctx, path)
	if err != nil {
		return nil, err
	}
	files := make([]File, len(dbFiles))
	for i, dbFile := range dbFiles {
		files[i] = s.fromDBItem(dbFile)
	}
	return files, nil
}

func (s *service) GetByPathAndVersion(ctx context.Context, path, version string) (File, error) {
	files, err := s.ListVersionsByPath(ctx, path)
	if err != nil {
		return File{}, err
	}
	for _, f := range files {
		if f.Version == version {
			return f, nil
		}
	}
	return File{}, fmt.Errorf("version %s not found for path %s", version, path)
}

func (s *service) Update(ctx context.Context, file File) (File, error) {
	dbFile, err := s.q.UpdateFile(ctx, db.UpdateFileParams{
		ID:      file.ID,
		Content: file.Content,
		Version: file.Version,
	})
	if err != nil {
		return File{}, err
	}
	updatedFile := s.fromDBItem(dbFile)
	s.Publish(pubsub.UpdatedEvent, updatedFile)
	return updatedFile, nil
}

func (s *service) Delete(ctx context.Context, id string) error {
	file, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	err = s.q.DeleteFile(ctx, id)
	if err != nil {
		return err
	}
	s.Publish(pubsub.DeletedEvent, file)
	return nil
}

func (s *service) DeleteSessionFiles(ctx context.Context, sessionID string) error {
	files, err := s.ListBySession(ctx, sessionID)
	if err != nil {
		return err
	}
	for _, file := range files {
		err = s.Delete(ctx, file.ID)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *service) fromDBItem(item db.File) File {
	return File{
		ID:        item.ID,
		SessionID: item.SessionID,
		Path:      item.Path,
		Content:   item.Content,
		Version:   item.Version,
		CreatedAt: item.CreatedAt,
		UpdatedAt: item.UpdatedAt,
	}
}
