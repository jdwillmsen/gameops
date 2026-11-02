import test from 'node:test';
import assert from 'node:assert/strict';
import { installDom, settle, all } from './graph/fake-dom.mjs';

const dom = installDom();
const { doc } = dom;
globalThis.location = { search: '', href: 'http://x/' };
const streams = [];
globalThis.EventSource = class {
  static CLOSED = 2;
  constructor(url) { this.url = url; this.readyState = 1; this.l = {}; streams.push(this); }
  addEventListener(t, fn) { this.l[t] = fn; }
  close() { this.readyState = 2; }
  emit(t, data) { this.l[t]({ data: JSON.stringify(data) }); }
};
// The fake select has no options list; the view reads its length.
const realCreate = doc.createElement.bind(doc);
doc.createElement = tag => {
  const e = realCreate(tag);
  if (tag === 'select') Object.defineProperty(e, 'options', { get: () => e.children.filter(c => c.tag === 'option') });
  return e;
};
const { initExplorer } = await import('./explorer-ui.js');

const ev = (id, o = {}) => ({ id, unit: 'crew-01', at: '2026-10-20T12:00:00Z', kind: 'online', severity: 'ok', payload: { text: `event ${id}` }, ...o });

function boot({ signedIn = true, events = [], total = events.length } = {}) {
  doc.body.replaceChildren();
  doc.activeElement = null;
  streams.length = 0;
  const calls = [];
  const host = {
    statuses: new Map([['crew-01', { name: 'Ann' }]]),
    order: () => ['crew-01'],
    state: () => ({ view: 'events' }),
    operator: () => (signedIn ? { name: 'op' } : null),
    sync() {},
    api: async (method, path) => {
      calls.push(path);
      if (path.startsWith('/v1/explorer/views')) return { ok: true, status: 200, data: { views: [] } };
      return { ok: true, status: 200, data: { events, total, next: '', head: events.length ? events[0].id : 0 } };
    },
  };
  const ui = initExplorer(host);
  const into = doc.createElement('main');
  doc.body.append(into);
  ui.mount(into);
  return { ui, host, calls, into, root: into.children[0] };
}

const heads = root => all(root, n => n.tag === 'th');
const bodyRows = root => all(root, n => n.tag === 'tr' && n.className && n.className.startsWith('ex-row'));
const click = n => n.click();

test('the history table lists the rows newest first with unit names and levels', async () => {
  const { root } = boot({ events: [ev(2, { severity: 'warn', kind: 'died' }), ev(1)] });
  await settle();
  const rows = bodyRows(root);
  assert.equal(rows.length, 2);
  assert.equal(rows[0].className, 'ex-row warn');
  assert.match(rows[0].children[2].textContent, /Ann \(crew-01\)/);
  assert.equal(rows[0].children[3].textContent, 'died');
  assert.equal(rows[0].children[4].textContent, 'event 2');
});

test('sortable headers carry aria-sort and the default sort is time, newest first', async () => {
  const { root } = boot({ events: [ev(1)] });
  await settle();
  assert.deepEqual(heads(root).map(h => h.getAttribute('aria-sort')), ['descending', 'none', 'none', 'none', null]);
});

test('a header click re-sorts by that column through the server', async () => {
  const { root, calls } = boot({ events: [ev(1)] });
  await settle();
  const before = calls.length;
  click(all(heads(root)[3], n => n.tag === 'button')[0]);
  await settle();
  assert.ok(calls.length > before);
  assert.match(calls.at(-1), /sort=kind/);
  assert.match(calls.at(-1), /dir=asc/);
  assert.deepEqual(heads(root).map(h => h.getAttribute('aria-sort')), ['none', 'none', 'none', 'ascending', null]);
});

test('Enter on a row opens its detail and focus stays on that row', async () => {
  const { root } = boot({ events: [ev(1), ev(2)] });
  await settle();
  const row = bodyRows(root)[1];
  row.focus();
  assert.equal(row.getAttribute('aria-expanded'), 'false');
  row.dispatch('keydown', { key: 'Enter', target: row });
  assert.equal(row.getAttribute('aria-expanded'), 'true');
  assert.equal(doc.activeElement, row);
  const full = all(root, n => n.className === 'ex-full');
  assert.equal(full.length, 1);
  assert.match(full[0].textContent, /"id": 2/);
  row.dispatch('keydown', { key: ' ', target: row });
  assert.equal(all(root, n => n.className === 'ex-full').length, 0);
});

test('an operator sees the position in an opened detail', async () => {
  const pos = { x: 1, y: 2, z: 3 };
  const { root } = boot({ events: [ev(1, { pos })] });
  await settle();
  click(bodyRows(root)[0]);
  assert.match(all(root, n => n.className === 'ex-full')[0].textContent, /"pos"/);
});

test('an empty history says so', async () => {
  const { root } = boot({ events: [] });
  await settle();
  const note = all(root, n => n.className === 'ex-empty')[0];
  assert.equal(note.textContent, 'No events yet.');
});

test('a failed load shows the server error in the note', async () => {
  doc.body.replaceChildren();
  const host = {
    statuses: new Map(), order: () => [], state: () => ({ view: 'events' }), operator: () => ({}), sync() {},
    api: async () => ({ ok: false, status: 503, data: null }),
  };
  const ui = initExplorer(host);
  const into = doc.createElement('main');
  doc.body.append(into);
  ui.mount(into);
  await settle();
  const note = all(into, n => n.className === 'ex-empty err')[0];
  assert.equal(note.textContent, 'The event store is not ready.');
});

test('a live tail row is added to the table and an existing row element survives', async () => {
  const { root } = boot({ events: [ev(1)] });
  await settle();
  const first = bodyRows(root)[0];
  assert.equal(streams.length, 1);
  assert.match(streams[0].url, /^\/v1\/explorer\/tail\?/);
  assert.match(streams[0].url, /after=1$/);
  streams[0].emit('events', { head: 2, events: [ev(2, { kind: 'died' })] });
  const rows = bodyRows(root);
  assert.equal(rows.length, 2);
  assert.equal(rows[0].children[3].textContent, 'died');
  assert.equal(rows[1], first);
});

test('a focused row keeps focus when a live row arrives', async () => {
  const { root } = boot({ events: [ev(1)] });
  await settle();
  const row = bodyRows(root)[0];
  row.focus();
  streams[0].emit('events', { head: 2, events: [ev(2)] });
  assert.equal(doc.activeElement, row);
});

test('a dropped tail is reopened from the newest id seen', async () => {
  const { root } = boot({ events: [ev(1)] });
  await settle();
  streams[0].emit('events', { head: 5, events: [ev(5)] });
  streams[0].readyState = 0;
  streams[0].onerror();
  await dom.timers.fire();
  assert.equal(streams.length, 2);
  assert.match(streams[1].url, /after=5$/);
  assert.equal(bodyRows(root).length, 2);
});

test('signed out, the table shows the public feed and opens no tail', async () => {
  const { ui, root } = boot({ signedIn: false });
  ui.onEvents([{ seq: 3, unit: 'crew-01', at: '2026-10-20T12:00:00Z', kind: 'online', level: 'ok', text: 'hello' }], false);
  await settle();
  assert.equal(bodyRows(root).length, 1);
  assert.equal(streams.length, 0);
  assert.equal(bodyRows(root)[0].children[4].textContent, 'hello');
});

test('unmounting closes the tail', async () => {
  const { ui } = boot({ events: [ev(1)] });
  await settle();
  ui.unmount();
  assert.equal(streams[0].readyState, 2);
});
