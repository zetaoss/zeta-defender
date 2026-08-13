package telemetry

import (
	"net/http"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/zetaoss/zeta-defender/internal/defender"
)

const maxArmingLevel = 99

type Metrics struct {
	registry         *prometheus.Registry
	level            prometheus.Gauge
	evaluations      prometheus.Counter
	evaluationErrors prometheus.Counter
	actions          *prometheus.CounterVec
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
			Help: "Current defender level. 0=standby, 1-99=arming, 101+=fighting.",
		}),
		evaluations: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "zeta_defender_evaluations_total",
			Help: "Total number of Prometheus expression evaluations.",
		}),
		evaluationErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "zeta_defender_evaluation_errors_total",
			Help: "Total number of failed Prometheus expression evaluations.",
		}),
		actions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "zeta_defender_actions_total",
			Help: "Total number of defender action attempts by action and result.",
		}, []string{"action", "result"}),
	}
	m.fightingSeconds = prometheus.NewCounterFunc(prometheus.CounterOpts{
		Name: "zeta_defender_fighting_seconds_total",
		Help: "Total time in seconds that the defender has spent in the fighting state.",
	}, m.currentFightingSeconds)
	registry.MustRegister(m.level, m.evaluations, m.evaluationErrors, m.actions, m.fightingSeconds)
	for _, action := range []string{defender.ActionEnable, defender.ActionDisable} {
		for _, result := range []string{"success", "error"} {
			m.actions.WithLabelValues(action, result)
		}
	}
	return m
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

func (m *Metrics) ObserveLevel(state defender.State, armingChecks, fightingLevel int) {
	m.observeFightingTime(state)

	var level int
	switch state {
	case defender.Arming:
		level = 1 + armingChecks
		if level > maxArmingLevel {
			level = maxArmingLevel
		}
	case defender.Fighting:
		level = 100 + fightingLevel
	}
	m.level.Set(float64(level))
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

func (m *Metrics) ObserveEvaluation(err error) {
	m.evaluations.Inc()
	if err != nil {
		m.evaluationErrors.Inc()
	}
}

func (m *Metrics) ObserveAction(action string, err error) {
	if action != defender.ActionEnable && action != defender.ActionDisable {
		return
	}
	result := "success"
	if err != nil {
		result = "error"
	}
	m.actions.WithLabelValues(action, result).Inc()
}
