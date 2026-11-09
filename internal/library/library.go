// Package library defines the data model and the read/action interfaces the
// TUI consumes. Slice 1 backs them with mock data only (spec S10); the
// embedded go-librespot player arrives in slice 2 behind the same interfaces.
//
// Local-player model: the app IS the player. Audio leaves the local machine
// through an audio output sink (Speakers · PipeWire, HDMI, JACK); there are
// no remote Connect devices and no transfer-to-device step.
package library

import "time"

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
}
