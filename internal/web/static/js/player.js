// Full-screen web player: <video> + hls.js (native HLS on Safari).
import { api, browserCaps, img } from './api.js';
import { esc, $, el, toast, fmtDuration } from './ui.js';

const qualities = [
  ['orig', 'Original quality', 0, 0],
  ['20', '20 Mbps 1080p', 20000, 1920],
  ['8', '8 Mbps 1080p', 8000, 1920],
  ['4', '4 Mbps 720p', 4000, 1280],
  ['2', '2 Mbps 720p', 2000, 1280],
];

const lsGet = (k, d) => { try { return localStorage.getItem(k) ?? d; } catch { return d; } };
const lsSet = (k, v) => { try { localStorage.setItem(k, v); } catch {} };

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
    this.quality = lsGet('haven.quality', 'orig');
    this.audioIndex = undefined;
    this.fileId = opts.fileId;
    this.sid = null;
    this.hls = null;
    this.lastReport = 0;
    this.closed = false;
    this.root = el(`<div class="player">
      <video playsinline controls autoplay crossorigin="anonymous"></video>
      <div class="wait"><div><div class="spinner"></div><div class="msg muted">Starting…</div></div></div>
      <div class="top">
        <button class="btn ghost" data-close title="Back (Esc)">←</button>
        <div class="ttl"><h2></h2><div class="sub small muted"></div></div>
        <select data-audio title="Audio" hidden></select>
        <select data-subs title="Subtitles" hidden></select>
        <select data-quality title="Quality">${qualities.map(([k, l]) => `<option value="${k}">${l}</option>`).join('')}</select>
        <button class="btn ghost small" data-info title="Playback info">ⓘ</button>
      </div></div>`);
    this.video = $('video', this.root);
    $('[data-quality]', this.root).value = this.quality;
  }

  async start() {
    $('#player-root').replaceChildren(this.root);
    document.body.style.overflow = 'hidden';
    const r = this.root;
    $('[data-close]', r).onclick = () => this.close();
    $('[data-info]', r).onclick = () => this.showInfo();
    $('[data-quality]', r).onchange = (e) => {
      this.quality = e.target.value;
      lsSet('haven.quality', this.quality);
      this.reload();
    };
    $('[data-audio]', r).onchange = (e) => { this.audioIndex = Number(e.target.value); this.reload(); };
    $('[data-subs]', r).onchange = (e) => this.selectSubs(e.target.value);
    this.onKey = (e) => {
      if (e.target.closest?.('select, input')) return;
      if (e.key === 'Escape' && !document.fullscreenElement) this.close();
      else if (e.key === 'f') this.toggleFullscreen();
      else if (e.key === ' ' && e.target === document.body) { e.preventDefault(); this.video.paused ? this.video.play() : this.video.pause(); }
      else if (e.key === 'ArrowRight' && e.target === document.body) this.video.currentTime += 10;
      else if (e.key === 'ArrowLeft' && e.target === document.body) this.video.currentTime -= 10;
    };
    document.addEventListener('keydown', this.onKey);
    // Hide the top bar while playing and idle.
    let idle;
    this.onMove = () => {
      r.classList.remove('idle');
      clearTimeout(idle);
      idle = setTimeout(() => !this.video.paused && r.classList.add('idle'), 2800);
    };
    r.addEventListener('mousemove', this.onMove);
    this.video.addEventListener('timeupdate', () => this.report(false));
    this.video.addEventListener('pause', () => { r.classList.remove('idle'); this.report(true); });
    this.video.addEventListener('ended', () => this.ended());
    this.video.addEventListener('error', () => this.mediaError());
    this.video.addEventListener('dblclick', () => this.toggleFullscreen());

    try {
      const d = await api('/api/items/' + this.itemId);
      this.item = d.item;
      this.show = d.show;
      $('h2', r).textContent = this.item.kind === 'episode' ? this.show?.title || this.item.title : this.item.title;
      $('.sub', r).textContent = this.item.kind === 'episode' ? `S${this.item.seasonNumber} · E${this.item.episodeNumber} — ${this.item.title}` : this.item.year || '';
      const bg = this.item.backdropPath || this.show?.backdropPath;
      if (bg) this.video.poster = img(bg, 'w1280');
    } catch {}
    let start = this.opts.start;
    await this.play(start);
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
    if (startAt === undefined) startAt = this.opts.resume ? res.resumePosition || 0 : 0;
    this.fillTracks();
    this.attach(res.url, startAt);
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
    const duration = v.duration && isFinite(v.duration) ? v.duration : this.duration;
    api(`/api/items/${this.itemId}/progress`, { method: 'POST', body: { position: v.currentTime, duration } }).catch(() => {});
  }

  async ended() {
    this.report(true);
    if (this.item?.kind !== 'episode' || !this.item.showId) return;
    let next;
    try { next = (await api('/api/items/' + this.item.showId)).nextEpisode; } catch {}
    if (!next || next.id === this.itemId || this.closed) return;
    let n = 10;
    const card = el(`<div class="upnext"><div class="thumb">${next.stillPath ? `<img src="${img(next.stillPath, 'w300')}" alt="">` : ''}</div>
      <div><div class="small muted">Up next in <b data-n>${n}</b></div><div style="font-weight:600">S${next.seasonNumber} · E${next.episodeNumber} — ${esc(next.title)}</div>
      <div class="actions" style="margin-top:8px"><button class="btn primary small" data-go>Play now</button><button class="btn small" data-x>Cancel</button></div></div></div>`);
    this.root.append(card);
    const t = setInterval(() => {
      n--;
      $('[data-n]', card).textContent = n;
      if (n <= 0) go();
    }, 1000);
    const go = () => { clearInterval(t); openPlayer(next.id, { ...this.opts, start: undefined, resume: true, fileId: undefined }); };
    $('[data-go]', card).onclick = go;
    $('[data-x]', card).onclick = () => { clearInterval(t); card.remove(); };
  }

  showInfo() {
    const r = this.res;
    if (!r) return;
    const d = r.decision || {};
    const v = r.info?.video?.[0];
    toast(`${d.method?.toUpperCase()} · video ${d.videoCopy ? 'original' : 'transcoded'} (${v?.codec || '?'}${v?.hdr ? ' ' + v.hdr : ''}) · audio ${d.audioCopy ? 'original' : '→ ' + (d.audioCodec || '').toUpperCase()}${(d.reasons || []).length ? ' — ' + d.reasons.join('; ') : ''}`, 7000);
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
