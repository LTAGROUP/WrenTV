# Playwright deployment gate — recorded run

**Date:** 2026-10-25 (final pass; earlier passes on 2026-10-24/25 are noted where
they matter)
**Build:** `go build -trimpath -ldflags "-s -w" ./cmd/wrentv` from this working
tree (same flags as the Dockerfile), run with `node e2e/run-gate.mjs`.
**Host:** Linux amd64, Go 1.27.3, Node 22.23.3, Playwright Chromium 1248
(headless), ffmpeg/ffprobe 5.1.9 installed for the run.
**Docker:** **not available** (no runtime, no socket, namespaces denied), so this
is a real server deployment of the built binary on `127.0.0.1:8099`, not the
container-image run the release gate requires. See
[docker-gate.md](docker-gate.md).
**Data:** real ESPN scoreboards (151 events in the last run), a generated Live TV
playlist of 302 channels named after those events plus two standalone channels
for the track test, a matching XMLTV guide with two programmes per sports channel
(one live, one four days out), a fixture provider that only serves media when the
request carries the referer and origin the addon asked for, and a generated
multi-rendition HLS master (two audio tracks, one WebVTT subtitle track). A real
local admin account signs in through the UI; no API is mocked.

Output (also in `playwright-run.txt`):

```
Running 10 tests using 1 worker

  ✓  1  search reaches channels, teams and events outside the loaded feed
  ✓  2  expanded feed mounts one page and swaps it
  ✓  3  drive replay draws provider coordinates and refuses what it cannot place
  ✓  4  event detail tabs render, and playback sessions are cleaned up
  ✓  5  addon playback: identity, quality, switching, failure and cleanup
  ✓  6  live player offers track menus only for streams that publish tracks
  ✓  7  a failing provider is reported without a URL and releases its session
  ✓  8  docked preview takes focus, closes on Escape and restores the launcher
  ✓  9  handing the preview to the full player never returns focus behind it
  ✓ 10  stats delay persists per device and withholds live scores while buffering

  10 passed (22.9s)
```

This is the **final** run, which used a populated guide
(`channels=304 guide=302`) and the multi-rendition fixture. Two earlier runs in
the same pass failed at test 6 — first because the assertion targeted the hidden
native `<select>` instead of the enhanced dropdown, then because the helper
returned the whole hls.js instance from `page.evaluate` (not serializable), then
because a single-rendition stream reports *zero* alternate renditions rather than
one. Those three were test defects; the product defect underneath them (bug 4)
was found with a direct probe between runs. The evidence directory is cleared at
the start of each run, so everything here is from the last one.

## What this run proves

- **Sign-in, navigation, search:** the Sports tab, server search over channels,
  EPG programmes, teams and events, and deep links to real identities.
- **Expanded feed (B16):** at most 12 `.sports-card` nodes in the expanded feed,
  `Next` changes the first card and stays within the bound, and `Back to lanes`
  removes the feed entirely (the page's cards are replaced, not hidden) while the
  lane rails render again.
- **Drive replay and perspective field (I4/I5):** a settled football game with
  provider drives shows replay rows, a projected field, at least one drawn
  `rush`/`pass` trace, a first-down line only where the provider supplied one and
  always inside the field, a named possession and a `Previous eligible play:`
  row, and every play either draws or states `No in-field trace for this play.` —
  no invented geometry. Invalid coordinates, penalties and unknown possession are
  refused; the model tests cover those cases directly.
- **Live track menus (I7):** the multi-rendition channel offers `Audio` with
  `English (en)` and `Spanish (es)` and `Subtitles` with `Subtitles off` and
  `English (en)`. Selecting through the real dropdown moves hls.js from
  `audioTrack 0` to `1` and `subtitleTrack` from `-1` to `0` and back, and the
  control shows the new label. The single-rendition channel plays through the
  same hls.js path, reports no alternate renditions, and shows neither control —
  so the hidden menu is a fact about the stream, not a missing player mode. This
  is the case a remuxed MPEG-TS channel is in as well (one AAC track, see
  `internal/livetv/remux.go`), which is why no menu is claimed for it.
- **Playlist-channel quality:** before the browser run the runner measures a real
  generated channel through `POST /api/sports/streams/quality` with ffprobe and
  records `width 640, height 360, h264, 25 fps, AAC mono, adaptive`
  (`quality-playlist.json`). This is a different code path from addon probing.
- **Addon identity:** the fixture addon asks for `Referer`, `Origin` and a
  `User-Agent`, and also offers a `Cookie` and an `X-Api-Key`. Every gated
  provider request carried the three allowlisted values — including the ffprobe
  ones — and none carried the credentials (`fixture-server.mjs` records the
  headers it received).
- **Measured addon quality:** `Check quality` returned a real ffprobe
  measurement (`640 × 360 · 25 fps · H264`, AAC mono) through the provider that
  refuses anonymous requests.
- **Playback:** the docked preview played the fixture HLS stream, showed the
  page's delayed scoreline in its HUD, and `/api/admin/livetv` reported exactly
  one session.
- **Sports player identity (I1):** `Full screen` plays through the ordinary
  Live TV player with the event's competition, lifecycle, matchup and broadcast
  feed; its back control is labelled `Back to event`, the lifecycle pill is a
  `role="status"` region (CONNECTING until the element is actually playing), and
  `Change source` is present only for a sports event.
- **Handover and cleanup:** switching to the second addon source replaces rather
  than stacks a session, closing the player releases it, and leaving the page
  releases the last one.
- **Provider failure (mandatory):** the broken addon is selected by name on an
  event the server reports as having feeds. Its post-tune 500 must produce the
  announced status (the test fails if it does not), the message must carry no
  URL, `/api/admin/livetv` must fall back to zero sessions, and the other addon
  choices must still be listed afterwards.
- **Guide-backed matching:** the generated channels are promoted by real
  now/next programmes; the first four events each resolve two strong groups
  (verified directly against `/api/sports/events/{id}/streams`).
- **Focus and stats delay:** focus order into and out of the docked preview,
  `Escape`, keyboard re-activation, no focus behind the full-screen player, and
  the stats-delay preference surviving a reload with the buffering copy.

## Bugs this gate caught

**1. An empty guide that looked like success.** The runner generated XMLTV
timestamps from `toISOString()`, which produces `20261025T035526 +0000`. XMLTV
wants `20261025035526 +0000`, and `parseXMLTVTime` correctly rejected the `T`
form, so every programme was dropped: `guide=0` for 300 channels, with
`errors=0`, so nothing looked wrong. EPG-based matching and the seven-day
horizon were therefore not exercised by any earlier gate run. Fixed in
`e2e/run-gate.mjs` (`stamp`); the guide now parses.

Fixing it exposed a second problem, which is the point of fixing fixtures rather
than tolerating them: the guide's now/next data changed hub ordering, and test 4
began failing on `expect.poll(sessions).toBe(1)`.

**2. "No match" shown while the match was still running.** The Watch panel's
first paint has an empty group list because the stream search is asynchronous,
and `render()` read that as "nothing matched" — so a viewer briefly saw *No Live
TV channel is a plausible match* before the feeds appeared. `sports-watch.js` now
paints `Matching feeds…` while `state.loading` and only claims no match once the
request has settled; the test waits for rows.

**3. A session leak and overwritten error on a mid-tune failure.** A provider
that failed *while its tune request was still in flight* had its error
overwritten by the success status line, and the session created by the late
response was never released. The preview now records the failure, refuses the
late session and releases it (`sports-watch.js`, `settleSession`/`failPreview`).

**4. Native HLS chosen in a browser that has no track APIs.** The live player and
the docked preview called `canPlayType('application/vnd.apple.mpegurl')` and
took the native path whenever it answered, but Chromium answers affirmatively
while playing through a pipeline that exposes no `audioTracks`/`textTracks`. The
multi-audio fixture therefore played with no menu at all — the feature was
unreachable, not merely untested. `live-model.nativeHlsSupported` now applies the
Safari-only rule the library player already used, both surfaces share it, and the
hls.js path listens for `AUDIO_TRACKS_UPDATED` / `SUBTITLE_TRACKS_UPDATED`
(alternate renditions arrive there, not on `MANIFEST_PARSED`, which is why the
first implementation saw the tracks in the instance and none in the menu).

## Screenshots

- `gate-addon-...addon-sources.png` — Watch panel with both Live TV groups and
  two addon sources; the first addon shows the measured quality.
- `gate-addon-...addon-preview.png` — docked preview playing the real HLS stream,
  HUD carrying the delayed scoreline.
- `gate-addon-...addon-failure.png` — the failing provider reported in the
  preview status with no URL and no session left behind.

The track menus were verified through DOM state and hls.js track indices rather
than a screenshot; the test asserts labels, selection and the applied index.

## What is still not covered

- **Docker image:** the container build/run was not executed; this host has no
  runtime and cannot start one. The release gate is therefore **not** satisfied
  by this run, even though the same server binary passes. The blocked attempt and
  the image-contract preflight are in [docker-gate.md](docker-gate.md) and
  [docker-image-contract.txt](docker-image-contract.txt).
- Native apps (P5/P6) remain out of scope until the container gate passes.
- The fixture media is a generated test pattern, not a provider stream; identity,
  measurement, track publication, session lifetime and failure behaviour are what
  it exercises, not a real broadcaster's CDN behaviour.
- The browser gate does not exercise picture-in-picture, multiview or recording:
  Wren TV has no such controls, and no decorative stand-ins were added.
- Subtitles are offered only where the stream publishes a subtitle rendition. The
  remux path cannot carry one (ffmpeg maps a single audio track and no text), so
  no control is claimed there rather than a disabled one.
