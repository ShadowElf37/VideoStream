package playback

import (
	"encoding/json"

	"github.com/ShadowElf37/VideoStream/proto"
)

func encode(st proto.PlaybackState) ([]byte, error) { return json.Marshal(st) }
