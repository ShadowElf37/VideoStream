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
	QueueDepth   int
	Chunks       int64
	Silence      int64
	LateFrames   int64
	AudioDelayMs float64
	ReadBytes    int64
	Flushes      int64
	Stalls       int64
}

// Timeline owns the media clock. One goroutine reads the FIFO, one emits
// chunks on a 20 ms cadence; video stamping is serialised by a mutex because
// both the frame path and the encoder's pacing tick use it.
type Timeline struct {
	log   *slog.Logger
	epoch time.Time

	q        chan []int16
	flushReq chan struct{}
	fifo     string

	sink atomic.Pointer[func(AudioFrame)]

	em      *Emitter
	delayNs atomic.Int64
	chunks  atomic.Int64
	silence atomic.Int64
	read    atomic.Int64
	flushes atomic.Int64
	stalls  atomic.Int64

	vmu sync.Mutex
	vs  *VideoStamper

	silenceBuf []int16
	wg         sync.WaitGroup
}

// New creates a timeline that drains the FIFO at path. The FIFO must already
// exist (the caller makes it, and passes the same path to mpv's ao-pcm-file).
func New(log *slog.Logger, fifoPath string, epoch time.Time) *Timeline {
	return &Timeline{
		log:        log,
		epoch:      epoch,
		fifo:       fifoPath,
		q:          make(chan []int16, 100), // 2 s of audio
		flushReq:   make(chan struct{}, 4),
		em:         NewEmitter(0.05),
		vs:         NewVideoStamper(epoch),
		silenceBuf: make([]int16, ChunkFrames),
	}
}

// Epoch is the monotonic anchor of both RTP timelines.
func (t *Timeline) Epoch() time.Time { return t.epoch }

// SetSink installs the consumer of emitted audio chunks. It is called from the
// emitter goroutine, once per 20 ms, and must not block for long.
func (t *Timeline) SetSink(fn func(AudioFrame)) { t.sink.Store(&fn) }

// Start launches the reader and the emitter. They stop when ctx is cancelled.
func (t *Timeline) Start(ctx context.Context) error {
	rd, err := openFIFO(t.fifo)
	if err != nil {
		return err
	}
	t.wg.Add(2)
	go func() {
		defer t.wg.Done()
		defer rd.Close()
		t.readLoop(ctx, rd)
	}()
	go func() {
		defer t.wg.Done()
		t.emitLoop(ctx)
	}()
	return nil
}

// Wait blocks until both goroutines have exited.
func (t *Timeline) Wait() { t.wg.Wait() }

// Flush discards everything queued and everything still sitting in the FIFO.
// Called on seek so pre-seek audio never reaches the viewers.
func (t *Timeline) Flush() {
	select {
	case t.flushReq <- struct{}{}:
	default:
	}
}

// StampVideo maps a frame's presentation wall time onto the 90 kHz timeline.
func (t *Timeline) StampVideo(wall time.Time) uint32 {
	t.vmu.Lock()
	defer t.vmu.Unlock()
	return t.vs.Stamp(wall, time.Duration(t.delayNs.Load()))
}

// AudioDelay is the smoothed FIFO queue depth as a duration.
func (t *Timeline) AudioDelay() time.Duration { return time.Duration(t.delayNs.Load()) }

// Stats returns a snapshot for the status line and mpv.state.
func (t *Timeline) Stats() Stats {
	t.vmu.Lock()
	late := t.vs.Late()
	t.vmu.Unlock()
	return Stats{
		QueueDepth:   len(t.q),
		Chunks:       t.chunks.Load(),
		Silence:      t.silence.Load(),
		LateFrames:   late,
		AudioDelayMs: float64(t.delayNs.Load()) / float64(time.Millisecond),
		ReadBytes:    t.read.Load(),
		Flushes:      t.flushes.Load(),
		Stalls:       t.stalls.Load(),
	}
}

// emitLoop ticks every 20 ms relative to the epoch and emits exactly one chunk
// per tick. If we wake up late it catches up by emitting the missed indices
// back to back, so an index (and therefore an RTP timestamp) is never skipped.
func (t *Timeline) emitLoop(ctx context.Context) {
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

		var pcm []int16
		select {
		case pcm = <-t.q:
		default:
		}
		step := t.em.Step(pcm != nil, len(t.q))
		t.delayNs.Store(int64(t.em.AudioDelay()))
		t.chunks.Add(1)
		if step.Silence {
			t.silence.Add(1)
			pcm = t.silenceBuf
		}
		if p := t.sink.Load(); p != nil {
			(*p)(AudioFrame{PCM: pcm, TS48k: step.TS48k, Index: step.Index, Silence: step.Silence})
		}
	}
}

// drainQueue empties the chunk queue; called on the reader goroutine so the
// producer side is quiet while it happens.
func (t *Timeline) drainQueue() int {
	n := 0
	for {
		select {
		case <-t.q:
			n++
		default:
			return n
		}
	}
}
