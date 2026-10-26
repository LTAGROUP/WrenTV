// SportsHub Watch panel regression: a Stremio IPTV entry must offer the same
// Preview and Watch controls as a conventional IPTV channel, and each control
// must start the flow its source needs. The run is a real server, a real
// scoreboard and a fixture provider; nothing is mocked.
//
// `run-browse-livetv.mjs` deploys the shape this needs: a playlist channel named
// after each event it carries and the event provider listing the same events, so
// one event page shows both kinds of row at once.
import { test, expect } from '@playwright/test';

const USER = process.env.WRENTV_E2E_USER;
const PASS = process.env.WRENTV_E2E_PASSWORD;

test.describe.configure({ mode: 'serial' });
test.skip(!USER || !PASS, 'Set WRENTV_E2E_USER and WRENTV_E2E_PASSWORD for a real deployment run.');

const fixtureUrl = () => process.env.WRENTV_FIXTURE_URL || 'http://127.0.0.1:8392';

async function authHeaders(page) {
  const token = await page.evaluate(() => JSON.parse(localStorage.getItem('wrentv.session') || '{}').token || '');
  return { Authorization: `Bearer ${token}` };
}

async function signIn(page) {
  await page.goto('/');
  await page.locator('input[name=email], input[name=username]').first().waitFor();
  if (!(await page.locator('input[name=username]').count())) {
    await page.getByText('Sign in with it').click();
    await page.locator('input[name=username]').waitFor();
  }
  await page.locator('input[name=username]').fill(USER);
  await page.locator('input[name=password]').fill(PASS);
  await page.getByRole('button', { name: /sign in/i }).click();
  await expect(page.getByRole('link', { name: 'Sports' })).toBeVisible();
}

// The session count the server reports is the only honest measure of whether a
// preview or a player left a provider connection behind.
async function playing(page, headers) {
  const admin = await (await page.request.get('/api/admin/livetv', { headers })).json();
  return admin?.liveTv?.playing || 0;
}

async function stopAnyPreview(page) {
  const stop = page.locator('.docked-preview').getByRole('button', { name: 'Stop' });
  if (await stop.count()) {
    await stop.click();
    await expect(page.locator('.docked-preview')).toHaveCount(0);
  }
}

// Every /api/ request the page makes from here on, so each button can be shown
// to invoke its own playback flow rather than merely looking like it did.
function recordApi(page) {
  const urls = [];
  const onRequest = (request) => {
    const path = new URL(request.url()).pathname;
    if (path.startsWith('/api/')) urls.push(path);
  };
  page.on('request', onRequest);
  return { urls, stop: () => page.off('request', onRequest) };
}

async function setFixtureFeeds(page, mode) {
  const res = await page.request.post(`${fixtureUrl()}/control/event-feeds`, { data: { mode } });
  expect(res.ok()).toBeTruthy();
  return res.json();
}

async function setFixtureRateLimit(page, seconds) {
  const res = await page.request.post(`${fixtureUrl()}/control/rate-limit`, { data: { seconds } });
  expect(res.ok()).toBeTruthy();
  return res.json();
}

async function fixtureRateLimitState(page) {
  const res = await page.request.get(`${fixtureUrl()}/control/rate-limit`);
  expect(res.ok()).toBeTruthy();
  return res.json();
}

async function mediaRequests(page) {
  const res = await page.request.get(`${fixtureUrl()}/_media-requests`);
  expect(res.ok()).toBeTruthy();
  return res.json();
}

// The deployment carries the live and scheduled events of the board in play.
// Near midnight that is the next day's board, so the search covers the same
// window the hub's own date rail allows.
const isoDate = (offsetDays) => new Date(Date.now() + offsetDays * 86_400_000).toISOString().slice(0, 10);
const HUB_DATES = ['', isoDate(1), isoDate(2)];

// openEventWithBothKinds asks the server which live event has a matched playlist
// channel and exactly one matched Stremio item, opens its Watch panel and
// returns both rows plus the identities behind them. The deployment guarantees
// one, so a miss is a deployment failure rather than a skip.
async function openEventWithBothKinds(page) {
  const headers = await authHeaders(page);
  const seen = [];
  for (const date of HUB_DATES) {
    const hubRes = await page.request.get(`/api/sports/hub${date ? `?date=${date}` : ''}`, { headers });
    if (!hubRes.ok()) continue;
    const hub = await hubRes.json();
    for (const event of hub.events || []) {
      // A finished event opens the replay search instead of the live channel
      // preview this spec is about.
      if (event.status === 'final') {
        seen.push(`${event.id}: final`);
        continue;
      }
      const streamsRes = await page.request.get(`/api/sports/events/${encodeURIComponent(event.id)}/streams`, { headers });
      if (!streamsRes.ok()) {
        seen.push(`${event.id}: streams ${streamsRes.status()}`);
        continue;
      }
      const streams = await streamsRes.json();
      const providers = await (await page.request.get(
        `/api/sports/events/${encodeURIComponent(event.id)}/providers?mode=live`, { headers },
      )).json();
      const groups = streams.groups || [];
      const matches = providers.matches || [];
      seen.push(`${event.id}:${date || 'today'}:${event.status} groups=${groups.length} matches=${matches.length}`);
      // One match, so the row this spec clicks is unambiguous.
      if (!groups.length || matches.length !== 1) continue;
      await page.goto(`/sports/event/${encodeURIComponent(event.id)}`);
      await page.getByRole('button', { name: /^(Watch|Search for Replay)$/ }).click();
      await expect(page.locator('.watch-panel')).toBeVisible();
      const channelRow = page.locator('.watch-panel .stream-row', { hasText: 'Fixture TV' }).first();
      const providerRow = page.locator('.watch-panel .stream-row', { hasText: 'Gate Events' }).first();
      await expect(channelRow).toBeVisible({ timeout: 30_000 });
      await expect(providerRow).toBeVisible({ timeout: 30_000 });
      // The channel this row belongs to, by the name the row shows.
      const channelName = (await channelRow.locator('.stream-name').textContent()).trim();
      const group = groups.find((entry) => (entry.displayName || entry.primary?.channelName) === channelName);
      if (!group) continue;
      return { event, headers, group, match: matches[0], channelRow, providerRow };
    }
  }
  throw new Error(`no live event has both a matched channel and a matched Stremio item: the deployment must carry both (${seen.length} events: ${seen.join('; ')})`);
}

const playRoute = (match) => `/api/livetv/addons/${match.source}/item/${match.type}/${match.itemId}/play`;

test('a Stremio entry offers the same Preview and Watch controls as a channel', async ({ page }) => {
  await signIn(page);
  const { headers, group, match, channelRow, providerRow } = await openEventWithBothKinds(page);
  // A lone strong channel feed can play straight away; that preview is stopped
  // so the only sessions this test measures are the ones its clicks start.
  await stopAnyPreview(page);
  await expect.poll(() => playing(page, headers)).toBe(0);

  // The same two actions, in the same order, on both kinds of row.
  const actions = async (row) => (await row.locator('.stream-actions button').allTextContents()).map((text) => text.trim());
  expect(await actions(channelRow)).toEqual(['Preview', 'Watch']);
  expect(await actions(providerRow)).toEqual(['Preview', 'Watch']);

  const api = recordApi(page);
  // Preview on the Stremio entry: the same docked preview, tuned through the
  // addon item route with its source, type and item id, and one session.
  await providerRow.getByRole('button', { name: 'Preview' }).click();
  await expect(page.locator('.docked-preview video')).toBeVisible({ timeout: 30_000 });
  await expect(page.locator('.docked-preview .dp-title')).toHaveText(match.name);
  expect(api.urls).toContain(playRoute(match));
  expect(api.urls.filter((path) => path.startsWith('/api/livetv/channels/'))).toEqual([]);
  await expect.poll(() => playing(page, headers)).toBe(1);

  // Preview on the channel: the same control, tuned through the channel route.
  const channelApi = recordApi(page);
  await channelRow.getByRole('button', { name: 'Preview' }).click();
  await expect(page.locator('.docked-preview video')).toBeVisible({ timeout: 30_000 });
  expect(channelApi.urls).toContain(`/api/livetv/channels/${group.primary.channelId}/play`);
  expect(channelApi.urls.filter((path) => path.startsWith('/api/livetv/addons/'))).toEqual([]);
  // Switching kinds replaced the playing session instead of stacking one.
  await expect.poll(() => playing(page, headers)).toBe(1);

  // Watch on the Stremio entry: the full player, through the same addon route
  // (the preview session is released first), and still one session.
  const watchApi = recordApi(page);
  await providerRow.getByRole('button', { name: 'Watch' }).click();
  await expect(page.locator('.live-player video')).toBeVisible({ timeout: 30_000 });
  expect(watchApi.urls).toContain(playRoute(match));
  expect(watchApi.urls.filter((path) => path.startsWith('/api/livetv/channels/'))).toEqual([]);
  await expect.poll(() => playing(page, headers)).toBe(1);
  await page.keyboard.press('Escape');
  await expect(page.locator('.live-player')).toHaveCount(0, { timeout: 20_000 });
  await expect.poll(() => playing(page, headers)).toBe(0);

  // Watch on the channel: the ordinary channel session, one at a time.
  const channelWatch = recordApi(page);
  await channelRow.getByRole('button', { name: 'Watch' }).click();
  await expect(page.locator('.live-player video')).toBeVisible({ timeout: 30_000 });
  expect(channelWatch.urls).toContain(`/api/livetv/channels/${group.primary.channelId}/play`);
  await expect.poll(() => playing(page, headers)).toBe(1);
  await page.keyboard.press('Escape');
  await expect(page.locator('.live-player')).toHaveCount(0, { timeout: 20_000 });
  await expect.poll(() => playing(page, headers)).toBe(0);

  watchApi.stop();
  channelApi.stop();
  channelWatch.stop();
  api.stop();
});

test('a Stremio Preview falls back to the next feed and reports one that cannot tune', async ({ page }) => {
  await signIn(page);
  const { headers, providerRow } = await openEventWithBothKinds(page);
  await stopAnyPreview(page);
  await expect.poll(() => playing(page, headers)).toBe(0);

  try {
    // The item now offers a feed that 404s in front of the working one. The
    // preview must replace it rather than settle on nothing, exactly as the
    // full player's automatic playback does.
    await setFixtureFeeds(page, 'fallback');
    const before = (await mediaRequests(page)).length;
    await providerRow.getByRole('button', { name: 'Preview' }).click();
    await expect(page.locator('.docked-preview video')).toBeVisible({ timeout: 30_000 });
    await expect.poll(() => playing(page, headers)).toBe(1);
    const during = (await mediaRequests(page)).slice(before);
    const dead = during.findIndex((request) => request.path.includes('missing.m3u8'));
    const working = during.findIndex((request) => request.path.includes('/media/open/index.m3u8'));
    expect(dead, 'the dead feed was tried').toBeGreaterThanOrEqual(0);
    expect(working, 'the working feed was played after it').toBeGreaterThan(dead);
    await page.locator('.docked-preview').getByRole('button', { name: 'Stop' }).click();
    await expect.poll(() => playing(page, headers)).toBe(0);

    // Nothing to fall back to: the preview says so, names no link and holds no
    // session.
    await setFixtureFeeds(page, 'broken');
    await providerRow.getByRole('button', { name: 'Preview' }).click();
    const error = page.locator('.watch-preview-slot .watch-error');
    await expect(error).toBeVisible({ timeout: 30_000 });
    await expect(error).not.toContainText(/https?:\/\//);
    await expect.poll(() => playing(page, headers)).toBe(0);
  } finally {
    await setFixtureFeeds(page, 'ok');
  }

  // A provider refusing this server is reported with the wait it asked for,
  // asked exactly once, and leaves no session behind.
  await setFixtureRateLimit(page, 30);
  try {
    await providerRow.getByRole('button', { name: 'Preview' }).click();
    const error = page.locator('.watch-preview-slot .watch-error');
    await expect(error).toBeVisible({ timeout: 30_000 });
    await expect(error).toContainText('rate limiting this server');
    await expect(error).toContainText('asked to wait 30s');
    expect((await fixtureRateLimitState(page)).hits, 'the preview asked the addon once').toBe(1);
    await expect.poll(() => playing(page, headers)).toBe(0);
  } finally {
    await setFixtureRateLimit(page, 0);
  }
});
