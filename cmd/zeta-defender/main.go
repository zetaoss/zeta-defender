package main

import (
	"context"
	"flag"
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

func main() {
	configPath := flag.String("config", "config.yaml", "path to the YAML configuration file")
	flag.Parse()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load(*configPath)
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
	act, err := cloudflareaction.New(cf.APIToken, cf.ZoneID, httpClient)
	if err != nil {
		logger.Error("failed to create Cloudflare action", "error", err)
		os.Exit(1)
	}
	exporter := telemetry.New()
	controller, err := defender.New(provider, act, defender.Policy{
		ArmingChecks: cfg.Policy.ArmingChecks,
		BaseDuration: cfg.Policy.Fighting.BaseDuration,
		MaxLevel:     cfg.Policy.Fighting.MaxLevel,
	}, cfg.Metrics.Interval, logger, defender.WithObserver(exporter))
	if err != nil {
		logger.Error("failed to create controller", "error", err)
		os.Exit(1)
	}

	signalCtx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
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

	logger.Info("zeta-defender started", "config", *configPath, "listen", cfg.Server.Listen)
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
