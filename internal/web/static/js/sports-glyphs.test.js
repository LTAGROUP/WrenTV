import test from 'node:test';
import assert from 'node:assert/strict';

import { sportGlyph, SPORT_GLYPHS } from './sports-glyphs.js';

test('every catalog sport has a decorative glyph', () => {
  for (const sport of SPORT_GLYPHS) {
    const svg = sportGlyph(sport);
    assert.match(svg, /<svg class="sport-glyph"/, sport);
    assert.match(svg, /aria-hidden="true"/, sport);
    assert.match(svg, /focusable="false"/, sport);
    assert.match(svg, /stroke="currentColor"/, sport);
  }
});

test('provider sport aliases resolve to the same glyph', () => {
  assert.equal(sportGlyph('american-football'), sportGlyph('football'));
  assert.equal(sportGlyph('ice-hockey'), sportGlyph('hockey'));
  assert.equal(sportGlyph('racing'), sportGlyph('motorsport'));
  assert.equal(sportGlyph('f1'), sportGlyph('motorsport'));
  assert.equal(sportGlyph('mixed-martial-arts'), sportGlyph('mma'));
});

test('an unknown sport renders no guessed icon', () => {
  assert.equal(sportGlyph('quidditch'), '');
  assert.equal(sportGlyph(''), '');
  assert.equal(sportGlyph(undefined), '');
});
