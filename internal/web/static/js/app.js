import { api, account, local, state, saveSession, isSupabase, usingAccount, img, ApiError } from './api.js';
import { esc, $, $$, el, toast, icons, fmtRuntime, fmtDuration, fmtSize } from './ui.js';
import { openPlayer } from './player.js';
import * as manage from './manage.js';
import * as browse from './browse.js';
import * as ambient from './ambient.js';
import * as layouts from './layouts.js';
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
  [/^\/$/, () => layouts.viewLayoutPage('home', 'Home', viewHome)],
  [/^\/watchlist$/, viewWatchlist],
  [/^\/movies$/, () => layouts.viewLayoutPage('movies', 'Movies', () => viewCatalog('movie'), '/movies/all')],
  [/^\/shows$/, () => layouts.viewLayoutPage('shows', 'TV Shows', () => viewCatalog('show'), '/shows/all')],
  [/^\/movies\/all$/, () => viewCatalog('movie')],
  [/^\/shows\/all$/, () => viewCatalog('show')],
  [/^\/library\/([^/]+)$/, (id) => location.search ? viewLibrary(id)
    : layouts.viewLayoutPage(`library:${id}`, libraries.find((l) => l.id === id)?.name || 'Library', () => viewLibrary(id), `/library/${id}/all`)],
  [/^\/library\/([^/]+)\/all$/, viewLibrary],
  [/^\/hub\/([^/]+)$/, (id) => layouts.viewHub(decodeURIComponent(id))],
  [/^\/layouts$/, layouts.viewLayouts],
  [/^\/layouts\/([^/]+)$/, layouts.viewLayoutEditor],
  [/^\/library\/([^/]+)\/genres$/, browse.viewGenres],
  [/^\/library\/([^/]+)\/collections$/, browse.viewCollections],
  [/^\/genres$/, () => browse.viewGenres()],
  [/^\/genre$/, browse.viewGenre],
  [/^\/collections$/, () => browse.viewCollections()],
  [/^\/collection\/([^/]+)$/, browse.viewCollection],
  [/^\/calendar$/, browse.viewCalendar],
  [/^\/person\/(\d+)$/, browse.viewPerson],
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
    if (a.dataset.streamPlay && it.kind === 'movie') openPlayer(it.id, { resume: true });
    else go('/item/' + it.id);
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
  setPosterSize(posterSize());
  // A session from another server or auth mode is useless here.
  const s = state.session;
  if (s && !s.token && !(isSupabase() && s.access_token)) saveSession(null);
  render();
}

let renderSeq = 0;
async function render() {
  const seq = ++renderSeq;
  const srv = state.server;
  if (!srv) return;

  if (!state.session) {
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
  // A TV's sign-in code (from its QR code) is approved on its own page.
  if (location.pathname === '/link') return viewLink();
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
  // Poster size follows the account; this browser's copy paints the first frame.
  api('/api/me/prefs').then((p) => p.posterSize && setPosterSize(p.posterSize)).catch(() => {});
  libraries = await api('/api/libraries');
}

export async function refreshLibraries() {
  libraries = await api('/api/libraries');
  const nav = $('#lib-nav');
  if (nav) nav.innerHTML = libNav();
  return libraries;
}

export function signOut() {
  (usingAccount() ? account.signOut() : Promise.resolve(saveSession(null))).finally(() => {
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
  if (!isSupabase() || mode === 'local') {
    authCard(`<p class="muted">${location.pathname === '/link' ? 'Sign in to approve your TV.' : `Sign in to <b>${esc(srv.name)}</b>${isSupabase() ? ' with the username the server admin gave you' : ''}.`}</p>
      <form><div class="field"><label>Username</label><input name="username" autocomplete="username" required></div>
      <div class="field"><label>Password</label><input name="password" type="password" autocomplete="current-password" required></div>
      <p class="error" hidden></p><button class="btn primary lg" style="width:100%">Sign in</button></form>
      ${isSupabase() ? '<p class="switch muted">Have a Wren TV account? <a data-acct>Sign in with it</a></p>' : ''}`);
    if (isSupabase()) $('[data-acct]', app).onclick = () => viewSignIn('in');
    return bindForm(async (v) => { await local.login(v.username, v.password); render(); });
  }
  const up = mode === 'up';
  const linking = location.pathname === '/link';
  authCard(`<p class="muted">${up ? 'Create your Wren TV account.' : linking ? 'Sign in with your Wren TV account to sign in your TV.' : `Sign in with your Wren TV account to use <b>${esc(srv.name)}</b>.`}</p>
    <form>${up ? '<div class="field"><label>Name</label><input name="name" autocomplete="name" required></div>' : ''}
    <div class="field"><label>Email</label><input name="email" type="email" autocomplete="email" required></div>
    <div class="field"><label>Password</label><input name="password" type="password" minlength="8" autocomplete="${up ? 'new-password' : 'current-password'}" required></div>
    <p class="error" hidden></p><button class="btn primary lg" style="width:100%">${up ? 'Create account' : 'Sign in'}</button></form>
    <p class="switch muted">${up ? 'Already have an account? <a data-sw>Sign in</a>' : 'New to Wren TV? <a data-sw>Create an account</a>'}</p>
    ${srv.localAccounts ? '<p class="switch muted">Got a username from this server\'s admin? <a data-local>Sign in with it</a></p>' : ''}`);
  $('[data-sw]', app).onclick = () => viewSignIn(up ? 'in' : 'up');
  if (srv.localAccounts) $('[data-local]', app).onclick = () => viewSignIn('local');
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

// Signs a TV in as this account. A TV connected by address shows a code and a
// QR code of /link?code=…; one that doesn't know a server yet shows an account
// code (watch.wrentv.com/link), which this page also takes when this server
// uses Wren TV accounts. Whoever approves here is who the TV is signed in as.
async function viewLink() {
  const params = new URLSearchParams(location.search);
  const code = (params.get('code') || '').toUpperCase().replace(/[^A-Z0-9]/g, '');
  const who = state.user?.displayName || state.user?.username || state.session?.user?.email || '';
  const pretty = (c) => c.length === 8 ? c.slice(0, 4) + '-' + c.slice(4) : c;
  if (!code) {
    authCard(`<h2 style="margin-bottom:6px">Sign in a TV</h2>
      <p class="muted">Enter the code shown on your TV.</p>
      <form><div class="field"><label>Code</label><input name="code" class="code-input" placeholder="XXXX-XXXX" autocomplete="off" autocapitalize="characters" required></div>
      <p class="error" hidden></p><button class="btn primary lg" style="width:100%">Continue</button></form>`);
    return bindForm(async (v) => go('/link?code=' + encodeURIComponent(v.code.trim()), true));
  }
  let info;
  let viaAccount = false;
  let accountErr;
  if (usingAccount()) {
    try {
      const d = await account.fn('link-device', { code, check: true });
      info = { device: d.deviceName || 'Your TV' };
      viaAccount = true;
    } catch (e) {
      accountErr = e;
    }
  }
  try {
    info ??= await api('/api/pair/' + code);
  } catch (ex) {
    const e = accountErr || ex;
    authCard(`<h2 style="margin-bottom:8px">Code expired</h2><p class="muted">${esc(e.message)}</p>
      <div class="actions"><button class="btn" id="other">Enter a code</button><a class="btn ghost" href="/">Open Wren TV</a></div>`);
    $('#other').onclick = () => go('/link', true);
    return;
  }
  authCard(`<h2 style="margin-bottom:6px">Sign in ${esc(info.device)}?</h2>
    <p class="muted">${esc(info.device)} will be signed in ${viaAccount ? `to your Wren TV account (<b>${esc(state.session?.user?.email || who)}</b>) and open your servers` : `to <b>${esc(state.server.name)}</b> as <b>${esc(who)}</b>`}. Only approve a code you see on your own TV.</p>
    <p class="mono" style="font-size:22px;letter-spacing:3px;text-align:center;margin:14px 0">${esc(pretty(code))}</p>
    <p class="error" hidden></p>
    <div class="actions"><button class="btn primary lg" id="approve" style="flex:1">Sign in TV</button><a class="btn ghost lg" href="/">Not now</a></div>
    <p class="switch muted">Not you? <a data-out>Sign out</a></p>`);
  // Signing out stays on this page, so another account can approve.
  $('[data-out]', app).onclick = () => (usingAccount() ? account.signOut() : Promise.resolve(saveSession(null))).finally(() => { state.user = null; render(); });
  $('#approve').onclick = async (e) => {
    e.target.disabled = true;
    try {
      if (viaAccount) await account.fn('link-device', { code });
      else await api('/api/pair/' + code + '/approve', { method: 'POST' });
      authCard(`<h2 style="margin-bottom:8px">You're all set</h2><p class="muted">${esc(info.device)} is signed in as <b>${esc(who)}</b>. You can close this page.</p>
        <div class="actions"><a class="btn primary lg" href="/" style="flex:1">Open Wren TV</a></div>`);
    } catch (ex) {
      const err = $('.error', app);
      err.textContent = ex.message;
      err.hidden = false;
      e.target.disabled = false;
    }
  };
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
        <a class="nav" href="/watchlist" data-path="/watchlist">${icons.bookmark}<span>Watchlist</span></a>
        ${canStream() ? `<a class="nav" href="/movies" data-path="/movies">${icons.movies}<span>Movies</span></a>
          <a class="nav" href="/shows" data-path="/shows">${icons.shows}<span>TV Shows</span></a>
          <a class="nav" href="/collections" data-path="/collections">${icons.stack}<span>Collections</span></a>
          <a class="nav" href="/calendar" data-path="/calendar">${icons.calendar}<span>Calendar</span></a>`
        : `<a class="nav" href="/collections" data-path="/collections">${icons.stack}<span>Collections</span></a>
          ${libraries.some((l) => l.type === 'shows') ? `<a class="nav" href="/calendar" data-path="/calendar">${icons.calendar}<span>Calendar</span></a>` : ''}
          <div class="nav-title">Libraries</div>
          <div id="lib-nav">${libNav()}</div>`}
        ${admin ? `<div class="nav-title">Manage</div>
          <a class="nav" href="/manage" data-path="/manage">${icons.activity}<span>Dashboard</span></a>
          <a class="nav" href="/manage/stats" data-path="/manage/stats">${icons.chart}<span>Stats</span></a>
          <a class="nav" href="/manage/libraries" data-path="/manage/libraries">${icons.folder}<span>Libraries</span></a>
          <a class="nav" href="/manage/users" data-path="/manage/users">${icons.users}<span>${isSupabase() ? 'Sharing' : 'Users'}</span></a>
          ${state.server.streaming ? `<a class="nav" href="/manage/downloads" data-path="/manage/downloads">${icons.download}<span>Downloads</span></a>` : ''}
          <a class="nav" href="/manage/settings" data-path="/manage/settings">${icons.settings}<span>Settings</span></a>` : ''}
        <div class="spacer"></div>
        <a class="nav" href="/layouts" data-path="/layouts">${icons.stack}<span>Layouts</span></a>
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
    ambient.mount($('.main'));
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
  ambient.route(location.pathname);
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

// setPosterSize scales posters and tiles: small, medium or large. The account
// keeps the choice; localStorage remembers it for the next page load.
export const posterSize = () => { try { return localStorage.getItem('wren.posterSize') || 'medium'; } catch { return 'medium'; } };
export function setPosterSize(size) {
  try { size && size !== 'medium' ? localStorage.setItem('wren.posterSize', size) : localStorage.removeItem('wren.posterSize'); } catch {}
  document.documentElement.style.setProperty('--ps', { small: '.85', large: '1.25' }[size] || '1');
}

// bd marks a card's backdrop for the page's ambient artwork.
export const bd = (path) => path ? ` data-bd="${esc(path)}"` : '';

export function posterCard(it, extra = '') {
  const title = it.kind === 'season' ? (it.seasonNumber === 0 ? 'Specials' : `Season ${it.seasonNumber}`) : it.title;
  const sub = it.kind === 'show' ? (it.childCount ? `${it.childCount} episode${it.childCount > 1 ? 's' : ''}` : it.year || '')
    : it.kind === 'season' ? (it.childCount ? `${it.childCount} episodes` : '') : it.year || '';
  const corner = it.kind === 'show' || it.kind === 'season'
    ? (it.unwatchedCount ? `<div class="badge">${it.unwatchedCount}</div>` : it.childCount ? '<div class="watched"></div>' : '')
    : it.progress?.watched ? '<div class="watched"></div>' : '';
  return `<a class="card" href="/item/${it.id}"${bd(it.backdropPath)}><div class="poster">${it.posterPath ? lazyImg(img(it.posterPath), title) : `<div class="ph">${esc(title)}</div>`}${corner}${extra}${progressBar(it)}</div>
    <div class="t" title="${esc(title)}">${esc(title)}</div><div class="s">${esc(sub)}</div></a>`;
}

// titleCard shows a TMDB title: the library item when the server has it,
// otherwise a dimmed poster linking to TMDB.
export function titleCard(v, rank) {
  const r = rank ? `<span class="rank">${rank}</span>` : '';
  if (v.item) return posterCard(v.item, r);
  if (canStream()) {
    return `<a class="card" href="/item/stream" data-stream-title="${v.tmdbId}" data-media="${esc(v.mediaType)}"${bd(v.backdropPath)}>
      <div class="poster">${v.posterPath ? lazyImg(img(v.posterPath), v.title) : `<div class="ph">${esc(v.title)}</div>`}${v.watched ? '<div class="watched"></div>' : ''}${r}</div>
      <div class="t">${esc(v.title)}</div><div class="s">${esc(v.year || '')}</div></a>`;
  }
  const href = `https://www.themoviedb.org/${v.mediaType === 'show' ? 'tv' : 'movie'}/${v.tmdbId}`;
  return `<a class="card ext" href="${href}" target="_blank" rel="noopener" title="Not in your library — open on TMDB"${bd(v.backdropPath)}>
    <div class="poster">${v.posterPath ? lazyImg(img(v.posterPath), v.title) : `<div class="ph">${esc(v.title)}</div>`}${v.watched ? '<div class="watched"></div>' : ''}${r}<span class="tag">Not in library</span></div>
    <div class="t">${esc(v.title)}</div><div class="s">${esc(v.year || '')}</div></a>`;
}

// "52m left" for something partly watched.
export function timeLeft(p) {
  const left = p ? p.duration - p.position : 0;
  return p && p.duration > 0 && left > 60 && !p.watched ? `${fmtRuntime(Math.max(1, Math.round(left / 60)))} left` : '';
}

export function wideCard(it) {
  const pic = it.stillPath || it.backdropPath;
  const title = it.kind === 'episode' ? it.showTitle || it.title : it.title;
  const sub = [it.kind === 'episode' ? `S${it.seasonNumber} · E${it.episodeNumber} — ${it.title}` : it.year, timeLeft(it.progress)].filter(Boolean).join(' · ');
  return `<a class="card wide" href="/item/${it.id}" data-play="${it.id}"${bd(it.backdropPath)}><div class="thumb">${pic ? lazyImg(img(pic, 'w500')) : ''}
    <div class="play-ov"><span></span></div>${progressBar(it)}</div>
    <div class="t">${esc(title)}</div><div class="s">${esc(sub)}</div></a>`;
}

export const arrows = '<div class="arrows"><button type="button" data-dir="-1" aria-label="Scroll left">‹</button><button type="button" data-dir="1" aria-label="Scroll right">›</button></div>';

export function shelf(title, cards, { see, seeLabel = 'See all', sub, id } = {}) {
  return `<section class="shelf"${id ? ` id="${id}"` : ''}><div class="shelf-head"><h2>${esc(title)}${sub ? ` <span class="sub">${esc(sub)}</span>` : ''}</h2>
    ${see ? `<a class="see" href="${see}">${esc(seeLabel)}</a>` : ''}${arrows}</div><div class="row">${cards}</div></section>`;
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

export function featureHero(it) {
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
  if (canStream()) return viewDiscoverHome();
  const home = await api('/api/home');
  if (!libraries.length) {
    return `<div class="empty"><h2>No libraries yet</h2><p>${isAdmin() ? 'Add a folder with your movies or shows to get started.' : 'The server owner hasn\'t shared any libraries with you yet.'}</p>
      ${isAdmin() ? '<a class="btn primary" href="/manage/libraries">Add a library</a>' : ''}</div>`;
  }
  const featured = pickFeatured(home);
  let html = featured ? featureHero(featured) : '';
  if (home.continueWatching.length) html += shelf('Continue Watching', home.continueWatching.map(wideCard).join(''));
  html += watchlistShelf(home.watchlist);
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

// viewDiscoverHome is the home page when the server streams: everything
// on TMDB, in rows, not just what the libraries hold.
async function viewDiscoverHome() {
  const home = await api('/api/discover/home');
  if (!home.enabled) {
    return `<div class="empty"><h2>Discovery needs TMDB</h2><p>${isAdmin() ? 'Add a TMDB API key in Settings to fill the home page.' : 'The server owner hasn\'t set up TMDB yet.'}</p>
      ${isAdmin() ? '<a class="btn primary" href="/manage/settings">Open settings</a>' : ''}</div>`;
  }
  let html = home.hero.length ? discoverHero(home.hero) : '';
  if (home.continueWatching.length) html += shelf('Continue Watching', home.continueWatching.map(wideCard).join(''));
  html += watchlistShelf(home.watchlist);
  home.rows.forEach((row, i) => {
    if (i === 1 && home.recentlyWatched.length) html += shelf('Recently Watched', home.recentlyWatched.map((it) => posterCard(it)).join(''));
    html += shelf(row.title, row.items.map((v, n) => titleCard(v, row.id === 'trending' && n < 10 ? n + 1 : 0)).join(''),
      row.collectionId ? { see: `/collection/${row.collectionId}` } : {});
  });
  if (home.rows.length < 2 && home.recentlyWatched.length) html += shelf('Recently Watched', home.recentlyWatched.map((it) => posterCard(it)).join(''));
  const node = el(`<div class="discover">${html || '<div class="empty"><h2>Nothing to show</h2><p>TMDB didn\'t answer. Try again in a moment.</p></div>'}</div>`);
  bindPlayLinks(node);
  bindShelves(node);
  bindHero(node, home.hero);
  return node;
}

// watchlistShelf is the home row of what the viewer saved for later.
const watchlistShelf = (items) => items?.length ? shelf('My Watchlist', items.map((it) => posterCard(it)).join(''), { see: '/watchlist' }) : '';

// viewWatchlist is the movies and shows the viewer saved for later, newest
// added first.
async function viewWatchlist() {
  setTitle('Watchlist');
  const all = await api('/api/watchlist');
  const showWatched = new URLSearchParams(location.search).get('watched') === '1';
  const items = showWatched ? all : all.filter((it) => !isWatched(it));
  const node = el(`<div><div class="page-head"><h1>Watchlist <span class="muted small">${items.length} title${items.length === 1 ? '' : 's'}</span></h1>
    <div class="toolbar"><button class="chip${showWatched ? ' on' : ''}" id="show-watched" aria-pressed="${showWatched}">Show watched</button></div></div>
    ${items.length ? `<div class="grid">${items.map((it) => posterCard(it)).join('')}</div>`
      : `<div class="empty"><h2>${all.length ? 'All watched' : 'Your watchlist is empty'}</h2><p>${all.length ? 'Turn on Show watched to see them.' : 'Add movies and shows from their pages to find them here.'}</p></div>`}</div>`);
  $('#show-watched', node).onclick = () => go('/watchlist' + (showWatched ? '' : '?watched=1'), true);
  return node;
}

const catalogSorts = [['popular', 'Popular'], ['top_rated', 'Top rated'], ['new', 'Newest']];

// viewCatalog is Movies or TV Shows when the server streams: TMDB's whole
// catalog, loading more as you scroll.
async function viewCatalog(type) {
  const label = type === 'show' ? 'TV Shows' : 'Movies';
  setTitle(label);
  const qs = new URLSearchParams(location.search);
  const sort = qs.get('sort') || 'popular';
  const genre = qs.get('genre') || '';
  const query = (page) => `/api/discover/browse?${new URLSearchParams({ type, sort, genre, page: String(page) })}`;
  const first = await api(query(1));
  if (!first.enabled) {
    return `<div class="empty"><h2>${label} need TMDB</h2><p>${isAdmin() ? 'Add a TMDB API key in Settings.' : 'The server owner hasn\'t set up TMDB yet.'}</p></div>`;
  }
  const node = el(`<div><div class="page-head"><h1>${label}</h1>
    <div class="toolbar"><select id="genre" data-caption="Genre"><option value="">All</option>${first.genres.map((g) => `<option value="${g.id}" ${String(g.id) === genre ? 'selected' : ''}>${esc(g.name)}</option>`).join('')}</select>
    <select id="sort" data-caption="Sort">${catalogSorts.map(([k, v]) => `<option value="${k}" ${k === sort ? 'selected' : ''}>${v}</option>`).join('')}</select></div></div>
    <div class="grid">${first.items.map((v) => titleCard(v)).join('')}</div>
    ${first.items.length ? '' : '<div class="empty"><h2>Nothing matches</h2></div>'}<div class="center" id="more" style="min-height:80px"></div></div>`);
  const nav = (patch) => {
    const n = new URLSearchParams(location.search);
    for (const [k, v] of Object.entries(patch)) v ? n.set(k, v) : n.delete(k);
    go(`/${type === 'show' ? 'shows' : 'movies'}${n.toString() ? '?' + n : ''}`, true);
  };
  $('#sort', node).onchange = (e) => nav({ sort: e.target.value === 'popular' ? '' : e.target.value });
  $('#genre', node).onchange = (e) => nav({ genre: e.target.value });
  // Load the next page when the bottom comes into view.
  let page = 1;
  let loading = false;
  const grid = $('.grid', node);
  const more = $('#more', node);
  const seen = new Set(first.items.map((v) => v.mediaType + v.tmdbId));
  const io = new IntersectionObserver(async (entries) => {
    if (!entries[0].isIntersecting || loading || page >= first.totalPages) return;
    if (!node.isConnected) return io.disconnect();
    loading = true;
    more.innerHTML = '<div class="spinner"></div>';
    try {
      const res = await api(query(page + 1));
      page = res.page;
      const fresh = res.items.filter((v) => !seen.has(v.mediaType + v.tmdbId));
      for (const v of fresh) seen.add(v.mediaType + v.tmdbId);
      grid.insertAdjacentHTML('beforeend', fresh.map((v) => titleCard(v)).join(''));
    } catch (e) {
      toast(e.message);
    } finally {
      loading = false;
      more.innerHTML = '';
      // Still in view (a short page): keep going.
      if (node.isConnected && more.getBoundingClientRect().top < innerHeight + 400) { io.unobserve(more); io.observe(more); }
    }
  }, { rootMargin: '600px' });
  if (first.totalPages > 1) io.observe(more);
  return node;
}

// The rotating banner of this week's trending titles, like the apps.
export function discoverHero(titles) {
  return `<section class="feature carousel">${titles.map((v, i) => `<div class="bg${i ? '' : ' on'}" data-bg="${img(v.backdropPath, 'w1280')}"></div>`).join('')}
    <div class="content">${heroContent(titles, 0)}</div></section>`;
}

function heroContent(titles, i) {
  const v = titles[i];
  const it = v.item;
  const meta = [v.year, v.mediaType === 'show' ? 'Series' : 'Film', v.rating && `★ ${v.rating.toFixed(1)}`].filter(Boolean);
  const stream = `data-stream-title="${v.tmdbId}" data-media="${esc(v.mediaType)}"`;
  const resume = it?.progress && !it.progress.watched && it.progress.position > 30;
  let actions;
  if (v.mediaType === 'movie') {
    actions = (it ? `<button class="btn primary lg" data-feature-play="${it.id}">▶ ${resume ? 'Resume' : 'Play'}</button>`
      : `<a class="btn primary lg" href="/item/stream" ${stream} data-stream-play="1">▶ Play</a>`)
      + (it ? `<a class="btn lg" href="/item/${it.id}">Details</a>` : `<a class="btn lg" href="/item/stream" ${stream}>Details</a>`);
  } else {
    actions = it ? `<a class="btn primary lg" href="/item/${it.id}">▶ Watch Now</a>` : `<a class="btn primary lg" href="/item/stream" ${stream}>▶ Watch Now</a>`;
  }
  const dots = titles.length > 1 ? `<div class="dots">${titles.map((t, n) => `<button type="button" data-dot="${n}" class="${n === i ? 'on' : ''}" title="${esc(t.title)}" aria-label="${esc(t.title)}"></button>`).join('')}</div>` : '';
  return `<div class="eyebrow">Trending this week</div>
    ${it?.logoPath ? `<img class="logo-img" src="${img(it.logoPath, 'w500')}" alt="${esc(v.title)}">` : `<h1>${esc(v.title)}</h1>`}
    <div class="meta">${meta.join('<span class="muted">·</span>')}</div>
    <p class="overview">${esc(v.overview || '')}</p>
    <div class="actions">${actions}</div>${dots}`;
}

// bindHero plays from the banner and, with several titles, moves to the next
// one every 8 seconds unless the pointer is over it.
export function bindHero(node, titles) {
  const sec = $('.feature', node);
  if (!sec) return;
  sec.addEventListener('click', (e) => {
    const b = e.target.closest('[data-feature-play]');
    if (b) openPlayer(b.dataset.featurePlay, { resume: true, onClose: () => render() });
    const d = e.target.closest('[data-dot]');
    if (d) show(Number(d.dataset.dot));
  });
  const bgs = $$('.bg', sec);
  // Backdrops load one ahead of time, not all twenty at once.
  const load = (n) => { const b = bgs[n % bgs.length]; if (b?.dataset.bg) { b.style.backgroundImage = `url('${b.dataset.bg}')`; delete b.dataset.bg; } };
  let i = 0;
  load(0);
  load(1);
  const show = (n) => {
    i = (n + titles.length) % titles.length;
    load(i);
    load(i + 1);
    bgs.forEach((b, k) => b.classList.toggle('on', k === i));
    const c = el(`<div class="content">${heroContent(titles, i)}</div>`);
    $('.content', sec).replaceWith(c);
  };
  if (titles.length < 2) return;
  let hover = false;
  sec.onmouseenter = () => (hover = true);
  sec.onmouseleave = () => (hover = false);
  const t = setInterval(() => {
    if (!sec.isConnected) return clearInterval(t);
    if (!hover && !document.hidden) show(i + 1);
  }, 8000);
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
  return `<nav class="tabs"><a href="/library/${id}" class="${active === 'home' ? 'active' : ''}">Overview</a>
    <a href="/library/${id}/all" class="${active === 'all' ? 'active' : ''}">All titles</a>
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
  const filtered = genre || unwatched || sort !== 'title';
  const [res, genres, rows] = await Promise.all([
    api(`/api/libraries/${id}/items?${params}`),
    api(`/api/genres?libraryId=${encodeURIComponent(id)}`).catch(() => []),
    filtered ? [] : api(`/api/libraries/${id}/rows`).catch(() => []),
  ]);
  // Collection rows the admin picked for this library, above the grid.
  const rowsHtml = rows.map((c) => shelf(c.collection.name, c.items.map((it) => posterCard(it)).join('') + (c.missing || []).map((v) => titleCard(v)).join(''),
    { see: `/collection/${c.collection.id}` })).join('');
  const noun = lib?.type === 'shows' ? 'shows' : 'movies';
  const node = el(`<div>${libraryTabs(id, 'all')}<div class="page-head"><h1>${esc(lib?.name || '')} <span class="muted small">${res.total} ${noun}</span></h1>
    <div class="toolbar">${isAdmin() ? '<button class="btn" id="rows" title="Pick which collections show as rows here, and their order">Edit rows</button>' : ''}
    <button class="chip${unwatched ? ' on' : ''}" id="unwatched" aria-pressed="${unwatched}">Unwatched</button>
    <select id="genre" data-caption="Genre"><option value="">All</option>${genres.map((g) => `<option value="${esc(g.genre)}" ${g.genre === genre ? 'selected' : ''}>${esc(g.genre)} (${g.count})</option>`).join('')}</select>
    <select id="sort" data-caption="Sort">${sorts.map(([k, v]) => `<option value="${k}" ${k === sort ? 'selected' : ''}>${v}</option>`).join('')}</select></div></div>
    ${rowsHtml ? `<div class="lib-rows">${rowsHtml}</div><h2 class="grid-title">All ${noun}</h2>` : ''}
    ${res.items.length ? `<div class="grid">${res.items.map((it) => posterCard(it)).join('')}</div>`
      : genre || unwatched ? '<div class="empty"><h2>Nothing matches</h2><p>Try a different genre or turn off Unwatched.</p></div>'
      : '<div class="empty"><h2>This library is empty</h2><p>Items appear here as they are scanned.</p></div>'}</div>`);
  const nav = (patch) => {
    const n = new URLSearchParams(location.search);
    for (const [k, v] of Object.entries(patch)) v ? n.set(k, v) : n.delete(k);
    go(`/library/${id}/all${n.toString() ? '?' + n : ''}`, true);
  };
  $('#sort', node).onchange = (e) => nav({ sort: e.target.value === 'title' ? '' : e.target.value });
  $('#genre', node).onchange = (e) => nav({ genre: e.target.value });
  $('#unwatched', node).onclick = () => nav({ unwatched: unwatched ? '' : '1' });
  $('#rows', node)?.addEventListener('click', () => browse.editLibraryRows(id));
  bindPlayLinks(node);
  bindShelves(node);
  return node;
}

async function viewSearch() {
  const q = new URLSearchParams(location.search).get('q') || '';
  setTitle('Search');
  const input = $('#search-form input');
  if (input) input.value = q;
  if (!q) return '<div class="empty"><h2>Search for a movie or show</h2></div>';
  const [items, more, colls] = await Promise.all([
    api('/api/search?q=' + encodeURIComponent(q)),
    api('/api/discover/search?q=' + encodeURIComponent(q)).catch(() => ({ items: [] })),
    api('/api/collections?q=' + encodeURIComponent(q)).catch(() => []),
  ]);
  // Collections named like the search, such as a franchise, come first.
  const collShelf = colls.length ? shelf('Collections', colls.map(browse.collectionTile).join(''), { sub: `${colls.length}` }) : '';
  // Library results first; TMDB adds whatever the server doesn't have.
  const have = new Set(items.map((it) => it.id));
  const extra = (more.items || []).filter((v) => !v.item || !have.has(v.item.id));
  const inLib = [...items, ...extra.filter((v) => v.item).map((v) => v.item)];
  const rest = extra.filter((v) => !v.item);
  if (canStream()) {
    // Everything plays, so there's one list: what the server has, then the rest.
    const all = [...inLib.map((it) => posterCard(it)), ...rest.map((v) => titleCard(v))];
    const node = el(`<div><div class="page-head"><h1>Results for “${esc(q)}”</h1></div>${collShelf}
      ${all.length ? `${collShelf ? '<h2 class="grid-title">Titles</h2>' : ''}<div class="grid">${all.join('')}</div>` : collShelf ? '' : '<div class="empty"><h2>No matches</h2></div>'}</div>`);
    bindPlayLinks(node);
    bindShelves(node);
    return node;
  }
  const restTitle = 'Not on this server';
  const node = el(`<div><div class="page-head"><h1>Results for “${esc(q)}”</h1></div>${collShelf}
    ${inLib.length ? `<h2 class="grid-title">On this server</h2><div class="grid">${inLib.map((it) => posterCard(it)).join('')}</div>` : ''}
    ${rest.length ? `<h2 class="grid-title">${restTitle}</h2>
      <div class="grid">${rest.map((v) => titleCard(v)).join('')}</div>` : ''}
    ${!inLib.length && !rest.length && !collShelf ? '<div class="empty"><h2>No matches</h2></div>' : ''}</div>`);
  bindPlayLinks(node);
  bindShelves(node);
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
  if (f.streamId && isAdmin()) b.push('Stream');
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
  const tag = c.id ? `a class="person" href="/person/${c.id}"` : 'div class="person"';
  return `<${tag}><div class="face">${c.profilePath ? lazyImg(img(c.profilePath, 'w185'), c.name) : esc(initials)}</div>
    <div class="n">${esc(c.name)}</div>${c.character ? `<div class="c">${esc(c.character)}</div>` : ''}</${c.id ? 'a' : 'div'}>`;
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

const langNames = (() => { try { return new Intl.DisplayNames([navigator.language], { type: 'language' }); } catch { return null; } })();
const langName = (c) => (c ? langNames?.of(c) || c : '');

const isWatched = (it) => it.progress?.watched || ((it.kind === 'show' || it.kind === 'season') && it.childCount > 0 && !it.unwatchedCount);

function episodeRows(eps) {
  if (!eps.length) return '<div class="empty small"><p>No episodes yet.</p></div>';
  return eps.map((e) => `<a class="episode" href="/item/${e.id}" data-play="${e.id}">
      <div class="thumb">${e.stillPath ? lazyImg(img(e.stillPath, 'w300')) : ''}<div class="play-ov"><span></span></div>${progressBar(e)}</div>
      <div><div class="ep-t">${e.episodeNumber}. ${esc(e.title)}${e.progress?.watched ? '<span class="ep-check" title="Watched"></span>' : ''}</div>
      <div class="small muted">${[e.airDate, fmtRuntime(e.runtimeMin), timeLeft(e.progress)].filter(Boolean).map(esc).join(' · ')}</div>
      <p class="small muted clamp3">${esc(e.overview || '')}</p></div></a>`).join('');
}

// The file a version label describes: its own label, else quality and size.
const versionName = (f) => f.label || [badgesFor(f)[0], fmtSize(f.size)].filter(Boolean).join(' · ') || f.fileName;

function mediaLine(f) {
  const v = f.videoCodec && `${f.videoCodec.toUpperCase()}${f.width ? ` ${f.width}×${f.height}` : ''}${f.hdr ? ` ${f.hdr}` : ''}${f.dvProfile ? ` P${f.dvProfile}` : ''}`;
  const a = f.info?.audio?.find((x) => x.default) || f.info?.audio?.[0];
  const audio = a && [a.codec?.toUpperCase(), a.channels >= 6 ? (a.channels === 8 ? '7.1' : '5.1') : a.channels === 2 ? 'Stereo' : '', a.atmos ? 'Atmos' : '', a.language].filter(Boolean).join(' ');
  const stream = f.streamId && isAdmin() ? (f.release ? `Stream · ${f.release}` : 'Stream') : '';
  return [v, audio, fmtSize(f.size), stream].filter(Boolean);
}

async function viewItem(id) {
  const [d, prefs] = await Promise.all([api('/api/items/' + id), api('/api/me/prefs').catch(() => ({}))]);
  const it = d.item;
  const show = d.show;
  setTitle(it.kind === 'episode' || it.kind === 'season' ? show?.title || it.title : it.title);
  const backdrop = it.backdropPath || show?.backdropPath || it.stillPath;
  const logo = it.kind === 'movie' || it.kind === 'show' ? it.logoPath : null;
  const heading = it.kind === 'season' ? (it.seasonNumber === 0 ? 'Specials' : `Season ${it.seasonNumber}`)
    : it.kind === 'episode' ? `S${it.seasonNumber} · E${it.episodeNumber} — ${it.title}` : it.title;
  const top = it.kind === 'movie' || it.kind === 'show';
  const meta = [];
  if (it.year) meta.push(esc(it.year));
  if (it.contentRating) meta.push(`<span class="pill">${esc(it.contentRating)}</span>`);
  if (it.runtimeMin) meta.push(fmtRuntime(it.runtimeMin));
  if (it.kind === 'show' && d.children?.length) meta.push(`${d.children.length} season${d.children.length > 1 ? 's' : ''}`);
  if (it.rating) meta.push(`★ ${it.rating.toFixed(1)}`);
  const genres = (it.genres?.length ? it.genres : show?.genres || []).slice(0, 3);
  const genreHtml = !genres.length ? '' : top ? `<span class="genres">${genres.map((g) => `<a href="/genre?g=${encodeURIComponent(g)}" title="More ${esc(g)}">${esc(g)}</a>`).join('')}</span>`
    : `<span class="muted">${genres.map(esc).join(', ')}</span>`;
  const badges = (f) => (f ? badgesFor(f).filter((b) => b !== 'Stream') : []).map((b) => `<span class="qbadge">${esc(b)}</span>`).join('');

  const playable = it.kind === 'movie' || it.kind === 'episode';
  const resumeAt = playable && it.progress && !it.progress.watched && it.progress.position > 30 ? it.progress.position : 0;
  const left = timeLeft(it.progress);
  let actions = '';
  if (playable) {
    actions += resumeAt
      ? `<button class="btn primary lg" data-act="resume" title="Resume from ${fmtDuration(resumeAt)}">▶ Resume${left ? ` · ${left}` : ''}</button><button class="btn lg" data-act="start">↺ Start Over</button>`
      : `<button class="btn primary lg" data-act="start">▶ Play</button>`;
  } else if (d.nextEpisode) {
    const n = d.nextEpisode;
    actions += `<button class="btn primary lg" data-act="next">▶ ${n.progress?.position > 30 ? 'Resume' : 'Play'} S${n.seasonNumber} · E${n.episodeNumber}</button>`;
  }
  actions += `<button class="btn lg" data-act="trailer" hidden>Trailer</button>`;
  if (d.onWatchlist !== undefined) {
    actions += `<button class="btn lg${d.onWatchlist ? ' on' : ''}" data-act="watchlist" aria-pressed="${d.onWatchlist}" title="${d.onWatchlist ? 'Remove from your watchlist' : 'Add to your watchlist'}">${icons.bookmark} ${d.onWatchlist ? 'On Watchlist' : 'Watchlist'}</button>`;
  }
  actions += `<button class="btn lg${isWatched(it) ? ' on' : ''}" data-act="watched">${isWatched(it) ? '✓ Watched' : 'Mark Watched'}</button>`;
  if (playable && d.canDownload && (d.files?.length || d.streaming)) actions += `<button class="btn lg" data-act="download" title="Save to this device">${icons.download} Download</button>`;
  if (isAdmin() && d.streaming) {
    actions += `<button class="btn lg" data-act="serverdl" title="Save as a file in your library">Download to server</button>`;
    if (it.kind !== 'show' || d.children?.length) actions += `<button class="btn lg" data-act="restream" title="Forget saved streams and look again">Find new streams</button>`;
  }
  if (isAdmin() && top) {
    actions += `<button class="btn lg" data-act="collect">${icons.plus} Collection</button><button class="btn lg" data-act="match">Fix match</button>`;
  }

  // What will play: version, audio and subtitles, from the viewer's settings
  // unless they pick another version.
  let tracks = '';
  if (playable && d.files?.length) {
    const best = d.files.reduce((a, f) => ((f.width || 0) > (a.width || 0) ? f : a), d.files[0]);
    const q = badgesFor(best)[0] || '';
    const version = d.files.length > 1
      ? `<select data-version data-caption="Version" class="version-pick"><option value="">${q ? `Auto · ${esc(q)}` : 'Auto'}</option>${d.files.map((f) => `<option value="${esc(f.id)}">${esc(versionName(f))}</option>`).join('')}</select>`
      : `<span class="track"><b>Version</b>${esc(q || 'Original')}</span>`;
    tracks = `<div class="tracks" title="Audio and subtitles follow your settings; change them while watching.">${version}
      <span class="track"><b>Audio</b>${esc(langName(prefs.audioLanguage) || 'Default')}</span>
      <span class="track"><b>Subtitles</b>${esc(langName(prefs.subtitleLanguage) || 'Off')}</span></div>`;
  }

  const crumbs = it.kind === 'season' || it.kind === 'episode'
    ? `<div class="crumbs"><a href="/item/${it.showId}">${esc(show?.title || '')}</a>${it.kind === 'episode' && it.parentId ? ` › <a href="/item/${it.parentId}">Season ${it.seasonNumber}</a>` : ''}</div>`
    : '';

  let below = '';
  if (it.kind === 'show' && d.children?.length) {
    const seasons = d.children;
    const next = d.nextEpisode && seasons.find((s) => s.id === d.nextEpisode.parentId);
    const first = next || seasons.find((s) => s.seasonNumber > 0) || seasons[0];
    below += `<section class="seasons"><div class="season-head"><select id="season" class="season-pick" aria-label="Season">${seasons.map((s) => `<option value="${s.id}" ${s.id === first.id ? 'selected' : ''}>${s.seasonNumber === 0 ? 'Specials' : `Season ${s.seasonNumber}`}</option>`).join('')}</select></div>
      <div class="episodes" id="eps"><div class="center" style="min-height:120px"><div class="spinner"></div></div></div></section>`;
  }
  if (it.kind === 'season') below += `<div class="episodes">${episodeRows(d.children || [])}</div>`;
  if (top) {
    // Cast and "more like this" arrive with the extras; show placeholders.
    below += `<section class="shelf" id="cast-shelf"><div class="shelf-head"><h2>Cast</h2>${arrows}</div><div class="cast">${Array.from({ length: 8 }, () =>
      '<div class="person"><div class="face sk"></div><div class="sk sk-line" style="width:70%;margin:0 auto"></div></div>').join('')}</div></section>`;
    below += skeletonShelf('More Like This', 8, 'more');
  }
  if (d.files?.length) {
    const chapters = d.files[0].info?.chapters?.length;
    below += `<section class="files"><h2>Media</h2>${chapters ? `<div class="small muted">${chapters} chapters</div>` : ''}${d.files.map((f) => `<div class="file">
      <code>${esc(f.fileName)}</code><div class="small muted">${mediaLine(f).map(esc).join(' · ')}</div></div>`).join('')}</section>`;
  }

  const node = el(`<div><div class="hero"><div class="bg" ${backdrop ? `style="background-image:url('${img(backdrop, 'w1280')}')"` : ''}></div>
    <div class="content"><div>${crumbs}${logo ? `<img class="logo-img" src="${img(logo, 'w500')}" alt="${esc(heading)}">` : `<h1>${esc(heading)}</h1>`}
    <div class="meta">${meta.join('<span class="muted">·</span>')}${genreHtml}${badges(d.files?.[0])}</div>
    ${top ? `<div class="ratings" id="ratings">${ratingsHtml({ tmdb: it.rating })}</div>` : ''}
    ${it.tagline ? `<div class="tagline">${esc(it.tagline)}</div>` : ''}
    <p class="overview">${esc(it.overview || '')}</p>${tracks}<div class="actions">${actions}</div></div></div></div>${below}</div>`);

  const act = (name, fn) => $(`[data-act=${name}]`, node)?.addEventListener('click', fn);
  const version = () => $('[data-version]', node)?.value || undefined;
  act('resume', () => openPlayer(it.id, { start: resumeAt, fileId: version(), onClose: () => render() }));
  act('start', () => openPlayer(it.id, { start: 0, fileId: version(), onClose: () => render() }));
  act('collect', () => browse.addToCollection(it));
  act('next', () => openPlayer(d.nextEpisode.id, { resume: true, onClose: () => render() }));
  act('watched', async () => {
    await api(`/api/items/${it.id}/watched`, { method: 'POST', body: { watched: !isWatched(it) } });
    render();
  });
  act('watchlist', async (e) => {
    const b = e.currentTarget;
    b.disabled = true;
    try {
      await api(`/api/items/${it.id}/watchlist`, { method: d.onWatchlist ? 'DELETE' : 'PUT' });
      render();
    } catch (ex) { toast(ex.message); b.disabled = false; }
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
  const pick = $('#season', node);
  if (pick) {
    const loadSeason = async () => {
      const eps = $('#eps', node);
      eps.innerHTML = '<div class="center" style="min-height:120px"><div class="spinner"></div></div>';
      try {
        const s = await api('/api/items/' + pick.value);
        eps.innerHTML = episodeRows(s.children || []);
        bindPlayLinks(eps);
      } catch (ex) { eps.innerHTML = `<p class="error">${esc(ex.message)}</p>`; }
    };
    pick.onchange = loadSeason;
    loadSeason();
  }
  bindPlayLinks(node);
  bindShelves(node);
  if (top) loadExtras(it, node);
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
