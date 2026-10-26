import assert from 'node:assert/strict';
import test from 'node:test';
import {
  localCyclingDay, cyclingDate, cyclingCalendarPeriod, compareCyclingCalendar,
  defaultCyclingStage, cyclingRaceKey, cyclingStageAddress, cyclingFetchable,
  cyclingStageFinished, cyclingStageName, cyclingResultRows, cyclingModuleState,
} from './sports-cycling-model.js';

const today = '2026-09-24';

test('cycling dates keep date-only values honest', () => {
  assert.equal(cyclingDate(''), 'Date to be confirmed');
  assert.equal(cyclingDate(undefined), 'Date to be confirmed');
  assert.equal(cyclingDate('nope'), 'Date to be confirmed');
  assert.match(cyclingDate('2026-09-20'), /2026/);
  assert.match(localCyclingDay(new Date(2026, 0, 5)), /^2026-01-05$/);
});

test('calendar periods classify current, upcoming, past and undated', () => {
  assert.equal(cyclingCalendarPeriod({ startDate: '2026-09-20', endDate: '2026-09-27' }, today), 'current');
  assert.equal(cyclingCalendarPeriod({ startDate: '2026-10-01', endDate: '2026-10-05' }, today), 'upcoming');
  assert.equal(cyclingCalendarPeriod({ startDate: '2026-08-01', endDate: '2026-08-05' }, today), 'past');
  assert.equal(cyclingCalendarPeriod({ startDate: '2026-09-01', endDate: '2026-08-01' }, today), 'undated');
  assert.equal(cyclingCalendarPeriod({}, today), 'undated');
  assert.equal(cyclingCalendarPeriod({ startDate: '2026-13-01' }, today), 'undated');
});

test('calendar ordering leads with current and upcoming races', () => {
  const races = [
    { name: 'Past', startDate: '2026-08-01', endDate: '2026-08-05' },
    { name: 'Upcoming', startDate: '2026-10-01' },
    { name: 'Current', startDate: '2026-09-20', endDate: '2026-09-27' },
  ].sort((a, b) => compareCyclingCalendar(a, b, today));
  assert.deepEqual(races.map((race) => race.name), ['Current', 'Upcoming', 'Past']);
});

test('finished stages follow the organizer, results or a past date', () => {
  assert.equal(cyclingStageFinished({ status: 'unknown', date: '2026-09-23' }, today), true);
  assert.equal(cyclingStageFinished({ status: 'scheduled', date: '2026-09-23' }, today), false);
  assert.equal(cyclingStageFinished({ status: 'cancelled', date: '2026-09-23' }, today), false);
  assert.equal(cyclingStageFinished({ status: 'unknown', date: today }, today), false);
  assert.equal(cyclingStageFinished({ status: 'final', date: today }, today), true);
  assert.equal(cyclingStageFinished({ status: 'completed', date: today }, today), true);
  // Today's stage is only finished once the organizer publishes a result.
  assert.equal(cyclingStageFinished({ status: 'unknown', date: today, results: { state: 'available' } }, today), true);
  assert.equal(cyclingStageFinished({ status: 'unknown', date: today, results: { state: 'stale' } }, today), false);
  assert.equal(cyclingStageFinished({ status: 'unknown', date: 'not a date' }, today), false);
  assert.equal(cyclingStageFinished(undefined, today), false);
});

test('stage selection never claims a future stage has finished', () => {
  const race = {
    stages: [
      { id: 'r:2026:1', date: '2026-09-20', status: 'unknown' },
      { id: 'r:2026:2', date: '2026-09-21', status: 'unknown' },
      { id: 'r:2026:3', date: '2026-09-26', status: 'scheduled' },
    ],
  };
  assert.equal(defaultCyclingStage(race, today).id, 'r:2026:2');
  assert.equal(defaultCyclingStage(race, '2026-09-19').id, 'r:2026:1');
  assert.equal(defaultCyclingStage({ stages: [] }, today), undefined);
});

test('stage addresses target the organizer API', () => {
  const race = { id: 'aso:paris-nice:2026' };
  const stage = { id: 'aso:paris-nice:2026:7' };
  assert.equal(cyclingRaceKey(race.id), 'paris-nice');
  assert.equal(cyclingStageAddress(race, stage), '/api/sports/cycling/paris-nice/2026/7');
  assert.equal(cyclingStageAddress({ id: 'calendar:road-abc:2026' }, stage), '/api/sports/cycling/road-abc/2026/7');
  assert.equal(cyclingStageAddress({ id: 'bad' }, stage), '');
  assert.equal(cyclingFetchable({ id: 'aso:tour:2026' }), true);
  assert.equal(cyclingFetchable({ id: 'aso:tour:2026', calendarOnly: true }), false);
  assert.equal(cyclingFetchable({ id: 'aso:tour:2026', scheduleOnly: true }), true);
});

test('stage summaries and result order', () => {
  assert.equal(cyclingStageName({ name: 'Stage 7', departure: 'Nice', arrival: 'Isola' }), 'Stage 7 · Nice → Isola');
  const rows = cyclingResultRows({ state: 'available', data: [{ rank: 0, name: 'No rank' }, { rank: 2, name: 'Second' }, { rank: 1, name: 'First' }] });
  assert.deepEqual(rows.map((row) => row.name), ['First', 'Second', 'No rank']);
  assert.deepEqual(cyclingResultRows({ state: 'unavailable', data: [{ rank: 1 }] }), []);
  assert.deepEqual(cyclingResultRows({ state: 'pending' }), []);
  assert.equal(cyclingModuleState(undefined), 'unavailable');
  assert.equal(cyclingModuleState({ state: 'stale' }), 'stale');
});
