// Package houseplayer streams a pre-encoded .vsm into a LiveKit room.
//
// This is the server-side half of the "house projector": no mpv, no ffmpeg,
// no decoding and no encoding, because the Ampere box cannot encode 1080p in
// real time (0.35x at x264 veryfast). Everything expensive happened in vspush
// on a machine with a hardware encoder; here we only read records, wait until
// each one is due, and hand it to the packetizer.
//
// The clock is the wall clock, anchored at the moment playback starts. The
// desktop projector derives its timeline from mpv's audio consumption because
// mpv is a live decoder that can fall behind; a file cannot, so there is
// nothing to follow and a monotonic timer is both simpler and exact.
// Timestamps stay absolute, computed from the frame index and sample counter,
// so they never accumulate error and never go backwards across a seek.
package houseplayer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/ShadowElf37/VideoStream/projector/internal/vsm"
)

// Sink is what a player publishes through: the projector's Publisher
// satisfies it.
type Sink interface {
	WriteVideo(au []byte, ts90k uint32) error
	WriteAudio(payload []byte, ts48k uint32) error
	PublishTracks(width, height int) error
}

// Player streams one file at a time.
type Player struct {
	log  *slog.Logger
	sink Sink

	mu         sync.Mutex
	cur        *vsm.Reader
	hdr        vsm.Header
	path       string
	paused     bool
	stopped    bool
	posMS      int64
	seekTo     int64 // -1 when no seek is pending
	generation int

	wake chan struct{}
}

// New creates an idle player.
func New(log *slog.Logger, sink Sink) *Player {
	return &Player{log: log, sink: sink, seekTo: -1, wake: make(chan struct{}, 1)}
}

// State is the snapshot the controller turns into proto.MpvState.
type State struct {
	Idle    bool
	Paused  bool
	PosMS   int64
	Header  vsm.Header
	Path    string
	Playing bool
}

// State returns what is loaded and where it is.
func (p *Player) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return State{
		Idle:    p.cur == nil,
		Paused:  p.paused,
		PosMS:   p.posMS,
		Header:  p.hdr,
		Path:    p.path,
		Playing: p.cur != nil && !p.paused,
	}
}

// Load opens path and starts playing it from the beginning, replacing
// whatever was playing.
func (p *Player) Load(path string) error {
	r, err := vsm.Open(path)
	if err != nil {
		return err
	}
	hdr := r.Header()
	if err := p.sink.PublishTracks(hdr.Width, hdr.Height); err != nil {
		r.Close()
		return fmt.Errorf("publishing tracks: %w", err)
	}
	p.mu.Lock()
	if p.cur != nil {
		p.cur.Close()
	}
	p.cur, p.hdr, p.path = r, hdr, path
	p.paused, p.posMS, p.seekTo = false, 0, -1
	p.generation++
	p.mu.Unlock()
	p.nudge()
	p.log.Info("house: loaded", "title", hdr.Title, "path", path,
		"size", fmt.Sprintf("%dx%d", hdr.Width, hdr.Height),
		"duration", time.Duration(hdr.DurationMS)*time.Millisecond)
	return nil
}

// SetPaused pauses or resumes. Paused playback keeps the tracks published and
// simply stops feeding them, so viewers keep the last picture and the
// subscription never tears down.
func (p *Player) SetPaused(v bool) {
	p.mu.Lock()
	changed := p.paused != v
	p.paused = v
	p.mu.Unlock()
	if changed {
		p.nudge()
	}
}

// TogglePause flips the pause state and reports the new value.
func (p *Player) TogglePause() bool {
	p.mu.Lock()
	p.paused = !p.paused
	v := p.paused
	p.mu.Unlock()
	p.nudge()
	return v
}

// SeekMS requests an absolute seek. It lands on the keyframe at or before the
// target, which is at most one GOP (2 s) earlier.
func (p *Player) SeekMS(ms int64) {
	if ms < 0 {
		ms = 0
	}
	p.mu.Lock()
	p.seekTo = ms
	p.mu.Unlock()
	p.nudge()
}

// Stop unloads the current file.
func (p *Player) Stop() {
	p.mu.Lock()
	if p.cur != nil {
		p.cur.Close()
		p.cur = nil
	}
	p.path, p.posMS = "", 0
	p.generation++
	p.mu.Unlock()
	p.nudge()
}

func (p *Player) nudge() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

// Run drives playback until ctx is done. It is the only goroutine that touches
// the reader after Load, so records are read in order without further locking.
func (p *Player) Run(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		if !p.playOne(ctx) {
			// Nothing to do: wait for a load, a resume or a seek.
			select {
			case <-ctx.Done():
				return
			case <-p.wake:
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
}

// playOne emits records until the state changes under it. It returns false
// when there is nothing to play, so Run can idle instead of spinning.
func (p *Player) playOne(ctx context.Context) bool {
	p.mu.Lock()
	r, hdr, gen := p.cur, p.hdr, p.generation
	paused := p.paused
	p.mu.Unlock()
	if r == nil || paused {
		return false
	}

	// Anchor the clock to now, at whatever position we are resuming from. Any
	// pause, seek or load re-enters here and re-anchors, so wall time only
	// ever maps onto the media while it is actually moving.
	startPos := p.currentPosMS()
	epoch := time.Now()
	frameNS := hdr.FrameDurationNS()

	for {
		if ctx.Err() != nil {
			return true
		}
		// A seek or a state change invalidates the anchor.
		p.mu.Lock()
		seek, changed := p.seekTo, p.generation != gen || p.paused
		if seek >= 0 {
			p.seekTo = -1
		}
		p.mu.Unlock()
		if changed {
			return true
		}
		if seek >= 0 {
			k, err := r.SeekMS(seek)
			if err != nil {
				p.log.Warn("house: seek failed", "err", err)
				return true
			}
			p.setPos(k.MS)
			startPos = k.MS
			epoch = time.Now()
			p.log.Info("house: seeked", "to", time.Duration(seek)*time.Millisecond,
				"landed", time.Duration(k.MS)*time.Millisecond)
			continue
		}

		rec, err := r.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				p.log.Info("house: end of file", "title", hdr.Title)
			} else {
				p.log.Warn("house: read failed", "err", err)
			}
			p.mu.Lock()
			if p.generation == gen && p.cur == r {
				p.paused = true // hold on the last frame rather than unpublishing
			}
			p.mu.Unlock()
			return true
		}

		_, samples := r.Position()
		var dueMS int64
		switch rec.Kind {
		case vsm.KindVideo:
			dueMS = int64(rec.Frame) * frameNS / 1e6
		case vsm.KindAudio:
			// samples already includes this packet, so back it out to get the
			// packet's own start time.
			dueMS = (samples - int64(rec.Samples)) * 1000 / int64(audioRate(hdr))
		}
		if wait := time.Until(epoch.Add(time.Duration(dueMS-startPos) * time.Millisecond)); wait > 0 {
			select {
			case <-ctx.Done():
				return true
			case <-time.After(wait):
			case <-p.wake:
				// A control change arrived. Publish this record anyway — it is
				// due, and the loop top handles the change on the next pass —
				// rather than discarding a record the reader has consumed.
			}
		}

		switch rec.Kind {
		case vsm.KindVideo:
			ts := uint32(int64(rec.Frame) * 90000 * int64(hdr.FPSDen) / int64(hdr.FPSNum))
			if err := p.sink.WriteVideo(rec.Data, ts); err != nil {
				p.log.Warn("house: video write failed", "err", err)
			}
			p.setPos(dueMS)
		case vsm.KindAudio:
			ts := uint32(samples - int64(rec.Samples))
			if err := p.sink.WriteAudio(rec.Data, ts); err != nil {
				p.log.Warn("house: audio write failed", "err", err)
			}
		}
	}
}

func audioRate(h vsm.Header) int {
	if h.AudioRate > 0 {
		return h.AudioRate
	}
	return 48000
}

func (p *Player) setPos(ms int64) {
	p.mu.Lock()
	p.posMS = ms
	p.mu.Unlock()
}

func (p *Player) currentPosMS() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.posMS
}
