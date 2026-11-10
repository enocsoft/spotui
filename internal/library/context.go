// Playlist contents through the playback resolver. Instead of the Web API's
// GET /playlists/{id}/items — which answers 403 for a playlist the user does
// not own — the catalog resolves the playlist the same way playback does:
// sess.Spclient().ContextResolve(ctx, "spotify:playlist:<id>"). That call
// needs no Web API quota and has no owner restriction, which is why ncspot
// shows those playlists.
//
// The resolver answers a bounded set of context pages. The catalog maps them
// to the track model, slices them locally if asked by offset, and reports
// honestly what it could not show: entries without a playable uri (local
// files, episodes) are counted, and a context that leaves a next page behind
// (or lists fewer entries than the playlist reports) is marked truncated.
package library

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
	"github.com/elxgy/go-librespot/spclient"
)

// ContextResolver is the narrow seam for playlist contents: it resolves one
// Spotify context uri into its play context — exactly what sess.Spclient().
// ContextResolve does for the player. Tests substitute fixtures; the player
// wires the real session accessor (player.Catalog).
type ContextResolver func(ctx context.Context, uri string) (*connectpb.Context, error)

// MetadataBatch is the optional fallback seam for names the context entries
// do not carry: one batched extended-metadata lookup keyed by track uri
// (sess.Spclient().ResolveTrackOrEpisodeMetadataBatch). A nil batch simply
// leaves missing names empty.
type MetadataBatch func(ctx context.Context, uris []string) (map[string]spclient.ResolvedEntry, error)

// playlistContextURI builds the context uri the resolver expects.
func playlistContextURI(playlistID string) string {
	return "spotify:playlist:" + playlistID
}

// itemsViaContext resolves the playlist context once and maps every playable
// entry to the track model. offset slices the resolved set locally — the
// resolver is bounded, so there is only ever one real page and NextOffset is
// -1: the UI must never wait for a second context resolve that will not come.
func itemsViaContext(ctx context.Context, playlistID string, offset int, resolve ContextResolver, batch MetadataBatch) (ItemsPage, error) {
	if resolve == nil {
		return ItemsPage{}, fmt.Errorf("no playlist contents resolver — the playback session is not up")
	}
	cctx, cancel := context.WithTimeout(ctx, catalogTimeout)
	defer cancel()
	spotCtx, err := resolve(cctx, playlistContextURI(playlistID))
	if err != nil {
		// The reason surfaces verbatim: the UI renders it in the error state.
		return ItemsPage{}, fmt.Errorf("resolving playlist %s: %w", playlistID, err)
	}

	page := ItemsPage{Offset: offset}
	var needNames []string
	for _, p := range spotCtx.Pages {
		for _, ct := range p.Tracks {
			t, playable := trackFromContext(ct)
			if playable {
				page.Tracks = append(page.Tracks, t)
				if t.Title == "" || t.Artist == "" || t.Duration == 0 {
					needNames = append(needNames, "spotify:track:"+t.ID)
				}
			} else {
				page.Skipped++
			}
		}
	}

	// Names the context did not carry: one batched lookup, best effort. A
	// batch failure never fails the whole page — the row renders with what
	// the context itself gave.
	if batch != nil && len(needNames) > 0 {
		if resolved, berr := batch(ctx, needNames); berr == nil {
			for i := range page.Tracks {
				e, ok := resolved["spotify:track:"+page.Tracks[i].ID]
				if !ok {
					continue
				}
				if page.Tracks[i].Title == "" {
					page.Tracks[i].Title = e.Name
				}
				if page.Tracks[i].Artist == "" {
					page.Tracks[i].Artist = e.Artist
				}
				if page.Tracks[i].Duration == 0 {
					page.Tracks[i].Duration = timeFromMS(e.DurationMS)
				}
			}
		}
	}

	// The bound, stated honestly: the playlist's own count when the context
	// carries it, otherwise what was resolved; truncated marks a resolve
	// that clearly did not cover the whole playlist.
	if expected, ok := playlistTrackCount(spotCtx); ok {
		page.Total = expected
	} else {
		page.Total = len(page.Tracks) + page.Skipped
	}
	page.Truncated = contextTruncated(spotCtx, len(page.Tracks)+page.Skipped, page.Total)

	// Local slicing for the UI's offset requests; the whole set is what the
	// resolver gave, so the next offset is always the end.
	if offset > 0 && offset < len(page.Tracks) {
		page.Tracks = page.Tracks[offset:]
	} else if offset > 0 {
		page.Tracks = nil
	}
	page.NextOffset = -1
	return page, nil
}

// trackFromContext maps one context entry to a track row or reports it
// unplayable (local files, episodes, ads, empty or malformed uris): those
// are counted, never silently dropped and never offered to the player.
func trackFromContext(ct *connectpb.ContextTrack) (Track, bool) {
	if ct == nil {
		return Track{}, false
	}
	uri := ct.Uri
	if uri == "" {
		uri = ct.Metadata["entity_uri"] // the entry's own spotify:track: uri
	}
	id, err := ParseTrackID(uri)
	if err != nil {
		return Track{}, false
	}
	return Track{
		ID:       id,
		Title:    meta(ct.Metadata, "title", "track_name"),
		Artist:   meta(ct.Metadata, "artist_name", "artist"),
		Album:    meta(ct.Metadata, "album_name", "album"),
		Duration: durationFromMeta(ct.Metadata["duration"], ct.Metadata["duration_ms"]),
	}, true
}

// meta reads the first present key. Spotify's context-resolve json is not
// pinned by go-librespot's types (the player resolves display metadata per
// uri instead), so both spellings are accepted and documented here.
func meta(md map[string]string, keys ...string) string {
	for _, k := range keys {
		if v := md[k]; v != "" {
			return v
		}
	}
	return ""
}

func timeFromMS(ms int) time.Duration {
	return time.Duration(ms) * time.Millisecond
}

// durationFromMeta parses the context duration, which arrives as a
// millisecond string under either key.
func durationFromMeta(values ...string) time.Duration {
	for _, v := range values {
		if ms, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && ms > 0 {
			return time.Duration(ms) * time.Millisecond
		}
	}
	return 0
}

// playlistTrackCount reads the playlist's own count from the context
// metadata — the same key go-librespot's context resolver consults to decide
// whether the pages it got cover the whole playlist.
func playlistTrackCount(spotCtx *connectpb.Context) (int, bool) {
	v := spotCtx.Metadata["playlist_number_of_tracks"]
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		return n, true
	}
	return 0, false
}

// contextTruncated reports whether the resolved set clearly does not cover
// the playlist: either the playlist reports more entries than were resolved,
// or a context page still points at a next page the resolver never followed.
func contextTruncated(spotCtx *connectpb.Context, seen, total int) bool {
	if total > seen {
		return true
	}
	if n := len(spotCtx.Pages); n > 0 && spotCtx.Pages[n-1].NextPageUrl != "" {
		return true
	}
	return false
}
