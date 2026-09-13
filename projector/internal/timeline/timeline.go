package timeline

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// AudioFrame is one 20 ms chunk handed to the sink. PCM is only valid for the
// duration of the call; the sink must encode or copy it before returning.
type AudioFrame struct {
	PCM     []int16
	TS48k   uint32
	Index   int64
	Silence bool
}

// Stats is a snapshot of the clock's health.
type Stats struct {
	FillBytes  int
	FillMs     float64
	Chunks     int64
	Silence    int64
	LateFrames int64
	ReadBytes  int64
	Flushes    int64
	Stalls     int64
}

// Timeline owns the media clock. A single goroutine drives it: at every 20 ms
// tick of an absolute schedule it reads exactly one chunk from the FIFO (or
// emits silence) and never reads ahead, so mpv stays back-pressured onto our
// clock. Video stamping is serialised by a mutex because both the frame path
// and the encoder's pacing tick use it.
type Timeline struct {
	log   *slog.Logger
	epoch time.Time

	flushReq chan struct{}
	fifo     string

	sink atomic.Pointer[func(AudioFrame)]

	em     *Emitter
	leadNs atomic.Int64
	fill   atomic.Int64

	chunks  atomic.Int64
	silence atomic.Int64
	read    atomic.Int64
	flushes atomic.Int64
	stalls  atomic.Int64

	vmu sync.Mutex
	vs  *VideoStamper

	pcmBuf     []int16
	silenceBuf []int16
	wg         sync.WaitGroup
}

// New creates a timeline that paces reads from the FIFO at path. The FIFO must
// already exist (the caller makes it, and passes the same path to mpv's
// ao-pcm-file).
func New(log *slog.Logger, fifoPath string, epoch time.Time) *Timeline {
	return &Timeline{
		log:        log,
		epoch:      epoch,
		fifo:       fifoPath,
		flushReq:   make(chan struct{}, 4),
		em:         NewEmitter(0.05),
		vs:         NewVideoStamper(epoch),
		pcmBuf:     make([]int16, ChunkFrames),
		silenceBuf: make([]int16, ChunkFrames),
	}
}

// Epoch is the monotonic anchor of both RTP timelines.
func (t *Timeline) Epoch() time.Time { return t.epoch }

// SetSink installs the consumer of emitted audio chunks. It is called from the
// clock goroutine, once per 20 ms, and must not block for long.
func (t *Timeline) SetSink(fn func(AudioFrame)) { t.sink.Store(&fn) }

// Start opens the FIFO and launches the clock. It stops when ctx is cancelled.
func (t *Timeline) Start(ctx context.Context) error {
	f, err := openFIFO(t.fifo)
	if err != nil {
		return err
	}
	t.wg.Add(1)
	go func() {
		defer t.wg.Done()
		defer f.Close()
		t.run(ctx, f)
	}()
	return nil
}

// Wait blocks until the clock goroutine has exited.
func (t *Timeline) Wait() { t.wg.Wait() }

// Flush discards everything still sitting in the FIFO. Called on seek so
// pre-seek audio never reaches the viewers.
func (t *Timeline) Flush() {
	select {
	case t.flushReq <- struct{}{}:
	default:
	}
}

// StampVideo maps a frame's presentation wall time onto the 90 kHz timeline,
// shifted forward by the current audio lead.
func (t *Timeline) StampVideo(wall time.Time) uint32 {
	t.vmu.Lock()
	defer t.vmu.Unlock()
	return t.vs.Stamp(wall, time.Duration(t.leadNs.Load()))
}

// Lead is the smoothed FIFO fill as a duration.
func (t *Timeline) Lead() time.Duration { return time.Duration(t.leadNs.Load()) }

// Stats returns a snapshot for the status line and mpv.state.
func (t *Timeline) Stats() Stats {
	t.vmu.Lock()
	late := t.vs.Late()
	t.vmu.Unlock()
	return Stats{
		FillBytes:  int(t.fill.Load()),
		FillMs:     float64(t.leadNs.Load()) / float64(time.Millisecond),
		Chunks:     t.chunks.Load(),
		Silence:    t.silence.Load(),
		LateFrames: late,
		ReadBytes:  t.read.Load(),
		Flushes:    t.flushes.Load(),
		Stalls:     t.stalls.Load(),
	}
}

// run is the clock. It ticks every 20 ms relative to the epoch and emits
// exactly one chunk per tick. If we wake up late it catches up by emitting the
// missed indices back to back, so a timestamp is never skipped.
func (t *Timeline) run(ctx context.Context, f *fifo) {
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()
	for {
		next := t.epoch.Add(time.Duration(t.em.Index()+1) * ChunkDur)
		if d := time.Until(next); d > 0 {
			timer.Reset(d)
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		} else if d < -200*time.Millisecond {
			t.stalls.Add(1)
		}
		select {
		case <-ctx.Done():
			return
		default:
		}

		select {
		case <-t.flushReq:
			n := f.discard()
			t.flushes.Add(1)
			t.em.Reset()
			t.log.Debug("timeline: flushed on seek", "fifoBytes", n)
		default:
		}

		// Exactly one chunk per tick, never more: this is the back-pressure
		// that keeps mpv on our clock.
		pcm := t.silenceBuf
		have := f.readChunk(t.pcmBuf)
		if have {
			pcm = t.pcmBuf
			t.read.Add(ChunkBytes)
		}
		fill := f.fill()
		t.fill.Store(int64(fill))
		step := t.em.Step(have, fill)
		t.leadNs.Store(int64(t.em.Lead()))
		t.chunks.Add(1)
		if step.Silence {
			t.silence.Add(1)
		}
		if p := t.sink.Load(); p != nil {
			(*p)(AudioFrame{PCM: pcm, TS48k: step.TS48k, Index: step.Index, Silence: step.Silence})
		}
	}
}
