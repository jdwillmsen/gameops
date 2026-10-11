'use strict';

// Markers that stand in for a heap of them. From far out, nine hundred
// mobs round a player or two thousand chests and beds across a world are
// plates lying on plates, and what the map then says is only that there
// is a heap. Where the viewer has asked for it, the markers of one family
// that fall in one square of the screen are drawn as a single mark that
// says how many they are and, where most are one thing, what.
//
// This is the sorting only, with nothing of the page in it: the layers
// hand over what they would draw and are told which of it to draw as one.
(() => {
  const app = window.mcmap;
  if (!app) return;

  // The side of a square, in CSS pixels: three plates of the usual size
  // across, so that what is left ungrouped has room to be told apart.
  const CELL = 48;
  // How wide a group's mark is, in CSS pixels, by how many it stands
  // for: up to two dozen, a crowd, a horde. Never by the count itself,
  // and never so wide that the marks of two squares side by side touch:
  // the widest leaves ten pixels between them.
  const WIDTHS = [26, 32, 38];
  // Grouping is from a block to two pixels outwards. From a block to the
  // pixel inwards a square is 48 blocks or fewer, which is a field or a
  // room: there everything is drawn by itself, as it is with grouping off.
  const FROM_ZOOM = -1;
  // How far past the edge of its square, as a part of the square's side,
  // a member is still counted in it: a mob that wanders back and forth
  // over an edge does not make two marks blink.
  const SLACK = 0.15;
  // Members no further apart than this, in blocks, cannot be told apart
  // by coming closer: at a block to the pixel they are one heap still.
  const TOGETHER = 16;

  const active = (zoom) => Number.isFinite(zoom) && zoom <= FROM_ZOOM;
  // A square's side in blocks at a zoom. A square is a square of the
  // world and not of the screen, so that moving the map moves the groups
  // with it and regroups nothing.
  const sideAt = (zoom) => CELL / 2 ** zoom;

  // Sorts items into groups, one to a square at most. Each item is
  // { id, x, z, family, type }; an item with alone set is never grouped.
  // was is the answer's own memory from the time before, or nothing: the
  // square each item was last in, which it stays in while it is within
  // the slack of it.
  //
  // The answer is { groups, single, memory }: groups as { key, x, z,
  // members, families, types }, where x and z are the middle of the
  // square, families is how many there are of each family, most first,
  // and types how many of each type of each family, as [family, type,
  // count]; single is every item drawn by itself. Every item given is in
  // exactly one of the two, and a group has two members or more.
  //
  // Whatever falls in a square is one group, whichever families it is
  // of: a mark a family would be several marks on one spot, each hiding
  // the last. The mark is in the middle of its square and not of its
  // members, so that no two marks can be nearer than a square apart.
  function cluster(items, zoom, was) {
    const single = [];
    const memory = { zoom, cells: new Map() };
    if (!active(zoom)) return { groups: [], single: [...items], memory };
    const side = sideAt(zoom);
    const before = was && was.zoom === zoom ? was.cells : null;
    const cells = new Map();
    for (const item of items) {
      if (item.alone || !Number.isFinite(item.x) || !Number.isFinite(item.z)) {
        single.push(item);
        continue;
      }
      let cx = Math.floor(item.x / side);
      let cz = Math.floor(item.z / side);
      const last = before ? before.get(item.id) : undefined;
      if (last !== undefined) {
        const within = (at, cell) => at >= (cell - SLACK) * side && at < (cell + 1 + SLACK) * side;
        if (within(item.x, last[0]) && within(item.z, last[1])) [cx, cz] = last;
      }
      memory.cells.set(item.id, [cx, cz]);
      const key = `${cx}|${cz}`;
      const cell = cells.get(key);
      if (cell) cell.at.push(item); else cells.set(key, { cx, cz, at: [item] });
    }
    const groups = [];
    for (const [key, { cx, cz, at: members }] of cells) {
      if (members.length < 2) {
        single.push(members[0]);
        continue;
      }
      const families = new Map();
      const types = new Map();
      for (const m of members) {
        families.set(m.family, (families.get(m.family) || 0) + 1);
        const of = `${m.family}\n${m.type}`;
        const held = types.get(of);
        if (held) held[2] += 1; else types.set(of, [m.family, m.type, 1]);
      }
      const most = (a, b) => b[b.length - 1] - a[a.length - 1] || String(a[0]).localeCompare(String(b[0])) || String(a[1]).localeCompare(String(b[1]));
      groups.push({
        key,
        x: (cx + 0.5) * side,
        z: (cz + 0.5) * side,
        members,
        families: [...families].sort(most),
        types: [...types.values()].sort(most),
      });
    }
    return { groups, single, memory };
  }

  // Whether a group's members are so near each other that coming closer
  // would not part them.
  function together(members) {
    let minX = Infinity;
    let maxX = -Infinity;
    let minZ = Infinity;
    let maxZ = -Infinity;
    for (const m of members) {
      minX = Math.min(minX, m.x);
      maxX = Math.max(maxX, m.x);
      minZ = Math.min(minZ, m.z);
      maxZ = Math.max(maxZ, m.z);
    }
    return maxX - minX <= TOGETHER && maxZ - minZ <= TOGETHER;
  }

  const widthOf = (n) => WIDTHS[n >= 200 ? 2 : n >= 25 ? 1 : 0];
  // The count as a mark says it, which has room for three characters.
  const said = (n) => (n < 1000 ? String(n) : n < 10000 ? `${Math.floor(n / 100) / 10}k` : `${Math.floor(n / 1000)}k`);

  // What is on the map besides the live mobs that may be grouped with
  // them: each layer that has such marks offers them under a name, as
  // { items, apply, colour, title }, where items gives what it would draw
  // now, apply is told which of those are in a group and so not to be
  // drawn, colour is a family's and title what a member is called. One
  // layer draws every group's mark; until it has said it does, nothing
  // is taken off the map, so a page whose scripts are of two ages draws
  // every marker as it always did.
  const sources = new Map();
  let draw = null;
  const changed = () => {
    if (draw) draw();
  };

  app.groups = {
    CELL,
    FROM_ZOOM,
    active,
    cluster,
    together,
    widthOf,
    said,
    sources,
    offer(name, source) {
      sources.set(name, source);
      changed();
    },
    changed,
    drawnBy(fn) {
      draw = fn;
    },
  };
})();
