// Package playback owns where each room is in its film.
//
// This is the change the whole file-on-server mode turns on: playback time
// stops being implicit in whichever frames have arrived at each browser and
// becomes an explicit value the server owns. Clients fetch bytes on their own
// schedule, buffering as deeply as they like, and separately lock their
// playhead to what this says.
//
// The state is an anchor, not a position: "media time M was true at server
// time A, advancing at rate R". Every client reconstructs
//
//	target = M + (now + offset − A) · R
//
// which makes each broadcast self-sufficient. Losing three in a row costs
// nothing, because the fourth still says exactly where the film is — whereas a
// bare position has to be interpolated open-loop between packets.
package playback

import (
	"context"
	"errors"
	"sync"
	"time"

	"github.com/ShadowElf37/VideoStream/proto"
)

// ErrNoMedia is returned for transport commands when nothing is loaded.
var ErrNoMedia = errors.New("nothing is loaded")

// Broadcaster publishes a payload to everyone in a room. The chat service's
// LiveKit broadcaster satisfies this.
type Broadcaster interface {
	Broadcast(ctx context.Context, roomID, topic string, payload []byte) error
}

// Resolver turns a media id into what a client needs to play it: how long it
// is, and a URL it may fetch.
type Resolver interface {
	Resolve(mediaID string) (durationMS int64, title, url string, err error)
}

// clock reads wall time without being moved by it.
//
// The wall clock is captured once and advanced by a monotonic reading, so an
// NTP step or slew on the box cannot shift the anchor under every client at
// once and make them all seek in unison for no reason.
type clock struct {
	wallEpoch time.Time
	monoStart time.Time
}

func newClock() clock {
	now := time.Now()
	return clock{wallEpoch: now, monoStart: now}
}

func (c clock) nowMS() int64 {
	return c.wallEpoch.Add(time.Since(c.monoStart)).UnixMilli()
}

// room is one room's playback state.
type room struct {
	mediaID    string
	title      string
	url        string
	durationMS int64

	paused      bool
	anchorPosMS int64
	anchorAtMS  int64
	rate        float64

	// gen changes on every discontinuity — load, seek, pause, resume, stop.
	// A client treats a change as licence to jump rather than as evidence its
	// own estimate has gone wrong.
	gen int64

	// queue is what plays when this finishes.
	queue []string
}

// Director holds every room's playback state.
type Director struct {
	mu    sync.Mutex
	rooms map[string]*room

	clock    clock
	bcast    Broadcaster
	resolver Resolver
	// seq is a global broadcast counter, so a client can drop a packet that
	// overtook a newer one.
	seq int64
}

// New creates a director.
func New(b Broadcaster, r Resolver) *Director {
	return &Director{rooms: map[string]*room{}, clock: newClock(), bcast: b, resolver: r}
}

// NowMS exposes the director's clock, which is the clock clients synchronise
// against.
func (d *Director) NowMS() int64 { return d.clock.nowMS() }

// posMS is where the room is now. Playing rooms advance with the clock;
// paused rooms sit where they were left.
func (d *Director) posMS(r *room) int64 {
	if r.mediaID == "" {
		return 0
	}
	if r.paused {
		return clamp(r.anchorPosMS, r.durationMS)
	}
	elapsed := float64(d.clock.nowMS()-r.anchorAtMS) * r.rate
	return clamp(r.anchorPosMS+int64(elapsed), r.durationMS)
}

func clamp(v, max int64) int64 {
	if v < 0 {
		return 0
	}
	if max > 0 && v > max {
		return max
	}
	return v
}

// reanchor pins the current position to now and marks a discontinuity.
// Every transport change goes through here, which is what keeps the anchor
// and the generation counter honest.
func (d *Director) reanchor(r *room, posMS int64) {
	r.anchorPosMS = clamp(posMS, r.durationMS)
	r.anchorAtMS = d.clock.nowMS()
	r.gen++
}

func (d *Director) get(roomID string) *room {
	r, ok := d.rooms[roomID]
	if !ok {
		r = &room{rate: 1}
		d.rooms[roomID] = r
	}
	return r
}

// Snapshot is the wire state for one room.
func (d *Director) Snapshot(roomID string) proto.PlaybackState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.snapshotLocked(roomID)
}

func (d *Director) snapshotLocked(roomID string) proto.PlaybackState {
	r := d.get(roomID)
	st := proto.PlaybackState{
		Seq:         d.nextSeq(),
		MediaID:     r.mediaID,
		Title:       r.title,
		URL:         r.url,
		DurationMS:  r.durationMS,
		Paused:      r.paused,
		AnchorPosMS: r.anchorPosMS,
		AnchorAtMS:  r.anchorAtMS,
		Rate:        r.rate,
		Gen:         r.gen,
		ServerNowMS: d.clock.nowMS(),
		Queue:       append([]string(nil), r.queue...),
	}
	if r.mediaID == "" {
		st.Idle = true
	}
	// PosMS is the anchor evaluated now. Clients compute this themselves from
	// the anchor; it is here so anything reading the state casually — a log, a
	// test, a debug overlay — does not have to.
	st.PosMS = d.posMS(r)
	return st
}

func (d *Director) nextSeq() int64 {
	d.seq++
	return d.seq
}

// Load starts a title, discarding the queue.
func (d *Director) Load(ctx context.Context, roomID, mediaID string) error {
	return d.load(ctx, roomID, mediaID, true)
}

// Enqueue adds a title behind whatever is playing, or starts it if nothing is.
func (d *Director) Enqueue(ctx context.Context, roomID, mediaID string) error {
	if _, _, _, err := d.resolver.Resolve(mediaID); err != nil {
		return err
	}
	d.mu.Lock()
	r := d.get(roomID)
	idle := r.mediaID == ""
	if !idle {
		r.queue = append(r.queue, mediaID)
	}
	d.mu.Unlock()
	if idle {
		return d.load(ctx, roomID, mediaID, false)
	}
	d.Publish(ctx, roomID)
	return nil
}

func (d *Director) load(ctx context.Context, roomID, mediaID string, clearQueue bool) error {
	duration, title, url, err := d.resolver.Resolve(mediaID)
	if err != nil {
		return err
	}
	d.mu.Lock()
	r := d.get(roomID)
	r.mediaID, r.title, r.url, r.durationMS = mediaID, title, url, duration
	r.rate = 1
	// Playing, not paused: picking something is a request to watch it. Clients
	// hold their own picture until they have enough buffered, and the sync
	// loop closes whatever gap that opens.
	r.paused = false
	if clearQueue {
		r.queue = nil
	}
	d.reanchor(r, 0)
	d.mu.Unlock()
	d.Publish(ctx, roomID)
	return nil
}

// SetPaused pauses or resumes, pinning the position at the moment it changed.
func (d *Director) SetPaused(ctx context.Context, roomID string, paused bool) error {
	d.mu.Lock()
	r := d.get(roomID)
	if r.mediaID == "" {
		d.mu.Unlock()
		return ErrNoMedia
	}
	if r.paused != paused {
		pos := d.posMS(r)
		r.paused = paused
		d.reanchor(r, pos)
	}
	d.mu.Unlock()
	d.Publish(ctx, roomID)
	return nil
}

// TogglePause flips the pause state and reports the new value.
func (d *Director) TogglePause(ctx context.Context, roomID string) (bool, error) {
	d.mu.Lock()
	r := d.get(roomID)
	if r.mediaID == "" {
		d.mu.Unlock()
		return false, ErrNoMedia
	}
	pos := d.posMS(r)
	r.paused = !r.paused
	paused := r.paused
	d.reanchor(r, pos)
	d.mu.Unlock()
	d.Publish(ctx, roomID)
	return paused, nil
}

// Seek moves to an absolute position, or by a delta when relative.
func (d *Director) Seek(ctx context.Context, roomID string, ms int64, relative bool) (int64, error) {
	d.mu.Lock()
	r := d.get(roomID)
	if r.mediaID == "" {
		d.mu.Unlock()
		return 0, ErrNoMedia
	}
	target := ms
	if relative {
		target = d.posMS(r) + ms
	}
	d.reanchor(r, target)
	landed := r.anchorPosMS
	d.mu.Unlock()
	d.Publish(ctx, roomID)
	return landed, nil
}

// Stop unloads, leaving the room idle.
func (d *Director) Stop(ctx context.Context, roomID string) {
	d.mu.Lock()
	r := d.get(roomID)
	r.mediaID, r.title, r.url = "", "", ""
	r.durationMS, r.anchorPosMS = 0, 0
	r.queue = nil
	r.paused = true
	r.gen++
	r.anchorAtMS = d.clock.nowMS()
	d.mu.Unlock()
	d.Publish(ctx, roomID)
}

// Forget drops a room's state, for when a room is gone.
func (d *Director) Forget(roomID string) {
	d.mu.Lock()
	delete(d.rooms, roomID)
	d.mu.Unlock()
}

// Publish broadcasts the current state to the room.
func (d *Director) Publish(ctx context.Context, roomID string) {
	if d.bcast == nil {
		return
	}
	st := d.Snapshot(roomID)
	payload, err := encode(st)
	if err != nil {
		return
	}
	_ = d.bcast.Broadcast(ctx, roomID, proto.TopicPlayback, payload)
}

// Run advances playlists and keeps late joiners fresh.
//
// Two jobs on one ticker: notice when a film has reached its end and move on,
// and re-broadcast periodically so a client that missed a packet, joined late
// or slept converges without having to ask.
func (d *Director) Run(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			for _, roomID := range d.endedRooms() {
				d.advance(ctx, roomID)
			}
			for _, roomID := range d.activeRooms() {
				d.Publish(ctx, roomID)
			}
		}
	}
}

// endedRooms lists rooms whose current title has run out.
func (d *Director) endedRooms() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []string
	for id, r := range d.rooms {
		if r.mediaID != "" && !r.paused && r.durationMS > 0 && d.posMS(r) >= r.durationMS {
			out = append(out, id)
		}
	}
	return out
}

func (d *Director) activeRooms() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	out := make([]string, 0, len(d.rooms))
	for id, r := range d.rooms {
		if r.mediaID != "" {
			out = append(out, id)
		}
	}
	return out
}

// advance moves to the next queued title, or holds at the end.
func (d *Director) advance(ctx context.Context, roomID string) {
	d.mu.Lock()
	r := d.get(roomID)
	if len(r.queue) == 0 {
		// Hold on the last frame rather than unloading: the room is still
		// watching something, it has simply finished.
		r.paused = true
		d.reanchor(r, r.durationMS)
		d.mu.Unlock()
		d.Publish(ctx, roomID)
		return
	}
	next := r.queue[0]
	r.queue = r.queue[1:]
	d.mu.Unlock()
	if err := d.load(ctx, roomID, next, false); err != nil {
		// A queued title that has since been deleted should not wedge the
		// room; drop it and try the next one on the following tick.
		d.mu.Lock()
		r := d.get(roomID)
		r.paused = true
		d.mu.Unlock()
		d.Publish(ctx, roomID)
	}
}
