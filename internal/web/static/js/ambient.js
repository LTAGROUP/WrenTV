// The focused or hovered title's artwork behind Home, Collections and the
// library pages, like Plex's cinematic backdrop: the art keeps its colours
// at the top right and fades into a background tinted from it.
import { img } from './api.js';

const pages = [/^\/$/, /^\/watchlist$/, /^\/library\/[^/]+$/, /^\/collections$/, /^\/collection\/[^/]+$/, /^\/studio\/\d+$/, /^\/library\/[^/]+\/collections$/, /^\/movies$/, /^\/shows$/, /^\/calendar$/];
let root, layers, front = 0, current = '', timer = 0, on = false;
const tints = new Map();

// mount puts the backdrop layers in the page shell, once.
export function mount(main) {
  root = document.createElement('div');
  root.id = 'ambient';
  root.setAttribute('aria-hidden', 'true');
  root.innerHTML = '<div class="amb"></div><div class="amb"></div>';
  main.prepend(root);
  layers = [...root.children];
  // A card that takes focus (remote, keyboard) or the pointer.
  const pick = (e) => {
    const card = e.target.closest?.('[data-bd]');
    if (card && on) want(card.dataset.bd);
  };
  document.addEventListener('focusin', pick);
  document.addEventListener('mouseover', pick);
}

// route turns the backdrop on for browsing pages, and clears it.
export function route(path) {
  on = pages.some((re) => re.test(path));
  clearTimeout(timer);
  show('');
}

// base is a page's own artwork, shown until a card takes focus.
export function base(path) {
  if (!on || !path) return;
  clearTimeout(timer);
  show(path);
}

function want(path) {
  if (path === current) return;
  clearTimeout(timer);
  // Skimming along a row shouldn't flash every poster's art.
  timer = setTimeout(() => show(path), 260);
}

function show(path) {
  if (!root) return;
  current = path;
  const back = layers[1 - front];
  if (!path) {
    for (const l of layers) l.classList.remove('on');
    root.style.removeProperty('--amb-tint');
    return;
  }
  const url = img(path, 'w1280');
  const pic = new Image();
  pic.onload = () => {
    if (current !== path) return;
    back.style.backgroundImage = `url("${url}")`;
    back.classList.add('on');
    layers[front].classList.remove('on');
    front = 1 - front;
    const t = tints.get(url) || tintOf(pic);
    if (t) { tints.set(url, t); root.style.setProperty('--amb-tint', t); }
  };
  pic.src = url;
}

// tintOf is the art's main colour, favouring saturated pixels, mixed into
// the page background so white text stays readable.
function tintOf(pic) {
  try {
    const c = document.createElement('canvas');
    c.width = 32; c.height = 18;
    const g = c.getContext('2d', { willReadFrequently: true });
    g.drawImage(pic, 0, 0, 32, 18);
    const d = g.getImageData(0, 0, 32, 18).data;
    let r = 0, gr = 0, b = 0, w = 0;
    for (let i = 0; i < d.length; i += 4) {
      const mx = Math.max(d[i], d[i + 1], d[i + 2]), mn = Math.min(d[i], d[i + 1], d[i + 2]);
      const k = (mx ? (mx - mn) / mx : 0) * (mx / 255) + 0.04;
      r += d[i] * k; gr += d[i + 1] * k; b += d[i + 2] * k; w += k;
    }
    const mix = (v, bg) => Math.round(bg + (Math.min(v / w, 158) - bg) * 0.42);
    return `rgb(${mix(r, 0x12)}, ${mix(gr, 0x10)}, ${mix(b, 0x16)})`;
  } catch { return ''; }
}
