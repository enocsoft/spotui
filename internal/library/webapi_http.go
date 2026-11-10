// The plain net/http Web API transport: the catalog talks directly to
// api.spotify.com with the library's own token (the second login, minted for
// the dashboard client id), never with the playback session's. One 401
// forces exactly one token refresh and one retry; a 429 (and every other
// non-2xx) surfaces truthfully with no invisible retry.
package library

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// APIBaseURL is the Spotify Web API root the catalog talks to directly.
const APIBaseURL = "https://api.spotify.com/"

// maxBodyBytes caps how much of one response is read; a runaway body must
// not balloon the process.
const maxBodyBytes = 1 << 20

// TokenSource supplies the library's access tokens: the bearer value every
// catalog request carries, refreshed proactively when expired and
// force-refreshed once after a 401. Implementations live in internal/auth.
type TokenSource interface {
	// AccessToken returns the current access token, refreshing it when
	// needed. Not logged in is reported as an error the UI renders.
	AccessToken(ctx context.Context) (string, error)
	// Refresh forces one token refresh after the API rejected the current
	// token.
	Refresh(ctx context.Context) error
}

// HTTPClient implements WebAPIClient over plain net/http. The zero-value
// fields fall back to production defaults (the real API root, a client with
// a timeout, no logging).
type HTTPClient struct {
	// Tokens supplies (and refreshes) the library's access token.
	Tokens TokenSource
	// Base is the API root; empty selects APIBaseURL. Tests point it at an
	// httptest server.
	Base string
	// HTTP is the client used for every call; nil = a client with a timeout.
	HTTP *http.Client
	// Logf optionally receives human-paced progress lines. Tokens are never
	// logged; the capture logger redacts token-shaped runs anyway.
	Logf func(format string, args ...any)
}

// NewHTTPClient builds the transport over the given token source with the
// production defaults; callers override Base, HTTP and Logf as needed.
func NewHTTPClient(tokens TokenSource) *HTTPClient {
	return &HTTPClient{Tokens: tokens, Base: APIBaseURL}
}

// Call runs one catalog request and returns the body with its status. A 401
// triggers exactly one token refresh and one retry; a 429 comes back as a
// plain status for the catalog's rate-limit state — no retry storm here.
func (h *HTTPClient) Call(ctx context.Context, method, path string, query url.Values, body []byte) ([]byte, int, error) {
	h.logf("catalog call %s %s?%s", method, path, query.Encode())
	tok, err := h.Tokens.AccessToken(ctx)
	if err != nil {
		h.logf("catalog token unavailable: %v", err)
		return nil, 0, err
	}
	b, status, err := h.send(ctx, method, path, query, body, tok)
	if err != nil {
		return nil, 0, err
	}
	if status == http.StatusUnauthorized {
		h.logf("catalog 401 · refreshing the library token once")
		if rerr := h.Tokens.Refresh(ctx); rerr != nil {
			return nil, http.StatusUnauthorized, fmt.Errorf("catalog 401 · refreshing the library token failed: %v", rerr)
		}
		tok, err = h.Tokens.AccessToken(ctx)
		if err != nil {
			return nil, http.StatusUnauthorized, fmt.Errorf("catalog 401 · %v", err)
		}
		b, status, err = h.send(ctx, method, path, query, body, tok)
		if err != nil {
			return nil, 0, err
		}
	}
	h.logf("catalog done %s %s status=%d err=%v bytes=%d", method, path, status, err, len(b))
	return b, status, err
}

// send issues one request with the given bearer token and reads the body.
func (h *HTTPClient) send(ctx context.Context, method, path string, query url.Values, body []byte, tok string) ([]byte, int, error) {
	u := strings.TrimSuffix(h.base(), "/") + "/" + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	var rd io.Reader
	if len(body) > 0 {
		rd = strings.NewReader(string(body))
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if len(body) > 0 {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := h.client().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("reading the response: %v", err)
	}
	return b, resp.StatusCode, nil
}

func (h *HTTPClient) base() string {
	if h.Base != "" {
		return h.Base
	}
	return APIBaseURL
}

func (h *HTTPClient) client() *http.Client {
	if h.HTTP != nil {
		return h.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (h *HTTPClient) logf(format string, args ...any) {
	if h.Logf != nil {
		h.Logf(format, args...)
	}
}
