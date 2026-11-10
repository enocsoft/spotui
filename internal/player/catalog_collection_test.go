// The saved-tracks fallback's context uri seam (library.CollectionContext):
// before the embedded session is live there is no user id to name the
// collection with, and the seam must say so honestly — the saved-tracks
// error then names the Web API denial plus why no collection uri exists,
// never a bare "could not load". Offline: no session, no network.
package player

import (
	"context"
	"testing"
)

func TestCollectionContextRequiresTheSession(t *testing.T) {
	p := &Player{}
	uri, err := p.collectionContext()(context.Background())
	if err == nil {
		t.Fatal("without a session the collection uri cannot exist: an error is required")
	}
	if uri != "" {
		t.Errorf("uri = %q, want empty: never a guessed collection uri", uri)
	}
	if err.Error() != "no embedded session" {
		t.Errorf("err = %v, want the honest no-session reason", err)
	}
}
