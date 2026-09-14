// Package chat implements chat history storage, broadcasting to LiveKit data
// channels, and per-identity rate limiting.
package chat

import (
	"context"

	"github.com/ShadowElf37/VideoStream/proto"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

// Broadcaster sends a payload to everyone in the room on a topic. It is an
// interface so tests can stub it out instead of talking to a real LiveKit
// server.
type Broadcaster interface {
	Broadcast(ctx context.Context, topic string, payload []byte) error
}

// LiveKitBroadcaster broadcasts via LiveKit's server-side SendData API.
type LiveKitBroadcaster struct {
	Client *lksdk.RoomServiceClient
}

// NewLiveKitBroadcaster wraps a RoomServiceClient as a Broadcaster.
func NewLiveKitBroadcaster(client *lksdk.RoomServiceClient) *LiveKitBroadcaster {
	return &LiveKitBroadcaster{Client: client}
}

func (b *LiveKitBroadcaster) Broadcast(ctx context.Context, topic string, payload []byte) error {
	t := topic
	_, err := b.Client.SendData(ctx, &livekit.SendDataRequest{
		Room:  proto.RoomID,
		Data:  payload,
		Kind:  livekit.DataPacket_RELIABLE,
		Topic: &t,
	})
	return err
}
