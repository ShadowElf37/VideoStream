package api

import (
	"github.com/ShadowElf37/VideoStream/proto"
	"github.com/ShadowElf37/VideoStream/server/internal/config"
	"github.com/ShadowElf37/VideoStream/server/internal/media"
)

// mediaResolver lets the director turn a media id into something playable
// without knowing anything about the library or about signing.
type mediaResolver struct {
	cfg *config.Config
	lib *media.Library
}

func (m *mediaResolver) Resolve(mediaID string) (int64, string, string, error) {
	meta, err := m.lib.Get(mediaID)
	if err != nil {
		return 0, "", "", err
	}
	url := media.URL(m.cfg.SessionSecret, mediaID, proto.MovieFileName, media.DefaultTTL)
	return meta.DurationMS, meta.Title, url, nil
}
