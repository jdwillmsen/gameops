// DOM-level checks for the replay screen against the fake DOM from the graph
// tests: what it asks the API for, that it stays empty without a session, and
// that scrubbing and playback move the clock and the event list.
import test from 'node:test';
import assert from 'node:assert/strict';
import { installDom, settle, byClass, one } from './graph/fake-dom.mjs';

const dom = installDom();
const { doc } = dom;
const draws = [];
const ctx = new Proxy({}, { get: (_, k) => (k === 'arc' ? (...a) => draws.push(a) : () => {}), set: () => true });
const realCreate = doc.createElement.bind(doc);
doc.createElement = tag => {
  const e = realCreate(tag);
  if (tag === 'canvas') e.getContext = () => ctx;
  return e;
};
globalThis.getComputedStyle = () => ({ getPropertyValue: () => '' });
globalThis.addEventListener = () => {};
globalThis.removeEventListener = () => {};
const frames = [];
globalThis.requestAnimationFrame = fn => { frames.push(fn); return frames.length; };
globalThis.cancelAnimationFrame = () => {};
const { initReplay } = await import('./replay-ui.js');

const NOW = Date.now();
const iso = ms => new Date(NOW - ms).toISOString();
const ok = data => ({ ok: true, status: 200, data });

function answer(path) {
  if (path.startsWith('/v1/places')) return ok([]);
  if (path.startsWith('/v1/replay/positions')) {
    const until = new URL(path, 'http://x').searchParams.get('until');
    const end = Date.parse(until);
    const samples = [];
    for (let i = 0; i <= 60; i++) {
      samples.push({ u: 'crew-01', t: end - 600_000 + i * 10_000, x: 300 + i, y: 64, z: -450 });
      samples.push({ u: 'crew-02', t: end - 600_000 + i * 10_000, x: 400, y: 64, z: -400 - i });
    }
    return ok({ samples });
  }
  return ok({ events: [
    { id: 1, unit: 'crew-01', at: iso(500_000), kind: 'task_started', severity: 'info' },
    { id: 2, unit: 'crew-02', at: iso(300_000), kind: 'died', severity: 'warn', pos: { x: 400, y: 64, z: -430 } },
  ] });
}

function page({ operator = { id: 'owner' } } = {}) {
  const calls = [];
  const live = { operator };
  const host = {
    statuses: new Map(),
    order: () => ['crew-01', 'crew-02'],
    operator: () => live.operator,
    api: async (method, path) => { calls.push(path); return answer(path); },
  };
  const ui = initReplay(host);
  const stage = doc.createElement('div');
  doc.body.append(stage);
  return { ui, calls, stage, live, mount: async () => { ui.mount(stage); await settle(); await settle(); } };
}

test.afterEach(() => { doc.body.children.length = 0; frames.length = 0; draws.length = 0; });

test('signed out: shows the sign-in note and asks the API for nothing', async () => {
  const p = page({ operator: null });
  await p.mount();
  assert.equal(p.calls.length, 0);
  assert.match(one(p.stage, 'rp-state').textContent, /Sign in/);
  p.ui.unmount();
});

test('reads positions and events over the chosen window, oldest first', async () => {
  const p = page();
  await p.mount();
  const pos = p.calls.find(c => c.startsWith('/v1/replay/positions'));
  const evs = p.calls.find(c => c.startsWith('/v1/events'));
  const span = u => { const q = new URL(u, 'http://x').searchParams; return Date.parse(q.get('until')) - Date.parse(q.get('since')); };
  assert.equal(span(pos), 600_000);
  assert.equal(span(evs), 600_000);
  assert.equal(new URL(evs, 'http://x').searchParams.get('sort'), 'asc');
  p.ui.unmount();
});

test('scrubbing moves the clock, the map and the event list', async () => {
  const p = page();
  await p.mount();
  const scrub = one(p.stage, 'rp-scrub');
  assert.equal(scrub.max, 600_000);
  assert.equal(byClass(p.stage, 'rp-events')[0].children.length, 1);
  assert.match(byClass(p.stage, 'rp-events')[0].children[0].textContent, /No events yet/);
  draws.length = 0;
  scrub.value = '400000';
  scrub.dispatch('input');
  assert.equal(draws.length, 2);
  const items = byClass(p.stage, 'rp-events')[0].children;
  assert.equal(items.length, 2);
  assert.match(items[0].textContent, /crew-02 died at 400, -430/);
  assert.match(one(p.stage, 'rp-clock').textContent, /400s of 600s/);
  p.ui.unmount();
});

test('picking a unit limits the event list to it', async () => {
  const p = page();
  await p.mount();
  one(p.stage, 'rp-scrub').value = '550000';
  one(p.stage, 'rp-scrub').dispatch('input');
  const unit = byClass(p.stage, 'rp-bar')[0].children.find(c => c.tag === 'select' && c.name === 'unit');
  unit.value = 'crew-01';
  unit.dispatch('change');
  const items = byClass(p.stage, 'rp-events')[0].children;
  assert.equal(items.length, 1);
  assert.match(items[0].textContent, /task_started/);
  p.ui.unmount();
});

test('play advances by the chosen speed and stops at the end', async () => {
  const p = page();
  await p.mount();
  const play = one(p.stage, 'rp-play');
  const x16 = byClass(p.stage, 'rp-transport')[0].children.find(c => c.dataset.speed === '16');
  x16.click();
  assert.equal(x16.attrs['aria-pressed'], 'true');
  play.click();
  assert.equal(frames.length, 1);
  const start = performance.now();
  frames.shift()(start + 1000);
  assert.match(one(p.stage, 'rp-clock').textContent, /(15|16|17)s of 600s/);
  frames.shift()(start + 1e6);
  assert.match(one(p.stage, 'rp-clock').textContent, /600s of 600s/);
  assert.equal(frames.length, 0);
  assert.equal(play.textContent, 'Play');
  p.ui.unmount();
});

test('unmounting drops the positions it held', async () => {
  const p = page();
  await p.mount();
  p.ui.unmount();
  assert.equal(p.ui.key({ key: ' ', target: {} }), false);
});
