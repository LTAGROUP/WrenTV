import test from 'node:test';
import assert from 'node:assert/strict';
import { audioChoice, subtitleChoice } from './live-tracks.js';

test('a stream with one audio rendition offers no audio menu', () => {
  const choice = audioChoice([{ name: 'English', lang: 'en' }]);
  assert.equal(choice.visible, false);
  assert.equal(choice.options.length, 1);
});

test('a stream with no renditions offers neither menu', () => {
  assert.equal(audioChoice([]).visible, false);
  assert.equal(subtitleChoice([]).visible, false);
});

test('two audio renditions are selectable and named with their language', () => {
  const choice = audioChoice([
    { name: 'English', lang: 'en' },
    { name: 'Spanish', lang: 'es' },
  ]);
  assert.equal(choice.visible, true);
  assert.deepEqual(choice.options, [
    { index: 0, label: 'English (en)' },
    { index: 1, label: 'Spanish (es)' },
  ]);
});

test('an unnamed rendition still gets a stable label', () => {
  const choice = audioChoice([{}, {}]);
  assert.deepEqual(choice.options.map((option) => option.label), ['Audio 1', 'Audio 2']);
});

test('a named rendition keeps its language next to the name', () => {
  assert.equal(audioChoice([{ name: 'English', lang: 'en' }, { name: 'Commentary en', lang: 'en' }]).options[1].label, 'Commentary en (en)');
});

test('subtitles are offered as off plus every published rendition', () => {
  const choice = subtitleChoice([{ name: 'English', lang: 'en' }]);
  assert.equal(choice.visible, true);
  assert.deepEqual(choice.options, [
    { index: -1, label: 'Subtitles off' },
    { index: 0, label: 'English (en)' },
  ]);
});
