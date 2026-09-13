// Package timeline is the projector's media clock.
//
// mpv writes s16 PCM into a FIFO; we read it back on an absolute 20 ms
// schedule anchored to a monotonic epoch — exactly one 960-sample chunk per
// tick, never reading ahead. Because mpv's ao_pcm blocks once the pipe fills,
// those paced reads are what slave mpv to *our* clock: draining as fast as data
// arrives instead lets mpv free-run about 0.2 % fast and the lead grows without
// bound. If a whole chunk is not there at a tick (pause, seek, idle) we emit
// silence, so audio RTP timestamps are chunkIndex*960 and can never skip.
//
// The audio lead is then simply how much mpv has written that we have not
// consumed: the FIFO fill, read with FIONREAD at each tick. Video frames are
// stamped from their render wall time shifted forward by that (smoothed) lead,
// which is what lines the two tracks up.
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
	// BytesPerSecond is the s16 stereo PCM byte rate, used to convert a FIFO
	// fill in bytes into an audio lead in time.
	BytesPerSecond = SampleRate * Channels * 2
)

// LeadOf converts a FIFO fill in bytes into the audio lead it represents.
func LeadOf(fillBytes float64) time.Duration {
	if fillBytes <= 0 {
		return 0
	}
	return time.Duration(fillBytes / BytesPerSecond * float64(time.Second))
}

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
// and keeps a smoothed estimate of the FIFO fill (the audio lead).
// It is not safe for concurrent use; one goroutine owns it.
type Emitter struct {
	index   int64
	fill    float64
	alpha   float64
	silence int64
	emitted int64
}

// NewEmitter returns an emitter whose fill estimate uses the given smoothing
// factor (0 < alpha <= 1); alpha <= 0 selects a sensible default.
func NewEmitter(alpha float64) *Emitter {
	if alpha <= 0 || alpha > 1 {
		alpha = 0.05
	}
	return &Emitter{alpha: alpha}
}

// Step emits the next chunk. haveChunk says whether a whole chunk was read from
// the FIFO; fillBytes is what FIONREAD reported after that read.
func (e *Emitter) Step(haveChunk bool, fillBytes int) Step {
	e.fill += e.alpha * (float64(fillBytes) - e.fill)
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

// Fill is the smoothed FIFO fill in bytes.
func (e *Emitter) Fill() float64 { return e.fill }

// Lead is the smoothed fill expressed as a duration: how far ahead of what the
// viewer is hearing mpv has already produced, and therefore how far forward a
// frame rendered now has to be placed on the media timeline.
func (e *Emitter) Lead() time.Duration { return LeadOf(e.fill) }

// Reset clears the fill estimate (used after a seek drain) without touching the
// chunk index, which must never go backwards.
func (e *Emitter) Reset() { e.fill = 0 }

// VideoTicks maps a wall-clock instant onto the 90 kHz media timeline, shifted
// forward by the current audio lead.
func VideoTicks(epoch, wall time.Time, lead time.Duration) int64 {
	d := wall.Sub(epoch) + lead
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
func (v *VideoStamper) Stamp(wall time.Time, lead time.Duration) uint32 {
	t := VideoTicks(v.epoch, wall, lead)
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
