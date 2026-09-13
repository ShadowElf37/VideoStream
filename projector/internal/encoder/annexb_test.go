package encoder

import (
	"bytes"
	"testing"
)

// nal builds one start-code prefixed NAL with the given type and body.
func nal(typ byte, body ...byte) []byte {
	out := []byte{0, 0, 0, 1, typ & 0x1f}
	return append(out, body...)
}

func nal3(typ byte, body ...byte) []byte {
	out := []byte{0, 0, 1, typ & 0x1f}
	return append(out, body...)
}

func cat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

func TestSplitAccessUnitsDropsAUDAndFiller(t *testing.T) {
	stream := cat(
		nal(nalAUD, 0x10),
		nal(nalSPS, 1, 2, 3),
		nal(nalPPS, 4),
		nal(nalIDR, 0xaa, 0xbb),
		nal(nalFiller, 0xff, 0xff),
		// second AU, non-IDR slice, 3-byte start code
		nal(nalAUD, 0x30),
		nal3(1, 0xcc),
		// third AU is still open: it must stay in rest
		nal(nalAUD, 0x30),
		nal(1, 0xdd),
	)
	aus, rest := SplitAccessUnits(stream)
	if len(aus) != 2 {
		t.Fatalf("got %d AUs, want 2", len(aus))
	}
	want0 := cat(nal(nalSPS, 1, 2, 3), nal(nalPPS, 4), nal(nalIDR, 0xaa, 0xbb))
	if !bytes.Equal(aus[0], want0) {
		t.Fatalf("AU0 = % x\nwant % x", aus[0], want0)
	}
	if !IsKeyframe(aus[0]) || !HasParameterSets(aus[0]) {
		t.Fatalf("AU0 should be a keyframe with SPS/PPS")
	}
	want1 := nal(1, 0xcc)
	if !bytes.Equal(aus[1], want1) {
		t.Fatalf("AU1 = % x\nwant % x", aus[1], want1)
	}
	if IsKeyframe(aus[1]) {
		t.Fatalf("AU1 should not be a keyframe")
	}
	if !bytes.Equal(rest, cat(nal(nalAUD, 0x30), nal(1, 0xdd))) {
		t.Fatalf("rest = % x", rest)
	}
}

func TestSplitAccessUnitsIncremental(t *testing.T) {
	// Feed the same stream one byte at a time; the AUs must come out whole and
	// in order, exactly once each.
	full := cat(
		nal(nalAUD, 0x10), nal(nalIDR, 1),
		nal(nalAUD, 0x30), nal(1, 2),
		nal(nalAUD, 0x30), nal(1, 3),
		nal(nalAUD, 0x30), nal(1, 4),
	)
	var pending []byte
	var got [][]byte
	for i := range full {
		pending = append(pending, full[i])
		aus, rest := SplitAccessUnits(pending)
		got = append(got, aus...)
		pending = append([]byte(nil), rest...)
	}
	if len(got) != 3 {
		t.Fatalf("got %d AUs, want 3", len(got))
	}
	for i, want := range [][]byte{nal(nalIDR, 1), nal(1, 2), nal(1, 3)} {
		if !bytes.Equal(got[i], want) {
			t.Fatalf("AU%d = % x, want % x", i, got[i], want)
		}
	}
}

func TestSplitAccessUnitsNoAUD(t *testing.T) {
	stream := cat(nal(nalSPS, 1), nal(nalIDR, 2))
	aus, rest := SplitAccessUnits(stream)
	if aus != nil {
		t.Fatalf("expected no AUs without delimiters, got %d", len(aus))
	}
	if !bytes.Equal(rest, stream) {
		t.Fatalf("rest should be the whole buffer")
	}
}

func TestSplitAccessUnitsEmptyAUIsSkipped(t *testing.T) {
	// Two AUDs back to back (an AU with nothing but filler) must not produce an
	// empty access unit.
	stream := cat(
		nal(nalAUD, 0x10), nal(nalFiller, 0xff),
		nal(nalAUD, 0x10), nal(nalIDR, 7),
		nal(nalAUD, 0x10),
	)
	aus, _ := SplitAccessUnits(stream)
	if len(aus) != 1 {
		t.Fatalf("got %d AUs, want 1", len(aus))
	}
	if !bytes.Equal(aus[0], nal(nalIDR, 7)) {
		t.Fatalf("AU = % x", aus[0])
	}
}

func TestScanNALsTrimsTrailingZeros(t *testing.T) {
	stream := append(nal(1, 0xaa, 0x00, 0x00), nal(2, 0xbb)...)
	nals := scanNALs(stream)
	if len(nals) != 2 {
		t.Fatalf("got %d NALs", len(nals))
	}
	if got := stream[nals[0].start:nals[0].end]; !bytes.Equal(got, []byte{1, 0xaa}) {
		t.Fatalf("NAL0 payload = % x", got)
	}
}
