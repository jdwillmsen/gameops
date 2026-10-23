import { h } from './dom.js';

const MIN = 60000;
export const DEFAULT_PRESETS = [
  { label: '15m', ms: 15 * MIN },
  { label: '1h', ms: 60 * MIN },
  { label: '6h', ms: 360 * MIN },
  { label: '24h', ms: 1440 * MIN },
];

// One range shared by every chart it is handed to, so brushing one panel
// re-scopes the rest.
export function createTimeRange() {
  let range = null;
  let domain = null;
  const subs = new Set();
  const emit = () => subs.forEach(fn => fn(range));
  return {
    get: () => range,
    getDomain: () => domain,
    setDomain(from, to) {
      domain = [from, to];
    },
    set(from, to) {
      if (to < from) [from, to] = [to, from];
      if (range && range[0] === from && range[1] === to) return;
      range = [from, to];
      emit();
    },
    reset() {
      if (!range) return;
      range = null;
      emit();
    },
    subscribe(fn) {
      subs.add(fn);
      return () => subs.delete(fn);
    },
  };
}

// The one filter row above the charts it scopes.
export function mountRangeFilter(host, store, { presets = DEFAULT_PRESETS, label = 'Time range' } = {}) {
  const root = h('div', { class: 'cc-root cc-filter', role: 'group', 'aria-label': label });
  const buttons = presets.map(p => {
    const b = h('button', { type: 'button', class: 'cc-btn', text: p.label });
    b.addEventListener('click', () => {
      const d = store.getDomain();
      if (d) store.set(d[1] - p.ms, d[1]);
    });
    return { b, p };
  });
  const all = h('button', { type: 'button', class: 'cc-btn', text: 'All' });
  all.addEventListener('click', () => store.reset());
  const readout = h('span', { class: 'cc-readout', 'aria-live': 'polite' });
  root.append(...buttons.map(x => x.b), all, readout);
  const sync = () => {
    const r = store.get();
    const d = store.getDomain();
    for (const { b, p } of buttons) b.setAttribute('aria-pressed', String(!!(r && d && r[1] === d[1] && r[1] - r[0] === p.ms)));
    all.setAttribute('aria-pressed', String(!r));
    readout.textContent = r ? `${new Date(r[0]).toISOString().slice(0, 16).replace('T', ' ')} → ${new Date(r[1]).toISOString().slice(11, 16)} UTC` : 'Full range · drag a chart to zoom';
  };
  const off = store.subscribe(sync);
  sync();
  host.append(root);
  return { root, destroy() { off(); root.remove(); } };
}
