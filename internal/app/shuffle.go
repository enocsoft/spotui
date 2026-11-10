package app

// Shuffle (T21): `s` toggles shuffle over the open view's playable rows.
//
// Semantics (the parent's call): while shuffle is on, OUR order owns
// playback. The list renders in the shuffled order, the drawer mirrors it,
// and `n`/`p` play the next/previous playable row of the shuffled list
// through the existing play path — asking the session to advance its own
// context would disagree with the visible order. With shuffle off, the
// session behaviour stands exactly.
//
// The permutation keeps the anchor row (the one playing, else the cursor
// row) at its position so the user does not lose their place, is rebuilt
// when the view changes, and is cleared when shuffle turns off. The
// generator is injectable with a seed so tests are deterministic.

import (
	"hash/fnv"
	"math/rand"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nahuel/spotify-tui/internal/library"
)

// seededPerm is the default permutation generator: rand.Perm over n items,
// seeded from the view identity, so the same view shuffles the same way
// until the view changes.
func seededPerm(n int, seed int64) []int {
	return rand.New(rand.NewSource(seed)).Perm(n)
}

// onShuffle is `s`: toggle shuffle for the open view (T21). The mode lives
// on the model — the player bar reads it from here, never from the session
// snapshot, because our order is the one that owns playback.
func (m *Model) onShuffle() tea.Cmd {
	if len(m.viewTracks()) == 0 {
		return m.setToast("s · nothing to shuffle here")
	}
	cursorID := ""
	if t, ok := m.trackUnderCursor(); ok {
		cursorID = t.ID
	}
	m.shuffleOn = !m.shuffleOn
	if m.shuffleOn {
		m.buildShuffleOrder()
	} else {
		m.shuffleOrd = nil
	}
	if cursorID != "" {
		m.followTrack(cursorID)
	}
	m.syncScroll()
	if m.shuffleOn {
		return m.setToast("shuffle on")
	}
	return m.setToast("shuffle off")
}

// rebuildShuffle recomputes the permutation for the current view; a no-op
// while shuffle is off. Called at every view change (another playlist,
// another query, a filter applied, new tracks landed).
func (m *Model) rebuildShuffle() {
	if m.shuffleOn {
		m.buildShuffleOrder()
	}
}

// buildShuffleOrder permutes the view's playable rows: the anchor keeps its
// position, the rest are ordered by the injected generator seeded from the
// view identity. shuffleOrd maps visible position -> base index.
func (m *Model) buildShuffleOrder() {
	m.shuffleOrd = nil
	tracks := m.viewTracks()
	n := len(tracks)
	if !m.shuffleOn || n == 0 {
		return
	}
	anchor := m.shuffleAnchor(tracks)
	rest := make([]int, 0, n-1)
	for i := 0; i < n; i++ {
		if i != anchor {
			rest = append(rest, i)
		}
	}
	gen := m.perm
	if gen == nil {
		gen = seededPerm
	}
	order := make([]int, 0, n)
	for _, j := range gen(len(rest), m.shuffleSeed()) {
		order = append(order, rest[j])
	}
	order = append(order, 0)
	copy(order[anchor+1:], order[anchor:])
	order[anchor] = anchor
	m.shuffleOrd = order
}

// shuffleAnchor is the base index the permutation keeps in place: the
// playing track's position, else the cursor track's position, else 0.
func (m Model) shuffleAnchor(tracks []library.Track) int {
	if id := m.data.Session().TrackID; id != "" {
		for i, t := range tracks {
			if t.ID == id {
				return i
			}
		}
	}
	if t, ok := m.trackUnderCursor(); ok && t.ID != "" {
		for i, tr := range tracks {
			if tr.ID == t.ID {
				return i
			}
		}
	}
	return 0
}

// viewTracks is the open view's playable track list in base order: the
// search results, the focused playlist's resolved tracks, or (mock mode)
// the open list narrowed by the active filter. The playlists navigation has
// none — there is nothing to shuffle there.
func (m Model) viewTracks() []library.Track {
	if m.fetch != nil {
		switch {
		case m.sView:
			return m.sTracks
		case m.section == SectionTracks:
			return m.plTracks
		default:
			return nil
		}
	}
	if m.section == SectionTracks {
		return m.tracksForView()
	}
	return nil
}

// orderedTracks is the view's track list in render order: the shuffled
// order while shuffle is on, the base order otherwise (and as a fallback if
// the view changed without a rebuild — it never renders a stale index).
func (m Model) orderedTracks() []library.Track {
	tracks := m.viewTracks()
	if len(m.shuffleOrd) != len(tracks) {
		return tracks
	}
	out := make([]library.Track, len(tracks))
	for pos, idx := range m.shuffleOrd {
		out[pos] = tracks[idx]
	}
	return out
}

// shuffleSeed derives a stable seed from the open view's identity.
func (m Model) shuffleSeed() int64 {
	h := fnv.New64a()
	h.Write([]byte(m.viewKey()))
	return int64(h.Sum64())
}

func (m Model) viewKey() string {
	if m.fetch != nil {
		if m.sView {
			return "search\x00" + m.sQuery
		}
		return "playlist\x00" + m.plID
	}
	return "mock\x00" + m.data.Playlist().Name + "\x00" + m.filter
}

// followTrack puts the cursor on the visible row of the track with this id:
// the cursor follows its track through the permutation, not its index.
func (m *Model) followTrack(id string) {
	for i, r := range m.visibleRows() {
		if r.kind == rowTrack && r.track.ID == id {
			m.cursor = i
			m.syncScroll()
			return
		}
	}
}

// shuffleStep walks the shuffled list (dir 1 next, -1 previous) through the
// existing play path. It reports ok=false when shuffle is off, leaving the
// session's own next/previous untouched. The walk target is recorded before
// the play is submitted: while the first press is still in flight, the
// second one walks from where the first will land — two rapid presses stay
// two ordered skips, not one repeated track.
func (m *Model) shuffleStep(dir int) (tea.Cmd, bool) {
	if !m.shuffleOn {
		return nil, false
	}
	order := m.orderedTracks()
	if len(order) == 0 {
		return m.setToast("nothing to shuffle here"), true
	}
	cur := m.shufflePosition(order)
	next := order[((cur+dir)%len(order)+len(order))%len(order)]
	m.walks = append(m.walks, next.ID)
	return m.playRow(next), true
}

// walkDone drops the first pending walk with this id: the play landed (or
// failed) and the session is the truth again. Called from applyPlayerMsg.
func (m *Model) walkDone(id string) {
	for i, w := range m.walks {
		if w == id {
			m.walks = append(m.walks[:i], m.walks[i+1:]...)
			return
		}
	}
}

// shufflePosition resolves where a walk through the shuffled list starts:
// a walk already in flight first (the next press must continue where the
// first one left off — the session has not learned about that play yet),
// else the playing track, else the track under the cursor, else -1 (the
// walk starts at the top of the shuffled list).
func (m Model) shufflePosition(order []library.Track) int {
	if n := len(m.walks); n > 0 {
		if id := m.walks[n-1]; id != "" {
			for i, t := range order {
				if t.ID == id {
					return i
				}
			}
		}
	}
	if id := m.data.Session().TrackID; id != "" {
		for i, t := range order {
			if t.ID == id {
				return i
			}
		}
	}
	if t, ok := m.trackUnderCursor(); ok && t.ID != "" {
		for i, tr := range order {
			if tr.ID == t.ID {
				return i
			}
		}
	}
	return -1
}

// shuffleUpNext is what the queue drawer shows while shuffle is on: the rows
// after the current position of the shuffled list — the same walk `n` takes.
func (m Model) shuffleUpNext() []library.Track {
	order := m.orderedTracks()
	n := len(order)
	if n == 0 {
		return nil
	}
	cur := m.shufflePosition(order)
	count := drawerRow
	if cur >= 0 && count > n-1 {
		count = n - 1 // the player bar owns the current track
	}
	if count > n {
		count = n
	}
	out := make([]library.Track, 0, count)
	for i := 1; i <= count; i++ {
		out = append(out, order[((cur+i)%n+n)%n])
	}
	return out
}
