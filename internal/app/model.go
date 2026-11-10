// Package app holds the bubbletea model: the layout shell (T3), the k9s-style
// keymap (T4) and the toast behaviour for not-yet-implemented actions.
package app

import (
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nahuel/spotify-tui/internal/config"
	"github.com/nahuel/spotify-tui/internal/library"
)

// Section is one of the two top-row sections (spec S18, T20): Tracks (the
// open list: playlist, album or search result) and Playlists (navigation).
// Search and Queue are not sections: the queue is the drawer (S4) and the
// search results are the transient sView state over whichever section is
// active — they never get a cell.
type Section int

const (
	SectionTracks Section = iota
	SectionPlaylists
)

func (s Section) String() string {
	if s == SectionPlaylists {
		return "Playlists"
	}
	return "Tracks"
}

var sections = []Section{SectionTracks, SectionPlaylists}

// toast is a one-line message shown in the status bar until the next key or
// the timeout fires. Unimplemented actions toast instead of failing silently.
type toast struct {
	text string
	seq  int
}

type toastTick struct{ seq int }

// searchDebounceTick fires ~250 ms after the last keystroke in the search
// window (T19): typing drives the search, and every keystroke re-arms the
// timer, so a tick whose seq is not the newest is stale and dropped.
type searchDebounceTick struct{ seq int }

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
	// loginPurpose is which login is asking (library.LoginPurpose*): two
	// tokens mean two consents on first run, and the state names its own.
	loginPurpose string
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
	filter       string // `/` client-side filter of the open list (mock mode)
	expanded     map[string]bool
	toast        toast

	// Floating window (S19): one at a time, opened by its key, closed with esc.
	win          windowKind
	winCursor    int    // cursor inside the window rows (picker, help)
	winOffset    int    // first visible window row when rows overflow
	cmdInput     string // palette command input (`:`)
	preWinCursor int    // list cursor saved while a window is open
	preWinOffset int    // list scroll offset saved while a window is open

	// Real catalog (T7). fetch is nil in mock mode (SPOTIFY_TUI_MOCK=1): every
	// block below is then unused and the mock data source drives everything.
	// All catalog calls run inside tea.Cmd goroutines (library.Fetch blocks);
	// the *Msg cases apply the answers here.
	fetch library.Fetch

	// Playlists panel (library root): one lazily fetched page after another.
	plsLoaded  bool // a page applied at least once — no auto-refetch after it
	plsLoading bool
	plsErr     error
	plsPending int // offset of the in-flight page, -1 when none
	pls        []library.PlaylistSummary
	plsTotal   int
	plsNext    int // offset of the next page, -1 when exhausted

	// Focused playlist contents (drill-down), resolved through the playback
	// resolver (library/context.go). plSkipped/plTruncated carry what the
	// bounded resolver could not show, footnoted under the list — never a
	// silent drop and never a playlist pretended complete.
	plID, plName, plOwner string
	plPrivate, plCollab   bool
	plTracks              []library.Track
	plTotal               int
	plNext                int
	plSkipped             int  // entries with no playable uri, counted
	plTruncated           bool // the resolver returned a subset
	plItemsLoading        bool
	plItemsErr            error
	iPending              int

	// Search (T19): the floating window (`/`) is input only — prompt and one
	// hint line, never result rows. Typing arms the 250 ms debounce; when the
	// tick fires the query runs and sView turns on: the results replace the
	// main list (tracks, then albums, then playlists) while the section row
	// stays put. enter closes the window on the results; esc backs out to the
	// previous section view. Stale answers are dropped by query.
	sQuery       string
	sPending     int
	sNext        int
	sLoading     bool
	sRateLimited bool
	sErr         error
	sTracks      []library.Track
	sAlbums      []library.AlbumSummary
	sPlaylists   []library.PlaylistSummary
	sTotals      library.SearchTotals
	sView        bool
	sDebounce    int // debounce epoch: each keystroke re-arms the timer

	// pendingSaved holds saved toggles in flight: the cell answers only when
	// the server does (Mirror Rule); on failure nothing to revert, just toast.
	pendingSaved map[string]bool

	// Shuffle (T21): `s` toggles shuffle over the open view's playable rows.
	// Our order owns playback: while it is on, the list and the drawer render
	// in the shuffled order and n/p walk it. shuffleOrd maps visible position
	// -> base index and is rebuilt on every view change; perm is the
	// injectable permutation generator (seeded, so tests are deterministic).
	shuffleOn  bool
	shuffleOrd []int
	perm       func(n int, seed int64) []int
}

// New builds the model. data and player are interfaces backed by mock data
// in slice 1 or by the embedded go-librespot player; clock is injectable for
// deterministic tests.
func New(cfg config.Config, data library.DataSource, player library.Player, clock func() time.Time) Model {
	if clock == nil {
		clock = time.Now
	}
	m := Model{
		cfg:          cfg,
		data:         data,
		player:       player,
		clock:        clock,
		expanded:     map[string]bool{"PLAYLISTS": true},
		pendingSaved: map[string]bool{},
		perm:         seededPerm,
	}
	// Real mode: the player exposes the Web API catalog (T7) and the app starts
	// on the playlists navigation. Mock mode keeps the artboard's open list.
	if cp, ok := player.(library.CatalogProvider); ok {
		m.fetch = cp.Catalog()
		m.section = SectionPlaylists
	} else {
		m.section = SectionTracks
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

// searchDebounceCmd arms the ~250 ms typing debounce (T19): the tick fires
// only while the window is still open, and its seq lets a newer keystroke
// drop it.
func searchDebounceCmd(seq int) tea.Cmd {
	return tea.Tick(searchDebounce, func(time.Time) tea.Msg { return searchDebounceTick{seq: seq} })
}

// searchDebounce is how long typing must stay quiet before the query runs.
const searchDebounce = 250 * time.Millisecond

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
			m.loginPurpose = msg.LoginPurpose
		case library.EventConnecting:
			m.connecting = true
		case library.EventReady:
			m.loginURL, m.loginFile, m.playerErr, m.connecting = "", "", nil, false
			// The session is live: fire the first (and only the first) playlists
			// page. Paging follows `next` lazily from here on.
			return m, tea.Batch(m.rearmEvents(), m.ensurePlaylistsLoaded())
		case library.EventFailed:
			// Clearing the URL matters: the login screen outranks the error view,
			// so a failure after the URL was shown used to hide itself.
			m.playerErr, m.connecting, m.loginURL, m.loginFile = msg.Err, false, "", ""
		}
		return m, m.rearmEvents()

	case playlistsMsg:
		m.applyPlaylists(msg)
		return m, nil

	case itemsMsg:
		m.applyItems(msg)
		return m, nil

	case searchMsg:
		m.applySearch(msg)
		return m, nil

	case searchDebounceTick:
		return m, m.onDebounceTick(msg.seq)

	case savedMsg:
		return m, m.applySaved(msg)

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

	var cmd tea.Cmd
	switch msg.String() {
	case "up", "k":
		m.moveCursor(-1)
	case "down", "j":
		m.moveCursor(1)
		cmd = m.ensureMore()
	case "g", "home":
		m.cursor = 0
		m.syncScroll()
	case "G", "end":
		m.cursor = max(0, len(m.visibleRows())-1)
		m.syncScroll()
		cmd = m.ensureMore()
	case "ctrl+d":
		m.moveCursor(m.listHeight() / 2)
		cmd = m.ensureMore()
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
	case "s":
		return m, m.onShuffle()
	case "+", "=":
		return m, m.onVolume(1)
	case "-":
		return m, m.onVolume(-1)
	case "tab":
		m.section = sections[(int(m.section)+1)%len(sections)]
		m.cursor = 0
		m.listOffset = 0
		m.rebuildShuffle()
		if m.section == SectionPlaylists {
			cmd = m.ensurePlaylistsLoaded()
		}
	case "q":
		m.drawerOpen = !m.drawerOpen
	case "l":
		return m, m.onToggle()
	case "r":
		return m, m.onRetry()
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
	return m, cmd
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
		if m.fetch != nil {
			return m.handleRealSearchKey(msg)
		}
		// Mock mode: the input-only window filters the open list live; enter
		// applies, esc cancels and restores (S19 unchanged).
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
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				m.filter += msg.String()
				m.cursor = 0
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
	if k == winSearch {
		// The filter narrows the open list (mock mode): a narrowed list is a
		// different view for shuffle.
		m.rebuildShuffle()
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

// handleRealSearchKey is the search window in real mode: the input edits the
// query and every keystroke re-arms the ~250 ms debounce that runs it (T19).
// enter closes the window and leaves the cursor on the results (a typed but
// unfired query flushes now); esc closes it and returns to the previous
// section view.
func (m Model) handleRealSearchKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "esc":
		m.closeWindow(true)
		m.sView = false
		return m, nil
	case "enter":
		q := strings.TrimSpace(m.filter)
		var cmd tea.Cmd
		if len([]rune(q)) >= 2 && q != m.sQuery {
			m.sDebounce++ // the pending tick is stale from now on
			cmd = m.startSearch(q)
		}
		m.closeWindow(false)
		m.clampCursor()
		m.syncScroll()
		return m, cmd
	case "backspace":
		if r := []rune(m.filter); len(r) > 0 {
			m.filter = string(r[:len(r)-1])
			m.sDebounce++
			return m, searchDebounceCmd(m.sDebounce)
		}
	default:
		if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
			m.filter += msg.String()
			m.sDebounce++
			return m, searchDebounceCmd(m.sDebounce)
		}
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
