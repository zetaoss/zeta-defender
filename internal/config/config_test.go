package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadExpandsEnvironmentAndParsesDurations(t *testing.T) {
	t.Setenv("TEST_CF_TOKEN", "secret-value")
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte(`
metrics:
  endpoint: http://prometheus:9090
  interval: 30s
  expr: up
policy:
  armingChecks: 2
  fighting:
    baseDuration: 5m
    maxLevel: 4
actions:
  cloudflare:
    apiToken: ${TEST_CF_TOKEN}
    zoneID: zone
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Metrics.Interval != 30*time.Second || cfg.Policy.Fighting.BaseDuration != 5*time.Minute {
		t.Fatalf("durations not parsed: %+v", cfg)
	}
	if cfg.Actions.Cloudflare.APIToken != "secret-value" {
		t.Fatal("environment variable was not expanded")
	}
	if cfg.Actions.Cloudflare.NormalSecurityLevel != DefaultNormalSecurityLevel {
		t.Fatalf("default normal security level=%q", cfg.Actions.Cloudflare.NormalSecurityLevel)
	}
	if cfg.Actions.Cloudflare.StartupMode != StartupModePreserve {
		t.Fatalf("default startup mode=%q", cfg.Actions.Cloudflare.StartupMode)
	}
	if cfg.Server.Listen != ":8080" {
		t.Fatalf("default server listen=%q", cfg.Server.Listen)
	}
}

func TestValidateRejectsInvalidPolicy(t *testing.T) {
	cfg := Config{Actions: ActionsConfig{Cloudflare: &CloudflareConfig{APIToken: "x", ZoneID: "z"}}}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestValidateRejectsInvalidCloudflarePolicy(t *testing.T) {
	tests := []CloudflareConfig{
		{APIToken: "token", NormalSecurityLevel: "under_attack", StartupMode: StartupModePreserve, ZoneID: "zone"},
		{APIToken: "token", NormalSecurityLevel: "medium", StartupMode: "invalid", ZoneID: "zone"},
	}
	for _, cloudflare := range tests {
		cfg := Config{
			Server:  ServerConfig{Listen: ":8080"},
			Metrics: MetricsConfig{Endpoint: "http://prometheus:9090", Expr: "up", Interval: time.Minute},
			Policy: PolicyConfig{
				ArmingChecks: 1,
				Fighting:     FightingConfig{BaseDuration: time.Minute, MaxLevel: 1},
			},
			Actions: ActionsConfig{Cloudflare: &cloudflare},
		}
		if err := cfg.Validate(); err == nil {
			t.Fatalf("expected validation error for %+v", cloudflare)
		}
	}
}

func TestLoadRejectsLegacyInitialUAM(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	data := []byte(`
metrics:
  endpoint: http://prometheus:9090
  interval: 30s
  expr: up
policy:
  armingChecks: 2
  fighting:
    baseDuration: 5m
    maxLevel: 4
actions:
  cloudflare:
    apiToken: token
    zoneID: zone
    initialUAM: preserve
`)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected legacy initialUAM to be rejected")
	}
}
