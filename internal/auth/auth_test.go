package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// freePort reserves an ephemeral port and releases it, for the callback
// server tests. Local loopback only: no external network is touched.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// TestNewPKCEProperties: the verifier is 43..128 unreserved characters, the
// challenge is its S256 derivation and the state is fresh per login.
func TestNewPKCEProperties(t *testing.T) {
	seen := make(map[string]bool)
	for i := 0; i < 32; i++ {
		p, err := NewPKCE()
		if err != nil {
			t.Fatalf("NewPKCE: %v", err)
		}
		if n := len(p.Verifier); n < 43 || n > 128 {
			t.Fatalf("verifier length %d, want 43..128", n)
		}
		for _, r := range p.Verifier {
			if !strings.ContainsRune(pkceAlphabet, r) {
				t.Fatalf("verifier contains non-unreserved rune %q: %q", r, p.Verifier)
			}
		}
		if want := ChallengeS256(p.Verifier); p.Challenge != want {
			t.Errorf("challenge %q, want S256(verifier) %q", p.Challenge, want)
		}
		if p.State == "" {
			t.Error("state is empty")
		}
		if seen[p.Verifier] {
			t.Error("verifier repeated across calls")
		}
		seen[p.Verifier] = true
	}
}

// TestChallengeS256KnownVector pins the S256 derivation against the worked
// example in RFC 7636 appendix B.
func TestChallengeS256KnownVector(t *testing.T) {
	const verifier = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	const want = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	if got := ChallengeS256(verifier); got != want {
		t.Errorf("ChallengeS256 = %q, want %q", got, want)
	}
}

// TestAuthorizeURL: response_type must be code (the implicit grant is dead),
// the redirect must be the loopback /login URI and the scopes the exact set.
func TestAuthorizeURL(t *testing.T) {
	cfg := Config{ClientID: "cid", CallbackPort: 8888}
	u, err := url.Parse(cfg.AuthorizeURL(PKCE{Challenge: "challenge-xyz", State: "state-xyz"}))
	if err != nil {
		t.Fatalf("parsing authorize URL: %v", err)
	}
	if u.Scheme != "https" || u.Host != "accounts.spotify.com" || u.Path != "/authorize" {
		t.Errorf("authorize endpoint = %s", u)
	}
	q := u.Query()
	want := map[string]string{
		"response_type":         "code",
		"client_id":             "cid",
		"redirect_uri":          "http://127.0.0.1:8888/login",
		"scope":                 Scopes,
		"code_challenge":        "challenge-xyz",
		"code_challenge_method": "S256",
		"state":                 "state-xyz",
	}
	for k, v := range want {
		if got := q.Get(k); got != v {
			t.Errorf("authorize URL %s = %q, want %q", k, got, v)
		}
	}
	if got := Scopes; got != "user-read-private user-read-playback-state user-modify-playback-state playlist-read-private user-library-read" {
		t.Errorf("Scopes = %q", got)
	}
}

// TestCallbackWrongStateRejected: a redirect with a foreign state is refused
// and does not consume the login — the real one can still arrive.
func TestCallbackWrongStateRejected(t *testing.T) {
	results := make(chan callbackResult, 1)
	srv := httptest.NewServer(callbackHandler("the-state", results))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/login?code=nope&state=foreign")
	if err != nil {
		t.Fatalf("GET with wrong state: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("wrong-state status = %d, want 400", resp.StatusCode)
	}
	select {
	case res := <-results:
		t.Fatalf("wrong state delivered a result: %+v", res)
	default:
	}

	resp2, err := http.Get(srv.URL + "/login?code=real&state=the-state")
	if err != nil {
		t.Fatalf("GET with right state after wrong one: %v", err)
	}
	resp2.Body.Close()
	select {
	case res := <-results:
		if res.code != "real" || res.err != nil {
			t.Errorf("result after wrong state = %+v, want code real", res)
		}
	default:
		t.Fatal("the real redirect never delivered its code")
	}
}

// TestCallbackAccessDenied: the error redirect is answered without hanging
// and reported as a denial.
func TestCallbackAccessDenied(t *testing.T) {
	results := make(chan callbackResult, 1)
	srv := httptest.NewServer(callbackHandler("st", results))
	defer srv.Close()

	done := make(chan struct{})
	var resp *http.Response
	go func() {
		resp, _ = http.Get(srv.URL + "/login?error=access_denied&state=st")
		close(done)
	}()
	select {
	case res := <-results:
		if res.err == nil || !strings.Contains(res.err.Error(), "access_denied") {
			t.Errorf("denial error = %v, want it to name access_denied", res.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("error redirect hung instead of reporting")
	}
	<-done
	if resp == nil || resp.StatusCode != http.StatusOK {
		t.Errorf("error redirect status = %v, want 200 with a plain page", resp)
	}
}

// TestCallbackCodeOnce: a good code is answered with a plain page and
// delivered exactly once; further requests are refused.
func TestCallbackCodeOnce(t *testing.T) {
	results := make(chan callbackResult, 1)
	srv := httptest.NewServer(callbackHandler("st", results))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/login?code=the-code&state=st")
	if err != nil {
		t.Fatalf("GET with code: %v", err)
	}
	body := make([]byte, 512)
	n, _ := resp.Body.Read(body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("code redirect status = %d, want 200", resp.StatusCode)
	}
	if !strings.Contains(string(body[:n]), "close this tab") {
		t.Errorf("code redirect page = %q, want a close-this-tab note", body[:n])
	}
	res := <-results
	if res.code != "the-code" || res.err != nil {
		t.Fatalf("delivered %+v, want code the-code", res)
	}

	resp2, err := http.Get(srv.URL + "/login?code=second&state=st")
	if err != nil {
		t.Fatalf("second GET: %v", err)
	}
	resp2.Body.Close()
	if resp2.StatusCode == http.StatusOK {
		t.Errorf("second request answered %d, want a refusal", resp2.StatusCode)
	}
}

// TestAwaitCodeShutsDownAfterOneRequest: end to end through the real local
// listener — the first redirect completes the login and the server is gone.
func TestAwaitCodeShutsDownAfterOneRequest(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port

	type out struct {
		code string
		err  error
	}
	done := make(chan out, 1)
	go func() {
		code, err := awaitCode(context.Background(), ln, "st")
		done <- out{code, err}
	}()

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/login?code=the-code&state=st", port))
	if err != nil {
		t.Fatalf("callback GET: %v", err)
	}
	resp.Body.Close()

	select {
	case got := <-done:
		if got.err != nil || got.code != "the-code" {
			t.Fatalf("awaitCode = %q, %v", got.code, got.err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("awaitCode did not return after the redirect")
	}
	if _, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/login", port)); err == nil {
		t.Error("callback server still listening after one completed login")
	}
}

// TestAwaitCodeDenied: the error redirect surfaces as an error, not a hang.
func TestAwaitCodeDenied(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	done := make(chan error, 1)
	go func() {
		_, err := awaitCode(context.Background(), ln, "st")
		done <- err
	}()
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/login?error=access_denied&state=st", port))
	if err != nil {
		t.Fatalf("callback GET: %v", err)
	}
	resp.Body.Close()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "access_denied") {
			t.Fatalf("awaitCode error = %v, want the denial", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("awaitCode hung on the error redirect")
	}
}

// tokenForm asserts the form a token endpoint must receive.
func tokenForm(t *testing.T, r *http.Request, want url.Values) {
	t.Helper()
	if err := r.ParseForm(); err != nil {
		t.Fatalf("token endpoint form: %v", err)
	}
	for k, v := range want {
		if got := r.PostFormValue(k); got != v[0] {
			t.Errorf("token request %s = %q, want %q", k, got, v[0])
		}
	}
}

func tokenServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// TestExchangeParsesToken: the exchange posts the authorization-code grant
// with the verifier and parses access/refresh/expiry.
func TestExchangeParsesToken(t *testing.T) {
	cfg := Config{ClientID: "cid", CallbackPort: 8888, StateDir: t.TempDir()}
	srv := tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		tokenForm(t, r, url.Values{
			"grant_type":    {"authorization_code"},
			"code":          {"the-code"},
			"redirect_uri":  {"http://127.0.0.1:8888/login"},
			"client_id":     {"cid"},
			"code_verifier": {strings.Repeat("a", 64)},
		})
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"access_token":"at","refresh_token":"rt","expires_in":3600}`)
	})
	cfg.Endpoints.Token = srv.URL

	tok, err := cfg.Exchange(context.Background(), "the-code", strings.Repeat("a", 64))
	if err != nil {
		t.Fatalf("Exchange: %v", err)
	}
	if tok.AccessToken != "at" || tok.RefreshToken != "rt" {
		t.Errorf("token = %+v, want access at refresh rt", tok)
	}
	if d := time.Until(tok.Expiry); d < 59*time.Minute || d > time.Hour {
		t.Errorf("expiry = %v from now, want about an hour", d)
	}
}

// TestExchangeSurfacesErrorBody: Spotify's rejection body must reach the
// error message with the status, never swallowed.
func TestExchangeSurfacesErrorBody(t *testing.T) {
	cfg := Config{ClientID: "cid", CallbackPort: 8888, StateDir: t.TempDir()}
	srv := tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		fmt.Fprint(w, `{"error":"invalid_grant","error_description":"Code not redeemed"}`)
	})
	cfg.Endpoints.Token = srv.URL

	_, err := cfg.Exchange(context.Background(), "bad", strings.Repeat("a", 64))
	if err == nil {
		t.Fatal("Exchange accepted a rejected code")
	}
	for _, want := range []string{"400", "invalid_grant", "Code not redeemed"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("exchange error %q does not mention %q", err, want)
		}
	}
}

// TestRefreshParsesAndKeepsOldRefreshToken: the refresh grant is posted and,
// when Spotify does not rotate the refresh token, the old one is kept.
func TestRefreshParsesAndKeepsOldRefreshToken(t *testing.T) {
	cfg := Config{ClientID: "cid", CallbackPort: 8888, StateDir: t.TempDir()}
	srv := tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		tokenForm(t, r, url.Values{
			"grant_type":    {"refresh_token"},
			"refresh_token": {"old-rt"},
			"client_id":     {"cid"},
		})
		fmt.Fprint(w, `{"access_token":"new-at","expires_in":1800}`)
	})
	cfg.Endpoints.Token = srv.URL

	tok, err := cfg.Refresh(context.Background(),
		Token{AccessToken: "old-at", RefreshToken: "old-rt", Expiry: time.Now().Add(-time.Minute), UserID: "user-1"})
	if err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	if tok.AccessToken != "new-at" {
		t.Errorf("refreshed access token = %q, want new-at", tok.AccessToken)
	}
	if tok.RefreshToken != "old-rt" {
		t.Errorf("refresh token = %q, want the old one kept", tok.RefreshToken)
	}
	if tok.UserID != "user-1" {
		t.Errorf("user id = %q, want user-1 carried over", tok.UserID)
	}
}

// TestCacheWrittenAndReloaded: the cache file is 0600 in the state dir and
// round-trips; a missing cache is not an error.
func TestCacheWrittenAndReloaded(t *testing.T) {
	cfg := Config{ClientID: "cid", CallbackPort: 8888, StateDir: t.TempDir()}

	if _, ok, err := cfg.LoadToken(); ok || err != nil {
		t.Fatalf("LoadToken with no cache = ok=%v err=%v, want false, nil", ok, err)
	}
	want := Token{AccessToken: "at", RefreshToken: "rt", Expiry: time.Now().Add(time.Hour).Truncate(time.Second), UserID: "user-1"}
	if err := cfg.SaveToken(want); err != nil {
		t.Fatalf("SaveToken: %v", err)
	}
	st, err := os.Stat(filepath.Join(cfg.StateDir, "token.json"))
	if err != nil {
		t.Fatalf("stat cache: %v", err)
	}
	if perm := st.Mode().Perm(); perm != 0o600 {
		t.Errorf("cache permissions = %o, want 600", perm)
	}
	got, ok, err := cfg.LoadToken()
	if !ok || err != nil {
		t.Fatalf("LoadToken = ok=%v err=%v", ok, err)
	}
	if got != want {
		t.Errorf("LoadToken = %+v, want %+v", got, want)
	}
}

// TestEnsureTokenUsesValidCache: a cached, non-expired token is used as is,
// with no login flow and no HTTP call.
func TestEnsureTokenUsesValidCache(t *testing.T) {
	cfg := Config{ClientID: "cid", CallbackPort: 8888, StateDir: t.TempDir()}
	if err := cfg.SaveToken(Token{AccessToken: "at", RefreshToken: "rt", Expiry: time.Now().Add(time.Hour), UserID: "user-1"}); err != nil {
		t.Fatal(err)
	}
	srv := tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("token endpoint hit with a valid cached token")
		fmt.Fprint(w, `{}`)
	})
	cfg.Endpoints.Token = srv.URL

	tok, err := cfg.EnsureToken(context.Background(), func(string) { t.Error("login flow started with a valid cache") })
	if err != nil {
		t.Fatalf("EnsureToken: %v", err)
	}
	if tok.AccessToken != "at" || tok.UserID != "user-1" {
		t.Errorf("EnsureToken = %+v, want the cached token", tok)
	}
}

// TestEnsureTokenRefreshesExpiredToken: an expired access token is refreshed
// before use, without a new login, and the cache is updated.
func TestEnsureTokenRefreshesExpiredToken(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{ClientID: "cid", CallbackPort: 8888, StateDir: dir}
	if err := cfg.SaveToken(Token{AccessToken: "old", RefreshToken: "rt", Expiry: time.Now().Add(-time.Hour), UserID: "user-1"}); err != nil {
		t.Fatal(err)
	}
	srv := tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		tokenForm(t, r, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {"rt"}, "client_id": {"cid"}})
		fmt.Fprint(w, `{"access_token":"new","expires_in":3600}`)
	})
	cfg.Endpoints.Token = srv.URL

	tok, err := cfg.EnsureToken(context.Background(), func(string) { t.Error("login flow started when a refresh was possible") })
	if err != nil {
		t.Fatalf("EnsureToken: %v", err)
	}
	if tok.AccessToken != "new" || tok.UserID != "user-1" {
		t.Errorf("EnsureToken = %+v, want refreshed token for user-1", tok)
	}
	if cached, ok, _ := cfg.LoadToken(); !ok || cached.AccessToken != "new" {
		t.Errorf("cache after refresh = %+v, want the new access token", cached)
	}
}

// TestEnsureTokenNoTokenRunsFullFlow: with no cache the login flow emits our
// authorization URL (response_type=code) before any token call, then
// completes through the local callback and the token exchange, and caches
// the result.
func TestEnsureTokenNoTokenRunsFullFlow(t *testing.T) {
	var tokenHits int32
	srv := tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenHits, 1)
		if err := r.ParseForm(); err != nil {
			t.Errorf("form: %v", err)
		}
		if got := r.PostFormValue("grant_type"); got != "authorization_code" {
			t.Errorf("grant_type = %q", got)
		}
		if v := r.PostFormValue("code_verifier"); len(v) < 43 || len(v) > 128 {
			t.Errorf("code_verifier length = %d, want 43..128", len(v))
		}
		fmt.Fprint(w, `{"access_token":"at","refresh_token":"rt","expires_in":3600}`)
	})
	port := freePort(t)
	cfg := Config{
		ClientID:     "cid",
		CallbackPort: port,
		StateDir:     t.TempDir(),
		Endpoints:    Endpoints{Authorize: "https://accounts.spotify.test/authorize", Token: srv.URL},
	}

	urlCh := make(chan string, 1)
	type out struct {
		tok Token
		err error
	}
	done := make(chan out, 1)
	go func() {
		tok, err := cfg.EnsureToken(context.Background(), func(u string) { urlCh <- u })
		done <- out{tok, err}
	}()

	var loginURL string
	select {
	case loginURL = <-urlCh:
	case <-time.After(2 * time.Second):
		t.Fatal("no login URL was emitted")
	}
	u, err := url.Parse(loginURL)
	if err != nil {
		t.Fatalf("emitted URL: %v", err)
	}
	q := u.Query()
	if q.Get("response_type") != "code" {
		t.Errorf("emitted URL response_type = %q, want code", q.Get("response_type"))
	}
	if q.Get("redirect_uri") != fmt.Sprintf("http://127.0.0.1:%d/login", port) {
		t.Errorf("emitted URL redirect_uri = %q", q.Get("redirect_uri"))
	}

	if n := atomic.LoadInt32(&tokenHits); n != 0 {
		t.Fatalf("token endpoint hit %d times before the browser redirect", n)
	}

	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/login?code=the-code&state=%s", port, url.QueryEscape(q.Get("state"))))
	if err != nil {
		t.Fatalf("completing the login: %v", err)
	}
	resp.Body.Close()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("EnsureToken: %v", got.err)
		}
		if got.tok.AccessToken != "at" {
			t.Errorf("token = %+v, want access at", got.tok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("EnsureToken did not complete after the callback")
	}
	if cached, ok, _ := cfg.LoadToken(); !ok || cached.AccessToken != "at" {
		t.Errorf("cache after login = %+v ok=%v, want the new token", cached, ok)
	}
}

// TestEnsureTokenRefreshRejectedFallsBackToLogin: a revoked refresh token
// does not kill the start — the full login runs instead.
func TestEnsureTokenRefreshRejectedFallsBackToLogin(t *testing.T) {
	var tokenHits int32
	srv := tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&tokenHits, 1)
		if r.PostFormValue("grant_type") == "refresh_token" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, `{"error":"invalid_grant","error_description":"Refresh token revoked"}`)
			return
		}
		fmt.Fprint(w, `{"access_token":"fresh","refresh_token":"rt2","expires_in":3600}`)
	})
	port := freePort(t)
	dir := t.TempDir()
	cfg := Config{
		ClientID:     "cid",
		CallbackPort: port,
		StateDir:     dir,
		Endpoints:    Endpoints{Authorize: "https://accounts.spotify.test/authorize", Token: srv.URL},
	}
	if err := cfg.SaveToken(Token{AccessToken: "old", RefreshToken: "dead", Expiry: time.Now().Add(-time.Hour)}); err != nil {
		t.Fatal(err)
	}

	urlCh := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		_, err := cfg.EnsureToken(context.Background(), func(u string) { urlCh <- u })
		done <- err
	}()
	var loginURL string
	select {
	case loginURL = <-urlCh:
	case <-time.After(2 * time.Second):
		t.Fatal("no login URL was emitted after the rejected refresh")
	}
	u, err := url.Parse(loginURL)
	if err != nil {
		t.Fatalf("emitted URL: %v", err)
	}
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d/login?code=c&state=%s",
		port, url.QueryEscape(u.Query().Get("state"))))
	if err != nil {
		t.Fatalf("completing the login: %v", err)
	}
	resp.Body.Close()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("EnsureToken after rejected refresh: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("EnsureToken hung after the rejected refresh")
	}
	if cached, ok, _ := cfg.LoadToken(); !ok || cached.AccessToken != "fresh" {
		t.Errorf("cache = %+v ok=%v, want the fresh token", cached, ok)
	}
}

// TestFetchUserID: the profile call returns the user id and never inspects
// product (spec S21: it no longer exists for Development Mode apps).
func TestFetchUserID(t *testing.T) {
	var body map[string]any
	srv := tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer at" {
			t.Errorf("Authorization = %q, want Bearer at", got)
		}
		body = map[string]any{"id": "user-1", "display_name": "Nahuel"}
		// product is deliberately absent.
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(body)
	})
	cfg := Config{ClientID: "cid", CallbackPort: 8888, StateDir: t.TempDir()}
	cfg.Endpoints.Me = srv.URL

	id, err := cfg.FetchUserID(context.Background(), "at")
	if err != nil {
		t.Fatalf("FetchUserID: %v", err)
	}
	if id != "user-1" {
		t.Errorf("FetchUserID = %q, want user-1", id)
	}
}

// TestFetchUserIDSurfacesFailure: a profile rejection names the step and the
// status instead of failing silently.
func TestFetchUserIDSurfacesFailure(t *testing.T) {
	srv := tokenServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"error":"invalid_token"}`)
	})
	cfg := Config{ClientID: "cid", CallbackPort: 8888, StateDir: t.TempDir()}
	cfg.Endpoints.Me = srv.URL

	_, err := cfg.FetchUserID(context.Background(), "at")
	if err == nil {
		t.Fatal("FetchUserID accepted a 401")
	}
	if !strings.Contains(err.Error(), "user profile") || !strings.Contains(err.Error(), "401") {
		t.Errorf("error = %q, want the step and status named", err)
	}
}
