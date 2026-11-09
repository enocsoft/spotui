// Package player is the adapter that makes this TUI the actual audio player
// (spec S1, S15): it embeds a go-librespot session built with
// sessionconfig.NewSessionFromConfigDir and drives the go-librespot player,
// whose audio leaves this machine through a local PulseAudio sink (spec S16).
//
// Library content (browse, search, playlists) is still served by a delegate
// DataSource until slice 3 (T7) wires the real catalog; every other
// DataSource aspect — session state, sinks, output switching — is real.
//
// Threading: the session bootstrap and the go-librespot player are owned by
// one goroutine (run). Public methods hand work over via channels or take a
// short-lived mutex; go-librespot player commands are themselves serialized
// through an internal command channel.
package player

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	librespot "github.com/elxgy/go-librespot"
	libplayer "github.com/elxgy/go-librespot/player"
	"github.com/elxgy/go-librespot/session"
	"github.com/elxgy/go-librespot/sessionconfig"

	"github.com/nahuel/spotify-tui/internal/config"
	"github.com/nahuel/spotify-tui/internal/library"
)

// audioBackend is the go-librespot output backend: pure-Go PulseAudio
// (PipeWire ships pipewire-pulse), the only backend verified on this
// machine (L11).
const audioBackend = "pulseaudio"

// Options configures the embedded session and audio output.
type Options struct {
	// ConfigDir is the XDG state dir holding the device id and the persisted
	// Spotify credentials (state.json, written 0600 by the session
	// bootstrap) — never the repository.
	ConfigDir string
	// CallbackPort is the interactive-login callback port (spec S12).
	CallbackPort int
	// ClientID is the Spotify app client id; empty uses the librespot default.
	ClientID string
	// Output is the preferred sink id; empty means the PulseAudio default.
	Output string
}

// Player implements library.DataSource and library.Player on top of an
// embedded go-librespot session.
type Player struct {
	content library.DataSource
	opts    Options
	baseCtx context.Context
	http    *http.Client

	libEvents chan library.Event
	volCh     chan float32 // external-volume updates from the output backend
	sinkReq   chan string  // sink switches requested by the picker

	mu        sync.Mutex
	sess      *session.Session
	live      *libplayer.Player
	liveCh    <-chan libplayer.Event
	state     library.State
	bitrate   string
	total     time.Duration
	volume    int
	current   library.Track
	hasCur    bool
	queue     []library.Track
	history   []library.Track
	sinks     []library.Sink // last PulseAudio listing (label -> id map)
	sinkID    string
	sinkLabel string
	loginURL  string
	bootErr   error
	ready     bool
	closing   bool
}

// New builds the real player. content is the delegate DataSource serving
// library lists (mock until slice 3).
func New(opts Options, content library.DataSource) *Player {
	if opts.CallbackPort <= 0 {
		opts.CallbackPort = config.DefaultCallbackPort
	}
	return &Player{
		content:   content,
		opts:      opts,
		http:      &http.Client{Timeout: 30 * time.Second},
		libEvents: make(chan library.Event, 16),
		volCh:     make(chan float32, 1),
		sinkReq:   make(chan string, 1),
		sinkID:    opts.Output,
		sinkLabel: opts.Output,
		state:     library.StateStarting,
	}
}

// Start launches the session bootstrap in the background. Progress arrives
// through Events() as EventLogin / EventReady / EventFailed.
func (p *Player) Start(ctx context.Context) {
	p.baseCtx = ctx
	go p.run()
}

// Close tears the embedded session down. Safe to call even when the
// bootstrap never completed.
func (p *Player) Close() {
	p.mu.Lock()
	live, sess := p.live, p.sess
	p.closing = true
	p.mu.Unlock()
	if live != nil {
		live.Close()
	}
	if sess != nil {
		sess.Close()
	}
}

// Events implements library.PlayerEvents.
func (p *Player) Events() <-chan library.Event { return p.libEvents }

// emit delivers an event to the UI. State-only events may be dropped when
// the UI is busy (the view re-reads Session() anyway); lifecycle events
// (login, ready, failed) are rare and must not be lost.
func (p *Player) emit(ev library.Event) {
	if ev.Type == library.EventState {
		select {
		case p.libEvents <- ev:
		default:
		}
		return
	}
	p.libEvents <- ev
}

// run owns the bootstrap and then pumps player events until the context dies.
func (p *Player) run() {
	log := newCaptureLogger(func(url string) {
		p.mu.Lock()
		p.loginURL = url
		p.mu.Unlock()
		p.emit(library.Event{Type: library.EventLogin, LoginURL: url})
	})

	sess, _, err := sessionconfig.NewSessionFromConfigDir(p.baseCtx, log, sessionconfig.Options{
		ConfigDir:    p.opts.ConfigDir,
		CallbackPort: p.opts.CallbackPort,
		DeviceType:   "computer",
		ClientId:     p.opts.ClientID,
		Credentials:  nil, // stored credentials, else interactive login
	})
	if err != nil {
		p.mu.Lock()
		p.bootErr = err
		p.mu.Unlock()
		p.emit(library.Event{Type: library.EventFailed, Err: err})
		return
	}

	p.mu.Lock()
	p.sess = sess
	p.mu.Unlock()

	live, err := p.newGoPlayer()
	if err != nil {
		p.mu.Lock()
		p.bootErr = err
		p.mu.Unlock()
		p.emit(library.Event{Type: library.EventFailed, Err: err})
		return
	}

	p.mu.Lock()
	p.live = live
	p.liveCh = live.Receive()
	p.loginURL = ""
	p.ready = true
	p.mu.Unlock()

	p.emit(library.Event{Type: library.EventReady})
	go p.checkAccount()

	for {
		select {
		case <-p.baseCtx.Done():
			return
		case v := <-p.volCh:
			p.mu.Lock()
			p.volume = VolumePercent(v)
			p.mu.Unlock()
			p.emit(library.Event{Type: library.EventState})
		case ev, ok := <-p.currentEventCh():
			if !ok {
				continue
			}
			p.handlePlayerEvent(ev)
		case id := <-p.sinkReq:
			p.rebuildPlayer(id)
		}
	}
}

// currentEventCh returns the event channel of the live player, or nil (which
// blocks forever in a select) while it is being rebuilt.
func (p *Player) currentEventCh() <-chan libplayer.Event {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.liveCh
}

// newGoPlayer builds the go-librespot player against the current session,
// pointing at the given sink (empty = PulseAudio default).
func (p *Player) newGoPlayer() (*libplayer.Player, error) {
	p.mu.Lock()
	sess, sinkID := p.sess, p.sinkID
	p.mu.Unlock()
	if sess == nil {
		return nil, errors.New("no embedded session")
	}
	// CountryCode must be non-nil: the player dereferences it on every
	// metadata resolve. It starts empty (no restriction filtering) and is
	// set from the account profile by checkAccount.
	return libplayer.NewPlayer(&libplayer.Options{
		Spclient:       sess.Spclient(),
		AudioKey:       sess.AudioKey(),
		Events:         sess.Events(),
		Log:            newCaptureLogger(nil),
		AudioBackend:   audioBackend,
		AudioDevice:    sinkID,
		ExternalVolume: true, // the PulseAudio backend always uses external volume
		VolumeUpdate:   p.volCh,
		CountryCode:    new(string),
	})
}

// handlePlayerEvent folds a go-librespot event into the mirrored state.
func (p *Player) handlePlayerEvent(ev libplayer.Event) {
	switch ev.Type {
	case libplayer.EventTypeNotPlaying:
		p.advanceOnEnd()
	default:
		p.mu.Lock()
		p.state = MapPlayerEvent(ev.Type)
		p.mu.Unlock()
		p.emit(library.Event{Type: library.EventState})
	}
}

// advanceOnEnd plays the next queued track when one is queued, otherwise
// parks the player paused at the end of the queue.
func (p *Player) advanceOnEnd() {
	p.mu.Lock()
	if len(p.queue) == 0 {
		p.state = library.StatePaused
		p.mu.Unlock()
		p.emit(library.Event{Type: library.EventState})
		return
	}
	next := p.queue[0]
	p.queue = p.queue[1:]
	p.mu.Unlock()
	if err := p.play(next, 0); err != nil {
		p.mu.Lock()
		p.state = library.StatePaused
		p.mu.Unlock()
		p.emit(library.Event{Type: library.EventState})
	}
}

// play loads a track into the live player at posMs. It must only be called
// when the player is ready.
func (p *Player) play(track library.Track, posMs int64) error {
	id, err := ParseSpotifyTrackID(track.ID)
	if err != nil {
		return fmt.Errorf("%s · mock rows cannot play until the real catalog lands", err)
	}
	p.mu.Lock()
	if !p.ready || p.closing {
		p.mu.Unlock()
		return errors.New("player not ready")
	}
	live := p.live
	prev, hasPrev := p.current, p.hasCur
	p.mu.Unlock()

	spotID, err := librespot.SpotifyIdFromUri("spotify:track:" + id)
	if err != nil {
		return fmt.Errorf("bad track id: %w", err)
	}
	streamCtx, cancel := context.WithTimeout(p.baseCtx, 30*time.Second)
	defer cancel()
	stream, err := live.NewStream(streamCtx, p.http, *spotID, bitrateKbps, posMs)
	if err != nil {
		return fmt.Errorf("loading track: %w", err)
	}
	if err := live.SetPrimaryStream(stream.Source, false, true); err != nil {
		_ = stream.Close()
		return fmt.Errorf("starting track: %w", err)
	}

	p.mu.Lock()
	if hasPrev {
		p.history = append(p.history, prev)
		if len(p.history) > 50 {
			p.history = p.history[len(p.history)-50:]
		}
	}
	p.current = track
	p.hasCur = true
	p.state = library.StatePlaying
	p.bitrate = BitrateLabel(libplayer.GetFormatBitrate(*stream.File.Format))
	p.total = time.Duration(stream.Media.Duration()) * time.Millisecond
	p.mu.Unlock()
	p.emit(library.Event{Type: library.EventState})
	return nil
}

// rebuildPlayer swaps the go-librespot player for one bound to the given
// sink, resuming the current track at its position, so switching the output
// moves the sound (spec S16).
func (p *Player) rebuildPlayer(sinkID string) {
	p.mu.Lock()
	if !p.ready || p.live == nil {
		p.mu.Unlock()
		return
	}
	live := p.live
	pos := int64(0)
	if p.hasCur {
		pos = live.PositionMs()
	}
	cur, hasCur := p.current, p.hasCur
	p.live, p.liveCh = nil, nil
	p.mu.Unlock()

	live.Close()

	np, err := p.newGoPlayer()
	if err != nil {
		p.mu.Lock()
		p.bootErr = fmt.Errorf("audio output: %w", err)
		p.mu.Unlock()
		p.emit(library.Event{Type: library.EventFailed, Err: p.bootErr})
		return
	}
	p.mu.Lock()
	p.live, p.liveCh = np, np.Receive()
	p.mu.Unlock()

	if hasCur {
		// best effort: if the reload fails the UI shows the stopped state
		_ = p.play(cur, pos)
	}
	p.emit(library.Event{Type: library.EventState})
}

// checkAccount reads the account profile once after login: it sets the
// player country code for media restrictions and rejects non-Premium
// accounts explicitly (librespot requires Premium, spec S13/S15).
func (p *Player) checkAccount() {
	p.mu.Lock()
	sess := p.sess
	p.mu.Unlock()
	if sess == nil {
		return
	}
	ctx, cancel := context.WithTimeout(p.baseCtx, 30*time.Second)
	defer cancel()
	body, status, err := sess.WebApiWith429RetryAndReadBody(ctx, "GET", "v1/me", nil, nil)
	if err != nil || status != 200 {
		return // leave restrictions off; playback itself will report failures
	}
	var me struct {
		Country string `json:"country"`
		Product string `json:"product"`
	}
	if json.Unmarshal(body, &me) != nil {
		return
	}
	p.mu.Lock()
	live := p.live
	p.mu.Unlock()
	if live != nil && len(me.Country) == 2 {
		live.SetCountryCode(me.Country)
	}
	if me.Product != "" && me.Product != "premium" {
		p.emit(library.Event{Type: library.EventFailed,
			Err: fmt.Errorf("premium required · this account is %q and librespot needs Spotify Premium", me.Product)})
	}
}
