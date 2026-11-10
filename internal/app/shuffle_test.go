package app

// Shuffle tests (T21 + the C/D revision): `s` toggles shuffle over the open
// view's playable rows. The permutation generator is injected (reversePerm)
// so every order here is deterministic. Semantics under test: the visible
// list never moves — Spotify keeps the list in its own order and shuffles
// only what plays next — while the playback order (what `n`/`p` walk and the
// drawer mirrors) is the shuffled permutation. With shuffle off the playback
// order is the view's base order; there is no session-queue dependence left
// in `n`/`p`, so an exhausted order never dead-ends silently.

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nahuel/spotify-tui/internal/library"
)

// reversePerm is an injected permutation generator: it reverses whatever
// index list it is given, so the shuffle order is fully determined by the
// contract (the generator receives the view's seed and the rest indices).
func reversePerm(n int, seed int64) []int {
	p := make([]int, n)
	for i := range p {
		p[i] = n - 1 - i
	}
	return p
}

// trackIDs lists the ids of the visible track rows in render order.
func trackIDs(m *Model) []string {
	var ids []string
	for _, r := range m.visibleRows() {
		if r.kind == rowTrack {
			ids = append(ids, r.track.ID)
		}
	}
	return ids
}

// trackIDsOf lists the ids of a track slice (the playback order).
func trackIDsOf(tracks []library.Track) []string {
	ids := make([]string, len(tracks))
	for i, t := range tracks {
		ids[i] = t.ID
	}
	return ids
}

// equalIDs reports whether two id slices are identical.
func equalIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestShuffleToggleKeepsTheListInBaseOrder (C): `s` leaves the visible list
// in its base order — the list is where it is, only what plays next is
// shuffled — and the cursor stays on the same row. The playback order (what
// `n`/`p` walk) is the deterministic permutation: the playing track keeps
// its base position, the rest is reversed by the injected generator.
func TestShuffleToggleKeepsTheListInBaseOrder(t *testing.T) {
	m := newModel(t, 180, 50)
	base := trackIDs(m)
	if len(base) != 42 {
		t.Fatalf("mock setup: 42 tracks, got %d", len(base))
	}
	m.perm = reversePerm
	press(t, m, "s")
	if !m.shuffleOn {
		t.Fatal("after `s` shuffle must be on")
	}
	if got := trackIDs(m); !equalIDs(got, base) {
		t.Fatalf("shuffling must not move the visible rows, got %v", got)
	}
	if m.cursor != 0 {
		t.Errorf("toggling shuffle on must not move the cursor: %d, was 0", m.cursor)
	}
	order := trackIDsOf(m.orderedTracks())
	if len(order) != len(base) {
		t.Fatalf("the playback order permutes, never drops rows: got %d, want %d", len(order), len(base))
	}
	if order[0] != "t01" {
		t.Errorf("the playing track keeps its position in the playback order: got[0] = %q, want t01", order[0])
	}
	for i := 1; i < len(order); i++ {
		if want := base[len(base)-i]; order[i] != want {
			t.Errorf("playback position %d: got %q, want %q (deterministic reversal)", i, order[i], want)
		}
	}
	press(t, m, "s")
	if m.shuffleOn {
		t.Fatal("a second `s` must turn shuffle off")
	}
	if got := trackIDs(m); !equalIDs(got, base) {
		t.Fatalf("the visible rows never move, in either direction: got %v", got)
	}
	if order := trackIDsOf(m.orderedTracks()); !equalIDs(order, base) {
		t.Fatalf("shuffle off restores the playback order exactly, got %v", order)
	}
	if m.shuffleOrd != nil {
		t.Error("the permutation must be cleared when shuffle turns off")
	}
	if m.cursor != 0 {
		t.Errorf("toggling shuffle off must not move the cursor: %d, was 0", m.cursor)
	}
}

// TestShuffleCursorDoesNotMoveOnToggle (C): the cursor stays exactly where
// it is — same index, same track — through both directions of the toggle.
func TestShuffleCursorDoesNotMoveOnToggle(t *testing.T) {
	m := newModel(t, 180, 50)
	press(t, m, "j")
	press(t, m, "j") // cursor on t03, row index 2
	if before, ok := m.trackUnderCursor(); !ok || before.ID != "t03" {
		t.Fatalf("mock setup: cursor on t03, got %q (ok=%v)", before.ID, ok)
	}
	cursor := m.cursor
	m.perm = reversePerm
	press(t, m, "s")
	if m.cursor != cursor {
		t.Errorf("toggling shuffle on must not move the cursor: %d, was %d", m.cursor, cursor)
	}
	if after, ok := m.trackUnderCursor(); !ok || after.ID != "t03" {
		t.Errorf("after shuffling the cursor must stay on t03, got %q (ok=%v)", after.ID, ok)
	}
	press(t, m, "s")
	if m.cursor != cursor {
		t.Errorf("toggling shuffle off must not move the cursor: %d, was %d", m.cursor, cursor)
	}
	if after, ok := m.trackUnderCursor(); !ok || after.ID != "t03" {
		t.Errorf("after unshuffling the cursor must stay on t03, got %q (ok=%v)", after.ID, ok)
	}
}

// TestShuffleNextWalksShuffledList: with shuffle on, `n` plays the next row
// of the shuffled playback order — the list itself stays where it is — and
// `p` mirrors it.
func TestShuffleNextWalksShuffledList(t *testing.T) {
	m := newModel(t, 180, 50)
	m.perm = reversePerm
	press(t, m, "s")
	// Play t05 (base index 4): under the reversed permutation it sits at
	// shuffled position 38, so `n` must reach base index 3 (t04) and `p`
	// base index 5 (t06) — the session's own next from t05 would be t06,
	// which only `p` may play here.
	if err := m.player.Play(library.Track{ID: "t05", Title: "Blue in Green"}); err != nil {
		t.Fatalf("mock setup: play t05: %v", err)
	}
	press(t, m, "n")
	if got := m.data.Session().TrackID; got != "t04" {
		t.Errorf("`n` under shuffle must play the next shuffled row (t04), got %q", got)
	}
	press(t, m, "p")
	if got := m.data.Session().TrackID; got != "t05" {
		t.Errorf("`p` under shuffle must walk back to the previous shuffled row (t05), got %q", got)
	}
	press(t, m, "p")
	if got := m.data.Session().TrackID; got != "t06" {
		t.Errorf("`p` under shuffle must walk back again (t06), got %q", got)
	}
	// The playback order is not circular: from the last shuffled row (t02 at
	// position 41) `n` hands over to radio. The mock has no autoplay resolver,
	// so nothing plays and the toast says the order ran out.
	if err := m.player.Play(library.Track{ID: "t02"}); err != nil {
		t.Fatalf("mock setup: play t02: %v", err)
	}
	press(t, m, "n")
	if got := m.data.Session().TrackID; got != "t02" {
		t.Errorf("`n` at the end of the shuffled list must not wrap, session moved to %q", got)
	}
	if !strings.Contains(m.toast.text, "nothing left to play") {
		t.Errorf("an exhausted order must toast instead of dead-ending, got %q", m.toast.text)
	}
}

// TestNextPrevWalkTheBaseOrder: with shuffle off, `n`/`p` walk the view's
// base order through the play path — from t01, `n` plays t02 and `p` returns
// to t01 without ever touching the session's own queue.
func TestNextPrevWalkTheBaseOrder(t *testing.T) {
	m := newModel(t, 180, 50)
	press(t, m, "n")
	if got := m.data.Session().TrackID; got != "t02" {
		t.Errorf("`n` without shuffle must play the next row of the base order (t02), got %q", got)
	}
	press(t, m, "p")
	if got := m.data.Session().TrackID; got != "t01" {
		t.Errorf("`p` without shuffle must walk back to the previous row (t01), got %q", got)
	}
	if m.shuffleOn {
		t.Error("n/p must not enable shuffle")
	}
}

// TestPlayerBarReportsShuffle: the bar says `shuffle on` only while the mode
// is on (the string already existed; the state behind it is ours now).
func TestPlayerBarReportsShuffle(t *testing.T) {
	m := newModel(t, 180, 50)
	if got := stripANSI(m.View()); !strings.Contains(got, "shuffle off") {
		t.Error("the bar starts at shuffle off")
	}
	m.perm = reversePerm
	press(t, m, "s")
	got := stripANSI(m.View())
	if !strings.Contains(got, "shuffle on") {
		t.Errorf("the bar must say `shuffle on` while enabled, got:\n%s", got)
	}
	if strings.Contains(got, "shuffle off") {
		t.Error("the bar must not say `shuffle off` while shuffle is enabled")
	}
	press(t, m, "s")
	if got := stripANSI(m.View()); !strings.Contains(got, "shuffle off") {
		t.Error("after toggling off the bar must say `shuffle off` again")
	}
}

// TestShuffleDrawerMirrorsOrder: with the drawer open, up-next follows the
// shuffled playback order while the list rows stay in base order; toggling
// off returns the queue rows.
func TestShuffleDrawerMirrorsOrder(t *testing.T) {
	m := newModel(t, 180, 50)
	base := trackIDs(m)
	m.perm = reversePerm
	press(t, m, "s")
	press(t, m, "q")
	// t01 plays at shuffled position 0, so the first up-next row is
	// position 1 = t42 ("Resolution").
	if got := stripANSI(m.View()); !strings.Contains(got, "▸ Resolution") {
		t.Errorf("with shuffle on the drawer must mirror the shuffled order (first up-next = Resolution), got:\n%s", got)
	}
	if got := trackIDs(m); !equalIDs(got, base) {
		t.Errorf("while the drawer mirrors the walk, the list must stay in base order, got %v", got)
	}
	press(t, m, "s")
	if got := stripANSI(m.View()); !strings.Contains(got, "▸ Moanin'") {
		t.Errorf("with shuffle off the drawer must show the queue again (Moanin'), got:\n%s", got)
	}
}

// TestShuffleNeedsTrackView: on the playlists navigation there is nothing to
// shuffle; `s` says so and leaves the mode off.
func TestShuffleNeedsTrackView(t *testing.T) {
	m := newModel(t, 180, 50)
	pressKey(t, m, tea.KeyEsc) // to the playlists navigation
	press(t, m, "s")
	if m.shuffleOn {
		t.Error("there is nothing to shuffle on the playlists navigation")
	}
	if status := stripANSI(lines(m)[49]); !strings.Contains(status, "shuffle") {
		t.Errorf("the toast must say why, got %q", status)
	}
}

// --- real mode (catalog) ---

// realItems is a canned ItemsPage for the stub catalog.
func realItems(ids ...string) library.ItemsPage {
	tracks := make([]library.Track, 0, len(ids))
	for _, id := range ids {
		tracks = append(tracks, library.Track{ID: id, Title: "track " + id, Artist: "artist", Album: "album"})
	}
	return library.ItemsPage{Tracks: tracks, Total: len(ids), NextOffset: -1}
}

// TestShuffleRebuildsOnViewChange (real mode): opening another playlist
// rebuilds the permutation for the new view's rows.
func TestShuffleRebuildsOnViewChange(t *testing.T) {
	const (
		a1 = "aaaaaaaaaaaaaaaaaaaaaa01"
		a2 = "aaaaaaaaaaaaaaaaaaaaaa02"
		a3 = "aaaaaaaaaaaaaaaaaaaaaa03"
		b1 = "bbbbbbbbbbbbbbbbbbbbbb01"
		b2 = "bbbbbbbbbbbbbbbbbbbbbb02"
		b3 = "bbbbbbbbbbbbbbbbbbbbbb03"
		b4 = "bbbbbbbbbbbbbbbbbbbbbb04"
	)
	f := &stubFetch{
		playlistsFn: func(offset int) (library.PlaylistPage, error) {
			return library.PlaylistPage{
				Playlists: []library.PlaylistSummary{
					{ID: "pl1", Name: "Mine", Owner: "nahuel", Private: true, TrackCount: 3},
					{ID: "pl2", Name: "Other", Owner: "nahuel", Private: true, TrackCount: 4},
				},
				Total: 2, NextOffset: -1,
			}, nil
		},
		itemsFn: func(id string, offset int) (library.ItemsPage, error) {
			switch id {
			case "pl1":
				return realItems(a1, a2, a3), nil
			case "pl2":
				return realItems(b1, b2, b3, b4), nil
			}
			return library.ItemsPage{}, errors.New("unexpected playlist")
		},
	}
	m, _ := newRealModel(t, 180, 50, f)
	m.perm = reversePerm
	loadPlaylistsReady(t, m)
	pressRun(t, m, "j")
	pressRun(t, m, "enter") // open pl1
	press(t, m, "s")
	if !m.shuffleOn {
		t.Fatal("after `s` shuffle must be on in the real catalog too")
	}
	// The visible list stays in base order; the playback order is the
	// permutation (the cursor's row keeps position 0, the rest reversed).
	if got, base := trackIDs(m), []string{a1, a2, a3}; !equalIDs(got, base) {
		t.Fatalf("the visible rows must stay in base order, got %v", got)
	}
	got := trackIDsOf(m.orderedTracks())
	want := []string{a1, a3, a2}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pl1 playback position %d: got %q, want %q", i, got[i], want[i])
		}
	}
	// Change the view: esc back, down to pl2, open it.
	pressKey(t, m, tea.KeyEsc)
	press(t, m, "j")
	press(t, m, "j")
	pressRun(t, m, "enter") // open pl2
	if got, base := trackIDs(m), []string{b1, b2, b3, b4}; !equalIDs(got, base) {
		t.Fatalf("the visible rows must stay in base order after the view change, got %v", got)
	}
	got = trackIDsOf(m.orderedTracks())
	want = []string{b1, b4, b3, b2}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pl2 playback position %d: got %q, want %q — the permutation must rebuild for the new view", i, got[i], want[i])
		}
	}
}

// TestShuffleExcludesNonPlayableRows (real mode): the skipped-entries
// footnote row is not a track and never enters the order; `n` walks only the
// resolved playable rows, wrapping inside the list.
func TestShuffleExcludesNonPlayableRows(t *testing.T) {
	const (
		a1 = "aaaaaaaaaaaaaaaaaaaaaa01"
		a2 = "aaaaaaaaaaaaaaaaaaaaaa02"
		a3 = "aaaaaaaaaaaaaaaaaaaaaa03"
	)
	f := &stubFetch{
		playlistsFn: func(offset int) (library.PlaylistPage, error) {
			return library.PlaylistPage{
				Playlists: []library.PlaylistSummary{{ID: "pl1", Name: "Mine", Owner: "nahuel", Private: true, TrackCount: 5}},
				Total:     1, NextOffset: -1,
			}, nil
		},
		itemsFn: func(id string, offset int) (library.ItemsPage, error) {
			return library.ItemsPage{Tracks: realItems(a1, a2, a3).Tracks, Total: 5, NextOffset: -1, Skipped: 2}, nil
		},
	}
	m, pl := newRealModel(t, 180, 50, f)
	m.perm = reversePerm
	loadPlaylistsReady(t, m)
	pressRun(t, m, "j")
	pressRun(t, m, "enter")
	press(t, m, "s")
	if len(m.shuffleOrd) != 3 {
		t.Errorf("the permutation must cover exactly the 3 playable rows, got %d", len(m.shuffleOrd))
	}
	if got := stripANSI(m.View()); !strings.Contains(got, "2 entries not playable") {
		t.Errorf("the skipped-entries footnote must still render, got:\n%s", got)
	}
	// `n` walks only the playable rows of the playback order: nothing plays,
	// so the walk starts from the cursor (position 0 = a1) and takes the next
	// row each time. The order is not circular: after the last playable row
	// `n` hands over to radio — no resolver here, so it toasts and stops.
	want := []string{a3, a2}
	for i, id := range want {
		press(t, m, "n")
		if len(pl.played) != i+1 || pl.played[i].ID != id {
			t.Fatalf("`n` walk step %d: played %+v, want %q", i, pl.played, id)
		}
	}
	press(t, m, "n")
	if len(pl.played) != len(want) {
		t.Errorf("the exhausted order must not wrap: played %+v", pl.played)
	}
	if !strings.Contains(m.toast.text, "nothing left to play") {
		t.Errorf("the exhausted order must toast, got %q", m.toast.text)
	}
}
