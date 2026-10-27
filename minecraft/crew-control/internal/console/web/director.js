// Auto-director: picks the unit most worth watching from the public event
// feed and the unit statuses. Pure, so node can test it; director-ui.js owns
// the DOM and the focus calls.

import { LOW } from './fleet.js';

// Closed vocabulary: only these kinds can pull the camera, and the words
// shown for them live here, never taken from a unit's own text.
export const WEIGHTS = { died: 100, stuck: 70, task_failed: 60, offline: 45, low_health: 40, low_hunger: 20 };
export const REASONS = { died: 'died', stuck: 'stuck', task_failed: 'task failed', offline: 'went offline', low_health: 'hurt', low_hunger: 'hungry' };

export const WINDOW_MS = 60000;
// A shot is held this long unless something worse happens, so the picture
// never flickers between two units that alternate in a burst.
export const DWELL_MS = 10000;
export const SWITCH_MARGIN = 25;

// What ends each kind of trouble; a later event of one of these cancels it.
const CLEARED_BY = {
  died: ['respawned'],
  offline: ['online'],
  stuck: ['task_done', 'task_cancelled', 'task_ended', 'task_failed', 'died', 'offline'],
};

const atOf = e => Date.parse(e.at);

// Per unit: the most interesting thing still going on, decayed with age.
export function scoreUnits(feed, statuses, now) {
  const out = new Map();
  const events = feed.filter(e => e && statuses.has(e.unit) && !Number.isNaN(atOf(e))).sort((a, b) => atOf(a) - atOf(b));
  for (let i = 0; i < events.length; i++) {
    const e = events[i];
    const w = WEIGHTS[e.kind];
    const age = now - atOf(e);
    if (!w || age < 0 || age > WINDOW_MS) continue;
    const s = statuses.get(e.unit);
    if (e.kind === 'low_health' && !(s.online && !s.dead && typeof s.health === 'number' && s.health <= LOW)) continue;
    if (e.kind === 'low_hunger' && !(s.online && typeof s.hunger === 'number' && s.hunger <= LOW)) continue;
    const ends = CLEARED_BY[e.kind];
    if (ends && events.slice(i + 1).some(n => n.unit === e.unit && ends.includes(n.kind))) continue;
    const score = w * (1 - age / (2 * WINDOW_MS));
    const have = out.get(e.unit);
    if (!have || score > have.score) out.set(e.unit, { unit: e.unit, kind: e.kind, score, at: atOf(e) });
  }
  return out;
}

// The unit to show next, or '' to keep the current shot.
export function choose(scores, current, heldFor, order = []) {
  const ranked = [...scores.values()].sort((a, b) => b.score - a.score || b.at - a.at || order.indexOf(a.unit) - order.indexOf(b.unit));
  const best = ranked[0];
  if (!best || best.unit === current) return null;
  const cur = scores.get(current);
  const curScore = cur ? cur.score : 0;
  if (best.kind !== 'died' && heldFor < DWELL_MS && best.score < curScore + SWITCH_MARGIN) return null;
  if (heldFor >= DWELL_MS && best.score <= curScore) return null;
  return best;
}

// ---- the mode ----

export const createDirector = () => ({ on: false, paused: false, unit: '', kind: '', at: 0, since: 0 });

// Shift-D: off to following, following to off, paused back to following.
export function press(d, now) {
  if (!d.on) { d.on = true; d.paused = false; d.since = 0; return d; }
  if (d.paused) { d.paused = false; d.since = now - DWELL_MS; return d; }
  d.on = false;
  d.unit = d.kind = '';
  return d;
}

// Any focus the viewer chose themselves pins the shot.
export function pause(d) {
  if (d.on && !d.paused) d.paused = true;
  return d;
}

// Returns the unit to focus when the shot should change, else ''.
export function tick(d, { feed, statuses, now, current, order }) {
  if (!d.on || d.paused) return '';
  const pick = choose(scoreUnits(feed, statuses, now), current, now - d.since, order);
  if (!pick) return '';
  d.unit = pick.unit;
  d.kind = pick.kind;
  d.at = pick.at;
  d.since = now;
  return pick.unit;
}

export function note(d, name, now) {
  if (!d.on) return '';
  if (d.paused) return `Director paused (pinned). Shift-D resumes.`;
  if (!d.unit) return 'Director on: waiting for something to happen';
  const s = Math.max(0, Math.round((now - d.at) / 1000));
  return `Director: ${name || d.unit} ${REASONS[d.kind] || ''} ${s}s ago`.replace(/ {2,}/g, ' ');
}
