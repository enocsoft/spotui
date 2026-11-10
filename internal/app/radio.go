package app

// Radio (T26): `R` toggles autoplay continuation — put on any track and keep
// going after it ends. Off by default. The player events carry no
// "track ended" signal (only Play/Resume/Pause/Stop/NotPlaying), so the
// boundary is derived from the existing one-second state tick: when the
// reported position reaches the duration, the track is over — the same read
// covers a track that stops parked at its end with nothing queued, because
// the position still sits at the duration. A user pause lands mid-track and
// never reaches it.
//
// On the boundary the injectable autoplay resolver asks Spotify for the next
// related context and the first playable result plays through the existing
// play path. Radio plays outside the view: the open list never changes, the
// drawer shows what radio has lined up while it is active. Failure is
// honest: a resolve that fails toasts naming it and radio stops — never a
// retry loop.

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nahuel/spotify-tui/internal/library"
)

// radioResolveTimeout bounds one autoplay resolve the UI drives.
const radioResolveTimeout = 30 * time.Second

// radioRecentCap bounds the recently played uris the model keeps.
const radioRecentCap = 20

// onRadio is `R`: toggle radio. Turning it off restores the normal
// end-of-list behaviour — the session stops when nothing is queued. No
// persistence between restarts, exactly like shuffle.
func (m *Model) onRadio() tea.Cmd {
	m.radioOn = !m.radioOn
	m.radioFiredID = ""
	if m.radioOn {
		m.radioUpNext = nil
		return m.setToast("radio on")
	}
	m.radioUpNext = nil
	return m.setToast("radio off")
}

// radioCheck runs on every state tick and decides whether the end-of-track
// boundary fires. It is intentionally cheap: one Session snapshot the tick
// already reads for the player bar plus a few comparisons. The latch
// (radioFiredID) fires the boundary at most once per track; the next track
// starts its own. Without the injectable resolver there is nothing to fire.
func (m *Model) radioCheck() tea.Cmd {
	if !m.radioOn || m.autoplay == nil {
		return nil
	}
	s := m.data.Session()
	if s.TrackID == "" || (s.State != library.StatePlaying && s.State != library.StatePaused) {
		return nil
	}
	if s.Total <= 0 || s.Elapsed < s.Total {
		return nil
	}
	if m.radioFiredID == s.TrackID {
		return nil // this track's boundary already fired
	}
	m.radioFiredID = s.TrackID
	return m.radioResolveCmd(s.TrackID)
}

// radioResolveCmd asks the resolver for the next related context: the
// blocking call runs inside the tea.Cmd goroutine, never in Update.
func (m Model) radioResolveCmd(trackID string) tea.Cmd {
	resolve := m.autoplay
	seed := "spotify:track:" + trackID
	recent := make([]string, len(m.radioRecent))
	copy(recent, m.radioRecent)
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), radioResolveTimeout)
		defer cancel()
		tracks, err := resolve.ResolveAutoplay(ctx, seed, recent)
		return radioResolvedMsg{trackID: trackID, tracks: tracks, err: err}
	}
}

// applyRadioResolved lands the resolve: the first playable result plays
// through the existing play path (which never touches the open list), the
// rest is what the drawer shows as lined up. A failure turns radio off with
// a toast that names it — one shot, never retried. Stale answers (radio was
// turned off, or another boundary won) are dropped.
func (m *Model) applyRadioResolved(msg radioResolvedMsg) tea.Cmd {
	if !m.radioOn || m.radioFiredID != msg.trackID {
		return nil
	}
	if msg.err != nil {
		m.radioOn, m.radioUpNext, m.radioFiredID = false, nil, ""
		return tea.Batch(tickCmd(), m.setToast("✗ radio: "+msg.err.Error()))
	}
	if len(msg.tracks) == 0 {
		m.radioOn, m.radioUpNext, m.radioFiredID = false, nil, ""
		return tea.Batch(tickCmd(), m.setToast("✗ radio: nothing related came back — radio stopped"))
	}
	next := msg.tracks[0]
	m.radioUpNext = msg.tracks[1:]
	cmd := m.playRow(next)
	return tea.Batch(tickCmd(), cmd)
}

// notePlayed records a successfully played track uri as radio seed history.
// Mock rows and non-track ids never reach it: ParseTrackID rejects them.
func (m *Model) notePlayed(id string) {
	plain, err := library.ParseTrackID(id)
	if err != nil {
		return
	}
	uri := "spotify:track:" + plain
	for _, u := range m.radioRecent {
		if u == uri {
			return
		}
	}
	m.radioRecent = append(m.radioRecent, uri)
	if len(m.radioRecent) > radioRecentCap {
		m.radioRecent = m.radioRecent[len(m.radioRecent)-radioRecentCap:]
	}
}
