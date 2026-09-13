// Package houseplayer streams a pre-encoded .vsm into a LiveKit room.
//
// This is the server-side half of the "house projector": no mpv, no ffmpeg,
// no decoding and no encoding, because the Ampere box cannot encode 1080p in
// real time (0.35x at x264 veryfast). Everything expensive happened in vspush
// on a machine with a hardware encoder; here we only read records, wait until
// each one is due, and hand it to the packetizer.
//
// There are two clocks, and keeping them apart is the whole trick.
//
// Pacing runs off the wall clock against the file's own timeline: each record
// is due at its position in the file, so playback tracks real time and a seek
// simply re-anchors. The desktop projector instead follows mpv's audio
// consumption, because mpv is a live decoder that can fall behind; a file
// cannot, so there is nothing to follow.
//
// RTP timestamps run off a separate counter of what has actually been sent,
// which only ever moves forward. Pause, seek and changing file are gaps in it,
// never rewinds. Deriving them from the file position instead — the obvious
// thing, since the file says exactly when each frame belongs — is wrong:
// seeking backwards then re-sends timestamps the receiver has already seen and
// it drops everything until the stream catches up.
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

	// queue is the playlist: what plays when the current file ends.
	queue []string

	// Size the tracks were published at. PublishTracks creates fresh tracks
	// every call, so calling it per file would tear the viewer's subscription
	// down between episodes; only a genuine resolution change needs it.
	pubW, pubH int

	// outSamples is the output clock: 48 kHz samples actually put on the wire.
	//
	// RTP timestamps come from here and not from the file position, because
	// they must only ever move forward. Stamping from the file meant seeking
	// backwards sent timestamps a receiver had already seen, and the decoder
	// discarded everything until the stream caught back up — a seek to the
	// start 4 s in froze the picture for 4 s. Pause, seek and switching files
	// are all just gaps in this clock.
	//
	// Only the Run goroutine touches it, and nothing ever resets it.
	outSamples int64

	// lastVideoNS is the output time of the most recent video frame, carried
	// across segments so a re-anchor can never place the next frame behind it.
	lastVideoNS int64

	// OnAdvance is called when the playlist moves on by itself, so the
	// controller can tell the room what is playing now.
	OnAdvance func(title string)

	// OnSeeked reports where a seek actually landed. The controller cannot
	// work this out itself: seeks are asynchronous and land on the keyframe
	// at or before the target, so announcing the requested position was
	// reporting the pre-seek one instead.
	OnSeeked func(posMS int64)

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

// Load handles the three mpv load modes the Queue tab uses.
//
//	replace      play it now, discarding the playlist
//	append       queue it behind whatever is playing
//	append-play  queue it and jump to it
//
// The mode is not decoration: "append" is the add-to-playlist button, and
// treating it as "replace" cuts the film off mid-scene.
func (p *Player) Load(path, mode string) error {
	switch mode {
	case "", "replace":
		p.mu.Lock()
		p.queue = nil
		p.mu.Unlock()
		return p.playFile(path)

	case "append":
		// Validate now rather than at the end of the current episode: a
		// corrupt or missing file should be reported while the host is still
		// looking at the Queue tab.
		if err := checkPlayable(path); err != nil {
			return err
		}
		p.mu.Lock()
		idle := p.cur == nil
		p.queue = append(p.queue, path)
		n := len(p.queue)
		p.mu.Unlock()
		if idle {
			return p.playNext()
		}
		p.log.Info("house: queued", "path", path, "queued", n)
		return nil

	case "append-play":
		return p.playFile(path)
	}
	return fmt.Errorf("unknown load mode %q", mode)
}

// checkPlayable confirms a file parses as a .vsm without disturbing playback.
func checkPlayable(path string) error {
	r, err := vsm.Open(path)
	if err != nil {
		return err
	}
	return r.Close()
}

// Playlist returns what is queued behind the current file.
func (p *Player) Playlist() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.queue...)
}

// playNext starts the head of the queue. It reports false when the queue is
// empty.
func (p *Player) playNext() error {
	p.mu.Lock()
	if len(p.queue) == 0 {
		p.mu.Unlock()
		return nil
	}
	next := p.queue[0]
	p.queue = p.queue[1:]
	p.mu.Unlock()
	return p.playFile(next)
}

// playFile switches to path immediately, leaving the queue alone.
func (p *Player) playFile(path string) error {
	r, err := vsm.Open(path)
	if err != nil {
		return err
	}
	hdr := r.Header()
	// Only (re)publish when the geometry actually changes. Republishing
	// per file would drop every viewer's subscription between episodes.
	p.mu.Lock()
	needPublish := hdr.Width != p.pubW || hdr.Height != p.pubH
	p.mu.Unlock()
	if needPublish {
		if err := p.sink.PublishTracks(hdr.Width, hdr.Height); err != nil {
			r.Close()
			return fmt.Errorf("publishing tracks: %w", err)
		}
	}
	p.mu.Lock()
	if p.cur != nil {
		p.cur.Close()
	}
	p.cur, p.hdr, p.path = r, hdr, path
	p.paused, p.posMS, p.seekTo = false, 0, -1
	p.pubW, p.pubH = hdr.Width, hdr.Height
	p.generation++
	p.mu.Unlock()
	p.nudge()
	p.log.Info("house: playing", "title", hdr.Title, "path", path,
		"size", fmt.Sprintf("%dx%d", hdr.Width, hdr.Height),
		"duration", time.Duration(hdr.DurationMS)*time.Millisecond,
		"republished", needPublish)
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
	p.queue = nil
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
	// Video timing is exact within a segment and monotonic across them.
	//
	// segFileNS is where this segment starts in the file; outAnchorNS is the
	// output time that maps to. A frame's output time is then simply the
	// anchor plus its distance into the segment, which keeps the spacing of
	// the source (a steady 3750 ticks at 24 fps) instead of snapping every
	// frame to the nearest 20 ms audio packet.
	//
	// The anchor never goes below the last frame already sent, so no seek or
	// file change can walk the clock backwards.
	segFileNS := startPos * 1e6
	outAnchorNS := p.outSamples * 1e9 / 48000
	if p.lastVideoNS > outAnchorNS {
		outAnchorNS = p.lastVideoNS
	}

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
			segFileNS = k.MS * 1e6
			outAnchorNS = p.outSamples * 1e9 / 48000
			if p.lastVideoNS > outAnchorNS {
				outAnchorNS = p.lastVideoNS
			}
			epoch = time.Now()
			p.log.Info("house: seeked", "to", time.Duration(seek)*time.Millisecond,
				"landed", time.Duration(k.MS)*time.Millisecond)
			if p.OnSeeked != nil {
				p.OnSeeked(k.MS)
			}
			continue
		}

		rec, err := r.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				p.log.Info("house: end of file", "title", hdr.Title)
			} else {
				p.log.Warn("house: read failed", "err", err)
			}
			// Move on to whatever is queued; the credits finishing is exactly
			// when the next episode should start.
			p.mu.Lock()
			stale := p.generation != gen || p.cur != r
			queued := len(p.queue) > 0
			p.mu.Unlock()
			if stale {
				return true
			}
			if queued {
				if err := p.playNext(); err != nil {
					p.log.Warn("house: could not start the next item", "err", err)
				} else if p.OnAdvance != nil {
					p.OnAdvance(p.State().Header.Title)
				}
				return true
			}
			p.mu.Lock()
			if p.generation == gen && p.cur == r {
				p.paused = true // hold on the last frame rather than unpublishing
			}
			p.mu.Unlock()
			return true
		}

		_, samples := r.Position()
		// Due times in nanoseconds: at 2.5 ms Opus frames and 23.976 fps,
		// truncating to milliseconds loses enough to drift over a film.
		var dueNS int64
		switch rec.Kind {
		case vsm.KindVideo:
			// Computed from the frame index against the exact rational rate,
			// never as index x a pre-truncated frame duration: truncating
			// 1/24 s to whole nanoseconds once and multiplying loses a tick a
			// frame, which is about two seconds of drift over a feature.
			dueNS = int64(rec.Frame) * int64(hdr.FPSDen) * 1e9 / int64(hdr.FPSNum)
		case vsm.KindAudio:
			// samples already includes this packet, so back it out to get the
			// packet's own start time.
			dueNS = (samples - int64(rec.Samples)) * 1e9 / int64(audioRate(hdr))
		}
		dueMS := dueNS / 1e6
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
			outNS := outAnchorNS + (dueNS - segFileNS)
			if outNS < p.lastVideoNS {
				outNS = p.lastVideoNS
			}
			p.lastVideoNS = outNS
			// Rounded, not truncated: at 24 fps truncation lands every frame a
			// tick early and the error compounds.
			ts := uint32((outNS*90000 + 5e8) / 1e9)
			if err := p.sink.WriteVideo(rec.Data, ts); err != nil {
				p.log.Warn("house: video write failed", "err", err)
			}
			p.setPos(dueMS)
		case vsm.KindAudio:
			ts := uint32(p.outSamples)
			if err := p.sink.WriteAudio(rec.Data, ts); err != nil {
				p.log.Warn("house: audio write failed", "err", err)
			}
			p.outSamples += int64(rec.Samples)
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
