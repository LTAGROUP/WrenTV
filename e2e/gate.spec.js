// Deployment gate for the sports web experience. Requires a real Wren TV
// server (Docker image), a fixture M3U/XMLTV playlist and at least one enabled
// streaming addon that serves a live HLS link. No API mocks: every assertion
// goes through the deployed app.
import { test, expect } from '@playwright/test';

const USER = process.env.WRENTV_E2E_USER;
const PASS = process.env.WRENTV_E2E_PASSWORD;
// The fixture provider records the headers every media request arrived with, so
// a forwarded identity and a dropped credential are observed, not assumed.
const FIXTURE = process.env.WRENTV_FIXTURE_URL || 'http://127.0.0.1:8391';
const FIXTURE_REFERER = 'https://fixture.example/watch';
const FIXTURE_ORIGIN = 'https://fixture.example';

test.describe.configure({ mode: 'serial' });
test.skip(!USER || !PASS, 'Set WRENTV_E2E_USER and WRENTV_E2E_PASSWORD for a real deployment run.');

// The gate must not assume the first card has a matched feed, and it must not
// quietly skip when none does either. Ask the server which event actually has a
// matched feed and open that one; the gate's own playlist makes this
// guaranteed, so a miss is a deployment failure, not a reason to pass.
async function openPreviewableEvent(page) {
  const token = await page.evaluate(() => JSON.parse(localStorage.getItem('wrentv.session') || '{}').token || '');
  const headers = { Authorization: `Bearer ${token}` };
  const hub = await (await page.request.get('/api/sports/hub', { headers })).json();
  for (const event of (hub.events || []).slice(0, 20)) {
    // A finished event opens the replay search, not the live channel preview:
    // the gate's preview assertion needs an event that still has live feeds.
    if (event.status === 'final') continue;
    const res = await page.request.get(`/api/sports/events/${encodeURIComponent(event.id)}/streams`, { headers });
    if (!res.ok()) continue;
    const streams = await res.json();
    if (streams.liveTV === false || !(streams.groups || []).length) continue;
    await page.goto(`/sports/event/${encodeURIComponent(event.id)}`);
    // A final event offers the same panel as "Search for Replay"; a live or
    // scheduled one offers "Watch".
    await page.getByRole('button', { name: /^(Watch|Search for Replay)$/ }).click();
    await expect(page.locator('.watch-panel')).toBeVisible();
    const preview = page.locator('.watch-panel [data-preview]').first();
    await expect(preview).toBeVisible();
    return preview;
  }
  throw new Error('no sports event has a matched Live TV feed: the gate playlist never reached matching');
}

async function signIn(page) {
  await page.goto('/');
  // A claimed server defaults to Wren TV account sign-in; an admin name from
  // this server is one link away. Both are real sign-in paths, no test bypass.
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

test('search reaches channels, teams and events outside the loaded feed', async ({ page }) => {
  await signIn(page);
  await page.getByRole('link', { name: 'Sports' }).click();
  const search = page.getByLabel('Search sports');
  // Search a real participant from the loaded feed, so the gate is not tied to
  // one league's schedule.
  const label = await page.locator('.sports-card').first().getAttribute('aria-label');
  const team = (label || '').split(',')[0].replace(/ \d+$/, '').trim();
  test.skip(!team, 'No event card on this deployment to search for.');
  await search.fill(team);
  await expect(page.getByRole('heading', { name: /Search all of Wren TV/ })).toBeVisible();
  await expect(page.locator('.search-results')).toContainText(/Channels|Events|Teams/);
  // A channel result must link to a real channel page, not a dead anchor.
  const channel = page.locator('.search-results a[href^="/live?channel="]').first();
  if (await channel.count()) {
    await expect(channel).toHaveAttribute('href', /\/live\?channel=/);
  }
});

// B16: the expanded feed mounts one page of cards at a time, so a large day
// never keeps every card in the DOM.
test('expanded feed mounts one page and swaps it', async ({ page }) => {
  await signIn(page);
  await page.getByRole('link', { name: 'Sports' }).click();
  await page.getByRole('button', { name: 'All events' }).click();
  const cards = page.locator('.sports-grid .sports-card');
  await expect(cards.first()).toBeVisible();
  expect(await cards.count()).toBeLessThanOrEqual(12);
  const firstName = await cards.first().getAttribute('aria-label');
  const next = page.getByRole('button', { name: 'Next' });
  if ((await next.count()) && (await next.isEnabled())) {
    await next.click();
    await expect.poll(async () => cards.first().getAttribute('aria-label')).not.toBe(firstName);
    expect(await cards.count()).toBeLessThanOrEqual(12);
  }
  // Leaving the expanded feed releases the page's cards instead of hiding them:
  // the lane rails are what is mounted again.
  await page.getByRole('button', { name: 'Back to lanes' }).click();
  await expect(page.locator('.feed')).toHaveCount(0);
  expect(await page.locator('.sports-rail .sports-card').count()).toBeGreaterThan(0);
});

// I4/I5: drive replay draws provider coordinates with a first-down line,
// down/distance, a previous eligible play, and no trace where a play cannot be
// placed.
test('drive replay draws provider coordinates and refuses what it cannot place', async ({ page }) => {
  await signIn(page);
  const token = await page.evaluate(() => JSON.parse(localStorage.getItem('wrentv.session') || '{}').token || '');
  const hub = await (await page.request.get('/api/sports/hub', { headers: { Authorization: `Bearer ${token}` } })).json();
  const settled = (hub.events || []).filter(
    (event) => (event.league === 'nfl' || event.league === 'college-football') && event.status === 'final',
  );
  test.skip(!settled.length, 'No settled football game in the feed to replay.');
  // Not every provider publishes drives for every game; pick one that does
  // instead of asserting on the first card.
  let football = null;
  for (const event of settled.slice(0, 8)) {
    const detail = await (await page.request.get(`/api/sports/events/${encodeURIComponent(event.id)}`, {
      headers: { Authorization: `Bearer ${token}` },
    })).json();
    if ((detail?.detail?.footballDrives || []).length) {
      football = event;
      break;
    }
  }
  test.skip(!football, 'No settled football game published drives.');
  await page.goto(`/sports/event/${encodeURIComponent(football.id)}`);
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  // The tab bar is painted from the async detail response; wait for it before
  // deciding whether this game has a Drives tab.
  await expect(page.locator('#event-tabs [data-tab]').first()).toBeVisible();
  const drives = page.getByRole('tab', { name: 'Drives' });
  test.skip(!(await drives.count()), 'This game published no drives.');
  await drives.click();
  const rows = page.locator('.replay-row');
  await expect(rows.first()).toBeVisible();
  await expect(page.locator('.replay-field').first()).toBeVisible();
  // A straight run can have a zero-width box, which Playwright calls invisible;
  // the trace existing is what matters, so count it instead.
  expect(await page.locator('.replay-trace').count()).toBeGreaterThan(0);
  expect(await page.locator('.replay-trace.rush, .replay-trace.pass').count()).toBeGreaterThan(0);
  // Every row either places the play or says it cannot: no invented trace.
  const placed = await page.locator('.replay-field').count();
  // The first-down marker exists only where the provider supplied a line, and
  // sits inside the drawn field when it does.
  const markers = await page.locator('.field-first').evaluateAll((lines) => lines.map((line) => Number(line.getAttribute('x1'))));
  expect(markers.length).toBeGreaterThan(0);
  expect(markers.length).toBeLessThanOrEqual(placed);
  for (const x of markers) {
    expect(x).toBeGreaterThanOrEqual(110);
    expect(x).toBeLessThanOrEqual(730);
  }
  const refused = await page.getByText('No in-field trace for this play.').count();
  expect(placed + refused).toBeGreaterThanOrEqual(await rows.count());
  // The header names the provider as the source, and each drawn field is
  // labelled for screen readers rather than left as an unlabelled image.
  await expect(page.locator('.drive-replay')).toContainText('provider coordinates');
  expect(await page.locator('.replay-field[aria-label]').count()).toBeGreaterThan(0);
  // Possession is named from provider data, and the row above a play is offered
  // as the previous eligible play rather than being invented for the first one.
  expect(await page.locator('.replay-row').filter({ hasText: / ball · / }).count()).toBeGreaterThan(0);
  expect(await page.locator('.replay-row').filter({ hasText: 'Previous eligible play:' }).count()).toBeGreaterThan(0);
});

// The live channel preview and the streaming-addon list only exist for an
// event that has not finished; a final event opens the replay search instead.
async function openWatchableEvent(page) {
  const token = await page.evaluate(() => JSON.parse(localStorage.getItem('wrentv.session') || '{}').token || '');
  const headers = { Authorization: `Bearer ${token}` };
  const hub = await (await page.request.get('/api/sports/hub', { headers })).json();
  const event = (hub.events || []).find((entry) => entry.status !== 'final');
  if (!event) throw new Error('no live or scheduled sports event in the hub');
  await page.goto(`/sports/event/${encodeURIComponent(event.id)}`);
  await expect(page.getByRole('button', { name: /^(Watch|Search for Replay)$/ })).toBeVisible();
  await page.getByRole('button', { name: /^(Watch|Search for Replay)$/ }).click();
  await expect(page.locator('.watch-panel')).toBeVisible();
  return event;
}

test('event detail tabs render, and playback sessions are cleaned up', async ({ page }) => {
  await signIn(page);
  await openWatchableEvent(page);
  await expect(page.getByRole('heading', { level: 1 })).toBeVisible();
  // Docked preview: start, confirm exactly one session, stop, confirm it fell.
  // /api/admin/livetv reports the live session count on this server. The app
  // authenticates with a bearer token, so the request needs the same one.
  const token = await page.evaluate(() => JSON.parse(localStorage.getItem('wrentv.session') || '{}').token || '');
  const sessions = async () => (await (await page.request.get('/api/admin/livetv', { headers: { Authorization: `Bearer ${token}` } })).json()).liveTv.playing;
  // The search is asynchronous: wait for rows before deciding how playback
  // starts, or a still-loading panel looks identical to one with no feeds.
  await expect(page.locator('.watch-panel [data-watch]').first()).toBeVisible();
  // A lone strong feed can play straight away (F5), so a preview may already be
  // open; either path must still leave the provider with one session and none
  // after Stop.
  const preview = page.getByRole('button', { name: 'Preview' }).first();
  if (!(await page.locator('.docked-preview video').count()) && (await preview.count())) await preview.click();
  await expect(page.locator('.docked-preview video')).toBeVisible();
  await expect.poll(sessions).toBe(1);
  await page.locator('.docked-preview').getByRole('button', { name: 'Stop' }).click();
  await expect.poll(sessions).toBe(0);
});

// The addon flow end to end: a provider that only serves with the referer and
// origin the addon asked for, quality measured through the same identity, no
// credential forwarded, source switching, a failing source, handover to the
// full player, leaving the page, and session cleanup after each step.
test('addon playback: identity, quality, switching, failure and cleanup', async ({ page }, testInfo) => {
  await signIn(page);
  await openWatchableEvent(page);
  const previews = page.locator('[data-addon-preview]');
  await expect(previews.first()).toBeVisible();
  await previews.first().scrollIntoViewIfNeeded();
  await page.screenshot({ path: testInfo.outputPath('addon-sources.png'), fullPage: true });

  const token = await page.evaluate(() => JSON.parse(localStorage.getItem('wrentv.session') || '{}').token || '');
  const playing = async () => (await (await page.request.get('/api/admin/livetv', { headers: { Authorization: `Bearer ${token}` } })).json()).liveTv.playing;
  const providerLog = async () => (await fetch(`${FIXTURE}/_media-requests`)).json();

  // 1. Measured quality: the provider refuses anything without its identity, so
  //    a measured resolution proves the probe carried it. Nothing is playing
  //    yet, so every gated request logged here came from ffprobe.
  await page.locator('[data-addon-probe]').first().click();
  await expect(page.locator('.probe-lines').first()).toContainText('640 × 360');
  const gated = (log) => log.filter((request) => request.path.includes('/media/gated/'));
  const measured = gated(await providerLog());
  expect(measured.length).toBeGreaterThan(0);
  expect(measured.map((request) => request.userAgent)).toContain('GateProbe/1.0');
  for (const request of measured) {
    expect(request.referer).toBe(FIXTURE_REFERER);
    expect(request.origin).toBe(FIXTURE_ORIGIN);
    expect(request.cookie).toBe('');
    expect(request.apiKey).toBe('');
  }

  // 2. The docked preview repeats the scoreline and holds exactly one session.
  await previews.first().click();
  await expect(page.locator('.docked-preview video')).toBeVisible();
  await expect(page.locator('.docked-preview .dp-hud')).not.toBeEmpty();
  await expect.poll(playing).toBe(1);
  await page.screenshot({ path: testInfo.outputPath('addon-preview.png'), fullPage: true });

  // 3. Handover to the full player: it carries the sports identity (competition,
  //    lifecycle, matchup, feed), and "Change source" returns to the event with
  //    the chooser open while releasing the session.
  const eventTitle = await page.getByRole('heading', { level: 1 }).textContent();
  await page.locator('.docked-preview').getByRole('button', { name: 'Full screen' }).click();
  await expect(page.locator('.live-player video')).toBeVisible();
  await expect(page.locator('.live-player .top h2')).toHaveText(eventTitle.trim());
  await expect(page.locator('.live-player .sports-meta')).not.toBeEmpty();
  await expect(page.locator('.live-player .sports-meta')).toContainText(/NBA|NFL|MLB|NHL|Final|Live|Q\d/i);
  // The HUD's back control is the way back to this event, and the lifecycle
  // label is a real status region rather than decoration.
  await expect(page.locator('.live-player [data-close]').first()).toHaveAttribute('aria-label', 'Back to event');
  await expect(page.locator('.live-player [data-live-pill]')).toHaveAttribute('role', 'status');
  await page.locator('.live-player [data-change-source]').click();
  await expect(page.locator('.live-player')).toHaveCount(0);
  await expect(page).toHaveURL(/\?watch=1$/);
  await expect(page.locator('.watch-panel')).toBeVisible();
  await expect.poll(playing).toBe(0);

  // 4. Switching to another healthy source replaces the session rather than
  //    stacking one.
  await previews.nth(1).scrollIntoViewIfNeeded();
  await previews.nth(1).click();
  await expect.poll(playing).toBe(1);
  await page.locator('.docked-preview').getByRole('button', { name: 'Stop' }).click();
  await expect.poll(playing).toBe(0);

  // 5. A provider that fails mid-stream is reported in the preview status
  //    without a URL, keeps the panel for another choice, and releases the
  //    session rather than holding the provider connection.
  await previews.nth(2).click();
  const status = page.locator('.docked-preview .dp-status[role="alert"]');
  await expect(status).toBeVisible();
  await expect(status).toContainText(/stopped responding|could not/i);
  await expect(status).not.toContainText(/https?:\/\//);
  await expect.poll(playing).toBe(0);
  await page.screenshot({ path: testInfo.outputPath('addon-failure.png'), fullPage: true });
  await page.locator('.docked-preview').getByRole('button', { name: 'Stop' }).click();

  // 6. Leaving the page stops playback and its provider session.
  await previews.first().scrollIntoViewIfNeeded();
  await previews.first().click();
  await expect.poll(playing).toBe(1);
  await page.locator('a[href="/sports"]').first().click();
  await expect.poll(playing).toBe(0);

  // Every gated request the provider saw carried the addon's identity and none
  // of the credential headers it also offered.
  const log = gated(await providerLog());
  expect(log.length).toBeGreaterThan(measured.length);
  for (const request of log) {
    expect(request.userAgent).toBe('GateProbe/1.0');
    expect(request.referer).toBe(FIXTURE_REFERER);
    expect(request.origin).toBe(FIXTURE_ORIGIN);
    expect(request.cookie).toBe('');
    expect(request.apiKey).toBe('');
  }
});

// I7: the live player offers audio and subtitle menus only when the stream
// publishes a choice, and applying a selection reaches the player rather than
// updating a label. A single-rendition stream (which is also what a remuxed
// MPEG-TS channel is) gets neither control.
test('live player offers track menus only for streams that publish tracks', async ({ page }) => {
  await signIn(page);
  const token = await page.evaluate(() => JSON.parse(localStorage.getItem('wrentv.session') || '{}').token || '');
  const body = await (await page.request.get('/api/livetv/channels', { headers: { Authorization: `Bearer ${token}` } })).json();
  const channels = body.channels || [];
  const multi = channels.find((channel) => channel.name === 'Gate Audio Tracks');
  const single = channels.find((channel) => channel.name === 'Gate Single Audio');
  expect(multi, 'the gate playlist must publish the multi-rendition channel').toBeTruthy();
  expect(single, 'the gate playlist must publish the single-rendition channel').toBeTruthy();
  // Only primitives can cross the evaluate boundary; the hls.js instance cannot.
  const trackIndex = (kind) => page.evaluate((which) => {
    const instance = document.querySelector('.live-player video')?._hls;
    if (!instance) return null;
    return which === 'audio' ? instance.audioTrack : instance.subtitleTrack;
  }, kind);
  const audioTrackCount = () => page.evaluate(() => document.querySelector('.live-player video')?._hls?.audioTracks?.length ?? null);

  await page.goto(`/live?channel=${encodeURIComponent(multi.id)}`);
  await expect(page.locator('.live-player video')).toBeVisible();
  // The app draws each enhanced dropdown next to its (hidden) native select.
  const audio = page.locator('.live-player [data-audio] + .dd');
  const subs = page.locator('.live-player [data-subs] + .dd');
  // Both renditions from the master playlist are offered, named as the provider
  // names them, and subtitles are offered as off plus the published rendition.
  await expect(audio).toBeVisible();
  await expect.poll(audioTrackCount).toBe(2);
  await expect(audio.locator('.dd-cap')).toHaveText('Audio');
  await expect(audio.locator('.dd-val')).toHaveText('English (en)');
  await expect(subs).toBeVisible();
  await expect(subs.locator('.dd-val')).toHaveText('Subtitles off');

  const optionLabels = async (button) => {
    await button.click();
    const opts = page.locator('.live-player .dd-menu .dd-opt');
    const labels = await opts.allTextContents();
    return labels.map((label) => label.trim());
  };
  expect(await optionLabels(audio)).toEqual(['English (en)', 'Spanish (es)']);
  // Switching really reaches the player: hls.js reports the newly selected
  // audio rendition and the control shows it, rather than only the menu closing.
  await expect.poll(() => trackIndex('audio')).toBe(0);
  await page.locator('.live-player .dd-menu .dd-opt').nth(1).click();
  await expect.poll(() => trackIndex('audio')).toBe(1);
  await expect(audio.locator('.dd-val')).toHaveText('Spanish (es)');

  expect(await optionLabels(subs)).toEqual(['Subtitles off', 'English (en)']);
  await expect.poll(() => trackIndex('subs')).toBe(-1);
  await page.locator('.live-player .dd-menu .dd-opt').nth(1).click();
  await expect.poll(() => trackIndex('subs')).toBe(0);
  await expect(subs.locator('.dd-val')).toHaveText('English (en)');
  // Off is a real selection, not a missing one.
  await optionLabels(subs);
  await page.locator('.live-player .dd-menu .dd-opt').nth(0).click();
  await expect.poll(() => trackIndex('subs')).toBe(-1);
  await expect(subs.locator('.dd-val')).toHaveText('Subtitles off');
  await page.keyboard.press('Escape');
  await expect(page.locator('.live-player')).toHaveCount(0);

  await page.goto(`/live?channel=${encodeURIComponent(single.id)}`);
  await expect(page.locator('.live-player video')).toBeVisible();
  // hls.js is playing here too and reports no alternate rendition: the hidden
  // menu is a fact about the stream, not a missing player mode.
  await expect.poll(audioTrackCount).toBe(0);
  await expect(page.locator('.live-player [data-audio] + .dd')).toBeHidden();
  await expect(page.locator('.live-player [data-subs] + .dd')).toBeHidden();
  await page.keyboard.press('Escape');
});

// A provider that fails mid-stream must always be reported: the assertion is
// mandatory, the failing feed is chosen by name rather than by position, and
// the message is sanitized and the session released.
test('a failing provider is reported without a URL and releases its session', async ({ page }) => {
  await signIn(page);
  await openPreviewableEvent(page);
  const token = await page.evaluate(() => JSON.parse(localStorage.getItem('wrentv.session') || '{}').token || '');
  const sessions = async () => (await (await page.request.get('/api/admin/livetv', { headers: { Authorization: `Bearer ${token}` } })).json()).liveTv.playing;
  // A single credible feed may have played straight away; stop it so the only
  // session this test measures is the one the failing provider creates.
  if (await page.locator('.docked-preview').count()) {
    await page.locator('.docked-preview').getByRole('button', { name: 'Stop' }).click();
  }
  await expect.poll(sessions).toBe(0);
  const addonPreviews = page.locator('.watch-panel [data-addon-preview]');
  const choices = await addonPreviews.count();
  const broken = page.locator('.stream-row', { hasText: 'Gate Fixture Broken' }).locator('[data-addon-preview]');
  await expect(broken).toHaveCount(1);
  await broken.click();
  const status = page.locator('.docked-preview .dp-status[role="alert"]');
  await expect(status).toBeVisible();
  await expect(status).toContainText(/stopped responding/i);
  await expect(status).not.toContainText(/https?:\/\//);
  await expect.poll(sessions).toBe(0);
  // A failed source does not erase the others: the chooser is still there.
  await expect(addonPreviews).toHaveCount(choices);
  await page.locator('.docked-preview').getByRole('button', { name: 'Stop' }).click();
});

test('docked preview takes focus, closes on Escape and restores the launcher', async ({ page }) => {
  await signIn(page);
  const preview = await openPreviewableEvent(page);
  await preview.focus();
  await preview.click();
  await expect(page.locator('.docked-preview video')).toBeVisible();
  // The preview HUD names the same delayed scoreline the page shows.
  await expect(page.locator('.docked-preview .dp-hud')).toBeVisible();
  await expect(page.locator('.docked-preview .dp-hud')).not.toBeEmpty();
  // Opening moves focus to the safe control, not to a page link behind the preview.
  await expect(page.locator('.docked-preview').getByRole('button', { name: 'Stop' })).toBeFocused();
  await page.keyboard.press('Escape');
  await expect(page.locator('.docked-preview')).toHaveCount(0);
  await expect(preview).toBeFocused();
  // The launch control must still be operable after focus is restored.
  await page.keyboard.press('Enter');
  await expect(page.locator('.docked-preview')).toHaveCount(1);
  await page.locator('.docked-preview').getByRole('button', { name: 'Stop' }).click();
});

test('handing the preview to the full player never returns focus behind it', async ({ page }) => {
  await signIn(page);
  const preview = await openPreviewableEvent(page);
  await preview.click();
  await page.getByRole('button', { name: 'Full screen' }).click();
  await expect(page.locator('.live-player')).toBeVisible();
  const focusBehindPlayer = await page.evaluate(() => !!document.activeElement?.closest('.watch-panel'));
  expect(focusBehindPlayer).toBe(false);
  // Escape closes the player through its own handler, whether or not the
  // provider feed started.
  await page.keyboard.press('Escape');
  await expect(page.locator('.live-player')).toHaveCount(0);
});

test('stats delay persists per device and withholds live scores while buffering', async ({ page }) => {
  await signIn(page);
  await page.goto('/sports');
  await page.locator('.sports-card').first().click();
  await page.getByRole('button', { name: /^Sync stats/ }).click();
  await page.locator('.sync-panel').getByRole('button', { name: '15s', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Sync stats · 15s' })).toBeVisible();
  // No eligible snapshot yet, so the page explains the wait instead of showing
  // the current score.
  await expect(page.getByText(/Syncing stats · 15s behind the stream/)).toBeVisible();
  await page.reload();
  await expect(page.getByRole('button', { name: 'Sync stats · 15s' })).toBeVisible();
  await page.getByRole('button', { name: 'Sync stats · 15s' }).click();
  await page.locator('.sync-panel').getByRole('button', { name: 'Live', exact: true }).click();
  await expect(page.getByRole('button', { name: 'Sync stats · Live' })).toBeVisible();
});
