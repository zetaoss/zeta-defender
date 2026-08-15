package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestActivateAndDeactivateApplyConfiguredLevels(t *testing.T) {
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
	a, err := newWithBase("token", "zone", "medium", srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Activate(context.Background()); err != nil || level != "under_attack" {
		t.Fatalf("activate: level=%s err=%v", level, err)
	}
	if err := a.Activate(context.Background()); err != nil || patches != 1 {
		t.Fatalf("idempotent activate: patches=%d err=%v", patches, err)
	}
	if err := a.Deactivate(context.Background()); err != nil || level != "medium" {
		t.Fatalf("deactivate: level=%s err=%v", level, err)
	}
}

func TestInitializeStartupMode(t *testing.T) {
	tests := []struct {
		name         string
		initialLevel string
		mode         StartupMode
		wantLevel    string
		wantRequests int
		wantPatches  int
		wantOwned    bool
	}{
		{name: "preserve", initialLevel: "under_attack", mode: StartupModePreserve, wantLevel: "under_attack"},
		{name: "normal from fighting", initialLevel: "under_attack", mode: StartupModeNormal, wantLevel: "medium", wantRequests: 2, wantPatches: 1},
		{name: "normal from another level", initialLevel: "high", mode: StartupModeNormal, wantLevel: "medium", wantRequests: 2, wantPatches: 1},
		{name: "already normal", initialLevel: "medium", mode: StartupModeNormal, wantLevel: "medium", wantRequests: 1},
		{name: "fighting", initialLevel: "high", mode: StartupModeFighting, wantLevel: "under_attack", wantRequests: 2, wantPatches: 1, wantOwned: true},
		{name: "already fighting", initialLevel: "under_attack", mode: StartupModeFighting, wantLevel: "under_attack", wantRequests: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			level := tt.initialLevel
			requests := 0
			patches := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
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
					"success": true, "result": map[string]string{"value": level},
				})
			}))
			defer srv.Close()

			a, err := newWithBase("token", "zone", "medium", srv.URL, srv.Client())
			if err != nil {
				t.Fatal(err)
			}
			if err := a.Initialize(context.Background(), tt.mode); err != nil {
				t.Fatal(err)
			}
			if level != tt.wantLevel || requests != tt.wantRequests || patches != tt.wantPatches || a.owned != tt.wantOwned {
				t.Fatalf("level=%q requests=%d patches=%d owned=%t", level, requests, patches, a.owned)
			}
		})
	}
}

func TestInitializeRejectsInvalidStartupMode(t *testing.T) {
	a, err := newWithBase("token", "zone", "medium", "https://example.com", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Initialize(context.Background(), StartupMode("invalid")); err == nil {
		t.Fatal("expected error")
	}
}

func TestNewRejectsInvalidNormalSecurityLevel(t *testing.T) {
	if _, err := newWithBase("token", "zone", "under_attack", "https://example.com", http.DefaultClient); err == nil {
		t.Fatal("expected error")
	}
}

func TestSecurityLevelManagement(t *testing.T) {
	level := "medium"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPatch {
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
			"success": true, "result": map[string]string{"value": level},
		})
	}))
	defer srv.Close()

	a, _ := newWithBase("token", "zone", "medium", srv.URL, srv.Client())
	got, err := a.SecurityLevel(context.Background())
	if err != nil || got != "medium" {
		t.Fatalf("level=%q err=%v", got, err)
	}
	if err := a.SetSecurityLevel(context.Background(), "under_attack"); err != nil {
		t.Fatal(err)
	}
	if level != "under_attack" {
		t.Fatalf("level=%q", level)
	}
	if err := a.SetSecurityLevel(context.Background(), "invalid"); err == nil {
		t.Fatal("expected invalid level error")
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
	a, _ := newWithBase("token", "zone", "medium", srv.URL, srv.Client())
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

func TestDeactivatePreservesOutOfBandSecurityLevelChange(t *testing.T) {
	level := "high"
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

	a, _ := newWithBase("token", "zone", "medium", srv.URL, srv.Client())
	if err := a.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	level = "low"
	if err := a.Deactivate(context.Background()); err != nil {
		t.Fatal(err)
	}
	if level != "low" || patches != 1 || a.owned {
		t.Fatalf("level=%q patches=%d owned=%t", level, patches, a.owned)
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
	a, _ := newWithBase("token", "zone", "medium", srv.URL, srv.Client())
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
	a, _ := newWithBase("token", "zone", "medium", srv.URL, srv.Client())
	if err := a.Activate(context.Background()); err == nil {
		t.Fatal("expected activation error")
	}
	if requests != 2 || a.owned {
		t.Fatalf("requests=%d owned=%v", requests, a.owned)
	}
}
