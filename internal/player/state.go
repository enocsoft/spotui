// library.DataSource and library.Player implementation on the embedded
// session. Library lists read from the content DataSource in mock mode; the
// real catalog is fetched through Catalog() (T7). Session state, sinks and
// every playback action are real.
package player

import (
	"errors"
	"fmt"
	"time"

	libplayer "github.com/elxgy/go-librespot/player"

	"github.com/nahuel/spotui/internal/library"
)

// libplayerMaxVolume is go-librespot's 0..65535 volume scale.
const libplayerMaxVolume = libplayer.MaxStateVolume

// volumeStep is the percent one volume keypress changes.
const volumeStep = 5

// --- library.DataSource ---

// Library serves the mock-mode library sections from the content DataSource;
// the real playlists panel reads catalog state instead (T7).
func (p *Player) Library() []library.Section { return p.content.Library() }

// Playlist serves the mock-mode focused playlist from the content DataSource;
// real drill-downs read catalog state instead (T7).
func (p *Player) Playlist() library.Playlist { return p.content.Playlist() }

// Queue returns the real play queue: what `a` enqueued and what played next
// from it. The slice-1 delegate fixture queue is appended only while the
// embedded session is not live — once the real catalog owns the lists, its
// 41 fixture rows must never leak into the drawer (T7).
func (p *Player) Queue() []library.Track {
	p.mu.Lock()
	q := make([]library.Track, len(p.queue))
	copy(q, p.queue)
	live := p.ready
	p.mu.Unlock()
	if live {
		return q
	}
	return append(q, p.content.Queue()...)
}

// SetSaved serves the mock-mode saved toggle; the real toggle goes through
// library.Fetch.SetSaved (generic PUT /v1/library · DELETE /me/library, T7).
func (p *Player) SetSaved(id string, saved bool) error { return p.content.SetSaved(id, saved) }

// Sinks lists the real audio outputs of this machine (spec S16), sorted for
// the picker: active first, blocked last. The listing is also the label ->
// id map used by SetSink.
func (p *Player) Sinks() []library.Sink {
	list, defaultID, err := listPulseSinks()
	p.mu.Lock()
	defer p.mu.Unlock()
	if err != nil || len(list) == 0 {
		return []library.Sink{{Name: "no audio output", Blocked: "pulseaudio unavailable"}}
	}
	p.sinks = list
	for _, sk := range list {
		if sk.ID == p.sinkID {
			p.sinkLabel = sk.Name
		}
	}
	out := make([]library.Sink, len(list))
	for i, sk := range list {
		out[i] = sk
		out[i].Active = sk.ID == p.sinkID || (p.sinkID == "" && sk.ID == defaultID)
	}
	return SortSinks(out)
}

// SetSink switches the audio output. Before the session is ready the choice
// is stored and applied at bootstrap; afterwards the player is rebuilt on
// the new sink and the current track resumes where it was.
func (p *Player) SetSink(label string) error {
	p.mu.Lock()
	id := p.lookupSinkLocked(label)
	if id == "" {
		p.mu.Unlock()
		return fmt.Errorf("no audio output %q", label)
	}
	p.sinkID, p.sinkLabel = id, label
	if !p.ready {
		p.mu.Unlock()
		return nil
	}
	p.mu.Unlock()

	select {
	case p.sinkReq <- id:
	default:
	}
	return nil
}

// lookupSinkLocked resolves a picker label or a raw sink id against the last
// PulseAudio listing.
func (p *Player) lookupSinkLocked(label string) string {
	for _, sk := range p.sinks {
		if sk.Name == label || sk.ID == label {
			return sk.ID
		}
	}
	return ""
}

// Session builds the snapshot the player bar mirrors. Position is read live
// from the go-librespot player (Mirror Rule: the UI never advances it).
func (p *Player) Session() library.Session {
	p.mu.Lock()
	s := library.Session{
		State:    p.state,
		Output:   p.sinkLabel,
		Bitrate:  p.bitrate,
		Volume:   p.volume,
		QueueLen: len(p.queue),
		Repeat:   "off",
	}
	if p.hasCur {
		s.TrackID = p.current.ID
		s.Title = p.current.Title
		s.Artist = p.current.Artist
		s.Album = p.current.Album
		s.Total = p.current.Duration
	}
	if p.total > 0 {
		s.Total = p.total
	}
	live, hasCur, ready := p.live, p.hasCur, p.ready
	p.mu.Unlock()

	if ready && live != nil && hasCur {
		s.Elapsed = time.Duration(live.PositionMs()) * time.Millisecond
	}
	return s
}

// --- library.Player ---

// Play starts a track. The track id must be a real Spotify track id; mock
// rows are rejected with an actionable error (real rows carry their ids).
func (p *Player) Play(track library.Track) error {
	p.mu.Lock()
	ready, bootErr := p.ready, p.bootErr
	p.mu.Unlock()
	switch {
	case bootErr != nil:
		return bootErr
	case !ready:
		return errors.New("player not ready · waiting for login")
	}
	return p.play(track, 0)
}

// TogglePause pauses/resumes the live stream.
func (p *Player) TogglePause() error {
	p.mu.Lock()
	live, state, ready := p.live, p.state, p.ready
	p.mu.Unlock()
	if !ready || live == nil {
		return errors.New("player not ready · waiting for login")
	}
	if state == library.StatePlaying {
		return live.Pause()
	}
	return live.Play()
}

// Next plays the first queued track.
func (p *Player) Next() error {
	p.mu.Lock()
	if !p.hasCur {
		p.mu.Unlock()
		return errors.New("nothing playing")
	}
	if len(p.queue) == 0 {
		p.mu.Unlock()
		return errors.New("no next track · a enqueues")
	}
	next := p.queue[0]
	p.queue = p.queue[1:]
	p.mu.Unlock()
	return p.play(next, 0)
}

// Prev replays the previous track from history.
func (p *Player) Prev() error {
	p.mu.Lock()
	if len(p.history) == 0 {
		p.mu.Unlock()
		return errors.New("no previous track")
	}
	prev := p.history[len(p.history)-1]
	p.history = p.history[:len(p.history)-1]
	p.mu.Unlock()
	return p.play(prev, 0)
}

// Enqueue appends a track to the real play queue.
func (p *Player) Enqueue(track library.Track) error {
	p.mu.Lock()
	p.queue = append(p.queue, track)
	p.mu.Unlock()
	p.emit(library.Event{Type: library.EventState})
	return nil
}

// VolumeUp raises the volume by one step.
func (p *Player) VolumeUp() error { return p.volumeDelta(volumeStep) }

// VolumeDown lowers the volume by one step.
func (p *Player) VolumeDown() error { return p.volumeDelta(-volumeStep) }

func (p *Player) volumeDelta(d int) error {
	p.mu.Lock()
	if !p.ready || p.live == nil {
		p.mu.Unlock()
		return errors.New("player not ready · waiting for login")
	}
	v := p.volume + d
	if v < 0 {
		v = 0
	}
	if v > 100 {
		v = 100
	}
	p.volume = v
	live := p.live
	p.mu.Unlock()
	live.SetVolume(uint32(v) * libplayerMaxVolume / 100)
	p.emit(library.Event{Type: library.EventState})
	return nil
}
