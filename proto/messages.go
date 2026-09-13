// Package proto is the shared message contract. Keep in sync with messages.ts.
package proto

// Data-channel topics.
const (
	TopicChat     = "chat"
	TopicTyping   = "typing"
	TopicReact    = "react"
	TopicPresence = "presence"
	TopicSettings = "settings"
	TopicMpvCmd   = "mpv.cmd"
	TopicMpvReply = "mpv.reply"
	TopicMpvState = "mpv.state"
	TopicPlayback = "playback"
	TopicMpvEvent = "mpv.event"
)

// Roles carried in LiveKit token metadata.
const (
	RoleHost      = "host"
	RoleViewer    = "viewer"
	RoleProjector = "projector"
)

// Quality presets understood by the projector.
const (
	Preset1080pHigh = "1080p-high"
	Preset1080p     = "1080p"
	Preset720p      = "720p"
	Preset540p      = "540p"
)

// ParticipantMetadata is the JSON stored in a LiveKit token's metadata field.
type ParticipantMetadata struct {
	Role  string `json:"role"`
	Color string `json:"color"`
}

type RoomSettings struct {
	AnyoneCanPause    bool   `json:"anyoneCanPause"`
	DeafenImpliesMute bool   `json:"deafenImpliesMute"`
	MaxPreset         string `json:"maxPreset"`
}

type ChatAuthor struct {
	Identity string `json:"identity"`
	Name     string `json:"name"`
	Color    string `json:"color"`
}

type ChatMessage struct {
	ID     string     `json:"id"`
	RoomID string     `json:"roomId"`
	From   ChatAuthor `json:"from"`
	Text   string     `json:"text"`
	TS     int64      `json:"ts"` // unix ms
	Kind   string     `json:"kind"` // "user" | "system"
}

type TypingMessage struct {
	Typing bool `json:"typing"`
}

type ReactMessage struct {
	Emoji string `json:"emoji"`
}

type PresenceMessage struct {
	MicMuted bool `json:"micMuted"`
	Deafened bool `json:"deafened"`
	PTT      bool `json:"ptt"`
}

// MpvCommand is an mpv JSON-IPC style command array, or a "vs/..." virtual command.
type MpvCommand struct {
	ID  int64 `json:"id"`
	Cmd []any `json:"cmd"`
}

type MpvReply struct {
	ID    int64  `json:"id"`
	OK    bool   `json:"ok"`
	Data  any    `json:"data,omitempty"`
	Error string `json:"error,omitempty"`
}

type MpvTrack struct {
	ID       int64  `json:"id"`
	Type     string `json:"type"` // video | audio | sub
	Lang     string `json:"lang,omitempty"`
	Title    string `json:"title,omitempty"`
	Codec    string `json:"codec,omitempty"`
	Selected bool   `json:"selected"`
	Default  bool   `json:"default,omitempty"`
	Forced   bool   `json:"forced,omitempty"`
	External bool   `json:"external,omitempty"`
}

type MpvChapter struct {
	Title string  `json:"title"`
	Time  float64 `json:"time"`
}

type MpvState struct {
	Seq           int64        `json:"seq"`
	Idle          bool         `json:"idle"`
	Pause         bool         `json:"pause"`
	TimePos       float64      `json:"timePos"`
	Duration      float64      `json:"duration"`
	Speed         float64      `json:"speed"`
	Chapter       int64        `json:"chapter"`
	Chapters      []MpvChapter `json:"chapters"`
	Tracks        []MpvTrack   `json:"tracks"`
	MediaTitle    string       `json:"mediaTitle"`
	Path          string       `json:"path"`
	SubDelay      float64      `json:"subDelay"`
	AudioDelay    float64      `json:"audioDelay"`
	Volume        float64      `json:"volume"`
	SubVisibility bool         `json:"subVisibility"`
	Encoder       string       `json:"encoder"`
	Preset        string       `json:"preset"`
	BitrateKbps   int          `json:"bitrateKbps"`
	FPS           float64      `json:"fps"`
	Width         int          `json:"width"`
	Height        int          `json:"height"`
	LateMs        int          `json:"lateMs"`
	PLI           int64        `json:"pli"`
	NACK          int64        `json:"nack"`
}

type MpvEvent struct {
	Type string         `json:"type"` // file-loaded | seek | pause | unpause | end-file | error | track-changed | quality-changed
	Text string         `json:"text"`
	TS   int64          `json:"ts"`
	Data map[string]any `json:"data,omitempty"`
}

type FsEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size,omitempty"`
	MTime int64  `json:"mtime,omitempty"`
}

type FsList struct {
	Dir     string    `json:"dir"`
	Roots   []string  `json:"roots"`
	Entries []FsEntry `json:"entries"`
}

// HTTP API shapes.

type CreateRoomRequest struct {
	Name     string `json:"name,omitempty"`
	Password string `json:"password,omitempty"`
}

type CreateRoomResponse struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	InviteLink    string `json:"inviteLink"`
	HostLink      string `json:"hostLink"`
	ProjectorLink string `json:"projectorLink"`
}

type RoomInfo struct {
	ID          string       `json:"id"`
	Name        string       `json:"name"`
	HasPassword bool         `json:"hasPassword"`
	Settings    RoomSettings `json:"settings"`
}

type TokenRequest struct {
	Name         string `json:"name"`
	InviteKey    string `json:"inviteKey,omitempty"`
	HostSecret   string `json:"hostSecret,omitempty"`
	ProjectorKey string `json:"projectorKey,omitempty"`
	Password     string `json:"password,omitempty"`
}

type TokenResponse struct {
	Token    string       `json:"token"`
	URL      string       `json:"url"`
	Identity string       `json:"identity"`
	Role     string       `json:"role"`
	Color    string       `json:"color"`
	Session  string       `json:"session"`
	Settings RoomSettings `json:"settings"`
}

type ChatHistoryResponse struct {
	Messages []ChatMessage `json:"messages"`
}

// PlaybackState is where a room is in its film, for the file-on-server mode.
//
// It is an anchor rather than a position: "media time AnchorPosMs was true at
// server time AnchorAtMs, advancing at Rate". A client reconstructs
//
//	target = AnchorPosMs + (clientNow + offset - AnchorAtMs) * Rate
//
// which makes every broadcast self-sufficient — losing several in a row costs
// nothing, because the next one still says exactly where the film is.
type PlaybackState struct {
	Seq  int64 `json:"seq"`
	Idle bool  `json:"idle"`

	MediaID string `json:"mediaId"`
	Title   string `json:"title"`
	// URL is signed and time-limited. Clients never construct one.
	URL        string `json:"url"`
	DurationMS int64  `json:"durationMs"`

	Paused      bool    `json:"paused"`
	AnchorPosMS int64   `json:"anchorPosMs"`
	AnchorAtMS  int64   `json:"anchorAtMs"`
	Rate        float64 `json:"rate"`
	// Gen changes on every discontinuity (load, seek, pause, resume, stop).
	// A client treats a change as licence to jump, rather than as evidence
	// that its own estimate has drifted.
	Gen int64 `json:"gen"`

	// ServerNowMS is the director's clock at the moment this was built, so a
	// client can estimate its offset even without a round trip.
	ServerNowMS int64 `json:"serverNowMs"`

	// PosMS is the anchor evaluated at ServerNowMS. Clients compute this
	// themselves; it is here so logs and debug views need not.
	PosMS int64 `json:"posMs"`

	// Queue is the media ids waiting behind this one.
	Queue []string `json:"queue"`
}
