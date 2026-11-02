import test from 'node:test';
import assert from 'node:assert/strict';
import { installDom, all, byClass, settle } from './graph/fake-dom.mjs';

const { doc } = installDom();
const { openBulk } = await import('./bulk-ui.js');

const host = (extra = {}) => ({ api: async () => ({ ok: true, status: 200, data: { results: [] } }), name: id => id.toUpperCase(), expired() {}, done() {}, ...extra });
const open = (action, h = host(), close = () => {}) => {
  doc.body.replaceChildren();
  return openBulk(h, action, ['a', 'b'], close);
};
const named = n => !!n.getAttribute('aria-label');

test('every select in the assign dialog exposes its name as an attribute', () => {
  const overlay = open('assign');
  const selects = all(overlay, n => n.tag === 'select');
  assert.deepEqual(selects.map(n => n.getAttribute('aria-label')), ['Task', 'Item']);
});

test('the quantity box is named too, and the coordinate boxes are named by their label', () => {
  const overlay = open('assign');
  const qty = all(overlay, n => n.tag === 'input' && n.getAttribute('aria-label') === 'Quantity');
  assert.equal(qty.length, 1);
  const inputs = all(overlay, n => n.tag === 'input' && !named(n));
  assert.ok(inputs.length >= 6);
  for (const i of inputs) assert.ok(i.parent.tag === 'label' && i.parent.textContent.length > 0);
});

test('the dialog is a labelled modal in the overlay', () => {
  const overlay = open('recall');
  const card = overlay.children[0];
  assert.equal(overlay.getAttribute('id'), 'overlay');
  assert.equal(card.getAttribute('role'), 'dialog');
  assert.equal(card.getAttribute('aria-modal'), 'true');
  assert.equal(card.getAttribute('aria-label'), 'Recall units');
  assert.equal(open('assign').children[0].getAttribute('aria-label'), 'Assign a task');
});

test('recall lists who is selected and offers no send until the destination is valid', () => {
  const overlay = open('recall');
  assert.match(byClass(overlay, 'bk-who')[0].textContent, /2 selected .*: A, B/);
  const send = all(overlay, n => n.tag === 'button' && n.className === 'danger')[0];
  assert.equal(send.textContent, 'Recall 2 units');
  assert.equal(send.disabled, true);
});

test('Cancel and a click on the backdrop both close', () => {
  let closed = 0;
  const overlay = open('recall', host(), () => closed++);
  all(overlay, n => n.tag === 'button' && n.textContent === 'Cancel')[0].click();
  overlay.dispatch('mousedown');
  assert.equal(closed, 2);
});

test('a destination in the recall form enables the send, which posts and reports', async () => {
  const calls = [];
  const done = [];
  let closed = 0;
  const overlay = open('recall', host({ api: async (m, p, b) => { calls.push([m, p, b]); return { ok: true, status: 200, data: { results: [{ id: 'a', ok: true }, { id: 'b', ok: true }] } }; }, done: r => done.push(r) }), () => closed++);
  const [x, y, z] = all(overlay, n => n.tag === 'input');
  x.value = '1'; y.value = '64'; z.value = '-3';
  const form = all(overlay, n => n.tag === 'form')[0];
  form.dispatch('input');
  const send = all(overlay, n => n.tag === 'button' && n.className === 'danger')[0];
  assert.equal(send.disabled, false);
  form.dispatch('submit');
  await settle();
  assert.equal(calls.length, 1);
  assert.equal(calls[0][1], '/v1/work/bulk');
  assert.equal(closed, 1);
  assert.equal(done.length, 1);
});

test('a 401 closes the dialog and tells the caller the session ended', async () => {
  let expired = 0, closed = 0;
  const overlay = open('recall', host({ api: async () => ({ ok: false, status: 401, data: null }), expired: () => expired++ }), () => closed++);
  const [x, y, z] = all(overlay, n => n.tag === 'input');
  x.value = '1'; y.value = '64'; z.value = '3';
  const form = all(overlay, n => n.tag === 'form')[0];
  form.dispatch('input');
  form.dispatch('submit');
  await settle();
  assert.equal(expired, 1);
  assert.equal(closed, 1);
});
