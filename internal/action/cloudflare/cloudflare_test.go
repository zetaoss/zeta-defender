package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestActivateAndDeactivateRestorePreviousLevel(t *testing.T) {
	level := "high"
	var patches int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Error("missing bearer token")
		}
		if r.URL.Path != "/zones/zone/settings/security_level" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if r.Method == http.MethodPatch {
			patches++
			var body struct {
				Value string `json:"value"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
				return
			}
			level = body.Value
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true,
			"result":  map[string]string{"value": level},
		})
	}))
	defer srv.Close()
	a, err := newWithBase("token", "zone", srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Activate(context.Background()); err != nil || level != "under_attack" {
		t.Fatalf("activate: level=%s err=%v", level, err)
	}
	if err := a.Activate(context.Background()); err != nil || patches != 1 {
		t.Fatalf("idempotent activate: patches=%d err=%v", patches, err)
	}
	if err := a.Deactivate(context.Background()); err != nil || level != "high" {
		t.Fatalf("deactivate: level=%s err=%v", level, err)
	}
}

func TestExistingUnderAttackModeIsNotOwnedOrDeactivated(t *testing.T) {
	level := "under_attack"
	patches := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			patches++
			var body struct {
				Value string `json:"value"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			level = body.Value
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "result": map[string]string{"value": level},
		})
	}))
	defer srv.Close()
	a, _ := newWithBase("token", "zone", srv.URL, srv.Client())
	if err := a.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if a.owned {
		t.Fatal("pre-existing Under Attack Mode must not be owned")
	}
	if err := a.Deactivate(context.Background()); err != nil || level != "under_attack" || patches != 0 {
		t.Fatalf("level=%s patches=%d err=%v", level, patches, err)
	}
}

func TestDeactivateDoesNotChangeAnInactiveLevel(t *testing.T) {
	patches := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
			patches++
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "result": map[string]string{"value": "high"},
		})
	}))
	defer srv.Close()
	a, _ := newWithBase("token", "zone", srv.URL, srv.Client())
	if err := a.Deactivate(context.Background()); err != nil || patches != 0 {
		t.Fatalf("err=%v patches=%d", err, patches)
	}
}

func TestActivateDoesNotClaimOwnershipWhenPatchResponseFails(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method == http.MethodGet {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"success": true, "result": map[string]string{"value": "high"},
			})
			return
		}
		http.Error(w, `{"success":false}`, http.StatusBadGateway)
	}))
	defer srv.Close()
	a, _ := newWithBase("token", "zone", srv.URL, srv.Client())
	if err := a.Activate(context.Background()); err == nil {
		t.Fatal("expected activation error")
	}
	if requests != 2 || a.previousLevel != "" || a.owned {
		t.Fatalf("requests=%d previousLevel=%q owned=%v", requests, a.previousLevel, a.owned)
	}
}
