package proto

// The media library: what `vspush` writes to the server and what the app
// server serves back.
//
// A title is a directory, not a file, because a title is several things — the
// movie, its metadata, later a poster, subtitle sidecars and a second
// rendition. Keeping them together means adding any of those later needs no
// change to the library, the API or the UI.
//
//	media/<id>/movie.mp4    H.264 High + AAC-LC, moov first, IDR every ~2s
//	media/<id>/meta.json    this file
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
)
