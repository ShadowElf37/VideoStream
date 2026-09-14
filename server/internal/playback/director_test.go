package playback

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/ShadowElf37/VideoStream/proto"
	"github.com/ShadowElf37/VideoStream/server/internal/media"
)

type stubResolver struct{ durations map[string]int64 }

func (s stubResolver) Resolve(id string) (int64, string, string, error) {
	d, ok := s.durations[id]
	if !ok {
		return 0, "", "", media.ErrNotFound
	}
	return d, "Title " + id, "/media/" + id + "/movie.mp4?e=1&s=x", nil
}

type capture struct {
	mu     sync.Mutex
	states []proto.PlaybackState
}

func (c *capture) Broadcast(_ context.Context, topic string, payload []byte) error {
	if topic != proto.TopicPlayback {
		return nil
	}
	var st proto.PlaybackState
	if err := json.Unmarshal(payload, &st); err != nil {
		return err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.states = append(c.states, st)
	return nil
}

func (c *capture) last() (proto.PlaybackState, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.states) == 0 {
		return proto.PlaybackState{}, false
	}
	return c.states[len(c.states)-1], true
}

func newDirector(t *testing.T) (*Director, *capture) {
	t.Helper()
	c := &capture{}
	return New(c, stubResolver{durations: map[string]int64{
		"short": 1500, "film": 7_200_000, "next": 60_000,
	}}), c
}

// The anchor is the whole contract: a client reconstructs its target from it,
// so a paused room must not advance and a playing one must advance with the
// clock.
func TestAnchorAdvancesOnlyWhilePlaying(t *testing.T) {
	d, _ := newDirector(t)
	ctx := context.Background()
	if err := d.Load(ctx, "film"); err != nil {
		t.Fatal(err)
	}

	st := d.Snapshot()
	if st.Paused {
		t.Fatal("a freshly loaded title should be playing")
	}
	if st.Rate != 1 {
		t.Errorf("rate = %v, want 1", st.Rate)
	}

	time.Sleep(60 * time.Millisecond)
	advanced := d.Snapshot().PosMS
	if advanced <= 0 {
		t.Errorf("position did not advance while playing: %d", advanced)
	}

	if err := d.SetPaused(ctx, true); err != nil {
		t.Fatal(err)
	}
	frozen := d.Snapshot().PosMS
	time.Sleep(60 * time.Millisecond)
	if again := d.Snapshot().PosMS; again != frozen {
		t.Errorf("position moved while paused: %d then %d", frozen, again)
	}

	// Resuming must continue from where it stopped, not from where the clock
	// would have carried it.
	if err := d.SetPaused(ctx, false); err != nil {
		t.Fatal(err)
	}
	if resumed := d.Snapshot().PosMS; resumed < frozen || resumed > frozen+200 {
		t.Errorf("resumed at %d, want to continue from about %d", resumed, frozen)
	}
}

// gen is a client's licence to jump. If it did not change on a discontinuity,
// a client would treat the jump as its own drift and try to smooth it out.
func TestGenerationChangesOnEveryDiscontinuity(t *testing.T) {
	d, _ := newDirector(t)
	ctx := context.Background()
	if err := d.Load(ctx, "film"); err != nil {
		t.Fatal(err)
	}

	seen := map[int64]bool{d.Snapshot().Gen: true}
	steps := []struct {
		name string
		do   func()
	}{
		{"pause", func() { _ = d.SetPaused(ctx, true) }},
		{"resume", func() { _ = d.SetPaused(ctx, false) }},
		{"seek", func() { _, _ = d.Seek(ctx, 30_000, false) }},
		{"relative seek", func() { _, _ = d.Seek(ctx, -10_000, true) }},
		{"toggle", func() { _, _ = d.TogglePause(ctx) }},
		{"stop", func() { d.Stop(ctx) }},
	}
	for _, s := range steps {
		s.do()
		g := d.Snapshot().Gen
		if seen[g] {
			t.Errorf("%s did not change gen (still %d)", s.name, g)
		}
		seen[g] = true
	}
}

func TestSeek(t *testing.T) {
	d, _ := newDirector(t)
	ctx := context.Background()
	if err := d.Load(ctx, "film"); err != nil {
		t.Fatal(err)
	}

	landed, err := d.Seek(ctx, 120_000, false)
	if err != nil {
		t.Fatal(err)
	}
	if landed != 120_000 {
		t.Errorf("absolute seek landed at %d, want 120000", landed)
	}

	if landed, err = d.Seek(ctx, -20_000, true); err != nil {
		t.Fatal(err)
	}
	if landed < 99_000 || landed > 101_000 {
		t.Errorf("relative seek landed at %d, want about 100000", landed)
	}

	// Seeking before the start clamps rather than going negative, which would
	// put every client's target in the past.
	if landed, err = d.Seek(ctx, -999_000, true); err != nil {
		t.Fatal(err)
	}
	if landed != 0 {
		t.Errorf("seek past the start landed at %d, want 0", landed)
	}

	// And past the end clamps to the duration.
	if landed, err = d.Seek(ctx, 99_999_999, false); err != nil {
		t.Fatal(err)
	}
	if landed != 7_200_000 {
		t.Errorf("seek past the end landed at %d, want the duration", landed)
	}
}

func TestTransportNeedsMedia(t *testing.T) {
	d, _ := newDirector(t)
	ctx := context.Background()
	if err := d.SetPaused(ctx, true); err == nil {
		t.Error("pausing an idle room was accepted")
	}
	if _, err := d.Seek(ctx, 1000, false); err == nil {
		t.Error("seeking an idle room was accepted")
	}
	if err := d.Load(ctx, "missing"); err == nil {
		t.Error("loading a title that does not exist was accepted")
	}
}

func TestEnqueueAndAdvance(t *testing.T) {
	d, c := newDirector(t)
	ctx := context.Background()

	// Enqueuing into an idle room starts it, rather than queueing behind
	// nothing.
	if err := d.Enqueue(ctx, "short"); err != nil {
		t.Fatal(err)
	}
	if st := d.Snapshot(); st.MediaID != "short" || st.Idle {
		t.Fatalf("enqueue on an idle room did not start it: %+v", st)
	}

	// A second one queues behind it instead of interrupting.
	if err := d.Enqueue(ctx, "next"); err != nil {
		t.Fatal(err)
	}
	st := d.Snapshot()
	if st.MediaID != "short" {
		t.Errorf("enqueue interrupted playback: now playing %q", st.MediaID)
	}
	if len(st.Queue) != 1 || st.Queue[0] != "next" {
		t.Errorf("queue = %v, want [next]", st.Queue)
	}

	// "short" is 1.5s long; the run loop should move on once it ends.
	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go d.Run(loopCtx)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if d.Snapshot().MediaID == "next" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	final := d.Snapshot()
	if final.MediaID != "next" {
		t.Fatalf("playlist did not advance when the title ended: still %q at %d ms",
			final.MediaID, final.PosMS)
	}
	if final.Paused {
		t.Error("paused after advancing; the next item should just play")
	}
	if len(final.Queue) != 0 {
		t.Errorf("queue = %v after advancing, want empty", final.Queue)
	}
	if _, ok := c.last(); !ok {
		t.Error("nothing was broadcast")
	}
}

// With nothing queued, the end of a film holds on the last frame rather than
// unloading — the room is still watching something, it has simply finished.
func TestEndWithEmptyQueueHolds(t *testing.T) {
	d, _ := newDirector(t)
	ctx := context.Background()
	if err := d.Load(ctx, "short"); err != nil {
		t.Fatal(err)
	}
	loopCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go d.Run(loopCtx)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if d.Snapshot().Paused {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	st := d.Snapshot()
	if !st.Paused {
		t.Fatal("still playing past the end")
	}
	if st.MediaID != "short" {
		t.Errorf("unloaded at the end; media is now %q", st.MediaID)
	}
	if st.PosMS != st.DurationMS {
		t.Errorf("held at %d, want the duration %d", st.PosMS, st.DurationMS)
	}
}

func TestEveryCommandBroadcasts(t *testing.T) {
	d, c := newDirector(t)
	ctx := context.Background()
	if err := d.Load(ctx, "film"); err != nil {
		t.Fatal(err)
	}
	before := len(c.states)
	_ = d.SetPaused(ctx, true)
	_, _ = d.Seek(ctx, 1000, false)
	if len(c.states) < before+2 {
		t.Errorf("%d broadcasts for 2 commands; clients would not hear about them",
			len(c.states)-before)
	}
	// Sequence numbers let a client drop a packet that overtook a newer one.
	for i := 1; i < len(c.states); i++ {
		if c.states[i].Seq <= c.states[i-1].Seq {
			t.Fatalf("seq did not increase: %d then %d", c.states[i-1].Seq, c.states[i].Seq)
		}
	}
}

// Pressing play on a finished film restarts it. Resuming at the end would be
// noticed by the run loop as "past the duration" and paused straight back, so
// play would appear to do nothing.
func TestPlayAtEndRestarts(t *testing.T) {
	d, _ := newDirector(t)
	ctx := context.Background()
	if err := d.Load(ctx, "film"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Seek(ctx, 7_200_000, false); err != nil {
		t.Fatal(err)
	}
	if err := d.SetPaused(ctx, true); err != nil {
		t.Fatal(err)
	}
	if st := d.Snapshot(); st.PosMS != st.DurationMS {
		t.Fatalf("setup: expected to be parked at the end, got %d", st.PosMS)
	}

	if err := d.SetPaused(ctx, false); err != nil {
		t.Fatal(err)
	}
	st := d.Snapshot()
	if st.Paused {
		t.Error("still paused after pressing play")
	}
	if st.PosMS > 1000 {
		t.Errorf("resumed at %d ms; a finished film should restart", st.PosMS)
	}
}

func TestTogglePlayAtEndRestarts(t *testing.T) {
	d, _ := newDirector(t)
	ctx := context.Background()
	if err := d.Load(ctx, "film"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Seek(ctx, 7_200_000, false); err != nil {
		t.Fatal(err)
	}
	if _, err := d.TogglePause(ctx); err != nil { // -> paused at end
		t.Fatal(err)
	}
	if _, err := d.TogglePause(ctx); err != nil { // -> play
		t.Fatal(err)
	}
	if st := d.Snapshot(); st.PosMS > 1000 {
		t.Errorf("toggling play at the end resumed at %d ms, want a restart", st.PosMS)
	}
}
