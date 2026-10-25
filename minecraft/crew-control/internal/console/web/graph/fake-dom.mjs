// A small fake DOM for the graph tests. jsdom is not a dependency, so this
// carries only the slice graph.js and jobs-ui.js touch: an element tree (HTML
// and SVG alike), attributes and classes, bubbling events that can be stopped,
// focus that is lost when a node leaves the tree, pointer capture and the
// window and observer globals. It is not a browser: layout, hit testing and
// CSS are left to the end-to-end run.

export class FakeText {
  constructor(text) { this.text = String(text); this.parent = null; }
  get textContent() { return this.text; }
}

const classList = e => String(e.attrs.class || '').split(/\s+/).filter(Boolean);

export class FakeEl {
  constructor(doc, tag) {
    this.doc = doc;
    this.tag = tag;
    this.children = [];
    this.parent = null;
    this.attrs = {};
    this.listeners = {};
    this.hidden = false;
    this.disabled = false;
    this.title = '';
    this.tabIndex = 0;
    this.clientWidth = 0;
    this.clientHeight = 0;
    this.captured = new Set();
    this.style = { setProperty(k, v) { this[k] = v; } };
    this.dataset = {};
  }
  get className() { return this.attrs.class || ''; }
  set className(v) { this.attrs.class = String(v); }
  get role() { return this.attrs.role ?? null; }
  set role(v) { this.attrs.role = String(v); }
  get id() { return this.attrs.id || ''; }
  get classList() {
    const self = this;
    return {
      contains: c => classList(self).includes(c),
      add: c => { if (!classList(self).includes(c)) self.attrs.class = [...classList(self), c].join(' '); },
      remove: c => { self.attrs.class = classList(self).filter(x => x !== c).join(' '); },
      toggle(c, force) {
        const on = force === undefined ? !this.contains(c) : !!force;
        if (on) this.add(c); else this.remove(c);
        return on;
      },
    };
  }
  setAttribute(k, v) { this.attrs[k] = String(v); }
  getAttribute(k) { return k in this.attrs ? this.attrs[k] : null; }
  removeAttribute(k) { delete this.attrs[k]; }
  get textContent() { return this.children.map(c => c.textContent).join(''); }
  set textContent(v) { this.replaceChildren(new FakeText(v)); }
  get parentNode() { return this.parent; }
  get firstChild() { return this.children[0] || null; }
  get isConnected() {
    for (let x = this; x; x = x.parent) if (x === this.doc.body) return true;
    return false;
  }
  adopt(kids) {
    return kids.map(k => {
      const n = typeof k === 'string' ? new FakeText(k) : k;
      if (n.parent) n.parent.children = n.parent.children.filter(c => c !== n);
      n.parent = this;
      return n;
    });
  }
  append(...kids) { this.children.push(...this.adopt(kids)); }
  // Replacing detaches every old child first, so a node that is put straight
  // back loses focus exactly as it does in a browser.
  replaceChildren(...kids) {
    for (const c of this.children) { this.doc.detached(c); c.parent = null; }
    this.children = [];
    this.append(...kids);
  }
  removeChild(node) { node.remove(); }
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
  matches(sel) {
    const attr = /^\[([\w-]+)\]$/.exec(sel);
    if (attr) return attr[1] in this.attrs;
    if (sel.startsWith('.')) return classList(this).includes(sel.slice(1));
    return this.tag === sel;
  }
  querySelector(sel) { return this.querySelectorAll(sel)[0] || null; }
  querySelectorAll(sel) {
    const out = [];
    const walk = n => { for (const c of n.children) if (c instanceof FakeEl) { if (c.matches(sel)) out.push(c); walk(c); } };
    walk(this);
    return out;
  }
  closest(sel) {
    for (let x = this; x; x = x.parent) if (x instanceof FakeEl && x.matches(sel)) return x;
    return null;
  }
  addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); }
  removeEventListener(type, fn) { this.listeners[type] = (this.listeners[type] || []).filter(f => f !== fn); }
  // Bubbles to the root unless a handler stops it; returns the event so a
  // test can read defaultPrevented.
  dispatch(type, init = {}) {
    const ev = {
      type, target: this, defaultPrevented: false, stopped: false,
      preventDefault() { this.defaultPrevented = true; },
      stopPropagation() { this.stopped = true; },
      ...init,
    };
    for (let x = this; x && !ev.stopped; x = x.parent) {
      for (const fn of (x.listeners && x.listeners[type]) || []) fn(ev);
    }
    return ev;
  }
  click() { return this.dispatch('click'); }
  focus() {
    if (this.doc.activeElement === this) return;
    this.doc.activeElement = this;
    this.dispatch('focusin');
  }
  blur() { if (this.doc.activeElement === this) this.doc.activeElement = null; }
  getBoundingClientRect() { return { left: 0, top: 0, width: this.clientWidth, height: this.clientHeight }; }
  setPointerCapture(id) { this.captured.add(id); }
  hasPointerCapture(id) { return this.captured.has(id); }
  releasePointerCapture(id) {
    if (this.captured.delete(id)) this.dispatch('lostpointercapture', { pointerId: id });
  }
}

export class FakeDocument {
  constructor() {
    this.activeElement = null;
    this.body = new FakeEl(this, 'body');
  }
  createElement(tag) { return new FakeEl(this, tag); }
  createElementNS(_ns, tag) { return new FakeEl(this, tag); }
  // A node leaving the tree takes focus with it, as in a browser.
  detached(node) {
    if (this.activeElement && node instanceof FakeEl && node.contains(this.activeElement)) this.activeElement = null;
  }
}

// Installs the browser globals the modules use and returns the pieces a test
// drives: the document, the window listener log and the scheduled timers.
export function installDom() {
  const doc = new FakeDocument();
  const win = {
    listeners: {},
    addEventListener(type, fn) { (this.listeners[type] ||= []).push(fn); },
    removeEventListener(type, fn) { this.listeners[type] = (this.listeners[type] || []).filter(f => f !== fn); },
    fire(type) { for (const fn of [...(this.listeners[type] || [])]) fn({ type }); },
    count: type => (win.listeners[type] || []).length,
  };
  const observers = new Set();
  globalThis.ResizeObserver = class {
    constructor(fn) { this.fn = fn; }
    observe() { observers.add(this); }
    disconnect() { observers.delete(this); }
  };
  const timers = [];
  globalThis.document = doc;
  globalThis.window = win;
  globalThis.setTimeout = (fn, ms) => { const t = { fn, ms, live: true }; timers.push(t); return t; };
  globalThis.clearTimeout = t => { if (t) t.live = false; };
  return {
    doc, win, observers,
    timers: {
      pending: () => timers.filter(t => t.live),
      // Runs the oldest pending timer and lets its promise chain settle.
      async fire() {
        const t = timers.find(x => x.live);
        if (!t) throw new Error('no timer pending');
        t.live = false;
        t.fn();
        await new Promise(r => setImmediate(r));
        return t.ms;
      },
    },
  };
}

export const settle = () => new Promise(r => setImmediate(r));

export const classesOf = e => classList(e);

export function all(root, pred) {
  const out = [];
  const walk = n => {
    if (!(n instanceof FakeEl)) return;
    if (pred(n)) out.push(n);
    n.children.forEach(walk);
  };
  walk(root);
  return out;
}
export const byClass = (root, cls) => all(root, n => classList(n).includes(cls));
export const one = (root, cls) => byClass(root, cls)[0] || null;
