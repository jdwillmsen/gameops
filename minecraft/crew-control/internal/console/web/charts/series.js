import { MAX_SERIES } from './palette.js';

export const OTHER_KEY = '__other';

// Colour follows the entity, never its row: a key keeps the slot it was first
// seen in, so filtering series out cannot repaint the survivors.
export function createRegistry(max = MAX_SERIES) {
  const slots = new Map();
  return {
    max,
    slot(key) {
      if (!slots.has(key)) slots.set(key, slots.size);
      return slots.get(key);
    },
  };
}

export const defaultRegistry = createRegistry();

export function slotClass(slot, max = MAX_SERIES) {
  return slot < max ? `cc-s${slot + 1}` : 'cc-other';
}

const COMBINE = {
  sum: (a, b) => a + b,
  max: Math.max,
};

// Series past the palette fold into one grey "Other" series; a ninth hue would
// be indistinguishable from an existing one for colour-blind readers.
export function foldOther(series, registry = defaultRegistry, { combine = 'sum', name = 'Other' } = {}) {
  const kept = [];
  const tail = [];
  for (const s of series) {
    const slot = registry.slot(s.key);
    (slot < registry.max ? kept : tail).push({ ...s, slot });
  }
  kept.sort((a, b) => a.slot - b.slot);
  if (!tail.length) return kept;
  const fn = combine === 'mean' ? COMBINE.sum : COMBINE[combine] || COMBINE.sum;
  const n = tail[0].values.length;
  const values = new Array(n);
  for (let i = 0; i < n; i++) {
    let acc = null;
    let seen = 0;
    for (const s of tail) {
      const v = s.values[i];
      if (v == null || Number.isNaN(v)) continue;
      acc = acc == null ? v : fn(acc, v);
      seen++;
    }
    values[i] = acc == null ? null : combine === 'mean' ? acc / seen : acc;
  }
  kept.push({ key: OTHER_KEY, name: `${name} (${tail.length})`, values, slot: registry.max, other: true });
  return kept;
}
