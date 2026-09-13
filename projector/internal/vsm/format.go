// Package vsm is the pre-encoded media format the server-side projector
// streams.
//
// Why a format of our own. The Oracle Ampere box has no GPU, and software
// x264 at 1080p manages 0.35x real time at veryfast and 0.97x at superfast —
// there is no live encoding to be had there. So the work moves to the machine
// that has the files: mpv renders the subtitles and VideoToolbox encodes, and
// what lands on the server is already in the exact shape the wire wants
// (H.264 Main, no B-frames, IDR every 2s; Opus 48 kHz stereo).
//
// That lets the server side be a pure demux-and-pace loop: no mpv, no ffmpeg,
// no decode, no encode, and a static binary with no cgo. A .vsm is therefore
// deliberately dull — a JSON header, then length-prefixed records already
// packetizable as they sit.
//
// Layout:
//
//	"VSM1"                 magic
//	uint32                 header length, little endian
//	<header>               JSON, see Header
//	<record>...            until EOF
//
// Record:
//
//	uint8                  Kind
//	uint32                 payload length, little endian
//	uint32                 aux, little endian
//	<payload>
//
// For video, aux is the frame index; for audio, the sample count the packet
// covers. Nine bytes of framing per record is ~670 B/s at 24 fps plus 50
// packets/s of audio, which is not worth optimising.
package vsm

import "encoding/binary"

// Magic identifies the format and its generation. A reader rejects anything
// else rather than guessing.
const Magic = "VSM1"

// Version is the header schema version, carried inside the header so a future
// reader can tell a v1 file apart without a new magic.
const Version = 1

// Kind distinguishes the two record types.
type Kind uint8

const (
	// KindVideo is one H.264 access unit in Annex-B, with SPS/PPS in front of
	// every IDR (ffmpeg's dump_extra=freq=keyframe) so a late joiner can
	// decode from any keyframe without side data.
	KindVideo Kind = 0
	// KindAudio is one Opus packet.
	KindAudio Kind = 1
)

// recordHeaderLen is the fixed framing in front of every payload.
const recordHeaderLen = 9

// MaxRecordLen bounds a single payload, so a corrupt length cannot make a
// reader allocate wildly. A 1080p IDR is tens of kilobytes; 16 MiB is far
// beyond anything legitimate.
const MaxRecordLen = 16 << 20

// Header describes the stream. It is JSON so that adding a field does not
// break existing files, and so `head -c 2000 file.vsm` stays readable when
// something is wrong.
type Header struct {
	Version int `json:"version"`

	// Title is what viewers see; Source is the file it came from, kept for
	// provenance when working out which push produced which .vsm.
	Title  string `json:"title"`
	Source string `json:"source,omitempty"`

	Width  int `json:"width"`
	Height int `json:"height"`
	// Frame rate as an exact rational: 24000/1001 must not become 23.976.
	FPSNum int `json:"fpsNum"`
	FPSDen int `json:"fpsDen"`

	DurationMS    int64 `json:"durationMs"`
	AudioRate     int   `json:"audioRate"`
	AudioChannels int   `json:"audioChannels"`

	// Which mpv tracks were baked in. Subtitles are burned into the video and
	// only one audio track survives, so this is the only record of what a
	// viewer is actually getting.
	AudioTrack int `json:"audioTrack,omitempty"`
	SubTrack   int `json:"subTrack,omitempty"`

	// Keyframes indexes every IDR for seeking. At one per 2s a three-hour
	// film is ~5400 entries, which is small enough to keep in the header
	// rather than inventing a second index structure.
	Keyframes []Keyframe `json:"keyframes"`
}

// Keyframe is a seek target: everything a player needs to resume mid-stream.
type Keyframe struct {
	MS int64 `json:"ms"`
	// Offset is the absolute byte offset of the video record, so a seek is one
	// ReadAt away rather than a scan.
	Offset int64 `json:"off"`
	Frame  int   `json:"frame"`
	// Samples elapsed at this point, which the player needs to keep RTP
	// timestamps monotonic across a seek.
	Samples int64 `json:"samples"`
}

// FrameDuration returns the nominal duration of one video frame.
func (h *Header) FrameDurationNS() int64 {
	if h.FPSNum <= 0 || h.FPSDen <= 0 {
		return 0
	}
	return int64(h.FPSDen) * 1e9 / int64(h.FPSNum)
}

func putRecordHeader(b []byte, kind Kind, n, aux uint32) {
	b[0] = byte(kind)
	binary.LittleEndian.PutUint32(b[1:5], n)
	binary.LittleEndian.PutUint32(b[5:9], aux)
}

func parseRecordHeader(b []byte) (kind Kind, n, aux uint32) {
	return Kind(b[0]), binary.LittleEndian.Uint32(b[1:5]), binary.LittleEndian.Uint32(b[5:9])
}

func putUint32(b []byte, v uint32) { binary.LittleEndian.PutUint32(b, v) }
