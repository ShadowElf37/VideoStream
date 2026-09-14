package proto

import "strconv"

// The media library: what `vspush` writes to the server and what the app
// server serves back.
//
// A title is a directory, not a file, because a title is several things — the
// movie, its metadata, later a poster, subtitle sidecars and a second
// rendition. Keeping them together means adding any of those later needs no
// change to the library, the API or the UI.
//
//	media/<id>/movie.mp4        H.264 High + AAC-LC, fragmented, IDR every ~2s
//	media/<id>/movie.720p.mp4   the same film at 3 Mbps, when the source is bigger
//	media/<id>/meta.json        this file
type MediaMeta struct {
	// ID is the directory name and the stable handle used everywhere.
	// Derived from the title, not the source filename, so re-pushing a title
	// replaces it rather than accumulating near-duplicates.
	ID    string `json:"id"`
	Title string `json:"title"`
	// Source is the file it came from, for working out which push produced
	// which title when several look alike.
	Source string `json:"source,omitempty"`

	DurationMS int64 `json:"durationMs"`
	Width      int   `json:"width"`
	Height     int   `json:"height"`
	// Frame rate as an exact rational: 24000/1001 must not become 23.976.
	FPSNum int `json:"fpsNum"`
	FPSDen int `json:"fpsDen"`

	VideoCodec string `json:"videoCodec"`
	AudioCodec string `json:"audioCodec"`
	SizeBytes  int64  `json:"sizeBytes"`

	// Which mpv tracks were baked in. Subtitles are burned into the video and
	// only one audio track survives, so this is the only record of what a
	// viewer actually gets.
	AudioTrack int `json:"audioTrack,omitempty"`
	SubTrack   int `json:"subTrack,omitempty"`

	Chapters []MediaChapter `json:"chapters,omitempty"`
	PushedAt string         `json:"pushedAt"`

	// Renditions is every encode of this title, largest first, including the
	// primary one in movie.mp4. Empty for a title pushed before renditions
	// existed, which is also the signal that it has no HLS playlist — its
	// MP4 is not fragmented and there are no byte ranges to publish.
	Renditions []Rendition `json:"renditions,omitempty"`
}

// Rendition is one encode of a title.
//
// A second rendition is what replaces simulcast for hosted media. A viewer on
// a weak link used to stall where WebRTC would have gone blurry; this is the
// mitigation, and it costs about 40% more disk per title.
type Rendition struct {
	// Name is how a viewer picks it and how its playlist is addressed:
	// "1080p", "720p". Derived from the height.
	Name string `json:"name"`
	// File is the name inside the title directory.
	File   string `json:"file"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
	// Kbps is what it was asked to target, for the master playlist's
	// BANDWIDTH and for the UI.
	Kbps      int   `json:"kbps"`
	SizeBytes int64 `json:"sizeBytes"`
}

// MediaChapter is a seek landmark. The seek bar already draws chapter ticks;
// HTTP delivery is what finally makes them available, since the RTP path had
// nowhere to carry them.
type MediaChapter struct {
	StartMS int64  `json:"startMs"`
	Title   string `json:"title,omitempty"`
}

// MetaFileName and MovieFileName are the fixed names inside a title directory.
const (
	MetaFileName  = "meta.json"
	MovieFileName = "movie.mp4"
	// MasterPlaylistName is generated, not stored: the server builds it from
	// the fragmented MP4s already on disk rather than keeping a second copy
	// of the media.
	MasterPlaylistName = "index.m3u8"
)

// RenditionFileName is where a named rendition lives. The primary one keeps
// the plain name so an older client, and a plain <video src>, still work.
func RenditionFileName(name string, primary bool) string {
	if primary {
		return MovieFileName
	}
	return "movie." + name + ".mp4"
}

// RenditionName is what a height is called.
func RenditionName(height int) string {
	if height <= 0 {
		return "source"
	}
	return strconv.Itoa(height) + "p"
}
