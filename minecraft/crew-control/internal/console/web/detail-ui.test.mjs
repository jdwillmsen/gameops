// DOM-level checks for the detail page. jsdom is not a dependency, so this
// file carries the small slice of the DOM that detail-ui.js touches: enough
// tree, events, focus and replacement to see what the page puts on screen and
// what survives a repaint. It is not a browser; layout and CSS are left to
// the end-to-end run.
import test from 'node:test';
import assert from 'node:assert/strict';

// ---- a small fake DOM ----

class FakeText {
  constructor(text) { this.text = String(text); this.parent = null; }
  get textContent() { return this.text; }
}

const classesOf = e => String(e.className || '').split(/\s+/).filter(Boolean);

class FakeEl {
  constructor(doc, tag) {
    this.doc = doc;
    this.tag = tag;
    this.children = [];
    this.parent = null;
    this.attrs = {};
    this.listeners = {};
    this.className = '';
    this.hidden = false;
    this.title = '';
    this.tabIndex = 0;
    this.style = { setProperty(k, v) { this[k] = v; } };
  }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  get textContent() { return this.children.map(c => c.textContent).join(''); }
  set textContent(v) { this.replaceChildren(new FakeText(v)); }
  get firstElementChild() { return this.children.find(c => c instanceof FakeEl) || null; }
  get lastChild() { return this.children[this.children.length - 1] || null; }
  get classList() { return { contains: c => classesOf(this).includes(c) }; }
  adopt(kids) {
    return kids.map(k => {
      const n = typeof k === 'string' ? new FakeText(k) : k;
      n.parent = this;
      return n;
    });
  }
  append(...kids) { this.children.push(...this.adopt(kids)); }
  replaceChildren(...kids) {
    for (const c of this.children) this.doc.detached(c);
    this.children = [];
    this.append(...kids);
  }
  remove() {
    if (!this.parent) return;
    this.parent.children = this.parent.children.filter(c => c !== this);
    this.doc.detached(this);
    this.parent = null;
  }
  replaceWith(node) {
    const at = this.parent.children.indexOf(this);
    const parent = this.parent;
    this.doc.detached(this);
    this.parent = null;
    parent.children.splice(at, 1, ...parent.adopt([node]));
  }
  contains(n) {
    for (let x = n; x; x = x.parent) if (x === this) return true;
    return false;
  }
  closest(sel) {
    const cls = sel.replace(/^\./, '');
    for (let x = this; x; x = x.parent) if (x instanceof FakeEl && classesOf(x).includes(cls)) return x;
    return null;
  }
  addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
  dispatch(type, init = {}) {
    const ev = { type, target: this, defaultPrevented: false, preventDefault() { this.defaultPrevented = true; }, ...init };
    for (let x = this; x; x = x.parent) for (const fn of (x.listeners && x.listeners[type]) || []) fn(ev);
    return ev;
  }
  click() { return this.dispatch('click'); }
  focus() {
    this.doc.activeElement = this;
    this.dispatch('focusin');
  }
}

class FakeDocument {
  constructor() {
    this.activeElement = null;
    this.visibilityState = 'visible';
    this.listeners = {};
  }
  createElement(tag) { return new FakeEl(this, tag); }
  // A node leaving the tree takes focus with it, as in a browser.
  detached(node) {
    if (this.activeElement && node instanceof FakeEl && node.contains(this.activeElement)) this.activeElement = null;
  }
  addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
  removeEventListener(type, fn) { this.listeners[type] = (this.listeners[type] || []).filter(f => f !== fn); }
  fire(type) { for (const fn of this.listeners[type] || []) fn({ type }); }
}

const walk = (node, fn) => {
  if (!(node instanceof FakeEl)) return;
  fn(node);
  for (const c of node.children) walk(c, fn);
};
const all = (root, pred) => {
  const out = [];
  walk(root, n => pred(n) && out.push(n));
  return out;
};
const byClass = (root, cls) => all(root, n => classesOf(n).includes(cls));
const one = (root, cls) => byClass(root, cls)[0] || null;
const slotsOf = (root, grid) => byClass(one(root, grid), 'dt-slot');

// ---- harness ----

const doc = new FakeDocument();
globalThis.document = doc;
let tick = null;
globalThis.setInterval = fn => { tick = fn; return 1; };
globalThis.clearInterval = () => { tick = null; };
const { initDetail } = await import('./detail-ui.js');

const settle = () => new Promise(r => setImmediate(r));

const iso = ms => new Date(ms).toISOString();
const NOW = Date.now();
const item = (slot, name, extra = {}) => ({ slot, name, count: 1, ...extra });

function payload(extra = {}) {
  return {
    id: 'crew-03', name: 'Cobalt', online: true,
    live: { state: 'known', asOf: iso(NOW) },
    save: { state: 'known', asOf: iso(NOW - 3 * 3600_000) },
    health: { current: 14, max: 20 }, hunger: { current: 18, max: 20 },
    xp: { level: 4, progress: 0.5 }, gameMode: 'survival', dimension: 'overworld',
    effects: [], inventory: [], armor: [], enderChest: null,
    ...extra,
  };
}

// A page against a scripted host. `answers` is read at each request, so a
// test can change what the next poll hears. Each call is recorded.
function page(answers, { sheet = null } = {}) {
  const calls = [];
  const fetched = [];
  let operator = { id: 'owner' };
  globalThis.fetch = async url => {
    fetched.push(url);
    if (!sheet) return { ok: false, status: 404 };
    return { ok: true, status: 200, json: async () => sheet };
  };
  const host = {
    statuses: {}, state: () => ({}), operator: () => operator,
    api: async (method, path, body, opts) => {
      calls.push({ method, path, opts });
      return typeof answers === 'function' ? answers(calls.length) : answers;
    },
  };
  const ui = initDetail(host);
  const into = doc.createElement('div');
  return {
    ui, calls, fetched, into,
    root: () => into.children[0],
    setOperator: op => { operator = op; },
    mount: async (id = 'crew-03') => { ui.mount(into, id); await settle(); },
    poll: async () => { assert.ok(tick, 'a poll is scheduled'); tick(); await settle(); },
  };
}

const ok = data => ({ ok: true, status: 200, data });

test.afterEach(() => { doc.activeElement = null; doc.visibilityState = 'visible'; });

// ---- what carries decides the screen ----

test('carry: a unit whose gear is only in the hotbar still shows its hotbar', async () => {
  const p = page(ok(payload({ inventory: [item(0, 'iron_sword'), item(1, 'bread', { count: 12 })] })));
  await p.mount();
  const root = p.root();
  assert.ok(one(root, 'dt-hotbar'), 'the hotbar grid is on the page');
  assert.equal(byClass(one(root, 'dt-hotbar'), 'dt-slot').filter(s => classesOf(s).includes('filled')).length, 2);
  assert.ok(!/nothing carried/.test(root.textContent), 'not reported as carrying nothing');
});

test('carry: a main-inventory-only unit shows both grids, the hotbar all empty', async () => {
  const p = page(ok(payload({ inventory: [item(9, 'cobblestone', { count: 64 })] })));
  await p.mount();
  assert.equal(slotsOf(p.root(), 'dt-main').filter(s => classesOf(s).includes('filled')).length, 1);
  assert.equal(slotsOf(p.root(), 'dt-hotbar').length, 9);
});

test('carry: nothing anywhere is said in words, with no grid', async () => {
  const p = page(ok(payload({ inventory: [item(40, 'bread')] })));
  await p.mount();
  assert.ok(/nothing carried/.test(p.root().textContent));
  assert.equal(one(p.root(), 'dt-hotbar'), null);
  assert.equal(one(p.root(), 'dt-main'), null);
});

// ---- the private answer is never kept ----

test('the detail read bypasses the browser cache', async () => {
  const p = page(ok(payload()));
  await p.mount();
  assert.equal(p.calls[0].method, 'GET');
  assert.match(p.calls[0].path, /\/units\/crew-03\/detail$/);
  assert.equal(p.calls[0].opts && p.calls[0].opts.cache, 'no-store');
});

test('sign-out clears the page at once, without waiting for a 401', async () => {
  const p = page(ok(payload({ inventory: [item(0, 'iron_sword')], enderChest: [item(0, 'diamond')].concat(new Array(26).fill(null)) })));
  await p.mount();
  assert.ok(/iron_sword/.test(all(p.root(), n => n.getAttribute('aria-label')).map(n => n.getAttribute('aria-label')).join()));
  p.setOperator(null);
  p.ui.onOperator(null);
  const text = p.root().textContent;
  assert.match(text, /Sign in to see a unit's inventory/);
  assert.ok(!/Cobalt|iron_sword|diamond|Health/.test(text), `signed-in data is gone: ${text}`);
  assert.equal(all(p.root(), n => (n.getAttribute('aria-label') || '').includes('iron_sword')).length, 0);
  assert.equal(p.calls.length, 1, 'no further read was needed to clear it');
});

test('a read already in flight at sign-out cannot repaint the signed-in answer', async () => {
  let release;
  const slow = new Promise(r => { release = r; });
  const p = page(slow);
  p.ui.mount(p.into, 'crew-03');
  await settle();
  p.setOperator(null);
  p.ui.onOperator(null);
  release(ok(payload({ inventory: [item(0, 'iron_sword')] })));
  await settle();
  assert.match(p.root().textContent, /Sign in to see a unit's inventory/);
  assert.equal(one(p.root(), 'dt-page'), null);
});

test('signing in again reads the unit afresh', async () => {
  const p = page(ok(payload()));
  await p.mount();
  p.ui.onOperator(null);
  p.setOperator({ id: 'owner' });
  p.ui.onOperator({ id: 'owner' });
  await settle();
  assert.equal(p.calls.length, 2);
  assert.ok(one(p.root(), 'dt-page'));
});
