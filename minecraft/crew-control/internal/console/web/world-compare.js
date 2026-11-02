// The per-world comparison, as pure functions: no DOM, so node can test it.
// Everything comes from what the dashboard already collects (the unit
// snapshots and the fleet event feed), all of it public, so the panel works
// signed out and shows no position, item or player name.

// The task templates the units know; anything else reads as "other".
export const TEMPLATES = ['gather_and_deposit', 'haul', 'withdraw', 'deposit', 'mine_shaft', 'goto', 'inspect', 'observe_blocks', 'place_block', 'craft', 'repair_build', 'build_blueprint'];
export const WORLD_RE = /^[a-z][a-z0-9-]{0,15}$/;
export const DEFAULT_WORLD = 'lab';
// Fewer watched unit-hours than this and a per-hour rate is noise, so it is
// shown as unknown instead.
export const MIN_UNIT_HOURS = 0.02;
// A value within this share of the median of the worlds reads as "near".
export const NEAR = 0.1;
export const MAX_WORLDS = 26;

const HOUR_MS = 3_600_000;

export const worldOf = s => {
  const w = s && s.world;
  if (w == null || w === '') return DEFAULT_WORLD;
  return typeof w === 'string' && WORLD_RE.test(w) ? w : 'other';
};
export const templateOf = t => (TEMPLATES.includes(t) ? t : 'other');

const at = e => Date.parse(e.at);
const finite = v => typeof v === 'number' && Number.isFinite(v);

export function median(xs) {
  const a = xs.filter(finite).sort((x, y) => x - y);
  if (!a.length) return null;
  const m = a.length >> 1;
  return a.length % 2 ? a[m] : (a[m - 1] + a[m]) / 2;
}

// Connected unit-hours per world inside [from, to]. Each sample carries how
// many units each world had online; a sample stands for the time until the
// next one, capped so a gap in the stream is not credited as watched time.
export function unitHours(samples, from, to, maxGap = 20_000) {
  const out = new Map();
  for (let i = 0; i < samples.length; i++) {
    const s = samples[i];
    if (s.t < from || s.t > to || !s.online) continue;
    const next = samples[i + 1];
    const dt = Math.min(next ? next.t - s.t : maxGap, maxGap, Math.max(0, to - s.t));
    for (const [w, n] of Object.entries(s.online)) out.set(w, (out.get(w) || 0) + (n * dt) / HOUR_MS);
  }
  return out;
}

// "above" / "near" / "below" next to a signed number, so no reading rests on
// colour. The band is a share of the median, floored so a median of zero does
// not make every value a divergence.
export function delta(value, med) {
  if (!finite(value) || !finite(med)) return null;
  const d = value - med;
  const band = Math.max(Math.abs(med) * NEAR, 1e-9);
  return { value: d, word: Math.abs(d) <= band ? 'near' : d > 0 ? 'above' : 'below' };
}

const round = (v, n = 1) => (finite(v) ? Math.round(v * 10 ** n) / 10 ** n : null);

// The rows the panel's table shows, in order. fmt turns a value into text.
export const METRICS = [
  { key: 'doneRate', label: 'Done per unit-hour', fmt: v => v.toFixed(1) },
  { key: 'failRate', label: 'Failure rate', fmt: v => `${Math.round(v * 100)}%` },
  { key: 'deathRate', label: 'Deaths per unit-hour', fmt: v => v.toFixed(2) },
  { key: 'stallRate', label: 'Stalls per unit-hour', fmt: v => v.toFixed(2) },
  { key: 'medianSecs', label: 'Median task time', fmt: v => (v < 90 ? `${Math.round(v)} s` : `${Math.round(v / 60)} min`) },
  { key: 'meanHealth', label: 'Mean health', fmt: v => v.toFixed(1) },
];

// Builds the panel's model. Inputs are the dashboard's own:
//   units      latest snapshot, filtered to the unit choice
//   feed       fleet events, oldest first
//   samples    5 s samples with .online = { world: n }
//   durations  [{ unit, secs, outcome, template }]
//   filter     { unit, from, to }; inFilter(e, filter) says whether an event counts
export function compareWorlds({ units, feed, samples, durations, filter, inFilter }) {
  if (!units.length && !samples.length) return { state: 'loading', worlds: [], chart: { categories: [], series: [] }, announce: '' };

  const unitWorld = new Map(units.map(u => [u.id, worldOf(u)]));
  const stats = new Map();
  const get = w => {
    if (!stats.has(w)) stats.set(w, { world: w, total: 0, online: 0, health: [], done: 0, failed: 0, died: 0, stuck: 0, byTemplate: new Map(), secs: [] });
    return stats.get(w);
  };
  for (const u of units) {
    const s = get(worldOf(u));
    s.total++;
    if (u.online) {
      s.online++;
      if (finite(u.health)) s.health.push(u.health);
    }
  }
  const worldOfEvent = e => (e.world ? worldOf(e) : unitWorld.get(e.unit));
  for (const e of feed) {
    if (!inFilter(e, filter)) continue;
    const w = worldOfEvent(e);
    if (!w) continue;
    const s = get(w);
    if (e.kind === 'task_done') {
      s.done++;
      const t = templateOf(e.template);
      s.byTemplate.set(t, (s.byTemplate.get(t) || 0) + 1);
    } else if (e.kind === 'task_failed') s.failed++;
    else if (e.kind === 'died') s.died++;
    else if (e.kind === 'stuck') s.stuck++;
  }
  for (const d of durations) {
    const w = unitWorld.get(d.unit);
    if (w) get(w).secs.push(d.secs);
  }

  const hours = unitHours(samples, filter.from, filter.to);
  const all = [...stats.values()].sort((a, b) => a.world.localeCompare(b.world)).slice(0, MAX_WORLDS);
  const rows = all.map(s => {
    const h = hours.get(s.world) || 0;
    const rate = n => (h >= MIN_UNIT_HOURS ? n / h : null);
    const finished = s.done + s.failed;
    return {
      world: s.world, online: s.online, total: s.total, unitHours: round(h, 2),
      values: {
        doneRate: rate(s.done),
        failRate: finished ? s.failed / finished : null,
        deathRate: rate(s.died),
        stallRate: rate(s.stuck),
        medianSecs: median(s.secs),
        meanHealth: s.health.length ? s.health.reduce((a, b) => a + b, 0) / s.health.length : null,
      },
      byTemplate: s.byTemplate,
      rateOf: t => rate(s.byTemplate.get(t) || 0),
    };
  });

  // The fleet median is the median of the worlds' values, so with one world
  // every delta is "near" by construction and is left off.
  const meds = {};
  for (const m of METRICS) meds[m.key] = median(rows.map(r => r.values[m.key]));
  const lone = rows.length < 2;
  const worlds = rows.map(r => ({
    world: r.world, online: r.online, total: r.total,
    cells: METRICS.map(m => {
      const v = r.values[m.key];
      const d = lone ? null : delta(v, meds[m.key]);
      return {
        key: m.key,
        text: v == null ? '-' : m.fmt(v),
        delta: d ? { text: `${d.value > 0 ? '+' : d.value < 0 ? '-' : ''}${m.fmt(Math.abs(d.value))}`, word: d.word } : null,
      };
    }),
  }));
  const medianRow = METRICS.map(m => ({ key: m.key, text: meds[m.key] == null ? '-' : m.fmt(meds[m.key]) }));

  const templates = TEMPLATES.concat('other').filter(t => rows.some(r => r.byTemplate.has(t)));
  const chart = {
    categories: templates,
    series: rows.map(r => ({ key: r.world, name: r.world, values: templates.map(t => round(r.rateOf(t), 2) ?? 0) })),
  };

  const state = rows.length && rows.some(r => r.total > 0 || r.done + r.failed > 0) ? 'ready' : 'empty';
  return { state, worlds, medianRow, chart, announce: announcement(worlds, lone) };
}

// The sentence a screen reader hears when the comparison changes. It holds
// the unit counts and where each world's done rate sits, never the rate
// itself: the rate moves every second, and announcing it would not stop.
export function announcement(worlds, lone) {
  if (!worlds.length) return '';
  const parts = worlds.map(w => {
    const done = w.cells.find(c => c.key === 'doneRate');
    const where = done.delta && done.delta.word !== 'near' ? `, done rate ${done.delta.word} the median of the worlds` : '';
    return `${w.world}: ${w.online} of ${w.total} units online${where}`;
  });
  return `${worlds.length} ${worlds.length === 1 ? 'world' : 'worlds'}${lone ? '' : ' compared'}. ${parts.join('. ')}.`;
}
