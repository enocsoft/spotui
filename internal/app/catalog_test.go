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
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/enocsoft/spotui/internal/config"
	"github.com/enocsoft/spotui/internal/library"
	"github.com/enocsoft/spotui/internal/library/mock"
	"github.com/enocsoft/spotui/internal/ui/theme"
)

const (
	realTrackID  = "4uLU6hMCjMI75M1A2tKUQC"
	realTrackID2 = "3uLU6hMCjMI75M1A2tKUQC"
)

// --- stubs ---

// stubFetch records every catalog call and serves canned pages/errors.
type stubFetch struct {
	playlistsFn   func(offset int) (library.PlaylistPage, error)
	albumsFn      func(offset int) (library.AlbumPage, error)
	savedTracksFn func(offset int) (library.SavedTracksPage, error)
	itemsFn       func(id string, offset int) (library.ItemsPage, error)
	albumItemsFn  func(id string, offset int) (library.ItemsPage, error)
	searchFn      func(query string, offset int) (library.SearchPage, error)
	savedFn       func(id string, saved bool) error
	calls         []string
}

func (f *stubFetch) Playlists(ctx context.Context, offset int) (library.PlaylistPage, error) {
	f.calls = append(f.calls, fmt.Sprintf("playlists %d", offset))
	return f.playlistsFn(offset)
}

func (f *stubFetch) SavedAlbums(ctx context.Context, offset int) (library.AlbumPage, error) {
	f.calls = append(f.calls, fmt.Sprintf("albums %d", offset))
	return f.albumsFn(offset)
}

func (f *stubFetch) SavedTracks(ctx context.Context, offset int) (library.SavedTracksPage, error) {
	f.calls = append(f.calls, fmt.Sprintf("savedTracks %d", offset))
	return f.savedTracksFn(offset)
}

func (f *stubFetch) Items(ctx context.Context, id string, offset int) (library.ItemsPage, error) {
	f.calls = append(f.calls, fmt.Sprintf("items %s %d", id, offset))
	return f.itemsFn(id, offset)
}

func (f *stubFetch) AlbumItems(ctx context.Context, id string, offset int) (library.ItemsPage, error) {
	f.calls = append(f.calls, fmt.Sprintf("albumItems %s %d", id, offset))
	return f.albumItemsFn(id, offset)
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
// to the model. Pure timer commands (toast ticks and such) are dropped —
// bubbletea schedules them for real — so after a catalog message lands, its
// own returned command (a toast) is not followed. Player results and the
// plays a resolved context kicks off run through runPlayerCmds.
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
	case playerMsg:
		// the async result of a player command: land it like the runtime does
		updated, _ := m.Update(msg)
		*m = updated.(Model)
	case playlistsMsg:
		updated, cmd := m.Update(msg)
		*m = updated.(Model)
		runPlayerCmds(t, m, cmd) // a resolved context may play its first track
	case albumsMsg:
		updated, cmd := m.Update(msg)
		*m = updated.(Model)
		runPlayerCmds(t, m, cmd)
	case savedTracksMsg:
		updated, cmd := m.Update(msg)
		*m = updated.(Model)
		runPlayerCmds(t, m, cmd)
	case itemsMsg:
		updated, cmd := m.Update(msg)
		*m = updated.(Model)
		runPlayerCmds(t, m, cmd)
	case searchMsg:
		updated, cmd := m.Update(msg)
		*m = updated.(Model)
		runPlayerCmds(t, m, cmd)
	case savedMsg:
		updated, cmd := m.Update(msg)
		*m = updated.(Model)
		runPlayerCmds(t, m, cmd)
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

// --- T23: saved albums as a browsable section ---

// savedAlbumFixtures serves one playlist and two saved-album pages (55
// total, 50 per page — the same lazy paging playlists follow), plus album
// contents through the resolver path.
func savedAlbumFixtures(f *stubFetch) {
	f.playlistsFn = func(offset int) (library.PlaylistPage, error) {
		if offset != 0 {
			return library.PlaylistPage{}, errors.New("unexpected offset — no prefetch")
		}
		return library.PlaylistPage{
			Playlists: []library.PlaylistSummary{{ID: "pl1", Name: "Mine", Owner: "nahuel", Private: true, TrackCount: 1}},
			Total:     1, NextOffset: -1,
		}, nil
	}
	f.albumsFn = func(offset int) (library.AlbumPage, error) {
		switch offset {
		case 0:
			return library.AlbumPage{Albums: []library.AlbumSummary{
				{ID: "al1", Name: "Kind of Blue", Artist: "Miles Davis", Year: "1959", TrackCount: 5},
			}, Total: 2, Offset: 0, NextOffset: 50}, nil
		case 50:
			return library.AlbumPage{Albums: []library.AlbumSummary{
				{ID: "al2", Name: "Maiden Voyage", Artist: "Herbie Hancock", Year: "1965", TrackCount: 5},
			}, Total: 2, Offset: 50, NextOffset: -1}, nil
		default:
			return library.AlbumPage{}, errors.New("unexpected offset — the catalog must never prefetch")
		}
	}
	f.albumItemsFn = func(id string, offset int) (library.ItemsPage, error) {
		if id != "al1" && id != "al2" {
			return library.ItemsPage{}, errors.New("unexpected album items request")
		}
		return library.ItemsPage{Tracks: []library.Track{
			{ID: realTrackID, Title: "So What", Artist: "Miles Davis", Album: "Kind of Blue"},
		}, Total: 1, NextOffset: -1}, nil
	}
}

// The library root renders the second ALBUMS group under PLAYLISTS, the
// group stays collapsed (and fetches nothing) until it is expanded, and
// expansion fetches the first page lazily — exactly the playlists behaviour.
func TestSavedAlbumsSectionRendersAndPagesLazily(t *testing.T) {
	f := &stubFetch{}
	savedAlbumFixtures(f)
	m, _ := newRealModel(t, 180, 50, f)
	loadPlaylistsReady(t, m)

	rows := m.visibleRows()
	if rows[0].kind != rowHeader || !strings.Contains(rows[0].text, "PLAYLISTS") {
		t.Fatalf("the library root starts with the playlists group, rows = %+v", rows)
	}
	last := rows[len(rows)-1]
	if last.kind != rowHeader || !strings.Contains(last.text, "SAVED") {
		t.Fatalf("the library root ends with the SAVED group header (T29), rows = %+v", rows)
	}
	alb := rows[len(rows)-2]
	if alb.kind != rowHeader || !strings.Contains(alb.text, "ALBUMS") {
		t.Fatalf("the SAVED group sits right after the ALBUMS group, rows = %+v", rows)
	}
	if got := strings.Join(f.calls, ","); strings.Contains(got, "albums") {
		t.Fatalf("a collapsed ALBUMS group must not fetch, made [%s]", got)
	}

	// expanding the group fetches exactly the first page
	m.cursor = len(m.visibleRows()) - 2 // the ALBUMS header, SAVED collapsed below it
	pressRun(t, m, "l")
	if got := strings.Join(f.calls, ","); got != "playlists 0,albums 0" {
		t.Fatalf("expanding ALBUMS must fetch the first page lazily, made [%s]", got)
	}
	got := stripANSI(m.View())
	if !strings.Contains(got, "Kind of Blue") {
		t.Errorf("the albums group should list its albums, got:\n%s", got)
	}
	if !strings.Contains(got, "Miles Davis · 1959 · 5") {
		t.Errorf("an album row shows artist · year · track count, got:\n%s", got)
	}

	// the second page loads only when the cursor reaches the last row
	pressRun(t, m, "G")
	if got := strings.Join(f.calls, ","); got != "playlists 0,albums 0,albums 50" {
		t.Fatalf("after scrolling to the end the catalog made [%s], want one lazy follow-up albums page", got)
	}
	if !strings.Contains(m.View(), "Maiden Voyage") {
		t.Errorf("the followed page should render its album, got:\n%s", m.View())
	}
	if got := stripANSI(lines(m)[0]); strings.Contains(got, "Albums") {
		t.Errorf("albums live inside the Playlists view: the section row must never grow an Albums cell, got %q", got)
	}

	// width discipline holds with the albums group expanded too
	for _, size := range [][2]int{{180, 50}, {140, 40}, {80, 24}} {
		w, h := size[0], size[1]
		wm, _ := newRealModel(t, w, h, f)
		loadPlaylistsReady(t, wm)
		wm.cursor = len(wm.visibleRows()) - 2 // the ALBUMS header, SAVED collapsed below
		pressRun(t, wm, "l")
		for i, line := range lines(wm) {
			if lw := visibleWidth(line); lw > w {
				t.Errorf("w=%d: album row %d is %d columns wide: %q", w, i, lw, stripANSI(line))
			}
		}
		if n := len(lines(wm)); n != h {
			t.Errorf("w=%d: rendered %d lines, want %d", w, n, h)
		}
	}
}

// enter on a saved album opens its tracks through the existing album
// resolver path (spotify:album:<id>) — the same path search albums use —
// and a row of the open album plays.
func TestSavedAlbumOpensItsTracksAndPlays(t *testing.T) {
	f := &stubFetch{}
	savedAlbumFixtures(f)
	m, pl := newRealModel(t, 180, 50, f)
	loadPlaylistsReady(t, m)

	m.cursor = len(m.visibleRows()) - 2 // the ALBUMS header, SAVED collapsed below
	pressRun(t, m, "l")                 // expand albums, first page lands
	pressRun(t, m, "j")                 // onto the album row
	pressRun(t, m, "enter")

	if m.section != SectionTracks || m.sView {
		t.Fatalf("enter on a saved album must open its tracks on the Tracks view, section=%v sView=%v", m.section, m.sView)
	}
	if got := stripANSI(m.View()); !strings.Contains(got, "So What") {
		t.Errorf("the open album must render its tracks, got:\n%s", got)
	}
	joined := strings.Join(f.calls, ",")
	if !strings.Contains(joined, "albumItems al1 0") {
		t.Errorf("a saved album must open through the album resolver path, made [%s]", joined)
	}
	if strings.Contains(joined, "items al1") {
		t.Errorf("the playlist items path must not fire for an album, made [%s]", joined)
	}

	pressRun(t, m, "enter")
	if len(pl.played) != 1 || pl.played[0].ID != realTrackID {
		t.Errorf("enter on an album track must play it, played %+v", pl.played)
	}
}

// --- T24: search results presentation ---

// withTrueColor pins the renderer's color profile for one test: without a
// TTY lipgloss renders with no ANSI at all, so style-level assertions (the
// bold match, the focused band) would be vacuous. The profile is restored
// so every other test keeps its detected behaviour.
func withTrueColor(t *testing.T) {
	t.Helper()
	prev := lipgloss.DefaultRenderer().ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(prev) })
}

// listRegionLines is the full-screen list's own slice of the frame: the
// listBody rows, below the top bar, its rule and the context strip's rule.
func listRegionLines(m *Model) []string {
	ls := lines(m)
	h := m.listHeight()
	if 4+h > len(ls) {
		h = len(ls) - 4
	}
	return ls[4 : 4+h]
}

// bandedRows counts the lines carrying a truecolor background band (the
// focused row's surface tint): exactly one focused row, wherever the cursor
// stands.
func bandedRows(ls []string) int {
	n := 0
	for _, l := range ls {
		if strings.Contains(l, "\x1b[48;2;") {
			n++
		}
	}
	return n
}

// The matched substring of every result label is bold; the rest of the label
// keeps the plain value role — bold is a weight, not a new colour.
func TestSearchMatchIsBoldAndTheRestIsNot(t *testing.T) {
	withTrueColor(t)
	f := &stubFetch{}
	searchGroupFixtures(f)
	m, _ := newRealModel(t, 180, 50, f)
	press(t, m, "/")
	typeSearch(t, m, "blue")
	pressKey(t, m, tea.KeyEnter) // close the window on the results

	// rows: 0 TRACKS header, 1 So What, 2 Blue in Green, 3 ALBUMS header,
	// 4 Kind of Blue, 5 PLAYLISTS header, 6 Bebop Essentials
	if len(m.visibleRows()) != 7 {
		t.Fatalf("setup rows = %d, want 7", len(m.visibleRows()))
	}
	ls := listRegionLines(m)

	track := ls[2]
	if !strings.Contains(track, matchedStyle.Render("Blue")) {
		t.Errorf("the matched substring of the track label must be bold, got %q", track)
	}
	if !strings.Contains(track, theme.ValueStyle.Render(" in Green")) {
		t.Errorf("the unmatched rest of the label must keep the value role, got %q", track)
	}
	if strings.Contains(track, theme.ValueStyle.Render("Blue")) {
		t.Errorf("the matched substring must not render in the plain value style: %q", track)
	}
	if strings.Count(track, matchedStyle.Render("Blue")) != 1 {
		t.Errorf("exactly one bold span per label, got %q", track)
	}

	// case-insensitive: the query in any casing bolds the label's own casing
	press(t, m, "/")
	typeSearch(t, m, "BLUE")
	pressKey(t, m, tea.KeyEnter)
	ls = listRegionLines(m)
	if !strings.Contains(ls[2], matchedStyle.Render("Blue")) {
		t.Errorf("a BLUE query must bold the row's own 'Blue', got %q", ls[2])
	}

	// album and playlist labels bold too; non-matching labels stay plain
	press(t, m, "/")
	typeSearch(t, m, "blue")
	pressKey(t, m, tea.KeyEnter)
	ls = listRegionLines(m)
	if !strings.Contains(ls[4], matchedStyle.Render("Blue")) {
		t.Errorf("the album label's matched substring must be bold, got %q", ls[4])
	}
	if strings.Contains(ls[1], matchedStyle.Render("So What")) {
		t.Errorf("a non-matching label must not render bold: %q", ls[1])
	}
	if strings.Contains(ls[6], matchedStyle.Render("Bebop")) {
		t.Errorf("a non-matching playlist label must not render bold: %q", ls[6])
	}
}

// Group headers sit in a visibly different role from the result rows, the
// cursor stays exactly one focused row, and the cursor moving between
// groups is obvious without colour alone: the header's glyph flips to the
// selected cue while it is focused.
func TestSearchGroupHeadersDifferFromRowsAndCursorGlyph(t *testing.T) {
	withTrueColor(t)
	f := &stubFetch{}
	searchGroupFixtures(f)
	m, _ := newRealModel(t, 180, 50, f)
	press(t, m, "/")
	typeSearch(t, m, "blue")
	pressKey(t, m, tea.KeyEnter)

	rows := m.visibleRows()
	if !strings.Contains(rows[0].text, theme.GlyphExpanded+" TRACKS") {
		t.Errorf("the group header carries the expanded glyph and its name, got %q", rows[0].text)
	}
	ls := listRegionLines(m)
	if !strings.Contains(ls[3], theme.LabelStyle.Render(" \u25be ALBUMS")) {
		t.Errorf("the ALBUMS header renders in the muted label role, got %q", stripANSI(ls[3]))
	}

	// exactly one focused row, and the focused header's glyph is the selected cue
	if n := bandedRows(ls); n != 1 {
		t.Fatalf("exactly one focused row, got %d:\n%s", n, strings.Join(ls, "\n"))
	}
	if !strings.Contains(stripANSI(ls[0]), theme.GlyphSelected+" TRACKS") {
		t.Errorf("the focused TRACKS header must carry the selected glyph, got %q", stripANSI(ls[0]))
	}

	// moving to the ALBUMS header moves the glyph cue with the cursor
	pressRun(t, m, "j")
	pressRun(t, m, "j")
	pressRun(t, m, "j")
	ls = listRegionLines(m)
	if !strings.Contains(stripANSI(ls[3]), theme.GlyphSelected+" ALBUMS") {
		t.Errorf("the focused ALBUMS header must carry the selected glyph, got %q", stripANSI(ls[3]))
	}
	if !strings.Contains(stripANSI(ls[0]), theme.GlyphExpanded+" TRACKS") {
		t.Errorf("the unfocused TRACKS header reads expanded again, got %q", stripANSI(ls[0]))
	}
	if n := bandedRows(ls); n != 1 {
		t.Errorf("exactly one focused row after moving between groups, got %d", n)
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
// and an album opens its tracks too (T22) — the results view acts on every
// row kind.
func TestSearchEnterOnResultsActs(t *testing.T) {
	f := &stubFetch{
		itemsFn: func(id string, offset int) (library.ItemsPage, error) {
			return library.ItemsPage{Tracks: []library.Track{
				{ID: realTrackID, Title: "So What", Artist: "Miles Davis", Album: "Kind of Blue"},
			}, Total: 1, NextOffset: -1}, nil
		},
		albumItemsFn: func(id string, offset int) (library.ItemsPage, error) {
			return library.ItemsPage{Tracks: []library.Track{
				{ID: realTrackID2, Title: "Freddie Freeloader", Artist: "Miles Davis", Album: "Kind of Blue"},
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
	updated, playCmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	*m = updated.(Model)
	runPlayerCmds(t, m, playCmd) // the play is a command now: run it like the runtime does
	if len(pl.played) != 1 || pl.played[0].ID != realTrackID {
		t.Errorf("enter on a track result must play it, played %+v", pl.played)
	}
	if m.section != SectionPlaylists || !m.sView {
		t.Errorf("playing a result must not switch the section or leave the results: %v/%v", m.section, m.sView)
	}

	pressRun(t, m, "j") // PLAYLISTS header
	pressRun(t, m, "j")
	pressRun(t, m, "j")
	pressRun(t, m, "j")
	pressRun(t, m, "j") // onto the playlist row
	pressRun(t, m, "enter")
	if m.section != SectionTracks || m.sView {
		t.Errorf("enter on a playlist result must open its tracks, section = %v sView = %v", m.section, m.sView)
	}
	if got := stripANSI(m.View()); !strings.Contains(got, "So What") {
		t.Errorf("the playlist contents should be on screen, got:\n%s", got)
	}

	// esc back to the navigation, re-run the search under a fresh query (the
	// debounce skips a repeated query), and open the album: enter on a
	// playlist already left the results, so the album ride needs its own pass
	// (T22).
	pressKey(t, m, tea.KeyEsc)
	press(t, m, "/")
	typeSearch(t, m, "rainy")
	pressKey(t, m, tea.KeyEnter)
	pressRun(t, m, "j")
	pressRun(t, m, "j")
	pressRun(t, m, "j")
	pressRun(t, m, "j") // onto the album row
	pressRun(t, m, "enter")
	if m.section != SectionTracks || m.sView {
		t.Errorf("enter on an album result must open its tracks, section = %v sView = %v", m.section, m.sView)
	}
	if got := stripANSI(m.View()); !strings.Contains(got, "Freddie Freeloader") {
		t.Errorf("the album contents should be on screen, got:\n%s", got)
	}
	joined := strings.Join(f.calls, ",")
	if !strings.Contains(joined, "albumItems al1 0") {
		t.Errorf("the album must open through the album resolver path, made [%s]", joined)
	}
	if strings.Contains(joined, "items al1") {
		t.Errorf("the playlist items path must not fire for an album, made [%s]", joined)
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

// --- T22: an album row opens its tracks through the resolver ---

// An album row in the search results opens its tracks like a playlist: the
// view switches to the track list, the context strip names the album and its
// artist, and the tracks ride the album resolver path (no Web API items).
func TestSearchAlbumRowOpensItsTracks(t *testing.T) {
	f := &stubFetch{
		albumItemsFn: func(id string, offset int) (library.ItemsPage, error) {
			if id != "al1" || offset != 0 {
				return library.ItemsPage{}, errors.New("unexpected album items request")
			}
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
	pressRun(t, m, "j") // onto track 1
	pressRun(t, m, "j") // track 2
	pressRun(t, m, "j") // ALBUMS header
	pressRun(t, m, "j") // onto the album row
	pressRun(t, m, "enter")

	if m.section != SectionTracks || m.sView {
		t.Fatalf("enter on an album row must open its tracks on the Tracks view, section=%v sView=%v", m.section, m.sView)
	}
	got := stripANSI(m.View())
	if !strings.Contains(got, "So What") {
		t.Errorf("the open album must render its tracks, got:\n%s", got)
	}
	if strings.Contains(got, "albums do not open") {
		t.Errorf("the honest-not-yet toast is gone: album rows open now, got:\n%s", got)
	}

	// The strip names the album and its artist with the albums crumb.
	strip := stripANSI(lines(m)[2])
	wantCrumb := "albums " + theme.GlyphCrumb + " Kind of Blue"
	if !strings.Contains(strip, wantCrumb) || !strings.Contains(strip, "Miles Davis") {
		t.Errorf("the context strip must name the album and its artist (crumb albums), got %q", strip)
	}
	if strings.Contains(strip, "private") || strings.Contains(strip, "public") {
		t.Errorf("an album has no owner/privacy fact to show, strip = %q", strip)
	}

	// The album's tracks came through the album path, not the playlist one.
	joined := strings.Join(f.calls, ",")
	if !strings.Contains(joined, "albumItems al1 0") {
		t.Errorf("opening an album must ride the album resolver path, made [%s]", joined)
	}
	if strings.Contains(joined, "items al1") {
		t.Errorf("the playlist items path must not be hit for an album, made [%s]", joined)
	}

	// enter on a row of the open album plays it, like a playlist's rows do.
	pressRun(t, m, "enter")
	if len(pl.played) != 1 || pl.played[0].ID != realTrackID {
		t.Errorf("enter on an album track must play it, played %+v", pl.played)
	}
}

// A resolver failure on an album shows the reason in the error state in
// place — never an empty table, never a success.
func TestSearchAlbumResolveErrorSurfacesTheReason(t *testing.T) {
	f := &stubFetch{
		albumItemsFn: func(id string, offset int) (library.ItemsPage, error) {
			return library.ItemsPage{}, errors.New("invalid status code from context resolve: 502")
		},
	}
	searchGroupFixtures(f)
	m, _ := newRealModel(t, 180, 50, f)
	press(t, m, "/")
	typeSearch(t, m, "rain")
	pressKey(t, m, tea.KeyEnter)

	// rows: TRACKS header (0), So What (1), Blue in Green (2), ALBUMS header (3), album (4)
	pressRun(t, m, "j")
	pressRun(t, m, "j")
	pressRun(t, m, "j")
	pressRun(t, m, "j")
	pressRun(t, m, "enter")

	got := stripANSI(m.View())
	if m.section != SectionTracks {
		t.Fatalf("the album view is the Tracks view, section = %v", m.section)
	}
	if !strings.Contains(got, "✗ could not load") || !strings.Contains(got, "502") {
		t.Errorf("a failed album resolve must show the reason in the error state, got:\n%s", got)
	}
	if len(m.plTracks) != 0 {
		t.Errorf("a failed resolve must not land track rows, tracks = %+v", m.plTracks)
	}
	// The only "So What" the frame may show is the (mock) player bar — never
	// a track row in the list.
	for _, line := range lines(m) {
		plain := stripANSI(line)
		if strings.Contains(plain, "So What") && !strings.Contains(plain, "192 kbps") {
			t.Errorf("a failed resolve must not render the album's tracks in the list, found %q", plain)
		}
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
		t.Error("SPOTUI_MOCK=1 must keep the mock data source: no catalog fetch")
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
		albumItemsFn: func(id string, offset int) (library.ItemsPage, error) {
			return library.ItemsPage{Tracks: []library.Track{
				{ID: realTrackID, Title: "So What", Artist: "Miles Davis", Album: "Kind of Blue", Duration: 562000000000},
			}, Total: 1, NextOffset: -1}, nil
		},
		searchFn: func(query string, offset int) (library.SearchPage, error) {
			return library.SearchPage{
				Query: query, Tracks: []library.Track{
					{ID: realTrackID, Title: "So What", Artist: "Miles Davis", Album: "Kind of Blue", Duration: 562000000000},
				}, Albums: []library.AlbumSummary{
					{ID: "al1", Name: "Kind of Blue — a very long album name that must truncate in the strip", Artist: "Miles Davis Quintet Sextet Orchestra"},
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
		// the album result opens too: its strip must respect the same budget
		pressRun(t, m, "j")
		pressRun(t, m, "j")
		pressRun(t, m, "j") // onto the album row
		pressRun(t, m, "enter")
		check("album view")
		pressKey(t, m, tea.KeyEsc)
		pressKey(t, m, tea.KeyEsc)

		// the sections toggle and the drawer coexists (no Queue section any more)
		pressKey(t, m, tea.KeyTab)
		check("tracks section")
		pressKey(t, m, tea.KeyTab)
		press(t, m, "q")
		check("playlists with drawer")
	}
}
