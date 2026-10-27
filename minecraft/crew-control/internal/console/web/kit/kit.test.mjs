import test from 'node:test';
import assert from 'node:assert/strict';
import { installDom, all, byClass } from '../graph/fake-dom.mjs';

const { doc } = installDom();
const { h, keepFocus, FOCUS_KEY } = await import('./dom.js');
const { errorText, stateView } = await import('./states.js');
const { createPanel } = await import('./panel.js');

const button = (root, label) => all(root, n => n.tag === 'button' && n.textContent === label)[0];

test('h sets aria, role and id as attributes and the rest as properties', () => {
  const el = h('select', { 'aria-label': 'Show', role: 'listbox', id: 'x', 'data-k': 3, title: 'T', hidden: true });
  assert.equal(el.getAttribute('aria-label'), 'Show');
  assert.equal(el.getAttribute('role'), 'listbox');
  assert.equal(el.getAttribute('id'), 'x');
  assert.equal(el.getAttribute('data-k'), '3');
  assert.equal(el.title, 'T');
  assert.equal(el.hidden, true);
});

test('h keeps aria-pressed="false" instead of dropping it', () => {
  assert.equal(h('button', { 'aria-pressed': false }).getAttribute('aria-pressed'), 'false');
});

test('h takes class, text and event handlers', () => {
  let clicks = 0;
  const el = h('button', { class: 'a b', text: 'Go', on: { click: () => clicks++ } });
  assert.equal(el.className, 'a b');
  assert.equal(el.textContent, 'Go');
  el.click();
  assert.equal(clicks, 1);
});

test('h flattens arrays, skips null and false, and writes numbers as text', () => {
  const el = h('p', {}, 'a', [h('b', { text: 'b' }), [null, false, undefined]], 0, 7);
  assert.equal(el.textContent, 'ab07');
});

test('h never parses markup', () => {
  const el = h('p', { text: '<img src=x onerror=alert(1)>' });
  assert.equal(el.children.length, 1);
  assert.equal(el.children[0].text, '<img src=x onerror=alert(1)>');
});

test('h sets a select value only after its options exist', () => {
  const real = doc.createElement.bind(doc);
  const seen = [];
  doc.createElement = tag => {
    const e = real(tag);
    if (tag === 'select') Object.defineProperty(e, 'value', { set(v) { seen.push([v, e.children.length]); }, get: () => '' });
    return e;
  };
  h('select', { value: 'b' }, h('option', { value: 'a' }), h('option', { value: 'b' }));
  doc.createElement = real;
  assert.deepEqual(seen, [['b', 2]]);
});

test('keepFocus returns focus to the element with the same key after a redraw', () => {
  const root = h('div', {}, h('button', { [FOCUS_KEY]: 'ack-1', text: 'Ack' }), h('button', { text: 'Other' }));
  doc.body.append(root);
  button(root, 'Ack').focus();
  keepFocus(root, () => root.replaceChildren(h('button', { 'data-focus-key': 'ack-1', text: 'Ack' }), h('button', { text: 'Other' })));
  assert.equal(doc.activeElement.getAttribute(FOCUS_KEY), 'ack-1');
  assert.ok(root.contains(doc.activeElement));
});

test('keepFocus leaves focus alone when it was outside the root or the key is gone', () => {
  const root = h('div', {}, h('button', { 'data-focus-key': 'k', text: 'A' }));
  const outside = h('button', { text: 'Out' });
  doc.body.append(root, outside);
  outside.focus();
  keepFocus(root, () => root.replaceChildren());
  assert.equal(doc.activeElement, outside);
  button(root, 'A') || root.replaceChildren(h('button', { 'data-focus-key': 'k', text: 'A' }));
  button(root, 'A').focus();
  keepFocus(root, () => root.replaceChildren(h('p', { text: 'gone' })));
  assert.equal(doc.activeElement, null);
});

test('errorText names the verb and prefers the server message', () => {
  assert.equal(errorText('load alerts', { status: 500, data: { error: 'db down' } }), 'Could not load alerts. db down');
  assert.equal(errorText('load alerts', { status: 502, data: null }), 'Could not load alerts. Failed (502).');
  assert.equal(errorText('load alerts'), 'Could not load alerts. Failed (0).');
});

test('loading is a status region', () => {
  const el = stateView('loading');
  assert.equal(el.getAttribute('role'), 'status');
  assert.equal(el.textContent, 'Loading...');
});

test('empty says what is empty', () => {
  assert.equal(stateView('empty', { message: 'No events yet.' }).textContent, 'No events yet.');
});

test('error is an alert with a working retry', () => {
  let retried = 0;
  const el = stateView('error', { message: 'Could not load. Failed (500).', retry: () => retried++ });
  assert.equal(el.getAttribute('role'), 'alert');
  button(el, 'Retry').click();
  assert.equal(retried, 1);
  assert.equal(button(stateView('error', { message: 'x' }), 'Retry'), undefined);
});

test('signed out names the action and links to the sign-in page', () => {
  const el = stateView('signedOut', { what: 'acknowledge alerts' });
  assert.equal(all(el, n => n.tag === 'h3')[0].textContent, 'Sign in to acknowledge alerts');
  assert.equal(all(el, n => n.tag === 'a')[0].href, '/signin');
});

test('an unknown state throws instead of painting nothing', () => {
  assert.throws(() => stateView('weird'), /unknown state/);
});

test('panel labels itself, titles its heading and starts ready and empty', () => {
  const p = createPanel({ title: 'Alerts' });
  assert.equal(p.el.getAttribute('aria-label'), 'Alerts');
  assert.equal(all(p.el, n => n.tag === 'h2')[0].textContent, 'Alerts');
  assert.equal(p.kind, 'ready');
  assert.equal(button(p.el, 'Close'), undefined);
});

test('panel close button names its target and calls back', () => {
  let closed = 0;
  const p = createPanel({ title: 'Alerts', onClose: () => closed++ });
  const close = button(p.el, 'Close');
  assert.equal(close.getAttribute('aria-label'), 'Close Alerts');
  close.click();
  assert.equal(closed, 1);
});

test('panel shows content, then a state, then content again', () => {
  const p = createPanel({ title: 'T' });
  p.show('ready', h('p', { text: 'rows' }));
  assert.equal(p.body.textContent, 'rows');
  assert.equal(p.el.getAttribute('aria-busy'), 'false');
  p.show('loading');
  assert.equal(p.body.textContent, 'Loading...');
  assert.equal(p.el.getAttribute('aria-busy'), 'true');
  p.show('error', { message: 'Could not x. Failed (500).' });
  assert.match(p.body.textContent, /Could not x/);
  p.show('ready', [h('p', { text: 'a' }), h('p', { text: 'b' })]);
  assert.equal(p.body.textContent, 'ab');
  assert.equal(p.kind, 'ready');
});

test('panel announces through its status region and re-announces a repeat', () => {
  const p = createPanel({ title: 'T' });
  const live = byClass(p.el, 'kit-sr')[0];
  assert.equal(live.getAttribute('role'), 'status');
  // A live region only re-announces when its text changes, so the repeat
  // must pass through an empty write first.
  const writes = [];
  const proto = Object.getPrototypeOf(live);
  const { get, set } = Object.getOwnPropertyDescriptor(proto, 'textContent');
  Object.defineProperty(live, 'textContent', {
    get() { return get.call(this); },
    set(v) { writes.push(v); set.call(this, v); },
  });
  p.announce('3 alerts');
  assert.equal(live.textContent, '3 alerts');
  p.announce('3 alerts');
  assert.equal(live.textContent, '3 alerts');
  assert.deepEqual(writes, ['', '3 alerts', '', '3 alerts']);
});

test('panel title can change', () => {
  const p = createPanel({ title: 'A' });
  p.setTitle('B (2)');
  assert.equal(all(p.el, n => n.tag === 'h2')[0].textContent, 'B (2)');
});
