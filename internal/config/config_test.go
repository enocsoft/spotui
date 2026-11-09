package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	return path
}

func TestLoadFromMissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.toml")
	_, err := LoadFrom(path)
	if err == nil {
		t.Fatal("expected error for missing config file, got nil")
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q should name the missing path %q", err, path)
	}
	if !strings.Contains(err.Error(), "client_id") {
		t.Errorf("error %q should tell the user what to put in the file", err)
	}
}

func TestLoadFromMissingClientID(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.toml", "# spotify-tui\ntheme = \"dark\"\n")
	_, err := LoadFrom(path)
	if err == nil {
		t.Fatal("expected error for missing client_id, got nil")
	}
	if !strings.Contains(err.Error(), "client_id") {
		t.Errorf("error %q should name client_id", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q should name the file %q", err, path)
	}
}

func TestLoadFromValid(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.toml",
		"client_id = \"abc123\"\noutput = \"Speakers\"\n")
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.ClientID != "abc123" {
		t.Errorf("ClientID = %q, want %q", cfg.ClientID, "abc123")
	}
	if cfg.Output != "Speakers" {
		t.Errorf("Output = %q, want %q", cfg.Output, "Speakers")
	}
}

func TestLoadFromDefaults(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.toml", "client_id = \"abc123\"\n")
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.Output != "" {
		t.Errorf("Output default = %q, want empty (system default)", cfg.Output)
	}
}

func TestLoadFromMalformedLine(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.toml", "client_id = \"ok\"\ngarbage line\n")
	_, err := LoadFrom(path)
	if err == nil {
		t.Fatal("expected error for malformed line, got nil")
	}
	if !strings.Contains(err.Error(), "line 2") {
		t.Errorf("error %q should point at line 2", err)
	}
	if !strings.Contains(err.Error(), path) {
		t.Errorf("error %q should name the file", err)
	}
}

func TestDefaultPathUsesXDG(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	got := DefaultPath()
	want := filepath.Join(dir, "spotify-tui", "config.toml")
	if got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}
