// Layered DAG layout in plain JS: longest-path layering, barycentre crossing
// reduction, a critical path (the longest chain; each step counts one unless a
// weight is passed) and a cycle report. Pure and free of the
// DOM, so node tests run it. Callers hand in nodes as {id} and edges as
// {from, to} (from is the dependency that runs before to); nothing here knows
// what a job or a step is.

const key = (a, b) => `${a}\u0000${b}`;

// rank: the node's original position, the tie-break everywhere and what makes
// two layouts of the same input identical.
export function buildGraph(nodes, edges) {
  const ids = nodes.map(n => n.id);
  const present = new Set(ids);
  const rank = new Map(ids.map((id, i) => [id, i]));
  const succ = new Map(ids.map(id => [id, []]));
  const pred = new Map(ids.map(id => [id, []]));
  const seen = new Set();
  for (const e of edges || []) {
    const { from, to } = e;
    if (!present.has(from) || !present.has(to) || from === to || seen.has(key(from, to))) continue;
    seen.add(key(from, to));
    succ.get(from).push(to);
    pred.get(to).push(from);
  }
  // Every list the algorithms walk is rank-sorted once, here, so no later pass
  // can introduce an iteration-order dependence.
  for (const m of [succ, pred]) for (const list of m.values()) list.sort((a, b) => rank.get(a) - rank.get(b));
  return { ids, rank, succ, pred };
}

// Kahn's algorithm over a snapshot: it drains a queue and stops once no node
// has remaining dependencies, so a cycle is reported and can never loop. What
// it cannot drain is a cycle plus everything waiting on one; Tarjan's strongly
// connected components over that remainder tell the two apart. Returns the
// acyclic ordering, the ids on a cycle, the ids only blocked behind one, and
// the remainder's components in dependency order (what layerOf needs).
export function splitCycles(g) {
  const wait = new Map(g.ids.map(id => [id, g.pred.get(id).length]));
  const queue = g.ids.filter(id => wait.get(id) === 0);
  const done = new Set();
  for (let i = 0; i < queue.length; i++) {
    const id = queue[i];
    done.add(id);
    for (const next of g.succ.get(id)) {
      const left = wait.get(next) - 1;
      wait.set(next, left);
      if (left === 0) queue.push(next);
    }
  }
  const stuck = g.ids.filter(id => !done.has(id));
  const stuckSet = new Set(stuck);
  const index = new Map(), low = new Map(), open = new Set();
  const stack = [];
  const comps = [];
  let counter = 0;
  const visit = id => {
    index.set(id, counter);
    low.set(id, counter++);
    stack.push(id);
    open.add(id);
    for (const next of g.succ.get(id)) {
      if (!stuckSet.has(next)) continue;
      if (!index.has(next)) {
        visit(next);
        low.set(id, Math.min(low.get(id), low.get(next)));
      } else if (open.has(next)) {
        low.set(id, Math.min(low.get(id), index.get(next)));
      }
    }
    if (low.get(id) !== index.get(id)) return;
    const comp = [];
    let top;
    do {
      top = stack.pop();
      open.delete(top);
      comp.push(top);
    } while (top !== id);
    comps.push(comp.sort((a, b) => g.rank.get(a) - g.rank.get(b)));
  };
  for (const id of stuck) if (!index.has(id)) visit(id);
  // Tarjan emits a component after everything it reaches, so the reverse is
  // the order dependencies run in.
  comps.reverse();
  const onCycle = new Set(comps.filter(c => c.length > 1).flat());
  return {
    order: queue,
    cyclic: stuck.filter(id => onCycle.has(id)),
    blocked: stuck.filter(id => !onCycle.has(id)),
    comps,
  };
}

// Longest-path layering: layer(n) = 0 without dependencies, else the largest
// layer of a dependency plus one. A cycle cannot be layered node by node, so
// its members share one layer, placed after everything they wait on; what is
// blocked behind a cycle continues from there like any other step.
export function layerOf(g, topo) {
  const layer = new Map();
  const after = (id, own) => {
    let l = 0;
    for (const dep of g.pred.get(id)) {
      if (own.has(dep)) continue;
      const dl = layer.get(dep) + 1;
      if (dl > l) l = dl;
    }
    return l;
  };
  for (const id of topo.order) layer.set(id, after(id, new Set()));
  for (const comp of topo.comps) {
    const own = new Set(comp);
    const l = Math.max(...comp.map(id => after(id, own)));
    for (const id of comp) layer.set(id, l);
  }
  return { layer };
}

// Edge pairs between adjacent layers whose on-screen order inverts — what the
// crossing reduction works to shrink. Longest-path layering can make an edge
// skip a layer; those wider edges are not counted here, which is the usual
// crossing count for the adjacent-layer reduction.
export function crossings(g, layers) {
  let total = 0;
  for (let i = 1; i < layers.length; i++) {
    const above = new Map(layers[i - 1].map((id, r) => [id, r]));
    const below = new Map(layers[i].map((id, r) => [id, r]));
    const pairs = [];
    for (const src of layers[i - 1]) {
      for (const dst of g.succ.get(src)) {
        if (below.has(dst)) pairs.push([above.get(src), below.get(dst)]);
      }
    }
    for (let a = 0; a < pairs.length; a++) {
      for (let b = a + 1; b < pairs.length; b++) {
        if ((pairs[a][0] - pairs[b][0]) * (pairs[a][1] - pairs[b][1]) < 0) total++;
      }
    }
  }
  return total;
}

// Deterministic crossing reduction: each sweep re-orders one layer by the mean
// position of its neighbours in the adjacent layer, ties on the previous slot.
// Four round trips is plenty for job graphs and keeps the result independent
// of how late a neighbour arrives. A node with no neighbours on that side
// keeps its row, so lone ids do not drift on every refresh.
export function reduceCrossings(g, initial, sweeps = 4) {
  const pos = new Map();
  const layers = initial.map(col => col.slice());
  layers.forEach(col => col.forEach((id, r) => pos.set(id, r)));
  const reorder = (l, neighbours) => {
    const scored = layers[l].map((id, r) => {
      const nb = neighbours(id).map(n => pos.get(n));
      const mean = nb.length ? nb.reduce((s, n) => s + n, 0) / nb.length : r;
      return { id, mean, r };
    });
    scored.sort((a, b) => a.mean - b.mean || a.r - b.r);
    layers[l] = scored.map(s => s.id);
    layers[l].forEach((id, r) => pos.set(id, r));
  };
  for (let turn = 0; turn < sweeps; turn++) {
    for (let l = 1; l < layers.length; l++) reorder(l, id => g.pred.get(id));
    for (let l = layers.length - 2; l >= 0; l--) reorder(l, id => g.succ.get(id));
  }
  return layers;
}

// Longest path through the acyclic part, weighted by `weight`. `order` must be the
// topological order from splitCycles; weight defaults to 1 per node, the
// gating chain when no durations are known. Deps are rank-sorted, so a strict
// improvement wins the tie and the choice is stable.
export function criticalPath(g, topo, weight = () => 1) {
  const cyclic = new Set(topo.cyclic);
  const dist = new Map(), back = new Map();
  let last = null;
  for (const id of topo.order) {
    let best = weight(id), from = null;
    for (const dep of g.pred.get(id)) {
      if (cyclic.has(dep) || !dist.has(dep)) continue;
      const d = dist.get(dep) + weight(id);
      if (d > best || (d === best && from === null)) { best = d; from = dep; }
    }
    dist.set(id, best);
    back.set(id, from);
    if (last === null || dist.get(last) < best) last = id;
  }
  const path = [];
  for (let id = last; id; id = back.get(id)) path.push(id);
  return { ids: path.reverse(), cost: last === null ? 0 : dist.get(last) };
}

// One entry point. Returns the layer columns in their reduced order, grid
// coordinates, the critical path ids in path order and the cycle report.
export function layout(nodes, edges, { weight } = {}) {
  const g = buildGraph(nodes, edges);
  const topo = splitCycles(g);
  const { layer } = layerOf(g, topo);
  const cols = new Map();
  for (const id of g.ids) {
    const l = layer.get(id);
    if (!cols.has(l)) cols.set(l, []);
    cols.get(l).push(id);
  }
  const layers = reduceCrossings(g, [...cols.keys()].sort((a, b) => a - b).map(k => cols.get(k)));
  const best = criticalPath(g, topo, weight);
  const pos = new Map();
  layers.forEach((col, li) => col.forEach((id, ri) => pos.set(id, { x: li, y: ri })));
  return {
    layers, layerOf: layer, pos,
    width: layers.length,
    height: layers.reduce((hi, c) => Math.max(hi, c.length), 0),
    critical: new Set(best.ids), criticalIds: best.ids,
    cycle: topo.cyclic, blocked: topo.blocked,
    graph: g,
  };
}
