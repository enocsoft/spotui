// Package theme holds every visual token of spotify-tui.
//
// Source of truth: design/DESIGN.md (frontmatter colors, roles and glyphs).
// No view may write a hex literal or an ad-hoc style; everything goes through
// these roles (spec S11).
package theme

import "github.com/charmbracelet/lipgloss"

// Color roles from design/DESIGN.md. The default theme values are the ones in
// the DESIGN.md frontmatter; users re-point roles at their own palette.
var (
	Canvas  = lipgloss.Color("#101215") // list body, palette body
	Rail    = lipgloss.Color("#16191d") // queue drawer band, playing row tint
	Surface = lipgloss.Color("#1c2026") // selected row, player bar, floating panels
	Border  = lipgloss.Color("#262b33") // pane rules, separators
	Text    = lipgloss.Color("#e6e9ee") // primary text: titles, current row
	Text2   = lipgloss.Color("#a8b0bd") // secondary text: values, unselected titles
	Muted   = lipgloss.Color("#7d8798") // labels, numbers, durations, hints
	Accent  = lipgloss.Color("#69b7e8") // selection, focus, progress done
	Live    = lipgloss.Color("#7fd18c") // the only green: playing, active device

	// state.warn / state.error / state.info have no default value in
	// DESIGN.md yet; they are reserved roles and stay on muted until the
	// design file assigns them. Never use them decoratively.
	Warn  = Muted
	Error = Muted
	Info  = Muted
)

// Glyphs from design/DESIGN.md §6. All single-width, one meaning each.
const (
	GlyphPlaying    = "▶"   // now playing (replaces the row number)
	GlyphPaused     = "‖"   // paused (DESIGN §7 player states)
	GlyphActive     = "●"   // active device, live session, brand dot
	GlyphIdle       = "○"   // idle device, empty state
	GlyphSelected   = "▸"   // selected rail node, collapsed section
	GlyphCollapsed  = "▸"   // collapsed section
	GlyphExpanded   = "▾"   // expanded section
	GlyphSaved      = "[x]" // saved to your library
	GlyphNotSaved   = "[ ]" // not saved
	GlyphNotApplic  = "·"   // not applicable; meta divider
	GlyphTruncation = "…"   // truncation; lists truncate, never wrap
	GlyphDivider    = "│"   // segment divider in bars and strips
	GlyphRule       = "─"   // rule, progress remaining
	GlyphProgress   = "-"   // progress track and elapsed, dashed

	GlyphSortDesc = "↓" // sort indicator (artboard copy)
	GlyphVolFill  = "█" // volume fill (artboard)
	GlyphVolEmpty = "░" // volume rest (artboard)
	GlyphCrumb    = "/" // crumb divider
)

// Type roles from design/DESIGN.md §3, as lipgloss styles.
var (
	// Title: bold + text.primary. Track titles, album names, now-playing title.
	TitleStyle = lipgloss.NewStyle().Bold(true).Foreground(Text)
	// Current: bold + primary on surface. The focused row.
	CurrentStyle = lipgloss.NewStyle().Bold(true).Foreground(Text).Background(Surface)
	// Value: normal + secondary. Table cells, artist/album names.
	ValueStyle = lipgloss.NewStyle().Foreground(Text2)
	// Number: normal + muted, right aligned (alignment done by the caller).
	NumberStyle = lipgloss.NewStyle().Foreground(Muted)
	// Label: muted. Column headers, section headers, status-bar keys.
	LabelStyle = lipgloss.NewStyle().Foreground(Muted)
	// Meta: muted, joined by ·.
	MetaStyle = lipgloss.NewStyle().Foreground(Muted)
	// Live: live glyph + primary.
	LiveStyle = lipgloss.NewStyle().Foreground(Live)
	// Accent: selection, focus, progress done.
	AccentStyle = lipgloss.NewStyle().Foreground(Accent)
	// Rule: border-colored rules and separators.
	RuleStyle = lipgloss.NewStyle().Foreground(Border)
	// Selected row: surface tint, used with Width(w) to fill the band.
	SelectedStyle = lipgloss.NewStyle().Bold(true).Foreground(Text).Background(Surface)
	// Playing row tint: rail band.
	PlayingStyle = lipgloss.NewStyle().Foreground(Text).Background(Rail)
	// Drawer band: rail background.
	DrawerStyle = lipgloss.NewStyle().Foreground(Text2).Background(Rail)
	// Player bar: surface background, full width.
	PlayerStyle = lipgloss.NewStyle().Background(Surface)
)
