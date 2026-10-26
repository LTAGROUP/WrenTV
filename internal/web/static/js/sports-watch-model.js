// Pure helpers for the Sports Watch flow. Kept DOM-free so the rules are
// unit-tested by node --test.

export function confidenceLabel(tier) {
  if (tier === 'manual') return 'Manual search';
  return tier === 'strong' ? 'Strong match' : 'Possible match';
}

// A finished event has no live feed to tune, so the same panel searches the
// configured Live TV providers for a replay or VOD instead.
export function providerMode(event) {
  return event?.status === 'final' ? 'replay' : 'live';
}

// providerStateLabel says what one provider actually contributed. A provider
// that answered with nothing is named, never silently dropped.
export function providerStateLabel(state) {
  switch (state) {
    case 'complete': return 'Matched';
    case 'empty': return 'Listed nothing for this event';
    case 'no-match': return 'No matching listing';
    case 'no-feed': return 'Listed, but no playable replay';

    case 'unsupported': return 'No playable catalogs';
    case 'unavailable': return 'Unavailable';
    default: return 'Answered';
  }
}

// providerEmptyNote is honest about what was searched: a finished event never
// claims a replay exists when no provider listed one.
export function providerEmptyNote(replay, sources = []) {
  const names = sources.map((source) => source?.name).filter(Boolean);
  const who = names.length ? names.join(', ') : 'your Live TV providers';
  if (replay) {
    return `No replay was found for this event: ${who} did not list a matching replay or VOD with a playable feed. Native catch-up and recording are not implemented.`;
  }
  return `No matching stream was found on ${who} for this event.`;
}

// Quality is whatever the channel label advertised; absent labels stay blank
// instead of claiming HD.
export function qualityLabel(quality) {
  if (!quality) return '';
  const parts = [];
  if (quality.resolutionHeight) parts.push(`${quality.resolutionHeight}p`);
  if (quality.bitrateBps) parts.push(`${(quality.bitrateBps / 1000000).toFixed(1)} Mbps`);
  return parts.join(' · ');
}

export function streamSubtitle(group) {
  const primary = group?.primary || {};
  const pieces = [];
  if (primary.matchReason) pieces.push(primary.matchReason);
  if (primary.sourceName) pieces.push(primary.sourceName);
  if (group?.feedCount > 1) pieces.push(`${group.feedCount} feeds`);
  return pieces.join(' · ');
}

// previewAllowed answers the one capability question the candidate row asks:
// can this source be played in the docked preview? Each kind answers from its
// own identity — a channel needs its channel reference and a scored match, a
// Stremio item the source, content type and item id its playback route needs.
// A source that cannot be played at all is offered neither Preview nor Watch,
// and a Stremio item is never denied a Preview merely because its playback
// route differs. A channel group answers the same question directly, so callers
// may pass either the row or the group.
export function previewAllowed(candidate) {
  if (candidate?.kind === 'provider') {
    const entry = candidate.provider || {};
    return !!(entry.source && entry.type && entry.itemId);
  }
  const group = candidate?.kind === 'channel' ? candidate.group : candidate;
  const primary = group?.primary || {};
  return !!primary.channelId && primary.confidence !== undefined;
}

export function searchNote(query) {
  return query ? `Manual search: “${query}”. These results bypass match scoring.` : '';
}

// Feed ordering: live-labelled feeds first, unknown next, scheduled/upcoming
// last. The label is a hint, never proof of availability (port of
// sportsFeedPriority).
export function labelPriority(label = '') {
  if (/\b(upcoming|scheduled|starting soon|starts? at|not started)\b/i.test(label)) return 2;
  return /\blive\b/i.test(label) ? 0 : 1;
}

export function feedPriority(group) {
  const primary = group?.primary || {};
  return labelPriority(`${primary.channelName || ''} ${primary.programTitle || ''}`);
}

// Port of getSportsWatchDecision: a single strong feed plays directly, more
// than one credible feed (or alternatives) opens the chooser, and weak feeds
// are not credible enough to offer.
export function watchDecision(groups) {
  const credible = (groups || []).filter((group) => group?.confidenceTier !== 'weak');
  if (!credible.length) return { kind: 'unavailable' };
  if (
    credible.length === 1 &&
    credible[0].confidenceTier === 'strong' &&
    (credible[0].alternatives || []).length === 0 &&
    feedPriority(credible[0]) !== 2
  ) {
    return { kind: 'direct', group: credible[0] };
  }
  return { kind: 'choose', groups: credible };
}

// The last working source is offered first in the recommended order; it never
// outranks a measured sort or changes the confidence tier.
export function preferLastSource(groups, key) {
  const list = [...(groups || [])];
  const index = key ? list.findIndex((group) => probeKey(group?.primary) === key) : -1;
  if (index <= 0) return list;
  const [first] = list.splice(index, 1);
  return [first, ...list];
}

// A probe identity: the same channel listed twice is measured once.
export function probeKey(match) {
  return `${match?.sourceId || ''}|${match?.channelId || ''}`;
}

// watchCandidates merges the two searches the server runs for one event into a
// single list: channels matched from playlists, Xtream logins and the guide,
// and items matched from Stremio manifests. A manifest is a Live TV provider
// like any other, so the viewer gets one list and one Watch action; the
// transport stays on the candidate so playback still uses the integration the
// provider needs.
export function watchCandidates({ groups = [], providers = [], mode = 'live', lastSource = '' } = {}) {
  const list = [];
  for (const group of groups || []) {
    const primary = group?.primary || {};
    if (!primary.channelId) continue;
    list.push({
      kind: 'channel',
      key: probeKey(primary),
      name: group.displayName || primary.channelName || '',
      tier: group.confidenceTier || 'possible',
      confidence: Number(group.confidence ?? primary.confidence) || 0,
      priority: feedPriority(group),
      group,
    });
  }
  for (const entry of providers || []) {
    if (!entry?.source || !entry?.type || !entry?.itemId) continue;
    const id = entry.id || `${entry.source}:${entry.type}:${entry.itemId}`;
    list.push({
      kind: 'provider',
      key: `provider|${id}`,
      name: entry.name || id,
      tier: entry.confidenceTier || 'possible',
      confidence: Number(entry.confidence) || 0,
      // Every replay row is the same answer: a feed the server already proved
      // playable. A live listing keeps the live-first hint.
      priority: mode === 'replay' ? 0 : labelPriority(`${entry.name || ''} ${entry.description || ''}`),
      provider: entry,
    });
  }
  // Live-labelled sources first, then the strongest evidence. The sort is
  // stable, so equal priorities keep the order the server ranked them in.
  list.sort((a, b) => a.priority - b.priority || b.confidence - a.confidence);
  // The device's last working source is offered first, exactly as it is for
  // channels alone.
  const at = lastSource ? list.findIndex((candidate) => candidate.key === lastSource) : -1;
  if (at > 0) list.unshift(...list.splice(at, 1));
  return list;
}

// watchTarget answers what one Watch click should do. Both kinds go through
// Live TV: a channel opens its own session, a provider item goes through
// automatic playback, which ranks the feeds, proves one starts and falls back.
export function watchTarget(candidate) {
  if (candidate?.kind === 'channel') {
    const primary = candidate.group?.primary || {};
    if (!primary.channelId) return null;
    return {
      probe: probeKey(primary),
      channelId: primary.channelId,
      name: primary.channelName || candidate.name,
      group: primary.sourceName || 'Live TV',
      label: [primary.sourceName, primary.channelName].filter(Boolean).join(' · '),
    };
  }
  const entry = candidate?.provider;
  if (!entry?.source || !entry?.type || !entry?.itemId) return null;
  return {
    probe: candidate.key,
    addonAuto: { source: entry.source, type: entry.type, id: entry.itemId },
    name: entry.name || entry.itemId,
    group: entry.sourceName || 'Live TV',
    label: [entry.sourceName, entry.name].filter(Boolean).join(' · '),
  };
}

// candidateSubtitle describes a row the same way for every provider kind: why
// it matched, which provider it came from and how many feeds it offers.
export function candidateSubtitle(candidate) {
  if (candidate?.kind === 'channel') return streamSubtitle(candidate.group);
  const entry = candidate?.provider || {};
  const pieces = [];
  if (entry.matchReason) pieces.push(entry.matchReason);
  if (entry.sourceName) pieces.push(entry.sourceName);
  if (entry.feeds > 1) pieces.push(`${entry.feeds} feeds`);
  return pieces.join(' · ');
}

// A race session or a cycling stage is not an id in the polled scoreboard, so
// the server cannot resolve it without its kind and the weekend it belongs to.
// An ordinary matchup carries neither and asks for no extra parameter.
export function watchQuery(event, query = '') {
  const params = new URLSearchParams();
  if (event?.streamKind) params.set('kind', event.streamKind);
  if (event?.parentId) params.set('parentId', event.parentId);
  if (query) params.set('q', query);
  const encoded = params.toString();
  return encoded ? `?${encoded}` : '';
}

// Measured resolution overrides a label. Unknown stays last; ties fall back to
// frame rate and bitrate (port of sortSportsProbedFeeds).
export function sortProbedFeeds(groups, results) {
  const measured = (group) => results?.[probeKey(group?.primary)]?.quality;
  return [...(groups || [])].sort((a, b) => {
    const aq = measured(a);
    const bq = measured(b);
    return (
      (bq?.height ?? b.primary?.reportedQuality?.resolutionHeight ?? 0) -
        (aq?.height ?? a.primary?.reportedQuality?.resolutionHeight ?? 0) ||
      (bq?.frameRate ?? 0) - (aq?.frameRate ?? 0) ||
      (bq?.bitrateBps ?? 0) - (aq?.bitrateBps ?? 0)
    );
  });
}

export function probeLabel(progress, results, feedCount) {
  if (progress?.running) return 'Cancel quality check';
  const checked = Object.keys(results || {}).length;
  if (feedCount > 0 && checked >= feedCount) return 'Recheck quality';
  return checked ? 'Check remaining streams' : 'Check stream quality';
}

export function probeProgressText(progress, results) {
  if (progress?.running) {
    return `Checking ${Math.min(progress.completed + 1, progress.total)} of ${progress.total} · ${progress.completed} completed · ${progress.failed} inconclusive`;
  }
  if (progress?.total) {
    const done = progress.cancelled ? 'Cancelled' : 'Check complete';
    return `${done} · ${progress.completed} of ${progress.total} checked · ${progress.failed} inconclusive · results kept`;
  }
  if (Object.keys(results || {}).length) {
    return 'Results are kept. Check the remaining streams or recheck all.';
  }
  return 'Check video and audio quality. This briefly connects to each stream. Cancel keeps completed results.';
}

// Measured quality is always described as measured; adaptive streams never
// claim a fixed playback quality (port of sportsProbedQualityLines).
export function probedQualityLines(result) {
  if (!result) return [];
  if (!result.quality) return [result.error || 'Quality not detected · playback may still work'];
  const q = result.quality;
  const video = [
    q.height ? `${q.width ? `${q.width} × ` : ''}${q.height}${q.width ? '' : 'p'}` : '',
    q.frameRate ? `${Number(q.frameRate.toFixed(2))} fps` : '',
    q.codec ? q.codec.toUpperCase() : '',
    q.bitrateBps ? `${Number((q.bitrateBps / 1e6).toFixed(2))} Mbps` : '',
  ]
    .filter(Boolean)
    .join(' · ');
  const audio = [
    ...new Set(
      (q.audio || [])
        .map((a) =>
          [
            a.codec ? a.codec.toUpperCase() : '',
            a.layout || (a.channels ? `${a.channels} ch` : ''),
            a.language || '',
            a.bitrateBps ? `${Math.round(a.bitrateBps / 1000)} kbps` : '',
          ]
            .filter(Boolean)
            .join(' · '),
        )
        .filter(Boolean),
    ),
  ].join(' / ');
  return [
    video ? `${q.adaptive ? 'Available' : 'Detected'} video · ${video}` : '',
    audio ? `Audio · ${audio}` : '',
    q.adaptive ? 'Adaptive stream · playback quality may vary' : '',
  ].filter(Boolean);
}

// API errors map to the same honest messages MediaStorm shows.
export function probeError(error) {
  const message = String(error?.message || '');
  const status = error?.status;
  if (status === 404 || /404/.test(message)) return 'Quality check unavailable · backend or channel not found';
  if (status === 409 || /409/.test(message)) return 'Provider connection limit reached';
  if (status === 429 || /429/.test(message)) return 'Quality checker busy';
  if (status === 504 || /timeout|timed out|504/i.test(message)) return 'Check timed out';
  return 'Quality not detected · playback may still work';
}
