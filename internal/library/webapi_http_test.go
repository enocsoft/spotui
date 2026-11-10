// Tests for the plain net/http Web API client (the transport that replaced
// the session accessor for catalog calls): it sends the library's own token,
// refreshes once after a 401 and retries once, never retries a 429, and
// surfaces every other status truthfully. Offline: an httptest server and a
// stub token source; no credentials.
package library

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// stubTokens is a TokenSource fixture: it hands out one token at a time and
// counts how often it was asked to refresh.
type stubTokens struct {
	token        string
	refreshes    int
	refreshError error
}

func (s *stubTokens) AccessToken(context.Context) (string, error) {
	return s.token, nil
}

func (s *stubTokens) Refresh(context.Context) error {
	s.refreshes++
	if s.refreshError != nil {
		return s.refreshError
	}
	s.token = fmt.Sprintf("refreshed-%d", s.refreshes)
	return nil
}

// recordingServer answers every catalog request through handler and records
// the Authorization header of every request.
func recordingServer(t *testing.T, handler func(n int, auth string) (int, string)) (*httptest.Server, *[]string) {
	t.Helper()
	auths := &[]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*auths = append(*auths, r.Header.Get("Authorization"))
		status, body := handler(len(*auths), r.Header.Get("Authorization"))
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv, auths
}

func newHTTPTest(apiBase string, tokens TokenSource) *WebAPI {
	c := NewHTTPClient(tokens)
	c.Base = apiBase
	return NewWebAPI(c)
}

const okPlaylists = `{"total":0,"offset":0,"next":null,"items":[]}`

// TestHTTPClientSendsTheWebAPIToken: every catalog request carries the
// library's own bearer token — never the playback session's.
func TestHTTPClientSendsTheWebAPIToken(t *testing.T) {
	srv, auths := recordingServer(t, func(n int, auth string) (int, string) {
		return 200, okPlaylists
	})
	ts := &stubTokens{token: "web-api-token"}
	if _, err := newHTTPTest(srv.URL, ts).Playlists(context.Background(), 0); err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if len(*auths) != 1 {
		t.Fatalf("made %d requests, want 1", len(*auths))
	}
	if (*auths)[0] != "Bearer web-api-token" {
		t.Errorf("Authorization = %q, want the library's own token", (*auths)[0])
	}
}

// TestHTTPClientRefreshesOnceOn401AndRetriesOnce: a 401 forces exactly one
// token refresh and exactly one retry with the fresh token — not a loop.
func TestHTTPClientRefreshesOnceOn401AndRetriesOnce(t *testing.T) {
	srv, auths := recordingServer(t, func(n int, auth string) (int, string) {
		if n == 1 {
			return 401, `{"error":{"status":401,"message":"The access token expired"}}`
		}
		if auth != "Bearer refreshed-1" {
			t.Errorf("retry Authorization = %q, want the refreshed token", auth)
		}
		return 200, okPlaylists
	})
	ts := &stubTokens{token: "stale-token"}
	if _, err := newHTTPTest(srv.URL, ts).Playlists(context.Background(), 0); err != nil {
		t.Fatalf("Playlists after refresh+retry: %v", err)
	}
	if len(*auths) != 2 {
		t.Fatalf("made %d requests, want exactly 2 (original + one retry)", len(*auths))
	}
	if (*auths)[0] != "Bearer stale-token" {
		t.Errorf("first Authorization = %q, want the stale token", (*auths)[0])
	}
	if ts.refreshes != 1 {
		t.Errorf("refreshes = %d, want exactly 1", ts.refreshes)
	}
}

// TestHTTPClientDoesNotRetry429: a 429 surfaces as the rate-limit state after
// a single request — no retry storm at the transport level.
func TestHTTPClientDoesNotRetry429(t *testing.T) {
	n := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.WriteHeader(429)
		_, _ = w.Write([]byte(`{"error":{"status":429,"message":"API rate limit exceeded"}}`))
	}))
	defer srv.Close()
	ts := &stubTokens{token: "web-api-token"}
	_, err := newHTTPTest(srv.URL, ts).Search(context.Background(), "q", 0)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !apiErr.RateLimited {
		t.Fatalf("a 429 must surface rate-limited, got %v", err)
	}
	if n != 1 {
		t.Errorf("a 429 must stop after one request, made %d", n)
	}
	if ts.refreshes != 0 {
		t.Errorf("a 429 must not refresh the token, refreshed %d times", ts.refreshes)
	}
}

// TestHTTPClientSurfaces401WhenRefreshFails: when the refresh is rejected the
// caller sees the failure with its reason, not a silent empty result.
func TestHTTPClientSurfaces401WhenRefreshFails(t *testing.T) {
	srv, _ := recordingServer(t, func(n int, auth string) (int, string) {
		return 401, `{"error":{"status":401,"message":"The access token expired"}}`
	})
	ts := &stubTokens{token: "stale-token", refreshError: errors.New("token exchange rejected: status 400: invalid_client")}
	_, err := newHTTPTest(srv.URL, ts).Playlists(context.Background(), 0)
	if err == nil || !strings.Contains(err.Error(), "invalid_client") {
		t.Errorf("a failed refresh must surface with its reason, got %v", err)
	}
	if ts.refreshes != 1 {
		t.Errorf("a 401 must try exactly one refresh, refreshed %d times", ts.refreshes)
	}
}

// TestHTTPClientKeepsThePathAndQuery: the request lands on base+path with the
// encoded query — the seam the endpoint shapes are pinned against.
func TestHTTPClientKeepsThePathAndQuery(t *testing.T) {
	var gotPath, gotQuery, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotMethod = r.URL.Path, r.URL.RawQuery, r.Method
		w.WriteHeader(200)
		_, _ = w.Write([]byte(okPlaylists))
	}))
	defer srv.Close()
	ts := &stubTokens{token: "t"}
	if _, err := newHTTPTest(srv.URL, ts).Playlists(context.Background(), 50); err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if gotPath != "/v1/me/playlists" || gotQuery != "limit=50&offset=50" || gotMethod != "GET" {
		t.Errorf("request = %s %s?%s, want GET /v1/me/playlists?limit=50&offset=50", gotMethod, gotPath, gotQuery)
	}
}
