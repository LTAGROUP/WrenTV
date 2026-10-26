import test from 'node:test';
import assert from 'node:assert/strict';

import { AUTO_ROUNDS, autoMessage, mergeTried, planAutoRound } from './addon-playback.js';

const apiError = (code, message, data = {}) => Object.assign(new Error(message), { code, data });

test('the first suitable stream plays without a second round', () => {
  const plan = planAutoRound({
    response: { feed: { key: 'main#0', name: 'Main', quality: 1080 }, remaining: 1 },
  });
  assert.equal(plan.action, 'play');
  assert.equal(plan.message, 'Finding stream…');
  assert.deepEqual(plan.tried, ['main#0']);
  assert.equal(plan.remaining, 1);
  assert.equal(plan.feed.name, 'Main');
});

test('a failed stream is skipped and the next one is asked for', () => {
  const plan = planAutoRound({
    round: 0,
    error: apiError('no_working_stream', 'Tried 1 stream, but none would start.', {
      tried: ['main#0'],
      remaining: 1,
    }),
  });
  assert.equal(plan.action, 'retry');
  assert.deepEqual(plan.tried, ['main#0']);
  assert.equal(plan.remaining, 1);
  assert.equal(autoMessage(1), 'Trying another stream…');
});

test('a stream that times out is replaced rather than waited on', () => {
  const plan = planAutoRound({
    round: 1,
    tried: ['main#0'],
    error: apiError('timeout', 'Finding a stream that starts is taking too long.', {
      tried: ['main#0', 'backup#0'],
      remaining: 2,
    }),
  });
  assert.equal(plan.action, 'retry');
  assert.deepEqual(plan.tried, ['main#0', 'backup#0']);
  assert.equal(plan.remaining, 2);
});

test('an addon with no playable feed is a final answer, not a retry', () => {
  const plan = planAutoRound({
    error: apiError('no_streams', 'The addon has no playable streams for this item right now.', {
      remaining: 0,
    }),
  });
  assert.equal(plan.action, 'stop');
  assert.match(plan.message, /no playable streams/);
});

test('when every stream has failed the viewer gets the error and a stop', () => {
  const plan = planAutoRound({
    round: 2,
    tried: ['main#0', 'backup#0'],
    error: apiError('no_working_stream', 'Tried 2 streams, but none would start.', {
      tried: ['main#0', 'backup#0'],
      remaining: 0,
    }),
  });
  assert.equal(plan.action, 'stop');
  assert.equal(plan.remaining, 0);
  assert.match(plan.message, /Tried 2 streams/);
});

test('a rate-limited provider stops the attempt, with the wait it asked for', () => {
  const plan = planAutoRound({
    round: 1,
    tried: ['main#0'],
    remaining: 4,
    error: apiError('rate_limited', 'the addon is rate limiting this server (429 Too Many Requests); it asked to wait 30s', {
      remaining: 4,
    }),
  });
  assert.equal(plan.action, 'stop');
  assert.match(plan.message, /rate limiting/);
  assert.match(plan.message, /30s/);
  // More feeds were left untried, and none of them is asked for: they would be
  // answered from the same limit.
  assert.deepEqual(plan.tried, ['main#0']);
});

test('an unreachable addon is not hammered with more rounds', () => {
  const plan = planAutoRound({
    error: apiError('addon_unreachable', 'The addon is unavailable.', { remaining: 3 }),
  });
  assert.equal(plan.action, 'stop');
  assert.match(plan.message, /unavailable/);
});

test('a stream the browser cannot start falls back to the remaining feeds', () => {
  const failing = planAutoRound({
    tried: ['main#0'],
    error: apiError('no_working_stream', 'That stream did not start.', { remaining: 2 }),
    remaining: 2,
  });
  assert.equal(failing.action, 'retry');

  const last = planAutoRound({
    tried: ['main#0'],
    error: apiError('no_working_stream', 'That stream did not start.', { remaining: 0 }),
  });
  assert.equal(last.action, 'stop');
});

test('the retry loop is bounded', () => {
  const plan = planAutoRound({
    round: AUTO_ROUNDS - 1,
    error: apiError('no_working_stream', 'Still trying.', { remaining: 5 }),
    remaining: 5,
  });
  assert.equal(plan.action, 'stop');
});

test('tried keys are unique and keep their order', () => {
  assert.deepEqual(mergeTried(['a', 'b'], ['b', '', '  ', 'c', 'a']), ['a', 'b', 'c']);
  const plan = planAutoRound({
    tried: ['a'],
    error: apiError('no_working_stream', 'x', { tried: ['a', 'b', 'b'] }),
    remaining: 1,
  });
  assert.deepEqual(plan.tried, ['a', 'b']);
});
