'use strict';

// The underground view, checked by doing it: its script is run in a page of
// the kind dom.cjs makes, beside the panel's own, with a map that only
// remembers its layers and a server that answers as these tests tell it to.
// Run by `go test ./web/` where there is a node to run it with, or by hand:
// `node web/testdata/underground.test.cjs`.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { page } = require('./dom.cjs');

const tests = [];
const test = (name, fn) => tests.push([name, fn]);
const wait = (ms = 30) => new Promise((done) => setTimeout(done, ms));
let now = Date.now();
Date.now = () => now;
const later = () => { now += 120_000; };

const STYLES = [{ id: 'floor', label: 'Spaces', says: 'The ground of caves.' }, { id: 'cut', label: 'Cut', says: 'Everything above the cut taken away.' }];
const level = (y, label = String(y), band = 16) => ({ id: String(y), y, label, band });
const OVERWORLD = [level(62, 'Sea level'), level(40), level(16), level(0), level(-16), level(-32), level(-48)];
const NETHER = [level(100, 'Top', 0), level(66, 'Mid', 0), level(34, 'Low', 0)];
const listingOf = (levels, over = {}) => ({
  offered: true, ready: true, version: 'v1', tiles: { minZoom: -1, maxZoom: 0, size: 256 }, styles: STYLES, levels, bands: [16, 32, 64], ...over,
});

async function load({ dimension = 'overworld', listings = {}, status = 200, storage = new Map(), nether = null, day = '' } = {}) {
  const p = page(storage);
  const { win, doc } = p;
  const lookup = doc.getElementById;
  const made = new Map();
  const dialog = (id) => {
    if (!made.has(id)) made.set(id, Object.assign(doc.createElement(id === 'grid' ? 'input' : 'div'), { id, checked: false, showModal() {}, close() {} }));
    return made.get(id);
  };
  doc.getElementById = (id) => lookup(id) || (/^(views|preview)(-|$)|^(grid|more-open)$/.test(id) ? dialog(id) : null);
  const heard = new Map();
  const state = {
    dimension, zoom: 0, asked: [], layers: [], removed: [], day, cut: 'default',
    listings: { overworld: listingOf(OVERWORLD), nether: listingOf(NETHER), end: listingOf([level(255, 'All', 0)]), ...listings },
  };
  win.L = {
    TileLayer: {
      extend: (proto) => {
        class Layer {
          constructor(url, options) { this.options = options; this.redraws = 0; state.layers.push(this); }
          addTo() { return this; }
          redraw() { this.redraws += 1; }
        }
        Object.assign(Layer.prototype, proto);
        return Layer;
      },
    },
  };
  // The nether's own levels, as the map's script gives them: the default
  // has the id 'default' and a height, and choosing one is the map's.
  const own = nether === null ? [] : nether;
  const placed = { get: () => ({ d: state.dimension, x: 1, z: 2, zoom: 0 }), set: (at) => { state.set = at; return true; } };
  Object.assign(win.mcmap, {
    map: {
      on: (type, fn) => { if (!heard.has(type)) heard.set(type, []); heard.get(type).push(fn); },
      removeLayer: (layer) => state.removed.push(layer),
      getZoom: () => state.zoom,
    },
    dimension: () => state.dimension,
    extent: () => null,
    past: { get: () => state.day },
    level: { list: () => (state.dimension === 'nether' ? own : []), get: () => state.cut, set: (id) => { state.cut = id; doc.dispatchEvent({ type: 'mcmap:view' }); return true; } },
    place: placed,
  });
  Object.assign(win, { location: { href: 'http://map.example/', origin: 'http://map.example', pathname: '/', search: '' }, URL, TextEncoder, TextDecoder, Uint8Array, atob, btoa });
  win.AbortController = AbortController;
  win.setInterval = () => 0;
  win.fetch = async (address) => {
    state.asked.push(address);
    const wanted = /^api\/layers\?dimension=(\w+)/.exec(address);
    if (status >= 400) return { ok: false, status, json: async () => ({}) };
    return { ok: true, status: 200, json: async () => JSON.parse(JSON.stringify(state.listings[wanted[1]])) };
  };
  vm.runInContext(fs.readFileSync(path.join(__dirname, '..', 'underground.js'), 'utf8'), win, { filename: 'underground.js' });
  await wait();

  const tree = () => doc.getElementById('layers-body').querySelector('.tree');
  const nameOf = (li) => {
    const row = li.children.find((child) => child.classList.contains('row'));
    const name = row ? row.children.find((child) => child.classList.contains('name')) : null;
    return name ? name.textContent : '';
  };
  const kidsOf = (li) => li.children.find((child) => child.classList.contains('kids'));
  const find = (...labels) => {
    let list = tree();
    let at = null;
    for (const label of labels) {
      at = list ? list.children.find((child) => child.classList.contains('node') && nameOf(child) === label) || null : null;
      if (!at) return null;
      list = kidsOf(at);
    }
    return at;
  };
  const part = (labels, name) => find(...labels).children.find((child) => child.classList.contains('row')).children.find((child) => child.classList.contains(name));
  const press = async (labels, name = 'check') => { part(labels, name).click(); await wait(); };
  const view = async (to) => {
    if (to) state.dimension = to;
    doc.dispatchEvent({ type: 'mcmap:view' });
    await wait();
  };
  const zoom = async (z) => { state.zoom = z; for (const fn of heard.get('zoomend')) fn(); await wait(); };
  // The buttons of the height choice under a row, by their label.
  const options = (row) => find('Underground', row).querySelectorAll('.segment').filter((b) => b.textContent !== '');
  return {
    ...p,
    state,
    find,
    part,
    press,
    view,
    zoom,
    nameOf,
    kidsOf,
    options,
    layer: () => state.layers[state.layers.length - 1] || null,
    live: () => state.layers.filter((l) => !state.removed.includes(l)),
    checked: (...labels) => part(labels, 'check').getAttribute('aria-checked'),
    note: (row) => find('Underground', row).children.find((child) => child.classList.contains('note')).textContent,
    asked: (prefix) => state.asked.filter((a) => a.startsWith(prefix)),
  };
}

const URL_OF = { z: 0, x: 3, y: -4 };

test('the panel has an Underground section of two rows, both off, and nothing is drawn', async () => {
  const p = await load();
  const group = p.find('Underground');
  assert.ok(group, 'there is no Underground section');
  assert.deepEqual(p.kidsOf(group).children.map(p.nameOf), ['Cave spaces', 'Cut view']);
  assert.equal(p.checked('Underground', 'Cave spaces'), 'false');
  assert.equal(p.checked('Underground', 'Cut view'), 'false');
  assert.equal(p.state.layers.length, 0);
  assert.equal(p.asked('api/layers/tiles').length, 0);
  assert.deepEqual(p.asked('api/layers'), ['api/layers?dimension=overworld']);
});

test('turning a style on draws one layer, at the height the viewer starts at, and the other style takes it over', async () => {
  const p = await load();
  await p.press(['Underground', 'Cave spaces']);
  assert.equal(p.live().length, 1);
  const layer = p.layer();
  assert.equal(layer.getTileUrl(URL_OF), 'api/layers/tiles/overworld/0/3/-4.png?level=0&style=floor&v=v1');
  await p.press(['Underground', 'Cut view']);
  assert.equal(p.checked('Underground', 'Cave spaces'), 'false', 'both styles were on');
  assert.equal(p.checked('Underground', 'Cut view'), 'true');
  assert.equal(p.live().length, 1, 'a second layer was made');
  assert.equal(layer.redraws, 1);
  assert.match(layer.getTileUrl(URL_OF), /style=cut/);
  await p.press(['Underground', 'Cut view']);
  assert.equal(p.state.removed.length, 1, 'the layer stayed with both off');
});

test('the layer is the terrain\'s grid, at the two zooms the server draws, over the terrain', async () => {
  const p = await load();
  await p.press(['Underground', 'Cave spaces']);
  const o = p.layer().options;
  assert.equal(o.tileSize, 256);
  assert.equal(o.minNativeZoom, -1);
  assert.equal(o.maxNativeZoom, 0);
  // Past the widest the terrain shows and this does not.
  assert.equal(o.minZoom, -1);
  assert.equal(o.noWrap, true);
  assert.equal(o.className, 'underground');
  assert.equal(o.zIndex, 2);
  await p.zoom(-3);
  assert.match(p.note('Cave spaces'), /Zoom in to see it/);
});

test('the choice of height is the overworld\'s levels, is kept, and redraws the layer', async () => {
  const storage = new Map();
  const p = await load({ storage });
  await p.press(['Underground', 'Cave spaces']);
  await p.press(['Underground', 'Cave spaces'], 'twist');
  assert.deepEqual(p.options('Cave spaces').map((b) => b.textContent), ['Sea level', '40', '16', '0', '-16', '-32', '-48']);
  const layer = p.layer();
  p.options('Cave spaces').find((b) => b.textContent === '-32').click();
  await wait();
  assert.match(layer.getTileUrl(URL_OF), /level=-32&style=floor/);
  assert.equal(layer.redraws, 1);
  assert.equal(p.settings.get('underground').level, '-32');
  // It is the same choice under the other row.
  await p.press(['Underground', 'Cut view']);
  assert.match(layer.getTileUrl(URL_OF), /level=-32&style=cut/);
  // And a page opened later starts there.
  const q = await load({ storage });
  await q.press(['Underground', 'Cave spaces']);
  assert.match(q.layer().getTileUrl(URL_OF), /level=-32/);
});

test('a kept height the server no longer offers is not drawn: the page starts as it does', async () => {
  const storage = new Map([['mcmap.settings', JSON.stringify({ v: 4, underground: { level: '-80' } })]]);
  const p = await load({ storage });
  await p.press(['Underground', 'Cave spaces']);
  assert.match(p.layer().getTileUrl(URL_OF), /level=0&/);
});

test('in the nether the height is the nether\'s own level, and choosing in either is choosing in both', async () => {
  const own = [{ id: 'default', y: 100, label: 'Top' }, { id: '66', y: 66, label: 'Mid' }, { id: '34', y: 34, label: 'Low' }];
  const p = await load({ dimension: 'nether', nether: own });
  await p.press(['Underground', 'Cave spaces']);
  assert.match(p.layer().getTileUrl(URL_OF), /nether\/0\/3\/-4\.png\?level=100&style=floor/);
  await p.press(['Underground', 'Cave spaces'], 'twist');
  assert.deepEqual(p.options('Cave spaces').map((b) => b.textContent), ['Top', 'Mid', 'Low']);
  // Choosing here moves the map's own level, which draws the layer.
  p.options('Cave spaces').find((b) => b.textContent === 'Low').click();
  await wait();
  assert.equal(p.state.cut, '34');
  assert.match(p.layer().getTileUrl(URL_OF), /level=34&/);
  // And the map's picker moving it moves this.
  p.state.cut = '66';
  await p.view();
  assert.match(p.layer().getTileUrl(URL_OF), /level=66&/);
  assert.equal(p.settings.get('underground').level, null, 'the nether\'s level was kept as the overworld\'s');
});

test('in a nether with no levels of its own the height is this page\'s, and the end has none to choose', async () => {
  const p = await load({ dimension: 'nether', nether: [] });
  await p.press(['Underground', 'Cut view']);
  assert.match(p.layer().getTileUrl(URL_OF), /level=100&style=cut/);
  await p.press(['Underground', 'Cut view'], 'twist');
  p.options('Cut view').find((b) => b.textContent === 'Mid').click();
  await wait();
  assert.match(p.layer().getTileUrl(URL_OF), /level=66&/);
  await p.view('end');
  assert.equal(p.layer().options.dimension, 'end');
  assert.match(p.layer().getTileUrl(URL_OF), /end\/0\/3\/-4\.png\?level=255&style=cut/);
});

test('another dimension replaces the layer, and a past day or the login takes it away', async () => {
  const p = await load();
  await p.press(['Underground', 'Cave spaces']);
  const first = p.layer();
  await p.view('nether');
  assert.ok(p.state.removed.includes(first));
  assert.equal(p.layer().options.dimension, 'nether');
  p.state.day = '2026-10-01';
  await p.view();
  assert.ok(p.state.removed.includes(p.layer()), 'a past day shows the underground of now');
  p.state.day = '';
  await p.view();
  assert.equal(p.live().length, 1);
  p.doc.body.classList.add('locked');
  await p.view();
  assert.equal(p.live().length, 0);
});

test('a new reading redraws the layer with its version, asked for when the listing is old', async () => {
  const p = await load();
  await p.press(['Underground', 'Cave spaces']);
  const layer = p.layer();
  p.state.listings.overworld = listingOf(OVERWORLD, { version: 'v2' });
  later();
  await p.view();
  assert.match(layer.getTileUrl(URL_OF), /&v=v2$/);
  assert.equal(layer.redraws, 1);
});

test('before the world is open nothing is drawn and the row says so, and a server without it has no rows', async () => {
  const p = await load({ listings: { overworld: listingOf(OVERWORLD, { ready: false, version: '' }) } });
  await p.press(['Underground', 'Cave spaces']);
  assert.equal(p.state.layers.length, 0);
  assert.match(p.note('Cave spaces'), /not been opened/);
  const off = await load({ status: 404 });
  assert.equal(off.find('Underground'), null);
  assert.equal(off.asked('api/layers').length, 1);
  await off.view();
  assert.equal(off.asked('api/layers').length, 1, 'a server without it was asked again');
});

test('a label and a level are only ever taken as text and as a height', async () => {
  const p = await load({ listings: { overworld: listingOf([level(0, '<img src=x onerror=alert(1)>'), { id: 'x; drop', y: 1, label: 'bad', band: 16 }]) } });
  await p.press(['Underground', 'Cave spaces']);
  await p.press(['Underground', 'Cave spaces'], 'twist');
  assert.deepEqual(p.options('Cave spaces').length, 0, 'one level is no choice');
  assert.match(p.layer().getTileUrl(URL_OF), /level=0&/);
});

test('a saved view carries the overworld\'s height in its place, and puts it back', async () => {
  const p = await load();
  await p.press(['Underground', 'Cave spaces']);
  await p.press(['Underground', 'Cave spaces'], 'twist');
  p.options('Cave spaces').find((b) => b.textContent === '-16').click();
  await wait();
  assert.equal(p.win.mcmap.place.get().level, '-16');
  p.win.mcmap.place.set({ d: 'overworld', x: 1, z: 2, zoom: 0, level: '40' });
  assert.equal(p.settings.get('underground').level, '40');
  assert.match(p.layer().getTileUrl(URL_OF), /level=40/);
  // A place that names a height that is not one leaves the choice be.
  p.win.mcmap.place.set({ d: 'overworld', x: 1, z: 2, zoom: 0, level: 'deep' });
  assert.equal(p.settings.get('underground').level, '40');
  assert.deepEqual(p.state.set, { d: 'overworld', x: 1, z: 2, zoom: 0, level: 'deep' }, 'the map was not given the place');
});

(async () => {
  let failed = 0;
  for (const [name, fn] of tests) {
    try {
      await fn();
      console.log(`ok ${name}`);
    } catch (err) {
      failed += 1;
      console.log(`FAIL ${name}\n${err.stack}`);
    }
  }
  console.log(`${tests.length - failed} of ${tests.length} passed`);
  process.exit(failed ? 1 : 0);
})();
