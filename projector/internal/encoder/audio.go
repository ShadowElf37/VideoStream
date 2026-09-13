package encoder

import (
	"fmt"

	"github.com/hraban/opus"

	"github.com/ShadowElf37/VideoStream/projector/internal/timeline"
)

// Audio encodes 20 ms stereo PCM chunks to Opus in-process. libopus is fast
// enough that the timeline's emitter goroutine can call it directly.
type Audio struct {
	enc *opus.Encoder
	buf []byte
}

// NewAudio builds a 48 kHz stereo music-tuned encoder with FEC and DTX off:
// the movie track is continuous (we emit silence rather than gaps), and the
// LiveKit publication disables DTX and RED on the wire too.
func NewAudio(bitrate int) (*Audio, error) {
	enc, err := opus.NewEncoder(timeline.SampleRate, timeline.Channels, opus.AppAudio)
	if err != nil {
		return nil, fmt.Errorf("opus encoder: %w", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		return nil, fmt.Errorf("opus bitrate: %w", err)
	}
	if err := enc.SetInBandFEC(false); err != nil {
		return nil, fmt.Errorf("opus fec: %w", err)
	}
	if err := enc.SetDTX(false); err != nil {
		return nil, fmt.Errorf("opus dtx: %w", err)
	}
	return &Audio{enc: enc, buf: make([]byte, 4000)}, nil
}

// Encode returns the Opus payload for one 960-sample-per-channel chunk. The
// returned slice is only valid until the next call.
func (a *Audio) Encode(pcm []int16) ([]byte, error) {
	if len(pcm) != timeline.ChunkFrames {
		return nil, fmt.Errorf("opus: expected %d samples, got %d", timeline.ChunkFrames, len(pcm))
	}
	n, err := a.enc.Encode(pcm, a.buf)
	if err != nil {
		return nil, err
	}
	return a.buf[:n], nil
}
