// What the dashboard's panels read. The console has no history API for the
// public view, so everything is built from the two things the stream already
// carries: the once-a-second unit snapshot and the fleet event feed. The
// windows are therefore what this page has watched, not stored history.
import { mergeEvents, alertsOf, LOW } from './fleet.js';

export const SAMPLE_MS = 5000;
export const SAMPLE_CAP = 720; // one hour at SAMPLE_MS
export const EVENT_CAP = 600;
// A snapshot is re-sent every 15 s even when idle; a longer gap means the
// stream dropped, and the unit's state across it is unknown, not idle.
const MAX_GAP_MS = 20_000;
export const DURATION_BUCKETS = [
  { label: '< 10 s', max: 10 },
  { label: '10-30 s', max: 30 },
  { label: '30-60 s', max: 60 },
  { label: '1-2 min', max: 120 },
  { label: '2-5 min', max: 300 },
  { label: '5 min +', max: Infinity },
];

export const stateOf = s => (!s.online ? 'offline' : s.dead ? 'dead' : s.task ? 'working' : 'idle');
const num = v => (typeof v === 'number' && !Number.isNaN(v) ? v : null);
const at = e => Date.parse(e.at);

export function createDashData() {
  let units = [];
  let samples = [];
  let feed = [];
  let lastTs = null;
  let prev = new Map();
  const idleMs = new Map();
  const onlineMs = new Map();
  let version = 0;

  function sample(list, now) {
    const c = { t: now, total: list.length, working: 0, idle: 0, dead: 0, offline: 0, alerts: 0, healthy: 0 };
    for (const s of list) {
      c[stateOf(s)]++;
      const a = alertsOf(s);
      if (a.length) c.alerts++;
      else c.healthy++;
    }
    return c;
  }

  return {
    get version() { return version; },
    units: () => units,
    samples: () => samples,
    feed: () => feed,

    onUnits(list, now) {
      // Time since the last snapshot is credited to the state the unit was in
      // then, which is the state that lasted through it.
      if (lastTs != null && now - lastTs <= MAX_GAP_MS) {
        const dt = now - lastTs;
        for (const [id, st] of prev) {
          if (st === 'offline') continue;
          onlineMs.set(id, (onlineMs.get(id) || 0) + dt);
          if (st === 'idle') idleMs.set(id, (idleMs.get(id) || 0) + dt);
        }
      }
      lastTs = now;
      prev = new Map(list.map(s => [s.id, stateOf(s)]));
      units = list;
      const last = samples[samples.length - 1];
      if (!last || now - last.t >= SAMPLE_MS) {
        samples = [...samples, sample(list, now)].slice(-SAMPLE_CAP);
      } else {
        samples = [...samples.slice(0, -1), sample(list, last.t)];
      }
      version++;
    },

    onEvents(incoming) {
      const r = mergeEvents(feed, incoming, EVENT_CAP);
      if (r.added.length) {
        feed = r.feed;
        version++;
      }
    },

    // Share of the time this page has seen the unit online that it had no task.
    idle(ids) {
      return ids.map(id => ({ id, idleMs: idleMs.get(id) || 0, onlineMs: onlineMs.get(id) || 0 }));
    },
  };
}

// ---- queries: pure functions over the data and a filter ----

// filter: { unit: '' | id, from, to } with from/to in epoch ms.
// Event times come from the server's clock, so the newest one can read a
// moment ahead of this page's; a little slack keeps it in the window.
export const SKEW_MS = 30_000;
export const inFilter = (e, f) => (!f.unit || e.unit === f.unit) && at(e) >= f.from && at(e) <= f.to + SKEW_MS;

export function pickUnits(list, f) {
  return f.unit ? list.filter(s => s.id === f.unit) : list;
}

export function windowSamples(samples, f) {
  return samples.filter(s => s.t >= f.from && s.t <= f.to);
}

// Bucket width keeps a chart near 40 points whatever the window, never finer
// than a minute because events are sparse.
export function bucketMs(span) {
  const steps = [60_000, 120_000, 300_000, 600_000, 900_000, 1_800_000, 3_600_000];
  return steps.find(s => span / s <= 40) || steps[steps.length - 1];
}

export function throughput(feed, f) {
  const size = bucketMs(f.to - f.from);
  const start = Math.floor(f.from / size) * size;
  const n = Math.max(2, Math.floor((f.to - start) / size) + 1);
  const x = Array.from({ length: n }, (_, i) => start + i * size);
  const done = new Array(n).fill(0);
  const failed = new Array(n).fill(0);
  for (const e of feed) {
    if (!inFilter(e, f)) continue;
    const i = Math.floor((at(e) - start) / size);
    if (i < 0 || i >= n) continue;
    if (e.kind === 'task_done') done[i]++;
    else if (e.kind === 'task_failed') failed[i]++;
  }
  return { x, done, failed, size };
}

export function outcomes(feed, f) {
  let done = 0, failed = 0, cancelled = 0;
  const perUnit = new Map();
  for (const e of feed) {
    if (!inFilter(e, f)) continue;
    const k = e.kind;
    if (k !== 'task_done' && k !== 'task_failed' && k !== 'task_cancelled') continue;
    const u = perUnit.get(e.unit) || { done: 0, failed: 0 };
    if (k === 'task_done') { done++; u.done++; }
    else if (k === 'task_failed') { failed++; u.failed++; }
    else cancelled++;
    perUnit.set(e.unit, u);
  }
  const finished = done + failed;
  return { done, failed, cancelled, rate: finished ? done / finished : null, perUnit };
}

// Pairs a start with its end by task id (else by unit, taking the latest
// start), so only tasks whose start this page also saw have a duration.
export function taskDurations(feed, f) {
  const open = new Map();
  const out = [];
  for (const e of feed) {
    const key = `${e.unit}|${e.task || ''}`;
    if (e.kind === 'task_started') open.set(key, at(e));
    else if (e.kind === 'task_done' || e.kind === 'task_failed') {
      const t0 = open.get(key);
      open.delete(key);
      if (t0 != null && inFilter(e, f)) out.push({ unit: e.unit, secs: Math.max(0, (at(e) - t0) / 1000), outcome: e.kind === 'task_done' ? 'done' : 'failed' });
    }
  }
  return out;
}

export function durationHistogram(durations) {
  const done = DURATION_BUCKETS.map(() => 0);
  const failed = DURATION_BUCKETS.map(() => 0);
  for (const d of durations) {
    const i = DURATION_BUCKETS.findIndex(b => d.secs < b.max);
    (d.outcome === 'done' ? done : failed)[i]++;
  }
  return { categories: DURATION_BUCKETS.map(b => b.label), done, failed };
}

export const median = a => {
  if (!a.length) return null;
  const s = [...a].sort((x, y) => x - y);
  const m = s.length >> 1;
  return s.length % 2 ? s[m] : (s[m - 1] + s[m]) / 2;
};

export function healthShare(list) {
  if (!list.length) return null;
  return Math.round((list.filter(s => alertsOf(s).length === 0).length / list.length) * 100);
}

export { LOW, num };
