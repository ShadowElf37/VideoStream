package timeline

import (
	"math"
	"testing"
	"time"
)

func TestAudioTSNeverSkipsOrDerivesFromWallTime(t *testing.T) {
	e := NewEmitter(0.5)
	// Alternate between "a chunk is ready" and "nothing arrived": the index and
	// therefore the timestamp must advance by exactly 960 every single tick.
	var prev uint32
	for i := range 1000 {
		st := e.Step(i%3 != 0, 0)
		if st.Index != int64(i) {
			t.Fatalf("tick %d: index = %d", i, st.Index)
		}
		if want := uint32(i * ChunkSamples); st.TS48k != want {
			t.Fatalf("tick %d: ts = %d, want %d", i, st.TS48k, want)
		}
		if i > 0 && st.TS48k-prev != ChunkSamples {
			t.Fatalf("tick %d: ts step = %d", i, st.TS48k-prev)
		}
		prev = st.TS48k
	}
	if got := e.SilenceCount(); got != 334 {
		t.Fatalf("silence = %d, want 334", got)
	}
	if e.Emitted() != 1000 {
		t.Fatalf("emitted = %d", e.Emitted())
	}
}

func TestAudioTSWrapsCleanly(t *testing.T) {
	// 2^32 / 960 is not an integer, so the wrap must come from uint32
	// truncation of index*960, not from a modulo of the index.
	idx := int64(4_473_925) // just past 2^32/960
	if got, want := AudioTS48k(idx), uint32(uint64(idx)*ChunkSamples); got != want {
		t.Fatalf("ts = %d, want %d", got, want)
	}
	if AudioTS48k(idx) >= AudioTS48k(idx-1) {
		t.Fatalf("expected wrap around index %d", idx)
	}
}

func TestEmitterDepthSmoothing(t *testing.T) {
	e := NewEmitter(0.5)
	for range 30 {
		e.Step(true, 4)
	}
	if d := e.Depth(); math.Abs(d-4) > 0.01 {
		t.Fatalf("depth = %v, want ~4", d)
	}
	if got, want := e.AudioDelay(), 4*ChunkDur; absDur(got-want) > 2*time.Millisecond {
		t.Fatalf("audio delay = %v, want ~%v", got, want)
	}
	e.Reset()
	if e.Depth() != 0 {
		t.Fatalf("reset did not clear depth")
	}
	if e.Index() != 30 {
		t.Fatalf("reset must not rewind the index, got %d", e.Index())
	}
}

func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func TestVideoTicks(t *testing.T) {
	epoch := time.Unix(1000, 0)
	cases := []struct {
		off   time.Duration
		delay time.Duration
		want  int64
	}{
		{0, 0, 0},
		{time.Second, 0, 90000},
		{time.Second, 40 * time.Millisecond, 93600},
		{-time.Second, 0, 0}, // before the epoch clamps to zero
		{41708 * time.Microsecond, 0, 3754},
	}
	for _, c := range cases {
		got := VideoTicks(epoch, epoch.Add(c.off), c.delay)
		if got != c.want {
			t.Fatalf("VideoTicks(%v,%v) = %d, want %d", c.off, c.delay, got, c.want)
		}
	}
}

func TestVideoStamperIsMonotonic(t *testing.T) {
	epoch := time.Unix(2000, 0)
	v := NewVideoStamper(epoch)
	first := v.Stamp(epoch.Add(time.Second), 0)
	if first != 90000 {
		t.Fatalf("first = %d", first)
	}
	// A frame that arrives out of order (or a seek that rewinds our estimate)
	// must not produce a backwards timestamp.
	back := v.Stamp(epoch.Add(500*time.Millisecond), 0)
	if back != first {
		t.Fatalf("backwards stamp = %d, want clamped to %d", back, first)
	}
	if v.Late() != 1 {
		t.Fatalf("late = %d, want 1", v.Late())
	}
	fwd := v.Stamp(epoch.Add(2*time.Second), 0)
	if fwd != 180000 {
		t.Fatalf("fwd = %d", fwd)
	}
	// A long pause (no frames for 30 s) still stamps forward, leaving a gap.
	gap := v.Stamp(epoch.Add(32*time.Second), 0)
	if gap != 32*90000 {
		t.Fatalf("gap = %d", gap)
	}
}

func TestStableRate(t *testing.T) {
	cases := []struct {
		in       float64
		num, den int
	}{
		{23.976023976, 24000, 1001},
		{23.98, 24000, 1001},
		{24.0, 24, 1},
		{25.0, 25, 1},
		{29.97, 30000, 1001},
		{30.0, 30, 1},
		{59.94, 60000, 1001},
		{60.0, 60, 1},
		{0, 24000, 1001},
		{math.NaN(), 24000, 1001},
		{15.0, 15, 1},
	}
	for _, c := range cases {
		n, d := StableRate(c.in)
		if n != c.num || d != c.den {
			t.Fatalf("StableRate(%v) = %d/%d, want %d/%d", c.in, n, d, c.num, c.den)
		}
	}
}
