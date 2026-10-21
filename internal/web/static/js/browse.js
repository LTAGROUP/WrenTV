// Browsing beyond libraries: genres, collections, and the viewer's own settings.
import { api, img } from './api.js';
import * as ambient from './ambient.js';
import { esc, $, $$, el, toast, modal, confirmDialog, icons } from './ui.js';
import { go, render, setTitle, isAdmin, getLibraries, posterCard, titleCard, lazyImg, libraryTabs, bd, shelf, bindShelves, posterSize, setPosterSize } from './app.js';

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
    now_playing: 'In theaters', upcoming: 'Coming soon', airing: 'On the air', mdblist: 'MDBList', simkl: 'Simkl' }[r.source] || 'TMDB';
}

// Simkl charts and watchlists a collection can follow.
const simklRefs = [['trending/movies/today', 'Trending movies today'], ['trending/movies/week', 'Trending movies this week'],
  ['trending/movies/month', 'Trending movies this month'], ['trending/tv/today', 'Trending shows today'], ['trending/tv/week', 'Trending shows this week'],
  ['trending/tv/month', 'Trending shows this month'], ['trending/anime/week', 'Trending anime this week'], ['trending/anime/month', 'Trending anime this month'],
  ['watchlist/plantowatch', 'My Simkl: Plan to watch'], ['watchlist/watching', 'My Simkl: Watching'], ['watchlist/completed', 'My Simkl: Completed']];

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

function sortTitles(list, sort) {
  const out = [...list];
  if (sort === 'title') out.sort((a, b) => a.title.localeCompare(b.title));
  else if (sort === 'year') out.sort((a, b) => (b.year || 0) - (a.year || 0));
  else if (sort === 'year_asc') out.sort((a, b) => (a.year || 9999) - (b.year || 9999));
  else if (sort === 'rating') out.sort((a, b) => (b.item?.rating || b.rating || 0) - (a.item?.rating || a.rating || 0));
  else if (sort === 'added') out.sort((a, b) => (b.item?.addedAt || 0) - (a.item?.addedAt || 0));
  return out;
}

export async function viewCollection(id) {
  // "order" is the default order as a grid, for a collection that's rows.
  const sort = qs().get('sort') || '';
  const srv = sort === 'order' ? '' : sort;
  const d = await api(`/api/collections/${id}` + (srv ? `?sort=${srv}` : ''));
  const c = d.collection;
  setTitle(c.name);
  ambient.base(c.backdropPath);
  const admin = isAdmin();
  // When streaming, a TMDB collection is everything in it, not just what the server has.
  const titles = d.titles ? sortTitles(d.titles, srv) : null;
  // A big collection shows as rows, like Home; picking a sort shows a grid.
  const rows = !sort && d.rows?.length ? d.rows : null;
  const sorts = rows ? [['', 'Rows'], ['order', 'Default order'], ...collSorts.slice(1)] : collSorts;
  const meta = [titles ? `${titles.length} title${titles.length === 1 ? '' : 's'}` : `${d.items.length} in your library`, kindLabel(c)];
  if (c.visibleFrom) meta.push(`Visible ${fmtMD(c.visibleFrom)} – ${fmtMD(c.visibleUntil)}`);
  if (admin && c.showOnHome) meta.push('Shown on Home');
  const removable = admin && c.kind === 'manual';
  const card = (it) => removable ? posterCard(it, `<button class="rm" data-rm="${it.id}" title="Remove from collection" aria-label="Remove ${esc(it.title)} from collection">×</button>`) : posterCard(it);
  const list = titles || d.items;
  const shelves = rows && rows.map((r) => shelf(r.title, r.index.map((n, i) => {
    const v = list[n];
    if (!v) return '';
    return titles ? titleCard(v, r.title === 'Top 20' && i < 10 ? i + 1 : 0) : card(v);
  }).join(''), { sub: `${r.index.length}` })).join('');
  const node = el(`<div><div class="hero coll-hero">
    <div class="content">${c.posterPath ? `<div class="poster"><img src="${img(c.posterPath, 'w500')}" alt=""></div>` : ''}
    <div><div class="small" style="margin-bottom:6px"><a href="/collections">Collections</a></div><h1>${esc(c.name)}</h1>
    <div class="meta">${meta.map(esc).join('<span class="muted">·</span>')}</div>
    ${c.syncError && admin ? `<p class="error small">Last sync failed: ${esc(c.syncError)}</p>` : ''}
    ${c.description ? `<p class="overview">${esc(c.description)}</p>` : ''}
    <div class="actions"><select id="sort" data-caption="Sort">${sorts.map(([k, v]) => `<option value="${k}" ${k === sort ? 'selected' : ''}>${v}</option>`).join('')}</select>
    ${admin ? `<button class="btn" data-act="edit">Edit</button>${c.kind === 'tmdb' ? '<button class="btn ghost" data-act="sync">Sync now</button>' : ''}
      <button class="btn ghost danger" data-act="delete">Delete</button>` : ''}</div></div></div></div>
    ${shelves ? `<div class="coll-rows">${shelves}</div>` : titles ? (titles.length ? `<div class="grid">${titles.map((v) => titleCard(v)).join('')}</div>` : '<div class="empty"><h2>This collection is empty</h2></div>')
      : d.items.length ? `<div class="grid">${d.items.map(card).join('')}</div>`
      : `<div class="empty"><h2>Nothing from your library yet</h2><p>${c.kind === 'manual' ? 'Add titles from their detail page with “+ Collection”.' : 'Titles appear as soon as the library has them.'}</p></div>`}
    ${!titles && d.missing?.length ? `<h2 class="section-title">Not in your library <span class="muted small">${d.missing.length}</span></h2>
      <div class="grid">${d.missing.map((v) => titleCard(v)).join('')}</div>` : ''}</div>`);
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
  for (const b of $$('[data-rm]', node)) {
    b.onclick = async (e) => {
      e.preventDefault();
      e.stopPropagation();
      await api(`/api/collections/${id}/items/${b.dataset.rm}`, { method: 'DELETE' });
      b.closest('.card').remove();
    };
  }
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

// viewCalendar is when episodes air, a shelf a day from a week back to a
// month ahead: shows in the library that are still running, and the ones
// being watched.
export async function viewCalendar() {
  setTitle('Calendar');
  const watching = qs().get('watching') === '1';
  const cal = await api('/api/calendar');
  const eps = cal.episodes.filter((e) => !watching || e.watching);
  const days = new Map();
  for (const e of eps) days.set(e.date, [...(days.get(e.date) || []), e]);
  const today = isoDay(new Date());
  const node = el(`<div><div class="page-head"><h1>Calendar</h1>
    <div class="toolbar"><button class="chip${watching ? ' on' : ''}" id="watching" aria-pressed="${watching}">Shows I'm watching</button></div></div>
    ${eps.length ? [...days].map(([d, list]) => shelf(dayName(d), list.map(calendarCard).join(''),
      { sub: `${list.length} episode${list.length === 1 ? '' : 's'}`, id: `day-${d}` })).join('')
      : `<div class="empty"><h2>${watching ? 'Nothing coming up for your shows' : 'Nothing airing'}</h2>
        <p>Episodes from a week back to a month ahead show here, for shows in the library that are still running.</p></div>`}</div>`);
  bindShelves(node);
  $('#watching', node).onclick = () => go('/calendar' + (watching ? '' : '?watching=1'), true);
  // Open on what's next to air, with what just aired above it.
  const next = [...days.keys()].find((d) => d >= today);
  if (next && next !== [...days.keys()][0]) {
    requestAnimationFrame(() => {
      const top = document.getElementById(`day-${next}`)?.getBoundingClientRect().top;
      if (top != null) window.scrollTo({ top: top + window.scrollY - ($('.topbar')?.offsetHeight || 0) - 12 });
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
      <div class="field"><label>Type</label><select name="kind" ${isNew ? '' : 'disabled'}>${opt('manual', 'Hand-picked', c.kind)}${opt('smart', 'Smart (rules)', c.kind)}${opt('tmdb', 'From TMDB, MDBList or Simkl', c.kind)}</select></div>
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
          ['mdblist', 'MDBList list'], ['simkl', 'Simkl chart or watchlist']].map(([k, v]) => opt(k, v, r.source || 'collection')).join('')}</select></div>
        <div class="field" data-src="chart"><label>Of</label><select name="r.mediaType">${opt('movie', 'Movies', r.mediaType || 'movie')}${opt('show', 'Shows', r.mediaType)}</select></div>
        <div class="field" data-src="mdblist"><label>MDBList link or user/list</label><input type="text" name="mdblist" value="${r.source === 'mdblist' ? esc(r.sourceRef || '') : ''}" placeholder="mdblist.com/lists/…"></div>
        <div class="field" data-src="simkl"><label>Simkl</label><select name="simkl">${simklRefs.map(([k, v]) => opt(k, v, r.source === 'simkl' ? r.sourceRef : 'trending/movies/week')).join('')}</select></div>
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
      <p class="small muted">Subtitles in this language turn on by themselves when the file has them. Both settings are saved to your account, so they apply on every device and browser you sign in with. You can still switch tracks while watching.</p>
      <div class="field"><label>Counts as watched at</label>
        <select id="watched">${[50, 60, 70, 75, 80, 85, 90, 95, 98].map((v) => `<option value="${v}" ${v === (prefs.watchedPercent || 90) ? 'selected' : ''}>${v}%</option>`).join('')}</select></div>
      <p class="small muted" style="margin:0">How far into a movie or episode you need to get before it's marked watched and drops off Continue Watching. Lower it if you usually stop when the credits start.</p>
      <div class="field" style="margin-top:16px"><label for="continue">Add to Continue Watching after</label>
        <select id="continue">${[[10, '10 seconds'], [30, '30 seconds'], [60, '1 minute'], [120, '2 minutes'], [300, '5 minutes'], [600, '10 minutes']].map(([v, l]) => `<option value="${v}" ${v === (prefs.continueAfter || 30) ? 'selected' : ''}>${l}</option>`).join('')}</select></div>
      <p class="small muted" style="margin:0">How long you need to watch a movie or episode before it shows on Continue Watching, so trying a few minutes of something doesn't put it there.</p>
    </div>
    <div class="panel" style="max-width:640px;margin-top:18px"><h2>Browsing</h2>
      <div class="field"><label for="posters">Poster size</label>
        <select id="posters">${[['small', 'Small'], ['medium', 'Medium'], ['large', 'Large']].map(([v, l]) => `<option value="${v}" ${v === posterSize() ? 'selected' : ''}>${l}</option>`).join('')}</select></div>
      <p class="small muted" style="margin:0">How big posters and tiles are in rows and grids, in this browser.</p>
    </div>
    <div class="panel" style="max-width:640px;margin-top:18px" id="simkl"></div>
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
  $('#posters', node).onchange = (e) => setPosterSize(e.target.value);
  simklPanel($('#simkl', node));
  return node;
}

// simklPanel links a Simkl account: scrobbling while you watch, and an
// import of the history you already have there.
async function simklPanel(box) {
  let st;
  try { st = await api('/api/me/simkl'); } catch { box.remove(); return; }
  if (!st.available && !st.linked) { box.remove(); return; }
  const a = st.account;
  const ago = (t) => t ? new Date(t * 1000).toLocaleString() : '';
  let body;
  if (st.linked) {
    const imp = st.importing || a.importStatus === 'running' ? 'Importing your history…'
      : a.importStatus === 'done' ? `Last import ${ago(a.importAt)}: ${a.importMovies} movies and ${a.importEpisodes} episodes marked watched.`
      : a.importStatus ? `Import failed: ${esc(a.importStatus)}` : '';
    body = `<p>Linked to <b>${esc(a.username || 'your Simkl account')}</b>.</p>
      <label class="check"><input type="checkbox" id="sk-scrobble" ${a.scrobble ? 'checked' : ''}> Send what I watch to Simkl</label>
      <p class="small muted">Simkl shows what you're watching while you play, and marks it watched when you pass your “counts as watched” point. Marking something watched by hand updates Simkl too.</p>
      ${imp ? `<p class="small muted">${imp}</p>` : ''}
      <div style="display:flex;gap:8px;flex-wrap:wrap"><button class="btn" id="sk-import" ${st.importing ? 'disabled' : ''}>Import history again</button>
      <button class="btn danger" id="sk-unlink">Disconnect</button></div>`;
  } else if (st.pending && !st.pending.error) {
    const url = st.pending.urlComplete || st.pending.url;
    body = `<p>Open <a href="${esc(url)}" target="_blank" rel="noopener">${esc(st.pending.url.replace(/^https?:\/\//, ''))}</a> and enter</p>
      <p style="font-size:28px;font-weight:700;letter-spacing:2px;margin:6px 0 12px">${esc(st.pending.userCode)}</p>
      <p class="small muted">Waiting for you to approve it on Simkl…</p>
      <button class="btn" id="sk-cancel">Cancel</button>`;
  } else {
    body = `<p class="small muted">Link your Simkl account to import what you've already watched and keep Simkl up to date as you watch here.</p>
      ${st.pending?.error ? `<p class="small" style="color:var(--danger,#e66)">${esc(st.pending.error)}</p>` : ''}
      <button class="btn primary" id="sk-link">Connect Simkl</button>`;
  }
  box.innerHTML = `<h2>Simkl</h2>${body}`;
  const again = () => simklPanel(box);
  const act = (sel, fn) => { const b = $(sel, box); if (b) b.onclick = async () => { try { await fn(); } catch (err) { toast(err.message); } again(); }; };
  act('#sk-link', async () => {
    const p = await api('/api/me/simkl/link', { method: 'POST' });
    if (p.urlComplete) window.open(p.urlComplete, '_blank', 'noopener');
  });
  act('#sk-cancel', () => api('/api/me/simkl/link', { method: 'DELETE' }));
  act('#sk-unlink', () => api('/api/me/simkl', { method: 'DELETE' }));
  act('#sk-import', () => api('/api/me/simkl/import', { method: 'POST' }));
  const cb = $('#sk-scrobble', box);
  if (cb) cb.onchange = async () => { try { await api('/api/me/simkl', { method: 'PATCH', body: { scrobble: cb.checked } }); toast('Saved.'); } catch (err) { toast(err.message); } };
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
  const body = all.length ? `<p class="muted small">Ticked collections show as rows on this library's page, in this order. Add MDBList and Simkl lists with New collection.</p>
    <ul class="row-picks" style="list-style:none;padding:0;margin:0">${on.map((c) => row(c, true)).join('')}${off.map((c) => row(c, false)).join('')}</ul>`
    : '<p class="muted">No collections yet. Make one under Collections (hand-picked, smart, TMDB, MDBList or Simkl), then pick it here.</p>';
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
