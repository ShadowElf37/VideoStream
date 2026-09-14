package encoder

import "time"

// Encoder is what the projector feeds rendered frames to.
//
// Two implementations, and the difference between them is one method. The
// ffmpeg child (Video) is the portable one and cannot be asked for an IDR —
// there is no way to reach into `-force_key_frames expr:...` and say "now" —
// so its ForceKeyframe does nothing and a late joiner waits out the GOP. The
// in-process VideoToolbox session honours it in one frame time.
type Encoder interface {
	// Submit hands over the newest rendered frame. The buffer is copied.
	Submit(buf []byte, wall time.Time)
	// ForceKeyframe asks for an IDR as soon as possible. Best effort: an
	// implementation that cannot do it does nothing, which is what the GOP
	// floor exists to cover.
	ForceKeyframe()
	Stats() VideoStats
	Close()
}

// ForceKeyframe is the ffmpeg child's answer: it cannot.
//
// The GOP floor is what covers this — a joiner who gets no keyframe out of a
// PLI still gets one within two seconds — and the fact that it is a no-op
// rather than an error is the honest shape, since the caller (an RTCP handler)
// has nothing useful to do about it either way.
func (v *Video) ForceKeyframe() {}
