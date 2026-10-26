// Race detail: one racing weekend from the event contract, its ordered
// sessions, the enriched classification, verified circuit geometry, F1 archive
// timing and MotoGP championship context. Nothing is inferred from order.
import { api } from './api.js';
import { esc, el, $, toast } from './ui.js';
import { leagueName, watchActionLabel, detailRefreshMs } from './sports-model.js';
import {
  resolveRaceSelection, defaultRaceSession, raceSessionList, sessionLabel,
  raceDate, raceClassification, driverSubtitle, circuitPath,
  archiveAvailable, archiveDrivers, archiveMessages, archiveWeather,
} from './sports-race-model.js';
import { mountSportsWatch } from './sports-watch.js';

const logoURL = (url) => (url ? `/api/sports/logo?url=${encodeURIComponent(url)}` : '');

export async function viewRace(league, eventId, sessionId) {
  const root = el('<section class="sports event-page race-page"></section>');
  let board;
  try {
    board = await api('/api/sports/races');
  } catch (err) {
    root.innerHTML = `<div class="empty"><h2>Races are unavailable</h2><p>${esc(err.message)}</p>
      <a class="btn primary" href="/sports">Back to Sports</a></div>`;
    return root;
  }
  const selection = resolveRaceSelection(board.events || [], sessionId || eventId);
  if (!selection) {
    root.innerHTML = `<div class="empty"><h2>Race not found</h2>
      <p>This race is not in the provider's current window.</p>
      <a class="btn primary" href="/sports">Back to Sports</a></div>`;
    return root;
  }
  const weekend = selection.event;
  let session = selection.session;
  if (!session) session = defaultRaceSession(weekend);
  const sessionBase = session
    ? `/api/sports/races/${encodeURIComponent(weekend.league)}/${encodeURIComponent(weekend.providerEventId || weekend.id)}/${encodeURIComponent(session.sessionId || '')}`
    : '';
  let detail = session;
  if (sessionBase) {
    try {
      detail = await api(sessionBase);
    } catch {
      // The board session is still a valid, honest fallback.
      detail = { ...session, stale: true };
    }
  }
  const leagues = await loadCatalog();
  // A session can start or finish while the page stays open. Only the action
  // wording depends on that lifecycle, so the page is not rebuilt under an open
  // watch panel.
  let statusTimer = null;
  const syncWatchButton = () => {
    const button = root.querySelector('#race-watch');
    if (button) button.textContent = watchActionLabel(detail);
  };
  const scheduleStatusRefresh = () => {
    clearTimeout(statusTimer);
    statusTimer = setTimeout(async () => {
      if (!root.isConnected) return;
      if (!document.hidden && sessionBase) {
        try {
          const next = await api(sessionBase);
          if (!root.isConnected) return;
          if (next?.id === detail?.id && next.status !== detail.status) {
            detail = { ...detail, ...next };
            syncWatchButton();
          }
        } catch {
          // Keep the last verified status; the next tick tries again.
        }
      }
      scheduleStatusRefresh();
    }, detailRefreshMs(detail));
  };
  paint();
  scheduleStatusRefresh();

  function paint() {
    const track = weekend.circuit ? circuitPath(weekend.circuit, 100, 6) : null;
    root.innerHTML = `
      <a class="btn ghost small" href="/sports">← Sports</a>
      <div class="event-head">
        <div class="small muted">${esc(leagueName(weekend.league, leagues) || weekend.league)} · ${esc(weekend.statusDetail || '')}</div>
        <h1>${esc(weekend.title)}</h1>
        <div class="small muted">${esc(weekend.venueName || '')}${weekend.broadcasts?.length ? ` · ${esc(weekend.broadcasts.join(' · '))}` : ''}</div>
      </div>
      ${weekend.stale ? '<p class="small stale">The provider is delayed; the last verified race board is shown.</p>' : ''}
      <div class="race-layout">
        <div class="race-main">
          <div class="session-chips" role="tablist" aria-label="Sessions">
            ${raceSessionList(weekend).map((item) => `<a class="chip${item.id === session?.id ? ' active' : ''}" role="tab" aria-selected="${item.id === session?.id}" href="/sports/race/${encodeURIComponent(weekend.league)}/${encodeURIComponent(weekend.id)}/${encodeURIComponent(item.id)}">
              ${esc(item.sessionType || 'Session')}<span class="small muted">${esc(raceDate(item.startTime))}</span></a>`).join('')}
          </div>
          ${raceSummary(detail, weekend, leagues)}
          ${watchPanel(detail, weekend)}
          <div id="race-archive"></div>
          ${weekend.league === 'motogp' ? '<div id="motogp-extra"></div>' : ''}
        </div>
        <aside class="race-side">
          ${track ? `<section class="event-section circuit"><h2>Circuit</h2>
            <svg viewBox="0 0 ${track.width} ${track.height}" role="img" aria-label="${esc(weekend.circuit.name)} reference layout">
              <path class="circuit-line" d="${track.path}"/>
            </svg>
            <div class="small muted">${esc(weekend.circuit.name)}${weekend.circuit.location ? ` · ${esc(weekend.circuit.location)}` : ''}${weekend.circuit.referenceLengthMeters ? ` · ${(weekend.circuit.referenceLengthMeters / 1000).toFixed(3)} km reference` : ''}</div>
            <div class="small muted">${esc(weekend.circuit.attribution)}</div>
            <a class="btn small" href="${esc(weekend.circuit.sourceUrl)}" rel="noopener noreferrer" target="_blank">Reference source</a>
          </section>` : ''}
        </aside>
      </div>`;

    root.querySelector('#race-watch')?.addEventListener('click', () => {
      const slot = root.querySelector('#race-watch-slot');
      if (!slot || !detail) return;
      mountSportsWatch(slot, {
        id: detail.id,
        title: weekend.title,
        participants: detail.participants || [],
        league: weekend.league,
        sport: 'racing',
        eventKind: 'race-session',
        eventContext: detail.sessionType,
        status: detail.status,
        startTime: detail.startTime,
        streamKind: 'racing',
        parentId: weekend.id,
        searchText: `${weekend.title} ${detail.sessionType || ''}`.trim(),
      });
    });
    bindArchive(weekend, detail);
    if (weekend.league === 'motogp') {
      loadMotoGP(root.querySelector('#motogp-extra'), weekend);
    }
  }

  function bindArchive(weekend, detail) {
    const slot = root.querySelector('#race-archive');
    if (!slot || !archiveAvailable({ ...detail, league: weekend.league })) return;
    slot.innerHTML = '<section class="event-section"><h2>Archive timing</h2><p class="small muted">Archived timing is only read on request. It is never presented as a live feed.</p><button class="btn" id="race-archive-load">Load archive timing</button><div id="race-archive-body"></div></section>';
    slot.querySelector('#race-archive-load').onclick = async (event) => {
      event.currentTarget.disabled = true;
      try {
        const archive = await api(`${sessionBase}/archive`);
        root.querySelector('#race-archive-body').innerHTML = archivePanel(archive);
      } catch (err) {
        toast(err.message || 'Archive timing unavailable');
      } finally {
        event.currentTarget.disabled = false;
      }
    };
  }

  return root;
}

async function loadCatalog() {
  try {
    const catalog = await api('/api/sports/leagues');
    return catalog.leagues || [];
  } catch {
    return [];
  }
}

function raceSummary(session, weekend, leagues) {
  if (!session) {
    return '<section class="event-section"><h2>Schedule</h2><p class="muted">No session has been published for this weekend.</p></section>';
  }
  const rows = raceClassification(session.participants);
  const stat = (driver, name) => (driver.statistics || []).find((item) => item.name === name)?.value || '';
  const hasPositions = rows.some((driver) => driver.position);
  const showLaps = rows.some((driver) => stat(driver, 'lapsCompleted'));
  const showBest = rows.some((driver) => stat(driver, 'fastestLap'));
  const showPoints = rows.some((driver) => stat(driver, 'points'));
  return `<section class="event-section"><h2>${esc(session.sessionType || 'Session')} <span class="small muted">${esc(sessionLabel(session))}</span>
      ${session.stale ? '<span class="badge warn">Delayed</span>' : ''}</h2>
    ${rows.length ? `<table class="scores stats-table race-table"><thead><tr>
      ${hasPositions ? '<th>Pos</th>' : ''}<th>Driver</th><th>Team</th>
      ${showLaps ? '<th>Laps</th>' : ''}<th>Gap / time</th>${showBest ? '<th>Best lap</th>' : ''}${showPoints ? '<th>Pts</th>' : ''}
    </tr></thead><tbody>${rows.map((driver) => {
      const photo = logoURL(driver.logoUrl);
      const gap = stat(driver, 'behindTime') || stat(driver, 'behindLaps') || stat(driver, 'totalTime');
      return `<tr class="${driver.winner ? 'highlight' : ''}">
        ${hasPositions ? `<td>${esc(driver.position || '')}</td>` : ''}
        <td><span class="driver"><span class="num">${esc(driver.number || '')}</span>${photo ? `<img loading="lazy" src="${esc(photo)}" alt="" onerror="this.remove()">` : ''}${esc(driver.name)}</span>
          ${driver.abbreviation ? `<span class="small muted">${esc(driver.abbreviation)}</span>` : ''}</td>
        <td>${esc(driver.team || '')}${driver.result ? `<div class="small muted">${esc(driver.result)}</div>` : ''}</td>
        ${showLaps ? `<td>${esc(stat(driver, 'lapsCompleted'))}</td>` : ''}
        <td>${esc(gap)}</td>
        ${showBest ? `<td>${esc(stat(driver, 'fastestLap'))}</td>` : ''}
        ${showPoints ? `<td>${esc(stat(driver, 'points'))}</td>` : ''}
      </tr>`;
    }).join('')}</tbody></table>` : '<p class="muted">The provider has not published a classification for this session.</p>'}
  </section>`;
}

// A session is not a two-team event, so Watch searches the race/session title
// and requires an explicit choice.
function watchPanel(session, weekend) {
  return `<div class="event-actions">
      <button class="btn primary" id="race-watch">${watchActionLabel(session)}</button>
      <a class="btn" href="/live?q=${encodeURIComponent(weekend.title)}">Browse Live TV</a>
    </div>
    <div id="race-watch-slot"></div>`;
}

function archivePanel(archive) {
  if (!archive || archive.state !== 'available') {
    return `<p class="small muted">${esc(archive?.reason || 'Archive timing is unavailable for this session.')}</p>`;
  }
  const drivers = archiveDrivers(archive);
  const messages = archiveMessages(archive);
  const weather = archiveWeather(archive);
  return `<p class="small muted">${esc(archive.sessionName || 'Session')} · archived timing · checked ${esc(raceDate(archive.checkedAt))}</p>
    ${weather ? `<p class="small muted">${esc(weather)}</p>` : ''}
    ${drivers.length ? `<table class="scores stats-table"><thead><tr><th>Pos</th><th>Driver</th><th>Team</th><th>Gap</th><th>Last lap</th><th>Pit</th></tr></thead>
      <tbody>${drivers.map((driver) => `<tr>
        <td>${driver.position ?? ''}</td>
        <td>${esc(driver.name)}${driver.abbreviation ? ` <span class="small muted">${esc(driver.abbreviation)}</span>` : ''}${driver.retired ? ' <span class="badge">Retired</span>' : ''}</td>
        <td>${esc(driver.team)}</td><td>${esc(driver.gap || driver.interval)}</td>
        <td>${esc(driver.lastLap || driver.bestLap)}</td><td>${driver.pitStops ?? ''}</td></tr>`).join('')}</tbody></table>` : '<p class="small muted">The archive has no driver list.</p>'}
    ${messages.length ? `<details class="archive-messages"><summary>Race control <span class="small muted">${messages.length}</span></summary>
      <ul class="plays">${messages.slice(0, 40).map((message) => `<li><span class="badge">${esc(message.category || message.flag || 'Message')}</span>
        <span>${esc(message.message)}</span>${message.lap != null ? ` <span class="small muted">lap ${message.lap}</span>` : ''}</li>`).join('')}</ul></details>` : ''}
    <p class="small muted">Source: <a href="${esc(archive.sourceUrl || '')}" rel="noopener noreferrer" target="_blank">official F1 timing archive</a></p>`;
}

async function loadMotoGP(slot, weekend) {
  if (!slot) return;
  const eventID = weekend.providerEventId || '';
  try {
    const [standings, circuit] = await Promise.all([
      api('/api/sports/motogp/standings'),
      eventID ? api(`/api/sports/motogp/circuit/${encodeURIComponent(eventID)}`) : Promise.resolve(null),
    ]);
    slot.innerHTML = `<section class="event-section"><h2>Championship</h2>
      ${standings.state === 'available' && standings.standings?.length
        ? `<table class="scores stats-table"><thead><tr><th>Pos</th><th>Rider</th><th>Team</th><th>Points</th><th>Wins</th></tr></thead>
          <tbody>${standings.standings.slice(0, 12).map((row) => `<tr>
            <td>${row.position ?? ''}</td><td>${esc(row.name)}${row.number ? ` <span class="small muted">#${row.number}</span>` : ''}</td>
            <td>${esc(row.team)}</td><td>${row.points ?? ''}</td><td>${row.wins ?? ''}</td></tr>`).join('')}</tbody></table>`
        : `<p class="small muted">${esc(standings.reason || 'Championship standings are unavailable.')}</p>`}
      ${circuit?.state === 'available' && circuit.circuit ? `<div class="small muted">${esc(circuit.circuit.name)}${circuit.circuit.lengthMeters ? ` · ${circuit.circuit.lengthMeters} m` : ''}${circuit.circuit.leftCorners != null ? ` · ${circuit.circuit.leftCorners} left` : ''}${circuit.circuit.rightCorners != null ? ` · ${circuit.circuit.rightCorners} right` : ''}</div>
        ${circuit.circuit.mapUrl ? `<a class="btn small" href="${esc(circuit.circuit.mapUrl)}" rel="noopener noreferrer" target="_blank">Official circuit map</a>` : ''}` : ''}
    </section>`;
  } catch {
    slot.innerHTML = '';
  }
}
