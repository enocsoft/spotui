// The SAVED group's second route (2026 work): when the Web API denies
// GET /v1/me/tracks — a Development Mode app can be refused it with a
// policy status (410, or 403 as the other likely denial) while the route
// stays on Spotify's endpoint list — the saved songs are read through the
// playback session's spclient instead, the same ContextResolve playlist and
// album contents already ride (context.go).
//
// Saved songs are internally a collection context: the resolver gets
// `spotify:user:<user id>:collection`, needs no Web API quota and has no
// owner restriction. The mapping, the skipped count and the bounded-resolve
// truncation are the playlist path's own — there is no second
// implementation — and the log names the route that served the list, so the
// next run proves which one it was. When both routes fail the error names
// both: the Web API status with its message, then the collection attempt.
package library

import (
	"context"
	"fmt"
)

// CollectionContext supplies the context uri of the user's saved songs,
// `spotify:user:<user id>:collection`. The real wiring reads the user id
// from the playback session (session.Session.Username, player.Catalog);
// tests substitute a fixture. Not wired means no fallback exists and the
// Web API error stands alone.
type CollectionContext func(ctx context.Context) (string, error)

// endpointDenial reports whether err is a Web API policy denial of an
// available endpoint: 410 Gone — the status the saved-tracks group actually
// saw — or 403, the other likely refusal. A 429 is never a denial: the
// rate-limit state owns it.
func endpointDenial(err error) bool {
	apiErr, ok := err.(*APIError)
	return ok && (apiErr.Status == 410 || apiErr.Status == 403)
}

// savedTracksViaCollection serves one page of the saved songs through the
// collection resolve. webErr is the denial that forced the fallback: it
// stays in every log line and in the error should the collection fail too.
// The context is one bounded set (itemsViaContext), so NextOffset is -1 —
// the same honest paging playlist contents report — and every mapped row is
// saved by definition: this list IS the saved set.
func (w *WebAPI) savedTracksViaCollection(ctx context.Context, offset int, webErr error) (SavedTracksPage, error) {
	if w.logf != nil {
		w.logf("saved tracks · Web API GET /v1/me/tracks denied: %v", webErr)
	}
	if w.collection == nil {
		w.logfSaved("saved tracks · no collection context wired — the Web API denial stands alone")
		return SavedTracksPage{}, webErr
	}
	uri, uerr := w.collection(ctx)
	if uerr != nil {
		err := fmt.Errorf("saved tracks · GET /v1/me/tracks: %v · collection context unavailable: %v", webErr, uerr)
		w.logfSaved("saved tracks · %v", err)
		return SavedTracksPage{}, err
	}
	w.logfSaved("saved tracks · falling back to the collection resolve %s", uri)
	items, rerr := itemsViaContext(ctx, uri, offset, w.resolve, w.batch)
	if rerr != nil {
		err := fmt.Errorf("saved tracks · GET /v1/me/tracks: %v · collection resolve %s: %v", webErr, uri, rerr)
		w.logfSaved("saved tracks · %v", err)
		return SavedTracksPage{}, err
	}
	page := SavedTracksPage{Offset: items.Offset, Total: items.Total, NextOffset: items.NextOffset}
	for _, t := range items.Tracks {
		t.Saved = true
		page.Tracks = append(page.Tracks, t)
	}
	w.logfSaved("saved tracks served by the collection resolve %s (%d tracks)", uri, len(page.Tracks))
	return page, nil
}

// logfSaved logs one saved-tracks diagnostic when a logger is wired; the
// player logger redacts token-shaped runs anyway and no line ever carries one.
func (w *WebAPI) logfSaved(format string, args ...any) {
	if w.logf != nil {
		w.logf(format, args...)
	}
}
