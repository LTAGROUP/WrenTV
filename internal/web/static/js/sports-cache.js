// Port of MediaStorm's sports-refresh.ts feed cache. MediaStorm keeps one
// cache per authenticated route identity, bounded to the three browse dates,
// with a 10-minute TTL and structural sharing so a score refresh does not
// redraw every unchanged card.
//
// The calendar day is part of the key, so a cache written before midnight can
// never be served for a feed requested after midnight.

export const FEED_CACHE_TTL_MS = 600_000;
export const FEED_CACHE_MAX_ENTRIES = 3;

// Preserve unchanged JSON subtrees. The returned value always equals `next`;
// only identity is shared, so callers may keep painting unchanged rows.
export function shareSportsData(previous, next) {
  if (Object.is(previous, next)) return previous;
  if (!previous || !next || typeof previous !== 'object' || typeof next !== 'object') return next;
  if (Array.isArray(previous) !== Array.isArray(next)) return next;
  const keys = Object.keys(next);
  let same = keys.length === Object.keys(previous).length;
  const result = Array.isArray(next) ? [] : {};
  for (const key of keys) {
    result[key] = shareSportsData(previous[key], next[key]);
    if (!Object.prototype.hasOwnProperty.call(previous, key) || result[key] !== previous[key]) same = false;
  }
  return same ? previous : result;
}

export function createSportsFeedCache() {
  const entries = new Map();
  const key = (date, now) => `${new Date(now).toDateString()}:${date}`;
  return {
    read(date, now = Date.now()) {
      const entry = entries.get(key(date, now));
      return entry && now - entry.at < FEED_CACHE_TTL_MS ? entry.value : undefined;
    },
    write(value, now = Date.now()) {
      const id = key(value.date, now);
      const shared = shareSportsData(entries.get(id)?.value, value);
      entries.delete(id);
      entries.set(id, { value: shared, at: now });
      while (entries.size > FEED_CACHE_MAX_ENTRIES) entries.delete(entries.keys().next().value);
      return shared;
    },
    // Explicit invalidation: a new server identity, a settings change or a
    // manual refresh must never paint a structural snapshot from before it.
    clear() {
      entries.clear();
    },
    size() {
      return entries.size;
    },
  };
}
