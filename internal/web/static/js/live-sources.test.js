import test from 'node:test';
import assert from 'node:assert/strict';

import {
  ALL_PROVIDERS, channelsForSource, defaultSource, eventsForSource, isAddonSource, parseSource,
  sourceName, sourceOptions, sourceValue,
} from './live-sources.js';

const providers = [
  { id: 'iptv', name: 'Main IPTV', kind: 'm3u', enabled: true, channels: 120 },
  { id: 'xt', name: 'Second IPTV', kind: 'xtream', enabled: true, channels: 40 },
  { id: 'tvaddon', name: 'TV addon', kind: 'stremio', addon: true, enabled: true, channels: 12 },
  { id: 'highfly', name: 'HighFly', kind: 'sport', addon: true, enabled: true, channels: 0, events: 3 },
];
const channels = [
  { id: 'c1', name: 'News', source: 'iptv', number: '1' },
  { id: 'c2', name: 'Sport', source: 'xt', number: '2' },
  { id: 'c3', name: 'Addon TV', source: 'tvaddon' },
];
const events = [
  { id: 'e1', name: 'Alpha vs Beta', source: 'highfly' },
  { id: 'e2', name: 'Gamma vs Delta', source: 'highfly' },
];

test('the picker is one flat list: IPTV and Stremio sources sit side by side', () => {
  const options = sourceOptions({ providers });
  assert.deepEqual(options.map((option) => option.value), [
    'all', 'iptv:iptv', 'iptv:xt', 'addon:tvaddon', 'addon:highfly',
  ]);
  // No group headings and no extra section for addons.
  assert.equal(options.some((option) => Array.isArray(option.options) || option.group), false);
  assert.equal(options[0].label, 'All providers');
  // The kind stays inside the value, so the server uses the right integration.
  assert.deepEqual(options[0], { value: ALL_PROVIDERS, label: 'All providers', kind: 'all', id: '' });
  assert.deepEqual(options[4], {
    value: 'addon:highfly', label: 'HighFly · 3 events', kind: 'addon', id: 'highfly',
  });
  assert.equal(options[3].label, 'TV addon · 12 channels');
});

test('the addon flag from the API wins over the stored kind', () => {
  assert.equal(isAddonSource({ id: 'a', kind: 'stremio' }), true);
  assert.equal(isAddonSource({ id: 'a', kind: 'sport' }), true);
  assert.equal(isAddonSource({ id: 'a', kind: 'm3u', addon: false }), false);
  assert.equal(isAddonSource({ id: 'a', kind: 'm3u', addon: true }), true);
});

test('a disabled source stays identifiable, and a source without counts has no suffix', () => {
  const options = sourceOptions({
    providers: [
      { id: 'off', name: 'Old IPTV', kind: 'm3u', enabled: false, channels: 0 },
      { id: 'quiet', name: 'Quiet addon', kind: 'stremio', addon: true, enabled: true, channels: 0, events: 0 },
    ],
  });
  assert.equal(options[1].label, 'Old IPTV (disabled)');
  assert.equal(options[2].label, 'Quiet addon');
});

test('sources with the same name are told apart by id', () => {
  const options = sourceOptions({
    providers: [
      { id: 'a', name: 'Shared', kind: 'm3u', channels: 1 },
      { id: 'b', name: 'Shared', kind: 'm3u', channels: 2 },
    ],
  });
  assert.deepEqual(options.map((option) => option.label), [
    'All providers', 'Shared (a) · 1 channel', 'Shared (b) · 2 channels',
  ]);
});

test('the list opens on All providers, never on one source', () => {
  assert.equal(defaultSource(), ALL_PROVIDERS);
});

test('source values round-trip and unknown values fall back to All providers', () => {
  assert.deepEqual(parseSource(sourceValue('iptv', 'iptv:odd')), { kind: 'iptv', id: 'iptv:odd' });
  assert.deepEqual(parseSource('addon:highfly'), { kind: 'addon', id: 'highfly' });
  assert.deepEqual(parseSource('all'), { kind: 'all', id: '' });
  assert.deepEqual(parseSource(''), { kind: 'all', id: '' });
  assert.deepEqual(parseSource('iptv:'), { kind: 'all', id: '' });
  assert.deepEqual(parseSource('nonsense:1'), { kind: 'all', id: '' });
});

test('selecting a source filters its own channels, and All keeps every one', () => {
  assert.deepEqual(channelsForSource(channels, 'iptv:iptv').map((c) => c.id), ['c1']);
  assert.deepEqual(channelsForSource(channels, 'iptv:xt').map((c) => c.id), ['c2']);
  assert.deepEqual(channelsForSource(channels, 'addon:tvaddon').map((c) => c.id), ['c3']);
  assert.deepEqual(channelsForSource(channels, ALL_PROVIDERS).map((c) => c.id), ['c1', 'c2', 'c3']);
});

test('the same source value filters events, so one picker covers both', () => {
  assert.deepEqual(eventsForSource(events, 'addon:highfly').map((e) => e.id), ['e1', 'e2']);
  assert.deepEqual(eventsForSource(events, 'iptv:iptv'), []);
  assert.deepEqual(eventsForSource(events, ALL_PROVIDERS).map((e) => e.id), ['e1', 'e2']);
  // An event is never mistaken for another provider's channel.
  assert.deepEqual(channelsForSource(channels, 'addon:highfly'), []);
});

test('source names stay human-readable for headings', () => {
  assert.equal(sourceName('all', { providers }), 'All providers');
  assert.equal(sourceName('addon:highfly', { providers }), 'HighFly');
  assert.equal(sourceName('iptv:xt', { providers }), 'Second IPTV');
  assert.equal(sourceName('iptv:unknown', { providers }), 'unknown');
});
