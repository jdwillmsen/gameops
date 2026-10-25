// Pure mapping between the jobs the API serves (the job/steps shape state.js
// mirrors) and what the graph component draws. No DOM, so node tests run it.
// One node per step, keyed by the step key, which is unique per job; an edge
// runs from each dependency to the step that waits on it.

import { jobTitle } from '../state.js';

export const STEP_STATES = ['waiting', 'queued', 'running', 'done', 'failed', 'cancelled'];

// Tone names into graph.css tokens, which lean on charts.css palette values.
// Waiting and cancelled stay quiet; queued stands apart from running by hue as
// well as word and icon.
export const STEP_TONE = {
  waiting: 'plain',
  queued: 'info',
  running: 'live',
  done: 'good',
  failed: 'critical',
  cancelled: 'plain',
};

// The signature shapes: a dash ring waits, dots queue, a filled wedge runs, a
// check is done, a cross failed, a struck ring cancelled.
export const STEP_ICON = {
  waiting: { icon: 'M6 2.2a3.8 3.8 0 1 1 0 7.6a3.8 3.8 0 1 1 0-7.6z', fill: false },
  queued: { icon: ['M3 6h.01M6 6h.01M9 6h.01'], fill: false },
  running: { icon: 'M4 2.8 L9.2 6 L4 9.2 Z', fill: true },
  done: { icon: 'M2.5 6.5 L5 9 L9.5 3', fill: false },
  failed: { icon: ['M3 3 L9 9', 'M9 3 L3 9'], fill: false },
  cancelled: { icon: ['M6 2.2a3.8 3.8 0 1 1 0 7.6a3.8 3.8 0 1 1 0-7.6z', 'M3 6 H9'], fill: false },
};

export function stepChip(state) {
  const known = STEP_STATES.includes(state);
  const shape = known ? STEP_ICON[state] : STEP_ICON.waiting;
  return {
    word: known || state ? state : 'unknown',
    tone: known ? STEP_TONE[state] : 'plain',
    icon: shape.icon,
    fill: shape.fill,
  };
}

export const JOB_STATES = STEP_STATES;
export const jobChip = stepChip;
export const isOpen = job => job.state === 'running';

const point = p => (p ? ` ${p.x} ${p.y} ${p.z}` : '');

// One line for what the step's task wants. The template JSON mirrors the crew
// task API; anything unreadable falls back to the template word.
export function taskText(t) {
  if (!t || typeof t !== 'object') return '';
  const kind = typeof t.template === 'string' ? t.template : '';
  if (kind === 'gather_and_deposit') return `gather ${t.item} x${t.qty}`;
  if (kind === 'haul' || kind === 'withdraw' || kind === 'deposit') {
    return `${kind} ${Object.entries(t.items || {}).map(([k, n]) => `${k} x${n}`).join(', ')}`;
  }
  if (kind === 'inspect') return `inspect${point(t.chest)}`;
  if (kind === 'mine_shaft') return 'dig a shaft';
  if (kind === 'craft') return `craft ${t.item} x${t.count}`;
  return kind;
}

const nodeOf = s => {
  const chip = stepChip(s.state);
  return {
    id: s.key,
    label: s.key,
    sub: taskText(s.template),
    chip,
    tone: chip.tone,
    // A failed step's reason is what a screen-reader user needs most.
    note: s.reason || '',
  };
};

// One job -> the graph's draw model.
export function jobModel(job) {
  const steps = job.steps || [];
  const index = new Map(steps.map(s => [s.key, s]));
  return {
    id: job.id,
    title: jobTitle(job),
    state: job.state,
    chip: stepChip(job.state),
    steps,
    nodes: steps.map(nodeOf),
    edges: steps.flatMap(s => (s.depends_on || [])
      .filter(d => index.has(d))
      .map(d => ({ from: d, to: s.key }))),
  };
}

// ---- the jobs list ----

// Running first, then the rest, newest order from the feed preserved inside
// each group.
const group = job => (job.state === 'running' ? 0 : 1);

// The jobs event holds the newest 20; a deep link to an older job has to be
// fetched, and stands in unsatisfied until then so the link never errors out.
export function jobList(jobs, wanted = '') {
  const seen = new Set();
  const rows = [];
  for (const job of jobs) {
    if (seen.has(job.id)) continue;
    seen.add(job.id);
    rows.push(job);
  }
  if (wanted && !seen.has(wanted)) rows.push({ id: wanted, fetching: true });
  return rows.sort((a, b) => group(a) - group(b));
}

export function pickJob(jobs, wanted = '') {
  if (wanted) {
    const got = jobs.find(j => j.id === wanted);
    if (got) return got;
  }
  return jobs.find(isOpen) || jobs[0] || null;
}

export function stepsDone(job) {
  const steps = job.steps || [];
  return { done: steps.filter(s => s.state === 'done').length, total: steps.length };
}

// A summary row for the list; a fetching stub (no steps yet) says so.
export function jobRow(job) {
  const { done, total } = stepsDone(job);
  return {
    id: job.id,
    title: job.fetching ? null : jobTitle(job),
    state: job.state,
    chip: job.state ? stepChip(job.state) : null,
    progress: total ? `${done}/${total}` : '',
    reason: job.reason || '',
  };
}
