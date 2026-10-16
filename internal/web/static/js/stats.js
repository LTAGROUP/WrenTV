// Server statistics (Tautulli-style): what's playing now, watch activity,
// most-watched titles and users, playback methods, and the library itself.
import { api, img } from './api.js';
import { esc, $, $$, el, toast, fmtDuration, fmtSize, timeAgo } from './ui.js';
import { setTitle, render } from './app.js';

const periods = [[7, '7 days'], [30, '30 days'], [90, '90 days'], [365, '1 year']];
const lsGet = (k, d) => { try { return localStorage.getItem(k) ?? d; } catch { return d; } };
const lsSet = (k, v) => { try { localStorage.setItem(k, v); } catch {} };

const fmtHours = (h) => h >= 100 ? Math.round(h).toLocaleString() : h >= 10 ? h.toFixed(0) : h.toFixed(1);
const num = (n) => Number(n || 0).toLocaleString();
const methodLabel = { direct: 'Direct play', remux: 'Direct stream', transcode: 'Transcode', 'audio transcode': 'Audio transcode' };

export async function viewStats() {
  setTitle('Stats');
  const days = Number(lsGet('wrentv.statsDays', '30'));
  const tz = -new Date().getTimezoneOffset() * 60;
  const d = await api(`/api/admin/stats?days=${days}&tz=${tz}`);
  const s = d.stats;
  const lib = d.library;
  charts.clear();

  const node = el(`<div>
    <div class="page-head"><h1>Stats</h1><div class="seg" role="group" aria-label="Period">${periods.map(([v, l]) =>
      `<button type="button" data-days="${v}" class="${v === days ? 'on' : ''}" aria-pressed="${v === days}">${l}</button>`).join('')}</div></div>

    <section id="np">${nowPlayingHtml(d.nowPlaying)}</section>

    <div class="kpis">
      ${kpi(num(s.plays), 'Plays')}${kpi(fmtHours(s.hours), 'Hours watched')}${kpi(num(s.users), 'Active viewers')}${kpi(num(s.titles), 'Titles watched')}
    </div>

    <div class="panel"><h2>Plays per day</h2>${s.plays ? columnChart(s.daily.map((x) => ({
      label: shortDate(x.date), value: x.plays, tip: `<b>${esc(longDate(x.date))}</b>${x.plays} play${x.plays === 1 ? '' : 's'} · ${fmtHours(x.hours)} h` })),
      { labelEvery: days <= 7 ? 1 : days <= 30 ? 5 : days <= 90 ? 14 : 60, height: 170 }) : emptyChart()}</div>

    <div class="stat-grid">
      <div class="panel"><h2>By time of day</h2>${s.plays ? columnChart(s.byHour.map((v, h) => ({
        label: hourLabel(h), value: v, tip: `<b>${hourLabel(h)}–${hourLabel((h + 1) % 24)}</b>${v} play${v === 1 ? '' : 's'}` })), { labelEvery: 6, height: 130 }) : emptyChart()}</div>
      <div class="panel"><h2>By day of week</h2>${s.plays ? columnChart(s.byWeekday.map((v, i) => ({
        label: weekday(i), value: v, tip: `<b>${weekday(i, 'long')}</b>${v} play${v === 1 ? '' : 's'}` })), { labelEvery: 1, height: 130 }) : emptyChart()}</div>
    </div>

    <div class="stat-grid">
      <div class="panel"><h2>Most watched movies</h2>${topList(s.topMovies, 'movie')}</div>
      <div class="panel"><h2>Most watched shows</h2>${topList(s.topShows, 'show')}</div>
    </div>

    <div class="stat-grid">
      <div class="panel"><h2>Top viewers</h2>${s.topUsers.length ? `<div class="table-wrap"><table class="table"><tr><th>Viewer</th><th style="text-align:right">Plays</th><th style="text-align:right">Hours</th><th>Last watched</th></tr>
        ${s.topUsers.map((u) => `<tr><td><b>${esc(u.name)}</b></td><td style="text-align:right">${num(u.plays)}</td><td style="text-align:right">${fmtHours(u.hours)}</td>
          <td><div style="max-width:220px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis">${esc(u.lastPlayed || '')}</div><div class="small muted">${timeAgo(u.lastSeen)}</div></td></tr>`).join('')}</table></div>` : emptyList()}</div>
      <div class="panel"><h2>How it played</h2>${s.byMethod.length ? hbars(s.byMethod.map((m) => ({ name: methodLabel[m.name] || m.name, count: m.count }))) : emptyList()}
        <h2 style="margin-top:22px">Players</h2>${s.byClient.length ? hbars(s.byClient) : emptyList()}</div>
    </div>

    <div class="panel"><h2>Recent plays</h2>${d.recent.length ? `<div class="table-wrap"><table class="table">
      <tr><th>Title</th><th>Viewer</th><th>Player</th><th>Playback</th><th style="text-align:right">Watched</th><th>When</th></tr>
      ${d.recent.map(historyRow).join('')}</table></div>` : emptyList('Plays show up here as people watch.')}</div>

    <h2 class="section-title">Library</h2>
    <div class="kpis">
      ${kpi(num(lib.movies), 'Movies')}${kpi(num(lib.shows), 'Shows')}${kpi(num(lib.episodes), 'Episodes')}
      ${kpi(fmtSize(lib.bytes) || '0 B', 'On disk')}${kpi(num(Math.round(lib.hours)), 'Hours of media')}
    </div>
    <div class="stat-grid">
      <div class="panel"><h2>Resolution</h2>${hbars(lib.resolutions)}<h2 style="margin-top:22px">Dynamic range</h2>${hbars(lib.hdr)}</div>
      <div class="panel"><h2>Video codec</h2>${hbars(lib.videoCodecs)}<h2 style="margin-top:22px">Container</h2>${hbars(lib.containers)}</div>
    </div>
    ${lib.libraries?.length ? `<div class="panel"><h2>Libraries</h2><div class="table-wrap"><table class="table"><tr><th>Name</th><th>Type</th><th style="text-align:right">Titles</th><th style="text-align:right">Size</th></tr>
      ${lib.libraries.map((l) => `<tr><td><a href="/library/${l.id}"><b>${esc(l.name)}</b></a></td><td class="muted">${l.type === 'shows' ? 'Shows' : 'Movies'}</td>
        <td style="text-align:right">${num(l.items)}</td><td style="text-align:right">${fmtSize(l.bytes) || '—'}</td></tr>`).join('')}</table></div></div>` : ''}
  </div>`);

  for (const b of $$('[data-days]', node)) b.onclick = () => { lsSet('wrentv.statsDays', b.dataset.days); render(); };
  bindCharts(node);
  bindStop(node);
  pollNowPlaying(node, days, tz);
  return node;
}

// ---------------------------------------------------------------- now playing

function nowPlayingHtml(list) {
  if (!list.length) return '<div class="panel"><h2>Now playing</h2><p class="muted" style="margin:0">Nobody is watching right now.</p></div>';
  return `<h2 class="section-title" style="margin-top:0">Now playing <span class="muted small">${list.length} stream${list.length > 1 ? 's' : ''}</span></h2>
    <div class="np-grid">${list.map((n) => {
      const pct = n.duration ? Math.min(100, (n.position / n.duration) * 100) : 0;
      const method = n.method === 'transcode' && n.videoCopy ? 'audio transcode' : n.method;
      const cls = n.method === 'transcode' ? 'transcode' : n.method === 'remux' ? 'remux' : 'direct';
      return `<div class="np">${n.backdropPath ? `<div class="bgimg" style="background-image:url('${img(n.backdropPath, 'w780')}')"></div>` : ''}
        ${n.posterPath ? `<img src="${img(n.posterPath, 'w185')}" alt="">` : '<img alt="">'}
        <div style="min-width:0"><a href="/item/${n.itemId}" style="font-weight:650;display:block;white-space:nowrap;overflow:hidden;text-overflow:ellipsis">${esc(n.title)}</a>
          ${n.subtitle ? `<div class="small muted" style="white-space:nowrap;overflow:hidden;text-overflow:ellipsis">${esc(n.subtitle)}</div>` : ''}
          <div class="small" style="margin-top:4px"><b>${esc(n.userName)}</b> <span class="muted">· ${esc(n.client || 'Unknown player')}</span></div>
          <div class="tags"><span class="tag2 ${cls}" title="${esc((n.reasons || []).join('; '))}">${esc(methodLabel[method] || method)}</span>
            ${n.quality ? `<span class="tag2">${esc(n.quality)}</span>` : ''}${n.engine === 'mpv' ? '<span class="tag2">mpv</span>' : ''}</div>
          <div class="prog"><i style="width:${pct.toFixed(1)}%"></i></div>
          <div class="small muted" style="display:flex;justify-content:space-between;margin-top:4px;font-variant-numeric:tabular-nums">
            <span>${fmtDuration(n.position)} / ${fmtDuration(n.duration)}</span><button class="btn ghost small danger" data-stop="${n.sessionId}" style="padding:2px 8px">Stop</button></div></div></div>`;
    }).join('')}</div>`;
}

function bindStop(root) {
  for (const b of $$('[data-stop]', root)) {
    b.onclick = async () => {
      await api(`/api/sessions/${b.dataset.stop}`, { method: 'DELETE' }).catch(() => {});
      toast('Stream stopped.');
      b.closest('.np')?.remove();
    };
  }
}

// Refresh the now-playing cards while the page is open.
function pollNowPlaying(node, days, tz) {
  const t = setInterval(async () => {
    if (!node.isConnected) return clearInterval(t);
    if (document.hidden) return;
    try {
      const d = await api(`/api/admin/stats?days=${days}&tz=${tz}`);
      const np = $('#np', node);
      if (!np) return clearInterval(t);
      np.innerHTML = nowPlayingHtml(d.nowPlaying);
      bindStop(np);
    } catch {}
  }, 10000);
}

// ---------------------------------------------------------------- pieces

const kpi = (v, l) => `<div class="kpi"><div class="v">${v}</div><div class="l">${esc(l)}</div></div>`;
const emptyChart = () => '<p class="muted" style="margin:0">No plays in this period yet.</p>';
const emptyList = (msg = 'Nothing in this period yet.') => `<p class="muted" style="margin:0">${esc(msg)}</p>`;

function niceMax(v) {
  if (v <= 4) return Math.max(1, v);
  const p = 10 ** Math.floor(Math.log10(v));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v) return m * p;
  return v;
}

// columnChart draws one series as columns: a single hue, rounded data ends
// on the baseline, a light grid, selective axis labels and a hover tooltip.
const charts = new Map();
let chartSeq = 0;

function columnChart(data, opts = {}) {
  const id = `c${++chartSeq}`;
  charts.set(id, { data, opts });
  const table = `<table class="sr-only"><tr><th>Label</th><th>Value</th></tr>${data.map((d) => `<tr><td>${esc(d.label)}</td><td>${d.value}</td></tr>`).join('')}</table>`;
  return `<div class="chart" data-chart="${id}" style="height:${opts.height || 160}px"></div>${table}`;
}

// drawColumns renders at the container's real pixel width so axis text keeps
// its CSS size instead of scaling with a viewBox.
function drawColumns(c) {
  const spec = charts.get(c.dataset.chart);
  if (!spec) return;
  const { data, opts: { labelEvery = 1, height = 160 } } = spec;
  const W = Math.max(120, Math.round(c.clientWidth));
  // Thin out labels when columns get too narrow for them.
  const every = Math.max(labelEvery, Math.ceil(48 / (W / data.length)));
  const max = niceMax(Math.max(...data.map((d) => d.value), 0));
  const H = height, padL = 28, padB = 20, top = 6;
  const plotW = W - padL, plotH = H - padB - top;
  const step = plotW / data.length;
  const gap = Math.min(4, step * 0.25);
  const bw = Math.max(1, Math.min(step - gap, 48));
  const y = (v) => top + plotH - (v / max) * plotH;
  const grid = [0, max / 2, max].map((v) => `<line class="gl" x1="${padL}" x2="${W}" y1="${y(v)}" y2="${y(v)}"/>
    <text class="ax" x="${padL - 6}" y="${y(v) + 4}" text-anchor="end">${Number.isInteger(v) ? v : v.toFixed(1)}</text>`).join('');
  const bars = data.map((d, i) => {
    const x = padL + i * step + (step - bw) / 2;
    const h = d.value ? Math.max(2, (d.value / max) * plotH) : 0;
    const r = Math.min(4, bw / 2, h);
    const yTop = top + plotH - h;
    // Rounded top, square base on the axis.
    const path = h ? `M${x},${top + plotH}V${yTop + r}Q${x},${yTop} ${x + r},${yTop}H${x + bw - r}Q${x + bw},${yTop} ${x + bw},${yTop + r}V${top + plotH}Z` : '';
    const label = i % every === 0 ? `<text class="ax" x="${x + bw / 2}" y="${H - 4}" text-anchor="middle">${esc(d.label)}</text>` : '';
    return `<g data-tip="${esc(d.tip)}"><rect class="hit" x="${padL + i * step}" y="${top}" width="${step}" height="${plotH}"/>${path ? `<path class="b" d="${path}"/>` : ''}${label}</g>`;
  }).join('');
  c.innerHTML = `<svg width="${W}" height="${H}" role="img" aria-label="Column chart">${grid}${bars}</svg>`;
}

function bindCharts(root) {
  for (const c of $$('.chart', root)) {
    drawColumns(c);
    let lastW = c.clientWidth;
    new ResizeObserver(() => {
      if (!c.isConnected || c.clientWidth === lastW) return;
      lastW = c.clientWidth;
      drawColumns(c);
    }).observe(c);
    let tip;
    c.addEventListener('mousemove', (e) => {
      const g = e.target.closest('g[data-tip]');
      if (!g) { tip?.remove(); tip = null; return; }
      if (!tip?.isConnected) { tip = document.createElement('div'); tip.className = 'chart-tip'; c.append(tip); }
      tip.innerHTML = g.dataset.tip;
      const cr = c.getBoundingClientRect();
      const br = g.querySelector('.hit').getBoundingClientRect();
      const bar = g.querySelector('.b')?.getBoundingClientRect();
      const x = Math.min(cr.width - tip.offsetWidth / 2, Math.max(tip.offsetWidth / 2, br.left + br.width / 2 - cr.left));
      tip.style.left = `${x}px`;
      tip.style.top = `${(bar ? bar.top : br.bottom) - cr.top}px`;
    });
    c.addEventListener('mouseleave', () => { tip?.remove(); tip = null; });
  }
}

function hbars(rows) {
  if (!rows?.length) return emptyList();
  const max = Math.max(...rows.map((r) => r.count), 1);
  const total = rows.reduce((a, r) => a + r.count, 0);
  return `<div class="hbars">${rows.map((r) => `<div class="hbar" title="${esc(r.name)}: ${r.count} (${Math.round((r.count / total) * 100)}%)">
    <span class="n">${esc(r.name)}</span><span class="t"><i style="width:${((r.count / max) * 100).toFixed(1)}%"></i></span><span class="c">${num(r.count)}</span></div>`).join('')}</div>`;
}

function topList(rows, kind) {
  if (!rows.length) return emptyList();
  return `<div class="toplist">${rows.map((t, i) => {
    const inner = `<span class="rk">${i + 1}</span>${t.posterPath ? `<img src="${img(t.posterPath, 'w92')}" alt="" loading="lazy">` : '<span class="ph"></span>'}
      <span style="min-width:0"><b style="display:block;white-space:nowrap;overflow:hidden;text-overflow:ellipsis">${esc(t.title)}</b>
      <span class="small muted">${t.year || ''}${t.year ? ' · ' : ''}${t.users} viewer${t.users === 1 ? '' : 's'}</span></span>
      <span class="m"><b style="color:var(--text)">${num(t.plays)}</b> play${t.plays === 1 ? '' : 's'}<br>${fmtHours(t.hours)} h</span>`;
    return t.itemId ? `<a href="/item/${t.itemId}">${inner}</a>` : `<div class="r">${inner}</div>`;
  }).join('')}</div>`;
}

function historyRow(h) {
  const title = h.kind === 'episode' && h.showTitle ? `${h.showTitle} — ${h.title}` : h.title;
  const method = h.method === 'transcode' && h.videoCopy ? 'audio transcode' : h.method;
  const watched = h.playedSeconds >= 60 ? fmtDuration(h.playedSeconds) : '<1 min';
  return `<tr><td><div class="poster-cell">${h.posterPath ? `<img src="${img(h.posterPath, 'w92')}" alt="" loading="lazy">` : ''}
      <div style="min-width:0">${h.itemId ? `<a href="/item/${h.itemId}">${esc(title)}</a>` : esc(title)}${h.completed ? ' <span class="small ok" title="Finished">✓</span>' : ''}</div></div></td>
    <td>${esc(h.userName)}</td><td class="small">${esc(h.client || '—')}</td><td class="small">${esc(methodLabel[method] || method)}</td>
    <td style="text-align:right;font-variant-numeric:tabular-nums">${watched}</td><td class="small muted" style="white-space:nowrap">${timeAgo(h.startedAt)}</td></tr>`;
}

const shortDate = (s) => new Date(s + 'T00:00:00').toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
const longDate = (s) => new Date(s + 'T00:00:00').toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric', year: 'numeric' });
const hourLabel = (h) => new Date(2000, 0, 1, h).toLocaleTimeString(undefined, { hour: 'numeric' });
const weekday = (i, style = 'short') => new Date(2000, 0, 2 + i).toLocaleDateString(undefined, { weekday: style }); // Jan 2 2000 was a Sunday
