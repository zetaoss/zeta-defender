package prometheus

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEvaluateBooleanValues(t *testing.T) {
	tests := []struct {
		name   string
		result string
		want   bool
	}{
		{"vector zero", `{"resultType":"vector","result":[{"metric":{},"value":[1,"0"]}]}`, false},
		{"vector one", `{"resultType":"vector","result":[{"metric":{},"value":[1,"1"]}]}`, true},
		{"vector any true", `{"resultType":"vector","result":[{"metric":{},"value":[1,"0"]},{"metric":{},"value":[1,"1"]}]}`, true},
		{"empty vector", `{"resultType":"vector","result":[]}`, false},
		{"scalar zero", `{"resultType":"scalar","result":[1,"0"]}`, false},
		{"scalar non-zero", `{"resultType":"scalar","result":[1,"2.5"]}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			const expr = `up >= bool 1`
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/query" || r.URL.Query().Get("query") != expr {
					t.Errorf("unexpected request: %s query=%q", r.URL.Path, r.URL.Query().Get("query"))
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"status":"success","data":%s}`, tt.result)
			}))
			defer srv.Close()
			provider, err := New(srv.URL, expr, srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			got, err := provider.Evaluate(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("Evaluate()=%v, want %v", got, tt.want)
			}
		})
	}
}

func TestEvaluateRejectsNonFiniteAndRangeResults(t *testing.T) {
	for _, data := range []string{
		`{"resultType":"vector","result":[{"metric":{},"value":[1,"NaN"]}]}`,
		`{"resultType":"matrix","result":[]}`,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			fmt.Fprintf(w, `{"status":"success","data":%s}`, data)
		}))
		provider, err := New(srv.URL, "up", srv.Client())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := provider.Evaluate(context.Background()); err == nil {
			t.Fatal("expected invalid result error")
		}
		srv.Close()
	}
}
