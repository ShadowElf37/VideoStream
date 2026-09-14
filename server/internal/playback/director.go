// Package playback owns where the room is in its film.
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
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/ShadowElf37/VideoStream/proto"
)

// ErrNoMedia is returned for transport commands when nothing is loaded.
var ErrNoMedia = errors.New("nothing is loaded")

// ErrNotAPermutation is returned when a reorder is not the same set of titles
// the queue currently holds. Rejecting rather than reconciling is deliberate:
// a client whose view of the queue is stale would otherwise silently drop
// whatever was added since it last looked.
var ErrNotAPermutation = errors.New("that is not the current queue reordered")

// ErrNotQueued is returned when asked to remove something that is not there.
var ErrNotQueued = errors.New("that title is not in the queue")

// Broadcaster publishes a payload to everyone in the room. The chat
// service's LiveKit broadcaster satisfies this.
type Broadcaster interface {
	Broadcast(ctx context.Context, topic string, payload []byte) error
}

// Resolver turns a media id into what a client needs to play it: how long it
// is, and a URL it may fetch.
type Resolver interface {
	Resolve(mediaID string) (durationMS int64, title, url string, err error)
}

// Clock is the director's only source of time. An interface because the hold
// logic has a six-second freshness window and a twenty-second timeout in it,
// and a test that has to sleep through those is a test nobody runs.
type Clock interface{ NowMS() int64 }

// wallClock reads wall time without being moved by it.
//
// The wall clock is captured once and advanced by a monotonic reading, so an
// NTP step or slew on the box cannot shift the anchor under every client at
// once and make them all seek in unison for no reason.
type wallClock struct {
	wallEpoch time.Time
	monoStart time.Time
}

func newWallClock() wallClock {
	now := time.Now()
	return wallClock{wallEpoch: now, monoStart: now}
}

func (c wallClock) NowMS() int64 {
	return c.wallEpoch.Add(time.Since(c.monoStart)).UnixMilli()
}

// room is the room's playback state.
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

	// holding is waitForEveryone parking the room at a discontinuity until
	// the slow clients catch up.
	//
	// Deliberately separate from paused: paused is what the room *intends*,
	// holding is why it is not happening yet. Folding the two together would
	// mean a release could not tell "the host pressed play and we are waiting"
	// from "the host pressed pause", and the room would resume things nobody
	// asked to resume. The wire state reports both as paused, because that is
	// what a still picture is.
	holding   bool
	holdSince int64
	// holdCold is set when the hold began with nobody reporting at all — a
	// first load, or an idle room. Then, and only then, the decision waits
	// out holdGraceMS so the first answers can arrive. In steady state every
	// client already has a fresh report on file, so there is nothing to
	// collect and no reason to make anyone wait.
	holdCold bool
}

// report is one client's last word on whether it could start.
type report struct {
	name   string
	gen    int64
	ready  bool
	seenAt int64
}

// How the hold is decided. All three numbers are judgement calls:
//
//   - A report older than ReportFreshMS is from someone who has closed their
//     laptop. Waiting on them forever is how this feature becomes the thing
//     everyone turns off.
//   - HoldTimeoutMS is the promise that the room always eventually starts. A
//     client on a genuinely hopeless link must not be able to stop the film.
//   - holdGraceMS covers the gap between the command landing and the first
//     answers for the new generation arriving. Without it the very first load
//     — when nobody has ever reported — releases before anyone can object.
const (
	ReportFreshMS = 6_000
	HoldTimeoutMS = 20_000
	holdGraceMS   = 2_000
	// reportTTLMS is only housekeeping: how long a silent client's entry
	// lingers in the map before it is dropped.
	reportTTLMS = 60_000
)

// Director holds the room's playback state.
type Director struct {
	mu   sync.Mutex
	room *room

	clock    Clock
	bcast    Broadcaster
	resolver Resolver

	// waitForEveryone mirrors the room setting; the settings handler pushes
	// it down so the director never has to reach back into the store.
	waitForEveryone bool
	// reports is the readiness of every client that has spoken recently,
	// keyed by identity.
	reports map[string]report
	// seq is a broadcast counter, so a client can drop a packet that
	// overtook a newer one.
	seq int64

	// lastIntent is the most recent echo, carried in the state so a late
	// joiner still gets the loading card for the film being started.
	lastIntent *proto.PlaybackIntent
	intentSeq  int64
}

// New creates a director on the real clock.
func New(b Broadcaster, r Resolver) *Director {
	return NewWithClock(b, r, newWallClock())
}

// NewWithClock creates a director on the given clock, for tests.
func NewWithClock(b Broadcaster, r Resolver, c Clock) *Director {
	return &Director{
		room:     &room{rate: 1},
		clock:    c,
		bcast:    b,
		resolver: r,
		reports:  map[string]report{},
	}
}

// NowMS exposes the director's clock, which is the clock clients synchronise
// against.
func (d *Director) NowMS() int64 { return d.clock.NowMS() }

// posMS is where the room is now. Playing, it advances with the clock;
// paused, it sits where it was left.
func (d *Director) posMS(r *room) int64 {
	if r.mediaID == "" {
		return 0
	}
	if stopped(r) {
		return clamp(r.anchorPosMS, r.durationMS)
	}
	elapsed := float64(d.clock.NowMS()-r.anchorAtMS) * r.rate
	return clamp(r.anchorPosMS+int64(elapsed), r.durationMS)
}

// stopped reports whether the picture is still — whether because the room is
// paused or because it is holding. Everything that asks "is time advancing"
// wants this rather than r.paused.
func stopped(r *room) bool { return r.paused || r.holding }

// atEnd reports whether a position is close enough to the end that resuming
// there would immediately end again. The tolerance covers a film whose last
// frame lands a little short of the container's stated duration.
func atEnd(r *room, posMS int64) bool {
	return r.durationMS > 0 && posMS >= r.durationMS-250
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
	r.anchorAtMS = d.clock.NowMS()
	r.gen++
}

// Snapshot is the wire state.
func (d *Director) Snapshot() proto.PlaybackState {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.snapshotLocked()
}

func (d *Director) snapshotLocked() proto.PlaybackState {
	r := d.room
	st := proto.PlaybackState{
		Seq:         d.nextSeq(),
		MediaID:     r.mediaID,
		Title:       r.title,
		URL:         r.url,
		DurationMS:  r.durationMS,
		Paused:      stopped(r),
		AnchorPosMS: r.anchorPosMS,
		AnchorAtMS:  r.anchorAtMS,
		Rate:        r.rate,
		Gen:         r.gen,
		ServerNowMS: d.clock.NowMS(),
		Queue:       append([]string(nil), r.queue...),
		Holding:     r.holding,
	}
	if r.holding {
		st.WaitingFor = d.waitingForLocked()
	}
	st.LastIntent = d.lastIntent
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
func (d *Director) Load(ctx context.Context, actor proto.PlaybackActor, mediaID string) error {
	return d.load(ctx, actor, mediaID, true)
}

// Enqueue adds a title behind whatever is playing, or starts it if nothing is.
func (d *Director) Enqueue(ctx context.Context, actor proto.PlaybackActor, mediaID string) error {
	if _, _, _, err := d.resolver.Resolve(mediaID); err != nil {
		return err
	}
	d.mu.Lock()
	r := d.room
	idle := r.mediaID == ""
	if !idle {
		r.queue = append(r.queue, mediaID)
	}
	d.mu.Unlock()
	if idle {
		return d.load(ctx, actor, mediaID, false)
	}
	d.Publish(ctx)
	return nil
}

func (d *Director) load(ctx context.Context, actor proto.PlaybackActor, mediaID string, clearQueue bool) error {
	duration, title, url, err := d.resolver.Resolve(mediaID)
	if err != nil {
		return err
	}
	d.mu.Lock()
	r := d.room
	r.mediaID, r.title, r.url, r.durationMS = mediaID, title, url, duration
	r.rate = 1
	// Playing, not paused: picking something is a request to watch it. Clients
	// hold their own picture until they have enough buffered, and the sync
	// loop closes whatever gap that opens.
	r.paused = false
	if clearQueue {
		r.queue = nil
	}
	d.armHold(r)
	d.reanchor(r, 0)
	in := d.intentLocked(proto.IntentLoad, actor, 0, 0)
	d.mu.Unlock()
	d.announce(ctx, in)
	return nil
}

// SetPaused pauses or resumes, pinning the position at the moment it changed.
func (d *Director) SetPaused(ctx context.Context, actor proto.PlaybackActor, paused bool) error {
	d.mu.Lock()
	r := d.room
	if r.mediaID == "" {
		d.mu.Unlock()
		return ErrNoMedia
	}
	was := d.posMS(r)
	// Holding counts as paused to the outside world, so "resume" while held
	// must still be a change — that is what re-arms the hold at a fresh
	// position rather than silently doing nothing.
	if r.paused != paused || (r.holding && !paused) {
		pos := d.posMS(r)
		// Resuming a film that has already finished restarts it, rather than
		// resuming at the end — where the run loop would notice it is past the
		// duration and pause it straight back, so play would appear to do
		// nothing at all.
		if !paused && atEnd(r, pos) {
			pos = 0
		}
		r.paused = paused
		if paused {
			// An explicit pause supersedes the hold: the room is now stopped
			// because someone asked, and nothing is waiting to start.
			r.holding = false
		} else if !r.holding {
			d.armHold(r)
		}
		d.reanchor(r, pos)
	}
	in := d.intentLocked(pauseAction(paused), actor, was, d.posMS(r))
	d.mu.Unlock()
	d.announce(ctx, in)
	return nil
}

// pauseAction names the echo for a pause or a resume.
func pauseAction(paused bool) string {
	if paused {
		return proto.IntentPause
	}
	return proto.IntentPlay
}

// TogglePause flips the pause state and reports the new value.
func (d *Director) TogglePause(ctx context.Context, actor proto.PlaybackActor) (bool, error) {
	d.mu.Lock()
	r := d.room
	if r.mediaID == "" {
		d.mu.Unlock()
		return false, ErrNoMedia
	}
	was := d.posMS(r)
	pos := was
	// Toggling a held room cancels the wait rather than overriding it: the
	// picture is already still, so the button under the host's finger reads
	// as pause, and "Start anyway" is the separate thing that starts it.
	paused := r.holding || !r.paused
	r.paused = paused
	if !paused && atEnd(r, pos) {
		pos = 0
	}
	if paused {
		r.holding = false
	} else {
		d.armHold(r)
	}
	d.reanchor(r, pos)
	in := d.intentLocked(pauseAction(paused), actor, was, d.posMS(r))
	d.mu.Unlock()
	d.announce(ctx, in)
	return paused, nil
}

// Seek moves to an absolute position, or by a delta when relative.
func (d *Director) Seek(ctx context.Context, actor proto.PlaybackActor, ms int64, relative bool) (int64, error) {
	d.mu.Lock()
	r := d.room
	if r.mediaID == "" {
		d.mu.Unlock()
		return 0, ErrNoMedia
	}
	was := d.posMS(r)
	target := ms
	if relative {
		target = was + ms
	}
	// A seek only starts anything if the room was playing, so only then is
	// there a start to wait for. Scrubbing around while paused must not put
	// up a "waiting for everyone" card over a still picture.
	if !r.paused {
		d.armHold(r)
	}
	d.reanchor(r, target)
	landed := r.anchorPosMS
	in := d.intentLocked(proto.IntentSeek, actor, was, landed)
	d.mu.Unlock()
	d.announce(ctx, in)
	return landed, nil
}

// Stop unloads, leaving the room idle.
func (d *Director) Stop(ctx context.Context, actor proto.PlaybackActor) {
	d.mu.Lock()
	r := d.room
	was := d.posMS(r)
	r.mediaID, r.title, r.url = "", "", ""
	r.durationMS, r.anchorPosMS = 0, 0
	r.queue = nil
	r.paused = true
	r.holding = false
	r.gen++
	r.anchorAtMS = d.clock.NowMS()
	in := d.intentLocked(proto.IntentStop, actor, was, 0)
	d.mu.Unlock()
	d.announce(ctx, in)
}

// Reorder replaces the queue with the same titles in a different order.
//
// It takes the whole queue rather than a move, because a move is only
// meaningful against a particular starting order and the client's may be one
// broadcast behind. Requiring the full list makes the staleness detectable
// instead of silently destructive.
func (d *Director) Reorder(ctx context.Context, queue []string) error {
	d.mu.Lock()
	r := d.room
	if !samePermutation(r.queue, queue) {
		d.mu.Unlock()
		return ErrNotAPermutation
	}
	r.queue = append([]string(nil), queue...)
	d.mu.Unlock()
	d.Publish(ctx)
	return nil
}

// Dequeue drops one title from the queue. Only the first occurrence: the same
// title queued twice is two things to watch, not one.
func (d *Director) Dequeue(ctx context.Context, mediaID string) error {
	d.mu.Lock()
	r := d.room
	at := -1
	for i, id := range r.queue {
		if id == mediaID {
			at = i
			break
		}
	}
	if at < 0 {
		d.mu.Unlock()
		return ErrNotQueued
	}
	r.queue = append(append([]string(nil), r.queue[:at]...), r.queue[at+1:]...)
	d.mu.Unlock()
	d.Publish(ctx)
	return nil
}

// samePermutation reports whether two lists hold the same titles with the same
// multiplicities — a title queued twice must still be queued twice.
func samePermutation(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	counts := make(map[string]int, len(a))
	for _, v := range a {
		counts[v]++
	}
	for _, v := range b {
		counts[v]--
		if counts[v] < 0 {
			return false
		}
	}
	return true
}

// The intent echo
// ------------------------------------------------------------------------
//
// A viewer sees the picture jump and has no idea who did it or why. The state
// broadcast cannot tell them: it says where the film is, not where it was or
// whose hand was on the transport, and by the time a client has applied it the
// jump has already happened. So every command also produces an intent, built
// while the command is being applied and sent ahead of the state it produces.

// intentLocked records an echo and returns it for broadcasting. Called with
// the lock held, from inside each command.
func (d *Director) intentLocked(action string, actor proto.PlaybackActor, fromMS, toMS int64) proto.PlaybackIntent {
	d.intentSeq++
	in := proto.PlaybackIntent{
		Seq:    d.intentSeq,
		Action: action,
		Actor:  actor,
		FromMS: fromMS,
		ToMS:   toMS,
		TS:     d.clock.NowMS(),
	}
	if action == proto.IntentLoad {
		in.MediaID, in.Title = d.room.mediaID, d.room.title
	}
	d.lastIntent = &in
	return in
}

// announce sends the echo, then the state. The order is the point: a client
// that learned the new position first would have jumped before being told why.
func (d *Director) announce(ctx context.Context, in proto.PlaybackIntent) {
	if d.bcast != nil {
		if payload, err := encodeIntent(in); err == nil {
			_ = d.bcast.Broadcast(ctx, proto.TopicPlaybackIntent, payload)
		}
	}
	d.Publish(ctx)
}

// waitForEveryone: holding the room together
// ------------------------------------------------------------------------
//
// The old contract was that the host presses play and everyone scrambles: a
// client that has not buffered stalls, then chases the anchor at 1.05x, and
// the people who were ready watch the first minute knowing someone else is
// not with them. With the server owning playback time and every browser
// already computing how much it has buffered, the honest alternative is for
// the director to simply not start until it has heard back.

// SetWaitForEveryone mirrors the room setting into the director. Turning it
// off while the room is holding releases immediately: the setting is off, so
// there is nothing left to wait for.
func (d *Director) SetWaitForEveryone(ctx context.Context, on bool) {
	d.mu.Lock()
	d.waitForEveryone = on
	release := !on && d.room.holding
	if release {
		d.releaseLocked()
	}
	d.mu.Unlock()
	if release {
		d.Publish(ctx)
	}
}

// armHold marks a discontinuity that would play as one to wait on. Called
// with the lock held, before reanchor, by every transport path that starts
// the film.
func (d *Director) armHold(r *room) {
	if !d.waitForEveryone || r.mediaID == "" {
		return
	}
	now := d.clock.NowMS()
	r.holding = true
	r.holdSince = now
	r.holdCold = d.freshCountLocked(now) == 0
}

// freshCountLocked is how many clients have said anything recently.
func (d *Director) freshCountLocked(now int64) int {
	n := 0
	for _, rep := range d.reports {
		if now-rep.seenAt <= ReportFreshMS {
			n++
		}
	}
	return n
}

// Report records what a client says about its own readiness.
//
// Clients report while a title is loaded whether or not the room is holding,
// which is what makes the *next* hold able to decide quickly: the window is
// already populated when the command lands.
func (d *Director) Report(ctx context.Context, identity, name string, r proto.PlaybackReady) {
	if identity == "" {
		return
	}
	d.mu.Lock()
	now := d.clock.NowMS()
	before := d.waitingKeyLocked()
	d.reports[identity] = report{name: name, gen: r.Gen, ready: r.Ready, seenAt: now}
	d.pruneLocked(now)
	// Evaluating here as well as in the run loop is what keeps the release
	// prompt: the last person to finish buffering is usually the one whose
	// report arrives, and making them wait up to another second for a tick
	// would be a second of everybody staring at a card for no reason.
	released := d.evaluateHoldLocked()
	changed := released || d.waitingKeyLocked() != before
	d.mu.Unlock()
	if changed {
		d.Publish(ctx)
	}
}

// Start is the host overriding a hold: go now, whoever is not ready.
func (d *Director) Start(ctx context.Context) error {
	d.mu.Lock()
	if d.room.mediaID == "" {
		d.mu.Unlock()
		return ErrNoMedia
	}
	if d.room.holding {
		d.releaseLocked()
	}
	d.mu.Unlock()
	d.Publish(ctx)
	return nil
}

// releaseLocked starts the held film, re-anchoring at the position it was
// parked at. Re-anchoring is the point: the clients have been sitting at that
// frame for however long the wait took, and an anchor from before the hold
// would tell them the film had been running all along.
func (d *Director) releaseLocked() {
	r := d.room
	r.holding = false
	d.reanchor(r, r.anchorPosMS)
}

// evaluateHoldLocked releases the hold if it is satisfied, and reports
// whether it did.
func (d *Director) evaluateHoldLocked() bool {
	r := d.room
	if !r.holding {
		return false
	}
	if !d.canStartLocked() {
		return false
	}
	d.releaseLocked()
	return true
}

// canStartLocked is the decision itself, split out because it is the part
// worth reading: everyone who is still talking to us is ready for the
// generation we are actually waiting at, or we have waited long enough.
func (d *Director) canStartLocked() bool {
	r := d.room
	now := d.clock.NowMS()
	if !d.waitForEveryone {
		return true
	}
	if now-r.holdSince >= HoldTimeoutMS {
		return true
	}
	// A cold start collects answers before it judges them. Without this the
	// first client to say "ready" releases the room while the second has not
	// been heard from at all — which is the room's first film, every time.
	if r.holdCold && now-r.holdSince < holdGraceMS {
		return false
	}
	fresh, blocked := 0, 0
	for _, rep := range d.reports {
		if now-rep.seenAt > ReportFreshMS {
			continue
		}
		fresh++
		// A report for an older generation is not a "no", but it is not a
		// "yes" either: it answers a question about a position the room has
		// already left.
		if !rep.ready || rep.gen != r.gen {
			blocked++
		}
	}
	if fresh == 0 {
		// Nobody is talking to us: an empty room, a projector-only room, or
		// clients on an older build. Start rather than wedge the room.
		return true
	}
	return blocked == 0
}

// waitingForLocked names the people still buffering, newest information
// first-come; sorted so the card does not reshuffle every second.
func (d *Director) waitingForLocked() []string {
	r := d.room
	now := d.clock.NowMS()
	var names []string
	for identity, rep := range d.reports {
		if now-rep.seenAt > ReportFreshMS {
			continue
		}
		if rep.ready && rep.gen == r.gen {
			continue
		}
		name := rep.name
		if name == "" {
			name = identity
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// waitingKeyLocked is the waiting list as one comparable string, so Report can
// tell whether anything a client would see has actually changed.
func (d *Director) waitingKeyLocked() string {
	if !d.room.holding {
		return ""
	}
	return strings.Join(d.waitingForLocked(), "\x00")
}

// pruneLocked drops clients that stopped talking to us a minute ago.
func (d *Director) pruneLocked(now int64) {
	for identity, rep := range d.reports {
		if now-rep.seenAt > reportTTLMS {
			delete(d.reports, identity)
		}
	}
}

// Publish broadcasts the current state to the room.
func (d *Director) Publish(ctx context.Context) {
	if d.bcast == nil {
		return
	}
	st := d.Snapshot()
	payload, err := encode(st)
	if err != nil {
		return
	}
	_ = d.bcast.Broadcast(ctx, proto.TopicPlayback, payload)
}

// Run advances the playlist and keeps late joiners fresh.
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
			d.Tick(ctx)
		}
	}
}

// Tick is one pass of the run loop, factored out so tests can drive the hold
// timeout and the freshness window without sleeping through them.
func (d *Director) Tick(ctx context.Context) {
	if d.ended() {
		d.advance(ctx)
	}
	d.mu.Lock()
	d.pruneLocked(d.clock.NowMS())
	released := d.evaluateHoldLocked()
	active := d.room.mediaID != ""
	d.mu.Unlock()
	if released || active {
		d.Publish(ctx)
	}
}

// ended reports whether the current title has run out.
func (d *Director) ended() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	r := d.room
	return r.mediaID != "" && !stopped(r) && r.durationMS > 0 && d.posMS(r) >= r.durationMS
}

// advance moves to the next queued title, or holds at the end.
func (d *Director) advance(ctx context.Context) {
	d.mu.Lock()
	r := d.room
	if len(r.queue) == 0 {
		// Hold on the last frame rather than unloading: the room is still
		// watching something, it has simply finished.
		r.paused = true
		r.holding = false
		d.reanchor(r, r.durationMS)
		d.mu.Unlock()
		d.Publish(ctx)
		return
	}
	next := r.queue[0]
	r.queue = r.queue[1:]
	d.mu.Unlock()
	// No actor: the playlist advancing is the server's doing, and the card
	// that results says "Now playing" rather than naming anyone.
	if err := d.load(ctx, proto.PlaybackActor{}, next, false); err != nil {
		// A queued title that has since been deleted should not wedge the
		// room; drop it and try the next one on the following tick.
		d.mu.Lock()
		d.room.paused = true
		d.room.holding = false
		d.mu.Unlock()
		d.Publish(ctx)
	}
}
