package defender

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type metricResult struct {
	value bool
	err   error
}

type fakeProvider struct {
	results []metricResult
	calls   int
}

func (p *fakeProvider) Evaluate(context.Context) (bool, error) {
	p.calls++
	if len(p.results) == 0 {
		return false, nil
	}
	r := p.results[0]
	p.results = p.results[1:]
	return r.value, r.err
}

type fakeAction struct {
	activateErrs   []error
	deactivateErrs []error
	activations    int
	deactivations  int
}

func (a *fakeAction) Activate(context.Context) error {
	a.activations++
	return popError(&a.activateErrs)
}

func (a *fakeAction) Deactivate(context.Context) error {
	a.deactivations++
	return popError(&a.deactivateErrs)
}

func popError(errs *[]error) error {
	if len(*errs) == 0 {
		return nil
	}
	err := (*errs)[0]
	*errs = (*errs)[1:]
	return err
}

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }
func (c *fakeClock) NewTimer(time.Duration) Timer {
	panic("NewTimer is not used by state-machine unit tests")
}

type observation struct {
	state         State
	armingLevel   int
	fightingLevel int
}

type recordingObserver struct {
	levels []observation
}

func (o *recordingObserver) ObserveLevel(state State, armingLevel, fightingLevel int) {
	o.levels = append(o.levels, observation{state, armingLevel, fightingLevel})
}

func newTestController(t *testing.T, armingLevels, fightingLevels int, p *fakeProvider, a *fakeAction, clock *fakeClock) *Controller {
	t.Helper()
	c, err := newWithClock(p, a, Policy{
		ArmingLevels:          armingLevels,
		FightingLevelDuration: 10 * time.Minute,
		FightingLevels:        fightingLevels,
	}, time.Minute, 6*time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)), clock)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func tick(t *testing.T, c *Controller) error {
	t.Helper()
	return c.Tick(context.Background())
}

func TestNormalAndArmingLevels(t *testing.T) {
	p := &fakeProvider{results: []metricResult{{value: false}, {value: true}, {value: true}, {value: true}}}
	a := &fakeAction{}
	c := newTestController(t, 2, 3, p, a, &fakeClock{})

	if err := tick(t, c); err != nil || c.State() != Normal {
		t.Fatalf("false normal tick: state=%s err=%v", c.State(), err)
	}
	if err := tick(t, c); err != nil || c.State() != Arming || c.ArmingLevel() != 0 {
		t.Fatalf("first true must only arm: state=%s arming_level=%d err=%v", c.State(), c.ArmingLevel(), err)
	}
	if err := tick(t, c); err != nil || c.State() != Arming || c.ArmingLevel() != 1 || a.activations != 0 {
		t.Fatalf("arming level 1: state=%s arming_level=%d activations=%d", c.State(), c.ArmingLevel(), a.activations)
	}
	if err := tick(t, c); err != nil || c.State() != Fighting || a.activations != 1 {
		t.Fatalf("arming completion: state=%s activations=%d err=%v", c.State(), a.activations, err)
	}
}

func TestObserverReceivesRuntimeProjection(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	p := &fakeProvider{results: []metricResult{{value: true}, {value: true}, {value: false}}}
	a := &fakeAction{}
	observer := &recordingObserver{}
	c, err := newWithClock(p, a, Policy{ArmingLevels: 1, FightingLevelDuration: time.Minute, FightingLevels: 2}, time.Second, 6*time.Hour, nil, clock, WithObserver(observer))
	if err != nil {
		t.Fatal(err)
	}
	_ = tick(t, c) // normal -> arming
	_ = tick(t, c) // arming -> fighting
	clock.now = clock.now.Add(time.Minute)
	_ = tick(t, c) // fighting -> arming
	_ = tick(t, c) // arming -> normal

	want := []observation{
		{Normal, 0, 1},
		{Arming, 0, 1},
		{Fighting, 0, 1},
		{Arming, 0, 1},
		{Normal, 0, 1},
	}
	if len(observer.levels) != len(want) {
		t.Fatalf("level observations=%v, want %v", observer.levels, want)
	}
	for i := range want {
		if observer.levels[i] != want[i] {
			t.Fatalf("observation %d=%v, want %v", i, observer.levels[i], want[i])
		}
	}
}

func TestArmingFalseReturnsToNormalAndResets(t *testing.T) {
	p := &fakeProvider{results: []metricResult{{value: true}, {value: true}, {value: false}}}
	c := newTestController(t, 3, 3, p, &fakeAction{}, &fakeClock{})
	_ = tick(t, c)
	_ = tick(t, c)
	c.fightingLevel = 3
	c.foughtInCycle = true
	if err := tick(t, c); err != nil {
		t.Fatal(err)
	}
	if c.State() != Normal || c.ArmingLevel() != 0 || c.FightingLevel() != 1 || c.foughtInCycle {
		t.Fatalf("reset failed: state=%s arming_level=%d fighting_level=%d", c.State(), c.ArmingLevel(), c.FightingLevel())
	}
}

func TestActivateFailureKeepsArmingAndRetries(t *testing.T) {
	p := &fakeProvider{results: []metricResult{{value: true}, {value: true}, {value: true}}}
	a := &fakeAction{activateErrs: []error{errors.New("temporary"), nil}}
	c := newTestController(t, 1, 3, p, a, &fakeClock{})
	_ = tick(t, c)
	if err := tick(t, c); err == nil || c.State() != Arming {
		t.Fatalf("activation failure: state=%s err=%v", c.State(), err)
	}
	if c.ArmingLevel() != 0 {
		t.Fatalf("activation failure advanced past final arming level: level=%d", c.ArmingLevel())
	}
	if err := tick(t, c); err != nil || c.State() != Fighting || a.activations != 2 {
		t.Fatalf("activation retry: state=%s attempts=%d err=%v", c.State(), a.activations, err)
	}
}

func TestFightingDoesNotPollAndDeactivationControlsTransition(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	p := &fakeProvider{results: []metricResult{{value: true}, {value: true}}}
	a := &fakeAction{deactivateErrs: []error{errors.New("temporary"), nil}}
	c := newTestController(t, 1, 3, p, a, clock)
	_ = tick(t, c)
	_ = tick(t, c)
	if c.FightingDuration() != 10*time.Minute {
		t.Fatalf("level 1 duration=%s", c.FightingDuration())
	}

	clock.now = clock.now.Add(9 * time.Minute)
	if err := tick(t, c); err != nil || p.calls != 2 || a.deactivations != 0 || c.State() != Fighting {
		t.Fatalf("early fight tick polled or deactivated: calls=%d deactivations=%d", p.calls, a.deactivations)
	}
	clock.now = clock.now.Add(time.Minute)
	if err := tick(t, c); err == nil || c.State() != Fighting || a.deactivations != 1 || p.calls != 2 {
		t.Fatalf("failed deactivation changed state: state=%s err=%v", c.State(), err)
	}
	if err := tick(t, c); err != nil || c.State() != Arming || c.ArmingLevel() != 0 || a.deactivations != 2 {
		t.Fatalf("deactivation retry: state=%s attempts=%d err=%v", c.State(), a.deactivations, err)
	}
}

func TestInitialFightingStartsProtectedAndDeactivatesAfterLevelDuration(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	p := &fakeProvider{}
	a := &fakeAction{}
	c, err := newWithClock(p, a, Policy{
		ArmingLevels:          1,
		FightingLevelDuration: 10 * time.Minute,
		FightingLevels:        3,
	}, time.Minute, 6*time.Hour, nil, clock, WithInitialFighting())
	if err != nil {
		t.Fatal(err)
	}
	if c.State() != Fighting || c.nextDelay() != 10*time.Minute {
		t.Fatalf("state=%s delay=%s", c.State(), c.nextDelay())
	}
	if err := tick(t, c); err != nil || p.calls != 0 || a.deactivations != 0 {
		t.Fatalf("initial fighting polled or deactivated early: calls=%d deactivations=%d err=%v", p.calls, a.deactivations, err)
	}
	clock.now = clock.now.Add(10 * time.Minute)
	if err := tick(t, c); err != nil || c.State() != Arming || p.calls != 0 || a.deactivations != 1 {
		t.Fatalf("state=%s calls=%d deactivations=%d err=%v", c.State(), p.calls, a.deactivations, err)
	}
}

func TestRepeatedFightingIncreasesAndCapsLevel(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	p := &fakeProvider{results: []metricResult{
		{value: true}, {value: true},
		{value: true},
		{value: true},
	}}
	c := newTestController(t, 1, 2, p, &fakeAction{}, clock)

	_ = tick(t, c) // normal -> arming
	_ = tick(t, c) // fighting level 1
	clock.now = clock.now.Add(10 * time.Minute)
	_ = tick(t, c) // fighting -> arming
	_ = tick(t, c) // fighting level 2
	if c.FightingLevel() != 2 || c.FightingDuration() != 20*time.Minute {
		t.Fatalf("second fight: level=%d duration=%s", c.FightingLevel(), c.FightingDuration())
	}
	clock.now = clock.now.Add(20 * time.Minute)
	_ = tick(t, c)
	_ = tick(t, c) // capped at level 2
	if c.FightingLevel() != 2 || c.FightingDuration() != 20*time.Minute {
		t.Fatalf("capped fight: level=%d duration=%s", c.FightingLevel(), c.FightingDuration())
	}
}

func TestMetricsErrorsResetArmingProgress(t *testing.T) {
	metricErr := errors.New("unavailable")
	p := &fakeProvider{results: []metricResult{{value: true}, {value: true}, {err: metricErr}, {value: true}, {value: true}}}
	a := &fakeAction{}
	c := newTestController(t, 2, 3, p, a, &fakeClock{})
	_ = tick(t, c)
	_ = tick(t, c)
	if err := tick(t, c); !errors.Is(err, metricErr) {
		t.Fatalf("expected wrapped metrics error, got %v", err)
	}
	if c.State() != Arming || c.ArmingLevel() != 0 || a.activations != 0 {
		t.Fatalf("metrics error did not reset progress: state=%s arming_level=%d", c.State(), c.ArmingLevel())
	}
	if err := tick(t, c); err != nil || c.State() != Arming || c.ArmingLevel() != 1 {
		t.Fatalf("first match after error: state=%s arming_level=%d err=%v", c.State(), c.ArmingLevel(), err)
	}
	if err := tick(t, c); err != nil || c.State() != Fighting {
		t.Fatalf("second match after error should complete arming: state=%s err=%v", c.State(), err)
	}
}

type blockingTimer struct {
	ch      chan time.Time
	stopped *atomic.Bool
}

func (t *blockingTimer) C() <-chan time.Time { return t.ch }
func (t *blockingTimer) Stop() bool {
	t.stopped.Store(true)
	return true
}

type blockingClock struct {
	created chan struct{}
	stopped atomic.Bool
}

func (*blockingClock) Now() time.Time { return time.Time{} }
func (c *blockingClock) NewTimer(time.Duration) Timer {
	close(c.created)
	return &blockingTimer{ch: make(chan time.Time), stopped: &c.stopped}
}

func TestRunStopsTimerOnContextCancellation(t *testing.T) {
	clock := &blockingClock{created: make(chan struct{})}
	a := &fakeAction{}
	c := newTestController(t, 1, 1, &fakeProvider{}, a, &fakeClock{})
	c.clock = clock
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	<-clock.created
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if !clock.stopped.Load() {
		t.Fatal("timer was not stopped")
	}
	if a.deactivations != 0 {
		t.Fatalf("startup deactivations=%d, want 0", a.deactivations)
	}
}

type capturingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *capturingHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h *capturingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r)
	return nil
}
func (h *capturingHandler) WithAttrs(attrs []slog.Attr) slog.Handler { return h }
func (h *capturingHandler) WithGroup(name string) slog.Handler       { return h }

func (h *capturingHandler) countInfoMsg(msg string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, r := range h.records {
		if r.Level == slog.LevelInfo && r.Message == msg {
			n++
		}
	}
	return n
}

// advancingClock fires timers immediately and advances its internal clock by
// the requested duration on each NewTimer call, simulating elapsed time.
type advancingClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *advancingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *advancingClock) NewTimer(d time.Duration) Timer {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
	ch := make(chan time.Time, 1)
	ch <- c.Now()
	return &chanTimer{ch: ch}
}

type chanTimer struct{ ch chan time.Time }

func (t *chanTimer) C() <-chan time.Time { return t.ch }
func (t *chanTimer) Stop() bool         { return false }

func TestRunEmitsPeriodicStatusLog(t *testing.T) {
	statusInterval := time.Hour
	start := time.Unix(0, 0)
	clock := &advancingClock{now: start}

	p := &fakeProvider{}
	a := &fakeAction{}
	h := &capturingHandler{}
	logger := slog.New(h)

	c, err := newWithClock(p, a, Policy{
		ArmingLevels:          1,
		FightingLevelDuration: time.Minute,
		FightingLevels:        1,
	}, time.Minute, statusInterval, logger, clock)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()

	// Wait until at least one status log has been emitted.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if h.countInfoMsg("status") >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if n := h.countInfoMsg("status"); n < 1 {
		t.Fatalf("expected at least one status log, got %d", n)
	}
}
