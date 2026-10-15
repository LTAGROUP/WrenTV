// Full-screen web player: <video> + hls.js (native HLS on Safari), with its
// own controls for chapters, skip intro/credits and up next.
import { api, browserCaps, img } from './api.js';
import { esc, $, $$, el, toast, fmtDuration } from './ui.js';

const qualities = [
  ['orig', 'Original quality', 0, 0],
  ['20', '20 Mbps 1080p', 20000, 1920],
  ['8', '8 Mbps 1080p', 8000, 1920],
  ['4', '4 Mbps 720p', 4000, 1280],
  ['2', '2 Mbps 720p', 2000, 1280],
];

const lsGet = (k, d) => { try { return localStorage.getItem(k) ?? d; } catch { return d; } };
const lsSet = (k, v) => { try { localStorage.setItem(k, v); } catch {} };

const ico = {
  play: '<svg viewBox="0 0 24 24" fill="currentColor"><path d="M8 5.5v13a1 1 0 001.5.86l10.4-6.5a1 1 0 000-1.72L9.5 4.64A1 1 0 008 5.5z"/></svg>',
  pause: '<svg viewBox="0 0 24 24" fill="currentColor"><rect x="6" y="5" width="4" height="14" rx="1"/><rect x="14" y="5" width="4" height="14" rx="1"/></svg>',
  back: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 12a9 9 0 103-6.7L3 8M3 3v5h5"/><text x="12" y="15.5" text-anchor="middle" font-size="7.5" font-weight="700" fill="currentColor" stroke="none">10</text></svg>',
  fwd: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M21 12a9 9 0 11-3-6.7L21 8M21 3v5h-5"/><text x="12" y="15.5" text-anchor="middle" font-size="7.5" font-weight="700" fill="currentColor" stroke="none">10</text></svg>',
  vol: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 9v6h4l5 4V5L8 9z" fill="currentColor"/><path d="M16.5 8.5a5 5 0 010 7M19 6a8.5 8.5 0 010 12"/></svg>',
  mute: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 9v6h4l5 4V5L8 9z" fill="currentColor"/><path d="M17 9l5 6M22 9l-5 6"/></svg>',
  chapters: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 6h11M4 12h16M4 18h9"/><circle cx="19" cy="6" r="1.5" fill="currentColor"/><circle cx="17" cy="18" r="1.5" fill="currentColor"/></svg>',
  full: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5"/></svg>',
};

let current = null;

export async function openPlayer(itemId, opts = {}) {
  current?.close(false);
  const p = new Player(itemId, opts);
  current = p;
  await p.start();
}

class Player {
  constructor(itemId, opts) {
    this.itemId = itemId;
    this.opts = opts;
    this.quality = lsGet('wrentv.quality', lsGet('haven.quality', 'orig'));
    this.audioIndex = undefined;
    this.fileId = opts.fileId;
    this.sid = null;
    this.hls = null;
    this.lastReport = 0;
    this.closed = false;
    this.chapters = [];
    this.markers = {};
    this.dismissed = new Set(); // skip prompts the viewer closed
    this.root = el(`<div class="player">
      <video playsinline autoplay crossorigin="anonymous"></video>
      <div class="wait"><div><div class="spinner"></div><div class="msg muted">Starting…</div></div></div>
      <div class="top">
        <button class="btn ghost" data-close title="Back (Esc)">←</button>
        <div class="ttl"><h2></h2><div class="sub small muted"></div></div>
        <select data-audio title="Audio" hidden></select>
        <select data-subs title="Subtitles" hidden></select>
        <select data-quality title="Quality">${qualities.map(([k, l]) => `<option value="${k}">${l}</option>`).join('')}</select>
        <button class="btn ghost small" data-info title="Playback info">ⓘ</button>
      </div>
      <div class="controls">
        <div class="timeline" data-timeline><div class="track"><div class="buf"></div><div class="done"></div><div class="marks"></div></div>
          <div class="knob"></div><div class="tip"></div></div>
        <div class="bar-row">
          <button class="cbtn" data-toggle title="Play (Space)">${ico.play}</button>
          <button class="cbtn" data-back title="Back 10 s (←)">${ico.back}</button>
          <button class="cbtn" data-fwd title="Forward 10 s (→)">${ico.fwd}</button>
          <span class="time">0:00 / 0:00</span>
          <span class="chap"></span>
          <button class="cbtn" data-mute title="Mute (M)">${ico.vol}</button>
          <input type="range" min="0" max="1" step="0.05" data-volume aria-label="Volume">
          <button class="cbtn" data-chapters title="Chapters (C)" hidden>${ico.chapters}</button>
          <button class="cbtn" data-full title="Full screen (F)">${ico.full}</button>
        </div>
      </div></div>`);
    this.video = $('video', this.root);
    $('[data-quality]', this.root).value = this.quality;
    this.video.volume = Number(lsGet('wrentv.volume', '1'));
    $('[data-volume]', this.root).value = this.video.volume;
  }

  async start() {
    $('#player-root').replaceChildren(this.root);
    document.body.style.overflow = 'hidden';
    const r = this.root;
    const v = this.video;
    $('[data-close]', r).onclick = () => this.close();
    $('[data-info]', r).onclick = () => this.showInfo();
    $('[data-quality]', r).onchange = (e) => {
      this.quality = e.target.value;
      lsSet('wrentv.quality', this.quality);
      this.reload();
    };
    $('[data-audio]', r).onchange = (e) => { this.audioIndex = Number(e.target.value); this.reload(); };
    $('[data-subs]', r).onchange = (e) => this.selectSubs(e.target.value);
    $('[data-toggle]', r).onclick = () => this.toggle();
    $('[data-back]', r).onclick = () => this.seek(v.currentTime - 10);
    $('[data-fwd]', r).onclick = () => this.seek(v.currentTime + 10);
    $('[data-full]', r).onclick = () => this.toggleFullscreen();
    $('[data-chapters]', r).onclick = () => this.toggleChapters();
    $('[data-mute]', r).onclick = () => { v.muted = !v.muted; };
    $('[data-volume]', r).oninput = (e) => { v.volume = Number(e.target.value); v.muted = v.volume === 0; lsSet('wrentv.volume', v.volume); };
    this.bindTimeline();

    this.onKey = (e) => {
      if (e.target.closest?.('select, input')) return;
      const k = e.key;
      if (k === 'Escape') {
        if ($('.chapters', r)) return this.toggleChapters(false);
        if (!document.fullscreenElement) this.close();
      } else if (k === 'f') this.toggleFullscreen();
      else if (k === ' ' || k === 'k') { e.preventDefault(); this.toggle(); }
      else if (k === 'ArrowRight') { e.preventDefault(); this.seek(v.currentTime + 10); }
      else if (k === 'ArrowLeft') { e.preventDefault(); this.seek(v.currentTime - 10); }
      else if (k === 'm') v.muted = !v.muted;
      else if (k === 'c' && this.chapters.length) this.toggleChapters();
      else if (k === 's') $('.skip', r)?.click();
      else return;
      this.onMove();
    };
    document.addEventListener('keydown', this.onKey);
    // Hide the controls while playing and idle.
    let idle;
    this.onMove = () => {
      r.classList.remove('idle');
      clearTimeout(idle);
      idle = setTimeout(() => !v.paused && !$('.chapters', r) && r.classList.add('idle'), 2800);
    };
    r.addEventListener('mousemove', this.onMove);
    v.addEventListener('timeupdate', () => { this.report(false); this.update(); });
    v.addEventListener('progress', () => this.update());
    v.addEventListener('durationchange', () => this.drawMarks());
    v.addEventListener('play', () => this.update());
    v.addEventListener('pause', () => { r.classList.remove('idle'); this.report(true); this.update(); });
    v.addEventListener('volumechange', () => {
      $('[data-mute]', r).innerHTML = v.muted || v.volume === 0 ? ico.mute : ico.vol;
      $('[data-volume]', r).value = v.muted ? 0 : v.volume;
    });
    v.addEventListener('ended', () => this.ended());
    v.addEventListener('error', () => this.mediaError());
    v.addEventListener('click', () => this.toggle());
    v.addEventListener('dblclick', () => this.toggleFullscreen());

    try {
      const d = await api('/api/items/' + this.itemId);
      this.item = d.item;
      this.show = d.show;
      $('h2', r).textContent = this.item.kind === 'episode' ? this.show?.title || this.item.title : this.item.title;
      $('.sub', r).textContent = this.item.kind === 'episode' ? `S${this.item.seasonNumber} · E${this.item.episodeNumber} — ${this.item.title}` : this.item.year || '';
      const bg = this.item.backdropPath || this.show?.backdropPath;
      if (bg) v.poster = img(bg, 'w1280');
    } catch {}
    await this.play(this.opts.start);
  }

  caps() {
    const c = browserCaps();
    const q = qualities.find((x) => x[0] === this.quality) || qualities[0];
    c.maxKbps = q[2];
    return { caps: c, maxWidth: q[3] };
  }

  async play(startAt) {
    this.setWait('Starting…');
    const { caps, maxWidth } = this.caps();
    let res;
    for (;;) {
      if (this.closed) return;
      try {
        res = await api(`/api/items/${this.itemId}/play`, { method: 'POST', body: { caps, maxWidth, fileId: this.fileId, audioIndex: this.audioIndex } });
      } catch (e) {
        this.setWait(e.message, true);
        return;
      }
      if (res.status === 'ready') break;
      this.setWait(res.message || 'Preparing stream…');
      await new Promise((ok) => setTimeout(ok, 4000));
    }
    if (this.closed) return api(`/api/sessions/${res.sessionId}`, { method: 'DELETE' }).catch(() => {});
    this.res = res;
    this.sid = res.sessionId;
    this.fileId = res.fileId;
    this.duration = res.duration;
    this.chapters = res.chapters || res.info?.chapters || [];
    this.markers = res.markers || {};
    $('[data-chapters]', this.root).hidden = !this.chapters.length;
    if (startAt === undefined) startAt = this.opts.resume ? res.resumePosition || 0 : 0;
    this.fillTracks();
    this.attach(res.url, startAt);
    this.drawMarks();
  }

  attach(url, startAt) {
    const v = this.video;
    this.hls?.destroy();
    this.hls = null;
    v.removeAttribute('src');
    for (const t of [...v.querySelectorAll('track')]) t.remove();
    const hlsUrl = url.endsWith('.m3u8');
    const nativeHls = v.canPlayType('application/vnd.apple.mpegurl') && /^((?!chrome|android).)*safari/i.test(navigator.userAgent);
    if (hlsUrl && !nativeHls && window.Hls?.isSupported()) {
      const hls = new window.Hls({ startPosition: startAt || -1, maxBufferLength: 60, backBufferLength: 90, enableWorker: true });
      this.hls = hls;
      hls.on(window.Hls.Events.ERROR, (_, data) => {
        if (!data.fatal) return;
        if (data.type === window.Hls.ErrorTypes.MEDIA_ERROR) hls.recoverMediaError();
        else this.setWait('Playback failed: ' + (data.details || data.type), true);
      });
      hls.loadSource(url);
      hls.attachMedia(v);
    } else {
      v.src = url;
      if (startAt) v.addEventListener('loadedmetadata', () => (v.currentTime = startAt), { once: true });
    }
    for (const s of this.res.subtitles || []) {
      if (!s.url) continue;
      const t = document.createElement('track');
      t.kind = 'subtitles';
      t.src = s.url;
      t.srclang = s.language || 'und';
      t.label = trackLabel(s);
      t.dataset.index = s.index;
      v.append(t);
    }
    v.addEventListener('playing', () => this.setWait(null), { once: true });
    v.addEventListener('canplay', () => this.setWait(null), { once: true });
    v.play().catch(() => this.setWait(null));
    this.selectSubs($('[data-subs]', this.root).value);
    this.onMove();
  }

  // ---------- controls

  dur() {
    const d = this.video.duration;
    return d && isFinite(d) ? d : this.duration || 0;
  }

  toggle() {
    const v = this.video;
    if (v.paused) v.play().catch(() => {}); else v.pause();
  }

  seek(t) {
    this.video.currentTime = Math.max(0, Math.min(this.dur() - 0.5, t));
    this.update();
  }

  chapterAt(t) {
    let c = null;
    for (const ch of this.chapters) if (ch.start <= t + 0.01) c = ch;
    return c;
  }

  // drawMarks places chapter ticks and shades the intro/credits on the timeline.
  drawMarks() {
    const d = this.dur();
    const marks = $('.marks', this.root);
    if (!d || !marks) return;
    const pct = (t) => `${Math.min(100, Math.max(0, (t / d) * 100)).toFixed(3)}%`;
    let html = this.chapters.filter((c) => c.start > 0.5).map((c) => `<i class="tick" style="left:${pct(c.start)}"></i>`).join('');
    for (const k of ['intro', 'credits']) {
      const m = this.markers[k];
      if (m) html += `<i class="zone" title="${k}" style="left:${pct(m.start)};width:${pct(m.end - m.start)}"></i>`;
    }
    marks.innerHTML = html;
  }

  bindTimeline() {
    const tl = $('[data-timeline]', this.root);
    const tip = $('.tip', tl);
    const at = (e) => {
      const r = tl.getBoundingClientRect();
      return Math.min(1, Math.max(0, (e.clientX - r.left) / r.width)) * this.dur();
    };
    tl.addEventListener('pointermove', (e) => {
      const t = at(e);
      const ch = this.chapterAt(t);
      tip.innerHTML = `${ch?.title ? `<b>${esc(ch.title)}</b>` : ''}${fmtDuration(t)}`;
      tip.style.left = `${(t / (this.dur() || 1)) * 100}%`;
      if (this.dragging) this.paint(t);
    });
    tl.addEventListener('pointerdown', (e) => {
      this.dragging = true;
      tl.classList.add('drag');
      tl.setPointerCapture(e.pointerId);
      this.paint(at(e));
    });
    tl.addEventListener('pointerup', (e) => {
      if (!this.dragging) return;
      this.dragging = false;
      tl.classList.remove('drag');
      this.seek(at(e));
    });
  }

  paint(t) {
    const d = this.dur() || 1;
    const p = `${Math.min(100, (t / d) * 100)}%`;
    $('.done', this.root).style.width = p;
    $('.knob', this.root).style.left = p;
  }

  update() {
    const v = this.video;
    const r = this.root;
    const t = v.currentTime || 0;
    const d = this.dur();
    if (!this.dragging) this.paint(t);
    let buf = 0;
    for (let i = 0; i < v.buffered.length; i++) if (v.buffered.start(i) <= t + 1) buf = Math.max(buf, v.buffered.end(i));
    $('.buf', r).style.width = `${d ? Math.min(100, (buf / d) * 100) : 0}%`;
    $('.time', r).textContent = `${fmtDuration(t)} / ${fmtDuration(d)}`;
    $('.chap', r).textContent = this.chapterAt(t)?.title || '';
    $('[data-toggle]', r).innerHTML = v.paused ? ico.play : ico.pause;
    this.updateSkip(t);
  }

  // updateSkip shows "Skip Intro" inside the intro, and at the credits either
  // the next-episode countdown or "Skip Credits".
  updateSkip(t) {
    const { intro, credits } = this.markers;
    const inIntro = intro && t >= intro.start && t < intro.end - 1 && !this.dismissed.has('intro');
    const inCredits = credits && t >= credits.start && t < credits.end - 1 && !this.dismissed.has('credits');
    let btn = $('.skip', this.root);
    if (inIntro) {
      if (!btn || btn.dataset.kind !== 'intro') {
        btn?.remove();
        btn = el('<button class="skip" data-kind="intro" title="S">Skip Intro</button>');
        btn.onclick = () => { this.seek(intro.end); btn.remove(); };
        this.root.append(btn);
      }
      return;
    }
    if (btn?.dataset.kind === 'intro') btn.remove();
    if (!inCredits) {
      if (btn?.dataset.kind === 'credits') btn.remove();
      if (credits && t < credits.start) this.creditsShown = false; // seeked back: offer again
      return;
    }
    if (this.creditsShown) return;
    this.creditsShown = true;
    if (this.item?.kind === 'episode') {
      this.nextEpisode().then((next) => {
        if (next) this.showUpNext(next, { atCredits: true });
        else this.showSkipCredits();
      });
    } else {
      this.showSkipCredits();
    }
  }

  showSkipCredits() {
    if ($('.skip', this.root) || this.closed) return;
    const btn = el('<button class="skip" data-kind="credits" title="S">Skip Credits</button>');
    btn.onclick = async () => {
      btn.remove();
      this.dismissed.add('credits');
      // Skipping the credits finishes the title.
      const d = this.dur();
      await api(`/api/items/${this.itemId}/progress`, { method: 'POST', body: { position: d, duration: d } }).catch(() => {});
      this.close();
    };
    this.root.append(btn);
  }

  toggleChapters(force) {
    const r = this.root;
    const open = $('.chapters', r);
    if (open || force === false) { open?.remove(); return; }
    const t = this.video.currentTime;
    const cur = this.chapterAt(t);
    const menu = el(`<div class="chapters"><h3>Chapters</h3>${this.chapters.map((c, i) => `<button data-i="${i}" class="${c === cur ? 'cur' : ''}">
      <span class="ts">${fmtDuration(c.start)}</span><span class="t">${esc(c.title || `Chapter ${i + 1}`)}</span></button>`).join('')}</div>`);
    for (const b of $$('button', menu)) b.onclick = () => { this.seek(this.chapters[Number(b.dataset.i)].start); menu.remove(); };
    r.append(menu);
    $('.cur', menu)?.scrollIntoView({ block: 'center' });
  }

  // ---------- tracks

  fillTracks() {
    const info = this.res.info || {};
    const a = $('[data-audio]', this.root);
    const audio = info.audio || [];
    a.innerHTML = audio.map((s) => `<option value="${s.typeIndex}">${esc(trackLabel(s))}</option>`).join('');
    a.value = String(this.res.decision?.audioIndex ?? audio[0]?.typeIndex ?? '');
    a.hidden = audio.length < 2;
    const subs = (this.res.subtitles || []).filter((s) => s.url);
    const sel = $('[data-subs]', this.root);
    const prev = sel.value;
    sel.innerHTML = `<option value="">Subtitles off</option>` + subs.map((s) => `<option value="${s.index}">${esc(trackLabel(s))}</option>`).join('');
    const forced = subs.find((s) => s.forced);
    sel.value = subs.some((s) => String(s.index) === prev) ? prev : forced ? String(forced.index) : '';
    sel.hidden = !subs.length;
  }

  selectSubs(index) {
    for (const t of this.video.textTracks) t.mode = 'disabled';
    if (index === '') return;
    const els = [...this.video.querySelectorAll('track')];
    const i = els.findIndex((t) => t.dataset.index === String(index));
    if (i >= 0 && this.video.textTracks[i]) this.video.textTracks[i].mode = 'showing';
  }

  async reload() {
    const at = this.video.currentTime || 0;
    await this.stopSession();
    await this.play(at);
  }

  setWait(msg, isError = false) {
    const w = $('.wait', this.root);
    w.hidden = !msg;
    if (!msg) return;
    $('.spinner', w).hidden = isError;
    const m = $('.msg', w);
    m.textContent = msg;
    m.className = 'msg ' + (isError ? 'error' : 'muted');
  }

  mediaError() {
    const err = this.video.error;
    if (!err || this.hls) return;
    this.setWait(`This browser can't play this stream (${err.message || 'error ' + err.code}). Try a lower quality.`, true);
  }

  report(force) {
    const v = this.video;
    if (!this.sid || !v.currentTime) return;
    const now = Date.now();
    if (!force && now - this.lastReport < 10000) return;
    this.lastReport = now;
    api(`/api/items/${this.itemId}/progress`, { method: 'POST', body: { position: v.currentTime, duration: this.dur() } }).catch(() => {});
  }

  // ---------- up next

  // nextEpisode is the episode after this one: the next in the season, else
  // the first of the next season.
  async nextEpisode() {
    if (this._next !== undefined) return this._next;
    const it = this.item;
    let next = null;
    try {
      const eps = await api(`/api/items/${it.parentId}/children`);
      next = eps.find((e) => e.episodeNumber > it.episodeNumber) || null;
      if (!next) {
        const seasons = await api(`/api/items/${it.showId}/children`);
        const s = seasons.filter((x) => x.seasonNumber > it.seasonNumber && x.seasonNumber > 0).sort((a, b) => a.seasonNumber - b.seasonNumber)[0];
        if (s) next = (await api(`/api/items/${s.id}/children`))[0] || null;
      }
    } catch {}
    this._next = next;
    return next;
  }

  async ended() {
    this.report(true);
    if (this.item?.kind !== 'episode' || $('.upnext', this.root)) return;
    const next = await this.nextEpisode();
    if (next && !this.closed) this.showUpNext(next, { atCredits: false });
  }

  showUpNext(next, { atCredits }) {
    if (this.closed || $('.upnext', this.root)) return;
    $('.skip', this.root)?.remove();
    const total = 10;
    let n = total;
    const card = el(`<div class="upnext"><div class="thumb">${next.stillPath ? `<img src="${img(next.stillPath, 'w300')}" alt="">` : ''}</div>
      <div style="min-width:0"><div class="small muted">Next episode in <b data-n>${n}</b></div>
      <div style="font-weight:650">S${next.seasonNumber} · E${next.episodeNumber} — ${esc(next.title)}</div>
      <div class="ring"><i style="width:100%"></i></div>
      <div class="actions" style="margin-top:10px"><button class="btn primary small" data-go>Next Episode</button>
      <button class="btn small" data-x>${atCredits ? 'Watch Credits' : 'Cancel'}</button></div></div></div>`);
    this.root.append(card);
    requestAnimationFrame(() => ($('.ring i', card).style.width = `${((n - 1) / total) * 100}%`));
    const timer = setInterval(() => {
      n--;
      $('[data-n]', card).textContent = n;
      $('.ring i', card).style.width = `${(Math.max(0, n - 1) / total) * 100}%`;
      if (n <= 0) go();
    }, 1000);
    const go = () => {
      clearInterval(timer);
      // Moving on counts as finishing this episode.
      const d = this.dur();
      api(`/api/items/${this.itemId}/progress`, { method: 'POST', body: { position: d, duration: d } }).catch(() => {});
      openPlayer(next.id, { ...this.opts, start: undefined, resume: true, fileId: undefined });
    };
    $('[data-go]', card).onclick = go;
    $('[data-x]', card).onclick = () => { clearInterval(timer); card.remove(); this.dismissed.add('credits'); };
  }

  showInfo() {
    const r = this.res;
    if (!r) return;
    const d = r.decision || {};
    const v = r.info?.video?.[0];
    const mk = r.markers?.source ? ` · skip markers from ${r.markers.source}` : '';
    toast(`${d.method?.toUpperCase()} · video ${d.videoCopy ? 'original' : 'transcoded'} (${v?.codec || '?'}${v?.hdr ? ' ' + v.hdr : ''}) · audio ${d.audioCopy ? 'original' : '→ ' + (d.audioCodec || '').toUpperCase()}${mk}${(d.reasons || []).length ? ' — ' + d.reasons.join('; ') : ''}`, 7000);
  }

  toggleFullscreen() {
    if (document.fullscreenElement) document.exitFullscreen();
    else this.root.requestFullscreen?.() || this.video.webkitEnterFullscreen?.();
  }

  async stopSession() {
    this.hls?.destroy();
    this.hls = null;
    this.video.removeAttribute('src');
    this.video.load();
    if (this.sid) {
      const sid = this.sid;
      this.sid = null;
      await api(`/api/sessions/${sid}`, { method: 'DELETE' }).catch(() => {});
    }
  }

  async close(notify = true) {
    if (this.closed) return;
    this.report(true);
    this.closed = true;
    document.removeEventListener('keydown', this.onKey);
    if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
    await this.stopSession();
    this.root.remove();
    document.body.style.overflow = '';
    if (current === this) current = null;
    if (notify) this.opts.onClose?.();
  }
}

function trackLabel(s) {
  const parts = [];
  let lang = s.language && s.language !== 'und' ? s.language : '';
  try { if (lang) lang = new Intl.DisplayNames([navigator.language], { type: 'language' }).of(lang) || lang; } catch {}
  if (lang) parts.push(lang);
  if (s.title) parts.push(s.title);
  if (s.codec && s.channels !== undefined) {
    let c = s.codec.toUpperCase();
    if (s.codec === 'eac3') c = s.profile?.includes('Atmos') ? 'Dolby Atmos' : 'Dolby Digital+';
    else if (s.codec === 'truehd') c = s.profile?.includes('Atmos') ? 'TrueHD Atmos' : 'TrueHD';
    else if (s.codec === 'ac3') c = 'Dolby Digital';
    else if (s.codec === 'dts') c = s.profile || 'DTS';
    if (s.channels) c += ' ' + (s.channels === 8 ? '7.1' : s.channels === 6 ? '5.1' : s.channels === 2 ? 'Stereo' : s.channels + 'ch');
    parts.push(c);
  }
  if (s.forced) parts.push('Forced');
  return parts.join(' · ') || `Track ${s.index + 1}`;
}

window.addEventListener('beforeunload', () => current?.report(true));
export { fmtDuration };
