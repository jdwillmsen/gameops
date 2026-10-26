// Pure helpers for the insight strip: what the capacity report and the
// insight list say, as the words and numbers the console shows.

export const RULE_NAMES = {
  R1: 'Zero success',
  R2: 'Throughput drop',
  R3: 'Long task',
  R4: 'Fleet-wide',
};

export function percent(x) {
  return `${Math.round(x * 100)}%`;
}

// Utilization above 100% means the busy-unit estimate exceeds the working
// units, which only a non-stationary window or missing durations can cause;
// the tile shows the number and the caveat rather than clamping it.
export function capacityTiles(report) {
  if (!report) return [];
  const tiles = [
    { label: 'Busy units', value: report.busy_units.toFixed(1), note: `of ${report.working_units} working` },
    { label: 'Utilization', value: report.working_units ? percent(report.utilization) : '-', note: '' },
  ];
  if (report.non_stationary) {
    tiles.push({ label: 'Reliability', value: 'Unsteady', note: 'arrival rate changed within the window; treat the numbers as rough' });
  } else if (report.unknown_durations > 0) {
    tiles.push({ label: 'Reliability', value: 'Partial', note: `${report.unknown_durations} tasks had no duration` });
  }
  return tiles;
}

export function templateRows(report) {
  if (!report) return [];
  return report.templates.map(t => ({
    template: t.template,
    lambda: t.lambda_per_min.toFixed(1),
    mean: t.mean_duration_s.toFixed(1),
    l: t.l.toFixed(1),
    failedTime: Math.round(t.failed_time_s),
    unsteady: t.non_stationary,
  }));
}

// Firing first (the server already orders them), the resolved ones kept
// short so they do not bury a live problem.
export function insightRows(list, maxResolved = 3) {
  const firing = (list || []).filter(i => i.state === 'firing');
  const resolved = (list || []).filter(i => i.state !== 'firing').slice(0, maxResolved);
  return [...firing, ...resolved].map(i => ({
    id: i.id,
    unit: i.fleet ? '' : i.unit,
    who: i.fleet ? 'Fleet' : i.unit,
    rule: i.rule,
    ruleName: RULE_NAMES[i.rule] || i.rule,
    severity: i.severity,
    state: i.state,
    since: i.first_seen,
    detail: i.detail,
    evidence: i.evidence || [],
  }));
}
