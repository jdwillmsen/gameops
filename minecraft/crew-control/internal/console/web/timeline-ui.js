// The timeline screen: one swim lane per unit over a chosen window, and a
// job's steps in the order they ran. timeline.js holds the logic; this file
// paints it. Both views carry positions in their event details, so they are
// for signed-in operators only and load nothing without a session.

import {
  buildLanes, colorFor, zoomRange, panRange, clampRange, ticks, cullBars, fetchWindow, jobRows, duration,
} from './timeline.js';

const SVG = 'http://www.w3.org/2000/svg';
const GUTTER = 96;
const LANE = 24;
const AXIS = 22;
const PRESETS = [['1h', 3600000], ['6h', 21600000], ['24h', 86400000], ['7d', 604800000]];
const OUTCOME_WORD = { done: 'done', failed: 'failed', cancelled: 'cancelled', ended: 'ended, outcome unknown', running: 'running', idle: 'idle polls' };

// Each outcome has a stroke or fill treatment as well as a word, so none of
// them leans on colour: failure is solid red edge plus an X, cancelled and
// unknown-outcome bars are dashed outlines.
const MARK_PATH = {
  failed: 'M-4 -4 L4 4 M4 -4 L-4 4',
  died: 'M0 -5 L5 0 L0 5 L-5 0 Z',
  stuck: 'M0 -5 L5 4 L-5 4 Z',
};
const MARK_WORD = { failed: 'task failed', died: 'died', stuck: 'stuck' };

function el(tag, props = {}, ...kids) {
  const e = Object.assign(document.createElement(tag), props);
  e.append(...kids);
  return e;
}

function svg(tag, attrs = {}, ...kids) {
  const e = document.createElementNS(SVG, tag);
  for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
  e.append(...kids);
  return e;
}

function svgText(attrs, text) {
  const e = svg('text', attrs);
  e.textContent = text;
  return e;
}

const clock = (t, withDate) => {
  const d = new Date(t);
  const time = d.toLocaleTimeString(undefined, { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' });
  return withDate ? `${d.toLocaleDateString(undefined, { month: 'short', day: 'numeric' })} ${time}` : time;
};

const eventText = e => (e.payload && e.payload.text) || e.kind.replace(/_/g, ' ');

// host: { statuses, order(), state(), jobs(), operator(), api(method, path), sync(), setView(view) }
export function initTimeline(host) {
  const perf = new URLSearchParams(location.search).get('perf') === '1';
  const measure = (name, start) => { if (perf) performance.measure(name, { start, end: performance.now() }); };

  let mounted = false;
  let mode = 'units';
  let span = 86400000;
  let fetched = null; // { range, units, lanes, truncated, failure }
  let view = null; // the visible window inside fetched.range
  let loading = 0;
  let selected = null; // the bar, mark or span whose events are open
  let drawn = []; // what the last paint put on screen, by data-i
  let jobLoad = null; // { id, state, job, rows, ... }
  const seen = [];

  const root = el('section', { className: 'cc-root tl' });
  root.setAttribute('data-theme', 'dark');
  root.setAttribute('aria-label', 'Timeline');

  const tabs = ['units', 'job'].map(m => {
    const b = el('button', { type: 'button', textContent: m === 'units' ? 'Units' : 'Job' });
    b.addEventListener('click', () => setMode(m));
    return [m, b];
  });
  const tabBox = el('div', { className: 'tl-group', role: 'group' }, ...tabs.map(t => t[1]));
  tabBox.setAttribute('aria-label', 'Timeline kind');

  const presets = PRESETS.map(([label, ms]) => {
    const b = el('button', { type: 'button', textContent: label });
    b.addEventListener('click', () => { span = ms; load(); });
    return [ms, b];
  });
  const presetBox = el('div', { className: 'tl-group', role: 'group' }, ...presets.map(p => p[1]));
  presetBox.setAttribute('aria-label', 'Window');

  const btn = (text, title, fn) => {
    const b = el('button', { type: 'button', textContent: text, title });
    b.setAttribute('aria-label', title);
    b.addEventListener('click', fn);
    return b;
  };
  const nav = el('div', { className: 'tl-group', role: 'group' },
    btn('←', 'Earlier window', () => shift(-1)),
    btn('−', 'Zoom out', () => zoom(2, 0.5)),
    btn('+', 'Zoom in', () => zoom(0.5, 0.5)),
    btn('→', 'Later window', () => shift(1)),
    btn('Now', 'Reload ending now', () => load()));

  const jobSelect = el('select');
  jobSelect.setAttribute('aria-label', 'Job');
  jobSelect.addEventListener('change', () => { host.state().job = jobSelect.value; host.sync(); loadJob(); });

  const note = el('span', { className: 'tl-note', role: 'status' });
  const bar = el('div', { className: 'tl-bar' }, tabBox, presetBox, jobSelect, nav, note);

  const legend = el('ul', { className: 'tl-legend' });
  const body = el('div', { className: 'tl-body' });
  const side = el('aside', { className: 'tl-side', hidden: true });
  side.setAttribute('aria-label', 'Events');
  const tip = el('div', { className: 'tl-tip', hidden: true });
  tip.setAttribute('role', 'tooltip');
  root.append(bar, legend, el('div', { className: 'tl-main' }, body, side), tip);

  // ---- loading ----

  function bounds() { return fetched ? fetched.range : [Date.now() - span, Date.now()]; }

  async function load(range) {
    if (!host.operator()) return paint();
    const my = ++loading;
    const to = range ? range[1] : Date.now();
    const r = range || [to - span, to];
    const units = host.order();
    note.textContent = 'loading…';
    const t0 = performance.now();
    const res = await fetchWindow(host.api, units, r);
    if (my !== loading || !mounted) return;
    measure('timeline-fetch', t0);
    if (res.failure && res.failure.status === 401) {
      host.setOperator?.(null);
      return;
    }
    const t1 = performance.now();
    const lanes = buildLanes(res.events, units, { from: r[0], to: Math.min(r[1], Date.now()) });
    measure('timeline-build', t1);
    fetched = { range: r, units, lanes, truncated: res.truncated, failure: res.failure, events: res.events };
    view = [...r];
    selected = null;
    paint();
  }

  function shift(dir) {
    const [a, b] = bounds();
    const w = b - a;
    if (mode === 'job') return;
    const to = Math.min(b + dir * w, Date.now());
    load([to - w, to]);
  }

  function zoom(factor, frac) {
    if (!fetched || !view) return;
    view = zoomRange(view, factor, frac, fetched.range);
    paintChart();
  }

  // ---- painting ----

  function paint() {
    const t0 = performance.now();
    for (const [m, b] of tabs) b.setAttribute('aria-pressed', String(m === mode));
    for (const [ms, b] of presets) b.setAttribute('aria-pressed', String(mode === 'units' && ms === span));
    jobSelect.hidden = mode !== 'job';
    presetBox.hidden = mode === 'job';
    if (!host.operator()) {
      legend.replaceChildren();
      side.hidden = true;
      body.replaceChildren(el('p', { className: 'tl-empty', textContent: 'Sign in as an operator to see the timeline.' }));
      note.textContent = '';
      return;
    }
    if (mode === 'job') paintJob();
    else paintChart();
    measure('timeline-paint', t0);
  }

  function setNote() {
    if (!fetched) return;
    const bits = [];
    if (fetched.failure) bits.push(`could not load some units (${fetched.failure.error})`);
    if (fetched.truncated) bits.push('busy units show only their newest events');
    bits.push(`${fetched.events.length} events`);
    note.textContent = bits.join(' · ');
  }

  function legendFor(templates, markKinds) {
    const items = templates.map(t => el('li', {}, el('span', { className: 'tl-sw', style: `background:${colorFor(t, seen)}` }), t || 'task'));
    for (const k of markKinds) {
      const s = svg('svg', { width: 12, height: 12, viewBox: '-6 -6 12 12', 'aria-hidden': 'true' }, svg('path', { d: MARK_PATH[k], fill: k === 'failed' ? 'none' : 'currentColor', stroke: 'currentColor', 'stroke-width': 1.6 }));
      items.push(el('li', {}, s, MARK_WORD[k]));
    }
    items.push(el('li', {}, el('span', { className: 'tl-sw tl-sw-off' }), 'offline'), el('li', {}, el('span', { className: 'tl-sw tl-sw-dead' }), 'dead'),
      el('li', {}, el('span', { className: 'tl-sw tl-sw-dash' }), 'cancelled or outcome unknown'));
    legend.replaceChildren(...items);
  }

  function paintChart() {
    if (!fetched) {
      body.replaceChildren(el('p', { className: 'tl-empty', textContent: 'loading…' }));
      return;
    }
    setNote();
    const [a, b] = view;
    const width = Math.max(600, body.clientWidth || 900);
    const plot = width - GUTTER - 12;
    const x = t => GUTTER + ((t - a) / (b - a)) * plot;
    const units = fetched.units;
    const height = AXIS + units.length * LANE + 4;
    const root = svg('svg', { class: 'tl-svg', width, height, viewBox: `0 0 ${width} ${height}`, role: 'group', tabindex: 0, 'aria-label': 'Unit timeline. Plus and minus zoom, arrow keys pan.' });
    const defs = svg('defs', {}, svg('pattern', { id: 'tl-hatch', width: 6, height: 6, patternUnits: 'userSpaceOnUse', patternTransform: 'rotate(45)' }, svg('rect', { width: 6, height: 6, fill: '#2c2c2a' }), svg('line', { x1: 0, y1: 0, x2: 0, y2: 6, stroke: '#898781', 'stroke-width': 2 })));
    root.append(defs);

    const { step, times } = ticks([a, b], Math.max(3, Math.floor(plot / 110)));
    const showDate = b - a > 20 * 3600000 || step >= 86400000;
    for (const t of times) {
      root.append(svg('line', { class: 'tl-grid', x1: x(t), x2: x(t), y1: AXIS - 4, y2: height }));
      root.append(svgText({ class: 'tl-tick', x: x(t), y: 12, 'text-anchor': 'middle' }, clock(t, showDate)));
    }

    drawn = [];
    const used = new Set();
    const markKinds = new Set();
    units.forEach((u, row) => {
      const y = AXIS + row * LANE;
      const lane = fetched.lanes.get(u);
      const label = svgText({ class: 'tl-lane', x: GUTTER - 8, y: y + LANE / 2 + 4, 'text-anchor': 'end' }, u);
      root.append(svg('line', { class: 'tl-rule', x1: GUTTER, x2: GUTTER + plot, y1: y + LANE, y2: y + LANE }), label);
      const clipped = (s, e) => e >= a && s <= b;
      const add = (kind, item) => {
        drawn.push({ kind, item });
        return drawn.length - 1;
      };
      for (const s of lane.spans) {
        if (!clipped(s.start, s.end)) continue;
        const x0 = Math.max(GUTTER, x(s.start));
        const x1 = Math.min(GUTTER + plot, x(s.end));
        root.append(svg('rect', { class: `tl-span tl-span-${s.kind}`, x: x0, y: y + 3, width: Math.max(1, x1 - x0), height: LANE - 6, fill: s.kind === 'offline' ? 'url(#tl-hatch)' : '#5a2b2b', 'data-i': add('span', s) }));
      }
      const bars = cullBars(lane.bars.filter(r => clipped(r.start, r.end)), x);
      for (const r of bars) {
        const x0 = Math.max(GUTTER, x(r.start));
        const x1 = Math.min(GUTTER + plot, x(r.end));
        const thin = r.outcome === 'idle';
        const color = thin ? '#898781' : colorFor(r.template, seen);
        if (!thin) used.add(r.template);
        const dashed = r.outcome === 'cancelled' || r.outcome === 'ended';
        const rect = svg('rect', {
          class: `tl-bar-${r.outcome}`, x: x0, y: thin ? y + 9 : y + 4, width: Math.max(1, x1 - x0), height: thin ? 6 : LANE - 8, rx: 2,
          fill: dashed ? 'none' : color, 'fill-opacity': thin ? 0.55 : 1,
          stroke: r.outcome === 'failed' ? '#e66767' : color, 'stroke-width': dashed || r.outcome === 'failed' ? 1.6 : 0,
          'data-i': add('bar', r),
        });
        if (dashed) rect.setAttribute('stroke-dasharray', '3 2');
        root.append(rect);
      }
      for (const m of lane.marks) {
        if (m.t < a || m.t > b) continue;
        markKinds.add(m.kind);
        const g = svg('g', { class: `tl-mark tl-mark-${m.kind}`, transform: `translate(${x(m.t)} ${y + LANE / 2})`, 'data-i': add('mark', m) },
          svg('path', { d: MARK_PATH[m.kind], fill: m.kind === 'failed' ? 'none' : '#f0efec', stroke: m.kind === 'failed' ? '#f0efec' : '#0b0b0b', 'stroke-width': m.kind === 'failed' ? 2 : 1.2 }));
        root.append(g);
      }
    });
    legendFor([...used], [...markKinds]);
    const hadFocus = chart && chart.el.parentNode === body && document.activeElement === chart.el;
    body.replaceChildren(root);
    chart = { el: root, plot };
    if (hadFocus) root.focus();
  }

  // ---- interaction ----

  // Listeners sit on the stable body, not the svg: every pan step replaces
  // the svg, and a drag or focus held on the old one would be lost mid-gesture.
  let chart = null; // { el, plot } of the svg currently on screen
  let drag = null;
  const live = ev => chart && chart.el.parentNode === body && chart.el.contains(ev.target);
  const frac = ev => {
    const r = chart.el.getBoundingClientRect();
    return Math.min(1, Math.max(0, (ev.clientX - r.left - GUTTER) / chart.plot));
  };
  body.addEventListener('wheel', ev => {
    if (!live(ev)) return;
    ev.preventDefault();
    zoom(ev.deltaY < 0 ? 0.7 : 1 / 0.7, frac(ev));
  });
  body.addEventListener('pointerdown', ev => {
    if (live(ev)) drag = { x: ev.clientX, view, moved: false };
  });
  body.addEventListener('pointermove', ev => {
    if (!live(ev)) return;
    if (drag) {
      const dx = ev.clientX - drag.x;
      if (Math.abs(dx) > 3) drag.moved = true;
      if (drag.moved) {
        const ms = -(dx / chart.plot) * (drag.view[1] - drag.view[0]);
        view = panRange(drag.view, ms, fetched.range);
        tip.hidden = true;
        paintChart();
        return;
      }
    }
    showTip(ev);
  });
  body.addEventListener('pointerup', ev => {
    const wasDrag = drag && drag.moved;
    drag = null;
    if (live(ev) && !wasDrag) pick(ev.target);
  });
  body.addEventListener('pointerleave', () => { drag = null; tip.hidden = true; });
  body.addEventListener('keydown', ev => {
    if (!live(ev)) return;
    if (ev.key === '+' || ev.key === '=') zoom(0.5, 0.5);
    else if (ev.key === '-') zoom(2, 0.5);
    else if (ev.key === 'ArrowLeft' || ev.key === 'ArrowRight') {
      view = panRange(view, (ev.key === 'ArrowLeft' ? -1 : 1) * (view[1] - view[0]) * 0.25, fetched.range);
      paintChart();
    } else return;
    ev.preventDefault();
    ev.stopPropagation();
  });

  const itemOf = node => {
    const i = node && node.getAttribute && node.getAttribute('data-i');
    return i == null ? null : drawn[Number(i)];
  };

  function describe(d) {
    const it = d.item;
    if (d.kind === 'mark') return `${it.unit} · ${MARK_WORD[it.kind]} · ${clock(it.t, true)}`;
    if (d.kind === 'span') return `${it.unit} · ${it.kind} · ${clock(it.start, true)} for ${duration(it.end - it.start)}`;
    const word = OUTCOME_WORD[it.outcome] || it.outcome;
    const count = it.count ? ` · ${it.count} polls` : '';
    return `${it.unit} · ${it.template || 'task'} · ${word} · ${clock(it.start, true)}${it.outcome === 'running' ? '' : ` for ${duration(it.end - it.start)}`}${count}`;
  }

  function showTip(ev) {
    const d = itemOf(ev.target);
    if (!d) { tip.hidden = true; return; }
    tip.textContent = describe(d);
    tip.hidden = false;
    const r = root.getBoundingClientRect ? root.getBoundingClientRect() : { left: 0, top: 0 };
    tip.style.left = `${ev.clientX - r.left + 12}px`;
    tip.style.top = `${ev.clientY - r.top + 14}px`;
  }

  function pick(node) {
    const d = itemOf(node);
    if (d) openEvents(describe(d), d.item.events || [], d.item);
  }

  // The events behind a bar, in time order. There is no separate explorer
  // screen, so this list is where a bar's events open.
  function openEvents(title, events, item) {
    selected = item;
    const rows = [...events].sort((p, q) => Date.parse(p.at) - Date.parse(q.at)).map(e => el('li', { className: `tl-ev tl-ev-${e.severity}` },
      el('time', { textContent: clock(Date.parse(e.at), true) }), el('b', { textContent: e.kind.replace(/_/g, ' ') }), el('span', { textContent: eventText(e) })));
    const close = el('button', { type: 'button', textContent: 'Close', className: 'tl-close' });
    close.addEventListener('click', closeEvents);
    const kids = [el('h3', { textContent: title }), close, rows.length ? el('ul', {}, ...rows) : el('p', { textContent: 'No events in this window.' })];
    const jobId = item && item.job;
    if (jobId) {
      const open = el('button', { type: 'button', textContent: 'Open this job' });
      open.addEventListener('click', () => { host.state().job = jobId; mode = 'job'; host.sync(); paint(); loadJob(); });
      kids.push(open);
    }
    side.replaceChildren(...kids);
    side.hidden = false;
  }

  function closeEvents() {
    const had = !side.hidden;
    selected = null;
    side.hidden = true;
    return had;
  }

  // ---- the job view ----

  function setMode(m) {
    if (m === mode) return;
    mode = m;
    closeEvents();
    if (m === 'job') loadJob();
    else if (!fetched) load();
    paint();
  }

  const sortedJobs = () => [...host.jobs()].sort((p, q) => Date.parse(q.created) - Date.parse(p.created));

  function fillJobSelect() {
    const want = host.state().job;
    const list = sortedJobs();
    const ids = list.map(j => j.id);
    if (want && !ids.includes(want) && jobLoad && jobLoad.job && jobLoad.id === want) list.push(jobLoad.job);
    const opts = list.map(j => el('option', { value: j.id, textContent: `${(j.spec && j.spec.template) || j.kind} · ${j.state} · ${clock(Date.parse(j.created), true)}` }));
    if (!opts.length) opts.push(el('option', { value: '', textContent: 'no jobs' }));
    jobSelect.replaceChildren(...opts);
    jobSelect.value = want || (list[0] ? list[0].id : '');
  }

  async function loadJob() {
    if (!host.operator()) return paint();
    fillJobSelect();
    let id = host.state().job || jobSelect.value;
    if (!id) { jobLoad = null; return paint(); }
    host.state().job = id;
    const my = ++loading;
    jobLoad = { id, state: 'loading' };
    paint();
    let job = host.jobs().find(j => j.id === id);
    // The job may be older than the stream's newest 20, and the stream's
    // copy can be stale, so the row set is always read by id.
    const r = await host.api('GET', `/v1/jobs/${encodeURIComponent(id)}`);
    if (my !== loading || !mounted) return;
    if (r.status === 401) { host.setOperator?.(null); return; }
    if (r.ok && r.data && r.data.id === id) job = r.data;
    else if (!job) { jobLoad = { id, state: r.status === 404 ? 'missing' : 'error' }; return paint(); }
    const units = [...new Set((job.steps || []).map(s => s.unit).filter(Boolean))];
    const open = job.state === 'running';
    const from = Date.parse(job.created) - 60000;
    const to = Math.min(Date.now(), (open ? Date.now() : Date.parse(job.updated)) + 60000);
    const res = await fetchWindow(host.api, units, [from, to]);
    if (my !== loading || !mounted) return;
    if (res.failure && res.failure.status === 401) { host.setOperator?.(null); return; }
    // Only this job's events: a unit may have done other work meanwhile.
    const work = new Set((job.steps || []).map(s => s.work_id).filter(Boolean));
    const mine = res.events.filter(e => (e.task && work.has(e.task)) || e.job === id);
    jobLoad = { id, state: 'ready', job, rows: jobRows(job, mine), range: [from, to], truncated: res.truncated, failure: res.failure, count: mine.length };
    fillJobSelect();
    paint();
  }

  function paintJob() {
    fillJobSelect();
    side.hidden = side.hidden || !selected;
    const j = jobLoad;
    legend.replaceChildren();
    if (!j || j.state !== 'ready') {
      note.textContent = '';
      const text = !j ? 'No job selected.' : j.state === 'loading' ? 'loading…' : j.state === 'missing' ? 'Job not found.' : 'Could not load that job.';
      body.replaceChildren(el('p', { className: 'tl-empty', textContent: text }));
      return;
    }
    note.textContent = [j.failure ? `could not load some units (${j.failure.error})` : '', j.truncated ? 'busy units show only their newest events' : '', `${j.count} events`].filter(Boolean).join(' · ');
    const timed = j.rows.filter(r => r.start != null);
    const t0 = timed.length ? Math.min(...timed.map(r => r.start)) : 0;
    const t1 = timed.length ? Math.max(...timed.map(r => r.end ?? Math.min(Date.now(), j.range[1]))) : 1;
    const total = Math.max(1, t1 - t0);
    const head = el('tr', {}, ...['#', 'Step', 'Unit', 'Task', 'Outcome', 'Started', 'Took', 'Timeline'].map(h => el('th', { textContent: h, scope: 'col' })));
    const rows = j.rows.map((r, i) => {
      const outcome = r.outcome || r.state;
      const word = r.outcome ? OUTCOME_WORD[r.outcome] || r.outcome : r.state;
      const end = r.start == null ? null : (r.end ?? Math.min(Date.now(), j.range[1]));
      const tr = el('tr', { className: `tl-job-row tl-o-${outcome}`, tabIndex: 0 });
      tr.setAttribute('aria-label', `${r.key}, ${word}`);
      const bar = svg('svg', { class: 'tl-job-bar', width: 240, height: 14, viewBox: '0 0 240 14', 'aria-hidden': 'true' });
      if (r.start != null) {
        const x0 = ((r.start - t0) / total) * 236;
        const w = Math.max(2, ((end - r.start) / total) * 236);
        const color = colorFor(r.template, seen);
        const dashed = outcome === 'cancelled' || outcome === 'ended';
        bar.append(svg('rect', { x: x0, y: 2, width: w, height: 10, rx: 2, fill: dashed ? 'none' : color, stroke: outcome === 'failed' ? '#e66767' : color, 'stroke-width': dashed || outcome === 'failed' ? 1.6 : 0, ...(dashed ? { 'stroke-dasharray': '3 2' } : {}) }));
        if (outcome === 'failed') bar.append(svg('path', { d: MARK_PATH.failed, transform: `translate(${Math.min(232, x0 + w)} 7)`, fill: 'none', stroke: '#f0efec', 'stroke-width': 2 }));
      }
      const cells = [String(i + 1), r.key, r.unit, r.template || '', r.reason ? `${word}: ${r.reason}` : word, r.start == null ? '' : clock(r.start, false), r.start == null ? '' : duration(end - r.start)];
      tr.append(...cells.map(c => el('td', { textContent: c })), el('td', {}, bar));
      const open = () => openEvents(`${r.key} on ${r.unit || 'no unit'}`, r.events, { job: '' });
      tr.addEventListener('click', open);
      tr.addEventListener('keydown', ev => { if (ev.key === 'Enter' || ev.key === ' ') { ev.preventDefault(); open(); } });
      return tr;
    });
    const used = [...new Set(j.rows.map(r => r.template).filter(Boolean))];
    legendFor(used, ['failed']);
    const table = el('table', { className: 'tl-job' }, el('thead', {}, head), el('tbody', {}, ...rows));
    const summary = el('p', { className: 'tl-job-sum', textContent: `${(j.job.spec && j.job.spec.template) || j.job.kind} · ${j.job.state}${j.job.reason ? ` (${j.job.reason})` : ''} · ${j.rows.length} steps${timed.length ? ` · ${duration(total)} start to finish` : ''}` });
    body.replaceChildren(summary, table);
  }

  return {
    mount(stage) {
      if (mounted) return;
      mounted = true;
      stage.replaceChildren(root);
      mode = host.state().job ? 'job' : 'units';
      if (mode === 'job') loadJob();
      else { paint(); load(); }
    },
    unmount() {
      mounted = false;
      loading++;
      fetched = jobLoad = selected = null;
      drawn = [];
      if (root.parentNode) root.parentNode.removeChild(root);
    },
    // A sign-in or sign-out mid-view flips between the empty state and data.
    onOperator() {
      if (!mounted) return;
      fetched = null;
      jobLoad = null;
      if (!host.operator()) { loading++; return paint(); }
      if (mode === 'job') loadJob(); else { paint(); load(); }
    },
    // The job list changed: keep the picker in step without refetching rows.
    onJobs() {
      if (mounted && mode === 'job' && host.operator()) fillJobSelect();
    },
    key(ev) {
      if (!mounted || ev.key !== 'Escape') return false;
      if (closeEvents()) { ev.preventDefault(); return true; }
      return false;
    },
  };
}
