// The catalog's transport wiring (T7, two tokens): the catalog talks to
// api.spotify.com over plain net/http for the playlist list, search and the
// saved toggle, with the library's own Web API token (the second login,
// minted for the dashboard client id) — never through the embedded session's
// accessor. Playlist contents are the exception: they resolve through the
// playback session's spclient (library/context.go), which needs no Web API
// quota and has no owner restriction. A 401 refreshes once and retries once;
// a 429 surfaces as the rate-limit state with no retry storm.
package player

import (
	"context"
	"errors"

	spclient "github.com/elxgy/go-librespot/spclient"

	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"

	"github.com/nahuel/spotui/internal/library"
)

// Catalog implements library.CatalogProvider: the UI drives every fetch from
// a tea.Cmd goroutine through this library.Fetch, never from Update.
func (p *Player) Catalog() library.Fetch {
	logf := newCaptureLogger(p.authCfg.StateDir, nil).Debugf
	client := library.NewHTTPClient(p.webTokens)
	client.Base = p.webBase
	client.HTTP = p.http
	client.Logf = logf
	return library.NewWebAPI(client).
		WithContentsResolver(p.contextResolver(), p.metadataBatch()).
		WithCollectionContext(p.collectionContext()).
		WithLogger(logf)
}

// collectionContext is the saved-tracks fallback's context uri seam: the
// user's saved songs are the collection context
// spotify:user:<user id>:collection, and the user id comes from the playback
// session's own username — the Web API denied GET /v1/me/tracks, so the
// fallback never asks it who the user is. It reads the session under the
// player mutex; before the session is live it fails with the honest error
// the saved-tracks error renders.
func (p *Player) collectionContext() library.CollectionContext {
	return func(ctx context.Context) (string, error) {
		p.mu.Lock()
		sess := p.sess
		p.mu.Unlock()
		if sess == nil {
			return "", errors.New("no embedded session")
		}
		return "spotify:user:" + sess.Username() + ":collection", nil
	}
}

// contextResolver is the catalog's playlist-contents seam: the same context
// resolution playback uses (sess.Spclient().ContextResolve). It reads the
// session under the player mutex; before the session is live it fails with
// the honest error the UI renders instead of guessing.
func (p *Player) contextResolver() library.ContextResolver {
	return func(ctx context.Context, uri string) (*connectpb.Context, error) {
		p.mu.Lock()
		sess := p.sess
		p.mu.Unlock()
		if sess == nil {
			return nil, errors.New("no embedded session")
		}
		return sess.Spclient().ContextResolve(ctx, uri)
	}
}

// metadataBatch is the display-metadata fallback the resolver-backed
// contents use when a context entry carries no names: one batched
// extended-metadata lookup over the same spclient.
func (p *Player) metadataBatch() library.MetadataBatch {
	return func(ctx context.Context, uris []string) (map[string]spclient.ResolvedEntry, error) {
		p.mu.Lock()
		sess := p.sess
		p.mu.Unlock()
		if sess == nil {
			return nil, errors.New("no embedded session")
		}
		return sess.Spclient().ResolveTrackOrEpisodeMetadataBatch(ctx, uris)
	}
}
