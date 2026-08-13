package defender

import (
	"context"
	"errors"
	"io"
	"log/slog"
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
	armingChecks  int
	fightingLevel int
}

type recordingObserver struct {
	levels      []observation
	evaluations int
	actions     []string
}

func (o *recordingObserver) ObserveLevel(state State, armingChecks, fightingLevel int) {
	o.levels = append(o.levels, observation{state, armingChecks, fightingLevel})
}
func (o *recordingObserver) ObserveEvaluation(error) { o.evaluations++ }
func (o *recordingObserver) ObserveAction(action string, _ error) {
	o.actions = append(o.actions, action)
}

func newTestController(t *testing.T, checks, maxLevel int, p *fakeProvider, a *fakeAction, clock *fakeClock) *Controller {
	t.Helper()
	c, err := newWithClock(p, a, Policy{
		ArmingChecks: checks,
		BaseDuration: 10 * time.Minute,
		MaxLevel:     maxLevel,
	}, time.Minute, slog.New(slog.NewTextHandler(io.Discard, nil)), clock)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func tick(t *testing.T, c *Controller) error {
	t.Helper()
	return c.Tick(context.Background())
}

func TestStandbyAndArmingChecks(t *testing.T) {
	p := &fakeProvider{results: []metricResult{{value: false}, {value: true}, {value: true}, {value: true}}}
	a := &fakeAction{}
	c := newTestController(t, 2, 3, p, a, &fakeClock{})

	if err := tick(t, c); err != nil || c.State() != Standby {
		t.Fatalf("false standby tick: state=%s err=%v", c.State(), err)
	}
	if err := tick(t, c); err != nil || c.State() != Arming || c.ArmingCheckCount() != 0 {
		t.Fatalf("first true must only arm: state=%s checks=%d err=%v", c.State(), c.ArmingCheckCount(), err)
	}
	if err := tick(t, c); err != nil || c.State() != Arming || c.ArmingCheckCount() != 1 || a.activations != 0 {
		t.Fatalf("first arming check: state=%s checks=%d activations=%d", c.State(), c.ArmingCheckCount(), a.activations)
	}
	if err := tick(t, c); err != nil || c.State() != Fighting || a.activations != 1 {
		t.Fatalf("second arming check: state=%s activations=%d err=%v", c.State(), a.activations, err)
	}
}

func TestObserverReceivesRuntimeProjection(t *testing.T) {
	clock := &fakeClock{now: time.Unix(100, 0)}
	p := &fakeProvider{results: []metricResult{{value: true}, {value: true}, {value: false}}}
	a := &fakeAction{}
	observer := &recordingObserver{}
	c, err := newWithClock(p, a, Policy{ArmingChecks: 1, BaseDuration: time.Minute, MaxLevel: 2}, time.Second, nil, clock, WithObserver(observer))
	if err != nil {
		t.Fatal(err)
	}
	_ = tick(t, c) // standby -> arming
	_ = tick(t, c) // arming -> fighting
	clock.now = clock.now.Add(time.Minute)
	_ = tick(t, c) // fighting -> arming
	_ = tick(t, c) // arming -> standby

	want := []observation{
		{Standby, 0, 1},
		{Arming, 0, 1},
		{Fighting, 1, 1},
		{Arming, 0, 1},
		{Standby, 0, 1},
	}
	if len(observer.levels) != len(want) {
		t.Fatalf("level observations=%v, want %v", observer.levels, want)
	}
	for i := range want {
		if observer.levels[i] != want[i] {
			t.Fatalf("observation %d=%v, want %v", i, observer.levels[i], want[i])
		}
	}
	if observer.evaluations != 3 {
		t.Fatalf("evaluations=%d", observer.evaluations)
	}
	if len(observer.actions) != 2 || observer.actions[0] != ActionEnable || observer.actions[1] != ActionDisable {
		t.Fatalf("actions=%v", observer.actions)
	}
}

func TestArmingFalseReturnsToStandbyAndResets(t *testing.T) {
	p := &fakeProvider{results: []metricResult{{value: true}, {value: true}, {value: false}}}
	c := newTestController(t, 3, 3, p, &fakeAction{}, &fakeClock{})
	_ = tick(t, c)
	_ = tick(t, c)
	c.fightingLevel = 3
	c.foughtInCycle = true
	if err := tick(t, c); err != nil {
		t.Fatal(err)
	}
	if c.State() != Standby || c.ArmingCheckCount() != 0 || c.FightingLevel() != 1 || c.foughtInCycle {
		t.Fatalf("reset failed: state=%s checks=%d level=%d", c.State(), c.ArmingCheckCount(), c.FightingLevel())
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
	if err := tick(t, c); err != nil || c.State() != Arming || c.ArmingCheckCount() != 0 || a.deactivations != 2 {
		t.Fatalf("deactivation retry: state=%s attempts=%d err=%v", c.State(), a.deactivations, err)
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

	_ = tick(t, c) // standby -> arming
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
	if c.State() != Arming || c.ArmingCheckCount() != 0 || a.activations != 0 {
		t.Fatalf("metrics error did not reset progress: state=%s checks=%d", c.State(), c.ArmingCheckCount())
	}
	if err := tick(t, c); err != nil || c.State() != Arming || c.ArmingCheckCount() != 1 {
		t.Fatalf("first check after error: state=%s checks=%d err=%v", c.State(), c.ArmingCheckCount(), err)
	}
	if err := tick(t, c); err != nil || c.State() != Fighting {
		t.Fatalf("second check after error should complete arming: state=%s err=%v", c.State(), err)
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
