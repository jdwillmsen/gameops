// Sub-chunk mesher, run as a Web Worker so the page's frame loop never waits
// on it. Builds per-sub-chunk geometry with only the faces next to a
// non-opaque neighbour, the same technique prismarine-viewer's mesher uses; no
// code is copied. Colours arrive already converted to the renderer's working
// space, so this file needs no three.js.
'use strict';

const blocks = new Map(); // "cx,cy,cz" -> Uint16Array(4096)
// "cx,cz" -> the cy of every sub-chunk loaded in that column. All-air
// sub-chunks are never sent, so a missing one in a loaded column is air; only
// a column with nothing loaded is unknown.
const columns = new Map();
const dirty = new Set();
const infoCache = new Map();
const colorCache = new Map();
let mats = [];            // index = id - 1
// Ids a cell used before the server defined them. A material that arrives for
// one of these is the only kind that changes what is already meshed.
const missing = new Set();
let atlas = null;         // { table, tiles: {"u0,v0,u1,v1": layer}, tints: {kind: [r, g, b]} }
let epoch = 0;
let seq = 0;              // last message seq received from the page
let at = { x: 0, y: 70, z: 0 };
let scheduled = false;
// Sub-chunks whose light changed because of a neighbour's blocks. They are
// meshed again only once the queue is empty, so a replay that lands a column
// top-down meshes each chunk with the light it will keep instead of once per
// arrival.
const lightDirty = new Set();
let biomeTable = null;    // Map id -> { grass, foliage, water: [r, g, b], alpha }
const biomeCols = new Map(); // "cx,cz" -> Uint16Array(256), indexed x * 16 + z
const tintCache = new Map(); // "cx,cz" -> blended colours, or null when unknown

const FACES = [ // name, dir, corners (ccw seen from outside, bottom-left first), shade
  { n: 'east',  d: [1, 0, 0],  c: [[1,0,1],[1,0,0],[1,1,0],[1,1,1]], s: .6 },
  { n: 'west',  d: [-1, 0, 0], c: [[0,0,0],[0,0,1],[0,1,1],[0,1,0]], s: .6 },
  { n: 'up',    d: [0, 1, 0],  c: [[0,1,1],[1,1,1],[1,1,0],[0,1,0]], s: 1 },
  { n: 'down',  d: [0, -1, 0], c: [[0,0,0],[1,0,0],[1,0,1],[0,0,1]], s: .5 },
  { n: 'south', d: [0, 0, 1],  c: [[0,0,1],[1,0,1],[1,1,1],[0,1,1]], s: .8 },
  { n: 'north', d: [0, 0, -1], c: [[1,0,0],[0,0,0],[0,1,0],[1,1,0]], s: .8 },
];
// Which block axis runs along a face's texture s (left to right) and t
// (bottom to top), and whether it grows that way, so a box that covers only
// part of a block shows the matching part of the texture.
for (const f of FACES) {
  const [c0, c1, , c3] = f.c;
  f.axis = f.d.findIndex(v => v !== 0);
  f.ax = [0, 1, 2].find(k => c0[k] !== c1[k]); f.sx = c1[f.ax] > c0[f.ax];
  f.ay = [0, 1, 2].find(k => c0[k] !== c3[k]); f.sy = c3[f.ay] > c0[f.ay];
}

// Stand-in shapes for blocks the engine builds itself: boxes as
// [x0,y0,z0,x1,y1,z1] in block units. Block state is not on the wire, so
// slabs are always the bottom half and stairs always rise towards +z.
const BOXES = {
  slab: [[0, 0, 0, 1, .5, 1]],
  stairs: [[0, 0, 0, 1, .5, 1], [0, .5, .5, 1, 1, 1]],
  torch: [[.4375, 0, .4375, .5625, .625, .5625]],
  layer: [[0, 0, 0, 1, .0625, 1]],
  fence: [[.375, 0, .375, .625, 1, .625], [0, .75, .4375, 1, .9375, .5625], [0, .375, .4375, 1, .5625, .5625]],
  wall: [[.25, 0, .25, .75, 1, .75], [0, 0, .3125, 1, .8125, .6875]],
  pane: [[.4375, 0, 0, .5625, 1, 1]],
  door: [[0, 0, 0, 1, 1, .1875]],
};
const FULL = [[0, 0, 0, 1, 1, 1]];
const WHITE = [1, 1, 1];

// Names the page draws blended. The wire only marks water and ice as
// translucent; glass blocks arrive as opaque, but drawing them that way would
// hide everything behind them.
const BLENDED = /^(water|flowing_water|ice|frosted_ice|glass|tinted_glass|glass_pane|[a-z_]+_stained_glass|[a-z_]+_stained_glass_pane)$/;
// Leaves are alpha-tested, so a hole in one shows what is behind it. Faces
// between two leaf blocks are therefore kept, or the holes open onto the sky.
const LEAVES = /_leaves$/;
// Blocks the table calls cubes but the game draws smaller: a path or farmland
// top below the edge, a chest inset from every side, a bed knee-high, redstone
// dust a decal. Boxes are in sixteenths so they read like the game's models.
const inset = (x0, y0, z0, x1, y1, z1) => [[x0 / 16, y0 / 16, z0 / 16, x1 / 16, y1 / 16, z1 / 16]];
const SHAPED = {
  grass_path: inset(0, 0, 0, 16, 15, 16), dirt_path: inset(0, 0, 0, 16, 15, 16), farmland: inset(0, 0, 0, 16, 15, 16),
  soul_sand: inset(0, 0, 0, 16, 14, 16),
  chest: inset(1, 0, 1, 15, 14, 15), trapped_chest: inset(1, 0, 1, 15, 14, 15), ender_chest: inset(1, 0, 1, 15, 14, 15),
  bed: inset(0, 0, 0, 16, 9, 16),
  enchanting_table: inset(0, 0, 0, 16, 12, 16),
  stonecutter_block: inset(0, 0, 0, 16, 9, 16),
  daylight_detector: inset(0, 0, 0, 16, 6, 16), daylight_detector_inverted: inset(0, 0, 0, 16, 6, 16),
  sculk_sensor: inset(0, 0, 0, 16, 8, 16), calibrated_sculk_sensor: inset(0, 0, 0, 16, 8, 16),
  cactus: inset(1, 0, 1, 15, 16, 15), decorated_pot: inset(1, 0, 1, 15, 16, 15),
  redstone_wire: inset(0, 0, 0, 16, 1, 16),
};
const FIRE = /^(soul_)?fire$/;
const WATER = /^(water|flowing_water)$/;
const WATER_TOP = [[0, 0, 0, 1, 14 / 16, 1]];
// The page converts to linear light but the game multiplies shade and
// occlusion on the encoded colour, so these factors are raised to the same
// power before they are baked into vertex colours.
const lin = v => Math.pow(v, 2.2);
const AO_LEVEL = [1, .85, .7, .55].map(lin);
const SHADE = Object.fromEntries(FACES.map(f => [f.n, lin(f.s)]));

// Block light the game gives off, by name without the namespace. Block state
// is not on the wire, so a block that is only sometimes lit (campfire, redstone
// lamp) counts as lit when the server names the lit form and as dark otherwise.
const EMIT = {
  torch: 14, soul_torch: 10, redstone_torch: 7, lantern: 15, soul_lantern: 10, glowstone: 15, sea_lantern: 15,
  lava: 15, flowing_lava: 15, fire: 15, soul_fire: 10, campfire: 15, soul_campfire: 10, shroomlight: 15,
  lit_pumpkin: 15, lit_redstone_lamp: 15, beacon: 15, end_rod: 14, ochre_froglight: 15, verdant_froglight: 15,
  pearlescent_froglight: 15, magma: 3, crying_obsidian: 10, enchanting_table: 7, ender_chest: 7, portal: 11,
  lit_furnace: 13, lit_blast_furnace: 13, lit_smoker: 13, conduit: 15, sculk_catalyst: 6, glow_lichen: 7,
  amethyst_cluster: 5, large_amethyst_bud: 4, medium_amethyst_bud: 2, small_amethyst_bud: 1, sea_pickle: 6,
  light_block: 15, lit_deepslate_redstone_ore: 9, lit_redstone_ore: 9, end_gateway: 15, brewing_stand: 1,
  brown_mushroom: 1, dragon_egg: 1, cave_vines_head_with_berries: 14, cave_vines_body_with_berries: 14,
  candle: 3, copper_torch: 14,
};
const CANDLE = /_candle$/;
const ICE = /^(ice|frosted_ice)$/;
const LAVA = /^(flowing_)?lava$/;
function boxVolume(boxes) {
  let v = 0;
  for (const b of boxes) v += (b[3] - b[0]) * (b[4] - b[1]) * (b[5] - b[2]);
  return v;
}

function jitter(x, y, z) {
  let h = (x * 73856093) ^ (y * 19349663) ^ (z * 83492791);
  h = (h ^ (h >>> 13)) * 1274126177; h ^= h >>> 16;
  return 0.9 + ((h & 255) / 255) * 0.2;
}
function rgb(id) {
  let c = colorCache.get(id);
  if (!c) {
    const m = mats[id - 1];
    if (!m) missing.add(id);
    c = m ? m.rgb : [1, 0, 1];
    colorCache.set(id, c);
  }
  return c;
}

// The atlas arrives after the first frames, so everything that depends on it
// is looked up lazily and dropped when it changes. Without it every block is
// a flat colour and the shapes the page cannot draw are skipped.
function parseBoxes(text) {
  if (!text) return null;
  const boxes = text.split(';').map(b => b.split(',').map(v => Number(v) / 16));
  return boxes.every(b => b.length === 6 && b.every(Number.isFinite)) ? boxes : null;
}

function info(id) {
  let r = infoCache.get(id);
  if (r) return r;
  const m = mats[id - 1];
  if (!m) missing.add(id);
  // A shaped block carries its boxes after an @ in the name; the table knows
  // it by the part before.
  const [base, boxText] = m ? m.name.split('@') : ['', ''];
  const custom = parseBoxes(boxText);
  const blk = (m && atlas && atlas.table.blocks['minecraft:' + base]) || null;
  let shape = 'cube';
  if (blk) {
    const k = blk.model;
    // A name the flat list marks as non-cube stays hidden when the table
    // would draw it as a cube, which is how it hid before textures existed.
    shape = k === 'liquid' ? 'cube' : k === 'none' || (k === 'cube' && m.nonCube) ? 'skip' : k;
    // The table has no model for fire and calls it a cube, which the flat-list
    // rule above hides; the flames are drawn as crossed planes instead.
    if (FIRE.test(base)) shape = 'cross';
  } else if (m && m.nonCube) shape = 'skip';
  let boxes = null;
  if (custom && shape !== 'skip') { shape = 'boxes'; boxes = custom; }
  else if (shape === 'cube' && Object.hasOwn(SHAPED, base)) { shape = 'boxes'; boxes = SHAPED[base]; }
  const trans = !!m && (m.alpha < 1 || BLENDED.test(base));
  const leaves = LEAVES.test(base);
  const opaque = shape === 'cube' && !trans && !leaves;
  const water = !!m && WATER.test(base);
  r = {
    m, blk, shape, boxes, trans, leaves, opaque, water, fire: !!blk && FIRE.test(base),
    tint: blk && atlas.tints[blk.tint] || null,
    tintKind: blk && blk.tint || '',
    // Water is clear but for its biome colour; the pack's texture alone is too
    // opaque to read as water.
    alpha: water ? atlas?.waterAlpha ?? .65 : m && m.alpha < 1 ? m.alpha : 1,
    // How much light a cell takes away passing through it: 15 shuts it out.
    // Partial blocks count as solid from half a block of volume, so a roof of
    // slabs is a roof and a fence is not.
    dampen: shape === 'skip' ? 0 : opaque && !LAVA.test(base) ? 15
      : leaves || water || ICE.test(base) || LAVA.test(base) ? 1
      : (shape === 'slab' || shape === 'stairs' || shape === 'boxes') && boxVolume(boxes || BOXES[shape]) >= .5 ? 15 : 0,
    emit: EMIT[base] ?? (CANDLE.test(base) ? 3 : 0),
  };
  infoCache.set(id, r);
  return r;
}

// Blended colours for the 16x16 blocks of a column, or null when the column's
// biomes, or the table that says what they tint, have not arrived. Each block
// averages the biomes within two blocks, as the game does, so a border fades
// instead of stepping.
function columnTints(cx, cz) {
  const key = cx + ',' + cz;
  let t = tintCache.get(key);
  if (t !== undefined) return t;
  t = null;
  if (biomeTable && biomeCols.has(key)) {
    const grid = new Int32Array(48 * 48).fill(-1);
    for (let dx = -1; dx <= 1; dx++) for (let dz = -1; dz <= 1; dz++) {
      const col = biomeCols.get((cx + dx) + ',' + (cz + dz));
      if (!col) continue;
      for (let x = 0; x < 16; x++) for (let z = 0; z < 16; z++) grid[((dx + 1) * 16 + x) * 48 + (dz + 1) * 16 + z] = col[x * 16 + z];
    }
    t = { grass: new Float32Array(768), foliage: new Float32Array(768), water: new Float32Array(768), alpha: new Float32Array(256) };
    const fallback = { grass: atlas?.tints.grass || WHITE, foliage: atlas?.tints.foliage || WHITE, water: atlas?.tints.water || WHITE, alpha: atlas?.waterAlpha ?? .65 };
    for (let x = 0; x < 16; x++) for (let z = 0; z < 16; z++) {
      const sum = { grass: [0, 0, 0], foliage: [0, 0, 0], water: [0, 0, 0] };
      let a = 0, n = 0;
      for (let dx = -2; dx <= 2; dx++) for (let dz = -2; dz <= 2; dz++) {
        const e = biomeTable.get(grid[(16 + x + dx) * 48 + 16 + z + dz]);
        if (!e) continue;
        n++; a += e.alpha;
        for (const k of ['grass', 'foliage', 'water']) for (let c = 0; c < 3; c++) sum[k][c] += e[k][c];
      }
      const o = x * 16 + z;
      for (const k of ['grass', 'foliage', 'water']) for (let c = 0; c < 3; c++) t[k][o * 3 + c] = n ? sum[k][c] / n : fallback[k][c];
      t.alpha[o] = n ? a / n : fallback.alpha;
    }
  }
  tintCache.set(key, t);
  return t;
}

const TINT = [1, 1, 1];
function faceColor(inf, face, wx, wz) {
  if (face.tinted && inf.tint) {
    const t = biomeTable && columnTints(wx >> 4, wz >> 4);
    const arr = t && t[inf.tintKind];
    if (!arr) return inf.tint;
    const o = ((wx & 15) * 16 + (wz & 15)) * 3;
    TINT[0] = arr[o]; TINT[1] = arr[o + 1]; TINT[2] = arr[o + 2];
    return TINT;
  }
  // The pack's per-texture overlay colour is not applied: it is a legacy
  // multiplier that turns grass sides orange.
  return WHITE;
}

// Light levels per vertex for lt: sky then block, 0..255 for levels 0..15.
// One quad. uvw is null for flat colour, else (s, t, layer) per corner. ao
// holds the four corners' brightness; the diagonal joins the brighter pair, so
// a lone dark corner stays in one triangle instead of smearing across the face.
function quad(g, pos, col, a, mul, uvw, ao, backface, lt) {
  const base = g.p.length / 3;
  for (let k = 0; k < 4; k++) {
    const m = mul * (ao ? ao[k] : 1);
    g.p.push(pos[k][0], pos[k][1], pos[k][2]);
    g.c.push(col[0] * m, col[1] * m, col[2] * m, a);
    g.l.push(lt[2 * k], lt[2 * k + 1]);
    if (uvw) g.u.push(uvw[k][0], uvw[k][1], uvw[k][2]);
  }
  if (backface) g.i.push(base, base + 2, base + 1, base, base + 3, base + 2);
  else if (ao && ao[0] + ao[2] < ao[1] + ao[3]) g.i.push(base + 1, base + 2, base + 3, base + 1, base + 3, base);
  else g.i.push(base, base + 1, base + 2, base, base + 2, base + 3);
}

// Texture coordinates for a face rectangle given (s,t) in 0..1 across it, t
// up. Each tile is its own array layer, so there is no neighbour to bleed
// into and no inset is needed.
function faceUVW(face, st) {
  const layer = atlas.tiles[face.uv.join(',')] ?? 0;
  return st.map(([s, t]) => [s, 1 - t, layer]);
}

// Light. The game computes it on the server and the client; crew is sent only
// blocks, so the worker works it out from them. Sky light falls straight down
// from the top of each column and both kinds then spread one level per block
// through whatever does not shut them out. The region is the sub-chunk plus a
// margin of LP blocks, so light from further away than that, which is most
// of a torch's reach, is not seen, and light from beyond a chunk that has
// not loaded is not either.
const LP = 8, LW = 16 + 2 * LP, LH = 16 + 2 * LP, LN = LW * LW * LH;
const STRIDE = [1, LW * LW, LW];
const lightOp = new Uint8Array(LN), lightSky = new Uint8Array(LN), lightBlk = new Uint8Array(LN);
const buckets = Array.from({ length: 16 }, () => []);
const FULL_SKY = new Uint8Array(256).fill(15);
const skyCache = new Map(); // "cx,cz" -> Map(cy -> light entering the top of that sub-chunk)
const LI = (x, y, z) => ((y + LP) * LW + (z + LP)) * LW + (x + LP);

// Opacity and emission by material id, filled on demand (255 is unknown) and
// dropped with infoCache.
const opLut = new Uint8Array(65536).fill(255), emitLut = new Uint8Array(65536).fill(255);
function forgetInfo(id) {
  if (id === undefined) { infoCache.clear(); opLut.fill(255); emitLut.fill(255); return; }
  infoCache.delete(id); opLut[id] = 255; emitLut[id] = 255;
}
function opacityOf(id) {
  let v = opLut[id];
  if (v === 255) opLut[id] = v = info(id).dampen;
  return v;
}
function emitOf(id) {
  let v = emitLut[id];
  if (v === 255) emitLut[id] = v = info(id).emit;
  return v;
}

// Light leaving the bottom of a sub-chunk's cells, given what entered the top.
function exitOf(cells, entry) {
  const out = new Uint8Array(256);
  for (let z = 0; z < 16; z++) for (let x = 0; x < 16; x++) {
    let v = entry[z * 16 + x];
    for (let y = 15; y >= 0 && v > 0; y--) {
      const id = cells[((y << 4 | z) << 4) | x];
      if (!id) continue;
      const o = opacityOf(id);
      v = o >= 15 ? 0 : Math.max(0, v - o);
    }
    out[z * 16 + x] = v;
  }
  return out;
}

// Sky light entering the top of a sub-chunk: full where nothing is loaded
// above it, else what the sub-chunk above lets through.
function skyEntry(cx, cy, cz) {
  const ck = cx + ',' + cz;
  let col = skyCache.get(ck);
  if (!col) skyCache.set(ck, col = new Map());
  let e = col.get(cy);
  if (e) return e;
  const ys = columns.get(ck);
  let top = -Infinity;
  if (ys) for (const y of ys) if (y > top) top = y;
  if (top <= cy) e = FULL_SKY;
  else {
    const above = skyEntry(cx, cy + 1, cz), cells = blocks.get(cx + ',' + (cy + 1) + ',' + cz);
    e = cells ? exitOf(cells, above) : above;
  }
  col.set(cy, e);
  return e;
}

// Runs fn, which changes the blocks of a sub-chunk, and queues for a later
// mesh the sub-chunks around it whose sky light the change altered.
function withSkyCheck(key, fn) {
  const [cx, cy, cz] = key.split(',').map(Number);
  const before = skyEntry(cx, cy - 1, cz);
  fn();
  skyCache.delete(cx + ',' + cz);
  const after = skyEntry(cx, cy - 1, cz);
  if (before === after) return;
  for (let i = 0; i < 256; i++) if (before[i] !== after[i]) {
    for (let dx = -1; dx <= 1; dx++) for (let dz = -1; dz <= 1; dz++) {
      const ys = columns.get((cx + dx) + ',' + (cz + dz));
      if (ys) for (const y of ys) if (y < cy || (y === cy && (dx || dz))) lightDirty.add((cx + dx) + ',' + y + ',' + (cz + dz));
    }
    return;
  }
}

function spread(light) {
  for (let l = 15; l > 1; l--) {
    const q = buckets[l];
    for (let i = 0; i < q.length; i++) {
      const c = q[i];
      if (light[c] !== l) continue;
      const x = c % LW, z = (c / LW | 0) % LW, y = (c / (LW * LW)) | 0;
      for (let k = 0; k < 6; k++) {
        let n;
        switch (k) {
          case 0: if (x === 0) continue; n = c - 1; break;
          case 1: if (x === LW - 1) continue; n = c + 1; break;
          case 2: if (z === 0) continue; n = c - LW; break;
          case 3: if (z === LW - 1) continue; n = c + LW; break;
          case 4: if (y === 0) continue; n = c - LW * LW; break;
          default: if (y === LH - 1) continue; n = c + LW * LW;
        }
        const o = lightOp[n];
        if (o >= 15) continue;
        const nl = l - (o > 1 ? o : 1);
        if (nl > light[n]) { light[n] = nl; if (nl > 1) buckets[nl].push(n); }
      }
    }
    q.length = 0;
  }
}

// Fills lightOp, lightSky and lightBlk for the region around sub-chunk
// (cx, cy, cz). nb and gap are mesh()'s view of the 3x3x3 chunks around it.
function computeLight(cx, cy, cz, nb, gap) {
  lightSky.fill(0); lightBlk.fill(0);
  for (let l = 2; l < 16; l++) buckets[l].length = 0;
  for (let y = -LP; y < 16 + LP; y++) {
    const yn = ((y >> 4) + 1) * 9, yl = (y & 15) << 4;
    for (let z = -LP; z < 16 + LP; z++) {
      const zn = yn + ((z >> 4) + 1) * 3, zl = (yl | (z & 15)) << 4;
      let i = LI(-LP, y, z);
      for (let x = -LP; x < 16 + LP; x++, i++) {
        const n = zn + (x >> 4) + 1, c = nb[n];
        if (!c) { lightOp[i] = gap[n] === -1 ? 15 : 0; continue; }
        const id = c[zl | (x & 15)];
        if (!id) { lightOp[i] = 0; continue; }
        lightOp[i] = opacityOf(id);
        const e = emitOf(id);
        if (e) { lightBlk[i] = e; if (e > 1) buckets[e].push(i); }
      }
    }
  }
  spread(lightBlk);
  const entries = [];
  for (let dz = -1; dz <= 1; dz++) for (let dx = -1; dx <= 1; dx++) entries.push(skyEntry(cx + dx, cy + 1, cz + dz));
  for (let z = -LP; z < 16 + LP; z++) for (let x = -LP; x < 16 + LP; x++) {
    const col = ((z >> 4) + 1) * 3 + (x >> 4) + 1;
    let v = entries[col][(z & 15) * 16 + (x & 15)];
    // The upper half of the sub-chunk above is not in the region, but what
    // sits there still shades it.
    const above = nb[18 + col];
    for (let y = 15; y >= 8 && v > 0; y--) {
      if (!above) { if (gap[18 + col] === -1) v = 0; continue; }
      const id = above[(((y << 4) | (z & 15)) << 4) | (x & 15)];
      if (id) { const o = opacityOf(id); v = o >= 15 ? 0 : v > o ? v - o : 0; }
    }
    let i = LI(x, 15 + LP, z);
    for (let y = 15 + LP; y >= -LP && v > 0; y--, i -= LW * LW) {
      const o = lightOp[i];
      if (o) v = o >= 15 ? 0 : v > o ? v - o : 0;
      if (v > 0) { lightSky[i] = v; if (v > 1) buckets[v].push(i); }
    }
  }
  spread(lightSky);
}

// Light at the four corners of a face of cell (x, y, z): the cell the face
// looks into and its three neighbours around each corner, averaged, which
// is the game's smooth lighting. Solid neighbours do not count.
const LT = new Array(8);
function faceLight(x, y, z, f) {
  let a = LI(x + f.d[0], y + f.d[1], z + f.d[2]);
  if (lightOp[a] >= 15) { const own = LI(x, y, z); if (lightOp[own] < 15) a = own; }
  const t1 = (f.axis + 1) % 3, t2 = (f.axis + 2) % 3;
  for (let k = 0; k < 4; k++) {
    const q = f.c[k];
    const o1 = q[t1] ? STRIDE[t1] : -STRIDE[t1], o2 = q[t2] ? STRIDE[t2] : -STRIDE[t2];
    let sk = lightSky[a], bl = lightBlk[a], n = 1;
    for (const i of [a + o1, a + o2, a + o1 + o2]) if (lightOp[i] < 15) { sk += lightSky[i]; bl += lightBlk[i]; n++; }
    LT[2 * k] = Math.round(sk / n * 17); LT[2 * k + 1] = Math.round(bl / n * 17);
  }
  return LT;
}
const CELL_LT = new Array(8);
const FULL_LT = new Array(8).fill(255);
function cellLight(x, y, z) {
  const i = LI(x, y, z);
  for (let k = 0; k < 4; k++) { CELL_LT[2 * k] = lightSky[i] * 17; CELL_LT[2 * k + 1] = lightBlk[i] * 17; }
  return CELL_LT;
}

function isAir(cx, cy, cz) {
  const col = columns.get(cx + ',' + cz);
  if (!col) return false;
  for (const y of col) if (y < cy) return true;
  return false;
}

// Records a sub-chunk in its column. Returns the column's lowest height before,
// Infinity when it had nothing.
function track(key) {
  const [cx, cy, cz] = key.split(',').map(Number);
  const ck = cx + ',' + cz;
  let col = columns.get(ck);
  if (!col) columns.set(ck, col = new Set());
  let min = Infinity;
  for (const y of col) if (y < min) min = y;
  col.add(cy);
  return min;
}

// A column that just became known changes what its neighbours' edge faces look
// into at every height; one whose floor dropped, only from the new floor up to
// the old one.
function markColumnRing(key, lo, hi) {
  const [cx, , cz] = key.split(',').map(Number);
  for (let dx = -1; dx <= 1; dx++) for (let dz = -1; dz <= 1; dz++) {
    const col = columns.get((cx + dx) + ',' + (cz + dz));
    if (col) for (const y of col) if (y >= lo && y <= hi) dirty.add((cx + dx) + ',' + y + ',' + (cz + dz));
  }
}

// Returns the non-empty buffers as { k, p, c, l, u, i, box }. k: 0 textured,
// 1 textured translucent, 2 flat, 3 flat translucent. c is rgba, l is (sky,
// block) light per vertex as bytes, u is (s, t, layer) per vertex. box is the tight [minx, miny, minz, maxx, maxy,
// maxz] of the vertices, for the page's culling.
function mesh(key) {
  const [cx, cy, cz] = key.split(',').map(Number);
  const b = blocks.get(key);
  if (!b) return [];
  // The 3x3x3 chunks around this one, so a neighbour lookup is an array read
  // rather than a string-keyed map lookup per face and per occlusion probe.
  const nb = [], gap = [];
  for (let dy = -1; dy <= 1; dy++) for (let dz = -1; dz <= 1; dz++) for (let dx = -1; dx <= 1; dx++) {
    const c = blocks.get((cx + dx) + ',' + (cy + dy) + ',' + (cz + dz));
    nb.push(c);
    // A sub-chunk that was never sent inside a column that has others is all
    // air; hiding the faces that look into it would cut holes in cliffs and
    // at the top of every full sub-chunk. Below the column's lowest sub-chunk
    // is the world floor, which stays hidden.
    gap.push(c || isAir(cx + dx, cy + dy, cz + dz) ? 0 : -1);
  }
  // Local coordinates run -1..16 for the ring of cells just outside the chunk.
  const at = (x, y, z) => {
    const n = ((y >> 4) + 1) * 9 + ((z >> 4) + 1) * 3 + (x >> 4) + 1;
    const c = nb[n];
    return c ? c[(((y & 15) << 4) | (z & 15)) << 4 | (x & 15)] : gap[n];
  };
  const solid = (x, y, z) => { const n = at(x, y, z); return n > 0 && info(n).opaque; };
  computeLight(cx, cy, cz, nb, gap);
  const buf = [0, 1, 2, 3].map(() => ({ p: [], c: [], l: [], u: [], i: [] }));
  const cell = [0, 0, 0];
  for (let y = 0; y < 16; y++) for (let z = 0; z < 16; z++) for (let x = 0; x < 16; x++) {
    const id = b[((y << 4 | z) << 4) | x];
    if (!id) continue;
    const inf = info(id);
    if (inf.shape === 'skip') continue;
    const wx = cx * 16 + x, wy = cy * 16 + y, wz = cz * 16 + z;
    const textured = !!inf.blk;
    const g = buf[(textured ? 0 : 2) + (inf.trans ? 1 : 0)];
    const flat = rgb(id), j = jitter(wx, wy, wz);

    if (inf.shape === 'cross') {
      // The pack names a double plant's lower half under down; side holds an
      // unrelated flower texture for it.
      // Flames glow whatever light the cell has; fire_0 is the pack's first frame.
      const face = inf.fire ? inf.blk.side : inf.blk.down, col = faceColor(inf, face, wx, wz), lt = inf.fire ? FULL_LT : cellLight(x, y, z);
      const uv = faceUVW(face, [[0, 0], [1, 0], [1, 1], [0, 1]]);
      for (const q of [[[0,0,0],[1,0,1],[1,1,1],[0,1,0]], [[1,0,0],[0,0,1],[0,1,1],[1,1,0]]])
        for (const backface of [false, true])
          quad(g, q.map(v => [wx + v[0], wy + v[1], wz + v[2]]), col, 1, 1, uv, null, backface, lt);
      continue;
    }
    let boxes = inf.shape === 'cube' ? FULL : inf.boxes || BOXES[inf.shape] || FULL;
    // The surface of a water block with nothing on top sits below the block's
    // edge, as in the game; with water or a solid above it fills the block.
    if (inf.water) {
      const above = at(x, y + 1, z);
      if (above !== id && !(above > 0 && info(above).opaque) && above !== -1) boxes = WATER_TOP;
    }
    // Occlusion is only defined for whole-block faces of opaque cubes.
    const lit = inf.shape === 'cube' && (inf.opaque || inf.leaves);
    for (const box of boxes) {
      for (const f of FACES) {
        const flush = f.d[f.axis] > 0 ? box[f.axis + 3] === 1 : box[f.axis] === 0;
        if (flush) {
          const n = at(x + f.d[0], y + f.d[1], z + f.d[2]);
          if (n === -1) continue;                        // unloaded: hide until it arrives
          if (n === id && inf.shape === 'cube' && !inf.leaves) continue;
          if (n !== 0 && info(n).opaque) continue;       // opaque neighbour
        }
        const pos = [], st = [];
        for (const q of f.c) {
          const p = [0, 1, 2].map(k => box[k] + q[k] * (box[k + 3] - box[k]));
          pos.push([wx + p[0], wy + p[1], wz + p[2]]);
          st.push([f.sx ? p[f.ax] : 1 - p[f.ax], f.sy ? p[f.ay] : 1 - p[f.ay]]);
        }
        let ao = null;
        if (lit) {
          // Classic corner rule: two solid sides shut the corner completely,
          // otherwise each solid side or corner cell darkens it a step.
          ao = f.c.map(q => {
            const t1 = (f.axis + 1) % 3, t2 = (f.axis + 2) % 3;
            const s1 = q[t1] ? 1 : -1, s2 = q[t2] ? 1 : -1;
            cell[0] = x; cell[1] = y; cell[2] = z; cell[f.axis] += f.d[f.axis];
            const ax = cell[0], ay = cell[1], az = cell[2];
            const side = (u, v) => {
              cell[0] = ax; cell[1] = ay; cell[2] = az; cell[t1] += u; cell[t2] += v;
              return solid(cell[0], cell[1], cell[2]) ? 1 : 0;
            };
            const a = side(s1, 0), c = side(0, s2);
            return AO_LEVEL[a && c ? 3 : a + c + side(s1, s2)];
          });
        }
        const lt = faceLight(x, y, z, f);
        if (textured) {
          const face = inf.blk[f.n];
          let alpha = inf.alpha;
          if (inf.water && biomeTable) { const t = columnTints(wx >> 4, wz >> 4); if (t) alpha = t.alpha[(wx & 15) * 16 + (wz & 15)]; }
          quad(g, pos, faceColor(inf, face, wx, wz), alpha, SHADE[f.n], faceUVW(face, st), ao, false, lt);
        } else quad(g, pos, flat, inf.trans ? Math.min(inf.alpha, .6) : 1, SHADE[f.n] * j, null, ao, false, lt);
      }
    }
  }
  const out = [];
  buf.forEach((g, k) => {
    if (!g.i.length) return;
    const box = [Infinity, Infinity, Infinity, -Infinity, -Infinity, -Infinity];
    for (let n = 0; n < g.p.length; n += 3) for (let a = 0; a < 3; a++) {
      const v = g.p[n + a];
      if (v < box[a]) box[a] = v;
      if (v > box[a + 3]) box[a + 3] = v;
    }
    out.push({ k, p: new Float32Array(g.p), c: new Float32Array(g.c), l: new Uint8Array(g.l), u: new Float32Array(g.u), i: new Uint32Array(g.i), box });
  });
  return out;
}

function markDirty(key, neighbours) {
  dirty.add(key);
  if (!neighbours) return;
  const [cx, cy, cz] = key.split(',').map(Number);
  // The whole 3x3x3 ring mesh() reads: an occlusion probe at a chunk corner
  // reaches the edge and corner neighbours as well as the face ones.
  for (let dx = -1; dx <= 1; dx++) for (let dy = -1; dy <= 1; dy++) for (let dz = -1; dz <= 1; dz++) {
    if (!dx && !dy && !dz) continue;
    const k = (cx + dx) + ',' + (cy + dy) + ',' + (cz + dz);
    if (blocks.has(k)) dirty.add(k);
  }
}

function schedule() {
  if (scheduled) return;
  scheduled = true;
  setTimeout(run, 0);
}

// Nearest-first, in slices short enough that new messages are read between
// them, so a burst of chunks cannot starve edits or the camera position.
function run() {
  scheduled = false;
  if (!dirty.size && lightDirty.size) {
    for (const k of lightDirty) dirty.add(k);
    lightDirty.clear();
  }
  if (dirty.size) {
    const ordered = [...dirty].map(k => {
      const [a, b, c] = k.split(',').map(Number);
      return [k, (a * 16 + 8 - at.x) ** 2 + (b * 16 + 8 - at.y) ** 2 + (c * 16 + 8 - at.z) ** 2];
    }).sort((p, q) => p[1] - q[1]);
    const until = performance.now() + 15;
    for (const [k] of ordered) {
      if (performance.now() > until) break;
      dirty.delete(k);
      const parts = mesh(k);
      const transfer = [];
      for (const q of parts) transfer.push(q.p.buffer, q.c.buffer, q.l.buffer, q.u.buffer, q.i.buffer);
      postMessage({ t: 'mesh', epoch, key: k, parts }, transfer);
    }
  }
  postMessage({ t: 'status', epoch, seq, queued: dirty.size });
  if (dirty.size) schedule();
}

onmessage = ev => {
  const m = ev.data;
  if (m.seq !== undefined) seq = m.seq;
  switch (m.t) {
    case 'mats': {
      // A replacing message is a new connection's table; otherwise the
      // entries are new ids, and only a cell that already named one needs
      // meshing again, so a stream that keeps discovering blocks does not
      // re-mesh the world each time.
      let stale = m.replace;
      if (m.replace) { mats = []; missing.clear(); colorCache.clear(); forgetInfo(); }
      for (const [id, entry] of m.mats) {
        mats[id - 1] = entry;
        colorCache.delete(id); forgetInfo(id);
        if (missing.delete(id)) stale = true;
      }
      if (stale) for (const k of blocks.keys()) dirty.add(k);
      break;
    }
    case 'atlas':
      atlas = m.atlas; forgetInfo(); skyCache.clear(); tintCache.clear();
      for (const k of blocks.keys()) dirty.add(k);
      break;
    case 'set': {
      let floor;
      const cy = Number(m.key.split(',')[1]);
      withSkyCheck(m.key, () => { blocks.set(m.key, m.cells); floor = track(m.key); });
      if (floor === Infinity) markColumnRing(m.key, -Infinity, Infinity);
      else if (cy < floor) markColumnRing(m.key, cy, floor);
      markDirty(m.key, true);
      break;
    }
    case 'edit': {
      const b = blocks.get(m.key);
      if (b) { withSkyCheck(m.key, () => { b[m.i] = m.v; }); markDirty(m.key, true); }
      break;
    }
    case 'biomes':
      biomeTable = new Map(m.table.map(e => [e.id, e]));
      tintCache.clear();
      for (const k of blocks.keys()) dirty.add(k);
      break;
    case 'biome': {
      biomeCols.set(m.cx + ',' + m.cz, m.ids);
      for (let dx = -1; dx <= 1; dx++) for (let dz = -1; dz <= 1; dz++) {
        const ck = (m.cx + dx) + ',' + (m.cz + dz);
        tintCache.delete(ck);
        // Only a mesh that was tinted from other biomes needs redoing.
        if (!biomeTable) continue;
        const ys = columns.get(ck);
        if (ys) for (const y of ys) dirty.add((m.cx + dx) + ',' + y + ',' + (m.cz + dz));
      }
      break;
    }
    case 'clear':
      blocks.clear(); columns.clear(); dirty.clear(); lightDirty.clear(); missing.clear(); skyCache.clear();
      biomeCols.clear(); tintCache.clear(); epoch = m.epoch;
      break;
    case 'at':
      at = m.at;
      return;
  }
  schedule();
};
