// Logic for the heatmap view: how a density answer maps to colour classes
// and to canvas pixels. heat-ui.js paints; nothing here touches the DOM.

export const LEVELS = 7;
export const LAYERS = [
  { id: 'position', label: 'Position', hint: 'where units stand' },
  { id: 'mining', label: 'Mining', hint: 'time on gathering tasks' },
  { id: 'travel', label: 'Travel', hint: 'cells crossed while moving' },
];
export const WINDOWS = ['15m', '1h', '6h'];

// Private plots and shared grounds; the view always shows at least this much
// so an empty or sparse answer still sits on a recognisable map.
export const LAB = { minX: 300, maxX: 459, minZ: -500, maxZ: -373 };

export const layerOf = id => LAYERS.find(l => l.id === id) || LAYERS[0];

// Square-root steps: a linear ramp lets one hotspot (a unit parked in a
// shaft) flatten every other cell to the palest class.
export function levelOf(count, max) {
  if (!(count > 0) || !(max > 0)) return 0;
  return 1 + Math.min(LEVELS - 1, Math.floor(Math.sqrt(count / max) * LEVELS));
}

// The count at which each class starts, for the legend.
export function legendSteps(max) {
  const out = [];
  for (let k = 1; k <= LEVELS; k++) {
    const from = k === 1 ? 1 : Math.max(1, Math.ceil(max * ((k - 1) / LEVELS) ** 2));
    out.push({ level: k, from });
  }
  return out;
}

// World bounds (blocks) covering the lab and every cell in the answer.
export function boundsOf(grid, extra = []) {
  const size = grid.cell || 4;
  let { minX, maxX, minZ, maxZ } = LAB;
  for (const [cx, cz] of grid.cells || []) {
    minX = Math.min(minX, cx * size); maxX = Math.max(maxX, cx * size + size - 1);
    minZ = Math.min(minZ, cz * size); maxZ = Math.max(maxZ, cz * size + size - 1);
  }
  for (const b of extra) {
    minX = Math.min(minX, b.min.x); maxX = Math.max(maxX, b.max.x);
    minZ = Math.min(minZ, b.min.z); maxZ = Math.max(maxZ, b.max.z);
  }
  const pad = 8;
  return { minX: minX - pad, maxX: maxX + pad, minZ: minZ - pad, maxZ: maxZ + pad };
}

// Top-down: +x is right and +z is down the page, as on the radar.
export function fit(bounds, w, h) {
  const bw = bounds.maxX - bounds.minX + 1;
  const bh = bounds.maxZ - bounds.minZ + 1;
  const scale = Math.min(w / bw, h / bh);
  const ox = (w - bw * scale) / 2;
  const oy = (h - bh * scale) / 2;
  return {
    scale,
    x: wx => ox + (wx - bounds.minX) * scale,
    y: wz => oy + (wz - bounds.minZ) * scale,
    wx: px => (px - ox) / scale + bounds.minX,
    wz: py => (py - oy) / scale + bounds.minZ,
  };
}

export function cellIndex(grid) {
  const m = new Map();
  for (const [cx, cz, n] of grid.cells || []) m.set(`${cx},${cz}`, n);
  return m;
}

export function cellAt(grid, index, wx, wz) {
  const size = grid.cell || 4;
  const cx = Math.floor(wx / size), cz = Math.floor(wz / size);
  const n = index.get(`${cx},${cz}`) || 0;
  return { cx, cz, n, x0: cx * size, z0: cz * size, x1: cx * size + size - 1, z1: cz * size + size - 1 };
}

export function hottest(grid, n = 5) {
  return [...(grid.cells || [])].sort((a, b) => b[2] - a[2] || a[0] - b[0] || a[1] - b[1]).slice(0, n);
}

export function summary(grid) {
  const cells = (grid.cells || []).length;
  if (!cells) return 'No samples in this window yet.';
  const scope = grid.unit || 'the fleet';
  return `${cells} cells of ${grid.cell}x${grid.cell} blocks, ${grid.samples} samples from ${scope}, busiest cell ${grid.max}.`;
}

export const heatQuery = ({ layer, window, unit }) => {
  const q = new URLSearchParams({ layer, window });
  if (unit) q.set('unit', unit);
  return `/v1/heatmap?${q}`;
};
