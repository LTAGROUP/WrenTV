// Original sport glyphs for the Sports Hub, drawn to Wren TV's icon
// conventions: a 24×24 stroke icon in currentColor, decorative only (the
// competition name follows in text), so nothing is announced twice.
const GLYPHS = {
  football: '<ellipse cx="12" cy="12" rx="9" ry="5.6" transform="rotate(-20 12 12)"/><path d="M8.2 13.8 15.8 10.2M10 12.9l.6 1.4M12 12l.6 1.4M14 11.1l.6 1.4"/>',
  basketball: '<circle cx="12" cy="12" r="8.4"/><path d="M3.6 12h16.8M12 3.6v16.8M5.8 5.8c3.2 3.2 3.2 9.2 0 12.4M18.2 5.8c-3.2 3.2-3.2 9.2 0 12.4"/>',
  baseball: '<circle cx="12" cy="12" r="8.4"/><path d="M6.4 5.3c3.1 3.7 3.1 9.7 0 13.4M17.6 5.3c-3.1 3.7-3.1 9.7 0 13.4"/>',
  hockey: '<ellipse cx="8.6" cy="17.4" rx="3.6" ry="1.9"/><path d="M10.8 16.1 18.4 4.4M16.6 6.4h3.4"/>',
  soccer: '<circle cx="12" cy="12" r="8.4"/><path d="m12 7.4 5 3.6-1.9 5.9h-6.2L7 11zM12 7.4V4.2M16.8 11l3.1-.9M15.1 16.9l1.9 2.6M8.9 16.9 7 19.5M7.2 11l-3.1-.9"/>',
  tennis: '<circle cx="12" cy="12" r="8.4"/><path d="M5 6.2c4.4 2 7.6 6.5 8.6 12M19 6.2c-4.4 2-7.6 6.5-8.6 12"/>',
  rugby: '<ellipse cx="12" cy="12" rx="9" ry="5.6" transform="rotate(28 12 12)"/><path d="M8.8 14.4 15.2 9.6M10.4 12.2l-1 1.2M13.6 11.8l-1 1.2"/>',
  cricket: '<path d="M5.4 18.6 16.2 5.4l2.4 2L7.8 20.6zM16 3.4l4.6 3.8M17.4 8.2l3.4-1.2"/><circle cx="4.6" cy="20.4" r="1.2"/>',
  golf: '<path d="M8 20.6V3.6M8 4.6l8 3-8 3M4.4 20.6h7.2"/><ellipse cx="15.6" cy="19.2" rx="3.8" ry="1.4"/>',
  boxing: '<path d="M6.4 9.2a3.6 3.6 0 0 1 3.6-3.6h3.2a4.8 4.8 0 0 1 4.8 4.8v2.4a3.4 3.4 0 0 1-3.4 3.4H9.8a3.4 3.4 0 0 1-3.4-3.4z"/><path d="M8.4 16.2v2.2a1.8 1.8 0 0 0 1.8 1.8h4.4M16.6 10.8h1.8"/>',
  mma: '<path d="M8.2 3.9h7.6l4.3 4.3v7.6l-4.3 4.3H8.2L3.9 15.8V8.2z"/><path d="M8.2 8.2h7.6v7.6H8.2z"/>',
  motorsport: '<path d="M4.4 20V4.6M4.4 4.6h13.2l-2.4 3.8 2.4 3.8H4.4"/><path d="M8 4.6v7.6M11.6 4.6v7.6M15.2 4.6v7.6"/>',
  cycling: '<circle cx="6.4" cy="16.4" r="3.6"/><circle cx="17.6" cy="16.4" r="3.6"/><path d="m6.4 16.4 4-6.4h4l3.2 6.4M10.4 10l-2-2.4h-2M14.4 10h3.2"/>',
};

const ALIASES = {
  'american-football': 'football',
  'australian-football': 'football',
  'ice-hockey': 'hockey',
  racing: 'motorsport',
  f1: 'motorsport',
  motogp: 'motorsport',
  'mixed-martial-arts': 'mma',
  'field-hockey': 'hockey',
};

export function sportGlyph(sport) {
  const key = ALIASES[sport] || String(sport || '');
  const body = GLYPHS[key];
  if (!body) return '';
  return `<svg class="sport-glyph" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true" focusable="false">${body}</svg>`;
}

export const SPORT_GLYPHS = Object.keys(GLYPHS);
