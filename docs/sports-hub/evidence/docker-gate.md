# Docker image gate — attempt record

**Status: BLOCKED. The image was not built or started; this is not a pass.**
**Date:** 2026-10-24 (re-probed 2026-10-25 04:47 UTC, again 2026-10-25 05:05 UTC)

The built-binary gate is recorded separately in [gate-run.md](gate-run.md) (9/9).
That run is real-browser evidence for a real server deployment, but it is **not**
the container-image gate, and P5/P6 stay blocked until this file can record a
pass.

## Runtime probe (repeated, not assumed)

```
docker:   not installed
podman:   not installed
buildah:  not installed
nerdctl:  not installed
docker socket: /var/run/docker.sock: No such file or directory
unshare -Ur: Operation not permitted
unshare -n:  Operation not permitted
unshare -m:  Operation not permitted   # no mount namespace either (re-probed)
CapEff: 00000000a80425fb      # Docker's default set: no CAP_SYS_ADMIN
/dev/fuse: absent
/proc/1/cgroup: 0::/          # we are already inside a container
/proc/self/mountinfo: root is overlayfs under /var/lib/docker/overlay2/…
```

The host clock and probe were repeated for this milestone. `unshare -m` is the
addition: even a mount namespace is refused, so neither `dockerd` nor rootless
podman could be started from this process, and installing a package cannot
change that.

There is no runtime, no socket and no capability to create one. Both `dockerd`
and rootless podman need namespaces and mounts this kernel refuses to the
current process (`unshare` for a user and a network namespace already fails
before any daemon starts), so installing a package cannot turn this host into a
Docker-capable host. The documented `docker build` / `docker run` were therefore
not executed and **no image tag or digest exists**.

## What the image build would do

Read from the checked-in `Dockerfile` (unchanged by this milestone):

- **build stage** `golang:1.27-bookworm`, `go mod download`, then
  `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /out/wrentv ./cmd/wrentv`;
- **runtime stage** `debian:bookworm-slim` plus `jellyfin-ffmpeg7` from the
  Jellyfin repository (for `zscale`/tonemapping and NVENC/QSV/VAAPI/AMF);
- `ENV WRENTV_DATA_DIR=/config WRENTV_FFMPEG=/usr/lib/jellyfin-ffmpeg/ffmpeg
  WRENTV_FFPROBE=/usr/lib/jellyfin-ffmpeg/ffprobe`,
  `VOLUME ["/config"]`, `EXPOSE 32500`,
  `HEALTHCHECK … curl -fsS http://127.0.0.1:32500/api/health`, and the binary as
  `ENTRYPOINT`;
- all web assets are embedded, so the image needs no separate static copy.

## What was verified instead (image contract, not the gate)

[docker-image-contract.txt](docker-image-contract.txt) records a preflight that
exercises the parts of the image this host *can* run, with the image's own build
flags and runtime variables (data directory substituted for `/config`, system
ffmpeg substituted for the Jellyfin paths because the Debian package cannot be
installed here):

- the binary builds with the Dockerfile's exact flags and reports the injected
  `VERSION` (`Wren TV gate-preflight`);
- it is a static ELF (`ldd`: "not a dynamic executable"), so the slim runtime
  stage needs no Go/libc packages;
- started with `WRENTV_DATA_DIR`, `WRENTV_FFMPEG`, `WRENTV_FFPROBE`, it creates
  `<dataDir>/wrentv.yaml`, `wrentv.db`, `cache/`, `images/` and `transcode/`;
- `/api/health` returns `200 {"status":"ok"}` (the image's healthcheck target),
  `/` serves the app shell, and `/js/app.js`, `/js/sports.js`,
  `/js/sports-model.js`, `/js/live-player.js`, `/sports.css` are served from the
  embedded assets with correct content types (no missing file in the image);
- `/api/server` reports `sports: true`, `liveTv: false` (this preflight config
  disabled it) and a machine id; `/api/sports/status` without a session returns
  `401`, so the sports API is not accidentally public;
- killing and restarting on the same data directory keeps the generated
  `machine_id` and answers health again, so the `/config` volume contract holds;
- the only log warnings are the expected unconfigured-TMDB, unclaimed-server and
  shutdown-cancelled sports refresh messages.

This preflight cannot show that the image starts, that its healthcheck passes
inside a container, that the container network reaches providers, or that the
Jellyfin ffmpeg build probes and remuxes as expected. Those are precisely why
the gate is still blocked.

## What would satisfy this gate

```sh
docker build -t wrentv:gate .
docker inspect --format '{{.Id}} {{.RepoDigests}}' wrentv:gate
docker run -d --name wrentv-gate -p 8099:32500 \
  -v "$PWD/e2e/gate-data:/config" wrentv:gate
# The image healthcheck is the same request; this proves it from the host too.
docker exec wrentv-gate curl -fsS http://127.0.0.1:32500/api/health
```

Then build the same deployment the local runner builds, inside the container's
`/config`: a `wrentv.yaml` with the fixture addon and a Live TV source pointing
at the fixture provider (`http://host.docker.internal:8391` on Docker Desktop,
or the host's LAN address on Linux), the generated playlist/guide written where
that source reads them, and a seeded local admin. Start `e2e/fixture-server.mjs`
on the host with the same files, and run the spec against the image:

```sh
WRENTV_BASE_URL=http://127.0.0.1:8099 \
WRENTV_E2E_USER=gate WRENTV_E2E_PASSWORD=gate-password \
WRENTV_FIXTURE_URL=http://127.0.0.1:8391 \
npx playwright test -c e2e/playwright.config.js
```

`node e2e/run-gate.mjs` always builds and starts its own local binary, so for the
image use the command above. Record on that host: build output, image id/digest,
`docker logs wrentv-gate`, the healthcheck result, the Playwright report
(`playwright-report.json`, `playwright-run.txt`), the playlist-channel quality
measurement (`POST /api/sports/streams/quality`) and any failures. Until then the
release gate stays **blocked**, not "passing locally".
