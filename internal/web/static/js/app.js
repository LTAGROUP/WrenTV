import { api, account, local, state, saveSession, isSupabase, img, ApiError } from './api.js';
import { esc, $, $$, el, toast, icons, fmtRuntime, fmtDuration, fmtSize } from './ui.js';
import { openPlayer } from './player.js';
import * as manage from './manage.js';
import * as browse from './browse.js';
import { viewStats } from './stats.js';

const app = $('#app');
let libraries = [];

// ---------------------------------------------------------------- routing

export function go(path, replace = false) {
  if (location.pathname + location.search !== path) history[replace ? 'replaceState' : 'pushState'](null, '', path);
  render();
}
window.addEventListener('popstate', render);
document.addEventListener('click', (e) => {
  const a = e.target.closest('a[href^="/"]');
  if (!a || e.metaKey || e.ctrlKey || e.shiftKey || a.target) return;
  e.preventDefault();
  $('.shell')?.classList.remove('open');
  go(a.getAttribute('href'));
});

const routes = [
  [/^\/$/, viewHome],
  [/^\/library\/([^/]+)$/, viewLibrary],
  [/^\/library\/([^/]+)\/genres$/, browse.viewGenres],
  [/^\/library\/([^/]+)\/collections$/, browse.viewCollections],
  [/^\/genres$/, () => browse.viewGenres()],
  [/^\/genre$/, browse.viewGenre],
  [/^\/collections$/, () => browse.viewCollections()],
  [/^\/collection\/([^/]+)$/, browse.viewCollection],
  [/^\/account$/, browse.viewAccount],
  [/^\/manage\/stats$/, viewStats, true],
  [/^\/item\/([^/]+)$/, viewItem],
  [/^\/search$/, viewSearch],
  [/^\/manage\/?$/, manage.viewStatus, true],
  [/^\/manage\/libraries$/, manage.viewLibraries, true],
  [/^\/manage\/users$/, manage.viewUsers, true],
  [/^\/manage\/downloads$/, manage.viewDownloads, true],
  [/^\/manage\/settings$/, manage.viewSettings, true],
];

export const canStream = () => !!(state.server?.streaming && state.limits?.canStream);

// Titles that aren't on the server yet are added to the streaming library on first open.
document.addEventListener('click', async (e) => {
  const a = e.target.closest('[data-stream-title]');
  if (!a) return;
  e.preventDefault();
  e.stopImmediatePropagation();
  if (a.dataset.busy) return;
  a.dataset.busy = '1';
  a.classList.add('busy');
  try {
    const it = await api('/api/streaming/titles', { method: 'POST', body: { tmdbId: Number(a.dataset.streamTitle), mediaType: a.dataset.media } });
    go('/item/' + it.id);
  } catch (ex) {
    toast(ex.message);
  } finally {
    delete a.dataset.busy;
    a.classList.remove('busy');
  }
}, true);

export const isAdmin = () => state.user && (state.user.role === 'owner' || state.user.role === 'admin' || state.user.isAdmin);

async function boot() {
  try {
    state.server = await api('/api/server');
  } catch (e) {
    app.innerHTML = `<div class="center"><div class="auth"><h2>Can't reach the server</h2><p class="muted">${esc(e.message)}</p></div></div>`;
    return;
  }
  document.title = state.server.name ? `${state.server.name} · Wren TV` : 'Wren TV';
  // A session from another server or auth mode is useless here.
  const s = state.session;
  if (s && (isSupabase() ? !s.access_token : !s.token)) saveSession(null);
  render();
}

let renderSeq = 0;
async function render() {
  const seq = ++renderSeq;
  const srv = state.server;
  if (!srv) return;

  if (!state.session) {
    if (!isSupabase() && srv.setupRequired) return viewSetupOwner();
    return viewSignIn();
  }
  if (isSupabase() && srv.setupRequired) return viewClaim();
  if (!state.user) {
    try {
      await loadMe();
    } catch (e) {
      if (e.status === 401) { saveSession(null); return render(); }
      if (e.status === 409) { srv.setupRequired = true; return render(); }
      if (e.status === 403) return viewNoAccess();
      app.innerHTML = `<div class="center"><div class="auth"><h2>Something went wrong</h2><p class="muted">${esc(e.message)}</p></div></div>`;
      return;
    }
  }
  for (const [re, view, admin] of routes) {
    const m = location.pathname.match(re);
    if (!m) continue;
    if (admin && !isAdmin()) return go('/', true);
    return shell(() => view(...m.slice(1)), seq);
  }
  go('/', true);
}
export { render };

async function loadMe() {
  const me = await api('/api/me');
  state.user = me.user;
  state.libraryIds = me.libraryIds;
  state.limits = me.limits || {};
  libraries = await api('/api/libraries');
}

export async function refreshLibraries() {
  libraries = await api('/api/libraries');
  const nav = $('#lib-nav');
  if (nav) nav.innerHTML = libNav();
  return libraries;
}

export function signOut() {
  (isSupabase() ? account.signOut() : Promise.resolve(saveSession(null))).finally(() => {
    state.user = null;
    go('/');
  });
}

// ---------------------------------------------------------------- auth screens

function authCard(inner) {
  app.innerHTML = `<div class="center"><div class="auth"><div class="brand"><img class="lockup" src="/brand/wren-tv-horizontal-on-dark.svg" alt="Wren TV"></div>${inner}</div></div>`;
}

function bindForm(onSubmit) {
  const f = $('form', app);
  f.onsubmit = async (e) => {
    e.preventDefault();
    const btn = $('button[type=submit], button:not([type])', f);
    const err = $('.error', f);
    err.hidden = true;
    btn.disabled = true;
    try {
      await onSubmit(Object.fromEntries(new FormData(f)));
    } catch (ex) {
      err.textContent = ex.message;
      err.hidden = false;
    } finally {
      btn.disabled = false;
    }
  };
  $('input', f)?.focus();
}

function viewSignIn(mode = 'in') {
  const srv = state.server;
  if (!isSupabase()) {
    authCard(`<p class="muted">Sign in to <b>${esc(srv.name)}</b>.</p>
      <form><div class="field"><label>Username</label><input name="username" autocomplete="username" required></div>
      <div class="field"><label>Password</label><input name="password" type="password" autocomplete="current-password" required></div>
      <p class="error" hidden></p><button class="btn primary lg" style="width:100%">Sign in</button></form>`);
    return bindForm(async (v) => { await local.login(v.username, v.password); render(); });
  }
  const up = mode === 'up';
  authCard(`<p class="muted">${up ? 'Create your Wren TV account.' : `Sign in with your Wren TV account to use <b>${esc(srv.name)}</b>.`}</p>
    <form>${up ? '<div class="field"><label>Name</label><input name="name" autocomplete="name" required></div>' : ''}
    <div class="field"><label>Email</label><input name="email" type="email" autocomplete="email" required></div>
    <div class="field"><label>Password</label><input name="password" type="password" minlength="8" autocomplete="${up ? 'new-password' : 'current-password'}" required></div>
    <p class="error" hidden></p><button class="btn primary lg" style="width:100%">${up ? 'Create account' : 'Sign in'}</button></form>
    <p class="switch muted">${up ? 'Already have an account? <a data-sw>Sign in</a>' : 'New to Wren TV? <a data-sw>Create an account</a>'}</p>`);
  $('[data-sw]', app).onclick = () => viewSignIn(up ? 'in' : 'up');
  bindForm(async (v) => {
    if (up) {
      const signedIn = await account.signUp(v.email.trim(), v.password, v.name.trim());
      if (!signedIn) {
        authCard(`<h2 style="margin-bottom:8px">Check your email</h2><p class="muted">We sent a confirmation link to <b>${esc(v.email)}</b>. Open it, then come back and sign in.</p>
          <button class="btn primary lg" style="width:100%;margin-top:12px" id="back">Back to sign in</button>`);
        $('#back').onclick = () => viewSignIn('in');
        return;
      }
    } else {
      await account.signIn(v.email.trim(), v.password);
    }
    render();
  });
}

function viewSetupOwner() {
  authCard(`<h2 style="margin-bottom:6px">Set up ${esc(state.server.name)}</h2>
    <p class="muted">Create the owner account for this server.</p>
    <form><div class="field"><label>Name</label><input name="displayName" required></div>
    <div class="field"><label>Username</label><input name="username" autocomplete="username" required></div>
    <div class="field"><label>Password</label><input name="password" type="password" minlength="8" autocomplete="new-password" required></div>
    <p class="error" hidden></p><button class="btn primary lg" style="width:100%">Create owner</button></form>`);
  bindForm(async (v) => {
    await local.setupOwner(v.username, v.password, v.displayName);
    state.server.setupRequired = false;
    go('/manage/libraries');
  });
}

function viewClaim() {
  const email = state.session?.user?.email || '';
  authCard(`<h2 style="margin-bottom:6px">Claim ${esc(state.server.name)}</h2>
    <p class="muted">This server isn't linked to an account yet. Enter the claim code from the server log to make <b>${esc(email)}</b> its owner.</p>
    <pre class="mono" style="background:#0e0c11;padding:10px;border-radius:6px;margin:12px 0">docker logs wrentv 2>&1 | grep claim_code</pre>
    <form><div class="field"><label>Claim code</label><input name="code" class="code-input" placeholder="XXXX-XXXX" autocomplete="off" required></div>
    <p class="error" hidden></p><button class="btn primary lg" style="width:100%">Claim server</button></form>
    <p class="switch muted">Not you? <a data-out>Sign out</a></p>`);
  $('[data-out]', app).onclick = signOut;
  bindForm(async (v) => {
    await api('/api/setup/claim', { method: 'POST', body: { claimCode: v.code.trim() } });
    state.server = await api('/api/server');
    toast('Server claimed. Welcome to Wren TV!');
    go('/manage/libraries');
  });
}

function viewNoAccess() {
  const email = state.session?.user?.email || '';
  authCard(`<h2 style="margin-bottom:8px">No access</h2>
    <p class="muted"><b>${esc(email)}</b> doesn't have access to ${esc(state.server.name)}. Ask the owner to share it with this email address.</p>
    <div class="actions"><button class="btn" id="retry">Check again</button><button class="btn ghost" id="out">Sign out</button></div>`);
  $('#retry').onclick = async () => { await account.rpc('accept_invites').catch(() => {}); render(); };
  $('#out').onclick = signOut;
}

// ---------------------------------------------------------------- shell

function libNav() {
  return libraries.map((l) => `<a class="nav" href="/library/${l.id}" data-path="/library/${l.id}">${l.type === 'shows' ? icons.shows : icons.movies}<span>${esc(l.name)}</span></a>`).join('');
}

async function shell(view, seq) {
  if (!$('.shell')) {
    const u = state.user;
    const name = u?.displayName || u?.username || state.session?.user?.email || '';
    const admin = isAdmin();
    app.innerHTML = `<div class="shell">
      <aside class="sidebar">
        <a class="brand" href="/"><img class="logo" src="/brand/wren-symbol-small-on-dark.svg" alt="">${esc(state.server.name || 'Wren TV')}</a>
        <a class="nav" href="/" data-path="/">${icons.home}<span>Home</span></a>
        <a class="nav" href="/collections" data-path="/collections">${icons.stack}<span>Collections</span></a>
        <a class="nav" href="/genres" data-path="/genres">${icons.tag}<span>Genres</span></a>
        <div class="nav-title">Libraries</div>
        <div id="lib-nav">${libNav()}</div>
        ${admin ? `<div class="nav-title">Manage</div>
          <a class="nav" href="/manage" data-path="/manage">${icons.activity}<span>Dashboard</span></a>
          <a class="nav" href="/manage/stats" data-path="/manage/stats">${icons.chart}<span>Stats</span></a>
          <a class="nav" href="/manage/libraries" data-path="/manage/libraries">${icons.folder}<span>Libraries</span></a>
          <a class="nav" href="/manage/users" data-path="/manage/users">${icons.users}<span>${isSupabase() ? 'Sharing' : 'Users'}</span></a>
          ${state.server.streaming ? `<a class="nav" href="/manage/downloads" data-path="/manage/downloads">${icons.download}<span>Downloads</span></a>` : ''}
          <a class="nav" href="/manage/settings" data-path="/manage/settings">${icons.settings}<span>Settings</span></a>` : ''}
        <div class="spacer"></div>
        <div class="account"><div class="avatar">${esc((name[0] || '?').toUpperCase())}</div>
          <div style="min-width:0;flex:1"><div style="white-space:nowrap;overflow:hidden;text-overflow:ellipsis">${esc(name)}</div>
          <div class="small muted">${esc(u?.role || '')}</div></div>
          <a class="btn ghost small" href="/account" title="Your settings" aria-label="Your settings">${icons.settings}</a>
          <button class="btn ghost small" id="signout" title="Sign out">Sign out</button></div>
      </aside>
      <div class="main">
        <div class="topbar"><button class="btn ghost menu-btn" id="menu">${icons.menu}</button>
          <div class="title" id="page-title"></div>
          <form class="search" id="search-form"><input type="search" name="q" placeholder="Search" autocomplete="off"></form></div>
        <div class="page" id="page"></div>
      </div></div>`;
    $('#signout').onclick = signOut;
    $('#menu').onclick = () => $('.shell').classList.toggle('open');
    $('#search-form').onsubmit = (e) => {
      e.preventDefault();
      const q = new FormData(e.target).get('q').trim();
      if (q) go('/search?q=' + encodeURIComponent(q));
    };
  }
  for (const a of $$('.nav[data-path]')) {
    const p = a.dataset.path;
    const here = location.pathname;
    a.classList.toggle('active', p === '/' ? here === '/' : here === p || (p !== '/manage' && here.startsWith(p + '/'))
      || (p === '/genres' && here === '/genre') || (p === '/collections' && here.startsWith('/collection/')));
  }
  const page = $('#page');
  page.innerHTML = '<div class="center" style="min-height:50vh"><div class="spinner"></div></div>';
  window.scrollTo(0, 0);
  try {
    const out = await view();
    if (seq !== renderSeq) return;
    if (typeof out === 'string') page.innerHTML = out;
    else if (out instanceof Node) page.replaceChildren(out);
  } catch (e) {
    if (seq !== renderSeq) return;
    if (e instanceof ApiError && e.status === 401) { saveSession(null); state.user = null; return render(); }
    page.innerHTML = `<div class="empty"><h2>Couldn't load this page</h2><p>${esc(e.message)}</p></div>`;
  }
}

export const getLibraries = () => libraries;

export function setTitle(t) {
  const el = $('#page-title');
  if (el) el.textContent = t;
}

// ---------------------------------------------------------------- cards

export function progressBar(it) {
  const p = it.progress;
  if (!p || p.watched || !p.duration || p.position < 30) return '';
  return `<div class="bar"><i style="width:${Math.min(100, (p.position / p.duration) * 100).toFixed(1)}%"></i></div>`;
}

// Images fade in once loaded instead of popping.
export const lazyImg = (src, alt = '') => `<img loading="lazy" class="loading" onload="this.classList.remove('loading')" onerror="this.remove()" src="${src}" alt="${esc(alt)}">`;

export function posterCard(it, extra = '') {
  const title = it.kind === 'season' ? (it.seasonNumber === 0 ? 'Specials' : `Season ${it.seasonNumber}`) : it.title;
  const sub = it.kind === 'show' ? (it.childCount ? `${it.childCount} episode${it.childCount > 1 ? 's' : ''}` : it.year || '')
    : it.kind === 'season' ? (it.childCount ? `${it.childCount} episodes` : '') : it.year || '';
  const corner = it.kind === 'show' || it.kind === 'season'
    ? (it.unwatchedCount ? `<div class="badge">${it.unwatchedCount}</div>` : it.childCount ? '<div class="watched"></div>' : '')
    : it.progress?.watched ? '<div class="watched"></div>' : '';
  return `<a class="card" href="/item/${it.id}"><div class="poster">${it.posterPath ? lazyImg(img(it.posterPath), title) : `<div class="ph">${esc(title)}</div>`}${corner}${extra}${progressBar(it)}</div>
    <div class="t" title="${esc(title)}">${esc(title)}</div><div class="s">${esc(sub)}</div></a>`;
}

// titleCard shows a TMDB title: the library item when the server has it,
// otherwise a dimmed poster linking to TMDB.
export function titleCard(v, rank) {
  const r = rank ? `<span class="rank">${rank}</span>` : '';
  if (v.item) return posterCard(v.item, r);
  if (canStream()) {
    return `<a class="card" href="/item/stream" data-stream-title="${v.tmdbId}" data-media="${esc(v.mediaType)}" title="Stream ${esc(v.title)}">
      <div class="poster">${v.posterPath ? lazyImg(img(v.posterPath), v.title) : `<div class="ph">${esc(v.title)}</div>`}${r}</div>
      <div class="t">${esc(v.title)}</div><div class="s">${esc(v.year || '')}</div></a>`;
  }
  const href = `https://www.themoviedb.org/${v.mediaType === 'show' ? 'tv' : 'movie'}/${v.tmdbId}`;
  return `<a class="card ext" href="${href}" target="_blank" rel="noopener" title="Not in your library — open on TMDB">
    <div class="poster">${v.posterPath ? lazyImg(img(v.posterPath), v.title) : `<div class="ph">${esc(v.title)}</div>`}${r}<span class="tag">Not in library</span></div>
    <div class="t">${esc(v.title)}</div><div class="s">${esc(v.year || '')}</div></a>`;
}

function wideCard(it) {
  const pic = it.stillPath || it.backdropPath;
  const title = it.kind === 'episode' ? it.showTitle || it.title : it.title;
  const sub = it.kind === 'episode' ? `S${it.seasonNumber} · E${it.episodeNumber} — ${it.title}` : it.year || '';
  return `<a class="card wide" href="/item/${it.id}" data-play="${it.id}"><div class="thumb">${pic ? lazyImg(img(pic, 'w500')) : ''}
    <div class="play-ov"><span></span></div>${progressBar(it)}</div>
    <div class="t">${esc(title)}</div><div class="s">${esc(sub)}</div></a>`;
}

export const arrows = '<div class="arrows"><button type="button" data-dir="-1" aria-label="Scroll left">‹</button><button type="button" data-dir="1" aria-label="Scroll right">›</button></div>';

export function shelf(title, cards, { see, sub, id } = {}) {
  return `<section class="shelf"${id ? ` id="${id}"` : ''}><div class="shelf-head"><h2>${esc(title)}${sub ? ` <span class="sub">${esc(sub)}</span>` : ''}</h2>
    ${see ? `<a class="see" href="${see}">See all</a>` : ''}${arrows}</div><div class="row">${cards}</div></section>`;
}

function skeletonShelf(title, n = 8, id) {
  const cards = Array.from({ length: n }, () => '<div class="card"><div class="poster sk"></div><div class="sk sk-line" style="width:80%"></div><div class="sk sk-line" style="width:40%"></div></div>').join('');
  return shelf(title, cards, { id });
}

export function bindShelves(root) {
  for (const b of $$('.arrows button', root)) {
    b.onclick = () => {
      const row = $('.row', b.closest('.shelf'));
      row.scrollBy({ left: Number(b.dataset.dir) * row.clientWidth * 0.85 });
    };
  }
}

// ---------------------------------------------------------------- views

function featureHero(it) {
  const playable = it.kind === 'movie';
  const meta = [it.year, it.kind === 'show' ? 'Series' : fmtRuntime(it.runtimeMin), it.contentRating && `<span class="pill">${esc(it.contentRating)}</span>`,
    it.rating && `★ ${it.rating.toFixed(1)}`].filter(Boolean);
  return `<section class="feature"><div class="bg" style="background-image:url('${img(it.backdropPath, 'w1280')}')"></div>
    <div class="content"><div class="eyebrow">Featured from your library</div>
    ${it.logoPath ? `<img class="logo-img" src="${img(it.logoPath, 'w500')}" alt="${esc(it.title)}">` : `<h1>${esc(it.title)}</h1>`}
    <div class="meta">${meta.join('<span class="muted">·</span>')}</div>
    ${it.genres?.length ? `<div class="genres">${it.genres.slice(0, 4).map((g) => `<span>${esc(g)}</span>`).join('')}</div>` : ''}
    <p class="overview">${esc(it.overview || '')}</p>
    <div class="actions">${playable ? `<button class="btn primary lg" data-feature-play="${it.id}">▶ Play</button>` : ''}
      <a class="btn lg" href="/item/${it.id}">More info</a></div></div></section>`;
}

function pickFeatured(home) {
  const pool = [...(home.topRated || []), ...home.recentlyAdded.flatMap((s) => s.items)]
    .filter((it) => it.backdropPath && it.overview && !it.progress?.watched);
  if (!pool.length) return null;
  // Same pick for an hour, so the page doesn't shuffle on every visit.
  return pool[Math.floor(Date.now() / 3.6e6) % pool.length];
}

async function viewHome() {
  setTitle('Home');
  const home = await api('/api/home');
  if (!libraries.length) {
    return `<div class="empty"><h2>No libraries yet</h2><p>${isAdmin() ? 'Add a folder with your movies or shows to get started.' : 'The server owner hasn\'t shared any libraries with you yet.'}</p>
      ${isAdmin() ? '<a class="btn primary" href="/manage/libraries">Add a library</a>' : ''}</div>`;
  }
  const featured = pickFeatured(home);
  let html = featured ? featureHero(featured) : '';
  if (home.continueWatching.length) html += shelf('Continue Watching', home.continueWatching.map(wideCard).join(''));
  html += skeletonShelf('Trending this week', 8, 'trending');
  for (const c of home.collections || []) {
    const cards = c.items.map((it) => posterCard(it)).join('') + (c.missing || []).map((v) => titleCard(v)).join('');
    html += shelf(c.collection.name, cards, { see: `/collection/${c.collection.id}`, sub: c.collection.description ? '' : `${c.collection.itemCount} in your library` });
  }
  for (const s of home.recentlyAdded) {
    if (s.items.length) html += shelf(`Recently Added in ${s.library.name}`, s.items.map((it) => posterCard(it)).join(''), { see: `/library/${s.library.id}?sort=added` });
  }
  if (home.topRated?.length) html += shelf('Top Rated in Your Library', home.topRated.map((it) => posterCard(it)).join(''));
  for (const g of home.genres || []) html += shelf(g.genre, g.items.map((it) => posterCard(it)).join(''));
  const node = el(`<div>${html || '<div class="empty"><h2>Nothing here yet</h2><p>Your libraries are still being scanned.</p></div>'}</div>`);
  bindPlayLinks(node);
  bindShelves(node);
  $('[data-feature-play]', node)?.addEventListener('click', (e) => openPlayer(e.currentTarget.dataset.featurePlay, { resume: true, onClose: () => render() }));
  loadTrending(node);
  return node;
}

async function loadTrending(node) {
  const sec = $('#trending', node);
  let res;
  try { res = await api('/api/discover/trending'); } catch { res = null; }
  if (!res?.enabled || !res.items.length) return sec.remove();
  const have = res.items.filter((v) => v.item).length;
  const next = el(shelf('Trending this week', res.items.slice(0, 20).map((v, i) => titleCard(v, i + 1)).join(''),
    { id: 'trending', sub: have ? `${have} in your library` : 'none in your library yet' }));
  sec.replaceWith(next);
  bindShelves(next);
}

export function bindPlayLinks(root) {
  for (const a of $$('[data-play]', root)) {
    $('.play-ov', a)?.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      openPlayer(a.dataset.play, { resume: true });
    });
  }
}

const sorts = [['title', 'Title'], ['added', 'Recently added'], ['year', 'Year'], ['rating', 'Rating']];

// libraryTabs switches between a library's titles, collections and genres.
export function libraryTabs(id, active) {
  return `<nav class="tabs"><a href="/library/${id}" class="${active === 'all' ? 'active' : ''}">Library</a>
    <a href="/library/${id}/collections" class="${active === 'collections' ? 'active' : ''}">Collections</a>
    <a href="/library/${id}/genres" class="${active === 'genres' ? 'active' : ''}">Genres</a></nav>`;
}

async function viewLibrary(id) {
  const lib = libraries.find((l) => l.id === id);
  setTitle(lib?.name || 'Library');
  const qs = new URLSearchParams(location.search);
  const sort = qs.get('sort') || 'title';
  const genre = qs.get('genre') || '';
  const unwatched = qs.get('unwatched') === '1';
  const params = new URLSearchParams({ sort, limit: '10000' });
  if (genre) params.set('genre', genre);
  if (unwatched) params.set('unwatched', '1');
  const [res, genres] = await Promise.all([
    api(`/api/libraries/${id}/items?${params}`),
    api(`/api/genres?libraryId=${encodeURIComponent(id)}`).catch(() => []),
  ]);
  const noun = lib?.type === 'shows' ? 'shows' : 'movies';
  const node = el(`<div>${libraryTabs(id, 'all')}<div class="page-head"><h1>${esc(lib?.name || '')} <span class="muted small">${res.total} ${noun}</span></h1>
    <div class="toolbar"><button class="chip${unwatched ? ' on' : ''}" id="unwatched" aria-pressed="${unwatched}">Unwatched</button>
    <select id="genre" data-caption="Genre"><option value="">All</option>${genres.map((g) => `<option value="${esc(g.genre)}" ${g.genre === genre ? 'selected' : ''}>${esc(g.genre)} (${g.count})</option>`).join('')}</select>
    <select id="sort" data-caption="Sort">${sorts.map(([k, v]) => `<option value="${k}" ${k === sort ? 'selected' : ''}>${v}</option>`).join('')}</select></div></div>
    ${res.items.length ? `<div class="grid">${res.items.map((it) => posterCard(it)).join('')}</div>`
      : genre || unwatched ? '<div class="empty"><h2>Nothing matches</h2><p>Try a different genre or turn off Unwatched.</p></div>'
      : '<div class="empty"><h2>This library is empty</h2><p>Items appear here as they are scanned.</p></div>'}</div>`);
  const nav = (patch) => {
    const n = new URLSearchParams(location.search);
    for (const [k, v] of Object.entries(patch)) v ? n.set(k, v) : n.delete(k);
    go(`/library/${id}${n.toString() ? '?' + n : ''}`, true);
  };
  $('#sort', node).onchange = (e) => nav({ sort: e.target.value === 'title' ? '' : e.target.value });
  $('#genre', node).onchange = (e) => nav({ genre: e.target.value });
  $('#unwatched', node).onclick = () => nav({ unwatched: unwatched ? '' : '1' });
  return node;
}

async function viewSearch() {
  const q = new URLSearchParams(location.search).get('q') || '';
  setTitle('Search');
  const input = $('#search-form input');
  if (input) input.value = q;
  if (!q) return '<div class="empty"><h2>Search for a movie or show</h2></div>';
  const [items, more] = await Promise.all([
    api('/api/search?q=' + encodeURIComponent(q)),
    api('/api/discover/search?q=' + encodeURIComponent(q)).catch(() => ({ items: [] })),
  ]);
  // Library results first; TMDB adds whatever the server doesn't have.
  const have = new Set(items.map((it) => it.id));
  const extra = (more.items || []).filter((v) => !v.item || !have.has(v.item.id));
  const inLib = [...items, ...extra.filter((v) => v.item).map((v) => v.item)];
  const rest = extra.filter((v) => !v.item);
  const restTitle = canStream() ? 'Stream' : 'Not on this server';
  const node = el(`<div><div class="page-head"><h1>Results for “${esc(q)}”</h1></div>
    ${inLib.length ? `<h2 class="grid-title">On this server</h2><div class="grid">${inLib.map((it) => posterCard(it)).join('')}</div>` : ''}
    ${rest.length ? `<h2 class="grid-title">${restTitle}${canStream() ? ' <span class="sub">plays through the server’s debrid service</span>' : ''}</h2>
      <div class="grid">${rest.map((v) => titleCard(v)).join('')}</div>` : ''}
    ${!inLib.length && !rest.length ? '<div class="empty"><h2>No matches</h2></div>' : ''}</div>`);
  bindPlayLinks(node);
  return node;
}

// Quality badges for a file: resolution, HDR format, audio.
function badgesFor(f) {
  const b = [];
  const w = f.width || 0;
  if (w >= 3200) b.push('4K'); else if (w >= 1800) b.push('1080p'); else if (w >= 1200) b.push('720p'); else if (w) b.push('SD');
  if (f.hdr === 'DV' || f.hdr === 'DV+HDR10') b.push('Dolby Vision'); else if (f.hdr) b.push(f.hdr);
  const a = f.info?.audio?.find((x) => x.default) || f.info?.audio?.[0];
  if (a?.atmos || a?.profile?.includes('Atmos')) b.push('Atmos');
  if (a?.channels >= 6) b.push(a.channels === 8 ? '7.1' : '5.1');
  if (f.streamId) b.push('Stream');
  return b;
}

const tomato = (fresh) => fresh
  ? '<svg viewBox="0 0 20 20" aria-hidden="true"><circle cx="10" cy="11" r="7.5" fill="#E04A2A"/><path d="M7 3.5c1.2.4 2.2 1.2 3 2.3.8-1.1 1.8-1.9 3-2.3-.4 1.4-1.4 2.4-3 2.8-1.6-.4-2.6-1.4-3-2.8z" fill="#4cc38a"/></svg>'
  : '<svg viewBox="0 0 20 20" aria-hidden="true"><path d="M10 2l2 4 4-1-1 4 4 2-4 2 1 4-4-1-2 4-2-4-4 1 1-4-4-2 4-2-1-4 4 1z" fill="#4cc38a"/></svg>';

function fmtVotes(n) {
  if (!n) return '';
  return n >= 1e6 ? (n / 1e6).toFixed(1) + 'M' : n >= 1e3 ? Math.round(n / 1e3) + 'K' : String(n);
}

function ratingsHtml(r) {
  const out = [];
  if (r.rottenTomatoes) out.push(`<span class="rating" title="Rotten Tomatoes (critics)">${tomato(r.rottenTomatoes >= 60)}${r.rottenTomatoes}%</span>`);
  if (r.imdb) out.push(`<span class="rating" title="IMDb"><span class="src imdb">IMDb</span>${r.imdb.toFixed(1)}${r.imdbVotes ? ` <span class="votes">${fmtVotes(r.imdbVotes)}</span>` : ''}</span>`);
  if (r.metacritic) out.push(`<span class="rating" title="Metacritic"><span class="src mc">MC</span>${r.metacritic}</span>`);
  if (r.tmdb) out.push(`<span class="rating" title="TMDB users"><span class="src tmdb">TMDB</span>${Math.round(r.tmdb * 10)}%</span>`);
  return out.join('');
}

function personCard(c) {
  const initials = c.name.split(/\s+/).map((w) => w[0]).slice(0, 2).join('');
  return `<div class="person"><div class="face">${c.profilePath ? lazyImg(img(c.profilePath, 'w185'), c.name) : esc(initials)}</div>
    <div class="n">${esc(c.name)}</div>${c.character ? `<div class="c">${esc(c.character)}</div>` : ''}</div>`;
}

// YouTube refuses embeds that arrive without a Referer (error 153), and the
// app's same-origin referrer policy strips it, so the iframe sends its origin.
function openTrailer(t) {
  const key = encodeURIComponent(t.youtubeKey);
  const q = new URLSearchParams({ autoplay: '1', rel: '0', playsinline: '1', origin: location.origin });
  const bg = el(`<div class="modal-bg"><div style="display:grid;gap:10px;justify-items:end"><div class="trailer"><iframe allow="autoplay; encrypted-media; picture-in-picture; fullscreen" allowfullscreen
    referrerpolicy="strict-origin-when-cross-origin" src="https://www.youtube-nocookie.com/embed/${key}?${q}" title="${esc(t.name)}"></iframe></div>
    <a class="small muted" href="https://www.youtube.com/watch?v=${key}" target="_blank" rel="noopener">Open on YouTube ↗</a></div></div>`);
  const close = () => { bg.remove(); document.removeEventListener('keydown', onKey); };
  const onKey = (e) => e.key === 'Escape' && close();
  bg.addEventListener('mousedown', (e) => e.target === bg && close());
  document.addEventListener('keydown', onKey);
  document.body.append(bg);
}

async function viewItem(id) {
  const d = await api('/api/items/' + id);
  const it = d.item;
  const show = d.show;
  setTitle(it.kind === 'episode' || it.kind === 'season' ? show?.title || it.title : it.title);
  const backdrop = it.backdropPath || show?.backdropPath || it.stillPath;
  const poster = it.kind === 'episode' ? null : it.posterPath || show?.posterPath;
  const logo = it.kind === 'movie' || it.kind === 'show' ? it.logoPath : null;
  const heading = it.kind === 'season' ? (it.seasonNumber === 0 ? 'Specials' : `Season ${it.seasonNumber}`) : it.title;
  const meta = [];
  if (it.kind === 'episode') meta.push(`S${it.seasonNumber} · E${it.episodeNumber}`);
  if (it.year) meta.push(it.year);
  if (it.runtimeMin) meta.push(fmtRuntime(it.runtimeMin));
  if (it.kind === 'show' && d.children?.length) meta.push(`${d.children.length} season${d.children.length > 1 ? 's' : ''}`);
  if (it.contentRating) meta.push(`<span class="pill">${esc(it.contentRating)}</span>`);
  const file = d.files?.[0];
  const badges = file ? badgesFor(file).filter((b) => b !== 'Stream') : [];
  if (badges.length) meta.push(badges.map((b, i) => `<span class="qbadge${i ? ' dim' : ''}">${esc(b)}</span>`).join(' '));
  const genres = it.genres?.length ? it.genres : show?.genres || [];

  const playable = it.kind === 'movie' || it.kind === 'episode';
  const resumeAt = playable && it.progress && !it.progress.watched && it.progress.position > 30 ? it.progress.position : 0;
  let actions = '';
  if (playable) {
    actions += resumeAt
      ? `<button class="btn primary lg" data-act="resume">▶ Resume ${fmtDuration(resumeAt)}</button><button class="btn lg" data-act="start">Play from start</button>`
      : `<button class="btn primary lg" data-act="start">▶ Play</button>`;
  } else if (d.nextEpisode) {
    const n = d.nextEpisode;
    actions += `<button class="btn primary lg" data-act="next">▶ ${n.progress?.position > 30 ? 'Resume' : 'Play'} S${n.seasonNumber} · E${n.episodeNumber}</button>`;
  }
  // More than one file: pick the version before playing ("Auto" lets the
  // server choose, e.g. 1080p over transcoding 4K at a lower quality setting).
  if (playable && d.files?.length > 1) {
    actions += `<select data-version data-caption="Version" class="version-pick"><option value="">Auto</option>${d.files.map((f) => `<option value="${esc(f.id)}">${esc(f.label || f.fileName)}</option>`).join('')}</select>`;
  }
  actions += `<button class="btn lg" data-act="trailer" hidden>Trailer</button>`;
  if (playable) actions += `<button class="btn lg ghost" data-act="watched">${it.progress?.watched ? 'Mark unwatched' : 'Mark watched'}</button>`;
  if (playable && d.canDownload && (d.files?.length || d.streaming)) actions += `<button class="btn lg ghost" data-act="download" title="Save to this device">${icons.download} Download</button>`;
  if (isAdmin() && d.streaming) {
    actions += `<button class="btn lg ghost" data-act="serverdl" title="Save as a file in your library">Download to server</button>`;
    if (it.kind !== 'show' || d.children?.length) actions += `<button class="btn lg ghost" data-act="restream" title="Forget saved streams and look again">Find new streams</button>`;
  }
  if (isAdmin() && (it.kind === 'movie' || it.kind === 'show')) {
    actions += `<button class="btn lg ghost" data-act="collect">${icons.plus} Collection</button><button class="btn lg ghost" data-act="match">Fix match</button>`;
  }

  const crumbs = it.kind === 'season' || it.kind === 'episode'
    ? `<div class="small" style="margin-bottom:6px"><a href="/item/${it.showId}">${esc(show?.title || '')}</a>${it.kind === 'episode' && it.parentId ? ` › <a href="/item/${it.parentId}">Season ${it.seasonNumber}</a>` : ''}</div>`
    : '';

  let below = '';
  if (it.kind === 'show' && d.children?.length) below += shelf('Seasons', d.children.map((c) => posterCard(c)).join(''));
  if (it.kind === 'season') {
    below += `<div class="episodes">${(d.children || []).map((e) => `<a class="episode" href="/item/${e.id}" data-play="${e.id}">
      <div class="thumb">${e.stillPath ? lazyImg(img(e.stillPath, 'w300')) : ''}<div class="play-ov"><span></span></div>${progressBar(e)}${e.progress?.watched ? '<div class="watched"></div>' : ''}</div>
      <div><div style="font-weight:600">${e.episodeNumber}. ${esc(e.title)}</div>
      <div class="small muted">${[e.airDate, fmtRuntime(e.runtimeMin)].filter(Boolean).map(esc).join(' · ')}</div>
      <p class="small muted" style="margin:6px 0 0;display:-webkit-box;-webkit-line-clamp:3;-webkit-box-orient:vertical;overflow:hidden">${esc(e.overview || '')}</p></div></a>`).join('')}</div>`;
  }
  // Cast and "more like this" arrive with the extras; show placeholders.
  below += `<section class="shelf" id="cast-shelf"><div class="shelf-head"><h2>Cast</h2>${arrows}</div><div class="cast">${Array.from({ length: 8 }, () =>
    '<div class="person"><div class="face sk"></div><div class="sk sk-line" style="width:70%;margin:0 auto"></div></div>').join('')}</div></section>`;
  below += skeletonShelf('More Like This', 8, 'more');
  if (d.files?.length) {
    below += `<div class="files"><h2 style="font-size:16px">Files</h2>${d.files.map((f) => `<div class="file">
      <div style="display:flex;gap:8px;flex-wrap:wrap;align-items:center">${badgesFor(f).map((b) => `<span class="qbadge dim">${esc(b)}</span>`).join('') || `<b>${esc(f.container)}</b>`}
      ${f.release ? `<span class="small muted">${esc(f.release)}</span>` : ''}
      ${f.info?.chapters?.length ? `<span class="small muted">${f.info.chapters.length} chapters</span>` : ''}
      </div>
      <code class="muted">${esc(f.fileName)}</code>
      ${f.info ? `<div class="small muted" style="margin-top:4px">${[f.videoCodec?.toUpperCase(), f.width && `${f.width}×${f.height}`, ...(f.info.audio || []).slice(0, 3).map((a) => [a.codec?.toUpperCase(), a.language].filter(Boolean).join(' '))].filter(Boolean).map(esc).join(' · ')}</div>` : ''}</div>`).join('')}</div>`;
  }

  const node = el(`<div><div class="hero"><div class="bg" ${backdrop ? `style="background-image:url('${img(backdrop, 'w1280')}')"` : ''}></div>
    <div class="content">${poster ? `<div class="poster"><img src="${img(poster, 'w500')}" alt=""></div>` : ''}
    <div>${crumbs}${logo ? `<img class="logo-img" src="${img(logo, 'w500')}" alt="${esc(heading)}">` : `<h1>${esc(heading)}</h1>`}
    ${it.tagline ? `<div class="muted" style="font-style:italic">${esc(it.tagline)}</div>` : ''}
    <div class="meta">${meta.join('<span class="muted">·</span>')}</div>
    <div class="ratings" id="ratings">${ratingsHtml({ tmdb: it.rating || show?.rating })}</div>
    ${genres.length ? `<div class="genres">${genres.map((g) => `<a href="/genre?g=${encodeURIComponent(g)}">${esc(g)}</a>`).join('')}</div>` : ''}
    <p class="overview">${esc(it.overview || '')}</p><div class="actions">${actions}</div></div></div></div>${below}</div>`);

  const act = (name, fn) => $(`[data-act=${name}]`, node)?.addEventListener('click', fn);
  const version = () => $('[data-version]', node)?.value || undefined;
  act('resume', () => openPlayer(it.id, { start: resumeAt, fileId: version(), onClose: () => render() }));
  act('start', () => openPlayer(it.id, { start: 0, fileId: version(), onClose: () => render() }));
  act('collect', () => browse.addToCollection(it));
  act('next', () => openPlayer(d.nextEpisode.id, { resume: true, onClose: () => render() }));
  act('watched', async () => {
    await api(`/api/items/${it.id}/watched`, { method: 'POST', body: { watched: !it.progress?.watched } });
    render();
  });
  act('match', () => manage.fixMatch(it));
  act('download', (e) => downloadToDevice(it, e.currentTarget, version()));
  act('serverdl', async (e) => {
    const b = e.currentTarget;
    b.disabled = true;
    try {
      const r = await api(`/api/items/${it.id}/server-download`, { method: 'POST' });
      toast(r.queued > 1 ? `${r.queued} episodes queued for download` : 'Queued for download');
    } catch (ex) { toast(ex.message); }
    b.disabled = false;
  });
  act('restream', async () => {
    if (!(await manage.confirmForget(it))) return;
    await api(`/api/items/${it.id}/streams`, { method: 'DELETE' });
    toast('Saved streams cleared. The next play looks again.');
    render();
  });
  bindPlayLinks(node);
  bindShelves(node);
  loadExtras(it, node);
  return node;
}

// Device downloads: the server hands out a short-lived link to the original
// file (or stream) and the browser saves it.
async function downloadToDevice(it, btn, fileId) {
  btn.disabled = true;
  const label = btn.innerHTML;
  try {
    for (let i = 0; ; i++) {
      const r = await api(`/api/items/${it.id}/download`, { method: 'POST', body: { fileId } });
      if (r.status !== 'pending') {
        const a = document.createElement('a');
        a.href = r.url;
        a.download = r.fileName || '';
        document.body.append(a);
        a.click();
        a.remove();
        toast(`Downloading ${r.fileName}${r.size ? ` (${fmtSize(r.size)})` : ''}`);
        break;
      }
      if (i >= 20) throw new Error('No stream found yet. Try again in a minute.');
      btn.textContent = r.message || 'Finding a stream…';
      await new Promise((res) => setTimeout(res, 3000));
    }
  } catch (ex) {
    toast(ex.message);
  } finally {
    btn.innerHTML = label;
    btn.disabled = false;
  }
}

async function loadExtras(it, node) {
  let x;
  try { x = await api(`/api/items/${it.id}/extras`); } catch { x = null; }
  const cast = $('#cast-shelf', node);
  const more = $('#more', node);
  if (!x) { cast.remove(); more.remove(); return; }
  const r = $('#ratings', node);
  r.innerHTML = ratingsHtml(x.ratings);
  if (!x.omdb && isAdmin()) r.insertAdjacentHTML('beforeend', '<span class="hint">Add an OMDb key in <a href="/manage/settings">Settings</a> for IMDb and Rotten Tomatoes scores.</span>');
  if (x.trailer) {
    const b = $('[data-act=trailer]', node);
    b.hidden = false;
    b.onclick = () => openTrailer(x.trailer);
  }
  if (x.cast.length) $('.cast', cast).innerHTML = x.cast.map(personCard).join('');
  else cast.remove();
  if (x.moreLikeThis.length) {
    const next = el(shelf('More Like This', x.moreLikeThis.map((v) => titleCard(v)).join(''), { id: 'more' }));
    more.replaceWith(next);
    bindShelves(next);
  } else more.remove();
  // Cast scrolls with the shelf arrows too.
  for (const b of $$('#cast-shelf .arrows button', node)) b.onclick = () => { const c = $('.cast', node); c.scrollBy({ left: Number(b.dataset.dir) * c.clientWidth * 0.85 }); };
}

boot();
