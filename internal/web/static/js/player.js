// Full-screen web player: <video> + hls.js (native HLS on Safari), with its
// own controls for chapters, skip intro/credits and up next.
import { api, browserCaps, img, ApiError } from './api.js';
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
  episodes: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="5" width="13" height="10" rx="2"/><path d="M7 19h13M20 9v10"/></svg>',
  full: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 9V4h5M20 9V4h-5M4 15v5h5M20 15v5h-5"/></svg>',
};

// UPNEXT_LEAD is how many seconds before an unmarked episode ends the
// next-episode countdown starts.
const UPNEXT_LEAD = 30;

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
    this.quality = lsGet('wrentv.quality', 'orig');
    this.audioIndex = undefined;
    // A version the viewer picked sticks; otherwise the server picks the best
    // one for the quality setting (e.g. the 1080p file rather than transcoding 4K).
    this.fileId = opts.fileId;
    this.pinnedFile = !!opts.fileId;
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
        <select data-version data-caption="Version" title="Version" hidden></select>
        <select data-audio data-caption="Audio" title="Audio" hidden></select>
        <select data-subs data-caption="Subtitles" title="Subtitles" hidden></select>
        <select data-quality data-caption="Quality" title="Quality">${qualities.map(([k, l]) => `<option value="${k}">${l}</option>`).join('')}</select>
        <button class="btn ghost small" data-info title="Stats for nerds (I)">ⓘ</button>
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
          <button class="cbtn" data-episodes title="Episodes (E)" hidden>${ico.episodes}</button>
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
    $('[data-info]', r).onclick = () => this.toggleStats();
    $('[data-quality]', r).onchange = (e) => {
      this.quality = e.target.value;
      lsSet('wrentv.quality', this.quality);
      if (!this.pinnedFile) this.fileId = undefined;
      this.reload();
    };
    $('[data-audio]', r).onchange = (e) => { this.audioIndex = Number(e.target.value); this.reload(); };
    $('[data-version]', r).onchange = (e) => {
      this.fileId = e.target.value;
      this.pinnedFile = true;
      this.audioIndex = undefined; // track numbers differ between files
      this.subsPicked = false;
      this.reload();
    };
    $('[data-subs]', r).onchange = (e) => {
      if (e.target.value === 'online') {
        e.target.value = this.subsValue || '';
        this.searchSubs();
        return;
      }
      this.subsPicked = true;
      this.selectSubs(e.target.value);
    };
    $('[data-toggle]', r).onclick = () => this.toggle();
    $('[data-back]', r).onclick = () => this.seek(v.currentTime - 10);
    $('[data-fwd]', r).onclick = () => this.seek(v.currentTime + 10);
    $('[data-full]', r).onclick = () => this.toggleFullscreen();
    $('[data-chapters]', r).onclick = () => this.toggleChapters();
    $('[data-episodes]', r).onclick = () => this.toggleEpisodes();
    $('[data-mute]', r).onclick = () => { v.muted = !v.muted; };
    $('[data-volume]', r).oninput = (e) => { v.volume = Number(e.target.value); v.muted = v.volume === 0; lsSet('wrentv.volume', v.volume); };
    this.bindTimeline();

    this.onKey = (e) => {
      if (e.target.closest?.('select, input, .dd-menu')) return;
      const k = e.key;
      if (k === 'Escape') {
        if ($('.chapters', r)) return $('.chapters', r).remove();
        if ($('.ep-shelf', r)) return this.closeEpisodes();
        if (!document.fullscreenElement) this.close();
      } else if (k === 'f') this.toggleFullscreen();
      else if (k === ' ' || k === 'k') { e.preventDefault(); this.toggle(); }
      else if (k === 'ArrowRight') { e.preventDefault(); this.seek(v.currentTime + 10); }
      else if (k === 'ArrowLeft') { e.preventDefault(); this.seek(v.currentTime - 10); }
      else if (k === 'm') v.muted = !v.muted;
      else if (k === 'c' && this.chapters.length) this.toggleChapters();
      else if (k === 'e' && this.item?.kind === 'episode') this.toggleEpisodes();
      else if (k === 'ArrowDown' && this.item?.kind === 'episode' && !$('.ep-shelf', r)) { e.preventDefault(); this.toggleEpisodes(); }
      else if (k === 'ArrowUp' && $('.ep-shelf', r)) { e.preventDefault(); this.closeEpisodes(); }
      else if (k === 'i') this.toggleStats();
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
      idle = setTimeout(() => !v.paused && !$('.chapters, .dd-menu, .ep-shelf', r) && r.classList.add('idle'), 2800);
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
      $('[data-episodes]', r).hidden = this.item.kind !== 'episode';
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
    // A play started again after a failure gets a fresh link, and the
    // version that broke is skipped; only the first ask says so.
    let retry = this.failed ? { retry: true, failedFileId: this.failed } : {};
    this.failed = undefined;
    for (;;) {
      if (this.closed) return;
      try {
        res = await api(`/api/items/${this.itemId}/play`, { method: 'POST', body: { caps, maxWidth, fileId: this.fileId, audioIndex: this.audioIndex, ...retry } });
      } catch (e) {
        this.setWait(e.message, true);
        return;
      }
      retry = {};
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
    this.watchVersions();
  }

  // A streamed title's lookup goes on after play starts: pick up versions it
  // finds while it's still looking.
  watchVersions() {
    clearTimeout(this.versionTimer);
    let tries = 0;
    const tick = async () => {
      const looking = await this.refreshVersions();
      if (looking && ++tries < 15 && !this.closed) this.versionTimer = setTimeout(tick, 20000);
    };
    this.versionTimer = setTimeout(tick, 5000);
  }

  async refreshVersions() {
    try {
      const d = await api(`/api/items/${this.itemId}/versions`);
      if (this.closed || !this.res) return false;
      this.res.versions = d.versions || [];
      this.fillVersions();
      return d.looking;
    } catch { return false; }
  }

  fillVersions() {
    const vs = this.res.versions || [];
    const ver = $('[data-version]', this.root);
    if (ver.matches(':focus')) return; // don't redraw an open picker
    ver.innerHTML = vs.map((v) => `<option value="${esc(v.id)}">${esc(v.label)}</option>`).join('');
    ver.value = this.res.fileId || '';
    ver.hidden = vs.length < 2;
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
        else if (data.response?.code === 410) {
          this.setWait('Playback failed: ' + (data.details || data.type), true);
          this.report(true); // stopped: the progress answer says why
        } else if (!this.recover()) {
          this.setWait('Playback failed: ' + (data.details || data.type), true);
        }
      });
      hls.loadSource(url);
      hls.attachMedia(v);
    } else {
      v.src = url;
      if (startAt) v.addEventListener('loadedmetadata', () => (v.currentTime = startAt), { once: true });
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
  // the next-episode countdown or "Skip Credits". An episode with no credits
  // marker gets the countdown over its last seconds instead.
  updateSkip(t) {
    const { intro } = this.markers;
    const credits = this.markers.credits || this.closingStretch();
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
        if (next) this.showUpNext(next, { atCredits: !credits.guessed });
        else if (!credits.guessed) this.showSkipCredits();
      });
    } else {
      this.showSkipCredits();
    }
  }

  // closingStretch stands in for unmarked credits on an episode: long enough
  // before the end that the countdown finishes before the picture does.
  closingStretch() {
    const d = this.dur();
    if (this.item?.kind !== 'episode' || d < 300) return null;
    return { start: d - UPNEXT_LEAD, end: d + 1, guessed: true };
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
    open?.remove();
    if (open?.dataset.panel === 'chapters' || force === false) return;
    const t = this.video.currentTime;
    const cur = this.chapterAt(t);
    const menu = el(`<div class="chapters" data-panel="chapters"><h3>Chapters</h3>${this.chapters.map((c, i) => `<button data-i="${i}" class="${c === cur ? 'cur' : ''}">
      <span class="ts">${fmtDuration(c.start)}</span><span class="t">${esc(c.title || `Chapter ${i + 1}`)}</span></button>`).join('')}</div>`);
    for (const b of $$('button', menu)) b.onclick = () => { this.seek(this.chapters[Number(b.dataset.i)].start); menu.remove(); };
    r.append(menu);
    $('.cur', menu)?.scrollIntoView({ block: 'center' });
  }

  // ---------- tracks

  fillTracks() {
    const info = this.res.info || {};
    this.fillVersions();
    const a = $('[data-audio]', this.root);
    const audio = info.audio || [];
    a.innerHTML = audio.map((s) => `<option value="${s.typeIndex}">${esc(trackLabel(s))}</option>`).join('');
    a.value = String(this.res.decision?.audioIndex ?? audio[0]?.typeIndex ?? '');
    a.hidden = audio.length < 2;
    const subs = this.res.subtitles || [];
    const sel = $('[data-subs]', this.root);
    const prev = sel.value;
    const text = subs.filter((s) => s.url);
    sel.innerHTML = `<option value="">Subtitles off</option>` + subs.map((s) => s.url
      ? `<option value="${s.index}">${esc(trackLabel(s))}</option>`
      : `<option value="${s.index}" disabled>${esc(trackLabel(s))} (picture, app only)</option>`).join('')
      + (this.res.onlineSubtitles ? '<option value="online">Search online…</option>' : '');
    // Keep the viewer's choice across reloads (quality changes); otherwise use
    // the track the server picked from their subtitle language setting.
    const pick = this.subsPicked ? prev : String(this.res.subtitleIndex ?? -1);
    sel.value = text.some((s) => String(s.index) === pick) ? pick : '';
    sel.hidden = !subs.length && !this.res.onlineSubtitles;
  }

  selectSubs(index) {
    this.subsValue = index;
    const gen = (this.subsGen = (this.subsGen || 0) + 1);
    clearTimeout(this.subsTimer);
    const v = this.video;
    if (!this.cueTrack) {
      this.cueTrack = v.addTextTrack('subtitles', 'Subtitles');
    }
    const track = this.cueTrack;
    for (const c of [...(track.cues || [])]) track.removeCue(c);
    for (const t of v.textTracks) if (t !== track) t.mode = 'disabled';
    const sub = (this.res?.subtitles || []).find((s) => String(s.index) === String(index) && s.url);
    track.mode = sub ? 'showing' : 'disabled';
    if (!sub) return;
    let added = 0;
    const load = async () => {
      let complete = true;
      try {
        const r = await fetch(sub.url + (sub.url.includes('?') ? '&' : '?') + 'partial=1', { cache: 'no-store' });
        if (!r.ok) throw new Error(r.status);
        complete = r.headers.get('X-Subtitle-Complete') !== 'false';
        const cues = parseVTT(await r.text(), complete);
        if (gen !== this.subsGen) return;
        for (; added < cues.length; added++) {
          const c = cues[added];
          try { track.addCue(new VTTCue(c.start, c.end, c.text)); } catch {}
        }
      } catch {
        complete = false;
      }
      // Large files on network storage take minutes to convert: show what's
      // ready and keep fetching the rest.
      if (!complete && gen === this.subsGen) this.subsTimer = setTimeout(load, 5000);
    };
    load();
  }

  // recover starts a stream that broke partway again from the same spot,
  // once every two minutes: the server resolves a fresh link, or plays
  // another version if this one keeps failing.
  recover() {
    const now = Date.now();
    if (!this.res?.fileId || now - (this.lastRecover || 0) < 120000) return false;
    this.lastRecover = now;
    this.failed = this.res.fileId;
    if (!this.pinnedFile) this.fileId = undefined;
    this.reload();
    return true;
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
    // A network error partway through is the stream breaking, not the browser.
    if (err.code === MediaError.MEDIA_ERR_NETWORK && this.video.currentTime > 0 && this.recover()) return;
    this.setWait(`This browser can't play this stream (${err.message || 'error ' + err.code}). Try a lower quality.`, true);
    this.report(true);
  }

  report(force) {
    const v = this.video;
    if (!this.sid || !v.currentTime) return;
    const now = Date.now();
    if (!force && now - this.lastReport < 10000) return;
    this.lastReport = now;
    api(`/api/items/${this.itemId}/progress`, { method: 'POST', body: { position: v.currentTime, duration: this.dur(), state: v.paused ? 'paused' : 'playing' } })
      .catch((e) => { if (e instanceof ApiError && e.status === 410) this.stoppedByServer(e.message); });
  }

  // stoppedByServer ends playback the server owner stopped, saying why.
  stoppedByServer(msg) {
    this.sid = null;
    this.hls?.destroy();
    this.hls = null;
    this.video.pause();
    this.video.removeAttribute('src');
    this.video.load();
    this.setWait(msg || 'The server owner stopped this stream.', true);
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

  // ---------- stats

  // toggleStats shows what's playing and how, refreshed every second.
  toggleStats() {
    const open = $('.stats', this.root);
    clearInterval(this.statsTimer);
    if (open) { open.remove(); return; }
    const box = el('<div class="stats"><button class="x" title="Close (I)">×</button><dl></dl></div>');
    $('.x', box).onclick = () => this.toggleStats();
    this.root.append(box);
    const draw = () => { if (box.isConnected) $('dl', box).innerHTML = this.statsRows().map(([k, v]) => `<dt>${esc(k)}</dt><dd>${esc(v)}</dd>`).join(''); };
    draw();
    this.statsTimer = setInterval(draw, 1000);
  }

  statsRows() {
    const r = this.res;
    if (!r) return [['Status', 'Starting']];
    const d = r.decision || {};
    const info = r.info || {};
    const v = info.video?.[0];
    const a = (info.audio || []).find((x) => x.typeIndex === d.audioIndex) || info.audio?.[0];
    const vid = this.video;
    const mbps = (b) => (b ? `${(b / 1e6).toFixed(1)} Mbps` : '');
    let ahead = 0;
    for (let i = 0; i < vid.buffered.length; i++) {
      if (vid.buffered.start(i) <= vid.currentTime + 0.5) ahead = Math.max(ahead, vid.buffered.end(i) - vid.currentTime);
    }
    const q = vid.getVideoPlaybackQuality?.();
    const version = (r.versions || []).find((x) => x.id === r.fileId)?.label;
    const rows = [
      ['Playback', `${(d.method || '').toUpperCase()}${d.engine ? ' · ' + d.engine : ''}`],
      ['Version', version || ''],
      ['Container', info.container || ''],
      ['Video', v ? `${v.codec || '?'} ${v.width}×${v.height}${v.hdr ? ' ' + v.hdr : ''} → ${d.videoCopy ? 'original' : 'transcoded'}` : ''],
      ['Audio', a ? `${a.codec || '?'}${a.channels ? ' ' + a.channels + 'ch' : ''}${a.language ? ' · ' + a.language : ''} → ${d.audioCopy ? 'original' : (d.audioCodec || '').toUpperCase()}` : ''],
      ['Source bitrate', mbps(info.bitrate)],
      ['Showing', vid.videoWidth ? `${vid.videoWidth}×${vid.videoHeight}` : ''],
      ['Buffer ahead', `${ahead.toFixed(1)} s`],
      ['Dropped frames', q ? `${q.droppedVideoFrames} of ${q.totalVideoFrames}` : ''],
      ['Connection', this.hls?.bandwidthEstimate ? mbps(this.hls.bandwidthEstimate) : ''],
      ['Skip markers', r.markers?.source || 'none'],
      ['Why', (d.reasons || []).join('; ')],
    ];
    return rows.filter(([, val]) => val);
  }

  // ---------- episodes

  // toggleEpisodes opens the season's episodes in a row under the controls,
  // with the show's other seasons above it, or closes it.
  async toggleEpisodes(seasonId) {
    const r = this.root;
    const it = this.item;
    let shelf = $('.ep-shelf', r);
    if (shelf && !seasonId) return this.closeEpisodes();
    if (!it || it.kind !== 'episode') return;
    $('.chapters', r)?.remove();
    if (!shelf) {
      shelf = el('<div class="ep-shelf"><div class="muted small">Loading…</div></div>');
      $('.controls', r).append(shelf);
      $('[data-episodes]', r).classList.add('on');
    }
    try {
      const [seasons, eps] = await Promise.all([
        this.seasons || api(`/api/items/${it.showId}/children`).then((x) => (this.seasons = x.filter((s) => s.kind === 'season'))),
        api(`/api/items/${seasonId || it.parentId}/children`),
      ]);
      if (!shelf.isConnected) return;
      const cur = seasonId || it.parentId;
      shelf.innerHTML = (seasons.length > 1 ? `<div class="seasons">${seasons.map((s) =>
        `<button class="chip ${s.id === cur ? 'on' : ''}" data-season="${esc(s.id)}">${esc(s.title || 'Season ' + s.seasonNumber)}</button>`).join('')}</div>` : '')
        + `<div class="row">${eps.map((e) => {
          const p = e.progress;
          const part = p && !p.watched && p.position > 0 && p.duration > 0 ? Math.min(100, (p.position / p.duration) * 100) : 0;
          return `<button data-id="${esc(e.id)}" class="ep ${e.id === this.itemId ? 'cur' : ''}">
          <span class="still">${e.stillPath ? `<img src="${img(e.stillPath, 'w300')}" alt="" loading="lazy">` : ''}
            ${e.id === this.itemId ? '<i class="playing">Playing</i>' : p?.watched ? '<i class="seen">✓</i>' : ''}
            ${part && e.id !== this.itemId ? `<span class="prog"><span style="width:${part}%"></span></span>` : ''}</span>
          <b>${e.episodeNumber}. ${esc(e.title)}</b>${e.runtimeMin ? `<span class="muted small">${e.runtimeMin} min</span>` : ''}</button>`;
        }).join('')}</div>`;
      for (const b of $$('[data-season]', shelf)) b.onclick = () => this.toggleEpisodes(b.dataset.season);
      for (const b of $$('button[data-id]', shelf)) {
        b.onclick = () => {
          if (b.dataset.id === this.itemId) return this.closeEpisodes();
          openPlayer(b.dataset.id, { ...this.opts, start: undefined, resume: true, fileId: undefined });
        };
      }
      const at = $('.cur', shelf) || $('button[data-id]', shelf);
      if (at) $('.row', shelf).scrollLeft = at.offsetLeft - 22;
    } catch (e) {
      if (shelf.isConnected) shelf.innerHTML = `<div class="error small">${esc(e.message)}</div>`;
    }
  }

  closeEpisodes() {
    $('.ep-shelf', this.root)?.remove();
    $('[data-episodes]', this.root)?.classList.remove('on');
  }

  // ---------- online subtitles

  // searchSubs looks the title up on the subtitle sites the server has keys
  // for; a pick is fetched, kept for later plays, and turned on.
  async searchSubs(lang) {
    const r = this.root;
    $('.chapters', r)?.remove();
    const menu = el(`<div class="chapters wide" data-panel="subs"><div class="ep-head"><h3>Online subtitles</h3>
      <input data-lang placeholder="Languages, e.g. en,fr" value="${esc(lang || '')}" aria-label="Languages"></div>
      <div data-list><div class="muted small" style="padding:8px 10px">Searching…</div></div></div>`);
    r.append(menu);
    const input = $('[data-lang]', menu);
    input.addEventListener('keydown', (e) => { if (e.key === 'Enter') this.searchSubs(input.value.trim()); e.stopPropagation(); });
    const list = $('[data-list]', menu);
    try {
      const d = await api(`/api/items/${this.itemId}/subtitles/online${lang ? '?lang=' + encodeURIComponent(lang) : ''}`);
      if (!menu.isConnected) return;
      const rs = d.results || [];
      if (!rs.length) { list.innerHTML = '<div class="muted small" style="padding:8px 10px">Nothing found. Try other languages.</div>'; return; }
      list.innerHTML = rs.map((x, i) => `<button data-i="${i}"><span class="ts">${esc((x.language || '?').toUpperCase())}</span>
        <span class="t">${esc(x.name)}<span class="muted small" style="display:block">${esc(x.provider)}${x.downloads ? ` · ${x.downloads.toLocaleString()} downloads` : ''}${x.hearingImpaired ? ' · SDH' : ''}${x.machineTranslated ? ' · machine translated' : ''}</span></span></button>`).join('');
      for (const b of $$('button', list)) b.onclick = () => this.fetchSub(rs[Number(b.dataset.i)], b, menu);
    } catch (e) {
      if (menu.isConnected) list.innerHTML = `<div class="error small" style="padding:8px 10px">${esc(e.message)}</div>`;
    }
  }

  async fetchSub(x, btn, menu) {
    btn.disabled = true;
    btn.classList.add('cur');
    try {
      const t = await api(`/api/items/${this.itemId}/subtitles/online`, { method: 'POST', body: { ...x, sessionId: this.sid } });
      if (this.closed || !this.res) return;
      const subs = (this.res.subtitles ||= []);
      if (!subs.some((s) => s.index === t.index)) subs.push(t);
      this.subsPicked = true;
      this.fillTracks();
      $('[data-subs]', this.root).value = String(t.index);
      this.selectSubs(String(t.index));
      menu.remove();
      toast('Subtitles on: ' + (t.title || t.language));
    } catch (e) {
      btn.disabled = false;
      btn.classList.remove('cur');
      toast(e.message, 6000);
    }
  }

  toggleFullscreen() {
    if (document.fullscreenElement) document.exitFullscreen();
    else this.root.requestFullscreen?.() || this.video.webkitEnterFullscreen?.();
  }

  async stopSession() {
    clearTimeout(this.subsTimer);
    clearTimeout(this.versionTimer);
    this.subsGen = (this.subsGen || 0) + 1;
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
    clearInterval(this.statsTimer);
    document.removeEventListener('keydown', this.onKey);
    if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
    await this.stopSession();
    this.root.remove();
    document.body.style.overflow = '';
    if (current === this) current = null;
    if (notify) this.opts.onClose?.();
  }
}

// Cues from a WebVTT file. A file still being written may end mid-cue, so the
// last block is left for the next fetch unless the file is complete.
function parseVTT(text, complete) {
  const blocks = text.replace(/\r/g, '').split(/\n\n+/);
  if (!complete) blocks.pop();
  const time = (t) => {
    const p = t.trim().split(':').map(Number);
    return p.reduce((acc, x) => acc * 60 + x, 0);
  };
  const out = [];
  for (const b of blocks) {
    const lines = b.split('\n');
    const at = lines.findIndex((l) => l.includes('-->'));
    if (at < 0) continue;
    const [a, z] = lines[at].split('-->');
    const start = time(a), end = time(z.trim().split(/\s+/)[0]);
    const body = lines.slice(at + 1).join('\n').trim();
    if (body && end > start) out.push({ start, end, text: body });
  }
  return out;
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
