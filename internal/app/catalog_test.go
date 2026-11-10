package app

// Catalog tests (T7): the real Spotify library behind the floating search
// window and the playlists view. Every fetch is a stub library.Fetch whose
// calls are recorded — no network, no credentials; the endpoint shapes
// themselves are pinned in internal/library with httptest fixtures.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nahuel/spotify-tui/internal/config"
	"github.com/nahuel/spotify-tui/internal/library"
	"github.com/nahuel/spotify-tui/internal/library/mock"
)

const (
	realTrackID  = "4uLU6hMCjMI75M1A2tKUQC"
	realTrackID2 = "3uLU6hMCjMI75M1A2tKUQC"
)

// --- stubs ---

// stubFetch records every catalog call and serves canned pages/errors.
type stubFetch struct {
	playlistsFn func(offset int) (library.PlaylistPage, error)
	itemsFn     func(id string, offset int) (library.ItemsPage, error)
	searchFn    func(query string, offset int) (library.SearchPage, error)
	savedFn     func(id string, saved bool) error
	calls       []string
}

func (f *stubFetch) Playlists(ctx context.Context, offset int) (library.PlaylistPage, error) {
	f.calls = append(f.calls, fmt.Sprintf("playlists %d", offset))
	return f.playlistsFn(offset)
}

func (f *stubFetch) Items(ctx context.Context, id string, offset int) (library.ItemsPage, error) {
	f.calls = append(f.calls, fmt.Sprintf("items %s %d", id, offset))
	return f.itemsFn(id, offset)
}

func (f *stubFetch) Search(ctx context.Context, query string, offset int) (library.SearchPage, error) {
	f.calls = append(f.calls, fmt.Sprintf("search %q %d", query, offset))
	return f.searchFn(query, offset)
}

func (f *stubFetch) SetSaved(ctx context.Context, id string, saved bool) error {
	f.calls = append(f.calls, fmt.Sprintf("saved %s %v", id, saved))
	return f.savedFn(id, saved)
}

// stubPlayer records Play calls; every other action is a no-op.
type stubPlayer struct {
	played []library.Track
}

func (p *stubPlayer) Play(t library.Track) error    { p.played = append(p.played, t); return nil }
func (p *stubPlayer) TogglePause() error            { return nil }
func (p *stubPlayer) Next() error                   { return nil }
func (p *stubPlayer) Prev() error                   { return nil }
func (p *stubPlayer) Enqueue(t library.Track) error { return nil }
func (p *stubPlayer) VolumeUp() error               { return nil }
func (p *stubPlayer) VolumeDown() error             { return nil }

// catalogPlayer is the real-mode wiring: a library.Player that also exposes
// the catalog (what internal/player.Player does once logged in).
type catalogPlayer struct {
	*stubPlayer
	fetch library.Fetch
}

func (c catalogPlayer) Catalog() library.Fetch { return c.fetch }

// session returns a minimal real-mode session snapshot for the player bar.
// --- harness ---

func newRealModel(t *testing.T, w, h int, fetch library.Fetch) (*Model, *stubPlayer) {
	t.Helper()
	data := mock.New()
	pl := &stubPlayer{}
	mm := New(config.Config{ClientID: "test-client", CallbackPort: config.DefaultCallbackPort},
		data, catalogPlayer{stubPlayer: pl, fetch: fetch}, testClock)
	updated, _ := mm.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m := updated.(Model)
	if m.fetch == nil {
		t.Fatal("real mode: the model must hold the catalog fetch")
	}
	if m.drillDown {
		t.Fatal("real mode must start at the library root, not inside a fixture playlist")
	}
	return &m, pl
}

// runCatalogCmd executes a tea.Cmd and applies its catalog message (or batch)
// to the model. Commands that only produce toasts/ticks are discarded — the
// toast tick would sleep for real — so after a catalog message lands, its own
// returned command (a toast) is not followed.
func runCatalogCmd(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	applyCatalogMsg(t, m, msg)
}

func applyCatalogMsg(t *testing.T, m *Model, msg tea.Msg) {
	t.Helper()
	switch msg := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, c := range msg {
			runCatalogCmd(t, m, c)
		}
	case playlistsMsg:
		updated, _ := m.Update(msg)
		*m = updated.(Model)
	case itemsMsg:
		updated, _ := m.Update(msg)
		*m = updated.(Model)
	case searchMsg:
		updated, _ := m.Update(msg)
		*m = updated.(Model)
	case savedMsg:
		updated, _ := m.Update(msg)
		*m = updated.(Model)
	}
}

// pressRun presses a key and drains the catalog command it returned.
func pressRun(t *testing.T, m *Model, key string) {
	t.Helper()
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	*m = updated.(Model)
	runCatalogCmd(t, m, cmd)
}

func typeIn(t *testing.T, m *Model, s string) {
	t.Helper()
	for _, r := range s {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		*m = updated.(Model)
	}
}

// loadPlaylistsReady simulates the session becoming live: EventReady fires
// the first (and only the first) playlists page.
func loadPlaylistsReady(t *testing.T, m *Model) {
	t.Helper()
	updated, cmd := m.Update(playerEventMsg{library.Event{Type: library.EventReady}})
	*m = updated.(Model)
	runCatalogCmd(t, m, cmd)
}

// --- playlists: paging follows `next` without a full prefetch ---

func TestCatalogPlaylistsPagingFollowsNextLazily(t *testing.T) {
	f := &stubFetch{
		playlistsFn: func(offset int) (library.PlaylistPage, error) {
			switch offset {
			case 0:
				return library.PlaylistPage{
					Playlists: []library.PlaylistSummary{
						{ID: "pl1", Name: "Bebop Essentials", Owner: "nahuel", Private: true, TrackCount: 42},
						{ID: "pl2", Name: "Road Trip", Owner: "nahuel", Private: false, TrackCount: 17},
					},
					Total: 3, Offset: 0, NextOffset: 50,
				}, nil
			case 50:
				return library.PlaylistPage{
					Playlists: []library.PlaylistSummary{{ID: "pl3", Name: "Focus", Owner: "nahuel", Private: true, TrackCount: 5}},
					Total:     3, Offset: 50, NextOffset: -1,
				}, nil
			default:
				return library.PlaylistPage{}, errors.New("unexpected offset — the catalog must never prefetch")
			}
		},
	}
	m, _ := newRealModel(t, 180, 50, f)

	loadPlaylistsReady(t, m)
	if got := strings.Join(f.calls, ","); got != "playlists 0" {
		t.Fatalf("after EventReady the catalog made [%s], want only the first page — never a full prefetch", got)
	}
	got := m.View()
	if !strings.Contains(got, "Bebop Essentials") || !strings.Contains(got, "Road Trip") {
		t.Errorf("the playlists view should list page 1, got:\n%s", got)
	}
	if !strings.Contains(got, "nahuel · private · 42") {
		t.Errorf("a playlist row shows owner, privacy and track count, got:\n%s", got)
	}

	// The second page loads only when the cursor reaches the last row.
	pressRun(t, m, "G")
	if got := strings.Join(f.calls, ","); got != "playlists 0,playlists 50" {
		t.Fatalf("after scrolling to the end the catalog made [%s], want one lazy follow-up page", got)
	}
	if !strings.Contains(m.View(), "Focus") {
		t.Errorf("the followed page should render its playlist, got:\n%s", m.View())
	}
}

// --- playlist contents: a playlist without items is unavailable, never empty ---

func TestCatalogPlaylistWithoutItemsShowsUnavailableState(t *testing.T) {
	f := &stubFetch{
		playlistsFn: func(offset int) (library.PlaylistPage, error) {
			return library.PlaylistPage{
				Playlists: []library.PlaylistSummary{
					{ID: "pl2", Name: "Shared List", Owner: "someone-else", Private: false, TrackCount: library.NoTrackCount},
				},
				Total: 1, NextOffset: -1,
			}, nil
		},
		itemsFn: func(id string, offset int) (library.ItemsPage, error) {
			// 2026 (S14): no items key for playlists the user does not own.
			return library.ItemsPage{Total: 37, Unavailable: "contents unavailable · you are not the owner or a collaborator"}, nil
		},
	}
	m, _ := newRealModel(t, 180, 50, f)
	loadPlaylistsReady(t, m)

	// the row under the header; enter opens it
	pressRun(t, m, "j")
	pressRun(t, m, "enter")
	if !m.drillDown {
		t.Fatal("enter on a real playlist row must drill into it")
	}
	got := m.View()
	if !strings.Contains(got, "contents unavailable") || !strings.Contains(got, "not the owner or a collaborator") {
		t.Errorf("the drill-down must say why there are no contents, got:\n%s", got)
	}
	if strings.Contains(got, "TITLE") {
		t.Errorf("an unavailable playlist must not render an empty track table pretending success:\n%s", got)
	}
}

func TestCatalogPlaylistLoadingAndErrorStates(t *testing.T) {
	f := &stubFetch{
		playlistsFn: func(offset int) (library.PlaylistPage, error) {
			return library.PlaylistPage{
				Playlists: []library.PlaylistSummary{{ID: "pl1", Name: "Mine", Owner: "nahuel", Private: true, TrackCount: 3}},
				Total:     1, NextOffset: -1,
			}, nil
		},
		itemsFn: func(id string, offset int) (library.ItemsPage, error) {
			if offset == 0 {
				return library.ItemsPage{}, &library.APIError{Status: 500, Message: "boom"}
			}
			return library.ItemsPage{}, errors.New("unexpected")
		},
	}
	m, _ := newRealModel(t, 180, 50, f)
	loadPlaylistsReady(t, m)
	pressRun(t, m, "j")
	// while the fetch is in flight the panel shows loading, not empty
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("enter")})
	*m = updated.(Model)
	if !strings.Contains(m.View(), "loading tracks") {
		t.Errorf("the contents panel must show a loading state, got:\n%s", m.View())
	}
	runCatalogCmd(t, m, cmd)
	got := m.View()
	if !strings.Contains(got, "✗ could not load") || !strings.Contains(got, "spotify 500 · boom") {
		t.Errorf("the error state must carry the Spotify status and message, got:\n%s", got)
	}
	// r retries the failed fetch
	f.itemsFn = func(id string, offset int) (library.ItemsPage, error) {
		return library.ItemsPage{Tracks: []library.Track{
			{ID: realTrackID, Title: "So What", Artist: "Miles Davis", Album: "Kind of Blue", Duration: 562000000000},
		}, Total: 1, NextOffset: -1}, nil
	}
	pressRun(t, m, "r")
	got = m.View()
	if !strings.Contains(got, "So What") {
		t.Errorf("`r` should retry the failed contents fetch, got:\n%s", got)
	}
}

// --- playing a real row ---

func TestCatalogEnterPlaysRealRow(t *testing.T) {
	f := &stubFetch{
		playlistsFn: func(offset int) (library.PlaylistPage, error) {
			return library.PlaylistPage{
				Playlists: []library.PlaylistSummary{{ID: "pl1", Name: "Mine", Owner: "nahuel", Private: true, TrackCount: 1}},
				Total:     1, NextOffset: -1,
			}, nil
		},
		itemsFn: func(id string, offset int) (library.ItemsPage, error) {
			return library.ItemsPage{Tracks: []library.Track{
				{ID: realTrackID, Title: "So What", Artist: "Miles Davis", Album: "Kind of Blue"},
			}, Total: 1, NextOffset: -1}, nil
		},
	}
	m, pl := newRealModel(t, 180, 50, f)
	loadPlaylistsReady(t, m)
	pressRun(t, m, "j")
	pressRun(t, m, "enter") // open the playlist
	press(t, m, "enter")    // play the row under the cursor
	if len(pl.played) != 1 || pl.played[0].ID != realTrackID {
		t.Errorf("enter on a real row must play that track through the player adapter, played %+v", pl.played)
	}
}

// --- saved toggle: the cell answers only after the server does ---

func savedFixtures(fail bool) *stubFetch {
	return &stubFetch{
		playlistsFn: func(offset int) (library.PlaylistPage, error) {
			return library.PlaylistPage{
				Playlists: []library.PlaylistSummary{{ID: "pl1", Name: "Mine", Owner: "nahuel", Private: true, TrackCount: 1}},
				Total:     1, NextOffset: -1,
			}, nil
		},
		itemsFn: func(id string, offset int) (library.ItemsPage, error) {
			return library.ItemsPage{Tracks: []library.Track{
				{ID: realTrackID, Title: "So What", Artist: "Miles Davis", Album: "Kind of Blue"},
			}, Total: 1, NextOffset: -1}, nil
		},
		savedFn: func(id string, saved bool) error {
			if fail {
				return &library.APIError{Status: 403, Message: "no permission"}
			}
			return nil
		},
	}
}

func TestCatalogSavedToggleWaitsForTheServer(t *testing.T) {
	f := savedFixtures(false)
	m, _ := newRealModel(t, 180, 50, f)
	loadPlaylistsReady(t, m)
	pressRun(t, m, "j")
	pressRun(t, m, "enter") // open the playlist

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	*m = updated.(Model)
	if m.plTracks[0].Saved {
		t.Error("the cell must not flip before the server answers")
	}
	runCatalogCmd(t, m, cmd)
	if !m.plTracks[0].Saved {
		t.Error("after the server confirmed, the cell should read saved")
	}
	if !strings.Contains(m.View(), "[x]") {
		t.Error("the row should now render [x]")
	}
}

func TestCatalogSavedToggleRevertsOnFailureWithToast(t *testing.T) {
	f := savedFixtures(true)
	m, _ := newRealModel(t, 180, 50, f)
	loadPlaylistsReady(t, m)
	pressRun(t, m, "j")
	pressRun(t, m, "enter")

	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	*m = updated.(Model)
	runCatalogCmd(t, m, cmd)
	if m.plTracks[0].Saved {
		t.Error("a failed save must revert: the track stays unsaved")
	}
	got := m.View()
	if !strings.Contains(got, "✗") || !strings.Contains(got, "403") {
		t.Errorf("a failed save must toast the server's answer, got:\n%s", got)
	}
	if strings.Contains(got, "[x]") {
		t.Errorf("the cell must not show [x] after a failed save:\n%s", got)
	}
}

// --- search: offset pagination under the 10-result cap ---

func searchFixtures(f *stubFetch) {
	f.searchFn = func(query string, offset int) (library.SearchPage, error) {
		page := library.SearchPage{Query: query, Offset: offset}
		if offset == 0 {
			page.Tracks = []library.Track{
				{ID: realTrackID, Title: "So What", Artist: "Miles Davis", Album: "Kind of Blue"},
				{ID: realTrackID2, Title: "Blue in Green", Artist: "Miles Davis", Album: "Kind of Blue"},
			}
			page.Totals.Tracks = 25
			page.NextOffset = 10
		} else if offset == 10 {
			page.Tracks = []library.Track{
				{ID: realTrackID, Title: "Blue Monk", Artist: "Thelonious Monk", Album: "Blue Monk"},
			}
			page.Totals.Tracks = 25
			page.NextOffset = -1
		}
		return page, nil
	}
}

func TestCatalogSearchPaginatesByOffsetUnderTheCap(t *testing.T) {
	f := &stubFetch{}
	searchFixtures(f)
	m, _ := newRealModel(t, 180, 50, f)

	press(t, m, "/")
	typeIn(t, m, "blue")
	pressRun(t, m, "enter") // runs the query
	if got := strings.Join(f.calls, ","); got != `search "blue" 0` {
		t.Fatalf("first search made [%s], want one capped request at offset 0", got)
	}
	got := m.View()
	if !strings.Contains(got, "tracks 2 of 25") {
		t.Errorf("the window shows type-grouped results with counts, got:\n%s", got)
	}
	if !strings.Contains(got, "So What") || !strings.Contains(got, "Blue in Green") {
		t.Errorf("the window should list the page's tracks, got:\n%s", got)
	}

	// scrolling to the last result drives the next offset page
	for i := 0; i < 2; i++ {
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("j")})
		*m = updated.(Model)
		runCatalogCmd(t, m, cmd)
	}
	if got := strings.Join(f.calls, ","); got != `search "blue" 0,search "blue" 10` {
		t.Fatalf("scrolling past the last result must page by offset, made [%s]", got)
	}
	if !strings.Contains(m.View(), "Blue Monk") {
		t.Errorf("the second page should append its results, got:\n%s", m.View())
	}
}

func TestCatalogSearchEmptyState(t *testing.T) {
	f := &stubFetch{
		searchFn: func(query string, offset int) (library.SearchPage, error) {
			return library.SearchPage{Query: query}, nil // an empty search
		},
	}
	m, _ := newRealModel(t, 180, 50, f)
	press(t, m, "/")
	typeIn(t, m, "zzzz")
	pressRun(t, m, "enter")
	got := m.View()
	if !strings.Contains(got, "nothing matches") {
		t.Errorf("an empty search shows the empty state, got:\n%s", got)
	}
	if strings.Contains(got, "tracks 0") {
		t.Errorf("an empty search must not render empty groups, got:\n%s", got)
	}
}

func TestCatalogSearchRateLimitState(t *testing.T) {
	f := &stubFetch{
		searchFn: func(query string, offset int) (library.SearchPage, error) {
			return library.SearchPage{}, &library.APIError{Status: 429, RateLimited: true}
		},
	}
	m, _ := newRealModel(t, 180, 50, f)
	press(t, m, "/")
	typeIn(t, m, "blue")
	pressRun(t, m, "enter")
	got := m.View()
	if !strings.Contains(got, "rate limited") {
		t.Errorf("a 429 must surface the existing rate-limit state, got:\n%s", got)
	}
	if !library.IsRateLimited(m.sErr) && m.sErr != nil {
		t.Errorf("the rate-limit error must stay inspectable, got %v", m.sErr)
	}
}

func TestCatalogSearchEnterPlaysTrackResult(t *testing.T) {
	f := &stubFetch{}
	searchFixtures(f)
	m, pl := newRealModel(t, 180, 50, f)
	press(t, m, "/")
	typeIn(t, m, "blue")
	pressRun(t, m, "enter") // run the query
	press(t, m, "j")        // off the group header, onto the first track
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	*m = updated.(Model)
	_ = cmd // acting on a result produces a toast; Play happens synchronously
	if len(pl.played) != 1 || pl.played[0].ID != realTrackID {
		t.Errorf("enter on a track result must play it, played %+v", pl.played)
	}
}

// --- mock mode is untouched ---

func TestMockModeHasNoCatalog(t *testing.T) {
	data := mock.New()
	m := New(config.Config{ClientID: "c", CallbackPort: 8888}, data, data, testClock)
	if m.fetch != nil {
		t.Error("SPOTIFY_TUI_MOCK=1 must keep the mock data source: no catalog fetch")
	}
	if !m.drillDown {
		t.Error("mock mode keeps the artboard's drill-down start")
	}
}

// --- width discipline holds in real mode too (180, 140, 80) ---

func TestCatalogViewFitsTerminal(t *testing.T) {
	f := &stubFetch{
		playlistsFn: func(offset int) (library.PlaylistPage, error) {
			return library.PlaylistPage{
				Playlists: []library.PlaylistSummary{
					{ID: "pl1", Name: "Bebop Essentials — a very long playlist name that must truncate", Owner: "a-very-long-owner-name", Private: true, TrackCount: 4200},
					{ID: "pl2", Name: "Shared List", Owner: "someone-else", Private: false, TrackCount: library.NoTrackCount},
				},
				Total: 2, NextOffset: -1,
			}, nil
		},
		itemsFn: func(id string, offset int) (library.ItemsPage, error) {
			if id == "pl2" {
				return library.ItemsPage{Unavailable: "contents unavailable · you are not the owner or a collaborator"}, nil
			}
			return library.ItemsPage{Tracks: []library.Track{
				{ID: realTrackID, Title: "So What", Artist: "Miles Davis", Album: "Kind of Blue", Duration: 562000000000},
			}, Total: 1, NextOffset: -1}, nil
		},
		searchFn: func(query string, offset int) (library.SearchPage, error) {
			return library.SearchPage{
				Query: query, Tracks: []library.Track{
					{ID: realTrackID, Title: "So What", Artist: "Miles Davis", Album: "Kind of Blue", Duration: 562000000000},
				}, Totals: library.SearchTotals{Tracks: 87}, NextOffset: -1,
			}, nil
		},
	}
	for _, size := range [][2]int{{180, 50}, {140, 40}, {80, 24}} {
		w, h := size[0], size[1]
		m, _ := newRealModel(t, w, h, f)
		loadPlaylistsReady(t, m)

		check := func(stage string) {
			t.Helper()
			for i, line := range lines(m) {
				if lw := visibleWidth(line); lw > w {
					t.Errorf("w=%d %s: line %d is %d columns wide: %q", w, stage, i, lw, stripANSI(line))
				}
			}
			if n := len(lines(m)); n != h {
				t.Errorf("w=%d %s: rendered %d lines, want %d", w, stage, n, h)
			}
		}
		check("playlists")

		// drill-down: owned, then unavailable
		pressRun(t, m, "j")
		pressRun(t, m, "enter")
		check("owned playlist")
		pressKey(t, m, tea.KeyEsc)
		pressRun(t, m, "j")
		pressRun(t, m, "enter")
		check("unavailable playlist")
		pressKey(t, m, tea.KeyEsc)

		// the search window over the list
		press(t, m, "/")
		typeIn(t, m, "blue")
		pressRun(t, m, "enter")
		check("search window")
		pressKey(t, m, tea.KeyEsc)

		// the queue section and drawer over catalog data
		pressKey(t, m, tea.KeyTab)
		pressKey(t, m, tea.KeyTab)
		press(t, m, "q")
		check("queue with drawer")
	}
}
