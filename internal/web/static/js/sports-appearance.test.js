import test from 'node:test';
import assert from 'node:assert/strict';

import {
  SPORTS_APPEARANCES, sportsAppearance, sportsColorContrast, normalizeSportsReadability,
  applySportsAppearance, applySportsReadability, readSportsAppearance, saveSportsAppearance,
  readSportsReadability, saveSportsReadability, SPORTS_APPEARANCE_KEY,
} from './sports-appearance.js';

test('Midnight is the default and all six choices are distinct', () => {
  assert.equal(sportsAppearance(null).id, 'midnight');
  assert.equal(sportsAppearance('unknown').id, 'midnight');
  assert.equal(new Set(SPORTS_APPEARANCES.map((theme) => theme.id)).size, 6);
});

test('text and muted labels stay readable on every preset surface', () => {
  for (const theme of SPORTS_APPEARANCES.filter((item) => item.id !== 'wrentv')) {
    for (const background of [
      ...theme.page, ...theme.card, ...theme.focusedCard, theme.panel, theme.inset, theme.elevated,
    ]) {
      assert.ok(sportsColorContrast(theme.text, background) >= 4.5, `${theme.id} text on ${background}`);
      assert.ok(sportsColorContrast(theme.muted, background) >= 4.5, `${theme.id} muted on ${background}`);
    }
  }
});

test('cards separate from their page on every preset', () => {
  for (const theme of SPORTS_APPEARANCES.filter((item) => item.id !== 'wrentv')) {
    for (const card of theme.card) {
      for (const page of theme.page) assert.ok(sportsColorContrast(card, page) >= 1.25, theme.id);
    }
    assert.ok(sportsColorContrast(theme.focusedCard[0], theme.card[0]) >= 1.4, theme.id);
  }
});

test('readability defaults to off and only accepts true', () => {
  assert.deepEqual(normalizeSportsReadability(null), { largeText: false, reduceMotion: false, strongFocus: false });
  assert.deepEqual(normalizeSportsReadability({ largeText: true, reduceMotion: 'yes', strongFocus: 1 }), {
    largeText: true, reduceMotion: false, strongFocus: false,
  });
});

test('applies a preset as a data attribute and Wren TV clears it', () => {
  const root = {
    attrs: {},
    setAttribute(name, value) { this.attrs[name] = value; },
    removeAttribute(name) { delete this.attrs[name]; },
  };
  applySportsAppearance(root, 'graphite');
  assert.equal(root.attrs['data-sports-theme'], 'graphite');
  assert.equal(sportsAppearance('wrentv').name, 'Wren TV');
  applySportsAppearance(root, 'wrentv');
  assert.equal(root.attrs['data-sports-theme'], undefined);
});

test('applies readability classes without accumulating stale ones', () => {
  const classes = new Set();
  const root = { classList: { toggle(name, on) { on ? classes.add(name) : classes.delete(name); } } };
  applySportsReadability(root, { largeText: true, reduceMotion: true, strongFocus: false });
  assert.deepEqual([...classes].sort(), ['sports-readable-large', 'sports-reduce-motion']);
  applySportsReadability(root, { largeText: false });
  assert.deepEqual([...classes], []);
});

test('stores only the appearance choice and survives storage failure', () => {
  const store = new Map();
  globalThis.localStorage = {
    getItem: (key) => store.get(key) ?? null,
    setItem: (key, value) => store.set(key, String(value)),
  };
  try {
    assert.equal(readSportsAppearance(), 'midnight');
    assert.equal(saveSportsAppearance('evergreen'), 'evergreen');
    assert.equal(store.get(SPORTS_APPEARANCE_KEY), 'evergreen');
    assert.equal(readSportsAppearance(), 'evergreen');
    assert.equal(saveSportsAppearance('not-a-theme'), 'midnight');
    const saved = saveSportsReadability({ largeText: true, reduceMotion: true });
    assert.deepEqual(saved, { largeText: true, reduceMotion: true, strongFocus: false });
    assert.deepEqual(readSportsReadability(), saved);
  } finally {
    delete globalThis.localStorage;
  }
});
