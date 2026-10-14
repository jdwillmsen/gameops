'use strict';

// A map that keeps Leaflet's own rules for the two things a layer's life
// turns on: a view outside the bounds it is given is brought inside them
// and a zoom below its least is raised to it, and a tile layer is loading
// only while the view shows some of its bounds, which must have some size,
// at a zoom it draws, and says 'load' only after that.
function leaflet() {
  const at = (p) => (Array.isArray(p) ? { lat: p[0], lng: p[1] } : { lat: p.lat, lng: p.lng });
  const box = (south, west, north, east) => ({
    south, west, north, east,
    pad: (by) => box(south - (north - south) * by, west - (east - west) * by, north + (north - south) * by, east + (east - west) * by),
    contains: (p) => { const q = at(p); return q.lat >= south && q.lat <= north && q.lng >= west && q.lng <= east; },
    getSouth: () => south,
    getWest: () => west,
    getNorth: () => north,
    getEast: () => east,
  });
  class Heard {
    // Several types at once, parted by spaces, as Leaflet takes them.
    on(types, fn) { this.heard = [...(this.heard || []), ...types.split(' ').map((type) => [type, fn, false])]; return this; }
    once(type, fn) { this.heard = [...(this.heard || []), [type, fn, true]]; return this; }
    fire(type) {
      const mine = (this.heard || []).filter((h) => h[0] === type);
      this.heard = (this.heard || []).filter((h) => !(h[0] === type && h[2]));
      for (const h of mine) h[1]({ type });
      return this;
    }
  }
  class Sheet extends Heard {
    constructor() {
      super();
      this.options = { inertia: true };
      this._fadeAnimated = true;
      this.layers = new Set();
      this.centre = null;
      this.zoom = null;
      this.least = -Infinity;
      this.bounds = null;
    }
    getCenter() { return { ...this.centre }; }
    getZoom() { return this.zoom; }
    getContainer() { return {}; }
    getSize() { return { x: 1024, y: 768 }; }
    // A pane is placed by how far the map has been dragged, which is the
    // test's to say.
    containerPointToLayerPoint(p) { return { x: p[0] - (this.dragged || 0), y: p[1] }; }
    createPane(name) { this.panes = { ...this.panes, [name]: { style: {} } }; return this.panes[name]; }
    getPane(name) { return this.panes[name]; }
    held() {
      if (this.centre === null) return;
      this.zoom = Math.max(this.zoom, this.least);
      if (!this.bounds) return;
      this.centre = { lat: Math.min(Math.max(this.centre.lat, this.bounds.south), this.bounds.north), lng: Math.min(Math.max(this.centre.lng, this.bounds.west), this.bounds.east) };
    }
    setView(to, zoom) { this.centre = at(to); this.zoom = zoom; this.held(); return this; }
    fitBounds(b) { return this.setView([(b.south + b.north) / 2, (b.west + b.east) / 2], this.least); }
    setMinZoom(zoom) { this.least = zoom; this.held(); return this; }
    setMaxBounds(b) { this.bounds = b; this.held(); return this; }
    removeLayer(layer) { this.layers.delete(layer); return this; }
    hasLayer(layer) { return this.layers.has(layer); }
    invalidateSize() { return this; }
    panBy() { return this; }
  }
  const map = new Sheet();
  class Layer extends Heard {
    constructor(url, options) { super(); this.options = { ...options }; this.come = false; }
    static extend(props) {
      const Sub = class extends this {};
      Object.assign(Sub.prototype, props);
      return Sub;
    }
    addTo(to) { to.layers.add(this); return this; }
    redraw() { return this; }
    setOpacity(to) { this.options.opacity = to; return this; }
    isLoading() {
      const o = this.options;
      // Bounds of no size hold no tile, wherever the view is.
      const room = !o.bounds || (o.bounds.north > o.bounds.south && o.bounds.east > o.bounds.west && o.bounds.contains(map.centre));
      return !this.come && map.centre !== null && map.zoom >= o.minZoom && map.zoom <= o.maxZoom && room;
    }
    // Its tiles have come.
    arrive() { this.come = true; this.fire('load'); }
  }
  const L = {
    extend: Object.assign,
    CRS: { Simple: {} },
    Transformation: class {},
    map: () => map,
    TileLayer: Layer,
    Renderer: Layer,
    latLngBounds: (a, b) => box(Math.min(a[0], b[0]), Math.min(a[1], b[1]), Math.max(a[0], b[0]), Math.max(a[1], b[1])),
  };
  return { L, map };
}

module.exports = { leaflet };
