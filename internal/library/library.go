// Package library defines the data model and the read/action interfaces the
// TUI consumes. Slice 1 backs them with mock data only (spec S10); the
// embedded go-librespot player arrives in slice 2 behind the same interfaces.
//
// Local-player model: the app IS the player. Audio leaves the local machine
// through an audio output sink (Speakers · PipeWire, HDMI, JACK); there are
// no remote Connect devices and no transfer-to-device step.
package library

import (
	"fmt"
	"strings"
	"time"
)

// Track is one row in a list: a playlist, search results or the queue.
type Track struct {
	ID       string
	Title    string
	Artist   string
	Album    string
	Duration time.Duration
	Added    string // short date label, e.g. "12 Nov"
	Saved    bool
}

// Section is a library group header: PLAYLISTS, ALBUMS, ARTISTS, GENRES.
type Section struct {
	Name  string
	Count int
}

// Playlist is a collection of tracks with its context-strip facts.
type Playlist struct {
	Name    string
	Owner   string
	Private bool
	Tracks  []Track
}

// Sink is one local audio output (PipeWire, PulseAudio, ALSA, JACK, HDMI...).
type Sink struct {
	Name    string // "Speakers · PipeWire", "HDMI / DisplayPort", "JACK"
	ID      string // sink identifier the player understands (PulseAudio sink name); empty for mock rows
	Active  bool
	Blocked string // non-empty when the sink cannot take audio right now
}

// State is the local player state the UI mirrors. The player owns progress
// and state; the UI never advances optimistically.
type State int

const (
	StatePlaying State = iota
	StatePaused
	// StateStarting is the brief go-librespot warm-up state. It replaces the
	// old "no active device" concept: local playback always has an owner.
	StateStarting
	// StateNoOutput means no usable audio sink was found; `d` opens the picker.
	StateNoOutput
)

// Session is the player session mirrored in the player bar.
type Session struct {
	State    State
	Output   string // active sink, e.g. "Speakers · PipeWire"
	Bitrate  string
	TrackID  string
	Title    string // now-playing text from the real player; empty for the mock (row lookup by TrackID)
	Artist   string
	Album    string
	Elapsed  time.Duration
	Total    time.Duration
	Volume   int // 0..100
	Shuffle  bool
	Repeat   string // "off", "track", "playlist"
	QueueLen int
}

// DataSource is everything the UI reads. Implemented by the mock in slice 1.
type DataSource interface {
	Library() []Section
	Playlist() Playlist
	Queue() []Track
	Session() Session
	Sinks() []Sink
	SetSaved(id string, saved bool) error
	SetSink(name string) error
}

// Player is every playback action. Each action is a local player command to
// the embedded go-librespot session (slice 2); there is no Web API request
// and no device transfer step.
type Player interface {
	Play(track Track) error
	TogglePause() error
	Next() error
	Prev() error
	Enqueue(track Track) error
	VolumeUp() error
	VolumeDown() error
}

// EventType is the kind of player notification carried by an Event.
type EventType uint8

const (
	// EventState means the session snapshot changed; re-read Session().
	EventState EventType = iota
	// EventLogin carries the interactive auth URL in LoginURL (spec S9).
	EventLogin
	// EventConnecting means the token is in hand and the session bootstrap is
	// running. Without it the UI sits on the login screen with no sign of life.
	EventConnecting

	// EventReady means the embedded player session is established.
	EventReady
	// EventFailed carries the bootstrap failure in Err.
	EventFailed
)

// Event is one player notification pushed to the UI loop. The UI mirrors
// state by re-reading Session(); it never advances progress optimistically.
type Event struct {
	Type EventType
	// LoginURL is the authorization URL the user must open.
	LoginURL string
	// LoginFile is where the same URL was written verbatim, because a long
	// authorization URL cannot be read or copied from a truncated screen line.
	LoginFile string
	Err       error
}

// PlayerEvents is implemented by players that push state updates. The TUI
// relays the channel into bubbletea messages.
type PlayerEvents interface {
	Events() <-chan Event
}

// trackIDLen is the length of a base62 Spotify track id.
const trackIDLen = 22

const base62 = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// ParseTrackID accepts a bare 22-char base62 Spotify track id or a full
// spotify:track: URI, stripping surrounding whitespace and quotes, and
// returns the bare id. Anything else (mock row ids, other Spotify object
// types) is rejected so it can never reach the player (T13, spec S1).
func ParseTrackID(target string) (string, error) {
	t := strings.TrimSpace(target)
	t = strings.Trim(t, "\"'`")
	t = strings.TrimSpace(t)
	t = strings.TrimPrefix(t, "spotify:track:")
	if len(t) != trackIDLen || strings.Trim(t, base62) != "" {
		return "", fmt.Errorf("not a Spotify track id: %q", target)
	}
	return t, nil
}
