package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	cloudflareaction "github.com/zetaoss/zeta-defender/internal/action/cloudflare"
	"github.com/zetaoss/zeta-defender/internal/config"
	"github.com/zetaoss/zeta-defender/internal/defender"
	prometheusmetrics "github.com/zetaoss/zeta-defender/internal/metrics/prometheus"
	"github.com/zetaoss/zeta-defender/internal/server"
	"github.com/zetaoss/zeta-defender/internal/telemetry"
)

const initializationTimeout = 15 * time.Second

var (
	version           = "dev"
	defaultConfigPath = "config.yaml"
)

func main() {
	var configPath string
	flag.StringVar(&configPath, "config", defaultConfigPath, "path to the YAML configuration file")
	flag.StringVar(&configPath, "c", defaultConfigPath, "shorthand for --config")
	logFormat := flag.String("log-format", "text", "log format: text or json")
	showVersion := flag.Bool("version", false, "display version and exit")
	flag.Usage = func() {
		printUsage(flag.CommandLine.Output(), defaultConfigPath)
	}
	flag.Parse()

	if *showVersion {
		_, _ = fmt.Fprintln(os.Stdout, "defender", version)
		return
	}

	logger, err := newLogger(*logFormat, os.Stdout)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load(configPath)
	if err != nil {
		logger.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	httpClient := &http.Client{Timeout: 15 * time.Second}
	provider, err := prometheusmetrics.New(cfg.Metrics.Endpoint, cfg.Metrics.Expr, httpClient)
	if err != nil {
		logger.Error("failed to create metrics provider", "error", err)
		os.Exit(1)
	}
	cf := cfg.Actions.Cloudflare
	act, err := cloudflareaction.New(cf.APIToken, cf.ZoneID, cf.NormalSecurityLevel, httpClient)
	if err != nil {
		logger.Error("failed to create Cloudflare action", "error", err)
		os.Exit(1)
	}
	initializationCtx, cancelInitialization := context.WithTimeout(signalCtx, initializationTimeout)
	err = act.Initialize(initializationCtx, cloudflareaction.StartupMode(cf.StartupMode))
	cancelInitialization()
	if err != nil {
		logger.Error("failed to apply startup mode", "mode", cf.StartupMode, "error", err)
		os.Exit(1)
	}
	logger.Info("startup mode applied", "mode", cf.StartupMode)
	exporter := telemetry.New()
	controllerOptions := []defender.Option{defender.WithObserver(exporter)}
	if cf.StartupMode == config.StartupModeFighting {
		controllerOptions = append(controllerOptions, defender.WithInitialFighting())
	}
	controller, err := defender.New(provider, act, defender.Policy{
		ArmingLevels:          cfg.Policy.Arming.Levels,
		FightingLevelDuration: cfg.Policy.Fighting.LevelDuration,
		FightingLevels:        cfg.Policy.Fighting.Levels,
	}, cfg.Metrics.Interval, cfg.StatusInterval, logger, controllerOptions...)
	if err != nil {
		logger.Error("failed to create controller", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(signalCtx)
	defer cancel()

	httpServer := server.New(cfg.Server.Listen, exporter.Handler(), logger)
	type runResult struct {
		component string
		err       error
	}
	results := make(chan runResult, 2)
	go func() { results <- runResult{"controller", controller.Run(ctx)} }()
	go func() { results <- runResult{"http server", httpServer.Run(ctx)} }()

	logger.Info("zeta-defender started", "version", version, "config", configPath, "listen", cfg.Server.Listen)
	first := <-results
	cancel()
	second := <-results
	if first.err != nil {
		logger.Error("component stopped with an error", "component", first.component, "error", first.err)
		os.Exit(1)
	}
	if second.err != nil {
		logger.Error("component stopped with an error", "component", second.component, "error", second.err)
		os.Exit(1)
	}
	logger.Info("zeta-defender stopped")
}

func printUsage(w io.Writer, configPath string) {
	_, _ = fmt.Fprintf(w, `Usage: zeta-defender [options]

Options:
  -c, --config string
        path to the YAML configuration file (default %q)
      --log-format string
        log format: text or json (default "text")
      --version
        display version and exit
  -h, --help
        display this help and exit
`, configPath)
}

func newLogger(format string, output io.Writer) (*slog.Logger, error) {
	switch format {
	case "text":
		return slog.New(slog.NewTextHandler(output, nil)), nil
	case "json":
		return slog.New(slog.NewJSONHandler(output, nil)), nil
	default:
		return nil, fmt.Errorf("invalid log format %q: must be text or json", format)
	}
}
