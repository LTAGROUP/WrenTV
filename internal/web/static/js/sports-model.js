// Pure Sports Hub model helpers. No DOM and no API calls, so the same rules are
// unit-tested with node --test and used by the browser module.

export const STATUS_FILTERS = [
  { id: 'all', label: 'All' },
  { id: 'live', label: 'Live' },
  { id: 'final', label: 'Results' },
  { id: 'scheduled', label: 'Upcoming' },
];

const pad = (n) => String(n).padStart(2, '0');

// localDate is the viewer's calendar day, which is what the hub's rail shows.
export function localDate(date = new Date()) {
  return `${date.getFullYear()}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}`;
}

export function shiftDate(date, days) {
  const next = new Date(date.getFullYear(), date.getMonth(), date.getDate() + days);
  return localDate(next);
}

// dateChoices builds yesterday/today/tomorrow for the rail. The server caps the
// window; a backend without date queries only receives today.
export function dateChoices(now = new Date(), supported = ['yesterday', 'today', 'tomorrow']) {
  const labels = { yesterday: 'Yesterday', today: 'Today', tomorrow: 'Tomorrow' };
  const offsets = { yesterday: -1, today: 0, tomorrow: 1 };
  return supported.map((id) => {
    const date = shiftDate(now, offsets[id] ?? 0);
    const d = new Date(`${date}T12:00:00`);
    return {
      id,
      date,
      label: labels[id] || id,
      weekday: d.toLocaleDateString([], { weekday: 'short' }),
      day: d.toLocaleDateString([], { month: 'short', day: 'numeric' }),
    };
  });
}

export function leagueIndex(leagues = []) {
  const map = new Map();
  for (const league of leagues) map.set(league.id, league);
  return map;
}

export function leagueName(id, leagues = []) {
  return leagues.find((l) => l.id === id)?.name || id;
}

export function eventStatus(event) {
  return ['live', 'final', 'scheduled'].includes(event?.status) ? event.status : 'scheduled';
}

// A finished event no longer offers live playback, so the same action searches
// for a replay instead. Live and upcoming events keep "Watch".
export function watchActionLabel(event) {
  return eventStatus(event) === 'final' ? 'Search for Replay' : 'Watch';
}

export function isTeamMatchup(event) {
  return (event?.eventKind || 'matchup') === 'matchup' && !!event?.participants?.length;
}

// scoresHidden matches MediaStorm's spoiler rule: scores and results are hidden
// on cards for anything that has started; opening the event reveals them.
export function scoresHidden(event, spoiler) {
  return !!spoiler && eventStatus(event) !== 'scheduled';
}

export function teamScore(event, participant, spoiler) {
  if (scoresHidden(event, spoiler)) return '';
  return participant?.score == null ? '' : String(participant.score);
}

export function statusText(event, spoiler) {
  const status = eventStatus(event);
  if (status === 'live') return event.statusDetail || 'Live';
  if (status === 'final') return scoresHidden(event, spoiler) ? 'Final' : 'Final';
  return event.statusDetail || startLabel(event);
}

export function startLabel(event, now = new Date()) {
  const start = new Date(event?.startTime);
  if (Number.isNaN(start.getTime())) return '';
  const today = localDate(now);
  const day = localDate(start);
  const time = start.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
  if (day === today) return time;
  return `${start.toLocaleDateString([], { weekday: 'short', month: 'short', day: 'numeric' })} · ${time}`;
}

export function eventTitle(event) {
  if (event?.title) return event.title;
  const names = (event?.participants || []).map((p) => p?.name).filter(Boolean);
  return names.length ? names.join(' vs ') : 'Event';
}

export function eventSort(a, b) {
  return (Date.parse(a?.startTime) || 0) - (Date.parse(b?.startTime) || 0);
}

// filterEvents applies the status tab, sport filter, league focus and search.
// Unknown statuses never disappear from "All".
export function filterEvents(events = [], { status = 'all', sport = 'all', league = 'all', query = '' } = {}, leagues = []) {
  const index = leagueIndex(leagues);
  const terms = String(query || '').toLowerCase().split(/\s+/).filter((t) => t.length >= 2);
  return events.filter((event) => {
    if (status !== 'all' && eventStatus(event) !== status) return false;
    if (league !== 'all' && event.league !== league) return false;
    if (sport !== 'all' && index.get(event.league)?.sport !== sport) return false;
    if (!terms.length) return true;
    const haystack = `${eventTitle(event)} ${event.venueName || ''} ${event.statusDetail || ''} ${(event.broadcasts || []).join(' ')} ${event.league}`.toLowerCase();
    return terms.some((term) => haystack.includes(term));
  }).sort(eventSort);
}

// pickFeatured is the event promoted above the lanes: the first live game,
// else the first event of the day, matching SportsHubTV.
export function pickFeatured(events = []) {
  return events.find((event) => eventStatus(event) === 'live') || events[0] || null;
}

// buildLanes keeps catalog order rather than sorting by volume, and drops the
// featured event from its lane so it is not shown twice.
export function buildLanes(events = [], leagues = [], { featuredId = '' } = {}) {
  const lanes = [];
  for (const league of leagues) {
    const laneEvents = events.filter((event) => event.league === league.id && event.id !== featuredId);
    if (laneEvents.length) {
      lanes.push({ league, name: league.name, sport: league.sport, events: laneEvents.sort(eventSort) });
    }
  }
  // Events from a league missing from the catalog stay reachable at the end.
  const known = new Set(leagues.map((l) => l.id));
  const extras = events.filter((e) => !known.has(e.league) && e.id !== featuredId);
  if (extras.length) lanes.push({ league: { id: 'other', name: 'Other', sport: 'other' }, name: 'Other', sport: 'other', events: extras.sort(eventSort) });
  return lanes;
}

export function sportOptions(leagues = []) {
  const seen = new Map();
  for (const league of leagues) {
    if (!league.enabled || !league.sport) continue;
    if (!seen.has(league.sport)) seen.set(league.sport, { id: league.sport, label: labelSport(league.sport) });
  }
  return [...seen.values()].sort((a, b) => a.label.localeCompare(b.label));
}

export function labelSport(sport) {
  return String(sport || '').replace(/-/g, ' ').replace(/\b\w/g, (c) => c.toUpperCase());
}

// freshness reports whether the feed is stale and what to tell the viewer.
// Server search results keep the identity each one needs to be actionable: an
// EPG hit names its playable channel, an event keeps its deep link, a team keeps
// its competition. Nothing is re-derived from the loaded feed.
export function searchResultGroups(results = {}, leagues = []) {
  const channelHref = (id) => (id ? `/live?channel=${encodeURIComponent(id)}` : '');
  const startLabelOf = (start) => {
    const value = Number(start);
    if (!Number.isFinite(value) || value <= 0) return '';
    const date = new Date(value * 1000);
    return date.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
  };
  const groups = [
    {
      kind: 'live', label: 'Live now',
      items: (results.livePrograms || []).map((hit) => ({
        key: `live:${hit.channelId}:${hit.start}`,
        title: hit.title || hit.channelName, subtitle: hit.channelName,
        meta: hit.description || '', href: channelHref(hit.channelId), action: 'Watch',
      })),
    },
    {
      kind: 'upcoming', label: 'Upcoming on TV',
      items: (results.upcomingPrograms || []).map((hit) => ({
        key: `upcoming:${hit.channelId}:${hit.start}`,
        title: hit.title || hit.channelName, subtitle: hit.channelName,
        meta: startLabelOf(hit.start), href: channelHref(hit.channelId), action: 'Channel',
      })),
    },
    {
      kind: 'channels', label: 'Channels',
      items: (results.channels || []).map((channel) => ({
        key: `channel:${channel.id}`,
        title: channel.name, subtitle: channel.group || '',
        meta: channel.now?.title ? `Now: ${channel.now.title}` : '',
        href: channelHref(channel.id), action: 'Watch',
      })),
    },
    {
      kind: 'events', label: 'Events',
      items: (results.sportsEvents || []).map((event) => ({
        key: `event:${event.id}`,
        title: eventTitle(event), subtitle: leagueName(event.league, leagues) || event.league || '',
        meta: event.status === 'live' ? 'Live' : event.status === 'final' ? 'Final' : startLabel(event),
        href: `/sports/event/${encodeURIComponent(event.id)}`, action: 'Open',
      })),
    },
    {
      kind: 'teams', label: 'Teams',
      items: (results.teams || []).map((team) => ({
        key: `team:${team.league || ''}:${team.id}`,
        title: team.name, subtitle: leagueName(team.league, leagues) || team.league || '',
        meta: team.record || '', href: '', action: '',
      })),
    },
  ];
  return groups.filter((group) => group.items.length);
}

export function freshness(updatedAt, stale, now = new Date(), staleAfterMs = 120000) {
  const at = Date.parse(updatedAt || '');
  const staleByTime = !Number.isFinite(at) || now.getTime() - at > staleAfterMs;
  const isStale = !!stale || staleByTime;
  if (!Number.isFinite(at)) return { stale: isStale, label: 'Scores may be delayed' };
  const when = new Date(at).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
  return { stale: isStale, label: isStale ? 'Scores may be delayed' : `Feed checked ${when}` };
}

export function pageItems(items = [], page = 0, size = 12) {
  const pages = Math.max(1, Math.ceil(items.length / size));
  const current = Math.min(Math.max(0, page), pages - 1);
  return { items: items.slice(current * size, current * size + size), page: current, pages };
}

// refreshIntervalMs mirrors sports-refresh.ts: frequent while live or stale,
// slower for an upcoming day, slowest for a settled one.
// Event detail is separate from the hub feed. MediaStorm refreshes a live event
// every two minutes, a settled one every ten, and a scheduled one every minute
// so the first pitch/tip-off transition is never missed.
export const DETAIL_REFRESH_MS = { live: 120000, final: 600000, scheduled: 60000 };

export function detailRefreshMs(event) {
  const status = eventStatus(event);
  return DETAIL_REFRESH_MS[status] || DETAIL_REFRESH_MS.scheduled;
}

export function refreshIntervalMs(events = [], stale = false) {
  if (stale || events.some((e) => eventStatus(e) === 'live')) return 30000;
  if (events.some((e) => eventStatus(e) === 'scheduled')) return 60000;
  return 300000;
}

// withholdLiveData removes every value that reveals how far the stream has
// progressed. Used while a stats delay is still warming up, so the live score
// is never shown early. Structure (teams, competition, tabs) stays visible.
export function withholdLiveData(event) {
  return {
    ...(event || {}),
    // syncing keeps the lifecycle out of the header too: a delayed view must
    // not reveal that the game has ended before the stream has shown it.
    syncing: true,
    period: '',
    clock: '',
    statusDetail: '',
    participants: (event?.participants || []).map((p) => ({ ...p, score: '', winner: false })),
    detail: {
      ...(event?.detail || {}),
      periods: ((event?.detail || {}).periods || []).map((p) => ({ ...p, away: '', home: '' })),
    },
  };
}

// ---------------------------------------------------------------- status text
// formatDetailStatus is the web port of MediaStorm's sport-aware sticky-header
// formatter. It uses only provider-supplied state: no invented periods, no
// guessed overtime and no clock where the provider has none.
const HOCKEY_PERIODS = { P1: '1st period', P2: '2nd period', P3: '3rd period' };
const SOCCER_PERIODS = {
  HT: 'Halftime',
  H1: 'First half',
  H2: 'Second half',
  ET1: 'Extra time · First half',
  ET2: 'Extra time · Second half',
  ET: 'Extra time',
  PEN: 'Penalty shootout',
  PK: 'Penalty shootout',
  FT: 'Full time',
};

function shootoutPlayed(event) {
  return event?.homeTeam?.shootoutScore != null || event?.awayTeam?.shootoutScore != null;
}

function shootoutScoreFor(event, participantId) {
  const team = [event?.homeTeam, event?.awayTeam].find((t) => t && t.id === participantId);
  return team?.shootoutScore;
}

export function formatDetailStatus(event) {
  if (event?.syncing) return 'Syncing stats…';
  const status = eventStatus(event);
  const sport = event?.sport || '';
  const period = String(event?.period || '').trim();
  const detailLabel = String(event?.statusDetail || '').trim();
  const clock = String(event?.clock || '').trim();
  const stale = event?.detail?.stale ? 'Updates delayed' : '';

  if (sport === 'cricket' && status !== 'scheduled') {
    const reported = detailLabel || period;
    const base = reported || (status === 'live' ? 'Live' : status === 'final' ? 'Final' : status);
    return [base, stale].filter(Boolean).join(' · ');
  }

  if (sport === 'hockey' && (status === 'live' || status === 'final')) {
    const supplied = `${period} ${detailLabel}`;
    const shootout = /\bSO\b|shootout/i.test(supplied) || shootoutPlayed(event);
    const overtime = /\b(?:\d+OT|OT\d*)\b|overtime/i.test(supplied);
    const intermission = /intermission|\bINT\b/i.test(supplied);
    const label = shootout
      ? 'Shootout'
      : intermission
        ? 'Intermission'
        : overtime
          ? (/(?:\d+OT|OT\d+)/i.test(period) ? `Overtime · ${period}` : 'Overtime')
          : (HOCKEY_PERIODS[period] || period || 'Live');
    const base = status === 'final'
      ? `Final${shootout ? ' · Shootout' : overtime ? ' · Overtime' : ''}`
      : [label || 'Live', !shootout && !intermission ? clock : ''].filter(Boolean).join(' · ');
    return [base, stale].filter(Boolean).join(' · ');
  }

  if (sport === 'soccer' && (status === 'live' || status === 'final')) {
    const finalStatus = `${period} ${detailLabel}`;
    const shootout = shootoutPlayed(event) || /\b(?:PEN|PK)\b|penalt/i.test(finalStatus);
    const extraTime = /\b(?:ET[12]?|AET)\b|extra time/i.test(finalStatus);
    const base = status === 'final'
      ? shootout
        ? 'Full time · Penalties'
        : extraTime
          ? 'Full time · After extra time'
          : 'Full time'
      : ([SOCCER_PERIODS[period.toUpperCase()] ?? period,
          !/^(HT|halftime|half time|PEN|PK)$/i.test(period) ? clock : ''].filter(Boolean).join(' · ') || 'Live');
    return [base, stale].filter(Boolean).join(' · ');
  }

  const collegeOT = event?.league === 'college-football' && /\b(?:\d+OT|OT\d*)\b|overtime/i.test(period);
  if (status === 'live') {
    return [period || detailLabel, !collegeOT && clock].filter(Boolean).join(' · ') || 'Live';
  }
  if (status === 'scheduled') return startLabel(event) || 'Scheduled';
  return detailLabel || ['Final', period].filter(Boolean).join(' · ');
}

// formatGameResult names the winner only when the provider marked exactly one.
export function formatGameResult(event) {
  if (eventStatus(event) !== 'final') return '';
  const participants = event?.participants || [];
  const winners = participants.filter((p) => p?.winner);
  if (participants.length === 2 && winners.length === 1) {
    const winner = winners[0];
    const loser = participants.find((p) => p !== winner);
    const winnerShootout = shootoutScoreFor(event, winner.id);
    const loserShootout = loser ? shootoutScoreFor(event, loser.id) : undefined;
    return `${winner.name} win${winnerShootout != null && loserShootout != null ? ` ${winnerShootout}–${loserShootout} on penalties` : ''}`;
  }
  if (
    event?.sport === 'soccer' &&
    participants.length === 2 &&
    participants[0]?.score != null &&
    participants[0].score === participants[1]?.score &&
    !shootoutPlayed(event)
  ) return 'Draw';
  return '';
}

// Only real provider counts are spoken; absent counts stay absent.
export function formatLiveSituation(situation) {
  if (!situation) return '';
  return [
    situation.balls != null && situation.strikes != null ? `${situation.balls}–${situation.strikes} count` : '',
    situation.outs != null ? `${situation.outs} ${situation.outs === 1 ? 'out' : 'outs'}` : '',
    situation.bases || '',
  ].filter(Boolean).join(' · ');
}

// buildEventAccessibilityLabel gives a card one complete spoken sentence:
// participants and scores, lifecycle, timing/result and the live situation.
// Individual sports use the event title instead of a two-participant score.
export function buildEventAccessibilityLabel(event) {
  const status = eventStatus(event);
  const participants = event?.participants || [];
  const individual = (event?.eventKind || 'matchup') !== 'matchup' ||
    ['golf', 'boxing', 'motorsport', 'cycling', 'racing'].includes(event?.sport);
  const scoreFor = (p) => (status === 'scheduled' || p?.score == null || p.score === '' ? p?.name : `${p?.name} ${p.score}`);
  const names = individual
    ? [eventTitle(event)]
    : participants.slice(0, 2).map(scoreFor).filter((name) => name && name !== 'undefined');
  const lifecycle = status === 'live' ? 'Live' : status === 'final' ? 'Final' : 'Upcoming';
  const extra = status === 'live'
    ? formatLiveSituation(event?.liveSituation)
    : status === 'final'
      ? formatGameResult(event)
      : '';
  return [...names, lifecycle, formatDetailStatus(event), extra].filter(Boolean).join(', ');
}
