package cron

import (
	"context"
	"log"
	"sync"
	"time"
)

// Job is a named periodic task.
type Job struct {
	Name     string        // Name is used for log lines
	Interval time.Duration // Interval controls how often Run fires
	Run      func(context.Context) error
}

// Scheduler fires Jobs at their intervals until ctx cancels.
type Scheduler struct {
	jobs []Job          // jobs is the registered list
	wg   sync.WaitGroup // wg tracks running job goroutines so Wait can block until they exit
}

// New constructs an empty Scheduler.
func New() *Scheduler {
	return &Scheduler{}
}

// Register adds a job to the scheduler. Call before Start.
func (s *Scheduler) Register(j Job) {
	s.jobs = append(s.jobs, j)
}

// Start launches one goroutine per registered job. Returns immediately.
func (s *Scheduler) Start(ctx context.Context) {
	for _, j := range s.jobs {
		s.wg.Add(1)
		go func(job Job) {
			defer s.wg.Done()
			runJob(ctx, job)
		}(j)
	}
}

// Wait blocks until all job goroutines have returned.
// Call after the context passed to Start is cancelled.
func (s *Scheduler) Wait() {
	s.wg.Wait()
}

func runJob(ctx context.Context, j Job) {
	t := time.NewTicker(j.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := j.Run(ctx); err != nil {
				log.Printf("cron %s: %v", j.Name, err)
			}
		}
	}
}
