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

// Three days as a server that keeps markers lists them: one that kept the
// overworld's, one from before any were kept, and one that kept the
// overworld's and was drawn by another renderer.
const marked = (name, more) => ({ ...day(name, more), dimensions: [{ id: 'overworld', format: 'webp', tiles: 3, changed: 1, markers: true }, { id: 'nether', format: 'webp', tiles: 1, changed: 0 }] });
const MARKED = [marked('2026-10-12'), day('2026-10-13'), marked('2026-10-14', { rendererChanged: true })];

// A page with the history's script and what it asks of the rest of the
// page. answer() is what the server says to GET /api/history, and wanted
// the day the page's address names, which the map takes for the day it is
// told as the real one does. follows is whether the map and the markers'
// script are ones that draw a day's markers, which is what the note goes
// by; the map then answers for the day it was last told.
// today is the day it is, which is where the scrubber's days end; kept is
// what the browser holds for the page, with whose settings script the
// page is then loaded, as it is when a play's speed is to be remembered.
async function load({ answer = () => ({ days: THREE }), locked = false, wanted = '', dimension = 'overworld', follows = false, today = '2026-10-14', kept = null, pairs = false, beside = '' } = {}) {
  const p = page(kept || new Map(), { files: kept ? ['settings.js'] : [] });
  const { win, doc } = p;
  const state = { asked: [], shown: [], ticks: [], paces: [], dimension, wanted, right: [], beside, cut: 0.5 };
  // The clock is the test's: noon of the day it is, and later when told.
  const noon = Date.parse(`${today}T12:00:00Z`);
  const clock = (ms) => { win.Date = class extends Date { static now() { return noon + ms; } }; };
  clock(0);
  if (locked) doc.body.classList.add('locked');
  win.mcmap.dimension = () => state.dimension;
  win.mcmap.past = {
    get: () => (state.shown.length ? state.shown[state.shown.length - 1] : ''),
    wanted: () => state.wanted,
    set(to) {
      state.shown.push(to ? to.day : '');
      state.wanted = to ? to.day : '';
      state.day = to;
      // As the map does: now is beside nothing, and a day set by whoever
      // compares nothing drops what the address asked for beside it.
      if (!to && state.comparing) { state.comparing = false; state.right.push('off'); }
      if (!state.comparing) state.beside = '';
      return true;
    },
  };
  // A map that can draw a second day beside the one shown: right is what
  // it was told to put there, a date, 'now', or 'off' when told to stop,
  // and beside what the address asks for, as the map takes it.
  if (pairs) {
    win.mcmap.past.compare = {
      on: () => state.comparing === true,
      wanted: () => state.beside,
      start(to) {
        if (!state.day) return false;
        state.comparing = true;
        state.right.push(to ? to.day : 'now');
        state.beside = to ? to.day : 'now';
        return true;
      },
      stop() {
        state.comparing = false;
        state.right.push('off');
        state.beside = '';
      },
      cut(to) {
        if (Number.isFinite(to)) state.cut = Math.min(1, Math.max(0, to));
        return state.cut;
      },
    };
  }
  if (follows) {
    win.mcmap.past.markers = (id) => Boolean(state.day) && state.day.dimensions.some((d) => d.id === id && d.markers === true);
    win.mcmap.markers = { day: () => (state.day ? state.day.day : '') };
  }
  win.fetch = async (address) => {
    state.asked.push(address);
    const given = answer();
    if (given.status) return { ok: false, status: given.status };
    if (given.fails) throw new Error('offline');
    return { ok: true, status: 200, json: async () => JSON.parse(JSON.stringify(given)) };
  };
  // The clock a play runs on is the test's to move. paces is how long
  // each play that was started waits between two days.
  win.setInterval = (fn, ms) => { state.ticks.push(fn); state.paces.push(ms); return state.ticks.length; };
  win.clearInterval = (id) => { state.ticks[id - 1] = null; };
  vm.runInContext(fs.readFileSync(path.join(__dirname, '..', 'history.js'), 'utf8'), win, { filename: 'history.js' });
  const settle = async () => { for (let i = 0; i < 6; i++) await tick(); };
  await settle();
  const bar = doc.body.querySelector('.timeline');
  const part = (name) => bar.querySelector(`.timeline-${name}`);
  const parts = (name) => bar.querySelectorAll(`.timeline-${name}`);
  const press = async (name) => { part(name).click(); await settle(); };
  const key = async (name, pressed) => { part(name).dispatchEvent({ type: 'keydown', key: pressed }); await settle(); };
  // The scrubber is a slider over the days kept and now: where it stands,
  // and its last step, which is now.
  const value = () => part('range').getAttribute('aria-valuenow');
  const max = () => part('range').getAttribute('aria-valuemax');
  // To a day by its place among the days kept, by the keys.
  const slide = async (to) => {
    await key('range', 'Home');
    for (let i = 0; i < to; i++) await key('range', 'ArrowRight');
  };
  // The scrubber as it would be laid out, so many pixels to a calendar
  // day, and a pointer put down on it, moved and lifted.
  const WIDE = 20;
  const slots = () => parts('tick').length;
  part('range').getBoundingClientRect = () => ({ left: 100, top: 0, width: slots() * WIDE, height: 40 });
  const point = async (type, slot) => { part('range').dispatchEvent({ type, pointerId: 1, button: 0, clientX: 100 + slot * WIDE + WIDE / 2 }); await settle(); };
  // The calendar's day of that date.
  const cell = (day) => parts('cell').find((node) => node.dataset.day === day) || null;
  const beat = async () => { for (const fn of state.ticks) if (fn) fn(); await settle(); };
  const view = async () => { doc.dispatchEvent({ type: 'mcmap:view' }); await settle(); };
  // What a comparison puts over the map is in the map's box, not the dock.
  const over = (name) => doc.getElementById('map').querySelector(`.timeline-${name}`);
  // The address has changed to one that names this day, or none, and
  // beside it this, if anything.
  const named = async (to, right = '') => {
    state.wanted = to;
    state.beside = right;
    doc.dispatchEvent({ type: 'mcmap:day' });
    await settle();
  };
  const last = () => state.shown[state.shown.length - 1];
  return { ...p, state, bar, part, parts, over, press, key, value, max, slide, slots, point, cell, beat, view, named, last, settle, clock };
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

test('nothing is asked before the map has shown a dimension, when it is not yet known there is a session', async () => {
  const p = await load({ dimension: null });
  await p.view();
  assert.equal(p.state.asked.length, 0);
  p.state.dimension = 'overworld';
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
  assert.equal(p.max(), '3');
  assert.equal(p.value(), '3');
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
  await p.press('close');
  assert.equal(p.part('body').hidden, true);
  assert.equal(p.last(), '');
  assert.equal(p.doc.body.classList.contains('past'), false);
});

test('a day says only its terrain is of the day, and one drawn by a different renderer says that as well', async () => {
  const p = await load();
  await p.press('open');
  await p.slide(2);
  assert.match(p.part('note').textContent, /renderer/);
  for (const still of [/Players/, /markers/, /structures/, /biomes/, /as they are now/]) assert.match(p.part('note').textContent, still);
  await p.slide(1);
  assert.doesNotMatch(p.part('note').textContent, /renderer/);
  for (const still of [/Players/, /markers/, /structures/, /biomes/, /as they are now/]) assert.match(p.part('note').textContent, still);
});

test('a day that kept nothing of the dimension shown says so, and stops saying so when one it kept is shown', async () => {
  const p = await load();
  await p.press('open');
  await p.slide(1);
  assert.match(p.part('note').textContent, /The terrain is of this day/);
  p.state.dimension = 'nether';
  await p.view();
  assert.match(p.part('note').textContent, /kept nothing of this dimension/);
  assert.doesNotMatch(p.part('note').textContent, /The terrain is of this day/);
  assert.match(p.part('note').textContent, /as they are now/);
  await p.press('now');
  assert.doesNotMatch(p.part('note').textContent, /kept nothing/);
  await p.slide(1);
  assert.match(p.part('note').textContent, /kept nothing of this dimension/);
  p.state.dimension = 'overworld';
  await p.view();
  assert.match(p.part('note').textContent, /The terrain is of this day/);
});

// What the note must never do is call something of the day that is of
// now, or the other way about. Each case is read for both halves.
const OF_NOW = [/Players/, /loaded mobs/, /waypoints/, /structures/, /biomes/, /as they are now/];

test('a day that kept its markers says the terrain and the beds, containers and named mobs are of the day', async () => {
  const p = await load({ answer: () => ({ days: MARKED }), follows: true });
  await p.press('open');
  await p.slide(0);
  const said = p.part('note').textContent;
  assert.match(said, /The terrain is of this day, and so are the beds, containers and named mobs/);
  for (const still of OF_NOW) assert.match(said, still);
  assert.doesNotMatch(said, /markers/, 'a waypoint is a marker too, and is of now');
  assert.doesNotMatch(said, /not kept/);
  assert.doesNotMatch(said, /renderer/);
  await p.slide(2);
  assert.match(p.part('note').textContent, /and so are the beds, containers and named mobs/);
  assert.match(p.part('note').textContent, /renderer/);
});

test('a day that kept its terrain and not its markers says none are drawn, and does not call them the day\'s or today\'s', async () => {
  const p = await load({ answer: () => ({ days: MARKED }), follows: true });
  await p.press('open');
  await p.slide(1);
  const said = p.part('note').textContent;
  assert.match(said, /The terrain is of this day\./);
  assert.match(said, /beds, containers and named mobs were not kept, so none are drawn/);
  assert.doesNotMatch(said, /and so are/);
  for (const still of OF_NOW) assert.match(said, still);
  assert.doesNotMatch(said, /markers/);
});

test('a dimension whose markers the day did not keep says so, though the day kept another dimension\'s', async () => {
  const p = await load({ answer: () => ({ days: MARKED }), follows: true, dimension: 'nether' });
  await p.press('open');
  await p.slide(0);
  assert.match(p.part('note').textContent, /were not kept, so none are drawn/);
  p.state.dimension = 'overworld';
  await p.view();
  assert.match(p.part('note').textContent, /and so are the beds, containers and named mobs/);
});

test('a day that kept nothing of the dimension shown says neither terrain nor markers are drawn', async () => {
  const p = await load({ answer: () => ({ days: MARKED }), follows: true, dimension: 'end' });
  await p.press('open');
  await p.slide(0);
  const said = p.part('note').textContent;
  assert.match(said, /kept nothing of this dimension, so no terrain is drawn, and no beds, containers or named mobs/);
  assert.doesNotMatch(said, /The terrain is of this day/);
  for (const still of OF_NOW) assert.match(said, still);
  assert.doesNotMatch(said, /markers/, 'none of today\'s are drawn there, so none are said to be');
});

test('on a page whose markers do not follow the day, a day that kept them is still said to have today\'s', async () => {
  const p = await load({ answer: () => ({ days: MARKED }) });
  await p.press('open');
  await p.slide(0);
  assert.match(p.part('note').textContent, /The terrain is of this day\. Players, markers, structures and biomes are as they are now\./);
  // The map can say what a day kept and the markers' script cannot draw it.
  p.win.mcmap.past.markers = () => true;
  await p.view();
  assert.match(p.part('note').textContent, /Players, markers, structures and biomes are as they are now/);
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

// Four days kept over ten, with days nothing was kept of between them and
// a month beginning on one of those.
const GAPPED = [day('2026-09-28'), day('2026-09-29'), day('2026-10-02'), day('2026-10-05')];
const gapped = (more) => load({ answer: () => ({ days: GAPPED }), today: '2026-10-07', ...more });

test('the control is a dock under the map, in a column with it, whose button goes while it is open and comes back with the focus', async () => {
  const p = await load();
  const column = p.bar.parentNode;
  const map = p.doc.getElementById('map');
  assert.equal(column.className, 'timeline-column');
  assert.deepEqual(column.children, [map, p.bar], 'the dock is the foot of the map\'s own column');
  assert.equal(column.parentNode.className, 'stage');
  assert.equal(p.doc.getElementById('layers').parentNode, column.parentNode, 'the layer panel is beside the column, not in it');
  assert.equal(map.querySelector('.timeline'), null, 'nothing of the dock is over the map');
  assert.equal(p.part('open').hidden, false);
  await p.press('open');
  assert.equal(p.part('open').hidden, true);
  assert.equal(p.doc.body.classList.contains('timeline-shown'), true);
  assert.equal(p.doc.activeElement, p.part('date'));
  await p.press('close');
  assert.equal(p.part('body').hidden, true);
  assert.equal(p.part('open').hidden, false);
  assert.equal(p.part('open').getAttribute('aria-expanded'), 'false');
  assert.equal(p.doc.body.classList.contains('timeline-shown'), false);
  assert.equal(p.doc.activeElement, p.part('open'));
});

test('the scrubber has a tick for every calendar day since the first one kept, a gap where none was, a taller one where a month begins, and now at its end', async () => {
  const p = await gapped();
  await p.press('open');
  const ticks = p.parts('tick').map((node) => node.className.replace(/timeline-tick ?/, ''));
  assert.deepEqual(ticks, ['kept', 'kept', 'gap', 'gap month', 'kept', 'gap', 'gap', 'kept', 'gap', 'gap', 'now']);
  const names = p.parts('month-name').map((node) => node.textContent);
  assert.equal(names.length, 2, 'the month the days begin in has no room for its name three days before the next');
  assert.match(names[0], /2026/);
  assert.equal(names[1], 'Now');
  assert.equal(p.part('range').style.getPropertyValue('--timeline-slots'), '11');
  assert.equal(p.part('range').style.getPropertyValue('--timeline-at'), '10', 'now is the last place');
  assert.equal(p.part('range').getAttribute('role'), 'slider');
  assert.equal(p.max(), '4', 'the slider\'s steps are the days kept and now, not the days of the calendar');
  assert.equal(p.value(), '4');
});

test('the scrubber grows by a day when the day turns, with nothing kept of it yet', async () => {
  const p = await gapped();
  await p.press('open');
  assert.equal(p.slots(), 11);
  p.clock(86_400_000);
  await p.view();
  assert.equal(p.slots(), 12);
  assert.equal(p.max(), '4');
});

test('a press on the scrubber goes to the day kept nearest it, never to a day nothing was kept of, and to now at its end', async () => {
  const p = await gapped();
  await p.press('open');
  for (const [slot, to] of [[2, '2026-09-29'], [3, '2026-10-02'], [4, '2026-10-02'], [5, '2026-10-02'], [6, '2026-10-05'], [8, '2026-10-05'], [9, ''], [0, '2026-09-28'], [10, '']]) {
    await p.point('pointerdown', slot);
    await p.point('pointerup', slot);
    assert.equal(p.last(), to, `a press on place ${slot}`);
  }
  assert.equal(p.part('day').textContent, 'Now');
  assert.equal(p.doc.activeElement, p.part('range'), 'a press leaves the keys on the scrubber');
});

test('a drag along the scrubber goes through the days kept, and ends when the pointer is lifted', async () => {
  const p = await gapped();
  await p.press('open');
  await p.point('pointerdown', 0);
  assert.equal(p.last(), '2026-09-28');
  assert.equal(p.part('range').style.getPropertyValue('--timeline-at'), '0');
  await p.point('pointermove', 3);
  assert.equal(p.last(), '2026-10-02');
  assert.equal(p.part('range').style.getPropertyValue('--timeline-at'), '4', 'the mark is on the day chosen, not under the pointer');
  await p.point('pointermove', 7);
  assert.equal(p.last(), '2026-10-05');
  await p.point('pointerup', 7);
  const shown = p.state.shown.length;
  await p.point('pointermove', 0);
  assert.equal(p.state.shown.length, shown);
});

test('the arrow keys step the scrubber between days kept, Home and End go to the first day and to now, and the date is what it says', async () => {
  const p = await gapped();
  await p.press('open');
  await p.key('range', 'ArrowLeft');
  assert.equal(p.last(), '2026-10-05');
  assert.equal(p.value(), '3');
  assert.match(p.part('range').getAttribute('aria-valuetext'), /5/);
  assert.match(p.part('range').getAttribute('aria-valuetext'), /2026/);
  await p.key('range', 'ArrowDown');
  assert.equal(p.last(), '2026-10-02', 'the days between that kept nothing are passed over');
  await p.key('range', 'Home');
  assert.equal(p.last(), '2026-09-28');
  await p.key('range', 'ArrowLeft');
  assert.equal(p.last(), '2026-09-28');
  await p.key('range', 'ArrowUp');
  assert.equal(p.last(), '2026-09-29');
  await p.key('range', 'PageUp');
  assert.equal(p.last(), '');
  assert.equal(p.part('range').getAttribute('aria-valuetext'), 'Now');
  await p.key('range', 'PageDown');
  assert.equal(p.last(), '2026-09-28');
  await p.key('range', 'End');
  assert.equal(p.last(), '');
  const shown = p.state.shown.length;
  await p.key('range', 'a');
  assert.equal(p.state.shown.length, shown, 'a key that is not the slider\'s is left alone');
});

test('a step along the scrubber stops a play', async () => {
  const p = await gapped();
  await p.press('open');
  await p.press('play');
  await p.key('range', 'ArrowRight');
  assert.equal(p.part('play').textContent, 'Play');
  await p.press('play');
  await p.point('pointerdown', 7);
  assert.equal(p.part('play').textContent, 'Play');
  assert.equal(p.last(), '2026-10-05');
});

test('the day opens a calendar of its month, with the days kept to press, the days nothing was kept of struck out, and the rest out of reach', async () => {
  const p = await gapped();
  await p.press('open');
  assert.equal(p.part('calendar').hidden, true);
  await p.press('date');
  assert.equal(p.part('calendar').hidden, false);
  assert.equal(p.part('date').getAttribute('aria-expanded'), 'true');
  assert.notEqual(p.part('calendar').tagName, 'DIALOG', 'the map under it is still the map');
  assert.equal(p.part('calendar').getAttribute('aria-modal'), 'false');
  assert.match(p.part('month').textContent, /October/, 'from now it opens on the month of the newest day');
  assert.equal(p.parts('cell').length, 31);
  const pressable = p.parts('cell').filter((node) => !node.disabled).map((node) => node.dataset.day);
  assert.deepEqual(pressable, ['2026-10-02', '2026-10-05']);
  for (const none of ['2026-10-01', '2026-10-03', '2026-10-07']) {
    assert.equal(p.cell(none).classList.contains('gap'), true, none);
    assert.match(p.cell(none).getAttribute('aria-label'), /no day kept/);
  }
  assert.equal(p.cell('2026-10-08').classList.contains('gap'), false, 'a day that has not come is not a day nothing was kept of');
  assert.match(p.cell('2026-10-08').getAttribute('aria-label'), /outside/);
  assert.equal(p.part('count').textContent, '2 of 7 days kept');
  assert.equal(p.part('month-after').disabled, true);
  assert.equal(p.part('month-before').disabled, false);
  assert.equal(p.doc.activeElement, p.cell('2026-10-02'), 'the focus is on a day that can be pressed');
  assert.equal(p.state.shown.length, 0, 'opening it must not move the map');
});

test('the calendar turns only through the months that have a day, and marks the day shown', async () => {
  const p = await gapped();
  await p.press('open');
  await p.slide(1);
  await p.press('date');
  assert.match(p.part('month').textContent, /September/, 'it opens on the month of the day shown');
  assert.equal(p.cell('2026-09-29').getAttribute('aria-pressed'), 'true');
  assert.equal(p.cell('2026-09-28').getAttribute('aria-pressed'), 'false');
  assert.equal(p.doc.activeElement, p.cell('2026-09-29'));
  assert.equal(p.part('count').textContent, '2 of 3 days kept');
  assert.equal(p.cell('2026-09-27').disabled, true);
  assert.equal(p.cell('2026-09-27').classList.contains('gap'), false);
  assert.equal(p.cell('2026-09-30').classList.contains('gap'), true);
  assert.equal(p.part('month-before').disabled, true);
  await p.press('month-before');
  assert.match(p.part('month').textContent, /September/);
  await p.press('month-after');
  assert.match(p.part('month').textContent, /October/);
  assert.equal(p.part('month-after').disabled, true);
  await p.press('month-after');
  assert.match(p.part('month').textContent, /October/);
});

test('choosing a day in the calendar shows it, puts the calendar away and gives the focus back to the day', async () => {
  const p = await gapped();
  await p.press('open');
  await p.press('play');
  await p.press('date');
  await p.press('month-after');
  p.cell('2026-10-01').click();
  await p.settle();
  assert.equal(p.part('calendar').hidden, false, 'a day nothing was kept of cannot be chosen');
  p.cell('2026-10-05').click();
  await p.settle();
  assert.equal(p.last(), '2026-10-05');
  assert.equal(p.part('calendar').hidden, true);
  assert.equal(p.part('date').getAttribute('aria-expanded'), 'false');
  assert.equal(p.doc.activeElement, p.part('date'));
  assert.equal(p.part('play').textContent, 'Play', 'a day chosen by hand stops a play');
  assert.match(p.part('day').textContent, /5/);
});

test('Escape puts the calendar away with the focus back on the day, and a press elsewhere puts it away where the press went', async () => {
  const p = await gapped();
  await p.press('open');
  await p.press('date');
  let passed = 0;
  p.doc.addEventListener('keydown', () => { passed += 1; });
  p.cell('2026-10-05').dispatchEvent({ type: 'keydown', key: 'Escape' });
  await p.settle();
  assert.equal(p.part('calendar').hidden, true);
  assert.equal(p.doc.activeElement, p.part('date'));
  assert.equal(p.state.shown.length, 0);
  await p.press('date');
  p.part('calendar').dispatchEvent({ type: 'pointerdown' });
  assert.equal(p.part('calendar').hidden, false, 'a press in the calendar is not a press elsewhere');
  p.part('play').focus();
  p.part('play').dispatchEvent({ type: 'pointerdown' });
  assert.equal(p.part('calendar').hidden, true);
  assert.equal(p.doc.activeElement, p.part('play'));
  // With no calendar open the key is whoever else's it is.
  const before = passed;
  p.part('play').dispatchEvent({ type: 'keydown', key: 'Escape' });
  assert.equal(passed, before + 1);
});

test('the arrow keys in the calendar go to the next day kept that way, and closing the dock puts the calendar away', async () => {
  const p = await gapped();
  await p.press('open');
  await p.press('date');
  p.cell('2026-10-02').dispatchEvent({ type: 'keydown', key: 'ArrowRight' });
  assert.equal(p.doc.activeElement, p.cell('2026-10-05'));
  p.cell('2026-10-05').dispatchEvent({ type: 'keydown', key: 'ArrowRight' });
  assert.equal(p.doc.activeElement, p.cell('2026-10-05'), 'there is no day kept after it');
  p.cell('2026-10-05').dispatchEvent({ type: 'keydown', key: 'ArrowLeft' });
  assert.equal(p.doc.activeElement, p.cell('2026-10-02'));
  await p.press('close');
  assert.equal(p.part('calendar').hidden, true);
  await p.press('open');
  assert.equal(p.part('calendar').hidden, true);
});

test('a play goes at the speed chosen, twice and four times as fast, and a speed chosen while it runs is taken up at once', async () => {
  const p = await load();
  await p.press('open');
  const chosen = () => p.parts('times').map((node) => node.getAttribute('aria-checked'));
  assert.deepEqual(chosen(), ['true', 'false', 'false']);
  await p.press('play');
  assert.deepEqual(p.state.paces, [1500]);
  p.parts('times')[1].click();
  await p.settle();
  assert.deepEqual(chosen(), ['false', 'true', 'false']);
  assert.deepEqual(p.state.paces, [1500, 750]);
  assert.equal(p.state.ticks.filter(Boolean).length, 1, 'the play it was is not left running beside the faster one');
  assert.equal(p.part('play').textContent, 'Pause');
  await p.beat();
  assert.equal(p.last(), '2026-10-13');
  await p.press('play');
  p.parts('times')[2].click();
  await p.settle();
  assert.deepEqual(p.state.paces, [1500, 750], 'a speed chosen while nothing plays starts nothing');
  await p.press('play');
  assert.deepEqual(p.state.paces, [1500, 750, 375]);
  // Where there is no room for the three, one button goes round them.
  assert.equal(p.part('pace').textContent, '4×');
  await p.press('pace');
  assert.equal(p.part('pace').textContent, '1×');
  assert.deepEqual(chosen(), ['true', 'false', 'false']);
  assert.equal(p.state.paces.at(-1), 1500);
  p.part('speed').dispatchEvent({ type: 'keydown', key: 'ArrowRight' });
  assert.deepEqual(chosen(), ['false', 'true', 'false']);
  assert.equal(p.doc.activeElement, p.parts('times')[1]);
});

test('a play that loops starts again from the first day in place of going to now, until the loop is taken off', async () => {
  const p = await load();
  await p.press('open');
  assert.equal(p.part('loop').getAttribute('aria-pressed'), 'false');
  await p.press('loop');
  assert.equal(p.part('loop').getAttribute('aria-pressed'), 'true');
  await p.slide(1);
  await p.press('play');
  await p.beat();
  assert.equal(p.last(), '2026-10-14');
  await p.beat();
  assert.equal(p.last(), '2026-10-12', 'from the newest day it goes round to the first');
  assert.equal(p.part('play').textContent, 'Pause');
  await p.beat();
  await p.beat();
  await p.beat();
  assert.equal(p.last(), '2026-10-12');
  assert.equal(p.state.shown.includes(''), false, 'a play that loops never rests on now');
  await p.press('loop');
  await p.beat();
  await p.beat();
  await p.beat();
  assert.equal(p.last(), '');
  assert.equal(p.part('play').textContent, 'Play');
});

test('a map that cannot draw two days has no Compare to press', async () => {
  const p = await load();
  await p.press('open');
  assert.equal(p.part('compare').hidden, true);
  assert.equal(p.over('divide').hidden, true);
});

test('Compare from now chooses the newest day kept, puts now on the right, and shows the divider with a day named either side', async () => {
  const p = await load({ pairs: true });
  await p.press('open');
  assert.equal(p.part('compare').hidden, false);
  assert.equal(p.part('compare').getAttribute('aria-pressed'), 'false');
  await p.press('compare');
  assert.deepEqual(p.state.shown, ['2026-10-14'], 'a comparison needs a day on the left');
  assert.deepEqual(p.state.right, ['now']);
  assert.equal(p.part('compare').getAttribute('aria-pressed'), 'true');
  assert.equal(p.over('divide').hidden, false);
  assert.match(p.over('left').textContent, /14/);
  assert.equal(p.over('right').textContent, 'Now');
  assert.equal(p.over('divider').getAttribute('role'), 'slider');
  assert.equal(p.over('divider').getAttribute('aria-valuenow'), '50');
  assert.equal(p.doc.body.classList.contains('comparing'), true);
  // The scrubber shows where the day on the right is, at its end for now.
  assert.equal(p.parts('handle').find((node) => node.classList.contains('other')).hidden, false);
  assert.equal(p.part('range').style.getPropertyValue('--timeline-other'), '3');
});

test('compared, the note says which side is the day and what is on the other, and whose the markers are', async () => {
  const p = await load({ answer: () => ({ days: MARKED }), follows: true, pairs: true });
  await p.press('open');
  await p.slide(0);
  await p.press('compare');
  let said = p.part('note').textContent;
  assert.match(said, /The terrain left of the divider is of this day, and so are the beds, containers and named mobs on either side/);
  assert.match(said, /Right of the divider is the terrain as it is now\./);
  for (const still of OF_NOW) assert.match(said, still);
  assert.doesNotMatch(said, /The terrain is of this day/, 'the terrain on the right is not of this day');
  await p.slide(1);
  said = p.part('note').textContent;
  assert.match(said, /The terrain left of the divider is of this day\. Its beds, containers and named mobs were not kept, so none are drawn on either side\./);
  p.state.dimension = 'end';
  await p.view();
  said = p.part('note').textContent;
  assert.match(said, /kept nothing of this dimension, so no terrain is drawn left of the divider, and no beds, containers or named mobs on either side\./);
  assert.match(said, /Right of the divider is the terrain as it is now/);
  for (const still of OF_NOW) assert.match(said, still);
  await p.press('compare');
  assert.match(p.part('note').textContent, /so no terrain is drawn, and no beds, containers or named mobs\. Players/, 'out of a comparison the note is as it was');
});

test('leaving Compare puts the map back as one, on the day it was on', async () => {
  const p = await load({ pairs: true });
  await p.press('open');
  await p.slide(1);
  await p.press('compare');
  await p.press('compare');
  assert.deepEqual(p.state.right, ['now', 'off']);
  assert.equal(p.last(), '2026-10-13');
  assert.equal(p.over('divide').hidden, true);
  assert.equal(p.part('compare').getAttribute('aria-pressed'), 'false');
  assert.equal(p.doc.body.classList.contains('comparing'), false);
  assert.match(p.part('note').textContent, /The terrain is of this day/);
});

test('the day on the right is chosen from the same calendar, which offers now and not the day on the left', async () => {
  const p = await load({ pairs: true });
  await p.press('open');
  await p.slide(1);
  await p.press('compare');
  p.over('right').click();
  await p.settle();
  assert.equal(p.part('calendar').hidden, false);
  assert.equal(p.over('right').getAttribute('aria-expanded'), 'true');
  assert.equal(p.part('date').getAttribute('aria-expanded'), 'false');
  assert.equal(p.part('pick-now').hidden, false);
  assert.equal(p.part('pick-now').getAttribute('aria-pressed'), 'true');
  assert.equal(p.cell('2026-10-13').disabled, true, 'a day is not compared with itself');
  assert.match(p.cell('2026-10-13').getAttribute('aria-label'), /on the left/);
  assert.equal(p.cell('2026-10-12').disabled, false);
  p.cell('2026-10-12').click();
  await p.settle();
  assert.deepEqual(p.state.right, ['now', '2026-10-12']);
  assert.equal(p.last(), '2026-10-13', 'the day on the left is left where it is');
  assert.equal(p.part('calendar').hidden, true);
  assert.equal(p.doc.activeElement, p.over('right'));
  assert.match(p.over('right').textContent, /12/);
  assert.match(p.part('note').textContent, /Right of the divider is the terrain of .*12/);
  assert.equal(p.part('range').style.getPropertyValue('--timeline-other'), '0');
  p.over('right').click();
  await p.settle();
  assert.equal(p.cell('2026-10-12').getAttribute('aria-pressed'), 'true');
  assert.equal(p.part('pick-now').getAttribute('aria-pressed'), 'false');
  await p.press('pick-now');
  assert.deepEqual(p.state.right, ['now', '2026-10-12', 'now']);
  assert.equal(p.over('right').textContent, 'Now');
  assert.equal(p.part('calendar').hidden, true);
  // Opened from the day, it is the day's calendar again.
  await p.press('date');
  assert.equal(p.part('pick-now').hidden, true);
  assert.equal(p.cell('2026-10-13').getAttribute('aria-pressed'), 'true');
  p.cell('2026-10-14').dispatchEvent({ type: 'keydown', key: 'Escape' });
  assert.equal(p.doc.activeElement, p.part('date'));
});

test('a day stepped to that is the day on the right is compared with now, and Escape gives the focus back to the right', async () => {
  const p = await load({ pairs: true });
  await p.press('open');
  await p.slide(2);
  await p.press('compare');
  p.over('right').click();
  await p.settle();
  p.cell('2026-10-13').dispatchEvent({ type: 'keydown', key: 'Escape' });
  assert.equal(p.part('calendar').hidden, true);
  assert.equal(p.doc.activeElement, p.over('right'));
  p.over('right').click();
  await p.settle();
  p.cell('2026-10-13').click();
  await p.settle();
  await p.press('earlier');
  assert.equal(p.last(), '2026-10-13');
  assert.equal(p.state.right.at(-1), 'now');
  assert.equal(p.over('right').textContent, 'Now');
});

test('now, and closing the dock, each end a comparison', async () => {
  const p = await load({ pairs: true });
  await p.press('open');
  await p.press('compare');
  await p.press('now');
  assert.equal(p.last(), '');
  assert.equal(p.state.comparing, false);
  assert.equal(p.over('divide').hidden, true);
  assert.equal(p.part('compare').getAttribute('aria-pressed'), 'false');
  await p.press('compare');
  assert.equal(p.state.comparing, true);
  await p.press('close');
  assert.equal(p.state.comparing, false);
  assert.equal(p.last(), '');
  assert.equal(p.over('divide').hidden, true);
  assert.equal(p.doc.body.classList.contains('comparing'), false);
});

test('the divider is moved by the arrow keys, to the edges by Home and End, and by a drag across the map', async () => {
  const p = await load({ pairs: true });
  await p.press('open');
  await p.press('compare');
  const divider = p.over('divider');
  const press = (key) => divider.dispatchEvent({ type: 'keydown', key });
  press('ArrowLeft');
  assert.equal(p.state.cut.toFixed(2), '0.48');
  assert.equal(divider.getAttribute('aria-valuenow'), '48');
  assert.match(divider.getAttribute('aria-valuetext'), /48%/);
  assert.equal(p.over('divide').style.getPropertyValue('--timeline-cut'), '48%');
  press('ArrowRight');
  press('ArrowUp');
  assert.equal(p.state.cut.toFixed(2), '0.52');
  press('Home');
  assert.equal(p.state.cut, 0);
  press('ArrowLeft');
  assert.equal(p.state.cut, 0, 'it is held to the map');
  press('End');
  assert.equal(p.state.cut, 1);
  assert.equal(divider.getAttribute('aria-valuenow'), '100');
  p.doc.getElementById('map').getBoundingClientRect = () => ({ left: 40, top: 0, width: 800, height: 600 });
  divider.dispatchEvent({ type: 'pointerdown', pointerId: 4, button: 0, clientX: 240 });
  assert.equal(p.state.cut, 0.25);
  assert.equal(p.doc.activeElement, divider);
  divider.dispatchEvent({ type: 'pointermove', pointerId: 4, clientX: 640 });
  assert.equal(p.state.cut, 0.75);
  divider.dispatchEvent({ type: 'pointermove', pointerId: 9, clientX: 40 });
  assert.equal(p.state.cut, 0.75, 'another pointer is not the one dragging');
  divider.dispatchEvent({ type: 'pointerup', pointerId: 4 });
  divider.dispatchEvent({ type: 'pointermove', pointerId: 4, clientX: 40 });
  assert.equal(p.state.cut, 0.75);
  assert.equal(p.state.shown.length, 1, 'moving the divider moves no day');
});

test('a play while days are compared rests on the newest day, and starts over from it', async () => {
  const p = await load({ pairs: true });
  await p.press('open');
  await p.slide(1);
  await p.press('compare');
  await p.press('play');
  await p.beat();
  assert.equal(p.last(), '2026-10-14');
  assert.equal(p.part('play').textContent, 'Play');
  assert.equal(p.state.comparing, true, 'a play does not run on to now and out of the comparison');
  await p.press('play');
  assert.equal(p.last(), '2026-10-12');
  assert.equal(p.part('play').textContent, 'Pause');
});

test('an address that names a day and what is beside it opens on the comparison, and one that drops it leaves it', async () => {
  const p = await load({ pairs: true, wanted: '2026-10-13', beside: 'now' });
  assert.deepEqual(p.state.shown, ['2026-10-13']);
  assert.deepEqual(p.state.right, ['now']);
  assert.equal(p.part('body').hidden, false);
  assert.equal(p.over('divide').hidden, false);
  await p.view();
  assert.deepEqual(p.state.right, ['now'], 'a comparison once followed is not started again');
  await p.named('2026-10-13', '2026-10-12');
  assert.deepEqual(p.state.right, ['now', '2026-10-12']);
  assert.match(p.over('right').textContent, /12/);
  // A day on the right that is not kept, or is the day on the left, is now.
  await p.named('2026-10-13', '2025-01-01');
  assert.equal(p.state.right.at(-1), 'now');
  await p.named('2026-10-14', '2026-10-14');
  assert.equal(p.last(), '2026-10-14');
  assert.equal(p.state.right.at(-1), 'now');
  await p.named('2026-10-14');
  assert.equal(p.state.right.at(-1), 'off');
  assert.equal(p.over('divide').hidden, true);
  assert.equal(p.last(), '2026-10-14');
  await p.named('2026-10-12', 'now');
  await p.named('');
  assert.equal(p.last(), '');
  assert.equal(p.state.comparing, false);
});

test('the speed and the loop are kept with the page\'s settings, and are how the next page opens', async () => {
  const kept = new Map();
  const p = await load({ kept });
  await p.press('open');
  p.parts('times')[2].click();
  await p.press('loop');
  assert.equal(JSON.stringify(p.settings.get('history')), '{"speed":4,"loop":true}');
  const next = await load({ kept });
  await next.press('open');
  assert.deepEqual(next.parts('times').map((node) => node.getAttribute('aria-checked')), ['false', 'false', 'true']);
  assert.equal(next.part('loop').getAttribute('aria-pressed'), 'true');
  await next.press('play');
  assert.deepEqual(next.state.paces, [375]);
  // Changed from elsewhere, as by a file of settings taken in.
  next.settings.set('history', { speed: 2, loop: false });
  await next.settle();
  assert.equal(next.part('loop').getAttribute('aria-pressed'), 'false');
  assert.deepEqual(next.state.paces, [375, 750]);
  next.settings.set('history', { speed: 3, loop: 'yes' });
  assert.equal(JSON.stringify(next.settings.get('history')), '{"speed":1,"loop":false}', 'what is not a speed or a switch is not kept');
});

test('a name from the server that is not a day is not offered', async () => {
  const p = await load({ answer: () => ({ days: [day('2026-10-12'), day('../../etc'), { day: '2026-10-13' }, day('2026-10-14')] }) });
  await p.press('open');
  assert.equal(p.max(), '2');
});

test('a server that cannot be reached is asked again later, and not on every move of the map', async () => {
  let answer = () => ({ fails: true });
  const p = await load({ answer: () => answer() });
  assert.equal(p.bar.hidden, true);
  await p.view();
  assert.equal(p.state.asked.length, 1);
  answer = () => ({ days: THREE });
  p.clock(2 * 60_000);
  await p.view();
  assert.equal(p.state.asked.length, 2);
  assert.equal(p.bar.hidden, false);
});

test('a page whose address names a kept day opens on that day, with the control open to say so', async () => {
  const p = await load({ wanted: '2026-10-13' });
  assert.deepEqual(p.state.shown, ['2026-10-13']);
  assert.equal(p.part('body').hidden, false);
  assert.equal(p.part('open').getAttribute('aria-expanded'), 'true');
  assert.equal(p.value(), '1');
  assert.match(p.part('day').textContent, /13/);
  assert.equal(p.doc.body.classList.contains('past'), true);
  await p.view();
  assert.deepEqual(p.state.shown, ['2026-10-13'], 'a day once followed is not set again');
});

test('a page whose address names a day that is not kept opens on now, and the map is told so', async () => {
  const p = await load({ wanted: '2025-01-01' });
  assert.deepEqual(p.state.shown, ['']);
  assert.equal(p.state.wanted, '');
  assert.equal(p.part('body').hidden, true);
  assert.equal(p.part('day').textContent, 'Now');
  assert.equal(p.doc.body.classList.contains('past'), false);
});

test('a day an address names is neither shown nor given up before the days kept are known', async () => {
  let answer = () => ({ fails: true });
  const p = await load({ answer: () => answer(), wanted: '2026-10-13' });
  await p.named('2026-10-12');
  assert.equal(p.state.shown.length, 0);
  assert.equal(p.state.wanted, '2026-10-12');
  answer = () => ({ days: THREE });
  p.clock(2 * 60_000);
  await p.view();
  assert.deepEqual(p.state.shown, ['2026-10-12']);
});

test('a server that keeps no history has no day an address could name', async () => {
  const p = await load({ answer: () => ({ status: 404 }), wanted: '2026-10-13' });
  assert.deepEqual(p.state.shown, ['']);
  assert.equal(p.bar.hidden, true);
});

test('an address that changes to a day, to a day not kept and to none moves the control with it', async () => {
  const p = await load();
  await p.named('2026-10-12');
  assert.equal(p.last(), '2026-10-12');
  assert.equal(p.part('body').hidden, false);
  assert.equal(p.value(), '0');
  await p.named('2026-10-14');
  assert.equal(p.last(), '2026-10-14');
  await p.named('2025-01-01');
  assert.equal(p.last(), '');
  assert.equal(p.part('day').textContent, 'Now');
  await p.named('2026-10-13');
  await p.named('');
  assert.equal(p.last(), '');
  assert.equal(p.value(), '3');
  const shown = p.state.shown.length;
  await p.named('');
  assert.equal(p.state.shown.length, shown, 'an address that names what is shown changes nothing');
});

test('an address that changes while a play runs stops the play on the day it names', async () => {
  const p = await load();
  await p.press('open');
  await p.press('play');
  await p.named('2026-10-14');
  assert.equal(p.part('play').textContent, 'Play');
  await p.beat();
  assert.equal(p.last(), '2026-10-14');
});

test('a day from an address is written as text, and one that is not a date is never on the page', async () => {
  const p = await load({ wanted: '<img src=x>' });
  assert.deepEqual(p.state.shown, ['']);
  assert.equal(p.bar.querySelectorAll('img').length, 0);
  assert.doesNotMatch(p.bar.textContent, /img/);
});

test('a map from before it could say which day its address names is left on now', async () => {
  const p = await load();
  delete p.win.mcmap.past.wanted;
  await p.named('2026-10-12');
  assert.equal(p.state.shown.length, 0);
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
