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
	"os"
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
	// Roster describes who the room is known to contain, for diagnosing why a
	// sender could not be identified.
	Roster() []string
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
	// Dispatch off the SDK's packet goroutine: resolving a role can wait on
	// the roster, and blocking here would stall every later packet behind it.
	go c.handle(cmd, params, identity, time.Now())
}

// handle runs a command and records how long each stage took. The timing is
// logged because "the projector felt slow" is otherwise impossible to pin on
// a component: it separates time spent working out who sent the command from
// time spent acting on it, and both from everything downstream.
func (c *Controller) handle(cmd proto.MpvCommand, params lksdk.DataReceiveParams, identity string, arrived time.Time) {
	role := c.roleOf(params, identity)
	authDone := time.Now()
	if role != proto.RoleHost {
		if !(c.anyoneCanPause.Load() && IsPauseCommand(cmd.Cmd)) {
			c.log.Warn("house: rejecting mpv.cmd from non-host",
				"identity", identity, "role", role, "cmd", cmd.Cmd,
				"senderAttached", params.Sender != nil,
				"roster", c.tx.Roster(),
				"waitedMs", time.Since(arrived).Milliseconds())
			// Answer the refusal. Dropping it leaves the client waiting for a
			// reply that never comes, which looks like a hang rather than a
			// permission problem.
			if err := c.tx.SendData(proto.MpvReply{ID: cmd.ID, Error: "host role required"},
				proto.TopicMpvReply, true, []string{identity}); err != nil {
				c.log.Debug("house: refusal reply failed", "err", err)
			}
			return
		}
	}
	reply := c.dispatch(cmd)
	done := time.Now()
	c.log.Info("house: command handled",
		"cmd", cmd.Cmd,
		"authMs", authDone.Sub(arrived).Milliseconds(),
		"actMs", done.Sub(authDone).Milliseconds(),
		"totalMs", done.Sub(arrived).Milliseconds())
	if err := c.tx.SendData(reply, proto.TopicMpvReply, true, []string{identity}); err != nil {
		c.log.Warn("house: reply failed", "err", err)
	}
}

// roleOf works out who sent a command.
//
// The packet's own participant is authoritative when it is there, but for the
// first commands after a join it is nil and the room roster does not have the
// sender yet either — the host's opening vs/fs.list was being rejected with an
// empty role about 300 ms before the same client's next command was accepted.
// Rather than reject a host for arriving early, give the roster a moment to
// catch up. This only ever waits when the role is genuinely unknown, which is
// rare and brief.
func (c *Controller) roleOf(params lksdk.DataReceiveParams, identity string) string {
	if params.Sender != nil {
		if role := publish.RoleOf(params.Sender); role != "" {
			return role
		}
	}
	if identity == "" {
		return ""
	}
	deadline := time.Now().Add(rosterWait)
	for {
		if role := publish.RoleOf(c.tx.Participant(identity)); role != "" {
			return role
		}
		if time.Now().After(deadline) {
			return ""
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// rosterWait bounds how long an unknown sender is given to appear in the
// roster. It only ever elapses for a command sent in the first moments of a
// join, and since commands are dispatched on their own goroutine it delays
// nothing else. Short, because a host who waits longer than this for a pause
// would rather be told it failed.
const rosterWait = 750 * time.Millisecond

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
		mode := str(cmd.Cmd, 2)
		if mode == "" {
			mode = "replace"
		}
		before := c.player.State().Path
		if err := c.player.Load(resolved, mode); err != nil {
			rep.Error = err.Error()
			return rep
		}
		// "append" behind something already playing changes nothing visible,
		// so only announce an actual switch.
		if st := c.player.State(); st.Path != before {
			c.event("file-loaded", st.Header.Title)
		}
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
		// No event here: the seek is asynchronous and lands on the keyframe at
		// or before the target, so anything announced now would be the old
		// position. The player reports the real landing through OnSeeked.
		rep.OK = true

	case "vs/rm":
		// Deleting media is host-only (enforced above) and confined to the
		// media roots by Resolve, which is the same allowlist browsing uses.
		// It exists because the alternative is SSHing to the box to free disk,
		// and a 45 GB volume fills after about fifteen episodes.
		target := str(cmd.Cmd, 1)
		if target == "" {
			rep.Error = "vs/rm needs a path"
			return rep
		}
		resolved, err := mediafs.Resolve(c.roots, target)
		if err != nil {
			rep.Error = err.Error()
			return rep
		}
		if st := c.player.State(); st.Path == resolved {
			// Removing the file underneath the reader would leave playback
			// reading a deleted inode until it ends.
			rep.Error = "that file is playing; load something else first"
			return rep
		}
		if err := c.removeQueued(resolved); err != nil {
			rep.Error = err.Error()
			return rep
		}
		if err := os.Remove(resolved); err != nil {
			rep.Error = err.Error()
			return rep
		}
		c.log.Info("house: deleted", "path", resolved)
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

// removeQueued drops a path from the playlist before it is deleted, so the
// queue cannot advance onto a file that is no longer there.
func (c *Controller) removeQueued(path string) error {
	c.player.Dequeue(path)
	return nil
}

// AnnounceLoaded broadcasts a file-loaded event, for playlist advances that
// no command asked for.
func (c *Controller) AnnounceLoaded(title string) { c.event("file-loaded", title) }

// AnnounceSeeked reports where a seek actually landed.
func (c *Controller) AnnounceSeeked(posMS int64) { c.eventAt("seek", "", posMS) }

func (c *Controller) pauseEvent(paused bool) {
	if paused {
		c.event("pause", "")
	} else {
		c.event("unpause", "")
	}
}

func (c *Controller) event(kind, text string) {
	c.eventAt(kind, text, c.player.State().PosMS)
}

func (c *Controller) eventAt(kind, text string, posMS int64) {
	ev := proto.MpvEvent{
		Type: kind,
		Text: text,
		TS:   time.Now().UnixMilli(),
		Data: map[string]any{"timePos": float64(posMS) / 1000},
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
