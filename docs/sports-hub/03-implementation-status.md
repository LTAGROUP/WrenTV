# Sports Hub — implementation status

Tracks every feature in [01-feature-inventory.md](01-feature-inventory.md) against
what has actually shipped and been tested. Updated at the end of each milestone.

Milestones complete: **P0 — Foundations and a real hub**, **P1 — Detail, matching,
quality and docked playback**, **P2 — Personalization, team channels, administration
and appearance** (see §12 of the migration plan). P0/P1 were verified against a
running server, the live ESPN provider and a fixture Live TV source; P2 was
verified with the Go/Node suites and a fresh binary against that fixture server.
**P4 is complete for web** and verified by a ten-test real-browser gate against
the built binary, most recently against a populated XMLTV guide. **The Docker image gate is still blocked**
(no container runtime on the verification host; see §3 and
`evidence/docker-gate.md`), so P5/P6 remain unstarted.

Status key: **Done** (implemented and tested), **Partial** (shipped for web,
native or a subset pending), **Next** (belongs to a later milestone), **N/A**
(cannot exist without a prerequisite elsewhere in Wren TV).

---

## 1. What shipped

### Server

| Area | Files | Notes |
|---|---|---|
| Contracts | `internal/sports/model.go` | MediaStorm game/league/event/detail/pregame/stream DTOs with unchanged JSON field names |
| Catalog | `internal/sports/catalog.go`, `coverage_catalog.json` | 374 documented entries, 267 active; legacy 54-league list plus 320 evidence descriptors |
| Provider HTTP | `internal/sports/provider.go` | 32 MB body cap, single-flight, per-host 4-slot budget, TTL cache, 429/Retry-After cooldown, gzip, paced sends (one per 200 ms per provider), provider-wide cut-off after five refusals inside 30 s |
| ESPN ingestion | `internal/sports/espn.go`, `espn_detail.go` | Scoreboards, teams, periods, baseball/football situation, tennis per-match split, MMA cards, golf/cricket/rugby scoreboard detail |
| Detail enrichment | `internal/sports/detail.go`, `detail_team.go`, `detail_stats.go`, `detail_context.go`, `detail_mlb.go`, `detail_cricket.go`, `detail_pregame.go`, `mma_detail.go` | Plays, comparisons, leaders, player stats, lineups, standings, pregame, drives, hockey/basketball shots, win probability, score history, innings, scorecards, fighter profiles and fight statistics, capabilities |
| Cricket discovery | `internal/sports/cricket_discovery.go` | 24 h provider dropdown snapshot, numeric-slug validation, IPL kept as the polled competition, last-good retention, never promoting a candidate into the catalog |
| Service | `internal/sports/service.go`, `dated.go` | 30 s/15 s dated cache with in-flight sharing, last-known retention, partial merge, disk snapshot, status, logo cache |
| Matcher | `internal/sports/match/match.go`, `event.go`, `named.go`, `quality.go` | Token-aware team/network matcher, aliases, negative/noise tokens, confidence tiers, EPG promotion, contradicting-label and contradicting-guide rejection, one-edit fuzzy guardrails, grouping, reported-quality parser, auto-link and manual-search paths |
| Live TV bridge | `internal/livetv/match.go`, `quality.go`, `headers.go` | Channel identity for matching; guide search; measured quality probe (ffprobe, 2 slots, 30 s, provider-limit aware); allowlisted provider request headers on the proxy, remux and probe paths |
| API | `internal/api/handlers_sports.go`, `sports_background.go`, `server.go` | Read/search/stream/quality/admin routes, 15 s / 32-entry stream cache, 90 s background cycle |
| Preferences | `internal/sports/preferences.go`, `internal/api/handlers_sports_prefs.go`, `internal/db/sports.go` | Versioned per-user document, strict schema and school merge rules, compare-and-swap revision, 409 on stale writes, admin read/write |
| Team links | `internal/sports/match/links.go`, `internal/api/handlers_sports_links.go`, `internal/db/sports.go` | Primary plus ordered backups, tvg-id/name resolution with ambiguity refusal, suggestions, confidence tiers, provenance, auto-link preview/apply, uniqueness |
| Storage | `internal/db/sports.go`, migrations in `db.go` | `sports_teams`, `sports_team_channel_links` and `sports_preferences` with change detection and indexes |
| Config | `internal/config/config.go` | `sports` block, `SportsConfig.Normalize`, `WRENTV_SPORTS` override |
| Media probe | `internal/media/probe.go` | `ProbeURL` for live streams: bounded read timeout and provider User-Agent |

### Web client

| Area | Files | Notes |
|---|---|---|
| Sports hub | `js/sports.js`, `js/sports-model.js`, `sports.css` | Date rail, status tabs, sport/league filters, lanes in catalog order, featured event, freshness, refresh, spoiler toggle, expanded feed paged at 12, empty/error/retry |
| Event page | `js/sports.js`, `js/sports-sport-model.js` | Capability-gated tabs: Summary, Play-by-play, Stats, Lineups, Standings, Drives, Scorecards, Leaderboard, Set scores, Fight card, Timeline, Before the game, Win probability, Shot map, Scoring history; baseball diamond; combat matchup, tale of the tape and fight-stat bars; in-place detail refresh; honest gating |
| Race / cycling pages | `js/sports-race.js`, `js/sports-cycling.js`, models | Session picker, classification, circuit layout, on-request F1 archive, MotoGP context; stage picker, results, GC, TTT and guides |
| Watch flow | `js/sports-watch.js`, `js/sports-watch-model.js` | One candidate list for every Live TV provider (playlist/Xtream channels and Stremio items) with the same Preview and Watch actions on every playable row; manual channel search, measured quality checks, resolution sorting, live-first ordering, single-strong-feed direct play, docked preview with expand/stop (a Stremio preview selects, falls back between and releases the item's feeds like the full player); a finished event lists the same rows as Search for Replay |
| Sports player HUD | `js/live-player.js`, `js/live-tracks.js` | Sports events open the ordinary Live TV player with the event's competition, lifecycle, matchup and broadcast feed; the lifecycle pill is a status region (CONNECTING → LIVE once frames play, OFFLINE on failure), the back control is "Back to event", and "Change source" returns to the chooser; non-sports channels keep the plain player. Audio and subtitle menus appear only when the stream publishes alternate renditions, and a selection is applied to hls.js or the element's own track lists |
| Live TV | `js/live.js`, `js/live-model.js`, `js/live-player.js` | Channel list, group filter, now/next, guide expansion, search, deep-link `?q=`, Wren TV live sessions |
| Navigation | `js/app.js`, `index.html` | Live TV and Sports entries appear from `/api/server` flags; leaving a page stops refresh timers and the docked preview |
| Personalization | `js/sports-preferences.js`, `js/sports.js` | Strict preference parser, pin ordering, competition/team follows, Following filter, favorite markers and featured/lane ranking, revision conflict recovery |
| Appearance | `js/sports-appearance.js`, `sports.css` | Six appearance presets (including Wren TV tokens), large text, reduce motion, strong focus, per-device storage, contrast tests |
| Team Channels admin | `js/team-channels.js`, `js/manage.js` | League picker, team sync, suggestion ranking, manual channel search, primary/backup edits, ordering, removal, unresolved warnings, auto-link preview/apply, per-user favorites editor |

No new runtime dependency, no build step, no Dockerfile change. `package.json` is
dev-only (Node's built-in test runner) and is not served by the server.

### API

| Method | Path | Status |
|---|---|---|
| GET | `/api/sports/status` | Done |
| GET | `/api/sports/leagues` | Done |
| GET | `/api/sports/scoreboard?date=&league=` | Done |
| GET | `/api/sports/hub?date=` | Done (includes per-league `leagues` availability) |
| GET | `/api/sports/search?q=` | Done (channels, EPG now/upcoming, teams, events) |
| GET | `/api/sports/events/{id}` | Done (enriched, capability-gated detail) |
| GET | `/api/sports/events/{id}/streams?q=&broadcast=` | Done (flat + grouped + 15 s cache) |
| GET | `/api/sports/match?event=` | Done (query-parameter form for native clients) |
| POST | `/api/sports/streams/quality` | Done (measured probe, no playback session) |
| GET | `/api/sports/logo?url=` | Done (CDN allowlist, disk cache) |
| GET/PUT | `/api/sports/preferences` | Done (per-user versioned snapshot, revision CAS, strict body) |
| GET | `/api/sports/teams?league=` | Done (persisted teams with resolved links) |
| GET | `/api/sports/teams/linked?ids=` | Done (batched hub lookup, capped at 100 ids) |
| GET | `/api/sports/teams/{id}/links` | Done |
| GET | `/api/sports/teams/{id}/suggestions?query=&limit=` | Done (scored suggestions, manual text search) |
| GET | `/api/sports/leagues/{league}/auto-link/preview` | Done (per-team candidate, gap, skip reason) |
| PUT | `/api/sports/teams/{id}/links/primary` | Admin only |
| POST | `/api/sports/teams/{id}/links` | Admin only (backup append) |
| POST | `/api/sports/teams/{id}/links/order` | Admin only (exact backup order) |
| DELETE | `/api/sports/teams/{id}/links/{linkId}` | Admin only |
| POST | `/api/sports/leagues/{league}/auto-link/apply` | Admin only (re-evaluates before insert) |
| GET/PUT | `/api/admin/users/{id}/sports/preferences` | Admin only |
| GET/PUT | `/api/admin/sports` | Done (strict JSON, unknown leagues dropped, non-empty all-unknown rejected) |
| POST | `/api/admin/sports/refresh` | Done |
| POST | `/api/admin/sports/logo-cache/clear` | Done |
| POST | `/api/admin/sports/teams/sync` | Done |
| GET | `/api/sports/races` | Done (weekends, ordered sessions, verified circuit) |
| GET | `/api/sports/races/{league}/{event}[/{session}]` | Done (session classification, driver statistics) |
| GET | `/api/sports/races/{league}/{event}/{session}/archive` | Done (`mode:archive`, unavailable when the provider has no archive) |
| GET | `/api/sports/cycling` | Done (organizer calendar, current/upcoming/settled stages) |
| GET | `/api/sports/cycling/{race}/{year}/{stage}` | Done (stage results, GC, teams, route guides) |
| GET | `/api/sports/motogp/standings` | Done (official table only) |
| GET | `/api/sports/motogp/circuit/{event}` | Done (verified circuit context) |
| GET | `/api/sports/cricket/series` | Done (discovery candidates, `complete:false`, `stale` flag, never polled) |
| GET | `/api/sports/events/{id}/addons` | Done (event-scoped addon candidates, opaque ids, source states, stale flag) |
| POST | `/api/sports/addons/{id}/quality` | Done (measured quality via the Live TV probe contract) |
| POST | `/api/sports/addons/{id}/play` | Done (Live TV session on a server-held addon URL; 410 when expired) |

Read, search, stream, quality, preference-read and team-link-read routes sit in
the signed-in group; every preference write for another user and every link
mutation sits behind `adminOnly`. Verified: anonymous request → 401, member
mutation → 403, missing team/channel → 404, duplicate channel → 409.

---

## 2. Inventory coverage

| # | Feature | Status | Evidence / remaining work |
|---|---|---|---|
| A1 | Sports tab in navigation | Partial | Web sidebar entry from `/api/server.sports`; native tabs are P5/P6 |
| A2 | Hub route | Done | `/sports`, feed refresh, route memory per session |
| A3 | Event detail route | Done | Deep-linkable `/sports/event/{id}`, tabs, enrichment, watch hand-off |
| A4 | Race detail route | Done | `/sports/race/{league}/{event}/{session}` with session picker, classification, circuit, F1 archive and MotoGP context; API covered by `TestSportsRaceRoutesAndSessionDetail` |
| A5 | Cycling detail route | Done | `/sports/cycling/{race}?stage=` with stage picker, results/GC/TTT, guides; API covered by `TestSportsCyclingMotoGPAndStandingsContracts` and provider tests |
| A6 | Local preview routes | Done | Demo data kept as tests only; no production route |
| A7 | Back/Select remote semantics | Next | Web D-pad layer is P4; native focus follows each platform |
| B1 | TV / large-screen hub | Partial | Web lanes/featured/filters; tvOS/Android TV presentation is P5/P6 |
| B2 | Mobile hub | Partial | Responsive web; native phone hub is P5/P6 |
| B3 | Shared card model | Partial | Go DTO + web cards; native card models are P5/P6 |
| B4 | Date selection | Done | Yesterday/today/tomorrow, capability gate, server window validation |
| B5 | League lanes and ordering | Done | Catalog order, featured removed from its lane |
| B6 | Sport / league / status filters | Done | Status tabs, sport select, competition select, local search |
| B7 | Pinned sports ordering | Done | 20 supported pins, per-user revisioned document, ordered pins rank lanes and featured; unknown pins survive |
| B8 | Competition/team/athlete follows | Done | Ordered competition follows, `league:team` follows, star markers and Following filter; athletes and schools are stored but have no catalog/UI yet |
| B9 | Hub search | Done | The hub field filters the loaded feed and, from two characters, debounces a server search across channels, EPG programmes, events and teams. Results render above the lanes (and above the empty state) with real identities: EPG/channel results link to `/live?channel=…`, events to their deep link, teams keep their competition. Stale responses are aborted, short queries clear results, and a failed search degrades to the local filter. `TestSportsLiveSearchContracts` (team keeps its league) and `searchResultGroups` tests |
| B10 | Spoiler mode | Done | Web toggle, device-local, hides hub scores, event page reveals |
| B11 | Appearance themes | Done | Six presets (Wren TV, Midnight, Graphite, Evergreen, Aubergine, Warm Slate), contrast-tested and scoped to Sports |
| B12 | Readability and text scale | Partial | Large text, reduce motion and strong focus are ported and persisted; arbitrary text scale and TV overscan sizing belong to native clients (P5/P6) |
| B13 | Staleness / freshness display | Done | `Feed checked …` / `Scores may be delayed`; per-lane delayed/unavailable badges |
| B14 | Refresh cadence | Done | 30 s live/stale, 60 s upcoming, 300 s settled; paused when hidden/off-page |
| B15 | Feed cache | Done (web) | Server dated cache with in-flight sharing plus the client cache ported from MediaStorm's `createSportsFeedCache`: date+calendar-day key, 10-minute TTL, three entries, structural sharing, explicit invalidation on hub entry, manual refresh and retry. The cache paints a remembered day while its request is in flight and never replaces that request (`sports-cache.test.js`) |
| B16 | Large-feed pagination | Done (web, browser-verified) | The expanded feed pages at 12 cards, `Previous`/`Next` swap the page (clamped when the feed shrinks) and `Back to lanes` replaces the page's cards with the rails, so the mounted set stays bounded. The browser gate asserts the 12-card bound, the swap and that the feed is gone after collapsing. Lane rails keep their full list, matching MediaStorm's hub scroll; native lazy lists remain P5/P6 |
| B17 | Empty/error/recovery states | Done | Disabled, unavailable, stale, retry, clear-filters, no-blank-screen |
| B18 | Multiscreen | N/A | Wren TV has no multiscreen |
| C1 | League catalog with capabilities | Done | 374 entries, 267 active, canonical dynamic IDs |
| C2 | Catalog acceptance rules | Done | Server authoritative; client renders only `enabled` rows |
| C3 | ESPN scoreboard ingestion | Done | Concurrent cap 4, provider-date queries, coverage merge, partial retention |
| C4 | Secondary providers | Done | Boxing/TheSportsDB (`TestBoxingScheduleKeepsCardContract`), CFL (`TestCFLFixturesTeamsAndStandings`), F1 archive (`TestF1ArchiveRequiresExactSessionMatch`), organizers/cycling (`TestCyclingScheduleAndResults`, `TestPublishedCyclingCalendarAndSubscription`, `TestGiroScheduleAndClassification`), MotoGP (`TestMotoGPSessionsAndClassification`) |
| C5 | Provider HTTP layer | Done | Ported with caps, single-flight, host slots, TTLs |
| C6 | Scoreboard cache and persistence | Done | `DataDir/cache/sports/scoreboard.json`; restart restores the snapshot |
| C7 | Detail enrichment | Done | Capability-gated plays, stats, comparisons, leaders, lineups, standings, pregame, shots, drives, win probability, score history, innings, scorecards |
| C8 | Team catalog | Done | Full ESPN catalog persisted, periodic sync with change detection, per-league team list and link management |
| C9 | Status and health | Done | enabled/dateQueries/lastRefresh/lastError/gameCount/refreshing |
| C10 | Manual refresh | Done | Admin-only; re-registers leagues and refreshes immediately |
| C11 | Logo proxy/cache | Done | https + ESPN CDN allowlist, 5 MB cap, image-type check, admin clear |
| C12 | Background scheduling | Done | 90 s scoreboard → team catalog → persist, re-checks the switch |
| C13 | Cricket series discovery | Done | Provider dropdown parsed with strict numeric slugs, 24 h snapshot, last-good on failure, `complete:false`; candidates never enter the polled catalog (`cricket_discovery_test.go`, API contract test) |
| D1 | Generic matchup card | Done | Away/home, logos, records, live period/clock, broadcast, venue |
| D2 | Live availability and context | Partial | Venue, neutral site and records on cards; a separate availability explanation is not built |
| D3 | Lead history / scoring graph | Done | Scoring-history tab plots the home margin and lists the validated sequence |
| D4 | American football | Partial | Drives tab with plays and scoring flags, projected field, replay traces, first-down line, possession and down/distance; provider timeouts are not shown |
| D5 | Basketball | Partial | Quarter breakdown, shot chart, win probability, player stats; possession/bonus indicators are not shown |
| D6 | Baseball | Partial | Count/outs/bases text plus a base-occupancy diamond; no separate linescore table |
| D7 | Ice hockey | Partial | Period scores, OT/SO labels, shot map; strength/shot situation is not shown |
| D8 | Soccer | Partial | Halves/ET/penalty labels, lineups with substitutions, comparisons; no pitch layout |
| D9 | Tennis | Done | Set-by-set periods, dedicated set-score tab with superscript tiebreaks and tournament context; no fabricated points |
| D10 | Rugby union/league | Done | Provider periods, half/full-time status and a scoring timeline with provider-labelled symbols; possession/territory panels are not supplied by the provider |
| D11 | Cricket | Done | Innings, scorecards, batting/bowling stats, result summary |
| D12 | Golf | Done | Leaderboard with to-par totals, rounds and hole strokes |
| D13 | Combat (MMA/boxing) | Done | Fighter matchup with portraits and records, result label, tale of the tape from provider profiles, fight card in provider order, comparable fight-stat bars; MMA profiles/statistics ported in `mma_detail.go` with scheduled bouts never showing statistics |
| D14 | Racing | Done | Race board, weekend sessions, enriched classification, verified circuit layout; `TestSportsRaceRoutesAndSessionDetail`, `TestRaceBoardNormalizesWeekendAndCircuit` |
| D15 | F1 archive analysis | Done | On-request archive timing labelled as archive, drivers/messages/weather parsers, `TestF1ArchiveRequiresExactSessionMatch` |
| D16 | MotoGP | Done | Sessions/classification, championship standings and circuit context only when the official table exists; `TestMotoGPSessionsAndClassification` |
| D17 | Cycling | Done | Organizer calendar, stages, results, GC, TTT, subscription and verified guides; `TestCyclingScheduleAndResults`, `TestGiroScheduleAndClassification`, `TestCyclingFeedSeparatesUnavailableFromStale` |
| D18 | Standings and leaders | Done | Standings tab, game leaders, team comparisons |
| D19 | Editorial / pregame content | Done | Season comparison, records, recent form, leaders, probables, previous meeting |
| D20 | Player presentation | Done | Box-score categories, headshots via the logo cache |
| D21 | Event symbols and artwork | Done (web) | Original 24×24 stroke glyphs for 13 sports plus provider aliases in `js/sports-glyphs.js`, rendered on hub cards and the event header; `sports-glyphs.test.js` asserts coverage, decorative attributes and unknown-sport refusal. Native artwork remains P5/P6 |
| D22 | Accessibility labels | Done (web) | `buildEventAccessibilityLabel` ported from MediaStorm's `sports-model.ts`: participants and scores, lifecycle, sport-aware status/result, provider-only live situation (count/outs/bases). Hub cards and the featured card carry it as `aria-label`; tests mirror the source cases. Watch availability is omitted because the hub does not know it, rather than guessed. Native labels remain P5/P6 |
| E1 | Detail tabs and capability gating | Done | Summary, plays, stats, lineups, standings, drives, scorecards, leaderboard, pregame, probability, shot map, history — only when data exists |
| E2 | Detail refresh | Done | In-place refresh at 120 s live, 600 s final and 60 s scheduled (MediaStorm's pregame poll); paused while the tab is hidden or the Watch panel is open, stopped on leaving the page (`detailRefreshMs` test) |
| E3 | Sticky score header | Done (web) | `formatDetailStatus` ported from MediaStorm's formatter: NHL periods/intermission/OT/SO, soccer halves/ET/penalties from period and shootout scores, cricket reported detail, college-football overtime clock suppression, generic live/final/scheduled, stale suffix. The event header uses it instead of the raw provider detail; tests port the MediaStorm cases |
| E4 | Play-by-play and drives | Partial | Chronological plays, scoring flags, drive list and replay geometry with previous-play stepping; drives are listed rather than grouped into a separate explorer view |
| E5 | Box score / team stats | Done | Per-player categories, team comparisons, zero vs missing preserved |
| E6 | Lineups | Done | Starters, positions, numbers, subbed in/out |
| E7 | Standings tab | Done | Same table component as the hub, sport-aware columns |
| E8 | Win probability | Done | Chart and sampled list; hidden when unsupported |
| E9 | Score history | Done | Complete scoring sequence with period/clock and margin chart |
| E10 | Tournament context | Done | Literal provider label, no implied bracket progression |
| E11 | Docked / preview playback | Partial | Web docked preview expands to the full player; native preview is P4 |
| E12 | Live stats delay | Done (web, browser-verified) | `js/sports-stats-buffer.js` is a line-for-line port of MediaStorm's `stats-buffer.ts` (monotonic receipts, request ordering, 300 s history, 256 receipts, copies, `buffering` instead of falling forward). `js/sports-stats-delay.js` adds the 5 s-normalized 0–300 s preference, per server+account device key, and `selectDelayedEvent`. The event page offers `Sync stats · Live/15s/…/120s`, withholds scores, winner flags, period, clock, detail label and even the lifecycle label while buffering (`Syncing stats…`), and applies the same snapshot to the docked-preview HUD; 16 tests. The browser gate verified persistence across reload and the buffering copy; the screenshot shows a buffering event page with no score |
| E13 | Playback orientation | Partial | Native players handle orientation; web is landscape by default |
| F1 | Stream discovery endpoint | Done | Flat matches, grouped feeds, per-request manual search |
| F2 | Channel matching | Done | Token-aware team/broadcast/EPG matching, aliases, negative tokens, confidence tiers, reasons/terms; a channel label or current programme naming another series, sport, session or stage rejects the channel before scoring, and a fuzzy matchup tolerates one edit only when the side carries no other provider or opponent word (provider descriptions from addon catalogues and Stremio sources feed the same rule) |
| F3 | EPG promotion | Done | Current and next programme promote a channel; broadcast filter; ended/archive labels excluded |
| F4 | Grouping and alternatives | Done | Equivalent entries collapse; distinct feeds remain explicit alternatives |
| F5 | Watch decision | Done | Strong lone feed plays directly; anything else opens the chooser; manual results never auto-play |
| F6 | Feed ordering | Done | Live-labelled feeds first, unknown next, scheduled last |
| F7 | Reported quality | Done | Only explicit labels; conflicting claims use the lower; deterministic order |
| F8 | Quality probing | Done | Server probe without a playback session, 2 slots, 30 s, provider-limit check; client checks one feed at a time, commits results individually, cancel keeps completed, re-check available |
| F9 | Progressive addon search | Done (backend + Watch preview) | Event-scoped discovery queries enabled streaming addons (4 addons, 2 catalogues each, 6 s per request, 15 s total, replay shelves skipped), matches items with the ordinary matcher and drops weak results. Clients receive opaque ids only; `POST /api/sports/addons/{id}/quality` reuses the Live TV quality contract and `POST /api/sports/addons/{id}/play` starts a normal Live TV session on the server-held URL. Identities expire after 10 minutes (410), failures are sanitized, and discovery caches for 3 minutes with stale-last-good retention. Watch lists addon sources with confidence, probes, a docked preview and a full-screen button that reuses the ordinary Live TV player and session endpoint (`openLiveChannel({ addonId })`), stopping the preview session first. A stream entry's `proxyHeaders.request` (or the older `headers`) is forwarded through an allowlist — `Referer`, `Origin`, `User-Agent` — with control characters rejected and values capped, so a provider that refuses an anonymous request plays; `Cookie`, `Authorization` and any other name a provider invents are dropped, and the same set reaches the proxy, the remuxer and the ffprobe path |
| F10 | Source/category scope | Done | Automatic event discovery applies `sportsLinkScope` (default sources/groups plus the per-league override); an explicit search stays global so an operator can pick outside the scope. `TestSportsEventStreamScopeAppliesToDiscoveryOnly` |
| F11 | Stream failure and recovery | Done | Other feeds stay selectable, probes fail per feed, and the last working source per event is remembered on the device, listed first with a `Last used` badge (`preferLastSource` test); a measured sort still wins |
| F12 | Recording options | N/A | Wren TV has no DVR recording; no fake control is shown |
| F13 | Legacy flat stream list | Done | `streams` returned alongside `groups` |
| F14 | Stream caching | Done | 15 s, 32 entries, keyed by event/broadcast/query |
| G1 | Team identity persistence | Done | `sports_teams`, stable `league:providerTeamId`, catalog sync |
| G2 | Primary + backup links | Done | One primary, ordered backups, snapshot, provenance, timestamps, duplicate rejection |
| G3 | Link resolution at read time | Done | tvg-id then name then stored id; ambiguous matches stay unresolved and unplayable |
| G4 | Channel suggestions | Done | Ranked candidates plus explicit manual search, never claiming a strong automatic match |
| G5 | Auto-link preview/apply | Done | 0.95 alone or 0.85 with 0.20 lead, per-team skip reason, never replaces an existing primary |
| G6 | Team list and links overview | Done | `/api/sports/teams`, `/teams/linked`, `/teams/{id}/links` with resolved state |
| G7 | Admin-only mutations | Done | Authenticated reads; every link/preference mutation is behind `adminOnly` and tested |
| G8 | Scheduled sync | Done | 90 s background cycle refreshes teams with change detection |
| H1 | Server-wide sports settings | Done | Sports tab: enablement, league filter, favorites, source/group scope, per-league overrides, refresh and logo-cache actions |
| H2 | Per-profile preferences | Done | Per authenticated Wren TV user (no profiles), versioned snapshot with 409 on stale revision |
| H3 | Preference merge semantics | Done | Dedupe, unknown IDs survive, unknown fields/versions rejected, school merge rules, empty programme list preserved |
| H4 | Admin sports page | Done | Manage → Settings → Sports using existing panels and admin authorization |
| H5 | Per-user profile editor | Done | Admin users screen reads and saves another user's document with its loaded revision |
| H6 | Navigation visibility migration | N/A | Wren TV derives nav from `/api/server` flags and has no stored per-user nav visibility to migrate |
| H7 | Favorites in server settings | Done | Server-wide favorite team IDs remain independent of per-user follows |
| I1 | Sports player surface | Done (web, browser-verified) | Wren TV's live player and sessions are reused, and the full player now carries the sports HUD: competition and lifecycle, the matchup as the title, the broadcast feed (`source · channel` or the addon name), a status-region lifecycle pill and "Back to event". The docked preview carries the same delayed scoreline and feed identity. Recording, multiview and multiscreen are absent because Wren TV has no such feature, so no stand-in controls were added |
| I2 | Live session lifecycle | Done | Existing live sessions reused; preview stops the session on leave/stop |
| I3 | Preview focus | Done (web, browser-verified) | Opening the docked preview focuses `Stop`; `Escape` and Stop close it and restore focus to the launching control; a tune failure releases the session and focuses an `aria-describedby`-linked error; handover to the full player (expand/addon full screen/Watch) deliberately does not restore focus behind the overlay; a fatal stream error releases the session, keeps the panel and announces the failure. The real-browser gate passed focus order, restoration, re-activation and non-stealing. It also caught a real race: stopping before the tune settled leaked the provider session; the late response now releases it |
| I4 | Drive replay | Done (web, browser-verified) | Replay logic and geometry ported to `js/sports-replay-model.js` and covered by 10 tests (classification, trace eligibility, field projection, situation/goal direction, previous-trace stepping, animation, result labels). Each of the last twelve plays in the Drives tab draws its provider trace with possession, down-and-distance, a first-down line only where the provider supplied one, its result label and the previous eligible play; a play without usable coordinates states `No in-field trace for this play.` The browser gate inspects a settled football game for a drawn rush/pass trace, in-field first-down markers, named possession, a previous-play row and the refusal copy |
| I5 | Perspective field | Done (web, browser-verified) | The same module supplies the perspective geometry (`projectFieldPoint`, `footballReplayPath`, `footballAnimatedPath`) with capability gating: absent coordinates, penalties and non-advancing plays produce no trace rather than a misleading line, and an unknown possession produces neither a goal direction nor a first down. The Drives tab renders the field from the possessing team's direction with labels for screen readers; model tests cover the geometry and the browser gate confirms the rendered traces and markers |
| I6 | Stats delay | Done (web, browser-verified) | See E12. The docked preview HUD repeats the delayed scoreline and shows the buffering note instead of a live score; it is fed by the page's delayed snapshot and never fetches independently, so switching sources cannot leave an old score on screen. The real-browser gate asserts the HUD is present and non-empty on a live preview |
| I7 | Player HUD controls | Done (web, browser-verified) | "Back to event" and "Change source" are real sports controls, and the lifecycle status reflects the media element. Audio and subtitle menus now follow MediaStorm's rule — a control exists only when the stream publishes a choice: `js/live-tracks.js` builds the lists (more than one audio rendition; subtitles as off plus every published rendition), `live-player.js` fills them from hls.js or the element's own rendition lists and applies the selection to the player, and `live-model.nativeHlsSupported` keeps Chromium on hls.js, where those APIs exist. A single-rendition stream — including any remuxed MPEG-TS channel, whose ffmpeg pipeline carries one AAC track — shows neither menu; nothing is faked. Six unit tests cover the label and visibility rules, and the browser gate switches audio and subtitles on a real multi-rendition master (verified through hls.js state, not just the menu) and confirms both controls are absent on a single-rendition channel. Live playback has no Pause, matching MediaStorm. Recording and multiscreen remain absent by platform decision |
| J1–J6 | Scoreboard/game/detail/league/status/streams contracts | Done | Contract tests assert field names and capability flags |
| J7–J8 | Preferences and team links | Done | Contract tests cover the revisioned snapshot, link lists, candidates and auto-link preview/apply |
| J9 | Racing/cycling contracts | Done | Races list, session detail, archive mode and unavailable archive asserted through the API; provider/session tests cover classification, statistics and MotoGP |
| J10 | Standings contract | Done | Groups, rows, sport-aware columns |

---

## 3. Verification

### Automated

| Command | Result |
|---|---|
| `go build ./...` | pass |
| `go vet ./internal/...` | pass |
| `go test ./...` | pass on the final run (2026-10-25), including the new `TestScoreboardDateAcceptsEveryViewerTimeZone`. One earlier run on 2026-10-24 failed only `internal/usenet` `TestHedgedArticle` ("stuck article asked for 3 times, want 2"); three standalone reruns of that test failed the same way, a fourth and later full-suite runs passed. That package is unmodified and imports neither `livetv` nor `media`, so it is a pre-existing timing-sensitive test, recorded rather than dismissed |
| `CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=…" ./cmd/wrentv` | pass (same flags as the Dockerfile build stage); the result is a static ELF |
| Docker image build/run | **blocked, not passed.** No Docker/podman/buildah/nerdctl, no socket and `unshare -Ur`/`unshare -n`/`unshare -m` denied, so the image cannot be built or started here. Re-probed 2026-10-25 (the process is itself inside an overlayfs container, `0::/`, `CapEff` without `CAP_SYS_ADMIN`). An image-contract preflight ran the static binary with the image's runtime variables, served the embedded assets, reached `/api/health` and kept its state across a restart (`evidence/docker-image-contract.txt`), which is evidence about the binary, **not** the container gate |
| `node e2e/run-gate.mjs` (Playwright gate against the built binary) | **10 passed, 0 skipped** on 2026-10-25 (final run; the two earlier runs in this pass failed at the new track test until the client defect below was fixed, so this was not an unbroken streak): search, expanded-feed paging (12-card bound, page swap, feed released on collapse), drive replay/perspective (drawn traces, in-field first-down markers, possession, previous eligible play, refusal copy), event detail, addon identity/quality/playback/switching/failure/cleanup, the sports player HUD identity, **live audio/subtitle track menus on a multi-rendition stream and their absence on a single-rendition one**, a mandatory sanitized provider failure with session release, preview focus/Escape/restoration, full-player focus handover, stats delay. Real ESPN data (151 events), 304 generated Live TV channels, Chromium, real ffprobe, and a fixture addon whose provider refuses anonymous requests. The addon case verifies the forwarded referer/origin/agent on the ffprobe request too, the measured 640 × 360 quality, and that a `Cookie`/`X-Api-Key` never arrives. The playlist-channel quality route is measured separately before the browser run (`640 × 360 · H264 · 25 fps`, `quality-playlist.json`). **This is not the Docker gate**, which remains blocked. Evidence: `docs/sports-hub/evidence/gate-run/` (log, server log, quality JSON, three screenshots) and `evidence/gate-run.md` |
| `npm run test:web` (`node --test internal/web/static/js/*.test.js`) | 167 tests pass on the 2026-10-25 final run (`sports-model` 17, `sports-timezone` 3, `sports-stats` 16, `sports-cache` 4, `sports-glyphs` 3, `sports-replay-model` 10, `sports-sport-model` 10, `sports-watch-model` 22, `live-model` 9, `live-tracks` 6, `sports-preferences` 10, `sports-appearance` 7, plus race/cycling models). The suite was repeated under `TZ=UTC`, `America/New_York`, `Asia/Kolkata`, `Pacific/Kiritimati`, `Pacific/Midway` and `Australia/Lord_Howe` (110/110 each before the seven track tests were added; the timezone-sensitive files are unchanged) |
| `node e2e/run-browse-livetv.mjs` (Playwright Browse Live TV regression) | **2 passed, 0 skipped** on 2026-10-25 against the built binary, real ESPN data and the fixture provider: the SportsHub deep link lists the provider's matched event on the first click (and plays it), and an event no provider carries settles on a real empty answer. No ffmpeg needed: the fixture HLS is a real playlist with one segment. It fails when the server's event match is disabled (`a real event is matched as a provider event`), so the assertion cannot pass without the fix. Evidence: `/tmp/wrentv-browse/artifacts/playwright-run.txt` |
| `node --check` on changed browser modules | pass |

Defects found and fixed by the 2026-10-25 verification pass (the first two were
hidden by the gate itself, not by missing tests):

- **The gate ran with an empty guide.** `e2e/run-gate.mjs` built XMLTV stamps
  from `toISOString()`, emitting `20261025T035526 +0000`; `parseXMLTVTime`  (correctly, per XMLTV) rejected the `T` form, so all 604 programmes were
  dropped and the server logged `guide=0 errors=0` — a silent success. Every
  earlier gate run therefore proved nothing about EPG-backed matching or the
  seven-day horizon. Fixed in the runner; the final log reports
  `channels=304 guide=302`, and a direct `/api/sports/events/{id}/streams`
  check shows the first four events resolving two strong groups each.
- **The Watch panel claimed "no match" while the search was still running.**
  `render()` treated the initial empty group list as a settled answer, so
  *No Live TV channel is a plausible match* flashed before results arrived — a
  false statement to the viewer, and the race that made test 4 fail once the
  guide changed hub ordering. `sports-watch.js` now paints `Matching feeds…`
  while `state.loading`, and the gate waits for result rows rather than for the
  always-present search box.
- **The live player took the native-HLS path in Chromium, where there are no
  track APIs.** `live-player.js` and the docked preview decided native playback
  from `canPlayType('application/vnd.apple.mpegurl')` alone. Chromium answers
  that affirmatively but plays HLS through its own pipeline without exposing
  `audioTracks`/`textTracks`, so the multi-rendition fixture played with no
  audio menu and no way to know a choice existed. `live-model.nativeHlsSupported`
  now uses the same rule the library player already used (Safari only), the live
  player and preview share it, and hls.js is used wherever the rendition APIs
  exist. The hls.js path also needed the correct hooks: alternate renditions
  arrive on `AUDIO_TRACKS_UPDATED`/`SUBTITLE_TRACKS_UPDATED`, not on
  `MANIFEST_PARSED`, which is why the first attempt saw two tracks in the
  instance and none in the menu.

- **A provider refusal flooded the log and was retried every cycle.** ESPN's
  edge (`AkamaiGHost`) answers 403 for every endpoint once it decides the
  client is abusing the API, and the default configuration polls the whole
  active catalog — 211 ESPN leagues — every 90 seconds, with a further 187 team
  catalogues on the first pass. The first cold cycle therefore emitted roughly
  400 requests at four-way concurrency, which is what earns the block, and then
  every league logged its own 403 twice a cycle for as long as the block
  lasted. Now: sends are paced to one request per 200 ms per provider, five
  refusals inside 30 s silence the provider for a minute (the status of the
  actual refusal is preserved, so the log says 403 and not 429), a refused
  league backs off 5 min doubling to 6 h with per-league jitter and is not
  re-asked in the meantime, a refusal is reported once per cycle instead of per
  league, last-known scores survive, and a cycle that runs out of time no
  longer logs one deadline per waiting league. Tests:
  `TestProviderRequestsArePaced`, `TestRepeatedRefusalStopsTheRestOfTheBurst`,
  `TestRefusedScoreboardBacksOffAndKeepsLastKnownScores`,
  `TestRefusedTeamCatalogBacksOffInsteadOfRepeating`.
  Pacing, not a user agent, is the lever: this host was refused with the
  default Go agent, a Chrome agent and a short application agent alike, so no
  `User-Agent` change was kept.

- **Browse Live TV did not search Stremio providers on the first click.**
  The SportsHub deep link (`/live?q=…&event=…`) ranked playlist channels only:
  `serveEventStreams` matched `MatchChannels()` and `live.js` dropped every
  addon event in event mode, so a manifest's event appeared only after the
  viewer submitted the Live TV search box, which drops the event context and
  falls back to a plain text search. The same request now matches the Live TV
  event entries with the shared matcher (live) or the replay scorer (finished)
  and returns them as `events`; the page renders matched channels and addon
  events as rows in one list with the existing Watch/Feeds flow. A refresh that
  has not finished — including a newly enabled provider — answers `ready:false`
  / `livetv_pending` and is never cached, and the client keeps the matching
  state while the server reports `refreshing`. Tests:
  `TestBrowseLiveTVMatchesStremioEventsOnFirstClick`,
  `TestBrowseLiveTVReportsPendingBeforeProvidersFinish`,
  `TestBrowseLiveTVEmptyAnswerSettles`,
  `TestSportsAddonEventMatchesUsesTheModeShelf`, the `matchedEvents` unit test
  and `e2e/browse-livetv.spec.js`.

- **Stremio providers grew their own Watch panel.** The event page rendered
  matched Stremio items in a separate *Stremio providers* section with their own
  `data-provider-watch` buttons (labelled *Watch replay* for a finished event),
  and the panel carried a second *Browse Live TV* link beside the page's own.
  That made a manifest look like something other than a Live TV provider.
  `watchCandidates`/`watchTarget` in `sports-watch-model.js` now merge matched
  channels and provider items into one list with one Watch action, the replay
  panel lists the same rows, and the panel no longer repeats the browse action;
  the transport (channel session vs automatic addon playback) stays inside the
  candidate.

- **A Stremio row lost its Preview button.** `candidateRow` computed
  `const preview = !!channel && previewAllowed(channel)`: Preview was gated on
  the candidate being a *channel group*, so a Stremio (or Xtream) item that was
  perfectly playable rendered with Watch only — not because its identity was
  missing (its source, content type and item id are what the Watch action
  already used) but because the row template asked the wrong question. The
  capability is now answered per candidate (`previewAllowed(candidate)`: a
  channel needs its reference and a scored match, a provider item the identity
  its playback route needs), so both kinds expose the same `Preview` and `Watch`
  buttons, in the same order, from the one row template.
  There is also one docked preview component rather than three: `previewSpec`
  describes a source (title, badge, status, transport, full-screen handover) and
  `showPreview` renders it, so the legacy streaming-addon preview became a third
  transport instead of a second implementation. A Stremio preview tunes through
  the same `/api/livetv/addons/{source}/item/{type}/{id}/play` route the Watch
  action uses — the server ranks and proves the item's feeds — inside the same
  bounded loop the full player runs (`planAutoRound`, `AUTO_ROUNDS`): a feed
  that does not start is released and replaced, a session created after the
  viewer stopped the preview is released rather than adopted, and only a
  provider that refused the request (rate limited, unreachable, no feed left)
  ends the preview with the server's own message. No channel id is invented for
  a Stremio item, and an item with no playable identity gets neither button
  rather than a Preview it could not honour.
  Tests: `sports-watch-model.test.js` (both kinds answer the capability question
  in their own terms; one row template, one preview component, no
  `data-provider-preview`) and `e2e/sports-watch.spec.js` (both rows expose the
  same two controls; each calls its own route and never the other's; switching
  between them and handing a preview to the full player leaves one session and
  none after close; a dead feed is replaced by the next one; a provider that
  cannot tune or is rate limiting this server is reported without a URL, asked
  once and holds no session).

The two conditional gate assertions are now gone rather than documented:
`openPreviewableEvent` asks the server which event has a matched feed and throws
if none does (the gate's own playlist guarantees one), and the provider-failure
test selects the broken addon by name, asserts the announced error, asserts no
URL, and asserts the session was released. Neither can pass without exercising
its subject.

F9 tests added:

- `internal/api/sports_addons_test.go` — a fixture addon serves a manifest, a
  searchable catalogue and a stream link: discovery finds only the event match,
  never queries a replay shelf, caches within the TTL, and leaks no URL or
  credential. Quality reports `unavailable` without ffprobe rather than a
  fabricated resolution, playback starts a real Live TV session and stops it,
  expired identities answer 410, unknown tokens 404, provider failures 502 with
  no upstream detail, and four concurrent plays get distinct sessions.
- `internal/livetv/livetv_test.go` / `quality_test.go` — `PlayURL` and
  `ProbeStream` share the session limit, probe slots and metric contract with
  channel playback; an addon key is never given a cached stream kind.
- Watch UI: addon candidates render with confidence, `Check quality`, a
  docked `Preview` and `Full screen`; they never auto-play and reuse last-source
  memory. Full screen closes the preview session before opening the Live TV
  player, and the player's existing close/error path stops the addon session.
- `internal/livetv/headers_test.go` — the allowlist keeps `Referer`/`Origin`/
  `User-Agent` and drops `Cookie`, `Authorization`, API keys and invented names;
  a CR/LF value and an over-length value are dropped rather than repaired; the
  proxy forwards the identity the provider asked for and never the credential.
- `internal/media/probe_test.go` — ffprobe receives the agent through
  `-user_agent` and the rest through `-headers` (sorted, CRLF separated, before
  `-i`), so `User-Agent` is never sent twice and no default ffprobe agent
  competes with the provider's.
- `internal/api/sports_addons_test.go` — the fixture stream answers 403 without
  a referer/origin, so the playlist reaching the player through the session
  proves the identity was forwarded, and the fixture's `Cookie`/`X-Api-Key`
  never arrive.

P4 replay tests added:

- `internal/web/static/js/sports-replay-model.test.js` — 10 tests ported from
  MediaStorm's `football-replay.test.ts`: play classification, NO PLAY
  precedence, trace eligibility, field projection, replay snapshot lookup,
  away/home attacking direction, goal-to-go labels, possession changes,
  previous-trace stepping, quadratic animation, and result labels that never
  invent a catch or yardage.
- Browser coverage for the same feature is in the gate: the Drives tab of a
  settled football game must draw at least one rush/pass trace, keep every
  first-down marker inside the field, name possession, show a previous eligible
  play and print the refusal copy for unplaceable plays.

P4 focus/stats-delay and P7 cache/cosmetic tests added:

- `internal/web/static/js/sports-stats.test.js` — MediaStorm's `stats-buffer`
  cases ported verbatim (same-time dynamic fields, warm-up and increased delay
  never falling forward, stale/regressing responses rejected, 300 s/256 receipt
  bounds, invalid clocks/delays) plus the web-only `selectDelayedEvent` rules:
  scores withheld on buffering, delay 0 paints the latest response, raising the
  delay buffers, cleared history re-buffers, and withheld copies keep structure
  but no live values.
- `internal/web/static/js/sports-cache.test.js` — MediaStorm's `sports-refresh`
  cases: structural sharing keeps unrelated event/team references, deleted
  fields never survive and previous snapshots are never mutated; the cache is
  bounded to three dates, expires at 10 minutes, cannot cross midnight or a
  second cache identity, and `clear()` invalidates explicitly.
- `internal/web/static/js/sports-glyphs.test.js` — every catalog sport has a
  decorative glyph, provider aliases resolve to the same drawing and an unknown
  sport renders no guessed icon.
- `internal/web/static/js/sports-model.test.js` — `formatDetailStatus` ported
  from MediaStorm (soccer HT/ET/PEN and final penalty detection from period,
  detail or shootout scores; hockey P1–P3/intermission/OT/SO; cricket reported
  detail; college-football overtime; generic live/final/scheduled; stale suffix)
  and `buildEventAccessibilityLabel`/`formatGameResult`/`formatLiveSituation`
  spoken composition.
- `e2e/gate.spec.js` — expanded-feed paging/trimming, drive replay and
  perspective geometry, focus order into the docked preview, Escape closing and
  launcher focus restoration, re-activation by keyboard, no focus behind the
  full-screen player after handover, the sports HUD's back label and lifecycle
  status, stats-delay persistence/buffering copy, and the addon flow: identity on
  every gated request (ffprobe included), measured quality, docked preview with
  HUD, full-screen handover, source switching, a post-tune provider failure
  reported without a URL, and session cleanup after leaving the page. Alongside
  those: the live player's audio/subtitle menus appear only on a stream that
  publishes alternate renditions and switching reaches hls.js, while a
  single-rendition channel shows neither control. **Executed on 2026-10-25 via
  `node e2e/run-gate.mjs` against the built binary: 10 passed, 0 skipped; the
  final run used a populated guide.** The Docker image run remains blocked; see
  `evidence/gate-run.md` and `evidence/docker-gate.md`.

P7 audit tests added:

- `internal/web/static/js/sports-timezone.test.js` — the rail is three
  consecutive local days across midnight and a DST change, a start time is a
  clock time on the viewer's today and a dated time otherwise, an unusable
  timestamp is empty rather than NaN, and freshness compares instants. The whole
  suite passes under UTC, both US coasts, India, UTC+14, UTC−11 and a 30-minute
  DST zone.
- `internal/sports/sports_test.go` — `TestScoreboardDateAcceptsEveryViewerTimeZone`
  proves the server's four-day window accepts the local calendar day of every
  offset from UTC−11 to UTC+14 (a client one day ahead of UTC is not refused) and
  still rejects a date three UTC days out.
- Long-text audit: provider titles and participant names stay whole in the model
  and spoken label, and the card's non-matchup title is clamped to two lines with
  wrapping allowed, so a 400-character title cannot resize a rail card.

P4 tests added (first slice):

- `internal/api/sports_test.go` — `TestSportsEventStreamScopeAppliesToDiscoveryOnly`
  proves the configured group scope narrows automatic discovery, an unscoped
  server searches every visible channel, and an explicit search stays global.
- `internal/web/static/js/sports-watch-model.test.js` — `preferLastSource`
  ordering, absent memory and unchanged confidence tiers.
- `internal/web/static/js/sports-model.test.js` — `detailRefreshMs` cadence for
  live (120 s), final (600 s) and scheduled (60 s) events, and
  `searchResultGroups` preserving channel/EPG/event/team identity.
- `internal/api/sports_test.go` — team search results carry their competition,
  so a repeated provider team id across leagues stays unambiguous; hub search
  returns only channels that `MatchChannelByID` can actually resolve.
- `internal/livetv/livetv_test.go` — `TestGuideRetainsSevenDaySearchHorizon`
  keeps a four-day fixture, drops an eight-day programme and proves the search
  helper reads that retained window.

P3 tests added:

- `internal/sports/providers_test.go` — boxing card contract, CFL fixtures/teams/
  standings, race board weekend/circuit normalization, verified race statistic
  columns, MotoGP sessions/classification, F1 archive exact-session matching,
  cycling schedule/results, published cycling calendar and subscription, Giro
  classification, active-catalog-to-provider resolution and cycling
  unavailable-vs-stale state.
- `internal/sports/mma_detail_test.go` — fighter profiles and fight statistics
  from the core endpoints, per-fighter pairing, cache reuse, scheduled bouts
  without statistics and refusal of traversal or non-numeric identifiers.
- `internal/sports/cricket_discovery_test.go` — dropdown parsing (empty and
  non-numeric slugs dropped, IPL keeps its polled identity), 24 h cache, last
  good snapshot on provider failure and no promotion into the league catalog.
- `internal/api/sports_test.go` — race list, session classification, archive
  mode and honest unavailable archive; cycling/MotoGP/standings honesty; cricket
  discovery contract (`complete:false`, stale flag, candidates absent from
  `/api/sports/leagues`).
- `internal/web/static/js/sports-sport-model.test.js` — combat portraits (no
  MMA headshot path for boxing, team crests rejected), tale of the tape from
  provider profiles only, fight-card results and comparable bars.
- `internal/web/static/js/sports-model.test.js` — detail refresh cadence for
  live, final and scheduled events.

P2 tests added:

- `internal/db/sports_test.go` — preference revisioning/isolation, link CRUD
  ordering, team-scoped deletion, exact backup order validation, auto-links
  never replacing a manual primary and provenance retention.
- `internal/api/sports_test.go` — preference persistence, per-user isolation,
  stale-revision 409, strict schema rejection, admin read/write/authorization;
  link route authorization, missing team/channel, duplicate rejection, primary
  replacement, backup append, delete, auto-link preview/apply, unresolved links
  never carrying a playable id, and route registration returning 401.
- `internal/sports/match/links_test.go` — link self-healing across playlist ids,
  ambiguity refusal across sources, manual-search labels, auto-link confidence
  thresholds and existing-primary preservation.
- `internal/web/static/js/sports-preferences.test.js` — strict document and
  snapshot parsing, dedupe, school merge, pin/follow/team updates, legacy
  aliases, favorites and lane ranking.
- `internal/web/static/js/sports-appearance.test.js` — six presets, WCAG
  contrast on text/muted labels, card separation, readability normalization,
  data-attribute/class application and storage failure behaviour.

P1 tests added:

- `internal/sports/match/match_test.go` — manual search never claims a strong
  match, whole-token broadcast matching, lifecycle filtering, EPG promotion,
  matchup variants, grouping, explicit search confidence.
- `internal/sports/detail_test.go` — detail capability gating, zero vs missing,
  score history, shots, lineups, pregame.
- `internal/livetv/quality_test.go` — measured stream selection, adaptive
  detection, guide search split/cap/order, probe guards (unknown channel, busy,
  missing ffprobe, provider limit).
- `internal/api/sports_test.go` — search validation and contracts, manual stream
  search, quality route validation, stream-cache expiry/bounding, detail
  enrichment and capability flags, auth/admin behaviour.
- `internal/web/static/js/sports-watch-model.test.js` — watch decision, feed
  priority, probe identity, measured-resolution sorting, quality lines, error
  messages, progress copy.

P0 regression coverage remains green: scoreboard/catalog/date-window/cache,
signed-out requests (401), admin settings normalization, Live TV playback mode,
dated-cache TTLs and restart snapshot.

### Live deployment check

A real Wren TV binary was run against the public ESPN API (five leagues) and a
fixture M3U/XMLTV Live TV source:

- `/api/sports/hub` → 39 events; `/api/sports/status` healthy, no error.
- Live NBA event detail → 2 periods, 162 plays, 10 team comparisons, 19 player
  stat lines, 6 leaders, 57 basketball shots, 162 win-probability points and a
  162-point score history; capabilities `plays`/`stats`/`winProbability` true.
  A scheduled game correctly reported `false` capabilities and no invented plays.
- `/api/sports/search?q=nba` → 1 channel, 2 live EPG programmes, 8 events;
  `?q=Celtics` → 2 channels and 1 EPG programme; a short query → 400.
- Manual stream search `?q=NBA` → one `manual` group (`livetv:<channelId>`),
  never `strong`; automatic matching for a Nets/Cavaliers game returned no
  group rather than a city-name guess.
- `POST /api/sports/streams/quality` for a known channel returned measured
  1920 × 1080, 59.94 fps, H.264, 6 Mbps, stereo AAC/en, adaptive; repeating the
  check succeeded (slot released), an unknown channel returned 404 and a missing
  id returned 400. At that point the success path used a stub ffprobe; the
  browser gate now measures a real generated playlist channel end to end and
  records `640 × 360 · H264 · 25 fps · AAC mono` in
  `evidence/gate-run/quality-playlist.json`.
- Two identical stream requests returned identical bodies inside the 15 s cache.
- Anonymous search/quality → 401.
- Live TV regression: `POST /api/livetv/channels/{id}/play` returned an HLS
  session and `DELETE /api/sessions/{sid}` returned 204.
- P0 restart, admin settings and cache behaviour re-checked.

### P2 live deployment check

A fresh binary was run with the fixture M3U/XMLTV playlist and the public ESPN
provider. Against that server:

- `GET /api/sports/preferences` returned revision 0 and an empty document; a
  save with a basketball pin, `premier-league` follow and `nba:2` team follow
  returned revision 1, reloaded identically, and a replay of revision 0
  returned 409. An unknown field returned 400. The admin editor read the same
  document for the signed-in admin.
- Team suggestions for `NBA TV` returned the channel with a `possible` tier and
  `Manual text match`, never a strong automatic claim. Setting a primary and
  adding a backup both resolved against the playlist; a duplicate backup
  returned 409; the batched linked lookup returned both, then deletion returned
  204 and left the primary alone. Auto-link preview skipped the team that now
  had a primary.
- `/manage/team-channels`, `/js/team-channels.js`, `/js/sports-preferences.js`,
  `/js/sports-appearance.js` and `/sports.css` were all served, and the stylesheet
  contained the theme definitions.

### Not verified here

- Docker image: the Docker CLI, daemon and every alternative runtime are absent,
  and this process cannot create the namespaces a daemon needs (`unshare -Ur` and
  `unshare -n` both return `Operation not permitted`; `CapEff` lacks
  `CAP_SYS_ADMIN`). The exact `CGO_ENABLED=0` flags were used and the same binary
  passed the browser gate twice, but the image itself has not been built or run,
  so **no image tag or digest exists**. No Dockerfile change is needed because
  all static assets are embedded. The failed attempt and the image-contract
  preflight are recorded in `evidence/docker-gate.md` and
  `evidence/docker-image-contract.txt`.
- Real-browser gate against the container image: the mandatory release gate is
  **not met**. The ten-test Playwright run against the built binary passed
  repeatedly, including addon playback, measured quality, the sports HUD and the
  live track menus, which is
  real-browser evidence for a real server deployment but not for the image, its
  healthcheck or a container network. Until the image run is recorded on a
  Docker-capable host, P5/P6 stay blocked.
- Container-specific paths: the image's Jellyfin ffmpeg build, its `/config`
  volume and the container healthcheck were not executed. The preflight checked
  the equivalent binary/env/volume behaviour on the host only.
- Native apps: P5/P6 have not started. **Android TV's Live TV list now reads
the same `/api/livetv/channels` listing the web page does** — channels, addon
events and providers — so a Stremio source's entries appear alongside IPTV
channels and play through the server's automatic addon playback. Those Kotlin
changes (`apps/android/core` and `apps/android/tv`) are **not compiled here**:
the environment has no JDK, Gradle or Android SDK, so the native change is
unverified by a build and by a device run. The installed client predates it, so
**Android TV needs a rebuild and reinstall** before a Stremio provider shows up;
a server-only update cannot make the old client render entries it has no model
for. The server-side work in this pass (manifest reuse, `429`/`Retry-After`
handling, session/kind cleanup, keeping a provider's last known entries when its
catalog read fails temporarily) is Go-only and covered by
`internal/livetv/ratelimit_test.go`, `internal/api/handlers_livetv_auto_test.go`
and the four-test browser run recorded in `e2e/README.md`.

---

## 4. Deployment changes

1. **Config** — optional sports block; no action needed for existing installs
   (`enabled` defaults true, empty `leagues` means the whole catalog):

   ```yaml
   sports:
     enabled: true
     leagues: ["nfl", "nba", "mlb", "nhl", "soccer-eng.1"]
     favorite_teams: []
     sources: []
     groups: []
   ```

   `WRENTV_SPORTS=false` disables the hub. Quality checks reuse
   `transcode.ffprobe_path`; without a working ffprobe the endpoint answers
   `Quality check unavailable` and playback is unaffected.

2. **Database** — appended migrations create `sports_teams`,
   `sports_team_channel_links` and `sports_preferences`; they run automatically
   on start. Nothing is destructive, and deleting a team cascades its links.
3. **Cache** — `DataDir/cache/sports/` (scoreboard snapshot and cached logos).
4. **Network** — the server calls `site.api.espn.com` every 90 s (30 s cached
   per league while live). No key or account is required. A quality check opens
   one short-lived connection to the provider channel and respects
   `live_tv.max_streams`.
5. **No native or API breaking change.** `/api/server` gains `sports`; existing
   fields are untouched. New preference/link routes are additive. Personalization
   is per authenticated user; theme, readability and spoiler choices stay
   device-local.

---

## 5. Remaining work (next milestones)

| Milestone | Depends on | Main items still open |
|---|---|---|
| P2 | P1 | **Complete.** Residual gaps moved: hub search across Live TV (B9 → P4), client 10-minute structural feed cache (B15 → P7), arbitrary text scale and TV overscan sizing (B12 → P5/P6) |
| P3 | P1 | **Complete.** Secondary providers (boxing, CFL, F1 archive, MotoGP, cycling organisers), racing/cycling/combat screens, cricket series discovery, MMA profiles, catalog resolution and sport-specific contracts are implemented and tested. Residual: original sport glyphs (D21) and sport-specific sticky-header formatter (E3), both cosmetic and scheduled with P7 |
| P4 | P1 | **Web complete; container-image gate blocked.** Shipped: detail auto-refresh (E2), discovery source/group scope (F10), last-source memory (F11), hub server search (B9), addon discovery/probe/play plus full-screen playback and allowlisted request headers (F9), preview focus (I3), drive replay (I4), perspective field (I5), stats delay (E12/I6), docked HUD scoreline and full-player sports identity/lifecycle (I1), and audio/subtitle track menus that exist only when the stream publishes a choice (I7). A real-browser run against the built binary passed all 10 gate tests, including addon identity, measured quality, playback, switching, failure, cleanup, replay geometry, feed paging and track switching. The Docker image run is blocked on a runtime, not on code. Still open: multiscreen/multiview/recording (no platform feature) |
| P5 | P1–P4 | iOS, iPadOS, macOS and tvOS slices |
| P6 | P1–P4 | Android phone/tablet, Android TV and Windows |
| P7 | all | **Nearly complete.** Done: structural feed cache with explicit invalidation (B15), spoken card labels (D22), original sport glyphs (D21), sport-aware sticky-header formatter (E3), expanded-feed DOM trimming (B16, browser-verified), long-text clamping (`overflow-wrap: anywhere` plus a two-line clamp on card titles), timezone audit (web suite under six zones plus a Go window test) and the provider-failure copy audit, whose browser assertion is now mandatory and deterministic (the broken addon is selected by name, the sanitized error is required, and the released session is polled) rather than conditional. Open: full inventory sign-off and the native-only rows |

Recorded decisions from P0–P4:

- The catalog and coverage evidence are embedded and versioned exactly as in
  MediaStorm; inactive entries stay documented but are never polled.
- Non-ESPN providers answer with their own contracts (boxing fight cards, CFL
  fixtures, organizer cycling stages, MotoGP sessions) and report
  `unavailable`/`stale` per feed instead of a fabricated matchup. A lane is only
  empty when that provider has nothing in its window.
- Channel stream URLs stay server-side. Sports stream matches carry
  `livetv:<channelId>`; playback always goes through Wren TV's live session
  endpoint, so no provider credential reaches a client.
- Manual channel search is an explicit choice, not evidence. It is labelled
  `manual`, never `strong`, and never auto-plays a single result.
- Measured quality is separate from label-derived quality; an unlabelled feed is
  not called HD, and adaptive streams never claim a fixed playback resolution.
- EPG retention and sports search now cover seven days ahead (2 h back), so a
  weekend fixture four days out is searchable. Memory grows only with what the
  provider actually publishes, and short feeds stay short; the seven-day window
  is guarded by a guide test.
- Recording (F12) remains absent until Wren TV has DVR support; no simulated
  control was added.
- Sports preferences use MediaStorm's document unchanged, one per authenticated
  Wren TV user. There is no profile layer, so "profile" means "user" here.
  Writes require the loaded revision; a stale client gets 409 instead of
  overwriting another device. Admin editing is opt-in per request and uses the
  same validation and revision rule.
- Team-channel links are resolved against the current playlist on every read.
  A link that matches several channels without a stored source or name
  discriminator stays unresolved, so a provider reshuffle never silently
  selects another team's feed. Manual corrections replace automatic links and
  clear their provenance; automatic linking never replaces a manual primary.
- The configured source/group scope (and its per-league override) is applied to
  automatic team linking and automatic event-stream discovery. An explicit
  channel search deliberately bypasses the scope, because it is the operator
  asking for a specific feed by name.
- Event-detail refresh lives on the event page: 120 s live, 600 s final and 60 s
  scheduled (MediaStorm's pregame poll). It pauses while the tab is hidden or the
  Watch panel is open and stops when the page is left, so a background tab never
  keeps polling providers.
- The last working source per event is remembered on the device and offered
  first with a `Last used` badge; a measured resolution sort still takes
  precedence, and the hint never changes a match's confidence tier.
- Theme, readability and spoiler choices are device-local. Themes are scoped to
  the Sports area so Wren TV's app-wide palette is unchanged.
- The stats delay keeps MediaStorm's buffer and preference semantics but is
  keyed by server + account rather than server + profile + source, because Wren
  TV has one profile per account. A source change still clears the receipt
  history, so a delayed score can never belong to the previous feed.
- The client feed cache is write-through and never answers a request on its own:
  it only paints a remembered day while that day's request is in flight. It is
  cleared when the hub is entered, refreshed explicitly, retried after an error,
  or when the calendar day changes (part of the key).
- The docked preview HUD reads the event page's delayed snapshot. It performs no
  request of its own, so a closed preview or a changed event cannot leave a
  scoreline behind.
- The full player's sports HUD is fed the same event identity, so it cannot
  disagree with the page. Its lifecycle label follows the media element
  (CONNECTING until `playing`, OFFLINE on failure) instead of the tune response,
  and the back control is named "Back to event" because that is where it goes.
  Audio and subtitle menus are the one HUD control added rather than omitted,
  because the player now has real renditions behind them: `live-tracks.js`
  shows Audio only above one rendition and Subtitles as off plus every published
  one, and the selection is applied to hls.js or the element's own track lists.
  Nothing is shown for a single-rendition stream (including remuxed MPEG-TS,
  whose pipeline carries one AAC track), so no stand-in control lies. Recording
  and multiscreen stay absent: the platform has no such capability.
- Expanded-feed paging is the DOM bound MediaStorm's expanded feed implies: at
  most twelve cards are mounted, the page is swapped rather than appended, and
  collapsing replaces the page with the lane rails. Rails themselves keep their
  full list, as MediaStorm's hub scroll does; there is no invented "more" page
  inside a horizontally scrolling rail.
- Date queries accept the viewer's local calendar day inside a four-day UTC
  window. Yesterday/today/tomorrow can differ from UTC by one day at UTC±14, and
  the ±2 day window covers that without letting a client walk a provider's
  schedule; both the web rail and the server window are tested around those
  boundaries.
- Provider titles and names are never truncated in the model or the spoken
  label; only the card's visual title is clamped to two lines, with wrapping
  allowed so a long single token cannot widen a rail card.
- Wren TV's web shell has no spatial D-pad layer (A7 stays partial). Preview
  focus therefore uses the browser's native focus order plus `Escape`: opening
  focuses Stop, the launcher gets focus back on close, and the full player keeps
  focus inside its own overlay. Android TV/remote focus is unchanged until the
  native clients exist.

---

## 6. Known gaps against MediaStorm (honest list)

Every item below is scheduled, not dropped. P3 and the web slice of P4 are
complete; the container gate and the native slices are what remain.

1. **Docker image gate** — **blocked, not passed.** No Docker/podman/buildah/
   nerdctl, no socket, and `unshare -Ur`/`unshare -n`/`unshare -m` are refused,
   so the image has never been built or started on this host; no digest exists. The built
   binary passes the same ten-test suite, and the image-contract preflight
   proves the static binary serves the embedded assets and honours the image's
   runtime variables, but neither is the container gate. Commands and the
   evidence to record are in `evidence/docker-gate.md`.
2. **Native clients (P5/P6)** — the iOS/iPadOS/macOS/tvOS, Android/Android TV
   and Windows apps contain no sports code yet: no tab, hub, detail stack,
   watch flow, preferences or admin pages. This is the largest remaining gap and
   needs one slice per platform against the same server contracts. **Not
   started by design**: P5/P6 wait for the Docker image gate above.
3. **Player HUD controls MediaStorm has that Wren TV does not (I7)** — audio and
   subtitle selection is now ported and browser-verified; a control appears only
   when the stream publishes more than one audio rendition or any subtitle
   rendition, and a remuxed MPEG-TS channel offers neither. Multiscreen,
   multiview and recording (F12) remain absent because the platform has no such
   feature. Live playback has no Pause, which matches MediaStorm.
4. **Sport-specific detail gaps** — provider timeouts (D4), possession/bonus
   indicators (D5), hockey strength/shot situation (D7), a soccer pitch layout
   (D8) and a separate baseball linescore (D6) are not shown because the
   provider data or a faithful design is missing; they stay listed as partial.
5. **Audits and sign-off (P7)** — the long-text clamp, timezone audit and
   provider-failure copy audit are done and recorded in §3. What remains is the
   final full-inventory sign-off and the native-only rows (B1–B3, B12, D21/D22
   native, E11/E13), which cannot close before P5/P6.

No row above is claimed as done; the inventory tables mark exactly what has
code and tests behind it.
