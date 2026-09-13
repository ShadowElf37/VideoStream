package encoder

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Candidate is one H.264 encoder the projector knows how to drive.
type Candidate struct {
	Name string
	// PreInput goes before -i (hardware device selection).
	PreInput []string
	// Args are the encoder-specific options after -c:v <name>.
	Args []string
	// PixFmt is the pixel format requested before -c:v; empty means "use VF".
	PixFmt string
	// VF is an optional filter chain (hardware upload paths need one).
	VF string
}

// Candidates lists the encoders to try, best first, for the current OS.
func Candidates() []Candidate {
	sw := Candidate{
		Name:   "libx264",
		Args:   []string{"-preset", "veryfast", "-tune", "zerolatency"},
		PixFmt: "yuv420p",
	}
	switch runtime.GOOS {
	case "darwin":
		return []Candidate{
			{
				Name:   "h264_videotoolbox",
				Args:   []string{"-realtime", "1", "-prio_speed", "1", "-allow_sw", "1"},
				PixFmt: "nv12",
			},
			sw,
		}
	case "linux":
		return []Candidate{
			{
				Name:   "h264_nvenc",
				Args:   []string{"-preset", "p4", "-tune", "hq", "-rc", "vbr", "-forced-idr", "1", "-no-scenecut", "1"},
				PixFmt: "nv12",
			},
			{
				Name:     "h264_vaapi",
				PreInput: []string{"-vaapi_device", "/dev/dri/renderD128"},
				Args:     []string{"-rc_mode", "VBR", "-idr_interval", "0"},
				VF:       "format=nv12,hwupload",
			},
			{
				Name:   "h264_qsv",
				Args:   []string{"-look_ahead", "0"},
				PixFmt: "nv12",
			},
			sw,
		}
	case "windows":
		return []Candidate{
			{
				Name:   "h264_nvenc",
				Args:   []string{"-preset", "p4", "-tune", "hq", "-rc", "vbr", "-forced-idr", "1", "-no-scenecut", "1"},
				PixFmt: "nv12",
			},
			{
				Name:   "h264_qsv",
				Args:   []string{"-look_ahead", "0"},
				PixFmt: "nv12",
			},
			{
				Name:   "h264_amf",
				Args:   []string{"-rc", "vbr_peak"},
				PixFmt: "nv12",
			},
			sw,
		}
	default:
		return []Candidate{sw}
	}
}

// libx264 needs its GOP settings through -x264-params to make them stick.
func (c Candidate) extraArgs(gop int) []string {
	if c.Name != "libx264" {
		return nil
	}
	g := strconv.Itoa(gop)
	return []string{"-x264-params", "keyint=" + g + ":min-keyint=" + g + ":scenecut=0:bframes=0"}
}

var (
	probeOnce   sync.Once
	probeResult Candidate
	probeErr    error
)

// Probe picks the best working encoder, validating each candidate with a
// one-second synthetic encode. The result is cached for the process.
// A non-empty override forces that encoder by name (still validated).
func Probe(ctx context.Context, log *slog.Logger, ffmpeg, override string) (Candidate, error) {
	probeOnce.Do(func() {
		cands := Candidates()
		if override != "" {
			var filtered []Candidate
			for _, c := range cands {
				if c.Name == override {
					filtered = append(filtered, c)
				}
			}
			if len(filtered) == 0 {
				filtered = []Candidate{{Name: override, PixFmt: "nv12"}}
			}
			cands = filtered
		}
		avail := availableEncoders(ctx, ffmpeg)
		for _, c := range cands {
			if avail != nil && !avail[c.Name] {
				log.Debug("encoder: not built into ffmpeg", "encoder", c.Name)
				continue
			}
			start := time.Now()
			if err := validate(ctx, ffmpeg, c); err != nil {
				log.Info("encoder: candidate rejected", "encoder", c.Name, "err", err)
				continue
			}
			log.Info("encoder: selected", "encoder", c.Name, "probeMs", time.Since(start).Milliseconds())
			probeResult = c
			return
		}
		probeErr = fmt.Errorf("no usable H.264 encoder found")
	})
	return probeResult, probeErr
}

func availableEncoders(ctx context.Context, ffmpeg string) map[string]bool {
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, ffmpeg, "-hide_banner", "-loglevel", "error", "-encoders").Output()
	if err != nil {
		return nil
	}
	set := map[string]bool{}
	for _, line := range strings.Split(string(out), "\n") {
		f := strings.Fields(line)
		if len(f) >= 2 && strings.HasPrefix(f[0], "V") {
			set[f[1]] = true
		}
	}
	if len(set) == 0 {
		return nil
	}
	return set
}

// validate runs a 1 s testsrc2 encode to confirm the encoder actually works on
// this machine (drivers, permissions, headless GPUs).
func validate(ctx context.Context, ffmpeg string, c Candidate) error {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-nostdin", "-loglevel", "error"}
	args = append(args, c.PreInput...)
	args = append(args, "-f", "lavfi", "-i", "testsrc2=size=640x360:rate=30", "-t", "1")
	if c.VF != "" {
		args = append(args, "-vf", c.VF)
	} else if c.PixFmt != "" {
		args = append(args, "-pix_fmt", c.PixFmt)
	}
	args = append(args, "-c:v", c.Name)
	args = append(args, c.Args...)
	args = append(args, c.extraArgs(60)...)
	args = append(args, "-profile:v", "main", "-bf", "0", "-g", "60", "-b:v", "2000k", "-f", "null", "-")
	cmd := exec.CommandContext(cctx, ffmpeg, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return fmt.Errorf("%w: %s", err, msg)
	}
	return nil
}
