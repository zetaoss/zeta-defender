package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Server               ServerConfig  `yaml:"server"`
	Metrics              MetricsConfig `yaml:"metrics"`
	Policy               PolicyConfig  `yaml:"policy"`
	Actions              ActionsConfig `yaml:"actions"`
	StatusInterval       time.Duration `yaml:"-"`
	StatusIntervalString string        `yaml:"statusInterval"`
}

type ServerConfig struct {
	Listen string `yaml:"listen"`
}

type MetricsConfig struct {
	Endpoint string        `yaml:"endpoint"`
	Expr     string        `yaml:"expr"`
	Interval time.Duration `yaml:"-"`
}

func (c *MetricsConfig) UnmarshalYAML(node *yaml.Node) error {
	var raw struct {
		Endpoint string `yaml:"endpoint"`
		Expr     string `yaml:"expr"`
		Interval string `yaml:"interval"`
	}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	d, err := time.ParseDuration(raw.Interval)
	if err != nil {
		return fmt.Errorf("metrics.interval: %w", err)
	}
	c.Endpoint, c.Expr, c.Interval = raw.Endpoint, raw.Expr, d
	return nil
}

type PolicyConfig struct {
	Arming   ArmingConfig   `yaml:"arming"`
	Fighting FightingConfig `yaml:"fighting"`
}

type ArmingConfig struct {
	Levels int `yaml:"levels"`
}

type FightingConfig struct {
	LevelDuration time.Duration `yaml:"-"`
	Levels        int           `yaml:"levels"`
}

func (c *FightingConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return errors.New("policy.fighting must be a mapping")
	}
	for i := 0; i < len(node.Content); i += 2 {
		switch node.Content[i].Value {
		case "levelDuration", "levels":
		default:
			return fmt.Errorf("unknown policy.fighting field %q", node.Content[i].Value)
		}
	}
	var raw struct {
		LevelDuration string `yaml:"levelDuration"`
		Levels        int    `yaml:"levels"`
	}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	d, err := time.ParseDuration(raw.LevelDuration)
	if err != nil {
		return fmt.Errorf("policy.fighting.levelDuration: %w", err)
	}
	c.LevelDuration, c.Levels = d, raw.Levels
	return nil
}

type ActionsConfig struct {
	Cloudflare *CloudflareConfig `yaml:"cloudflare"`
}

type CloudflareConfig struct {
	APIToken            string `yaml:"apiToken"`
	ZoneID              string `yaml:"zoneID"`
	NormalSecurityLevel string `yaml:"normalSecurityLevel"`
	StartupMode         string `yaml:"startupMode"`
}

const (
	DefaultNormalSecurityLevel = "essentially_off"
	StartupModePreserve        = "preserve"
	StartupModeNormal          = "normal"
	StartupModeFighting        = "fighting"
	maxPolicyLevels            = 99
)

const DefaultStatusInterval = 6 * time.Hour

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config: %w", err)
	}
	b = []byte(os.ExpandEnv(string(b)))
	cfg := Config{Server: ServerConfig{Listen: ":8080"}}
	dec := yaml.NewDecoder(strings.NewReader(string(b)))
	dec.KnownFields(true)
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	if cfg.StatusIntervalString == "" {
		cfg.StatusInterval = DefaultStatusInterval
	} else {
		d, err := time.ParseDuration(cfg.StatusIntervalString)
		if err != nil {
			return Config{}, fmt.Errorf("statusInterval: %w", err)
		}
		cfg.StatusInterval = d
	}
	if cfg.Actions.Cloudflare != nil {
		if cfg.Actions.Cloudflare.NormalSecurityLevel == "" {
			cfg.Actions.Cloudflare.NormalSecurityLevel = DefaultNormalSecurityLevel
		}
		if cfg.Actions.Cloudflare.StartupMode == "" {
			cfg.Actions.Cloudflare.StartupMode = StartupModePreserve
		}
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var errs []error
	if strings.TrimSpace(c.Server.Listen) == "" {
		errs = append(errs, errors.New("server.listen is required"))
	}
	if strings.TrimSpace(c.Metrics.Endpoint) == "" {
		errs = append(errs, errors.New("metrics.endpoint is required"))
	}
	if strings.TrimSpace(c.Metrics.Expr) == "" {
		errs = append(errs, errors.New("metrics.expr is required"))
	}
	if c.Metrics.Interval <= 0 {
		errs = append(errs, errors.New("metrics.interval must be positive"))
	}
	if c.StatusInterval <= 0 {
		errs = append(errs, errors.New("statusInterval must be positive"))
	}
	if c.Policy.Arming.Levels < 1 {
		errs = append(errs, errors.New("policy.arming.levels must be at least 1"))
	} else if c.Policy.Arming.Levels > maxPolicyLevels {
		errs = append(errs, fmt.Errorf("policy.arming.levels must be at most %d", maxPolicyLevels))
	}
	if c.Policy.Fighting.LevelDuration <= 0 {
		errs = append(errs, errors.New("policy.fighting.levelDuration must be positive"))
	}
	if c.Policy.Fighting.Levels < 1 {
		errs = append(errs, errors.New("policy.fighting.levels must be at least 1"))
	} else if c.Policy.Fighting.Levels > maxPolicyLevels {
		errs = append(errs, fmt.Errorf("policy.fighting.levels must be at most %d", maxPolicyLevels))
	}
	if c.Actions.Cloudflare == nil {
		errs = append(errs, errors.New("actions.cloudflare is required"))
	} else {
		if strings.TrimSpace(c.Actions.Cloudflare.APIToken) == "" {
			errs = append(errs, errors.New("actions.cloudflare.apiToken is required"))
		}
		if strings.TrimSpace(c.Actions.Cloudflare.ZoneID) == "" {
			errs = append(errs, errors.New("actions.cloudflare.zoneID is required"))
		}
		switch c.Actions.Cloudflare.NormalSecurityLevel {
		case "off", "essentially_off", "low", "medium", "high":
		default:
			errs = append(errs, errors.New("actions.cloudflare.normalSecurityLevel must be off, essentially_off, low, medium, or high"))
		}
		switch c.Actions.Cloudflare.StartupMode {
		case StartupModePreserve, StartupModeNormal, StartupModeFighting:
		default:
			errs = append(errs, errors.New("actions.cloudflare.startupMode must be preserve, normal, or fighting"))
		}
	}
	return errors.Join(errs...)
}
