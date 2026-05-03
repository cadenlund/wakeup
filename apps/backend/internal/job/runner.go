// Package job is the §4.12 background-job runner. Three sweepers
// register here in production: presence (30s), idempotency-keys (1h),
// expired-sessions (1h). Phase 7 adds the attachment orphan sweeper as
// the first registered job.
//
// Lifecycle:
//
//   - Register every job before Start.
//   - Start launches one goroutine per job. Each goroutine ticks at the
//     job's declared Interval and calls Run with a child of the runner
//     context.
//   - Stop cancels the runner context and blocks until every goroutine
//     has returned. Drives main.go's §4.9 graceful-shutdown sequence.
//
// Job-level errors are logged at warn but the goroutine continues — a
// transient DB blip on one tick should not silently kill a sweeper for
// the rest of the process.
package job

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Job is the contract every sweeper implements. Implementations live in
// their service packages (e.g. `internal/service/attachment/orphan_sweeper.go`).
type Job interface {
	// Name is a stable identifier used in logs / metrics. Should match
	// the file's package, e.g. "attachment-orphan-sweeper".
	Name() string
	// Interval is the gap between ticks. The first tick fires after
	// Interval has elapsed (so a fresh process doesn't immediately
	// hammer downstreams during boot).
	Interval() time.Duration
	// Run executes one tick. ctx is a child of the Runner's context; on
	// shutdown it is cancelled and the implementation should return as
	// soon as practical.
	Run(ctx context.Context) error
}

// Runner is the lifecycle owner.
type Runner struct {
	jobs   []Job
	log    *slog.Logger
	mu     sync.Mutex
	wg     sync.WaitGroup
	cancel context.CancelFunc
	// started guards Start so accidental double-Start is a no-op rather
	// than a goroutine leak.
	started bool
}

// New returns a Runner. Logger defaults to slog.Default() when nil so a
// tiny test that does not pass a logger still writes somewhere.
func New(log *slog.Logger) *Runner {
	if log == nil {
		log = slog.Default()
	}
	return &Runner{log: log}
}

// Register adds a job. Must be called before Start; calling after Start
// is a programmer error.
func (r *Runner) Register(j Job) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.started {
		// Surfacing this as a panic is intentional: registering after
		// Start would silently never-tick the new job.
		panic("job: Register called after Start")
	}
	r.jobs = append(r.jobs, j)
}

// Start launches one goroutine per registered job. Idempotent — a
// repeated Start is a no-op.
func (r *Runner) Start(ctx context.Context) {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return
	}
	r.started = true
	rootCtx, cancel := context.WithCancel(ctx)
	r.cancel = cancel
	jobs := append([]Job(nil), r.jobs...)
	r.mu.Unlock()

	for _, j := range jobs {
		r.wg.Add(1)
		go r.tickLoop(rootCtx, j)
	}
}

// Stop cancels the runner context and blocks until every goroutine has
// returned. Safe to call before Start (no-op) and safe to call twice.
func (r *Runner) Stop() {
	r.mu.Lock()
	cancel := r.cancel
	r.cancel = nil
	r.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	r.wg.Wait()
}

// tickLoop is one job's lifecycle: NewTicker(Interval()) → Run on each
// tick → return when ctx is done.
func (r *Runner) tickLoop(ctx context.Context, j Job) {
	defer r.wg.Done()
	ticker := time.NewTicker(j.Interval())
	defer ticker.Stop()
	r.log.Info("job: started",
		slog.String("name", j.Name()),
		slog.Duration("interval", j.Interval()),
	)
	for {
		select {
		case <-ctx.Done():
			r.log.Info("job: stopped", slog.String("name", j.Name()))
			return
		case <-ticker.C:
			runCtx := ctx
			start := time.Now()
			if err := j.Run(runCtx); err != nil {
				// A cancelled ctx during shutdown isn't a real failure;
				// drop it down to debug so shutdown logs stay clean.
				if errors.Is(err, context.Canceled) {
					r.log.Debug("job: cancelled", slog.String("name", j.Name()))
					continue
				}
				r.log.Warn("job: run failed",
					slog.String("name", j.Name()),
					slog.Duration("elapsed", time.Since(start)),
					slog.String("error", err.Error()),
				)
				continue
			}
			r.log.Debug("job: tick ok",
				slog.String("name", j.Name()),
				slog.Duration("elapsed", time.Since(start)),
			)
		}
	}
}
