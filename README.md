# Wren TV

A fast, ad-free personal media server. Wren TV aims for **direct play at full
quality**: Dolby Vision and Dolby Atmos pass through untouched. Transcoding
happens only when the client truly can't play the file. With a debrid
account, it can also stream any movie or show, Stremio-style, for everyone
you share the server with.

- **Runs anywhere:** Docker, Linux (systemd), macOS (launchd) and Windows (service). Single static Go binary.
- **Hardware transcoding:** VideoToolbox, NVENC, Quick Sync, VAAPI and AMF, auto-detected. HDR→SDR tone mapping runs on the GPU.
- **Playback ladder:**
  1. Direct play.
  2. Lossless remux to HLS/fMP4. Dolby Vision and HDR10 are kept, and Atmos (E-AC-3 JOC) passes through.
  3. TrueHD/DTS audio becomes lossless ALAC while the HDR video stays native.
  4. Clients with libmpv play anything directly (for example DV profile 7).
  5. Transcode, as a last resort.
- **Fast scanning:**
  - Incremental and parallel: only new or changed files are touched.
  - Items appear instantly from their file names; metadata (TMDB) fills in concurrently.
  - Watched folders pick up new episodes in seconds.
- **Streaming (optional):**
  - Search shows every movie and show on TMDB. Press play and the server finds a cached release through TorBox, AllDebrid or Real-Debrid using Stremio addons (Torrentio and Comet by default), then streams it through itself.
  - It saves the stream for the rest of the season, and the other quality (4K and 1080p), for later. Only links are stored, never video, unless download mode is on.
  - Streams and files mix: a file added later replaces the stream. Without a debrid key, streamed titles disappear.
  - Limits for size per quality, bandwidth, concurrent streams and downloads, and disk use. Per-user stream and download limits.
- **Downloads:** viewers can save a movie or episode to their device (the Mac app plays them offline). Admins can download streamed titles to the server as regular files.
- **Accounts:** local accounts today. Supabase-backed Plex-style accounts (server claiming, sharing, device linking) are ready to switch on.

## Quick start

### Docker

```bash
docker compose -f deploy/docker/docker-compose.yml up -d
```

Every push to `main` publishes `ghcr.io/ltagroup/wrentv:latest` (GitHub Actions, `.github/workflows/docker.yml`).
To update a server that uses the image:

```bash
docker compose pull && docker compose up -d
```

Use `network_mode: host` on Linux so automatic port forwarding (UPnP) and LAN address detection see the real network.
Mount your media read-only (for example at `/media`). Config and the database live in `/config`. The image bundles
jellyfin-ffmpeg; uncomment `/dev/dri` (Intel/AMD) or the NVIDIA block in the compose file for hardware transcoding.

### Native

```bash
go build -o bin/wrentv ./cmd/wrentv
./bin/wrentv                 # run in the foreground
sudo ./bin/wrentv install    # install as a system service (systemd / launchd / Windows SCM)
./bin/wrentv start|stop|restart|status|uninstall|version
```

Requires `ffmpeg` and `ffprobe` on `PATH` (or set `transcode.ffmpeg_path`). On macOS, `brew install ffmpeg`.
Unit files: `deploy/systemd/wrentv.service`, `deploy/launchd/com.wrentv.server.plist`, `deploy/windows/README.md`.

### First run

```bash
curl -X POST localhost:32500/api/setup/owner -d '{"username":"me","password":"…"}'      # → {token}
curl -X POST localhost:32500/api/libraries -H "Authorization: Bearer $TOKEN" \
     -d '{"name":"Movies","type":"movie","paths":["/media/Movies"]}'
```

## Configuration

`wrentv.yaml` lives in the data directory and is created on first start:

| Platform | Data directory                         |
|----------|----------------------------------------|
| Docker   | `/config`                              |
| macOS    | `~/Library/Application Support/Wren TV` |
| Linux    | `/var/lib/wrentv` (root) or `~/.local/share/wrentv` |
| Windows  | `C:\ProgramData\WrenTV`                |

```yaml
server_name: Wren TV
listen: ":32500"
public_url: ""                 # externally reachable URL
integration_key: wtv_…         # generated; for backend services (X-WrenTV-Key)
auth:
  mode: local                  # local | supabase
  supabase: { url: "", publishable_key: "" }
remote_access:                 # automatic HTTPS addresses (supabase mode only)
  enabled: true
  upnp: true                   # forward a port with UPnP / NAT-PMP
  public_port: 0               # a port you forwarded by hand (replaces UPnP)
metadata:
  tmdb_api_key: ""
  language: en-US
  workers: 8
transcode:
  ffmpeg_path: ffmpeg
  ffprobe_path: ffprobe
  hwaccel: auto                # auto | videotoolbox | nvenc | qsv | vaapi | amf | none
  vaapi_device: /dev/dri/renderD128
streaming:
  enabled: false
  torbox_key: ""
  alldebrid_key: ""
  realdebrid_key: ""
  provider_order: [torbox, alldebrid, realdebrid]
  addons:
    - { name: torrentio, url: "https://torrentio.strem.fun/manifest.json", enabled: true }
    - { name: comet, url: "https://comet.feels.legal/manifest.json", enabled: true }
  qualities: [2160p, 1080p]    # a stream is saved for each
  max_size_gb: { 2160p: 0, 1080p: 0 } # per file; 0 = any size
  prefetch: true               # find the rest of the season in the background
  mode: stream                 # stream | download (also save what's watched as files)
  movie_library: ""            # where downloads go (library ids)
  show_library: ""
  download_quality: 2160p
  limits:                      # 0 = no limit
    max_streams: 0             # debrid streams at once, all users
    bandwidth_mbps: 0          # all debrid traffic
    per_stream_mbps: 0
    download_mbps: 0
    max_downloads: 2           # server downloads at once
    prefetch_workers: 2
    download_quota_gb: 0
    min_free_gb: 20
users:                         # defaults; each user can override them
  max_streams: 0               # playbacks at once per user
  device_downloads: true
  max_downloads: 2             # device downloads at once per user
webhooks:
  - url: https://backend.example.com/wrentv
    secret: change-me
    events: [download.completed, download.failed, stream.unavailable]
scanner:
  watch: true
  poll_sec: 0                  # folder poll interval (0 = 5s macOS, 30s elsewhere)
  interval_min: 30             # full incremental rescan safety net
  probe_workers: 4             # -1 = probe on first play only (best for debrid/cloud mounts)
```

Environment overrides: `WRENTV_DATA_DIR`, `WRENTV_LISTEN`, `WRENTV_SERVER_NAME`, `WRENTV_PUBLIC_URL`,
`WRENTV_AUTH_MODE`, `WRENTV_SUPABASE_URL`, `WRENTV_SUPABASE_PUBLISHABLE_KEY`, `WRENTV_TMDB_API_KEY`,
`WRENTV_FFMPEG`, `WRENTV_FFPROBE`, `WRENTV_HWACCEL`, `WRENTV_SCAN_POLL_SEC`, `WRENTV_STREAMING`,
`WRENTV_TORBOX_API_KEY`, `WRENTV_ALLDEBRID_API_KEY`, `WRENTV_REALDEBRID_API_KEY`,
`WRENTV_REMOTE_ACCESS`, `WRENTV_PUBLIC_PORT`, `WRENTV_ACME_DIRECTORY`, `WRENTV_DIRECT_API`.

## Upgrading from Haven

Wren TV was called Haven. Upgrades keep your data:

- On first start, the old default data directory (`Application Support/Haven`, `/var/lib/haven`,
  `~/.local/share/haven`, `C:\ProgramData\Haven`) is moved to the new one, with a symlink left at the
  old path; if it can't be moved it is used in place. `haven.yaml` and `haven.db` are renamed to
  `wrentv.yaml` and `wrentv.db`.
- `HAVEN_*` environment variables still work (`WRENTV_*` wins when both are set).
- `wrentv install` stops and uninstalls an old `haven` service first.
- Sign-in tokens, `hvn_` integration keys and `X-Haven-*` request headers keep working; webhooks send
  both `X-WrenTV-*` and `X-Haven-*` headers.
- Docker: the container and image are now `wrentv`; keep mounting the same `/config` volume.
- Hand-installed systemd unit (`haven.service`, which runs as user `haven`): the new unit runs as `wrentv`,
  which can't move the old directory itself. Run `systemctl disable --now haven`, create the `wrentv` user,
  then `mv /var/lib/haven /var/lib/wrentv && chown -R wrentv:wrentv /var/lib/wrentv` before enabling
  `wrentv.service`.

Back up the data directory before upgrading.

## Streaming

Turn it on in **Settings → Streaming** in the web app or Mac app, add a debrid API key, and keep at least one addon.
Everyone who may stream (on by default, set per user) then sees two extra libraries, *Streaming Movies* and
*Streaming Shows*, and search results for titles the server doesn't have.

- Pressing play asks the addons for releases, keeps the ones your debrid service has **cached** (uncached releases
  are never downloaded), and plays the best match. Bytes always go through this server, so debrid links stay private.
- Season packs fill every episode they contain, and the rest of the season is looked up in the background.
- When a stored link stops working, the server re-adds the release; after repeated failures it forgets the stream and
  searches again. Admins can force this with **Find new streams** on a title.
- A file for the same movie or episode in a regular library takes precedence, and the streamed copy is hidden.
- Download mode (or **Download to server** on a title) saves streamed titles into the libraries you pick, named
  `Title (Year) {tmdb-ID}/…`. A disk quota and a free-space floor stop downloads before the disk fills.

Upgrading from a version with torrent support removes the torrent engine and its `/api/torrents` and
`/api/integration/torrents` endpoints, and deletes torrent-backed items. A Real-Debrid key from the old
`torrents.debrid` setting carries over.

## Integration API (for backend services)

Backends authenticate with the integration key: `X-WrenTV-Key: wtv_…`.

| Method | Path | Body |
|--------|------|------|
| GET    | `/api/integration/libraries` | |
| POST   | `/api/integration/libraries` | `{"name","type":"movies"|"shows","paths":[…]}` |
| POST   | `/api/integration/libraries/{id}/scan` | |

Webhooks are signed with the webhook secret:

```http
POST https://backend.example.com/wrentv
X-WrenTV-Event: download.completed
X-WrenTV-Signature: sha256=<hex HMAC-SHA256 of the body using the webhook secret>
```

```json
{
  "event": "download.completed",
  "id": "…",
  "timestamp": 1760000000,
  "server": { "id": "…", "name": "Wren TV", "url": "https://wrentv.example.com" },
  "data": { "id": "…", "itemId": "…", "title": "…", "quality": "2160p", "status": "done", "path": "/media/Movies/…", "bytes": 0, "total": 0 }
}
```

Events: `download.completed`, `download.failed` (a server download) and `stream.unavailable` (no cached release
was found for a title someone tried to play).

## Client API (summary)

Authenticate with `Authorization: Bearer <token>` (from `/api/auth/login`).

- `GET /api/home`: continue watching, next up and recently added, limited to the libraries the user can access.
- `GET /api/libraries`, `GET /api/libraries/{id}/items`, `GET /api/items/{id}`, `GET /api/items/{id}/children`, `GET /api/search?q=`
- `POST /api/items/{id}/play` with `{"caps": {...}, "audioIndex", "maxWidth"}` returns `{engine, method, url, …}`, or `202 pending` while a stream is being found
- `GET /api/discover/search?q=`: every matching movie and show, linked to library items; `POST /api/streaming/titles` `{"tmdbId","mediaType"}` adds one to the streaming library
- `POST /api/items/{id}/download` `{"fileId"}` returns a 24-hour link (`/download/<ticket>/<name>`) to the original file, or `202 pending`
- `POST /api/items/{id}/progress`, `POST /api/items/{id}/watched`, `DELETE /api/sessions/{sid}`
- `GET /api/events`: server-sent events (library changes, scan progress)
- `GET /api/me` includes the user's effective `limits`
- Admin: `/api/libraries` (CRUD and scan), `/api/users` (with per-user `limits`), `/api/admin/status`, `/api/admin/settings`,
  `/api/admin/downloads`, `/api/items/{id}/server-download`, `/api/items/{id}/streams`

## Accounts with Supabase (optional)

`supabase/migrations/` contains the account backend: profiles, server claiming, and sharing and invites.
Apply the migrations to your Supabase project, then set `auth.mode: supabase` with your project URL and publishable
key. Open the web app, sign in, and claim the server with the claim code printed in the server log.

### Remote access (automatic HTTPS)

A claimed server gets secure addresses without a domain or reverse proxy, like Plex's plex.direct:

- `https://192-168-1-20.<id>.wrentv.net:32500` for each LAN address, and
- `https://203-0-113-7.<id>.wrentv.net:<port>` for its public address, when it's reachable from the internet.

The server forwards a port on the router (UPnP or NAT-PMP, or `remote_access.public_port` for a manual forward),
asks the address service (`api.wrentv.net`, a Cloudflare Worker in the website repo) to publish the DNS records,
and gets a wildcard Let's Encrypt certificate for `*.<id>.wrentv.net` with a DNS-01 challenge. The certificate is
stored in `<data>/certs/direct/` and renewed automatically. HTTP and HTTPS share the listen port. The addresses are
published in the heartbeat (`local_urls` and `remote_url`), so the web player at watch.wrentv.com and the apps can
use them. Video always goes straight from the server to the viewer.

Behind CGNAT (an internet provider sharing one public IP between customers) the server is only reachable at home;
the settings page says so. Some routers block DNS answers that point to LAN addresses ("DNS rebinding protection"):
allow `wrentv.net` there, or viewers at home fall back to the public address.

