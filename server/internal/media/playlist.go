package media

// HLS over the bytes already on disk.
//
// Two playlists are generated per title, neither of which is stored: a master
// listing one variant per rendition, and a media playlist per rendition whose
// segments are EXT-X-BYTERANGE ranges into that rendition's fragmented MP4.
// The media never moves and is never copied.
//
// This is what buys native Safari playback and, more importantly, what makes
// switching rendition mid-film possible without tearing down the <video>
// element and losing the buffer — which was the objection to a second
// rendition on its own.

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ShadowElf37/VideoStream/proto"
)

// PlaylistVersion 7 is the floor for EXT-X-MAP, which fragmented MP4 needs.
const PlaylistVersion = 7

// Index caches the fragment layout of the files it has parsed.
//
// Walking a two-hour film's box structure is thousands of small reads, and a
// player asks for the playlist on every rendition switch and every reload.
// Keyed on size and mtime, so a re-push of the same title invalidates itself.
type Index struct {
	mu    sync.Mutex
	cache map[string]indexEntry
}

type indexEntry struct {
	size    int64
	modTime time.Time
	frags   Fragments
}

func NewIndex() *Index { return &Index{cache: map[string]indexEntry{}} }

// Fragments returns the parsed layout of path, from cache when it can.
func (ix *Index) Fragments(path string, size int64, modTime time.Time) (Fragments, error) {
	ix.mu.Lock()
	if e, ok := ix.cache[path]; ok && e.size == size && e.modTime.Equal(modTime) {
		ix.mu.Unlock()
		return e.frags, nil
	}
	ix.mu.Unlock()

	frags, err := ReadFragments(path)
	if err != nil {
		return Fragments{}, err
	}

	ix.mu.Lock()
	ix.cache[path] = indexEntry{size: size, modTime: modTime, frags: frags}
	ix.mu.Unlock()
	return frags, nil
}

// MasterPlaylist lists one variant per rendition, largest first.
//
// The query string is the title's signature, repeated on every URI: a player
// fetching a variant sends no headers we control, so the capability has to
// travel in the link.
func MasterPlaylist(m proto.MediaMeta, codecsOf func(r proto.Rendition) string, query string) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	fmt.Fprintf(&b, "#EXT-X-VERSION:%d\n", PlaylistVersion)
	for _, r := range m.Renditions {
		attrs := []string{fmt.Sprintf("BANDWIDTH=%d", bandwidth(r))}
		if r.Width > 0 && r.Height > 0 {
			attrs = append(attrs, fmt.Sprintf("RESOLUTION=%dx%d", r.Width, r.Height))
		}
		// CODECS is omitted rather than guessed when the file could not be
		// read: a wrong one makes a player refuse a stream it could have
		// played, which is worse than making it probe.
		if c := codecsOf(r); c != "" {
			attrs = append(attrs, fmt.Sprintf("CODECS=%q", c))
		}
		fmt.Fprintf(&b, "#EXT-X-STREAM-INF:%s\n", strings.Join(attrs, ","))
		fmt.Fprintf(&b, "%s.m3u8?%s\n", r.Name, query)
	}
	return b.String()
}

// bandwidth is the peak a player should assume. The stated target plus a
// little headroom for the audio and the container; a variant whose BANDWIDTH
// understates it is one a player picks and then stalls on.
func bandwidth(r proto.Rendition) int {
	kbps := r.Kbps
	if kbps <= 0 {
		kbps = 5000
	}
	return (kbps + 250) * 1000
}

// MediaPlaylist writes one rendition as byte ranges over its own MP4.
//
// durationMS is the title's duration from its metadata rather than from the
// container: an fMP4 written with empty_moov carries no duration in its mvhd,
// and the last segment's length can only be found by subtracting.
func MediaPlaylist(frags Fragments, file string, durationMS int64, query string) string {
	durations := segmentDurations(frags.Segments, durationMS)

	target := 0
	for _, d := range durations {
		if int(d+0.999) > target {
			target = int(d + 0.999)
		}
	}
	if target < 1 {
		target = 1
	}

	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	fmt.Fprintf(&b, "#EXT-X-VERSION:%d\n", PlaylistVersion)
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	fmt.Fprintf(&b, "#EXT-X-TARGETDURATION:%d\n", target)
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	fmt.Fprintf(&b, "#EXT-X-MAP:URI=%q,BYTERANGE=\"%d@0\"\n", file+"?"+query, frags.InitSize)
	for i, seg := range frags.Segments {
		fmt.Fprintf(&b, "#EXTINF:%.3f,\n", durations[i])
		fmt.Fprintf(&b, "#EXT-X-BYTERANGE:%d@%d\n", seg.Size, seg.Offset)
		fmt.Fprintf(&b, "%s?%s\n", file, query)
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

// segmentDurations turns decode times into per-segment lengths.
//
// The difference between consecutive tfdt values is exact. The last segment
// has no successor, so it is whatever is left of the title — and if the
// decode times are missing entirely (a muxer that omitted tfdt) the duration
// is shared out evenly, which keeps the playlist valid and the seek bar
// roughly right rather than refusing to produce one at all.
func segmentDurations(segs []Segment, durationMS int64) []float64 {
	out := make([]float64, len(segs))
	if len(segs) == 0 {
		return out
	}
	usable := durationMS > 0
	for _, s := range segs {
		if s.DecodeTimeMS < 0 {
			usable = false
			break
		}
	}
	if !usable {
		even := 2.0
		if durationMS > 0 {
			even = float64(durationMS) / 1000 / float64(len(segs))
		}
		for i := range out {
			out[i] = even
		}
		return out
	}
	for i := range segs {
		var endMS int64
		if i+1 < len(segs) {
			endMS = segs[i+1].DecodeTimeMS
		} else {
			endMS = durationMS
		}
		d := float64(endMS-segs[i].DecodeTimeMS) / 1000
		if d <= 0 {
			// A non-monotonic or overshooting tail: a zero-length segment is
			// invalid HLS, so give it a nominal frame.
			d = 0.04
		}
		out[i] = d
	}
	return out
}
