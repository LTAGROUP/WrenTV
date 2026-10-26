// Port of MediaStorm's live-dashboard/stats-delay-preference.ts. The viewer
// delays on-screen scores and statistics until they match what the stream has
// shown, and the choice is remembered on this device for this server and
// account. Nothing is sent to the server: it is a presentation preference.

import { withholdLiveData } from './sports-model.js';

export const STATS_DELAY_OPTIONS = [0, 15, 30, 60, 90, 120];
export const STATS_DELAY_MAX = 300;

// Five-second steps between 0 and 300 seconds, so a future slider can produce
// any value without inventing a different scale.
export function normalizeStatsDelay(value) {
  return typeof value === 'number' && Number.isFinite(value)
    ? Math.max(0, Math.min(STATS_DELAY_MAX, Math.round(value / 5) * 5))
    : 0;
}

export function delayPreferenceKey(server, profile) {
  return `wren.sports.stats-delay.v1:${[server || '', profile || ''].map(encodeURIComponent).join(':')}`;
}

export function readStatsDelay(key) {
  try {
    return normalizeStatsDelay(JSON.parse(localStorage.getItem(key) ?? '0'));
  } catch {
    return 0;
  }
}

export function writeStatsDelay(key, value) {
  const next = normalizeStatsDelay(value);
  try {
    localStorage.setItem(key, JSON.stringify(next));
  } catch {
    // Storage may be unavailable; the delay still applies for this page.
  }
  return next;
}

// "Live" is the honest label for no delay; anything else names the seconds so
// a viewer never mistakes delayed scores for the live ones.
export function statsDelayLabel(seconds) {
  return normalizeStatsDelay(seconds) ? `${normalizeStatsDelay(seconds)}s` : 'Live';
}

// While the history is shorter than the chosen delay there is nothing honest to
// show: the note explains the wait instead of displaying current data.
export function statsDelayNote(seconds, buffering) {
  const value = normalizeStatsDelay(seconds);
  if (!value) return '';
  return buffering
    ? `Syncing stats · ${value}s behind the stream`
    : `Stats delayed ${value}s to match the stream`;
}

// A source switch restarts the receipt history, so the delayed view cannot mix
// two streams' timelines. Applied when the docked preview or the full player
// starts on a different source.
export function statsSourceKey(match) {
  if (!match) return '';
  return `${match.sourceId || ''}|${match.channelId || ''}`;
}

// One decision for every delayed surface (event page and preview HUD): either
// the eligible snapshot, or a structural copy with its live values withheld.
// There is deliberately no third "show the latest anyway" branch.
export function selectDelayedEvent(buffer, latest, seconds, now) {
  const delay = normalizeStatsDelay(seconds);
  if (!delay) return { kind: 'ready', event: latest, buffering: false };
  const picked = buffer.select(now, delay);
  if (picked.kind === 'ready') {
    return { kind: 'ready', event: picked.value, receivedAt: picked.receivedAt, buffering: false };
  }
  return { kind: 'buffering', event: withholdLiveData(latest), buffering: true };
}
