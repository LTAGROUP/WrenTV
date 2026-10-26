import assert from 'node:assert/strict';
import test from 'node:test';
import {
  tennisTiebreakSuperscript, tennisEventContext, tennisSets,
  rugbyPlaySymbol, rugbyLatestPlays, rugbyStatus,
  combatPortrait, combatTape, combatBouts, combatResult, combatBarValues,
} from './sports-sport-model.js';

test('tennis tiebreak points are superscript, including zero', () => {
  assert.equal(tennisTiebreakSuperscript(0), '⁰');
  assert.equal(tennisTiebreakSuperscript(10), '¹⁰');
  assert.equal(tennisTiebreakSuperscript(undefined), '');
  assert.equal(tennisTiebreakSuperscript(-1), '');
  assert.equal(tennisTiebreakSuperscript(1.5), '');
});

test('tennis context falls back to the prefixed title', () => {
  const event = {
    league: 'atp',
    title: 'Wimbledon: Carlos Alcaraz vs Jannik Sinner',
    participants: [{ name: 'Carlos Alcaraz' }, { name: 'Jannik Sinner' }],
  };
  assert.equal(tennisEventContext(event), 'Wimbledon');
  assert.equal(tennisEventContext({ ...event, league: 'nba' }), undefined);
  assert.equal(tennisEventContext({ ...event, eventContext: 'Final' }), 'Final');
});

test('tennis sets keep reported tiebreaks', () => {
  const sets = tennisSets({
    detail: {
      periods: [
        { label: 'Set 1', away: '7', home: '6', awayTiebreak: 7, homeTiebreak: 4, awayWinner: true, homeWinner: false },
        { label: 'Set 2', away: '6', home: '4' },
      ],
    },
  });
  assert.equal(sets.length, 2);
  assert.equal(sets[0].awayTiebreakLabel, '⁷');
  assert.equal(sets[0].homeTiebreakLabel, '⁴');
  assert.equal(sets[1].awayTiebreakLabel, '');
  assert.equal(sets[0].awayWinner, true);
});

test('rugby play symbols describe provider events only', () => {
  assert.equal(rugbyPlaySymbol('Try scored'), 'T');
  assert.equal(rugbyPlaySymbol('Conversion missed'), '×');
  assert.equal(rugbyPlaySymbol('Penalty try awarded'), 'PT');
  assert.equal(rugbyPlaySymbol('Yellow card'), 'YC');
  assert.equal(rugbyPlaySymbol('Red card'), 'RC');
  assert.equal(rugbyPlaySymbol('Substitution'), '↔');
  assert.equal(rugbyPlaySymbol('Penalty goal'), 'PG');
  assert.equal(rugbyPlaySymbol('Drop goal'), 'DG');
  assert.equal(rugbyPlaySymbol('Knock on'), '•');
});

test('rugby plays sort only when every clock is usable', () => {
  const plays = [
    { clock: "5'", title: 'a' },
    { clock: "45+2'", title: 'b' },
    { clock: "40'", title: 'c' },
  ];
  assert.deepEqual(rugbyLatestPlays(plays).map((play) => play.title), ['b', 'c', 'a']);
  const unknown = [{ clock: '12:00', title: 'x' }, { clock: "5'", title: 'y' }];
  assert.deepEqual(rugbyLatestPlays(unknown).map((play) => play.title), ['x', 'y']);
  assert.deepEqual(rugbyLatestPlays(undefined), []);
});

test('rugby status uses half-time and provider clocks', () => {
  assert.equal(rugbyStatus({ status: 'final' }), 'Full time');
  assert.equal(rugbyStatus({ status: 'live', statusDetail: 'HT' }), 'Half time');
  assert.equal(rugbyStatus({ status: 'live', detail: { periodLabel: '2nd' }, clock: "62'" }), 'Second half · 62\'');
  assert.equal(rugbyStatus({ status: 'scheduled' }), 'Scheduled');
});

test('combat results never invent a winner', () => {
  const base = {
    status: 'final',
    statusDetail: 'Final',
    participants: [{ name: 'A', winner: true }, { name: 'B', winner: false }],
    period: 'R5',
    clock: '2:14',
  };
  assert.equal(combatResult(base), 'A wins · R5 · 2:14');
  assert.equal(combatResult({ ...base, participants: [{ name: 'A' }, { name: 'B' }] }), 'Result not supplied · R5 · 2:14');
  assert.equal(combatResult({ status: 'scheduled' }), 'Scheduled');
  assert.equal(combatResult({ status: 'live', period: 'R2', clock: '1:00' }), 'R2 · 1:00');
  assert.equal(combatResult(base, true), 'Result hidden');
  assert.deepEqual(combatBouts({ combat: { bouts: [{ id: '1' }] } }), [{ id: '1' }]);
  assert.deepEqual(combatBouts(undefined), []);
});

test('combat bar values compare only real counts', () => {
  assert.deepEqual(combatBarValues('Strikes', '10', 20), [10, 20]);
  assert.deepEqual(combatBarValues('Control time', '2:30', '1:15'), [150, 75]);
  assert.equal(combatBarValues('Accuracy', '55%', '60%'), undefined);
  assert.equal(combatBarValues('Strikes', '10', 'n/a'), undefined);
});

test('combat portraits prefer a real image and never invent one for boxing', () => {
  const ufc = { league: 'ufc', participants: [{ id: '3166126' }] };
  assert.equal(combatPortrait(ufc, ufc.participants[0]), 'https://a.espncdn.com/i/headshots/mma/players/full/3166126.png');
  assert.equal(
    combatPortrait(ufc, { id: '3166126', logoUrl: 'https://a.espncdn.com/i/headshots/mma/players/full/3166126.png' }),
    'https://a.espncdn.com/i/headshots/mma/players/full/3166126.png',
  );
  // A team crest is not a fighter portrait.
  assert.equal(
    combatPortrait(ufc, { id: '3166126', logoUrl: 'https://a.espncdn.com/i/teamlogos/mma/500/x.png' }),
    'https://a.espncdn.com/i/headshots/mma/players/full/3166126.png',
  );
  assert.equal(combatPortrait({ league: 'boxing' }, { id: '3166126' }), undefined);
  assert.equal(combatPortrait(ufc, { id: 'not-a-number' }), undefined);
});

test('tale of the tape shows only reported profile values', () => {
  const event = {
    participants: [{ id: 'a' }, { id: 'b' }],
    detail: {
      fighterProfiles: [
        { id: 'a', stats: [{ label: 'Height', value: "6' 1\"" }, { label: 'Reach', value: '75"' }] },
        { id: 'b', stats: [{ label: 'Height', value: "5' 11\"" }] },
      ],
    },
  };
  assert.deepEqual(combatTape(event), [
    { label: 'Height', away: "6' 1\"", home: "5' 11\"" },
    { label: 'Reach', away: '75"', home: '' },
  ]);
  assert.deepEqual(combatTape({ participants: [{ id: 'a' }, { id: 'b' }] }), []);
});
