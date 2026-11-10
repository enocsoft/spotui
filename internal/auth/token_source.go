// The library's own token source (the second login): it serves the Web API
// catalog the access token minted for the dashboard client id, refreshing it
// proactively when expired and force-refreshing once after a 401. It is safe
// for concurrent use — the catalog runs inside tea.Cmd goroutines — and it
// never logs tokens.
package auth

import (
	"context"
	"errors"
	"fmt"
	"sync"
)

// ErrNoWebToken is what the source reports with nothing cached: the catalog
// renders it as the not-logged-in state, never a crash.
var ErrNoWebToken = errors.New("not logged in · the library token is missing — start the app once to log in")

// WebAPITokenSource yields the library's access tokens from its own cache
// (WebTokenCacheFile), refreshing through the same PKCE machinery as the
// playback login but as a separate client.
type WebAPITokenSource struct {
	cfg    Config
	mu     sync.Mutex
	tok    Token
	loaded bool
}

// NewWebAPITokenSource builds the source for one login configuration.
func NewWebAPITokenSource(cfg Config) *WebAPITokenSource {
	return &WebAPITokenSource{cfg: cfg}
}

// AccessToken returns a usable access token: the cached one, refreshed
// proactively when it is expired (or within the expiry skew).
func (s *WebAPITokenSource) AccessToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		tok, ok, err := s.cfg.LoadToken()
		if err != nil {
			return "", err
		}
		if !ok || tok.AccessToken == "" {
			return "", ErrNoWebToken
		}
		s.tok, s.loaded = tok, true
	}
	if s.tok.Expired() {
		if err := s.refreshLocked(ctx); err != nil {
			return "", err
		}
	}
	return s.tok.AccessToken, nil
}

// Refresh forces one token refresh after the API rejected the current token
// (a 401). The fresh token replaces the cached one; a rejected refresh
// surfaces with the endpoint's answer.
func (s *WebAPITokenSource) Refresh(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.loaded {
		tok, ok, err := s.cfg.LoadToken()
		if err != nil {
			return err
		}
		if !ok || tok.AccessToken == "" {
			return ErrNoWebToken
		}
		s.tok, s.loaded = tok, true
	}
	return s.refreshLocked(ctx)
}

func (s *WebAPITokenSource) refreshLocked(ctx context.Context) error {
	if s.tok.RefreshToken == "" {
		return fmt.Errorf("the library token carries no refresh token — delete %s and start the app to log in again",
			s.cfg.cachePath())
	}
	nt, err := s.cfg.Refresh(ctx, s.tok)
	if err != nil {
		return err
	}
	s.tok = nt
	if err := s.cfg.SaveToken(nt); err != nil {
		return fmt.Errorf("saving the refreshed library token: %v", err)
	}
	return nil
}
