// Package publish owns the LiveKit participant: the room connection, the two
// published tracks, and our own RTP packetization.
//
// We packetize ourselves and call LocalTrack.WriteRTP rather than WriteSample,
// because WriteSample derives RTP timestamps by accumulating per-sample
// durations. At 24000/1001 fps that truncation costs about 0.7 s of A/V drift
// per hour. Our timestamps are absolute functions of the media clock, so they
// cannot drift. pion rewrites SSRC and payload type per binding; sequence
// numbers pass through untouched, so we own those.
package publish

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/rtp/codecs"
	"github.com/pion/webrtc/v4"

	"github.com/ShadowElf37/VideoStream/proto"
)

// MTU is the RTP payload budget; 1200 leaves room for SRTP and extensions.
const MTU = 1200

// Stats are the RTCP/throughput counters shown in the status line.
type Stats struct {
	PLI        int64
	FIR        int64
	NACK       int64
	VideoBytes int64
	AudioBytes int64
	VideoPkts  int64
	AudioPkts  int64
}

// Publisher is the projector's LiveKit participant.
type Publisher struct {
	log  *slog.Logger
	room *lksdk.Room

	mu       sync.Mutex
	video    *lksdk.LocalTrack
	audio    *lksdk.LocalTrack
	videoPub *lksdk.LocalTrackPublication
	audioPub *lksdk.LocalTrackPublication
	width    int
	height   int

	payloader codecs.H264Payloader
	vseq      uint16
	aseq      uint16

	// keyReq is called when a subscriber asks for a picture. An encoder that
	// can honour it turns a late joiner's two-second wait into one frame.
	keyReq atomic.Pointer[func()]

	pli   atomic.Int64
	fir   atomic.Int64
	nack  atomic.Int64
	vb    atomic.Int64
	ab    atomic.Int64
	vpkts atomic.Int64
	apkts atomic.Int64
}

// Connect joins the room without subscribing to anything (the projector never
// needs to receive media).
func Connect(log *slog.Logger, url, token string, cb *lksdk.RoomCallback) (*Publisher, error) {
	p := &Publisher{log: log}
	room, err := lksdk.ConnectToRoomWithToken(url, token, cb, lksdk.WithAutoSubscribe(false))
	if err != nil {
		return nil, fmt.Errorf("connect to room: %w", err)
	}
	p.room = room
	return p, nil
}

// Room exposes the SDK room for the control channel.
func (p *Publisher) Room() *lksdk.Room { return p.room }

// Identity is our own participant identity.
func (p *Publisher) Identity() string {
	if p.room == nil || p.room.LocalParticipant == nil {
		return ""
	}
	return p.room.LocalParticipant.Identity()
}

// PublishTracks creates and publishes movie.video and movie.audio.
func (p *Publisher) PublishTracks(width, height int) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.publishLocked(width, height)
}

func (p *Publisher) publishLocked(width, height int) error {
	video, err := lksdk.NewLocalTrack(webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeH264,
		ClockRate:   90000,
		SDPFmtpLine: "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f",
	}, lksdk.WithRTCPHandler(p.onRTCP))
	if err != nil {
		return fmt.Errorf("create video track: %w", err)
	}
	audio, err := lksdk.NewLocalTrack(webrtc.RTPCodecCapability{
		MimeType:    webrtc.MimeTypeOpus,
		ClockRate:   48000,
		Channels:    2,
		SDPFmtpLine: "minptime=10;useinbandfec=1;stereo=1;sprop-stereo=1",
	}, lksdk.WithRTCPHandler(p.onRTCP))
	if err != nil {
		return fmt.Errorf("create audio track: %w", err)
	}
	vpub, err := p.room.LocalParticipant.PublishTrack(video, &lksdk.TrackPublicationOptions{
		Name:        "movie.video",
		Source:      livekit.TrackSource_SCREEN_SHARE,
		VideoWidth:  width,
		VideoHeight: height,
	})
	if err != nil {
		return fmt.Errorf("publish video: %w", err)
	}
	apub, err := p.room.LocalParticipant.PublishTrack(audio, &lksdk.TrackPublicationOptions{
		Name:       "movie.audio",
		Source:     livekit.TrackSource_SCREEN_SHARE_AUDIO,
		Stereo:     true,
		DisableDTX: true,
		// NOTE: server-sdk-go v2.18.1 has no DisableRED option. We do our own
		// packetization and never emit RED, so nothing is lost on the wire.
	})
	if err != nil {
		return fmt.Errorf("publish audio: %w", err)
	}
	p.video, p.audio = video, audio
	p.videoPub, p.audioPub = vpub, apub
	p.width, p.height = width, height
	p.log.Info("publish: tracks live",
		"video", vpub.SID(), "audio", apub.SID(),
		"size", fmt.Sprintf("%dx%d", width, height))
	return nil
}

// Republish recreates both tracks after a full reconnect. RTP sequence numbers
// and timestamps keep running, so the media timeline is unbroken.
func (p *Publisher) Republish() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	w, h := p.width, p.height
	if p.videoPub != nil {
		_ = p.room.LocalParticipant.UnpublishTrack(p.videoPub.SID())
	}
	if p.audioPub != nil {
		_ = p.room.LocalParticipant.UnpublishTrack(p.audioPub.SID())
	}
	p.video, p.audio, p.videoPub, p.audioPub = nil, nil, nil, nil
	return p.publishLocked(w, h)
}

// SetKeyframeRequest installs what to do about a PLI or FIR. Until this is
// called they are only counted, which is all the ffmpeg path can do with them.
func (p *Publisher) SetKeyframeRequest(fn func()) { p.keyReq.Store(&fn) }

func (p *Publisher) onRTCP(pkt rtcp.Packet) {
	switch pkt.(type) {
	case *rtcp.PictureLossIndication:
		p.pli.Add(1)
		p.requestKeyframe()
	case *rtcp.FullIntraRequest:
		p.fir.Add(1)
		p.requestKeyframe()
	case *rtcp.TransportLayerNack:
		p.nack.Add(1)
	}
}

func (p *Publisher) requestKeyframe() {
	if fn := p.keyReq.Load(); fn != nil {
		(*fn)()
	}
}

// WriteVideo packetizes one access unit and writes it with the marker bit set
// on its last packet.
func (p *Publisher) WriteVideo(au []byte, ts90k uint32) error {
	p.mu.Lock()
	track := p.video
	payloads := p.payloader.Payload(MTU, au)
	seq := p.vseq
	p.vseq += uint16(len(payloads))
	p.mu.Unlock()
	if track == nil || len(payloads) == 0 {
		return nil
	}
	for i, payload := range payloads {
		pkt := &rtp.Packet{
			Header: rtp.Header{
				Version:        2,
				Marker:         i == len(payloads)-1,
				SequenceNumber: seq + uint16(i),
				Timestamp:      ts90k,
			},
			Payload: payload,
		}
		if err := track.WriteRTP(pkt, nil); err != nil {
			return err
		}
		p.vb.Add(int64(len(payload)))
		p.vpkts.Add(1)
	}
	return nil
}

// WriteAudio writes one 20 ms Opus frame as a single RTP packet.
func (p *Publisher) WriteAudio(payload []byte, ts48k uint32) error {
	p.mu.Lock()
	track := p.audio
	seq := p.aseq
	p.aseq++
	p.mu.Unlock()
	if track == nil || len(payload) == 0 {
		return nil
	}
	pkt := &rtp.Packet{
		Header: rtp.Header{
			Version:        2,
			Marker:         false,
			SequenceNumber: seq,
			Timestamp:      ts48k,
		},
		Payload: payload,
	}
	if err := track.WriteRTP(pkt, nil); err != nil {
		return err
	}
	p.ab.Add(int64(len(payload)))
	p.apkts.Add(1)
	return nil
}

// Stats returns the RTCP and throughput counters.
func (p *Publisher) Stats() Stats {
	return Stats{
		PLI:        p.pli.Load(),
		FIR:        p.fir.Load(),
		NACK:       p.nack.Load(),
		VideoBytes: p.vb.Load(),
		AudioBytes: p.ab.Load(),
		VideoPkts:  p.vpkts.Load(),
		AudioPkts:  p.apkts.Load(),
	}
}

// SendData publishes a JSON payload on a topic.
func (p *Publisher) SendData(v any, topic string, reliable bool, to []string) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	opts := []lksdk.DataPublishOption{
		lksdk.WithDataPublishTopic(topic),
		lksdk.WithDataPublishReliable(reliable),
	}
	if len(to) > 0 {
		opts = append(opts, lksdk.WithDataPublishDestination(to))
	}
	return p.room.LocalParticipant.PublishData(b, opts...)
}

// RoleOf reads a remote participant's token metadata and returns its role.
// A nil participant (an unknown sender) has no role, which denies it.
func RoleOf(pt *lksdk.RemoteParticipant) string {
	if pt == nil {
		return ""
	}
	var md proto.ParticipantMetadata
	if err := json.Unmarshal([]byte(pt.Metadata()), &md); err != nil {
		return ""
	}
	return md.Role
}

// Disconnect leaves the room.
func (p *Publisher) Disconnect() {
	if p.room != nil {
		p.room.Disconnect()
	}
}
