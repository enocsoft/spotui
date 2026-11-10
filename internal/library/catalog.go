// Catalog model for the real Spotify library (T7): the types behind the
// 2026 Web API constraints (spec S14) plus the Fetch interface the app
// drives from tea.Cmd goroutines — every method blocks, so none of them may
// ever be called from bubbletea's Update.
package library

import (
	"context"
	"fmt"
	"strings"
)

// NoTrackCount marks a playlist whose track count the 2026 API does not
// expose (the user is not the owner or a collaborator): the row renders ·
// (not-applicable), never a fake 0.
const NoTrackCount = -1

// PlaylistSummary is one row of the playlists browse view.
type PlaylistSummary struct {
	ID            string
	Name          string
	Owner         string
	Private       bool
	Collaborative bool
	TrackCount    int // NoTrackCount when the 2026 API hides the contents
}

// PlaylistPage is one lazily fetched page of GET /v1/me/playlists. NextOffset
// is the offset of the following page, or -1 when the list is exhausted: the
// client never fetches everything up front.
type PlaylistPage struct {
	Playlists  []PlaylistSummary
	Offset     int
	Total      int
	NextOffset int
}

// ItemsPage is one page of playlist contents. Since the resolver swap the
// contents come from the playback session's ContextResolve (context.go), not
// the Web API: no owner restriction, but the resolver answers one bounded
// set, so the page states honestly what it could not show — Skipped counts
// entries without a playable uri (local files, episodes) and Truncated marks
// a resolve that did not cover the whole playlist.
type ItemsPage struct {
	Tracks     []Track
	Offset     int
	Total      int
	NextOffset int
	Skipped    int    // entries without a playable uri, counted not dropped
	Truncated  bool   // the resolver returned a subset of the playlist
	Name       string // the context's own display name when the resolve carries one; empty means unknown
}

// AlbumSummary is one album row: a search result (T22) or a saved album of
// the library's ALBUMS group (T23). Year is the front of release_date and
// TrackCount the collection's own count; search summaries leave both unset
// (the search shape the app parses carries neither), and the row renders
// only the facts it actually has.
type AlbumSummary struct {
	ID         string
	Name       string
	Artist     string
	Year       string
	TrackCount int
}

// AlbumPage is one lazily fetched page of GET /v1/me/albums. NextOffset is
// the offset of the following page, or -1 when the list is exhausted: the
// client never fetches everything up front (the playlists paging, T23).
type AlbumPage struct {
	Albums     []AlbumSummary
	Offset     int
	Total      int
	NextOffset int
}

// SearchTotals are the per-type totals of one search response.
type SearchTotals struct {
	Tracks    int
	Albums    int
	Playlists int
}

// SearchPage is one page of GET /v1/search: the 2026 API caps limit at 10,
// so pagination is offset-driven and pages append to what the window shows.
// NextOffset is -1 when no type has more rows.
type SearchPage struct {
	Query      string
	Offset     int
	Tracks     []Track
	Albums     []AlbumSummary
	Playlists  []PlaylistSummary
	Totals     SearchTotals
	NextOffset int
}

// Fetch is the blocking Web API catalog. The app runs every method inside a
// tea.Cmd goroutine and applies the result as a message; nothing else in the
// TUI talks to Spotify's Web API.
type Fetch interface {
	// Playlists fetches one page of the user's playlists (50 per page).
	Playlists(ctx context.Context, offset int) (PlaylistPage, error)
	// SavedAlbums fetches one page of the user's saved albums (50 per page,
	// the same lazy paging as playlists, T23).
	SavedAlbums(ctx context.Context, offset int) (AlbumPage, error)
	// Items returns one slice of a playlist's contents resolved through the
	// playback session (no Web API quota, no owner restriction). The set is
	// bounded; Skipped/Truncated state the parts it could not show.
	Items(ctx context.Context, playlistID string, offset int) (ItemsPage, error)
	// AlbumItems returns one slice of an album's tracks through the same
	// context resolver (spotify:album:<id>): the second caller of the same
	// bounded resolve (T22).
	AlbumItems(ctx context.Context, albumID string, offset int) (ItemsPage, error)
	// Search queries tracks, albums and playlists (limit cap 10).
	Search(ctx context.Context, query string, offset int) (SearchPage, error)
	// SetSaved toggles the saved state through the generic 2026 endpoints.
	SetSaved(ctx context.Context, trackID string, saved bool) error
}

// CatalogProvider is implemented by data sources that can fetch the real
// catalog (the embedded player, once its session is up). The mock does not
// implement it: mock mode stays mock (spec S10).
type CatalogProvider interface {
	Catalog() Fetch
}

// APIError carries the Spotify status and the message when the body has one
// (scope 6: errors are specific, never a bare "failed").
type APIError struct {
	Status  int
	Message string
	// RateLimited marks a 429: the session accessor retries internally; when
	// the status still surfaces here the UI shows the existing rate-limit
	// state instead of inventing its own retry loop.
	RateLimited bool
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = "spotify api error"
	}
	return fmt.Sprintf("spotify %d · %s", e.Status, msg)
}

// IsRateLimited reports whether err is (or wraps) a 429 APIError.
func IsRateLimited(err error) bool {
	var apiErr *APIError
	if !asAPIError(err, &apiErr) {
		return false
	}
	return apiErr.RateLimited
}

// asAPIError unwraps err through errors.As while keeping the single-return
// call sites tidy.
func asAPIError(err error, target **APIError) bool {
	for err != nil {
		if e, ok := err.(*APIError); ok {
			*target = e
			return true
		}
		inner, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = inner.Unwrap()
	}
	return false
}

// shorten cuts a status line to a sane one-row length for toasts.
func shorten(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.TrimSpace(s[:n-1]) + "…"
}
