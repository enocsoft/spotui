package player

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	librespot "github.com/elxgy/go-librespot"
	libplayer "github.com/elxgy/go-librespot/player"
	"github.com/elxgy/go-librespot/session"
	"github.com/nahuel/spotify-tui/internal/auth"
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
// flow it now owns (spec S20) — librespot's own client id (the only one whose
// token carries the `username` claim the access point needs), the callback
// port and the state dir for the 0600 token cache (spec S9). The dashboard
// id from config is never used for playback.
func TestNewBuildsAuthConfig(t *testing.T) {
	p := New(Options{ConfigDir: "/state/dir", CallbackPort: 9001, ClientID: "dashboard-cid"}, nil)
	if p.authCfg.ClientID != librespot.ClientIdHex {
		t.Errorf("auth client id = %q, want librespot's own %q", p.authCfg.ClientID, librespot.ClientIdHex)
	}
	if p.authCfg.CallbackPort != 9001 || p.authCfg.StateDir != "/state/dir" {
		t.Errorf("auth config = %+v, want port 9001, state /state/dir", p.authCfg)
	}
	if got := p.authCfg.RedirectURI(); got != "http://127.0.0.1:9001/login" {
		t.Errorf("auth redirect URI = %q", got)
	}
	p = New(Options{ConfigDir: "/state/dir"}, nil)
	if p.authCfg.CallbackPort != config.DefaultCallbackPort {
		t.Errorf("default callback port = %d, want %d", p.authCfg.CallbackPort, config.DefaultCallbackPort)
	}
}

// TestSessionOptionsUsesLibrespotTokenCredentials: the bootstrap must carry
// librespot's own client id and the token as SpotifyTokenCredentials — the
// same credentials the library's interactive branch derives after its own
// exchange — and must never delegate the login to InteractiveCredentials,
// whose callback server consumes the code channel on any request.
func TestSessionOptionsUsesLibrespotTokenCredentials(t *testing.T) {
	got := sessionOptions(Options{ConfigDir: "/state/dir"}, auth.Token{AccessToken: "at", UserID: "user-1"})
	if got.ClientId != librespot.ClientIdHex {
		t.Errorf("session client id = %q, want librespot's own %q", got.ClientId, librespot.ClientIdHex)
	}
	if got.ConfigDir != "/state/dir" || got.DeviceType != "computer" {
		t.Errorf("session options = %+v, want the config dir and device type kept", got)
	}
	if got.CallbackPort != 0 {
		t.Errorf("session CallbackPort = %d, want unset: the library must not open its own callback", got.CallbackPort)
	}
	creds, ok := got.Credentials.(session.SpotifyTokenCredentials)
	if !ok {
		t.Fatalf("credentials type = %T, want session.SpotifyTokenCredentials", got.Credentials)
	}
	if creds.Username != "user-1" || creds.Token != "at" {
		t.Errorf("credentials = %+v, want username user-1 with the access token", creds)
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

// TestCatalogNeedsTheSession: the catalog needs the library's own token (the
// second login); with nothing cached every call fails with one clear error
// the UI renders, never a crash and never a playback-token call.
func TestCatalogNeedsTheSession(t *testing.T) {
	p := New(Options{}, nil)
	fetch := p.Catalog()
	if fetch == nil {
		t.Fatal("the player must expose the catalog even before login: the UI renders its states")
	}
	if _, err := fetch.Playlists(context.Background(), 0); err == nil ||
		!strings.Contains(err.Error(), "not logged in") {
		t.Errorf("Playlists without a session = %v, want the not-logged-in error", err)
	}
}

// TestQueueDropsTheDelegateFixtureOnceLive: slice 1 filled the drawer with the
// delegate's 41 mock rows; with the real catalog (T7) the queue is only what
// the user enqueued, before and after login — fixture rows must never leak
// into the real drawer.
func TestQueueDropsTheDelegateFixtureOnceLive(t *testing.T) {
	p := New(Options{}, fixtureContent{})
	if got := p.Queue(); len(got) != 1 {
		t.Fatalf("before login queue = %d rows, want the 1 delegate row", len(got))
	}
	p.mu.Lock()
	p.ready = true
	p.mu.Unlock()
	if got := p.Queue(); len(got) != 0 {
		t.Errorf("after login queue = %d rows, want 0 — the fixture queue must not leak", len(got))
	}
}

// fixtureContent is a minimal library.DataSource for the tests above.
type fixtureContent struct{}

func (fixtureContent) Library() []library.Section { return nil }
func (fixtureContent) Playlist() library.Playlist {
	return library.Playlist{Tracks: []library.Track{{ID: "fixture"}}}
}
func (fixtureContent) Queue() []library.Track      { return []library.Track{{ID: "fixture"}} }
func (fixtureContent) Session() library.Session    { return library.Session{} }
func (fixtureContent) Sinks() []library.Sink       { return nil }
func (fixtureContent) SetSaved(string, bool) error { return nil }
func (fixtureContent) SetSink(string) error        { return nil }

// TestNewBuildsTheWebAPIAuthConfig: the library login is a second, separate
// PKCE flow: the dashboard client id from config, the Web API scope set, the
// same loopback callback port and its own cache file — the two tokens are
// never each other's.
func TestNewBuildsTheWebAPIAuthConfig(t *testing.T) {
	p := New(Options{ConfigDir: "/state/dir", CallbackPort: 9001, ClientID: "dashboard-cid"}, nil)
	if p.webCfg.ClientID != "dashboard-cid" {
		t.Errorf("web auth client id = %q, want the dashboard id from config", p.webCfg.ClientID)
	}
	if p.webCfg.Scopes != auth.WebAPIScopes {
		t.Errorf("web auth scopes = %q, want the Web API set %q", p.webCfg.Scopes, auth.WebAPIScopes)
	}
	if p.webCfg.CacheFile != "webtoken.json" {
		t.Errorf("web cache file = %q, want webtoken.json — token.json is the playback token's", p.webCfg.CacheFile)
	}
	if p.webCfg.CallbackPort != 9001 || p.webCfg.StateDir != "/state/dir" {
		t.Errorf("web auth config = %+v, want the same port and state dir", p.webCfg)
	}
	if got := p.webCfg.RedirectURI(); got != "http://127.0.0.1:9001/login" {
		t.Errorf("web redirect URI = %q, want the same loopback redirect", got)
	}
}

// stubWebTokens is a library.TokenSource fixture: it hands out one fixed
// token, the one the Web API must carry.
type stubWebTokens struct{ token string }

func (s stubWebTokens) AccessToken(context.Context) (string, error) { return s.token, nil }
func (s stubWebTokens) Refresh(context.Context) error               { return nil }

// TestCatalogSendsTheWebAPITokenNeverTheSessionToken: the catalog talks to
// api.spotify.com over plain HTTP with the library's own token; the playback
// session token must never ride along.
func TestCatalogSendsTheWebAPITokenNeverTheSessionToken(t *testing.T) {
	var auths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auths = append(auths, r.Header.Get("Authorization"))
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"total":0,"offset":0,"next":null,"items":[]}`))
	}))
	defer srv.Close()

	p := New(Options{ConfigDir: t.TempDir(), ClientID: "dashboard-cid"}, nil)
	p.webTokens = stubWebTokens{token: "web-api-token"}
	p.webBase = srv.URL
	if _, err := p.Catalog().Playlists(context.Background(), 0); err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if len(auths) != 1 {
		t.Fatalf("made %d requests, want 1", len(auths))
	}
	if auths[0] != "Bearer web-api-token" {
		t.Errorf("catalog Authorization = %q, want exactly the Web API token", auths[0])
	}
	if strings.Contains(auths[0], "session-token") {
		t.Errorf("the playback session token leaked into a catalog request: %q", auths[0])
	}
}

// TestCatalogNeedsTheWebAPITokenBeforeLogin: with no library token cached the
// catalog fails with the not-logged-in error the UI renders.
func TestCatalogNeedsTheWebAPITokenBeforeLogin(t *testing.T) {
	p := New(Options{ConfigDir: t.TempDir(), ClientID: "dashboard-cid"}, nil)
	_, err := p.Catalog().Playlists(context.Background(), 0)
	if err == nil || !strings.Contains(err.Error(), "not logged in") {
		t.Errorf("Playlists before the library login = %v, want the not-logged-in error", err)
	}
}
