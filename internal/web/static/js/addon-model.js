// Pure helpers for capability-aware Stremio addons. An addon's manifest decides
// what it can do: television channels, sport events, another catalog type, or
// nothing to browse at all. These helpers describe that without assuming a
// particular content type.

const modelsOf = (caps) => (Array.isArray(caps?.models) ? caps.models : []);
const catalogsOf = (caps) => (Array.isArray(caps?.catalogs) ? caps.catalogs : []);

// addonHasBrowseModel reports whether a configured addon belongs in the catalog
// browser rather than only in the channel list. An addon whose capabilities
// have not been discovered yet is listed optimistically: the browser reports
// what it finds instead of hiding a valid addon.
export function addonHasBrowseModel(source = {}) {
  const caps = source.capabilities;
  if (!caps) return true;
  const models = modelsOf(caps);
  if (models.some((model) => model?.model !== 'channels' && Number(model?.catalogs) > 0)) return true;
  if (models.length) return false;
  const hasTV = catalogsOf(caps).some((cat) => String(cat?.type || '').toLowerCase() === 'tv');
  return !hasTV && caps.browsable !== false;
}

// addonCatalogTypes is the distinct content types among usable catalogs.
export function addonCatalogTypes(catalogs = []) {
  const seen = new Set();
  for (const cat of catalogs || []) {
    const type = String(cat?.type || '').trim();
    if (type) seen.add(type);
  }
  return [...seen];
}

// addonCatalogLabel names a shelf, adding its content type only when the addon
// declares more than one, so a single-type addon keeps its own names.
export function addonCatalogLabel(catalog = {}, catalogs = []) {
  const name = String(catalog?.name || catalog?.id || '').trim();
  const type = String(catalog?.type || '').trim();
  const types = addonCatalogTypes(catalogs);
  if (!type || types.length < 2) return name;
  return `${name} · ${type}`;
}

// addonCapabilitySummary describes what a check discovered. It separates what
// the addon is (a valid manifest) from what it can do (usable catalogs and
// streams), and keeps the addon's own limitations visible.
export function addonCapabilitySummary(caps) {
  if (!caps) return { headline: 'Not checked yet', details: [], limitations: [] };
  const counts = new Map();
  for (const cat of catalogsOf(caps)) {
    const type = String(cat?.type || '').trim() || 'other';
    counts.set(type, (counts.get(type) || 0) + 1);
  }  const details = [...counts.entries()].map(([type, n]) => `${n} ${type} catalog${n === 1 ? '' : 's'}`);
  const hasTV = counts.has('tv');
  const headline = caps.browsable || caps.playable ? 'Valid addon' : 'Valid addon · nothing to browse yet';
  if (hasTV) details.unshift('TV channels');
  return { headline, details, limitations: Array.isArray(caps.limitations) ? caps.limitations : [] };
}

// addonItemWords keeps the event vocabulary for sports addons and uses neutral
// item words elsewhere, so a film catalog isn't called an event.
export function addonItemWords(type) {
  return String(type || '').toLowerCase() === 'sport'
    ? { item: 'event', items: 'events', browse: 'Events', watch: 'Watch' }
    : { item: 'item', items: 'items', browse: 'Browse', watch: 'Watch' };
}
