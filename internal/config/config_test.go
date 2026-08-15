package config

import (
	"os"
	"path/filepath"
	"strings"
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
  arming:
    levels: 2
  fighting:
    levelDuration: 5m
    levels: 4
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
	if cfg.Metrics.Interval != 30*time.Second || cfg.Policy.Fighting.LevelDuration != 5*time.Minute {
		t.Fatalf("durations not parsed: %+v", cfg)
	}
	if cfg.Policy.Arming.Levels != 2 || cfg.Policy.Fighting.Levels != 4 {
		t.Fatalf("policy levels not parsed: %+v", cfg.Policy)
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

func TestValidateRejectsLevelsAboveMetricRange(t *testing.T) {
	tests := []struct {
		name string
		edit func(*Config)
		want string
	}{
		{"arming", func(c *Config) { c.Policy.Arming.Levels = maxPolicyLevels + 1 }, "arming.levels must be at most 99"},
		{"fighting", func(c *Config) { c.Policy.Fighting.Levels = maxPolicyLevels + 1 }, "fighting.levels must be at most 99"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.edit(&cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q error, got %v", tt.want, err)
			}
		})
	}
}

func TestValidateRejectsInvalidCloudflarePolicy(t *testing.T) {
	tests := []CloudflareConfig{
		{APIToken: "token", NormalSecurityLevel: "under_attack", StartupMode: StartupModePreserve, ZoneID: "zone"},
		{APIToken: "token", NormalSecurityLevel: "medium", StartupMode: "invalid", ZoneID: "zone"},
	}
	for _, cloudflare := range tests {
		cfg := validConfig()
		cfg.Actions.Cloudflare = &cloudflare
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
  arming:
    levels: 2
  fighting:
    levelDuration: 5m
    levels: 4
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

func TestLoadRejectsLegacyPolicyFields(t *testing.T) {
	tests := []string{
		"  armingChecks: 2\n  fighting:\n    levelDuration: 5m\n    levels: 4",
		"  arming:\n    levels: 2\n  fighting:\n    baseDuration: 5m\n    levels: 4",
		"  arming:\n    levels: 2\n  fighting:\n    levelDuration: 5m\n    maxLevel: 4",
	}
	for _, policy := range tests {
		path := filepath.Join(t.TempDir(), "config.yaml")
		data := []byte("metrics:\n  endpoint: http://prometheus:9090\n  interval: 30s\n  expr: up\npolicy:\n" + policy + "\nactions:\n  cloudflare:\n    apiToken: token\n    zoneID: zone\n")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := Load(path); err == nil {
			t.Fatalf("expected legacy policy to be rejected:\n%s", policy)
		}
	}
}

func validConfig() Config {
	return Config{
		Server:  ServerConfig{Listen: ":8080"},
		Metrics: MetricsConfig{Endpoint: "http://prometheus:9090", Expr: "up", Interval: time.Minute},
		Policy: PolicyConfig{
			Arming:   ArmingConfig{Levels: 1},
			Fighting: FightingConfig{LevelDuration: time.Minute, Levels: 1},
		},
		Actions: ActionsConfig{Cloudflare: &CloudflareConfig{
			APIToken:            "token",
			ZoneID:              "zone",
			NormalSecurityLevel: DefaultNormalSecurityLevel,
			StartupMode:         StartupModePreserve,
		}},
	}
}
