// Sports Hub: dates, filters, lanes, featured event, freshness, spoiler mode and
// bounded paging, built from the shared server contract.
import { api, state as apiState } from './api.js';
import { mountSportsWatch } from './sports-watch.js';
import { createSportsFeedCache } from './sports-cache.js';
import { createStatsBuffer } from './sports-stats-buffer.js';
import {
  STATS_DELAY_OPTIONS, normalizeStatsDelay, delayPreferenceKey, readStatsDelay,
  writeStatsDelay, statsDelayLabel, statsDelayNote, selectDelayedEvent,
} from './sports-stats-delay.js';
import { sportGlyph } from './sports-glyphs.js';
import { esc, $, $$, el, toast } from './ui.js';
import {
  STATUS_FILTERS, dateChoices, filterEvents, buildLanes, pickFeatured, teamScore,
  statusText, startLabel, eventTitle, eventStatus, watchActionLabel, freshness, pageItems,
  refreshIntervalMs, sportOptions, leagueName, scoresHidden, labelSport, detailRefreshMs,
  searchResultGroups, formatDetailStatus, buildEventAccessibilityLabel,
} from './sports-model.js';
import {
  EMPTY_SPORTS_PREFERENCES, SUPPORTED_SPORT_PINS, supportedCompetitionFollows,
  matchupFollowId, loadSportsPreferences, saveSportsPreferences, updateSupportedPins,
  updateSupportedFollows, updateTeamFollows, prioritizeLanes, isFavoriteEvent,
  isCompetitionFollowed, isTeamFollowed, teamFollowId, pinLabel,
} from './sports-preferences.js';
import {
  SPORTS_APPEARANCES, readSportsAppearance, saveSportsAppearance, readSportsReadability,
  saveSportsReadability, applySportsAppearance, applySportsReadability, DEFAULT_READABILITY,
} from './sports-appearance.js';

import { compareCyclingCalendar, cyclingCalendarPeriod, cyclingDate } from './sports-cycling-model.js';
import { raceSessionList, defaultRaceSession, raceDate } from './sports-race-model.js';
import {
  tennisSets, tennisEventContext, rugbyPlaySymbol, rugbyLatestPlays, rugbyStatus,
  combatBouts, combatResult, combatPortrait, combatTape, combatBarValues,
} from './sports-sport-model.js';
import {
  footballReplayPath, footballReplaySituation, footballPlayResult, previousPlayTrail,
} from './sports-replay-model.js';

const PAGE_SIZE = 12;
const SPOILER_KEY = 'wren.sports.spoiler';

const state = {
  root: null,
  status: null,
  leagues: [],
  dates: [],
  dateId: 'today',
  board: { events: [], leagues: [] },
  error: '',
  loading: false,
  statusFilter: 'all',
  sport: 'all',
  league: 'all',
  query: '',
  expanded: false,
  page: 0,
  spoiler: false,
  timer: null,
  statsTimer: null,
  seq: 0,
  watch: null,
  feedCache: createSportsFeedCache(),
  snapshot: null,
  prefs: null,
  prefError: '',
  prefsOpen: false,
  prefDraft: null,
  prefMessage: '',
  prefMessageError: false,
  prefTeams: new Map(),
  prefLeague: '',
  prefLoadingTeams: false,
  followingOnly: false,	appearance: 'midnight',
	readability: { ...DEFAULT_READABILITY },
	races: [],
	raceLeagues: [],
	cycling: [],
	cyclingState: '',
	cyclingReason: '',
	// Server search runs beside the local feed filter so a channel, programme,
	// event or team outside the loaded day is still reachable.
	search: { query: '', results: null, loading: false, error: '', controller: null },
	searchTimer: null,
};

const readSpoiler = () => { try { return localStorage.getItem(SPOILER_KEY) === '1'; } catch { return false; } };
const writeSpoiler = (on) => { try { on ? localStorage.setItem(SPOILER_KEY, '1') : localStorage.removeItem(SPOILER_KEY); } catch {} };
const logoURL = (url) => (url ? `/api/sports/logo?url=${encodeURIComponent(url)}` : '');

export async function viewSports() {
  stopSports();
  const seq = ++state.seq;
  // Entering the hub is an explicit freshness boundary: cached structural
  // feeds from an earlier visit are never reused.
  state.feedCache.clear();
  state.spoiler = readSpoiler();
  state.expanded = false;
  state.page = 0;
  state.error = '';
  state.prefsOpen = false;
  state.prefMessage = '';
  state.prefMessageError = false;
  state.followingOnly = false;
  state.appearance = readSportsAppearance();
  state.readability = readSportsReadability();
  state.root = el('<section class="sports"><div class="center" style="min-height:40vh"><div class="spinner"></div></div></section>');
  applySportsAppearance(state.root, state.appearance);
  applySportsReadability(state.root, state.readability);
  await Promise.all([loadStatus(), loadPreferences()]);
  if (seq !== state.seq) return state.root;	await loadBoard();
	if (seq !== state.seq) return state.root;
	render();
	// Racing and cycling providers are separate boards with their own cadence;
	// they load after the scoreboard so a slow organizer never delays the feed.
	loadAuxiliary(seq);
	return state.root;
}

async function loadAuxiliary(seq) {
	const [races, cycling] = await Promise.allSettled([
		api('/api/sports/races'),
		api('/api/sports/cycling'),
	]);
	if (seq !== state.seq || !state.root?.isConnected) return;
	if (races.status === 'fulfilled') {
		state.races = races.value.events || [];
		state.raceLeagues = races.value.leagues || [];
	}
	if (cycling.status === 'fulfilled') {
		state.cycling = cycling.value.data || [];
		state.cyclingState = cycling.value.state || '';
		state.cyclingReason = cycling.value.reason || '';
	}
	paint();
}

async function loadPreferences() {
  state.prefError = '';
  try {
    state.snapshot = await loadSportsPreferences();
    state.prefs = state.snapshot.preferences;
  } catch (err) {
    state.snapshot = null;
    state.prefs = null;
    state.prefError = err.status === 404
      ? 'Sports favorites are unavailable on this server. You can still browse every event.'
      : 'Could not load sports favorites. You can still browse and try again.';
  }
}

const clonePrefs = (value) => JSON.parse(JSON.stringify(value || EMPTY_SPORTS_PREFERENCES));
const supportedFollowSet = () => new Set(supportedCompetitionFollows(state.leagues).map((item) => item.id));

// Leaving the hub or event page stops its refresh timer and any docked
// preview, so a closed page does not hold a provider connection.
export function stopSports() {
  state.seq++;
  clearTimeout(state.timer);
  state.timer = null;
  clearTimeout(state.searchTimer);
  state.searchTimer = null;
  state.search.controller?.abort();
  state.search = { ...state.search, controller: null, loading: false };
  clearTimeout(state.detailTimer);
  state.detailTimer = null;
  clearInterval(state.statsTimer);
  state.statsTimer = null;
  state.watch?.stop();
  state.watch = null;
}

async function loadStatus() {
  try {
    state.status = await api('/api/sports/status');
    if (!state.status.enabled) {
      state.error = 'disabled';
      return;
    }
    const catalog = await api('/api/sports/leagues');
    state.leagues = (catalog.leagues || []).filter((l) => l.enabled);
    const supported = state.status.dateQueries ? ['yesterday', 'today', 'tomorrow'] : ['today'];
    state.dates = dateChoices(new Date(), supported);
    state.dateId = state.dates.some((d) => d.id === state.dateId) ? state.dateId : 'today';
  } catch (err) {
    state.error = err.message;
  }
}

// loadBoard always asks the server: the cache exists to paint a remembered day
// instantly while its own request is in flight, never to skip that request.
// `fresh` is used by explicit refresh/retry after invalidating the cache.
async function loadBoard({ fresh = false } = {}) {
  if (state.error) return;
  if (fresh) state.feedCache.clear();
  state.loading = true;
  const date = state.dates.find((d) => d.id === state.dateId)?.date;
  try {
    const board = await api(`/api/sports/hub?date=${encodeURIComponent(date)}`);
    state.board = state.feedCache.write({
      date,
      events: board.events || [],
      leagues: board.leagues || [],
      updatedAt: board.updatedAt,
      stale: board.stale,
    });
    state.error = '';
  } catch (err) {
    // Keep the previous board; the freshness line explains the failure.
    if (!state.board.events.length) state.error = err.message;
    state.board = { ...state.board, stale: true, error: err.message };
  } finally {
    state.loading = false;
  }
}

// paintCachedDay shows the remembered structural feed for a date switch before
// its request settles. The freshness line still comes from the cached snapshot.
function paintCachedDay(date) {
  const cached = state.feedCache.read(date);
  if (!cached) return false;
  state.board = cached;
  return true;
}

function render() {
  if (!state.root) return;
  applySportsAppearance(state.root, state.appearance);
  applySportsReadability(state.root, state.readability);
  if (state.error === 'disabled') {
    state.root.innerHTML = `<div class="empty"><h2>Sports is turned off</h2>
      <p>The server owner can enable it in Manage → Settings → Sports.</p></div>`;
    return;
  }
  if (state.error && state.error !== 'disabled') {
    state.root.innerHTML = `<div class="empty"><h2>Couldn't load Sports</h2><p>${esc(state.error)}</p>
      <button class="btn primary" id="sports-retry">Try again</button></div>`;
    $('#sports-retry', state.root).onclick = () => retry();
    return;
  }
  const fresh = freshness(state.board.updatedAt, state.board.stale);
  state.root.innerHTML = `
    <div class="sports-head">
      <div><h1>Sports</h1><div class="fresh ${fresh.stale ? 'stale' : ''}" id="sports-fresh">${esc(fresh.label)}</div></div>
      <div class="sports-actions">
        <button class="btn" id="sports-customize" aria-pressed="${state.prefsOpen}">Customize</button>
        <button class="btn" id="sports-refresh" aria-label="Refresh scores">Refresh</button>
        <button class="btn ${state.spoiler ? 'primary' : ''}" id="sports-spoiler" aria-pressed="${state.spoiler}">${state.spoiler ? 'Show scores' : 'Hide scores'}</button>
      </div>
    </div>
    <div class="date-rail" role="tablist" aria-label="Day">
      ${state.dates.map((d) => `<button class="date ${d.id === state.dateId ? 'active' : ''}" role="tab" aria-selected="${d.id === state.dateId}" data-date="${d.id}">
        <span class="dow">${esc(d.weekday)}</span><span class="dnum">${esc(d.day)}</span><span class="dlab">${esc(d.label)}</span></button>`).join('')}
    </div>
    <div class="sports-filters">
      <div class="status-tabs" role="tablist" aria-label="Status">
        ${STATUS_FILTERS.map((f) => `<button class="tab ${state.statusFilter === f.id ? 'active' : ''}" role="tab" aria-selected="${state.statusFilter === f.id}" data-status="${f.id}">${esc(f.label)}</button>`).join('')}
      </div>
      <select id="sports-sport" aria-label="Sport">
        <option value="all">All sports</option>
        ${sportOptions(state.leagues).map((s) => `<option value="${esc(s.id)}"${state.sport === s.id ? ' selected' : ''}>${esc(s.label)}</option>`).join('')}
      </select>
      <select id="sports-league" aria-label="Competition">
        <option value="all">All competitions</option>
        ${state.leagues.filter((l) => state.sport === 'all' || l.sport === state.sport).map((l) => `<option value="${esc(l.id)}"${state.league === l.id ? ' selected' : ''}>${esc(l.name)}</option>`).join('')}
      </select>
      <input id="sports-search" type="search" placeholder="Search teams, events, channels or TV" value="${esc(state.query)}" aria-label="Search sports">
      ${state.prefs ? `<button class="btn ${state.followingOnly ? 'primary' : ''}" id="sports-following" aria-pressed="${state.followingOnly}">Following</button>` : ''}
      ${state.expanded ? '<button class="btn" id="sports-collapse">Back to lanes</button>' : '<button class="btn" id="sports-expand">All events</button>'}
    </div>
    <div id="sports-body"></div>`;
  bind();
  paint();
  schedule();
}

function bind() {
  $$('[data-date]', state.root).forEach((b) => {
    b.onclick = async () => {
      if (b.dataset.date === state.dateId) return;
      state.dateId = b.dataset.date;
      state.page = 0;
      paintCachedDay(state.dates.find((d) => d.id === state.dateId)?.date);
      render();
      await loadBoard();
      if (state.root?.isConnected) { render(); }
    };
  });
  $$('[data-status]', state.root).forEach((b) => {
    b.onclick = () => {
      state.statusFilter = b.dataset.status;
      state.page = 0;
      render();
    };
  });
  $('#sports-sport', state.root).onchange = (e) => {
    state.sport = e.target.value;
    state.league = 'all';
    state.page = 0;
    render();
  };
  $('#sports-league', state.root).onchange = (e) => {
    state.league = e.target.value;
    state.page = 0;
    render();
  };
  const search = $('#sports-search', state.root);
  search.oninput = (e) => {
    clearTimeout(state.searchTimer);
    state.query = e.target.value.trim();
    state.page = 0;
    state.searchTimer = setTimeout(() => { paint(); runSearch(); }, 180);
  };
  $('#sports-expand', state.root)?.addEventListener('click', () => { state.expanded = true; state.page = 0; render(); });
  $('#sports-collapse', state.root)?.addEventListener('click', () => { state.expanded = false; render(); });
  $('#sports-customize', state.root).onclick = () => openPreferences();
  $('#sports-following', state.root)?.addEventListener('click', () => {
    state.followingOnly = !state.followingOnly;
    state.page = 0;
    render();
  });
  $('#sports-refresh', state.root).onclick = async (e) => {
    e.currentTarget.disabled = true;
    await loadBoard({ fresh: true });
    if (!state.root?.isConnected) return;
    render();
    toast(state.board.error ? 'Could not refresh. Showing the last scores.' : 'Scores refreshed');
  };
  $('#sports-spoiler', state.root).onclick = () => {
    state.spoiler = !state.spoiler;
    writeSpoiler(state.spoiler);
    render();
  };
}

function filteredEvents() {
  return filterEvents(state.board.events, {
    status: state.statusFilter, sport: state.sport, league: state.league, query: state.query,
  }, state.leagues);
}

function paint() {
  const body = $('#sports-body', state.root);
  if (!body) return;
  if (state.prefsOpen) {
    body.innerHTML = preferencesPanel();
    bindPreferences(body);
    return;
  }
  const filtered = filteredEvents();
  const events = state.followingOnly && state.prefs
    ? filtered.filter((event) => isFavoriteEvent(event, state.prefs, state.leagues))
    : filtered;
  if (!events.length) {
    // Server results come first: an event outside today's feed is still a
    // useful answer when the day itself has nothing that matches.
    body.innerHTML = `${searchSection()}${emptyState()}`;
    $('#sports-clear', body)?.addEventListener('click', () => {
      state.statusFilter = 'all';
      state.sport = 'all';
      state.league = 'all';
      state.query = '';
      state.expanded = false;
      state.followingOnly = false;
      resetSearch();
      render();
    });
    return;
  }
  if (state.expanded) {
    body.innerHTML = `${searchSection()}${expandedFeed(events)}`;
    bindPager(events);
    return;
  }
  const featured = personalizedFeatured(events);
  const lanes = prioritizeLanes(
    buildLanes(events, state.leagues, { featuredId: featured?.id || '' }),
    state.prefs, state.leagues,
  );  body.innerHTML = `${searchSection()}${featured ? featuredCard(featured) : ''}
    ${racingSection()}
    ${cyclingSection()}
    <div class="lanes">${lanes.map(lane).join('')}</div>
    ${lanes.length ? '' : '<p class="muted">Every event today is in the featured card.</p>'}`;
}

// The hub search queries the server instead of only the loaded day, so a
// channel, EPG programme, event or team outside this feed is still reachable.
let searchSeq = 0;

function resetSearch() {
  clearTimeout(state.searchTimer);
  state.search.controller?.abort();
  state.search = { query: '', results: null, loading: false, error: '', controller: null };
}

async function runSearch() {
  const query = state.query;
  if (query.length < 2) {
    resetSearch();
    paint();
    return;
  }
  if (state.search.query === query && (state.search.results || state.search.loading)) return;
  state.search.controller?.abort();
  const controller = new AbortController();
  const seq = ++searchSeq;
  state.search = { query, results: null, loading: true, error: '', controller };
  paint();
  try {
    const results = await api(`/api/sports/search?q=${encodeURIComponent(query)}`, { signal: controller.signal });
    if (seq !== searchSeq) return;
    state.search = { query, results, loading: false, error: '', controller: null };
  } catch (err) {
    if (err.name === 'AbortError' || seq !== searchSeq) return;
    state.search = { query, results: null, loading: false, error: err.message || 'Search is unavailable.', controller: null };
  }
  if (state.root?.isConnected) paint();
}

function searchSection() {
  const search = state.search;
  if (state.query.length < 2 || search.query !== state.query) return '';
  const head = `<h2>Search all of Wren TV <span class="small muted">“${esc(state.query)}”</span></h2>`;
  if (search.loading) {
    return `<section class="event-section search-results">${head}<p class="small muted">Searching channels, the guide, events and teams…</p></section>`;
  }
  if (search.error) {
    return `<section class="event-section search-results">${head}<p class="small muted">Live TV, guide, event and team search is unavailable. The list below is filtered locally.</p></section>`;
  }
  const groups = searchResultGroups(search.results || {}, state.leagues);
  if (!groups.length) {
    return `<section class="event-section search-results">${head}<p class="small muted">No channel, programme, event or team matches this search.</p></section>`;
  }
  return `<section class="event-section search-results">${head}
    ${groups.map((group) => `<div class="search-group"><h3>${esc(group.label)} <span class="small muted">${group.items.length}</span></h3>
      <div class="search-list">${group.items.map((item) => `<div class="search-row">
        <div class="search-text"><div class="search-title">${esc(item.title)}</div>
          <div class="small muted">${esc([item.subtitle, item.meta].filter(Boolean).join(' · '))}</div></div>
        ${item.href ? `<a class="btn small" href="${esc(item.href)}">${esc(item.action)}</a>` : ''}
      </div>`).join('')}</div></div>`).join('')}
  </section>`;
}

function emptyState() {
  const scope = state.query ? ` for “${esc(state.query)}”` : '';
  const following = state.followingOnly ? ' and your favorites' : '';
  return `<div class="empty"><h2>No events${scope}${following}</h2>
    <p>${state.followingOnly ? 'No events match your favorites on this day. Turn off Following to see everything.' : state.statusFilter === 'all' ? 'Nothing is scheduled for this day in the selected competitions.' : 'No events match this status.'}</p>
    <button class="btn" id="sports-clear">Clear filters</button></div>`;
}

// personalizedFeatured prefers a followed/pinned event, matching MediaStorm's
// prioritizeSportsHub, then falls back to the ordinary first live event.
function personalizedFeatured(events) {
  if (!state.prefs) return pickFeatured(events);
  const favorites = events.filter((event) => isFavoriteEvent(event, state.prefs, state.leagues));
  return favorites.find((event) => eventStatus(event) === 'live') || favorites[0] || pickFeatured(events);
}

function availability(leagueId) {
  return state.board.leagues?.find((l) => l.league === leagueId);
}

function lane(entry) {
  const avail = availability(entry.league.id);
  return `<section class="lane">
    <div class="lane-head"><h2>${esc(entry.name)}</h2>
      <span class="small muted">${entry.events.length} event${entry.events.length === 1 ? '' : 's'}</span>
      ${avail?.unavailable ? '<span class="badge warn">Unavailable</span>' : avail?.stale ? '<span class="badge warn">Delayed</span>' : ''}
    </div>
    <div class="sports-rail">${entry.events.map((e) => card(e)).join('')}</div>
  </section>`;
}

// A race weekend is a field and several sessions, not two teams. It links to
// the race page and only offers sessions the provider actually published.
function racingSection() {
  if (!state.races.length) return '';
  if (state.sport !== 'all' && state.sport !== 'motorsport') return '';
  if (state.league !== 'all' && !state.races.some((race) => race.league === state.league)) return '';
  const visible = state.statusFilter === 'scheduled'
    ? state.races.filter((race) => race.status === 'scheduled')
    : state.statusFilter === 'final'
      ? state.races.filter((race) => race.status === 'final')
      : state.races;
  if (!visible.length) return '';
  return `<section class="lane racing-lane">
    <div class="lane-head"><h2>Racing</h2><span class="small muted">${visible.length} weekend${visible.length === 1 ? '' : 's'}</span></div>
    <div class="sports-grid race-grid">${visible.map(raceCard).join('')}</div>
  </section>`;
}

function raceCard(weekend) {
  const sessions = raceSessionList(weekend);
  const main = defaultRaceSession(weekend);
  const avail = state.raceLeagues?.find((entry) => entry.league === weekend.league);
  const status = weekend.status || 'scheduled';
  return `<article class="race-card ${status}">
    <div class="sc-top"><span class="league">${esc(leagueName(weekend.league, state.leagues) || weekend.league)}</span>
      <span class="status ${esc(status)}">${esc(status === 'live' ? '● Live' : status === 'final' ? 'Final' : 'Upcoming')}</span></div>
    <h3>${esc(weekend.title)}</h3>
    <div class="small muted">${esc(raceDate(weekend.startTime))}${weekend.venueName ? ` · ${esc(weekend.venueName)}` : ''}</div>
    ${avail?.stale || avail?.unavailable ? '<span class="badge warn">Delayed</span>' : ''}
    <div class="session-list">${sessions.map((session) => `<a class="chip small" href="/sports/race/${encodeURIComponent(weekend.league)}/${encodeURIComponent(weekend.id)}/${encodeURIComponent(session.id)}">
      ${esc(session.sessionType || 'Session')}<span class="small muted">${esc(raceDate(session.startTime))}</span></a>`).join('')}</div>
    ${main ? `<a class="btn small primary" href="/sports/race/${encodeURIComponent(weekend.league)}/${encodeURIComponent(weekend.id)}/${encodeURIComponent(main.id)}">Open race</a>` : ''}
  </article>`;
}

// Cycling is a calendar of stage races. Current and upcoming races lead; a
// schedule-only race says so instead of implying live timing.
function cyclingSection() {
  if (!state.cycling.length) return '';
  if (state.sport !== 'all' && state.sport !== 'cycling') return '';
  if (state.league !== 'all') return '';
  const today = new Date();
  const day = `${today.getFullYear()}-${String(today.getMonth() + 1).padStart(2, '0')}-${String(today.getDate()).padStart(2, '0')}`;
  const visible = [...state.cycling]
    .filter((race) => ['current', 'upcoming'].includes(cyclingCalendarPeriod(race, day)))
    .sort((a, b) => compareCyclingCalendar(a, b, day))
    .slice(0, 6);
  if (!visible.length) return '';
  return `<section class="lane cycling-lane">
    <div class="lane-head"><h2>Cycling</h2><span class="small muted">${esc(state.cyclingState || '')}</span></div>
    <div class="sports-grid race-grid">${visible.map((race) => `<a class="race-card" href="/sports/cycling/${encodeURIComponent(race.id)}">
      <div class="sc-top"><span class="league">Cycling · ${esc(race.category || '')}</span>
        <span class="status ${cyclingCalendarPeriod(race, day) === 'current' ? 'live' : 'scheduled'}">${cyclingCalendarPeriod(race, day) === 'current' ? 'Live' : 'Upcoming'}</span></div>
      <h3>${esc(race.name)}</h3>
      <div class="small muted">${esc(cyclingDate(race.startDate))} – ${esc(cyclingDate(race.endDate))}</div>
      <div class="small muted">${(race.stages || []).length} stage${(race.stages || []).length === 1 ? '' : 's'}${race.scheduleOnly || race.calendarOnly ? ' · schedule only' : ''}</div>
    </a>`).join('')}</div>
  </section>`;
}

function expandedFeed(events) {
  const page = pageItems(events, state.page, PAGE_SIZE);
  state.page = page.page;
  return `<section class="feed"><div class="lane-head"><h2>All events</h2><span class="small muted">${events.length}</span></div>
    <div class="sports-grid">${page.items.map((e) => card(e)).join('')}</div>
    ${page.pages > 1 ? `<div class="pager"><button class="btn" data-page="-1" ${page.page === 0 ? 'disabled' : ''}>Previous</button>
      <span class="small muted">Page ${page.page + 1} of ${page.pages}</span>
      <button class="btn" data-page="1" ${page.page >= page.pages - 1 ? 'disabled' : ''}>Next</button></div>` : ''}
  </section>`;
}

function bindPager(events) {
  $$('[data-page]', state.root).forEach((b) => {
    b.onclick = () => {
      state.page += Number(b.dataset.page);
      paint();
      $('#sports-body', state.root)?.scrollIntoView({ block: 'start', behavior: 'smooth' });
    };
  });
  if (!events.length) state.page = 0;
}

// The sticky header uses the same sport-aware status rules as MediaStorm, not
// the provider's raw detail string.
function eventHeadMarkup(event, league) {
  return `<div class="small muted">${sportGlyph(event.sport)}${esc(league)} · ${esc(formatDetailStatus(event))}</div>
    <h1>${esc(eventTitle(event))}</h1>
    <div class="small muted">${event.venueName ? `${esc(event.venueName)}` : ''}${event.broadcasts?.length ? `${event.venueName ? ' · ' : ''}${esc(event.broadcasts.join(' · '))}` : ''}</div>`;
}

function eventTeamsMarkup(event) {
  const status = eventStatus(event);
  return (event.participants || []).map((p) => `<div class="event-team${p.winner ? ' win' : ''}">
    ${combatEvent(event) ? combatMark(event, p) : teamMark(p)}<span class="nm">${esc(p.name)}</span>
    ${status === 'scheduled' ? '' : `<b>${esc(p.score ?? '')}</b>`}
    ${p.record ? `<span class="small muted">${esc(p.record)}</span>` : ''}
  </div>`).join('');
}

function teamMark(team, small = false) {
  const logo = logoURL(team?.logoUrl);
  return `<span class="team-mark${small ? ' small' : ''}">${logo ? `<img loading="lazy" src="${esc(logo)}" alt="" onerror="this.remove()">` : ''}</span>`;
}

function card(event) {
  const status = eventStatus(event);
  const hidden = scoresHidden(event, state.spoiler);
  const participants = event.participants || [];
  const matchup = (event.eventKind || 'matchup') === 'matchup' && participants.length >= 2;
  const rows = matchup
    ? participants.slice(0, 2).map((p) => `<div class="sc-team${p.winner ? ' win' : ''}">
        ${teamMark(p)}<span class="nm">${esc(p.name)}</span>
        ${p.abbreviation && p.abbreviation !== p.name ? `<span class="abbr muted">${esc(p.abbreviation)}</span>` : ''}
        ${hidden ? '<span class="score-dot" aria-label="Score hidden">–</span>' : `<b class="score">${esc(teamScore(event, p, false))}</b>`}
      </div>`).join('')
    : `<div class="sc-title">${esc(eventTitle(event))}</div>
       <div class="sc-participants">${participants.slice(0, 4).map((p) => `<span>${esc(p.name)}</span>`).join('') || '<span class="muted">Field not announced</span>'}</div>`;
  const favorite = state.prefs && isFavoriteEvent(event, state.prefs, state.leagues);
  return `<a class="sports-card ${status}${favorite ? ' favorite' : ''}" href="/sports/event/${encodeURIComponent(event.id)}" aria-label="${esc(buildEventAccessibilityLabel(event))}">
    <div class="sc-top"><span class="league">${sportGlyph(event.sport)}${favorite ? '<span class="fav" aria-label="Favorite">★</span> ' : ''}${esc(leagueName(event.league, state.leagues))}</span>
      <span class="status ${status}">${esc(status === 'live' ? '● Live' : status === 'final' ? 'Final' : 'Upcoming')}</span></div>
    ${rows}
    <div class="sc-meta"><span>${esc(status === 'scheduled' ? startLabel(event) : statusText(event, state.spoiler))}</span>
      ${event.venueName ? `<span class="venue">${esc(event.venueName)}</span>` : ''}</div>
    ${event.broadcasts?.length ? `<div class="sc-broadcast small muted">${esc(event.broadcasts.join(' · '))}</div>` : ''}
  </a>`;
}

function featuredCard(event) {
  const status = eventStatus(event);
  const hidden = scoresHidden(event, state.spoiler);
  const participants = (event.participants || []).slice(0, 2);
  const favorite = state.prefs && isFavoriteEvent(event, state.prefs, state.leagues);
  return `<section class="featured-event ${status}${favorite ? ' favorite' : ''}">
    <div class="fe-top"><span class="league">${sportGlyph(event.sport)}${favorite ? '<span class="fav" aria-label="Favorite">★</span> ' : ''}${esc(leagueName(event.league, state.leagues))}</span>
      <span class="status ${status}">${esc(status === 'live' ? '● Live' : status === 'final' ? 'Final' : 'Upcoming')}</span>
      <span class="small muted">${esc(status === 'scheduled' ? startLabel(event) : statusText(event, state.spoiler))}</span></div>
    <a class="fe-body" href="/sports/event/${encodeURIComponent(event.id)}" aria-label="${esc(buildEventAccessibilityLabel(event))}">
      <div class="fe-teams">${participants.map((p) => `<div class="fe-team">${teamMark(p)}<span>${esc(p.name)}</span>
        ${hidden ? '<span class="score-dot">–</span>' : `<b>${esc(teamScore(event, p, false))}</b>`}</div>`).join('')}</div>
      <div class="fe-info"><h2>${esc(eventTitle(event))}</h2>
        <div class="small muted">${esc(event.venueName || '')}${event.broadcasts?.length ? ` · ${esc(event.broadcasts.join(' · '))}` : ''}</div></div>
    </a>
  </section>`;
}

function schedule() {
  clearTimeout(state.timer);
  const ms = refreshIntervalMs(state.board.events, state.board.stale);
  state.timer = setTimeout(async () => {
    if (!state.root?.isConnected) { stopSports(); return; }
    if (document.hidden || location.pathname !== '/sports') { schedule(); return; }
    await loadBoard();
    if (state.root?.isConnected && !state.error) {
      const fresh = freshness(state.board.updatedAt, state.board.stale);
      const line = $('#sports-fresh', state.root);
      if (line) { line.textContent = fresh.label; line.classList.toggle('stale', fresh.stale); }
      paint();
    }
    schedule();
  }, ms);
}

async function retry() {
  state.error = '';
  state.feedCache.clear();
  await loadStatus();
  await loadBoard({ fresh: true });
  render();
}

// ---------- favorites, appearance and readability ----------

function openPreferences() {
  state.prefsOpen = true;
  state.prefDraft = state.prefs ? clonePrefs(state.prefs) : clonePrefs(EMPTY_SPORTS_PREFERENCES);
  // The editor only offers follows the current catalog supports. Existing
  // unsupported ids stay in the saved document and are re-added on save.
  const supported = supportedFollowSet();
  state.prefDraft.competitionIds = state.prefDraft.competitionIds.filter((id) => supported.has(id));
  state.prefDraft.pinnedSportIds = state.prefDraft.pinnedSportIds.filter((id) =>
    SUPPORTED_SPORT_PINS.some((sport) => sport.id === id));
  state.prefLeague = state.leagues.find((l) => l.supportsTeams)?.id || state.prefLeague || 'nba';
  loadDraftTeams();
  render();
}

function closePreferences() {
  state.prefsOpen = false;
  state.prefDraft = null;
  state.prefMessage = '';
  state.prefMessageError = false;
  render();
}

// Team follows are stored as "league:team", so a followed team can be labelled
// from its own league even before that league is picked in the add-team menu.
function loadDraftTeams() {
  const leagues = new Set([state.prefLeague]);
  for (const id of state.prefDraft?.teamIds || []) {
    const split = id.lastIndexOf(':');
    if (split > 0) leagues.add(id.slice(0, split));
  }
  for (const league of leagues) {
    ensureTeams(league).then(() => { if (state.prefsOpen) paint(); });
  }
}

async function ensureTeams(league) {
  if (!league || state.prefTeams.has(league)) return;
  state.prefLoadingTeams = true;
  try {
    const data = await api(`/api/sports/teams?league=${encodeURIComponent(league)}`);
    state.prefTeams.set(league, data.teams || []);
  } catch {
    state.prefTeams.set(league, []);
  } finally {
    state.prefLoadingTeams = false;
  }
}

function teamLabel(id) {
  for (const teams of state.prefTeams.values()) {
    const team = teams.find((t) => t.id === id);
    if (team) return team.name;
  }
  return id;
}

function preferencesPanel() {
  const draft = state.prefDraft || clonePrefs(EMPTY_SPORTS_PREFERENCES);
  const supported = supportedCompetitionFollows(state.leagues);
  const groups = [...new Set(supported.map((item) => item.sport))]
    .map((sport) => ({ sport, items: supported.filter((item) => item.sport === sport) }))
    .sort((a, b) => a.sport.localeCompare(b.sport));
  const teams = state.prefTeams.get(state.prefLeague) || [];
  const availableTeams = teams.filter((team) => !draft.teamIds.includes(team.id));
  return `<div class="prefs">
    <div class="prefs-head"><div><h2>Customize Sports</h2>
      <p class="small muted">Pin sports, follow competitions and follow teams. Everything not followed stays available.</p></div>
      <div class="sports-actions"><button class="btn" data-prefs-done>Done</button></div></div>
    ${state.prefMessage ? `<p class="small ${state.prefMessageError ? 'error' : 'muted'}" role="status">${esc(state.prefMessage)}</p>` : ''}

    <section class="prefs-panel"><h3>Pinned sports</h3>
      <p class="small muted">Pinned sports move first in the hub. Use Earlier and Later to order them.</p>
      <div class="pin-grid">${SUPPORTED_SPORT_PINS.map((sport) => {
        const index = draft.pinnedSportIds.indexOf(sport.id);
        return `<div class="pin${index >= 0 ? ' on' : ''}">
          <button type="button" class="btn small pin-main" data-pin="${esc(sport.id)}" aria-pressed="${index >= 0}">
            ${index >= 0 ? `<b>${index + 1}.</b> ` : ''}${esc(sport.label)}${index >= 0 ? ' ✓' : ''}</button>
          ${index >= 0 ? `<button type="button" class="btn small" data-pin-move="up" data-pin-id="${esc(sport.id)}" ${index === 0 ? 'disabled' : ''} aria-label="Move ${esc(sport.label)} earlier">↑</button>
          <button type="button" class="btn small" data-pin-move="down" data-pin-id="${esc(sport.id)}" ${index === draft.pinnedSportIds.length - 1 ? 'disabled' : ''} aria-label="Move ${esc(sport.label)} later">↓</button>` : ''}
        </div>`;
      }).join('')}</div>
    </section>

    <section class="prefs-panel"><h3>Followed competitions</h3>
      <p class="small muted">Followed competitions rank ahead of others in the hub.</p>
      ${groups.map((group) => `<div class="follow-group"><div class="follow-head">${esc(labelSport(group.sport))}</div>
        <div class="follow-list">${group.items.map((item) => `<label class="check">
          <input type="checkbox" data-follow="${esc(item.id)}" ${draft.competitionIds.includes(item.id) ? 'checked' : ''}> ${esc(item.label)}</label>`).join('')}</div></div>`).join('') || '<p class="muted small">No competitions are enabled.</p>'}
    </section>

    <section class="prefs-panel"><h3>Followed teams</h3>
      <p class="small muted">Followed team games are marked with a star and rank first.</p>
      <div class="team-follows">${draft.teamIds.map((id) => `<span class="team-follow">${esc(teamLabel(id))}
        <button type="button" class="btn ghost small" data-team-remove="${esc(id)}" aria-label="Unfollow ${esc(teamLabel(id))}">✕</button></span>`).join('') || '<span class="muted small">No teams followed yet.</span>'}</div>
      <div class="team-add">
        <select data-team-league aria-label="Team competition">${state.leagues.filter((l) => l.supportsTeams).map((l) => `<option value="${esc(l.id)}"${state.prefLeague === l.id ? ' selected' : ''}>${esc(l.name)}</option>`).join('')}</select>
        <select data-team-pick aria-label="Team">${state.prefLoadingTeams ? '<option>Loading teams…</option>' : availableTeams.length ? availableTeams.map((team) => `<option value="${esc(team.id)}">${esc(team.name)}</option>`).join('') : '<option value="">No teams available</option>'}</select>
        <button type="button" class="btn" data-team-add ${availableTeams.length ? '' : 'disabled'}>Follow team</button>
      </div>
    </section>

    <section class="prefs-panel"><h3>Appearance</h3>
      <p class="small muted">Applies to the Sports area on this device. Wren TV follows your app colours.</p>
      <div class="theme-grid">${SPORTS_APPEARANCES.map((theme) => `<button type="button" class="theme-option${state.appearance === theme.id ? ' active' : ''}" data-theme="${esc(theme.id)}" aria-pressed="${state.appearance === theme.id}">
        <span class="theme-swatch" style="background:${esc(theme.page[1])};border-color:${esc(theme.border)}">
          <i style="background:${esc(theme.focusedCard[0])};border-color:${esc(theme.accent)}"></i></span>
        <span><b>${esc(theme.name)}</b><span class="small muted">${esc(theme.description)}</span></span>
        ${state.appearance === theme.id ? '<span class="ok">✓</span>' : ''}</button>`).join('')}</div>
    </section>

    <section class="prefs-panel"><h3>Readability</h3>
      <label class="check"><input type="checkbox" data-read="largeText" ${state.readability.largeText ? 'checked' : ''}> Larger sports text</label>
      <label class="check"><input type="checkbox" data-read="reduceMotion" ${state.readability.reduceMotion ? 'checked' : ''}> Reduce motion</label>
      <label class="check"><input type="checkbox" data-read="strongFocus" ${state.readability.strongFocus ? 'checked' : ''}> Stronger focus outline</label>
      <label class="check"><input type="checkbox" data-spoiler ${state.spoiler ? 'checked' : ''}> Hide scores until a game is opened</label>
    </section>

    <div class="prefs-actions">
      <button type="button" class="btn primary" data-prefs-save ${state.snapshot ? '' : 'disabled'}>Save favorites</button>
      <button type="button" class="btn" data-prefs-reload>Reload saved favorites</button>
      <button type="button" class="btn ghost" data-prefs-done>Done</button>
    </div>
  </div>`;
}

function bindPreferences(root) {
  const markDirty = () => { state.prefMessage = ''; state.prefMessageError = false; };
  $$('[data-pin]', root).forEach((button) => button.onclick = () => {
    markDirty();
    const id = button.dataset.pin;
    const index = state.prefDraft.pinnedSportIds.indexOf(id);
    if (index >= 0) state.prefDraft.pinnedSportIds.splice(index, 1);
    else state.prefDraft.pinnedSportIds.push(id);
    paint();
  });
  $$('[data-pin-move]', root).forEach((button) => button.onclick = () => {
    markDirty();
    const list = state.prefDraft.pinnedSportIds;
    const index = list.indexOf(button.dataset.pinId);
    const next = button.dataset.pinMove === 'up' ? index - 1 : index + 1;
    if (index < 0 || next < 0 || next >= list.length) return;
    [list[index], list[next]] = [list[next], list[index]];
    paint();
  });
  $$('[data-follow]', root).forEach((box) => box.onchange = () => {
    markDirty();
    const id = box.dataset.follow;
    const list = state.prefDraft.competitionIds;
    if (box.checked && !list.includes(id)) list.push(id);
    if (!box.checked) state.prefDraft.competitionIds = list.filter((value) => value !== id);
  });
  $$('[data-team-remove]', root).forEach((button) => button.onclick = () => {
    markDirty();
    state.prefDraft.teamIds = state.prefDraft.teamIds.filter((id) => id !== button.dataset.teamRemove);
    paint();
  });
  $('[data-team-league]', root)?.addEventListener('change', async (e) => {
    state.prefLeague = e.target.value;
    await ensureTeams(state.prefLeague);
    if (state.prefsOpen) paint();
  });
  $('[data-team-add]', root)?.addEventListener('click', () => {
    const select = $('[data-team-pick]', root);
    const id = select?.value;
    if (!id) return;
    markDirty();
    if (!state.prefDraft.teamIds.includes(id)) state.prefDraft.teamIds.push(id);
    paint();
  });
  $$('[data-theme]', root).forEach((button) => button.onclick = () => {
    state.appearance = saveSportsAppearance(button.dataset.theme);
    applySportsAppearance(state.root, state.appearance);
    paint();
  });
  $$('[data-read]', root).forEach((box) => box.onchange = () => {
    state.readability = saveSportsReadability({ ...state.readability, [box.dataset.read]: box.checked });
    applySportsReadability(state.root, state.readability);
  });
  $('[data-spoiler]', root)?.addEventListener('change', (e) => {
    state.spoiler = e.target.checked;
    writeSpoiler(state.spoiler);
    render();
  });
  $$('[data-prefs-done]', root).forEach((button) => button.onclick = () => closePreferences());
  $('[data-prefs-save]', root)?.addEventListener('click', () => savePreferences());
  $('[data-prefs-reload]', root)?.addEventListener('click', () => reloadPreferences());
}

async function savePreferences() {
  if (!state.snapshot || !state.prefDraft) return;
  try {
    let next = updateSupportedPins(state.snapshot.preferences, state.prefDraft.pinnedSportIds);
    next = updateSupportedFollows(next, state.prefDraft.competitionIds, state.leagues);
    next = updateTeamFollows(next, state.prefDraft.teamIds);
    const saved = await saveSportsPreferences(state.snapshot, next);
    state.snapshot = saved;
    state.prefs = saved.preferences;
    state.prefDraft = clonePrefs(saved.preferences);
    state.prefMessage = 'Favorites saved.';
    state.prefMessageError = false;
    render();
  } catch (err) {
    state.prefMessage = err.status === 409
      ? 'Favorites changed on another device. Reload saved favorites before editing again.'
      : (err.message || 'Could not save favorites. Reload saved favorites to check.');
    state.prefMessageError = true;
    render();
  }
}

async function reloadPreferences() {
  state.prefMessage = '';
  state.prefMessageError = false;
  await loadPreferences();
  state.prefsOpen = true;
  state.prefDraft = state.prefs ? clonePrefs(state.prefs) : clonePrefs(EMPTY_SPORTS_PREFERENCES);
  const supported = supportedFollowSet();
  state.prefDraft.competitionIds = state.prefDraft.competitionIds.filter((id) => supported.has(id));
  state.prefDraft.pinnedSportIds = state.prefDraft.pinnedSportIds.filter((id) =>
    SUPPORTED_SPORT_PINS.some((sport) => sport.id === id));
  state.prefMessage = state.prefError;
  state.prefMessageError = !!state.prefError;
  loadDraftTeams();
  render();
}

// mutatePreferences applies one follow toggle and replaces the loaded snapshot
// only after the server confirms it, so a stale edit can never win silently.
async function mutatePreferences(mutator) {
  if (!state.snapshot) return false;
  try {
    const saved = await saveSportsPreferences(state.snapshot, mutator(state.prefs || EMPTY_SPORTS_PREFERENCES));
    state.snapshot = saved;
    state.prefs = saved.preferences;
    return true;
  } catch (err) {
    toast(err.status === 409 ? 'Favorites changed elsewhere. Reload Sports to see them.' : err.message);
    return false;
  }
}

// The event page reveals scores even with spoilers on, matching MediaStorm.
// It returns a live element so tab switching, the Watch search and the docked
// preview can attach handlers after the router inserts it.
export async function viewEvent(id) {
  // Opening an event leaves the hub: its feed timer must stop before this page
  // schedules its own detail timer, or the stale hub tick would clear it.
  stopSports();
  state.appearance = readSportsAppearance();
  state.readability = readSportsReadability();
  state.spoiler = readSpoiler();
  const [event] = await Promise.all([
    api(`/api/sports/events/${encodeURIComponent(id)}`),
    state.snapshot || state.prefError ? null : loadPreferences().catch(() => {}),
  ]);
  if (!state.leagues.length) {
    try {
      const catalog = await api('/api/sports/leagues');
      state.leagues = (catalog.leagues || []).filter((l) => l.enabled);
    } catch { /* the event still renders with its league id */ }
  }
  const root = el('<section class="sports event-page"></section>');
  applySportsAppearance(root, state.appearance);
  applySportsReadability(root, state.readability);
  const participants = event.participants || [];
  const detail = event.detail || {};
  const leag = leagueName(event.league, state.leagues) || event.league;
  // Preserve the matchup shape: the Live TV page uses the event identity to
  // rank real matches, and the text keeps the two teams distinct if it falls
  // back to a plain search.
  const search = participants.map((p) => p.name).filter(Boolean).join(' vs ').trim();
  const liveSearch = search
    ? `/live?q=${encodeURIComponent(search)}${event.id ? `&event=${encodeURIComponent(event.id)}` : ''}`
    : '';
  // The page updates in place: the header, teams and the open panel are rebuilt
  // from the refreshed event without dropping the Watch slot or the chosen tab.
  //
  // Stats delay (E12) reuses MediaStorm's buffer rules: the page paints the
  // newest snapshot that is at least `statsDelay` seconds old, drops stale
  // responses, and buffers instead of falling forward when the delay is raised.
  const delayKey = delayPreferenceKey(location.host, apiState.user?.id || apiState.user?.username || '');
  let statsDelay = readStatsDelay(delayKey);
  const buffer = createStatsBuffer((value) => JSON.parse(JSON.stringify(value)));
  let requestId = 0;
  let current = event;
  let display = event;
  let statsBuffering = false;
  let paintedKey = '';
  let active = '';

  root.innerHTML = `
    <a class="btn ghost small" href="/sports">← Sports</a>
    <div class="event-head">${eventHeadMarkup(event, leag)}</div>
    <div id="event-stale">${detail.stale ? '<p class="small stale">Detailed data may be delayed; the last verified snapshot is shown.</p>' : ''}</div>
    <div class="event-teams">${eventTeamsMarkup(event)}</div>
    <div class="event-follows">${followControls(event, participants)}</div>
    ${baseballSituation(detail.sport)}
    <div class="event-actions">
      <button class="btn primary" id="event-watch">${watchActionLabel(event)}</button>
      ${liveSearch ? `<a class="btn" href="${liveSearch}">Browse Live TV</a>` : ''}
    </div>
    <div class="sync-stats">
      <button class="btn" id="event-sync" aria-expanded="false" aria-controls="event-sync-panel" data-delay="${statsDelay}">Sync stats · ${esc(statsDelayLabel(statsDelay))}</button>
      <div class="sync-panel" id="event-sync-panel" hidden role="group" aria-label="Stats delay">
        ${STATS_DELAY_OPTIONS.map((n) => `<button class="btn small" data-delay="${n}" aria-pressed="${n === 0}">${n ? `${n}s` : 'Live'}</button>`).join('')}
      </div>
      <p class="small muted" id="event-sync-note" role="status"></p>
    </div>
    <div id="event-watch-slot"></div>
    <div id="event-channels" class="event-channels"></div>
    <div id="event-tabs" class="event-tabs" role="tablist" aria-label="Event details"></div>
    <div id="event-panel" class="event-panels"></div>`;

  const paintPanel = () => {
    const panel = root.querySelector('#event-panel');
    panel.innerHTML = active ? eventPanel(active, display, display.participants || [], (display.detail || {}).periods || []) : '';
  };
  const paintTabs = () => {
    const tabs = eventTabs(display);
    if (!tabs.some((tab) => tab.id === active)) active = tabs[0]?.id || '';
    const bar = root.querySelector('#event-tabs');
    bar.innerHTML = tabs.length > 1 ? tabs.map((t) => `<button class="tab${t.id === active ? ' active' : ''}" role="tab" aria-selected="${t.id === active}" data-tab="${t.id}">${esc(t.label)}</button>`).join('') : '';
    bar.querySelectorAll('[data-tab]').forEach((b) => (b.onclick = () => { active = b.dataset.tab; paintTabs(); paintPanel(); }));
    paintPanel();
  };

  const paintVisible = (force = false) => {
    const picked = selectDelayedEvent(buffer, current, statsDelay, performance.now());
    const key = picked.buffering ? 'b' : `r:${picked.receivedAt ?? 0}:${requestId}`;
    if (!force && key === paintedKey) return;
    paintedKey = key;
    statsBuffering = picked.buffering;
    display = picked.event;
    // The page is painted before the router inserts it, so detached roots are
    // updated too; otherwise the pre-delay markup would stay on screen.
    root.querySelector('.event-head').innerHTML = eventHeadMarkup(display, leagueName(display.league, state.leagues) || display.league);
    root.querySelector('.event-teams').innerHTML = eventTeamsMarkup(display);
    root.querySelector('#event-stale').innerHTML = !statsBuffering && (current.detail || {}).stale
      ? '<p class="small stale">Detailed data may be delayed; the last verified snapshot is shown.</p>' : '';
    // An event that ends while the page is open stops offering live playback.
    const watch = root.querySelector('#event-watch');
    if (watch) watch.textContent = watchActionLabel(display);
    const sync = root.querySelector('#event-sync');
    if (sync) {
      sync.textContent = `Sync stats · ${statsDelayLabel(statsDelay)}`;
      sync.dataset.delay = String(statsDelay);
      root.querySelectorAll('.sync-panel [data-delay]').forEach((b) => b.setAttribute('aria-pressed', String(Number(b.dataset.delay) === statsDelay)));
    }
    const note = root.querySelector('#event-sync-note');
    if (note) note.textContent = statsDelayNote(statsDelay, statsBuffering);
    paintTabs();
  };

  const accept = (next) => {
    current = next;
    requestId += 1;
    buffer.append(next, performance.now(), requestId);
    paintVisible();
  };

  const startStatsClock = () => {
    clearInterval(state.statsTimer);
    state.statsTimer = null;
    if (!statsDelay) return;
    state.statsTimer = setInterval(() => { if (root.isConnected) paintVisible(); }, 1000);
  };

  const syncButton = root.querySelector('#event-sync');
  const syncPanel = root.querySelector('#event-sync-panel');
  const setSyncOpen = (open) => {
    syncButton.setAttribute('aria-expanded', String(open));
    syncPanel.hidden = !open;
    if (open) syncPanel.querySelector(`[data-delay="${statsDelay}"]`)?.focus();
    else syncButton.focus();
  };
  syncButton.onclick = () => setSyncOpen(syncPanel.hidden);
  syncPanel.onkeydown = (e) => { if (e.key === 'Escape') { e.preventDefault(); setSyncOpen(false); } };
  syncPanel.querySelectorAll('[data-delay]').forEach((b) => (b.onclick = () => {
    statsDelay = writeStatsDelay(delayKey, normalizeStatsDelay(Number(b.dataset.delay)));
    setSyncOpen(false);
    paintedKey = '';
    paintVisible(true);
    startStatsClock();
  }));

  accept(event);
  startStatsClock();
  bindEventFollows(root, event);
  loadTeamChannels(root, event);

  // A refresh only touches this page; it pauses while the tab is hidden or the
  // Watch panel is finding sources, and stops when the page is left.
  const scheduleRefresh = () => {
    clearTimeout(state.detailTimer);
    state.detailTimer = setTimeout(async () => {
      if (!root.isConnected) return;
      if (document.hidden || state.watch) { scheduleRefresh(); return; }
      try {
        const next = await api(`/api/sports/events/${encodeURIComponent(id)}`);
        if (next?.id === id) accept(next);
      } catch {
        // Keep the last verified detail; the next tick tries again.
      }
      scheduleRefresh();
    }, detailRefreshMs(current));
  };
  scheduleRefresh();

  // The preview HUD repeats the delayed score, never the live one, so opening
  // or switching a source cannot reveal a score the stream has not reached.
  const hudLine = () => {
    if (statsBuffering) return statsDelayNote(statsDelay, true);
    const names = (display.participants || []).slice(0, 2)
      .map((p) => (p.score ? `${p.name} ${p.score}` : p.name)).join(' – ');
    return [names, formatDetailStatus(display)].filter(Boolean).join(' · ');
  };
  // A source switch restarts the receipt history, so delayed values can never
  // come from a different stream's timeline.
  const onSourceChange = () => {
    buffer.clear();
    requestId += 1;
    buffer.append(current, performance.now(), requestId);
    paintedKey = '';
    paintVisible(true);
  };
  // The full player names the event it belongs to, using the same delayed
  // snapshot as the page, so opening a source never reveals a live lifecycle
  // the stream has not reached.
  const playerIdentity = () => ({
    href: `/sports/event/${encodeURIComponent(id)}`,
    title: eventTitle(display),
    competition: leagueName(display.league, state.leagues) || display.league || '',
    status: formatDetailStatus(display),
  });
  const openWatch = () => {
    if (state.watch) { root.querySelector('.watch-panel')?.scrollIntoView({ block: 'nearest', behavior: 'smooth' }); return; }
    state.watch = mountSportsWatch(root.querySelector('#event-watch-slot'), display, { hudLine, onSourceChange, playerIdentity });
  };
  root.querySelector('#event-watch').onclick = openWatch;
  // A deep link from the hub may ask to start the Watch flow immediately.
  if (new URLSearchParams(location.search).get('watch') === '1') openWatch();
  return root;
}

function followControls(event, participants) {
  if (!state.prefs) {
    return `<span class="small muted">${state.prefError || 'Loading favorites…'}</span>`;
  }
  const league = isCompetitionFollowed(state.prefs, event.league);
  return `<span class="small muted">Follow</span>
    <button type="button" class="btn small${league ? ' primary' : ''}" data-follow-kind="league" aria-pressed="${league}">${league ? 'Following competition' : 'Follow competition'}</button>
    ${participants.map((p) => {
      const followed = isTeamFollowed(state.prefs, event.league, p.id);
      return `<button type="button" class="btn small${followed ? ' primary' : ''}" data-follow-team="${esc(p.id)}" aria-pressed="${followed}">${followed ? 'Following' : 'Follow'} ${esc(p.name || 'team')}</button>`;
    }).join('')}`;
}

function bindEventFollows(root, event) {
  const participants = event.participants || [];
  const refresh = () => {
    const slot = $('.event-follows', root);
    if (!slot) return;
    slot.innerHTML = followControls(event, participants);
    bindEventFollows(root, event);
  };
  $('[data-follow-kind="league"]', root)?.addEventListener('click', async () => {
    const id = matchupFollowId(event.league);
    const ok = await mutatePreferences((prefs) => {
      const next = clonePrefs(prefs);
      next.competitionIds = next.competitionIds.includes(id)
        ? next.competitionIds.filter((value) => value !== id)
        : [...next.competitionIds, id];
      return next;
    });
    if (ok) refresh();
  });
  $$('[data-follow-team]', root).forEach((button) => button.addEventListener('click', async () => {
    const id = teamFollowId(event.league, button.dataset.followTeam);
    if (!id) return;
    const ok = await mutatePreferences((prefs) => {
      const next = clonePrefs(prefs);
      next.teamIds = next.teamIds.includes(id)
        ? next.teamIds.filter((value) => value !== id)
        : [...next.teamIds, id];
      return next;
    });
    if (ok) refresh();
  }));
}

// Administrator-linked team channels appear only when they resolve to a
// channel in the current playlist. An unresolved link is reported, never
// offered as a playable guess.
async function loadTeamChannels(root, event) {
  const slot = root.querySelector('#event-channels');
  const participants = event.participants || [];
  if (!slot || !participants.length) return;
  const names = new Map(participants.map((p) => [teamFollowId(event.league, p.id), p.name]));
  const ids = [...names.keys()].filter(Boolean).join(',');
  if (!ids) return;
  try {
    const data = await api(`/api/sports/teams/linked?ids=${encodeURIComponent(ids)}`);
    const links = Object.values(data.linksByTeam || {}).flat();
    const resolved = links.filter((link) => link.resolved && link.channelId);
    const unresolved = links.filter((link) => !link.resolved);
    if (!resolved.length && !unresolved.length) return;
    const seen = new Set();
    const buttons = resolved.filter((link) => {
      if (seen.has(link.channelId)) return false;
      seen.add(link.channelId);
      return true;
    }).map((link) => `<a class="btn small primary" href="/live?channel=${encodeURIComponent(link.channelId)}">Watch ${esc(link.channelName)}</a>
      <span class="small muted">${esc(names.get(link.teamId) || link.teamId)}${link.autoLinked ? ' · linked automatically' : ' · linked by an admin'}</span>`).join('');
    slot.innerHTML = `<h2>Team channels</h2><div class="team-channels">${buttons || '<span class="muted small">No linked channel is in the current playlist.</span>'}</div>
      ${unresolved.length ? '<p class="small muted">A linked channel is not in the current playlist, so it is not offered.</p>' : ''}`;
  } catch {
    // Team links are an enhancement; a failure leaves the Watch flow untouched.
  }
}

function eventTabs(event) {
  const d = event.detail || {};
  const caps = d.capabilities || {};
  const tabs = [{ id: 'summary', label: 'Summary' }];
  if (caps.plays && (d.plays || []).length) tabs.push({ id: 'plays', label: 'Play-by-play' });
  if (caps.stats || (d.comparisons || []).length || (d.playerStats || []).length || (d.leaders || []).length) tabs.push({ id: 'stats', label: 'Stats' });
  if ((d.lineups || []).length) tabs.push({ id: 'lineups', label: 'Lineups' });
  if ((d.standings || []).length) tabs.push({ id: 'standings', label: 'Standings' });
  if ((d.footballDrives || []).length) tabs.push({ id: 'drives', label: 'Drives' });
  if ((d.cricketScorecards || []).length) tabs.push({ id: 'scorecards', label: 'Scorecards' });
  if ((d.leaderboard || []).length) tabs.push({ id: 'leaderboard', label: 'Leaderboard' });
  if (d.pregame) tabs.push({ id: 'pregame', label: 'Before the game' });
  if (d.winProbability?.length) tabs.push({ id: 'probability', label: 'Win probability' });
  if ((d.shots || []).length || (d.basketballShots || []).length) tabs.push({ id: 'shots', label: 'Shot map' });
  if (d.scoreHistory?.points?.length) tabs.push({ id: 'history', label: 'Scoring history' });
  if ((d.periods || []).some((p) => p.awayTiebreak != null || p.homeTiebreak != null)) tabs.push({ id: 'sets', label: 'Set scores' });
  if ((event.combat?.bouts || []).length) tabs.push({ id: 'card', label: 'Fight card' });
  if (String(event.sport || '').startsWith('rugby') && (d.plays || []).length) tabs.push({ id: 'timeline', label: 'Timeline' });
  return tabs;
}

function eventPanel(id, event, participants, periods) {
  const d = event.detail || {};
  if (id === 'summary') {
    const scoring = (d.plays || []).filter((p) => p.scoring).slice(0, 6);
    const sportLine = sportSummary(event, d);
    return `<div class="event-grid">
      ${sportLine}
      ${periods.length ? `<section class="event-section"><h2>Periods</h2>
        <table class="scores"><thead><tr><th></th>${periods.map((p) => `<th>${esc(p.label)}</th>`).join('')}</tr></thead>
        <tbody><tr><td>${esc(participants[0]?.abbreviation || 'Away')}</td>${periods.map((p) => `<td>${esc(p.away ?? '')}</td>`).join('')}</tr>
        <tr><td>${esc(participants[1]?.abbreviation || 'Home')}</td>${periods.map((p) => `<td>${esc(p.home ?? '')}</td>`).join('')}</tr></tbody></table>
        ${periodTies(periods)}</section>` : ''}
      ${scoring.length ? `<section class="event-section"><h2>Scoring</h2>${playList(scoring)}</section>` : ''}
      ${d.tournamentContext ? `<p class="small muted">${esc(d.tournamentContext)}</p>` : ''}
      ${d.coverageNote ? `<p class="small muted">${esc(d.coverageNote)}</p>` : ''}
    </div>`;
  }
  if (id === 'plays') return `<section class="event-section"><h2>Play-by-play <span class="small muted">${d.plays.length}</span></h2>${playList(d.plays)}</section>`;
  if (id === 'stats') {
    return `<div class="event-grid">		${(d.comparisons || []).length ? `<section class="event-section"><h2>${combatEvent(event) ? 'Fight statistics' : 'Team stats'}</h2>
        <table class="scores stats-table"><thead><tr><th></th><th>${esc(participants[0]?.abbreviation || 'Away')}</th><th>${esc(participants[1]?.abbreviation || 'Home')}</th></tr></thead>
        <tbody>${d.comparisons.map((c) => `<tr><td>${esc(c.label)}</td><td>${esc(c.away)}</td><td>${esc(c.home)}</td></tr>${combatBarRow(event, c)}`).join('')}</tbody></table></section>` : ''}
      ${(d.leaders || []).length ? `<section class="event-section"><h2>Game leaders</h2>
        <div class="leader-grid">${d.leaders.map((l) => `<div class="leader">
          ${l.headshotUrl ? `<img class="leader-photo" loading="lazy" src="/api/sports/logo?url=${encodeURIComponent(l.headshotUrl)}" alt="" onerror="this.remove()">` : ''}
          <div><div class="small muted">${esc(l.label)}${l.teamAbbreviation ? ` · ${esc(l.teamAbbreviation)}` : ''}</div>
            <div>${esc(l.name)}</div><b>${esc(l.value)}</b></div></div>`).join('')}</div></section>` : ''}
      ${playerStats(d.playerStats || [], participants)}
    </div>`;
  }
  if (id === 'lineups') {
    return `<div class="event-grid">${d.lineups.map((lineup) => {
      const team = participants.find((p) => p.id === lineup.teamId);
      return `<section class="event-section"><h2>${esc(team?.name || 'Lineup')}${lineup.formation ? ` <span class="small muted">${esc(lineup.formation)}</span>` : ''}</h2>
        <ul class="lineup">${lineup.players.map((p) => `<li><span class="num">${esc(p.number || '')}</span>
          <span class="nm">${esc(p.name)}</span><span class="small muted">${esc(p.position || '')}</span>
          ${p.subbedIn ? '<span class="badge">In</span>' : ''}${p.subbedOut ? '<span class="badge">Out</span>' : ''}</li>`).join('')}</ul></section>`;
    }).join('')}</div>`;
  }
  if (id === 'standings') {
    return `<div class="event-grid">${d.standings.map((group) => `<section class="event-section"><h2>${esc(group.title)}</h2>
      <table class="scores stats-table"><thead><tr><th>#</th><th>Team</th><th>P</th><th>Pts</th><th>GD</th><th>Record</th></tr></thead>
      <tbody>${group.rows.map((r) => `<tr class="${participants.some((p) => p.id === r.teamId) ? 'highlight' : ''}">
        <td>${esc(r.rank || '')}</td><td>${esc(r.name)}</td><td>${esc(r.played || '')}</td>
        <td>${esc(r.points || '')}</td><td>${esc(r.goalDifference || '')}</td><td>${esc(r.record || r.conferenceRecord || '')}</td></tr>`).join('')}</tbody></table></section>`).join('')}</div>`;
  }
  if (id === 'drives') {
    return `<section class="event-section drive-replay"><h2>Drive replay <span class="small muted">provider coordinates</span></h2>
      ${footballReplayPanel(event, d)}</section>
    <section class="event-section"><h2>Drives</h2><div class="drives">${d.footballDrives.map((drive) => {
      const team = participants.find((p) => p.id === drive.teamId);
      return `<details class="drive"${drive.current ? ' open' : ''}><summary>
        <span class="badge${drive.current ? ' live' : ''}">${drive.current ? 'Current' : esc(drive.period)}</span>
        <span class="nm">${esc(team?.name || '')}</span>
        <span class="small muted">${esc(drive.result || '')}${drive.playCount != null ? ` · ${drive.playCount} plays` : ''}${drive.yards != null ? ` · ${drive.yards} yds` : ''}${drive.elapsed ? ` · ${drive.elapsed}` : ''}</span>
      </summary><div class="drive-plays">${(drive.plays || []).map((play) => `<div class="drive-play">
        <span class="small muted">${esc(play.clock)}</span><span>${esc(play.description)}</span>
        ${play.scoring ? '<span class="badge">Score</span>' : ''}</div>`).join('') || '<p class="small muted">No play detail.</p>'}</div></details>`;
    }).join('')}</div></section>`;
  }
  if (id === 'scorecards') {
    return `<div class="event-grid">${d.cricketScorecards.map((card) => `<section class="event-section"><h2>${esc(card.teamName)} <span class="small muted">${esc(card.kind)} · innings ${card.innings}</span></h2>
      <table class="scores stats-table"><thead><tr><th>Player</th>${(card.players[0]?.stats || []).map((s) => `<th>${esc(s.label)}</th>`).join('')}</tr></thead>
      <tbody>${card.players.map((p) => `<tr><td>${esc(p.name)}${p.dismissal ? ` <span class="small muted">${esc(p.dismissal)}</span>` : ''}</td>${p.stats.map((s) => `<td>${esc(s.value)}</td>`).join('')}</tr>`).join('')}</tbody></table>
      ${card.extras ? `<p class="small muted">Extras ${esc(card.extras)}</p>` : ''}</section>`).join('')}</div>`;
  }
  if (id === 'leaderboard') {
    return `<section class="event-section"><h2>Leaderboard</h2><table class="scores stats-table"><thead><tr><th>#</th><th>Player</th><th>Score</th>${(d.leaderboard[0]?.rounds || []).map((r) => `<th>R${r.round}</th>`).join('')}</tr></thead>
      <tbody>${d.leaderboard.map((e, i) => `<tr><td>${i + 1}</td><td>${esc(e.name)}</td><td>${esc(e.score || '')}</td>${(e.rounds || []).map((r) => `<td>${esc(r.strokes || r.toPar || '')}</td>`).join('')}</tr>`).join('')}</tbody></table></section>`;
  }
  if (id === 'sets') return tennisSetsPanel(event, participants);
  if (id === 'card') return fightCardPanel(event, participants);
  if (id === 'timeline') return rugbyTimelinePanel(event, participants);
  if (id === 'pregame') return pregamePanel(d.pregame, participants);
  if (id === 'shots') return shotMapPanel(d, event, participants);
  if (id === 'history') return leadHistoryPanel(d.scoreHistory, participants);
  if (id === 'probability') {
    return `<section class="event-section"><h2>Win probability <span class="small muted">home</span></h2>
      <div class="probability">${d.winProbability.slice(-80).map((p) => `<div class="prob-row"><span class="small muted">${esc(p.periodLabel)} ${esc(p.clock)}</span>
        <span class="prob-bar"><i style="width:${Math.round(p.home * 100)}%"></i></span><span class="small">${Math.round(p.home * 100)}%</span></div>`).join('')}</div></section>`;
  }
  return '';
}

// A replay trace is drawn only when the provider supplied both endpoints and
// the play advances the ball. Possession decides the goal; absent possession
// yields no direction and no invented first down.
function footballReplayPanel(event, detail) {
  const drives = detail.footballDrives || [];
  const plays = drives.flatMap((drive) => (drive.plays || []).map((play) => ({ drive, play })));
  if (!plays.length) return '<p class="muted small">No play coordinates have been published.</p>';
  const away = event.participants?.[0]?.id || '';
  const home = event.participants?.[1]?.id || '';
  const rows = plays.slice(-12).reverse();
  return `<div class="replay-grid">${rows.map(({ drive, play }) => {
    const path = footballReplayPath(play);
    const situation = footballReplaySituation(play, drive.teamId, away, home);
    const previous = previousPlayTrail(detail, play.id);
    const label = footballPlayResult(play);
    // Possession is the provider's own statement; a play without it shows no
    // team and no direction rather than borrowing the previous drive's.
    const possession = situation.side === 'away'
      ? event.participants?.[0]?.name || ''
      : situation.side === 'home'
        ? event.participants?.[1]?.name || ''
        : '';
    const meta = [possession ? `${possession} ball` : '', situation.downLabel, situation.position]
      .filter(Boolean).join(' · ');
    return `<div class="replay-row">
      <div class="replay-head"><span class="badge">${esc(play.period || '')}</span>
        <span class="small muted">${esc(play.clock || '')}</span>
        <span class="small">${esc(label)}</span></div>
      <div class="small muted">${esc(play.description || play.type || '')}</div>
      ${meta ? `<div class="small muted">${esc(meta)}</div>` : ''}
      ${path ? `<svg class="replay-field" viewBox="0 0 840 220" role="img" aria-label="${esc(`${play.type || 'Play'} from ${situation.position || 'an unknown spot'}, ${label}`)}">
        <rect class="field-turf" x="0" y="0" width="840" height="220" rx="10"/>
        <line class="field-mid" x1="420" y1="12" x2="420" y2="208"/>
        <line class="field-goal" x1="110" y1="12" x2="110" y2="208"/>
        <line class="field-goal" x1="730" y1="12" x2="730" y2="208"/>
        ${situation.firstDown !== undefined ? `<line class="field-first" x1="${110 + (620 * situation.firstDown) / 100}" y1="12"
          x2="${110 + (620 * situation.firstDown) / 100}" y2="208"/>` : ''}
        <path class="replay-trace ${path.kind}" d="M${path.start.x},${path.start.y} Q${path.control.x},${path.control.y} ${path.end.x},${path.end.y}"/>
        <circle class="replay-start" cx="${path.start.x}" cy="${path.start.y}" r="6"/>
        <circle class="replay-end" cx="${path.end.x}" cy="${path.end.y}" r="7"/>
      </svg>` : '<p class="small muted">No in-field trace for this play.</p>'}
      ${previous ? `<div class="small muted">Previous eligible play: ${esc(previous.play.description || previous.play.type || '')}</div>` : ''}
    </div>`;
  }).join('')}</div>`;
}

function playList(plays) {
  if (!plays?.length) return '<p class="muted small">No plays yet.</p>';
  return `<ol class="plays">${plays.map((p) => `<li class="${p.scoring ? 'scoring' : ''}">
    <div class="play-line"><span class="badge">${esc(p.periodLabel || '')}</span>${p.clock ? `<span class="small muted">${esc(p.clock)}</span>` : ''}
      ${p.title ? `<span class="small muted">${esc(p.title)}</span>` : ''}</div>
    <div>${esc(p.description || '')}</div>
    ${p.participants?.length ? `<div class="small muted">${p.participants.map((x) => esc(x.name)).join(', ')}</div>` : ''}</li>`).join('')}</ol>`;
}

function playerStats(rows, participants) {
  if (!rows.length) return '';
  const byTeam = new Map();
  for (const row of rows) {
    if (!byTeam.has(row.teamId)) byTeam.set(row.teamId, []);
    byTeam.get(row.teamId).push(row);
  }
  return [...byTeam.entries()].map(([teamId, players]) => {
    const team = participants.find((p) => p.id === teamId);
    return `<section class="event-section"><h2>${esc(team?.name || 'Team')} player stats</h2>
      <table class="scores stats-table"><thead><tr><th>Player</th><th>Cat</th>${(players[0]?.stats || []).map((s) => `<th>${esc(s.label)}</th>`).join('')}</tr></thead>
      <tbody>${players.map((p) => `<tr><td>${esc(p.jersey ? `#${p.jersey} ` : '')}${esc(p.name)}</td><td class="muted">${esc(p.category)}</td>
        ${p.stats.map((s) => `<td>${esc(s.value)}</td>`).join('')}</tr>`).join('')}</tbody></table></section>`;
  }).join('');
}

function pregamePanel(pregame, participants) {
  if (!pregame) return '';
  return `<div class="event-grid">
    ${pregame.seasonLabel ? `<p class="small muted">${esc(pregame.seasonLabel)}</p>` : ''}
    ${(pregame.comparisons || []).length ? `<section class="event-section"><h2>Season comparison</h2>
      <table class="scores stats-table"><thead><tr><th></th><th>${esc(participants[0]?.abbreviation || 'Away')}</th><th>${esc(participants[1]?.abbreviation || 'Home')}</th></tr></thead>
      <tbody>${pregame.comparisons.map((c) => `<tr><td>${esc(c.label)}</td><td>${esc(c.away)}</td><td>${esc(c.home)}</td></tr>`).join('')}</tbody></table></section>` : ''}
    ${(pregame.teams || []).map((t) => {
      const team = participants.find((p) => p.id === t.teamId);
      return `<section class="event-section"><h2>${esc(team?.name || 'Team')}${t.rank ? ` <span class="badge">#${t.rank}</span>` : ''}</h2>
        <div class="small muted">${[t.record && `Overall ${t.record}`, t.homeRecord && `Home ${t.homeRecord}`, t.awayRecord && `Away ${t.awayRecord}`, t.conferenceRecord && `Conf ${t.conferenceRecord}`].filter(Boolean).map(esc).join(' · ')}</div>
        ${t.standingLabel ? `<div class="small muted">${esc(t.standingLabel)}</div>` : ''}
        ${(t.probables || []).length ? `<p class="small">${t.probables.map((p) => `${esc(p.label)}: ${esc(p.name)}`).join(' · ')}</p>` : ''}
        ${(t.leaders || []).length ? `<ul class="leaders">${t.leaders.map((l) => `<li>${esc(l.label)}: <b>${esc(l.name)}</b> <span class="muted">${esc(l.value)}</span></li>`).join('')}</ul>` : ''}
        ${(t.recent || []).length ? `<div class="recent">${t.recent.map((r) => `<span class="recent-item"><b>${esc(r.result)}</b> ${esc(r.opponent)} <span class="small muted">${esc(r.score || '')}</span></span>`).join('')}</div>` : ''}
      </section>`;
    }).join('')}
    ${pregame.previousMeeting ? `<section class="event-section"><h2>Previous meeting</h2><p>${esc(new Date(pregame.previousMeeting.date).toLocaleDateString())} ·
      ${esc(pregame.previousMeeting.awayScore)}–${esc(pregame.previousMeeting.homeScore)}</p></section>` : ''}
  </div>`;
}

function baseballSituation(situation) {
  if (!situation || situation.kind !== 'mlb') return '';
  const bits = [situation.inning, situation.bases, situation.batter ? `At bat: ${situation.batter}` : '',
    situation.pitcher ? `Pitching: ${situation.pitcher}` : '',
    [situation.balls != null ? `${situation.balls} balls` : '', situation.strikes != null ? `${situation.strikes} strikes` : '', situation.outs != null ? `${situation.outs} outs` : ''].filter(Boolean).join(' · ')]
    .filter(Boolean);
  if (!bits.length) return '';
  const on = (base) => new RegExp(base).test(situation.bases || '');
  const base = (x, y, label) => `<rect class="base${on(label) ? ' on' : ''}" x="${x}" y="${y}" width="13" height="13" transform="rotate(45 ${x + 6.5} ${y + 6.5})"><title>${label}</title></rect>`;
  return `<div class="baseball-state">
    <svg class="diamond" viewBox="0 0 80 76" role="img" aria-label="Base runners: ${esc(situation.bases || 'bases empty')}">
      <path class="diamond-line" d="M40 8 L72 40 L40 72 L8 40 Z"/>
      ${base(33.5, 1.5, '2nd')}${base(57.5, 26, '1st')}${base(9.5, 26, '3rd')}
      <rect class="base home" x="34" y="60" width="12" height="12" transform="rotate(45 40 66)"/>
    </svg>
    ${bits.map((b) => `<span>${esc(b)}</span>`).join('')}</div>`;
}

// Team colours come from the provider and are used for charts only; missing
// colours fall back to the theme instead of inventing a brand colour.
function teamColor(event, teamId, fallback) {
  const team = [event?.homeTeam, event?.awayTeam].find((t) => t?.id === teamId);
  const color = String(team?.color || '').replace(/^#/, '');
  return /^[0-9a-f]{6}$/i.test(color) ? `#${color}` : fallback;
}

function shotMapPanel(detail, event, participants) {
  const hockey = detail.shots || [];
  const basketball = detail.basketballShots || [];
  const away = teamColor(event, participants[0]?.id, '#7aa2f7');
  const home = teamColor(event, participants[1]?.id, '#e9b45c');
  const color = (teamId) => (teamId === participants[0]?.id ? away : teamId === participants[1]?.id ? home : 'var(--muted)');
  const legend = `<p class="small muted">${participants.map((p, i) => `<span class="shot-key" style="--c:${i === 0 ? away : home}">${esc(p.abbreviation || p.name)}</span>`).join(' ')} · dashed marks are misses. Provider coordinates; direction is not inferred.</p>`;
  return `<div class="event-grid">
    ${hockey.length ? `<section class="event-section"><h2>Hockey shots <span class="small muted">${hockey.length}</span></h2>
      <svg class="shot-map" viewBox="-106 -48 212 96" role="img" aria-label="Hockey shot map">
        <rect class="rink-line" x="-100" y="-42.5" width="200" height="85" rx="28"/>
        <line class="rink-line" x1="0" y1="-42.5" x2="0" y2="42.5"/>
        <circle class="rink-line" cx="0" cy="0" r="15"/>
        <circle class="rink-line" cx="-69" cy="0" r="15"/><circle class="rink-line" cx="69" cy="0" r="15"/>
        <line class="rink-line" x1="-89" y1="-4" x2="-89" y2="4"/><line class="rink-line" x1="89" y1="-4" x2="89" y2="4"/>
        ${hockey.map((s) => `<circle class="shot ${esc(s.kind)}" cx="${Number(s.x)}" cy="${Number(s.y)}" r="${s.kind === 'goal' ? 2.4 : 1.7}" fill="${color(s.teamId)}"><title>${esc([s.period, s.clock, s.kind, s.description].filter(Boolean).join(' · '))}</title></circle>`).join('')}
      </svg>${legend}</section>` : ''}
    ${basketball.length ? `<section class="event-section"><h2>Basketball shots <span class="small muted">${basketball.length}</span></h2>
      <svg class="shot-map court" viewBox="-1 -7 52 97" role="img" aria-label="Basketball shot map">
        <rect class="rink-line" x="0" y="-6" width="50" height="94"/>
        <rect class="rink-line" x="17" y="0" width="16" height="19"/>
        <circle class="rink-line" cx="25" cy="19" r="6"/>
        <path class="rink-line" d="M1.25 0 A23.75 23.75 0 0 1 48.75 0"/>
        <line class="rink-line" x1="1.25" y1="-6" x2="1.25" y2="14"/><line class="rink-line" x1="48.75" y1="-6" x2="48.75" y2="14"/>
        <circle class="rim" cx="25" cy="0" r="0.75"/>
        ${basketball.map((s) => `<circle class="shot ${s.made ? 'goal' : 'missed'}" cx="${Number(s.x)}" cy="${Number(s.y)}" r="1.1" fill="${color(s.teamId)}"><title>${esc([s.periodLabel, s.clock, `${s.points}pt ${s.made ? 'made' : 'missed'}`, s.playerName || s.description].filter(Boolean).join(' · '))}</title></circle>`).join('')}
      </svg>${legend}</section>` : ''}
  </div>`;
}

// Scoring history is the provider's validated point sequence; the chart plots
// the home margin and never fills missing points.
function leadHistoryPanel(history, participants) {
  const points = history?.points || [];
  if (!points.length) return '';
  const width = 620, height = 150, pad = 12;
  const spread = Math.max(1, ...points.map((p) => Math.abs(p.home - p.away)));
  const step = points.length > 1 ? (width - pad * 2) / (points.length - 1) : 0;
  const y = (value) => height / 2 - (value / spread) * (height / 2 - pad);
  const path = points.map((p, i) => `${i ? 'L' : 'M'}${(pad + i * step).toFixed(1)},${y(p.home - p.away).toFixed(1)}`).join(' ');
  return `<div class="event-grid">
    <section class="event-section"><h2>Scoring history <span class="small muted">home margin</span></h2>
      <svg class="lead-chart" viewBox="0 0 ${width} ${height}" role="img" aria-label="Scoring margin over the game">
        <line class="chart-zero" x1="${pad}" y1="${height / 2}" x2="${width - pad}" y2="${height / 2}"/>
        <path class="chart-line" d="${path}"/>
      </svg>
      <p class="small muted">${esc(participants[1]?.abbreviation || 'Home')} above the line, ${esc(participants[0]?.abbreviation || 'Away')} below.${history.complete ? '' : ' This sequence is incomplete.'}</p>
    </section>
    <section class="event-section"><h2>Scoring sequence <span class="small muted">${points.length}</span></h2>
      <ol class="plays">${points.map((p) => `<li>
        <div class="play-line"><span class="badge">${esc(p.periodLabel || '')}</span>${p.clock ? `<span class="small muted">${esc(p.clock)}</span>` : ''}</div>
        <div>${esc(participants[0]?.abbreviation || 'Away')} ${esc(p.away)} – ${esc(p.home)} ${esc(participants[1]?.abbreviation || 'Home')}</div></li>`).join('')}</ol>
    </section>
  </div>`;
}

// Sport-specific context belongs on the summary, not in an invented stat.
function sportSummary(event, detail) {
  const sport = String(event.sport || '');
  if (['atp', 'wta'].includes(event.league)) {
    const context = tennisEventContext(event);
    return context ? `<p class="small muted">${esc(context)}</p>` : '';
  }
  if (combatEvent(event)) {
    const result = combatResult(event, false);
    const card = event.combat?.cardName || event.combat?.division;
    const rounds = event.combat?.scheduledRounds;
    const tape = combatTape(event);
    const fighters = (event.participants || []).slice(0, 2);
    return `<section class="event-section"><h2>${fighters.length === 2 ? 'Fighter matchup' : 'Fight information'}</h2>
      ${card ? `<p class="small muted">${esc(card)}</p>` : ''}
      ${fighters.length === 2 ? `<div class="combat-pair">${fighters.map((p) => `<div class="combat-fighter${p.winner ? ' win' : ''}">
        ${combatMark(event, p)}<span class="nm">${esc(p.name)}</span>
        ${p.record ? `<span class="small muted">Current record · ${esc(p.record)}</span>` : ''}
        ${p.winner ? '<span class="badge">Winner</span>' : ''}</div>`).join('<span class="small muted vs">VS</span>')}</div>` : ''}
      ${rounds === 3 || rounds === 5 ? `<p class="small muted">${rounds} scheduled rounds</p>` : ''}
      <p>${esc(result)}</p>
      ${tape.length ? `<table class="scores tape"><thead><tr><th>${esc(fighters[0]?.name || 'Fighter')}</th><th></th><th>${esc(fighters[1]?.name || 'Fighter')}</th></tr></thead>
        <tbody>${tape.map((row) => `<tr><td>${esc(row.away || '—')}</td><td class="muted">${esc(row.label)}</td><td>${esc(row.home || '—')}</td></tr>`).join('')}</tbody></table>` : ''}
    </section>`;
  }
  if (sport.startsWith('rugby')) {
    return `<p class="small muted">${esc(rugbyStatus(event))}</p>`;
  }
  return '';
}

function combatEvent(event) {
  const sport = String(event?.sport || '');
  return event?.league === 'ufc' || event?.league === 'boxing' || sport === 'mma' || sport === 'boxing';
}

// A fighter is not a team: the crest placeholder is replaced by a portrait or
// an empty mark, and a boxing id never addresses ESPN's MMA headshot path.
function combatMark(event, participant) {
  const url = combatPortrait(event, participant);
  return `<span class="team-mark combat">${url ? `<img loading="lazy" src="${esc(logoURL(url))}" alt="" onerror="this.remove()">` : ''}</span>`;
}

// Bars compare only numeric totals; the numbers stay visible next to them.
function combatBarRow(event, statistic) {
  if (!combatEvent(event)) return '';
  const values = combatBarValues(statistic.label, statistic.away, statistic.home);
  if (!values) return '';
  const max = Math.max(...values, 1);
  return `<tr class="combat-bars" aria-hidden="true"><td colspan="3"><span class="combat-bar away"><i style="width:${Math.round((values[0] / max) * 100)}%"></i></span>
    <span class="combat-bar home"><i style="width:${Math.round((values[1] / max) * 100)}%"></i></span></td></tr>`;
}

// Set scores use the provider's set periods. A tiebreak is shown as a
// superscript and never recalculated from the set score.
function tennisSetsPanel(event, participants) {
  const sets = tennisSets(event);
  if (!sets.length) return '<p class="muted small">No set scores have been published.</p>';
  const name = (index) => participants[index]?.name || (index === 0 ? 'Away' : 'Home');
  const cell = (value, tiebreak, won) => `<td class="${won ? 'highlight' : ''}">${esc(value ?? '')}${tiebreak ? `<sup class="tiebreak">${esc(tiebreak)}</sup>` : ''}</td>`;
  return `<section class="event-section"><h2>Set scores</h2>
    <table class="scores stats-table sets-table"><thead><tr><th></th>${sets.map((set) => `<th>${esc(set.label)}</th>`).join('')}</tr></thead>
    <tbody>
      <tr><td>${esc(name(0))}</td>${sets.map((set) => cell(set.away, set.awayTiebreakLabel, set.awayWinner)).join('')}</tr>
      <tr><td>${esc(name(1))}</td>${sets.map((set) => cell(set.home, set.homeTiebreakLabel, set.homeWinner)).join('')}</tr>
    </tbody></table>
    <p class="small muted">Superscript numbers are tiebreak points reported by the provider.</p></section>`;
}

// Bout order is the provider's card order; no main/prelim inference.
function fightCardPanel(event, participants) {
  const bouts = combatBouts(event);
  if (!bouts.length) return '<p class="muted small">No bout details have been published.</p>';
  const nameOf = (team) => participants.find((p) => p.id === team?.id)?.name || team?.name || 'TBD';
  return `<section class="event-section"><h2>Fight card <span class="small muted">${bouts.length}</span></h2>
    <ol class="bouts">${bouts.map((bout) => `<li class="bout ${esc(bout.status || 'scheduled')}">
      <div class="play-line"><span class="badge">${esc(bout.status === 'live' ? 'Live' : bout.status === 'final' ? 'Final' : 'Scheduled')}</span>
        ${bout.division ? `<span class="small muted">${esc(bout.division)}</span>` : ''}
        ${bout.period ? `<span class="small muted">${esc(bout.period)}${bout.clock ? ` · ${esc(bout.clock)}` : ''}</span>` : ''}</div>
      <div><b>${esc(nameOf(bout.away))}</b> vs <b>${esc(nameOf(bout.home))}</b></div>
    </li>`).join('')}</ol></section>`;
}

// Only provider-labelled events receive a symbol; the timeline never invents
// points or an active sin-bin timer.
function rugbyTimelinePanel(event, participants) {
  const plays = rugbyLatestPlays((event.detail || {}).plays || []);
  if (!plays.length) return '<p class="muted small">No play-by-play has been published.</p>';
  return `<section class="event-section"><h2>Timeline <span class="small muted">${esc(rugbyStatus(event))}</span></h2>
    <ol class="timeline">${plays.slice(0, 120).map((play) => `<li class="timeline-row">
      <span class="symbol" aria-hidden="true">${esc(rugbyPlaySymbol(play.title))}</span>
      <span class="small muted clock">${esc(play.clock || '')}</span>
      <span class="small muted">${esc(play.periodLabel || '')}</span>
      <span>${esc(play.description || play.title || '')}</span>
      ${play.scoring ? '<span class="badge">Score</span>' : ''}</li>`).join('')}</ol></section>`;
}

function periodTies(periods) {
  const notes = [];
  for (const p of periods) {
    if (p.awayTiebreak != null || p.homeTiebreak != null) {
      notes.push(`${esc(p.label)} tiebreak ${p.awayTiebreak ?? ''}–${p.homeTiebreak ?? ''}`);
    }
  }
  return notes.length ? `<p class="small muted">${notes.join(' · ')}</p>` : '';
}
