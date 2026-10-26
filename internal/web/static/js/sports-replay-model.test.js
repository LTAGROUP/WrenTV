import assert from 'node:assert/strict';
import test from 'node:test';
import {
  classifyFootballPlay, projectFieldPoint, footballReplayPath, resolveReplayPlay,
  footballReplaySituation, previousPlayTrail, footballAnimatedPath, footballPlayResult,
} from './sports-replay-model.js';

const play = (type, start, end, description = '') => ({
  id: 'p', type, start, end, description, clock: '2:00', period: 'Q1',
});

test('classifies football plays without guessing', () => {
  assert.equal(classifyFootballPlay(play('Pass Reception')), 'pass');
  assert.equal(classifyFootballPlay(play('Rush')), 'rush');
  assert.equal(classifyFootballPlay(play('Pass Incompletion')), 'incomplete');
  assert.equal(classifyFootballPlay(play('Sack')), 'sack');
  assert.equal(classifyFootballPlay(play('Penalty')), 'penalty');
  assert.equal(classifyFootballPlay(play('Punt')), 'kick');
  assert.equal(classifyFootballPlay(play('Pass Interception Return')), 'turnover');
  assert.equal(classifyFootballPlay(play('Timeout')), 'other');
  // "NO PLAY" overrides the apparent type.
  assert.equal(classifyFootballPlay(play('Rush', 2, 4, 'Penalty. NO PLAY')), 'penalty');
});

test('only complete, advancing plays get a trace', () => {
  assert.ok(footballReplayPath(play('Rush', 0, 0)));
  assert.equal(footballReplayPath(play('Rush', undefined, 5)), undefined);
  assert.equal(footballReplayPath(play('Penalty', 2, 4)), undefined);
  assert.equal(footballReplayPath(play('Rush', -5, 5)), undefined);
  assert.equal(footballReplayPath(play('Rush', 5, 105)), undefined);
});

test('projects verified field corners and midpoint', () => {
  assert.deepEqual(projectFieldPoint(0, 0), { x: 110, y: 25 });
  assert.deepEqual(projectFieldPoint(100, 1), { x: 800, y: 195 });
  assert.deepEqual(projectFieldPoint(50, 0.5), { x: 420, y: 110 });
});

test('replay cannot resolve a play outside its snapshot', () => {
  const detail = { footballDrives: [{ id: 'd', plays: [play('Rush', 20, 25)] }] };
  assert.equal(resolveReplayPlay(detail, { mode: 'replay', driveId: 'd', playId: 'future' }), undefined);
  assert.equal(resolveReplayPlay(detail, { mode: 'current' }).id, 'p');
  assert.equal(resolveReplayPlay({}, { mode: 'current' }), undefined);
});

test('away attacks right and home attacks left without flipping coordinates', () => {
  const p = { ...play('Rush', 68, 65), down: 2, distance: 6 };
  assert.deepEqual(footballReplaySituation(p, 'a', 'a', 'h'), {
    side: 'away', direction: 1, yard: 68, firstDown: 74, position: 'Opponent 32', downLabel: '2nd & 6',
  });
  assert.deepEqual(footballReplaySituation(p, 'h', 'a', 'h'), {
    side: 'home', direction: -1, yard: 68, firstDown: 62, position: 'Own 32', downLabel: '2nd & 6',
  });
  // The selected play's possession wins on a change of possession.
  assert.equal(footballReplaySituation({ ...p, possessionTeamId: 'h' }, 'a', 'a', 'h').direction, -1);
});

test('unknown possession invents neither a goal nor a first down', () => {
  const situation = footballReplaySituation(
    { ...play('Rush', 20, 30), possessionTeamId: 'unknown', distance: 10 }, 'a', 'a', 'h',
  );
  assert.equal(situation.direction, 0);
  assert.equal(situation.firstDown, undefined);
  assert.equal(situation.position, undefined);
});

test('goal-to-go is labelled without changing the reported yard', () => {
  assert.equal(footballReplaySituation({ ...play('Rush', 95, 100), down: 1, distance: 10 }, 'a', 'a', 'h').downLabel, '1st & Goal');
  assert.equal(footballReplaySituation({ ...play('Rush', 5, 0), down: 1, distance: 10 }, 'h', 'a', 'h').downLabel, '1st & Goal');
});

test('previous trace skips back only one eligible play', () => {
  const detail = {
    footballDrives: [
      { id: 'd1', teamId: 'a', plays: [{ ...play('Rush', 10, 12), id: 'p1' }, { ...play('Rush', 12, 15), id: 'p2' }] },
      { id: 'd2', teamId: 'h', plays: [{ ...play('Pass Reception', 15, 20), id: 'p3' }] },
    ],
  };
  assert.equal(previousPlayTrail(detail, 'p3').play.id, 'p2');
  assert.equal(previousPlayTrail(detail, 'p1'), undefined);
  assert.equal(previousPlayTrail(detail, 'missing'), undefined);
});

test('animated trace starts at the start and ends at the reported endpoint', () => {
  const path = footballReplayPath(play('Pass Reception', 20, 45));
  const first = footballAnimatedPath(path, 0);
  const last = footballAnimatedPath(path, 1);
  assert.deepEqual(first.end, path.start);
  assert.deepEqual(last.end, path.end);
  assert.equal(footballAnimatedPath(path, 2).end.x, path.end.x);
});

test('play results never invent a catch or yardage', () => {
  assert.equal(footballPlayResult(play('Pass Incompletion', 20, 20)), 'Incomplete pass');
  assert.equal(footballPlayResult({ ...play('Pass Reception'), yards: 12, distance: 10 }), '12-yard completion · First down');
  assert.equal(footballPlayResult({ ...play('Rush'), yards: 3, distance: 10 }), '3-yard run');
  assert.equal(footballPlayResult({ ...play('Rush'), yards: 0 }), '0-yard run');
  assert.equal(footballPlayResult(play('Punt')), 'Punt');
});
