'use strict';

// What the history's script does, checked by doing it: the script is run in
// a page of the kind dom.cjs makes, against a map that only remembers which
// day it was last told to show and a server that answers as these tests
// tell it to. Run by `go test ./web/` where there is a node to run it with,
// or by hand: `node web/testdata/history.test.cjs`.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { page } = require('./dom.cjs');

const tick = () => new Promise((done) => setTimeout(done, 0));
const tests = [];
const test = (name, fn) => tests.push([name, fn]);

const day = (name, more) => ({ day: name, takenAt: `${name}T00:20:00Z`, dimensions: [{ id: 'overworld', format: 'webp', tiles: 3, changed: 1 }], ...more });
const THREE = [day('2026-10-12'), day('2026-10-13'), day('2026-10-14', { rendererChanged: true })];

// A page with the history's script and what it asks of the rest of the
// page. answer() is what the server says to GET /api/history.
async function load({ answer = () => ({ days: THREE }), locked = false } = {}) {
  const p = page(new Map(), { files: [] });
  const { win, doc } = p;
  const state = { asked: [], shown: [], ticks: [] };
  if (locked) doc.body.classList.add('locked');
  win.mcmap.past = {
    get: () => (state.shown.length ? state.shown[state.shown.length - 1] : ''),
    set(to) { state.shown.push(to ? to.day : ''); return true; },
  };
  win.fetch = async (address) => {
    state.asked.push(address);
    const given = answer();
    if (given.status) return { ok: false, status: given.status };
    if (given.fails) throw new Error('offline');
    return { ok: true, status: 200, json: async () => JSON.parse(JSON.stringify(given)) };
  };
  // The clock a play runs on is the test's to move.
  win.setInterval = (fn) => { state.ticks.push(fn); return state.ticks.length; };
  win.clearInterval = (id) => { state.ticks[id - 1] = null; };
  vm.runInContext(fs.readFileSync(path.join(__dirname, '..', 'history.js'), 'utf8'), win, { filename: 'history.js' });
  const settle = async () => { for (let i = 0; i < 6; i++) await tick(); };
  await settle();
  const bar = doc.body.querySelector('.timeline');
  const part = (name) => bar.querySelector(`.timeline-${name}`);
  const press = async (name) => { part(name).click(); await settle(); };
  const slide = async (to) => {
    part('range').value = String(to);
    part('range').dispatchEvent({ type: 'input' });
    await settle();
  };
  const beat = async () => { for (const fn of state.ticks) if (fn) fn(); await settle(); };
  const view = async () => { doc.dispatchEvent({ type: 'mcmap:view' }); await settle(); };
  const last = () => state.shown[state.shown.length - 1];
  return { ...p, state, bar, part, press, slide, beat, view, last, settle };
}

test('a server that keeps no history leaves the page without the control, and is not asked again', async () => {
  const p = await load({ answer: () => ({ status: 404 }) });
  assert.equal(p.bar.hidden, true);
  await p.view();
  await p.view();
  assert.equal(p.state.asked.length, 1);
});

test('a server with no day kept yet shows no control either', async () => {
  const p = await load({ answer: () => ({ days: [] }) });
  assert.equal(p.bar.hidden, true);
});

test('nothing is asked while the page is locked, and it is asked once the login is done', async () => {
  const p = await load({ locked: true });
  assert.equal(p.state.asked.length, 0);
  p.doc.body.classList.remove('locked');
  await p.view();
  assert.deepEqual(p.state.asked, ['api/history']);
  assert.equal(p.bar.hidden, false);
});

test('the control opens on now, with the days there are to go back to', async () => {
  const p = await load();
  assert.equal(p.bar.hidden, false);
  assert.equal(p.part('body').hidden, true);
  await p.press('open');
  assert.equal(p.part('body').hidden, false);
  assert.equal(p.part('open').getAttribute('aria-expanded'), 'true');
  assert.equal(p.part('day').textContent, 'Now');
  assert.equal(p.part('range').max, '3');
  assert.equal(p.part('range').value, '3');
  assert.equal(p.part('later').disabled, true);
  assert.equal(p.part('now').disabled, true);
  assert.match(p.part('note').textContent, /3 days kept/);
  assert.equal(p.state.shown.length, 0, 'opening it must not move the map');
});

test('stepping earlier shows the newest day kept, then the one before, and stops at the first', async () => {
  const p = await load();
  await p.press('open');
  await p.press('earlier');
  assert.equal(p.last(), '2026-10-14');
  assert.match(p.part('day').textContent, /14/);
  assert.match(p.part('day').textContent, /2026/);
  assert.equal(p.doc.body.classList.contains('past'), true);
  await p.press('earlier');
  await p.press('earlier');
  assert.equal(p.last(), '2026-10-12');
  assert.equal(p.part('earlier').disabled, true);
  await p.press('later');
  assert.equal(p.last(), '2026-10-13');
});

test('now puts the map back as it is', async () => {
  const p = await load();
  await p.press('open');
  await p.slide(0);
  assert.equal(p.last(), '2026-10-12');
  await p.press('now');
  assert.equal(p.last(), '');
  assert.equal(p.part('day').textContent, 'Now');
  assert.equal(p.doc.body.classList.contains('past'), false);
});

test('closing the control puts the map back as it is', async () => {
  const p = await load();
  await p.press('open');
  await p.slide(1);
  await p.press('open');
  assert.equal(p.part('body').hidden, true);
  assert.equal(p.last(), '');
  assert.equal(p.doc.body.classList.contains('past'), false);
});

test('a day drawn by a different renderer says its differences are not all the world\'s', async () => {
  const p = await load();
  await p.press('open');
  await p.slide(2);
  assert.match(p.part('note').textContent, /renderer/);
  await p.slide(1);
  assert.doesNotMatch(p.part('note').textContent, /renderer/);
  assert.match(p.part('note').textContent, /as they are now/);
});

test('play runs from the day shown to now and stops there', async () => {
  const p = await load();
  await p.press('open');
  await p.slide(1);
  await p.press('play');
  assert.equal(p.part('play').textContent, 'Pause');
  await p.beat();
  assert.equal(p.last(), '2026-10-14');
  await p.beat();
  assert.equal(p.last(), '');
  assert.equal(p.part('play').textContent, 'Play');
  const shown = p.state.shown.length;
  await p.beat();
  assert.equal(p.state.shown.length, shown, 'a play that reached now must not go on');
});

test('play from now starts at the first day kept, and pause holds the day it is on', async () => {
  const p = await load();
  await p.press('open');
  await p.press('play');
  assert.equal(p.last(), '2026-10-12');
  await p.beat();
  assert.equal(p.last(), '2026-10-13');
  await p.press('play');
  assert.equal(p.part('play').textContent, 'Play');
  await p.beat();
  assert.equal(p.last(), '2026-10-13');
});

test('a name from the server that is not a day is not offered', async () => {
  const p = await load({ answer: () => ({ days: [day('2026-10-12'), day('../../etc'), { day: '2026-10-13' }, day('2026-10-14')] }) });
  await p.press('open');
  assert.equal(p.part('range').max, '2');
});

test('a server that cannot be reached is asked again later, and not on every move of the map', async () => {
  let answer = () => ({ fails: true });
  const p = await load({ answer: () => answer() });
  assert.equal(p.bar.hidden, true);
  await p.view();
  assert.equal(p.state.asked.length, 1);
  answer = () => ({ days: THREE });
  p.win.Date = class extends Date { static now() { return Date.now() + 2 * 60_000; } };
  await p.view();
  assert.equal(p.state.asked.length, 2);
  assert.equal(p.bar.hidden, false);
});

(async () => {
  let failed = 0;
  for (const [name, fn] of tests) {
    try {
      await fn();
      console.log(`ok   ${name}`);
    } catch (err) {
      failed += 1;
      console.log(`FAIL ${name}\n     ${String(err && err.stack ? err.stack : err).split('\n').slice(0, 8).join('\n     ')}`);
    }
  }
  console.log(`${tests.length - failed} of ${tests.length} passed`);
  process.exit(failed ? 1 : 0);
})();
