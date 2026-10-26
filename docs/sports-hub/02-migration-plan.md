# Sports Hub — migration plan

Target: the complete Sports Hub experience, on Wren TV's architecture, for every client Wren TV ships.
Phases are implementation milestones toward parity; no phase is a permanent reduction.

---

## 1. Definition of parity

Parity means every row of [01-feature-inventory.md](01-feature-inventory.md) has a Wren TV implementation with
the same user-visible behaviour, data semantics and failure handling. Concretely:

1. A user can open Sports, pick a date, filter by sport/league/status, search, pin sports, follow competitions,
   teams, athletes and schools, hide scores with spoilers, and theme the area.
2. Every supported competition renders correct cards for its event kind (matchup, fight card, tournament, race,
   stage), with scores, status, records, seeds/ranks, broadcast and venue.
3. Opening an event gives the same tabs and content MediaStorm gives, gated by the same capabilities; no
   fabricated values where the provider is missing data.
4. "Watch" resolves the event to Live TV channels with the same matching rules, confidence tiers, grouping,
   alternatives, quality ordering and search progress; failures are recoverable and never hide other feeds.
5. Admins can configure sports server-wide, manage team channels (primary/backup/suggestions/auto-link), inspect
   and edit per-user preferences, and clear the logo cache.
6. Scores survive provider failures and restarts; stale state is surfaced; refresh cadence matches.
7. The experience exists on web, iOS/iPad, macOS, Apple TV, Android phone/tablet, Android TV and Windows, using
   each app's existing navigation, focus and player conventions.
8. The verification rules in inventory §L are covered by tests and manual checks.

Acceptance gates per phase are listed in §12. A phase is not done until its gate's checks pass and the affected
clients build.

---

## 2. Target architecture

New server packages, following Wren TV's `internal/*` layout:

```
internal/sports/
  model.go             DTOs ported from models/sports.go + sports_detail.go
  catalog.go           LeagueCatalog (explicit + embedded coverage catalog JSON)
  catalog.json         port of services/sports/coverage_catalog.json
  registry.go          enabled leagues, catalog descriptors
  service.go           refresh loop, in-memory + disk cache, status, GetGame
  provider/
    client.go          bounded HTTP cache, singleflight, per-host slots (port provider_http.go)
    espn.go            site/core API decode + normalization
    thesportsdb.go     boxing day schedule
    cfl.go             CFL scoreboards/standings
    f1archive.go       official archive client
    organizer.go       ASO/RCS/Giro HTML guide parsing
  espn_scoreboard.go   per-league fetch, date overlap, coverage merge
  dates.go             provider-date queries + dated cache
  detail.go            enrichment orchestration + per-event cache
  team_sports.go       NFL/NBA/MLB/NHL/soccer/tennis/rugby detail
  baseball.go, basketball.go, football.go, hockey.go, soccer.go
  golf.go, cricket.go, combat.go, tennis.go, rugby.go
  racing.go, motogp.go, f1_archive.go, circuits.go
  cycling.go, cycling_calendar.go, cycling_guides.go
  standings.go, pregame.go, players.go
  teams.go             team catalog sync
  logo.go              provider logo cache
internal/sports/match/
  matcher.go           team/broadcast/EPG token matcher (port sports_matcher.go)
  quality.go           label quality parser (port sports_stream_quality.go)
  groups.go            stream grouping + ordering (port models + sorting)
internal/sports/links/
  links.go             team channel links repository + suggestions + auto-link
internal/db/
  sports.go            teams, links, preferences queries
internal/api/
  handlers_sports.go   read API
  handlers_sports_admin.go  settings, refresh, logo cache, per-user preferences
  handlers_sports_streams.go streams, live search, quality probe
  handlers_sports_teams.go   teams + links + auto-link
```

Web client additions (embedded, no build step):

```
internal/web/static/
  sports.css                    tokens, six palettes, layouts, charts
  js/sports.js                  routes and page controllers
  js/sports-api.js              endpoint wrappers
  js/sports-cards.js            cards and sport-specific visuals
  js/sports-detail.js           detail tabs and sections
  js/sports-streams.js          picker, quality probe, search progress
  js/sports-charts.js           SVG helpers
  js/sports-prefs.js            preferences, pins, follows, spoilers, themes
  js/sports-admin.js            Manage → Sports and Team Channels
  js/live.js                    Live TV page (prerequisite)
  js/live-player.js             live playback surface (prerequisite)
```

Native apps (one feature slice per app):

```
apps/ios/Wren Tv/Model/Sports.swift
apps/ios/Wren Tv/Views/SportsHubView.swift, SportsEventView.swift, SportsRaceView.swift, SportsComponents.swift
apps/macos/Sources/Model/Sports.swift
apps/macos/Sources/Views/SportsHubView.swift, SportsEventView.swift, SportsComponents.swift
apps/tvos/WrenTV/Model/Sports.swift
apps/tvos/WrenTV/Views/SportsHubView.swift, SportsEventView.swift, SportsRaceView.swift
apps/android/core/src/main/java/com/wrentv/tv/data/SportsModels.kt
apps/android/phone/src/main/java/com/wrentv/phone/ui/SportsScreen.kt, SportsDetailScreen.kt
apps/android/tv/src/main/java/com/wrentv/tv/ui/screens/SportsScreen.kt, SportsDetailScreen.kt
apps/windows/WrenTV/Core/SportsModels.cs
apps/windows/WrenTV/UI/SportsPage.cs, SportsDetailPage.cs, TeamChannelsPage.cs
```

No new runtime dependencies on the server. The matcher needs Unicode folding for provider name normalization;
either add `golang.org/x/text/unicode/norm` (small, standard) or implement a local fold. Decide in P0 and record it.

---

## 3. HTTP API

Authenticated group in `internal/api/server.go`, matching Wren TV's existing JSON conventions. Parity routes are
grouped under `/api/sports`; admin mutations sit behind `adminOnly`; Live TV stays under `/api/livetv`.

### 3.1 Read API (any signed-in user)

| Method | Path | Purpose | MediaStorm equivalent |
|---|---|---|---|
| GET | `/api/sports/status` | enabled, dateQueries, lastRefresh, lastError, gameCount, refreshing | `/sports/status` |
| GET | `/api/sports/leagues` | catalog with enabled flags | `/sports/leagues` |
| GET | `/api/sports/scoreboard?date=&league=` | cached scoreboard, per-league freshness | `/sports/scoreboard` |
| GET | `/api/sports/hub?date=` | event list plus special tournaments (clients build lanes) | `/sports/hub` |
| GET | `/api/sports/events/{id}` | event detail with capabilities | `/sports/game/{id}` |
| GET | `/api/sports/events/{id}/streams?scope=&kind=&parentId=&broadcast=` | flat matches + groups + source statuses | `/sports/game/{id}/streams` |
| GET | `/api/sports/search?q=` | channels, live/upcoming programmes, teams, events | `/live/search` |
| GET | `/api/sports/standings/{league}` | league table groups | `/sports/standings/{league}` |
| GET | `/api/sports/races` | racing board (series, races, sessions) | `/sports/races` |
| GET | `/api/sports/races/{league}/{event}/{session}` | race session detail | `/sports/races/{league}/{event}/{session}` |
| GET | `/api/sports/f1/archive/{event}/{session}` | archived F1 analysis | `/sports/f1/archive/…` |
| GET | `/api/sports/motogp/standings` | MotoGP standings | `/sports/motogp/standings` |
| GET | `/api/sports/motogp/circuit/{event}` | circuit geometry | `/sports/motogp/circuit/{event}` |
| GET | `/api/sports/cycling` | cycling calendar and current race | `/sports/cycling` |
| GET | `/api/sports/cycling/{race}/{year}/{stage}` | stage detail | `/sports/cycling/…` |
| GET | `/api/sports/cricket/series` | discoverable cricket series | `/sports/cricket/series` |
| GET | `/api/sports/logo?url=` | cached provider artwork | `/sports/logo` |
| GET | `/api/sports/preferences` | caller's revisioned document | `/users/{id}/sports/preferences` (adapted) |
| PUT | `/api/sports/preferences` | optimistic-concurrency save | same |
| GET | `/api/sports/teams?league=` | teams with link counts | `/sports/teams` |
| GET | `/api/sports/teams/linked` | which teams have links | `/sports/teams/linked-channels` |
| GET | `/api/sports/teams/{id}/links` | links with `resolved` | `/sports/teams/{id}/links` |
| GET | `/api/sports/teams/{id}/suggestions` | ranked channel candidates | `/sports/teams/{id}/channel-suggestions` |
| GET | `/api/sports/leagues/{league}/auto-link` | preview auto-link results | `/sports/leagues/{id}/auto-link/preview` |
| POST | `/api/livetv/quality` | probe a channel/stream for resolution/audio | `/video/live/quality` |

### 3.2 Admin API

| Method | Path | Purpose |
|---|---|---|
| GET | `/api/admin/sports` | server-wide sports settings (normalized) |
| PUT | `/api/admin/sports` | validate and save settings, re-poll immediately |
| POST | `/api/admin/sports/refresh` | force refresh |
| POST | `/api/admin/sports/logo-cache/clear` | clear logo cache |
| GET | `/api/admin/sports/users/{id}/preferences` | inspect a user's document |
| PUT | `/api/admin/sports/users/{id}/preferences` | admin edit (revision-checked) |
| PUT | `/api/sports/teams/{id}/links/primary` | set primary link |
| POST | `/api/sports/teams/{id}/links/backup` | add backup |
| PUT | `/api/sports/teams/{id}/links/order` | reorder backups |
| DELETE | `/api/sports/teams/{id}/links/{linkId}` | delete link |
| POST | `/api/sports/leagues/{league}/auto-link` | apply eligible auto-links |

Behavioural requirements carried over: strict JSON decoding on settings, unknown leagues dropped, empty input
selection rejected, `409` on stale preference revision, admin-only mutations, `Cache-Control: no-store` on
preferences, `OPTIONS` not required (Wren TV's web client is same-origin), CORS already handled by the router.

---

## 4. Database

Append migrations to `internal/db/db.go` (never edit applied ones). SQLite types, Unix seconds like the rest of
the schema. Tables land in the phase that uses them: `sports_teams` in P0, the link and preference tables in P2.

```sql
-- sports team identities
CREATE TABLE sports_teams (
  id                TEXT PRIMARY KEY,           -- '{league}:{providerTeamId}'
  league            TEXT NOT NULL,
  provider_team_id  TEXT NOT NULL,
  name              TEXT NOT NULL,
  location          TEXT NOT NULL DEFAULT '',
  nickname          TEXT NOT NULL DEFAULT '',
  abbreviation      TEXT NOT NULL DEFAULT '',
  logo_url          TEXT NOT NULL DEFAULT '',
  updated_at        INTEGER NOT NULL,
  UNIQUE (league, provider_team_id)
);

-- permanently linked live channels (denormalized snapshot, resolved at read time)
CREATE TABLE sports_team_channel_links (
  id               TEXT PRIMARY KEY,
  team_id          TEXT NOT NULL REFERENCES sports_teams(id) ON DELETE CASCADE,
  slot             TEXT NOT NULL CHECK (slot IN ('primary','backup')),
  position         INTEGER NOT NULL DEFAULT 0,
  channel_tvg_id   TEXT NOT NULL DEFAULT '',
  channel_name     TEXT NOT NULL,
  channel_url      TEXT NOT NULL,
  channel_logo     TEXT NOT NULL DEFAULT '',
  source_id        TEXT NOT NULL DEFAULT '',
  source_name      TEXT NOT NULL DEFAULT '',
  auto_linked      INTEGER NOT NULL DEFAULT 0,
  link_confidence  REAL NOT NULL DEFAULT 0,
  match_reason     TEXT NOT NULL DEFAULT '',
  last_verified_at INTEGER,
  created_at       INTEGER NOT NULL,
  updated_at       INTEGER NOT NULL,
  UNIQUE (team_id, slot, position)
);
CREATE INDEX idx_sports_team_links_team ON sports_team_channel_links(team_id);
CREATE INDEX idx_sports_team_links_tvg ON sports_team_channel_links(channel_tvg_id)
  WHERE channel_tvg_id <> '';

-- per-user preferences with revision (Wren TV has users, not profiles)
CREATE TABLE sports_preferences (
  user_id    TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
  revision   INTEGER NOT NULL DEFAULT 0,
  document   TEXT NOT NULL,
  updated_at INTEGER NOT NULL
);
```

Notes:

- Scoreboard caches stay off the database: `DataDir/cache/sports/scoreboard.json` plus in-memory maps, matching
  MediaStorm's restart behaviour and keeping SQLite writes short.
- Team link channel identity is intentionally denormalized: Wren TV channel IDs regenerate on playlist refresh
  (`internal/livetv/playlist.go`), so links resolve by `channel_tvg_id`/`channel_name` against the current list
  and report `resolved` instead of hard-failing.
- Preferences document is the MediaStorm v1 JSON (`pinnedSportIds`, `competitionIds`, `teamIds`, `athleteIds`,
  `schools`) so the parser and semantics port directly.
- Wren TV's Supabase mode uses the same local DB for server state; no change needed.

Config (`internal/config/config.go`, YAML, env override):

```yaml
sports:
  enabled: true
  leagues: ["nfl", "nba", "mlb", "nhl"]   # empty + enabled true means "all catalog leagues"
  favorite_teams: []
  sources: []          # Live TV source IDs limiting automatic discovery; empty = all
  groups: []           # channel groups limiting discovery; empty = all
  overrides:           # per-league discovery scope
    nfl: { sources: ["src1"], groups: ["Sports"] }
```

`SportsConfig.Normalize()` mirrors `SportsSettings.Normalize()` (nil → defaults, empty lists preserved), and
`PATCH /api/admin/settings` gains a `sports` block alongside `liveTv`. Changing leagues re-registers the
registry and triggers a refresh without a restart.

---

## 5. Ingestion

### 5.1 Port map

| MediaStorm file(s) | Wren TV target | Notes |
|---|---|---|
| `services/sports/service.go` | `internal/sports/service.go` | Refresh concurrency cap 4, seed-with-last-known, disk save, status, GetGame, logo cache |
| `services/sports/espn.go`, `extended_scoreboard.go` | `internal/sports/espn_scoreboard.go` | Scoreboard/summary/teams decode, coverage merge |
| `services/sports/dates.go` | `internal/sports/dates.go` | Provider date queries + overlap + dated cache |
| `services/sports/detail.go` + per-sport files | `internal/sports/detail.go` + sport files | Same capabilities gating and caches |
| `services/sports/standings.go`, `team_detail.go`, `team_context.go` | `internal/sports/standings.go`, `teams.go` | Record enrichment and team catalog |
| `services/sports/racing.go`, `motogp*.go`, `f1_archive.go`, `circuits.go` | `internal/sports/racing.go`, `motogp.go`, `f1_archive.go`, `circuits.go` | Keep archive labels and attribution |
| `services/sports/cycling*.go`, `aso_guides.go`, `giro.go` | `internal/sports/cycling*.go`, `organizer.go` | HTML parsing isolated behind one client |
| `services/sports/cricket*.go`, `boxing.go`, `mma_detail.go`, `golf`, `tennis`, `rugby` | matching sport files | Carry coverage limits verbatim |
| `services/sports/coverage_catalog.go` + `.json` | `internal/sports/catalog.go` + `catalog.json` | Embedded catalog, same validation panics |
| `services/sports/provider_http.go` | `internal/sports/provider/client.go` | Bounded body, singleflight, host slots, TTL, retry-after |
| `handlers/sports*.go` | `internal/api/handlers_sports*.go` | Split read/admin/streams/teams |
| `handlers/sports_matcher.go` | `internal/sports/match/matcher.go` | Adapt `LiveChannel` to `livetv.Channel` |
| `handlers/sports_stream_quality.go` | `internal/sports/match/quality.go` | Label-only quality, no overclaiming |
| `handlers/sports_links.go` | `internal/sports/links/links.go` + DB queries | Auto-link eligibility and provenance |
| `config/settings.go` `SportsSettings` | `internal/config/config.go` | YAML instead of JSON settings file |
| `models/sports.go`, `sports_detail.go` | `internal/sports/model.go` | Keep JSON field names |

### 5.2 Refresh and failure semantics

- 90-second background cycle: scoreboards → team catalog → team link sync → link catalog prune, re-reading
  config between stages and stopping cleanly when disabled (port of `sports_background.go`).
- One in-flight refresh at a time; concurrent requests return the cached snapshot (`refreshing` flag in status).
- Start each refresh from the previous games per league; on error keep last known data and set `lastError`.
  Partial coverage merges rather than erases.
- Per-league `stale`/`unavailable` computed from last success age and error, exposed in the scoreboard payload.
- Provider HTTP cache is shared across all leagues; `Retry-After` honoured; body cap 32 MB.
- Logo fetch only for `https` URLs on allowed provider hosts; served through the image cache with a disk path.

### 5.3 Catalog and coverage limits

The ported catalog carries the same `implementationStatus` (`validated`/`limited`) and `coverageNote` so clients
hide unverified modules. Specific limits stay documented and enforced: the documented cricket coverage (IPL
first, later verified competitions), PGA-only golf, TheSportsDB's limited boxing schedule, archive-only F1
telemetry, rugby union enrichment only.

---

## 6. Live stream matching

1. Build `sportsIdentity` from the event's teams, broadcasts and league, using league-scoped aliases.
2. Fetch channels through `livetv.Service.Channels()`; when discovery scope is configured, filter by Live TV
   source and group (equivalent of `FetchFilteredChannelsForRequest`).
3. Score candidates with the ported matcher: normalized tokens, noise and negative tokens, team location/nickname/
   abbreviation, matchup phrases, broadcast/network names, EPG title/description; EPG now/next can promote but
   never fabricate.
4. Confidence tiers: `strong ≥ 0.85`, `possible ≥ 0.65`, below that only for explicit manual searches. Reject
   channels with non-live labels (archive, replay, VOD) and ended lifecycles.
5. Group equivalent matches; preserve every distinct source URL as an alternative; order by live/unknown/upcoming
   and parsed reported quality (lower claim wins on conflict).
6. Return `{streams, groups, sources}`. The client decides: strong single → play; otherwise chooser; weak only →
   unavailable screen with search.
7. Quality probe: `POST /api/livetv/quality` starts a short-lived ffprobe of the resolved HLS/TS URL, one at a
   time per client, returns `{height, bitrate, audioTracks}`; cancellation leaves completed results.
8. Addon expansion: when a matched channel is a Stremio addon request (Wren TV `livetv` stremio source or
   `streaming` addon), resolve stream URLs in bounded batches (4 concurrent, 30 s budget), emit progress and
   per-source `complete`/`unavailable`/`rate-limited`/`authentication-required`, keep partial results.
9. Server cache: 15 s per `(event, scope, profile)`, max 32 entries.

Integration with playback: watch actions use the existing `POST /api/livetv/channels/{id}/play` (HLS flag where
needed) and `/live/{sid}` session URLs, so no parallel playback stack is introduced.

---

## 7. Team channels

- Team catalog sync writes `sports_teams` with change detection (skip unchanged rows).
- Link CRUD writes the denormalized snapshot; uniqueness `(team, slot, position)` enforced with a transaction for
  reorder and primary promotion.
- Suggestions reuse the matcher with team identity only; manual search may return candidates below the automatic
  floor, marked accordingly.
- Auto-link preview returns candidate, runner-up confidence, gap and eligibility/skip reason; apply inserts only
  eligible links, records `auto_linked`, confidence and reason.
- Reads resolve against the current playlist by normalized tvg-id then normalized name; `resolved=false` links
  stay visible but are not offered as automatic watch targets.

---

## 8. Preferences, settings and admin

Server-wide (config + `/api/admin/sports`):

- enabled switch, enabled league list, favorite team IDs, discovery scope defaults, per-league overrides.
- Strict decode, normalization, unknown league filtering, rejection of a non-empty all-unknown league list.
- Saving re-registers enabled leagues and starts a refresh; disabling stops polling and clears nothing.

Per user (`/api/sports/preferences`):

- `{revision, preferences}` snapshot, strict parser (same rules: version 1, dedupe, unknown IDs survive, unknown
  fields rejected, school scope merge).
- Update requires the caller's current revision; `409` otherwise; response increments revision.
- UI surfaces: pins order (20 supported sport IDs), competition/team/athlete follows, school follows, and the
  separate device-local spoiler toggle.

Admin UI (web, Manage):

- Sports settings tab: enable, league checklist with coverage notes and implementation status, discovery scope,
  favorites, logo cache clear, force refresh, status/health.
- Team Channels page: league picker, team list with link state, primary/backup editing, suggestions with
  confidence, auto-link preview/apply.
- Users page: open a user's sports preferences (read/edit) where Wren TV's user editor already lives.

Native apps: preferences remain per user; spoilers device-local; theme choice device-local unless the user asks
for cross-device sync (not in MediaStorm either).

---

## 9. Web client

### 9.1 Prerequisite: Live TV

Wren TV's web client has no Live TV page even though the API and native apps do. "Watch" needs a destination:

- `/live`: channel list by group, now/next, guide window, search, hidden-group behaviour.
- `live.js`: uses `/api/livetv/channels`, `/api/livetv/guide`, `POST /api/livetv/channels/{id}/play`.
- `live-player.js`: full-screen live playback (HLS via `hls.min.js`, native HLS on Safari), channel switching,
  no seek/progress, reuse of existing overlay/controls styling.
- Sidebar entry shown when `server.liveTv` is true, matching native apps.

### 9.2 Sports

Routes in `app.js` (registered like existing entries):

```
/sports                        → sports.viewHub
/sports/event/:id              → sports.viewEvent        (?watch=1 to auto-open picker)
/sports/race/:id               → sports.viewRace
/sports/cycling/:id[/:stage]   → sports.viewCycling
/sports/teams                  → sportsAdmin.viewTeamChannels (admin)
/manage/sports                 → sportsAdmin.viewSettings (admin)
```

Hub composition (parity with `SportsHubTV` / `SportsMobileHub`):

- Date rail with capability gate; status tabs; sport and league filters via the existing modal helper.
- Pinned sports first, then followed competitions/teams, then remaining lanes.
- Featured event; lane sections with horizontal card rails; expanded feed ("See all") paginates 12 cards with
  previous/next and keeps scroll position across date/filter changes.
- Card variants: dense/rail/expanded/featured; sport-specific visuals (diamond, court, pitch, rink, race table).
- Freshness line, stale badges, per-league unavailable state, retry.
- Spoiler toggle in the header; hiding applies to cards and featured; detail entry reveals.
- Theme menu with the six palettes applied through CSS custom properties scoped to `.sports`.
- Keyboard/D-pad: roving tabindex on rails, arrow keys move focus, Enter selects exactly once, Escape closes the
  top layer; visible focus ring; skip-link for the lane list.
- Accessibility: `aria-label` built from the same composition as `buildEventAccessibilityLabel`
  (teams/scores/status/result/watch availability).

Event page:

- Sticky score header with sport-aware status text.
- Tabs: Overview, Box score, Play-by-play, Lineups, Standings, shown per `capabilities`.
- Sport modules: football drives + field map; basketball shot chart + win probability; baseball diamond +
  linescore; hockey shot map; soccer pitch + lineups; tennis sets; rugby timeline; cricket scorecards; golf
  leaderboard; combat bouts + fighter stats; racing classification; cycling stage tables.
- Pregame panel before start; score history/lead chart for finals; delayed stats control for live playback.
- Watch button opens the stream picker; direct play when the decision is unambiguous.
- Detail refresh: 120 s live / 600 s otherwise, independent of hub; identity guard prevents stale renders.

Stream picker:

- Groups with alternatives, confidence label and match reason, broadcast/programme label, quality chips.
- Search-all-sources action with progress (`checked/total/failed/paused`), cancel keeps results.
- Probe action with per-feed status; sort by resolution after/skip; re-check.
- Failure of one feed returns to the list; last source remembered; explicit unavailable state with search.

Charts: hand-authored SVG (no chart dependency) matching the existing `styles`/`stats.js` approach.

Styles: `sports.css` loaded after `app.css`; uses Wren tokens by default and the six palettes as
`sports[data-appearance]` variable sets; responsive breakpoints shared with the shell.

### 9.3 Admin pages

- `sports-admin.js` renders settings and team-channel management using existing `manage.js` patterns
  (`modal`, tabs, async forms, toasts).
- Server status card shows enabled, catalog size, last refresh, last error, refresh now.

---

## 10. Native apps

Shared contract: the same `/api/sports/*` endpoints and JSON field names; each app adds a Sports destination,
models, API methods, views and a spoiler/theme preference store. No app gains a second networking stack.

### 10.1 iOS / iPadOS (SwiftUI)

- `Model/Sports.swift`: `SportsStatus`, `SportsLeague`, `SportsEventSummary`, `SportsEventDetail`, `SportsStreamGroup`,
  `SportsPreferences`, racing/cycling/standings types (Codable, tolerant decoding of optional fields).
- `WrenTVAPI`: `sportsStatus`, `sportsLeagues`, `sportsScoreboard(date:)`, `sportsEvent(id:)`,
  `sportsStreams(eventId:scope:)`, `sportsSearch`, `sportsPreferences` get/put, teams/links.
- Placement: a Sports tab when `server.sports` is enabled (new flag on `/api/server`, which already exposes
  `liveTv`), next to Live TV, matching MediaStorm's drawer ordering (Home, Sports, …).
- Views: `SportsHubView` (date rail, filters sheet, lanes, pinned section), `SportsEventView` (tabs, sticky
  header, pregame, stats), `SportsRaceView`, `SportsCyclingView`, `SportsStreamPicker`, `SportsComponents`
  (cards, badges, diamond/court/pitch glyphs), `SportsAdminView` for admins.
- Playback: reuse the existing live player view/controller; docked preview where the app already supports
  picture-in-page, otherwise full-screen with return-to-event.
- Device-local spoiler toggle (`@AppStorage`), appearance choice, freshness and refresh timers driven by
  `scenePhase`.

### 10.2 macOS

- Same model/API files (shared target membership where the project already shares files), Sports item in the
  sidebar enum (`Sources/Views/MainView.swift`) after Live TV, hub/detail views in `Sources/Views`.
- Admin: Sports settings and Team Channels pages under the existing manage section.

### 10.3 tvOS

- `Tab("Sports", …)` in `MainView.swift` (conditional on sports enabled).
- Focus-driven hub: one focusable row per league, horizontal card rails, featured card as default focus, bounded
  feed pagination, no nested focus traps.
- Event page with docked player and tabs; D-pad back returns to the same card.
- Spoiler toggle and appearance in the Sports settings sheet.

### 10.4 Android (phone + TV)

- `core/data/SportsModels.kt` + `WrenTVApi` methods; sports-enabled flag in `AppState`.
- Phone: `Route.Sports`, `Route.SportsEvent`, `Route.SportsRace`, `Route.SportsCycling` in `ui/Navigation.kt`;
  `Tab` gains Sports (or a Home entry when tabs are full); screens use `LazyColumn` with 12-card pages, filter
  bottom sheets, pull-to-refresh, `LocalAppState`.
- TV: `SportsScreen`, `SportsDetailScreen` with `TvLazyRow`/`LazyColumn` focus handling, matching existing live
  TV conventions.
- Player: reuse `LivePlayerScreen`; sports HUD adds broadcast/match identity only.
- Spoiler toggle in DataStore/preferences (same storage as other local prefs).

### 10.5 Windows (WinUI)

- `Core/SportsModels.cs` + `WrenTVApi` methods.
- `UI/SportsPage.cs` hub, `UI/SportsDetailPage.cs` tabs, `UI/TeamChannelsPage.cs` admin; navigation item in
  `MainWindow.BuildNav()` next to Live TV; cards in `UI/Cards.cs` style.
- Live playback through the existing `LivePlayerWindow`; spoiler toggle in `SettingsPage`/local settings.

---

## 11. Testing and verification

### 11.1 Go

- Port behaviour tests with their fixtures: scoreboard normalization (all sports), date overlap, coverage merge,
  partial failure retention, status, detail capabilities, standings, racing/cycling/combat/cricket/golf/tennis,
  matcher table tests, quality parser, grouping/order, settings validation, preference revision conflicts, link
  resolution, auto-link eligibility, catalog validation panics.
- Provider clients tested with `httptest` and recorded JSON (no live network in CI). HTML guide parsing uses
  stored pages; failures must degrade, not panic.
- Handler tests via `httptest` + chi router: auth, admin-only writes, strict decode, 404 for unknown event,
  409 for stale preference revision.
- Cache tests: restart restores snapshot; failed refresh keeps last known data.
- Contract tests: golden JSON for `/api/sports/leagues`, scoreboard, event detail, streams and preferences, so
  native clients can rely on field names.

### 11.2 Web

Wren TV has no JS test runner today. Two layers:

1. Go tests assert embedded assets, route registration, and that the Sports page is served for deep links.
2. Add a dev-only Playwright suite (new dev dependency, not shipped) covering: hub render from a stubbed API,
   date/status filters, pinned order, spoiler toggle, event tabs by capability, stream picker direct/choose/
   unavailable, quality progress cancellation, keyboard navigation order and single-select.

If adding a JS toolchain is unwanted, the fallback is a fixture page (`/sports?fixture=1` guarded to
`WRENTV_DEV`) plus the checklist-driven manual pass; the trade-off is weaker regression protection.

### 11.3 Native

- Swift: decoding/formatting unit tests (status strings, score/result copy, capabilities), snapshot-free view
  model tests; focus behaviour checked on device/simulator.
- Kotlin: model decode and formatter unit tests; Compose UI tests for filter sheet and pager bounds.
- Windows: model decode tests where the project has a test project; otherwise manual checklist.

### 11.4 Parity audit

A checklist generated from inventory §A–§L, marked per platform. Parity is declared only when every row is
implemented and verified, with the known provider limits documented rather than hidden.

---

## 12. Phases

Each phase ends with: `go build ./...`, `go test ./...`, affected native builds, web checks, and the gate below.
Phases are ordered by dependency, not by permanent scope cuts.

### P0 — Foundations and a real hub

Server: `internal/sports` model/catalog/provider/service/ESPN scoreboard/status/refresh/logo; migrations for
`sports_teams`; API status, leagues, scoreboard, hub, logo; config block; admin settings read/write; background
cycle. Web: Live TV page + live player, `/sports` hub with dates, filters, lanes, cards, featured, freshness,
refresh, empty/error states, spoiler toggle.

Gate: US majors + soccer show real fixtures; a live event opens a matched channel and plays; restart keeps
scores; provider failure keeps last-known data; catalog/status contract tests pass.

Inventory coverage: A1–A2, B1–B6, B10, B13–B17, C1–C3, C5–C6, C9–C12, D1, H1 (API), J1–J5.

### P1 — Team-sport event parity and watch flow

Server: detail enrichment for NFL/NBA/MLB/NHL/soccer, standings, pregame, player stats, win probability, shots,
drives, score history, capabilities; streams endpoint with matcher, EPG promotion, grouping, ordering, quality
labels, 15 s cache; live search; quality probe. Web: event page with tabs, sticky header, sport modules, stream
picker with alternatives/progress/quality/unavailable, deep links, identity-guarded refresh, docked preview
where supported.

Gate: side-by-side acceptance against MediaStorm for one live and one final game per team sport; matcher table
tests pass; a failed feed leaves other feeds selectable; capability gaps hide tabs (no empty tabs).

Inventory coverage: A3, A7, C7, D2–D9, E1–E10, F1–F11, F13–F14, J2–J6.

### P2 — Personalization, teams and admin

Server: preferences table + revisioned API, pins/follows parsing; team catalog sync; links CRUD, suggestions,
auto-link preview/apply, resolution; admin user preference endpoints. Web: preferences editor, pins ordering,
follows, theme/readability, admin Sports settings, Team Channels page.

Gate: revision conflict returns 409; auto-link never inserts ineligible teams; links survive playlist churn and
report resolved correctly; admin-only mutations enforced.

Inventory coverage: B7–B9, B11–B12, C8, G1–G8, H1–H5, J7–J8.

### P3 — Extended sports parity

Server: golf, cricket (incl. series discovery), tennis, rugby union/league, combat, racing boards/sessions,
MotoGP standings/circuits, F1 archive, cycling calendar/stages/guides, cricket scorecards. Web: dedicated race
and cycling pages, sport-specific cards and detail modules.

Gate: coverage limits match `SPORTS_DETAIL_COVERAGE.md`; fixtures for scheduled/live/final per sport; archive
data labelled; zero-vs-missing tests.

Inventory coverage: A4–A5, D10–D20, J9–J10.

### P4 — Live dashboard and playback parity

Server: nothing new beyond session reuse; client: docked live player, preview focus, stats delay and persisted
preference, drive replay, perspective field, sports HUD identity, orientation behaviour, multiscreen placement
where the platform supports it.

Gate: live game flow matches MediaStorm on phone/TV/desktop (start, dock, expand, back, retune, stop); stats delay
persists; replay scroll and back behaviour verified.

Inventory coverage: B18, E11–E13, I1–I7. Recording (F12) ships only if Wren TV gains recording; until then the
control is absent rather than simulated.

### P5 — Apple apps

iOS/iPadOS, macOS and tvOS implement every implemented server feature with the native conventions above.

Gate: all three build; Sports tab hidden when disabled; hub/detail/watch/preferences/admin work against a real
server; tvOS focus audit; VoiceOver labels.

### P6 — Android and Windows

Phone, Android TV and WinUI apps.

Gate: both build; navigation placement; live playback; paging bounds; admin pages where the platform has admin UI.

### P7 — Hardening and parity audit

Performance (bounded DOM/lazy lists, image caching, no per-card requests), accessibility, long-text layouts,
timezone edge cases, provider degradation, documentation of limits, full checklist marked, release notes.

Gate: every inventory row verified on every supported client, with documented provider limits.

---

## 13. Risks and decisions

| Risk / decision | Impact | Mitigation |
|---|---|---|
| MediaStorm UI is React Native; Wren TV web is vanilla JS | No copy-paste; large rewrite | Port behaviour and contracts, component-by-component; keep shared Go DTOs |
| Wren TV has no profiles | Per-profile preferences become per-user | Key preferences to `users.id`; note the difference in UI copy |
| Wren TV web has no Live TV page | Watch flow has no destination on web | P0 ships Live TV page + live player first |
| MediaStorm recording control depends on DVR | Cannot honestly show it if Wren TV lacks recording | Omit until server support; track as parity gap, not a fake button |
| ESPN public API is undocumented | Shape changes break ingestion | Isolated decoder, fixtures, tolerant parsing, last-known-data retention, per-league errors |
| Organiser HTML (ASO/Giro) is fragile | Cycling guides can break | Single adapter, stored fixtures, degradation to stage data without guides |
| TheSportsDB free tier limits boxing | Limited coverage | Keep `implementationStatus: limited` and the existing coverage note |
| No live F1 telemetry source | Cannot claim live telemetry | Archive-only F1 as in MediaStorm |
| SQLite single writer | Link/preference writes could contend | Short transactions, WAL (already), no scoreboard writes to SQLite |
| Team logos / circuit geometry licensing | Redistribution limits | Optional provider logos with fallbacks, attribution for geometry as in source |
| Web test tooling | Weak regression protection if omitted | Prefer a dev-only Playwright suite; otherwise dev fixture mode plus checklist |
| Native effort is per-platform | Long tail after web parity | Phases P5/P6 after server/web parity, same contract tests guard drift |

## 14. Open questions

1. Should `sports` be a top-level tab for every user, or hidden until an admin enables it? (MediaStorm enables by
   default with all leagues.) Plan assumes enabled by default, admin-switchable.
2. Should the six MediaStorm appearances replace Wren TV's palette inside Sports, or be additive presets? Plan
   assumes additive presets scoped to Sports.
3. Is a web Live TV page acceptable as a prerequisite, or should Sports ship web-only with external-app handoff
   first? Plan assumes the page ships in P0.
4. For Supabase-mode servers, should sports settings be owner-only (as MediaStorm's master-only rule) or any
   admin? Plan assumes Wren TV's existing `adminOnly` rule.
5. Is adding a dev-only Playwright suite acceptable? Plan recommends it; the alternative is documented above.
