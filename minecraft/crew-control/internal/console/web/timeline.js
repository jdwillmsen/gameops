// Pure model for the timeline views: turns the stored unit events into task
// bars, markers and status spans per unit, and a job's steps into rows. No
// DOM, so node tests run it.

import { SERIES, OTHER } from './charts/palette.js';

const END_KINDS = {
  task_done: 'done',
  task_failed: 'failed',
  task_cancelled: 'cancelled',
  task_ended: 'ended',
};

// Fixed per template name, in order of first sight, so a template keeps its
// colour while the operator pans; the palette is short, the rest share grey.
export function colorFor(template, seen) {
  if (!template) return OTHER;
  let i = seen.indexOf(template);
  if (i < 0) i = seen.push(template) - 1;
  return i < SERIES.dark.length ? SERIES.dark[i] : OTHER;
}

const when = e => (typeof e.at === 'number' ? e.at : Date.parse(e.at));
const taskOf = e => e.task || (e.payload && e.payload.task) || '';
const tplOf = e => (e.payload && e.payload.template) || '';

// events: any order. Bars end where the unit's own record says the task
// ended; a task whose end was never seen runs to `to` (still going) unless the
// unit went offline or died first, or started something else, where it is
// closed as "ended" because nothing says how it finished.
export function buildLane(events, { from, to }) {
  const evs = events.map(e => ({ ...e, t: when(e) })).sort((a, b) => a.t - b.t || (a.id || 0) - (b.id || 0));
  const bars = [];
  const marks = [];
  const spans = [];
  let open = null;
  let off = null;
  let dead = null;

  const closeOpen = (t, outcome) => {
    if (!open) return;
    open.end = t;
    open.outcome = outcome;
    open = null;
  };

  for (const e of evs) {
    const kind = e.kind;
    if (kind === 'task_started') {
      closeOpen(e.t, 'ended');
      open = { id: taskOf(e) || `s${e.id}`, unit: e.unit, template: tplOf(e), start: e.t, end: to, outcome: 'running', job: e.job || '', events: [e] };
      bars.push(open);
    } else if (END_KINDS[kind]) {
      const id = taskOf(e);
      let bar = open && (!id || open.id === id) ? open : null;
      if (bar) {
        open = null;
      } else {
        // The start fell before the window, or was never stored.
        const took = e.payload && e.payload.duration_ms;
        bar = { id: id || `e${e.id}`, unit: e.unit, template: tplOf(e), start: took ? e.t - took : (from ?? e.t), end: e.t, outcome: '', job: e.job || '', events: [], clipped: !took };
        bars.push(bar);
      }
      bar.end = e.t;
      bar.outcome = END_KINDS[kind];
      bar.template = bar.template || tplOf(e);
      bar.job = bar.job || e.job || '';
      bar.events.push(e);
      if (bar.outcome === 'failed') marks.push({ kind: 'failed', unit: e.unit, t: e.t, events: [e], bar });
    } else if (kind === 'idle_rollup') {
      const last = e.payload && e.payload.last ? Date.parse(e.payload.last) : e.t;
      bars.push({ id: `i${e.id}`, unit: e.unit, template: tplOf(e), start: e.t, end: Math.max(last, e.t), outcome: 'idle', job: '', events: [e], count: (e.payload && e.payload.count) || 1 });
    } else if (kind === 'stuck') {
      marks.push({ kind: 'stuck', unit: e.unit, t: e.t, events: [e] });
      if (open && (!taskOf(e) || open.id === taskOf(e))) open.events.push(e);
    } else if (kind === 'died') {
      closeOpen(e.t, 'ended');
      marks.push({ kind: 'died', unit: e.unit, t: e.t, events: [e] });
      dead = { kind: 'dead', unit: e.unit, start: e.t, end: to, events: [e] };
      spans.push(dead);
    } else if (kind === 'respawned') {
      if (dead) { dead.end = e.t; dead.events.push(e); dead = null; }
    } else if (kind === 'offline') {
      closeOpen(e.t, 'ended');
      if (dead) { dead.end = e.t; dead = null; }
      off = { kind: 'offline', unit: e.unit, start: e.t, end: to, events: [e] };
      spans.push(off);
    } else if (kind === 'online') {
      if (off) { off.end = e.t; off.events.push(e); off = null; }
    }
  }
  return { bars, marks, spans };
}

export function buildLanes(events, units, range) {
  const by = new Map(units.map(u => [u, []]));
  for (const e of events) if (by.has(e.unit)) by.get(e.unit).push(e);
  return new Map(units.map(u => [u, buildLane(by.get(u), range)]));
}

// ---- window arithmetic ----

export const MIN_SPAN = 30000;

// Keeps the window inside the bounds and no narrower than MIN_SPAN.
export function clampRange([a, b], [lo, hi]) {
  let span = Math.max(MIN_SPAN, Math.min(b - a, hi - lo));
  let start = Math.min(Math.max(a, lo), hi - span);
  return [start, start + span];
}

// factor < 1 zooms in; frac is the pointer's place across the window, so the
// time under the pointer stays put.
export function zoomRange([a, b], factor, frac, bounds) {
  const span = (b - a) * factor;
  const at = a + (b - a) * frac;
  return clampRange([at - span * frac, at + span * (1 - frac)], bounds);
}

export function panRange([a, b], ms, bounds) {
  return clampRange([a + ms, b + ms], bounds);
}

const STEPS = [1, 5, 15, 30, 60, 300, 900, 1800, 3600, 7200, 21600, 43200, 86400].map(s => s * 1000);

// About `target` tick times in the window, on round clock boundaries.
export function ticks([a, b], target = 8) {
  const step = STEPS.find(s => (b - a) / s <= target) || STEPS[STEPS.length - 1];
  // Local round numbers: shift by the zone offset so "every hour" lands on the hour.
  const off = new Date(a).getTimezoneOffset() * 60000;
  const out = [];
  for (let t = Math.ceil((a - off) / step) * step + off; t <= b; t += step) out.push(t);
  return { step, times: out };
}

// Bars thinner than a pixel that land on pixels already painted by the same
// colour add nothing but nodes; failures are never dropped.
export function cullBars(bars, x) {
  const painted = new Map();
  const out = [];
  for (const bar of bars) {
    const x0 = x(bar.start);
    const x1 = x(bar.end);
    if (bar.outcome === 'failed' || x1 - x0 >= 1) { out.push(bar); continue; }
    const key = `${Math.floor(x0)}:${bar.template}`;
    if (painted.has(key)) continue;
    painted.set(key, true);
    out.push(bar);
  }
  return out;
}

// ---- reading the history ----

// One unit's events over the window, newest first so a busy unit loses its
// oldest rows to the page cap, not its latest. `api` is the console's fetcher.
export async function fetchUnit(api, unit, [since, until], { pages = 15, signal } = {}) {
  const events = [];
  let cursor = '';
  let truncated = false;
  for (let n = 0; ; n++) {
    if (n >= pages) { truncated = true; break; }
    const q = new URLSearchParams({ unit, since: new Date(since).toISOString(), until: new Date(until).toISOString(), limit: '200', sort: 'desc' });
    if (cursor) q.set('cursor', cursor);
    const r = await api('GET', `/v1/events?${q}`);
    if (signal && signal.aborted) return { events: [], truncated: false, aborted: true };
    if (!r.ok) return { events, truncated, status: r.status, error: (r.data && r.data.error) || 'could not load events' };
    events.push(...(r.data.events || []));
    if (!r.data.next) break;
    cursor = r.data.next;
  }
  return { events: events.reverse(), truncated };
}

export async function fetchWindow(api, units, range, { concurrency = 6, signal, pages } = {}) {
  const all = [];
  let truncated = false;
  let failure = null;
  const queue = [...units];
  const worker = async () => {
    while (queue.length && !failure) {
      const u = queue.shift();
      const r = await fetchUnit(api, u, range, { signal, pages });
      if (r.aborted) return;
      if (r.error) { failure = { status: r.status, error: r.error }; return; }
      all.push(...r.events);
      truncated ||= r.truncated;
    }
  };
  await Promise.all(Array.from({ length: Math.min(concurrency, units.length) }, worker));
  return { events: all, truncated, failure };
}

// ---- a job's steps as timed rows ----

// Rows follow the steps' start times (unstarted last, in step order), so the
// list reads as the order the job actually ran in.
export function jobRows(job, events) {
  const byTask = new Map();
  for (const e of events) {
    const id = taskOf(e);
    if (!id) continue;
    if (!byTask.has(id)) byTask.set(id, []);
    byTask.get(id).push(e);
  }
  const rows = (job.steps || []).map((s, i) => {
    const evs = (s.work_id && byTask.get(s.work_id)) || [];
    const { bars } = buildLane(evs, { from: undefined, to: Date.parse(job.updated) || Date.now() });
    const bar = bars[0];
    const tpl = s.template && typeof s.template === 'object' ? s.template.template : '';
    return {
      key: s.key,
      unit: s.unit || (evs[0] && evs[0].unit) || '',
      template: tpl || (bar && bar.template) || '',
      state: s.state,
      reason: s.reason || '',
      attempts: s.attempts || 0,
      order: i,
      start: bar ? bar.start : null,
      end: bar && bar.outcome !== 'running' ? bar.end : null,
      outcome: bar ? bar.outcome : '',
      events: evs,
    };
  });
  return rows.sort((a, b) => (a.start ?? Infinity) - (b.start ?? Infinity) || a.order - b.order);
}

export function duration(ms) {
  if (ms == null || !Number.isFinite(ms) || ms < 0) return '';
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  const h = Math.floor(m / 60);
  return `${h}h ${m % 60}m`;
}
