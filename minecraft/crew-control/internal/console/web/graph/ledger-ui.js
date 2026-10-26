// The world ledger view: places, chests and resources as a node graph, with
// the ledger's last-known contents in a details panel. graph.js draws and
// interacts; ledger.js maps data; this file owns fetching (the signed-out and
// failure states, a periodic refresh that patches in place) and the layout
// choice between the layered auto layout and the map.

import { createDagGraph } from './graph.js';
import { ledgerModel, mapPlace, legendChips, staleChip, EDGE_WORDS } from './ledger.js';

const REFRESH_MS = 15000;

function el(tag, props = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k.includes('-')) e.setAttribute(k, v);
    else e[k] = v;
  }
  e.append(...kids);
  return e;
}

const clock = d => d.toLocaleTimeString(undefined, { hour12: false });
const at = iso => {
  const t = Date.parse(iso);
  return Number.isNaN(t) ? '—' : new Date(t).toLocaleString(undefined, { hour12: false });
};

// host: { operator(), api(method, path), setOperator(op) }
export function initLedgerGraph(host, { now = Date.now, refreshMs = REFRESH_MS } = {}) {
  let mounted = false;
  let places = null; // null until the first answer
  let failure = ''; // why the last fetch failed; the last good data stays drawn
  let fetchedAt = null;
  let selected = '';
  let layoutMode = 'auto';
  let timer = null;
  let inflight = 0;
  let panel = null; // {key, sig, dl}

  const comp = createDagGraph({
    label: 'World ledger',
    critical: false,
    onSelect: id => openNode(id),
    onClose: () => closeDetails(),
  });
  comp.setLegend(legendChips());
  const edgeKey = el('p', { className: 'lv-edges', textContent:
    `Lines: solid, ${EDGE_WORDS.stored}; dashed, ${EDGE_WORDS.supplies}; dotted, ${EDGE_WORDS.used}. Hover a line for its meaning.` });

  const title = el('h2', { className: 'jv-title', textContent: 'World ledger' });
  const status = el('span', { className: 'jv-timing', role: 'status' });
  const auto = el('button', { type: 'button', textContent: 'Auto layout' });
  const map = el('button', { type: 'button', textContent: 'Map layout', title: 'Place nodes at world coordinates' });
  const refresh = el('button', { type: 'button', textContent: 'Refresh' });
  const modes = el('span', { className: 'lv-modes', role: 'group', 'aria-label': 'Layout' }, auto, map);
  const empty = el('div', { className: 'dg-empty', role: 'status' });
  const root = el('section', { className: 'dg-view cc-root' });
  root.setAttribute('data-theme', 'dark');
  root.setAttribute('aria-label', 'World ledger');
  root.append(el('div', { className: 'jv-main lv' },
    el('header', { className: 'jv-head' }, title, modes, refresh, status), edgeKey, comp.root, empty));

  function paintModes() {
    auto.setAttribute('aria-pressed', String(layoutMode === 'auto'));
    map.setAttribute('aria-pressed', String(layoutMode === 'map'));
  }

  function say(text) {
    empty.hidden = !text;
    empty.textContent = text;
  }

  function clearGraph() {
    comp.setData({ id: '', nodes: [], edges: [] });
    panel = null;
    comp.setContent(null);
  }

  function model() {
    const m = ledgerModel(places, now());
    for (const n of m.nodes) n.selected = n.id === selected;
    return {
      id: layoutMode,
      nodes: m.nodes,
      edges: m.edges,
      place: layoutMode === 'map' ? mapPlace(m, places) : undefined,
      facts: m.facts,
    };
  }

  function paintStatus() {
    status.textContent = failure
      ? `${failure}${fetchedAt ? ` — showing the ledger as of ${clock(fetchedAt)}` : ''}`
      : (fetchedAt ? `updated ${clock(fetchedAt)}` : '');
  }

  function update() {
    if (!mounted) return;
    paintModes();
    if (!host.operator()) {
      places = null;
      failure = '';
      fetchedAt = null;
      selected = '';
      clearGraph();
      paintStatus();
      say('Sign in to see the world ledger.');
      return;
    }
    if (!places) {
      clearGraph();
      paintStatus();
      say(failure || 'loading the ledger…');
      return;
    }
    paintStatus();
    if (!places.length) {
      clearGraph();
      say('The ledger has no places yet. Seed some with CREW_PLACES or add one from the Places tab (Shift-J).');
      return;
    }
    say('');
    const m = model();
    comp.setData(m);
    paintDetails(m.facts, false);
  }

  // ---- fetching ----

  async function load() {
    if (!mounted || !host.operator()) return;
    const mine = ++inflight;
    const r = await host.api('GET', '/v1/places');
    // A newer request, an unmount or a sign-out overtook this one.
    if (mine !== inflight || !mounted) return;
    if (r.status === 401) {
      host.setOperator?.(null);
      return;
    }
    if (r.ok && Array.isArray(r.data)) {
      places = r.data;
      failure = '';
      fetchedAt = new Date(now());
    } else {
      failure = r.status === 503 ? 'the ledger is unavailable (no database connected)'
        : `could not load the ledger (${r.status || 'no connection'})`;
    }
    update();
  }

  function schedule() {
    clearInterval(timer);
    timer = setInterval(() => {
      if (typeof document === 'undefined' || document.visibilityState !== 'hidden') load();
    }, refreshMs);
  }

  // ---- details ----

  const stockRows = rows => rows.map(s => [
    s.item || s.place,
    `${s.count}, seen ${at(s.seenAt || s.seen_at)}${s.unit ? ` by ${s.unit}` : ''} (${staleChip(s.seenAt || s.seen_at, now()).word})`,
  ]);

  function rowsOf(f) {
    if (f.kind === 'resource') {
      return [
        ['Resource', f.title],
        ['Total recorded', String(f.total)],
        ['Last observed', f.seenAt ? `${at(f.seenAt)} (${f.chip.word})` : 'never seen'],
        ['Supplied by', f.suppliers.length ? f.suppliers.join(', ') : '—'],
        ...(f.stock.length ? f.stock.map(s => [`In ${s.place}`,
          `${s.count}, seen ${at(s.seen_at)} (${staleChip(s.seen_at, now()).word})`]) : [['Stored in', 'nowhere recorded']]),
      ];
    }
    if (f.kind === 'unit') return [['Unit', f.title], ['Last read', f.places.join(', ') || '—']];
    const b = f.box;
    return [
      ['Place', f.title],
      ['Kind', `${f.kind}${f.seeded ? ', from configuration' : ''}`],
      ['Position', `${b.min.x} ${b.min.y} ${b.min.z} to ${b.max.x} ${b.max.y} ${b.max.z}`],
      ['Last observed', f.seenAt ? `${at(f.seenAt)} (${f.chip.word})` : 'never seen'],
      ['Meant for', f.items.length ? f.items.join(', ') : '—'],
      ...(f.stock.length ? stockRows(f.stock) : [['Stock', 'nothing recorded']]),
    ];
  }

  function paintDetails(facts, focus) {
    const f = selected && facts.get(selected);
    if (!f) {
      selected = '';
      if (panel) {
        panel = null;
        comp.setContent(null);
      }
      return;
    }
    const rows = rowsOf(f);
    const sig = JSON.stringify(rows);
    if (panel && panel.key === selected && panel.sig === sig) {
      if (focus) panel.dl.focus();
      return;
    }
    // A repaint replaces the panel; keep the focus it held.
    const held = panel && panel.dl === document.activeElement;
    const dl = el('dl', { tabIndex: '0', 'data-autofocus': '' });
    for (const [k, v] of rows) dl.append(el('dt', { textContent: k }), el('dd', { textContent: v }));
    const close = el('button', { type: 'button', textContent: 'x', title: 'Close details', 'aria-label': 'Close details' });
    close.addEventListener('click', () => closeDetails());
    panel = { key: selected, sig, dl };
    comp.setContent(el('div', { className: 'dg-details' },
      el('div', { className: 'dg-details-bar' }, el('h3', { textContent: f.title }), close), dl), { focus });
    if (held && !focus) dl.focus();
  }

  function repaint(focus) {
    if (!places || !places.length) return;
    const m = model();
    comp.setData(m);
    paintDetails(m.facts, focus);
  }

  function openNode(id) {
    selected = id;
    repaint(true);
  }

  function closeDetails() {
    if (!selected) return false;
    const back = selected;
    selected = '';
    panel = null;
    comp.setContent(null);
    repaint(false);
    comp.open(back);
    return true;
  }

  auto.addEventListener('click', () => { layoutMode = 'auto'; update(); });
  map.addEventListener('click', () => { layoutMode = 'map'; update(); });
  refresh.addEventListener('click', () => load());

  return {
    mount(stage) {
      if (mounted) return;
      mounted = true;
      stage.replaceChildren(root);
      comp.connect();
      update();
      load();
      schedule();
    },
    unmount() {
      mounted = false;
      inflight++;
      clearInterval(timer);
      timer = null;
      selected = '';
      places = null;
      failure = '';
      fetchedAt = null;
      clearGraph();
      comp.disconnect();
      if (root.parentNode) root.parentNode.removeChild(root);
    },
    onOperator() {
      if (!mounted) return;
      update();
      load();
    },
    key(ev) {
      if (ev.key !== 'Escape' || !mounted) return false;
      if (closeDetails()) {
        ev.preventDefault();
        return true;
      }
      return false;
    },
  };
}
