import test from 'node:test';
import assert from 'node:assert/strict';
import { installDom, settle, all, byClass } from './graph/fake-dom.mjs';

const dom = installDom();
const { doc } = dom;
globalThis.location = { search: '' };
globalThis.requestAnimationFrame = fn => setImmediate(fn);
globalThis.cancelAnimationFrame = id => clearImmediate(id);
const ticks = [];
globalThis.setInterval = fn => ticks.push(fn);
globalThis.clearInterval = () => {};
doc.addEventListener = () => {};
doc.visibilityState = 'visible';
const { initOverview } = await import('./overview-ui.js');

const unit = (id, extra = {}) => ({ id, name: id, online: true, dead: false, health: 20, hunger: 20, task: null, ...extra });

function boot(units, { current = '' } = {}) {
  doc.body.replaceChildren();
  doc.activeElement = null;
  ticks.length = 0;
  const statuses = new Map(units.map(u => [u.id, u]));
  const focused = [];
  const host = {
    statuses, order: () => units.map(u => u.id), state: () => ({ unit: current }), operator: () => null,
    focus: id => focused.push(id), sync() {},
  };
  const ui = initOverview(host);
  const into = doc.createElement('main');
  doc.body.append(into);
  ui.onUnits();
  ui.mount(into);
  return { ui, host, focused, into, root: into.children[0], statuses, units };
}

const grid = root => all(root, n => n.getAttribute('role') === 'grid')[0];
const heads = root => all(root, n => n.getAttribute('role') === 'columnheader');
const rows = root => all(root, n => n.className && n.className.split(' ').includes('ov-row'));
const names = root => rows(root).filter(r => !r.hidden).map(r => r.dataset.unit);

test('every unit gets a row and the grid is labelled', async () => {
  const { root } = boot([unit('crew-01'), unit('crew-02')]);
  await settle();
  assert.equal(grid(root).getAttribute('aria-label'), 'Crew units');
  assert.deepEqual(names(root), ['crew-01', 'crew-02']);
});

test('sortable headers are buttons, the sorted one says which way', async () => {
  const { root } = boot([unit('crew-01')]);
  await settle();
  const sorts = heads(root).map(h => h.getAttribute('aria-sort'));
  assert.equal(sorts.filter(s => s === 'ascending' || s === 'descending').length, 1);
  assert.equal(sorts.filter(s => s === 'none').length, sorts.filter(s => s !== null).length - 1);
  const buttons = heads(root).flatMap(h => all(h, n => n.tag === 'button'));
  assert.ok(buttons.length >= 7);
});

test('a header click sorts by that column and a second click reverses it', async () => {
  const { root } = boot([unit('a', { health: 5 }), unit('b', { health: 20 }), unit('c', { health: 12 })]);
  await settle();
  const health = heads(root).find(h => h.textContent.startsWith('Health') && all(h, n => n.tag === 'button').length);
  const press = () => all(health, n => n.tag === 'button')[0].click();
  press();
  await settle();
  const first = names(root);
  assert.notEqual(health.getAttribute('aria-sort'), 'none');
  press();
  await settle();
  assert.deepEqual(names(root), [...first].reverse());
  assert.notEqual(first.join(), names(root).join());
});

test('re-sorting moves the existing row elements instead of rebuilding them', async () => {
  const { root } = boot([unit('a', { health: 5 }), unit('b', { health: 20 })]);
  await settle();
  const before = new Map(rows(root).map(r => [r.dataset.unit, r]));
  const health = heads(root).find(h => h.textContent.startsWith('Health'));
  all(health, n => n.tag === 'button')[0].click();
  await settle();
  for (const r of rows(root)) assert.equal(before.get(r.dataset.unit), r);
});

test('a focused row keeps focus when a re-sort moves it', async () => {
  const { root } = boot([unit('a', { health: 5 }), unit('b', { health: 20 }), unit('c', { health: 12 })]);
  await settle();
  const row = rows(root).find(r => r.dataset.unit === 'a');
  row.focus();
  const health = heads(root).find(h => h.textContent.startsWith('Health'));
  all(health, n => n.tag === 'button')[0].click();
  await settle();
  assert.equal(doc.activeElement, row);
});

test('the filter box hides rows that do not match and the empty note offers a way out', async () => {
  const { root } = boot([unit('alpha'), unit('beta')]);
  await settle();
  const q = all(root, n => n.tag === 'input' && n.type === 'search')[0];
  q.value = 'alp';
  q.dispatch('input');
  await settle();
  assert.deepEqual(names(root), ['alpha']);
  q.value = 'zzz';
  q.dispatch('input');
  await settle();
  assert.deepEqual(names(root), []);
  const empty = byClass(root, 'ov-empty')[0];
  assert.equal(empty.hidden, false);
  assert.match(empty.textContent, /No unit matches the filters\./);
  all(empty, n => n.tag === 'button')[0].click();
  await settle();
  assert.equal(names(root).length, 2);
  assert.equal(empty.hidden, true);
});

test('a row click focuses its unit', async () => {
  const { root, focused } = boot([unit('a'), unit('b')]);
  await settle();
  rows(root)[1].click();
  assert.deepEqual(focused, ['b']);
});

test('the roving tab stop starts on one row and follows focus', async () => {
  const { root } = boot([unit('a'), unit('b')]);
  await settle();
  assert.deepEqual(rows(root).map(r => r.tabIndex), [0, -1]);
  rows(root)[1].focus();
  assert.deepEqual(rows(root).map(r => r.tabIndex), [-1, 0]);
});

test('a unit that leaves the roster loses its row', async () => {
  const { ui, root, statuses, units } = boot([unit('a'), unit('b')]);
  await settle();
  statuses.delete('b');
  units.pop();
  ui.onUnits();
  ticks.forEach(fn => fn());
  await settle();
  assert.deepEqual(names(root), ['a']);
  assert.equal(rows(root).length, 1);
});
