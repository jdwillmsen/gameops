// The panel contract. A panel type is
//   { type, title, about, size: { w, h },
//     query(ctx) -> model,            // pure, JSON-safe; ctx = { data, units, filter, now }
//     create(host, { height }) -> { update(model), destroy() } }
// query is pure and cheap so the dashboard can skip a redraw when a model
// equals the last one; create owns the DOM. A new chart type only has to
// register one of these.
import { BarChart } from './charts/bar.js';
import { Gauge } from './charts/gauge.js';
import { LineChart } from './charts/line.js';
import { StatTile, statusChip } from './charts/spark.js';
import { createRegistry } from './charts/series.js';
import { alertsOf, ago } from './fleet.js';
import { STATE_LABEL } from './overview.js';
import { healthShare, windowSamples, throughput, outcomes, taskDurations, durationHistogram, median, stateOf } from './dash-data.js';

const MAX_BARS = 12;
const pct = v => `${Math.round(v)}%`;
const secs = v => (v == null ? '-' : v < 90 ? `${Math.round(v)} s` : `${Math.round(v / 60)} min`);
const el = (tag, props = {}, ...kids) => {
  const e = Object.assign(document.createElement(tag), props);
  e.append(...kids);
  return e;
};
const clock = t => new Date(t).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });

// Charts redraw lazily, so a wrapper only forwards the model.
const wrap = (chart, extra = {}) => ({ update: m => chart.update(m), destroy: () => chart.destroy(), ...extra });

// A line needs two points to have a width; a window that holds one sample is
// drawn with an empty point before it rather than as a collapsed axis.
export function twoPoints(m) {
  if (m.x.length >= 2) return m;
  const t = m.x.length ? m.x[0] : Date.now();
  return { x: [t - 1000, t], series: m.series.map(sr => ({ ...sr, values: [null, sr.values[0] ?? null] })) };
}

const lineChart = (opts, to = m => m) => (host, { height }) => {
  const chart = new LineChart(host, { height, brush: false, registry: createRegistry(), ...opts });
  return wrap(chart, { update: m => chart.update(twoPoints(to(m))) });
};

// A bar chart with nothing to plot cannot lay out an axis, so the panel says
// so instead and keeps the last drawing out of sight.
const barChart = opts => (host, { height }) => {
  const chart = new BarChart(host, { registry: createRegistry(), height: opts.horizontal ? 'auto' : height, ...opts });
  const none = el('p', { className: 'db-empty', textContent: `${opts.title}: no data in this window yet.`, hidden: true });
  chart.root.after(none);
  return {
    update: m => {
      const empty = !m.categories.length;
      none.hidden = !empty;
      chart.root.style.display = empty ? 'none' : '';
      if (!empty) chart.update(m);
    },
    destroy: () => { chart.destroy(); none.remove(); },
  };
};

// A horizontal bar view of one value per unit, worst or biggest first.
const perUnit = (units, f, { asc = false } = {}) => {
  const rows = units.map(s => ({ name: s.name || s.id, v: f(s) })).filter(r => r.v != null);
  rows.sort((a, b) => (asc ? a.v - b.v : b.v - a.v) || a.name.localeCompare(b.name));
  return rows.slice(0, MAX_BARS);
};

const tileStatus = (value, { good, warn }) => (value == null ? {} : value >= good ? { status: 'good', statusLabel: 'Healthy' } : value >= warn ? { status: 'warning', statusLabel: 'Degraded' } : { status: 'critical', statusLabel: 'Failing' });

function tile(host) {
  const t = new StatTile(host, { label: '', value: '-', format: n => String(n), trendFormat: n => (n == null ? 'unknown' : String(Math.round(n))) });
  return t;
}

const states = [
  { key: 'working', name: 'Working' },
  { key: 'idle', name: 'Idle' },
  { key: 'dead', name: 'Dead' },
  { key: 'offline', name: 'Offline' },
];

export const PANELS = {
  'fleet-health': {
    title: 'Fleet health',
    about: 'Share of units with no alert: online, alive, and neither hurt nor hungry.',
    size: { w: 3, h: 2 },
    query: ({ units }) => ({ value: healthShare(units) ?? 0, n: units.length }),
    create: (host, { height }) => wrap(new Gauge(host, { title: 'Fleet health', subtitle: 'units with no alert', unit: '%', warn: 60, crit: 30, invert: true, height: Math.max(150, height) })),
  },

  'fleet-states': {
    title: 'Units by state',
    about: 'How many units were working, idle, dead or offline over time, for the whole fleet.',
    size: { w: 6, h: 2 },
    query: ({ data, filter }) => {
      const rows = windowSamples(data.samples(), filter);
      return { x: rows.map(r => r.t), series: states.map(st => ({ key: st.key, name: st.name, values: rows.map(r => r[st.key]) })) };
    },
    create: lineChart({ title: 'Units by state', subtitle: 'whole fleet, stacked', stacked: true, valueFormat: String }),
  },

  throughput: {
    title: 'Task throughput',
    about: 'Tasks finished per time bucket, from the fleet event feed.',
    size: { w: 6, h: 2 },
    query: ({ data, filter }) => {
      const t = throughput(data.feed(), filter);
      return { x: t.x, series: [{ key: 'done', name: 'Done', values: t.done }, { key: 'failed', name: 'Failed', values: t.failed }], size: t.size };
    },
    create: lineChart({ title: 'Task throughput', subtitle: 'tasks finished per bucket', valueFormat: String }, m => ({ x: m.x, series: m.series })),
  },

  'success-rate': {
    title: 'Success rate',
    about: 'Finished tasks that were done rather than failed. Cancelled tasks are left out.',
    size: { w: 3, h: 1 },
    query: ({ data, filter }) => {
      const o = outcomes(data.feed(), filter);
      return { rate: o.rate == null ? null : Math.round(o.rate * 100), done: o.done, failed: o.failed };
    },
    create: host => {
      const t = tile(host);
      return {
        update: m => t.update({ label: 'Success rate', value: m.rate == null ? 'No tasks yet' : `${m.rate}%`, ...tileStatus(m.rate, { good: 90, warn: 70 }), delta: m.rate == null ? undefined : { value: m.failed, label: `failed of ${m.done + m.failed}`, goodWhenUp: false, format: String } }),
        destroy: () => t.destroy(),
      };
    },
  },

  'alert-count': {
    title: 'Alerts',
    about: 'Units with at least one alert now: offline, dead, low health or low hunger.',
    size: { w: 3, h: 1 },
    query: ({ data, units, filter }) => {
      const rows = windowSamples(data.samples(), filter).slice(-24);
      const alerts = units.filter(s => alertsOf(s).length).length;
      return { alerts, dead: units.filter(s => s.online && s.dead).length, values: rows.map(r => r.alerts), labels: rows.map(r => clock(r.t)) };
    },
    create: host => {
      const t = tile(host);
      return {
        update: m => t.update({ label: 'Units with alerts', value: m.alerts, status: m.alerts === 0 ? 'good' : m.dead ? 'critical' : 'warning', statusLabel: m.alerts === 0 ? 'None' : m.dead ? 'Unit dead' : 'Needs attention', trend: m.values.length > 1 ? { values: m.values, labels: m.labels } : undefined }),
        destroy: () => t.destroy(),
      };
    },
  },

  'tasks-by-unit': {
    title: 'Outcomes per unit',
    about: 'Tasks done and failed by each unit in the window.',
    size: { w: 6, h: 3 },
    query: ({ data, filter }) => {
      const o = outcomes(data.feed(), filter);
      const rows = [...o.perUnit].map(([unit, c]) => ({ unit, ...c })).sort((a, b) => b.done + b.failed - (a.done + a.failed) || a.unit.localeCompare(b.unit)).slice(0, MAX_BARS);
      return { categories: rows.map(r => r.unit), series: [{ key: 'done', name: 'Done', values: rows.map(r => r.done) }, { key: 'failed', name: 'Failed', values: rows.map(r => r.failed) }] };
    },
    create: barChart({ title: 'Outcomes per unit', subtitle: 'done and failed in the window', horizontal: true, stacked: true }),
  },

  'idle-time': {
    title: 'Idle time',
    about: 'Share of the time this page has watched each unit online that it had no task.',
    size: { w: 6, h: 3 },
    query: ({ data, units }) => {
      const byId = new Map(data.idle(units.map(s => s.id)).map(r => [r.id, r]));
      const rows = perUnit(units, s => {
        const r = byId.get(s.id);
        return r && r.onlineMs > 0 ? (r.idleMs / r.onlineMs) * 100 : null;
      });
      return { categories: rows.map(r => r.name), series: [{ key: 'idle', name: 'Idle', values: rows.map(r => Math.round(r.v)) }] };
    },
    create: barChart({ title: 'Idle time', subtitle: 'share of watched online time with no task', horizontal: true, valueFormat: pct, yFormat: pct }),
  },

  hunger: {
    title: 'Hunger',
    about: 'Food level of each unit, hungriest first. 20 is full.',
    size: { w: 6, h: 3 },
    query: ({ units }) => {
      const rows = perUnit(units.filter(s => s.online), s => (typeof s.hunger === 'number' ? s.hunger : null), { asc: true });
      return { categories: rows.map(r => r.name), series: [{ key: 'hunger', name: 'Hunger', values: rows.map(r => r.v) }] };
    },
    create: barChart({ title: 'Hunger', subtitle: 'food level, hungriest first', horizontal: true }),
  },

  'task-durations': {
    title: 'Task durations',
    about: 'How long finished tasks ran, for tasks whose start this page also saw.',
    size: { w: 6, h: 2 },
    query: ({ data, filter }) => {
      const d = taskDurations(data.feed(), filter);
      const hist = durationHistogram(d);
      return { categories: hist.categories, series: [{ key: 'done', name: 'Done', values: hist.done }, { key: 'failed', name: 'Failed', values: hist.failed }], median: median(d.map(x => x.secs)), n: d.length };
    },
    create: (host, { height }) => {
      const chart = new BarChart(host, { title: 'Task durations', subtitle: 'waiting for tasks', stacked: true, height, registry: createRegistry() });
      return {
        update: m => {
          chart.opts.subtitle = m.n ? `median ${secs(m.median)} over ${m.n} tasks` : 'no finished tasks seen yet';
          const sub = chart.root.querySelector('.cc-sub');
          if (sub) sub.textContent = chart.opts.subtitle;
          chart.update({ categories: m.categories, series: m.series });
        },
        destroy: () => chart.destroy(),
      };
    },
  },

  'unit-status': {
    title: 'Unit status',
    about: 'Every unit with its state, health, hunger and task.',
    size: { w: 6, h: 3 },
    query: ({ units }) => ({
      rows: units.map(s => {
        const st = stateOf(s);
        return { id: s.id, name: s.name || s.id, state: st, hp: typeof s.health === 'number' ? Math.round(s.health) : null, food: typeof s.hunger === 'number' ? s.hunger : null, task: st === 'working' ? String(s.task.template || 'task').replace('gather_and_deposit', 'gather') : '' };
      }),
    }),
    create: host => {
      const body = el('tbody');
      const root = el('figure', { className: 'cc-root cc-panel db-table' },
        el('header', { className: 'cc-head' }, el('div', { className: 'cc-titles' }, el('h3', { className: 'cc-title', textContent: 'Unit status' }))),
        el('div', { className: 'db-scroll' }, el('table', {}, el('caption', { className: 'cc-sr', textContent: 'Unit status' }),
          el('thead', {}, el('tr', {}, ...['Unit', 'State', 'Health', 'Hunger', 'Task'].map(t => el('th', { scope: 'col', textContent: t, className: t === 'Health' || t === 'Hunger' ? 'cc-num' : '' })))), body)));
      host.append(root);
      const tone = { working: 'good', idle: 'good', dead: 'critical', offline: 'serious', stuck: 'warning' };
      return {
        update: m => {
          body.replaceChildren(...m.rows.map(r => el('tr', {},
            el('th', { scope: 'row', textContent: r.name }),
            el('td', {}, statusChip(tone[r.state], STATE_LABEL[r.state])),
            el('td', { className: 'cc-num', textContent: r.hp == null ? '-' : String(r.hp) }),
            el('td', { className: 'cc-num', textContent: r.food == null ? '-' : String(r.food) }),
            el('td', { textContent: r.task || '-' }))));
        },
        destroy: () => root.remove(),
      };
    },
  },

  'recent-events': {
    title: 'Recent alerts',
    about: 'The latest warnings and critical events from the fleet feed.',
    size: { w: 6, h: 3 },
    query: ({ data, filter, now }) => ({
      rows: data.feed().filter(e => (e.level === 'warn' || e.level === 'crit') && (!filter.unit || e.unit === filter.unit) && Date.parse(e.at) >= filter.from).slice(-12).reverse()
        .map(e => ({ seq: e.seq, level: e.level, who: e.name || e.unit, text: e.text, ago: ago(now - Date.parse(e.at)) })),
    }),
    create: host => {
      const list = el('ul', { className: 'db-events' });
      const empty = el('p', { className: 'db-empty', textContent: 'No warnings in this window.' });
      const root = el('figure', { className: 'cc-root cc-panel db-table' },
        el('header', { className: 'cc-head' }, el('div', { className: 'cc-titles' }, el('h3', { className: 'cc-title', textContent: 'Recent alerts' }))),
        el('div', { className: 'db-scroll' }, list, empty));
      host.append(root);
      return {
        update: m => {
          empty.hidden = m.rows.length > 0;
          list.replaceChildren(...m.rows.map(r => el('li', {},
            statusChip(r.level === 'crit' ? 'critical' : 'warning', r.level === 'crit' ? 'Critical' : 'Warning'),
            el('b', { textContent: r.who }), ' ', r.text, el('span', { className: 'db-ago', textContent: ` ${r.ago} ago` }))));
        },
        destroy: () => root.remove(),
      };
    },
  },
};

export const PANEL_TYPES = new Set(Object.keys(PANELS));

export function defaultLayout() {
  const order = ['fleet-health', 'throughput', 'success-rate', 'alert-count', 'fleet-states', 'tasks-by-unit', 'unit-status', 'idle-time', 'hunger', 'task-durations', 'recent-events'];
  return { panels: order.map((type, i) => ({ id: `p${i + 1}`, type, w: PANELS[type].size.w, h: PANELS[type].size.h })) };
}
