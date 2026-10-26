// Sports appearance and readability, ported from MediaStorm's
// sports-appearance.ts and useSportsReadability.ts. Presets are device-local:
// only the choice id is stored, never a snapshot of colours. The "wrentv"
// choice follows Wren TV's own tokens, exactly like MediaStorm's app-theme
// option.
export const SPORTS_APPEARANCE_KEY = 'wren.sports.appearance.v1';
export const SPORTS_READABILITY_KEY = 'wren.sports.readability.v1';

const shared = { text: '#f5f5f7', muted: '#c1c9d3', border: '#ffffff26' };

export const SPORTS_APPEARANCES = [
  {
    ...shared,
    id: 'midnight',
    name: 'Midnight',
    description: 'Ink blue with cool slate cards',
    page: ['#101722', '#090E16', '#070A10'],
    card: ['#263343', '#222E3D', '#202B39'],
    focusedCard: ['#3B516A', '#33485F', '#2D4055'],
    panel: '#263343',
    inset: '#151E2A',
    elevated: '#34475E',
    accent: '#8ebcf7',
  },
  {
    ...shared,
    id: 'graphite',
    name: 'Graphite',
    description: 'Pure black and soft charcoal',
    page: ['#101012', '#080809', '#040405'],
    card: ['#2C2D31', '#28292D', '#25262A'],
    focusedCard: ['#484B52', '#40434A', '#383B42'],
    panel: '#2C2D31',
    inset: '#18191C',
    elevated: '#3B3D43',
    accent: '#d5d5dc',
  },
  {
    ...shared,
    id: 'evergreen',
    name: 'Evergreen',
    description: 'Deep ink with sage-tinted slate',
    page: ['#101A18', '#090F0E', '#060A09'],
    card: ['#293B35', '#26352F', '#23312C'],
    focusedCard: ['#40584D', '#374D43', '#30443B'],
    panel: '#293B35',
    inset: '#17241F',
    elevated: '#374D43',
    accent: '#a6d7bd',
  },
  {
    ...shared,
    id: 'aubergine',
    name: 'Aubergine',
    description: 'Charcoal plum with soft lavender',
    page: ['#18131D', '#100C14', '#0A080D'],
    card: ['#38303F', '#322B39', '#2E2735'],
    focusedCard: ['#56465F', '#4B3D54', '#41364B'],
    panel: '#38303F',
    inset: '#211B28',
    elevated: '#493C53',
    accent: '#d0b6e9',
  },
  {
    ...shared,
    id: 'warm-slate',
    name: 'Warm Slate',
    description: 'Dark espresso with warm stone cards',
    page: ['#1A1713', '#100E0B', '#0A0907'],
    card: ['#3A352F', '#342F2A', '#302B26'],
    focusedCard: ['#575047', '#4C453D', '#433D36'],
    panel: '#3A352F',
    inset: '#24201B',
    elevated: '#494238',
    accent: '#dbc6a5',
  },
  {
    ...shared,
    id: 'wrentv',
    name: 'Wren TV',
    description: 'Follows your app appearance and colours',
    page: ['var(--bg)', 'var(--bg)', 'var(--bg)'],
    card: ['var(--panel)', 'var(--panel)', 'var(--panel)'],
    focusedCard: ['var(--panel2)', 'var(--panel2)', 'var(--panel)'],
    panel: 'var(--panel)',
    inset: 'var(--bg)',
    elevated: 'var(--panel2)',
    accent: 'var(--accent)',
  },
];

// sportsAppearance falls back to Midnight for a missing or unknown choice,
// matching MediaStorm.
export function sportsAppearance(id) {
  return SPORTS_APPEARANCES.find((theme) => theme.id === id) || SPORTS_APPEARANCES[0];
}

const rgb = (hex) => [1, 3, 5].map((start) => parseInt(String(hex).slice(start, start + 2), 16));
const luminance = (color) =>
  color.reduce((sum, channel, i) => {
    const value = channel / 255;
    return sum + (value <= 0.04045 ? value / 12.92 : ((value + 0.055) / 1.055) ** 2.4) * [0.2126, 0.7152, 0.0722][i];
  }, 0);

export function sportsColorContrast(first, second) {
  const a = luminance(rgb(first));
  const b = luminance(rgb(second));
  return (Math.max(a, b) + 0.05) / (Math.min(a, b) + 0.05);
}

// applySportsAppearance sets only a data attribute; CSS owns the resolved
// colours so theme changes never leave inline styles behind.
export function applySportsAppearance(root, id) {
  if (!root) return;
  const theme = sportsAppearance(id);
  if (theme.id === 'wrentv') root.removeAttribute('data-sports-theme');
  else root.setAttribute('data-sports-theme', theme.id);
}

export function readSportsAppearance() {
  try {
    const stored = localStorage.getItem(SPORTS_APPEARANCE_KEY);
    return sportsAppearance(stored).id;
  } catch {
    return SPORTS_APPEARANCES[0].id;
  }
}

export function saveSportsAppearance(id) {
  const theme = sportsAppearance(id);
  try {
    localStorage.setItem(SPORTS_APPEARANCE_KEY, theme.id);
  } catch {
    // The theme still applies for this page; a failed write is not fatal.
  }
  return theme.id;
}

export const DEFAULT_READABILITY = Object.freeze({ largeText: false, reduceMotion: false, strongFocus: false });

export function normalizeSportsReadability(value) {
  const input = value && typeof value === 'object' ? value : {};
  return {
    largeText: input.largeText === true,
    reduceMotion: input.reduceMotion === true,
    strongFocus: input.strongFocus === true,
  };
}

export function readSportsReadability() {
  try {
    return normalizeSportsReadability(JSON.parse(localStorage.getItem(SPORTS_READABILITY_KEY) || 'null'));
  } catch {
    return { ...DEFAULT_READABILITY };
  }
}

export function saveSportsReadability(value) {
  const next = normalizeSportsReadability(value);
  try {
    localStorage.setItem(SPORTS_READABILITY_KEY, JSON.stringify(next));
  } catch {
    // Applied for this page; persistence is best effort like theme choice.
  }
  return next;
}

export function applySportsReadability(root, readability) {
  if (!root) return;
  const value = normalizeSportsReadability(readability);
  root.classList.toggle('sports-readable-large', value.largeText);
  root.classList.toggle('sports-reduce-motion', value.reduceMotion);
  root.classList.toggle('sports-strong-focus', value.strongFocus);
}
