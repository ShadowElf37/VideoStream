package tokens

import (
	"encoding/json"
	"testing"

	"github.com/ShadowElf37/VideoStream/proto"
	"github.com/livekit/protocol/auth"
)

func decodeGrants(t *testing.T, jwt, apiKey, apiSecret string) *auth.ClaimGrants {
	t.Helper()
	verifier, err := auth.ParseAPIToken(jwt)
	if err != nil {
		t.Fatalf("ParseAPIToken: %v", err)
	}
	if verifier.APIKey() != apiKey {
		t.Fatalf("issuer = %q, want %q", verifier.APIKey(), apiKey)
	}
	_, grants, err := verifier.Verify(apiSecret)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	return grants
}

func TestMintLiveKitTokenViewerGrants(t *testing.T) {
	jwt, err := MintLiveKitToken("key", "secret-at-least-32-bytes-long!!", "room1", "alice-ab12", "Alice", proto.RoleViewer, "#e57373")
	if err != nil {
		t.Fatalf("MintLiveKitToken: %v", err)
	}

	grants := decodeGrants(t, jwt, "key", "secret-at-least-32-bytes-long!!")
	if grants.Identity != "alice-ab12" {
		t.Errorf("identity = %q", grants.Identity)
	}
	if grants.Name != "Alice" {
		t.Errorf("name = %q", grants.Name)
	}
	vg := grants.Video
	if vg == nil {
		t.Fatal("expected video grant")
	}
	if !vg.RoomJoin || vg.Room != "room1" {
		t.Errorf("roomJoin/room = %v/%q", vg.RoomJoin, vg.Room)
	}
	if vg.CanPublish == nil || !*vg.CanPublish {
		t.Error("expected canPublish true")
	}
	if vg.CanSubscribe == nil || !*vg.CanSubscribe {
		t.Error("expected canSubscribe true for viewer")
	}
	if vg.RoomAdmin {
		t.Error("viewer should not have roomAdmin")
	}
	wantSources := []string{"microphone", "screen_share", "screen_share_audio"}
	if !stringSlicesEqual(vg.CanPublishSources, wantSources) {
		t.Errorf("canPublishSources = %v, want %v", vg.CanPublishSources, wantSources)
	}

	var md proto.ParticipantMetadata
	if err := json.Unmarshal([]byte(grants.Metadata), &md); err != nil {
		t.Fatalf("unmarshal metadata: %v", err)
	}
	if md.Role != proto.RoleViewer || md.Color != "#e57373" {
		t.Errorf("metadata = %+v", md)
	}
}

func TestMintLiveKitTokenHostGrants(t *testing.T) {
	jwt, err := MintLiveKitToken("key", "secret-at-least-32-bytes-long!!", "room1", "bob-cd34", "Bob", proto.RoleHost, "#4dd0e1")
	if err != nil {
		t.Fatalf("MintLiveKitToken: %v", err)
	}
	grants := decodeGrants(t, jwt, "key", "secret-at-least-32-bytes-long!!")
	if !grants.Video.RoomAdmin {
		t.Error("host should have roomAdmin")
	}
}

func TestMintLiveKitTokenProjectorGrants(t *testing.T) {
	jwt, err := MintLiveKitToken("key", "secret-at-least-32-bytes-long!!", "room1", "projector", "Projector", proto.RoleProjector, "#ba68c8")
	if err != nil {
		t.Fatalf("MintLiveKitToken: %v", err)
	}
	grants := decodeGrants(t, jwt, "key", "secret-at-least-32-bytes-long!!")
	vg := grants.Video
	// Subscribe is granted even though the projector subscribes to nothing:
	// LiveKit withholds the participant roster from anyone who cannot
	// subscribe, and without the roster the projector cannot tell whether a
	// command came from the host. It joins with AutoSubscribe off.
	if vg.CanSubscribe == nil || !*vg.CanSubscribe {
		t.Error("projector needs subscribe permission to receive the participant roster")
	}
	// It must still not be able to publish a microphone.
	wantSources := []string{"screen_share", "screen_share_audio"}
	if !stringSlicesEqual(vg.CanPublishSources, wantSources) {
		t.Errorf("canPublishSources = %v, want %v", vg.CanPublishSources, wantSources)
	}
}

func stringSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSlugifyAndIdentity(t *testing.T) {
	cases := map[string]string{
		"Alice":      "alice",
		"  Bob Cat ": "bob-cat",
		"!!!":        "user",
		"a__b--c":    "a-b-c",
	}
	for in, want := range cases {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}

	id := NewIdentity("Alice")
	if len(id) != len("alice")+1+4 {
		t.Errorf("NewIdentity length = %d for %q", len(id), id)
	}
}

func TestColorForIsDeterministic(t *testing.T) {
	a := ColorFor("alice-ab12")
	b := ColorFor("alice-ab12")
	if a != b {
		t.Errorf("ColorFor not deterministic: %q vs %q", a, b)
	}
	if a == "" {
		t.Error("expected non-empty color")
	}
}
