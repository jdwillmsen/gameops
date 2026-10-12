'use strict';

// Which point a zoom is about, and how far a wheel turn goes: the zoom's own
// arithmetic, and the live layer's choice between the pointer and whoever
// is followed, each run and not read. The zoom is moved a step at a time,
// as a pinch is, and the live layer puts whoever is followed in the middle
// of every step of those. Run by `go test ./web/` where there is a node, or
// by hand: `node web/testdata/zoomfollow.test.cjs`.
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const { stage } = require('./stage.cjs');

const tests = [];
const test = (name, fn) => tests.push([name, fn]);
const near = (got, want, what, by = 1e-6) => assert.ok(Math.abs(got - want) <= by, `${what}: ${got}, not ${want}`);

// The zoom script on a map of plain objects: a block is 2^zoom pixels, the
// box is 1000 by 600, and frames are run by hand.
function zoomed() {
  const listeners = new Map();
  const frames = [];
  const dummy = () => new Proxy(function () {}, { get: (t, k) => (k === 'then' ? undefined : k === Symbol.toPrimitive ? () => '' : dummy()), set: () => true, apply: () => dummy() });
  const point = (x, y) => ({ x, y, distanceTo: (o) => Math.hypot(x - o.x, y - o.y), divideBy: (n) => point(x / n, y / n) });
  const surface = { addEventListener: (type, fn) => listeners.set(type, fn), getBoundingClientRect: () => ({ left: 0, top: 0 }) };
  const map = {
    _loaded: true, _animatingZoom: false, zoom: 0, centre: { lat: 0, lng: 0 }, moves: [], ended: 0,
    options: {},
    on() { return this; },
    getZoom() { return this.zoom; },
    getCenter() { return this.centre; },
    getMinZoom: () => -4,
    getMaxZoom: () => 7,
    getSize: () => point(1000, 600),
    getContainer: () => surface,
    containerPointToLatLng(p) { return { lat: this.centre.lat + (p.y - 300) / 2 ** this.zoom, lng: this.centre.lng + (p.x - 500) / 2 ** this.zoom }; },
    _stop() {}, _moveStart() {}, _moveEnd() { this.ended += 1; },
    _move(centre, zoom) { this.centre = centre; this.zoom = zoom; this.moves.push({ centre, zoom }); },
  };
  const win = {
    document: { getElementById: () => dummy(), addEventListener() {} },
    addEventListener() {},
    mcmap: { map, extent: () => null },
    mcmapSettings: null,
    L: { point, latLng: (c) => ({ lat: c.lat, lng: c.lng }) },
    requestAnimationFrame: (fn) => frames.push(fn),
    cancelAnimationFrame: () => {},
    performance: { now: () => 0 },
  };
  win.window = win;
  vm.createContext(win);
  vm.runInContext(fs.readFileSync(path.join(__dirname, '..', 'zoom.js'), 'utf8'), win, { filename: 'zoom.js' });
  let now = 0;
  // Runs frames at 60 Hz until none is waiting.
  const run = () => {
    for (let n = 0; n < 600 && frames.length > 0; n += 1) {
      now += 16.67;
      win.performance.now = () => now;
      frames.splice(0).forEach((fn) => fn(now));
    }
  };
  const wheel = (deltaY, x, y, extra = {}) => {
    const event = { deltaY, deltaMode: 0, ctrlKey: false, clientX: x, clientY: y, preventDefault() { this.prevented = true; }, ...extra };
    listeners.get('wheel')(event);
    return event;
  };
  win.performance.now = () => now;
  return { map, win, wheel, run, listeners, point, tick: (ms) => { now += ms; win.performance.now = () => now; } };
}

// The block under a pixel of the box, at the map's zoom now.
const under = (map, x, y) => map.containerPointToLatLng({ x, y });

test('a turn of the wheel is half a level, a line a third of that and a page more', () => {
  const { wheelLevels } = zoomed().win.mcmapZoom;
  near(wheelLevels(-100, 0, false), 0.5, 'a turn in');
  near(wheelLevels(100, 0, false), -0.5, 'a turn out');
  near(wheelLevels(-3, 1, false), 0.495, 'three lines, as Firefox sends a turn');
  assert.ok(wheelLevels(-1, 2, false) > 0.5, 'a page');
  near(wheelLevels(-5000, 0, false), 0.75, 'a lurch of the hardware is a turn and a half');
  near(wheelLevels(-10, 0, true), 0.15, 'a pinch is finer');
  assert.equal(wheelLevels(0, 0, false), 0);
});

test('a wheel turn zooms about the pointer, and the block under it stays under it', () => {
  const z = zoomed();
  const where = [820, 90];
  const block = under(z.map, ...where);
  const event = z.wheel(-100, ...where);
  assert.ok(event.prevented, 'the page does not scroll');
  z.run();
  near(z.map.zoom, 0.5, 'zoom');
  const after = under(z.map, ...where);
  near(after.lat, block.lat, 'down', 1e-6);
  near(after.lng, block.lng, 'across', 1e-6);
  assert.equal(z.map.ended, 1, 'the zoom ends once');
  // Every step on the way kept it there too.
  for (const move of z.map.moves) {
    const k = 2 ** move.zoom;
    near(block.lng - move.centre.lng, (where[0] - 500) / k, 'across on the way', 1e-6);
  }
});

test('a trackpad adds up its small steps, whichever way they come', () => {
  const z = zoomed();
  for (let n = 0; n < 20; n += 1) {
    z.wheel(-4, 300, 200);
    z.tick(16);
  }
  z.run();
  near(z.map.zoom, 0.4, 'twenty steps of four pixels');
  const out = zoomed();
  out.wheel(-1, 300, 200);
  out.run();
  near(out.map.zoom, 0.005, 'one step of one pixel is a hair, not a level');
});

test('a pinch with Ctrl held, and one Safari sends, are as fine as they are made', () => {
  const z = zoomed();
  for (let n = 0; n < 10; n += 1) z.wheel(-8, 500, 300, { ctrlKey: true });
  z.run();
  near(z.map.zoom, 1.2, 'ten pinch steps');
  const s = zoomed();
  s.listeners.get('gesturestart')({ clientX: 700, clientY: 100, preventDefault() {} });
  const block = under(s.map, 700, 100);
  for (const scale of [1.1, 1.5, 2.2]) s.listeners.get('gesturechange')({ scale, preventDefault() {} });
  s.listeners.get('gestureend')({ preventDefault() {} });
  s.run();
  near(s.map.zoom, Math.log2(2.2), 'a scale of 2.2 is that many levels');
  near(under(s.map, 700, 100).lng, block.lng, 'about the fingers', 1e-6);
});

test('a zoom never goes past the ends and ends where it was sent', () => {
  const z = zoomed();
  for (let n = 0; n < 40; n += 1) z.wheel(-100, 500, 300);
  z.run();
  assert.equal(z.map.zoom, 7);
  for (let n = 0; n < 80; n += 1) z.wheel(100, 500, 300);
  z.run();
  assert.equal(z.map.zoom, -4);
});

test('steps go to the next whole level, from between two as from on one', () => {
  const { stepped } = zoomed().win.mcmapZoom;
  assert.equal(stepped(2, 1), 3);
  assert.equal(stepped(2, -1), 1);
  assert.equal(stepped(2.37, 1), 3);
  assert.equal(stepped(2.37, -1), 2);
  assert.equal(stepped(2, 1, 3), 5);
  assert.equal(stepped(2.37, -1, 3), 0);
});

test('the catch-up covers most of the way in a few frames and all of it for reduced motion', () => {
  const { eased } = zoomed().win.mcmapZoom;
  let zoom = 0;
  for (let n = 0; n < 40; n += 1) zoom = eased(zoom, 1, 16.67, 55, false).zoom;
  assert.equal(zoom, 1, 'arrives');
  assert.ok(eased(0, 1, 16.67, 55, false).zoom > 0.2 && eased(0, 1, 16.67, 55, false).zoom < 0.4, 'a frame is a part of it');
  assert.equal(eased(0, 3, 16.67, 55, true).zoom, 3);
});

// --- the live layer's choice of what a zoom is about -----------------------------------

const steve = (x, z) => ({ i: '1', n: 'Steve', x, y: 64, z, r: 0 });

async function following() {
  const s = await stage();
  s.send = (world) => s.sample(world);
  await s.send({ players: [steve(10, 20)], mobs: [] });
  assert.ok(s.app.inspect.open({ kind: 'player', id: '1', type: 'cow', name: 'Steve', x: 0, y: 64, z: 0 }));
  s.doc.getElementById('inspect-follow').click();
  return s;
}

test('with Follow on every step of a zoom is about whoever is followed, wherever the pointer was', async () => {
  const s = await following();
  s.boxes['#inspect'] = { left: 8, top: 440, right: 992, bottom: 592 };
  await s.send({ players: [steve(33, -12)], mobs: [] });
  for (const aboutPointer of [{ lat: 300, lng: 900 }, { lat: -50, lng: -70 }]) {
    for (const zoom of [0.3, 1.7, 5.25, 7]) {
      s.map._move(aboutPointer, zoom, { pinch: true, round: false });
      const at = s.map.onScreen([-12, 33]);
      near(at.x, 500, `across at zoom ${zoom}`, 0.01);
      near(at.y, 220, `down at zoom ${zoom}`, 0.01);
    }
  }
});

test('without Follow a step of a zoom keeps the centre it was given', async () => {
  const s = await following();
  s.doc.getElementById('inspect-follow').click();
  assert.equal(s.app.inspect.following(), null);
  s.map._move({ lat: 300, lng: 900 }, 2.5, { pinch: true, round: false });
  assert.deepEqual({ ...s.map.centre }, { lat: 300, lng: 900 });
});

async function main() {
  let failed = 0;
  for (const [name, fn] of tests) {
    try {
      await fn();
      console.log(`ok   ${name}`);
    } catch (err) {
      failed += 1;
      console.log(`FAIL ${name}\n     ${String(err && err.stack ? err.stack : err).split('\n').slice(0, 6).join('\n     ')}`);
    }
  }
  console.log(`${tests.length - failed} of ${tests.length} passed`);
  process.exitCode = failed === 0 ? 0 : 1;
}

main();
