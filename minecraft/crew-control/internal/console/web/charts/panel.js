import { h, s, uid } from './dom.js';
import { defaultRegistry, slotClass } from './series.js';
import { MAX_SERIES } from './palette.js';
import { hideTip } from './tooltip.js';

const TEXTURE_SLOTS = MAX_SERIES + 1;

// One queue draws every panel. Twenty charts refreshing in the same tick would
// otherwise land in one frame; the budget spreads them over as many frames as a
// slow machine needs, so no single frame blows past 16 ms.
const FRAME_BUDGET_MS = 6;
const queue = new Set();
let pumping = 0;
let observer = null;
let eager = false;

// Fixtures and full-page screenshots need every panel drawn, scrolled into
// view or not.
export function renderOffscreen(on) {
  eager = on;
}

function pump() {
  pumping = 0;
  const t0 = performance.now();
  let drawn = 0;
  for (const panel of queue) {
    queue.delete(panel);
    panel.draw();
    drawn++;
    if (performance.now() - t0 > FRAME_BUDGET_MS) break;
  }
  observer?.({ drawn, ms: performance.now() - t0, pending: queue.size });
  if (queue.size) pumping = requestAnimationFrame(pump);
}

// fn({ drawn, ms, pending }) after every drawing frame; for measuring.
export function observeFrames(fn) {
  observer = fn;
}

// The shell every chart shares: title, plot host, legend, table view, live
// region, sizing and visibility. Subclasses draw into this.svg from render()
// and describe their data for the table view in tableModel().
export class Panel {
  constructor(host, opts, kind) {
    this.host = host;
    this.kind = kind;
    this.opts = { height: 200, legend: 'auto', ...opts };
    this.id = uid();
    this.registry = this.opts.registry || defaultRegistry;
    this.hidden = new Set();
    this.w = 0;
    this.ht = this.opts.height;
    this.visible = true;
    this.dirty = false;
    this.legendSig = '';

    this.svg = s('svg', { class: 'cc-svg', focusable: 'false' });
    this.defs = s('defs');
    this.svg.append(this.defs);
    this.plotHost = h('div', { class: 'cc-plot' }, this.svg);
    this.legendEl = h('div', { class: 'cc-legend', role: 'group', 'aria-label': 'Legend' });
    this.tableBtn = h('button', { type: 'button', class: 'cc-btn cc-tbtn', 'aria-expanded': 'false', 'aria-controls': `${this.id}-table`, text: 'Table' });
    this.tableEl = h('div', { class: 'cc-table', id: `${this.id}-table`, hidden: true, tabindex: '-1' });
    this.live = h('p', { class: 'cc-sr', 'aria-live': 'polite' });
    const head = h('header', { class: 'cc-head' },
      h('div', { class: 'cc-titles' },
        h('h3', { class: 'cc-title', id: `${this.id}-t`, text: this.opts.title || '' }),
        this.opts.subtitle ? h('p', { class: 'cc-sub', text: this.opts.subtitle }) : null),
      this.tableBtn);
    this.root = h('figure', { class: `cc-root cc-panel cc-${kind}`, 'aria-labelledby': `${this.id}-t` }, head, this.plotHost, this.legendEl, this.tableEl, this.live);
    host.append(this.root);

    this.tableBtn.addEventListener('click', () => this.toggleTable());
    this.ro = new ResizeObserver(entries => {
      const w = Math.floor(entries[entries.length - 1].contentRect.width);
      if (w && w !== this.w) {
        this.w = w;
        this.invalidate();
      }
    });
    this.ro.observe(this.plotHost);
    this.io = new IntersectionObserver(entries => {
      this.visible = eager || entries[entries.length - 1].isIntersecting;
      if (this.visible && this.dirty) this.invalidate();
    }, { rootMargin: '120px' });
    this.io.observe(this.root);
  }

  // Redraws are coalesced per panel and skipped while off screen; a panel that
  // becomes visible again draws what it missed.
  invalidate() {
    this.dirty = true;
    if (!this.visible || !this.w) return;
    queue.add(this);
    if (!pumping) pumping = requestAnimationFrame(pump);
  }

  draw() {
    if (!this.dirty || !this.visible || !this.w) return;
    this.dirty = false;
    this.render();
    this.svg.setAttribute('width', this.w);
    this.svg.setAttribute('height', this.ht);
    this.svg.setAttribute('viewBox', `0 0 ${this.w} ${this.ht}`);
    if (this.tableOpen) this.renderTable();
  }

  setLoading(on) {
    this.root.classList.toggle('cc-loading', !!on);
  }

  say(text) {
    this.live.textContent = text;
  }

  setAria(label) {
    this.svg.setAttribute('role', 'img');
    this.svg.setAttribute('aria-label', `${this.opts.title || this.kind}. ${label} Use the table view for every value.`);
  }

  // entries: [{ key, name, slot }]. Rebuilt only when the key set changes, so a
  // 1 Hz refresh does not steal focus from a legend button.
  setLegend(entries, shape) {
    const need = this.opts.legend === 'always' || (this.opts.legend === 'auto' && entries.length >= 2);
    this.legendEl.hidden = !need;
    const sig = entries.map(e => `${e.key}:${e.name}`).join('|') + shape;
    if (sig !== this.legendSig) {
      this.legendSig = sig;
      this.legendEl.replaceChildren(...entries.map(e => {
        const sw = h('span', { class: `cc-sw cc-sw-${shape} ${slotClass(e.slot, this.registry.max)}`, 'aria-hidden': 'true' });
        const b = h('button', { type: 'button', class: 'cc-legend-item', 'aria-pressed': 'true' }, sw, h('span', { text: e.name }));
        b.dataset.key = e.key;
        b.addEventListener('click', () => this.toggleSeries(e.key));
        return b;
      }));
    }
    for (const b of this.legendEl.children) b.setAttribute('aria-pressed', String(!this.hidden.has(b.dataset.key)));
  }

  toggleSeries(key) {
    if (this.hidden.has(key)) this.hidden.delete(key);
    else this.hidden.add(key);
    this.invalidate();
  }

  // Patterns are created per slot on demand; marks pick them up through the
  // --cc-tx custom property when texture is switched on.
  texture(slot) {
    const cls = slotClass(slot, this.registry.max);
    const id = `${this.id}-tx${Math.min(slot, TEXTURE_SLOTS - 1)}`;
    if (!this.defs.querySelector(`#${id}`)) {
      const angle = slot % 2 ? 135 : 45;
      this.defs.append(s('pattern', { id, width: 6, height: 6, patternUnits: 'userSpaceOnUse', patternTransform: `rotate(${angle})` },
        s('rect', { width: 6, height: 6, class: `cc-fill ${cls}` }),
        s('line', { x1: 0, y1: 0, x2: 0, y2: 6, class: `cc-tx-ink ${cls}` })));
    }
    return `url(#${id})`;
  }

  toggleTable(force) {
    this.tableOpen = force ?? !this.tableOpen;
    this.tableBtn.setAttribute('aria-expanded', String(this.tableOpen));
    this.tableBtn.setAttribute('aria-pressed', String(this.tableOpen));
    this.tableEl.hidden = !this.tableOpen;
    if (this.tableOpen) this.renderTable();
  }

  // The accessibility twin of every chart: the same numbers as a real table.
  renderTable() {
    const m = this.tableModel();
    if (!m) return;
    const head = h('tr', null, ...m.head.map((c, i) => h('th', { scope: 'col', text: c, class: i ? 'cc-num' : '' })));
    const rows = m.rows.map(r => h('tr', null, ...r.map((c, i) => (i ? h('td', { class: 'cc-num', text: c }) : h('th', { scope: 'row', text: c })))));
    this.tableEl.replaceChildren(h('table', null, h('caption', { class: 'cc-sr', text: m.caption }), h('thead', null, head), h('tbody', null, ...rows)));
  }

  destroy() {
    queue.delete(this);
    this.ro.disconnect();
    this.io.disconnect();
    hideTip();
    this.root.remove();
  }
}
