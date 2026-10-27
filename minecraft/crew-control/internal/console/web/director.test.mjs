import test from 'node:test';
import assert from 'node:assert/strict';
import { scoreUnits, choose, createDirector, press, pause, tick, note, DWELL_MS, WINDOW_MS } from './director.js';

const now = Date.parse('2026-10-26T12:00:00Z');
const iso = ago => new Date(now - ago).toISOString();
const ev = (unit, kind, ago, seq = 1) => ({ seq, unit, kind, at: iso(ago) });
const unit = (id, o = {}) => [id, { id, online: true, dead: false, health: 20, hunger: 20, ...o }];
const fleet = (...u) => new Map(u);

test('a death outranks stuck, and newer outranks older', () => {
  const st = fleet(unit('a'), unit('b'), unit('c'));
  const s = scoreUnits([ev('a', 'stuck', 1000), ev('b', 'died', 1000), ev('c', 'died', 30000)], st, now);
  assert.ok(s.get('b').score > s.get('a').score);
  assert.ok(s.get('b').score > s.get('c').score);
});

test('old, unknown and closed-vocabulary-violating events do not count', () => {
  const st = fleet(unit('a'));
  const s = scoreUnits([ev('a', 'died', WINDOW_MS + 1), ev('a', 'chat', 100), ev('ghost', 'died', 100), ev('a', 'died', -5000)], st, now);
  assert.equal(s.size, 0);
});

test('trouble that ended no longer counts', () => {
  const st = fleet(unit('a'), unit('b'));
  const s = scoreUnits([ev('a', 'died', 5000), ev('a', 'respawned', 2000), ev('b', 'stuck', 5000), ev('b', 'task_done', 1000)], st, now);
  assert.equal(s.size, 0);
});

test('low health only counts while the unit is still hurt', () => {
  assert.equal(scoreUnits([ev('a', 'low_health', 1000)], fleet(unit('a', { health: 20 })), now).size, 0);
  assert.equal(scoreUnits([ev('a', 'low_health', 1000)], fleet(unit('a', { health: 3 })), now).size, 1);
  assert.equal(scoreUnits([ev('a', 'low_health', 1000)], fleet(unit('a', { health: 3, dead: true })), now).size, 0);
});

test('the current shot is held unless something clearly worse happens', () => {
  const st = fleet(unit('a'), unit('b'));
  const stuckA = scoreUnits([ev('a', 'stuck', 1000)], st, now);
  const both = scoreUnits([ev('a', 'stuck', 1000), ev('b', 'task_failed', 500)], st, now);
  assert.equal(choose(both, 'a', 3000), null);
  assert.equal(choose(stuckA, 'b', 3000).unit, 'a');
});

test('a death cuts the dwell short, a calmer event waits it out', () => {
  const st = fleet(unit('a'), unit('b'));
  const s = scoreUnits([ev('a', 'stuck', 1000), ev('b', 'died', 500)], st, now);
  assert.equal(choose(s, 'a', 1000).unit, 'b');
  const calm = scoreUnits([ev('a', 'low_hunger', 1000), ev('b', 'stuck', 500)], fleet(unit('a', { hunger: 2 }), unit('b')), now);
  assert.equal(choose(calm, 'a', 1000).unit, 'b');
  assert.equal(choose(calm, 'a', DWELL_MS).unit, 'b');
});

test('with nothing going on the shot stays put', () => {
  assert.equal(choose(new Map(), 'a', DWELL_MS * 9), null);
});

test('shift-D cycles off, following, off; paused resumes at once', () => {
  const d = createDirector();
  press(d, now);
  assert.deepEqual([d.on, d.paused], [true, false]);
  pause(d);
  assert.equal(d.paused, true);
  press(d, now);
  assert.deepEqual([d.on, d.paused], [true, false]);
  press(d, now);
  assert.equal(d.on, false);
});

test('a paused or disabled director never moves the camera', () => {
  const st = fleet(unit('a'), unit('b'));
  const input = { feed: [ev('b', 'died', 100)], statuses: st, now, current: 'a', order: ['a', 'b'] };
  const d = createDirector();
  assert.equal(tick(d, input), '');
  press(d, now);
  pause(d);
  assert.equal(tick(d, input), '');
  press(d, now);
  assert.equal(tick(d, input), 'b');
  assert.match(note(d, 'Basalt', now), /Basalt died 0s ago/);
});

test('notes are plain words from the closed vocabulary', () => {
  const d = createDirector();
  assert.equal(note(d, '', now), '');
  press(d, now);
  assert.match(note(d, '', now), /waiting/);
  pause(d);
  assert.match(note(d, '', now), /paused/);
});
