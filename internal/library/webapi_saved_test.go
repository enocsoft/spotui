package library

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

// --- GET /v1/me/tracks (T29: the SAVED group — the user's saved songs) ---

// The saved-track object wraps the track one level down and carries added_at;
// the app keeps only the facts the track row renders, and every mapped row is
// saved by definition: the list IS the saved set.
const svTrackSoWhat = `{"added_at":"2026-01-02T10:00:00Z","track":{"id":"4uLU6hMCjMI75M1A2tKUQC","name":"So What","artists":[{"name":"Miles Davis"}],"album":{"name":"Kind of Blue"},"duration_ms":562000}}`
const svTrackBlueInGreen = `{"added_at":"2026-01-03T10:00:00Z","track":{"id":"3uLU6hMCjMI75M1A2tKUQC","name":"Blue in Green","artists":[{"name":"Miles Davis"},{"name":"Bill Evans"}],"album":{"name":"Kind of Blue"},"duration_ms":337000}}`

func TestWebAPISavedTracksFirstPageAndLazyNext(t *testing.T) {
	page1 := `{"total":55,"offset":0,"next":"next","items":[` + svTrackSoWhat + `]}`
	page2 := `{"total":55,"offset":50,"next":null,"items":[` + svTrackBlueInGreen + `]}`
	calls := 0
	client, log := fixtureClient(t, func(t *testing.T, r *http.Request) (int, string) {
		calls++
		if r.URL.Path != "/v1/me/tracks" {
			t.Errorf("path = %q, want /v1/me/tracks", r.URL.Path)
		}
		if got := r.URL.Query().Get("limit"); got != "50" {
			t.Errorf("limit = %q, want 50 (paging at 50 per page, like playlists)", got)
		}
		switch off := r.URL.Query().Get("offset"); off {
		case "0":
			return 200, page1
		case "50":
			return 200, page2
		default:
			t.Errorf("unexpected offset %q — the catalog must never prefetch everything", off)
			return 400, `{}`
		}
	})
	api := NewWebAPI(client)
	first, err := api.SavedTracks(context.Background(), 0)
	if err != nil {
		t.Fatalf("SavedTracks first page: %v", err)
	}
	if calls != 1 {
		t.Fatalf("after the first page the client made %d calls, want 1 (lazy paging)", calls)
	}
	log.want(t, "GET /v1/me/tracks?limit=50&offset=0")
	if len(first.Tracks) != 1 {
		t.Fatalf("tracks = %d, want 1", len(first.Tracks))
	}
	tr := first.Tracks[0]
	if tr.ID != "4uLU6hMCjMI75M1A2tKUQC" || tr.Title != "So What" || tr.Artist != "Miles Davis" || tr.Album != "Kind of Blue" {
		t.Errorf("track row = %+v, want the fixture values", tr)
	}
	if tr.Duration != 562000*time.Millisecond {
		t.Errorf("duration = %v, want 562000ms", tr.Duration)
	}
	if !tr.Saved {
		t.Errorf("every mapped saved track is saved: the list IS the saved set")
	}
	if first.Total != 55 || first.NextOffset != 50 {
		t.Errorf("total/next = %d/%d, want 55/50: one page fetched, never the whole library up front", first.Total, first.NextOffset)
	}

	second, err := api.SavedTracks(context.Background(), first.NextOffset)
	if err != nil {
		t.Fatalf("SavedTracks second page: %v", err)
	}
	log.want(t, "GET /v1/me/tracks?limit=50&offset=0", "GET /v1/me/tracks?limit=50&offset=50")
	if len(second.Tracks) != 1 || second.Tracks[0].ID != "3uLU6hMCjMI75M1A2tKUQC" {
		t.Errorf("second page = %+v, want the Blue in Green fixture", second)
	}
	if second.Tracks[0].Artist != "Miles Davis, Bill Evans" {
		t.Errorf("artists joined = %q, want \"Miles Davis, Bill Evans\"", second.Tracks[0].Artist)
	}
	if second.NextOffset != -1 {
		t.Errorf("NextOffset = %d, want -1: the end of the list", second.NextOffset)
	}
}

// A Development Mode app can be refused the endpoint outright: the status and
// the server's message must surface — never an empty list pretending nothing
// is saved.
func TestWebAPISavedTracksErrorCarriesStatus(t *testing.T) {
	client, _ := fixtureClient(t, func(t *testing.T, r *http.Request) (int, string) {
		return 403, `{"error":{"status":403,"message":"Endpoint is not available for this app"}}`
	})
	_, err := NewWebAPI(client).SavedTracks(context.Background(), 0)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 403 || apiErr.Message != "Endpoint is not available for this app" {
		t.Fatalf("want *APIError 403 with the server's message, got %v", err)
	}
}
