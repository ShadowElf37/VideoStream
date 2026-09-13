# syntax=docker/dockerfile:1
#
# Build with the REPOSITORY ROOT as context: the server module's replace
# directive points at ../proto, and the SPA the binary embeds is built from
# web/ in the first stage. From the repo root:
#   docker build -f server/Dockerfile -t videostream-server .
# deploy/docker-compose.yml does exactly that (context: ..).

# Web stage: builds the SPA. Without it the binary would embed only the
# placeholder internal/web/dist/index.html — web/dist is not in git.
FROM --platform=$BUILDPLATFORM node:22-bookworm-slim AS web
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm \
    npm ci --no-audit --no-fund
# sync-proto copies ../proto/messages.ts into src/proto/, so proto/ must be
# in place before the build runs.
COPY proto/ /src/proto/
COPY web/ ./
RUN npm run sync-proto && npm run build

# Build stage: cross-compiles a static binary for the target platform.
# modernc.org/sqlite is pure Go, so CGO_ENABLED=0 works fine and keeps the
# final image tiny (no libc/cgo toolchain needed).
FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS build
ARG TARGETARCH
WORKDIR /src

# Copy the whole module set needed to resolve the local proto replace
# directive, then download dependencies before copying source for better
# layer caching.
COPY proto/ ../proto/
COPY server/go.mod server/go.sum ./
RUN --mount=type=cache,target=/root/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

COPY server/ ./
# The SPA the server embeds (//go:embed dist in internal/web/web.go).
COPY --from=web /src/web/dist ./internal/web/dist
RUN --mount=type=cache,target=/root/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=linux GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" -o /out/server ./cmd/server

# Runtime stage: static binary on a minimal, CA-cert-equipped base.
FROM gcr.io/distroless/static-debian12:nonroot AS runtime
WORKDIR /app
COPY --from=build /out/server /app/server

ENV LISTEN=:8080
ENV DB_PATH=/data/videostream.db
VOLUME ["/data"]
EXPOSE 8080

ENTRYPOINT ["/app/server"]
