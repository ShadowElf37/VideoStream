//go:build darwin

package encoder

// The in-process VideoToolbox encoder.
//
// The ffmpeg child cannot be asked for an IDR. PLI and FIR arrive on the RTCP
// handler, are counted, and are then thrown away, because there is no way to
// reach into `ffmpeg -force_key_frames expr:...` and say "now". So the GOP is
// pinned at two seconds and a late joiner stares at nothing until the next one
// comes round. Two seconds of black is the sort of thing people quietly decide
// the whole projector is broken over.
//
// VideoToolbox in-process fixes exactly that: `kVTEncodeFrameOptionKey_ForceKeyFrame`
// on the next frame, which at 24 fps is at most 42 ms away. Everything else is
// kept the same as the ffmpeg path on purpose — same real-time mode, same
// absent B-frames, same 2 s GOP floor so a joiner who never sends a PLI still
// gets a picture — so that switching between them changes one thing only.
//
// The 2 s floor stays for another reason too: a PLI storm from several joiners
// at once must not turn the stream into all-intra. Forced keyframes are
// coalesced to at most one every KeyframeMinInterval.

/*
#cgo CFLAGS: -x objective-c
#cgo LDFLAGS: -framework VideoToolbox -framework CoreMedia -framework CoreVideo -framework CoreFoundation
#include <stdlib.h>
#include <string.h>
#include <VideoToolbox/VideoToolbox.h>

// vtOutput is the Go trampoline (see the //export below).
void vtOutput(uintptr_t token, void *data, int len, int key, int64_t ptsNanos);

typedef struct {
	VTCompressionSessionRef session;
	CVPixelBufferPoolRef pool; // our own, 32BGRA — see vt_encode
	int width, height;
	uintptr_t token;
	int nalLen;      // AVCC length-prefix size, learned from the format description
	uint8_t *params; // cached SPS/PPS in Annex-B, prepended to every IDR
	int paramsLen;
} vt_enc;

// appendNAL writes a 4-byte start code plus the payload into dst at *off.
static void appendNAL(uint8_t *dst, int *off, const uint8_t *src, int len) {
	dst[*off + 0] = 0; dst[*off + 1] = 0; dst[*off + 2] = 0; dst[*off + 3] = 1;
	memcpy(dst + *off + 4, src, len);
	*off += 4 + len;
}

// cacheParams pulls SPS/PPS out of the format description and keeps them in
// Annex-B form. VideoToolbox only hands them over with the first sample of a
// new description, and RTP needs them in front of every IDR.
static void cacheParams(vt_enc *e, CMFormatDescriptionRef fmt) {
	size_t count = 0;
	int nalLen = 4;
	if (CMVideoFormatDescriptionGetH264ParameterSetAtIndex(fmt, 0, NULL, NULL, &count, &nalLen) != noErr) return;
	e->nalLen = nalLen;

	int total = 0;
	for (size_t i = 0; i < count; i++) {
		const uint8_t *p = NULL; size_t sz = 0;
		if (CMVideoFormatDescriptionGetH264ParameterSetAtIndex(fmt, i, &p, &sz, NULL, NULL) != noErr) return;
		total += 4 + (int)sz;
	}
	uint8_t *buf = malloc(total);
	if (!buf) return;
	int off = 0;
	for (size_t i = 0; i < count; i++) {
		const uint8_t *p = NULL; size_t sz = 0;
		if (CMVideoFormatDescriptionGetH264ParameterSetAtIndex(fmt, i, &p, &sz, NULL, NULL) != noErr) { free(buf); return; }
		appendNAL(buf, &off, p, (int)sz);
	}
	free(e->params);
	e->params = buf;
	e->paramsLen = off;
}

static void vt_callback(void *refcon, void *frameRefcon, OSStatus status,
                        VTEncodeInfoFlags flags, CMSampleBufferRef sbuf) {
	vt_enc *e = (vt_enc *)refcon;
	if (status != noErr || sbuf == NULL || !CMSampleBufferDataIsReady(sbuf)) return;

	// A frame is a sync sample unless it is explicitly marked otherwise.
	int key = 1;
	CFArrayRef attachments = CMSampleBufferGetSampleAttachmentsArray(sbuf, false);
	if (attachments && CFArrayGetCount(attachments) > 0) {
		CFDictionaryRef d = CFArrayGetValueAtIndex(attachments, 0);
		CFBooleanRef notSync = NULL;
		if (CFDictionaryGetValueIfPresent(d, kCMSampleAttachmentKey_NotSync, (const void **)&notSync)
		    && notSync && CFBooleanGetValue(notSync)) {
			key = 0;
		}
	}
	if (key) cacheParams(e, CMSampleBufferGetFormatDescription(sbuf));

	CMBlockBufferRef block = CMSampleBufferGetDataBuffer(sbuf);
	if (!block) return;
	size_t total = 0;
	char *avcc = NULL;
	if (CMBlockBufferGetDataPointer(block, 0, NULL, &total, &avcc) != noErr) return;

	int nalLen = e->nalLen > 0 ? e->nalLen : 4;
	// Worst case every NAL grows by (4 - nalLen) bytes, plus the parameter
	// sets on an IDR.
	int cap = (int)total + 4 * ((int)total / (nalLen + 1) + 1) + (key ? e->paramsLen : 0);
	uint8_t *out = malloc(cap);
	if (!out) return;
	int off = 0;
	if (key && e->params) { memcpy(out, e->params, e->paramsLen); off = e->paramsLen; }

	size_t i = 0;
	while (i + (size_t)nalLen <= total) {
		uint32_t n = 0;
		for (int b = 0; b < nalLen; b++) n = (n << 8) | (uint8_t)avcc[i + b];
		i += nalLen;
		if (n == 0 || i + n > total) break;
		appendNAL(out, &off, (const uint8_t *)(avcc + i), (int)n);
		i += n;
	}

	// The raw numerator, not CMTimeGetSeconds: the PTS is an exact identity
	// for the input frame, and a double cannot round-trip a nanosecond count
	// without moving it. Everything is created at a 1 ns timescale.
	CMTime pts = CMSampleBufferGetPresentationTimeStamp(sbuf);
	int64_t ptsNanos = (pts.timescale == 1000000000)
		? pts.value
		: (int64_t)((double)pts.value / (double)pts.timescale * 1e9);
	vtOutput(e->token, out, off, key, ptsNanos);
	free(out);
}

static void setNum(VTCompressionSessionRef s, CFStringRef key, int32_t v) {
	CFNumberRef n = CFNumberCreate(kCFAllocatorDefault, kCFNumberSInt32Type, &v);
	VTSessionSetProperty(s, key, n);
	CFRelease(n);
}

// setRate applies both bitrate controls.
//
// AverageBitRate on its own is advisory, and in real-time mode VideoToolbox
// cheerfully ignores it: asking for 3000 kbps produced a measured 5500-7700.
// DataRateLimits is the hard one — bytes per window — and it is what actually
// holds the stream inside what the preset promised and the network was sized
// for. One-second window, to match what the ffmpeg path gets from
// -maxrate/-bufsize at the same value.
static int setRate(VTCompressionSessionRef s, int kbps) {
	int32_t bits = kbps * 1000;
	CFNumberRef n = CFNumberCreate(kCFAllocatorDefault, kCFNumberSInt32Type, &bits);
	OSStatus a = VTSessionSetProperty(s, kVTCompressionPropertyKey_AverageBitRate, n);
	CFRelease(n);

	int64_t bytesPerSecond = (int64_t)kbps * 1000 / 8;
	double window = 1.0;
	CFNumberRef b = CFNumberCreate(kCFAllocatorDefault, kCFNumberSInt64Type, &bytesPerSecond);
	CFNumberRef w = CFNumberCreate(kCFAllocatorDefault, kCFNumberFloat64Type, &window);
	const void *vals[2] = { b, w };
	CFArrayRef limits = CFArrayCreate(kCFAllocatorDefault, vals, 2, &kCFTypeArrayCallBacks);
	OSStatus d = VTSessionSetProperty(s, kVTCompressionPropertyKey_DataRateLimits, limits);
	CFRelease(limits);
	CFRelease(b);
	CFRelease(w);
	if (a != noErr) return (int)a;
	return (int)d;
}

static int vt_create(vt_enc *e, int width, int height, int kbps, int gopFrames, double fps, uintptr_t token, int *rateErr) {
	memset(e, 0, sizeof(*e));
	e->token = token;
	e->nalLen = 4;
	e->width = width;
	e->height = height;

	// Our own BGRA pool, IOSurface-backed so the hardware encoder can take it
	// without another copy.
	{
		int32_t fmt = kCVPixelFormatType_32BGRA;
		CFNumberRef nfmt = CFNumberCreate(kCFAllocatorDefault, kCFNumberSInt32Type, &fmt);
		CFNumberRef nw = CFNumberCreate(kCFAllocatorDefault, kCFNumberIntType, &width);
		CFNumberRef nh = CFNumberCreate(kCFAllocatorDefault, kCFNumberIntType, &height);
		CFDictionaryRef iosurface = CFDictionaryCreate(kCFAllocatorDefault, NULL, NULL, 0,
			&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
		const void *keys[] = {
			kCVPixelBufferPixelFormatTypeKey, kCVPixelBufferWidthKey,
			kCVPixelBufferHeightKey, kCVPixelBufferIOSurfacePropertiesKey,
		};
		const void *vals[] = { nfmt, nw, nh, iosurface };
		CFDictionaryRef attrs = CFDictionaryCreate(kCFAllocatorDefault, keys, vals, 4,
			&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
		OSStatus ps = CVPixelBufferPoolCreate(kCFAllocatorDefault, NULL, attrs, &e->pool);
		CFRelease(attrs); CFRelease(iosurface); CFRelease(nfmt); CFRelease(nw); CFRelease(nh);
		if (ps != noErr) return (int)ps;
	}

	CFMutableDictionaryRef spec = CFDictionaryCreateMutable(kCFAllocatorDefault, 1,
		&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	// Ask for hardware, but do not insist: a machine without it should still
	// stream, just more expensively.
	CFDictionarySetValue(spec, kVTVideoEncoderSpecification_EnableHardwareAcceleratedVideoEncoder, kCFBooleanTrue);

	OSStatus st = VTCompressionSessionCreate(kCFAllocatorDefault, width, height,
		kCMVideoCodecType_H264, spec, NULL, NULL, vt_callback, e, &e->session);
	CFRelease(spec);
	if (st != noErr) return (int)st;

	VTSessionSetProperty(e->session, kVTCompressionPropertyKey_RealTime, kCFBooleanTrue);
	VTSessionSetProperty(e->session, kVTCompressionPropertyKey_ProfileLevel, kVTProfileLevel_H264_Main_AutoLevel);
	// No B-frames: browser H.264 over RTP assumes decode order equals display
	// order, and our RTP timestamps are absolute functions of the media clock.
	VTSessionSetProperty(e->session, kVTCompressionPropertyKey_AllowFrameReordering, kCFBooleanFalse);
	setNum(e->session, kVTCompressionPropertyKey_MaxKeyFrameInterval, gopFrames);
	setNum(e->session, kVTCompressionPropertyKey_MaxKeyFrameIntervalDuration, (int32_t)(gopFrames / (fps > 0 ? fps : 24) + 0.5));
	*rateErr = setRate(e->session, kbps);
	setNum(e->session, kVTCompressionPropertyKey_ExpectedFrameRate, (int32_t)(fps + 0.5));

	VTCompressionSessionPrepareToEncodeFrames(e->session);
	return 0;
}

static int vt_set_bitrate(vt_enc *e, int kbps) {
	if (!e->session) return -1;
	setRate(e->session, kbps);
	return 0;
}

// vt_encode copies one BGRX frame into a pixel buffer and submits it.
//
// The buffer comes from a pool we create ourselves rather than the session's
// own (VTCompressionSessionGetPixelBufferPool). That pool is in the encoder's
// preferred format, which on Apple Silicon is biplanar NV12 — and a planar
// buffer has no single base address, so copying BGRA rows into it writes
// through a NULL pointer. It segfaulted at 540p and, worse, quietly did
// something at 720p. Ours is always 32BGRA and VideoToolbox converts.
static int vt_encode(vt_enc *e, const uint8_t *bgrx, int width, int height, int64_t ptsNanos, int forceKey) {
	if (!e->session || !e->pool || width != e->width || height != e->height) return -1;

	CVPixelBufferRef pb = NULL;
	OSStatus st = CVPixelBufferPoolCreatePixelBuffer(kCFAllocatorDefault, e->pool, &pb);
	if (st != noErr || !pb) return (int)st;

	if (CVPixelBufferIsPlanar(pb)) { CVPixelBufferRelease(pb); return -2; }
	CVPixelBufferLockBaseAddress(pb, 0);
	uint8_t *dst = CVPixelBufferGetBaseAddress(pb);
	size_t dstStride = CVPixelBufferGetBytesPerRow(pb);
	size_t srcStride = (size_t)width * 4;
	if (!dst || dstStride < srcStride) {
		CVPixelBufferUnlockBaseAddress(pb, 0);
		CVPixelBufferRelease(pb);
		return -3;
	}
	// Row by row: a pool buffer's stride is rounded up for alignment and is
	// rarely width*4.
	for (int y = 0; y < height; y++) memcpy(dst + (size_t)y * dstStride, bgrx + (size_t)y * srcStride, srcStride);
	CVPixelBufferUnlockBaseAddress(pb, 0);

	CFDictionaryRef props = NULL;
	if (forceKey) {
		const void *k = kVTEncodeFrameOptionKey_ForceKeyFrame;
		const void *v = kCFBooleanTrue;
		props = CFDictionaryCreate(kCFAllocatorDefault, &k, &v, 1,
			&kCFTypeDictionaryKeyCallBacks, &kCFTypeDictionaryValueCallBacks);
	}
	CMTime pts = CMTimeMake(ptsNanos, 1000000000);
	VTEncodeInfoFlags flags = 0;
	st = VTCompressionSessionEncodeFrame(e->session, pb, pts, kCMTimeInvalid, props, NULL, &flags);
	if (props) CFRelease(props);
	CVPixelBufferRelease(pb);
	return (int)st;
}

static void vt_destroy(vt_enc *e) {
	if (e->session) {
		VTCompressionSessionCompleteFrames(e->session, kCMTimeInvalid);
		VTCompressionSessionInvalidate(e->session);
		CFRelease(e->session);
		e->session = NULL;
	}
	if (e->pool) {
		CVPixelBufferPoolRelease(e->pool);
		e->pool = NULL;
	}
	free(e->params);
	e->params = NULL;
	e->paramsLen = 0;
}
*/
import "C"

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"
)

// VTName is what --encoder takes to select this path.
const VTName = "vt"

// KeyframeMinInterval coalesces forced keyframes. Several viewers joining at
// once means several PLIs within a few hundred milliseconds, and honouring
// each one turns the stream all-intra at the exact moment it is most loaded.
const KeyframeMinInterval = 500 * time.Millisecond

// vtRegistry maps a token to the encoder, so the C callback never holds a Go
// pointer. cgo forbids that outright, and a token is cheaper than the
// alternatives anyway.
var (
	vtMu   sync.Mutex
	vtSeq  uintptr
	vtEncs = map[uintptr]*VideoToolbox{}
)

//export vtOutput
func vtOutput(token C.uintptr_t, data unsafe.Pointer, length C.int, key C.int, ptsNanos C.int64_t) {
	vtMu.Lock()
	v := vtEncs[uintptr(token)]
	vtMu.Unlock()
	if v == nil || length <= 0 {
		return
	}
	// The C buffer is freed as soon as this returns, so copy before handing it
	// on — the sink writes RTP asynchronously.
	au := make([]byte, int(length))
	copy(au, unsafe.Slice((*byte)(data), int(length)))
	v.deliver(au, key != 0, int64(ptsNanos))
}

// VideoToolbox is an in-process H.264 encoder with the same shape as the
// ffmpeg-child Video: frames in through Submit, access units out through the
// configured Sink.
type VideoToolbox struct {
	log *slog.Logger
	cfg VideoConfig
	enc C.vt_enc
	tok uintptr

	frameBytes int

	mu          sync.Mutex
	pending     []byte
	pendingWall time.Time
	hasPending  bool
	spare       []byte
	cur         []byte

	// forceKey is set by ForceKeyframe and consumed by the next paced frame.
	forceKey  atomic.Bool
	lastForce atomic.Int64 // unix nanos

	// epoch is what PTS values are measured from, so they stay small: they
	// are only ever an identity for matching an access unit to the frame that
	// produced it, and a unix nanosecond count is needlessly large.
	epoch time.Time

	done chan struct{}
	stop sync.Once
	wg   sync.WaitGroup

	// pts is the 90 kHz stamp for the frame currently in flight, keyed by the
	// nanosecond PTS we handed VideoToolbox. No B-frames, so one in one out in
	// order, and a small map is enough.
	tsMu sync.Mutex
	ts   map[int64]tsEntry

	fed    atomic.Int64
	dup    atomic.Int64
	aus    atomic.Int64
	bytes  atomic.Int64
	keys   atomic.Int64
	lateMs atomic.Int64
	forced atomic.Int64
}

// NewVideoToolbox creates the compression session and starts the pacing loop.
func NewVideoToolbox(ctx context.Context, log *slog.Logger, cfg VideoConfig) (*VideoToolbox, error) {
	if cfg.FPSNum <= 0 || cfg.FPSDen <= 0 {
		cfg.FPSNum, cfg.FPSDen = 24000, 1001
	}
	fps := float64(cfg.FPSNum) / float64(cfg.FPSDen)
	gop := int(fps*2 + 0.5)
	if gop < 2 {
		gop = 2
	}

	v := &VideoToolbox{
		log:        log,
		cfg:        cfg,
		frameBytes: cfg.Preset.Width * 4 * cfg.Preset.Height,
		done:       make(chan struct{}),
		ts:         map[int64]tsEntry{},
		epoch:      time.Now(),
	}

	vtMu.Lock()
	vtSeq++
	v.tok = vtSeq
	vtEncs[v.tok] = v
	vtMu.Unlock()

	var rateErr C.int
	rc := C.vt_create(&v.enc, C.int(cfg.Preset.Width), C.int(cfg.Preset.Height),
		C.int(cfg.Preset.Kbps), C.int(gop), C.double(fps), C.uintptr_t(v.tok), &rateErr)
	if rc != 0 {
		vtMu.Lock()
		delete(vtEncs, v.tok)
		vtMu.Unlock()
		return nil, fmt.Errorf("VTCompressionSessionCreate: OSStatus %d", int(rc))
	}

	if rateErr != 0 {
		// Worth saying out loud: without a working rate limit the stream runs
		// at whatever the encoder feels like, which on this content measured
		// nearly twice the preset.
		log.Warn("encoder: videotoolbox rejected a bitrate property", "status", int(rateErr))
	}
	log.Info("encoder: videotoolbox in-process",
		"preset", cfg.Preset.Name,
		"size", fmt.Sprintf("%dx%d", cfg.Preset.Width, cfg.Preset.Height),
		"fps", fmt.Sprintf("%d/%d", cfg.FPSNum, cfg.FPSDen),
		"kbps", cfg.Preset.Kbps, "gopFrames", gop)

	v.wg.Add(1)
	go func() { defer v.wg.Done(); v.paceLoop(ctx) }()
	return v, nil
}

// Submit hands the newest rendered frame over. Identical contract to the
// ffmpeg path: the buffer is copied and the caller may reuse it at once.
func (v *VideoToolbox) Submit(buf []byte, wall time.Time) {
	if len(buf) != v.frameBytes {
		return
	}
	v.mu.Lock()
	if v.spare == nil {
		v.spare = make([]byte, v.frameBytes)
	}
	copy(v.spare, buf)
	v.pending, v.spare = v.spare, v.pending
	v.hasPending = true
	v.pendingWall = wall
	v.mu.Unlock()
}

// ForceKeyframe asks for an IDR on the next frame. This is the whole point of
// the in-process encoder: a PLI from a late joiner produces a picture in one
// frame time rather than at the next two-second boundary.
func (v *VideoToolbox) ForceKeyframe() {
	now := time.Now().UnixNano()
	last := v.lastForce.Load()
	if now-last < int64(KeyframeMinInterval) {
		return
	}
	if !v.lastForce.CompareAndSwap(last, now) {
		return
	}
	v.forceKey.Store(true)
}

// SetBitrate retargets the session without tearing it down.
func (v *VideoToolbox) SetBitrate(kbps int) {
	C.vt_set_bitrate(&v.enc, C.int(kbps))
}

// paceLoop feeds exactly one frame per output tick, duplicating the last
// picture when mpv produced nothing. Same reasoning as the ffmpeg path: the
// keyframe cadence is counted in frames, so only a full-rate feed holds it,
// and a static picture costs the encoder almost nothing.
func (v *VideoToolbox) paceLoop(ctx context.Context) {
	start := time.Now()
	period := func(n int64) time.Duration {
		return time.Duration(n * int64(time.Second) * int64(v.cfg.FPSDen) / int64(v.cfg.FPSNum))
	}
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()

	var n int64
	for {
		n++
		if d := time.Until(start.Add(period(n))); d > 0 {
			timer.Reset(d)
			select {
			case <-ctx.Done():
				return
			case <-v.done:
				return
			case <-timer.C:
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-v.done:
			return
		default:
		}
		now := time.Now()

		v.mu.Lock()
		fresh := v.hasPending
		if fresh {
			v.cur, v.pending = v.pending, v.cur
			v.hasPending = false
		}
		frame := v.cur
		wall := v.pendingWall
		v.mu.Unlock()

		if frame == nil {
			continue
		}
		if !fresh {
			v.dup.Add(1)
		}
		stampAt := now
		if fresh {
			stampAt = wall
		}

		// The PTS handed to VideoToolbox is only an identity: it comes back on
		// the sample buffer and is how the access unit finds the 90 kHz stamp
		// that belongs to it.
		key := now.Sub(v.epoch).Nanoseconds()
		v.tsMu.Lock()
		v.ts[key] = tsEntry{ts: v.cfg.Stamp(stampAt), fed: now}
		v.tsMu.Unlock()

		force := 0
		if v.forceKey.Swap(false) {
			force = 1
			v.forced.Add(1)
		}
		rc := C.vt_encode(&v.enc, (*C.uint8_t)(unsafe.Pointer(&frame[0])),
			C.int(v.cfg.Preset.Width), C.int(v.cfg.Preset.Height), C.int64_t(key), C.int(force))
		if rc != 0 {
			v.tsMu.Lock()
			delete(v.ts, key)
			v.tsMu.Unlock()
			v.log.Warn("encoder: VTCompressionSessionEncodeFrame failed", "status", int(rc))
			continue
		}
		v.fed.Add(1)
	}
}

func (v *VideoToolbox) deliver(au []byte, key bool, ptsNanos int64) {
	v.tsMu.Lock()
	e, ok := v.ts[ptsNanos]
	delete(v.ts, ptsNanos)
	// A frame that never came back would leak its entry; the session is
	// in-order and B-frame-free, so anything older than this one is gone.
	for k := range v.ts {
		if k < ptsNanos {
			delete(v.ts, k)
		}
	}
	v.tsMu.Unlock()
	if !ok {
		v.log.Warn("encoder: access unit with no matching input timestamp")
		return
	}

	late := time.Since(e.fed)
	v.aus.Add(1)
	v.bytes.Add(int64(len(au)))
	v.lateMs.Store(late.Milliseconds())
	if key {
		v.keys.Add(1)
	}
	if v.cfg.Sink != nil {
		v.cfg.Sink(AccessUnit{Data: au, TS90k: e.ts, Key: key, Late: late})
	}
}

// Stats returns counters for the status line and mpv.state.
func (v *VideoToolbox) Stats() VideoStats {
	return VideoStats{
		Fed:       v.fed.Load(),
		Dup:       v.dup.Load(),
		AUs:       v.aus.Load(),
		Bytes:     v.bytes.Load(),
		Keyframes: v.keys.Load(),
		LateMs:    v.lateMs.Load(),
	}
}

// Forced counts keyframes produced on demand rather than on the GOP cadence.
func (v *VideoToolbox) Forced() int64 { return v.forced.Load() }

// Close stops pacing and tears the session down.
func (v *VideoToolbox) Close() {
	v.stop.Do(func() { close(v.done) })
	v.wg.Wait()
	// After the pacing goroutine is gone, nothing else can call into C.
	C.vt_destroy(&v.enc)
	vtMu.Lock()
	delete(vtEncs, v.tok)
	vtMu.Unlock()
}
