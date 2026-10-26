import test from 'node:test';
import assert from 'node:assert/strict';
import {
  addPanel, removePanel, movePanel, resizePanel, normalizeLayout, snapWidth, snapRows, stepWidth,
  loadStore, persist, saveNamed, deleteNamed, cleanName, STORE_KEY, MAX_PANELS, MAX_SAVED, WIDTHS,
} from './dash.js';

const known = new Set(['a', 'b', 'c']);
const L = (...types) => ({ panels: types.map((type, i) => ({ id: `p${i + 1}`, type, w: 4, h: 2 })) });

test('snapWidth lands on the grid', () => {
  assert.equal(snapWidth(5), 4);
  assert.equal(snapWidth(100), 12);
  assert.equal(snapWidth('x'), 6);
  assert.ok(WIDTHS.includes(snapWidth(7)));
  assert.equal(snapRows(9), 4);
  assert.equal(snapRows(0), 1);
});

test('normalizeLayout drops unknown types, fixes ids and sizes', () => {
  const out = normalizeLayout({ panels: [{ id: 'p1', type: 'a', w: 5, h: 9 }, { id: 'p1', type: 'b' }, { type: 'gone' }, null, { id: 'BAD ID', type: 'c', w: 3, h: 1 }] }, known);
  assert.deepEqual(out.panels.map(p => p.type), ['a', 'b', 'c']);
  assert.equal(new Set(out.panels.map(p => p.id)).size, 3);
  assert.deepEqual([out.panels[0].w, out.panels[0].h], [4, 4]);
  assert.deepEqual(normalizeLayout('junk', known), { panels: [] });
});

test('normalizeLayout caps the panel count', () => {
  const many = { panels: Array.from({ length: MAX_PANELS + 5 }, (_, i) => ({ id: `x${i}`, type: 'a' })) };
  assert.equal(normalizeLayout(many, known).panels.length, MAX_PANELS);
});

test('add gives a fresh id and respects the cap', () => {
  let l = L('a', 'b');
  l = addPanel(l, 'c', { w: 6, h: 3 });
  assert.equal(l.panels.at(-1).id, 'p3');
  assert.deepEqual([l.panels.at(-1).w, l.panels.at(-1).h], [6, 3]);
  const full = normalizeLayout({ panels: Array.from({ length: MAX_PANELS }, (_, i) => ({ id: `x${i}`, type: 'a' })) }, known);
  assert.equal(addPanel(full, 'a'), full);
});

test('add never reuses an id of a removed panel that is still referenced', () => {
  let l = L('a', 'b', 'c');
  l = removePanel(l, 'p1');
  l = addPanel(l, 'a');
  assert.equal(new Set(l.panels.map(p => p.id)).size, 3);
});

test('move reorders and clamps', () => {
  const l = L('a', 'b', 'c');
  assert.deepEqual(movePanel(l, 'p3', 0).panels.map(p => p.type), ['c', 'a', 'b']);
  assert.deepEqual(movePanel(l, 'p1', 99).panels.map(p => p.type), ['b', 'c', 'a']);
  assert.equal(movePanel(l, 'p2', 1), l);
  assert.equal(movePanel(l, 'nope', 0), l);
});

test('resize snaps and keeps the other axis', () => {
  const l = resizePanel(L('a'), 'p1', { w: 11 });
  assert.deepEqual([l.panels[0].w, l.panels[0].h], [12, 2]);
  assert.equal(stepWidth(12, 1), 12);
  assert.equal(stepWidth(3, -1), 3);
  assert.equal(stepWidth(4, 1), 6);
});

const mem = (init = null) => {
  const m = new Map(init ? [[STORE_KEY, init]] : []);
  return { getItem: k => m.get(k) ?? null, setItem: (k, v) => m.set(k, v), m };
};

test('store survives a round trip through storage', () => {
  const st = mem();
  let store = { current: L('a', 'b'), saved: {} };
  const r = saveNamed(store, '  Ops  ', L('c'));
  assert.equal(r.name, 'Ops');
  store = r.store;
  assert.ok(persist(st, store));
  const back = loadStore(st, known);
  assert.deepEqual(back.current.panels.map(p => p.type), ['a', 'b']);
  assert.deepEqual(back.saved.Ops.panels.map(p => p.type), ['c']);
  assert.deepEqual(deleteNamed(back, 'Ops').saved, {});
});

test('corrupt or hostile storage reads as empty', () => {
  assert.deepEqual(loadStore(mem('{not json'), known), { current: null, saved: {} });
  assert.deepEqual(loadStore(mem('42'), known), { current: null, saved: {} });
  assert.deepEqual(loadStore({ getItem() { throw new Error('denied'); } }, known), { current: null, saved: {} });
  const hostile = JSON.stringify({ current: { panels: [{ type: '__proto__' }] }, saved: { '': L('a'), ok: { panels: 'x' } } });
  const s = loadStore(mem(hostile), known);
  assert.deepEqual(s.current.panels, []);
  assert.deepEqual(Object.keys(s.saved), ['ok']);
});

test('persist reports a refused write', () => {
  assert.equal(persist({ setItem() { throw new Error('quota'); } }, { current: null, saved: {} }), false);
});

test('names are cleaned and saved layouts are capped', () => {
  assert.equal(cleanName('a\u0000b '), 'ab');
  assert.ok(saveNamed({ current: null, saved: {} }, '  ', L('a')).error);
  let store = { current: null, saved: {} };
  for (let i = 0; i < MAX_SAVED; i++) store = saveNamed(store, `n${i}`, L('a')).store;
  assert.ok(saveNamed(store, 'extra', L('a')).error);
  assert.ok(saveNamed(store, 'n0', L('b')).store, 'overwriting an existing name is allowed');
});
