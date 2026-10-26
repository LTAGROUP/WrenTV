# Sports deployment gate (dev only)

Playwright is a development dependency. It is **not** part of the server build or
the Docker image; CI installs it in a separate job.

## Run it

```sh
npm install                 # @playwright/test
npx playwright install chromium
go build ./...              # the runner builds the binary too
node e2e/run-gate.mjs
```

`run-gate.mjs` builds the server with the Dockerfile's flags, creates a throwaway
data directory (`/tmp/wrentv-gate`), seeds a local admin (`e2e/seed`), starts the
fixture provider, generates a Live TV playlist from the day's real events, and
runs Playwright against that server. It needs:

- Go (built into the runner),
- ffmpeg and ffprobe (`/usr/bin`) so quality measurement is real,
- network access to `site.api.espn.com` for sports data,
- Node 20+.

Environment: `WRENTV_GATE_DATA`, `WRENTV_GATE_PORT` (default 8099),
`WRENTV_GATE_USER` / `WRENTV_GATE_PASS` (default `gate` / `gate-password`).

Against an existing deployment (with a fixture provider running separately):

```sh
WRENTV_BASE_URL=http://localhost:8099 \
WRENTV_E2E_USER=admin WRENTV_E2E_PASSWORD=... \
WRENTV_FIXTURE_URL=http://127.0.0.1:8391 \
npx playwright test -c e2e/playwright.config.js
```

## Browse Live TV regression

`run-browse-livetv.mjs` is a smaller deployment of the same idea for one
question: does the first *Browse Live TV* click search every provider? It builds
the server, generates a playlist channel for most of the day's events, gives a
second Live TV source (`/addon-events/manifest.json`) the same events as event
listings, and leaves at least one hub event in neither. `browse-livetv.spec.js`
then opens one event page, clicks Browse Live TV once, and asserts the provider
event is listed (and plays) without any search interaction; a second test opens
an event no provider carries and asserts the page settles on the empty answer
instead of waiting forever.

The same spec covers playback of an addon event and the two provider-limit
behaviours a viewer can hit:

- *a rate-limited provider is reported honestly and is not asked again* — the
  fixture addon is switched, through `POST /control/rate-limit`, into answering
  its stream endpoint with `429` and `Retry-After: 30`. The player shows the
  server's own message (the provider is rate limiting this server and asked to
  wait 30 s), the addon was asked exactly once (its request count is read back
  from the fixture), no session is left behind, and *Try again* after the
  provider recovers resolves the feed afresh and plays.
- *a conventional IPTV channel still plays beside the provider events* — the
  playlist channel keeps its own row, its provider label and its guide data in
  the merged list, plays through the ordinary channel session and releases it on
  close, so the addon entries did not displace the IPTV path.
- *switching events leaves one provider session at a time* — two provider events
  are watched in turn; each leaves exactly one session while playing and none
  after the player closes, so switching never keeps the previous stream running.

The fixture's `/control/rate-limit` (`GET` to read `{seconds, hits}`, `POST
{seconds}` to set or clear it, which also resets the count) is a test harness
control, not part of the server.

## SportsHub Watch panel regression

`e2e/sports-watch.spec.js` is run by the same deployment, because it needs the
same thing that run already builds: one event carried by *both* a playlist
channel and the provider, so one event page shows a conventional IPTV row and a
Stremio row together. It asks the server for such an event (live or scheduled,
with a matched channel group and exactly one matched provider item) and throws
if none exists rather than skipping.

- *a Stremio entry offers the same Preview and Watch controls as a channel* —
  both rows expose `Preview` and `Watch` in the same order; the Stremio row's
  Preview and Watch both call `/api/livetv/addons/{source}/item/{type}/{id}/play`
  (its source, type and item id intact) and never the channel route, the channel
  row's Preview and Watch call `/api/livetv/channels/{id}/play` and never the
  addon route, and switching between them — including opening the full player
  over a playing preview — leaves exactly one session, with none after close.
- *a Stremio Preview falls back to the next feed and reports one that cannot
  tune* — the fixture item is switched to offering a feed that 404s in front of
  the working one: the preview still plays, and the fixture's media log shows the
  dead feed was tried before the working one. Switched to offering only the dead
  feed, the preview reports the failure without a URL and holds no session; with
  the provider rate limiting this server it reports the wait it asked for, asks
  exactly once and also holds no session.

The fixture's `/control/event-feeds` (`GET` to read `{mode}`, `POST {mode}` with
`ok`, `fallback` or `broken`) is a test harness control, not part of the server:
in `fallback` the dead feed advertises a higher quality than the working one, so
the server's own ranking puts it first and the fallback is the one a viewer
would have hit.

```sh
node e2e/run-browse-livetv.mjs
```

The runner carries live and scheduled events first (a board of settled games
cannot show a channel row beside a provider row), which is why the deployment's
`sports.leagues` list is the six the deployment gate also uses. `playwright.config.js`
runs one worker: both specs drive the same fixture provider, whose request
counters and rate-limit switch are what half the assertions read.

It needs Go, network access to `site.api.espn.com` and Node 20+, but no ffmpeg:
the fixture HLS is a real playlist with one short segment. Environment:
`WRENTV_BROWSE_DATA` (default `/tmp/wrentv-browse`), `WRENTV_BROWSE_PORT`
(default 8100), `WRENTV_BROWSE_USER` / `WRENTV_BROWSE_PASS` (default `gate` /
`gate-password`). The fixture's `/control/events` endpoint is what the runner
posts the day's event names to.

## The fixture

`fixture-server.mjs` is one dependency-free HTTP server playing three parts:

- a Stremio addon with two searchable tv catalogues whose stream entries ask for
  `Referer`, `Origin` and `User-Agent` and also offer a `Cookie` and an
  `X-Api-Key` (which must never be forwarded);
- a provider whose gated media path refuses anonymous requests, so a forwarded
  identity is observed rather than assumed, plus a broken path that fails after
  tuning;
- a Live TV playlist/guide served from files the runner generates: a channel
  named after each of the day's events, plus two standalone channels the track
  test uses — `Gate Audio Tracks` serves a master playlist with two audio
  renditions and one WebVTT subtitle rendition, `Gate Single Audio` serves a
  single-rendition stream. Both are deliberately not named after an event, so
  they never enter sports matching.

It records every media request (path and the headers it arrived with) at
`/_media-requests`, which the addon test reads. It is a test harness, not part of
the server or its image.

## Docker

The release gate requires the container image, with the image's own `/config`
volume and port:

```sh
docker build -t wrentv:gate .
docker run -d --name wrentv-gate -p 8099:32500 \
  -v "$PWD/e2e/gate-data:/config" wrentv:gate
docker exec wrentv-gate curl -fsS http://127.0.0.1:32500/api/health
```

`run-gate.mjs` always builds and starts its own local binary, so against the
image prepare its `/config` (fixture addon, Live TV source pointing at the
fixture provider, generated playlist/admin) and run the spec directly with
`WRENTV_BASE_URL=http://127.0.0.1:8099` as shown above. If Docker is unavailable,
run those commands in CI or another Docker-capable host; results must be recorded
there. A local-binary run is evidence, not that gate.

## Recorded run

2026-10-26 (Watch panel pass): `node e2e/run-browse-livetv.mjs` against the
built binary, real ESPN data (147 events across six leagues) and the fixture
addon — **7 passed, 0 skipped** (Chromium): the five above plus the two SportsHub
Watch panel cases (a Stremio row offering the same Preview and Watch controls as
a channel, each calling its own playback route with one session at a time, and
the preview's feed fallback, honest failure and rate-limit report).

2026-10-26: `node e2e/run-browse-livetv.mjs` against the built binary, real ESPN
data and the fixture addon — **5 passed, 0 skipped** (Chromium): the first-click
Stremio search and playback, the settled empty answer, the rate-limited provider
(one addon request, honest message with the provider's wait, no session left,
recovery through *Try again*), the conventional IPTV channel's row, guide and
session, and the two-event switching/session-release check.
The run is what verified the manifest-reuse, rate-limit and session-lifetime
changes end to end; its artifacts are under `/tmp/wrentv-browse/artifacts`.

2026-10-25 (final pass): `node e2e/run-gate.mjs` against the built binary, real
ESPN data, the fixture addon/provider, Chromium and real ffprobe — **10 passed,
0 skipped on the final run**. The two earlier runs in this pass failed at the new
track test until the client defect described below was fixed, so this was not an
unbroken streak. The playlist-channel quality check is measured separately
before the browser run (`640 × 360 · H264 · 25 fps`, recorded in
`quality-playlist.json`).

The runner previously emitted XMLTV stamps with an ISO `T`
(`20261025T035526 +0000`), which the server correctly rejects, so every programme
was dropped and the guide was empty (`guide=0` with `errors=0`); the stamp is
fixed and the server now reports `channels=304 guide=302` (the two standalone
Gate channels carry no guide data by design). The guide fix changed hub ordering
and exposed a Watch-panel race, which is also fixed: the panel painted *No Live
TV channel is a plausible match* while its search was still in flight, and it
now paints `Matching feeds…` until the request settles.

The run covers search, the paged expanded feed (12 cards max, page swap, lanes
restored), drive replay (provider coordinates, first-down line, possession,
previous eligible play, honest refusal), event tabs and session cleanup, addon
identity on every gated request including ffprobe, measured quality, docked
playback with the delayed HUD, full-screen handover with the sports identity,
source switching, a mandatory sanitized provider failure with session release,
live audio/subtitle track menus on a multi-rendition master and their absence on
a single-rendition channel, focus order/Escape/focus restoration, and the
stats-delay preference surviving a reload.

Nothing in the spec is conditional any more. `openPreviewableEvent` asks the
server which event has a matched feed and throws if none does — the gate's own
playlist guarantees one, so a miss is a deployment failure rather than a skip —
and the failing-provider test selects the broken addon by name, requires the
announced error, requires that it carries no URL, and polls the released
session.

The track test also caught a real defect: the live player chose native HLS
whenever `canPlayType` accepted the type, and Chromium accepts it while playing
through a pipeline that exposes no `audioTracks`, so a multi-audio stream played
with no menu at all. The player now uses the same Safari-only rule as the library
player and listens for hls.js's `AUDIO_TRACKS_UPDATED`/`SUBTITLE_TRACKS_UPDATED`
events, which is where alternate renditions actually arrive.

**Docker image run: still open** (no runtime on that host; re-probed and
recorded). Environment, output, the bug found and the screenshots are in
`docs/sports-hub/evidence/gate-run.md`; the blocked image attempt and the image
contract preflight are in `docs/sports-hub/evidence/docker-gate.md`.
