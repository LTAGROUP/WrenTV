// Port of MediaStorm's live-dashboard/stats-buffer.ts. Receipts are monotonic
// local times taken when a response arrives, never provider game clocks, so a
// delayed view can only ever move backwards in time and can never fall forward
// to data the viewer has not seen yet.
//
// Rules carried over unchanged:
// - a stale request (non-monotonic requestId) or a regressing receipt is dropped;
// - selecting a delay larger than the history returns `buffering`, never the
//   newest value;
// - at most 300 seconds of history and 256 receipts are kept;
// - values are copied on append so later source mutation cannot rewrite history.

export const STATS_HISTORY_MS = 300_000;
export const STATS_MAX_RECEIPTS = 256;

export function createStatsBuffer(copy) {
  let entries = [];
  let lastRequest = -Infinity;
  return {
    append(value, receivedAt, requestId) {
      if (
        !Number.isFinite(receivedAt) ||
        !Number.isFinite(requestId) ||
        requestId <= lastRequest ||
        (entries.length && receivedAt < entries[entries.length - 1].receivedAt)
      ) return;
      lastRequest = requestId;
      entries.push({ value: copy(value), receivedAt });
      const cutoff = receivedAt - STATS_HISTORY_MS;
      while (entries.length > 1 && entries[1].receivedAt <= cutoff) entries.shift();
      if (entries.length > STATS_MAX_RECEIPTS) entries.splice(0, entries.length - STATS_MAX_RECEIPTS);
    },
    select(now, delaySeconds) {
      if (!Number.isFinite(now)) return { kind: 'buffering' };
      const seconds = Number.isFinite(delaySeconds) ? Math.max(0, Math.min(300, delaySeconds)) : 0;
      const cutoff = now - seconds * 1000;
      for (let i = entries.length - 1; i >= 0; i -= 1) {
        if (entries[i].receivedAt <= cutoff) return { kind: 'ready', ...entries[i] };
      }
      return { kind: 'buffering' };
    },
    clear() {
      entries = [];
      lastRequest = -Infinity;
    },
    size() {
      return entries.length;
    },
  };
}
