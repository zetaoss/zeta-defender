package defender

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"time"

	"github.com/zetaoss/zeta-defender/internal/action"
	"github.com/zetaoss/zeta-defender/internal/metrics"
)

type State string

const (
	Normal   State = "normal"
	Arming   State = "arming"
	Fighting State = "fighting"
)

type Policy struct {
	ArmingChecks int
	BaseDuration time.Duration
	MaxLevel     int
}

// Observer receives a one-way projection of controller activity. Implementations
// must not use observations to modify controller state.
type Observer interface {
	ObserveLevel(State, int, int)
}

type noopObserver struct{}

func (noopObserver) ObserveLevel(State, int, int) {}

type Option func(*Controller)

func WithObserver(observer Observer) Option {
	return func(c *Controller) {
		if observer != nil {
			c.observer = observer
		}
	}
}

func WithInitialFighting() Option {
	return func(c *Controller) {
		c.state = Fighting
		c.foughtInCycle = true
		c.fightingUntil = c.clock.Now().Add(c.FightingDuration())
	}
}

type Clock interface {
	Now() time.Time
	NewTimer(time.Duration) Timer
}

type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

type realClock struct{}

func (realClock) Now() time.Time                 { return time.Now() }
func (realClock) NewTimer(d time.Duration) Timer { return realTimer{time.NewTimer(d)} }

type realTimer struct{ *time.Timer }

func (t realTimer) C() <-chan time.Time { return t.Timer.C }

type Controller struct {
	provider metrics.Provider
	action   action.Action
	policy   Policy
	interval time.Duration
	clock    Clock
	logger   *slog.Logger
	observer Observer

	state         State
	armingChecks  int
	fightingLevel int
	foughtInCycle bool
	fightingUntil time.Time
}

func New(provider metrics.Provider, act action.Action, policy Policy, interval time.Duration, logger *slog.Logger, options ...Option) (*Controller, error) {
	return newWithClock(provider, act, policy, interval, logger, realClock{}, options...)
}

func newWithClock(provider metrics.Provider, act action.Action, policy Policy, interval time.Duration, logger *slog.Logger, clock Clock, options ...Option) (*Controller, error) {
	if provider == nil || act == nil || clock == nil {
		return nil, errors.New("metrics provider, action, and clock are required")
	}
	if interval <= 0 || policy.ArmingChecks <= 0 || policy.BaseDuration <= 0 || policy.MaxLevel < 1 {
		return nil, errors.New("interval, arming checks, base duration, and max level must be positive")
	}
	if int64(policy.BaseDuration) > math.MaxInt64/int64(policy.MaxLevel) {
		return nil, errors.New("base duration multiplied by max level overflows time.Duration")
	}
	if logger == nil {
		logger = slog.Default()
	}
	c := &Controller{
		provider: provider, action: act, policy: policy, interval: interval,
		clock: clock, logger: logger, observer: noopObserver{}, state: Normal, fightingLevel: 1,
	}
	for _, option := range options {
		option(c)
	}
	c.observeLevel()
	return c, nil
}

func (c *Controller) State() State          { return c.state }
func (c *Controller) ArmingCheckCount() int { return c.armingChecks }
func (c *Controller) FightingLevel() int    { return c.fightingLevel }
func (c *Controller) FightingDuration() time.Duration {
	return c.policy.BaseDuration * time.Duration(c.fightingLevel)
}

// Tick performs exactly one due operation. It never queries metrics while fighting.
func (c *Controller) Tick(ctx context.Context) error {
	if c.state == Fighting {
		if c.clock.Now().Before(c.fightingUntil) {
			return nil
		}
		if err := c.action.Deactivate(ctx); err != nil {
			return fmt.Errorf("deactivate defense: %w", err)
		}
		c.transition(Arming)
		c.armingChecks = 0
		c.observeLevel()
		return nil
	}

	matched, err := c.provider.Evaluate(ctx)
	if err != nil {
		if c.state == Arming && c.armingChecks != 0 {
			c.armingChecks = 0
			c.observeLevel()
		}
		return fmt.Errorf("evaluate metrics: %w", err)
	}
	return c.applyEvaluation(ctx, matched)
}

func (c *Controller) applyEvaluation(ctx context.Context, matched bool) error {
	switch c.state {
	case Normal:
		if matched {
			c.armingChecks = 0
			c.transition(Arming)
			c.observeLevel()
		}
	case Arming:
		if !matched {
			c.armingChecks = 0
			c.fightingLevel = 1
			c.foughtInCycle = false
			c.transition(Normal)
			c.observeLevel()
			return nil
		}
		c.armingChecks++
		if c.armingChecks < c.policy.ArmingChecks {
			c.observeLevel()
			return nil
		}
		if err := c.action.Activate(ctx); err != nil {
			c.observeLevel()
			return fmt.Errorf("activate defense: %w", err)
		}
		if c.foughtInCycle && c.fightingLevel < c.policy.MaxLevel {
			c.fightingLevel++
		}
		c.foughtInCycle = true
		c.fightingUntil = c.clock.Now().Add(c.FightingDuration())
		c.transition(Fighting)
		c.observeLevel()
	}
	return nil
}

func (c *Controller) observeLevel() {
	c.observer.ObserveLevel(c.state, c.armingChecks, c.fightingLevel)
}

func (c *Controller) transition(next State) {
	previous := c.state
	c.state = next
	c.logger.Info("state transition", "from", previous, "to", next, "fighting_level", c.fightingLevel)
}

func (c *Controller) nextDelay() time.Duration {
	if c.state != Fighting {
		return c.interval
	}
	remaining := c.fightingUntil.Sub(c.clock.Now())
	if remaining > 0 {
		return remaining
	}
	return c.interval
}

func (c *Controller) Run(ctx context.Context) error {
	delay := time.Duration(0)
	for {
		timer := c.clock.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C():
		}
		if err := c.Tick(ctx); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			c.logger.Error("controller operation failed; will retry", "state", c.state, "error", err)
		}
		delay = c.nextDelay()
	}
}
