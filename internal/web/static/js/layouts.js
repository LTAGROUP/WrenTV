// Pages built from the server's layout rows — Home, Movies, TV Shows and
// each library — plus hub pages and the editor that picks a page's rows.
import { api, img, ApiError } from './api.js';
import { esc, $, el, toast, icons } from './ui.js';
import { openPlayer } from './player.js';
import { render, setTitle, isAdmin, canStream, getLibraries, posterCard, titleCard, shelf, bindShelves, bindPlayLinks,
  wideCard, discoverHero, bindHero, featureHero, lazyImg, bd } from './app.js';

const uniq = (list, key) => {
  const seen = new Set();
  return (list || []).filter((x) => { const k = key(x); return seen.has(k) ? false : seen.add(k); });
};
const titleKey = (v) => v.mediaType + v.tmdbId;

// viewLayoutPage draws scope's rows under a banner of its first titles.
// Servers from before layouts answer 404 and get fallback instead.
export async function viewLayoutPage(scope, title, fallback, browseAll) {
  setTitle(title);
  let layout;
  try {
    layout = await api(`/api/layouts/${scope}`);
  } catch (e) {
    if (e instanceof ApiError && e.status === 404) return fallback();
    throw e;
  }
  const rows = layout.rows || [];
  const titles = uniq(rows.find((r) => r.kind === 'titles' && r.titles?.length)?.titles, titleKey).filter((v) => v.backdropPath).slice(0, 6);
  const feature = titles.length ? null : rows.filter((r) => r.kind === 'items' || r.kind === 'collection').flatMap((r) => r.items || [])
    .find((it) => it.backdropPath && it.kind !== 'episode');
  let html = titles.length ? discoverHero(titles) : feature ? featureHero(feature) : `<div class="page-head"><h1>${esc(title)}</h1></div>`;
  html += `<div class="page-actions">${browseAll ? `<a class="btn" href="${browseAll}">Browse all</a>` : ''}
    <a class="btn ghost" href="/layouts/${encodeURIComponent(scope)}?title=${encodeURIComponent(title)}">${icons.settings}<span>Customize</span></a></div>`;
  html += layoutRows(rows);
  if (!rows.length) {
    html += `<div class="empty"><h2>Nothing here yet</h2><p>${isAdmin() ? 'Add a library, or choose rows with Customize.' : 'The server owner hasn\'t shared anything here yet.'}</p></div>`;
  }
  const node = el(`<div class="discover">${html}</div>`);
  bindPlayLinks(node);
  bindShelves(node);
  if (titles.length) bindHero(node, titles);
  $('[data-feature-play]', node)?.addEventListener('click', (e) => openPlayer(e.currentTarget.dataset.featurePlay, { resume: true, onClose: () => render() }));
  return node;
}

// layoutRows draws each row the way its kind asks. A collection's or hub's
// row ends in a card that opens the rest of it.
function layoutRows(rows) {
  return rows.map((row) => {
    let cards;
    switch (row.kind) {
      case 'landscape': cards = uniq(row.items, (i) => i.id).map(wideCard); break;
      case 'hubs': cards = uniq(row.hubs, (h) => h.id).map((h) => hubTile(h, row.style === 'logos')); break;
      case 'titles': cards = uniq(row.titles, titleKey).map((v, n) => titleCard(v, row.ranked && n < 10 ? n + 1 : 0)); break;
      default: cards = [...uniq(row.items, (i) => i.id).map((it) => posterCard(it)), ...uniq(row.missing, titleKey).map((v) => titleCard(v))];
    }
    if (!cards.length) return '';
    let see;
    if (row.collection) {
      see = `/collection/${row.collection.id}`;
      cards.push(moreCard('View all', see));
    } else if (row.hubId) {
      see = `/hub/${encodeURIComponent(row.hubId)}`;
      cards.push(moreCard(`Open ${row.title}`, see));
    }
    return shelf(row.title, cards.join(''), { see, sub: row.subtitle || '' });
  }).join('');
}

const moreCard = (label, href) => `<a class="card more-card" href="${href}"><div class="poster"><span>${icons.stack}<b>${esc(label)}</b></span></div></a>`;

// A hub's tile: its artwork with the name over it, or a service's logo on its colour.
function hubTile(h, logos) {
  const href = `/hub/${encodeURIComponent(h.id)}`;
  if (logos && h.logoPath) {
    return `<a class="card wide hub logo" href="${href}" title="${esc(h.name)}"${bd(h.backdropPath)}><div class="thumb" style="background:${esc(h.color || 'var(--bg-3)')}">
      <img loading="lazy" src="${img(h.logoPath, 'w300')}" alt="${esc(h.name)}"></div></a>`;
  }
  return `<a class="card wide hub" href="${href}" title="${esc(h.name)}"${bd(h.backdropPath)}><div class="thumb">${h.backdropPath ? lazyImg(img(h.backdropPath, 'w780')) : ''}
    <div class="shade" style="--tint:${esc(h.color || '#000000')}"></div><b>${esc(h.name)}</b></div></a>`;
}

// viewHub is a genre, a streaming service or an admin's own hub, as shelves.
export async function viewHub(id) {
  const page = await api(`/api/hubs/${encodeURIComponent(id)}`);
  const h = page.hub;
  setTitle(h.name);
  const kind = h.kind === 'genre' ? 'Genre' : h.kind === 'service' ? 'Streaming service' : 'Hub';
  const node = el(`<div class="discover"><section class="feature hub-head">${h.backdropPath ? `<div class="bg" style="background-image:url('${img(h.backdropPath, 'w1280')}')"></div>` : ''}
    <div class="content"><div class="eyebrow">${kind}</div><h1>${esc(h.name)}</h1>${h.tagline ? `<p class="overview">${esc(h.tagline)}</p>` : ''}</div></section>
    ${layoutRows(page.rows || []) || '<div class="empty"><h2>Nothing here yet</h2><p>None of this hub\'s shelves found any titles.</p></div>'}</div>`);
  bindPlayLinks(node);
  bindShelves(node);
  return node;
}

// viewLayouts lists the pages whose rows can be chosen.
export function viewLayouts() {
  setTitle('Layouts');
  const link = (scope, title, icon) => `<a class="layout-link" href="/layouts/${encodeURIComponent(scope)}?title=${encodeURIComponent(title)}">${icon}<span>${esc(title)}</span></a>`;
  let html = link('home', 'Home', icons.home);
  if (canStream()) html += link('movies', 'Movies', icons.movies) + link('shows', 'TV Shows', icons.shows);
  for (const l of getLibraries()) html += link(`library:${l.id}`, l.name, l.type === 'shows' ? icons.shows : icons.movies);
  return `<div class="layouts"><div class="page-head"><h1>Layouts</h1></div>
    <p class="muted">Choose which rows each page shows, and in what order.</p><div class="layout-links">${html}</div></div>`;
}

// viewLayoutEditor moves, hides, removes and adds one page's rows. Admins
// also choose whether they're editing their own layout or everyone's default.
export async function viewLayoutEditor(scope) {
  scope = decodeURIComponent(scope);
  const qs = new URLSearchParams(location.search);
  const title = qs.get('title') || 'Layout';
  const everyone = isAdmin() && qs.get('target') === 'default';
  const target = everyone ? 'default' : 'me';
  setTitle(title);
  const c = await api(`/api/layouts/${scope}/config?target=${target}`);
  const entries = c.entries || [];
  const key = (e) => `${e.type}|${e.ref}`;
  const have = new Set(entries.map(key));
  const options = (c.options || []).filter((o) => !have.has(key(o)));
  const here = `/layouts/${encodeURIComponent(scope)}?title=${encodeURIComponent(title)}`;
  const node = el(`<div class="layouts"><div class="page-head"><h1>${esc(title)} <span class="muted small">${everyone ? 'Default for everyone' : 'Your layout'}</span></h1>
    ${isAdmin() ? `<div class="toolbar"><a class="chip${everyone ? '' : ' on'}" href="${here}">My layout</a><a class="chip${everyone ? ' on' : ''}" href="${here}&target=default">Default for everyone</a></div>` : ''}</div>
    <ul class="layout-rows">${entries.map((e, i) => `<li class="${e.hidden ? 'hidden' : ''}" data-i="${i}"><div><b>${esc(e.title || e.ref)}</b>${e.detail ? `<div class="small muted">${esc(e.detail)}</div>` : ''}</div>
      <button class="btn ghost small" data-act="top" title="Move to top" ${i ? '' : 'disabled'}>⤒</button>
      <button class="btn ghost small" data-act="up" title="Move up" ${i ? '' : 'disabled'}>↑</button>
      <button class="btn ghost small" data-act="down" title="Move down" ${i < entries.length - 1 ? '' : 'disabled'}>↓</button>
      <button class="btn ghost small" data-act="hide">${e.hidden ? 'Show' : 'Hide'}</button>
      <button class="btn ghost small" data-act="remove" title="Remove">✕</button></li>`).join('')}</ul>
    ${options.length ? `<div class="layout-add"><select id="add"><option value="">Add a row…</option>${options.map((o, i) => `<option value="${i}">${esc(o.title || o.ref)}${o.detail ? ` — ${esc(o.detail)}` : ''}</option>`).join('')}</select></div>` : ''}
    <p class="muted small">${c.custom ? '' : !everyone && c.hasDefault ? 'Showing the server\'s default. Changing anything makes it your own.' : 'Showing Wren TV\'s built-in layout.'}</p>
    ${c.custom ? `<button class="btn" id="reset">${!everyone && c.hasDefault ? 'Use the default layout' : 'Back to the built-in layout'}</button>` : ''}</div>`);
  const save = async (next) => {
    try {
      await api(`/api/layouts/${scope}?target=${target}`, { method: 'PUT', body: { entries: next.map((e) => ({ type: e.type, ref: e.ref, hidden: !!e.hidden })) } });
    } catch (e) { toast(e.message); }
    render();
  };
  $('.layout-rows', node).addEventListener('click', (ev) => {
    const b = ev.target.closest('[data-act]');
    if (!b) return;
    const i = Number(b.closest('li').dataset.i);
    const next = entries.slice();
    const move = (to) => next.splice(to, 0, next.splice(i, 1)[0]);
    switch (b.dataset.act) {
      case 'top': move(0); break;
      case 'up': move(i - 1); break;
      case 'down': move(i + 1); break;
      case 'hide': next[i] = { ...next[i], hidden: !next[i].hidden }; break;
      case 'remove': next.splice(i, 1); break;
    }
    save(next);
  });
  $('#add', node)?.addEventListener('change', (ev) => { if (ev.target.value) save([...entries, options[Number(ev.target.value)]]); });
  $('#reset', node)?.addEventListener('click', async () => {
    try { await api(`/api/layouts/${scope}?target=${target}`, { method: 'DELETE' }); } catch (e) { toast(e.message); }
    render();
  });
  return node;
}
