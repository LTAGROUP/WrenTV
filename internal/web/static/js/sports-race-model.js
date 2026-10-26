// Race weekends and sessions use the generalized event contract. These helpers
// stay DOM-free so the rules — session selection, classification order, circuit
// geometry and archive parsing — are unit-tested.

export function raceSessionList(weekend) {
  if (!weekend) return [];
  const sessions = weekend.subEvents || [];
  if (sessions.length) return sessions;
  // A weekend with no published sessions has none; a bare session object is
  // its own list.
  if (weekend.eventKind === 'race') return [];
  return weekend.eventKind === 'race-session' || weekend.sessionType ? [weekend] : [];
}

// A hub card may point at a weekend or one of its sessions.
export function resolveRaceSelection(events, id) {
  for (const event of events || []) {
    if (event.id === id) {
      return { event, session: event.eventKind === 'race-session' ? event : defaultRaceSession(event) };
    }
    const session = (event.subEvents || []).find((item) => item.id === id);
    if (session) return { event, session };
  }
  return undefined;
}

// Prefer a live session, then the main race, then the latest session. A
// scheduled weekend never pretends practice is the race.
export function defaultRaceSession(weekend) {
  const sessions = raceSessionList(weekend);
  if (!sessions.length) return undefined;
  return (
    sessions.find((session) => session.status === 'live') ||
    [...sessions].reverse().find((session) => /^race\s*\d*$/i.test(session.sessionType || '')) ||
    sessions[sessions.length - 1]
  );
}

export function raceDate(value) {
  const date = new Date(value);
  return Number.isNaN(date.getTime())
    ? 'Date unavailable'
    : date.toLocaleString([], { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' });
}

export function sessionLabel(session) {
  if (!session) return '';
  const kind = session.sessionType || (session.eventKind === 'race' ? 'Race weekend' : 'Session');
  return `${kind} · ${raceDate(session.startTime)}`;
}

// Classification order uses the provider position only. Missing positions stay
// at the end in provider order; array order is never promoted to a rank.
export function raceClassification(participants) {
  return [...(participants || [])]
    .map((driver, index) => ({ driver, index }))
    .sort((a, b) => {
      const ap = a.driver.position || 0;
      const bp = b.driver.position || 0;
      if (ap === 0 && bp === 0) return a.index - b.index;
      if (ap === 0) return 1;
      if (bp === 0) return -1;
      return ap - bp || a.index - b.index;
    })
    .map(({ driver }) => driver);
}

export function driverSubtitle(driver) {
  const bits = [];
  if (driver.number) bits.push(`#${driver.number}`);
  if (driver.team) bits.push(driver.team);
  if (driver.result) bits.push(driver.result);
  return bits.join(' · ');
}

// Circuit geometry is reference data. The path is normalized into an SVG
// viewBox; fewer than three valid points yields no drawing rather than a guess.
export function circuitPath(circuit, size = 100, pad = 6) {
  const points = (circuit?.coordinates || []).filter(
    (point) => Array.isArray(point) && point.length === 2 && Number.isFinite(point[0]) && Number.isFinite(point[1]),
  );
  if (points.length < 3) return { path: '', width: size, height: size };
  const xs = points.map((point) => point[0]);
  const ys = points.map((point) => point[1]);
  const minX = Math.min(...xs);
  const maxX = Math.max(...xs);
  const minY = Math.min(...ys);
  const maxY = Math.max(...ys);
  const spanX = maxX - minX || 1;
  const spanY = maxY - minY || 1;
  const scale = (size - pad * 2) / Math.max(spanX, spanY);
  const offsetX = pad + ((size - pad * 2) - spanX * scale) / 2;
  const offsetY = pad + ((size - pad * 2) - spanY * scale) / 2;
  // Chart y grows downward while latitude grows north; invert it.
  const path = points
    .map((point, index) => {
      const x = offsetX + (point[0] - minX) * scale;
      const y = offsetY + (maxY - point[1]) * scale;
      return `${index ? 'L' : 'M'}${x.toFixed(2)},${y.toFixed(2)}`;
    })
    .join(' ');
  return { path, width: size, height: size };
}

const object = (value) => (value && typeof value === 'object' ? value : {});
const text = (value) => (typeof value === 'string' || typeof value === 'number' ? String(value) : '');
const count = (value) => {
  if (value === '' || value == null || typeof value === 'boolean') return undefined;
  const number = Number(value);
  return Number.isFinite(number) && number >= 0 ? number : undefined;
};

// F1 archive timing is derived only from the archived channels. Missing values
// stay missing; archive order is the only order used.
export function archiveDrivers(archive) {
  const channels = object(archive?.channels);
  const list = object(channels.DriverList);
  const timing = object(object(channels.TimingData).Lines);
  const app = object(object(channels.TimingAppData).Lines);
  return Object.entries(list)
    .filter(([, value]) => !!object(value).RacingNumber)
    .map(([id, raw]) => {
      const driver = object(raw);
      const line = object(timing[id]);
      return {
        id,
        name: text(driver.FullName),
        abbreviation: text(driver.Tla),
        team: text(driver.TeamName),
        position: count(line.Position),
        laps: count(line.NumberOfLaps),
        knockedOut: line.KnockedOut === true,
        bestLap: text(object(line.BestLapTime).Value),
        lastLap: text(object(line.LastLapTime).Value),
        interval: text(object(line.IntervalToPositionAhead).Value),
        pitStops: count(line.NumberOfPitStops),
        retired: line.Retired === true,
        stopped: line.Stopped === true,
        gap: text(line.GapToLeader),
        inPit: line.InPit === true,
        stints: Object.entries(object(object(app[id]).Stints)).map(([key, value]) => ({
          id: key,
          compound: text(object(value).Compound) || 'UNKNOWN',
          age: count(object(value).TotalLaps),
        })),
      };
    })
    .sort((a, b) => (a.position ?? 999) - (b.position ?? 999));
}

export function archiveMessages(archive) {
  return Object.entries(object(object(archive?.channels?.RaceControlMessages).Messages))
    .map(([id, value]) => {
      const message = object(value);
      return {
        id,
        message: text(message.Message),
        lap: count(message.Lap),
        utc: text(message.Utc),
        flag: text(message.Flag),
        category: text(message.Category),
      };
    })
    .filter((message) => message.message)
    .sort((a, b) => b.utc.localeCompare(a.utc) || Number(b.id) - Number(a.id));
}

export function archiveWeather(archive) {
  const weather = object(archive?.channels?.WeatherData);
  return [
    weather.AirTemp != null ? `Air ${text(weather.AirTemp)}°C` : '',
    weather.TrackTemp != null ? `Track ${text(weather.TrackTemp)}°C` : '',
    text(weather.Rainfall) === '1'
      ? 'Rain reported'
      : text(weather.Rainfall) === '0'
        ? 'No rainfall reported'
        : '',
  ]
    .filter(Boolean)
    .join(' · ');
}

// A final session can offer archive timing; a live session never can.
export function archiveAvailable(session) {
  return !!session && session.status === 'final' && session.league === 'f1';
}
