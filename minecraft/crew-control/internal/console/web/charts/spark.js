import { h, s, uid } from './dom.js';
import { compact, full, linear } from './scale.js';
import { hideTip, showTip } from './tooltip.js';
import { linePath } from './geom.js';

const HEIGHT = 36;
const PAD = 5;

const ICON = {
  up: 'M1 8 L5 2 L9 8 Z',
  down: 'M1 2 L5 8 L9 2 Z',
  flat: 'M1 4 H9 V6 H1 Z',
};

export const STATUS_LABEL = { good: 'Normal', warning: 'Warning', serious: 'Serious', critical: 'Critical' };
const STATUS_ICON = {
  good: 'M2 6.5 L5 9.5 L10 3',
  warning: 'M6 2 L11 10.5 H1 Z',
  serious: 'M6 2 L11 10.5 H1 Z',
  critical: 'M3 3 L9 9 M9 3 L3 9',
};

// State never rides on colour alone: an icon and a word go with it.
export function statusChip(state, label = STATUS_LABEL[state]) {
  const fill = state === 'good' ? 'none' : state === 'critical' ? 'none' : 'currentColor';
  const icon = s('svg', { class: 'cc-icon', width: 12, height: 12, viewBox: '0 0 12 12', 'aria-hidden': 'true' }, s('path', { d: STATUS_ICON[state] || STATUS_ICON.good, fill, stroke: 'currentColor', 'stroke-width': 1.8, 'stroke-linecap': 'round', 'stroke-linejoin': 'round' }));
  return h('span', { class: `cc-status cc-status-${state}` }, icon, h('span', { text: label }));
}

// A bare trend line: de-emphasis ink with the current period in the accent.
//   new Sparkline(host, { values, format, labels, current, height, pad, domain, table })
// domain pins the vertical range (a unit's 0-20 health should not stretch a
// half-point wobble to full height); table: false skips the screen-reader
// table, for rows that already state the value in text.
export class Sparkline {
  constructor(host, opts = {}) {
    this.opts = { format: full, current: 0.25, height: HEIGHT, pad: PAD, table: true, ...opts };
    this.id = uid();
    this.svg = s('svg', { class: 'cc-svg cc-spark', height: this.opts.height, focusable: 'false', tabindex: '0', role: 'img' });
    this.base = s('path', { class: 'cc-spark-base' });
    this.now = s('path', { class: 'cc-spark-now cc-stroke cc-s1' });
    this.dot = s('circle', { r: 4, class: 'cc-dot cc-fill cc-s1' });
    this.cross = s('line', { class: 'cc-cross', visibility: 'hidden' });
    this.hover = s('circle', { r: 4, class: 'cc-dot cc-fill cc-s1', visibility: 'hidden' });
    this.hit = s('rect', { fill: 'transparent', height: this.opts.height });
    this.svg.append(this.cross, this.base, this.now, this.dot, this.hover, this.hit);
    this.sr = h('table', { class: 'cc-sr' });
    this.w = 0;
    host.append(this.svg, this.sr);
    this.ro = new ResizeObserver(e => {
      const w = Math.floor(e[e.length - 1].contentRect.width);
      if (w && w !== this.w) {
        this.w = w;
        this.render();
      }
    });
    this.ro.observe(this.svg.parentElement || host);
    this.hit.addEventListener('pointermove', ev => this.at(ev.clientX, ev.clientY));
    this.hit.addEventListener('pointerleave', () => this.clear());
    this.svg.addEventListener('focus', () => this.idx(this.values.length - 1));
    this.svg.addEventListener('blur', () => this.clear());
    this.svg.addEventListener('keydown', ev => {
      const d = ev.key === 'ArrowRight' ? 1 : ev.key === 'ArrowLeft' ? -1 : 0;
      if (!d) return;
      ev.preventDefault();
      this.idx(Math.max(0, Math.min(this.values.length - 1, (this.i ?? this.values.length - 1) + d)));
    });
    this.values = [];
    if (opts.values) this.update(opts.values, opts.labels);
  }

  update(values, labels) {
    this.values = values;
    this.labels = labels || values.map((_, i) => `Point ${i + 1}`);
    this.render();
  }

  render() {
    const v = this.values;
    if (!this.w || !v.length) return;
    const w = this.w;
    const { height, pad } = this.opts;
    this.svg.setAttribute('width', w);
    this.svg.setAttribute('viewBox', `0 0 ${w} ${height}`);
    this.hit.setAttribute('width', w);
    let lo = Infinity;
    let hi = -Infinity;
    for (const x of v) {
      if (x == null) continue;
      if (x < lo) lo = x;
      if (x > hi) hi = x;
    }
    if (this.opts.domain) [lo, hi] = this.opts.domain;
    const x = linear(0, Math.max(1, v.length - 1), pad, w - pad);
    const y = linear(lo, hi === lo ? lo + 1 : hi, height - pad, pad);
    this.x = x;
    this.y = y;
    const xs = v.map((_, i) => x(i));
    const ys = v.map(q => (q == null ? null : y(q)));
    const cut = Math.max(1, Math.min(v.length - 1, Math.round(v.length * (1 - this.opts.current))));
    this.base.setAttribute('d', linePath(xs, ys));
    this.now.setAttribute('d', linePath(xs.slice(cut - 1), ys.slice(cut - 1)));
    const last = v.length - 1;
    this.dot.setAttribute('cx', xs[last]);
    this.dot.setAttribute('cy', ys[last] ?? height / 2);
    this.svg.setAttribute('aria-label', `Trend of ${v.length} points, latest ${this.opts.format(v[last])}`);
    if (this.opts.table) this.sr.replaceChildren(h('caption', { text: 'Trend values' }), ...v.map((q, i) => h('tr', null, h('th', { scope: 'row', text: this.labels[i] }), h('td', { text: this.opts.format(q) }))));
  }

  at(cx, cy) {
    const r = this.svg.getBoundingClientRect();
    this.idx(Math.round(this.x.invert(cx - r.left)), [cx, cy]);
  }

  idx(i, client) {
    i = Math.max(0, Math.min(this.values.length - 1, i));
    this.i = i;
    const px = this.x(i);
    this.cross.setAttribute('x1', px);
    this.cross.setAttribute('x2', px);
    this.cross.setAttribute('y1', 0);
    this.cross.setAttribute('y2', this.opts.height);
    this.cross.setAttribute('visibility', 'visible');
    this.hover.setAttribute('cx', px);
    this.hover.setAttribute('cy', this.y(this.values[i]));
    this.hover.setAttribute('visibility', 'visible');
    const r = this.svg.getBoundingClientRect();
    const c = client || [r.left + px, r.top];
    showTip(this.svg, c[0], c[1], { title: this.labels[i], rows: [{ cls: 'cc-s1', shape: 'line', name: this.opts.name || 'Value', value: this.opts.format(this.values[i]) }] });
  }

  clear() {
    this.i = null;
    this.cross.setAttribute('visibility', 'hidden');
    this.hover.setAttribute('visibility', 'hidden');
    hideTip();
  }

  destroy() {
    this.ro.disconnect();
    hideTip();
    this.svg.remove();
    this.sr.remove();
  }
}

// label · value · delta · trend. The value is the headline; a one-bar chart
// would only restate it.
//   new StatTile(host, { label, value, format, delta: { value, label, goodWhenUp, format },
//                        trend: { values, labels }, status })
export class StatTile {
  constructor(host, opts = {}) {
    this.opts = { format: compact, ...opts };
    this.labelEl = h('div', { class: 'cc-tile-label' });
    this.valueEl = h('div', { class: 'cc-tile-value' });
    this.deltaEl = h('div', { class: 'cc-tile-delta' });
    this.statusEl = h('div', { class: 'cc-tile-status' });
    this.sparkHost = h('div', { class: 'cc-tile-spark' });
    this.root = h('div', { class: 'cc-root cc-tile', role: 'group' }, this.labelEl, this.valueEl, this.deltaEl, this.statusEl, this.sparkHost);
    host.append(this.root);
    this.spark = null;
    this.update(opts);
  }

  update(next = {}) {
    const o = (this.opts = { ...this.opts, ...next });
    this.labelEl.textContent = o.label;
    this.valueEl.textContent = typeof o.value === 'number' ? o.format(o.value) : o.value;
    this.root.setAttribute('aria-label', `${o.label}: ${this.valueEl.textContent}`);

    const d = o.delta;
    this.deltaEl.hidden = !d;
    if (d) {
      const dir = d.value > 0 ? 'up' : d.value < 0 ? 'down' : 'flat';
      const good = dir === 'flat' ? null : (dir === 'up') === (d.goodWhenUp !== false);
      const fmt = d.format || (v => compact(Math.abs(v)));
      const icon = s('svg', { class: 'cc-icon', width: 10, height: 10, viewBox: '0 0 10 10', 'aria-hidden': 'true' }, s('path', { d: ICON[dir], fill: 'currentColor' }));
      this.deltaEl.className = `cc-tile-delta ${good == null ? 'cc-flat' : good ? 'cc-good' : 'cc-bad'}`;
      this.deltaEl.replaceChildren(icon, h('span', { class: 'cc-delta-num', text: `${dir === 'up' ? '+' : dir === 'down' ? '−' : ''}${fmt(d.value)}` }), h('span', { class: 'cc-delta-ctx', text: d.label || '' }));
    }
    this.statusEl.hidden = !o.status;
    this.statusEl.replaceChildren(...(o.status ? [statusChip(o.status, o.statusLabel)] : []));

    this.sparkHost.hidden = !o.trend;
    if (o.trend) {
      if (!this.spark) this.spark = new Sparkline(this.sparkHost, { name: o.label, format: o.trendFormat || o.format });
      this.spark.opts.name = o.label;
      this.spark.update(o.trend.values, o.trend.labels);
    }
  }

  destroy() {
    this.spark?.destroy();
    this.root.remove();
  }
}
