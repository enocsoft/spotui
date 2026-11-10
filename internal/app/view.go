package app

import (
	"fmt"
	"github.com/nahuel/spotify-tui/internal/config"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/nahuel/spotify-tui/internal/library"
	"github.com/nahuel/spotify-tui/internal/ui/theme"
)

// Mock chrome until slice 2 wires the real account (artboard copy).
const (
	mockUser  = "nahuel"
	mockPlan  = "premium"
	drawerRow = 8
	minWidth  = 80
)

var ansiRe = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

// visibleWidth is the printable width of a styled string. All chrome content
// is narrow runes (Measured Glyph Rule), so rune count is the cell width.
func visibleWidth(s string) int { return len([]rune(stripANSI(s))) }

// truncate cuts s to w cells, appending the truncation glyph. Lists truncate,
// they never wrap. Call it on plain text, before styling.
func truncate(s string, w int) string {
	r := []rune(s)
	if len(r) <= w {
		return s
	}
	if w <= 1 {
		return string(r[:1])
	}
	return string(r[:w-1]) + theme.GlyphTruncation
}

func padRight(s string, w int) string {
	if d := w - visibleWidth(s); d > 0 {
		return s + strings.Repeat(" ", d)
	}
	return s
}

func padLeft(s string, w int) string {
	if d := w - visibleWidth(s); d > 0 {
		return strings.Repeat(" ", d) + s
	}
	return s
}

func rule(w int) string { return theme.RuleStyle.Render(strings.Repeat(theme.GlyphRule, w)) }

// matchedStyle is the matched substring of a result label (T24, DESIGN §7
// search: "the matched substring bold"): the value role's colour at bold
// weight — a weight, never a new colour, and no new theme token. It is
// composed from the theme roles here, the way the floating-window styles
// are.
var matchedStyle = lipgloss.NewStyle().Bold(true).Foreground(theme.Text2)

// tableCols distributes the terminal width over the track-table columns.
// ≥140: every column (DESIGN breakpoint table); 100–139: ADDED dropped;
// 80–99: narrower artist/album. showAdded decides whether the table carries
// ADDED at all (T28): the column set may differ per view. The context
// resolver and the search API never return added_at, so real-mode track
// tables (playlist, album, search results) drop it instead of promising a
// column their data cannot fill; the mock fixture carries dates and keeps it.
type tableCols struct{ num, title, artist, album, saved, added, timeC int }

func tableLayout(w int, showAdded bool) tableCols {
	c := tableCols{num: 4, saved: 5, timeC: 8}
	switch {
	case w >= 140:
		c.artist, c.album, c.added = 30, 34, 10
	case w >= 100:
		c.artist, c.album = 30, 34
	default:
		c.artist, c.album = 18, 20
	}
	if !showAdded {
		c.added = 0
	}
	fixed := c.num + c.artist + c.album + c.saved + c.added + c.timeC
	seps := 5
	if c.added > 0 {
		seps = 6
	}
	c.title = w - 2 - fixed - seps // 1-cell inset each side
	if c.title < 4 {
		c.title = 4
	}
	return c
}

// showAddedColumn reports whether the table may render the ADDED column:
// only where the data exists. The resolver-backed playlist and album
// contents (and search results) never carry added_at; the mock does.
func (m Model) showAddedColumn() bool { return m.fetch == nil }

// listHeight is the frame rows the full-screen list owns.
func (m Model) listHeight() int {
	playerRows := 1
	if m.w >= 100 {
		playerRows = 2
	}
	fixed := 4 + 1 + 1 + playerRows // top bar, rule, context, rule + rule + status
	if m.drawerOpen && m.w >= 100 {
		fixed += 1 + 1 + drawerRow // rule above the drawer, band header, rows
	}
	h := m.h - fixed
	if h < 1 {
		h = 1
	}
	return h
}

// visibleDataRows is how many data rows fit below the table header.
func (m Model) visibleDataRows() int {
	h := m.listHeight()
	if m.sView || m.section != SectionTracks {
		return h
	}
	h--
	if h < 0 {
		h = 0
	}
	return h
}

// View renders the whole frame (T3): section row, context strip, full-screen
// list, queue drawer, player bar, status bar. Total lines == terminal height.
// Full-screen player states (login, bootstrap failure) replace the frame. A
// floating window (S19) overlays the list region without moving it.
func (m Model) View() string {
	if m.w < minWidth {
		return m.refusalView()
	}
	if m.playerErr != nil {
		return m.playerErrorView()
	}
	if m.connecting {
		return m.connectingView()
	}
	if m.loginURL != "" {
		return m.loginView()
	}
	var out []string
	out = append(out, m.topBar(m.w), rule(m.w), m.contextStrip(m.w), rule(m.w))
	out = append(out, m.listBody(m.listHeight())...)
	if m.drawerOpen && m.w >= 100 {
		out = append(out, rule(m.w), m.drawerHead(m.w))
		out = append(out, m.drawerRows(m.w)...)
	}
	out = append(out, rule(m.w))
	out = append(out, m.playerBar(m.w)...)
	out = append(out, m.statusBar(m.w))
	if m.win != winNone {
		m.overlayWindow(out)
	}
	return strings.Join(out, "\n")
}

// messageView renders full-screen player states (login, bootstrap failure):
// one padded canvas of h lines, every line inside the width budget.
func (m Model) messageView(lines []string) string {
	out := make([]string, 0, m.h)
	for _, l := range lines {
		if visibleWidth(l) > m.w {
			l = theme.ValueStyle.Render(truncate(stripANSI(l), m.w))
		}
		out = append(out, l)
	}
	for len(out) < m.h {
		out = append(out, "")
	}
	return strings.Join(out[:m.h], "\n")
}

// loginView surfaces the interactive auth URL instead of hanging silently
// (spec S9): the URL and where the callback lands, which login is asking, keys
// at the bottom.
func (m Model) loginView() string {
	lines := []string{
		"",
		theme.TitleStyle.Render("login required"),
		"",
		theme.ValueStyle.Render("open this URL in a browser and approve the Spotify access:"),
		"",
		theme.MetaStyle.Render(m.loginNames()),
	}
	for _, chunk := range wrapURL(m.loginURL, m.w-4) {
		lines = append(lines, theme.AccentStyle.Render(chunk))
	}
	if m.loginFile != "" {
		lines = append(lines, theme.MetaStyle.Render("full URL saved to "+m.loginFile))
	}
	lines = append(lines,
		"",
		theme.MetaStyle.Render("the callback lands on "+loginCallbackURL(m.cfg.CallbackPort)+
			" — playback starts when you return"),
		"",
		theme.MetaStyle.Render("build "+Revision()),
		theme.LabelStyle.Render("o open in browser · ctrl+c quit"),
	)
	return m.messageView(lines)
}

// loginNames says which of the two logins is asking (two tokens, one purpose
// each): the playback one runs on librespot's client id, the library one on
// the dashboard's. An unlabeled event still reads as playback.
func (m Model) loginNames() string {
	if m.loginPurpose == library.LoginPurposeLibrary {
		return "this login is for the library · playlists, search and saved tracks"
	}
	return "this login is for playback · the embedded player session"
}

// wrapURL breaks a long URL into chunks that fit width, cutting at "&" so every
// line ends on a parameter boundary. A URL is not a list to truncate: a cut URL
// reaches the browser without parameters and the server blames the wrong thing
// (a lost response_type reads as "response_type must be code").
func wrapURL(raw string, width int) []string {
	if width < 8 {
		width = 8
	}
	if len(raw) <= width {
		return []string{raw}
	}
	var out []string
	rest := raw
	for len(rest) > width {
		cut := strings.LastIndex(rest[:width], "&")
		if cut <= 0 {
			cut = width
		}
		out = append(out, rest[:cut])
		rest = rest[cut:]
	}
	return append(out, rest)
}

// playerErrorView surfaces a bootstrap failure (auth, no Premium, dead
// output server) instead of failing silently.
func (m Model) playerErrorView() string {
	return m.messageView([]string{
		"",
		theme.TitleStyle.Render("player error"),
		"",
		theme.ValueStyle.Render("✗ " + m.playerErr.Error()),
		"",
		theme.MetaStyle.Render("usual causes: an account without Premium, or stale credentials in the state dir"),
		"",
		theme.MetaStyle.Render("build " + Revision()),
		theme.LabelStyle.Render("ctrl+c quit"),
	})
}

func (m Model) refusalView() string {
	lines := []string{
		"spotify-tui needs at least 80 columns.",
		"",
		"resize the terminal, or press q to quit.",
		"tab view · q drawer · : cmd · ? help",
	}
	for i, l := range lines {
		lines[i] = theme.ValueStyle.Render(truncate(l, m.w))
	}
	return strings.Join(lines, "\n")
}

// topBar: brand, then the sections as │ cells. Active on surface, bold; the
// rest muted (The Sections-Only Rule). H/L shift the window sideways; the row
// never grows a second line.
func (m Model) topBar(w int) string {
	sep := theme.LabelStyle.Render(theme.GlyphDivider)
	left := theme.LiveStyle.Render(theme.GlyphActive) + " " + theme.TitleStyle.Render("spotify")
	right := theme.LabelStyle.Render(mockUser) + "   " + theme.LabelStyle.Render(mockPlan)
	budget := w - visibleWidth(right)
	for _, s := range sections[m.sectionShift:] {
		cell := " " + s.String() + " "
		var styled string
		if s == m.section {
			styled = theme.SelectedStyle.Render(cell)
		} else {
			styled = theme.LabelStyle.Render(cell)
		}
		next := left + "   " + sep + "   " + styled
		if visibleWidth(next) > budget {
			break
		}
		left = next
	}
	return padRight(left, w-visibleWidth(right)) + right
}

// contextStrip: one row — where you are, owner/privacy, then the facts that
// appear nowhere else (One Fact Rule). The search results name the query and
// their counts (T19). Real mode reads the catalog state; the mock keeps the
// fixture playlist.
func (m Model) contextStrip(w int) string {
	if m.fetch != nil {
		return m.realContextStrip(w)
	}
	var left, right string
	switch {
	case m.section == SectionTracks:
		pl := m.data.Playlist()
		left = theme.LabelStyle.Render("playlists") + " " + theme.GlyphCrumb + " " +
			theme.TitleStyle.Render(truncate(pl.Name, w/3)) + "  " +
			theme.MetaStyle.Render(pl.Owner+" · private")
		right = theme.MetaStyle.Render(fmt.Sprintf("%d tracks · %s", len(pl.Tracks), fmtTotal(totalDuration(pl.Tracks)))) +
			" " + theme.LabelStyle.Render(theme.GlyphDivider) + " " +
			theme.MetaStyle.Render("sort: added "+theme.GlyphSortDesc)
	default:
		// Playlists navigation
		left = theme.TitleStyle.Render("playlists")
		right = theme.MetaStyle.Render(fmt.Sprintf("%d sections", len(m.data.Library())))
	}
	if lw, rw := visibleWidth(left), visibleWidth(right); lw+rw+1 > w {
		return theme.ValueStyle.Render(truncate(stripANSI(left), w))
	}
	return padRight(left, w-visibleWidth(right)) + right
}

// realContextStrip is the context strip over catalog state (T7): the Tracks
// view carries the open playlist with owner and privacy, the Playlists panel
// the fetched total, and the search results the query and its group counts
// (T19). Total time only shows when the whole list is loaded — a partial page
// cannot claim the total.
func (m Model) realContextStrip(w int) string {
	var left, right string
	switch {
	case m.sView:
		left = theme.LabelStyle.Render("search") + " " + theme.GlyphCrumb + " " +
			theme.TitleStyle.Render(truncate(m.sQuery, w/3))
		switch {
		case m.sLoading && m.searchLoaded() == 0:
			right = theme.MetaStyle.Render("searching…")
		case m.sErr != nil:
			if m.sRateLimited {
				right = theme.MetaStyle.Render("rate limited")
			} else {
				right = theme.MetaStyle.Render("search failed")
			}
		default:
			right = theme.MetaStyle.Render(fmt.Sprintf("%d track%s · %d album%s",
				len(m.sTracks), plural(len(m.sTracks)), len(m.sAlbums), plural(len(m.sAlbums))))
			if len(m.sPlaylists) > 0 {
				right = theme.MetaStyle.Render(fmt.Sprintf("%d track%s · %d album%s · %d playlist%s",
					len(m.sTracks), plural(len(m.sTracks)), len(m.sAlbums), plural(len(m.sAlbums)),
					len(m.sPlaylists), plural(len(m.sPlaylists))))
			}
		}
	case m.section == SectionTracks && m.plID == "":
		left = theme.TitleStyle.Render("tracks")
		right = theme.MetaStyle.Render("no playlist open")
	case m.section == SectionTracks && m.plIsAlbum:
		// The open list is a search-result album (T22): the strip names the
		// album and its artist — the facts the search summary carries. There
		// is no owner, privacy or year to invent.
		left = theme.LabelStyle.Render("albums") + " " + theme.GlyphCrumb + " " +
			theme.TitleStyle.Render(truncate(m.plName, w/3)) + "  " +
			theme.MetaStyle.Render(m.plArtist)
		right = theme.MetaStyle.Render("tracks")
		if m.plTotal > 0 {
			right = theme.MetaStyle.Render(fmt.Sprintf("%d tracks", m.plTotal))
		}
		if m.plNext < 0 && m.plItemsErr == nil && len(m.plTracks) > 0 {
			right = theme.MetaStyle.Render(fmt.Sprintf("%d tracks · %s", m.plTotal, fmtTotal(totalDuration(m.plTracks))))
		}
	case m.section == SectionTracks:
		left = theme.LabelStyle.Render("playlists") + " " + theme.GlyphCrumb + " " +
			theme.TitleStyle.Render(truncate(m.plName, w/3)) + "  " +
			theme.MetaStyle.Render(m.plOwner+" · "+m.privacyWord())
		right = theme.MetaStyle.Render(fmt.Sprintf("%d tracks", m.plCount()))
		if m.plNext < 0 && m.plItemsErr == nil && len(m.plTracks) > 0 {
			right = theme.MetaStyle.Render(fmt.Sprintf("%d tracks · %s", m.plTotal, fmtTotal(totalDuration(m.plTracks))))
		}
	default:
		// Playlists navigation
		left = theme.TitleStyle.Render("playlists")
		switch {
		case m.plsErr != nil:
			right = theme.MetaStyle.Render("could not load")
		case m.plsLoading && m.plsTotal == 0:
			right = theme.MetaStyle.Render("loading…")
		default:
			right = theme.MetaStyle.Render(fmt.Sprintf("%d playlists", m.plsTotal))
		}
	}
	if lw, rw := visibleWidth(left), visibleWidth(right); lw+rw+1 > w {
		return theme.ValueStyle.Render(truncate(stripANSI(left), w))
	}
	return padRight(left, w-visibleWidth(right)) + right
}

// plural is the singular/plural tail of a count fact.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// privacyWord renders the playlist privacy fact (never invented: collab wins).
func (m Model) privacyWord() string {
	switch {
	case m.plCollab:
		return "collab"
	case m.plPrivate:
		return "private"
	default:
		return "public"
	}
}

// plCount is the known track count of the focused playlist: the server total
// once contents arrive, the listing's count before that, · otherwise.
func (m Model) plCount() int {
	if m.plTotal > 0 {
		return m.plTotal
	}
	return m.plKnownCount()
}

func (m Model) plKnownCount() int {
	for _, pl := range m.pls {
		if pl.ID == m.plID {
			return pl.TrackCount
		}
	}
	return library.NoTrackCount
}

// listBody renders exactly h lines: the table header (in track views) plus
// the visible data rows, padded to h. Nothing may overflow the width.
func (m Model) listBody(h int) []string {
	rows := m.visibleRows()
	out := make([]string, 0, h)
	dataRows := h
	cols := tableLayout(m.w, m.showAddedColumn())
	// The table header belongs to a track list: the Tracks view (open playlist
	// or the mock's open list). The results view renders mixed kinds, the
	// Playlists navigation its own groups — no header there.
	inTrackView := m.section == SectionTracks && !m.sView
	if inTrackView {
		out = append(out, tableHeader(cols))
		dataRows--
	}
	if dataRows < 0 {
		dataRows = 0
	}
	from := m.listOffset
	if from > len(rows) {
		from = 0
	}
	to := from + dataRows
	if to > len(rows) {
		to = len(rows)
	}
	for i, r := range rows[from:to] {
		sel := from+i == m.cursor
		if r.kind == rowTrack {
			out = append(out, m.trackLine(cols, r, sel))
		} else if r.kind == rowStatus {
			out = append(out, m.statusLine(r, sel))
		} else {
			out = append(out, m.libraryRow(r, sel))
		}
	}
	for len(out) < h {
		out = append(out, "")
	}
	return out
}

// trackLine renders one track row. Selected rows take the surface tint and
// bold (current role); the playing row takes the rail tint (DESIGN §3).
func (m Model) trackLine(c tableCols, r row, selected bool) string {
	num := padLeft(fmt.Sprintf("%d", r.num), c.num)
	if r.playing {
		num = theme.GlyphPlaying
	}
	cells := []string{
		padLeft(num, c.num),
		padRight(truncate(r.track.Title, c.title), c.title),
		padRight(truncate(r.track.Artist, c.artist), c.artist),
		padRight(truncate(r.track.Album, c.album), c.album),
		padRight(savedGlyph(r.track.Saved), c.saved),
	}
	if c.added > 0 {
		cells = append(cells, padRight(r.track.Added, c.added))
	}
	cells = append(cells, padLeft(fmtDur(r.track.Duration), c.timeC))
	plain := " " + strings.Join(cells, " ")
	if selected {
		return theme.SelectedStyle.Width(m.w).Render(plain)
	}
	if r.playing {
		return theme.PlayingStyle.Width(m.w).Render(plain)
	}
	styled := " " + strings.Join([]string{
		theme.NumberStyle.Render(padLeft(num, c.num)),
		m.trackTitleCell(r, c),
		theme.ValueStyle.Render(padRight(truncate(r.track.Artist, c.artist), c.artist)),
		theme.ValueStyle.Render(padRight(truncate(r.track.Album, c.album), c.album)),
		theme.LabelStyle.Render(padRight(savedGlyph(r.track.Saved), c.saved)),
		theme.NumberStyle.Render(padLeft(fmtDur(r.track.Duration), c.timeC)),
	}, " ")
	if c.added > 0 {
		// ADDED sits before TIME; splice it in to keep the budget identical.
		i := len(cells) - 1
		styled = " " + strings.Join(append([]string{
			theme.NumberStyle.Render(padLeft(num, c.num)),
			m.trackTitleCell(r, c),
			theme.ValueStyle.Render(padRight(truncate(r.track.Artist, c.artist), c.artist)),
			theme.ValueStyle.Render(padRight(truncate(r.track.Album, c.album), c.album)),
			theme.LabelStyle.Render(padRight(savedGlyph(r.track.Saved), c.saved)),
			theme.LabelStyle.Render(padRight(r.track.Added, c.added)),
			theme.NumberStyle.Render(padLeft(fmtDur(r.track.Duration), c.timeC)),
		}, cells[:0]...), " ")
		_ = i
	}
	return styled
}

func tableHeader(c tableCols) string {
	cells := []string{
		padLeft("#", c.num),
		padRight("TITLE", c.title),
		padRight("ARTIST", c.artist),
		padRight("ALBUM", c.album),
		padRight("SAVED", c.saved),
	}
	if c.added > 0 {
		cells = append(cells, padRight("ADDED", c.added))
	}
	cells = append(cells, padLeft("TIME", c.timeC))
	return " " + theme.LabelStyle.Render(strings.Join(cells, " "))
}

// trackTitleCell is the TITLE cell of a track row: the value role, with the
// search query's case-insensitive matched substring bold (T24). Selected and
// playing rows render plain: their whole-row roles already read as focus and
// live, and no styled segment may sit inside their surface/rail bands.
func (m Model) trackTitleCell(r row, c tableCols) string {
	title := truncate(r.track.Title, c.title)
	if r.match != "" {
		return padRight(highlightMatch(title, r.match), c.title)
	}
	return theme.ValueStyle.Render(padRight(title, c.title))
}

func savedGlyph(saved bool) string {
	if saved {
		return theme.GlyphSaved
	}
	return theme.GlyphNotSaved
}

// highlightMatch renders a label with its case-insensitive matched
// substring bold (T24): the match takes the bold weight of the value role's
// own colour (theme.MatchedStyle — a weight, never a new colour); every
// other byte keeps the plain value style. Each segment is styled on its
// own, so no nested reset can leak. No match, no query: the label renders
// plain, byte-identical to the unhighlighted path.
func highlightMatch(text, query string) string {
	if query == "" {
		return theme.ValueStyle.Render(text)
	}
	lower := []rune(strings.ToLower(text))
	want := []rune(strings.ToLower(query))
	start := -1
	for i := 0; i+len(want) <= len(lower); i++ {
		ok := true
		for j := range want {
			if lower[i+j] != want[j] {
				ok = false
				break
			}
		}
		if ok {
			start = i
			break
		}
	}
	if start < 0 {
		return theme.ValueStyle.Render(text)
	}
	runes := []rune(text)
	end := start + len(want)
	var b strings.Builder
	if start > 0 {
		b.WriteString(theme.ValueStyle.Render(string(runes[:start])))
	}
	b.WriteString(matchedStyle.Render(string(runes[start:end])))
	if end < len(runes) {
		b.WriteString(theme.ValueStyle.Render(string(runes[end:])))
	}
	return b.String()
}

// libraryRow renders library-root rows: section headers and collection items,
// name left, count right (DESIGN §7 library view). Real playlist rows carry a
// meta segment (owner · privacy · count) instead of the bare count; search
// result albums and playlists reuse the same item shape with their meta.
func (m Model) libraryRow(r row, selected bool) string {
	w := m.w
	indent := " "
	if r.kind == rowPlaylistItem || r.kind == rowAlbumItem {
		indent = "   "
	}
	text := r.text
	if r.kind == rowHeader && selected {
		// T24: the focused group header carries the selected glyph, so the
		// cursor moving between groups stays obvious without colour alone
		// (Never-Color-Alone Rule).
		if t := strings.TrimPrefix(text, theme.GlyphExpanded+" "); t != text {
			text = theme.GlyphSelected + " " + t
		}
	}
	if (r.kind == rowPlaylistItem || r.kind == rowAlbumItem) && r.meta != "" {
		line := truncate(indent+text, w-visibleWidth(r.meta)-3)
		if selected {
			plain := padRight(line, w-1-visibleWidth(r.meta)) + r.meta
			return theme.SelectedStyle.Width(w).Render(plain)
		}
		var label string
		if r.match != "" {
			// T24: bold the matched substring of the label; the meta keeps its role
			label = padRight(highlightMatch(line, r.match), w-1-visibleWidth(r.meta))
		} else {
			label = padRight(theme.ValueStyle.Render(line), w-1-visibleWidth(r.meta))
		}
		return label + theme.MetaStyle.Render(r.meta)
	}
	count := fmt.Sprintf("%d", r.count)
	plain := padRight(indent+text, w-9) + padLeft(count, 8)
	if selected {
		return theme.SelectedStyle.Width(w).Render(plain)
	}
	name := theme.LabelStyle.Render(indent + text)
	if r.kind == rowPlaylistItem || r.kind == rowAlbumItem {
		name = theme.ValueStyle.Render(indent + text)
	}
	return padRight(name, w-9) + theme.NumberStyle.Render(padLeft(count, 8))
}

// statusLine renders a panel state row in place: loading / empty / info in
// the muted meta role, error in the value role with its reason. Selected
// states take the surface band like any row.
func (m Model) statusLine(r row, selected bool) string {
	line := " " + r.text
	if r.hint != "" {
		line += " · " + r.hint
	}
	line = truncate(stripANSI(line), m.w-2)
	if selected {
		return theme.SelectedStyle.Width(m.w).Render(" " + padRight(line, m.w-2))
	}
	style := theme.MetaStyle
	if r.st == stError {
		style = theme.ValueStyle
	}
	return " " + style.Render(padRight(line, m.w-2))
}

// drawerHead: ▾ QUEUE · up next 8 of 41 · total · repeat mode (rail band).
// While radio is active (T26) the count is what radio has lined up: the
// drawer mirrors what plays next, not the untouched open list.
func (m Model) drawerHead(w int) string {
	q := m.drawerSource()
	left := theme.GlyphExpanded + " QUEUE  " + fmt.Sprintf("up next %d of %d", drawerRow, len(q))
	right := fmt.Sprintf("%s · repeat %s", fmtTotal(totalDuration(q)), m.data.Session().Repeat)
	if visibleWidth(left)+visibleWidth(right)+2 > w {
		right = ""
	}
	line := padRight(" "+left, w-visibleWidth(right)-1) + right
	return theme.DrawerStyle.Width(w).Render(truncate(line, w))
}

// drawerSource is what the drawer mirrors right now: what radio has lined up
// while radio is active (T26), otherwise the session queue.
func (m Model) drawerSource() []library.Track {
	if m.radioOn && len(m.radioUpNext) > 0 {
		return m.radioUpNext
	}
	return m.data.Queue()
}

// drawerRows: up next only; the player bar owns the current track (S4).
// While radio is active and has lined-up tracks (T26) the drawer shows those:
// radio plays outside the open list.
func (m Model) drawerRows(w int) []string {
	q := m.drawerSource()
	if m.shuffleOn && !(m.radioOn && len(m.radioUpNext) > 0) {
		// Shuffle on: the drawer mirrors the shuffled order — the same walk
		// `n` takes (T21).
		if up := m.shuffleUpNext(); len(up) > 0 {
			q = up
		}
	}
	if len(q) > drawerRow {
		q = q[:drawerRow]
	}
	out := make([]string, 0, drawerRow)
	for i, t := range q {
		glyph := " "
		if i == 0 {
			glyph = theme.GlyphSelected
		}
		line := fmt.Sprintf(" %s %s · %s  %s", glyph, t.Title, t.Artist, t.Album)
		line = padRight(truncate(line, w-8), w-8) + padLeft(fmtDur(t.Duration), 7)
		out = append(out, theme.DrawerStyle.Width(w).Render(line))
	}
	for len(out) < drawerRow {
		out = append(out, theme.DrawerStyle.Width(w).Render(strings.Repeat(" ", w)))
	}
	return out
}

// playerBar: 2 rows at ≥100 columns, 1 row below (DESIGN §4 player states).
func (m Model) playerBar(w int) []string {
	s := m.data.Session()
	title, artist := "", ""
	if t, ok := m.currentTrack(); ok {
		title, artist = t.Title, t.Artist
	}
	glyph := theme.GlyphPlaying
	if s.State == library.StatePaused {
		glyph = theme.GlyphPaused
	} else if s.State == library.StateNoOutput || s.State == library.StateStarting {
		glyph = theme.GlyphIdle
	}

	var output string
	if s.State == library.StateNoOutput {
		output = theme.MetaStyle.Render(theme.GlyphIdle + " no audio output · d")
	} else if s.State == library.StateStarting {
		output = theme.MetaStyle.Render("player starting…")
	} else {
		output = theme.MetaStyle.Render(s.Bitrate) + " " + theme.LabelStyle.Render(theme.GlyphDivider) + " " +
			theme.LiveStyle.Render(theme.GlyphSelected) + " " + theme.ValueStyle.Render(s.Output)
	}
	outW := visibleWidth(output)

	if w < 100 {
		// one row: state glyph, title, progress track, output; no times
		titleBudget := w - 2 - 16 - 2 - outW
		if titleBudget < 6 {
			output, outW = "", 0
			titleBudget = w - 2 - 16
		}
		if titleBudget > 40 {
			titleBudget = 40
		}
		line := padRight(theme.LiveStyle.Render(glyph)+" "+theme.TitleStyle.Render(truncate(title, titleBudget-2)), titleBudget) +
			"  " + m.progressCell(16)
		if output != "" {
			line += "  " + output
		}
		return []string{theme.PlayerStyle.Width(w).Render(line)}
	}

	midBudget := w - 2 - 40 - outW
	if midBudget < 8 {
		midBudget = 8
	}
	mid := theme.MetaStyle.Render(truncate(artist+" — "+m.albumOf(), midBudget))
	row1 := padRight(theme.LiveStyle.Render(glyph)+" "+theme.TitleStyle.Render(truncate(title, 38)), 40) +
		padRight(mid, midBudget) + output

	mode := "shuffle off · repeat " + s.Repeat
	if m.shuffleOn {
		mode = "shuffle on · repeat " + s.Repeat
	}
	vol := m.volumeCell(s.Volume)
	// Radio (T26) reports in the live role, next to the shuffle/repeat text;
	// the word is a state (Never-Color-Alone Rule), no new glyph.
	radioTxt := ""
	if m.radioOn {
		radioTxt = " · " + theme.LiveStyle.Render("radio on")
	}
	modeBudget := w - 2 - 40 - visibleWidth(vol) - visibleWidth(radioTxt)
	if modeBudget < 8 {
		modeBudget = 8
	}
	row2 := padRight(m.progressCell(30), 40) +
		padRight(theme.MetaStyle.Render(truncate(mode, modeBudget)), modeBudget) +
		radioTxt + vol
	return []string{
		theme.PlayerStyle.Width(w).Render(row1),
		theme.PlayerStyle.Width(w).Render(row2),
	}
}

func (m Model) albumOf() string {
	if t, ok := m.currentTrack(); ok {
		return t.Album
	}
	return ""
}

// progressCell: elapsed ━··●─── total, inside the given cell budget.
func (m Model) progressCell(width int) string {
	s := m.data.Session()
	barW := width
	if s.Total > 0 {
		barW = width - visibleWidth(fmtDur(s.Elapsed)) - visibleWidth(fmtDur(s.Total)) - 2
	}
	if barW < 3 {
		barW = 3
	}
	done := 0
	if s.Total > 0 {
		done = int(float64(s.Elapsed) / float64(s.Total) * float64(barW))
	}
	if done > barW {
		done = barW
	}
	// the knob occupies one cell inside the track (DESIGN: ━ + ●)
	if done > barW-1 {
		done = barW - 1
	}
	bar := theme.AccentStyle.Render(strings.Repeat(theme.GlyphProgress, done)) +
		theme.TitleStyle.Render(theme.GlyphKnob) +
		theme.RuleStyle.Render(strings.Repeat(theme.GlyphRule, barW-done-1))
	return theme.NumberStyle.Render(fmtDur(s.Elapsed)) + " " + bar + " " + theme.NumberStyle.Render(fmtDur(s.Total))
}

func (m Model) volumeCell(vol int) string {
	if vol < 0 {
		vol = 0
	}
	if vol > 100 {
		vol = 100
	}
	fill := vol / 10
	bar := theme.LiveStyle.Render(strings.Repeat(theme.GlyphVolFill, fill)) +
		theme.LabelStyle.Render(strings.Repeat(theme.GlyphVolEmpty, 10-fill))
	return theme.LabelStyle.Render("vol") + " " + bar + " " + theme.NumberStyle.Render(fmt.Sprintf("%d%%", vol))
}

// statusBar: contextual keys left (artboard order), view state + clock right
// (Keys-Once Rule). A toast replaces the keys line; the search window keeps
// the live filter indicator here.
func (m Model) statusBar(w int) string {
	right := theme.ValueStyle.Render(m.viewState()) + "   " + theme.NumberStyle.Render(m.clock().Format("15:04"))
	rw := visibleWidth(right)
	var left string
	switch {
	case m.win == winSearch:
		hint := "enter results · esc close"
		if m.fetch == nil {
			hint = "enter apply · esc cancel"
		}
		left = theme.AccentStyle.Render("/ "+truncate(m.filter, w/2)) + "   " +
			theme.LabelStyle.Render(hint)
	case m.toast.text != "":
		left = theme.ValueStyle.Render(truncate(m.toast.text, w))
	default:
		left = m.keyHints(w - rw - 2)
	}
	if lw := visibleWidth(left); lw+rw+1 > w {
		left = theme.ValueStyle.Render(truncate(stripANSI(left), w-rw-1))
	}
	return padRight(left, w-rw) + right
}

func (m Model) keyHints(maxWidth int) string {
	var parts []string
	total := 0
	for _, h := range keyHintsTable {
		part := theme.ValueStyle.Render(h[0]) + " " + theme.LabelStyle.Render(h[1])
		if total+visibleWidth(part)+3*len(parts) > maxWidth && len(parts) > 0 {
			break
		}
		parts = append(parts, part)
		total += visibleWidth(part)
	}
	return strings.Join(parts, "   ")
}

func (m Model) viewState() string {
	if m.sView {
		return "search / " + strings.ToLower(truncate(m.sQuery, 24))
	}
	switch m.section {
	case SectionPlaylists:
		return "playlists"
	default:
		if m.fetch != nil {
			if m.plName != "" {
				return "tracks / " + strings.ToLower(truncate(m.plName, 32))
			}
			return "tracks"
		}
		return "tracks / " + strings.ToLower(m.data.Playlist().Name)
	}
}

func fmtDur(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

func fmtTotal(d time.Duration) string {
	mins := int(d.Minutes())
	if h := mins / 60; h > 0 {
		return fmt.Sprintf("%dh %dm", h, mins%60)
	}
	return fmt.Sprintf("%dm", mins)
}

func totalDuration(tracks []library.Track) time.Duration {
	var total time.Duration
	for _, t := range tracks {
		total += t.Duration
	}
	return total
}

// loginCallbackURL is where the interactive-login callback lands (spec S12:
// loopback redirect with an IP literal, never localhost).
func loginCallbackURL(port int) string { return fmt.Sprintf("http://127.0.0.1:%d/login", port) }

// connectingView covers the gap between the authorization callback and a live
// session. It was silent before: the login screen stayed up while the session
// negotiated, which reads as a frozen app. The log path is on screen because the
// handshake is the one place where the only evidence lives in a file.
func (m Model) connectingView() string {
	return m.messageView([]string{
		"",
		theme.TitleStyle.Render("starting the session"),
		"",
		theme.ValueStyle.Render("the authorization was accepted — handing the token to the player"),
		"",
		theme.MetaStyle.Render("if this stays here, the log is " + config.StateDir() + "/player.log"),
		"",
		theme.MetaStyle.Render("build " + Revision()),
		theme.LabelStyle.Render("ctrl+c quit"),
	})
}
