// One picker for every Live TV source. A playlist, an Xtream login and a
// Stremio manifest are the same kind of thing here: a configured provider that
// contributes channels, events, or both. There is no IPTV/Stremio grouping and
// no mode switch — the picker renders one flat list, and the source type is
// kept inside the value so the server still uses the right integration.
//
// Values are `all` or `<kind>:<source id>`, where kind is `iptv` for playlist
// and Xtream sources and `addon` for Stremio manifests. "All providers" is the
// unfiltered list of every source together.

export const ALL_PROVIDERS = 'all';

// Keep in step with livetv.IsAddonKind on the server. The `addon` flag the API
// sends is authoritative; the kind is a fallback for an older response.
const addonKinds = new Set(['stremio', 'sport']);

export function isAddonSource(source = {}) {
  return source?.addon === true || addonKinds.has(String(source?.kind || '').trim().toLowerCase());
}

export function sourceValue(kind, id) {
  if (!id || kind === 'all') return ALL_PROVIDERS;
  return `${kind === 'addon' ? 'addon' : 'iptv'}:${id}`;
}

export function parseSource(value) {
  const text = String(value || '');
  const cut = text.indexOf(':');
  if (cut < 0) return { kind: 'all', id: '' };
  const kind = text.slice(0, cut);
  const id = text.slice(cut + 1);
  if (!id || (kind !== 'iptv' && kind !== 'addon')) return { kind: 'all', id: '' };
  return { kind, id };
}

const nameOf = (source) => String(source?.name || source?.id || '').trim();

// Two sources can share a display name. Their ids stay distinct, and the row
// keeps working, so the id is shown only when it is needed to tell them apart.
function labelFor(source, all) {
  const name = nameOf(source);
  const duplicate = all.filter((x) => nameOf(x) === name).length > 1;
  const counts = [];
  if (Number(source?.channels) > 0) counts.push(`${source.channels} channel${source.channels === 1 ? '' : 's'}`);
  if (Number(source?.events) > 0) counts.push(`${source.events} event${source.events === 1 ? '' : 's'}`);
  return `${duplicate ? `${name} (${source.id})` : name}${source.enabled === false ? ' (disabled)' : ''}${counts.length ? ` · ${counts.join(' · ')}` : ''}`;
}

// sourceOptions is what the picker renders: one flat list, "All providers"
// first, then every configured source in configuration order. An addon is a
// provider entry like any other, labelled by what it actually contributes.
export function sourceOptions({ providers = [] } = {}) {
  const list = (providers || []).filter((provider) => provider?.id);
  const options = [{ value: ALL_PROVIDERS, label: 'All providers', kind: 'all', id: '' }];
  for (const provider of list) {
    const kind = isAddonSource(provider) ? 'addon' : 'iptv';
    options.push({
      value: sourceValue(kind, provider.id),
      label: labelFor(provider, list),
      kind,
      id: provider.id,
    });
  }
  return options;
}

// defaultSource opens on "All providers". Filtering to one source is a choice,
// not the way the page starts.
export function defaultSource() {
  return ALL_PROVIDERS;
}

// channelsForSource applies a source value to the channel list. Only ids a
// channel actually carries can match, so choosing a path can never show a
// different provider's stations.
export function channelsForSource(channels = [], value = ALL_PROVIDERS) {
  const { kind, id } = parseSource(value);
  if (kind === 'all') return channels || [];
  return (channels || []).filter((channel) => channel.source === id);
}

// eventsForSource is the same filter for addon events, under the same value.
export function eventsForSource(events = [], value = ALL_PROVIDERS) {
  const { kind, id } = parseSource(value);
  if (kind === 'all') return events || [];
  return (events || []).filter((event) => event.source === id);
}

// sourceName names a selected source for a heading or a title.
export function sourceName(value, { providers = [] } = {}) {
  const { kind, id } = parseSource(value);
  if (kind === 'all') return 'All providers';
  const found = (providers || []).find((source) => source?.id === id);
  return nameOf(found) || id;
}
