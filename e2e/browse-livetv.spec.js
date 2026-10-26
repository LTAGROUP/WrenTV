// Browse Live TV regression: the SportsHub deep link must search every Live TV
// provider, including the Stremio event provider, on its first click. The run
// is a real server, a real scoreboard and a fixture provider; nothing is mocked.
//
// `run-browse-livetv.mjs` writes a playlist channel for most of the day's
// events and gives the event provider the same listings, leaving at least one
// event that no provider carries so the empty answer can be checked too.
import { test, expect } from '@playwright/test';

const USER = process.env.WRENTV_E2E_USER;
const PASS = process.env.WRENTV_E2E_PASSWORD;

test.describe.configure({ mode: 'serial' });
test.skip(!USER || !PASS, 'Set WRENTV_E2E_USER and WRENTV_E2E_PASSWORD for a real deployment run.');

// The provider and the playlist name an event the way the hub titles it.
function nameOf(event) {
  return (event?.title || (event?.participants || []).map((participant) => participant.name).filter(Boolean).join(' vs ')).trim();
}

// The event page's Browse Live TV link searches away vs home, which is the
// query the Live TV box is expected to carry once the link is followed.
function queryOf(event) {
  const teams = (event?.participants || []).map((participant) => participant.name).filter(Boolean).join(' vs ').trim();
  return teams || nameOf(event);
}

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

async function listing(page, headers) {
  const res = await page.request.get('/api/livetv/channels', { headers });
  expect(res.ok()).toBeTruthy();
  return res.json();
}

async function streamsOf(page, headers, eventId) {
  const res = await page.request.get(`/api/sports/events/${encodeURIComponent(eventId)}/streams`, { headers });
  if (!res.ok()) return null;
  return res.json();
}

test('Browse Live TV searches the Stremio provider on the first click', async ({ page }) => {
  await signIn(page);
  const headers = await authHeaders(page);
  const live = await listing(page, headers);
  const providerEvents = live.events || [];
  expect(providerEvents.length, 'the fixture provider lists events in Live TV').toBeGreaterThan(0);

  const hub = await (await page.request.get('/api/sports/hub', { headers })).json();
  // Choose a real hub event the provider lists and the server matched, so the
  // assertion is about the first click rather than about a lucky name.
  let chosen = null;
  for (const event of hub.events || []) {
    const name = nameOf(event);
    if (!name || !providerEvents.some((entry) => entry.name === name)) continue;
    const streams = await streamsOf(page, headers, event.id);
    if (!streams?.events?.some((entry) => entry.name === name)) continue;
    chosen = { event, name, streams };
    break;
  }
  expect(chosen, 'a real event is matched as a provider event').toBeTruthy();
  const { event, name } = chosen;
  const query = queryOf(event);

  await page.goto(`/sports/event/${encodeURIComponent(event.id)}`);
  // Scoped to the page: the shell's brand link carries the server name, which
  // is not this action.
  const browse = page.locator('#page').getByRole('link', { name: 'Browse Live TV' });
  await expect(browse).toBeVisible();
  // The only action this test takes: one click on Browse Live TV.
  await browse.click();
  await expect(page).toHaveURL(/\/live\?/);

  // The deep link's query is untouched: the provider was searched by the page
  // itself, not by a second manual search.
  await expect(page.locator('#live-search input')).toHaveValue(query);

  const row = page.locator('.live-event', { hasText: name });
  await expect(row).toBeVisible({ timeout: 30_000 });
  await expect(row.getByRole('button', { name: 'Watch' })).toBeVisible();

  // The provider event and the playlist channel are rows in the same list.
  await expect(page.locator('.channel:not(.live-event)').filter({ hasText: name })).toHaveCount(1);
  await expect(page.locator('#live-count')).toContainText(/event/);
  await expect(page.locator('#live-context')).toContainText('Ranked by the Sports matcher');

  // Neither a pending answer nor an unsearched one: the first click searched.
  await expect(page.getByText('Matching this event against your Live TV providers…')).toHaveCount(0);
  await expect(page.getByText('Live TV providers are still loading', { exact: false })).toHaveCount(0);
  await expect(page.getByRole('heading', { name: 'Event match not ready' })).toHaveCount(0);
  await expect(page.getByRole('heading', { name: /No Live TV source matches/ })).toHaveCount(0);

  // The same Watch action plays the provider's event.
  await row.getByRole('button', { name: 'Watch' }).click();
  await expect(page.locator('.live-player')).toBeVisible();
  await expect.poll(async () => {
    const admin = await (await page.request.get('/api/admin/livetv', { headers })).json();
    return admin?.liveTv?.playing || 0;
  }).toBeGreaterThan(0);
  await page.keyboard.press('Escape');
  await expect(page.locator('.live-player')).toHaveCount(0, { timeout: 20_000 });
});

test('an event no provider lists settles on a real empty answer', async ({ page }) => {
  await signIn(page);
  const headers = await authHeaders(page);
  const live = await listing(page, headers);
  const providerNames = new Set((live.events || []).map((entry) => entry.name));
  const channelNames = new Set((live.channels || []).map((entry) => entry.name));

  const hub = await (await page.request.get('/api/sports/hub', { headers })).json();
  // The server must call this one settled and empty: anything else would be a
  // provider that still has an answer, and the assertion would be meaningless.
  let chosen = null;
  for (const event of hub.events || []) {
    const name = nameOf(event);
    if (!name || providerNames.has(name) || channelNames.has(name)) continue;
    const streams = await streamsOf(page, headers, event.id);
    if (!streams || streams.ready !== true) continue;
    if ((streams.groups || []).length || (streams.events || []).length) continue;
    chosen = { event, name };
    break;
  }
  test.skip(!chosen, 'The day has no event outside both providers to test the empty answer.');
  await page.goto(`/live?q=${encodeURIComponent(queryOf(chosen.event))}&event=${encodeURIComponent(chosen.event.id)}`);

  const heading = page.getByRole('heading', { name: 'No Live TV source matches this event' });
  await expect(heading).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole('heading', { name: 'Event match not ready' })).toHaveCount(0);
  await expect(page.getByText('Matching this event against your Live TV providers…')).toHaveCount(0);
  await expect(page.locator('#live-search input')).toHaveValue(queryOf(chosen.event));

  // Reloading the same link reproduces the settled answer: it was not a race
  // that happened to look empty once.
  await page.reload();
  await expect(heading).toBeVisible({ timeout: 30_000 });
});

// ---------------------------------------------------- playback and provider limits
// Automatic stream switching and provider rate limiting, observed through the
// viewer's own UI against the real server: a session is released when the
// player closes, and a provider that is refusing this server is reported with
// the wait it asked for instead of being asked again and again.

const fixtureUrl = () => process.env.WRENTV_FIXTURE_URL || 'http://127.0.0.1:8392';

const eventRow = (page, entry) => page.locator(`.live-event[data-live-event="${entry.id}"]`);

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

async function playingCount(page, headers) {
  const admin = await (await page.request.get('/api/admin/livetv', { headers })).json();
  return admin?.liveTv?.playing || 0;
}

async function providerEvents(page, headers, count) {
  const live = await listing(page, headers);
  const events = (live.events || []).slice(0, count);
  expect(events.length, 'the provider lists the events this test needs').toBe(count);
  return events;
}

test('a rate-limited provider is reported honestly and is not asked again', async ({ page }) => {
  await signIn(page);
  const headers = await authHeaders(page);
  const [entry] = await providerEvents(page, headers, 1);

  // The provider starts refusing this server, for longer than a request may wait.
  await setFixtureRateLimit(page, 30);
  try {
    await page.goto('/live');
    const row = eventRow(page, entry);
    await expect(row).toBeVisible({ timeout: 30_000 });
    await row.getByRole('button', { name: 'Watch' }).click();

    // The viewer is told what happened and when to come back, not shown a
    // generic failure or an endless spinner.
    const error = page.locator('.live-player .live-error');
    await expect(error).toBeVisible({ timeout: 30_000 });
    await expect(error).toContainText('rate limiting this server');
    await expect(error).toContainText('asked to wait 30s');

    // One attempt, one request: the rest of the feed list is not walked into the
    // same limit.
    const state = await fixtureRateLimitState(page);
    expect(state.hits, 'the addon was asked exactly once').toBe(1);

    // The refused attempt left no session behind.
    expect(await playingCount(page, headers)).toBe(0);

    // The provider recovers and "Try again" resolves the feed afresh.
    await setFixtureRateLimit(page, 0);
    await error.getByRole('button', { name: 'Try again' }).click();
    await expect.poll(async () => playingCount(page, headers), { timeout: 30_000 }).toBeGreaterThan(0);
    await page.keyboard.press('Escape');
    await expect(page.locator('.live-player')).toHaveCount(0, { timeout: 20_000 });
    await expect.poll(async () => playingCount(page, headers), { timeout: 20_000 }).toBe(0);
  } finally {
    await setFixtureRateLimit(page, 0);
  }
});

test('a conventional IPTV channel still plays beside the provider events', async ({ page }) => {
  await signIn(page);
  const headers = await authHeaders(page);
  const live = await listing(page, headers);
  const channel = (live.channels || [])[0];
  expect(channel, 'the playlist contributes channels').toBeTruthy();
  expect(channel.source, 'a channel keeps the provider it came from').toBe('fixture');
  // Addon entries must not have displaced the channel list: both are in one
  // listing, and the channel is still the ordinary playable row.
  expect((live.events || []).length, 'the addon still contributes its events').toBeGreaterThan(0);

  await page.goto('/live');
  const row = page.locator(`.channel:not(.live-event)[data-channel="${channel.id}"]`);
  await expect(row).toBeVisible({ timeout: 30_000 });
  // Every provider is shown at once, so the row names the one this channel came
  // from (the addon's entries are labelled the same way).
  await expect(row.locator('.ch-name')).toContainText(channel.name);
  await expect(row.locator('.ch-name')).toContainText('Fixture TV');
  // The guide still reaches the list: the fixture writes a programme per channel.
  await expect(row.locator('.ch-now')).toContainText('Live');

  await row.getByRole('button', { name: 'Watch' }).click();
  await expect(page.locator('.live-player')).toBeVisible();
  await expect.poll(async () => playingCount(page, headers), { timeout: 30_000 }).toBe(1);
  await page.keyboard.press('Escape');
  await expect(page.locator('.live-player')).toHaveCount(0, { timeout: 20_000 });
  await expect.poll(async () => playingCount(page, headers), { timeout: 20_000 }).toBe(0);
});

test('switching events leaves one provider session at a time', async ({ page }) => {
  await signIn(page);
  const headers = await authHeaders(page);
  const events = await providerEvents(page, headers, 2);

  await page.goto('/live');
  for (const entry of events) {
    const row = eventRow(page, entry);
    await expect(row).toBeVisible({ timeout: 30_000 });
    await row.getByRole('button', { name: 'Watch' }).click();
    await expect(page.locator('.live-player')).toBeVisible();
    await expect.poll(async () => playingCount(page, headers), { timeout: 30_000 }).toBe(1);

    // Closing the player releases its session, so switching between events never
    // leaves the previous stream running.
    await page.keyboard.press('Escape');
    await expect(page.locator('.live-player')).toHaveCount(0, { timeout: 20_000 });
    await expect.poll(async () => playingCount(page, headers), { timeout: 20_000 }).toBe(0);
  }
});
