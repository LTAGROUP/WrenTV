// Pure helpers for the sports-addon browser in Live TV. A sport addon lists
// events (matchups, sessions, race weekends), not channels, so these helpers
// keep that model separate from channel grouping and search.

// groupSportItems keeps the addon's own shelf labels and order. Items without
// an id cannot be opened, so they are dropped rather than rendered as dead
// cards.
export function groupSportItems(items = []) {
  const groups = new Map();
  for (const item of items || []) {
    const id = String(item?.id || '').trim();
    if (!id) continue;
    const name = String(item?.group || '').trim() || 'Events';
    if (!groups.has(name)) groups.set(name, []);
    groups.get(name).push({ ...item, id });
  }
  return [...groups.entries()].map(([name, list]) => ({ name, items: list }));
}

// eventStatus reads only words the addon itself published. An unrecognised
// releaseInfo stays unlabelled instead of guessing that an event is live.
export function eventStatus(item = {}) {
  const text = `${item.releaseInfo || ''} ${item.description || ''}`.toUpperCase();
  if (/\bLIVE\b|\bLIVE NOW\b/.test(text)) return 'live';
  if (/\bUPCOMING\b|\bTOMORROW\b|\bTODAY\b/.test(text)) return 'upcoming';
  return '';
}

// streamLabel names a feed in the picker. Addons usually label feeds with the
// channel they carry; when they do not, the title or its position is used so
// every feed is still selectable.
export function streamLabel(stream = {}, index = 0) {
  const name = String(stream?.name || '').trim();
  if (name) return name;
  const title = String(stream?.title || '').trim();
  return title || `Feed ${index + 1}`;
}
