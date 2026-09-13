//go:build unix

package timeline

import (
	"context"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// fifo is the read end of mpv's ao-pcm FIFO.
//
// Two tricks make this well behaved:
//   - the read end is opened O_NONBLOCK so we do not wait for mpv to open the
//     write end, and we poll(2) with a timeout instead of blocking, which keeps
//     the goroutine cancellable without busy-looping;
//   - we hold a dummy write end open ourselves, so when mpv tears its AO down
//     (track change, end of file) the FIFO never reports EOF and the reader
//     does not have to reopen it.
type fifo struct {
	rd int
	wr int
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
	return &fifo{rd: rd, wr: wr}, nil
}

func (f *fifo) Close() error {
	unix.Close(f.wr)
	return unix.Close(f.rd)
}

// discard reads and throws away everything currently buffered in the FIFO.
func (f *fifo) discard(scratch []byte) int {
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

// readLoop drains the FIFO as data arrives and cuts it into 20 ms chunks.
// It stops reading when the queue is nearly full so mpv feels back-pressure
// and slows down, which is exactly how mpv stays slaved to real time.
func (t *Timeline) readLoop(ctx context.Context, f *fifo) {
	raw := make([]byte, ChunkBytes*4)
	part := make([]byte, 0, ChunkBytes*2)
	pfd := []unix.PollFd{{Fd: int32(f.rd), Events: unix.POLLIN}}

	for {
		select {
		case <-ctx.Done():
			return
		case <-t.flushReq:
			n := f.discard(raw)
			part = part[:0]
			q := t.drainQueue()
			t.flushes.Add(1)
			t.em.Reset()
			t.log.Debug("timeline: flushed on seek", "fifoBytes", n, "queuedChunks", q)
		default:
		}

		if len(t.q) >= cap(t.q)-2 {
			// Queue is full: stop reading. mpv blocks on its write and stalls,
			// which is the back-pressure that keeps it on our clock.
			if _, err := unix.Poll(pfd, 5); err != nil && err != unix.EINTR {
				t.log.Warn("timeline: poll failed", "err", err)
				return
			}
			continue
		}

		n, err := unix.Poll(pfd, 200)
		if err != nil {
			if err == unix.EINTR {
				continue
			}
			t.log.Warn("timeline: poll failed", "err", err)
			return
		}
		if n == 0 {
			continue
		}

		k, err := unix.Read(f.rd, raw)
		if err != nil {
			if err == unix.EAGAIN || err == unix.EINTR {
				continue
			}
			t.log.Warn("timeline: fifo read failed", "err", err)
			return
		}
		if k <= 0 {
			continue
		}
		t.read.Add(int64(k))
		part = append(part, raw[:k]...)
		for len(part) >= ChunkBytes {
			pcm := make([]int16, ChunkFrames)
			b := part[:ChunkBytes]
			for i := range pcm {
				pcm[i] = int16(uint16(b[2*i]) | uint16(b[2*i+1])<<8)
			}
			part = append(part[:0], part[ChunkBytes:]...)
			select {
			case t.q <- pcm:
			default:
				// Should not happen: the fill check above leaves headroom.
				t.log.Warn("timeline: chunk queue overflow, dropping")
			}
		}
	}
}
