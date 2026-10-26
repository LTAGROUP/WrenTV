import test from 'node:test';
import assert from 'node:assert/strict';

import { eventStatus, groupSportItems, streamLabel } from './sport-addon-model.js';

test('groupSportItems keeps the addon shelf labels and order', () => {
  const groups = groupSportItems([
    { id: 'a', name: 'Alpha vs Beta', group: 'Football' },
    { id: 'b', name: 'Gamma vs Delta', group: 'Tennis' },
    { id: 'c', name: 'Epsilon vs Zeta', group: 'Football' },
  ]);
  assert.deepEqual(groups.map((g) => g.name), ['Football', 'Tennis']);
  assert.deepEqual(groups[0].items.map((i) => i.id), ['a', 'c']);
});

test('groupSportItems drops items that cannot be opened and names the fallback group', () => {
  const groups = groupSportItems([
    { id: '', name: 'No identity' },
    { name: 'Missing id' },
    { id: 'x', name: 'Unlabelled event' },
  ]);
  assert.equal(groups.length, 1);
  assert.equal(groups[0].name, 'Events');
  assert.deepEqual(groups[0].items, [{ id: 'x', name: 'Unlabelled event' }]);
});

test('groupSportItems accepts an empty catalog', () => {
  assert.deepEqual(groupSportItems(), []);
  assert.deepEqual(groupSportItems([]), []);
});

test('eventStatus reads published lifecycle words only', () => {
  assert.equal(eventStatus({ releaseInfo: 'LIVE' }), 'live');
  assert.equal(eventStatus({ description: 'Alpha vs Beta\nLIVE NOW' }), 'live');
  assert.equal(eventStatus({ releaseInfo: 'UPCOMING' }), 'upcoming');
  assert.equal(eventStatus({ description: 'Tomorrow · 20:00' }), 'upcoming');
  // A scheduled date is not a status; nothing is claimed.
  assert.equal(eventStatus({ releaseInfo: '25 Oct 2026 · 17:00 UTC' }), '');
  assert.equal(eventStatus({}), '');
});

test('streamLabel falls back from name to title to position', () => {
  assert.equal(streamLabel({ name: 'TNT SPORT 1', title: '1080p' }), 'TNT SPORT 1');
  assert.equal(streamLabel({ name: '  ', title: '1080p' }), '1080p');
  assert.equal(streamLabel({}, 2), 'Feed 3');
});
