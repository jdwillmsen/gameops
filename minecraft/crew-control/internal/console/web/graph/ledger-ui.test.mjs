import test from 'node:test';
import assert from 'node:assert/strict';
import { installDom, settle, byClass, one, all } from './fake-dom.mjs';

const dom = installDom();
const { doc } = dom;
const { initLedgerGraph } = await import('./ledger-ui.js');

const NOW = Date.parse('2026-10-26T12:00:00Z');
const iso = msAgo => new Date(NOW - msAgo).toISOString();
const ok = data => ({ ok: true, status: 200, data });
const fail = status => ({ ok: false, status, data: { error: 'x' } });
const ps = () => [
  { id: 'quarry-1', name: 'Quarry', kind: 'area', seeded: true, box: { min: { x: 10, y: 60, z: 10 }, max: { x: 30, y: 70, z: 30 } }, items: ['cobblestone'], stock: [] },
  { id: 'chest-1-2-3', name: 'Stone chest', kind: 'chest', box: { min: { x: 1, y: 2, z: 3 }, max: { x: 1, y: 2, z: 3 } }, stock: [
    { item: 'cobblestone', count: 64, seen_at: iso(30_000), unit: 'agate' }] },
];

const pages = [];
function page({ answer = () => ok(ps()), operator = { id: 'owner' } } = {}) {
  const calls = [];
  const live = { operator };
  const host = {
    operator: () => live.operator,
    api: async (method, path) => { calls.push(`${method} ${path}`); return answer(); },
    setOperator: op => { live.operator = op; ui.onOperator(); },
  };
  const ui = initLedgerGraph(host, { now: () => NOW, refreshMs: 3600_000 });
  const stage = doc.createElement('div');
  doc.body.append(stage);
  pages.push(ui);
  return {
    ui, calls, live, stage,
    mount: async () => { ui.mount(stage); await settle(); },
    nodes: () => all(stage, n => n.getAttribute('data-id') !== null).map(n => n.getAttribute('data-id')).sort(),
    node: id => all(stage, n => n.getAttribute('data-id') === id)[0],
    say: () => one(stage, 'dg-empty'),
    side: () => one(stage, 'dg-side'),
  };
}

test.afterEach(() => {
  for (const ui of pages.splice(0)) ui.unmount();
  doc.activeElement = null;
  doc.body.children.length = 0;
});

test('signed out: says so and asks for nothing', async () => {
  const p = page({ operator: null });
  await p.mount();
  assert.match(p.say().textContent, /Sign in/);
  assert.deepEqual(p.calls, []);
  assert.deepEqual(p.nodes(), []);
});

test('draws places, resources and units from GET /v1/places', async () => {
  const p = page();
  await p.mount();
  assert.deepEqual(p.calls, ['GET /v1/places']);
  assert.deepEqual(p.nodes(), ['p:chest-1-2-3', 'p:quarry-1', 'r:cobblestone', 'u:agate']);
  assert.equal(p.say().hidden, true);
  const label = p.node('p:chest-1-2-3').getAttribute('aria-label');
  assert.match(label, /fresh 30s/, 'staleness is in the accessible name as words');
  assert.match(p.node('p:quarry-1').getAttribute('aria-label'), /never seen/);
});

test('a node opens its details with last-known contents and observed time', async () => {
  const p = page();
  await p.mount();
  p.node('p:chest-1-2-3').dispatch('click', {});
  const text = p.side().textContent;
  assert.match(text, /Stone chest/);
  assert.match(text, /cobblestone/);
  assert.match(text, /64/);
  assert.match(text, /Last observed/);
  assert.match(text, /fresh 30s/);
  const closed = { key: 'Escape', preventDefault() {} };
  assert.equal(p.ui.key(closed), true);
  assert.equal(p.side().hidden, true);
});

test('empty ledger and unavailable store are stated, not blank', async () => {
  const e = page({ answer: () => ok([]) });
  await e.mount();
  assert.match(e.say().textContent, /no places/);
  const d = page({ answer: () => fail(503) });
  await d.mount();
  assert.match(d.say().textContent, /unavailable/);
});

test('a 401 clears the operator', async () => {
  const p = page({ answer: () => fail(401) });
  await p.mount();
  assert.equal(p.live.operator, null);
  assert.match(p.say().textContent, /Sign in/);
});

test('map layout redraws the same nodes', async () => {
  const p = page();
  await p.mount();
  const btn = all(p.stage, n => n.textContent === 'Map layout')[0];
  btn.dispatch('click', {});
  assert.deepEqual(p.nodes(), ['p:chest-1-2-3', 'p:quarry-1', 'r:cobblestone', 'u:agate']);
  assert.equal(btn.getAttribute('aria-pressed'), 'true');
});
