// Server management: dashboard, libraries, users/sharing, torrents, settings.
import { api, account, state, isSupabase } from './api.js';
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
        <span class="muted">Torrent providers</span><span>${esc((st.torrentProviders || []).join(', ') || 'off')}</span>
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
        <td class="mono small">${l.paths.map(esc).join('<br>')}</td><td>${l.itemCount}</td><td>${timeAgo(l.lastScanAt)}</td>
        <td style="white-space:nowrap;text-align:right"><button class="btn small" data-scan="${l.id}">Scan</button>
        <button class="btn small" data-edit="${l.id}">Edit</button><button class="btn small danger" data-del="${l.id}">Remove</button></td></tr>`).join('')}</table></div>`
      : `<div class="empty"><h2>Add your first library</h2><p>Point Wren TV at a folder of movies or TV shows. On Docker, that's a path inside the container (e.g. /media/Movies).</p></div>`}</div>`);

  $('[data-add]', node).onclick = () => editLibrary();
  for (const b of $$('[data-edit]', node)) b.onclick = () => editLibrary(libs.find((l) => l.id === b.dataset.edit));
  for (const b of $$('[data-scan]', node)) b.onclick = async () => { await api(`/api/libraries/${b.dataset.scan}/scan`, { method: 'POST' }); toast('Scan started'); };
  for (const b of $$('[data-del]', node)) b.onclick = async () => {
    const l = libs.find((x) => x.id === b.dataset.del);
    if (!(await confirmDialog(`Remove ${l.name}?`, 'The library and its watch history are removed from Wren TV. Your files are not touched.'))) return;
    await api('/api/libraries/' + l.id, { method: 'DELETE' });
    render();
  };
  return node;
}

async function editLibrary(lib) {
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

// ---------------------------------------------------------------- users / sharing

export async function viewUsers() {
  return isSupabase() ? viewSharing() : viewLocalUsers();
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

async function viewLocalUsers() {
  setTitle('Users');
  const [users, libs] = await Promise.all([api('/api/users'), api('/api/libraries')]);
  const node = el(`<div><div class="page-head"><h1>Users</h1><button class="btn primary" data-add>Add user</button></div>
    <div class="panel"><table class="table"><tr><th>Name</th><th>Username</th><th>Access</th><th></th></tr>
    ${users.map((u) => `<tr><td><b>${esc(u.displayName)}</b></td><td>${esc(u.username)}</td>
      <td>${u.isAdmin ? 'Admin · all libraries' : u.libraryIds.length ? u.libraryIds.map((id) => esc(libs.find((l) => l.id === id)?.name || id)).join(', ') : '<span class="muted">No libraries</span>'}</td>
      <td style="text-align:right;white-space:nowrap"><button class="btn small" data-edit="${u.id}">Edit</button>
      ${u.id !== state.user.id ? `<button class="btn small danger" data-del="${u.id}">Remove</button>` : ''}</td></tr>`).join('')}</table></div></div>`);

  const edit = async (u) => {
    const body = `${u ? '' : '<div class="field"><label>Username</label><input name="username" required></div>'}
      <div class="field"><label>Name</label><input name="displayName" value="${esc(u?.displayName || '')}"></div>
      <div class="field"><label>${u ? 'New password (optional)' : 'Password'}</label><input name="password" type="password" minlength="8" ${u ? '' : 'required'} autocomplete="new-password"></div>
      ${u?.id === state.user.id ? '' : `<label class="check"><input type="checkbox" name="isAdmin" ${u?.isAdmin ? 'checked' : ''}> Admin (manages the server, sees every library)</label>`}
      <div class="field" style="margin-top:12px"><label>Libraries</label>${libs.map((l) => `<label class="check"><input type="checkbox" name="lib" value="${l.id}" ${u?.libraryIds?.includes(l.id) ? 'checked' : ''}> ${esc(l.name)}</label>`).join('')}</div>`;
    const ok = await modal(u ? `Edit ${u.displayName}` : 'Add user', body, {
      onOk: async (f) => {
        const v = Object.fromEntries(new FormData(f));
        const libraryIds = $$('input[name=lib]:checked', f).map((c) => c.value);
        const isAdminBox = $('input[name=isAdmin]', f);
        if (u) {
          const patch = { displayName: v.displayName, libraryIds };
          if (isAdminBox) patch.isAdmin = isAdminBox.checked;
          if (v.password) patch.password = v.password;
          await api('/api/users/' + u.id, { method: 'PATCH', body: patch });
        } else {
          await api('/api/users', { method: 'POST', body: { username: v.username, password: v.password, displayName: v.displayName, isAdmin: !!isAdminBox?.checked, libraryIds } });
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
  const libs = await api('/api/libraries');
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
    <div class="panel"><h2>People with access</h2><table class="table"><tr><th>Name</th><th>Email</th><th>Libraries</th><th></th></tr>
      <tr><td><b>${esc(state.user.displayName || 'You')}</b></td><td>${esc(state.session?.user?.email || '')}</td><td>Owner · all libraries</td><td></td></tr>
      ${shares.map((s) => `<tr><td>${esc(prof(s.user_id).display_name || '—')}</td><td>${esc(prof(s.user_id).email || '')}</td><td>${access(s.library_ids)}</td>
        <td style="text-align:right;white-space:nowrap">${owner ? `<button class="btn small" data-edit="${s.user_id}">Edit</button><button class="btn small danger" data-del="${s.user_id}">Remove</button>` : ''}</td></tr>`).join('')}
    </table></div>
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
    const p = modal(`Edit ${prof(s.user_id).display_name || 'access'}`, `<div class="field"><label>Libraries</label>${libraryChecks(libs, s.library_ids || [], s.library_ids == null)}</div>`, {
      onOk: (f) => account.update('server_shares', `server_id=eq.${sid}&user_id=eq.${s.user_id}`, { library_ids: readLibraryChecks(f) }),
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

// ---------------------------------------------------------------- torrents

let torrentTimer;
export async function viewTorrents() {
  setTitle('Torrents');
  clearTimeout(torrentTimer);
  const [ts, libs] = await Promise.all([api('/api/torrents'), api('/api/libraries')]);
  const node = el(`<div><div class="page-head"><h1>Torrents</h1><button class="btn primary" data-add>Add torrent</button></div>
    <p class="muted" style="margin-top:-8px">Torrents stream on demand and never seed. When one can't be played, your source manager is notified to find a replacement.</p>
    ${ts.length ? `<div class="panel"><table class="table"><tr><th>Name</th><th>Library</th><th>Status</th><th>Provider</th><th>Added</th><th></th></tr>
      ${ts.map((t) => `<tr><td style="max-width:420px;word-break:break-word">${esc(t.name || t.infoHash)}${t.error ? `<div class="small error">${esc(t.error)}</div>` : ''}</td>
        <td>${esc(libs.find((l) => l.id === t.libraryId)?.name || '')}</td><td><span class="status s-${esc(t.status)}">${esc(t.status)}</span></td>
        <td>${esc(t.provider || '—')}</td><td>${timeAgo(t.createdAt)}</td>
        <td style="text-align:right;white-space:nowrap"><button class="btn small" data-replace="${t.id}">Replace</button><button class="btn small danger" data-del="${t.id}">Remove</button></td></tr>`).join('')}</table></div>`
      : '<div class="empty"><h2>No torrents</h2><p>Add a magnet link or .torrent file to stream it into a library.</p></div>'}</div>`);

  const sourceForm = (withLib) => `${withLib ? `<div class="field"><label>Library</label><select name="libraryId">${libs.map((l) => `<option value="${l.id}">${esc(l.name)}</option>`).join('')}</select></div>` : ''}
    <div class="field"><label>Magnet link</label><textarea name="magnet" placeholder="magnet:?xt=urn:btih:…"></textarea></div>
    <div class="field"><label>…or a .torrent file</label><input type="file" name="file" accept=".torrent,application/x-bittorrent"></div>`;
  const submit = async (f, path, withLib) => {
    const fd = new FormData(f);
    if (!fd.get('magnet').trim() && !fd.get('file')?.size) throw new Error('Paste a magnet link or choose a file');
    if (!fd.get('file')?.size) fd.delete('file');
    await api(path, { method: 'POST', form: fd });
  };
  $('[data-add]', node).onclick = async () => {
    if (!libs.length) return toast('Add a library first');
    if (await modal('Add torrent', sourceForm(true), { ok: 'Add', onOk: (f) => submit(f, '/api/torrents', true) })) { toast('Torrent added'); render(); }
  };
  for (const b of $$('[data-replace]', node)) b.onclick = async () => {
    if (await modal('Replace source', sourceForm(false), { ok: 'Replace', onOk: (f) => submit(f, `/api/torrents/${b.dataset.replace}/replace`) })) render();
  };
  for (const b of $$('[data-del]', node)) b.onclick = async () => {
    if (!(await confirmDialog('Remove torrent?', 'Its items disappear from the library.'))) return;
    await api('/api/torrents/' + b.dataset.del, { method: 'DELETE' });
    render();
  };
  // Keep statuses fresh while something is in flight.
  if (ts.some((t) => t.status === 'resolving' || t.status === 'replacing')) {
    torrentTimer = setTimeout(() => location.pathname === '/manage/torrents' && !$('.modal') && render(), 4000);
  }
  return node;
}

// ---------------------------------------------------------------- settings

export async function viewSettings() {
  setTitle('Settings');
  const s = await api('/api/admin/settings');
  const hw = ['auto', 'videotoolbox', 'nvenc', 'qsv', 'vaapi', 'amf', 'none'];
  const node = el(`<form><div class="page-head"><h1>Settings</h1><button class="btn primary">Save changes</button></div>
    <p class="error" hidden></p>
    <div class="panel"><h2>General</h2><div class="form-grid">
      <div class="field"><label>Server name</label><input name="serverName" value="${esc(s.serverName)}"></div>
      <div class="field"><label>Public URL</label><input name="publicUrl" value="${esc(s.publicUrl)}" placeholder="https://wrentv.example.com"></div>
    </div></div>
    <div class="panel"><h2>Metadata &amp; transcoding</h2><div class="form-grid">
      <div class="field"><label>TMDB API key ${s.tmdbKeySet ? '<span class="ok">(set)</span>' : ''}</label><input name="tmdbApiKey" type="password" placeholder="${s.tmdbKeySet ? 'Leave blank to keep' : 'Required for posters and info'}" autocomplete="off"></div>
      <div class="field"><label>OMDb API key ${s.omdbKeySet ? '<span class="ok">(set)</span>' : ''}</label><input name="omdbApiKey" type="password" placeholder="${s.omdbKeySet ? 'Leave blank to keep' : 'Optional: IMDb, Rotten Tomatoes, Metacritic'}" autocomplete="off">
        <span class="small muted">Free key at <a href="https://www.omdbapi.com/apikey.aspx" target="_blank" rel="noopener" style="color:var(--accent)">omdbapi.com</a></span></div>
      <div class="field"><label>Hardware acceleration</label><select name="hwaccel">${hw.map((h) => `<option ${h === s.hwaccel ? 'selected' : ''}>${h}</option>`).join('')}</select></div>
    </div>
      <label class="check"><input type="checkbox" name="markerFingerprinting" ${s.markerFingerprinting ? 'checked' : ''}> Detect intros and credits by comparing episode audio (background job; chapters are always used)</label></div>
    <div class="panel"><h2>Torrents</h2>
      <label class="check"><input type="checkbox" name="torrentsEnabled" ${s.torrentsEnabled ? 'checked' : ''}> Enable torrent streaming (no seeding)</label>
      <div class="form-grid" style="margin-top:10px">
        <div class="field"><label>Providers (in order)</label><input name="torrentProviders" value="${esc((s.torrentProviders || []).join(', '))}" placeholder="debrid, builtin"></div>
        <div class="field"><label>Debrid service</label><select name="debridService"><option value="realdebrid">Real-Debrid</option></select></div>
        <div class="field"><label>Debrid API key ${s.debridKeySet ? '<span class="ok">(set)</span>' : ''}</label><input name="debridApiKey" type="password" placeholder="${s.debridKeySet ? 'Leave blank to keep' : ''}" autocomplete="off"></div>
      </div></div>
    <div class="panel"><h2>Webhooks</h2><p class="small muted">Called when a torrent can't be played, so a source manager can find a replacement.</p>
      <div class="hooks"></div><button type="button" class="btn small" data-addhook>Add webhook</button></div>
    <div class="panel"><h2>Integration key</h2><p class="small muted">Lets services like a source manager add torrents and libraries (header <code>X-WrenTV-Key</code>).</p>
      <div class="mono">${esc(s.integrationKey)}</div></div>
  </form>`);
  const hooks = $('.hooks', node);
  const hookRow = (h = {}) => {
    const r = el(`<div class="form-grid hook" style="align-items:end">
      <div class="field"><label>URL</label><input data-k="url" value="${esc(h.url || '')}" placeholder="http://riven:8080/wrentv"></div>
      <div class="field"><label>Secret</label><input data-k="secret" value="${esc(h.secret || '')}" autocomplete="off"></div>
      <div class="field"><label>Events</label><div style="display:flex;gap:8px"><input data-k="events" value="${esc((h.events || []).join(', '))}" placeholder="all"><button type="button" class="btn small danger">✕</button></div></div></div>`);
    $('button', r).onclick = () => r.remove();
    hooks.append(r);
  };
  (s.webhooks || []).forEach(hookRow);
  $('[data-addhook]', node).onclick = () => hookRow();
  node.onsubmit = async (e) => {
    e.preventDefault();
    const v = Object.fromEntries(new FormData(node));
    const body = {
      serverName: v.serverName, publicUrl: v.publicUrl, hwaccel: v.hwaccel, torrentsEnabled: !!v.torrentsEnabled,
      markerFingerprinting: !!v.markerFingerprinting,
      torrentProviders: v.torrentProviders.split(',').map((x) => x.trim()).filter(Boolean), debridService: v.debridService,
      webhooks: $$('.hook', node).map((r) => ({
        url: $('[data-k=url]', r).value.trim(), secret: $('[data-k=secret]', r).value,
        events: $('[data-k=events]', r).value.split(',').map((x) => x.trim()).filter(Boolean),
      })).filter((h) => h.url),
    };
    if (v.tmdbApiKey) body.tmdbApiKey = v.tmdbApiKey;
    if (v.omdbApiKey) body.omdbApiKey = v.omdbApiKey;
    if (v.debridApiKey) body.debridApiKey = v.debridApiKey;
    const err = $('.error', node);
    err.hidden = true;
    try {
      const res = await api('/api/admin/settings', { method: 'PATCH', body });
      state.server.name = res.serverName;
      toast(res.restartRequired ? 'Saved. Restart the server to apply all changes.' : 'Saved');
    } catch (ex) {
      err.textContent = ex.message;
      err.hidden = false;
    }
  };
  return node;
}
