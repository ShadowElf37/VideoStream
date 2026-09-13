package chat

import (
	"encoding/json"

	"github.com/ShadowElf37/VideoStream/proto"
)

func marshalMessage(msg proto.ChatMessage) ([]byte, error) {
	return json.Marshal(msg)
}
