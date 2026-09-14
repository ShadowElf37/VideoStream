// Package occupancy watches how many people are in the room, and notices
// when everyone has left for long enough that the invite link should stop
// working.
//
// It polls LiveKit rather than taking webhooks: a poll needs no inbound
// route from the SFU to the app, works identically in development, and ten
// seconds of latency on "is anyone here" is nothing against a rotation
// grace of minutes.
package occupancy

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/livekit/protocol/livekit"

	"github.com/ShadowElf37/VideoStream/proto"
)

// Lister is the one LiveKit call the watcher needs. *lksdk.RoomServiceClient
// satisfies it.
type Lister interface {
	ListParticipants(ctx context.Context, req *livekit.ListParticipantsRequest) (*livekit.ListParticipantsResponse, error)
}

// projectorIdentity is the fixed identity the projector joins with. It is
// not a person, so it does not keep the room "occupied": a projector left
// running overnight must not keep a leaked link alive.
const projectorIdentity = "projector"

// Watcher polls the room and fires OnEmpty once per stretch of emptiness.
type Watcher struct {
	lister      Lister
	interval    time.Duration
	rotateAfter time.Duration
	onEmpty     func(ctx context.Context)
	log         *slog.Logger
	now         func() time.Time

	mu           sync.Mutex
	count        int
	lastOccupied time.Time
	// armed is true once the room has been seen occupied since the last time
	// OnEmpty fired, so a room that stays empty fires exactly once.
	armed bool
}

// New creates a watcher. rotateAfter is how long the room must stand empty
// before onEmpty fires; zero disables that entirely (the count is still
// kept). onEmpty may be nil.
func New(l Lister, rotateAfter time.Duration, onEmpty func(context.Context), logger *slog.Logger) *Watcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Watcher{
		lister:      l,
		interval:    10 * time.Second,
		rotateAfter: rotateAfter,
		onEmpty:     onEmpty,
		log:         logger,
		now:         time.Now,
	}
}

// Count is the number of people (projector excluded) seen in the room by the
// most recent poll.
func (w *Watcher) Count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.count
}

// Run polls until ctx is done.
func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(w.interval)
	defer t.Stop()
	w.Poll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.Poll(ctx)
		}
	}
}

// Poll asks LiveKit who is in the room and updates the count, firing
// onEmpty if the room has now stood empty for long enough.
func (w *Watcher) Poll(ctx context.Context) {
	count := 0
	resp, err := w.lister.ListParticipants(ctx, &livekit.ListParticipantsRequest{Room: proto.RoomID})
	if err != nil {
		// The usual empty state: LiveKit deletes a room once it has been
		// empty for its timeout, and listing a room that is gone is an error.
		w.log.Debug("occupancy: list participants", "err", err)
	} else {
		for _, p := range resp.GetParticipants() {
			if p.GetIdentity() != projectorIdentity {
				count++
			}
		}
	}

	now := w.now()
	fire := false
	w.mu.Lock()
	w.count = count
	if count > 0 {
		w.lastOccupied = now
		w.armed = true
	} else if w.armed && w.rotateAfter > 0 && now.Sub(w.lastOccupied) >= w.rotateAfter {
		w.armed = false
		fire = true
	}
	w.mu.Unlock()

	if fire && w.onEmpty != nil {
		w.onEmpty(ctx)
	}
}
