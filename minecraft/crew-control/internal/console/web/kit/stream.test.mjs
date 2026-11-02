import test from 'node:test';
import assert from 'node:assert/strict';
import { installDom, settle } from '../graph/fake-dom.mjs';

const dom = installDom();
const streams = [];
globalThis.EventSource = class {
  static CLOSED = 2;
  constructor(url) { this.url = url; this.readyState = 1; this.l = {}; streams.push(this); }
  addEventListener(t, fn) { this.l[t] = fn; }
  close() { this.readyState = 2; }
  emit(t, data) { this.l[t]({ data }); }
  fail(state) { this.readyState = state; this.onerror(); }
};
const { openStream } = await import('./stream.js');
const { createStore } = await import('./store.js');

// Timers outlive a test on the shared fake clock, so each test starts clean.
const reset = () => {
  streams.length = 0;
  for (const t of dom.timers.pending()) clearTimeout(t);
};

test('delivers named events to their handlers', () => {
  reset();
  const got = [];
  openStream('/s', { units: ev => got.push(['u', ev.data]), log: ev => got.push(['l', ev.data]) });
  streams[0].emit('units', 1);
  streams[0].emit('log', 2);
  assert.deepEqual(got, [['u', 1], ['l', 2]]);
});

test('a stream the browser is still retrying is left alone', () => {
  reset();
  openStream('/s', {});
  streams[0].fail(0);
  assert.equal(streams.length, 1);
  assert.equal(dom.timers.pending().length, 0);
});

test('a closed stream is rebuilt after a backoff that doubles up to the cap', async () => {
  reset();
  openStream('/s', {}, { min: 2000, max: 8000 });
  const waits = [];
  for (let i = 0; i < 5; i++) {
    streams.at(-1).fail(2);
    waits.push(await dom.timers.fire());
  }
  assert.deepEqual(waits, [2000, 4000, 8000, 8000, 8000]);
  assert.equal(streams.length, 6);
});

test('the default backoff runs 2 s to 30 s', async () => {
  reset();
  openStream('/s', {});
  const waits = [];
  for (let i = 0; i < 6; i++) {
    streams.at(-1).fail(2);
    waits.push(await dom.timers.fire());
  }
  assert.deepEqual(waits, [2000, 4000, 8000, 16000, 30000, 30000]);
});

test('a delivered event resets the backoff', async () => {
  reset();
  openStream('/s', { units() {} }, { min: 2000, max: 30000 });
  streams.at(-1).fail(2);
  await dom.timers.fire();
  streams.at(-1).fail(2);
  assert.equal(await dom.timers.fire(), 4000);
  streams.at(-1).emit('units', '[]');
  streams.at(-1).fail(2);
  assert.equal(await dom.timers.fire(), 2000);
});

test('onError runs for every error, including those the browser retries', () => {
  reset();
  const seen = [];
  openStream('/s', {}, { onError: es => seen.push(es.readyState) });
  streams[0].fail(0);
  streams[0].fail(2);
  assert.deepEqual(seen, [0, 2]);
});

test('always rebuilds on any error and asks a url function for a fresh address', async () => {
  reset();
  let head = 5;
  openStream(() => `/tail?after=${head}`, {}, { min: 2000, max: 2000, always: true });
  assert.equal(streams[0].url, '/tail?after=5');
  head = 9;
  streams[0].fail(0);
  assert.equal(streams[0].readyState, 2);
  await dom.timers.fire();
  assert.equal(streams[1].url, '/tail?after=9');
});

test('close stops the pending rebuild and ignores late errors', async () => {
  reset();
  const s = openStream('/s', {});
  streams[0].fail(2);
  assert.equal(dom.timers.pending().length, 1);
  s.close();
  assert.equal(dom.timers.pending().length, 0);
  const s2 = openStream('/s', {});
  const es = streams.at(-1);
  s2.close();
  assert.equal(es.readyState, 2);
  es.onerror();
  assert.equal(dom.timers.pending().length, 0);
  await settle();
});

test('an error handler that closes the stream suppresses the rebuild', () => {
  reset();
  const s = openStream('/s', {}, { onError: () => s.close() });
  streams.at(-1).fail(2);
  assert.equal(dom.timers.pending().length, 0);
});

test('store notifies only subscribers of the changed keys', () => {
  const st = createStore({ a: 1, b: 1 });
  const all = [], onA = [], onB = [];
  st.subscribe((s, p) => all.push([s.a, p.a]));
  st.subscribe(s => onA.push(s.a), 'a');
  st.subscribe(s => onB.push(s.b), 'b');
  st.set({ a: 2 });
  assert.deepEqual(all, [[2, 1]]);
  assert.deepEqual(onA, [2]);
  assert.deepEqual(onB, []);
});

test('store ignores a patch that changes nothing and replaces state immutably', () => {
  const st = createStore({ a: 1 });
  const first = st.get();
  let n = 0;
  st.subscribe(() => n++);
  st.set({ a: 1 });
  assert.equal(n, 0);
  assert.equal(st.get(), first);
  st.set({ a: 2 });
  assert.notEqual(st.get(), first);
  assert.equal(first.a, 1);
});

test('unsubscribe stops notifications, even from inside a notification', () => {
  const st = createStore({ a: 0 });
  const seen = [];
  const off1 = st.subscribe(() => { seen.push(1); off2(); });
  const off2 = st.subscribe(() => seen.push(2));
  st.set({ a: 1 });
  assert.deepEqual(seen, [1]);
  off1();
  st.set({ a: 2 });
  assert.deepEqual(seen, [1]);
});
