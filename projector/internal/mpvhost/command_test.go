package mpvhost

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gen2brain/go-mpv"

	"github.com/ShadowElf37/VideoStream/proto"
)

// decode mimics what arrives over the data channel: JSON numbers become float64.
func decode(t *testing.T, s string) []any {
	t.Helper()
	var v []any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestCommandStringsFormatsJSONNumbers(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{`["seek", 60, "absolute"]`, []string{"seek", "60", "absolute"}},
		{`["seek", -10, "relative"]`, []string{"seek", "-10", "relative"}},
		{`["add", "sub-delay", 0.1]`, []string{"add", "sub-delay", "0.1"}},
		{`["set", "speed", 1.5]`, []string{"set", "speed", "1.5"}},
		{`["cycle", "pause"]`, []string{"cycle", "pause"}},
		{`["set", "pause", true]`, []string{"set", "pause", "yes"}},
		{`["set", "sub-visibility", false]`, []string{"set", "sub-visibility", "no"}},
		{`["loadfile", "/m/a.mkv", "replace"]`, []string{"loadfile", "/m/a.mkv", "replace"}},
		// 1e6 decodes as float64 1000000: no exponent, no trailing zeros.
		{`["seek", 1e6, "absolute"]`, []string{"seek", "1000000", "absolute"}},
		{`["set", "volume", 100.0]`, []string{"set", "volume", "100"}},
	}
	for _, c := range cases {
		got, err := CommandStrings(decode(t, c.in))
		if err != nil {
			t.Fatalf("%s: %v", c.in, err)
		}
		if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Fatalf("%s -> %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCommandStringsRejectsGarbage(t *testing.T) {
	if _, err := CommandStrings([]any{"seek", map[string]any{"a": 1}}); err == nil {
		t.Fatal("expected an error for an object argument")
	}
	if _, err := CommandStrings([]any{"seek", []any{1}}); err == nil {
		t.Fatal("expected an error for an array argument")
	}
}

func TestPropertyFormat(t *testing.T) {
	cases := []struct {
		in     any
		format mpv.Format
		val    any
	}{
		{true, mpv.FormatFlag, true},
		{"eng", mpv.FormatString, "eng"},
		{float64(2), mpv.FormatInt64, int64(2)},   // ["set_property","sid",2]
		{float64(0.1), mpv.FormatDouble, 0.1},     // ["set_property","sub-delay",0.1]
		{float64(-1), mpv.FormatInt64, int64(-1)}, // ["set_property","chapter",-1]
	}
	for _, c := range cases {
		gf, gv, gok := propertyFormat(c.in)
		if !gok || gf != c.format || gv != c.val {
			t.Fatalf("propertyFormat(%v) = %v,%v,%v want %v,%v", c.in, gf, gv, gok, c.format, c.val)
		}
	}
	if _, _, ok := propertyFormat([]any{1}); ok {
		t.Fatal("arrays are not valid property values")
	}
}

func TestIsVirtual(t *testing.T) {
	if !IsVirtual(decode(t, `["vs/fs.list", ""]`)) {
		t.Fatal("vs/ commands are virtual")
	}
	if IsVirtual(decode(t, `["seek", 1]`)) {
		t.Fatal("mpv commands are not virtual")
	}
	if IsVirtual(nil) {
		t.Fatal("empty command is not virtual")
	}
}

func TestFormatTime(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "0:00"},
		{61.4, "1:01"},
		{3599.6, "1:00:00"},
		{3661, "1:01:01"},
		{-5, "0:00"},
	}
	for _, c := range cases {
		if got := FormatTime(c.in); got != c.want {
			t.Fatalf("FormatTime(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTrackLabel(t *testing.T) {
	tracks := mustTracks(t, `[
		{"id":1,"type":"audio","lang":"eng","title":"Commentary","selected":true},
		{"id":2,"type":"audio","lang":"jpn"},
		{"id":1,"type":"sub","title":"Full"}
	]`)
	if got := trackLabel(tracks, "audio", "1"); got != "Commentary (eng)" {
		t.Fatalf("got %q", got)
	}
	if got := trackLabel(tracks, "audio", "2"); got != "jpn" {
		t.Fatalf("got %q", got)
	}
	if got := trackLabel(tracks, "sub", "1"); got != "Full" {
		t.Fatalf("got %q", got)
	}
	if got := trackLabel(tracks, "sub", "no"); got != "none" {
		t.Fatalf("got %q", got)
	}
	if got := trackLabel(tracks, "audio", "9"); got != "track 9" {
		t.Fatalf("got %q", got)
	}
}

// mustTracks builds the same []any/map[string]any shape libmpv's node
// conversion produces for track-list.
func mustTracks(t *testing.T, s string) []proto.MpvTrack {
	t.Helper()
	var raw []any
	if err := json.Unmarshal([]byte(s), &raw); err != nil {
		t.Fatal(err)
	}
	tracks, ok := nodeTracks(raw)
	if !ok {
		t.Fatal("nodeTracks failed")
	}
	return tracks
}
