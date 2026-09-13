# Self-hosted watch party: mpv-powered projector + web app

## Context

You want to watch shows from a local file with 3–8 friends, with voice chat, on Linux/macOS/Windows, without Discord, and with mute / deafen / movie-audio controls that are independent of each other. Element and Signal choke on throughput and have poor Linux screen-share-with-audio; FaceTime is janky.

Two premise corrections drive the design:

1. **Screen-sharing a local file is the wrong tool.** It re-encodes an already-encoded video through the OS screen capture path, and "capture system audio" is exactly the part that is broken or flag-gated on Linux (and only recently works on macOS in Chrome 141+). The right tool is to feed the file *directly* into the room as a video+audio track. That makes the Linux audio problem disappear entirely, gives better quality than any screenshare, and works identically on every OS.
2. **mpv should be the playback engine, not ffmpeg.** You already have the best player; its control layer (play/pause/seek/track selection/sub delay/chapters/playlists/URLs via yt-dlp) and libass subtitle rendering are exactly what a watch party needs. The projector embeds **libmpv** (mpv's official embedding library, shipped with every mpv install; not a source hook) and acts as mpv's "display" and "speaker", pulling rendered frames and PCM out and pushing them into the room. Your `mpv.conf`, fonts, sub styles and Lua scripts all still apply.

Outcome: friends open a link in any browser and get a polished theater UI with voice, chat sidebar, reactions and per-source audio controls. The host runs one extra binary (`projector`) that drives mpv and streams it. A 4-core ARM VM on Oracle's free tier relays everything.

## Evaluation of options (verdicts)

| Option | Verdict |
|---|---|
| Pure P2P mesh | Voice-only viable. Video needs host upload = bitrate × viewers (8 Mbps × 6 = 48 Mbps). Rejected for video; an SFU relay on the free VM fixes it. |
| Self-host Jitsi / Galène / LiveKit Meet as-is | Would work for voice + screenshare but none has separated deafen/movie controls, file projection, or the UI you want. Rejected, but we **reuse LiveKit** (Apache-2.0 SFU, Go, arm64 images, embedded TURN) as the media core so we never write an SFU. |
| Tauri desktop client | WebKitGTK on Linux has no WebRTC. Rejected. |
| Electron desktop client | Works, but heavy, and a browser tab does everything friends need. Rejected; host gets a small native `projector` binary instead. |
| Browser-only host (`<video>` + `captureStream`) | No AC3/DTS/EAC3, no embedded subs, spotty HEVC. Rejected. |
| ffmpeg-only projector | Works (`ffmpeg -f whip` exists in ffmpeg 8), but pause/seek = process restart, and subtitle/track handling is inferior to mpv. Kept as the Phase-1 smoke test only. |
| **mpv (libmpv) projector + LiveKit + web app** | **Chosen.** |
| Screen share (for non-file content) | Kept as a later mode: browser `getDisplayMedia` on Windows/macOS, OBS → WHIP → LiveKit Ingress on Linux. mpv playing a URL (yt-dlp) covers most "watch a web video" cases without screen share at all. |

Facts verified during research: ffmpeg 8.1.2 on this Mac has the `whip` muxer, `h264_videotoolbox`, `libopus`, `libsvtav1`; LiveKit Go SDK has `NewLocalSampleTrack` + `WriteSample` (caller-paced) and data packets with topics; LiveKit Ingress WHIP defaults to passthrough and ships arm64 images; mpv's `ao_pcm` is untimed (writes as fast as the consumer accepts, blocking `fwrite`); libmpv's software render API outputs `bgr0` frames with subtitles/OSD composited and `gen2brain/go-mpv` wraps it (`NewRenderContextSW`, `RenderSW`, `SetUpdateCallback`, `ObserveProperty`, `Command`).

## Architecture

```
Host machine                                  Oracle A1 VM (docker compose)            Friends
┌──────────────────────────────┐              ┌──────────────────────────────┐        ┌──────────┐
│ projector (Go)               │  WebRTC      │ Caddy :443 (TLS, reverse px) │ WebRTC │ browser  │
│  ├ libmpv (vo=libmpv, SW/GL) │ ───────────► │ livekit-server (SFU + TURN)  │ ◄────► │ web app  │
│  │   frames w/ subs, PCM     │  publishes   │ app (Go): tokens, rooms,     │        │ (React)  │
│  ├ encoder: ffmpeg child     │  movie-video │   chat history, invites,     │        └──────────┘
│  │   (hw H.264) + libopus    │  movie-audio │   serves web/ build  (SQLite)│        ┌──────────┐
│  └ LiveKit Go SDK participant│  data: state │ [optional] redis + ingress   │ ◄────► │ host's   │
│      "Projector"             │ ◄─────────── │   (OBS/WHIP screen share)    │        │ browser  │
│  + --input-ipc-server socket │  data: mpv   └──────────────────────────────┘        │ (host UI)│
└──────────────────────────────┘  commands                                             └──────────┘
```

- **Everyone, including the host, watches in the browser.** The projector has no window; the host's browser subscribes to the movie track like everyone else, so what the host sees/hears is exactly what friends see/hear, in sync with voice. Host-only controls in the web UI send mpv commands to the projector over a LiveKit data channel.
- One LiveKit room per party. Tracks: each person's `microphone`; the projector's `movie-video` + `movie-audio` (published under a dedicated name so the client can treat them separately from voice).

## Components

### 1. `deploy/` — server on Oracle Always Free (Ampere A1, 4 OCPU / 24 GB, 10 TB egress)

- `docker-compose.yml`: `caddy`, `livekit` (`livekit/livekit-server` arm64, `network_mode: host` so UDP isn't NATed twice), `app` (our Go server), and an `ingress` profile (`redis` + `livekit/ingress`) that is off by default. Redis is not needed without ingress.
- `Caddyfile`: `watch.example.tld` → `app:8080`; `/rtc*`, `/twirp*` → `localhost:7880` (same origin for app and LiveKit avoids CORS surprises). Caddy handles Let's Encrypt; its cert storage is mounted read-only into LiveKit for TURN/TLS, with a renewal hook that restarts LiveKit.
- `livekit.yaml`: `rtc.udp_port: 7882` (single UDP mux port), `rtc.tcp_port: 7881`, `rtc.use_external_ip: true` + `rtc.node_ip: <reserved public IP>` (OCI public IPs are 1:1 NAT; the SFU must advertise the public one), `rtc.congestion_control.allow_pause: false` (a single-layer movie must never be "paused" by the SFU; simulcast later gives it a step-down instead), TURN enabled (`udp_port: 3478`, `tls_port: 5349`, `relay_range 30000–30100`), API key/secret, `room.auto_create: false`, `room.empty_timeout: 300`.
- `oracle-setup.sh` (idempotent, run on Ubuntu 24.04 arm64): install docker, fix the on-instance firewall (Oracle's image ships `/etc/iptables/rules.v4` with a `REJECT` rule in `INPUT` after SSH; our `ACCEPT` rules must be *inserted before it*, then `netfilter-persistent save`), add the TURN hairpin DNAT (`-t nat -A OUTPUT -d <public_ip> -j DNAT --to <private_ip>`) so the SFU can reach its own relay, create `.env` from prompts, `docker compose up -d`.
- Oracle account hygiene: attach a **reserved** public IP (ephemeral ones die with the instance) and upgrade the tenancy to pay-as-you-go (still $0 inside the free limits) so the Always-Free idle-reclamation rule (7 days under 20 % CPU/net) can't delete an idle LiveKit box.
- Ports to open in the VCN security list **and** iptables: TCP 80, 443, 7881, 5349; UDP 7882, 3478. Ingress profile adds TCP 1935 and UDP 7885–7895. Restrict 22 to your IP.
- DNS: a real domain is strongly preferred (needed for clean TLS and TURN/TLS). Until you buy one, `sslip.io` (`<ip>.sslip.io`) works with Caddy for free.
- Bandwidth: 8 Mbps × 6 viewers ≈ 48 Mbps egress; ~10 h/week ≈ 0.9 TB/month, well under 10 TB.

### 2. `server/` — app server (Go, single binary, SQLite via `modernc.org/sqlite`)

Small on purpose; LiveKit does the heavy lifting.

- `POST /api/rooms` → creates room row, returns `hostLink` (contains host secret) and `inviteLink`.
- `POST /api/rooms/{id}/token` with `{name, invite|hostSecret|projectorKey}` → mints a LiveKit JWT. Grants by role: `viewer` (publish mic + data), `host` (viewer + `roomAdmin`, metadata `role=host`), `projector` (publish video/audio/data, identity `projector`, `hidden: false` so it shows as "Projector"). Tokens are short-lived; the client refreshes via the same endpoint.
- Chat: `POST /api/rooms/{id}/chat` stores the message and rebroadcasts it to the room with LiveKit's server-side `RoomServiceClient.SendData` (topic `chat`). `GET /api/rooms/{id}/chat?before=` returns history so late joiners see it. Rate-limited.
- Room settings: password (optional), `anyoneCanPause`, `deafenImpliesMute` default, movie quality cap.
- Serves the built `web/` bundle. Health endpoint. Structured logs.

### 3. `web/` — the watch party app (React 19 + TypeScript + Vite)

Stack: `livekit-client` + `@livekit/components-react` hooks (we build our own UI, not their prebuilt components), `zustand` for local state, Tailwind v4 + Radix primitives (menus, sliders, dialogs, tooltips), `lucide-react` icons, `vite-plugin-pwa` (installable, offline shell only).

**Join flow.** Link → "Who are you?" card (name, auto-assigned avatar color, remembered in localStorage) → device check (mic picker with live level meter, speaker picker with "play test tone", noise-suppression toggle) → Join. Room password if set. Autoplay is unlocked by this click, which matters for Safari.

**Layout (desktop).**
- *Stage*: 16:9 video letterboxed on near-black, no chrome. Overlays: floating emoji reactions (bottom-right), "speaking" avatar chips (bottom-left), toast for "Now playing: <title> · chapter 3" on change, host transport bar on hover (see below), buffering spinner, "stream paused by host" state, connection-quality glyph.
- *Right sidebar* (collapsible `C`, resizable, remembered): tabs **Chat** / **People** / **Queue**.
  - **Chat**: messages grouped by author with name color + relative timestamps, emoji picker, `@name` mentions with highlight, link auto-detection, image/GIF URLs render inline, typing indicator, unread badge on the collapsed tab, "jump to newest" pill. System lines interleave from projector events: *"Host paused at 12:34"*, *"Seeked to 41:02"*, *"Switched subtitles to English (Full)"*, *"Alice joined"*. Sound on new message when the tab is unfocused (toggle).
  - **People**: avatar, name, mic state, speaking ring, per-person volume slider (0–200 %), "deafened" glyph, host badge, connection quality; host actions: mute-for-me, make host, kick.
  - **Queue** (host and co-hosts): file browser over the projector's allowed media roots (data channel `fs.list`), search filter, click to play or append; mpv playlist with drag reorder; "Open URL" (yt-dlp).
- *Bottom dock*: `[Mic M] [Deafen D] [Movie audio S + slider] [Reactions R] [Push-to-talk toggle] [Screen share (host)] [Fullscreen F] [Sidebar C] [Settings] [Leave]`. The three audio toggles are visually distinct (icon + color + label) so nobody confuses "I muted myself" with "I can't hear you" with "the movie is muted".
- *Host transport bar* (only for `host` role; appears on hover/keyboard): mpv-style OSC — seek bar with chapter ticks and hover time, play/pause, ±10 s / ±60 s, speed, audio-track and subtitle-track dropdowns (from mpv `track-list`), sub delay ±, audio delay ±, mpv volume, "sub visibility", chapter prev/next, "load file". Keyboard mirrors mpv where sensible (`Space`, `←/→`, `[`/`]`, `j`/`#`, `z`/`x` for sub delay) when the stage is focused. Viewers get a read-only progress bar with the same chapter ticks and a "request pause" button (host sees a toast; if `anyoneCanPause` is on, it just pauses).
- *Fullscreen/theater*: video fills the screen; sidebar becomes an overlay drawer; dock auto-hides. Picture-in-picture button for the video.
- *Settings*: devices, noise suppression (browser NS; optional RNNoise AudioWorklet later), echo cancellation, AGC, `deafen implies mute` (Discord semantics; off by default = fully independent), auto-duck movie when someone speaks (0–12 dB), **smoothness** slider (jitter buffer target 0.2–2.5 s), preferred quality (auto/1080p/720p), stats overlay, theme (dark default, light available), notification sounds.
- Responsive: usable at tablet/phone widths (stacked layout, sidebar as bottom sheet), but phones are not a design target per your answer.

**Visual direction.** Cinematic dark: `#0b0b0d` ground, glass panels (`backdrop-filter`), one accent (e.g. warm amber), Inter/system font, 8 px grid, restrained motion (speaking rings, reaction floats, sidebar slide). Light theme via CSS tokens for people who insist.

**Audio model (the separated controls).**
- `micMuted` → `localParticipant.setMicrophoneEnabled(false)` (track stays published, muted; instant unmute). Push-to-talk = hold key to enable.
- `voiceDeafened` → every remote `microphone` track's `setVolume(0)` (and we stop attaching new ones while deafened). Movie audio unaffected. If `deafenImpliesMute` is on, also sets `micMuted` and restores the prior state on undeafen.
- `movieMuted` / `movieVolume` → the projector's `movie-audio` track only (`setVolume`, 0–100 %, remembered).
- Per-person volume → that participant's `microphone` track `setVolume(x)`, 0–2.0.
- Ducking: on `RoomEvent.ActiveSpeakersChanged` with any non-projector speaker, ramp movie gain down to the configured level over ~150 ms, back up ~600 ms after silence. (An `AnalyserNode` per voice track can replace the server's speaker detection if its 200–300 ms lag is noticeable.)
- Implementation: LiveKit's `webAudioMix: true` room option, so every remote audio track already sits behind a `GainNode` (`setVolume` ramps smoothly, allows >1.0 boost). Gains are *derived* from one reducer state (`micEnabled = !(micMuted || (deafened && deafenImpliesMute))`, `voiceGain(id) = deafened ? 0 : volumes[id]`, `movieGain = movieMuted ? 0 : movieVolume × duck`) and re-applied on `TrackSubscribed` / `Reconnected`. Elements stay attached at gain 0 rather than detached (detaching breaks the Web Audio graph on some Chrome versions). After 10 s deafened we also `setSubscribed(false)` on voice tracks to save bandwidth. `adaptiveStream: false` so element size never downgrades the movie. Output device via `switchActiveDevice('audiooutput')` (hidden on Safari).
- Smoothness: on subscribing to the two movie tracks, set `receiver.jitterBufferTarget` (Chrome/Firefox; also the older `playoutDelayHint`) to the slider value, same on audio and video so they don't fight, default 1.5 s; re-apply after reconnects. Safari has neither, so Safari viewers run on default buffers; the join screen recommends Chrome or Firefox on desktop. The stage's player is one component behind a small interface so an MSE/LL-HLS "big buffer" mode can be added later without touching the rest of the app.
- Mic capture: `echoCancellation`, `noiseSuppression`, `autoGainControl` on; Opus `dtx: true`, `red: true`, `audioPreset: speech`, `stopMicTrackOnMute: false` for instant unmute. Movie audio playback comes from the same page, so Chrome's AEC has it as a reference and removes it from the mic; headphones still recommended, PTT for the speaker-happy.

**Data-channel protocol (LiveKit `publishData`, JSON, topics).**
- `chat` (server-originated via SendData), `typing`, `react` (`{emoji}`), `presence` (client prefs like deafened, for the People tab).
- `mpv.cmd` (host → projector, reliable): `{id, cmd: ["seek", 10, "relative"]}` — literally mpv command arrays, plus a few virtual commands (`load {path|url}`, `fs.list {dir}`, `quality {preset}`). The projector authorizes by checking the sender's token metadata `role=host`.
- `mpv.state` (projector → all, lossy, 4 Hz + on change): `{pause, timePos, duration, speed, chapter, chapters[], tracks[] (aid/sid/lang/title/selected), mediaTitle, path, subDelay, audioDelay, encoder, bitrate, fps, dropped}`. Drives the progress bar, transport dropdowns and the chat system lines.
- `mpv.event` (projector → all, reliable): `file-loaded`, `seek`, `pause`, `end-file`, `error`, with human text for the chat.

### 4. `projector/` — host-side mpv streamer (Go, cgo, single binary per OS)

**What it is.** A LiveKit participant named `Projector` that embeds libmpv, renders mpv's output into memory, encodes it, publishes it, and executes mpv commands sent by the host UI. No window. Runs on macOS, Linux, Windows; also runs headless on the server if a file lives there.

**Process model.**
```
libmpv core ──render callback──► frame ring (bgr0, output res) ─┐
   │  --ao=pcm → FIFO/socketpair ──► audio reader (20 ms chunks) ─┤ media clock (audio-sample counter)
   │                                                              ▼
   │                           ffmpeg child (rawvideo in → hw H.264 Annex-B + AUD out)   libopus (cgo) 20 ms frames
   │                                        │                                                 │
   │                                        ▼                                                 ▼
   │                           LocalSampleTrack "movie-video" WriteSample(dur)   LocalSampleTrack "movie-audio"
   └── ObserveProperty(time-pos, pause, track-list, chapter, …) ──► mpv.state / mpv.event data packets
   └── OnDataPacket(topic mpv.cmd) ──► mpv.Command(...)
   └── --input-ipc-server=~/.cache/projector/mpv.sock (so your existing mpv tooling/scripts keep working)
```

- **mpv setup**: `gen2brain/go-mpv` (cgo against system libmpv; `brew install mpv` / `libmpv-dev`). Options: `vo=libmpv`, `ao=pcm`, `ao-pcm-file=<fifo>`, `ao-pcm-waveheader=no`, `audio-format=s16`, `audio-samplerate=48000`, `audio-channels=stereo`, `audio-normalize-downmix=yes`, `hwdec=auto-copy`, `keep-open=yes`, `osd-level=0` (OSD off; subs on), `config=yes` + `config-dir` pointing at the user's mpv config so their `mpv.conf`, fonts, `sub-*` styling and `scripts/` load, `ytdl=yes`, `input-ipc-server`. Renderer: `NewRenderContextSW`; on `RenderUpdateFrame` we `RenderSW` into a `bgr0` buffer at the output resolution (default 1920×1080 letterboxed, mpv does the scaling and composites libass subs).
- **Clocking (the key trick).** mpv's `ao_pcm` is untimed: it writes PCM as fast as the consumer accepts and blocks when the pipe is full. The projector reads the pipe at exactly real time (960 samples per 20 ms tick from a monotonic timer) → mpv is slaved to our clock through back-pressure, and video frames are released by mpv in step with the audio it has written. Timestamps for both tracks come from the same audio-sample counter, so A/V sync is exact by construction and cannot drift over a 2-hour film. Video lead caused by pipe buffering (≈16–64 KB ≈ 85–340 ms) is measured with `FIONREAD` at render time and compensated by holding frames in a small queue; on Linux the pipe is shrunk with `F_SETPIPE_SZ`, and a `socketpair` with a small `SO_SNDBUF` is the portable alternative.
- **Pause / seek / EOF**: the audio reader uses non-blocking reads with the tick as deadline; no data → emit Opus silence (DTX) and keep the clock running, and resend the last video frame at 1 fps (viewers keep the frozen frame; late joiners still get a picture). On `seeking` we drain the pipe so pre-seek audio is discarded. The published tracks never go away, so viewers see a clean cut instead of a track teardown.
- **Video output cadence**: we emit at the source fps (from `container-fps`/`estimated-vf-fps`, rounded to a stable rational; 23.976 stays 24000/1001); at each output tick we send the newest rendered frame (dup if mpv had none).
- **Timestamps: absolute, never accumulated.** Do **not** use the SDK's `WriteSample` (it derives RTP time by summing per-frame durations; at 23.976 fps that truncates 0.75 tick/frame ≈ 0.7 s of A/V drift per hour). Instead packetize ourselves (pion `codecs.H264Payloader` / Opus, MTU 1200, marker on the last packet of an access unit, strip AUD and filler NALs) and call `LocalTrack.WriteRTP` with `ts = round(mediaClock × clockRate)` computed from the audio-sample counter: audio `ts = samples`, video `ts = samples × 15/8` at the frame's tick. Sequence numbers are our own continuous counters. Timestamps only ever move forward (pause = gap, seek = gap); pion/LiveKit rewrite SSRC/PT.
- **Encoder**: an ffmpeg child per session, fed raw frames on stdin, H.264 Annex-B on stdout. Because we feed CFR frames and B-frames are off, output access unit *k* is input frame *k*, so its timestamp is looked up from a queue of fed-frame ticks; no timestamps need to survive ffmpeg. Base args: `-f rawvideo -pix_fmt bgr0 -s WxH -r FPS -i pipe:0 -pix_fmt nv12 -c:v <enc> -profile:v main -bf 0 -g <2 s> -force_key_frames expr:gte(t,n_forced*2) -b:v 8M -maxrate 8M -bufsize 8M -bsf:v dump_extra=freq=keyframe -f h264 pipe:1` (SPS/PPS in-band before every IDR). Encoder auto-picked at startup by probing `ffmpeg -encoders` plus a 1-second `testsrc2` self-encode: `h264_videotoolbox -realtime 1 -prio_speed 1` (macOS), `h264_nvenc -preset p4 -tune hq -rc vbr -forced-idr 1 -no-scenecut 1` / `h264_vaapi -rc_mode VBR -idr_interval 0` / `h264_qsv -look_ahead 0` (Linux), `h264_nvenc` / `h264_qsv` / `h264_amf -rc vbr_peak` (Windows), falling back to `libx264 -preset veryfast -x264-params keyint=48:min-keyint=48:scenecut=0:bframes=0`. Presets: `1080p high` (8 Mbps), `1080p normal` (5), `720p` (3), `540p` (1.5). Encoder lateness (AUs arriving > 500 ms after their tick for a sustained period) is surfaced to the host UI and can auto-drop a preset. Opus in-process via `hraban/opus` (libopus cgo), 128–160 kbps stereo, 20 ms frames, `-application audio`.
- **Publish parameters**: video `RTPCodecCapability{video/H264, 90000, "level-asymmetry-allowed=1;packetization-mode=1;profile-level-id=42e01f"}` (Main-profile bitstream advertised as constrained baseline, which every browser accepts), `TrackPublicationOptions{Name: "movie.video", Source: SCREEN_SHARE, VideoWidth, VideoHeight}`; audio `RTPCodecCapability{audio/opus, 48000, 2, "minptime=10;useinbandfec=1;stereo=1;sprop-stereo=1"}`, `{Name: "movie.audio", Source: SCREEN_SHARE_AUDIO, Stereo: true, DisableDTX: true, DisableRED: true}` (without `stereo=1` libwebrtc decodes mono). Projector token: `canSubscribe: false`, metadata `role=projector`.
- **Keyframes / late joiners**: ffmpeg can't be asked for an IDR on demand, so the GOP is 2 s (≤2 s to first picture for a late joiner; PLI/FIR/NACK counts from `WithRTCPHandler` are shown in the host stats, never acted on by restarting anything). Because the encoder keeps running while paused (we keep feeding the frozen frame at 1 fps), late joiners during a pause still get an IDR within 2 s. Phase 3 can move the encoder in-process (VideoToolbox/x264 via cgo) to honor PLI immediately. Verify early that the Go SDK's publisher connection answers NACKs under induced loss (`tc netem` / `dnctl`); if not, add pion's NACK responder interceptor.
- **Simulcast**: publish a second 540p/1.5 Mbps layer as a real simulcast layer (`NewLocalTrack(..., WithSimulcast(...))` + `PublishSimulcastTrack`) so LiveKit steps weak viewers down instead of pausing their video; a second ffmpeg child is fed the same frame ring, downscaled, with the same forced-keyframe expression so IDRs align.

**Hardware encoding (yes, default on).** For a real-time 1080p stream the hardware encoder is the right default everywhere: VideoToolbox on Apple Silicon, NVENC, VA-API/QSV, AMF. It costs near-zero CPU, has 1–2 frames of latency, and at 5–8 Mbps for 1080p its quality is close to x264 `veryfast`; the gap only matters at low bitrates, where x264 `medium` wins but burns 2–4 cores. Rules that apply to every encoder: B-frames off (browser H.264 RTP assumes decode order = display order), fixed 2 s IDR cadence, VBR with `maxrate` = target and a 1 s VBV buffer so IDR bursts don't overrun the SFU, Main profile. Hardware *decoding* matters as much: mpv runs with `hwdec=auto-copy`, so 4K HEVC/10-bit sources decode on the GPU and only the 1080p output frame crosses to the CPU. What stays on the CPU today: mpv's software renderer (YUV→RGB + libass compositing) and ffmpeg's RGB→NV12 conversion, a few ms per 1080p frame; the Phase 3 OpenGL render path removes most of it. HEVC/AV1 hardware encode is not worth it yet: WebRTC HEVC decode is Safari-only, and AV1 hardware encoders exist only on NVIDIA 40-series and Intel Arc (Apple Silicon has AV1 decode, not encode). `libsvtav1` at a fast preset is a later "all viewers on Chrome/Firefox" option for better quality per bit.
- **CLI**: `projector --room <invite/host link> [--media-root DIR]... [--preset 1080p] [--fps auto] [file|url]`. Also `projector open <file>` to load into a running instance (via its IPC socket). Tray/menu-bar icon later.
- **Reconnect**: LiveKit SDK reconnect; on full disconnect the projector re-joins and republishes without touching mpv (playback keeps going, e.g. auto-pause is a room setting).

**Why not Lua for the media path.** Lua scripts run inside mpv and see properties/commands, but not frames or PCM; the only stock-mpv path to frames is `screenshot-raw` from a script at ~24 Hz, which stalls the render pipeline and gives jittery timing. libmpv's render API is the mechanism mpv provides for exactly this. For *control*, Lua isn't needed either: JSON IPC and the libmpv API cover it. Where Lua *is* handy (later): a `projector.lua` shipped for people who prefer running a *visible* stock mpv window (screen-share mode) that shows chat/speakers as an mpv OSD overlay and mirrors their key presses to the party. Escape hatch if embedding ever turns out painful: stock `mpv --ao=pcm --ao-pcm-file=fifo` for audio + the `screenshot-raw` Lua script for frames, same downstream pipeline.

## Repository layout & stack

```
videostream/
  deploy/      docker-compose.yml, Caddyfile, livekit.yaml, oracle-setup.sh, README (ports, DNS)
  server/      Go: cmd/server, internal/{rooms,tokens,chat,api}, embeds web/dist
  projector/   Go (cgo): cmd/projector, internal/{mpv,clock,encode,publish,control,fs}
  web/         React+TS+Vite: src/{app,room,audio,chat,host,ui,state}, PWA manifest
  proto/       data-channel message schemas (TypeScript types + Go structs, hand-kept)
  Makefile     make web | server | projector | deploy | dev (local livekit via `brew install livekit`)
```

Toolchain on this Mac: ffmpeg 8.1.2, node 25, Rust unused; needs `brew install go livekit livekit-cli mpv` (Docker is only needed on the VM). Go cross-compiles the server; the projector is built per OS because of cgo (GitHub Actions matrix: macos-arm64, linux-amd64/arm64, windows-amd64).

## Phases and verification

**Phase 0 — infra (½ day).** Provision Oracle A1 (Ubuntu 24.04 arm64, reserved IP), DNS, run `oracle-setup.sh`, `docker compose up`. Verify: `lk room join --publish-demo` from the Mac; again with UDP blocked on the client (must fall back to 7881/TCP); again forced through TURN (relay + hairpin); ICE candidates show the public IP; a phone on cellular sees the demo video.

**Phase 1 — rooms + voice + chat web app (1 week).** Server endpoints, join flow, stage, dock with the three audio toggles, People tab with per-person volume, Chat with history, reactions, settings, keyboard shortcuts. Movie track for testing comes from `lk room join --publish` of an H.264/Opus sample (or `ffmpeg -f whip` into the ingress profile). Verify: two laptops + one Linux VM; deafen hides voices but not the movie, movie mute hides the movie but not voices, `deafenImpliesMute` restores the prior mic state, per-person volume boosts above 1.0, ducking ramps without clicks; Firefox + Chrome + Safari; reload mid-session restores prefs and re-applies gains.

**Probe result (2026-09-12, `projector/cmd/clockprobe`).** With `ao=pcm` into a FIFO read at real time, mpv delivers a steady 23.98 fps (intervals 37–46 ms, no bursts), its time-pos tracks consumed audio within the pipe fill (~43 ms on macOS), stalling the reader stops mpv (back-pressure works), and reading at 2x does *not* speed mpv up: mpv paces itself on the system monotonic clock, which Go shares. Decision: **drain the FIFO as data arrives; mpv is the master clock**; audio samples define the timeline, video frames are stamped with their (blocking) render time mapped onto it, corrected by the measured audio queue depth. `RenderSW` blocks until the frame's display time by default (the 40 ms "render" figure), which is exactly the timestamp we want. Seek leaves ~1 s gap and a little stale audio to drain. Use `set`/`cycle` commands (the C API has no `set_property`).

**Phase 2 — projector v1 (1–2 weeks).** Day 1–2: throwaway prototype of the PCM-pipe clock (does mpv follow our read rate, or free-run/burst?) to lock the audio approach. Then libmpv embed, SW render, ffmpeg child, own packetizer + `WriteRTP`, `mpv.cmd/state/event`, host transport bar and Queue tab. Verify with a synthetic clip (white flash + 1 kHz beep every second, made with ffmpeg `testsrc2`): flash-to-beep offset ≤ 40 ms at 0/30/60 min and after a pause/resume, measured by filming a viewer; RTP timestamps strictly monotonic across 50 pause/seek cycles with no zombie ffmpeg or goroutine growth; late joiner gets picture ≤ 2 s (also while paused); stereo verified in a subscriber's stats; Firefox decodes the Main-profile stream; NACK recovers 2 % induced uplink loss without PLI storms; Chrome `freezeCount` ≈ 0 over 30 min at a 1.5 s jitter target; MKV with ASS subs and DTS audio plays with styled subs; a YouTube URL plays via yt-dlp; CPU ≤ 1 core for 1080p on Apple Silicon; `docker restart livekit` mid-playback → projector republishes within 10 s. Linux host test with VA-API/NVENC and the libx264 fallback.

**Phase 3 — polish & robustness (1 week).** Simulcast second layer (throttle one viewer to 3 Mbps and confirm only that viewer steps down), stats overlay with PLI/NACK/lateness, encoder auto-downgrade, reconnect paths (projector, server, LiveKit restart), room password, host handoff, PWA install, GL render path if SW render can't keep up with 4K→1080p + heavy subs, in-process encoder for PLI-driven keyframes, tray icon + `projector open`, HDR→SDR tonemapping check (mpv handles it in the render path; verify colors).

**Phase 4 — screen share modes (optional).** Browser `getDisplayMedia` (Windows; macOS Chrome 141+ with system audio), OBS → WHIP → Ingress for full-fidelity Linux sharing with a "stream key" panel in the host UI, `projector.lua` overlay for visible-mpv sessions. Native capture (ScreenCaptureKit / PipeWire portal / WGC) only if OBS proves too clunky.

## Risks and fallbacks

- **PCM-pipe clocking behaves unexpectedly** (mpv free-runs video or bursts frames). Fallback: real-time AO into a loopback device (PipeWire null-sink on Linux created by the projector, BlackHole on macOS, VB-Cable on Windows), captured by the projector; wall-clock timestamps with audio device clock as master. Slightly more setup for the host, same everything else. Decide in the first two days of Phase 2 with a throwaway prototype.
- **SW renderer too slow** for 4K sources or dense ASS. Fallback: `NewRenderContextGL` with an offscreen GLFW context (needs main-thread pinning on macOS), `glReadPixels` via PBO.
- **Host uplink** is the binding constraint: 8 Mbps sustained (≈10 with the simulcast layer). Mitigation: presets down to 1.5 Mbps, a 10 s uplink self-test at startup, auto-downgrade on sustained lateness/PLI.
- **Viewers on weak Wi-Fi** get frozen video on a single 8 Mbps track. Mitigation: `allow_pause: false` now, simulcast layer + smoothness slider + client quality preference next.
- **Safari viewers** can't set a jitter-buffer target and will stutter more on jittery links; friends on macOS should use Chrome/Firefox. An MSE/HLS "big buffer" player mode is the long-term fix if it matters.
- **Echo from viewers on speakers** is a physics/UX problem, not an architecture one: headphones prompt at join, push-to-talk, DTX.
- **Oracle networking gotchas**: forgotten iptables rules, no UDP → everything through TURN/TCP (works, worse). Setup script + a `doctor` command in the web UI showing the ICE path.
- **Cgo build friction on Windows** (libmpv + libopus): ship prebuilt CI binaries; document MSYS2 build.
- **Autoplay/audio policies** on Safari: all audio goes through one `AudioContext` created on the Join click.

## Open decisions (defaults chosen; say if you want otherwise)

- Working name and domain: placeholder `watch.<yourdomain>`; repo stays `videostream`.
- Default `deafen implies mute`: **off** (fully independent controls, your stated preference).
- Default movie preset: 1080p / 8 Mbps H.264 High, GOP 2 s; AV1/VP9 later if all viewers are on Chrome/Firefox.
- Projector control channel: LiveKit data channel with `role=host` check (works for a remote co-host too), plus the local mpv IPC socket for your own tooling.

## Status (2026-09-12, end of first session)

Implemented and verified locally end to end (projector → livekit-server --dev → browser):
- `deploy/`: compose, Caddy, LiveKit/ingress templates, Oracle setup script, dev config. **Not yet provisioned** (no VM, no domain).
- `server/`: full HTTP API, SQLite, LiveKit JWTs, chat history/broadcast, embedded SPA; tests pass.
- `web/`: theater UI, join flow, host transport bar, chat/people/queue sidebar, audio model (mic / deafen / movie independent), settings, PWA; 39 unit tests + headless-browser smoke test.
- `projector/`: libmpv SW render, paced FIFO audio clock (fill flat over 2 min), ffmpeg h264_videotoolbox, in-process Opus, own RTP packetization with absolute timestamps, data-channel control with host-role check, media-root allowlist. Measured: 24.00 fps, 0 timestamp regressions across pause/seek/quality change, A/V delta ≈ 23 ms, IDR every 2.00 s including during pause.

Next: provision the Oracle VM and domain (Phase 0 script is ready); test with a real MKV (ASS subs, DTS/AC3 audio) and a yt-dlp URL; Linux host run (VA-API/NVENC/libx264 fallback); simulcast second layer; 60-minute drift soak with the sync clip; Windows named-pipe audio path; GL render path if 4K sources are too slow for the SW renderer.
