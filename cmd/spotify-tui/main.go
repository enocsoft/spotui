// Command spotify-tui is the terminal player. By default it embeds a real
// go-librespot session (spec S1, S15): the audio leaves this machine through
// a local PulseAudio sink and the first login surfaces an auth URL in the
// TUI (spec S9). Set SPOTIFY_TUI_MOCK=1 to run the offline shell with mock
// data and the mock player, no credentials needed.
//
// The XDG config lives at $XDG_CONFIG_HOME/spotify-tui/config.toml and must
// at least carry the Spotify app client_id; the device id and the persisted
// credentials land in $XDG_STATE_HOME/spotify-tui (written 0600, never in
// the repository). The Spotify app in the developer dashboard must register
// http://127.0.0.1:<callback_port>/login as a redirect URI (go-librespot's
// interactive login redirects there; default callback port 8888).
package main

import (
	"context"
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nahuel/spotify-tui/internal/app"
	"github.com/nahuel/spotify-tui/internal/config"
	"github.com/nahuel/spotify-tui/internal/library/mock"
	"github.com/nahuel/spotify-tui/internal/player"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if os.Getenv("SPOTIFY_TUI_MOCK") == "1" {
		data := mock.New() // offline shell: mock data, mock player
		program := tea.NewProgram(app.New(cfg, data, data, nil), tea.WithAltScreen())
		if _, err := program.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "spotify-tui:", err)
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
		fmt.Fprintln(os.Stderr, "spotify-tui:", runErr)
		os.Exit(1)
	}
}
