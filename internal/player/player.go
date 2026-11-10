// Package player is the adapter that makes this TUI the actual audio player
// (spec S1, S15): it embeds a go-librespot session built with
// sessionconfig.NewSessionFromConfigDir and drives the go-librespot player,
// whose audio leaves this machine through a local PulseAudio sink (spec S16).
//
// Library content is real since T7: the UI fetches browse/search/library
// through Catalog(), driven over plain net/http against api.spotify.com with
// the library's own Web API token — a second login, separate from the
// playback one (two tokens, one purpose each). The content DataSource
// delegate remains only as the mock-mode backing store; session state,
// sinks, output switching and playback are real.
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

	"github.com/nahuel/spotify-tui/internal/auth"
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
	// ClientID is the dashboard client id from the XDG config. It drives the
	// library's own Web API login (two tokens, one purpose each): Web API
	// quota belongs to the dashboard app. It is never used for the playback
	// login: that flow authenticates as librespot's own client, the only one
	// whose tokens carry the `username` claim the access point requires.
	ClientID string
	// Output is the preferred sink id; empty means the PulseAudio default.
	Output string
}

// Player implements library.DataSource and library.Player on top of an
// embedded go-librespot session.
type Player struct {
	content library.DataSource
	opts    Options
	authCfg auth.Config
	baseCtx context.Context
	http    *http.Client

	// webCfg is the library's own login: the dashboard client id, the Web API
	// scope set and the webtoken.json cache — a second token that never
	// touches the session or the playback cache.
	webCfg auth.Config
	// webTokens supplies (and refreshes) that second token to the catalog.
	webTokens library.TokenSource
	// webBase overrides the Web API root for tests; empty = production.
	webBase string

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

// New builds the real player. content backs the mock-mode library lists; the
// real catalog flows through Catalog().
func New(opts Options, content library.DataSource) *Player {
	if opts.CallbackPort <= 0 {
		opts.CallbackPort = config.DefaultCallbackPort
	}
	return &Player{
		content: content,
		opts:    opts,
		authCfg: auth.Config{
			// librespot's own client id, never the dashboard one: only that
			// client's tokens carry the `username` claim the access point
			// needs, and the dashboard id replaces librespot's inside the
			// session and breaks it (L20, L24).
			ClientID:     librespot.ClientIdHex,
			CallbackPort: opts.CallbackPort,
			// ConfigDir is the XDG state dir: the token cache (spec S9)
			// lives next to the device id, never in the repository.
			StateDir: opts.ConfigDir,
		},
		webCfg: auth.Config{
			// The library's second login: the dashboard client id from
			// config, the Web API scope set, its own cache file. Same
			// loopback redirect, same state dir, never the same token.
			ClientID:     opts.ClientID,
			CallbackPort: opts.CallbackPort,
			StateDir:     opts.ConfigDir,
			CacheFile:    auth.WebTokenCacheFile,
			Scopes:       auth.WebAPIScopes,
		},
		http: &http.Client{Timeout: 30 * time.Second},
		webTokens: auth.NewWebAPITokenSource(auth.Config{
			ClientID:  opts.ClientID,
			StateDir:  opts.ConfigDir,
			CacheFile: auth.WebTokenCacheFile,
		}),
		libEvents: make(chan library.Event, 16),
		volCh:     make(chan float32, 1),
		sinkReq:   make(chan string, 1),
		sinkID:    opts.Output,
		sinkLabel: opts.Output,
		state:     library.StateStarting,
	}
}

// bootstrapTimeout bounds the session handshake: a hang must become an error
// with a log to read, not a frozen screen.
const loginTimeout = 10 * time.Minute

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

// run owns the login (spec S20), the session bootstrap and then pumps
// player events until the context dies.
func (p *Player) run() {
	log := newCaptureLogger(p.authCfg.StateDir, func(u string) {
		p.onAuthorizeURL(library.LoginPurposePlayback, u)
	})

	// The playback login is ours (spec S20): go-librespot's interactive
	// callback accepts ANY request on the port as the code carrier — a
	// favicon fetch, a tab restore or a reload without a code consumes the
	// one-shot channel and the exchange dies with "code must be supplied"
	// (reproduced: a code-less request kills the flow; a bogus code at least
	// reports invalid_grant). So we run the Authorization Code + PKCE flow
	// ourselves with librespot's client id over http://127.0.0.1:<port>/login
	// and hand the session the result as SpotifyTokenCredentials — exactly
	// the credentials the library's interactive branch derives after its own
	// exchange (ap.ConnectSpotifyToken with the username claim), so the
	// access point accepts them. The dashboard client id plays no role here.
	p.emit(library.Event{Type: library.EventConnecting})

	// Human-paced: the wait covers the browser consent, so the cap is generous. It
	// exists to turn a silent hang into a message, not to rush a login.
	bootCtx, cancel := context.WithTimeout(p.baseCtx, loginTimeout)
	defer cancel()

	authCfg := p.authCfg
	authCfg.Log = log.Infof
	tok, err := authCfg.EnsureToken(bootCtx, func(u string) {
		p.onAuthorizeURL(library.LoginPurposePlayback, u)
	})
	if err != nil {
		p.fail(err)
		return
	}

	// The library's second login: a separate PKCE flow with the dashboard
	// client id whose tokens are the only ones the Web API serves (the
	// playback token belongs to librespot's client id and is rate-limited
	// out of every catalog endpoint). Sequential, on the same callback
	// port, never at the same time; a cached token skips its consent.
	webCfg := p.webCfg
	webCfg.Log = log.Infof
	if _, err := webCfg.EnsureToken(bootCtx, func(u string) {
		p.onAuthorizeURL(library.LoginPurposeLibrary, u)
	}); err != nil {
		p.fail(fmt.Errorf("library login: %v", err))
		return
	}

	sess, _, bootErr := sessionconfig.NewSessionFromConfigDir(bootCtx, log, sessionOptions(p.opts, tok))
	if bootErr != nil {
		p.fail(bootErr)
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

// onAuthorizeURL surfaces the login URL the auth flow generated: the TUI shows
// it as a full-screen state (spec S9), naming which half of the app is
// asking — playback or the library — because two consents on one port must
// not be confusing.
func (p *Player) onAuthorizeURL(purpose, url string) {
	p.mu.Lock()
	p.loginURL = url
	p.mu.Unlock()
	file := ""
	if stateDir := p.authCfg.StateDir; stateDir != "" {
		if path, werr := auth.WriteLoginURL(stateDir, url); werr == nil {
			file = path
		}
	}
	p.emit(library.Event{Type: library.EventLogin, LoginURL: url, LoginFile: file, LoginPurpose: purpose})
}

// fail records a bootstrap failure and surfaces it to the UI.
func (p *Player) fail(err error) {
	p.mu.Lock()
	p.bootErr = err
	p.mu.Unlock()
	p.emit(library.Event{Type: library.EventFailed, Err: err})
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
		Log:            newCaptureLogger(p.authCfg.StateDir, nil),
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
// player country code for media restrictions. It never inspects product:
// the field no longer exists for Development Mode apps, so nothing here may
// claim anything about the subscription (spec S21); playback failures
// surface with their real causes instead.
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
}

// sessionOptions builds the bootstrap options once the playback token is in
// hand: librespot's own client id and the token as SpotifyTokenCredentials —
// the same credentials the library's interactive branch derives after its
// exchange, so the access point accepts them. No CallbackPort: the library
// never opens its own callback server on this path.
func sessionOptions(opts Options, tok auth.Token) sessionconfig.Options {
	return sessionconfig.Options{
		ConfigDir:  opts.ConfigDir,
		DeviceType: "computer",
		ClientId:   librespot.ClientIdHex,
		Credentials: session.SpotifyTokenCredentials{
			Username: tok.UserID,
			Token:    tok.AccessToken,
		},
	}
}
