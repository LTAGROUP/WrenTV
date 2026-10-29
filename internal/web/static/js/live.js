// Live TV: what's on, as an overview or a guide. The picked programme fills
// the top with its details and a muted live preview of its channel; below,
// either shelves of games, the viewer's shows and channels and what's on by
// kind, or filters and a grid of every channel's programmes with a now line.
import { api } from './api.js';
import { esc, $, $$, el, toast, icons, pictureInPicture } from './ui.js';
import { setTitle } from './app.js';

const ROW = 68;          // guide row height, px
const HALF = 1800;       // the guide moves half an hour at a time
const PREVIEW_AFTER = 900; // ms a channel stays picked before its preview starts

const GENRES = [['sports', 'Sports'], ['news', 'News'], ['movies', 'Movies'], ['kids', 'Kids'], ['music', 'Music'], ['entertainment', 'Entertainment']];

const now = () => Date.now() / 1000;
const halfHour = (t) => t - (t % HALF);
const clock = (t) => new Date(t * 1000).toLocaleTimeString([], { hour: 'numeric', minute: '2-digit' });
const day = (t) => new Date(t * 1000).toLocaleDateString([], { weekday: 'long', month: 'short', day: 'numeric' });
const left = (p) => Math.max(0, Math.round((p.end - now()) / 60));
const progress = (p) => (p.end > p.start ? Math.min(Math.max((now() - p.start) / (p.end - p.start), 0), 1) : 0);
const airing = (p) => p && p.start <= now() && now() < p.end;
const initials = (n) => n.split(/\s+/).slice(0, 2).map((w) => w[0] || '').join('').toUpperCase();

function logo(ch, cls = 'ch-logo') {
  const mark = `<b>${esc(initials(ch.name))}</b>`;
  return ch.logo ? `<span class="${cls}"><img src="${esc(ch.logo)}" alt="" loading="lazy" onerror="this.parentNode.classList.add('broken')">${mark}</span>`
    : `<span class="${cls} broken">${mark}</span>`;
}

function badges(p) {
  return `${p.live ? '<span class="lv-badge live">LIVE</span>' : ''}${p.new ? '<span class="lv-badge">NEW</span>' : ''}`;
}

// A show's title as the server keys followed shows: lower case, words only.
const showKey = (t) => (t || '').toLowerCase().split(/[^\p{L}\p{N}]+/u).filter(Boolean).join(' ');

// "7:00 PM", "Tomorrow 7:00 PM" or "Sat 7:00 PM".
function startsAt(t) {
  const d = Math.round((new Date(t * 1000).setHours(0, 0, 0, 0) - new Date().setHours(0, 0, 0, 0)) / 86400000);
  return d === 0 ? clock(t) : d === 1 ? `Tomorrow ${clock(t)}` : `${new Date(t * 1000).toLocaleDateString([], { weekday: 'short' })} ${clock(t)}`;
}

// Two teams over their colours, like a scoreboard.
function matchup(m, big = false) {
  const col = (t, d) => (/^[0-9a-f]{6}$/i.test(t.color || '') ? `#${t.color}` : d);
  const team = (t) => `<span class="lv-team">${t.logo ? `<img src="${esc(t.logo)}" alt="" loading="lazy">` : `<i>${esc(initials(t.name))}</i>`}<small>${esc(t.short || t.name)}</small></span>`;
  return `<span class="lv-match${big ? ' big' : ''}" style="--a:${col(m.away, '#2b2833')};--h:${col(m.home, '#3a3542')}">${team(m.away)}<em>@</em>${team(m.home)}</span>`;
}
const gameTitle = (m, short = false) => `${short ? m.away.short || m.away.name : m.away.name} at ${short ? m.home.short || m.home.name : m.home.name}`;

function prefs() {
  try { return JSON.parse(localStorage.getItem('wren.live') || '{}'); } catch { return {}; }
}
function savePrefs(p) {
  try { localStorage.setItem('wren.live', JSON.stringify({ ...prefs(), ...p })); } catch { /* private window */ }
}

// ---------------------------------------------------------------- streams

// A channel's stream on a <video>: hls.js where the browser lacks HLS.
class Stream {
  constructor(video) { this.video = video; this.hls = null; this.sid = null; }

  async play(ch, { preview = false } = {}) {
    this.stop();
    const want = ch.id;
    this.want = want;
    const r = await api(`/api/livetv/channels/${encodeURIComponent(ch.id)}/play`, { method: 'POST', body: { hls: true, preview } });
    if (this.want !== want) { stopSession(r.sessionId); return; }
    this.sid = r.sessionId;
    if (r.type !== 'hls') throw new Error('This channel needs FFmpeg on the server to play in a browser.');
    const v = this.video;
    const native = v.canPlayType('application/vnd.apple.mpegurl') && /^((?!chrome|android).)*safari/i.test(navigator.userAgent);
    if (!native && window.Hls?.isSupported()) {
      const hls = new window.Hls({ liveSyncDurationCount: 3, enableWorker: true });
      this.hls = hls;
      let retries = 0;
      hls.on(window.Hls.Events.FRAG_LOADED, () => { retries = 0; });
      hls.on(window.Hls.Events.ERROR, (_, d) => {
        if (!d.fatal || this.hls !== hls) return;
        // A few retries ride out a hiccup; a channel that stays down is
        // given up on rather than polled forever.
        if (retries++ >= 4) { this.stop(); this.onfail?.(); return; }
        if (d.type === window.Hls.ErrorTypes.MEDIA_ERROR) hls.recoverMediaError();
        else setTimeout(() => this.hls === hls && hls.startLoad(), 1000 * retries);
      });
      hls.loadSource(r.url);
      hls.attachMedia(v);
    } else {
      v.src = r.url;
    }
    await v.play().catch(() => {});
  }

  stop() {
    this.want = null;
    this.hls?.destroy();
    this.hls = null;
    this.video.removeAttribute('src');
    this.video.load();
    if (this.sid) stopSession(this.sid);
    this.sid = null;
  }
}

const stopSession = (sid) => sid && api(`/api/sessions/${encodeURIComponent(sid)}`, { method: 'DELETE' }).catch(() => {});

// ---------------------------------------------------------------- the page

export async function viewLive() {
  setTitle('Live TV');
  const data = await api('/api/livetv/channels');
  const s = {
    channels: data.channels || [],
    favorites: new Set(data.favorites || []),
    recents: data.recents || [],
    shows: new Set((data.shows || []).map(showKey)),
    view: prefs().view === 'guide' ? 'guide' : 'overview',
    shelves: null, // the overview's, once loaded
    guide: new Map(),
    loaded: [0, 0], // the guide's window, Unix seconds
    filter: prefs().filter || 'all',
    providers: data.providers || [],
    provider: prefs().provider || '', // with more than one provider, the guide shows one ('' for all)
    start: halfHour(now()),
    pick: null, // {ch, p, tile}
    query: '',
  };
  if (!s.channels.length) {
    return `<div class="empty"><h2>No channels</h2><p class="muted">Add an IPTV playlist under Settings → Live TV on the server's dashboard.</p></div>`;
  }
  const node = el(`<div class="live">
    <section class="lv-hero">
      <div class="lv-preview"><video muted playsinline></video><div class="lv-art"></div>
        <div class="lv-pv-bar"><span class="lv-badge live">LIVE</span><span class="lv-pv-note"></span>
        <button class="lv-icon" data-a="unmute" title="Sound">${speaker(true)}</button>
        <button class="lv-icon" data-a="watch" title="Full screen">${expand}</button></div></div>
      <div class="lv-info"></div>
    </section>
    <div class="lv-tools">
      <div class="lv-tabs"><button class="chip" data-view="overview">Overview</button><button class="chip" data-view="guide">Guide</button></div>
      <div class="lv-chips"></div>
      <label class="lv-search">${searchIcon}<input type="search" placeholder="Search channels and shows" autocomplete="off"></label>
    </div>
    <div class="lv-results" hidden></div>
    <div class="lv-overview"></div>
    <div class="lv-guide">
      <div class="lv-ruler"><div class="lv-day"><button class="lv-icon" data-a="prev" title="Earlier">${chevron(-1)}</button>
        <button class="btn small ghost" data-a="now">Now</button><button class="lv-icon" data-a="next" title="Later">${chevron(1)}</button></div>
        <div class="lv-times"></div></div>
      <div class="lv-body"><div class="lv-nowline"></div></div>
    </div>
  </div>`);

  const video = $('.lv-preview video', node);
  const preview = new Stream(video);
  const body = $('.lv-body', node);
  const times = $('.lv-times', node);
  const overview = $('.lv-overview', node);
  let shown = [];
  let previewTimer = null;

  // ----- data

  async function loadGuide(from, hours) {
    const g = await api(`/api/livetv/guide?from=${Math.floor(from)}&hours=${hours}`).catch(() => null);
    if (!g) return;
    for (const c of g.channels) {
      const have = s.guide.get(c.id) || [];
      const seen = new Set(have.map((p) => p.start));
      s.guide.set(c.id, [...have, ...c.programs.filter((p) => !seen.has(p.start))].sort((a, b) => a.start - b.start));
    }
    s.loaded = [Math.min(s.loaded[0] || from, from), Math.max(s.loaded[1], from + hours * 3600)];
  }
  const programs = (ch) => s.guide.get(ch.id) || [ch.now, ch.next].filter(Boolean);
  const onNow = (ch) => programs(ch).find(airing) || null;

  // ----- filters

  // The chosen provider's channels, or all of them.
  function mine() {
    const p = s.providers.length > 1 && s.providers.find((x) => x.id === s.provider);
    return p ? s.channels.filter((c) => c.source === p.id) : s.channels;
  }

  function list() {
    const f = s.filter;
    let out = mine();
    if (f === 'favorites') out = out.filter((c) => s.favorites.has(c.id));
    else if (f === 'recent') { const by = new Map(out.map((c) => [c.id, c])); out = s.recents.map((id) => by.get(id)).filter(Boolean); }
    else if (f.startsWith('genre:')) out = out.filter((c) => c.genre === f.slice(6));
    else if (f.startsWith('group:')) out = out.filter((c) => c.group === f.slice(6));
    return out;
  }

  function chips() {
    const chans = mine();
    const genres = GENRES.filter(([g]) => chans.some((c) => c.genre === g));
    const groups = [...new Set(chans.map((c) => c.group).filter(Boolean))];
    const chip = (id, label, extra = '') => `<button class="chip${s.filter === id ? ' on' : ''}" data-f="${esc(id)}">${extra}${esc(label)}</button>`;
    $('.lv-chips', node).innerHTML = (s.providers.length > 1 ? `<select class="lv-groups lv-providers" aria-label="Provider"><option value="">All providers</option>${s.providers.map((p) =>
        `<option value="${esc(p.id)}"${s.provider === p.id ? ' selected' : ''}>${esc(p.name)}</option>`).join('')}</select>` : '')
      + chip('all', 'All channels')
      + chip('favorites', 'Favorites', star(true))
      + (s.recents.length ? chip('recent', 'Recent') : '')
      + genres.map(([g, l]) => chip(`genre:${g}`, l)).join('')
      + (groups.length > 1 ? `<select class="lv-groups" aria-label="Playlist group"><option value="">Groups…</option>${groups.map((g) =>
        `<option value="${esc(g)}"${s.filter === `group:${g}` ? ' selected' : ''}>${esc(g)}</option>`).join('')}</select>` : '');
  }

  // ----- the grid

  const span = () => (innerWidth < 820 ? 3600 : innerWidth < 1300 ? 5400 : 7200);
  const chcol = () => parseFloat(getComputedStyle(node).getPropertyValue('--chcol')) || 232;
  const gridWidth = () => Math.max(body.clientWidth - chcol(), 120);
  const xOf = (t) => ((t - s.start) / span()) * gridWidth();

  function ruler() {
    const n = span() / HALF;
    let html = '';
    for (let i = 0; i < n; i++) {
      const t = s.start + i * HALF;
      html += `<span style="left:${(i / n) * 100}%">${i === 0 && s.start === halfHour(now()) ? 'Today · ' : ''}${clock(t)}</span>`;
    }
    times.innerHTML = html;
    times.title = day(s.start);
    $('[data-a="prev"]', node).disabled = s.start <= halfHour(now());
    $('[data-a="now"]', node).disabled = s.start === halfHour(now());
  }

  function rowHtml(ch, i) {
    const end = s.start + span();
    const ps = programs(ch).filter((p) => p.end > s.start && p.start < end);
    const w = gridWidth();
    const cells = ps.length ? ps.map((p) => {
      const x0 = Math.max(0, xOf(p.start)), x1 = Math.min(w, xOf(p.end));
      const on = airing(p);
      const picked = s.pick?.ch.id === ch.id && s.pick?.p?.start === p.start;
      const cont = p.start < s.start ? '<i class="lv-cont">‹</i>' : '';
      return `<button class="lv-prog${on ? ' on' : ''}${picked ? ' picked' : ''}" data-ch="${esc(ch.id)}" data-start="${p.start}"
        style="left:${x0}px;width:${Math.max(x1 - x0 - 4, 2)}px" title="${esc(p.title)}">
        ${on ? `<span class="lv-fill" style="width:${progress(p) * 100}%"></span>` : ''}
        <b>${cont}${esc(p.title)}</b><small>${badges(p)}${esc(p.episode ? `${p.episode} · ` : '')}${on ? `${left(p)} min left` : `${clock(p.start)} – ${clock(p.end)}`}</small></button>`;
    }).join('') : `<button class="lv-prog none" data-ch="${esc(ch.id)}" style="left:0;width:${w - 4}px"><b>${esc(ch.name)}</b><small>No guide information</small></button>`;
    const fav = s.favorites.has(ch.id);
    return `<div class="lv-row${s.pick?.ch.id === ch.id ? ' picked' : ''}" style="top:${i * ROW}px" data-row="${esc(ch.id)}">
      <div class="lv-ch" data-ch="${esc(ch.id)}" title="Watch ${esc(ch.name)}">${logo(ch)}<span class="lv-ch-text"><b>${esc(ch.name)}</b><small>${esc(ch.number || '')}</small></span>
        <button class="lv-star${fav ? ' on' : ''}" data-fav="${esc(ch.id)}" title="${fav ? 'Remove from favorites' : 'Add to favorites'}">${star(fav)}</button></div>
      <div class="lv-cells">${cells}</div></div>`;
  }

  // Only the rows on screen are drawn: a playlist can have thousands.
  function draw() {
    if (s.view !== 'guide') return;
    const top = body.getBoundingClientRect().top;
    const first = Math.max(0, Math.floor(-top / ROW) - 6);
    const last = Math.min(shown.length, Math.ceil((innerHeight - top) / ROW) + 6);
    let html = '';
    for (let i = first; i < last; i++) html += rowHtml(shown[i], i);
    for (const r of $$('.lv-row', body)) r.remove();
    body.insertAdjacentHTML('beforeend', html);
    const x = xOf(now());
    const line = $('.lv-nowline', body);
    line.hidden = x < 0 || x > gridWidth();
    line.style.left = `${chcol() + x}px`;
  }

  // The toolbar sticks under the top bar, and the time ruler under both.
  function stick() {
    const bar = $('.topbar')?.offsetHeight || 0;
    node.style.setProperty('--tools-top', `${bar}px`);
    node.style.setProperty('--ruler-top', `${bar + $('.lv-tools', node).offsetHeight}px`);
  }

  function relist() {
    stick();
    shown = list();
    body.style.height = `${Math.max(shown.length, 1) * ROW}px`;
    body.classList.toggle('empty-list', !shown.length);
    body.dataset.empty = s.filter === 'favorites' ? 'Star channels to keep them here.' : 'No channels here.';
    chips();
    ruler();
    draw();
  }

  // ----- the overview

  // Which of the overview, the guide and search results are on show.
  function parts() {
    const searching = !!s.query;
    $('.lv-results', node).hidden = !searching;
    overview.hidden = searching || s.view !== 'overview';
    $('.lv-guide', node).hidden = searching || s.view !== 'guide';
    $('.lv-chips', node).hidden = s.view !== 'guide';
    for (const b of $$('.lv-tabs .chip', node)) b.classList.toggle('on', b.dataset.view === s.view);
  }

  function setView(v) {
    s.view = v;
    savePrefs({ view: v });
    parts();
    window.scrollTo({ top: 0 });
    if (v === 'overview') loadOverview(); else relist();
  }

  let overviewSeq = 0;
  async function loadOverview() {
    const seq = ++overviewSeq;
    try {
      const r = await api('/api/livetv/overview');
      if (seq !== overviewSeq) return;
      s.shelves = (r.shelves || []).filter((sh) => sh.tiles?.length);
    } catch (e) {
      if (seq !== overviewSeq) return;
      s.shelves = null;
      overview.innerHTML = `<div class="empty"><h2>No overview</h2><p class="muted">${e.status === 404
        ? 'Your WrenTV server needs an update for the Live TV overview. The guide works as before.' : esc(e.message)}</p></div>`;
      return;
    }
    drawOverview();
  }

  function card(tile, i, j) {
    const ch = s.channels.find((c) => c.id === tile.channelId);
    if (!ch) return '';
    const p = tile.program, m = tile.matchup;
    const later = p && p.start > now();
    const art = m ? matchup(m) : tile.art ? `<img class="lv-card-pic" src="${esc(tile.art)}" alt="" loading="lazy" onerror="this.remove()">${logo(ch, 'ch-logo huge')}` : logo(ch, 'ch-logo huge');
    const title = m ? gameTitle(m, true) : p?.title || ch.name;
    const sub = p && airing(p) ? `${ch.name} · ${left(p)} min left` : p ? ch.name : ch.group || '';
    const picked = s.pick?.tile === tile;
    return `<button class="lv-card${picked ? ' picked' : ''}" data-tile="${i}:${j}" title="${esc(title)}">
      <span class="lv-card-art">${art}
        <span class="lv-card-tags">${later ? `<span class="lv-badge time">${esc(startsAt(p.start))}</span>` : p ? badges(p) : ''}</span>
        ${m || tile.art ? logo(ch, 'ch-logo bug') : ''}
        ${p && airing(p) ? `<span class="lv-card-bar"><i style="width:${progress(p) * 100}%"></i></span>` : ''}
        <span class="lv-card-more" data-more="${i}:${j}" title="Details and options">${moreIcon}</span></span>
      <b class="t">${esc(title)}</b><small class="s">${esc(sub)}</small></button>`;
  }

  function drawOverview() {
    if (!s.shelves) return;
    overview.innerHTML = s.shelves.length ? s.shelves.map((sh, i) => `<section class="shelf"><div class="shelf-head"><h2>${esc(sh.title)}</h2></div>
      <div class="row">${sh.tiles.map((t, j) => card(t, i, j)).join('')}</div></section>`).join('')
      : '<div class="empty"><h2>Nothing to show yet</h2><p class="muted">Once the guide has loaded, games, your shows and what\'s on gather here.</p></div>';
  }

  const tileAt = (ref) => {
    const [i, j] = ref.split(':').map(Number);
    return s.shelves?.[i] && { tile: s.shelves[i].tiles[j], shelf: s.shelves[i] };
  };

  // A card shows its details up top, or plays when it's on now.
  function openCard(ref, details) {
    const at = tileAt(ref);
    const ch = at && s.channels.find((c) => c.id === at.tile.channelId);
    if (!ch) return;
    const p = at.tile.program || null;
    if (!details && (!p || airing(p))) {
      const ids = at.shelf.tiles.map((t) => t.channelId);
      const by = new Map(s.channels.map((c) => [c.id, c]));
      const chans = [...new Set(ids)].map((id) => by.get(id)).filter(Boolean);
      return watch(ch, () => chans);
    }
    pick(ch, p, { tile: at.tile });
    window.scrollTo({ top: 0, behavior: 'smooth' });
  }

  async function toggleFollow(title) {
    const on = !s.shows.has(showKey(title));
    try {
      await api('/api/livetv/shows', { method: 'POST', body: { title, on } });
      if (on) s.shows.add(showKey(title)); else s.shows.delete(showKey(title));
      toast(on ? `Following “${title}”: it gathers under Your shows` : `Stopped following “${title}”`, 2200);
      info();
      if (s.view === 'overview') loadOverview();
    } catch (e) { toast(e.status === 404 ? 'Your WrenTV server needs an update to follow shows.' : e.message); }
  }

  // ----- the picked programme

  function info() {
    const box = $('.lv-info', node);
    if (!s.pick) { box.innerHTML = ''; return; }
    const { ch, p, tile } = s.pick;
    const m = tile?.matchup;
    const on = airing(p);
    const nextUp = p && programs(ch).find((q) => q.start >= p.end);
    const fav = s.favorites.has(ch.id);
    const cats = (p?.categories || []).slice(0, 3);
    box.innerHTML = `<div class="lv-chan">${logo(ch, 'ch-logo big')}<span><b>${esc(ch.name)}</b>${ch.number ? `<small>Channel ${esc(ch.number)}</small>` : ''}</span></div>
      <h1>${esc(m ? gameTitle(m) : p?.title || ch.name)}</h1>
      <div class="lv-meta">${p ? `${badges(p)}<span>${on ? `${clock(p.start)} – ${clock(p.end)} · ${left(p)} min left` : `${day(p.start) === day(now()) ? '' : day(p.start) + ' · '}${clock(p.start)} – ${clock(p.end)}`}</span>` : ''}
        ${p?.episode ? `<span>${esc(p.episode)}</span>` : ''}${cats.map((c) => `<span class="lv-cat">${esc(c)}</span>`).join('')}</div>
      ${on ? `<div class="lv-progress"><span style="width:${progress(p) * 100}%"></span></div>` : ''}
      ${p?.description ? `<p class="lv-desc">${esc(p.description)}</p>` : ''}
      <div class="lv-actions"><button class="btn primary" data-a="watch">${icons.play}<span>${on || !p ? 'Watch live' : 'Watch channel'}</span></button>
        <button class="btn ghost" data-fav="${esc(ch.id)}">${star(fav)}<span>${fav ? 'Favorite' : 'Add to favorites'}</span></button>
        ${p && !m ? `<button class="btn ghost${s.shows.has(showKey(p.title)) ? ' on' : ''}" data-follow="${esc(p.title)}"
          title="Its airings, on any channel, gather under Your shows on the overview">${s.shows.has(showKey(p.title)) ? `${checkIcon}<span>Following</span>` : `${plusIcon}<span>Follow show</span>`}</button>` : ''}</div>
      ${nextUp ? `<div class="lv-next"><small>Up next · ${clock(nextUp.start)}</small><b>${esc(nextUp.title)}</b></div>` : ''}`;
    const art = $('.lv-art', node);
    const pic = m ? '' : tile?.art || p?.icon || '';
    art.style.backgroundImage = pic ? `url("${pic.replace(/"/g, '%22')}")` : '';
    art.innerHTML = m ? matchup(m, true) : pic ? '' : logo(ch, 'ch-logo huge');
  }

  function pick(ch, p, { previewNow = false, tile = null } = {}) {
    const changed = s.pick?.ch.id !== ch.id;
    s.pick = { ch, p: p ?? onNow(ch), tile };
    info();
    for (const c of $$('.lv-card.picked', overview)) c.classList.remove('picked');
    if (tile) for (const c of $$('.lv-card', overview)) if (tileAt(c.dataset.tile)?.tile === tile) c.classList.add('picked');
    for (const r of $$('.lv-row.picked, .lv-prog.picked', body)) r.classList.remove('picked');
    $(`.lv-row[data-row="${CSS.escape(ch.id)}"]`, body)?.classList.add('picked');
    if (s.pick.p) $(`.lv-prog[data-ch="${CSS.escape(ch.id)}"][data-start="${s.pick.p.start}"]`, body)?.classList.add('picked');
    if (changed) startPreview(previewNow);
  }

  function startPreview(soon) {
    clearTimeout(previewTimer);
    const note = $('.lv-pv-note', node);
    node.classList.remove('previewing');
    preview.stop();
    if (prefs().previews === false) { note.textContent = 'Previews off'; return; }
    note.textContent = '';
    const ch = s.pick.ch;
    previewTimer = setTimeout(async () => {
      try {
        video.onplaying = () => { if (s.pick?.ch === ch) node.classList.add('previewing'); };
        preview.onfail = () => { node.classList.remove('previewing'); note.textContent = 'Preview ended'; };
        video.muted = !s.sound;
        await preview.play(ch, { preview: true });
      } catch (e) {
        if (s.pick?.ch === ch) note.textContent = e.status === 429 ? 'No free streams for a preview' : 'Preview unavailable';
      }
    }, soon ? 150 : PREVIEW_AFTER);
  }

  // ----- search

  let searchSeq = 0, searchTimer = null;
  function search(q) {
    s.query = q.trim();
    const box = $('.lv-results', node);
    clearTimeout(searchTimer);
    if (!s.query) { parts(); if (s.view === 'guide') relist(); return; }
    searchTimer = setTimeout(async () => {
      const seq = ++searchSeq;
      const r = await api(`/api/livetv/search?q=${encodeURIComponent(s.query)}&limit=40`).catch(() => null);
      if (seq !== searchSeq || !r) return;
      parts();
      const byId = new Map(s.channels.map((c) => [c.id, c]));
      box.innerHTML = (!r.channels.length && !r.airings.length) ? `<div class="empty"><h2>Nothing found</h2><p class="muted">No channel or upcoming show matches “${esc(s.query)}”.</p></div>`
        : `${r.channels.length ? `<h3>Channels</h3><div class="lv-res-chans">${r.channels.map((c) => `<button class="lv-res-chan" data-ch="${esc(c.id)}">${logo(c)}
            <span><b>${esc(c.name)}</b><small>${esc(c.now ? `${c.now.title}` : c.number || '')}</small></span></button>`).join('')}</div>` : ''}
          ${r.airings.length ? `<h3>Shows</h3><div class="lv-res-list">${r.airings.map((a) => {
            const c = byId.get(a.channelId); if (!c) return '';
            const p = a.program;
            return `<button class="lv-res" data-ch="${esc(c.id)}" data-start="${p.start}">
              <span class="lv-res-art"${p.icon ? ` style="background-image:url('${esc(p.icon)}')"` : ''}>${p.icon ? '' : logo(c)}</span>
              <span class="lv-res-text"><b>${esc(p.title)}</b><small>${badges(p)}${airing(p) ? '<span class="lv-badge on">ON NOW</span>' : ''}${esc(p.episode ? p.episode + ' · ' : '')}${esc(c.name)} · ${day(p.start) === day(now()) ? '' : day(p.start) + ' '}${clock(p.start)}</small></span></button>`;
          }).join('')}</div>` : ''}`;
    }, 220);
  }

  // ----- full screen

  // Channel up and down go through [chans]: the shelf or the filtered guide.
  function watch(ch, chans = () => (list().length ? list() : s.channels)) {
    clearTimeout(previewTimer);
    preview.stop();
    node.classList.remove('previewing');
    openLive(ch, chans, {
      programs, onNow,
      onClose: (last) => {
        s.recents = [last.id, ...s.recents.filter((id) => id !== last.id)].slice(0, 20);
        pick(last, null);
        if (s.view === 'overview') loadOverview(); else relist();
      },
    });
  }

  async function toggleFav(id) {
    const on = !s.favorites.has(id);
    try {
      await api(`/api/livetv/favorites/${encodeURIComponent(id)}`, { method: on ? 'PUT' : 'DELETE' });
      if (on) s.favorites.add(id); else s.favorites.delete(id);
      toast(on ? 'Added to favorites' : 'Removed from favorites', 1600);
      if (s.view === 'overview') loadOverview(); else relist();
      info();
    } catch (e) { toast(e.message); }
  }

  // ----- events

  node.addEventListener('click', (e) => {
    const t = e.target;
    const fav = t.closest('[data-fav]');
    if (fav) { e.stopPropagation(); return toggleFav(fav.dataset.fav); }
    const follow = t.closest('[data-follow]');
    if (follow) return toggleFollow(follow.dataset.follow);
    const view = t.closest('[data-view]');
    if (view) return view.dataset.view !== s.view && setView(view.dataset.view);
    const more = t.closest('[data-more]');
    if (more) return openCard(more.dataset.more, true);
    const tileCard = t.closest('.lv-card');
    if (tileCard) return openCard(tileCard.dataset.tile, false);
    const chip = t.closest('[data-f]');
    if (chip) { s.filter = chip.dataset.f; savePrefs({ filter: s.filter }); window.scrollTo({ top: 0 }); return relist(); }
    const a = t.closest('[data-a]')?.dataset.a;
    if (a === 'prev' || a === 'next' || a === 'now') {
      s.start = a === 'now' ? halfHour(now()) : Math.max(halfHour(now()), s.start + (a === 'next' ? HALF : -HALF));
      if (s.start + span() > s.loaded[1]) loadGuide(s.loaded[1], 6).then(relist);
      return relist();
    }
    if (a === 'watch' && s.pick) return watch(s.pick.ch);
    if (a === 'unmute') {
      s.sound = !s.sound;
      video.muted = !s.sound;
      t.closest('button').innerHTML = speaker(!s.sound);
      return;
    }
    const cell = t.closest('.lv-prog, .lv-res, .lv-res-chan');
    if (cell) {
      const ch = s.channels.find((c) => c.id === cell.dataset.ch);
      if (!ch) return;
      const p = cell.dataset.start ? programs(ch).find((q) => String(q.start) === cell.dataset.start)
        || (cell.classList.contains('lv-res') ? null : undefined) : null;
      // Picking what's on twice, or a search result, watches it.
      if (s.pick?.ch.id === ch.id && (!p || airing(p)) && cell.classList.contains('lv-prog') && (s.pick.p?.start === p?.start)) return watch(ch);
      if (cell.classList.contains('lv-res-chan')) return watch(ch);
      pick(ch, p);
      if (cell.classList.contains('lv-res')) window.scrollTo({ top: 0, behavior: 'smooth' });
      return;
    }
    const chCell = t.closest('.lv-ch');
    if (chCell) {
      const ch = s.channels.find((c) => c.id === chCell.dataset.ch);
      if (ch) watch(ch);
    }
  });
  node.addEventListener('change', (e) => {
    if (e.target.matches('.lv-providers')) {
      s.provider = e.target.value;
      // A group belongs to one provider's playlist.
      if (s.filter.startsWith('group:')) s.filter = 'all';
      savePrefs({ provider: s.provider, filter: s.filter });
      relist();
    } else if (e.target.matches('.lv-groups')) {
      s.filter = e.target.value ? `group:${e.target.value}` : 'all';
      savePrefs({ filter: s.filter });
      relist();
    }
  });
  $('.lv-search input', node).addEventListener('input', (e) => search(e.target.value));

  // Arrow keys move through the guide; Enter watches.
  const onKey = (e) => {
    if (!document.body.contains(node)) return;
    if (e.target.closest('input, select, textarea') || $('.lv-player') || s.view !== 'guide' || s.query) return;
    if (!s.pick || !['ArrowUp', 'ArrowDown', 'ArrowLeft', 'ArrowRight', 'Enter'].includes(e.key)) return;
    e.preventDefault();
    const i = shown.findIndex((c) => c.id === s.pick.ch.id);
    if (e.key === 'Enter') return watch(s.pick.ch);
    if (e.key === 'ArrowUp' || e.key === 'ArrowDown') {
      const ch = shown[Math.min(Math.max(i + (e.key === 'ArrowUp' ? -1 : 1), 0), shown.length - 1)];
      if (!ch) return;
      const t = s.pick.p ? Math.max(s.pick.p.start, s.start) : now();
      pick(ch, programs(ch).find((p) => p.start <= t && t < p.end) || onNow(ch));
      const rowTop = body.getBoundingClientRect().top + scrollY + shown.indexOf(ch) * ROW;
      if (rowTop < scrollY + 160 || rowTop + ROW > scrollY + innerHeight) window.scrollTo({ top: rowTop - innerHeight / 2 });
    } else {
      const ps = programs(s.pick.ch);
      const j = ps.findIndex((p) => p.start === s.pick.p?.start);
      const p = ps[j + (e.key === 'ArrowLeft' ? -1 : 1)];
      if (!p || p.end <= now()) return;
      if (p.start >= s.start + span()) s.start += HALF;
      if (p.end <= s.start) s.start = Math.max(halfHour(now()), s.start - HALF);
      s.pick = { ch: s.pick.ch, p };
      info();
      relist();
    }
  };
  document.addEventListener('keydown', onKey);
  const onScroll = () => requestAnimationFrame(draw);
  addEventListener('scroll', onScroll, { passive: true });
  addEventListener('resize', onScroll);
  addEventListener('resize', stick);

  // Keep the now line, progress, the guide and the overview current while
  // the page is open.
  let ticks = 0;
  const ticker = setInterval(async () => {
    if (!document.body.contains(node)) {
      clearInterval(ticker);
      clearTimeout(previewTimer);
      preview.stop();
      document.removeEventListener('keydown', onKey);
      removeEventListener('scroll', onScroll);
      removeEventListener('resize', onScroll);
      removeEventListener('resize', stick);
      return;
    }
    if (s.start + span() <= now()) s.start = halfHour(now());
    if (now() + 4 * 3600 > s.loaded[1]) await loadGuide(s.loaded[1], 6);
    draw();
    info();
    if (s.view === 'overview' && !s.query && ++ticks % 4 === 0) loadOverview();
    else if (s.view === 'overview') drawOverview();
  }, 30000);

  await loadGuide(halfHour(now()), 12);
  // Start on the last channel watched, or the first one.
  const first = s.channels.find((c) => c.id === s.recents[0]) || list()[0] || s.channels[0];
  // The grid is measured, so it's first drawn once the page is on screen.
  const attached = async () => {
    if (!node.isConnected) return requestAnimationFrame(attached);
    parts();
    relist();
    if (s.view === 'overview') {
      await loadOverview();
      // The overview opens on its first card, often a game on now.
      const t = s.shelves?.[0]?.tiles[0];
      const ch = t && s.channels.find((c) => c.id === t.channelId);
      if (ch) return pick(ch, t.program || null, { tile: t });
    }
    if (first) pick(first, null);
  };
  requestAnimationFrame(attached);
  return node;
}

// ---------------------------------------------------------------- the player

// openLive plays a channel full screen. Up and down (or the buttons) change
// channel through [listFn]'s channels; the guide strip shows what's on.
export function openLive(start, listFn, { programs, onNow, onClose }) {
  let ch = start;
  let last = null;
  const ov = el(`<div class="lv-player" tabindex="-1">
    <video playsinline autoplay></video>
    <div class="lv-p-top"><button class="lv-icon" data-a="close" title="Back to the guide (Esc)">${icons.back}</button>
      <div class="lv-p-title"></div><button class="lv-icon" data-a="pip" title="Picture in picture">${pipIcon}</button>
      <button class="lv-icon" data-a="mute" title="Sound (M)">${speaker(false)}</button><button class="lv-icon" data-a="fs" title="Full screen (F)">${expand}</button></div>
    <div class="lv-p-msg" hidden></div>
    <div class="lv-p-bottom"><button class="lv-icon" data-a="up" title="Previous channel (↑)">${chevron(-1, true)}</button>
      <div class="lv-p-strip"></div><button class="lv-icon" data-a="down" title="Next channel (↓)">${chevron(1, true)}</button></div>
  </div>`);
  const video = $('video', ov);
  const stream = new Stream(video);
  const pip = pictureInPicture(video, $('[data-a="pip"]', ov));
  stream.onfail = () => {
    ov.classList.remove('tuning');
    const msg = $('.lv-p-msg', ov);
    msg.hidden = false;
    msg.textContent = `${ch.name} stopped playing. Pick it again to retry.`;
  };
  document.body.append(ov);
  ov.focus();

  let hideT = null;
  const wake = () => {
    ov.classList.add('awake');
    clearTimeout(hideT);
    hideT = setTimeout(() => ov.classList.remove('awake'), 3500);
  };

  function strip() {
    const chans = listFn();
    const i = Math.max(0, chans.findIndex((c) => c.id === ch.id));
    const around = [-2, -1, 0, 1, 2].map((d) => chans[(i + d + chans.length) % chans.length]).filter(Boolean);
    $('.lv-p-strip', ov).innerHTML = [...new Map(around.map((c) => [c.id, c])).values()].map((c) => {
      const p = onNow(c);
      return `<button class="lv-p-chan${c.id === ch.id ? ' on' : ''}" data-ch="${esc(c.id)}">${logo(c)}<span><b>${esc(p?.title || c.name)}</b>
        <small>${esc(c.number ? c.number + ' · ' : '')}${esc(c.name)}${p ? ` · ${left(p)} min left` : ''}</small>
        ${p ? `<i class="lv-p-prog"><i style="width:${progress(p) * 100}%"></i></i>` : ''}</span></button>`;
    }).join('');
    const p = onNow(ch);
    $('.lv-p-title', ov).innerHTML = `${logo(ch)}<span><b>${esc(p?.title || ch.name)}</b><small>${esc(ch.number ? ch.number + ' · ' : '')}${esc(ch.name)}${p ? ` · ${clock(p.start)} – ${clock(p.end)}` : ''}</small></span>`;
  }

  async function tune(next) {
    if (next.id !== ch.id) last = ch;
    ch = next;
    strip();
    wake();
    const msg = $('.lv-p-msg', ov);
    msg.hidden = true;
    ov.classList.add('tuning');
    try {
      await stream.play(ch);
    } catch (e) {
      msg.hidden = false;
      msg.textContent = e.status === 429 ? 'Every stream is in use right now. Try again in a moment.' : e.message;
    }
  }
  video.addEventListener('playing', () => ov.classList.remove('tuning'));
  video.addEventListener('waiting', () => ov.classList.add('tuning'));

  const step = (d) => {
    const chans = listFn();
    const i = chans.findIndex((c) => c.id === ch.id);
    tune(chans[((i < 0 ? 0 : i) + d + chans.length) % chans.length]);
  };

  function close() {
    pip.stop();
    stream.stop();
    if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
    document.removeEventListener('keydown', key, true);
    clearInterval(tick);
    ov.remove();
    onClose?.(ch);
  }

  const key = (e) => {
    const k = e.key;
    if (k === 'Escape' && !document.fullscreenElement) { e.preventDefault(); return close(); }
    if (k === 'ArrowUp' || k === 'PageUp' || k === 'ChannelUp') { e.preventDefault(); step(-1); }
    else if (k === 'ArrowDown' || k === 'PageDown' || k === 'ChannelDown') { e.preventDefault(); step(1); }
    else if (k === 'Backspace' || k === 'l') { if (last) { e.preventDefault(); tune(last); } }
    else if (k === 'm') { video.muted = !video.muted; $('[data-a="mute"]', ov).innerHTML = speaker(video.muted); }
    else if (k === 'f') { fullscreen(); }
    else if (k === 'p') { pip.toggle(); }
    else return wake();
    wake();
  };
  const fullscreen = () => (document.fullscreenElement ? document.exitFullscreen() : ov.requestFullscreen()).catch(() => {});
  document.addEventListener('keydown', key, true);
  ov.addEventListener('mousemove', wake);
  ov.addEventListener('click', (e) => {
    const a = e.target.closest('[data-a]')?.dataset.a;
    if (a === 'close') return close();
    if (a === 'up') return step(-1);
    if (a === 'down') return step(1);
    if (a === 'fs') return fullscreen();
    if (a === 'pip') return pip.toggle();
    if (a === 'mute') { video.muted = !video.muted; e.target.closest('button').innerHTML = speaker(video.muted); return; }
    const c = e.target.closest('[data-ch]');
    if (c) { const next = listFn().find((x) => x.id === c.dataset.ch); if (next && (next.id !== ch.id || !stream.sid)) tune(next); return; }
    if (e.target === video) wake();
  });
  video.addEventListener('dblclick', fullscreen);
  const tick = setInterval(strip, 30000);
  tune(start);
  void programs;
}

// ---------------------------------------------------------------- icons

const searchIcon = '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2"><circle cx="11" cy="11" r="7"/><path d="M20 20l-3.5-3.5"/></svg>';
const expand = '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5"/></svg>';
const pipIcon = '<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="5" width="18" height="14" rx="2"/><rect x="12" y="11" width="7" height="6" rx="1" fill="currentColor"/></svg>';
const chevron = (d, vertical = false) => `<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2.2"><path d="${
  vertical ? (d < 0 ? 'M6 15l6-6 6 6' : 'M6 9l6 6 6-6') : (d < 0 ? 'M15 18l-6-6 6-6' : 'M9 6l6 6-6 6')}"/></svg>`;
const speaker = (muted) => `<svg viewBox="0 0 24 24" width="18" height="18" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 9h4l5-4v14l-5-4H4z"/>${
  muted ? '<path d="M17 9l4 6M21 9l-4 6"/>' : '<path d="M17 8.5a5 5 0 010 7M19.5 6a8.5 8.5 0 010 12"/>'}</svg>`;
const moreIcon = '<svg viewBox="0 0 24 24" width="16" height="16" fill="currentColor"><circle cx="5" cy="12" r="2"/><circle cx="12" cy="12" r="2"/><circle cx="19" cy="12" r="2"/></svg>';
const plusIcon = '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M12 5v14M5 12h14"/></svg>';
const checkIcon = '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.4"><path d="M5 12.5l4.5 4.5L19 7.5"/></svg>';
const star = (on) => `<svg viewBox="0 0 24 24" width="16" height="16" fill="${on ? 'currentColor' : 'none'}" stroke="currentColor" stroke-width="2"><path d="M12 3l2.7 5.6 6.1.9-4.4 4.3 1 6.1L12 17l-5.4 2.9 1-6.1-4.4-4.3 6.1-.9z"/></svg>`;
