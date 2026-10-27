// Bulk recall and assign: building and checking the request, and wording the
// outcome. Pure; bulk-ui.js owns the dialog.
import { gotoBody, taskBody } from './state.js';

// Mirrors the server's bound.
export const MAX_UNITS = 16;

export const ACTIONS = ['recall', 'assign'];

const pointText = p => `${p.x} ${p.y} ${p.z}`;

// form: { template: 'gather'|'haul', item, qty, chest, from } for assign,
// { to } for recall; points are raw {x,y,z} strings from the inputs.
export function buildRequest(action, units, form) {
  if (!units.length) return { error: 'select at least one unit' };
  if (units.length > MAX_UNITS) return { error: `select at most ${MAX_UNITS} units (${units.length} selected)` };
  const head = { units, confirm: units.length, action };
  if (action === 'recall') {
    const r = gotoBody(form.to || {});
    return r.error ? r : { body: { ...head, to: r.body }, what: `Walk to ${pointText(r.body)}` };
  }
  if (action === 'assign') {
    const r = taskBody(form);
    if (r.error) return r;
    const t = r.body;
    const what = t.template === 'haul'
      ? `Haul ${Object.entries(t.items).map(([k, v]) => `${v} ${k}`).join(', ')} from ${pointText(t.from[0])} to ${pointText(t.to)}`
      : `Gather ${t.qty} ${t.item}, deposit at ${pointText(t.chest)}`;
    return { body: { ...head, task: t }, what };
  }
  return { error: 'unknown action' };
}

// What the operator is asked to confirm; recall cancels current work, assign
// queues behind it, and the sentence says which.
export function confirmText(action, names, what) {
  const who = names.length > 3 ? `${names.length} units` : names.join(', ');
  const effect = action === 'recall' ? 'Their current and queued work is cancelled first.' : 'It joins their queue.';
  return `${what}: ${who}. ${effect}`;
}

// The server answers 200 with a verdict per unit.
export function outcome(results, names = {}) {
  const list = Array.isArray(results) ? results : [];
  const failed = list.filter(r => !r.ok);
  const ok = list.length - failed.length;
  const text = failed.length
    ? `${ok} sent, ${failed.length} failed: ${failed.map(f => `${names[f.unit] || f.unit} (${f.error || 'failed'})`).join(', ')}`
    : `sent to ${ok} unit${ok === 1 ? '' : 's'}`;
  return { ok, failed: failed.length, text };
}
