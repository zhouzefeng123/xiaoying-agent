package memory

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/auramate/auramate/internal/config"
	"github.com/auramate/auramate/internal/db"
	"github.com/auramate/auramate/internal/dbtest"
)

func TestMemoryOwnershipIsolation(t *testing.T) {
	conn := dbtest.OpenMySQL(t)
	service := NewService(db.New(conn), conn, nil)

	ctxA := config.ContextWithUserEmail(context.Background(), "alice@qq.com")
	ctxB := config.ContextWithUserEmail(context.Background(), "bob@qq.com")

	memoryA, err := service.Add(ctxA, "fact", "Alice private memory", "user")
	if err != nil {
		t.Fatalf("add Alice memory: %v", err)
	}
	memoryB, err := service.Add(ctxB, "fact", "Bob private memory", "user")
	if err != nil {
		t.Fatalf("add Bob memory: %v", err)
	}

	if memoryA.OwnerEmail != "alice@qq.com" || memoryB.OwnerEmail != "bob@qq.com" {
		t.Fatalf("unexpected owners: A=%q B=%q", memoryA.OwnerEmail, memoryB.OwnerEmail)
	}

	listA, err := service.List(ctxA)
	if err != nil {
		t.Fatalf("list Alice memories: %v", err)
	}
	if len(listA) != 1 || listA[0].ID != memoryA.ID {
		t.Fatalf("Alice list = %+v, want only Alice memory", listA)
	}

	listB, err := service.List(ctxB)
	if err != nil {
		t.Fatalf("list Bob memories: %v", err)
	}
	if len(listB) != 1 || listB[0].ID != memoryB.ID {
		t.Fatalf("Bob list = %+v, want only Bob memory", listB)
	}

	if _, err := service.Get(ctxB, memoryA.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Bob reading Alice memory error = %v, want sql.ErrNoRows", err)
	}
	if _, err := service.Update(ctxB, memoryA.ID, "fact", "tampered", "user"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Bob updating Alice memory error = %v, want sql.ErrNoRows", err)
	}
	if err := service.Delete(ctxB, memoryA.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Bob deleting Alice memory error = %v, want sql.ErrNoRows", err)
	}
	if err := service.Link(ctxB, memoryA.ID, memoryB.ID, "related", 1); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("Bob linking Alice memory error = %v, want sql.ErrNoRows", err)
	}

	relatedA, err := service.GetRelated(ctxA, memoryA.ID)
	if err != nil {
		t.Fatalf("Alice related memories: %v", err)
	}
	if len(relatedA) != 0 {
		t.Fatalf("Alice should not see cross-owner relations: %+v", relatedA)
	}
}
