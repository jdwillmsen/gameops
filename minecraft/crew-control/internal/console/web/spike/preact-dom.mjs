// The node API Preact needs on top of graph/fake-dom.mjs. This is the extra
// test infrastructure option C costs; option B runs on the fake DOM unchanged.
import { FakeEl, FakeText } from '../graph/fake-dom.mjs';

const define = (proto, props) => { for (const [k, d] of Object.entries(props)) Object.defineProperty(proto, k, { configurable: true, ...d }); };
const sibling = (n, step) => { const kids = n.parent ? n.parent.children : []; return kids[kids.indexOf(n) + step] || null; };

define(FakeEl.prototype, {
  nodeType: { get: () => 1 },
  nodeName: { get() { return this.tag.toUpperCase(); } },
  localName: { get() { return this.tag; } },
  childNodes: { get() { return this.children; } },
  nextSibling: { get() { return sibling(this, 1); } },
  previousSibling: { get() { return sibling(this, -1); } },
  ownerDocument: { get() { return this.doc; } },
});
FakeEl.prototype.appendChild = function appendChild(n) { this.append(n); return n; };
FakeEl.prototype.insertBefore = function insertBefore(n, ref) {
  if (!ref) return this.appendChild(n);
  const [m] = this.adopt([n]);
  this.children.splice(this.children.indexOf(ref), 0, m);
  return n;
};
FakeEl.prototype.hasAttribute = function hasAttribute(k) { return k in this.attrs; };

define(FakeText.prototype, {
  nodeType: { get: () => 3 },
  data: { get() { return this.text; }, set(v) { this.text = String(v); } },
  nextSibling: { get() { return sibling(this, 1); } },
  parentNode: { get() { return this.parent; } },
});
FakeText.prototype.remove = function remove() {
  if (this.parent) this.parent.children = this.parent.children.filter(c => c !== this);
  this.parent = null;
};

export function patchDocument(doc) {
  doc.createTextNode = t => new FakeText(t);
}

// Preact installs one shared listener per event type and reads the handler off
// `this`, which the fake dispatch does not bind.
FakeEl.prototype.dispatch = function dispatch(type, init = {}) {
  const ev = {
    type, target: this, defaultPrevented: false, stopped: false,
    preventDefault() { this.defaultPrevented = true; },
    stopPropagation() { this.stopped = true; },
    ...init,
  };
  for (let x = this; x && !ev.stopped; x = x.parent) {
    for (const fn of (x.listeners && x.listeners[type]) || []) fn.call(x, ev);
  }
  return ev;
};
