# deploy/ — the watch-party server

One `docker compose` stack on a single Oracle Cloud Ampere A1 instance:

| Service | Image | Network | Role |
|---|---|---|---|
| `caddy` | `caddy:2` | bridge, publishes 80/443 | TLS (Let's Encrypt), reverse proxy, HTTP/3 |
| `livekit` | `livekit/livekit-server:v1.13` | **host** | SFU + embedded TURN |
| `app` | built here from `server/Dockerfile` | bridge | the door (password, invite link, cookies), tokens, chat history, the playback director, serves the web build |
| `redis` | `redis:7-alpine` | host | *(profile `ingress`)* job queue for ingress |
| `ingress` | `livekit/ingress:v1.5` | host | *(profile `ingress`)* WHIP/RTMP publishing |

Two things are worth knowing before reading anything else:

- **LiveKit runs on the host network on purpose.** Docker's userland proxy
  rewrites the source address of UDP packets, which breaks ICE and makes the
  embedded TURN server useless. Host networking also means `livekit` is *not*
  reachable as `livekit:7880` from the bridge network — Caddy and the app get
  at it through `host.docker.internal`, mapped to the host gateway by
  `extra_hosts`.
- **Caddy's certificate store is shared, read-only, with LiveKit** so TURN/TLS
  on :5349 can present a real certificate for `turn.$DOMAIN` without a second
  ACME client. Details in `livekit.yaml.tmpl`.
- **The app image is built on the VM**, not pulled from a registry — one less
  account to hold. `server/Dockerfile` builds the SPA (node stage) and the Go
  binary that embeds it, with the repository root as the build context. Rebuild
  it after a `git pull` with `sudo docker compose build app`.

Files:

```
docker-compose.yml   the stack
Caddyfile            TLS + routing (/rtc*, /twirp* -> LiveKit, everything else -> app)
livekit.yaml.tmpl    SFU config template  -> rendered to livekit.yaml
ingress.yaml.tmpl    ingress config template -> rendered to ingress.yaml
oracle-setup.sh      idempotent provisioning for a fresh Ubuntu 24.04 arm64 box
.env.example         every variable, documented
dev/livekit-dev.yaml localhost SFU config for development
```

`.env`, `livekit.yaml`, `ingress.yaml` and `data/` are generated on the server
and are not in git — they hold secrets and state.

---

## 1. Create the instance

Oracle Cloud console → Compute → Instances → Create instance.

| Setting | Value |
|---|---|
| Shape | **VM.Standard.A1.Flex**, 4 OCPU / 24 GB RAM (the whole Always Free ARM allowance) |
| Image | **Canonical Ubuntu 24.04** (aarch64) |
| Boot volume | 50–100 GB (Always Free allows 200 GB total) |
| SSH key | your public key |
| Public IP | assign one now, then **reserve** it (below) |

If the create button reports "Out of host capacity", try another availability
domain or retry later — A1 capacity comes and goes.

**Reserve the public IP.** Instance → Attached VNICs → the VNIC → IPv4 addresses
→ edit the public IP → **Reserved public IP**. An ephemeral IP is released when
the instance stops, and `rtc.node_ip` in `livekit.yaml` would then advertise an
address you no longer own.

**Upgrade the tenancy to pay-as-you-go.** Still $0 while you stay inside the
Always Free limits, but Always-Free-only tenancies are subject to idle
reclamation: an instance under 20 % CPU *and* under 20 % network for 7 days can
be reclaimed. A party server is idle almost all week.

## 2. VCN security list

Oracle's firewall is separate from the instance's. Both must allow the traffic.
Networking → Virtual Cloud Networks → your VCN → Subnets → your subnet →
Security Lists → Default Security List → Add Ingress Rules.

All rules: stateful, source type CIDR, source `0.0.0.0/0`, destination port
range as given.

| Protocol | Port(s) | What |
|---|---|---|
| TCP | 80 | HTTP — ACME challenge, redirect to HTTPS |
| TCP | 443 | HTTPS — web app, LiveKit signalling (`/rtc`), Twirp API |
| UDP | 443 | HTTP/3 (optional; drop it if you prefer) |
| TCP | 7881 | LiveKit ICE/TCP fallback for UDP-blocked viewers |
| UDP | 7882 | LiveKit WebRTC media (single UDP mux port) |
| TCP | 5349 | TURN over TLS |
| UDP | 3478 | TURN over UDP |
| UDP | 30000–30100 | TURN relay allocation range |
| TCP | 22 | SSH — **change the default rule to your IP only** |
| TCP | 1935 | *(ingress profile only)* RTMP |
| UDP | 7885 | *(ingress profile only)* WHIP media |

Port 7880 is deliberately absent: LiveKit's signalling port stays on the host,
reachable only by Caddy.

## 3. DNS

Two A records at your DNS provider, both pointing at the reserved public IP:

```
watch.example.com.      A   203.0.113.10
turn.watch.example.com. A   203.0.113.10
```

The TURN record matters: Caddy obtains a certificate for it (the `respond 404`
site block in the `Caddyfile`) and LiveKit reuses that certificate for TURN/TLS.

No domain yet? `sslip.io` resolves `203.0.113.10.sslip.io` to `203.0.113.10`
for free, and Caddy will happily get a certificate for it (`DOMAIN` becomes
`203.0.113.10.sslip.io`, TURN becomes `turn.203.0.113.10.sslip.io` — which
sslip.io also resolves).

## 4. Run the setup script

```bash
ssh ubuntu@203.0.113.10
git clone https://github.com/shadowelf37/videostream.git
cd videostream/deploy
./oracle-setup.sh
```

It installs Docker + the compose plugin from Docker's own apt repository,
`gettext-base` (for `envsubst`) and `ufw`; configures the instance firewall
(see below); adds the TURN hairpin DNAT; prompts for `DOMAIN` and `PUBLIC_IP` and generates
the secrets into `.env`; renders `livekit.yaml` and `ingress.yaml`; builds the app image; and brings
the stack up. Re-running it is safe.

The first `docker compose build app` takes a few minutes (npm install, Vite
build, Go build); later ones reuse the layer cache. It needs ~2 GB of free disk
and is comfortable on the 4-OCPU/24 GB A1 shape.

Watch the certificates arrive:

```bash
sudo docker compose logs -f caddy       # "certificate obtained successfully" x2
sudo docker compose logs livekit | grep -i turn
```

If Caddy loops on ACME failures, DNS has not propagated or the VCN rule for TCP
80 is missing.

### The door

There is one room. `https://<DOMAIN>` is its door, and three things get
through it:

- **The room password** (`ROOM_PASSWORD` in `.env`): what you type to get in
  as host. Once per device — the server sets a long-lived cookie.
- **The invite link** (`https://<DOMAIN>/?k=…`): shown inside the room in the
  Invite dialog; send it to friends. It stops working once the room has stood
  empty for `LINKS_ROTATE_AFTER` (default two minutes), and a host can refresh
  it at any time. Friends who have been in before are remembered by cookie and
  keep getting in.
- **The cookie**: HttpOnly, signed with `SESSION_SECRET`, kept for 400 days,
  only ever upgraded. "Forget this device" in the Invite dialog clears it.

The desktop projector joins with the password too:
`VS_PASSWORD=… projector --room https://<DOMAIN> file.mkv`.

### Media, and how a film reaches the room

The normal way to watch something is to **push it to the server ahead of
time**. The server then serves the file over HTTPS and tells every browser
where the room is in it; each browser buffers as deeply as it likes and keeps
its own playhead locked to that.

**The server cannot encode**, which is why the push exists. Measured on the A1:
software x264 at 1080p runs 0.35x real time at `veryfast` and 0.97x at
`superfast`, with no GPU to fall back on. So encoding happens once, on the
machine holding the files.

```bash
make push FILE=~/Videos/ep01.mkv AID=2 SID=1 HOST=ubuntu@203.0.113.10
```

`AID`/`SID` are mpv-style track numbers — 1-based, counted per type, so
`AID=2` is the second audio track and `SID=0` burns in no subtitles. Check a
file with `mpv --list-tracks file.mkv`.

That produces a title directory in `deploy/media/`:

```
<id>/movie.mp4        H.264 High + AAC-LC, fragmented, IDR every ~2s
<id>/movie.720p.mp4   the same film at 3 Mbps, unless the source is smaller
<id>/meta.json        duration, geometry, codecs, chapters, renditions
```

One ffmpeg pass does it, at roughly 8x real time with a hardware encoder. It
needs an ffmpeg built with **libass** — on macOS that is `ffmpeg-full`, not the
slim `ffmpeg`; vspush finds one and says so plainly if there is none.

Subtitles are burned in and one audio track survives, because nothing
downstream can render or switch them any more. A dual-audio show has to be
pushed twice to offer both languages.

Defaults: video 5000 kbps, audio 192 kbps AAC (the source is already lossy, and
a second generation at 128k is audibly worse on music), keyframes every 2 s —
which is also the seek granularity. All overridable; see `vspush --help`.

#### Renditions and HLS

A viewer on a weak link used to stall where WebRTC would have gone blurry.
`--renditions` (default `720p`) writes a second, smaller encode next to the
first; it is skipped when the source is not bigger than it, so a 720p show
gains nothing and costs nothing. `--renditions none` turns it off. Budget
about +40% disk per title for the extra one.

The MP4s are **fragmented** — an empty `moov` at the front followed by
self-contained `moof`+`mdat` pairs at every keyframe. The server publishes
them as HLS over the same bytes, with no second copy of the media:

```
GET /media/<id>/index.m3u8     master; one variant per rendition
GET /media/<id>/<name>.m3u8    that rendition, as EXT-X-BYTERANGE ranges
GET /media/<id>/movie.mp4      the bytes those ranges address
```

Both playlists are generated on demand from the files on disk, behind the same
signed-URL scheme as the media (the signature covers the title, so one covers
its playlists and every rendition), and the parsed fragment layout is cached
per file. There is nothing extra to back up and nothing to regenerate after a
restart — deleting the cache is restarting the process.

In the browser: hls.js on Chrome and Firefox, native HLS on Safari, and a
plain `<video src>` for titles pushed before any of this existed, which have
no fragments to address. The Quality menu in the transport bar (Auto / 1080p /
720p) sets the level within the running player, so changing rendition
mid-film does not touch the `<video>` element or throw away the buffer.
Safari adapts on its own and shows no menu.

#### Housekeeping

A 1080p episode is 250–300 MB, plus about 40% again for the 720p rendition, so
a 45 GB boot volume holds roughly 100. The host can delete titles from the
Library tab, and the library view shows free space for exactly this reason.

```bash
ls -lh ~/videostream/deploy/media/
df -h /
sudo docker compose logs -f app
```

#### The desktop projector

Still supported, as the fallback. It is the only way to play a yt-dlp URL, to
switch subtitle or audio tracks live, or to watch something without pushing it
first. It streams live over WebRTC, so a viewer's picture necessarily trails
the host by their receive buffer and the central clock does not apply — pause
and seek are as immediate as the transport allows, which is not very.

### The instance firewall

`ufw` owns the instance's ruleset; `oracle-setup.sh` configures it and
`netfilter-persistent` is disabled so the two cannot fight over `INPUT`.

```bash
sudo ufw status verbose          # what is open
sudo ufw allow 1935/tcp          # e.g. when enabling the ingress profile
sudo ufw delete allow 1935/tcp
```

Two Docker-shaped caveats are worth knowing before you trust that output:

- **Ports published by a container bypass ufw entirely.** Caddy's 80/443 are
  DNAT'd in the `nat` table and traverse `FORWARD`, never `INPUT`, so `ufw deny
  443` would *not* close them — only removing the `ports:` mapping does. They
  are listed in the ruleset for readability, but Docker is what exposes them.
- **LiveKit's ports do go through ufw**, because it runs with
  `network_mode: host`. That includes 7880, which is allowed only `in on
  docker0` and `in on br-videostream` — the bridges caddy and app reach the host
  from. It is never open to the internet. The compose file pins that bridge's
  interface name, since ufw cannot match `br-*` wildcards.

The TURN hairpin DNAT lives in a managed block at the top of
`/etc/ufw/before.rules` rather than in `iptables-persistent`, so it survives
`ufw reload` and reboots. Re-running `oracle-setup.sh` rewrites that block,
which is how a changed `PUBLIC_IP` gets picked up.

**ufw is not a substitute for the VCN security list.** Oracle drops traffic
before it reaches the instance, so a port must be open in *both* — unless you
have deliberately left the security list wide open, in which case ufw is the
only thing enforcing anything.

### Certificate renewal

LiveKit reads the TURN certificate **once, at startup**. Caddy renews ~30 days
before expiry, so restart LiveKit periodically to pick up the new file:

```bash
sudo crontab -e
# 04:20 every Monday
20 4 * * 1 cd /home/ubuntu/videostream/deploy && /usr/bin/docker compose restart livekit
```

A restart drops in-flight WebRTC sessions; clients reconnect, but do not
schedule it for movie night.

## 5. Verify

Install the CLI on your laptop (`brew install livekit-cli`), then publish the
demo video into a room:

```bash
lk room join \
  --publish-demo \
  --url wss://watch.example.com \
  --api-key APIxxxxxxxxxxxx \
  --api-secret "$(ssh ubuntu@203.0.113.10 'grep ^LIVEKIT_API_SECRET= videostream/deploy/.env | cut -d= -f2')" \
  --identity test \
  --room test
```

`room.auto_create` is `false`, so create the room first if the app server has
not:

```bash
lk room create --url wss://watch.example.com --api-key ... --api-secret ... test
```

Join the same room from a browser (or a second `lk room join --identity test2`)
and confirm the video plays. Three checks that actually exercise the network
design:

**1. UDP blocked on the client → must fall back to ICE/TCP on 7881.**

macOS (pf), for the duration of the test:

```bash
echo 'block drop out proto udp from any to any port 7882' | sudo pfctl -ef -
# ...run the test...
sudo pfctl -d
```

Linux: `sudo iptables -A OUTPUT -p udp --dport 7882 -j DROP` (delete with `-D`).

The call must still connect. In `chrome://webrtc-internals`, the selected
candidate pair shows `tcp` and remote port 7881.

**2. Force TURN.** In Chrome DevTools on the join page, or with a build flag,
create the room with `iceTransportPolicy: 'relay'`; or simply block both 7882/UDP
and 7881/TCP so only 3478/5349 remain. The selected pair should show a `relay`
local candidate. This is also the test for the hairpin DNAT: without the
`iptables -t nat -A OUTPUT -d <public> -j DNAT --to <private>` rule the relay
allocation is made but media never flows.

**3. ICE candidates advertise the public IP.** In `webrtc-internals`, remote
candidates must be `203.0.113.10`, not `10.0.0.x`. If you see the private
address, `rtc.node_ip` / `use_external_ip` is wrong in `livekit.yaml`.

Also worth doing once: a phone on cellular (not Wi-Fi) joining the demo room.

## 6. Day-to-day

```bash
cd ~/videostream/deploy

sudo docker compose ps
sudo docker compose logs -f app            # or caddy / livekit
sudo docker compose logs --since 15m livekit

git pull && sudo docker compose build app && sudo docker compose up -d  # deploy new code
sudo docker compose pull && sudo docker compose up -d   # update caddy / livekit
sudo docker compose restart livekit                     # after editing livekit.yaml
sudo docker compose down                                # stop everything
```

After editing `livekit.yaml.tmpl` or `.env`, re-render before restarting — or
just re-run `./oracle-setup.sh`, which does it for you:

```bash
set -a; . ./.env; set +a
envsubst '${DOMAIN} ${PUBLIC_IP} ${LIVEKIT_API_KEY} ${LIVEKIT_API_SECRET}' \
  < livekit.yaml.tmpl > livekit.yaml
sudo docker compose restart livekit
```

Back up `./data/videostream.db` (chat history and room rows); everything else is
reproducible.

### Key rotation

The API key/secret pair lives in exactly two places: `.env` (read by the app
server) and `livekit.yaml` (the `keys:` block). Rotate both together:

```bash
cd ~/videostream/deploy
cp .env .env.bak
NEW_KEY="API$(tr -dc 'A-Za-z0-9' </dev/urandom | head -c 12)"
NEW_SECRET="$(tr -dc 'A-Za-z0-9_-' </dev/urandom | head -c 32)"
sed -i "s|^LIVEKIT_API_KEY=.*|LIVEKIT_API_KEY=${NEW_KEY}|;s|^LIVEKIT_API_SECRET=.*|LIVEKIT_API_SECRET=${NEW_SECRET}|" .env
set -a; . ./.env; set +a
envsubst '${DOMAIN} ${PUBLIC_IP} ${LIVEKIT_API_KEY} ${LIVEKIT_API_SECRET}' \
  < livekit.yaml.tmpl > livekit.yaml
envsubst '${LIVEKIT_API_KEY} ${LIVEKIT_API_SECRET}' < ingress.yaml.tmpl > ingress.yaml
sudo docker compose up -d --force-recreate app livekit
```

Everyone currently in a room is disconnected and has to rejoin (their tokens
were signed with the old secret). Rotating `SESSION_SECRET` the same way just
invalidates sessions; rotating it alone needs only `app` restarted.

### Enabling the ingress profile

Only needed for OBS → WHIP/RTMP screen sharing (Phase 4). It adds Redis and the
ingress service, both on the host network.

```bash
# 1. open the extra ports: TCP 1935, UDP 7885 in the VCN security list,
#    and uncomment the two `allow` lines near the end of the firewall
#    section of oracle-setup.sh, then re-run it.
# 2. uncomment the @whip block in the Caddyfile.
# 3. start it:
sudo docker compose --profile ingress up -d
sudo docker compose --profile ingress logs -f ingress
```

Stop it again with `sudo docker compose --profile ingress down` (or
`docker compose stop ingress redis`). The projector does not need any of this.

---

## Local development

No Docker, no Oracle. Three terminals:

```bash
brew install livekit livekit-cli go   # plus: brew install mpv ffmpeg node for the projector/web

# 1. the SFU
livekit-server --config deploy/dev/livekit-dev.yaml

# 2. the app server
cd server && LIVEKIT_API_KEY=devkey LIVEKIT_API_SECRET=secret go run ./cmd/server

# 3. the web app
cd web && npm run dev
```

`dev/livekit-dev.yaml` uses the well-known `devkey` / `secret` pair,
`use_external_ip: false`, `room.auto_create: true` and no TURN — everything is
on loopback, so there is nothing to discover or relay. It is not safe to expose;
never use it on a public box.

Publish a test movie track into a dev room without the projector:

```bash
lk room join --publish-demo --url ws://localhost:7880 \
  --api-key devkey --api-secret secret --identity test --room test
```

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| Caddy retries ACME forever | DNS not propagated, or TCP 80 closed in the VCN |
| Page loads, joining hangs | `/rtc*` not proxied, or 7880 not listening on the host (`sudo ss -lntp \| grep 7880`) |
| Caddy logs `dial tcp 172.17.0.1:7880: no route to host`, Twirp 502s | the firewall is eating container→host traffic; 7880 must be allowed in on `docker0` and `br-videostream` (oracle-setup.sh does this) |
| Video connects only on TCP | UDP 7882 blocked — check the VCN rule *and* `sudo ufw status` |
| Remote candidates are `10.0.0.x` | `rtc.node_ip` / `use_external_ip` wrong, or `PUBLIC_IP` stale after an IP change |
| TURN never relays | hairpin DNAT missing (`sudo iptables -t nat -L OUTPUT -n`; it is defined in `/etc/ufw/before.rules`), or UDP 30000–30100 closed |
| `livekit` logs a TURN cert error | certificate not issued yet, or the path in `livekit.yaml` does not match — `sudo docker compose exec livekit ls /caddy/caddy/certificates/*/turn.$DOMAIN/` |
| Rules vanish after reboot | ufw not enabled (`sudo ufw status`), or something re-enabled `netfilter-persistent` alongside it |
| `app` restarts on "unable to open database file (14)" | `deploy/data` not owned by the container user — `sudo chown -R 65532:65532 deploy/data` |
| App page says "web build not present" | the image was built without the web stage — `sudo docker compose build --no-cache app` |
| `compose build app` fails on `npm ci` | out of memory or disk — `df -h`, and check the shape really has 24 GB RAM |
