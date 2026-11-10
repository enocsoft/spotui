// Package config loads the spotui XDG configuration file.
//
// The file lives at $XDG_CONFIG_HOME/spotui/config.toml and must at least
// carry the Spotify app client_id (ncspot-style local config, spec S9).
package config

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Config holds the parsed configuration. Only client_id is required; every
// other key is optional and falls back to a sensible default.
type Config struct {
	// ClientID is the Spotify app client id used for the OAuth PKCE flow.
	ClientID string
	// Output is the preferred local audio sink id; empty means the system
	// default (spec S16: a local sink, never a remote Connect device).
	Output string
	// CallbackPort is the TCP port the interactive login callback listens on
	// (spec S12: loopback redirect, IP literal).
	CallbackPort int
}

// DefaultCallbackPort is the login callback port used when the config does
// not set callback_port.
const DefaultCallbackPort = 8888

func xdgDir(envVar, fallback string) string {
	base := os.Getenv(envVar)
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			base = "."
		} else {
			base = filepath.Join(home, fallback)
		}
	}
	return base
}

func dirExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// ConfigDir returns the spotui XDG config directory:
// $XDG_CONFIG_HOME/spotui. Unlike DefaultPath it never applies the legacy
// fallback, so it names the destination when the user wants to migrate.
func ConfigDir() string {
	return filepath.Join(xdgDir("XDG_CONFIG_HOME", ".config"), "spotui")
}

// DefaultPath returns the XDG config path for spotui:
// $XDG_CONFIG_HOME/spotui/config.toml.
//
// One-time rename compatibility: when the spotui config dir does not exist
// and the pre-rename spotify-tui dir does, the legacy config file is used
// instead and LegacyConfigPath reports it, so an existing install keeps
// working instead of silently asking to log in again.
func DefaultPath() string {
	path, _ := resolveConfigPath()
	return path
}

// LegacyConfigPath reports the pre-rename spotify-tui config file that
// DefaultPath is reading, or "" when the new spotui config file applies.
func LegacyConfigPath() string {
	path, legacy := resolveConfigPath()
	if legacy {
		return path
	}
	return ""
}

func resolveConfigPath() (path string, legacy bool) {
	newPath := filepath.Join(ConfigDir(), "config.toml")
	if dirExists(ConfigDir()) {
		return newPath, false
	}
	legacyDir := filepath.Join(xdgDir("XDG_CONFIG_HOME", ".config"), "spotify-tui")
	if dirExists(legacyDir) {
		return filepath.Join(legacyDir, "config.toml"), true
	}
	return newPath, false
}

// NewStateDir returns the spotui XDG state directory without the legacy
// fallback: $XDG_STATE_HOME/spotui. It names the destination when the user
// wants to migrate.
func NewStateDir() string {
	return filepath.Join(xdgDir("XDG_STATE_HOME", filepath.Join(".local", "state")), "spotui")
}

// StateDir returns the XDG state directory for spotui:
// $XDG_STATE_HOME/spotui. The device id and the persisted Spotify
// credentials land there (written 0600 by the session bootstrap), never in
// the repository (spec S9).
//
// One-time rename compatibility: when the spotui state dir does not exist
// and the pre-rename spotify-tui one does, the legacy dir is returned so an
// existing token cache keeps working; LegacyStateDir reports it.
func StateDir() string {
	dir, _ := resolveStateDir()
	return dir
}

// LegacyStateDir reports the pre-rename spotify-tui state dir that StateDir
// is using, or "" when the new spotui state dir applies.
func LegacyStateDir() string {
	_, legacy := resolveStateDir()
	if legacy {
		return filepath.Join(xdgDir("XDG_STATE_HOME", filepath.Join(".local", "state")), "spotify-tui")
	}
	return ""
}

func resolveStateDir() (dir string, legacy bool) {
	if dirExists(NewStateDir()) {
		return NewStateDir(), false
	}
	legacyDir := filepath.Join(xdgDir("XDG_STATE_HOME", filepath.Join(".local", "state")), "spotify-tui")
	if dirExists(legacyDir) {
		return legacyDir, true
	}
	return NewStateDir(), false
}

// Load reads the config from DefaultPath.
func Load() (Config, error) {
	return LoadFrom(DefaultPath())
}

// LoadFrom reads and parses a config.toml. Errors are single-line and
// actionable: they name the file and what to do about it.
func LoadFrom(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Config{}, fmt.Errorf(
				"spotui: no config file at %s — run \"spotui --provider\" to store your Spotify app client_id", path)
		}
		return Config{}, fmt.Errorf("spotui: cannot read %s: %v", path, err)
	}
	defer f.Close()

	cfg := Config{}
	sc := bufio.NewScanner(f)
	lineNo := 0
	for sc.Scan() {
		lineNo++
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "[") {
			continue // blank, comment or table header
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			return Config{}, fmt.Errorf(
				"spotui: %s: line %d: expected key = \"value\", got %q", path, lineNo, line)
		}
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = strings.Trim(value, "\"")
		switch key {
		case "client_id":
			cfg.ClientID = value
		case "output":
			cfg.Output = value
		case "callback_port":
			n, err := strconv.Atoi(value)
			if err != nil || n < 1 || n > 65535 {
				return Config{}, fmt.Errorf(
					"spotui: %s: line %d: callback_port must be a port between 1 and 65535, got %q",
					path, lineNo, value)
			}
			cfg.CallbackPort = n
		default:
			// Unknown keys are ignored so future versions stay forward-compatible.
		}
	}
	if err := sc.Err(); err != nil {
		return Config{}, fmt.Errorf("spotui: cannot read %s: %v", path, err)
	}
	if cfg.ClientID == "" {
		return Config{}, fmt.Errorf(
			"spotui: %s is missing client_id — run \"spotui --provider\" to set it", path)
	}
	if cfg.CallbackPort == 0 {
		cfg.CallbackPort = DefaultCallbackPort
	}
	return cfg, nil
}
