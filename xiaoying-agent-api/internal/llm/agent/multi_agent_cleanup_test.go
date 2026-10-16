package agent

import (
	"testing"
	"time"
)

func TestCleanupKeepsRunningWorkerUntilDeadline(t *testing.T) {
	orch := NewOrchestrator(nil, nil)
	workerID := "worker-with-deadline"
	now := time.Now()
	deadline := now.Add(3 * time.Minute)

	orch.workers[workerID] = &worker{
		id:        workerID,
		role:      "tester",
		taskQueue: make(chan workerTask, 1),
	}
	orch.workerMeta[workerID] = &workerRuntime{
		Status:          "running",
		LastActivityAt:  now.Add(-3 * time.Minute),
		LastProgressAt:  now.Add(-3 * time.Minute),
		TimeoutDuration: 5 * time.Minute,
		DeadlineAt:      &deadline,
	}

	orch.collectAndCleanupWorkers()

	if _, ok := orch.workers[workerID]; !ok {
		t.Fatal("expected running worker to stay alive until its explicit deadline")
	}
}

func TestCleanupKeepsRunningWorkerWithFreshHeartbeat(t *testing.T) {
	orch := NewOrchestrator(nil, nil)
	workerID := "worker-with-heartbeat"
	now := time.Now()

	orch.workers[workerID] = &worker{
		id:        workerID,
		role:      "tester",
		taskQueue: make(chan workerTask, 1),
	}
	orch.workerMeta[workerID] = &workerRuntime{
		Status:          "running",
		LastActivityAt:  now.Add(-3 * time.Minute),
		LastProgressAt:  now.Add(-3 * time.Minute),
		LastHeartbeatAt: now.Add(-30 * time.Second),
		TimeoutDuration: 5 * time.Minute,
	}

	orch.collectAndCleanupWorkers()

	if _, ok := orch.workers[workerID]; !ok {
		t.Fatal("expected fresh heartbeat to prevent stale-progress cleanup")
	}
}
