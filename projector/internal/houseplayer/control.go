package houseplayer

// The data-channel side of the house projector.
//
// It answers the same mpv.cmd surface as the desktop projector — vs/state,
// vs/fs.list, vs/load, cycle/set pause, seek — and broadcasts the same
// mpv.state and mpv.event. That is deliberate: the web client then cannot
// tell the two apart, so the transport bar, seek bar and Queue tab work
// against a server-hosted file with no changes at all.
//
// What it does not implement is what a pre-encoded file cannot offer:
// subtitle and audio track switching (burned in and discarded at push time)
// and quality presets (fixed by the encode). Those return an explicit error
// rather than silently doing nothing.

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	lksdk "github.com/livekit/server-sdk-go/v2"

	"github.com/ShadowElf37/VideoStream/projector/internal/mediafs"
	"github.com/ShadowElf37/VideoStream/projector/internal/publish"
	"github.com/ShadowElf37/VideoStream/proto"
)

// Transport is the publisher surface the controller needs.
type Transport interface {
	SendData(v any, topic string, reliable bool, to []string) error
	// Participant resolves an identity against the room's roster. It is the
	// fallback for working out a sender's role when the packet did not carry
	// the participant itself.
	Participant(identity string) *lksdk.RemoteParticipant
}

// Controller wires a Player to the room's data channel.
type Controller struct {
	log    *slog.Logger
	player *Player
	tx     Transport
	roots  []string

	seq            atomic.Int64
	anyoneCanPause atomic.Bool
}

// NewController creates the data-channel side. roots is the media library.
func NewController(log *slog.Logger, p *Player, tx Transport, roots []string, anyoneCanPause bool) *Controller {
	c := &Controller{log: log, player: p, tx: tx, roots: roots}
	c.anyoneCanPause.Store(anyoneCanPause)
	return c
}

// BroadcastLoop pushes state to the room at a steady rate so late joiners and
// reconnecting clients converge without asking.
func (c *Controller) BroadcastLoop(ctx context.Context) {
	t := time.NewTicker(250 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := c.tx.SendData(c.State(), proto.TopicMpvState, false, nil); err != nil {
				c.log.Debug("house: state broadcast failed", "err", err)
			}
		}
	}
}

// State renders the player as the MpvState the web client expects.
func (c *Controller) State() proto.MpvState {
	st := c.player.State()
	h := st.Header
	out := proto.MpvState{
		Seq:           c.seq.Add(1),
		Idle:          st.Idle,
		Pause:         st.Paused,
		TimePos:       float64(st.PosMS) / 1000,
		Duration:      float64(h.DurationMS) / 1000,
		Speed:         1,
		MediaTitle:    h.Title,
		Path:          st.Path,
		Volume:        100,
		SubVisibility: h.SubTrack > 0,
		Width:         h.Width,
		Height:        h.Height,
		// Named so the host stats read honestly: nothing is being encoded
		// here, the file arrived this way.
		Encoder:  "pre-encoded",
		Preset:   "file",
		Chapters: []proto.MpvChapter{},
		Tracks:   []proto.MpvTrack{},
	}
	if h.FPSDen > 0 {
		out.FPS = float64(h.FPSNum) / float64(h.FPSDen)
	}
	return out
}

// SetAnyoneCanPause follows the room setting, as the desktop projector does.
func (c *Controller) SetAnyoneCanPause(v bool) { c.anyoneCanPause.Store(v) }

// OnDataPacket handles mpv.cmd and settings updates.
func (c *Controller) OnDataPacket(data lksdk.DataPacket, params lksdk.DataReceiveParams) {
	u, ok := data.(*lksdk.UserDataPacket)
	if !ok {
		return
	}
	topic := u.Topic
	if topic == "" {
		topic = params.Topic
	}
	identity := params.SenderIdentity
	if identity == "" && params.Sender != nil {
		identity = params.Sender.Identity()
	}

	if topic == proto.TopicSettings {
		var st proto.RoomSettings
		if err := json.Unmarshal(u.Payload, &st); err == nil {
			c.anyoneCanPause.Store(st.AnyoneCanPause)
		}
		return
	}
	if topic != proto.TopicMpvCmd {
		return
	}

	var cmd proto.MpvCommand
	if err := json.Unmarshal(u.Payload, &cmd); err != nil {
		c.log.Warn("house: bad mpv.cmd payload", "err", err)
		return
	}
	if role := c.roleOf(params, identity); role != proto.RoleHost {
		if !(c.anyoneCanPause.Load() && IsPauseCommand(cmd.Cmd)) {
			c.log.Warn("house: rejecting mpv.cmd from non-host",
				"identity", identity, "role", role, "cmd", cmd.Cmd)
			return
		}
	}
	reply := c.dispatch(cmd)
	if err := c.tx.SendData(reply, proto.TopicMpvReply, true, []string{identity}); err != nil {
		c.log.Warn("house: reply failed", "err", err)
	}
}

// roleOf prefers the participant the packet arrived with and falls back to the
// room roster. The roster lookup alone is not enough: a command can arrive
// before the SDK has the sender in its participant list, and the role then
// reads as empty, which rejected the host's own commands.
func (c *Controller) roleOf(params lksdk.DataReceiveParams, identity string) string {
	if params.Sender != nil {
		if role := publish.RoleOf(params.Sender); role != "" {
			return role
		}
	}
	if identity == "" {
		return ""
	}
	return publish.RoleOf(c.tx.Participant(identity))
}

// IsPauseCommand matches exactly a pause toggle, so a viewer allowed to pause
// cannot reach anything else. Kept in step with the desktop projector's rule.
func IsPauseCommand(cmd []any) bool {
	if len(cmd) < 2 {
		return false
	}
	name, _ := cmd[0].(string)
	prop, _ := cmd[1].(string)
	if prop != "pause" {
		return false
	}
	switch name {
	case "cycle":
		return len(cmd) == 2
	case "set":
		return len(cmd) == 3
	}
	return false
}

func (c *Controller) dispatch(cmd proto.MpvCommand) proto.MpvReply {
	rep := proto.MpvReply{ID: cmd.ID}
	if len(cmd.Cmd) == 0 {
		rep.Error = "empty command"
		return rep
	}
	name, _ := cmd.Cmd[0].(string)
	c.log.Info("house: command", "cmd", cmd.Cmd)

	switch name {
	case "vs/state":
		rep.OK, rep.Data = true, c.State()

	case "vs/fs.list":
		list, err := mediafs.ListDir(c.roots, str(cmd.Cmd, 1))
		if err != nil {
			rep.Error = err.Error()
			return rep
		}
		rep.OK, rep.Data = true, list

	case "vs/load":
		target := str(cmd.Cmd, 1)
		if target == "" {
			rep.Error = "vs/load needs a path"
			return rep
		}
		if mediafs.IsURL(target) {
			// Nothing here can transcode, so a URL would need yt-dlp plus an
			// encoder. Say so rather than failing obscurely later.
			rep.Error = "the server projector plays pushed files only; URLs need the desktop projector"
			return rep
		}
		resolved, err := mediafs.Resolve(c.roots, target)
		if err != nil {
			rep.Error = err.Error()
			return rep
		}
		if err := c.player.Load(resolved); err != nil {
			rep.Error = err.Error()
			return rep
		}
		c.event("file-loaded", c.player.State().Header.Title)
		rep.OK = true

	case "cycle":
		if str(cmd.Cmd, 1) != "pause" {
			rep.Error = fmt.Sprintf("the server projector cannot cycle %q", str(cmd.Cmd, 1))
			return rep
		}
		paused := c.player.TogglePause()
		c.pauseEvent(paused)
		rep.OK = true

	case "set":
		if str(cmd.Cmd, 1) != "pause" {
			rep.Error = fmt.Sprintf("the server projector cannot set %q", str(cmd.Cmd, 1))
			return rep
		}
		v := truthy(cmd.Cmd, 2)
		c.player.SetPaused(v)
		c.pauseEvent(v)
		rep.OK = true

	case "seek":
		secs, err := number(cmd.Cmd, 1)
		if err != nil {
			rep.Error = err.Error()
			return rep
		}
		st := c.player.State()
		target := int64(secs * 1000)
		// mpv's default is a relative seek, and the web client relies on that
		// for the arrow keys.
		if mode := str(cmd.Cmd, 2); mode == "" || mode == "relative" {
			target = st.PosMS + target
		}
		if target > st.Header.DurationMS {
			target = st.Header.DurationMS
		}
		c.player.SeekMS(target)
		c.event("seek", "")
		rep.OK = true

	case "vs/quality":
		rep.Error = "quality is fixed when the file is pushed; re-push with a different --bitrate"

	case "stop":
		c.player.Stop()
		rep.OK = true

	default:
		rep.Error = fmt.Sprintf("the server projector does not support %q", name)
	}
	return rep
}

func (c *Controller) pauseEvent(paused bool) {
	if paused {
		c.event("pause", "")
	} else {
		c.event("unpause", "")
	}
}

func (c *Controller) event(kind, text string) {
	st := c.player.State()
	ev := proto.MpvEvent{
		Type: kind,
		Text: text,
		TS:   time.Now().UnixMilli(),
		Data: map[string]any{"timePos": float64(st.PosMS) / 1000},
	}
	if err := c.tx.SendData(ev, proto.TopicMpvEvent, true, nil); err != nil {
		c.log.Debug("house: event broadcast failed", "err", err)
	}
}

func str(cmd []any, i int) string {
	if i >= len(cmd) {
		return ""
	}
	s, _ := cmd[i].(string)
	return s
}

func number(cmd []any, i int) (float64, error) {
	if i >= len(cmd) {
		return 0, fmt.Errorf("missing numeric argument %d", i)
	}
	switch v := cmd[i].(type) {
	case float64:
		return v, nil
	case string:
		return strconv.ParseFloat(v, 64)
	}
	return 0, fmt.Errorf("argument %d is not a number", i)
}

func truthy(cmd []any, i int) bool {
	if i >= len(cmd) {
		return false
	}
	switch v := cmd[i].(type) {
	case bool:
		return v
	case string:
		return v == "yes" || v == "true" || v == "1"
	case float64:
		return v != 0
	}
	return false
}
