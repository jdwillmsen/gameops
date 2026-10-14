'use strict';

// What the height bands do, checked by doing it: the panel's script, the
// bands' and the live layer's are run on the page stage.cjs makes, frames
// are sent as the server sends them, and which markers are on the map, what
// the panel counts and what was kept are read back. The bands themselves
// are checked against worked examples written out here, not against the
// script's own tables. Run by `go test ./web/` where there is a node to run
// it with, or by hand: `node web/testdata/heights.test.cjs`.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { stage } = require('./stage.cjs');

const tests = [];
const test = (name, fn) => tests.push([name, fn]);
const same = (got, want) => assert.equal(JSON.stringify(got), JSON.stringify(want));
const run = (win, file) => vm.runInContext(fs.readFileSync(path.join(__dirname, '..', file), 'utf8'), win, { filename: file });

// A page with the bands and the live layer on it. The panel's rows are
// kept as they were last told, as the panel keeps them.
async function load({ storage = new Map(), look = {}, dimension = 'overworld', me = 'Steve', world = null, days = {} } = {}) {
  const s = await stage({ storage, look, files: ['names.js', 'duration.js', 'groups.js', 'icons.js', 'layers.js', 'room.js'] });
  const { win, app } = s;
  const state = { dimension };
  app.dimension = () => state.dimension;
  const rows = new Map();
  const register = app.layers.register;
  app.layers.register = (spec) => {
    const row = register(spec);
    const seen = { items: [], note: '', available: true, count: null };
    rows.set(spec.id, seen);
    for (const [name, keep] of [['setItems', (list) => { seen.items = list; }], ['setNote', (text) => { seen.note = text; }], ['setAvailable', (on) => { seen.available = on; }], ['setCount', (n) => { seen.count = n; }]]) {
      const was = row[name];
      row[name] = (...args) => { keep(...args); return was.apply(row, args); };
    }
    return row;
  };
  const fetched = win.fetch;
  win.fetch = async (address, ...rest) => {
    if (address === 'api/me') return { ok: true, status: 200, json: async () => ({ gamertag: me }) };
    return fetched(address, ...rest);
  };
  const past = { day: '' };
  if (world) {
    const answered = win.fetch;
    const json = (body) => ({ ok: true, status: 200, headers: { get: () => null }, json: async () => JSON.parse(JSON.stringify(body)) });
    win.fetch = async (address, ...rest) => {
      if (address.startsWith('api/markers?')) return json(world);
      if (address.startsWith('api/history/markers/')) return json(days[address.split('?')[0].split('/').pop()]);
      if (address.startsWith('api/waypoints')) return { ok: false, status: 404, headers: { get: () => null }, json: async () => ({}) };
      return answered(address, ...rest);
    };
    app.past = { get: () => past.day, markers: () => true };
  }
  run(win, 'heights.js');
  run(win, 'live.js');
  if (world) run(win, 'markers.js');
  const wait = async () => { await new Promise((done) => setTimeout(done, 30)); await s.settle(); };
  await wait();

  // The panel's lines, pressed as a viewer would.
  const tree = () => win.document.getElementById('layers-body').querySelector('.tree');
  const nameOf = (li) => {
    const row = li.children.find((child) => child.classList.contains('row'));
    const name = row ? row.children.find((child) => child.classList.contains('name')) : null;
    return name ? name.textContent : '';
  };
  const kidsOf = (li) => li.children.find((child) => child.classList.contains('kids'));
  const li = (...labels) => {
    let list = tree();
    let at = null;
    for (const label of labels) {
      at = list.children.find((child) => child.classList.contains('node') && nameOf(child) === label) || null;
      assert.ok(at, `there is no line ${labels.join(' > ')}`);
      list = kidsOf(at);
    }
    return at;
  };
  const part = (labels, name) => li(...labels).children.find((child) => child.classList.contains('row')).children.find((child) => child.classList.contains(name));
  const press = async (labels, name = 'check') => {
    part(labels, name).click();
    await wait();
  };
  const bands = ['Height', 'Filter by height'];
  const open = async () => {
    if (part(bands, 'twist').getAttribute('aria-expanded') !== 'true') await press(bands, 'twist');
  };
  const toggleBand = async (label) => {
    await open();
    await press([...bands, label]);
  };
  const state_ = (...labels) => part(labels, 'check').getAttribute('aria-checked');
  const marks = () => [...s.map.layers].flatMap((layer) => (layer.getLayers ? layer.getLayers() : []));
  return { ...s, state, past, rows, wait, press, toggleBand, open, bands, state_, marks, h: app.heights, layers: app.layers };
}

const HEIGHTS = ['sky', 'surface', 'shallow', 'deep', 'deepslate'];

// --- the bands, against worked examples ------------------------------------------

test('over a surface of 70 a block is in the band that its height above or below it says', async () => {
  const { h } = await load();
  for (const [y, want] of [
    [75, 'surface'], [78, 'surface'], [79, 'sky'], [200, 'sky'],
    [70, 'surface'], [62, 'surface'], [61, 'shallow'], [38, 'shallow'], [37, 'deep'],
    [0, 'deep'], [-1, 'deepslate'], [-10, 'deepslate'], [-64, 'deepslate'],
  ]) {
    assert.equal(h.bandOf('overworld', y, 70), want, `y ${y} over a surface of 70`);
  }
  // A block is in the one its feet are in: a mob at 75.9 stands in block 75.
  assert.equal(h.bandOf('overworld', 78.9, 70), 'surface');
  assert.equal(h.bandOf('overworld', 79, 70), 'sky');
});

test('under y 0 is deepslate whatever the surface, and above it a block with no surface under it has no band', async () => {
  const { h } = await load();
  for (const s of [undefined, null, NaN]) {
    assert.equal(h.bandOf('overworld', -10, s), 'deepslate');
    assert.equal(h.bandOf('overworld', 64, s), null);
    assert.equal(h.bandOf('overworld', 0, s), null);
  }
  // Within the margin of a surface that is itself under y 0 it is the surface.
  assert.equal(h.bandOf('overworld', -10, -5), 'surface');
  // A low surface leaves the shallow underground short, and no deep at all.
  assert.equal(h.bandOf('overworld', 5, 20), 'shallow');
  assert.equal(h.bandOf('overworld', -1, 20), 'deepslate');
  assert.equal(h.bandOf('overworld', 85, 20), 'sky');
});

test('the nether is cut at fixed heights and takes no notice of a surface, which is its roof', async () => {
  const { h } = await load();
  for (const s of [undefined, 127, 5]) {
    for (const [y, want] of [[200, 'upper'], [127, 'upper'], [96, 'upper'], [95, 'middle'], [32, 'middle'], [31, 'lava'], [5, 'lava'], [0, 'lava']]) {
      assert.equal(h.bandOf('nether', y, s), want, `y ${y}`);
    }
  }
});

test('the end is sky, surface and below an island, and has none where there is no island', async () => {
  const { h } = await load();
  for (const [y, want] of [[80, 'sky'], [69, 'sky'], [68, 'surface'], [52, 'surface'], [51, 'below'], [10, 'below']]) {
    assert.equal(h.bandOf('end', y, 60), want, `y ${y} over an island at 60`);
  }
  assert.equal(h.bandOf('end', 60, undefined), null);
});

test('every block over a known surface is in exactly one band, in every dimension that has surfaces', async () => {
  const { h } = await load();
  for (const dimension of ['overworld', 'end']) {
    for (const s of [-10, -3, 0, 7, 20, 31, 40, 63, 70, 150, 319]) {
      for (let y = -70; y <= 330; y += 1) {
        const got = h.bandsOf(dimension, y, y, s);
        assert.equal(got.length, 1, `${dimension} y ${y} over ${s}: ${got}`);
        assert.equal(got[0], h.bandOf(dimension, y, s));
      }
    }
  }
  for (let y = -10; y <= 300; y += 1) assert.equal(h.bandsOf('nether', y, y, undefined).length, 1);
});

test('a structure whose box spans bands is in each of them, and one with no height is in none', async () => {
  const { h } = await load();
  same(h.bandsOf('overworld', 70, 90, 70), ['sky', 'surface']);
  same(h.bandsOf('overworld', 10, 50, 70), ['shallow', 'deep']);
  same(h.bandsOf('overworld', -20, 20, 70), ['deep', 'deepslate']);
  same(h.bandsOf('overworld', 20, 90, 70), ['sky', 'surface', 'shallow', 'deep']);
  same(h.bandsOf('overworld', 39, 39, 70), ['shallow']);
  // The ends are inside, and a box given the wrong way up is the same box.
  same(h.bandsOf('overworld', 79, 79, 70), ['sky']);
  same(h.bandsOf('overworld', 90, 70, 70), ['sky', 'surface']);
  same(h.bandsOf('nether', 20, 100, undefined), ['upper', 'middle', 'lava']);
  // No Y is no band: a predicted structure, and a box whose edges are not numbers.
  same(h.bandsOf('overworld', undefined, undefined, 70), []);
  same(h.bandsOf('overworld', NaN, NaN, 70), []);
  // With no surface only what is under y 0 can be said.
  same(h.bandsOf('overworld', -5, 30, undefined), ['deepslate']);
  same(h.bandsOf('overworld', 5, 30, undefined), []);
  same(h.bandsOf('end', 5, 30, undefined), []);
});

// --- the panel -----------------------------------------------------------------------

test('the panel gets a section of its own with the bands, the near-my-height switch and the colouring, the last two off', async () => {
  const p = await load();
  assert.equal(p.state_('Height', 'Filter by height'), 'true');
  assert.equal(p.state_('Height', 'Near my height (±16)'), 'false');
  assert.equal(p.state_('Height', 'Colour by height'), 'false');
  assert.equal(p.rows.get('near').available, false, 'it is for a viewer who is online and seen live');
  assert.match(p.rows.get('near').note, /online/);
  same(p.rows.get('bands').items.map((item) => item.label), ['Sky', 'Surface', 'Shallow underground', 'Deep underground', 'Deepslate']);
  // Nothing is hidden by default.
  assert.ok(p.h.shows('overworld', -10, 70) && p.h.shows('overworld', 90, 70) && p.h.shows('overworld', 64, undefined));
  assert.equal(p.h.tint('overworld', 64, 70), '');
});

test('each dimension lists its own bands', async () => {
  const p = await load({ dimension: 'nether' });
  p.h.count('nothing', () => {});
  await p.wait();
  same(p.rows.get('bands').items.map((item) => item.label), ['Upper', 'Middle', 'Lava sea']);
  p.state.dimension = 'end';
  p.doc.dispatchEvent({ type: 'mcmap:view' });
  await p.wait();
  same(p.rows.get('bands').items.map((item) => item.label), ['Sky', 'Surface', 'Below the islands']);
});

test('a band switched off hides what is in it, and what has no known height is never hidden', async () => {
  const p = await load();
  await p.toggleBand('Sky');
  assert.equal(p.h.shows('overworld', 90, 70), false);
  assert.equal(p.h.shows('overworld', 75, 70), true);
  assert.equal(p.h.shows('overworld', 90, undefined), true, 'no surface, no band, no hiding');
  assert.equal(p.h.shows('overworld', NaN, NaN), true);
  // A box in two bands stays while either is shown, and goes when both are hidden.
  assert.equal(p.h.showsBox('overworld', 70, 90, 70), true);
  assert.equal(p.h.showsBox('overworld', 85, 95, 70), false);
  await p.toggleBand('Surface');
  assert.equal(p.h.showsBox('overworld', 70, 90, 70), false);
  assert.equal(p.h.showsBox('overworld', 60, 90, 70), true, 'it reaches the shallow underground too');
  assert.equal(p.h.showsBox('overworld', undefined, undefined, undefined), true);
  assert.equal(p.h.showsBox('overworld', NaN, NaN, 70), true);
  // Switching the row off is switching the filter off.
  await p.press(p.bands);
  assert.ok(p.h.shows('overworld', 90, 70) && p.h.showsBox('overworld', 70, 90, 70));
});

test('the panel counts what is in each band, a structure in every band it reaches, and says how many have no height', async () => {
  const p = await load();
  p.h.count('test', (add, addBox, dimension) => {
    assert.equal(dimension, 'overworld');
    for (const [y, s] of [[75, 70], [90, 70], [-10, 70], [50, 70], [10, 70], [64, undefined], [-3, undefined]]) add(y, s);
    addBox(70, 90, 70);
    addBox(undefined, undefined, 70);
    // Three predicted structures.
    add(NaN, NaN, 3);
  });
  await p.wait();
  const counts = Object.fromEntries(p.rows.get('bands').items.map((item) => [item.id, item.count]));
  same(counts, { sky: 2, surface: 2, shallow: 1, deep: 1, deepslate: 2 });
  assert.match(p.rows.get('bands').note, /^5 with no known height, always shown$/);
  // Counted again when a layer says its markers changed.
  p.h.count('test', (add) => add(75, 70));
  await p.wait();
  assert.equal(p.rows.get('bands').items.find((item) => item.id === 'surface').count, 1);
  assert.equal(p.rows.get('bands').note, '');
});

test('colouring is off until asked for, gives each band its own colour and the panel a legend', async () => {
  const p = await load();
  assert.ok(p.rows.get('bands').items.every((item) => !item.colour));
  await p.press(['Height', 'Colour by height']);
  const colours = HEIGHTS.map((_, at) => p.h.tint('overworld', [90, 70, 50, 10, -10][at], 70));
  assert.ok(colours.every((c) => /^#[0-9a-f]{6}$/i.test(c)), colours.join());
  assert.equal(new Set(colours).size, 5, 'a colour to a band');
  assert.equal(p.h.tint('overworld', 64, undefined), '', 'a marker of no known height is not tinted');
  same(p.rows.get('bands').items.map((item) => item.colour), colours);
  assert.notEqual(p.h.tint('nether', 100, undefined), '');
  await p.press(['Height', 'Colour by height']);
  assert.equal(p.h.tint('overworld', 90, 70), '');
});

test('near my height is within 16 blocks of the viewer and only while they are seen, in this dimension', async () => {
  const p = await load();
  p.h.viewer('overworld', 64.7);
  await p.wait();
  assert.equal(p.rows.get('near').available, true);
  assert.equal(p.rows.get('near').note, '', 'nothing to wait for now');
  await p.press(['Height', 'Near my height (±16)']);
  assert.equal(p.state_('Height', 'Near my height (±16)'), 'true');
  for (const [y, want] of [[80, true], [80.9, true], [81, false], [48, true], [47, false], [64, true]]) {
    assert.equal(p.h.shows('overworld', y, 70), want, `y ${y} for a viewer at 64`);
  }
  assert.equal(p.h.shows('overworld', NaN, NaN), true, 'no height, no hiding');
  assert.equal(p.h.showsBox('overworld', 70, 100, 70), true);
  assert.equal(p.h.showsBox('overworld', 90, 100, 70), false);
  assert.equal(p.h.showsBox('overworld', 10, 40, 70), false);
  assert.equal(p.h.showsBox('overworld', 10, 48, 70), true);
  // It moves with them.
  p.h.viewer('overworld', 100);
  assert.equal(p.h.shows('overworld', 80, 70), false);
  assert.equal(p.h.shows('overworld', 90, 70), true);
  // And is not in force without them, or from another dimension.
  p.h.viewer('overworld', null);
  assert.equal(p.rows.get('near').available, false);
  assert.equal(p.h.shows('overworld', 200, 70), true);
  p.h.viewer('nether', 40);
  assert.equal(p.h.shows('overworld', 200, 70), true);
});

test('the bands combine with near my height: a marker has to pass both', async () => {
  const p = await load();
  p.h.viewer('overworld', 70);
  await p.wait();
  await p.press(['Height', 'Near my height (±16)']);
  await p.toggleBand('Surface');
  assert.equal(p.h.shows('overworld', 70, 70), false, 'near but in a band that is off');
  assert.equal(p.h.shows('overworld', 85, 70), true, 'in the sky and near');
  assert.equal(p.h.shows('overworld', 100, 70), false, 'in the sky and not near');
});

// --- what is kept ------------------------------------------------------------------

test('the bands hidden and the colouring are kept, and a reload brings them back', async () => {
  const storage = new Map();
  const p = await load({ storage });
  await p.toggleBand('Deepslate');
  await p.press(['Height', 'Colour by height']);
  const kept = p.settings.get('layers');
  assert.equal(kept['heights/colour'], true);
  same(kept['heights#bands'].hidden, ['deepslate']);

  const q = await load({ storage });
  assert.equal(q.h.shows('overworld', -10, 70), false);
  assert.equal(q.h.shows('overworld', 75, 70), true);
  assert.notEqual(q.h.tint('overworld', 75, 70), '');
});

test('a saved view carries the bands and the colouring, and the built-in views put them back', async () => {
  const p = await load();
  const taken = {
    ...p.settings.view('everything'),
    layers: { ...p.settings.view('everything').layers, 'heights/colour': true },
    items: { ...p.settings.view('everything').items, 'heights#bands': { only: null, hidden: ['sky', 'deep'] } },
  };
  assert.ok(p.settings.check.view({ name: 'Down', layers: taken.layers, items: taken.items }), 'a view of them is a view');
  p.settings.adopt(taken);
  p.layers.adopt(Object.keys(taken.layers));
  await p.wait();
  assert.equal(p.h.shows('overworld', 90, 70), false);
  assert.equal(p.h.shows('overworld', 10, 70), false);
  assert.equal(p.h.shows('overworld', 75, 70), true);
  assert.notEqual(p.h.tint('overworld', 75, 70), '');
  assert.equal(p.state_('Height', 'Colour by height'), 'true');

  // The page's own views put every band back and the colouring away.
  for (const id of p.settings.BUILT_IN) {
    const view = p.settings.view(id);
    p.settings.adopt(taken);
    p.layers.adopt(Object.keys(taken.layers));
    p.settings.adopt(view);
    p.layers.adopt(Object.keys(view.layers));
    await p.wait();
    assert.ok(p.h.shows('overworld', 90, 70) && p.h.shows('overworld', 10, 70), `${id}: every band is back`);
    assert.equal(p.h.tint('overworld', 75, 70), '', `${id}: not coloured`);
  }
});

// --- the live layer -----------------------------------------------------------------

const mob = (i, y, s, extra = {}) => ({ i: String(i), t: 'zombie', x: 10 + i, y, z: 10, ...(s === undefined ? {} : { s }), ...extra });
const drawnMobs = (p) => p.marks().filter((m) => m.options.type === 'zombie' && !m.options.heap);

test('a live mob is on the map or off it by the band it is in, as it moves, and one of no known height is always there', async () => {
  const p = await load();
  await p.send({ mobs: [mob(1, 75, 70), mob(2, -10, 70), mob(3, 95, 70), mob(4, 64)] });
  assert.equal(drawnMobs(p).length, 4);
  await p.toggleBand('Deepslate');
  assert.deepEqual(drawnMobs(p).map((m) => m.getLatLng().lng).sort((a, b) => a - b), [11, 13, 14]);
  await p.toggleBand('Sky');
  assert.deepEqual(drawnMobs(p).map((m) => m.getLatLng().lng).sort((a, b) => a - b), [11, 14]);
  // The mob that was in the sky comes down to the surface in the next frame, and is drawn.
  await p.send({ mobs: [mob(1, 75, 70), mob(2, -10, 70), mob(3, 72, 70), mob(4, 64)] });
  assert.deepEqual(drawnMobs(p).map((m) => m.getLatLng().lng).sort((a, b) => a - b), [11, 13, 14]);
  // And the one at the surface digs down past the margin, and is not.
  await p.send({ mobs: [mob(1, 40, 70), mob(2, -10, 70), mob(3, 72, 70), mob(4, 64)] });
  assert.deepEqual(drawnMobs(p).map((m) => m.getLatLng().lng).sort((a, b) => a - b), [11, 13, 14], 'shallow underground is still shown');
  await p.toggleBand('Shallow underground');
  assert.deepEqual(drawnMobs(p).map((m) => m.getLatLng().lng).sort((a, b) => a - b), [13, 14]);
});

test('the panel counts the live mobs and players by band', async () => {
  const p = await load();
  await p.send({ players: [{ i: 'p1', n: 'Steve', x: 0, y: 66, z: 0, r: 0, s: 66 }], mobs: [mob(1, 75, 70), mob(2, -10, 70), mob(3, 95, 70), mob(4, 64), mob(5, 20, 70)] });
  await p.wait();
  const counts = Object.fromEntries(p.rows.get('bands').items.map((item) => [item.id, item.count]));
  same(counts, { sky: 1, surface: 2, shallow: 0, deep: 1, deepslate: 1 });
  assert.match(p.rows.get('bands').note, /^1 with no known height/);
});

test('players are hidden by band too, and near my height follows the viewer up and down', async () => {
  const p = await load();
  const steve = (y, s = 70) => ({ i: 'p1', n: 'Steve', x: 0, y, z: 0, r: 0, s });
  const alex = (y, s = 70) => ({ i: 'p2', n: 'Alex', x: 5, y, z: 5, r: 0, s });
  const players = () => p.marks().filter((m) => m.options.yaw !== undefined);
  await p.send({ players: [steve(70), alex(40)], mobs: [mob(1, 75, 70), mob(2, 20, 70), mob(3, 60, 70)] });
  assert.equal(players().length, 2);
  await p.toggleBand('Shallow underground');
  assert.equal(players().length, 1, 'the one in the shallow underground is off the map');
  await p.toggleBand('Shallow underground');
  assert.equal(players().length, 2);

  // The viewer is Steve, at 70: the mobs within 16 blocks of that.
  assert.equal(p.rows.get('near').available, true);
  await p.press(['Height', 'Near my height (±16)']);
  assert.deepEqual(drawnMobs(p).map((m) => m.getLatLng().lng).sort((a, b) => a - b), [11, 13]);
  assert.equal(players().length, 1, 'Alex is 30 blocks down');
  // Steve goes down a mine, and what is near is what is round him there.
  await p.send({ players: [steve(25), alex(40)], mobs: [mob(1, 75, 70), mob(2, 20, 70), mob(3, 60, 70)] });
  assert.deepEqual(drawnMobs(p).map((m) => m.getLatLng().lng).sort((a, b) => a - b), [12]);
  assert.equal(players().length, 2);
  // Off the map, or in the picture of another dimension, he is no longer a measure.
  await p.send({ players: [alex(40)], mobs: [mob(1, 75, 70), mob(2, 20, 70)] });
  assert.equal(p.rows.get('near').available, false);
  assert.equal(drawnMobs(p).length, 2);
});

test('a group of crowded mobs holds only those at the heights shown', async () => {
  const p = await load({ look: { group: 'on' } });
  await p.zoomTo(-2);
  const herd = [...Array.from({ length: 6 }, (_, i) => mob(100 + i, 72, 70, { x: 10 + i })), ...Array.from({ length: 4 }, (_, i) => mob(200 + i, 120, 70, { x: 20 + i }))];
  await p.send({ mobs: herd });
  const heap = () => p.marks().find((m) => m.options.heap);
  assert.equal(heap().options.heap.members.length, 10);
  await p.toggleBand('Sky');
  assert.equal(heap().options.heap.members.length, 6);
  assert.ok(heap().options.heap.members.every((m) => m.id.startsWith('m:10')));
  // Coloured, the same group, not one mark more.
  await p.press(['Height', 'Colour by height']);
  assert.equal(p.marks().filter((m) => m.options.heap).length, 1);
  assert.equal(heap().options.heap.members.length, 6);
  // Nothing of any band left but those with no height: they stay, and are a group of their own.
  await p.toggleBand('Surface');
  assert.equal(p.marks().filter((m) => m.options.heap).length, 0);
  await p.send({ mobs: [...herd, ...Array.from({ length: 3 }, (_, i) => mob(300 + i, 64, undefined, { x: 12 + i }))] });
  assert.equal(heap().options.heap.members.length, 3);
});

test('colouring rings the marker of a mob and a player in the colour of its band, and takes it off again', async () => {
  const p = await load();
  await p.send({ players: [{ i: 'p1', n: 'Steve', x: 0, y: 66, z: 0, r: 0, s: 66 }], mobs: [mob(1, 75, 70), mob(2, -10, 70), mob(3, 64)] });
  const ring = (m) => m.options.band || '';
  assert.deepEqual(p.marks().map(ring).filter(Boolean), []);
  await p.press(['Height', 'Colour by height']);
  const colour = (id) => p.h.bands('overworld').find((band) => band.id === id).colour;
  const byX = (x) => p.marks().find((m) => m.getLatLng().lng === x);
  assert.equal(ring(byX(11)), colour('surface'));
  assert.equal(ring(byX(12)), colour('deepslate'));
  assert.equal(ring(byX(13)), '', 'no known height, no ring');
  assert.equal(ring(p.marks().find((m) => m.options.yaw !== undefined)), colour('surface'), 'the player');
  // A mob changing band changes its ring in the next frame.
  await p.send({ players: [], mobs: [mob(1, 120, 70)] });
  assert.equal(ring(byX(11)), colour('sky'));
  await p.press(['Height', 'Colour by height']);
  assert.equal(ring(byX(11)), '');
});

// --- the markers the world holds -------------------------------------------------------

const WORLD = {
  at: '2026-10-14T09:00:00Z',
  beds: [{ x: 1, y: 72, z: 1, s: 70, c: 'red' }, { x: 2, y: 100, z: 2, s: 70, c: 'red' }, { x: 3, y: 64, z: 3, c: 'red' }],
  containers: [{ x: 4, y: -20, z: 4, s: 70, k: 'chest' }, { x: 5, y: 45, z: 5, s: 70, k: 'chest' }, { x: 6, y: 10, z: 6, s: 70, k: 'barrel' }],
  mobs: [{ x: 7, y: -30, z: 7, s: 70, k: 'cat', n: 'Tango', i: '9' }, { x: 8, y: 70, z: 8, s: 70, k: 'cat', n: 'Miso', i: '10' }],
  more: { beds: 0, containers: 0, mobs: 0 },
};
const placed = (p, kind) => p.marks().filter((m) => m.options.kind === kind).map((m) => m.options.data.x).sort((a, b) => a - b);

test('beds, containers and saved named mobs are on the map or off it by their band, and one of no known height is always there', async () => {
  const p = await load({ world: WORLD });
  assert.deepEqual(placed(p, 'beds'), [1, 2, 3]);
  await p.toggleBand('Sky');
  assert.deepEqual(placed(p, 'beds'), [1, 3], 'the bed at 100 over a surface of 70 is in the sky');
  await p.toggleBand('Deepslate');
  assert.deepEqual(placed(p, 'containers'), [5, 6]);
  assert.deepEqual(placed(p, 'mobs'), [8]);
  await p.toggleBand('Deep underground');
  assert.deepEqual(placed(p, 'containers'), [5]);
  await p.toggleBand('Deep underground');
  await p.toggleBand('Deepslate');
  assert.deepEqual(placed(p, 'containers'), [4, 5, 6]);
  assert.deepEqual(placed(p, 'mobs'), [7, 8]);
});

test('the markers of the world are counted by band beside the live ones, and with the same ring', async () => {
  const p = await load({ world: WORLD });
  await p.send({ mobs: [mob(1, 75, 70)] });
  await p.wait();
  const counts = Object.fromEntries(p.rows.get('bands').items.map((item) => [item.id, item.count]));
  same(counts, { sky: 1, surface: 3, shallow: 1, deep: 1, deepslate: 2 });
  assert.match(p.rows.get('bands').note, /^1 with no known height/);
  await p.press(['Height', 'Colour by height']);
  const colour = (id) => p.h.bands('overworld').find((band) => band.id === id).colour;
  const ring = (kind, x) => p.marks().find((m) => m.options.kind === kind && m.options.data.x === x).options.band;
  assert.equal(ring('beds', 1), colour('surface'));
  assert.equal(ring('beds', 2), colour('sky'));
  assert.equal(ring('beds', 3), '');
  assert.equal(ring('containers', 6), colour('deep'));
  assert.equal(ring('mobs', 7), colour('deepslate'));
});

test('a day\'s markers are sorted by band as today\'s are', async () => {
  const then = { at: '2026-10-12T00:20:00Z', beds: [{ x: -10, y: 120, z: -20, s: 70, c: 'blue' }, { x: -11, y: 66, z: -21, s: 70, c: 'blue' }], containers: [], mobs: [], more: { beds: 0, containers: 0, mobs: 0 } };
  const p = await load({ world: WORLD, days: { '2026-10-12': then } });
  await p.toggleBand('Sky');
  assert.deepEqual(placed(p, 'beds'), [1, 3]);
  p.past.day = '2026-10-12';
  p.doc.dispatchEvent({ type: 'mcmap:view' });
  await p.wait();
  assert.deepEqual(placed(p, 'beds'), [-11], 'the bed of that day in the sky is off the map too');
  const counts = Object.fromEntries(p.rows.get('bands').items.map((item) => [item.id, item.count]));
  assert.equal(counts.sky, 1);
  assert.equal(counts.surface, 1);
});

test('a group of beds and containers holds only those at the heights shown', async () => {
  const p = await load({ world: WORLD, look: { group: 'on' } });
  await p.zoomTo(-2);
  await p.wait();
  const heap = () => p.marks().find((m) => m.options.heap);
  assert.equal(heap().options.heap.members.length, 6, 'every bed and container, as one mark');
  await p.toggleBand('Sky');
  await p.toggleBand('Deepslate');
  assert.equal(heap().options.heap.members.length, 4);
  await p.press(['Height', 'Colour by height']);
  assert.equal(heap().options.heap.members.length, 4);
});

// --- the ring, in every style --------------------------------------------------------

// A marker of the script's own class, drawn as Leaflet would draw it.
function drawn(s, Class, sprite, options = {}) {
  const renderer = s.L.canvas({});
  renderer._drawing = true;
  renderer._steady = 1;
  const marker = new Class([0, 0], { renderer, radius: 12, sprite, ...options });
  marker._point = s.L.point(300, 200);
  marker._updatePath();
  return renderer._ctx;
}

test('the ring is round a dot, a plate and a large picture alike, in the band\'s colour, and only when there is a band', async () => {
  for (const style of ['dots', 'plates', 'large']) {
    const s = await stage({ look: { style } });
    s.icons.mob('cow', '#f00');
    await s.settle();
    const sprite = s.icons.mob('cow', '#f00');
    assert.equal(Boolean(sprite), style !== 'dots', style);
    for (const Class of [s.icons.Stamped, s.icons.Tagged]) {
      const plain = drawn(s, Class, sprite);
      const ringed = drawn(s, Class, sprite, { band: '#8a5cc9' });
      const extra = ringed.arcs.filter((a) => Math.abs(a.r - 14.5) < 0.01 && a.x === 300 && a.y === 200);
      assert.equal(extra.length, 1, `${style}: one ring, round the marker's own point and clear of its edge`);
      assert.equal(plain.arcs.filter((a) => Math.abs(a.r - 14.5) < 0.01).length, 0, `${style}: none without a band`);
      assert.equal(ringed.strokeStyle, '#8a5cc9', `${style}: the last stroke is the band's colour`);
    }
  }
});

test('the ring keeps its size through a zoom as the marker does', async () => {
  const s = await stage({ look: { style: 'plates' } });
  for (const stretch of [2, 0.5, 4]) {
    const renderer = s.L.canvas({});
    renderer._drawing = true;
    renderer._steady = 1 / stretch;
    const marker = new s.icons.Stamped([0, 0], { renderer, radius: 12, sprite: null, band: '#fff' });
    marker._point = s.L.point(300, 200);
    marker._updatePath();
    const ring = renderer._ctx.arcs.filter((a) => Math.abs(a.r * stretch - 14.5) < 0.01);
    assert.equal(ring.length, 1, `stretched ${stretch}`);
  }
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
