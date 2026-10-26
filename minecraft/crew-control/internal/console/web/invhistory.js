// Pure logic for the inventory history panel on the unit detail page: how a
// change is worded and ordered. No DOM, so node can test it.

const pretty = n => String(n || '').replace(/^minecraft:/, '').replaceAll('_', ' ');

export const DAY_CHOICES = [1, 7, 30];

// "+12" and "-3": the sign is spelled, so a gain never reads as a count.
export function deltaText(n) {
  return n > 0 ? `+${n}` : n < 0 ? `−${-n}` : '0';
}

export function deltaClass(n) {
  return n > 0 ? 'gain' : n < 0 ? 'loss' : 'same';
}

// One recorded change as a sentence fragment: what moved and from where to
// where, so an item that appeared or vanished reads as such.
export function changeText(c) {
  const name = pretty(c.item);
  if (c.from === 0) return `${name} ${deltaText(c.to)} (new)`;
  if (c.to === 0) return `${name} ${deltaText(-c.from)} (gone)`;
  return `${name} ${deltaText(c.to - c.from)} (${c.from} → ${c.to})`;
}

export function netRows(report) {
  return ((report && report.net) || []).map(n => ({
    item: pretty(n.item), delta: n.delta, text: deltaText(n.delta), cls: deltaClass(n.delta),
  }));
}

export function entryRows(report) {
  return ((report && report.entries) || []).map(e => ({ at: e.at, changes: e.changes.map(changeText) }));
}

// An empty window and a truncated one must not read alike: the first is
// "nothing changed", the second is "this is only the newest part".
export function summary(report, days) {
  const span = days === 1 ? 'the last day' : `the last ${days} days`;
  if (!report) return 'The history could not be read.';
  const n = (report.entries || []).length;
  if (!n) return `No inventory changes recorded in ${span}.`;
  const note = report.truncated ? ' Older changes were left out, so the totals cover only the newest part.' : '';
  return `${n} change${n === 1 ? '' : 's'} recorded in ${span}.${note}`;
}
