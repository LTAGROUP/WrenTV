import { api, account, local, state, saveSession, isSupabase, img, ApiError } from './api.js';
import { esc, $, $$, el, toast, icons, fmtRuntime } from './ui.js';
import { openPlayer } from './player.js';
import * as manage from './manage.js';

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
  [/^\/item\/([^/]+)$/, viewItem],
  [/^\/search$/, viewSearch],
  [/^\/link$/, manage.viewLink],
  [/^\/manage\/?$/, manage.viewStatus, true],
  [/^\/manage\/libraries$/, manage.viewLibraries, true],
  [/^\/manage\/users$/, manage.viewUsers, true],
  [/^\/manage\/torrents$/, manage.viewTorrents, true],
  [/^\/manage\/settings$/, manage.viewSettings, true],
];

export const isAdmin = () => state.user && (state.user.role === 'owner' || state.user.role === 'admin' || state.user.isAdmin);

async function boot() {
  try {
    state.server = await api('/api/server');
  } catch (e) {
    app.innerHTML = `<div class="center"><div class="auth"><h2>Can't reach the server</h2><p class="muted">${esc(e.message)}</p></div></div>`;
    return;
  }
  document.title = state.server.name ? `${state.server.name} · Haven` : 'Haven';
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
  if (location.pathname === '/link' && isSupabase()) {
    // Linking a device needs only an account, not access to this server.
    if (!state.user) await loadMe().catch(() => {});
    return shell(() => manage.viewLink(), seq);
  }
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
  app.innerHTML = `<div class="center"><div class="auth"><div class="brand"><div class="logo"></div>Haven</div>${inner}</div></div>`;
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
  authCard(`<p class="muted">${up ? 'Create your Haven account.' : `Sign in with your Haven account to use <b>${esc(srv.name)}</b>.`}</p>
    <form>${up ? '<div class="field"><label>Name</label><input name="name" autocomplete="name" required></div>' : ''}
    <div class="field"><label>Email</label><input name="email" type="email" autocomplete="email" required></div>
    <div class="field"><label>Password</label><input name="password" type="password" minlength="8" autocomplete="${up ? 'new-password' : 'current-password'}" required></div>
    <p class="error" hidden></p><button class="btn primary lg" style="width:100%">${up ? 'Create account' : 'Sign in'}</button></form>
    <p class="switch muted">${up ? 'Already have an account? <a data-sw>Sign in</a>' : 'New to Haven? <a data-sw>Create an account</a>'}</p>`);
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
    <pre class="mono" style="background:#0d0d0d;padding:10px;border-radius:6px;margin:12px 0">docker logs haven 2>&1 | grep claim_code</pre>
    <form><div class="field"><label>Claim code</label><input name="code" class="code-input" placeholder="XXXX-XXXX" autocomplete="off" required></div>
    <p class="error" hidden></p><button class="btn primary lg" style="width:100%">Claim server</button></form>
    <p class="switch muted">Not you? <a data-out>Sign out</a></p>`);
  $('[data-out]', app).onclick = signOut;
  bindForm(async (v) => {
    await api('/api/setup/claim', { method: 'POST', body: { claimCode: v.code.trim() } });
    state.server = await api('/api/server');
    toast('Server claimed. Welcome to Haven!');
    go('/manage/libraries');
  });
}

function viewNoAccess() {
  const email = state.session?.user?.email || '';
  authCard(`<h2 style="margin-bottom:8px">No access</h2>
    <p class="muted"><b>${esc(email)}</b> doesn't have access to ${esc(state.server.name)}. Ask the owner to share it with this email address.</p>
    <div class="actions"><button class="btn" id="retry">Check again</button><a class="btn" href="/link">Link a device</a><button class="btn ghost" id="out">Sign out</button></div>`);
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
        <a class="brand" href="/"><div class="logo"></div>${esc(state.server.name || 'Haven')}</a>
        <a class="nav" href="/" data-path="/">${icons.home}<span>Home</span></a>
        <div class="nav-title">Libraries</div>
        <div id="lib-nav">${libNav()}</div>
        ${admin ? `<div class="nav-title">Manage</div>
          <a class="nav" href="/manage" data-path="/manage">${icons.activity}<span>Dashboard</span></a>
          <a class="nav" href="/manage/libraries" data-path="/manage/libraries">${icons.folder}<span>Libraries</span></a>
          <a class="nav" href="/manage/users" data-path="/manage/users">${icons.users}<span>${isSupabase() ? 'Sharing' : 'Users'}</span></a>
          ${state.server.torrents ? `<a class="nav" href="/manage/torrents" data-path="/manage/torrents">${icons.torrent}<span>Torrents</span></a>` : ''}
          <a class="nav" href="/manage/settings" data-path="/manage/settings">${icons.settings}<span>Settings</span></a>` : ''}
        ${isSupabase() ? `<div class="nav-title">Account</div><a class="nav" href="/link" data-path="/link">${icons.device}<span>Link a device</span></a>` : ''}
        <div class="spacer"></div>
        <div class="account"><div class="avatar">${esc((name[0] || '?').toUpperCase())}</div>
          <div style="min-width:0;flex:1"><div style="white-space:nowrap;overflow:hidden;text-overflow:ellipsis">${esc(name)}</div>
          <div class="small muted">${esc(u?.role || '')}</div></div>
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
    a.classList.toggle('active', p === '/' ? location.pathname === '/' : location.pathname === p || (p !== '/manage' && location.pathname.startsWith(p + '/')));
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

export function setTitle(t) {
  const el = $('#page-title');
  if (el) el.textContent = t;
}

// ---------------------------------------------------------------- cards

function progressBar(it) {
  const p = it.progress;
  if (!p || p.watched || !p.duration || p.position < 30) return '';
  return `<div class="bar"><i style="width:${Math.min(100, (p.position / p.duration) * 100).toFixed(1)}%"></i></div>`;
}

function posterCard(it) {
  const title = it.kind === 'season' ? (it.seasonNumber === 0 ? 'Specials' : `Season ${it.seasonNumber}`) : it.title;
  const sub = it.kind === 'show' ? (it.childCount ? `${it.childCount} season${it.childCount > 1 ? 's' : ''}` : it.year || '')
    : it.kind === 'season' ? (it.childCount ? `${it.childCount} episodes` : '') : it.year || '';
  const corner = it.kind === 'show' || it.kind === 'season'
    ? (it.unwatchedCount ? `<div class="badge">${it.unwatchedCount}</div>` : it.childCount ? '<div class="watched"></div>' : '')
    : it.progress?.watched ? '<div class="watched"></div>' : '';
  return `<a class="card" href="/item/${it.id}"><div class="poster">${it.posterPath ? `<img loading="lazy" src="${img(it.posterPath)}" alt="">` : `<div class="ph">${esc(title)}</div>`}${corner}${progressBar(it)}</div>
    <div class="t" title="${esc(title)}">${esc(title)}</div><div class="s">${esc(sub)}</div></a>`;
}

function wideCard(it) {
  const pic = it.stillPath || it.backdropPath;
  const title = it.kind === 'episode' ? it.showTitle || it.title : it.title;
  const sub = it.kind === 'episode' ? `S${it.seasonNumber} · E${it.episodeNumber} — ${it.title}` : it.year || '';
  return `<a class="card wide" href="/item/${it.id}" data-play="${it.id}"><div class="thumb">${pic ? `<img loading="lazy" src="${img(pic, 'w500')}" alt="">` : ''}
    <div class="play-ov"><span></span></div>${progressBar(it)}</div>
    <div class="t">${esc(title)}</div><div class="s">${esc(sub)}</div></a>`;
}

// ---------------------------------------------------------------- views

async function viewHome() {
  setTitle('Home');
  const home = await api('/api/home');
  if (!libraries.length) {
    return `<div class="empty"><h2>No libraries yet</h2><p>${isAdmin() ? 'Add a folder with your movies or shows to get started.' : 'The server owner hasn\'t shared any libraries with you yet.'}</p>
      ${isAdmin() ? '<a class="btn primary" href="/manage/libraries">Add a library</a>' : ''}</div>`;
  }
  let html = '';
  if (home.continueWatching.length) {
    html += `<section class="shelf"><h2>Continue Watching</h2><div class="row">${home.continueWatching.map(wideCard).join('')}</div></section>`;
  }
  for (const s of home.recentlyAdded) {
    if (!s.items.length) continue;
    html += `<section class="shelf"><h2>Recently Added in ${esc(s.library.name)} <a class="small muted" href="/library/${s.library.id}">See all</a></h2>
      <div class="row">${s.items.map(posterCard).join('')}</div></section>`;
  }
  const node = el(`<div>${html || '<div class="empty"><h2>Nothing here yet</h2><p>Your libraries are still being scanned.</p></div>'}</div>`);
  bindPlayLinks(node);
  return node;
}

function bindPlayLinks(root) {
  for (const a of $$('[data-play]', root)) {
    $('.play-ov', a)?.addEventListener('click', (e) => {
      e.preventDefault();
      e.stopPropagation();
      openPlayer(a.dataset.play, { resume: true });
    });
  }
}

const sorts = [['title', 'Title'], ['added', 'Recently added'], ['year', 'Year'], ['rating', 'Rating']];

async function viewLibrary(id) {
  const lib = libraries.find((l) => l.id === id);
  setTitle(lib?.name || 'Library');
  const sort = new URLSearchParams(location.search).get('sort') || 'title';
  const res = await api(`/api/libraries/${id}/items?sort=${encodeURIComponent(sort)}&limit=5000`);
  const node = el(`<div><div class="page-head"><h1>${esc(lib?.name || '')} <span class="muted small">${res.total} ${lib?.type === 'shows' ? 'shows' : 'movies'}</span></h1>
    <select id="sort" style="width:auto">${sorts.map(([k, v]) => `<option value="${k}" ${k === sort ? 'selected' : ''}>${v}</option>`).join('')}</select></div>
    ${res.items.length ? `<div class="grid">${res.items.map(posterCard).join('')}</div>` : '<div class="empty"><h2>This library is empty</h2><p>Items appear here as they are scanned.</p></div>'}</div>`);
  $('#sort', node).onchange = (e) => go(`/library/${id}?sort=${e.target.value}`, true);
  return node;
}

async function viewSearch() {
  const q = new URLSearchParams(location.search).get('q') || '';
  setTitle('Search');
  const input = $('#search-form input');
  if (input) input.value = q;
  const items = q ? await api('/api/search?q=' + encodeURIComponent(q)) : [];
  return `<div class="page-head"><h1>Results for “${esc(q)}”</h1></div>
    ${items.length ? `<div class="grid">${items.map(posterCard).join('')}</div>` : '<div class="empty"><h2>No matches</h2></div>'}`;
}

function badgesFor(f) {
  const b = [];
  if (f.width >= 3200) b.push('4K'); else if (f.width >= 1800) b.push('1080p'); else if (f.width >= 1200) b.push('720p');
  if (f.hdr === 'DV' || f.hdr === 'DV+HDR10') b.push('Dolby Vision'); else if (f.hdr) b.push(f.hdr);
  const a = f.info?.audio?.find((x) => x.default) || f.info?.audio?.[0];
  if (a?.profile?.includes('Atmos')) b.push('Atmos');
  else if (a?.channels >= 6) b.push(a.channels === 8 ? '7.1' : '5.1');
  if (f.torrentId) b.push('Torrent');
  return b;
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
  if (it.contentRating) meta.push(`<span class="pill">${esc(it.contentRating)}</span>`);
  if (it.rating) meta.push(`★ ${it.rating.toFixed(1)}`);
  const file = d.files?.[0];
  if (file) for (const b of badgesFor(file)) meta.push(`<span class="pill">${esc(b)}</span>`);

  const playable = it.kind === 'movie' || it.kind === 'episode';
  const resumeAt = playable && it.progress && !it.progress.watched && it.progress.position > 30 ? it.progress.position : 0;
  let actions = '';
  if (playable) {
    actions += resumeAt
      ? `<button class="btn primary lg" data-act="resume">▶ Resume</button><button class="btn lg" data-act="start">Play from start</button>`
      : `<button class="btn primary lg" data-act="start">▶ Play</button>`;
    actions += `<button class="btn lg" data-act="watched">${it.progress?.watched ? 'Mark unwatched' : 'Mark watched'}</button>`;
  } else if (d.nextEpisode) {
    const n = d.nextEpisode;
    actions += `<button class="btn primary lg" data-act="next">▶ ${n.progress?.position > 30 ? 'Resume' : 'Play'} S${n.seasonNumber} · E${n.episodeNumber}</button>`;
  }
  if (isAdmin() && (it.kind === 'movie' || it.kind === 'show')) actions += `<button class="btn lg ghost" data-act="match">Fix match</button>`;

  const crumbs = it.kind === 'season' || it.kind === 'episode'
    ? `<div class="small" style="margin-bottom:6px"><a href="/item/${it.showId}">${esc(show?.title || '')}</a>${it.kind === 'episode' && it.parentId ? ` › <a href="/item/${it.parentId}">Season ${it.seasonNumber}</a>` : ''}</div>`
    : '';

  let below = '';
  if (it.kind === 'show' && d.children?.length) {
    below = `<section class="shelf"><h2>Seasons</h2><div class="row">${d.children.map(posterCard).join('')}</div></section>`;
  }
  if (it.kind === 'season') {
    below = `<div class="episodes">${(d.children || []).map((e) => `<a class="episode" href="/item/${e.id}" data-play="${e.id}">
      <div class="thumb">${e.stillPath ? `<img loading="lazy" src="${img(e.stillPath, 'w300')}" alt="">` : ''}<div class="play-ov"><span></span></div>${progressBar(e)}${e.progress?.watched ? '<div class="watched"></div>' : ''}</div>
      <div><div style="font-weight:600">${e.episodeNumber}. ${esc(e.title)}</div>
      <div class="small muted">${[e.airDate, fmtRuntime(e.runtimeMin)].filter(Boolean).map(esc).join(' · ')}</div>
      <p class="small muted" style="margin:6px 0 0;display:-webkit-box;-webkit-line-clamp:3;-webkit-box-orient:vertical;overflow:hidden">${esc(e.overview || '')}</p></div></a>`).join('')}</div>`;
  }
  if (d.files?.length) {
    below += `<div class="files"><h2 style="font-size:16px">Files</h2>${d.files.map((f) => `<div class="file">
      <div style="display:flex;gap:8px;flex-wrap:wrap;align-items:center"><b>${badgesFor(f).map(esc).join(' · ') || esc(f.container)}</b>
      ${f.torrentStatus ? `<span class="status s-${esc(f.torrentStatus)}">${esc(f.torrentStatus)}</span>` : ''}
      ${d.files.length > 1 ? `<button class="btn small" data-file="${f.id}" style="margin-left:auto">Play this version</button>` : ''}</div>
      <code class="muted">${esc(f.fileName)}</code>
      ${f.info ? `<div class="small muted" style="margin-top:4px">${[f.videoCodec?.toUpperCase(), f.width && `${f.width}×${f.height}`, ...(f.info.audio || []).slice(0, 3).map((a) => [a.codec?.toUpperCase(), a.language].filter(Boolean).join(' '))].filter(Boolean).map(esc).join(' · ')}</div>` : ''}</div>`).join('')}</div>`;
  }

  const node = el(`<div><div class="hero"><div class="bg" ${backdrop ? `style="background-image:url('${img(backdrop, 'w1280')}')"` : ''}></div>
    <div class="content">${poster ? `<div class="poster"><img src="${img(poster, 'w500')}" alt=""></div>` : ''}
    <div>${crumbs}${logo ? `<img class="logo-img" src="${img(logo, 'w500')}" alt="${esc(heading)}">` : `<h1>${esc(heading)}</h1>`}
    ${it.tagline ? `<div class="muted" style="font-style:italic">${esc(it.tagline)}</div>` : ''}
    <div class="meta">${meta.join('<span class="muted">·</span>')}</div>
    ${it.genres?.length ? `<div class="small muted" style="margin-bottom:8px">${it.genres.map(esc).join(', ')}</div>` : ''}
    <p class="overview">${esc(it.overview || '')}</p><div class="actions">${actions}</div></div></div></div>${below}</div>`);

  const act = (name, fn) => $(`[data-act=${name}]`, node)?.addEventListener('click', fn);
  act('resume', () => openPlayer(it.id, { start: resumeAt, onClose: () => render() }));
  act('start', () => openPlayer(it.id, { start: 0, onClose: () => render() }));
  act('next', () => openPlayer(d.nextEpisode.id, { resume: true, onClose: () => render() }));
  act('watched', async () => {
    await api(`/api/items/${it.id}/watched`, { method: 'POST', body: { watched: !it.progress?.watched } });
    render();
  });
  act('match', () => manage.fixMatch(it));
  for (const b of $$('[data-file]', node)) b.onclick = () => openPlayer(it.id, { start: resumeAt, fileId: b.dataset.file, onClose: () => render() });
  bindPlayLinks(node);
  return node;
}

boot();
