import test from 'node:test';
import assert from 'node:assert/strict';

import {
  addonCapabilitySummary,
  addonCatalogLabel,
  addonCatalogTypes,
  addonHasBrowseModel,
  addonItemWords,
} from './addon-model.js';

const caps = (catalogs, models, extra = {}) => ({ catalogs, models, ...extra });

test('an addon with only TV channels stays out of the catalog picker', () => {
  const source = {
    capabilities: caps(
      [{ type: 'tv', id: 'channels' }],
      [{ type: 'tv', model: 'channels', catalogs: 1 }],
    ),
  };
  assert.equal(addonHasBrowseModel(source), false);
});

test('sport, film and mixed addons are offered as sources', () => {
  assert.equal(addonHasBrowseModel({
    capabilities: caps([{ type: 'sport', id: 'live' }], [{ type: 'sport', model: 'events', catalogs: 1 }]),
  }), true);
  assert.equal(addonHasBrowseModel({
    capabilities: caps([{ type: 'movie', id: 'films' }], [{ type: 'movie', model: 'catalog', catalogs: 1 }]),
  }), true);
  assert.equal(addonHasBrowseModel({
    capabilities: caps(
      [{ type: 'tv', id: 'channels' }, { type: 'sport', id: 'live' }],
      [{ type: 'tv', model: 'channels', catalogs: 1 }, { type: 'sport', model: 'events', catalogs: 1 }],
    ),
  }), true);
});

test('an undiscovered addon is offered so its capabilities can be discovered', () => {
  assert.equal(addonHasBrowseModel({}), true);
  assert.equal(addonHasBrowseModel({ capabilities: null }), true);
});

test('a valid addon with no catalogs is not offered as a source', () => {
  assert.equal(addonHasBrowseModel({
    capabilities: caps([], [{ type: 'movie', model: 'catalog', catalogs: 0 }], { browsable: false }),
  }), false);
});

test('catalog labels add the content type only when the addon mixes types', () => {
  const single = [{ type: 'sport', id: 'live', name: 'Live Now' }];
  assert.equal(addonCatalogLabel(single[0], single), 'Live Now');
  const mixed = [...single, { type: 'movie', id: 'films', name: 'Films' }];
  assert.equal(addonCatalogLabel(single[0], mixed), 'Live Now · sport');
  assert.equal(addonCatalogLabel({ type: 'movie', id: 'films', name: 'Films' }, mixed), 'Films · movie');
  assert.deepEqual(addonCatalogTypes(mixed), ['sport', 'movie']);
});

test('the capability summary separates validity from usability', () => {
  const summary = addonCapabilitySummary(caps(
    [{ type: 'sport', id: 'a' }, { type: 'sport', id: 'b' }, { type: 'movie', id: 'c' }],
    [],
    { browsable: true, limitations: ['No TV channel catalogs.'] },
  ));
  assert.equal(summary.headline, 'Valid addon');
  assert.deepEqual(summary.details, ['2 sport catalogs', '1 movie catalog']);
  assert.deepEqual(summary.limitations, ['No TV channel catalogs.']);

  const empty = addonCapabilitySummary(caps([], [], { browsable: false }));
  assert.match(empty.headline, /nothing to browse/);
  assert.deepEqual(addonCapabilitySummary(null), { headline: 'Not checked yet', details: [], limitations: [] });
});

test('sports keep event words and other catalogs use item words', () => {
  assert.deepEqual(addonItemWords('sport'), { item: 'event', items: 'events', browse: 'Events', watch: 'Watch' });
  assert.equal(addonItemWords('movie').item, 'item');
  assert.equal(addonItemWords().items, 'items');
});
