package telemetry

import (
	"io"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/zetaoss/zeta-defender/internal/defender"
)

func TestLevelEncoding(t *testing.T) {
	m := New()
	tests := []struct {
		name          string
		state         defender.State
		armingChecks  int
		fightingLevel int
		want          float64
	}{
		{"normal", defender.Normal, 0, 1, 0},
		{"arming entered", defender.Arming, 0, 1, 1},
		{"arming progress", defender.Arming, 4, 1, 5},
		{"arming boundary", defender.Arming, 200, 1, 99},
		{"fighting level 1", defender.Fighting, 0, 1, 101},
		{"fighting level 2", defender.Fighting, 0, 2, 102},
		{"fighting level N", defender.Fighting, 0, 12, 112},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m.ObserveLevel(tt.state, tt.armingChecks, tt.fightingLevel)
			if got := testutil.ToFloat64(m.level); got != tt.want {
				t.Fatalf("level=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestFightingSecondsIncludesCurrentAndCompletedPeriods(t *testing.T) {
	now := time.Unix(100, 0)
	m := newWithNow(func() time.Time { return now })

	m.ObserveLevel(defender.Fighting, 0, 1)
	now = now.Add(30 * time.Second)
	if got := testutil.ToFloat64(m.fightingSeconds); got != 30 {
		t.Fatalf("active fighting seconds=%v, want 30", got)
	}

	m.ObserveLevel(defender.Arming, 0, 1)
	now = now.Add(20 * time.Second)
	if got := testutil.ToFloat64(m.fightingSeconds); got != 30 {
		t.Fatalf("completed fighting seconds=%v, want 30", got)
	}

	m.ObserveLevel(defender.Fighting, 0, 2)
	now = now.Add(15 * time.Second)
	if got := testutil.ToFloat64(m.fightingSeconds); got != 45 {
		t.Fatalf("accumulated fighting seconds=%v, want 45", got)
	}
}

func TestMetricsExposition(t *testing.T) {
	m := New()
	m.ObserveLevel(defender.Fighting, 0, 3)
	req := httptest.NewRequest("GET", "/metrics", nil)
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status=%d", rec.Code)
	}
	body := rec.Body.String()
	for _, expected := range []string{
		"# HELP zeta_defender_level ",
		"# TYPE zeta_defender_level gauge",
		"zeta_defender_level 103",
		"# TYPE zeta_defender_fighting_seconds_total counter",
		"zeta_defender_fighting_seconds_total",
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("exposition does not contain %q", expected)
		}
	}
	for _, unexpected := range []string{
		"zeta_defender_evaluations_total",
		"zeta_defender_evaluation_errors_total",
		"zeta_defender_actions_total",
	} {
		if strings.Contains(body, unexpected) {
			t.Errorf("exposition contains removed metric %q", unexpected)
		}
	}
}

func TestConcurrentScrapeAndUpdates(t *testing.T) {
	m := New()
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(2)
		go func() {
			defer wg.Done()
			for level := 1; level <= 500; level++ {
				m.ObserveLevel(defender.Fighting, 0, level)
			}
		}()
		go func() {
			defer wg.Done()
			for range 100 {
				rec := httptest.NewRecorder()
				m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
				_, _ = io.Copy(io.Discard, rec.Result().Body)
				_ = rec.Result().Body.Close()
			}
		}()
	}
	wg.Wait()
}
