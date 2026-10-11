'use strict';

// What the structures' script does, checked by doing it: the script is run
// in a page of the kind dom.cjs makes, beside the panel's own script, with
// a map that only remembers what was put on it and a server that answers
// as these tests tell it to. Run by `go test ./web/` where there is a node
// to run it with, or by hand: `node web/testdata/structures.test.cjs`.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { page } = require('./dom.cjs');

const same = (got, want) => assert.equal(JSON.stringify(got), JSON.stringify(want));
const tick = () => new Promise((done) => setTimeout(done, 0));
const tests = [];
const test = (name, fn) => tests.push([name, fn]);

// Every kind a server with all of them lists, as it lists them.
const CATALOG = [
  { kind: 'fortress', dimensions: ['nether'] },
  { kind: 'monument', dimensions: ['overworld'] },
  { kind: 'village', dimensions: ['overworld'], quiet: true },
  { kind: 'stronghold', dimensions: ['overworld'], asked: true },
  { kind: 'ruined_portal', dimensions: ['overworld', 'nether'], quiet: true },
  { kind: 'bastion', dimensions: ['nether'], quiet: true },
  { kind: 'end_city', dimensions: ['end'], asked: true, quiet: true },
  { kind: 'end_gateway', dimensions: ['end'] },
];
const box = (x, z) => ({ minX: x, minY: 60, minZ: z, maxX: x + 10, maxY: 70, maxZ: z + 10 });
const WORLD = {
  overworld: {
    recorded: [{ kind: 'monument', ...box(0, 0), areas: 4 }, { kind: 'village', ...box(100, 0), village: { counted: false } }, { kind: 'village', ...box(200, 0), village: { counted: false } },
      { kind: 'stronghold', ...box(300, 0), evidence: 9 }, { kind: 'ruined_portal', ...box(400, 0), evidence: 1 }, { kind: 'ruined_portal', ...box(500, 0), evidence: 1 }],
    predicted: [{ kind: 'monument', x: 1000, z: 0, candidate: true }, { kind: 'ruined_portal', x: 1100, z: 0, generated: true, vacant: true }, { kind: 'village', x: 1200, z: 0, generated: true, vacant: true },
      { kind: 'village', x: 1300, z: 0, candidate: true }],
    kinds: { monument: { state: 'verified', agree: 4, disagree: 0 } },
  },
  nether: {
    recorded: [{ kind: 'fortress', ...box(0, 0), areas: 9 }, { kind: 'bastion', ...box(100, 0), evidence: 5 }, { kind: 'ruined_portal', ...box(200, 0), evidence: 1 }],
    predicted: [{ kind: 'fortress', x: 1000, z: 0 }],
    kinds: {},
  },
  end: {
    recorded: [{ kind: 'end_city', ...box(0, 0), evidence: 30 }, { kind: 'end_gateway', ...box(100, 0), evidence: 1 }],
    predicted: [{ kind: 'end_city', x: 1000, z: 0, candidate: true }],
    kinds: {},
  },
};

// A page with the panel, the structures' script and what that script asks
// of the rest of the page. answer(dimension) is what the server says.
async function load({ storage = new Map(), catalog = CATALOG, world = WORLD, dimension = 'overworld', answer } = {}) {
  const p = page(storage);
  const { win, doc } = p;
  const state = { dimension, asked: [], map: new Set() };
  const layer = () => {
    const group = { layers: [] };
    group.addTo = (to) => { if (to === win.mcmap.map) state.map.add(group); else to.layers.push(group); return group; };
    group.clearLayers = () => { group.layers = []; };
    group.eachLayer = (fn) => group.layers.forEach(fn);
    return group;
  };
  const drawn = (what) => {
    const thing = { ...what };
    thing.bindTooltip = (label) => { thing.label = label; return thing; };
    thing.on = () => thing;
    thing.addTo = (group) => { group.layers.push(thing); return thing; };
    thing.getLatLng = () => thing.at;
    thing.getElement = () => null;
    return thing;
  };
  win.L = {
    layerGroup: layer,
    marker: (at, options) => drawn({ at, icon: options.icon }),
    rectangle: (bounds) => drawn({ bounds, getBounds: () => bounds }),
    divIcon: (options) => options,
    latLngBounds: () => ({ extend() {}, isValid: () => false }),
  };
  Object.assign(win.mcmap, {
    map: { hasLayer: (group) => state.map.has(group), removeLayer: (group) => state.map.delete(group), getCenter: () => ({ lat: 0, lng: 0 }), fitBounds() {} },
    icons: { picture: () => doc.createElement('span'), keyOf: (group, of) => `${group}/${of.kind}`, paint() {} },
    names: {
      structure: (kind) => kind.split('_').map((word) => word[0].toUpperCase() + word.slice(1)).join(' '),
      plural: (name) => `${name}s`, tidy: (id) => id, entity: (id) => id, container: (id) => id, kindOf: (id) => id,
    },
    dimension: () => state.dimension,
    label: (id) => id,
    go: () => true,
  });
  win.setInterval = () => 0;
  win.fetch = async (address) => {
    state.asked.push(address);
    const at = /dimension=([a-z]+)/.exec(address)[1];
    const given = answer ? answer(at, address) : undefined;
    if (given && given.status) return { ok: false, status: given.status };
    const body = given || { surveyed: true, prediction: 'verified', recorded: [], predicted: [], kinds: {}, ...world[at], ...(catalog ? { catalog } : {}) };
    return { ok: true, status: 200, json: async () => JSON.parse(JSON.stringify(body)) };
  };
  vm.runInContext(fs.readFileSync(path.join(__dirname, '..', 'structures.js'), 'utf8'), win, { filename: 'structures.js' });
  const settle = async () => { for (let i = 0; i < 6; i++) await tick(); await new Promise((done) => setTimeout(done, 10)); };
  await settle();
  const tree = () => doc.getElementById('layers-body').querySelector('.tree');
  const nameOf = (li) => {
    const row = li.children.find((child) => child.classList.contains('row'));
    const name = row ? row.children.find((child) => child.classList.contains('name')) : null;
    return name ? name.textContent : '';
  };
  const section = () => tree().children.find((li) => nameOf(li) === 'Structures');
  const lines = () => {
    const out = {};
    const kids = section().children.find((child) => child.classList.contains('kids'));
    for (const li of kids.children.filter((child) => child.classList.contains('node'))) {
      const row = li.children.find((child) => child.classList.contains('row'));
      const part = (name) => row.children.find((child) => child.classList.contains(name));
      out[nameOf(li)] = { count: part('count') ? part('count').textContent : '', on: part('check').getAttribute('aria-checked'), row, li };
    }
    return out;
  };
  const kinds = () => Object.keys(lines()).filter((name) => !['Known', 'Predicted', 'Possible'].includes(name));
  const press = async (name) => { lines()[name].row.children.find((child) => child.classList.contains('check')).click(); await settle(); };
  // What is on the map, by what each mark's tooltip says it is.
  const onMap = () => [...state.map].flatMap((group) => group.layers).filter((thing) => thing.label).map((thing) => thing.label.children[0].textContent).sort();
  const classes = () => [...state.map].flatMap((group) => group.layers).filter((thing) => thing.icon).map((thing) => `${thing.label.children[0].textContent} | ${thing.icon.className}`).sort();
  const go = async (to) => { state.dimension = to; doc.dispatchEvent({ type: 'mcmap:view' }); await settle(); };
  const noteOf = (name) => {
    const li = lines()[name].li;
    const find = (node) => (node.classList && node.classList.contains('note') ? node : (node.children || []).map(find).find(Boolean));
    const note = find(li);
    return note ? note.textContent : '';
  };
  return { ...p, state, lines, kinds, press, onMap, classes, go, noteOf, settle, structures: win.mcmap.structures, storage };
}

test('a dimension is listed with the kinds the server says it can hold, and its own counts', async () => {
  const p = await load();
  same(p.kinds(), ['Monuments', 'Villages', 'Strongholds', 'Ruined Portals']);
  same(['Monuments', 'Villages', 'Strongholds', 'Ruined Portals'].map((name) => p.lines()[name].count), ['1', '2', '1', '2']);
  assert.ok(p.state.asked[0].includes('dimension=overworld') && p.state.asked[0].includes('kinds=all'), p.state.asked[0]);
  await p.go('nether');
  same(p.kinds(), ['Fortresss', 'Ruined Portals', 'Bastions']);
  assert.equal(p.lines()['Ruined Portals'].count, '1', 'the portals of the Nether, not the overworld\'s');
  await p.go('end');
  same(p.kinds(), ['End Citys', 'End Gateways']);
});

test('a kind keeps its choice from one dimension to the next, and is drawn or not by it', async () => {
  const p = await load();
  await p.press('Ruined Portals');
  assert.ok(!p.onMap().some((label) => label.startsWith('Ruined Portal')), 'hidden in the overworld');
  await p.go('nether');
  assert.equal(p.lines()['Ruined Portals'].on, 'false');
  assert.ok(!p.onMap().some((label) => label.startsWith('Ruined Portal')), 'and so in the Nether');
  assert.ok(p.onMap().some((label) => label.startsWith('Bastion')));
  await p.go('overworld');
  assert.equal(p.lines()['Ruined Portals'].on, 'false');
  assert.equal(p.lines().Villages.on, 'true');
});

test('a kind that is off until asked for is off from the first answer, and asked for by its row alone', async () => {
  const p = await load({ dimension: 'end' });
  assert.equal(p.lines()['End Citys'].on, 'false');
  assert.ok(!p.onMap().some((label) => label.startsWith('End City')), `drawn before it was asked for: ${p.onMap()}`);
  assert.ok(p.onMap().some((label) => label.startsWith('End Gateway')));
  same(p.structures.asked(), []);
  await p.press('End Citys');
  assert.ok(p.onMap().some((label) => label.startsWith('End City · found by its blocks')));
  same(p.structures.asked(), ['end_city']);
  // What a link or a view says of it shows nothing the viewer has not asked for.
  const q = await load({ dimension: 'end' });
  q.settings.set('layers', { ...q.settings.get('layers'), 'structures#kinds': { only: 'end_city', hidden: [] } });
  q.layers.adopt([]);
  await q.settle();
  assert.ok(!q.onMap().some((label) => label.startsWith('End City')), `shown by a view's "only": ${q.onMap()}`);
  same(q.structures.asked(), []);
});

test('a kind the server does not list has no line and is not drawn, whatever else is sent', async () => {
  const withheld = CATALOG.filter((k) => k.kind !== 'ruined_portal');
  const p = await load({ catalog: withheld });
  same(p.kinds(), ['Monuments', 'Villages', 'Strongholds']);
  assert.ok(!p.onMap().some((label) => label.startsWith('Ruined Portal')), `drawn though it is not listed: ${p.onMap()}`);
  await p.go('nether');
  same(p.kinds(), ['Fortresss', 'Bastions']);
});

test('a server from before it listed its kinds is shown the seven there were then, in every dimension', async () => {
  const old = {
    overworld: { recorded: WORLD.overworld.recorded.filter((s) => s.kind !== 'ruined_portal'), predicted: [], kinds: {} },
    // A kind of a later server, sent with no word of which kinds there
    // are: it has no line to be put away by, as it never had.
    nether: { recorded: [WORLD.nether.recorded[0], WORLD.nether.recorded[1]], predicted: [], kinds: {} },
  };
  const p = await load({ catalog: null, world: old });
  same(p.kinds(), ['Monuments', 'Villages', 'Strongholds']);
  assert.equal(p.lines().Strongholds.on, 'false');
  await p.go('nether');
  // Its fortress, and the stronghold's line, which such a server's page always had.
  same(p.kinds(), ['Fortresss', 'Strongholds']);
});

test('the kinds a dimension has are known before the first survey has found any', async () => {
  const p = await load({ dimension: 'end', answer: () => ({ surveyed: false, recorded: [], predicted: [], kinds: {}, catalog: CATALOG }) });
  // Only the kind that is listed so that it can be asked for; none of the overworld's.
  same(p.kinds(), ['End Citys']);
});

test('a site with nothing found at it is said to be that, and is not counted as predicted', async () => {
  const p = await load();
  const drawn = p.classes();
  assert.ok(drawn.includes('Ruined Portal · a site with nothing found at it now | structure predicted vacant'), drawn.join('\n'));
  assert.ok(drawn.includes('Village · a site with nothing found at it now | structure predicted vacant'), drawn.join('\n'));
  assert.ok(drawn.includes('Monument · possible here | structure predicted candidate'));
  assert.ok(!drawn.some((line) => /Ruined Portal · predicted from the seed/.test(line)));
  assert.match(p.noteOf('Ruined Portals'), /1 site with nothing found/);
  assert.match(p.noteOf('Villages'), /1 possible, 1 site with nothing found/);
  assert.doesNotMatch(p.noteOf('Villages'), /predicted/);
  // A server from before it said so means the same by a finished site of such a kind.
  const world = { ...WORLD, overworld: { ...WORLD.overworld, predicted: [{ kind: 'village', x: 1200, z: 0, generated: true }, { kind: 'monument', x: 900, z: 0, generated: true }] } };
  const q = await load({ world });
  same(q.classes().filter((line) => line.includes('predicted')), ['Monument · predicted from the seed | structure predicted doubted', 'Village · a site with nothing found at it now | structure predicted vacant']);
});

test('a fortress\'s line says what it counts', async () => {
  const p = await load({ dimension: 'nether' });
  assert.match(p.noteOf('Fortresss'), /Parts within 96 blocks of each other are counted as one fortress/);
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
