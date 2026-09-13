package vsm

import (
	"bufio"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Record is one item from the stream. Data aliases an internal buffer that the
// next Read overwrites, so copy it if it has to outlive the call.
type Record struct {
	Kind Kind
	Data []byte
	// Frame is the video frame index; Samples the audio packet's sample count.
	Frame   int
	Samples int
}

// Reader streams records out of a .vsm and can jump to a keyframe.
type Reader struct {
	f   *os.File
	br  *bufio.Reader
	hdr Header
	buf []byte

	// Position, maintained across Seek so timestamps stay absolute.
	frame   int
	samples int64
}

// Open reads the header and leaves the reader positioned at the first record.
func Open(path string) (*Reader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	r := &Reader{f: f, br: bufio.NewReaderSize(f, 1<<20)}
	if err := r.readHeader(); err != nil {
		f.Close()
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return r, nil
}

func (r *Reader) readHeader() error {
	var magic [4]byte
	if _, err := io.ReadFull(r.br, magic[:]); err != nil {
		return fmt.Errorf("reading magic: %w", err)
	}
	if string(magic[:]) != Magic {
		return fmt.Errorf("not a vsm file (magic %q)", magic[:])
	}
	var n [4]byte
	if _, err := io.ReadFull(r.br, n[:]); err != nil {
		return fmt.Errorf("reading header length: %w", err)
	}
	length := binary.LittleEndian.Uint32(n[:])
	if length == 0 || length > MaxRecordLen {
		return fmt.Errorf("implausible header length %d", length)
	}
	blob := make([]byte, length)
	if _, err := io.ReadFull(r.br, blob); err != nil {
		return fmt.Errorf("reading header: %w", err)
	}
	if err := json.Unmarshal(blob, &r.hdr); err != nil {
		return fmt.Errorf("parsing header: %w", err)
	}
	if r.hdr.Version != Version {
		return fmt.Errorf("unsupported version %d (this build reads %d)", r.hdr.Version, Version)
	}
	if r.hdr.FPSNum <= 0 || r.hdr.FPSDen <= 0 {
		return fmt.Errorf("header has no usable frame rate (%d/%d)", r.hdr.FPSNum, r.hdr.FPSDen)
	}
	return nil
}

// Header returns the stream description.
func (r *Reader) Header() Header { return r.hdr }

// Next reads the next record. It returns io.EOF at the end of the stream.
func (r *Reader) Next() (Record, error) {
	var h [recordHeaderLen]byte
	if _, err := io.ReadFull(r.br, h[:]); err != nil {
		if err == io.ErrUnexpectedEOF {
			// A truncated record at the tail: report it rather than pretending
			// the stream ended cleanly.
			return Record{}, fmt.Errorf("vsm: truncated record header")
		}
		return Record{}, err
	}
	kind, n, aux := parseRecordHeader(h[:])
	if n > MaxRecordLen {
		return Record{}, fmt.Errorf("vsm: record of %d bytes exceeds the limit", n)
	}
	if cap(r.buf) < int(n) {
		r.buf = make([]byte, n)
	}
	payload := r.buf[:n]
	if _, err := io.ReadFull(r.br, payload); err != nil {
		return Record{}, fmt.Errorf("vsm: truncated record payload: %w", err)
	}
	rec := Record{Kind: kind, Data: payload}
	switch kind {
	case KindVideo:
		rec.Frame = int(aux)
		r.frame = int(aux) + 1
	case KindAudio:
		rec.Samples = int(aux)
		r.samples += int64(aux)
	default:
		return Record{}, fmt.Errorf("vsm: unknown record kind %d", kind)
	}
	return rec, nil
}

// Position reports the frame index and audio sample count reached so far,
// which the player turns into RTP timestamps.
func (r *Reader) Position() (frame int, samples int64) { return r.frame, r.samples }

// SeekMS moves to the last keyframe at or before ms and returns the keyframe
// it landed on. Seeking is keyframe-granular by construction: the push step
// pins an IDR every 2 s, so the worst case is a 2 s backward nudge, and
// starting anywhere else would hand the decoder frames it cannot decode.
func (r *Reader) SeekMS(ms int64) (Keyframe, error) {
	if len(r.hdr.Keyframes) == 0 {
		return Keyframe{}, fmt.Errorf("vsm: file has no keyframe index")
	}
	target := r.hdr.Keyframes[0]
	for _, k := range r.hdr.Keyframes {
		if k.MS > ms {
			break
		}
		target = k
	}
	if _, err := r.f.Seek(target.Offset, io.SeekStart); err != nil {
		return Keyframe{}, err
	}
	r.br.Reset(r.f)
	r.frame, r.samples = target.Frame, target.Samples
	return target, nil
}

// Close releases the file.
func (r *Reader) Close() error { return r.f.Close() }
