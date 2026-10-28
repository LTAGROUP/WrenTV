// Browsing beyond libraries: genres, collections, and the viewer's own settings.
import { api, img } from './api.js';
import * as ambient from './ambient.js';
import { esc, $, $$, el, toast, modal, confirmDialog, icons } from './ui.js';
import { go, render, setTitle, isAdmin, getLibraries, posterCard, titleCard, lazyImg, libraryTabs, bd, shelf, bindShelves, posterSize, setPosterSize, discoverHero, bindHero } from './app.js';

const qs = () => new URLSearchParams(location.search);

// ---------------------------------------------------------------- genres

export async function viewGenres(libId) {
  const lib = libId && getLibraries().find((l) => l.id === libId);
  setTitle(lib ? lib.name : 'Genres');
  const genres = await api('/api/genres' + (libId ? `?libraryId=${encodeURIComponent(libId)}` : ''));
  const link = (g) => `/genre?g=${encodeURIComponent(g.genre)}${libId ? `&lib=${libId}` : ''}`;
  const sub = (g) => libId ? `${g.count} ${lib?.type === 'shows' ? 'shows' : 'movies'}`
    : [g.movies && `${g.movies} movie${g.movies > 1 ? 's' : ''}`, g.shows && `${g.shows} show${g.shows > 1 ? 's' : ''}`].filter(Boolean).join(' · ');
  return `<div>${libId ? libraryTabs(libId, 'genres') : '<div class="page-head"><h1>Genres</h1></div>'}
    ${genres.length ? `<div class="genre-grid">${genres.map((g) => `<a class="genre-tile" href="${link(g)}">
      ${g.backdropPath ? lazyImg(img(g.backdropPath, 'w500')) : ''}<div><b>${esc(g.genre)}</b><span>${esc(sub(g))}</span></div></a>`).join('')}</div>`
      : '<div class="empty"><h2>No genres yet</h2><p>Genres come from TMDB as titles are matched.</p></div>'}</div>`;
}

const genreSorts = [['rating', 'Rating'], ['title', 'Title'], ['added', 'Recently added'], ['year', 'Newest'], ['year_asc', 'Oldest']];

export async function viewGenre() {
  const q = qs();
  const g = q.get('g') || '';
  const lib = q.get('lib') || '';
  const kind = q.get('kind') || '';
  const sort = q.get('sort') || 'rating';
  const unwatched = q.get('unwatched') === '1';
  setTitle(g);
  const p = new URLSearchParams({ genre: g, sort });
  if (lib) p.set('libraryId', lib);
  if (kind) p.set('kind', kind);
  if (unwatched) p.set('unwatched', '1');
  const res = await api('/api/genres/items?' + p);
  const libs = getLibraries();
  const node = el(`<div><div class="page-head"><h1>${esc(g)} <span class="muted small">${res.total} titles</span></h1>
    <div class="toolbar"><button class="chip${unwatched ? ' on' : ''}" id="unwatched" aria-pressed="${unwatched}">Unwatched</button>
    ${libs.length > 1 ? `<select id="lib" data-caption="In"><option value="">All libraries</option>${libs.map((l) => `<option value="${l.id}" ${l.id === lib ? 'selected' : ''}>${esc(l.name)}</option>`).join('')}</select>` : ''}
    ${lib ? '' : `<select id="kind" data-caption="Show"><option value="">Movies & shows</option><option value="movie" ${kind === 'movie' ? 'selected' : ''}>Movies</option><option value="show" ${kind === 'show' ? 'selected' : ''}>Shows</option></select>`}
    <select id="sort" data-caption="Sort">${genreSorts.map(([k, v]) => `<option value="${k}" ${k === sort ? 'selected' : ''}>${v}</option>`).join('')}</select></div></div>
    ${res.items.length ? `<div class="grid">${res.items.map((it) => posterCard(it)).join('')}</div>` : '<div class="empty"><h2>Nothing here</h2></div>'}</div>`);
  const nav = (patch) => {
    const n = qs();
    for (const [k, v] of Object.entries(patch)) v ? n.set(k, v) : n.delete(k);
    go('/genre?' + n, true);
  };
  bindShelves(node);
  $('#sort', node).onchange = (e) => nav({ sort: e.target.value });
  $('#lib', node)?.addEventListener('change', (e) => nav({ lib: e.target.value, kind: '' }));
  $('#kind', node)?.addEventListener('change', (e) => nav({ kind: e.target.value }));
  $('#unwatched', node).onclick = () => nav({ unwatched: unwatched ? '' : '1' });
  return node;
}

// ---------------------------------------------------------------- collections

const kindLabel = (c) => c.kind === 'manual' ? 'Hand-picked' : c.kind === 'smart' ? 'Smart' : sourceLabel(c.rules);

function sourceLabel(r) {
  const mt = r.mediaType === 'show' ? 'shows' : 'movies';
  return { collection: 'TMDB franchise', list: 'TMDB list', trending: `Trending ${mt}`, popular: `Popular ${mt}`, top_rated: `Top rated ${mt}`,
    now_playing: 'In theaters', upcoming: 'Coming soon', airing: 'On the air', mdblist: 'MDBList', simkl: 'Simkl', trakt: 'Trakt' }[r.source] || 'TMDB';
}

// Simkl charts and watchlists a collection can follow.
const simklRefs = [['trending/movies/today', 'Trending movies today'], ['trending/movies/week', 'Trending movies this week'],
  ['trending/movies/month', 'Trending movies this month'], ['trending/tv/today', 'Trending shows today'], ['trending/tv/week', 'Trending shows this week'],
  ['trending/tv/month', 'Trending shows this month'], ['trending/anime/week', 'Trending anime this week'], ['trending/anime/month', 'Trending anime this month'],
  ['watchlist/plantowatch', 'My Simkl: Plan to watch'], ['watchlist/watching', 'My Simkl: Watching'], ['watchlist/completed', 'My Simkl: Completed']];

// Trakt charts and the watchlist a collection can follow.
const traktRefs = [['trending/movies', 'Trending movies'], ['trending/shows', 'Trending shows'], ['popular/movies', 'Popular movies'],
  ['popular/shows', 'Popular shows'], ['anticipated/movies', 'Most anticipated movies'], ['anticipated/shows', 'Most anticipated shows'],
  ['watchlist', 'My Trakt watchlist']];

const fmtMD = (md) => {
  if (!md) return '';
  const [m, d] = md.split('-').map(Number);
  return new Date(2000, m - 1, d).toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
};

// groupShelves is a shelf per kind of collection, in the server's order:
// its own, franchises, lists, charts, then smart collections.
function groupShelves(cs) {
  const groups = new Map();
  for (const c of cs) {
    const g = c.group || 'Collections';
    groups.set(g, [...(groups.get(g) || []), c]);
  }
  return [...groups].map(([g, list]) => shelf(g, list.map(collectionTile).join(''), { sub: `${list.length} collection${list.length === 1 ? '' : 's'}` })).join('');
}

// collectionTile is a collection's wide tile: its artwork with the name over it.
export function collectionTile(c) {
  const tags = [];
  if (!c.active) tags.push(`Hidden until ${fmtMD(c.visibleFrom)}`);
  else if (c.showOnHome && isAdmin()) tags.push('On Home');
  else if (c.showInLibrary && isAdmin()) tags.push('Library row');
  const pic = c.backdropPath || c.posterPath;
  return `<a class="card wide coll-tile" href="/collection/${c.id}"${bd(c.backdropPath)}><div class="thumb">${pic ? lazyImg(img(pic, 'w780'), c.name) : '<div class="ph"></div>'}
    ${tags.length ? `<span class="flag">${esc(tags[0])}</span>` : ''}<b>${esc(c.name)}</b></div>
    <div class="s" style="margin-top:7px">${c.itemCount} title${c.itemCount === 1 ? '' : 's'} · ${esc(kindLabel(c))}</div></a>`;
}

export async function viewCollections(libId) {
  const lib = libId && getLibraries().find((l) => l.id === libId);
  setTitle(lib ? lib.name : 'Collections');
  const cs = await api('/api/collections' + (libId ? `?libraryId=${encodeURIComponent(libId)}` : ''));
  const admin = isAdmin();
  const node = el(`<div>${libId ? libraryTabs(libId, 'collections') : ''}
    <div class="page-head"><h1>${libId ? 'Collections' : 'Collections'} <span class="muted small">${cs.length}</span></h1>
    ${admin ? `<div class="toolbar"><button class="btn" id="auto" title="Create a collection for every movie series with two or more films in your library">Auto-create franchises</button>
      <button class="btn primary" id="new">${icons.plus} New collection</button></div>` : ''}</div>
    ${cs.length ? groupShelves(cs)
      : `<div class="empty"><h2>No collections yet</h2><p>${admin ? 'Group titles by hand, with rules (smart collections), or from TMDB franchises, lists and charts.' : 'The server owner hasn\'t made any collections yet.'}</p></div>`}</div>`);
  bindShelves(node);
  $('#new', node)?.addEventListener('click', () => editCollection(null, { libraryId: libId }));
  $('#auto', node)?.addEventListener('click', async (e) => {
    e.currentTarget.disabled = true;
    e.currentTarget.textContent = 'Finding franchises…';
    try {
      const r = await api('/api/collections/auto', { method: 'POST' });
      toast(r.created ? `Created ${r.created} franchise collection${r.created > 1 ? 's' : ''}.` : 'No new franchises with two or more movies found.');
      render();
    } catch (err) { toast(err.message); render(); }
  });
  return node;
}

const collSorts = [['', 'Default order'], ['title', 'Title'], ['added', 'Recently added'], ['year', 'Newest'], ['year_asc', 'Oldest'], ['rating', 'Rating']];

// A collection is one shelf in its order, loaded a page at a time as it
// scrolls toward the end.
const collPage = 40;

export async function viewCollection(id) {
  const sort = qs().get('sort') || '';
  const page = (offset) => api(`/api/collections/${id}?limit=${collPage}&offset=${offset}` + (sort ? `&sort=${sort}` : ''));
  const d = await page(0);
  const c = d.collection;
  setTitle(c.name);
  ambient.base(c.backdropPath);
  const admin = isAdmin();
  const meta = [d.countLabel, kindLabel(c)].filter(Boolean);
  if (c.visibleFrom) meta.push(`Visible ${fmtMD(c.visibleFrom)} – ${fmtMD(c.visibleUntil)}`);
  if (admin && c.showOnHome) meta.push('Shown on Home');
  const removable = admin && c.kind === 'manual';
  const card = (v) => removable && v.item
    ? posterCard(v.item, `<button class="rm" data-rm="${v.item.id}" title="Remove from collection" aria-label="Remove ${esc(v.title)} from collection">×</button>`)
    : titleCard(v);
  const entries = d.entries || [];
  const hero = entries.filter((v) => v.backdropPath).slice(0, 10);
  const node = el(`<div>${hero.length ? discoverHero(hero, c.name) : ''}<div class="hero coll-hero">
    <div class="content">${c.posterPath ? `<div class="poster"><img src="${img(c.posterPath, 'w500')}" alt=""></div>` : ''}
    <div><div class="small" style="margin-bottom:6px"><a href="/collections">Collections</a></div><h1>${esc(c.name)}</h1>
    <div class="meta">${meta.map(esc).join('<span class="muted">·</span>')}</div>
    ${c.syncError && admin ? `<p class="error small">Last sync failed: ${esc(c.syncError)}</p>` : ''}
    ${c.description ? `<p class="overview">${esc(c.description)}</p>` : ''}
    <div class="actions"><select id="sort" data-caption="Sort">${collSorts.map(([k, v]) => `<option value="${k}" ${k === sort ? 'selected' : ''}>${v}</option>`).join('')}</select>
    ${admin ? `<button class="btn" data-act="edit">Edit</button>${c.kind === 'tmdb' ? '<button class="btn ghost" data-act="sync">Sync now</button>' : ''}
      <button class="btn ghost danger" data-act="delete">Delete</button>` : ''}</div></div></div></div>
    ${entries.length ? shelf(c.name, entries.map(card).join(''), { sub: `${d.total}`, id: 'coll-shelf' })
      : `<div class="empty"><h2>${c.kind === 'tmdb' ? 'This collection is empty' : 'Nothing from your library yet'}</h2><p>${c.kind === 'manual' ? 'Add titles from their detail page with “+ Collection”.' : 'Titles appear as soon as the library has them.'}</p></div>`}</div>`);
  if (hero.length) bindHero(node, hero, c.name);
  bindShelves(node);
  $('#sort', node).onchange = (e) => go(`/collection/${id}${e.target.value ? '?sort=' + e.target.value : ''}`, true);
  const act = (n, fn) => $(`[data-act=${n}]`, node)?.addEventListener('click', fn);
  act('edit', () => editCollection(c));
  act('sync', async () => {
    try { await api(`/api/collections/${id}/sync`, { method: 'POST' }); toast('Collection synced.'); render(); } catch (e) { toast(e.message); }
  });
  act('delete', async () => {
    if (!(await confirmDialog(`Delete “${c.name}”?`, 'The titles stay in your library; only the collection is removed.', 'Delete'))) return;
    await api(`/api/collections/${id}`, { method: 'DELETE' });
    go('/collections');
  });
  const bindRemove = (root) => {
    for (const b of $$('[data-rm]', root)) {
      b.onclick = async (e) => {
        e.preventDefault();
        e.stopPropagation();
        await api(`/api/collections/${id}/items/${b.dataset.rm}`, { method: 'DELETE' });
        b.closest('.card').remove();
      };
    }
  };
  bindRemove(node);
  const row = $('#coll-shelf .row', node);
  if (!row) return node;
  // Fetch the next page once the view is within about 15 cards of the end,
  // one fetch at a time; a failed fetch leaves a card to retry it.
  let loaded = entries.length, busy = false;
  const more = async () => {
    if (busy || loaded >= d.total) return;
    busy = true;
    $('.retry', row)?.remove();
    try {
      const p = await page(loaded);
      const add = p.entries || [];
      if (!add.length) { loaded = d.total; return; }
      loaded += add.length;
      const frag = el(`<div>${add.map(card).join('')}</div>`);
      bindRemove(frag);
      row.append(...frag.children);
    } catch {
      const r = el('<button class="card retry" type="button"><div class="poster"><span>Couldn’t load more.<br>Retry</span></div></button>');
      r.onclick = () => more();
      row.append(r);
    } finally {
      busy = false;
    }
    near();
  };
  const near = () => {
    const w = row.firstElementChild?.offsetWidth || 160;
    if (row.scrollLeft + row.clientWidth >= row.scrollWidth - w * 15) more();
  };
  row.addEventListener('scroll', near, { passive: true });
  row.addEventListener('focusin', (e) => {
    const cards = [...row.children];
    if (cards.indexOf(e.target.closest('.card')) >= cards.length - 15) more();
  });
  requestAnimationFrame(near);
  return node;
}

// ---------------------------------------------------------------- calendar

const day = (d) => {
  const [y, m, dd] = d.split('-').map(Number);
  return new Date(y, m - 1, dd);
};
const isoDay = (t) => `${t.getFullYear()}-${String(t.getMonth() + 1).padStart(2, '0')}-${String(t.getDate()).padStart(2, '0')}`;

// dayName is "Today", "Tomorrow", "Yesterday", or the weekday and date.
function dayName(d) {
  const t = new Date();
  const off = Math.round((day(d) - day(isoDay(t))) / 864e5);
  if (off === 0) return 'Today';
  if (off === 1) return 'Tomorrow';
  if (off === -1) return 'Yesterday';
  return day(d).toLocaleDateString(undefined, { weekday: 'long', month: 'short', day: 'numeric' });
}

function calendarCard(e) {
  const pic = e.stillPath || e.show.backdropPath;
  const flag = e.upNext ? '<span class="flag next">Up next</span>' : e.watched ? '<span class="flag">Watched</span>' : '';
  const code = `S${e.seasonNumber} · E${e.episodeNumber}`;
  return `<a class="card wide" href="/item/${e.itemId || e.show.id}"${bd(e.show.backdropPath)} title="${esc(e.overview || '')}"><div class="thumb">${pic ? lazyImg(img(pic, 'w500')) : ''}${flag}</div>
    <div class="t">${esc(e.show.title)}</div><div class="s">${esc(`${code} — ${e.title || `Episode ${e.episodeNumber}`}`)}</div></a>`;
}

// The week strip starts on the locale's first day of the week.
const firstWeekday = (() => { try { const l = new Intl.Locale(navigator.language); return ((l.getWeekInfo?.() || l.weekInfo)?.firstDay ?? 7) % 7; } catch { return 0; } })();
const addDays = (d, n) => { const t = day(d); t.setDate(t.getDate() + n); return isoDay(t); };
const weekOf = (d) => addDays(d, -((day(d).getDay() - firstWeekday + 7) % 7));

// viewCalendar is when episodes air, a week at a time: a strip of the
// week's days on top (pick one to see just that day), then a shelf a day.
// Shows in the library that are still running, and the ones being watched.
export async function viewCalendar() {
  setTitle('Calendar');
  const q = qs();
  const today = isoDay(new Date());
  const watching = q.get('watching') === '1';
  const valid = (d) => /^\d{4}-\d{2}-\d{2}$/.test(d || '') && !isNaN(day(d));
  const week = weekOf(valid(q.get('week')) ? q.get('week') : today);
  const picked = valid(q.get('day')) && weekOf(q.get('day')) === week ? q.get('day') : '';
  const dates = Array.from({ length: 7 }, (_, i) => addDays(week, i));
  const cal = await api(`/api/calendar?from=${dates[0]}&to=${dates[6]}`);
  const eps = cal.episodes.filter((e) => !watching || e.watching);
  const days = new Map();
  for (const e of eps) days.set(e.date, [...(days.get(e.date) || []), e]);
  const link = (o) => {
    const p = new URLSearchParams();
    const w = o.week ?? week;
    if (w !== weekOf(today)) p.set('week', w);
    if (o.day ?? picked) p.set('day', o.day ?? picked);
    if (o.watching ?? watching) p.set('watching', '1');
    return '/calendar' + (p.size ? '?' + p : '');
  };
  const range = `${day(dates[0]).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })} – ${day(dates[6]).toLocaleDateString(undefined, { month: 'short', day: 'numeric', year: 'numeric' })}`;
  const strip = `<nav class="weekstrip" aria-label="Week of ${esc(range)}">
    <a class="wk-nav" href="${link({ week: addDays(week, -7), day: '' })}" aria-label="Previous week">‹</a>
    <div class="wk-days">${dates.map((d) => {
      const t = day(d), n = days.get(d)?.length || 0;
      return `<a class="wk-day${d === picked ? ' on' : ''}${d === today ? ' today' : ''}${n ? '' : ' none'}" href="${link({ day: d === picked ? '' : d })}"
        aria-pressed="${d === picked}" aria-label="${esc(t.toLocaleDateString(undefined, { weekday: 'long', month: 'long', day: 'numeric' }))}, ${n} episode${n === 1 ? '' : 's'}">
        <span class="wd">${esc(t.toLocaleDateString(undefined, { weekday: 'short' }))}</span><span class="dn">${t.getDate()}</span><span class="cnt">${n || ''}</span></a>`;
    }).join('')}</div>
    <a class="wk-nav" href="${link({ week: addDays(week, 7), day: '' })}" aria-label="Next week">›</a></nav>`;
  const shown = [...days].filter(([d]) => !picked || d === picked);
  const node = el(`<div><div class="page-head"><h1>Calendar</h1>
    <div class="toolbar"><span class="muted small">${esc(range)}</span>${week !== weekOf(today) ? `<a class="chip" href="${link({ week: weekOf(today), day: '' })}">This week</a>` : ''}
      <button class="chip${watching ? ' on' : ''}" id="watching" aria-pressed="${watching}">Shows I'm watching</button></div></div>
    ${strip}
    ${shown.length ? shown.map(([d, list]) => shelf(dayName(d), list.map(calendarCard).join(''),
      { sub: `${list.length} episode${list.length === 1 ? '' : 's'}`, id: `day-${d}` })).join('')
      : `<div class="empty"><h2>${picked ? `Nothing airing ${dayName(picked) === 'Today' ? 'today' : 'on ' + esc(dayName(picked))}` : watching ? 'Nothing this week for your shows' : 'Nothing airing this week'}</h2>
        <p>Episodes show here for shows in the library that are still running. Use the arrows to see other weeks.</p></div>`}</div>`);
  bindShelves(node);
  $('#watching', node).onclick = () => go(link({ watching: !watching }), true);
  // The week strip's links replace this page rather than stacking history.
  for (const a of $$('.weekstrip a, .toolbar a.chip', node)) a.onclick = (e) => { e.preventDefault(); e.stopPropagation(); go(a.getAttribute('href'), true); };
  // Arrow keys move between days; Page Up/Down change week.
  $('.weekstrip', node).addEventListener('keydown', (e) => {
    const all = $$('.weekstrip a', node);
    const at = all.indexOf(document.activeElement);
    if (e.key === 'ArrowLeft' || e.key === 'ArrowRight') {
      e.preventDefault();
      all[Math.max(0, Math.min(all.length - 1, at + (e.key === 'ArrowLeft' ? -1 : 1)))]?.focus();
    }
  });
  // Open the current week on today, with what aired earlier in the week above it.
  const next = !picked && [...days.keys()].find((d) => d >= today);
  if (next && next !== [...days.keys()][0]) {
    requestAnimationFrame(() => {
      const top = document.getElementById(`day-${next}`)?.getBoundingClientRect().top;
      if (top != null) window.scrollTo({ top: top + window.scrollY - ($('.topbar')?.offsetHeight || 0) - 12 });
    });
  }
  return node;
}

// ---------------------------------------------------------------- people

const fmtDate = (d) => day(d).toLocaleDateString(undefined, { year: 'numeric', month: 'long', day: 'numeric' });
function ageAt(birth, end) {
  const b = day(birth), e = end ? day(end) : new Date();
  return e.getFullYear() - b.getFullYear() - (e < new Date(e.getFullYear(), b.getMonth(), b.getDate()) ? 1 : 0);
}

// viewPerson is an actor or crew member: their photo, details and
// biography, then rows of what they're in (role under each title).
export async function viewPerson(id) {
  const p = await api(`/api/people/${id}`);
  setTitle(p.name);
  ambient.base(p.backdropPath);
  const facts = [];
  if (p.knownFor) facts.push(`Known for ${p.knownFor.toLowerCase()}`);
  if (p.birthday) facts.push(`Born ${fmtDate(p.birthday)}${p.deathday ? '' : ` (age ${ageAt(p.birthday)})`}`);
  if (p.deathday) facts.push(`Died ${fmtDate(p.deathday)}${p.birthday ? ` (aged ${ageAt(p.birthday, p.deathday)})` : ''}`);
  if (p.placeOfBirth) facts.push(p.placeOfBirth);
  const credit = (v) => {
    const n = el(titleCard(v));
    const s = $('.s', n);
    if (s) s.textContent = [v.role, v.year].filter(Boolean).join(' · ');
    return n.outerHTML;
  };
  const initials = p.name.split(/\s+/).map((w) => w[0]).slice(0, 2).join('');
  const node = el(`<div><div class="hero coll-hero person-hero">
    <div class="content"><div class="poster">${p.profilePath ? `<img src="${img(p.profilePath, 'w500')}" alt="">` : `<div class="ph">${esc(initials)}</div>`}</div>
    <div><h1>${esc(p.name)}</h1>
    ${facts.length ? `<div class="meta">${facts.map(esc).join('<span class="muted">·</span>')}</div>` : ''}
    ${p.biography ? `<p class="overview bio">${esc(p.biography)}</p><button class="btn ghost small" id="more" hidden>More</button>` : ''}
    <div class="actions">${p.imdbId ? `<a class="btn ghost" href="https://www.imdb.com/name/${esc(p.imdbId)}/" target="_blank" rel="noopener">IMDb ↗</a>` : ''}
      <a class="btn ghost" href="https://www.themoviedb.org/person/${p.id}" target="_blank" rel="noopener">TMDB ↗</a></div></div></div></div>
    ${p.rows.length ? `<div class="coll-rows">${p.rows.map((r) => shelf(r.title, r.credits.map(credit).join(''), { sub: `${r.credits.length}` })).join('')}</div>`
      : '<div class="empty"><h2>No movies or shows listed</h2></div>'}</div>`);
  bindShelves(node);
  // A biography longer than its five lines opens below the header, on a
  // plain panel of its own; each paragraph takes focus in turn, so a remote
  // reads down through it before reaching the rows.
  const bio = $('.bio', node), more = $('#more', node);
  if (bio) requestAnimationFrame(() => {
    if (bio.scrollHeight <= bio.clientHeight + 4) return;
    more.hidden = false;
    let panel = null;
    const close = () => {
      panel?.remove();
      panel = null;
      bio.hidden = false;
      more.textContent = 'More';
      more.focus({ preventScroll: true });
      $('.hero', node).scrollIntoView({ block: 'start', behavior: 'smooth' });
    };
    more.onclick = () => {
      if (panel) return close();
      const paras = p.biography.split(/\n\s*\n|\n/).map((t) => t.trim()).filter(Boolean);
      panel = el(`<section class="bio-read"><h2>Biography</h2>${paras.map((t) => `<p tabindex="0">${esc(t)}</p>`).join('')}
        <button class="btn ghost small" type="button">Show less</button></section>`);
      $('button', panel).onclick = close;
      $('.hero', node).after(panel);
      bio.hidden = true;
      more.textContent = 'Less';
      const first = $('p', panel);
      first.focus({ preventScroll: true });
      first.scrollIntoView({ block: 'start', behavior: 'smooth' });
    };
  });
  return node;
}

// ---------------------------------------------------------------- studio

// Each studio's shelves as far as they've been loaded, so Back comes to the
// same cards; history.state keeps where the page and each shelf were.
const studioRows = new Map();

export async function viewStudio(id) {
  const c = await api(`/api/companies/${id}`);
  setTitle(c.name);
  ambient.base(c.backdropPath);
  const back = history.state?.studio === id ? history.state : null;
  const kept = back && studioRows.get(id);
  const rows = c.rows.map((r) => ({ ...r, page: 1, ...(kept?.[r.id] || {}) }));
  studioRows.set(id, Object.fromEntries(rows.map((r) => [r.id, r])));
  const facts = [c.headquarters, c.originCountry].filter(Boolean);
  const node = el(`<div><div class="hero coll-hero studio-hero">
    <div class="content">${c.logoPath ? `<div class="studio-logo"><img src="${img(c.logoPath, 'w500')}" alt=""></div>` : ''}
    <div><h1>${esc(c.name)}</h1>
    ${facts.length ? `<div class="meta">${facts.map(esc).join('<span class="muted">·</span>')}</div>` : ''}
    ${c.description ? `<p class="overview">${esc(c.description)}</p>` : ''}</div></div></div>
    ${rows.length ? `<div class="coll-rows">${rows.map((r) => shelf(r.title, r.items.map((v) => titleCard(v)).join(''), { id: `studio-${r.id}` })).join('')}</div>`
      : '<div class="empty"><h2>No movies or shows listed</h2></div>'}</div>`);
  bindShelves(node);

  // Titles not yet on the server all link to /item/stream; their TMDB id tells them apart.
  const cardKey = (a) => (a.dataset.streamTitle ? `${a.dataset.media}:${a.dataset.streamTitle}` : a.getAttribute('href'));
  const write = (card = document.activeElement?.closest?.('.card')) => {
    if (!node.isConnected) return window.removeEventListener('scroll', save);
    if (location.pathname !== `/studio/${id}`) return;
    const lefts = Object.fromEntries(rows.map((r) => [r.id, $(`#studio-${r.id} .row`, node)?.scrollLeft || 0]));
    history.replaceState({ studio: id, y: window.scrollY, lefts, focus: card && node.contains(card) ? cardKey(card) : null }, '');
  };
  let saving = 0;
  const save = () => {
    clearTimeout(saving);
    saving = setTimeout(write, 250);
  };
  // Opening a title saves at once, before the page changes under it.
  const now = (e) => {
    const card = e.target.closest?.('.card');
    if (card) { clearTimeout(saving); write(card); }
  };
  window.addEventListener('scroll', save, { passive: true });
  node.addEventListener('focusin', save);
  node.addEventListener('pointerdown', now, true);
  node.addEventListener('keydown', (e) => { if (e.key === 'Enter') now(e); }, true);

  for (const r of rows) {
    const row = $(`#studio-${r.id} .row`, node);
    if (!row) continue;
    row.addEventListener('scroll', save, { passive: true });
    let busy = false;
    const more = async () => {
      if (busy || r.page >= r.totalPages) return;
      busy = true;
      $('.retry', row)?.remove();
      try {
        const q = new URLSearchParams({ type: r.mediaType, sort: r.sort, company: id, page: r.page + 1 });
        const p = await api(`/api/discover/browse?${q}`);
        const have = new Set(r.items.map((v) => `${v.mediaType}:${v.tmdbId}`));
        const add = (p.items || []).filter((v) => !have.has(`${v.mediaType}:${v.tmdbId}`));
        r.items = r.items.concat(add);
        r.page = p.page || r.page + 1;
        r.totalPages = p.totalPages || r.page;
        row.append(...el(`<div>${add.map((v) => titleCard(v)).join('')}</div>`).children);
      } catch {
        const b = el('<button class="card retry" type="button"><div class="poster"><span>Couldn’t load more.<br>Retry</span></div></button>');
        b.onclick = () => more();
        row.append(b);
      } finally {
        busy = false;
      }
      near();
    };
    // Within about 15 cards of the end, the next page comes in.
    const near = () => {
      const w = row.firstElementChild?.offsetWidth || 160;
      if (row.scrollLeft + row.clientWidth >= row.scrollWidth - w * 15) more();
    };
    row.addEventListener('scroll', near, { passive: true });
    row.addEventListener('focusin', (e) => {
      const cards = [...row.children];
      if (cards.indexOf(e.target.closest('.card')) >= cards.length - 15) more();
    });
    requestAnimationFrame(near);
  }

  if (back) {
    // Once it's on the page: each shelf where it was, then the page, then the card.
    requestAnimationFrame(() => {
      for (const r of rows) {
        const row = $(`#studio-${r.id} .row`, node);
        if (row && back.lefts?.[r.id]) row.scrollLeft = back.lefts[r.id];
      }
      window.scrollTo(0, back.y || 0);
      const card = back.focus && [...$$('.card', node)].find((a) => cardKey(a) === back.focus);
      card?.focus({ preventScroll: true });
    });
  }
  return node;
}

// ---------------------------------------------------------------- collection editor

const windows = [['', 'Always'], ['10-01|10-31', 'October (Halloween)'], ['11-01|11-30', 'November (Thanksgiving)'], ['12-01|12-31', 'December (Holidays)'],
  ['02-01|02-14', 'Valentine\'s'], ['06-01|08-31', 'Summer'], ['custom', 'Custom dates…']];

export async function editCollection(c, defaults = {}) {
  const isNew = !c;
  c = c || { kind: 'manual', rules: {}, sort: '', itemLimit: 0, showOnHome: false, homeOrder: 0, showMissing: true, visibleFrom: '', visibleUntil: '', libraryId: defaults.libraryId || '' };
  const r = c.rules || {};
  const genres = await api('/api/genres').catch(() => []);
  const libs = getLibraries();
  const win = c.visibleFrom ? (windows.find(([k]) => k === `${c.visibleFrom}|${c.visibleUntil}`)?.[0] || 'custom') : '';
  const gState = (g) => r.genres?.includes(g) ? 'in' : r.excludeGenres?.includes(g) ? 'ex' : '';
  const opt = (v, label, cur) => `<option value="${v}" ${String(cur ?? '') === String(v) ? 'selected' : ''}>${label}</option>`;
  const body = `
    <div class="field"><label>Name</label><input type="text" name="name" value="${esc(c.name || '')}" placeholder="${c.kind === 'tmdb' ? 'Leave empty to use the source\'s name' : 'e.g. Date Night'}"></div>
    <div class="field"><label>Description</label><input type="text" name="description" value="${esc(c.description || '')}"></div>
    <div class="rules">
      <div class="field"><label>Type</label><select name="kind" ${isNew ? '' : 'disabled'}>${opt('manual', 'Hand-picked', c.kind)}${opt('smart', 'Smart (rules)', c.kind)}${opt('tmdb', 'From TMDB, MDBList, Simkl or Trakt', c.kind)}</select></div>
      <div class="field"><label>Library</label><select name="libraryId">${opt('', 'All libraries', c.libraryId)}${libs.map((l) => opt(l.id, esc(l.name), c.libraryId)).join('')}</select></div>
    </div>
    <fieldset data-for="smart"><legend>Rules</legend>
      <div class="rules">
        <div class="field"><label>Includes</label><select name="r.kind">${opt('', 'Movies & shows', r.kind)}${opt('movie', 'Movies', r.kind)}${opt('show', 'Shows', r.kind)}</select></div>
        <div class="field"><label>Title contains</label><input type="text" name="r.titleContains" value="${esc(r.titleContains || '')}"></div>
        <div class="field"><label>Year from</label><input type="number" name="r.yearFrom" min="1900" max="2100" value="${r.yearFrom || ''}"></div>
        <div class="field"><label>Year to</label><input type="number" name="r.yearTo" min="1900" max="2100" value="${r.yearTo || ''}"></div>
        <div class="field"><label>Minimum rating</label><input type="number" name="r.minRating" min="0" max="10" step="0.1" value="${r.minRating || ''}" placeholder="0–10"></div>
        <div class="field"><label>Added in the last (days)</label><input type="number" name="r.addedDays" min="0" value="${r.addedDays || ''}"></div>
        <div class="field"><label>Minimum resolution</label><select name="r.minHeight">${opt(0, 'Any', r.minHeight || 0)}${opt(700, '720p', r.minHeight)}${opt(1000, '1080p', r.minHeight)}${opt(2000, '4K', r.minHeight)}</select></div>
        <div class="field"><label>Content ratings</label><input type="text" name="r.contentRatings" value="${esc((r.contentRatings || []).join(', '))}" placeholder="e.g. G, PG, TV-Y7"></div>
      </div>
      <label class="check"><input type="checkbox" name="r.unwatched" ${r.unwatched ? 'checked' : ''}> Only titles the viewer hasn't watched</label>
      <label class="check"><input type="checkbox" name="r.hdr" ${r.hdr ? 'checked' : ''}> HDR / Dolby Vision only</label>
      ${genres.length ? `<div class="field" style="margin-top:8px"><label>Genres <span style="text-transform:none;font-weight:500">— click once to require any of, twice to exclude</span></label>
        <div class="genres" id="gpick">${genres.map((g) => `<button type="button" class="chip${gState(g.genre) === 'in' ? ' on' : ''}${gState(g.genre) === 'ex' ? ' ex' : ''}" data-g="${esc(g.genre)}">${esc(g.genre)}</button>`).join('')}</div></div>` : ''}
    </fieldset>
    <fieldset data-for="tmdb"><legend>Source</legend>
      <div class="rules">
        <div class="field"><label>From</label><select name="r.source">${[['collection', 'Franchise (movie series)'], ['list', 'TMDB list'], ['trending', 'Trending this week'], ['popular', 'Popular'],
          ['top_rated', 'Top rated'], ['now_playing', 'In theaters (movies)'], ['upcoming', 'Coming soon (movies)'], ['airing', 'On the air (shows)'],
          ['mdblist', 'MDBList list'], ['simkl', 'Simkl chart or watchlist'], ['trakt', 'Trakt chart or watchlist']].map(([k, v]) => opt(k, v, r.source || 'collection')).join('')}</select></div>
        <div class="field" data-src="chart"><label>Of</label><select name="r.mediaType">${opt('movie', 'Movies', r.mediaType || 'movie')}${opt('show', 'Shows', r.mediaType)}</select></div>
        <div class="field" data-src="mdblist"><label>MDBList link or user/list</label><input type="text" name="mdblist" value="${r.source === 'mdblist' ? esc(r.sourceRef || '') : ''}" placeholder="mdblist.com/lists/…"></div>
        <div class="field" data-src="simkl"><label>Simkl</label><select name="simkl">${simklRefs.map(([k, v]) => opt(k, v, r.source === 'simkl' ? r.sourceRef : 'trending/movies/week')).join('')}</select></div>
        <div class="field" data-src="trakt"><label>Trakt</label><select name="trakt">${traktRefs.map(([k, v]) => opt(k, v, r.source === 'trakt' ? r.sourceRef : 'trending/movies')).join('')}</select></div>
        <div class="field" data-src="list"><label>List ID or link</label><input type="text" name="listId" value="${r.source === 'list' ? r.sourceId : ''}" placeholder="themoviedb.org/list/…"></div>
      </div>
      <div data-src="collection"><div class="field"><label>Franchise</label><input type="search" id="fq" placeholder="Search, e.g. Harry Potter" autocomplete="off"></div>
        <div class="results" id="fres">${r.source === 'collection' && r.sourceId ? `<button type="button" class="on" data-fid="${r.sourceId}">${esc(c.name)}</button>` : ''}</div></div>
      <label class="check"><input type="checkbox" name="showMissing" ${c.showMissing ? 'checked' : ''}> Also show titles that aren't in the library</label>
    </fieldset>
    <fieldset><legend>Display</legend>
      <div class="rules">
        <div class="field"><label>Order</label><select name="sort">${collSorts.map(([k, v]) => opt(k, k ? v : 'Default (source order)', c.sort)).join('')}</select></div>
        <div class="field"><label>Show at most</label><input type="number" name="itemLimit" min="0" value="${c.itemLimit || ''}" placeholder="No limit"></div>
        <div class="field"><label>Visible</label><select name="window">${windows.map(([k, v]) => opt(k, v, win)).join('')}</select></div>
        <div class="field" data-custom><label>From – until (MM-DD)</label><div style="display:flex;gap:6px"><input type="text" name="visibleFrom" value="${esc(c.visibleFrom)}" placeholder="10-01" maxlength="5">
          <input type="text" name="visibleUntil" value="${esc(c.visibleUntil)}" placeholder="10-31" maxlength="5"></div></div>
      </div>
      <label class="check"><input type="checkbox" name="showOnHome" ${c.showOnHome ? 'checked' : ''}> Show as a row on everyone's Home screen</label>
      <div class="field" data-home><label>Home position (lower comes first)</label><input type="number" name="homeOrder" value="${c.homeOrder || 0}"></div>
      <label class="check" data-librow><input type="checkbox" name="showInLibrary" ${c.showInLibrary ? 'checked' : ''}> Show as a row on the library's page (reorder rows there)</label>
    </fieldset>`;

  let franchise = r.source === 'collection' ? r.sourceId : 0;
  const p = modal(isNew ? 'New collection' : `Edit “${c.name}”`, body, {
    ok: isNew ? 'Create' : 'Save',
    onOk: async (f) => {
      const v = (n) => f.elements[n]?.value?.trim() ?? '';
      const num = (n) => Number(v(n)) || 0;
      const kind = isNew ? v('kind') : c.kind;
      const rules = {};
      if (kind === 'smart') {
        Object.assign(rules, { kind: v('r.kind'), titleContains: v('r.titleContains'), yearFrom: num('r.yearFrom'), yearTo: num('r.yearTo'),
          minRating: num('r.minRating'), addedDays: num('r.addedDays'), minHeight: num('r.minHeight'),
          contentRatings: v('r.contentRatings').split(',').map((x) => x.trim()).filter(Boolean),
          unwatched: f.elements['r.unwatched'].checked, hdr: f.elements['r.hdr'].checked,
          genres: $$('#gpick .chip.on', f).map((b) => b.dataset.g), excludeGenres: $$('#gpick .chip.ex', f).map((b) => b.dataset.g) });
      } else if (kind === 'tmdb') {
        rules.source = v('r.source');
        rules.mediaType = v('r.mediaType');
        if (rules.source === 'collection') {
          if (!franchise) throw new Error('Search for a franchise and pick one.');
          rules.sourceId = franchise;
        } else if (rules.source === 'list') {
          rules.sourceId = Number(v('listId').match(/(\d+)/)?.[1] || 0);
          if (!rules.sourceId) throw new Error('Enter a TMDB list ID or link.');
        } else if (rules.source === 'mdblist') {
          rules.sourceRef = v('mdblist');
          if (!rules.sourceRef) throw new Error('Enter an MDBList list link.');
        } else if (rules.source === 'simkl') {
          rules.sourceRef = v('simkl');
        } else if (rules.source === 'trakt') {
          rules.sourceRef = v('trakt');
        }
      }
      let [from, until] = [v('visibleFrom'), v('visibleUntil')];
      const w = v('window');
      if (w === '') [from, until] = ['', ''];
      else if (w !== 'custom') [from, until] = w.split('|');
      const payload = { name: v('name'), description: v('description'), kind, rules, libraryId: v('libraryId'), sort: v('sort'),
        itemLimit: num('itemLimit'), showOnHome: f.elements.showOnHome.checked, homeOrder: num('homeOrder'),
        showMissing: f.elements.showMissing.checked, visibleFrom: from, visibleUntil: until,
        showInLibrary: !!v('libraryId') && f.elements.showInLibrary.checked };
      if (kind !== 'tmdb' && !payload.name) throw new Error('Give the collection a name.');
      const saved = isNew ? await api('/api/collections', { method: 'POST', body: { ...payload, itemIds: defaults.itemIds } })
        : await api(`/api/collections/${c.id}`, { method: 'PATCH', body: payload });
      return saved;
    },
  });

  // Wire up the open form.
  const f = $('.modal');
  f.classList.add('wide');
  const sync = () => {
    const kind = f.elements.kind.value;
    for (const fs of $$('[data-for]', f)) fs.hidden = fs.dataset.for !== kind;
    const src = f.elements['r.source'].value;
    for (const x of $$('[data-src]', f)) {
      const want = x.dataset.src;
      x.hidden = !(want === src || (want === 'chart' && ['trending', 'popular', 'top_rated'].includes(src)));
    }
    $('[data-custom]', f).hidden = f.elements.window.value !== 'custom';
    $('[data-home]', f).hidden = !f.elements.showOnHome.checked;
    $('[data-librow]', f).hidden = !f.elements.libraryId.value;
  };
  f.addEventListener('change', sync);
  sync();
  for (const b of $$('#gpick .chip', f)) {
    b.onclick = () => {
      if (b.classList.contains('on')) { b.classList.remove('on'); b.classList.add('ex'); }
      else if (b.classList.contains('ex')) b.classList.remove('ex');
      else b.classList.add('on');
    };
  }
  let t;
  $('#fq', f)?.addEventListener('input', (e) => {
    clearTimeout(t);
    const q = e.target.value.trim();
    if (q.length < 2) return;
    t = setTimeout(async () => {
      let res = [];
      try { res = await api('/api/collections/franchises?q=' + encodeURIComponent(q)); } catch (err) { $('#fres', f).innerHTML = `<p class="error small">${esc(err.message)}</p>`; return; }
      $('#fres', f).innerHTML = res.length ? res.slice(0, 12).map((x) => `<button type="button" data-fid="${x.id}" class="${x.id === franchise ? 'on' : ''}">
        ${x.poster_path ? `<img src="${img(x.poster_path, 'w92')}" alt="">` : '<img alt="">'}<span>${esc(x.name)}</span></button>`).join('') : '<p class="muted small">No franchises found.</p>';
    }, 300);
  });
  $('#fres', f)?.addEventListener('click', (e) => {
    const b = e.target.closest('[data-fid]');
    if (!b) return;
    franchise = Number(b.dataset.fid);
    for (const x of $$('[data-fid]', f)) x.classList.toggle('on', x === b);
  });
  // Typing in the search box must not submit the form.
  $('#fq', f)?.addEventListener('keydown', (e) => e.key === 'Enter' && e.preventDefault());

  const saved = await p;
  if (!saved) return;
  toast(isNew ? 'Collection created.' : 'Collection saved.');
  if (isNew && saved.id) go(`/collection/${saved.id}`); else render();
}

// addToCollection toggles an item's membership in hand-picked collections.
export async function addToCollection(it) {
  const [all, member] = await Promise.all([api('/api/collections'), api(`/api/items/${it.id}/collections`)]);
  const manual = all.filter((c) => c.kind === 'manual');
  const had = new Set(member);
  const body = `${manual.length ? `<div class="pick-list">${manual.map((c) => `<label><input type="checkbox" name="c" value="${c.id}" ${had.has(c.id) ? 'checked' : ''}>
      <span style="flex:1">${esc(c.name)}</span><span class="small muted">${c.itemCount}</span></label>`).join('')}</div>` : '<p class="muted">No hand-picked collections yet.</p>'}
    <div class="field"><label>Or create a new one</label><input type="text" name="newName" placeholder="Collection name"></div>`;
  const res = await modal(`Collections for “${it.title}”`, body, {
    onOk: async (f) => {
      const want = new Set($$('input[name=c]:checked', f).map((x) => x.value));
      const ops = [];
      for (const c of manual) {
        if (want.has(c.id) && !had.has(c.id)) ops.push(api(`/api/collections/${c.id}/items`, { method: 'POST', body: { itemIds: [it.id] } }));
        if (!want.has(c.id) && had.has(c.id)) ops.push(api(`/api/collections/${c.id}/items/${it.id}`, { method: 'DELETE' }));
      }
      const name = f.elements.newName.value.trim();
      if (name) ops.push(api('/api/collections', { method: 'POST', body: { name, kind: 'manual', itemIds: [it.id] } }));
      await Promise.all(ops);
      return true;
    },
  });
  if (res) toast('Collections updated.');
}

// ---------------------------------------------------------------- account

const languages = ['en', 'es', 'fr', 'de', 'it', 'pt', 'ja', 'ko', 'zh', 'hi', 'ru', 'ar', 'nl', 'sv', 'no', 'da', 'fi', 'pl', 'tr', 'el', 'he', 'th', 'vi', 'id', 'cs', 'hu', 'ro', 'uk', 'ta', 'te', 'tl', 'fa'];

export async function viewAccount() {
  setTitle('Settings');
  const prefs = await api('/api/me/prefs');
  let names;
  try { names = new Intl.DisplayNames([navigator.language], { type: 'language' }); } catch {}
  const name = (c) => names?.of(c) || c;
  const sorted = [...languages].sort((a, b) => a === 'en' ? -1 : b === 'en' ? 1 : name(a).localeCompare(name(b)));
  const node = el(`<div><div class="page-head"><h1>Your settings</h1></div>
    <div class="panel" style="max-width:640px"><h2>Playback</h2>
      <div class="field"><label>Preferred audio language</label>
        <select id="audio">${`<option value="">File's default track</option>` + sorted.map((c) => `<option value="${c}" ${c === prefs.audioLanguage ? 'selected' : ''}>${esc(name(c))}</option>`).join('')}</select></div>
      <p class="small muted">Wren TV starts playback with an audio track in this language when the file has one.</p>
      <div class="field"><label>Subtitles</label>
        <select id="subs">${`<option value="">Off</option>` + sorted.map((c) => `<option value="${c}" ${c === prefs.subtitleLanguage ? 'selected' : ''}>${esc(name(c))}</option>`).join('')}</select></div>
      <p class="small muted">Subtitles in this language turn on by themselves when the file has them. These settings are saved to your account, so they apply on every device and browser you sign in with. You can still switch tracks while watching.</p>
      <div class="field"><label for="forced">Forced subtitles</label>
        <select id="forced"><option value="1" ${prefs.forcedSubtitles !== false ? 'selected' : ''}>Show automatically</option><option value="" ${prefs.forcedSubtitles === false ? 'selected' : ''}>Off</option></select></div>
      <p class="small muted">Forced tracks only cover foreign-language lines, like signs or a scene in another language. With subtitles off, Wren TV shows them in your subtitle or audio language.</p>
      <div class="field"><label for="foreign">Subtitles with foreign audio</label>
        <select id="foreign"><option value="1" ${prefs.foreignAudioSubtitles !== false ? 'selected' : ''}>Show automatically</option><option value="" ${prefs.foreignAudioSubtitles === false ? 'selected' : ''}>Off</option></select></div>
      <p class="small muted">When a title plays in a language other than your audio language, subtitles in your audio language turn on.</p>
      <div class="field"><label>Counts as watched at</label>
        <select id="watched">${[50, 60, 70, 75, 80, 85, 90, 95, 98].map((v) => `<option value="${v}" ${v === (prefs.watchedPercent || 90) ? 'selected' : ''}>${v}%</option>`).join('')}</select></div>
      <p class="small muted" style="margin:0">How far into a movie or episode you need to get before it's marked watched and drops off Continue Watching. Lower it if you usually stop when the credits start.</p>
      <div class="field" style="margin-top:16px"><label for="continue">Add to Continue Watching after</label>
        <select id="continue">${[[10, '10 seconds'], [30, '30 seconds'], [60, '1 minute'], [120, '2 minutes'], [300, '5 minutes'], [600, '10 minutes']].map(([v, l]) => `<option value="${v}" ${v === (prefs.continueAfter || 30) ? 'selected' : ''}>${l}</option>`).join('')}</select></div>
      <p class="small muted" style="margin:0">How long you need to watch a movie or episode before it shows on Continue Watching, so trying a few minutes of something doesn't put it there.</p>
    </div>
    <div class="panel" style="max-width:640px;margin-top:18px"><h2>Browsing</h2>
      <div class="field"><label for="posters">Poster size</label>
        <select id="posters">${[['small', 'Small'], ['medium', 'Medium'], ['large', 'Large']].map(([v, l]) => `<option value="${v}" ${v === (prefs.posterSize || posterSize()) ? 'selected' : ''}>${l}</option>`).join('')}</select></div>
      <p class="small muted" style="margin:0">How big posters and tiles are in rows and grids. It's saved to your account, so every app you sign in to uses it.</p>
    </div>
    <div class="panel" style="max-width:640px;margin-top:18px" id="simkl"></div>
    <div class="panel" style="max-width:640px;margin-top:18px" id="trakt"></div>
    <div class="panel" style="max-width:640px;margin-top:18px"><h2>Sign in a TV</h2>
      <p class="small muted">On the TV, choose “Sign in with your phone”. Scan its QR code, or enter the code it shows here.</p>
      <a class="btn" href="/link">Enter a TV code</a>
    </div></div>`);
  const save = (key) => async (e) => {
    try {
      await api('/api/me/prefs', { method: 'PATCH', body: { [key]: e.target.value } });
      toast('Saved. New playback will use this setting.');
    } catch (err) { toast(err.message); }
  };
  $('#audio', node).onchange = save('audioLanguage');
  $('#subs', node).onchange = save('subtitleLanguage');
  const toggle = (key) => async (e) => {
    try {
      await api('/api/me/prefs', { method: 'PATCH', body: { [key]: e.target.value === '1' } });
      toast('Saved. New playback will use this setting.');
    } catch (err) { toast(err.message); }
  };
  $('#forced', node).onchange = toggle('forcedSubtitles');
  $('#foreign', node).onchange = toggle('foreignAudioSubtitles');
  $('#watched', node).onchange = async (e) => {
    try {
      await api('/api/me/prefs', { method: 'PATCH', body: { watchedPercent: Number(e.target.value) } });
      toast('Saved.');
    } catch (err) { toast(err.message); }
  };
  $('#continue', node).onchange = async (e) => {
    try {
      await api('/api/me/prefs', { method: 'PATCH', body: { continueAfter: Number(e.target.value) } });
      toast('Saved.');
    } catch (err) { toast(err.message); }
  };
  $('#posters', node).onchange = async (e) => {
    setPosterSize(e.target.value);
    try {
      await api('/api/me/prefs', { method: 'PATCH', body: { posterSize: e.target.value } });
      toast('Saved.');
    } catch (err) { toast(err.message); }
  };
  trackerPanel($('#simkl', node), 'simkl');
  trackerPanel($('#trakt', node), 'trakt');
  return node;
}

// trackerPanel links a Simkl or Trakt account: scrobbling while you watch,
// and an import of the history you already have there.
async function trackerPanel(box, tracker) {
  const name = tracker === 'trakt' ? 'Trakt' : 'Simkl';
  const base = `/api/me/${tracker}`;
  let st;
  try { st = await api(base); } catch { box.remove(); return; }
  if (!st.available && !st.linked) { box.remove(); return; }
  const a = st.account;
  const ago = (t) => t ? new Date(t * 1000).toLocaleString() : '';
  let body;
  if (st.linked) {
    const imp = st.importing || a.importStatus === 'running' ? 'Importing your history…'
      : a.importStatus === 'done' ? `Last import ${ago(a.importAt)}: ${a.importMovies} movies and ${a.importEpisodes} episodes marked watched.`
      : a.importStatus ? `Import failed: ${esc(a.importStatus)}` : '';
    body = `<p>Linked to <b>${esc(a.username || `your ${name} account`)}</b>.</p>
      <label class="check"><input type="checkbox" data-a="scrobble" ${a.scrobble ? 'checked' : ''}> Send what I watch to ${name}</label>
      <p class="small muted">${name} shows what you're watching while you play, and marks it watched when you pass your “counts as watched” point. Marking something watched by hand updates ${name} too.</p>
      ${imp ? `<p class="small muted">${imp}</p>` : ''}
      <div style="display:flex;gap:8px;flex-wrap:wrap"><button class="btn" data-a="import" ${st.importing ? 'disabled' : ''}>Import history again</button>
      <button class="btn danger" data-a="unlink">Disconnect</button></div>`;
  } else if (st.pending && !st.pending.error) {
    const url = st.pending.urlComplete || st.pending.url;
    body = `<p>Open <a href="${esc(url)}" target="_blank" rel="noopener">${esc(st.pending.url.replace(/^https?:\/\//, ''))}</a> and enter</p>
      <p style="font-size:28px;font-weight:700;letter-spacing:2px;margin:6px 0 12px">${esc(st.pending.userCode)}</p>
      <p class="small muted">Waiting for you to approve it on ${name}…</p>
      <button class="btn" data-a="cancel">Cancel</button>`;
  } else {
    body = `<p class="small muted">Link your ${name} account to import what you've already watched and keep ${name} up to date as you watch here.</p>
      ${st.pending?.error ? `<p class="small" style="color:var(--danger,#e66)">${esc(st.pending.error)}</p>` : ''}
      <button class="btn primary" data-a="link">Connect ${name}</button>`;
  }
  box.innerHTML = `<h2>${name}</h2>${body}`;
  const again = () => trackerPanel(box, tracker);
  const act = (key, fn) => { const b = $(`[data-a=${key}]`, box); if (b) b.onclick = async () => { try { await fn(); } catch (err) { toast(err.message); } again(); }; };
  act('link', async () => {
    const p = await api(`${base}/link`, { method: 'POST' });
    if (p.urlComplete) window.open(p.urlComplete, '_blank', 'noopener');
  });
  act('cancel', () => api(`${base}/link`, { method: 'DELETE' }));
  act('unlink', () => api(base, { method: 'DELETE' }));
  act('import', () => api(`${base}/import`, { method: 'POST' }));
  const cb = $('[data-a=scrobble]', box);
  if (cb) cb.onchange = async () => { try { await api(base, { method: 'PATCH', body: { scrobble: cb.checked } }); toast('Saved.'); } catch (err) { toast(err.message); } };
  if ((st.pending && !st.pending.error) || st.importing || a?.importStatus === 'running') {
    setTimeout(() => { if (box.isConnected) again(); }, 3000);
  }
}


// ---------------------------------------------------------------- library rows

// editLibraryRows picks the collections shown as rows on a library's page
// and their order, top to bottom.
export async function editLibraryRows(libId) {
  const all = (await api('/api/collections')).filter((c) => c.libraryId === libId || !c.libraryId);
  const on = all.filter((c) => c.showInLibrary && c.libraryId === libId).sort((a, b) => a.libraryOrder - b.libraryOrder);
  const off = all.filter((c) => !on.includes(c));
  const row = (c, checked) => `<li class="row-pick" data-id="${c.id}"><label class="check" style="flex:1"><input type="checkbox" ${checked ? 'checked' : ''}>
    <span>${esc(c.name)} <span class="small muted">${esc(kindLabel(c))}${c.libraryId ? '' : ' · all libraries, moves here'}</span></span></label>
    <button type="button" class="btn ghost small" data-up title="Move up">↑</button><button type="button" class="btn ghost small" data-down title="Move down">↓</button></li>`;
  const body = all.length ? `<p class="muted small">Ticked collections show as rows on this library's page, in this order. Add MDBList, Simkl and Trakt lists with New collection.</p>
    <ul class="row-picks" style="list-style:none;padding:0;margin:0">${on.map((c) => row(c, true)).join('')}${off.map((c) => row(c, false)).join('')}</ul>`
    : '<p class="muted">No collections yet. Make one under Collections (hand-picked, smart, TMDB, MDBList, Simkl or Trakt), then pick it here.</p>';
  const p = modal('Library rows', body, {
    ok: 'Save',
    onOk: async (f) => {
      const ids = $$('.row-pick', f).filter((li) => $('input', li).checked).map((li) => li.dataset.id);
      await api(`/api/libraries/${libId}/rows`, { method: 'PUT', body: { ids } });
      return true;
    },
  });
  const f = $('.modal');
  f.addEventListener('click', (e) => {
    const li = e.target.closest('.row-pick');
    if (!li) return;
    if (e.target.closest('[data-up]') && li.previousElementSibling) li.parentNode.insertBefore(li, li.previousElementSibling);
    if (e.target.closest('[data-down]') && li.nextElementSibling) li.parentNode.insertBefore(li.nextElementSibling, li);
  });
  if (await p) { toast('Rows saved.'); render(); }
}
