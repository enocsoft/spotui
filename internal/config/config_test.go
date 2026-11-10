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
	want := filepath.Join(dir, "spotui", "config.toml")
	if got != want {
		t.Errorf("DefaultPath() = %q, want %q", got, want)
	}
}

func TestLoadFromCallbackPortDefault(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.toml", "client_id = \"abc123\"\n")
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.CallbackPort != DefaultCallbackPort {
		t.Errorf("CallbackPort default = %d, want %d", cfg.CallbackPort, DefaultCallbackPort)
	}
}

func TestLoadFromCallbackPortCustom(t *testing.T) {
	path := writeFile(t, t.TempDir(), "config.toml",
		"client_id = \"abc123\"\ncallback_port = \"9999\"\n")
	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.CallbackPort != 9999 {
		t.Errorf("CallbackPort = %d, want 9999", cfg.CallbackPort)
	}
}

func TestLoadFromCallbackPortInvalid(t *testing.T) {
	for _, bad := range []string{"0", "65536", "-1", "abc", "\"\""} {
		path := writeFile(t, t.TempDir(), "config.toml",
			"client_id = \"abc123\"\ncallback_port = \""+bad+"\"\n")
		_, err := LoadFrom(path)
		if err == nil {
			t.Errorf("callback_port = %s: expected error, got nil", bad)
			continue
		}
		if !strings.Contains(err.Error(), "callback_port") {
			t.Errorf("callback_port = %s: error %q should name the key", bad, err)
		}
		if !strings.Contains(err.Error(), path) {
			t.Errorf("callback_port = %s: error %q should name the file", bad, err)
		}
	}
}

func TestStateDirUsesXDG(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	got := StateDir()
	want := filepath.Join(dir, "spotui")
	if got != want {
		t.Errorf("StateDir() = %q, want %q", got, want)
	}
}

// TestLoadFallsBackToLegacyConfigDir: after the spotui rename an existing
// install still has its client_id under ~/.config/spotify-tui. When the new
// spotui config dir does not exist and the legacy one does, the legacy file
// is read and the fallback is reported so main can print one notice line.
func TestLoadFallsBackToLegacyConfigDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	legacyDir := filepath.Join(dir, "spotify-tui")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("mkdir legacy dir: %v", err)
	}
	legacyPath := writeFile(t, legacyDir, "config.toml", "client_id = \"abc123\"\n")
	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load with only the legacy config dir present: %v", err)
	}
	if cfg.ClientID != "abc123" {
		t.Errorf("ClientID = %q, want %q read from the legacy file", cfg.ClientID, "abc123")
	}
	if got := LegacyConfigPath(); got != legacyPath {
		t.Errorf("LegacyConfigPath() = %q, want the legacy file %q actually used", got, legacyPath)
	}
}

// TestDefaultPathPrefersNewDirWhenBothExist: the compatibility path is one-time.
// Once the spotui dir exists it wins, even if the legacy dir is still around.
func TestDefaultPathPrefersNewDirWhenBothExist(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	for _, name := range []string{"spotui", "spotify-tui"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	writeFile(t, filepath.Join(dir, "spotify-tui"), "config.toml", "client_id = \"legacy\"\n")
	_, err := Load()
	if err == nil {
		t.Fatal("expected the missing spotui config file error, got nil")
	}
	if !strings.Contains(err.Error(), filepath.Join(dir, "spotui", "config.toml")) {
		t.Errorf("error %q should name the new spotui path, not the legacy one", err)
	}
	if got := LegacyConfigPath(); got != "" {
		t.Errorf("LegacyConfigPath() = %q, want empty when the new dir exists", got)
	}
}

// TestStateDirFallsBackToLegacyStateDir: token caches live in the state dir.
// When the new spotui state dir does not exist and the legacy one does, the
// legacy dir is used so an existing token cache keeps working.
func TestStateDirFallsBackToLegacyStateDir(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	legacyDir := filepath.Join(dir, "spotify-tui")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("mkdir legacy dir: %v", err)
	}
	if got := StateDir(); got != legacyDir {
		t.Errorf("StateDir() = %q, want the legacy dir %q", got, legacyDir)
	}
	if got := LegacyStateDir(); got != legacyDir {
		t.Errorf("LegacyStateDir() = %q, want the legacy dir actually used", got)
	}
}

// TestStateDirPrefersNewWhenBothExist: the new spotui state dir wins once it
// exists; the legacy fallback never resurrects old state over new state.
func TestStateDirPrefersNewWhenBothExist(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	for _, name := range []string{"spotui", "spotify-tui"} {
		if err := os.MkdirAll(filepath.Join(dir, name), 0o700); err != nil {
			t.Fatalf("mkdir %s: %v", name, err)
		}
	}
	want := filepath.Join(dir, "spotui")
	if got := StateDir(); got != want {
		t.Errorf("StateDir() = %q, want the new dir %q", got, want)
	}
	if got := LegacyStateDir(); got != "" {
		t.Errorf("LegacyStateDir() = %q, want empty when the new dir exists", got)
	}
}
