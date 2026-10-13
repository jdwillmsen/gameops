'use strict';

// What the map does when it is told a day, checked by doing it: app.js is
// run in a page of the kind dom.cjs makes, on a map that keeps Leaflet's
// own rules for the two things a day must not disturb. A view outside the
// bounds it is given is brought inside them and a zoom below its least is
// raised to it, as Leaflet does, and a tile layer is loading only while
// the view shows some of its bounds, which must have some size, at a zoom
// it draws, and says 'load' only after that, as Leaflet does. Run by
// `go test ./web/` where there is a node to run it with, or by hand:
// `node web/testdata/past.test.cjs`.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { page } = require('./dom.cjs');
const { leaflet } = require('./leaflet.cjs');

const tick = () => new Promise((done) => setTimeout(done, 0));
const tests = [];
const test = (name, fn) => tests.push([name, fn]);

// The overworld as it is, four regions each way about the origin, and as
// it was on a day when it was one region and was cut another way.
const TODAY = { id: 'overworld', rendered: true, renderedAt: '2026-10-14T09:00:00Z', format: 'webp', minZoom: -5, maxZoom: 0, minRegionX: -4, maxRegionX: 3, minRegionZ: -4, maxRegionZ: 3 };
const THEN = { day: '2026-09-01', dimensions: [{ id: 'overworld', format: 'png', minZoom: -2, maxZoom: 0, minRegionX: 0, maxRegionX: 0, minRegionZ: 0, maxRegionZ: 0 }] };
const LATER = { day: '2026-09-02', dimensions: [{ ...THEN.dimensions[0], maxRegionX: 1 }] };
const NETHER = { ...TODAY, id: 'nether' };

// A page with app.js run on it, opened at the place its address names.
// Given the days a server keeps, the history's script is run on it as
// well, against a server that lists them.
async function load(hash, look, kept) {
  const p = page(new Map(look ? [['mcmap.settings', JSON.stringify({ v: 1, look })]] : []), { files: look ? ['settings.js'] : [] });
  const { win, doc } = p;
  const found = doc.getElementById;
  const asked = new Map();
  doc.getElementById = (id) => {
    if (found(id)) return found(id);
    if (!asked.has(id)) asked.set(id, doc.createElement('div'));
    return asked.get(id);
  };
  // The page is sent with its login put away.
  doc.getElementById('login').hidden = true;
  doc.hidden = false;
  const { L, map } = leaflet();
  const state = { today: TODAY, heard: [] };
  const turns = [];
  Object.assign(win, {
    L,
    location: { hash, get href() { return `https://map.test/${this.hash}`; } },
    addEventListener: (type, fn) => { if (type === 'hashchange') turns.push(fn); },
    history: { replaceState(_, __, to) { win.location.hash = to; } },
    navigator: {},
    setInterval: () => 0,
    clearInterval() {},
    fetch: async (address) => {
      const body = address === 'api/map' ? { world: 'w', refreshSeconds: 600, dimensions: [state.today, NETHER] } : address === 'api/history' ? { days: kept } : {};
      return { ok: true, status: 200, headers: { get: () => '' }, json: async () => body, text: async () => JSON.stringify(body) };
    },
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, '..', 'app.js'), 'utf8'), win, { filename: 'app.js' });
  doc.addEventListener('mcmap:day', () => state.heard.push(win.mcmap.past.wanted()));
  if (kept) vm.runInContext(fs.readFileSync(path.join(__dirname, '..', 'history.js'), 'utf8'), win, { filename: 'history.js' });
  const settle = async () => { for (let i = 0; i < 6; i++) await tick(); };
  await settle();
  const terrain = () => [...map.layers].filter((l) => l.options.dimension);
  // The address changed under the page, as by a link followed to it.
  const turn = async (to) => {
    win.location.hash = to;
    for (const fn of turns) fn({ type: 'hashchange' });
    await settle();
  };
  const address = () => win.location.hash;
  const part = (name) => doc.body.querySelector(`.timeline-${name}`);
  const press = async (name) => { part(name).click(); await settle(); };
  // Where the timeline's scrubber stands among the days kept.
  const value = () => part('range').getAttribute('aria-valuenow');
  return { ...p, app: win.mcmap, map, state, settle, terrain, turn, address, part, press, value };
}

// Long enough for tiles to have faded in: the page's waits are cut to a
// moment by dom.cjs.
const faded = () => new Promise((done) => setTimeout(done, 30));
const blocks = (b) => [b.south, b.west, b.north, b.east];

test('a day is drawn from its own tiles, over its own extent and zooms', async () => {
  const p = await load('#overworld/100/200/-1');
  p.terrain()[0].arrive();
  p.app.past.set(THEN);
  const layer = p.terrain().at(-1);
  assert.equal(layer.getTileUrl({ z: -2, x: 0, y: 0 }), 'api/history/tiles/2026-09-01/overworld/-2/0/0.png');
  assert.deepEqual(blocks(layer.options.bounds), [0, 0, 512, 512]);
  assert.equal(layer.options.minNativeZoom, -2);
  assert.equal(p.app.past.get(), '2026-09-01');
});

test('choosing a day leaves the view where it was, outside what that day held and zoomed out past it', async () => {
  const p = await load('#overworld/-1500/1200/-5');
  p.app.past.set(THEN);
  assert.deepEqual(p.map.getCenter(), { lat: 1200, lng: -1500 });
  assert.equal(p.map.getZoom(), -5);
  assert.ok(p.terrain().at(-1).options.minZoom <= p.map.least, 'the day is drawn, stretched, as far out as the map goes');
  p.app.past.set(null);
  assert.deepEqual(p.map.getCenter(), { lat: 1200, lng: -1500 });
  assert.equal(p.map.getZoom(), -5);
  assert.equal(p.terrain().at(-1).getTileUrl({ z: -5, x: 0, y: 0 }), 'tiles/overworld/-5/0/0.webp');
});

test('what is laid over a day is laid over the world as it is', async () => {
  const p = await load('#overworld/100/200/-1');
  p.app.past.set(THEN);
  assert.deepEqual(blocks(p.app.extent()), [-2048, -2048, 2048, 2048]);
});

test('the map says which of the day drawn\'s dimensions kept their markers, and none of them while it draws now', async () => {
  const p = await load('#overworld/100/200/-1');
  assert.equal(p.app.past.markers('overworld'), false);
  p.app.past.set({ day: '2026-09-03', dimensions: [{ ...THEN.dimensions[0], markers: true }, { ...THEN.dimensions[0], id: 'nether' }, null, { ...THEN.dimensions[0], id: 'end', markers: 'yes' }] });
  assert.equal(p.app.past.markers('overworld'), true);
  assert.equal(p.app.past.markers('nether'), false, 'a dimension listed without markers kept none');
  assert.equal(p.app.past.markers('end'), false, 'only true is a yes');
  assert.equal(p.app.past.markers('moon'), false, 'a dimension the day did not hold kept none');
  p.app.past.set(THEN);
  assert.equal(p.app.past.markers('overworld'), false, 'a day from before markers were kept');
  p.app.past.set({ day: '2026-09-03', dimensions: [{ ...THEN.dimensions[0], markers: true }] });
  p.app.past.set(null);
  assert.equal(p.app.past.markers('overworld'), false);
});

test('a day that did not hold the place shown is nothing at once, not today under the day\'s name', async () => {
  const p = await load('#overworld/-1500/1200/-1');
  const [today] = p.terrain();
  today.arrive();
  p.app.past.set(THEN);
  assert.equal(p.map.hasLayer(today), false);
  assert.equal(p.terrain().length, 1);
});

test('a day that held the place shown is drawn over what was there, which goes when its tiles have come', async () => {
  const p = await load('#overworld/100/200/-1');
  const [today] = p.terrain();
  today.arrive();
  p.app.past.set(THEN);
  assert.equal(p.map.hasLayer(today), true);
  p.terrain().at(-1).arrive();
  assert.equal(p.map.hasLayer(today), true, 'tiles that have just come are still fading in, with nothing under them but what was there');
  await faded();
  assert.equal(p.map.hasLayer(today), false);
});

test('for a viewer who asked for less motion nothing fades in, and what was there goes as the tiles come', async () => {
  const p = await load('#overworld/100/200/-1', { motion: 'reduce' });
  const [today] = p.terrain();
  today.arrive();
  p.app.past.set(THEN);
  assert.equal(p.map.hasLayer(today), true);
  p.terrain().at(-1).arrive();
  assert.equal(p.map.hasLayer(today), false);
});

test('what was there does not wait for ever on tiles that never come', async () => {
  const p = await load('#overworld/100/200/-1');
  const [today] = p.terrain();
  p.app.past.set(THEN);
  assert.equal(p.map.hasLayer(today), true);
  // The page's waits are cut to a moment by dom.cjs.
  await new Promise((done) => setTimeout(done, 30));
  assert.equal(p.map.hasLayer(today), false);
  assert.equal(p.terrain().length, 1);
});

test('a second day chosen before the first has come takes the first away, and leaves what was under it until the second has come', async () => {
  const p = await load('#overworld/100/200/-1');
  const [today] = p.terrain();
  today.arrive();
  p.app.past.set(THEN);
  const first = p.terrain().at(-1);
  p.app.past.set(LATER);
  const second = p.terrain().at(-1);
  assert.equal(second.options.day, '2026-09-02');
  assert.equal(p.map.hasLayer(first), false, 'a day passed over must not go on asking for its tiles');
  assert.equal(p.map.hasLayer(today), true);
  second.arrive();
  await faded();
  assert.deepEqual(p.terrain(), [second]);
});

test('a day whose tiles come late, under a day chosen after it, stays until that day has come', async () => {
  const p = await load('#overworld/100/200/-1');
  const [today] = p.terrain();
  today.arrive();
  p.app.past.set(THEN);
  const first = p.terrain().at(-1);
  // The page's waits are cut to a moment by dom.cjs.
  await new Promise((done) => setTimeout(done, 30));
  assert.deepEqual(p.terrain(), [first]);
  p.app.past.set(LATER);
  const second = p.terrain().at(-1);
  first.arrive();
  assert.equal(p.map.hasLayer(first), true, 'the picture under the day still coming must not go as it becomes whole');
  second.arrive();
  await faded();
  assert.deepEqual(p.terrain(), [second]);
});

test('a loaded day remains under the next day during its fade wait', async () => {
  const p = await load('#overworld/100/200/-1');
  const [today] = p.terrain();
  today.arrive();
  p.app.past.set(THEN);
  const first = p.terrain().at(-1);
  first.arrive();
  p.app.past.set(LATER);
  const second = p.terrain().at(-1);
  assert.equal(p.map.hasLayer(today), false, 'the earlier under-layer is replaced by the complete first day');
  assert.equal(p.map.hasLayer(first), true, 'the complete first day stays under the second while it loads');
  second.arrive();
  await faded();
  assert.deepEqual(p.terrain(), [second]);
});

test('another dimension shown while a day is still coming is shown alone', async () => {
  const p = await load('#overworld/100/200/-1');
  p.terrain()[0].arrive();
  p.app.past.set(THEN);
  assert.equal(p.app.go('nether', 10, 20), true);
  assert.deepEqual(p.terrain().map((l) => l.options.dimension), ['nether']);
  const [nether] = p.terrain();
  await new Promise((done) => setTimeout(done, 30));
  assert.deepEqual(p.terrain(), [nether]);
});

test('a day that did not hold the dimension shown asks for none of its tiles, and what was there goes at once', async () => {
  const p = await load('#nether/0/0/-1');
  const [today] = p.terrain();
  today.arrive();
  p.app.past.set(THEN);
  const [layer] = p.terrain();
  assert.notEqual(layer, today);
  assert.equal(p.terrain().length, 1, 'today must not stay under a day that has nothing to draw over it');
  assert.equal(layer.isLoading(), false);
  const b = layer.options.bounds;
  assert.ok(b.north === b.south && b.east === b.west, 'the layer has nowhere a tile could be');
  assert.equal(layer.options.format, 'webp');
  assert.equal(layer.options.minNativeZoom, -5);
  assert.deepEqual(blocks(p.app.extent()), [-2048, -2048, 2048, 2048]);
  assert.deepEqual(p.map.getCenter(), { lat: 0, lng: 0 });
});

test('a dimension the day did not hold, come to from one it did, asks for none of its tiles either', async () => {
  const p = await load('#overworld/100/200/-1');
  p.terrain()[0].arrive();
  p.app.past.set(THEN);
  assert.equal(p.app.go('nether', 100, 200), true);
  const [nether] = p.terrain();
  assert.equal(p.terrain().length, 1);
  assert.equal(nether.isLoading(), false);
  p.app.past.set(null);
  assert.equal(p.terrain().at(-1).isLoading(), true, 'back on now the dimension is drawn as it is');
});

const SHEET = 'structure~village~r~10~20';

test('an address without a day is written as it always was', async () => {
  const p = await load('#overworld/100/200/-1');
  assert.equal(p.address(), '#overworld/100/200/-1');
  assert.equal(p.app.past.wanted(), '');
  p.app.link.set(SHEET);
  assert.equal(p.address(), `#overworld/100/200/-1/${SHEET}`);
  p.app.link.set('');
  assert.equal(p.address(), '#overworld/100/200/-1');
  p.map.setView([64, -32], 2.5);
  p.map.fire('moveend');
  assert.equal(p.address(), '#overworld/-32/64/2.5');
  assert.equal(p.app.link.href(), 'https://map.test/#overworld/-32/64/2.5');
});

test('a day chosen is the seventh part of the address, after whatever is open, and now takes it out again', async () => {
  const p = await load('#overworld/100/200/-1');
  p.app.past.set(THEN);
  assert.equal(p.address(), '#overworld/100/200/-1///2026-09-01');
  assert.equal(p.app.link.href(), 'https://map.test/#overworld/100/200/-1///2026-09-01');
  p.app.link.set(SHEET);
  assert.equal(p.address(), `#overworld/100/200/-1/${SHEET}//2026-09-01`);
  p.app.past.set(LATER);
  assert.equal(p.address(), `#overworld/100/200/-1/${SHEET}//2026-09-02`);
  p.app.past.set(null);
  assert.equal(p.address(), `#overworld/100/200/-1/${SHEET}`);
  p.app.link.set('');
  assert.equal(p.address(), '#overworld/100/200/-1');
});

test('an address with a day reads to everything else as it did without one', async () => {
  const p = await load(`#nether/100/200/-1/${SHEET}//2026-09-01`);
  assert.deepEqual(p.terrain().map((l) => l.options.dimension), ['nether']);
  assert.deepEqual(p.map.getCenter(), { lat: 200, lng: 100 });
  assert.equal(p.map.getZoom(), -1);
  assert.equal(p.app.link.get(), SHEET);
  assert.equal(p.app.past.wanted(), '2026-09-01');
});

test('a day an address names is kept in it, and not drawn, until the map is told that day or told there is none', async () => {
  const p = await load('#overworld/100/200/-1///2026-09-01');
  assert.equal(p.app.past.wanted(), '2026-09-01');
  assert.equal(p.app.past.get(), '');
  assert.equal(p.terrain()[0].options.day, '');
  p.map.fire('moveend');
  assert.equal(p.address(), '#overworld/100/200/-1///2026-09-01');
  p.app.past.set(null);
  assert.equal(p.address(), '#overworld/100/200/-1');
  assert.equal(p.app.past.wanted(), '');
  assert.equal(p.terrain().length, 1, 'a day that was never drawn is not a change to draw');
});

test('what is not a date is no day, in an address or from whoever sets one', async () => {
  for (const not of ['2026-9-1', '20260901', '2026-09-01x', '..%2F..', '<b>', 'now']) {
    const p = await load(`#overworld/100/200/-1///${not}`);
    assert.equal(p.app.past.wanted(), '', not);
    assert.equal(p.address(), '#overworld/100/200/-1', not);
  }
  const p = await load('#overworld/100/200/-1');
  p.app.past.set({ day: '../../etc', dimensions: THEN.dimensions });
  assert.equal(p.app.past.get(), '');
  assert.equal(p.address(), '#overworld/100/200/-1');
  assert.equal(p.terrain()[0].options.day, '');
});

test('a day beside a view in an address is not read, as nothing else beside one is', async () => {
  const p = await load('#overworld/100/200/-1//v1.e30/2026-09-01');
  assert.equal(p.app.past.wanted(), '');
});

test('an address that changes says which day it now names, once, and only when that has changed', async () => {
  const p = await load('#overworld/100/200/-1');
  await p.turn('#overworld/300/400/-2///2026-09-01');
  assert.deepEqual(p.state.heard, ['2026-09-01']);
  assert.deepEqual(p.map.getCenter(), { lat: 400, lng: 300 });
  await p.turn('#overworld/500/600/-2///2026-09-01');
  assert.deepEqual(p.state.heard, ['2026-09-01']);
  await p.turn('#overworld/500/600/-2');
  assert.deepEqual(p.state.heard, ['2026-09-01', '']);
});

// Where the pane of the terrain on the right is cut, as the four corners
// the map gave it.
const cutAt = (p) => (/^polygon\((-?\d+)px /.exec(p.map.panes.beside.style.clipPath || '') || [])[1];
const sides = (p) => p.terrain().map((l) => `${l.options.pane}:${l.options.day || 'now'}`);

test('a day is compared with now by a second terrain in a pane of its own, cut at the divider, and the view stays', async () => {
  const p = await load('#overworld/100/200/-1');
  p.terrain()[0].arrive();
  assert.equal(p.app.past.compare.on(), false);
  p.app.past.set(THEN);
  p.terrain().at(-1).arrive();
  await faded();
  assert.deepEqual(sides(p), ['tilePane:2026-09-01']);
  assert.equal(p.app.past.compare.start(null), true);
  assert.deepEqual(sides(p), ['tilePane:2026-09-01', 'beside:now']);
  assert.equal(p.app.past.compare.on(), true);
  assert.equal(p.app.past.compare.get(), '');
  assert.equal(p.terrain().at(-1).getTileUrl({ z: -5, x: 0, y: 0 }), 'tiles/overworld/-5/0/0.webp');
  assert.deepEqual(blocks(p.terrain().at(-1).options.bounds), [-2048, -2048, 2048, 2048], 'the right is today, over today\'s extent');
  assert.equal(cutAt(p), '512', 'the right is seen from the middle of the map');
  assert.deepEqual(p.map.getCenter(), { lat: 200, lng: 100 });
  assert.equal(p.map.getZoom(), -1);
  assert.equal(p.app.past.get(), '2026-09-01', 'the day the markers follow is the left one');
});

test('now has nothing to be compared with, and going back to now ends a comparison', async () => {
  const p = await load('#overworld/100/200/-1');
  assert.equal(p.app.past.compare.start(null), false);
  assert.equal(p.app.past.compare.start(THEN), false);
  assert.equal(p.terrain().length, 1);
  p.app.past.set(THEN);
  p.app.past.compare.start(LATER);
  assert.deepEqual(sides(p).slice(-2), ['tilePane:2026-09-01', 'beside:2026-09-02']);
  assert.equal(p.app.past.compare.get(), '2026-09-02');
  assert.equal(p.terrain().at(-1).getTileUrl({ z: -2, x: 0, y: 0 }), 'api/history/tiles/2026-09-02/overworld/-2/0/0.png');
  p.app.past.set(null);
  assert.equal(p.app.past.compare.on(), false);
  assert.equal(p.terrain().some((l) => l.options.pane === 'beside'), false);
  assert.equal(p.map.panes.beside.style.clipPath, '');
  assert.equal(p.address(), '#overworld/100/200/-1');
});

test('the divider is moved, held to the map, and the cut follows it and the map as it is dragged', async () => {
  const p = await load('#overworld/100/200/-1');
  p.app.past.set(THEN);
  p.app.past.compare.start(null);
  assert.equal(p.app.past.compare.cut(), 0.5);
  assert.equal(p.app.past.compare.cut(0.25), 0.25);
  assert.equal(cutAt(p), '256');
  assert.equal(p.app.past.compare.cut(7), 1);
  assert.equal(p.app.past.compare.cut(-3), 0);
  assert.equal(p.app.past.compare.cut('x'), 0, 'what is not a number moves nothing');
  p.app.past.compare.cut(0.5);
  p.map.dragged = 300;
  p.map.fire('move');
  assert.equal(cutAt(p), '212', 'the pane went with the map, and the cut stays under the divider');
});

test('the day on the right is changed as a day is: what was there stays under it until its tiles have come', async () => {
  const p = await load('#overworld/100/200/-1');
  p.terrain()[0].arrive();
  p.app.past.set(LATER);
  p.terrain().at(-1).arrive();
  await faded();
  p.app.past.compare.start(null);
  const now = p.terrain().at(-1);
  now.arrive();
  p.app.past.compare.start(THEN);
  const then = p.terrain().at(-1);
  assert.equal(then.options.pane, 'beside');
  assert.equal(p.map.hasLayer(now), true);
  then.arrive();
  await faded();
  assert.deepEqual(sides(p), ['tilePane:2026-09-02', 'beside:2026-09-01']);
  const drawn = p.terrain().length;
  p.app.past.compare.start(THEN);
  assert.equal(p.terrain().length, drawn, 'the day already on the right is not drawn again');
});

test('stepping the day on the left leaves the right as it is, and each side turns over by itself', async () => {
  const p = await load('#overworld/100/200/-1');
  p.terrain()[0].arrive();
  p.app.past.set(THEN);
  const then = p.terrain().at(-1);
  then.arrive();
  await faded();
  p.app.past.compare.start(null);
  const right = p.terrain().at(-1);
  right.arrive();
  p.app.past.set(LATER);
  assert.equal(p.map.hasLayer(right), true);
  assert.equal(p.terrain().filter((l) => l.options.pane === 'beside').length, 1);
  assert.equal(p.map.hasLayer(then), true, 'the day stepped from stays under the day stepped to');
  p.terrain().find((l) => l.options.day === '2026-09-02').arrive();
  await faded();
  assert.deepEqual(sides(p).sort(), ['beside:now', 'tilePane:2026-09-02']);
});

test('another dimension shown while two are compared draws both sides of it alone', async () => {
  const p = await load('#overworld/100/200/-1');
  p.terrain()[0].arrive();
  p.app.past.set(THEN);
  p.app.past.compare.start(null);
  assert.equal(p.app.go('nether', 10, 20), true);
  assert.deepEqual(p.terrain().map((l) => `${l.options.pane}:${l.options.dimension}`), ['tilePane:nether', 'beside:nether']);
  assert.equal(p.terrain()[0].isLoading(), false, 'the day did not hold the nether, so the left asks for none of it');
  assert.equal(p.terrain()[1].isLoading(), true);
});

test('leaving a comparison takes the second terrain away and leaves the day as it was drawn', async () => {
  const p = await load('#overworld/100/200/-1');
  p.terrain()[0].arrive();
  p.app.past.set(THEN);
  const left = p.terrain().at(-1);
  left.arrive();
  await faded();
  p.app.past.compare.start(LATER);
  p.app.past.compare.start(null);
  p.app.past.compare.stop();
  assert.deepEqual(p.terrain(), [left]);
  assert.equal(p.app.past.compare.on(), false);
  assert.equal(p.app.past.get(), '2026-09-01');
  assert.equal(p.map.panes.beside.style.clipPath, '');
  await faded();
  assert.deepEqual(p.terrain(), [left], 'nothing of the right comes back when its wait ends');
});

test('a new render while a day is compared with now draws the right again and leaves the day', async () => {
  const p = await load('#overworld/100/200/-1');
  p.terrain()[0].arrive();
  p.app.past.set(THEN);
  const left = p.terrain().at(-1);
  left.arrive();
  await faded();
  p.app.past.compare.start(null);
  const right = p.terrain().at(-1);
  right.arrive();
  p.state.today = { ...TODAY, renderedAt: '2026-10-14T10:00:00Z', maxRegionX: 5 };
  p.app.reload();
  await p.settle();
  const again = p.terrain().at(-1);
  assert.notEqual(again, right);
  assert.equal(again.options.pane, 'beside');
  assert.deepEqual(blocks(again.options.bounds), [-2048, -2048, 2048, 3072]);
  assert.equal(p.map.hasLayer(left), true);
  p.app.past.compare.start(LATER);
  const later = p.terrain().at(-1);
  p.state.today = { ...TODAY, renderedAt: '2026-10-14T11:00:00Z' };
  p.app.reload();
  await p.settle();
  assert.equal(p.terrain().at(-1), later, 'two days that are past are not drawn again for a render');
  assert.equal(p.map.hasLayer(left), true);
});

test('the day on the right is an eighth part of the address, after the day, and goes with the comparison', async () => {
  const p = await load('#overworld/100/200/-1');
  p.app.past.set(THEN);
  p.app.past.compare.start(null);
  assert.equal(p.address(), '#overworld/100/200/-1///2026-09-01/now');
  assert.equal(p.app.past.compare.wanted(), 'now');
  p.app.past.compare.start(LATER);
  assert.equal(p.address(), '#overworld/100/200/-1///2026-09-01/2026-09-02');
  p.app.link.set(SHEET);
  assert.equal(p.address(), `#overworld/100/200/-1/${SHEET}//2026-09-01/2026-09-02`);
  assert.equal(p.app.link.href(), `https://map.test/#overworld/100/200/-1/${SHEET}//2026-09-01/2026-09-02`);
  p.map.setView([64, -32], 2.5);
  p.map.fire('moveend');
  assert.equal(p.address(), `#overworld/-32/64/2.5/${SHEET}//2026-09-01/2026-09-02`);
  p.app.past.compare.stop();
  assert.equal(p.address(), `#overworld/-32/64/2.5/${SHEET}//2026-09-01`);
  assert.equal(p.app.past.compare.wanted(), '');
});

test('an address with a day on the right reads to everything else as it did, and says what it asks for until it is taken up', async () => {
  const p = await load(`#nether/100/200/-1/${SHEET}//2026-09-01/2026-09-02`);
  assert.deepEqual(p.terrain().map((l) => l.options.dimension), ['nether']);
  assert.deepEqual(p.map.getCenter(), { lat: 200, lng: 100 });
  assert.equal(p.app.link.get(), SHEET);
  assert.equal(p.app.past.wanted(), '2026-09-01');
  assert.equal(p.app.past.compare.wanted(), '2026-09-02');
  assert.equal(p.app.past.compare.on(), false, 'what an address asks for is not drawn until the days are known');
  p.map.fire('moveend');
  assert.equal(p.address(), `#nether/100/200/-1/${SHEET}//2026-09-01/2026-09-02`);
  // Whoever sets the day and compares nothing leaves nothing beside it.
  p.app.past.set(THEN);
  assert.equal(p.app.past.compare.wanted(), '');
  assert.equal(p.address(), `#nether/100/200/-1/${SHEET}//2026-09-01`);
});

test('what is beside no day, or is neither a date nor now, is not read from an address', async () => {
  for (const [hash, plain] of [
    ['#overworld/100/200/-1////now', '#overworld/100/200/-1'],
    ['#overworld/100/200/-1///2026-09-01/later', '#overworld/100/200/-1///2026-09-01'],
    ['#overworld/100/200/-1///2026-09-01/<b>', '#overworld/100/200/-1///2026-09-01'],
    ['#overworld/100/200/-1//v1.e30/2026-09-01/now', '#overworld/100/200/-1//v1.e30/2026-09-01/now'],
  ]) {
    const p = await load(hash);
    assert.equal(p.app.past.compare.wanted(), '', hash);
    if (hash !== plain) {
      p.map.fire('moveend');
      assert.equal(p.address(), plain, hash);
    }
  }
});

test('an address that changes only in what is beside its day says so once', async () => {
  const p = await load('#overworld/100/200/-1');
  await p.turn('#overworld/100/200/-1///2026-09-01/now');
  assert.deepEqual(p.state.heard, ['2026-09-01']);
  assert.equal(p.app.past.compare.wanted(), 'now');
  await p.turn('#overworld/100/200/-1///2026-09-01/2026-09-02');
  assert.deepEqual(p.state.heard, ['2026-09-01', '2026-09-01']);
  assert.equal(p.app.past.compare.wanted(), '2026-09-02');
  await p.turn('#overworld/300/400/-1///2026-09-01/2026-09-02');
  assert.equal(p.state.heard.length, 2);
  await p.turn('#overworld/300/400/-1///2026-09-01');
  assert.equal(p.state.heard.length, 3);
  assert.equal(p.app.past.compare.wanted(), '');
});

test('with the timeline: a link to a kept day opens on that day, and stepping through days and back to now is in the address', async () => {
  const p = await load('#overworld/100/200/-1///2026-09-01', null, [THEN, LATER]);
  assert.equal(p.app.past.get(), '2026-09-01');
  assert.equal(p.terrain().at(-1).options.day, '2026-09-01');
  assert.equal(p.part('body').hidden, false);
  assert.equal(p.address(), '#overworld/100/200/-1///2026-09-01');
  await p.press('later');
  assert.equal(p.address(), '#overworld/100/200/-1///2026-09-02');
  assert.equal(p.app.link.href(), 'https://map.test/#overworld/100/200/-1///2026-09-02');
  await p.press('now');
  assert.equal(p.address(), '#overworld/100/200/-1');
  assert.equal(p.app.past.get(), '');
  await p.press('earlier');
  assert.equal(p.address(), '#overworld/100/200/-1///2026-09-02');
  await p.press('close');
  assert.equal(p.address(), '#overworld/100/200/-1', 'putting the timeline away puts the address back on now');
});

test('with the timeline: a link to a day that is not kept opens on now, and the address stops naming it', async () => {
  const p = await load('#overworld/100/200/-1///2025-01-01', null, [THEN, LATER]);
  assert.equal(p.app.past.get(), '');
  assert.equal(p.address(), '#overworld/100/200/-1');
  assert.equal(p.part('body').hidden, true);
  assert.equal(p.part('day').textContent, 'Now');
  assert.equal(p.terrain().length, 1);
  assert.equal(p.terrain()[0].options.day, '');
});

test('with the timeline: an address that changes to a day, to another place on it, and to none moves the timeline with it', async () => {
  const p = await load('#overworld/100/200/-1', null, [THEN, LATER]);
  assert.equal(p.part('body').hidden, true);
  await p.turn('#overworld/300/400/-1///2026-09-02');
  assert.equal(p.app.past.get(), '2026-09-02');
  assert.equal(p.part('body').hidden, false);
  assert.equal(p.value(), '1');
  assert.deepEqual(p.map.getCenter(), { lat: 400, lng: 300 });
  await p.turn('#nether/10/20/-1///2026-09-02');
  assert.equal(p.app.past.get(), '2026-09-02');
  assert.equal(p.address(), '#nether/10/20/-1///2026-09-02');
  await p.turn('#nether/10/20/-1///2025-01-01');
  assert.equal(p.app.past.get(), '', 'a day not kept is now');
  assert.equal(p.address(), '#nether/10/20/-1');
  await p.turn('#overworld/300/400/-1///2026-09-01');
  assert.equal(p.app.past.get(), '2026-09-01');
  await p.turn('#overworld/300/400/-1');
  assert.equal(p.app.past.get(), '');
  assert.equal(p.part('day').textContent, 'Now');
  assert.equal(p.address(), '#overworld/300/400/-1');
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
