// The alerts panel and bell. The server decides what is firing; this only
// shows it, toasts what is new and, for operators, sends acknowledge and mute.

export const RULE_LABELS = {
  unit_died: 'Unit died',
  unit_stuck: 'Unit stuck',
  task_failures: 'Repeated task failures',
  low_food: 'Low food',
  unit_silent: 'Unit silent',
};

export const MUTE_CHOICES = [['15m', '15 min'], ['1h', '1 hour'], ['8h', '8 hours']];

export const SNOOZE_CHOICES = [['15m', '15 min'], ['1h', '1 hour'], ['8h', '8 hours']];

// Active type-wide snoozes, soonest to end first, with the time left.
export function snoozeList(snoozed, now) {
  return Object.entries(snoozed || {})
    .map(([rule, until]) => ({ rule, label: ruleLabel(rule), left: Date.parse(until) - now }))
    .filter(s => s.left > 0)
    .sort((a, b) => a.left - b.left || a.rule.localeCompare(b.rule));
}

export function leftText(ms) {
  const m = Math.ceil(ms / 60000);
  return m < 60 ? `${m}m left` : `${Math.floor(m / 60)}h ${m % 60}m left`;
}

export const ruleLabel = rule => RULE_LABELS[rule] || rule;

export function isMuted(a, now) {
  return !!a.muted_until && Date.parse(a.muted_until) > now;
}

// Muted alerts stay listed but stay quiet; an acknowledged one has been seen.
export function isLoud(a, now) {
  return a.state === 'firing' && !a.acked && !isMuted(a, now);
}

export const bellCount = (list, now) => list.filter(a => isLoud(a, now)).length;

export function ageText(since, now) {
  const t = Date.parse(since);
  if (Number.isNaN(t)) return '';
  const s = Math.max(0, Math.round((now - t) / 1000));
  if (s < 60) return `${s}s`;
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
}

// Alerts that began firing since the last list and are loud. Keyed on id and
// start time so a re-fire after a resolution toasts again and a repeat of the
// same list does not.
export function newlyLoud(seen, list, now) {
  const out = [];
  for (const a of list) {
    const key = `${a.id}@${a.since}`;
    if (a.state === 'firing' && !seen.has(key) && isLoud(a, now)) out.push(a);
    if (a.state === 'firing') seen.add(key);
  }
  return out;
}

const SEV_RANK = { crit: 0, warn: 1 };

// filter: { show: 'active' | 'all', severity: 'all' | 'crit' | 'warn' }
export function visible(list, filter, now) {
  return list
    .filter(a => (filter.show === 'all' || a.state === 'firing') && (filter.severity === 'all' || a.severity === filter.severity))
    .sort((x, y) => (x.state === 'firing' ? 0 : 1) - (y.state === 'firing' ? 0 : 1)
      || (SEV_RANK[x.severity] ?? 2) - (SEV_RANK[y.severity] ?? 2)
      || Date.parse(y.since) - Date.parse(x.since));
}
