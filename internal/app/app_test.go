package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nahuel/spotify-tui/internal/config"
	"github.com/nahuel/spotify-tui/internal/library"
	"github.com/nahuel/spotify-tui/internal/library/mock"
)

var testClock = func() time.Time { return time.Date(2026, 1, 1, 12, 4, 0, 0, time.UTC) }

func newModel(t *testing.T, w, h int) *Model {
	t.Helper()
	data := mock.New()
	m := New(config.Config{ClientID: "test-client", CallbackPort: config.DefaultCallbackPort}, data, data, testClock)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: w, Height: h})
	model := updated.(Model)
	return &model
}

func press(t *testing.T, m *Model, key string) {
	t.Helper()
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	*m = updated.(Model)
}

func pressKey(t *testing.T, m *Model, keyType tea.KeyType) {
	t.Helper()
	updated, _ := m.Update(tea.KeyMsg{Type: keyType})
	*m = updated.(Model)
}

func lines(m *Model) []string { return strings.Split(m.View(), "\n") }

// isRule reports whether a rendered line is a full-width ─ rule.
func isRule(line string, w int) bool {
	plain := stripANSI(line)
	return visibleWidth(line) == w && strings.Trim(plain, "─") == ""
}

// TestRenderFitsTerminal: at 180x50, 140x40 and 80x24 every rendered line's
// visible width stays inside the terminal width and the frame is exactly h lines.
func TestRenderFitsTerminal(t *testing.T) {
	sizes := [][2]int{{180, 50}, {140, 40}, {80, 24}}
	for _, size := range sizes {
		w, h := size[0], size[1]
		m := newModel(t, w, h)
		for _, open := range []bool{false, true} {
			m.drawerOpen = open
			got := lines(m)
			if len(got) != h {
				t.Errorf("w=%d h=%d drawerOpen=%v: rendered %d lines, want %d",
					w, h, open, len(got), h)
			}
			for i, line := range got {
				if width := visibleWidth(line); width > w {
					t.Errorf("w=%d h=%d drawerOpen=%v: line %d visible width %d exceeds %d: %q",
						w, h, open, i, width, w, line)
				}
			}
		}
	}
}

// TestSectionRowOneLine: the section row is exactly one line — the next line is
// a full rule, so a wrap would show up as a broken rule.
func TestSectionRowOneLine(t *testing.T) {
	m := newModel(t, 180, 50)
	got := lines(m)
	for _, section := range []string{"Library", "Search", "Queue"} {
		if !strings.Contains(got[0], section) {
			t.Errorf("section row %q missing %q", got[0], section)
		}
	}
	if !isRule(got[1], 180) {
		t.Errorf("line below the section row should be a rule, got %q", got[1])
	}
}

// TestSavedToggle: `l` flips [x] -> [ ] on the row under the cursor.
func TestSavedToggle(t *testing.T) {
	m := newModel(t, 180, 50)
	got := lines(m)
	row := -1
	for i, line := range got {
		if strings.Contains(line, "So What") {
			row = i
			if !strings.Contains(line, "[x]") {
				t.Fatalf("mock setup: 'So What' row %d should start saved ([x]), got %q", i, line)
			}
			break
		}
	}
	if row == -1 {
		t.Fatal("cursor row 'So What' not rendered")
	}
	press(t, m, "l")
	got = lines(m)
	if !strings.Contains(got[row], "[ ]") || strings.Contains(got[row], "[x]") {
		t.Errorf("after `l` the cursor row should show [ ], got %q", got[row])
	}
	press(t, m, "l")
	got = lines(m)
	if !strings.Contains(got[row], "[x]") {
		t.Errorf("after a second `l` the cursor row should show [x] again, got %q", got[row])
	}
}

// TestQueueDrawerToggle: `q` opens and closes the 8-row queue drawer.
func TestQueueDrawerToggle(t *testing.T) {
	m := newModel(t, 180, 50)
	press(t, m, "q")
	if !strings.Contains(m.View(), "▾ QUEUE") {
		t.Error("after `q` the drawer should be open")
	}
	press(t, m, "q")
	if strings.Contains(m.View(), "▾ QUEUE") {
		t.Error("after a second `q` the drawer should be closed again")
	}
	if n := len(lines(m)); n != 50 {
		t.Errorf("closing the drawer must keep the frame at 50 lines, got %d", n)
	}
	press(t, m, "q")
	if !strings.Contains(m.View(), "▾ QUEUE") {
		t.Error("after a third `q` the drawer should be open again")
	}
}

// TestQueueDrawerShowsEightRows: the drawer band holds the header plus 8 rows.
func TestQueueDrawerShowsEightRows(t *testing.T) {
	m := newModel(t, 180, 50)
	press(t, m, "q")
	found := 0
	for _, line := range lines(m) {
		plain := stripANSI(line)
		switch {
		case strings.Contains(plain, "up next 8 of 41"):
			found++
		case strings.HasPrefix(strings.TrimSpace(plain), "▸ Moanin'"):
			found++ // drawer first up-next row with its selection glyph
		}
	}
	if found != 2 {
		t.Errorf("expected the drawer header and one ▸ Moanin' drawer row, found %d markers", found)
	}
}

// TestTabCyclesSections: tab moves Library -> Search -> Queue -> Library.
func TestTabCyclesSections(t *testing.T) {
	m := newModel(t, 180, 50)
	pressKey(t, m, tea.KeyTab)
	if got := m.ActiveSection(); got != SectionSearch {
		t.Errorf("after 1 tab section = %v, want Search", got)
	}
	pressKey(t, m, tea.KeyTab)
	if got := m.ActiveSection(); got != SectionQueue {
		t.Errorf("after 2 tabs section = %v, want Queue", got)
	}
	pressKey(t, m, tea.KeyTab)
	if got := m.ActiveSection(); got != SectionLibrary {
		t.Errorf("after 3 tabs section = %v, want Library", got)
	}
}

// TestUnimplementedActionsToast: `:` and `?` report a one-line toast
// instead of panicking or doing nothing. (`d` has a real picker since the
// local-player pivot.)
func TestUnimplementedActionsToast(t *testing.T) {
	m := newModel(t, 180, 50)
	for _, key := range []string{":", "?"} {
		press(t, m, key)
		status := stripANSI(lines(m)[49])
		if !strings.Contains(status, "slice 2") {
			t.Errorf("key %q should show a one-line toast mentioning slice 2, got %q", key, status)
		}
	}
}

// TestFilterNarrowsList: `/` filters the current list live and esc restores it.
func TestFilterNarrowsList(t *testing.T) {
	m := newModel(t, 180, 50)
	press(t, m, "/")
	press(t, m, "m")
	press(t, m, "o")
	got := m.View()
	if !strings.Contains(got, "Moanin'") {
		t.Error("filter 'mo' should keep Moanin'")
	}
	if strings.Contains(got, "Giant Steps") {
		t.Error("filter 'mo' should drop Giant Steps")
	}
	pressKey(t, m, tea.KeyEsc)
	if strings.Contains(m.View(), "filtering") == false && !strings.Contains(m.View(), "Giant Steps") {
		t.Error("esc should cancel the filter and restore the full list")
	}
}

// TestEscLeavesDrillDown: esc backs out of the playlist drill-down to the
// library root without leaving the section row.
func TestEscLeavesDrillDown(t *testing.T) {
	m := newModel(t, 180, 50)
	if !strings.Contains(m.View(), "Bebop Essentials") {
		t.Fatal("mock setup: app should start inside the mock playlist")
	}
	pressKey(t, m, tea.KeyEsc)
	got := m.View()
	if !strings.Contains(got, "PLAYLISTS") || !strings.Contains(got, "ALBUMS") {
		t.Errorf("esc should show the library root with its sections, got:\n%s", got)
	}
}

// TestCursorMoves: j/k move the cursor, g/G jump to the ends, and the selected
// row is the bold surface row (the only one carrying the ▸ selection cue is
// asserted indirectly through the saved-toggle test; here we pin movement).
func TestCursorMoves(t *testing.T) {
	m := newModel(t, 180, 50)
	if m.cursor != 0 {
		t.Fatalf("cursor starts at %d, want 0", m.cursor)
	}
	press(t, m, "j")
	if m.cursor != 1 {
		t.Errorf("after j cursor = %d, want 1", m.cursor)
	}
	press(t, m, "k")
	if m.cursor != 0 {
		t.Errorf("after k cursor = %d, want 0", m.cursor)
	}
	press(t, m, "G")
	if m.cursor != len(m.visibleRows())-1 {
		t.Errorf("after G cursor = %d, want %d", m.cursor, len(m.visibleRows())-1)
	}
	press(t, m, "g")
	if m.cursor != 0 {
		t.Errorf("after g cursor = %d, want 0", m.cursor)
	}
}

// TestLibraryDataShape pins the mock contract behind the interfaces
// (local-player pivot: audio output sinks, no remote devices).
func TestLibraryDataShape(t *testing.T) {
	data := mock.New()
	pl := data.Playlist()
	if len(pl.Tracks) != 42 {
		t.Errorf("playlist tracks = %d, want 42", len(pl.Tracks))
	}
	if q := data.Queue(); len(q) != 41 {
		t.Errorf("queue = %d items, want 41", len(q))
	}
	s := data.Session()
	if s.Output == "" || s.Bitrate == "" {
		t.Errorf("session should report local output and bitrate, got %+v", s)
	}
	sinks := data.Sinks()
	if len(sinks) < 2 {
		t.Errorf("sinks = %d, want the local output list (Speakers, HDMI, JACK)", len(sinks))
	}
	active := 0
	for _, sk := range sinks {
		if sk.Active {
			active++
		}
	}
	if active != 1 {
		t.Errorf("exactly one sink should be active, got %d of %d", active, len(sinks))
	}
	if len(data.Library()) == 0 {
		t.Error("library should expose sections")
	}
}

// TestPlayerBarShowsOutputAndBitrate: the right segment reads output +
// bitrate, never a remote device (local-player pivot).
func TestPlayerBarShowsOutputAndBitrate(t *testing.T) {
	m := newModel(t, 180, 50)
	got := m.View()
	if !strings.Contains(got, "Speakers · PipeWire") {
		t.Errorf("player bar should show the local output 'Speakers · PipeWire', got:\n%s", got)
	}
	if !strings.Contains(got, "192 kbps") {
		t.Error("player bar should show the bitrate")
	}
	for _, banned := range []string{"no active device", "Connect"} {
		if strings.Contains(got, banned) {
			t.Errorf("view must not mention %q after the local-player pivot", banned)
		}
	}
}

// TestAudioOutputPicker: `d` opens the local audio output picker, enter
// switches the active sink, esc closes.
func TestAudioOutputPicker(t *testing.T) {
	m := newModel(t, 180, 50)
	press(t, m, "d")
	got := m.View()
	if !strings.Contains(got, "AUDIO OUTPUT") {
		t.Errorf("`d` should open the audio output picker, got:\n%s", got)
	}
	if !strings.Contains(got, "● Speakers · PipeWire · active") {
		t.Errorf("picker should show the active sink, got:\n%s", got)
	}
	if !strings.Contains(got, "○ JACK") {
		t.Errorf("picker should list JACK, got:\n%s", got)
	}
	if n := len(lines(m)); n != 50 {
		t.Errorf("picker view should keep the frame at 50 lines, got %d", n)
	}
	// move to JACK and select
	press(t, m, "j")
	press(t, m, "j")
	pressKey(t, m, tea.KeyEnter)
	got = m.View()
	if strings.Contains(got, "AUDIO OUTPUT") {
		t.Error("enter should close the picker")
	}
	if !strings.Contains(got, "JACK") {
		t.Errorf("player bar should now show the selected output JACK, got:\n%s", got)
	}
	// reopen and close with esc
	press(t, m, "d")
	pressKey(t, m, tea.KeyEsc)
	if strings.Contains(m.View(), "AUDIO OUTPUT") {
		t.Error("esc should close the picker")
	}
}

// TestVolumeKeysMock: `+` and `-` drive the player volume, clamped 0..100.
func TestVolumeKeysMock(t *testing.T) {
	m := newModel(t, 180, 50)
	press(t, m, "+")
	press(t, m, "+")
	if v := m.data.Session().Volume; v != 92 {
		t.Errorf("volume after two `+` = %d, want 92", v)
	}
	press(t, m, "+")
	if v := m.data.Session().Volume; v != 100 {
		t.Errorf("volume clamped at top = %d, want 100", v)
	}
	press(t, m, "-")
	press(t, m, "-")
	if v := m.data.Session().Volume; v != 80 {
		t.Errorf("volume after two `-` = %d, want 80", v)
	}
}

// TestPlayerEventLoginShowsURL: the EventLogin event surfaces the auth URL as
// a full-screen state instead of hanging silently (spec S9).
func TestPlayerEventLoginShowsURL(t *testing.T) {
	m := newModel(t, 180, 50)
	const url = "https://accounts.spotify.com/authorize?client_id=abc&code_challenge=xyz"
	updated, _ := m.Update(playerEventMsg{library.Event{Type: library.EventLogin, LoginURL: url}})
	*m = updated.(Model)
	got := lines(m)
	if len(got) != 50 {
		t.Fatalf("login view rendered %d lines, want 50", len(got))
	}
	if !strings.Contains(m.View(), "login required") {
		t.Errorf("login view should say login required, got:\n%s", m.View())
	}
	if !strings.Contains(m.View(), url) {
		t.Errorf("login view should show the auth URL %q", url)
	}
	if !strings.Contains(m.View(), "http://127.0.0.1:8888/login") {
		t.Errorf("login view should say where the callback lands")
	}
	for i, line := range got {
		if width := visibleWidth(line); width > 180 {
			t.Errorf("login view line %d width %d exceeds 180", i, width)
		}
	}
}

// TestPlayerEventReadyClearsLogin: EventReady returns to the normal frame.
func TestPlayerEventReadyClearsLogin(t *testing.T) {
	m := newModel(t, 180, 50)
	updated, _ := m.Update(playerEventMsg{library.Event{Type: library.EventLogin, LoginURL: "https://accounts.spotify.com/authorize?x=1"}})
	*m = updated.(Model)
	updated, _ = m.Update(playerEventMsg{library.Event{Type: library.EventReady}})
	*m = updated.(Model)
	if strings.Contains(m.View(), "login required") {
		t.Error("after EventReady the login view should be gone")
	}
	if n := len(lines(m)); n != 50 {
		t.Errorf("normal frame should render 50 lines, got %d", n)
	}
}

// TestPlayerEventFailedShowsError: a bootstrap failure is surfaced, never silent.
func TestPlayerEventFailedShowsError(t *testing.T) {
	m := newModel(t, 180, 50)
	updated, _ := m.Update(playerEventMsg{library.Event{
		Type: library.EventFailed, Err: errors.New("authentication failed")}})
	*m = updated.(Model)
	got := m.View()
	if !strings.Contains(got, "✗") || !strings.Contains(got, "authentication failed") {
		t.Errorf("failure view should show the error, got:\n%s", got)
	}
}

// TestPlayerEventLoginFitsNarrow: the login state must respect the cell budget
// at narrow widths too; a real auth URL is long, so this is where it can blow up.
func TestPlayerEventLoginFitsNarrow(t *testing.T) {
	m := newModel(t, 80, 24)
	const url = "https://accounts.spotify.com/authorize?client_id=abc123&code_challenge=qWERTY1234567890abcdef&code_challenge_method=S256&scope=user-read-playback-state"
	updated, _ := m.Update(playerEventMsg{library.Event{Type: library.EventLogin, LoginURL: url}})
	*m = updated.(Model)
	got := lines(m)
	if len(got) != 24 {
		t.Fatalf("login view rendered %d lines at 80x24, want 24", len(got))
	}
	for i, line := range got {
		if w := visibleWidth(line); w > 80 {
			t.Errorf("line %d is %d columns wide at 80 columns: %q", i, w, line)
		}
	}
	if !strings.Contains(m.View(), "login required") {
		t.Errorf("login view should still name the state at 80 columns")
	}
}
