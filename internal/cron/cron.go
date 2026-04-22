package cron

import (
	"context"
	"log"
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
	jobs []Job // jobs is the registered list
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
		go runJob(ctx, j)
	}
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
