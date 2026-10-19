// Sub-chunk mesher, run as a Web Worker so the page's frame loop never waits
// on it. Builds per-sub-chunk geometry with only the faces next to a
// non-opaque neighbour, the same technique prismarine-viewer's mesher uses; no
// code is copied. Colours arrive already converted to the renderer's working
// space, so this file needs no three.js.
'use strict';

const blocks = new Map(); // "cx,cy,cz" -> Uint8Array(4096)
const dirty = new Set();
const infoCache = new Map();
const colorCache = new Map();
let mats = [];            // index = id - 1
let atlas = null;         // { table, tints: {kind: [r, g, b]} }
let epoch = 0;
let seq = 0;              // last message seq received from the page
let at = { x: 0, y: 70, z: 0 };
let scheduled = false;

const FACES = [ // name, dir, corners (ccw seen from outside, bottom-left first), shade
  { n: 'east',  d: [1, 0, 0],  c: [[1,0,1],[1,0,0],[1,1,0],[1,1,1]], s: .8 },
  { n: 'west',  d: [-1, 0, 0], c: [[0,0,0],[0,0,1],[0,1,1],[0,1,0]], s: .8 },
  { n: 'up',    d: [0, 1, 0],  c: [[0,1,1],[1,1,1],[1,1,0],[0,1,0]], s: 1 },
  { n: 'down',  d: [0, -1, 0], c: [[0,0,0],[1,0,0],[1,0,1],[0,0,1]], s: .5 },
  { n: 'south', d: [0, 0, 1],  c: [[0,0,1],[1,0,1],[1,1,1],[0,1,1]], s: .65 },
  { n: 'north', d: [0, 0, -1], c: [[1,0,0],[0,0,0],[0,1,0],[1,1,0]], s: .65 },
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

function blockAt(wx, wy, wz) {
  const b = blocks.get((wx >> 4) + ',' + (wy >> 4) + ',' + (wz >> 4));
  if (!b) return -1; // not loaded
  return b[(((wy & 15) << 4) | (wz & 15)) << 4 | (wx & 15)];
}
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
function info(id) {
  let r = infoCache.get(id);
  if (r) return r;
  const m = mats[id - 1];
  const blk = (m && atlas && atlas.table.blocks['minecraft:' + m.name]) || null;
  let shape = 'cube';
  if (blk) {
    const k = blk.model;
    // A name the flat list marks as non-cube stays hidden when the table
    // would draw it as a cube, which is how it hid before textures existed.
    shape = k === 'liquid' ? 'cube' : k === 'none' || (k === 'cube' && m.nonCube) ? 'skip' : k;
  } else if (m && m.nonCube) shape = 'skip';
  const trans = !!m && m.alpha < 1;
  r = { m, blk, shape, trans, opaque: shape === 'cube' && !trans, tint: blk && atlas.tints[blk.tint] || null };
  infoCache.set(id, r);
  return r;
}

function faceColor(inf, face) {
  if (face.tinted && inf.tint) return inf.tint;
  // The pack's per-texture overlay colour is not applied: it is a legacy
  // multiplier that turns grass sides orange.
  return WHITE;
}

// One quad. uv is null for flat colour.
function quad(g, pos, col, shade, uv, flip) {
  const base = g.p.length / 3;
  for (let k = 0; k < 4; k++) {
    g.p.push(pos[k][0], pos[k][1], pos[k][2]);
    g.c.push(col[0] * shade, col[1] * shade, col[2] * shade);
    if (uv) g.u.push(uv[k][0], uv[k][1]);
  }
  g.i.push(...(flip ? [base, base + 2, base + 1, base, base + 3, base + 2] : [base, base + 1, base + 2, base, base + 2, base + 3]));
}

// Texture coordinates for a face rectangle given (s,t) in 0..1 across it,
// t up. The rectangle is inset a hair so nearest sampling at a tile edge
// never picks up the neighbouring tile.
function faceUV(r, st) {
  const e = 0.02 / atlas.table.width, f = 0.02 / atlas.table.height;
  const u0 = r[0] + e, u1 = r[2] - e, v0 = r[1] + f, v1 = r[3] - f;
  return st.map(([s, t]) => [u0 + s * (u1 - u0), v1 - t * (v1 - v0)]);
}

// Returns the non-empty buffers as { k, p, c, u, i, box }. k: 0 textured,
// 1 textured translucent, 2 flat, 3 flat translucent. box is the tight
// [minx, miny, minz, maxx, maxy, maxz] of the vertices, for the page's culling.
function mesh(key) {
  const [cx, cy, cz] = key.split(',').map(Number);
  const b = blocks.get(key);
  if (!b) return [];
  const buf = [0, 1, 2, 3].map(() => ({ p: [], c: [], u: [], i: [] }));
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
      const uv = faceUV(face.uv, [[0, 0], [1, 0], [1, 1], [0, 1]]);
      for (const q of [[[0,0,0],[1,0,1],[1,1,1],[0,1,0]], [[1,0,0],[0,0,1],[0,1,1],[1,1,0]]])
        for (const flip of [false, true])
          quad(g, q.map(v => [wx + v[0], wy + v[1], wz + v[2]]), col, .9, uv, flip);
      continue;
    }
    for (const box of inf.shape === 'cube' ? FULL : BOXES[inf.shape] || FULL) {
      for (const f of FACES) {
        const flush = f.d[f.axis] > 0 ? box[f.axis + 3] === 1 : box[f.axis] === 0;
        if (flush) {
          const n = blockAt(wx + f.d[0], wy + f.d[1], wz + f.d[2]);
          if (n === -1) continue;                        // unloaded: hide until it arrives
          if (n === id && inf.shape === 'cube') continue;
          if (n !== 0 && info(n).opaque) continue;       // opaque neighbour
        }
        const pos = [], st = [];
        for (const q of f.c) {
          const p = [0, 1, 2].map(k => box[k] + q[k] * (box[k + 3] - box[k]));
          pos.push([wx + p[0], wy + p[1], wz + p[2]]);
          st.push([f.sx ? p[f.ax] : 1 - p[f.ax], f.sy ? p[f.ay] : 1 - p[f.ay]]);
        }
        if (textured) {
          const face = inf.blk[f.n];
          quad(g, pos, faceColor(inf, face), f.s, faceUV(face.uv, st), false);
        } else quad(g, pos, flat, f.s * j, null, false);
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
  for (const [dx, dy, dz] of [[1,0,0],[-1,0,0],[0,1,0],[0,-1,0],[0,0,1],[0,0,-1]]) {
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
      markDirty(m.key, true);
      break;
    case 'edit': {
      const b = blocks.get(m.key);
      if (b) { b[m.i] = m.v; markDirty(m.key, true); }
      break;
    }
    case 'clear':
      blocks.clear(); dirty.clear(); epoch = m.epoch;
      break;
    case 'at':
      at = m.at;
      return;
  }
  schedule();
};
