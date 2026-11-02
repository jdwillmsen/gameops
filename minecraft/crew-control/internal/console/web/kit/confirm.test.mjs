import test from 'node:test';
import assert from 'node:assert/strict';
import { installDom, all } from '../graph/fake-dom.mjs';

const { doc } = installDom();
const { h } = await import('./dom.js');
const { confirmDialog } = await import('./confirm.js');

const btn = (root, label) => all(root, n => n.tag === 'button' && n.textContent === label)[0];
const overlay = () => doc.body.children.find(n => n.getAttribute('id') === 'overlay');

function ask(extra = {}) {
  doc.body.replaceChildren();
  doc.activeElement = null;
  const opener = h('button', { text: 'Delete' });
  doc.body.append(opener);
  opener.focus();
  const answer = confirmDialog({ label: 'Delete place', message: 'Delete Home?', confirmLabel: 'Delete', ...extra });
  return { opener, answer };
}

test('the question is a labelled modal that starts on Cancel', () => {
  ask();
  const card = overlay().children[0];
  assert.equal(card.getAttribute('role'), 'dialog');
  assert.equal(card.getAttribute('aria-label'), 'Delete place');
  assert.match(card.textContent, /Delete Home\?/);
  assert.equal(doc.activeElement, btn(card, 'Cancel'));
});

test('the confirm button answers true, closes and gives focus back', async () => {
  const { opener, answer } = ask();
  btn(overlay(), 'Delete').click();
  assert.equal(await answer, true);
  assert.equal(overlay(), undefined);
  assert.equal(doc.activeElement, opener);
});

test('Cancel, Escape and the backdrop answer false', async () => {
  let t = ask();
  btn(overlay(), 'Cancel').click();
  assert.equal(await t.answer, false);

  t = ask();
  overlay().dispatch('keydown', { key: 'Escape' });
  assert.equal(await t.answer, false);
  assert.equal(doc.activeElement, t.opener);

  t = ask();
  overlay().dispatch('mousedown');
  assert.equal(await t.answer, false);
  assert.equal(overlay(), undefined);
});

test('Tab stays inside the question', async () => {
  const { answer } = ask();
  const o = overlay();
  const ok = btn(o, 'Delete');
  ok.focus();
  o.dispatch('keydown', { key: 'Tab' });
  assert.equal(doc.activeElement, btn(o, 'Cancel'));
  btn(o, 'Cancel').click();
  await answer;
});

test('the confirm label defaults to Confirm', async () => {
  const { answer } = ask({ confirmLabel: undefined });
  assert.ok(btn(overlay(), 'Confirm'));
  btn(overlay(), 'Cancel').click();
  await answer;
});

test('no key reaches the global handler while the question is open', async () => {
  const { answer } = ask();
  let leaked = 0;
  doc.body.addEventListener('keydown', () => { leaked++; });
  const o = overlay();
  for (const key of ['?', '/', 'w', 'j', '1', 'k']) o.dispatch('keydown', { key, ctrlKey: key === 'k' });
  assert.equal(leaked, 0);
  btn(o, 'Cancel').click();
  await answer;
});
