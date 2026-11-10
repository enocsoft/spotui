// The real catalog in the TUI (T7): async state for the playlists panel, the
// focused playlist contents and the search window, plus the tea.Cmd wrappers
// that run every library.Fetch call off the update loop (Fetch blocks; Update
// must not). Mock mode (SPOTIFY_TUI_MOCK=1) has fetch == nil and never enters
// any of this.
package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nahuel/spotify-tui/internal/library"
	"github.com/nahuel/spotify-tui/internal/ui/theme"
)

// statusKind is the panel state a rowStatus row carries (DESIGN §7: loading,
// empty, error render in place, never as a dead empty list).
type statusKind uint8

const (
	stNone statusKind = iota
	stLoading
	stEmpty
	stError
	stInfo // rate limited: the info role (DESIGN global states)
)

// --- messages ---

// playlistsMsg carries one page of GET /v1/me/playlists (or its error).
type playlistsMsg struct {
	offset int
	page   library.PlaylistPage
	err    error
}

// itemsMsg carries one slice of the playlist's resolved contents (or its
// error). The contents ride the playback resolver (library/context.go).
type itemsMsg struct {
	plID   string
	offset int
	page   library.ItemsPage
	err    error
}

// searchMsg carries one page of GET /v1/search (or its error).
type searchMsg struct {
	query  string
	offset int
	page   library.SearchPage
	err    error
}

// savedMsg carries the server's answer to a saved toggle. The cell flips only
// here: never optimistically (Mirror Rule).
type savedMsg struct {
	trackID string
	saved   bool
	err     error
}

// --- commands ---

func (m Model) playlistsCmd(offset int) tea.Cmd {
	f := m.fetch
	return func() tea.Msg {
		page, err := f.Playlists(context.Background(), offset)
		return playlistsMsg{offset: offset, page: page, err: err}
	}
}

func (m Model) itemsCmd(id string, offset int) tea.Cmd {
	f := m.fetch
	isAlbum := m.plIsAlbum // the open list decides which resolver path serves it
	return func() tea.Msg {
		var (
			page library.ItemsPage
			err  error
		)
		if isAlbum {
			page, err = f.AlbumItems(context.Background(), id, offset)
		} else {
			page, err = f.Items(context.Background(), id, offset)
		}
		return itemsMsg{plID: id, offset: offset, page: page, err: err}
	}
}

func (m Model) searchCmd(offset int) tea.Cmd {
	f, q := m.fetch, m.sQuery
	return func() tea.Msg {
		page, err := f.Search(context.Background(), q, offset)
		return searchMsg{query: q, offset: offset, page: page, err: err}
	}
}

func (m Model) savedCmd(id string, saved bool) tea.Cmd {
	f := m.fetch
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := f.SetSaved(ctx, id, saved)
		return savedMsg{trackID: id, saved: saved, err: err}
	}
}

// --- applying results ---

func (m *Model) applyPlaylists(msg playlistsMsg) {
	if msg.offset != m.plsPending {
		return // a stale page (another retry won the race): drop it
	}
	m.plsPending, m.plsLoading = -1, false
	if msg.err != nil {
		m.plsErr = msg.err
		return
	}
	m.plsErr = nil
	m.plsLoaded = true
	if msg.offset == 0 {
		m.pls = msg.page.Playlists
	} else {
		m.pls = append(m.pls, msg.page.Playlists...)
	}
	m.plsTotal = msg.page.Total
	m.plsNext = msg.page.NextOffset
	m.clampCursor()
}

func (m *Model) applyItems(msg itemsMsg) {
	if msg.plID != m.plID || msg.offset != m.iPending {
		return // stale: the user moved to another playlist meanwhile
	}
	m.iPending, m.plItemsLoading = -1, false
	if msg.err != nil {
		m.plItemsErr = msg.err
		return
	}
	m.plItemsErr = nil
	m.plTracks = append(m.plTracks, msg.page.Tracks...)
	m.plTotal = msg.page.Total
	m.plNext = msg.page.NextOffset
	// What the bounded resolver could not show — footnoted, never silent.
	m.plSkipped, m.plTruncated = msg.page.Skipped, msg.page.Truncated
	m.rebuildShuffle() // new playable rows landed: re-permute over them
	m.clampCursor()
}

func (m *Model) applySearch(msg searchMsg) {
	if msg.query != m.sQuery || msg.offset != m.sPending {
		return // stale: a newer query is already running
	}
	m.sPending, m.sLoading = -1, false
	if msg.err != nil {
		// A 429 keeps the rate-limit marker for the info role (DESIGN global
		// states); any other error takes the error role. No auto-retry: `r` is
		// the retry, in the results view, once.
		m.sErr, m.sRateLimited = msg.err, library.IsRateLimited(msg.err)
		return
	}
	m.sErr, m.sRateLimited = nil, false
	if msg.offset == 0 {
		m.sTracks, m.sAlbums, m.sPlaylists = msg.page.Tracks, msg.page.Albums, msg.page.Playlists
	} else {
		m.sTracks = append(m.sTracks, msg.page.Tracks...)
		m.sAlbums = append(m.sAlbums, msg.page.Albums...)
		m.sPlaylists = append(m.sPlaylists, msg.page.Playlists...)
	}
	m.sTotals = msg.page.Totals
	m.sNext = msg.page.NextOffset
	m.rebuildShuffle() // new result rows landed: re-permute over them
	m.clampCursor()
}

// applySaved lands the server's answer: flip the cell, or revert (which is a
// no-op — the glyph never moved) with the toast carrying the status.
func (m *Model) applySaved(msg savedMsg) tea.Cmd {
	if _, busy := m.pendingSaved[msg.trackID]; !busy {
		return nil
	}
	delete(m.pendingSaved, msg.trackID)
	if msg.err != nil {
		return m.setToast("✗ save failed · " + msg.err.Error())
	}
	m.markSaved(msg.trackID, msg.saved)
	if t, ok := m.trackByID(msg.trackID); ok {
		if msg.saved {
			return m.setToast("saved · " + t.Title)
		}
		return m.setToast("unsaved · " + t.Title)
	}
	return nil
}

// markSaved updates the saved flag on every copy of the track the model holds.
func (m *Model) markSaved(id string, saved bool) {
	for i := range m.plTracks {
		if m.plTracks[i].ID == id {
			m.plTracks[i].Saved = saved
		}
	}
	for i := range m.sTracks {
		if m.sTracks[i].ID == id {
			m.sTracks[i].Saved = saved
		}
	}
}

func (m Model) trackByID(id string) (library.Track, bool) {
	for _, t := range m.plTracks {
		if t.ID == id {
			return t, true
		}
	}
	for _, t := range m.sTracks {
		if t.ID == id {
			return t, true
		}
	}
	return library.Track{}, false
}

// --- lazy paging (scope 1: follow `next`, never prefetch everything) ---

// ensureMore is the hook after every cursor move: when the cursor reaches the
// last row of a paged panel, the next page loads.
func (m *Model) ensureMore() tea.Cmd {
	if m.fetch == nil || m.win != winNone {
		return nil
	}
	if m.sView {
		return m.ensureMoreSearch()
	}
	if m.section == SectionPlaylists {
		return m.ensureMorePlaylists()
	}
	return m.ensureMoreItems()
}

func (m *Model) ensureMoreSearch() tea.Cmd {
	if m.sLoading || m.sNext < 0 || m.sErr != nil {
		return nil
	}
	rows := m.visibleRows()
	if m.cursor != len(rows)-1 {
		return nil
	}
	m.sLoading, m.sPending = true, m.sNext
	return m.searchCmd(m.sNext)
}

func (m *Model) ensureMorePlaylists() tea.Cmd {
	if m.plsLoading || m.plsNext < 0 || m.plsErr != nil {
		return nil
	}
	rows := m.visibleRows()
	if m.cursor != len(rows)-1 {
		return nil
	}
	m.plsLoading, m.plsPending = true, m.plsNext
	return m.playlistsCmd(m.plsNext)
}

func (m *Model) ensureMoreItems() tea.Cmd {
	if m.plItemsLoading || m.plItemsErr != nil || m.plNext < 0 {
		return nil
	}
	rows := m.visibleRows()
	if m.cursor != len(rows)-1 {
		return nil
	}
	m.plItemsLoading, m.iPending = true, m.plNext
	return m.itemsCmd(m.plID, m.plNext)
}

// ensurePlaylistsLoaded fires the first playlists page once the session is
// live (EventReady) or the user returns to the library section.
func (m *Model) ensurePlaylistsLoaded() tea.Cmd {
	if m.fetch == nil || m.plsLoaded || m.plsLoading {
		return nil
	}
	m.plsLoading, m.plsPending = true, 0
	return m.playlistsCmd(0)
}

// --- actions on the real catalog ---

// openPlaylist drills into a real playlist. Cached contents are kept: re-entering
// a playlist the user just left must not re-resolve. The playlist's tracks
// are the open list, so the view lands on Tracks (S18).
func (m *Model) openPlaylist(s library.PlaylistSummary) tea.Cmd {
	m.section = SectionTracks
	m.sView = false
	m.cursor, m.listOffset = 0, 0
	if m.plID == s.ID && !m.plIsAlbum && (len(m.plTracks) > 0 || m.plItemsErr != nil || m.plSkipped > 0) {
		return nil
	}
	m.plID, m.plName, m.plOwner = s.ID, s.Name, s.Owner
	m.plPrivate, m.plCollab = s.Private, s.Collaborative
	m.plIsAlbum, m.plArtist = false, ""
	m.plTracks, m.plItemsErr = nil, nil
	m.plTotal, m.plNext = s.TrackCount, -1
	m.plSkipped, m.plTruncated = 0, false
	if m.plTotal == library.NoTrackCount {
		m.plTotal = 0
	}
	m.plItemsLoading, m.iPending = true, 0
	m.rebuildShuffle() // the view changed: the old permutation is void
	return m.itemsCmd(s.ID, 0)
}

// openAlbum drills into a search-result album (T22): the same drill-down a
// playlist gets — the resolver serves spotify:album:<id> with no Web API
// quota and no owner restriction, so the albums in the search results open,
// too. Cached contents are kept the way openPlaylist keeps them. An album
// has no owner, no privacy and no count the resolver reports, so none of
// those facts is invented: the total comes from the resolve itself.
func (m *Model) openAlbum(a library.AlbumSummary) tea.Cmd {
	m.section = SectionTracks
	m.sView = false
	m.cursor, m.listOffset = 0, 0
	if m.plID == a.ID && m.plIsAlbum && (len(m.plTracks) > 0 || m.plItemsErr != nil || m.plSkipped > 0) {
		return nil
	}
	m.plID, m.plName, m.plArtist = a.ID, a.Name, a.Artist
	m.plIsAlbum = true
	m.plOwner, m.plPrivate, m.plCollab = "", false, false
	m.plTracks, m.plItemsErr = nil, nil
	m.plTotal, m.plNext = 0, -1
	m.plSkipped, m.plTruncated = 0, false
	m.plItemsLoading, m.iPending = true, 0
	m.rebuildShuffle() // the view changed: the old permutation is void
	return m.itemsCmd(a.ID, 0)
}

// startSearch runs (or re-runs) a query: one capped request at offset 0.
// The results replace the main list as soon as the query runs, so the view
// switches by itself (T19) — loading, results, error and rate-limit states
// all render there.
func (m *Model) startSearch(query string) tea.Cmd {
	m.sQuery = query
	m.sErr, m.sRateLimited = nil, false
	m.sLoading, m.sPending, m.sNext = true, 0, -1
	m.sTracks, m.sAlbums, m.sPlaylists = nil, nil, nil
	m.sView = true
	m.cursor, m.listOffset = 0, 0
	m.rebuildShuffle() // the view changed: the old permutation is void
	return m.searchCmd(0)
}

// onDebounceTick is the ~250 ms timer of the search window: only the newest
// epoch fires, only a query of at least 2 characters runs, and re-running
// the current query does nothing. Stale responses are dropped later by query
// (applySearch).
func (m *Model) onDebounceTick(seq int) tea.Cmd {
	if m.win != winSearch || seq != m.sDebounce {
		return nil
	}
	q := strings.TrimSpace(m.filter)
	if len([]rune(q)) < 2 || q == m.sQuery {
		return nil
	}
	return m.startSearch(q)
}

// searchLoaded is how many result rows the current search holds.
func (m Model) searchLoaded() int {
	return len(m.sTracks) + len(m.sAlbums) + len(m.sPlaylists)
}

// onRetry is `r`: the DESIGN error state's `r retry`.
func (m *Model) onRetry() tea.Cmd {
	if m.fetch == nil {
		return m.setToast("r · nothing to retry")
	}
	switch {
	case m.section == SectionPlaylists && m.plsErr != nil:
		m.plsErr = nil
		m.plsLoading, m.plsPending = true, 0
		return m.playlistsCmd(0)
	case m.section == SectionTracks && m.plID != "" && m.plItemsErr != nil:
		m.plItemsErr = nil
		m.plItemsLoading, m.iPending = true, 0
		return m.itemsCmd(m.plID, 0)
	case m.sView && m.sErr != nil:
		return m.startSearch(m.sQuery)
	}
	return m.setToast("r · nothing to retry")
}

// --- real-mode rows ---

// realRows is visibleRows for the real catalog (fetch != nil). The search
// results view is transient: it replaces the list whichever section is
// active, but never gets a section cell of its own (T20).
func (m Model) realRows() []row {
	if m.sView {
		return m.searchResultRows()
	}
	if m.section == SectionPlaylists {
		return m.playlistsRows()
	}
	return m.trackRows()
}

func (m Model) playlistsRows() []row {
	rows := []row{{kind: rowHeader, text: theme.GlyphExpanded + " PLAYLISTS", count: m.plsTotal}}
	if !m.expanded["PLAYLISTS"] {
		return rows
	}
	if len(m.pls) == 0 && !m.plsLoading {
		if m.plsErr != nil {
			rows = append(rows, m.errorStatusRow(m.plsErr))
		} else {
			rows = append(rows, row{kind: rowStatus, st: stEmpty, text: theme.GlyphIdle + " no playlists"})
		}
	}
	for _, pl := range m.pls {
		rows = append(rows, row{kind: rowPlaylistItem, text: pl.Name, meta: playlistMeta(pl), pl: pl})
	}
	if m.plsErr != nil && len(m.pls) > 0 {
		rows = append(rows, m.errorStatusRow(m.plsErr))
	}
	if m.plsLoading && len(m.pls) > 0 {
		rows = append(rows, row{kind: rowStatus, st: stLoading, text: theme.GlyphIdle + " loading more…"})
	}
	return rows
}

// errorStatusRow renders the DESIGN error state (`✗ could not load · r retry`),
// carrying the Spotify status and message when present; a 429 renders as the
// info rate-limit state instead (DESIGN global states).
func (m Model) errorStatusRow(err error) row {
	if library.IsRateLimited(err) {
		return row{kind: rowStatus, st: stInfo, text: theme.GlyphIdle + " rate limited · press r to retry"}
	}
	return row{kind: rowStatus, st: stError, text: "✗ could not load", hint: "r retry · " + err.Error()}
}

func (m Model) trackRows() []row {
	tracks := m.orderedTracks()
	rows := make([]row, 0, len(tracks)+2)
	switch {
	case m.section == SectionTracks && m.plID == "":
		rows = append(rows, row{kind: rowStatus, st: stEmpty, text: theme.GlyphIdle + " no playlist open · pick one in playlists"})
	case m.section == SectionTracks:
		rows = m.appendPlaylistStatus(rows, len(tracks))
	}
	for i, t := range tracks {
		rows = append(rows, row{kind: rowTrack, track: t, num: i + 1, playing: m.playlistPlaying(t.ID)})
	}
	if m.section == SectionTracks && m.plID != "" {
		rows = m.appendFootnotes(rows, len(tracks))
	}
	return rows
}

// appendFootnotes puts what the bounded resolver could not show under the
// track list: entries with no playable uri (local files, episodes) and a
// truncated resolve — counted, never silently dropped or pretended complete.
// There is no "loading more" row here by design: the resolver answers one
// bounded set (itemsViaContext), so plNext stays -1 and no second page exists.
func (m Model) appendFootnotes(rows []row, n int) []row {
	if n == 0 {
		return rows // the status row above already carries the facts
	}
	if m.plSkipped > 0 {
		rows = append(rows, row{kind: rowStatus, st: stInfo,
			text: theme.GlyphIdle + fmt.Sprintf(" %d entries not playable · local files or episodes", m.plSkipped)})
	}
	if m.plTruncated {
		rows = append(rows, row{kind: rowStatus, st: stInfo,
			text: theme.GlyphIdle + fmt.Sprintf(" resolver subset · showing %d of %d tracks", n, m.plTotal)})
	}
	return rows
}

// appendPlaylistStatus puts the panel state in place of an empty track list:
// loading, error, the skipped-entries footnote when nothing was playable, or
// a real empty playlist — never an empty list pretending success.
func (m Model) appendPlaylistStatus(rows []row, n int) []row {
	if n > 0 {
		return rows
	}
	switch {
	case m.plItemsErr != nil:
		return append(rows, m.errorStatusRow(m.plItemsErr))
	case m.plItemsLoading:
		return append(rows, row{kind: rowStatus, st: stLoading, text: theme.GlyphIdle + " loading tracks…"})
	case m.plSkipped > 0:
		return append(rows, row{kind: rowStatus, st: stInfo,
			text: theme.GlyphIdle + fmt.Sprintf(" %d entries not playable · local files or episodes", m.plSkipped)})
	default:
		return append(rows, row{kind: rowStatus, st: stEmpty,
			text: theme.GlyphIdle + " this " + m.emptyCollectionWord() + " has no tracks"})
	}
}

// emptyCollectionWord says which kind of list is open: album or playlist —
// the empty state names the truth, never a playlist pretending to be one.
func (m Model) emptyCollectionWord() string {
	if m.plIsAlbum {
		return "album"
	}
	return "playlist"
}

// searchResultRows is the transient results view (T19): type-grouped,
// tracks first, then albums, then playlists — the way the list already
// renders its groups — with the panel states in place: loading, the empty
// state, and the rate-limit row that offers `r`, never a retry loop.
func (m Model) searchResultRows() []row {
	loaded := m.searchLoaded()
	rows := make([]row, 0, loaded+3)
	if loaded == 0 {
		switch {
		case m.sLoading:
			return append(rows, row{kind: rowStatus, st: stLoading, text: theme.GlyphIdle + " searching…"})
		case m.sErr != nil:
			return append(rows, m.errorStatusRow(m.sErr))
		case m.sQuery != "":
			return append(rows, row{kind: rowStatus, st: stEmpty, text: theme.GlyphIdle + " nothing matches"})
		}
	}
	if len(m.sTracks) > 0 {
		rows = append(rows, row{kind: rowHeader, text: "TRACKS", count: len(m.sTracks)})
		for i, t := range m.orderedTracks() {
			rows = append(rows, row{kind: rowTrack, track: t, num: i + 1, playing: m.playlistPlaying(t.ID)})
		}
	}
	if len(m.sAlbums) > 0 {
		rows = append(rows, row{kind: rowHeader, text: "ALBUMS", count: len(m.sAlbums)})
		for _, a := range m.sAlbums {
			rows = append(rows, row{kind: rowAlbumItem, text: a.Name, meta: a.Artist, al: a})
		}
	}
	if len(m.sPlaylists) > 0 {
		rows = append(rows, row{kind: rowHeader, text: "PLAYLISTS", count: len(m.sPlaylists)})
		for _, p := range m.sPlaylists {
			rows = append(rows, row{kind: rowPlaylistItem, text: p.Name, meta: playlistMeta(p), pl: p})
		}
	}
	if m.sLoading && loaded > 0 {
		rows = append(rows, row{kind: rowStatus, st: stLoading, text: theme.GlyphIdle + " loading more…"})
	}
	if m.sErr != nil && loaded > 0 {
		rows = append(rows, m.errorStatusRow(m.sErr))
	}
	return rows
}

// playlistMeta is the right-aligned row meta: owner · privacy · count. A count
// the 2026 API hides renders as · (not-applicable), never a fake 0.
func playlistMeta(pl library.PlaylistSummary) string {
	parts := []string{pl.Owner}
	switch {
	case pl.Collaborative:
		parts = append(parts, "collab")
	case pl.Private:
		parts = append(parts, "private")
	default:
		parts = append(parts, "public")
	}
	if pl.TrackCount != library.NoTrackCount {
		parts = append(parts, fmt.Sprintf("%d", pl.TrackCount))
	}
	return strings.Join(parts, " · ")
}

// The search window's own cursor helpers are gone with the input-only
// window (T19); the rows the window once scrolled now live in the results
// view and use the plain list cursor.
