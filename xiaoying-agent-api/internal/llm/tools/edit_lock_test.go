package tools

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofrs/flock"
)

func TestBuildEditLockFilePathIsStableAndShort(t *testing.T) {
	t.Parallel()

	filePath := filepath.Join(t.TempDir(), "nested", "Example File.txt")
	lockPath1, err := buildEditLockFilePath(filePath)
	if err != nil {
		t.Fatalf("buildEditLockFilePath returned error: %v", err)
	}

	lockPath2, err := buildEditLockFilePath(filePath)
	if err != nil {
		t.Fatalf("buildEditLockFilePath returned error on second call: %v", err)
	}

	if lockPath1 != lockPath2 {
		t.Fatalf("expected stable lock path, got %q and %q", lockPath1, lockPath2)
	}

	lockName := filepath.Base(lockPath1)
	if len(lockName) > 64 {
		t.Fatalf("expected short lock filename, got %q (%d chars)", lockName, len(lockName))
	}
	if filepath.Ext(lockPath1) != ".lock" {
		t.Fatalf("expected .lock suffix, got %q", lockPath1)
	}
}

func TestGetEditFileMutexReturnsSharedMutex(t *testing.T) {
	t.Parallel()

	filePath := filepath.Join(t.TempDir(), "shared.txt")
	mu1 := getEditFileMutex(filePath)
	mu2 := getEditFileMutex(filePath)

	if mu1 != mu2 {
		t.Fatal("expected the same mutex instance for identical file path")
	}
}

func TestAcquireLockWithRetryWaitsForRelease(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "edit.lock")
	firstLock := flock.New(lockPath)
	locked, err := firstLock.TryLock()
	if err != nil {
		t.Fatalf("failed to acquire first lock: %v", err)
	}
	if !locked {
		t.Fatal("expected first lock acquisition to succeed")
	}
	defer firstLock.Unlock()

	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = firstLock.Unlock()
	}()

	secondLock := flock.New(lockPath)
	start := time.Now()
	locked, err = acquireLockWithRetry(context.Background(), secondLock, 8, 50*time.Millisecond)
	if err != nil {
		t.Fatalf("acquireLockWithRetry returned error: %v", err)
	}
	if !locked {
		t.Fatal("expected second lock acquisition to succeed after release")
	}
	defer secondLock.Unlock()

	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("expected retry wait before acquiring lock, elapsed=%s", elapsed)
	}
}

func TestAcquireLockWithRetryHonorsContextCancellation(t *testing.T) {
	lockPath := filepath.Join(t.TempDir(), "edit.lock")
	firstLock := flock.New(lockPath)
	locked, err := firstLock.TryLock()
	if err != nil {
		t.Fatalf("failed to acquire first lock: %v", err)
	}
	if !locked {
		t.Fatal("expected first lock acquisition to succeed")
	}
	defer firstLock.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()

	secondLock := flock.New(lockPath)
	locked, err = acquireLockWithRetry(ctx, secondLock, 10, 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected context cancellation error")
	}
	if locked {
		t.Fatal("did not expect lock acquisition when context is cancelled")
	}
}

func TestRunWithEditFileLockSerializesSameProcessCallbacks(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "serialized.txt")
	firstEntered := make(chan struct{})
	releaseFirst := make(chan struct{})
	firstDone := make(chan error, 1)
	secondEntered := make(chan struct{})
	secondDone := make(chan error, 1)

	go func() {
		firstDone <- runWithEditFileLock(context.Background(), filePath, func() error {
			close(firstEntered)
			<-releaseFirst
			return nil
		})
	}()

	<-firstEntered

	go func() {
		secondDone <- runWithEditFileLock(context.Background(), filePath, func() error {
			close(secondEntered)
			return nil
		})
	}()

	select {
	case <-secondEntered:
		t.Fatal("second callback entered before first callback released the lock")
	case <-time.After(120 * time.Millisecond):
	}

	close(releaseFirst)

	if err := <-firstDone; err != nil {
		t.Fatalf("first locked callback returned error: %v", err)
	}

	select {
	case err := <-secondDone:
		if err != nil {
			t.Fatalf("second locked callback returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for second callback to acquire the lock")
	}
}

func TestRunWithEditFileLockHighContention(t *testing.T) {
	filePath := filepath.Join(t.TempDir(), "contention.txt")
	const workers = 24
	const holdDuration = 20 * time.Millisecond

	start := make(chan struct{})
	errCh := make(chan error, workers)
	var currentInside atomic.Int32
	var maxInside atomic.Int32
	var completed atomic.Int32
	var waitTotal atomic.Int64
	var maxWait atomic.Int64
	var wg sync.WaitGroup

	updateMax := func(target *atomic.Int64, candidate int64) {
		for {
			current := target.Load()
			if candidate <= current {
				return
			}
			if target.CompareAndSwap(current, candidate) {
				return
			}
		}
	}

	updateMaxInt32 := func(target *atomic.Int32, candidate int32) {
		for {
			current := target.Load()
			if candidate <= current {
				return
			}
			if target.CompareAndSwap(current, candidate) {
				return
			}
		}
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start

			attemptStart := time.Now()
			errCh <- runWithEditFileLock(context.Background(), filePath, func() error {
				waitMs := time.Since(attemptStart).Milliseconds()
				waitTotal.Add(waitMs)
				updateMax(&maxWait, waitMs)

				inside := currentInside.Add(1)
				updateMaxInt32(&maxInside, inside)
				defer currentInside.Add(-1)

				time.Sleep(holdDuration)
				completed.Add(1)
				return nil
			})
		}()
	}

	close(start)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("runWithEditFileLock returned error under contention: %v", err)
		}
	}

	if got := completed.Load(); got != workers {
		t.Fatalf("expected %d completed callbacks, got %d", workers, got)
	}
	if got := maxInside.Load(); got != 1 {
		t.Fatalf("expected max concurrent callbacks inside lock to be 1, got %d", got)
	}
	if got := maxWait.Load(); got <= 0 {
		t.Fatalf("expected at least one waiter to observe lock delay, got max wait %dms", got)
	}
	if got := waitTotal.Load(); got <= 0 {
		t.Fatalf("expected aggregate wait time to be positive, got %dms", got)
	}
}
