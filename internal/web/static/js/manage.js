// Server management: dashboard, libraries, users/sharing, downloads, settings.
import { api, account, state, isSupabase, usingAccount } from './api.js';
import { esc, $, $$, el, toast, modal, confirmDialog, fmtSize, timeAgo } from './ui.js';
import { setTitle, refreshLibraries, render, isAdmin } from './app.js';

// ---------------------------------------------------------------- dashboard

export async function viewStatus() {
  setTitle('Dashboard');
  const [st, libs, users] = await Promise.all([
    api('/api/admin/status'),
    api('/api/libraries'),
    isSupabase() ? Promise.resolve(null) : api('/api/users'),
  ]);
  const srv = state.server;
  const hw = st.hardware || {};
  const userName = (id) => users?.find((u) => u.id === id)?.displayName || id.slice(0, 8);
  const node = el(`<div>
    <div class="page-head"><h1>${esc(srv.name)}</h1><span class="muted">Wren TV ${esc(st.version)}</span></div>
    <div class="form-grid">
      <div class="panel"><h2>Server</h2><div class="kv">
        <span class="muted">Accounts</span><span>${isSupabase() ? 'Wren TV accounts' : 'Local accounts'}</span>
        <span class="muted">Machine ID</span><span class="mono">${esc(srv.machineId)}</span>
        <span class="muted">Addresses</span><span class="mono">${(srv.localUrls || []).map(esc).join('<br>') || '—'}</span>
        <span class="muted">Metadata</span><span>${st.metadata ? '<span class="ok">TMDB connected</span>' : '<span class="error">No TMDB key</span>'}</span>
        <span class="muted">Ratings</span><span>${st.ratings ? '<span class="ok">OMDb connected</span>' : '<span class="muted">No OMDb key (IMDb / Rotten Tomatoes hidden)</span>'}</span>
      </div></div>
      <div class="panel"><h2>Transcoding</h2><div class="kv">
        <span class="muted">Hardware</span><span>${esc((hw.kind || 'software').toUpperCase())}</span>
        <span class="muted">H.264 encoder</span><span class="mono">${esc(hw.h264Encoder || '—')}</span>
        <span class="muted">HW decode</span><span>${hw.hwDecode ? 'Yes' : 'No'}</span>
        <span class="muted">HDR tone mapping</span><span>${esc(hw.tonemap || 'none')}</span>
      </div></div>
      <div class="panel"><h2>Streaming</h2><div class="kv">
        <span class="muted">Status</span><span>${st.streaming?.active ? '<span class="ok">On</span>' : '<span class="muted">Off</span>'}</span>
        <span class="muted">Debrid</span><span>${esc((st.streaming?.providers || []).map(debridName).join(', ') || '—')}</span>
        <span class="muted">Streams playing</span><span>${st.streaming?.activeStreams ?? 0}</span>
      </div></div>
    </div>
    <div class="panel"><h2>Now playing</h2>
      ${st.sessions.length ? `<table class="table"><tr><th>User</th><th>Title</th><th>Playback</th><th>Started</th><th></th></tr>
        ${st.sessions.map((s) => `<tr><td>${esc(userName(s.userId))}</td><td><a href="/item/${s.itemId}" data-item="${s.itemId}">${esc(s.itemId)}</a></td>
          <td><b>${esc(s.method)}</b> <span class="small muted">${esc((s.reasons || []).join('; '))}</span></td><td>${timeAgo(s.started)}</td>
          <td><button class="btn small danger" data-stop="${s.id}">Stop</button></td></tr>`).join('')}</table>`
        : '<p class="muted">Nobody is watching right now.</p>'}</div>
    <div class="panel"><h2>Libraries</h2><table class="table"><tr><th>Name</th><th>Items</th><th>Last scan</th></tr>
      ${libs.map((l) => `<tr><td><a href="/library/${l.id}">${esc(l.name)}</a></td><td>${l.itemCount}</td><td>${timeAgo(l.lastScanAt)}</td></tr>`).join('') || '<tr><td colspan="3" class="muted">No libraries</td></tr>'}</table></div>
  </div>`);
  // Fill in titles for active sessions.
  for (const a of $$('[data-item]', node)) api('/api/items/' + a.dataset.item).then((d) => (a.textContent = d.item.kind === 'episode' ? `${d.show?.title || ''} S${d.item.seasonNumber}E${d.item.episodeNumber}` : d.item.title)).catch(() => {});
  for (const b of $$('[data-stop]', node)) b.onclick = async () => { await api('/api/sessions/' + b.dataset.stop, { method: 'DELETE' }); render(); };
  return node;
}

// ---------------------------------------------------------------- libraries

export async function viewLibraries() {
  setTitle('Libraries');
  const libs = await refreshLibraries();
  const node = el(`<div><div class="page-head"><h1>Libraries</h1><button class="btn primary" data-add>Add library</button></div>
    ${libs.length ? `<div class="panel"><table class="table"><tr><th>Name</th><th>Type</th><th>Folders</th><th>Items</th><th>Last scan</th><th></th></tr>
      ${libs.map((l) => `<tr><td><a href="/library/${l.id}"><b>${esc(l.name)}</b></a></td><td>${l.type === 'shows' ? 'TV Shows' : 'Movies'}</td>
        <td class="mono small">${l.source === 'stream' ? '<span class="muted" style="font-family:inherit">Streamed through your debrid service</span>' : l.paths.map(esc).join('<br>')}</td><td>${l.itemCount}</td><td>${timeAgo(l.lastScanAt)}</td>
        <td style="white-space:nowrap;text-align:right">${l.source === 'stream' ? `<button class="btn small" data-edit="${l.id}">Rename</button>` : `<button class="btn small" data-scan="${l.id}">Scan</button>
        <button class="btn small" data-edit="${l.id}">Edit</button>`}<button class="btn small danger" data-del="${l.id}">Remove</button></td></tr>`).join('')}</table></div>`
      : `<div class="empty"><h2>Add your first library</h2><p>Point Wren TV at a folder of movies or TV shows. On Docker, that's a path inside the container (e.g. /media/Movies).</p></div>`}</div>`);

  $('[data-add]', node).onclick = () => editLibrary();
  for (const b of $$('[data-edit]', node)) b.onclick = () => editLibrary(libs.find((l) => l.id === b.dataset.edit));
  for (const b of $$('[data-scan]', node)) b.onclick = async () => { await api(`/api/libraries/${b.dataset.scan}/scan`, { method: 'POST' }); toast('Scan started'); };
  for (const b of $$('[data-del]', node)) b.onclick = async () => {
    const l = libs.find((x) => x.id === b.dataset.del);
    const text = l.source === 'stream' ? 'Its titles, saved streams and watch history are removed. It comes back empty the next time someone streams.'
      : 'The library and its watch history are removed from Wren TV. Your files are not touched.';
    if (!(await confirmDialog(`Remove ${l.name}?`, text))) return;
    await api('/api/libraries/' + l.id, { method: 'DELETE' });
    render();
  };
  return node;
}

async function editLibrary(lib) {
  if (lib?.source === 'stream') {
    const ok = await modal('Rename library', `<div class="field"><label>Name</label><input name="name" value="${esc(lib.name)}" required></div>`, {
      onOk: (f) => api('/api/libraries/' + lib.id, { method: 'PATCH', body: { name: String(new FormData(f).get('name')).trim() } }),
    });
    if (ok) render();
    return;
  }
  const body = `<div class="field"><label>Name</label><input name="name" value="${esc(lib?.name || '')}" placeholder="Movies" required></div>
    ${lib ? '' : `<div class="field"><label>Type</label><select name="type"><option value="movies">Movies</option><option value="shows">TV Shows</option></select></div>`}
    <div class="field"><label>Folders (one per line)</label><textarea name="paths" required placeholder="/media/Movies">${esc((lib?.paths || []).join('\n'))}</textarea></div>
    <div class="field"><label>Browse server folders</label><div class="browser"></div></div>`;
  const done = modal(lib ? 'Edit library' : 'Add library', body, {
    ok: lib ? 'Save' : 'Add library',
    onOk: async (f) => {
      const v = Object.fromEntries(new FormData(f));
      const paths = v.paths.split('\n').map((s) => s.trim()).filter(Boolean);
      if (lib) await api('/api/libraries/' + lib.id, { method: 'PATCH', body: { name: v.name.trim(), paths } });
      else await api('/api/libraries', { method: 'POST', body: { name: v.name.trim(), type: v.type, paths } });
    },
  });
  folderBrowser($('.modal .browser'), (p) => {
    const ta = $('.modal textarea[name=paths]');
    const lines = ta.value.split('\n').map((s) => s.trim()).filter(Boolean);
    if (!lines.includes(p)) lines.push(p);
    ta.value = lines.join('\n');
  });
  if (await done) { toast(lib ? 'Library saved' : 'Library added — scanning'); render(); }
}

function folderBrowser(root, onPick) {
  const load = async (path) => {
    root.innerHTML = '<div class="small muted">Loading…</div>';
    try {
      const d = await api('/api/admin/browse?path=' + encodeURIComponent(path || ''));
      root.innerHTML = `<div class="mono small" style="margin-bottom:6px">${esc(d.path)}</div>
        <div style="max-height:180px;overflow:auto;border:1px solid var(--line);border-radius:6px">
        ${d.parent ? `<div class="nav" data-go="${esc(d.parent)}">↑ ..</div>` : ''}
        ${d.dirs.map((x) => `<div class="nav" data-go="${esc(x.path)}" style="justify-content:space-between"><span>📁 ${esc(x.name)}</span><button type="button" class="btn small" data-pick="${esc(x.path)}">Add</button></div>`).join('') || '<div class="small muted" style="padding:8px">No folders</div>'}</div>`;
      for (const n of $$('[data-go]', root)) n.onclick = (e) => { if (!e.target.closest('[data-pick]')) load(n.dataset.go); };
      for (const b of $$('[data-pick]', root)) b.onclick = () => onPick(b.dataset.pick);
    } catch (e) {
      root.innerHTML = `<div class="small error">${esc(e.message)}</div>`;
    }
  };
  load('');
}

export async function fixMatch(it) {
  const ok = await modal('Fix match', `<p class="muted small">Paste the TMDB link or ID for <b>${esc(it.title)}</b> (search on themoviedb.org).</p>
    <div class="field"><label>TMDB ID or URL</label><input name="id" placeholder="https://www.themoviedb.org/${it.kind === 'show' ? 'tv' : 'movie'}/603" required></div>`, {
    ok: 'Match',
    onOk: async (f) => {
      const m = String(new FormData(f).get('id')).match(/(\d+)/);
      if (!m) throw new Error('No TMDB ID found');
      await api(`/api/items/${it.id}/match`, { method: 'POST', body: { tmdbId: Number(m[1]) } });
    },
  });
  if (ok) { toast('Match updated'); render(); }
}

export function confirmForget(it) {
  const what = it.kind === 'show' ? 'every episode of this show' : it.kind === 'season' ? 'every episode of this season' : 'this title';
  return confirmDialog('Find new streams?', `The streams saved for ${what} are forgotten. The next play searches the addons again.`, 'Find new streams');
}

// ---------------------------------------------------------------- users / sharing

export async function viewUsers() {
  if (!isSupabase()) return viewLocalUsers();
  // Account servers can also have local accounts, for people without a Wren
  // TV account. They sign in by typing the server's address.
  const local = await viewLocalUsers(true);
  if (!usingAccount()) return local;
  const node = await viewSharing();
  node.append(local);
  return node;
}

function libraryChecks(libs, selected, all) {
  return `<label class="check"><input type="checkbox" name="all" ${all ? 'checked' : ''}> All libraries (including future ones)</label>
    <div class="libs" style="padding-left:24px">${libs.map((l) => `<label class="check"><input type="checkbox" name="lib" value="${l.id}" ${selected?.includes(l.id) ? 'checked' : ''}> ${esc(l.name)}</label>`).join('')}</div>`;
}

function bindLibraryChecks() {
  const all = $('.modal input[name=all]');
  const sync = () => $$('.modal input[name=lib]').forEach((c) => (c.disabled = all.checked));
  all.onchange = sync;
  sync();
}

function readLibraryChecks(f) {
  if ($('input[name=all]', f).checked) return null;
  const ids = $$('input[name=lib]:checked', f).map((c) => c.value);
  if (!ids.length) throw new Error('Pick at least one library');
  return ids;
}

// Per-user streaming limits. Blank numbers follow the server default; 0 is no limit.
function limitsFields(l = {}, d = {}) {
  const num = (v) => (v == null || v < 0 ? '' : v);
  const dflt = (v) => `Default (${v ? v : 'no limit'})`;
  const dl = l.allowDownloads ?? -1;
  return `<fieldset><legend>Streaming &amp; downloads</legend>
    <label class="check"><input type="checkbox" name="allowStreaming" ${l.allowStreaming !== false ? 'checked' : ''}> Can play titles streamed through debrid</label>
    <div class="form-grid">
      <div class="field"><label>Device downloads</label><select name="allowDownloads">
        <option value="-1" ${dl === -1 ? 'selected' : ''}>Default (${d.deviceDownloads ? 'allowed' : 'off'})</option>
        <option value="1" ${dl === 1 ? 'selected' : ''}>Allowed</option><option value="0" ${dl === 0 ? 'selected' : ''}>Not allowed</option></select></div>
      <div class="field"><label>Streams at once</label><input name="maxStreams" type="number" min="0" value="${num(l.maxStreams)}" placeholder="${dflt(d.maxStreams)}"></div>
      <div class="field"><label>Downloads at once</label><input name="maxDownloads" type="number" min="0" value="${num(l.maxDownloads)}" placeholder="${dflt(d.maxDownloads)}"></div>
    </div><p class="small muted" style="margin-top:0">Leave a number blank to use the server default; 0 means no limit.</p></fieldset>`;
}

function readLimits(f) {
  const v = Object.fromEntries(new FormData(f));
  const n = (x) => (String(x ?? '').trim() === '' ? -1 : Math.max(0, Math.floor(Number(x)) || 0));
  return { allowStreaming: !!v.allowStreaming, allowDownloads: Number(v.allowDownloads ?? -1), maxStreams: n(v.maxStreams), maxDownloads: n(v.maxDownloads) };
}

function limitsSummary(e) {
  if (!e) return '';
  const parts = [];
  if (!e.canStream && state.server.streaming) parts.push('No streaming');
  parts.push(e.maxStreams ? `${e.maxStreams} stream${e.maxStreams > 1 ? 's' : ''}` : 'Unlimited streams');
  parts.push(e.canDownload ? (e.maxDownloads ? `${e.maxDownloads} download${e.maxDownloads > 1 ? 's' : ''}` : 'Downloads') : 'No downloads');
  return `<span class="small muted">${parts.join(' · ')}</span>`;
}

async function viewLocalUsers(alongsideAccounts = false) {
  setTitle('Users');
  const [allUsers, libs, settings] = await Promise.all([api('/api/users'), api('/api/libraries'), api('/api/admin/settings')]);
  const users = alongsideAccounts ? allUsers.filter((u) => !u.externalId) : allUsers;
  const fileLibs = libs.filter((l) => l.source !== 'stream');
  const defaults = settings.userDefaults || {};
  const head = alongsideAccounts
    ? `<div class="page-head" style="margin-top:28px"><h2>Local accounts</h2><button class="btn" data-add>Add local account</button></div>
      <p class="muted" style="margin-top:-8px">For people without a Wren TV account. They open the app, choose <b>Connect to a server by address</b>, enter this server's address, then sign in with the username and password you set here.</p>`
    : '<div class="page-head"><h1>Users</h1><button class="btn primary" data-add>Add user</button></div>';
  const node = el(`<div>${head}
    ${alongsideAccounts && !users.length ? '' : `<div class="panel table-wrap"><table class="table"><tr><th>Name</th><th>Username</th><th>Access</th><th>Limits</th><th></th></tr>
    ${users.map((u) => `<tr><td><b>${esc(u.displayName)}</b></td><td>${esc(u.username)}</td>
      <td>${u.isAdmin ? 'Admin · all libraries' : u.libraryIds.length ? u.libraryIds.map((id) => esc(libs.find((l) => l.id === id)?.name || id)).join(', ') : '<span class="muted">No libraries</span>'}</td>
      <td>${limitsSummary(u.effective)}</td>
      <td style="text-align:right;white-space:nowrap"><button class="btn small" data-edit="${u.id}">Edit</button>
      ${u.id !== state.user.id ? `<button class="btn small danger" data-del="${u.id}">Remove</button>` : ''}</td></tr>`).join('')}</table></div>`}
    ${alongsideAccounts ? '' : `<p class="small muted">Defaults for everyone are in <a href="/manage/settings#users" style="color:var(--accent)">Settings → Users</a>. Streamed titles are visible to anyone allowed to stream.</p>`}</div>`);

  const edit = async (u) => {
    const body = `${u ? '' : '<div class="field"><label>Username</label><input name="username" required></div>'}
      <div class="field"><label>Name</label><input name="displayName" value="${esc(u?.displayName || '')}"></div>
      <div class="field"><label>${u ? 'New password (optional)' : 'Password'}</label><input name="password" type="password" minlength="8" ${u ? '' : 'required'} autocomplete="new-password"></div>
      ${u?.id === state.user.id ? '' : `<label class="check"><input type="checkbox" name="isAdmin" ${u?.isAdmin ? 'checked' : ''}> Admin (manages the server, sees every library)</label>`}
      <div class="field" style="margin-top:12px"><label>Libraries</label>${fileLibs.map((l) => `<label class="check"><input type="checkbox" name="lib" value="${l.id}" ${u?.libraryIds?.includes(l.id) ? 'checked' : ''}> ${esc(l.name)}</label>`).join('') || '<span class="small muted">No libraries yet</span>'}</div>
      ${limitsFields(u || { allowStreaming: true }, defaults)}`;
    const ok = await modal(u ? `Edit ${u.displayName}` : 'Add user', body, {
      onOk: async (f) => {
        const v = Object.fromEntries(new FormData(f));
        const libraryIds = $$('input[name=lib]:checked', f).map((c) => c.value);
        const isAdminBox = $('input[name=isAdmin]', f);
        const limits = readLimits(f);
        if (u) {
          const patch = { displayName: v.displayName, libraryIds, limits };
          if (isAdminBox) patch.isAdmin = isAdminBox.checked;
          if (v.password) patch.password = v.password;
          await api('/api/users/' + u.id, { method: 'PATCH', body: patch });
        } else {
          await api('/api/users', { method: 'POST', body: { username: v.username, password: v.password, displayName: v.displayName, isAdmin: !!isAdminBox?.checked, libraryIds, limits } });
        }
      },
    });
    if (ok) render();
  };
  $('[data-add]', node).onclick = () => edit();
  for (const b of $$('[data-edit]', node)) b.onclick = () => edit(users.find((u) => u.id === b.dataset.edit));
  for (const b of $$('[data-del]', node)) b.onclick = async () => {
    const u = users.find((x) => x.id === b.dataset.del);
    if (!(await confirmDialog(`Remove ${u.displayName}?`, 'They will be signed out and lose access to this server.'))) return;
    await api('/api/users/' + u.id, { method: 'DELETE' });
    render();
  };
  return node;
}

async function viewSharing() {
  setTitle('Sharing');
  const sid = state.server.serverId;
  const owner = state.user.role === 'owner';
  const [allLibs, serverUsers, settings] = await Promise.all([api('/api/libraries'), api('/api/users').catch(() => []), api('/api/admin/settings')]);
  const libs = allLibs.filter((l) => l.source !== 'stream');
  const serverUser = (accountId) => serverUsers.find((u) => u.externalId === accountId);
  const libName = (id) => esc(libs.find((l) => l.id === id)?.name || id);
  const access = (ids) => (ids == null ? 'All libraries' : ids.length ? ids.map(libName).join(', ') : '<span class="muted">None</span>');
  const [shares, invites] = await Promise.all([
    account.select('server_shares', `server_id=eq.${sid}&select=user_id,library_ids,created_at&order=created_at`),
    owner ? account.select('share_invites', `server_id=eq.${sid}&select=id,email,library_ids,created_at&order=created_at`) : Promise.resolve([]),
  ]);
  const profiles = shares.length ? await account.select('profiles', `id=in.(${shares.map((s) => s.user_id).join(',')})&select=id,display_name,email`) : [];
  const prof = (id) => profiles.find((p) => p.id === id) || {};

  const node = el(`<div><div class="page-head"><h1>Sharing</h1>${owner ? '<button class="btn primary" data-invite>Share server</button>' : ''}</div>
    <p class="muted" style="margin-top:-8px">People sign in with their own Wren TV account. Invite them by email; access starts the next time they sign in.</p>
    <div class="panel table-wrap"><h2>People with access</h2><table class="table"><tr><th>Name</th><th>Email</th><th>Libraries</th><th>Limits</th><th></th></tr>
      <tr><td><b>${esc(state.user.displayName || 'You')}</b></td><td>${esc(state.session?.user?.email || '')}</td><td>Owner · all libraries</td><td></td><td></td></tr>
      ${shares.map((s) => `<tr><td>${esc(prof(s.user_id).display_name || '—')}</td><td>${esc(prof(s.user_id).email || '')}</td><td>${access(s.library_ids)}</td>
        <td>${serverUser(s.user_id) ? limitsSummary(serverUser(s.user_id).effective) : '<span class="small muted">Defaults · not signed in yet</span>'}</td>
        <td style="text-align:right;white-space:nowrap">${owner ? `<button class="btn small" data-edit="${s.user_id}">Edit</button><button class="btn small danger" data-del="${s.user_id}">Remove</button>` : ''}</td></tr>`).join('')}
    </table></div>
    <p class="small muted">Defaults for everyone are in <a href="/manage/settings#users" style="color:var(--accent)">Settings → Users</a>.</p>
    ${invites.length ? `<div class="panel"><h2>Pending invites</h2><table class="table"><tr><th>Email</th><th>Libraries</th><th>Sent</th><th></th></tr>
      ${invites.map((i) => `<tr><td>${esc(i.email)}</td><td>${access(i.library_ids)}</td><td>${new Date(i.created_at).toLocaleDateString()}</td>
        <td style="text-align:right"><button class="btn small danger" data-cancel="${i.id}">Cancel</button></td></tr>`).join('')}</table></div>` : ''}
  </div>`);

  $('[data-invite]', node) && ($('[data-invite]', node).onclick = async () => {
    const p = modal('Share server', `<div class="field"><label>Email</label><input name="email" type="email" required placeholder="friend@example.com"></div>
      <div class="field"><label>Libraries</label>${libraryChecks(libs, [], true)}</div>`, {
      ok: 'Send invite',
      onOk: async (f) => {
        const email = String(new FormData(f).get('email')).trim().toLowerCase();
        await account.insert('share_invites', { server_id: sid, email, library_ids: readLibraryChecks(f) });
      },
    });
    bindLibraryChecks();
    if (await p) { toast('Invite saved. They get access when they sign in to Wren TV.'); render(); }
  });
  for (const b of $$('[data-edit]', node)) b.onclick = async () => {
    const s = shares.find((x) => x.user_id === b.dataset.edit);
    const su = serverUser(s.user_id);
    const p = modal(`Edit ${prof(s.user_id).display_name || 'access'}`, `<div class="field"><label>Libraries</label>${libraryChecks(libs, s.library_ids || [], s.library_ids == null)}</div>
      ${su ? limitsFields(su, settings.userDefaults || {}) : '<p class="small muted">Streaming and download limits can be set once they have signed in.</p>'}`, {
      onOk: async (f) => {
        await account.update('server_shares', `server_id=eq.${sid}&user_id=eq.${s.user_id}`, { library_ids: readLibraryChecks(f) });
        if (su) await api('/api/users/' + su.id, { method: 'PATCH', body: { limits: readLimits(f) } });
      },
    });
    bindLibraryChecks();
    if (await p) render();
  };
  for (const b of $$('[data-del]', node)) b.onclick = async () => {
    if (!(await confirmDialog('Remove access?', `${prof(b.dataset.del).email || 'This person'} will no longer see this server.`))) return;
    await account.remove('server_shares', `server_id=eq.${sid}&user_id=eq.${b.dataset.del}`);
    render();
  };
  for (const b of $$('[data-cancel]', node)) b.onclick = async () => {
    await account.remove('share_invites', `id=eq.${b.dataset.cancel}`);
    render();
  };
  return node;
}

// ---------------------------------------------------------------- downloads

const DEBRID = { torbox: 'TorBox', alldebrid: 'AllDebrid', realdebrid: 'Real-Debrid' };
const debridName = (p) => DEBRID[p] || p;

const dlStatus = { queued: 's-resolving', downloading: 's-resolving', done: 's-ready', failed: 's-failed', canceled: 's-unplayable' };

let downloadsTimer;
export async function viewDownloads() {
  setTitle('Downloads');
  clearTimeout(downloadsTimer);
  const list = await api('/api/admin/downloads');
  const pct = (d) => (d.total ? Math.min(100, (d.bytes / d.total) * 100) : 0);
  const node = el(`<div><div class="page-head"><h1>Downloads</h1></div>
    <p class="muted" style="margin-top:-8px">Streamed titles saved to the server as files. Once a download finishes, the file replaces the stream.</p>
    ${list.length ? `<div class="panel table-wrap"><table class="table"><tr><th>Title</th><th>Quality</th><th>Status</th><th>Progress</th><th>Added</th><th></th></tr>
      ${list.map((d) => `<tr><td style="max-width:380px">${d.itemId ? `<a href="/item/${d.itemId}"><b>${esc(d.title)}</b></a>` : `<b>${esc(d.title)}</b>`}
          ${d.error ? `<div class="small error" style="margin:2px 0 0">${esc(d.error)}</div>` : ''}${d.status === 'done' && d.path ? `<div class="small muted mono">${esc(d.path)}</div>` : ''}</td>
        <td>${esc(d.quality)}</td><td><span class="status ${dlStatus[d.status] || ''}">${esc(d.status)}</span></td>
        <td style="min-width:160px">${d.status === 'downloading' || (d.status === 'failed' && d.bytes) ? `<div class="meter"><i style="width:${pct(d).toFixed(1)}%"></i></div>
          <div class="small muted">${fmtSize(d.bytes)}${d.total ? ` of ${fmtSize(d.total)}` : ''}</div>` : d.total ? `<span class="small muted">${fmtSize(d.total)}</span>` : ''}</td>
        <td>${timeAgo(d.createdAt)}</td>
        <td style="text-align:right;white-space:nowrap">${d.status === 'failed' || d.status === 'canceled' ? `<button class="btn small" data-retry="${d.id}">Retry</button>` : ''}
          ${d.status === 'queued' || d.status === 'downloading' ? `<button class="btn small danger" data-cancel="${d.id}">Cancel</button>`
            : `<button class="btn small ghost" data-cancel="${d.id}" title="Remove from the list">Clear</button>`}</td></tr>`).join('')}</table></div>`
      : `<div class="empty"><h2>Nothing downloading</h2><p>Open a streamed title and choose <b>Download to server</b>, or turn on download mode in Settings → Streaming.</p></div>`}</div>`);
  for (const b of $$('[data-cancel]', node)) b.onclick = async () => { await api('/api/admin/downloads/' + b.dataset.cancel, { method: 'DELETE' }); render(); };
  for (const b of $$('[data-retry]', node)) b.onclick = async () => { await api(`/api/admin/downloads/${b.dataset.retry}/retry`, { method: 'POST' }); render(); };
  if (list.some((d) => d.status === 'queued' || d.status === 'downloading')) {
    downloadsTimer = setTimeout(() => location.pathname === '/manage/downloads' && !$('.modal') && render(), 3000);
  }
  return node;
}

// ---------------------------------------------------------------- settings

// Automatic HTTPS addresses under wrentv.net (servers using Wren TV accounts).
function remoteAccessPanel(ra) {
  const st = ra.status || {};
  let line;
  if (!ra.enabled) line = '<span class="muted">Off</span>';
  else if (st.remoteUrl) line = `<span class="ok">Reachable from the internet</span> <span class="small muted mono">${esc(st.remoteUrl)}</span>`;
  else if ((st.localUrls || []).length) line = '<span class="ok">Secure at home</span> <span class="small muted">· not reachable from outside yet</span>';
  else line = '<span class="muted">Setting up…</span>';
  return `<div class="panel"><h2>Remote access</h2>
    <p class="small muted">Gives this server secure (HTTPS) addresses so it plays in the web player at watch.wrentv.com and in the apps away from home. Video goes straight from this server to the viewer.</p>
    <p>${line}</p>
    ${st.problem ? `<p class="small muted">${esc(st.problem)}</p>` : ''}
    <label class="check"><input type="checkbox" name="remoteEnabled" ${ra.enabled ? 'checked' : ''}> Enable remote access</label>
    <label class="check"><input type="checkbox" name="remoteUpnp" ${ra.upnp ? 'checked' : ''}> Forward a port automatically (UPnP / NAT-PMP)</label>
    <div class="form-grid" style="margin-top:10px">
      <div class="field"><label>Manually forwarded port</label><input name="remotePort" type="number" min="0" max="65535" value="${ra.publicPort || ''}" placeholder="Automatic"></div>
    </div></div>`;
}

const QUALITIES = [['2160p', '4K (2160p)'], ['1080p', '1080p'], ['720p', '720p']];

// A number input whose blank value means 0 ("no limit").
const numField = (name, label, value, { unit = '', step = 1, hint = 'No limit' } = {}) => `<div class="field"><label>${label}</label>
  <div class="unit-input"><input name="${name}" type="number" min="0" step="${step}" value="${value || ''}" placeholder="${hint}">${unit ? `<span>${unit}</span>` : ''}</div></div>`;

function streamingStatusLine(st, s) {
  if (!st.enabled) return '<span class="muted">Off</span>';
  if (st.active) return '<span class="ok">On</span> <span class="small muted">· everyone allowed to stream can search for and play any movie or show</span>';
  if (!s.tmdbKeySet) return '<span class="error">Needs a TMDB API key</span> <span class="small muted">(General tab)</span>';
  const usenet = st.newsServers?.some((n) => n.enabled) && st.indexers?.some((i) => i.enabled && i.keySet);
  const keyless = st.indexers?.find((i) => i.enabled && !i.keySet);
  if (!usenet && keyless && st.newsServers?.some((n) => n.enabled)) return `<span class="error">Usenet is off: ${esc(keyless.name || 'an indexer')} needs its API key</span>`;
  if (!usenet && !st.addons.some((a) => a.enabled)) return '<span class="error">Needs an addon or Usenet</span>';
  return '<span class="muted">Starting…</span>';
}

function streamingTab(s, libs) {
  const st = s.streaming;
  const L = st.limits || {};
  const libOpts = (type, sel) => `<option value="">Choose a library…</option>${libs.filter((l) => l.source !== 'stream' && l.type === type)
    .map((l) => `<option value="${l.id}" ${l.id === sel ? 'selected' : ''}>${esc(l.name)}</option>`).join('')}`;
  return `<div data-tab="streaming">
    <div class="panel"><h2>Streaming</h2>
      <p class="small muted">Turns the server into a shared Stremio-style client. Search shows every movie and show; pressing play finds a cached release through your
        debrid service and streams it through this server. Only the stream link is saved, never the video, unless download mode is on.</p>
      <p data-st-status>${streamingStatusLine(st, s)}</p>
      <label class="check"><input type="checkbox" name="stEnabled" ${st.enabled ? 'checked' : ''}> Enable streaming</label>
      <label class="check"><input type="checkbox" name="stPrefetch" ${st.prefetch ? 'checked' : ''}> Find streams in the background before they're played: watchlist titles, and the rest of a season once an episode plays</label>
      <label class="check"><input type="checkbox" name="stLookupOnOpen" ${st.lookupOnOpen !== false ? 'checked' : ''}> Start finding streams when a movie's or episode's page opens, so play starts sooner</label>
      <label class="check"><input type="checkbox" name="stCheckLinks" ${st.checkAddonLinks ? 'checked' : ''}> Check addon links while finding streams, so dead ones are dropped before play (slower, and addons like StreamNZB start a download for each one checked)</label>
    </div>

    <div class="panel"><h2>Debrid services</h2>
      <p class="small muted">Your keys are used for everyone. Services are tried top to bottom; the first that has a release cached plays it.
        If every key is removed, streamed titles disappear until one is added back.</p>
      <div class="debrid-list">${st.debrid.map((d) => `<div class="debrid" data-provider="${d.provider}">
        <div class="order"><button type="button" class="btn small ghost" data-up title="Move up" aria-label="Move up">▲</button><button type="button" class="btn small ghost" data-down title="Move down" aria-label="Move down">▼</button></div>
        <div class="name"><b>${esc(d.name)}</b><div class="small" data-acct>${d.keySet ? `<span class="muted">Key ending ${esc(d.keyHint || '••••')}</span>` : '<span class="muted">Not connected</span>'}</div></div>
        <input type="password" data-key placeholder="${d.keySet ? 'Paste a new key to replace' : 'API key'}" autocomplete="off">
        <div class="acts"><button type="button" class="btn small" data-check>Check</button>${d.keySet ? '<button type="button" class="btn small danger" data-remove>Disconnect</button>' : ''}</div>
      </div>`).join('')}</div>
      <h3 class="sub-h">Link lifetime</h3>
      <p class="small muted">How long a play link is reused before the service is asked for a fresh one. Shorter catches links that stopped working
        sooner (a torrent no longer cached); longer asks the service less often. A stream that fails while playing always gets a fresh link.</p>
      <div class="form-grid">${[...st.debrid.map((d) => [d.provider, d.name]), ['addon', 'Addon links']].map(([p, name]) => `<div class="field"><label>${esc(name)}</label>
        <div class="unit-input"><input name="stTTL-${p}" type="number" min="0" max="10080" step="1" value="${st.linkTTLMinutes?.[p] || ''}" placeholder="60"><span>minutes</span></div></div>`).join('')}</div>
    </div>

    <div class="panel"><h2>Usenet</h2>
      <p class="small muted">Plays Usenet posts straight from your news server as they're watched, with nothing downloaded first.
        Posts are found on your indexers (NZBGeek and other Newznab sites). TorBox Pro includes a news server: its address,
        username and password are on torbox.app/tools, and it allows 10 connections.</p>
      <h3 class="sub-h">News servers</h3>
      <div class="nserver-list"></div>
      <button type="button" class="btn small" data-ns-add>Add news server</button>
      <h3 class="sub-h">Indexers</h3>
      <div class="indexer-list"></div>
      <button type="button" class="btn small" data-ix-add>Add indexer</button>
      <div style="margin-top:14px"><button type="button" class="btn" data-usenet-check>Check Usenet</button> <span class="small" data-usenet-res></span></div>
      <label class="check"><input type="checkbox" name="stUsenet" ${st.usenet !== false ? 'checked' : ''}> Play Usenet posts before torrents</label>
      <h3 class="sub-h">Article cache</h3>
      <div class="form-grid">
        ${numField('stCacheMB', 'In memory', st.usenetCacheMB, { unit: 'MB', hint: '256' })}
        ${numField('stDiskGB', 'On disk', st.usenetDiskCacheGB, { unit: 'GB', step: 0.5, hint: 'Off' })}
      </div>
      <p class="small muted">Articles already read are kept for rewinds and other viewers, so they aren't fetched again.</p>
    </div>

    <div class="panel"><h2>Addons</h2>
      <p class="small muted">Stremio addons that list streams for a title: torrents (Torrentio, Comet), or ready-to-play links from addons that use your debrid account themselves (AIOStreams, Meteor) or stream Usenet on their own (StreamNZB, which needs no debrid key). Results from every enabled addon are merged.</p>
      <div class="addon-list"></div>
      <div class="addon-add"><input type="text" data-addon-url placeholder="https://…/manifest.json" autocomplete="off"><button type="button" class="btn" data-addon-add>Add addon</button></div>
      <p class="small error" data-addon-err hidden></p>
    </div>

    <div class="panel"><h2>Quality</h2>
      <p class="small muted">Streams are saved for each quality picked here. The player chooses between them like versions of a file.</p>
      <div class="quality-grid">${QUALITIES.map(([q, label]) => `<div class="q"><label class="check"><input type="checkbox" name="stQ" value="${q}" ${st.qualities.includes(q) ? 'checked' : ''}> ${label}</label>
        <div class="unit-input"><input name="stMax-${q}" type="number" min="0" step="0.5" value="${st.maxSizeGB?.[q] || ''}" placeholder="Any size"><span>GB max</span></div></div>`).join('')}</div>
      <p class="small muted" style="margin-bottom:0">Size limits apply to the file that plays (one episode of a season pack, not the whole pack).</p>
    </div>

    <div class="panel"><h2>Saving to the server</h2>
      <div class="seg" role="radiogroup">
        <label><input type="radio" name="stMode" value="stream" ${st.mode !== 'download' ? 'checked' : ''}><span><b>Stream only</b><small>Nothing is written to your media folders. Admins can still download single titles.</small></span></label>
        <label><input type="radio" name="stMode" value="download" ${st.mode === 'download' ? 'checked' : ''}><span><b>Download what's watched</b><small>Each streamed title is also downloaded and becomes a regular file.</small></span></label>
      </div>
      <div class="form-grid" style="margin-top:14px">
        <div class="field"><label>Movies go to</label><select name="stMovieLib">${libOpts('movies', st.movieLibrary)}</select></div>
        <div class="field"><label>Shows go to</label><select name="stShowLib">${libOpts('shows', st.showLibrary)}</select></div>
        <div class="field"><label>Download quality</label><select name="stDlQuality">${QUALITIES.map(([q, label]) => `<option value="${q}" ${q === st.downloadQuality ? 'selected' : ''}>${label}</option>`).join('')}</select></div>
        <div class="field"><label>Versions per quality</label><select name="stPerQuality">${[1, 2, 3, 4, 5].map((n) => `<option value="${n}" ${n === (st.perQuality || 3) ? 'selected' : ''}>${n}</option>`).join('')}</select></div>
      </div>
      <p class="small muted">Each title gets this many 4K and 1080p versions to choose from. The best one plays unless the viewer picks another.</p>
    </div>

    <div class="panel"><h2>Release filters</h2>
      <div class="field"><label>Skip releases containing</label><textarea name="stExclude" rows="3" placeholder="One per line: HDTS, korean dub, /\\b3d\\b/i">${esc((st.excludeWords || []).join('\n'))}</textarea></div>
      <p class="small muted">Whole words only, so TS won't skip DTS-HD. Phrases match across dots, dashes and underscores. Put a regular expression between slashes.</p>
    </div>

    <div class="panel"><h2>Limits</h2>
      <p class="small muted">Leave a field blank for no limit.</p>
      <h3 class="sub-h">Bandwidth</h3>
      <div class="form-grid">
        ${numField('lBandwidth', 'All debrid traffic', L.bandwidthMbps, { unit: 'Mbps', step: 0.1 })}
        ${numField('lPerStream', 'Each stream', L.perStreamMbps, { unit: 'Mbps', step: 0.1 })}
        ${numField('lDownload', 'Server downloads', L.downloadMbps, { unit: 'Mbps', step: 0.1 })}
      </div>
      <h3 class="sub-h">Concurrency</h3>
      <div class="form-grid">
        ${numField('lStreams', 'Streams at once (all users)', L.maxStreams)}
        ${numField('lDownloads', 'Server downloads at once', L.maxDownloads)}
        ${numField('lPrefetch', 'Background lookups at once', L.prefetchWorkers, { hint: '2' })}
      </div>
      <h3 class="sub-h">Disk</h3>
      <div class="form-grid">
        ${numField('lQuota', 'Total downloaded', L.downloadQuotaGB, { unit: 'GB' })}
        ${numField('lFree', 'Keep this much free', L.minFreeGB, { unit: 'GB', hint: 'No minimum' })}
      </div>
    </div>
  </div>`;
}

// ---------------------------------------------------------------- live TV

function liveStatusLine(lt) {
  const st = lt.status || {};
  if (!lt.enabled) return '<span class="muted">Off</span>';
  if (st.refreshing && !st.channels) return '<span class="muted">Loading playlists…</span>';
  if (!lt.sources.length) return '<span class="error">Add a playlist below</span>';
  if (!st.channels) return '<span class="error">No channels found</span>';
  const guide = st.guideChannels ? `guide for ${st.guideChannels}` : 'no guide';
  const when = st.refreshed ? ` · updated ${timeAgo(Date.parse(st.refreshed) / 1000)}` : '';
  return `<span class="ok">On</span> <span class="small muted">· ${st.visible} of ${st.channels} channels shown · ${guide}${when}${st.refreshing ? ' · refreshing…' : ''}</span>`;
}

function liveTvTab(lt) {
  return `<div data-tab="livetv">
    <div class="panel"><h2>Live TV</h2>
      <p class="small muted">Watch channels from an IPTV provider on every Wren TV app. Add your playlist and the rest is automatic: channels are numbered,
        the guide is found and matched to them, and both are refreshed in the background. Streams play through this server, so your login never reaches the apps.</p>
      <p data-lt-status>${liveStatusLine(lt)}</p>
      <label class="check"><input type="checkbox" name="ltEnabled" ${lt.enabled ? 'checked' : ''}> Enable live TV</label>
      <ul class="small error" data-lt-errors ${(lt.status?.errors || []).length ? '' : 'hidden'}>${(lt.status?.errors || []).map((e) => `<li>${esc(e)}</li>`).join('')}</ul>
      <button type="button" class="btn small" data-lt-refresh ${lt.sources.length ? '' : 'disabled'}>Refresh now</button>
    </div>

    <div class="panel"><h2>Playlists</h2>
      <p class="small muted">An M3U playlist link, an Xtream Codes login (server, username and password) from your provider, or a Stremio addon with TV channels (its manifest link).
        The guide is found on its own; only fill in a guide link if your provider gave you a separate one.</p>
      <div class="lt-sources"></div>
      <button type="button" class="btn" data-lt-add>Add playlist</button>
    </div>

    <div class="panel"><h2>Channel groups</h2>
      <p class="small muted">Untick groups you don't want in the channel list. Big playlists often include whole countries you'll never watch.</p>
      <div class="lt-groups-bar"><input type="search" data-lt-filter placeholder="Filter groups" autocomplete="off">
        <button type="button" class="btn small" data-lt-all>Show all</button><button type="button" class="btn small" data-lt-none>Hide all</button></div>
      <div class="lt-groups"></div>
    </div>

    <div class="panel"><h2>Limits</h2>
      <p class="small muted">Most providers allow only a few connections at once. When the limit is reached, someone changing channel still works; a new viewer is told to wait.</p>
      <div class="form-grid">${numField('ltMaxStreams', 'Channels playing at once', lt.maxStreams, { hint: 'No limit' })}</div>
    </div>
  </div>`;
}

function setupLiveTv(node, lt, groups) {
  let sources = lt.sources.map((x) => ({ ...x }));
  const box = $('.lt-sources', node);
  const draw = () => {
    box.innerHTML = sources.length ? '' : '<p class="muted small">No playlists yet.</p>';
    sources.forEach((src) => {
      const x = src.kind === 'xtream';
      const sa = src.kind === 'stremio';
      const r = el(`<div class="lt-source">
        <div class="form-grid">
          <div class="field"><label>Name</label><input data-k="name" value="${esc(src.name || '')}" placeholder="My provider"></div>
          <div class="field"><label>Type</label><select data-k="kind"><option value="m3u" ${x || sa ? '' : 'selected'}>M3U playlist link</option><option value="xtream" ${x ? 'selected' : ''}>Xtream Codes login</option><option value="stremio" ${sa ? 'selected' : ''}>Stremio addon</option></select></div>
        </div>
        <div class="form-grid">
          <div class="field"><label>${x ? 'Server' : sa ? 'Addon manifest link' : 'Playlist link'}</label><input data-k="url" value="${esc(src.url || '')}" placeholder="${x ? 'http://provider.example:8080' : sa ? 'https://…/manifest.json' : 'https://…/playlist.m3u'}" autocomplete="off"></div>
          ${x ? `<div class="field"><label>Username</label><input data-k="username" value="${esc(src.username || '')}" autocomplete="off"></div>
          <div class="field"><label>Password</label><input data-k="password" type="password" value="${esc(src.password || '')}" autocomplete="new-password"></div>` : ''}
        </div>
        <details ${src.epgUrl || src.userAgent ? 'open' : ''}><summary class="small muted">Advanced</summary><div class="form-grid">
          <div class="field"><label>Guide (XMLTV) link</label><input data-k="epgUrl" value="${esc(src.epgUrl || '')}" placeholder="Found automatically" autocomplete="off"></div>
          <div class="field"><label>User agent</label><input data-k="userAgent" value="${esc(src.userAgent || '')}" placeholder="VLC (most providers expect it)" autocomplete="off"></div>
        </div></details>
        <div class="lt-acts"><label class="check" style="margin:0"><input type="checkbox" data-k="enabled" ${src.enabled !== false ? 'checked' : ''}> Use this playlist</label>
          <span class="small" data-res></span>
          <button type="button" class="btn small" data-check>Check</button><button type="button" class="btn small danger" data-del>Remove</button></div>
      </div>`);
      for (const inp of $$('[data-k]', r)) {
        inp.oninput = inp.onchange = () => {
          src[inp.dataset.k] = inp.type === 'checkbox' ? inp.checked : inp.value;
          if (inp.dataset.k === 'kind') draw();
        };
      }
      $('[data-del]', r).onclick = () => { sources = sources.filter((y) => y !== src); draw(); };
      $('[data-check]', r).onclick = async (e) => {
        const res = $('[data-res]', r);
        const btn = e.currentTarget;
        btn.disabled = true;
        res.innerHTML = '<span class="muted">Checking…</span>';
        try {
          const c = await api('/api/admin/livetv/check', { method: 'POST', body: src });
          res.innerHTML = `<span class="ok">${c.channels} channels</span> <span class="muted">· ${c.guide ? 'guide found' : 'no guide found'}</span>`;
        } catch (ex) { res.innerHTML = `<span class="error">${esc(ex.message)}</span>`; }
        btn.disabled = false;
      };
      box.append(r);
    });
  };
  draw();
  $('[data-lt-add]', node).onclick = () => {
    sources.push({ name: '', kind: 'm3u', url: '', enabled: true });
    draw();
    $$('.lt-source [data-k=url]', box).at(-1)?.focus();
  };

  // Groups
  const gbox = $('.lt-groups', node);
  const hidden = new Set(lt.hiddenGroups || []);
  gbox.innerHTML = groups.length
    ? groups.map((g) => `<label class="check" data-name="${esc(g.name.toLowerCase())}"><input type="checkbox" value="${esc(g.name)}" ${hidden.has(g.name) ? '' : 'checked'}> ${esc(g.name)} <span class="muted small">${g.channels}</span></label>`).join('')
    : '<p class="muted small">Groups show here once a playlist has loaded.</p>';
  const visibleBoxes = () => $$('label', gbox).filter((l) => !l.hidden).map((l) => $('input', l));
  $('[data-lt-filter]', node).oninput = (e) => {
    const q = e.target.value.trim().toLowerCase();
    for (const l of $$('label', gbox)) l.hidden = !!q && !l.dataset.name.includes(q);
  };
  $('[data-lt-all]', node).onclick = () => visibleBoxes().forEach((c) => { c.checked = true; });
  $('[data-lt-none]', node).onclick = () => visibleBoxes().forEach((c) => { c.checked = false; });

  // Refresh, then watch the status until it settles.
  const statusEl = $('[data-lt-status]', node);
  const errEl = $('[data-lt-errors]', node);
  const poll = async (n = 0) => {
    try {
      const r = await api('/api/admin/livetv');
      statusEl.innerHTML = liveStatusLine(r.liveTv);
      const errs = r.liveTv.status?.errors || [];
      errEl.innerHTML = errs.map((e) => `<li>${esc(e)}</li>`).join('');
      errEl.hidden = !errs.length;
      if (r.liveTv.status?.refreshing && n < 60) setTimeout(() => poll(n + 1), 2000);
    } catch { /* the page moved on */ }
  };
  $('[data-lt-refresh]', node).onclick = async (e) => {
    e.currentTarget.disabled = true;
    await api('/api/admin/livetv/refresh', { method: 'POST' });
    toast('Refreshing playlists and guide');
    setTimeout(() => poll(), 1000);
    e.currentTarget.disabled = false;
  };
  if (lt.status?.refreshing) setTimeout(() => poll(), 2000);

  // What the save sends.
  return (v) => {
    const out = {
      enabled: !!v.ltEnabled,
      maxStreams: Math.max(0, Math.floor(Number(v.ltMaxStreams) || 0)),
      sources: sources.map((x) => ({ ...x, enabled: x.enabled !== false })).filter((x) => (x.url || '').trim()),
    };
    // Groups only known once loaded; otherwise leave the hidden ones alone.
    if (groups.length) out.hiddenGroups = $$('input', gbox).filter((c) => !c.checked).map((c) => c.value);
    return out;
  };
}

export async function viewSettings() {
  setTitle('Settings');
  const [s, libs, live] = await Promise.all([api('/api/admin/settings'), api('/api/libraries'), api('/api/admin/livetv').catch(() => null)]);
  const hw = ['auto', 'videotoolbox', 'nvenc', 'qsv', 'vaapi', 'amf', 'none'];
  const ud = s.userDefaults || {};
  const tabs = [['general', 'General'], ['streaming', 'Streaming'], ['livetv', 'Live TV'], ['users', 'Users'], ['integrations', 'Integrations']];
  const node = el(`<form class="settings"><div class="page-head"><h1>Settings</h1><button class="btn primary" data-save>Save changes</button></div>
    <div class="tabs" role="tablist">${tabs.map(([k, label]) => `<button type="button" role="tab" data-tabbtn="${k}">${label}</button>`).join('')}</div>
    <p class="error" data-err hidden></p>

    <div data-tab="general">
      <div class="panel"><h2>Server</h2><div class="form-grid">
        <div class="field"><label>Server name</label><input name="serverName" value="${esc(s.serverName)}"></div>
        <div class="field"><label>Public URL</label><input name="publicUrl" value="${esc(s.publicUrl)}" placeholder="https://wrentv.example.com"></div>
      </div></div>
      ${s.remoteAccess ? remoteAccessPanel(s.remoteAccess) : ''}
      <div class="panel"><h2>Metadata</h2><div class="form-grid">
        <div class="field"><label>TMDB API key ${s.tmdbKeySet ? '<span class="ok">· set</span>' : ''}</label><input name="tmdbApiKey" type="password" placeholder="${s.tmdbKeySet ? 'Leave blank to keep' : 'Required for posters, info and streaming'}" autocomplete="off"></div>
        <div class="field"><label>OMDb API key ${s.omdbKeySet ? '<span class="ok">· set</span>' : ''}</label><input name="omdbApiKey" type="password" placeholder="${s.omdbKeySet ? 'Leave blank to keep' : 'Optional: IMDb, Rotten Tomatoes, Metacritic'}" autocomplete="off">
          <span class="small muted">Free key at <a href="https://www.omdbapi.com/apikey.aspx" target="_blank" rel="noopener" style="color:var(--accent)">omdbapi.com</a></span></div>
      </div></div>
      <div class="panel"><h2>Playback</h2><div class="form-grid">
        <div class="field"><label>Hardware acceleration</label><select name="hwaccel">${hw.map((h) => `<option ${h === s.hwaccel ? 'selected' : ''}>${h}</option>`).join('')}</select></div>
      </div>
        <label class="check"><input type="checkbox" name="markerFingerprinting" ${s.markerFingerprinting ? 'checked' : ''}> Detect intros and credits by comparing episode audio (background job; chapters are always used)</label>
        <label class="check"><input type="checkbox" name="markerCommunity" ${s.markerCommunity ? 'checked' : ''}> Use community intro and credits timestamps from IntroDB where nothing else has them (sends the title's IMDb id to IntroDB)</label></div>
      <div class="panel"><h2>Online subtitles</h2>
        <p class="small muted">Lets players search subtitle sites when a file has none in the right language. A provider is used once it has a key.</p>
        <div class="form-grid">
        <div class="field"><label>OpenSubtitles API key ${s.subtitles?.openSubtitlesKeySet ? '<span class="ok">· set</span>' : ''}</label><input name="osApiKey" type="password" placeholder="${s.subtitles?.openSubtitlesKeySet ? 'Leave blank to keep' : 'Consumer key'}" autocomplete="off">
          <span class="small muted">Create a consumer at <a href="https://www.opensubtitles.com/en/consumers" target="_blank" rel="noopener" style="color:var(--accent)">opensubtitles.com</a></span></div>
        <div class="field"><label>OpenSubtitles username</label><input name="osUsername" value="${esc(s.subtitles?.openSubtitlesUsername || '')}" placeholder="Optional: raises the daily download limit" autocomplete="off"></div>
        <div class="field"><label>OpenSubtitles password ${s.subtitles?.openSubtitlesPasswordSet ? '<span class="ok">· set</span>' : ''}</label><input name="osPassword" type="password" placeholder="${s.subtitles?.openSubtitlesPasswordSet ? 'Leave blank to keep' : ''}" autocomplete="new-password"></div>
        <div class="field"><label>SubDL API key ${s.subtitles?.subdlKeySet ? '<span class="ok">· set</span>' : ''}</label><input name="subdlApiKey" type="password" placeholder="${s.subtitles?.subdlKeySet ? 'Leave blank to keep' : 'Optional'}" autocomplete="off">
          <span class="small muted">Free key at <a href="https://subdl.com/panel/api" target="_blank" rel="noopener" style="color:var(--accent)">subdl.com</a></span></div>
      </div></div>
    </div>

    ${streamingTab(s, libs)}

    ${liveTvTab(s.liveTv)}

    <div data-tab="users">
      <div class="panel"><h2>Defaults for everyone</h2>
        <p class="small muted">Applies to every user unless their own settings say otherwise (Users → Edit). Blank means no limit.</p>
        <label class="check"><input type="checkbox" name="udDevice" ${ud.deviceDownloads ? 'checked' : ''}> Let people download movies and episodes to their devices</label>
        <div class="form-grid" style="margin-top:10px">
          ${numField('udStreams', 'Streams at once per person', ud.maxStreams)}
          ${numField('udDownloads', 'Device downloads at once per person', ud.maxDownloads)}
        </div>
        <p class="small muted" style="margin-bottom:0">Playing the same title on two screens counts once. A paused stream left idle for two minutes makes room for a new one.</p>
      </div>
    </div>

    <div data-tab="integrations">
      <div class="panel"><h2>Webhooks</h2><p class="small muted">Called on server events: <code>playback.start</code>, <code>playback.pause</code>, <code>playback.resume</code>, <code>playback.stop</code>, <code>playback.watched</code>, <code>item.watched</code>, <code>stream.unavailable</code>, <code>download.completed</code>, <code>download.failed</code>, and library changes.</p>
        <div class="hooks"></div><button type="button" class="btn small" data-addhook>Add webhook</button></div>
      <div class="panel"><h2>Simkl</h2><p class="small muted">Lets people link their Simkl account in their settings, to import watch history and scrobble what they watch. Register an app at <a href="https://simkl.com/settings/developer/" target="_blank" rel="noopener">simkl.com/settings/developer</a> (AUTH V2, type “TV, devices &amp; command line”) and paste its client ID.</p>
        <div class="field"><label>Client ID</label><input name="simklClientId" value="${esc(s.simklClientId || '')}" autocomplete="off" spellcheck="false"></div></div>
      <div class="panel"><h2>Integration key</h2><p class="small muted">Lets other services manage libraries on this server (header <code>X-WrenTV-Key</code>).</p>
        <div class="mono">${esc(s.integrationKey)}</div></div>
    </div>
  </form>`);

  // Tabs, remembered in the URL hash.
  const show = (k) => {
    if (!tabs.some(([t]) => t === k)) k = 'general';
    for (const b of $$('[data-tabbtn]', node)) b.setAttribute('aria-selected', String(b.dataset.tabbtn === k));
    for (const t of $$('[data-tab]', node)) t.hidden = t.dataset.tab !== k;
    history.replaceState(null, '', '#' + k);
  };
  for (const b of $$('[data-tabbtn]', node)) b.onclick = () => show(b.dataset.tabbtn);
  show(location.hash.slice(1));

  const readLiveTv = setupLiveTv(node, s.liveTv, live?.groups || []);

  // Webhooks
  const hooks = $('.hooks', node);
  const hookRow = (h = {}) => {
    const r = el(`<div class="form-grid hook" style="align-items:end">
      <div class="field"><label>URL</label><input data-k="url" value="${esc(h.url || '')}" placeholder="https://example.com/wrentv"></div>
      <div class="field"><label>Secret</label><input data-k="secret" value="${esc(h.secret || '')}" autocomplete="off"></div>
      <div class="field"><label>Events</label><div style="display:flex;gap:8px"><input data-k="events" value="${esc((h.events || []).join(', '))}" placeholder="all"><button type="button" class="btn small danger" aria-label="Remove webhook">✕</button></div></div></div>`);
    $('button', r).onclick = () => r.remove();
    hooks.append(r);
  };
  (s.webhooks || []).forEach(hookRow);
  $('[data-addhook]', node).onclick = () => hookRow();

  // Debrid: order, key checks, disconnect.
  const keyEdits = {};
  const list = $('.debrid-list', node);
  for (const row of $$('.debrid', node)) {
    const prov = row.dataset.provider;
    const acct = $('[data-acct]', row);
    $('[data-up]', row).onclick = () => row.previousElementSibling && list.insertBefore(row, row.previousElementSibling);
    $('[data-down]', row).onclick = () => row.nextElementSibling && list.insertBefore(row.nextElementSibling, row);
    $('[data-key]', row).oninput = (e) => { keyEdits[prov] = e.target.value.trim(); };
    $('[data-check]', row).onclick = async (e) => {
      const btn = e.currentTarget;
      btn.disabled = true;
      acct.innerHTML = '<span class="muted">Checking…</span>';
      try {
        const a = await api('/api/admin/streaming/check-debrid', { method: 'POST', body: { provider: prov, key: $('[data-key]', row).value.trim() } });
        if (a.ok) {
          const exp = a.expires ? ` · until ${new Date(a.expires).toLocaleDateString()}` : '';
          acct.innerHTML = `<span class="${a.premium ? 'ok' : 'error'}">${a.premium ? 'Premium' : 'Not premium'}</span> <span class="muted">${esc(a.user || '')}${exp}</span>`;
        } else acct.innerHTML = `<span class="error">${esc(a.error || 'Key rejected')}</span>`;
      } catch (ex) {
        acct.innerHTML = `<span class="error">${esc(ex.message)}</span>`;
      }
      btn.disabled = false;
    };
    const rm = $('[data-remove]', row);
    if (rm) rm.onclick = async () => {
      if (!(await confirmDialog(`Disconnect ${debridName(prov)}?`, 'The key is removed when you save. Without any debrid key, torrents can\'t play; Usenet and addons that stream on their own still do.', 'Disconnect'))) return;
      keyEdits[prov] = '';
      $('[data-key]', row).value = '';
      acct.innerHTML = '<span class="muted">Disconnects when you save</span>';
      rm.remove();
    };
  }

  // Usenet: news servers and indexers. Saved passwords and keys aren't
  // sent back; leaving the field empty keeps them.
  const servers = (s.streaming.newsServers || []).map((n) => ({ ...n, password: '' }));
  const indexers = (s.streaming.indexers || []).map((i) => ({ ...i, apiKey: '' }));
  const nsList = $('.nserver-list', node);
  const ixList = $('.indexer-list', node);
  const bind = (row, item, onDel) => {
    for (const el of $$('[data-f]', row)) {
      const f = el.dataset.f;
      el.oninput = el.onchange = () => {
        item[f] = el.type === 'checkbox' ? el.checked : el.type === 'number' ? Number(el.value) || 0 : el.value;
      };
    }
    $('[data-del]', row).onclick = onDel;
  };
  const drawServers = () => {
    nsList.innerHTML = servers.length ? servers.map((n, i) => `<div class="usenet-row" data-i="${i}">
      <label class="check" style="margin:0"><input type="checkbox" data-f="enabled" ${n.enabled ? 'checked' : ''} aria-label="Use this server"></label>
      <div class="form-grid">
        <div class="field"><label>Address</label><input type="text" data-f="host" value="${esc(n.host || '')}" placeholder="news.example.com" autocomplete="off"></div>
        <div class="field"><label>Port</label><input type="number" data-f="port" min="1" max="65535" value="${n.port || 563}"></div>
        <div class="field"><label>Connections</label><input type="number" data-f="connections" min="1" max="100" value="${n.connections || 10}"></div>
        <div class="field"><label>Username</label><input type="text" data-f="username" value="${esc(n.username || '')}" autocomplete="off"></div>
        <div class="field"><label>Password</label><input type="password" data-f="password" placeholder="${n.passwordSet ? 'Saved; type to replace' : ''}" autocomplete="new-password"></div>
        <div class="field"><label class="check" style="margin-top:26px"><input type="checkbox" data-f="tls" ${n.tls !== false ? 'checked' : ''}> SSL</label></div>
        <div class="field"><label class="check" style="margin-top:26px" title="Only asked for articles the other servers don't have"><input type="checkbox" data-f="backup" ${n.backup ? 'checked' : ''}> Backup only</label></div>
      </div>
      <div class="acts"><button type="button" class="btn small danger" data-del>Remove</button></div></div>`).join('')
      : '<p class="muted small">No news server.</p>';
    for (const r of $$('.usenet-row', nsList)) {
      const i = Number(r.dataset.i);
      bind(r, servers[i], () => { servers.splice(i, 1); drawServers(); });
    }
  };
  const drawIndexers = () => {
    ixList.innerHTML = indexers.length ? indexers.map((x, i) => `<div class="usenet-row" data-i="${i}">
      <label class="check" style="margin:0"><input type="checkbox" data-f="enabled" ${x.enabled ? 'checked' : ''} aria-label="Use this indexer"></label>
      <div class="form-grid">
        <div class="field"><label>Name</label><input type="text" data-f="name" value="${esc(x.name || '')}" placeholder="NZBGeek"></div>
        <div class="field"><label>Address</label><input type="text" data-f="url" value="${esc(x.url || '')}" placeholder="https://api.nzbgeek.info" autocomplete="off"></div>
        <div class="field"><label>API key</label><input type="password" data-f="apiKey" placeholder="${x.keySet ? `Saved ${esc(x.keyHint || '')}; type to replace` : 'Needed to search'}" autocomplete="off"></div>
      </div>
      <div class="acts"><button type="button" class="btn small danger" data-del>Remove</button></div></div>`).join('')
      : '<p class="muted small">No indexer.</p>';
    for (const r of $$('.usenet-row', ixList)) {
      const i = Number(r.dataset.i);
      bind(r, indexers[i], () => { indexers.splice(i, 1); drawIndexers(); });
    }
  };
  drawServers();
  drawIndexers();
  $('[data-ns-add]', node).onclick = () => { servers.push({ host: '', port: 563, tls: true, connections: 10, enabled: true, password: '' }); drawServers(); };
  $('[data-ix-add]', node).onclick = () => { indexers.push({ name: '', url: '', enabled: true, apiKey: '' }); drawIndexers(); };
  const usenetBody = () => ({
    newsServers: servers.filter((n) => n.host.trim()).map(({ passwordSet, ...n }) => n),
    indexers: indexers.filter((x) => x.url.trim()).map(({ keySet, keyHint, ...x }) => x),
  });
  $('[data-usenet-check]', node).onclick = async (e) => {
    const btn = e.currentTarget;
    const res = $('[data-usenet-res]', node);
    btn.disabled = true;
    res.innerHTML = '<span class="muted">Checking…</span>';
    try {
      await api('/api/admin/streaming/check-usenet', { method: 'POST', body: usenetBody() });
      res.innerHTML = '<span class="ok">Working</span>';
    } catch (ex) { res.innerHTML = `<span class="error">${esc(ex.message)}</span>`; }
    btn.disabled = false;
  };

  // Addons
  let addons = (s.streaming.addons || []).map((a) => ({ ...a }));
  const addonList = $('.addon-list', node);
  const addonErr = $('[data-addon-err]', node);
  const drawAddons = () => {
    addonList.innerHTML = addons.length ? addons.map((a, i) => `<div class="addon" data-i="${i}">
      <label class="check" style="margin:0"><input type="checkbox" data-on ${a.enabled ? 'checked' : ''} aria-label="Use ${esc(a.name)}"></label>
      <div class="name"><b>${esc(a.name)}</b><div class="small muted mono">${esc(a.url)}</div><div class="small" data-res></div></div>
      <div class="acts"><button type="button" class="btn small" data-check>Check</button><button type="button" class="btn small danger" data-del aria-label="Remove ${esc(a.name)}">Remove</button></div></div>`).join('')
      : '<p class="muted small">No addons. Add one below; streaming needs at least one.</p>';
    for (const r of $$('.addon', addonList)) {
      const a = addons[Number(r.dataset.i)];
      $('[data-on]', r).onchange = (e) => { a.enabled = e.target.checked; };
      $('[data-del]', r).onclick = () => { addons = addons.filter((x) => x !== a); drawAddons(); };
      $('[data-check]', r).onclick = async (e) => {
        const res = $('[data-res]', r);
        const btn = e.currentTarget;
        btn.disabled = true;
        try {
          const m = await api('/api/admin/streaming/check-addon', { method: 'POST', body: { url: a.url } });
          res.innerHTML = `<span class="ok">Working</span> <span class="muted">${esc(m.name || '')}</span>`;
        } catch (ex) { res.innerHTML = `<span class="error">${esc(ex.message)}</span>`; }
        btn.disabled = false;
      };
    }
  };
  drawAddons();
  const addAddon = async () => {
    const input = $('[data-addon-url]', node);
    const url = input.value.trim();
    if (!url) return;
    addonErr.hidden = true;
    const btn = $('[data-addon-add]', node);
    btn.disabled = true;
    try {
      const m = await api('/api/admin/streaming/check-addon', { method: 'POST', body: { url } });
      if (addons.some((a) => a.url === m.url)) throw new Error('That addon is already in the list');
      addons.push({ name: (m.name || new URL(m.url).host).toLowerCase(), url: m.url, enabled: true });
      input.value = '';
      drawAddons();
    } catch (ex) {
      addonErr.textContent = ex.message;
      addonErr.hidden = false;
    }
    btn.disabled = false;
  };
  $('[data-addon-add]', node).onclick = addAddon;
  $('[data-addon-url]', node).onkeydown = (e) => { if (e.key === 'Enter') { e.preventDefault(); addAddon(); } };

  // Download libraries only matter in download mode (and for admin downloads).
  const syncMode = () => {
    const dl = $('input[name=stMode]:checked', node)?.value === 'download';
    $('select[name=stMovieLib]', node).required = dl;
    $('select[name=stShowLib]', node).required = dl;
  };
  for (const r of $$('input[name=stMode]', node)) r.onchange = syncMode;
  syncMode();

  node.onsubmit = async (e) => {
    e.preventDefault();
    const v = Object.fromEntries(new FormData(node));
    const num = (x) => Math.max(0, Number(x) || 0);
    const int = (x) => Math.floor(num(x));
    const streaming = {
      enabled: !!v.stEnabled,
      prefetch: !!v.stPrefetch,
      lookupOnOpen: !!v.stLookupOnOpen,
      checkAddonLinks: !!v.stCheckLinks,
      linkTTLMinutes: Object.fromEntries([...s.streaming.debrid.map((d) => d.provider), 'addon'].map((p) => [p, int(v['stTTL-' + p])])),
      providerOrder: $$('.debrid', node).map((r) => r.dataset.provider),
      addons,
      qualities: $$('input[name=stQ]:checked', node).map((c) => c.value),
      maxSizeGB: Object.fromEntries(QUALITIES.map(([q]) => [q, num(v['stMax-' + q])])),
      mode: v.stMode,
      movieLibrary: v.stMovieLib,
      showLibrary: v.stShowLib,
      downloadQuality: v.stDlQuality,
      perQuality: int(v.stPerQuality),
      excludeWords: String(v.stExclude || '').split('\n').map((w) => w.trim()).filter(Boolean),
      usenetCacheMB: int(v.stCacheMB),
      usenetDiskCacheGB: num(v.stDiskGB),
      usenet: !!v.stUsenet,
      ...usenetBody(),
      limits: {
        bandwidthMbps: num(v.lBandwidth), perStreamMbps: num(v.lPerStream), downloadMbps: num(v.lDownload),
        maxStreams: int(v.lStreams), maxDownloads: int(v.lDownloads), prefetchWorkers: int(v.lPrefetch),
        downloadQuotaGB: num(v.lQuota), minFreeGB: num(v.lFree),
      },
    };
    for (const [prov, key] of Object.entries(keyEdits)) streaming[prov + 'Key'] = key;
    const body = {
      serverName: v.serverName, publicUrl: v.publicUrl, hwaccel: v.hwaccel,
      markerFingerprinting: !!v.markerFingerprinting,
      markerCommunity: !!v.markerCommunity,
      streaming,
      liveTv: readLiveTv(v),
      userDefaults: { deviceDownloads: !!v.udDevice, maxStreams: int(v.udStreams), maxDownloads: int(v.udDownloads) },
      webhooks: $$('.hook', node).map((r) => ({
        url: $('[data-k=url]', r).value.trim(), secret: $('[data-k=secret]', r).value,
        events: $('[data-k=events]', r).value.split(',').map((x) => x.trim()).filter(Boolean),
      })).filter((h) => h.url),
    };
    if (s.remoteAccess) {
      body.remoteAccess = { enabled: !!v.remoteEnabled, upnp: !!v.remoteUpnp, publicPort: Number(v.remotePort) || 0 };
    }
    if (v.simklClientId !== undefined) body.simklClientId = v.simklClientId.trim();
    if (v.tmdbApiKey) body.tmdbApiKey = v.tmdbApiKey;
    if (v.omdbApiKey) body.omdbApiKey = v.omdbApiKey;
    body.subtitles = { openSubtitlesUsername: String(v.osUsername || '').trim() };
    if (v.osApiKey) body.subtitles.openSubtitlesApiKey = v.osApiKey;
    if (v.osPassword) body.subtitles.openSubtitlesPassword = v.osPassword;
    if (v.subdlApiKey) body.subtitles.subdlApiKey = v.subdlApiKey;
    const err = $('[data-err]', node);
    err.hidden = true;
    const save = $('[data-save]', node);
    save.disabled = true;
    try {
      const res = await api('/api/admin/settings', { method: 'PATCH', body });
      state.server.name = res.serverName;
      state.server.streaming = res.streaming?.active;
      toast(res.restartRequired ? 'Saved. Restart the server to apply all changes.' : 'Saved');
      render();
    } catch (ex) {
      err.textContent = ex.message;
      err.hidden = false;
      window.scrollTo({ top: 0, behavior: 'smooth' });
    }
    save.disabled = false;
  };
  return node;
}
