// Package mediatest builds fragmented MP4s for tests.
//
// A real clip would be a megabyte in a repository that deliberately gitignores
// testdata/*.mp4, and it would not let a test say "now make the decode times
// non-monotonic" or "now truncate the tail" — which are the cases worth having
// tests for. The layout here is what ffmpeg's
// `+frag_keyframe+empty_moov+default_base_moof+global_sidx` writes, minus the
// parts the parser skips without reading.
//
// It is a package rather than a test helper because the HTTP tests need it
// too: a playlist whose byte ranges are not ranges of a real file is a
// playlist nobody can check.
package mediatest

import (
	"bytes"
	"encoding/binary"
)

func box(typ string, payload ...[]byte) []byte {
	var body []byte
	for _, p := range payload {
		body = append(body, p...)
	}
	out := make([]byte, 8, 8+len(body))
	binary.BigEndian.PutUint32(out[0:4], uint32(8+len(body)))
	copy(out[4:8], typ)
	return append(out, body...)
}

func u32(v uint32) []byte {
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], v)
	return b[:]
}

func u64(v uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], v)
	return b[:]
}

// tkhd, version 0, with the track id and the 16.16 dimensions in the places
// the parser looks for them.
func tkhd(trackID uint32, w, h int) []byte {
	body := []byte{0, 0, 0, 0} // version 0, flags
	body = append(body, u32(0)...)
	body = append(body, u32(0)...)
	body = append(body, u32(trackID)...)
	body = append(body, make([]byte, 4+4+8+2+2+2+2+36)...)
	body = append(body, u32(uint32(w)<<16)...)
	body = append(body, u32(uint32(h)<<16)...)
	return box("tkhd", body)
}

func mdhd(timescale uint32) []byte {
	body := []byte{0, 0, 0, 0}
	body = append(body, u32(0)...)
	body = append(body, u32(0)...)
	body = append(body, u32(timescale)...)
	body = append(body, u32(0)...)
	body = append(body, 0, 0, 0, 0)
	return box("mdhd", body)
}

func hdlr(kind string) []byte {
	body := []byte{0, 0, 0, 0}
	body = append(body, u32(0)...)
	body = append(body, []byte(kind)...)
	body = append(body, make([]byte, 12)...)
	body = append(body, 0)
	return box("hdlr", body)
}

// avc1 with an avcC carrying High profile, level 4.0 — "avc1.640028".
func avc1(profile, compat, level byte) []byte {
	avcC := box("avcC", []byte{1, profile, compat, level, 0xff})
	return box("avc1", append(make([]byte, 78), avcC...))
}

func videoTrak(trackID uint32, timescale uint32, w, h int) []byte {
	stsd := box("stsd", []byte{0, 0, 0, 0}, u32(1), avc1(0x64, 0x00, 0x28))
	return box("trak",
		tkhd(trackID, w, h),
		box("mdia",
			mdhd(timescale),
			hdlr("vide"),
			box("minf", box("stbl", stsd)),
		),
	)
}

func audioTrak(trackID uint32) []byte {
	stsd := box("stsd", []byte{0, 0, 0, 0}, u32(1), box("mp4a", make([]byte, 28)))
	return box("trak",
		tkhd(trackID, 0, 0),
		box("mdia", mdhd(48000), hdlr("soun"), box("minf", box("stbl", stsd))),
	)
}

func fragment(trackID uint32, decodeTime uint64, payload int) []byte {
	tfhd := box("tfhd", []byte{0, 0, 0, 0}, u32(trackID))
	tfdt := box("tfdt", []byte{1, 0, 0, 0}, u64(decodeTime))
	moof := box("moof", box("mfhd", []byte{0, 0, 0, 0}, u32(1)), box("traf", tfhd, tfdt))
	mdat := box("mdat", bytes.Repeat([]byte{0xAB}, payload))
	return append(moof, mdat...)
}

// FMP4 assembles a file with count fragments, stepMS apart, each carrying
// payload+i bytes so no two are the same size.
func FMP4(timescale uint32, count int, stepMS int64, payload int) []byte {
	var out []byte
	out = append(out, box("ftyp", []byte("isom"), u32(512), []byte("isomiso2avc1mp41"))...)
	out = append(out, box("moov",
		box("mvhd", make([]byte, 100)),
		videoTrak(1, timescale, 1920, 1080),
		audioTrak(2),
	)...)
	// ffmpeg writes one sidx per track between the moov and the first
	// fragment; they belong to the init segment and are otherwise ignored.
	out = append(out, box("sidx", make([]byte, 40))...)
	out = append(out, box("sidx", make([]byte, 40))...)
	for i := 0; i < count; i++ {
		dt := uint64(int64(i) * stepMS * int64(timescale) / 1000)
		out = append(out, fragment(1, dt, payload+i)...)
	}
	return out
}

// PlainMP4 is the same shape without fragments: an old push, which has no byte
// ranges to publish.
func PlainMP4() []byte {
	var out []byte
	out = append(out, box("ftyp", []byte("isom"), u32(512))...)
	out = append(out, box("moov", box("mvhd", make([]byte, 100)), videoTrak(1, 15360, 1280, 720))...)
	out = append(out, box("mdat", bytes.Repeat([]byte{1}, 500))...)
	return out
}
