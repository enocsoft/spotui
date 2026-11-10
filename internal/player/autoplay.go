// Radio (T26): the autoplay resolver seam. When the UI's one-second state
// tick detects the end of the current track with radio on, the app asks this
// player for the next related context — sess.Spclient().ContextResolveAutoplay
// over the seed track uri and the recently played track uris — and plays the
// first playable result through the existing play path. The seam is the
// library.AutoplayResolver interface, so tests need no session.
package player

import (
	"context"
	"errors"
	"fmt"
	"time"

	playerpb "github.com/elxgy/go-librespot/proto/spotify/player"
	"google.golang.org/protobuf/proto"

	"github.com/nahuel/spotui/internal/library"
)

// autoplayTimeout bounds one ContextResolveAutoplay call: a hung resolve must
// become an error the UI can toast, not a frozen radio.
const autoplayTimeout = 30 * time.Second

// recentURIsCap is how many recently played track uris ride one resolve: the
// request only uses them to avoid repeating what was heard.
const recentURIsCap = 10

// ResolveAutoplay implements library.AutoplayResolver (T26). It never logs
// tokens and never retries: a failure is one error the UI surfaces once.
func (p *Player) ResolveAutoplay(ctx context.Context, seedURI string, recent []string) ([]library.Track, error) {
	p.mu.Lock()
	sess := p.sess
	p.mu.Unlock()
	if sess == nil {
		return nil, errors.New("no embedded session")
	}
	cctx, cancel := context.WithTimeout(ctx, autoplayTimeout)
	defer cancel()
	if len(recent) > recentURIsCap {
		recent = recent[len(recent)-recentURIsCap:]
	}
	spotCtx, err := sess.Spclient().ContextResolveAutoplay(cctx, &playerpb.AutoplayContextRequest{
		ContextUri:     proto.String(seedURI),
		RecentTrackUri: recent,
	})
	if err != nil {
		return nil, fmt.Errorf("resolving the radio context: %w", err)
	}
	tracks := library.TracksFromContext(spotCtx)
	// Autoplay contexts carry the uri and nothing else: without the batch lookup
	// the track plays while the player bar keeps the previous title.
	return library.EnrichTracks(cctx, tracks, p.metadataBatch()), nil
}
