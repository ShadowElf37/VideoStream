// Command houseprojector streams pushed .vsm files into party rooms from the
// server itself, so a watch party does not depend on anyone's home uplink or
// on someone remembering to start a binary.
//
// It is a pure-Go static binary: no libmpv, no libopus, no ffmpeg. All it
// does is read records, pace them and packetize them, which is the only shape
// that fits a machine with no hardware encoder (see internal/vsm).
//
// It owns no rooms of its own. The app server records which room wants the
// house projector, and this polls for that assignment, joining and leaving as
// it changes. That is what makes it feel like part of the site rather than a
// process someone has to run.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	lksdk "github.com/livekit/server-sdk-go/v2"

	"github.com/ShadowElf37/VideoStream/projector/internal/houseplayer"
	"github.com/ShadowElf37/VideoStream/projector/internal/mediafs"
	"github.com/ShadowElf37/VideoStream/projector/internal/publish"
	"github.com/ShadowElf37/VideoStream/proto"
)

func main() {
	var (
		server    = flag.String("server", "http://localhost:8080", "app server base URL")
		secret    = flag.String("secret", os.Getenv("HOUSE_SECRET"), "shared secret for the assignment API (or HOUSE_SECRET)")
		mediaRoot = flag.String("media", "/media", "directory of pushed .vsm files")
		poll      = flag.Duration("poll", 3*time.Second, "how often to ask the app server which room to serve")
		level     = flag.String("log-level", "info", "debug | info | warn | error")
	)
	flag.Parse()

	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(*level)); err != nil {
		lvl = slog.LevelInfo
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: lvl}))

	if *secret == "" {
		log.Error("no assignment secret; set --secret or HOUSE_SECRET")
		os.Exit(2)
	}
	roots, err := mediafs.NormalizeRoots([]string{*mediaRoot})
	if err != nil || len(roots) == 0 {
		log.Error("media root unusable", "path", *mediaRoot, "err", err)
		os.Exit(1)
	}
	log.Info("house projector starting", "server", *server, "media", roots[0])

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	a := &agent{log: log, server: strings.TrimRight(*server, "/"), secret: *secret, roots: roots}
	a.run(ctx, *poll)
}

// assignment is what the app server says this projector should be doing.
type assignment struct {
	RoomID       string `json:"roomId"`
	ProjectorKey string `json:"projectorKey"`
}

type agent struct {
	log    *slog.Logger
	server string
	secret string
	roots  []string

	cur    assignment
	cancel context.CancelFunc
	done   chan struct{}
}

func (a *agent) run(ctx context.Context, poll time.Duration) {
	t := time.NewTicker(poll)
	defer t.Stop()
	for {
		next, err := a.fetch(ctx)
		if err != nil && ctx.Err() == nil {
			a.log.Warn("house: assignment poll failed", "err", err)
		}
		if err == nil && next != a.cur {
			a.stopSession()
			a.cur = next
			if next.RoomID != "" {
				a.startSession(ctx, next)
			} else {
				a.log.Info("house: unassigned; idle")
			}
		}
		select {
		case <-ctx.Done():
			a.stopSession()
			return
		case <-t.C:
		}
	}
}

func (a *agent) fetch(ctx context.Context) (assignment, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.server+"/api/house/assignment", nil)
	if err != nil {
		return assignment{}, err
	}
	req.Header.Set("Authorization", "Bearer "+a.secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return assignment{}, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	if resp.StatusCode == http.StatusUnauthorized {
		return assignment{}, errors.New("assignment API rejected the secret")
	}
	if resp.StatusCode != http.StatusOK {
		return assignment{}, fmt.Errorf("assignment API: %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var out assignment
	if err := json.Unmarshal(body, &out); err != nil {
		return assignment{}, err
	}
	return out, nil
}

// startSession joins the assigned room and plays until it changes.
func (a *agent) startSession(ctx context.Context, as assignment) {
	sctx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.done = make(chan struct{})
	go func() {
		defer close(a.done)
		if err := a.session(sctx, as); err != nil && sctx.Err() == nil {
			a.log.Error("house: session ended", "room", as.RoomID, "err", err)
		}
	}()
}

func (a *agent) stopSession() {
	if a.cancel == nil {
		return
	}
	a.cancel()
	<-a.done
	a.cancel, a.done = nil, nil
}

func (a *agent) session(ctx context.Context, as assignment) error {
	wsURL, token, settings, err := a.token(ctx, as)
	if err != nil {
		return fmt.Errorf("token: %w", err)
	}

	var ctl *houseplayer.Controller
	cb := &lksdk.RoomCallback{
		OnDisconnected: func() { a.log.Warn("house: disconnected from room", "room", as.RoomID) },
		ParticipantCallback: lksdk.ParticipantCallback{
			OnDataPacket: func(data lksdk.DataPacket, params lksdk.DataReceiveParams) {
				if ctl != nil {
					ctl.OnDataPacket(data, params)
				}
			},
		},
	}
	pub, err := publish.Connect(a.log, wsURL, token, cb)
	if err != nil {
		return fmt.Errorf("connecting: %w", err)
	}
	defer pub.Disconnect()
	a.log.Info("house: joined", "room", as.RoomID)

	player := houseplayer.New(a.log, pub)
	ctl = houseplayer.NewController(a.log, player, &transport{pub}, a.roots, settings.AnyoneCanPause)

	go player.Run(ctx)
	go ctl.BroadcastLoop(ctx)

	<-ctx.Done()
	player.Stop()
	return nil
}

// token asks the app server for a projector token, exactly as the desktop
// projector does with a projector link.
func (a *agent) token(ctx context.Context, as assignment) (string, string, proto.RoomSettings, error) {
	var none proto.RoomSettings
	body, _ := json.Marshal(proto.TokenRequest{Name: "Projector", ProjectorKey: as.ProjectorKey})
	endpoint := a.server + "/api/rooms/" + url.PathEscape(as.RoomID) + "/token"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return "", "", none, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", none, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return "", "", none, fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(raw)))
	}
	var tr proto.TokenResponse
	if err := json.Unmarshal(raw, &tr); err != nil {
		return "", "", none, err
	}
	if tr.Token == "" || tr.URL == "" {
		return "", "", none, errors.New("token response missing token or url")
	}
	return tr.URL, tr.Token, tr.Settings, nil
}

// transport adapts the Publisher to what the controller needs, which is only
// "send this" and "who is that".
type transport struct{ pub *publish.Publisher }

func (t *transport) SendData(v any, topic string, reliable bool, to []string) error {
	return t.pub.SendData(v, topic, reliable, to)
}

func (t *transport) RoleOf(identity string) string {
	if identity == "" {
		return ""
	}
	return publish.RoleOf(t.pub.Room().GetParticipantByIdentity(identity))
}
