# Sports Hub for Wren TV

This directory holds the port plan for MediaStorm's Sports Hub. The goal is feature parity: a Wren TV user
sees the same hub, event pages, watch flow, preferences and admin controls that a MediaStorm user sees, built
on Wren TV's Go/chi + SQLite server, vanilla-JS web client and native apps.

- [Feature inventory and gap analysis](01-feature-inventory.md) — every Sports Hub capability found in
  `mediastorm-frontend` and `mediastorm`, what it does, and what Wren TV has today.
- [Migration plan](02-migration-plan.md) — backend, database, ingestion, web client, native apps, tests and
  the phased milestones that reach parity.

## Source of truth

| Area | Repository | Key paths |
|---|---|---|
| Sports UI, models, behaviours | `mediastorm-frontend` | `components/sports/`, `features/sports/`, `app/sports/`, `app/(drawer)/sports.tsx` |
| Score ingestion, matching, team channels, preferences | `mediastorm` (backend) | `services/sports/`, `handlers/sports*.go`, `handlers/admin_sports.go`, `models/sports*.go`, `internal/datastore/pg_sports_links.go` |
| Coverage promises and limits | `mediastorm-backend/SPORTS_DETAIL_COVERAGE.md`, `mediastorm-frontend/docs/verification/sports-regression-2026-09-20.md` | — |

Sizes at the time of the audit: ~25.6k lines of sports components, ~6.9k lines of sports feature modules,
~1k lines of sports routes, ~17.2k lines of sports tests (162 test files) on the frontend; ~7.9k lines of
`services/sports`, ~3.1k lines of sports handlers and ~590 lines of sports models on the backend.

## Non-goals

- No permanent reduction in functionality. Phases below are milestones toward parity, not a reduced product.
- No shared database or runtime dependency on MediaStorm. The port is self-contained in Wren TV.
- No paid sports data provider. MediaStorm's contracts use the same public providers (ESPN, TheSportsDB,
  official F1 archive, ASO/RCS guides); the port keeps that constraint.
- No client-side fixtures in production. MediaStorm's `.local` / `replay` fixtures are design and test
  material only; they become Wren TV testdata, never runtime fallbacks.
