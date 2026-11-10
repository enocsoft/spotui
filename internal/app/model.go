// Package app holds the bubbletea model: the layout shell (T3), the k9s-style
// keymap (T4) and the toast behaviour for not-yet-implemented actions.
package app

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nahuel/spotify-tui/internal/config"
	"github.com/nahuel/spotify-tui/internal/library"
)

// Section is one of the three top-row sections (spec S5).
type Section int

const (
	SectionLibrary Section = iota
	SectionSearch
	SectionQueue
)

func (s Section) String() string {
	switch s {
	case SectionSearch:
		return "Search"
	case SectionQueue:
		return "Queue"
	default:
		return "Library"
	}
}

func (s Section) lower() string {
	switch s {
	case SectionSearch:
		return "search"
	case SectionQueue:
		return "queue"
	default:
		return "library"
	}
}

var sections = []Section{SectionLibrary, SectionSearch, SectionQueue}

// toast is a one-line message shown in the status bar until the next key or
// the timeout fires. Unimplemented actions toast instead of failing silently.
type toast struct {
	text string
	seq  int
}

type toastTick struct{ seq int }

// playerEventMsg wraps a library.Event from the real player (EventLogin /
// EventReady / EventFailed / EventState) into the update loop.
type playerEventMsg struct{ library.Event }

// tickMsg asks for a re-render so live player state (progress) mirrors
// without any keypress (Mirror Rule).
type tickMsg struct{}

// Model is the whole TUI state. Slice 1 renders mock data only (S10).
type Model struct {
	cfg    config.Config
	data   library.DataSource
	player library.Player
	clock  func() time.Time

	eventCh   <-chan library.Event // real-player events; nil for the mock
	loginURL  string               // non-empty: full-screen login state (S9)
	loginFile string
	// connecting is true between the token and the session handshake: the login
	// screen must hand over to something that shows life, or a slow bootstrap looks
	// like a dead app.
	connecting bool
	playerErr  error // non-nil: full-screen player error state

	w, h         int
	section      Section
	sectionShift int // H/L shift the section row sideways
	cursor       int
	listOffset   int
	drawerOpen   bool
	drillDown    bool   // inside the mock playlist (library drill-down)
	filter       string // `/` client-side filter of the open list
	expanded     map[string]bool
	toast        toast

	// Floating window (S19): one at a time, opened by its key, closed with esc.
	win          windowKind
	winCursor    int    // cursor inside the window rows (picker, help)
	winOffset    int    // first visible window row when rows overflow
	cmdInput     string // palette command input (`:`)
	preWinCursor int    // list cursor saved while a window is open
	preWinOffset int    // list scroll offset saved while a window is open
}

// New builds the model. data and player are interfaces backed by mock data
// in slice 1 or by the embedded go-librespot player; clock is injectable for
// deterministic tests.
func New(cfg config.Config, data library.DataSource, player library.Player, clock func() time.Time) Model {
	if clock == nil {
		clock = time.Now
	}
	m := Model{
		cfg:       cfg,
		data:      data,
		player:    player,
		clock:     clock,
		section:   SectionLibrary,
		drillDown: true, // start inside the mock playlist, as in the artboard
		expanded:  map[string]bool{"PLAYLISTS": true},
	}
	if pe, ok := player.(library.PlayerEvents); ok {
		m.eventCh = pe.Events()
	}
	return m
}

// ActiveSection reports the focused section (used by tests and the status bar).
func (m *Model) ActiveSection() Section { return m.section }

// Init arms the real-player event relay and the one-second render tick.
func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{tickCmd()}
	if m.eventCh != nil {
		cmds = append(cmds, waitForEvents(m.eventCh))
	}
	return tea.Batch(cmds...)
}

// waitForEvents relays the next player event into the update loop; a nil
// message (channel closed) re-arms nothing.
func waitForEvents(ch <-chan library.Event) tea.Cmd {
	return func() tea.Msg {
		ev, ok := <-ch
		if !ok {
			return nil
		}
		return playerEventMsg{ev}
	}
}

func (m Model) rearmEvents() tea.Cmd {
	if m.eventCh == nil {
		return nil
	}
	return waitForEvents(m.eventCh)
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg { return tickMsg{} })
}

// Update implements tea.Model.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.clampCursor()
		return m, nil

	case toastTick:
		if m.toast.seq == msg.seq {
			m.toast = toast{}
		}
		return m, nil

	case tickMsg:
		return m, tickCmd()

	case playerEventMsg:
		switch msg.Type {
		case library.EventLogin:
			m.loginURL, m.loginFile, m.connecting = msg.LoginURL, msg.LoginFile, false
		case library.EventConnecting:
			m.connecting = true
		case library.EventReady:
			m.loginURL, m.loginFile, m.playerErr, m.connecting = "", "", nil, false
		case library.EventFailed:
			m.playerErr, m.connecting = msg.Err, false
		}
		return m, m.rearmEvents()

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.toast = toast{} // any key clears the toast

	if msg.Type == tea.KeyCtrlC {
		return m, tea.Quit
	}
	if m.win != winNone {
		return m.handleWindowKey(msg)
	}

	switch msg.String() {
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
	case "g", "home":
		m.cursor = 0
		m.syncScroll()
	case "G", "end":
		m.cursor = max(0, len(m.visibleRows())-1)
		m.syncScroll()
	case "ctrl+d":
		m.moveCursor(m.listHeight() / 2)
	case "ctrl+u":
		m.moveCursor(-m.listHeight() / 2)
	case "enter":
		return m, m.onEnter()
	case "a":
		return m, m.onEnqueue()
	case " ":
		return m, m.onPause()
	case "n":
		return m, m.onNext()
	case "p":
		return m, m.onPrev()
	case "+", "=":
		return m, m.onVolume(1)
	case "-":
		return m, m.onVolume(-1)
	case "tab":
		m.section = sections[(int(m.section)+1)%len(sections)]
		m.cursor = 0
		m.listOffset = 0
	case "q":
		m.drawerOpen = !m.drawerOpen
	case "l":
		return m, m.onToggle()
	case "esc":
		return m, m.onEsc()
	case "H":
		return m, m.shiftSections(-1)
	case "L":
		return m, m.shiftSections(1)
	case "/":
		m.openWindow(winSearch)
	case "d":
		m.openWindow(winPicker)
	case ":":
		m.openWindow(winPalette)
	case "?":
		m.openWindow(winHelp)
	}
	return m, nil
}

// handleWindowKey routes keys into the open floating window (S19): esc
// closes it and restores the list; the search input keeps filtering
// client-side; the palette edits its command; picker and help move the
// window cursor.
func (m Model) handleWindowKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.win {
	case winPicker:
		switch msg.String() {
		case "esc", "d":
			m.closeWindow(true)
		case "up", "k":
			m.moveWinCursor(-1)
		case "down", "j":
			m.moveWinCursor(1)
		case "enter":
			return m.onPickSinkCmd()
		}
		return m, nil

	case winHelp:
		switch msg.String() {
		case "esc", "?":
			m.closeWindow(true)
		case "up", "k":
			m.moveWinCursor(-1)
		case "down", "j":
			m.moveWinCursor(1)
		}
		return m, nil

	case winSearch:
		switch msg.String() {
		case "esc":
			m.closeWindow(true) // cancel: restore list and cursor
		case "enter":
			m.closeWindow(false) // apply: keep the filter
			m.clampCursor()
			m.syncScroll()
		case "backspace":
			if r := []rune(m.filter); len(r) > 0 {
				m.filter = string(r[:len(r)-1])
			}
			m.cursor = 0
			m.winOffset = 0
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				m.filter += msg.String()
				m.cursor = 0
				m.winOffset = 0
			}
		}
		return m, nil

	case winPalette:
		switch msg.String() {
		case "esc":
			m.closeWindow(true)
		case "enter":
			cmd := m.execCommand()
			m.closeWindow(false)
			return m, cmd
		case "backspace":
			if r := []rune(m.cmdInput); len(r) > 0 {
				m.cmdInput = string(r[:len(r)-1])
			}
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				m.cmdInput += msg.String()
			}
		}
		return m, nil
	}
	return m, nil
}

// openWindow opens the floating window kind, remembering the list state so
// esc can restore it (S19: the list behind stays put).
func (m *Model) openWindow(k windowKind) {
	if m.win == k {
		m.closeWindow(true)
		return
	}
	m.preWinCursor, m.preWinOffset = m.cursor, m.listOffset
	m.win = k
	m.winCursor, m.winOffset = 0, 0
	switch k {
	case winSearch:
		m.filter = ""
		// keep the cursor row visible inside the window results
		if area := m.windowRowsArea(); area > 0 && m.cursor >= area {
			m.winOffset = m.cursor - area + 1
		}
	case winPalette:
		m.cmdInput = ""
	}
}

// closeWindow closes the floating window. restore puts the saved list cursor
// and scroll offset back (esc); otherwise today's state stays (enter).
func (m *Model) closeWindow(restore bool) {
	k := m.win
	m.win = winNone
	m.winCursor, m.winOffset = 0, 0
	if restore {
		m.cursor, m.listOffset = m.preWinCursor, m.preWinOffset
	}
	if k == winSearch && restore {
		m.filter = ""
	}
	if k == winPalette {
		m.cmdInput = ""
	}
	m.clampCursor()
}

// moveWinCursor moves the window cursor (picker, help) and scrolls the
// window content when it overflows.
func (m *Model) moveWinCursor(delta int) {
	n := m.windowRowCount()
	m.winCursor += delta
	if m.winCursor < 0 {
		m.winCursor = 0
	}
	if m.winCursor >= n {
		m.winCursor = n - 1
	}
	if m.winCursor < 0 {
		m.winCursor = 0
	}
	m.syncWinScroll()
}

func (m *Model) syncWinScroll() {
	area := m.windowRowsArea()
	if area <= 0 {
		m.winOffset = 0
		return
	}
	if m.winCursor < m.winOffset {
		m.winOffset = m.winCursor
	}
	if m.winCursor >= m.winOffset+area {
		m.winOffset = m.winCursor - area + 1
	}
}

func (m *Model) setToast(text string) tea.Cmd {
	m.toast = toast{text: text, seq: m.toast.seq + 1}
	seq := m.toast.seq
	return tea.Tick(3*time.Second, func(time.Time) tea.Msg { return toastTick{seq: seq} })
}

func (m *Model) moveCursor(delta int) {
	m.cursor += delta
	m.clampCursor()
	m.syncScroll()
}

func (m *Model) clampCursor() {
	if m.cursor < 0 {
		m.cursor = 0
	}
	if max := len(m.visibleRows()) - 1; m.cursor > max {
		m.cursor = max
	}
	if m.cursor < 0 {
		m.cursor = 0
	}
}

func (m *Model) syncScroll() {
	h := m.listHeight()
	if h <= 0 {
		m.listOffset = 0
		return
	}
	if m.cursor < m.listOffset {
		m.listOffset = m.cursor
	}
	if m.cursor >= m.listOffset+h {
		m.listOffset = m.cursor - h + 1
	}
}
