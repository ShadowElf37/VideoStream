// Package timeline is the projector's media clock.
//
// mpv is the master clock: it writes s16 PCM into a FIFO paced by its own
// real-time clock, so the arrival of audio defines the media timeline. The
// emitter runs on a 20 ms wall-clock cadence anchored to a monotonic epoch and
// emits exactly one chunk per tick — a real one if the reader has queued any,
// otherwise silence — so audio RTP timestamps are chunkIndex*960 and can never
// skip or drift. Video frames are stamped from their render wall time, shifted
// forward by the smoothed audio queue depth so the two tracks line up.
//
// This file holds the pure math; it has no dependency on mpv, ffmpeg or the OS.
package timeline

import (
	"math"
	"time"
)

const (
	// SampleRate is the PCM/Opus sample rate we force mpv to produce.
	SampleRate = 48000
	// Channels is the channel count (stereo).
	Channels = 2
	// ChunkSamples is the number of samples per channel in one 20 ms chunk.
	ChunkSamples = 960
	// ChunkFrames is the number of interleaved int16 values in one chunk.
	ChunkFrames = ChunkSamples * Channels
	// ChunkBytes is the size of one chunk as s16le bytes.
	ChunkBytes = ChunkFrames * 2
	// ChunkDur is the wall-clock duration of one chunk.
	ChunkDur = time.Duration(ChunkSamples) * time.Second / SampleRate
	// VideoClock is the RTP clock rate of the video track.
	VideoClock = 90000
)

// AudioTS48k returns the RTP timestamp of the chunk with the given index.
// It is derived only from the index, never from wall time.
func AudioTS48k(index int64) uint32 { return uint32(uint64(index) * ChunkSamples) }

// Step is one decision of the audio emitter.
type Step struct {
	Index   int64
	TS48k   uint32
	Silence bool
}

// Emitter is the audio side of the clock: it hands out one Step per 20 ms tick
// and keeps a smoothed estimate of how many chunks are waiting in the queue.
// It is not safe for concurrent use; one goroutine owns it.
type Emitter struct {
	index   int64
	depth   float64
	alpha   float64
	silence int64
	emitted int64
}

// NewEmitter returns an emitter whose queue-depth estimate uses the given
// smoothing factor (0 < alpha <= 1); alpha <= 0 selects a sensible default.
func NewEmitter(alpha float64) *Emitter {
	if alpha <= 0 || alpha > 1 {
		alpha = 0.05
	}
	return &Emitter{alpha: alpha}
}

// Step emits the next chunk. haveChunk says whether a real chunk was taken off
// the queue; queueDepth is how many chunks remain queued after that take.
func (e *Emitter) Step(haveChunk bool, queueDepth int) Step {
	e.depth += e.alpha * (float64(queueDepth) - e.depth)
	s := Step{Index: e.index, TS48k: AudioTS48k(e.index), Silence: !haveChunk}
	e.index++
	e.emitted++
	if s.Silence {
		e.silence++
	}
	return s
}

// Index is the index the next Step will use.
func (e *Emitter) Index() int64 { return e.index }

// Emitted is the total number of chunks emitted.
func (e *Emitter) Emitted() int64 { return e.emitted }

// SilenceCount is how many of them were synthesised silence.
func (e *Emitter) SilenceCount() int64 { return e.silence }

// Depth is the smoothed queue depth in chunks.
func (e *Emitter) Depth() float64 { return e.depth }

// AudioDelay is the smoothed queue depth expressed as a duration: how far
// behind the wall clock the audio the viewer is hearing actually is.
func (e *Emitter) AudioDelay() time.Duration {
	return time.Duration(e.depth * float64(ChunkDur))
}

// Reset clears the queue-depth estimate (used after a seek drain) without
// touching the chunk index, which must never go backwards.
func (e *Emitter) Reset() { e.depth = 0 }

// VideoTicks maps a wall-clock instant onto the 90 kHz media timeline.
func VideoTicks(epoch, wall time.Time, audioDelay time.Duration) int64 {
	d := wall.Sub(epoch) + audioDelay
	if d < 0 {
		d = 0
	}
	return int64(math.Round(float64(d) / float64(time.Second) * VideoClock))
}

// VideoStamper turns render wall times into monotonic non-decreasing RTP
// timestamps. It is not safe for concurrent use; callers serialise access.
type VideoStamper struct {
	epoch time.Time
	last  int64
	has   bool
	late  int64
}

// NewVideoStamper anchors a stamper at epoch.
func NewVideoStamper(epoch time.Time) *VideoStamper {
	return &VideoStamper{epoch: epoch}
}

// Stamp returns the RTP timestamp for a frame presented at wall.
func (v *VideoStamper) Stamp(wall time.Time, audioDelay time.Duration) uint32 {
	t := VideoTicks(v.epoch, wall, audioDelay)
	if v.has && t < v.last {
		v.late++
		t = v.last
	}
	v.last = t
	v.has = true
	return uint32(uint64(t))
}

// Late counts frames whose computed timestamp had to be clamped forward.
func (v *VideoStamper) Late() int64 { return v.late }

// StableRate rounds a reported frame rate to the nearest well-known rational so
// the encoder is not reconfigured for measurement noise. Unknown rates fall
// back to the nearest of the standard set.
func StableRate(fps float64) (num, den int) {
	type rate struct {
		num, den int
	}
	cands := []rate{
		{24000, 1001}, {24, 1}, {25, 1}, {30000, 1001}, {30, 1},
		{48, 1}, {50, 1}, {60000, 1001}, {60, 1}, {120, 1},
	}
	if !(fps > 0) || math.IsInf(fps, 0) || math.IsNaN(fps) {
		return 24000, 1001
	}
	best, bestErr := cands[0], math.Inf(1)
	for _, c := range cands {
		e := math.Abs(float64(c.num)/float64(c.den) - fps)
		if e < bestErr {
			best, bestErr = c, e
		}
	}
	// Far outside the known set (e.g. a weird 15 fps webcam source): use the
	// rounded integer rate rather than a wildly wrong standard one.
	if bestErr > 0.5 {
		return int(math.Round(fps)), 1
	}
	return best.num, best.den
}
