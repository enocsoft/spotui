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
	if m.section != SectionPlaylists {
		t.Fatal("real mode must start at the playlists navigation, not inside a fixture playlist")
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

// fireDebounce applies the debounce tick synchronously (no sleeping) and
// drains the command it produced — the same contract runCatalogCmd uses for
// key presses.
func fireDebounce(t *testing.T, m *Model, seq int) {
	t.Helper()
	updated, cmd := m.Update(searchDebounceTick{seq: seq})
	*m = updated.(Model)
	runCatalogCmd(t, m, cmd)
}

// typeSearch types a whole query and fires the debounce once. The keystrokes'
// own tick commands are discarded (they only re-arm the 250 ms timer).
func typeSearch(t *testing.T, m *Model, q string) {
	t.Helper()
	typeIn(t, m, q)
	fireDebounce(t, m, m.sDebounce)
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

// --- playlist contents: a playlist the user does not own loads now ---

// The regression that started the resolver path: a playlist the user does not
// own opens with its contents. The playback resolver has no owner restriction
// (the old Web API items endpoint answered 403 and showed "contents
// unavailable"), so the drill-down must render the real table.
func TestCatalogForeignPlaylistContentsLoad(t *testing.T) {
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
			return library.ItemsPage{Tracks: []library.Track{
				{ID: realTrackID, Title: "So What", Artist: "Miles Davis", Album: "Kind of Blue", Duration: 562000000000},
			}, Total: 1, NextOffset: -1}, nil
		},
	}
	m, pl := newRealModel(t, 180, 50, f)
	loadPlaylistsReady(t, m)

	// the row under the header; enter opens it
	pressRun(t, m, "j")
	pressRun(t, m, "enter") // open the playlist
	if m.section != SectionTracks {
		t.Fatal("a real playlist opens on the Tracks view")
	}
	got := m.View()
	if !strings.Contains(got, "So What") {
		t.Errorf("a non-owned playlist must render its contents, got:\n%s", got)
	}
	if strings.Contains(got, "not the owner") || strings.Contains(got, "contents unavailable") {
		t.Errorf("the 403 mapping is gone; a non-owned playlist must not show the old unavailable state:\n%s", got)
	}

	// enter on a resolved row still plays it (the player accepts a URI)
	pressRun(t, m, "enter")
	if len(pl.played) != 1 || pl.played[0].ID != realTrackID {
		t.Errorf("enter on a resolved row must play it, played %+v", pl.played)
	}
}

// The resolver is a bounded set: entries without a playable uri (local
// files, episodes) are skipped and counted, and a truncated resolve is never
// rendered as a complete playlist — both get a footnote row.
func TestCatalogSkippedAndTruncatedFootnotes(t *testing.T) {
	f := &stubFetch{
		playlistsFn: func(offset int) (library.PlaylistPage, error) {
			return library.PlaylistPage{
				Playlists: []library.PlaylistSummary{
					{ID: "pl1", Name: "Mine", Owner: "nahuel", Private: true, TrackCount: 42},
				},
				Total: 1, NextOffset: -1,
			}, nil
		},
		itemsFn: func(id string, offset int) (library.ItemsPage, error) {
			return library.ItemsPage{
				Tracks: []library.Track{
					{ID: realTrackID, Title: "So What", Artist: "Miles Davis", Album: "Kind of Blue"},
				},
				Total: 42, Skipped: 2, Truncated: true, NextOffset: -1,
			}, nil
		},
	}
	m, _ := newRealModel(t, 180, 50, f)
	loadPlaylistsReady(t, m)
	pressRun(t, m, "j")
	pressRun(t, m, "enter")
	got := m.View()
	if !strings.Contains(got, "2 entries not playable") {
		t.Errorf("skipped entries must be counted in a footnote row, got:\n%s", got)
	}
	if !strings.Contains(got, "showing 1 of 42") {
		t.Errorf("a truncated resolve must be rendered as a subset, got:\n%s", got)
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
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	*m = updated.(Model)
	if !strings.Contains(m.View(), "loading tracks") {
		t.Errorf("the contents panel must show a loading state, got:\n%s", m.View())
	}
	runCatalogCmd(t, m, cmd)
	if m.section != SectionTracks {
		t.Fatal("opening a playlist must land on the Tracks view")
	}
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

// --- search: the input-only window, the debounced query, the results view ---

// searchGroupFixtures serves one page per query with a track, an album and a
// playlist, so grouped rendering can be asserted end to end.
func searchGroupFixtures(f *stubFetch) {
	f.searchFn = func(query string, offset int) (library.SearchPage, error) {
		if offset != 0 {
			return library.SearchPage{}, errors.New("unexpected offset — no prefetch")
		}
		return searchGroupFixturesPage(query, offset), nil
	}
	f.playlistsFn = func(offset int) (library.PlaylistPage, error) {
		if offset != 0 {
			return library.PlaylistPage{}, errors.New("unexpected offset — no prefetch")
		}
		return library.PlaylistPage{
			Playlists: []library.PlaylistSummary{
				{ID: "pl2", Name: "Road Trip", Owner: "nahuel", Private: false, TrackCount: 17},
			},
			Total: 1, NextOffset: -1,
		}, nil
	}
}

func searchGroupFixturesPage(query string, offset int) library.SearchPage {
	return library.SearchPage{
		Query: query,
		Tracks: []library.Track{
			{ID: realTrackID, Title: "So What", Artist: "Miles Davis", Album: "Kind of Blue"},
			{ID: realTrackID2, Title: "Blue in Green", Artist: "Miles Davis", Album: "Kind of Blue"},
		},
		Albums:     []library.AlbumSummary{{ID: "al1", Name: "Kind of Blue", Artist: "Miles Davis"}},
		Playlists:  []library.PlaylistSummary{{ID: "pl1", Name: "Bebop Essentials", Owner: "nahuel", Private: true, TrackCount: 42}},
		Totals:     library.SearchTotals{Tracks: 25, Albums: 4, Playlists: 2},
		NextOffset: -1,
	}
}

// TestSearchTypingDrivesSearch: typing arms a ~250 ms debounce; a shorter
// query never fires; every keystroke re-arms it, so a stale tick is dropped.
func TestSearchTypingDrivesSearch(t *testing.T) {
	f := &stubFetch{}
	searchGroupFixtures(f)
	m, _ := newRealModel(t, 180, 50, f)

	press(t, m, "/")
	if m.win != winSearch {
		t.Fatal("`/` should open the search window")
	}
	typeIn(t, m, "r")
	fireDebounce(t, m, m.sDebounce)
	if calls := strings.Join(f.calls, ","); calls != "" {
		t.Fatalf("a 1-char query must not search, made [%s]", calls)
	}

	typeIn(t, m, "a") // "ra" — armed, not fired yet
	if calls := strings.Join(f.calls, ","); calls != "" {
		t.Fatalf("the debounce must not fire before its tick, made [%s]", calls)
	}
	fireDebounce(t, m, m.sDebounce)
	if got := strings.Join(f.calls, ","); got != `search "ra" 0` {
		t.Fatalf("the debounce tick must run the query once, made [%s]", got)
	}
	if m.sQuery != "ra" || !m.sView {
		t.Errorf("after the tick the query must be applied and the results view on: q=%q sView=%v", m.sQuery, m.sView)
	}

	staleCalls := len(f.calls)
	typeIn(t, m, "i")                 // "rai" re-arms the debounce
	fireDebounce(t, m, m.sDebounce-1) // a stale tick must be dropped
	if got := len(f.calls); got != staleCalls {
		t.Errorf("a stale debounce tick must not search, calls went %d -> %d", staleCalls, got)
	}
	fireDebounce(t, m, m.sDebounce)
	if got := strings.Join(f.calls, ","); got != `search "ra" 0,search "rai" 0` {
		t.Errorf("the new tick must run the new query, made [%s]", got)
	}
}

// TestSearchStaleResponseDropped: answers are matched by query — a response
// for an older query must not overwrite the current results.
func TestSearchStaleResponseDropped(t *testing.T) {
	f := &stubFetch{}
	searchGroupFixtures(f)
	m, _ := newRealModel(t, 180, 50, f)
	press(t, m, "/")
	typeSearch(t, m, "rain")
	if got := strings.Join(f.calls, ","); got != `search "rain" 0` {
		t.Fatalf("setup: made [%s]", got)
	}

	stale := library.SearchPage{Query: "old", Tracks: []library.Track{
		{ID: "stale1234567890123456", Title: "Stale Track", Artist: "Nobody"},
	}}
	updated, _ := m.Update(searchMsg{query: "old", offset: 0, page: stale})
	*m = updated.(Model)
	if len(m.sTracks) != 2 || m.sTracks[0].Title != "So What" {
		t.Errorf("a stale response must be dropped, tracks are %+v", m.sTracks)
	}
	if got := m.View(); !strings.Contains(got, "So What") || strings.Contains(got, "Stale Track") {
		t.Errorf("the view must keep the current results, got:\n%s", got)
	}
}

// TestSearchResultsReplaceTheListGrouped: the results render in the main list
// — tracks, then albums, then playlists — with the context strip naming the
// query and the counts, and the section row untouched.
func TestSearchResultsReplaceTheListGrouped(t *testing.T) {
	f := &stubFetch{}
	searchGroupFixtures(f)
	m, _ := newRealModel(t, 180, 50, f)
	loadPlaylistsReady(t, m)

	press(t, m, "/")
	typeSearch(t, m, "rain")
	got := stripANSI(m.View())
	if !strings.Contains(got, "So What") || !strings.Contains(got, "Blue in Green") {
		t.Errorf("the track results must render in the list, got:\n%s", got)
	}
	if !strings.Contains(got, "Kind of Blue") || !strings.Contains(got, "Bebop Essentials") {
		t.Errorf("album and playlist results must render after the tracks, got:\n%s", got)
	}
	if !strings.Contains(got, "2 tracks · 1 album · 1 playlist") {
		t.Errorf("the context strip must name the counts, got:\n%s", got)
	}
	if m.section != SectionPlaylists {
		t.Errorf("the results view must not switch the section, got %v", m.section)
	}
	// the section row still renders Tracks │ Playlists — no Search cell
	if row := stripANSI(lines(m)[0]); strings.Contains(row, "Search") || strings.Contains(row, "Queue") {
		t.Errorf("the section row must never grow a Search or Queue cell: %q", row)
	}
	// the cursor sits on the first result row
	if m.cursor != 0 {
		t.Errorf("results must reset the cursor to 0, got %d", m.cursor)
	}
}

// TestSearchEnterClosesWindowLeavesCursorOnResults: enter in the window
// closes it and leaves the cursor on the results, ready to navigate.
func TestSearchEnterClosesWindowLeavesCursorOnResults(t *testing.T) {
	f := &stubFetch{}
	searchGroupFixtures(f)
	m, _ := newRealModel(t, 180, 50, f)
	press(t, m, "/")
	typeSearch(t, m, "rain")
	if m.win != winSearch {
		t.Fatal("setup: the window should still be open")
	}
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	*m = updated.(Model)
	if m.win != winNone {
		t.Errorf("enter must close the window, win = %v", m.win)
	}
	if !m.sView {
		t.Error("enter must leave the results view on")
	}
	if m.cursor != 0 {
		t.Errorf("the cursor must sit on the results, got %d", m.cursor)
	}
	if got := stripANSI(m.View()); !strings.Contains(got, "So What") {
		t.Errorf("the results must still be on screen, got:\n%s", got)
	}
}

// TestSearchEscReturnsToPreviousSection: esc in the window returns to the
// previous section view — the results are discarded, cursor restored.
func TestSearchEscReturnsToPreviousSection(t *testing.T) {
	f := &stubFetch{}
	searchGroupFixtures(f)
	m, _ := newRealModel(t, 180, 50, f)
	loadPlaylistsReady(t, m)
	press(t, m, "j") // cursor on the first playlist row
	before := m.View()
	preCursor := m.cursor

	press(t, m, "/")
	typeSearch(t, m, "rain")
	if !m.sView {
		t.Fatal("setup: results should be showing")
	}
	pressKey(t, m, tea.KeyEsc)
	if m.win != winNone || m.sView {
		t.Errorf("esc must close the window and discard the results: win=%v sView=%v", m.win, m.sView)
	}
	if m.cursor != preCursor {
		t.Errorf("esc must restore the cursor, got %d want %d", m.cursor, preCursor)
	}
	if got := m.View(); got != before {
		t.Errorf("esc must return to the previous section view exactly:\n%s", got)
	}
}

// TestSearchListEscReturnsFromResults: with the window already closed, esc on
// the results view goes back to the previous section view.
func TestSearchListEscReturnsFromResults(t *testing.T) {
	f := &stubFetch{}
	searchGroupFixtures(f)
	m, _ := newRealModel(t, 180, 50, f)
	press(t, m, "/")
	typeSearch(t, m, "rain")
	pressKey(t, m, tea.KeyEnter) // close the window on the results
	pressKey(t, m, tea.KeyEsc)
	if m.sView {
		t.Error("esc on the results must return to the previous section view")
	}
	if got := stripANSI(m.View()); !strings.Contains(got, "playlists") {
		t.Errorf("the playlists navigation should be back, got:\n%s", got)
	}
}

// TestSearchEnterOnResultsActs: a track plays, a playlist opens its tracks,
// and an album honestly says it does not open — without leaving the results.
func TestSearchEnterOnResultsActs(t *testing.T) {
	f := &stubFetch{
		itemsFn: func(id string, offset int) (library.ItemsPage, error) {
			return library.ItemsPage{Tracks: []library.Track{
				{ID: realTrackID, Title: "So What", Artist: "Miles Davis", Album: "Kind of Blue"},
			}, Total: 1, NextOffset: -1}, nil
		},
	}
	searchGroupFixtures(f)
	m, pl := newRealModel(t, 180, 50, f)
	press(t, m, "/")
	typeSearch(t, m, "rain")
	pressKey(t, m, tea.KeyEnter) // close the window on the results

	// rows: TRACKS header, 2 tracks, ALBUMS header, album, PLAYLISTS header, playlist
	pressRun(t, m, "j") // onto the first track
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	*m = updated.(Model)
	if len(pl.played) != 1 || pl.played[0].ID != realTrackID {
		t.Errorf("enter on a track result must play it, played %+v", pl.played)
	}
	if m.section != SectionPlaylists || !m.sView {
		t.Errorf("playing a result must not switch the section or leave the results: %v/%v", m.section, m.sView)
	}

	pressRun(t, m, "j") // onto track 2
	pressRun(t, m, "j") // onto the ALBUMS header
	pressRun(t, m, "j") // onto the album row
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	*m = updated.(Model)
	if got := stripANSI(lines(m)[49]); !strings.Contains(got, "albums do not open") {
		t.Errorf("an album result must say honestly that it does not open, got %q", got)
	}
	if m.section != SectionPlaylists || !m.sView {
		t.Errorf("an unopenable album must not move the user: %v/%v", m.section, m.sView)
	}

	pressRun(t, m, "j") // PLAYLISTS header
	pressRun(t, m, "j") // playlist row
	pressRun(t, m, "enter")
	if m.section != SectionTracks {
		t.Errorf("enter on a playlist result must open its tracks, section = %v", m.section)
	}
	if m.sView {
		t.Error("opening the playlist leaves the results view")
	}
	if got := stripANSI(m.View()); !strings.Contains(got, "So What") {
		t.Errorf("the playlist contents should be on screen, got:\n%s", got)
	}
}

// TestSearchRateLimitRowInResults: a 429 shows the rate-limit row with `r` to
// retry in the results view, and nothing retries on its own.
func TestSearchRateLimitRowInResults(t *testing.T) {
	f := &stubFetch{
		searchFn: func(query string, offset int) (library.SearchPage, error) {
			return library.SearchPage{}, &library.APIError{Status: 429, RateLimited: true}
		},
	}
	m, _ := newRealModel(t, 180, 50, f)
	press(t, m, "/")
	typeSearch(t, m, "rain")
	pressKey(t, m, tea.KeyEnter) // close the window on the results
	got := stripANSI(m.View())
	if !strings.Contains(got, "rate limited · press r to retry") {
		t.Errorf("the rate-limit row must offer `r` to retry, got:\n%s", got)
	}
	if !m.sRateLimited {
		t.Error("the rate-limit state must stay inspectable")
	}
	// no retry loop: scrolling without `r` never re-requests
	pressRun(t, m, "G")
	if got := len(f.calls); got != 1 {
		t.Errorf("a rate-limited result must not retry by itself, made %d calls", got)
	}
	f.searchFn = func(query string, offset int) (library.SearchPage, error) {
		return searchGroupFixturesPage(query, offset), nil
	}
	pressRun(t, m, "r")
	if got := len(f.calls); got != 2 {
		t.Errorf("`r` must retry the query once, made %d calls", got)
	}
	if got := stripANSI(m.View()); !strings.Contains(got, "So What") {
		t.Errorf("the retried results should render, got:\n%s", got)
	}
}

// --- search: offset pagination under the 10-result cap ---

func TestCatalogSearchPaginatesByOffsetUnderTheCap(t *testing.T) {
	f := &stubFetch{}
	searchFixtures(f)
	m, _ := newRealModel(t, 180, 50, f)

	press(t, m, "/")
	typeSearch(t, m, "blue")
	if got := strings.Join(f.calls, ","); got != `search "blue" 0` {
		t.Fatalf("first search made [%s], want one capped request at offset 0", got)
	}
	got := m.View()
	if !strings.Contains(got, "So What") || !strings.Contains(got, "Blue in Green") {
		t.Errorf("the results view should list the page's tracks, got:\n%s", got)
	}
	pressKey(t, m, tea.KeyEnter) // close the window on the results

	// scrolling the results view to the last row drives the next offset page
	pressRun(t, m, "G")
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
	typeSearch(t, m, "zzzz")
	got := m.View()
	if !strings.Contains(got, "nothing matches") {
		t.Errorf("an empty search shows the empty state, got:\n%s", got)
	}
	if strings.Contains(got, "TRACKS") {
		t.Errorf("an empty search must not render empty groups, got:\n%s", got)
	}
}

// TestRealSearchWindowStaysInputOnly: even once the results are in the list,
// the floating window renders only its prompt and one hint line.
func TestRealSearchWindowStaysInputOnly(t *testing.T) {
	f := &stubFetch{}
	searchGroupFixtures(f)
	m, _ := newRealModel(t, 180, 50, f)
	press(t, m, "/")
	typeSearch(t, m, "rain")
	inner := windowInner(t, m)
	for _, banned := range []string{"So What", "Bebop Essentials", "Kind of Blue"} {
		if strings.Contains(inner, banned) {
			t.Errorf("the window must never render result rows, found %q inside it:\n%s", banned, inner)
		}
	}
	if !strings.Contains(inner, "> ") || !strings.Contains(inner, "rain") {
		t.Errorf("the window should show the prompt with the typed query, got:\n%s", inner)
	}
}

// --- mock mode is untouched ---

func TestMockModeHasNoCatalog(t *testing.T) {
	data := mock.New()
	m := New(config.Config{ClientID: "c", CallbackPort: 8888}, data, data, testClock)
	if m.fetch != nil {
		t.Error("SPOTIFY_TUI_MOCK=1 must keep the mock data source: no catalog fetch")
	}
	if m.section != SectionTracks {
		t.Error("mock mode keeps the artboard start inside the open track list")
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

		// drill-down: two playlists, both resolve with contents now
		pressRun(t, m, "j")
		pressRun(t, m, "enter")
		check("first playlist")
		pressKey(t, m, tea.KeyEsc)
		pressRun(t, m, "j")
		pressRun(t, m, "enter")
		check("second playlist")
		pressKey(t, m, tea.KeyEsc)

		// the search window over the list, results behind it
		press(t, m, "/")
		typeSearch(t, m, "blue")
		check("search window with results")
		pressKey(t, m, tea.KeyEnter) // close the window on the results
		check("search results view")
		pressKey(t, m, tea.KeyEsc)

		// the sections toggle and the drawer coexists (no Queue section any more)
		pressKey(t, m, tea.KeyTab)
		check("tracks section")
		pressKey(t, m, tea.KeyTab)
		press(t, m, "q")
		check("playlists with drawer")
	}
}
