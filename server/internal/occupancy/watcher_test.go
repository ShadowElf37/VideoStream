package occupancy

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/livekit/protocol/livekit"
)

type fakeLister struct {
	identities []string
	err        error
}

func (f *fakeLister) ListParticipants(context.Context, *livekit.ListParticipantsRequest) (*livekit.ListParticipantsResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	resp := &livekit.ListParticipantsResponse{}
	for _, id := range f.identities {
		resp.Participants = append(resp.Participants, &livekit.ParticipantInfo{Identity: id})
	}
	return resp, nil
}

func TestFiresOncePerStretchOfEmptiness(t *testing.T) {
	l := &fakeLister{identities: []string{"alice-ab12", "bob-cd34", "projector"}}
	fired := 0
	w := New(l, 2*time.Minute, func(context.Context) { fired++ }, nil)
	now := time.Date(2026, 9, 13, 20, 0, 0, 0, time.UTC)
	w.now = func() time.Time { return now }
	ctx := context.Background()

	w.Poll(ctx)
	if w.Count() != 2 {
		t.Fatalf("count = %d, want 2 (the projector is not a person)", w.Count())
	}

	// Everyone leaves. Not yet: a page refresh or a LiveKit restart must not
	// rotate the link out from under people.
	l.identities = nil
	now = now.Add(time.Minute)
	w.Poll(ctx)
	if fired != 0 {
		t.Fatal("fired after one minute of emptiness with a two-minute grace")
	}
	if w.Count() != 0 {
		t.Errorf("count = %d, want 0", w.Count())
	}

	now = now.Add(time.Minute)
	w.Poll(ctx)
	if fired != 1 {
		t.Fatalf("fired %d times at the grace boundary, want 1", fired)
	}

	// Still empty an hour later: nothing more happens.
	now = now.Add(time.Hour)
	w.Poll(ctx)
	if fired != 1 {
		t.Fatalf("fired again while the room stayed empty (%d)", fired)
	}

	// Someone comes back, then leaves again: it re-arms.
	l.identities = []string{"alice-ab12"}
	w.Poll(ctx)
	l.identities = nil
	now = now.Add(3 * time.Minute)
	w.Poll(ctx)
	if fired != 2 {
		t.Fatalf("did not re-arm after the room was used again (%d)", fired)
	}
}

func TestListErrorCountsAsEmpty(t *testing.T) {
	l := &fakeLister{identities: []string{"alice-ab12"}}
	fired := 0
	w := New(l, time.Minute, func(context.Context) { fired++ }, nil)
	now := time.Now()
	w.now = func() time.Time { return now }
	ctx := context.Background()

	w.Poll(ctx)
	l.err = errors.New("twirp error not_found: requested room does not exist")
	now = now.Add(2 * time.Minute)
	w.Poll(ctx)
	if w.Count() != 0 || fired != 1 {
		t.Errorf("count = %d, fired = %d; a missing room is an empty room", w.Count(), fired)
	}
}

func TestZeroGraceNeverFires(t *testing.T) {
	l := &fakeLister{identities: []string{"alice-ab12"}}
	fired := 0
	w := New(l, 0, func(context.Context) { fired++ }, nil)
	now := time.Now()
	w.now = func() time.Time { return now }
	w.Poll(context.Background())
	l.identities = nil
	now = now.Add(24 * time.Hour)
	w.Poll(context.Background())
	if fired != 0 {
		t.Error("fired with rotation disabled")
	}
}
