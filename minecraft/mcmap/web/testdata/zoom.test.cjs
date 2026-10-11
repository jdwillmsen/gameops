'use strict';

// What a zoom is said to be, and how a place with one is kept: the scale in
// words at every zoom from the farthest out to the deepest, what a viewer
// may type, and a view's place read back with a fractional zoom or an old
// whole one. Run by `go test ./web/` where there is a node, or by hand:
// `node web/testdata/zoom.test.cjs`.
const assert = require('node:assert/strict');
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const { page } = require('./dom.cjs');

const tests = [];
const test = (name, fn) => tests.push([name, fn]);

function model() {
  const win = { document: { getElementById: () => null } };
  win.window = win;
  vm.createContext(win);
  vm.runInContext(fs.readFileSync(path.join(__dirname, '..', 'zoom.js'), 'utf8'), win, { filename: 'zoom.js' });
  return win.mcmapZoom;
}

test('the scale is told in blocks and pixels at every zoom, whole or not', () => {
  const z = model();
  const want = [
    [-6, '1 px = 64 blocks'], [-4.5, '1 px = 22.6 blocks'], [-2, '1 px = 4 blocks'], [-1, '1 px = 2 blocks'],
    [-0.5, '1 px = 1.41 blocks'], [0, '1 block = 1 px'], [0.004, '1 block = 1 px'], [-0.004, '1 block = 1 px'],
    [0.5, '1 block = 1.41 px'], [1, '1 block = 2 px'], [3, '1 block = 8 px'], [3.5, '1 block = 11.3 px'],
    [6, '1 block = 64 px'], [7, '1 block = 128 px'], [7.5, '1 block = 181 px'],
  ];
  for (const [zoom, text] of want) assert.equal(z.scale(zoom), text, `zoom ${zoom}`);
});

test('the level has at most two decimals and no trailing zeros', () => {
  const z = model();
  assert.equal(z.level(3), '3');
  assert.equal(z.level(2.5), '2.5');
  assert.equal(z.level(2.3666), '2.37');
  assert.equal(z.level(-6), '-6');
});

test('blocks across follows the width and the zoom', () => {
  const z = model();
  assert.equal(z.across(0, 960), '960 blocks across');
  assert.equal(z.across(7, 960), '7.5 blocks across');
  assert.equal(z.across(-4, 1000), '16,000 blocks across');
  assert.equal(z.across(0, 0), '');
});

test('a viewer may type a level, a percentage or a scale', () => {
  const z = model();
  const near = (text, want) => { const got = z.parse(text); assert.ok(got !== null && Math.abs(got - want) < 1e-9, `${text}: ${got}`); };
  near('3.5', 3.5);
  near('-2', -2);
  near('zoom 4', 4);
  near('z:2.5', 2.5);
  near('100%', 0);
  near('800%', 3);
  near('8 px', 3);
  near('1 block = 8 px', 3);
  near('64px', 6);
  near('1 px = 4 blocks', -2);
  near('4 blocks per px', -2);
  for (const bad of ['', 'abc', '0%', '0 px', '-4 px', '1:8', '3..5', 'x'.repeat(41)]) assert.equal(z.parse(bad), null, bad);
});

test('the stops fit a region and a chunk to the smaller side of the window', () => {
  const z = model();
  assert.equal(z.fitBlocks(512, 1000, 512), 0);
  assert.equal(z.fitBlocks(16, 360, 740), Math.log2(360 / 16));
  assert.equal(z.BLOCK_DETAIL, 6);
});

test('a place keeps a fractional zoom and still reads a whole one', () => {
  const { settings } = page(new Map(), { files: ['settings.js'] });
  const view = (zoom) => settings.check.view({ name: 'Here', layers: {}, place: { d: 'overworld', x: 10, z: -20, zoom } });
  assert.equal(view(5.37).place.zoom, 5.37);
  assert.equal(view(5.3712).place.zoom, 5.37);
  assert.equal(view(3).place.zoom, 3);
  assert.equal(view(-4.25).place.zoom, -4.25);
  assert.equal(view(7).place.zoom, 7);
  assert.equal(view(Infinity), null);
  assert.equal(view(99), null);
  assert.equal(view('3'), null);
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
