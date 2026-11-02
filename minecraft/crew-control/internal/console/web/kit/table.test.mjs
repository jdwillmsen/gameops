import test from 'node:test';
import assert from 'node:assert/strict';
import { installDom, all, byClass } from '../graph/fake-dom.mjs';

const { doc } = installDom();
const { h } = await import('./dom.js');
const { sortCell, markSort, orderChildren, tableNote, createTable } = await import('./table.js');

const texts = parent => parent.children.map(c => c.textContent);

test('a sort header is one button, starts unsorted and asks for its column', () => {
  const asked = [];
  const cell = sortCell({ key: 'at', label: 'Time', onSort: k => asked.push(k) });
  assert.equal(cell.tag, 'th');
  assert.equal(cell.getAttribute('aria-sort'), 'none');
  assert.equal(cell.textContent, 'Time');
  const buttons = all(cell, n => n.tag === 'button');
  assert.equal(buttons.length, 1);
  buttons[0].click();
  assert.deepEqual(asked, ['at']);
});

test('markSort names the direction in aria-sort and shows an arrow only on the sorted column', () => {
  const cell = sortCell({ key: 'at', label: 'Time', onSort() {} });
  markSort(cell, true, 'asc');
  assert.equal(cell.getAttribute('aria-sort'), 'ascending');
  assert.equal(cell.textContent, 'Time▲');
  markSort(cell, true, 'desc');
  assert.equal(cell.getAttribute('aria-sort'), 'descending');
  assert.equal(cell.textContent, 'Time▼');
  markSort(cell, false, 'desc');
  assert.equal(cell.getAttribute('aria-sort'), 'none');
  assert.equal(cell.textContent, 'Time');
});

test('the arrow is hidden from assistive technology since aria-sort says it', () => {
  const cell = sortCell({ key: 'k', label: 'K', onSort() {} });
  assert.equal(byClass(cell, 'kit-arrow')[0].getAttribute('aria-hidden'), 'true');
});

test('a header can be a div with a role and its own glyphs and arrow class', () => {
  const cell = sortCell({ key: 'k', label: 'K', onSort() {}, tag: 'div', role: 'columnheader', className: 'ov-h', arrowClass: 'ov-arrow', arrows: ['↑', '↓'] });
  assert.equal(cell.tag, 'div');
  assert.equal(cell.getAttribute('role'), 'columnheader');
  assert.equal(cell.scope, undefined);
  markSort(cell, true, 'desc');
  assert.equal(byClass(cell, 'ov-arrow')[0].textContent, '↓');
});

test('markSort writes nothing when nothing changed', () => {
  const cell = sortCell({ key: 'k', label: 'K', onSort() {} });
  markSort(cell, true, 'asc');
  const arrow = cell.arrow;
  let writes = 0;
  const proto = Object.getPrototypeOf(arrow);
  const { get, set } = Object.getOwnPropertyDescriptor(proto, 'textContent');
  Object.defineProperty(arrow, 'textContent', { get() { return get.call(this); }, set(v) { writes++; set.call(this, v); } });
  markSort(cell, true, 'asc');
  assert.equal(writes, 0);
});

// ---- ordering ----

const list = (...names) => h('ul', {}, ...names.map(n => h('li', { text: n })));
const byName = (ul, n) => ul.children.find(c => c.textContent === n);

test('orderChildren reorders to match the list', () => {
  const ul = list('a', 'b', 'c', 'd');
  orderChildren(ul, ['d', 'b', 'a', 'c'].map(n => byName(ul, n)));
  assert.deepEqual(texts(ul), ['d', 'b', 'a', 'c']);
});

test('orderChildren moves only what is out of place', () => {
  const ul = list('a', 'b', 'c', 'd');
  const moved = [];
  const real = ul.insertBefore.bind(ul);
  ul.insertBefore = (n, ref) => { moved.push(n.textContent); return real(n, ref); };
  orderChildren(ul, ['a', 'b', 'c', 'd'].map(n => byName(ul, n)));
  assert.deepEqual(moved, []);
  orderChildren(ul, ['b', 'a', 'c', 'd'].map(n => byName(ul, n)));
  assert.equal(moved.length, 1);
  assert.deepEqual(texts(ul), ['b', 'a', 'c', 'd']);
});

test('orderChildren adds new nodes and leaves hidden nodes that are not in the list', () => {
  const ul = list('a', 'b', 'c');
  byName(ul, 'b').hidden = true;
  const fresh = h('li', { text: 'z' });
  orderChildren(ul, [byName(ul, 'c'), fresh, byName(ul, 'a')]);
  assert.deepEqual(texts(ul).filter(t => t !== 'b'), ['c', 'z', 'a']);
  assert.ok(ul.children.includes(byName(ul, 'b')));
});

test('a focused node keeps focus through a move', () => {
  const ul = list('a', 'b', 'c');
  doc.body.append(ul);
  const a = byName(ul, 'a');
  a.focus();
  orderChildren(ul, ['b', 'c', 'a'].map(n => byName(ul, n)));
  assert.equal(doc.activeElement, a);
  assert.deepEqual(texts(ul), ['b', 'c', 'a']);
});

// ---- notes ----

const MSG = { loading: 'Loading...', empty: 'No events yet.', filtered: 'No events match.' };

test('tableNote says loading first, then the error, then why it is empty', () => {
  assert.equal(tableNote(MSG, { count: 0, loading: true, error: 'boom' }), 'Loading...');
  assert.equal(tableNote(MSG, { count: 0, error: 'boom' }), 'boom');
  assert.equal(tableNote(MSG, { count: 0 }), 'No events yet.');
  assert.equal(tableNote(MSG, { count: 0, filtered: true }), 'No events match.');
  assert.equal(tableNote(MSG, { count: 3, filtered: true }), '');
});

// ---- table ----

function fixture(extra = {}) {
  const sorted = [];
  const opened = [];
  const t = createTable({
    label: 'Events', className: 'tbl', rowKey: r => r.id,
    onSort: k => sorted.push(k), onActivate: r => opened.push(r.id),
    rowClass: r => `row ${r.level}`,
    messages: MSG, noteClass: 'note', errorClass: 'err',
    columns: [
      { key: 'name', label: 'Name', sortable: true, text: r => r.name },
      { key: 'level', label: 'Level', sortable: true, className: 'lvl', sig: r => r.level, node: r => [h('i', { text: '*' }), r.level] },
      { key: 'more', label: 'More', text: r => r.more || '' },
    ],
    ...extra,
  });
  doc.body.replaceChildren(t.el);
  return { t, sorted, opened, body: () => t.el.children[1], rows: () => t.el.children[1].children };
}
const ev = (id, o = {}) => ({ id, name: `n${id}`, level: 'ok', ...o });

test('the table labels itself and heads only sortable columns with buttons', () => {
  const { t } = fixture();
  assert.equal(t.el.getAttribute('aria-label'), 'Events');
  const heads = all(t.el, n => n.tag === 'th');
  assert.deepEqual(heads.map(n => n.textContent), ['Name', 'Level', 'More']);
  assert.deepEqual(heads.map(n => all(n, b => b.tag === 'button').length), [1, 1, 0]);
  assert.deepEqual(heads.map(n => n.scope), ['col', 'col', 'col']);
});

test('update marks the sorted column and a header click asks for a sort', () => {
  const { t, sorted } = fixture();
  t.update({ rows: [ev(1)], sort: 'level', dir: 'desc' });
  const heads = all(t.el, n => n.tag === 'th');
  assert.deepEqual(heads.map(n => n.getAttribute('aria-sort')), ['none', 'descending', null]);
  all(heads[0], n => n.tag === 'button')[0].click();
  assert.deepEqual(sorted, ['name']);
});

test('rows draw text, node cells and the row class', () => {
  const { t, rows } = fixture();
  t.update({ rows: [ev(1, { level: 'warn' }), ev(2)], sort: 'name', dir: 'asc' });
  assert.equal(rows().length, 2);
  assert.equal(rows()[0].className, 'row warn');
  assert.deepEqual(rows()[0].children.map(c => c.textContent), ['n1', '*warn', '']);
  assert.equal(rows()[0].children[1].className, 'lvl');
});

test('rows are patched by key: the same element survives and keeps focus', () => {
  const { t, rows } = fixture();
  t.update({ rows: [ev(1), ev(2), ev(3)], sort: 'name', dir: 'asc' });
  const second = rows()[1];
  second.focus();
  t.update({ rows: [ev(3), ev(2, { name: 'renamed' }), ev(1)], sort: 'name', dir: 'asc' });
  assert.deepEqual(Array.from(rows()).map(r => r.children[0].textContent), ['n3', 'renamed', 'n1']);
  assert.equal(rows()[1], second);
  assert.equal(doc.activeElement, second);
});

test('a text cell is written only when its value changes', () => {
  const { t, rows } = fixture();
  t.update({ rows: [ev(1)], sort: 'name', dir: 'asc' });
  const cell = rows()[0].children[0];
  let writes = 0;
  const proto = Object.getPrototypeOf(cell);
  const { get, set } = Object.getOwnPropertyDescriptor(proto, 'textContent');
  Object.defineProperty(cell, 'textContent', { get() { return get.call(this); }, set(v) { writes++; set.call(this, v); } });
  t.update({ rows: [ev(1)], sort: 'name', dir: 'asc' });
  assert.equal(writes, 0);
  t.update({ rows: [ev(1, { name: 'x' })], sort: 'name', dir: 'asc' });
  assert.equal(writes, 1);
});

test('a node cell is rebuilt only when its sig changes', () => {
  const { t, rows } = fixture();
  t.update({ rows: [ev(1)], sort: 'name', dir: 'asc' });
  const icon = rows()[0].children[1].children[0];
  t.update({ rows: [ev(1, { name: 'other' })], sort: 'name', dir: 'asc' });
  assert.equal(rows()[0].children[1].children[0], icon);
  t.update({ rows: [ev(1, { level: 'crit' })], sort: 'name', dir: 'asc' });
  assert.notEqual(rows()[0].children[1].children[0], icon);
  assert.equal(rows()[0].children[1].textContent, '*crit');
});

test('rows that leave the data leave the table', () => {
  const { t, rows } = fixture();
  t.update({ rows: [ev(1), ev(2)], sort: 'name', dir: 'asc' });
  t.update({ rows: [ev(2)], sort: 'name', dir: 'asc' });
  assert.equal(rows().length, 1);
  assert.equal(rows()[0].children[0].textContent, 'n2');
  t.update({ rows: [ev(1)], sort: 'name', dir: 'asc' });
  assert.equal(rows().length, 1);
  assert.equal(rows()[0].children[0].textContent, 'n1');
});

test('a row is keyboard operable: focusable, Enter and Space activate, other keys do not', () => {
  const { t, opened, rows } = fixture();
  t.update({ rows: [ev(7)], sort: 'name', dir: 'asc' });
  const tr = rows()[0];
  assert.equal(tr.tabIndex, 0);
  tr.click();
  let e = tr.dispatch('keydown', { key: 'Enter', target: tr });
  assert.equal(e.defaultPrevented, true);
  tr.dispatch('keydown', { key: ' ', target: tr });
  tr.dispatch('keydown', { key: 'a', target: tr });
  assert.deepEqual(opened, [7, 7, 7]);
});

test('Enter on something inside a row is left to that control', () => {
  const { t, opened, rows } = fixture();
  t.update({ rows: [ev(1)], sort: 'name', dir: 'asc' });
  const tr = rows()[0];
  tr.dispatch('keydown', { key: 'Enter', target: tr.children[0] });
  assert.deepEqual(opened, []);
});

test('activation sees the newest data of the row, not the first', () => {
  const seen = [];
  const t = createTable({ label: 'x', rowKey: r => r.id, onSort() {}, onActivate: r => seen.push(r.name), messages: MSG, columns: [{ key: 'name', label: 'N', text: r => r.name }] });
  t.update({ rows: [ev(1)], sort: '', dir: 'asc' });
  t.update({ rows: [ev(1, { name: 'fresh' })], sort: '', dir: 'asc' });
  t.el.children[1].children[0].click();
  assert.deepEqual(seen, ['fresh']);
});

test('without onActivate rows carry no click handler', () => {
  const t = createTable({ label: 'x', rowKey: r => r.id, onSort() {}, messages: MSG, columns: [{ key: 'name', label: 'N', text: r => r.name }] });
  t.update({ rows: [ev(1)], sort: '', dir: 'asc' });
  assert.equal(t.el.children[1].children[0].listeners.click, undefined);
});

test('the row cap hides the rest and reports it', () => {
  const { t, rows } = fixture({ cap: 2 });
  const r = t.update({ rows: [ev(1), ev(2), ev(3), ev(4)], sort: 'name', dir: 'asc' });
  assert.equal(rows().length, 2);
  assert.deepEqual(r, { shown: 2, total: 4 });
});

test('detail rows open under their row, aria-expanded follows, and close again', () => {
  const open = new Set([1]);
  const { t, rows } = fixture({ detail: r => (open.has(r.id) ? `full ${r.id}` : null), detailClass: 'full' });
  t.update({ rows: [ev(1), ev(2)], sort: 'name', dir: 'asc' });
  assert.equal(rows().length, 3);
  assert.equal(rows()[0].getAttribute('aria-expanded'), 'true');
  assert.equal(rows()[1].className, 'full');
  assert.equal(rows()[1].textContent, 'full 1');
  assert.equal(rows()[1].children[0].colSpan, 3);
  assert.equal(rows()[2].getAttribute('aria-expanded'), 'false');
  open.clear();
  open.add(2);
  t.update({ rows: [ev(1), ev(2)], sort: 'name', dir: 'asc' });
  assert.equal(rows().length, 3);
  assert.equal(rows()[0].getAttribute('aria-expanded'), 'false');
  assert.equal(rows()[2].textContent, 'full 2');
});

test('a detail row goes away with its row', () => {
  const { t, rows } = fixture({ detail: () => 'x' });
  t.update({ rows: [ev(1), ev(2)], sort: 'name', dir: 'asc' });
  t.update({ rows: [ev(2)], sort: 'name', dir: 'asc' });
  assert.equal(rows().length, 2);
});

test('the note says loading, error, empty and filtered-empty, and clears with rows', () => {
  const { t } = fixture();
  t.update({ rows: [], sort: 'name', dir: 'asc', loading: true });
  assert.equal(t.note.textContent, 'Loading...');
  t.update({ rows: [], sort: 'name', dir: 'asc', error: 'Could not load events.' });
  assert.equal(t.note.textContent, 'Could not load events.');
  assert.equal(t.note.className, 'note err');
  t.update({ rows: [], sort: 'name', dir: 'asc' });
  assert.equal(t.note.textContent, 'No events yet.');
  assert.equal(t.note.className, 'note');
  t.update({ rows: [], sort: 'name', dir: 'asc', filtered: true });
  assert.equal(t.note.textContent, 'No events match.');
  t.update({ rows: [ev(1)], sort: 'name', dir: 'asc', filtered: true });
  assert.equal(t.note.textContent, '');
  assert.equal(t.note.getAttribute('role'), 'status');
});
