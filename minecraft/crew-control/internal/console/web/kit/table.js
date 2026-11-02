// Sortable tables. Callers sort and filter their rows with their own pure
// logic; the kit paints the headers (aria-sort, one button per sortable
// column), patches rows by key so a live feed keeps focus and scroll, and says
// why a table is empty.
import { h } from './dom.js';

const SORT_NAME = { asc: 'ascending', desc: 'descending' };

// A header cell: a button whose click asks for a sort on this column.
// opts: { key, label, onSort(key), tag?, role?, className?, arrowClass?, arrows?: [up, down] }
export function sortCell(opts) {
  const tag = opts.tag || 'th';
  const arrow = h('span', { class: opts.arrowClass || 'kit-arrow', 'aria-hidden': 'true' });
  const cell = h(tag, {
    class: opts.className, role: opts.role, scope: tag === 'th' ? 'col' : null, 'aria-sort': 'none',
  }, h('button', { type: 'button', on: { click: () => opts.onSort(opts.key) } }, h('span', { text: opts.label }), arrow));
  cell.arrow = arrow;
  cell.arrows = opts.arrows || ['▲', '▼'];
  return cell;
}

// Writes only what changed, since a live table calls this on every tick.
export function markSort(cell, active, dir) {
  const sort = active ? SORT_NAME[dir] : 'none';
  if (cell.getAttribute('aria-sort') !== sort) cell.setAttribute('aria-sort', sort);
  const glyph = active ? cell.arrows[dir === 'asc' ? 0 : 1] : '';
  if (cell.arrow.textContent !== glyph) cell.arrow.textContent = glyph;
}

// Puts the nodes into the given order under parent, moving only those out of
// place so an ordered list costs no DOM writes. Nodes that are hidden and not
// in the list stay where they are. A focused node keeps focus through a move.
export function orderChildren(parent, nodes) {
  const active = document.activeElement;
  const kids = Array.from(parent.children);
  const placed = new Set();
  let at = 0;
  for (const node of nodes) {
    while (at < kids.length && kids[at] !== node && (kids[at].hidden || placed.has(kids[at]))) at++;
    if (kids[at] === node) at++;
    else parent.insertBefore(node, kids[at] || null);
    placed.add(node);
  }
  if (active && active !== document.activeElement && parent.contains(active)) active.focus({ preventScroll: true });
}

// What an empty table should say, or '' when it has rows.
// state: { count, loading?, error?, filtered? }; messages: { loading, empty, filtered }
export function tableNote(messages, state) {
  if (state.loading) return messages.loading || 'Loading...';
  if (state.error) return state.error;
  if (state.count) return '';
  return state.filtered ? messages.filtered : messages.empty;
}

// opts: {
//   label, className?, rowKey(row), onSort(key), onActivate?(row),
//   columns: [{ key, label, sortable?, className?, text?(row), node?(row), sig?(row) }]
//   rowClass?(row), detail?(row) -> string | null, detailClass?, cap?, arrows?,
//   messages: { loading, empty, filtered }, noteClass?, errorClass?
// }
// A text column is written when it changes; a node column (one node or an array) is
// rebuilt when its sig changes (or never, without one). Returns { el, note, update(state) }.
export function createTable(opts) {
  const lines = new Map();
  const heads = new Map();
  const head = h('tr', {}, ...opts.columns.map(c => {
    if (!c.sortable) return h('th', { scope: 'col', class: c.className, text: c.label });
    const cell = sortCell({ key: c.key, label: c.label, onSort: opts.onSort, className: c.className, arrows: opts.arrows });
    heads.set(c.key, cell);
    return cell;
  }));
  const body = h('tbody', {});
  const el = h('table', { class: opts.className, 'aria-label': opts.label }, h('thead', {}, head), body);
  const note = h('p', { class: opts.noteClass, role: 'status' });

  function build(row) {
    const entry = { row, cells: opts.columns.map(c => h('td', { class: c.className })), seen: {} };
    entry.tr = h('tr', { tabIndex: opts.onActivate ? 0 : null }, entry.cells);
    if (opts.onActivate) {
      const go = () => opts.onActivate(entry.row);
      entry.tr.addEventListener('click', go);
      entry.tr.addEventListener('keydown', ev => {
        if (ev.target !== entry.tr || (ev.key !== 'Enter' && ev.key !== ' ')) return;
        ev.preventDefault();
        go();
      });
    }
    return entry;
  }

  function patch(entry, row) {
    entry.row = row;
    opts.columns.forEach((c, i) => {
      if (c.text) {
        const v = c.text(row);
        if (entry.seen[c.key] !== v) { entry.seen[c.key] = v; entry.cells[i].textContent = v; }
      } else if (c.node) {
        const sig = c.sig ? c.sig(row) : '';
        if (entry.seen[c.key] !== sig || !entry.cells[i].firstChild) { entry.seen[c.key] = sig; entry.cells[i].replaceChildren(...[].concat(c.node(row))); }
      }
    });
    const cls = opts.rowClass ? opts.rowClass(row) : '';
    if (entry.cls !== cls) { entry.cls = cls; entry.tr.className = cls; }
  }

  // state: { rows, sort, dir, loading?, error?, filtered? }
  // Returns { shown, total }: rows past opts.cap are not drawn.
  function update(state) {
    for (const [key, cell] of heads) markSort(cell, key === state.sort, state.dir);
    const rows = opts.cap ? state.rows.slice(0, opts.cap) : state.rows;
    const keep = new Set();
    const order = [];
    for (const row of rows) {
      const key = String(opts.rowKey(row));
      let entry = lines.get(key);
      if (!entry) { entry = build(row); lines.set(key, entry); }
      patch(entry, row);
      keep.add(key);
      order.push(entry.tr);
      if (opts.detail) {
        const text = opts.detail(row);
        entry.tr.setAttribute('aria-expanded', String(text != null));
        if (text == null) {
          if (entry.detail) { entry.detail.remove(); entry.detail = null; }
        } else {
          if (!entry.detail) {
            entry.pre = h('pre', {});
            entry.detail = h('tr', { class: opts.detailClass }, h('td', { colSpan: opts.columns.length }, entry.pre));
          }
          if (entry.pre.textContent !== text) entry.pre.textContent = text;
          order.push(entry.detail);
        }
      }
    }
    for (const [key, entry] of lines) {
      if (keep.has(key)) continue;
      entry.tr.remove();
      if (entry.detail) entry.detail.remove();
      lines.delete(key);
    }
    orderChildren(body, order);
    const text = tableNote(opts.messages, { count: rows.length, loading: state.loading, error: state.error, filtered: state.filtered });
    if (note.textContent !== text) note.textContent = text;
    note.className = `${opts.noteClass || ''}${state.error && opts.errorClass ? ` ${opts.errorClass}` : ''}`.trim();
    return { shown: rows.length, total: state.rows.length };
  }

  return { el, note, update };
}
