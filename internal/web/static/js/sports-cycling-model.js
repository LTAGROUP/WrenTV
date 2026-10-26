// Cycling dates may be date-only; an unknown start time is never turned into
// midnight local time. These helpers keep the calendar and stage rules testable.

export function localCyclingDay(date = new Date()) {
  return `${date.getFullYear()}-${String(date.getMonth() + 1).padStart(2, '0')}-${String(date.getDate()).padStart(2, '0')}`;
}

export function cyclingDate(value) {
  if (!value) return 'Date to be confirmed';
  const date = new Date(/^\d{4}-\d{2}-\d{2}$/.test(value) ? `${value}T12:00:00` : value);
  return Number.isNaN(date.getTime())
    ? 'Date to be confirmed'
    : date.toLocaleDateString([], { month: 'short', day: 'numeric', year: 'numeric' });
}

const validDay = (value) =>
  !!value && /^\d{4}-\d{2}-\d{2}$/.test(value) && !Number.isNaN(Date.parse(value)) &&
  new Date(value).toISOString().slice(0, 10) === value;

export function cyclingCalendarPeriod(race, today = localCyclingDay()) {
  if (!validDay(race?.startDate) || (race?.endDate && (!validDay(race.endDate) || race.endDate < race.startDate))) {
    return 'undated';
  }
  if (race.startDate > today) return 'upcoming';
  return (race.endDate || race.startDate) < today ? 'past' : 'current';
}

export function compareCyclingCalendar(a, b, today = localCyclingDay()) {
  const order = { current: 0, upcoming: 1, past: 2, undated: 3 };
  const ap = cyclingCalendarPeriod(a, today);
  const bp = cyclingCalendarPeriod(b, today);
  return (
    order[ap] - order[bp] ||
    (ap === 'past'
      ? (b.endDate || b.startDate || '').localeCompare(a.endDate || a.startDate || '')
      : (a.startDate || '').localeCompare(b.startDate || '')) ||
    String(a.name || '').localeCompare(String(b.name || ''))
  );
}

// Choose the latest dated stage on or before today, without claiming it has
// finished.
export function defaultCyclingStage(race, today = localCyclingDay()) {
  const stages = race?.stages || [];
  return [...stages].reverse().find((stage) => stage.date && stage.date <= today) || stages[0];
}

// The API addresses a race by its organizer id, not its full feed id.
export function cyclingRaceKey(raceId) {
  const parts = String(raceId || '').split(':');
  if (parts.length < 2) return '';
  return parts[1];
}

export function cyclingStageAddress(race, stage) {
  const key = cyclingRaceKey(race?.id);
  const year = String(race?.id || '').split(':').pop();
  const number = String(stage?.id || '').split(':').pop();
  if (!key || !/^\d{4}$/.test(year) || !/^\d+$/.test(number)) return '';
  return `/api/sports/cycling/${encodeURIComponent(key)}/${year}/${number}`;
}

// Calendar-only entries cannot load organizer results; the board stage is all
// that exists and the UI says so. A published organizer schedule can still be
// addressed by its own stage ids.
export function cyclingFetchable(race) {
  return !!race && !race.calendarOnly && cyclingRaceKey(race.id) !== '';
}

// A stage is finished when the organizer says so, its published result is
// already available, or its date is past for schedule-only coverage. A
// cancelled or still-upcoming stage is never called finished, and a stage on
// today's date is only finished once results exist.
export function cyclingStageFinished(stage, today = localCyclingDay()) {
  if (!stage || stage.status === 'cancelled' || stage.status === 'scheduled') return false;
  if (stage.status === 'final' || stage.status === 'completed') return true;
  if (stage.results?.state === 'available') return true;
  const date = String(stage.date || '').slice(0, 10);
  return validDay(date) && date < today;
}

export function cyclingStageName(stage) {
  if (!stage) return '';
  return [stage.name, stage.departure && stage.arrival ? `${stage.departure} → ${stage.arrival}` : '']
    .filter(Boolean)
    .join(' · ');
}

export function cyclingResultRows(module) {
  if (!module || module.state === 'unavailable' || module.state === 'pending') return [];
  return [...(module.data || [])].sort((a, b) => {
    const ap = a.rank || 0;
    const bp = b.rank || 0;
    if (ap === 0 && bp === 0) return 0;
    if (ap === 0) return 1;
    if (bp === 0) return -1;
    return ap - bp;
  });
}

export function cyclingModuleState(module) {
  if (!module) return 'unavailable';
  return module.state || 'unavailable';
}
