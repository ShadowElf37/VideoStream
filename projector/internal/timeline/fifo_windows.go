//go:build windows

package timeline

import (
	"context"
	"errors"
)

// errNoFIFO reports that the ao=pcm FIFO transport is not implemented on
// Windows yet; a named pipe (\\.\pipe\...) is the intended replacement.
var errNoFIFO = errors.New("timeline: mpv PCM FIFO is not implemented on windows")

type fifo struct{}

// MakeFIFO is not supported on Windows.
func MakeFIFO(string) error { return errNoFIFO }

func openFIFO(string) (*fifo, error) { return nil, errNoFIFO }

func (f *fifo) Close() error { return nil }

func (t *Timeline) readLoop(ctx context.Context, f *fifo) { <-ctx.Done() }
