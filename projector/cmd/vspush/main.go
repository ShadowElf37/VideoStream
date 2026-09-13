// Command vspush prepares a video for the server and copies it there.
//
// It exists because the server cannot encode. A 4-OCPU Ampere A1 has no GPU,
// and software x264 at 1080p runs at 0.35x real time (veryfast) or 0.97x
// (superfast) — live encoding is not available there at any quality worth
// watching. So the machine holding the files does the work once, and the
// server only serves bytes.
//
// The output is a title directory the app server can serve directly:
//
//	<id>/movie.mp4    H.264 High + AAC-LC, moov first, IDR every ~2s
//	<id>/meta.json    duration, geometry, codecs, chapters, provenance
//
// One ffmpeg pass does all of it: libass renders the subtitles with the fonts
// attached to the source, and they are burned in because nothing downstream
// can render them any more.
//
// Usage:
//
//	vspush --title "Madoka 01" --aid 2 --sid 1 episode.mkv
//	vspush --dest ubuntu@host:/srv/media episode.mkv
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/ShadowElf37/VideoStream/proto"
)

type opts struct {
	in      string
	out     string
	title   string
	aid     int
	sid     int
	kbps    int
	akbps   int
	height  int
	gopSecs float64
	dest    string
	format  string
	ffmpeg  string
	ffprobe string
}

func main() {
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	var o opts
	flag.StringVar(&o.out, "out", "", "output title directory (default: alongside the source)")
	flag.StringVar(&o.title, "title", "", "title viewers see (default: the source file name)")
	flag.IntVar(&o.aid, "aid", 0, "mpv audio track to keep, 1-based (0 = mpv's default)")
	flag.IntVar(&o.sid, "sid", 0, "mpv subtitle track to burn in, 1-based (0 = none)")
	flag.IntVar(&o.kbps, "bitrate", 5000, "target video bitrate in kbps")
	flag.IntVar(&o.akbps, "audio-bitrate", 192, "audio bitrate in kbps; 192 is effectively transparent for music, 96 is plenty for speech")
	flag.IntVar(&o.height, "height", 0, "scale to this height, preserving aspect (0 = keep source)")
	flag.Float64Var(&o.gopSecs, "gop", 2, "seconds between keyframes; also the seek granularity and a late joiner's wait")
	flag.StringVar(&o.dest, "dest", "", "scp destination for the finished title, e.g. user@host:/srv/media")
	flag.StringVar(&o.format, "format", "mp4", "output format: mp4 (played by the browser directly) or vsm (legacy RTP projector)")
	flag.StringVar(&o.ffmpeg, "ffmpeg", "ffmpeg", "ffmpeg binary")
	flag.StringVar(&o.ffprobe, "ffprobe", "ffprobe", "ffprobe binary")
	flag.Parse()

	if flag.NArg() != 1 {
		fmt.Fprintf(os.Stderr, "usage: vspush [flags] <source video>\n\n")
		flag.PrintDefaults()
		os.Exit(2)
	}
	o.in = flag.Arg(0)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, log, o); err != nil {
		if errors.Is(err, context.Canceled) {
			log.Info("cancelled")
			os.Exit(130)
		}
		log.Error("push failed", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger, o opts) error {
	abs, err := filepath.Abs(o.in)
	if err != nil {
		return err
	}
	if _, err := os.Stat(abs); err != nil {
		return err
	}
	if o.title == "" {
		o.title = strings.TrimSuffix(filepath.Base(abs), filepath.Ext(abs))
	}
	id := sanitize(o.title)
	if o.out == "" {
		if o.format == "mp4" {
			o.out = filepath.Join(filepath.Dir(abs), id)
		} else {
			o.out = filepath.Join(filepath.Dir(abs), id+".vsm")
		}
	}
	if o.format != "mp4" && o.format != "vsm" {
		return fmt.Errorf("unknown --format %q (want mp4 or vsm)", o.format)
	}

	start := time.Now()
	// The source frame rate decides the GOP length in frames. Encoding does
	// not change the rate, so probing the source is enough and saves waiting
	// for the transcode to find out.
	src, err := probeMKV(ctx, o.ffprobe, abs)
	if err != nil {
		return fmt.Errorf("probing the source: %w", err)
	}
	fps := float64(src.FPSNum) / float64(src.FPSDen)
	log.Info("source", "size", fmt.Sprintf("%dx%d", src.Width, src.Height),
		"fps", fmt.Sprintf("%.3f", fps),
		"duration", time.Duration(src.DurationMS)*time.Millisecond)

	return buildMP4(ctx, log, o, abs, id, fps, start)
}

// videoEncoder picks the hardware H.264 encoder for the host. Quality at 5
// Mbps is close enough to x264 that the CPU cost is not worth paying, and a
// push that takes three minutes instead of twenty is the difference between
// doing it before a party and not bothering.
func videoEncoder() string {
	switch runtime.GOOS {
	case "darwin":
		return "h264_videotoolbox"
	default:
		// Left deliberately conservative: NVENC/VAAPI need a working device
		// and fail late and confusingly when they are missing.
		return "libx264"
	}
}

type probeResult struct {
	Width, Height  int
	FPSNum, FPSDen int
	DurationMS     int64
}

func probeMKV(ctx context.Context, ffprobe, path string) (probeResult, error) {
	out, err := exec.CommandContext(ctx, ffprobe,
		"-v", "error",
		"-select_streams", "v:0",
		"-show_entries", "stream=width,height,r_frame_rate:format=duration",
		"-of", "json", path).Output()
	if err != nil {
		return probeResult{}, err
	}
	var parsed struct {
		Streams []struct {
			Width      int    `json:"width"`
			Height     int    `json:"height"`
			RFrameRate string `json:"r_frame_rate"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return probeResult{}, err
	}
	if len(parsed.Streams) == 0 {
		return probeResult{}, errors.New("no video stream in the encoded file")
	}
	s := parsed.Streams[0]
	num, den, err := parseRational(s.RFrameRate)
	if err != nil {
		return probeResult{}, err
	}
	res := probeResult{Width: s.Width, Height: s.Height, FPSNum: num, FPSDen: den}
	if secs, err := strconv.ParseFloat(parsed.Format.Duration, 64); err == nil {
		res.DurationMS = int64(secs * 1000)
	}
	return res, nil
}

func parseRational(s string) (int, int, error) {
	a, b, ok := strings.Cut(s, "/")
	if !ok {
		return 0, 0, fmt.Errorf("frame rate %q is not a rational", s)
	}
	num, err := strconv.Atoi(a)
	if err != nil {
		return 0, 0, err
	}
	den, err := strconv.Atoi(b)
	if err != nil {
		return 0, 0, err
	}
	if num <= 0 || den <= 0 {
		return 0, 0, fmt.Errorf("frame rate %q is not positive", s)
	}
	return num, den, nil
}

func upload(ctx context.Context, log *slog.Logger, path, dest string) error {
	log.Info("uploading", "dest", dest)
	cmd := exec.CommandContext(ctx, "scp", "-q", "-r", path, dest)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// sanitize keeps generated file names free of separators and spaces so they
// are painless to scp and to type on the server.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case r == ' ':
			b.WriteRune('_')
		}
	}
	out := strings.Trim(b.String(), "._")
	if out == "" {
		return "media"
	}
	return out
}

// buildMP4 produces a title directory the app server can serve directly:
// movie.mp4 plus meta.json. This is the primary path.
//
// It is one mpv pass and nothing else. The .vsm path needs a second step to
// split the stream into RTP-sized access units; a file the browser fetches
// needs no preparation beyond being a well-formed MP4 with its index at the
// front.
func buildMP4(ctx context.Context, log *slog.Logger, o opts, src, id string, fps float64, start time.Time) error {
	dir := o.out
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	movie := filepath.Join(dir, proto.MovieFileName)

	if err := transcodeMP4(ctx, log, o, src, movie, fps); err != nil {
		return fmt.Errorf("transcode: %w", err)
	}
	log.Info("transcode done", "took", time.Since(start).Round(time.Second))

	probe, err := probeMKV(ctx, o.ffprobe, movie)
	if err != nil {
		return fmt.Errorf("probing the encoded file: %w", err)
	}
	st, err := os.Stat(movie)
	if err != nil {
		return err
	}
	chapters, err := probeChapters(ctx, o.ffprobe, src)
	if err != nil {
		// Chapters are a convenience; a source without them is normal and a
		// probe that fails should not lose the encode.
		log.Warn("could not read chapters", "err", err)
	}

	meta := proto.MediaMeta{
		ID: id, Title: o.title, Source: filepath.Base(src),
		DurationMS: probe.DurationMS,
		Width:      probe.Width, Height: probe.Height,
		FPSNum: probe.FPSNum, FPSDen: probe.FPSDen,
		VideoCodec: "h264", AudioCodec: "aac",
		SizeBytes:  st.Size(),
		AudioTrack: o.aid, SubTrack: o.sid,
		Chapters: chapters,
		PushedAt: time.Now().UTC().Format(time.RFC3339),
	}
	blob, err := json.MarshalIndent(&meta, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, proto.MetaFileName), append(blob, '\n'), 0o644); err != nil {
		return err
	}

	log.Info("built", "id", id, "path", dir,
		"size", fmt.Sprintf("%.1f MiB", float64(st.Size())/(1<<20)),
		"duration", time.Duration(meta.DurationMS)*time.Millisecond,
		"chapters", len(chapters),
		"kbps", int(float64(st.Size())*8/(float64(meta.DurationMS)/1000)/1000))

	if o.dest != "" {
		if err := upload(ctx, log, dir, o.dest); err != nil {
			return fmt.Errorf("upload: %w", err)
		}
	}
	log.Info("done", "total", time.Since(start).Round(time.Second))
	return nil
}

// transcodeMP4 renders subtitles and encodes straight to a browser-playable
// MP4, in one ffmpeg pass.
//
// ffmpeg rather than mpv, which the .vsm path uses, for one concrete reason:
// B-frames. They are legal for a file — the ban existed only because browser
// H.264 over RTP assumes decode order equals display order — but mpv's
// encoding path cannot mux reordered frames, failing with
// "pts (984) < dts (2016) ... Writing packet failed". ffmpeg muxes them
// correctly at the same 8x real time, and using one tool instead of two is
// simpler besides.
//
// This needs an ffmpeg built with libass (Homebrew's `ffmpeg-full`, not the
// slim `ffmpeg`); findSubtitleFFmpeg locates one and says so plainly if there
// is none, because the failure is otherwise "No such filter: 'subtitles'"
// twenty minutes in.
//
// Two codec choices that only became available by leaving RTP behind:
// AAC-LC instead of Opus (Opus in MP4 is not reliably supported across Safari
// and older MSE), and High profile instead of Main advertised as constrained
// baseline.
func transcodeMP4(ctx context.Context, log *slog.Logger, o opts, in, out string, fps float64) error {
	ff, err := findSubtitleFFmpeg(o.ffmpeg)
	if err != nil {
		return err
	}
	gopFrames := int(o.gopSecs*fps + 0.5)
	if gopFrames < 1 {
		gopFrames = 48
	}

	// The subtitles filter takes a filename inside a filter-argument string,
	// where ':', ',', '[' and '\' all mean something. Real filenames are full
	// of them. Linking the source to a plain name in a scratch directory
	// sidesteps the escaping entirely.
	work, err := os.MkdirTemp("", "vspush-sub-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(work)
	linked := filepath.Join(work, "src"+filepath.Ext(in))
	if err := os.Symlink(in, linked); err != nil {
		return fmt.Errorf("linking the source for the subtitle filter: %w", err)
	}

	var filters []string
	if o.sid > 0 {
		// si is 0-based among subtitle streams; --sid is 1-based, as in mpv.
		filters = append(filters, fmt.Sprintf("subtitles=%s:si=%d", filepath.Base(linked), o.sid-1))
	}
	if o.height > 0 {
		filters = append(filters, fmt.Sprintf("scale=-2:%d", o.height))
	}

	audioIdx := 0
	if o.aid > 0 {
		audioIdx = o.aid - 1
	}

	args := []string{
		"-nostdin", "-y", "-v", "error", "-stats",
		"-i", filepath.Base(linked),
		"-map", "0:v:0",
		"-map", fmt.Sprintf("0:a:%d", audioIdx),
	}
	if len(filters) > 0 {
		args = append(args, "-vf", strings.Join(filters, ","))
	}
	enc := videoEncoder()
	args = append(args,
		"-c:v", enc,
		"-profile:v", "high",
		"-g", strconv.Itoa(gopFrames),
		"-bf", "2",
	)
	if enc == "libx264" {
		// Quality-targeted where the encoder supports it: CRF spends bits
		// where they are needed instead of holding a flat bitrate through
		// scenes that do not need it. VideoToolbox has no CRF equivalent.
		args = append(args, "-crf", "20", "-preset", "medium",
			"-maxrate", fmt.Sprintf("%dk", o.kbps), "-bufsize", fmt.Sprintf("%dk", o.kbps*2))
	} else {
		args = append(args, "-b:v", fmt.Sprintf("%dk", o.kbps),
			"-maxrate", fmt.Sprintf("%dk", o.kbps*3/2),
			"-bufsize", fmt.Sprintf("%dk", o.kbps*2))
	}
	args = append(args,
		"-c:a", "aac", "-b:a", fmt.Sprintf("%dk", o.akbps), "-ac", "2", "-ar", "48000",
		// moov before mdat, so the browser can start playing and can seek
		// without first fetching the tail of the file.
		"-movflags", "+faststart",
		out,
	)

	log.Info("transcoding", "ffmpeg", ff, "encoder", enc, "aid", o.aid, "sid", o.sid,
		"kbps", o.kbps, "audioKbps", o.akbps, "gopFrames", gopFrames)
	cmd := exec.CommandContext(ctx, ff, args...)
	cmd.Dir = work // so the bare filename in the filter resolves
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// findSubtitleFFmpeg returns an ffmpeg that can burn subtitles. Homebrew's
// default `ffmpeg` is built without libass and has no `subtitles` filter;
// `ffmpeg-full` has it.
func findSubtitleFFmpeg(preferred string) (string, error) {
	candidates := []string{preferred, "/opt/homebrew/opt/ffmpeg-full/bin/ffmpeg", "ffmpeg-full", "ffmpeg"}
	var tried []string
	for _, c := range candidates {
		if c == "" {
			continue
		}
		path, err := exec.LookPath(c)
		if err != nil {
			continue
		}
		tried = append(tried, path)
		out, err := exec.Command(path, "-hide_banner", "-filters").Output()
		if err == nil && strings.Contains(string(out), " subtitles ") {
			return path, nil
		}
	}
	return "", fmt.Errorf("no ffmpeg with libass found (tried %s); "+
		"subtitles cannot be burned in without one — on macOS: brew install ffmpeg-full, "+
		"or pass --ffmpeg with a build that has the subtitles filter",
		strings.Join(tried, ", "))
}

// probeChapters reads chapter marks from the source. The seek bar already
// draws chapter ticks; nothing could carry them over RTP, so they were always
// empty until now.
func probeChapters(ctx context.Context, ffprobe, path string) ([]proto.MediaChapter, error) {
	out, err := exec.CommandContext(ctx, ffprobe,
		"-v", "error", "-show_chapters", "-of", "json", path).Output()
	if err != nil {
		return nil, err
	}
	var parsed struct {
		Chapters []struct {
			StartTime string `json:"start_time"`
			Tags      struct {
				Title string `json:"title"`
			} `json:"tags"`
		} `json:"chapters"`
	}
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, err
	}
	var chs []proto.MediaChapter
	for _, c := range parsed.Chapters {
		secs, err := strconv.ParseFloat(c.StartTime, 64)
		if err != nil {
			continue
		}
		chs = append(chs, proto.MediaChapter{StartMS: int64(secs * 1000), Title: repairMojibake(c.Tags.Title)})
	}
	return chs, nil
}
