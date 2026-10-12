'use strict';

// What the map does when it is told a day, checked by doing it: app.js is
// run in a page of the kind dom.cjs makes, on a map that keeps Leaflet's
// own rules for the two things a day must not disturb. A view outside the
// bounds it is given is brought inside them and a zoom below its least is
// raised to it, as Leaflet does, and a tile layer is loading only while
// the view shows some of its bounds at a zoom it draws, and says 'load'
// only after that, as Leaflet does. Run by `go test ./web/` where there is
// a node to run it with, or by hand: `node web/testdata/past.test.cjs`.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { page } = require('./dom.cjs');

const tick = () => new Promise((done) => setTimeout(done, 0));
const tests = [];
const test = (name, fn) => tests.push([name, fn]);

function leaflet() {
  const at = (p) => (Array.isArray(p) ? { lat: p[0], lng: p[1] } : { lat: p.lat, lng: p.lng });
  const box = (south, west, north, east) => ({
    south, west, north, east,
    pad: (by) => box(south - (north - south) * by, west - (east - west) * by, north + (north - south) * by, east + (east - west) * by),
    contains: (p) => { const q = at(p); return q.lat >= south && q.lat <= north && q.lng >= west && q.lng <= east; },
  });
  class Heard {
    on(type, fn) { this.heard = [...(this.heard || []), [type, fn, false]]; return this; }
    once(type, fn) { this.heard = [...(this.heard || []), [type, fn, true]]; return this; }
    fire(type) {
      const mine = (this.heard || []).filter((h) => h[0] === type);
      this.heard = (this.heard || []).filter((h) => !(h[0] === type && h[2]));
      for (const h of mine) h[1]({ type });
      return this;
    }
  }
  class Sheet extends Heard {
    constructor() {
      super();
      this.options = { inertia: true };
      this._fadeAnimated = true;
      this.layers = new Set();
      this.centre = null;
      this.zoom = null;
      this.least = -Infinity;
      this.bounds = null;
    }
    getCenter() { return { ...this.centre }; }
    getZoom() { return this.zoom; }
    getContainer() { return {}; }
    held() {
      if (this.centre === null) return;
      this.zoom = Math.max(this.zoom, this.least);
      if (!this.bounds) return;
      this.centre = { lat: Math.min(Math.max(this.centre.lat, this.bounds.south), this.bounds.north), lng: Math.min(Math.max(this.centre.lng, this.bounds.west), this.bounds.east) };
    }
    setView(to, zoom) { this.centre = at(to); this.zoom = zoom; this.held(); return this; }
    fitBounds(b) { return this.setView([(b.south + b.north) / 2, (b.west + b.east) / 2], this.least); }
    setMinZoom(zoom) { this.least = zoom; this.held(); return this; }
    setMaxBounds(b) { this.bounds = b; this.held(); return this; }
    removeLayer(layer) { this.layers.delete(layer); return this; }
    hasLayer(layer) { return this.layers.has(layer); }
    invalidateSize() { return this; }
    panBy() { return this; }
  }
  const map = new Sheet();
  class Layer extends Heard {
    constructor(url, options) { super(); this.options = { ...options }; this.come = false; }
    static extend(props) {
      const Sub = class extends this {};
      Object.assign(Sub.prototype, props);
      return Sub;
    }
    addTo(to) { to.layers.add(this); return this; }
    isLoading() {
      const o = this.options;
      return !this.come && map.centre !== null && map.zoom >= o.minZoom && map.zoom <= o.maxZoom && (!o.bounds || o.bounds.contains(map.centre));
    }
    // Its tiles have come.
    arrive() { this.come = true; this.fire('load'); }
  }
  const L = {
    extend: Object.assign,
    CRS: { Simple: {} },
    Transformation: class {},
    map: () => map,
    TileLayer: Layer,
    GridLayer: Layer,
    latLngBounds: (a, b) => box(Math.min(a[0], b[0]), Math.min(a[1], b[1]), Math.max(a[0], b[0]), Math.max(a[1], b[1])),
  };
  return { L, map };
}

// The overworld as it is, four regions each way about the origin, and as
// it was on a day when it was one region and was cut another way.
const TODAY = { id: 'overworld', rendered: true, renderedAt: '2026-10-14T09:00:00Z', format: 'webp', minZoom: -5, maxZoom: 0, minRegionX: -4, maxRegionX: 3, minRegionZ: -4, maxRegionZ: 3 };
const THEN = { day: '2026-09-01', dimensions: [{ id: 'overworld', format: 'png', minZoom: -2, maxZoom: 0, minRegionX: 0, maxRegionX: 0, minRegionZ: 0, maxRegionZ: 0 }] };
const LATER = { day: '2026-09-02', dimensions: [{ ...THEN.dimensions[0], maxRegionX: 1 }] };
const NETHER = { ...TODAY, id: 'nether' };

// A page with app.js run on it, opened at the place its address names.
async function load(hash, look) {
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
  const state = { today: TODAY };
  Object.assign(win, {
    L,
    location: { hash, href: '' },
    history: { replaceState(_, __, to) { win.location.hash = to; } },
    navigator: {},
    setInterval: () => 0,
    clearInterval() {},
    fetch: async (address) => {
      const body = address === 'api/map' ? { world: 'w', refreshSeconds: 600, dimensions: [state.today, NETHER] } : {};
      return { ok: true, status: 200, headers: { get: () => '' }, json: async () => body, text: async () => JSON.stringify(body) };
    },
  });
  vm.runInContext(fs.readFileSync(path.join(__dirname, '..', 'app.js'), 'utf8'), win, { filename: 'app.js' });
  const settle = async () => { for (let i = 0; i < 6; i++) await tick(); };
  await settle();
  const terrain = () => [...map.layers].filter((l) => l.options.dimension);
  return { ...p, app: win.mcmap, map, state, settle, terrain };
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
  assert.equal(p.terrain().at(-1).options.minZoom, -5, 'the day is drawn, stretched, as far out as the map goes');
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
