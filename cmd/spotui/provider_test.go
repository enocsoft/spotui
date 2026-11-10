package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/enocsoft/spotui/internal/config"
)

func TestProviderStoresAValidClientID(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out bytes.Buffer
	err := runProvider(strings.NewReader("0123456789abcdef0123456789abcdef\n"), &out)
	if err != nil {
		t.Fatalf("runProvider: %v (out: %s)", err, out.String())
	}
	if !strings.Contains(out.String(), "saved to") {
		t.Errorf("the user must be told where it went, got: %s", out.String())
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ClientID != "0123456789abcdef0123456789abcdef" {
		t.Errorf("client id = %q", cfg.ClientID)
	}
}

func TestProviderRejectsGarbage(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out bytes.Buffer
	err := runProvider(strings.NewReader("not-a-client-id\n"), &out)
	if err == nil {
		t.Fatal("garbage must be rejected")
	}
	if !strings.Contains(err.Error(), "32 hex") {
		t.Errorf("the error should say what was expected: %v", err)
	}
}

func TestProviderKeepsTheCurrentIDOnEmptyInput(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	var out bytes.Buffer
	if err := runProvider(strings.NewReader("0123456789abcdef0123456789abcdef\n"), &out); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if err := runProvider(strings.NewReader("\n"), &out); err != nil {
		t.Fatalf("empty input with an existing id must keep it: %v", err)
	}
	if !strings.Contains(out.String(), "kept the current client id") {
		t.Errorf("expected the keep message, got: %s", out.String())
	}
}
