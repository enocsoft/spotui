package app

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nahuel/spotify-tui/internal/library"
	"github.com/nahuel/spotify-tui/internal/ui/theme"
)

type rowKind int

const (
	rowHeader rowKind = iota
	rowPlaylistItem
	rowTrack
	rowStatus // loading / empty / error / unavailable / rate-limit panel state
)

// row is one visible list row. The list is one full-screen component (S4);
// its rows are either section headers, a collection item, a track or a panel
// status row (loading, empty, error, unavailable).
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

// tracksForView is the track list of the focused view (playlist, search or
// queue), narrowed by the active filter.
func (m Model) tracksForView() []library.Track {
	var tracks []library.Track
	switch m.section {
	case SectionSearch:
		tracks = m.data.Playlist().Tracks // mock: search covers the library list
	case SectionQueue:
		tracks = m.data.Queue()
	default:
		if !m.drillDown {
			return nil
		}
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
// mode it is the fixture library.
func (m Model) visibleRows() []row {
	if m.fetch != nil {
		return m.realRows()
	}
	if m.section == SectionLibrary && !m.drillDown {
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
	tracks := m.tracksForView()
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

func (m *Model) onEnter() tea.Cmd {
	rows := m.visibleRows()
	if m.section == SectionLibrary && !m.drillDown {
		if m.cursor < len(rows) {
			r := rows[m.cursor]
			if r.kind == rowPlaylistItem {
				if m.fetch != nil {
					return m.openPlaylist(r.pl)
				}
				m.drillDown = true
				m.cursor = 0
				m.listOffset = 0
				return nil
			}
			if r.kind == rowHeader {
				return m.onToggle()
			}
		}
		return m.setToast("enter · nothing to open here")
	}
	if track, ok := m.trackUnderCursor(); ok {
		if err := m.player.Play(track); err != nil {
			return m.setToast("✗ " + err.Error())
		}
		return m.setToast("▶ " + track.Title)
	}
	return m.setToast("enter · nothing to play on this row")
}

func (m *Model) onEnqueue() tea.Cmd {
	if track, ok := m.trackUnderCursor(); ok {
		if err := m.player.Enqueue(track); err != nil {
			return m.setToast("✗ " + err.Error())
		}
		return m.setToast("queued · " + track.Title)
	}
	return m.setToast("a · open a track list first")
}

func (m *Model) onPause() tea.Cmd {
	if err := m.player.TogglePause(); err != nil {
		return m.setToast("✗ " + err.Error())
	}
	if m.data.Session().State == library.StatePaused {
		return m.setToast("paused")
	}
	return m.setToast("playing")
}

func (m *Model) onNext() tea.Cmd {
	if err := m.player.Next(); err != nil {
		return m.setToast("✗ " + err.Error())
	}
	if t, ok := m.currentTrack(); ok {
		return m.setToast("▶ " + t.Title)
	}
	return nil
}

func (m *Model) onPrev() tea.Cmd {
	if err := m.player.Prev(); err != nil {
		return m.setToast("✗ " + err.Error())
	}
	if t, ok := m.currentTrack(); ok {
		return m.setToast("▶ " + t.Title)
	}
	return nil
}

// onVolume steps the player volume up (+1) or down (-1).
func (m *Model) onVolume(dir int) tea.Cmd {
	var err error
	if dir > 0 {
		err = m.player.VolumeUp()
	} else {
		err = m.player.VolumeDown()
	}
	if err != nil {
		return m.setToast("✗ " + err.Error())
	}
	return m.setToast(fmt.Sprintf("volume %d%%", m.data.Session().Volume))
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
		name := strings.TrimPrefix(r.text, theme.GlyphExpanded+" ")
		name = strings.TrimPrefix(name, theme.GlyphCollapsed+" ")
		if name == "PLAYLISTS" {
			m.expanded["PLAYLISTS"] = !m.expanded["PLAYLISTS"]
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

func (m *Model) onEsc() tea.Cmd {
	if m.drillDown && m.section == SectionLibrary {
		m.drillDown = false
		m.cursor = 0
		m.listOffset = 0
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
		return m.setToast(": type a command · :play <spotify track uri or id>")
	}
	name, rest := raw, ""
	if i := strings.IndexAny(raw, " \t"); i >= 0 {
		name, rest = raw[:i], strings.TrimSpace(raw[i+1:])
	}
	switch name {
	case "play", ":play":
		id, err := library.ParseTrackID(rest)
		if err != nil {
			return m.setToast("✗ :play wants a track id, spotify:track:<id> or a pasted open.spotify.com link")
		}
		if err := m.player.Play(library.Track{ID: id}); err != nil {
			return m.setToast("✗ " + err.Error())
		}
		return m.setToast("▶ " + id)
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
