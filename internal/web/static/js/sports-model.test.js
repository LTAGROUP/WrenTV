import test from 'node:test';
import assert from 'node:assert/strict';

import {
  STATUS_FILTERS, localDate, shiftDate, dateChoices, filterEvents, buildLanes, pickFeatured,
  scoresHidden, teamScore, statusText, watchActionLabel, freshness, pageItems, refreshIntervalMs,
  detailRefreshMs, searchResultGroups, sportOptions, startLabel, eventTitle, formatDetailStatus,
  formatGameResult, buildEventAccessibilityLabel,
} from './sports-model.js';

const now = new Date(2026, 9, 24, 12, 0, 0);

const leagues = [
  { id: 'nfl', name: 'NFL', sport: 'football', enabled: true },
  { id: 'nba', name: 'NBA', sport: 'basketball', enabled: true },
  { id: 'mlb', name: 'MLB', sport: 'baseball', enabled: true },
  { id: 'ufc', name: 'UFC', sport: 'mma', enabled: true },
];

const events = [
  { id: '1', league: 'nba', status: 'live', eventKind: 'matchup', startTime: '2026-10-24T22:00:00Z', statusDetail: '3rd - 4:12', title: 'Celtics at Heat', participants: [{ id: '2', name: 'Celtics', score: '81' }, { id: '14', name: 'Heat', score: '78' }] },
  { id: '2', league: 'nfl', status: 'final', eventKind: 'matchup', startTime: '2026-10-24T18:00:00Z', statusDetail: 'Final', title: 'Bills at Jets', participants: [{ id: '3', name: 'Bills', score: '24' }, { id: '4', name: 'Jets', score: '17' }] },
  { id: '3', league: 'mlb', status: 'scheduled', eventKind: 'matchup', startTime: '2026-10-25T00:00:00Z', statusDetail: '7:05 PM', title: 'Yankees at Red Sox', participants: [{ id: '10', name: 'Yankees', score: '' }, { id: '2', name: 'Red Sox', score: '' }] },
  { id: '4', league: 'ufc', status: 'final', eventKind: 'fight-card', startTime: '2026-10-24T02:00:00Z', statusDetail: 'Final', title: 'UFC 300', participants: [{ id: 'a', name: 'Fighter A', score: 'W' }, { id: 'b', name: 'Fighter B', score: 'L' }] },
];

test('watch action wording follows the event lifecycle for every sport', () => {
  assert.equal(watchActionLabel({ status: 'live' }), 'Watch');
  assert.equal(watchActionLabel({ status: 'scheduled' }), 'Watch');
  assert.equal(watchActionLabel({ status: 'final' }), 'Search for Replay');
  // An absent or unknown lifecycle is treated as upcoming, never as finished.
  assert.equal(watchActionLabel({ status: 'postponed' }), 'Watch');
  assert.equal(watchActionLabel(null), 'Watch');
});

test('date rail uses local days and follows server capabilities', () => {
  const rail = dateChoices(now);
  assert.equal(rail.length, 3);
  assert.deepEqual(rail.map((d) => d.id), ['yesterday', 'today', 'tomorrow']);
  assert.equal(rail[1].date, localDate(now));
  assert.equal(rail[2].label, 'Tomorrow');
  assert.equal(rail[1].weekday.length > 0, true);
  const limited = dateChoices(now, ['today']);
  assert.deepEqual(limited.map((d) => d.id), ['today']);
});

test('status, sport and search filters compose', () => {
  assert.equal(filterEvents(events, { status: 'live' }, leagues).length, 1);
  assert.equal(filterEvents(events, { status: 'final' }, leagues).length, 2);
  assert.equal(filterEvents(events, { sport: 'baseball' }, leagues).map((e) => e.id)[0], '3');
  assert.equal(filterEvents(events, { query: 'bills' }, leagues).map((e) => e.id)[0], '2');
  // Single letters are not search terms, matching the two-character floor.
  assert.equal(filterEvents(events, { query: 'a' }, leagues).length, 4);
  assert.equal(filterEvents(events, { league: 'ufc' }, leagues).length, 1);
});

test('featured is the first live event and is removed from its lane', () => {
  const featured = pickFeatured(events);
  assert.equal(featured.id, '1');
  const lanes = buildLanes(events, leagues, { featuredId: featured.id });
  assert.deepEqual(lanes.map((l) => l.name), ['NFL', 'MLB', 'UFC']);
  assert.equal(lanes.some((l) => l.events.some((e) => e.id === '1')), false);
  assert.equal(pickFeatured(events.filter((e) => e.status !== 'live')).id, '2');
});

test('lanes keep catalog order, not event volume', () => {
  const many = [...events, { ...events[2], id: '3b', league: 'mlb' }, { ...events[2], id: '3c', league: 'mlb' }];
  const lanes = buildLanes(many, leagues, { featuredId: '' });
  assert.deepEqual(lanes.map((l) => l.league.id), ['nfl', 'nba', 'mlb', 'ufc']);
});

test('spoiler hides scores for started events only', () => {
  assert.equal(scoresHidden(events[0], true), true);
  assert.equal(scoresHidden(events[1], true), true);
  assert.equal(scoresHidden(events[2], true), false);
  assert.equal(teamScore(events[0], events[0].participants[0], true), '');
  assert.equal(teamScore(events[0], events[0].participants[0], false), '81');
  assert.equal(teamScore(events[2], events[2].participants[0], true), '');
  assert.equal(statusText(events[1], true), 'Final');
  assert.equal(statusText(events[0], false), '3rd - 4:12');
});

test('freshness labels reflect the server timestamp', () => {
  const recent = freshness(new Date(now.getTime() - 30000).toISOString(), false, now);
  assert.equal(recent.stale, false);
  assert.match(recent.label, /^Feed checked /);
  const old = freshness(new Date(now.getTime() - 600000).toISOString(), false, now);
  assert.equal(old.stale, true);
  assert.equal(old.label, 'Scores may be delayed');
  assert.equal(freshness('', false, now).label, 'Scores may be delayed');
  assert.equal(freshness(new Date(now.getTime() - 1000).toISOString(), true, now).stale, true);
});

test('pagination clamps and bounds each page', () => {
  const items = Array.from({ length: 26 }, (_, i) => i);
  const first = pageItems(items, 0, 12);
  assert.equal(first.items.length, 12);
  assert.equal(first.pages, 3);
  const last = pageItems(items, 9, 12);
  assert.equal(last.page, 2);
  assert.equal(last.items.length, 2);
});

test('refresh cadence follows live, upcoming and settled states', () => {
  assert.equal(refreshIntervalMs(events, false), 30000);
  assert.equal(refreshIntervalMs(events, true), 30000);
  assert.equal(refreshIntervalMs([events[2]], false), 60000);
  assert.equal(refreshIntervalMs([events[1]], false), 300000);
});

test('sport options come from enabled leagues', () => {
  const options = sportOptions([...leagues, { id: 'off', name: 'Off', sport: 'soccer', enabled: false }]);
  assert.deepEqual(options.map((o) => o.id).sort(), ['baseball', 'basketball', 'football', 'mma']);
  assert.equal(STATUS_FILTERS.length, 4);
  assert.equal(startLabel({ startTime: '' }), '');
});

test('event detail refresh follows live, final and scheduled cadence', () => {
  assert.equal(detailRefreshMs({ status: 'live' }), 120000);
  assert.equal(detailRefreshMs({ status: 'final' }), 600000);
  assert.equal(detailRefreshMs({ status: 'scheduled' }), 60000);
  // An unknown lifecycle still polls, so a stale mark can be corrected.
  assert.equal(detailRefreshMs({}), 60000);
});

test('server search results keep playable channels and event identity', () => {
  const groups = searchResultGroups({
    livePrograms: [{ title: 'Celtics vs Heat', channelId: 'c1', channelName: 'NBA TV', start: 1700000000 }],
    upcomingPrograms: [{ title: 'Postgame', channelId: 'c2', channelName: 'ESPN', start: 1700003600 }],
    channels: [{ id: 'c1', name: 'NBA TV', group: 'Sports', now: { title: 'Celtics vs Heat' } }],
    sportsEvents: [{ id: '402', league: 'nba', status: 'live', title: 'Celtics at Heat' }],
    teams: [{ id: '2', name: 'Boston Celtics', league: 'nba', record: '3-1' }],
  }, [{ id: 'nba', name: 'NBA' }]);
  assert.deepEqual(groups.map((group) => group.kind), ['live', 'upcoming', 'channels', 'events', 'teams']);
  assert.equal(groups[0].items[0].href, '/live?channel=c1');
  assert.equal(groups[1].items[0].href, '/live?channel=c2');
  assert.equal(groups[2].items[0].meta, 'Now: Celtics vs Heat');
  assert.equal(groups[3].items[0].href, '/sports/event/402');
  assert.equal(groups[3].items[0].subtitle, 'NBA');
  // A team has no page yet, so it carries its competition and no dead link.
  assert.equal(groups[4].items[0].href, '');
  assert.equal(groups[4].items[0].subtitle, 'NBA');
  assert.equal(groups[4].items[0].meta, '3-1');
  // Empty sources are dropped instead of rendering empty headings.
  assert.deepEqual(searchResultGroups({}), []);
  assert.equal(searchResultGroups({ channels: [{ id: 'c', name: 'One' }] }).length, 1);
});

// Ported from MediaStorm's sports-model.test.ts so the web sticky header and
// the spoken card label use the same sport-aware rules.
test('the sticky header formats soccer periods and outcomes from provider state', () => {

  const base = { sport: 'soccer', league: 'soccer-eng.1', status: 'live', clock: '120+1', period: 'HT' };
  assert.equal(formatDetailStatus(base), 'Halftime');
  assert.equal(formatDetailStatus({ ...base, period: 'ET2' }), 'Extra time · Second half · 120+1');
  assert.equal(formatDetailStatus({ ...base, period: 'PEN' }), 'Penalty shootout');
  assert.equal(formatDetailStatus({ ...base, period: 'PEN', status: 'final' }), 'Full time · Penalties');
  assert.equal(formatDetailStatus({ ...base, period: 'ET2', status: 'final' }), 'Full time · After extra time');
  assert.equal(formatDetailStatus({ ...base, period: 'H2', detail: { stale: true } }), 'Second half · 120+1 · Updates delayed');
  assert.equal(
    formatDetailStatus({ ...base, period: 'FT', status: 'final', statusDetail: 'Final - Penalties' }),
    'Full time · Penalties',
  );
  const teams = {
    homeTeam: { id: 'h', shootoutScore: 4 }, awayTeam: { id: 'a', shootoutScore: 2 },
  };
  assert.equal(
    formatDetailStatus({ ...base, period: 'FT', status: 'final', statusDetail: 'Final', ...teams }),
    'Full time · Penalties',
  );
  const draw = {
    sport: 'soccer', status: 'final', participants: [{ id: 'a', score: '2' }, { id: 'h', score: '2' }],
  };
  assert.equal(formatGameResult(draw), 'Draw');
  assert.equal(
    formatGameResult({ ...draw, homeTeam: { id: 'h', shootoutScore: 4 }, awayTeam: { id: 'a', shootoutScore: 2 } }),
    '',
  );
});

test('the sticky header formats hockey periods, intermissions and shootouts', () => {

  const base = { sport: 'hockey', league: 'nhl', status: 'live', statusDetail: 'In progress', clock: '02:30' };
  assert.equal(formatDetailStatus({ ...base, period: 'P1' }), '1st period · 02:30');
  assert.equal(formatDetailStatus({ ...base, period: 'P2', statusDetail: 'Intermission' }), 'Intermission');
  assert.equal(formatDetailStatus({ ...base, period: 'OT' }), 'Overtime · 02:30');
  assert.equal(formatDetailStatus({ ...base, period: '2OT' }), 'Overtime · 2OT · 02:30');
  assert.equal(formatDetailStatus({ ...base, period: 'SO', statusDetail: 'Shootout' }), 'Shootout');
  assert.equal(formatDetailStatus({ ...base, period: 'P3', status: 'final', statusDetail: 'Final/SO' }), 'Final · Shootout');
});

test('the sticky header preserves cricket detail and generic states', () => {

  assert.equal(
    formatDetailStatus({ sport: 'cricket', status: 'final', statusDetail: 'Match tied' }),
    'Match tied',
  );
  assert.equal(
    formatDetailStatus({ sport: 'cricket', status: 'final', statusDetail: 'Match tied', detail: { stale: true } }),
    'Match tied · Updates delayed',
  );
  assert.equal(
    formatDetailStatus({ sport: 'basketball', status: 'live', period: '3rd quarter', clock: '4:12' }),
    '3rd quarter · 4:12',
  );
  assert.equal(formatDetailStatus({ sport: 'basketball', status: 'live' }), 'Live');
  assert.equal(formatDetailStatus({ sport: 'football', status: 'scheduled', startTime: '' }), 'Scheduled');
  assert.equal(
    formatDetailStatus({ sport: 'football', league: 'college-football', status: 'live', period: '1OT', clock: '0:12' }),
    '1OT',
  );
  assert.equal(formatDetailStatus({ sport: 'football', status: 'final', statusDetail: '' }), 'Final');
  assert.equal(formatDetailStatus({ sport: 'football', status: 'final', statusDetail: 'Final' }), 'Final');
});

test('the spoken card label names score, lifecycle, result and situation', () => {

  const live = {
    league: 'nba', sport: 'basketball', status: 'live', period: '3rd quarter', clock: '4:12',
    participants: [{ name: 'Celtics', score: '81' }, { name: 'Heat', score: '78' }],
  };
  assert.equal(
    buildEventAccessibilityLabel(live),
    'Celtics 81, Heat 78, Live, 3rd quarter · 4:12',
  );
  const scheduled = { ...live, status: 'scheduled', startTime: '', participants: [{ name: 'Celtics' }, { name: 'Heat' }] };
  assert.equal(buildEventAccessibilityLabel(scheduled), 'Celtics, Heat, Upcoming, Scheduled');
  const final = {
    league: 'nfl', sport: 'football', status: 'final',
    participants: [{ name: 'Bills', score: '24', winner: true }, { name: 'Jets', score: '17' }],
  };
  assert.equal(buildEventAccessibilityLabel(final), 'Bills 24, Jets 17, Final, Final, Bills win');
  const baseball = {
    league: 'mlb', sport: 'baseball', status: 'live', period: '7th inning', clock: '',
    participants: [{ name: 'Yankees', score: '3' }, { name: 'Red Sox', score: '2' }],
    liveSituation: { balls: 2, strikes: 1, outs: 1, bases: 'First and third' },
  };
  assert.match(buildEventAccessibilityLabel(baseball), /2–1 count · 1 out · First and third/);
  {
    const golf = {
      league: 'pga', sport: 'golf', eventKind: 'tournament', status: 'live', title: 'The Open',
      participants: [{ name: 'Player A', score: '-4' }, { name: 'Player B', score: '-3' }],
    };
    assert.match(buildEventAccessibilityLabel(golf), /^The Open, Live/);
  }
  const fight = {
    league: 'ufc', sport: 'mma', eventKind: 'fight-card', status: 'final', title: 'UFC 300',
    participants: [{ name: 'Fighter A', score: 'W', winner: true }, { name: 'Fighter B', score: 'L' }],
  };
  assert.match(buildEventAccessibilityLabel(fight), /^UFC 300, Final/);
  assert.equal(buildEventAccessibilityLabel({ status: 'live' }), 'Live, Live');
});

// P7 audit: long provider strings stay bounded in the card/header model, and the
// spoken label keeps the full text rather than a clipped one.
test('long titles and details are carried whole and do not invent values', () => {
  const long = 'A'.repeat(400);
  // Today at 13:00 in the viewer's zone: the spoken label shows a clock time
  // only on the same local day, so a fixed calendar date would make this test
  // depend on the day it runs.
  const start = new Date();
  start.setHours(13, 0, 0, 0);
  const event = {
    league: 'nfl', status: 'scheduled', eventKind: 'matchup', title: long,
    startTime: start.toISOString(),
    participants: [{ name: 'Team One' }, { name: 'Team Two' }],
  };
  assert.equal(eventTitle(event), long);
  const label = buildEventAccessibilityLabel(event);
  assert.ok(label.startsWith('Team One, Team Two, Upcoming'));
  assert.match(label, /^Team One, Team Two, Upcoming, (?:1:00 PM|13:00|1:00 p\.m\.)/);
  // A missing title falls back to the participants, never to a slice of a URL.
  assert.equal(eventTitle({ participants: [{ name: 'A' }, { name: 'B' }] }), 'A vs B');
});

// P7 audit: day boundaries are the viewer's local calendar day, so an evening
// game is not shown as tomorrow (or yesterday) because the server is in UTC.
test('dates and start times use the local calendar day', () => {
  const late = new Date(2026, 9, 24, 23, 45);
  const early = new Date(2026, 9, 25, 0, 15);
  assert.equal(localDate(late), '2026-10-24');
  assert.equal(localDate(early), '2026-10-25');
  assert.equal(shiftDate(late, 1), '2026-10-25');
  const label = startLabel({ startTime: late.toISOString() }, new Date(2026, 9, 24, 8, 0));
  assert.match(label, /11:45/);
  const otherDay = startLabel({ startTime: early.toISOString() }, new Date(2026, 9, 24, 8, 0));
  assert.match(otherDay, /Oct 25/);
  for (const choice of dateChoices(new Date(2026, 9, 24, 23, 59))) {
    assert.equal(choice.date, localDate(new Date(`${choice.date}T12:00:00`)));
  }
});
