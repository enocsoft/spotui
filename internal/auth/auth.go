// Package auth implements the login the app owns (spec S20): OAuth 2.0
// Authorization Code with PKCE against accounts.spotify.com, a local
// /login callback on 127.0.0.1 (spec S12), token exchange and refresh, and
// a 0600 token cache in the XDG state dir (spec S9). The library's own
// interactive login is dead: it emits response_type=token (implicit grant),
// which Spotify removed on 2025-11-27, so the resulting token is produced
// here and handed to the session as SpotifyTokenCredentials.
//
// Every HTTP call goes through the injectable endpoints and client in
// Config; tests use httptest and never touch the network. Tokens,
// verifiers and codes are never logged.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Scopes is the authorization scope set requested from the user. The
// library-facing scopes are included from the start: Spotify only shows the
// consent screen once, and asking again would force a second approval.
const Scopes = "user-read-private user-read-playback-state user-modify-playback-state playlist-read-private user-library-read"

// Production endpoints. Config.Endpoints overrides them in tests.
const (
	authorizeEndpoint = "https://accounts.spotify.com/authorize"
	tokenEndpoint     = "https://accounts.spotify.com/api/token"
	meEndpoint        = "https://api.spotify.com/v1/me"
)

// loginPath is the callback path (fixed by our callback server, matching
// the redirect URI registered in the dashboard, spec S12).
const loginPath = "/login"

const (
	// verifierLen is inside RFC 7636's 43..128 range.
	verifierLen = 64
	// stateLen is the anti-CSRF state length.
	stateLen = 32
	// pkceAlphabet is RFC 7636's unreserved set.
	pkceAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789-._~"

	// cacheFile lives in the XDG state dir, written 0600 (spec S9).
	cacheFile = "token.json"

	// tokenExpirySkew: treat a token as expired this long before its real
	// expiry, so it is refreshed proactively instead of dying mid-request.
	tokenExpirySkew = time.Minute
)

// Endpoints are the Spotify URLs the flow talks to. The zero value selects
// the production endpoints; tests point them at an httptest server.
type Endpoints struct {
	Authorize string
	Token     string
	Me        string
}

// Config configures the PKCE flow and the token cache.
type Config struct {
	// ClientID is the Spotify app client id from the dashboard config.
	ClientID string
	// CallbackPort is the TCP port of the loopback redirect URI.
	CallbackPort int
	// StateDir is the XDG state dir; the cache lands in
	// <StateDir>/token.json with 0600 permissions.
	StateDir string
	// Endpoints overrides the Spotify URLs (tests); zero = production.
	Endpoints Endpoints
	// HTTP is the client used for every call; nil = a client with a timeout.
	HTTP *http.Client
}

func (c Config) endpoints() Endpoints {
	e := c.Endpoints
	if e.Authorize == "" {
		e.Authorize = authorizeEndpoint
	}
	if e.Token == "" {
		e.Token = tokenEndpoint
	}
	if e.Me == "" {
		e.Me = meEndpoint
	}
	return e
}

func (c Config) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

// RedirectURI is the loopback redirect URI the callback server answers and
// the dashboard must register (spec S12: IP literal, never localhost).
func (c Config) RedirectURI() string {
	return fmt.Sprintf("http://127.0.0.1:%d%s", c.CallbackPort, loginPath)
}

// PKCE carries the per-login proof key pair (RFC 7636) and the anti-CSRF
// state. None of it is secret on the wire except the verifier, which never
// leaves the process except in the token exchange.
type PKCE struct {
	Verifier  string
	Challenge string
	State     string
}

// NewPKCE generates a fresh verifier/challenge pair and state.
func NewPKCE() (PKCE, error) {
	verifier, err := randomUnreserved(verifierLen)
	if err != nil {
		return PKCE{}, fmt.Errorf("generating code verifier: %v", err)
	}
	state, err := randomUnreserved(stateLen)
	if err != nil {
		return PKCE{}, fmt.Errorf("generating state: %v", err)
	}
	return PKCE{Verifier: verifier, Challenge: ChallengeS256(verifier), State: state}, nil
}

// ChallengeS256 derives the S256 code challenge of a verifier (RFC 7636
// §4.2): base64url(SHA256(verifier)) without padding.
func ChallengeS256(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// randomUnreserved returns n unbiased characters from pkceAlphabet via
// rejection sampling over crypto/rand.
func randomUnreserved(n int) (string, error) {
	buf := make([]byte, 0, n*2)
	out := make([]byte, 0, n)
	for len(out) < n {
		if len(buf) == 0 {
			chunk := make([]byte, 256)
			if _, err := rand.Read(chunk); err != nil {
				return "", err
			}
			buf = chunk
		}
		b := buf[0]
		buf = buf[1:]
		if int(b) >= 256/len(pkceAlphabet)*len(pkceAlphabet) {
			continue // rejection sample: avoid modulo bias
		}
		out = append(out, pkceAlphabet[int(b)%len(pkceAlphabet)])
	}
	return string(out), nil
}

// AuthorizeURL builds the authorization URL: response_type=code (the
// implicit grant is dead), S256 challenge, our redirect and scopes.
func (c Config) AuthorizeURL(p PKCE) string {
	q := url.Values{
		"response_type":         {"code"},
		"client_id":             {c.ClientID},
		"redirect_uri":          {c.RedirectURI()},
		"scope":                 {Scopes},
		"state":                 {p.State},
		"code_challenge":        {p.Challenge},
		"code_challenge_method": {"S256"},
	}
	return c.endpoints().Authorize + "?" + q.Encode()
}

// Token is a Spotify access token with its refresh material and the user id
// the session needs as Username.
type Token struct {
	AccessToken  string    `json:"access_token"`
	RefreshToken string    `json:"refresh_token"`
	Expiry       time.Time `json:"expiry"`
	UserID       string    `json:"user_id"`
}

// Expired reports whether the access token must be refreshed before use:
// at (or within the proactive skew of) its expiry, or absent entirely.
func (t Token) Expired() bool {
	return t.AccessToken == "" || !time.Now().Before(t.Expiry.Add(-tokenExpirySkew))
}

// callbackResult is what the browser redirect carried.
type callbackResult struct {
	code string
	err  error
}

// callbackHandler answers Spotify's redirect on /login: it validates the
// anti-CSRF state, captures the code or the error, answers the browser with
// a plain close-this-tab page and reports the result exactly once. A state
// mismatch is rejected without consuming the login, so the real redirect
// can still arrive.
func callbackHandler(state string, results chan<- callbackResult) http.Handler {
	var mu sync.Mutex
	served := false
	report := func(res callbackResult) bool {
		mu.Lock()
		defer mu.Unlock()
		if served {
			return false
		}
		served = true
		results <- res
		return true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != loginPath {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		if e := q.Get("error"); e != "" {
			report(callbackResult{err: fmt.Errorf("authorization was denied (%s)", e)})
			w.WriteHeader(http.StatusOK)
			fmt.Fprint(w, "spotify-tui: authorization was denied — you can close this tab.")
			return
		}
		if got := q.Get("state"); got != state {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "spotify-tui: state mismatch — this tab is not the login you started.")
			return
		}
		code := q.Get("code")
		if code == "" {
			w.WriteHeader(http.StatusBadRequest)
			fmt.Fprint(w, "spotify-tui: the redirect carried no authorization code.")
			return
		}
		if !report(callbackResult{code: code}) {
			w.WriteHeader(http.StatusServiceUnavailable)
			fmt.Fprint(w, "spotify-tui: login already completed.")
			return
		}
		fmt.Fprint(w, "spotify-tui: login complete — you can close this tab.")
	})
}

// AwaitCode runs the local callback server on 127.0.0.1:<CallbackPort> and
// returns the authorization code Spotify redirected back with. The server
// shuts down as soon as the login result arrives (or the context dies).
func (c Config) AwaitCode(ctx context.Context, state string) (string, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", c.CallbackPort))
	if err != nil {
		return "", fmt.Errorf("authorize: cannot listen on 127.0.0.1:%d — the login callback port is taken: %v", c.CallbackPort, err)
	}
	defer ln.Close()
	return awaitCode(ctx, ln, state)
}

// awaitCode serves the callback on an existing listener so tests can bind an
// ephemeral port and probe the one-shot shutdown.
func awaitCode(ctx context.Context, ln net.Listener, state string) (string, error) {
	results := make(chan callbackResult, 1)
	srv := &http.Server{Handler: callbackHandler(state, results)}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	select {
	case res := <-results:
		_ = srv.Shutdown(context.Background())
		if res.err != nil {
			return "", fmt.Errorf("authorize: %v", res.err)
		}
		return res.code, nil
	case <-ctx.Done():
		_ = srv.Shutdown(context.Background())
		return "", fmt.Errorf("authorize: login cancelled (%v)", ctx.Err())
	case err := <-serveErr:
		return "", fmt.Errorf("authorize: callback server failed: %v", err)
	}
}

// tokenResponse is the shape of Spotify's token endpoint body, success or
// error (RFC 6749 §5.1/§5.2).
type tokenResponse struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// tokenRequest posts one grant to the token endpoint and parses the
// response. Failures carry the status and Spotify's error body.
func (c Config) tokenRequest(ctx context.Context, form url.Values) (Token, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoints().Token,
		strings.NewReader(form.Encode()))
	if err != nil {
		return Token{}, fmt.Errorf("token exchange: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return Token{}, fmt.Errorf("token exchange: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return Token{}, fmt.Errorf("token exchange: reading the response: %v", err)
	}
	var parsed tokenResponse
	if json.Unmarshal(body, &parsed) != nil {
		return Token{}, fmt.Errorf("token exchange: status %d with an unreadable body: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if resp.StatusCode != http.StatusOK {
		detail := parsed.Error
		if parsed.ErrorDescription != "" {
			detail += ": " + parsed.ErrorDescription
		}
		if detail == "" {
			detail = strings.TrimSpace(string(body))
		}
		return Token{}, fmt.Errorf("token exchange rejected: status %d: %s", resp.StatusCode, detail)
	}
	if parsed.AccessToken == "" {
		return Token{}, fmt.Errorf("token exchange: Spotify returned no access token (status %d)", resp.StatusCode)
	}
	if parsed.ExpiresIn <= 0 {
		return Token{}, fmt.Errorf("token exchange: Spotify returned no expiry (expires_in %d)", parsed.ExpiresIn)
	}
	return Token{
		AccessToken:  parsed.AccessToken,
		RefreshToken: parsed.RefreshToken,
		Expiry:       time.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second),
	}, nil
}

// Exchange trades the authorization code for tokens (grant_type=
// authorization_code with the PKCE verifier, RFC 7636 §4.5).
func (c Config) Exchange(ctx context.Context, code, verifier string) (Token, error) {
	return c.tokenRequest(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"redirect_uri":  {c.RedirectURI()},
		"client_id":     {c.ClientID},
		"code_verifier": {verifier},
	})
}

// Refresh trades a refresh token for a fresh access token, keeping the old
// refresh token when Spotify does not rotate it and carrying the user id.
func (c Config) Refresh(ctx context.Context, tok Token) (Token, error) {
	nt, err := c.tokenRequest(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {tok.RefreshToken},
		"client_id":     {c.ClientID},
	})
	if err != nil {
		return Token{}, err
	}
	if nt.RefreshToken == "" {
		nt.RefreshToken = tok.RefreshToken
	}
	nt.UserID = tok.UserID
	return nt, nil
}

func (c Config) cachePath() string { return filepath.Join(c.StateDir, cacheFile) }

// LoadToken reads the cached token. A missing cache is not an error; a
// corrupt one is, and says what to do about it.
func (c Config) LoadToken() (Token, bool, error) {
	b, err := os.ReadFile(c.cachePath())
	if err != nil {
		if os.IsNotExist(err) {
			return Token{}, false, nil
		}
		return Token{}, false, fmt.Errorf("token cache: cannot read %s: %v", c.cachePath(), err)
	}
	var t Token
	if json.Unmarshal(b, &t) != nil {
		return Token{}, false, fmt.Errorf("token cache: %s is corrupt — delete it and log in again", c.cachePath())
	}
	return t, true, nil
}

// SaveToken persists the token in the state dir with 0600 permissions
// (spec S9); nothing lands in the repository.
func (c Config) SaveToken(t Token) error {
	if err := os.MkdirAll(c.StateDir, 0o700); err != nil {
		return fmt.Errorf("token cache: cannot create %s: %v", c.StateDir, err)
	}
	b, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("token cache: %v", err)
	}
	if err := os.WriteFile(c.cachePath(), b, 0o600); err != nil {
		return fmt.Errorf("token cache: cannot write %s: %v", c.cachePath(), err)
	}
	return nil
}

// FetchUserID reads the current user id once from GET /v1/me — the session
// needs it as Username (spec S20). It never inspects product: the field no
// longer exists for Development Mode apps, so no code may infer the
// subscription from it (spec S21).
func (c Config) FetchUserID(ctx context.Context, accessToken string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoints().Me, nil)
	if err != nil {
		return "", fmt.Errorf("user profile: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("user profile: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if err != nil {
		return "", fmt.Errorf("user profile: reading the response: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("user profile: GET /v1/me rejected: status %d: %s",
			resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var me struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(body, &me) != nil || me.ID == "" {
		return "", fmt.Errorf("user profile: GET /v1/me returned no user id")
	}
	return me.ID, nil
}

// EnsureToken returns a usable access token: the cache first, a proactive
// refresh when the cached access token is expired, and the full PKCE login
// when nothing usable is cached. onAuthorizeURL (optional) is called with
// the authorization URL before the callback wait — the TUI surfaces it.
// A rejected refresh falls back to a fresh login instead of failing.
func (c Config) EnsureToken(ctx context.Context, onAuthorizeURL func(string)) (Token, error) {
	tok, ok, err := c.LoadToken()
	if err != nil {
		return Token{}, err
	}
	if ok && !tok.Expired() {
		return tok, nil
	}
	if ok && tok.RefreshToken != "" {
		if nt, err := c.Refresh(ctx, tok); err == nil {
			if err := c.SaveToken(nt); err != nil {
				return Token{}, err
			}
			return nt, nil
		}
		// The refresh token was rejected (revoked or expired): fall
		// through to a fresh login rather than failing the start.
	}

	pkce, err := NewPKCE()
	if err != nil {
		return Token{}, fmt.Errorf("authorize: %v", err)
	}
	if onAuthorizeURL != nil {
		onAuthorizeURL(c.AuthorizeURL(pkce))
	}
	code, err := c.AwaitCode(ctx, pkce.State)
	if err != nil {
		return Token{}, err
	}
	tok, err = c.Exchange(ctx, code, pkce.Verifier)
	if err != nil {
		return Token{}, err
	}
	if err := c.SaveToken(tok); err != nil {
		return Token{}, err
	}
	return tok, nil
}

// WriteLoginURL saves the authorization URL verbatim into dir and returns the
// path. Terminal screens truncate long URLs, and a URL missing parameters makes
// the server reject the request with a message that blames the wrong thing.
func WriteLoginURL(dir, rawURL string) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	path := filepath.Join(dir, "login-url.txt")
	if err := os.WriteFile(path, []byte(rawURL+"\n"), 0o600); err != nil {
		return "", err
	}
	return path, nil
}
