package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// SaveClientID writes client_id into the config file, keeping every other key
// the user already set. It creates the directory and writes the file 0600: the
// client id is not a secret, but a config file with a credential-shaped value
// should not be world readable by default.
func SaveClientID(id string) (string, error) {
	path := filepath.Join(ConfigDir(), "config.toml")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("spotui: cannot create %s: %v", filepath.Dir(path), err)
	}
	var lines []string
	if existing, err := os.ReadFile(path); err == nil {
		for _, line := range strings.Split(strings.TrimRight(string(existing), "\n"), "\n") {
			key, _, ok := strings.Cut(line, "=")
			if ok && strings.TrimSpace(key) == "client_id" {
				continue // replaced below; the rest of the file stays as it was
			}
			lines = append(lines, line)
		}
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("spotui: cannot read %s: %v", path, err)
	}
	lines = append(lines, fmt.Sprintf("client_id = %q", id))
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("spotui: cannot write %s: %v", path, err)
	}
	return path, nil
}
