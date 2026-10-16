package tools

import (
	"context"
	"time"

	"github.com/gofrs/flock"
)

// acquireLockWithRetry attempts to lock the file with retries
func acquireLockWithRetry(ctx context.Context, fileLock *flock.Flock, retries int, delay time.Duration) (bool, error) {
	if retries <= 0 {
		retries = 1
	}
	if delay <= 0 {
		delay = 50 * time.Millisecond
	}

	waitDelay := delay
	maxDelay := time.Second

	for i := 0; i < retries; i++ {
		// Check context cancellation
		if ctx.Err() != nil {
			return false, ctx.Err()
		}

		locked, err := fileLock.TryLock()
		if err != nil {
			return false, err
		}
		if locked {
			return true, nil
		}

		if i == retries-1 {
			break
		}

		// Wait before retry
		select {
		case <-ctx.Done():
			return false, ctx.Err()
		case <-time.After(waitDelay):
			// continue
		}

		if waitDelay < maxDelay {
			waitDelay *= 2
			if waitDelay > maxDelay {
				waitDelay = maxDelay
			}
		}
	}
	return false, nil
}
