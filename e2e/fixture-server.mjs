// Development fixture for the sports deployment gate.
//
// One dependency-free HTTP server plays three parts:
//   - a Stremio addon with two searchable tv catalogues and stream links whose
//     proxyHeaders ask for a referer and an origin (and also offer a Cookie and
//     an API key, which the server must never forward);
//   - a provider that only serves media when the referer and origin match, so a
//     forwarded identity is observable rather than assumed;
//   - a Live TV playlist/guide whose channels are rewritten by the gate so the
//     docked preview can be exercised against real sports events.
//
// It is a test harness, not part of the server or its image.
import http from 'node:http';
import fs from 'node:fs';
import path from 'node:path';

const PORT = Number(process.env.FIXTURE_PORT || 8391);
const MEDIA_DIR = process.env.FIXTURE_MEDIA_DIR || '';
const PLAYLIST_FILE = process.env.FIXTURE_PLAYLIST || '';
const GUIDE_FILE = process.env.FIXTURE_GUIDE || '';
const REFERER = process.env.FIXTURE_REFERER || 'https://fixture.example/watch';
const ORIGIN = process.env.FIXTURE_ORIGIN || 'https://fixture.example';
const BASE = `http://127.0.0.1:${PORT}`;

// Every media request is recorded with the headers it arrived with, so the gate
// can prove which identity reached the provider and that credentials did not.
const mediaRequests = [];

function record(req) {
  mediaRequests.push({
    path: req.url,
    userAgent: req.headers['user-agent'] || '',
    referer: req.headers.referer || '',
    origin: req.headers.origin || '',
    cookie: req.headers.cookie || '',
    apiKey: req.headers['x-api-key'] || '',
    at: new Date().toISOString(),
  });
}

function json(res, body, status = 200) {
  const payload = JSON.stringify(body);
  res.writeHead(status, { 'Content-Type': 'application/json', 'Content-Length': Buffer.byteLength(payload) });
  res.end(payload);
}

function file(res, target, type) {
  if (!target || !fs.existsSync(target)) {
    res.writeHead(404).end('missing fixture file');
    return;
  }
  res.writeHead(200, { 'Content-Type': type, 'Cache-Control': 'no-store' });
  fs.createReadStream(target).pipe(res);
}

// The gated provider refuses anonymous requests: without the browser identity
// the addon promised, nothing is served. This is what makes the header assertion
// meaningful. Open media backs the Live TV playlist, which carries only a
// user-agent, so channel playback is not part of that assertion.
function media(req, res, filePath) {
  record(req);
  const gated = filePath.startsWith('/media/gated/');
  if (gated && (req.headers.referer !== REFERER || req.headers.origin !== ORIGIN)) {
    res.writeHead(403, { 'Content-Type': 'text/plain' });
    res.end('browser identity required');
    return;
  }
  if (filePath.endsWith('.m3u8') && filePath.includes('broken')) {
    res.writeHead(500, { 'Content-Type': 'text/plain' });
    res.end('provider failure');
    return;
  }
  const type = filePath.endsWith('.m3u8')
    ? 'application/vnd.apple.mpegurl'
    : filePath.endsWith('.vtt') ? 'text/vtt' : 'video/mp2t';
  file(res, path.join(MEDIA_DIR, path.basename(filePath)), type);
}

const addonHeaders = {
  Referer: REFERER,
  Origin: ORIGIN,
  // A distinctive player identity proves the addon's User-Agent reaches both the
  // proxy and the probe, rather than each falling back to its own default.
  'User-Agent': 'GateProbe/1.0',
  // Credentials: the server must drop these instead of forwarding them.
  Cookie: 'sid=gate-secret',
  'X-Api-Key': 'gate-secret',
};

function streamBody(mediaPath) {
  return {
    streams: [
      {
        url: `${BASE}${mediaPath}`,
        name: 'Fixture HD',
        behaviorHints: { proxyHeaders: { request: addonHeaders } },
      },
    ],
  };
}

const manifests = {
  '/addon/manifest.json': [
    { type: 'tv', id: 'sports-live', name: 'Sports live', extra: [{ name: 'search' }] },
    { type: 'tv', id: 'sports-backup', name: 'Sports backup', extra: [{ name: 'search' }] },
    // Replay shelves must never be queried by discovery.
    { type: 'tv', id: 'replay', name: 'Replay archive' },
  ],
  // A second addon, so a failing provider can be tested without making the
  // healthy source the only choice.
  '/addon-broken/manifest.json': [
    { type: 'tv', id: 'sports-broken', name: 'Sports broken', extra: [{ name: 'search' }] },
  ],
};

// The event provider is a manifest of its own. It lists events rather than
// channels, so a Live TV source can be given real sports listings without
// changing what the tv addons above declare.
const eventManifest = {
  id: 'gate.events',
  version: '1.0.0',
  name: 'Gate Events',
  description: 'Event provider',
  types: ['sport'],
  resources: [
    { name: 'catalog', types: ['sport'] },
    { name: 'meta', types: ['sport'] },
    { name: 'stream', types: ['sport'] },
  ],
  catalogs: [{ type: 'sport', id: 'gate-events', name: 'Gate events', extra: [{ name: 'search' }] }],
};

// eventItems is the day's real events, posted by the runner after the scoreboard
// has loaded. Before that the provider lists nothing, which is a real empty
// shelf rather than an error.
let eventItems = [];

// The event provider can be switched into refusing this server for a while: its
// stream endpoint then answers 429 with Retry-After. That lets the gate watch the
// honest rate-limit message and count how many times the addon was asked, which
// is how a retry storm would show up. Cleared again by the spec that sets it.
let rateLimitSeconds = 0;
let rateLimitHits = 0;

// What the event provider offers for one item:
//   ok       one feed that starts,
//   fallback a feed that 404s in front of it,
//   broken   only the feed that 404s.
// Automatic playback and the docked preview must both fall back to the next feed
// and both report the honest failure when there is none. The dead path is a real
// missing file, so the server's own check is what rejects it.
const DEAD_MEDIA = '/media/open/missing.m3u8';
let eventFeedMode = 'ok';

function eventStreamBody() {
  const good = { url: `${BASE}/media/open/index.m3u8`, name: 'Fixture HD', behaviorHints: { proxyHeaders: { request: addonHeaders } } };
  // The dead feed advertises a higher quality than the working one, so the
  // ranking the server already does puts it first: the fallback is exercised by
  // the feed a viewer would otherwise have been shown.
  const dead = { url: `${BASE}${DEAD_MEDIA}`, name: 'Fixture 1080p', behaviorHints: { proxyHeaders: { request: addonHeaders } } };
  if (eventFeedMode === 'broken') return { streams: [dead] };
  if (eventFeedMode === 'fallback') return { streams: [dead, good] };
  return { streams: [good] };
}

const server = http.createServer((req, res) => {
  const url = new URL(req.url, BASE);
  const p = decodeURIComponent(url.pathname);

  if (p === '/control/events') {
    if (req.method !== 'POST') {
      json(res, { error: 'POST the event names to list' }, 405);
      return;
    }
    let body = '';
    req.on('data', (chunk) => { body += chunk; });
    req.on('end', () => {
      let parsed = [];
      try {
        parsed = JSON.parse(body || '[]');
      } catch {
        parsed = [];
      }
      eventItems = (Array.isArray(parsed) ? parsed : []).slice(0, 40).map((entry, index) => ({
        id: entry.id || `gate-event-${index}`,
        type: 'sport',
        name: String(entry.name || ''),
        description: `${entry.name || ''} fixture listing`,
        genres: ['Sports'],
        releaseInfo: 'LIVE',
      })).filter((item) => item.name);
      json(res, { events: eventItems.length });
    });
    return;
  }

  if (p === '/control/rate-limit') {
    if (req.method !== 'POST') {
      json(res, { seconds: rateLimitSeconds, hits: rateLimitHits });
      return;
    }
    let body = '';
    req.on('data', (chunk) => { body += chunk; });
    req.on('end', () => {
      let parsed = {};
      try {
        parsed = JSON.parse(body || '{}');
      } catch {
        parsed = {};
      }
      // Setting or clearing the refusal restarts the count, so a spec can say
      // exactly how many requests one attempt made.
      rateLimitSeconds = Math.max(0, Number(parsed.seconds || 0));
      rateLimitHits = 0;
      json(res, { seconds: rateLimitSeconds, hits: rateLimitHits });
    });
    return;
  }

  if (p === '/control/event-feeds') {
    if (req.method !== 'POST') {
      json(res, { mode: eventFeedMode });
      return;
    }
    let body = '';
    req.on('data', (chunk) => { body += chunk; });
    req.on('end', () => {
      let parsed = {};
      try {
        parsed = JSON.parse(body || '{}');
      } catch {
        parsed = {};
      }
      const mode = String(parsed.mode || '');
      if (['ok', 'fallback', 'broken'].includes(mode)) eventFeedMode = mode;
      json(res, { mode: eventFeedMode });
    });
    return;
  }

  if (p === '/addon-events/manifest.json') {
    json(res, eventManifest);
    return;
  }
  const eventCatalog = p.match(/^\/addon-events\/catalog\/sport\/gate-events(?:\/search=(.+))?\.json$/);
  if (eventCatalog) {
    const query = (eventCatalog[1] || '').trim().toLowerCase();
    const metas = query
      ? eventItems.filter((item) => query.split(/\s+/).filter(Boolean).every((word) => item.name.toLowerCase().includes(word)))
      : eventItems;
    json(res, { metas });
    return;
  }
  const eventMeta = p.match(/^\/addon-events\/meta\/sport\/(.+)\.json$/);
  if (eventMeta) {
    const meta = eventItems.find((item) => item.id === eventMeta[1]);
    json(res, meta ? { meta } : { meta: null }, meta ? 200 : 404);
    return;
  }
  if (p.match(/^\/addon-events\/stream\/sport\/(.+)\.json$/)) {
    if (rateLimitSeconds > 0) {
      rateLimitHits += 1;
      res.writeHead(429, { 'Content-Type': 'text/plain', 'Retry-After': String(rateLimitSeconds) });
      res.end('slow down');
      return;
    }
    json(res, eventStreamBody());
    return;
  }

  if (manifests[p]) {
    json(res, {
      id: p === '/addon/manifest.json' ? 'gate.fixture' : 'gate.fixture.broken',
      version: '1.0.0',
      name: p === '/addon/manifest.json' ? 'Gate Fixture' : 'Gate Fixture Broken',
      catalogs: manifests[p],
    });
    return;
  }

  // The catalogue echoes the searched team so the ordinary matcher can score it;
  // a second, unrelated item proves discovery filters.
  const catalogue = p.match(/^\/addon(?:-broken)?\/catalog\/tv\/(sports-live|sports-backup|sports-broken)\/search=(.+)\.json$/);
  if (catalogue) {
    const [, id, query] = catalogue;
    const metaId = { 'sports-live': 'live-primary', 'sports-backup': 'live-alt', 'sports-broken': 'live-broken' }[id];
    const suffix = id === 'sports-broken' ? ' broken' : id === 'sports-backup' ? ' alt' : ' live';
    json(res, {
      metas: [
        { id: metaId, type: 'tv', name: `${query}${suffix}`, description: `${query} fixture feed` },
        { id: 'concert', type: 'tv', name: 'Unrelated concert special', description: 'not this event' },
      ],
    });
    return;
  }

  if (p === '/addon/stream/tv/live-primary.json' || p === '/addon/stream/tv/live-alt.json') {
    json(res, streamBody('/media/gated/index.m3u8'));
    return;
  }
  if (p === '/addon-broken/stream/tv/live-broken.json') {
    // This provider fails after tuning, so the gate can watch a source fail and
    // verify the session is released with an honest message.
    json(res, streamBody('/media/gated/broken.m3u8'));
    return;
  }

  if (p.startsWith('/media/')) {
    media(req, res, p);
    return;
  }

  if (p === '/tv.m3u') {
    file(res, PLAYLIST_FILE, 'application/vnd.apple.mpegurl');
    return;
  }
  if (p === '/guide.xml') {
    file(res, GUIDE_FILE, 'application/xml');
    return;
  }
  if (p === '/_media-requests') {
    json(res, mediaRequests);
    return;
  }
  res.writeHead(404).end('not found');
});

server.listen(PORT, '127.0.0.1', () => {
  console.log(`fixture listening on ${BASE}`);
});
