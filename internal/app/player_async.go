package app

// The async player path: every player command Update used to call inline
// (m.player.Play, Enqueue, TogglePause, Next, Prev, VolumeUp/Down) now goes
// through the serialized playerQueue as a tea.Cmd, and its result — an error
// or the success confirmation — arrives back as a playerMsg. Update returns
// the command immediately and never touches the player on the UI thread; a
// network round trip can no longer freeze the frame.

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/enocsoft/spotui/internal/library"
)

// playerOp names which command a playerMsg answers, so Update can apply the
// same toast and side effects the inline call used to.
type playerOp uint8

const (
	opPlay        playerOp = iota // playRow: list rows, search results, playback walks
	opPlayContext                 // the :play context resolve's first playable track (no radio seed)
	opEnqueue
	opPause
	opVolumeUp
	opVolumeDown
	opRadioPlay // the radio boundary's resolved track; carries its latch id
)

// playerMsg carries one serialized player command's outcome into Update.
// Success effects (the toast, the radio seed) are applied here, at the same
// point in time the old inline call produced them — just off the UI thread.
type playerMsg struct {
	op      playerOp
	track   library.Track
	latchID string // opRadioPlay: the boundary track id the play was latched to
	err     error
}

// playerTask is one command handed to the serialized queue.
type playerTask struct {
	op      playerOp
	track   library.Track
	latchID string
	run     func() error
}

// playerCmd is the tea.Cmd the serialized player results ride in. A distinct
// named type so the test harness can pick it out of returned batches without
// running toast ticks. bubbletea calls it through the tea.Cmd interface, so
// the dynamic type is invisible to the runtime.
type playerCmd func() tea.Msg

// submitPlayer hands the task to the serialized queue and returns the
// command that carries its result back into Update. Submission is
// synchronous (the keypress order is the execution order) but never blocks
// and never calls the player.
func submitPlayer(q *playerQueue, task playerTask) tea.Cmd {
	if q == nil {
		q = &playerQueue{}
	}
	done := q.submit(task)
	return tea.Cmd(playerCmd(func() tea.Msg { return <-done }))
}

// submit jobs go through the model so the queue always exists.
func (m *Model) submitPlayer(task playerTask) tea.Cmd {
	return submitPlayer(m.pq, task)
}

// applyPlayerMsg lands a completed player command: errors keep today's toast
// naming them — now arriving as messages, not as a blocked frame — and the
// success side effects are the ones the inline calls used to do.
func (m *Model) applyPlayerMsg(msg playerMsg) tea.Cmd {
	switch msg.op {
	case opPlay:
		m.walkDone(msg.track.ID) // a shuffle walk that landed (or failed) is no longer pending
		if msg.err != nil {
			return m.setToast("✗ " + msg.err.Error())
		}
		m.lastPlayed = msg.track.ID
		m.notePlayed(msg.track.ID)
		return m.setToast("▶ " + playLabel(msg.track))
	case opPlayContext:
		if msg.err != nil {
			return m.setToast("✗ " + msg.err.Error())
		}
		m.lastPlayed = msg.track.ID
		return m.setToast("▶ " + playLabel(msg.track))
	case opEnqueue:
		if msg.err != nil {
			return m.setToast("✗ " + msg.err.Error())
		}
		return m.setToast("queued · " + msg.track.Title)
	case opPause:
		if msg.err != nil {
			return m.setToast("✗ " + msg.err.Error())
		}
		// The state read happens after the call, exactly where the inline
		// path read it.
		if m.data.Session().State == library.StatePaused {
			return m.setToast("paused")
		}
		return m.setToast("playing")
	case opVolumeUp, opVolumeDown:
		if msg.err != nil {
			return m.setToast("✗ " + msg.err.Error())
		}
		return m.setToast(fmt.Sprintf("volume %d%%", m.data.Session().Volume))
	case opRadioPlay:
		// A stale answer (radio was toggled off, this boundary is spent, or
		// another boundary won) is dropped, the same rule a stale resolve
		// follows.
		if !m.radioOn || msg.latchID == "" || m.radioFiredID != msg.latchID {
			return nil
		}
		if msg.err != nil {
			// Unlatch and stop: never "on" with a queue that will not sound.
			m.radioOn, m.radioUpNext, m.radioFiredID = false, nil, ""
			return m.setToast("✗ radio: " + msg.err.Error())
		}
		m.notePlayed(msg.track.ID)
		m.lastPlayed = msg.track.ID
		return m.setToast("▶ " + playLabel(msg.track))
	}
	return nil
}

// playLabel names what played: the track title, or its id when the label was
// never known (`:play` with a bare id).
func playLabel(t library.Track) string {
	if t.Title != "" {
		return t.Title
	}
	return t.ID
}
