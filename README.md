# VideoStream

Self-hosted watch party: an mpv-powered "projector" streams a local file into a
LiveKit room; friends join from a browser and get a theater UI with voice chat,
a chat sidebar, and independent mic / deafen / movie-audio controls.

See [docs/PLAN.md](docs/PLAN.md) for the architecture, evaluation and build phases.

```
deploy/     docker compose for the Oracle VM (Caddy, LiveKit, app server)
server/     Go app server: rooms, tokens, chat history, serves the web build
web/        React + TypeScript watch-party client
projector/  Go + libmpv host-side streamer
proto/      data-channel message schemas shared by web/ and projector/
```
