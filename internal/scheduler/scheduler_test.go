package scheduler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

type testJob struct {
	id       string
	requires []string
	delay    time.Duration
	err      error
	onRun    func(string)
}

func (j testJob) ID() string { return j.id }

func (j testJob) Requires() []string { return j.requires }

func (j testJob) Run(ctx context.Context) error {
	if j.delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(j.delay):
		}
	}
	if j.onRun != nil {
		j.onRun(j.id)
	}
	return j.err
}

func TestExecutorRespectsDependencies(t *testing.T) {
	var mu sync.Mutex
	runOrder := []string{}
	record := func(id string) {
		mu.Lock()
		defer mu.Unlock()
		runOrder = append(runOrder, id)
	}

	jobs := []Job{
		testJob{id: "a", onRun: record},
		testJob{id: "b", requires: []string{"a"}, onRun: record},
		testJob{id: "c", requires: []string{"a"}, onRun: record},
	}

	executor := Executor{Workers: 2}
	if err := executor.Run(context.Background(), jobs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(runOrder) != 3 {
		t.Fatalf("expected 3 jobs to run, got %d", len(runOrder))
	}

	if runOrder[0] != "a" {
		t.Fatalf("expected first job to be a, got %s", runOrder[0])
	}
}

func TestExecutorStopsOnFailure(t *testing.T) {
	fail := errors.New("boom")
	jobs := []Job{
		testJob{id: "a", err: fail},
		testJob{id: "b", requires: []string{"a"}},
	}

	executor := Executor{Workers: 2}
	err := executor.Run(context.Background(), jobs)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestExecutorRunsJobsInParallel(t *testing.T) {
	jobs := []Job{
		testJob{id: "a", delay: 200 * time.Millisecond},
		testJob{id: "b", delay: 200 * time.Millisecond},
	}

	start := time.Now()
	executor := Executor{Workers: 2}
	if err := executor.Run(context.Background(), jobs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	elapsed := time.Since(start)
	if elapsed >= 350*time.Millisecond {
		t.Fatalf("expected parallel execution to finish faster, elapsed=%s", elapsed)
	}
}
