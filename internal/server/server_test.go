package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEndpoints(t *testing.T) {
	metrics := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("test_metric 1\n"))
	})
	srv := New(":8080", metrics, nil)

	for _, tt := range []struct {
		path string
		body string
	}{
		{"/metrics", "test_metric 1"},
		{"/healthz", "ok"},
	} {
		rec := httptest.NewRecorder()
		srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", tt.path, nil))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), tt.body) {
			t.Fatalf("%s: status=%d body=%q", tt.path, rec.Code, rec.Body.String())
		}
	}
}

func TestRunShutsDownOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	srv := New(":0", http.NotFoundHandler(), nil)
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("server did not shut down")
	}
}
