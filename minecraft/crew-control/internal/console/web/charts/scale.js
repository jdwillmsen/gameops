export function linear(d0, d1, r0, r1) {
  const k = d1 === d0 ? 0 : (r1 - r0) / (d1 - d0);
  const f = v => r0 + (v - d0) * k;
  f.invert = p => (k === 0 ? d0 : d0 + (p - r0) / k);
  return f;
}

// Ticks land on 1, 2 or 5 times a power of ten; the returned domain is the
// tick range so the top gridline always sits at or above the data.
export function niceTicks(min, max, count = 5) {
  if (!Number.isFinite(min) || !Number.isFinite(max)) return { ticks: [0, 1], lo: 0, hi: 1, step: 1 };
  if (min === max) {
    if (min === 0) max = 1;
    else {
      const pad = Math.abs(min) * 0.1;
      min -= pad;
      max += pad;
    }
  }
  const raw = (max - min) / Math.max(1, count - 1);
  const mag = 10 ** Math.floor(Math.log10(raw));
  const norm = raw / mag;
  const step = mag * (norm < 1.5 ? 1 : norm < 3 ? 2 : norm < 7 ? 5 : 10);
  const lo = Math.floor(min / step + 1e-9) * step;
  const hi = Math.ceil(max / step - 1e-9) * step;
  const ticks = [];
  for (let v = lo; v <= hi + step / 2; v += step) ticks.push(+v.toPrecision(12));
  return { ticks, lo: ticks[0], hi: ticks[ticks.length - 1], step };
}

const trim = (n, d) => {
  const s = n.toFixed(d);
  return s.includes('.') ? s.replace(/\.?0+$/, '') : s;
};

// Compact from 1,000 up so axis ticks stay short: 0 / 1K / 2.5K / 1.2M.
export function compact(v) {
  if (v == null || Number.isNaN(v)) return '–';
  const a = Math.abs(v);
  if (a >= 1e12) return trim(v / 1e12, 1) + 'T';
  if (a >= 1e9) return trim(v / 1e9, 1) + 'B';
  if (a >= 1e6) return trim(v / 1e6, 1) + 'M';
  if (a >= 1e3) return trim(v / 1e3, 1) + 'K';
  if (a >= 100) return trim(v, 0);
  if (a >= 1) return trim(v, 1);
  return a === 0 ? '0' : trim(v, 2);
}

export function full(v) {
  if (v == null || Number.isNaN(v)) return '–';
  return Math.abs(v) >= 100 ? Math.round(v).toLocaleString('en-US') : trim(v, 2);
}

// First index whose value is >= v.
export function lowerBound(xs, v) {
  let lo = 0;
  let hi = xs.length;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (xs[mid] < v) lo = mid + 1;
    else hi = mid;
  }
  return lo;
}

export function nearest(xs, v, from = 0, to = xs.length - 1) {
  if (to < from) return -1;
  let i = lowerBound(xs, v);
  if (i <= from) return from;
  if (i > to) return to;
  return v - xs[i - 1] <= xs[i] - v ? i - 1 : i;
}

// Null counts as zero: a gap in a stack must not tear the layers above it.
export function stack(seriesValues) {
  const n = seriesValues.length ? seriesValues[0].length : 0;
  const base = new Array(n).fill(0);
  return seriesValues.map(vals => {
    const lo = base.slice();
    const hi = new Array(n);
    for (let i = 0; i < n; i++) {
      const v = vals[i];
      hi[i] = base[i] + (v == null || Number.isNaN(v) ? 0 : v);
      base[i] = hi[i];
    }
    return { lo, hi };
  });
}

const SEC = 1000;
const MIN = 60 * SEC;
const HOUR = 60 * MIN;
const DAY = 24 * HOUR;
const STEPS = [SEC, 2 * SEC, 5 * SEC, 10 * SEC, 15 * SEC, 30 * SEC, MIN, 2 * MIN, 5 * MIN, 10 * MIN, 15 * MIN, 30 * MIN, HOUR, 2 * HOUR, 3 * HOUR, 6 * HOUR, 12 * HOUR, DAY, 2 * DAY, 7 * DAY, 30 * DAY];

const offsetMs = (t, tz) => (tz === 'UTC' ? 0 : -new Date(t).getTimezoneOffset() * MIN);

export function timeStep(span, count) {
  return STEPS.find(s => span / s <= count) || STEPS[STEPS.length - 1];
}

export function timeTicks(t0, t1, count = 6, tz = 'local') {
  const step = timeStep(t1 - t0, count);
  const off = offsetMs(t0, tz);
  const ticks = [];
  for (let t = Math.ceil((t0 + off) / step) * step - off; t <= t1; t += step) ticks.push(t);
  return { ticks, step };
}

const pad2 = n => String(n).padStart(2, '0');
const MONTHS = ['Jan', 'Feb', 'Mar', 'Apr', 'May', 'Jun', 'Jul', 'Aug', 'Sep', 'Oct', 'Nov', 'Dec'];

function parts(t, tz) {
  const d = new Date(t);
  return tz === 'UTC'
    ? { mo: d.getUTCMonth(), d: d.getUTCDate(), h: d.getUTCHours(), m: d.getUTCMinutes(), s: d.getUTCSeconds() }
    : { mo: d.getMonth(), d: d.getDate(), h: d.getHours(), m: d.getMinutes(), s: d.getSeconds() };
}

export function tickLabel(t, step, tz) {
  const p = parts(t, tz);
  if (step >= DAY) return `${MONTHS[p.mo]} ${p.d}`;
  if (step < MIN) return `${pad2(p.h)}:${pad2(p.m)}:${pad2(p.s)}`;
  return `${pad2(p.h)}:${pad2(p.m)}`;
}

export function dateTime(t, tz) {
  const p = parts(t, tz);
  return `${MONTHS[p.mo]} ${p.d}, ${pad2(p.h)}:${pad2(p.m)}:${pad2(p.s)}`;
}
