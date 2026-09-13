# syntax=docker/dockerfile:1
#
# Build with the repository root as context (needed for the ../proto
# replace directive), e.g. from the repo root:
#   docker build -f server/Dockerfile -t videostream-server .

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
