package mpvhost

import (
	"fmt"
	"math"
	"time"

	"github.com/gen2brain/go-mpv"

	"github.com/ShadowElf37/VideoStream/proto"
)

// Observed property ids. The id is the reply userdata mpv echoes back.
const (
	pPause = iota + 1
	pTimePos
	pDuration
	pSpeed
	pChapter
	pChapterList
	pTrackList
	pMediaTitle
	pPath
	pSubDelay
	pAudioDelay
	pVolume
	pSubVisibility
	pIdleActive
	pSeeking
	pContainerFPS
	pWidth
	pHeight
	pEOFReached
	pAid
	pSid
)

type observedProp struct {
	id     uint64
	name   string
	format mpv.Format
}

// NOTE on formats: go-mpv v0.4.0 delivers MPV_FORMAT_FLAG property changes as
// a Go int (not bool), and mis-dereferences MPV_FORMAT_STRING property data
// (it reads the char** as the string itself and returns garbage). So flags go
// through asBool, and string properties are observed as nodes, whose
// conversion handles the union correctly.
var observed = []observedProp{
	{pPause, "pause", mpv.FormatFlag},
	{pTimePos, "time-pos", mpv.FormatDouble},
	{pDuration, "duration", mpv.FormatDouble},
	{pSpeed, "speed", mpv.FormatDouble},
	{pChapter, "chapter", mpv.FormatInt64},
	{pChapterList, "chapter-list", mpv.FormatNode},
	{pTrackList, "track-list", mpv.FormatNode},
	{pMediaTitle, "media-title", mpv.FormatNode},
	{pPath, "path", mpv.FormatNode},
	{pSubDelay, "sub-delay", mpv.FormatDouble},
	{pAudioDelay, "audio-delay", mpv.FormatDouble},
	{pVolume, "volume", mpv.FormatDouble},
	{pSubVisibility, "sub-visibility", mpv.FormatFlag},
	{pIdleActive, "idle-active", mpv.FormatFlag},
	{pSeeking, "seeking", mpv.FormatFlag},
	{pContainerFPS, "container-fps", mpv.FormatDouble},
	{pWidth, "width", mpv.FormatInt64},
	{pHeight, "height", mpv.FormatInt64},
	{pEOFReached, "eof-reached", mpv.FormatFlag},
	{pAid, "aid", mpv.FormatNode},
	{pSid, "sid", mpv.FormatNode},
}

func (h *Host) observe() {
	for _, p := range observed {
		if err := h.mpv.ObserveProperty(p.id, p.name, p.format); err != nil {
			h.log.Warn("mpv: observe failed", "property", p.name, "err", err)
		}
	}
}

// snapshot is the mutex-protected mirror of the observed properties.
type snapshot struct {
	seq        int64
	idle       bool
	pause      bool
	seeking    bool
	eof        bool
	timePos    float64
	duration   float64
	speed      float64
	chapter    int64
	chapters   []proto.MpvChapter
	tracks     []proto.MpvTrack
	mediaTitle string
	path       string
	subDelay   float64
	audioDelay float64
	volume     float64
	subVisible bool
	fps        float64
	width      int
	height     int
	aid        string
	sid        string
}

func (s *snapshot) init() {
	s.speed = 1
	s.volume = 100
	s.subVisible = true
	s.chapter = -1
	s.chapters = []proto.MpvChapter{}
	s.tracks = []proto.MpvTrack{}
}

// State returns the current mpv state for the mpv.state data packet. Fields the
// projector owns (encoder, bitrate, PLI, ...) are filled in by the caller.
func (h *Host) State() proto.MpvState {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := &h.snap
	s.seq++
	chapters := make([]proto.MpvChapter, len(s.chapters))
	copy(chapters, s.chapters)
	tracks := make([]proto.MpvTrack, len(s.tracks))
	copy(tracks, s.tracks)
	return proto.MpvState{
		Seq:           s.seq,
		Idle:          s.idle,
		Pause:         s.pause,
		TimePos:       s.timePos,
		Duration:      s.duration,
		Speed:         s.speed,
		Chapter:       s.chapter,
		Chapters:      chapters,
		Tracks:        tracks,
		MediaTitle:    s.mediaTitle,
		Path:          s.path,
		SubDelay:      s.subDelay,
		AudioDelay:    s.audioDelay,
		Volume:        s.volume,
		SubVisibility: s.subVisible,
		FPS:           s.fps,
		Width:         s.width,
		Height:        s.height,
	}
}

// Playing reports whether mpv is actively producing audio and video.
func (h *Host) Playing() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.snap.pause && !h.snap.idle && !h.snap.eof
}

// ContainerFPS is the source frame rate mpv reports, or 0 if unknown.
func (h *Host) ContainerFPS() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.snap.fps
}

// TimePos is the current playback position in seconds.
func (h *Host) TimePos() float64 {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.snap.timePos
}

func asFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	}
	return 0, false
}

func asInt(v any) (int64, bool) {
	switch x := v.(type) {
	case int64:
		return x, true
	case float64:
		return int64(x), true
	}
	return 0, false
}

// asBool copes with mpv flags arriving as bool, int or float depending on the
// path they took through the C API bindings.
func asBool(v any) (bool, bool) {
	switch x := v.(type) {
	case bool:
		return x, true
	case int:
		return x != 0, true
	case int32:
		return x != 0, true
	case int64:
		return x != 0, true
	case float64:
		return x != 0, true
	case string:
		return x == "yes" || x == "true" || x == "1", true
	}
	return false, false
}

func asString(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case bool:
		if x {
			return "yes", true
		}
		return "no", true
	case int64:
		return fmt.Sprint(x), true
	case float64:
		return fmt.Sprint(x), true
	}
	return "", false
}

func nodeTracks(v any) ([]proto.MpvTrack, bool) {
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]proto.MpvTrack, 0, len(list))
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		var t proto.MpvTrack
		if id, ok := asInt(m["id"]); ok {
			t.ID = id
		}
		t.Type, _ = m["type"].(string)
		t.Lang, _ = m["lang"].(string)
		t.Title, _ = m["title"].(string)
		t.Codec, _ = m["codec"].(string)
		t.Selected, _ = m["selected"].(bool)
		t.Default, _ = m["default"].(bool)
		t.Forced, _ = m["forced"].(bool)
		t.External, _ = m["external"].(bool)
		out = append(out, t)
	}
	return out, true
}

func nodeChapters(v any) ([]proto.MpvChapter, bool) {
	list, ok := v.([]any)
	if !ok {
		return nil, false
	}
	out := make([]proto.MpvChapter, 0, len(list))
	for _, e := range list {
		m, ok := e.(map[string]any)
		if !ok {
			continue
		}
		var c proto.MpvChapter
		c.Title, _ = m["title"].(string)
		if t, ok := asFloat(m["time"]); ok {
			c.Time = t
		}
		out = append(out, c)
	}
	return out, true
}

// trackLabel is the human name used in "Switched audio to ..." chat lines.
func trackLabel(tracks []proto.MpvTrack, kind, id string) string {
	if id == "" || id == "no" || id == "false" {
		return "none"
	}
	for _, t := range tracks {
		if t.Type != kind {
			continue
		}
		if fmt.Sprint(t.ID) != id {
			continue
		}
		switch {
		case t.Title != "" && t.Lang != "":
			return fmt.Sprintf("%s (%s)", t.Title, t.Lang)
		case t.Title != "":
			return t.Title
		case t.Lang != "":
			return t.Lang
		}
		return "track " + id
	}
	return "track " + id
}

// FormatTime renders seconds as h:mm:ss or mm:ss for chat lines.
func FormatTime(sec float64) string {
	if math.IsNaN(sec) || math.IsInf(sec, 0) || sec < 0 {
		sec = 0
	}
	total := int(sec + 0.5)
	h, m, s := total/3600, (total/60)%60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, s)
	}
	return fmt.Sprintf("%d:%02d", m, s)
}

func (h *Host) emit(kind, text string, data map[string]any) {
	ev := proto.MpvEvent{Type: kind, Text: text, TS: time.Now().UnixMilli(), Data: data}
	select {
	case h.events <- ev:
	default:
		h.log.Warn("mpv: event channel full, dropping", "type", kind)
	}
}

// eventLoop is the only caller of WaitEvent; it keeps the snapshot current and
// turns interesting transitions into proto.MpvEvent values.
func (h *Host) eventLoop() {
	for {
		select {
		case <-h.done:
			return
		default:
		}
		ev := h.mpv.WaitEvent(0.2)
		if ev == nil {
			continue
		}
		switch ev.EventID {
		case mpv.EventNone:
		case mpv.EventShutdown:
			return
		case mpv.EventPropertyChange:
			h.onProperty(ev)
		case mpv.EventFileLoaded:
			h.mu.Lock()
			title := h.snap.mediaTitle
			h.mu.Unlock()
			if title == "" {
				title = h.mpv.GetPropertyString("media-title")
			}
			h.emit("file-loaded", "Now playing: "+title, map[string]any{"title": title})
		case mpv.EventSeek:
			h.mu.Lock()
			pos := h.snap.timePos
			h.mu.Unlock()
			h.emit("seek", "Seeked to "+FormatTime(pos), map[string]any{"timePos": pos})
		case mpv.EventEnd:
			r := ev.EndFile().Reason
			h.emit("end-file", "Playback ended ("+r.String()+")", map[string]any{"reason": r.String()})
		case mpv.EventLogMsg:
			lm := ev.LogMessage()
			switch lm.Level {
			case "error", "fatal":
				h.emit("error", lm.Prefix+": "+trimNL(lm.Text), map[string]any{"prefix": lm.Prefix})
			}
		}
	}
}

func trimNL(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func (h *Host) onProperty(ev *mpv.Event) {
	p := ev.Property()
	h.mu.Lock()
	s := &h.snap
	var emits []func()
	switch ev.ReplyUserdata {
	case pPause:
		if b, ok := asBool(p.Data); ok && b != s.pause {
			s.pause = b
			pos := s.timePos
			if b {
				emits = append(emits, func() {
					h.emit("pause", "Host paused at "+FormatTime(pos), map[string]any{"timePos": pos})
				})
			} else {
				emits = append(emits, func() {
					h.emit("unpause", "Host resumed at "+FormatTime(pos), map[string]any{"timePos": pos})
				})
			}
		}
	case pTimePos:
		if v, ok := asFloat(p.Data); ok {
			s.timePos = v
		}
	case pDuration:
		if v, ok := asFloat(p.Data); ok {
			s.duration = v
		}
	case pSpeed:
		if v, ok := asFloat(p.Data); ok {
			s.speed = v
		}
	case pChapter:
		if v, ok := asInt(p.Data); ok {
			s.chapter = v
		}
	case pChapterList:
		if c, ok := nodeChapters(p.Data); ok {
			s.chapters = c
		}
	case pTrackList:
		if t, ok := nodeTracks(p.Data); ok {
			s.tracks = t
		}
	case pMediaTitle:
		if v, ok := asString(p.Data); ok {
			s.mediaTitle = v
		}
	case pPath:
		if v, ok := asString(p.Data); ok {
			s.path = v
		}
	case pSubDelay:
		if v, ok := asFloat(p.Data); ok {
			s.subDelay = v
		}
	case pAudioDelay:
		if v, ok := asFloat(p.Data); ok {
			s.audioDelay = v
		}
	case pVolume:
		if v, ok := asFloat(p.Data); ok {
			s.volume = v
		}
	case pSubVisibility:
		if v, ok := asBool(p.Data); ok {
			s.subVisible = v
		}
	case pIdleActive:
		if v, ok := asBool(p.Data); ok {
			s.idle = v
		}
	case pSeeking:
		if v, ok := asBool(p.Data); ok {
			s.seeking = v
		}
	case pContainerFPS:
		if v, ok := asFloat(p.Data); ok {
			s.fps = v
		}
	case pWidth:
		if v, ok := asInt(p.Data); ok {
			s.width = int(v)
		}
	case pHeight:
		if v, ok := asInt(p.Data); ok {
			s.height = int(v)
		}
	case pEOFReached:
		if v, ok := asBool(p.Data); ok {
			s.eof = v
		}
	case pAid:
		if v, ok := asString(p.Data); ok && v != s.aid {
			old := s.aid
			s.aid = v
			label := trackLabel(s.tracks, "audio", v)
			if old != "" {
				emits = append(emits, func() {
					h.emit("track-changed", "Switched audio to "+label,
						map[string]any{"kind": "audio", "id": v})
				})
			}
		}
	case pSid:
		if v, ok := asString(p.Data); ok && v != s.sid {
			old := s.sid
			s.sid = v
			label := trackLabel(s.tracks, "sub", v)
			if old != "" {
				emits = append(emits, func() {
					h.emit("track-changed", "Switched subtitles to "+label,
						map[string]any{"kind": "sub", "id": v})
				})
			}
		}
	}
	h.mu.Unlock()
	for _, f := range emits {
		f()
	}
}
