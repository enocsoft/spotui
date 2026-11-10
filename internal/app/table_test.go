package app

// T28: the context resolver (library/context.go) never returns added_at, so
// the real-mode track table must not promise an ADDED column it cannot fill.
// The mock fixture still carries dates, so its table keeps ADDED. The column
// set may differ per view; the width discipline (180/140/80) stays.

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nahuel/spotui/internal/library"
)

// contentsFetch is a stub catalog whose playlist and album both resolve one
// real track — the shapes the resolver path serves.
func contentsFetch() *stubFetch {
	return &stubFetch{
		playlistsFn: func(offset int) (library.PlaylistPage, error) {
			return library.PlaylistPage{
				Playlists: []library.PlaylistSummary{
					{ID: "pl1", Name: "Bebop Essentials", Owner: "nahuel", Private: true, TrackCount: 1},
				},
				Total: 1, NextOffset: -1,
			}, nil
		},
		itemsFn: func(id string, offset int) (library.ItemsPage, error) {
			return library.ItemsPage{Tracks: []library.Track{
				{ID: realTrackID, Title: "So What", Artist: "Miles Davis", Album: "Kind of Blue", Duration: 562000000000},
			}, Total: 1, NextOffset: -1}, nil
		},
		albumItemsFn: func(id string, offset int) (library.ItemsPage, error) {
			return library.ItemsPage{Tracks: []library.Track{
				{ID: realTrackID2, Title: "Freddie Freeloader", Artist: "Miles Davis", Album: "Kind of Blue", Duration: 587000000000},
			}, Total: 1, NextOffset: -1}, nil
		},
		searchFn: func(query string, offset int) (library.SearchPage, error) {
			return library.SearchPage{Query: query, Albums: []library.AlbumSummary{
				{ID: "al1", Name: "Kind of Blue", Artist: "Miles Davis"},
			}, Totals: library.SearchTotals{Albums: 1}, NextOffset: -1}, nil
		},
	}
}

// findHeaderLine returns the rendered table header line, if any.
func findHeaderLine(t *testing.T, m *Model) (string, bool) {
	t.Helper()
	for _, line := range lines(m) {
		if strings.Contains(stripANSI(line), "TITLE") {
			return line, true
		}
	}
	return "", false
}

func TestRealPlaylistTableDropsAddedColumn(t *testing.T) {
	f := contentsFetch()
	for _, size := range [][2]int{{180, 50}, {140, 40}} {
		w, h := size[0], size[1]
		m, _ := newRealModel(t, w, h, f)
		loadPlaylistsReady(t, m)
		m.drawerOpen = false
		pressRun(t, m, "j")
		pressRun(t, m, "enter") // open the playlist
		header, ok := findHeaderLine(t, m)
		if !ok {
			t.Fatalf("w=%d: no track table header rendered", w)
		}
		if strings.Contains(stripANSI(header), "ADDED") {
			t.Errorf("w=%d: playlist table still promises ADDED with no data: %q", w, stripANSI(header))
		}
		for _, want := range []string{"#", "TITLE", "ARTIST", "ALBUM", "SAVED", "TIME"} {
			if !strings.Contains(stripANSI(header), want) {
				t.Errorf("w=%d: playlist table lost column %q: %q", w, want, stripANSI(header))
			}
		}
		// the width discipline holds with the column gone
		for i, line := range lines(m) {
			if lw := visibleWidth(line); lw > w {
				t.Errorf("w=%d: line %d is %d columns wide: %q", w, i, lw, stripANSI(line))
			}
		}
		if n := len(lines(m)); n != h {
			t.Errorf("w=%d: rendered %d lines, want %d", w, n, h)
		}
	}
}

func TestRealAlbumTableDropsAddedColumn(t *testing.T) {
	f := contentsFetch()
	for _, size := range [][2]int{{180, 50}, {140, 40}} {
		w, h := size[0], size[1]
		m, _ := newRealModel(t, w, h, f)
		loadPlaylistsReady(t, m)
		// search for the album, open its tracks through the resolver
		press(t, m, "/")
		typeSearch(t, m, "blue")
		pressKey(t, m, tea.KeyEnter)
		pressRun(t, m, "j")
		pressRun(t, m, "j")
		pressRun(t, m, "j") // onto the album row
		pressRun(t, m, "enter")
		header, ok := findHeaderLine(t, m)
		if !ok {
			t.Fatalf("w=%d: no track table header rendered in the album view", w)
		}
		if strings.Contains(stripANSI(header), "ADDED") {
			t.Errorf("w=%d: album table still promises ADDED with no data: %q", w, stripANSI(header))
		}
		if !strings.Contains(stripANSI(header), "TIME") {
			t.Errorf("w=%d: album table lost TIME: %q", w, stripANSI(header))
		}
		for i, line := range lines(m) {
			if lw := visibleWidth(line); lw > w {
				t.Errorf("w=%d: album line %d is %d columns wide: %q", w, i, lw, stripANSI(line))
			}
		}
		if n := len(lines(m)); n != h {
			t.Errorf("w=%d: rendered %d lines, want %d", w, n, h)
		}
	}
}

// TestMockTableKeepsAddedColumn is the PRESERVE test: the mock fixture fills
// ADDED (library/mock carries dates), so its table keeps the column.
func TestMockTableKeepsAddedColumn(t *testing.T) {
	m := newModel(t, 180, 50)
	header, ok := findHeaderLine(t, m)
	if !ok {
		t.Fatal("no track table header rendered in mock mode")
	}
	if !strings.Contains(stripANSI(header), "ADDED") {
		t.Errorf("mock fixture has dates; its table must keep ADDED: %q", stripANSI(header))
	}
	if !strings.Contains(stripANSI(header), "TIME") {
		t.Errorf("mock table lost TIME: %q", stripANSI(header))
	}
}
