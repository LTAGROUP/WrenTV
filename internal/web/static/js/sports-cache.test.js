import test from 'node:test';
import assert from 'node:assert/strict';

import { createSportsFeedCache, shareSportsData, FEED_CACHE_TTL_MS } from './sports-cache.js';

const hub = (date = 'today') => ({
  date,
  updatedAt: '2026-09-20T12:00:00Z',
  stale: false,
  events: [
    { id: '1', league: 'nba', status: 'live', participants: [{ id: 'a', score: '7' }, { id: 'b', score: '9' }] },
    { id: '2', league: 'nfl', status: 'final', participants: [{ id: 'c', score: '24' }, { id: 'd', score: '17' }] },
  ],
});

// Ported from MediaStorm's sports-refresh.test.ts.
test('a changed score retains all unrelated event and team references', () => {
  const previous = JSON.parse(JSON.stringify(hub()));
  const next = JSON.parse(JSON.stringify(previous));
  next.events[0].participants[0].score = '999';
  const shared = shareSportsData(previous, next);
  assert.deepEqual(shared, next);
  assert.equal(shared.events[1], previous.events[1]);
  assert.equal(shared.events[0].participants[1], previous.events[0].participants[1]);
  assert.notEqual(shared.events[0].participants[0], previous.events[0].participants[0]);
  assert.equal(shareSportsData(shared, JSON.parse(JSON.stringify(shared))), shared);
});

test('sharing removes deleted fields and never mutates previous snapshots', () => {
  const previous = { score: 1, marker: 'old', rows: [1, 2] };
  const next = { score: 2, rows: [1] };
  assert.deepEqual(shareSportsData(previous, next), next);
  assert.deepEqual(previous, { score: 1, marker: 'old', rows: [1, 2] });
  // Arrays changing type never keep a stale array shape.
  assert.deepEqual(shareSportsData([1, 2], { a: 1 }), { a: 1 });
  assert.equal(shareSportsData(undefined, next), next);
});

test('cached date feeds expire, cannot cross midnight or identity caches, and reuse unchanged data', () => {
  const a = createSportsFeedCache();
  const b = createSportsFeedCache();
  const now = new Date(2026, 8, 20, 12).getTime();
  const value = hub();
  a.write(value, now);
  assert.equal(a.read('today', now + 1000), value);
  assert.equal(a.read('tomorrow', now), undefined);
  assert.equal(b.read('today', now), undefined);
  assert.equal(a.write(JSON.parse(JSON.stringify(value)), now + 1000), value);
  assert.equal(a.read('today', now + FEED_CACHE_TTL_MS + 1000), undefined);
  const midnight = new Date(2026, 8, 21).getTime();
  a.write(value, midnight - 1000);
  assert.equal(a.read('today', midnight + 1000), undefined);
});

test('the cache is bounded to three dates and can be invalidated explicitly', () => {
  const cache = createSportsFeedCache();
  const now = new Date(2026, 8, 20, 12).getTime();
  for (const date of ['day-1', 'today', 'day+1', 'day+2']) cache.write(hub(date), now);
  assert.equal(cache.size(), 3);
  assert.equal(cache.read('day-1', now), undefined);
  assert.ok(cache.read('day+2', now));
  cache.clear();
  assert.equal(cache.size(), 0);
  assert.equal(cache.read('today', now), undefined);
});
