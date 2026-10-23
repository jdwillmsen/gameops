import { Panel } from './panel.js';
import { h, s } from './dom.js';
import { arcPath } from './geom.js';
import { full, linear } from './scale.js';
import { statusChip } from './spark.js';
import { hideTip, showTip } from './tooltip.js';

const START = 135;
const SWEEP = 270;
const STROKE = 12;

// Where a value falls against its limits. The fill walks accent -> warning ->
// critical, and every non-accent state also carries an icon and a word.
//   data: { value }
// Options: title, subtitle, min, max, warn, crit (thresholds; set `invert` when
// low is bad), valueFormat, unit, height.
export class Gauge extends Panel {
  constructor(host, opts = {}) {
    super(host, { height: 170, min: 0, max: 100, valueFormat: full, legend: 'never', ...opts }, 'gauge');
    this.track = s('path', { class: 'cc-gauge-track' });
    this.fill = s('path', { class: 'cc-gauge-fill' });
    this.ticks = s('g');
    this.text = s('text', { class: 'cc-gauge-value', 'text-anchor': 'middle' });
    this.limits = s('g', { class: 'cc-gauge-limits' });
    this.hit = s('rect', { fill: 'transparent' });
    this.svg.append(this.track, this.ticks, this.fill, this.text, this.limits, this.hit);
    this.svg.setAttribute('tabindex', '0');
    this.svg.setAttribute('role', 'meter');
    this.statusEl = h('div', { class: 'cc-gauge-status' });
    this.root.insertBefore(this.statusEl, this.legendEl);
    this.hit.addEventListener('pointermove', ev => this.tip([ev.clientX, ev.clientY]));
    this.hit.addEventListener('pointerleave', () => hideTip());
    this.svg.addEventListener('focus', () => this.tip(null));
    this.svg.addEventListener('blur', () => hideTip());
    if (opts.data) this.update(opts.data);
  }

  update(data) {
    this.value = data.value;
    this.invalidate();
  }

  state() {
    const { warn, crit, invert } = this.opts;
    const v = this.value;
    if (v == null) return 'good';
    const past = t => t != null && (invert ? v <= t : v >= t);
    return past(crit) ? 'critical' : past(warn) ? 'warning' : 'good';
  }

  render() {
    const o = this.opts;
    if (this.value == null) return;
    const H = (this.ht = o.height);
    const r = Math.min(this.w / 2 - STROKE, H / 2 + 6) - 4;
    const cx = this.w / 2;
    const cy = H / 2 + 6;
    const f = linear(o.min, o.max, START, START + SWEEP);
    const v = Math.max(o.min, Math.min(o.max, this.value));
    const state = this.state();
    this.root.dataset.state = state;
    this.track.setAttribute('d', arcPath(cx, cy, r, START, START + SWEEP));
    this.fill.setAttribute('d', v > o.min ? arcPath(cx, cy, r, START, Math.max(START + 0.5, f(v))) : '');
    this.fill.setAttribute('class', `cc-gauge-fill cc-gauge-${state}`);
    this.track.setAttribute('class', `cc-gauge-track cc-gauge-${state}`);
    const tick = t => {
      const a = (f(t) * Math.PI) / 180;
      return s('line', { x1: cx + (r - STROKE / 2 - 3) * Math.cos(a), y1: cy + (r - STROKE / 2 - 3) * Math.sin(a), x2: cx + (r + STROKE / 2 + 3) * Math.cos(a), y2: cy + (r + STROKE / 2 + 3) * Math.sin(a), class: 'cc-gauge-tick' });
    };
    this.ticks.replaceChildren(...[o.warn, o.crit].filter(t => t != null).map(tick));
    this.text.setAttribute('x', cx);
    this.text.setAttribute('y', cy + 8);
    this.text.textContent = `${o.valueFormat(this.value)}${o.unit || ''}`;
    const lo = s('text', { x: cx - r * 0.72, y: cy + r * 0.78 + 14, 'text-anchor': 'middle', class: 'cc-tick', text: o.valueFormat(o.min) });
    const hi = s('text', { x: cx + r * 0.72, y: cy + r * 0.78 + 14, 'text-anchor': 'middle', class: 'cc-tick', text: o.valueFormat(o.max) });
    this.limits.replaceChildren(lo, hi);
    this.hit.setAttribute('x', 0);
    this.hit.setAttribute('y', 0);
    this.hit.setAttribute('width', this.w);
    this.hit.setAttribute('height', H);
    this.statusEl.replaceChildren(statusChip(state));
    this.svg.setAttribute('aria-valuenow', this.value);
    this.svg.setAttribute('aria-valuemin', o.min);
    this.svg.setAttribute('aria-valuemax', o.max);
    this.svg.setAttribute('aria-valuetext', `${o.valueFormat(this.value)}${o.unit || ''}, ${state === 'good' ? 'normal' : state}`);
    this.svg.setAttribute('aria-label', `${o.title || 'Gauge'}. Use the table view for the limits.`);
  }

  tip(client) {
    const o = this.opts;
    const r = this.svg.getBoundingClientRect();
    const c = client || [r.left + this.w / 2, r.top + 12];
    const rows = [{ cls: 'cc-s1', shape: 'bar', name: o.title || 'Value', value: `${o.valueFormat(this.value)}${o.unit || ''}` }];
    if (o.warn != null) rows.push({ name: 'Warning at', value: o.valueFormat(o.warn) });
    if (o.crit != null) rows.push({ name: 'Critical at', value: o.valueFormat(o.crit) });
    showTip(this.svg, c[0], c[1], { title: this.opts.subtitle || '', rows });
  }

  tableModel() {
    const o = this.opts;
    if (this.value == null) return null;
    return {
      caption: `${o.title || 'Gauge'} values`,
      head: ['Measure', 'Value'],
      rows: [['Current', `${o.valueFormat(this.value)}${o.unit || ''}`], ['State', this.state() === 'good' ? 'Normal' : this.state()], ['Minimum', o.valueFormat(o.min)], ['Maximum', o.valueFormat(o.max)], ...(o.warn != null ? [['Warning at', o.valueFormat(o.warn)]] : []), ...(o.crit != null ? [['Critical at', o.valueFormat(o.crit)]] : [])],
    };
  }
}
