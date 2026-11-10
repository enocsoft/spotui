// Pure mapping helpers between go-librespot values and the library view
// model. No network, no session: unit-testable on their own.
package player

import (
	"fmt"

	libplayer "github.com/elxgy/go-librespot/player"

	"github.com/nahuel/spotui/internal/library"
)

// bitrateKbps is the stream quality requested from Spotify (OGG Vorbis
// 160 kbps, librespot's default high quality).
const bitrateKbps = 160

// trackIDLen is the length of a base62 Spotify track id.
const trackIDLen = 22

// MapPlayerEvent maps a go-librespot player event to the library state the
// player bar mirrors. A dead output (EventTypeStop) becomes StateNoOutput so
// the bar offers `d` (spec S16).
func MapPlayerEvent(ev libplayer.EventType) library.State {
	switch ev {
	case libplayer.EventTypePlay, libplayer.EventTypeResume:
		return library.StatePlaying
	case libplayer.EventTypePause, libplayer.EventTypeNotPlaying:
		return library.StatePaused
	case libplayer.EventTypeStop:
		return library.StateNoOutput
	default:
		return library.StateStarting
	}
}

// VolumePercent converts a 0..1 volume fraction to the 0..100 integer the UI
// mirrors, clamped.
func VolumePercent(v float32) int {
	switch {
	case v <= 0:
		return 0
	case v >= 1:
		return 100
	default:
		return int(v*100 + 0.5)
	}
}

// BitrateLabel formats the stream bitrate for the player bar.
func BitrateLabel(kbps int) string { return fmt.Sprintf("%d kbps", kbps) }

// SinkLabel prefers the human-readable sink description and falls back to the
// raw sink id; the raw id is never a secret but should not be what the user
// reads when a proper name exists.
func SinkLabel(id, description string) string {
	if description != "" {
		return description
	}
	return id
}

// LoginCallbackURL is where the interactive-login callback lands (spec S12:
// loopback redirect with an IP literal, never localhost).
func LoginCallbackURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d/login", port)
}

// ParseSpotifyTrackID validates a track id for playback: a bare 22-char
// base62 track id or a full spotify:track: URI. The parse is domain logic
// shared with the palette (`:play`), so it lives in library.
func ParseSpotifyTrackID(id string) (string, error) {
	return library.ParseTrackID(id)
}

// SortSinks orders sinks for the picker: the active sink first, blocked
// sinks last, alphabetical in between. The input slice is not mutated.
func SortSinks(sinks []library.Sink) []library.Sink {
	out := make([]library.Sink, len(sinks))
	copy(out, sinks)
	weight := func(s library.Sink) int {
		switch {
		case s.Active:
			return 0
		case s.Blocked != "":
			return 2
		default:
			return 1
		}
	}
	// insertion sort keeps the ordering stable and the dependency set nil
	for i := 1; i < len(out); i++ {
		for j := i; j > 0; j-- {
			wj, wp := weight(out[j]), weight(out[j-1])
			if wj < wp || (wj == wp && out[j].Name < out[j-1].Name) {
				out[j], out[j-1] = out[j-1], out[j]
				continue
			}
			break
		}
	}
	return out
}
