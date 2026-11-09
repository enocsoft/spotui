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
	playerErr error                // non-nil: full-screen player error state

	w, h         int
	section      Section
	sectionShift int // H/L shift the section row sideways
	cursor       int
	listOffset   int
	drawerOpen   bool
	drillDown    bool // inside the mock playlist (library drill-down)
	filtering    bool
	filter       string
	expanded     map[string]bool
	pickerOpen   bool // audio output picker (`d`)
	pickerCursor int
	toast        toast
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
			m.loginURL = msg.LoginURL
		case library.EventReady:
			m.loginURL, m.playerErr = "", nil
		case library.EventFailed:
			m.playerErr = msg.Err
		}
		return m, m.rearmEvents()

	case tea.KeyMsg:
		return m.handleKey(msg)
	}
	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	m.toast = toast{} // any key clears the toast

	if m.pickerOpen {
		switch msg.String() {
		case "esc", "d":
			m.pickerOpen = false
		case "up", "k":
			if m.pickerCursor > 0 {
				m.pickerCursor--
			}
		case "down", "j":
			if m.pickerCursor < len(m.data.Sinks())-1 {
				m.pickerCursor++
			}
		case "enter":
			return m.onPickSinkCmd()
		}
		return m, nil
	}

	if m.filtering {
		switch msg.String() {
		case "esc":
			m.filtering = false
			m.filter = ""
			m.clampCursor()
			return m, nil
		case "enter":
			m.filtering = false
			m.clampCursor()
			return m, nil
		case "backspace":
			if r := []rune(m.filter); len(r) > 0 {
				m.filter = string(r[:len(r)-1])
			}
			m.cursor = 0
			return m, nil
		}
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			m.filter += msg.String()
			m.cursor = 0
		}
		return m, nil
	}

	switch msg.String() {
	case "ctrl+c":
		return m, tea.Quit
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
		m.filtering = true
		m.filter = ""
	case "d":
		m.pickerOpen = true
		m.pickerCursor = 0
	case ":":
		m.setToast(": command palette · slice 2")
	case "?":
		m.setToast("? all keys · slice 2")
	}
	return m, nil
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
