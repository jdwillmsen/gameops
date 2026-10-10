'use strict';

// The part of the map that can be seen. Beside a docked panel the map is
// its own box, but a panel that floats over its edge, the sheet that
// comes up from the bottom of a phone and the card about one mob or
// player all lie over it, and the middle of the box may then be under
// one of them. Whoever keeps something in the middle of the map asks
// here where the middle is.
(() => {
  const app = window.mcmap;
  if (!app || !app.map) return;
  const { map } = app;

  // Whatever would leave less of the map than this across is not made
  // room for: a sheet at its full height covers the map, and there is
  // then no middle to find but the box's own.
  const LEAST = 96;
  // What lies over the map. The panel's own box is beside the map when it
  // is docked and takes nothing from it.
  const OVER = ['#layers .panel', '#inspect', '#chunk'];

  const across = (r) => r.right - r.left;
  const down = (r) => r.bottom - r.top;

  // The box less a band for each cover that lies across a middle line of
  // what is left, the largest first. A cover across the upright line is
  // cut off from the top or the bottom, one across the level line from
  // the left or the right, and of those whichever leaves the most; one
  // that is across neither is in a corner, with the middle clear of it.
  // box and covers are { left, top, right, bottom } in one frame.
  function free(box, covers) {
    const left = { ...box };
    const within = (c) => Math.max(0, Math.min(c.right, box.right) - Math.max(c.left, box.left)) * Math.max(0, Math.min(c.bottom, box.bottom) - Math.max(c.top, box.top));
    for (const cover of [...covers].sort((a, b) => within(b) - within(a))) {
      const c = { left: Math.max(cover.left, left.left), top: Math.max(cover.top, left.top), right: Math.min(cover.right, left.right), bottom: Math.min(cover.bottom, left.bottom) };
      if (across(c) <= 0 || down(c) <= 0) continue;
      const x = (left.left + left.right) / 2;
      const y = (left.top + left.bottom) / 2;
      const cuts = [];
      if (c.left <= x && c.right >= x) cuts.push({ ...left, top: c.bottom }, { ...left, bottom: c.top });
      if (c.top <= y && c.bottom >= y) cuts.push({ ...left, left: c.right }, { ...left, right: c.left });
      let best = null;
      for (const cut of cuts) {
        if (across(cut) < LEAST || down(cut) < LEAST) continue;
        if (!best || across(cut) * down(cut) > across(best) * down(best)) best = cut;
      }
      if (best) Object.assign(left, best);
    }
    return left;
  }

  // How far the middle of what can be seen is from the middle of the box.
  function offset(box, covers) {
    const seen = free(box, covers);
    return { x: (seen.left + seen.right - box.left - box.right) / 2, y: (seen.top + seen.bottom - box.top - box.bottom) / 2 };
  }

  const boxOf = (node) => {
    const b = node.getBoundingClientRect();
    return { left: b.left, top: b.top, right: b.right, bottom: b.bottom };
  };

  function covers() {
    const out = [];
    for (const selector of OVER) {
      const node = document.querySelector(selector);
      // One that is not shown has no box.
      if (!node || node.getClientRects().length === 0) continue;
      out.push(boxOf(node));
    }
    return out;
  }

  // The centre to give the map so that a place is in the middle of what
  // can be seen, at a zoom.
  function aim(latlng, zoom = map.getZoom()) {
    const by = offset(boxOf(map.getContainer()), covers());
    if (by.x === 0 && by.y === 0) return L.latLng(latlng);
    return map.unproject(map.project(latlng, zoom).subtract([by.x, by.y]), zoom);
  }

  // Calls back when the room may have changed: the map's box has, or
  // something over it has come, gone or changed size, in the frame it
  // does and before that frame is painted. A cover made after this is
  // asked is not watched, and the map's own change still is.
  function watch(changed) {
    map.on('resize', changed);
    if (typeof ResizeObserver !== 'function') return;
    const observer = new ResizeObserver(() => changed());
    observer.observe(map.getContainer());
    for (const selector of OVER) {
      const node = document.querySelector(selector);
      if (node) observer.observe(node);
    }
  }

  app.room = { free, offset, aim, watch };
})();
