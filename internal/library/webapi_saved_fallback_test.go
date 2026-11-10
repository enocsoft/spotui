// The SAVED group through the collection resolver (2026 work): GET
// /v1/me/tracks is still listed as available, but a Development Mode app can
// be denied it with a policy status — 410, or 403 as the other likely
// denial — while the route stays on the endpoint list. Saved songs are
// internally a collection context, so the fallback reads them the way
// playlist and album contents already do: the session's spclient resolves
// `spotify:user:<user id>:collection` and no Web API quota is spent.
//
// Offline: the Web API side is an httptest fixture, the resolver the
// injected fake seam (context_test.go); a successful Web API page must never
// touch the resolver, a denial must fall back and say so in the log, and
// when both routes fail the error must name both.
package library

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	connectpb "github.com/elxgy/go-librespot/proto/spotify/connectstate"
)

const savedDeniedBody = `{"error":{"status":%d,"message":"Endpoint is not available for this app"}}`

// collectionFixture builds a WebAPI whose /v1/me/tracks answers with the
// given denial status and whose resolver resolves the user's collection.
func collectionFixture(t *testing.T, status int, r *fakeResolver) (*WebAPI, *requestLog, *[]string) {
	t.Helper()
	client, log := fixtureClient(t, func(t *testing.T, req *http.Request) (int, string) {
		if req.URL.Path != "/v1/me/tracks" {
			t.Errorf("path = %q, want /v1/me/tracks only", req.URL.Path)
			return 400, `{}`
		}
		return status, fmt.Sprintf(savedDeniedBody, status)
	})
	logs := &[]string{}
	api := NewWebAPI(client).
		WithContentsResolver(r.resolve, nil).
		WithCollectionContext(func(context.Context) (string, error) {
			return "spotify:user:nahuel:collection", nil
		}).
		WithLogger(func(format string, args ...any) {
			*logs = append(*logs, fmt.Sprintf(format, args...))
		})
	return api, log, logs
}

// collectionSoWhat is one collection context page carrying one playable
// track, mapped the same way playlist contents are.
func collectionSoWhat() *connectpb.Context {
	return &connectpb.Context{
		Uri: "spotify:user:nahuel:collection",
		Pages: []*connectpb.ContextPage{ctxPage(
			&connectpb.ContextTrack{Uri: "spotify:track:4uLU6hMCjMI75M1A2tKUQC", Metadata: map[string]string{
				"title": "So What", "artist_name": "Miles Davis", "album_name": "Kind of Blue", "duration": "562000",
			}},
		)},
	}
}

// TestWebAPISavedTracks410FallsBackToCollectionResolve: a 410 policy denial
// from GET /v1/me/tracks falls back to the session's collection resolve, and
// the group loads — every mapped row Saved, the resolve leaving nothing to
// page (NextOffset -1).
func TestWebAPISavedTracks410FallsBackToCollectionResolve(t *testing.T) {
	r := &fakeResolver{ctx: collectionSoWhat()}
	api, log, logs := collectionFixture(t, 410, r)

	page, err := api.SavedTracks(context.Background(), 0)
	if err != nil {
		t.Fatalf("SavedTracks after a 410 must fall back to the collection: %v", err)
	}
	if got := strings.Join(r.uris, ","); got != "spotify:user:nahuel:collection" {
		t.Errorf("the fallback resolved [%s], want the saved-songs collection context", got)
	}
	log.want(t, "GET /v1/me/tracks?limit=50&offset=0") // the Web API side, then the resolver
	if len(page.Tracks) != 1 {
		t.Fatalf("tracks = %d, want the collection's 1", len(page.Tracks))
	}
	tr := page.Tracks[0]
	if tr.ID != "4uLU6hMCjMI75M1A2tKUQC" || tr.Title != "So What" || tr.Artist != "Miles Davis" || tr.Album != "Kind of Blue" {
		t.Errorf("track row = %+v, want the fixture values mapped from the context", tr)
	}
	if tr.Duration != 562000*time.Millisecond {
		t.Errorf("duration = %v, want 562000ms from the context metadata", tr.Duration)
	}
	if !tr.Saved {
		t.Errorf("every row of the saved list is saved by definition, got %+v", tr)
	}
	if page.NextOffset != -1 {
		t.Errorf("NextOffset = %d, want -1: the collection resolve answers one bounded set", page.NextOffset)
	}
	// The log proves which route served the list and why the fallback ran.
	joined := strings.Join(*logs, "\n")
	for _, want := range []string{
		"410",
		"Endpoint is not available for this app",
		"spotify:user:nahuel:collection",
		"served by",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("the saved-tracks log must name the denial and the fallback route, missing %q in:\n%s", want, joined)
		}
	}
}

// TestWebAPISavedTracks403FallsBackToCollectionResolve: 403 is the other
// likely policy denial and takes the same fallback.
func TestWebAPISavedTracks403FallsBackToCollectionResolve(t *testing.T) {
	r := &fakeResolver{ctx: collectionSoWhat()}
	api, _, _ := collectionFixture(t, 403, r)

	page, err := api.SavedTracks(context.Background(), 0)
	if err != nil {
		t.Fatalf("SavedTracks after a 403 must fall back to the collection: %v", err)
	}
	if got := strings.Join(r.uris, ","); got != "spotify:user:nahuel:collection" {
		t.Errorf("the fallback resolved [%s], want the saved-songs collection context", got)
	}
	if len(page.Tracks) != 1 || !page.Tracks[0].Saved {
		t.Fatalf("tracks = %+v, want the collection's saved row", page.Tracks)
	}
}

// TestWebAPISavedTracksBothRoutesFailErrorNamesBoth: when the Web API denial
// stands and the collection resolve also fails, the error names both routes
// with their reasons — never a bare "could not load".
func TestWebAPISavedTracksBothRoutesFailErrorNamesBoth(t *testing.T) {
	r := &fakeResolver{err: errors.New("collection resolve refused")}
	api, _, _ := collectionFixture(t, 410, r)

	_, err := api.SavedTracks(context.Background(), 0)
	if err == nil {
		t.Fatal("both routes failed: an error is required")
	}
	for _, want := range []string{
		"GET /v1/me/tracks", "410",
		"spotify:user:nahuel:collection", "collection resolve refused",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must name both failed routes, missing %q in %q", want, err.Error())
		}
	}
}

// TestWebAPISavedTracksBothRoutesFailWithoutSessionNamesTheWebAPISide: when
// not even the collection context uri can be named (no session), the error
// still names the Web API denial with its status — the legible half is kept.
func TestWebAPISavedTracksBothRoutesFailWithoutSessionNamesTheWebAPISide(t *testing.T) {
	client, _ := fixtureClient(t, func(t *testing.T, req *http.Request) (int, string) {
		return 410, fmt.Sprintf(savedDeniedBody, 410)
	})
	api := NewWebAPI(client).
		WithContentsResolver(func(context.Context, string) (*connectpb.Context, error) {
			return nil, errors.New("must not resolve without a context uri")
		}, nil).
		WithCollectionContext(func(context.Context) (string, error) {
			return "", errors.New("no embedded session")
		})
	_, err := api.SavedTracks(context.Background(), 0)
	if err == nil {
		t.Fatal("both routes failed: an error is required")
	}
	for _, want := range []string{"GET /v1/me/tracks", "410", "no embedded session"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must name the Web API denial and why no collection uri exists, missing %q in %q", want, err.Error())
		}
	}
}

// TestWebAPISavedTracksSuccessNeverCallsTheResolver: a normal 200 page from
// GET /v1/me/tracks is served by the Web API alone — the resolver stays
// untouched even when it is wired.
func TestWebAPISavedTracksSuccessNeverCallsTheResolver(t *testing.T) {
	page1 := `{"total":1,"offset":0,"next":null,"items":[` + svTrackSoWhat + `]}`
	client, log := fixtureClient(t, func(t *testing.T, req *http.Request) (int, string) {
		return 200, page1
	})
	r := &fakeResolver{err: errors.New("the resolver must never be asked on a healthy Web API page")}
	logs := &[]string{}
	api := NewWebAPI(client).
		WithContentsResolver(r.resolve, nil).
		WithCollectionContext(func(context.Context) (string, error) {
			return "spotify:user:nahuel:collection", nil
		}).
		WithLogger(func(format string, args ...any) { *logs = append(*logs, fmt.Sprintf(format, args...)) })

	page, err := api.SavedTracks(context.Background(), 0)
	if err != nil {
		t.Fatalf("SavedTracks: %v", err)
	}
	if len(r.uris) != 0 {
		t.Fatalf("a healthy Web API page must not touch the resolver, resolved %v", r.uris)
	}
	if len(page.Tracks) != 1 || !page.Tracks[0].Saved || page.NextOffset != -1 {
		t.Errorf("page = %+v, want the one saved fixture row, exhausted", page)
	}
	log.want(t, "GET /v1/me/tracks?limit=50&offset=0")
	joined := strings.Join(*logs, "\n")
	if !strings.Contains(joined, "Web API") {
		t.Errorf("the log should name the route that served a healthy page, got:\n%s", joined)
	}
}
