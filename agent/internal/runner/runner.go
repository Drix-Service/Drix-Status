// Package runner schedules checks for the monitors currently assigned to
// this agent. Each monitor gets its own lightweight goroutine and ticker; a
// global semaphore caps concurrent in-flight probes.
package runner

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"drix/agent/internal/checks"
	"drix/shared/protocol"
)

type job struct {
	spec   protocol.MonitorSpec
	cancel context.CancelFunc
}

// Runner owns the set of scheduled monitor jobs.
type Runner struct {
	out      chan<- protocol.Result
	sem      chan struct{}
	log      *slog.Logger
	mu       sync.Mutex
	jobs     map[int64]*job
	inFlight atomic.Int64
	root     context.Context
}

// New creates a runner that emits results to out.
func New(ctx context.Context, out chan<- protocol.Result, maxConcurrent int, log *slog.Logger) *Runner {
	if maxConcurrent <= 0 {
		maxConcurrent = 512
	}
	return &Runner{
		out:  out,
		sem:  make(chan struct{}, maxConcurrent),
		log:  log,
		jobs: map[int64]*job{},
		root: ctx,
	}
}

// Apply replaces the full assignment. Unchanged monitors keep their timers so
// reassignments never cause a burst of checks.
func (r *Runner) Apply(specs []protocol.MonitorSpec) (started, stopped int) {
	r.mu.Lock()
	defer r.mu.Unlock()

	want := make(map[int64]protocol.MonitorSpec, len(specs))
	for _, s := range specs {
		want[s.ID] = s
	}
	for id, j := range r.jobs {
		s, ok := want[id]
		if !ok || s.Rev != j.spec.Rev {
			j.cancel()
			delete(r.jobs, id)
			stopped++
		}
	}
	for id, s := range want {
		if _, ok := r.jobs[id]; ok {
			continue
		}
		ctx, cancel := context.WithCancel(r.root)
		r.jobs[id] = &job{spec: s, cancel: cancel}
		go r.loop(ctx, s)
		started++
	}
	return started, stopped
}

// Running returns the number of scheduled monitors.
func (r *Runner) Running() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.jobs)
}

// InFlight returns the number of checks currently executing.
func (r *Runner) InFlight() int { return int(r.inFlight.Load()) }

// Stop cancels all jobs.
func (r *Runner) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, j := range r.jobs {
		j.cancel()
		delete(r.jobs, id)
	}
}

// RunOnce executes a single ad-hoc check, honoring the concurrency cap.
func (r *Runner) RunOnce(ctx context.Context, s protocol.MonitorSpec) protocol.Result {
	select {
	case r.sem <- struct{}{}:
	case <-ctx.Done():
		return protocol.Result{MonitorID: s.ID, At: time.Now().UTC(), Message: "cancelled"}
	}
	defer func() { <-r.sem }()
	r.inFlight.Add(1)
	defer r.inFlight.Add(-1)
	return checks.Run(ctx, s)
}

// firstDelay spreads monitors deterministically across their interval so a
// fresh assignment of thousands of monitors doesn't fire all at once.
func firstDelay(s protocol.MonitorSpec) time.Duration {
	iv := s.Interval()
	if iv <= 0 {
		return 0
	}
	// Aligned to wall clock with a per-monitor phase so the server's
	// slot-based dedupe sees one result per slot regardless of which agent ran.
	now := time.Now()
	return protocol.NextRun(s.ID, iv, now).Sub(now)
}

func (r *Runner) loop(ctx context.Context, s protocol.MonitorSpec) {
	iv := s.Interval()
	if iv < time.Second {
		iv = 30 * time.Second
	}
	first := firstDelay(s)
	if s.Immediate {
		first = 0 // fresh or edited monitor: give feedback right away
	}
	timer := time.NewTimer(first)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		timer.Reset(iv)

		select {
		case r.sem <- struct{}{}:
		case <-ctx.Done():
			return
		}
		r.inFlight.Add(1)
		res := checks.Run(ctx, s)
		r.inFlight.Add(-1)
		<-r.sem

		if ctx.Err() != nil {
			return // reassigned mid-check; drop the result
		}
		select {
		case r.out <- res:
		case <-ctx.Done():
			return
		}
	}
}
