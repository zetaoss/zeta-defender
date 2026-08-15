package main

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/zetaoss/zeta-defender/internal/config"
)

type fakeSecurityLevelService struct {
	level  string
	getErr error
	setErr error
}

func (f *fakeSecurityLevelService) SecurityLevel(context.Context) (string, error) {
	return f.level, f.getErr
}

func (f *fakeSecurityLevelService) SetSecurityLevel(_ context.Context, level string) error {
	if f.setErr == nil {
		f.level = level
	}
	return f.setErr
}

func testDependencies(service securityLevelService) dependencies {
	return dependencies{
		loadConfig: func(string) (config.Config, error) {
			return config.Config{Actions: config.ActionsConfig{Cloudflare: &config.CloudflareConfig{}}}, nil
		},
		newService: func(config.CloudflareConfig, *http.Client) (securityLevelService, error) {
			return service, nil
		},
	}
}

func TestRunGet(t *testing.T) {
	service := &fakeSecurityLevelService{level: "medium"}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-config", "custom.yaml", "get"}, &stdout, &stderr, testDependencies(service)); code != 0 {
		t.Fatalf("exit code=%d stderr=%s", code, stderr.String())
	}
	if stdout.String() != "medium\n" {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestRunVersion(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := run([]string{"-version"}, &stdout, &stderr, testDependencies(nil)); code != 0 {
		t.Fatalf("exit code=%d stderr=%s", code, stderr.String())
	}
	if stdout.String() != "defenderctl dev\n" {
		t.Fatalf("stdout=%q", stdout.String())
	}
}

func TestRunSet(t *testing.T) {
	service := &fakeSecurityLevelService{level: "medium"}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"set", "under_attack"}, &stdout, &stderr, testDependencies(service)); code != 0 {
		t.Fatalf("exit code=%d stderr=%s", code, stderr.String())
	}
	if service.level != "under_attack" || stdout.String() != "under_attack\n" {
		t.Fatalf("level=%q stdout=%q", service.level, stdout.String())
	}
}

func TestRunRejectsInvalidArguments(t *testing.T) {
	for _, args := range [][]string{nil, {"unknown"}, {"get", "extra"}, {"set"}} {
		var stdout, stderr bytes.Buffer
		if code := run(args, &stdout, &stderr, testDependencies(&fakeSecurityLevelService{})); code != 2 {
			t.Fatalf("args=%v exit code=%d", args, code)
		}
		if !strings.Contains(stderr.String(), "Usage:") {
			t.Fatalf("args=%v stderr=%q", args, stderr.String())
		}
	}
}

func TestRunReportsServiceErrors(t *testing.T) {
	service := &fakeSecurityLevelService{getErr: errors.New("API unavailable")}
	var stdout, stderr bytes.Buffer
	if code := run([]string{"get"}, &stdout, &stderr, testDependencies(service)); code != 1 {
		t.Fatalf("exit code=%d", code)
	}
	if !strings.Contains(stderr.String(), "get security level: API unavailable") {
		t.Fatalf("stderr=%q", stderr.String())
	}
}
