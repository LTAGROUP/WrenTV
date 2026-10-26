// Cycling detail: organizer races, ordered stages, published classifications,
// team time trials and verified route guides. A schedule-only race never shows
// invented results.
import { api } from './api.js';
import { esc, el, toast } from './ui.js';
import {
  cyclingDate, defaultCyclingStage, compareCyclingCalendar, cyclingStageAddress,
  cyclingFetchable, cyclingStageFinished, cyclingStageName, cyclingResultRows,
  cyclingModuleState, cyclingCalendarPeriod,
} from './sports-cycling-model.js';
import { detailRefreshMs, watchActionLabel } from './sports-model.js';
import { mountSportsWatch } from './sports-watch.js';

const logoURL = (url) => (url ? `/api/sports/logo?url=${encodeURIComponent(url)}` : '');

export async function viewCycling(raceId) {
  const root = el('<section class="sports event-page cycling-page"></section>');
  let feed;
  try {
    feed = await api('/api/sports/cycling');
  } catch (err) {
    root.innerHTML = `<div class="empty"><h2>Cycling is unavailable</h2><p>${esc(err.message)}</p>
      <a class="btn primary" href="/sports">Back to Sports</a></div>`;
    return root;
  }
  const race = (feed.data || []).find((item) => item.id === raceId);
  if (!race) {
    root.innerHTML = `<div class="empty"><h2>Race not found</h2>
      <p>This race is not in the organizer's current calendar.</p>
      <a class="btn primary" href="/sports">Back to Sports</a></div>`;
    return root;
  }
  const params = new URLSearchParams(location.search);
  const wanted = Number(params.get('stage') || '');
  let stage = Number.isInteger(wanted) && wanted > 0
    ? (race.stages || []).find((item) => Number(String(item.id).split(':').pop()) === wanted) || defaultCyclingStage(race)
    : defaultCyclingStage(race);
  let detail = stage;
  await loadStage();
  const stageFinished = () => cyclingStageFinished(detail || stage);
  const watchLabel = () => watchActionLabel({ status: stageFinished() ? 'final' : 'scheduled' });
  // A stage can start or finish while the page stays open. Only the action
  // wording depends on that lifecycle, so the page is not rebuilt under an open
  // watch panel.
  let statusTimer = null;
  const syncWatchButton = () => {
    const button = root.querySelector('#cycling-watch');
    if (button) button.textContent = watchLabel();
  };
  const scheduleStatusRefresh = () => {
    clearTimeout(statusTimer);
    statusTimer = setTimeout(async () => {
      if (!root.isConnected) return;
      if (!document.hidden) {
        await loadStage();
        if (!root.isConnected) return;
        syncWatchButton();
      }
      scheduleStatusRefresh();
    }, detailRefreshMs({ status: stageFinished() ? 'final' : 'scheduled' }));
  };
  paint();
  scheduleStatusRefresh();

  async function loadStage() {
    if (!stage) return;
    const address = cyclingStageAddress(race, stage);
    if (!address || !cyclingFetchable(race)) return;
    // A published schedule has no organizer results endpoint; the board stage is
    // already the complete, honest answer.
    if (race.scheduleOnly) return;
    try {
      detail = await api(address);
    } catch {
      detail = { ...stage, results: { state: 'unavailable', reason: 'The organizer results feed could not be reached.' } };
    }
  }

  function paint() {
    root.innerHTML = `
      <a class="btn ghost small" href="/sports">← Sports</a>
      <div class="event-head">
        <div class="small muted">Cycling · ${esc(race.category || 'unknown')} · ${esc(race.raceKind || '')}</div>
        <h1>${esc(race.name)}</h1>
        <div class="small muted">${esc(cyclingDate(race.startDate))} – ${esc(cyclingDate(race.endDate))}${race.classification ? ` · ${esc(race.classification)}` : ''}</div>
        ${race.source?.provider ? `<div class="small muted">${esc(race.source.provider)}${race.source.updatedAt ? ` · updated ${esc(cyclingDate(race.source.updatedAt))}` : ''}</div>` : ''}
      </div>
      ${race.scheduleOnly && !race.calendarOnly ? '<p class="small muted">Schedule coverage only. The organizer does not publish machine-readable results for this race.</p>' : ''}
      ${race.calendarOnly ? '<p class="small muted">Calendar coverage only. Live timing and results are not supplied for this race.</p>' : ''}
      <div class="session-chips" role="tablist" aria-label="Stages">
        ${(race.stages || []).map((item) => `<a class="chip${item.id === stage?.id ? ' active' : ''}" role="tab" aria-selected="${item.id === stage?.id}" href="/sports/cycling/${encodeURIComponent(race.id)}?stage=${encodeURIComponent(String(item.id).split(':').pop())}">
          <span>${esc(item.name)}</span><span class="small muted">${esc(cyclingDate(item.date))}</span></a>`).join('')}
      </div>
      <div class="event-actions">
        <button class="btn primary" id="cycling-watch">${watchLabel()}</button>
        <a class="btn" href="/live?q=${encodeURIComponent(race.name)}">Browse Live TV</a>
        ${race.sourceUrl ? `<a class="btn ghost" href="${esc(race.sourceUrl)}" rel="noopener noreferrer" target="_blank">Official race centre</a>` : ''}
      </div>
      <div id="cycling-watch-slot"></div>
      ${stagePanel(race, detail || stage)}
    `;
    root.querySelector('#cycling-watch').onclick = () => {
      const slot = root.querySelector('#cycling-watch-slot');
      if (!slot || !stage) return;
      mountSportsWatch(slot, {
        id: stage.id,
        title: race.name,
        participants: [],
        league: 'cycling',
        sport: 'cycling',
        eventKind: 'cycling-stage',
        eventContext: stage.name,
        status: 'scheduled',
        streamKind: 'cycling',
        parentId: race.id,
        searchText: `${race.name} ${stage.name || ''}`.trim(),
      });
    };
  }

  return root;
}

function stagePanel(race, stage) {
  if (!stage) return '<p class="muted">No stage has been published for this race.</p>';
  const modules = [];
  if (stage.teamResults) modules.push(resultTable('Team classification', stage.teamResults, true));
  modules.push(resultTable('Stage result', stage.results, false, stage.generalClassificationLabel ? '' : ''));
  if (stage.generalClassification && stage.generalClassification.state !== 'unavailable') {
    modules.push(resultTable(stage.generalClassificationLabel || 'General classification', stage.generalClassification, false));
  } else if (!race.scheduleOnly && !race.calendarOnly) {
    modules.push(resultTable(stage.generalClassificationLabel || 'General classification', stage.generalClassification, false));
  }
  return `<section class="event-section"><h2>${esc(cyclingStageName(stage))}</h2>
      <div class="small muted">${esc(cyclingDate(stage.date))}${stage.distance ? ` · ${esc(stage.distance)}` : ''}${stage.terrain ? ` · ${esc(stage.terrain)}` : ''}${stage.status ? ` · ${esc(stage.status)}` : ''}</div>
      ${routeGuide(stage.routeGuide)}
    </section>
    ${modules.join('')}`;
}

function resultTable(title, module, team) {
  const state = cyclingModuleState(module);
  const rows = cyclingResultRows(module);
  if (!rows.length) {
    return `<section class="event-section"><h2>${esc(title)}</h2>
      <p class="small muted">${esc(module?.reason || (state === 'pending' ? 'Select this stage to load published results.' : 'No classification has been published.'))}</p></section>`;
  }
  return `<section class="event-section"><h2>${esc(title)} <span class="small muted">${esc(state)}${module?.reason ? ` · ${esc(module.reason)}` : ''}</span></h2>
    <table class="scores stats-table"><thead><tr><th>Rank</th><th>${team ? 'Team' : 'Rider'}</th>${team ? '' : '<th>Team</th>'}<th>Time</th><th>Gap</th></tr></thead>
    <tbody>${rows.map((row) => `<tr>
      <td>${row.rank || ''}</td>
      <td>${esc(row.name)}${!team && row.bib ? ` <span class="small muted">#${row.bib}</span>` : ''}</td>
      ${team ? '' : `<td>${esc(row.team || '')}</td>`}
      <td>${esc(row.time || '')}</td><td>${esc(row.gap || '')}</td></tr>`).join('')}</tbody></table>
  </section>`;
}

function routeGuide(guide) {
  if (!guide) return '';
  if (guide.state === 'pending') return `<p class="small muted">${esc(guide.reason || 'Select this stage to load the organizer route guide.')}</p>`;
  const map = guide.mapUrl ? `/api/sports/logo?url=${encodeURIComponent(guide.mapUrl)}` : '';
  const profile = guide.profileUrl ? `/api/sports/logo?url=${encodeURIComponent(guide.profileUrl)}` : '';
  if (!map && !profile) {
    return `<p class="small muted">${esc(guide.reason || 'The organizer route guide is not available.')}${guide.sourceUrl ? ` <a href="${esc(guide.sourceUrl)}" rel="noopener noreferrer" target="_blank">Official stage guide</a>` : ''}</p>`;
  }
  return `<div class="route-guide">
    ${map ? `<figure><img loading="lazy" src="${esc(map)}" alt="Official stage map" onerror="this.closest('figure').remove()"><figcaption class="small muted">Route map${guide.state === 'stale' ? ' · last verified' : ''}</figcaption></figure>` : ''}
    ${profile ? `<figure><img loading="lazy" src="${esc(profile)}" alt="Official stage profile" onerror="this.closest('figure').remove()"><figcaption class="small muted">Profile${guide.state === 'stale' ? ' · last verified' : ''}</figcaption></figure>` : ''}
    ${guide.sourceUrl ? `<a class="btn small" href="${esc(guide.sourceUrl)}" rel="noopener noreferrer" target="_blank">Official stage guide</a>` : ''}
  </div>`;
}
