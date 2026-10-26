// The insight strip on the fleet overview (capacity tiles and the live
// insight rows) and the Why panel on the unit detail page. Both read public
// routes that carry no coordinates, so they work signed out; evidence ids
// appear only when the server sends them to a signed-in session.
import { capacityTiles, templateRows, insightRows } from './insights.js';
import { renderWhy } from './why.js';

const POLL_MS = 15_000;
const WINDOW = '1h';

function el(tag, props = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k.includes('-') || k === 'role') e.setAttribute(k, v);
    else e[k] = v;
  }
  e.append(...kids);
  return e;
}

async function getJSON(path) {
  try {
    const res = await fetch(path, { cache: 'no-store' });
    return res.ok ? await res.json() : null;
  } catch {
    return null;
  }
}

const clock = iso => {
  const t = Date.parse(iso);
  return Number.isNaN(t) ? '' : new Date(t).toLocaleTimeString(undefined, { hour12: false });
};

async function explain(unit) {
  return getJSON(`/v1/units/${encodeURIComponent(unit)}/explain`);
}

function poller(fn) {
  let timer = 0;
  const tick = () => { if (document.visibilityState === 'visible') fn(); };
  return {
    start() { this.stop(); fn(); timer = setInterval(tick, POLL_MS); },
    stop() { clearInterval(timer); timer = 0; },
  };
}

export function createInsightStrip() {
  const root = el('section', { className: 'ins', role: 'region', 'aria-label': 'Insights and capacity' });
  const tiles = el('div', { className: 'ins-tiles' });
  const table = el('details', { className: 'ins-table' }, el('summary', { textContent: 'Capacity by task type' }));
  const rows = el('ul', { className: 'ins-rows' });
  root.append(tiles, table, rows);
  const open = new Map(); // insight id -> the Why element once asked for
  let seq = 0;

  function paintCapacity(report) {
    tiles.replaceChildren(...capacityTiles(report).map(t => el('div', { className: 'ins-tile', title: t.note },
      el('span', { className: 'ins-tile-label', textContent: t.label }),
      el('span', { className: 'ins-tile-value', textContent: t.value }),
      el('span', { className: 'ins-tile-note', textContent: t.note }))));
    const head = el('tr', {}, ...['Task type', 'Per min', 'Mean s', 'Busy units', 'Failed s'].map(h => el('th', { textContent: h })));
    const body = templateRows(report).map(r => el('tr', { className: r.unsteady ? 'unsteady' : '' },
      el('td', { textContent: r.template }), el('td', { textContent: r.lambda }), el('td', { textContent: r.mean }),
      el('td', { textContent: r.l }), el('td', { textContent: String(r.failedTime) })));
    const t = el('table', {}, el('thead', {}, head), el('tbody', {}, ...body));
    table.replaceChildren(table.firstElementChild || el('summary', { textContent: 'Capacity by task type' }), t);
  }

  function paintInsights(list) {
    const items = insightRows(list);
    if (!items.length) {
      rows.replaceChildren(el('li', { className: 'ins-none', textContent: 'No insights: nothing the rules flag right now.' }));
      return;
    }
    rows.replaceChildren(...items.map(i => {
      const why = el('div', { className: 'ins-why', hidden: !open.has(i.id) });
      if (open.has(i.id)) why.replaceChildren(open.get(i.id));
      const btn = el('button', { type: 'button', className: 'ins-ask', textContent: 'Why', hidden: !i.unit });
      btn.addEventListener('click', async () => {
        if (!why.hidden) { why.hidden = true; open.delete(i.id); return; }
        why.hidden = false;
        why.textContent = 'Reading…';
        const r = await explain(i.unit);
        const node = r ? renderWhy(r) : el('p', { textContent: 'The explanation could not be read.' });
        open.set(i.id, node);
        why.replaceChildren(node);
      });
      return el('li', { className: `ins-row ins-${i.severity} ins-${i.state}` },
        el('span', { className: 'ins-badge', textContent: i.rule, title: i.ruleName }),
        el('strong', { textContent: i.who }),
        el('span', { className: 'ins-detail', textContent: ` ${i.detail}` }),
        el('span', { className: 'ins-since', textContent: ` ${i.state === 'firing' ? 'since' : 'resolved, first seen'} ${clock(i.since)}` }),
        el('span', { className: 'ins-badge ins-rule', textContent: 'Rule-derived' }), btn, why);
    }));
  }

  const p = poller(async () => {
    const my = ++seq;
    const [cap, ins] = await Promise.all([getJSON(`/v1/capacity?window=${WINDOW}`), getJSON('/v1/insights')]);
    if (my !== seq) return;
    if (cap) paintCapacity(cap);
    if (ins) paintInsights(ins.insights);
  });

  return { el: root, start: () => p.start(), stop: () => { seq++; p.stop(); } };
}

export function createWhyPanel() {
  const body = el('div', { className: 'why-body' });
  const root = el('details', { className: 'dt-why', open: true },
    el('summary', {}, 'Why did this unit fail? ', el('span', { className: 'ins-badge ins-rule', textContent: 'Rule-derived' })), body);
  let unit = '';
  let seq = 0;
  const p = poller(async () => {
    const my = ++seq;
    const r = await explain(unit);
    if (my !== seq) return;
    body.replaceChildren(r ? renderWhy(r) : el('p', { textContent: 'The explanation could not be read.' }));
  });
  return {
    el: root,
    start(id) { unit = id; body.textContent = 'Reading…'; p.start(); },
    stop() { seq++; p.stop(); root.remove(); },
  };
}
