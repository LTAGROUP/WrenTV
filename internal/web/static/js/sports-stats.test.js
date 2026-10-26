import test from 'node:test';
import assert from 'node:assert/strict';

import { createStatsBuffer } from './sports-stats-buffer.js';
import {
  STATS_DELAY_OPTIONS, normalizeStatsDelay, delayPreferenceKey, readStatsDelay,
  writeStatsDelay, statsDelayLabel, statsDelayNote, statsSourceKey,
} from './sports-stats-delay.js';

const make = () => createStatsBuffer((value) => ({ ...value }));

// These cases are ported from MediaStorm's stats-buffer.test.ts so the web
// behaves identically, not merely similarly.
test('departing snapshot keeps every dynamic field at the same eligible time', () => {
  const b = make();
  b.append({ score: 7, final: false }, 0, 1);
  b.append({ score: 14, final: true }, 30000, 2);
  assert.deepEqual(b.select(45000, 30).value, { score: 7, final: false });
  assert.deepEqual(b.select(60000, 30).value, { score: 14, final: true });
});

test('warmup and an increased delay never fall forward to current data', () => {
  const b = make();
  b.append({ score: 7, final: false }, 100000, 1);
  assert.deepEqual(b.select(110000, 60), { kind: 'buffering' });
  assert.equal(b.select(110000, 0).kind, 'ready');
  assert.deepEqual(b.select(110000, 300), { kind: 'buffering' });
});

test('append copies values and rejects stale responses or regressing receipts', () => {
  const b = make();
  const value = { score: 7, final: false };
  b.append(value, 100, 2);
  value.score = 999;
  b.append(value, 150, 1);
  b.append(value, 99, 3);
  assert.equal(b.select(200, 0).value.score, 7);
  b.append({ score: 6, final: false }, 300, 4);
  assert.equal(b.select(310, 0).value.score, 6);
  b.clear();
  assert.deepEqual(b.select(310, 0), { kind: 'buffering' });
});

test('a source switch clears the mixed timeline instead of reusing it', () => {
  const b = make();
  b.append({ score: 3 }, 0, 1);
  b.clear();
  b.append({ score: 10 }, 100, 1);
  assert.equal(b.select(200, 0).value.score, 10);
  assert.equal(b.select(200, 30).kind, 'buffering');
});

test('history is bounded but keeps a predecessor for the maximum delay', () => {
  const b = make();
  for (let i = 0; i < 1000; i += 1) b.append({ score: i, final: false }, i * 1000, i);
  assert.ok(b.size() <= 256);
  assert.deepEqual(b.select(999000, 300), { kind: 'buffering' });
  assert.equal(b.select(999000, 0).value.score, 999);
});

test('invalid delays and clocks are rejected instead of guessed', () => {
  const b = make();
  b.append({ score: 1, final: false }, NaN, 1);
  assert.equal(b.size(), 0);
  b.append({ score: 7, final: false }, 100, 2);
  assert.deepEqual(b.select(NaN, 0), { kind: 'buffering' });
  assert.equal(b.select(100, -5).value.score, 7);
  assert.equal(b.select(100, 9999).kind, 'buffering');
});

test('delay values normalize to the five-second scale', () => {
  assert.deepEqual(STATS_DELAY_OPTIONS, [0, 15, 30, 60, 90, 120]);
  assert.equal(normalizeStatsDelay('no'), 0);
  assert.equal(normalizeStatsDelay(999), 300);
  assert.equal(normalizeStatsDelay(-5), 0);
  assert.equal(normalizeStatsDelay(32), 30);
  assert.equal(normalizeStatsDelay(NaN), 0);
});

test('delay keys isolate servers and accounts, never events', () => {
  assert.notEqual(delayPreferenceKey('a', 'p'), delayPreferenceKey('a', 'q'));
  assert.notEqual(delayPreferenceKey('a', 'p'), delayPreferenceKey('b', 'p'));
  assert.equal(delayPreferenceKey('a', 'p'), delayPreferenceKey('a', 'p'));
});

test('the stored delay is read back normalized and survives corrupt storage', () => {
  const memory = new Map();
  globalThis.localStorage = {
    getItem: (key) => (memory.has(key) ? memory.get(key) : null),
    setItem: (key, value) => memory.set(key, String(value)),
  };
  const key = delayPreferenceKey('server', 'user');
  assert.equal(readStatsDelay(key), 0);
  assert.equal(writeStatsDelay(key, 32), 30);
  assert.equal(readStatsDelay(key), 30);
  memory.set(key, '{not json');
  assert.equal(readStatsDelay(key), 0);
  memory.set(key, '"90"');
  assert.equal(readStatsDelay(key), 0);
  delete globalThis.localStorage;
});

test('labels and notes never present delayed data as live', () => {
  assert.equal(statsDelayLabel(0), 'Live');
  assert.equal(statsDelayLabel('bad'), 'Live');
  assert.equal(statsDelayLabel(30), '30s');
  assert.equal(statsDelayNote(0, false), '');
  assert.match(statsDelayNote(30, true), /Syncing stats · 30s/);
  assert.match(statsDelayNote(30, false), /delayed 30s/);
});

test('a source key identifies the feed a delay history belongs to', () => {
  assert.equal(statsSourceKey(null), '');
  assert.equal(statsSourceKey({ sourceId: 'iptv', channelId: '7' }), 'iptv|7');
  assert.notEqual(statsSourceKey({ sourceId: 'iptv', channelId: '7' }), statsSourceKey({ sourceId: 'addon', channelId: '7' }));
});

import { selectDelayedEvent } from './sports-stats-delay.js';
import { withholdLiveData, formatDetailStatus, buildEventAccessibilityLabel } from './sports-model.js';

const event = (over = {}) => ({
  id: 'evt', league: 'nba', sport: 'basketball', status: 'live', period: '3rd quarter', clock: '4:12',
  participants: [{ name: 'Celtics', score: '81' }, { name: 'Heat', score: '78' }],
  detail: { periods: [{ label: '1', away: '24', home: '22' }], stale: false },
  ...over,
});

test('a live delay withholds scores until an eligible snapshot exists', () => {
  const b = make();
  b.append(event(), 0, 1);
  const warm = selectDelayedEvent(b, event(), 30, 5000);
  assert.equal(warm.kind, 'buffering');
  assert.equal(warm.buffering, true);
  // Structure remains so the page does not collapse, but no live value leaks.
  assert.deepEqual(warm.event.participants.map((p) => p.score), ['', '']);
  assert.equal(warm.event.statusDetail, '');
  assert.equal(warm.event.detail.periods[0].home, '');
  assert.equal(formatDetailStatus(warm.event), 'Syncing stats…');
  // The HUD and card label never speak a score that has not been shown.
  assert.match(buildEventAccessibilityLabel(warm.event), /Syncing stats…/);

  const ready = selectDelayedEvent(b, event(), 30, 30001);
  assert.equal(ready.kind, 'ready');
  assert.equal(ready.event.participants[0].score, '81');
  assert.equal(ready.buffering, false);
});

test('no delay paints the latest response exactly as received', () => {
  const b = make();
  b.append(event(), 0, 1);
  const latest = event({ status: 'final', statusDetail: 'Final' });
  const picked = selectDelayedEvent(b, latest, 0, 1000);
  assert.equal(picked.event, latest);
  assert.equal(picked.buffering, false);
});

test('raising the delay buffers instead of falling forward, lowering it recovers', () => {
  const b = make();
  b.append(event({ participants: [{ name: 'A', score: '1' }, { name: 'B', score: '0' }] }), 0, 1);
  b.append(event({ participants: [{ name: 'A', score: '2' }, { name: 'B', score: '0' }] }), 20000, 2);
  const raised = selectDelayedEvent(b, event(), 60, 25000);
  assert.equal(raised.kind, 'buffering');
  assert.deepEqual(raised.event.participants.map((p) => p.score), ['', '']);
  const lowered = selectDelayedEvent(b, event(), 15, 25000);
  assert.equal(lowered.event.participants[0].score, '1');
  assert.equal(selectDelayedEvent(b, event(), 0, 25000).event.participants[0].score, '81');
});

test('switching sources clears the history so the new stream starts buffering', () => {
  const b = make();
  b.append(event(), 0, 1);
  assert.equal(selectDelayedEvent(b, event(), 0, 1000).kind, 'ready');
  b.clear();
  b.append(event({ participants: [{ name: 'A', score: '5' }, { name: 'B', score: '1' }] }), 1000, 1);
  const switched = selectDelayedEvent(b, event(), 15, 2000);
  assert.equal(switched.kind, 'buffering');
  assert.deepEqual(switched.event.participants.map((p) => p.score), ['', '']);
});

test('withheld data preserves tabs and identity but no live values', () => {
  const stripped = withholdLiveData(event({ statusDetail: 'Final', status: 'final' }));
  assert.equal(stripped.id, 'evt');
  assert.equal(stripped.detail.periods[0].label, '1');
  assert.equal(stripped.statusDetail, '');
  assert.equal(stripped.participants[0].name, 'Celtics');
  assert.equal(stripped.participants[0].winner, false);
  assert.equal(stripped.syncing, true);
  assert.equal(formatDetailStatus(stripped), 'Syncing stats…');
  assert.equal(withholdLiveData(undefined).participants.length, 0);
});
