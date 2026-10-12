// Runs in the page before its own scripts. It changes nothing the page
// does; it keeps a record of what the page draws and is sent, so that a
// check can measure what is on the screen and not what the page says of
// itself. Everything it keeps stays in the page: a check reads numbers out
// of it, never the positions or names themselves.
(() => {
  if (window.__probe) return;
  const P = (window.__probe = { draws: new Map(), arcs: [], truth: {}, streams: [], composed: new Map(), quiet: false });
  const onMap = (c) => c && c.isConnected && c.classList && c.classList.contains('leaflet-zoom-animated');
  const proto = CanvasRenderingContext2D.prototype;

  // Where a picture came from, so that one drawn can be traced to the
  // address it was fetched at: the response a blob was read from, and
  // the blob a bitmap was made of.
  const blobFrom = new WeakMap();
  P.origin = new WeakMap();
  const blob = Response.prototype.blob;
  Response.prototype.blob = async function () {
    const made = await blob.call(this);
    blobFrom.set(made, this.url);
    return made;
  };
  const bitmap = window.createImageBitmap;
  if (bitmap) {
    window.createImageBitmap = async function (source, ...rest) {
      const made = await bitmap.call(window, source, ...rest);
      if (blobFrom.has(source)) P.origin.set(made, blobFrom.get(source));
      return made;
    };
  }

  const drawImage = proto.drawImage;
  proto.drawImage = function (image, ...a) {
    if (!P.quiet) {
      if (onMap(this.canvas) && a.length >= 2) {
        const t = this.getTransform();
        const w = a.length >= 4 ? a[2] : image.width;
        const h = a.length >= 4 ? a[3] : image.height;
        let list = P.draws.get(image);
        if (!list) P.draws.set(image, (list = []));
        list.push({ canvas: this.canvas, x: t.a * a[0] + t.e, y: t.d * a[1] + t.f, w: t.a * w, h: t.d * h, at: performance.now() });
        if (list.length > 4000) list.splice(0, 2000);
      } else if (!this.canvas.isConnected && a.length === 4 && P.origin.has(image)) {
        // A fetched picture set into a canvas that is not on the page:
        // a marker being composed. Where it was put is the measure of
        // whether it sits in the middle.
        P.composed.set(this.canvas, { image, x: a[0], y: a[1], w: a[2], h: a[3], round: this.__round === true, side: this.canvas.width });
      }
    }
    return drawImage.call(this, image, ...a);
  };
  const arc = proto.arc;
  proto.arc = function (x, y, r, ...rest) {
    if (onMap(this.canvas)) {
      const t = this.getTransform();
      this.__arc = { canvas: this.canvas, x: t.a * x + t.e, y: t.d * y + t.f, r: t.a * r };
    } else if (!this.canvas.isConnected) {
      this.__round = true;
    }
    return arc.call(this, x, y, r, ...rest);
  };
  const stroke = proto.stroke;
  proto.stroke = function (...a) {
    if (this.__arc) {
      P.arcs.push({ ...this.__arc, colour: String(this.strokeStyle), width: this.lineWidth, at: performance.now() });
      if (P.arcs.length > 8000) P.arcs.splice(0, 4000);
    }
    return stroke.apply(this, a);
  };
  const beginPath = proto.beginPath;
  proto.beginPath = function () {
    this.__arc = null;
    return beginPath.call(this);
  };
  // Whatever lay under a cleared stretch is no longer painted.
  const forget = (canvas, x0, y0, x1, y1) => {
    for (const list of P.draws.values()) {
      for (let n = list.length - 1; n >= 0; n -= 1) {
        const rec = list[n];
        if (rec.canvas !== canvas) continue;
        const cx = rec.x + rec.w / 2;
        const cy = rec.y + rec.h / 2;
        if (cx >= x0 && cx <= x1 && cy >= y0 && cy <= y1) list.splice(n, 1);
      }
    }
  };
  const clearRect = proto.clearRect;
  proto.clearRect = function (x, y, w, h) {
    if (onMap(this.canvas)) {
      const t = this.getTransform();
      forget(this.canvas, t.a * x + t.e, t.d * y + t.f, t.a * (x + w) + t.e, t.d * (y + h) + t.f);
    }
    return clearRect.call(this, x, y, w, h);
  };
  const width = Object.getOwnPropertyDescriptor(HTMLCanvasElement.prototype, 'width');
  Object.defineProperty(HTMLCanvasElement.prototype, 'width', {
    get() { return width.get.call(this); },
    set(v) {
      if (onMap(this)) forget(this, -Infinity, -Infinity, Infinity, Infinity);
      width.set.call(this, v);
    },
  });

  P.composedList = () => [...P.composed.values()];

  // Every live stream the page opens: when it opened, when it failed, and
  // when each frame came. truth is the newest position sent for each
  // entity of the dimension shown, which a check compares the screen with.
  const Source = window.EventSource;
  window.EventSource = function (url, ...rest) {
    const es = new Source(url, ...rest);
    const record = { opened: [], errors: [], frames: 0, first: null, last: null, gap: 0, began: performance.now(), players: 0, mobs: 0, headed: 0, intervals: new Set() };
    record.state = () => es.readyState;
    P.streams.push(record);
    es.addEventListener('open', () => record.opened.push(performance.now()));
    es.addEventListener('error', () => record.errors.push(performance.now()));
    es.addEventListener('message', (ev) => {
      const now = performance.now();
      if (record.last !== null) {
        record.gap = Math.max(record.gap, now - record.last);
        record.intervals.add(Math.round((now - record.last) / 1000));
      }
      if (record.first === null) record.first = now;
      record.last = now;
      record.frames += 1;
      let frame;
      try { frame = JSON.parse(ev.data); } catch { return; }
      record.players = (frame.players || []).length;
      record.mobs = (frame.mobs || []).length;
      record.most = Math.max(record.most || 0, record.players + record.mobs);
      // Only the stream the map itself is on: a search for a lost player opens others.
      if (!window.mcmap || !String(url).includes(encodeURIComponent(window.mcmap.dimension()))) return;
      P.frame = frame;
      for (const e of frame.players || []) P.truth[`p:${e.i}`] = { x: e.x, z: e.z, at: now };
      for (const e of frame.mobs || []) P.truth[`m:${e.i}`] = { x: e.x, z: e.z, at: now };
    });
    return es;
  };
  Object.assign(window.EventSource, { CONNECTING: 0, OPEN: 1, CLOSED: 2 });
  window.EventSource.prototype = Source.prototype;

  // Where a record of the canvas's device pixels is on the screen now, in
  // CSS pixels, through whatever transform the canvas is under.
  const onScreen = (rec) => {
    const box = rec.canvas.getBoundingClientRect();
    const k = box.width / rec.canvas.width;
    return { x: box.left + rec.x * k, y: box.top + rec.y * k, w: (rec.w || 0) * k, h: (rec.h || 0) * k };
  };
  // Every picture now painted on the map: how wide each is on the screen
  // and how wide it was made to be.
  P.sizes = () => {
    const density = window.mcmap.icons.density();
    const out = [];
    for (const [image, list] of P.draws) {
      const rec = list.at(-1);
      if (!rec || !rec.canvas.isConnected || !(image instanceof HTMLCanvasElement)) continue;
      out.push({ painted: onScreen(rec).w, made: image.width / density });
    }
    return out;
  };
  // A layer of the map by a test of its options.
  P.layer = (test) => {
    let found = null;
    window.mcmap.map.eachLayer((layer) => { if (!found && layer.options && test(layer.options, layer)) found = layer; });
    return found;
  };
  P.screen = (latlng) => {
    const map = window.mcmap.map;
    const p = map.latLngToContainerPoint(latlng);
    const box = map.getContainer().getBoundingClientRect();
    return { x: box.left + p.x, y: box.top + p.y };
  };
  // The middle of the map that can be seen: its box, less a band for
  // whatever lies across a middle line of what is left, the largest first.
  P.centre = () => {
    const rect = (el) => { const b = el.getBoundingClientRect(); return { left: b.left, top: b.top, right: b.right, bottom: b.bottom }; };
    const box = rect(window.mcmap.map.getContainer());
    const free = { ...box };
    const overs = [];
    for (const el of [document.querySelector('#layers .panel'), document.getElementById('inspect'), document.getElementById('chunk')]) {
      if (!el || el.hidden) continue;
      const style = getComputedStyle(el);
      if (style.display === 'none' || style.visibility === 'hidden') continue;
      const o = rect(el);
      if (o.right > o.left && o.bottom > o.top) overs.push(o);
    }
    const area = (o) => Math.max(0, Math.min(o.right, box.right) - Math.max(o.left, box.left)) * Math.max(0, Math.min(o.bottom, box.bottom) - Math.max(o.top, box.top));
    overs.sort((a, b) => area(b) - area(a));
    for (const o of overs) {
      const c = { left: Math.max(o.left, free.left), top: Math.max(o.top, free.top), right: Math.min(o.right, free.right), bottom: Math.min(o.bottom, free.bottom) };
      if (c.right <= c.left || c.bottom <= c.top) continue;
      const mx = (free.left + free.right) / 2;
      const my = (free.top + free.bottom) / 2;
      const cuts = [];
      if (c.left <= mx && c.right >= mx) cuts.push({ ...free, top: c.bottom }, { ...free, bottom: c.top });
      if (c.top <= my && c.bottom >= my) cuts.push({ ...free, left: c.right }, { ...free, right: c.left });
      let best = null;
      for (const cut of cuts) {
        const w = cut.right - cut.left;
        const h = cut.bottom - cut.top;
        if (w < 96 || h < 96) continue;
        if (!best || w * h > (best.right - best.left) * (best.bottom - best.top)) best = cut;
      }
      if (best) Object.assign(free, best);
    }
    return { x: (free.left + free.right) / 2, y: (free.top + free.bottom) / 2, box: { x: (box.left + box.right) / 2, y: (box.top + box.bottom) / 2 } };
  };
})();
