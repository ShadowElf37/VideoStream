package mpvhost

// The OpenGL render path.
//
// The software renderer has never actually been the bottleneck — around 19%
// of one core for 1080p on Apple Silicon — so this is the fallback the plan
// named for the case where it becomes one: a 4K source, or dense ASS subtitles
// that libass has to rasterise per frame. mpv's GL renderer does the scaling
// and the subtitle compositing on the GPU; what comes back over PCIe is only
// the finished picture.
//
// That readback is the catch, and it is why this is not the default. The frame
// has to come *back* into system memory for the encoder, so the GPU does the
// drawing and the bus does the rest. `glReadPixels` into a mapped buffer is a
// full pipeline stall unless it is made asynchronous, which is what the two
// pixel buffer objects below are for: issue the read into one, and copy out of
// the one issued last time, by which point the GPU has finished with it. That
// costs one frame of latency, so the wall clock that stamps a frame is
// recorded when its read was *issued*, not when it is collected — otherwise
// every frame would be timestamped ~40 ms late and the audio would drift
// against it.
//
// Threading: on macOS a GL context belongs to the thread that made it current,
// and GLFW insists window operations happen on the main thread. So unlike the
// software path — which runs in a goroutine started by New — this loop is
// driven from main's own goroutine, with the process's main OS thread locked
// under it. Everything else in the projector stays exactly where it was.

import (
	"context"
	"fmt"
	"runtime"
	"time"
	"unsafe"

	"github.com/gen2brain/go-mpv"
	"github.com/go-gl/gl/v3.2-core/gl"
	"github.com/go-gl/glfw/v3.3/glfw"
)

// Render paths, as the --render flag spells them.
const (
	RenderSW = "sw"
	RenderGL = "gl"
)

// ValidRender reports whether s names a render path.
func ValidRender(s string) bool { return s == RenderSW || s == RenderGL }

// glTarget is the offscreen framebuffer mpv draws into, plus the two pixel
// buffers the finished frames come back through.
type glTarget struct {
	fbo  uint32
	tex  uint32
	rbo  uint32
	pbos [2]uint32
	w, h int

	// pending records, per PBO, whether a read is in flight and when it was
	// issued — the moment that is the frame's presentation time.
	pending [2]bool
	issued  [2]time.Time
}

func (t *glTarget) free() {
	if t.fbo != 0 {
		gl.DeleteFramebuffers(1, &t.fbo)
	}
	if t.tex != 0 {
		gl.DeleteTextures(1, &t.tex)
	}
	if t.rbo != 0 {
		gl.DeleteRenderbuffers(1, &t.rbo)
	}
	if t.pbos[0] != 0 {
		gl.DeleteBuffers(2, &t.pbos[0])
	}
	*t = glTarget{}
}

func (t *glTarget) resize(w, h int) error {
	t.free()
	t.w, t.h = w, h

	gl.GenTextures(1, &t.tex)
	gl.BindTexture(gl.TEXTURE_2D, t.tex)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MIN_FILTER, gl.LINEAR)
	gl.TexParameteri(gl.TEXTURE_2D, gl.TEXTURE_MAG_FILTER, gl.LINEAR)
	gl.TexImage2D(gl.TEXTURE_2D, 0, gl.RGBA8, int32(w), int32(h), 0, gl.RGBA, gl.UNSIGNED_BYTE, nil)

	// mpv wants a depth/stencil attachment available even though it draws no
	// geometry that needs one; without it some drivers report the framebuffer
	// incomplete.
	gl.GenRenderbuffers(1, &t.rbo)
	gl.BindRenderbuffer(gl.RENDERBUFFER, t.rbo)
	gl.RenderbufferStorage(gl.RENDERBUFFER, gl.DEPTH24_STENCIL8, int32(w), int32(h))

	gl.GenFramebuffers(1, &t.fbo)
	gl.BindFramebuffer(gl.FRAMEBUFFER, t.fbo)
	gl.FramebufferTexture2D(gl.FRAMEBUFFER, gl.COLOR_ATTACHMENT0, gl.TEXTURE_2D, t.tex, 0)
	gl.FramebufferRenderbuffer(gl.FRAMEBUFFER, gl.DEPTH_STENCIL_ATTACHMENT, gl.RENDERBUFFER, t.rbo)
	if status := gl.CheckFramebufferStatus(gl.FRAMEBUFFER); status != gl.FRAMEBUFFER_COMPLETE {
		t.free()
		return fmt.Errorf("framebuffer incomplete: 0x%x", status)
	}
	gl.BindFramebuffer(gl.FRAMEBUFFER, 0)

	gl.GenBuffers(2, &t.pbos[0])
	for _, p := range t.pbos {
		gl.BindBuffer(gl.PIXEL_PACK_BUFFER, p)
		gl.BufferData(gl.PIXEL_PACK_BUFFER, w*h*4, nil, gl.STREAM_READ)
	}
	gl.BindBuffer(gl.PIXEL_PACK_BUFFER, 0)
	return nil
}

// InitGL brings up the GL context and hands it to mpv.
//
// Separate from RunGL, and called as soon as the host exists, because mpv
// initialises its video output the moment a file is loaded: with no render
// context in place by then it gives up with "vo/libmpv: No render context
// set." and plays the audio only.
//
// Must be called from main's goroutine, on the locked main OS thread, and only
// when the host was created with Render: "gl".
func (h *Host) InitGL() error {
	if h.cfg.Render != RenderGL {
		return fmt.Errorf("host was created for the %q render path", h.cfg.Render)
	}
	runtime.LockOSThread()

	if err := glfw.Init(); err != nil {
		return fmt.Errorf("glfw init: %w", err)
	}

	// An invisible 1x1 window: all we want is the context. The picture is
	// drawn into an offscreen framebuffer and read straight back out, so
	// nothing is ever presented and there is no vsync to wait on.
	glfw.WindowHint(glfw.Visible, glfw.False)
	glfw.WindowHint(glfw.ContextVersionMajor, 3)
	glfw.WindowHint(glfw.ContextVersionMinor, 2)
	glfw.WindowHint(glfw.OpenGLProfile, glfw.OpenGLCoreProfile)
	glfw.WindowHint(glfw.OpenGLForwardCompatible, glfw.True)
	win, err := glfw.CreateWindow(1, 1, "videostream-projector", nil, nil)
	if err != nil {
		glfw.Terminate()
		return fmt.Errorf("glfw window: %w", err)
	}
	win.MakeContextCurrent()
	// No presentation, so no reason to throttle to a display that is not
	// being drawn to.
	glfw.SwapInterval(0)

	if err := gl.Init(); err != nil {
		win.Destroy()
		glfw.Terminate()
		return fmt.Errorf("gl init: %w", err)
	}

	rc, err := h.mpv.NewRenderContextGL(func(name string) unsafe.Pointer {
		return glfw.GetProcAddress(name)
	})
	if err != nil {
		win.Destroy()
		glfw.Terminate()
		return fmt.Errorf("mpv gl render context: %w", err)
	}
	rc.SetUpdateCallback(func() {
		// Runs on an mpv thread: only ever signal.
		select {
		case h.wake <- struct{}{}:
		default:
		}
	})
	h.setRenderContext(rc)
	h.glWin = win
	h.log.Info("mpv: opengl render path",
		"renderer", gl.GoStr(gl.GetString(gl.RENDERER)),
		"version", gl.GoStr(gl.GetString(gl.VERSION)))
	return nil
}

// RunGL drives the OpenGL render loop until ctx is done, then tears the
// context down. Same goroutine and same locked thread as InitGL.
func (h *Host) RunGL(ctx context.Context) error {
	rc := h.renderContext()
	if h.cfg.Render != RenderGL || rc == nil || h.glWin == nil {
		return fmt.Errorf("RunGL without a successful InitGL")
	}
	defer func() {
		h.setRenderContext(nil)
		rc.Free()
		h.glWin.Destroy()
		h.glWin = nil
		glfw.Terminate()
		runtime.UnlockOSThread()
	}()

	var target glTarget
	defer target.free()
	const nbuf = 3
	var bufs [nbuf][]byte
	// slot walks the two pixel buffers and must advance on every frame, not
	// only on the ones that produce output — otherwise the first frame issues
	// its read into slot 0, finds slot 1 empty, and the pair never swaps.
	slot := 0
	bufIdx := 0
	var gen uint64

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-h.done:
			return nil
		case <-h.wake:
		}
		if rc.Update()&mpv.RenderUpdateFrame == 0 {
			continue
		}

		h.outMu.Lock()
		w, hgt, g := h.outW, h.outH, h.outGen
		h.outMu.Unlock()
		if w != target.w || hgt != target.h || g != gen {
			if err := target.resize(w, hgt); err != nil {
				return err
			}
			for i := range bufs {
				bufs[i] = make([]byte, w*4*hgt)
			}
			gen = g
			slot, bufIdx = 0, 0
		}

		// flipY, because a framebuffer's origin is bottom-left and
		// glReadPixels hands back rows from the bottom up. Letting mpv draw
		// upside down is free; flipping 8 MB per frame on the CPU is not.
		if err := rc.RenderGL(int(target.fbo), target.w, target.h, true); err != nil {
			h.log.Warn("mpv: gl render failed", "err", err)
			continue
		}
		// mpv leaves its own state behind; the readback needs ours.
		gl.BindFramebuffer(gl.FRAMEBUFFER, target.fbo)
		gl.ReadBuffer(gl.COLOR_ATTACHMENT0)
		gl.PixelStorei(gl.PACK_ALIGNMENT, 1)

		next, prev := slot%2, (slot+1)%2
		slot++
		gl.BindBuffer(gl.PIXEL_PACK_BUFFER, target.pbos[next])
		// BGRA so the bytes land as B,G,R,X — which is the "bgr0" the encoder
		// already consumes from the software path, with no conversion here.
		gl.ReadPixels(0, 0, int32(target.w), int32(target.h), gl.BGRA, gl.UNSIGNED_BYTE, nil)
		target.pending[next] = true
		target.issued[next] = time.Now()

		// Collect the read issued last time round, which the GPU has had a
		// whole frame to finish.
		if !target.pending[prev] {
			gl.BindBuffer(gl.PIXEL_PACK_BUFFER, 0)
			gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
			continue
		}
		gl.BindBuffer(gl.PIXEL_PACK_BUFFER, target.pbos[prev])
		ptr := gl.MapBuffer(gl.PIXEL_PACK_BUFFER, gl.READ_ONLY)
		if ptr == nil {
			target.pending[prev] = false
			gl.BindBuffer(gl.PIXEL_PACK_BUFFER, 0)
			gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
			h.log.Warn("mpv: could not map the pixel buffer")
			continue
		}
		buf := bufs[bufIdx%nbuf]
		bufIdx++
		copy(buf, unsafe.Slice((*byte)(ptr), len(buf)))
		gl.UnmapBuffer(gl.PIXEL_PACK_BUFFER)
		gl.BindBuffer(gl.PIXEL_PACK_BUFFER, 0)
		gl.BindFramebuffer(gl.FRAMEBUFFER, 0)
		target.pending[prev] = false

		h.renders.Add(1)
		select {
		case h.frames <- Frame{Buf: buf, Wall: target.issued[prev], Width: target.w, Height: target.h}:
		default:
			// Drop-newest: the consumer is still busy with the previous frame.
			h.drops.Add(1)
		}
	}
}
