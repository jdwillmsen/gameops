import { Panel } from './panel.js';
import { s } from './dom.js';
import { foldOther, slotClass } from './series.js';
import { barPath, textWidth, truncate } from './geom.js';
import { compact, full, linear, niceTicks } from './scale.js';
import { hideTip, showTip } from './tooltip.js';

const THICK = 24;
const GAP = 2;
const RADIUS = 4;
const M_TOP = 18;
const M_BOTTOM = 26;

// Bars and stacked bars, vertical or horizontal.
//   data: { categories: [name...], series: [{ key, name, values: [number...] }] }
// Options: title, subtitle, height (a number, or 'auto' when horizontal), horizontal, stacked, valueFormat, yFormat,
// labels (value at the tip of single-series bars), legend, registry.
// Stacked values must be non-negative.
export class BarChart extends Panel {
  constructor(host, opts = {}) {
    super(host, { labels: true, valueFormat: full, yFormat: compact, ...opts }, 'bar');
    this.gGrid = s('g');
    this.gMarks = s('g');
    this.gAxis = s('g');
    this.gLabels = s('g');
    this.hit = s('rect', { class: 'cc-hit', fill: 'transparent' });
    this.svg.append(this.gGrid, this.gMarks, this.gAxis, this.gLabels, this.hit);
    this.svg.setAttribute('tabindex', '0');
    this.hit.addEventListener('pointermove', ev => this.onMove(ev));
    this.hit.addEventListener('pointerleave', () => this.clearHover());
    this.svg.addEventListener('keydown', ev => this.onKey(ev));
    this.svg.addEventListener('blur', () => this.clearHover());
    if (opts.data) this.update(opts.data);
  }

  update(data) {
    this.cats = data.categories;
    this.series = foldOther(data.series, this.registry, { combine: this.opts.otherCombine });
    this.invalidate();
  }

  render() {
    const o = this.opts;
    if (!this.series) return;
    const { w } = this;
    const cats = this.cats;
    const vis = this.series.filter(sr => !this.hidden.has(sr.key));
    const horiz = !!o.horizontal;
    const stacked = !!o.stacked && vis.length > 1;
    const nPer = stacked ? 1 : Math.max(1, vis.length);
    const thick = Math.min(THICK, horiz ? 22 : 24);

    const H = (this.ht = horiz && o.height === 'auto' ? 8 + cats.length * (nPer * (thick + GAP) + 10) + M_BOTTOM : o.height);

    let lo = 0;
    let hi = 0;
    for (let c = 0; c < cats.length; c++) {
      let up = 0;
      for (const sr of vis) {
        const v = sr.values[c] || 0;
        if (stacked) up += v;
        else {
          if (v < lo) lo = v;
          if (v > hi) hi = v;
        }
      }
      if (stacked && up > hi) hi = up;
    }
    const yt = niceTicks(lo, hi || 1, horiz ? 4 : Math.max(3, Math.min(6, Math.floor((H - M_TOP - M_BOTTOM) / 34))));
    const labelW = horiz ? Math.min(120, Math.max(...cats.map(c => textWidth(truncate(c, 18), 6.2))) + 14) : 0;
    const mL = horiz ? labelW : Math.max(...yt.ticks.map(t => o.yFormat(t).length)) * 6.4 + 14;
    const mR = horiz ? 28 : 8;
    const mT = horiz ? 6 : M_TOP;
    const plotW = Math.max(10, w - mL - mR);
    const plotH = H - mT - M_BOTTOM;
    const vS = horiz ? linear(yt.lo, yt.hi, mL, mL + plotW) : linear(yt.lo, yt.hi, mT + plotH, mT);
    const band = horiz ? plotH / cats.length : plotW / cats.length;
    const base = vS(0);
    this.geo = { mL, mT, plotW, plotH, band, horiz, stacked, vis, vS, base };

    const grid = [];
    const axis = [];
    for (const t of yt.ticks) {
      const p = vS(t);
      if (horiz) {
        grid.push(s('line', { x1: p, x2: p, y1: mT, y2: mT + plotH, class: 'cc-gridline' }));
        axis.push(s('text', { x: p, y: H - 8, 'text-anchor': 'middle', class: 'cc-tick', text: o.yFormat(t) }));
      } else {
        grid.push(s('line', { x1: mL, x2: mL + plotW, y1: p, y2: p, class: 'cc-gridline' }));
        axis.push(s('text', { x: mL - 8, y: p, dy: '.32em', 'text-anchor': 'end', class: 'cc-tick', text: o.yFormat(t) }));
      }
    }
    axis.push(horiz
      ? s('line', { x1: base, x2: base, y1: mT, y2: mT + plotH, class: 'cc-baseline' })
      : s('line', { x1: mL, x2: mL + plotW, y1: base, y2: base, class: 'cc-baseline' }));

    // Category labels, thinned when they would collide.
    const every = horiz ? 1 : Math.max(1, Math.ceil((Math.max(...cats.map(c => textWidth(truncate(c, 12), 6.2))) + 8) / band));
    cats.forEach((c, i) => {
      if (i % every) return;
      const mid = (horiz ? mT : mL) + band * (i + 0.5);
      axis.push(horiz
        ? s('text', { x: mL - 8, y: mid, dy: '.32em', 'text-anchor': 'end', class: 'cc-tick cc-cat', text: truncate(c, 18) })
        : s('text', { x: mid, y: H - 8, 'text-anchor': 'middle', class: 'cc-tick cc-cat', text: truncate(c, 12) }));
    });
    this.gGrid.replaceChildren(...grid);
    this.gAxis.replaceChildren(...axis);

    // Marks: each is a rect clipped to nothing, drawn from the baseline.
    this.marks = [];
    const marks = [];
    const labels = [];
    const single = vis.length === 1 && o.labels;
    const groupSpan = nPer * thick + (nPer - 1) * GAP;
    cats.forEach((_, c) => {
      let acc = 0;
      let topSeg = -1;
      if (stacked) vis.forEach((sr, k) => (sr.values[c] > 0 ? (topSeg = k) : 0));
      vis.forEach((sr, k) => {
        const v = sr.values[c];
        if (v == null || Number.isNaN(v)) return;
        const cls = slotClass(sr.slot, this.registry.max);
        const along = (horiz ? mT : mL) + band * c + (band - (stacked ? thick : groupSpan)) / 2 + (stacked ? 0 : k * (thick + GAP));
        let a;
        let b;
        let end = null;
        if (stacked) {
          a = vS(acc);
          b = vS(acc + v);
          acc += v;
          if (k === topSeg) end = horiz ? 'right' : 'top';
          // The surface gap sits at the segment's start so the stack stays flush with the baseline.
          if (k > 0) a += horiz ? GAP : -GAP;
        } else {
          a = base;
          b = vS(v);
          end = horiz ? (v >= 0 ? 'right' : 'left') : v >= 0 ? 'top' : 'bottom';
        }
        const len = Math.max(1, Math.abs(b - a));
        const lead = Math.min(a, b);
        const d = horiz ? barPath(lead, along, len, thick, RADIUS, end) : barPath(along, lead, thick, len, RADIUS, end);
        const el = s('path', { d, class: `cc-mark cc-fill ${cls}` });
        el.style.setProperty('--cc-tx', this.texture(sr.slot));
        marks.push(el);
        this.marks.push({ c, k, el, mid: along + thick / 2, lo: lead, hi: lead + len });
        if (single && !stacked) {
          const text = o.valueFormat(v);
          const tw = textWidth(text, 6.4);
          if (horiz) {
            const tip = v >= 0 ? lead + len : lead;
            if (v >= 0 ? tip + 6 + tw < w - 2 : tip - 6 - tw > 2) labels.push(s('text', { x: v >= 0 ? tip + 6 : tip - 6, y: along + thick / 2, dy: '.32em', 'text-anchor': v >= 0 ? 'start' : 'end', class: 'cc-vlabel', text }));
          } else if (tw + 4 <= band) {
            labels.push(s('text', { x: along + thick / 2, y: v >= 0 ? lead - 6 : lead + len + 13, 'text-anchor': 'middle', class: 'cc-vlabel', text }));
          }
        }
      });
    });
    this.gMarks.replaceChildren(...marks);
    this.gLabels.replaceChildren(...labels);

    if (horiz) {
      this.hit.setAttribute('x', mL);
      this.hit.setAttribute('y', mT);
      this.hit.setAttribute('width', plotW);
      this.hit.setAttribute('height', plotH);
    } else {
      this.hit.setAttribute('x', mL);
      this.hit.setAttribute('y', mT - 12);
      this.hit.setAttribute('width', plotW);
      this.hit.setAttribute('height', plotH + 12);
    }
    // A single series is named by the title; a legend box would only repeat it.
    this.setLegend(this.series.map(sr => ({ key: sr.key, name: sr.name, slot: sr.slot })), 'rect');
    this.setAria(`${cats.length} categories, ${vis.length} series.`);
    if (this.focus) this.showFocus(this.focus.c, this.focus.k, this.focusClient);
  }

  // Resolve a pointer to a category band and, within it, the nearest mark.
  // Bands are hit as a whole, so a 2px sliver or a 24px bar is never a pinpoint.
  hitAt(ev) {
    const g = this.geo;
    const r = this.svg.getBoundingClientRect();
    const px = ev.clientX - r.left;
    const py = ev.clientY - r.top;
    const along = g.horiz ? py : px;
    const across = g.horiz ? px : py;
    const c = Math.max(0, Math.min(this.cats.length - 1, Math.floor((along - (g.horiz ? g.mT : g.mL)) / g.band)));
    let best = null;
    let bestD = Infinity;
    for (const m of this.marks) {
      if (m.c !== c) continue;
      const d = g.stacked ? Math.max(m.lo - across, across - m.hi, 0) : Math.abs(along - m.mid);
      if (d < bestD) {
        bestD = d;
        best = m;
      }
    }
    return { c, k: best ? best.k : -1 };
  }

  onMove(ev) {
    if (!this.geo) return;
    const { c, k } = this.hitAt(ev);
    this.focusClient = [ev.clientX, ev.clientY];
    this.showFocus(c, k, this.focusClient);
  }

  showFocus(c, k, client) {
    const g = this.geo;
    if (c >= this.cats.length) return;
    this.focus = { c, k };
    for (const m of this.marks) m.el.classList.toggle('cc-hot', m.c === c && m.k === k);
    const rows = g.vis.map((sr, i) => ({ cls: slotClass(sr.slot, this.registry.max), shape: 'bar', name: sr.name, value: this.opts.valueFormat(sr.values[c]), hot: i === k && g.vis.length > 1 }));
    const total = g.stacked ? `Total ${this.opts.valueFormat(g.vis.reduce((a, sr) => a + (sr.values[c] || 0), 0))}` : null;
    this.tipModel = { title: this.cats[c], rows, foot: total };
    if (!client) {
      const b = this.hit.getBoundingClientRect();
      const mid = g.band * (c + 0.5);
      client = g.horiz ? [b.left + b.width / 2, b.top + mid] : [b.left + mid, b.top + 12];
    }
    showTip(this.svg, client[0], client[1], this.tipModel);
  }

  clearHover() {
    this.focus = null;
    for (const m of this.marks || []) m.el.classList.remove('cc-hot');
    hideTip();
  }

  onKey(ev) {
    if (!this.geo) return;
    const g = this.geo;
    const fwd = g.horiz ? 'ArrowDown' : 'ArrowRight';
    const back = g.horiz ? 'ArrowUp' : 'ArrowLeft';
    if (ev.key !== fwd && ev.key !== back && ev.key !== 'Home' && ev.key !== 'End') {
      if (ev.key === 'Escape') this.clearHover();
      return;
    }
    ev.preventDefault();
    const n = this.cats.length;
    const cur = this.focus ? this.focus.c : -1;
    const c = ev.key === 'Home' ? 0 : ev.key === 'End' ? n - 1 : Math.max(0, Math.min(n - 1, cur + (ev.key === fwd ? 1 : -1)));
    this.focusClient = null;
    this.showFocus(c, -1, null);
    this.say(`${this.tipModel.title}. ${this.tipModel.rows.map(r => `${r.name} ${r.value}`).join(', ')}`);
  }

  tableModel() {
    if (!this.series) return null;
    return {
      caption: `${this.opts.title || 'Chart'} values`,
      head: ['Category', ...this.series.map(sr => sr.name)],
      rows: this.cats.map((c, i) => [c, ...this.series.map(sr => this.opts.valueFormat(sr.values[i]))]),
    };
  }
}
