// Watch flow for a sports event: server-side matched Live TV candidates, an
// explicit picker, measured quality checks and a docked preview that can
// expand to the normal player. A single strong feed starts directly; a
// "possible" match must always be chosen by the viewer.
import { api } from './api.js';
import { esc } from './ui.js';
import { openLiveChannel } from './live-player.js';
import { nativeHlsSupported } from './live-model.js';
import { AUTO_ROUNDS, autoMessage, planAutoRound } from './addon-playback.js';
import {
  confidenceLabel, qualityLabel, previewAllowed, searchNote,
  feedPriority, watchDecision, probeKey, sortProbedFeeds, probedQualityLines,
  probeLabel, probeProgressText, probeError, preferLastSource, watchQuery,
  providerMode, providerStateLabel, providerEmptyNote, watchCandidates,
  watchTarget, candidateSubtitle,
} from './sports-watch-model.js';

// The device remembers which source last worked for this event, like
// MediaStorm's per-event source memory. It is a hint, never a proof.
const LAST_SOURCE_KEY = 'wren.sports.last-source';

function readLastSource(eventId) {
  try {
    const all = JSON.parse(localStorage.getItem(LAST_SOURCE_KEY) || '{}');
    return typeof all[eventId] === 'string' ? all[eventId] : '';
  } catch {
    return '';
  }
}

function rememberProbe(eventId, key) {
  try {
    const all = JSON.parse(localStorage.getItem(LAST_SOURCE_KEY) || '{}');
    all[eventId] = key;
    localStorage.setItem(LAST_SOURCE_KEY, JSON.stringify(all));
  } catch {
    // Storage may be unavailable; the watch flow still works without memory.
  }
}

export function mountSportsWatch(container, event, options = {}) {
  const state = {
    groups: [], liveTV: true, loading: false, error: '', query: '', preview: null,
    probes: {}, progress: { running: false, completed: 0, total: 0, failed: 0, cancelled: false },
    owner: null, sortByResolution: false, frozen: [], decided: false,
    // Live TV providers are searched by the server; the browser only holds a
    // provider's source id and item id, never a stream URL. A Stremio item and
    // a playlist channel become rows in the same candidate list below.
    providerMode: providerMode(event) || 'live',
    providers: [], providerSources: [], providerStale: false, providerError: '',
    candidates: [],
    loadSeq: 0, stopped: false,
    // Streaming addons configured for the library are an additive list kept
    // for compatibility. They are not Live TV providers, so they never replace
    // or delay the unified provider search.
    addons: [], addonsStale: false, addonProbes: {},
    lastSource: readLastSource(event.id),
    hudTimer: null,
  };
  const panel = document.createElement('section');
  panel.className = 'event-section watch-panel';
  // The preview lives outside the list panel so re-rendering results, sorting
  // or probing never detaches a playing video.
  const previewSlot = document.createElement('div');
  previewSlot.className = 'watch-preview-slot';
  container.append(panel, previewSlot);

  const attach = async (video, res) => {
    const native = nativeHlsSupported(video);
    if (native) {
      video.src = res.url;
    } else if (window.Hls?.isSupported?.()) {
      const hls = new window.Hls({ lowLatencyMode: true, backBufferLength: 60 });
      hls.on(window.Hls.Events.ERROR, (_event, data) => {
        if (data?.fatal) video.dispatchEvent(new Event('error'));
      });
      hls.loadSource(res.url);
      hls.attachMedia(video);
      video._hls = hls;
    } else {
      throw new Error('This browser cannot play HLS. Open the channel in the Wren TV app.');
    }
    await video.play().catch(() => {});
    return res.sessionId || null;
  };

  // How long a previewed feed has to start before the next one is tried. The
  // full player measures a start the same way: a live stream that is merely slow
  // to buffer is not discarded, and one that never loads is replaced.
  const PREVIEW_START_WAIT = 15_000;

  // releaseSession stops a session whose preview is already gone.
  const releaseSession = (sid) => {
    if (!sid) return;
    api(`/api/sessions/${encodeURIComponent(sid)}`, { method: 'DELETE' }).catch(() => {});
  };

  // A viewer can stop the preview before the tune request settles, and a stream
  // can fail while it is still tuning. The server has already created the
  // session in both cases, so the late response releases it instead of leaking a
  // provider connection or being adopted by a preview that is already dead.
  const settleSession = (sid) => {
    if (state.preview && !state.preview.failed) {
      state.preview.sid = sid;
      return true;
    }
    releaseSession(sid);
    return false;
  };

  // resetPreviewStream tears down one feed's player so the next attempt starts
  // clean instead of inheriting a dead hls.js instance.
  const resetPreviewStream = (video) => {
    if (!video) return;
    video._hls?.destroy?.();
    video._hls = null;
    video.pause?.();
    video.removeAttribute('src');
    try {
      video.load();
    } catch {
      // The element may already be gone with the preview.
    }
  };

  // previewFeedStarts resolves true once the browser has the playlist or the
  // metadata, and false when the feed errors or never loads inside the window.
  const previewFeedStarts = (video, url) => new Promise((resolve) => {
    let done = false;
    const settle = (started) => {
      if (done) return;
      done = true;
      clearTimeout(timer);
      video.removeEventListener('playing', onStarted);
      video.removeEventListener('loadedmetadata', onStarted);
      video.removeEventListener('error', onFailed);
      resolve(started);
    };
    const onStarted = () => settle(true);
    const onFailed = () => settle(false);
    const timer = setTimeout(() => settle(false), PREVIEW_START_WAIT);
    video.addEventListener('playing', onStarted);
    video.addEventListener('loadedmetadata', onStarted);
    video.addEventListener('error', onFailed);
    attach(video, { url }).catch(() => settle(false));
  });

  // A channel has exactly one stream: the server opens it and the preview shows
  // it. A Stremio item has no channel at all, so the preview asks the same
  // automatic-playback route Watch uses for the best feed it hasn't tried — the
  // server ranks the item's feeds and proves each one — and honours the bounded
  // fallback the full player does, releasing every session that is replaced.
  const tuneChannel = async (video, target) => {
    const res = await api(`/api/livetv/channels/${encodeURIComponent(target.channelId)}/play`, { method: 'POST', body: { hls: true } });
    return attach(video, res);
  };

  const tuneProvider = async (video, target, setStatus) => {
    const route = `/api/livetv/addons/${encodeURIComponent(target.addonAuto.source)}/item/${encodeURIComponent(target.addonAuto.type)}/${encodeURIComponent(target.addonAuto.id)}/play`;
    let tried = [];
    let remaining = 0;
    for (let round = 0; round < AUTO_ROUNDS; round += 1) {
      if (!state.preview) return null;
      setStatus(autoMessage(round));
      let response = null;
      let error = null;
      try {
        response = await api(route, { method: 'POST', body: { hls: true, skip: tried } });
      } catch (err) {
        error = err;
      }
      // The viewer can stop the preview mid-round. That answer belongs to
      // nobody, so its session is released rather than adopted by the next one.
      if (!state.preview) {
        releaseSession(response?.sessionId);
        return null;
      }
      const plan = planAutoRound({ round, tried, response, error, remaining });
      tried = plan.tried;
      remaining = plan.remaining;
      if (plan.action === 'stop') throw new Error(plan.message);
      if (plan.action === 'retry') continue;
      if (!settleSession(response.sessionId)) return null;
      const started = await previewFeedStarts(video, response.url);
      if (!state.preview) return null;
      if (started) return state.preview.sid || null;
      // The server proved this feed answers, but this browser couldn't start it.
      // Its session goes before the next feed gets a turn, so a preview never
      // holds two provider connections at once.
      releaseSession(state.preview.sid);
      state.preview.sid = null;
      resetPreviewStream(video);
      if (remaining <= 0) throw new Error('That source did not start, and the addon has no other feed available.');
    }
    throw new Error('No stream from this addon started. Try again in a moment.');
  };	  // restoreFocus is false when the preview hands over to the full-screen
  // player: focus belongs in that player, never on the page underneath it.
  const stopPreview = ({ restoreFocus = true } = {}) => {
    const current = state.preview;
    state.preview = null;
    clearInterval(state.hudTimer);
    state.hudTimer = null;
    document.removeEventListener('keydown', onPreviewKey);
    if (!current) return;
    current.video?._hls?.destroy?.();
    current.video?.pause?.();
    if (current.video) current.video.removeAttribute('src');
    if (current.sid) releaseSession(current.sid);
    current.root?.remove();
    // Focus returns to the control that opened the preview. If the page was
    // left meanwhile the node is gone, so nothing is stolen from the new view.
    if (restoreFocus && current.opener?.isConnected) current.opener.focus();
  };

  // A feed that dies mid-preview keeps the panel so the viewer can pick another
  // source; only the session is released, and the status is announced.
  const failPreview = (message) => {
    const current = state.preview;
    if (!current) return;
    // Remember the failure: a tune that settles afterwards must not paint a
    // success line over the alert or keep the session it created.
    current.failed = true;
    current.video?._hls?.destroy?.();
    current.video?.pause?.();
    const note = current.root?.querySelector('.dp-status');
    if (note) {
      note.textContent = message;
      note.setAttribute('role', 'alert');
    }
    if (current.sid) {
      const sid = current.sid;
      current.sid = null;
      releaseSession(sid);
    }
  };

  // The docked preview is the sports HUD: it names the feed and, when the page
  // can supply a delayed snapshot, its score. The text is never fetched here,
  // so switching sources cannot leave an old score on screen.
  const startHud = () => {
    clearInterval(state.hudTimer);
    const line = state.preview?.root?.querySelector('.dp-hud');
    if (!line || typeof options.hudLine !== 'function') return;
    const paint = () => {
      if (!state.preview) return;
      const text = options.hudLine() || '';
      line.textContent = text;
      line.hidden = !text;
    };
    paint();
    state.hudTimer = setInterval(paint, 1000);
  };

  // Escape closes the docked preview the same way Stop does, including the
  // session release and focus restoration.
  const onPreviewKey = (event) => {
    if (event.key === 'Escape' && state.preview) {
      event.preventDefault();
      stopPreview();
    }
  };
  // The full player keeps the event's identity (competition, lifecycle, matchup)
  // and the feed that is playing, exactly as MediaStorm's sports HUD does. Only
  // the factual source label is added here; the page supplies the rest.
  const sportsIdentity = (broadcast) => {
    const base = typeof options.playerIdentity === 'function' ? options.playerIdentity() : null;
    return base ? { ...base, broadcast } : null;
  };
  // previewSpec describes one preview the same way for every kind of source:
  // the name, badge and status it shows, how its session is started and where
  // "Full screen" goes. A playlist channel, a Stremio item and a library
  // streaming addon therefore share one component, one set of labels and one
  // set of interaction rules; only the transport underneath differs. It returns
  // null when the source carries no playable identity at all, so a Preview is
  // never offered for something it could not tune.
  const previewSpec = (input) => {
    if (input?.kind === 'streamAddon') {
      const title = input.title || input.addon;
      const match = { sourceId: 'addon', channelId: input.id };
      return {
        title,
        tier: input.confidenceTier,
        note: `Addon · ${input.addon}`,
        status: `Tuning from ${input.addon}…`,
        playing: `${title} · ${input.addon}`,
        broken: 'This addon source stopped responding. Stop the preview and choose another source.',
        probe: probeKey(match),
        match,
        tune: (video) => api(`/api/sports/addons/${encodeURIComponent(input.id)}/play`, { method: 'POST', body: { hls: true } })
          .then((res) => attach(video, res)),
        expand: () => openLiveChannel({
          addonId: input.id, name: title, group: `Addon · ${input.addon}`, sports: sportsIdentity(input.addon),
        }),
      };
    }
    // A row from the candidate list, or a channel group on the single-strong
    // feed path: the normalization is the same one the Watch action uses, so
    // both controls resolve one identity.
    const candidate = input?.kind ? input : watchCandidates({ groups: input ? [input] : [] })[0];
    const target = watchTarget(candidate);
    if (!target) return null;
    const group = candidate.kind === 'channel' ? candidate.group : null;
    const primary = group?.primary || {};
    const entry = candidate.provider || {};
    return {
      title: candidate.name,
      tier: candidate.tier,
      note: qualityLabel(primary.reportedQuality) || candidateSubtitle(candidate),
      status: `Tuning ${target.name}…`,
      playing: group
        ? [primary.channelName, primary.programTitle].filter(Boolean).join(' · ') || target.name
        : [target.name, entry.sourceName].filter(Boolean).join(' · '),
      broken: 'This feed stopped responding. Stop the preview and choose another source.',
      probe: target.probe,
      match: group ? primary : { sourceId: entry.source, channelId: entry.itemId },
      tune: (video, setStatus) => (target.channelId ? tuneChannel(video, target) : tuneProvider(video, target, setStatus)),
      expand: () => playTarget(target),
    };
  };

  // playTarget opens the full player on the identity Watch resolved: a channel
  // opens its own session, a Stremio item goes through automatic playback, which
  // ranks the feeds, proves one starts and falls back. Preview's "Full screen"
  // is that same handover, so neither button can start a second kind of player.
  const playTarget = (target) => openLiveChannel(target.channelId
    ? { id: target.channelId, name: target.name, group: target.group, sports: sportsIdentity(target.label) }
    : { name: target.name, group: target.group, sports: sportsIdentity(target.label), addonAuto: target.addonAuto });

  const showPreview = async (spec) => {
    if (!spec) return;
    const opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    stopPreview();
    const root = document.createElement('div');
    root.className = 'docked-preview';
    root.setAttribute('role', 'region');
    root.setAttribute('aria-label', `Preview: ${spec.title}`);
    root.innerHTML = `<div class="dp-head"><span class="dp-title">${esc(spec.title)}</span>
      <span class="badge ${esc(spec.tier)}">${esc(confidenceLabel(spec.tier))}</span>
      <span class="small muted">${esc(spec.note)}</span></div>
      <div class="dp-hud small muted" role="status" hidden></div>
      <video playsinline autoplay muted controls></video>
      <div class="dp-status small muted">${esc(spec.status)}</div>
      <div class="dp-actions">
        <button class="btn primary" data-expand>Full screen</button>
        <button class="btn" data-stop>Stop</button>
      </div>`;
    previewSlot.append(root);
    const video = root.querySelector('video');
    const setStatus = (text) => {
      const note = root.querySelector('.dp-status');
      if (note) note.textContent = text;
    };
    state.preview = { root, video, sid: null, opener, failed: false, tuning: true };
    document.addEventListener('keydown', onPreviewKey);
    // A feed error belongs to the preview once a feed is playing. While an
    // attempt is in flight it is that attempt's failure, which replaces the feed
    // instead of ending the preview.
    video.addEventListener('error', () => { if (!state.preview?.tuning) failPreview(spec.broken); });
    // Move focus into the preview without leaving the page: Stop is the safe
    // first target for remote/keyboard users.
    root.querySelector('[data-stop]')?.focus();
    root.querySelector('[data-stop]').onclick = () => stopPreview();
    // Full screen reuses the ordinary Live TV player; it closes this preview
    // session first, so only one provider connection is held and focus follows
    // into the player instead of returning to the page behind it.
    root.querySelector('[data-expand]').onclick = () => {
      stopPreview({ restoreFocus: false });
      spec.expand();
    };
    startHud();
    try {
      const sid = await spec.tune(video, setStatus);
      if (!settleSession(sid)) return;
      if (state.preview) state.preview.tuning = false;
      state.lastSource = spec.probe;
      rememberProbe(event.id, spec.probe);
      // A different source has a different latency: the page restarts its
      // delayed history so two streams' timelines are never mixed.
      if (typeof options.onSourceChange === 'function') options.onSourceChange(spec.match);
      setStatus(spec.playing);
    } catch (err) {
      // A viewer who already stopped the preview is not shown an error for the
      // request they cancelled.
      if (!state.preview) return;
      // A failed tune still releases the session and returns focus to the
      // launcher, then reports the error where a screen reader will read it.
      const failedOpener = state.preview?.opener || opener;
      stopPreview();
      previewSlot.replaceChildren();
      const box = document.createElement('p');
      box.className = 'watch-error small';
      box.id = 'watch-error';
      box.tabIndex = -1;
      box.textContent = err.message || 'This feed could not be played.';
      previewSlot.append(box);
      box.focus();
      if (failedOpener?.isConnected) failedOpener.setAttribute('aria-describedby', 'watch-error');
    }
  };

  const probeAddon = async (candidate) => {
    state.addonProbes = { ...state.addonProbes, [candidate.id]: { pending: true } };
    render();
    try {
      const quality = await api(`/api/sports/addons/${encodeURIComponent(candidate.id)}/quality`, {
        method: 'POST', body: { addonId: candidate.id },
      });
      const result = quality.height || (quality.audio || []).length
        ? { quality }
        : { error: 'Quality not detected · playback may still work' };
      state.addonProbes = { ...state.addonProbes, [candidate.id]: result };
    } catch (err) {
      state.addonProbes = { ...state.addonProbes, [candidate.id]: { error: probeError(err) } };
    }
    render();
  };

  // Live-labelled feeds first, unknown next, scheduled last. Sorting applies to
  // the visible list and never drops a feed.
  const orderedGroups = () => {
    if (state.progress.running) return state.frozen;
    const base = [...state.groups].sort((a, b) => feedPriority(a) - feedPriority(b));
    if (state.sortByResolution) return sortProbedFeeds(base, state.probes);
    return preferLastSource(base, state.lastSource);
  };

  const groupQuality = (group) => probedQualityLines(state.probes[probeKey(group.primary)]);

  const cancelProbe = () => {
    state.owner?.abort();
    state.owner = null;
    state.progress = { ...state.progress, running: false, cancelled: state.progress.running || state.progress.cancelled };
    state.sortByResolution = true;
  };

  const startProbe = async (recheck = false) => {
    if (state.owner) return;
    const candidates = [...new Map(state.groups.map((group) => [probeKey(group.primary), group.primary])).values()];
    const queue = candidates.filter((match) => recheck || !state.probes[probeKey(match)]);
    if (!queue.length) return;
    state.frozen = orderedGroups();
    const owner = new AbortController();
    state.owner = owner;
    state.progress = {
      running: true,
      completed: candidates.length - queue.length,
      total: candidates.length,
      failed: recheck ? 0 : candidates.filter((match) => state.probes[probeKey(match)]?.error).length,
      cancelled: false,
    };
    render();
    for (const match of queue) {
      if (owner.signal.aborted) break;
      let result;
      try {
        const quality = await api('/api/sports/streams/quality', {
          method: 'POST', body: { channelId: match.channelId }, signal: owner.signal,
        });
        result = quality.height || (quality.audio || []).length
          ? { quality }
          : { error: 'Quality not detected · playback may still work' };
      } catch (err) {
        if (owner.signal.aborted) break;
        result = { error: probeError(err) };
      }
      // A cancelled check keeps every result already measured.
      if (owner.signal.aborted || state.owner !== owner) break;
      state.probes = { ...state.probes, [probeKey(match)]: result };
      state.progress = {
        ...state.progress,
        completed: state.progress.completed + 1,
        failed: state.progress.failed + (result.error ? 1 : 0),
      };
      render();
    }
    if (state.owner === owner) {
      state.owner = null;
      state.progress = { ...state.progress, running: false };
      state.sortByResolution = true;
      render();
    }
  };

  // A single list of playable sources for this event. A playlist channel and a
  // Stremio item are rows in the same list with the same Watch action; only
  // the row details come from the provider that supplied them.
  const candidateRow = (candidate, index) => {
    const channel = candidate.kind === 'channel' ? candidate.group : null;
    const lines = channel ? groupQuality(channel) : [];
    const quality = channel ? qualityLabel(channel.primary.reportedQuality) : '';
    // The same capability question for both kinds: a channel and a Stremio item
    // expose the same Preview and Watch controls whenever they can be played.
    const preview = previewAllowed(candidate);
    const logo = channel?.primary?.channelLogo
      ? `<img class="stream-logo" loading="lazy" src="/api/sports/logo?url=${encodeURIComponent(channel.primary.channelLogo)}" alt="" onerror="this.remove()">`
      : '<span class="stream-logo"></span>';
    return `<div class="stream-row ${esc(candidate.tier)}">
      ${logo}
      <div class="stream-main"><div class="stream-name">${esc(candidate.name)}</div>
        <div class="small muted">${esc(candidateSubtitle(candidate))}</div>
        ${lines.length
          ? `<div class="probe-lines small">${lines.map((line) => `<span>${esc(line)}</span>`).join('')}</div>`
          : quality ? `<div class="small muted">${esc(quality)}</div>` : ''}</div>
      <span class="badge ${esc(candidate.tier)}">${esc(confidenceLabel(candidate.tier))}</span>
      ${candidate.key === state.lastSource ? '<span class="badge">Last used</span>' : ''}
      ${quality ? `<span class="small muted quality">${esc(quality)}</span>` : ''}
      <div class="stream-actions">
        ${preview ? `<button class="btn" data-preview="${index}">Preview</button>` : ''}
        <button class="btn ${preview ? '' : 'primary'}" data-watch="${index}">Watch</button>
      </div>
    </div>`;
  };

  const candidateList = () => (state.candidates.length
    ? `<div class="stream-list">${state.candidates.map(candidateRow).join('')}</div>`
    : '');

  // providerNote says what each configured provider answered, so a provider
  // that found nothing is named instead of silently dropped. It is a status
  // line, not a second list: every playable source is already a row above.
  const providerNote = () => {
    if (state.providerError) return `<p class="watch-error small">${esc(state.providerError)}</p>`;
    const replay = state.providerMode === 'replay';
    if (!state.providerSources.length) {
      return replay
        ? '<p class="muted small">No Stremio provider is configured in Live TV. Add one in Manage → Settings to search it here.</p>'
        : '';
    }
    const rows = state.providerSources
      .filter((source) => source.state !== 'complete')
      .map((source) => `<div class="small muted">${esc(source.name)}: ${esc(providerStateLabel(source.state))}${source.reason ? ` — ${esc(source.reason)}` : ''}</div>`)
      .join('');
    const hasProviderRow = state.candidates.some((candidate) => candidate.kind === 'provider');
    const empty = hasProviderRow ? '' : `<p class="muted small">${esc(providerEmptyNote(replay, state.providerSources))}</p>`;
    const stale = state.providerStale
      ? '<p class="small muted">A provider could not be reached; the providers that answered are listed.</p>'
      : '';
    return `${empty}${rows ? `<div class="provider-states">${rows}</div>` : ''}${stale}`;
  };

  // The one Watch action. A channel and an addon item differ only in how Live
  // TV starts them, never in the control the viewer presses: a channel opens
  // its own session, a provider item goes through automatic playback, which
  // ranks the feeds, proves one starts and falls back.
  const watchCandidate = (candidate) => {
    const target = watchTarget(candidate);
    if (!target) return;
    stopPreview({ restoreFocus: false });
    state.lastSource = target.probe;
    rememberProbe(event.id, target.probe);
    playTarget(target);
  };

  const bindCandidates = () => {
    panel.querySelectorAll('[data-watch]').forEach((button) => {
      button.onclick = () => watchCandidate(state.candidates[Number(button.dataset.watch)]);
    });
    panel.querySelectorAll('[data-preview]').forEach((button) => {
      const candidate = state.candidates[Number(button.dataset.preview)];
      button.onclick = () => showPreview(previewSpec(candidate));
    });
  };

  // Sorting by measured quality only reorders the sources that were measured:
  // a provider item that was never probed keeps its place after them.
  const orderByResolution = (candidates) => {
    const channels = candidates.filter((candidate) => candidate.kind === 'channel');
    const byGroup = new Map(channels.map((candidate) => [candidate.group, candidate]));
    return [
      ...sortProbedFeeds(channels.map((candidate) => candidate.group), state.probes)
        .map((group) => byGroup.get(group)).filter(Boolean),
      ...candidates.filter((candidate) => candidate.kind === 'provider'),
    ];
  };

  // Streaming addons (the library's release addons) are listed separately when
  // they matched this event. They keep their explicit preview and measured
  // quality flow; nothing here replaces the automatic Live TV providers.
  const legacyAddonMarkup = () => {
    if (!state.addons.length) return '';
    return `<h3 class="small">Streaming addons <span class="small muted">${state.addons.length}</span></h3>
      <p class="small muted">${state.addonsStale ? 'Addon discovery is stale. ' : ''}Also matched from the addons configured for streaming. These never play automatically.</p>
      <div class="stream-list">${state.addons.map((candidate) => {
        const probe = state.addonProbes[candidate.id] || {};
        const lines = probe.quality ? probedQualityLines(probe) : [];
        const match = { sourceId: 'addon', channelId: candidate.id };
        return `<div class="stream-row ${esc(candidate.confidenceTier)}">
          <span class="stream-logo"></span>
          <div class="stream-main"><div class="stream-name">${esc(candidate.title || candidate.addon)}</div>
            <div class="small muted">${esc(candidate.addon)}${candidate.matchReason ? ` · ${esc(candidate.matchReason)}` : ''}</div>
            ${lines.length ? `<div class="probe-lines small">${lines.map((line) => `<span>${esc(line)}</span>`).join('')}</div>`
              : probe.error ? `<div class="small muted">${esc(probe.error)}</div>` : ''}</div>
          <span class="badge ${esc(candidate.confidenceTier)}">${esc(confidenceLabel(candidate.confidenceTier))}</span>
          ${probeKey(match) === state.lastSource ? '<span class="badge">Last used</span>' : ''}
          <div class="stream-actions">
            <button class="btn small" data-addon-probe="${esc(candidate.id)}" ${probe.pending ? 'disabled' : ''}>${probe.pending ? 'Checking…' : 'Check quality'}</button>
            <button class="btn" data-addon-preview="${esc(candidate.id)}">Preview</button>
          </div>
        </div>`;
      }).join('')}</div>`;
  };

  const legacyAddonBindings = () => {
    panel.querySelectorAll('[data-addon-probe]').forEach((button) => {
      button.onclick = () => {
        const candidate = state.addons.find((item) => item.id === button.dataset.addonProbe);
        if (candidate) probeAddon(candidate);
      };
    });
    panel.querySelectorAll('[data-addon-preview]').forEach((button) => {
      button.onclick = () => {
        const candidate = state.addons.find((item) => item.id === button.dataset.addonPreview);
        // A streaming-addon candidate is explicit: it is probed and previewed
        // from its opaque server identity, never auto-played, and it reuses the
        // same preview component as every other row.
        if (candidate) showPreview(previewSpec({ kind: 'streamAddon', ...candidate }));
      };
    });
  };

  const render = () => {
    state.candidates = watchCandidates({
      groups: state.groups,
      providers: state.providers,
      mode: state.providerMode,
      lastSource: state.lastSource,
    });
    if (state.sortByResolution) state.candidates = orderByResolution(state.candidates);
    // A failed channel search only replaces the panel when there is nothing
    // else to show; a provider that answered is still listed beside the error.
    if (state.error && !state.candidates.length) {
      panel.innerHTML = `<h2>Watch</h2><p class="watch-error">${esc(state.error)}</p>`;
      return;
    }
    // A finished event has nothing to tune live. The same panel searches every
    // eligible provider for a replay and says so plainly when none is found.
    if (state.providerMode === 'replay') {
      panel.innerHTML = `<h2>Search for Replay</h2>
        <p class="small muted">A finished event has no live feed. Wren TV searches the Live TV providers you have configured for a replay or VOD, and lists one only when the provider really offers a playable feed.</p>
        ${state.loading && !state.candidates.length && !state.providerError
          ? '<p class="muted" role="status">Searching your providers for a replay…</p>'
          : `${candidateList()}${providerNote()}`}
        <div class="event-actions">
          <button class="btn primary" data-reload ${state.loading ? 'disabled' : ''}>${state.loading ? 'Searching…' : 'Search again'}</button>
        </div>`;
      bindCandidates();
      panel.querySelector('[data-reload]')?.addEventListener('click', () => load());
      return;
    }
    if (!state.liveTV) {
      panel.innerHTML = `<h2>Watch</h2><p class="muted">Live TV is not configured on this server, so there is nothing to tune.</p>`;
      return;
    }
    const searchBox = `<div class="watch-search"><input type="search" placeholder="Search every channel" value="${esc(state.query)}" aria-label="Search channels">
      <button class="btn" data-search>Search</button></div>
      ${state.query ? `<p class="small muted">${esc(searchNote(state.query))}</p>` : ''}`;
    // The search is asynchronous, so an empty list before it settles is "still
    // looking", not "nothing matched". Claiming no match while the request is
    // in flight is a lie the viewer cannot tell from a real one.
    if (state.loading && !state.candidates.length) {
      panel.innerHTML = `<h2>Watch</h2><p class="muted" role="status">Matching feeds…</p>`;
      return;
    }
    const progress = state.progress;
    const checked = Object.keys(state.probes).length;
    const feedCount = new Set(state.groups.map((g) => probeKey(g.primary))).size;
    const qualityControls = feedCount
      ? `<div class="watch-quality">
          <button class="btn small" data-probe>${esc(probeLabel(progress, state.probes, feedCount))}</button>
          ${checked ? `<button class="btn small" data-sort>${state.sortByResolution ? 'Sort: highest resolution' : 'Sort: recommended'}</button>` : ''}
          <span class="small muted" aria-live="polite">${esc(probeProgressText(progress, state.probes))}</span>
        </div>
        ${progress.running ? `<div class="probe-bar" role="progressbar" aria-valuemin="0" aria-valuemax="${progress.total}" aria-valuenow="${progress.completed}"><i style="width:${Math.round((100 * progress.completed) / Math.max(1, progress.total))}%"></i></div>` : ''}`
      : '';
    const heading = `<h2>Watch${state.candidates.length ? ` <span class="small muted">${state.candidates.length} source${state.candidates.length === 1 ? '' : 's'}</span>` : ''}</h2>`;
    if (!state.candidates.length) {
      panel.innerHTML = `${heading}
        <p class="muted">No Live TV source is a plausible match for this event. Matching is heuristic; an unmatched event is not an error.</p>
        ${searchBox}
        ${state.error ? `<p class="watch-error small">${esc(state.error)}</p>` : ''}
        <div class="event-actions"><button class="btn primary" data-reload>Check again</button></div>
        ${providerNote()}${legacyAddonMarkup()}`;
    } else {
      panel.innerHTML = `${heading}
        ${searchBox}
        ${qualityControls}
        ${candidateList()}
        <p class="small muted">Feeds are matched from channel names and the guide. “Possible” matches may be the wrong event — check before watching. Match score reflects event evidence, not playback quality.</p>
        ${state.error ? `<p class="watch-error small">${esc(state.error)}</p>` : ''}
        ${providerNote()}${legacyAddonMarkup()}`;
    }
    bindCandidates();
    legacyAddonBindings();
    panel.querySelector('[data-search]').onclick = () => load(panel.querySelector('input').value.trim());
    panel.querySelector('input').onkeydown = (e) => { if (e.key === 'Enter') load(e.target.value.trim()); };
    panel.querySelector('[data-reload]')?.addEventListener('click', () => load());
    panel.querySelector('[data-probe]')?.addEventListener('click', () => {
      if (state.progress.running) return cancelProbe();
      const unique = new Set(state.groups.map((g) => probeKey(g.primary))).size;
      return startProbe(Object.keys(state.probes).length >= unique);
    });
    panel.querySelector('[data-sort]')?.addEventListener('click', () => { state.sortByResolution = !state.sortByResolution; render(); });
  };

  const load = async (query = '') => {
    if (state.loading) return;
    cancelProbe();
    stopPreview();
    state.loading = true;
    state.error = '';
    state.providerError = '';
    state.query = query;
    state.probes = {};
    state.progress = { running: false, completed: 0, total: 0, failed: 0, cancelled: false };
    state.sortByResolution = false;
    // A race session and a cycling stage are resolved from the weekly board, so
    // their kind and weekend travel with every watch request.
    const scope = watchQuery(event, query);
    const providerScope = watchQuery(event);
    const replay = providerMode(event) === 'replay';
    state.providerMode = replay ? 'replay' : 'live';
    const providerURL = `/api/sports/events/${encodeURIComponent(event.id)}/providers${providerScope}${providerScope ? '&' : '?'}mode=${state.providerMode}`;
    panel.innerHTML = '<div class="center" style="min-height:80px"><div class="spinner"></div></div>';
    // The two searches are independent: a provider failure never hides the
    // Live TV channels, and a Live TV failure never hides a match.
    const [streamsResult, providersResult] = await Promise.all([
      replay
        ? Promise.resolve({ data: { groups: [], liveTV: true } })
        : api(`/api/sports/events/${encodeURIComponent(event.id)}/streams${scope}`)
          .then((data) => ({ data })).catch((error) => ({ error })),
      api(providerURL).then((data) => ({ data })).catch((error) => ({ error })),
    ]);
    if (streamsResult.error && !replay) {
      // The channel search failed: keep whatever the providers answered and
      // report the failure beside it instead of erasing the list.
      state.error = streamsResult.error.message || 'Could not search Live TV.';
      state.groups = [];
    } else {
      const res = streamsResult.data || {};
      state.groups = Array.isArray(res.groups) ? res.groups : [];
      state.liveTV = res.liveTV !== false;
    }
    if (providersResult.error) {
      state.providers = [];
      state.providerSources = [];
      state.providerStale = false;
      state.providerError = providersResult.error.message
        || (replay ? 'Could not search your providers for a replay.' : 'Could not search your Live TV providers.');
    } else {
      const feed = providersResult.data || {};
      state.providers = Array.isArray(feed.matches) ? feed.matches : [];
      state.providerSources = Array.isArray(feed.sources) ? feed.sources : [];
      state.providerStale = !!feed.stale;
    }
    state.loading = false;
    // The library's streaming addons are additive and may be slow or offline.
    // They load beside the panel, never in front of the Live TV providers this
    // panel searches for the event.
    state.addons = [];
    state.addonsStale = false;
    state.addonProbes = {};
    const loadId = ++state.loadSeq;
    if (!replay) {
      api(`/api/sports/events/${encodeURIComponent(event.id)}/addons${watchQuery(event)}`)
        .then((data) => {
          if (state.stopped || state.loadSeq !== loadId) return;
          state.addons = Array.isArray(data?.candidates) ? data.candidates : [];
          state.addonsStale = !!data?.stale;
          state.addonProbes = {};
          render();
        })
        .catch(() => {});
    }
    render();
    // MediaStorm plays a lone strong feed directly instead of asking a
    // question with one answer. The first load of the Watch flow decides;
    // later searches and reloads stay explicit. A replay is always an
    // explicit choice.
    if (!query && !state.decided && !state.error && !replay) {
      state.decided = true;
      const decision = watchDecision(state.groups);
      if (decision.kind === 'direct') showPreview(previewSpec(decision.group));
    }
  };

  // A hidden page releases the probe connection; playback keeps running, as it
  // does in MediaStorm's docked preview.
  const onVisibility = () => {
    if (document.hidden) cancelProbe();
  };
  document.addEventListener('visibilitychange', onVisibility);

  load();
  return {
    stop: () => {
      state.stopped = true;
      state.loadSeq += 1;
      cancelProbe();
      stopPreview();
      document.removeEventListener('visibilitychange', onVisibility);
    },
    reload: load,
  };
}
