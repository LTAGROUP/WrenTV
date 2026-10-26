// Pure sports personalization rules plus the revisioned preference transport.
// Mirrors MediaStorm's framework/preferences.ts, sports-pins.ts and
// sports-follows.ts: strict document validation, supported pin/follow updates,
// and ranking-only application that never removes data.
import { api } from './api.js';

export const EMPTY_SPORTS_PREFERENCES = Object.freeze({
  version: 1, pinnedSportIds: [], competitionIds: [], teamIds: [], athleteIds: [], schools: [],
});

// The 20 pins MediaStorm offers. Wren TV's catalog maps each league to one
// application sport, so a pin ranks every league of that sport together.
export const SUPPORTED_SPORT_PINS = [
  { id: 'american-football', league: 'nfl', label: 'American football' },
  { id: 'basketball', league: 'nba', label: 'Basketball' },
  { id: 'baseball', league: 'mlb', label: 'Baseball' },
  { id: 'ice-hockey', league: 'nhl', label: 'Ice hockey' },
  { id: 'motorsport', league: 'f1', label: 'Motorsport' },
  { id: 'cycling', league: 'cycling', label: 'Cycling' },
  { id: 'australian-football', league: '', label: 'Australian football' },
  { id: 'lacrosse', league: '', label: 'Lacrosse' },
  { id: 'volleyball', league: '', label: 'Volleyball' },
  { id: 'field-hockey', league: '', label: 'Field hockey' },
  { id: 'water-polo', league: '', label: 'Water polo' },
  { id: 'softball', league: '', label: 'Softball' },
  { id: 'golf', league: 'pga', label: 'Golf' },
  { id: 'cricket', league: 'cricket-8048', label: 'Cricket' },
  { id: 'tennis', league: 'atp', label: 'Tennis' },
  { id: 'mma', league: 'ufc', label: 'MMA' },
  { id: 'boxing', league: 'boxing', label: 'Boxing' },
  { id: 'rugby-union', league: 'rugby-180659', label: 'Rugby union' },
  { id: 'rugby-league', league: 'rugby-league-3', label: 'Rugby league' },
  { id: 'soccer', league: 'soccer-eng.1', label: 'Soccer' },
];

const followAliases = {
  'soccer-eng.1': 'premier-league',
  'soccer-esp.1': 'la-liga',
  'soccer-ger.1': 'bundesliga',
  'soccer-ita.1': 'serie-a',
  'soccer-fra.1': 'ligue-1',
  'soccer-usa.1': 'mls',
  'soccer-usa.nwsl': 'nwsl',
  'soccer-uefa.champions': 'uefa-champions-league',
  'college-football': 'ncaa-football',
  'mens-college-basketball': 'ncaa-mens-basketball',
  'womens-college-basketball': 'ncaa-womens-basketball',
};

const racingAliases = { f1: 'formula-one', nascar: 'nascar-cup', indycar: 'indycar' };

// matchupFollowId accepts either a Wren TV league id or a legacy MediaStorm
// follow id, so imported documents keep their meaning.
export function matchupFollowId(id) {
  const raw = String(id || '').trim();
  if (!raw) return '';
  const known = followAliases[raw];
  if (known) return known;
  const reverse = Object.entries(followAliases).find(([, alias]) => alias === raw);
  return reverse ? reverse[0] : raw;
}

export function racingFollowId(id) {
  return racingAliases[id] || id;
}

export function cyclingFollowId(id) {
  return String(id || '').replace(/:\d{4}$/, '');
}

export function supportedCompetitionFollows(leagues = []) {
  const choices = [];
  const seen = new Set();
  for (const league of leagues) {
    if (!league?.enabled || !league.id) continue;
    const id = matchupFollowId(league.id);
    if (seen.has(id)) continue;
    seen.add(id);
    choices.push({
      id,
      label: league.name || league.id,
      sport: league.applicationSport || league.sport || 'other',
      league: league.id,
    });
  }
  // Racing organisers may not be in the enabled catalog yet; MediaStorm still
  // offers their follows when racing itself is enabled.
  if (leagues.some((l) => l?.enabled && (l.applicationSport || l.sport) === 'motorsport')) {
    for (const [id, label] of [['formula-one', 'Formula 1'], ['nascar-cup', 'NASCAR Cup'], ['indycar', 'IndyCar']]) {
      if (!seen.has(id)) {
        seen.add(id);
        choices.push({ id, label, sport: 'motorsport', league: '' });
      }
    }
  }
  return choices;
}

function record(input) {
  if (!input || typeof input !== 'object' || Array.isArray(input)) throw new Error('Invalid sports preference document');
  return input;
}

function onlyKeys(input, allowed) {
  if (Object.keys(input).some((key) => !allowed.includes(key))) throw new Error('Unsupported sports preference field');
}

function preferenceId(input) {
  if (typeof input !== 'string' || !input.trim()) throw new Error('Expected nonempty sports preference ID');
  return input.trim();
}

function preferenceIds(input) {
  if (!Array.isArray(input)) throw new Error('Expected sports preference ID array');
  return [...new Set(input.map(preferenceId))];
}

// parseSportsPreferences is the strict browser boundary. Unknown IDs survive;
// unknown schema fields do not silently disappear.
export function parseSportsPreferences(input) {
  const value = record(input);
  if (value.version !== 1) throw new Error('Unsupported sports preference version');
  onlyKeys(value, ['version', 'pinnedSportIds', 'competitionIds', 'teamIds', 'athleteIds', 'schools']);
  if (!Array.isArray(value.schools)) throw new Error('Expected school follows');
  const schools = new Map();
  for (const raw of value.schools) {
    const school = record(raw);
    const schoolId = preferenceId(school.schoolId);
    let next;
    if (school.scope === 'all-supported') {
      onlyKeys(school, ['schoolId', 'scope']);
      next = { schoolId, scope: 'all-supported' };
    } else if (school.scope === 'selected-programs') {
      onlyKeys(school, ['schoolId', 'scope', 'programIds']);
      next = { schoolId, scope: 'selected-programs', programIds: preferenceIds(school.programIds) };
    } else {
      throw new Error('Unsupported school follow scope');
    }
    const previous = schools.get(schoolId);
    if (previous?.scope === 'all-supported') continue;
    if (previous?.scope === 'selected-programs' && next.scope === 'selected-programs') {
      next.programIds = [...new Set([...previous.programIds, ...next.programIds])];
    }
    schools.set(schoolId, next);
  }
  return {
    version: 1,
    pinnedSportIds: preferenceIds(value.pinnedSportIds),
    competitionIds: preferenceIds(value.competitionIds),
    teamIds: preferenceIds(value.teamIds),
    athleteIds: preferenceIds(value.athleteIds),
    schools: [...schools.values()],
  };
}

export function parseSportsPreferenceSnapshot(input) {
  const value = record(input);
  if (Object.keys(value).some((key) => key !== 'revision' && key !== 'preferences')) {
    throw new Error('Unsupported sports preference snapshot field');
  }
  if (typeof value.revision !== 'number' || !Number.isSafeInteger(value.revision) || value.revision < 0) {
    throw new Error('Invalid sports preference revision');
  }
  return { revision: value.revision, preferences: parseSportsPreferences(value.preferences) };
}

// updateSupportedPins replaces the supported prefix in place, preserving any
// unknown pins (including their order) from a newer client.
export function updateSupportedPins(saved, ordered) {
  const supported = new Set(SUPPORTED_SPORT_PINS.map((sport) => sport.id));
  if (ordered.some((id) => !supported.has(id))) throw new Error('Unsupported sport pin');
  const next = parseSportsPreferences(saved);
  const insertion = next.pinnedSportIds.findIndex((id) => supported.has(id));
  const kept = next.pinnedSportIds.filter((id) => !supported.has(id));
  kept.splice(insertion < 0 ? kept.length : insertion, 0, ...new Set(ordered));
  next.pinnedSportIds = kept;
  return next;
}

export function updateSupportedFollows(saved, ids, leagues = []) {
  const supported = new Set(supportedCompetitionFollows(leagues).map((item) => item.id));
  if (ids.some((id) => !supported.has(id))) throw new Error('Unsupported competition follow');
  const next = parseSportsPreferences(saved);
  next.competitionIds = [...new Set([...next.competitionIds.filter((id) => !supported.has(id)), ...ids])];
  return next;
}

// updateTeamFollows validates that every added team identity is a league:team
// pair; unknown teams may still be followed (MediaStorm keeps unknown IDs).
export function updateTeamFollows(saved, ids) {
  const next = parseSportsPreferences(saved);
  next.teamIds = [...new Set(ids.map((id) => {
    const value = preferenceId(id);
    if (!value.includes(':')) throw new Error('Team follows need a league:team id');
    return value;
  }))];
  return next;
}

export function teamFollowId(league, teamId) {
  const value = String(teamId || '').trim();
  if (!value) return '';
  return value.includes(':') ? value : `${league}:${value}`;
}

export function isTeamFollowed(preferences, league, teamId) {
  const id = teamFollowId(league, teamId);
  return !!id && (preferences?.teamIds || []).includes(id);
}

export function isCompetitionFollowed(preferences, league) {
  const id = matchupFollowId(league);
  return !!(id && (preferences?.competitionIds || []).includes(id));
}

export function isSportPinned(preferences, sport) {
  return (preferences?.pinnedSportIds || []).includes(sport);
}

// The application sport a league belongs to, used for pin ranking.
export function applicationSport(league, leagues = []) {
  const found = leagues.find((item) => item.id === league);
  return found?.applicationSport || found?.sport || league;
}

export function isFavoriteEvent(event, preferences, leagues = []) {
  if (!event || !preferences) return false;
  if (isCompetitionFollowed(preferences, event.league)) return true;
  if (isSportPinned(preferences, applicationSport(event.league, leagues))) return true;
  return (event.participants || []).some((p) => isTeamFollowed(preferences, event.league, p.id));
}

// prioritizeLanes ranks followed competitions first, then pinned sports. All
// lanes remain present; this is ordering only.
export function prioritizeLanes(lanes = [], preferences, leagues = []) {
  if (!preferences) return lanes;
  const follows = preferences.competitionIds.map(matchupFollowId);
  const pins = preferences.pinnedSportIds;
  const rank = (lane) => {
    const followed = follows.indexOf(matchupFollowId(lane.league.id));
    if (followed >= 0) return followed;
    const index = pins.indexOf(applicationSport(lane.league.id, leagues));
    return index < 0 ? Number.MAX_SAFE_INTEGER : follows.length + index;
  };
  return [...lanes].sort((a, b) => rank(a) - rank(b));
}

export function pinLabel(id) {
  return SUPPORTED_SPORT_PINS.find((sport) => sport.id === id)?.label || String(id || '');
}

export async function loadSportsPreferences() {
  return parseSportsPreferenceSnapshot(await api('/api/sports/preferences'));
}

// saveSportsPreferences requires the exact loaded snapshot; the caller reloads
// on a 409 rather than overwriting another device's edit.
export async function saveSportsPreferences(snapshot, preferences) {
  const saved = parseSportsPreferenceSnapshot(await api('/api/sports/preferences', {
    method: 'PUT',
    body: { revision: snapshot.revision, preferences: parseSportsPreferences(preferences) },
  }));
  if (saved.revision !== snapshot.revision + 1) throw new Error('Unexpected saved sports preference revision');
  return saved;
}
