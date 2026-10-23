import { Panel } from './panel.js';
import { s } from './dom.js';
import { createTimeRange } from './range.js';
import { foldOther, slotClass } from './series.js';
import { areaPath, labelsFit, linePath, textWidth, truncate } from './geom.js';
import { compact, dateTime, full, linear, lowerBound, nearest, niceTicks, stack, tickLabel, timeTicks } from './scale.js';
import { hideTip, showTip } from './tooltip.js';

const M_TOP = 10;
const M_BOTTOM = 24;
const BRUSH_MIN_PX = 6;

// Time series as lines, areas or a stacked area.
//   data: { x: [ms...], series: [{ key, name, values: [number|null...] }] }
// Options: title, subtitle, height, area, stacked, zero, y: {min, max},
// yFormat, valueFormat, otherCombine ('sum' | 'mean' | 'max' for the folded tail), tz ('UTC' | 'local'), brush, range (shared
// createTimeRange store), onBrush, axes, labels, legend, registry.
export class LineChart extends Panel {
  constructor(host, opts = {}) {
    super(host, { brush: true, axes: true, labels: true, tz: 'local', yFormat: compact, valueFormat: full, ...opts }, 'line');
    const o = this.opts;
    this.range = o.range || createTimeRange();
    this.unsub = this.range.subscribe(() => this.invalidate());
    this.els = new Map();

    this.clip = s('clipPath', { id: `${this.id}-clip` }, s('rect'));
    this.defs.append(this.clip);
    this.gGrid = s('g', { class: 'cc-grid' });
    this.gAxis = s('g', { class: 'cc-axis' });
    this.gSeries = s('g', { 'clip-path': `url(#${this.id}-clip)` });
    this.gDots = s('g');
    this.gLabels = s('g', { class: 'cc-dlabels' });
    this.cross = s('line', { class: 'cc-cross', visibility: 'hidden' });
    this.hoverDots = s('g');
    this.brushRect = s('rect', { class: 'cc-brush', visibility: 'hidden' });
    this.hit = s('rect', { class: 'cc-hit', fill: 'transparent' });
    this.svg.append(this.gGrid, this.gAxis, this.gSeries, this.gDots, this.gLabels, this.cross, this.hoverDots, this.brushRect, this.hit);
    this.svg.setAttribute('tabindex', '0');

    this.hit.addEventListener('pointermove', ev => this.onMove(ev));
    this.hit.addEventListener('pointerleave', () => !this.drag && this.clearHover());
    // A press on the plot is a brush, never a text selection or a focus ring.
    this.hit.addEventListener('mousedown', ev => ev.preventDefault());
    this.hit.addEventListener('pointerdown', ev => this.onDown(ev));
    this.hit.addEventListener('pointerup', ev => this.onUp(ev));
    this.hit.addEventListener('pointercancel', () => this.endDrag());
    this.hit.addEventListener('dblclick', () => this.range.reset());
    this.svg.addEventListener('keydown', ev => this.onKey(ev));
    this.svg.addEventListener('blur', () => this.clearHover());
    this.svg.addEventListener('focus', () => {
      if (this.hoverPx == null && this.plot) this.moveTo(this.plot.i1);
    });
    if (o.data) this.update(o.data);
  }

  update(data) {
    this.x = data.x;
    this.series = foldOther(data.series, this.registry, { combine: this.opts.otherCombine });
    this.invalidate();
  }

  visibleSeries() {
    return this.series.filter(sr => !this.hidden.has(sr.key));
  }

  render() {
    const o = this.opts;
    const { w } = this;
    const H = (this.ht = o.height);
    if (!this.series) return;
    const all = this.series;
    const vis = this.visibleSeries();
    const x = this.x;
    const n = x.length;
    const stacked = !!o.stacked;
    const zero = o.zero ?? (o.area || stacked);

    // Visible window, with one point of slack so lines run to the plot edge.
    const r = this.range.get();
    const wx0 = r ? r[0] : x[0];
    const wx1 = r ? r[1] : x[n - 1];
    const i0 = Math.max(0, lowerBound(x, wx0) - 1);
    const i1 = Math.min(n - 1, lowerBound(x, wx1));

    const stacks = stacked ? stack(vis.map(sr => sr.values)) : null;
    let lo = Infinity;
    let hi = -Infinity;
    vis.forEach((sr, k) => {
      for (let i = i0; i <= i1; i++) {
        const v = stacked ? stacks[k].hi[i] : sr.values[i];
        if (v == null || Number.isNaN(v)) continue;
        if (v < lo) lo = v;
        if (v > hi) hi = v;
      }
    });
    if (lo === Infinity) {
      lo = 0;
      hi = 1;
    }
    if (zero) lo = Math.min(0, lo);
    if (o.y?.min != null) lo = o.y.min;
    if (o.y?.max != null) hi = o.y.max;
    const axes = o.axes;
    const yt = niceTicks(lo, hi, Math.max(3, Math.min(6, Math.floor((H - M_TOP - M_BOTTOM) / 34))));
    const yTicks = o.y?.max != null || o.y?.min != null ? { ...yt, lo: o.y?.min ?? yt.lo, hi: o.y?.max ?? yt.hi } : yt;

    const mT = axes ? M_TOP : 6;
    const mB = axes ? M_BOTTOM : 6;
    const plotH = H - mT - mB;
    const yS = linear(yTicks.lo, yTicks.hi, mT + plotH, mT);

    // End labels only when the series separate; otherwise the legend and
    // tooltip carry identity.
    const ends = vis.map((sr, k) => {
      for (let i = i1; i >= i0; i--) {
        const v = stacked ? stacks[k].hi[i] : sr.values[i];
        if (v != null && !Number.isNaN(v)) return { sr, y: yS(v) };
      }
      return null;
    }).filter(Boolean);
    const labelled = o.labels && vis.length >= 1 && vis.length <= 4 && axes && labelsFit(ends.map(e => e.y), 14);
    const names = ends.map(e => truncate(e.sr.name, 14));
    const mR = labelled ? Math.min(100, Math.max(...names.map(t => textWidth(t, 6.4))) + 16) : 14;
    const mL = axes ? Math.max(...yTicks.ticks.map(t => o.yFormat(t).length)) * 6.4 + 14 : 6;
    const plotW = Math.max(10, w - mL - mR);
    const xS = linear(wx0, wx1 === wx0 ? wx0 + 1 : wx1, mL, mL + plotW);
    this.plot = { l: mL, t: mT, w: plotW, h: plotH, xS, yS, i0, i1, vis, stacks };

    this.clip.firstChild.setAttribute('x', mL);
    this.clip.firstChild.setAttribute('y', mT - 4);
    this.clip.firstChild.setAttribute('width', plotW);
    this.clip.firstChild.setAttribute('height', plotH + 8);
    this.hit.setAttribute('x', mL);
    this.hit.setAttribute('y', mT);
    this.hit.setAttribute('width', plotW);
    this.hit.setAttribute('height', plotH);

    // Grid and axes.
    const grid = [];
    const axis = [];
    if (axes) {
      for (const t of yTicks.ticks) {
        const y = yS(t);
        grid.push(s('line', { x1: mL, x2: mL + plotW, y1: y, y2: y, class: 'cc-gridline' }));
        axis.push(s('text', { x: mL - 8, y, dy: '.32em', 'text-anchor': 'end', class: 'cc-tick', text: o.yFormat(t) }));
      }
      const tt = timeTicks(wx0, wx1, Math.max(2, Math.floor(plotW / 84)), o.tz);
      axis.push(s('line', { x1: mL, x2: mL + plotW, y1: mT + plotH, y2: mT + plotH, class: 'cc-baseline' }));
      for (const t of tt.ticks) axis.push(s('text', { x: xS(t), y: H - 6, 'text-anchor': 'middle', class: 'cc-tick', text: tickLabel(t, tt.step, o.tz) }));
    }
    this.gGrid.replaceChildren(...grid);
    this.gAxis.replaceChildren(...axis);

    // Series marks.
    const px = new Array(i1 - i0 + 1);
    for (let i = i0; i <= i1; i++) px[i - i0] = xS(x[i]);
    const areas = [];
    const lines = [];
    const dots = [];
    const seen = new Set();
    vis.forEach((sr, k) => {
      seen.add(sr.key);
      let e = this.els.get(sr.key);
      if (!e) {
        e = { line: s('path', { class: 'cc-line' }), area: s('path'), dot: s('circle', { r: 4, class: 'cc-dot' }) };
        this.els.set(sr.key, e);
      }
      const cls = slotClass(sr.slot, this.registry.max);
      e.line.setAttribute('class', `cc-line cc-stroke ${cls}`);
      e.dot.setAttribute('class', `cc-dot cc-fill ${cls}`);
      const vals = sr.values.slice(i0, i1 + 1);
      if (stacked) {
        const hiY = stacks[k].hi.slice(i0, i1 + 1).map(yS);
        const loY = stacks[k].lo.slice(i0, i1 + 1).map(yS);
        e.area.setAttribute('class', `cc-area cc-area-stacked cc-fill ${cls}`);
        e.area.style.setProperty('--cc-tx', this.texture(sr.slot));
        e.area.setAttribute('d', areaPath(px, hiY, loY));
        e.line.setAttribute('d', linePath(px, hiY));
        areas.push(e.area);
      } else {
        const ys = vals.map(v => (v == null ? null : yS(v)));
        e.line.setAttribute('d', linePath(px, ys));
        if (o.area) {
          e.area.setAttribute('class', `cc-area cc-fill ${cls}`);
          e.area.setAttribute('d', areaPath(px, ys, ys.map(() => yS(Math.max(yTicks.lo, 0)))));
          areas.push(e.area);
        }
      }
      lines.push(e.line);
      const end = ends.find(q => q.sr === sr);
      if (end) {
        const j = this.lastIndex(sr, k, i0, i1);
        e.dot.setAttribute('cx', xS(x[j]));
        e.dot.setAttribute('cy', end.y);
        dots.push(e.dot);
      }
    });
    for (const k of this.els.keys()) if (!seen.has(k)) this.els.delete(k);
    this.gSeries.replaceChildren(...areas.reverse(), ...lines);
    this.gDots.replaceChildren(...dots);

    this.gLabels.replaceChildren(...(labelled ? ends.map((e, i) => s('text', { x: mL + plotW + 10, y: e.y, dy: '.32em', class: 'cc-dlabel', text: names[i] })) : []));

    this.setLegend(all.map(sr => ({ key: sr.key, name: sr.name, slot: sr.slot })), o.area || stacked ? 'rect' : 'line');
    this.setAria(`${vis.length} of ${all.length} series, ${n} points${r ? ', zoomed' : ''}.`);
    this.svg.classList.toggle('cc-empty', !vis.length);
    if (this.hoverPx != null) this.showAtPx(this.hoverPx, this.hoverClient);
  }

  lastIndex(sr, k, i0, i1) {
    for (let i = i1; i >= i0; i--) {
      const v = this.opts.stacked ? this.plot.stacks[k].hi[i] : sr.values[i];
      if (v != null && !Number.isNaN(v)) return i;
    }
    return i1;
  }

  localX(ev) {
    return ev.clientX - this.hit.getBoundingClientRect().left + this.plot.l;
  }

  onMove(ev) {
    if (!this.plot) return;
    if (this.drag) {
      this.dragTo(this.localX(ev));
      return;
    }
    this.hoverClient = [ev.clientX, ev.clientY];
    this.showAtPx(this.localX(ev), this.hoverClient);
  }

  indexAt(px) {
    const p = this.plot;
    return nearest(this.x, p.xS.invert(px), p.i0, p.i1);
  }

  showAtPx(px, client) {
    const p = this.plot;
    const i = this.indexAt(px);
    if (i < 0) return;
    this.hoverPx = px;
    const cx = p.xS(this.x[i]);
    this.cross.setAttribute('x1', cx);
    this.cross.setAttribute('x2', cx);
    this.cross.setAttribute('y1', p.t);
    this.cross.setAttribute('y2', p.t + p.h);
    this.cross.setAttribute('visibility', 'visible');
    const markers = [];
    const rows = [];
    p.vis.forEach((sr, k) => {
      const v = sr.values[i];
      const cls = slotClass(sr.slot, this.registry.max);
      if (v != null && !Number.isNaN(v)) {
        const y = this.opts.stacked ? p.stacks[k].hi[i] : v;
        markers.push(s('circle', { cx, cy: p.yS(y), r: 4, class: `cc-dot cc-fill ${cls}` }));
      }
      rows.push({ cls, shape: 'line', name: sr.name, value: this.opts.valueFormat(v) });
    });
    this.hoverDots.replaceChildren(...markers);
    const total = this.opts.stacked ? `Total ${this.opts.valueFormat(p.vis.reduce((a, sr) => a + (sr.values[i] || 0), 0))}` : null;
    this.tipModel = { title: dateTime(this.x[i], this.opts.tz), rows, foot: total };
    const c = client || this.keyClient(cx);
    showTip(this.svg, c[0], c[1], this.tipModel);
    this.hoverI = i;
  }

  keyClient(cx) {
    const b = this.hit.getBoundingClientRect();
    return [b.left + cx - this.plot.l, b.top + 8];
  }

  clearHover() {
    this.hoverPx = null;
    this.hoverI = null;
    this.hoverClient = null;
    this.cross.setAttribute('visibility', 'hidden');
    this.hoverDots.replaceChildren();
    hideTip();
  }

  moveTo(i) {
    const p = this.plot;
    i = Math.max(p.i0, Math.min(p.i1, i));
    this.hoverClient = null;
    this.showAtPx(p.xS(this.x[i]), null);
    const m = this.tipModel;
    this.say(`${m.title}. ${m.rows.map(r => `${r.name} ${r.value}`).join(', ')}`);
  }

  // Brushing: drag on the plot, or Shift+arrows then Enter from the keyboard.
  onDown(ev) {
    if (!this.opts.brush || ev.button !== 0 || !this.plot) return;
    this.drag = { x0: this.localX(ev), x1: this.localX(ev) };
    this.hit.setPointerCapture(ev.pointerId);
    hideTip();
  }

  dragTo(px) {
    const p = this.plot;
    this.drag.x1 = Math.max(p.l, Math.min(p.l + p.w, px));
    this.drawBrush(this.drag.x0, this.drag.x1);
  }

  drawBrush(a, b) {
    const p = this.plot;
    const x = Math.min(a, b);
    this.brushRect.setAttribute('x', x);
    this.brushRect.setAttribute('width', Math.abs(b - a));
    this.brushRect.setAttribute('y', p.t);
    this.brushRect.setAttribute('height', p.h);
    this.brushRect.setAttribute('visibility', 'visible');
  }

  onUp(ev) {
    if (!this.drag) return;
    const { x0, x1 } = this.drag;
    this.hit.releasePointerCapture?.(ev.pointerId);
    this.endDrag();
    if (Math.abs(x1 - x0) >= BRUSH_MIN_PX) this.commitBrush(x0, x1);
  }

  endDrag() {
    this.drag = null;
    this.brushRect.setAttribute('visibility', 'hidden');
  }

  commitBrush(a, b) {
    const p = this.plot;
    const t0 = Math.round(p.xS.invert(Math.min(a, b)));
    const t1 = Math.round(p.xS.invert(Math.max(a, b)));
    this.range.set(t0, t1);
    this.opts.onBrush?.({ from: t0, to: t1 });
    this.say(`Zoomed to ${dateTime(t0, this.opts.tz)} – ${dateTime(t1, this.opts.tz)}`);
  }

  onKey(ev) {
    if (!this.plot) return;
    const step = ev.key === 'ArrowRight' ? 1 : ev.key === 'ArrowLeft' ? -1 : 0;
    const p = this.plot;
    if (step) {
      ev.preventDefault();
      const cur = this.hoverI ?? p.i1;
      const next = Math.max(p.i0, Math.min(p.i1, cur + step));
      if (ev.shiftKey && this.opts.brush) {
        this.anchor ??= cur;
        this.drawBrush(p.xS(this.x[this.anchor]), p.xS(this.x[next]));
      }
      this.moveTo(next);
    } else if (ev.key === 'Home' || ev.key === 'End') {
      ev.preventDefault();
      this.moveTo(ev.key === 'Home' ? p.i0 : p.i1);
    } else if (ev.key === 'Enter' && this.anchor != null && this.hoverI != null && this.anchor !== this.hoverI) {
      ev.preventDefault();
      this.commitBrush(p.xS(this.x[this.anchor]), p.xS(this.x[this.hoverI]));
      this.anchor = null;
      this.endDrag();
    } else if (ev.key === 'Escape') {
      this.anchor = null;
      this.endDrag();
      if (this.range.get()) this.range.reset();
      else this.clearHover();
    }
  }

  tableModel() {
    if (!this.series) return null;
    const tz = this.opts.tz;
    const vis = this.series;
    return {
      caption: `${this.opts.title || 'Chart'} values`,
      head: ['Time', ...vis.map(sr => sr.name)],
      rows: this.x.map((t, i) => [dateTime(t, tz), ...vis.map(sr => this.opts.valueFormat(sr.values[i]))]),
    };
  }

  destroy() {
    this.unsub();
    super.destroy();
  }
}
