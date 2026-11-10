// Command spotui is the terminal player. By default it embeds a real
// go-librespot session (spec S1, S15): the audio leaves this machine through
// a local PulseAudio sink, and the login is OAuth Authorization Code + PKCE
// run by the app itself (spec S20): the first run surfaces the authorization
// URL in the TUI, a local /login callback on 127.0.0.1 captures the code and
// the token is handed to the session (the library's own interactive login
// still uses the removed implicit grant and cannot be used).
// Set SPOTUI_MOCK=1 to run the offline shell with mock data and the
// mock player, no credentials needed.
//
// spotui -version (or --version) prints "spotui 1.0.0 (<revision>)" and
// exits before any config read or audio device is opened.
//
// The XDG config lives at $XDG_CONFIG_HOME/spotui/config.toml and must
// at least carry the Spotify app client_id; the token cache (0600), the
// device id and the persisted session credentials land in
// $XDG_STATE_HOME/spotui, never in the repository. One-time rename
// compatibility: a config or state dir left behind under the pre-rename
// spotify-tui name is still used and announced with one notice line. The
// Spotify app in the developer dashboard must register
// http://127.0.0.1:<callback_port>/login as a redirect URI (default 8888).
package main

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/enocsoft/spotui/internal/app"
	"github.com/enocsoft/spotui/internal/config"
	"github.com/enocsoft/spotui/internal/library/mock"
	"github.com/enocsoft/spotui/internal/player"
)

func main() {
	// -version answers before anything else: no config read, no audio
	// device, no TUI. A user asking for the version must never trigger a
	// login flow.
	if len(os.Args) > 1 && (os.Args[1] == "-version" || os.Args[1] == "--version") {
		fmt.Println(app.VersionString())
		return
	}

	// --provider is the first-run setup: ask for the dashboard client id and
	// store it. It must run before config.Load, because its whole purpose is to
	// cope with a config that does not exist yet.
	if len(os.Args) > 1 && (os.Args[1] == "-provider" || os.Args[1] == "--provider") {
		if err := runProvider(os.Stdin, os.Stdout); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		return
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	noticeLegacyDirs()

	if mockMode() {
		data := mock.New() // offline shell: mock data, mock player
		program := tea.NewProgram(app.New(cfg, data, data, nil), tea.WithAltScreen())
		if _, err := program.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "spotui:", err)
			os.Exit(1)
		}
		return
	}

	// Library content is still mock until slice 3 (T7) wires the Web API
	// catalog; session, playback and sinks are the real embedded player.
	content := mock.New()
	pl := player.New(player.Options{
		ConfigDir:    config.StateDir(),
		CallbackPort: cfg.CallbackPort,
		ClientID:     cfg.ClientID,
		Output:       cfg.Output,
	}, content)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pl.Start(ctx)

	program := tea.NewProgram(app.New(cfg, pl, pl, nil), tea.WithAltScreen())
	_, runErr := program.Run()
	cancel()
	pl.Close()
	if runErr != nil {
		fmt.Fprintln(os.Stderr, "spotui:", runErr)
		os.Exit(1)
	}
}

// mockMode reports the offline shell: SPOTUI_MOCK=1, or the pre-rename
// SPOTIFY_TUI_MOCK=1 for one compatibility release.
func mockMode() bool {
	return os.Getenv("SPOTUI_MOCK") == "1" || os.Getenv("SPOTIFY_TUI_MOCK") == "1"
}

// noticeLegacyDirs says, once and in one line each, when a config or state
// dir from before the spotui rename is still being read. Silent reuse would
// look like the rename did nothing; loud reuse points at the migration.
func noticeLegacyDirs() {
	if legacy := config.LegacyConfigPath(); legacy != "" {
		fmt.Fprintf(os.Stderr,
			"spotui: using the legacy config %s — move it to %s to stop this notice\n",
			legacy, config.ConfigDir())
	}
	if legacy := config.LegacyStateDir(); legacy != "" {
		fmt.Fprintf(os.Stderr,
			"spotui: using the legacy state dir %s — move it to %s to stop this notice\n",
			legacy, config.NewStateDir())
	}
}
