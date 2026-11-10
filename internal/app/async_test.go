package app

// The player commands must never run on the UI thread: every media key
// (play, queue, space, n/p, volume) resolves metadata and talks to the
// access point, so a blocking call inside Update froze the whole frame for a
// network round trip. These tests pin the contract: Update returns a command
// immediately, the frame renders while the call is still in flight, the
// result arrives as a message, and rapid presses stay ordered through the
// serialized queue.

import (
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nahuel/spotify-tui/internal/config"
	"github.com/nahuel/spotify-tui/internal/library"
	"github.com/nahuel/spotify-tui/internal/library/mock"
)

// blockingPlayer is a library.Player whose Play blocks on release until the
// test lets go — the stand-in for a real metadata resolve.
type blockingPlayer struct {
	mu      sync.Mutex
	release chan struct{} // closed by the test to let Play return
	entered chan struct{} // closed once Play is entered
	calls   int
}

func newBlockingPlayer() *blockingPlayer {
	return &blockingPlayer{
		release: make(chan struct{}),
		entered: make(chan struct{}),
	}
}

func (p *blockingPlayer) Play(t library.Track) error {
	p.mu.Lock()
	p.calls++
	release := p.release
	first := p.calls == 1
	p.mu.Unlock()
	if first {
		close(p.entered)
		<-release
	}
	return nil
}
func (p *blockingPlayer) TogglePause() error            { return nil }
func (p *blockingPlayer) Next() error                   { return nil }
func (p *blockingPlayer) Prev() error                   { return nil }
func (p *blockingPlayer) Enqueue(t library.Track) error { return nil }
func (p *blockingPlayer) VolumeUp() error               { return nil }
func (p *blockingPlayer) VolumeDown() error             { return nil }

func (p *blockingPlayer) releasePlay() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.release != nil {
		close(p.release)
		p.release = nil
	}
}

// asyncModel builds a mock-mode model over a broken-out player.
func asyncModel(t *testing.T, player library.Player) Model {
	t.Helper()
	m := New(config.Config{ClientID: "test-client", CallbackPort: config.DefaultCallbackPort},
		mock.New(), player, testClock)
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 180, Height: 50})
	return updated.(Model)
}

// updateAsync runs one Update in its own goroutine and reports the result.
type updateResult struct {
	model Model
	cmd   tea.Cmd
}

func updateAsync(m Model, key string) <-chan updateResult {
	res := make(chan updateResult, 1)
	go func() {
		updated, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)})
		res <- updateResult{model: updated.(Model), cmd: cmd}
	}()
	return res
}

func waitUpdate(t *testing.T, ch <-chan updateResult) updateResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(2 * time.Second):
		t.Fatal("Update blocked on the player call — the frame froze")
		return updateResult{}
	}
}

// TestUpdateNeverBlocksOnThePlayer: `enter` on a track row must come back
// from Update with a command at once, the frame must still render while the
// play is in flight, and the toast must arrive afterwards as a message.
func TestUpdateNeverBlocksOnThePlayer(t *testing.T) {
	p := newBlockingPlayer()
	m := asyncModel(t, p)

	first := waitUpdate(t, updateAsync(m, "enter"))
	// The command must not run the call inline: nothing is in flight yet,
	// and a command came back for bubbletea to schedule.
	if first.cmd == nil {
		t.Fatal("enter must return a command for the player call")
	}

	// Run the command (what bubbletea does): the play enters and blocks.
	msgCh := make(chan tea.Msg, 1)
	go func() { msgCh <- first.cmd() }()
	select {
	case <-p.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("the command must start the player call")
	}

	// While the play is still in flight the whole frame must compose.
	if got := first.model.View(); got == "" || len(lines(&first.model)) < 50 {
		t.Fatalf("the frame must render while the play is in flight:\n%s", got)
	}

	// Let the play finish: the result arrives as a message, and applying it
	// toasts the success — today's behaviour, off the UI thread.
	p.releasePlay()
	select {
	case msg := <-msgCh:
		updated, _ := first.model.Update(msg)
		after := updated.(Model)
		if after.toast.text == "" {
			t.Errorf("the play toast must arrive through the message, got empty toast")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the play result never arrived as a message")
	}
}

// orderedPlayer records every player call in order and can block the first
// Next until the test releases it.
type orderedPlayer struct {
	mu      sync.Mutex
	order   []string
	release chan struct{} // non-nil: the first Next blocks until closed
}

func (p *orderedPlayer) record(what string) {
	p.mu.Lock()
	p.order = append(p.order, what)
	p.mu.Unlock()
}

func (p *orderedPlayer) calls() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.order...)
}

func (p *orderedPlayer) Play(t library.Track) error    { p.record("play " + t.ID); return nil }
func (p *orderedPlayer) TogglePause() error            { p.record("pause"); return nil }
func (p *orderedPlayer) Next() error                   { p.takeRelease(); p.record("next"); return nil }
func (p *orderedPlayer) Prev() error                   { p.record("prev"); return nil }
func (p *orderedPlayer) Enqueue(t library.Track) error { p.record("queue " + t.ID); return nil }
func (p *orderedPlayer) VolumeUp() error               { p.record("vol+"); return nil }
func (p *orderedPlayer) VolumeDown() error             { p.record("vol-"); return nil }

func (p *orderedPlayer) takeRelease() {
	p.mu.Lock()
	ch := p.release
	p.mu.Unlock()
	if ch != nil {
		<-ch
	}
}

func (p *orderedPlayer) unblockNext() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.release != nil {
		close(p.release)
		p.release = nil
	}
}

// TestTickCadenceFollowsTheSession: an idle session — paused or stopped, no
// radio — must not re-render the whole screen every second. Only a playing
// session (live progress) and radio (the boundary detector) keep the
// one-second poll.
func TestTickCadenceFollowsTheSession(t *testing.T) {
	data := newRadioData()
	rp := &radioPlayer{data: data}
	m := newRadioModel(t, 180, 50, data, rp)
	if got := m.tickIntervalFor(); got != tickInterval {
		t.Errorf("playing: tick = %v, want the live poll %v", got, tickInterval)
	}
	data.session.State = library.StatePaused
	if got := m.tickIntervalFor(); got != idleRefresh {
		t.Errorf("paused without radio: tick = %v, want the idle refresh %v", got, idleRefresh)
	}
	press(t, m, "R")
	if got := m.tickIntervalFor(); got != tickInterval {
		t.Errorf("radio on: tick = %v, want the boundary poll %v", got, tickInterval)
	}
}

// TestTwoRapidNextPressesStayOrdered: two `n` presses while the first skip is
// still in flight must apply as two ordered commands — never interleave into
// one garbled skip, never race each other. The first press comes back from
// Update immediately; the second, too; the fake player sees next; next — in
// exactly that order, even though the test runs the second command first.
func TestTwoRapidNextPressesStayOrdered(t *testing.T) {
	p := &orderedPlayer{release: make(chan struct{})}
	m := asyncModel(t, p)

	first := waitUpdate(t, updateAsync(m, "n"))
	second := waitUpdate(t, updateAsync(first.model, "n"))
	if first.cmd == nil || second.cmd == nil {
		t.Fatal("each press must return its own command")
	}

	run := func(cmd tea.Cmd) <-chan tea.Msg {
		ch := make(chan tea.Msg, 1)
		go func() {
			msg := cmd()
			if batch, is := msg.(tea.BatchMsg); is {
				for _, sub := range batch {
					if sub == nil {
						continue
					}
					msg = sub()
					break
				}
			}
			ch <- msg
		}()
		return ch
	}
	// Deliberately start the second command first: serialization, not
	// goroutine luck, must keep the order.
	secondMsg := run(second.cmd)
	firstMsg := run(first.cmd)

	p.unblockNext()
	var got [2]tea.Msg
	select {
	case got[0] = <-firstMsg:
	case <-time.After(2 * time.Second):
		t.Fatal("the first skip never completed")
	}
	select {
	case got[1] = <-secondMsg:
	case <-time.After(2 * time.Second):
		t.Fatal("the second skip never completed")
	}

	// The player saw exactly two skips, first press first.
	calls := p.calls()
	if len(calls) != 2 || calls[0] != "next" || calls[1] != "next" {
		t.Fatalf("two rapid `n` presses must apply as two ordered skips, got %v", calls)
	}
}
