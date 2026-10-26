// Football drive replay and perspective-field geometry. Ported from
// MediaStorm's football-replay.ts: coordinates are the provider's 0–100 field,
// possession decides the attacking goal, and anything the provider did not
// supply stays absent instead of being guessed.

export function classifyFootballPlay(play) {
  const type = String(play?.type || '').toLowerCase();
  if (/\bno play\b/i.test(play?.description || '') || type.includes('penalty')) return 'penalty';
  const id = play?.typeId;
  if (id === '26' || /interception|fumble/.test(type)) return 'turnover';
  if (id === '7' || type.includes('sack')) return 'sack';
  if (id === '3' || /incompletion|incomplete/.test(type)) return 'incomplete';
  if (['24', '67'].includes(id ?? '') || /pass reception|passing touchdown|pass complete/.test(type)) return 'pass';
  if (['5', '68'].includes(id ?? '') || /rush|run/.test(type)) return 'rush';
  if (/kick|punt|field goal/.test(type)) return 'kick';
  return 'other';
}

const validYard = (value) => typeof value === 'number' && Number.isFinite(value) && value >= 0 && value <= 100;

export function projectFieldPoint(yard, lane) {
  const left = 110 - 70 * lane;
  const right = 730 + 70 * lane;
  return { x: left + ((right - left) * yard) / 100, y: 25 + 170 * lane };
}

// A trace needs both endpoints inside the field and a play that advances the
// ball; a penalty or an absent coordinate stays undrawable.
export function footballReplayPath(play) {
  const kind = classifyFootballPlay(play);
  if (!['pass', 'rush'].includes(kind) || !validYard(play?.start) || !validYard(play?.end)) return undefined;
  const start = projectFieldPoint(play.start, 0.5);
  const end = projectFieldPoint(play.end, 0.5);
  return { kind, start, end, control: { x: (start.x + end.x) / 2, y: kind === 'pass' ? 35 : 110 } };
}

export function resolveReplayPlay(detail, selection) {
  const drives = detail?.footballDrives || [];
  if (selection?.mode === 'replay') {
    return drives.find((drive) => drive.id === selection.driveId)?.plays?.find((play) => play.id === selection.playId);
  }
  const plays = (drives.find((drive) => drive.current) || drives[drives.length - 1])?.plays;
  return plays?.[plays.length - 1];
}

// Coordinates stay away-to-home; possession determines the goal, never the
// play's gain or loss.
export function footballReplaySituation(play, driveTeamId, awayId, homeId) {
  const teamId = play?.possessionTeamId || driveTeamId;
  const side = teamId && teamId === awayId ? 'away' : teamId && teamId === homeId ? 'home' : undefined;
  const direction = side === 'away' ? 1 : side === 'home' ? -1 : 0;
  const yard = validYard(play?.start) ? play.start : undefined;
  const distance = play?.distance;
  const firstDown =
    yard !== undefined && direction && typeof distance === 'number' && Number.isFinite(distance) && distance > 0
      ? Math.max(0, Math.min(100, yard + direction * distance))
      : undefined;
  const ownYard = yard !== undefined && direction ? (direction === 1 ? yard : 100 - yard) : undefined;
  const position =
    ownYard === undefined
      ? undefined
      : ownYard === 50
        ? 'Midfield'
        : ownYard < 50
          ? `Own ${Math.round(ownYard)}`
          : `Opponent ${Math.round(100 - ownYard)}`;
  const down = play?.down;
  const downLabel =
    down &&
    down >= 1 &&
    down <= 4 &&
    Number.isInteger(down) &&
    typeof distance === 'number' &&
    Number.isFinite(distance) &&
    distance >= 0
      ? `${['1st', '2nd', '3rd', '4th'][down - 1]} & ${yard !== undefined && direction && distance >= (direction === 1 ? 100 - yard : yard) ? 'Goal' : distance}`
      : undefined;
  return { side, direction, yard, firstDown, position, downLabel };
}

// Never step backward over a non-drawable event or use a later snapshot play.
export function previousPlayTrail(detail, playId) {
  const plays = (detail?.footballDrives || []).flatMap((drive) =>
    (drive.plays || []).map((play) => ({ play, teamId: play.possessionTeamId || drive.teamId })),
  );
  const index = plays.findIndex((item) => item.play.id === playId);
  return index > 0 ? plays[index - 1] : undefined;
}

// Quadratic subdivision reveals the trace up to the animated ball.
export function footballAnimatedPath(path, progress) {
  const t = Math.max(0, Math.min(1, progress));
  const control = {
    x: path.start.x + t * (path.control.x - path.start.x),
    y: path.start.y + t * (path.control.y - path.start.y),
  };
  const end = {
    x: (1 - t) ** 2 * path.start.x + 2 * (1 - t) * t * path.control.x + t ** 2 * path.end.x,
    y: (1 - t) ** 2 * path.start.y + 2 * (1 - t) * t * path.control.y + t ** 2 * path.end.y,
  };
  return { end, d: `M${path.start.x},${path.start.y} Q${control.x},${control.y} ${end.x},${end.y}` };
}

// Summarize only supplied results; never infer a catch position for an
// incompletion.
export function footballPlayResult(play) {
  const kind = classifyFootballPlay(play);
  if (kind === 'incomplete') return 'Incomplete pass';
  if (kind !== 'pass' && kind !== 'rush') return play?.type || '';
  const yards = typeof play?.yards === 'number' && Number.isFinite(play.yards) ? play.yards : undefined;
  const result = kind === 'pass' ? 'completion' : 'run';
  const label = yards === undefined ? (kind === 'pass' ? 'Completed pass' : 'Run') : `${yards}-yard ${result}`;
  if (/touchdown/i.test(play?.type || '')) return `${label} · Touchdown`;
  if (play?.scoring) return `${label} · Scoring play`;
  if (yards !== undefined && typeof play?.distance === 'number' && play.distance > 0 && yards >= play.distance)
    return `${label} · First down`;
  return label;
}
