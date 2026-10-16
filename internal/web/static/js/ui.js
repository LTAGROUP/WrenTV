// Small DOM helpers shared by the views.

export const esc = (s) => String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]);

export const $ = (sel, root = document) => root.querySelector(sel);
export const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

export function el(html) {
  const t = document.createElement('template');
  t.innerHTML = html.trim();
  return t.content.firstElementChild;
}

let toastTimer;
export function toast(msg, ms = 3200) {
  const t = $('#toast');
  t.textContent = msg;
  t.hidden = false;
  clearTimeout(toastTimer);
  toastTimer = setTimeout(() => (t.hidden = true), ms);
}

export function modal(title, bodyHtml, { ok = 'Save', danger = false, onOk } = {}) {
  return new Promise((resolve) => {
    const bg = el(`<div class="modal-bg"><form class="modal">
      <h2>${esc(title)}</h2><div class="body">${bodyHtml}</div><p class="error" hidden></p>
      <div class="foot"><button type="button" class="btn ghost" data-cancel>Cancel</button>
      <button class="btn ${danger ? 'danger' : 'primary'}" data-ok>${esc(ok)}</button></div></form></div>`);
    const close = (v) => { bg.remove(); resolve(v); };
    bg.addEventListener('mousedown', (e) => e.target === bg && close(null));
    $('[data-cancel]', bg).onclick = () => close(null);
    $('form', bg).onsubmit = async (e) => {
      e.preventDefault();
      const btn = $('[data-ok]', bg);
      btn.disabled = true;
      try {
        const v = onOk ? await onOk($('form', bg)) : true;
        close(v ?? true);
      } catch (err) {
        const p = $('.error', bg);
        p.textContent = err.message;
        p.hidden = false;
        btn.disabled = false;
      }
    };
    document.body.append(bg);
    $('input, select, textarea', bg)?.focus();
  });
}

export const confirmDialog = (title, text, ok = 'Remove') => modal(title, `<p class="muted">${esc(text)}</p>`, { ok, danger: true });

export function fmtDuration(sec) {
  sec = Math.max(0, Math.round(sec || 0));
  const h = Math.floor(sec / 3600), m = Math.floor((sec % 3600) / 60), s = sec % 60;
  return h ? `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}` : `${m}:${String(s).padStart(2, '0')}`;
}

export function fmtRuntime(min) {
  if (!min) return '';
  const h = Math.floor(min / 60), m = min % 60;
  return h ? `${h}h ${m}m` : `${m}m`;
}

export function fmtSize(b) {
  if (!b) return '';
  const u = ['B', 'KB', 'MB', 'GB', 'TB'];
  let i = 0;
  while (b >= 1024 && i < u.length - 1) { b /= 1024; i++; }
  return `${b.toFixed(i > 2 ? 1 : 0)} ${u[i]}`;
}

export function timeAgo(unix) {
  if (!unix) return 'never';
  const d = Date.now() / 1000 - unix;
  if (d < 60) return 'just now';
  if (d < 3600) return `${Math.floor(d / 60)} min ago`;
  if (d < 86400) return `${Math.floor(d / 3600)} h ago`;
  return new Date(unix * 1000).toLocaleDateString();
}

export const icons = {
  home: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 11l9-8 9 8v9a1 1 0 01-1 1h-5v-6H9v6H4a1 1 0 01-1-1z"/></svg>',
  movies: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="4" width="18" height="16" rx="2"/><path d="M7 4v16M17 4v16M3 9h4M3 15h4M17 9h4M17 15h4"/></svg>',
  shows: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="6" width="18" height="13" rx="2"/><path d="M8 2l4 4 4-4"/></svg>',
  server: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="3" y="4" width="18" height="7" rx="1.5"/><rect x="3" y="13" width="18" height="7" rx="1.5"/><path d="M7 7.5h.01M7 16.5h.01"/></svg>',
  folder: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 7a2 2 0 012-2h4l2 2h8a2 2 0 012 2v8a2 2 0 01-2 2H5a2 2 0 01-2-2z"/></svg>',
  users: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="9" cy="8" r="3.5"/><path d="M2.5 20a6.5 6.5 0 0113 0M16 4.5a3.5 3.5 0 010 7M18 14a6 6 0 013.5 6"/></svg>',
  download: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M12 3v12M7 10l5 5 5-5M4 19h16"/></svg>',
  settings: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 00.3 1.8l.1.1a2 2 0 11-2.8 2.8l-.1-.1a1.7 1.7 0 00-1.8-.3 1.7 1.7 0 00-1 1.5V21a2 2 0 01-4 0v-.1a1.7 1.7 0 00-1.1-1.5 1.7 1.7 0 00-1.8.3l-.1.1a2 2 0 11-2.8-2.8l.1-.1a1.7 1.7 0 00.3-1.8 1.7 1.7 0 00-1.5-1H3a2 2 0 010-4h.1a1.7 1.7 0 001.5-1.1 1.7 1.7 0 00-.3-1.8l-.1-.1a2 2 0 112.8-2.8l.1.1a1.7 1.7 0 001.8.3H9a1.7 1.7 0 001-1.5V3a2 2 0 014 0v.1a1.7 1.7 0 001 1.5 1.7 1.7 0 001.8-.3l.1-.1a2 2 0 112.8 2.8l-.1.1a1.7 1.7 0 00-.3 1.8V9a1.7 1.7 0 001.5 1H21a2 2 0 010 4h-.1a1.7 1.7 0 00-1.5 1z"/></svg>',
  activity: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 12h4l3 8 4-16 3 8h4"/></svg>',
  stack: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><rect x="7" y="7" width="13" height="14" rx="2"/><path d="M4 17V5a2 2 0 012-2h10"/></svg>',
  tag: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M3 12V4a1 1 0 011-1h8l9 9-9 9z"/><circle cx="8" cy="8" r="1.6" fill="currentColor"/></svg>',
  chart: '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 20V10M10 20V4M16 20v-7M22 20H2"/></svg>',
  plus: '<svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" stroke-width="2.2"><path d="M12 5v14M5 12h14"/></svg>',
  menu: '<svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2"><path d="M4 6h16M4 12h16M4 18h16"/></svg>',
  back: '<svg viewBox="0 0 24 24" width="20" height="20" fill="none" stroke="currentColor" stroke-width="2"><path d="M15 18l-6-6 6-6"/></svg>',
};

// ---------- dropdowns ----------
// Native <select> menus render as OS widgets that clash with the app (and
// look broken over video). enhanceSelect keeps the <select> as the source of
// truth (value, change events, forms) and draws a styled button and menu.

let openMenu = null;

function closeMenu() {
  if (!openMenu) return;
  openMenu.menu.remove();
  openMenu.btn.setAttribute('aria-expanded', 'false');
  openMenu.btn.classList.remove('open');
  openMenu = null;
}
document.addEventListener('mousedown', (e) => { if (openMenu && !openMenu.menu.contains(e.target) && !openMenu.btn.contains(e.target)) closeMenu(); }, true);
window.addEventListener('resize', closeMenu);
document.addEventListener('scroll', (e) => { if (openMenu && !openMenu.menu.contains(e.target)) closeMenu(); }, true);

const valueDesc = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value');

export function enhanceSelect(sel) {
  if (sel.dataset.enhanced || sel.multiple || sel.dataset.native !== undefined) return;
  sel.dataset.enhanced = '1';
  const btn = document.createElement('button');
  btn.type = 'button';
  btn.className = 'dd' + (sel.className ? ' ' + sel.className : '');
  btn.setAttribute('aria-haspopup', 'listbox');
  btn.setAttribute('aria-expanded', 'false');
  if (sel.title) btn.title = sel.title;
  const caption = sel.dataset.caption;
  btn.innerHTML = `${caption ? `<span class="dd-cap">${esc(caption)}</span>` : ''}<span class="dd-val"></span><svg class="dd-chev" viewBox="0 0 12 12" aria-hidden="true"><path d="M3 4.5l3 3 3-3" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"/></svg>`;
  sel.classList.add('dd-native');
  sel.tabIndex = -1;
  sel.setAttribute('aria-hidden', 'true');
  sel.after(btn);

  const sync = () => {
    const o = sel.selectedOptions[0];
    $('.dd-val', btn).textContent = o ? o.textContent : '';
    btn.hidden = sel.hidden;
    btn.disabled = sel.disabled;
  };
  // Programmatic value changes don't fire events; catch them on this element.
  Object.defineProperty(sel, 'value', {
    configurable: true,
    get() { return valueDesc.get.call(this); },
    set(v) { valueDesc.set.call(this, v); sync(); },
  });
  new MutationObserver(sync).observe(sel, { childList: true, subtree: true, attributes: true, attributeFilter: ['hidden', 'disabled'] });
  sel.addEventListener('change', sync);
  sync();

  const choose = (opt) => {
    closeMenu();
    btn.focus();
    if (opt.value === sel.value) return;
    sel.value = opt.value;
    sel.dispatchEvent(new Event('change', { bubbles: true }));
    sel.dispatchEvent(new Event('input', { bubbles: true }));
  };

  const open = () => {
    if (openMenu?.btn === btn) return closeMenu();
    closeMenu();
    const menu = document.createElement('div');
    menu.className = 'dd-menu' + (btn.closest('.player') ? ' dark' : '');
    menu.setAttribute('role', 'listbox');
    let html = '';
    for (const node of sel.children) {
      if (node.tagName === 'OPTGROUP') {
        html += `<div class="dd-group">${esc(node.label)}</div>`;
        for (const o of node.children) html += optHtml(o);
      } else html += optHtml(node);
    }
    menu.innerHTML = html;
    // The player is full screen; menus must live inside it to stay visible.
    (btn.closest('.player') || document.body).append(menu);
    const r = btn.getBoundingClientRect();
    const below = innerHeight - r.bottom - 12, above = r.top - 12;
    const up = below < 220 && above > below;
    menu.style.minWidth = `${Math.max(r.width, 180)}px`;
    menu.style.maxHeight = `${Math.max(140, Math.min(380, up ? above : below))}px`;
    const left = Math.min(r.left, innerWidth - menu.offsetWidth - 8);
    menu.style.left = `${Math.max(8, left)}px`;
    if (up) menu.style.bottom = `${innerHeight - r.top + 6}px`; else menu.style.top = `${r.bottom + 6}px`;
    menu.classList.toggle('up', up);
    btn.setAttribute('aria-expanded', 'true');
    btn.classList.add('open');
    openMenu = { btn, menu };
    const opts = $$('.dd-opt:not([aria-disabled])', menu);
    const cur = $('.dd-opt[aria-selected=true]', menu) || opts[0];
    cur?.focus();
    cur?.scrollIntoView({ block: 'nearest' });
    menu.addEventListener('click', (e) => {
      const o = e.target.closest('.dd-opt');
      if (o && !o.hasAttribute('aria-disabled')) choose(sel.options[Number(o.dataset.i)]);
    });
    menu.addEventListener('keydown', (e) => {
      const i = opts.indexOf(document.activeElement);
      if (e.key === 'ArrowDown') { e.preventDefault(); opts[Math.min(opts.length - 1, i + 1)]?.focus(); }
      else if (e.key === 'ArrowUp') { e.preventDefault(); opts[Math.max(0, i - 1)]?.focus(); }
      else if (e.key === 'Home') { e.preventDefault(); opts[0]?.focus(); }
      else if (e.key === 'End') { e.preventDefault(); opts.at(-1)?.focus(); }
      else if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); if (i >= 0) choose(sel.options[Number(opts[i].dataset.i)]); }
      else if (e.key === 'Escape' || e.key === 'Tab') { e.preventDefault(); e.stopPropagation(); closeMenu(); btn.focus(); }
      else if (e.key.length === 1) {
        const k = e.key.toLowerCase();
        const n = opts.slice(i + 1).concat(opts.slice(0, i + 1)).find((o) => o.textContent.trim().toLowerCase().startsWith(k));
        n?.focus();
      }
    });
  };
  const optHtml = (o) => `<div class="dd-opt" role="option" tabindex="-1" data-i="${o.index}" aria-selected="${o.selected}"${o.disabled ? ' aria-disabled="true"' : ''}>
    <svg class="dd-check" viewBox="0 0 12 12" aria-hidden="true"><path d="M2.5 6.2l2.3 2.3 4.7-4.9" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"/></svg><span>${esc(o.textContent)}</span></div>`;

  btn.addEventListener('click', open);
  btn.addEventListener('keydown', (e) => {
    if (e.key === 'ArrowDown' || e.key === 'ArrowUp') { e.preventDefault(); open(); }
  });
}

// Enhance every select as it appears.
export function enhanceAll(root = document) {
  for (const s of root.querySelectorAll('select')) enhanceSelect(s);
}
new MutationObserver((muts) => {
  for (const m of muts) for (const n of m.addedNodes) {
    if (n.nodeType !== 1) continue;
    if (n.tagName === 'SELECT') enhanceSelect(n); else if (n.querySelector) enhanceAll(n);
  }
}).observe(document.documentElement, { childList: true, subtree: true });
