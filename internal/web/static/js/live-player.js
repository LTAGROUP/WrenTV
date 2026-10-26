// Full-screen live TV playback. Reuses the server's live sessions: a channel is
// tuned with POST /api/livetv/channels/{id}/play and the session is stopped when
// the overlay closes, so a closed tab does not hold a provider connection.
import { api } from './api.js';
import { el, esc } from './ui.js';
import { nativeHlsSupported, playbackMode } from './live-model.js';
import { audioChoice, subtitleChoice } from './live-tracks.js';
import { AUTO_ROUNDS, autoMessage, planAutoRound } from './addon-playback.js';

// How long a chosen stream has to actually start before the next one is tried.
// A feed that hasn't produced a frame by then is not going to.
const AUTO_START_WAIT = 15_000;

let current = null;

// releaseSession stops a session that arrived after its player was closed.
// Without it, a fast Escape leaks a provider connection.
function releaseSession(sid) {
  if (!sid) return;
  api(`/api/sessions/${encodeURIComponent(sid)}`, { method: 'DELETE' }).catch(() => {});
}

export function closeLivePlayer() {
  current?.close();
  current = null;
}

export async function openLiveChannel(channel) {
  closeLivePlayer();
  const p = new LivePlayer(channel);
  current = p;
  await p.start();
}

class LivePlayer {
  constructor(channel) {
    this.channel = channel || {};
    // An addon source has no playlist channel: the server holds its URL and the
    // player only knows the opaque identity. Everything else is identical.
    this.addonId = this.channel.addonId || null;
    // An addon feed is addressed the same way: the Live TV browser gets a
    // token from the server and never sees the provider's play link.
    this.addonFeedId = this.channel.addonFeedId || null;
    // Automatic playback for an addon item: the source, content type and item
    // id. The server picks, checks and starts the feed, so one click plays the
    // item instead of asking which stream to try.
    this.addonAuto = this.channel.addonAuto || null;
    this.autoTried = [];
    this.autoRemaining = 0;
    // settling fails the feed currently being tried, for failures the player
    // discovers itself (a fatal HLS error, a media error, a timeout);
    // markStarted reports that the feed loaded.
    this.settling = null;
    this.markStarted = null;
    // One automatic attempt at a time: a second Retry click must not start a
    // second loop over the same feeds.
    this.autoRunning = false;
    // A player opened from a sports event carries the event's identity: the
    // competition and lifecycle, the matchup instead of a channel name, and the
    // feed it is playing. That is the sports HUD; track controls stay the
    // player's own.
    this.sports = this.channel.sports || null;
    this.sid = null;
    this.hls = null;
    this.closed = false;
  }

  async start() {
    const name = this.channel.name || 'Live TV';
    const number = this.channel.number ? `${esc(this.channel.number)} · ` : '';
    const sports = this.sports;
    const meta = sports ? [sports.competition, sports.status].filter(Boolean).join(' · ') : '';
    const sub = sports ? (sports.broadcast || '') : `${number}${esc(this.channel.group || 'Live TV')}`;
    this.root = el(`<div class="player live-player">
      <video playsinline autoplay crossorigin="anonymous"></video>
      <div class="wait"><div><div class="spinner"></div><div class="msg muted">Tuning ${esc(name)}…</div></div></div>
      <div class="top">
        <button class="btn ghost" data-close title="Back (Esc)" aria-label="Back">←</button>
        <div class="ttl">
          ${meta ? `<div class="sports-meta small muted">${esc(meta)}</div>` : ''}
          <h2>${esc(sports?.title || name)}</h2>
          <div class="sub small muted">${esc(sub)}</div>
        </div>
        <select data-audio data-caption="Audio" title="Audio" aria-label="Audio track" hidden></select>
        <select data-subs data-caption="Subtitles" title="Subtitles" aria-label="Subtitles" hidden></select>
        ${sports?.href ? '<button class="btn ghost" data-change-source>Change source</button>' : ''}
        <span class="live-pill" data-live-pill role="status" aria-live="polite">${sports ? 'CONNECTING' : 'LIVE'}</span>
      </div>
      <div class="live-error" hidden><div><h2>Can't play this channel</h2><p class="msg muted"></p>
        <div class="actions"><button class="btn primary" data-retry hidden>Try again</button>
        <button class="btn" data-choose hidden>Choose a feed</button>
        <button class="btn" data-close>Close</button></div></div></div>
    </div>`);
    this.video = this.root.querySelector('video');
    // From a sports event the same control is the way back to that event, so it
    // says so; the Live TV player keeps its plain back.
    if (sports) {
      this.root.querySelectorAll('[data-close]').forEach((b) => {
        b.title = 'Back to event (Esc)';
        b.setAttribute('aria-label', 'Back to event');
      });
    }
    // The lifecycle label reflects the element, not the request: a tuned stream
    // is only LIVE once the browser is actually decoding frames.
    this.video.addEventListener('playing', () => {
      this.root.querySelector('[data-live-pill]')?.replaceChildren('LIVE');
    });
    this.root.querySelectorAll('[data-close]').forEach((b) => (b.onclick = () => this.close()));
    this.root.querySelector('[data-retry]').onclick = () => this.retryAuto();
    // Choosing by hand is the honest fallback when no feed starts: the item's
    // own feed list is still one click away.
    this.root.querySelector('[data-choose]').onclick = () => {
      const choose = this.channel.chooseFeeds;
      this.close();
      choose?.();
    };
    // A player is an overlay over the event page, so "Change source" has to
    // leave the overlay and open the chooser: close (releases the session) and
    // hand the destination to the app router through one event.
    const changeSource = this.root.querySelector('[data-change-source]');
    if (changeSource) {
      changeSource.onclick = () => {
        const href = this.sports?.href;
        if (!href) return;
        this.close();
        const join = href.includes('?') ? '&' : '?';
        window.dispatchEvent(new CustomEvent('wrentv:navigate', { detail: `${href}${join}watch=1` }));
      };
    }
    this.onKey = (e) => { if (e.key === 'Escape') this.close(); };
    document.addEventListener('keydown', this.onKey);
    document.body.style.overflow = 'hidden';
    document.getElementById('player-root').replaceChildren(this.root);

    if (this.addonAuto) {
      await this.runAuto();
      return;
    }

    try {
      const res = this.addonId
        ? await api(`/api/sports/addons/${encodeURIComponent(this.addonId)}/play`, { method: 'POST', body: { hls: true } })
        : this.addonFeedId
          ? await api(`/api/livetv/addons/streams/${encodeURIComponent(this.addonFeedId)}/play`, { method: 'POST', body: { hls: true } })
          : await api(`/api/livetv/channels/${encodeURIComponent(this.channel.id)}/play`, { method: 'POST', body: { hls: true } });
      // The viewer can close the player before the tune request settles. The
      // server has already created the session, so the late answer releases it
      // instead of holding a provider connection nobody is watching.
      if (this.closed) {
        releaseSession(res.sessionId);
        return;
      }
      this.sid = res.sessionId || null;
      const mode = playbackMode(res.type, nativeHlsSupported(this.video), !!window.Hls?.isSupported?.());
      if (mode === 'unsupported') {
        throw new Error('This channel is a raw MPEG-TS stream, which browsers cannot play. Open it in the Wren TV app, or enable ffmpeg so the server can repackage it as HLS.');
      }
      await this.attach(res.url, mode);
      this.root.querySelector('.wait')?.setAttribute('hidden', '');
      this.video.play().catch(() => {});
    } catch (err) {
      this.fail(err.message || 'Live TV playback failed.');
    }
  }

  async attach(url, mode) {
    if (!url) throw new Error('The server did not return a stream URL.');
    if (mode === 'native') {
      // Safari exposes the rendition lists on the element itself.
      this.video.addEventListener('loadedmetadata', () => {
        this.markStarted?.();
        this.fillTracks();
      }, { once: true });
      this.video.src = url;
      return;
    }
    if (mode === 'hlsjs') {
      this.hls = new window.Hls({ lowLatencyMode: true, backBufferLength: 60 });
      this.hls.on(window.Hls.Events.ERROR, (_e, data) => {
        if (!data?.fatal) return;
        // While automatic playback is trying a feed, a fatal error is that
        // feed's failure: the next one is tried. A stream that dies mid-play is
        // still reported to the viewer.
        if (this.settling) {
          this.settling();
          return;
        }
        this.fail('The channel stopped responding. Try again or pick another channel.');
      });
      // Alternate audio/subtitle renditions arrive after the master is parsed
      // (hls.js populates them lazily), so the menus follow those events; a
      // stream that publishes none keeps both controls hidden.
      this.hls.on(window.Hls.Events.MANIFEST_PARSED, () => {
        this.markStarted?.();
        this.fillTracks();
      });
      this.hls.on(window.Hls.Events.AUDIO_TRACKS_UPDATED, () => this.fillTracks());
      this.hls.on(window.Hls.Events.SUBTITLE_TRACKS_UPDATED, () => this.fillTracks());
      this.hls.loadSource(url);
      this.hls.attachMedia(this.video);
      // Same handle the docked preview uses, so any surface can inspect the
      // session's tracks.
      this.video._hls = this.hls;
      return;
    }
    throw new Error('This browser cannot play HLS streams.');
  }

  // runAuto plays an addon item without asking which stream to use. Each round
  // asks the server for the best feed it hasn't tried yet, so a dead link, a
  // timeout or a codec the browser can't decode is replaced instead of shown.
  async runAuto() {
    if (this.autoRunning) return;
    this.autoRunning = true;
    try {
      await this.autoLoop();
    } finally {
      this.autoRunning = false;
    }
  }

  async autoLoop() {
    const auto = this.addonAuto;
    this.autoTried = [];
    this.autoRemaining = 0;
    this.hideError();
    this.root.querySelector('[data-live-pill]')?.replaceChildren('CONNECTING');
    for (let round = 0; round < AUTO_ROUNDS; round += 1) {
      if (this.closed) return;
      this.setWait(autoMessage(round));
      let response = null;
      let error = null;
      try {
        response = await api(
          `/api/livetv/addons/${encodeURIComponent(auto.source)}/item/${encodeURIComponent(auto.type)}/${encodeURIComponent(auto.id)}/play`,
          { method: 'POST', body: { hls: true, skip: this.autoTried } },
        );
      } catch (err) {
        error = err;
      }
      if (this.closed) {
        releaseSession(response?.sessionId);
        return;
      }
      const plan = planAutoRound({ round, tried: this.autoTried, response, error, remaining: this.autoRemaining });
      this.autoTried = plan.tried;
      this.autoRemaining = plan.remaining;
      if (plan.action === 'stop') {
        this.fail(plan.message, { retry: true, choose: !!this.channel.chooseFeeds });
        return;
      }
      if (plan.action === 'retry') continue;

      this.sid = response.sessionId || null;
      const mode = playbackMode(response.type, nativeHlsSupported(this.video), !!window.Hls?.isSupported?.());
      if (mode === 'unsupported') {
        this.stopSession();
        if (this.autoRemaining > 0) continue;
        this.fail('This addon only offered a raw MPEG-TS stream, which browsers cannot play. Open it in the Wren TV app, or enable ffmpeg so the server can repackage it as HLS.',
          { retry: true, choose: !!this.channel.chooseFeeds });
        return;
      }
      const started = await this.tryFeed(response.url, mode);
      if (this.closed) return;
      if (started) {
        this.root.querySelector('.wait')?.setAttribute('hidden', '');
        this.video.play().catch(() => {});
        return;
      }
      // The server proved this feed answers, but the browser couldn't decode
      // it: release the session and let the next candidate have a turn.
      this.stopSession();
      this.resetStream();
      if (this.autoRemaining <= 0) {
        this.fail('That stream did not start, and the addon has no other feed available.',
          { retry: true, choose: !!this.channel.chooseFeeds });
        return;
      }
    }
    this.fail('No stream from this addon started. Try again in a moment.',
      { retry: true, choose: !!this.channel.chooseFeeds });
  }

  // tryFeed resolves true once the stream has started, and false when it errors
  // or never loads inside the start window. "Started" means the browser has the
  // playlist or metadata: the server has already proved the feed answers with
  // media, and waiting for a decoded frame would discard a live stream that is
  // merely slow to buffer.
  tryFeed(url, mode) {
    if (!url) return Promise.resolve(false);
    return new Promise((resolve) => {
      const video = this.video;
      let done = false;
      const settle = (ok) => {
        if (done) return;
        done = true;
        clearTimeout(timer);
        video.removeEventListener('playing', onPlaying);
        video.removeEventListener('loadedmetadata', onPlaying);
        video.removeEventListener('error', onError);
        this.settling = null;
        this.markStarted = null;
        resolve(ok);
      };
      const onPlaying = () => settle(true);
      const onError = () => settle(false);
      const timer = setTimeout(() => settle(false), AUTO_START_WAIT);
      this.settling = () => settle(false);
      this.markStarted = () => settle(true);
      video.addEventListener('playing', onPlaying);
      video.addEventListener('loadedmetadata', onPlaying);
      video.addEventListener('error', onError);
      this.attach(url, mode).catch(() => settle(false));
    });
  }

  // resetStream tears down one attempt's player so the next feed starts clean.
  resetStream() {
    this.settling = null;
    this.markStarted = null;
    this.hls?.destroy?.();
    this.hls = null;
    if (!this.video) return;
    this.video.removeAttribute('src');
    this.video._hls = null;
    try {
      this.video.load();
    } catch {}
  }

  setWait(text) {
    const wait = this.root?.querySelector('.wait');
    if (!wait) return;
    wait.removeAttribute('hidden');
    const msg = wait.querySelector('.msg');
    if (msg) msg.textContent = text;
  }

  hideError() {
    const box = this.root?.querySelector('.live-error');
    if (box) box.hidden = true;
  }

  // retryAuto starts the whole attempt again, including feeds that failed: a
  // retry is a new playback attempt, and an addon's links may have changed.
  async retryAuto() {
    if (this.closed) return;
    this.resetStream();
    this.stopSession();
    this.hideError();
    await this.runAuto();
  }

  // fillTracks builds the audio and subtitle menus from what the stream really
  // publishes. A single audio rendition and the remuxed MPEG-TS path (one AAC
  // track) offer nothing, so their controls stay hidden rather than pretending.
  fillTracks() {
    if (this.closed || !this.root) return;
    const audioEl = this.root.querySelector('[data-audio]');
    const subsEl = this.root.querySelector('[data-subs]');
    if (!audioEl || !subsEl) return;
    const src = this.trackSource();
    const audio = audioChoice(src.audio);
    audioEl.innerHTML = audio.options.map((o) => `<option value="${o.index}">${esc(o.label)}</option>`).join('');
    audioEl.hidden = !audio.visible;
    if (audio.visible) {
      audioEl.value = String(Math.max(0, src.current));
      audioEl.onchange = () => this.pickAudio(Number(audioEl.value));
    }
    const subs = subtitleChoice(src.subs);
    subsEl.innerHTML = subs.options.map((o) => `<option value="${o.index}">${esc(o.label)}</option>`).join('');
    subsEl.hidden = !subs.visible;
    if (subs.visible) {
      subsEl.value = String(src.subCurrent);
      subsEl.onchange = () => this.pickSubs(Number(subsEl.value));
    }
  }

  trackSource() {
    if (this.hls) {
      return {
        audio: this.hls.audioTracks || [],
        subs: this.hls.subtitleTracks || [],
        current: this.hls.audioTrack ?? 0,
        subCurrent: this.hls.subtitleTrack ?? -1,
      };
    }
    const tracks = this.video?.audioTracks;
    const audio = tracks ? Array.from(tracks) : [];
    const subs = Array.from(this.video?.textTracks || []).filter(
      (track) => track.kind === 'subtitles' || track.kind === 'captions',
    );
    return {
      audio,
      subs,
      current: audio.findIndex((track) => track.enabled),
      subCurrent: subs.findIndex((track) => track.mode === 'showing'),
    };
  }

  pickAudio(index) {
    if (this.hls) {
      this.hls.audioTrack = index;
      return;
    }
    Array.from(this.video?.audioTracks || []).forEach((track, i) => { track.enabled = i === index; });
  }

  pickSubs(index) {
    if (this.hls) {
      this.hls.subtitleTrack = index;
      this.hls.subtitleDisplay = index >= 0;
      return;
    }
    const subs = Array.from(this.video?.textTracks || []).filter(
      (track) => track.kind === 'subtitles' || track.kind === 'captions',
    );
    subs.forEach((track, i) => { track.mode = i === index ? 'showing' : 'disabled'; });
  }

  fail(message, { retry = false, choose = false } = {}) {
    if (this.closed) return;
    this.root.querySelector('.wait')?.setAttribute('hidden', '');
    this.root.querySelector('[data-live-pill]')?.replaceChildren('OFFLINE');
    const box = this.root.querySelector('.live-error');
    box.hidden = false;
    box.querySelector('.msg').textContent = message;
    const again = box.querySelector('[data-retry]');
    if (again) again.hidden = !retry;
    const pick = box.querySelector('[data-choose]');
    if (pick) pick.hidden = !choose;
    this.stopSession();
  }

  stopSession() {
    if (!this.sid) return;
    const sid = this.sid;
    this.sid = null;
    api(`/api/sessions/${encodeURIComponent(sid)}`, { method: 'DELETE' }).catch(() => {});
  }

  close() {
    if (this.closed) return;
    this.closed = true;
    this.hls?.destroy?.();
    this.video?.pause?.();
    if (this.video) this.video.removeAttribute('src');
    this.stopSession();
    document.removeEventListener('keydown', this.onKey);
    document.body.style.overflow = '';
    this.root?.remove();
    if (current === this) current = null;
  }
}
