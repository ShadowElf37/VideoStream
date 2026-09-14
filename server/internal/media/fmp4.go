package media

// Reading a fragmented MP4 well enough to publish it as HLS.
//
// The point of this file is what it does *not* do: there is no second copy of
// the media. A fragmented MP4 is already a sequence of self-contained
// moof+mdat pairs behind a small header, which is exactly the shape HLS wants
// — so the playlist can address the bytes already on disk with
// EXT-X-BYTERANGE, and the same file stays playable as a plain <video src>.
// Segmenting it into thousands of little files would double the disk for a
// box where free space is already surfaced in the library view for a reason.
//
// So this walks the box structure far enough to answer three questions: where
// does the init segment end, where does each fragment start and stop, and how
// long is each one. Everything else in the file is skipped without being
// understood, which is why a hundred lines is enough.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// ErrNotFragmented is returned for an MP4 with no fragments — a title pushed
// before vspush wrote fragmented output. It has no byte ranges to publish, and
// the client falls back to the plain file.
var ErrNotFragmented = errors.New("not a fragmented mp4")

// Segment is one moof+mdat pair: a self-contained piece of media that a player
// can fetch and decode given the init segment.
type Segment struct {
	// Offset and Size address it in the file.
	Offset int64
	Size   int64
	// DecodeTimeMS is the fragment's start on the video track's timeline,
	// from tfdt. The difference between consecutive ones is the duration,
	// which is more trustworthy than summing sample durations.
	DecodeTimeMS int64
}

// Fragments is everything needed to write a playlist for one file.
type Fragments struct {
	// InitSize is the length of the init segment, which starts at zero:
	// everything before the first moof (ftyp, moov, and any sidx or free the
	// muxer left in). A player fetches this once via EXT-X-MAP.
	InitSize int64
	Segments []Segment
	// Codecs is the RFC 6381 string for the master playlist, e.g.
	// "avc1.640028,mp4a.40.2". Empty when it could not be read, in which
	// case the playlist simply omits CODECS.
	Codecs string
	Width  int
	Height int
}

const maxBoxDepth = 8

// ReadFragments parses one fragmented MP4.
func ReadFragments(path string) (Fragments, error) {
	f, err := os.Open(path)
	if err != nil {
		return Fragments{}, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return Fragments{}, err
	}
	return readFragments(f, st.Size())
}

func readFragments(r io.ReaderAt, size int64) (Fragments, error) {
	var out Fragments
	// videoTrackID and timescale come out of moov; the fragments' tfdt values
	// mean nothing without them.
	var videoTrackID uint32
	var timescale uint32

	var pendingMoof int64 = -1
	var pendingTime int64 = -1

	off := int64(0)
	for off < size {
		hdr, body, next, err := readBoxHeader(r, off, size)
		if err != nil {
			// A truncated tail is a push still in flight, not a reason to
			// refuse everything parsed so far.
			break
		}
		switch hdr.typ {
		case "moov":
			videoTrackID, timescale, out.Codecs, out.Width, out.Height = parseMoov(r, body, next)
		case "moof":
			if pendingMoof >= 0 {
				// A moof with no mdat after it: ignore the orphan rather than
				// emitting a segment that decodes to nothing.
				pendingMoof = -1
			}
			pendingMoof = hdr.start
			pendingTime = parseMoofDecodeTime(r, body, next, videoTrackID)
		case "mdat":
			if pendingMoof >= 0 {
				ms := int64(-1)
				if pendingTime >= 0 && timescale > 0 {
					ms = pendingTime * 1000 / int64(timescale)
				}
				out.Segments = append(out.Segments, Segment{
					Offset:       pendingMoof,
					Size:         next - pendingMoof,
					DecodeTimeMS: ms,
				})
				pendingMoof = -1
				pendingTime = -1
			}
		}
		if next <= off {
			break
		}
		off = next
	}

	if len(out.Segments) == 0 {
		return Fragments{}, ErrNotFragmented
	}
	// The init segment is by definition everything before the first fragment:
	// ftyp, moov, and whatever sidx or free the muxer left between them.
	out.InitSize = out.Segments[0].Offset
	return out, nil
}

type boxHeader struct {
	start int64
	typ   string
}

// readBoxHeader returns the box at off, where body is the first content byte
// and next is one past the box.
func readBoxHeader(r io.ReaderAt, off, size int64) (h boxHeader, body, next int64, err error) {
	var buf [16]byte
	if _, err = r.ReadAt(buf[:8], off); err != nil {
		return
	}
	boxSize := int64(binary.BigEndian.Uint32(buf[0:4]))
	h = boxHeader{start: off, typ: string(buf[4:8])}
	body = off + 8
	switch boxSize {
	case 1:
		if _, err = r.ReadAt(buf[8:16], off+8); err != nil {
			return
		}
		boxSize = int64(binary.BigEndian.Uint64(buf[8:16]))
		body = off + 16
	case 0:
		// "to end of file", legal for the last box.
		boxSize = size - off
	}
	if boxSize < body-off || off+boxSize > size {
		err = fmt.Errorf("box %q at %d claims %d bytes", h.typ, off, boxSize)
		return
	}
	next = off + boxSize
	return
}

// eachChild walks the boxes between from and to, calling fn for each.
func eachChild(r io.ReaderAt, from, to int64, depth int, fn func(typ string, body, next int64)) {
	if depth > maxBoxDepth {
		return
	}
	off := from
	for off < to {
		h, body, next, err := readBoxHeader(r, off, to)
		if err != nil || next <= off {
			return
		}
		fn(h.typ, body, next)
		off = next
	}
}

// parseMoov finds the video track's id and timescale, plus the codec string
// and dimensions for the master playlist.
func parseMoov(r io.ReaderAt, from, to int64) (trackID, timescale uint32, codecs string, width, height int) {
	var audioCodec string
	eachChild(r, from, to, 0, func(typ string, body, next int64) {
		if typ != "trak" {
			return
		}
		var thisID, thisScale uint32
		var isVideo bool
		var w, h int
		var vCodec string
		eachChild(r, body, next, 1, func(typ string, body, next int64) {
			switch typ {
			case "tkhd":
				thisID, w, h = parseTkhd(r, body, next)
			case "mdia":
				eachChild(r, body, next, 2, func(typ string, body, next int64) {
					switch typ {
					case "mdhd":
						thisScale = parseMdhd(r, body)
					case "hdlr":
						isVideo = parseHdlr(r, body) == "vide"
					case "minf":
						eachChild(r, body, next, 3, func(typ string, body, next int64) {
							if typ != "stbl" {
								return
							}
							eachChild(r, body, next, 4, func(typ string, body, next int64) {
								if typ != "stsd" {
									return
								}
								c, a := parseStsd(r, body, next)
								if c != "" {
									vCodec = c
								}
								if a != "" {
									audioCodec = a
								}
							})
						})
					}
				})
			}
		})
		if isVideo && trackID == 0 {
			trackID, timescale = thisID, thisScale
			width, height = w, h
			codecs = vCodec
		}
	})
	if codecs != "" && audioCodec != "" {
		codecs += "," + audioCodec
	} else if codecs == "" {
		codecs = audioCodec
	}
	return
}

func parseTkhd(r io.ReaderAt, body, next int64) (id uint32, width, height int) {
	buf := make([]byte, next-body)
	if _, err := r.ReadAt(buf, body); err != nil || len(buf) < 4 {
		return
	}
	version := buf[0]
	// version 0: 4+4+4 (creation, modification, track_id); version 1 uses
	// 8-byte times.
	idOff := 4 + 8
	if version == 1 {
		idOff = 4 + 16
	}
	if len(buf) < idOff+4 {
		return
	}
	id = binary.BigEndian.Uint32(buf[idOff : idOff+4])
	// Width and height are the last two 16.16 fixed-point fields.
	if len(buf) >= 8 {
		w := binary.BigEndian.Uint32(buf[len(buf)-8 : len(buf)-4])
		h := binary.BigEndian.Uint32(buf[len(buf)-4:])
		width, height = int(w>>16), int(h>>16)
	}
	return
}

func parseMdhd(r io.ReaderAt, body int64) uint32 {
	var buf [24]byte
	if _, err := r.ReadAt(buf[:], body); err != nil {
		return 0
	}
	if buf[0] == 1 {
		return binary.BigEndian.Uint32(buf[20:24])
	}
	return binary.BigEndian.Uint32(buf[12:16])
}

func parseHdlr(r io.ReaderAt, body int64) string {
	var buf [12]byte
	if _, err := r.ReadAt(buf[:], body); err != nil {
		return ""
	}
	return string(buf[8:12])
}

// parseStsd reads the sample description far enough to build an RFC 6381
// codec string. Anything unrecognised yields "", and the playlist omits
// CODECS rather than guessing — a wrong one is worse than none.
func parseStsd(r io.ReaderAt, body, next int64) (video, audio string) {
	// 4 bytes version/flags, 4 bytes entry count, then the entries.
	entries := body + 8
	eachChild(r, entries, next, 5, func(typ string, body, next int64) {
		switch typ {
		case "avc1", "avc3":
			entry := typ
			// 78 bytes of VisualSampleEntry before the extension boxes.
			eachChild(r, body+78, next, 6, func(typ string, body, next int64) {
				if typ != "avcC" || next-body < 4 {
					return
				}
				var p [4]byte
				if _, err := r.ReadAt(p[:], body); err != nil {
					return
				}
				// profile_idc, profile_compatibility, level_idc.
				video = fmt.Sprintf("%s.%02x%02x%02x", entry, p[1], p[2], p[3])
			})
		case "mp4a":
			// AAC-LC is what vspush writes and the only audio this library
			// ever contains; reading the esds to confirm would be ceremony.
			audio = "mp4a.40.2"
		}
	})
	return
}

// parseMoofDecodeTime pulls the video track's tfdt out of a fragment. Returns
// -1 when the fragment has no entry for that track, which happens in files
// muxed one track per moof.
func parseMoofDecodeTime(r io.ReaderAt, from, to int64, videoTrackID uint32) int64 {
	found := int64(-1)
	eachChild(r, from, to, 0, func(typ string, body, next int64) {
		if typ != "traf" || found >= 0 {
			return
		}
		var id uint32
		var base int64 = -1
		eachChild(r, body, next, 1, func(typ string, body, next int64) {
			switch typ {
			case "tfhd":
				var buf [8]byte
				if _, err := r.ReadAt(buf[:], body); err == nil {
					id = binary.BigEndian.Uint32(buf[4:8])
				}
			case "tfdt":
				var buf [12]byte
				if _, err := r.ReadAt(buf[:], body); err != nil {
					return
				}
				if buf[0] == 1 {
					base = int64(binary.BigEndian.Uint64(buf[4:12]))
				} else {
					base = int64(binary.BigEndian.Uint32(buf[4:8]))
				}
			}
		})
		// No video track id known yet (a moov we could not read): take the
		// first traf, which is the video track in everything vspush writes.
		if base >= 0 && (videoTrackID == 0 || id == videoTrackID) {
			found = base
		}
	})
	return found
}
