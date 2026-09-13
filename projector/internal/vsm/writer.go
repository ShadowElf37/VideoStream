package vsm

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// Writer builds a .vsm. Records go to a scratch file first because the
// keyframe index is only complete at the end but has to sit in the header at
// the front — where a player can read it without scanning the whole file.
type Writer struct {
	hdr Header

	body     *os.File
	bw       *bufio.Writer
	off      int64 // bytes written to body so far
	frames   int
	samples  int64
	finished bool
}

// NewWriter starts a stream. hdr's Keyframes are ignored and rebuilt from what
// is written.
func NewWriter(hdr Header, scratchDir string) (*Writer, error) {
	if hdr.FPSNum <= 0 || hdr.FPSDen <= 0 {
		return nil, fmt.Errorf("vsm: frame rate must be a positive rational, got %d/%d", hdr.FPSNum, hdr.FPSDen)
	}
	f, err := os.CreateTemp(scratchDir, "vsm-body-*")
	if err != nil {
		return nil, err
	}
	hdr.Version = Version
	hdr.Keyframes = nil
	return &Writer{hdr: hdr, body: f, bw: bufio.NewWriterSize(f, 1<<20)}, nil
}

// VideoPTS is the presentation time of frame i, exact in the frame-rate
// rational so 23.976 does not drift.
func (w *Writer) videoMS(frame int) int64 {
	return int64(frame) * int64(w.hdr.FPSDen) * 1000 / int64(w.hdr.FPSNum)
}

// WriteVideo appends one access unit. key marks an IDR, which becomes a seek
// point. The offset recorded is relative to the body and fixed up in Close
// once the header length is known.
func (w *Writer) WriteVideo(au []byte, key bool) error {
	if len(au) > MaxRecordLen {
		return fmt.Errorf("vsm: access unit of %d bytes exceeds the %d byte limit", len(au), MaxRecordLen)
	}
	if key {
		w.hdr.Keyframes = append(w.hdr.Keyframes, Keyframe{
			MS:      w.videoMS(w.frames),
			Offset:  w.off,
			Frame:   w.frames,
			Samples: w.samples,
		})
	}
	if err := w.record(KindVideo, au, uint32(w.frames)); err != nil {
		return err
	}
	w.frames++
	return nil
}

// WriteAudio appends one Opus packet covering samples 48 kHz samples.
func (w *Writer) WriteAudio(pkt []byte, samples int) error {
	if len(pkt) > MaxRecordLen {
		return fmt.Errorf("vsm: opus packet of %d bytes exceeds the %d byte limit", len(pkt), MaxRecordLen)
	}
	if err := w.record(KindAudio, pkt, uint32(samples)); err != nil {
		return err
	}
	w.samples += int64(samples)
	return nil
}

func (w *Writer) record(kind Kind, payload []byte, aux uint32) error {
	var h [recordHeaderLen]byte
	putRecordHeader(h[:], kind, uint32(len(payload)), aux)
	if _, err := w.bw.Write(h[:]); err != nil {
		return err
	}
	if _, err := w.bw.Write(payload); err != nil {
		return err
	}
	w.off += recordHeaderLen + int64(len(payload))
	return nil
}

// Frames and Samples report progress, for a push tool's status line.
func (w *Writer) Frames() int    { return w.frames }
func (w *Writer) Samples() int64 { return w.samples }

// Close finishes the file at path: header first, then the body, with every
// keyframe offset shifted past the header.
func (w *Writer) Close(path string) (err error) {
	if w.finished {
		return fmt.Errorf("vsm: writer already closed")
	}
	w.finished = true
	defer func() {
		name := w.body.Name()
		w.body.Close()
		os.Remove(name)
	}()

	if err := w.bw.Flush(); err != nil {
		return err
	}
	if w.hdr.DurationMS == 0 {
		w.hdr.DurationMS = w.videoMS(w.frames)
	}

	// The header's own length depends on the offsets it contains, and the
	// offsets depend on the header length. Marshal once to measure, shift, and
	// marshal again — the second pass cannot change the length, because the
	// numbers only grow by a fixed amount and JSON integers of the same digit
	// count keep the same width. Loop to be certain rather than assume it.
	base := int64(0)
	for i := 0; i < 8; i++ {
		shifted := w.hdr
		shifted.Keyframes = make([]Keyframe, len(w.hdr.Keyframes))
		copy(shifted.Keyframes, w.hdr.Keyframes)
		for k := range shifted.Keyframes {
			shifted.Keyframes[k].Offset += base
		}
		blob, err := json.Marshal(&shifted)
		if err != nil {
			return err
		}
		want := int64(len(Magic) + 4 + len(blob))
		if want == base {
			return w.emit(path, blob)
		}
		base = want
	}
	return fmt.Errorf("vsm: header length did not settle")
}

func (w *Writer) emit(path string, header []byte) error {
	tmp := path + ".partial"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	defer os.Remove(tmp) // no-op once the rename below succeeds

	bw := bufio.NewWriterSize(out, 1<<20)
	if _, err := bw.WriteString(Magic); err != nil {
		out.Close()
		return err
	}
	var n [4]byte
	putUint32(n[:], uint32(len(header)))
	if _, err := bw.Write(n[:]); err != nil {
		out.Close()
		return err
	}
	if _, err := bw.Write(header); err != nil {
		out.Close()
		return err
	}
	if _, err := w.body.Seek(0, io.SeekStart); err != nil {
		out.Close()
		return err
	}
	if _, err := io.Copy(bw, w.body); err != nil {
		out.Close()
		return err
	}
	if err := bw.Flush(); err != nil {
		out.Close()
		return err
	}
	// fsync before the rename: a half-written .vsm that looks complete is
	// worse than no file, since the server would happily serve it.
	if err := out.Sync(); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
