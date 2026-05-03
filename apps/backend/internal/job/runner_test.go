package job_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cadenlund/wakeup/apps/backend/internal/job"
)

// fakeJob is a Job that increments a counter on every Run and waits a
// fixed Interval between ticks. Optionally returns a synthetic error so
// we can verify the runner doesn't kill the goroutine on failure.
type fakeJob struct {
	name      string
	interval  time.Duration
	runCount  atomic.Int64
	returnErr error
	// ranAt is closed on the first run so tests can wait for the loop
	// to actually fire at least once without polling.
	ranAt chan struct{}
}

func newFakeJob(name string, interval time.Duration) *fakeJob {
	return &fakeJob{name: name, interval: interval, ranAt: make(chan struct{}, 1)}
}

func (f *fakeJob) Name() string            { return f.name }
func (f *fakeJob) Interval() time.Duration { return f.interval }
func (f *fakeJob) Run(_ context.Context) error {
	f.runCount.Add(1)
	select {
	case f.ranAt <- struct{}{}:
	default:
	}
	return f.returnErr
}

func TestRunner_RunsRegisteredJobsOnInterval(t *testing.T) {
	t.Parallel()
	r := job.New(nil)
	j := newFakeJob("ticker", 30*time.Millisecond)
	r.Register(j)

	r.Start(context.Background())
	defer r.Stop()

	select {
	case <-j.ranAt:
		// First tick fired — runner is alive.
	case <-time.After(2 * time.Second):
		t.Fatal("job did not run within 2s")
	}
	// Wait long enough that at least 2 ticks should have happened so the
	// counter assertion isn't flaky.
	time.Sleep(80 * time.Millisecond)
	if got := j.runCount.Load(); got < 2 {
		t.Errorf("runCount = %d, want >= 2", got)
	}
}

func TestRunner_StopBlocksUntilGoroutinesReturn(t *testing.T) {
	t.Parallel()
	r := job.New(nil)
	j := newFakeJob("blocker", 10*time.Millisecond)
	r.Register(j)
	r.Start(context.Background())

	// Wait for the first tick so we know the goroutine is running.
	<-j.ranAt
	stopped := make(chan struct{})
	go func() {
		r.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		// Good.
	case <-time.After(2 * time.Second):
		t.Fatal("Stop did not return within 2s")
	}
	preStop := j.runCount.Load()
	// After Stop, runCount must not increase.
	time.Sleep(50 * time.Millisecond)
	if got := j.runCount.Load(); got != preStop {
		t.Errorf("runCount changed after Stop: %d → %d", preStop, got)
	}
}

func TestRunner_KeepsTickingAfterError(t *testing.T) {
	t.Parallel()
	r := job.New(nil)
	j := newFakeJob("errors", 20*time.Millisecond)
	j.returnErr = errors.New("simulated")
	r.Register(j)
	r.Start(context.Background())
	defer r.Stop()

	// Wait long enough for at least 3 ticks even if the runner skipped
	// one due to scheduling jitter; keep this loose to avoid flake.
	time.Sleep(120 * time.Millisecond)
	if got := j.runCount.Load(); got < 3 {
		t.Errorf("runCount after error = %d, want >= 3 (errors must not stop the loop)", got)
	}
}

func TestRunner_StartIsIdempotent(t *testing.T) {
	t.Parallel()
	r := job.New(nil)
	j := newFakeJob("idem", 30*time.Millisecond)
	r.Register(j)
	r.Start(context.Background())
	r.Start(context.Background())
	defer r.Stop()
	<-j.ranAt
	// If Start spawned a second goroutine, runCount would tick at twice
	// the natural rate. Wait two intervals (≈ 60 ms; +slack for
	// scheduler jitter) and assert at most three ticks have happened —
	// "≤ 3" leaves headroom for one extra tick under load while still
	// catching the duplicate-loop regression where we'd see ≥ 4.
	time.Sleep(80 * time.Millisecond)
	if got := j.runCount.Load(); got > 3 {
		t.Errorf("runCount = %d (want ≤ 3) — Start may have spawned duplicate goroutines", got)
	}
}

func TestRunner_RegisterAfterStartPanics(t *testing.T) {
	t.Parallel()
	r := job.New(nil)
	r.Start(context.Background())
	defer r.Stop()
	defer func() {
		if recover() == nil {
			t.Error("Register after Start should panic")
		}
	}()
	r.Register(newFakeJob("late", time.Second))
}

func TestRunner_StopIsSafeBeforeStart(t *testing.T) {
	t.Parallel()
	r := job.New(nil)
	r.Stop() // must not panic
}

func TestRunner_RegisterRejectsNonPositiveInterval(t *testing.T) {
	t.Parallel()
	r := job.New(nil)
	defer func() {
		if recover() == nil {
			t.Error("Register with non-positive Interval should panic (would otherwise crash time.NewTicker on first tick)")
		}
	}()
	r.Register(newFakeJob("zero", 0))
}

func TestRunner_RegisterRejectsNilJob(t *testing.T) {
	t.Parallel()
	r := job.New(nil)
	defer func() {
		if recover() == nil {
			t.Error("Register(nil) should panic")
		}
	}()
	r.Register(nil)
}

// Race regression: a goroutine that calls Stop() right after Start()
// must not see Wait() return before the spawned tick loops have
// registered themselves with the WaitGroup. Run a few times so the
// race detector has a chance to spot it if the WaitGroup.Add ever
// drifts back outside the lock.
func TestRunner_StartStopRaceFreedom(t *testing.T) {
	t.Parallel()
	for i := 0; i < 10; i++ {
		r := job.New(nil)
		r.Register(newFakeJob("racy", 5*time.Millisecond))
		r.Start(context.Background())
		r.Stop()
	}
}
