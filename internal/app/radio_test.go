package app

// T26 + the B/D revision: radio (autoplay) starts ON — like Spotify, playing
// a context continues with related tracks when it ends — and `R` toggles it.
// The end-of-track boundary is derived from the one-second state tick (the
// player events carry no "track ended" signal): when the reported position
// reaches the duration the boundary fires exactly once per track, the
// injectable autoplay resolver asks Spotify for the next related context,
// and the first playable result plays through the existing play path —
// outside the open list, which never changes. Resolver failures toast and
// stop: no retry loop. The same resolver path serves `n` when the playback
// order runs out.

import (
	"os"

	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/enocsoft/spotui/internal/config"
	"github.com/enocsoft/spotui/internal/library"
)

// radioData is a DataSource whose session the test drives directly.
type radioData struct {
	pls      library.Playlist
	queue    []library.Track
	sinks    []library.Sink
	sections []library.Section
	session  library.Session
}

func newRadioData() *radioData {
	track := library.Track{ID: realTrackID, Title: "So What", Artist: "Miles Davis", Album: "Kind of Blue", Duration: 100 * time.Second}
	return &radioData{
		pls:      library.Playlist{Name: "Bebop Essentials", Owner: "nahuel", Private: true, Tracks: []library.Track{track}},
		sinks:    []library.Sink{{Name: "Speakers · PipeWire", Active: true}},
		sections: []library.Section{{Name: "PLAYLISTS", Count: 1}},
		session: library.Session{
			State:    library.StatePlaying,
			Output:   "Speakers · PipeWire",
			TrackID:  track.ID,
			Title:    track.Title,
			Elapsed:  0,
			Total:    track.Duration,
			Volume:   50,
			Repeat:   "off",
			QueueLen: 0,
		},
	}
}

func (d *radioData) Library() []library.Section { return d.sections }
func (d *radioData) Playlist() library.Playlist { return d.pls }
func (d *radioData) Queue() []library.Track     { return d.queue }
func (d *radioData) Session() library.Session   { return d.session }
func (d *radioData) Sinks() []library.Sink      { return d.sinks }
func (d *radioData) SetSaved(id string, saved bool) error {
	for i := range d.pls.Tracks {
		if d.pls.Tracks[i].ID == id {
			d.pls.Tracks[i].Saved = saved
			return nil
		}
	}
	return errors.New("no track " + id)
}
func (d *radioData) SetSink(name string) error { return nil }

// radioPlayer is a Player plus the AutoplayResolver seam, recording what it
// was asked and what it played.
type radioPlayer struct {
	data       *radioData
	played     []library.Track
	next       []library.Track // what every resolve answers
	err        error           // what every resolve fails with
	calls      []string        // "seed|recent..." of every resolve call
	rejectedID string          // Play rejects this track like a mock row would
}

func (p *radioPlayer) Play(t library.Track) error {
	if t.ID == p.rejectedID {
		return errors.New("not a Spotify track id")
	}
	p.played = append(p.played, t)
	p.data.session.State = library.StatePlaying
	p.data.session.TrackID = t.ID
	p.data.session.Title = t.Title
	p.data.session.Elapsed = 0
	p.data.session.Total = t.Duration
	return nil
}
func (p *radioPlayer) TogglePause() error            { return nil }
func (p *radioPlayer) Next() error                   { return nil }
func (p *radioPlayer) Prev() error                   { return nil }
func (p *radioPlayer) Enqueue(t library.Track) error { return nil }
func (p *radioPlayer) VolumeUp() error               { return nil }
func (p *radioPlayer) VolumeDown() error             { return nil }

// ResolveAutoplay is the seam the app drives: it must be called with the
// seed uri and the recent uris, and answers the canned results.
func (p *radioPlayer) ResolveAutoplay(ctx context.Context, seed string, recent []string) ([]library.Track, error) {
	if ctx == nil {
		return nil, errors.New("no context")
	}
	p.calls = append(p.calls, seed+"|"+strings.Join(recent, ","))
	return p.next, p.err
}

func newRadioModel(t *testing.T, w, h int, data *radioData, rp *radioPlayer) *Model {
	t.Helper()
	mm := New(config.Config{ClientID: "test-client", CallbackPort: config.DefaultCallbackPort}, data, rp, testClock)
	updated, _ := mm.Update(tea.WindowSizeMsg{Width: w, Height: h})
	m := updated.(Model)
	return &m
}

// radioAtEnd puts the session exactly at the end of the current track.
func radioAtEnd(d *radioData) {
	d.session.Elapsed = d.session.Total
}

// runRadioTick feeds one state tick through Update and runs the command it
// produced. tickCmd sleeps a full second (the render re-arm); the radio
// resolve answers instantly, so a 100 ms timeout separates the two: it
// reports whether this tick fired the end-of-track boundary.
func runRadioTick(t *testing.T, m *Model) (radioResolvedMsg, bool) {
	t.Helper()
	// The boundary needs the position to be frozen at the end band, so it takes
	// two consecutive ticks: the first records the position, the second sees it
	// unchanged. Tick twice and report the first fire.
	for attempt := 0; attempt < 2; attempt++ {
		updated, cmd := m.Update(tickMsg{})
		*m = updated.(Model)
		if cmd == nil {
			continue
		}
		if msg, ok := runRadioCmd(t, cmd); ok {
			return msg, true
		}
	}
	return radioResolvedMsg{}, false
}

func runRadioCmd(t *testing.T, cmd tea.Cmd) (radioResolvedMsg, bool) {
	t.Helper()
	type res struct {
		msg radioResolvedMsg
		ok  bool
	}
	ch := make(chan res, 1)
	go func() {
		msg := cmd()
		// Since R4-001 the boundary returns the resolve batched with the tick, so
		// the tick chain cannot die with a stale answer. Unwrap it here and ignore
		// the tick's own message.
		if batch, is := msg.(tea.BatchMsg); is {
			for _, sub := range batch {
				if sub == nil {
					continue
				}
				if rm, ok := sub().(radioResolvedMsg); ok {
					ch <- res{rm, true}
					return
				}
			}
			ch <- res{radioResolvedMsg{}, false}
			return
		}
		if rm, is := msg.(radioResolvedMsg); is {
			ch <- res{rm, true}
			return
		}
		ch <- res{radioResolvedMsg{}, false}
	}()
	select {
	case r := <-ch:
		return r.msg, r.ok
	case <-time.After(3 * time.Second):
		return radioResolvedMsg{}, false // no resolve came back
	}
}

// TestRadioStartsOn (B): autoplay starts enabled — like Spotify — and the
// player bar reports the state before any key is pressed.
func TestRadioStartsOn(t *testing.T) {
	data := newRadioData()
	rp := &radioPlayer{data: data}
	m := newRadioModel(t, 180, 50, data, rp)
	if !m.radioOn {
		t.Fatal("radio must start on")
	}
	if got := stripANSI(strings.Join(lines(m), "\n")); !strings.Contains(got, "radio on") {
		t.Errorf("the player bar must report radio on from the start, got:\n%s", got)
	}
}

// TestRadioTogglePlayerBar: `R` toggles both ways and the player bar follows
// — `radio off` while off, `radio on` while on.
func TestRadioTogglePlayerBar(t *testing.T) {
	data := newRadioData()
	rp := &radioPlayer{data: data}
	m := newRadioModel(t, 180, 50, data, rp)
	if !m.radioOn {
		t.Fatal("radio must start on")
	}
	press(t, m, "R")
	if m.radioOn {
		t.Error("R must turn radio off")
	}
	if m.toast.text != "radio off" {
		t.Errorf("the toggle must toast, got %q", m.toast.text)
	}
	bar := playerBarLines(m)
	if strings.Contains(bar, "radio on") {
		t.Errorf("player bar must not report radio on while it is off, got:\n%s", bar)
	}
	if !strings.Contains(bar, "radio off") {
		t.Errorf("player bar must report radio off while it is off, got:\n%s", bar)
	}
	press(t, m, "R")
	if !m.radioOn {
		t.Error("a second R must turn radio on again")
	}
	if m.toast.text != "radio on" {
		t.Errorf("the toggle must toast, got %q", m.toast.text)
	}
	if bar = playerBarLines(m); !strings.Contains(bar, "radio on") {
		t.Errorf("player bar must report radio on when enabled, got:\n%s", bar)
	}
}

// playerBarLines collects the rendered text of the player bar's mode row.
func playerBarLines(m *Model) string {
	var bar strings.Builder
	for _, l := range lines(m) {
		if strings.Contains(stripANSI(l), "vol") || strings.Contains(stripANSI(l), "shuffle") {
			bar.WriteString(stripANSI(l) + "\n")
		}
	}
	return bar.String()
}

// TestRadioBoundaryFiresOnce: at the end of the track the boundary fires
// exactly once — repeated ticks while the resolve is in flight and after it
// never re-fire for the same track — and the resolved track plays through
// the existing play path.
func TestRadioBoundaryFiresOnce(t *testing.T) {
	data := newRadioData()
	rp := &radioPlayer{data: data, next: []library.Track{
		{ID: realTrackID2, Title: "Freddie Freeloader", Artist: "Miles Davis", Album: "Kind of Blue", Duration: 100 * time.Second},
	}}
	m := newRadioModel(t, 180, 50, data, rp)

	radioAtEnd(data)
	msg, fired := runRadioTick(t, m)
	if !fired {
		t.Fatal("the end-of-track boundary did not fire")
	}
	updated, cmd := m.Update(msg)
	*m = updated.(Model)
	runPlayerCmds(t, m, cmd)
	if got := len(rp.played); got != 1 {
		t.Fatalf("the resolved track must play exactly once, played %d", got)
	}
	if rp.played[0].ID != realTrackID2 {
		t.Errorf("played %q, want the first resolved track %q", rp.played[0].ID, realTrackID2)
	}
	if data.session.TrackID != realTrackID2 {
		t.Errorf("the session must mirror the radio track, got %q", data.session.TrackID)
	}

	// the new track plays normally below its end: further ticks do not fire
	data.session.Elapsed = data.session.Total / 2
	if _, fired := runRadioTick(t, m); fired {
		t.Fatal("a mid-track tick must not fire the boundary")
	}

	// the same boundary must not re-fire: park the (old) track at its end
	// again without a new track having played — radioFiredID latches it.
	data.session.TrackID = realTrackID
	data.session.Title = "So What"
	radioAtEnd(data)
	for i := 0; i < 5; i++ {
		if _, fired := runRadioTick(t, m); fired {
			t.Fatalf("tick %d: the boundary re-fired for the same track", i)
		}
	}
	if got := len(rp.calls); got != 1 {
		t.Errorf("the resolver must be called exactly once per boundary, called %d times", got)
	}
}

// TestRadioSeedsWithCurrentAndRecent: the resolve is asked for the current
// track's uri as the seed and the recently played uris alongside.
func TestRadioSeedsWithCurrentAndRecent(t *testing.T) {
	data := newRadioData()
	rp := &radioPlayer{data: data, next: []library.Track{
		{ID: realTrackID2, Title: "Freddie Freeloader", Duration: time.Minute},
	}}
	m := newRadioModel(t, 180, 50, data, rp)

	// the user played a track through the list first: it lands in the recent
	// uris the resolver sees
	runPlayerCmds(t, m, m.playRow(library.Track{ID: realTrackID, Title: "So What", Duration: 100 * time.Second}))
	radioAtEnd(data)
	msg, fired := runRadioTick(t, m)
	if !fired {
		t.Fatal("the end-of-track boundary did not fire")
	}
	updated, cmd := m.Update(msg)
	*m = updated.(Model)
	runPlayerCmds(t, m, cmd)
	if len(rp.calls) != 1 {
		t.Fatalf("expected exactly one resolve call, got %d", len(rp.calls))
	}
	call := rp.calls[0]
	if !strings.HasPrefix(call, "spotify:track:"+realTrackID+"|") {
		t.Errorf("the seed must be the current track's uri, got %q", call)
	}
	// the row the user played through the list rides the recent uris too
	if !strings.Contains(strings.SplitN(call, "|", 2)[1], "spotify:track:"+realTrackID) {
		t.Errorf("the recently played uris must ride the resolve, got %q", call)
	}
}

// TestRadioLeavesOpenListUntouched: radio plays outside the view — the open
// list keeps exactly its rows and the cursor stays put, and the queue drawer
// shows what radio has lined up while it is active.
func TestRadioLeavesOpenListUntouched(t *testing.T) {
	data := newRadioData()
	rp := &radioPlayer{data: data, next: []library.Track{
		{ID: realTrackID2, Title: "Freddie Freeloader", Duration: time.Minute},
		{ID: "3uLU6hMCjMI75M1A2tKUQC", Title: "Blue in Green", Duration: 2 * time.Minute},
	}}
	m := newRadioModel(t, 180, 50, data, rp)

	rowsBefore := len(m.visibleRows())
	cursorBefore := m.cursor
	radioAtEnd(data)
	msg, fired := runRadioTick(t, m)
	if !fired {
		t.Fatal("the end-of-track boundary did not fire")
	}
	updated, cmd := m.Update(msg)
	*m = updated.(Model)
	runPlayerCmds(t, m, cmd)
	if got := len(m.visibleRows()); got != rowsBefore {
		t.Errorf("the open list changed while radio ran: %d rows, was %d", got, rowsBefore)
	}
	if m.cursor != cursorBefore {
		t.Errorf("the cursor moved while radio ran: %d, was %d", m.cursor, cursorBefore)
	}
	if data.session.TrackID != realTrackID2 {
		t.Errorf("the radio track must play nonetheless, session at %q", data.session.TrackID)
	}
	// the drawer shows what radio lined up next
	m.drawerOpen = true
	got := strings.Join(lines(m), "\n")
	if !strings.Contains(stripANSI(got), "Blue in Green") {
		t.Error("the queue drawer must show what radio lined up while it is active")
	}
}

// TestRadioOffStopsAtEnd: with radio off the end-of-list behaviour stands —
// the boundary does not fire and nothing new plays.
func TestRadioOffStopsAtEnd(t *testing.T) {
	data := newRadioData()
	rp := &radioPlayer{data: data, next: []library.Track{
		{ID: realTrackID2, Title: "Freddie Freeloader", Duration: time.Minute},
	}}
	m := newRadioModel(t, 180, 50, data, rp)
	radioAtEnd(data)
	msg, fired := runRadioTick(t, m)
	if !fired {
		t.Fatal("the end-of-track boundary did not fire")
	}
	updated, cmd := m.Update(msg)
	*m = updated.(Model)
	runPlayerCmds(t, m, cmd)
	if len(rp.played) != 1 {
		t.Fatalf("the resolved track must play once, played %d", len(rp.played))
	}

	press(t, m, "R") // radio off again
	radioAtEnd(data)
	if _, fired := runRadioTick(t, m); fired {
		t.Fatal("with radio off the boundary must not fire")
	}
	if len(rp.played) != 1 {
		t.Errorf("with radio off nothing new may play, played %d", len(rp.played))
	}
}

// TestRadioResolveErrorToastsAndStops: a resolve failure toasts naming it and
// radio stops. It must not loop retrying.
func TestRadioResolveErrorToastsAndStops(t *testing.T) {
	data := newRadioData()
	rp := &radioPlayer{data: data, err: errors.New("autoplay rejected")}
	m := newRadioModel(t, 180, 50, data, rp)

	radioAtEnd(data)
	msg, fired := runRadioTick(t, m)
	if !fired {
		t.Fatal("the end-of-track boundary did not fire")
	}
	updated, cmd := m.Update(msg)
	*m = updated.(Model)
	runPlayerCmds(t, m, cmd)
	if len(rp.played) != 0 {
		t.Error("a failed resolve must never play anything")
	}
	if m.radioOn {
		t.Error("a failed resolve must stop radio")
	}
	if !strings.Contains(m.toast.text, "radio") || !strings.Contains(m.toast.text, "autoplay rejected") {
		t.Errorf("the toast must name the failure, got %q", m.toast.text)
	}

	// no retry loop: further ticks at the (still ended) track stay silent
	radioAtEnd(data)
	for i := 0; i < 3; i++ {
		if _, fired := runRadioTick(t, m); fired {
			t.Fatalf("tick %d: radio retried after its failure", i)
		}
	}
	if got := len(rp.calls); got != 1 {
		t.Errorf("the resolver must never be retried, called %d times", got)
	}
}

// TestRadioFiresWhenTrackStopsParkedAtEnd: the track stopping with radio on
// and nothing queued is the same boundary — the position sits at the
// duration even though the state says paused. A user pause lands mid-track
// and must never fire.
func TestRadioFiresWhenTrackStopsParkedAtEnd(t *testing.T) {
	data := newRadioData()
	rp := &radioPlayer{data: data, next: []library.Track{
		{ID: realTrackID2, Title: "Freddie Freeloader", Duration: time.Minute},
	}}
	m := newRadioModel(t, 180, 50, data, rp)
	// user pause mid-track: not an end (radio is on by default)
	data.session.State = library.StatePaused
	if _, fired := runRadioTick(t, m); fired {
		t.Fatal("a user pause mid-track must not fire the boundary")
	}
	// park the track at its end: the boundary fires
	radioAtEnd(data)
	msg, fired := runRadioTick(t, m)
	if !fired {
		t.Fatal("the track stopping parked at its end must fire the boundary")
	}
	updated, cmd := m.Update(msg)
	*m = updated.(Model)
	runPlayerCmds(t, m, cmd)
	if len(rp.played) != 1 {
		t.Errorf("the resolved track must play once, played %d", len(rp.played))
	}
}

// TestRadioOffNeverFires: with radio toggled off the same ended track does
// nothing — the normal end-of-list behaviour the toggle restores.
func TestRadioOffNeverFires(t *testing.T) {
	data := newRadioData()
	rp := &radioPlayer{data: data, next: []library.Track{
		{ID: realTrackID2, Title: "Freddie Freeloader", Duration: time.Minute},
	}}
	m := newRadioModel(t, 180, 50, data, rp)
	press(t, m, "R") // radio off
	radioAtEnd(data)
	if _, fired := runRadioTick(t, m); fired {
		t.Fatal("radio off must never fire the boundary")
	}
	if len(rp.calls) != 0 {
		t.Errorf("radio off must never call the resolver, called %d times", len(rp.calls))
	}
}

// TestNOnSingleTrackContinuesThroughResolver (D): the playback order holds a
// single track and it is playing — `n` does not dead-end: it resolves the
// related context from the current track, the same path the end-of-track
// boundary uses, and the first resolved track plays.
func TestNOnSingleTrackContinuesThroughResolver(t *testing.T) {
	data := newRadioData() // the fixture playlist holds exactly one track
	rp := &radioPlayer{data: data, next: []library.Track{
		{ID: realTrackID2, Title: "Freddie Freeloader", Duration: time.Minute},
	}}
	m := newRadioModel(t, 180, 50, data, rp)
	if got := len(trackIDs(m)); got != 1 {
		t.Fatalf("mock setup: single-track view, got %d rows", got)
	}
	updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("n")})
	*m = updated.(Model)
	msg, ok := runRadioCmd(t, cmd)
	if !ok {
		t.Fatal("`n` on a single-track view must continue through the resolver")
	}
	if len(rp.calls) != 1 || !strings.HasPrefix(rp.calls[0], "spotify:track:"+realTrackID+"|") {
		t.Fatalf("the resolve must be seeded from the current track, calls %v", rp.calls)
	}
	updated, cmd = m.Update(msg)
	*m = updated.(Model)
	runPlayerCmds(t, m, cmd)
	if len(rp.played) != 1 || rp.played[0].ID != realTrackID2 {
		t.Fatalf("the resolved track must play, played %v", rp.played)
	}
	if data.session.TrackID != realTrackID2 {
		t.Errorf("the session must mirror the radio track, got %q", data.session.TrackID)
	}
}

// TestNWithRadioOffToastsOnExhaustedOrder (D): with radio off and nothing
// left in the playback order, `n` says so in one toast and plays nothing.
func TestNWithRadioOffToastsOnExhaustedOrder(t *testing.T) {
	data := newRadioData() // single track: the order is exhausted right away
	rp := &radioPlayer{data: data}
	m := newRadioModel(t, 180, 50, data, rp)
	press(t, m, "R") // radio off
	press(t, m, "n")
	if len(rp.played) != 0 || len(rp.calls) != 0 {
		t.Fatalf("nothing may play or resolve with radio off, played %v, calls %v", rp.played, rp.calls)
	}
	if !strings.Contains(m.toast.text, "nothing left to play") {
		t.Errorf("the exhausted order must toast, got %q", m.toast.text)
	}
}

// TestPrevStopsAtTheStartWithAToast (D): `p` mirrors `n` backwards and stops
// at the start of the playback order with an honest toast.
func TestPrevStopsAtTheStartWithAToast(t *testing.T) {
	data := newRadioData()
	rp := &radioPlayer{data: data}
	m := newRadioModel(t, 180, 50, data, rp)
	press(t, m, "p")
	if len(rp.played) != 0 {
		t.Fatalf("`p` at the start must not play, played %v", rp.played)
	}
	if !strings.Contains(m.toast.text, "start") {
		t.Errorf("the toast must say the walk is at the start, got %q", m.toast.text)
	}
}

// R4-001: the state tick must survive a stale resolve. The boundary used to own
// the tick chain, so a radio toggle mid-flight (or any dropped answer) froze live
// progress and end-of-track detection until an unrelated keypress.
func TestRadioStaleResolveKeepsTheTickAlive(t *testing.T) {
	data := newRadioData()
	rp := &radioPlayer{data: data, next: []library.Track{
		{ID: realTrackID2, Title: "Freddie Freeloader", Duration: 100 * time.Second},
	}}
	m := newRadioModel(t, 180, 50, data, rp)
	radioAtEnd(data)

	msg, fired := runRadioTick(t, m)
	if !fired {
		t.Fatal("the boundary must fire before the resolve can go stale")
	}
	press(t, m, "R") // radio off while the answer is in flight
	updated, cmd := m.Update(msg)
	*m = updated.(Model)
	runPlayerCmds(t, m, cmd)

	updated, cmd = m.Update(tickMsg{})
	*m = updated.(Model)
	if cmd == nil {
		t.Fatal("the one-second tick died with the stale resolve")
	}
	if len(rp.played) != 0 {
		t.Errorf("a stale resolve must not play anything, played %d", len(rp.played))
	}
}

// R4-002: a resolve can take up to 30s. If the user starts something else in the
// meantime, the answer is stale and playing it would interrupt them.
func TestRadioStaleResolveDoesNotInterruptANewTrack(t *testing.T) {
	const otherID = "0VjIjW4GlUZAMYd2vXMi3b"
	data := newRadioData()
	rp := &radioPlayer{data: data, next: []library.Track{
		{ID: realTrackID2, Title: "Freddie Freeloader", Duration: 100 * time.Second},
	}}
	m := newRadioModel(t, 180, 50, data, rp)
	radioAtEnd(data)

	msg, fired := runRadioTick(t, m)
	if !fired {
		t.Fatal("the boundary must fire")
	}
	// the user starts another track before the answer lands
	if err := rp.Play(library.Track{ID: otherID, Title: "Something else"}); err != nil {
		t.Fatalf("manual play failed: %v", err)
	}
	updated, cmd := m.Update(msg)
	*m = updated.(Model)
	runPlayerCmds(t, m, cmd)

	if len(rp.played) != 1 || rp.played[0].ID != otherID {
		t.Fatalf("the stale resolve interrupted the user's track: played %v", rp.played)
	}
}

// R4-003: if the resolved track cannot play, radio must stop instead of claiming
// to be on with a queue that will never sound.
func TestRadioPlayFailureStopsRadio(t *testing.T) {
	data := newRadioData()
	rp := &radioPlayer{data: data, rejectedID: realTrackID2, next: []library.Track{
		{ID: realTrackID2, Title: "Freddie Freeloader", Duration: 100 * time.Second},
	}}
	m := newRadioModel(t, 180, 50, data, rp)
	radioAtEnd(data)

	msg, fired := runRadioTick(t, m)
	if !fired {
		t.Fatal("the boundary must fire")
	}
	updated, cmd := m.Update(msg)
	*m = updated.(Model)
	runPlayerCmds(t, m, cmd)

	if m.radioOn {
		t.Error("radio must turn off when the resolved track cannot play")
	}
	if len(m.radioUpNext) != 0 {
		t.Errorf("radio must not keep a queue it cannot play: %v", m.radioUpNext)
	}
	if !strings.Contains(m.toast.text, "radio") {
		t.Errorf("the failure must be named, toast = %q", m.toast.text)
	}
}

// TestMain shrinks every wall-clock timer the helpers drive: the live-state
// poll (the radio boundary needs consecutive ticks and a real second per tick
// dominated the suite), the 3s toast tick and the 250ms search debounce —
// the async-harness helpers run whatever a keypress returned, so every
// blocking tick must come back instantly.
func TestMain(m *testing.M) {
	tickInterval = time.Millisecond
	toastTimeout = time.Millisecond
	searchDebounce = time.Millisecond
	os.Exit(m.Run())
}

// TestRadioFiresWhenThePositionStopsShortOfTheEnd: Spotify's metadata duration and
// the decoded audio differ by a second or two. A real track ended at 3m34.292s of a
// 3m36.396s total and the position froze there, so requiring elapsed >= total never
// fired and radio looked dead. The end band plus a frozen position is the boundary.
func TestRadioFiresWhenThePositionStopsShortOfTheEnd(t *testing.T) {
	data := newRadioData()
	rp := &radioPlayer{data: data, next: []library.Track{
		{ID: realTrackID2, Title: "Freddie Freeloader", Duration: 100 * time.Second},
	}}
	m := newRadioModel(t, 180, 50, data, rp)

	radioAtEnd(data)
	data.session.Elapsed = data.session.Total - 2*time.Second // the real-world gap
	msg, fired := runRadioTick(t, m)
	if !fired {
		t.Fatal("a frozen position inside the end band must fire the boundary")
	}
	updated, cmd := m.Update(msg)
	*m = updated.(Model)
	runPlayerCmds(t, m, cmd)
	if len(rp.played) != 1 {
		t.Fatalf("the resolved track must play, played %d", len(rp.played))
	}
}
