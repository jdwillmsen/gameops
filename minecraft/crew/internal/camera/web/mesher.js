// Sub-chunk mesher, run as a Web Worker so the page's frame loop never waits
// on it. Builds per-sub-chunk geometry with only the faces next to a
// non-opaque neighbour, the same technique prismarine-viewer's mesher uses; no
// code is copied. Colours arrive already converted to the renderer's working
// space, so this file needs no three.js.
'use strict';

const blocks = new Map(); // "cx,cy,cz" -> Uint8Array(4096)
// "cx,cz" -> the cy of every sub-chunk loaded in that column. All-air
// sub-chunks are never sent, so a missing one in a loaded column is air; only
// a column with nothing loaded is unknown.
const columns = new Map();
const dirty = new Set();
const infoCache = new Map();
const colorCache = new Map();
let mats = [];            // index = id - 1
let atlas = null;         // { table, tiles: {"u0,v0,u1,v1": layer}, tints: {kind: [r, g, b]} }
let epoch = 0;
let seq = 0;              // last message seq received from the page
let at = { x: 0, y: 70, z: 0 };
let scheduled = false;

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
// Full-width blocks the table calls cubes but whose top sits below the edge.
const SUNKEN = { grass_path: 15 / 16, dirt_path: 15 / 16, farmland: 15 / 16 };
const WATER = /^(water|flowing_water)$/;
const WATER_TOP = [[0, 0, 0, 1, 14 / 16, 1]];
// The page converts to linear light but the game multiplies shade and
// occlusion on the encoded colour, so these factors are raised to the same
// power before they are baked into vertex colours.
const lin = v => Math.pow(v, 2.2);
const AO_LEVEL = [1, .85, .7, .55].map(lin);
const SHADE = Object.fromEntries(FACES.map(f => [f.n, lin(f.s)]));

function jitter(x, y, z) {
  let h = (x * 73856093) ^ (y * 19349663) ^ (z * 83492791);
  h = (h ^ (h >>> 13)) * 1274126177; h ^= h >>> 16;
  return 0.9 + ((h & 255) / 255) * 0.2;
}
function rgb(id) {
  let c = colorCache.get(id);
  if (!c) { const m = mats[id - 1]; c = m ? m.rgb : [1, 0, 1]; colorCache.set(id, c); }
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
  } else if (m && m.nonCube) shape = 'skip';
  let boxes = null;
  if (custom && shape !== 'skip') { shape = 'boxes'; boxes = custom; }
  else if (shape === 'cube' && base in SUNKEN) { shape = 'boxes'; boxes = [[0, 0, 0, 1, SUNKEN[base], 1]]; }
  const trans = !!m && (m.alpha < 1 || BLENDED.test(base));
  const leaves = LEAVES.test(base);
  r = {
    m, blk, shape, boxes, trans, leaves, opaque: shape === 'cube' && !trans && !leaves, water: !!m && WATER.test(base),
    tint: blk && atlas.tints[blk.tint] || null,
    // Water is clear but for its biome colour; the pack's texture alone is too
    // opaque to read as water.
    alpha: m && WATER.test(base) ? .7 : m && m.alpha < 1 ? m.alpha : 1,
  };
  infoCache.set(id, r);
  return r;
}

function faceColor(inf, face) {
  if (face.tinted && inf.tint) return inf.tint;
  // The pack's per-texture overlay colour is not applied: it is a legacy
  // multiplier that turns grass sides orange.
  return WHITE;
}

// One quad. uvw is null for flat colour, else (s, t, layer) per corner. ao
// holds the four corners' brightness; the diagonal joins the brighter pair, so
// a lone dark corner stays in one triangle instead of smearing across the face.
function quad(g, pos, col, a, mul, uvw, ao, backface) {
  const base = g.p.length / 3;
  for (let k = 0; k < 4; k++) {
    const m = mul * (ao ? ao[k] : 1);
    g.p.push(pos[k][0], pos[k][1], pos[k][2]);
    g.c.push(col[0] * m, col[1] * m, col[2] * m, a);
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

function isAir(cx, cy, cz) {
  const col = columns.get(cx + ',' + cz);
  if (!col) return false;
  for (const y of col) if (y < cy) return true;
  return false;
}

// Keeps columns in step with blocks. True when the column had nothing before.
function track(key, add) {
  const [cx, , cz] = key.split(',').map(Number);
  const cy = Number(key.split(',')[1]);
  const ck = cx + ',' + cz;
  let col = columns.get(ck);
  if (add) {
    if (!col) columns.set(ck, col = new Set());
    const first = col.size === 0;
    col.add(cy);
    return first;
  }
  if (col) { col.delete(cy); if (!col.size) columns.delete(ck); }
  return false;
}

// A column that just became known changes what its neighbours' edge faces
// look into, at every height.
function markColumnRing(key) {
  const [cx, , cz] = key.split(',').map(Number);
  for (let dx = -1; dx <= 1; dx++) for (let dz = -1; dz <= 1; dz++) {
    const col = columns.get((cx + dx) + ',' + (cz + dz));
    if (col) for (const y of col) dirty.add((cx + dx) + ',' + y + ',' + (cz + dz));
  }
}

// Returns the non-empty buffers as { k, p, c, u, i, box }. k: 0 textured,
// 1 textured translucent, 2 flat, 3 flat translucent. c is rgba, u is
// (s, t, layer) per vertex. box is the tight [minx, miny, minz, maxx, maxy,
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
  const buf = [0, 1, 2, 3].map(() => ({ p: [], c: [], u: [], i: [] }));
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
      const face = inf.blk.down, col = faceColor(inf, face);
      const uv = faceUVW(face, [[0, 0], [1, 0], [1, 1], [0, 1]]);
      for (const q of [[[0,0,0],[1,0,1],[1,1,1],[0,1,0]], [[1,0,0],[0,0,1],[0,1,1],[1,1,0]]])
        for (const backface of [false, true])
          quad(g, q.map(v => [wx + v[0], wy + v[1], wz + v[2]]), col, 1, 1, uv, null, backface);
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
        if (textured) {
          const face = inf.blk[f.n];
          quad(g, pos, faceColor(inf, face), inf.alpha, SHADE[f.n], faceUVW(face, st), ao, false);
        } else quad(g, pos, flat, inf.trans ? Math.min(inf.alpha, .6) : 1, SHADE[f.n] * j, null, ao, false);
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
    out.push({ k, p: new Float32Array(g.p), c: new Float32Array(g.c), u: new Float32Array(g.u), i: new Uint32Array(g.i), box });
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
      for (const q of parts) transfer.push(q.p.buffer, q.c.buffer, q.u.buffer, q.i.buffer);
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
    case 'mats':
      mats = m.mats; colorCache.clear(); infoCache.clear();
      for (const k of blocks.keys()) dirty.add(k);
      break;
    case 'atlas':
      atlas = m.atlas; infoCache.clear();
      for (const k of blocks.keys()) dirty.add(k);
      break;
    case 'set':
      blocks.set(m.key, m.cells);
      if (track(m.key, true)) markColumnRing(m.key);
      markDirty(m.key, true);
      break;
    case 'edit': {
      const b = blocks.get(m.key);
      if (b) { b[m.i] = m.v; markDirty(m.key, true); }
      break;
    }
    case 'clear':
      blocks.clear(); columns.clear(); dirty.clear(); epoch = m.epoch;
      break;
    case 'at':
      at = m.at;
      return;
  }
  schedule();
};
