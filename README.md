# Haven

A fast, ad-free personal media server. Haven aims for **direct play at full
quality**: Dolby Vision and Dolby Atmos pass through untouched. Transcoding
happens only when the client truly can't play the file. Torrents are
first-class media sources.

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
- **Torrents:**
  - Add a magnet or .torrent and play it immediately.
  - Real-Debrid is used when the torrent is cached; otherwise the built-in engine streams pieces in playback order.
  - **Never seeds.**
  - Unplayable torrents fire a webhook so a backend (Riven-style) can supply a replacement. Players just see "Finding a better source…".
- **Accounts:** local accounts today. Supabase-backed Plex-style accounts (server claiming, sharing, device linking) are ready to switch on.

## Quick start

### Docker

```bash
docker compose -f deploy/docker/docker-compose.yml up -d
```

Mount your media read-only (for example at `/media`). Config and the database live in `/config`. The image bundles
jellyfin-ffmpeg; uncomment `/dev/dri` (Intel/AMD) or the NVIDIA block in the compose file for hardware transcoding.

### Native

```bash
go build -o bin/haven ./cmd/haven
./bin/haven                 # run in the foreground
sudo ./bin/haven install    # install as a system service (systemd / launchd / Windows SCM)
./bin/haven start|stop|restart|status|uninstall|version
```

Requires `ffmpeg` and `ffprobe` on `PATH` (or set `transcode.ffmpeg_path`). On macOS, `brew install ffmpeg`.
Unit files: `deploy/systemd/haven.service`, `deploy/launchd/com.haven.server.plist`, `deploy/windows/README.md`.

### First run

```bash
curl -X POST localhost:32500/api/setup/owner -d '{"username":"me","password":"…"}'      # → {token}
curl -X POST localhost:32500/api/libraries -H "Authorization: Bearer $TOKEN" \
     -d '{"name":"Movies","type":"movie","paths":["/media/Movies"]}'
```

## Configuration

`haven.yaml` lives in the data directory and is created on first start:

| Platform | Data directory                         |
|----------|----------------------------------------|
| Docker   | `/config`                              |
| macOS    | `~/Library/Application Support/Haven`  |
| Linux    | `/var/lib/haven` (root) or `~/.local/share/haven` |
| Windows  | `C:\ProgramData\Haven`                 |

```yaml
server_name: Haven
listen: ":32500"
public_url: ""                 # externally reachable URL (used in webhook replaceUrl)
integration_key: hvn_…         # generated; for backend services (X-Haven-Key)
auth:
  mode: local                  # local | supabase
  supabase: { url: "", publishable_key: "" }
metadata:
  tmdb_api_key: ""
  language: en-US
  workers: 8
transcode:
  ffmpeg_path: ffmpeg
  ffprobe_path: ffprobe
  hwaccel: auto                # auto | videotoolbox | nvenc | qsv | vaapi | amf | none
  vaapi_device: /dev/dri/renderD128
torrents:
  enabled: true
  providers: [debrid, builtin] # tried in order
  listen_port: 42069
  debrid: { service: realdebrid, api_key: "" }
  health:
    metadata_timeout_sec: 60   # no metadata in time → unplayable
    stall_timeout_sec: 45      # playback starved this long → unplayable
    min_peers: 1
    replace_timeout_min: 30    # no replacement in time → torrent.failed
webhooks:
  - url: https://backend.example.com/haven
    secret: change-me
    events: [torrent.unplayable, torrent.failed]
scanner:
  watch: true
  poll_sec: 0                  # folder poll interval (0 = 5s macOS, 30s elsewhere)
  interval_min: 30             # full incremental rescan safety net
  probe_workers: 4             # -1 = probe on first play only (best for debrid/cloud mounts)
```

Environment overrides: `HAVEN_DATA_DIR`, `HAVEN_LISTEN`, `HAVEN_SERVER_NAME`, `HAVEN_PUBLIC_URL`,
`HAVEN_AUTH_MODE`, `HAVEN_SUPABASE_URL`, `HAVEN_SUPABASE_PUBLISHABLE_KEY`, `HAVEN_TMDB_API_KEY`,
`HAVEN_FFMPEG`, `HAVEN_FFPROBE`, `HAVEN_HWACCEL`, `HAVEN_SCAN_POLL_SEC`, `HAVEN_TORRENT_PROVIDERS`, `HAVEN_DEBRID_API_KEY`.

## Torrent integration (for backend services)

Torrent providers are configured on the server; users never see them. Backends authenticate
with the integration key: `X-Haven-Key: hvn_…`.

| Method | Path | Body |
|--------|------|------|
| GET    | `/api/integration/libraries` | |
| POST   | `/api/integration/libraries` | `{"name","type":"movies"|"shows","paths":[…]}` |
| POST   | `/api/integration/libraries/{id}/scan` | |
| POST   | `/api/integration/torrents` | `{"libraryId","magnet","externalRef"}` or multipart with `file` |
| GET    | `/api/integration/torrents/{id}` | |
| POST   | `/api/integration/torrents/{id}/replace` | `{"magnet"}` or multipart `file` |
| DELETE | `/api/integration/torrents/{id}` | |

Each torrent's files are added to the library as normal items. If a torrent can't be played (dead, no peers,
stalled, corrupt, not video), Haven marks it `unplayable` and sends a webhook:

```http
POST https://backend.example.com/haven
X-Haven-Event: torrent.unplayable
X-Haven-Signature: sha256=<hex HMAC-SHA256 of the body using the webhook secret>
```

```json
{
  "event": "torrent.unplayable",
  "id": "…",
  "timestamp": 1760000000,
  "server": { "id": "…", "name": "Haven", "url": "https://haven.example.com" },
  "data": {
    "torrent": { "id": "…", "infoHash": "…", "name": "…", "magnet": "…", "provider": "builtin",
                 "status": "unplayable", "reason": "stalled: no data for 45s", "externalRef": "riven-123",
                 "libraryId": "…" },
    "items": [ { "id": "…", "kind": "episode", "title": "…", "season": 1, "episode": 2,
                 "showTitle": "…", "showTmdbId": 1399, "showImdbId": "tt0944947" } ],
    "replaceUrl": "https://haven.example.com/api/integration/torrents/…/replace"
  }
}
```

The backend responds by POSTing a new magnet to `replaceUrl`. The same library items, watch history and
artwork are kept, and players that requested playback in the meantime got `202 {"status":"pending"}`
and simply retry. If no replacement arrives within `replace_timeout_min`, `torrent.failed` is sent.

## Client API (summary)

Authenticate with `Authorization: Bearer <token>` (from `/api/auth/login`).

- `GET /api/home`: continue watching, next up and recently added, limited to the libraries the user can access.
- `GET /api/libraries`, `GET /api/libraries/{id}/items`, `GET /api/items/{id}`, `GET /api/items/{id}/children`, `GET /api/search?q=`
- `POST /api/items/{id}/play` with `{"caps": {...}, "audioIndex", "maxWidth"}` returns `{engine, method, url, …}`, or `202 pending`
- `POST /api/items/{id}/progress`, `POST /api/items/{id}/watched`, `DELETE /api/sessions/{sid}`
- `GET /api/events`: server-sent events (library changes, scan progress, torrent status)
- Admin: `/api/libraries` (CRUD and scan), `/api/users`, `/api/torrents`, `/api/admin/status`, `/api/admin/settings`

## Accounts with Supabase (optional)

`supabase/migrations/` contains the account backend: profiles, server claiming, and sharing and invites.
Apply the migrations to your Supabase project, then set `auth.mode: supabase` with your project URL and publishable
key. Open the web app, sign in, and claim the server with the claim code printed in the server log.
