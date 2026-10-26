// Runs the sports deployment gate against a real Wren TV server.
//
// It builds the server, writes a throwaway data directory, seeds a local admin,
// starts the fixture addon/provider, generates a Live TV playlist from the
// day's real events, and then runs Playwright against that deployment. No API
// is mocked: every assertion goes through the running app.
//
// Usage: node e2e/run-gate.mjs
//   WRENTV_GATE_DATA   data directory (default /tmp/wrentv-gate)
//   WRENTV_GATE_PORT   server port (default 8099)
//   WRENTV_GATE_USER   admin username (default gate)
//   WRENTV_GATE_PASS   admin password (default gate-password)
//
// Docker is optional. Without a Docker engine this is still a real server
// deployment (built from this source, real ESPN data, real sessions), but it is
// not the container-image run the release gate requires.
import { spawn, spawnSync } from 'node:child_process';
import fs from 'node:fs';
import path from 'node:path';
import process from 'node:process';

const root = path.resolve(import.meta.dirname, '..');
const data = process.env.WRENTV_GATE_DATA || '/tmp/wrentv-gate';
const port = Number(process.env.WRENTV_GATE_PORT || 8099);
const user = process.env.WRENTV_GATE_USER || 'gate';
const password = process.env.WRENTV_GATE_PASS || 'gate-password';
const baseUrl = `http://127.0.0.1:${port}`;
const fixturePort = 8391;
const fixtureBase = `http://127.0.0.1:${fixturePort}`;
const evidence = path.join(root, 'docs/sports-hub/evidence/gate-run');
const mediaDir = path.join(data, 'fixture-media');
const playlistFile = path.join(data, 'fixture-tv.m3u');
const guideFile = path.join(data, 'fixture-guide.xml');

const children = [];
let token = '';

function log(...args) {
  console.log('[gate]', ...args);
}

function fail(message) {
  console.error(`[gate] ${message}`);
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

function prepareMedia() {
  fs.mkdirSync(mediaDir, { recursive: true });
  run('ffmpeg', [
    '-hide_banner', '-loglevel', 'error', '-y',
    '-f', 'lavfi', '-i', 'testsrc2=size=640x360:rate=25',
    '-f', 'lavfi', '-i', 'sine=frequency=440:sample_rate=48000',
    '-t', '6', '-c:v', 'libx264', '-preset', 'veryfast', '-pix_fmt', 'yuv420p', '-g', '25',
    '-c:a', 'aac', '-b:a', '96k',
    '-f', 'hls', '-hls_time', '2', '-hls_list_size', '0',
    '-hls_segment_filename', path.join(mediaDir, 'seg%d.ts'),
    path.join(mediaDir, 'index.m3u8'),
  ]);
  if (!fs.existsSync(path.join(mediaDir, 'index.m3u8'))) fail('fixture media was not produced');
  // A second stream publishes two audio renditions and one subtitle rendition,
  // so the player's track menus can be exercised against real HLS rather than
  // a mocked track list. Renditions are separate playlists, as a provider's
  // master would deliver them.
  run('ffmpeg', [
    '-hide_banner', '-loglevel', 'error', '-y',
    '-f', 'lavfi', '-i', 'testsrc2=size=640x360:rate=25',
    '-t', '6', '-an', '-c:v', 'libx264', '-preset', 'veryfast', '-pix_fmt', 'yuv420p', '-g', '25',
    '-f', 'hls', '-hls_time', '2', '-hls_list_size', '0',
    '-hls_segment_filename', path.join(mediaDir, 'multi-v%d.ts'),
    path.join(mediaDir, 'multi-video.m3u8'),
  ]);
  for (const [name, frequency] of [['en', '440'], ['es', '880']]) {
    run('ffmpeg', [
      '-hide_banner', '-loglevel', 'error', '-y',
      '-f', 'lavfi', '-i', `sine=frequency=${frequency}:sample_rate=48000`,
      '-t', '6', '-c:a', 'aac', '-b:a', '96k',
      '-f', 'hls', '-hls_time', '2', '-hls_list_size', '0',
      '-hls_segment_filename', path.join(mediaDir, `multi-${name}%d.ts`),
      path.join(mediaDir, `multi-${name}.m3u8`),
    ]);
  }
  fs.writeFileSync(
    path.join(mediaDir, 'multi.m3u8'),
    ['#EXTM3U',
      '#EXT-X-VERSION:3',
      '#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="English",LANGUAGE="en",DEFAULT=YES,AUTOSELECT=YES,URI="multi-en.m3u8"',
      '#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="aud",NAME="Spanish",LANGUAGE="es",DEFAULT=NO,AUTOSELECT=YES,URI="multi-es.m3u8"',
      '#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID="subs",NAME="English",LANGUAGE="en",DEFAULT=NO,AUTOSELECT=NO,URI="multi-subs.m3u8"',
      '#EXT-X-STREAM-INF:BANDWIDTH=800000,RESOLUTION=640x360,AUDIO="aud",SUBTITLES="subs"',
      'multi-video.m3u8',
    ].join('\n') + '\n',
  );
  fs.writeFileSync(path.join(mediaDir, 'multi-subs0.vtt'), 'WEBVTT\n\n00:00.000 --> 00:05.000\nFixture subtitle\n');
  fs.writeFileSync(
    path.join(mediaDir, 'multi-subs.m3u8'),
    ['#EXTM3U',
      '#EXT-X-VERSION:3',
      '#EXT-X-TARGETDURATION:6',
      '#EXT-X-MEDIA-SEQUENCE:0',
      '#EXT-X-PLAYLIST-TYPE:VOD',
      '#EXTINF:6.0,',
      'multi-subs0.vtt',
      '#EXT-X-ENDLIST',
    ].join('\n') + '\n',
  );
  for (const name of ['multi.m3u8', 'multi-video.m3u8', 'multi-en.m3u8', 'multi-es.m3u8', 'multi-subs.m3u8']) {
    if (!fs.existsSync(path.join(mediaDir, name))) fail(`fixture rendition ${name} was not produced`);
  }
}

function writeConfig() {
  const config = `server_name: Gate Server
listen: 127.0.0.1:${port}
remote_access:
  enabled: false
  upnp: false
auth:
  supabase:
    # A pre-claimed identity keeps the app on the sign-in screen instead of the
    # claim flow; the gate signs in with a local admin account.
    server_id: gate-local
    server_secret: gate-local
transcode:
  ffmpeg_path: ${spawnSync('which', ['ffmpeg']).stdout.toString().trim() || 'ffmpeg'}
  ffprobe_path: ${spawnSync('which', ['ffprobe']).stdout.toString().trim() || 'ffprobe'}
  hwaccel: none
scanner:
  watch: false
  interval_min: 0
sports:
  enabled: true
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
streaming:
  enabled: false
  addons:
    - name: Gate Fixture
      url: ${fixtureBase}/addon/manifest.json
      enabled: true
    - name: Gate Fixture Broken
      url: ${fixtureBase}/addon-broken/manifest.json
      enabled: true
`;
  fs.writeFileSync(path.join(data, 'wrentv.yaml'), config);
}

// The playlist names a channel after each event's away team, so the ordinary
// matcher can promote a real feed and the docked preview is reachable from a
// real sports card.
function writePlaylist(hub) {
  // Hub events carry a title and participants (the scoreboard card shape), not
  // home/away team objects.
  const nameOf = (event) =>
    (event.title || (event.participants || []).map((participant) => participant.name).filter(Boolean).join(' vs ')).trim();
  const events = (hub.events || []).filter((event) => nameOf(event));
  const lines = ['#EXTM3U'];
  const guide = [
    '<?xml version="1.0" encoding="UTF-8"?>',
    '<tv generator-info-name="gate">',
  ];
  const now = new Date();
  // XMLTV wants "20261016200000 +0000", not ISO's "20261016T200000": the
  // server parses the standard form, so a T here silently yields an empty guide.
  const stamp = (date) => `${date.toISOString().slice(0, 19).replace(/[-:]/g, '').replace('T', '')} +0000`;
  const start = new Date(now.getTime() - 60 * 60 * 1000);
  const stop = new Date(now.getTime() + 6 * 60 * 60 * 1000);
  const later = new Date(now.getTime() + 4 * 24 * 60 * 60 * 1000);
  events.forEach((event, index) => {
    const name = nameOf(event);
    // Two feeds per event: the race between them is what keeps a lone strong
    // feed from auto-playing, so the chooser (and its Preview control) is the
    // path the gate exercises.
    for (const variant of ['', ' Alt']) {
      const channel = `gate${index}${variant ? 'b' : 'a'}`;
      const display = `${name}${variant}`;
      lines.push(`#EXTINF:-1 tvg-id="${channel}" tvg-name="${display}" group-title="Sports",${display}`);
      lines.push('#EXTVLCOPT:http-user-agent=VLC/3.0.20');
      lines.push(`${fixtureBase}/media/open/index.m3u8`);
      guide.push(`  <channel id="${channel}"><display-name>${display}</display-name></channel>`);
      // A programme four days out proves the seven-day search horizon.
      guide.push(
        `  <programme start="${stamp(start)}" stop="${stamp(stop)}" channel="${channel}"><title>${display} Live</title><desc>Fixture coverage</desc></programme>`,
      );
      guide.push(
        `  <programme start="${stamp(later)}" stop="${stamp(new Date(later.getTime() + 3 * 60 * 60 * 1000))}" channel="${channel}"><title>${display} Weekend Fixture</title></programme>`,
      );
    }
  });
  // Two standalone channels back the track-menu test and are deliberately not
  // named after an event, so they never enter sports matching: one publishes
  // alternate audio and subtitles, the other publishes a single rendition.
  for (const [channel, display, source] of [
    ['gate-audio', 'Gate Audio Tracks', `${fixtureBase}/media/open/multi.m3u8`],
    ['gate-single', 'Gate Single Audio', `${fixtureBase}/media/open/index.m3u8`],
  ]) {
    lines.push(`#EXTINF:-1 tvg-id="${channel}" tvg-name="${display}" group-title="Gate",${display}`);
    lines.push(source);
  }
  guide.push('</tv>');
  fs.writeFileSync(playlistFile, `${lines.join('\n')}\n`);
  fs.writeFileSync(guideFile, `${guide.join('\n')}\n`);
  log(`playlist has ${events.length} sports channels`);
}

// ---------------------------------------------------------------------- main

async function main() {
  fs.rmSync(data, { recursive: true, force: true });
  fs.mkdirSync(path.join(data, 'fixture'), { recursive: true });
  // Evidence is this run's, not a mix with earlier attempts.
  fs.rmSync(evidence, { recursive: true, force: true });
  fs.mkdirSync(evidence, { recursive: true });
  log('data directory', data);

  log('building the server (same flags as the Dockerfile)');
  run('go', ['build', '-trimpath', '-ldflags', '-s -w', '-o', path.join(data, 'wrentv'), './cmd/wrentv'], {
    env: { ...process.env, PATH: `${process.env.PATH}:/usr/local/go/bin`, CGO_ENABLED: '0' },
  });

  writeConfig();
  log('seeding the gate admin account');
  run('go', ['run', './e2e/seed', '-data', data, '-user', user, '-password', password], {
    env: { ...process.env, PATH: `${process.env.PATH}:/usr/local/go/bin` },
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
  await waitFor(`${fixtureBase}/addon/manifest.json`, { label: 'fixture addon' });

  log('starting the server binary');
  const server = spawn(path.join(data, 'wrentv'), ['-data', data], { cwd: root, stdio: ['ignore', 'pipe', 'pipe'] });
  children.push(server);
  const serverLog = fs.createWriteStream(path.join(evidence, 'server.log'));
  server.stdout.pipe(serverLog);
  server.stderr.pipe(serverLog);
  await waitFor(`${baseUrl}/api/server`, { label: 'wren tv server' });

  const login = await api('POST', '/api/auth/login', { username: user, password });
  if (login.status !== 200 || !login.payload?.token) fail(`sign-in failed: ${login.status} ${JSON.stringify(login.payload)}`);
  token = login.payload.token;
  log('signed in as admin');

  // Wait for the first scoreboard refresh, then name a channel after each game.
  let hub = null;
  for (let attempt = 0; attempt < 40; attempt += 1) {
    const res = await api('GET', '/api/sports/hub');
    if (res.status === 200 && (res.payload?.events || []).length) {
      hub = res.payload;
      break;
    }
    await new Promise((resolve) => setTimeout(resolve, 1500));
  }
  if (!hub) fail('no sports events appeared: the gate needs provider data');
  writePlaylist(hub);

  // Refresh is asynchronous (202) and the playlist is fetched in the background.
  const refresh = await api('POST', '/api/admin/livetv/refresh', {});
  if (refresh.status !== 202 && refresh.status !== 200) {
    fail(`Live TV refresh failed: ${refresh.status} ${JSON.stringify(refresh.payload)}`);
  }
  let channelList = [];
  for (let attempt = 0; attempt < 40 && !channelList.length; attempt += 1) {
    const channels = await api('GET', '/api/livetv/channels');
    channelList = channels.payload?.channels || [];
    if (!channelList.length) await new Promise((resolve) => setTimeout(resolve, 1000));
  }
  if (!channelList.length) fail('the fixture playlist produced no channels');
  log(`Live TV has ${channelList.length} channels, ${(hub.events || []).length} events`);

  // The playlist-channel quality route is a separate code path from addon
  // probing: measure one generated channel with real ffprobe before the browser
  // run, so a failure here is reported as a server problem, not a UI one.
  const quality = await api('POST', '/api/sports/streams/quality', { channelId: channelList[0].id });
  if (quality.status !== 200 || quality.payload?.height !== 360 || quality.payload?.width !== 640) {
    fail(`playlist channel quality = ${quality.status} ${JSON.stringify(quality.payload)}`);
  }
  fs.writeFileSync(path.join(evidence, 'quality-playlist.json'), JSON.stringify(quality.payload, null, 2));
  log(`playlist channel quality: ${quality.payload.width}x${quality.payload.height} ${quality.payload.codec} ${quality.payload.frameRate}fps`);

  log('running Playwright against the deployment');
  const playwright = spawnSync('npx', ['playwright', 'test', '-c', 'e2e/playwright.config.js', '--reporter=list,json'], {
    cwd: root,
    stdio: ['ignore', 'pipe', 'pipe'],
    encoding: 'utf8',
    env: {
      ...process.env,
      WRENTV_BASE_URL: baseUrl,
      WRENTV_E2E_USER: user,
      WRENTV_E2E_PASSWORD: password,
      WRENTV_FIXTURE_URL: fixtureBase,
      PLAYWRIGHT_HTML_REPORT: path.join('/tmp', 'wrentv-gate-report'),
      PLAYWRIGHT_JSON_OUTPUT_NAME: path.join(evidence, 'playwright-report.json'),
    },
  });
  const output = `${playwright.stdout || ''}${playwright.stderr || ''}`;
  process.stdout.write(output);
  fs.writeFileSync(path.join(evidence, 'playwright-run.txt'), output);
  fs.writeFileSync(path.join(evidence, 'playwright-status.txt'), `exit=${playwright.status}\n`);
  copyScreenshots();
  cleanup();

  if (playwright.status !== 0) fail(`Playwright exited ${playwright.status}`);
  log('gate passed');
}

// Failure artifacts (and the screenshots the addon test always takes) are copied
// next to the run log so the evidence does not depend on a /tmp directory.
function copyScreenshots() {
  const results = path.join(root, 'test-results');
  if (!fs.existsSync(results)) return;
  for (const entry of fs.readdirSync(results)) {
    const dir = path.join(results, entry);
    if (!fs.statSync(dir).isDirectory()) continue;
    for (const file of fs.readdirSync(dir)) {
      if (!file.endsWith('.png')) continue;
      fs.copyFileSync(path.join(dir, file), path.join(evidence, `${entry}-${file}`));
    }
  }
}

main().catch((err) => fail(err.stack || String(err)));
