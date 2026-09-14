# VideoStream

Self-hosted watch party, Plex-shaped: titles are pushed to a small server ahead
of time, the server owns playback time, and friends open one link in any
browser to get the film, voice chat, a chat sidebar and three *independent*
audio controls: mute my mic, deafen voices, mute/volume the movie. A desktop
**projector** (mpv streamed live from someone's machine) is a separate mode
behind a button, for URLs and anything not pushed.

See [docs/PLAN.md](docs/PLAN.md) for the architecture and how it got here,
[deploy/README.md](deploy/README.md) for hosting on an Oracle Always Free VM,
and the GitHub issues for what is next.

```
deploy/     docker compose for the server (Caddy, LiveKit, app server)
            plus media/, the pushed library
server/     Go app server: the door, tokens, chat history, the playback
            director, the media library, serves the web build
web/        React + TypeScript watch-party client
projector/  Go + libmpv desktop streamer, and vspush (the push tool)
proto/      message and API contract shared by web/, server/ and projector/
```

## The door

There is one room. Its URL is the site; three things get through:

- **The room password** (`ROOM_PASSWORD` on the server) — what a host types.
  Once per device: the server sets a long-lived cookie.
- **The invite link** (`https://<host>/?k=…`) — shown inside the room in the
  Invite dialog and in the host's address bar. It stops working once the room
  has stood empty for a couple of minutes (`LINKS_ROTATE_AFTER`), and a host
  can refresh it any time. Friends who have been in before are remembered.
- **The cookie** — signed, HttpOnly, only ever upgraded; "Forget this device"
  clears it.

## Quick start (local, one machine)

Prerequisites on macOS: `brew install go node mpv opus ffmpeg livekit livekit-cli`.
(Linux: Go 1.26+, Node 22+, `libmpv-dev libopus-dev ffmpeg pkg-config`, plus a
LiveKit server binary.)

```sh
make web server projector          # builds the SPA into the server binary + the projector

# terminal 1: LiveKit
make dev-livekit
# terminal 2: app server (serves the web app on :8080; MEDIA_ROOT is the library)
cd server && ROOM_PASSWORD=dev LIVEKIT_API_KEY=devkey LIVEKIT_API_SECRET=secret \
  MEDIA_ROOT=../deploy/media ./bin/server
```

Open http://localhost:8080, type the password (`dev`), and you are the host.
The **Invite** button shows the link for friends. Push a title into the
library with `make push FILE=~/Videos/episode.mkv HOST=user@server` (or, for
a local library, run `vspush --out ../deploy/media/<id> file.mkv`), then pick
it in the **Library** tab.

**Projector mode** (Library tab → *Projector mode*) is for the desktop
projector, which streams mpv live from the machine holding the files and
joins with the same password:

```sh
VS_PASSWORD=dev ./projector/bin/projector --room http://localhost:8080 --media-root ~/Videos ~/Videos/episode.mkv
```

Host keys on the stage: `Space` pause, `←/→` ±5 s, `↑/↓` ±60 s; in projector
mode also `j` cycle subtitles, `#` cycle audio, `z`/`x` sub delay. `f` is
fullscreen.

## Production

Run `deploy/oracle-setup.sh` on an Ubuntu 24.04 arm64 VM (see deploy/README.md); it
asks for the room password. Type it at `https://<your domain>` and hand out the
invite link from inside.

## Development

- `make test` runs Go vet/tests, the TypeScript check and the web unit tests.
- `cd web && npm run dev` serves the SPA on :5173 with `/api` proxied to :8080.
- `projector/cmd/clockprobe` is the experiment that established how mpv is clocked.
