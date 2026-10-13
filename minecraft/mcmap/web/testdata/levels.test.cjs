'use strict';

// What the nether's levels do, checked by doing it: app.js, the record's
// script and the picker's script are run in a page of the kind dom.cjs makes,
// on a map that keeps Leaflet's rules for a tile layer's loading, against a
// server that lists the levels it has drawn. Run by `go test ./web/` where
// there is a node to run it with, or by hand:
// `node web/testdata/levels.test.cjs`.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { page } = require('./dom.cjs');
const { leaflet } = require('./leaflet.cjs');

// What the page's script makes comes from another realm, which strict
// deep equality would tell apart from a literal that looks the same.
const same = (got, want, said) => assert.equal(JSON.stringify(got), JSON.stringify(want), said);
const tick = () => new Promise((done) => setTimeout(done, 0));
const tests = [];
const test = (name, fn) => tests.push([name, fn]);

const EXTENT = { rendered: true, renderedAt: '2026-10-14T09:00:00Z', format: 'webp', minZoom: -5, maxZoom: 0, minRegionX: -4, maxRegionX: 3, minRegionZ: -4, maxRegionZ: 3 };
const LEVELS = [{ id: 'default', y: 100, label: 'Top' }, { id: '66', y: 66, label: 'Mid' }, { id: '34', y: 34, label: 'Low' }];
const THEN = { day: '2026-09-01', dimensions: [{ id: 'nether', format: 'webp', minZoom: -2, maxZoom: 0, minRegionX: 0, maxRegionX: 0, minRegionZ: 0, maxRegionZ: 0 }] };
const KEY = 'mcmap.settings';

// A page opened at the place its address names. levels is what the server
// says the nether has, and storage what the browser already holds.
async function load(hash, { levels = LEVELS, storage = new Map() } = {}) {
  const p = page(storage, { files: ['settings.js'] });
  const { win, doc } = p;
  const found = doc.getElementById;
  const asked = new Map();
  doc.getElementById = (id) => {
    if (found(id)) return found(id);
    if (!asked.has(id)) asked.set(id, doc.createElement('div'));
    return asked.get(id);
  };
  doc.getElementById('login').hidden = true;
  doc.hidden = false;
  const { L, map } = leaflet();
  const state = { levels };
  Object.assign(win, {
    L,
    location: { hash, href: '' },
    history: { replaceState(_, __, to) { win.location.hash = to; } },
    navigator: {},
    setInterval: () => 0,
    clearInterval() {},
    fetch: async (address) => {
      const nether = { id: 'nether', ...EXTENT, ...(state.levels ? { levels: state.levels } : {}) };
      const body = address === 'api/map' ? { world: 'w', refreshSeconds: 600, dimensions: [{ id: 'overworld', ...EXTENT }, nether] } : {};
      return { ok: true, status: 200, headers: { get: () => '' }, json: async () => body, text: async () => JSON.stringify(body) };
    },
  });
  for (const file of ['app.js', 'levels.js']) vm.runInContext(fs.readFileSync(path.join(__dirname, '..', file), 'utf8'), win, { filename: file });
  const settle = async () => { for (let i = 0; i < 6; i++) await tick(); };
  await settle();
  const terrain = () => [...map.layers].filter((l) => l.options.dimension);
  const bar = doc.getElementById('map').querySelector('.levels');
  const buttons = () => bar.children.map((b) => b.textContent);
  const pressed = () => bar.children.filter((b) => b.getAttribute('aria-pressed') === 'true').map((b) => b.textContent);
  const press = async (label) => {
    bar.children.find((b) => b.textContent === label).click();
    await settle();
  };
  const pick = (label) => win.mcmap.level.set(bar.children.find((b) => b.textContent === label).dataset.id);
  const url = (layer) => layer.getTileUrl({ z: -1, x: 2, y: 3 });
  const kept = () => JSON.parse(storage.get(KEY)).levels;
  return { ...p, app: win.mcmap, map, state, settle, terrain, bar, buttons, pressed, press, pick, url, kept, storage };
}

// Long enough for tiles to have faded in: the page's waits are cut to a
// moment by dom.cjs.
const faded = () => new Promise((done) => setTimeout(done, 30));

test('the default level is drawn from the addresses there have always been', async () => {
  const p = await load('#nether/100/200/-1');
  assert.equal(p.url(p.terrain()[0]), 'tiles/nether/-1/2/3.webp');
});

test('in the nether with several levels there is a labelled row of buttons, the default one pressed', async () => {
  const p = await load('#nether/100/200/-1');
  assert.equal(p.bar.hidden, false);
  assert.equal(p.bar.getAttribute('role'), 'group');
  assert.ok(p.bar.getAttribute('aria-label'));
  same(p.buttons(), ['Top', 'Mid', 'Low']);
  same(p.pressed(), ['Top']);
  assert.ok(p.bar.children.every((b) => b.tagName === 'BUTTON' && b.tabIndex === 0), 'each is a button, reached by the keyboard');
});

test('choosing a level draws it from its own addresses, where the map was, and presses its button', async () => {
  const p = await load('#nether/-1500/1200/-3');
  p.terrain()[0].arrive();
  await p.press('Mid');
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp?level=66');
  same(p.map.getCenter(), { lat: 1200, lng: -1500 });
  assert.equal(p.map.getZoom(), -3);
  same(p.pressed(), ['Mid']);
  assert.equal(p.app.level.get(), '66');
  await p.press('Top');
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp');
});

test('the choice is kept, and the nether opens on it next time', async () => {
  const p = await load('#nether/100/200/-1');
  await p.press('Low');
  same(p.kept(), { nether: '34' });
  const again = await load('#nether/100/200/-1', { storage: p.storage });
  assert.equal(again.url(again.terrain()[0]), 'tiles/nether/-1/2/3.webp?level=34');
  same(again.pressed(), ['Low']);
  await again.press('Top');
  same(again.kept(), { nether: null });
});

test('the old version of the record is read, and a later one is left as it was', async () => {
  const old = await load('#nether/100/200/-1', { storage: new Map([[KEY, JSON.stringify({ v: 2, grid: { on: true } })]]) });
  same(old.pressed(), ['Top']);
  const later = JSON.stringify({ v: 99, levels: { nether: '66' } });
  const p = await load('#nether/100/200/-1', { storage: new Map([[KEY, later]]) });
  await p.press('Low');
  assert.equal(p.storage.get(KEY), later, 'a record of a later page is not written over');
});

test('a level that is kept but is no longer offered draws the default one', async () => {
  const p = await load('#nether/100/200/-1', { storage: new Map([[KEY, JSON.stringify({ v: 3, levels: { nether: '12' } })]]) });
  assert.equal(p.url(p.terrain()[0]), 'tiles/nether/-1/2/3.webp');
  same(p.pressed(), ['Top']);
});

test('no picker outside the nether, and it is there again on coming back', async () => {
  const p = await load('#overworld/100/200/-1');
  assert.equal(p.bar.hidden, true);
  same(p.app.level.list(), []);
  p.app.go('nether', 10, 20);
  await p.settle();
  assert.equal(p.bar.hidden, false);
  p.app.go('overworld', 10, 20);
  await p.settle();
  assert.equal(p.bar.hidden, true);
});

test('a level chosen in another dimension is not drawn there, and is drawn in the nether', async () => {
  const p = await load('#overworld/100/200/-1');
  assert.equal(p.app.level.set('66'), true, 'kept for the nether');
  assert.equal(p.url(p.terrain()[0]), 'tiles/overworld/-1/2/3.webp');
  p.app.go('nether', 10, 20);
  await p.settle();
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp?level=66');
});

test('no picker where the server offers one level, or none', async () => {
  for (const levels of [null, [], [LEVELS[0]]]) {
    const p = await load('#nether/100/200/-1', { levels });
    assert.equal(p.bar.hidden, true, JSON.stringify(levels));
    assert.equal(p.app.level.set('66'), false);
    assert.equal(p.url(p.terrain()[0]), 'tiles/nether/-1/2/3.webp');
  }
});

test('a level the server does not offer cannot be chosen', async () => {
  const p = await load('#nether/100/200/-1');
  for (const id of ['12', '', '../overworld', undefined, 66]) assert.equal(p.app.level.set(id), false, String(id));
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp');
  assert.equal(p.storage.get(KEY) === undefined || JSON.parse(p.storage.get(KEY)).levels.nether === null, true);
});

test('while a past day is shown the picker is away and the day is of the default cut, and the choice returns after it', async () => {
  const p = await load('#nether/100/200/-1');
  await p.press('Mid');
  p.terrain().at(-1).arrive();
  p.app.past.set(THEN);
  await p.settle();
  assert.equal(p.bar.hidden, true);
  const layer = p.terrain().at(-1);
  assert.equal(layer.getTileUrl({ z: -1, x: 2, y: 3 }), 'api/history/tiles/2026-09-01/nether/-1/2/3.webp');
  same(p.app.level.list(), []);
  assert.equal(p.app.level.set('34'), true, 'a choice made now is kept for after the day, and nothing is redrawn');
  assert.equal(p.terrain().at(-1), layer);
  p.app.past.set(null);
  await p.settle();
  assert.equal(p.bar.hidden, false);
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp?level=34');
  same(p.pressed(), ['Low']);
});

test('stepping through levels fast keeps one picture under the one coming, and never an empty map', async () => {
  const p = await load('#nether/100/200/-1');
  const [top] = p.terrain();
  top.arrive();
  p.pick('Mid');
  const mid = p.terrain().at(-1);
  p.pick('Low');
  const low = p.terrain().at(-1);
  assert.equal(p.map.hasLayer(mid), false, 'a level passed over must not go on asking for its tiles');
  assert.equal(p.map.hasLayer(top), true, 'what was there stays until what is wanted has come');
  p.pick('Top');
  const again = p.terrain().at(-1);
  assert.equal(p.map.hasLayer(low), false);
  assert.equal(p.map.hasLayer(top), true);
  assert.equal(p.terrain().length, 2, 'the one under and the one coming, and no more');
  again.arrive();
  await faded();
  assert.equal(p.terrain().length, 1);
  assert.equal(p.terrain()[0], again);
});

test('a level whose tiles never come does not keep the old ones for ever', async () => {
  const p = await load('#nether/100/200/-1');
  p.terrain()[0].arrive();
  p.pick('Mid');
  assert.equal(p.terrain().length, 2);
  await faded();
  assert.equal(p.terrain().length, 1);
});

test('the place carries the level, and takes the map to it', async () => {
  const p = await load('#nether/100/200/-1');
  assert.equal(p.app.place.get().level, undefined, 'the default cut is the absence of a level');
  await p.press('Mid');
  same(p.app.place.get(), { d: 'nether', x: 100, z: 200, zoom: -1, level: '66' });
  await p.press('Top');
  assert.equal(p.app.place.set({ d: 'nether', x: 10, z: 20, zoom: -2, level: '34' }), true);
  await p.settle();
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp?level=34');
  same(p.pressed(), ['Low']);
  // A place without a level is the default cut, and one for another
  // dimension leaves the choice alone.
  p.app.place.set({ d: 'nether', x: 10, z: 20, zoom: -2 });
  await p.settle();
  same(p.pressed(), ['Top']);
  p.app.place.set({ d: 'nether', x: 10, z: 20, zoom: -2, level: '66' });
  p.app.place.set({ d: 'overworld', x: 10, z: 20, zoom: -2 });
  await p.settle();
  assert.equal(p.app.level.get(), '66');
});

test('a saved view and a link may name the level, and nothing else of the kind', async () => {
  const p = await load('#nether/100/200/-1');
  const view = (level) => ({ name: 'v', layers: {}, place: { d: 'nether', x: 1, z: 2, zoom: 0, level } });
  assert.equal(p.settings.check.view(view('66')).place.level, '66');
  for (const bad of ['', '1234', '../x', '6 6', 66, null]) assert.equal(p.settings.check.view(view(bad)), null, JSON.stringify(bad));
  assert.equal(p.settings.check.record({ v: 3, levels: { nether: '34' } }).levels.nether, '34');
  assert.equal(p.settings.check.record({ v: 3, levels: { nether: 'x' } }), null);
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
