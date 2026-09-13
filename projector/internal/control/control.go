// Package control is the projector's data-channel side: it accepts mpv.cmd
// from host participants, replies on mpv.reply, and broadcasts mpv.state and
// mpv.event to everyone.
package control

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	lksdk "github.com/livekit/server-sdk-go/v2"

	"github.com/ShadowElf37/VideoStream/projector/internal/encoder"
	"github.com/ShadowElf37/VideoStream/projector/internal/mpvhost"
	"github.com/ShadowElf37/VideoStream/projector/internal/publish"
	"github.com/ShadowElf37/VideoStream/proto"
)

// Deps are the things the controller drives.
type Deps struct {
	Log  *slog.Logger
	Host *mpvhost.Host
	Pub  *publish.Publisher
	// Roots is the media-root allowlist (absolute, symlink-resolved).
	Roots []string
	// MaxPreset caps vs/quality.
	MaxPreset string
	// SetQuality switches the encoder preset.
	SetQuality func(preset string) error
	// State returns the full state, with the projector-owned fields filled in.
	State func() proto.MpvState
	// OnEvent is called for every mpv event before it is broadcast, so the
	// media clock can react (a seek drains the stale audio in the FIFO).
	OnEvent func(proto.MpvEvent)
}

// Controller handles inbound data packets and outbound state/events.
type Controller struct {
	deps Deps
	log  *slog.Logger

	mu       sync.Mutex
	lastSent time.Time
	dirty    bool
	wake     chan struct{}
}

// New creates a controller.
func New(deps Deps) *Controller {
	if deps.MaxPreset == "" {
		deps.MaxPreset = proto.Preset1080pHigh
	}
	return &Controller{deps: deps, log: deps.Log, wake: make(chan struct{}, 1)}
}

// Touch marks the state dirty so it is broadcast on the next loop iteration.
func (c *Controller) Touch() {
	c.mu.Lock()
	c.dirty = true
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

// Run broadcasts state at 4 Hz (and immediately on change) and forwards mpv
// events to the room until ctx is cancelled.
func (c *Controller) Run(ctx context.Context) {
	tick := time.NewTicker(250 * time.Millisecond)
	defer tick.Stop()
	events := c.deps.Host.Events()
	for {
		select {
		case <-ctx.Done():
			return
		case ev := <-events:
			c.log.Info("mpv event", "type", ev.Type, "text", ev.Text)
			if c.deps.OnEvent != nil {
				c.deps.OnEvent(ev)
			}
			if err := c.deps.Pub.SendData(ev, proto.TopicMpvEvent, true, nil); err != nil {
				c.log.Warn("control: event publish failed", "err", err)
			}
			c.Touch()
		case <-c.wake:
			c.broadcastState()
		case <-tick.C:
			c.broadcastState()
		}
	}
}

func (c *Controller) broadcastState() {
	c.mu.Lock()
	c.dirty = false
	c.lastSent = time.Now()
	c.mu.Unlock()
	st := c.deps.State()
	if err := c.deps.Pub.SendData(st, proto.TopicMpvState, false, nil); err != nil {
		c.log.Debug("control: state publish failed", "err", err)
	}
}

// OnDataPacket is wired to lksdk.RoomCallback.OnDataPacket.
func (c *Controller) OnDataPacket(data lksdk.DataPacket, params lksdk.DataReceiveParams) {
	u, ok := data.(*lksdk.UserDataPacket)
	if !ok {
		return
	}
	topic := u.Topic
	if topic == "" {
		topic = params.Topic
	}
	if topic != proto.TopicMpvCmd {
		return
	}
	identity := params.SenderIdentity
	if identity == "" && params.Sender != nil {
		identity = params.Sender.Identity()
	}
	sender := params.Sender
	if sender == nil && identity != "" {
		sender = c.deps.Pub.Room().GetParticipantByIdentity(identity)
	}
	if role := publish.RoleOf(sender); role != proto.RoleHost {
		c.log.Warn("control: rejecting mpv.cmd from non-host",
			"identity", identity, "role", role)
		return
	}

	var cmd proto.MpvCommand
	if err := json.Unmarshal(u.Payload, &cmd); err != nil {
		c.log.Warn("control: bad mpv.cmd payload", "err", err)
		return
	}
	reply := c.dispatch(cmd)
	if err := c.deps.Pub.SendData(reply, proto.TopicMpvReply, true, []string{identity}); err != nil {
		c.log.Warn("control: reply publish failed", "err", err)
	}
	c.Touch()
}

func (c *Controller) dispatch(cmd proto.MpvCommand) proto.MpvReply {
	rep := proto.MpvReply{ID: cmd.ID}
	if len(cmd.Cmd) == 0 {
		rep.Error = "empty command"
		return rep
	}
	name, _ := cmd.Cmd[0].(string)
	c.log.Info("control: command", "cmd", cmd.Cmd)

	if strings.HasPrefix(name, "vs/") {
		data, err := c.virtual(name, cmd.Cmd)
		if err != nil {
			rep.Error = err.Error()
			return rep
		}
		rep.OK, rep.Data = true, data
		return rep
	}
	data, err := c.deps.Host.Command(cmd.Cmd)
	if err != nil {
		rep.Error = err.Error()
		return rep
	}
	rep.OK, rep.Data = true, data
	return rep
}

func arg(cmd []any, i int) string {
	if i >= len(cmd) {
		return ""
	}
	s, _ := cmd[i].(string)
	return s
}

func (c *Controller) virtual(name string, cmd []any) (any, error) {
	switch name {
	case "vs/state":
		return c.deps.State(), nil

	case "vs/fs.list":
		list, err := ListDir(c.deps.Roots, arg(cmd, 1))
		if err != nil {
			return nil, err
		}
		return list, nil

	case "vs/load":
		target := arg(cmd, 1)
		if target == "" {
			return nil, fmt.Errorf("vs/load needs a path or URL")
		}
		mode := arg(cmd, 2)
		if mode == "" {
			mode = "replace"
		}
		if mode != "replace" && mode != "append" && mode != "append-play" {
			return nil, fmt.Errorf("vs/load: bad mode %q", mode)
		}
		if !IsURL(target) {
			resolved, err := Resolve(c.deps.Roots, target)
			if err != nil {
				return nil, err
			}
			target = resolved
		}
		if _, err := c.deps.Host.Command([]any{"loadfile", target, mode}); err != nil {
			return nil, err
		}
		return map[string]any{"loaded": target, "mode": mode}, nil

	case "vs/quality":
		preset := arg(cmd, 1)
		if encoder.PresetRank(preset) < 0 {
			return nil, fmt.Errorf("unknown preset %q", preset)
		}
		if encoder.PresetRank(preset) < encoder.PresetRank(c.deps.MaxPreset) {
			return nil, fmt.Errorf("preset %q is above the configured maximum %q", preset, c.deps.MaxPreset)
		}
		if c.deps.SetQuality == nil {
			return nil, fmt.Errorf("quality switching is not available")
		}
		if err := c.deps.SetQuality(preset); err != nil {
			return nil, err
		}
		return map[string]any{"preset": preset}, nil
	}
	return nil, fmt.Errorf("unknown virtual command %q", name)
}
