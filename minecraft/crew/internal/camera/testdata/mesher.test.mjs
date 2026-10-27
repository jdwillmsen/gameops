// Runs the real mesher worker in a vm and checks which faces it emits. Run by
// TestMesherFaces with `node --test`; it needs nothing but node.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import fs from 'node:fs';
import vm from 'node:vm';

const src = fs.readFileSync(process.env.MESHER_JS, 'utf8');

const face = { uv: [0, 0, 1, 1] };
const model = k => ({ model: k, up: face, down: face, side: face, north: face, south: face, east: face, west: face, tint: '' });

// names: material list; ids are 1-based positions. kinds maps a block name to
// the table's model kind. The returned object also defines further materials
// later, as the server does when a new block shows up.
function worker(names, kinds, extra = {}) {
  const out = [];
  const sandbox = {
    postMessage: m => out.push(m), setTimeout, performance,
    onmessage: null,
  };
  sandbox.self = sandbox;
  vm.createContext(sandbox);
  vm.runInContext(src, sandbox);
  const entry = n => {
    const base = n.split('@')[0];
    return { name: n, alpha: 1, nonCube: !!kinds[base]?.nonCube, rgb: [1, 1, 1] };
  };
  const blocks = {};
  for (const n of names) { const base = n.split('@')[0]; if (kinds[base]) blocks['minecraft:' + base] = model(kinds[base].model); }
  const send = m => sandbox.onmessage({ data: m });
  send({ t: 'mats', replace: true, mats: names.map((n, i) => [i + 1, entry(n)]) });
  Object.assign(blocks, extra.blocks);
  send({ t: 'atlas', atlas: { table: { blocks }, tiles: {}, tints: extra.tints || {}, waterAlpha: extra.waterAlpha } });
  return {
    biomeTable(table) { send({ t: 'biomes', table }); },
    biome(cx, cz, id) { send({ t: 'biome', cx, cz, ids: new Uint16Array(256).fill(id) }); },
    define(id, name) { send({ t: 'mats', replace: false, mats: [[id, entry(name)]] }); },
    set(key, fill) {
      const cells = new Uint16Array(4096);
      fill(cells, (x, y, z, id) => { cells[(y << 8) | (z << 4) | x] = id; });
      send({ t: 'set', key, cells });
    },
    // How many times the sub-chunk has been meshed so far.
    meshed(key) { return out.filter(m => m.t === 'mesh' && m.key === key).length; },
    async meshes() {
      await new Promise(r => setTimeout(r, 120));
      const last = new Map();
      for (const m of out) if (m.t === 'mesh') last.set(m.key, m.parts);
      return last;
    },
  };
}

const quads = parts => parts.reduce((n, p) => n + p.i.length / 6, 0);
const kinds = {
  stone: { model: 'cube' },
  grass_path: { model: 'cube' },
  chest: { model: 'cube' },
  brown_mushroom_block: { model: 'cube' },
  oak_leaves: { model: 'cube' },
  oak_stairs: { model: 'stairs', nonCube: true },
  stone_slab: { model: 'slab' },
};

// A solid layer of one block at height y of a chunk.
const layer = (id, y) => (cells, put) => { for (let z = 0; z < 16; z++) for (let x = 0; x < 16; x++) put(x, y, z, id); };

test('a whole-cube block the old name list hid is drawn', async () => {
  const w = worker(['brown_mushroom_block'], kinds);
  w.set('0,0,0', (c, put) => put(5, 5, 5, 1));
  const m = await w.meshes();
  assert.equal(quads(m.get('0,0,0')), 6);
});

test('a path block is drawn below the block edge', async () => {
  const w = worker(['grass_path'], kinds);
  w.set('0,0,0', (c, put) => put(5, 5, 5, 1));
  const parts = (await w.meshes()).get('0,0,0');
  const maxY = Math.max(...parts.map(p => p.box[4]));
  assert.ok(Math.abs(maxY - (5 + 15 / 16)) < 1e-6, `top at ${maxY}`);
});

test('a chest is drawn inset from the block edge', async () => {
  const w = worker(['chest'], kinds);
  w.set('0,0,0', (c, put) => put(5, 5, 5, 1));
  const parts = (await w.meshes()).get('0,0,0');
  const [x0, y0, z0, x1, y1, z1] = [0, 1, 2, 3, 4, 5].map(k => Math.min(...parts.map(p => p.box[k])));
  const maxes = [3, 4, 5].map(k => Math.max(...parts.map(p => p.box[k])));
  assert.deepEqual([x0 - 5, y0 - 5, z0 - 5].map(v => Math.round(v * 16)), [1, 0, 1]);
  assert.deepEqual([maxes[0] - 5, maxes[1] - 5, maxes[2] - 5].map(v => Math.round(v * 16)), [15, 14, 15]);
});

test('a top slab is drawn in the top half', async () => {
  const w = worker(['stone_slab@0,8,0,16,16,16'], kinds);
  w.set('0,0,0', (c, put) => put(5, 5, 5, 1));
  const parts = (await w.meshes()).get('0,0,0');
  assert.equal(Math.min(...parts.map(p => p.box[1])), 5.5);
  assert.equal(Math.max(...parts.map(p => p.box[4])), 6);
});

test('a stair variant carries its own boxes', async () => {
  const w = worker(['oak_stairs@0,0,0,16,8,16;8,8,0,16,16,16'], kinds);
  w.set('0,0,0', (c, put) => put(5, 5, 5, 1));
  const parts = (await w.meshes()).get('0,0,0');
  // Two boxes: the second sits on the +x half only.
  assert.equal(Math.max(...parts.map(p => p.box[3])), 6);
  assert.ok(quads(parts) >= 10);
});

test('faces toward an unsent all-air sub-chunk in a loaded column are drawn', async () => {
  const w = worker(['stone'], kinds);
  w.set('0,0,0', layer(1, 15));
  const m = await w.meshes();
  // Top (256) + four sides (16 each); the bottom touches the unknown floor.
  const tops = m.get('0,0,0').flatMap(p => [...Array(p.p.length / 12).keys()].map(q => p.p[q * 12 + 1])).filter(y => y === 16).length;
  assert.equal(tops, 256, 'top faces at y=16');
});

test('a chunk whose neighbouring column is unknown keeps that edge hidden', async () => {
  const w = worker(['stone'], kinds);
  w.set('0,0,0', (c, put) => put(15, 5, 5, 1));
  const m = await w.meshes();
  // The +x neighbour column is not loaded: its face stays hidden, the others show.
  assert.equal(quads(m.get('0,0,0')), 5);
});

test('a neighbouring column that arrives later reveals the edge', async () => {
  const w = worker(['stone'], kinds);
  w.set('0,0,0', (c, put) => put(15, 5, 5, 1));
  await w.meshes();
  w.set('1,0,0', (c, put) => put(0, 0, 0, 1));
  const m = await w.meshes();
  assert.equal(quads(m.get('0,0,0')), 6);
});

test('a neighbouring column whose lower sub-chunk arrives later reveals the edge', async () => {
  const w = worker(['stone'], kinds);
  w.set('1,6,0', (c, put) => put(0, 0, 0, 1));
  w.set('0,5,0', (c, put) => put(15, 5, 5, 1));
  await w.meshes();
  w.set('1,3,0', (c, put) => put(0, 0, 0, 1));
  const m = await w.meshes();
  assert.equal(quads(m.get('0,5,0')), 6);
});

test('leaf blocks keep the faces between them', async () => {
  const w = worker(['oak_leaves'], kinds);
  w.set('0,0,0', (c, put) => { put(5, 5, 5, 1); put(6, 5, 5, 1); });
  const m = await w.meshes();
  assert.equal(quads(m.get('0,0,0')), 12);
});

test('stone blocks still cull the face between them', async () => {
  const w = worker(['stone'], kinds);
  w.set('0,0,0', (c, put) => { put(5, 5, 5, 1); put(6, 5, 5, 1); });
  const m = await w.meshes();
  assert.equal(quads(m.get('0,0,0')), 10);
});

// Ids above 255 must reach the mesher whole: 300 & 255 is 44, which here is a
// block that draws nothing.
test('a cell holding an id above 255 is that material, not its low byte', async () => {
  const names = Array.from({ length: 300 }, (_, i) => 'filler_' + i);
  names[43] = 'torch';
  names[299] = 'stone';
  const w = worker(names, { ...kinds, torch: { model: 'none', nonCube: true } });
  w.set('0,0,0', (c, put) => put(5, 5, 5, 300));
  const m = await w.meshes();
  assert.equal(quads(m.get('0,0,0')), 6);
});

test('a material for an id no cell uses does not re-mesh anything', async () => {
  const w = worker(['stone'], kinds);
  w.set('0,0,0', (c, put) => put(5, 5, 5, 1));
  await w.meshes();
  const before = w.meshed('0,0,0');
  w.define(2, 'oak_leaves');
  await w.meshes();
  assert.equal(w.meshed('0,0,0'), before);
});

test('a material that arrives after a cell already used its id re-meshes that sub-chunk', async () => {
  const w = worker(['stone'], { ...kinds, torch: { model: 'none', nonCube: true } });
  w.set('0,0,0', (c, put) => put(5, 5, 5, 2));
  assert.equal(quads((await w.meshes()).get('0,0,0')), 6, 'an undefined id is drawn as a placeholder');
  w.define(2, 'torch');
  assert.equal(quads((await w.meshes()).get('0,0,0')), 0);
});

// The mean sky and block light (0..255) over the four corners of the face of
// cell (x, y, z) that looks along the given axis direction.
function lightOf(parts, x, y, z, dir = 'up') {
  const sel = { up: [1, y + 1], down: [1, y], east: [0, x + 1], west: [0, x], south: [2, z + 1], north: [2, z] }[dir];
  for (const p of parts) for (let q = 0; q < p.p.length / 12; q++) {
    const v = [0, 1, 2, 3].map(k => [p.p[(q * 4 + k) * 3], p.p[(q * 4 + k) * 3 + 1], p.p[(q * 4 + k) * 3 + 2]]);
    const inCell = v.every(c => c[0] >= x && c[0] <= x + 1 && c[1] >= y && c[1] <= y + 1 && c[2] >= z && c[2] <= z + 1 && c[sel[0]] === sel[1]);
    if (!inCell) continue;
    let sky = 0, blk = 0;
    for (let k = 0; k < 4; k++) { sky += p.l[(q * 4 + k) * 2]; blk += p.l[(q * 4 + k) * 2 + 1]; }
    return { sky: sky / 4, blk: blk / 4 };
  }
  throw new Error(`no ${dir} face at ${x},${y},${z}`);
}

const lightKinds = { ...kinds, torch: { model: 'torch', nonCube: true } };

test('a floor open to the sky takes full sky light and no block light', async () => {
  const w = worker(['stone'], kinds);
  w.set('0,0,0', layer(1, 0));
  const parts = (await w.meshes()).get('0,0,0');
  assert.deepEqual(lightOf(parts, 8, 0, 8), { sky: 255, blk: 0 });
});

test('sky light fades one level per block under a roof', async () => {
  const w = worker(['stone'], kinds);
  w.set('0,0,0', (c, put) => {
    for (let z = 0; z < 16; z++) for (let x = 0; x < 16; x++) { put(x, 0, z, 1); if (x >= 8) put(x, 3, z, 1); }
  });
  const parts = (await w.meshes()).get('0,0,0');
  const at = x => lightOf(parts, x, 0, 8).sky;
  assert.equal(at(2), 255);
  assert.ok(at(9) < at(2) && at(9) > at(13) && at(13) > 0, `${at(2)} ${at(9)} ${at(13)}`);
});

test('a torch lights the floor around it, less with distance, in a dark room', async () => {
  const w = worker(['stone', 'torch'], lightKinds);
  w.set('0,0,0', (c, put) => {
    for (let z = 0; z < 16; z++) for (let x = 0; x < 16; x++) { put(x, 0, z, 1); put(x, 4, z, 1); }
    put(8, 1, 8, 2);
  });
  const parts = (await w.meshes()).get('0,0,0');
  const near = lightOf(parts, 8, 0, 8), mid = lightOf(parts, 11, 0, 8), far = lightOf(parts, 14, 0, 8);
  assert.equal(near.sky, 0);
  assert.ok(near.blk > mid.blk && mid.blk > far.blk && far.blk > 0, `${near.blk} ${mid.blk} ${far.blk}`);
  assert.ok(near.blk >= 13 * 17);
});

test('a roof that lands later darkens the sub-chunk below it', async () => {
  const w = worker(['stone'], kinds);
  w.set('0,0,0', layer(1, 0));
  assert.equal(lightOf((await w.meshes()).get('0,0,0'), 8, 0, 8).sky, 255);
  w.set('0,1,0', layer(1, 0));
  const m = await w.meshes();
  assert.equal(lightOf(m.get('0,0,0'), 8, 0, 8).sky, 0);
});

test('a sub-chunk that stays open is meshed once, not again for every neighbour above it', async () => {
  const w = worker(['stone'], kinds);
  w.set('0,0,0', layer(1, 0));
  await w.meshes();
  w.set('0,1,0', (c, put) => put(3, 3, 3, 1));
  await w.meshes();
  assert.ok(w.meshed('0,0,0') <= 2, `meshed ${w.meshed('0,0,0')} times`);
});

const grassBlock = {
  'minecraft:grass_block': { model: 'cube', tint: 'grass', up: { uv: [0, 0, 1, 1], tinted: true }, down: face, side: face, north: face, south: face, east: face, west: face },
  'minecraft:water': { model: 'liquid', tint: 'water', up: { uv: [0, 0, 1, 1], tinted: true }, down: face, side: face, north: face, south: face, east: face, west: face },
};
const biomeEntries = [{ id: 1, grass: [1, 0, 0], foliage: [0, 1, 0], water: [0, 0, 1], alpha: .5 }];

function topColour(parts) {
  const p = parts.find(q => q.k === 0 || q.k === 1);
  for (let q = 0; q < p.p.length / 12; q++) if (p.p[q * 12 + 1] === 6) return [...p.c.slice(q * 16, q * 16 + 4)];
  throw new Error('no top face');
}

test('grass takes its biome colour once the biome and its table are known', async () => {
  const w = worker(['grass_block'], kinds, { blocks: grassBlock, tints: { grass: [.1, .8, .1] } });
  w.set('0,0,0', (c, put) => put(5, 5, 5, 1));
  const plain = topColour((await w.meshes()).get('0,0,0'));
  assert.deepEqual(plain.map(v => +v.toFixed(2)), [.1, .8, .1, 1]);
  w.biomeTable(biomeEntries);
  w.biome(0, 0, 1);
  const tinted = topColour((await w.meshes()).get('0,0,0'));
  assert.deepEqual(tinted, [1, 0, 0, 1]);
});

test('a biome border blends over the blocks near it', async () => {
  const w = worker(['grass_block'], kinds, { blocks: grassBlock, tints: { grass: [.1, .8, .1] } });
  w.biomeTable([...biomeEntries, { id: 2, grass: [0, 0, 1], foliage: [0, 1, 0], water: [0, 0, 1], alpha: .5 }]);
  w.biome(0, 0, 1);
  w.biome(1, 0, 2);
  w.set('0,0,0', (c, put) => { put(15, 5, 5, 1); put(0, 5, 5, 1); });
  const parts = (await w.meshes()).get('0,0,0');
  const colourAt = x => {
    const p = parts.find(q => q.k === 0);
    for (let q = 0; q < p.p.length / 12; q++) if (p.p[q * 12] >= x && p.p[q * 12] <= x + 1 && p.p[q * 12 + 1] === 6) return [...p.c.slice(q * 16, q * 16 + 3)];
  };
  const edge = colourAt(15), inside = colourAt(0);
  assert.ok(edge[2] > 0 && edge[0] > 0, `edge ${edge}`);
  assert.deepEqual(inside.map(v => +v.toFixed(2)), [1, 0, 0]);
});

test('water takes its biome opacity', async () => {
  const w = worker(['water'], kinds, { blocks: grassBlock, waterAlpha: .65 });
  w.set('0,0,0', (c, put) => put(5, 5, 5, 1));
  assert.equal(+topColourAny(await w.meshes()).toFixed(2), .65);
  w.biomeTable(biomeEntries);
  w.biome(0, 0, 1);
  assert.equal(topColourAny(await w.meshes()), .5);
});

function topColourAny(m) {
  const p = m.get('0,0,0').find(q => q.k === 1);
  return p.c[3];
}
