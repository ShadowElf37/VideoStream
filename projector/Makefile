# The projector is cgo: libmpv is found through pkg-config (tag "pkgconfig"),
# and libopus is used without libopusfile (tag "nolibopusfile").
TAGS := pkgconfig nolibopusfile
GO   ?= go

.PHONY: build test vet fmt clean run-dev

build:
	$(GO) build -tags "$(TAGS)" -o bin/projector ./cmd/projector

test:
	$(GO) test -tags "$(TAGS)" ./...

vet:
	$(GO) vet -tags "$(TAGS)" ./...

fmt:
	gofmt -w cmd internal

clean:
	rm -rf bin

# Local development against `livekit-server --dev` (key devkey / secret secret).
# Usage: make run-dev FILE=../testdata/sync-2min.mp4
FILE ?= ../testdata/sync-2min.mp4
ROOM ?= test
PRESET ?= 720p
run-dev: build
	@token=$$(lk token create --api-key devkey --api-secret secret --join \
		--room $(ROOM) --identity projector \
		--metadata '{"role":"projector","color":"#888888"}' \
		--valid-for 2h --token-only); \
	if [ -z "$$token" ]; then echo "could not mint a token; is lk installed?" >&2; exit 1; fi; \
	./bin/projector --url ws://localhost:7880 --token "$$token" \
		--preset $(PRESET) --media-root $(dir $(FILE)) $(FILE)
