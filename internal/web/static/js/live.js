// Live TV: one list of channels and addon events, filtered by one provider
// picker and one group selector. A Stremio manifest is a provider like any
// other: its TV catalogs become channels, its event catalogs become selectable
// entries in this same list, and both play through the ordinary Live TV player.
import { api, state } from './api.js';
import { esc, $, $$, el, toast } from './ui.js';
import { openLiveChannel } from './live-player.js';
import {
  eventMatchPending, groupsOfAll, matchedChannels, matchedEvents, providersPending, visibleChannels, visibleEvents,
} from './live-model.js';
import { eventStatus, streamLabel } from './sport-addon-model.js';
import {
  ALL_PROVIDERS, channelsForSource, defaultSource, eventsForSource, parseSource, sourceOptions,
} from './live-sources.js';

const view = {
  channels: [],
  // Addon catalog items, from the same response as the channels. They share the
  // source field with channels so one provider filter covers both.
  events: [],
  guide: new Map(),
  group: 'all',
  query: '',
  // One source picker for the whole page: `all`, `iptv:<id>` or `addon:<id>`.
  // The value carries its kind so the server uses the right integration, but
  // the picker itself is one flat list.
  source: ALL_PROVIDERS,
  providers: [],
  expanded: false,
  root: null,
  // A SportsHub search carries the event id. Until the matcher answers,
  // `matches` is undefined and the page shows a loading state; null means there
  // is no event context (a plain text search), and an empty array is a real
  // "no plausible channel" answer that must stay empty.
  event: '',
  matches: null,
  // Addon events the server matched to the same event. They are rows in the
  // same list as the matched channels, never a second browsing mode.
  matchEvents: [],
  matchError: '',
  listingError: '',
  // Event matching and the first provider refresh are both asynchronous. A
  // sequence number keeps an older visit or search from painting over a newer
  // one, and the tokens cancel a pending event match.
  seq: 0,
  matchToken: 0,
  waiting: false,
  touched: false,
  // One event's feed list, opened by hand after automatic playback could not
  // start a stream. It is a detail view of an entry, not a browsing mode.
  feeds: null,
};

const timeOf = (seconds) => new Date(seconds * 1000).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
const windowLabel = (p) => (p ? `${timeOf(p.start)} – ${timeOf(p.end)}` : '');

export async function viewLive() {
  const seq = ++view.seq;
  view.matchToken += 1;
  const params = new URLSearchParams(location.search);
  view.query = params.get('q') || '';
  view.event = params.get('event') || '';
  view.matches = view.event ? undefined : null;
  view.matchEvents = [];
  view.matchError = '';
  view.touched = false;
  const wantedChannel = params.get('channel') || '';
  view.group = 'all';
  view.expanded = false;
  view.feeds = null;
  // The provider list is the prerequisite for everything else: an event match
  // computed before the first refresh is empty for a reason the viewer can't
  // see. Wait for that first refresh (bounded) instead of painting "nothing".
  let listing = await fetchListing();
  // Retry a failed listing briefly too: a provider that is still starting can
  // answer nothing at all before it answers with data.
  for (let attempt = 0; attempt < LISTING_ATTEMPTS && seq === view.seq && (listingWaiting(listing) || !listing.channels); attempt += 1) {
    await delay(400);
    if (seq !== view.seq) return view.root;
    listing = await fetchListing();
  }
  if (seq !== view.seq) return view.root;
  applyListing(listing);
  view.listingError = listing.channels ? '' : 'Live TV could not be loaded. Check that the server is reachable, then try again.';
  view.waiting = listingWaiting(listing);
  // A provider removed or renamed since the last visit must not leave a stale
  // filter behind. The page opens on every source together.
  view.source = view.event ? ALL_PROVIDERS : defaultSource();

  if (!state.server?.liveTv && !view.channels.length && !view.events.length) {
    return `<div class="empty"><h2>Live TV isn't set up</h2>
      <p>Add a playlist, an Xtream login or a Stremio manifest in Manage → Settings to watch here.</p></div>`;
  }
  view.root = el(shellHTML());
  bind();
  syncGroups();
  paint();
  // The event matcher needs the same provider data; it runs after the listing
  // is applied and shows its own pending state until it answers.
  if (view.event) loadEventMatches(seq);
  if (view.waiting) waitForProviders(seq);
  // A team-channel quick action opens the exact linked channel, but only when
  // that id still exists in the current playlist. A stale link does nothing.
  if (wantedChannel) {
    const channel = view.channels.find((c) => c.id === wantedChannel);
    if (channel) openLiveChannel(channel).catch((err) => toast(err.message));
    else toast('That team channel is no longer in the playlist.');
  }
  return view.root;
}

const delay = (ms) => new Promise((resolve) => setTimeout(resolve, ms));
const LISTING_ATTEMPTS = 8;
const MATCH_ATTEMPTS = 10;
// Extra attempts while the server says a provider refresh is still running.
const MATCH_REFRESH_ATTEMPTS = 40;

async function fetchListing() {
  const [channels, guide] = await Promise.all([
    api('/api/livetv/channels').catch(() => null),
    api('/api/livetv/guide?hours=6').catch(() => ({ channels: [] })),
  ]);
  return { channels, guide };
}

// listingWaiting is true only while providers are configured and Live TV has
// not finished its first refresh. A completed refresh with no content is a real
// empty answer and is never waited on.
function listingWaiting(listing) {
  return providersPending(listing?.channels);
}

function applyListing(listing) {
  const channels = listing?.channels || {};
  view.channels = Array.isArray(channels.channels) ? channels.channels : [];
  view.events = Array.isArray(channels.events) ? channels.events : [];
  view.providers = Array.isArray(channels.providers) ? channels.providers : [];
  view.guide = new Map(((listing?.guide || {}).channels || []).map((g) => [g.id, g.programs || []]));
}

// waitForProviders keeps polling after the initial bounded wait. A first run
// can download a large playlist; painting "no channels" in the meantime would
// be a lie, and asking the viewer to search first would be a worse one.
async function waitForProviders(seq) {
  for (let attempt = 0; attempt < 40 && seq === view.seq; attempt += 1) {
    await delay(750);
    if (seq !== view.seq) return;
    const listing = await fetchListing();
    if (seq !== view.seq) return;
    if (listingWaiting(listing)) continue;
    view.waiting = false;
    // Do not throw away a filter the viewer chose while waiting.
    if (!view.touched) {
      applyListing(listing);
      view.source = defaultSource();
      // The earlier answer (or its pending explanation) belongs to the refresh
      // that just finished; the new match starts from its own pending state.
      if (view.event) resetEventMatch();
      view.root.innerHTML = shellHTML();
      bind();
      syncGroups();
      paint();
      if (view.event) loadEventMatches(seq);
    } else {
      applyListing(listing);
      paint();
    }
    return;
  }
}

// loadEventMatches ranks the loaded channels and addon events against the
// event. A pending provider refresh is retried, and a stale response can never
// replace a newer search: both the page sequence and the match token must still
// match.
async function loadEventMatches(seq) {
  if (!view.event) return;
  const token = ++view.matchToken;
  const fresh = () => seq === view.seq && token === view.matchToken && !!view.event;
  // A pending answer is not an empty result. The first attempts are quick; a
  // provider refresh that is still running is evidence of work in flight, so it
  // keeps the page in its matching state until the bounded window closes.
  for (let attempt = 0; attempt < MATCH_ATTEMPTS + MATCH_REFRESH_ATTEMPTS; attempt += 1) {
    if (!fresh()) return;
    let data;
    try {
      data = await api(`/api/sports/events/${encodeURIComponent(view.event)}/streams`);
    } catch (err) {
      if (!fresh()) return;
      if (err?.status === 503 && err?.data?.code === 'sports_pending' && attempt + 1 < MATCH_ATTEMPTS) {
        await delay(400);
        continue;
      }
      // The ranker could not answer. A plain text search is still useful, but
      // the reason is shown rather than quietly changing modes.
      view.matches = null;
      view.matchEvents = [];
      view.matchError = err.message || 'Could not rank channels for this event.';
      paint();
      return;
    }
    if (!fresh()) return;
    if (eventMatchPending(data)) {
      const patience = data?.refreshing ? MATCH_ATTEMPTS + MATCH_REFRESH_ATTEMPTS : MATCH_ATTEMPTS;
      if (attempt + 1 < patience) {
        await delay(data?.refreshing ? 750 : 400);
        continue;
      }
      view.matches = [];
      view.matchEvents = [];
      view.matchError = 'Live TV providers are still loading, so this event has not been matched yet.';
      paint();
      return;
    }
    view.matches = Array.isArray(data.groups) ? data.groups : [];
    view.matchEvents = Array.isArray(data.events) ? data.events : [];
    view.matchError = '';
    paint();
    return;
  }
  if (!fresh()) return;
  // The retry window closed without an answer. The event context stays, with
  // an explanation instead of a spinner that never ends.
  view.matches = [];
  view.matchEvents = [];
  view.matchError = 'Live TV providers are still loading, so this event has not been matched yet.';
  paint();
}

// Channel and event filtering share the source value: choosing a provider shows
// what that provider contributes, whichever model it uses.
function channelsFor(source = view.source) {
  return channelsForSource(view.channels, source);
}

function eventsFor(source = view.source) {
  return eventsForSource(view.events, source);
}

function entriesForFilter() {
  const options = { group: view.group, query: view.query };
  return {
    channels: visibleChannels(channelsFor(), view.guide, options),
    events: visibleEvents(eventsFor(), options),
  };
}

// Provider selection changes which groups exist. Reset a group that the chosen
// provider does not have instead of leaving an empty filtered list behind.
function syncGroups() {
  const select = $('#live-group', view.root);
  if (!select) return;
  const groups = groupsOfAll(channelsFor(), eventsFor());
  if (view.group !== 'all' && !groups.includes(view.group)) view.group = 'all';
  select.innerHTML = `<option value="all">All groups</option>${groups.map((g) => `<option value="${esc(g)}">${esc(g)}</option>`).join('')}`;
  select.value = view.group;
}

function providerDisplayName(p) {
  const name = p.name || p.id;
  // Two providers may share a display name. Their ids stay distinct, so show
  // one only when it is needed to tell them apart.
  const duplicate = view.providers.filter((x) => (x.name || x.id) === name).length > 1;
  return duplicate ? `${name} (${p.id})` : name;
}

// sourcePicker is the one dropdown for every configured source. There are no
// IPTV and Stremio headings and no mode switch: a manifest is an option beside
// the playlists, and the value keeps the kind the server needs.
function sourcePicker() {
  // A SportsHub deep link keeps its matcher-ranked list; the picker is
  // deliberately not part of that flow.
  if (view.event) return '';
  const options = sourceOptions({ providers: view.providers });
  // "All providers" alone leaves nothing to switch to.
  if (options.length < 2) return '';
  return `<select id="live-source" aria-label="Live TV source">${options.map((option) =>
    `<option value="${esc(option.value)}"${view.source === option.value ? ' selected' : ''}>${esc(option.label)}</option>`).join('')}</select>`;
}

function shellHTML() {
  const groups = groupsOfAll(channelsFor(), eventsFor());
  return `<section class="live">
    <div class="live-head">
      <div><h1>Live TV</h1><div class="small muted" id="live-count"></div></div>
      <form class="live-search" id="live-search"><input type="search" name="q" placeholder="Search channels, events or programmes" value="${esc(view.query)}" autocomplete="off"></form>
      <div class="live-actions">
        ${sourcePicker()}
        <select id="live-group" aria-label="Channel and event group"><option value="all">All groups</option>${groups.map((g) => `<option value="${esc(g)}"${view.group === g ? ' selected' : ''}>${esc(g)}</option>`).join('')}</select>
        <button class="btn" id="live-guide-toggle">${view.expanded ? 'Hide guide' : 'Show guide'}</button>
      </div>
    </div>
    <div id="live-context"></div>
    <div id="live-list"></div>
  </section>`;
}

function providerName(id) {
  const provider = view.providers.find((p) => p.id === id);
  return provider ? providerDisplayName(provider) : id || '';
}

function channelRow(c) {
  const logo = c.logo ? `<img loading="lazy" src="${esc(c.logo)}" alt="" onerror="this.remove()">` : '<span class="ph"></span>';
  // When every provider is shown, two providers can publish the same channel
  // name. Naming the provider on the row keeps those entries distinguishable
  // without changing the channel id, guide lookup or play request.
  const from = parseSource(view.source).kind === 'all' && view.providers.length > 1
    ? `<span class="small muted"> · ${esc(providerName(c.source))}</span>` : '';
  const now = c.now ? `<span class="now">${esc(c.now.title)}</span> <span class="when muted">${windowLabel(c.now)}</span>`
    : '<span class="muted">No guide data</span>';
  const guide = view.expanded
    ? `<div class="live-guide">${(view.guide.get(c.id) || []).slice(0, 6).map((p) => `<div class="programme"><span class="t">${esc(p.title)}</span><span class="w">${windowLabel(p)}</span></div>`).join('') || '<div class="muted small">No programmes listed.</div>'}</div>`
    : '';
  return `<article class="channel" data-channel="${esc(c.id)}">
    <div class="ch-num">${esc(c.number || '')}</div>
    <div class="ch-logo">${logo}</div>
    <div class="ch-info"><div class="ch-name">${esc(c.name)}${from}</div><div class="ch-now">${now}</div></div>
    <div class="ch-next">${c.next ? `<div class="small muted">Next</div><div class="small">${esc(c.next.title)}</div><div class="small muted">${windowLabel(c.next)}</div>` : ''}</div>
    <button class="btn primary small" data-play="${esc(c.id)}">Watch</button>
    ${guide}
  </article>`;
}

// An event row is a Live TV entry, not a channel: it keeps the addon's own
// name and status, has no channel number and never gets a guide row. The
// provider is shown only when the list mixes providers, like a channel's.
function eventRow(entry) {
  const status = eventStatus(entry);
  const badge = status ? `<span class="sport-badge ${status}">${status === 'live' ? 'LIVE' : 'UPCOMING'}</span>` : '<span class="sport-badge">EVENT</span>';
  const from = parseSource(view.source).kind === 'all' && view.providers.length > 1
    ? `<span class="small muted"> · ${esc(providerName(entry.source))}</span>` : '';
  const art = entry.logo
    ? `<img loading="lazy" src="${esc(entry.logo)}" alt="" onerror="this.remove()">`
    : '<span class="ph"></span>';
  const detail = [entry.group, entry.releaseInfo].filter(Boolean).join(' · ');
  return `<article class="channel live-event" data-live-event="${esc(entry.id)}">
    <div class="ch-num"></div>
    <div class="ch-logo">${art}</div>
    <div class="ch-info"><div class="ch-name">${esc(entry.name)}${from}</div>
      <div class="ch-now">${esc(entry.description || detail || '')}</div>
      <div class="small muted">${esc(detail)}</div></div>
    <div class="ch-next">${badge}</div>
    <div class="live-event-actions">
      <button class="btn primary small" data-play-event="${esc(entry.id)}">Watch</button>
      <button class="btn small" data-event-feeds="${esc(entry.id)}">Feeds</button>
    </div>
  </article>`;
}

// Leaving the event context is explicit: the viewer can always browse the
// whole playlist, and the search box then behaves like the normal Live TV page.
function showAllChannels() {
  view.matchToken += 1;
  view.matches = null;
  view.matchEvents = [];
  view.matchError = '';
  view.event = '';
  view.touched = true;
  view.query = '';
  view.group = 'all';
  view.source = ALL_PROVIDERS;
  view.feeds = null;
  const search = $('#live-search input', view.root);
  if (search) search.value = '';
  const source = $('#live-source', view.root);
  if (source) source.value = ALL_PROVIDERS;
  syncGroups();
  paint();
}

// Switching source starts the new source from its own first view: a SportsHub
// match, a channel group and an open feed list all stop applying, so a stale
// filter can never hide the source that was just chosen.
function selectSource(value) {
  view.source = value;
  view.matchToken += 1;
  view.matches = null;
  view.matchEvents = [];
  view.matchError = '';
  view.event = '';
  view.touched = true;
  view.query = '';
  view.group = 'all';
  view.feeds = null;
  const input = $('#live-search input', view.root);
  if (input) input.value = '';
  syncGroups();
  paint();
}

function paint() {
  if (!view.root) return;
  const list = $('#live-list', view.root);
  const count = $('#live-count', view.root);
  const search = $('#live-search input', view.root);
  if (search) search.placeholder = 'Search channels, events or programmes';
  if (view.feeds) {
    paintFeeds(list, count);
    return;
  }
  const eventPending = view.matches === undefined;
  const eventMode = !eventPending && view.matches !== null;
  // This event's matched channels and the providers' matched events are one
  // list: the same row, the same group filter and the same Watch action.
  const { channels, events } = eventMode
    ? {
      channels: matchedChannels(view.channels, view.matches, { group: view.group }),
      events: matchedEvents(view.matchEvents, { group: view.group }),
    }
    : eventPending
      ? { channels: [], events: [] }
      : entriesForFilter();
  const context = $('#live-context', view.root);
  if (context) {
    let html = '';
    if (eventPending) {
      html = `<p class="small muted" role="status">Matching this event against your Live TV providers…</p>`;
    } else if (eventMode) {
      html = `<p class="small muted">Ranked by the Sports matcher${view.query ? ` for “${esc(view.query)}”` : ''} across every Live TV provider. Only plausible matches are shown. <button type="button" class="btn small" id="live-show-all">Browse all channels</button></p>`;
    } else if (view.matchError) {
      // A failed ranker is a provider problem, not evidence that no channel
      // exists. The text search underneath is still shown.
      html = `<p class="small muted">Couldn't rank channels for this event: ${esc(view.matchError)}
        <button type="button" class="btn small" id="live-retry-match">Try again</button></p>`;
    }
    context.innerHTML = html;
    $('#live-show-all', context)?.addEventListener('click', showAllChannels);
    $('#live-retry-match', context)?.addEventListener('click', retryEventMatches);
  }
  if (view.listingError) {
    if (count) count.textContent = '';
    list.innerHTML = `<div class="empty"><h2>Couldn't load Live TV</h2><p>${esc(view.listingError)}</p>
      <button type="button" class="btn" id="live-retry">Try again</button></div>`;
    const retry = $('#live-retry', list);
    if (retry) retry.onclick = () => window.dispatchEvent(new CustomEvent('wrentv:navigate', { detail: location.pathname + location.search }));
    return;
  }
  if (count) {
    if (eventPending) count.textContent = 'Matching event…';
    else {
      const parts = [`${channels.length} channel${channels.length === 1 ? '' : 's'}`];
      if (events.length) parts.push(`${events.length} event${events.length === 1 ? '' : 's'}`);
      count.textContent = parts.join(' · ');
    }
  }
  if (eventPending) {
    list.innerHTML = `<div class="center" style="min-height:80px"><div class="spinner"></div><p class="small muted">Ranking channels by team and guide evidence…</p></div>`;
    return;
  }
  if (view.waiting && !view.channels.length && !view.events.length) {
    list.innerHTML = `<div class="empty"><h2>Loading your providers</h2>
      <p>Live TV is still fetching playlists and Stremio catalogs. This list fills in as soon as they arrive.</p>
      <button type="button" class="btn" id="live-retry">Check again</button></div>`;
    const retry = $('#live-retry', list);
    if (retry) retry.onclick = () => window.dispatchEvent(new CustomEvent('wrentv:navigate', { detail: location.pathname + location.search }));
    return;
  }
  if (!view.channels.length && !view.events.length) {
    list.innerHTML = `<div class="empty"><h2>No channels or events yet</h2><p>Add a working playlist, Xtream login or Stremio manifest in Manage → Settings. Providers that failed to load are reported on the dashboard.</p></div>`;
    return;
  }
  if (!channels.length && !events.length) {
    if (eventMode || view.matchError) {
      const message = view.matchError
        ? `<p>${esc(view.matchError)}</p><p class="small muted">Retry the event match, or browse every channel.</p>`
        : `<p>No Live TV source is a plausible match${view.query ? ` for “${esc(view.query)}”` : ''}. Every provider was searched; matching is heuristic, and an unmatched event is not an error.</p>`;
      list.innerHTML = `<div class="empty"><h2>${view.matchError ? 'Event match not ready' : 'No Live TV source matches this event'}</h2>
        ${message}
        <div class="event-actions">${view.matchError ? '<button type="button" class="btn primary" id="live-retry-match-2">Try again</button>' : ''}
        <button type="button" class="btn" id="live-browse-all">Browse all channels</button></div></div>`;
      $('#live-browse-all', list).onclick = showAllChannels;
      $('#live-retry-match-2', list)?.addEventListener('click', retryEventMatches);
      return;
    }
    list.innerHTML = `<div class="empty"><h2>Nothing matches</h2><p>Try a different group or search.</p><button class="btn" id="live-clear">Clear filters</button></div>`;
    $('#live-clear', list).onclick = () => {
      view.query = '';
      view.group = 'all';
      $('#live-search input', view.root).value = '';
      $('#live-group', view.root).value = 'all';
      paint();
    };
    return;
  }
  list.innerHTML = channels.map(channelRow).join('') + events.map(eventRow).join('');
  bindList(list);
}

// retryEventMatches returns the page to the event context and asks again.
// Nothing is reset if the event link has been left.
function retryEventMatches() {
  if (!view.event) return;
  resetEventMatch();
  paint();
  loadEventMatches(view.seq);
}

// resetEventMatch returns the page to "the matcher is still answering". It is a
// pending state, never an empty result.
function resetEventMatch() {
  view.matches = undefined;
  view.matchEvents = [];
  view.matchError = '';
}

// findEvent looks in the event match first: a matched addon row is not part of
// the page's own listing when the two snapshots differ.
function findEvent(id) {
  return view.matchEvents.find((entry) => entry.id === id)
    || view.events.find((entry) => entry.id === id);
}

// bindList wires the channel and event rows of one paint. Both kinds open the
// same player; an event starts through the addon's automatic feed selection.
function bindList(list) {
  $$('[data-play]', list).forEach((b) => {
    b.onclick = async (e) => {
      e.preventDefault();
      e.stopPropagation();
      const channel = view.channels.find((c) => c.id === b.dataset.play);
      if (!channel) return;
      b.disabled = true;
      try {
        await openLiveChannel(channel);
      } catch (err) {
        toast(err.message);
      } finally {
        b.disabled = false;
      }
    };
  });
  $$('.channel:not(.live-event)', list).forEach((row) => {
    row.onclick = () => {
      const channel = view.channels.find((c) => c.id === row.dataset.channel);
      if (channel) openLiveChannel(channel).catch((err) => toast(err.message));
    };
  });
  $$('[data-play-event]', list).forEach((b) => {
    b.onclick = (e) => {
      e.stopPropagation();
      const entry = findEvent(b.dataset.playEvent);
      if (entry) playEvent(entry);
    };
  });
  $$('[data-event-feeds]', list).forEach((b) => {
    b.onclick = (e) => {
      e.stopPropagation();
      const entry = findEvent(b.dataset.eventFeeds);
      if (entry) openFeeds(entry);
    };
  });
  $$('.live-event', list).forEach((row) => {
    row.onclick = () => {
      const entry = findEvent(row.dataset.liveEvent);
      if (entry) playEvent(entry);
    };
  });
}

// playEvent is one click: the player asks the server for the best feed it
// hasn't tried yet, so a dead link or an expired token falls through to the
// next one. "Choose a feed" keeps the manual list reachable from the error.
function playEvent(entry) {
  openLiveChannel({
    name: entry.name,
    group: entry.sourceName || providerName(entry.source) || 'Live TV',
    addonAuto: { source: entry.source, type: entry.type, id: entry.itemId },
    chooseFeeds: () => openFeeds(entry),
  }).catch((err) => toast(err.message));
}

function openFeeds(entry) {
  view.feeds = { entry, streams: [], loading: true, error: '' };
  paint();
  api(`/api/livetv/addons/${encodeURIComponent(entry.source)}/item/${encodeURIComponent(entry.type)}/${encodeURIComponent(entry.itemId)}`)
    .then((data) => {
      if (view.feeds?.entry?.id !== entry.id) return;
      view.feeds = { entry, streams: Array.isArray(data.streams) ? data.streams : [], loading: false, error: '' };
      paint();
    })
    .catch((err) => {
      if (view.feeds?.entry?.id !== entry.id) return;
      view.feeds = { entry, streams: [], loading: false, error: err.message };
      paint();
    });
}

// paintFeeds shows one event's feeds. It is the honest fallback when no feed
// starts automatically: the viewer picks one, and it plays through the same
// session and proxy as everything else.
function paintFeeds(list, count) {
  const state = view.feeds;
  const entry = state.entry;
  const context = $('#live-context', view.root);
  if (context) {
    context.innerHTML = `<div class="live-sport-bar"><button type="button" class="btn small" id="live-feeds-back">← Live TV</button>
      <span class="small muted">${esc(entry.sourceName || providerName(entry.source))}</span></div>`;
    const back = $('#live-feeds-back', context);
    if (back) back.onclick = () => { view.feeds = null; paint(); };
  }
  if (count) count.textContent = entry.name;
  if (state.loading) {
    list.innerHTML = `<p class="muted">Asking the addon for this event's feeds…</p>`;
    return;
  }
  if (state.error) {
    list.innerHTML = `<div class="empty"><h2>This event can't be opened</h2><p>${esc(state.error)}</p>
      <button type="button" class="btn" id="live-feeds-back-2">← Live TV</button></div>`;
    const back = $('#live-feeds-back-2', list);
    if (back) back.onclick = () => { view.feeds = null; paint(); };
    return;
  }
  const feeds = state.streams.length
    ? `<div class="sport-feeds">${state.streams.map((stream, index) => {
      const label = streamLabel(stream, index);
      return `<button type="button" class="btn" data-feed="${esc(stream.id)}" data-feed-name="${esc(label)}">
        <strong>${esc(label)}</strong>${stream.title ? `<span class="small muted">${esc(stream.title)}</span>` : ''}</button>`;
    }).join('')}</div>`
    : `<div class="empty"><h2>No playable feed right now</h2><p>The addon lists this event but is not offering a stream Wren TV can play. Feeds often appear closer to the start time, and some are for a paid plan.</p></div>`;
  const art = entry.logo ? `<img class="sport-event-art" loading="lazy" src="${esc(entry.logo)}" alt="" onerror="this.remove()">` : '';
  const description = entry.description || '';
  list.innerHTML = `<article class="sport-event">${art}
    <div class="sport-event-body"><h2>${esc(entry.name)}</h2>
      ${entry.releaseInfo ? `<div class="small muted">${esc(entry.releaseInfo)}</div>` : ''}
      ${description ? `<p class="small muted">${esc(description)}</p>` : ''}</div>
    ${feeds}
  </article>`;
  $$('[data-feed]', list).forEach((button) => {
    button.onclick = async () => {
      button.disabled = true;
      try {
        await openLiveChannel({
          name: button.dataset.feedName ? `${entry.name} · ${button.dataset.feedName}` : entry.name,
          group: entry.sourceName || 'Live TV',
          addonFeedId: button.dataset.feed,
        });
      } catch (err) {
        toast(err.message);
      } finally {
        button.disabled = false;
      }
    };
  });
}

function bind() {
  const search = $('#live-search', view.root);
  // Editing the query is a manual search: stop constraining results to the
  // event the viewer arrived from, and ignore an event match still in flight.
  const manualSearch = () => {
    view.matchToken += 1;
    view.matches = null;
    view.matchEvents = [];
    view.matchError = '';
    view.event = '';
    view.touched = true;
  };
  search.onsubmit = (e) => {
    e.preventDefault();
    manualSearch();
    view.query = new FormData(search).get('q').trim();
    view.feeds = null;
    paint();
  };
  search.querySelector('input').oninput = (e) => {
    manualSearch();
    view.query = e.target.value.trim();
    view.feeds = null;
    paint();
  };
  $('#live-group', view.root).onchange = (e) => { view.group = e.target.value; view.touched = true; paint(); };
  const source = $('#live-source', view.root);
  if (source) source.onchange = (e) => selectSource(e.target.value);
  $('#live-guide-toggle', view.root).onclick = (e) => {
    view.expanded = !view.expanded;
    e.currentTarget.textContent = view.expanded ? 'Hide guide' : 'Show guide';
    paint();
  };
}
