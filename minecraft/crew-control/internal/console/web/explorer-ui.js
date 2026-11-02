import { h } from './kit/dom.js';
// The event explorer screen. explorer.js holds the logic; this file fetches
// and paints. Signed in, the history comes from the server and a live tail
// streams new rows; signed out, the same filter runs over the public feed the
// page already holds, which carries no coordinates.
import { openStream } from './kit/stream.js';
import { createTable } from './kit/table.js';
import {
  KINDS, SEVERITIES, RANGES, PAGE, ROW_CAP, severityOf, readFilter, formatFilter, toServerQuery, emptyFilter, isDefault, chips, dropChip,
  toggleIn, fromPublic, filterAndSort, mergeRows, nextSort, tailsToTop, detailText, matchRow, sortRows,
} from './explorer.js';


const clock = iso => {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? '' : d.toLocaleTimeString([], { hour12: false }) + ' ' + d.toLocaleDateString([], { month: 'short', day: 'numeric' });
};
// A datetime-local box reads local time without a zone.
const localInput = iso => {
  if (!iso) return '';
  const d = new Date(iso);
  return new Date(d.getTime() - d.getTimezoneOffset() * 6e4).toISOString().slice(0, 16);
};

// host: { statuses, order(), state(), operator(), api(method, path, body), sync() }
export function initExplorer(host) {
  const root = h('section', { className: 'ex' });
  root.setAttribute('aria-label', 'Event explorer');
  let mounted = false;
  let filter = emptyFilter();
  let rows = [];
  let total = 0;
  let next = '';
  let head = 0;
  let truncated = false;
  let loading = false;
  let error = '';
  let token = 0;
  let paused = false;
  let pending = 0;
  let tail = null;
  let views = [];
  let open = new Set();
  // The public feed, newest first, kept while the screen is away so the
  // first signed-out view is not empty.
  let publicRows = [];

  const signedIn = () => !!host.operator();

  // ---- data ----

  async function load(more = false) {
    const my = ++token;
    if (!signedIn()) {
      const all = filterAndSort(publicRows, filter);
      total = all.length;
      rows = all.slice(0, ROW_CAP);
      next = '';
      error = '';
      loading = false;
      paint();
      return;
    }
    loading = true;
    error = '';
    paint();
    const q = toServerQuery(filter, { limit: PAGE, cursor: more ? next : '' });
    const r = await host.api('GET', `/v1/explorer/events?${q}`);
    if (my !== token || !mounted) return;
    loading = false;
    if (!r.ok) {
      error = r.status === 401 ? 'Sign in again to read history.' : r.status === 503 ? 'The event store is not ready.' : (r.data && r.data.error) || 'Could not load events.';
      paint();
      return;
    }
    const d = r.data;
    // Appending pages keeps their order: the server already sorted them.
    rows = more ? [...rows, ...d.events.filter(e => !rows.some(r => r.id === e.id))] : d.events;
    total = d.total;
    next = d.next || '';
    head = d.head || 0;
    truncated = !!d.truncated;
    pending = 0;
    openTail();
    paint();
  }

  // ---- live tail ----

  function closeTail() {
    if (tail) tail.close();
    tail = null;
  }

  function openTail() {
    closeTail();
    if (!mounted || !signedIn() || typeof EventSource === 'undefined') return;
    const my = token;
    // Rebuilt from the newest id seen on any error, so the browser's own retry
    // (which would reuse the stale address) never replays or skips rows.
    tail = openStream(() => `/v1/explorer/tail?${toServerQuery(filter)}&after=${head}`, {
      events(ev) {
        if (my !== token) return;
        let d;
        try { d = JSON.parse(ev.data); } catch { return; }
        head = Math.max(head, d.head || 0);
        incoming(d.events || []);
      },
    }, { min: 2000, max: 2000, always: true });
  }

  function incoming(batch) {
    if (!batch.length) return;
    if (paused || !tailsToTop(filter)) {
      pending += batch.length;
      total += batch.length;
      paintBar();
      return;
    }
    const m = mergeRows(rows, [...batch].sort((a, b) => b.id - a.id));
    rows = m.rows;
    total += m.added.length;
    paint();
  }

  async function resume() {
    paused = false;
    await load();
  }

  // ---- state changes ----

  function change(next_) {
    filter = next_;
    open = new Set();
    host.sync();
    load();
  }

  async function loadViews() {
    if (!signedIn()) { views = []; return; }
    const r = await host.api('GET', '/v1/explorer/views');
    views = r.ok ? r.data.views : [];
    if (mounted) paintViews();
  }

  async function saveView(name) {
    const r = await host.api('POST', '/v1/explorer/views', { name, query: toServerQuery(filter) });
    if (!r.ok) { note((r.data && r.data.error) || 'Could not save the view.'); return; }
    note(`Saved "${r.data.name}".`);
    await loadViews();
  }

  async function removeView(id) {
    const r = await host.api('POST', '/v1/explorer/views/delete', { id });
    if (!r.ok) { note((r.data && r.data.error) || 'Could not delete the view.'); return; }
    await loadViews();
  }

  // ---- painting ----

  const parts = {};

  function note(text) {
    parts.note.textContent = text;
  }

  function build() {
    parts.search = h('input', { type: 'search', placeholder: 'Search text, unit, kind, task', 'aria-label': 'Search events' });
    parts.search.addEventListener('input', () => {
      clearTimeout(parts.debounce);
      parts.debounce = setTimeout(() => change({ ...filter, q: parts.search.value.trim() }), 250);
    });
    parts.range = h('select', { 'aria-label': 'Time range' }, ...RANGES.map(r => h('option', { value: r.id, textContent: r.id ? `Last ${r.label}` : 'All time' })), h('option', { value: 'custom', textContent: 'Custom' }));
    parts.range.addEventListener('change', () => {
      if (parts.range.value === 'custom') { parts.custom.hidden = false; return; }
      change({ ...filter, range: parts.range.value, since: '', until: '' });
    });
    parts.from = h('input', { type: 'datetime-local', 'aria-label': 'From' });
    parts.to = h('input', { type: 'datetime-local', 'aria-label': 'To' });
    const apply = () => change({ ...filter, range: '', since: parts.from.value ? new Date(parts.from.value).toISOString() : '', until: parts.to.value ? new Date(parts.to.value).toISOString() : '' });
    parts.from.addEventListener('change', apply);
    parts.to.addEventListener('change', apply);
    parts.custom = h('span', { className: 'ex-custom', hidden: true }, parts.from, ' to ', parts.to);
    parts.unit = h('select', { 'aria-label': 'Add unit filter' });
    parts.unit.addEventListener('change', () => { if (parts.unit.value) change({ ...filter, unit: toggleIn(filter.unit, parts.unit.value) }); });
    parts.kind = h('select', { 'aria-label': 'Add kind filter' }, h('option', { value: '', textContent: 'Kind...' }), ...KINDS.map(k => h('option', { value: k, textContent: k })));
    parts.kind.addEventListener('change', () => { if (parts.kind.value) change({ ...filter, kind: toggleIn(filter.kind, parts.kind.value) }); });
    parts.sev = h('span', { className: 'ex-sevs', role: 'group', 'aria-label': 'Severity' }, ...SEVERITIES.map(s => {
      const b = h('button', { type: 'button', className: 'ex-sev ' + s.id, title: s.label }, h('span', { className: 'ico', textContent: s.icon, 'aria-hidden': 'true' }), s.label);
      b.dataset.id = s.id;
      b.addEventListener('click', () => change({ ...filter, severity: toggleIn(filter.severity, s.id) }));
      return b;
    }));
    parts.chips = h('div', { className: 'ex-chips', 'aria-label': 'Active filters' });
    parts.live = h('button', { type: 'button', className: 'ex-live' });
    parts.live.addEventListener('click', () => (paused ? resume() : (paused = true, paintBar())));
    parts.count = h('span', { className: 'ex-count', role: 'status' });
    parts.link = h('button', { type: 'button', textContent: 'Copy link', title: 'Copy a link to this filter' });
    parts.link.addEventListener('click', async () => {
      try { await navigator.clipboard.writeText(location.href); note('Link copied.'); } catch { note('Copy the address bar to share this view.'); }
    });
    parts.reset = h('button', { type: 'button', textContent: 'Clear' });
    parts.reset.addEventListener('click', () => change(emptyFilter()));
    parts.views = h('select', { 'aria-label': 'Saved views' });
    parts.views.addEventListener('change', () => {
      const v = views.find(x => x.id === parts.views.value);
      if (v) change(readFilter(new URLSearchParams([...new URLSearchParams(v.query)].map(([k, x]) => ['f.' + k, x]))));
    });
    parts.name = h('input', { type: 'text', placeholder: 'View name', maxLength: 60, 'aria-label': 'View name' });
    parts.save = h('button', { type: 'button', textContent: 'Save view' });
    parts.save.addEventListener('click', () => { const n = parts.name.value.trim(); if (n) { saveView(n); parts.name.value = ''; } });
    parts.del = h('button', { type: 'button', textContent: 'Delete' });
    parts.del.addEventListener('click', () => { if (parts.views.value) removeView(parts.views.value); });
    parts.hint = h('span', { className: 'ex-hint' });
    parts.saved = h('span', { className: 'ex-saved' }, parts.views, parts.del, parts.name, parts.save, parts.hint);
    parts.note = h('span', { className: 'ex-note', role: 'status' });
    parts.grid = createTable({
      label: 'Events',
      className: 'ex-table',
      rowKey: r => r.id,
      onSort: key => change(nextSort(filter, key)),
      onActivate: r => { open.has(r.id) ? open.delete(r.id) : open.add(r.id); paint(); },
      rowClass: r => 'ex-row ' + r.severity,
      detail: r => (open.has(r.id) ? fullText(r) : null),
      detailClass: 'ex-full',
      messages: { loading: 'Loading...', empty: 'No events yet.', filtered: 'No events match these filters.' },
      noteClass: 'ex-empty',
      errorClass: 'err',
      columns: [
        { key: 'at', label: 'Time', sortable: true, className: 'ex-time', text: r => clock(r.at) },
        {
          key: 'severity', label: 'Level', sortable: true, className: 'ex-level', sig: r => r.severity,
          node: r => { const sev = severityOf(r.severity); return [h('span', { className: 'ico', textContent: sev.icon, 'aria-hidden': 'true' }), sev.label]; },
        },
        { key: 'unit', label: 'Unit', sortable: true, text: r => { const name = (host.statuses.get(r.unit) || {}).name; return name ? `${name} (${r.unit})` : r.unit; } },
        { key: 'kind', label: 'Kind', sortable: true, text: r => r.kind },
        { key: 'details', label: 'Details', className: 'ex-detail', text: detailText },
      ],
    });
    parts.table = parts.grid.el;
    parts.more = h('button', { type: 'button', className: 'ex-more', textContent: 'Load more' });
    parts.more.addEventListener('click', () => load(true));
    parts.empty = parts.grid.note;
    root.replaceChildren(
      h('div', { className: 'ex-bar' }, parts.search, parts.range, parts.custom, parts.unit, parts.kind, parts.sev),
      h('div', { className: 'ex-bar' }, parts.chips, parts.reset, parts.link),
      h('div', { className: 'ex-bar' }, parts.live, parts.count, parts.note, parts.saved),
      h('div', { className: 'ex-scroll' }, parts.table, parts.empty, parts.more),
    );
  }

  function paintBar() {
    const ids = host.order();
    const have = parts.unit.options.length - 1;
    if (have !== ids.length) {
      parts.unit.replaceChildren(h('option', { value: '', textContent: 'Unit...' }), ...ids.map(id => h('option', { value: id, textContent: (host.statuses.get(id) || {}).name || id })));
    }
    parts.unit.value = '';
    parts.kind.value = '';
    if (document.activeElement !== parts.search) parts.search.value = filter.q;
    parts.range.value = filter.since || filter.until ? 'custom' : filter.range;
    parts.custom.hidden = !(filter.since || filter.until);
    parts.from.value = localInput(filter.since);
    parts.to.value = localInput(filter.until);
    for (const b of parts.sev.children) b.setAttribute('aria-pressed', String(filter.severity.includes(b.dataset.id)));
    parts.chips.replaceChildren(...chips(filter).map(c => {
      const b = h('button', { type: 'button', className: 'ex-chip', textContent: c.label + ' ×', title: 'Remove filter' });
      b.addEventListener('click', () => change(dropChip(filter, c)));
      return b;
    }));
    parts.reset.hidden = isDefault(filter);
    const live = signedIn();
    parts.live.hidden = false;
    parts.live.setAttribute('aria-pressed', String(!paused));
    parts.live.textContent = paused ? (pending ? `Resume (${pending} new)` : 'Resume') : 'Live';
    parts.live.title = paused ? 'Resume the live tail' : 'Pause the live tail';
    const more = tailsToTop(filter) ? '' : ' Live rows are counted, not inserted, while sorted by this column.';
    parts.count.textContent = `${total} event${total === 1 ? '' : 's'}${truncated ? ' (older history left out)' : ''}${paused || !tailsToTop(filter) ? (pending ? `, ${pending} new` : '') + more : ''}`;
    parts.saved.hidden = false;
    parts.views.hidden = parts.del.hidden = parts.name.hidden = parts.save.hidden = !live;
    parts.hint.textContent = live ? '' : 'Sign in to save views.';
  }

  function paintViews() {
    parts.views.replaceChildren(h('option', { value: '', textContent: views.length ? 'Saved views...' : 'No saved views' }), ...views.map(v => h('option', { value: v.id, textContent: v.name })));
  }

  // The pos is the operator's: the public feed never carries it.
  const fullText = r => JSON.stringify({ id: r.id, task: r.task, job: r.job, ...(signedIn() && r.pos ? { pos: r.pos } : {}), payload: r.payload }, null, 2);

  function paint() {
    if (!mounted) return;
    paintBar();
    parts.grid.update({ rows, sort: filter.sort, dir: filter.dir, loading, error, filtered: !isDefault(filter) });
    parts.more.hidden = !next;
    parts.more.disabled = loading;
  }

  return {
    mount(into) {
      into.append(root);
      mounted = true;
      document.body.classList.add('view-events');
      if (!parts.table) build();
      filter = readFilter(location.search);
      paused = false;
      pending = 0;
      paintViews();
      load();
      loadViews();
    },
    unmount() {
      if (!mounted) return;
      mounted = false;
      token++;
      closeTail();
      clearTimeout(parts.debounce);
      root.remove();
      document.body.classList.remove('view-events');
    },
    // A stream backlog replaces the public feed; a live event is added to it.
    onEvents(list, live) {
      const mapped = list.map(fromPublic).sort((a, b) => b.id - a.id);
      const m = mergeRows(live ? publicRows : [], mapped);
      publicRows = m.rows;
      if (!mounted || signedIn()) return;
      if (!live) { load(); return; }
      const fresh = m.added.filter(r => matchRow(r, filter));
      if (!fresh.length) return;
      if (paused || !tailsToTop(filter)) { pending += fresh.length; total += fresh.length; paintBar(); return; }
      rows = sortRows([...fresh, ...rows], filter).slice(0, ROW_CAP);
      total += fresh.length;
      paint();
    },
    onOperator() {
      if (!mounted) return;
      paused = false;
      load();
      loadViews();
    },
    // The address-bar suffix, leading-&.
    query() {
      return host.state().view === 'events' ? formatFilter(filter) : '';
    },
  };
}
