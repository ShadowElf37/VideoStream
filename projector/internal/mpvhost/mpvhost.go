// Package mpvhost embeds libmpv: it owns the mpv instance, its software render
// context, the observed-property snapshot that becomes proto.MpvState, and the
// event stream that becomes proto.MpvEvent.
//
// Threading rules that the rest of the projector relies on:
//   - only the render goroutine touches RenderContext methods;
//   - the render update callback runs on an mpv thread and only signals a
//     channel;
//   - the event goroutine is the only caller of WaitEvent.
package mpvhost

import (
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/go-mpv"

	"github.com/ShadowElf37/VideoStream/proto"
)

// Config describes how the embedded mpv is set up. Everything here is applied
// as an option before mpv.Initialize.
type Config struct {
	FIFOPath  string // ao-pcm-file
	ConfigDir string // config-dir; the user's own mpv.conf/scripts/fonts
	IPCSocket string // input-ipc-server
	Width     int    // initial render size
	Height    int
	LogLevel  string // mpv msg-level, e.g. "all=warn"
}

// Frame is one rendered picture. Buf is owned by the host and stays valid only
// until the consumer callback returns.
type Frame struct {
	Buf    []byte
	Wall   time.Time // the moment RenderSW returned: the frame's presentation time
	Width  int
	Height int
}

// Host is an embedded mpv instance plus its render loop.
type Host struct {
	log *slog.Logger
	cfg Config
	mpv *mpv.Mpv
	rc  *mpv.RenderContext

	wake   chan struct{}
	events chan proto.MpvEvent
	frames chan Frame
	done   chan struct{}
	closed sync.Once
	wg     sync.WaitGroup

	sink atomic.Pointer[func(Frame)]

	// Output size, read by the render goroutine, written by SetOutput.
	outMu  sync.Mutex
	outW   int
	outH   int
	outGen uint64

	drops   atomic.Int64
	renders atomic.Int64

	mu   sync.Mutex
	snap snapshot
}

// New creates and initialises mpv. The FIFO must already exist.
func New(log *slog.Logger, cfg Config) (*Host, error) {
	if cfg.Width <= 0 || cfg.Height <= 0 {
		cfg.Width, cfg.Height = 1920, 1080
	}
	if cfg.LogLevel == "" {
		cfg.LogLevel = "all=warn"
	}
	h := &Host{
		log:    log,
		cfg:    cfg,
		mpv:    mpv.New(),
		wake:   make(chan struct{}, 1),
		events: make(chan proto.MpvEvent, 64),
		frames: make(chan Frame, 1),
		done:   make(chan struct{}),
		outW:   cfg.Width,
		outH:   cfg.Height,
	}
	h.snap.init()

	opts := [][2]string{
		{"vo", "libmpv"},
		{"ao", "pcm"},
		{"ao-pcm-file", cfg.FIFOPath},
		{"ao-pcm-waveheader", "no"},
		{"audio-format", "s16"},
		{"audio-samplerate", "48000"},
		{"audio-channels", "stereo"},
		{"audio-normalize-downmix", "yes"},
		{"hwdec", "auto-copy"},
		// mpv's default framedrop=vo decides a frame is too late to bother
		// rendering by comparing against the VO's display timing. With
		// vo=libmpv there is no display: we render on demand and hand the
		// frame to an encoder, so that estimate is meaningless — and mpv acts
		// on it, silently dropping frames it has already decoded. On a 1080p24
		// 10-bit HEVC source it dropped ~16 of every 24 frames (decoder drops
		// stayed at 0), which the output loop then papered over by re-sending
		// the previous frame: a steady 24 fps of which only ~8 were new.
		// Nothing downstream can detect that — the encoder, the SFU and the
		// browser all see a well-formed 24 fps stream.
		// We do our own pacing and report genuine misses as dupFrames, so mpv
		// must hand us every frame it decodes.
		{"framedrop", "no"},
		{"keep-open", "yes"},
		{"idle", "yes"},
		{"terminal", "no"},
		{"msg-level", cfg.LogLevel},
		{"osd-level", "0"},
		{"ytdl", "yes"},
		{"input-default-bindings", "no"},
		// mpv letterboxes into the buffer we hand RenderSW, so the encoder
		// always sees the full output rectangle with black bars where needed.
		{"keepaspect", "yes"},
		{"keepaspect-window", "yes"},
		{"video-unscaled", "no"},
	}
	if cfg.ConfigDir != "" {
		opts = append(opts,
			[2]string{"config", "yes"},
			[2]string{"config-dir", cfg.ConfigDir},
			[2]string{"load-scripts", "yes"},
		)
	} else {
		opts = append(opts, [2]string{"config", "no"})
	}
	if cfg.IPCSocket != "" {
		opts = append(opts, [2]string{"input-ipc-server", cfg.IPCSocket})
	}
	for _, kv := range opts {
		if err := h.mpv.SetOptionString(kv[0], kv[1]); err != nil {
			h.mpv.TerminateDestroy()
			return nil, fmt.Errorf("mpv option %s=%s: %w", kv[0], kv[1], err)
		}
	}
	if err := h.mpv.Initialize(); err != nil {
		h.mpv.TerminateDestroy()
		return nil, fmt.Errorf("mpv initialize: %w", err)
	}
	rc, err := h.mpv.NewRenderContextSW()
	if err != nil {
		h.mpv.TerminateDestroy()
		return nil, fmt.Errorf("mpv render context: %w", err)
	}
	h.rc = rc
	rc.SetUpdateCallback(func() {
		// Runs on an mpv thread: only ever signal.
		select {
		case h.wake <- struct{}{}:
		default:
		}
	})
	if err := h.mpv.RequestLogMessages("error"); err != nil {
		h.log.Warn("mpv: request log messages", "err", err)
	}
	h.observe()

	h.wg.Add(3)
	go func() { defer h.wg.Done(); h.renderLoop() }()
	go func() { defer h.wg.Done(); h.eventLoop() }()
	go func() { defer h.wg.Done(); h.dispatchLoop() }()
	return h, nil
}

// Mpv exposes the raw handle for callers that need the typed property API.
func (h *Host) Mpv() *mpv.Mpv { return h.mpv }

// Events is the projector's event stream (file-loaded, seek, pause, ...).
func (h *Host) Events() <-chan proto.MpvEvent { return h.events }

// SetFrameSink installs the consumer of rendered frames. It is called from a
// dispatch goroutine; the Frame's buffer is only valid during the call.
func (h *Host) SetFrameSink(fn func(Frame)) { h.sink.Store(&fn) }

// SetOutput changes the rendered picture size. The render goroutine picks it up
// on the next frame and allocates fresh buffers.
func (h *Host) SetOutput(w, hgt int) {
	if w <= 0 || hgt <= 0 {
		return
	}
	h.outMu.Lock()
	if h.outW != w || h.outH != hgt {
		h.outW, h.outH = w, hgt
		h.outGen++
	}
	h.outMu.Unlock()
	// Nudge the render loop so a still picture is re-rendered at the new size.
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

// Output returns the current render size.
func (h *Host) Output() (int, int) {
	h.outMu.Lock()
	defer h.outMu.Unlock()
	return h.outW, h.outH
}

// Drops counts frames thrown away because the consumer was still busy.
func (h *Host) Drops() int64 { return h.drops.Load() }

// Rendered counts frames handed to RenderSW.
func (h *Host) Rendered() int64 { return h.renders.Load() }

// Close terminates mpv and waits for the goroutines.
func (h *Host) Close() {
	h.closed.Do(func() {
		close(h.done)
		// Waking mpv's event loop lets eventLoop notice done promptly.
		h.mpv.Wakeup()
	})
	h.wg.Wait()
	if h.rc != nil {
		h.rc.Free()
		h.rc = nil
	}
	h.mpv.TerminateDestroy()
	if h.cfg.IPCSocket != "" {
		_ = os.Remove(h.cfg.IPCSocket)
	}
}

// renderLoop is the only goroutine allowed to call RenderContext methods.
// RenderSW blocks until the frame's target display time, so the instant it
// returns is the frame's presentation time on the same monotonic clock Go uses.
func (h *Host) renderLoop() {
	const nbuf = 3 // one being rendered, one queued, one in the consumer
	var bufs [nbuf][]byte
	var bw, bh int
	var gen uint64
	cur := 0

	for {
		select {
		case <-h.done:
			return
		case <-h.wake:
		}
		flags := h.rc.Update()
		if flags&mpv.RenderUpdateFrame == 0 {
			continue
		}
		h.outMu.Lock()
		w, hgt, g := h.outW, h.outH, h.outGen
		h.outMu.Unlock()
		if w != bw || hgt != bh || g != gen {
			for i := range bufs {
				bufs[i] = make([]byte, w*4*hgt)
			}
			bw, bh, gen = w, hgt, g
			cur = 0
		}
		buf := bufs[cur]
		if err := h.rc.RenderSW(bw, bh, bw*4, "bgr0", buf); err != nil {
			h.log.Warn("mpv: render failed", "err", err)
			continue
		}
		now := time.Now()
		h.renders.Add(1)
		select {
		case h.frames <- Frame{Buf: buf, Wall: now, Width: bw, Height: bh}:
			cur = (cur + 1) % nbuf
		default:
			// Drop-newest: the consumer is still busy with the previous frame.
			h.drops.Add(1)
		}
	}
}

// dispatchLoop hands rendered frames to the consumer off the render goroutine
// so a slow consumer cannot stall mpv's render pacing.
func (h *Host) dispatchLoop() {
	for {
		select {
		case <-h.done:
			return
		case f := <-h.frames:
			if p := h.sink.Load(); p != nil {
				(*p)(f)
			}
		}
	}
}
