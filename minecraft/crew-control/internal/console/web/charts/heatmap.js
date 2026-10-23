import { Panel } from './panel.js';
import { h, s } from './dom.js';
import { compact, full } from './scale.js';
import { textWidth, truncate } from './geom.js';
import { hideTip, showTip } from './tooltip.js';

const LEVELS = 7;
const GAP = 2;
const ROW_H = 24;

// A grid of magnitudes on one-hue steps (or a diverging scale around zero).
//   data: { rows: [name...], cols: [name...], values: [[number|null...]...] }
// Options: title, subtitle, diverging, domain: [min, max], cellLabels,
// valueFormat, rowTitle, colTitle.
export class Heatmap extends Panel {
  constructor(host, opts = {}) {
    super(host, { valueFormat: full, legend: 'never', ...opts }, 'heatmap');
    this.gCells = s('g');
    this.gAxis = s('g');
    this.gLabels = s('g');
    this.svg.append(this.gCells, this.gAxis, this.gLabels);
    this.svg.setAttribute('tabindex', '0');
    this.scale = h('div', { class: 'cc-scale' });
    this.root.insertBefore(this.scale, this.tableEl);
    this.svg.addEventListener('pointermove', ev => this.onMove(ev));
    this.svg.addEventListener('pointerleave', () => this.clearHover());
    this.svg.addEventListener('keydown', ev => this.onKey(ev));
    this.svg.addEventListener('blur', () => this.clearHover());
    if (opts.data) this.update(opts.data);
  }

  update(data) {
    this.data = data;
    this.invalidate();
  }

  domain() {
    if (this.opts.domain) return this.opts.domain;
    let lo = Infinity;
    let hi = -Infinity;
    for (const row of this.data.values) for (const v of row) if (v != null && !Number.isNaN(v)) { if (v < lo) lo = v; if (v > hi) hi = v; }
    if (lo === Infinity) return [0, 1];
    return this.opts.diverging ? [-Math.max(Math.abs(lo), Math.abs(hi)), Math.max(Math.abs(lo), Math.abs(hi))] : [Math.min(0, lo), hi];
  }

  // Seven classes, quantised so the ramp stays legible and themes via CSS.
  level(v, d) {
    const [lo, hi] = d;
    if (hi === lo) return 1;
    if (this.opts.diverging) return Math.max(-3, Math.min(3, Math.round(((v - (lo + hi) / 2) / ((hi - lo) / 2)) * 3)));
    return 1 + Math.min(LEVELS - 1, Math.floor(((v - lo) / (hi - lo)) * LEVELS));
  }

  cls(lv) {
    return this.opts.diverging ? `cc-dv${lv < 0 ? 'n' : 'p'}${Math.abs(lv)}` : `cc-lv${lv}`;
  }

  render() {
    if (!this.data) return;
    const o = this.opts;
    const { rows, cols, values } = this.data;
    const d = this.domain();
    const mL = Math.min(110, Math.max(...rows.map(r => textWidth(truncate(r, 16)))) + 12);
    const mT = 4;
    const plotW = Math.max(10, this.w - mL - 4);
    const cw = plotW / cols.length;
    const every = Math.max(1, Math.ceil((Math.max(...cols.map(c => textWidth(truncate(c, 8)))) + 6) / cw));
    const H = (this.ht = mT + rows.length * ROW_H + 22);
    this.geo = { mL, mT, cw, rows: rows.length, cols: cols.length };
    const cells = [];
    const axis = [];
    const labels = [];
    this.cells = [];
    rows.forEach((r, i) => {
      axis.push(s('text', { x: mL - 8, y: mT + i * ROW_H + ROW_H / 2, dy: '.32em', 'text-anchor': 'end', class: 'cc-tick cc-cat', text: truncate(r, 16) }));
      cols.forEach((c, j) => {
        const v = values[i][j];
        const empty = v == null || Number.isNaN(v);
        const lv = empty ? 0 : this.level(v, d);
        const el = s('rect', { x: mL + j * cw + GAP / 2, y: mT + i * ROW_H + GAP / 2, width: Math.max(1, cw - GAP), height: ROW_H - GAP, rx: 2, class: empty ? 'cc-cell cc-cell-empty' : `cc-cell ${this.cls(lv)}` });
        cells.push(el);
        this.cells[i * cols.length + j] = el;
        if (o.cellLabels && !empty && cw >= 34) labels.push(s('text', { x: mL + j * cw + cw / 2, y: mT + i * ROW_H + ROW_H / 2, dy: '.32em', 'text-anchor': 'middle', class: `cc-cell-label ${this.cls(lv)}`, text: compact(v) }));
      });
    });
    cols.forEach((c, j) => {
      if (j % every === 0) axis.push(s('text', { x: mL + j * cw + cw / 2, y: H - 6, 'text-anchor': 'middle', class: 'cc-tick', text: truncate(c, 8) }));
    });
    this.gCells.replaceChildren(...cells);
    this.gAxis.replaceChildren(...axis);
    this.gLabels.replaceChildren(...labels);
    this.drawScale(d);
    this.setLegend([], 'rect');
    this.setAria(`${rows.length} by ${cols.length} grid from ${o.valueFormat(d[0])} to ${o.valueFormat(d[1])}.`);
    if (this.focus) this.show(this.focus.i, this.focus.j, null);
  }

  // The scale legend: what the ramp means, since colour alone is not a value.
  drawScale(d) {
    const lv = this.opts.diverging ? [-3, -2, -1, 0, 1, 2, 3] : [1, 2, 3, 4, 5, 6, 7];
    const fmt = this.opts.valueFormat;
    this.scale.replaceChildren(
      h('span', { class: 'cc-scale-end', text: fmt(d[0]) }),
      h('span', { class: 'cc-scale-bar', 'aria-hidden': 'true' }, ...lv.map(n => h('i', { class: this.cls(n) === 'cc-dvp0' || this.cls(n) === 'cc-dvn0' ? 'cc-dv0' : this.cls(n) }))),
      h('span', { class: 'cc-scale-end', text: fmt(d[1]) }));
  }

  cellAt(ev) {
    const r = this.svg.getBoundingClientRect();
    const g = this.geo;
    const j = Math.floor((ev.clientX - r.left - g.mL) / g.cw);
    const i = Math.floor((ev.clientY - r.top - g.mT) / ROW_H);
    return i >= 0 && j >= 0 && i < g.rows && j < g.cols ? [i, j] : null;
  }

  onMove(ev) {
    if (!this.geo) return;
    const c = this.cellAt(ev);
    if (!c) return this.clearHover();
    this.show(c[0], c[1], [ev.clientX, ev.clientY]);
  }

  show(i, j, client) {
    const { rows, cols, values } = this.data;
    this.focus = { i, j };
    this.cells.forEach((el, n) => el.classList.toggle('cc-hot', n === i * cols.length + j));
    const v = values[i][j];
    const model = { title: `${rows[i]} · ${cols[j]}`, rows: [{ cls: this.opts.diverging ? 'cc-dvp3' : 'cc-lv6', shape: 'bar', name: this.opts.valueName || 'Value', value: this.opts.valueFormat(v) }] };
    this.tipModel = model;
    if (!client) {
      const r = this.svg.getBoundingClientRect();
      const g = this.geo;
      client = [r.left + g.mL + (j + 0.5) * g.cw, r.top + g.mT + (i + 0.5) * ROW_H];
    }
    showTip(this.svg, client[0], client[1], model);
  }

  clearHover() {
    this.focus = null;
    for (const el of this.cells || []) el.classList.remove('cc-hot');
    hideTip();
  }

  onKey(ev) {
    if (!this.geo) return;
    const di = ev.key === 'ArrowDown' ? 1 : ev.key === 'ArrowUp' ? -1 : 0;
    const dj = ev.key === 'ArrowRight' ? 1 : ev.key === 'ArrowLeft' ? -1 : 0;
    if (ev.key === 'Escape') return this.clearHover();
    if (!di && !dj) return;
    ev.preventDefault();
    const cur = this.focus || { i: 0, j: -1 };
    const i = Math.max(0, Math.min(this.geo.rows - 1, cur.i + di));
    const j = Math.max(0, Math.min(this.geo.cols - 1, cur.j + dj));
    this.show(i, j, null);
    this.say(`${this.tipModel.title}: ${this.tipModel.rows[0].value}`);
  }

  tableModel() {
    if (!this.data) return null;
    const { rows, cols, values } = this.data;
    return {
      caption: `${this.opts.title || 'Heatmap'} values`,
      head: [this.opts.rowTitle || 'Row', ...cols],
      rows: rows.map((r, i) => [r, ...cols.map((_, j) => this.opts.valueFormat(values[i][j]))]),
    };
  }
}
