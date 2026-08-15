package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	cloudflareaction "github.com/zetaoss/zeta-defender/internal/action/cloudflare"
	"github.com/zetaoss/zeta-defender/internal/config"
)

const requestTimeout = 15 * time.Second

var version = "dev"

type securityLevelService interface {
	SecurityLevel(context.Context) (string, error)
}

type dependencies struct {
	loadConfig func(string) (config.Config, error)
	newService func(config.CloudflareConfig, *http.Client) (securityLevelService, error)
}

var defaultDependencies = dependencies{
	loadConfig: config.Load,
	newService: func(cf config.CloudflareConfig, client *http.Client) (securityLevelService, error) {
		return cloudflareaction.New(cf.APIToken, cf.ZoneID, cf.NormalSecurityLevel, client)
	},
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, defaultDependencies))
}

func run(args []string, stdout, stderr io.Writer, deps dependencies) int {
	flags := flag.NewFlagSet("defendertool", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", "config.yaml", "path to the YAML configuration file")
	flags.Usage = func() {
		_, _ = fmt.Fprintln(stderr, "Usage: defendertool [-config path] status")
		_, _ = fmt.Fprintln(stderr, "       defendertool version")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		return 2
	}

	commandArgs := flags.Args()
	if len(commandArgs) == 0 {
		flags.Usage()
		return 2
	}
	command := commandArgs[0]
	if (command == "status" || command == "version") && len(commandArgs) != 1 {
		flags.Usage()
		return 2
	}
	if command != "status" && command != "version" {
		_, _ = fmt.Fprintf(stderr, "unknown command %q\n", command)
		flags.Usage()
		return 2
	}
	if command == "version" {
		_, _ = fmt.Fprintln(stdout, "defendertool", version)
		return 0
	}

	cfg, err := deps.loadConfig(*configPath)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "load configuration: %v\n", err)
		return 1
	}
	cf := cfg.Actions.Cloudflare
	if cf == nil {
		_, _ = fmt.Fprintln(stderr, "load configuration: actions.cloudflare is required")
		return 1
	}
	client := &http.Client{Timeout: requestTimeout}
	service, err := deps.newService(*cf, client)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "create Cloudflare client: %v\n", err)
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
	defer cancel()

	level, err := service.SecurityLevel(ctx)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "get security level: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintln(stdout, level)
	return 0
}
