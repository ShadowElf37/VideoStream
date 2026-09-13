//go:build windows

package timeline

import "errors"

// errNoFIFO reports that the ao=pcm FIFO transport is not implemented on
// Windows yet; a named pipe (\\.\pipe\...) is the intended replacement.
var errNoFIFO = errors.New("timeline: mpv PCM FIFO is not implemented on windows")

type fifo struct{}

// MakeFIFO is not supported on Windows.
func MakeFIFO(string) error { return errNoFIFO }

func openFIFO(string) (*fifo, error) { return nil, errNoFIFO }

func (f *fifo) Close() error           { return nil }
func (f *fifo) fill() int              { return 0 }
func (f *fifo) readChunk([]int16) bool { return false }
func (f *fifo) discard() int           { return 0 }
