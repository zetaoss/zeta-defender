package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestNewLogger(t *testing.T) {
	t.Run("text", func(t *testing.T) {
		var output bytes.Buffer
		logger, err := newLogger("text", &output)
		if err != nil {
			t.Fatal(err)
		}
		logger.Info("started", "component", "controller")
		if got := output.String(); !strings.Contains(got, `msg=started component=controller`) {
			t.Fatalf("unexpected text log: %s", got)
		}
	})

	t.Run("json", func(t *testing.T) {
		var output bytes.Buffer
		logger, err := newLogger("json", &output)
		if err != nil {
			t.Fatal(err)
		}
		logger.Info("started", "component", "controller")
		if !json.Valid(output.Bytes()) {
			t.Fatalf("invalid JSON log: %s", output.String())
		}
	})

	if _, err := newLogger("invalid", &bytes.Buffer{}); err == nil {
		t.Fatal("expected invalid format error")
	}
}
