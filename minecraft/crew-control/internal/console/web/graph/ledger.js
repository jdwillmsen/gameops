// Pure mapping from GET /v1/places (a place with its stock rows) to the draw
// model of the node graph. No DOM, so node tests run it.
//
// Nodes: a place (chest or area), a resource (an item type) and a unit (the
// one that last read a chest). Edges, each between exactly one pair so the
// graph's de-duplication never hides one:
//   stored    resource -> place   the stock a unit last saw there
//   supplies  area -> resource    the area lists the item (read "supplied by")
//   used      chest -> unit       the unit that last read the chest

import { NODE_W, NODE_H, COL_GAP, ROW_GAP } from './graph.js';

// A reading is a unit's glance at a chest, not a live inventory, so how old it
// is matters as much as what it says.
export const FRESH_MS = 2 * 60 * 1000;
export const STALE_MS = 10 * 60 * 1000;

export const STALE_STATES = ['fresh', 'aging', 'stale', 'unseen'];

export const STALE_TONE = { fresh: 'good', aging: 'info', stale: 'live', unseen: 'plain' };

// Distinct shapes as well as words and tones: a check, a half ring, a
// warning triangle, a dash ring.
export const STALE_ICON = {
  fresh: { icon: 'M2.5 6.5 L5 9 L9.5 3', fill: false },
  aging: { icon: ['M6 2.2a3.8 3.8 0 1 1 0 7.6', 'M6 2.2 V6 L8.4 7.4'], fill: false },
  stale: { icon: 'M6 2 L10.5 10 L1.5 10 Z', fill: true },
  unseen: { icon: 'M6 2.2a3.8 3.8 0 1 1 0 7.6a3.8 3.8 0 1 1 0-7.6z', fill: false },
};

export function ago(ms) {
  if (!Number.isFinite(ms) || ms < 0) return '';
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  return h < 48 ? `${h}h` : `${Math.floor(h / 24)}d`;
}

export function staleState(seenAt, now) {
  const t = Date.parse(seenAt);
  if (Number.isNaN(t)) return 'unseen';
  const age = now - t;
  return age <= FRESH_MS ? 'fresh' : age <= STALE_MS ? 'aging' : 'stale';
}

export function staleChip(seenAt, now) {
  const state = staleState(seenAt, now);
  const t = Date.parse(seenAt);
  const age = state === 'unseen' ? '' : ago(now - t);
  const shape = STALE_ICON[state];
  return {
    state,
    word: age ? `${state} ${age}` : (state === 'unseen' ? 'never seen' : state),
    tone: STALE_TONE[state],
    icon: shape.icon,
    fill: shape.fill,
  };
}

export const legendChips = () => STALE_STATES.map(s => ({
  word: s === 'unseen' ? 'never seen' : s,
  tone: STALE_TONE[s],
  icon: STALE_ICON[s].icon,
  fill: STALE_ICON[s].fill,
}));

export const EDGE_WORDS = { stored: 'stored in', supplies: 'supplied by', used: 'used by' };

const newest = rows => rows.reduce((best, r) => {
  const t = Date.parse(r.seen_at);
  return !Number.isNaN(t) && (best === null || t > best) ? t : best;
}, null);

const centre = p => ({
  x: (p.box.min.x + p.box.max.x) / 2,
  z: (p.box.min.z + p.box.max.z) / 2,
});

export const placeId = id => `p:${id}`;
export const itemId = item => `r:${item}`;
export const unitId = unit => `u:${unit}`;

// places -> {nodes, edges, facts}. facts keys the details panel by node id.
export function ledgerModel(places, now = Date.now()) {
  const nodes = [];
  const edges = [];
  const facts = new Map();
  const items = new Map(); // item -> {total, rows: [{place, count, seen_at}]}
  const units = new Map(); // unit -> chest names
  const item = name => {
    if (!items.has(name)) items.set(name, { total: 0, rows: [], suppliers: [] });
    return items.get(name);
  };

  for (const p of places) {
    const stock = p.stock || [];
    const seen = newest(stock);
    const seenIso = seen === null ? '' : new Date(seen).toISOString();
    const chip = staleChip(seenIso, now);
    const held = stock.reduce((n, s) => n + s.count, 0);
    const id = placeId(p.id);
    const stockText = stock.length ? `${held} items, ${stock.length} kinds` : 'no stock recorded';
    nodes.push({
      id, label: p.name || p.id, sub: `${p.kind} · ${stockText}`, chip, tone: chip.tone,
      note: p.kind === 'area' ? 'area' : 'chest',
      kind: 'place',
    });
    facts.set(id, {
      title: p.name || p.id,
      kind: p.kind,
      seeded: !!p.seeded,
      seenAt: seenIso,
      chip,
      box: p.box,
      stock: stock.map(s => ({ item: s.item, count: s.count, seenAt: s.seen_at, unit: s.unit })),
      items: p.items || [],
    });
    for (const s of stock) {
      const r = item(s.item);
      r.total += s.count;
      r.rows.push({ place: p.name || p.id, count: s.count, seen_at: s.seen_at });
      edges.push({
        from: itemId(s.item), to: id, kind: 'stored',
        title: `${s.item} ${EDGE_WORDS.stored} ${p.name || p.id}`,
      });
      if (s.unit) {
        if (!units.has(s.unit)) units.set(s.unit, new Map());
        units.get(s.unit).set(id, p.name || p.id);
      }
    }
    if (p.kind === 'area') {
      // An area that also holds stock of the item already has its stored
      // edge; the opposite supplies edge would make a two-node cycle.
      const held = new Set(stock.map(s => s.item));
      for (const name of (p.items || []).filter(n => !held.has(n))) {
        item(name).suppliers.push(p.name || p.id);
        edges.push({
          from: id, to: itemId(name), kind: 'supplies',
          title: `${name} ${EDGE_WORDS.supplies} ${p.name || p.id}`,
        });
      }
    }
  }

  const sorted = [...items.keys()].sort();
  for (const name of sorted) {
    const r = items.get(name);
    const seen = newest(r.rows);
    const chip = staleChip(seen === null ? '' : new Date(seen).toISOString(), now);
    const id = itemId(name);
    nodes.push({
      id, label: name, sub: r.rows.length ? `${r.total} in ${r.rows.length} places` : 'no stock recorded',
      chip, tone: chip.tone, note: 'resource', kind: 'resource',
    });
    facts.set(id, {
      title: name, kind: 'resource', total: r.total, chip,
      seenAt: seen === null ? '' : new Date(seen).toISOString(),
      stock: r.rows, suppliers: r.suppliers,
    });
  }

  for (const [unit, chests] of [...units].sort(([a], [b]) => (a < b ? -1 : 1))) {
    const id = unitId(unit);
    nodes.push({
      id, label: unit, sub: `read ${chests.size} ${chests.size === 1 ? 'place' : 'places'}`,
      chip: null, tone: 'plain', note: 'unit', kind: 'unit',
    });
    facts.set(id, { title: unit, kind: 'unit', places: [...chests.values()].sort() });
    for (const [pid, name] of chests) {
      edges.push({ from: pid, to: id, kind: 'used', title: `${name} ${EDGE_WORDS.used} ${unit}` });
    }
  }

  return { nodes, edges, facts };
}

const COL = NODE_W + COL_GAP;
const ROW = NODE_H + ROW_GAP;
const BOX_W = NODE_W / COL;
const BOX_H = NODE_H / ROW;
// Whole map no wider or taller than this many pixels, and never magnified
// past MAX_PX per block, so a lone pair of chests does not sprawl.
const MAP_W = 1400, MAP_H = 900, MAX_PX = 8;

// Grid positions for the map layout: a place stands where its box centre is
// (x east, z south), scaled evenly on both axes. A node that would cover an
// earlier one drops a row. Resources and units have no position, so they stand
// in two columns to the right of the map.
export function mapPlace(model, places) {
  const byId = new Map(places.map(p => [placeId(p.id), p]));
  const spots = [...byId].map(([id, p]) => ({ id, ...centre(p) }));
  const out = new Map();
  let right = 0;
  if (spots.length) {
    const xs = spots.map(s => s.x), zs = spots.map(s => s.z);
    const x0 = Math.min(...xs), z0 = Math.min(...zs);
    const spanX = Math.max(...xs) - x0, spanZ = Math.max(...zs) - z0;
    const k = Math.min(MAX_PX, spanX ? MAP_W / spanX : MAX_PX, spanZ ? MAP_H / spanZ : MAX_PX);
    const placed = [];
    for (const s of spots.sort((a, b) => a.x - b.x || a.z - b.z || (a.id < b.id ? -1 : 1))) {
      const x = ((s.x - x0) * k) / COL;
      let y = ((s.z - z0) * k) / ROW;
      while (placed.some(q => Math.abs(q.x - x) < BOX_W && Math.abs(q.y - y) < BOX_H)) y += 1;
      placed.push({ x, y });
      out.set(s.id, { x, y });
      right = Math.max(right, x + 1);
    }
  }
  for (const kind of ['resource', 'unit']) {
    right = Math.floor(right) + 1;
    let row = 0;
    for (const n of model.nodes) {
      if (n.kind === kind) out.set(n.id, { x: right, y: row++ });
    }
  }
  return out;
}
