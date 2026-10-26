// Sport-specific presentation rules: tennis sets, rugby timelines and combat
// cards. Every helper avoids inventing data the provider did not send.

const superscripts = '⁰¹²³⁴⁵⁶⁷⁸⁹';

// Superscript tiebreak points, including a reported zero.
export function tennisTiebreakSuperscript(points) {
  if (points == null || !Number.isInteger(points) || points < 0) return '';
  return String(points).replace(/[0-9]/g, (digit) => superscripts[Number(digit)]);
}

export function tennisEventContext(event) {
  if (!['atp', 'wta'].includes(event?.league)) return undefined;
  if (event.eventContext?.trim()) return event.eventContext.trim();
  // Older backends prefix the matchup with its tournament but omit structured
  // context.
  const participants = event.participants || [];
  const suffix = `: ${participants[0]?.name} vs ${participants[1]?.name}`;
  return event.title?.endsWith(suffix) ? event.title.slice(0, -suffix.length) : undefined;
}

export function tennisSets(event) {
  return (event?.detail?.periods || []).map((period) => ({
    label: period.label,
    away: period.away,
    home: period.home,
    awayTiebreak: period.awayTiebreak,
    homeTiebreak: period.homeTiebreak,
    awayTiebreakLabel: tennisTiebreakSuperscript(period.awayTiebreak),
    homeTiebreakLabel: tennisTiebreakSuperscript(period.homeTiebreak),
    awayWinner: period.awayWinner,
    homeWinner: period.homeWinner,
  }));
}

// Labels describe provider events, never inferred points or active sin-bin
// timers.
export function rugbyPlaySymbol(title) {
  const text = String(title || '').toLowerCase();
  if (/miss|unsuccessful|disallow|no try/.test(text)) return '×';
  if (/yellow card/.test(text)) return 'YC';
  if (/red card/.test(text)) return 'RC';
  if (/substitut|replacement/.test(text)) return '↔';
  if (/penalty try/.test(text)) return 'PT';
  if (/\btry\b/.test(text)) return 'T';
  if (/conversion/.test(text)) return 'C';
  if (/penalty goal/.test(text)) return 'PG';
  if (/field goal/.test(text)) return 'FG';
  if (/drop goal/.test(text)) return 'DG';
  return '•';
}

// Only sort when every clock is usable; otherwise preserve provider order.
export function rugbyLatestPlays(plays) {
  const minute = (play) => {
    const match = /^(\d+)(?:\+(\d+))?(?:['′]|$)/.exec(String(play?.clock || '').trim());
    return match ? Number(match[1]) + Number(match[2] ?? 0) : undefined;
  };
  const list = plays || [];
  if (!list.every((play) => minute(play) !== undefined)) return [...list];
  return list
    .map((play, index) => ({ play, index }))
    .sort((a, b) => minute(b.play) - minute(a.play) || a.index - b.index)
    .map((entry) => entry.play);
}

export function rugbyStatus(event) {
  const detail = event?.detail || {};
  const labels = { '1st': 'First half', '2nd': 'Second half', '3rd': 'Extra time · first half', '4th': 'Extra time · second half' };
  const lifecycle = event?.status || 'scheduled';
  if (lifecycle === 'final') return 'Full time';
  if (lifecycle === 'scheduled') return event?.startTime ? 'Scheduled' : 'Scheduled';
  if (/^(?:ht|half[- ]?time)$/i.test(String(event?.statusDetail || '').trim())) return 'Half time';
  const phase = labels[detail.periodLabel || ''] || detail.periodLabel || event?.period;
  return [phase, event?.clock].filter(Boolean).join(' · ') || 'Live';
}

// ESPN MMA headshots are numbered per sport, so a numeric id is only turned
// into a headshot URL for UFC. A provider-supplied image always wins, except
// the team-logo placeholder that would render a crest for a fighter.
export function combatPortrait(event, athlete) {
  if (athlete?.logoUrl && !/\/teamlogos\//i.test(athlete.logoUrl)) return athlete.logoUrl;
  return event?.league === 'ufc' && /^\d+$/.test(String(athlete?.id || ''))
    ? `https://a.espncdn.com/i/headshots/mma/players/full/${athlete.id}.png`
    : undefined;
}

// Tale of the tape: only provider profile values, and only rows where at least
// one fighter reports one.
export function combatTape(event) {
  const profiles = event?.detail?.fighterProfiles || [];
  const participants = event?.participants || [];
  const value = (team, label) =>
    profiles.find((profile) => profile.id === team?.id)?.stats?.find((stat) => stat.label === label)?.value || '';
  return ['Height', 'Reach', 'Weight', 'Stance']
    .map((label) => ({ label, away: value(participants[0], label), home: value(participants[1], label) }))
    .filter((row) => row.away || row.home);
}

export function combatBouts(event) {
  return event?.combat?.bouts || [];
}

export function combatResult(event, hidden = false) {
  if (hidden) return 'Result hidden';
  if (event?.status === 'scheduled') return 'Scheduled';
  if (event?.status !== 'final') {
    return [event?.period, event?.clock].filter((value) => value && !/^[-–—]+$/.test(value)).join(' · ') || 'Live bout';
  }
  const participants = event?.participants || [];
  const winners = participants.filter((participant) => participant.winner);
  const detail = String(event?.statusDetail || '').trim();
  return [
    winners.length === 1 ? `${winners[0].name} wins` : 'Result not supplied',
    detail && !/^final$/i.test(detail) ? detail : '',
    event?.period,
    event?.clock,
  ]
    .filter(Boolean)
    .join(' · ');
}

// Comparable totals only: ratios, percentages and absent values are not counts.
export function combatBarValues(label, away, home) {
  const parse = (value) => {
    const raw = String(value ?? '').trim();
    if (/control time/i.test(String(label || ''))) {
      const clock = /^(\d+):([0-5]\d)$/.exec(raw);
      return clock ? Number(clock[1]) * 60 + Number(clock[2]) : undefined;
    }
    return /^\d+(\.\d+)?$/.test(raw) ? Number(raw) : undefined;
  };
  const a = parse(away);
  const b = parse(home);
  return a !== undefined && b !== undefined && Number.isFinite(a + b) ? [a, b] : undefined;
}
