import test from 'node:test';
import assert from 'node:assert/strict';

import {
  channelTerms, matchesChannel, channelMatch, matchedChannels, matchedEvents, visibleChannels,
  groupsOf, groupsOfAll, visibleEvents, playbackMode, nativeHlsSupported,
  providersPending, eventMatchPending,
} from './live-model.js';

const channels = [
  { id: '1', name: 'BBC One', number: '1', group: 'News', now: { title: 'Breakfast' }, next: { title: 'Politics Live' } },
  { id: '2', name: 'Sky Sports F1', number: '10', group: 'Sports', now: { title: 'Practice 2' } },
  { id: '3', name: 'ESPN', number: '2', group: 'Sports', now: { title: 'NBA Countdown' } },
  { id: '4', name: 'TNT Sports 1', group: 'Sports', now: null },
];
const guide = new Map([['1', [{ title: 'Question Time' }]]]);

const jazzLakers = [
  { id: 'exact', name: 'Utah Jazz vs Los Angeles Lakers', number: '1', group: 'NBA' },
  { id: 'nicknames', name: 'Jazz vs Lakers', number: '2', group: 'NBA' },
  { id: 'rsn', name: 'Spectrum SportsNet', number: '3', group: 'NBA', now: { title: 'NBA: Utah Jazz at Los Angeles Lakers' } },
  { id: 'jazz-tv', name: 'Utah Jazz TV', number: '4', group: 'NBA' },
  { id: 'lakers-tv', name: 'Lakers TV', number: '5', group: 'NBA' },
  { id: 'music', name: 'Jazz Music Channel', number: '6', group: 'Music' },
  { id: 'news', name: 'Lakers News', number: '7', group: 'News' },
  { id: 'classics', name: 'Lakers Classics', number: '8', group: 'NBA' },
  { id: 'la-news', name: 'Los Angeles News', number: '9', group: 'News' },
  { id: 'utah-news', name: 'Utah News', number: '10', group: 'News' },
  { id: 'tennis', name: 'Tennis Channel', number: '11', group: 'Sports' },
  { id: 'nba', name: 'NBA TV', number: '12', group: 'NBA' },
];

test('search terms ignore single letters', () => {
  assert.deepEqual(channelTerms('a F1'), ['f1']);
  assert.deepEqual(channelTerms('  Sky   Sports '), ['sky', 'sports']);
});

test('matching covers channel, now/next and EPG titles', () => {
  assert.equal(matchesChannel(channels[0], [], ['breakfast']), true);
  assert.equal(matchesChannel(channels[0], guide.get('1'), ['question']), true);
  assert.equal(matchesChannel(channels[0], [], ['nba']), false);
  assert.equal(matchesChannel(channels[1], [], ['sky']), true);
});

test('short terms match whole words, not fragments', () => {
  assert.deepEqual(
    visibleChannels([
      { id: 'closing', name: 'Closing Bell' },
      { id: 'los', name: 'Los Angeles Lakers' },
    ], new Map(), { query: 'los' }).map((c) => c.id),
    ['los'],
  );
  const match = channelMatch({ name: 'Closing Bell' }, [], ['los']);
  assert.equal(match.matched, 0);
});

test('a matchup query keeps team evidence and rejects shared-word channels', () => {
  const got = visibleChannels(jazzLakers, new Map(), { query: 'Utah Jazz vs Los Angeles Lakers' });
  assert.deepEqual(got.map((c) => c.id), ['exact', 'rsn', 'nicknames', 'jazz-tv', 'lakers-tv']);
  // The strongest evidence is the exact matchup and the RSN whose guide names
  // both teams; a permanent team channel is weaker but still legitimate.
  for (const id of ['music', 'news', 'classics', 'la-news', 'utah-news', 'tennis', 'nba']) {
    assert.equal(got.some((channel) => channel.id === id), false, `${id} was treated as a match`);
  }
  // The same event works with the provider's "at" separator.
  assert.deepEqual(
    visibleChannels(jazzLakers, new Map(), { query: 'Utah Jazz at Los Angeles Lakers' }).map((c) => c.id),
    ['exact', 'rsn', 'nicknames', 'jazz-tv', 'lakers-tv'],
  );
});

test('matcher-ranked groups keep only their channels, in matcher order', () => {
  const groups = [
    { primary: { channelId: 'rsn' } },
    { primary: { channelId: 'exact' }, alternatives: [{ channelId: 'jazz-tv' }] },
  ];
  assert.deepEqual(
    matchedChannels(jazzLakers, groups).map((c) => c.id),
    ['rsn', 'exact', 'jazz-tv'],
  );
  assert.deepEqual(matchedChannels(jazzLakers, groups, { group: 'News' }), []);
});

test('visible channels filter by group, search and numeric order', () => {
  const all = visibleChannels(channels, guide, {});
  assert.deepEqual(all.map((c) => c.id), ['4', '1', '3', '2']);
  const sports = visibleChannels(channels, guide, { group: 'Sports' });
  assert.deepEqual(sports.map((c) => c.id), ['4', '3', '2']);
  const nba = visibleChannels(channels, guide, { query: 'nba' });
  assert.deepEqual(nba.map((c) => c.id), ['3']);
  const epg = visibleChannels(channels, guide, { query: 'question time' });
  assert.deepEqual(epg.map((c) => c.id), ['1']);
});

test('groups are unique and sorted', () => {
  assert.deepEqual(groupsOf(channels), ['News', 'Sports']);
});

test('channel groups and event groups share one filter', () => {
  const events = [
    { id: 'e1', name: 'Alpha vs Beta', group: 'Football', source: 'highfly' },
    { id: 'e2', name: 'Gamma vs Delta', group: 'Sports', source: 'highfly' },
  ];
  assert.deepEqual(groupsOfAll(channels, events), ['Football', 'News', 'Sports']);
});

test('events filter by source, group and the same search terms as channels', () => {
  const events = [
    { id: 'e1', name: 'Alpha vs Beta', group: 'Football', source: 'highfly', description: 'League opener' },
    { id: 'e2', name: 'Gamma vs Delta', group: 'Hockey', source: 'highfly' },
    { id: 'e3', name: 'Other addon event', group: 'Football', source: 'other' },
  ];
  assert.deepEqual(visibleEvents(events).map((e) => e.id), ['e1', 'e2', 'e3']);
  assert.deepEqual(visibleEvents(events, { source: 'highfly' }).map((e) => e.id), ['e1', 'e2']);
  assert.deepEqual(visibleEvents(events, { group: 'Football' }).map((e) => e.id), ['e1', 'e3']);
  assert.deepEqual(visibleEvents(events, { query: 'alpha' }).map((e) => e.id), ['e1']);
  assert.deepEqual(visibleEvents(events, { query: 'opener' }).map((e) => e.id), ['e1']);
  // An event keeps the addon's order: there is no channel number to sort by.
  assert.deepEqual(visibleEvents(events, { query: 'vs' }).map((e) => e.id), ['e1', 'e2', 'e3']);
});

test('provider selection keeps duplicate channel names apart', () => {
  const providers = [
    { id: 'a', name: 'Shared News', number: '1', group: 'News', source: 'a' },
    { id: 'b', name: 'Shared News', number: '1', group: 'News', source: 'b' },
    { id: 'a2', name: 'A Sports', number: '2', group: 'Sports', source: 'a' },
  ];
  assert.deepEqual(
    visibleChannels(providers, new Map(), { source: 'a' }).map((c) => c.id),
    ['a', 'a2'],
  );
  assert.deepEqual(
    visibleChannels(providers, new Map(), { source: 'b' }).map((c) => c.id),
    ['b'],
  );
  assert.deepEqual(
    visibleChannels(providers, new Map(), { source: 'a', group: 'Sports' }).map((c) => c.id),
    ['a2'],
  );
  // No provider filter keeps both copies, each under its own id.
  assert.deepEqual(visibleChannels(providers, new Map(), {}).map((c) => c.id), ['a', 'b', 'a2']);
});

test('an empty listing is pending only before the first provider refresh finishes', () => {
  const provider = { id: 'hf', name: 'HighFly', enabled: true };
  // Configured providers, nothing loaded, first refresh still running.
  assert.equal(providersPending({ providers: [provider], prepared: false, refreshing: true }), true);
  // The first refresh completed with nothing: a real, stable empty answer.
  assert.equal(providersPending({ providers: [provider], prepared: true, refreshing: false }), false);
  // No providers configured: there is nothing to wait for.
  assert.equal(providersPending({ providers: [], prepared: false, refreshing: true }), false);
  // A disabled provider is not waited on, and content ends the wait.
  assert.equal(providersPending({ providers: [{ ...provider, enabled: false }], prepared: false }), false);
  assert.equal(providersPending({ providers: [provider], prepared: false, channels: [{ id: '1' }] }), false);
  assert.equal(providersPending({ providers: [provider], prepared: false, events: [{ id: 'e1' }] }), false);
  assert.equal(providersPending(undefined), false);
});

test('matched provider events join the event list under the same group filter', () => {
  const events = [
    { id: 'ev1', group: 'Live Now', name: 'Boston Celtics vs Miami Heat' },
    { id: 'ev2', group: 'Replay archive', name: 'Boston Celtics vs Miami Heat Replay' },
  ];
  assert.deepEqual(matchedEvents(events).map((event) => event.id), ['ev1', 'ev2']);
  assert.deepEqual(matchedEvents(events, { group: 'Live Now' }).map((event) => event.id), ['ev1']);
  assert.deepEqual(matchedEvents(events, { group: 'Nothing' }), []);
  assert.deepEqual(matchedEvents(undefined, { group: 'Live Now' }), []);
  // An entry without an identity is never rendered as a row.
  assert.deepEqual(matchedEvents([null, { id: 'ev3' }]).map((event) => event.id), ['ev3']);
});

test('a matcher answer can say it was computed before providers were ready', () => {
  assert.equal(eventMatchPending({ ready: false, groups: [] }), true);
  assert.equal(eventMatchPending({ code: 'livetv_pending', groups: [] }), true);
  // A real empty answer is final and must stay empty.
  assert.equal(eventMatchPending({ ready: true, groups: [] }), false);
  assert.equal(eventMatchPending({}), false);
  assert.equal(eventMatchPending(undefined), false);
});

test('native HLS is Safari only, so Chromium keeps the rendition APIs', () => {
  const hlsVideo = { canPlayType: (type) => (type === 'application/vnd.apple.mpegurl' ? 'maybe' : '') };
  const plainVideo = { canPlayType: () => '' };
  const safari = 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.0 Safari/605.1.15';
  const chromium = 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Safari/537.36';
  assert.equal(nativeHlsSupported(hlsVideo, safari), true);
  // Chromium accepts the type but cannot expose audioTracks/textTracks.
  assert.equal(nativeHlsSupported(hlsVideo, chromium), false);
  assert.equal(nativeHlsSupported(plainVideo, safari), false);
  const android = 'Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0 Mobile Safari/537.36';
  assert.equal(nativeHlsSupported(hlsVideo, android), false);
});

test('live playback mode never pretends a TS stream is playable', () => {
  assert.equal(playbackMode('hls', true, false), 'native');
  assert.equal(playbackMode('hls', false, true), 'hlsjs');
  assert.equal(playbackMode('hls', false, false), 'unsupported');
  assert.equal(playbackMode('ts', true, true), 'unsupported');
});
