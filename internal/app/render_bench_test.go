package app

// Render-cost benchmarks: what one full frame costs, idle and with a
// floating window open, so the responsiveness work is measured instead of
// guessed. Frame cost x tick frequency is the idle burn: before the change
// the tick re-armed every second unconditionally and the search window made
// the model compose the row list twice per frame.

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nahuel/spotui/internal/config"
	"github.com/nahuel/spotui/internal/library/mock"
)

func benchModel(b *testing.B, w, h int) Model {
	b.Helper()
	m := New(config.Config{ClientID: "test-client", CallbackPort: config.DefaultCallbackPort},
		mock.New(), mock.New(), testClock)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	return updated.(Model)
}

// BenchmarkFrameIdle measures one whole frame (Update on the state tick plus
// the View it triggers) in the idle state — the cost paid once per tick.
func BenchmarkFrameIdle(b *testing.B) {
	m := benchModel(b, 180, 50)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		updated, _ := m.Update(tickMsg{})
		live := updated.(Model)
		_ = live.View()
	}
}

// BenchmarkFrameWindowOpen measures one whole frame with the search floating
// window open over the tracks list and a filter applied.
func BenchmarkFrameWindowOpen(b *testing.B) {
	m := benchModel(b, 180, 50)
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")})
	live := updated.(Model)
	for _, r := range "blue" {
		updated, _ = live.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		live = updated.(Model)
	}
	if live.win != winSearch {
		b.Fatal("setup: the search window must be open")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = live.View()
	}
}
