// Pure Live TV list helpers, unit-tested and shared by the browser view.

// Query words that describe the matchup rather than a channel. Keeping "vs"
// out of the scored terms stops an EPG full of "A vs B" titles from looking
// relevant to every search.
const matchupFillers = new Set(['vs', 'v', 'versus']);

export function channelTerms(query) {
  return String(query || '').toLowerCase().split(/\s+/).filter((t) => t.length >= 2);
}

const words = (value) => String(value || '').toLowerCase().split(/[^a-z0-9]+/).filter(Boolean);

export function channelHaystack(channel, programs = []) {
  return [channel?.name, channel?.number, channel?.group, channel?.tvgName, channel?.tvgId,
    channel?.now?.title, channel?.now?.description, channel?.next?.title, channel?.next?.description,
    ...(programs || []).flatMap((p) => [p?.title, p?.description])]
    .filter(Boolean).join(' ').toLowerCase();
}

// A short query word is an abbreviation, not a fragment: "los" must not match
// "closing" and "la" is already dropped as a single letter. Longer words keep
// substring matching so typing part of a name still works.
function termMatches(haystack, hayWords, term) {
  if (!term) return false;
  if (term.length <= 3) return hayWords.includes(term);
  return haystack.includes(term);
}

function containsPhrase(hayWords, terms) {
  if (!terms || terms.length < 2) return false;
  for (let i = 0; i + terms.length <= hayWords.length; i++) {
    if (terms.every((term, offset) => hayWords[i + offset] === term)) return true;
  }
  return false;
}

// channelMatch describes how much of a query a channel actually covers. The
// phrase bonus keeps an exact matchup or team name above a channel that merely
// shares one word.
export function channelMatch(channel, programs, terms) {
  const list = (terms || []).filter(Boolean);
  if (!list.length) return { matched: 0, total: 0, phrase: false, score: 0 };
  const haystack = channelHaystack(channel, programs);
  const hayWords = words(haystack);
  const matched = list.filter((term) => termMatches(haystack, hayWords, term)).length;
  const phrase = matched > 0 && containsPhrase(hayWords, list);
  const score = matched + (matched === list.length ? 1 : 0) + (phrase ? 1 : 0);
  return { matched, total: list.length, phrase, score };
}

export function matchesChannel(channel, programs, terms) {
  return channelMatch(channel, programs, terms).matched > 0;
}

const numericOrder = (a, b) => String(a.number || '').localeCompare(String(b.number || ''), undefined, { numeric: true });

// A single team word can be a music, news or comedy channel. These labels are
// not evidence that the channel carries the game, however well the word fits.
const unrelatedTeamWords = new Set([
  'comedy', 'documentary', 'documentaries', 'education', 'entertainment', 'food', 'kids',
  'music', 'news', 'reality', 'religion', 'shopping', 'talk', 'travel', 'weather',
]);
const nonLiveWords = new Set(['replay', 'archive', 'classic', 'classics', 'movie', 'movies', 'ondemand', 'vod']);

// A SportsHub query is two team names joined by a matchup marker. Reading the
// two sides separately is what lets a team name outrank a shared word such as
// "Los" or "Angeles". A bare "at" is only treated as a separator when both
// sides are multi-word phrases, so a normal search like "BBC at the Proms" is
// left to the ordinary text search.
function matchupSides(query) {
  const value = String(query || '').trim().replace(/\s+/g, ' ');
  const explicit = /\s(?:vs\.?|versus|@)\s/i.test(value);
  const parts = value.split(/\s(?:vs\.?|versus|@|at)\s/i);
  if (parts.length !== 2) return null;
  const sides = parts.map(words);
  if (sides.some((side) => !side.length) || (!explicit && sides.some((side) => side.length < 2))) return null;
  return sides.map((full) => ({ full, nickname: full[full.length - 1] }));
}

function hasAll(hay, phrase) {
  return phrase.length > 0 && phrase.every((word) => hay.includes(word));
}

// matchupChannels keeps only channels with positive team evidence. Both team
// names (or both nicknames) are the strongest evidence; one full team name is
// a permanent team channel; one nickname is accepted only when the channel
// label itself is the team brand rather than another subject.
function matchupChannels(channels, guide, sides) {
  const [away, home] = sides;
  const scored = [];
  for (const channel of channels) {
    const brand = words([channel.name, channel.tvgName].filter(Boolean).join(' '));
    const label = [...brand, ...words(channel.group)];
    if (label.some((word) => nonLiveWords.has(word))) continue;
    const brandSet = new Set(brand);
    const programs = guide.get(channel.id) || [];
    const hay = words(channelHaystack(channel, programs));
    const awayFull = hasAll(hay, away.full);
    const homeFull = hasAll(hay, home.full);
    const awayNick = !awayFull && hay.includes(away.nickname);
    const homeNick = !homeFull && hay.includes(home.nickname);
    let score = 0;
    if (awayFull && homeFull) score = 6;
    else if (awayNick && homeNick) score = 5;
    else if (hasAll(brand, away.full) || hasAll(brand, home.full)) score = 4;
    else if (brandSet.has(away.nickname) || brandSet.has(home.nickname)) {
      // A nickname on its own is a team brand only when the channel does not
      // otherwise name a different subject (music, news, comedy).
      if (label.some((word) => unrelatedTeamWords.has(word))) continue;
      score = 2;
    } else continue;
    scored.push({ channel, score });
  }
  return scored.sort((a, b) => b.score - a.score || numericOrder(a.channel, b.channel)).map((entry) => entry.channel);
}

// visibleChannels applies the group filter, the relevance-ranked search and the
// playlist's channel order (numeric when numbers exist). A matchup query is
// matched by team identity, not by an OR of loose words. A multi-word plain
// query keeps only channels covering at least two words once such a channel
// exists, so it does not degenerate into single-word coincidences.
export function visibleChannels(channels = [], guide = new Map(), { group = 'all', query = '', source = 'all' } = {}) {
  const inGroup = channels.filter((c) =>
    (group === 'all' || c.group === group) && (source === 'all' || c.source === source));
  const sides = matchupSides(query);
  if (sides) return matchupChannels(inGroup, guide, sides);
  const terms = channelTerms(query).filter((term) => !matchupFillers.has(term));
  if (!terms.length) return inGroup.sort(numericOrder);
  const scored = inGroup
    .map((channel) => ({ channel, match: channelMatch(channel, guide.get(channel.id) || [], terms) }))
    .filter((entry) => entry.match.matched > 0);
  if (!scored.length) return [];
  const best = Math.max(...scored.map((entry) => entry.match.matched));
  const minimum = best >= 2 ? 2 : 1;
  return scored
    .filter((entry) => entry.match.matched >= minimum)
    .sort((a, b) => b.match.score - a.match.score || numericOrder(a.channel, b.channel))
    .map((entry) => entry.channel);
}

// matchedChannels turns the Sports matcher's ranked groups into the Live TV
// list: only channels the matcher considered plausible survive, in its order.
// Unrelated channels can never be re-admitted by a loose text match.
export function matchedChannels(channels = [], groups = [], { group = 'all' } = {}) {
  const byId = new Map(channels.map((channel) => [channel.id, channel]));
  const seen = new Set();
  const out = [];
  for (const entry of groups || []) {
    for (const match of [entry?.primary, ...(entry?.alternatives || [])]) {
      const id = match?.channelId;
      if (!id || seen.has(id)) continue;
      seen.add(id);
      const channel = byId.get(id);
      if (channel && (group === 'all' || channel.group === group)) out.push(channel);
    }
  }
  return out;
}

// matchedEvents keeps the addon events the server matched to a SportsHub event
// in the same list as its matched channels. The group filter covers both kinds,
// so one control never hides one of them silently.
export function matchedEvents(events = [], { group = 'all' } = {}) {
  return (events || []).filter((event) => event && (group === 'all' || event.group === group));
}

export function groupsOf(channels = []) {
  return [...new Set(channels.map((c) => c.group).filter(Boolean))].sort((a, b) => a.localeCompare(b));
}

// groupsOfAll is the one group filter for the one list: channel groups and the
// groups addon events carry. An event's group is its catalog or genre, never a
// invented television group.
export function groupsOfAll(channels = [], events = []) {
  return [...new Set([...channels, ...events].map((entry) => entry.group).filter(Boolean))]
    .sort((a, b) => a.localeCompare(b));
}

const eventHaystack = (event) => [event?.name, event?.group, event?.description, event?.releaseInfo, event?.sourceName]
  .filter(Boolean).join(' ').toLowerCase();

// visibleEvents applies the same source filter and search box to addon events
// as to channels. Events keep the addon's own catalog order; there is no
// channel number to sort them by, and inventing one would make an event look
// like a station.
export function visibleEvents(events = [], { group = 'all', query = '', source = 'all' } = {}) {
  const inGroup = (events || []).filter((event) =>
    (group === 'all' || event.group === group) && (source === 'all' || event.source === source));
  const terms = channelTerms(query).filter((term) => !matchupFillers.has(term));
  if (!terms.length) return inGroup;
  return inGroup.filter((event) => {
    const haystack = eventHaystack(event);
    const hayWords = words(haystack);
    return terms.some((term) => termMatches(haystack, hayWords, term));
  });
}

// providersPending reports whether a Live TV listing is still waiting for its
// first refresh. Providers that are configured but have contributed nothing
// yet are not an empty answer, and a completed refresh with no content is.
export function providersPending(listing = {}) {
  if (!listing || listing.prepared === true) return false;
  const providers = Array.isArray(listing.providers) ? listing.providers : [];
  if (!providers.some((provider) => provider.enabled !== false)) return false;
  return (listing.channels || []).length === 0 && (listing.events || []).length === 0;
}

// eventMatchPending separates a matcher answer computed before Live TV was
// ready from a real "no channel matches" answer.
export function eventMatchPending(data = {}) {
  return data?.ready === false || data?.code === 'livetv_pending';
}

// nativeHlsSupported is the same rule the library player uses: Chromium
// answers canPlayType for HLS but plays it through its own pipeline without the
// rendition APIs (audioTracks/textTracks), so native HLS means Safari.
export function nativeHlsSupported(video, ua = typeof navigator === 'undefined' ? '' : navigator.userAgent) {
  if (!video?.canPlayType?.('application/vnd.apple.mpegurl')) return false;
  return /^((?!chrome|android).)*safari/i.test(ua);
}

// playbackMode says how a returned live session can be played: native HLS on
// Safari, hls.js elsewhere, or not at all for a raw MPEG-TS stream.
export function playbackMode(type, nativeHls, hlsSupported) {
  if (type !== 'hls') return 'unsupported';
  if (nativeHls) return 'native';
  if (hlsSupported) return 'hlsjs';
  return 'unsupported';
}
