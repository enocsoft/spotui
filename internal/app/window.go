// Floating windows (S19, T12): search (`/`), the command palette (`:`), the
// audio output picker (`d`) and help (`?`) share one component — a closed
// ┌─┐ │ └─┘ edge in the border role on the surface role, centred over the
// list, one at a time, closed with `esc`, sized to its content (never to the
// screen) and scrolling inside when rows overflow. The section row and the
// context strip never change and the list behind does not move.
package app

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"

	"github.com/nahuel/spotify-tui/internal/ui/theme"
)

// windowKind is which floating window is open. Zero value = none (S19: one
// window at a time).
type windowKind uint8

const (
	winNone windowKind = iota
	winSearch
	winPalette
	winPicker
	winHelp
)

// Geometry budget from the delegated task: the window is clamped to
// min(104, w-4) columns (104 = widths.palette in DESIGN.md) and
// min(14, h-6) rows.
const (
	winMaxWidth  = 104
	winMaxHeight = 14
	winMinWidth  = 24
	winRowCap    = 60 // result rows wider than this do not widen the window
)

// Surface-backdrop styles: the theme roles are foreground-only, so the
// floating window composes them over the surface role here. Tokens come only
// from internal/ui/theme — no hex literals (spec S11).
var (
	winEdgeStyle   = lipgloss.NewStyle().Foreground(theme.Border).Background(theme.Surface)
	winRowStyle    = lipgloss.NewStyle().Foreground(theme.Text2).Background(theme.Surface)
	winLabelStyle  = lipgloss.NewStyle().Foreground(theme.Muted).Background(theme.Surface)
	winAccentStyle = lipgloss.NewStyle().Foreground(theme.Accent).Background(theme.Surface)
	winCursorStyle = lipgloss.NewStyle().Foreground(theme.Surface).Background(theme.Accent)
)

// winRow is a scrollable row of window content (plain text; styled at render
// time). The search window has none since T19 — it is input only.
type winRow struct {
	text  string
	sel   bool // cursor row: bold primary on surface (current role)
	label bool
}

// winSpec is the content of one window: header title + meta, an optional
// input line, scrollable rows, and a footer with its right segment.
type winSpec struct {
	title    string
	meta     string
	hasInput bool // search/palette always show their input line
	input    string
	rows     []winRow
	foot     string
	footR    string
}

// keyHintsTable is the single source of key facts (Keys-Once Rule): the
// status bar and the `?` help window render the same table.
var keyHintsTable = [][2]string{
	{"j/k", "move"}, {"enter", "play"}, {"a", "queue"}, {"space", "pause"},
	{"n/p", "track"}, {"+/-", "volume"}, {"tab", "view"}, {"q", "drawer"},
	{"esc", "back"}, {"/", "search"}, {":", "cmd"}, {"d", "output"},
}

// helpExtraKeys are the keys the status bar has no room for.
var helpExtraKeys = [][2]string{
	{"g/G", "top/bottom"}, {"ctrl+d/u", "half page"}, {"H/L", "shift"},
	{"l", "save"}, {"r", "retry"}, {"ctrl+c", "quit"},
}

func (m Model) windowSpec() winSpec {
	switch m.win {
	case winSearch:
		return m.searchSpec()
	case winPalette:
		return m.paletteSpec()
	case winPicker:
		return m.pickerSpec()
	case winHelp:
		return m.helpSpec()
	}
	return winSpec{}
}

// searchSpec: the search window is input only (T19) — header, the prompt
// row with the typed query, and one hint line (the state or the result
// count). No result rows ever render inside the window; the results appear
// in the list behind, type-grouped by the list itself.
func (m Model) searchSpec() winSpec {
	s := winSpec{
		title:    "search",
		meta:     "esc close",
		hasInput: true,
		input:    m.filter,
	}
	if m.fetch == nil {
		// Mock mode: the window filters the open list client-side.
		s.foot = "enter apply · esc cancel"
		s.footR = fmt.Sprintf("%d matches", len(m.visibleRows()))
		return s
	}
	s.foot = "type ≥2 chars · enter results · esc close"
	switch {
	case m.sQuery == "" && m.filter == "":
		s.footR = ""
	case m.sLoading:
		s.footR = "searching…"
	case m.sErr != nil:
		if m.sRateLimited {
			s.footR = "rate limited · r retry"
		} else {
			s.footR = "search failed · r retry"
		}
	default:
		s.footR = fmt.Sprintf("%d results", m.searchLoaded())
	}
	return s
}

// paletteSpec: command mode. The empty query shows the command the palette
// supports today (`:play`, T13); unknown commands toast like before.
func (m Model) paletteSpec() winSpec {
	s := winSpec{
		title:    "command",
		meta:     "esc close",
		hasInput: true,
		input:    m.cmdInput,
		foot:     "enter run · esc close",
	}
	if m.cmdInput == "" {
		s.rows = []winRow{{text: ":play <spotify:track:id> · play a track now"}}
	}
	return s
}

// pickerSpec: the local audio outputs through the existing adapter (S16) —
// ● active, ○ idle, blocked reason inline.
func (m Model) pickerSpec() winSpec {
	s := winSpec{title: "AUDIO OUTPUT", foot: "enter select · esc close"}
	sinks := m.data.Sinks()
	if len(sinks) == 0 {
		s.rows = []winRow{{text: theme.GlyphIdle + " no audio output"}}
		return s
	}
	for i, sk := range sinks {
		glyph, state := theme.GlyphIdle, ""
		if sk.Active {
			glyph, state = theme.GlyphActive, " · active"
		} else if sk.Blocked != "" {
			state = " · " + sk.Blocked
		}
		s.rows = append(s.rows, winRow{text: glyph + " " + sk.Name + state, sel: i == m.winCursor})
	}
	return s
}

// helpSpec: the keymap (Keys-Once Rule — same table the status bar renders).
func (m Model) helpSpec() winSpec {
	s := winSpec{title: "help", meta: "esc close", foot: "j/k scroll"}
	for _, table := range [][][2]string{keyHintsTable, helpExtraKeys} {
		for _, h := range table {
			s.rows = append(s.rows, winRow{text: h[0] + " " + h[1], sel: len(s.rows) == m.winCursor})
		}
	}
	return s
}

// windowMaxSize is the clamp min(104, w-4) x min(14, h-6), further bounded
// by the list region so the window never leaves the list.
func (m Model) windowMaxSize() (int, int) {
	maxW := winMaxWidth
	if cap := m.w - 4; cap < maxW {
		maxW = cap
	}
	maxH := winMaxHeight
	if cap := m.h - 6; cap < maxH {
		maxH = cap
	}
	if lh := m.listHeight(); lh < maxH {
		maxH = lh
	}
	if maxW < winMinWidth {
		maxW = winMinWidth
	}
	if maxH < 3 {
		maxH = 3
	}
	return maxW, maxH
}

// windowRowCount is how many scrollable rows the open window has. The
// search window is input only (T19): no scrollable rows, no window cursor.
func (m Model) windowRowCount() int {
	switch m.win {
	case winHelp:
		return len(keyHintsTable) + len(helpExtraKeys)
	case winPicker:
		return len(m.data.Sinks())
	}
	return 0
}

// windowRowsArea is how many window rows fit under the current geometry.
func (m Model) windowRowsArea() int {
	spec := m.windowSpec()
	_, maxH := m.windowMaxSize()
	fixed := 6 // top, header, sep, footer sep, footer, bottom edge lines
	if spec.hasInput {
		fixed += 2 // input line + separator
	}
	if area := maxH - fixed; area > 0 {
		return area
	}
	return 0
}

// windowRender builds the window: winW columns, winH lines including the
// closed edge, every line exactly winW cells wide.
func (m Model) windowRender() (winW, winH int, lines []string) {
	spec := m.windowSpec()
	maxW, maxH := m.windowMaxSize()

	// width: content-driven, clamped — sizes to its content, never to the
	// screen. Result rows wider than winRowCap do not widen the window.
	contentW := winMinWidth - 4
	if w := utf8.RuneCountInString(spec.title) + 2 + utf8.RuneCountInString(spec.meta); w > contentW {
		contentW = w
	}
	if spec.input != "" {
		if w := utf8.RuneCountInString(spec.input) + 4; w > contentW {
			contentW = w
		}
	}
	if w := utf8.RuneCountInString(spec.foot) + 2 + utf8.RuneCountInString(spec.footR); w > contentW {
		contentW = w
	}
	longest := 0
	for _, r := range spec.rows {
		if w := utf8.RuneCountInString(r.text); w > longest {
			longest = w
		}
	}
	if w := min(longest, winRowCap); w > contentW {
		contentW = w
	}
	winW = contentW + 4 // edges + 1-cell inset each side
	if winW > maxW {
		winW = maxW
	}
	innerW := winW - 4

	// height: content-driven, clamped; rows scroll inside when they overflow
	fixed := 6 // top, header, sep, footer sep, footer, bottom edge lines
	if spec.hasInput {
		fixed += 2 // input line + separator
	}
	winH = fixed + len(spec.rows)
	if winH > maxH {
		winH = maxH
	}
	rowsArea := winH - fixed
	off := m.winOffset
	if off > len(spec.rows)-rowsArea {
		off = max(0, len(spec.rows)-rowsArea)
	}
	if off < 0 {
		off = 0
	}

	lines = append(lines, winEdge("┌", "┐", winW))
	lines = append(lines, winHeader(spec, innerW, winW))
	lines = append(lines, winSep(winW))
	if spec.hasInput {
		lines = append(lines, winInput(spec.input, innerW, winW))
		lines = append(lines, winSep(winW))
	}
	for i := off; i < off+rowsArea && i < len(spec.rows); i++ {
		lines = append(lines, winRowLine(spec.rows[i], innerW))
	}
	for len(lines) < winH-3 {
		lines = append(lines, winBlank(winW))
	}
	lines = append(lines, winSep(winW))
	lines = append(lines, winFooter(spec, innerW))
	lines = append(lines, winEdge("└", "┘", winW))
	return winW, winH, lines
}

func winEdge(l, r string, winW int) string {
	return winEdgeStyle.Render(l + strings.Repeat(theme.GlyphRule, winW-2) + r)
}

func winSep(winW int) string {
	return winEdgeStyle.Render("│" + strings.Repeat(theme.GlyphRule, winW-2) + "│")
}

func winBlank(winW int) string {
	return winEdgeStyle.Render("│" + strings.Repeat(" ", winW-2) + "│")
}

// winHeader: bold primary title left (title role), muted meta right; one
// cell of inset each side, like every content row.
func winHeader(spec winSpec, innerW, winW int) string {
	meta := truncate(spec.meta, innerW-1)
	titleBudget := innerW - utf8.RuneCountInString(meta) - 1
	if titleBudget < 1 {
		meta, titleBudget = "", innerW
	}
	title := truncate(spec.title, titleBudget)
	gap := innerW - utf8.RuneCountInString(title) - utf8.RuneCountInString(meta)
	if gap < 1 {
		gap = 1
	}
	return winEdgeStyle.Render("│") +
		theme.CurrentStyle.Render(" "+title+strings.Repeat(" ", gap)) +
		winLabelStyle.Render(meta+" ") +
		winEdgeStyle.Render("│")
}

// winInput: accent > prompt, input text, accent block cursor.
func winInput(text string, innerW, winW int) string {
	budget := innerW - 3 // "> " + cursor cell
	visible := truncate(text, budget)
	pad := budget - utf8.RuneCountInString(visible)
	return winEdgeStyle.Render("│") +
		winAccentStyle.Render(" > ") +
		winRowStyle.Render(visible+strings.Repeat(" ", pad)) +
		winCursorStyle.Render(" ") +
		winEdgeStyle.Render(" │")
}

func winRowLine(r winRow, innerW int) string {
	text := padRight(truncate(r.text, innerW), innerW)
	if r.sel {
		return winEdgeStyle.Render("│") + theme.CurrentStyle.Render(" "+text+" ") + winEdgeStyle.Render("│")
	}
	if r.label {
		// group header: the muted label role (Same-Role Rule)
		return winEdgeStyle.Render("│") + winLabelStyle.Render(" "+text+" ") + winEdgeStyle.Render("│")
	}
	return winEdgeStyle.Render("│") + winRowStyle.Render(" "+text+" ") + winEdgeStyle.Render("│")
}

// winFooter: key hints left, count right.
func winFooter(spec winSpec, innerW int) string {
	footR := truncate(spec.footR, innerW-1)
	left := truncate(spec.foot, innerW-utf8.RuneCountInString(footR)-1)
	return winEdgeStyle.Render("│") +
		winLabelStyle.Render(" "+padRight(left, innerW-utf8.RuneCountInString(footR))+footR+" ") +
		winEdgeStyle.Render("│")
}

// overlayWindow splices the window into the assembled frame, centred over
// the list region. The frame keeps exactly h lines; the covered rows keep
// their content outside the window.
func (m Model) overlayWindow(frame []string) {
	winW, winH, lines := m.windowRender()
	listTop, listH := 4, m.listHeight()
	top := listTop + (listH-winH)/2
	if top < listTop {
		top = listTop
	}
	if top+winH > listTop+listH {
		top = listTop + listH - winH
	}
	x := (m.w - winW) / 2
	if x < 2 {
		x = 2
	}
	for i, wl := range lines {
		out := spliceLine(frame[top+i], x, winW, wl)
		if d := m.w - visibleWidth(out); d > 0 {
			out += strings.Repeat(" ", d)
		}
		frame[top+i] = out
	}
}

// spliceLine replaces the visible columns [x, x+w) of a styled line with
// repl (exactly w cells wide), keeping the styled flanks. Cuts land between
// cells and resets are inserted at the seam so no half-cut style can leak
// into the replacement (Measured Glyph Rule: every rune is single-width).
func spliceLine(line string, x, w int, repl string) string {
	type tok struct {
		esc string // non-empty: escape sequence (zero width)
		r   rune   // visible rune when esc == ""
		col int    // visible column of the rune
	}
	toks := make([]tok, 0, utf8.RuneCountInString(line))
	col := 0
	for i := 0; i < len(line); {
		if line[i] == '\x1b' {
			if loc := ansiRe.FindStringIndex(line[i:]); loc != nil && loc[0] == 0 {
				toks = append(toks, tok{esc: line[i : i+loc[1]]})
				i += loc[1]
				continue
			}
		}
		r, sz := utf8.DecodeRuneInString(line[i:])
		toks = append(toks, tok{r: r, col: col})
		col++
		i += sz
	}
	total := col

	start := -1 // first visible token at col >= x
	for i, t := range toks {
		if t.esc == "" && t.col >= x {
			start = i
			break
		}
	}
	if start == -1 {
		// the line ends before the window starts: pad up to the seam
		return line + strings.Repeat(" ", x-total) + repl
	}
	// left flank: everything before the seam, minus the trailing escapes that
	// belong to the cut cell's own style (any leak is reset at the seam)
	endLeft := start
	for endLeft > 0 && toks[endLeft-1].esc != "" {
		endLeft--
	}
	var b strings.Builder
	for _, t := range toks[:endLeft] {
		if t.esc != "" {
			b.WriteString(t.esc)
		} else {
			b.WriteRune(t.r)
		}
	}
	// right flank: first visible token at col >= x+w, escapes included
	b.WriteString("\x1b[0m")
	b.WriteString(repl)
	b.WriteString("\x1b[0m")
	for i := start; i < len(toks); i++ {
		if toks[i].esc == "" && toks[i].col >= x+w {
			for _, t := range toks[i:] {
				if t.esc != "" {
					b.WriteString(t.esc)
				} else {
					b.WriteRune(t.r)
				}
			}
			break
		}
	}
	return b.String()
}
