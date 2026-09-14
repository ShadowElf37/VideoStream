package tokens

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/ShadowElf37/VideoStream/proto"
	"github.com/livekit/protocol/auth"
	"github.com/livekit/protocol/livekit"
	lksdk "github.com/livekit/server-sdk-go/v2"
)

// tokenValidity is how long a minted LiveKit access token (and its paired
// session) remains valid; clients refresh via the token endpoint.
const tokenValidity = 6 * time.Hour

// TokenValidity is exported so callers can align session expiry with it.
func TokenValidity() time.Duration { return tokenValidity }

func boolPtr(b bool) *bool { return &b }

// MintLiveKitToken builds a signed LiveKit access token for a participant
// joining the room with the given identity, display name, role and color.
func MintLiveKitToken(apiKey, apiSecret, identity, name, role, color string) (string, error) {
	metadata, err := json.Marshal(proto.ParticipantMetadata{Role: role, Color: color})
	if err != nil {
		return "", fmt.Errorf("marshal participant metadata: %w", err)
	}

	grant := &auth.VideoGrant{
		RoomJoin:       true,
		Room:           proto.RoomID,
		CanPublish:     boolPtr(true),
		CanSubscribe:   boolPtr(role != proto.RoleProjector),
		CanPublishData: boolPtr(true),
		RoomAdmin:      role == proto.RoleHost,
	}
	if role == proto.RoleProjector {
		grant.CanPublishSources = []string{"screen_share", "screen_share_audio"}
	} else {
		grant.CanPublishSources = []string{"microphone", "screen_share", "screen_share_audio"}
	}

	at := auth.NewAccessToken(apiKey, apiSecret).
		SetIdentity(identity).
		SetName(name).
		SetMetadata(string(metadata)).
		SetVideoGrant(grant).
		SetValidFor(tokenValidity)

	return at.ToJWT()
}

// EnsureRoom makes sure the LiveKit room exists before a participant tries
// to join it. Production LiveKit deployments run with auto_create off.
// Errors are the caller's to log; they are not fatal to token issuance.
func EnsureRoom(ctx context.Context, client *lksdk.RoomServiceClient) error {
	_, err := client.CreateRoom(ctx, &livekit.CreateRoomRequest{
		Name:            proto.RoomID,
		EmptyTimeout:    300,
		MaxParticipants: 16,
	})
	return err
}
