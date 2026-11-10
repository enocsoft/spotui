package app

// T29: the SAVED group — every track the user saved, inside the Playlists
// view like ALBUMS one group up, and openable in the Tracks view where the
// list IS the saved set: every row renders [x], `l` unsaves, and the row
// disappears only after the server confirms.

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nahuel/spotify-tui/internal/library"
)

// savedGroupFixtures serves one playlist page plus two saved-track pages
// (2 total, 50 per page — the same lazy paging playlists and albums follow).
func savedGroupFixtures(f *stubFetch) {
	f.playlistsFn = func(offset int) (library.PlaylistPage, error) {
		if offset != 0 {
			return library.PlaylistPage{}, errors.New("unexpected offset — no prefetch")
		}
		return library.PlaylistPage{
			Playlists: []library.PlaylistSummary{{ID: "pl1", Name: "Mine", Owner: "nahuel", Private: true, TrackCount: 1}},
			Total:     1, NextOffset: -1,
		}, nil
	}
	f.savedTracksFn = func(offset int) (library.SavedTracksPage, error) {
		switch offset {
		case 0:
			return library.SavedTracksPage{Tracks: []library.Track{
				{ID: realTrackID, Title: "So What", Artist: "Miles Davis", Album: "Kind of Blue", Saved: true},
			}, Total: 2, Offset: 0, NextOffset: 50}, nil
		case 50:
			return library.SavedTracksPage{Tracks: []library.Track{
				{ID: realTrackID2, Title: "Blue in Green", Artist: "Miles Davis", Album: "Kind of Blue", Saved: true},
			}, Total: 2, Offset: 50, NextOffset: -1}, nil
		default:
			return library.SavedTracksPage{}, errors.New("unexpected offset — the catalog must never prefetch")
		}
	}
}

// savedOpenFixtures serves the whole saved set on one page: two tracks, no
// next — the setup for the open-list behaviours (strip, rows, unsave).
func savedOpenFixtures(f *stubFetch, failUnsave bool) {
	savedGroupFixtures(f)
	f.savedTracksFn = func(offset int) (library.SavedTracksPage, error) {
		if offset != 0 {
			return library.SavedTracksPage{}, errors.New("unexpected offset — the catalog must never prefetch")
		}
		return library.SavedTracksPage{Tracks: []library.Track{
			{ID: realTrackID, Title: "So What", Artist: "Miles Davis", Album: "Kind of Blue", Saved: true},
			{ID: realTrackID2, Title: "Blue in Green", Artist: "Miles Davis", Album: "Kind of Blue", Saved: true},
		}, Total: 2, Offset: 0, NextOffset: -1}, nil
	}
	f.savedFn = func(id string, saved bool) error {
		if failUnsave {
			return errors.New("spotify 500 · boom")
		}
		return nil
	}
}

// The library root ends with the collapsed SAVED group; it fetches nothing
// until it is expanded, and expanding fetches exactly the first page.
func TestSavedGroupRendersCollapsedAndExpansionFetchesTheFirstPage(t *testing.T) {
	f := &stubFetch{}
	savedGroupFixtures(f)
	m, _ := newRealModel(t, 180, 50, f)
	loadPlaylistsReady(t, m)

	rows := m.visibleRows()
	last := rows[len(rows)-1]
	if last.kind != rowHeader || !strings.Contains(last.text, "SAVED") {
		t.Fatalf("the library root ends with the SAVED group header, rows = %+v", rows)
	}
	if got := strings.Join(f.calls, ","); strings.Contains(got, "savedTracks") {
		t.Fatalf("a collapsed SAVED group must not fetch, made [%s]", got)
	}

	// expanding the group fetches exactly the first page
	m.cursor = len(m.visibleRows()) - 1
	pressRun(t, m, "l")
	if got := strings.Join(f.calls, ","); got != "playlists 0,savedTracks 0" {
		t.Fatalf("expanding SAVED must fetch the first page lazily, made [%s]", got)
	}
	got := stripANSI(m.View())
	if !strings.Contains(got, "So What") {
		t.Errorf("the SAVED group should list its tracks, got:\n%s", got)
	}
	if strings.Count(got, "[x]") < 1 {
		t.Errorf("the saved group's rows render [x]: the list is the saved set, got:\n%s", got)
	}

	// width discipline holds with the saved group expanded too
	for _, size := range [][2]int{{180, 50}, {140, 40}, {80, 24}} {
		w, h := size[0], size[1]
		wm, _ := newRealModel(t, w, h, f)
		loadPlaylistsReady(t, wm)
		wm.cursor = len(wm.visibleRows()) - 1
		pressRun(t, wm, "l")
		for i, line := range lines(wm) {
			if lw := visibleWidth(line); lw > w {
				t.Errorf("w=%d: saved row %d is %d columns wide: %q", w, i, lw, stripANSI(line))
			}
		}
		if n := len(lines(wm)); n != h {
			t.Errorf("w=%d: rendered %d lines, want %d", w, n, h)
		}
	}
}

// A collapsed SAVED header never interrupts the playlists paging: reaching it
// with the playlist list unfinished pages the playlists, exactly the rule the
// collapsed ALBUMS header follows.
func TestCollapsedSavedHeaderDoesNotBlockPlaylistPaging(t *testing.T) {
	f := &stubFetch{}
	savedGroupFixtures(f)
	f.savedTracksFn = func(offset int) (library.SavedTracksPage, error) {
		return library.SavedTracksPage{}, errors.New("unexpected savedTracks call — the collapsed group must not fetch")
	}
	f.playlistsFn = func(offset int) (library.PlaylistPage, error) {
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
			return library.PlaylistPage{}, errors.New("unexpected offset — no prefetch")
		}
	}
	m, _ := newRealModel(t, 180, 50, f)
	loadPlaylistsReady(t, m)

	pressRun(t, m, "G") // the cursor lands on the collapsed SAVED header
	if got := strings.Join(f.calls, ","); got != "playlists 0,playlists 50" {
		t.Fatalf("a collapsed SAVED header must not interrupt the playlists paging, made [%s]", got)
	}
	if !strings.Contains(m.View(), "Focus") {
		t.Errorf("the followed playlists page should render, got:\n%s", m.View())
	}
}

// The cursor at the expanded group's end pages the saved tracks lazily.
func TestSavedGroupPagesLazily(t *testing.T) {
	f := &stubFetch{}
	savedGroupFixtures(f)
	m, _ := newRealModel(t, 180, 50, f)
	loadPlaylistsReady(t, m)

	m.cursor = len(m.visibleRows()) - 1
	pressRun(t, m, "l") // expand: page 0 lands
	pressRun(t, m, "G") // the cursor reaches the group's last row
	if got := strings.Join(f.calls, ","); got != "playlists 0,savedTracks 0,savedTracks 50" {
		t.Fatalf("after scrolling to the end the catalog made [%s], want one lazy follow-up savedTracks page", got)
	}
	if !strings.Contains(m.View(), "Blue in Green") {
		t.Errorf("the followed page should render its track, got:\n%s", m.View())
	}
}

// enter on the SAVED group opens the saved songs in the Tracks view: the
// strip reads `saved / <whoever> · N tracks`, the table keeps the track-list
// columns (no ADDED), and every row shows [x]; enter on a row plays it.
func TestSavedOpensEveryRowSavedAndPlays(t *testing.T) {
	f := &stubFetch{}
	savedOpenFixtures(f, false)
	m, pl := newRealModel(t, 180, 50, f)
	loadPlaylistsReady(t, m)

	m.cursor = len(m.visibleRows()) - 1
	pressRun(t, m, "l")     // expand, first page lands
	pressRun(t, m, "enter") // enter on the SAVED header opens the saved songs

	if m.section != SectionTracks || m.sView {
		t.Fatalf("enter on the SAVED group must open the Tracks view, section=%v sView=%v", m.section, m.sView)
	}
	ls := listRegionLines(m)
	strip := stripANSI(lines(m)[2])
	if !strings.Contains(strip, "saved / nahuel") || !strings.Contains(strip, "2 tracks") {
		t.Errorf("the context strip must read `saved / nahuel · 2 tracks`, got %q", strip)
	}
	header := stripANSI(ls[0])
	if !strings.Contains(header, "SAVED") {
		t.Errorf("the table keeps the SAVED column, got %q", header)
	}
	if strings.Contains(header, "ADDED") {
		t.Errorf("the saved list's table has no ADDED column, got %q", header)
	}
	rows := 0
	for i, line := range ls {
		if strings.Contains(stripANSI(line), "So What") || strings.Contains(stripANSI(line), "Blue in Green") {
			rows++
			if !strings.Contains(stripANSI(line), "[x]") {
				t.Errorf("row %d is a saved track and must render [x], got %q", i, stripANSI(line))
			}
		}
	}
	if rows != 2 {
		t.Errorf("the saved list renders its 2 tracks, got %d rows:\n%s", rows, strings.Join(ls, "\n"))
	}

	pressRun(t, m, "enter")
	if len(pl.played) != 1 || pl.played[0].ID != realTrackID {
		t.Errorf("enter on a saved track must play it, played %+v", pl.played)
	}
}

// Unsaving removes the row only after the server confirms: the keypress must
// not drop the row, the confirmed answer does.
func TestSavedUnsaveRemovesTheRowOnlyAfterTheServerConfirms(t *testing.T) {
	f := &stubFetch{}
	savedOpenFixtures(f, false)
	m, _ := newRealModel(t, 180, 50, f)
	loadPlaylistsReady(t, m)

	m.cursor = len(m.visibleRows()) - 1
	pressRun(t, m, "l")
	pressRun(t, m, "enter") // open the saved songs; cursor on the first row

	// press `l` but do NOT run the returned command yet: nothing may move
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("l")})
	*m = updated.(Model)
	if cmd == nil {
		t.Fatal("l on a saved track must issue the server toggle")
	}
	if _, busy := m.pendingSaved[realTrackID]; !busy {
		t.Error("the toggle must be pending until the server answers")
	}
	listHas := func(s string) bool {
		for _, line := range listRegionLines(m) {
			if strings.Contains(stripANSI(line), s) {
				return true
			}
		}
		return false
	}
	if !listHas("So What") {
		t.Errorf("no optimistic removal: the row stays until the server confirms, got:\n%s", m.View())
	}

	runCatalogCmd(t, m, cmd) // the server confirms the unsave
	if listHas("So What") {
		t.Errorf("after the confirmation the row must be gone, got:\n%s", m.View())
	}
	if !listHas("Blue in Green") {
		t.Error("the other rows stay")
	}
	if strip := stripANSI(lines(m)[2]); !strings.Contains(strip, "1 tracks") {
		t.Errorf("the strip count follows the confirmed removal, got %q", strip)
	}
}

// A failed unsave reverts (nothing ever moved) and toasts the failure.
func TestSavedUnsaveFailureToastsAndKeepsTheRow(t *testing.T) {
	f := &stubFetch{}
	savedOpenFixtures(f, true)
	m, _ := newRealModel(t, 180, 50, f)
	loadPlaylistsReady(t, m)

	m.cursor = len(m.visibleRows()) - 1
	pressRun(t, m, "l")
	pressRun(t, m, "enter")

	pressRun(t, m, "l") // the toggle fails server-side
	if got := stripANSI(m.View()); !strings.Contains(got, "So What") {
		t.Errorf("a failed unsave keeps the row, got:\n%s", got)
	}
	if got := stripANSI(m.View()); !strings.Contains(got, "✗ save failed") {
		t.Errorf("a failed unsave toasts, got:\n%s", got)
	}
	if _, busy := m.pendingSaved[realTrackID]; busy {
		t.Error("the failed toggle must not stay pending")
	}
}

// An error from the endpoint surfaces its status and message instead of an
// empty list pretending nothing is saved.
func TestSavedEndpointErrorSurfacesTheStatus(t *testing.T) {
	f := &stubFetch{}
	savedGroupFixtures(f)
	f.savedTracksFn = func(offset int) (library.SavedTracksPage, error) {
		return library.SavedTracksPage{}, &library.APIError{Status: 403, Message: "Endpoint is not available for this app"}
	}
	m, _ := newRealModel(t, 180, 50, f)
	loadPlaylistsReady(t, m)

	m.cursor = len(m.visibleRows()) - 1
	pressRun(t, m, "l") // expand: the fetch fails
	got := stripANSI(m.View())
	if !strings.Contains(got, "could not load") {
		t.Errorf("the failed group renders the error state in place, got:\n%s", got)
	}
	if !strings.Contains(got, "spotify 403") || !strings.Contains(got, "Endpoint is not available for this app") {
		t.Errorf("the status and message must surface, got:\n%s", got)
	}
	if strings.Contains(got, "no saved tracks") {
		t.Errorf("an error must never render as an empty list, got:\n%s", got)
	}
}

// TestUnsavingKeepsThePagingOffsetHonest: the saved list pages by offset against
// the server. A confirmed unsave shrinks that list before savedNext, so the next
// page must be requested one slot earlier or the first track is skipped and the
// saved set silently loses a song.
func TestUnsavingKeepsThePagingOffsetHonest(t *testing.T) {
	m := newModel(t, 180, 50)
	m.savedTracks = []library.Track{{ID: realTrackID}, {ID: "0VjIjW4GlUZAMYd2vXMi3b"}}
	m.savedTotal = 137
	m.savedNext = 50

	m.removeSavedTrack(realTrackID)

	if len(m.savedTracks) != 1 || m.savedTracks[0].ID != "0VjIjW4GlUZAMYd2vXMi3b" {
		t.Fatalf("the row was not removed: %+v", m.savedTracks)
	}
	if m.savedTotal != 136 {
		t.Errorf("total = %d, want 136", m.savedTotal)
	}
	if m.savedNext != 49 {
		t.Errorf("next offset = %d, want 49: reusing 50 skips a saved track", m.savedNext)
	}
}
