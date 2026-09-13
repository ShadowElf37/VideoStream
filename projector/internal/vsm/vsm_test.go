package vsm

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(Header{
		Title: "Test", Width: 1920, Height: 1080,
		FPSNum: 24000, FPSDen: 1001,
		AudioRate: 48000, AudioChannels: 2,
	}, dir)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}

	// 48 frames at 24 fps with an IDR every 24, interleaved with 20 ms audio.
	var wantVideo [][]byte
	for i := 0; i < 48; i++ {
		au := bytes.Repeat([]byte{byte(i)}, 100+i)
		wantVideo = append(wantVideo, au)
		if err := w.WriteVideo(au, i%24 == 0); err != nil {
			t.Fatalf("WriteVideo(%d): %v", i, err)
		}
		if err := w.WriteAudio([]byte{0xfc, byte(i)}, 960); err != nil {
			t.Fatalf("WriteAudio(%d): %v", i, err)
		}
	}
	path := filepath.Join(dir, "out.vsm")
	if err := w.Close(path); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	hdr := r.Header()
	if hdr.FPSNum != 24000 || hdr.FPSDen != 1001 {
		t.Errorf("frame rate = %d/%d, want 24000/1001", hdr.FPSNum, hdr.FPSDen)
	}
	if len(hdr.Keyframes) != 2 {
		t.Fatalf("keyframes = %d, want 2", len(hdr.Keyframes))
	}
	if hdr.DurationMS <= 0 {
		t.Errorf("DurationMS = %d, want > 0", hdr.DurationMS)
	}

	var gotVideo [][]byte
	var audio int
	for {
		rec, err := r.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		switch rec.Kind {
		case KindVideo:
			gotVideo = append(gotVideo, append([]byte(nil), rec.Data...))
		case KindAudio:
			audio++
			if rec.Samples != 960 {
				t.Errorf("audio samples = %d, want 960", rec.Samples)
			}
		}
	}
	if audio != 48 {
		t.Errorf("audio packets = %d, want 48", audio)
	}
	if len(gotVideo) != len(wantVideo) {
		t.Fatalf("video records = %d, want %d", len(gotVideo), len(wantVideo))
	}
	for i := range wantVideo {
		if !bytes.Equal(gotVideo[i], wantVideo[i]) {
			t.Fatalf("video record %d differs", i)
		}
	}
}

// The keyframe offsets are written before the header length is known, so the
// fixup in Close is the part most likely to be subtly wrong. Seeking proves it:
// a bad offset lands mid-record and fails to parse.
func TestSeekLandsOnKeyframe(t *testing.T) {
	dir := t.TempDir()
	w, err := NewWriter(Header{Width: 640, Height: 360, FPSNum: 24, FPSDen: 1}, dir)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	for i := 0; i < 240; i++ { // 10 s, IDR every 48 frames (2 s)
		if err := w.WriteVideo(bytes.Repeat([]byte{byte(i)}, 64), i%48 == 0); err != nil {
			t.Fatal(err)
		}
		if err := w.WriteAudio([]byte{0xfc}, 2000); err != nil {
			t.Fatal(err)
		}
	}
	path := filepath.Join(dir, "seek.vsm")
	if err := w.Close(path); err != nil {
		t.Fatalf("Close: %v", err)
	}

	r, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer r.Close()

	// 5 s in: the keyframe at 4 s (frame 96) is the one at or before it.
	k, err := r.SeekMS(5000)
	if err != nil {
		t.Fatalf("SeekMS: %v", err)
	}
	if k.Frame != 96 {
		t.Errorf("landed on frame %d, want 96", k.Frame)
	}
	rec, err := r.Next()
	if err != nil {
		t.Fatalf("Next after seek: %v", err)
	}
	if rec.Kind != KindVideo || rec.Frame != 96 {
		t.Fatalf("after seek got kind=%d frame=%d, want a video record at frame 96", rec.Kind, rec.Frame)
	}
	// Position must resume from the keyframe, not from zero, or RTP
	// timestamps would jump backwards across a seek.
	if _, samples := r.Position(); samples != k.Samples {
		t.Errorf("samples after seek = %d, want %d", samples, k.Samples)
	}

	// Seeking before the first keyframe must clamp to it rather than fail.
	if k, err := r.SeekMS(-5000); err != nil || k.Frame != 0 {
		t.Errorf("SeekMS(-5000) = %+v, %v; want frame 0", k, err)
	}
}

func TestRejectsForeignFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope.vsm")
	if err := os.WriteFile(path, []byte("not a vsm file at all"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Error("Open accepted a file that is not a vsm")
	}
}

func TestOpusSamples(t *testing.T) {
	cases := []struct {
		name string
		toc  []byte
		want int
	}{
		// config 15 is CELT 20 ms; code 0 is one frame -> 960 samples at 48k.
		{"celt 20ms single", []byte{15<<3 | 0}, 960},
		{"celt 20ms two frames", []byte{15<<3 | 1}, 1920},
		{"celt 10ms single", []byte{14<<3 | 0}, 480},
		{"silk 20ms single", []byte{1<<3 | 0}, 960},
		{"code 3 three frames", []byte{15<<3 | 3, 3}, 2880},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := opusSamples(tc.toc)
			if err != nil {
				t.Fatalf("opusSamples: %v", err)
			}
			if got != tc.want {
				t.Errorf("opusSamples = %d, want %d", got, tc.want)
			}
		})
	}
	if _, err := opusSamples(nil); err == nil {
		t.Error("opusSamples(nil) should fail")
	}
}
