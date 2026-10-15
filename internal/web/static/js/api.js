// Wren TV server API + account (Supabase) client. No dependencies.

const LS = {
  get(k) { try { return JSON.parse(localStorage.getItem(k)); } catch { return null; } },
  set(k, v) { try { v == null ? localStorage.removeItem(k) : localStorage.setItem(k, JSON.stringify(v)); } catch {} },
};

export class ApiError extends Error {
  constructor(status, message) { super(message); this.status = status; }
}

export const state = {
  server: null,      // /api/server
  user: null,        // /api/me user
  libraryIds: null,
  // local mode: {token}; supabase mode: {access_token, refresh_token, expires_at, user}
  // Sessions saved before the rename live under the old haven.* key.
  session: LS.get('wrentv.session') ?? LS.get('haven.session'),
};

export function saveSession(s) {
  state.session = s;
  LS.set('wrentv.session', s);
  LS.set('haven.session', null);
}

export const isSupabase = () => state.server?.authMode === 'supabase';
const supa = () => state.server.supabase;

// ---------- account service (Supabase GoTrue / PostgREST / Functions) ----------

async function supaFetch(path, { method = 'GET', body, auth = true, headers = {} } = {}) {
  const h = { apikey: supa().publishableKey, 'Content-Type': 'application/json', ...headers };
  if (auth) h.Authorization = 'Bearer ' + (await accessToken());
  const res = await fetch(supa().url + path, { method, headers: h, body: body === undefined ? undefined : JSON.stringify(body) });
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = text; }
  if (!res.ok) {
    const msg = data?.msg || data?.error_description || data?.message || data?.error || res.statusText;
    throw new ApiError(res.status, msg);
  }
  return data;
}

function acceptSession(d) {
  saveSession({ access_token: d.access_token, refresh_token: d.refresh_token, expires_at: d.expires_at || (Date.now() / 1000 + d.expires_in), user: d.user });
}

export const account = {
  async signIn(email, password) {
    acceptSession(await supaFetch('/auth/v1/token?grant_type=password', { method: 'POST', body: { email, password }, auth: false }));
    await account.rpc('accept_invites').catch(() => {});
  },
  async signUp(email, password, displayName) {
    const d = await supaFetch('/auth/v1/signup', { method: 'POST', body: { email, password, data: { display_name: displayName } }, auth: false });
    if (d?.access_token) {
      acceptSession(d);
      await account.rpc('accept_invites').catch(() => {});
      return true;
    }
    return false; // email confirmation required
  },
  async signOut() {
    try { await supaFetch('/auth/v1/logout', { method: 'POST' }); } catch {}
    saveSession(null);
  },
  rpc(fn, args = {}) { return supaFetch('/rest/v1/rpc/' + fn, { method: 'POST', body: args }); },
  select(table, query) { return supaFetch(`/rest/v1/${table}?${query}`); },
  insert(table, row) { return supaFetch(`/rest/v1/${table}`, { method: 'POST', body: row, headers: { Prefer: 'return=representation' } }); },
  update(table, query, patch) { return supaFetch(`/rest/v1/${table}?${query}`, { method: 'PATCH', body: patch, headers: { Prefer: 'return=representation' } }); },
  remove(table, query) { return supaFetch(`/rest/v1/${table}?${query}`, { method: 'DELETE' }); },
};

let refreshing = null;
async function accessToken() {
  const s = state.session;
  if (!s) return '';
  if (!isSupabase()) return s.token;
  if (s.expires_at - 60 > Date.now() / 1000) return s.access_token;
  refreshing ??= supaFetch('/auth/v1/token?grant_type=refresh_token', { method: 'POST', body: { refresh_token: s.refresh_token }, auth: false })
    .then(acceptSession)
    .catch((e) => { saveSession(null); throw new ApiError(401, 'Your sign-in expired. Sign in again.'); })
    .finally(() => { refreshing = null; });
  await refreshing;
  return state.session.access_token;
}
export { accessToken };

// ---------- Wren TV server ----------

export async function api(path, { method = 'GET', body, form } = {}) {
  const headers = {};
  const tok = await accessToken().catch(() => '');
  if (tok) headers.Authorization = 'Bearer ' + tok;
  let payload;
  if (form) payload = form;
  else if (body !== undefined) { headers['Content-Type'] = 'application/json'; payload = JSON.stringify(body); }
  const res = await fetch(path, { method, headers, body: payload });
  if (res.status === 204) return null;
  const text = await res.text();
  let data = null;
  try { data = text ? JSON.parse(text) : null; } catch { data = text; }
  if (!res.ok) throw new ApiError(res.status, data?.error || res.statusText);
  return data;
}
api.raw = async (path, opts) => {
  const tok = await accessToken().catch(() => '');
  return fetch(path, { ...opts, headers: { ...(opts?.headers || {}), Authorization: 'Bearer ' + tok } });
};

export const img = (path, size = 'w342') => (path ? `/api/images/${size}/${path.replace(/^\//, '')}` : '');

// ---------- local accounts ----------

export const local = {
  async login(username, password) {
    const d = await api('/api/auth/login', { method: 'POST', body: { username, password, device: 'Web' } });
    saveSession({ token: d.token });
  },
  async setupOwner(username, password, displayName) {
    const d = await api('/api/setup/owner', { method: 'POST', body: { username, password, displayName, device: 'Web' } });
    saveSession({ token: d.token });
  },
};

// ---------- browser playback capabilities ----------

export function browserCaps() {
  const v = document.createElement('video');
  const mse = window.ManagedMediaSource || window.MediaSource;
  const can = (type) => {
    try { if (mse?.isTypeSupported(type)) return true; } catch {}
    return v.canPlayType(type) === 'probably';
  };
  const video = [];
  if (can('video/mp4; codecs="avc1.640028"')) video.push('h264');
  if (can('video/mp4; codecs="hvc1.2.4.L153.B0"') || can('video/mp4; codecs="hev1.2.4.L153.B0"')) video.push('hevc');
  if (can('video/mp4; codecs="av01.0.12M.10"')) video.push('av1');
  const audio = [];
  const a = { aac: 'mp4a.40.2', mp3: 'mp4a.6B', ac3: 'ac-3', eac3: 'ec-3', flac: 'flac', opus: 'opus', alac: 'alac' };
  for (const [name, codec] of Object.entries(a)) if (can(`audio/mp4; codecs="${codec}"`)) audio.push(name);
  if (!audio.includes('aac')) audio.push('aac');
  const hdrScreen = matchMedia('(dynamic-range: high)').matches;
  const dv = hdrScreen && can('video/mp4; codecs="dvh1.08.06"');
  const containers = ['mp4'];
  if (v.canPlayType('video/quicktime')) containers.push('mov');
  return {
    engines: ['avplayer'],
    nativeVideo: video,
    nativeAudio: audio,
    nativeContainers: containers,
    dolbyVision: dv,
    hdr: hdrScreen && video.includes('hevc'),
    maxKbps: 0,
    audioFallback: audio.includes('flac') ? 'flac' : 'aac',
  };
}
