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
| POST | `/api/room/playback/ready` | `PlaybackReady` `{gen, bufferedAheadMs, ready}` → 204 (Bearer `session`; anyone in the room) |
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
| `playback` | server → all | no | `PlaybackState` (on every command, plus 1 Hz while something is loaded) |
| `playback.intent` | server → all | yes | `PlaybackIntent`, sent *before* the state it produces |
| `mpv.cmd` | host → projector | yes | `MpvCommand` |
| `mpv.reply` | projector → sender | yes | `MpvReply` |
| `mpv.state` | projector → all | no | `MpvState` (4 Hz while playing, plus on change) |
| `mpv.event` | projector → all | yes | `MpvEvent` |

## Playback actions

`POST /api/room/playback` takes one `action`:

| Action | Body | Who |
|---|---|---|
| `load` | `{mediaId}` | host |
| `enqueue` | `{mediaId}` | host |
| `play` / `pause` / `toggle` | — | host, or anyone when `anyoneCanPause` |
| `seek` | `{posMs, relative?}` | host |
| `stop` | — | host |
| `reorder` | `{queue}` — the whole new order | host |
| `dequeue` | `{mediaId}` | host |
| `start` | — | host (overrides a `waitForEveryone` hold) |

`reorder` takes the whole queue rather than a move, because a move is only
meaningful against a particular starting order and a client's may be one
broadcast behind. A list that is not the current queue permuted — the same
titles with the same multiplicities — is a stale client, and gets 409 rather
than silently dropping whatever was queued since it last looked. `dequeue`
removes the first occurrence, since the same title queued twice is two things
to watch; 404 if it is not there.

## The intent echo

A viewer sees the picture jump and has no idea who did it. `PlaybackState`
cannot tell them: it says where the film is, not where it was or whose hand was
on the transport, and by the time a client has applied it the jump has already
happened.

So every command also emits a `PlaybackIntent` on `playback.intent` — built
while the command is applied, broadcast **before** the state. The order is the
contract: a client that learned the new position first would have jumped before
being told why. `PlaybackState.lastIntent` carries the most recent one for
whoever joins afterwards.

`fromMs` and `toMs` are the positions before and after (equal for a pause or a
play), which is what lets the client tell a nudge from a jump: the web app
draws a ±30 s seek as a corner chip and anything larger as a centred glyph with
the target time. An empty `actor` is the server itself — the run loop advancing
a playlist — and the wording stays neutral rather than claiming a person, the
same way the live mpv path does, since mpv knows the playback changed but not
who asked.

## waitForEveryone

Off by default. With it on, every discontinuity that *would start the film* —
load, resume, a seek while playing, toggling into play — parks the room at the
target instead: `PlaybackState.paused` and `holding` are both true, and
`waitingFor` names who is not ready yet. Pausing, stopping and seeking while
already paused never hold, because nothing was about to start.

Each browser posts `PlaybackReady` every ~2 s while a title is loaded, and
immediately when its answer flips. `ready` is the player's own start gate:
three seconds buffered ahead, or buffered to the end of the film. `gen` is what
makes the answer meaningful — a report for a position the room has already left
neither blocks the hold nor satisfies it.

The director releases the hold — re-anchoring as it does, so the clients start
from the frame they were parked on — when any of these is true:

- every report seen in the last **6 s** is `ready` at the current `gen`;
- **20 s** have passed (a client on a hopeless link cannot stop the film);
- a host sends the `start` action, or turns the setting off.

A hold that begins when *nobody* has reported recently — a first load, an idle
room — waits **2 s** first, so the first answers can arrive before they are
judged. Otherwise whoever answers first would start the film for everybody. If
nothing is reporting at all (an empty room, projector-only, older clients) the
room starts rather than wedging.

## mpv commands

`MpvCommand.cmd` is an mpv command array exactly as in mpv's JSON IPC
(`["seek", 10, "relative"]`, `["set_property", "sid", 2]`, `["cycle", "pause"]`).
Commands whose first element starts with `vs/` are virtual and handled by the
projector itself:

- `["vs/load", "<path or URL>", "replace"|"append"]` — path must be under a media root
- `["vs/fs.list", "<dir>"]` — reply `FsList`; empty dir lists the roots
- `["vs/quality", "<preset>"]` — `1080p-high` | `1080p` | `720p` | `540p`
- `["vs/state"]` — reply with a full `MpvState`
