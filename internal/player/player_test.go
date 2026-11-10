package player

import (
	"bytes"
	"errors"
	"strings"
	"testing"

	libplayer "github.com/elxgy/go-librespot/player"
	"github.com/nahuel/spotify-tui/internal/config"
	"github.com/nahuel/spotify-tui/internal/library"
)

func TestSortSinksActiveFirstBlockedLast(t *testing.T) {
	got := SortSinks([]library.Sink{
		{Name: "Zeta", ID: "z"},
		{Name: "Speakers", ID: "sp", Active: true},
		{Name: "BT Headset", ID: "bt", Blocked: "not connected"},
		{Name: "Alpha", ID: "a"},
	})
	want := []string{"Speakers", "Alpha", "Zeta", "BT Headset"}
	if len(got) != len(want) {
		t.Fatalf("SortSinks returned %d sinks, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Name != want[i] {
			t.Errorf("SortSinks[%d] = %q, want %q", i, got[i].Name, want[i])
		}
	}
}

func TestSortSinksActiveWinsOverBlocked(t *testing.T) {
	got := SortSinks([]library.Sink{
		{Name: "Zeta", ID: "z", Blocked: "not connected"},
		{Name: "Active", ID: "a", Active: true},
	})
	if got[0].Name != "Active" {
		t.Errorf("SortSinks[0] = %q, want the active sink first", got[0].Name)
	}
}

func TestSortSinksDoesNotMutateInput(t *testing.T) {
	in := []library.Sink{{Name: "B", ID: "b"}, {Name: "A", ID: "a", Active: true}}
	_ = SortSinks(in)
	if in[0].Name != "B" {
		t.Errorf("SortSinks mutated its input: %v", in)
	}
}

func TestParseSpotifyTrackID(t *testing.T) {
	id := "4uLU6hMCjMI75M1A2tKUQC"
	if got, err := ParseSpotifyTrackID(id); err != nil || got != id {
		t.Errorf("ParseSpotifyTrackID(bare) = %q, %v; want %q, nil", got, err, id)
	}
	uri := "spotify:track:" + id
	if got, err := ParseSpotifyTrackID(uri); err != nil || got != id {
		t.Errorf("ParseSpotifyTrackID(uri) = %q, %v; want %q, nil", got, err, id)
	}
	for _, bad := range []string{"", "t01", "short", "spotify:album:" + id, "spotify:track:with:extra:" + id} {
		if _, err := ParseSpotifyTrackID(bad); err == nil {
			t.Errorf("ParseSpotifyTrackID(%q): expected error, got nil", bad)
		}
	}
}

func TestMapPlayerEvent(t *testing.T) {
	cases := map[libplayer.EventType]library.State{
		libplayer.EventTypePlay:       library.StatePlaying,
		libplayer.EventTypeResume:     library.StatePlaying,
		libplayer.EventTypePause:      library.StatePaused,
		libplayer.EventTypeStop:       library.StateNoOutput,
		libplayer.EventTypeNotPlaying: library.StatePaused,
	}
	for ev, want := range cases {
		if got := MapPlayerEvent(ev); got != want {
			t.Errorf("MapPlayerEvent(%v) = %v, want %v", ev, got, want)
		}
	}
}

func TestVolumePercent(t *testing.T) {
	cases := []struct {
		in   float32
		want int
	}{
		{0, 0},
		{0.72, 72},
		{1, 100},
		{1.5, 100},
		{-0.2, 0},
		{0.5, 50},
	}
	for _, c := range cases {
		if got := VolumePercent(c.in); got != c.want {
			t.Errorf("VolumePercent(%v) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestBitrateLabel(t *testing.T) {
	if got := BitrateLabel(320); got != "320 kbps" {
		t.Errorf("BitrateLabel(320) = %q, want %q", got, "320 kbps")
	}
}

func TestSinkLabelPrefersDescription(t *testing.T) {
	if got := SinkLabel("alsa_output.pci.analog-stereo", "Built-in Audio Analog Stereo"); got != "Built-in Audio Analog Stereo" {
		t.Errorf("SinkLabel with description = %q, want the description", got)
	}
	if got := SinkLabel("alsa_output.pci.analog-stereo", ""); got != "alsa_output.pci.analog-stereo" {
		t.Errorf("SinkLabel without description = %q, want the id", got)
	}
}

func TestLoginCallbackURL(t *testing.T) {
	if got := LoginCallbackURL(8888); got != "http://127.0.0.1:8888/login" {
		t.Errorf("LoginCallbackURL(8888) = %q", got)
	}
}

// TestNewBuildsAuthConfig: the player threads its options into the login
// flow it now owns (spec S20) — client id, callback port and the state dir
// for the 0600 token cache (spec S9).
func TestNewBuildsAuthConfig(t *testing.T) {
	p := New(Options{ConfigDir: "/state/dir", CallbackPort: 9001, ClientID: "cid"}, nil)
	if p.authCfg.ClientID != "cid" || p.authCfg.CallbackPort != 9001 || p.authCfg.StateDir != "/state/dir" {
		t.Errorf("auth config = %+v, want client cid, port 9001, state /state/dir", p.authCfg)
	}
	if got := p.authCfg.RedirectURI(); got != "http://127.0.0.1:9001/login" {
		t.Errorf("auth redirect URI = %q", got)
	}
	p = New(Options{ConfigDir: "/state/dir"}, nil)
	if p.authCfg.CallbackPort != config.DefaultCallbackPort {
		t.Errorf("default callback port = %d, want %d", p.authCfg.CallbackPort, config.DefaultCallbackPort)
	}
}

func TestSessionClientIDs(t *testing.T) {
	if got := sessionClientIDs("abc123"); len(got) != 2 || got[0] != "abc123" || got[1] != "" {
		t.Errorf("configured id should be tried first, then librespot default: %v", got)
	}
	if got := sessionClientIDs(""); len(got) != 1 || got[0] != "" {
		t.Errorf("empty config must not duplicate the default id: %v", got)
	}
}

func TestIsClientTokenRejection(t *testing.T) {
	real := errors.New("failed obtaining client token: clienttoken exchange rejected: status=400")
	if !isClientTokenRejection(real) {
		t.Errorf("the real bootstrap failure must be recognised as a client-token rejection")
	}
	if isClientTokenRejection(errors.New("connection refused")) {
		t.Errorf("unrelated failures must not trigger the client id fallback")
	}
	if isClientTokenRejection(nil) {
		t.Errorf("nil must not be a rejection")
	}
}

func TestLoggerRedactsSecrets(t *testing.T) {
	var buf bytes.Buffer
	l := &captureLogger{w: &buf}
	l.Infof("obtained new client token: %s", "AAHQPSce8ZzZ4AT0cxABTXPNxJr6Dv/k994uXMc8Z/dod+ubpVbei90shAsaPq1CPby5KknzU3d9Z9BCHZry8mrNUvxcoWIfTaH/FmZYyZ")
	got := buf.String()
	if strings.Contains(got, "AAHQPSce8ZzZ") {
		t.Errorf("the log leaked a client token: %s", got)
	}
	if !strings.Contains(got, "<redacted>") {
		t.Errorf("expected a redaction marker, got: %s", got)
	}
	if !strings.Contains(got, "obtained new client token") {
		t.Errorf("the message itself must survive redaction: %s", got)
	}
}

// TestLoginURLIsHandedOverWhole: redaction must not reach the authorization URL.
// It is a challenge and a state, not a token, and a redacted URL cannot be opened.
func TestLoginURLIsHandedOverWhole(t *testing.T) {
	const url = "https://accounts.spotify.com/authorize?client_id=0123456789abcdef0123456789abcdef&code_challenge=Q2xhc3NpYy1jaGFsbGVuZ2UtdmFsdWUtMTIzNDU2Nzg5&code_challenge_method=S256&redirect_uri=http%3A%2F%2F127.0.0.1%3A8888%2Flogin&response_type=code&scope=streaming&state=UAtdFGUIBz1Nhmqp5lV-L6lP8Y9sWoI"
	var got string
	var buf bytes.Buffer
	l := &captureLogger{w: &buf, onLogin: func(u string) { got = u }}
	l.Infof("to complete authentication visit the following link: %s", url)
	if got != url {
		t.Errorf("the login URL must reach the UI whole:\n got %q\nwant %q", got, url)
	}
	if strings.Contains(buf.String(), "0123456789abcdef") {
		t.Errorf("the log file must still redact the URL: %s", buf.String())
	}
}
