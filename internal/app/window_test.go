package app

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// windowBox locates the floating window in the rendered frame and asserts it
// is a closed box: ┌ at the top, └ at the bottom, every line between framed
// with │ at the same columns, all edges aligned (S19, Contrast-Edge Rule).
func windowBox(t *testing.T, m *Model) (top, bottom, left, right int) {
	t.Helper()
	got := lines(m)
	for i, l := range got {
		r := []rune(stripANSI(l))
		tl, tr := -1, -1
		for c, ch := range r {
			switch ch {
			case '┌':
				tl = c
			case '┐':
				tr = c
			}
		}
		if tl < 0 {
			continue
		}
		if tr < 0 {
			t.Fatalf("window top edge at line %d has no right corner: %q", i, l)
		}
		for j := i + 1; j < len(got); j++ {
			rb := []rune(stripANSI(got[j]))
			bl, br := -1, -1
			for c, ch := range rb {
				switch ch {
				case '└':
					bl = c
				case '┘':
					br = c
				}
			}
			if bl < 0 {
				continue
			}
			if br < 0 {
				t.Fatalf("window bottom edge at line %d has no right corner: %q", j, got[j])
			}
			if bl != tl || br != tr {
				t.Fatalf("window edges misaligned: top %d..%d, bottom %d..%d", tl, tr, bl, br)
			}
			for k := i + 1; k < j; k++ {
				rk := []rune(stripANSI(got[k]))
				if len(rk) <= tr || rk[tl] != '│' || rk[tr] != '│' {
					t.Fatalf("window line %d is not framed: %q", k, got[k])
				}
			}
			return i, j, tl, tr
		}
		t.Fatalf("window opened at line %d never closes", i)
	}
	t.Fatalf("no floating window found in view:\n%s", m.View())
	return 0, 0, 0, 0
}

// TestFloatingWindowsFitAndAreClosedBoxes: every window key opens one closed
// ┌─┐ │ └─┘ box sized to min(104, w-4) columns and min(14, h-6) rows, one
// line per row, with no overflow at 180, 140 and 80 columns.
func TestFloatingWindowsFitAndAreClosedBoxes(t *testing.T) {
	for _, size := range [][2]int{{180, 50}, {140, 40}, {80, 24}} {
		w, h := size[0], size[1]
		for _, key := range []string{"/", ":", "d", "?"} {
			m := newModel(t, w, h)
			m.drawerOpen = w >= 100 // drawer coexistence at wide terminals
			press(t, m, key)
			top, bottom, left, right := windowBox(t, m)
			winW, winH := right-left+1, bottom-top+1
			if maxW := min(104, w-4); winW > maxW {
				t.Errorf("%q at %dx%d: window is %d columns, want <= min(104, w-4) = %d", key, w, h, winW, maxW)
			}
			if maxH := min(14, h-6); winH > maxH {
				t.Errorf("%q at %dx%d: window is %d rows, want <= min(14, h-6) = %d", key, w, h, winH, maxH)
			}
			got := lines(m)
			if len(got) != h {
				t.Errorf("%q at %dx%d: rendered %d lines, want %d", key, w, h, len(got), h)
			}
			for i, line := range got {
				if vw := visibleWidth(line); vw > w {
					t.Errorf("%q at %dx%d: line %d visible width %d exceeds %d: %q", key, w, h, i, vw, w, line)
				}
			}
			pressKey(t, m, tea.KeyEsc)
			if strings.Contains(m.View(), "┌") {
				t.Errorf("%q: esc must close the window", key)
			}
		}
	}
}

// TestWindowEscRestoresListAndCursor: esc closes the window and puts the list
// back exactly as it was, cursor and scroll offset included.
func TestWindowEscRestoresListAndCursor(t *testing.T) {
	m := newModel(t, 180, 50)
	press(t, m, "j")
	press(t, m, "j") // cursor 2
	before := m.View()
	preCursor, preOffset := m.cursor, m.listOffset
	for _, key := range []string{"d", "?", ":"} {
		press(t, m, key)
		if !strings.Contains(m.View(), "┌") {
			t.Fatalf("%q should open a floating window", key)
		}
		press(t, m, "j") // move inside the window, not the list
		pressKey(t, m, tea.KeyEsc)
		if m.cursor != preCursor || m.listOffset != preOffset {
			t.Errorf("%q: esc restored cursor=%d offset=%d, want %d/%d", key, m.cursor, m.listOffset, preCursor, preOffset)
		}
		if got := m.View(); got != before {
			t.Errorf("%q: esc must restore the exact frame before the window", key)
		}
	}
	// search: the filter narrows the list behind the window; esc restores it
	press(t, m, "/")
	press(t, m, "m")
	press(t, m, "o")
	if strings.Contains(m.View(), "Giant Steps") {
		t.Error("filter 'mo' should hide Giant Steps behind the search window")
	}
	pressKey(t, m, tea.KeyEsc)
	if m.cursor != preCursor || m.listOffset != preOffset {
		t.Errorf("search esc restored cursor=%d offset=%d, want %d/%d", m.cursor, m.listOffset, preCursor, preOffset)
	}
	if got := m.View(); got != before {
		t.Error("search esc must restore the exact frame before the window")
	}
}

// TestWindowOpenKeepsListScroll: opening a window never moves the list or its
// scroll offset (S19: the list behind stays put).
func TestWindowOpenKeepsListScroll(t *testing.T) {
	for _, key := range []string{"/", ":", "d", "?"} {
		m := newModel(t, 80, 24)
		press(t, m, "G") // scroll to the bottom of the 42-track playlist
		preCursor, preOffset := m.cursor, m.listOffset
		if preOffset == 0 {
			t.Fatal("mock setup: G should scroll the list at 80 columns")
		}
		press(t, m, key)
		if m.cursor != preCursor || m.listOffset != preOffset {
			t.Errorf("%q changed the list state: cursor=%d offset=%d, want %d/%d",
				key, m.cursor, m.listOffset, preCursor, preOffset)
		}
		pressKey(t, m, tea.KeyEsc)
	}
}

// windowInner returns the visible text strictly inside the floating window's
// frame (between the edges, excluding them), so tests can assert what the
// window itself renders regardless of the list behind it.
func windowInner(t *testing.T, m *Model) string {
	t.Helper()
	top, bottom, left, right := windowBox(t, m)
	var b strings.Builder
	for i := top + 1; i < bottom; i++ {
		r := []rune(stripANSI(lines(m)[i]))
		if right+1 <= len(r) {
			r = r[left+1 : right]
		}
		b.WriteString(strings.TrimRight(string(r), " "))
		b.WriteString("\n")
	}
	return b.String()
}

// TestSearchWindowIsInputOnly: `/` opens an input-only window — header,
// prompt row and one hint line, never a result row (T19). The results appear
// in the list behind, not inside the window.
func TestSearchWindowIsInputOnly(t *testing.T) {
	m := newModel(t, 180, 50)
	press(t, m, "/")
	top, bottom, _, _ := windowBox(t, m)
	if got := bottom - top + 1; got > 10 {
		t.Errorf("an empty input-only window should be ~8 lines, got %d", got)
	}
	press(t, m, "m")
	press(t, m, "o")
	inner := windowInner(t, m)
	for _, banned := range []string{"Moanin", "So What", "Giant Steps"} {
		if strings.Contains(inner, banned) {
			t.Errorf("the search window must never render result rows, found %q inside it:\n%s", banned, inner)
		}
	}
	if !strings.Contains(inner, "> ") {
		t.Errorf("the search window must show its > input prompt, got:\n%s", inner)
	}
	// esc still restores the list behind unchanged
	pressKey(t, m, tea.KeyEsc)
	if strings.Contains(m.View(), "┌") {
		t.Error("esc must close the search window")
	}
}

// TestPalettePlay: `:play` accepts a bare 22-char base62 id and a full
// spotify:track: URI (with surrounding whitespace and quotes), plays it
// through the existing player adapter, and rejects garbage with a one-line
// toast naming what was expected (T13).
func TestPalettePlay(t *testing.T) {
	const id = "4uLU6hMCjMI75M1A2tKUQC"
	m := newModel(t, 180, 50)
	play := func(cmd string) {
		t.Helper()
		press(t, m, ":")
		for _, r := range cmd {
			press(t, m, string(r))
		}
		pressKey(t, m, tea.KeyEnter)
	}
	play("play " + id)
	if got := m.data.Session().TrackID; got != id {
		t.Errorf("bare id: TrackID = %q, want %q", got, id)
	}
	if status := stripANSI(lines(m)[49]); !strings.Contains(status, "▶") {
		t.Errorf("bare id should confirm playback, got %q", status)
	}
	play("play spotify:track:" + id)
	if got := m.data.Session().TrackID; got != id {
		t.Errorf("uri: TrackID = %q, want %q", got, id)
	}
	play(` play  "` + id + `"  `)
	if got := m.data.Session().TrackID; got != id {
		t.Errorf("quoted+whitespace: TrackID = %q, want %q", got, id)
	}
	play("play not-a-track")
	if got := m.data.Session().TrackID; got != id {
		t.Errorf("garbage must not reach the player: TrackID = %q, want %q", got, id)
	}
	if status := stripANSI(lines(m)[49]); !strings.Contains(status, ":play") || !strings.Contains(status, "track") {
		t.Errorf("invalid target must toast what was expected, got %q", status)
	}
	play("frobnicate now")
	if status := stripANSI(lines(m)[49]); !strings.Contains(status, "frobnicate") {
		t.Errorf("unknown command should keep toasting with its name, got %q", status)
	}
}

// TestHelpListsStatusBarKeys: `?` lists every key the status bar shows
// (Keys-Once Rule: one source of key facts).
func TestHelpListsStatusBarKeys(t *testing.T) {
	m := newModel(t, 180, 50)
	hints := stripANSI(m.keyHints(1000))
	if hints == "" {
		t.Fatal("mock setup: status bar has no key hints")
	}
	press(t, m, "?")
	help := stripANSI(m.View())
	for _, part := range strings.Fields(hints) {
		if !strings.Contains(help, part) {
			t.Errorf("? help must list the status bar key %q", part)
		}
	}
}

// TestPaletteAndHelpOpenWindows: `:` and `?` are floating windows now (S19,
// T12) — the palette shows its `>` input, help lists the keymap; both close
// with esc.
func TestPaletteAndHelpOpenWindows(t *testing.T) {
	m := newModel(t, 180, 50)
	press(t, m, ":")
	windowBox(t, m)
	if !strings.Contains(stripANSI(m.View()), "command") {
		t.Error("palette window should be titled command")
	}
	if !strings.Contains(stripANSI(m.View()), ">") {
		t.Error("palette window should show the > input prompt")
	}
	pressKey(t, m, tea.KeyEsc)
	if strings.Contains(m.View(), "┌") {
		t.Error("esc should close the palette")
	}
	press(t, m, "?")
	windowBox(t, m)
	if !strings.Contains(stripANSI(m.View()), "help") {
		t.Error("help window should be titled help")
	}
	pressKey(t, m, tea.KeyEsc)
	if strings.Contains(m.View(), "┌") {
		t.Error("esc should close help")
	}
}

// TestSearchWindowAnnouncesRemoteSearch was removed with T19: the window is
// input-only now, so there is no in-window result list left to announce.
