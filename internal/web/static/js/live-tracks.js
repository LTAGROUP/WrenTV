// Track menus for live playback.
//
// A track control is only offered when the stream publishes a choice: one audio
// rendition, or a stream with no subtitle rendition, has nothing to select. That
// is the same rule MediaStorm's player HUD applies (Audio needs audioTracks,
// Subtitles needs a subtitle selector), and it keeps the menu from claiming a
// capability the playback pipeline does not have. A remuxed MPEG-TS channel is
// repackaged with a single AAC track (see internal/livetv/remux.go), so it
// deliberately offers neither.

function trackLabel(track, fallback) {
  const name = String(track?.name || track?.label || '').trim();
  const lang = String(track?.lang || track?.language || '').trim();
  if (name && lang) return `${name} (${lang})`;
  return name || lang || fallback;
}

// audioChoice lists the audio renditions a stream actually publishes. `visible`
// is false for 0 or 1 renditions: there is no alternative to pick.
export function audioChoice(tracks = []) {
  const options = (tracks || []).map((track, index) => ({
    index,
    label: trackLabel(track, `Audio ${index + 1}`),
  }));
  return { options, visible: options.length > 1 };
}

// subtitleChoice includes an explicit off entry, because turning subtitles off
// is a selection too. One published rendition is enough to offer the menu.
export function subtitleChoice(tracks = []) {
  const options = [
    { index: -1, label: 'Subtitles off' },
    ...(tracks || []).map((track, index) => ({
      index,
      label: trackLabel(track, `Subtitles ${index + 1}`),
    })),
  ];
  return { options, visible: options.length > 1 };
}
