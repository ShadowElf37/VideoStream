# Top-level build orchestration. Each component also builds on its own.
SHELL := /bin/bash
export PATH := /opt/homebrew/bin:$(PATH)

PROJECTOR_TAGS := pkgconfig nolibopusfile

.PHONY: all web server projector vspush push test dev-livekit dev-server dev-web clean

all: web server projector

## web: build the SPA into web/dist and copy it into the server's embed dir
web:
	cd web && npm ci --no-audit --no-fund && npm run sync-proto && npm run build
	rm -rf server/internal/web/dist && mkdir -p server/internal/web && cp -R web/dist server/internal/web/dist

## server: static binary (embeds whatever is in server/internal/web/dist)
server:
	cd server && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/server ./cmd/server

## vspush: the push tool (transcode + pack + upload to the server)
vspush:
	go build -tags "$(PROJECTOR_TAGS)" -o projector/bin/vspush ./projector/cmd/vspush

## push: prepare a file and upload it to the server's media library.
##   make push FILE=~/Videos/ep01.mkv AID=2 SID=1
## AID/SID are mpv track numbers (1-based, per type); SID=0 burns no subtitles.
## HOST comes from deploy/.env if you have one, else pass HOST=user@ip.
PUSH_DIR ?= /home/ubuntu/videostream/deploy/media
AID ?= 0
SID ?= 0
push: vspush
	@test -n "$(FILE)" || { echo "usage: make push FILE=<video> [AID=2] [SID=1] [HOST=user@host]"; exit 2; }
	@test -n "$(HOST)" || { echo "set HOST=user@host (the server)"; exit 2; }
	./projector/bin/vspush --aid $(AID) --sid $(SID) --dest "$(HOST):$(PUSH_DIR)/" "$(FILE)"

## projector: cgo binary linked against system libmpv + libopus
projector:
	cd projector && go build -tags "$(PROJECTOR_TAGS)" -o bin/projector ./cmd/projector

test:
	cd proto && go vet ./...
	cd server && go vet ./... && go test ./...
	cd projector && go vet -tags "$(PROJECTOR_TAGS)" ./... && go test -tags "$(PROJECTOR_TAGS)" ./...
	cd web && npx tsc --noEmit && npx vitest run

## local development: three terminals
dev-livekit:
	livekit-server --config deploy/dev/livekit-dev.yaml

dev-server:
	cd server && ROOM_PASSWORD=dev LIVEKIT_API_KEY=devkey LIVEKIT_API_SECRET=secret PUBLIC_URL=http://localhost:5173 LIVEKIT_URL=ws://localhost:7880 go run ./cmd/server

dev-web:
	cd web && npm run dev

clean:
	rm -rf web/dist server/bin projector/bin server/internal/web/dist
