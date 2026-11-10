package app

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nahuel/spotui/internal/library"
	"github.com/nahuel/spotui/internal/ui/theme"
)

type rowKind int

const (
	rowHeader rowKind = iota
	rowPlaylistItem
	rowAlbumItem // a search-result album: opens its tracks on enter (T22)
	rowTrack
	rowStatus // loading / empty / error / rate-limit panel state
)

// row is one visible list row. The list is one full-screen component (S4);
// its rows are either section headers, a collection item, a track or a panel
// status row (loading, empty, error).
type row struct {
	kind    rowKind
	text    string
	count   int
	num     int // 1-based position in its list (track rows)
	track   library.Track
	playing bool
	// real catalog rows (T7): playlist rows carry their summary so `enter`
	// can drill into them; status rows carry their state kind and hint.
	meta string
	hint string
	st   statusKind
	pl   library.PlaylistSummary
	al   library.AlbumSummary // real catalog rows (T22): album rows carry their summary so `enter` can drill into them
	// match is the query whose case-insensitive match inside the row's label
	// renders bold (T24, search results only; empty means no highlighting).
	match string
}

// currentTrack resolves the now-playing track. The real player reports the
// track text itself (Session.Title); the mock falls back to a TrackID lookup
// in the playlist.
func (m Model) currentTrack() (library.Track, bool) {
	s := m.data.Session()
	if s.Title != "" {
		return library.Track{
			ID: s.TrackID, Title: s.Title, Artist: s.Artist, Album: s.Album, Duration: s.Total,
		}, true
	}
	for _, t := range m.data.Playlist().Tracks {
		if t.ID == s.TrackID {
			return t, true
		}
	}
	return library.Track{}, false
}

// tracksForView is the track list of the focused view (the open playlist in
// mock mode), narrowed by the active filter.
func (m Model) tracksForView() []library.Track {
	var tracks []library.Track
	if m.section == SectionTracks {
		tracks = m.data.Playlist().Tracks
	}
	if m.filter != "" {
		q := strings.ToLower(m.filter)
		narrowed := make([]library.Track, 0, len(tracks))
		for _, t := range tracks {
			hay := strings.ToLower(t.Title + " " + t.Artist + " " + t.Album)
			if strings.Contains(hay, q) {
				narrowed = append(narrowed, t)
			}
		}
		tracks = narrowed
	}
	return tracks
}

func (m Model) playlistPlaying(id string) bool { return m.data.Session().TrackID == id }

// visibleRows is the row list under the cursor. In real mode it is the
// catalog's view (playlists pages, focused contents, search results); in mock
// mode it is the fixture library. The list renders in base order — shuffle
// permutes only the playback order (the C revision), never these rows.
func (m Model) visibleRows() []row {
	if m.fetch != nil {
		return m.realRows()
	}
	if m.section == SectionPlaylists {
		var rows []row
		for _, s := range m.data.Library() {
			expanded := s.Name == "PLAYLISTS" && m.expanded[s.Name]
			glyph := theme.GlyphCollapsed
			if expanded {
				glyph = theme.GlyphExpanded
			}
			rows = append(rows, row{kind: rowHeader, text: glyph + " " + s.Name, count: s.Count})
			if expanded && s.Name == "PLAYLISTS" {
				pl := m.data.Playlist()
				rows = append(rows, row{kind: rowPlaylistItem, text: pl.Name, count: len(pl.Tracks)})
			}
		}
		return rows
	}
	tracks := m.viewTracks()
	rows := make([]row, len(tracks))
	for i, t := range tracks {
		rows[i] = row{kind: rowTrack, track: t, num: i + 1, playing: m.playlistPlaying(t.ID)}
	}
	return rows
}

// trackUnderCursor returns the track on the row under the cursor, if any.
func (m Model) trackUnderCursor() (library.Track, bool) {
	rows := m.visibleRows()
	if m.cursor < 0 || m.cursor >= len(rows) {
		return library.Track{}, false
	}
	r := rows[m.cursor]
	return r.track, r.kind == rowTrack
}

// playRow plays the track on a row; used by list and search results. Every
// successful play rides the recently played uris radio seeds with (T26).
// Radio plays through this same path, outside the open list. The call runs
// through the serialized queue: Update returns the command at once and the
// toast (or the error naming itself) arrives as a playerMsg.
func (m *Model) playRow(t library.Track) tea.Cmd {
	return m.submitPlayer(playerTask{
		op:    opPlay,
		track: t,
		run:   func() error { return m.player.Play(t) },
	})
}

func (m *Model) onEnter() tea.Cmd {
	rows := m.visibleRows()
	if m.cursor >= len(rows) {
		return m.setToast("enter · nothing to open here")
	}
	r := rows[m.cursor]
	// The results view acts on its rows without ever switching the section
	// row: a track plays, a playlist opens its tracks, an album opens its
	// tracks too (T22) — every row below the cursor acts.
	if m.sView {
		switch r.kind {
		case rowTrack:
			return m.playRow(r.track)
		case rowPlaylistItem:
			return m.openPlaylist(r.pl)
		case rowAlbumItem:
			return m.openAlbum(r.al)
		default:
			return m.setToast("enter · nothing to open here")
		}
	}
	if m.section == SectionPlaylists {
		if r.kind == rowPlaylistItem {
			if m.fetch != nil {
				return m.openPlaylist(r.pl)
			}
			m.section = SectionTracks
			m.cursor = 0
			m.listOffset = 0
			m.rebuildShuffle()
			return nil
		}
		if r.kind == rowAlbumItem && m.fetch != nil {
			// T23: a saved album opens like a search album — the resolver
			// serves spotify:album:<id> with no owner restriction.
			return m.openAlbum(r.al)
		}
		if r.kind == rowHeader {
			name := strings.TrimPrefix(r.text, theme.GlyphExpanded+" ")
			name = strings.TrimPrefix(name, theme.GlyphCollapsed+" ")
			if name == "SAVED" && m.fetch != nil {
				// T29: enter on the SAVED group opens the saved songs in the
				// Tracks view — the list IS the saved set.
				return m.openSavedTracks()
			}
			return m.onToggle()
		}
		if r.kind == rowTrack {
			// T29: a saved-track row of the expanded SAVED group plays in place.
			return m.playRow(r.track)
		}
		return m.setToast("enter · nothing to open here")
	}
	if r.kind == rowTrack {
		return m.playRow(r.track)
	}
	return m.setToast("enter · nothing to play on this row")
}

func (m *Model) onEnqueue() tea.Cmd {
	if track, ok := m.trackUnderCursor(); ok {
		return m.submitPlayer(playerTask{
			op:    opEnqueue,
			track: track,
			run:   func() error { return m.player.Enqueue(track) },
		})
	}
	return m.setToast("a · open a track list first")
}

func (m *Model) onPause() tea.Cmd {
	// The paused/playing toast reads the session after the toggle lands —
	// now at playerMsg time, the same point in time the inline call read it.
	return m.submitPlayer(playerTask{op: opPause, run: m.player.TogglePause})
}

func (m *Model) onNext() tea.Cmd {
	// The D revision: `n` walks the playback order (shuffled while shuffle is
	// on, base order otherwise) through the play path, and hands over to radio
	// when the order runs out — playback never waits for the session's queue.
	return m.playbackStep(1)
}

func (m *Model) onPrev() tea.Cmd {
	// `p` walks the playback order backwards and stops at the start with an
	// honest toast.
	return m.playbackStep(-1)
}

// onVolume steps the player volume up (+1) or down (-1).
func (m *Model) onVolume(dir int) tea.Cmd {
	op, run := opVolumeDown, m.player.VolumeDown
	if dir > 0 {
		op, run = opVolumeUp, m.player.VolumeUp
	}
	return m.submitPlayer(playerTask{op: op, run: run})
}

// onToggle handles `l`: expand/collapse on library headers, saved toggle on
// track rows (spec S6, in place, no dialog).
func (m *Model) onToggle() tea.Cmd {
	rows := m.visibleRows()
	if m.cursor >= len(rows) {
		return m.setToast("l · nothing to toggle")
	}
	r := rows[m.cursor]
	if r.kind == rowHeader {
		if m.sView {
			return m.setToast("l · nothing to save here")
		}
		name := strings.TrimPrefix(r.text, theme.GlyphExpanded+" ")
		name = strings.TrimPrefix(name, theme.GlyphCollapsed+" ")
		if name == "PLAYLISTS" {
			m.expanded["PLAYLISTS"] = !m.expanded["PLAYLISTS"]
			m.clampCursor()
			return nil
		}
		if name == "ALBUMS" && m.fetch != nil {
			// T23: expanding the saved-albums group fetches its first page
			// lazily; collapsing is local.
			m.expanded["ALBUMS"] = !m.expanded["ALBUMS"]
			m.clampCursor()
			if m.expanded["ALBUMS"] {
				return m.ensureAlbumsLoaded()
			}
			return nil
		}
		if name == "SAVED" && m.fetch != nil {
			// T29: expanding the saved-songs group fetches its first page
			// lazily; collapsing is local — the albums behaviour one group later.
			m.expanded["SAVED"] = !m.expanded["SAVED"]
			m.clampCursor()
			if m.expanded["SAVED"] {
				return m.ensureSavedTracksLoaded()
			}
			return nil
		}
		return m.setToast("l · " + name + " is empty in slice 1")
	}
	if r.kind == rowTrack {
		if m.fetch != nil {
			// Real mode: the cell answers only after the server does. While the
			// toggle is in flight further presses are ignored (Mirror Rule).
			if _, busy := m.pendingSaved[r.track.ID]; busy {
				return nil
			}
			saved := !r.track.Saved
			m.pendingSaved[r.track.ID] = saved
			return m.savedCmd(r.track.ID, saved)
		}
		saved := !r.track.Saved
		if err := m.data.SetSaved(r.track.ID, saved); err != nil {
			return m.setToast("✗ " + err.Error())
		}
		if saved {
			return m.setToast("saved · " + r.track.Title)
		}
		return m.setToast("unsaved · " + r.track.Title)
	}
	return m.setToast("l · nothing to save here")
}

// onEsc is esc in the list: the results view backs out to the previous
// section view first, then the Tracks view returns to the navigation.
func (m *Model) onEsc() tea.Cmd {
	if m.sView {
		m.sView = false
		m.rebuildShuffle() // back to the previous section view: rebuild
		m.clampCursor()
		m.syncScroll()
		return nil
	}
	if m.section == SectionTracks {
		m.section = SectionPlaylists
		m.cursor, m.listOffset = 0, 0
		m.rebuildShuffle()
		if m.fetch != nil {
			return m.ensurePlaylistsLoaded()
		}
		return nil
	}
	return m.setToast("already at top")
}

// onPickSinkCmd wraps onPickSink for the update loop.
func (m Model) onPickSinkCmd() (tea.Model, tea.Cmd) {
	cmd := (&m).onPickSink()
	return m, cmd
}

// onPickSink selects the audio output under the picker window cursor
// (local-player pivot: `d` picks a local sink, never a remote device).
func (m *Model) onPickSink() tea.Cmd {
	sinks := m.data.Sinks()
	if m.winCursor < 0 || m.winCursor >= len(sinks) {
		m.closeWindow(false)
		return m.setToast("no audio output selected")
	}
	sink := sinks[m.winCursor]
	if err := m.data.SetSink(sink.Name); err != nil {
		return m.setToast("✗ " + err.Error())
	}
	m.closeWindow(false)
	return m.setToast("output · " + sink.Name)
}

// execCommand runs the palette input (T13). `:play` validates its target
// before touching the player so invalid input never reaches librespot;
// unknown commands keep the one-line toast behaviour.
func (m *Model) execCommand() tea.Cmd {
	raw := strings.TrimSpace(m.cmdInput)
	if raw == "" {
		return m.setToast(": type a command · :play <track, playlist or album link or uri>")
	}
	name, rest := raw, ""
	if i := strings.IndexAny(raw, " \t"); i >= 0 {
		name, rest = raw[:i], strings.TrimSpace(raw[i+1:])
	}
	switch name {
	case "play", ":play":
		tgt, err := library.ParseTarget(rest)
		if err != nil {
			return m.setToast("✗ :play wants a track, playlist or album — spotify:track:<id>, spotify:playlist:<id>, spotify:album:<id> or a pasted open.spotify.com link")
		}
		if tgt.Kind == library.TargetTrack {
			// unchanged track path (T25): the id plays exactly as before
			t := library.Track{ID: tgt.ID}
			return m.submitPlayer(playerTask{
				op:    opPlay,
				track: t,
				run:   func() error { return m.player.Play(t) },
			})
		}
		if m.fetch == nil {
			return m.setToast("✗ :play playlist and album targets need the real catalog — mock mode has no resolver")
		}
		// A context target: resolve it, load its tracks into the list view and
		// play the first playable track (T25).
		return m.playContext(tgt.Kind, tgt.ID)
	}
	return m.setToast(": unknown command " + name)
}

func (m *Model) shiftSections(delta int) tea.Cmd {
	next := m.sectionShift + delta
	if next < 0 || next >= len(sections) {
		return m.setToast("sections fit · nothing to shift")
	}
	m.sectionShift = next
	return nil
}
