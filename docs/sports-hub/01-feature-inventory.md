# Sports Hub — feature inventory and Wren TV gap analysis

Audit basis: `mediastorm-frontend` (sports UI and behaviours), `mediastorm` backend (ingestion, matching,
persistence, preferences) and Wren TV at the time of writing. "Wren TV today" describes the existing codebase,
not a proposed design; the port target is in the migration plan.

Status key: **Absent** (nothing comparable), **Partial** (building blocks exist), **Present** (equivalent
capability already exists and is reused).

---

## A. Entry points and navigation

| # | Feature | MediaStorm implementation | Behaviour to preserve | Wren TV today | Port target |
|---|---|---|---|---|---|
| A1 | Sports tab in app navigation | `app/(drawer)/_layout.tsx`, `app/(drawer)/sports.tsx`; `config/settings.go` `NavigationTabVisibilityIncludesSports` migration | Top-level Sports destination, hidden when the server disables sports; nav visibility migration for older installs | Absent | Sports destination in web sidebar and every native app, shown when the server reports sports enabled |
| A2 | Hub route | `app/(drawer)/sports.tsx` (287 lines) | Authenticated route owning feed cache, foreground/menu suspension, route selection memory, TV key forwarding | Absent | `/sports` web route; `Sports` tab/stack in native apps |
| A3 | Event detail route | `app/sports/[eventId].tsx` (532 lines) | Deep-linkable event id, optional `watch` param, identity-scoped snapshot, retry/error, spoiler provider | Absent | `/sports/event/{id}` web route; native detail stack route |
| A4 | Race detail route | `app/sports/race/[eventId].tsx` (83 lines) | Race session detail with playback surface | Absent | `/sports/race/{sessionId}` |
| A5 | Cycling detail route | `app/sports/cycling/[raceId].tsx` (90 lines) | Race + optional stage selection | Absent | `/sports/cycling/{raceId}[/{stageId}]` |
| A6 | Precedent for local preview routes | `app/sports/*.local.tsx` | Design/demo galleries are excluded from production navigation | Not applicable (no sports routes) | Keep demo data as test fixtures only; not shipped as routes |
| A7 | Back/Select remote semantics | `SportsTVFocus.tsx`, `useSportsHubKeyboard.ts`, `useSportsRouteSelection.ts` | One Select per transition; hidden hub cannot navigate; back closes modal before page | Partial (native apps handle focus; web has no D-pad layer) | Web spatial/keyboard navigation for cards, dialogs and player; native focus follows platform conventions |

## B. Hub experience

| # | Feature | MediaStorm implementation | Behaviour to preserve | Wren TV today | Port target |
|---|---|---|---|---|---|
| B1 | TV / large-screen hub | `components/sports/SportsHubTV.tsx` (1,382 lines) | Date rail, status filters, league lanes, featured event, paginated expanded feed, pinned sports, stream picker hooks, appearance, spoilers | Absent | Web hub layout + tvOS/Android TV/Windows TV presentation |
| B2 | Mobile hub | `components/sports/SportsMobileHub.tsx` (444 lines) | `SectionList` by league, compact rows, search field, filter sheet, pinned order | Absent | Responsive web layout; phone native screens |
| B3 | Card model shared by hub | `SportsHubShowcase.tsx` (1,153 lines) | `HubCard` = match \| racing \| cycling, featured/dense variants, availability flag, start labels | Absent | Shared Go DTO + per-client card model matching this shape |
| B4 | Date selection | `sports-model.ts` (`SportsDate`), `sports-backend-source.ts`, `sports-scoreboard-dates.ts` | Yesterday/today/tomorrow; server capability may limit to today; provider-date overlap for cross-midnight/long events | Absent | Same dates and capability gate; client computes local day, server accepts provider dates |
| B5 | League lanes and ordering | `SportsHubTV`, `buildLeagueLanes`, `sports-availability.ts` | One lane per enabled league in catalog order, featured event removed from lanes | Absent | Same |
| B6 | Sport / league / status filters | `SportsFilterChoicePage.tsx`, `SportsMobileFilters.tsx`, `SportsTabRail.tsx` | Status tabs (live/results/upcoming/all), sport filters derived from enabled catalog, league focus | Absent | Same filter set on web and native |
| B7 | Pinned sports ordering | `sports-pins.ts` | 20 supported sport pins; pinned sports rank first while all data stays reachable; unsupported pins rejected | Absent | Same pin list persisted per user |
| B8 | Competition/team/athlete/school follows | `sports-follows.ts`, `framework/preferences.ts` | Follow IDs ordered ahead of non-followed events; school scope (all programmes or selected) | Absent | Same preference document, per Wren TV user |
| B9 | Hub search | `SportsHubTV` search + backend `/live/search` | Query ≥2 chars; OR-of-terms across channel name/group/tvg-name, EPG titles and descriptions, teams, events; capped result sets | Absent | `/api/sports/search` combining same sources |
| B10 | Spoiler mode | `components/sports/SportsSpoilerMode.tsx` | Device-local toggle hides scores/results on cards; opening detail reveals; waits for stored value to avoid score flash | Absent | Per-device toggle on web (localStorage) and native (platform storage), same reveal rules |
| B11 | Appearance themes | `features/sports/sports-appearance.ts`, `SportsAppearanceSettings.tsx` | Six themes (mediastorm, midnight, graphite, evergreen, aubergine, warm-slate) applied to cards/panels/accent | Present design system, single theme | Sports area theming integrated with Wren TV tokens; the six palettes are the parity target |
| B12 | Readability and text scale | `SportsReadableText.tsx`, `sports-design.ts`, `sports-tv-scale.ts` | User text size, high-contrast text on surfaces, TV overscan-aware sizing | Partial (global sizes in native apps) | Same controls in Sports settings |
| B13 | Staleness / freshness display | `SportsFeedFreshness.tsx`, `sports-model.ts` | "Feed checked …" and "Scores may be delayed"; per-league stale/unavailable flags from server | Absent | Server returns `updatedAt`, `stale`, per-league state; clients render same copy |
| B14 | Refresh cadence | `sports-refresh.ts`, `useSportsForeground.ts` | 30 s for stale/live/unavailable/empty, 60 s for today with unstarted events, 300 s otherwise; pause when backgrounded or menu open | Absent | Same schedule in web and native; server polling independent |
| B15 | Feed cache | `createSportsFeedCache` | Three dates max, 10-minute TTL keyed by date identity, structural sharing to avoid re-render | Present (`js/sports-cache.js`: date+calendar-day key, 10-minute TTL, 3 entries, structural sharing, explicit `clear()` on hub entry/refresh/retry) | Same client cache semantics |
| B16 | Large-feed pagination | `SportsHubTV` expanded feed | Max 12 cards mounted per page; previous/next controls; page state scoped to date/filter and clamped when feed shrinks | Done (web, browser-verified by the gate's "expanded feed mounts one page and swaps it": ≤12 `.sports-card` nodes, `Next` changes the first card, `Back to lanes` removes the feed entirely; re-run 2026-10-25) | Same bound in web (DOM) and native (lazy lists) |
| B17 | Empty/error/recovery states | `SportsFeedState.tsx`, `SportsSearchProgress.tsx` | Disabled, stale, unavailable, retry and disabled-by-server states, never blank screens | Partial (generic empty states exist) | Sports-specific states including per-source progress |
| B18 | Multiscreen | `app/multiscreen.tsx` integration | Sports player participates in multiscreen layouts | Absent (no multiscreen) | Sports player as a multiscreen tile wherever Wren TV supports multiscreen |

## C. Catalog and ingestion

| # | Feature | MediaStorm implementation | Behaviour to preserve | Wren TV today | Port target |
|---|---|---|---|---|---|
| C1 | League catalog with capabilities | `services/sports/service.go` `LeagueCatalog`, `coverage_catalog.go` + `.json` | 55 explicit leagues plus dynamic `espn:{sport}:{league}` entries; each carries provider, application sport, college, adapter, event kind, capabilities, implementation status (`validated`/`limited`), aliases, coverage note, supports-teams | Absent | Same catalog shape served by `GET /api/sports/leagues` |
| C2 | Catalog acceptance rules | `features/sports/sports-runtime-catalog.ts` | Client accepts only implemented, schedule-capable rows; unknown identity rejected; second backend cannot invalidate retained cards | Absent | Server is authoritative; clients still validate as defence in depth |
| C3 | ESPN scoreboard ingestion | `services/sports/espn.go`, `extended_scoreboard.go`, `dates.go`, `service.go` | Site API scoreboards per league, date queries with provider-date overlap, coverage merge, partial-failure retention, concurrent fetch with cap 4 | Absent | `internal/sports` ingest package with same contracts |
| C4 | Secondary providers | `boxing.go` (TheSportsDB), `cfl.go` (CFL JSON), `f1_archive.go` (official archive), `aso_guides.go` / `giro.go` (organiser HTML), `motogp.go` / `motogp_enrichment.go`, `racing.go`, `cycling*.go`, `cricket*.go` | Each provider keeps its own limits and caching; no paid provider | Absent | Same providers adapted to Go package layout |
| C5 | Provider HTTP layer | `services/sports/provider_http.go` | Bounded body (32 MB), singleflight per URL, per-host concurrency slot, response cache with TTL, retry-after handling, gzip | Absent (Wren TV has ad-hoc clients) | Shared `internal/sports/provider` HTTP cache reused by all sports providers |
| C6 | Scoreboard cache and persistence | `service.go` `saveToDisk` / `loadFromDisk` under cache/sports | In-memory per league + disk snapshot so restart shows last scores; transient failures never erase data | Absent | SQLite or disk cache in `DataDir/cache/sports` with same no-erase rule |
| C7 | Detail enrichment | `services/sports/detail.go` + per-sport files | One `SportsGameDetail` with periods, plays, comparisons, leaders, player stats, lineups, standings, pregame, shots, drives, innings, scorecards, win probability, capabilities; bounded per-event cache | Absent | Same DTO and enrichment gating |
| C8 | Team catalog | `service.go` `EnsureTeamCatalog`, `fetchLeagueTeams`, `team_detail.go`, `team_context.go` | Full ESPN team catalog per league, scoreboard teams as fallback, persisted identities for linking | Absent | `sports_teams` table + periodic sync |
| C9 | Status and health | `GetStatus` (`enabled`, `dateQueries`, `lastRefresh`, `lastError`, `refreshing`, `gameCount`) | Clients gate the hub on status; admin sees health | Absent | `GET /api/sports/status` with same fields |
| C10 | Manual refresh | `POST /sports/refresh` (admin) | Force refresh; master-only in MediaStorm | Absent | Admin-only `POST /api/sports/refresh` |
| C11 | Logo proxy/cache | `GET /sports/logo?url=…`, `ClearLogoCache` | Only http(s) images fetched, cached on disk, admin clear | Partial (`internal/images` proxies TMDB only) | Extend image cache for provider logos with allowlist + admin clear |
| C12 | Background scheduling | `services/sports` ticker + `handlers/sports_background.go` | 90 s score/team/link cycle, re-checks settings between stages, stops when disabled | Absent | Same cycle started with the server, disabled cleanly |

## D. Event cards and sport-specific presentation

| # | Feature | MediaStorm implementation | Behaviour to preserve | Wren TV today | Port target |
|---|---|---|---|---|---|
| D1 | Generic matchup card | `SportsGameCard.tsx`, `SportsCardFrame/Header/Heading/StatusPill/Accent`, `SportsTeamMark`, `SportsStatusBadge` | Away/home, logos, colors, records, seeds/ranks, live period/clock, status pill, broadcast string, venue, watch affordance | Absent | Shared card components per client |
| D2 | Live availability and context | `SportsMatchAvailability.tsx`, `SportsMatchContext.tsx`, `sports-match-confidence.ts` | Event state explained independently of analysis; venue/neutral site; conference/record context | Absent | Same |
| D3 | Lead history / scoring graph | `SportsLeadHistory.tsx`, `scoring_history.go` | Complete point-by-point lead chart for completed games | Absent | SVG chart in web, Canvas/SwiftUI/Compose equivalents |
| D4 | American football | `SportsFootballContent.tsx` (912 lines), `football-presentation.ts`, `FootballFieldMarkings.tsx` | Drives, down/distance/possession/timeouts, field position, play geometry | Partial (web): drives, down/distance/possession, projected field, replay traces and first-down line; provider timeouts are not shown | Same |
| D5 | Basketball | `SportsBasketballLiveCard.tsx` (615), `SportsBasketballOverviewStats.tsx`, `basketball-court.ts`, shot data | Score, possession/bonus, quarter breakdown, shot chart, win probability | Absent | Same |
| D6 | Baseball | `SportsBaseballDetail.tsx` (343), `sports-baseball-state.ts` | Innings, linescore, count/outs/bases situation, diamond graphic | Absent | Same |
| D7 | Ice hockey | `SportsPeriodBreakdown.tsx`, shot map (`shots`), strength/shots situation | Period scores, OT/SO/intermission labels, shot chart | Absent | Same |
| D8 | Soccer | `SportsSoccerPitch.tsx`, `sports-presentation.ts` | Half/ET/penalty labels, lineups on pitch, match comparisons | Absent | Same |
| D9 | Tennis | `SportsTennisCard.tsx`, `SportsTennisSetScores.tsx`, `sports-tennis.ts` | Set-by-set scores, tiebreaks, best-of context | Absent | Same |
| D10 | Rugby union/league | `SportsRugbyHeader.tsx`, `SportsRugbyTimeline.tsx`, `sports-rugby.ts` | Scoring timeline and possession/territory comparisons | Absent | Same |
| D11 | Cricket | `SportsCricketOverview.tsx`, `SportsCricketScorecards.tsx`, `cricket_detail.go` | Innings, score strings, batting/bowling scorecards, result summary | Absent | Same |
| D12 | Golf | `SportsGolfOverview.tsx`, `leaderboard` in detail | Tournament leaderboard to par, round and hole strokes | Absent | Same |
| D13 | Combat (MMA/boxing) | `SportsCombatOverview.tsx`, `SportsCombatHeader.tsx`, `SportsCombatBoutRow.tsx`, `mma_detail.go`, `boxing.go` | Fight card order, bout state, fighter profiles and stats | Absent | Same |
| D14 | Racing | `SportsRacingHub.tsx`, `SportsRaceTable.tsx`, `SportsRaceComparison.tsx`, `SportsRaceDriverCard.tsx`, `sports-racing.ts` | Race board by series, session list, classification, gaps/tyres, circuit map | Absent | Same |
| D15 | F1 archive analysis | `SportsF1Analysis.tsx`, `SportsCircuitMap.tsx`, `services/sports/f1_archive.go`, `circuits.go` | Archived session analysis (tyres, stints, timing, weather) clearly marked archive, not live telemetry | Absent | Same with attribution |
| D16 | MotoGP | `SportsMotoGPDetails.tsx`, `motogp.go`, `motogp_enrichment.go` | Standings, circuit geometry, session detail | Absent | Same |
| D17 | Cycling | `SportsCyclingHub.tsx`, `SportsCyclingDetailTV.tsx`, `sports-cycling.ts`, `cycling*.go` | Race calendar, stage profiles/results/classifications, organiser guides | Absent | Same |
| D18 | Standings and leaders | `SportsLeagueStandings.tsx`, `SportsLeagueOverview.tsx`, `SportsClassificationLeaders.tsx`, `SportsLeaderComparison.tsx`, `standings.go` | League tables (conference/division), classification leaders, record enrichment for cards | Absent | Same |
| D19 | Editorial / pregame content | `SportsEditorial.tsx` (747), `SportsPregamePreview.tsx`, `SportsMobilePregame.tsx`, `pregame.go` | Season context, records, recent form, leaders, probables, previous meeting | Absent | Same |
| D20 | Player presentation | `SportsPlayerStats.tsx`, `SportsPlayerHUD.tsx`, `SportsPlayerPortrait.tsx`, `player_stats.go`, `player_headshot.go` | Box-score categories, headshots, per-player detail | Absent | Same, provider headshots proxied |
| D21 | Event symbols and artwork | `SportsEventSymbol.tsx`, `sports-league-vectors.ts`, `sports-team-colors.ts`, `sports-card-theme.ts` | Original vector sport glyphs, team colors, no unlicensed athlete art | Present (`js/sports-glyphs.js`: original 24×24 stroke glyphs for 13 sports plus provider aliases) | Reuse Wren TV icon conventions, add sport glyphs |
| D22 | Accessibility labels | `sports-model.ts` `buildEventAccessibilityLabel` | Full spoken score/status/result/watch availability per card | Present (`buildEventAccessibilityLabel` on every hub card and featured card; watch availability is not claimed because the hub does not know it) | Same label composition |

## E. Event detail

| # | Feature | MediaStorm implementation | Behaviour to preserve | Wren TV today | Port target |
|---|---|---|---|---|---|
| E1 | Detail tabs and capability gating | `SportsGameDetailTV.tsx` (1,553), `app/sports/[eventId].tsx` | Overview, Box score, Play-by-play, Lineups, Standings; tabs shown only when capability true; unknown modules stay hidden | Absent | Same tabs and gating on web/native |
| E2 | Detail refresh | `SportsEventContext.tsx` (514), `useSportsDetailIdentity.ts` | Independent detail refresh; live 120 s / final 600 s freshness; stale detail cannot override a fresh feed silently | Absent | Same |
| E3 | Sticky score header | `formatDetailStatus` | Sport-aware status strings (NHL periods/OT/SO, soccer halves/ET/pens, cricket result, college OT) | Present (`formatDetailStatus`: NHL, soccer, cricket, college OT, generic live/final/scheduled, stale suffix) | Same formatter shared server-side or ported to each client |
| E4 | Play-by-play and drives | `SportsPeriodBreakdown`, `SportsFootballContent`, `football-replay.ts`, `SportsDriveExplorer.tsx` | Chronological plays, scoring flags, drive reconstruction with field geometry and replay | Partial (web, browser-verified): plays, scoring flags, drives, replay geometry and previous-play stepping; drives are not grouped into a separate explorer view | Same |
| E5 | Box score / team stats | `SportsPlayerStats`, `SportsBasketballOverviewStats`, comparisons | Per-player categories, team comparisons, zero vs missing distinctions | Absent | Same |
| E6 | Lineups | `lineups` in detail, soccer pitch, substitution flags | Starters, positions, shirts, subbed in/out | Absent | Same |
| E7 | Standings tab | `SportsLeagueStandings` inside detail | Same table component as hub, sport-aware columns | Absent | Same |
| E8 | Win probability | `winProbability` series, chart | Live/finished probability graph; hidden when unsupported | Absent | Same |
| E9 | Score history | `SportsScoreHistory`, `SportsLeadHistory` | Complete scoring sequence with period/clock | Absent | Same |
| E10 | Tournament context | `tournamentContext` (soccer only) | Literal provider label, never implied bracket progression | Absent | Same |
| E11 | Docked / preview playback | `live-dashboard/SportsLivePlayerSurface.tsx` (397), `useSportsPreviewFocus.ts`, `SportsLiveDashboard.tsx` (338) | Play the matched channel in-page while detail stays visible; expand to fullscreen; back returns to detail | Partial (web player is a full-screen overlay; live player exists in native apps) | Docked sports player on web; native follows existing live-player conventions |
| E12 | Live stats delay | `stats-delay-preference.ts`, `useDelayedSportsSnapshot.ts`, `SportsStatsDelayControl.tsx` | Delay displayed stats to match the stream's latency; persisted per device | Present web (`js/sports-stats-buffer.js`, `js/sports-stats-delay.js`, event-page `Sync stats` control: 0/15/30/60/90/120 s, persisted per server+account) | Same |
| E13 | Playback orientation | `useSportsPlayerOrientation.ts` | Mobile landscape/fullscreen transitions with correct callbacks | Partial (native players handle orientation) | Preserve native behaviour and test |

## F. Watch flow and streams

| # | Feature | MediaStorm implementation | Behaviour to preserve | Wren TV today | Port target |
|---|---|---|---|---|---|
| F1 | Stream discovery endpoint | `GET /sports/game/{id}/streams?profileId&scope&kind&parentId&broadcast` | Returns flat matches + grouped feeds + per-source statuses | Absent | `GET /api/sports/events/{id}/streams` with same grouping |
| F2 | Channel matching | `handlers/sports_matcher.go` (834 lines) | Token-aware matching on team location/nickname/abbrev, broadcast/network names, matchup phrases, EPG title/description; negative tokens; league-scoped aliases; confidence tiers strong/possible/weak; reasons and matched terms | Absent | Port matcher into Wren TV, against Wren TV `livetv.Channel` fields |
| F3 | EPG promotion | `matchGameToChannels` + `GetScheduleMultiple` | Current and next programme can promote a channel; broadcast filter; lifecycle state (upcoming/ended) | Partial (Wren TV has now/next and schedule; `match_test.go` covers promotion, and from 2026-10-25 the browser gate runs against a populated XMLTV guide — 300 channels, two programmes each — so guide-backed matching is exercised end to end instead of on an empty guide) | Same using `livetv.Service.Schedule/Channels` |
| F4 | Grouping and alternatives | `models.SportsStreamGroup`, `sports-watch.ts` | Equivalent playlist entries collapse into one group; every distinct source remains an explicit alternative | Absent | Same |
| F5 | Watch decision | `getSportsWatchDecision` | Strong single feed plays directly; otherwise chooser; weak feeds are not offered as credible | Absent | Same |
| F6 | Feed ordering | `sportsFeedPriority` | Live-labelled feeds first, unknown next, upcoming/scheduled last | Absent | Same |
| F7 | Reported quality | `handlers/sports_stream_quality.go` | Parse only explicit labels (resolution, bitrate); conflicting labels use the lower claim; deterministic ordering | Absent | Same parser on channel/stream names |
| F8 | Quality probing | `/video/live/quality` + `useSportsQualityProbe.ts` | One connection at a time, per-feed results committed individually, cancel keeps completed results, re-check option | Partial (Wren TV probes media files) | Add live quality probe endpoint and the same client behaviour |
| F9 | Progressive addon search | `sports-stream-options.ts`, `expandSportsSearch` | Direct-playlist matches first; addon stream URLs resolved in batches (4 concurrent, 30 s budget), progress and per-source cooldown; partial results retained | Partial (streaming addons exist, no sports search) | Same expansion with Wren TV streaming addons |
| F10 | Source/category scope | `config.SportsSettings` defaults + per-league overrides; `sportsDiscoveryRequest` | Limit automatic discovery to selected Live TV sources/groups; `scope=all` bypasses | Absent | Config + query support |
| F11 | Stream failure and recovery | `SportsStreamPicker.tsx` (622), `SportsStreamUnavailable.tsx` (174), `sports-stream-memory.ts` | Keep other feeds selectable when one fails; remember last source; explicit unavailable screen with search action | Partial (native live player handles channel failover) | Same |
| F12 | Recording options | `SportsRecordingOptions.tsx` | Offer recording of the matched live channel/programme where the server supports it | Partial (Wren TV has live sessions, no DVR recording) | Expose only if/when Wren TV supports recording; otherwise omit the control (no fake affordance) |
| F13 | Legacy flat stream list | `streams` alongside `groups` in response | Older clients still get a flat list | Absent | Return both, same as source |
| F14 | Stream caching | `sportsStreamCache` 15 s, max 32 | Short-lived per-event cache | Absent | Same server-side cache keyed by event/scope |

## G. Teams and channel links ("Manage Team Channels")

| # | Feature | MediaStorm implementation | Behaviour to preserve | Wren TV today | Port target |
|---|---|---|---|---|---|
| G1 | Team identity persistence | `sports_teams` (migration 060/061), `SyncTeams`, `SyncTeamsFromGames` | Stable `{league}:{espnTeamId}` identity, location/nickname, logo; team rename does not orphan links | Absent | `sports_teams` table with same key |
| G2 | Primary + backup links | `sports_team_channel_links` (060/062), `SetPrimaryLink`, `AddBackupLink`, `ReorderBackups`, `DeleteLink` | One primary, ordered backups, denormalized channel snapshot, provenance (auto-linked, confidence, reason), timestamps | Absent | Same table + CRUD endpoints |
| G3 | Link resolution at read time | `resolveChannelSet`, `markResolved` | Link reports `resolved` by re-matching tvg-id/name against current playlist; survives provider ID churn | Absent | Same using `livetv` channel list |
| G4 | Channel suggestions | `GetChannelSuggestions` + matcher | Ranked candidates for a team; explicit search may include below-floor candidates | Absent | Same |
| G5 | Auto-link preview/apply | `PreviewAutoLinks`, `ApplyAutoLinks`, `sports_auto_link_provenance.sql` | Per-team candidate, runner-up gap, eligibility/skip reason; apply only eligible links, record provenance | Absent | Same |
| G6 | Team list and links overview | `GET /sports/teams`, `/sports/teams/linked-channels`, `/sports/teams/{id}/links` | League-scoped list, which teams have links, per-team links with resolved state | Absent | Same endpoints |
| G7 | Admin-only mutations | `masterOnlySportsMutation` | Only owner/master mutates links; everyone reads | Partial (admin middleware exists) | Admin-only writes, authenticated reads |
| G8 | Scheduled sync | `sports_background.go` (90 s), `SyncTeams` change detection | Keeps catalog and links fresh without rewriting unchanged rows | Absent | Same |

## H. Preferences, personalization and admin

| # | Feature | MediaStorm implementation | Behaviour to preserve | Wren TV today | Port target |
|---|---|---|---|---|---|
| H1 | Server-wide sports settings | `config.SportsSettings`, `GetSettings` / `PutSettings`, `admin_sports.go` | Enabled switch, enabled league list, favorite team IDs, default source/category scope, per-league search overrides; strict decoding; unknown leagues dropped; empty selection rejected when input was non-empty; immediate refresh | Absent | `Sports` block in `config.yaml` + admin GET/PUT with same validation |
| H2 | Per-profile preferences | `sports_preferences.go`, `user_settings` revisioning, `/users/{id}/sports/preferences` | Versioned snapshot `{revision, preferences}`; strict schema; optimistic concurrency (409 on stale revision); pinned sports, competitions, teams, athletes, schools | Partial (Wren TV has per-user prefs, no revisioning or profiles) | Per-user versioned sports preferences; no profile level exists in Wren TV |
| H3 | Preference merge semantics | `parseSportsPreferences` | Dedupe, unknown IDs survive, unknown fields rejected, school follow merge rules | Absent | Same parser behaviour in Go |
| H4 | Admin sports page | `admin_sports.go` + admin template | Catalog list, league enablement, favorites, source scope per league, logo cache clear | Partial (admin settings UI exists) | New Sports tab in Manage → Settings |
| H5 | Per-user profile editor | `SportsProfileAPI` | Admin can view/edit a user's sports document | Absent | Admin view of per-user pins/follows |
| H6 | Navigation visibility migration | `migrateNavigationTabVisibilitySports` | Existing installs get Sports without clobbering custom nav | Absent | Migration if nav visibility becomes configurable |
| H7 | Favorites in server settings | `FavoriteTeamIDs` | Server-wide favorites independent of per-user follows | Absent | Same |

## I. Playback, player HUD and live dashboard

| # | Feature | MediaStorm implementation | Behaviour to preserve | Wren TV today | Port target |
|---|---|---|---|---|---|
| I1 | Sports player surface | `SportsPlayerHUD.tsx` (431), `SportsLivePlayerSurface.tsx` | Channel identity, broadcast label, controls, recording options, multiview entry | Done (web, browser-verified in the gate's addon case: HUD title, `sports-meta`, `Back to event` and the status pill); recording/multiview absent because Wren TV has no such feature | Reuse Wren TV player; add sports-specific HUD data (broadcast, watch identity) |
| I2 | Live session lifecycle | `useSportsLiveSession.ts`, `useSportsEventPlayback.ts`, `SportsEventPlayback.tsx` | Start/stop session with identity guard, resume/current-event return, error exit | Present (sessions manager, `/api/livetv/channels/{id}/play`) | Reuse; keep identity-guard semantics |
| I3 | Preview focus | `useSportsPreviewFocus.ts` | Docked preview takes focus predictably on TV | Present web (`js/sports-watch.js`: Stop focused on open, Escape/Stop close, launcher restored on close, focus stays in the full player on handover, tune failure focuses the announced error) | Web spatial focus + native focus |
| I4 | Drive replay | `SportsDriveExplorer.tsx` (574), `football-replay.ts`, `SportsReplayButton.tsx` | Field replay overlay, bounded scrolling, back dismissal | Done (web; `sports-replay-model.test.js` plus the gate's "drive replay draws provider coordinates…"): the last twelve plays draw provider traces with possession, down/distance, first-down line and previous eligible play; unplaceable plays are refused | Same |
| I5 | Perspective field | `SportsPerspectiveField.tsx` (517) | Field rendered from either team's perspective | Done (web; `sports-replay-model.test.js` plus the gate's drive-replay case): possession decides the direction, absent coordinates draw no trace and no invented first down | Same |
| I6 | Stats delay | See E12 | Stream-synced stats | Present web (see E12; the docked preview HUD repeats the delayed score and withholds it while buffering) | Same |
| I7 | Player HUD controls | `Controls` reuse in `SportsPlayerHUD` | Language/subtitles/quality where available; live streams have no Pause | Done (web, browser-verified): back-to-event, change source and the lifecycle status are in the sports HUD, plus Audio and Subtitles menus that exist only when the stream publishes a choice — `js/live-tracks.js` builds the lists, hls.js or the element's own track lists supply them, and a remuxed single-AAC channel offers neither. Live video has no Pause | Reuse Wren TV controls; ensure live Pause behaviour matches |

## J. Data contracts to reproduce

| # | Contract | MediaStorm definition | Notes for Wren TV |
|---|---|---|---|
| J1 | Scoreboard | `BackendScoreboard` `{games, updatedAt, stale, leagues:[{league, updatedAt, stale, unavailable}]}` | Keep field names for client parity |
| J2 | Game | `BackendSportsGame`: id, league, sport, title, eventKind, start/end, status/statusDetail, period/clock, home/away team (id, name, location, nickname, abbreviation, logo, colors, score, shootout, winner, rank, seed, record, conference record, standing summary), broadcasts, venue, neutralSite, liveSituation, footballSituation, combat, bestOf, courtName, eventContext, detail | Port 1:1, plus `fieldType`-style hints only where already present |
| J3 | Detail | `SportsGameDetail`: updatedAt, stale, periods, plays, comparisons, leaders, playerStats, lineups, standings, pregame, shots, basketballShots, winProbability, innings, cricketScorecards, footballDrives, scoreHistory, fighterProfiles, leaderboard, tournamentContext, capabilities, source | Same |
| J4 | League | `{id, name, sport, applicationSport, provider, providerSport, providerLeague, college, adapter, eventKind, supportsTeams, implementationStatus, capabilities, aliases, coverageNote, enabled}` | Same |
| J5 | Status | `{enabled, dateQueries, lastRefresh, lastError, gameCount, refreshing}` | Same |
| J6 | Streams | `{streams:[SportsStreamMatch], groups:[SportsStreamGroup], sources:[SportsSourceStatus]}` | Same |
| J7 | Preferences | `{revision, preferences:{version:1, pinnedSportIds, competitionIds, teamIds, athleteIds, schools}}` | Same document, keyed to Wren TV user id |
| J8 | Team links | `SportsTeamWithLinks`, `SportsTeamChannelLink`, `SportsChannelCandidate`, `SportsAutoLinkPreview` | Same |
| J9 | Racing/cycling | `RaceBoard`, `SportsEvent` (sessions/subEvents), `CyclingFeed`, `CyclingRace`, `CyclingStage`, `F1Archive`, `MotoGPEnrichment` | Same |
| J10 | Standings | `LeagueStandings` groups/rows; per-sport columns | Same |

## K. Wren TV baseline (what the port builds on)

| Area | Existing implementation | Relevance |
|---|---|---|
| HTTP API | `internal/api/server.go`, chi, `writeJSON`/`writeErr`, middleware auth + admin | Sports routes slot into the authenticated group |
| Store | SQLite (`internal/db/db.go`), append-only migrations with `schema_version` | New sports tables become appended migrations |
| Config | YAML `internal/config/config.go` with env overrides; `PATCH /api/admin/settings` | Sports server settings follow `LiveTV` pattern |
| Live TV | `internal/livetv`: M3U/Xtream/Stremio channels, XMLTV EPG, now/next, 36 h schedule, groups, play sessions (`/live/{sid}`), max streams, hidden groups | Source of channels and EPG for matching; playback reuse |
| Images | `internal/images` TMDB cache | Extend for provider logos |
| Web client | `internal/web/static`: 10 ES modules (~5k lines), history routing in `app.js`, CSS tokens in `app.css`, player in `player.js` | New `sports.js` module + route + styles; no build step |
| Live TV in web | API only; **no channel/guide page in the web client** | Prerequisite: web Live TV page so "Watch" has a destination outside native apps |
| iOS / macOS / tvOS | SwiftUI, `WrenTVAPI`, `AppState`; tvOS `TabView` tabs; macOS sidebar enum; iOS tab/nav views | Add Sports tab/stack, models, API methods, views |
| Android | Kotlin Compose, `WrenTVApi`, phone `Navigator` routes, TV screens | Add Sports routes/screens and API |
| Windows | WinUI, `WrenTVApi`, `NavigationView` pages | Add Sports page, models, API |
| Users | `users` table, local/Supabase auth, per-user prefs (`/api/me/prefs`), no profiles | Preferences keyed to user id |
| Tests | Go tests beside packages; web has no test runner | Port Go provider/behaviour tests; add browser-level checks for sports |

## L. Parity requirements that are easy to miss

Taken from `SPORTS_DETAIL_COVERAGE.md` and the regression report; these are acceptance criteria, not extras:

1. Verified provider samples for scheduled, live and final states, or an explicit limited/schedule-only contract.
2. Stable provider IDs and correct timezone/day boundaries, including cross-midnight and multi-day events.
3. Zero versus missing values distinguished (e.g. 0–0 count vs unavailable count).
4. Unsupported modules hidden, never synthetic ("no fabricated stats in production").
5. Long-text handling on phone layouts; TV focus/scroll checked; no unbounded card mounting.
6. Spoiler mode verified on hub, cards and detail entry.
7. Source-failure recovery: failed sources don't erase last-good scores; partial results retained.
8. Stale/unavailable per league surfaced to the client, not silently dropped.
9. Stream matching must never offer a city name or league name alone as a credible feed.
10. Archive imagery/geometry keeps attribution; provider artwork stays optional with fallbacks.
11. Live F1 telemetry is not claimed; F1 archive is labelled archive.
12. Discovery of unknown addon sources runs bounded and resumable, with per-source states.
13. One request per user action on TV; repeated Select cannot double-navigate.
14. Deep links validate catalog identity before rendering.
15. Settings changes take effect without restart (polling set, enabled switch).

## M. Gap summary

| Area | MediaStorm | Wren TV | Gap size |
|---|---|---|---|
| Sports data ingestion | 12 provider families, 55+ leagues, 8 sport detail models | none | Large (greenfield package) |
| Sports API | ~40 endpoints incl. admin | none | Large |
| Persistence | Postgres: teams, links, preferences revisioning | SQLite users/prefs only | Medium |
| Live channel matching | token matcher + EPG + grouping + quality + expansion | live channels/EPG exist, no matcher | Medium |
| Web UI | React Native app (phone/tablet/TV) | vanilla JS, no sports, no live TV page | Large (rewrite, not copy) |
| Native UIs | — (MediaStorm clients are RN) | five native codebases to extend | Large |
| Tests | 162 frontend sports suites; 156 backend test files reference sports | Go test conventions only | Large but portable at behaviour level |

No Wren TV feature needs to be removed or reduced. Everything above is additive.
