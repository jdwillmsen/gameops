import test from 'node:test';
import assert from 'node:assert/strict';
import { installDom, all, byClass } from '../graph/fake-dom.mjs';

const dom = installDom();
const { doc } = dom;
const { h } = await import('./dom.js');
const { openDialog, focusables } = await import('./dialog.js');
const { createToasts } = await import('./toast.js');
const { createTabs } = await import('./tabs.js');
const { createKeys, isTyping } = await import('./keys.js');

const clean = () => {
  doc.body.replaceChildren();
  doc.activeElement = null;
  for (const t of dom.timers.pending()) clearTimeout(t);
};
const btn = (root, label) => all(root, n => n.tag === 'button' && n.textContent === label)[0];
const key = (target, k, init = {}) => target.dispatch('keydown', { key: k, ...init });

// ---- dialog ----

function dialogFixture(extra = {}) {
  clean();
  const opener = h('button', { text: 'Open' });
  doc.body.append(opener);
  opener.focus();
  const d = openDialog({
    label: 'Keyboard', class: 'help',
    content: [h('h2', { text: 'Keyboard' }), h('input', { type: 'text' }), h('button', { type: 'button', text: 'A' }), h('button', { type: 'button', text: 'B' })],
    ...extra,
  });
  return { opener, d };
}

test('dialog is a labelled modal in the overlay and takes focus', () => {
  const { d } = dialogFixture();
  assert.equal(d.el.getAttribute('id'), 'overlay');
  assert.equal(d.card.getAttribute('role'), 'dialog');
  assert.equal(d.card.getAttribute('aria-modal'), 'true');
  assert.equal(d.card.getAttribute('aria-label'), 'Keyboard');
  assert.equal(d.card.className, 'palette help');
  assert.ok(doc.body.contains(d.el));
  assert.equal(doc.activeElement.tag, 'input');
});

test('dialog focuses the element it is told to, else the card', () => {
  clean();
  const pick = h('button', { text: 'Pick' });
  const d = openDialog({ label: 'x', content: [h('button', { text: 'First' }), pick], focus: pick });
  assert.equal(doc.activeElement, pick);
  d.close();
  const bare = openDialog({ label: 'x', content: [h('p', { text: 'only text' })] });
  assert.equal(doc.activeElement, bare.card);
});

test('dialog returns focus to the opener on close, once', () => {
  let closed = 0;
  const { opener, d } = dialogFixture({ onClose: () => closed++ });
  d.close();
  d.close();
  assert.equal(closed, 1);
  assert.equal(doc.activeElement, opener);
  assert.equal(doc.body.contains(d.el), false);
});

test('dialog does not focus an opener that left the page', () => {
  const { opener, d } = dialogFixture();
  opener.remove();
  d.close();
  assert.equal(doc.activeElement, null);
});

test('Escape closes the dialog and is not passed on', () => {
  const { opener, d } = dialogFixture();
  let reached = 0;
  doc.body.addEventListener('keydown', () => reached++);
  const ev = key(doc.activeElement, 'Escape');
  assert.equal(ev.defaultPrevented, true);
  assert.equal(reached, 0);
  assert.equal(doc.body.contains(d.el), false);
  assert.equal(doc.activeElement, opener);
});

test('a click on the backdrop closes it, a click on the card does not', () => {
  const { d } = dialogFixture();
  d.card.dispatch('mousedown');
  assert.equal(doc.body.contains(d.el), true);
  d.el.dispatch('mousedown');
  assert.equal(doc.body.contains(d.el), false);
});

test('Tab wraps from the last control to the first and Shift-Tab back', () => {
  const { d } = dialogFixture();
  const [input, a, b] = focusables(d.card);
  assert.equal(doc.activeElement, input);
  b.focus();
  let ev = key(b, 'Tab');
  assert.equal(ev.defaultPrevented, true);
  assert.equal(doc.activeElement, input);
  ev = key(input, 'Tab', { shiftKey: true });
  assert.equal(ev.defaultPrevented, true);
  assert.equal(doc.activeElement, b);
  a.focus();
  ev = key(a, 'Tab');
  assert.equal(ev.defaultPrevented, false);
});

test('Tab from outside the card is pulled in', () => {
  const { d } = dialogFixture();
  const outside = h('button', { text: 'Out' });
  doc.body.append(outside);
  outside.focus();
  d.el.dispatch('keydown', { key: 'Tab' });
  assert.ok(d.card.contains(doc.activeElement));
});

test('a card with nothing focusable keeps focus on itself', () => {
  clean();
  const d = openDialog({ label: 'x', content: [h('p', { text: 't' })] });
  const ev = key(d.card, 'Tab');
  assert.equal(ev.defaultPrevented, true);
  assert.equal(doc.activeElement, d.card);
});

test('focusables skips disabled, hidden and subtrees that are hidden', () => {
  clean();
  const root = h('div', {},
    h('button', { text: 'ok' }),
    h('button', { text: 'off', disabled: true }),
    h('input', { type: 'hidden' }),
    h('div', { hidden: true }, h('button', { text: 'inside' })),
    h('a', { text: 'skip', tabIndex: -1 }),
    h('select', {}));
  assert.deepEqual(focusables(root).map(n => n.tag), ['button', 'select']);
});

// ---- toast ----

test('toasts sit in a polite live region and name the source', () => {
  clean();
  const box = h('div', { id: 'toasts' });
  const t = createToasts(box);
  assert.equal(box.getAttribute('role'), 'status');
  assert.equal(box.getAttribute('aria-live'), 'polite');
  const n = t.show({ level: 'warn', title: 'Ann', text: 'died' });
  assert.equal(n.className, 'toast warn');
  assert.equal(n.textContent, 'Ann died');
});

test('toasts keep an existing live region as it is', () => {
  const box = h('div', { role: 'log', 'aria-live': 'assertive' });
  createToasts(box);
  assert.equal(box.getAttribute('role'), 'log');
  assert.equal(box.getAttribute('aria-live'), 'assertive');
});

test('the queue drops the oldest beyond its cap', () => {
  const box = h('div', {});
  const t = createToasts(box, { max: 2 });
  for (const n of ['a', 'b', 'c']) t.show({ title: n, text: '' });
  assert.deepEqual(box.children.map(c => c.children[0].textContent), ['b', 'c']);
});

test('the default cap is four', () => {
  const box = h('div', {});
  const t = createToasts(box);
  for (let i = 0; i < 6; i++) t.show({ title: String(i), text: '' });
  assert.equal(box.children.length, 4);
});

test('a click dismisses the toast and runs its action', () => {
  const box = h('div', {});
  const t = createToasts(box);
  let acted = 0;
  const n = t.show({ title: 'a', text: 'b', onClick: () => acted++ });
  n.click();
  assert.equal(acted, 1);
  assert.equal(box.children.length, 0);
  t.show({ title: 'x', text: 'y' }).click();
  assert.equal(box.children.length, 0);
});

test('critical toasts stay longer and each expires', () => {
  clean();
  const box = h('div', {});
  const t = createToasts(box);
  t.show({ level: 'crit', title: 'a', text: '' });
  t.show({ level: 'ok', title: 'b', text: '' });
  t.show({ level: 'ok', title: 'c', text: '', ms: 1234 });
  assert.deepEqual(dom.timers.pending().map(x => x.ms), [10000, 6000, 1234]);
  dom.timers.pending().forEach(x => x.fn());
  assert.equal(box.children.length, 0);
});

test('clear empties the box', () => {
  const box = h('div', {});
  const t = createToasts(box);
  t.show({ title: 'a', text: '' });
  t.clear();
  assert.equal(box.children.length, 0);
});

// ---- tabs ----

function tabsFixture(extra = {}) {
  clean();
  const picked = [];
  const tabs = createTabs({ label: 'View', tabs: [{ id: 'a', label: 'A' }, { id: 'b', label: 'B' }, { id: 'c', label: 'C' }], onSelect: id => picked.push(id), ...extra });
  doc.body.append(tabs.el);
  return { tabs, picked, b: id => btn(tabs.el, id.toUpperCase()) };
}

test('tabs form a labelled tablist with one tab stop', () => {
  const { tabs, b } = tabsFixture();
  assert.equal(tabs.el.getAttribute('role'), 'tablist');
  assert.equal(tabs.el.getAttribute('aria-label'), 'View');
  assert.deepEqual(['a', 'b', 'c'].map(i => b(i).getAttribute('role')), ['tab', 'tab', 'tab']);
  assert.deepEqual(['a', 'b', 'c'].map(i => b(i).tabIndex), [0, -1, -1]);
  assert.deepEqual(['a', 'b', 'c'].map(i => b(i).getAttribute('aria-selected')), ['true', 'false', 'false']);
  assert.equal(tabs.selected(), 'a');
});

test('a click selects once and reports only a change', () => {
  const { tabs, picked, b } = tabsFixture();
  b('b').click();
  b('b').click();
  assert.deepEqual(picked, ['b']);
  assert.equal(b('b').tabIndex, 0);
  assert.equal(b('a').tabIndex, -1);
  assert.ok(byClass(tabs.el, 'selected').includes(b('b')));
  assert.equal(byClass(tabs.el, 'selected').length, 1);
});

test('arrows move focus and selection, wrapping at both ends', () => {
  const { picked, b } = tabsFixture();
  b('a').focus();
  assert.equal(key(b('a'), 'ArrowRight').defaultPrevented, true);
  assert.equal(doc.activeElement, b('b'));
  key(b('b'), 'ArrowRight');
  key(b('c'), 'ArrowRight');
  assert.equal(doc.activeElement, b('a'));
  key(b('a'), 'ArrowLeft');
  assert.equal(doc.activeElement, b('c'));
  assert.deepEqual(picked, ['b', 'c', 'a', 'c']);
});

test('Home and End jump to the ends; other keys are left alone', () => {
  const { tabs, b } = tabsFixture();
  key(b('a'), 'End');
  assert.equal(tabs.selected(), 'c');
  key(b('c'), 'Home');
  assert.equal(tabs.selected(), 'a');
  assert.equal(key(b('a'), 'ArrowDown').defaultPrevented, false);
  assert.equal(key(b('a'), 'x').defaultPrevented, false);
});

test('select() from code does not steal focus and ignores unknown ids', () => {
  const { tabs, picked, b } = tabsFixture({ selected: 'b' });
  assert.equal(b('b').tabIndex, 0);
  doc.body.append(h('input', {}));
  tabs.select('c');
  tabs.select('nope');
  assert.equal(tabs.selected(), 'c');
  assert.equal(doc.activeElement, null);
  assert.deepEqual(picked, ['c']);
});

test('tabs point at their panels when asked', () => {
  const { b } = tabsFixture({ panelId: id => `panel-${id}` });
  assert.equal(b('a').getAttribute('aria-controls'), 'panel-a');
});

// ---- keys ----

const ev = (k, init = {}) => ({ key: k, target: { tagName: 'BODY' }, ...init });

test('isTyping covers fields and editable content only', () => {
  for (const tag of ['INPUT', 'SELECT', 'TEXTAREA']) assert.equal(isTyping({ tagName: tag }), true);
  assert.equal(isTyping({ tagName: 'BUTTON' }), false);
  assert.equal(isTyping({ tagName: 'DIV', isContentEditable: true }), true);
  assert.equal(isTyping(null), false);
});

test('Escape layers answer newest first and stop at the first that handles it', () => {
  const keys = createKeys();
  const seen = [];
  keys.escape(() => { seen.push('view'); return true; });
  keys.escape(() => { seen.push('overlay'); return false; });
  assert.equal(keys.handle(ev('Escape')), true);
  assert.deepEqual(seen, ['overlay', 'view']);
  seen.length = 0;
  keys.escape(() => { seen.push('top'); return true; });
  keys.handle(ev('Escape'));
  assert.deepEqual(seen, ['top']);
});

test('an Escape nobody handles is reported as unhandled', () => {
  const keys = createKeys();
  assert.equal(keys.handle(ev('Escape')), false);
  keys.escape(() => false);
  assert.equal(keys.handle(ev('Escape')), false);
});

test('removing a layer uncovers the one below', () => {
  const keys = createKeys();
  const seen = [];
  keys.escape(() => { seen.push('low'); return true; });
  const off = keys.escape(() => { seen.push('high'); return true; });
  off();
  off();
  keys.handle(ev('Escape'));
  assert.deepEqual(seen, ['low']);
});

test('Escape is left to a field that is being typed in', () => {
  const keys = createKeys();
  let n = 0;
  keys.escape(() => { n++; return true; });
  assert.equal(keys.handle(ev('Escape', { target: { tagName: 'INPUT' } })), false);
  assert.equal(n, 0);
});

test('plain bindings skip typing and modifiers', () => {
  const keys = createKeys();
  const hits = [];
  keys.bind('j', e => { hits.push('j'); return true; });
  assert.equal(keys.handle(ev('j')), true);
  assert.equal(keys.handle(ev('j', { target: { tagName: 'TEXTAREA' } })), false);
  assert.equal(keys.handle(ev('j', { ctrlKey: true })), false);
  assert.equal(keys.handle(ev('j', { altKey: true })), false);
  assert.equal(keys.handle(ev('k')), false);
  assert.deepEqual(hits, ['j']);
});

test('a typing binding fires inside fields and a mod binding needs ctrl or meta', () => {
  const keys = createKeys();
  const hits = [];
  keys.bind('Enter', () => { hits.push('enter'); return true; }, { typing: true });
  keys.bind('k', () => { hits.push('palette'); return true; }, { mod: true });
  assert.equal(keys.handle(ev('Enter', { target: { tagName: 'INPUT' } })), true);
  assert.equal(keys.handle(ev('k')), false);
  assert.equal(keys.handle(ev('K', { metaKey: true, target: { tagName: 'INPUT' } })), false);
  assert.equal(keys.handle(ev('K', { ctrlKey: true })), true);
  assert.deepEqual(hits, ['enter', 'palette']);
});

test('a binding that declines lets an older one answer; unbind removes it', () => {
  const keys = createKeys();
  const hits = [];
  keys.bind('x', () => { hits.push('old'); return true; });
  const off = keys.bind('x', () => { hits.push('new'); return false; });
  keys.handle(ev('x'));
  assert.deepEqual(hits, ['new', 'old']);
  off();
  keys.handle(ev('x'));
  assert.deepEqual(hits, ['new', 'old', 'old']);
});

test('attach listens for keydown and detaches', () => {
  clean();
  const keys = createKeys();
  let n = 0;
  keys.bind('q', () => { n++; return true; });
  const detach = keys.attach(doc.body);
  doc.body.dispatch('keydown', { key: 'q', target: doc.body });
  detach();
  doc.body.dispatch('keydown', { key: 'q', target: doc.body });
  assert.equal(n, 1);
});
