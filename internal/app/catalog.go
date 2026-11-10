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
	stUnavailable
	stInfo // rate limited: the info role (DESIGN global states)
)

// --- messages ---

// playlistsMsg carries one page of GET /v1/me/playlists (or its error).
type playlistsMsg struct {
	offset int
	page   library.PlaylistPage
	err    error
}

// itemsMsg carries one page of GET /playlists/{id}/items (or its error).
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
	return func() tea.Msg {
		page, err := f.Items(context.Background(), id, offset)
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
	m.plUnavailable = msg.page.Unavailable
	if msg.offset == 0 {
		m.plTracks = msg.page.Tracks
	} else {
		m.plTracks = append(m.plTracks, msg.page.Tracks...)
	}
	m.plTotal = msg.page.Total
	m.plNext = msg.page.NextOffset
	m.clampCursor()
}

func (m *Model) applySearch(msg searchMsg) {
	if msg.query != m.sQuery || msg.offset != m.sPending {
		return // stale: a newer query is already running
	}
	m.sPending, m.sLoading = -1, false
	if msg.err != nil {
		if library.IsRateLimited(msg.err) {
			m.sRateLimited, m.sErr = true, nil
		} else {
			m.sErr, m.sRateLimited = msg.err, false
		}
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
	m.clampWinCursor()
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
	if m.section == SectionLibrary && !m.drillDown {
		return m.ensureMorePlaylists()
	}
	return m.ensureMoreItems()
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
	if m.plItemsLoading || m.plUnavailable != "" || m.plItemsErr != nil || m.plNext < 0 {
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
// a playlist the user just left must not refetch.
func (m *Model) openPlaylist(s library.PlaylistSummary) tea.Cmd {
	m.section = SectionLibrary
	m.drillDown = true
	m.cursor, m.listOffset = 0, 0
	if m.plID == s.ID && (len(m.plTracks) > 0 || m.plUnavailable != "" || m.plItemsErr != nil) {
		return nil
	}
	m.plID, m.plName, m.plOwner = s.ID, s.Name, s.Owner
	m.plPrivate, m.plCollab = s.Private, s.Collaborative
	m.plTracks, m.plUnavailable, m.plItemsErr = nil, "", nil
	m.plTotal, m.plNext = s.TrackCount, -1
	if m.plTotal == library.NoTrackCount {
		m.plTotal = 0
	}
	m.plItemsLoading, m.iPending = true, 0
	return m.itemsCmd(s.ID, 0)
}

// startSearch runs (or re-runs) a query: one capped request at offset 0.
func (m *Model) startSearch(query string) tea.Cmd {
	m.sQuery = query
	m.sErr, m.sRateLimited = nil, false
	m.sLoading, m.sPending, m.sNext = true, 0, -1
	m.sTracks, m.sAlbums, m.sPlaylists = nil, nil, nil
	m.winCursor, m.winOffset = 0, 0
	return m.searchCmd(0)
}

// onRetry is `r`: the DESIGN error state's `r retry`.
func (m *Model) onRetry() tea.Cmd {
	if m.fetch == nil {
		return m.setToast("r · nothing to retry")
	}
	switch {
	case m.section == SectionLibrary && !m.drillDown && m.plsErr != nil:
		m.plsErr = nil
		m.plsLoading, m.plsPending = true, 0
		return m.playlistsCmd(0)
	case m.section == SectionLibrary && m.drillDown && m.plItemsErr != nil:
		m.plItemsErr = nil
		m.plItemsLoading, m.iPending = true, 0
		return m.itemsCmd(m.plID, 0)
	case m.win == winSearch && m.sErr != nil:
		return m.startSearch(m.sQuery)
	}
	return m.setToast("r · nothing to retry")
}

// --- real-mode rows ---

// realRows is visibleRows for the real catalog (fetch != nil).
func (m Model) realRows() []row {
	if m.section == SectionLibrary && !m.drillDown {
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
	tracks := m.realTracksForView()
	rows := make([]row, 0, len(tracks)+2)
	switch {
	case m.section == SectionLibrary && m.drillDown:
		rows = m.appendPlaylistStatus(rows, len(tracks))
	case m.section == SectionSearch && len(tracks) == 0 && m.sQuery == "" && !m.sLoading && m.sErr == nil:
		rows = append(rows, row{kind: rowStatus, st: stEmpty,
			text: theme.GlyphIdle + " nothing searched yet", hint: "/ opens search"})
	}
	for i, t := range tracks {
		rows = append(rows, row{kind: rowTrack, track: t, num: i + 1, playing: m.playlistPlaying(t.ID)})
	}
	if m.section == SectionLibrary && m.drillDown && m.plItemsLoading && len(tracks) > 0 {
		rows = append(rows, row{kind: rowStatus, st: stLoading, text: theme.GlyphIdle + " loading more…"})
	}
	return rows
}

// appendPlaylistStatus puts the panel state in place of an empty track list:
// loading, error, the honest unavailable state (2026: not owner/collaborator)
// or a real empty playlist — never an empty list pretending success.
func (m Model) appendPlaylistStatus(rows []row, n int) []row {
	if n > 0 {
		return rows
	}
	switch {
	case m.plItemsErr != nil:
		return append(rows, m.errorStatusRow(m.plItemsErr))
	case m.plUnavailable != "":
		return append(rows, row{kind: rowStatus, st: stUnavailable, text: theme.GlyphIdle + " " + m.plUnavailable})
	case m.plItemsLoading:
		return append(rows, row{kind: rowStatus, st: stLoading, text: theme.GlyphIdle + " loading tracks…"})
	default:
		return append(rows, row{kind: rowStatus, st: stEmpty, text: theme.GlyphIdle + " this playlist has no tracks"})
	}
}

func (m Model) realTracksForView() []library.Track {
	switch m.section {
	case SectionSearch:
		return m.sTracks
	case SectionQueue:
		return m.data.Queue()
	default:
		if !m.drillDown {
			return nil
		}
		return m.plTracks
	}
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

// clampWinCursor keeps the window cursor inside the (newly changed) rows.
func (m *Model) clampWinCursor() {
	if n := m.windowRowCount(); m.winCursor >= n {
		m.winCursor = n - 1
	}
	if m.winCursor < 0 {
		m.winCursor = 0
	}
	m.syncWinScroll()
}

// searchHit is one actionable row of the real search results.
type searchHit struct {
	kind     int // 0 track, 1 album, 2 playlist
	track    library.Track
	album    library.AlbumSummary
	playlist library.PlaylistSummary
}

// searchHits lists the actionable rows of the current search results in the
// order the window renders them (tracks, then albums, then playlists).
func (m Model) searchHits() []searchHit {
	hits := make([]searchHit, 0, len(m.sTracks)+len(m.sAlbums)+len(m.sPlaylists))
	for _, t := range m.sTracks {
		hits = append(hits, searchHit{kind: 0, track: t})
	}
	for _, a := range m.sAlbums {
		hits = append(hits, searchHit{kind: 1, album: a})
	}
	for _, p := range m.sPlaylists {
		hits = append(hits, searchHit{kind: 2, playlist: p})
	}
	return hits
}

// searchHitAtCursor resolves the row under the window cursor to its hit;
// group-header rows resolve to nothing.
func (m Model) searchHitAtCursor() (searchHit, bool) {
	rows := m.searchSpec().rows
	if m.winCursor < 0 || m.winCursor >= len(rows) {
		return searchHit{}, false
	}
	if rows[m.winCursor].label {
		return searchHit{}, false
	}
	hits := m.searchHits()
	idx := 0
	for _, r := range rows[:m.winCursor] {
		if !r.label {
			idx++
		}
	}
	if idx >= len(hits) {
		return searchHit{}, false
	}
	return hits[idx], true
}

// searchEnter handles enter in the search window (real mode): a changed query
// runs, otherwise the cursor row acts — a track plays, a playlist opens, an
// album says honestly why it does not open (S14: no album-contents endpoint
// in this slice's scope).

func (m *Model) searchEnter() tea.Cmd {
	if q := strings.TrimSpace(m.filter); q != "" && q != m.sQuery {
		return m.startSearch(q)
	}
	hits := m.searchHits()
	if m.sErr != nil && len(hits) == 0 {
		return m.startSearch(m.sQuery) // retry the failed search with enter
	}
	h, ok := m.searchHitAtCursor()
	if !ok {
		return nil
	}
	switch h.kind {
	case 0:
		m.closeWindow(false)
		if err := m.player.Play(h.track); err != nil {
			return m.setToast("✗ " + err.Error())
		}
		return m.setToast("▶ " + h.track.Title)
	case 2:
		m.closeWindow(false)
		return m.openPlaylist(h.playlist)
	default:
		return m.setToast("○ albums do not open · search a track or playlist")
	}
}
