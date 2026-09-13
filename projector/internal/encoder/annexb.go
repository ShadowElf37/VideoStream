package encoder

// Annex-B parsing.
//
// ffmpeg is told to insert an access unit delimiter (NAL type 9) in front of
// every access unit (-bsf:v h264_metadata=aud=insert), so an AUD is an exact
// AU boundary. We drop the AUD itself and any filler (type 12) because they
// waste RTP payload, and keep everything else — in particular the SPS/PPS that
// dump_extra=freq=keyframe puts in front of each IDR.

const (
	nalIDR    = 5
	nalSPS    = 7
	nalPPS    = 8
	nalAUD    = 9
	nalFiller = 12
)

// MaxPendingBytes guards against a stream with no AUDs at all (a bitstream
// filter that silently did nothing) buffering without bound.
const MaxPendingBytes = 16 << 20

type nalRef struct {
	typ   byte
	sc    int // offset of the start code
	start int // offset of the NAL payload
	end   int // offset just past the payload
}

// scanNALs locates every NAL unit in an Annex-B buffer.
func scanNALs(buf []byte) []nalRef {
	var out []nalRef
	i := 0
	for {
		sc, payload := nextStartCode(buf, i)
		if sc < 0 {
			break
		}
		out = append(out, nalRef{typ: buf[payload] & 0x1f, sc: sc, start: payload})
		i = payload + 1
	}
	for k := range out {
		end := len(buf)
		if k+1 < len(out) {
			end = out[k+1].sc
		}
		// Trim the zero bytes that belong to the next start code prefix.
		for end > out[k].start && buf[end-1] == 0 {
			end--
		}
		out[k].end = end
	}
	return out
}

// nextStartCode finds the next 3- or 4-byte start code at or after i and
// returns (start-code offset, payload offset).
func nextStartCode(buf []byte, i int) (int, int) {
	for ; i+3 <= len(buf); i++ {
		if buf[i] != 0 || buf[i+1] != 0 || buf[i+2] != 1 {
			continue
		}
		if i+3 >= len(buf) {
			return -1, -1 // start code with no payload yet
		}
		sc := i
		if sc > 0 && buf[sc-1] == 0 {
			sc--
		}
		return sc, i + 3
	}
	return -1, -1
}

// SplitAccessUnits cuts an Annex-B byte stream into complete access units.
//
// An AU is only complete once the next AUD has been seen, so the trailing
// partial AU is returned as rest and must be prepended to the next read. Each
// returned AU is a fresh slice of 4-byte-start-code-prefixed NALs with AUD and
// filler removed.
func SplitAccessUnits(buf []byte) (aus [][]byte, rest []byte) {
	nals := scanNALs(buf)
	var aud []int
	for i, n := range nals {
		if n.typ == nalAUD {
			aud = append(aud, i)
		}
	}
	if len(aud) < 2 {
		return nil, buf
	}
	for k := 0; k+1 < len(aud); k++ {
		var au []byte
		for i := aud[k]; i < aud[k+1]; i++ {
			n := nals[i]
			if n.typ == nalAUD || n.typ == nalFiller || n.end <= n.start {
				continue
			}
			au = append(au, 0, 0, 0, 1)
			au = append(au, buf[n.start:n.end]...)
		}
		if len(au) > 0 {
			aus = append(aus, au)
		}
	}
	return aus, buf[nals[aud[len(aud)-1]].sc:]
}

// IsKeyframe reports whether an access unit carries an IDR picture.
func IsKeyframe(au []byte) bool {
	for _, n := range scanNALs(au) {
		if n.typ == nalIDR {
			return true
		}
	}
	return false
}

// HasParameterSets reports whether the AU carries SPS and PPS in band.
func HasParameterSets(au []byte) bool {
	var sps, pps bool
	for _, n := range scanNALs(au) {
		switch n.typ {
		case nalSPS:
			sps = true
		case nalPPS:
			pps = true
		}
	}
	return sps && pps
}
