package app

// Shuffle tests (T21): `s` toggles shuffle over the open view's playable
// rows. The permutation generator is injected (reversePerm) so every order
// here is deterministic. Semantics under test: while shuffle is on, OUR
// order owns playback — the list, the drawer and n/p follow the shuffled
// list through the existing play path; with shuffle off, today's behaviour
// stands exactly.

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

// TestShuffleTogglePermutesDeterministically: `s` with an injected generator
// builds a deterministic permutation of the view's playable rows and keeps
// the playing row (t01, position 0) at its position.
func TestShuffleTogglePermutesDeterministically(t *testing.T) {
	m := newModel(t, 180, 50)
	base := trackIDs(m)
	if len(base) != 42 {
		t.Fatalf("mock setup: 42 tracks, got %d", len(base))
	}
	var seeds []int64
	m.perm = func(n int, seed int64) []int {
		seeds = append(seeds, seed)
		return reversePerm(n, seed)
	}
	press(t, m, "s")
	if !m.shuffleOn {
		t.Fatal("after `s` shuffle must be on")
	}
	if len(seeds) == 0 || seeds[0] == 0 {
		t.Fatalf("the generator must be called with a seed, got %v", seeds)
	}
	got := trackIDs(m)
	if len(got) != len(base) {
		t.Fatalf("shuffle permutes, never drops rows: got %d rows, want %d", len(got), len(base))
	}
	if got[0] != "t01" {
		t.Errorf("the playing row must keep its position: got[0] = %q, want t01", got[0])
	}
	// reversePerm over the rest (base indices 1..41 reversed): visible
	// position i>0 must hold base row len(base)-i, deterministically.
	for i := 1; i < len(got); i++ {
		if want := base[len(base)-i]; got[i] != want {
			t.Errorf("position %d: got %q, want %q (deterministic reversal)", i, got[i], want)
		}
	}
}

// TestShuffleOffRestoresOriginalOrder: a second `s` clears the mode and the
// permutation, and restores the exact original order.
func TestShuffleOffRestoresOriginalOrder(t *testing.T) {
	m := newModel(t, 180, 50)
	base := trackIDs(m)
	m.perm = reversePerm
	press(t, m, "s")
	press(t, m, "s")
	if m.shuffleOn {
		t.Fatal("a second `s` must turn shuffle off")
	}
	if m.shuffleOrd != nil {
		t.Error("the permutation must be cleared when shuffle turns off")
	}
	got := trackIDs(m)
	for i := range base {
		if got[i] != base[i] {
			t.Fatalf("position %d: got %q, want %q — the original order must be restored exactly", i, got[i], base[i])
		}
	}
}

// TestShuffleCursorFollowsTrack: the cursor stays on the same track through
// both directions of the toggle — it follows the track, not its index.
func TestShuffleCursorFollowsTrack(t *testing.T) {
	m := newModel(t, 180, 50)
	press(t, m, "j")
	press(t, m, "j") // cursor on t03
	if before, ok := m.trackUnderCursor(); !ok || before.ID != "t03" {
		t.Fatalf("mock setup: cursor on t03, got %q (ok=%v)", before.ID, ok)
	}
	m.perm = reversePerm
	press(t, m, "s")
	if after, ok := m.trackUnderCursor(); !ok || after.ID != "t03" {
		t.Errorf("after shuffling the cursor must stay on t03, got %q (ok=%v)", after.ID, ok)
	}
	press(t, m, "s")
	if after, ok := m.trackUnderCursor(); !ok || after.ID != "t03" {
		t.Errorf("after unshuffling the cursor must stay on t03, got %q (ok=%v)", after.ID, ok)
	}
}

// TestShuffleNextWalksShuffledList: with shuffle on, `n` plays the next row
// of the shuffled list — not the session's own next — and `p` mirrors it.
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
	// Wrapping: from the last shuffled row (t02 at position 41), `n` wraps to
	// the playing anchor t01 at position 0.
	if err := m.player.Play(library.Track{ID: "t02"}); err != nil {
		t.Fatalf("mock setup: play t02: %v", err)
	}
	press(t, m, "n")
	if got := m.data.Session().TrackID; got != "t01" {
		t.Errorf("`n` at the end of the shuffled list must wrap to its first row (t01), got %q", got)
	}
}

// TestNextPrevUnchangedWithoutShuffle: with shuffle off, `n`/`p` keep the
// session's own behaviour exactly (previous behaviour preserved).
func TestNextPrevUnchangedWithoutShuffle(t *testing.T) {
	m := newModel(t, 180, 50)
	press(t, m, "n")
	if got := m.data.Session().TrackID; got != "t02" {
		t.Errorf("`n` without shuffle must use the session next (t02), got %q", got)
	}
	press(t, m, "p")
	if got := m.data.Session().TrackID; got != "t01" {
		t.Errorf("`p` without shuffle must use the session prev (t01), got %q", got)
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
// shuffled order; toggling off returns the queue rows.
func TestShuffleDrawerMirrorsOrder(t *testing.T) {
	m := newModel(t, 180, 50)
	m.perm = reversePerm
	press(t, m, "s")
	press(t, m, "q")
	// t01 plays at shuffled position 0, so the first up-next row is
	// position 1 = t42 ("Resolution").
	if got := stripANSI(m.View()); !strings.Contains(got, "▸ Resolution") {
		t.Errorf("with shuffle on the drawer must mirror the shuffled order (first up-next = Resolution), got:\n%s", got)
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
	// The anchor (the cursor's row, nothing plays) keeps position 0; the rest
	// is reversed by the injected generator.
	got := trackIDs(m)
	want := []string{a1, a3, a2}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pl1 shuffled position %d: got %q, want %q", i, got[i], want[i])
		}
	}
	// Change the view: esc back, down to pl2, open it.
	pressKey(t, m, tea.KeyEsc)
	press(t, m, "j")
	press(t, m, "j")
	pressRun(t, m, "enter") // open pl2
	got = trackIDs(m)
	want = []string{b1, b4, b3, b2}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("pl2 shuffled position %d: got %q, want %q — the permutation must rebuild for the new view", i, got[i], want[i])
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
	// `n` walks only the playable rows of the shuffled order: nothing plays,
	// so the walk starts from the cursor (position 0 = a1) and takes the next
	// row each time, wrapping at the end of the list.
	want := []string{a3, a2, a1, a1} // order [a1, a3, a2], cursor riding along
	for i, id := range want {
		press(t, m, "n")
		if len(pl.played) != i+1 || pl.played[i].ID != id {
			t.Fatalf("`n` walk step %d: played %+v, want %q", i, pl.played, id)
		}
		press(t, m, "j") // the cursor rides along the shuffled order
	}
}
