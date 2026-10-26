import assert from 'node:assert/strict';
import test from 'node:test';
import {
  resolveRaceSelection, defaultRaceSession, raceSessionList, raceDate,
  raceClassification, circuitPath, archiveDrivers, archiveMessages,
  archiveWeather, archiveAvailable, driverSubtitle,
} from './sports-race-model.js';

const sessions = [
  { id: 'w:1', sessionType: 'FP1', status: 'final', startTime: '2026-09-04T10:30:00Z' },
  { id: 'w:2', sessionType: 'Qual', status: 'final', startTime: '2026-09-05T14:00:00Z' },
  { id: 'w:3', sessionType: 'Race', status: 'scheduled', startTime: '2026-09-06T13:00:00Z' },
];
const weekend = { id: 'w', league: 'f1', eventKind: 'race', title: 'Italian Grand Prix', subEvents: sessions };

test('race selection resolves weekends and sessions', () => {
  assert.equal(resolveRaceSelection([weekend], 'w').session.id, 'w:3');
  assert.equal(resolveRaceSelection([weekend], 'w:2').session.id, 'w:2');
  assert.equal(resolveRaceSelection([weekend], 'missing'), undefined);
  assert.deepEqual(raceSessionList(weekend).map((s) => s.id), ['w:1', 'w:2', 'w:3']);
  assert.deepEqual(raceSessionList(sessions[0]).map((s) => s.id), ['w:1']);
});

test('default session prefers live, then the main race, then the latest', () => {
  const live = { ...weekend, subEvents: [{ ...sessions[0], status: 'live' }, ...sessions.slice(1)] };
  assert.equal(defaultRaceSession(live).sessionType, 'FP1');
  assert.equal(defaultRaceSession(weekend).sessionType, 'Race');
  const practiceOnly = { ...weekend, subEvents: sessions.slice(0, 2) };
  assert.equal(defaultRaceSession(practiceOnly).sessionType, 'Qual');
  assert.equal(defaultRaceSession({ subEvents: [] }), undefined);
});

test('race date is labelled and never invented', () => {
  assert.match(raceDate('2026-09-06T13:00:00Z'), /Sep/);
  assert.equal(raceDate('not a date'), 'Date unavailable');
});

test('classification uses provider position only', () => {
  const rows = raceClassification([
    { name: 'No position A', position: 0 },
    { name: 'Third', position: 3 },
    { name: 'Winner', position: 1 },
    { name: 'No position B' },
  ]);
  assert.deepEqual(rows.map((row) => row.name), ['Winner', 'Third', 'No position A', 'No position B']);
  assert.equal(driverSubtitle({ number: '16', team: 'Ferrari', result: 'Finished' }), '#16 · Ferrari · Finished');
});

test('circuit path normalizes verified geometry', () => {
  const circuit = { coordinates: [[9.28, 45.61], [9.29, 45.63], [9.30, 45.62]] };
  const track = circuitPath(circuit, 100, 6);
  assert.equal(track.path.split(' ').length, 3);
  assert.ok(track.path.startsWith('M'));
  assert.equal(track.width, 100);
  assert.equal(circuitPath({ coordinates: [[1, 2]] }).path, '');
  assert.equal(circuitPath({ coordinates: [[1, 2], ['x', 3], [4, 5]] }).path, '');
});

test('archive helpers read only provider channels', () => {
  const archive = {
    state: 'available',
    sessionName: 'Race',
    checkedAt: '2026-09-06T16:00:00Z',
    channels: {
      DriverList: {
        p1: { RacingNumber: '16', FullName: 'Charles Leclerc', Tla: 'LEC', TeamName: 'Ferrari' },
        p2: { RacingNumber: '1', FullName: 'Max Verstappen', Tla: 'VER', TeamName: 'Red Bull' },
        p3: { FullName: 'Not a driver' },
      },
      TimingData: {
        Lines: {
          p1: { Position: '2', NumberOfLaps: 53, LastLapTime: { Value: '1:22.000' }, NumberOfPitStops: 1 },
          p2: { Position: '1', NumberOfLaps: 53, LastLapTime: { Value: '1:21.500' }, NumberOfPitStops: 0, Retired: false },
        },
      },
      TimingAppData: { Lines: {} },
      RaceControlMessages: {
        Messages: {
          1: { Message: 'Track clear', Lap: '3', Utc: '2026-09-06T13:10:00' },
          2: { Message: '', Lap: '4', Utc: '2026-09-06T13:11:00' },
          3: { Message: 'Yellow flag', Lap: '5', Utc: '2026-09-06T13:12:00' },
        },
      },
      WeatherData: { AirTemp: '24', TrackTemp: '38', Rainfall: '1' },
    },
  };
  const drivers = archiveDrivers(archive);
  assert.deepEqual(drivers.map((driver) => driver.name), ['Max Verstappen', 'Charles Leclerc']);
  assert.equal(drivers[0].position, 1);
  assert.equal(drivers[0].pitStops, 0);
  assert.equal(drivers[1].lastLap, '1:22.000');
  const messages = archiveMessages(archive);
  assert.deepEqual(messages.map((message) => message.message), ['Yellow flag', 'Track clear']);
  assert.equal(archiveWeather(archive), 'Air 24°C · Track 38°C · Rain reported');
  assert.equal(archiveWeather({ channels: {} }), '');
  assert.equal(archiveAvailable({ league: 'f1', status: 'final' }), true);
  assert.equal(archiveAvailable({ league: 'f1', status: 'live' }), false);
  assert.equal(archiveAvailable({ league: 'nascar', status: 'final' }), false);
  assert.deepEqual(archiveDrivers(undefined), []);
  assert.deepEqual(archiveMessages({}), []);
});
