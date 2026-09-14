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

// alice is whoever pressed the button in these tests; the actor only matters
// to the intent echo, which has its own tests.
var alice = proto.PlaybackActor{Identity: "u_alice", Name: "Alice", Color: "#8ab4f8"}

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
	ints   []proto.PlaybackIntent
	order  []string
}

func (c *capture) Broadcast(_ context.Context, topic string, payload []byte) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	switch topic {
	case proto.TopicPlayback:
		var st proto.PlaybackState
		if err := json.Unmarshal(payload, &st); err != nil {
			return err
		}
		c.states = append(c.states, st)
	case proto.TopicPlaybackIntent:
		var in proto.PlaybackIntent
		if err := json.Unmarshal(payload, &in); err != nil {
			return err
		}
		c.ints = append(c.ints, in)
	default:
		return nil
	}
	c.order = append(c.order, topic)
	return nil
}

// topics records the order broadcasts went out in, which is the whole
// contract for the intent echo: a client that learned the new position first
// would have jumped before being told why.
func (c *capture) topics() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.order...)
}

func (c *capture) intents() []proto.PlaybackIntent {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]proto.PlaybackIntent(nil), c.ints...)
}

func (c *capture) lastIntent(t *testing.T) proto.PlaybackIntent {
	t.Helper()
	ins := c.intents()
	if len(ins) == 0 {
		t.Fatal("nothing was echoed on playback.intent")
	}
	return ins[len(ins)-1]
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
	if err := d.Load(ctx, alice, "film"); err != nil {
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

	if err := d.SetPaused(ctx, alice, true); err != nil {
		t.Fatal(err)
	}
	frozen := d.Snapshot().PosMS
	time.Sleep(60 * time.Millisecond)
	if again := d.Snapshot().PosMS; again != frozen {
		t.Errorf("position moved while paused: %d then %d", frozen, again)
	}

	// Resuming must continue from where it stopped, not from where the clock
	// would have carried it.
	if err := d.SetPaused(ctx, alice, false); err != nil {
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
	if err := d.Load(ctx, alice, "film"); err != nil {
		t.Fatal(err)
	}

	seen := map[int64]bool{d.Snapshot().Gen: true}
	steps := []struct {
		name string
		do   func()
	}{
		{"pause", func() { _ = d.SetPaused(ctx, alice, true) }},
		{"resume", func() { _ = d.SetPaused(ctx, alice, false) }},
		{"seek", func() { _, _ = d.Seek(ctx, alice, 30_000, false) }},
		{"relative seek", func() { _, _ = d.Seek(ctx, alice, -10_000, true) }},
		{"toggle", func() { _, _ = d.TogglePause(ctx, alice) }},
		{"stop", func() { d.Stop(ctx, alice) }},
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
	if err := d.Load(ctx, alice, "film"); err != nil {
		t.Fatal(err)
	}

	landed, err := d.Seek(ctx, alice, 120_000, false)
	if err != nil {
		t.Fatal(err)
	}
	if landed != 120_000 {
		t.Errorf("absolute seek landed at %d, want 120000", landed)
	}

	if landed, err = d.Seek(ctx, alice, -20_000, true); err != nil {
		t.Fatal(err)
	}
	if landed < 99_000 || landed > 101_000 {
		t.Errorf("relative seek landed at %d, want about 100000", landed)
	}

	// Seeking before the start clamps rather than going negative, which would
	// put every client's target in the past.
	if landed, err = d.Seek(ctx, alice, -999_000, true); err != nil {
		t.Fatal(err)
	}
	if landed != 0 {
		t.Errorf("seek past the start landed at %d, want 0", landed)
	}

	// And past the end clamps to the duration.
	if landed, err = d.Seek(ctx, alice, 99_999_999, false); err != nil {
		t.Fatal(err)
	}
	if landed != 7_200_000 {
		t.Errorf("seek past the end landed at %d, want the duration", landed)
	}
}

func TestTransportNeedsMedia(t *testing.T) {
	d, _ := newDirector(t)
	ctx := context.Background()
	if err := d.SetPaused(ctx, alice, true); err == nil {
		t.Error("pausing an idle room was accepted")
	}
	if _, err := d.Seek(ctx, alice, 1000, false); err == nil {
		t.Error("seeking an idle room was accepted")
	}
	if err := d.Load(ctx, alice, "missing"); err == nil {
		t.Error("loading a title that does not exist was accepted")
	}
}

func TestEnqueueAndAdvance(t *testing.T) {
	d, c := newDirector(t)
	ctx := context.Background()

	// Enqueuing into an idle room starts it, rather than queueing behind
	// nothing.
	if err := d.Enqueue(ctx, alice, "short"); err != nil {
		t.Fatal(err)
	}
	if st := d.Snapshot(); st.MediaID != "short" || st.Idle {
		t.Fatalf("enqueue on an idle room did not start it: %+v", st)
	}

	// A second one queues behind it instead of interrupting.
	if err := d.Enqueue(ctx, alice, "next"); err != nil {
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
	if err := d.Load(ctx, alice, "short"); err != nil {
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
	if err := d.Load(ctx, alice, "film"); err != nil {
		t.Fatal(err)
	}
	before := len(c.states)
	_ = d.SetPaused(ctx, alice, true)
	_, _ = d.Seek(ctx, alice, 1000, false)
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
	if err := d.Load(ctx, alice, "film"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Seek(ctx, alice, 7_200_000, false); err != nil {
		t.Fatal(err)
	}
	if err := d.SetPaused(ctx, alice, true); err != nil {
		t.Fatal(err)
	}
	if st := d.Snapshot(); st.PosMS != st.DurationMS {
		t.Fatalf("setup: expected to be parked at the end, got %d", st.PosMS)
	}

	if err := d.SetPaused(ctx, alice, false); err != nil {
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
	if err := d.Load(ctx, alice, "film"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Seek(ctx, alice, 7_200_000, false); err != nil {
		t.Fatal(err)
	}
	if _, err := d.TogglePause(ctx, alice); err != nil { // -> paused at end
		t.Fatal(err)
	}
	if _, err := d.TogglePause(ctx, alice); err != nil { // -> play
		t.Fatal(err)
	}
	if st := d.Snapshot(); st.PosMS > 1000 {
		t.Errorf("toggling play at the end resumed at %d ms, want a restart", st.PosMS)
	}
}

// waitForEveryone ----------------------------------------------------------

// fakeClock lets the hold's six-second window and twenty-second timeout be
// tested in microseconds instead of slept through.
type fakeClock struct{ ms int64 }

func (c *fakeClock) NowMS() int64     { return c.ms }
func (c *fakeClock) advance(ms int64) { c.ms += ms }

func newHoldingDirector(t *testing.T) (*Director, *capture, *fakeClock) {
	t.Helper()
	c := &capture{}
	clk := &fakeClock{ms: 1_700_000_000_000}
	d := NewWithClock(c, stubResolver{durations: map[string]int64{
		"short": 1500, "film": 7_200_000, "next": 60_000,
	}}, clk)
	d.SetWaitForEveryone(context.Background(), true)
	return d, c, clk
}

func ready(gen int64) proto.PlaybackReady {
	return proto.PlaybackReady{Gen: gen, BufferedAheadMS: 5000, Ready: true}
}

func notReady(gen int64) proto.PlaybackReady {
	return proto.PlaybackReady{Gen: gen, BufferedAheadMS: 300, Ready: false}
}

// pastGrace moves past the cold-start collection window, where a hold that
// began with nobody reporting waits for the first answers.
func pastGrace(d *Director, clk *fakeClock) {
	clk.advance(holdGraceMS + 1)
	d.Tick(context.Background())
}

// The whole point: loading with the setting on parks the room rather than
// starting it, and says who it is waiting for.
func TestHoldOnLoadUntilEveryoneIsReady(t *testing.T) {
	d, _, clk := newHoldingDirector(t)
	ctx := context.Background()
	if err := d.Load(ctx, alice, "film"); err != nil {
		t.Fatal(err)
	}

	st := d.Snapshot()
	if !st.Holding {
		t.Fatal("loading with waitForEveryone on did not hold")
	}
	if !st.Paused {
		t.Error("a held room must read as paused; clients have nothing else to go on")
	}
	gen := st.Gen

	// Alice answers first and says go. The cold-start window is what stops
	// that from starting the film before Bob has been heard from at all.
	d.Report(ctx, "a", "Alice", ready(gen))
	if !d.Snapshot().Holding {
		t.Fatal("started on the first answer, before Bob had been heard from at all")
	}
	d.Report(ctx, "b", "Bob", notReady(gen))
	pastGrace(d, clk)

	st = d.Snapshot()
	if !st.Holding {
		t.Fatal("released while Bob was still buffering")
	}
	if len(st.WaitingFor) != 1 || st.WaitingFor[0] != "Bob" {
		t.Errorf("waitingFor = %v, want [Bob]", st.WaitingFor)
	}
	// Time must not pass for the film while it is held.
	clk.advance(2000)
	if pos := d.Snapshot().PosMS; pos != 0 {
		t.Errorf("a held film advanced to %d ms", pos)
	}

	d.Report(ctx, "b", "Bob", ready(gen))
	st = d.Snapshot()
	if st.Holding || st.Paused {
		t.Fatalf("still held after everyone reported ready: %+v", st)
	}
	// Releasing re-anchors: the clients sat on that frame for three seconds,
	// and an anchor from before the hold would claim the film had been running.
	if st.AnchorAtMS != clk.ms {
		t.Errorf("anchorAt = %d, want the release moment %d", st.AnchorAtMS, clk.ms)
	}
	if st.Gen == gen {
		t.Error("releasing did not change gen; clients would not know to start")
	}
}

// A report for a position the room has already left is not an answer to the
// question being asked.
func TestStaleGenerationReportsDoNotRelease(t *testing.T) {
	d, _, clk := newHoldingDirector(t)
	ctx := context.Background()
	if err := d.Load(ctx, alice, "film"); err != nil {
		t.Fatal(err)
	}
	gen := d.Snapshot().Gen
	d.Report(ctx, "a", "Alice", ready(gen))
	pastGrace(d, clk)
	if d.Snapshot().Holding {
		t.Fatal("setup: the only client reported ready and the hold did not release")
	}

	// Now seek while playing: a new hold, at a new generation, and Alice's
	// old "I am ready" must not satisfy it.
	if _, err := d.Seek(ctx, alice, 600_000, false); err != nil {
		t.Fatal(err)
	}
	st := d.Snapshot()
	if !st.Holding {
		t.Fatal("seeking while playing did not hold")
	}
	if st.Gen == gen {
		t.Fatal("setup: the seek did not change gen")
	}
	if len(st.WaitingFor) != 1 || st.WaitingFor[0] != "Alice" {
		t.Errorf("waitingFor = %v, want [Alice]: her report is for the old position", st.WaitingFor)
	}

	d.Report(ctx, "a", "Alice", ready(st.Gen))
	if d.Snapshot().Holding {
		t.Error("still held after a report for the current generation")
	}
}

// A client on a hopeless link must not be able to stop the film for good.
func TestHoldTimesOut(t *testing.T) {
	d, _, clk := newHoldingDirector(t)
	ctx := context.Background()
	if err := d.Load(ctx, alice, "film"); err != nil {
		t.Fatal(err)
	}
	gen := d.Snapshot().Gen
	// Carol keeps reporting, as a real client does every two seconds — so
	// this is the timeout doing the work, not her going stale.
	for elapsed := int64(0); elapsed < HoldTimeoutMS-2000; elapsed += 2000 {
		d.Report(ctx, "slow", "Carol", notReady(gen))
		clk.advance(2000)
		d.Tick(ctx)
	}
	if !d.Snapshot().Holding {
		t.Fatal("released before the timeout while Carol was still saying no")
	}

	d.Report(ctx, "slow", "Carol", notReady(gen))
	clk.advance(3000)
	d.Tick(ctx)
	st := d.Snapshot()
	if st.Holding {
		t.Fatal("the hold never timed out; Carol could stop the film forever")
	}
	if st.Paused {
		t.Error("timing out should start the film, not pause it")
	}
}

// Someone who has closed their laptop is not someone to wait for.
func TestStaleReportsAreNotWaitedFor(t *testing.T) {
	d, _, clk := newHoldingDirector(t)
	ctx := context.Background()
	if err := d.Load(ctx, alice, "film"); err != nil {
		t.Fatal(err)
	}
	gen := d.Snapshot().Gen
	d.Report(ctx, "gone", "Dave", notReady(gen))
	pastGrace(d, clk)
	if !d.Snapshot().Holding {
		t.Fatal("setup: should be holding for Dave")
	}

	clk.advance(ReportFreshMS + 500)
	d.Tick(ctx)
	if d.Snapshot().Holding {
		t.Error("still waiting on a client that stopped reporting six seconds ago")
	}
}

// Nobody reporting at all: wait a beat for the first answers, then go rather
// than wedge a room whose clients are all on an older build.
func TestSilentRoomStartsAfterTheGrace(t *testing.T) {
	d, _, clk := newHoldingDirector(t)
	ctx := context.Background()
	if err := d.Load(ctx, alice, "film"); err != nil {
		t.Fatal(err)
	}
	d.Tick(ctx)
	if !d.Snapshot().Holding {
		t.Fatal("released instantly; the first reports never had a chance to arrive")
	}
	clk.advance(holdGraceMS + 100)
	d.Tick(ctx)
	if d.Snapshot().Holding {
		t.Error("a room where nothing reports stayed held forever")
	}
}

func TestStartOverridesTheHold(t *testing.T) {
	d, _, clk := newHoldingDirector(t)
	ctx := context.Background()
	if err := d.Load(ctx, alice, "film"); err != nil {
		t.Fatal(err)
	}
	d.Report(ctx, "slow", "Carol", notReady(d.Snapshot().Gen))
	pastGrace(d, clk)
	if !d.Snapshot().Holding {
		t.Fatal("setup: should be holding")
	}
	if err := d.Start(ctx); err != nil {
		t.Fatal(err)
	}
	st := d.Snapshot()
	if st.Holding || st.Paused {
		t.Errorf("start did not get the film going: %+v", st)
	}
}

// Turning the setting off is also an answer to "how long are we waiting".
func TestTurningTheSettingOffReleases(t *testing.T) {
	d, _, clk := newHoldingDirector(t)
	ctx := context.Background()
	if err := d.Load(ctx, alice, "film"); err != nil {
		t.Fatal(err)
	}
	d.Report(ctx, "slow", "Carol", notReady(d.Snapshot().Gen))
	pastGrace(d, clk)
	if !d.Snapshot().Holding {
		t.Fatal("setup: should be holding")
	}
	d.SetWaitForEveryone(ctx, false)
	if d.Snapshot().Holding {
		t.Error("still holding after waitForEveryone was turned off")
	}
}

// Pausing and scrubbing must not put a "waiting for everyone" card over a
// picture that was already still.
func TestPauseAndPausedSeekDoNotHold(t *testing.T) {
	d, _, clk := newHoldingDirector(t)
	ctx := context.Background()
	if err := d.Load(ctx, alice, "film"); err != nil {
		t.Fatal(err)
	}
	d.Report(ctx, "a", "Alice", ready(d.Snapshot().Gen))
	pastGrace(d, clk)

	if err := d.SetPaused(ctx, alice, true); err != nil {
		t.Fatal(err)
	}
	if st := d.Snapshot(); st.Holding {
		t.Error("pausing entered a hold")
	}
	if _, err := d.Seek(ctx, alice, 120_000, false); err != nil {
		t.Fatal(err)
	}
	st := d.Snapshot()
	if st.Holding {
		t.Error("seeking while paused entered a hold; nothing was going to start")
	}
	if !st.Paused {
		t.Error("seeking while paused started playing")
	}

	// Resuming does hold, and toggling out of a hold pauses rather than plays.
	if err := d.SetPaused(ctx, alice, false); err != nil {
		t.Fatal(err)
	}
	if !d.Snapshot().Holding {
		t.Fatal("resuming did not hold")
	}
	paused, err := d.TogglePause(ctx, alice)
	if err != nil {
		t.Fatal(err)
	}
	if !paused {
		t.Error("toggling a held room should pause it, not start it")
	}
	if d.Snapshot().Holding {
		t.Error("toggling to pause left the hold armed")
	}
}

// With the setting off nothing changes, which is the default everyone gets.
func TestNoHoldWhenTheSettingIsOff(t *testing.T) {
	d, _ := newDirector(t)
	ctx := context.Background()
	if err := d.Load(ctx, alice, "film"); err != nil {
		t.Fatal(err)
	}
	st := d.Snapshot()
	if st.Holding || st.Paused {
		t.Errorf("held with waitForEveryone off: %+v", st)
	}
}

// The intent echo ----------------------------------------------------------

// The order is the contract. A client that got the state first would have
// jumped to the new position before being told whose doing it was.
func TestIntentIsBroadcastBeforeTheState(t *testing.T) {
	d, c, _ := newHoldingDirector(t)
	d.SetWaitForEveryone(context.Background(), false)
	ctx := context.Background()
	if err := d.Load(ctx, alice, "film"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Seek(ctx, alice, 600_000, false); err != nil {
		t.Fatal(err)
	}
	if err := d.SetPaused(ctx, alice, true); err != nil {
		t.Fatal(err)
	}

	got := c.topics()
	want := []string{
		proto.TopicPlaybackIntent, proto.TopicPlayback, // load
		proto.TopicPlaybackIntent, proto.TopicPlayback, // seek
		proto.TopicPlaybackIntent, proto.TopicPlayback, // pause
	}
	if len(got) != len(want) {
		t.Fatalf("broadcast topics = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("broadcast topics = %v, want %v", got, want)
		}
	}

	// Sequence numbers so a client can drop an echo that overtook a newer one.
	ins := c.intents()
	for i := 1; i < len(ins); i++ {
		if ins[i].Seq <= ins[i-1].Seq {
			t.Fatalf("intent seq did not increase: %d then %d", ins[i-1].Seq, ins[i].Seq)
		}
	}
}

func TestIntentContents(t *testing.T) {
	d, c, clk := newHoldingDirector(t)
	d.SetWaitForEveryone(context.Background(), false)
	ctx := context.Background()

	if err := d.Load(ctx, alice, "film"); err != nil {
		t.Fatal(err)
	}
	in := c.lastIntent(t)
	if in.Action != proto.IntentLoad {
		t.Errorf("action = %q, want load", in.Action)
	}
	if in.MediaID != "film" || in.Title != "Title film" {
		t.Errorf("load intent did not name the film: %+v", in)
	}
	if in.Actor.Name != "Alice" || in.Actor.Color != alice.Color {
		t.Errorf("actor = %+v, want Alice", in.Actor)
	}
	if in.TS != clk.ms {
		t.Errorf("ts = %d, want the director's clock %d", in.TS, clk.ms)
	}

	// A seek carries where the room was as well as where it is going: that
	// difference is what decides a corner chip from a centred glyph.
	clk.advance(10_000)
	if _, err := d.Seek(ctx, alice, 3_600_000, false); err != nil {
		t.Fatal(err)
	}
	in = c.lastIntent(t)
	if in.Action != proto.IntentSeek {
		t.Errorf("action = %q, want seek", in.Action)
	}
	if in.FromMS < 9_000 || in.FromMS > 11_000 {
		t.Errorf("fromMs = %d, want about 10000 — the position before the seek", in.FromMS)
	}
	if in.ToMS != 3_600_000 {
		t.Errorf("toMs = %d, want 3600000", in.ToMS)
	}

	// Pause and play are distinct actions, not a flag on one.
	if err := d.SetPaused(ctx, alice, true); err != nil {
		t.Fatal(err)
	}
	if got := c.lastIntent(t).Action; got != proto.IntentPause {
		t.Errorf("action = %q, want pause", got)
	}
	if _, err := d.TogglePause(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if got := c.lastIntent(t).Action; got != proto.IntentPlay {
		t.Errorf("toggling out of pause echoed %q, want play", got)
	}
	d.Stop(ctx, alice)
	if got := c.lastIntent(t).Action; got != proto.IntentStop {
		t.Errorf("action = %q, want stop", got)
	}
}

// A late joiner gets the echo in the state, because the loading card for the
// film the room is in the middle of starting is exactly what they need.
func TestLastIntentTravelsInTheState(t *testing.T) {
	d, _, _ := newHoldingDirector(t)
	d.SetWaitForEveryone(context.Background(), false)
	ctx := context.Background()
	if st := d.Snapshot(); st.LastIntent != nil {
		t.Fatalf("an untouched room has a lastIntent: %+v", st.LastIntent)
	}
	if err := d.Load(ctx, alice, "film"); err != nil {
		t.Fatal(err)
	}
	st := d.Snapshot()
	if st.LastIntent == nil {
		t.Fatal("lastIntent missing from the state after a load")
	}
	if st.LastIntent.MediaID != "film" || st.LastIntent.Actor.Name != "Alice" {
		t.Errorf("lastIntent = %+v", st.LastIntent)
	}
}

// The playlist advancing is the server's doing and names nobody.
func TestAdvanceEchoesWithNoActor(t *testing.T) {
	d, c := newDirector(t)
	ctx := context.Background()
	if err := d.Enqueue(ctx, alice, "short"); err != nil {
		t.Fatal(err)
	}
	if err := d.Enqueue(ctx, alice, "next"); err != nil {
		t.Fatal(err)
	}
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
	in := c.lastIntent(t)
	if in.Action != proto.IntentLoad || in.MediaID != "next" {
		t.Fatalf("advancing did not echo a load of the next title: %+v", in)
	}
	if in.Actor.Name != "" || in.Actor.Identity != "" {
		t.Errorf("the playlist advancing named an actor: %+v", in.Actor)
	}
}
