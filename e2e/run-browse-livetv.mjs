// Runs the Browse Live TV first-click regression against a real Wren TV server.
//
// It builds the server, writes a throwaway data directory, seeds a local admin,
// starts the fixture provider, generates a Live TV playlist from the day's real
// events and gives a second Stremio source the same events as event listings.
// Then it runs Playwright against that deployment. No API is mocked.
//
// Usage: node e2e/run-browse-livetv.mjs
//   WRENTV_BROWSE_DATA   data directory (default /tmp/wrentv-browse)
//   WRENTV_BROWSE_PORT   server port (default 8100)
//   WRENTV_BROWSE_USER   admin username (default gate)
//   WRENTV_BROWSE_PASS   admin password (default gate-password)
//
// Unlike the deployment gate this run needs no ffmpeg: the fixture HLS is a real
// playlist with one short segment, which is enough to prove a session starts.
import { spawn, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';

const root = path.resolve(import.meta.dirname, '..');
const data = process.env.WRENTV_BROWSE_DATA || '/tmp/wrentv-browse';
const port = Number(process.env.WRENTV_BROWSE_PORT || 8100);
const user = process.env.WRENTV_BROWSE_USER || 'gate';
const password = process.env.WRENTV_BROWSE_PASS || 'gate-password';
const baseUrl = `http://127.0.0.1:${port}`;
const fixturePort = 8392;
const fixtureBase = `http://127.0.0.1:${fixturePort}`;
const mediaDir = path.join(data, 'fixture-media');
const playlistFile = path.join(data, 'fixture-tv.m3u');
const guideFile = path.join(data, 'fixture-guide.xml');
const runDir = path.join(data, 'artifacts');

const children = [];
let token = '';

function log(...args) {
  console.log('[browse]', ...args);
}

function fail(message) {
  console.error(`[browse] ${message}`);
  cleanup();
  process.exit(1);
}

function cleanup() {
  for (const child of children.reverse()) {
    if (!child.killed) child.kill('SIGTERM');
  }
}

process.on('exit', cleanup);
process.on('SIGINT', () => {
  cleanup();
  process.exit(130);
});

function run(command, args, options = {}) {
  const result = spawnSync(command, args, { cwd: root, stdio: 'inherit', ...options });
  if (result.status !== 0) fail(`${command} ${args.join(' ')} exited ${result.status}`);
}

async function waitFor(url, { timeoutMs = 60000, label = url } = {}) {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    try {
      const res = await fetch(url);
      if (res.ok) return res;
    } catch {
      // not up yet
    }
    await new Promise((resolve) => setTimeout(resolve, 500));
  }
  fail(`${label} did not become ready`);
}

async function api(method, route, body, bearer = token) {
  const res = await fetch(`${baseUrl}${route}`, {
    method,
    headers: {
      ...(body ? { 'Content-Type': 'application/json' } : {}),
      ...(bearer ? { Authorization: `Bearer ${bearer}` } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  const text = await res.text();
  let payload = null;
  try {
    payload = text ? JSON.parse(text) : null;
  } catch {
    payload = text;
  }
  return { status: res.status, payload };
}

// ------------------------------------------------------------------ fixtures

// A real HLS playlist and one real segment. The bytes only need to be a valid
// transport-stream packet for the server to serve and for hls.js to fetch; the
// regression is about discovery, not picture quality.
function prepareMedia() {
  fs.mkdirSync(mediaDir, { recursive: true });
  fs.writeFileSync(
    path.join(mediaDir, 'index.m3u8'),
    ['#EXTM3U',
      '#EXT-X-VERSION:3',
      '#EXT-X-TARGETDURATION:6',
      '#EXT-X-MEDIA-SEQUENCE:0',
      '#EXT-X-PLAYLIST-TYPE:VOD',
      '#EXTINF:6.0,',
      'seg0.ts',
      '#EXT-X-ENDLIST',
    ].join('\n') + '\n',
  );
  fs.writeFileSync(path.join(mediaDir, 'seg0.ts'), Buffer.alloc(188, 0x47));
}

function writeConfig() {
  const config = `server_name: Browse Live TV Regression
listen: 127.0.0.1:${port}
remote_access:
  enabled: false
  upnp: false
auth:
  supabase:
    server_id: browse-livetv
    server_secret: browse-livetv
scanner:
  watch: false
  interval_min: 0
sports:
  enabled: true
  # Several leagues, so the board carries a live or scheduled event at any hour:
  # the SportsHub Watch panel regression compares a channel row with a provider
  # row on one event, which a day of settled games cannot show.
  leagues: ["nba", "nfl", "mlb", "nhl", "soccer-eng.1", "college-football"]
live_tv:
  enabled: true
  sources:
    - id: fixture
      name: Fixture TV
      enabled: true
      kind: m3u
      url: ${fixtureBase}/tv.m3u
      epg_url: ${fixtureBase}/guide.xml
    - id: events
      name: Gate Events
      enabled: true
      kind: stremio
      url: ${fixtureBase}/addon-events/manifest.json
streaming:
  enabled: false
`;
  fs.writeFileSync(path.join(data, 'wrentv.yaml'), config);
}

// The event's own title is the channel name, so the ordinary matcher has the
// same evidence a real provider would publish.
function eventName(event) {
  return (event.title || (event.participants || []).map((participant) => participant.name).filter(Boolean).join(' vs ')).trim();
}

function writePlaylist(events) {
  const lines = ['#EXTM3U'];
  const guide = ['<?xml version="1.0" encoding="UTF-8"?>', '<tv generator-info-name="browse">'];
  const now = new Date();
  // XMLTV wants "20261016200000 +0000"; an ISO T makes the server drop every
  // programme, which would silently weaken the match.
  const stamp = (date) => `${date.toISOString().slice(0, 19).replace(/[-:]/g, '').replace('T', '')} +0000`;
  const start = new Date(now.getTime() - 60 * 60 * 1000);
  const stop = new Date(now.getTime() + 6 * 60 * 60 * 1000);
  events.forEach((event, index) => {
    const name = eventName(event);
    const channel = `gate${index}`;
    lines.push(`#EXTINF:-1 tvg-id="${channel}" tvg-name="${name}" group-title="Sports",${name}`);
    lines.push(`${fixtureBase}/media/open/index.m3u8`);
    guide.push(`  <channel id="${channel}"><display-name>${name}</display-name></channel>`);
    guide.push(`  <programme start="${stamp(start)}" stop="${stamp(stop)}" channel="${channel}"><title>${name} Live</title><desc>Fixture coverage</desc></programme>`);
  });
  guide.push('</tv>');
  fs.writeFileSync(playlistFile, `${lines.join('\n')}\n`);
  fs.writeFileSync(guideFile, `${guide.join('\n')}\n`);
}

// ---------------------------------------------------------------------- main

async function main() {
  fs.rmSync(data, { recursive: true, force: true });
  fs.mkdirSync(path.join(data, 'fixture'), { recursive: true });
  fs.mkdirSync(runDir, { recursive: true });
  log('data directory', data);

  log('building the server');
  run('go', ['build', '-o', path.join(data, 'wrentv'), './cmd/wrentv'], {
    env: { ...process.env, PATH: `${process.env.PATH}:/usr/local/go/bin:/tmp/gotool/go/bin`, GOMODCACHE: '/tmp/gomodcache', GOCACHE: '/tmp/gocache' },
  });

  writeConfig();
  log('seeding the admin account');
  run('go', ['run', './e2e/seed', '-data', data, '-user', user, '-password', password], {
    env: { ...process.env, PATH: `${process.env.PATH}:/usr/local/go/bin:/tmp/gotool/go/bin`, GOMODCACHE: '/tmp/gomodcache', GOCACHE: '/tmp/gocache' },
  });

  prepareMedia();
  fs.writeFileSync(playlistFile, '#EXTM3U\n');
  fs.writeFileSync(guideFile, '<?xml version="1.0"?><tv></tv>\n');
  const fixture = spawn('node', [path.join(root, 'e2e/fixture-server.mjs')], {
    cwd: root,
    stdio: 'inherit',
    env: {
      ...process.env,
      FIXTURE_PORT: String(fixturePort),
      FIXTURE_MEDIA_DIR: mediaDir,
      FIXTURE_PLAYLIST: playlistFile,
      FIXTURE_GUIDE: guideFile,
    },
  });
  children.push(fixture);
  await waitFor(`${fixtureBase}/addon-events/manifest.json`, { label: 'fixture event provider' });

  log('starting the server binary');
  const server = spawn(path.join(data, 'wrentv'), ['-data', data], { cwd: root, stdio: ['ignore', 'pipe', 'pipe'] });
  children.push(server);
  const serverLog = fs.createWriteStream(path.join(runDir, 'server.log'));
  server.stdout.pipe(serverLog);
  server.stderr.pipe(serverLog);
  await waitFor(`${baseUrl}/api/server`, { label: 'wren tv server' });

  const login = await api('POST', '/api/auth/login', { username: user, password });
  if (login.status !== 200 || !login.payload?.token) fail(`sign-in failed: ${login.status} ${JSON.stringify(login.payload)}`);
  token = login.payload.token;
  log('signed in as admin');

  // The day's real events, once the scoreboard has loaded.
  let hub = null;
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const res = await api('GET', '/api/sports/hub');
    if (res.status === 200 && (res.payload?.events || []).length) {
      hub = res.payload;
      break;
    }
    await new Promise((resolve) => setTimeout(resolve, 1500));
  }
  if (!hub) fail('no sports events appeared: this run needs provider data');
  const events = (hub.events || []).filter((event) => eventName(event));
  if (!events.length) fail('the hub listed no named events');
  // Both providers carry the same events, and at least one hub event is left out
  // of both so the empty answer is a real one rather than a guess. Live and
  // scheduled events are carried first: they are the ones whose Watch panel
  // shows a matched channel and a matched provider at the same time.
  const ordered = [...events].sort((a, b) => (a.status === 'final' ? 1 : 0) - (b.status === 'final' ? 1 : 0));
  const carried = ordered.slice(0, Math.min(5, events.length - 1));
  if (!carried.length) fail('the hub needs at least two events to split carried and empty');
  if (!carried.some((event) => event.status !== 'final')) {
    fail('every event on the board has finished: the SportsHub Watch panel regression needs a live or scheduled one');
  }
	writePlaylist(carried);
	const listed = carried.map((event, index) => ({ id: `gate-event-${index}`, name: eventName(event) }));
  const posted = await fetch(`${fixtureBase}/control/events`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(listed),
  });
  if (!posted.ok) fail(`the fixture did not accept the event listings: ${posted.status}`);
  log(`provider lists ${listed.length} of ${events.length} events`);

  const refresh = await api('POST', '/api/admin/livetv/refresh', {});
  if (refresh.status !== 202 && refresh.status !== 200) {
    fail(`Live TV refresh failed: ${refresh.status} ${JSON.stringify(refresh.payload)}`);
  }
  let providerEvents = [];
  for (let attempt = 0; attempt < 60 && providerEvents.length < listed.length; attempt += 1) {
    const channels = await api('GET', '/api/livetv/channels');
    providerEvents = channels.payload?.events || [];
    if (providerEvents.length < listed.length) await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  if (providerEvents.length < listed.length) {
    fail(`the event provider never reached Live TV: ${JSON.stringify(providerEvents)}`);
  }
  log(`Live TV lists ${providerEvents.length} provider events`);

  log('running Playwright against the deployment');
  // Two specs against one deployment: the Browse Live TV first-click regression,
  // and the SportsHub Watch panel regression that needs an event carried by both
  // a playlist channel and the provider (which this deployment writes).
  const playwright = spawnSync('npx', ['playwright', 'test', '-c', 'e2e/playwright.config.js', 'e2e/browse-livetv.spec.js', 'e2e/sports-watch.spec.js', '--reporter=list'], {
    cwd: root,
    stdio: ['ignore', 'pipe', 'pipe'],
    encoding: 'utf8',
    env: {
      ...process.env,
      WRENTV_BASE_URL: baseUrl,
      WRENTV_E2E_USER: user,
      WRENTV_E2E_PASSWORD: password,
      WRENTV_FIXTURE_URL: fixtureBase,
      PLAYWRIGHT_HTML_REPORT: path.join(runDir, 'report'),
    },
  });
  const output = `${playwright.stdout || ''}${playwright.stderr || ''}`;
  process.stdout.write(output);
  fs.writeFileSync(path.join(runDir, 'playwright-run.txt'), output);
  fs.writeFileSync(path.join(runDir, 'playwright-status.txt'), `exit=${playwright.status}\n`);
  cleanup();

  if (playwright.status !== 0) fail(`Playwright exited ${playwright.status}`);
  log('PASS');
}

main().catch((err) => fail(err.stack || String(err)));
