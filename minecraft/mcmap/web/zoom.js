'use strict';

// Zoom, in plain terms: what the scale is, in the words a player would use
// for it; the wheel and the steps that move it; and the reading in the
// footer with the small panel behind it for going somewhere exact. The
// first part of the file has no page in it and is run on its own by the
// tests.
(() => {
  // --- what a zoom is ---------------------------------------------------------
  //
  // The map's CRS makes a block 2^zoom screen pixels, so zoom 0 is a block
  // to the pixel and every level is twice the last.

  const WORLD_MARGIN = 0.95;
  // A region is 512 blocks square and a chunk 16.
  const REGION_BLOCKS = 512;
  const CHUNK_BLOCKS = 16;
  // Where "block detail" is: 64 pixels to a block, a texture pixel of a
  // marker's picture being several of the map's own.
  const BLOCK_DETAIL = 6;

  const trim = (n, places) => {
    const text = n.toFixed(places);
    return text.includes('.') ? text.replace(/0+$/, '').replace(/\.$/, '') : text;
  };
  // Three figures: 128, 11.3, 1.41, 0.707.
  const figure = (n) => (n >= 100 ? String(Math.round(n)) : n >= 10 ? trim(n, 1) : trim(n, 2));
  const level = (zoom) => trim(zoom, 2);
  const pixels = (zoom) => 2 ** zoom;

  // "1 block = 8 px" when a block is a pixel or more, "1 px = 4 blocks"
  // when it is less, and "1 block = 1 px" at one.
  function scale(zoom) {
    if (Math.abs(zoom) < 0.005) return '1 block = 1 px';
    const px = pixels(zoom);
    if (px >= 1) return `1 block = ${figure(px)} px`;
    const blocks = figure(1 / px);
    return blocks === '1' ? '1 block = 1 px' : `1 px = ${blocks} blocks`;
  }

  // How much ground a width of map is, to three figures and in words.
  function across(zoom, width) {
    const n = width / pixels(zoom);
    if (!Number.isFinite(n) || n <= 0) return '';
    const said = n < 10 ? trim(n, 1) : Math.round(Number(n.toPrecision(3))).toLocaleString('en-US');
    return `${said} blocks across`;
  }

  // The zoom at which a square of blocks just fits the smaller side of a
  // box of pixels.
  const fitBlocks = (blocks, width, height) => Math.log2(Math.min(width, height) / blocks);

  const NUMBER = '(\\d+(?:\\.\\d+)?)';
  const READINGS = [
    // A bare number is a level, as is one with its name.
    [new RegExp(`^(?:(?:z|zoom|level)\\s*[:=]?\\s*)?([+-]?\\d+(?:\\.\\d+)?)$`), (n) => n],
    [new RegExp(`^${NUMBER}\\s*%$`), (n) => Math.log2(n / 100)],
    [new RegExp(`^(?:1\\s*block\\s*=\\s*)?${NUMBER}\\s*(?:px|pixels?)(?:\\s*(?:per|/)\\s*block)?$`), (n) => Math.log2(n)],
    [new RegExp(`^1\\s*(?:px|pixel)\\s*=\\s*${NUMBER}\\s*blocks?$`), (n) => -Math.log2(n)],
    [new RegExp(`^${NUMBER}\\s*blocks?\\s*(?:per|/)\\s*(?:px|pixel)$`), (n) => -Math.log2(n)],
  ];

  // What a viewer typed, as a zoom: a level ("3.5", "zoom 3"), a percentage
  // where 100% is a block to the pixel ("200%"), or a scale in either
  // direction ("8 px", "1 block = 8 px", "1 px = 4 blocks"). Null for
  // anything else, and for a scale of nothing.
  function parse(text) {
    if (typeof text !== 'string' || text.length > 40) return null;
    const said = text.trim().toLowerCase();
    for (const [pattern, toZoom] of READINGS) {
      const m = pattern.exec(said);
      if (!m) continue;
      const n = Number(m[1]);
      if (!Number.isFinite(n) || (pattern !== READINGS[0][0] && n <= 0)) return null;
      const zoom = toZoom(n);
      return Number.isFinite(zoom) ? zoom : null;
    }
    return null;
  }

  // A turn of a mouse wheel is a hundred pixels of scroll and is half a
  // level; a trackpad's scroll adds up from its small steps to the same.
  const WHEEL_LEVELS_PER_PX = 0.005;
  // A pinch on a trackpad arrives as scrolling with Ctrl held, in much
  // smaller steps.
  const PINCH_LEVELS_PER_PX = 0.015;
  // One event is never more than a turn and a half of the wheel, whatever
  // it says, so that a lurch of the hardware is not a lurch of the map.
  const MOST_PX = 150;
  const LINE_PX = 33;
  const PAGE_PX = 400;
  // How long the map takes to catch up with where it has been told to go,
  // as the time to cover most of the distance, and how long a gesture is
  // taken to go on for after its last event.
  const CHASE_MS = 55;
  const GLIDE_MS = 110;
  const QUIET_MS = 100;
  // A pointer that has moved this far since the wheel last turned is
  // pointing at something else, and the zoom is about that.
  const RE_ANCHOR_PX = 4;
  const SETTLED = 0.004;

  // How many levels a wheel event asks for: a turn of a mouse wheel is a
  // hundred pixels and half a level, a line is a third of a hundred and a
  // page four, and what Ctrl adds to a trackpad's pinch is finer.
  function wheelLevels(deltaY, deltaMode, ctrlKey) {
    const unit = deltaMode === 1 ? LINE_PX : deltaMode === 2 ? PAGE_PX : 1;
    const dy = Math.max(-MOST_PX, Math.min(MOST_PX, deltaY * unit));
    return dy === 0 ? 0 : -dy * (ctrlKey ? PINCH_LEVELS_PER_PX : WHEEL_LEVELS_PER_PX);
  }

  // Safari's pinch is a gesture with a scale from where it began, not
  // scrolling.
  const gestureZoom = (startZoom, scale) => (Number.isFinite(scale) && scale > 0 ? startZoom + Math.log2(scale) : startZoom);

  // One frame's catch-up toward a zoom: most of the distance in chase
  // milliseconds, all of it for a viewer who has asked for less motion.
  function eased(from, target, dt, chase, still) {
    const catchUp = still ? 1 : 1 - Math.exp(-Math.min(64, dt) / chase);
    const next = from + (target - from) * catchUp;
    return { zoom: Math.abs(target - next) < SETTLED ? target : next, catchUp };
  }

  // Where the map's centre has to be, at a zoom, for the block that was
  // under a point of its box to stay under it. Blocks, and the map's box
  // in pixels: a block is 2^zoom of them.
  function anchoredCentre(about, at, half, zoom) {
    const k = 2 ** zoom;
    return { lat: about.lat - (at.y - half.y) / k, lng: about.lng - (at.x - half.x) / k };
  }

  // The zoom a step up or down comes to from where it is going: the next
  // whole level, so that steps land where a scale can be read off, and
  // levels of them for a shift.
  function stepped(now, direction, levels = 1) {
    const nearest = Math.round(now);
    const first = Math.abs(now - nearest) < 0.01 ? nearest + direction : direction > 0 ? Math.ceil(now) : Math.floor(now);
    return first + direction * (levels - 1);
  }

  const model = { WORLD_MARGIN, REGION_BLOCKS, CHUNK_BLOCKS, BLOCK_DETAIL, level, scale, across, fitBlocks, parse, pixels, wheelLevels, gestureZoom, eased, anchoredCentre, stepped };
  window.mcmapZoom = model;

  const app = window.mcmap;
  if (!app || !app.map || !document.getElementById('zoom-open')) return;
  const { map } = app;
  const settings = window.mcmapSettings || null;

  // --- moving the zoom ----------------------------------------------------------
  //
  // Everything here moves the map as a pinch does, a step at a time with
  // the zoom event of each, and not as Leaflet's own zooms do. Those are
  // animated, and one asked for while another is under way is dropped; a
  // step of a wheel turned quickly, or of a button held down, would be
  // lost. A pinch's way is the one that follows the fingers without
  // starting over, and the markers, the grid and a followed player are all
  // already made to follow it. _move, _moveStart, _moveEnd, _stop,
  // _animatingZoom, _onZoomTransitionEnd and _limitZoom are Leaflet
  // internals, which is safe only because Leaflet is vendored at a fixed
  // version.

  const still = () => Boolean(settings) && settings.look().motion === 'reduce';
  const limit = (zoom) => Math.max(map.getMinZoom(), Math.min(map.getMaxZoom(), zoom));

  // One run of the map to a zoom: where it is going and about what.
  let run = null;
  let frame = 0;

  function finish() {
    run = null;
    cancelAnimationFrame(frame);
    frame = 0;
    map._moveEnd(true);
    // The world may be smaller than the window now, or the view off its edge.
    if (map.options.maxBounds) map.panInsideBounds(map.options.maxBounds, { animate: !still() });
  }

  function step(now) {
    frame = 0;
    if (!run) return;
    const dt = now - run.last;
    run.last = now;
    const { zoom, catchUp } = eased(map.getZoom(), run.target, dt, run.chase, still());
    let centre;
    if (run.centre) {
      const at = map.getCenter();
      centre = zoom === run.target ? run.centre : L.latLng(at.lat + (run.centre.lat - at.lat) * catchUp, at.lng + (run.centre.lng - at.lng) * catchUp);
    } else {
      // The block that was under the anchor stays under it.
      const half = map.getSize().divideBy(2);
      const raw = anchoredCentre(run.about, run.at, half, zoom);
      // A pointer or a map with no size to speak of has no centre to give;
      // the zoom ends where it is and the map is left as it was.
      if (!Number.isFinite(raw.lat) || !Number.isFinite(raw.lng) || !Number.isFinite(zoom)) {
        finish();
        return;
      }
      centre = L.latLng(raw);
    }
    map._move(centre, zoom, { pinch: true, round: false });
    if (zoom === run.target && now >= run.until) finish();
    else frame = requestAnimationFrame(step);
  }

  function begin(target, how) {
    if (!map._loaded || !Number.isFinite(target)) return;
    const goal = limit(target);
    if (!run) {
      if (map._animatingZoom) map._onZoomTransitionEnd();
      map._stop();
      map._moveStart(true, false);
      run = { last: performance.now() };
    }
    Object.assign(run, how, { target: goal, until: performance.now() + (how.hold || 0) });
    if (frame === 0) frame = requestAnimationFrame(step);
  }

  // Another way of moving the map takes over: a drag, a view set outright,
  // or a zoom of Leaflet's own. The zoom that was begun is ended, so that
  // the layers that were stretched through it are drawn again; a zoom of
  // Leaflet's own ends itself.
  function letGo(e) {
    if (!run) return;
    run = null;
    cancelAnimationFrame(frame);
    frame = 0;
    if (e.type !== 'zoomanim') map._moveEnd(true);
  }
  map.on('dragstart zoomanim viewreset', letGo);

  const middle = () => map.getSize().divideBy(2);

  // Goes to a zoom, about a point of the map's box or its middle, or to a
  // zoom and a place together.
  function to(zoom, options = {}) {
    const at = options.at || middle();
    if (options.centre) begin(zoom, { centre: L.latLng(options.centre), chase: GLIDE_MS, hold: 0 });
    else begin(zoom, { centre: null, at, about: map.containerPointToLatLng(at), chase: options.chase || GLIDE_MS, hold: 0 });
  }

  // The zoom it is going to, or is at.
  const heading = () => (run ? run.target : map.getZoom());
  // A level up or down. From between two it is to the next whole one, so
  // that the steps land on levels a scale can be read off.
  function by(direction, levels = 1) {
    to(stepped(heading(), direction, levels));
  }
  map.zoomIn = (delta) => { by(1, Math.max(1, Math.round(delta || 1))); return map; };
  map.zoomOut = (delta) => { by(-1, Math.max(1, Math.round(delta || 1))); return map; };

  map.getContainer().addEventListener('wheel', (e) => {
    const levels = wheelLevels(e.deltaY, e.deltaMode, e.ctrlKey);
    // Sideways scrolling is not a zoom, and the page does not scroll.
    e.preventDefault();
    if (levels === 0 || !map._loaded) return;
    const box = map.getContainer().getBoundingClientRect();
    const where = L.point(e.clientX - box.left, e.clientY - box.top);
    if (!Number.isFinite(where.x) || !Number.isFinite(where.y)) return;
    const going = run && !run.centre && run.at && where.distanceTo(run.at) <= RE_ANCHOR_PX;
    const at = going ? run.at : where;
    begin(heading() + levels, {
      centre: null,
      at,
      about: going ? run.about : map.containerPointToLatLng(where),
      chase: CHASE_MS,
      hold: QUIET_MS,
    });
  }, { passive: false });

  // Safari's trackpad pinch is not scrolling but a gesture, with the scale
  // it has come to since it began.
  let pinching = null;
  const surface = map.getContainer();
  const pointOf = (e) => {
    const box = surface.getBoundingClientRect();
    return L.point(e.clientX - box.left, e.clientY - box.top);
  };
  surface.addEventListener('gesturestart', (e) => {
    e.preventDefault();
    if (!map._loaded) return;
    const at = pointOf(e);
    pinching = { zoom: heading(), at, about: map.containerPointToLatLng(at) };
  });
  surface.addEventListener('gesturechange', (e) => {
    e.preventDefault();
    if (pinching) begin(gestureZoom(pinching.zoom, e.scale), { centre: null, at: pinching.at, about: pinching.about, chase: CHASE_MS, hold: QUIET_MS });
  });
  surface.addEventListener('gestureend', (e) => {
    e.preventDefault();
    pinching = null;
  });

  // --- where the useful stops are -----------------------------------------------

  function stops() {
    const size = map.getSize();
    const extent = app.extent && app.extent();
    return {
      world: () => {
        if (!extent) return null;
        return { zoom: app.fitZoom(extent) + Math.log2(WORLD_MARGIN), centre: extent.getCenter() };
      },
      region: () => ({ zoom: fitBlocks(REGION_BLOCKS, size.x, size.y) }),
      chunk: () => ({ zoom: fitBlocks(CHUNK_BLOCKS, size.x, size.y) }),
      block: () => ({ zoom: BLOCK_DETAIL }),
      actual: () => ({ zoom: 0 }),
    };
  }

  function stop(name) {
    const made = stops()[name];
    const there = made && made();
    if (!there || !Number.isFinite(there.zoom)) return false;
    to(there.zoom, there.centre ? { centre: there.centre } : {});
    return true;
  }

  app.zoom = {
    to,
    stop,
    by: (direction) => by(direction),
    heading,
    world: () => stop('world'),
    block: () => stop('block'),
    // For whoever reports on the page: where a zoom run is going, or null.
    running: () => (run ? { target: run.target } : null),
  };

  // --- the reading in the footer and the panel behind it --------------------------

  const el = {
    open: document.getElementById('zoom-open'),
    level: document.getElementById('zoom-level'),
    scale: document.getElementById('zoom-scale'),
    across: document.getElementById('zoom-across'),
    pop: document.getElementById('zoom-pop'),
    close: document.getElementById('zoom-close'),
    says: document.getElementById('zoom-says'),
    range: document.getElementById('zoom-range'),
    out: document.getElementById('zoom-out'),
    in: document.getElementById('zoom-in'),
    stops: document.getElementById('zoom-stops'),
    form: document.getElementById('zoom-form'),
    field: document.getElementById('zoom-field'),
    said: document.getElementById('zoom-field-said'),
  };
  if (Object.values(el).some((node) => !node)) return;

  const write = (node, text) => {
    if (node.textContent !== text) node.textContent = text;
  };

  let shown = '';
  function paint() {
    painting = 0;
    const zoom = map.getZoom();
    // Before the map has been given a view there is no zoom to read.
    if (!Number.isFinite(zoom)) return;
    const width = map.getSize().x;
    const key = `${level(zoom)}|${width}`;
    if (key === shown) return;
    shown = key;
    write(el.level, level(zoom));
    write(el.scale, scale(zoom));
    write(el.across, across(zoom, width));
    if (!el.pop.hidden) paintPop(zoom, width);
  }

  let painting = 0;
  function schedule() {
    if (painting === 0) painting = requestAnimationFrame(paint);
  }

  function paintPop(zoom, width) {
    write(el.says, `Zoom ${level(zoom)}: ${scale(zoom)}, ${across(zoom, width)}.`);
    if (document.activeElement !== el.range) {
      el.range.min = String(map.getMinZoom());
      el.range.max = String(map.getMaxZoom());
      el.range.value = String(zoom);
    }
    el.range.setAttribute('aria-valuetext', `${level(zoom)}, ${scale(zoom)}`);
    el.out.disabled = zoom <= map.getMinZoom() + 0.001;
    el.in.disabled = zoom >= map.getMaxZoom() - 0.001;
  }

  // What a screen reader says of the button changes when the zoom has
  // come to rest and not with every step of it.
  function settle() {
    const zoom = map.getZoom();
    if (!Number.isFinite(zoom)) return;
    el.open.setAttribute('aria-label', `Zoom ${level(zoom)}, ${scale(zoom)}. Open the zoom controls`);
  }

  map.on('zoom resize', schedule);
  map.on('zoomend moveend', () => {
    paint();
    settle();
  });

  function place() {
    const foot = el.pop.offsetParent;
    if (!foot) return;
    const button = el.open.getBoundingClientRect();
    const box = foot.getBoundingClientRect();
    const room = box.width - el.pop.offsetWidth - 8;
    el.pop.style.left = `${Math.max(4, Math.min(room, button.left - box.left))}px`;
  }

  let opener = null;
  function open() {
    if (!el.pop.hidden) return;
    el.pop.hidden = false;
    el.open.setAttribute('aria-expanded', 'true');
    shown = '';
    paint();
    paintPop(map.getZoom(), map.getSize().x);
    place();
    write(el.said, '');
    opener = el.open;
    el.range.focus();
  }

  function shut(refocus) {
    if (el.pop.hidden) return;
    el.pop.hidden = true;
    el.open.setAttribute('aria-expanded', 'false');
    if (refocus && opener) opener.focus();
  }

  el.open.addEventListener('click', () => (el.pop.hidden ? open() : shut(true)));
  el.close.addEventListener('click', () => shut(true));
  el.pop.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape') return;
    e.stopPropagation();
    shut(true);
  });
  document.addEventListener('pointerdown', (e) => {
    if (el.pop.hidden || !(e.target instanceof Node)) return;
    if (!el.pop.contains(e.target) && !el.open.contains(e.target)) shut(false);
  });
  window.addEventListener('resize', () => {
    if (!el.pop.hidden) place();
  });

  el.range.addEventListener('input', () => to(Number(el.range.value), { chase: 40 }));
  el.out.addEventListener('click', () => by(-1));
  el.in.addEventListener('click', () => by(1));
  el.stops.addEventListener('click', (e) => {
    const button = e.target instanceof Element ? e.target.closest('button[data-stop]') : null;
    if (button) stop(button.dataset.stop);
  });

  const HINT = 'Try 3.5, 200%, 8 px or 1 px = 4 blocks.';
  el.form.addEventListener('submit', (e) => {
    e.preventDefault();
    const asked = parse(el.field.value);
    if (asked === null) {
      el.field.setAttribute('aria-invalid', 'true');
      write(el.said, `That is not a zoom or a scale. ${HINT}`);
      return;
    }
    el.field.removeAttribute('aria-invalid');
    const got = limit(asked);
    to(got);
    write(el.said, got === asked ? `Going to ${scale(got)}.` : `The map goes from ${scale(map.getMinZoom())} to ${scale(map.getMaxZoom())}; going to ${scale(got)}.`);
  });
  el.field.addEventListener('input', () => el.field.removeAttribute('aria-invalid'));

  paint();
  settle();
})();
