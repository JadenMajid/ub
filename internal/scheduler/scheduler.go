package scheduler

import (
	"context"
	"fmt"
	"sync"
)

type contextKey string

const workerIDContextKey contextKey = "ub.scheduler.workerID"

func WithWorkerID(ctx context.Context, workerID int) context.Context {
	return context.WithValue(ctx, workerIDContextKey, workerID)
}

func WorkerID(ctx context.Context) (int, bool) {
	value := ctx.Value(workerIDContextKey)
	workerID, ok := value.(int)
	return workerID, ok
}

type Job interface {
	ID() string
	Requires() []string
	Run(ctx context.Context) error
}

type Executor struct {
	Workers        int
	OnJobStart     func(workerID int, jobID string)
	OnJobComplete  func(workerID int, jobID string)
	OnJobError     func(workerID int, jobID string, err error)
}

func (e Executor) Run(ctx context.Context, jobs []Job) error {
	if e.Workers <= 0 {
		e.Workers = 1
	}

	jobByID := make(map[string]Job, len(jobs))
	dependents := make(map[string][]string, len(jobs))
	inDegree := make(map[string]int, len(jobs))

	for _, j := range jobs {
		id := j.ID()
		if _, exists := jobByID[id]; exists {
			return fmt.Errorf("duplicate job id %q", id)
		}
		jobByID[id] = j
		inDegree[id] = len(j.Requires())
	}

	for _, j := range jobs {
		for _, dep := range j.Requires() {
			if _, ok := jobByID[dep]; !ok {
				return fmt.Errorf("job %q requires unknown job %q", j.ID(), dep)
			}
			dependents[dep] = append(dependents[dep], j.ID())
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ready := make(chan string, len(jobs))
	completed := make(chan string, len(jobs))
	errs := make(chan error, 1)

	var workerWG sync.WaitGroup
	for workerID := 1; workerID <= e.Workers; workerID++ {
		workerWG.Add(1)
		go func(workerID int) {
			defer workerWG.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case id, ok := <-ready:
					if !ok {
						return
					}
					if e.OnJobStart != nil {
						e.OnJobStart(workerID, id)
					}
					jobCtx := WithWorkerID(ctx, workerID)
					if err := jobByID[id].Run(jobCtx); err != nil {
						if e.OnJobError != nil {
							e.OnJobError(workerID, id, err)
						}
						select {
						case errs <- fmt.Errorf("job %q failed: %w", id, err):
						default:
						}
						cancel()
						return
					}
					if e.OnJobComplete != nil {
						e.OnJobComplete(workerID, id)
					}
					select {
					case completed <- id:
					case <-ctx.Done():
						return
					}
				}
			}
		}(workerID)
	}

	queued := map[string]bool{}
	for id, deg := range inDegree {
		if deg == 0 {
			queued[id] = true
			ready <- id
		}
	}

	if len(queued) == 0 && len(jobs) > 0 {
		close(ready)
		workerWG.Wait()
		return fmt.Errorf("no initial runnable jobs; cycle likely present")
	}

	finished := 0
	for finished < len(jobs) {
		select {
		case err := <-errs:
			close(ready)
			workerWG.Wait()
			return err
		case <-ctx.Done():
			close(ready)
			workerWG.Wait()
			if len(errs) > 0 {
				return <-errs
			}
			return ctx.Err()
		case id := <-completed:
			finished++
			for _, dependent := range dependents[id] {
				inDegree[dependent]--
				if inDegree[dependent] == 0 {
					if !queued[dependent] {
						queued[dependent] = true
						ready <- dependent
					}
				}
			}
		}
	}

	close(ready)
	workerWG.Wait()
	return nil
}
