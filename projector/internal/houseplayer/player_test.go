package houseplayer

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/ShadowElf37/VideoStream/projector/internal/vsm"
)

type capture struct {
	mu     sync.Mutex
	video  []uint32
	audio  []uint32
	tracks int
}

func (c *capture) WriteVideo(au []byte, ts uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.video = append(c.video, ts)
	return nil
}

func (c *capture) WriteAudio(p []byte, ts uint32) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.audio = append(c.audio, ts)
	return nil
}

func (c *capture) PublishTracks(w, h int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tracks++
	return nil
}

func (c *capture) snapshot() ([]uint32, []uint32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]uint32(nil), c.video...), append([]uint32(nil), c.audio...)
}

// build writes a .vsm of secs seconds at 24 fps with an IDR every second.
func build(t *testing.T, secs int) string {
	t.Helper()
	dir := t.TempDir()
	w, err := vsm.NewWriter(vsm.Header{
		Title: "T", Width: 640, Height: 360,
		FPSNum: 24, FPSDen: 1, AudioRate: 48000, AudioChannels: 2,
	}, dir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 24*secs; i++ {
		if err := w.WriteVideo([]byte{byte(i), 1, 2, 3}, i%24 == 0); err != nil {
			t.Fatal(err)
		}
		// Two 20 ms Opus packets per frame keeps audio roughly in step at 24 fps.
		for k := 0; k < 2; k++ {
			if err := w.WriteAudio([]byte{0xfc, byte(k)}, 960); err != nil {
				t.Fatal(err)
			}
		}
	}
	path := filepath.Join(dir, "t.vsm")
	if err := w.Close(path); err != nil {
		t.Fatal(err)
	}
	return path
}

func newPlayer(t *testing.T) (*Player, *capture) {
	t.Helper()
	c := &capture{}
	return New(slog.New(slog.NewTextHandler(io.Discard, nil)), c), c
}

// Timestamps must be derived from the absolute frame index and sample counter,
// never accumulated, or a long film drifts.
func TestTimestampsAreAbsoluteAndMonotonic(t *testing.T) {
	p, cap := newPlayer(t)
	if err := p.Load(build(t, 3), "replace"); err != nil {
		t.Fatalf("Load: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Millisecond)
	defer cancel()
	p.Run(ctx)

	video, audio := cap.snapshot()
	if len(video) < 4 {
		t.Fatalf("only %d video frames published; expected playback to progress", len(video))
	}
	for i, ts := range video {
		// 24 fps at a 90 kHz clock is exactly 3750 ticks per frame.
		if want := uint32(i * 3750); ts != want {
			t.Fatalf("video ts[%d] = %d, want %d", i, ts, want)
		}
	}
	for i := 1; i < len(audio); i++ {
		if audio[i] <= audio[i-1] {
			t.Fatalf("audio ts went backwards at %d: %d then %d", i, audio[i-1], audio[i])
		}
	}
	if cap.tracks != 1 {
		t.Errorf("PublishTracks called %d times, want 1", cap.tracks)
	}
}

// Playback must track the wall clock: a file cannot fall behind, so anything
// other than ~1x means the pacing maths is wrong.
func TestPlaysAtRealTime(t *testing.T) {
	p, cap := newPlayer(t)
	if err := p.Load(build(t, 5), "replace"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	p.Run(ctx)
	elapsed := time.Since(start)

	video, _ := cap.snapshot()
	played := time.Duration(len(video)) * time.Second / 24
	// Generous bounds: this asserts "paced", not "precise to the frame".
	if played < elapsed/2 || played > elapsed*2 {
		t.Errorf("published %v of video in %v of wall time; expected roughly 1x", played, elapsed)
	}
}

func TestPauseStopsOutput(t *testing.T) {
	p, cap := newPlayer(t)
	if err := p.Load(build(t, 5), "replace"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	time.Sleep(200 * time.Millisecond)
	p.SetPaused(true)
	time.Sleep(50 * time.Millisecond) // let any in-flight record land
	before, _ := cap.snapshot()
	time.Sleep(300 * time.Millisecond)
	after, _ := cap.snapshot()

	if len(after) != len(before) {
		t.Errorf("published %d more frames while paused", len(after)-len(before))
	}
	if !p.State().Paused {
		t.Error("State().Paused = false after SetPaused(true)")
	}

	p.SetPaused(false)
	time.Sleep(200 * time.Millisecond)
	resumed, _ := cap.snapshot()
	if len(resumed) <= len(after) {
		t.Error("no frames published after resuming")
	}
}

// A seek must land on a keyframe at or before the target: anywhere else and
// the decoder has no reference frame.
func TestSeekLandsOnKeyframeAndKeepsTimestampsOrdered(t *testing.T) {
	p, cap := newPlayer(t)
	if err := p.Load(build(t, 10), "replace"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	time.Sleep(150 * time.Millisecond)
	p.SeekMS(6000)
	time.Sleep(250 * time.Millisecond)

	video, _ := cap.snapshot()
	if len(video) == 0 {
		t.Fatal("nothing published")
	}
	last := video[len(video)-1]
	// 6 s at 24 fps is frame 144; the IDR at or before it is frame 144 itself.
	if want := uint32(144 * 3750); last < want {
		t.Errorf("after seeking to 6s the latest video ts is %d, want >= %d", last, want)
	}
	if pos := p.State().PosMS; pos < 5500 {
		t.Errorf("position after seek = %d ms, want >= 5500", pos)
	}
}

func TestStopGoesIdle(t *testing.T) {
	p, _ := newPlayer(t)
	if err := p.Load(build(t, 2), "replace"); err != nil {
		t.Fatal(err)
	}
	if p.State().Idle {
		t.Fatal("idle immediately after Load")
	}
	p.Stop()
	if !p.State().Idle {
		t.Error("not idle after Stop")
	}
}

// "append" is the add-to-playlist button. Treating it as "replace" cuts the
// current episode off mid-scene, which is exactly what it did.
func TestAppendQueuesInsteadOfInterrupting(t *testing.T) {
	p, cap := newPlayer(t)
	first := build(t, 4)
	second := build(t, 4)
	if err := p.Load(first, "replace"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)

	time.Sleep(200 * time.Millisecond)
	if err := p.Load(second, "append"); err != nil {
		t.Fatalf("append: %v", err)
	}
	time.Sleep(200 * time.Millisecond)

	if got := p.State().Path; got != first {
		t.Errorf("playing %q after an append; want the first file to keep playing", got)
	}
	if q := p.Playlist(); len(q) != 1 || q[0] != second {
		t.Errorf("playlist = %v, want exactly the appended file", q)
	}
	// Tracks must not be republished per file, or viewers lose the stream
	// between episodes.
	if cap.tracks != 1 {
		t.Errorf("PublishTracks called %d times, want 1", cap.tracks)
	}
}

func TestAppendOnIdlePlayerStartsPlaying(t *testing.T) {
	p, _ := newPlayer(t)
	path := build(t, 2)
	if err := p.Load(path, "append"); err != nil {
		t.Fatal(err)
	}
	if st := p.State(); st.Idle || st.Path != path {
		t.Errorf("append on an idle player did not start it: %+v", st)
	}
	if q := p.Playlist(); len(q) != 0 {
		t.Errorf("playlist = %v, want empty once it started playing", q)
	}
}

// The credits ending is when the next episode should start, not when playback
// should stop.
func TestPlaylistAdvancesAtEndOfFile(t *testing.T) {
	p, cap := newPlayer(t)
	first := build(t, 1) // one second, so the end arrives quickly
	second := build(t, 4)
	if err := p.Load(first, "replace"); err != nil {
		t.Fatal(err)
	}
	if err := p.Load(second, "append"); err != nil {
		t.Fatal(err)
	}
	var advanced string
	p.OnAdvance = func(title string) { advanced = title }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go p.Run(ctx)
	time.Sleep(1800 * time.Millisecond)

	st := p.State()
	if st.Path != second {
		t.Errorf("playing %q at the end of the first file; want it to advance to the second", st.Path)
	}
	if st.Paused {
		t.Error("paused after advancing; the next item should just play")
	}
	if advanced == "" {
		t.Error("OnAdvance was not called, so the room is never told what started")
	}
	if cap.tracks != 1 {
		t.Errorf("PublishTracks called %d times across the advance, want 1", cap.tracks)
	}
}

func TestRejectsUnknownLoadMode(t *testing.T) {
	p, _ := newPlayer(t)
	if err := p.Load(build(t, 1), "sideways"); err == nil {
		t.Error("an unknown load mode should be reported, not silently treated as replace")
	}
}

func TestAppendRejectsUnplayableFile(t *testing.T) {
	p, _ := newPlayer(t)
	if err := p.Load(build(t, 1), "replace"); err != nil {
		t.Fatal(err)
	}
	// A bad file must fail now, while the host is looking at the Queue tab,
	// not twenty minutes later when the current episode ends.
	bad := filepath.Join(t.TempDir(), "broken.vsm")
	if err := os.WriteFile(bad, []byte("nope"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := p.Load(bad, "append"); err == nil {
		t.Error("appending an unplayable file was accepted")
	}
	if q := p.Playlist(); len(q) != 0 {
		t.Errorf("playlist = %v, want the bad file rejected", q)
	}
}
