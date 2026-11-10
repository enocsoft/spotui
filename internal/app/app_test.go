package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/enocsoft/spotui/internal/config"
	"github.com/enocsoft/spotui/internal/library"
	"github.com/enocsoft/spotui/internal/library/mock"
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
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
	*m = updated.(Model)
	runPlayerCmds(t, m, cmd)
}

func pressKey(t *testing.T, m *Model, keyType tea.KeyType) {
	t.Helper()
	updated, cmd := m.Update(tea.KeyMsg{Type: keyType})
	*m = updated.(Model)
	runPlayerCmds(t, m, cmd)
}

// runPlayerCmds drives the async plumbing a real bubbletea runtime drives
// for us: it runs the command a keypress (or a catalog message) returned,
// and applies the messages that change model state — player results and
// catalog pages — then recurses on whatever they returned in turn. Ticks and
// toast timers are run (they shrunk to ~1ms in TestMain) but never applied:
// a real runtime applies them on its own schedule, and no test may depend on
// a toast expiring.
func runPlayerCmds(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	switch msg := cmd().(type) {
	case nil:
	case tea.BatchMsg:
		for _, sub := range msg {
			if sub != nil {
				runPlayerCmds(t, m, sub)
			}
		}
	case playerMsg:
		applyRun(t, m, msg)
	case playlistsMsg, albumsMsg, savedTracksMsg, itemsMsg, searchMsg, savedMsg:
		applyRun(t, m, msg)
	default:
		// ticks and toasts: scheduled for real by bubbletea, dropped here
	}
}

// applyRun feeds one state-carrying message into Update and drains what it
// returned in turn (a player result may toast; a catalog page may play).
func applyRun(t *testing.T, m *Model, msg tea.Msg) {
	t.Helper()
	updated, next := m.Update(msg)
	*m = updated.(Model)
	runPlayerCmds(t, m, next)
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

// TestSectionRowOneLine: the section row is exactly one line and renders
// only `Tracks │ Playlists` (T20/S18) — Search and Queue are never cells: the
// queue is the drawer and search results are a transient view.
func TestSectionRowOneLine(t *testing.T) {
	m := newModel(t, 180, 50)
	got := lines(m)
	for _, section := range []string{"Tracks", "Playlists"} {
		if !strings.Contains(got[0], section) {
			t.Errorf("section row %q missing %q", got[0], section)
		}
	}
	for _, banned := range []string{"Search", "Queue"} {
		if strings.Contains(got[0], banned) {
			t.Errorf("section row %q must not carry a %q cell", got[0], banned)
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

// TestTabTogglesSections: tab moves Tracks -> Playlists -> Tracks. There are
// only two sections now (T20).
func TestTabTogglesSections(t *testing.T) {
	m := newModel(t, 180, 50)
	if got := m.ActiveSection(); got != SectionTracks {
		t.Errorf("mock starts inside the open list, section = %v, want Tracks", got)
	}
	pressKey(t, m, tea.KeyTab)
	if got := m.ActiveSection(); got != SectionPlaylists {
		t.Errorf("after 1 tab section = %v, want Playlists", got)
	}
	pressKey(t, m, tea.KeyTab)
	if got := m.ActiveSection(); got != SectionTracks {
		t.Errorf("after 2 tabs section = %v, want Tracks", got)
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

// TestPlayerErrorViewDoesNotAssumePremium: a bootstrap failure must surface the
// real error; a bare 400 from the client-token exchange is not proof that the
// account lacks Premium, and saying so sends people down the wrong path.
func TestPlayerErrorViewDoesNotAssumePremium(t *testing.T) {
	m := newModel(t, 180, 50)
	m.playerErr = errors.New("failed obtaining client token: status=400")
	got := m.View()
	if strings.Contains(got, "needs Spotify Premium") {
		t.Errorf("failure view asserts the account is not Premium without knowing it")
	}
	if !strings.Contains(got, "client token") {
		t.Errorf("failure view should show the real error, got:\n%s", got)
	}
}

// TestLoginViewShowsBuildRevision: the screen that shows the auth URL must also
// say which build produced it, so a stale binary is visible instead of silent.
func TestLoginViewShowsBuildRevision(t *testing.T) {
	m := newModel(t, 180, 50)
	updated, _ := m.Update(playerEventMsg{library.Event{Type: library.EventLogin, LoginURL: "https://example.test/auth"}})
	*m = updated.(Model)
	if !strings.Contains(m.View(), Revision()) {
		t.Errorf("login view should name the build revision")
	}
	if Revision() == "" {
		t.Errorf("Revision must never be empty")
	}
}

const longAuthURL = "https://accounts.spotify.com/authorize?client_id=abc123&code_challenge=Q2xhc3NpYy1jaGFsbGVuZ2U&code_challenge_method=S256&redirect_uri=http%3A%2F%2F127.0.0.1%3A8888%2Flogin&response_type=code&scope=user-read-private+user-read-playback-state&state=st4te"

// TestWrapURLKeepsEveryParameter: wrapping must not drop a single byte. A URL
// that reaches the browser without response_type makes the server answer
// "response_type must be code" and sends the reader hunting the wrong bug.
func TestWrapURLKeepsEveryParameter(t *testing.T) {
	chunks := wrapURL(longAuthURL, 60)
	if got := strings.Join(chunks, ""); got != longAuthURL {
		t.Fatalf("wrapping lost bytes:\n got %q\nwant %q", got, longAuthURL)
	}
	for i, c := range chunks {
		if len(c) > 60 {
			t.Errorf("chunk %d is %d wide, want <= 60", i, len(c))
		}
		if i > 0 && !strings.HasPrefix(c, "&") {
			t.Errorf("chunk %d should start on a parameter boundary: %q", i, c)
		}
	}
	if !strings.Contains(strings.Join(chunks, ""), "response_type=code") {
		t.Errorf("response_type must survive wrapping")
	}
}

// TestLoginViewKeepsTheWholeURL: at 80 columns the screen cannot show a 300
// character URL on one line, so it wraps it and names the file holding it
// verbatim. Truncating it is what broke a real login attempt.
func TestLoginViewKeepsTheWholeURL(t *testing.T) {
	m := newModel(t, 80, 24)
	updated, _ := m.Update(playerEventMsg{library.Event{
		Type: library.EventLogin, LoginURL: longAuthURL, LoginFile: "/state/login-url.txt",
	}})
	*m = updated.(Model)
	view := m.View()
	if !strings.Contains(strings.Join(strings.Fields(view), ""), "response_type=code") {
		t.Errorf("login view at 80 columns lost response_type=code:\n%s", view)
	}
	if !strings.Contains(view, "/state/login-url.txt") {
		t.Errorf("login view should name the file with the full URL")
	}
	for i, line := range lines(m) {
		if w := visibleWidth(line); w > 80 {
			t.Errorf("line %d is %d columns wide", i, w)
		}
	}
}

// TestConnectingStateIsVisible: the callback used to land on a screen that said
// nothing while the session negotiated, so a slow handshake looked like a dead
// app. The state must be visible and must hand back to the list once ready.
func TestConnectingStateIsVisible(t *testing.T) {
	m := newModel(t, 80, 24)
	updated, _ := m.Update(playerEventMsg{library.Event{Type: library.EventConnecting}})
	*m = updated.(Model)
	if !strings.Contains(m.View(), "starting the session") {
		t.Errorf("the handshake must be visible, got:\n%s", m.View())
	}
	updated, _ = m.Update(playerEventMsg{library.Event{Type: library.EventReady}})
	*m = updated.(Model)
	if strings.Contains(m.View(), "starting the session") {
		t.Errorf("ready must leave the connecting state")
	}
}

// TestFailureAfterLoginIsVisible: the login screen used to outrank the error view,
// so a session failure after the URL was shown displayed the URL and nothing else —
// indistinguishable from "still waiting".
func TestFailureAfterLoginIsVisible(t *testing.T) {
	m := newModel(t, 80, 24)
	updated, _ := m.Update(playerEventMsg{library.Event{Type: library.EventLogin, LoginURL: "https://example.test/auth", LoginFile: "/f"}})
	*m = updated.(Model)
	updated, _ = m.Update(playerEventMsg{library.Event{Type: library.EventFailed, Err: errors.New("failed exchanging oauth2 code: 400")}})
	*m = updated.(Model)
	view := m.View()
	if !strings.Contains(view, "player error") {
		t.Errorf("a failure after the login URL must be visible, got:\n%s", view)
	}
	if strings.Contains(view, "login required") {
		t.Errorf("the login screen must not outrank the error")
	}
}

// TestLoginViewShowsWhichLoginAsks: with two tokens there are two consents on
// first run, served sequentially on the same port. The login state must say
// which half of the app is asking so the two screens are not confusing.
func TestLoginViewShowsWhichLoginAsks(t *testing.T) {
	m := newModel(t, 80, 24)
	updated, _ := m.Update(playerEventMsg{library.Event{
		Type: library.EventLogin, LoginURL: "https://example.test/auth",
		LoginPurpose: library.LoginPurposeLibrary}})
	*m = updated.(Model)
	got := m.View()
	if !strings.Contains(got, "this login is for the library") {
		t.Errorf("the library login must name itself, got:\n%s", got)
	}
	if strings.Contains(got, "this login is for playback") {
		t.Errorf("the library login must not present itself as the playback login:\n%s", got)
	}

	m = newModel(t, 80, 24)
	updated, _ = m.Update(playerEventMsg{library.Event{
		Type: library.EventLogin, LoginURL: "https://example.test/auth",
		LoginPurpose: library.LoginPurposePlayback}})
	*m = updated.(Model)
	if !strings.Contains(m.View(), "this login is for playback") {
		t.Errorf("the playback login must name itself, got:\n%s", m.View())
	}

	// A login event without a purpose (older emitter) still reads as playback.
	m = newModel(t, 80, 24)
	updated, _ = m.Update(playerEventMsg{library.Event{
		Type: library.EventLogin, LoginURL: "https://example.test/auth"}})
	*m = updated.(Model)
	if !strings.Contains(m.View(), "this login is for playback") {
		t.Errorf("an unlabeled login must default to playback, got:\n%s", m.View())
	}
}

// TestOpenKeyLaunchesTheBrowser: the login screen must be able to open the
// authorization URL with `o`, with the URL as a single argv entry (no shell), and
// the key must do nothing anywhere else.
func TestOpenKeyLaunchesTheBrowser(t *testing.T) {
	prev := openInBrowser
	t.Cleanup(func() { openInBrowser = prev })

	var opened []string
	openInBrowser = func(u string) error { opened = append(opened, u); return nil }

	const url = "https://accounts.spotify.com/authorize?client_id=abc&response_type=code&state=s"
	m := newModel(t, 180, 50)
	updated, _ := m.Update(playerEventMsg{library.Event{Type: library.EventLogin, LoginURL: url, LoginFile: "/state/login-url.txt"}})
	*m = updated.(Model)

	_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	if cmd == nil {
		t.Fatal("pressing o on the login screen must return a command")
	}
	if msg := cmd(); msg != nil {
		if _, ok := msg.(browserOpenedMsg); !ok {
			t.Fatalf("expected browserOpenedMsg, got %T", msg)
		}
	}
	if len(opened) != 1 || opened[0] != url {
		t.Fatalf("opener received %v, want exactly the login URL", opened)
	}

	// outside the login screen the key is inert
	opened = nil
	m2 := newModel(t, 180, 50)
	if _, cmd := m2.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}}); cmd != nil {
		if msg := cmd(); msg != nil {
			if _, ok := msg.(browserOpenedMsg); ok {
				t.Fatalf("o must not open anything outside the login screen")
			}
		}
	}
	if len(opened) != 0 {
		t.Fatalf("opener was called outside the login screen: %v", opened)
	}
}

// TestProgressBarIsDashed: the progress track is a run of hyphens, not the
// heavy bar with a knob the design used before.
func TestProgressBarIsDashed(t *testing.T) {
	m := newModel(t, 180, 50)
	for _, line := range lines(m) {
		if !strings.Contains(line, "0:42") && !strings.Contains(line, "0:00") {
			continue
		}
		if strings.Contains(line, "●") {
			t.Errorf("the progress bar still draws a knob: %q", line)
		}
		if strings.Contains(line, "━") {
			t.Errorf("the progress bar still draws the heavy line: %q", line)
		}
		if !strings.Contains(line, "-") {
			t.Errorf("the progress bar should be a run of hyphens: %q", line)
		}
	}
}
