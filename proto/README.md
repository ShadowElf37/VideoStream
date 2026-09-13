# Shared protocol

Everything that crosses a process boundary is defined here, hand-kept in two
languages: `messages.ts` (web) and `messages.go` (server + projector). Keep them
in sync; there is no code generation.

## Roles

A LiveKit JWT carries JSON `metadata`: `{"role":"host"|"viewer"|"projector","color":"#rrggbb"}`.
Roles are assigned by the app server when it mints the token; clients never
choose their role. The projector accepts `mpv.cmd` only from `role=host`.

## Links

- Invite:    `https://<host>/r/<roomId>?k=<inviteKey>`
- Host:      `https://<host>/r/<roomId>?h=<hostSecret>`
- Projector: `https://<host>/r/<roomId>?p=<projectorKey>` (passed to `projector --room`)

## HTTP API (app server)

| Method | Path | Body → Response |
|---|---|---|
| POST | `/api/rooms` | `{name?, password?}` → `{id, name, inviteLink, hostLink, projectorLink}` |
| GET | `/api/rooms/{id}` | → `{id, name, hasPassword, settings}` |
| POST | `/api/rooms/{id}/token` | `{name, inviteKey?, hostSecret?, projectorKey?, password?}` → `{token, url, identity, role, color, session, settings}` |
| GET | `/api/rooms/{id}/chat?before=<unixms>&limit=100` | → `{messages: ChatMessage[]}` (Bearer `session`) |
| POST | `/api/rooms/{id}/chat` | `{text}` → `ChatMessage` (Bearer `session`; server stores and broadcasts on topic `chat`) |
| PATCH | `/api/rooms/{id}/settings` | partial `RoomSettings` → `RoomSettings` (Bearer `session`, host only; server broadcasts on topic `settings`) |
| GET | `/healthz` | → `ok` |

`session` is an HMAC-signed opaque string binding `{roomId, identity, name, color, role, exp}`.
`url` is the LiveKit websocket URL (`wss://<host>` when proxied by Caddy).

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
