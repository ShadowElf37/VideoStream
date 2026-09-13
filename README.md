# VideoStream

Self-hosted watch party. The host's machine runs a small **projector** binary that
embeds mpv, renders it into memory and streams it into a LiveKit room; friends open a
link in any browser and get a theater UI with voice chat, a chat sidebar, reactions and
three *independent* audio controls: mute my mic, deafen voices, mute/volume the movie.

See [docs/PLAN.md](docs/PLAN.md) for the architecture, evaluation and build phases, and
[deploy/README.md](deploy/README.md) for hosting on an Oracle Always Free VM.

```
deploy/     docker compose for the server (Caddy, LiveKit, app server)
            plus media/, the pushed library
server/     Go app server: rooms, tokens, chat history, serves the web build
web/        React + TypeScript watch-party client
projector/  Go + libmpv host-side streamer
proto/      data-channel message schemas shared by web/ and projector/
```

## Quick start (local, one machine)

Prerequisites on macOS: `brew install go node mpv opus ffmpeg livekit livekit-cli`.
(Linux: Go 1.26+, Node 22+, `libmpv-dev libopus-dev ffmpeg pkg-config`, plus a
LiveKit server binary.)

```sh
make web server projector          # builds the SPA into the server binary + the projector

# terminal 1: LiveKit
make dev-livekit
# terminal 2: app server (serves the web app on :8080)
cd server && LIVEKIT_API_KEY=devkey LIVEKIT_API_SECRET=secret ./bin/server
```

Open http://localhost:8080, create a room, and copy the three links it shows:

- **Invite link** — send to friends.
- **Host link** — open it yourself; it unlocks the transport bar and the Queue tab.
- **Projector link** — pass it to the projector on the machine that has the video:

```sh
./projector/bin/projector --room '<projector link>' --media-root ~/Videos ~/Videos/episode.mkv
```

The projector joins the room as "Projector" and starts streaming. Everyone, including
the host, watches in the browser. Host keys on the stage: `Space` pause, `←/→` ±5 s,
`↑/↓` ±60 s, `j` cycle subtitles, `#` cycle audio, `z`/`x` sub delay, `f` fullscreen.
The Queue tab browses the projector's media roots; "Open URL" plays anything yt-dlp can.

## Production

Run `deploy/oracle-setup.sh` on an Ubuntu 24.04 arm64 VM (see deploy/README.md), then
point friends at `https://<your domain>`. The projector talks to the same domain.

## Development

- `make test` runs Go vet/tests, the TypeScript check and the web unit tests.
- `cd web && npm run dev` serves the SPA on :5173 with `/api` proxied to :8080.
- `projector/cmd/clockprobe` is the experiment that established how mpv is clocked.
