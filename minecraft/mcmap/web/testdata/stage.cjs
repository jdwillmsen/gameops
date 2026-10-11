'use strict';

// A map to run the page's drawing scripts on: the page dom.cjs makes, with
// just enough of Leaflet, of a canvas and of the server for the script
// that keeps the pictures, the one that finds the middle of the map and
// the live layer to run as they do in a browser. Nothing is painted: a
// canvas remembers what was drawn on it and where, through whatever
// transform it was under, which is what the checks read.
const vm = require('node:vm');
const fs = require('node:fs');
const path = require('node:path');
const { page } = require('./dom.cjs');

// A drawing context that keeps its transform and a list of what was drawn,
// in the canvas's own pixels.
function context(canvas) {
  let t = { a: 1, d: 1, e: 0, f: 0 };
  const stack = [];
  const ctx = {
    canvas,
    drawn: [],
    arcs: [],
    save() { stack.push({ ...t }); },
    restore() { if (stack.length) t = stack.pop(); },
    translate(x, y) { t.e += t.a * x; t.f += t.d * y; },
    scale(x, y) { t.a *= x; t.d *= y; },
    setTransform(a, b, c, d, e, f) { t = { a, d, e, f }; },
    getTransform() { return { ...t }; },
    drawImage(image, x, y, w = image.width, h = image.height) {
      ctx.drawn.push({ image, x: t.a * x + t.e, y: t.d * y + t.f, w: t.a * w, h: t.d * h });
    },
    arc(x, y, r) { ctx.arcs.push({ x: t.a * x + t.e, y: t.d * y + t.f, r: t.a * r }); },
    measureText: (text) => ({ width: String(text).length * 7 }),
  };
  // Everything else a script may call on a context is drawing this does not look at.
  return new Proxy(ctx, { get: (target, name) => (name in target ? target[name] : () => {}), set: (target, name, value) => { target[name] = value; return true; } });
}

// The few classes of Leaflet the scripts build on, as plain objects.
function leaflet(win) {
  const latLng = (a, b) => (Array.isArray(a) ? { lat: a[0], lng: a[1] } : a && typeof a === 'object' ? { lat: a.lat, lng: a.lng } : { lat: a, lng: b });
  const point = (x, y) => {
    const p = Array.isArray(x) ? { x: x[0], y: x[1] } : typeof x === 'object' ? { x: x.x, y: x.y } : { x, y };
    p.subtract = (o) => { const q = point(o); return point(p.x - q.x, p.y - q.y); };
    p.add = (o) => { const q = point(o); return point(p.x + q.x, p.y + q.y); };
    return p;
  };
  class Events {
    on(types, fn) {
      this.heard = this.heard || new Map();
      for (const type of types.split(' ')) this.heard.set(type, [...(this.heard.get(type) || []), fn]);
      return this;
    }
    off() { return this; }
    fire(type, data = {}) {
      for (const fn of (this.heard && this.heard.get(type)) || []) fn({ type, ...data });
      return this;
    }
  }
  class CircleMarker extends Events {
    constructor(at, options = {}) {
      super();
      this.options = { ...options };
      this._latlng = latLng(at);
      this._radius = this.options.radius || 10;
      this._renderer = this.options.renderer || null;
      this._point = point(0, 0);
      this.moves = 0;
    }
    static extend(props) {
      const Sub = class extends this {};
      Object.assign(Sub.prototype, props);
      return Sub;
    }
    setLatLng(at) { this._latlng = latLng(at); this.moves += 1; return this.redraw(); }
    getLatLng() { return this._latlng; }
    setRadius(r) { this._radius = r; return this.redraw(); }
    getRadius() { return this._radius; }
    setStyle(style) { Object.assign(this.options, style); return this; }
    redraw() { return this; }
    bindTooltip(content, options) { this.tip = { content, options }; return this; }
    getTooltip() { return this.tip || null; }
    addTo(map) { map.layers.add(this); this._map = map; return this; }
    remove() { if (this._map) this._map.layers.delete(this); this._map = null; return this; }
    bringToFront() { return this; }
    onAdd() {}
    onRemove() {}
    _empty() { return false; }
    _clickTolerance() { return 0; }
    _updateBounds() { this._pxBounds = { extend() {} }; }
    _containsPoint() { return false; }
    // Leaflet's own circle, for a marker with no picture.
    _updatePath() { if (this._renderer && this._renderer._drawing) this._renderer._ctx.arc(this._point.x, this._point.y, this._radius, 0, Math.PI * 2); }
  }
  class Group extends Events {
    constructor() { super(); this.held = new Set(); }
    addLayer(layer) { this.held.add(layer); return this; }
    removeLayer(layer) { this.held.delete(layer); return this; }
    hasLayer(layer) { return this.held.has(layer); }
    clearLayers() { this.held.clear(); return this; }
    eachLayer(fn) { [...this.held].forEach(fn); return this; }
    getLayers() { return [...this.held]; }
    bindTooltip() { return this; }
    isTooltipOpen() { return false; }
    addTo(map) { map.layers.add(this); return this; }
  }
  // A map of one block to the pixel at zoom 0, its box the page's #map.
  class Sheet extends Events {
    constructor() {
      super();
      this.layers = new Set();
      this.zoom = 0;
      this.centre = latLng(0, 0);
      this.options = { maxBounds: null };
      this.went = [];
      this._animatingZoom = false;
    }
    getZoom() { return this.zoom; }
    getCenter() { return this.centre; }
    getContainer() { return win.document.getElementById('map'); }
    project(at, zoom = this.zoom) { const p = latLng(at); return point(p.lng * 2 ** zoom, p.lat * 2 ** zoom); }
    unproject(p, zoom = this.zoom) { return latLng(p.y / 2 ** zoom, p.x / 2 ** zoom); }
    _limitZoom(zoom) { return Math.max(-6, Math.min(3, zoom)); }
    panTo(at) { this.centre = latLng(at); this.went.push({ how: 'panTo', centre: this.centre, zoom: this.zoom }); return this; }
    setView(at, zoom) { this.centre = latLng(at); this.zoom = zoom; this.went.push({ how: 'setView', centre: this.centre, zoom }); return this; }
    setZoom(zoom) { this.zoom = zoom; this.went.push({ how: 'setZoom', zoom }); return this; }
    setZoomAround(at, zoom) { this.zoom = zoom; this.went.push({ how: 'setZoomAround', at, zoom }); return this; }
    invalidateSize() { this.sized = (this.sized || 0) + 1; return this; }
    hasLayer(layer) { return this.layers.has(layer); }
    eachLayer(fn) { [...this.layers].forEach(fn); return this; }
    // Where a place is on the screen, by the map's centre and its box.
    onScreen(at) {
      const box = this.getContainer().getBoundingClientRect();
      const p = this.project(at);
      const c = this.project(this.centre);
      return { x: (box.left + box.right) / 2 + p.x - c.x, y: (box.top + box.bottom) / 2 + p.y - c.y };
    }
  }
  const map = new Sheet();
  const L = {
    Browser: { retina: win.devicePixelRatio > 1 },
    CircleMarker,
    circleMarker: (at, options) => new CircleMarker(at, options),
    featureGroup: () => new Group(),
    canvas: (options) => {
      const container = win.document.createElement('canvas');
      return { options, _container: container, _ctx: container.getContext('2d'), _drawing: false, _map: map, _zoom: map.zoom, redraws: 0, _redraw() { this.redraws += 1; } };
    },
    latLng,
    latLngBounds: () => ({ extend() {}, isValid: () => false }),
    point,
    bounds: () => ({ extend() {} }),
    DomEvent: { stopPropagation() {} },
  };
  return { L, map };
}

// The pictures a server would list, and how large each is.
const SIDES = { 'face/villager_v2': [11, 11], 'face/fox': [8, 8], 'face/shulker': [16, 16], 'face/cow': [8, 8], 'face/zombie': [8, 8], 'container/chest': [16, 16], 'block/chest': [32, 32], 'structure/village': [16, 16], 'marker/waypoint': [16, 16] };

// A page with the scripts run on it. dpr is the screen's density, heads
// the players the server has a head for, and boxes what getBoundingClientRect
// answers for the map and for what lies over it.
async function stage({ dpr = 1, look = {}, heads = {}, boxes: headBoxes = {}, files = ['names.js', 'duration.js', 'icons.js', 'layers.js', 'room.js', 'live.js'], storage = new Map() } = {}) {
  if (Object.keys(look).length > 0) storage.set('mcmap.settings', JSON.stringify({ v: 1, look }));
  const p = page(storage, { files: ['settings.js'] });
  const { win, doc } = p;
  const made = doc.createElement;
  const canvases = [];
  doc.createElement = (tag) => {
    const node = made(tag);
    if (tag === 'canvas') {
      canvases.push(node);
      node.width = 0;
      node.height = 0;
      const ctx = context(node);
      node.getContext = () => ctx;
    }
    return node;
  };
  // Every element a script asks for by id is there, as it is on the page.
  const found = doc.getElementById;
  const stageNode = doc.getElementById('map').parentNode;
  const asked = new Map();
  doc.getElementById = (id) => {
    const node = found(id) || asked.get(id);
    if (node) return node;
    const fresh = doc.createElement(/picture/.test(id) ? 'canvas' : 'div');
    fresh.id = id;
    stageNode.append(fresh);
    asked.set(id, fresh);
    return fresh;
  };
  const boxes = { '#map': { left: 0, top: 0, right: 1000, bottom: 600 } };
  doc.getElementById('map').getBoundingClientRect = () => boxes['#map'];
  doc.querySelector = (selector) => (boxes[selector] ? { getBoundingClientRect: () => boxes[selector], getClientRects: () => [boxes[selector]] } : null);
  doc.querySelectorAll = () => [];
  doc.hidden = false;
  doc.documentElement.toggleAttribute = function (name, on) { if (on) this.setAttribute(name, ''); else this.removeAttribute(name); };

  const sources = [];
  Object.assign(win, {
    devicePixelRatio: dpr,
    performance: { now: () => Date.now() },
    navigator: {},
    setInterval: () => 0,
    clearInterval() {},
    createImageBitmap: async (blob) => {
      const key = Object.keys(SIDES).find((k) => blob.includes(`/${k}?`));
      const [width, height] = key ? SIDES[key] : [8, 8];
      return { width, height };
    },
    fetch: async (address) => {
      if (address === 'api/icons') {
        return { ok: true, status: 200, text: async () => JSON.stringify({ mobs: { version: 'm1', types: ['cow', 'zombie', 'villager_v2', 'fox', 'shulker'] }, pictures: { version: 'p1', keys: Object.keys(SIDES), boxes: headBoxes }, names: { version: '' }, heads, me: '' }) };
      }
      if (address.startsWith('api/icons/')) return { ok: true, status: 200, blob: async () => address };
      return { ok: false, status: 404, json: async () => ({}), text: async () => '' };
    },
    EventSource: class {
      constructor(url) { this.url = url; this.readyState = 1; sources.push(this); }
      close() { this.readyState = 2; }
      static get CLOSED() { return 2; }
      static get OPEN() { return 1; }
    },
    DOMMatrixReadOnly: class { constructor(said) { this.a = Number(/matrix\(([^,]+)/.exec(said)[1]); } },
  });
  // How far each canvas says it is stretched, for a script that asks the browser.
  const stretched = new Map();
  win.getComputedStyle = (node) => ({ getPropertyValue: () => '', transform: stretched.has(node) ? `matrix(${stretched.get(node)}, 0, 0, ${stretched.get(node)}, 0, 0)` : 'none' });
  const frames = [];
  win.requestAnimationFrame = (fn) => frames.push(fn);
  const { L, map } = leaflet(win);
  win.L = L;
  Object.assign(win.mcmap, { map, dimension: () => 'overworld', dimensions: () => ['overworld'], live: () => true, reload() {}, label: (id) => id });
  for (const file of files) vm.runInContext(fs.readFileSync(path.join(__dirname, '..', file), 'utf8'), win, { filename: file });

  const tick = () => new Promise((done) => setTimeout(done, 0));
  // Runs what was asked for the next frame, and whatever that asks for in turn up to a point.
  const frame = () => { for (const fn of frames.splice(0)) fn(); };
  const settle = async () => { for (let n = 0; n < 6; n += 1) { await tick(); frame(); } };
  doc.dispatchEvent({ type: 'mcmap:view' });
  await settle();
  return {
    win, doc, map, L, boxes, stretched, frame, settle, tick,
    // Every canvas a script has made, in the order made.
    made: canvases,
    app: win.mcmap,
    icons: win.mcmap.icons,
    settings: win.mcmapSettings,
    // The stream the live layer has open, and a frame sent down it.
    send: async (frameOf) => {
      const es = sources.filter((s) => s.readyState === 1 && s.onmessage).at(-1);
      es.onmessage({ data: JSON.stringify({ serverNow: new Date().toISOString(), at: new Date().toISOString(), ttlSeconds: 10, players: [], mobs: [], ...frameOf }) });
      await settle();
    },
  };
}

module.exports = { stage, context };
