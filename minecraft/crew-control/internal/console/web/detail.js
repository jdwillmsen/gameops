// Pure logic for the unit detail page: no DOM, so node can test it.
// Relative imports keep it loadable from node as well as the browser.

// The words a slot grid holds per cell; everything an operator learns from a
// colour elsewhere is spelled here, and a slot answers by its label alone.
export const ENDER_SLOTS = 27;
export const HOTBAR_SLOTS = 9;
export const MAIN_SLOTS = 27;
export const ARMOR_SLOTS = ['head', 'chest', 'legs', 'feet'];

// An age spoken the way the freshness line uses it: coarse on purpose —
// a save is minutes old at best, so seconds barely matter past a moment.
export function ageText(iso, now = Date.now()) {
  const t = Date.parse(iso);
  if (Number.isNaN(t)) return '';
  const s = Math.max(0, Math.floor((now - t) / 1000));
  if (s < 10) return 'just now';
  if (s < 90) return `${s}s ago`;
  if (s < 5400) return `${Math.max(1, Math.floor(s / 60))} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)}h ago`;
  return `${Math.floor(s / 86400)}d ago`;
}

const SAVE_STATE = {
  known: (save, now) => {
    const ago = ageText(save.asOf, now);
    return { kind: 'save', text: ago === 'just now' || !ago ? 'from the save, just now' : `from the save, ${ago}` };
  },
  none: () => ({ kind: 'none', text: "this unit's record is not saved yet" }),
  off: () => ({ kind: 'off', text: 'world save not connected' }),
  pending: () => ({ kind: 'pending', text: 'reading the world save' }),
};

// One line says where the answer came from: a unit that is online speaks
// for itself, and only the classes of the words, not their colour, say so.
export function freshness(data, now = Date.now()) {
  if (!data) return { kind: 'unknown', text: '' };
  if (data.live && data.live.state === 'known') return { kind: 'live', text: 'live' };
  const f = SAVE_STATE[data.save && data.save.state];
  return (f || (() => ({ kind: 'unknown', text: 'freshness unknown' })))((data.save || {}), now);
}

const ROMAN = ['', 'I', 'II', 'III', 'IV', 'V', 'VI', 'VII', 'VIII', 'IX', 'X'];
export const roman = n => (n >= 1 && n <= 10 ? ROMAN[n] : String(n));

// One effect chip: name out of the table, or the number the unit reported
// when the table does not know it.
export function effectLabel(e) {
  if (!e) return '';
  const n = e.amplifier > 0 ? ` ${roman(e.amplifier + 1)}` : '';
  return `${e.name || 'effect ' + e.id}${n}`;
}

// Duration arrives in ticks; a negative one is the game's word for never
// running out.
export function effectTime(ticks) {
  if (!Number.isFinite(ticks)) return '';
  if (ticks < 0) return '∞';
  const s = Math.max(0, Math.round(ticks / 20));
  return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`;
}

export function enchantLabel(en) {
  if (!en) return '';
  return `${en.name || 'enchantment ' + en.id} ${roman(en.level)}`;
}

export function enchantText(it) {
  return (it.enchantments || []).map(enchantLabel).join(', ');
}

// How much of an item's durability is left, 0 to 1; null for a name the
// tool table does not price or an undamaged sort.
export function durability(it) {
  if (!it || !it.maxDamage) return null;
  return (it.maxDamage - it.damage) / it.maxDamage;
}

const pretty = n => n.replace(/^minecraft:/, '');

// One slot's whole story as a screen-reader label and tooltip. Every state
// it can show is a phrase, never a colour.
export function slotLabel(it, extra = {}) {
  if (!it) return extra.emptyLabel || 'empty';
  const parts = [];
  if (it.customName) parts.push(`"${it.customName}" ${pretty(it.name)}`);
  else parts.push(pretty(it.name));
  if (it.count > 1) parts.push(`x${it.count}`);
  const en = enchantText(it);
  if (en) parts.push(en);
  if (it.maxDamage) parts.push(`${it.maxDamage - it.damage}/${it.maxDamage} durability left`);
  if (it.contents) parts.push(`holds ${it.contents.length} stack${it.contents.length === 1 ? '' : 's'}`);
  return parts.join(' ');
}

// The inventory list is slots out of order and missing empties; the grids
// are fixed. A slot that came back with no entry is a hole, not a crash.
export function slotGrid(items, count, lo = 0, hi = null) {
  hi = hi === null ? lo + count : hi;
  const out = new Array(count).fill(null);
  for (const it of items || []) {
    if (it.slot < lo || it.slot >= hi || it.slot - lo >= count) continue;
    out[it.slot - lo] = it;
  }
  return out;
}

// Main inventory above the hotbar, as the screens in the game draw it.
export function inventoryRows(items) {
  return {
    main: slotGrid(items, MAIN_SLOTS, HOTBAR_SLOTS, HOTBAR_SLOTS + MAIN_SLOTS),
    hotbar: slotGrid(items, HOTBAR_SLOTS, 0, HOTBAR_SLOTS),
  };
}

export function armorRow(armor) {
  return slotGrid(armor, ARMOR_SLOTS.length, 0, 4);
}

// A chest the save never read says nothing: null in the answer means where
// the page would put one, no grid exists to show.
export function chestState(chest) {
  if (chest == null) return { kind: 'unsaved' };
  const filled = chest.filter(Boolean).length;
  return { kind: filled ? 'filled' : 'empty', filled, total: chest.length, chest };
}

// Arrows run over a slot grid as the picture runs: left and right by one
// cell, up and down a row, all clamped at the ends — a grid is not a ring.
// The page draws every grid one row of nine wide, on any screen.
export function nextSlot(i, count, key) {
  const step = { ArrowRight: 1, ArrowLeft: -1, ArrowDown: 9, ArrowUp: -9 }[key];
  if (step === undefined) return null;
  const at = i + step;
  if (at < 0 || at >= count) return i;
  return at;
}

// The item sheet lookup and the CSS that crops one tile out of it.
// Rects are normalised [x0, y0, x1, y1] with y up, like the camera draws;
// browser backgrounds run top-down, so the top edge is 1 - y1.

export function iconRect(doc, name) {
  if (!doc || !doc.items) return null;
  return doc.items['minecraft:' + pretty(name)] ?? doc.placeholder ?? null;
}

// Percentages only, so one style serves every slot size.
export function iconStyle(rect) {
  if (!rect) return null;
  const sx = rect[2] - rect[0], sy = rect[3] - rect[1];
  if (sx <= 0 || sy <= 0) return null;
  const px = (f, span) => {
    if (span >= 0.999) return 0;
    return Math.round((f / (1 - span)) * 100);
  };
  // A tile outside the sheet's own bounds would tint a neighbour tile;
  // clamp keeps a half-off lookup honest at the sheet edge.
  return {
    backgroundSize: `${Math.min(1000, Math.round(100 / sx))}% ${Math.min(1000, Math.round(100 / sy))}%`,
    backgroundPosition: `${px(rect[0], sx)}% ${px(1 - rect[3], sy)}%`,
  };
}
