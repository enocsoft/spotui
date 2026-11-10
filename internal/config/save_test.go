package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSaveClientIDKeepsTheOtherKeys: the provider flow must not clobber a config
// the user already tuned.
func TestSaveClientIDKeepsTheOtherKeys(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	dir := ConfigDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("client_id = \"old\"\ncallback_port = 9000\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := SaveClientID("0123456789abcdef0123456789abcdef")
	if err != nil {
		t.Fatalf("SaveClientID: %v", err)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, `callback_port = 9000`) {
		t.Errorf("other keys must survive, got:\n%s", text)
	}
	if !strings.Contains(text, `client_id = "0123456789abcdef0123456789abcdef"`) {
		t.Errorf("client_id not written, got:\n%s", text)
	}
	if strings.Count(text, "client_id") != 1 {
		t.Errorf("client_id duplicated, got:\n%s", text)
	}
	if info, err := os.Stat(got); err != nil || info.Mode().Perm() != 0o600 {
		t.Errorf("config must be 0600, got %v", info.Mode().Perm())
	}
}
