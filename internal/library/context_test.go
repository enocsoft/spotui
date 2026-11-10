// Playlist contents through the playback resolver (2026 work to replace the
// Web API items endpoint): the same ContextResolve playback uses for a
// playlist context — no Web API quota, no owner restriction. The seam is a
// func type, so fixtures hand back a connectpb.Context or an error with no
// session and no network.
package library

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
	"github.com/elxgy/go-librespot/spclient"
)

// fakeResolver is the injectable seam fixture: it records the uris it was
// asked to resolve and answers with a canned context or error.
type fakeResolver struct {
	uris []string
	ctx  *connectpb.Context
	err  error
}

func (f *fakeResolver) resolve(_ context.Context, uri string) (*connectpb.Context, error) {
	f.uris = append(f.uris, uri)
	if f.err != nil {
		return nil, f.err
	}
	return f.ctx, nil
}

// resolverAPI builds a catalog whose contents path rides the fake seam.
func resolverAPI(r *fakeResolver, batch MetadataBatch) *WebAPI {
	return NewWebAPI(nil).WithContentsResolver(r.resolve, batch)
}

func ctxPage(tracks ...*connectpb.ContextTrack) *connectpb.ContextPage {
	return &connectpb.ContextPage{Tracks: tracks}
}

// The regression that started this work: a playlist the user does not own
// opens with its contents. The resolver has no owner restriction, so these
// must load as tracks mapped from the context.
func TestItemsViaResolverLoadsForeignPlaylist(t *testing.T) {
	r := &fakeResolver{ctx: &connectpb.Context{
		Uri: "spotify:playlist:pl2",
		Metadata: map[string]string{
			"playlist_number_of_tracks": "2",
		},
		Pages: []*connectpb.ContextPage{ctxPage(
			&connectpb.ContextTrack{Uri: "spotify:track:4uLU6hMCjMI75M1A2tKUQC", Metadata: map[string]string{
				"title": "So What", "artist_name": "Miles Davis", "album_name": "Kind of Blue", "duration": "562000",
			}},
			&connectpb.ContextTrack{Uri: "spotify:track:3uLU6hMCjMI75M1A2tKUQC", Metadata: map[string]string{
				"title": "Blue in Green", "artist_name": "Miles Davis", "album_name": "Kind of Blue", "duration": "338000",
			}},
		)},
	}}
	page, err := resolverAPI(r, nil).Items(context.Background(), "pl2", 0)
	if err != nil {
		t.Fatalf("Items via resolver: %v", err)
	}
	if len(page.Tracks) != 2 {
		t.Fatalf("tracks = %d, want 2 — a non-owned playlist must load now", len(page.Tracks))
	}
	tr := page.Tracks[0]
	if tr.ID != "4uLU6hMCjMI75M1A2tKUQC" || tr.Title != "So What" ||
		tr.Artist != "Miles Davis" || tr.Album != "Kind of Blue" {
		t.Errorf("track row = %+v, want the context metadata mapped", tr)
	}
	if tr.Duration != 562000*time.Millisecond {
		t.Errorf("duration = %v, want 9m22s from the context metadata", tr.Duration)
	}
	// The fake resolver must receive the exact context uri of this playlist.
	if got, want := strings.Join(r.uris, ","), "spotify:playlist:pl2"; got != want {
		t.Errorf("resolver uris = %q, want %q", got, want)
	}
	for _, got := range r.uris {
		if got != "spotify:playlist:pl2" {
			t.Errorf("resolver got uri %q, want the exact spotify:playlist:<id> form", got)
		}
	}
}

// The fake resolver backed the exact context uri even for a different id —
// pinned on its own to keep the uri form from drifting.
func TestItemsViaResolverUsesTheExactContextUri(t *testing.T) {
	r := &fakeResolver{ctx: &connectpb.Context{
		Pages: []*connectpb.ContextPage{ctxPage(&connectpb.ContextTrack{
			Uri:      "spotify:track:9uLU6hMCjMI75M1A2tKUQC",
			Metadata: map[string]string{"title": "T", "artist_name": "a", "album_name": "b", "duration": "1000"},
		})},
	}}
	if _, err := resolverAPI(r, nil).Items(context.Background(), "37i9dQZF1DXcBWIGoYBM5M", 0); err != nil {
		t.Fatalf("Items: %v", err)
	}
	if len(r.uris) != 1 || r.uris[0] != "spotify:playlist:37i9dQZF1DXcBWIGoYBM5M" {
		t.Errorf("resolver uris = %v, want exactly one spotify:playlist:<id> uri", r.uris)
	}
}

// A resolver failure is an error state carrying the reason, never an empty
// table pretending the playlist has no tracks.
func TestItemsViaResolverErrorSurfacesTheReason(t *testing.T) {
	r := &fakeResolver{err: errors.New("invalid status code from context resolve: 502")}
	page, err := resolverAPI(r, nil).Items(context.Background(), "pl2", 0)
	if err == nil {
		t.Fatalf("a resolver failure must be an error, got page %+v", page)
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("the error should carry the resolver's reason, got %q", err.Error())
	}
	if len(page.Tracks) != 0 {
		t.Errorf("a failed resolve must not return rows, got %+v", page.Tracks)
	}
}

// Entries without a playable uri (local files, episodes) are skipped and
// counted, never silently dropped.
func TestItemsViaResolverSkipsUnplayableAndCounts(t *testing.T) {
	r := &fakeResolver{ctx: &connectpb.Context{
		Pages: []*connectpb.ContextPage{ctxPage(
			&connectpb.ContextTrack{Uri: "spotify:track:4uLU6hMCjMI75M1A2tKUQC", Metadata: map[string]string{
				"title": "So What", "artist_name": "Miles Davis", "album_name": "Kind of Blue", "duration": "562000",
			}},
			&connectpb.ContextTrack{Uri: "spotify:local:The+Beatles:Let+It+Be:96:320", Metadata: nil},
			&connectpb.ContextTrack{Uri: "spotify:episode:0VuagF(schemaless", Metadata: nil},
			&connectpb.ContextTrack{Uri: "", Metadata: nil},
			&connectpb.ContextTrack{Uri: "spotify:track:not-a-real-track-id----", Metadata: nil},
		)},
	}}
	page, err := resolverAPI(r, nil).Items(context.Background(), "pl1", 0)
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	if len(page.Tracks) != 1 {
		t.Fatalf("tracks = %d, want 1 (only the playable track renders)", len(page.Tracks))
	}
	if page.Skipped != 4 {
		t.Errorf("Skipped = %d, want 4 (local file, episode, empty uri, bad id)", page.Skipped)
	}
	if page.Tracks[0].ID != "4uLU6hMCjMI75M1A2tKUQC" {
		t.Errorf("kept track = %+v, want the playable one", page.Tracks[0])
	}
}

// A context entry whose metadata lacks names falls back to the batch
// metadata resolver; entries with complete metadata never call it.
func TestItemsViaResolverBatchMetadataFillsMissingNames(t *testing.T) {
	const uri = "spotify:track:4uLU6hMCjMI75M1A2tKUQC"
	r := &fakeResolver{ctx: &connectpb.Context{
		Pages: []*connectpb.ContextPage{ctxPage(
			&connectpb.ContextTrack{Uri: uri, Metadata: nil}, // no names in the context
		)},
	}}
	calls := 0
	batch := MetadataBatch(func(_ context.Context, uris []string) (map[string]spclient.ResolvedEntry, error) {
		calls++
		if len(uris) != 1 || uris[0] != uri {
			t.Errorf("batch uris = %v, want [%s]", uris, uri)
		}
		return map[string]spclient.ResolvedEntry{
			uri: {Name: "So What", Artist: "Miles Davis", DurationMS: 562000},
		}, nil
	})
	page, err := resolverAPI(r, batch).Items(context.Background(), "pl1", 0)
	if err != nil {
		t.Fatalf("Items: %v", err)
	}
	if calls != 1 {
		t.Fatalf("batch calls = %d, want 1", calls)
	}
	tr := page.Tracks[0]
	if tr.Title != "So What" || tr.Artist != "Miles Davis" || tr.Duration != 562000*time.Millisecond {
		t.Errorf("track = %+v, want names filled from the batch metadata", tr)
	}
}

// The truncated bound is honest: when the resolver returns fewer entries
// than the playlist reports (or leaves a next-page url behind), the page says
// so instead of pretending the playlist is complete.
func TestItemsViaResolverTruncationIsHonest(t *testing.T) {
	t.Run("fewer entries than the playlist reports", func(t *testing.T) {
		r := &fakeResolver{ctx: &connectpb.Context{
			Metadata: map[string]string{"playlist_number_of_tracks": "42"},
			Pages: []*connectpb.ContextPage{ctxPage(
				&connectpb.ContextTrack{Uri: "spotify:track:4uLU6hMCjMI75M1A2tKUQC", Metadata: map[string]string{
					"title": "So What", "artist_name": "Miles Davis", "album_name": "Kind of Blue", "duration": "562000",
				}},
			)},
		}}
		page, err := resolverAPI(r, nil).Items(context.Background(), "pl1", 0)
		if err != nil {
			t.Fatalf("Items: %v", err)
		}
		if !page.Truncated {
			t.Errorf("Truncated = false, want true: 42 tracks reported, 1 resolved")
		}
		if page.Total != 42 {
			t.Errorf("Total = %d, want the playlist's own count 42", page.Total)
		}
		if page.NextOffset != -1 {
			t.Errorf("NextOffset = %d, want -1: the resolver set is bounded, no further page exists", page.NextOffset)
		}
	})
	t.Run("complete context is not marked truncated", func(t *testing.T) {
		r := &fakeResolver{ctx: &connectpb.Context{
			Metadata: map[string]string{"playlist_number_of_tracks": "1"},
			Pages: []*connectpb.ContextPage{ctxPage(
				&connectpb.ContextTrack{Uri: "spotify:track:4uLU6hMCjMI75M1A2tKUQC", Metadata: map[string]string{
					"title": "So What", "artist_name": "Miles Davis", "album_name": "Kind of Blue", "duration": "562000",
				}},
			)},
		}}
		page, err := resolverAPI(r, nil).Items(context.Background(), "pl1", 0)
		if err != nil {
			t.Fatalf("Items: %v", err)
		}
		if page.Truncated || page.Total != 1 {
			t.Errorf("complete context = %+v, want Truncated=false Total=1", page)
		}
	})
	t.Run("a next page url the resolver did not follow means a subset", func(t *testing.T) {
		r := &fakeResolver{ctx: &connectpb.Context{
			Pages: []*connectpb.ContextPage{
				ctxPage(&connectpb.ContextTrack{Uri: "spotify:track:4uLU6hMCjMI75M1A2tKUQC", Metadata: nil}),
				{Tracks: []*connectpb.ContextTrack{}, NextPageUrl: "hm://path/to/next/page"},
			},
		}}
		page, err := resolverAPI(r, nil).Items(context.Background(), "pl1", 0)
		if err != nil {
			t.Fatalf("Items: %v", err)
		}
		if !page.Truncated {
			t.Errorf("Truncated = false, want true: the resolver left a next-page url")
		}
	})
}

// Paging stays local: the resolved set is sliced by offset because the UI
// still asks by offset; there is never a follow-up request.
func TestItemsViaResolverSlicesLocally(t *testing.T) {
	mk := func(base byte, title string) *connectpb.ContextTrack {
		return &connectpb.ContextTrack{Uri: "spotify:track:" + strings.Repeat("A", 21) + string(base),
			Metadata: map[string]string{"title": title, "artist_name": "a", "album_name": "b", "duration": "1000"}}
	}
	r := &fakeResolver{ctx: &connectpb.Context{
		Pages: []*connectpb.ContextPage{ctxPage(
			mk('1', "First"), mk('2', "Second"), mk('3', "Third"),
		)},
	}}
	page, err := resolverAPI(r, nil).Items(context.Background(), "pl1", 1)
	if err != nil {
		t.Fatalf("Items offset 1: %v", err)
	}
	if len(page.Tracks) != 2 || page.Tracks[0].Title != "Second" || page.Tracks[1].Title != "Third" {
		t.Errorf("sliced page = %+v, want the entries after offset 1", page.Tracks)
	}
	if page.NextOffset != -1 {
		t.Errorf("NextOffset = %d, want -1", page.NextOffset)
	}
}

// A catalog wired without the resolver seam must say so instead of hitting
// the dead Web API path.
func TestItemsWithoutResolverIsAnError(t *testing.T) {
	if _, err := NewWebAPI(nil).Items(context.Background(), "pl1", 0); err == nil {
		t.Fatal("Items without a wired resolver must be an error, not a silent empty page")
	}
}

// The resolved context's own display name rides the page when the resolver's
// metadata carries it (:play with a context target names the strip with it,
// T25); an album resolve answers with the album spelling.
func TestItemsViaResolverCarriesTheContextName(t *testing.T) {
	track := func() *connectpb.ContextTrack {
		return &connectpb.ContextTrack{Uri: "spotify:track:4uLU6hMCjMI75M1A2tKUQC", Metadata: map[string]string{
			"title": "So What", "artist_name": "Miles Davis", "album_name": "Kind of Blue", "duration": "562000",
		}}
	}
	t.Run("playlist name", func(t *testing.T) {
		r := &fakeResolver{ctx: &connectpb.Context{
			Uri:      "spotify:playlist:pl2",
			Metadata: map[string]string{"playlist_name": "Road Trip"},
			Pages:    []*connectpb.ContextPage{ctxPage(track())},
		}}
		page, err := resolverAPI(r, nil).Items(context.Background(), "pl2", 0)
		if err != nil {
			t.Fatalf("Items: %v", err)
		}
		if page.Name != "Road Trip" {
			t.Errorf("page name = %q, want the context's own playlist_name", page.Name)
		}
	})
	t.Run("album name", func(t *testing.T) {
		r := &fakeResolver{ctx: &connectpb.Context{
			Uri:      "spotify:album:al1",
			Metadata: map[string]string{"album_name": "Kind of Blue"},
			Pages:    []*connectpb.ContextPage{ctxPage(track())},
		}}
		page, err := resolverAPI(r, nil).AlbumItems(context.Background(), "al1", 0)
		if err != nil {
			t.Fatalf("AlbumItems: %v", err)
		}
		if page.Name != "Kind of Blue" {
			t.Errorf("page name = %q, want the context's own album_name", page.Name)
		}
	})
	t.Run("no name in the metadata is an empty name, never invented", func(t *testing.T) {
		r := &fakeResolver{ctx: &connectpb.Context{
			Pages: []*connectpb.ContextPage{ctxPage(track())},
		}}
		page, err := resolverAPI(r, nil).Items(context.Background(), "pl2", 0)
		if err != nil {
			t.Fatalf("Items: %v", err)
		}
		if page.Name != "" {
			t.Errorf("page name = %q, want empty when the context carries none", page.Name)
		}
	})
}

// T22: albums are the second caller of the same resolver — the exact context
// uri is spotify:album:<id> (no Web API quota, no owner restriction), and the
// mapping/mapping skip/truncation logic is the playlist path's, not a copy.
func TestAlbumItemsViaResolverUsesTheExactAlbumUri(t *testing.T) {
	r := &fakeResolver{ctx: &connectpb.Context{
		Pages: []*connectpb.ContextPage{ctxPage(
			&connectpb.ContextTrack{Uri: "spotify:track:4uLU6hMCjMI75M1A2tKUQC", Metadata: map[string]string{
				"title": "So What", "artist_name": "Miles Davis", "album_name": "Kind of Blue", "duration": "562000",
			}},
			&connectpb.ContextTrack{Uri: "spotify:local:The+Beatles:Let+It+Be:96:320", Metadata: nil},
		)},
	}}
	page, err := resolverAPI(r, nil).AlbumItems(context.Background(), "al1", 0)
	if err != nil {
		t.Fatalf("AlbumItems via resolver: %v", err)
	}
	if got, want := strings.Join(r.uris, ","), "spotify:album:al1"; got != want {
		t.Errorf("resolver uris = %q, want exactly spotify:album:<id>", got)
	}
	if len(page.Tracks) != 1 || page.Tracks[0].ID != "4uLU6hMCjMI75M1A2tKUQC" ||
		page.Tracks[0].Title != "So What" || page.Tracks[0].Artist != "Miles Davis" ||
		page.Tracks[0].Album != "Kind of Blue" || page.Tracks[0].Duration != 562000*time.Millisecond {
		t.Errorf("album tracks = %+v, want the context metadata mapped", page.Tracks)
	}
	if page.Skipped != 1 {
		t.Errorf("Skipped = %d, want 1 — the unplayable entry is counted, not dropped", page.Skipped)
	}
	if page.NextOffset != -1 {
		t.Errorf("NextOffset = %d, want -1: the resolver set is bounded", page.NextOffset)
	}
}

// A resolver failure on an album is an error state carrying the reason,
// never an empty table pretending the album has no tracks.
func TestAlbumItemsViaResolverErrorSurfacesTheReason(t *testing.T) {
	r := &fakeResolver{err: errors.New("invalid status code from context resolve: 502")}
	page, err := resolverAPI(r, nil).AlbumItems(context.Background(), "al1", 0)
	if err == nil {
		t.Fatalf("a resolver failure must be an error, got page %+v", page)
	}
	if !strings.Contains(err.Error(), "502") {
		t.Errorf("the error should carry the resolver's reason, got %q", err.Error())
	}
	if len(page.Tracks) != 0 {
		t.Errorf("a failed resolve must not return rows, got %+v", page.Tracks)
	}
}

// TestEnrichTracksFillsWhatTheContextLacks: autoplay contexts carry the uri and
// nothing else, so without this the track plays with an empty title.
func TestEnrichTracksFillsWhatTheContextLacks(t *testing.T) {
	tracks := []Track{
		{ID: "4uLU6hMCjMI75M1A2tKUQC"}, // nothing but the id
		{ID: "0VjIjW4GlUZAMYd2vXMi3b", Title: "Known", Artist: "Someone", Duration: 90 * time.Second},
	}
	batch := func(ctx context.Context, uris []string) (map[string]spclient.ResolvedEntry, error) {
		if len(uris) != 1 || uris[0] != "spotify:track:4uLU6hMCjMI75M1A2tKUQC" {
			t.Errorf("batch asked for %v, want only the entry missing names", uris)
		}
		return map[string]spclient.ResolvedEntry{
			"spotify:track:4uLU6hMCjMI75M1A2tKUQC": {Name: "Never Gonna Give You Up", Artist: "Rick Astley", DurationMS: 213000},
		}, nil
	}
	got := EnrichTracks(context.Background(), tracks, batch)
	if got[0].Title != "Never Gonna Give You Up" || got[0].Artist != "Rick Astley" {
		t.Errorf("the empty entry was not filled: %+v", got[0])
	}
	if got[1].Title != "Known" || got[1].Artist != "Someone" {
		t.Errorf("an entry that already had names must not be touched: %+v", got[1])
	}
}

// TestEnrichTracksIsBestEffort: a failing batch leaves the tracks as they came.
func TestEnrichTracksIsBestEffort(t *testing.T) {
	tracks := []Track{{ID: "4uLU6hMCjMI75M1A2tKUQC"}}
	batch := func(ctx context.Context, uris []string) (map[string]spclient.ResolvedEntry, error) {
		return nil, errors.New("boom")
	}
	got := EnrichTracks(context.Background(), tracks, batch)
	if len(got) != 1 || got[0].Title != "" {
		t.Errorf("a batch failure must not change the tracks: %+v", got)
	}
}
