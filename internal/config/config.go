// Package config loads the spotify-tui XDG configuration file.
//
// The file lives at $XDG_CONFIG_HOME/spotify-tui/config.toml and must at
// least carry the Spotify app client_id (ncspot-style local config, spec S9).
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

// DefaultPath returns the XDG config path for spotify-tui:
// $XDG_CONFIG_HOME/spotify-tui/config.toml.
func DefaultPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			base = "."
		} else {
			base = filepath.Join(home, ".config")
		}
	}
	return filepath.Join(base, "spotify-tui", "config.toml")
}

// StateDir returns the XDG state directory for spotify-tui:
// $XDG_STATE_HOME/spotify-tui. The device id and the persisted Spotify
// credentials land there (written 0600 by the session bootstrap), never in
// the repository (spec S9).
func StateDir() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			base = "."
		} else {
			base = filepath.Join(home, ".local", "state")
		}
	}
	return filepath.Join(base, "spotify-tui")
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
				"spotify-tui: no config file at %s — create it with:  client_id = \"your-spotify-client-id\"", path)
		}
		return Config{}, fmt.Errorf("spotify-tui: cannot read %s: %v", path, err)
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
				"spotify-tui: %s: line %d: expected key = \"value\", got %q", path, lineNo, line)
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
					"spotify-tui: %s: line %d: callback_port must be a port between 1 and 65535, got %q",
					path, lineNo, value)
			}
			cfg.CallbackPort = n
		default:
			// Unknown keys are ignored so future versions stay forward-compatible.
		}
	}
	if err := sc.Err(); err != nil {
		return Config{}, fmt.Errorf("spotify-tui: cannot read %s: %v", path, err)
	}
	if cfg.ClientID == "" {
		return Config{}, fmt.Errorf(
			"spotify-tui: %s is missing client_id — add:  client_id = \"your-spotify-client-id\"", path)
	}
	if cfg.CallbackPort == 0 {
		cfg.CallbackPort = DefaultCallbackPort
	}
	return cfg, nil
}
