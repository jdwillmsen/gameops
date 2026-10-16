'use strict';

// The lost-chunks overlay, checked by doing it: its script is run in a page
// of the kind dom.cjs makes, beside the panel's own, with a map that only
// remembers its layers and a server that answers as these tests tell it to.
// Run by `go test ./web/` where there is a node to run it with, or by hand:
// `node web/testdata/lost.test.cjs`.
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

const listingOf = (over = {}) => ({ offered: true, version: 'c1', missing: 3, tiles: { minZoom: -5, maxZoom: 4, size: 256 }, ...over });

async function load({ dimension = 'overworld', listings = {}, status = 200 } = {}) {
  const p = page(new Map());
  const { win, doc } = p;
  const state = { dimension, asked: [], layers: [], removed: [], day: '', listings: { overworld: listingOf(), nether: listingOf({ missing: 0 }), end: listingOf({ missing: 0 }), ...listings } };
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
  Object.assign(win.mcmap, {
    map: { on() {}, removeLayer: (layer) => state.removed.push(layer), getZoom: () => 0 },
    dimension: () => state.dimension,
    extent: () => null,
    past: { get: () => state.day },
  });
  win.setInterval = () => 0;
  win.fetch = async (address) => {
    state.asked.push(address);
    if (status >= 400) return { ok: false, status, json: async () => ({}) };
    const wanted = /^api\/lost\?dimension=(\w+)/.exec(address);
    return { ok: true, status: 200, json: async () => JSON.parse(JSON.stringify(state.listings[wanted[1]])) };
  };
  vm.runInContext(fs.readFileSync(path.join(__dirname, '..', 'lost.js'), 'utf8'), win, { filename: 'lost.js' });
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
  return {
    state,
    find,
    press: async () => { part(['Overlays', 'Lost chunks'], 'check').click(); await wait(); },
    checked: () => part(['Overlays', 'Lost chunks'], 'check').getAttribute('aria-checked'),
    note: () => find('Overlays', 'Lost chunks').children.find((child) => child.classList.contains('note')).textContent,
    view: async (to) => { if (to) state.dimension = to; doc.dispatchEvent({ type: 'mcmap:view' }); await wait(); },
    layer: () => state.layers[state.layers.length - 1] || null,
    live: () => state.layers.filter((l) => !state.removed.includes(l)),
    asked: () => state.asked.filter((a) => a.startsWith('api/lost')),
    doc,
  };
}

test('the overlay is a row of the Overlays section, off, and nothing is drawn until it is on', async () => {
  const p = await load();
  assert.ok(p.find('Overlays', 'Lost chunks'));
  assert.equal(p.checked(), 'false');
  assert.equal(p.state.layers.length, 0);
});

test('on, one layer is drawn from the server\'s tiles, over everything else but the grid, and says how many are missing', async () => {
  const p = await load();
  await p.press();
  assert.equal(p.live().length, 1);
  const layer = p.layer();
  assert.equal(layer.getTileUrl({ z: -3, x: 2, y: -1 }), 'api/lost/tiles/overworld/-3/2/-1.png?v=c1');
  assert.equal(layer.options.minNativeZoom, -5);
  assert.equal(layer.options.maxNativeZoom, 4);
  assert.equal(layer.options.zIndex, 4);
  assert.equal(layer.options.className, 'lost');
  assert.match(p.note(), /3 chunks that were in the world are not now/);
  await p.press();
  assert.equal(p.state.removed.length, 1);
});

test('a dimension that lost nothing says so, and another dimension replaces the layer', async () => {
  const p = await load();
  await p.press();
  const first = p.layer();
  await p.view('nether');
  assert.ok(p.state.removed.includes(first));
  assert.equal(p.layer().options.dimension, 'nether');
  assert.match(p.note(), /No chunk of this dimension is missing/);
});

test('a new count redraws the layer with its version, asked for when the listing is old', async () => {
  const p = await load();
  await p.press();
  const layer = p.layer();
  p.state.listings.overworld = listingOf({ version: 'c2', missing: 5 });
  later();
  await p.view();
  assert.match(layer.getTileUrl({ z: 0, x: 0, y: 0 }), /v=c2$/);
  assert.equal(layer.redraws, 1);
  assert.match(p.note(), /5 chunks/);
});

test('before a count there is no record and nothing is drawn, a past day and the login take it away, and a server without it has no row', async () => {
  const p = await load({ listings: { overworld: listingOf({ offered: false, version: '', missing: 0 }) } });
  await p.press();
  assert.equal(p.state.layers.length, 0);
  assert.match(p.note(), /No record/);

  const q = await load();
  await q.press();
  q.state.day = '2026-10-01';
  await q.view();
  assert.ok(q.state.removed.includes(q.layer()));
  q.state.day = '';
  q.doc.body.classList.add('locked');
  await q.view();
  assert.equal(q.live().length, 0);

  const off = await load({ status: 404 });
  assert.equal(off.find('Overlays', 'Lost chunks'), null);
  await off.view();
  assert.equal(off.asked().length, 1, 'a server without it was asked again');
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
