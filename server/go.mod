module github.com/ShadowElf37/VideoStream/server

go 1.25

require (
	github.com/ShadowElf37/VideoStream/proto v0.0.0
	github.com/livekit/protocol v1.51.0
	github.com/livekit/server-sdk-go/v2 v2.18.1
	golang.org/x/crypto v0.57.0
	modernc.org/sqlite v1.58.0
)

replace github.com/ShadowElf37/VideoStream/proto => ../proto
