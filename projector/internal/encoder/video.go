package encoder

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os/exec"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// AccessUnit is one complete H.264 access unit ready for RTP packetization.
type AccessUnit struct {
	Data  []byte
	TS90k uint32
	Key   bool
	Late  time.Duration // how long after its input tick the AU came out
}

// VideoConfig configures one ffmpeg child.
type VideoConfig struct {
	FFmpeg    string
	Candidate Candidate
	Preset    Preset
	FPSNum    int
	FPSDen    int
	// Stamp maps a wall-clock instant onto the 90 kHz media timeline. It is
	// only ever called from the pacing goroutine.
	Stamp func(time.Time) uint32
	// Sink receives access units in encode order. Data is owned by the caller.
	Sink func(AccessUnit)
	// IdleAfter is how long without a fresh rendered frame before we fall back
	// to re-sending the last picture once a second (pause/seek/idle).
	IdleAfter time.Duration
}

// VideoStats is a snapshot for the status line.
type VideoStats struct {
	Fed       int64
	Dup       int64
	AUs       int64
	Bytes     int64
	Keyframes int64
	LateMs    int64
}

type tsEntry struct {
	ts  uint32
	fed time.Time
}

// Video is an ffmpeg child fed CFR bgr0 frames on stdin and read back as an
// Annex-B H.264 elementary stream on stdout.
type Video struct {
	log  *slog.Logger
	cfg  VideoConfig
	cmd  *exec.Cmd
	in   io.WriteCloser
	tsq  chan tsEntry
	done chan struct{}
	stop sync.Once
	wg   sync.WaitGroup

	frameBytes int

	mu          sync.Mutex
	pending     []byte
	pendingWall time.Time
	hasPending  bool
	spare       []byte
	cur         []byte
	lastArrival time.Time

	fed     atomic.Int64
	dup     atomic.Int64
	aus     atomic.Int64
	bytes   atomic.Int64
	keys    atomic.Int64
	lateMs  atomic.Int64
	dropped atomic.Int64
}

// Args builds the ffmpeg command line. Exposed for logging and tests.
func (cfg VideoConfig) Args() []string {
	c := cfg.Candidate
	p := cfg.Preset
	fps := float64(cfg.FPSNum) / float64(cfg.FPSDen)
	gop := int(fps*2 + 0.5)
	if gop < 2 {
		gop = 2
	}
	kbps := strconv.Itoa(p.Kbps) + "k"
	rate := strconv.Itoa(cfg.FPSNum) + "/" + strconv.Itoa(cfg.FPSDen)

	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error"}
	args = append(args, c.PreInput...)
	args = append(args,
		"-f", "rawvideo", "-pix_fmt", "bgr0",
		"-s", fmt.Sprintf("%dx%d", p.Width, p.Height),
		"-r", rate, "-i", "pipe:0")
	if c.VF != "" {
		args = append(args, "-vf", c.VF)
	} else if c.PixFmt != "" {
		args = append(args, "-pix_fmt", c.PixFmt)
	}
	args = append(args, "-c:v", c.Name)
	args = append(args, c.Args...)
	args = append(args, c.extraArgs(gop)...)
	args = append(args,
		"-profile:v", "main",
		"-bf", "0",
		"-g", strconv.Itoa(gop),
		"-force_key_frames", "expr:gte(t,n_forced*2)",
		"-b:v", kbps, "-maxrate", kbps, "-bufsize", kbps,
		// dump_extra puts SPS/PPS in front of every IDR; h264_metadata puts an
		// access unit delimiter in front of every AU so we can split the stream.
		"-bsf:v", "dump_extra=freq=keyframe,h264_metadata=aud=insert",
		"-f", "h264", "pipe:1")
	return args
}

// NewVideo starts the ffmpeg child and its pacing/parsing goroutines.
func NewVideo(ctx context.Context, log *slog.Logger, cfg VideoConfig) (*Video, error) {
	if cfg.FPSNum <= 0 || cfg.FPSDen <= 0 {
		cfg.FPSNum, cfg.FPSDen = 24000, 1001
	}
	if cfg.IdleAfter <= 0 {
		cfg.IdleAfter = time.Second
	}
	v := &Video{
		log:        log,
		cfg:        cfg,
		tsq:        make(chan tsEntry, 1024),
		done:       make(chan struct{}),
		frameBytes: cfg.Preset.Width * 4 * cfg.Preset.Height,
	}
	args := cfg.Args()
	log.Info("encoder: starting ffmpeg",
		"encoder", cfg.Candidate.Name,
		"preset", cfg.Preset.Name,
		"size", fmt.Sprintf("%dx%d", cfg.Preset.Width, cfg.Preset.Height),
		"fps", fmt.Sprintf("%d/%d", cfg.FPSNum, cfg.FPSDen),
		"kbps", cfg.Preset.Kbps)
	log.Debug("encoder: ffmpeg args", "args", args)

	cmd := exec.Command(cfg.FFmpeg, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}
	v.cmd = cmd
	v.in = stdin

	v.wg.Add(3)
	go func() { defer v.wg.Done(); v.readLoop(stdout) }()
	go func() { defer v.wg.Done(); v.logLoop(stderr) }()
	go func() { defer v.wg.Done(); v.paceLoop(ctx) }()
	return v, nil
}

// Submit hands the newest rendered frame to the encoder. The buffer is copied;
// the caller may reuse it immediately. Frames arriving faster than the output
// frame rate simply replace each other.
func (v *Video) Submit(buf []byte, wall time.Time) {
	if len(buf) != v.frameBytes {
		v.dropped.Add(1)
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
	v.lastArrival = time.Now()
	v.mu.Unlock()
}

// paceLoop feeds ffmpeg exactly one frame per output tick (CFR), duplicating
// the last picture when mpv produced nothing. While idle it drops to 1 fps so a
// paused stream costs almost nothing but still keeps the encoder running.
func (v *Video) paceLoop(ctx context.Context) {
	start := time.Now()
	period := func(n int64) time.Duration {
		return time.Duration(n * int64(time.Second) * int64(v.cfg.FPSDen) / int64(v.cfg.FPSNum))
	}
	timer := time.NewTimer(time.Hour)
	defer timer.Stop()

	var n int64
	var lastFed time.Time
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
		idle := !v.lastArrival.IsZero() && now.Sub(v.lastArrival) > v.cfg.IdleAfter
		v.mu.Unlock()

		if frame == nil {
			continue // nothing rendered yet
		}
		if !fresh {
			if idle && now.Sub(lastFed) < time.Second {
				continue
			}
			v.dup.Add(1)
		}
		ts := now
		if fresh {
			ts = wall
		}
		entry := tsEntry{ts: v.cfg.Stamp(ts), fed: now}
		select {
		case v.tsq <- entry:
		default:
			v.log.Warn("encoder: timestamp queue full; encoder is far behind")
		}
		if _, err := v.in.Write(frame); err != nil {
			select {
			case <-v.done:
			default:
				v.log.Error("encoder: write to ffmpeg failed", "err", err)
			}
			return
		}
		v.fed.Add(1)
		lastFed = now
	}
}

// readLoop parses ffmpeg's Annex-B output into access units and pairs each one
// with the timestamp of the input frame that produced it. B-frames are off and
// the encoder emits one AU per input frame in order, so a plain FIFO is exact.
func (v *Video) readLoop(stdout io.ReadCloser) {
	defer stdout.Close()
	r := bufio.NewReaderSize(stdout, 1<<16)
	buf := make([]byte, 64<<10)
	var pending []byte
	for {
		n, err := r.Read(buf)
		if n > 0 {
			pending = append(pending, buf[:n]...)
			var aus [][]byte
			aus, pending = SplitAccessUnits(pending)
			for _, au := range aus {
				v.deliver(au)
			}
			if len(pending) > MaxPendingBytes {
				v.log.Error("encoder: no access unit delimiters in output; dropping buffer")
				pending = pending[:0]
			}
		}
		if err != nil {
			if err != io.EOF {
				select {
				case <-v.done:
				default:
					v.log.Warn("encoder: read from ffmpeg failed", "err", err)
				}
			}
			return
		}
	}
}

func (v *Video) deliver(au []byte) {
	var e tsEntry
	select {
	case e = <-v.tsq:
	default:
		v.log.Warn("encoder: access unit with no matching input timestamp")
		return
	}
	late := time.Since(e.fed)
	v.aus.Add(1)
	v.bytes.Add(int64(len(au)))
	v.lateMs.Store(late.Milliseconds())
	key := IsKeyframe(au)
	if key {
		v.keys.Add(1)
	}
	if v.cfg.Sink != nil {
		v.cfg.Sink(AccessUnit{Data: au, TS90k: e.ts, Key: key, Late: late})
	}
}

func (v *Video) logLoop(stderr io.ReadCloser) {
	defer stderr.Close()
	sc := bufio.NewScanner(stderr)
	sc.Buffer(make([]byte, 0, 8192), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			continue
		}
		v.log.Warn("ffmpeg", "msg", line)
	}
}

// Stats returns counters for the status line and mpv.state.
func (v *Video) Stats() VideoStats {
	return VideoStats{
		Fed:       v.fed.Load(),
		Dup:       v.dup.Load(),
		AUs:       v.aus.Load(),
		Bytes:     v.bytes.Load(),
		Keyframes: v.keys.Load(),
		LateMs:    v.lateMs.Load(),
	}
}

// Close shuts the child down: EOF on stdin, then wait, then kill if it lingers.
func (v *Video) Close() {
	v.stop.Do(func() { close(v.done) })
	_ = v.in.Close()
	waited := make(chan struct{})
	go func() {
		_ = v.cmd.Wait()
		close(waited)
	}()
	select {
	case <-waited:
	case <-time.After(3 * time.Second):
		v.log.Warn("encoder: ffmpeg did not exit, killing")
		_ = v.cmd.Process.Kill()
		<-waited
	}
	v.wg.Wait()
}
