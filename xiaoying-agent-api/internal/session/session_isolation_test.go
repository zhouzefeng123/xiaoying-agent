package session

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/dbtest"
)

func TestSessionOwnershipIsolation(t *testing.T) {
	conn := dbtest.OpenMySQL(t)
	service := NewService(db.New(conn))

	ctxA := config.ContextWithUserEmail(context.Background(), "alice@qq.com")
	ctxB := config.ContextWithUserEmail(context.Background(), "bob@qq.com")

	sessionA, err := service.Create(ctxA, "Alice session")
	if err != nil {
		t.Fatalf("create Alice session: %v", err)
	}
	if sessionA.OwnerEmail != "alice@qq.com" {
		t.Fatalf("owner = %q, want alice@qq.com", sessionA.OwnerEmail)
	}

	sessionB, err := service.Create(ctxB, "Bob session")
	if err != nil {
		t.Fatalf("create Bob session: %v", err)
	}
	if sessionB.OwnerEmail != "bob@qq.com" {
		t.Fatalf("owner = %q, want bob@qq.com", sessionB.OwnerEmail)
	}

	listA, err := service.List(ctxA)
	if err != nil {
		t.Fatalf("list Alice sessions: %v", err)
	}
	if len(listA) != 1 || listA[0].ID != sessionA.ID {
		t.Fatalf("Alice list = %+v, want only Alice session", listA)
	}

	listB, err := service.List(ctxB)
	if err != nil {
		t.Fatalf("list Bob sessions: %v", err)
	}
	if len(listB) != 1 || listB[0].ID != sessionB.ID {
		t.Fatalf("Bob list = %+v, want only Bob session", listB)
	}

	if _, err := service.Get(ctxB, sessionA.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Bob reading Alice session error = %v, want sql.ErrNoRows", err)
	}
	if _, err := service.Save(ctxB, sessionA); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Bob saving Alice session error = %v, want sql.ErrNoRows", err)
	}
	if err := service.Delete(ctxB, sessionA.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Bob deleting Alice session error = %v, want sql.ErrNoRows", err)
	}

	child, err := service.CreateTaskSession(ctxA, uuid.New().String(), sessionA.ID, "Alice child")
	if err != nil {
		t.Fatalf("create Alice child session: %v", err)
	}
	if child.OwnerEmail != "alice@qq.com" {
		t.Fatalf("child owner = %q, want alice@qq.com", child.OwnerEmail)
	}
	if _, err := service.CreateTaskSession(ctxB, uuid.New().String(), sessionA.ID, "Bob child"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Bob creating child under Alice session error = %v, want sql.ErrNoRows", err)
	}
}
