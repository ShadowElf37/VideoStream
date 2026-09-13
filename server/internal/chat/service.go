package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/ShadowElf37/VideoStream/proto"
	"github.com/ShadowElf37/VideoStream/server/internal/store"
)

// ErrEmptyText and ErrTextTooLong are validation errors for PostMessage.
var (
	ErrEmptyText   = errors.New("text must not be empty")
	ErrTextTooLong = errors.New("text must be at most 2000 characters")
	ErrRateLimited = errors.New("rate limited")
)

const maxTextLen = 2000

// Service stores chat messages and rebroadcasts them over LiveKit data
// channels.
type Service struct {
	store       *store.Store
	broadcaster Broadcaster
	limiter     *Limiter
}

// NewService creates a chat Service. broadcaster may be a stub in tests.
func NewService(st *store.Store, broadcaster Broadcaster) *Service {
	return &Service{
		store:       st,
		broadcaster: broadcaster,
		limiter:     NewLimiter(5, 2*time.Second),
	}
}

// PostMessage validates, stores, and broadcasts a user chat message.
func (s *Service) PostMessage(ctx context.Context, roomID, identity, name, color, text string) (proto.ChatMessage, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return proto.ChatMessage{}, ErrEmptyText
	}
	if len(text) > maxTextLen {
		return proto.ChatMessage{}, ErrTextTooLong
	}
	if !s.limiter.Allow(identity) {
		return proto.ChatMessage{}, ErrRateLimited
	}

	msg := proto.ChatMessage{
		ID:     store.NewID(16),
		RoomID: roomID,
		From:   proto.ChatAuthor{Identity: identity, Name: name, Color: color},
		Text:   text,
		TS:     time.Now().UnixMilli(),
		Kind:   "user",
	}

	if err := s.store.InsertMessage(ctx, msg); err != nil {
		return proto.ChatMessage{}, fmt.Errorf("store message: %w", err)
	}
	s.broadcast(ctx, roomID, msg)
	return msg, nil
}

// System stores and broadcasts a system-authored chat line, e.g. for
// settings changes or presence events.
func (s *Service) System(ctx context.Context, roomID, text string) error {
	msg := proto.ChatMessage{
		ID:     store.NewID(16),
		RoomID: roomID,
		From:   proto.ChatAuthor{Identity: "system", Name: "System", Color: "#9e9e9e"},
		Text:   text,
		TS:     time.Now().UnixMilli(),
		Kind:   "system",
	}
	if err := s.store.InsertMessage(ctx, msg); err != nil {
		return fmt.Errorf("store system message: %w", err)
	}
	s.broadcast(ctx, roomID, msg)
	return nil
}

// History returns up to limit messages older than beforeMS (0 = most
// recent), oldest-first.
func (s *Service) History(ctx context.Context, roomID string, beforeMS int64, limit int) ([]proto.ChatMessage, error) {
	msgs, err := s.store.ListMessages(ctx, roomID, beforeMS, limit)
	if err != nil {
		return nil, fmt.Errorf("list messages: %w", err)
	}
	return msgs, nil
}

// BroadcastSettings publishes the full room settings on the settings topic.
func (s *Service) BroadcastSettings(ctx context.Context, roomID string, settings proto.RoomSettings) error {
	payload, err := json.Marshal(settings)
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}
	return s.broadcaster.Broadcast(ctx, roomID, proto.TopicSettings, payload)
}

func (s *Service) broadcast(ctx context.Context, roomID string, msg proto.ChatMessage) {
	payload, err := marshalMessage(msg)
	if err != nil {
		slog.Warn("chat: marshal message for broadcast failed", "err", err)
		return
	}
	if err := s.broadcaster.Broadcast(ctx, roomID, proto.TopicChat, payload); err != nil {
		slog.Warn("chat: broadcast failed", "room", roomID, "err", err)
	}
}
