// Command spotify-tui is the terminal player shell (slice 1: mock data).
//
// It loads the XDG config ($XDG_CONFIG_HOME/spotify-tui/config.toml), builds
// the bubbletea program and runs it. Ctrl+C quits.
package main

import (
	"fmt"
	"os"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nahuel/spotify-tui/internal/app"
	"github.com/nahuel/spotify-tui/internal/config"
	"github.com/nahuel/spotify-tui/internal/library/mock"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	data := mock.New() // slice 1: mock data only; go-librespot arrives in slice 2
	program := tea.NewProgram(app.New(cfg, data, data, nil), tea.WithAltScreen())
	if _, err := program.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "spotify-tui:", err)
		os.Exit(1)
	}
}
