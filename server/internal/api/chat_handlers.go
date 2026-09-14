package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/ShadowElf37/VideoStream/proto"
	"github.com/ShadowElf37/VideoStream/server/internal/chat"
)

const (
	defaultChatLimit = 100
	maxChatLimit     = 200
)

func (s *Server) handleGetChat(w http.ResponseWriter, r *http.Request) {
	var before int64
	if v := r.URL.Query().Get("before"); v != "" {
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil || parsed < 0 {
			writeError(w, http.StatusBadRequest, "before must be a non-negative integer (unix ms)")
			return
		}
		before = parsed
	}

	limit := defaultChatLimit
	if v := r.URL.Query().Get("limit"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < 1 {
			writeError(w, http.StatusBadRequest, "limit must be a positive integer")
			return
		}
		limit = parsed
	}
	if limit > maxChatLimit {
		limit = maxChatLimit
	}

	msgs, err := s.chat.History(r.Context(), before, limit)
	if err != nil {
		s.logger.Error("get chat history failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	writeJSON(w, http.StatusOK, proto.ChatHistoryResponse{Messages: msgs})
}

func (s *Server) handlePostChat(w http.ResponseWriter, r *http.Request) {
	sess := sessionFromContext(r.Context())

	var body struct {
		Text string `json:"text"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}

	msg, err := s.chat.PostMessage(r.Context(), sess.Identity, sess.Name, sess.Color, body.Text)
	switch {
	case err == nil:
		writeJSON(w, http.StatusCreated, msg)
	case errors.Is(err, chat.ErrEmptyText), errors.Is(err, chat.ErrTextTooLong):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, chat.ErrRateLimited):
		writeError(w, http.StatusTooManyRequests, err.Error())
	default:
		s.logger.Error("post chat message failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
	}
}
