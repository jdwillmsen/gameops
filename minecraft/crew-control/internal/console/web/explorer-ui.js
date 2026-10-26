// The event explorer screen. explorer.js holds the logic; this file fetches
// and paints. Signed in, the history comes from the server and a live tail
// streams new rows; signed out, the same filter runs over the public feed the
// page already holds, which carries no coordinates.
import {
  KINDS, SEVERITIES, RANGES, PAGE, ROW_CAP, severityOf, readFilter, formatFilter, toServerQuery, emptyFilter, isDefault, chips, dropChip,
  toggleIn, fromPublic, filterAndSort, mergeRows, nextSort, tailsToTop, detailText, matchRow, sortRows,
} from '/static/explorer.js';

function el(tag, props = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k.includes('-') || k === 'role') e.setAttribute(k, v);
    else e[k] = v;
  }
  e.append(...kids);
  return e;
}

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
  const root = el('section', { className: 'ex' });
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
  let tailTimer = 0;
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
    clearTimeout(tailTimer);
    if (tail) tail.close();
    tail = null;
  }

  function openTail() {
    closeTail();
    if (!mounted || !signedIn() || typeof EventSource === 'undefined') return;
    const my = token;
    const es = tail = new EventSource(`/v1/explorer/tail?${toServerQuery(filter)}&after=${head}`);
    es.addEventListener('events', ev => {
      if (my !== token) return;
      let d;
      try { d = JSON.parse(ev.data); } catch { return; }
      head = Math.max(head, d.head || 0);
      incoming(d.events || []);
    });
    // Reopened from the newest id seen, so the browser's own retry (which
    // would reuse the stale address) never replays or skips rows.
    es.onerror = () => {
      if (tail !== es) return;
      closeTail();
      tailTimer = setTimeout(openTail, 2000);
    };
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
    parts.search = el('input', { type: 'search', placeholder: 'Search text, unit, kind, task', 'aria-label': 'Search events' });
    parts.search.addEventListener('input', () => {
      clearTimeout(parts.debounce);
      parts.debounce = setTimeout(() => change({ ...filter, q: parts.search.value.trim() }), 250);
    });
    parts.range = el('select', { 'aria-label': 'Time range' }, ...RANGES.map(r => el('option', { value: r.id, textContent: r.id ? `Last ${r.label}` : 'All time' })), el('option', { value: 'custom', textContent: 'Custom' }));
    parts.range.addEventListener('change', () => {
      if (parts.range.value === 'custom') { parts.custom.hidden = false; return; }
      change({ ...filter, range: parts.range.value, since: '', until: '' });
    });
    parts.from = el('input', { type: 'datetime-local', 'aria-label': 'From' });
    parts.to = el('input', { type: 'datetime-local', 'aria-label': 'To' });
    const apply = () => change({ ...filter, range: '', since: parts.from.value ? new Date(parts.from.value).toISOString() : '', until: parts.to.value ? new Date(parts.to.value).toISOString() : '' });
    parts.from.addEventListener('change', apply);
    parts.to.addEventListener('change', apply);
    parts.custom = el('span', { className: 'ex-custom', hidden: true }, parts.from, ' to ', parts.to);
    parts.unit = el('select', { 'aria-label': 'Add unit filter' });
    parts.unit.addEventListener('change', () => { if (parts.unit.value) change({ ...filter, unit: toggleIn(filter.unit, parts.unit.value) }); });
    parts.kind = el('select', { 'aria-label': 'Add kind filter' }, el('option', { value: '', textContent: 'Kind...' }), ...KINDS.map(k => el('option', { value: k, textContent: k })));
    parts.kind.addEventListener('change', () => { if (parts.kind.value) change({ ...filter, kind: toggleIn(filter.kind, parts.kind.value) }); });
    parts.sev = el('span', { className: 'ex-sevs', role: 'group', 'aria-label': 'Severity' }, ...SEVERITIES.map(s => {
      const b = el('button', { type: 'button', className: 'ex-sev ' + s.id, title: s.label }, el('span', { className: 'ico', textContent: s.icon, 'aria-hidden': 'true' }), s.label);
      b.dataset.id = s.id;
      b.addEventListener('click', () => change({ ...filter, severity: toggleIn(filter.severity, s.id) }));
      return b;
    }));
    parts.chips = el('div', { className: 'ex-chips', 'aria-label': 'Active filters' });
    parts.live = el('button', { type: 'button', className: 'ex-live' });
    parts.live.addEventListener('click', () => (paused ? resume() : (paused = true, paintBar())));
    parts.count = el('span', { className: 'ex-count', role: 'status' });
    parts.link = el('button', { type: 'button', textContent: 'Copy link', title: 'Copy a link to this filter' });
    parts.link.addEventListener('click', async () => {
      try { await navigator.clipboard.writeText(location.href); note('Link copied.'); } catch { note('Copy the address bar to share this view.'); }
    });
    parts.reset = el('button', { type: 'button', textContent: 'Clear' });
    parts.reset.addEventListener('click', () => change(emptyFilter()));
    parts.views = el('select', { 'aria-label': 'Saved views' });
    parts.views.addEventListener('change', () => {
      const v = views.find(x => x.id === parts.views.value);
      if (v) change(readFilter(new URLSearchParams([...new URLSearchParams(v.query)].map(([k, x]) => ['f.' + k, x]))));
    });
    parts.name = el('input', { type: 'text', placeholder: 'View name', maxLength: 60, 'aria-label': 'View name' });
    parts.save = el('button', { type: 'button', textContent: 'Save view' });
    parts.save.addEventListener('click', () => { const n = parts.name.value.trim(); if (n) { saveView(n); parts.name.value = ''; } });
    parts.del = el('button', { type: 'button', textContent: 'Delete' });
    parts.del.addEventListener('click', () => { if (parts.views.value) removeView(parts.views.value); });
    parts.hint = el('span', { className: 'ex-hint' });
    parts.saved = el('span', { className: 'ex-saved' }, parts.views, parts.del, parts.name, parts.save, parts.hint);
    parts.note = el('span', { className: 'ex-note', role: 'status' });
    parts.table = el('table', { className: 'ex-table' });
    parts.more = el('button', { type: 'button', className: 'ex-more', textContent: 'Load more' });
    parts.more.addEventListener('click', () => load(true));
    parts.empty = el('p', { className: 'ex-empty' });
    root.replaceChildren(
      el('div', { className: 'ex-bar' }, parts.search, parts.range, parts.custom, parts.unit, parts.kind, parts.sev),
      el('div', { className: 'ex-bar' }, parts.chips, parts.reset, parts.link),
      el('div', { className: 'ex-bar' }, parts.live, parts.count, parts.note, parts.saved),
      el('div', { className: 'ex-scroll' }, parts.table, parts.empty, parts.more),
    );
  }

  function paintBar() {
    const ids = host.order();
    const have = parts.unit.options.length - 1;
    if (have !== ids.length) {
      parts.unit.replaceChildren(el('option', { value: '', textContent: 'Unit...' }), ...ids.map(id => el('option', { value: id, textContent: (host.statuses.get(id) || {}).name || id })));
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
      const b = el('button', { type: 'button', className: 'ex-chip', textContent: c.label + ' ×', title: 'Remove filter' });
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
    parts.views.replaceChildren(el('option', { value: '', textContent: views.length ? 'Saved views...' : 'No saved views' }), ...views.map(v => el('option', { value: v.id, textContent: v.name })));
  }

  const HEADS = [['at', 'Time'], ['severity', 'Level'], ['unit', 'Unit'], ['kind', 'Kind']];

  function paint() {
    if (!mounted) return;
    paintBar();
    const op = signedIn();
    const head_ = el('tr', {}, ...HEADS.map(([key, label]) => {
      const th = el('th', { scope: 'col' });
      const b = el('button', { type: 'button', textContent: label + (filter.sort === key ? (filter.dir === 'asc' ? ' ▲' : ' ▼') : '') });
      th.setAttribute('aria-sort', filter.sort === key ? (filter.dir === 'asc' ? 'ascending' : 'descending') : 'none');
      b.addEventListener('click', () => change(nextSort(filter, key)));
      th.append(b);
      return th;
    }), el('th', { scope: 'col', textContent: 'Details' }));
    const body = el('tbody');
    for (const r of rows) {
      const sev = severityOf(r.severity);
      const tr = el('tr', { className: 'ex-row ' + r.severity, tabIndex: 0 });
      tr.setAttribute('aria-expanded', String(open.has(r.id)));
      const toggle = () => { open.has(r.id) ? open.delete(r.id) : open.add(r.id); paint(); };
      tr.addEventListener('click', toggle);
      tr.addEventListener('keydown', ev => { if (ev.key === 'Enter' || ev.key === ' ') { ev.preventDefault(); toggle(); } });
      const name = (host.statuses.get(r.unit) || {}).name;
      tr.append(
        el('td', { className: 'ex-time', textContent: clock(r.at) }),
        el('td', { className: 'ex-level' }, el('span', { className: 'ico', textContent: sev.icon, 'aria-hidden': 'true' }), sev.label),
        el('td', { textContent: name ? `${name} (${r.unit})` : r.unit }),
        el('td', { textContent: r.kind }),
        el('td', { className: 'ex-detail', textContent: detailText(r) }),
      );
      body.append(tr);
      if (open.has(r.id)) {
        const full = { id: r.id, task: r.task, job: r.job, ...(op && r.pos ? { pos: r.pos } : {}), payload: r.payload };
        body.append(el('tr', { className: 'ex-full' }, el('td', { colSpan: 5 }, el('pre', { textContent: JSON.stringify(full, null, 2) }))));
      }
    }
    parts.table.replaceChildren(el('thead', {}, head_), body);
    parts.empty.textContent = loading ? 'Loading...' : error || (rows.length ? '' : isDefault(filter) ? 'No events yet.' : 'No events match these filters.');
    parts.empty.className = 'ex-empty' + (error ? ' err' : '');
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
