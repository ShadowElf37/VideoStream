// Package proto is the shared message contract. Keep in sync with messages.ts.
package proto

// RoomID is the one room. There is no other: the site is a single door into a
// single LiveKit room, and this is its name everywhere — the LiveKit room,
// the chat history, the director's state.
const RoomID = "main"

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
	// TopicPlaybackIntent echoes who did what the moment a command lands,
	// ahead of the state it produces.
	TopicPlaybackIntent = "playback.intent"
	TopicMpvEvent       = "mpv.event"
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
	// WaitForEveryone holds playback at every discontinuity until each client
	// that is still reporting says it has buffered enough to start. Off by
	// default: it trades a few seconds at the top of a scene for nobody
	// scrambling to catch up, and that is a room's choice, not ours.
	WaitForEveryone bool `json:"waitForEveryone"`
}

type ChatAuthor struct {
	Identity string `json:"identity"`
	Name     string `json:"name"`
	Color    string `json:"color"`
}

type ChatMessage struct {
	ID   string     `json:"id"`
	From ChatAuthor `json:"from"`
	Text string     `json:"text"`
	TS   int64      `json:"ts"`   // unix ms
	Kind string     `json:"kind"` // "user" | "system"
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

// Access is what a visitor already holds: the role their link or their
// remembered-device cookie grants, or AccessNone when they hold neither — in
// which case only the room password gets them in (as host).
const (
	AccessHost   = "host"
	AccessViewer = "viewer"
	AccessNone   = "none"
)

// RoomInfo is what an unauthenticated visitor learns from GET /api/room:
// enough to render the door, no more.
type RoomInfo struct {
	// Access is the best of what the visitor presented: the key in their
	// link and the cookie on their device ("host" | "viewer" | "none").
	Access string `json:"access"`
	// Occupants is how many people (projector excluded) are in the room, so
	// the door can say "3 people are watching". Zero when the key is invalid.
	Occupants int `json:"occupants"`
}

// Links is the link to send to friends. There is no host link: hosts get in
// with the password, and from then on with the cookie it sets. The viewer
// key rotates automatically once the room has stood empty for a while, and
// on demand by a host.
type Links struct {
	Viewer string `json:"viewer"`
}

// TokenRequest asks to join. A visitor needs one of: a viewer key from a
// link, the room password (grants host), or the remembered-device cookie
// from an earlier join. The best of what they present wins.
type TokenRequest struct {
	Name     string `json:"name"`
	Key      string `json:"key,omitempty"`
	Password string `json:"password,omitempty"`
	// Projector asks for the projector identity instead of a person's.
	// Requires host access (the password).
	Projector bool `json:"projector,omitempty"`
}

type TokenResponse struct {
	Token    string       `json:"token"`
	URL      string       `json:"url"`
	Identity string       `json:"identity"`
	Role     string       `json:"role"`
	Color    string       `json:"color"`
	Session  string       `json:"session"`
	Settings RoomSettings `json:"settings"`
	// Links is the current viewer link, so the client can put it in the
	// address bar and a host can hand it out without another request.
	Links Links `json:"links"`
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

	// Holding is true while waitForEveryone is parking the room at a
	// discontinuity until the slow clients catch up. The room reads as
	// paused as well — holding is the reason, not a second kind of pause.
	Holding bool `json:"holding"`
	// WaitingFor names the people still buffering, for the card that says so.
	WaitingFor []string `json:"waitingFor,omitempty"`

	// LastIntent is the most recent echo, for someone who joined after it
	// was broadcast — a late joiner still wants the loading card for the
	// film the room is in the middle of starting.
	LastIntent *PlaybackIntent `json:"lastIntent,omitempty"`
}

// Playback intent actions.
const (
	IntentSeek  = "seek"
	IntentPause = "pause"
	IntentPlay  = "play"
	IntentLoad  = "load"
	IntentStop  = "stop"
)

// PlaybackActor is who asked for something. The zero value is the server
// itself — the run loop advancing a playlist — which is why the name is
// allowed to be empty.
type PlaybackActor struct {
	Identity string `json:"identity"`
	Name     string `json:"name"`
	Color    string `json:"color"`
}

// PlaybackIntent is the echo: what a person just asked for, sent the moment
// the command lands rather than when its effects settle.
//
// The state broadcast alone cannot carry this. It says where the film is, not
// who moved it or where it was a moment ago, and by the time a client has
// applied it the picture has already jumped. Pause got a loud red indicator
// early; seeks got nothing, so a viewer saw the picture jump with no idea who
// did it or why.
type PlaybackIntent struct {
	Seq    int64         `json:"seq"`
	Action string        `json:"action"` // seek | pause | play | load | stop
	Actor  PlaybackActor `json:"actor"`
	// FromMS is where the room was; ToMS where it is going. Equal for a
	// pause or a play, which is how a seek's size is told from a nudge.
	FromMS int64 `json:"fromMs"`
	ToMS   int64 `json:"toMs"`
	// Title and MediaID are set for a load, so the card can name the film
	// before anyone has fetched a byte of it.
	Title   string `json:"title,omitempty"`
	MediaID string `json:"mediaId,omitempty"`
	TS      int64  `json:"ts"`
}

// PlaybackReady is a client telling the director whether it could start now.
//
// Gen matters as much as Ready: "I am buffered" is only an answer to the
// question the director is currently asking, and a report for a position the
// room has already left says nothing about the one it is waiting at.
type PlaybackReady struct {
	Gen             int64 `json:"gen"`
	BufferedAheadMS int64 `json:"bufferedAheadMs"`
	Ready           bool  `json:"ready"`
}
