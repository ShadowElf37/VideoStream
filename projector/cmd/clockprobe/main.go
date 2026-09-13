// clockprobe is a throwaway experiment (plan Phase 2, day 1-2): can mpv be
// slaved to our clock by reading its untimed ao=pcm output from a FIFO at
// exactly real time, and do rendered frames then arrive in step with the
// audio we consume?
//
// It plays a file with vo=libmpv (software render) and ao=pcm→FIFO, reads the
// FIFO on a 20 ms ticker, and logs once a second: audio consumed, FIFO fill,
// frames rendered, inter-frame interval stats, and mpv's own time-pos. A fixed
// schedule then stalls the reader, reads at 2x, pauses, and seeks, to see how
// mpv reacts to each.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gen2brain/go-mpv"
	"golang.org/x/sys/unix"
)

const (
	sampleRate     = 48000
	channels       = 2
	bytesPerSample = 2
	chunkSamples   = 960 // 20 ms
	chunkBytes     = chunkSamples * channels * bytesPerSample
)

type frameStats struct {
	mu        sync.Mutex
	times     []time.Time
	rendered  atomic.Int64
	renderDur atomic.Int64 // ns, last render call
}

func (s *frameStats) add(t time.Time) {
	s.mu.Lock()
	s.times = append(s.times, t)
	s.mu.Unlock()
	s.rendered.Add(1)
}

// takeSince returns the frame timestamps since t and clears older ones.
func (s *frameStats) takeSince(t time.Time) []time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]time.Time, 0, len(s.times))
	for _, ft := range s.times {
		if ft.After(t) {
			out = append(out, ft)
		}
	}
	s.times = s.times[:0]
	return out
}

func main() {
	file := flag.String("file", "", "media file to play")
	width := flag.Int("w", 1280, "render width")
	height := flag.Int("h", 720, "render height")
	runFor := flag.Duration("for", 40*time.Second, "how long to run")
	flag.Parse()
	if *file == "" {
		log.Fatal("-file is required")
	}

	dir, err := os.MkdirTemp("", "clockprobe")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	fifo := filepath.Join(dir, "audio.pcm")
	if err := unix.Mkfifo(fifo, 0o600); err != nil {
		log.Fatal("mkfifo:", err)
	}

	m := mpv.New()
	opts := map[string]string{
		"vo":                 "libmpv",
		"ao":                 "pcm",
		"ao-pcm-file":        fifo,
		"ao-pcm-waveheader":  "no",
		"audio-format":       "s16",
		"audio-samplerate":   fmt.Sprint(sampleRate),
		"audio-channels":     "stereo",
		"hwdec":              "no",
		"keep-open":          "yes",
		"terminal":           "no",
		"msg-level":          "all=warn",
		"osd-level":          "0",
		"config":             "no",
		"input-default-bindings": "no",
	}
	for k, v := range opts {
		if err := m.SetOptionString(k, v); err != nil {
			log.Fatalf("set option %s=%s: %v", k, v, err)
		}
	}
	if err := m.Initialize(); err != nil {
		log.Fatal("mpv init:", err)
	}
	defer m.TerminateDestroy()

	rc, err := m.NewRenderContextSW()
	if err != nil {
		log.Fatal("render context:", err)
	}
	defer rc.Free()

	stats := &frameStats{}
	wake := make(chan struct{}, 1)
	rc.SetUpdateCallback(func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	})

	// Render goroutine: the only caller of rc.* after setup.
	stride := *width * 4
	buf := make([]byte, stride*(*height))
	go func() {
		for range wake {
			flags := rc.Update()
			if flags&mpv.RenderUpdateFrame == 0 {
				continue
			}
			t0 := time.Now()
			if err := rc.RenderSW(*width, *height, stride, "bgr0", buf); err != nil {
				log.Println("render:", err)
				continue
			}
			stats.renderDur.Store(int64(time.Since(t0)))
			stats.add(t0)
		}
	}()

	// Event loop: observe properties so we can print mpv's own view of time.
	var timePos, audioPts atomic.Value
	timePos.Store(-1.0)
	audioPts.Store(-1.0)
	var paused, seeking atomic.Bool
	_ = m.ObserveProperty(1, "time-pos", mpv.FormatDouble)
	_ = m.ObserveProperty(2, "audio-pts", mpv.FormatDouble)
	_ = m.ObserveProperty(3, "pause", mpv.FormatFlag)
	_ = m.ObserveProperty(4, "seeking", mpv.FormatFlag)
	go func() {
		for {
			ev := m.WaitEvent(0.2)
			if ev == nil {
				continue
			}
			switch ev.EventID {
			case mpv.EventShutdown:
				return
			case mpv.EventPropertyChange:
				p := ev.Property()
				switch ev.ReplyUserdata {
				case 1:
					if v, ok := p.Data.(float64); ok {
						timePos.Store(v)
					}
				case 2:
					if v, ok := p.Data.(float64); ok {
						audioPts.Store(v)
					}
				case 3:
					if v, ok := p.Data.(bool); ok {
						paused.Store(v)
					}
				case 4:
					if v, ok := p.Data.(bool); ok {
						seeking.Store(v)
					}
				}
			case mpv.EventEnd:
				log.Printf("mpv: end-file reason=%v", ev.EndFile().Reason)
			case mpv.EventFileLoaded:
				log.Printf("mpv: file loaded")
			case mpv.EventLogMsg:
				lm := ev.LogMessage()
				log.Printf("mpv[%s] %s: %s", lm.Level, lm.Prefix, lm.Text)
			}
		}
	}()

	// Open the FIFO read end non-blocking so it doesn't wait for mpv's writer.
	fd, err := unix.Open(fifo, unix.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		log.Fatal("open fifo:", err)
	}
	defer unix.Close(fd)

	if err := m.Command([]string{"loadfile", *file}); err != nil {
		log.Fatal("loadfile:", err)
	}

	// Reader schedule: phase name -> tick interval (0 = don't read).
	type phase struct {
		at   time.Duration
		name string
		tick time.Duration
		cmd  []string
	}
	schedule := []phase{
		{0, "realtime", 20 * time.Millisecond, nil},
		{10 * time.Second, "STALL (no reads)", 0, nil},
		{13 * time.Second, "realtime", 20 * time.Millisecond, nil},
		{20 * time.Second, "2x speed reads", 10 * time.Millisecond, nil},
		{25 * time.Second, "realtime", 20 * time.Millisecond, nil},
		{27 * time.Second, "realtime + mpv pause", 20 * time.Millisecond, []string{"set_property", "pause", "yes"}},
		{30 * time.Second, "realtime + unpause", 20 * time.Millisecond, []string{"set_property", "pause", "no"}},
		{33 * time.Second, "realtime + seek to 60s", 20 * time.Millisecond, []string{"seek", "60", "absolute"}},
	}

	start := time.Now()
	var samplesRead int64
	var underruns int64
	chunk := make([]byte, chunkBytes)
	curPhase := 0
	var tick time.Duration = 20 * time.Millisecond
	nextTick := start
	lastReport := start
	deadline := start.Add(*runFor)

	fill := func() int {
		n, err := unix.IoctlGetInt(fd, fionread)
		if err != nil {
			return -1
		}
		return n
	}
	// readChunk reads exactly one 20 ms chunk if available; returns false on underrun.
	readChunk := func() bool {
		if fill() < chunkBytes {
			return false
		}
		got := 0
		for got < chunkBytes {
			n, err := unix.Read(fd, chunk[got:])
			if err != nil {
				if err == unix.EAGAIN {
					return false
				}
				log.Println("read:", err)
				return false
			}
			got += n
		}
		samplesRead += chunkSamples
		return true
	}

	log.Printf("probe: %s -> %dx%d, %s", *file, *width, *height, *runFor)
	for now := time.Now(); now.Before(deadline); now = time.Now() {
		el := now.Sub(start)
		// Advance phases.
		if curPhase+1 < len(schedule) && el >= schedule[curPhase+1].at {
			curPhase++
			p := schedule[curPhase]
			tick = p.tick
			nextTick = now
			log.Printf("===== t=%4.1fs phase: %s", el.Seconds(), p.name)
			if p.cmd != nil {
				if err := m.Command(p.cmd); err != nil {
					log.Println("cmd:", err)
				}
			}
		}
		if tick > 0 && !now.Before(nextTick) {
			if !readChunk() {
				underruns++
			}
			nextTick = nextTick.Add(tick)
			if now.Sub(nextTick) > 200*time.Millisecond { // we fell way behind; resync
				nextTick = now
			}
		}
		if now.Sub(lastReport) >= time.Second {
			frames := stats.takeSince(lastReport)
			ivals := make([]float64, 0, len(frames))
			for i := 1; i < len(frames); i++ {
				ivals = append(ivals, frames[i].Sub(frames[i-1]).Seconds()*1000)
			}
			sort.Float64s(ivals)
			var min, med, max float64
			bursty := 0
			if len(ivals) > 0 {
				min, med, max = ivals[0], ivals[len(ivals)/2], ivals[len(ivals)-1]
				for _, v := range ivals {
					if v < 5 {
						bursty++
					}
				}
			}
			f := fill()
			log.Printf("t=%4.1fs audio=%6.2fs fifo=%6dB(%3.0fms) frames/s=%2d ivl(ms) min=%5.1f med=%5.1f max=%6.1f burst<5ms=%2d underrun=%3d render=%.1fms mpv:time-pos=%6.2f audio-pts=%6.2f paused=%v seeking=%v",
				el.Seconds(), float64(samplesRead)/sampleRate, f, float64(f)/float64(sampleRate*channels*bytesPerSample)*1000,
				len(frames), min, med, max, bursty, underruns,
				float64(stats.renderDur.Load())/1e6,
				timePos.Load().(float64), audioPts.Load().(float64), paused.Load(), seeking.Load())
			lastReport = now
		}
		sleepFor := time.Millisecond
		if tick > 0 {
			if d := time.Until(nextTick); d < sleepFor {
				sleepFor = d
			}
		}
		if sleepFor > 0 {
			time.Sleep(sleepFor)
		}
	}
	log.Printf("done: audio consumed %.2fs, frames rendered %d, underruns %d", float64(samplesRead)/sampleRate, stats.rendered.Load(), underruns)
}
