# Shared protocol

Everything that crosses a process boundary is defined here, hand-kept in two
languages: `messages.ts` (web) and `messages.go` (server + projector). Keep them
in sync; there is no code generation.

## Roles

A LiveKit JWT carries JSON `metadata`: `{"role":"host"|"viewer"|"projector","color":"#rrggbb"}`.
Roles are assigned by the app server when it mints the token; clients never
choose their role. The projector accepts `mpv.cmd` only from `role=host`.

## The room

There is exactly one room; `proto.RoomID` / `ROOM_ID` (`"main"`) names it in
LiveKit, in the chat history and in the director. Nobody creates or names
rooms. The site is a door, and there are three ways through it:

| Way in | Who | What you get |
|---|---|---|
| `https://<host>/?k=<viewerKey>` | friends | viewer |
| the room password (`ROOM_PASSWORD` on the server) | whoever runs the party | host |
| the `vs_auth` cookie | anyone who got in before, on that device | whatever they had |

The password is the only thing that has to be *known*. Joining sets a
long-lived, HttpOnly, signed cookie carrying the role, so a host enters the
password once per device and a friend who has been in before keeps getting
in after the link rotates. The cookie only ever upgrades (viewer → host),
and `POST /api/room/logout` clears it.

The viewer key is rotated automatically once the room has stood empty for
`LINKS_ROTATE_AFTER` (default 2 minutes; `0` disables it), and on demand by
a host. A rotated key gets `access: "none"` from `GET /api/room` (unless the
cookie says otherwise), and the door asks for the password instead.

The projector joins with the password (`projector --room https://<host>
--password …`, or `VS_PASSWORD` in the environment) and asks for the
projector identity with `projector: true`.

## HTTP API (app server)

| Method | Path | Body → Response |
|---|---|---|
| GET | `/api/room?key=<viewerKey>` | → `RoomInfo` `{access, occupants}` (unauthenticated; the door; reads the cookie too) |
| POST | `/api/room/token` | `TokenRequest` `{name, key?, password?, projector?}` → `TokenResponse` `{token, url, identity, role, color, session, settings, links}`; sets the `vs_auth` cookie |
| POST | `/api/room/logout` | clears the cookie → 204 |
| GET | `/api/room/links` | → `Links` (Bearer `session`) |
| POST | `/api/room/links/rotate` | → `Links` (Bearer `session`, host only; posts a system chat line) |
| GET | `/api/room/chat?before=<unixms>&limit=100` | → `{messages: ChatMessage[]}` (Bearer `session`) |
| POST | `/api/room/chat` | `{text}` → `ChatMessage` (Bearer `session`; stored and broadcast on topic `chat`) |
| PATCH | `/api/room/settings` | partial `RoomSettings` → `RoomSettings` (Bearer `session`, host only; broadcast on topic `settings`) |
| GET | `/api/room/playback` | → `PlaybackState` (Bearer `session`) |
| POST | `/api/room/playback` | `{action, mediaId?, posMs?, relative?}` → `PlaybackState` (Bearer `session`; host, or anyone for pause-ish actions when `anyoneCanPause`) |
| GET | `/api/media` | → `{items: (MediaMeta & {url})[], freeBytes}` (Bearer `session`) |
| DELETE | `/api/media/{id}` | (Bearer `session`, host only) |
| GET | `/media/{id}/{file}?e=&s=` | the bytes, behind a signed URL (Range supported) |
| GET | `/api/time` | → `{nowMs}` |
| GET | `/healthz` | → `ok` |

`session` is an HMAC-signed opaque string binding `{identity, name, color, role, exp}`,
valid for six hours; the cookie is a separate signature over `{role}` and cannot
be used as a session. `url` is the LiveKit websocket URL (`wss://<host>` when
proxied by Caddy). Wrong passwords are rate-limited per client address.

## LiveKit data topics

| Topic | Direction | Reliable | Payload |
|---|---|---|---|
| `chat` | server → all | yes | `ChatMessage` |
| `typing` | client → all | no | `TypingMessage` |
| `react` | client → all | no | `ReactMessage` |
| `presence` | client → all | yes | `PresenceMessage` (sent on join and on change) |
| `settings` | server → all | yes | `RoomSettings` |
| `mpv.cmd` | host → projector | yes | `MpvCommand` |
| `mpv.reply` | projector → sender | yes | `MpvReply` |
| `mpv.state` | projector → all | no | `MpvState` (4 Hz while playing, plus on change) |
| `mpv.event` | projector → all | yes | `MpvEvent` |

## mpv commands

`MpvCommand.cmd` is an mpv command array exactly as in mpv's JSON IPC
(`["seek", 10, "relative"]`, `["set_property", "sid", 2]`, `["cycle", "pause"]`).
Commands whose first element starts with `vs/` are virtual and handled by the
projector itself:

- `["vs/load", "<path or URL>", "replace"|"append"]` — path must be under a media root
- `["vs/fs.list", "<dir>"]` — reply `FsList`; empty dir lists the roots
- `["vs/quality", "<preset>"]` — `1080p-high` | `1080p` | `720p` | `540p`
- `["vs/state"]` — reply with a full `MpvState`
