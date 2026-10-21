// Server statistics: what's playing now and the machine's load, home stats,
// graphs, the full play history, each viewer, and the libraries.
import { api, img } from './api.js';
import { esc, $, $$, el, toast, modal, fmtDuration, fmtSize, timeAgo } from './ui.js';
import { setTitle, render } from './app.js';

const tabs = [['activity', 'Activity'], ['overview', 'Overview'], ['graphs', 'Graphs'], ['history', 'History'], ['viewers', 'Viewers'], ['libraries', 'Libraries']];
const periods = [[7, '7 days'], [30, '30 days'], [90, '90 days'], [365, '1 year']];
const lsGet = (k, d) => { try { return localStorage.getItem(k) ?? d; } catch { return d; } };
const lsSet = (k, v) => { try { localStorage.setItem(k, v); } catch {} };

// Series colours, checked for colour-blind separation on the dark surface.
const C = { direct: '#199e70', remux: '#3987e5', transcode: '#d95926', a: '#3987e5', b: '#d95926', c: '#199e70' };
const methodLabel = { direct: 'Direct play', remux: 'Direct stream', transcode: 'Transcode', 'audio transcode': 'Audio transcode' };

const fmtHours = (h) => h >= 100 ? Math.round(h).toLocaleString() : h >= 10 ? h.toFixed(0) : h.toFixed(1);
const num = (n) => Number(n || 0).toLocaleString();
const tz = () => -new Date().getTimezoneOffset() * 60;

export async function viewStats() {
  setTitle('Stats');
  const qs = new URLSearchParams(location.search);
  let tab = qs.get('tab');
  if (!tabs.some(([k]) => k === tab)) tab = lsGet('wrentv.statsTab', 'activity');
  if (!tabs.some(([k]) => k === tab)) tab = 'activity';
  lsSet('wrentv.statsTab', tab);
  charts.clear();
  const body = await views[tab](qs);
  const node = el(`<div class="stats"><div class="page-head"><h1>Stats</h1>${['overview', 'graphs'].includes(tab) ? periodSeg() : ''}</div>
    <nav class="stat-tabs" aria-label="Stats sections">${tabs.map(([k, l]) => `<a href="/manage/stats?tab=${k}" class="${k === tab ? 'on' : ''}"${k === tab ? ' aria-current="page"' : ''}>${l}</a>`).join('')}</nav>
    <div class="stat-body"></div></div>`);
  $('.stat-body', node).append(body);
  for (const b of $$('[data-days]', node)) b.onclick = () => { lsSet('wrentv.statsDays', b.dataset.days); render(); };
  requestAnimationFrame(() => bindCharts(node));
  return node;
}

const statsDays = () => Number(lsGet('wrentv.statsDays', '30')) || 30;
const periodSeg = () => {
  const days = statsDays();
  return `<div class="pseg" role="group" aria-label="Period">${periods.map(([v, l]) =>
    `<button type="button" data-days="${v}" class="${v === days ? 'on' : ''}" aria-pressed="${v === days}">${l}</button>`).join('')}</div>`;
};
const loadStats = () => api(`/api/admin/stats?days=${statsDays()}&tz=${tz()}`);

const views = { activity: viewActivity, overview: viewOverview, graphs: viewGraphs, history: viewHistory, viewers: viewViewers, libraries: viewLibraries };

// ================================================================ activity

async function viewActivity() {
  const range = lsGet('wrentv.sysRange', '1h');
  const [a, sys] = await Promise.all([api('/api/admin/activity'), api(`/api/admin/system?range=${range}`).catch(() => null)]);
  const live = { sessions: new Map(), skew: a.serverTime - Date.now() / 1000 };
  const node = el(`<div>
    <div class="kpis" id="act-totals">${totalsHtml(a.totals)}</div>
    <section id="np">${cardsHtml(a.sessions, live)}</section>
    <div class="section-title"><h2 style="font-size:16px;margin:0">Server</h2><div class="pseg" role="group" aria-label="Range">${['1h', '6h', '24h'].map((r) =>
      `<button type="button" data-range="${r}" class="${r === range ? 'on' : ''}" aria-pressed="${r === range}">${r}</button>`).join('')}</div></div>
    <div id="sys">${sys ? systemHtml(sys) : '<p class="muted">System figures are unavailable.</p>'}</div></div>`);
  for (const b of $$('[data-range]', node)) b.onclick = () => { lsSet('wrentv.sysRange', b.dataset.range); render(); };
  bindStop(node, live);
  tick(node, live);

  // Positions move every second; everything else is fetched every few.
  const tickT = setInterval(() => node.isConnected ? tick(node, live) : clearInterval(tickT), 1000);
  let polls = 0;
  const pollT = setInterval(async () => {
    if (!node.isConnected) return clearInterval(pollT);
    if (document.hidden) return;
    try {
      const x = await api('/api/admin/activity');
      live.skew = x.serverTime - Date.now() / 1000;
      $('#act-totals', node).innerHTML = totalsHtml(x.totals);
      const np = $('#np', node);
      np.innerHTML = cardsHtml(x.sessions, live);
      bindStop(np, live);
      tick(node, live);
      if (++polls % (range === '1h' ? 4 : 20) === 0) {
        const s = await api(`/api/admin/system?range=${range}`);
        const box = $('#sys', node);
        box.innerHTML = systemHtml(s);
        bindCharts(box);
      }
    } catch {}
  }, 3000);
  return node;
}

function totalsHtml(t) {
  const split = [t.direct && `${t.direct} direct`, t.remux && `${t.remux} direct stream`, t.transcode && `${t.transcode} transcode`].filter(Boolean).join(' · ');
  return kpi(num(t.streams), t.streams === 1 ? 'Stream' : 'Streams', split || 'Nobody is watching')
    + kpi(fmtRate(t.kbps), 'Bandwidth', t.kbps ? `${fmtRate(t.lanKbps)} local · ${fmtRate(t.wanKbps)} remote` : 'Nothing streaming')
    + kpi(num(t.transcode), 'Transcodes', t.transcode ? 'Using the server\'s CPU or GPU' : 'None running');
}

function cardsHtml(list, live) {
  live.sessions = new Map(list.map((n) => [n.sessionId, n]));
  if (!list.length) return '<div class="panel np-empty"><h2>Now playing</h2><p class="muted" style="margin:0">Nobody is watching right now.</p></div>';
  return `<div class="np-grid">${list.map(card).join('')}</div>`;
}

function card(n) {
  const audioOnly = n.method === 'transcode' && n.videoCopy;
  const cls = n.method === 'transcode' ? 'transcode' : n.method === 'remux' ? 'remux' : 'direct';
  const label = methodLabel[audioOnly ? 'audio transcode' : n.method] || n.method;
  const live = n.kind === 'live';
  const art = live ? (n.channelLogo ? `<img class="logo" src="${esc(n.channelLogo)}" alt="">` : '<span class="ph"></span>')
    : n.posterPath ? `<img src="${img(n.posterPath, 'w185')}" alt="">` : '<span class="ph"></span>';
  const title = n.itemId ? `<a href="/item/${n.itemId}">${esc(n.title)}</a>` : `<span>${esc(n.title)}</span>`;
  const rows = [];
  rows.push(['Stream', `<span class="dot" style="background:${C[cls]}"></span>${esc(label)}${n.engine === 'mpv' ? ' <span class="muted">· mpv</span>' : ''}`,
    (n.reasons || []).join('; ')]);
  if (!live) {
    const s = n.source || {}, o = n.stream || {};
    rows.push(['Container', `${esc(containerName(s.container))}${s.container && o.container && containerName(o.container) !== containerName(s.container) ? ` → ${esc(containerName(o.container))}` : ''}`]);
    if (s.videoCodec) rows.push(['Video', videoLine(s, o, n)]);
    if (s.audioCodec) rows.push(['Audio', audioLine(s, o)]);
    if (n.quality) rows.push(['Version', esc(n.quality)]);
  }
  const rate = n.stream?.kbps || n.delivery?.kbps;
  rows.push(['Bitrate', `${fmtRate(rate)}${n.source?.kbps && n.source.kbps !== rate ? ` <span class="muted">· file ${fmtRate(n.source.kbps)}</span>` : ''}`]);
  if (n.delivery?.bytes) rows.push(['Sent', `${fmtSize(n.delivery.bytes)}${n.delivery.kbps ? ` <span class="muted">· ${fmtRate(n.delivery.kbps)} now</span>` : ''}`]);
  if (n.method === 'transcode' && n.delivery) {
    const d = n.delivery;
    const parts = [d.speed ? `${d.speed.toFixed(1)}× speed` : '', d.encodeDone ? 'Finished encoding' : d.bufferSeconds ? `${Math.round(d.bufferSeconds)} s ahead` : '',
      n.hw && !n.videoCopy ? hwName(n.hw) + (n.hwDecode ? ' decode + encode' : ' encode') : n.videoCopy ? 'Audio only' : ''];
    rows.push(['Transcoder', esc(parts.filter(Boolean).join(' · ') || 'Starting'), d.speed && d.speed < 1 ? 'Encoding slower than real time: the viewer may buffer.' : '']);
  }
  const where = [n.ip, n.ip ? (n.local ? 'Local' : 'Remote') : ''].filter(Boolean).join(' · ');
  return `<article class="np ${n.state === 'paused' ? 'paused' : ''}" data-sid="${esc(n.sessionId)}">
    ${n.backdropPath ? `<div class="bgimg" style="background-image:url('${img(n.backdropPath, 'w780')}')"></div>` : ''}
    <div class="np-head">${art}
      <div class="np-main"><div class="np-title">${title}</div>
        ${n.subtitle ? `<div class="small muted ell">${esc(n.subtitle)}</div>` : n.year ? `<div class="small muted">${n.year}</div>` : ''}
        <div class="small ell" style="margin-top:6px"><b>${esc(n.userName)}</b> <span class="muted">· ${esc(n.client || 'Unknown player')}</span></div>
        ${where ? `<div class="small muted ell">${esc(where)}</div>` : ''}
        <div class="tags"><span class="tag2 ${cls}" title="${esc((n.reasons || []).join('; '))}">${esc(label)}</span>
          ${n.stream?.height && !live ? `<span class="tag2">${esc(resLabel(n.stream.width, n.stream.height))}</span>` : ''}
          ${n.hw && n.method === 'transcode' && !n.videoCopy ? `<span class="tag2">${esc(hwName(n.hw))}</span>` : ''}
          ${n.library ? `<span class="tag2 plain">${esc(n.library)}</span>` : ''}</div>
      </div></div>
    <dl class="np-dl">${rows.map(([k, v, tip]) => `<dt>${k}</dt><dd${tip ? ` title="${esc(tip)}"` : ''}>${v}</dd>`).join('')}</dl>
    <div class="np-prog"><div class="prog"><i data-f="bar"></i></div>
      <div class="np-foot"><span class="state" data-f="state"></span><span data-f="time" class="tnum"></span><span class="muted tnum" data-f="ends"></span>
        <button class="btn ghost small danger" data-stop="${esc(n.sessionId)}">Stop</button></div></div>
  </article>`;
}

// tick moves each card's time on from its last report.
function tick(node, live) {
  const now = Date.now() / 1000 + live.skew;
  for (const c of $$('.np[data-sid]', node)) {
    const n = live.sessions.get(c.dataset.sid);
    if (!n) continue;
    const playing = n.state !== 'paused';
    let pos = n.position + (playing ? Math.max(0, now - n.positionAt) : 0);
    if (n.duration) pos = Math.min(pos, n.duration);
    const f = (k) => $(`[data-f="${k}"]`, c);
    f('state').innerHTML = playing ? `${playIcon} Playing` : `${pauseIcon} Paused`;
    c.classList.toggle('paused', !playing);
    if (n.kind === 'live') {
      f('time').textContent = `Watching for ${fmtDuration(pos)}`;
      f('bar').style.width = '100%';
      f('ends').textContent = '';
      continue;
    }
    f('time').textContent = `${fmtDuration(pos)} / ${fmtDuration(n.duration)}`;
    f('bar').style.width = `${n.duration ? Math.min(100, (pos / n.duration) * 100).toFixed(2) : 0}%`;
    f('ends').textContent = n.duration && playing ? `ends ${clock(Date.now() / 1000 + (n.duration - pos))}` : '';
  }
}

const playIcon = '<svg viewBox="0 0 24 24" width="12" height="12" fill="currentColor" aria-hidden="true"><path d="M7 4v16l13-8z"/></svg>';
const pauseIcon = '<svg viewBox="0 0 24 24" width="12" height="12" fill="currentColor" aria-hidden="true"><path d="M6 4h4v16H6zM14 4h4v16h-4z"/></svg>';

function bindStop(root, live) {
  for (const b of $$('[data-stop]', root)) {
    b.onclick = async () => {
      const n = live.sessions.get(b.dataset.stop);
      const who = n ? `${n.title}${n.subtitle ? ` (${n.subtitle})` : ''} for ${n.userName}` : 'this stream';
      const done = await modal('Stop stream', `<p class="muted" style="margin-top:0">Stop ${esc(who)}? Their player stops and shows your message.</p>
        <div class="field"><label for="kill-msg">Message to the viewer</label><input id="kill-msg" name="message" maxlength="200" placeholder="Optional, e.g. Server maintenance"></div>`, {
        ok: 'Stop stream', danger: true,
        onOk: (f) => api(`/api/sessions/${encodeURIComponent(b.dataset.stop)}?kill=1&message=${encodeURIComponent(f.message.value.trim())}`, { method: 'DELETE' }),
      });
      if (!done) return;
      toast('Stream stopped.');
      b.closest('.np')?.remove();
      live.sessions.delete(b.dataset.stop);
    };
  }
}

function videoLine(s, o, n) {
  const src = [codecName(s.videoCodec) + (s.videoProfile ? ` ${s.videoProfile}` : ''), resLabel(s.width, s.height), s.hdr || 'SDR',
    s.frameRate ? `${Math.round(s.frameRate * 100) / 100} fps` : ''].filter(Boolean).join(' · ');
  if (o.video === 'transcode') {
    return `${esc(src)}<br><span class="arrow">→</span> ${esc([codecName(o.videoCodec), resLabel(o.width, o.height), o.kbps ? fmtRate(o.kbps) : '', n.tonemap ? 'tone-mapped to SDR' : ''].filter(Boolean).join(' · '))}`;
  }
  const how = o.video === 'copy' ? (n.stripDolbyVision ? 'Copied, Dolby Vision removed' : 'Copied') : 'Direct';
  return `${esc(src)} <span class="muted">· ${how}</span>`;
}

function audioLine(s, o) {
  const src = [codecName(s.audioCodec), channelName(s.audioChannels, s.audioLayout), langName(s.audioLanguage)].filter(Boolean).join(' · ');
  if (o.audio === 'transcode') return `${esc(src)}<br><span class="arrow">→</span> ${esc([codecName(o.audioCodec), channelName(o.audioChannels, o.audioLayout)].filter(Boolean).join(' · '))}`;
  return `${esc(src)} <span class="muted">· ${o.audio === 'copy' ? 'Copied' : 'Direct'}</span>`;
}

// ---------------------------------------------------------------- system

function systemHtml(s) {
  const info = s.info || {};
  const samples = s.samples || [];
  const last = samples[samples.length - 1];
  const proc = info.procLabel || 'Wren TV';
  const memCap = info.memLimit && info.memLimit < info.memTotal ? info.memLimit : 0;
  const times = samples.map((x) => x.t);
  const cpu = lineChart({
    times, height: 180, max: 100, fmt: (v) => `${v.toFixed(v < 10 ? 1 : 0)}%`, axis: (v) => `${Math.round(v)}%`,
    series: [{ name: info.host === false ? 'Machine' : 'Host', color: C.a, values: samples.map((x) => x.cpu) },
      ...(info.procCpuAvailable === false ? [] : [{ name: proc, color: C.b, values: samples.map((x) => x.procCpu) }])],
  });
  const mem = lineChart({
    times, height: 180, max: info.memTotal || undefined, fmt: (v) => fmtSize(v) || '0 B', axis: (v) => fmtGB(v),
    series: [{ name: info.host === false ? 'Machine' : 'Host', color: C.a, values: samples.map((x) => x.mem) },
      { name: proc, color: C.b, values: samples.map((x) => x.procMem) }],
  });
  const facts = [
    ['Wren TV', s.version || '—'],
    ['System', [osName(info.os), info.arch].filter(Boolean).join(' · ')],
    ['Processor', info.cpus ? `${info.cpus} core${info.cpus === 1 ? '' : 's'}${info.cpuLimit ? ` · ${proc.toLowerCase()} limited to ${info.cpuLimit}` : ''}` : '—'],
    ['Memory', `${fmtSize(info.memTotal) || '—'}${memCap ? ` · ${proc.toLowerCase()} limited to ${fmtSize(memCap)}` : ''}`],
    ['Transcoding', s.hardware ? `${hwName(s.hardware.kind)} · ${s.hardware.h264Encoder || ''}${s.hardware.hwDecode ? ' · hardware decode' : ''}` : '—'],
    ['Tone mapping', s.hardware?.tonemap && s.hardware.tonemap !== 'none' ? s.hardware.tonemap + (s.hardware.dovi ? ' · Dolby Vision aware' : '') : 'Not available'],
    ['Up for', s.uptime ? fmtUptime(s.uptime) : '—'],
  ];
  return `<div class="kpis">
      ${kpi(last ? `${last.cpu.toFixed(0)}%` : '—', 'CPU', last && info.procCpuAvailable !== false ? `${proc} ${last.procCpu.toFixed(1)}%` : '')}
      ${kpi(last ? fmtSize(last.mem) : '—', 'Memory used', last ? `${proc} ${fmtSize(last.procMem) || '0 B'} of ${fmtSize(info.memTotal)}` : '')}
    </div>
    <div class="stat-grid">
      <div class="panel"><h2>CPU</h2>${legend(cpu.series)}${samples.length > 1 ? cpu.html : '<p class="muted">Collecting samples…</p>'}</div>
      <div class="panel"><h2>Memory</h2>${legend(mem.series)}${samples.length > 1 ? mem.html : '<p class="muted">Collecting samples…</p>'}</div>
    </div>
    <div class="stat-grid">
      <div class="panel"><h2>Server</h2><dl class="facts">${facts.map(([k, v]) => `<dt>${k}</dt><dd>${esc(v)}</dd>`).join('')}</dl></div>
      <div class="panel"><h2>Storage</h2>${(s.disks || []).length ? `<div class="disks">${s.disks.map((d) => {
        const used = d.total - d.free, pct = d.total ? (used / d.total) * 100 : 0;
        return `<div class="disk"><div class="row1"><b class="ell">${esc(d.label)}</b><span class="muted tnum">${fmtSize(d.free)} free of ${fmtSize(d.total)}</span></div>
          <div class="meter${pct > 90 ? ' full' : ''}" role="meter" aria-valuenow="${pct.toFixed(0)}" aria-valuemin="0" aria-valuemax="100" aria-label="${esc(d.label)} used"><i style="width:${pct.toFixed(1)}%"></i></div>
          <div class="small muted ell" title="${esc(d.path)}">${esc(d.path)} · ${pct.toFixed(0)}% used</div></div>`;
      }).join('')}</div>` : emptyList('No disks found.')}</div>
    </div>`;
}

// ================================================================ overview

async function viewOverview() {
  const d = await loadStats();
  const s = d.stats;
  const transcodes = (s.byMethod || []).filter((m) => m.name === 'transcode' || m.name === 'audio transcode').reduce((a, m) => a + m.count, 0);
  const node = el(`<div>
    <div class="kpis">
      ${kpi(num(s.plays), 'Plays')}
      ${kpi(fmtHours(s.hours), 'Hours watched', s.hours ? `${fmtHours(s.movieHours || 0)} h movies · ${fmtHours(s.episodeHours || 0)} h TV` : '')}
      ${kpi(num(s.users), 'Active viewers')}
      ${kpi(num(s.titles), 'Titles watched')}
      ${kpi(num(s.peakStreams), 'Most at once', s.peakAt ? new Date(s.peakAt * 1000).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }) : '')}
      ${kpi(s.plays ? `${Math.round((transcodes / s.plays) * 100)}%` : '—', 'Transcoded', s.plays ? `${num(transcodes)} of ${num(s.plays)} plays` : '')}
    </div>
    <div class="stat-grid three">
      <div class="panel"><h2>Most watched movies</h2>${topList(s.topMovies)}</div>
      <div class="panel"><h2>Most popular movies</h2>${topList(s.popularMovies, true)}</div>
      <div class="panel"><h2>Most watched shows</h2>${topList(s.topShows)}</div>
      <div class="panel"><h2>Most popular shows</h2>${topList(s.popularShows, true)}</div>
      <div class="panel"><h2>Top viewers</h2>${topUsers(s.topUsers)}</div>
      <div class="panel"><h2>Top players</h2>${hbars(s.byClient)}<h2 style="margin-top:22px">Libraries</h2>${hbars((s.byLibrary || []).filter((l) => l.plays).map((l) => ({ name: l.name, count: l.plays })))}</div>
    </div>
    ${d.recentlyAdded?.length ? `<div class="panel"><h2>Recently added</h2><div class="added">${d.recentlyAdded.map((a) => {
      const t = a.kind === 'episode' && a.showTitle ? a.showTitle : a.title;
      const sub = a.kind === 'episode' ? `S${a.season} · E${a.episode}` : a.year || '';
      return `<a href="/item/${a.kind === 'episode' && a.showId ? a.showId : a.itemId}" title="${esc(a.kind === 'episode' ? `${t} — ${a.title}` : t)}">
        <div class="pst">${a.posterPath ? `<img src="${img(a.posterPath, 'w185')}" alt="" loading="lazy">` : ''}</div><b class="ell">${esc(t)}</b>
        <span class="small muted ell">${esc(String(sub))}${sub ? ' · ' : ''}${timeAgo(a.addedAt)}</span></a>`;
    }).join('')}</div></div>` : ''}
    <div class="panel"><div class="panel-head"><h2>Recent plays</h2><a class="small muted" href="/manage/stats?tab=history">Full history</a></div>
      ${historyTable(d.recent.slice(0, 15))}</div>
  </div>`);
  return node;
}

// ================================================================ graphs

async function viewGraphs() {
  const d = await loadStats();
  const s = d.stats;
  const days = statsDays();
  const every = days <= 7 ? 1 : days <= 30 ? 5 : days <= 90 ? 14 : 60;
  const daily = s.daily || [];
  const other = (x) => Math.max(0, x.plays - x.movies - x.episodes);
  const typeSeries = [{ name: 'Movies', color: C.a }, { name: 'TV', color: C.b }];
  const hasOther = daily.some((x) => other(x) > 0);
  if (hasOther) typeSeries.push({ name: 'Other', color: C.c });
  const methods = [{ name: 'Direct play', color: C.direct }, { name: 'Direct stream', color: C.remux }, { name: 'Transcode', color: C.transcode }];
  const tipRows = (series, vals) => series.map((x, i) => `<span class="tr"><i style="background:${x.color}"></i>${esc(x.name)}<b>${num(vals[i])}</b></span>`).join('');
  const node = el(`<div>
    <div class="panel"><h2>Plays per day by type</h2>${s.plays ? legend(typeSeries) + stacked(daily.map((x) => ({
      label: shortDate(x.date), values: hasOther ? [x.movies, x.episodes, other(x)] : [x.movies, x.episodes],
      tip: `<b>${esc(longDate(x.date))}</b>${tipRows(typeSeries, hasOther ? [x.movies, x.episodes, other(x)] : [x.movies, x.episodes])}<span class="tr">${fmtHours(x.hours)} h watched</span>` })),
      typeSeries, { labelEvery: every, height: 190 }) : emptyChart()}</div>
    <div class="panel"><h2>Plays per day by stream type</h2>${s.plays ? legend(methods) + stacked(daily.map((x) => ({
      label: shortDate(x.date), values: [x.direct, x.remux, x.transcode],
      tip: `<b>${esc(longDate(x.date))}</b>${tipRows(methods, [x.direct, x.remux, x.transcode])}` })), methods, { labelEvery: every, height: 190 }) : emptyChart()}</div>
    <div class="stat-grid">
      <div class="panel"><h2>Most streams at once, per day</h2>${s.plays ? stacked(daily.map((x) => ({
        label: shortDate(x.date), values: [x.peak], tip: `<b>${esc(longDate(x.date))}</b>${x.peak} at once` })), [{ name: 'Streams', color: C.a }], { labelEvery: every, height: 150 }) : emptyChart()}</div>
      <div class="panel"><h2>Plays per month</h2>${(s.monthly || []).some((m) => m.plays) ? legend(typeSeries.slice(0, 2)) + stacked(s.monthly.map((m) => ({
        label: monthLabel(m.month), values: [m.movies, m.episodes],
        tip: `<b>${esc(monthLabel(m.month, 'long'))}</b>${tipRows(typeSeries, [m.movies, m.episodes])}<span class="tr">${fmtHours(m.hours)} h watched</span>` })), typeSeries.slice(0, 2), { labelEvery: 2, height: 150 }) : emptyChart()}</div>
    </div>
    <div class="stat-grid">
      <div class="panel"><h2>By time of day</h2>${s.plays ? stacked(s.byHour.map((v, h) => ({
        label: hourLabel(h), values: [v], tip: `<b>${hourLabel(h)}–${hourLabel((h + 1) % 24)}</b>${v} play${v === 1 ? '' : 's'}` })), [{ name: 'Plays', color: C.a }], { labelEvery: 6, height: 140 }) : emptyChart()}</div>
      <div class="panel"><h2>By day of week</h2>${s.plays ? stacked(s.byWeekday.map((v, i) => ({
        label: weekday(i), values: [v], tip: `<b>${weekday(i, 'long')}</b>${v} play${v === 1 ? '' : 's'}` })), [{ name: 'Plays', color: C.a }], { labelEvery: 1, height: 140 }) : emptyChart()}</div>
    </div>
    <div class="stat-grid">
      <div class="panel"><h2>Source resolution</h2>${hbars(s.sourceResolutions)}</div>
      <div class="panel"><h2>Stream resolution</h2>${hbars(s.streamResolutions)}</div>
    </div>
    <div class="stat-grid">
      <div class="panel"><h2>Players by stream type</h2>${legend(methods)}${splitBars(s.clientMethods)}</div>
      <div class="panel"><h2>Viewers by stream type</h2>${legend(methods)}${splitBars(s.userMethods)}</div>
    </div>
  </div>`);
  return node;
}

// ================================================================ history

async function viewHistory(qs) {
  const f = { days: qs.get('days') || '', user: qs.get('user') || '', kind: qs.get('kind') || '', method: qs.get('method') || '', q: qs.get('q') || '', offset: Number(qs.get('offset')) || 0 };
  const viewers = await api('/api/admin/viewers').catch(() => []);
  const opt = (v, l, cur) => `<option value="${esc(v)}"${v === cur ? ' selected' : ''}>${esc(l)}</option>`;
  const node = el(`<div class="panel">
    <form class="hist-filters" role="search">
      <input type="search" name="q" placeholder="Search titles, players or addresses" value="${esc(f.q)}" aria-label="Search history">
      <select name="days" aria-label="Period">${opt('', 'All time', f.days)}${opt('1', 'Last 24 hours', f.days)}${periods.map(([v, l]) => opt(String(v), `Last ${l}`, f.days)).join('')}</select>
      <select name="user" aria-label="Viewer">${opt('', 'All viewers', f.user)}${viewers.map((v) => opt(v.userId, v.name, f.user)).join('')}</select>
      <select name="kind" aria-label="Type">${opt('', 'Everything', f.kind)}${opt('movie', 'Movies', f.kind)}${opt('episode', 'TV episodes', f.kind)}</select>
      <select name="method" aria-label="Stream type">${opt('', 'Any playback', f.method)}${opt('direct', 'Direct play', f.method)}${opt('remux', 'Direct stream', f.method)}${opt('transcode', 'Transcode', f.method)}</select>
    </form>
    <div id="hist"><div class="spinner" style="margin:30px auto"></div></div></div>`);
  const form = $('form', node);
  const load = async () => {
    const p = new URLSearchParams({ tab: 'history' });
    for (const k of ['q', 'days', 'user', 'kind', 'method']) if (f[k]) p.set(k, f[k]);
    if (f.offset) p.set('offset', f.offset);
    history.replaceState(null, '', `/manage/stats?${p}`);
    const api_ = new URLSearchParams({ limit: 50, offset: f.offset });
    for (const k of ['q', 'days', 'user', 'kind', 'method']) if (f[k]) api_.set(k, f[k]);
    let r;
    try { r = await api(`/api/admin/history?${api_}`); } catch (e) { $('#hist', node).innerHTML = `<p class="error">${esc(e.message)}</p>`; return; }
    const end = Math.min(r.total, f.offset + r.rows.length);
    $('#hist', node).innerHTML = (r.rows.length ? historyTable(r.rows, true) : emptyList('No plays match.'))
      + `<div class="pager"><span class="muted small">${r.total ? `${num(f.offset + 1)}–${num(end)} of ${num(r.total)}` : ''}</span>
        <button class="btn ghost small" data-pg="-1" ${f.offset ? '' : 'disabled'}>Previous</button><button class="btn ghost small" data-pg="1" ${end < r.total ? '' : 'disabled'}>Next</button></div>`;
    for (const b of $$('[data-pg]', node)) b.onclick = () => { f.offset = Math.max(0, f.offset + Number(b.dataset.pg) * 50); load(); node.scrollIntoView({ block: 'start' }); };
  };
  let deb;
  form.addEventListener('input', (e) => {
    clearTimeout(deb);
    deb = setTimeout(() => { f[e.target.name] = e.target.value.trim(); f.offset = 0; load(); }, e.target.name === 'q' ? 300 : 0);
  });
  form.onsubmit = (e) => e.preventDefault();
  load();
  return node;
}

// ================================================================ viewers

async function viewViewers(qs) {
  const id = qs.get('user');
  if (id) return viewViewer(id);
  const list = await api('/api/admin/viewers');
  return el(`<div class="panel"><div class="table-wrap"><table class="table viewers">
    <tr><th>Viewer</th><th class="r">Plays</th><th class="r">Hours</th><th>Last watched</th><th>Last player</th><th>Address</th><th>Last seen</th></tr>
    ${list.map((v) => `<tr>
      <td><a class="who" href="/manage/stats?tab=viewers&user=${encodeURIComponent(v.userId)}"><span class="avatar sm">${esc((v.name || '?')[0].toUpperCase())}</span><b>${esc(v.name)}</b>${v.admin ? ' <span class="tag2 plain">Admin</span>' : ''}</a></td>
      <td class="r tnum">${num(v.plays)}</td><td class="r tnum">${fmtHours(v.hours)}</td>
      <td><div class="ell" style="max-width:220px">${esc(v.lastPlayed || '—')}</div></td><td class="small">${esc(v.lastClient || '—')}</td>
      <td class="small tnum">${esc(v.lastIp || '—')}</td><td class="small muted nowrap">${v.lastSeen ? timeAgo(v.lastSeen) : 'never'}</td></tr>`).join('')}
  </table></div></div>`);
}

async function viewViewer(id) {
  const v = await api(`/api/admin/viewers/${encodeURIComponent(id)}`);
  setTitle(`${v.name} · Stats`);
  const m = v.methods || {};
  const methods = [{ name: 'Direct play', count: m.direct || 0 }, { name: 'Direct stream', count: m.remux || 0 }, { name: 'Transcode', count: m.transcode || 0 }].filter((x) => x.count);
  const used = (rows, addr) => rows.length ? `<div class="table-wrap"><table class="table"><tr><th>${addr ? 'Address' : 'Player'}</th>${addr ? '<th>Player</th>' : ''}<th class="r">Plays</th><th class="r">Hours</th><th>Last used</th></tr>
    ${rows.map((u) => `<tr><td class="${addr ? 'tnum' : ''}"><b>${esc(u.name)}</b>${addr ? ` <span class="small muted">${isPrivate(u.name) ? 'Local' : 'Remote'}</span>` : ''}</td>${addr ? `<td class="small">${esc(u.detail || '—')}</td>` : ''}
      <td class="r tnum">${num(u.plays)}</td><td class="r tnum">${fmtHours(u.hours || 0)}</td><td class="small muted nowrap">${timeAgo(u.lastSeen)}</td></tr>`).join('')}</table></div>` : emptyList();
  return el(`<div>
    <a class="small muted" href="/manage/stats?tab=viewers">‹ All viewers</a>
    <div class="viewer-head"><span class="avatar lg">${esc((v.name || '?')[0].toUpperCase())}</span><div><h2>${esc(v.name)}${v.admin ? ' <span class="tag2 plain">Admin</span>' : ''}</h2>
      <div class="small muted">${v.joined ? `Joined ${new Date(v.joined * 1000).toLocaleDateString()}` : ''}${v.lastSeen ? ` · Last seen ${timeAgo(v.lastSeen)}` : ''}</div></div></div>
    <div class="kpis">${(v.totals || []).map((t) => kpi(num(t.plays), t.label, `${fmtHours(t.hours)} h`)).join('')}</div>
    <div class="stat-grid">
      <div class="panel"><h2>Most watched</h2>${topList(v.topTitles, false, true)}</div>
      <div class="panel"><h2>Stream types</h2>${hbars(methods)}<h2 style="margin-top:22px">Players</h2>${used(v.players || [], false)}</div>
    </div>
    <div class="panel"><h2>Addresses</h2>${used(v.addresses || [], true)}</div>
    <div class="panel"><div class="panel-head"><h2>Recent plays</h2><a class="small muted" href="/manage/stats?tab=history&user=${encodeURIComponent(v.userId)}">Full history</a></div>${historyTable(v.recent || [], true, true)}</div>
  </div>`);
}

// ================================================================ libraries

async function viewLibraries() {
  const d = await api(`/api/admin/stats?days=30&tz=${tz()}`);
  const lib = d.library;
  return el(`<div>
    <div class="kpis">
      ${kpi(num(lib.movies), 'Movies')}${kpi(num(lib.shows), 'Shows')}${kpi(num(lib.episodes), 'Episodes')}
      ${kpi(fmtSize(lib.bytes) || '0 B', 'On disk', `${num(lib.files)} files`)}${kpi(fmtHours(lib.hours), 'Hours of media')}
    </div>
    ${lib.libraries?.length ? `<div class="panel"><h2>Libraries</h2><div class="table-wrap"><table class="table"><tr><th>Name</th><th>Type</th><th class="r">Titles</th><th class="r">Episodes</th>
      <th class="r">Size</th><th class="r">Plays</th><th class="r">Hours watched</th><th>Last played</th></tr>
      ${lib.libraries.map((l) => `<tr><td><a href="/library/${l.id}"><b>${esc(l.name)}</b></a></td><td class="muted">${l.type === 'shows' ? 'Shows' : 'Movies'}</td>
        <td class="r tnum">${num(l.items)}</td><td class="r tnum">${l.type === 'shows' ? num(l.episodes) : '—'}</td><td class="r tnum">${fmtSize(l.bytes) || '—'}</td>
        <td class="r tnum">${num(l.plays)}</td><td class="r tnum">${fmtHours(l.hours || 0)}</td>
        <td><div class="ell" style="max-width:220px">${esc(l.lastPlayed || '—')}</div>${l.lastPlayedAt ? `<div class="small muted">${timeAgo(l.lastPlayedAt)}</div>` : ''}</td></tr>`).join('')}</table></div></div>` : ''}
    <div class="stat-grid">
      <div class="panel"><h2>Resolution</h2>${hbars(lib.resolutions)}<h2 style="margin-top:22px">Dynamic range</h2>${hbars(lib.hdr)}</div>
      <div class="panel"><h2>Video codec</h2>${hbars(lib.videoCodecs)}<h2 style="margin-top:22px">Container</h2>${hbars(lib.containers)}</div>
    </div>
  </div>`);
}

// ================================================================ pieces

const kpi = (v, l, sub = '') => `<div class="kpi"><div class="v">${v}</div><div class="l">${esc(l)}</div>${sub ? `<div class="s">${esc(sub)}</div>` : ''}</div>`;
const emptyChart = () => '<p class="muted" style="margin:0">No plays in this period yet.</p>';
const emptyList = (msg = 'Nothing in this period yet.') => `<p class="muted" style="margin:0">${esc(msg)}</p>`;
const legend = (series) => series.length < 2 ? '' : `<div class="legend">${series.map((x) => `<span><i style="background:${x.color}"></i>${esc(x.name)}</span>`).join('')}</div>`;

function historyTable(rows, full = false, noViewer = false) {
  if (!rows.length) return emptyList('Plays show up here as people watch.');
  return `<div class="table-wrap"><table class="table hist"><tr><th>Title</th>${noViewer ? '' : '<th>Viewer</th>'}<th>Player</th>${full ? '<th>Address</th>' : ''}<th>Playback</th>
    <th class="r">Watched</th>${full ? '<th class="r">Progress</th>' : ''}<th>When</th></tr>${rows.map((h) => historyRow(h, full, noViewer)).join('')}</table></div>`;
}

function historyRow(h, full, noViewer) {
  const title = h.kind === 'episode' && h.showTitle ? `${h.showTitle} — ${h.title}` : h.title;
  const method = h.method === 'transcode' && h.videoCopy ? 'audio transcode' : h.method;
  const cls = h.method === 'transcode' ? 'transcode' : h.method === 'remux' ? 'remux' : 'direct';
  const watched = h.playedSeconds >= 60 ? fmtDuration(h.playedSeconds) : '<1 min';
  const pct = h.duration ? Math.min(100, Math.round((h.position / h.duration) * 100)) : 0;
  const res = h.height ? resLabel(0, h.height) + (h.streamHeight && h.streamHeight !== h.height ? ` → ${resLabel(0, h.streamHeight)}` : '') : '';
  const when = new Date(h.startedAt * 1000);
  return `<tr><td><div class="poster-cell">${h.posterPath ? `<img src="${img(h.posterPath, 'w92')}" alt="" loading="lazy">` : ''}
      <div style="min-width:0">${h.itemId ? `<a href="/item/${h.itemId}">${esc(title)}</a>` : esc(title)}${h.completed ? ' <span class="small ok" title="Finished">✓</span>' : ''}
      ${h.quality ? `<div class="small muted">${esc(h.quality)}</div>` : ''}</div></div></td>
    ${noViewer ? '' : `<td><a href="/manage/stats?tab=viewers&user=${encodeURIComponent(h.userId)}">${esc(h.userName)}</a></td>`}<td class="small">${esc(h.client || '—')}</td>
    ${full ? `<td class="small tnum">${esc(h.ip || '—')}</td>` : ''}
    <td class="small"${h.reasons ? ` title="${esc(h.reasons)}"` : ''}><span class="dot" style="background:${C[cls]}"></span>${esc(methodLabel[method] || method)}${res ? `<div class="muted">${esc(res)}</div>` : ''}</td>
    <td class="r tnum">${watched}</td>${full ? `<td class="r tnum">${h.duration ? `${pct}%` : '—'}</td>` : ''}
    <td class="small muted nowrap" title="${esc(when.toLocaleString())}">${full ? when.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' }) : timeAgo(h.startedAt)}</td></tr>`;
}

function hbars(rows) {
  if (!rows?.length) return emptyList();
  const max = Math.max(...rows.map((r) => r.count), 1);
  const total = rows.reduce((a, r) => a + r.count, 0);
  return `<div class="hbars">${rows.map((r) => `<div class="hbar" title="${esc(methodLabel[r.name] || r.name)}: ${r.count} (${Math.round((r.count / total) * 100)}%)">
    <span class="n">${esc(methodLabel[r.name] || r.name)}</span><span class="t"><i style="width:${((r.count / max) * 100).toFixed(1)}%"></i></span><span class="c">${num(r.count)}</span></div>`).join('')}</div>`;
}

// splitBars is one stacked bar per player or viewer, split by stream type.
function splitBars(rows) {
  if (!rows?.length) return emptyList();
  const tot = (r) => r.direct + r.remux + r.transcode;
  const max = Math.max(...rows.map(tot), 1);
  return `<div class="hbars">${rows.map((r) => {
    const seg = (n, k) => n ? `<i style="width:${((n / max) * 100).toFixed(2)}%;background:${C[k]}"></i>` : '';
    return `<div class="hbar split" title="${esc(r.name)}: ${r.direct} direct play, ${r.remux} direct stream, ${r.transcode} transcode">
      <span class="n">${esc(r.name)}</span><span class="t">${seg(r.direct, 'direct')}${seg(r.remux, 'remux')}${seg(r.transcode, 'transcode')}</span><span class="c">${num(tot(r))}</span></div>`;
  }).join('')}</div>`;
}

function topList(rows, byUsers = false, own = false) {
  if (!rows?.length) return emptyList();
  return `<div class="toplist">${rows.slice(0, 10).map((t, i) => {
    const inner = `<span class="rk">${i + 1}</span>${t.posterPath ? `<img src="${img(t.posterPath, 'w92')}" alt="" loading="lazy">` : '<span class="ph"></span>'}
      <span style="min-width:0"><b class="ell" style="display:block">${esc(t.title)}</b>
      <span class="small muted">${t.year || ''}${t.year && !own ? ' · ' : ''}${own ? '' : `${t.users} viewer${t.users === 1 ? '' : 's'}`}</span></span>
      <span class="m">${byUsers ? `<b style="color:var(--text)">${num(t.users)}</b> viewer${t.users === 1 ? '' : 's'}<br>${num(t.plays)} plays`
        : `<b style="color:var(--text)">${num(t.plays)}</b> play${t.plays === 1 ? '' : 's'}<br>${fmtHours(t.hours)} h`}</span>`;
    return t.itemId ? `<a href="/item/${t.itemId}">${inner}</a>` : `<div class="r">${inner}</div>`;
  }).join('')}</div>`;
}

function topUsers(rows) {
  if (!rows?.length) return emptyList();
  return `<div class="toplist users">${rows.slice(0, 10).map((u, i) => `<a href="/manage/stats?tab=viewers&user=${encodeURIComponent(u.userId)}"><span class="rk">${i + 1}</span>
    <span class="avatar sm">${esc((u.name || '?')[0].toUpperCase())}</span><span style="min-width:0"><b class="ell" style="display:block">${esc(u.name)}</b>
    <span class="small muted ell" style="display:block">${esc(u.lastPlayed || '')}</span></span>
    <span class="m"><b style="color:var(--text)">${num(u.plays)}</b> play${u.plays === 1 ? '' : 's'}<br>${fmtHours(u.hours)} h</span></a>`).join('')}</div>`;
}

// ---------------------------------------------------------------- charts

function niceMax(v) {
  if (v <= 4) return Math.max(1, Math.ceil(v));
  const p = 10 ** Math.floor(Math.log10(v));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= v) return m * p;
  return v;
}

// Charts draw at the container's real pixel width so axis text keeps its
// CSS size instead of scaling with a viewBox.
const charts = new Map();
let chartSeq = 0;

// stacked draws columns, one segment per series, with 2px gaps between
// segments and a rounded top on each column.
function stacked(data, series, opts = {}) {
  const id = `c${++chartSeq}`;
  charts.set(id, { draw: (c) => drawStacked(c, data, series, opts) });
  const table = `<div class="sr-only"><table><tr><th>Label</th>${series.map((x) => `<th>${esc(x.name)}</th>`).join('')}</tr>${data.map((d) =>
    `<tr><td>${esc(d.label)}</td>${d.values.map((v) => `<td>${v}</td>`).join('')}</tr>`).join('')}</table></div>`;
  return `<div class="chart" data-chart="${id}" style="height:${opts.height || 160}px"></div>${table}`;
}

function drawStacked(c, data, series, { labelEvery = 1, height = 160 }) {
  const W = Math.max(120, Math.round(c.clientWidth));
  const every = Math.max(labelEvery, Math.ceil(48 / (W / data.length)));
  const max = niceMax(Math.max(...data.map((d) => d.values.reduce((a, v) => a + v, 0)), 0));
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
    const total = d.values.reduce((a, v) => a + v, 0);
    const lastIdx = d.values.reduce((a, v, k) => v ? k : a, -1);
    let base = top + plotH, segs = '';
    d.values.forEach((v, k) => {
      if (!v) return;
      let h = Math.max(2, (v / max) * plotH);
      const yTop = base - h;
      const gapH = k === lastIdx ? 0 : Math.min(2, h - 1);
      if (k === lastIdx) {
        const r = Math.min(4, bw / 2, h);
        segs += `<path class="b" style="fill:${series[k].color}" d="M${x},${base}V${yTop + r}Q${x},${yTop} ${x + r},${yTop}H${x + bw - r}Q${x + bw},${yTop} ${x + bw},${yTop + r}V${base}Z"/>`;
      } else {
        segs += `<rect class="b" style="fill:${series[k].color}" x="${x}" y="${yTop + gapH}" width="${bw}" height="${h - gapH}"/>`;
      }
      base = yTop;
    });
    const label = i % every === 0 ? `<text class="ax" x="${x + bw / 2}" y="${H - 4}" text-anchor="middle">${esc(d.label)}</text>` : '';
    return `<g data-tip="${esc(d.tip)}"${total ? '' : ' data-empty'}><rect class="hit" x="${padL + i * step}" y="${top}" width="${step}" height="${plotH}"/>${segs}${label}</g>`;
  }).join('');
  c.innerHTML = `<svg width="${W}" height="${H}" role="img" aria-label="Column chart">${grid}${bars}</svg>`;
}

// lineChart draws series over time on one axis, with a crosshair that
// reads every series at the hovered moment.
function lineChart(spec) {
  const id = `c${++chartSeq}`;
  charts.set(id, { draw: (c) => drawLines(c, spec), lines: spec });
  return { series: spec.series, html: `<div class="chart lines" data-chart="${id}" style="height:${spec.height || 160}px"></div>` };
}

function drawLines(c, { times, series, height = 160, max, axis }) {
  const W = Math.max(160, Math.round(c.clientWidth));
  const H = height, padL = 44, padB = 20, top = 8;
  const plotW = W - padL - 4, plotH = H - padB - top;
  const peak = Math.max(...series.flatMap((s) => s.values), 0);
  const yMax = max && peak > max * 0.6 ? max : niceMax(peak * 1.15 || 1);
  const t0 = times[0], t1 = times[times.length - 1] || t0 + 1;
  const x = (t) => padL + ((t - t0) / Math.max(1, t1 - t0)) * plotW;
  const y = (v) => top + plotH - (Math.min(v, yMax) / yMax) * plotH;
  const grid = [0, yMax / 2, yMax].map((v) => `<line class="gl" x1="${padL}" x2="${W}" y1="${y(v)}" y2="${y(v)}"/>
    <text class="ax" x="${padL - 6}" y="${y(v) + 4}" text-anchor="end">${esc(axis(v))}</text>`).join('');
  const span = t1 - t0;
  const ticks = [];
  const stepT = span <= 3600 ? 900 : span <= 6 * 3600 ? 3600 : 6 * 3600;
  for (let t = Math.ceil(t0 / stepT) * stepT; t <= t1; t += stepT) ticks.push(t);
  const xl = ticks.map((t) => `<text class="ax" x="${x(t)}" y="${H - 4}" text-anchor="middle">${clock(t)}</text>`).join('');
  const paths = series.map((s) => `<path class="ln" stroke="${s.color}" d="${s.values.map((v, i) => `${i ? 'L' : 'M'}${x(times[i]).toFixed(1)},${y(v).toFixed(1)}`).join('')}"/>`).join('');
  c.innerHTML = `<svg width="${W}" height="${H}" role="img" aria-label="${esc(series.map((s) => s.name).join(' and '))} over time">${grid}${xl}${paths}
    <line class="xh" x1="0" x2="0" y1="${top}" y2="${top + plotH}" visibility="hidden"/>${series.map((s, k) => `<circle class="pt" data-k="${k}" r="4" fill="${s.color}" visibility="hidden"/>`).join('')}</svg>`;
  c._geom = { x, y, padL, plotW, t0, t1 };
}

function bindCharts(root) {
  for (const c of $$('.chart', root)) {
    const spec = charts.get(c.dataset.chart);
    if (!spec || c._bound) continue;
    c._bound = true;
    spec.draw(c);
    let lastW = c.clientWidth;
    new ResizeObserver(() => {
      if (!c.isConnected || c.clientWidth === lastW) return;
      lastW = c.clientWidth;
      spec.draw(c);
    }).observe(c);
    let tip;
    const showTip = (html, left, topPx) => {
      if (!tip?.isConnected) { tip = document.createElement('div'); tip.className = 'chart-tip'; c.append(tip); }
      tip.innerHTML = html;
      const cr = c.getBoundingClientRect();
      tip.style.left = `${Math.min(cr.width - tip.offsetWidth / 2, Math.max(tip.offsetWidth / 2, left))}px`;
      tip.style.top = `${topPx}px`;
    };
    const hide = () => {
      tip?.remove(); tip = null;
      for (const e of $$('.xh, .pt', c)) e.setAttribute('visibility', 'hidden');
    };
    c.addEventListener('mousemove', (e) => {
      const cr = c.getBoundingClientRect();
      if (spec.lines) {
        const { times, series, fmt } = spec.lines;
        const g = c._geom;
        if (!g || !times.length) return;
        const t = g.t0 + ((e.clientX - cr.left - g.padL) / g.plotW) * (g.t1 - g.t0);
        let i = 0;
        for (let k = 1; k < times.length; k++) if (Math.abs(times[k] - t) < Math.abs(times[i] - t)) i = k;
        const px = g.x(times[i]);
        const xh = $('.xh', c);
        xh.setAttribute('x1', px); xh.setAttribute('x2', px); xh.setAttribute('visibility', 'visible');
        for (const p of $$('.pt', c)) {
          const s = series[Number(p.dataset.k)];
          p.setAttribute('cx', px); p.setAttribute('cy', g.y(s.values[i])); p.setAttribute('visibility', 'visible');
        }
        showTip(`<b>${clock(times[i], true)}</b>${series.map((s) => `<span class="tr"><i style="background:${s.color}"></i>${esc(s.name)}<b>${esc(fmt(s.values[i]))}</b></span>`).join('')}`,
          px, Math.min(...series.map((s) => g.y(s.values[i]))));
        return;
      }
      const g = e.target.closest('g[data-tip]');
      if (!g || g.hasAttribute('data-empty')) return hide();
      const br = g.querySelector('.hit').getBoundingClientRect();
      const bars = [...g.querySelectorAll('.b')].map((b) => b.getBoundingClientRect().top);
      showTip(g.dataset.tip, br.left + br.width / 2 - cr.left, (bars.length ? Math.min(...bars) : br.bottom) - cr.top);
    });
    c.addEventListener('mouseleave', hide);
  }
}

// ---------------------------------------------------------------- formatting

function fmtRate(kbps) {
  if (!kbps) return '0 Mbps';
  return kbps >= 1000 ? `${(kbps / 1000).toFixed(kbps >= 10000 ? 0 : 1)} Mbps` : `${kbps} kbps`;
}
const fmtGB = (b) => !b ? '0' : b >= 1024 ** 3 ? `${(b / 1024 ** 3).toFixed(b >= 10 * 1024 ** 3 ? 0 : 1)} GB` : `${Math.round(b / 1024 ** 2)} MB`;
function fmtUptime(s) {
  const d = Math.floor(s / 86400), h = Math.floor((s % 86400) / 3600), m = Math.floor((s % 3600) / 60);
  return d ? `${d} d ${h} h` : h ? `${h} h ${m} min` : `${m} min`;
}
const clock = (unix, secs = false) => new Date(unix * 1000).toLocaleTimeString(undefined, { hour: 'numeric', minute: '2-digit', ...(secs ? { second: '2-digit' } : {}) });

function resLabel(w, h) {
  if (!h && !w) return '';
  if (w >= 3200 || h >= 1600) return '4K';
  if (w >= 1700 || h >= 1000) return '1080p';
  if (w >= 1100 || h >= 700) return '720p';
  if (h >= 560) return '576p';
  if (h >= 470) return '480p';
  return h ? `${h}p` : 'SD';
}
const codecs = { hevc: 'HEVC', h265: 'HEVC', h264: 'H.264', avc: 'H.264', av1: 'AV1', vp9: 'VP9', vp8: 'VP8', mpeg2video: 'MPEG-2', mpeg4: 'MPEG-4', vc1: 'VC-1',
  aac: 'AAC', ac3: 'Dolby Digital', eac3: 'Dolby Digital+', truehd: 'TrueHD', dts: 'DTS', 'dts-hd': 'DTS-HD', flac: 'FLAC', opus: 'Opus', mp3: 'MP3', vorbis: 'Vorbis', pcm_s16le: 'PCM', pcm_s24le: 'PCM', alac: 'ALAC' };
const codecName = (c) => codecs[(c || '').toLowerCase()] || (c || '').toUpperCase();
function containerName(c) {
  c = (c || '').toLowerCase();
  if (!c) return '—';
  if (c.includes('matroska')) return 'MKV';
  if (c.includes('mp4') || c.includes('mov')) return 'MP4';
  if (c === 'mpegts') return 'MPEG-TS';
  return c.split(',')[0].toUpperCase();
}
function channelName(n, layout) {
  if (layout && /^\d\.\d/.test(layout)) return layout.replace(/\(.*\)/, '');
  return { 1: 'Mono', 2: 'Stereo', 6: '5.1', 8: '7.1' }[n] || (n ? `${n} ch` : '');
}
function langName(code) {
  if (!code || code === 'und') return '';
  try { return new Intl.DisplayNames(undefined, { type: 'language' }).of(code.length === 3 ? ({ eng: 'en', spa: 'es', fre: 'fr', fra: 'fr', ger: 'de', deu: 'de', jpn: 'ja', ita: 'it', por: 'pt', kor: 'ko', chi: 'zh', zho: 'zh', rus: 'ru' }[code] || code) : code) || code; } catch { return code; }
}
const hwName = (k) => ({ videotoolbox: 'VideoToolbox', nvenc: 'NVIDIA NVENC', qsv: 'Intel Quick Sync', vaapi: 'VA-API', amf: 'AMD AMF', software: 'Software' }[k] || k || '');
const osName = (o) => ({ linux: 'Linux', darwin: 'macOS', windows: 'Windows', freebsd: 'FreeBSD' }[o] || o || '');
function isPrivate(ip) {
  return /^(10\.|192\.168\.|172\.(1[6-9]|2\d|3[01])\.|127\.|::1$|fe80:|fc|fd)/i.test(ip || '');
}

const shortDate = (s) => new Date(s + 'T00:00:00').toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
const longDate = (s) => new Date(s + 'T00:00:00').toLocaleDateString(undefined, { weekday: 'short', month: 'short', day: 'numeric', year: 'numeric' });
const monthLabel = (m, style = 'short') => new Date(m + '-01T00:00:00').toLocaleDateString(undefined, style === 'long' ? { month: 'long', year: 'numeric' } : { month: 'short' });
const hourLabel = (h) => new Date(2000, 0, 1, h).toLocaleTimeString(undefined, { hour: 'numeric' });
const weekday = (i, style = 'short') => new Date(2000, 0, 2 + i).toLocaleDateString(undefined, { weekday: style }); // Jan 2 2000 was a Sunday
