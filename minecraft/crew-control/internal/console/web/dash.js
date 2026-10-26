// Pure layout logic for the dashboard: no DOM, so node can test it. A layout
// is an ordered list of panels, each with a type and a size on a 12-column
// grid; order is placement, so reordering needs no coordinates.

export const COLS = 12;
export const WIDTHS = [3, 4, 6, 8, 12];
export const MAX_ROWS = 4;
export const MAX_PANELS = 24;
export const MAX_SAVED = 12;
export const NAME_MAX = 40;
export const STORE_KEY = 'crew-control.dashboard.v1';

const clamp = (n, lo, hi) => Math.min(hi, Math.max(lo, n));

// The nearest allowed width, so a hand-edited or older value still lands on
// the grid instead of leaving a ragged row.
export function snapWidth(w) {
  const n = Number(w);
  if (!Number.isFinite(n)) return WIDTHS[2];
  return WIDTHS.reduce((best, c) => (Math.abs(c - n) < Math.abs(best - n) ? c : best), WIDTHS[0]);
}

export const snapRows = h => (Number.isFinite(Number(h)) ? clamp(Math.round(Number(h)), 1, MAX_ROWS) : 2);

const newId = used => {
  let i = used.size + 1;
  while (used.has(`p${i}`)) i++;
  return `p${i}`;
};

// Anything read back from storage is untrusted: unknown panel types are
// dropped (a release may remove one) and sizes are snapped.
export function normalizeLayout(raw, known) {
  const out = [];
  const used = new Set();
  const list = raw && Array.isArray(raw.panels) ? raw.panels : [];
  for (const p of list) {
    if (out.length >= MAX_PANELS) break;
    if (!p || typeof p.type !== 'string' || !known.has(p.type)) continue;
    let id = typeof p.id === 'string' && /^[a-z0-9-]{1,16}$/.test(p.id) ? p.id : '';
    if (!id || used.has(id)) id = newId(used);
    used.add(id);
    out.push({ id, type: p.type, w: snapWidth(p.w), h: snapRows(p.h) });
  }
  return { panels: out };
}

export function addPanel(layout, type, size = {}) {
  if (layout.panels.length >= MAX_PANELS) return layout;
  const used = new Set(layout.panels.map(p => p.id));
  const panel = { id: newId(used), type, w: snapWidth(size.w ?? 4), h: snapRows(size.h ?? 2) };
  return { panels: [...layout.panels, panel] };
}

export const removePanel = (layout, id) => ({ panels: layout.panels.filter(p => p.id !== id) });

export function movePanel(layout, id, to) {
  const from = layout.panels.findIndex(p => p.id === id);
  if (from < 0) return layout;
  const dest = clamp(to, 0, layout.panels.length - 1);
  if (dest === from) return layout;
  const panels = [...layout.panels];
  const [p] = panels.splice(from, 1);
  panels.splice(dest, 0, p);
  return { panels };
}

export function resizePanel(layout, id, size) {
  return {
    panels: layout.panels.map(p => (p.id === id ? { ...p, w: size.w == null ? p.w : snapWidth(size.w), h: size.h == null ? p.h : snapRows(size.h) } : p)),
  };
}

// Steps to the next allowed width, for the keyboard.
export function stepWidth(w, dir) {
  const i = WIDTHS.indexOf(snapWidth(w));
  return WIDTHS[clamp(i + dir, 0, WIDTHS.length - 1)];
}

// ---- saved layouts ----

export function cleanName(name) {
  return String(name ?? '').replace(/[\u0000-\u001f]/g, '').trim().slice(0, NAME_MAX);
}

const emptyStore = () => ({ current: null, saved: {} });

// Storage can be missing, full, or hold anything: every failure reads as an
// empty store, and the dashboard falls back to its default layout.
export function loadStore(storage, known) {
  let raw;
  try {
    raw = JSON.parse(storage.getItem(STORE_KEY));
  } catch {
    return emptyStore();
  }
  if (!raw || typeof raw !== 'object') return emptyStore();
  const saved = {};
  for (const [name, l] of Object.entries(raw.saved && typeof raw.saved === 'object' ? raw.saved : {})) {
    const n = cleanName(name);
    if (n && Object.keys(saved).length < MAX_SAVED) saved[n] = normalizeLayout(l, known);
  }
  return { current: raw.current ? normalizeLayout(raw.current, known) : null, saved };
}

// Returns false when the browser refused the write (private mode, quota), so
// the page can say the layout will not survive a reload.
export function persist(storage, store) {
  try {
    storage.setItem(STORE_KEY, JSON.stringify(store));
    return true;
  } catch {
    return false;
  }
}

export function saveNamed(store, name, layout) {
  const n = cleanName(name);
  if (!n) return { error: 'Name the layout' };
  if (!(n in store.saved) && Object.keys(store.saved).length >= MAX_SAVED) return { error: `At most ${MAX_SAVED} saved layouts` };
  return { store: { ...store, saved: { ...store.saved, [n]: layout } }, name: n };
}

export function deleteNamed(store, name) {
  const saved = { ...store.saved };
  delete saved[name];
  return { ...store, saved };
}
