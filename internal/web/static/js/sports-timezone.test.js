// P7 timezone audit: the hub's rail is the viewer's calendar day, not UTC's.
// These cases construct local dates on purpose, so the same assertions hold in
// every zone; the verification run repeats the file under several TZ values.
import test from 'node:test';
import assert from 'node:assert/strict';

import { localDate, shiftDate, dateChoices, startLabel, freshness } from './sports-model.js';

test('the rail follows the viewer day across midnight and a DST change', () => {
  // 8 March 2026 is the US spring-forward date; the rail must still be three
  // consecutive local days, with no gap or repeat.
  const late = new Date(2026, 2, 8, 23, 45);
  assert.equal(localDate(late), '2026-03-08');
  assert.equal(shiftDate(late, 1), '2026-03-09');
  assert.equal(shiftDate(late, -1), '2026-03-07');
  const rail = dateChoices(late);
  assert.deepEqual(rail.map((day) => day.date), ['2026-03-07', '2026-03-08', '2026-03-09']);
  assert.deepEqual(rail.map((day) => day.id), ['yesterday', 'today', 'tomorrow']);
  for (const day of rail) {
    const midday = new Date(`${day.date}T12:00:00`);
    assert.equal(day.weekday, midday.toLocaleDateString([], { weekday: 'short' }));
    assert.equal(day.day, midday.toLocaleDateString([], { month: 'short', day: 'numeric' }));
  }
});

test('a start time is a clock time today and a dated time on another day', () => {
  const noon = new Date(2026, 9, 24, 12, 0);
  const evening = new Date(2026, 9, 24, 21, 30);
  const tomorrow = new Date(2026, 9, 25, 9, 5);
  assert.equal(
    startLabel({ startTime: evening.toISOString() }, noon),
    evening.toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' }),
  );
  const dated = startLabel({ startTime: tomorrow.toISOString() }, noon);
  assert.ok(dated.includes(tomorrow.toLocaleDateString([], { month: 'short', day: 'numeric' })));
  // An unusable provider timestamp stays empty instead of showing NaN or "now".
  assert.equal(startLabel({ startTime: 'not-a-date' }, noon), '');
});

test('freshness reads the timestamp as an instant, not a local day', () => {
  const now = new Date(2026, 9, 24, 23, 59);
  const justChecked = new Date(now.getTime() - 30_000).toISOString();
  assert.equal(freshness(justChecked, false, now).label.startsWith('Feed checked '), true);
  const old = new Date(now.getTime() - 300_000).toISOString();
  assert.equal(freshness(old, false, now).label, 'Scores may be delayed');
  assert.equal(freshness(old, false, now).stale, true);
});
