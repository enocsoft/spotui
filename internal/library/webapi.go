// The real Web API catalog (T7). Every call goes through one transport —
// library.WebAPIClient, which the plain net/http HTTP client implements for
// the app and tests replace with httptest fixtures.
//
// Playlist contents are the exception on purpose: since the resolver swap,
// items no longer ride the Web API at all — GET /playlists/{id}/items
// answered 403 for a playlist the user does not own (2026 API constraint,
// spec S14) — because the catalog resolves them through the playback
// session's spclient instead (library.ContextResolver, wired by
// player.Catalog). That call needs no Web API quota and has no owner
// restriction, which is why ncspot shows those playlists.
//
// What still rides the Web API (2026 constraints baked in, spec S14, L7):
//   - playlists: GET /v1/me/playlists, 50 per page, `next` followed lazily;
//   - search: GET /v1/search with limit capped at 10, offset pagination;
//   - saved toggle: generic PUT /v1/library and DELETE /me/library — the
//     per-type endpoints (me/tracks, …) are gone;
//   - 429 is never retried invisibly: one surfaced 429 becomes the
//     rate-limit state, not a retry loop here.
package library

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// playlistPageLimit is the page size of every paged catalog fetch (scope 1:
// follow `next` lazily, never fetch everything up front).
const playlistPageLimit = 50

// searchLimit is the 2026 cap for GET /v1/search (spec S14).
const searchLimit = 10

// catalogTimeout bounds one catalog call; a hung request must become an error
// state, not a frozen window.
const catalogTimeout = 120 * time.Second

// WebAPIClient is the transport the catalog's requests ride on: one request
// in, one status-and-body out. The app wires it to the plain net/http HTTP
// client over the library's own token (webapi_http.go); tests replace it
// with an httptest-backed fixture so the suite stays credential-free and
// offline.
type WebAPIClient interface {
	Call(ctx context.Context, method, path string, query url.Values, body []byte) ([]byte, int, error)
}

// WebAPIClientFunc adapts a function to WebAPIClient.
type WebAPIClientFunc func(ctx context.Context, method, path string, query url.Values, body []byte) ([]byte, int, error)

// Call implements WebAPIClient.
func (f WebAPIClientFunc) Call(ctx context.Context, method, path string, query url.Values, body []byte) ([]byte, int, error) {
	return f(ctx, method, path, query, body)
}

// WebAPI implements library.Fetch on top of a WebAPIClient. Playlist
// contents ride the playback resolver when one is wired (player.Catalog
// does); without it Items fails with the honest error the UI renders. Safe
// for concurrent use as long as the client is (the session accessor is).
type WebAPI struct {
	client  WebAPIClient
	resolve ContextResolver // the playback context resolver,
	batch   MetadataBatch   // fallback names for thin context entries
}

// NewWebAPI builds the real catalog over the given accessor.
func NewWebAPI(client WebAPIClient) *WebAPI { return &WebAPI{client: client} }

// WithContentsResolver wires the playlist-contents path to the playback
// resolver (sess.Spclient().ContextResolve) and the optional batch metadata
// fallback, both owned by the playback session. It returns the same WebAPI
// for wiring-time chaining.
func (w *WebAPI) WithContentsResolver(resolve ContextResolver, batch MetadataBatch) *WebAPI {
	w.resolve, w.batch = resolve, batch
	return w
}

// webAPIError turns a non-2xx response into an *APIError, parsing Spotify's
// error body ({"error":{"status":..,"message":".."}}) when present.
func webAPIError(status int, body []byte, err error) error {
	if err != nil {
		if strings.Contains(err.Error(), "429") || strings.Contains(strings.ToLower(err.Error()), "rate limit") {
			return &APIError{Status: 429, Message: err.Error(), RateLimited: true}
		}
		return err
	}
	apiErr := &APIError{Status: status}
	var parsed struct {
		Error struct {
			Status  int    `json:"status"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &parsed) == nil && parsed.Error.Message != "" {
		apiErr.Message = parsed.Error.Message
		if parsed.Error.Status == 429 {
			apiErr.RateLimited = true
		}
	}
	if status == 429 {
		apiErr.RateLimited = true
	}
	return apiErr
}

// call runs one request through the accessor and returns the body on 2xx.
func (w *WebAPI) call(ctx context.Context, method, path string, query url.Values) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	body, status, err := w.client.Call(cctx, method, path, query, nil)
	if err != nil {
		return nil, webAPIError(status, body, err)
	}
	if status < 200 || status > 299 {
		return nil, webAPIError(status, body, nil)
	}
	return body, nil
}

// pageJSON decodes one page and computes NextOffset from total/offset/limit —
// lazy paging: each page is fetched only when the UI asks for it.
func nextOffset(offset, limit, total int) int {
	if limit > 0 && offset+limit < total {
		return offset + limit
	}
	return -1
}

// --- GET /v1/me/playlists ---

func (w *WebAPI) Playlists(ctx context.Context, offset int) (PlaylistPage, error) {
	q := url.Values{}
	q.Set("limit", strconv.Itoa(playlistPageLimit))
	q.Set("offset", strconv.Itoa(offset))
	body, err := w.call(ctx, "GET", "v1/me/playlists", q)
	if err != nil {
		return PlaylistPage{}, err
	}
	var raw struct {
		Total int `json:"total"`
		Items []struct {
			ID    string `json:"id"`
			Name  string `json:"name"`
			Owner struct {
				DisplayName string `json:"display_name"`
			} `json:"owner"`
			Public        bool `json:"public"`
			Collaborative bool `json:"collaborative"`
			// 2026 rename (S14): the playlist object counts its tracks under
			// `items`, and the key is absent when the user cannot see them.
			Items *struct {
				Total int `json:"total"`
			} `json:"items"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return PlaylistPage{}, fmt.Errorf("decoding playlists: %w", err)
	}
	page := PlaylistPage{Offset: offset, Total: raw.Total}
	for _, it := range raw.Items {
		count := NoTrackCount
		if it.Items != nil {
			count = it.Items.Total
		}
		page.Playlists = append(page.Playlists, PlaylistSummary{
			ID:            it.ID,
			Name:          it.Name,
			Owner:         it.Owner.DisplayName,
			Private:       !it.Public,
			Collaborative: it.Collaborative,
			TrackCount:    count,
		})
	}
	page.NextOffset = nextOffset(offset, playlistPageLimit, raw.Total)
	return page, nil
}

// --- GET /v1/me/albums (T23: saved albums, the ALBUMS group of the
// Playlists view) ---

// SavedAlbums fetches one page of the user's saved albums over the same
// Web API token as playlists: 50 per page, offset pagination, `next`
// followed lazily. The saved-albums object wraps the album one level down,
// and the row keeps only the facts the 2026 shape carries: id, name,
// artist, the year (front of release_date) and the album's own track
// count. There is no owner/privacy fact to invent.
func (w *WebAPI) SavedAlbums(ctx context.Context, offset int) (AlbumPage, error) {
	q := url.Values{}
	q.Set("limit", strconv.Itoa(playlistPageLimit))
	q.Set("offset", strconv.Itoa(offset))
	body, err := w.call(ctx, "GET", "v1/me/albums", q)
	if err != nil {
		return AlbumPage{}, err
	}
	var raw struct {
		Total int `json:"total"`
		Items []struct {
			Album struct {
				ID      string `json:"id"`
				Name    string `json:"name"`
				Artists []struct {
					Name string `json:"name"`
				} `json:"artists"`
				ReleaseDate string `json:"release_date"`
				TotalTracks int    `json:"total_tracks"`
			} `json:"album"`
		} `json:"items"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return AlbumPage{}, fmt.Errorf("decoding saved albums: %w", err)
	}
	page := AlbumPage{Offset: offset, Total: raw.Total}
	for _, it := range raw.Items {
		page.Albums = append(page.Albums, AlbumSummary{
			ID:         it.Album.ID,
			Name:       it.Album.Name,
			Artist:     joinArtists(it.Album.Artists),
			Year:       releaseYear(it.Album.ReleaseDate),
			TrackCount: it.Album.TotalTracks,
		})
	}
	page.NextOffset = nextOffset(offset, playlistPageLimit, raw.Total)
	return page, nil
}

// releaseYear keeps the year of an ISO release date: "1959-08-17" and
// "1959" both render as 1959. Anything shorter is taken as-is — the row
// never invents a year.
func releaseYear(date string) string {
	if len(date) >= 4 {
		return date[:4]
	}
	return date
}

// --- playlist and album contents via the playback resolver ---

// Items resolves the playlist's contents the way playback does
// (sess.Spclient().ContextResolve, no Web API quota, no owner restriction.
// The mapping lives in context.go; the old Web API items endpoint is gone:
// it answered 403 for a playlist the user does not own (spec S14) and no
// fallback uses it any more.
func (w *WebAPI) Items(ctx context.Context, playlistID string, offset int) (ItemsPage, error) {
	return itemsViaContext(ctx, contextURI("playlist", playlistID), offset, w.resolve, w.batch)
}

// AlbumItems resolves a search-result album's tracks through the same
// resolver — the context uri is spotify:album:<id> (T22). Mapping, skipped
// counts and the bounded-resolve truncation are the playlist path's own
// (context.go): there is no second implementation.
func (w *WebAPI) AlbumItems(ctx context.Context, albumID string, offset int) (ItemsPage, error) {
	return itemsViaContext(ctx, contextURI("album", albumID), offset, w.resolve, w.batch)
}

// --- GET /v1/search ---

func (w *WebAPI) Search(ctx context.Context, query string, offset int) (SearchPage, error) {
	q := url.Values{}
	q.Set("q", query)
	q.Set("type", "track,album,playlist")
	q.Set("limit", strconv.Itoa(searchLimit)) // the 2026 cap is 10 (S14)
	q.Set("offset", strconv.Itoa(offset))
	body, err := w.call(ctx, "GET", "v1/search", q)
	if err != nil {
		return SearchPage{}, err
	}
	var raw struct {
		Tracks *searchSection[searchTrackItem] `json:"tracks"`
		Albums *searchSection[struct {
			ID      string `json:"id"`
			Name    string `json:"name"`
			Artists []struct {
				Name string `json:"name"`
			} `json:"artists"`
		}] `json:"albums"`
		Playlists *searchSection[playlistSummaryItem] `json:"playlists"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return SearchPage{}, fmt.Errorf("decoding search: %w", err)
	}
	page := SearchPage{Query: query, Offset: offset}
	more := -1
	if raw.Tracks != nil {
		page.Totals.Tracks = raw.Tracks.Total
		for _, it := range raw.Tracks.Items {
			page.Tracks = append(page.Tracks, Track{
				ID:       it.ID,
				Title:    it.Name,
				Artist:   joinArtists(it.Artists),
				Album:    it.Album.Name,
				Duration: time.Duration(it.DurationMs) * time.Millisecond,
			})
		}
		if n := nextOffset(offset, searchLimit, raw.Tracks.Total); n >= 0 {
			more = n
		}
	}
	if raw.Albums != nil {
		page.Totals.Albums = raw.Albums.Total
		for _, it := range raw.Albums.Items {
			page.Albums = append(page.Albums, AlbumSummary{
				ID:     it.ID,
				Name:   it.Name,
				Artist: joinArtists(it.Artists),
			})
		}
		if n := nextOffset(offset, searchLimit, raw.Albums.Total); n >= 0 {
			more = n
		}
	}
	if raw.Playlists != nil {
		page.Totals.Playlists = raw.Playlists.Total
		for _, it := range raw.Playlists.Items {
			page.Playlists = append(page.Playlists, it.summary())
		}
		if n := nextOffset(offset, searchLimit, raw.Playlists.Total); n >= 0 {
			more = n
		}
	}
	page.NextOffset = more
	return page, nil
}

// searchSection is one type section of the search response; sections are
// absent or empty when the offset is past that type's total.
type searchSection[T any] struct {
	Total  int `json:"total"`
	Offset int `json:"offset"`
	Items  []T `json:"items"`
}

type searchTrackItem struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	URI     string `json:"uri"`
	Artists []struct {
		Name string `json:"name"`
	} `json:"artists"`
	Album struct {
		Name string `json:"name"`
	} `json:"album"`
	DurationMs int64 `json:"duration_ms"`
}

func joinArtists(artists []struct {
	Name string `json:"name"`
}) string {
	var out []string
	for _, a := range artists {
		out = append(out, a.Name)
	}
	return strings.Join(out, ", ")
}

// playlistSummaryItem reuses the me/playlists playlist object shape.
type playlistSummaryItem struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Owner struct {
		DisplayName string `json:"display_name"`
	} `json:"owner"`
	Public        bool `json:"public"`
	Collaborative bool `json:"collaborative"`
	Items         *struct {
		Total int `json:"total"`
	} `json:"items"`
}

func (it playlistSummaryItem) summary() PlaylistSummary {
	count := NoTrackCount
	if it.Items != nil {
		count = it.Items.Total
	}
	return PlaylistSummary{
		ID:            it.ID,
		Name:          it.Name,
		Owner:         it.Owner.DisplayName,
		Private:       !it.Public,
		Collaborative: it.Collaborative,
		TrackCount:    count,
	}
}

// --- saved toggle ---

// SetSaved toggles the saved state through the generic 2026 endpoints: the
// per-type endpoints (me/tracks, me/albums, …) are gone (scope 4).
func (w *WebAPI) SetSaved(ctx context.Context, trackID string, saved bool) error {
	q := url.Values{}
	q.Set("ids", trackID)
	method, path := "PUT", "v1/library"
	if !saved {
		method, path = "DELETE", "me/library"
	}
	_, err := w.call(ctx, method, path, q)
	return err
}
