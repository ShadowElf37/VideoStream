// Command projector is the host-side mpv streamer: it embeds libmpv, renders
// its output into memory, encodes it, and publishes it into a LiveKit room as
// the "Projector" participant, taking mpv commands from the host's web UI over
// a data channel.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	lksdk "github.com/livekit/server-sdk-go/v2"

	"github.com/ShadowElf37/VideoStream/projector/internal/control"
	"github.com/ShadowElf37/VideoStream/projector/internal/encoder"
	"github.com/ShadowElf37/VideoStream/projector/internal/mpvhost"
	"github.com/ShadowElf37/VideoStream/projector/internal/publish"
	"github.com/ShadowElf37/VideoStream/projector/internal/timeline"
	"github.com/ShadowElf37/VideoStream/proto"
)

type stringList []string

func (s *stringList) String() string     { return strings.Join(*s, ",") }
func (s *stringList) Set(v string) error { *s = append(*s, v); return nil }

func main() {
	var roots stringList
	roomLink := flag.String("room", "", "projector link (https://host/r/<id>?p=<projectorKey>)")
	lkURL := flag.String("url", "", "LiveKit websocket URL (dev; use with --token)")
	lkToken := flag.String("token", "", "LiveKit join token (dev; use with --url)")
	flag.Var(&roots, "media-root", "directory the host UI may browse and load from (repeatable)")
	presetName := flag.String("preset", proto.Preset1080p, "quality preset: 1080p-high|1080p|720p|540p")
	maxPreset := flag.String("max-preset", proto.Preset1080pHigh, "highest preset the host may select")
	fpsFlag := flag.Float64("fps", 0, "output frame rate; 0 = follow the source")
	encName := flag.String("encoder", "", "force an ffmpeg H.264 encoder instead of probing")
	ffmpegPath := flag.String("ffmpeg", "ffmpeg", "path to the ffmpeg binary")
	mpvConfigDir := flag.String("mpv-config-dir", defaultMpvConfigDir(), "mpv config dir (mpv.conf, fonts, scripts)")
	ipcSocket := flag.String("ipc-socket", defaultIPCSocket(), "mpv JSON IPC socket path")
	logLevel := flag.String("log-level", "info", "debug|info|warn|error")
	flag.Parse()

	log := newLogger(*logLevel)
	if err := run(log, runOpts{
		roomLink:  *roomLink,
		lkURL:     *lkURL,
		lkToken:   *lkToken,
		roots:     roots,
		preset:    *presetName,
		maxPreset: *maxPreset,
		fps:       *fpsFlag,
		encoder:   *encName,
		ffmpeg:    *ffmpegPath,
		mpvConfig: *mpvConfigDir,
		ipcSocket: *ipcSocket,
		file:      flag.Arg(0),
	}); err != nil {
		log.Error("projector failed", "err", err)
		os.Exit(1)
	}
}

type runOpts struct {
	roomLink  string
	lkURL     string
	lkToken   string
	roots     []string
	preset    string
	maxPreset string
	fps       float64
	encoder   string
	ffmpeg    string
	mpvConfig string
	ipcSocket string
	file      string
}

func newLogger(level string) *slog.Logger {
	var l slog.Level
	if err := l.UnmarshalText([]byte(level)); err != nil {
		l = slog.LevelInfo
	}
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: l}))
}

func defaultMpvConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "mpv")
}

func defaultIPCSocket() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".cache", "videostream", "mpv.sock")
}

func run(log *slog.Logger, o runOpts) error {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	preset, err := encoder.LookupPreset(o.preset)
	if err != nil {
		return err
	}
	if encoder.PresetRank(o.maxPreset) < 0 {
		return fmt.Errorf("unknown --max-preset %q", o.maxPreset)
	}
	if encoder.PresetRank(preset.Name) < encoder.PresetRank(o.maxPreset) {
		return fmt.Errorf("--preset %q is above --max-preset %q", preset.Name, o.maxPreset)
	}

	// Media roots: explicit flags, else the directory of the positional file,
	// else ~/Videos.
	rootArgs := o.roots
	if len(rootArgs) == 0 {
		switch {
		case o.file != "" && !control.IsURL(o.file):
			rootArgs = []string{filepath.Dir(o.file)}
		default:
			if home, err := os.UserHomeDir(); err == nil {
				rootArgs = []string{filepath.Join(home, "Videos")}
			}
		}
	}
	roots, err := control.NormalizeRoots(rootArgs)
	if err != nil {
		log.Warn("media roots", "err", err)
	}
	if len(roots) == 0 {
		return fmt.Errorf("no usable --media-root")
	}
	log.Info("media roots", "roots", roots)

	wsURL, token := o.lkURL, o.lkToken
	// Room settings come with the token. With --url/--token there is no app
	// server to ask, so the zero value stands until the first `settings`
	// broadcast arrives.
	var settings proto.RoomSettings
	if o.roomLink != "" {
		wsURL, token, settings, err = fetchToken(ctx, o.roomLink)
		if err != nil {
			return err
		}
	}
	if wsURL == "" || token == "" {
		return errors.New("need --room, or both --url and --token")
	}

	cand, err := encoder.Probe(ctx, log, o.ffmpeg, o.encoder)
	if err != nil {
		return err
	}

	// mpv writes PCM into this FIFO; the timeline drains it.
	tmp, err := os.MkdirTemp("", "projector")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)
	fifo := filepath.Join(tmp, "movie.pcm")
	if err := timeline.MakeFIFO(fifo); err != nil {
		return err
	}
	if o.ipcSocket != "" {
		if err := os.MkdirAll(filepath.Dir(o.ipcSocket), 0o700); err != nil {
			log.Warn("ipc socket dir", "err", err)
		}
		_ = os.Remove(o.ipcSocket)
	}

	host, err := mpvhost.New(log, mpvhost.Config{
		FIFOPath:  fifo,
		ConfigDir: o.mpvConfig,
		IPCSocket: o.ipcSocket,
		Width:     preset.Width,
		Height:    preset.Height,
	})
	if err != nil {
		return err
	}
	defer host.Close()

	epoch := time.Now()
	tl := timeline.New(log, fifo, epoch)
	if err := tl.Start(ctx); err != nil {
		return err
	}

	p := &projector{
		log:     log,
		opts:    o,
		host:    host,
		tl:      tl,
		cand:    cand,
		preset:  preset,
		fpsNum:  24000,
		fpsDen:  1001,
		started: epoch,
	}
	if o.fps > 0 {
		p.fpsNum, p.fpsDen = timeline.StableRate(o.fps)
		p.fpsPinned = true
	}

	cb := lksdk.NewRoomCallback()
	cb.OnDisconnected = func() { log.Warn("livekit: disconnected") }
	cb.OnReconnecting = func() { log.Warn("livekit: reconnecting") }
	cb.OnReconnected = func() {
		log.Info("livekit: reconnected; republishing tracks")
		if err := p.pub.Republish(); err != nil {
			log.Error("livekit: republish failed", "err", err)
		}
	}
	cb.OnDataPacket = func(data lksdk.DataPacket, params lksdk.DataReceiveParams) {
		p.ctl.OnDataPacket(data, params)
	}

	pub, err := publish.Connect(log, wsURL, token, cb)
	if err != nil {
		return err
	}
	p.pub = pub
	defer pub.Disconnect()
	log.Info("livekit: joined", "identity", pub.Identity(), "url", wsURL)

	if err := pub.PublishTracks(preset.Width, preset.Height); err != nil {
		return err
	}

	audioEnc, err := encoder.NewAudio(encoder.AudioBitrate)
	if err != nil {
		return err
	}
	tl.SetSink(func(f timeline.AudioFrame) {
		payload, err := audioEnc.Encode(f.PCM)
		if err != nil {
			log.Warn("opus encode failed", "err", err)
			return
		}
		if err := pub.WriteAudio(payload, f.TS48k); err != nil {
			log.Warn("audio write failed", "err", err)
		}
	})

	p.ctl = control.New(control.Deps{
		Log:        log,
		Host:       host,
		Pub:        pub,
		Roots:      roots,
		MaxPreset:  o.maxPreset,
		SetQuality: p.setQuality,
		State:      p.state,
		OnEvent:    p.onEvent,

		AnyoneCanPause: settings.AnyoneCanPause,
	})

	if err := p.rebuildEncoder(ctx); err != nil {
		return err
	}
	host.SetFrameSink(func(f mpvhost.Frame) {
		if v := p.video.Load(); v != nil {
			(*v).Submit(f.Buf, f.Wall)
		}
	})

	go p.ctl.Run(ctx)
	go p.watchFPS(ctx)
	go p.statusLoop(ctx)

	if o.file != "" {
		target := o.file
		if !control.IsURL(target) {
			if target, err = control.Resolve(roots, target); err != nil {
				return err
			}
		}
		if _, err := host.Command([]any{"loadfile", target, "replace"}); err != nil {
			return err
		}
		log.Info("loading", "target", target)
	}

	<-ctx.Done()
	log.Info("shutting down")
	if v := p.video.Load(); v != nil {
		(*v).Close()
	}
	return nil
}

type projector struct {
	log  *slog.Logger
	opts runOpts
	host *mpvhost.Host
	tl   *timeline.Timeline
	pub  *publish.Publisher
	ctl  *control.Controller

	cand    encoder.Candidate
	started time.Time

	mu        sync.Mutex
	preset    encoder.Preset
	fpsNum    int
	fpsDen    int
	fpsPinned bool

	video atomic.Pointer[*encoder.Video]
}

// rebuildEncoder tears the ffmpeg child down and starts a fresh one for the
// current preset and frame rate. The LiveKit tracks are untouched, so viewers
// see a quality change rather than a track teardown.
func (p *projector) rebuildEncoder(ctx context.Context) error {
	p.mu.Lock()
	preset, num, den := p.preset, p.fpsNum, p.fpsDen
	p.mu.Unlock()

	p.host.SetOutput(preset.Width, preset.Height)
	v, err := encoder.NewVideo(ctx, p.log, encoder.VideoConfig{
		FFmpeg:    p.opts.ffmpeg,
		Candidate: p.cand,
		Preset:    preset,
		FPSNum:    num,
		FPSDen:    den,
		Stamp:     p.tl.StampVideo,
		Sink: func(au encoder.AccessUnit) {
			if err := p.pub.WriteVideo(au.Data, au.TS90k); err != nil {
				p.log.Warn("video write failed", "err", err)
			}
		},
	})
	if err != nil {
		return err
	}
	old := p.video.Swap(&v)
	if old != nil {
		(*old).Close()
	}
	return nil
}

func (p *projector) setQuality(name string) error {
	preset, err := encoder.LookupPreset(name)
	if err != nil {
		return err
	}
	p.mu.Lock()
	same := p.preset.Name == preset.Name
	p.preset = preset
	p.mu.Unlock()
	if same {
		return nil
	}
	p.log.Info("quality change", "preset", preset.Name)
	return p.rebuildEncoder(context.Background())
}

// watchFPS follows the source frame rate and restarts the encoder when it
// settles on a different stable rational (a new file with a different rate).
func (p *projector) watchFPS(ctx context.Context) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		p.mu.Lock()
		pinned := p.fpsPinned
		curNum, curDen := p.fpsNum, p.fpsDen
		p.mu.Unlock()
		if pinned {
			return
		}
		fps := p.host.ContainerFPS()
		if fps <= 0 {
			continue
		}
		num, den := timeline.StableRate(fps)
		if num == curNum && den == curDen {
			continue
		}
		p.log.Info("source frame rate changed", "fps", fps, "rate", fmt.Sprintf("%d/%d", num, den))
		p.mu.Lock()
		p.fpsNum, p.fpsDen = num, den
		p.mu.Unlock()
		if err := p.rebuildEncoder(ctx); err != nil {
			p.log.Error("encoder rebuild failed", "err", err)
		}
	}
}

func (p *projector) onEvent(ev proto.MpvEvent) {
	if ev.Type == "seek" {
		// Whatever is still in the FIFO is pre-seek audio: throw it away.
		p.tl.Flush()
	}
}

func (p *projector) state() proto.MpvState {
	st := p.host.State()
	p.mu.Lock()
	preset, num, den := p.preset, p.fpsNum, p.fpsDen
	p.mu.Unlock()
	st.Encoder = p.cand.Name
	st.Preset = preset.Name
	st.BitrateKbps = preset.Kbps
	st.FPS = float64(num) / float64(den)
	st.Width, st.Height = preset.Width, preset.Height
	if v := p.video.Load(); v != nil {
		st.LateMs = int((*v).Stats().LateMs)
	}
	ps := p.pub.Stats()
	st.PLI, st.NACK = ps.PLI, ps.NACK
	return st
}

func (p *projector) statusLoop(ctx context.Context) {
	const period = 5 * time.Second
	t := time.NewTicker(period)
	defer t.Stop()
	var lastBytes int64
	var lastAUs int64
	last := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			elapsed := now.Sub(last)
			last = now
			var vs encoder.VideoStats
			if v := p.video.Load(); v != nil {
				vs = (*v).Stats()
			}
			if vs.Bytes < lastBytes || vs.AUs < lastAUs {
				// The encoder was rebuilt (preset or frame-rate change) and its
				// counters restarted; don't report a negative rate.
				lastBytes, lastAUs = 0, 0
			}
			ts := p.tl.Stats()
			ps := p.pub.Stats()
			kbps := float64(vs.Bytes-lastBytes) * 8 / elapsed.Seconds() / 1000
			fps := float64(vs.AUs-lastAUs) / elapsed.Seconds()
			lastBytes, lastAUs = vs.Bytes, vs.AUs
			p.log.Info("status",
				"pos", mpvhost.FormatTime(p.host.TimePos()),
				"fps", fmt.Sprintf("%.2f", fps),
				"kbps", fmt.Sprintf("%.0f", kbps),
				"lateMs", vs.LateMs,
				"fifoFillB", ts.FillBytes,
				"fifoLeadMs", fmt.Sprintf("%.1f", ts.FillMs),
				"silence", ts.Silence,
				"dupFrames", vs.Dup,
				"idr", vs.Keyframes,
				"renderDrops", p.host.Drops(),
				"lateStamps", ts.LateFrames,
				"pli", ps.PLI,
				"nack", ps.NACK,
			)
		}
	}
}

// fetchToken turns a projector link into a LiveKit URL and join token by
// calling the app server's POST /api/rooms/{id}/token endpoint.
func fetchToken(ctx context.Context, link string) (string, string, proto.RoomSettings, error) {
	var none proto.RoomSettings
	u, err := url.Parse(link)
	if err != nil {
		return "", "", none, fmt.Errorf("bad --room link: %w", err)
	}
	key := u.Query().Get("p")
	if key == "" {
		return "", "", none, errors.New("--room link has no ?p=<projectorKey>")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[len(parts)-2] != "r" {
		return "", "", none, fmt.Errorf("--room link path should look like /r/<roomId>, got %q", u.Path)
	}
	roomID := parts[len(parts)-1]

	body, _ := json.Marshal(proto.TokenRequest{Name: "Projector", ProjectorKey: key})
	endpoint := (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/api/rooms/" + roomID + "/token"}).String()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", "", none, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", none, fmt.Errorf("token request: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", "", none, fmt.Errorf("token request: %s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var tr proto.TokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return "", "", none, fmt.Errorf("token response: %w", err)
	}
	if tr.Token == "" || tr.URL == "" {
		return "", "", none, errors.New("token response missing token or url")
	}
	return tr.URL, tr.Token, tr.Settings, nil
}
