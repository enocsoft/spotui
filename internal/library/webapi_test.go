package library

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// fixtureClient returns a WebAPIClient over an httptest server. handler sees
// every request (path + query) and answers with the 2026 endpoint shapes the
// specs pin (S14): no credentials, no network beyond the loopback test server.
func fixtureClient(t *testing.T, handler func(t *testing.T, r *http.Request) (int, string)) (WebAPIClient, *requestLog) {
	t.Helper()
	log := &requestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.record(r)
		status, body := handler(t, r)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return WebAPIClientFunc(func(ctx context.Context, method, path string, query url.Values, body []byte) ([]byte, int, error) {
		u := srv.URL + "/" + path
		if len(query) > 0 {
			u += "?" + query.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, method, u, strings.NewReader(string(body)))
		if err != nil {
			return nil, 0, err
		}
		resp, err := srv.Client().Do(req)
		if err != nil {
			return nil, 0, err
		}
		defer resp.Body.Close()
		buf := make([]byte, 0, 4096)
		tmp := make([]byte, 1024)
		for {
			n, rerr := resp.Body.Read(tmp)
			buf = append(buf, tmp[:n]...)
			if rerr != nil {
				break
			}
		}
		return buf, resp.StatusCode, nil
	}), log
}

// requestLog records the requests the catalog made, so tests can pin the
// exact paths, methods, queries and their order.
type requestLog struct {
	entries []string
}

func (l *requestLog) record(r *http.Request) {
	q := ""
	if raw := r.URL.RawQuery; raw != "" {
		q = "?" + raw
	}
	l.entries = append(l.entries, r.Method+" "+r.URL.Path+q)
}

func (l *requestLog) want(t *testing.T, want ...string) {
	t.Helper()
	if strings.Join(l.entries, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests:\n got %q\nwant %q", l.entries, want)
	}
}

const plBebop = `{"id":"pl1","name":"Bebop Essentials","owner":{"display_name":"nahuel"},"public":false,"collaborative":false,"items":{"total":42}}`

// playlist without the items field: the user is not the owner or a
// collaborator, so the 2026 API never returns contents (spec S14).
const plForeign = `{"id":"pl2","name":"Shared List","owner":{"display_name":"someone-else"},"public":true,"collaborative":false}`

// --- GET /v1/me/playlists ---

func TestWebAPIPlaylistsFirstPage(t *testing.T) {
	body := `{"total":55,"offset":0,"next":"https://api.spotify.com/v1/me/playlists?limit=50&offset=50","items":[` +
		plBebop + `,` + plForeign + `]}`
	client, log := fixtureClient(t, func(t *testing.T, r *http.Request) (int, string) {
		if r.URL.Path != "/v1/me/playlists" {
			t.Errorf("unexpected path %q", r.URL.Path)
		}
		if got := r.URL.Query().Get("limit"); got != "50" {
			t.Errorf("limit = %q, want 50 (paging at 50 per page)", got)
		}
		if got := r.URL.Query().Get("offset"); got != "0" {
			t.Errorf("offset = %q, want 0", got)
		}
		return 200, body
	})
	api := NewWebAPI(client)
	page, err := api.Playlists(context.Background(), 0)
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	log.want(t, "GET /v1/me/playlists?limit=50&offset=0")
	if len(page.Playlists) != 2 {
		t.Fatalf("playlists = %d, want 2", len(page.Playlists))
	}
	p := page.Playlists[0]
	if p.ID != "pl1" || p.Name != "Bebop Essentials" || p.Owner != "nahuel" {
		t.Errorf("playlist row = %+v, want the fixture values", p)
	}
	if !p.Private || p.Collaborative {
		t.Errorf("Bebop Essentials should be private, non-collaborative: %+v", p)
	}
	if p.TrackCount != 42 {
		t.Errorf("track count = %d, want 42", p.TrackCount)
	}
	if page.Total != 55 || page.Offset != 0 {
		t.Errorf("page total/offset = %d/%d, want 55/0", page.Total, page.Offset)
	}
	if page.NextOffset != 50 {
		t.Errorf("NextOffset = %d, want 50: one page fetched, never the whole library up front", page.NextOffset)
	}
	// The foreign playlist carries no items field: the count is unknown, so
	// the row shows · (not-applicable) instead of a fake 0.
	f := page.Playlists[1]
	if f.TrackCount != -1 {
		t.Errorf("foreign playlist track count = %d, want -1 (unknown)", f.TrackCount)
	}
	if f.Private {
		t.Errorf("foreign playlist is public: %+v", f)
	}
}

func TestWebAPIPlaylistsFollowsNextLazily(t *testing.T) {
	// 55 playlists, 50 per page: page 2 must only be fetched when asked.
	page1 := `{"total":55,"offset":0,"next":"next","items":[` + plBebop + `]}`
	page2 := `{"total":55,"offset":50,"next":null,"items":[` + plForeign + `]}`
	calls := 0
	client, log := fixtureClient(t, func(t *testing.T, r *http.Request) (int, string) {
		calls++
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
	first, err := api.Playlists(context.Background(), 0)
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if calls != 1 {
		t.Fatalf("after the first page the client made %d calls, want 1 (lazy paging)", calls)
	}
	if first.NextOffset != 50 {
		t.Fatalf("NextOffset = %d, want 50", first.NextOffset)
	}
	second, err := api.Playlists(context.Background(), first.NextOffset)
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	log.want(t, "GET /v1/me/playlists?limit=50&offset=0", "GET /v1/me/playlists?limit=50&offset=50")
	if len(second.Playlists) != 1 || second.Playlists[0].ID != "pl2" {
		t.Errorf("second page = %+v, want the pl2 fixture", second)
	}
	if second.NextOffset != -1 {
		t.Errorf("NextOffset = %d, want -1: the end of the list", second.NextOffset)
	}
}

func TestWebAPIPlaylistsEmptyLibrary(t *testing.T) {
	client, _ := fixtureClient(t, func(t *testing.T, r *http.Request) (int, string) {
		return 200, `{"total":0,"offset":0,"next":null,"items":[]}`
	})
	page, err := NewWebAPI(client).Playlists(context.Background(), 0)
	if err != nil {
		t.Fatalf("Playlists: %v", err)
	}
	if len(page.Playlists) != 0 || page.NextOffset != -1 {
		t.Errorf("empty library page = %+v, want no rows and no next", page)
	}
}

// --- GET /v1/search ---

const searchTrack1 = `{"id":"4uLU6hMCjMI75M1A2tKUQC","name":"So What","artists":[{"name":"Miles Davis"}],"album":{"name":"Kind of Blue"},"duration_ms":562000}`

func TestWebAPISearchShapeAndCap(t *testing.T) {
	body := `{"tracks":{"total":87,"offset":0,"next":"next","items":[` + searchTrack1 + `]},` +
		`"albums":{"total":12,"offset":0,"next":null,"items":[{"id":"al1","name":"Kind of Blue","artists":[{"name":"Miles Davis"}]}]},` +
		`"playlists":{"total":3,"offset":0,"next":null,"items":[` + plBebop + `]}}`
	client, log := fixtureClient(t, func(t *testing.T, r *http.Request) (int, string) {
		if r.URL.Path != "/v1/search" {
			t.Errorf("path = %q, want /v1/search", r.URL.Path)
		}
		q := r.URL.Query()
		if got := q.Get("type"); got != "track,album,playlist" {
			t.Errorf("type = %q, want track,album,playlist", got)
		}
		if got := q.Get("limit"); got != "10" {
			t.Errorf("limit = %q, want 10 (the 2026 cap)", got)
		}
		if got := q.Get("q"); got != "kind of blue" {
			t.Errorf("q = %q", got)
		}
		return 200, body
	})
	page, err := NewWebAPI(client).Search(context.Background(), "kind of blue", 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	log.want(t, "GET /v1/search?limit=10&offset=0&q=kind+of+blue&type=track%2Calbum%2Cplaylist")
	if len(page.Tracks) != 1 || page.Tracks[0].Title != "So What" {
		t.Errorf("search tracks = %+v", page.Tracks)
	}
	if len(page.Albums) != 1 || page.Albums[0].Name != "Kind of Blue" || page.Albums[0].Artist != "Miles Davis" {
		t.Errorf("search albums = %+v", page.Albums)
	}
	if len(page.Playlists) != 1 || page.Playlists[0].Name != "Bebop Essentials" {
		t.Errorf("search playlists = %+v", page.Playlists)
	}
	if page.Totals.Tracks != 87 || page.Totals.Albums != 12 || page.Totals.Playlists != 3 {
		t.Errorf("totals = %+v, want 87/12/3", page.Totals)
	}
	if page.NextOffset != 10 {
		t.Errorf("NextOffset = %d, want 10: tracks still have more under the cap", page.NextOffset)
	}
}

func TestWebAPISearchEmpty(t *testing.T) {
	client, log := fixtureClient(t, func(t *testing.T, r *http.Request) (int, string) {
		return 200, `{"tracks":{"total":0,"offset":0,"next":null,"items":[]}}`
	})
	page, err := NewWebAPI(client).Search(context.Background(), "zzzz nothing", 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	log.want(t, "GET /v1/search?limit=10&offset=0&q=zzzz+nothing&type=track%2Calbum%2Cplaylist")
	if len(page.Tracks) != 0 || len(page.Albums) != 0 || len(page.Playlists) != 0 {
		t.Errorf("empty search returned rows: %+v", page)
	}
	if page.NextOffset != -1 {
		t.Errorf("NextOffset = %d, want -1", page.NextOffset)
	}
}

func TestWebAPISearchOffsetPagination(t *testing.T) {
	first := `{"tracks":{"total":25,"offset":0,"next":"next","items":[` + searchTrack1 + `]}}`
	second := `{"tracks":{"total":25,"offset":10,"next":"next","items":[` + searchTrack1 + `]}}`
	client, log := fixtureClient(t, func(t *testing.T, r *http.Request) (int, string) {
		if r.URL.Query().Get("offset") == "0" {
			return 200, first
		}
		return 200, second
	})
	api := NewWebAPI(client)
	p1, err := api.Search(context.Background(), "q", 0)
	if err != nil {
		t.Fatalf("search 0: %v", err)
	}
	if p1.NextOffset != 10 {
		t.Fatalf("NextOffset = %d, want 10", p1.NextOffset)
	}
	p2, err := api.Search(context.Background(), "q", 10)
	if err != nil {
		t.Fatalf("search 10: %v", err)
	}
	log.want(t, "GET /v1/search?limit=10&offset=0&q=q&type=track%2Calbum%2Cplaylist",
		"GET /v1/search?limit=10&offset=10&q=q&type=track%2Calbum%2Cplaylist")
	if len(p2.Tracks) != 1 || p2.NextOffset != 20 {
		t.Errorf("second page = %+v, want 1 track and next 20", p2)
	}
}

// --- saved toggle: the 2026 generic endpoints ---

func TestWebAPISetSavedUsesGenericLibraryEndpoints(t *testing.T) {
	// 2026: PUT /v1/library and DELETE /me/library — the per-type endpoints
	// (me/tracks, me/albums...) are gone.
	var calls []string
	client, _ := fixtureClient(t, func(t *testing.T, r *http.Request) (int, string) {
		calls = append(calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		return 200, `{}`
	})
	api := NewWebAPI(client)
	const id = "4uLU6hMCjMI75M1A2tKUQC"
	if err := api.SetSaved(context.Background(), id, true); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := api.SetSaved(context.Background(), id, false); err != nil {
		t.Fatalf("unsave: %v", err)
	}
	want := []string{
		"PUT /v1/library?ids=" + id,
		"DELETE /me/library?ids=" + id,
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("saved toggle calls:\n got %q\nwant %q", calls, want)
	}
}

func TestWebAPISetSavedRejectsNon2xx(t *testing.T) {
	client, _ := fixtureClient(t, func(t *testing.T, r *http.Request) (int, string) {
		return 403, `{"error":{"status":403,"message":"no permission"}}`
	})
	err := NewWebAPI(client).SetSaved(context.Background(), "4uLU6hMCjMI75M1A2tKUQC", true)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want an *APIError, got %v", err)
	}
	if apiErr.Status != 403 || apiErr.Message != "no permission" {
		t.Errorf("APIError = %+v, want status 403 and the Spotify message", apiErr)
	}
}

// --- errors ---

func TestWebAPIErrorCarriesStatusAndMessage(t *testing.T) {
	client, _ := fixtureClient(t, func(t *testing.T, r *http.Request) (int, string) {
		return 404, `{"error":{"status":404,"message":"playlist not found"}}`
	})
	_, err := NewWebAPI(client).Playlists(context.Background(), 0)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *APIError, got %v", err)
	}
	if apiErr.Status != 404 || apiErr.Message != "playlist not found" {
		t.Errorf("APIError = %+v, want 404 + spotify message", apiErr)
	}
	if !strings.Contains(apiErr.Error(), "404") || !strings.Contains(apiErr.Error(), "playlist not found") {
		t.Errorf("Error() should carry status and message: %q", apiErr.Error())
	}
}

func TestWebAPIErrorWithoutBodyStillHasStatus(t *testing.T) {
	client, _ := fixtureClient(t, func(t *testing.T, r *http.Request) (int, string) {
		return 500, ``
	})
	_, err := NewWebAPI(client).Playlists(context.Background(), 0)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Status != 500 {
		t.Fatalf("want *APIError with status 500, got %v", err)
	}
}

// --- 429: the existing rate-limit state, not invented retries ---

func TestWebAPI429SurfacesAsRateLimited(t *testing.T) {
	// The transport never retries a 429 (no retry storm); when the status
	// lands here the UI shows the rate-limit state (DESIGN global states).
	client, _ := fixtureClient(t, func(t *testing.T, r *http.Request) (int, string) {
		return 429, `{"error":{"status":429,"message":"rate limit exceeded"}}`
	})
	_, err := NewWebAPI(client).Search(context.Background(), "q", 0)
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("want *APIError, got %v", err)
	}
	if !apiErr.RateLimited {
		t.Errorf("a 429 must surface as the rate-limit state, got %+v", apiErr)
	}
}

func TestWebAPIRateLimitFromTransportError(t *testing.T) {
	// A transport error naming the rate limit (e.g. a dropped-but-429'd
	// connection) must surface the same state.
	client := WebAPIClientFunc(func(ctx context.Context, method, path string, query url.Values, body []byte) ([]byte, int, error) {
		return nil, 0, errors.New("webapi rate limit (429) after 3 attempts")
	})
	_, err := NewWebAPI(client).Playlists(context.Background(), 0)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || !apiErr.RateLimited {
		t.Fatalf("want a rate-limited *APIError, got %v", err)
	}
}

func TestWebAPISearchMalformedBodyIsAnError(t *testing.T) {
	client, _ := fixtureClient(t, func(t *testing.T, r *http.Request) (int, string) {
		return 200, `{"tracks": {"total":`
	})
	if _, err := NewWebAPI(client).Search(context.Background(), "q", 0); err == nil {
		t.Fatal("a malformed body must be an error, not silent empty results")
	}
}

// TestItemsOtherErrorsStillFail is covered by TestWebAPIErrorCarriesStatusAndMessage
// (a 404 stays an error); the Web API items path is gone entirely, so there
// is no 403-to-unavailable mapping any more — contents resolve through the
// playback session instead (context_test.go).
