'use strict';

// What the footer says of the ground under the pointer, checked by doing
// it: the biomes' script is run in a page of the kind dom.cjs makes, beside
// the panel's own script, with a map that only remembers its listeners and
// a server that answers as these tests tell it to. Run by `go test ./web/`
// where there is a node to run it with, or by hand:
// `node web/testdata/biomes.test.cjs`.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { page } = require('./dom.cjs');

const tests = [];
const test = (name, fn) => tests.push([name, fn]);
const wait = (ms = 30) => new Promise((done) => setTimeout(done, ms));

const LISTING = {
  extracted: true,
  version: 'abc',
  biomes: [{ name: 'plains', label: 'Plains', color: '#8db360', area: 10 }],
  tiles: { minZoom: -6, maxZoom: 2, size: 256 },
};

// A page with the panel, the biomes' script, and a server whose biome
// lookup is answered by at(address).
async function load({ dimension = 'overworld', at, listing = LISTING } = {}) {
  const p = page(new Map());
  const { win, doc } = p;
  const output = doc.createElement('output');
  output.hidden = true;
  const lookup = doc.getElementById;
  doc.getElementById = (id) => (id === 'biome' ? output : lookup(id));
  const heard = new Map();
  const state = { dimension, asked: [] };
  win.L = {
    TileLayer: {
      extend: () => class {
        constructor(url, options) { this.options = options; }
        addTo() { return this; }
        setOpacity() {}
        redraw() {}
      },
    },
  };
  Object.assign(win.mcmap, {
    map: { on: (type, fn) => heard.set(type, fn), removeLayer() {} },
    dimension: () => state.dimension,
    extent: () => null,
  });
  win.AbortController = AbortController;
  win.setInterval = () => 0;
  win.fetch = async (address) => {
    state.asked.push(address);
    const body = address.startsWith('api/biomes/at?') ? at(address) : listing;
    return { ok: true, status: 200, json: async () => JSON.parse(JSON.stringify(body)) };
  };
  vm.runInContext(fs.readFileSync(path.join(__dirname, '..', 'biomes.js'), 'utf8'), win, { filename: 'biomes.js' });
  await wait();
  const rest = (x, z) => { heard.get('mousemove')({ latlng: { lat: z, lng: x } }); return wait(); };
  const asked = () => state.asked.filter((a) => a.startsWith('api/biomes/at?'));
  return { ...p, output, state, rest, asked, show: () => win.mcmap.biomes.show('plains'), click: (x, z) => { heard.get('click')({ latlng: { lat: z, lng: x } }); return wait(); } };
}

const plains = { generated: true, biome: { name: 'plains', label: 'Plains' } };

test('the biome is followed by the height of the surface where the server gives one', async () => {
  const p = await load({ at: () => ({ ...plains, surface: 71 }) });
  assert.equal(p.output.hidden, true, 'the readout is there with the overlay off');
  assert.equal(p.show(), true);
  await p.rest(10.7, -3.2);
  assert.equal(p.output.hidden, false);
  assert.equal(p.output.textContent, 'Biome: Plains · surface 71');
  // Whole blocks, as the biome is asked for.
  assert.match(p.asked()[0], /dimension=overworld&x=10&z=-4$/);
});

test('a surface below zero is said as it is, and sea level is not mistaken for nothing', async () => {
  const p = await load({ at: (a) => ({ ...plains, surface: /x=1&/.test(a) ? -64 : 0 }) });
  p.show();
  await p.rest(1, 1);
  assert.equal(p.output.textContent, 'Biome: Plains · surface -64');
  await p.rest(2, 1);
  assert.equal(p.output.textContent, 'Biome: Plains · surface 0');
});

test('the biome alone is said while the server has no heights, or sends what is no height', async () => {
  for (const surface of [undefined, null, '71', 71.5, Number.NaN, {}, [71]]) {
    const p = await load({ at: () => ({ ...plains, ...(surface === undefined ? {} : { surface }) }) });
    p.show();
    await p.rest(5, 5);
    assert.equal(p.output.textContent, 'Biome: Plains', `for ${JSON.stringify(surface)}`);
  }
});

test('nothing is said of a surface where nothing is generated', async () => {
  const p = await load({ at: () => ({ generated: false, surface: 70 }) });
  p.show();
  await p.rest(5, 5);
  assert.equal(p.output.textContent, 'Biome: not generated here');
});

test('every dimension is asked about by its own name, and a tap asks at once', async () => {
  for (const dimension of ['overworld', 'nether', 'end']) {
    const p = await load({ dimension, at: () => ({ ...plains, surface: 12 }) });
    p.show();
    await p.click(3, 4);
    assert.match(p.asked()[0], new RegExp(`dimension=${dimension}&x=3&z=4$`));
    assert.equal(p.output.textContent, 'Biome: Plains · surface 12');
  }
});

test('with the overlay off nothing is asked and nothing is said', async () => {
  const p = await load({ at: () => ({ ...plains, surface: 71 }) });
  await p.rest(5, 5);
  assert.equal(p.asked().length, 0);
  assert.equal(p.output.textContent, '');
  assert.equal(p.output.hidden, true);
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
