const n1 = v => (Math.round(v * 10) / 10 + 0).toString();

// A null or NaN value lifts the pen, so a gap in the data is a gap in the line.
export function linePath(xs, ys) {
  let d = '';
  let pen = false;
  for (let i = 0; i < xs.length; i++) {
    const y = ys[i];
    if (y == null || Number.isNaN(y)) {
      pen = false;
      continue;
    }
    d += `${pen ? 'L' : 'M'}${n1(xs[i])} ${n1(y)}`;
    pen = true;
  }
  return d;
}

// Closed shape between a top and bottom edge, one sub-path per unbroken run.
export function areaPath(xs, hi, lo) {
  let d = '';
  let start = -1;
  const close = end => {
    if (start < 0) return;
    for (let i = start; i <= end; i++) d += `${i === start ? 'M' : 'L'}${n1(xs[i])} ${n1(hi[i])}`;
    for (let i = end; i >= start; i--) d += `L${n1(xs[i])} ${n1(lo[i])}`;
    d += 'Z';
    start = -1;
  };
  for (let i = 0; i < xs.length; i++) {
    const ok = hi[i] != null && !Number.isNaN(hi[i]);
    if (ok && start < 0) start = i;
    else if (!ok) close(i - 1);
  }
  close(xs.length - 1);
  return d;
}

// Rounded only at the data end; the baseline side stays square so bars read as
// growing from one line.
export function barPath(x, y, w, h, r, end) {
  if (w <= 0 || h <= 0) return '';
  const q = Math.max(0, Math.min(r, w / 2, h));
  const X = n1(x), Y = n1(y), R = n1(q);
  const x2 = n1(x + w), y2 = n1(y + h);
  if (!end || q === 0) return `M${X} ${Y}H${x2}V${y2}H${X}Z`;
  const a = (sweep, ex, ey) => `A${R} ${R} 0 0 ${sweep} ${ex} ${ey}`;
  const L = n1(x + q), Rt = n1(x + w - q), T = n1(y + q), B = n1(y + h - q);
  switch (end) {
    case 'top':
      return `M${X} ${y2}V${T}${a(1, L, Y)}H${Rt}${a(1, x2, T)}V${y2}Z`;
    case 'bottom':
      return `M${X} ${Y}V${B}${a(0, L, y2)}H${Rt}${a(0, x2, B)}V${Y}Z`;
    case 'right':
      return `M${X} ${Y}H${Rt}${a(1, x2, T)}V${B}${a(1, Rt, y2)}H${X}Z`;
    default:
      return `M${x2} ${Y}H${L}${a(0, X, T)}V${B}${a(0, L, y2)}H${x2}Z`;
  }
}

// Degrees clockwise from 3 o'clock, as SVG measures them.
export function arcPath(cx, cy, r, a0, a1) {
  const rad = a => (a * Math.PI) / 180;
  const p = a => `${n1(cx + r * Math.cos(rad(a)))} ${n1(cy + r * Math.sin(rad(a)))}`;
  return `M${p(a0)}A${n1(r)} ${n1(r)} 0 ${a1 - a0 > 180 ? 1 : 0} 1 ${p(a1)}`;
}

// Direct end-labels only work while they separate; nudging them apart detaches
// them from their lines, so the caller drops them all when this is false.
export function labelsFit(ys, minGap) {
  const sorted = [...ys].sort((a, b) => a - b);
  for (let i = 1; i < sorted.length; i++) if (sorted[i] - sorted[i - 1] < minGap) return false;
  return true;
}

export function truncate(text, max) {
  return text.length > max ? `${text.slice(0, max - 1)}…` : text;
}

// Width estimate for 11px system-ui; measuring would force a layout per label.
export const textWidth = (text, px = 6.2) => text.length * px;
