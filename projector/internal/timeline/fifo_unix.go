//go:build unix

package timeline

import (
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// fifo is the read end of mpv's ao-pcm FIFO.
//
// Two details make this well behaved:
//   - the read end is opened O_NONBLOCK so we neither wait for mpv to open the
//     write end nor ever block inside a tick;
//   - we hold a dummy write end open ourselves, so when mpv tears its AO down
//     (track change, end of file) the FIFO never reports EOF and the reader
//     does not have to reopen it.
//
// Reads are *paced*: exactly one 20 ms chunk per 20 ms tick, never ahead. That
// is what slaves mpv to our clock — it blocks on its own write once the pipe is
// full, so the steady-state fill is small and constant instead of growing.
type fifo struct {
	rd   int
	wr   int
	part []byte // carry for a short read
}

// MakeFIFO creates (replacing any stale one) the named pipe mpv writes PCM to.
func MakeFIFO(path string) error {
	_ = os.Remove(path)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := unix.Mkfifo(path, 0o600); err != nil {
		return &os.PathError{Op: "mkfifo", Path: path, Err: err}
	}
	return nil
}

func openFIFO(path string) (*fifo, error) {
	rd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	wr, err := unix.Open(path, unix.O_WRONLY|unix.O_NONBLOCK, 0)
	if err != nil {
		unix.Close(rd)
		return nil, &os.PathError{Op: "open-keepalive", Path: path, Err: err}
	}
	return &fifo{rd: rd, wr: wr, part: make([]byte, 0, ChunkBytes)}, nil
}

func (f *fifo) Close() error {
	unix.Close(f.wr)
	return unix.Close(f.rd)
}

// fill is how many bytes mpv has written that we have not consumed yet: the
// audio lead, and the number we compensate the video timestamps by.
func (f *fifo) fill() int {
	n, err := unix.IoctlGetInt(f.rd, fionread)
	if err != nil {
		return len(f.part)
	}
	return n + len(f.part)
}

// readChunk reads exactly one 20 ms chunk if a whole one is available. It
// returns false without consuming anything when the FIFO is short, which is
// how a pause, a seek or an idle mpv turns into emitted silence.
func (f *fifo) readChunk(dst []int16) bool {
	if f.fill() < ChunkBytes {
		return false
	}
	buf := make([]byte, ChunkBytes)
	got := copy(buf, f.part)
	f.part = f.part[:0]
	for got < ChunkBytes {
		n, err := unix.Read(f.rd, buf[got:])
		if n > 0 {
			got += n
			continue
		}
		if err == unix.EINTR {
			continue
		}
		// Short pipe read: keep the fragment for the next tick rather than
		// losing sample alignment.
		f.part = append(f.part[:0], buf[:got]...)
		return false
	}
	for i := range dst {
		dst[i] = int16(uint16(buf[2*i]) | uint16(buf[2*i+1])<<8)
	}
	return true
}

// discard throws away everything currently buffered; used on seek so pre-seek
// audio never reaches the viewers.
func (f *fifo) discard() int {
	f.part = f.part[:0]
	scratch := make([]byte, 64<<10)
	n := 0
	for {
		k, err := unix.Read(f.rd, scratch)
		if k > 0 {
			n += k
		}
		if err != nil || k <= 0 || k < len(scratch) {
			return n
		}
	}
}
