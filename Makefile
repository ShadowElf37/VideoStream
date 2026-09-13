# Top-level build orchestration. Each component also builds on its own.
SHELL := /bin/bash
export PATH := /opt/homebrew/bin:$(PATH)

PROJECTOR_TAGS := pkgconfig nolibopusfile

.PHONY: all web server projector test dev-livekit dev-server dev-web clean

all: web server projector

## web: build the SPA into web/dist and copy it into the server's embed dir
web:
	cd web && npm ci --no-audit --no-fund && npm run sync-proto && npm run build
	rm -rf server/internal/web/dist && mkdir -p server/internal/web && cp -R web/dist server/internal/web/dist

## server: static binary (embeds whatever is in server/internal/web/dist)
server:
	cd server && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o bin/server ./cmd/server

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
	cd server && LIVEKIT_API_KEY=devkey LIVEKIT_API_SECRET=secret PUBLIC_URL=http://localhost:5173 LIVEKIT_URL=ws://localhost:7880 go run ./cmd/server

dev-web:
	cd web && npm run dev

clean:
	rm -rf web/dist server/bin projector/bin server/internal/web/dist
