package api

import (
	"fmt"
	"net/http"

	"github.com/ShadowElf37/VideoStream/proto"
)

var allowedPresets = map[string]bool{
	proto.Preset1080pHigh: true,
	proto.Preset1080p:     true,
	proto.Preset720p:      true,
	proto.Preset540p:      true,
}

type settingsPatch struct {
	AnyoneCanPause    *bool   `json:"anyoneCanPause"`
	DeafenImpliesMute *bool   `json:"deafenImpliesMute"`
	MaxPreset         *string `json:"maxPreset"`
}

func (s *Server) handlePatchSettings(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sess := sessionFromContext(r.Context())
	if sess.Role != proto.RoleHost {
		writeError(w, http.StatusForbidden, "host role required")
		return
	}

	room, err := s.getRoomOr404(w, r.Context(), id)
	if err != nil {
		return
	}

	var patch settingsPatch
	if !decodeJSON(w, r, &patch) {
		return
	}
	if patch.MaxPreset != nil && !allowedPresets[*patch.MaxPreset] {
		writeError(w, http.StatusBadRequest, "maxPreset must be one of 1080p-high, 1080p, 720p, 540p")
		return
	}

	settings := room.Settings
	var systemLines []string

	if patch.AnyoneCanPause != nil && *patch.AnyoneCanPause != settings.AnyoneCanPause {
		settings.AnyoneCanPause = *patch.AnyoneCanPause
		systemLines = append(systemLines, fmt.Sprintf("Host changed room settings: anyone can pause = %s", onOff(settings.AnyoneCanPause)))
	}
	if patch.DeafenImpliesMute != nil && *patch.DeafenImpliesMute != settings.DeafenImpliesMute {
		settings.DeafenImpliesMute = *patch.DeafenImpliesMute
		systemLines = append(systemLines, fmt.Sprintf("Host changed room settings: deafen implies mute = %s", onOff(settings.DeafenImpliesMute)))
	}
	if patch.MaxPreset != nil && *patch.MaxPreset != settings.MaxPreset {
		settings.MaxPreset = *patch.MaxPreset
		systemLines = append(systemLines, fmt.Sprintf("Host changed room settings: max quality = %s", settings.MaxPreset))
	}

	if err := s.rooms.UpdateSettings(r.Context(), id, settings); err != nil {
		s.logger.Error("update settings failed", "err", err)
		writeError(w, http.StatusInternalServerError, "internal error")
		return
	}

	if err := s.chat.BroadcastSettings(r.Context(), id, settings); err != nil {
		s.logger.Warn("broadcast settings failed", "room", id, "err", err)
	}
	for _, line := range systemLines {
		if err := s.chat.System(r.Context(), id, line); err != nil {
			s.logger.Warn("post settings system message failed", "room", id, "err", err)
		}
	}

	writeJSON(w, http.StatusOK, settings)
}

func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
