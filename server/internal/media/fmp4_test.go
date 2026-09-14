package media

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/ShadowElf37/VideoStream/proto"
	"github.com/ShadowElf37/VideoStream/server/internal/media/mediatest"
)

func TestReadFragments(t *testing.T) {
	const timescale = 15360
	blob := mediatest.FMP4(timescale, 5, 2000, 1000)

	frags, err := readFragments(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		t.Fatal(err)
	}
	if len(frags.Segments) != 5 {
		t.Fatalf("found %d fragments, want 5", len(frags.Segments))
	}

	// The init segment is everything before the first moof, sidx boxes
	// included — a player that fetched less would be missing the moov.
	if frags.InitSize != frags.Segments[0].Offset {
		t.Errorf("initSize = %d, want the first fragment's offset %d", frags.InitSize, frags.Segments[0].Offset)
	}
	if frags.InitSize <= 0 {
		t.Fatal("init segment is empty")
	}

	// Every segment must address real, contiguous bytes: a range that is off
	// by one byte is a fragment that will not decode.
	var covered int64 = frags.InitSize
	for i, seg := range frags.Segments {
		if seg.Offset != covered {
			t.Errorf("fragment %d starts at %d, want %d — a gap means bytes nobody fetches", i, seg.Offset, covered)
		}
		if seg.Size <= 0 {
			t.Errorf("fragment %d has size %d", i, seg.Size)
		}
		covered += seg.Size
		if want := int64(i) * 2000; seg.DecodeTimeMS != want {
			t.Errorf("fragment %d decode time = %d ms, want %d", i, seg.DecodeTimeMS, want)
		}
	}
	if covered != int64(len(blob)) {
		t.Errorf("fragments cover %d of %d bytes", covered, len(blob))
	}

	if frags.Codecs != "avc1.640028,mp4a.40.2" {
		t.Errorf("codecs = %q, want avc1.640028,mp4a.40.2", frags.Codecs)
	}
	if frags.Width != 1920 || frags.Height != 1080 {
		t.Errorf("dimensions = %dx%d, want 1920x1080", frags.Width, frags.Height)
	}
}

// A title pushed before vspush wrote fragmented output has no byte ranges to
// publish, and must be told apart from a broken file.
func TestReadFragmentsRejectsPlainMP4(t *testing.T) {
	blob := mediatest.PlainMP4()
	if _, err := readFragments(bytes.NewReader(blob), int64(len(blob))); err != ErrNotFragmented {
		t.Errorf("err = %v, want ErrNotFragmented", err)
	}
}

// A push still in flight is a truncated file, and the right answer is
// whatever parsed rather than nothing.
func TestReadFragmentsToleratesATruncatedTail(t *testing.T) {
	blob := mediatest.FMP4(15360, 4, 2000, 1000)
	cut := blob[:len(blob)-300]
	frags, err := readFragments(bytes.NewReader(cut), int64(len(cut)))
	if err != nil {
		t.Fatal(err)
	}
	if len(frags.Segments) != 3 {
		t.Errorf("found %d fragments in a truncated file, want the 3 that are whole", len(frags.Segments))
	}
}

func TestMediaPlaylist(t *testing.T) {
	const timescale = 15360
	blob := mediatest.FMP4(timescale, 4, 2000, 1000)
	frags, err := readFragments(bytes.NewReader(blob), int64(len(blob)))
	if err != nil {
		t.Fatal(err)
	}

	// 8.5 s: three 2 s fragments and a short tail, which is what the end of
	// a real film looks like.
	out := MediaPlaylist(frags, proto.MovieFileName, 8500, "e=1&s=abc")
	t.Log("\n" + out)

	for _, want := range []string{
		"#EXTM3U",
		"#EXT-X-VERSION:7",
		"#EXT-X-PLAYLIST-TYPE:VOD",
		// Ceiling of the longest segment: the 2.5 s tail, not the 2 s body.
		"#EXT-X-TARGETDURATION:3",
		"#EXT-X-MEDIA-SEQUENCE:0",
		fmt.Sprintf("#EXT-X-MAP:URI=\"movie.mp4?e=1&s=abc\",BYTERANGE=\"%d@0\"", frags.InitSize),
		"#EXT-X-ENDLIST",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("playlist is missing %q", want)
		}
	}

	// Every segment: a duration, a range, and the file it lives in — in that
	// order, because a player reads the tags that precede a URI as its own.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	var segs int
	for i, l := range lines {
		if !strings.HasPrefix(l, "#EXT-X-BYTERANGE:") {
			continue
		}
		segs++
		if !strings.HasPrefix(lines[i-1], "#EXTINF:") {
			t.Errorf("byte range at line %d is not preceded by EXTINF: %q", i, lines[i-1])
		}
		if lines[i+1] != "movie.mp4?e=1&s=abc" {
			t.Errorf("byte range at line %d is not followed by the signed file: %q", i, lines[i+1])
		}
		seg := frags.Segments[segs-1]
		if want := fmt.Sprintf("#EXT-X-BYTERANGE:%d@%d", seg.Size, seg.Offset); l != want {
			t.Errorf("range %d = %q, want %q", segs, l, want)
		}
	}
	if segs != 4 {
		t.Errorf("%d segments in the playlist, want 4", segs)
	}

	// The tail is what is left of the title, not another 2 s: overstating it
	// makes a player wait at the end of the film for media that is not there.
	if !strings.Contains(out, "#EXTINF:2.500,") {
		t.Errorf("the last segment is not 2.5 s; playlist:\n%s", out)
	}
	if strings.Count(out, "#EXTINF:2.000,") != 3 {
		t.Errorf("want three 2.000 s segments; playlist:\n%s", out)
	}
}

func TestSegmentDurationsDegradeGracefully(t *testing.T) {
	// No decode times at all (a muxer that omitted tfdt): share the duration
	// out rather than refusing to produce a playlist.
	segs := []Segment{{DecodeTimeMS: -1}, {DecodeTimeMS: -1}, {DecodeTimeMS: -1}}
	got := segmentDurations(segs, 9000)
	for i, d := range got {
		if d != 3 {
			t.Errorf("duration[%d] = %v, want 3", i, d)
		}
	}

	// A decode time past the stated duration must not produce a zero or
	// negative EXTINF, which is invalid HLS.
	got = segmentDurations([]Segment{{DecodeTimeMS: 0}, {DecodeTimeMS: 5000}}, 4000)
	if got[1] <= 0 {
		t.Errorf("duration[1] = %v, want something positive", got[1])
	}
}

func TestMasterPlaylist(t *testing.T) {
	meta := proto.MediaMeta{
		ID: "film", DurationMS: 8500,
		Renditions: []proto.Rendition{
			{Name: "1080p", File: proto.MovieFileName, Width: 1920, Height: 1080, Kbps: 5000},
			{Name: "720p", File: "movie.720p.mp4", Width: 1280, Height: 720, Kbps: 3000},
		},
	}
	out := MasterPlaylist(meta, func(r proto.Rendition) string {
		if r.Name == "720p" {
			return "" // unreadable: CODECS must be omitted, not guessed
		}
		return "avc1.640028,mp4a.40.2"
	}, "e=1&s=abc")
	t.Log("\n" + out)

	for _, want := range []string{
		"#EXT-X-STREAM-INF:BANDWIDTH=5250000,RESOLUTION=1920x1080,CODECS=\"avc1.640028,mp4a.40.2\"",
		"1080p.m3u8?e=1&s=abc",
		"#EXT-X-STREAM-INF:BANDWIDTH=3250000,RESOLUTION=1280x720",
		"720p.m3u8?e=1&s=abc",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("master playlist is missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "CODECS=\"\"") {
		t.Error("an unreadable rendition got an empty CODECS rather than none")
	}
}

// The cache is what stops a rendition switch re-walking a two-hour film.
func TestIndexCachesUntilTheFileChanges(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/movie.mp4"
	if err := os.WriteFile(path, mediatest.FMP4(15360, 3, 2000, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	ix := NewIndex()
	first, err := ix.Fragments(path, st.Size(), st.ModTime())
	if err != nil {
		t.Fatal(err)
	}
	if len(ix.cache) != 1 {
		t.Fatalf("nothing was cached")
	}

	// Same key: the cached answer, even though the file on disk now says
	// something else entirely.
	if err := os.WriteFile(path, mediatest.FMP4(15360, 9, 2000, 1000), 0o644); err != nil {
		t.Fatal(err)
	}
	again, err := ix.Fragments(path, st.Size(), st.ModTime())
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Segments) != len(first.Segments) {
		t.Error("the cache was bypassed for an unchanged key")
	}

	// A re-push changes size and mtime, and must invalidate.
	st2, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := ix.Fragments(path, st2.Size(), st2.ModTime())
	if err != nil {
		t.Fatal(err)
	}
	if len(fresh.Segments) != 9 {
		t.Errorf("after a re-push the cache still had %d fragments, want 9", len(fresh.Segments))
	}
}

// Point this at a real push to check the parser against what ffmpeg actually
// writes:  VS_FMP4=…/media/sync_2min/movie.mp4 go test ./internal/media/
func TestRealFragmentedFile(t *testing.T) {
	path := os.Getenv("VS_FMP4")
	if path == "" {
		t.Skip("set VS_FMP4 to a fragmented mp4 to check the parser against a real one")
	}
	frags, err := ReadFragments(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	covered := frags.InitSize
	for _, s := range frags.Segments {
		covered += s.Size
	}
	t.Logf("%d fragments, init %d bytes, codecs %q, %dx%d, covering %d of %d bytes (%d trailing)",
		len(frags.Segments), frags.InitSize, frags.Codecs, frags.Width, frags.Height,
		covered, st.Size(), st.Size()-covered)
	// ffmpeg writes an mfra index after the last fragment, which belongs to
	// no segment. Everything up to it must be covered exactly.
	if covered > st.Size() {
		t.Errorf("fragments claim %d bytes of a %d byte file", covered, st.Size())
	}
	if st.Size()-covered > 1<<20 {
		t.Errorf("%d bytes after the last fragment is more than an mfra", st.Size()-covered)
	}
	for i := 1; i < len(frags.Segments); i++ {
		if frags.Segments[i].DecodeTimeMS <= frags.Segments[i-1].DecodeTimeMS {
			t.Fatalf("decode times are not increasing at fragment %d", i)
		}
	}
}
