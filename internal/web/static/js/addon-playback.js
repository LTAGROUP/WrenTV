// Automatic addon playback: one click, the best working stream.
//
// The server ranks an addon's feeds and proves each one starts. The player's
// share of that is the loop: ask for the best untried stream, try it, and ask
// again with the streams it has already tried. These helpers are pure, so the
// retry rules can be tested without a browser or a server.

// AUTO_ROUNDS bounds the loop. Every round either starts a stream or consumes
// at least one candidate the server hadn't tried, so this only guards against a
// misbehaving server, not a slow provider.
export const AUTO_ROUNDS = 8;

// Only these answers can be improved by asking again. A dead feed, a feed that
// timed out and an addon whose remaining feeds haven't been tried are worth
// another round; an unreachable addon, a refused request or a provider that is
// rate limiting this server would answer the same way — and another round would
// add to the limit — so the viewer gets the error instead of a loop.
const RETRYABLE = new Set(['no_working_stream', 'timeout']);

// mergeTried keeps the "already tried" list unique and in order. A stream that
// failed once in a playback attempt is never offered again.
export function mergeTried(tried = [], more = []) {
  const seen = new Set();
  const out = [];
  for (const key of [...(tried || []), ...(more || [])]) {
    const text = String(key ?? '').trim();
    if (!text || seen.has(text)) continue;
    seen.add(text);
    out.push(text);
  }
  return out;
}

export function autoMessage(round = 0) {
  return round <= 0 ? 'Finding stream…' : 'Trying another stream…';
}

// planAutoRound reads one round's answer and says what the player should do:
// play the returned feed, ask again, or stop with a message the viewer can act
// on. `response` is the endpoint's body; `error` is the thrown ApiError (or a
// local failure such as a stream that never started).
export function planAutoRound({ round = 0, tried = [], response = null, error = null, remaining = 0 } = {}) {
  if (response) {
    return {
      action: 'play',
      feed: response.feed || null,
      tried: mergeTried(tried, [response.feed?.key]),
      remaining: Math.max(0, Number(response.remaining ?? 0) || 0),
      message: autoMessage(round),
    };
  }
  const data = error?.data || {};
  const left = Math.max(0, Number(data.remaining ?? remaining) || 0);
  const code = String(error?.code || data.code || '');
  const next = mergeTried(tried, [...(data.tried || []), ...(error?.tried || [])]);
  const canRetry = RETRYABLE.has(code) && left > 0 && round + 1 < AUTO_ROUNDS;
  return {
    action: canRetry ? 'retry' : 'stop',
    feed: null,
    tried: next,
    remaining: left,
    message: error?.message || 'No stream from this addon could be played.',
  };
}
