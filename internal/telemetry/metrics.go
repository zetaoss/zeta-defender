package telemetry

import (
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/zetaoss/zeta-defender/internal/defender"
)

const (
	armingLevelBase   = 100
	fightingLevelBase = 200
	maxLevelOffset    = 99
)

type Metrics struct {
	registry         *prometheus.Registry
	level            prometheus.Gauge
	fightingSeconds  prometheus.CounterFunc
	now              func() time.Time
	mu               sync.Mutex
	fighting         bool
	fightingStarted  time.Time
	completedSeconds float64
}

func New() *Metrics {
	return newWithNow(time.Now)
}

func newWithNow(now func() time.Time) *Metrics {
	registry := prometheus.NewRegistry()
	m := &Metrics{
		registry: registry,
		now:      now,
		level: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "zeta_defender_level",
			Help: "Current defender level. 0xx=normal, 1xx=arming, 2xx=fighting.",
		}),
	}
	m.fightingSeconds = prometheus.NewCounterFunc(prometheus.CounterOpts{
		Name: "zeta_defender_fighting_seconds_total",
		Help: "Total time in seconds that the defender has spent in the fighting state.",
	}, m.currentFightingSeconds)
	registry.MustRegister(m.level, m.fightingSeconds)
	return m
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) ObserveLevel(state defender.State, armingLevel, fightingLevel int) {
	m.observeFightingTime(state)

	var level int
	switch state {
	case defender.Arming:
		level = armingLevelBase + clampLevelOffset(armingLevel)
	case defender.Fighting:
		level = fightingLevelBase + clampLevelOffset(fightingLevel)
	}
	m.level.Set(float64(level))
}

func clampLevelOffset(level int) int {
	if level < 0 {
		return 0
	}
	if level > maxLevelOffset {
		return maxLevelOffset
	}
	return level
}

func (m *Metrics) observeFightingTime(state defender.State) {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := m.now()
	if state == defender.Fighting && !m.fighting {
		m.fighting = true
		m.fightingStarted = now
		return
	}
	if state != defender.Fighting && m.fighting {
		m.completedSeconds += elapsedSeconds(m.fightingStarted, now)
		m.fighting = false
	}
}

func (m *Metrics) currentFightingSeconds() float64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	total := m.completedSeconds
	if m.fighting {
		total += elapsedSeconds(m.fightingStarted, m.now())
	}
	return total
}

func elapsedSeconds(start, end time.Time) float64 {
	if end.Before(start) {
		return 0
	}
	return end.Sub(start).Seconds()
}
