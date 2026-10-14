'use strict';

// What the markers' script draws while the map is on a day of its history,
// checked by doing it: the script is run on the page stage.cjs makes, with
// the live layer beside it, against a map that says which day it draws and
// what that day kept, and a server that answers as these tests tell it to.
// Which markers are on the map, what each row counts and lists, what was
// asked of the server, and what the card says of a named mob are read
// back. Run by `go test ./web/` where there is a node to run it with, or
// by hand: `node web/testdata/daymarkers.test.cjs`.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { stage } = require('./stage.cjs');

const tests = [];
const test = (name, fn) => tests.push([name, fn]);

const answer = (body, etag = '') => ({ ok: true, status: 200, headers: { get: (name) => (name === 'ETag' ? etag : null) }, json: async () => JSON.parse(JSON.stringify(body)) });
const refusal = (status) => ({ ok: false, status, headers: { get: () => null }, json: async () => ({}) });

const bed = (x, z, c = 'red') => ({ x, y: 64, z, c });
const chest = (x, z) => ({ x, y: 64, z, k: 'chest' });
const biscuit = (x, z) => ({ x, y: 64, z, k: 'cow', n: 'Biscuit', i: '50' });

// The world as it is: two beds, a chest and a named cow. And as it was on
// two days: one that kept its markers, when there was one bed, of another
// colour, and the cow stood elsewhere, and one from before any were kept.
const TODAY = { at: '2026-10-14T09:00:00Z', beds: [bed(1, 2), bed(3, 4)], containers: [chest(5, 6)], mobs: [biscuit(7, 8)], more: { beds: 0, containers: 0, mobs: 0 } };
const THEN = { at: '2026-10-12T00:20:00Z', beds: [bed(-10, -20, 'blue')], containers: [], mobs: [biscuit(-70, -80)], more: { beds: 0, containers: 4, mobs: 0 } };
const KEPT = { day: '2026-10-12', dimensions: [{ id: 'overworld', markers: true }, { id: 'nether' }] };
const BARE = { day: '2026-10-11', dimensions: [{ id: 'overworld' }] };
const MINE = { waypoints: [{ name: 'Home', dimension: 'overworld', x: 100, y: 70, z: 200 }], more: 0 };

// A page with the markers' script run on it. serve is what the server says
// to each address, by its start; an address it does not name is a 404.
async function load({ serve = {}, old = false } = {}) {
  const s = await stage();
  const { win, doc, app } = s;
  const state = { day: null, dimension: 'overworld', asked: [] };
  const answers = { 'api/markers?': () => answer(TODAY, '"t1"'), 'api/waypoints': () => answer(MINE), 'api/history/markers/2026-10-12?': () => answer(THEN), ...serve };
  const icons = win.fetch;
  win.fetch = async (address, ...rest) => {
    if (address.startsWith('api/icons')) return icons(address, ...rest);
    state.asked.push(address);
    const found = Object.keys(answers).find((start) => address.startsWith(start));
    const given = found ? answers[found](address) : refusal(404);
    if (given === 'offline') throw new Error('offline');
    return given;
  };
  app.dimension = () => state.dimension;
  app.go = (dimension, x, z) => s.map.setView([z, x], s.map.getZoom());
  app.past = {
    get: () => (state.day ? state.day.day : ''),
    ...(old ? {} : { markers: (id) => Boolean(state.day) && state.day.dimensions.some((d) => d.id === id && d.markers === true) }),
  };
  // Each row as the panel was last told it, and each kind's marks.
  const rows = new Map();
  const register = app.layers.register;
  app.layers.register = (spec) => {
    const row = register(spec);
    const seen = { count: null, items: [], note: '', there: true };
    rows.set(spec.id, seen);
    for (const [name, keep] of [['setCount', (n) => { seen.count = n; }], ['setItems', (list) => { seen.items = list; }], ['setNote', (text) => { seen.note = text; }], ['remove', () => { seen.there = false; }]]) {
      const was = row[name];
      row[name] = (...args) => { keep(...args); return was ? was.apply(row, args) : undefined; };
    }
    return row;
  };
  const groups = [];
  const group = win.L.featureGroup;
  win.L.featureGroup = () => { const made = group(); groups.push(made); return made; };
  vm.runInContext(fs.readFileSync(path.join(__dirname, '..', 'markers.js'), 'utf8'), win, { filename: 'markers.js' });
  win.L.featureGroup = group;

  // Longer than the script waits for a view to settle, which dom.cjs cuts
  // to a moment.
  const settle = async () => { await new Promise((done) => setTimeout(done, 30)); await s.settle(); };
  const view = async () => { doc.dispatchEvent({ type: 'mcmap:view' }); await settle(); };
  // The map is told a day, or now, and says so as the real one does.
  const choose = async (day) => { state.day = day; await view(); };
  // The marks of a kind that are on the map, as their data.
  const on = (kind) => groups.filter((g) => s.map.hasLayer(g)).flatMap((g) => g.getLayers()).filter((m) => m.options.kind === kind).map((m) => m.options.data);
  const spots = (kind) => on(kind).map((m) => `${m.x},${m.z}`).sort();
  const row = (kind) => (rows.has(kind) && rows.get(kind).there ? rows.get(kind) : null);
  const asked = (start) => state.asked.filter((address) => address.startsWith(start));
  await settle();
  return { ...s, state, view, choose, on, spots, row, asked, settle };
}

test('a day that kept its markers is drawn with them in place of today\'s, and now with today\'s again', async () => {
  const p = await load();
  assert.deepEqual(p.spots('beds'), ['1,2', '3,4']);
  assert.equal(p.row('beds').count, 2);
  assert.equal(p.app.markers.day(), '');

  await p.choose(KEPT);
  assert.deepEqual(p.asked('api/history/markers/'), ['api/history/markers/2026-10-12?dimension=overworld&s=1']);
  assert.deepEqual(p.spots('beds'), ['-10,-20']);
  assert.deepEqual(p.spots('containers'), []);
  assert.deepEqual(p.spots('mobs'), ['-70,-80']);
  assert.equal(p.row('beds').count, 1);
  assert.equal(p.row('containers').count, 0);
  assert.equal(p.row('mobs').count, 1);
  assert.deepEqual(Array.from(p.row('beds').items, (item) => item.id), ['blue'], 'the row lists what is drawn');
  assert.match(p.row('containers').note, /4 more/, 'what the day left out is the day\'s to say');
  assert.equal(p.app.markers.day(), '2026-10-12');
  assert.equal(p.app.markers.savedAt(), null, 'the day\'s time is not when today\'s snapshot was taken');

  // The day is asked for once however often the view announces itself.
  await p.view();
  await p.view();
  assert.equal(p.asked('api/history/markers/').length, 1);

  await p.choose(null);
  assert.deepEqual(p.spots('beds'), ['1,2', '3,4']);
  assert.deepEqual(p.spots('containers'), ['5,6']);
  assert.equal(p.row('beds').count, 2);
  assert.equal(p.row('containers').note, '');
  assert.equal(p.app.markers.day(), '');
  assert.equal(p.app.markers.savedAt(), Date.parse(TODAY.at));
});

test('a day that kept no markers is drawn with none, not with today\'s, and is not asked for any', async () => {
  const p = await load();
  await p.choose(BARE);
  for (const kind of ['beds', 'containers', 'mobs']) {
    assert.deepEqual(p.on(kind), [], `${kind} on a day that kept none`);
    assert.equal(p.row(kind).count, 0);
    assert.equal(p.row(kind).items.length, 0);
  }
  assert.deepEqual(p.asked('api/history/'), [], 'a request that could only be refused');
  assert.equal(p.app.markers.day(), '2026-10-11');
  assert.equal(p.app.markers.within({ minX: -100, maxX: 100, minZ: -100, maxZ: 100 }).mobs.length, 0);

  await p.choose(null);
  assert.deepEqual(p.spots('beds'), ['1,2', '3,4']);
  assert.equal(p.row('mobs').count, 1);
});

test('a dimension the day kept no markers for is drawn with none, whatever its other dimensions kept', async () => {
  const p = await load();
  await p.choose(KEPT);
  assert.deepEqual(p.spots('beds'), ['-10,-20']);
  p.state.dimension = 'nether';
  await p.view();
  assert.deepEqual(p.on('beds'), []);
  assert.deepEqual(p.on('mobs'), []);
  assert.equal(p.asked('api/history/markers/').length, 1, 'only the overworld\'s were asked for');
  p.state.dimension = 'overworld';
  await p.view();
  assert.deepEqual(p.spots('beds'), ['-10,-20']);
  assert.equal(p.asked('api/history/markers/').length, 2);
});

test('stepping from one day to another never leaves the first day\'s markers under the second', async () => {
  const p = await load();
  await p.choose(KEPT);
  await p.choose(BARE);
  assert.deepEqual(p.on('beds'), []);
  await p.choose(KEPT);
  assert.deepEqual(p.spots('beds'), ['-10,-20']);
});

test('the player\'s own waypoints are as they are on every day', async () => {
  const p = await load();
  assert.deepEqual(p.spots('waypoints'), ['100,200']);
  for (const day of [KEPT, BARE, null]) {
    await p.choose(day);
    assert.deepEqual(p.spots('waypoints'), ['100,200']);
    assert.equal(p.row('waypoints').count, 1);
  }
});

test('a named mob of a day is where it was saved then, and is never the one loaded now', async () => {
  const p = await load();
  const loaded = { players: [], mobs: [{ i: '50', t: 'cow', n: 'Biscuit', x: 40, y: 64, z: 50 }] };
  await p.send(loaded);
  // Today the snapshot's mark steps aside for the live marker of the same
  // animal, which is what a day's mark must not do.
  assert.deepEqual(p.on('mobs'), [], 'today the loaded mob is drawn by the live layer alone');
  assert.match(p.row('mobs').items[0].detail, /loaded/);

  await p.choose(KEPT);
  await p.send(loaded);
  assert.deepEqual(p.spots('mobs'), ['-70,-80'], 'the day\'s mark stays on the map beside the live marker');
  assert.match(p.row('mobs').items[0].detail, /saved/);
  assert.doesNotMatch(p.row('mobs').items[0].detail, /loaded/);

  const [found] = p.app.markers.within({ minX: -100, maxX: 0, minZ: -100, maxZ: 0 }).mobs;
  found.open();
  await p.settle();
  assert.match(p.app.inspect.key(), /^s:/, 'the card is about a saved place, not about the mob with that id');
  assert.equal(p.doc.getElementById('inspect-follow').disabled, true);
  assert.match(p.doc.getElementById('inspect-what').textContent, /2026-10-12/);
  assert.match(p.doc.getElementById('inspect-what').textContent, /not where it is now/);
  assert.deepEqual(p.map.went.at(-1).centre, { lat: -79.5, lng: -69.5 }, 'the map goes to where it was saved, not to where it is');

  // The live picture moving on changes nothing of it.
  await p.send({ players: [], mobs: [{ i: '50', t: 'cow', n: 'Biscuit', x: 41, y: 64, z: 50 }] });
  assert.match(p.app.inspect.key(), /^s:/);
  assert.equal(p.doc.getElementById('inspect-follow').disabled, true);
  assert.deepEqual(p.spots('mobs'), ['-70,-80']);

  // Back on now the card about that day's place goes with the day.
  await p.choose(null);
  assert.equal(p.app.inspect.key(), null);
  await p.send(loaded);
  assert.deepEqual(p.on('mobs'), []);
  assert.match(p.row('mobs').items[0].detail, /loaded/);
});

test('a day whose markers do not come is drawn with none, and they are asked for again', async () => {
  let given = () => refusal(500);
  const p = await load({ serve: { 'api/history/markers/2026-10-12?': () => given() } });
  await p.choose(KEPT);
  assert.deepEqual(p.on('beds'), [], 'today\'s must not stand in for the day\'s');
  assert.deepEqual(p.on('mobs'), []);
  assert.equal(p.row('beds').count, null);
  given = () => 'offline';
  await p.view();
  assert.deepEqual(p.on('beds'), []);
  given = () => answer(THEN);
  await p.view();
  assert.deepEqual(p.spots('beds'), ['-10,-20']);
  assert.equal(p.asked('api/history/markers/').length, 3);
});

test('a server that reads no markers now still shows the ones a day kept', async () => {
  const p = await load({ serve: { 'api/markers?': () => refusal(404) } });
  assert.equal(p.row('beds'), null);
  await p.choose(KEPT);
  assert.deepEqual(p.spots('beds'), ['-10,-20']);
  assert.equal(p.row('beds').count, 1);
  await p.choose(BARE);
  assert.deepEqual(p.on('beds'), []);
  await p.choose(null);
  assert.deepEqual(p.on('beds'), []);
  assert.equal(p.row('beds'), null);
});

test('on a map from before it could say what a day kept, the markers are today\'s as they were', async () => {
  const p = await load({ old: true });
  await p.choose(KEPT);
  assert.deepEqual(p.spots('beds'), ['1,2', '3,4']);
  assert.deepEqual(p.asked('api/history/'), []);
  assert.equal(p.app.markers.day(), '');
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
