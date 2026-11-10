// The Web API catalog of the real library (T7). Every catalog call is routed
// through the embedded session's own accessor — sess.WebApiWith429RetryAnd
// ReadBody (spec S20): one token, its 429 retry loop, no second auth flow and
// no credentials of our own. The dashboard client id plays no role here
// either (L24): the accessor authenticates with the session the login
// established.
package player

import (
	"context"
	"net/url"

	"github.com/nahuel/spotify-tui/internal/library"
)

// Catalog implements library.CatalogProvider: the UI drives every fetch from
// a tea.Cmd goroutine through this library.Fetch, never from Update.
func (p *Player) Catalog() library.Fetch {
	return library.NewWebAPI(library.WebAPIClientFunc(p.webApiCall))
}

// webApiCall is the one bridge between the catalog and Spotify: it resolves
// the current session and hands the request to its Web API accessor with 429
// retry. Calls before the session exists (or after it closed) fail with the
// not-logged-in error the UI renders as a state, never a crash.
func (p *Player) webApiCall(ctx context.Context, method, path string, query url.Values, body []byte) ([]byte, int, error) {
	p.mu.Lock()
	sess, closing := p.sess, p.closing
	p.mu.Unlock()
	if sess == nil || closing {
		return nil, 0, library.ErrNotLoggedIn
	}
	log := newCaptureLogger(p.authCfg.StateDir, nil)
	log.Debugf("catalog call %s %s?%s", method, path, query.Encode())
	body2, status, err := sess.WebApiWith429RetryAndReadBody(ctx, method, path, query, body)
	log.Debugf("catalog done %s %s status=%d err=%v bytes=%d", method, path, status, err, len(body2))
	return body2, status, err
}
