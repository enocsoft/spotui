// The catalog's transport wiring (T7, two tokens): the catalog talks directly
// to api.spotify.com over plain net/http with the library's own Web API
// token (the second login, minted for the dashboard client id) — never
// through the embedded session's accessor. A 401 refreshes once and retries
// once; a 429 surfaces as the rate-limit state with no retry storm.
package player

import (
	"github.com/nahuel/spotify-tui/internal/library"
)

// Catalog implements library.CatalogProvider: the UI drives every fetch from
// a tea.Cmd goroutine through this library.Fetch, never from Update.
func (p *Player) Catalog() library.Fetch {
	client := library.NewHTTPClient(p.webTokens)
	client.Base = p.webBase
	client.HTTP = p.http
	client.Logf = newCaptureLogger(p.authCfg.StateDir, nil).Debugf
	return library.NewWebAPI(client)
}
