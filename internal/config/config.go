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
	Server  ServerConfig  `yaml:"server"`
	Metrics MetricsConfig `yaml:"metrics"`
	Policy  PolicyConfig  `yaml:"policy"`
	Actions ActionsConfig `yaml:"actions"`
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
	ArmingChecks int            `yaml:"armingChecks"`
	Fighting     FightingConfig `yaml:"fighting"`
}

type FightingConfig struct {
	BaseDuration time.Duration `yaml:"-"`
	MaxLevel     int           `yaml:"maxLevel"`
}

func (c *FightingConfig) UnmarshalYAML(node *yaml.Node) error {
	var raw struct {
		BaseDuration string `yaml:"baseDuration"`
		MaxLevel     int    `yaml:"maxLevel"`
	}
	if err := node.Decode(&raw); err != nil {
		return err
	}
	d, err := time.ParseDuration(raw.BaseDuration)
	if err != nil {
		return fmt.Errorf("policy.fighting.baseDuration: %w", err)
	}
	c.BaseDuration, c.MaxLevel = d, raw.MaxLevel
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
	DefaultNormalSecurityLevel = "medium"
	StartupModePreserve        = "preserve"
	StartupModeNormal          = "normal"
	StartupModeFighting        = "fighting"
)

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
	if c.Policy.ArmingChecks <= 0 {
		errs = append(errs, errors.New("policy.armingChecks must be positive"))
	}
	if c.Policy.Fighting.BaseDuration <= 0 {
		errs = append(errs, errors.New("policy.fighting.baseDuration must be positive"))
	}
	if c.Policy.Fighting.MaxLevel < 1 {
		errs = append(errs, errors.New("policy.fighting.maxLevel must be at least 1"))
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
