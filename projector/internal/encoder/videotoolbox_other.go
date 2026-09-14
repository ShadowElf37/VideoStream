//go:build !darwin

package encoder

// The in-process encoder exists only on macOS so far.
//
// NOTE (Linux/Windows): the same trick is available through libx264 via cgo —
// `x264_encoder_encode` takes a per-picture `i_type`, and setting it to
// X264_TYPE_IDR is the equivalent of VideoToolbox's ForceKeyFrame. NVENC has
// `forceIntraRefresh`/`forceIDR` on its per-frame parameters, and VAAPI can be
// driven the same way. None of it is written; the ffmpeg child stays the path
// there, with the two-second GOP floor as the only guarantee a late joiner
// gets, which is what this issue set out to improve and has not yet, off
// macOS.

import (
	"context"
	"errors"
	"log/slog"
)

// VTName is the --encoder value that would select the in-process path.
const VTName = "vt"

// ErrNoInProcessEncoder is returned when --encoder vt is asked for on a
// platform that does not have one.
var ErrNoInProcessEncoder = errors.New("the in-process encoder is macOS-only; use the ffmpeg path (drop --encoder vt)")

// NewVideoToolbox is not available off macOS.
func NewVideoToolbox(context.Context, *slog.Logger, VideoConfig) (Encoder, error) {
	return nil, ErrNoInProcessEncoder
}
