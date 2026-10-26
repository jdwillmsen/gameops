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
function page(answers, { sheet = null, history = null } = {}) {
  const calls = [];
  const fetched = [];
  let operator = { id: 'owner' };
  globalThis.fetch = async url => {
    // The Why and history panels read their own routes; this file checks the sheet reads.
    if (String(url).includes('/inventory/changes')) {
      if (history === 401) return { ok: false, status: 401 };
      return history ? { ok: true, status: 200, json: async () => history } : { ok: false, status: 500 };
    }
    if (String(url).includes('/explain')) return { ok: false, status: 404 };
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

// ---- a repaint keeps what the operator was doing ----

// A scripted host whose answer a test swaps between polls.
function scripted(first, opts) {
  const s = { next: first };
  const p = page(() => s.next, opts);
  p.say = a => { s.next = a; };
  return p;
}

const filled = grid => grid.filter(s => classesOf(s).includes('filled'));
const inspectOf = (root, gridCls) => one(one(root, gridCls).parent, 'dt-inspect').textContent;

test('an unchanged answer repaints nothing: the very same nodes stay', async () => {
  const inv = [item(0, 'iron_sword'), item(9, 'bread')];
  const p = scripted(ok(payload({ inventory: inv })));
  await p.mount();
  const before = ['dt-main', 'dt-hotbar', 'dt-vitals'].map(c => one(p.root(), c));
  // The poller's own clock moves every read; it is not a change of content.
  p.say(ok(payload({ inventory: inv, live: { state: 'known', asOf: iso(NOW + 1000) } })));
  await p.poll();
  const after = ['dt-main', 'dt-hotbar', 'dt-vitals'].map(c => one(p.root(), c));
  after.forEach((n, i) => assert.equal(n, before[i], 'section node was kept'));
});

test('a change in one section leaves the others, and their focus, alone', async () => {
  const inv = [item(0, 'iron_sword'), item(1, 'bread')];
  const p = scripted(ok(payload({ inventory: inv })));
  await p.mount();
  const hotbar = slotsOf(p.root(), 'dt-hotbar');
  hotbar[1].focus();
  const grid = one(p.root(), 'dt-hotbar');
  p.say(ok(payload({ inventory: inv, health: { current: 9, max: 20 } })));
  await p.poll();
  assert.equal(one(p.root(), 'dt-hotbar'), grid, 'the hotbar was not remade for a health change');
  assert.equal(doc.activeElement, hotbar[1], 'focus is still on the slot');
  assert.match(byClass(p.root(), 'hp')[0].title, /Health 9 of 20/);
});

test('a repaint of the focused grid puts focus back on the same slot and says it again', async () => {
  const inv = [item(0, 'iron_sword'), item(1, 'bread', { count: 3 })];
  const p = scripted(ok(payload({ inventory: inv })));
  await p.mount();
  slotsOf(p.root(), 'dt-hotbar')[1].focus();
  assert.match(inspectOf(p.root(), 'dt-hotbar'), /bread x3/);
  const old = slotsOf(p.root(), 'dt-hotbar')[1];
  // The stack count changes, so the grid is remade.
  p.say(ok(payload({ inventory: [inv[0], item(1, 'bread', { count: 5 })] })));
  await p.poll();
  const now = slotsOf(p.root(), 'dt-hotbar')[1];
  assert.notEqual(now, old, 'the grid really was remade');
  assert.equal(doc.activeElement, now, 'focus moved to the same slot of the new grid');
  assert.match(inspectOf(p.root(), 'dt-hotbar'), /bread x5/);
  assert.equal(now.tabIndex, 0);
  assert.equal(slotsOf(p.root(), 'dt-hotbar').filter(s => s.tabIndex === 0).length, 1, 'one tab stop in the grid');
});

test('an expanded shulker box stays expanded across a repaint', async () => {
  const box = item(9, 'shulker_box', { contents: [item(0, 'iron_ingot', { count: 64 })] });
  const p = scripted(ok(payload({ inventory: [box, item(0, 'bread')] })));
  await p.mount();
  slotsOf(p.root(), 'dt-main')[0].click();
  assert.match(inspectOf(p.root(), 'dt-main'), /inside: iron_ingot x64/);
  p.say(ok(payload({ inventory: [box, item(0, 'bread', { count: 2 }), item(10, 'cobblestone')] })));
  await p.poll();
  assert.match(inspectOf(p.root(), 'dt-main'), /inside: iron_ingot x64/, 'still open after the grid was remade');
  assert.equal(doc.activeElement, slotsOf(p.root(), 'dt-main')[0]);
});

test('a failed poll keeps the last good view and marks it stale', async () => {
  const p = scripted(ok(payload({ inventory: [item(0, 'iron_sword')] })));
  await p.mount();
  const page_ = one(p.root(), 'dt-page');
  p.say({ ok: false, status: 0, data: { error: 'could not reach the server' } });
  await p.poll();
  assert.equal(one(p.root(), 'dt-page'), page_, 'the page was not replaced by an error');
  assert.ok(one(p.root(), 'dt-hotbar'), 'the inventory is still on screen');
  const stale = one(p.root(), 'dt-stale');
  assert.equal(stale.hidden, false);
  assert.match(stale.textContent, /could not refresh/i);
  p.say({ ok: false, status: 502, data: null });
  await p.poll();
  assert.equal(one(p.root(), 'dt-stale').hidden, false, 'a bad gateway is stale too');
  // The next good read clears the marker.
  p.say(ok(payload({ inventory: [item(0, 'iron_sword')] })));
  await p.poll();
  assert.equal(one(p.root(), 'dt-stale').hidden, true);
});

test('with nothing yet drawn, a failed first read offers a retry', async () => {
  const p = scripted({ ok: false, status: 0, data: { error: 'could not reach the server' } });
  await p.mount();
  assert.match(p.root().textContent, /Could not read this unit/);
  assert.equal(one(p.root(), 'dt-page'), null);
  p.say(ok(payload()));
  all(p.root(), n => n.tag === 'button')[0].click();
  await settle();
  assert.ok(one(p.root(), 'dt-page'), 'retry paints the page');
});

test('a later 401 or 404 does replace the page: those are answers, not outages', async () => {
  const p = scripted(ok(payload({ inventory: [item(0, 'iron_sword')] })));
  await p.mount();
  p.say({ ok: false, status: 401, data: { error: 'signed_out' } });
  await p.poll();
  assert.match(p.root().textContent, /Sign in to see a unit's inventory/);
  assert.ok(!/iron_sword/.test(p.root().textContent));
  p.say(ok(payload()));
  await p.poll();
  p.say({ ok: false, status: 404, data: { error: 'no such unit' } });
  await p.poll();
  assert.match(p.root().textContent, /not in the crew roster/);
});

test('a stale page paints afresh when a unit it had dropped comes back', async () => {
  // After a 401 the page is gone, so the next good read builds it whole and
  // not as a patch of nodes that no longer exist.
  const p = scripted(ok(payload({ inventory: [item(0, 'iron_sword')] })));
  await p.mount();
  p.say({ ok: false, status: 401, data: null });
  await p.poll();
  p.say(ok(payload({ inventory: [item(0, 'iron_sword')] })));
  await p.poll();
  assert.equal(filled(slotsOf(p.root(), 'dt-hotbar')).length, 1);
});

// ---- the item sheet and the clock ----

test('the item sheet is read once per mount and once more for another unit', async () => {
  const sheet = { items: { 'minecraft:iron_sword': [0, 0, .25, .25] }, placeholder: [.75, .75, 1, 1] };
  const p = scripted(ok(payload({ inventory: [item(0, 'iron_sword')] })), { sheet });
  await p.mount();
  await p.poll();
  await p.poll();
  assert.equal(p.calls.length, 3, 'three detail reads');
  assert.deepEqual(p.fetched, ['/units/crew-03/v1/camera/items.json'], 'one read of the sheet');
  assert.ok(one(p.root(), 'dt-icon'), 'the sheet is used for the pictures');
  p.ui.unmount();
  await p.mount('crew-04');
  assert.deepEqual(p.fetched.slice(1), ['/units/crew-04/v1/camera/items.json']);
});

test('without a sheet the page still draws, with text for the pictures', async () => {
  const p = scripted(ok(payload({ inventory: [item(0, 'iron_sword')] })));
  await p.mount();
  assert.equal(one(p.root(), 'dt-icon'), null);
  assert.ok(one(p.root(), 'dt-noicon'));
});

test('the header keeps the save age on a live unit', async () => {
  const p = scripted(ok(payload()));
  await p.mount();
  assert.equal(one(p.root(), 'dt-fresh').textContent, 'live · save 3h ago');
  assert.ok(classesOf(one(p.root(), 'dt-fresh')).includes('live'));
  p.say(ok(payload({ live: { state: 'known', asOf: iso(Date.now() - 5 * 60_000) } })));
  await p.poll();
  assert.match(one(p.root(), 'dt-fresh').textContent, /^last heard 5 min ago · save 3h ago$/);
  assert.ok(classesOf(one(p.root(), 'dt-fresh')).includes('stale'));
});

test('a tab that becomes visible reads at once; a hidden one does not poll', async () => {
  const p = scripted(ok(payload()));
  await p.mount();
  assert.equal(p.calls.length, 1);
  doc.visibilityState = 'hidden';
  await p.poll();
  assert.equal(p.calls.length, 1, 'no read while hidden');
  doc.visibilityState = 'visible';
  doc.fire('visibilitychange');
  await settle();
  assert.equal(p.calls.length, 2, 'read the moment it came back');
  doc.visibilityState = 'hidden';
  doc.fire('visibilitychange');
  await settle();
  assert.equal(p.calls.length, 2, 'going hidden reads nothing');
  p.ui.unmount();
  doc.visibilityState = 'visible';
  doc.fire('visibilitychange');
  await settle();
  assert.equal(p.calls.length, 2, 'an unmounted page does not listen');
});

// ---- armour and held ----

const labelsOf = (root, grid) => slotsOf(root, grid).map(s => s.getAttribute('aria-label'));

test('armour: an off hand with no main hand stays in the off hand cell', async () => {
  const p = scripted(ok(payload({ armor: [item(0, 'iron_helmet')], offhand: item(0, 'shield') })));
  await p.mount();
  const labels = labelsOf(p.root(), 'dt-armourgrid');
  assert.equal(labels.length, 6, 'four worn, two hands, always');
  assert.equal(labels[0], 'head: iron_helmet');
  assert.equal(labels[4], 'main hand: empty', 'the empty main hand is its own empty cell');
  assert.equal(labels[5], 'off hand: shield');
  assert.ok(classesOf(slotsOf(p.root(), 'dt-armourgrid')[4]).includes('empty'));
  assert.ok(classesOf(slotsOf(p.root(), 'dt-armourgrid')[5]).includes('filled'));
});

test('armour: every cell names its place to the eye and to a screen reader', async () => {
  const p = scripted(ok(payload({
    armor: [item(0, 'iron_helmet'), item(1, 'iron_chestplate'), item(3, 'iron_boots')],
    mainhand: item(0, 'diamond_sword'), offhand: item(0, 'shield'),
  })));
  await p.mount();
  assert.deepEqual(labelsOf(p.root(), 'dt-armourgrid'), [
    'head: iron_helmet', 'chest: iron_chestplate', 'legs: empty', 'feet: iron_boots',
    'main hand: diamond_sword', 'off hand: shield',
  ]);
  assert.deepEqual(byClass(one(p.root(), 'dt-armourgrid'), 'dt-cap').map(c => c.textContent), ['head', 'chest', 'legs', 'feet', 'main', 'off']);
  // The hover says the same as the spoken label.
  assert.equal(slotsOf(p.root(), 'dt-armourgrid')[2].title, 'legs: empty');
  assert.equal(slotsOf(p.root(), 'dt-armourgrid')[4].title, 'main hand: diamond_sword');
});

test('armour: nothing worn and nothing held says so in words', async () => {
  const p = scripted(ok(payload()));
  await p.mount();
  assert.equal(one(p.root(), 'dt-armourgrid'), null);
  assert.match(p.root().textContent, /no armour worn and nothing held/);
});

// ---- roles and picture stand-ins ----

test('no slot grid claims to be an ARIA grid without rows; each is a labelled group', async () => {
  const chest = new Array(27).fill(null);
  chest[3] = item(3, 'diamond');
  const p = scripted(ok(payload({ inventory: [item(0, 'bread'), item(9, 'oak_log')], mainhand: item(0, 'diamond_sword'), enderChest: chest })));
  await p.mount();
  assert.equal(all(p.root(), n => n.getAttribute('role') === 'grid').length, 0);
  const groups = all(p.root(), n => n.getAttribute('role') === 'group');
  assert.deepEqual(groups.map(g => g.getAttribute('aria-label')).sort(), ['Armour and held', 'Ender chest', 'Hotbar', 'Main inventory']);
  // One tab stop per group: a slot each with tabindex 0, the rest roved off.
  for (const g of groups) assert.equal(byClass(g, 'dt-slot').filter(s => s.tabIndex === 0).length, 1, g.getAttribute('aria-label'));
});

test('a missing picture is told by words that tell items apart', async () => {
  const p = scripted(ok(payload({ inventory: [item(0, 'iron_ingot'), item(1, 'iron_pickaxe'), item(2, 'apple'), item(3, 'minecraft:oak_planks')] })));
  await p.mount();
  const stubs = slotsOf(p.root(), 'dt-hotbar').slice(0, 4).map(s => one(s, 'dt-noicon'));
  const texts = stubs.map(n => n.textContent);
  assert.equal(new Set(texts).size, 4, `all distinct: ${JSON.stringify(texts)}`);
  assert.deepEqual(texts, ['iron\ningot', 'iron\npickax', 'apple', 'oak\nplanks']);
  assert.ok(stubs.every(n => n.getAttribute('aria-hidden') === 'true'), 'the label speaks; the stub does not repeat it');
});

test('an empty slot says which grid and, in the chest, which slot it is', async () => {
  const chest = new Array(27).fill(null);
  chest[0] = item(0, 'diamond');
  const p = scripted(ok(payload({ inventory: [item(0, 'bread')], enderChest: chest })));
  await p.mount();
  assert.equal(labelsOf(p.root(), 'dt-hotbar')[3], 'Hotbar: empty');
  assert.equal(labelsOf(p.root(), 'dt-main')[0], 'Main inventory: empty');
  assert.equal(labelsOf(p.root(), 'dt-chest')[1], 'slot 2: empty');
  assert.equal(labelsOf(p.root(), 'dt-chest')[0], 'diamond');
});

const HISTORY = {
  unit: 'crew-03', truncated: false,
  net: [{ item: 'minecraft:iron_ingot', delta: 4 }, { item: 'minecraft:coal', delta: -2 }],
  entries: [{ at: iso(NOW - 60_000), changes: [{ item: 'minecraft:iron_ingot', from: 0, to: 4 }, { item: 'minecraft:coal', from: 5, to: 3 }] }],
};

test('the inventory history shows the net and each change, below the sections', async () => {
  const p = scripted(ok(payload()), { history: HISTORY });
  await p.mount();
  const panel = one(p.root(), 'dt-hist');
  assert.ok(panel, 'the panel is on the page');
  const text = panel.textContent;
  assert.match(text, /iron ingot/);
  assert.match(text, /\+4/);
  assert.match(text, /coal −2 \(5 → 3\)/);
  assert.match(text, /1 change recorded in the last 7 days/);
  const page = one(p.root(), 'dt-page');
  assert.equal(page.children[page.children.length - 1], panel, 'the panel closes the page');
});

test('a signed-out history read says to sign in, and a failed one says it failed', async () => {
  let p = scripted(ok(payload()), { history: 401 });
  await p.mount();
  assert.match(one(p.root(), 'dt-hist').textContent, /Sign in to see what this unit gained and lost/);
  p = scripted(ok(payload()));
  await p.mount();
  assert.match(one(p.root(), 'dt-hist').textContent, /could not be read/);
});
