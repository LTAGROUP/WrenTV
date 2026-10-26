import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';

import {
  confidenceLabel, qualityLabel, streamSubtitle, previewAllowed, searchNote,
  feedPriority, watchDecision, probeKey, sortProbedFeeds, preferLastSource, probeLabel,
  probeProgressText, probedQualityLines, probeError, watchQuery,
  providerMode, providerStateLabel, providerEmptyNote, watchCandidates,
  watchTarget, candidateSubtitle,
} from './sports-watch-model.js';

const group = (over = {}) => ({
  displayName: 'NBA TV',
  confidenceTier: 'strong',
  alternatives: [],
  primary: { channelId: 'c1', channelName: 'NBA TV', sourceId: 's1', confidence: 0.9 },
  ...over,
});

test('confidence tiers are labelled without overclaiming', () => {
  assert.equal(confidenceLabel('strong'), 'Strong match');
  assert.equal(confidenceLabel('possible'), 'Possible match');
  assert.equal(confidenceLabel('manual'), 'Manual search');
  assert.equal(confidenceLabel(undefined), 'Possible match');
});

test('quality labels come only from advertised values', () => {
  assert.equal(qualityLabel(null), '');
  assert.equal(qualityLabel({}), '');
  assert.equal(qualityLabel({ resolutionHeight: 2160 }), '2160p');
  assert.equal(qualityLabel({ resolutionHeight: 1080, bitrateBps: 8000000 }), '1080p · 8.0 Mbps');
  assert.equal(qualityLabel({ bitrateBps: 4500000 }), '4.5 Mbps');
});

test('stream subtitle lists reason, source and feed count', () => {
  assert.equal(streamSubtitle({ primary: { matchReason: 'Both team nicknames', sourceName: 'Provider' }, feedCount: 2 }),
    'Both team nicknames · Provider · 2 feeds');
  assert.equal(streamSubtitle({ primary: {}, feedCount: 1 }), '');
});

test('preview requires a playable channel reference', () => {
  assert.equal(previewAllowed({ primary: { channelId: 'abc', confidence: 0.9 } }), true);
  assert.equal(previewAllowed({ primary: { channelId: '', confidence: 0.9 } }), false);
  assert.equal(previewAllowed({ primary: { confidence: 0.9 } }), false);
});

test('manual search explains that scoring is bypassed', () => {
  assert.equal(searchNote(''), '');
  assert.match(searchNote('Celtics'), /Celtics/);
  assert.match(searchNote('Celtics'), /bypass match scoring/);
});

test('feed priority puts live first and scheduled last', () => {
  assert.equal(feedPriority(group()), 1);
  assert.equal(feedPriority(group({ primary: { channelName: 'NBA TV Live' } })), 0);
  assert.equal(feedPriority(group({ primary: { channelName: 'NBA TV', programTitle: 'Celtics vs Heat Live' } })), 0);
  assert.equal(feedPriority(group({ primary: { channelName: 'NBA TV', programTitle: 'Celtics vs Heat (Upcoming)' } })), 2);
  assert.equal(feedPriority(group({ primary: { channelName: 'Starting soon: NBA' } })), 2);
});

test('a lone strong feed plays directly; everything else asks', () => {
  assert.equal(watchDecision([]).kind, 'unavailable');
  assert.equal(watchDecision([group({ confidenceTier: 'weak' })]).kind, 'unavailable');
  const direct = watchDecision([group()]);
  assert.equal(direct.kind, 'direct');
  assert.equal(direct.group.primary.channelId, 'c1');
  assert.equal(watchDecision([group({ confidenceTier: 'possible' })]).kind, 'choose');
  assert.equal(watchDecision([group(), group({ primary: { channelId: 'c2' } })]).kind, 'choose');
  assert.equal(watchDecision([group({ alternatives: [{ channelId: 'alt' }] })]).kind, 'choose');
  // A feed labelled upcoming is not a confirmed live broadcast, so never direct.
  assert.equal(watchDecision([group({ primary: { channelName: 'NBA TV', programTitle: 'Game upcoming' } })]).kind, 'choose');
});

test('a probe identity is stable per source channel', () => {
  assert.equal(probeKey({ sourceId: 'iptv', channelId: '7' }), 'iptv|7');
  assert.notEqual(probeKey({ sourceId: 'iptv', channelId: '7' }), probeKey({ sourceId: 'addon', channelId: '7' }));
});

test('measured resolution overrides labels and unknown sorts last', () => {
  const low = group({ primary: { channelId: 'low', reportedQuality: { resolutionHeight: 2160 } } });
  const high = group({ primary: { channelId: 'high' } });
  const unknown = group({ primary: { channelId: 'unknown' } });
  const results = {
    [probeKey(low.primary)]: { quality: { height: 540 } },
    [probeKey(high.primary)]: { quality: { height: 1080, adaptive: true } },
  };
  assert.deepEqual(
    sortProbedFeeds([unknown, low, high], results).map((g) => g.primary.channelId),
    ['high', 'low', 'unknown'],
  );
});

test('quality lines describe measured values and never claim adaptive quality', () => {
  const lines = probedQualityLines({
    quality: {
      width: 1920, height: 1080, codec: 'h264', frameRate: 59.94, bitrateBps: 8000000,
      audio: [{ codec: 'aac', channels: 2, language: 'en' }, { codec: 'aac', channels: 2, language: 'en' }],
      adaptive: true,
    },
  });
  assert.deepEqual(lines, [
    'Available video · 1920 × 1080 · 59.94 fps · H264 · 8 Mbps',
    'Audio · AAC · 2 ch · en',
    'Adaptive stream · playback quality may vary',
  ]);
  assert.deepEqual(probedQualityLines({ error: 'Check timed out' }), ['Check timed out']);
  assert.deepEqual(probedQualityLines(undefined), []);
  assert.deepEqual(probedQualityLines({ quality: { height: 720, adaptive: false } }), ['Detected video · 720p']);
});

test('probe errors map to honest messages', () => {
  assert.match(probeError({ status: 404 }), /unavailable/);
  assert.match(probeError({ status: 409 }), /connection limit/);
  assert.match(probeError({ status: 429 }), /busy/);
  assert.match(probeError({ message: 'Quality check timed out' }), /timed out/);
  assert.match(probeError({ status: 502 }), /playback may still work/);
});

test('probe labels and progress keep completed results', () => {
  assert.equal(probeLabel({ running: true }, {}, 2), 'Cancel quality check');
  assert.equal(probeLabel({ running: false }, {}, 2), 'Check stream quality');
  assert.equal(probeLabel({ running: false }, { 'a|1': {} }, 2), 'Check remaining streams');
  assert.equal(probeLabel({ running: false }, { 'a|1': {}, 'a|2': {} }, 2), 'Recheck quality');
  assert.equal(
    probeProgressText({ running: true, completed: 1, total: 3, failed: 0 }),
    'Checking 2 of 3 · 1 completed · 0 inconclusive',
  );
  assert.equal(
    probeProgressText({ running: false, total: 3, completed: 2, failed: 1, cancelled: true }),
    'Cancelled · 2 of 3 checked · 1 inconclusive · results kept',
  );
});

test('race sessions and cycling stages keep their kind and weekend', () => {
  // Without these the server has no board entry to resolve and answers 404.
  assert.equal(
    watchQuery({ id: 'race-session:1', streamKind: 'racing', parentId: 'espn:f1:600' }),
    '?kind=racing&parentId=espn%3Af1%3A600',
  );
  assert.equal(
    watchQuery({ id: 'stage:2', streamKind: 'cycling', parentId: 'tour-de-france' }, 'tour de france'),
    '?kind=cycling&parentId=tour-de-france&q=tour+de+france',
  );
  // An ordinary matchup adds nothing and keeps a plain search parameter.
  assert.equal(watchQuery({ id: '401' }), '');
  assert.equal(watchQuery({ id: '401' }, 'nba tv'), '?q=nba+tv');
  // A kind without a known weekend still travels, so the server can say why.
  assert.equal(watchQuery({ id: '401', streamKind: 'racing' }), '?kind=racing');
});

test('a finished event searches for a replay, everything else for a live feed', () => {
  assert.equal(providerMode({ status: 'final' }), 'replay');
  assert.equal(providerMode({ status: 'live' }), 'live');
  assert.equal(providerMode({ status: 'scheduled' }), 'live');
  assert.equal(providerMode({}), 'live');
});

test('provider states are reported instead of silently dropping a provider', () => {
  assert.equal(providerStateLabel('complete'), 'Matched');
  assert.equal(providerStateLabel('empty'), 'Listed nothing for this event');
  assert.equal(providerStateLabel('no-match'), 'No matching listing');
  assert.equal(providerStateLabel('no-feed'), 'Listed, but no playable replay');
  assert.equal(providerStateLabel('unsupported'), 'No playable catalogs');
  assert.equal(providerStateLabel('unavailable'), 'Unavailable');
  assert.equal(providerStateLabel('anything-else'), 'Answered');
});

test('a replay is never promised when no provider listed one', () => {
  const sources = [{ name: 'HighFly' }, { name: 'Other' }];
  const note = providerEmptyNote(true, sources);
  assert.match(note, /No replay was found/);
  assert.match(note, /HighFly, Other/);
  assert.match(note, /catch-up and recording are not implemented/);
  assert.doesNotMatch(note, /is available/);
  assert.match(providerEmptyNote(false, sources), /No matching stream/);
  assert.match(providerEmptyNote(false, []), /your Live TV providers/);
});

// A playlist channel and a Stremio item are the same kind of answer to "what
// can I watch this event on": one list, one Watch action, and only the
// play request differs underneath.
const providerMatch = (over = {}) => ({
  id: 'addon:sport:ev:alpha',
  source: 'addon', sourceName: 'Addon Provider', type: 'sport', itemId: 'ev:alpha',
  name: 'Alpha vs Beta', confidence: 0.98, confidenceTier: 'strong',
  matchReason: 'Both team names', mode: 'live',
  ...over,
});

test('channels and Stremio items share one candidate list', () => {
  const candidates = watchCandidates({
    groups: [group()],
    providers: [providerMatch()],
  });
  assert.deepEqual(candidates.map((candidate) => candidate.kind).sort(), ['channel', 'provider']);
  // Both rows expose the same identity fields to the row renderer.
  for (const candidate of candidates) {
    assert.ok(candidate.name);
    assert.ok(['strong', 'possible'].includes(candidate.tier));
    assert.ok(candidate.key);
  }
  // A provider item that can never be played is not a row.
  assert.deepEqual(watchCandidates({ providers: [providerMatch({ itemId: '' })] }), []);
  assert.deepEqual(watchCandidates({ groups: [{ primary: { confidence: 0.9 } }] }), []);
});

// Preview is a capability, not a channel privilege: a Stremio item is
// previewable from exactly the identity its playback route needs, so it keeps
// the button beside Watch instead of losing it because its transport differs.
test('every playable candidate can be previewed, in its own terms', () => {
  const [channelCandidate, providerCandidate] = watchCandidates({
    groups: [group()],
    providers: [providerMatch()],
  }).sort((a, b) => a.kind.localeCompare(b.kind));
  assert.equal(previewAllowed(channelCandidate), true);
  assert.equal(previewAllowed(providerCandidate), true);
  // A candidate that cannot be played at all is offered neither action.
  assert.equal(previewAllowed({ kind: 'provider', provider: { source: 'addon', type: 'sport' } }), false);
  assert.equal(previewAllowed({ kind: 'provider', provider: {} }), false);
  assert.equal(previewAllowed(undefined), false);
});

test('one Watch action knows which Live TV integration starts the source', () => {
  const candidates = watchCandidates({ groups: [group()], providers: [providerMatch()] });
  const channelCandidate = candidates.find((candidate) => candidate.kind === 'channel');
  const providerCandidate = candidates.find((candidate) => candidate.kind === 'provider');
  const channel = watchTarget(channelCandidate);
  assert.equal(channel.channelId, 'c1');
  assert.equal(channel.addonAuto, undefined);
  // Without a source name the ordinary Live TV label is used, never a blank.
  assert.equal(channel.group, 'Live TV');
  assert.equal(channel.probe, 's1|c1');
  const provider = watchTarget(providerCandidate);
  assert.equal(provider.channelId, undefined);
  assert.deepEqual(provider.addonAuto, { source: 'addon', type: 'sport', id: 'ev:alpha' });
  assert.equal(provider.group, 'Addon Provider');
  // A provider id is remembered in its own namespace, never as a channel.
  assert.equal(provider.probe, 'provider|addon:sport:ev:alpha');
  assert.equal(watchTarget({ kind: 'provider', provider: {} }), null);
  assert.equal(watchTarget(undefined), null);
});

test('every candidate is described the same way', () => {
  const candidates = watchCandidates({
    groups: [group({ primary: { channelId: 'c1', channelName: 'NBA TV', sourceName: 'Provider', matchReason: 'Both team names' }, feedCount: 2 })],
    providers: [providerMatch({ feeds: 2 })],
  });
  const channelCandidate = candidates.find((candidate) => candidate.kind === 'channel');
  const providerCandidate = candidates.find((candidate) => candidate.kind === 'provider');
  assert.equal(candidateSubtitle(channelCandidate), 'Both team names · Provider · 2 feeds');
  assert.equal(candidateSubtitle(providerCandidate), 'Both team names · Addon Provider · 2 feeds');
  assert.equal(candidateSubtitle({ kind: 'provider', provider: {} }), '');
});

test('candidates keep the live-first order and the last working source', () => {
  const scheduled = group({ displayName: 'Scheduled Channel', confidenceTier: 'strong', primary: { sourceId: 's1', channelId: 'later', channelName: 'NBA TV', programTitle: 'Game upcoming' } });
  const live = group({ displayName: 'Live Channel', confidenceTier: 'possible', primary: { sourceId: 's1', channelId: 'now', channelName: 'NBA TV Live' } });
  const replay = providerMatch({ name: 'Alpha vs Beta Replay', confidence: 0.7 });
  assert.deepEqual(
    watchCandidates({ groups: [scheduled, live], providers: [replay] }).map((candidate) => candidate.name),
    ['Live Channel', 'Alpha vs Beta Replay', 'Scheduled Channel'],
  );
  // The last working source is offered first, without changing its tier.
  const remembered = watchCandidates({ groups: [scheduled, live], lastSource: 's1|now' });
  assert.equal(remembered[0].key, 's1|now');
  assert.equal(remembered[0].tier, 'possible');
  // A replay search lists what the provider proved playable, strongest first.
  assert.deepEqual(
    watchCandidates({ providers: [providerMatch({ confidence: 0.6 }), providerMatch({ id: 'addon:sport:ev:2', name: 'Better', confidence: 0.99 })], mode: 'replay' })
      .map((candidate) => candidate.name),
    ['Better', 'Alpha vs Beta'],
  );
});

// The panel must not grow a second set of Stremio-only controls again: every
// kind of provider is a row with the same Watch button.
test('the watch panel has one Watch action and no Stremio-only controls', () => {
  const source = readFileSync(new URL('./sports-watch.js', import.meta.url), 'utf8');
  assert.doesNotMatch(source, /data-provider-watch/);
  assert.doesNotMatch(source, /Watch replay/);
  assert.doesNotMatch(source, /Stremio providers/);
  // One row template renders both kinds, so one attribute carries both.
  assert.equal((source.match(/data-watch="/g) || []).length, 1);
  assert.equal((source.match(/data-preview="/g) || []).length, 1);
  assert.equal((source.match(/state\.candidates\.map\(candidateRow\)/g) || []).length, 1);
  // Both kinds are started through the one target resolver.
  assert.match(source, /watchTarget\(candidate\)/);
  // Preview asks the same capability question for every row and renders one
  // docked preview, so a Stremio entry does not grow a control of its own.
  assert.equal((source.match(/previewAllowed\(candidate\)/g) || []).length, 1);
  assert.equal((source.match(/className = 'docked-preview'/g) || []).length, 1);
  assert.doesNotMatch(source, /data-provider-preview/);
  assert.doesNotMatch(source, /showAddonPreview/);
  // The panel does not own a second Browse Live TV link; the page exposes it
  // once, beside the Watch action.
  assert.doesNotMatch(source, /Browse Live TV/);
});

test('the last working source is offered first without changing tiers', () => {
  const groups = [
    { confidenceTier: 'possible', primary: { sourceId: 's1', channelId: 'a' } },
    { confidenceTier: 'strong', primary: { sourceId: 's1', channelId: 'b' } },
  ];
  const ordered = preferLastSource(groups, 's1|b');
  assert.deepEqual(ordered.map((group) => group.primary.channelId), ['b', 'a']);
  // An unknown or absent memory leaves the order untouched.
  assert.deepEqual(preferLastSource(groups, 's1|missing').map((g) => g.primary.channelId), ['a', 'b']);
  assert.deepEqual(preferLastSource(groups, '').map((g) => g.primary.channelId), ['a', 'b']);
  assert.deepEqual(preferLastSource(undefined, 's1|a'), []);
  assert.equal(ordered[0].confidenceTier, 'strong');
});
