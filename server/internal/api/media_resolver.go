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
	return meta.DurationMS, meta.Title, PlayURL(m.cfg.SessionSecret, meta), nil
}

// PlayURL is what a client should point its player at: the HLS master
// playlist when the title has renditions, and the plain MP4 when it does not.
//
// The distinction is the title's age, not the client's: a title pushed before
// renditions existed has an unfragmented MP4 with no byte ranges to publish,
// and progressive download is exactly what it was written for.
func PlayURL(secret []byte, meta proto.MediaMeta) string {
	if len(meta.Renditions) > 0 {
		return media.URL(secret, meta.ID, proto.MasterPlaylistName, media.DefaultTTL)
	}
	return media.URL(secret, meta.ID, proto.MovieFileName, media.DefaultTTL)
}
