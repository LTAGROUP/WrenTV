import test from 'node:test';
import assert from 'node:assert/strict';

import {
  EMPTY_SPORTS_PREFERENCES, SUPPORTED_SPORT_PINS, parseSportsPreferences,
  parseSportsPreferenceSnapshot, updateSupportedPins, updateSupportedFollows,
  updateTeamFollows, prioritizeLanes, isFavoriteEvent, isCompetitionFollowed,
  isTeamFollowed, matchupFollowId, teamFollowId, supportedCompetitionFollows,
} from './sports-preferences.js';

const leagues = [
  { id: 'nba', name: 'NBA', sport: 'basketball', applicationSport: 'basketball', enabled: true },
  { id: 'nfl', name: 'NFL', sport: 'football', applicationSport: 'american-football', enabled: true },
  { id: 'soccer-eng.1', name: 'Premier League', sport: 'soccer', applicationSport: 'soccer', enabled: true },
  { id: 'mlb', name: 'MLB', sport: 'baseball', applicationSport: 'baseball', enabled: true },
];

const doc = (over = {}) => ({
  version: 1, pinnedSportIds: [], competitionIds: [], teamIds: [], athleteIds: [], schools: [], ...over,
});

test('document boundary rejects unknown fields and versions', () => {
  assert.throws(() => parseSportsPreferences({ ...doc(), extra: true }), /Unsupported/);
  assert.throws(() => parseSportsPreferences(doc({ version: 2 })), /version/);
  assert.throws(() => parseSportsPreferences({ ...doc(), teamIds: [1] }), /nonempty/);
  assert.throws(() => parseSportsPreferences({ ...doc(), teamIds: [''] }), /nonempty/);
  assert.throws(() => parseSportsPreferences(doc({ schools: {} })), /school/);
  assert.throws(() => parseSportsPreferences(doc({ schools: [{ schoolId: 's', scope: 'some' }] })), /scope/);
});

test('dedupes IDs while unknown IDs survive', () => {
  const parsed = parseSportsPreferences(doc({
    pinnedSportIds: ['basketball', 'basketball', 'future-sport'],
    competitionIds: ['premier-league'], teamIds: ['nba:2'], athleteIds: ['a:9'],
  }));
  assert.deepEqual(parsed.pinnedSportIds, ['basketball', 'future-sport']);
  assert.deepEqual(parsed.competitionIds, ['premier-league']);
  assert.deepEqual(parsed.teamIds, ['nba:2']);
});

test('school follows merge and all-supported wins', () => {
  const parsed = parseSportsPreferences(doc({
    schools: [
      { schoolId: 'duke', scope: 'selected-programs', programIds: ['mbb'] },
      { schoolId: 'duke', scope: 'selected-programs', programIds: ['wbb', 'mbb'] },
      { schoolId: 'unc', scope: 'all-supported' },
      { schoolId: 'unc', scope: 'selected-programs', programIds: ['mbb'] },
    ],
  }));
  assert.deepEqual(parsed.schools, [
    { schoolId: 'duke', scope: 'selected-programs', programIds: ['mbb', 'wbb'] },
    { schoolId: 'unc', scope: 'all-supported' },
  ]);
});

test('snapshot requires an exact safe revision', () => {
  assert.equal(parseSportsPreferenceSnapshot({ revision: 4, preferences: doc() }).revision, 4);
  assert.throws(() => parseSportsPreferenceSnapshot({ revision: -1, preferences: doc() }));
  assert.throws(() => parseSportsPreferenceSnapshot({ revision: 1.5, preferences: doc() }));
  assert.throws(() => parseSportsPreferenceSnapshot({ revision: 1, preferences: doc(), extra: 1 }), /snapshot/);
});

test('pin updates keep supported pins contiguous and unknown pins untouched', () => {
  const saved = parseSportsPreferences(doc({ pinnedSportIds: ['future-sport', 'basketball', 'soccer'] }));
  const next = updateSupportedPins(saved, ['soccer', 'baseball']);
  assert.deepEqual(next.pinnedSportIds, ['future-sport', 'soccer', 'baseball']);
  assert.throws(() => updateSupportedPins(saved, ['not-a-sport']), /Unsupported/);
  assert.equal(SUPPORTED_SPORT_PINS.length, 20);
});

test('follow updates reject unsupported competitions but keep existing unknown ones', () => {
  const saved = parseSportsPreferences(doc({ competitionIds: ['future-cup'] }));
  const next = updateSupportedFollows(saved, ['premier-league'], leagues);
  assert.deepEqual(next.competitionIds, ['future-cup', 'premier-league']);
  assert.throws(() => updateSupportedFollows(saved, ['not-enabled'], leagues), /Unsupported/);
  assert.equal(supportedCompetitionFollows(leagues).find((x) => x.league === 'soccer-eng.1').id, 'premier-league');
});

test('team follows need league:team identities', () => {
  const saved = EMPTY_SPORTS_PREFERENCES;
  const next = updateTeamFollows(saved, [teamFollowId('nba', '2'), 'nba:2']);
  assert.deepEqual(next.teamIds, ['nba:2']);
  assert.throws(() => updateTeamFollows(saved, ['bare-team']), /league:team/);
});

test('follow matching accepts legacy competition aliases', () => {
  assert.equal(matchupFollowId('soccer-eng.1'), 'premier-league');
  assert.equal(matchupFollowId('premier-league'), 'soccer-eng.1');
  const prefs = parseSportsPreferences(doc({ competitionIds: ['premier-league'] }));
  assert.equal(isCompetitionFollowed(prefs, 'soccer-eng.1'), true);
  assert.equal(isCompetitionFollowed(prefs, 'nba'), false);
});

test('favorite events come from followed competitions, teams and pinned sports', () => {
  const prefs = parseSportsPreferences(doc({
    competitionIds: ['nba'], teamIds: ['mlb:10'], pinnedSportIds: ['soccer'],
  }));
  const nba = { id: '1', league: 'nba', participants: [] };
  const mlb = { id: '2', league: 'mlb', participants: [{ id: '10' }] };
  const soccer = { id: '3', league: 'soccer-eng.1', participants: [] };
  const nfl = { id: '4', league: 'nfl', participants: [] };
  assert.equal(isFavoriteEvent(nba, prefs, leagues), true);
  assert.equal(isFavoriteEvent(mlb, prefs, leagues), true);
  assert.equal(isFavoriteEvent(soccer, prefs, leagues), true);
  assert.equal(isFavoriteEvent(nfl, prefs, leagues), false);
  assert.equal(isTeamFollowed(prefs, 'mlb', '10'), true);
});

test('lane prioritization ranks follows then pins and keeps every lane', () => {
  const lanes = [
    { league: { id: 'nba' }, events: [1] },
    { league: { id: 'nfl' }, events: [1] },
    { league: { id: 'mlb' }, events: [1] },
    { league: { id: 'soccer-eng.1' }, events: [1] },
  ];
  const prefs = parseSportsPreferences(doc({
    competitionIds: ['mlb'], pinnedSportIds: ['soccer', 'american-football'],
  }));
  const ranked = prioritizeLanes(lanes, prefs, leagues);
  assert.deepEqual(ranked.map((lane) => lane.league.id), ['mlb', 'soccer-eng.1', 'nfl', 'nba']);
  assert.equal(prioritizeLanes(lanes, null, leagues).length, 4);
});
