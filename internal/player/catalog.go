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

	"github.com/nahuel/spotify-tui/internal/library"
)

// Catalog implements library.CatalogProvider: the UI drives every fetch from
// a tea.Cmd goroutine through this library.Fetch, never from Update.
func (p *Player) Catalog() library.Fetch {
	client := library.NewHTTPClient(p.webTokens)
	client.Base = p.webBase
	client.HTTP = p.http
	client.Logf = newCaptureLogger(p.authCfg.StateDir, nil).Debugf
	return library.NewWebAPI(client).
		WithContentsResolver(p.contextResolver(), p.metadataBatch())
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
