// Pure logic for the container browser: no DOM, so node can test it.

// Units report a chest only when they visit it, so a quiet hour is normal;
// past this the contents are what was last seen, not what is there.
export const STALE_MS = 10 * 60 * 1000;

const pos = p => `${p.x}, ${p.y}, ${p.z}`;

export const cellLabel = c => `${c.item.replace(/^minecraft:/, '').replace(/_/g, ' ')} x${c.count}`;

// The ledger holds an item total per chest, not slots, so a chest is drawn as
// one cell per kind of item rather than a slot grid that would invent layout.
export function containerList(places, now = Date.now()) {
  const out = [];
  for (const p of places || []) {
    if (p.kind !== 'chest') continue;
    const rows = p.stock || [];
    const cells = rows.filter(s => s.count > 0).map(s => ({ item: s.item, count: s.count })).sort((a, b) => b.count - a.count);
    const bySeen = [...rows].sort((a, b) => Date.parse(b.seen_at) - Date.parse(a.seen_at));
    const units = [...new Set(bySeen.map(s => s.unit).filter(Boolean))];
    const seenAt = bySeen.length ? bySeen[0].seen_at : '';
    const t = Date.parse(seenAt);
    let state = 'unseen';
    if (rows.length && !Number.isNaN(t)) state = now - t > STALE_MS ? 'stale' : cells.length ? 'fresh' : 'empty';
    // An old empty reading is still an old reading.
    out.push({ id: p.id, name: p.name || p.id, seeded: !!p.seeded, pos: pos(p.box.min), cells, units, seenAt, state });
  }
  return out.sort((a, b) => (b.seeded - a.seeded) || a.name.localeCompare(b.name));
}
