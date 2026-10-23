import { observeFrames, renderOffscreen, BarChart, Gauge, Heatmap, LineChart, StatTile, createRegistry, createTimeRange, mountRangeFilter, setTexture, setTheme } from './index.js';
import { NOW, STEP, UNITS, grid, rng, series, times, walk } from './synth.js';

const $ = id => document.getElementById(id);
const q = new URLSearchParams(location.search);
const card = host => {
  const d = document.createElement('div');
  host.append(d);
  return d;
};

renderOffscreen(q.get('eager') === '1');
setTheme(document.documentElement, q.get('theme') || 'auto');
setTexture(document.documentElement, q.get('texture') === '1');
for (const b of document.querySelectorAll('[data-theme-set]')) b.addEventListener('click', () => setTheme(document.documentElement, b.dataset.themeSet));
const texBtn = $('texture');
texBtn.setAttribute('aria-pressed', String(q.get('texture') === '1'));
texBtn.addEventListener('click', () => {
  const on = texBtn.getAttribute('aria-pressed') !== 'true';
  texBtn.setAttribute('aria-pressed', String(on));
  setTexture(document.documentElement, on);
});

const rand = rng(167);
const N = 288;
const x = times(N);
const tz = 'UTC';

// One range store scopes every time-series panel; the filter row drives it.
const range = createTimeRange();
range.setDomain(x[0], x[N - 1]);
mountRangeFilter($('filter'), range);
const registry = createRegistry();

// Tiles
const spark = (base, vol) => ({ values: walk(rand, 24, { base, vol, min: 0 }), labels: times(24, STEP, NOW).map(t => new Date(t).toISOString().slice(11, 16) + ' UTC') });
new StatTile(card($('tiles')), { label: 'Units online', value: 18, delta: { value: 2, label: 'vs prior hour' }, trend: spark(16, 3) });
new StatTile(card($('tiles')), { label: 'Tasks per minute', value: 1284, delta: { value: -92, label: 'vs prior hour' }, trend: spark(1300, 140) });
new StatTile(card($('tiles')), { label: 'Failed tasks', value: 37, delta: { value: 9, label: 'vs prior hour', goodWhenUp: false }, trend: spark(30, 8), status: 'warning', statusLabel: 'Above usual' });
new StatTile(card($('tiles')), { label: 'Stream latency', value: '412 ms', delta: { value: 0, label: 'vs prior hour' }, trend: { ...spark(400, 60), }, status: 'critical', statusLabel: 'Over budget' });

// Time series
const ms = v => (v == null ? '–' : `${Math.round(v)} ms`);
const lines = $('lines');
new LineChart(card(lines), { title: 'Latency by service', subtitle: 'p95, last 24 hours', tz, range, registry, yFormat: v => `${v}`, valueFormat: ms,
  data: { x, series: series(rand, ['Camera', 'Work queue', 'Positions'], N, { base: 120, vol: 14, min: 10 }) } });
new LineChart(card(lines), { title: 'Throughput', subtitle: 'tasks per minute, single series', tz, range, registry: createRegistry(), area: true,
  data: { x, series: [{ key: 'tasks', name: 'Tasks', values: walk(rand, N, { base: 1200, vol: 120, min: 0, swell: 300 }) }] } });
new LineChart(card(lines), { title: 'Blocks mined by material', subtitle: 'stacked, per 5 minutes', tz, range, registry: createRegistry(), stacked: true,
  data: { x, series: series(rand, ['Stone', 'Dirt', 'Iron ore', 'Coal'], N, { base: 40, vol: 8, min: 2 }) } });
new LineChart(card(lines), { title: 'Load across 20 units', subtitle: 'first 8 keep a hue, the rest fold into Other', tz, range, registry: createRegistry(), otherCombine: 'mean',
  data: { x, series: series(rand, UNITS, N, { base: 30, vol: 6, min: 0, max: 100 }) } });

// Bars
const bars = $('bars');
const tasks = ['Gather', 'Haul', 'Goto', 'Stock', 'Plan', 'Build'];
new BarChart(card(bars), { title: 'Tasks done today', subtitle: 'one series, one colour', registry: createRegistry(),
  data: { categories: tasks, series: [{ key: 'done', name: 'Done', values: [412, 268, 190, 96, 54, 31] }] } });
new BarChart(card(bars), { title: 'Busiest units', subtitle: 'horizontal, value at the tip', horizontal: true, height: 'auto', registry: createRegistry(),
  data: { categories: UNITS.slice(0, 8), series: [{ key: 'tasks', name: 'Tasks', values: [320, 280, 255, 210, 188, 140, 96, 41] }] } });
new BarChart(card(bars), { title: 'Outcome by task kind', subtitle: 'grouped', registry: createRegistry(),
  data: { categories: tasks, series: [
    { key: 'done', name: 'Done', values: [412, 268, 190, 96, 54, 31] },
    { key: 'failed', name: 'Failed', values: [21, 34, 9, 12, 5, 8] },
    { key: 'cancelled', name: 'Cancelled', values: [8, 15, 4, 3, 9, 2] } ] } });
new BarChart(card(bars), { title: 'Time spent per unit', subtitle: 'stacked, hours', stacked: true, registry: createRegistry(),
  data: { categories: UNITS.slice(0, 10), series: [
    { key: 'work', name: 'Working', values: [6.5, 7, 5.5, 6, 7.5, 4, 6, 5, 3.5, 6.5] },
    { key: 'idle', name: 'Idle', values: [1.5, 1, 2, 1.5, 0.5, 2, 1, 2.5, 3, 1] },
    { key: 'travel', name: 'Travelling', values: [2, 1.5, 2.5, 2, 1, 3, 2, 1.5, 2.5, 1.5] },
    { key: 'down', name: 'Offline', values: [0, 0.5, 0, 0.5, 0, 1, 0, 1, 1, 0] } ] } });

// Heatmaps
const heat = $('heat');
const days = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun'];
const hours = Array.from({ length: 24 }, (_, i) => String(i).padStart(2, '0'));
new Heatmap(card(heat), { title: 'Activity by hour', subtitle: 'tasks started, sequential', rowTitle: 'Day', valueName: 'Tasks', data: { rows: days, cols: hours, values: grid(rand, 7, 24) } });
const delta = grid(rand, 7, 24).map(r => r.map(v => v - 50));
new Heatmap(card(heat), { title: 'Change against last week', subtitle: 'diverging around zero, tasks', diverging: true, cellLabels: false, rowTitle: 'Day', valueName: 'Change', data: { rows: days, cols: hours, values: delta } });

// Gauges
const gauges = $('gauges');
new Gauge(card(gauges), { title: 'Fleet load', subtitle: 'share of units working', unit: '%', warn: 70, crit: 90, data: { value: 42 } });
new Gauge(card(gauges), { title: 'Queue depth', subtitle: 'open tasks', warn: 60, crit: 90, data: { value: 74 } });
new Gauge(card(gauges), { title: 'Disk used', subtitle: 'persistent volume', unit: '%', warn: 70, crit: 90, data: { value: 93 } });
new Gauge(card(gauges), { title: 'Units healthy', subtitle: 'low is bad', unit: '%', warn: 60, crit: 30, invert: true, data: { value: 24 } });

// Palette
{
  const cs = getComputedStyle(document.body);
  const group = (name, vars) => {
    const g = document.createElement('div');
    g.className = 'pal-group';
    const label = document.createElement('span');
    label.textContent = name;
    const row = document.createElement('div');
    row.className = 'pal-row';
    for (const v of vars) {
      const i = document.createElement('i');
      i.style.setProperty('--c', `var(${v})`);
      i.title = `${v} ${cs.getPropertyValue(v).trim()}`;
      row.append(i);
    }
    g.append(label, row);
    return g;
  };
  const n = (p, k) => Array.from({ length: k }, (_, i) => `--cc-${p}-${i + 1}`);
  $('palette').className = 'pal';
  $('palette').append(
    group('Categorical, in fixed order', [...n('series', 8), '--cc-other']),
    group('Sequential', n('seq', 7)),
    group('Diverging', ['--cc-div-neg', '--cc-div-mid', '--cc-div-pos']),
    group('Status', ['--cc-status-good', '--cc-status-warning', '--cc-status-serious', '--cc-status-critical']));
}

// Live: 20 small charts, each fed one new point per second.
const WINDOW = 60;
const liveHost = $('live');
const liveCharts = UNITS.map((name, i) => {
  const r = rng(1000 + i);
  const data = { x: times(WINDOW, 1000, NOW), series: [
    { key: 'cpu', name: 'CPU', values: walk(r, WINDOW, { base: 40, vol: 12, min: 0, max: 100 }) },
    { key: 'mem', name: 'Memory', values: walk(r, WINDOW, { base: 60, vol: 5, min: 0, max: 100 }) } ] };
  const chart = new LineChart(card(liveHost), { title: name, subtitle: 'CPU and memory, %', height: 120, brush: false, labels: false, legend: 'never', tz, registry: createRegistry(), y: { min: 0, max: 100 }, data });
  return { chart, data, r };
});

const stats = { ticks: 0, frames: 0, dropped: 0, last: 0, worstFrame: 0, tick: [], draw: [], settle: [] };
let pendingTick = 0;
let settleFrames = 0;
observeFrames(({ ms, pending }) => {
  stats.draw.push(ms);
  if (pendingTick) settleFrames++;
  if (pendingTick && !pending) {
    stats.settle.push(settleFrames);
    pendingTick = 0;
  }
});
window.__ccPerf = stats;
let timer = 0;
let raf = 0;
const frame = t => {
  if (stats.last) {
    const dt = t - stats.last;
    stats.frames++;
    if (dt > 25) stats.dropped++;
    if (dt > stats.worstFrame) stats.worstFrame = dt;
  }
  stats.last = t;
  raf = requestAnimationFrame(frame);
};
const pct = (a, p) => (a.length ? [...a].sort((u, v) => u - v)[Math.min(a.length - 1, Math.floor(a.length * p))] : 0);
function tick() {
  const t0 = performance.now();
  for (const L of liveCharts) {
    const { data, r } = L;
    data.x = [...data.x.slice(1), data.x[data.x.length - 1] + 1000];
    data.series = data.series.map(s => ({ ...s, values: [...s.values.slice(1), Math.max(0, Math.min(100, s.values[s.values.length - 1] + (r() - 0.5) * 10))] }));
    L.chart.update(data);
  }
  stats.tick.push(performance.now() - t0);
  stats.ticks++;
  pendingTick = 1;
  settleFrames = 0;
  $('perf').textContent = `${stats.ticks} ticks · update ${pct(stats.tick, 0.5).toFixed(1)} ms · frame draw p95 ${pct(stats.draw, 0.95).toFixed(1)} ms · frames ${stats.frames}, over 25 ms: ${stats.dropped}`;
}
const toggle = $('live-toggle');
function setLive(on) {
  toggle.setAttribute('aria-pressed', String(on));
  toggle.textContent = on ? 'Stop' : 'Start';
  clearInterval(timer);
  cancelAnimationFrame(raf);
  if (on) {
    Object.assign(stats, { ticks: 0, frames: 0, dropped: 0, last: 0, worstFrame: 0, tick: [], draw: [], settle: [] });
    timer = setInterval(tick, 1000);
    raf = requestAnimationFrame(frame);
  } else $('perf').textContent = 'stopped';
}
toggle.addEventListener('click', () => setLive(toggle.getAttribute('aria-pressed') !== 'true'));
if (q.get('live') === '1') setLive(true);
