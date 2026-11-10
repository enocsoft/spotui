// Tests for the library (Web API) token source: a second, separate login
// whose token is served to the Web API catalog, refreshed proactively and
// force-refreshed once after a 401. Offline: the token endpoint is an
// httptest server and the cache lives in a temp dir; no credentials, no
// network beyond the loopback test server.
package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestWebAPITokenSourceWithoutToken: with nothing cached the source says the
// app is not logged in — the catalog renders it as a state, never a crash.
func TestWebAPITokenSourceWithoutToken(t *testing.T) {
	src := NewWebAPITokenSource(Config{StateDir: t.TempDir()})
	if _, err := src.AccessToken(context.Background()); err == nil ||
		!strings.Contains(err.Error(), "not logged in") {
		t.Errorf("AccessToken with no cache = %v, want a not-logged-in error", err)
	}
}

// TestWebAPITokenSourcesTheWebAPICache: the source reads the library's own
// cache file and never the playback token.json.
func TestWebAPITokenSourcesTheWebAPICache(t *testing.T) {
	dir := t.TempDir()
	playback := Config{StateDir: dir}
	if err := playback.SaveToken(Token{AccessToken: "playback-token"}); err != nil {
		t.Fatalf("seeding the playback cache: %v", err)
	}
	web := Config{StateDir: dir, CacheFile: WebTokenCacheFile}
	if err := web.SaveToken(Token{AccessToken: "web-token", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("seeding the web cache: %v", err)
	}
	got, err := NewWebAPITokenSource(web).AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if got != "web-token" {
		t.Errorf("AccessToken = %q, want the library's own cached token", got)
	}
}

// TestWebAPITokenSourceRefreshesExpired: an expired cached token is refreshed
// against the token endpoint with the dashboard client id, and the fresh
// token is persisted back into the web cache.
func TestWebAPITokenSourceRefreshesExpired(t *testing.T) {
	dir := t.TempDir()
	var grants []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		grants = append(grants, r.Form.Get("grant_type")+" "+r.Form.Get("client_id"))
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fresh-1", "refresh_token": "rt-2", "expires_in": 3600,
		})
	}))
	defer srv.Close()

	cfg := Config{ClientID: "dash-cid", StateDir: dir, Endpoints: Endpoints{Token: srv.URL}}
	expired := Token{AccessToken: "stale", RefreshToken: "rt-1", Expiry: time.Now().Add(-2 * time.Minute)}
	if err := cfg.SaveToken(expired); err != nil {
		t.Fatalf("seeding the cache: %v", err)
	}

	src := NewWebAPITokenSource(cfg)
	got, err := src.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken: %v", err)
	}
	if got != "fresh-1" {
		t.Errorf("AccessToken = %q, want the refreshed token", got)
	}
	if len(grants) != 1 || grants[0] != "refresh_token dash-cid" {
		t.Errorf("token endpoint grants = %v, want one refresh_token grant as dash-cid", grants)
	}
	// The fresh token must land in the library's own cache file.
	tok, ok, err := cfg.LoadToken()
	if err != nil || !ok {
		t.Fatalf("cache after refresh: ok=%v err=%v", ok, err)
	}
	if tok.AccessToken != "fresh-1" || tok.RefreshToken != "rt-2" {
		t.Errorf("cached token = %+v, want the refreshed material", tok)
	}
	// A still-valid token is served without another endpoint round-trip.
	if _, err := src.AccessToken(context.Background()); err != nil {
		t.Fatalf("second AccessToken: %v", err)
	}
	if len(grants) != 1 {
		t.Errorf("a valid token must not refresh: grants = %v", grants)
	}
}

// TestWebAPITokenSourceForcedRefresh: after a 401 the transport forces one
// refresh even while the cached token is not yet expired.
func TestWebAPITokenSourceForcedRefresh(t *testing.T) {
	dir := t.TempDir()
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "forced-" + itoa(calls), "refresh_token": "rt", "expires_in": 3600,
		})
	}))
	defer srv.Close()

	cfg := Config{ClientID: "dash-cid", StateDir: dir, Endpoints: Endpoints{Token: srv.URL}}
	if err := cfg.SaveToken(Token{AccessToken: "still-valid", RefreshToken: "rt", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("seeding the cache: %v", err)
	}
	src := NewWebAPITokenSource(cfg)
	if err := src.Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	got, err := src.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("AccessToken after the forced refresh: %v", err)
	}
	if got != "forced-1" {
		t.Errorf("AccessToken = %q, want the forcibly refreshed token", got)
	}
}

// TestWebAPITokenSourceRefreshWithoutRefreshToken: a cache without refresh
// material cannot be refreshed; the error must say so instead of hanging or
// panicking.
func TestWebAPITokenSourceRefreshWithoutRefreshToken(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{StateDir: dir}
	if err := cfg.SaveToken(Token{AccessToken: "bare", Expiry: time.Now().Add(time.Hour)}); err != nil {
		t.Fatalf("seeding the cache: %v", err)
	}
	src := NewWebAPITokenSource(cfg)
	if err := src.Refresh(context.Background()); err == nil {
		t.Error("Refresh without a refresh token must fail, got nil")
	}
}

// TestWebAPIRefreshRejectSurfaces: the token endpoint rejecting the refresh
// (e.g. invalid_client when the client id is wrong) must surface with the
// endpoint's answer, not be swallowed.
func TestWebAPIRefreshRejectSurfaces(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"error": "invalid_client", "error_description": "client id mismatch",
		})
	}))
	defer srv.Close()

	cfg := Config{ClientID: "wrong-cid", StateDir: dir, Endpoints: Endpoints{Token: srv.URL}}
	if err := cfg.SaveToken(Token{AccessToken: "stale", RefreshToken: "rt", Expiry: time.Now().Add(-time.Minute)}); err != nil {
		t.Fatalf("seeding the cache: %v", err)
	}
	src := NewWebAPITokenSource(cfg)
	_, err := src.AccessToken(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid_client") {
		t.Errorf("AccessToken with a rejected refresh = %v, want the endpoint's error", err)
	}
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

// TestPlaybackAndLibraryCachesStaySeparate: the two logins cache into two
// files in the same state dir and neither clobbers the other; the library
// cache is 0600 (spec S9).
func TestPlaybackAndLibraryCachesStaySeparate(t *testing.T) {
	dir := t.TempDir()
	playback := Config{StateDir: dir}
	web := Config{StateDir: dir, CacheFile: WebTokenCacheFile}
	if err := playback.SaveToken(Token{AccessToken: "playback-tok"}); err != nil {
		t.Fatalf("playback SaveToken: %v", err)
	}
	if err := web.SaveToken(Token{AccessToken: "web-tok"}); err != nil {
		t.Fatalf("web SaveToken: %v", err)
	}
	pt, ok, err := playback.LoadToken()
	if err != nil || !ok || pt.AccessToken != "playback-tok" {
		t.Errorf("playback cache read = (%+v, %v, %v), want its own token", pt, ok, err)
	}
	wt, ok, err := web.LoadToken()
	if err != nil || !ok || wt.AccessToken != "web-tok" {
		t.Errorf("web cache read = (%+v, %v, %v), want its own token", wt, ok, err)
	}
	info, err := os.Stat(filepath.Join(dir, WebTokenCacheFile))
	if err != nil {
		t.Fatalf("stat web cache: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("web cache mode = %v, want 0600", info.Mode().Perm())
	}
}

// TestWebAPIAuthorizeURLUsesTheWebScopes: the library login authorizes with
// the dashboard client id and the Web API scope set — a different grant from
// the playback login's.
func TestWebAPIAuthorizeURLUsesTheWebScopes(t *testing.T) {
	cfg := Config{ClientID: "dash-cid", CallbackPort: 8888, Scopes: WebAPIScopes}
	u, err := url.Parse(cfg.AuthorizeURL(PKCE{Challenge: "c", State: "s"}))
	if err != nil {
		t.Fatalf("parsing authorize URL: %v", err)
	}
	if got := u.Query().Get("scope"); got != WebAPIScopes {
		t.Errorf("web authorize scope = %q, want the Web API set %q", got, WebAPIScopes)
	}
	if got := u.Query().Get("client_id"); got != "dash-cid" {
		t.Errorf("web authorize client_id = %q, want the dashboard id", got)
	}
	if got := u.Query().Get("redirect_uri"); got != "http://127.0.0.1:8888/login" {
		t.Errorf("web authorize redirect = %q, want the same loopback /login", got)
	}
	if WebAPIScopes == Scopes {
		t.Error("the Web API scope set must differ from the playback scope set")
	}
	for _, want := range []string{"playlist-read-private", "user-library-read", "user-read-private"} {
		if !strings.Contains(WebAPIScopes, want) {
			t.Errorf("WebAPIScopes %q is missing %q", WebAPIScopes, want)
		}
	}
}
