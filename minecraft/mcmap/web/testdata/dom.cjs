'use strict';

// Just enough of a page for the panel's script and the script that keeps
// the record to run in, so that what they do can be checked by doing it
// and not by reading them. Nothing is laid out or drawn: an element is its
// children, its attributes and its listeners.
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');

class Text {
  constructor(text) { this.nodeType = 3; this.textContent = String(text); this.parentNode = null; }
  remove() { if (this.parentNode) this.parentNode.removeChild(this); }
}

// One simple selector at a time: a tag, classes, [hidden], :hover (never)
// and :not(:disabled); a space between two means anywhere under.
function matchesOne(el, simple) {
  const parts = simple.match(/^[a-z]+|\.[\w-]+|\[hidden\]|:not\(:disabled\)|:hover/g) || [];
  if (parts.join('') !== simple) throw new Error(`the stub does not read the selector ${simple}`);
  return parts.every((part) => {
    if (part === '[hidden]') return el.hidden === true;
    if (part === ':hover') return false;
    if (part === ':not(:disabled)') return el.disabled !== true;
    if (part[0] === '.') return el.classList.contains(part.slice(1));
    return el.tagName === part.toUpperCase();
  });
}

function matches(el, selector) {
  return selector.split(',').some((one) => {
    const steps = one.trim().split(/\s+/);
    if (!matchesOne(el, steps[steps.length - 1])) return false;
    let at = el.parentNode;
    for (let i = steps.length - 2; i >= 0; i--) {
      while (at && !(at instanceof Element && matchesOne(at, steps[i]))) at = at.parentNode;
      if (!at) return false;
      at = at.parentNode;
    }
    return true;
  });
}

class Element {
  constructor(doc, tag) {
    this.ownerDocument = doc;
    this.nodeType = 1;
    this.tagName = tag.toUpperCase();
    this.childNodes = [];
    this.parentNode = null;
    this.attributes = new Map();
    this.listeners = new Map();
    this.hidden = false;
    this.disabled = false;
    this.tabIndex = tag === 'button' || tag === 'input' ? 0 : -1;
    this.dataset = {};
    this.value = '';
    this.title = '';
    const classes = new Set();
    this.classList = {
      add: (...names) => names.forEach((name) => classes.add(name)),
      remove: (...names) => names.forEach((name) => classes.delete(name)),
      contains: (name) => classes.has(name),
      toggle: (name, on) => {
        const want = on === undefined ? !classes.has(name) : Boolean(on);
        if (want) classes.add(name); else classes.delete(name);
        return want;
      },
      get value() { return [...classes].join(' '); },
      set: (text) => { classes.clear(); String(text).split(/\s+/).filter(Boolean).forEach((name) => classes.add(name)); },
    };
    const props = new Map();
    this.style = { setProperty: (name, value) => props.set(name, value), getPropertyValue: (name) => props.get(name) || '', removeProperty: (name) => props.delete(name) };
  }

  get className() { return this.classList.value; }
  set className(text) { this.classList.set(text); }
  get children() { return this.childNodes.filter((node) => node instanceof Element); }
  get firstChild() { return this.childNodes[0] || null; }
  get parentElement() { return this.parentNode instanceof Element ? this.parentNode : null; }
  get isConnected() {
    let at = this;
    while (at.parentNode) at = at.parentNode;
    return at === this.ownerDocument.documentElement;
  }
  // Shown at all: on the page, with nothing it is in hidden.
  get offsetParent() {
    if (!this.isConnected) return null;
    for (let at = this; at; at = at.parentNode) if (at.hidden) return null;
    return this.parentNode;
  }
  get offsetWidth() { return 0; }
  get offsetHeight() { return 0; }
  get clientHeight() { return 600; }
  get textContent() { return this.childNodes.map((node) => node.textContent).join(''); }
  set textContent(text) {
    this.replaceChildren();
    if (String(text) !== '') this.append(String(text));
  }

  setAttribute(name, value) {
    if (name === 'class') this.className = value;
    this.attributes.set(name, String(value));
  }
  getAttribute(name) { return this.attributes.has(name) ? this.attributes.get(name) : null; }
  hasAttribute(name) { return this.attributes.has(name); }
  removeAttribute(name) { this.attributes.delete(name); }

  adopt(node) {
    const child = node instanceof Element || node instanceof Text ? node : new Text(node);
    if (child.parentNode) child.parentNode.removeChild(child);
    child.parentNode = this;
    return child;
  }
  removeChild(node) {
    const at = this.childNodes.indexOf(node);
    if (at >= 0) this.childNodes.splice(at, 1);
    node.parentNode = null;
    // Focus in what has left the page is focus nowhere.
    const doc = this.ownerDocument;
    if (doc.activeElement && node instanceof Element && (node === doc.activeElement || node.contains(doc.activeElement))) doc.activeElement = doc.body;
  }
  append(...nodes) { for (const node of nodes) this.childNodes.push(this.adopt(node)); }
  prepend(...nodes) { nodes.reverse().forEach((node) => this.childNodes.unshift(this.adopt(node))); }
  insertBefore(node, before) {
    const child = this.adopt(node);
    const at = before ? this.childNodes.indexOf(before) : -1;
    if (at < 0) this.childNodes.push(child); else this.childNodes.splice(at, 0, child);
    return child;
  }
  after(node) { this.parentNode.insertBefore(node, this.parentNode.childNodes[this.parentNode.childNodes.indexOf(this) + 1] || null); }
  replaceChildren(...nodes) {
    for (const node of [...this.childNodes]) this.removeChild(node);
    this.append(...nodes);
  }
  remove() { if (this.parentNode) this.parentNode.removeChild(this); }
  contains(node) {
    for (let at = node; at; at = at.parentNode) if (at === this) return true;
    return false;
  }

  matches(selector) { return matches(this, selector); }
  closest(selector) {
    for (let at = this; at instanceof Element; at = at.parentNode) if (matches(at, selector)) return at;
    return null;
  }
  querySelectorAll(selector) {
    const out = [];
    const walk = (node) => {
      for (const child of node.children) {
        if (matches(child, selector)) out.push(child);
        walk(child);
      }
    };
    walk(this);
    return out;
  }
  querySelector(selector) { return this.querySelectorAll(selector)[0] || null; }

  addEventListener(type, fn) {
    if (!this.listeners.has(type)) this.listeners.set(type, []);
    this.listeners.get(type).push(fn);
  }
  dispatchEvent(event) {
    if (!event.target) event.target = this;
    let stopped = false;
    event.stopPropagation = () => { stopped = true; };
    event.preventDefault = () => { event.defaultPrevented = true; };
    for (let at = this; at && !stopped; at = at.parentNode || (at === this.ownerDocument.documentElement ? this.ownerDocument : null)) {
      for (const fn of (at.listeners && at.listeners.get(event.type)) || []) fn(event);
      if (at === this.ownerDocument) break;
    }
    return !event.defaultPrevented;
  }
  click() { if (!this.disabled) this.dispatchEvent({ type: 'click' }); }
  focus() {
    if (this.offsetParent === null) return;
    this.ownerDocument.activeElement = this;
    this.dispatchEvent({ type: 'focusin' });
  }
  getBoundingClientRect() { return { left: 0, top: 0, right: 0, bottom: 0, width: 0, height: 0 }; }
  setPointerCapture() {}
  scrollIntoView() {}
}

// A page as it is sent, as far as the two scripts look at it, with what
// the browser keeps given from outside so that one load can follow another.
function page(storage, { compact = false, files = ['settings.js', 'layers.js'], days = 0 } = {}) {
  // The day it is, for what is only done after some have passed.
  const clock = class extends Date { static now() { return Date.now() + days * 86_400_000; } };
  const doc = { listeners: new Map(), activeElement: null };
  doc.createElement = (tag) => new Element(doc, tag);
  doc.createElementNS = (ns, tag) => new Element(doc, tag);
  doc.addEventListener = Element.prototype.addEventListener.bind(doc);
  doc.dispatchEvent = (event) => { for (const fn of doc.listeners.get(event.type) || []) fn(event); };
  doc.documentElement = doc.createElement('html');
  doc.body = doc.createElement('body');
  doc.documentElement.append(doc.body);
  doc.activeElement = doc.body;
  const byId = new Map();
  const add = (parent, tag, id, className) => {
    const node = doc.createElement(tag);
    if (id) { node.id = id; byId.set(id, node); }
    if (className) node.className = className;
    parent.append(node);
    return node;
  };
  const stage = add(doc.body, 'div', '', 'stage');
  const panel = add(stage, 'aside', 'layers', 'layers');
  add(panel, 'button', 'layers-toggle');
  add(panel, 'div', 'layers-body');
  add(stage, 'main', 'map');
  doc.getElementById = (id) => byId.get(id) || null;

  const media = (query) => ({ matches: compact && /max-width: 720px/.test(query), addEventListener() {} });
  const win = {
    document: doc,
    localStorage: {
      getItem: (key) => (storage.has(key) ? storage.get(key) : null),
      setItem: (key, value) => { storage.set(key, String(value)); },
    },
    matchMedia: media,
    getComputedStyle: () => ({ getPropertyValue: () => '' }),
    addEventListener() {},
    requestAnimationFrame: (fn) => setTimeout(fn, 0),
    cancelAnimationFrame: (id) => clearTimeout(id),
    innerWidth: 1440,
    innerHeight: 900,
    console,
    // Nothing here is worth waiting for: a wait of seconds is a moment.
    setTimeout: (fn, ms) => setTimeout(fn, Math.min(ms || 0, 5)),
    clearTimeout,
    Date: clock,
    Node: Element,
    Element,
    HTMLInputElement: class {},
    CustomEvent: class { constructor(type, init) { this.type = type; this.detail = init ? init.detail : undefined; } },
    PointerEvent: class {},
  };
  win.window = win;
  win.mcmap = { layers: {} };
  vm.createContext(win);
  for (const file of files) vm.runInContext(fs.readFileSync(path.join(__dirname, '..', file), 'utf8'), win, { filename: file });
  return { win, doc, panel, layers: win.mcmap.layers, settings: win.mcmapSettings };
}

module.exports = { page, Element };
