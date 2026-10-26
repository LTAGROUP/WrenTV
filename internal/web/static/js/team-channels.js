// Manage Team Channels: link Live TV channels to persisted team identities.
// Reads are available to any signed-in user; every mutation here uses the
// admin-only routes, which enforce authorization again on the server.
import { api } from './api.js';
import { esc, $, $$, el, toast, confirmDialog } from './ui.js';
import { setTitle } from './app.js';

const state = {
  root: null,
  catalog: [],
  league: '',
  teams: [],
  editor: null, // { teamId, candidates, query, loading, error }
  preview: null,
  message: '',
  loading: false,
};

const confidence = (value) => `${Math.round(Number(value || 0) * 100)}%`;

export async function viewTeamChannels() {
  setTitle('Team Channels');
  state.editor = null;
  state.preview = null;
  state.message = '';
  let catalog = await api('/api/sports/leagues').catch(() => ({ leagues: [] }));
  state.catalog = (catalog.leagues || []).filter((league) => league.enabled && league.supportsTeams);
  if (!state.catalog.length) {
    return el(`<div><div class="page-head"><h1>Team Channels</h1></div>
      <div class="empty"><h2>No team competitions yet</h2>
      <p>Enable a team league in <a href="/manage/settings#sports" style="color:var(--accent)">Settings → Sports</a>, then sync teams.</p></div></div>`);
  }
  if (!state.catalog.some((league) => league.id === state.league)) state.league = state.catalog[0].id;
  state.root = el('<div><div class="center" style="min-height:40vh"><div class="spinner"></div></div></div>');
  await reload();
  paint();
  return state.root;
}

async function reload() {
  state.loading = true;
  try {
    const data = await api(`/api/sports/teams?league=${encodeURIComponent(state.league)}`);
    state.teams = data.teams || [];
    state.error = '';
  } catch (err) {
    state.teams = [];
    state.error = err.message;
  } finally {
    state.loading = false;
  }
}

function paint() {
  if (!state.root) return;
  const leagueName = state.catalog.find((league) => league.id === state.league)?.name || state.league;
  const linked = state.teams.filter((team) => (team.links || []).length).length;
  state.root.innerHTML = `
    <div class="page-head"><h1>Team Channels</h1>
      <select id="tc-league" aria-label="Competition">${state.catalog.map((league) => `<option value="${esc(league.id)}"${league.id === state.league ? ' selected' : ''}>${esc(league.name)}</option>`).join('')}</select>
      <button class="btn" id="tc-sync">Sync teams</button>
      <button class="btn" id="tc-preview">Preview auto-link</button>
      <button class="btn primary" id="tc-apply" ${state.preview?.eligibleCount ? '' : 'disabled'}>Apply ${state.preview?.eligibleCount || 0} link${state.preview?.eligibleCount === 1 ? '' : 's'}</button>
    </div>
    <p class="small muted">A team channel resolves by tvg-id or name against the current playlist, so provider id changes do not break it. Unresolved links are never offered for playback. <b>${linked}</b> of <b>${state.teams.length}</b> ${esc(leagueName)} teams have a linked channel.</p>
    ${state.message ? `<p class="small muted" role="status">${esc(state.message)}</p>` : ''}
    ${state.error ? `<p class="error">${esc(state.error)}</p>` : ''}
    ${state.loading ? '<div class="center" style="min-height:200px"><div class="spinner"></div></div>' : ''}
    ${!state.loading && !state.teams.length ? `<div class="empty"><h2>No teams in this competition yet</h2><p>Refresh sports data or press Sync teams, then try again.</p></div>` : ''}
    ${!state.loading && state.teams.length ? `<div class="panel table-wrap"><table class="table"><tr><th>Team</th><th>Primary channel</th><th>Backups</th><th></th></tr>
      ${state.teams.map((team) => {
        const links = team.links || [];
        const primary = links.find((link) => link.slot === 'primary');
        const backups = links.filter((link) => link.slot === 'backup');
        return `<tr><td><b>${esc(team.name)}</b><div class="small muted">${esc(team.league)}${team.abbreviation ? ` · ${esc(team.abbreviation)}` : ''}</div></td>
          <td>${primary ? `<span class="${primary.resolved ? 'ok' : 'error'}">${esc(primary.channelName)}</span>
            <div class="small muted">${primary.resolved ? 'In current playlist' : 'Not in the current playlist'}${primary.autoLinked ? ` · automatic ${confidence(primary.linkConfidence)}` : ' · manual'}</div>`
            : '<span class="muted">Not linked</span>'}</td>
          <td>${backups.length || '<span class="muted">—</span>'}</td>
          <td style="text-align:right"><button class="btn small" data-manage="${esc(team.id)}">Manage</button></td></tr>`;
      }).join('')}</table></div>` : ''}
    ${state.preview ? previewHTML() : ''}
    ${state.editor ? editorHTML() : ''}`;
  bind();
}

function previewHTML() {
  const preview = state.preview;
  return `<div class="panel"><div class="page-head" style="margin-bottom:6px"><h2>Automatic link preview</h2>
      <span class="small muted">${preview.eligibleCount} eligible · ${preview.skippedCount} skipped</span>
      <button class="btn small ghost" id="tc-preview-close">Close</button></div>
    ${preview.message ? `<p class="muted small">${esc(preview.message)}</p>` : ''}
    <p class="small muted">A team is linked only at 95% confidence, or at 85% with a 20-point lead over the runner-up. Teams with a primary channel are skipped.</p>
    <div class="table-wrap"><table class="table"><tr><th>Team</th><th>Candidate</th><th>Confidence</th><th>Gap</th><th>Result</th></tr>
      ${(preview.results || []).map((result) => `<tr><td>${esc(result.team.name)}</td>
        <td>${result.candidate ? `${esc(result.candidate.channelName)} <span class="small muted">${esc(result.candidate.matchReason || '')}</span>` : '<span class="muted">—</span>'}</td>
        <td>${result.candidate ? confidence(result.candidate.confidence) : '—'}</td>
        <td>${result.confidenceGap ? confidence(result.confidenceGap) : '—'}</td>
        <td>${result.eligible ? '<span class="ok">Eligible</span>' : `<span class="muted">${esc(result.skipReason || 'Skipped')}</span>`}</td></tr>`).join('')}</table></div></div>`;
}

function editorHTML() {
  const editor = state.editor;
  const team = state.teams.find((item) => item.id === editor.teamId);
  if (!team) return '';
  const links = team.links || [];
  const primary = links.find((link) => link.slot === 'primary');
  const backups = links.filter((link) => link.slot === 'backup');
  const unresolved = links.filter((link) => !link.resolved);
  const candidateRow = (candidate, manual = false) => `<tr>
    <td><b>${esc(candidate.channelName)}</b><div class="small muted">${esc([candidate.sourceName, candidate.group].filter(Boolean).join(' · '))}</div></td>
    <td>${confidence(candidate.confidence)} <span class="badge ${esc(candidate.confidenceTier || 'possible')}">${esc(manual ? 'manual' : candidate.confidenceTier || 'possible')}</span></td>
    <td class="small muted">${esc(candidate.matchReason || '')}</td>
    <td style="white-space:nowrap"><button class="btn small" data-primary="${esc(candidate.channelId)}">Set primary</button>
      <button class="btn small" data-backup="${esc(candidate.channelId)}">Add backup</button></td></tr>`;
  return `<div class="panel team-editor"><div class="page-head" style="margin-bottom:6px">
      <h2>${esc(team.name)}</h2><span class="small muted">${esc(team.league)}</span>
      <button class="btn small ghost" id="tc-editor-close" style="margin-left:auto">Close</button></div>
    ${unresolved.length ? `<p class="error small">${unresolved.length} linked channel${unresolved.length === 1 ? ' is' : 's are'} not in the current playlist. ${unresolved.length === 1 ? 'It is' : 'They are'} marked unresolved and never offered for playback.</p>` : ''}
    <h3>Current links</h3>
    ${!links.length ? '<p class="muted small">No channel linked yet. Choose a suggestion below.</p>' : `<div class="link-list">
      ${links.map((link, index) => `<div class="link-row ${link.resolved ? '' : 'unresolved'}">
        <span class="badge">${esc(link.slot === 'primary' ? 'Primary' : `Backup ${link.position}`)}</span>
        <div class="link-main"><b>${esc(link.channelName)}</b>
          <div class="small muted">${link.resolved ? '<span class="ok">In current playlist</span>' : '<span class="error">Unresolved</span>'}
            ${link.autoLinked ? ` · automatic ${confidence(link.linkConfidence)}${link.matchReason ? ` (${esc(link.matchReason)})` : ''}` : ' · manual'}
            ${link.sourceName ? ` · ${esc(link.sourceName)}` : ''}</div></div>
        <div class="link-actions">
          ${link.slot === 'backup' ? `<button class="btn small" data-backup-up="${esc(link.id)}" ${index <= 1 ? 'disabled' : ''} aria-label="Move backup earlier">↑</button>
            <button class="btn small" data-backup-down="${esc(link.id)}" ${index >= links.length - 1 ? 'disabled' : ''} aria-label="Move backup later">↓</button>` : ''}
          <button class="btn small danger" data-remove-link="${esc(link.id)}">Remove</button>
        </div></div>`).join('')}</div>`}
    <h3>Suggestions</h3>
    ${editor.error ? `<p class="error small">${esc(editor.error)}</p>` : ''}
    ${editor.loading ? '<p class="muted small">Ranking channels…</p>' : ''}
    ${!editor.loading && editor.candidates.length ? `<div class="table-wrap"><table class="table"><tr><th>Channel</th><th>Confidence</th><th>Reason</th><th></th></tr>
      ${editor.candidates.map((candidate) => candidateRow(candidate)).join('')}</table></div>` : (!editor.loading ? '<p class="muted small">No channel name matches this team. Search for a channel by name below.</p>' : '')}
    <h3>Search all channels</h3>
    <div class="team-add"><input data-tc-query type="search" placeholder="Channel name or tvg-id" value="${esc(editor.query)}" aria-label="Search channels">
      <button class="btn" data-tc-search>Search</button></div>
    ${editor.searchResults?.length ? `<div class="table-wrap"><table class="table"><tr><th>Channel</th><th>Confidence</th><th>Reason</th><th></th></tr>
      ${editor.searchResults.map((candidate) => candidateRow(candidate, true)).join('')}</table></div>` : ''}
    ${primary ? `<p class="small muted">Setting a new primary replaces ${esc(primary.channelName)}. Backups stay.</p>` : ''}
  </div>`;
}

function bind() {
  $('#tc-league', state.root)?.addEventListener('change', async (e) => {
    state.league = e.target.value;
    state.editor = null;
    state.preview = null;
    state.message = '';
    await reload();
    paint();
  });
  $('#tc-sync', state.root)?.addEventListener('click', async (e) => {
    e.currentTarget.disabled = true;
    try {
      const result = await api(`/api/admin/sports/teams/sync?league=${encodeURIComponent(state.league)}`, { method: 'POST' });
      // A partial sync stays honest: leagues that failed, and leagues whose
      // provider publishes no team catalog, are named beside the counts.
      const notes = [];
      if (result.failed?.length) notes.push(`${result.failed.length} league(s) failed`);
      if (result.unsupported?.length) notes.push(`${result.unsupported.length} without a provider team catalog`);
      const suffix = notes.length ? ` ${notes.join(', ')}.` : '';
      state.message = `Synced ${result.teams?.length || 0} teams (${result.changed || 0} changed).${suffix}`;
      await reload();
      paint();
    } catch (err) {
      toast(err.message);
      e.currentTarget.disabled = false;
    }
  });
  $('#tc-preview', state.root)?.addEventListener('click', async (e) => {
    e.currentTarget.disabled = true;
    try {
      state.preview = await api(`/api/sports/leagues/${encodeURIComponent(state.league)}/auto-link/preview`);
      state.message = '';
      paint();
    } catch (err) {
      toast(err.message);
      e.currentTarget.disabled = false;
    }
  });
  $('#tc-preview-close', state.root)?.addEventListener('click', () => { state.preview = null; paint(); });
  $('#tc-apply', state.root)?.addEventListener('click', async (e) => {
    e.currentTarget.disabled = true;
    try {
      const result = await api(`/api/sports/leagues/${encodeURIComponent(state.league)}/auto-link/apply`, { method: 'POST' });
      state.preview = result.preview || null;
      state.message = `Linked ${result.linkedCount} team${result.linkedCount === 1 ? '' : 's'} automatically. Existing primary links were kept.`;
      await reload();
      paint();
    } catch (err) {
      toast(err.message);
      e.currentTarget.disabled = false;
    }
  });
  for (const button of $$('[data-manage]', state.root)) button.onclick = () => openEditor(button.dataset.manage);
  $('#tc-editor-close', state.root)?.addEventListener('click', () => { state.editor = null; paint(); });
  if (!state.editor) return;
  for (const button of $$('[data-primary]', state.root)) button.onclick = () => addLink(button.dataset.primary, 'primary');
  for (const button of $$('[data-backup]', state.root)) button.onclick = () => addLink(button.dataset.backup, 'backup');
  for (const button of $$('[data-remove-link]', state.root)) button.onclick = () => removeLink(button.dataset.removeLink);
  for (const button of $$('[data-backup-up]', state.root)) button.onclick = () => moveBackup(button.dataset.backupUp, -1);
  for (const button of $$('[data-backup-down]', state.root)) button.onclick = () => moveBackup(button.dataset.backupDown, 1);
  $('[data-tc-search]', state.root)?.addEventListener('click', () => searchChannels());
  $('[data-tc-query]', state.root)?.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); searchChannels(); } });
}

async function openEditor(teamId) {
  state.editor = { teamId, candidates: [], searchResults: [], query: '', loading: true, error: '' };
  paint();
  await loadSuggestions();
}

async function loadSuggestions() {
  const editor = state.editor;
  if (!editor) return;
  editor.loading = true;
  editor.error = '';
  paint();
  try {
    const data = await api(`/api/sports/teams/${encodeURIComponent(editor.teamId)}/suggestions?limit=20`);
    if (state.editor?.teamId === editor.teamId) editor.candidates = data.candidates || [];
  } catch (err) {
    if (state.editor?.teamId === editor.teamId) editor.error = err.message;
  } finally {
    if (state.editor?.teamId === editor.teamId) editor.loading = false;
    paint();
  }
}

async function searchChannels() {
  const editor = state.editor;
  const input = $('[data-tc-query]', state.root);
  if (!editor || !input) return;
  editor.query = input.value.trim();
  if (!editor.query) return;
  editor.loading = true;
  paint();
  try {
    const data = await api(`/api/sports/teams/${encodeURIComponent(editor.teamId)}/suggestions?query=${encodeURIComponent(editor.query)}&limit=50`);
    if (state.editor?.teamId === editor.teamId) {
      editor.searchResults = data.candidates || [];
      editor.error = '';
    }
  } catch (err) {
    if (state.editor?.teamId === editor.teamId) editor.error = err.message;
  } finally {
    if (state.editor?.teamId === editor.teamId) editor.loading = false;
    paint();
  }
}

async function addLink(channelId, slot) {
  const editor = state.editor;
  if (!editor) return;
  try {
    const path = `/api/sports/teams/${encodeURIComponent(editor.teamId)}/links`;
    if (slot === 'primary') await api(`${path}/primary`, { method: 'PUT', body: { channelId } });
    else await api(path, { method: 'POST', body: { channelId } });
    state.message = slot === 'primary' ? 'Primary channel saved.' : 'Backup channel added.';
    await reload();
    paint();
  } catch (err) {
    toast(err.message);
  }
}

async function removeLink(linkId) {
  const editor = state.editor;
  if (!editor) return;
  const ok = await confirmDialog('Remove this channel link?', 'The team keeps its other links. Stream matching is unaffected until a new link is added.', 'Remove');
  if (!ok) return;
  try {
    await api(`/api/sports/teams/${encodeURIComponent(editor.teamId)}/links/${encodeURIComponent(linkId)}`, { method: 'DELETE' });
    state.message = 'Channel link removed.';
    await reload();
    paint();
  } catch (err) {
    toast(err.message);
  }
}

async function moveBackup(linkId, direction) {
  const editor = state.editor;
  const team = state.teams.find((item) => item.id === editor?.teamId);
  if (!editor || !team) return;
  const order = (team.links || []).filter((link) => link.slot === 'backup').map((link) => link.id);
  const index = order.indexOf(linkId);
  const next = index + direction;
  if (index < 0 || next < 0 || next >= order.length) return;
  [order[index], order[next]] = [order[next], order[index]];
  try {
    await api(`/api/sports/teams/${encodeURIComponent(editor.teamId)}/links/order`, { method: 'POST', body: { linkIds: order } });
    await reload();
    paint();
  } catch (err) {
    toast(err.message);
  }
}
