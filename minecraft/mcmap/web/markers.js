'use strict';

// What stays where it is: the player's own waypoints, and the beds,
// containers and named mobs read from the world at the last snapshot. They
// change at most every few minutes, so they are fetched, not streamed. Each
// is drawn as its own picture on a square plate, where the live layer's are
// round, and as the ring it used to be while the server has no picture for
// it. A named mob is its type's icon under its name, and is listed by name
// under its row, since a name is what it is looked for by. While the same
// mob is loaded the live layer draws it where it is, under the same name,
// and the mark the snapshot left steps aside: one animal, one marker.
(() => {
  const app = window.mcmap;
  // The page and its scripts are cached apart for a few minutes, so just
  // after a release this can meet a page that has no panel yet.
  if (!app || !app.layers || !app.layers.register || !app.icons || !app.names) return;
  const { map, icons, names } = app;

  // Several things announce a change of view at once; one fetch answers
  // them all.
  const SETTLE_MS = 50;
  // From further out than this a bed or a container is its ring: a
  // village's worth of pictures twenty pixels wide, eight blocks to the
  // pixel, is a heap in which none can be made out.
  const PICTURE_ZOOM = -2;
  const INK = '#0b0c0e';
  const { DENSITY } = icons;

  // picture is the one that stands for the whole row in the panel, where
  // a single one can.
  const KINDS = {
    waypoints: { label: 'Waypoints', color: '#b48cf2', radius: 6, picture: 'marker/waypoint' },
    beds: { label: 'Beds', color: '#f277b5', radius: 4, picture: 'bed/red' },
    containers: { label: 'Containers', color: '#f08a3c', radius: 4, picture: 'container/chest' },
    mobs: { label: 'Named mobs', color: '#4fd1c5', radius: 5 },
  };
  const WORLD_KINDS = ['beds', 'containers', 'mobs'];
  const BABY_RADIUS = 4;

  // Each kind's row in the panel, once the service is known to have that
  // kind. One it does not have gets no row.
  const rows = new Map();

  // On the live layer's canvas. A canvas takes every pointer event over
  // the map, so two of them cannot both be hovered; on the one, these and
  // the live markers both can, and two thousand pictures are two thousand
  // stamps, not two thousand elements.
  const renderer = app.liveRenderer || L.canvas({ padding: 0.5 });

  // Names reach here from the game and from chat, where players choose
  // them, and Leaflet treats a string given to a tooltip as HTML. An
  // element holding text is the form it cannot interpret.
  const text = (s) => {
    const span = document.createElement('span');
    span.textContent = s;
    return span;
  };

  const fmt = (n) => n.toLocaleString('en-US');
  const at = (m) => `${fmt(m.x)}, ${fmt(m.y)}, ${fmt(m.z)}`;
  const str = (v) => (typeof v === 'string' ? v : '');

  const SORTS = { waypoints: 'waypoint', beds: 'bed', containers: 'container', mobs: 'mob' };
  const pictureOf = (kind, m) => icons.keyOf(SORTS[kind], { kind: m.k, colour: m.c, trapped: m.t });

  // What a marker says of itself, without where it is.
  function titleOf(kind, m) {
    if (kind === 'beds') return names.bed(m.c);
    if (kind === 'containers') {
      const what = names.holder(m.k, m.c, m.t);
      return str(m.n) ? `${str(m.n)} (${what})` : what;
    }
    if (kind === 'mobs') return names.mob(m.n, m.k, m.b);
    return str(m.name) || 'Waypoint';
  }

  // Said when it is asked for, so that a name which arrives after the
  // marker was drawn is the one shown.
  function tip(marker) {
    const { kind, data } = marker.options;
    const box = text(`${titleOf(kind, data)} · ${at(data)}`);
    box.prepend(icons.picture(pictureOf(kind, data)));
    return box;
  }

  // A marker with, for a named mob, its name over it.
  const Pin = icons.Tagged;

  const layers = {};
  for (const kind of Object.keys(KINDS)) {
    layers[kind] = L.featureGroup();
    layers[kind].bindTooltip((marker) => tip(marker), { sticky: true, direction: 'top', className: 'marker-tip' });
  }

  // Whether the service has each kind at all; null until it has answered.
  const available = { world: null, waypoints: null };
  const totals = { waypoints: null, beds: null, containers: null, mobs: null };
  const more = { waypoints: 0, beds: 0, containers: 0, mobs: 0 };
  // What is drawn now, so that an unchanged answer redraws nothing.
  let drawn = { dimension: null, etag: null };
  let waypoints = [];
  let timer = null;
  let asked = 0;
  let near = false;

  // The named mobs of this dimension, as { data, marker }, in the order
  // listed.
  let named = [];
  // When the snapshot the world's markers were read from was taken, in
  // milliseconds, or null where the answer did not say.
  let snapshotAt = null;
  // What the list was last built from, so that an unchanged one is left
  // alone under the viewer's pointer.
  let rosterOf = '';
  const roster = document.createElement('div');
  roster.className = 'roster';

  // Brings one marker in line with the pictures there are now and with
  // how far out the map is.
  function dress(marker) {
    const { kind, data } = marker.options;
    const style = KINDS[kind];
    let worn = null;
    let radius = style.radius;
    if (kind === 'mobs') {
      const baby = data.b === true;
      worn = icons.mob(str(data.k), style.color, baby);
      if (worn) radius = baby ? icons.BABY_RADIUS : icons.MOB_RADIUS;
      else if (baby) radius = BABY_RADIUS;
    } else if (near || kind === 'waypoints') {
      worn = icons.plate(pictureOf(kind, data), style.color);
      if (worn) radius = icons.PLATE_RADIUS;
    }
    if (worn === marker.options.sprite && radius === marker.getRadius()) return;
    marker.options.sprite = worn;
    marker.setRadius(radius);
  }

  function dressAll(kinds) {
    for (const kind of kinds) {
      // A mark that has stepped aside is dressed too, for when it is back.
      if (kind === 'mobs') named.forEach((entry) => dress(entry.marker));
      else layers[kind].eachLayer(dress);
    }
  }

  function pin(kind, m) {
    const style = KINDS[kind];
    // The middle of the block, not its north-west corner.
    const marker = new Pin([m.z + 0.5, m.x + 0.5], {
      renderer,
      radius: style.radius,
      color: style.color,
      weight: 2,
      fillColor: INK,
      fillOpacity: 0.75,
      kind,
      data: m,
      sprite: null,
      tag: kind === 'mobs' ? icons.tag(m.n, style.color) : null,
    });
    dress(marker);
    return marker;
  }

  const placed = (m) => m && Number.isFinite(m.x) && Number.isFinite(m.y) && Number.isFinite(m.z);

  function fill(kind, list) {
    layers[kind].clearLayers();
    const made = [];
    for (const m of list) {
      if (!placed(m)) continue;
      const marker = pin(kind, m);
      if (kind === 'waypoints') {
        // A waypoint is the one marker a player put there by name, so the
        // name is always showing.
        marker.bindTooltip(text(str(m.name) || 'Waypoint'), { permanent: true, direction: 'right', offset: [icons.PLATE_RADIUS + 2, 0], className: 'marker-name' });
      }
      layers[kind].addLayer(marker);
      made.push({ data: m, marker });
    }
    totals[kind] = made.length;
    if (kind !== 'mobs') return;
    named = made.sort((a, b) => str(a.data.n).localeCompare(str(b.data.n)) || a.data.x - b.data.x || a.data.z - b.data.z);
  }

  function show(kind) {
    const want = rows.has(kind) && rows.get(kind).enabled;
    if (want === map.hasLayer(layers[kind])) return;
    if (!want) {
      map.removeLayer(layers[kind]);
      return;
    }
    layers[kind].addTo(map);
  }

  // --- the named mobs, by name ---------------------------------------------

  const inspect = () => app.inspect || null;
  // The id the live layer would know this mob by, where the snapshot gave
  // one.
  const idOf = (data) => (typeof data.i === 'string' && data.i !== '' ? data.i : null);
  // What the card is opened under for it, which is how the list knows
  // which of its entries the card is about.
  const keyOf = (data) => (idOf(data) ? `m:${idOf(data)}` : `s:${drawn.dimension}:${data.x + 0.5}:${data.y}:${data.z + 0.5}`);

  // Opens the card about a named mob: live where the live layer has it,
  // and otherwise where the snapshot left it, said as that.
  function examine(entry, go) {
    const card = inspect();
    const { data } = entry;
    const dimension = drawn.dimension || app.dimension();
    const id = idOf(data);
    const now = card && id ? card.where(id) : null;
    // The middle of the block, not its north-west corner.
    const at = now || { x: data.x + 0.5, y: data.y, z: data.z + 0.5 };
    if (go && dimension) app.go(dimension, at.x, at.z);
    if (!card) return;
    card.open({ kind: 'mob', id, name: str(data.n), type: str(data.k), baby: data.b === true, ...at, dimension, savedAt: snapshotAt ?? undefined });
  }

  // Takes the mark of a mob the live layer is drawing off the map, and
  // puts back the mark of one it no longer is. The name stays on the map
  // either way: the live marker wears it.
  function aside() {
    const card = inspect();
    if (!card) return;
    let back = false;
    for (const entry of named) {
      const id = idOf(entry.data);
      if (!id) continue;
      const live = card.drawn(id);
      if (live === !layers.mobs.hasLayer(entry.marker)) continue;
      if (live) layers.mobs.removeLayer(entry.marker);
      else layers.mobs.addLayer(entry.marker);
      back = back || !live;
    }
    // A mark put back was added last, and would paint over what moves.
    if (back && map.hasLayer(layers.mobs)) stack();
  }

  function current() {
    const key = inspect() ? inspect().key() : null;
    for (const button of roster.querySelectorAll('button')) {
      if (key !== null && button.dataset.key === key) button.setAttribute('aria-current', 'true');
      else button.removeAttribute('aria-current');
    }
  }

  // The list under the row: every named mob in this dimension, by name,
  // with what it is. All of it is set as text; a name tag is a player's
  // choice.
  function buildRoster() {
    const key = `${drawn.dimension}|${drawn.etag}|${named.length}|${named.map((e) => names.kindOf(e.data.k, e.data.b)).join('|')}`;
    if (key === rosterOf) return;
    rosterOf = key;
    const list = document.createElement('ul');
    list.setAttribute('aria-label', 'Named mobs in this dimension, by name');
    for (const entry of named) {
      const { data } = entry;
      const button = document.createElement('button');
      button.type = 'button';
      const swatch = document.createElement('i');
      swatch.className = 'ring mobs';
      const name = text(str(data.n) || names.entity(data.k));
      name.className = 'name';
      const what = text(names.kindOf(data.k, data.b));
      what.className = 'what';
      button.append(icons.picture(pictureOf('mobs', data)), swatch, name, what);
      button.title = `${fmt(data.x)}, ${fmt(data.y)}, ${fmt(data.z)}`;
      button.dataset.key = keyOf(data);
      button.addEventListener('click', () => examine(entry, true));
      const item = document.createElement('li');
      item.append(button);
      list.append(item);
    }
    roster.replaceChildren(list);
    current();
  }

  // The canvas paints in the order markers were added, so a layer switched
  // back on would cover the ones over it. What a player named goes over
  // what the world merely holds, and what moves over what stays put.
  function stack() {
    for (const kind of ['waypoints', 'mobs']) {
      if (map.hasLayer(layers[kind])) layers[kind].eachLayer((marker) => marker.bringToFront());
    }
    if (app.liveToFront) app.liveToFront();
  }

  function paint() {
    const has = { waypoints: available.waypoints, beds: available.world, containers: available.world, mobs: available.world };
    let added = false;
    Object.keys(KINDS).forEach((kind, at) => {
      if (!has[kind]) {
        if (rows.has(kind)) rows.get(kind).remove();
        rows.delete(kind);
      } else if (!rows.has(kind)) {
        const picture = KINDS[kind].picture ? icons.picture(KINDS[kind].picture) : null;
        const row = app.layers.register({ group: 'markers', id: kind, label: KINDS[kind].label, order: (at + 1) * 10, swatch: `ring ${kind}`, picture });
        row.onToggle(paint);
        rows.set(kind, row);
      }
      const was = map.hasLayer(layers[kind]);
      show(kind);
      added = added || (!was && map.hasLayer(layers[kind]));
      if (!rows.has(kind)) return;
      rows.get(kind).setCount(totals[kind]);
      rows.get(kind).setNote(more[kind] > 0 ? `${fmt(more[kind])} more are not shown` : '');
    });
    if (added) stack();
    // With the named mobs off, a loaded one goes without its name too.
    if (inspect()) inspect().labels(!rows.has('mobs') || rows.get('mobs').enabled);
    if (!rows.has('mobs')) return;
    if (rows.get('mobs').enabled && named.length > 0) {
      buildRoster();
      rows.get('mobs').setBody(roster);
    } else {
      rows.get('mobs').setBody(null);
    }
  }

  function clear() {
    for (const kind of Object.keys(KINDS)) {
      layers[kind].clearLayers();
      totals[kind] = null;
      more[kind] = 0;
    }
    drawn = { dimension: null, etag: null };
    waypoints = [];
    named = [];
    snapshotAt = null;
    paint();
  }

  function drawWaypoints(dimension) {
    fill('waypoints', waypoints.filter((w) => w && w.dimension === dimension));
  }

  async function loadWorld(dimension, turn) {
    let res;
    try {
      // The browser keeps the last answer and asks whether it still
      // stands, so an unchanged world costs a 304.
      res = await fetch(`api/markers?dimension=${encodeURIComponent(dimension)}`);
    } catch {
      return false; // what is drawn stays; the next announcement asks again
    }
    if (turn !== asked) return false;
    if (res.status === 404) {
      available.world = false;
      return false;
    }
    if (!res.ok) return false;
    const etag = res.headers.get('ETag');
    let doc;
    try { doc = await res.json(); } catch { return false; }
    if (turn !== asked) return false;
    available.world = true;
    if (drawn.dimension === dimension && etag && drawn.etag === etag) return false;
    drawn = { dimension, etag };
    const at = Date.parse(doc.at);
    snapshotAt = Number.isFinite(at) ? at : null;
    for (const kind of WORLD_KINDS) {
      fill(kind, Array.isArray(doc[kind]) ? doc[kind] : []);
      more[kind] = (doc.more && Number.isFinite(doc.more[kind])) ? doc.more[kind] : 0;
    }
    return true;
  }

  async function loadWaypoints(dimension, turn) {
    let res;
    try {
      res = await fetch('api/waypoints', { cache: 'no-store' });
    } catch {
      return false;
    }
    if (turn !== asked) return false;
    if (res.status === 404) {
      available.waypoints = false;
      return false;
    }
    // Shown as a filter even while the agent cannot be reached: the layer
    // exists, it just has nothing to draw until the next try.
    available.waypoints = true;
    if (!res.ok) return false;
    let doc;
    try { doc = await res.json(); } catch { return false; }
    if (turn !== asked) return false;
    waypoints = Array.isArray(doc.waypoints) ? doc.waypoints : [];
    more.waypoints = Number.isFinite(doc.more) ? doc.more : 0;
    drawWaypoints(dimension);
    return true;
  }

  async function refresh() {
    const dimension = app.dimension();
    if (document.body.classList.contains('locked')) {
      // Logged out: nothing of the last player's stays on the page.
      asked += 1;
      clear();
      return;
    }
    if (!dimension || document.hidden) return;
    const turn = asked += 1;
    if (drawn.dimension !== null && drawn.dimension !== dimension) {
      // Another dimension's markers are wrong here, not merely old.
      for (const kind of WORLD_KINDS) layers[kind].clearLayers();
      named = [];
      drawn = { dimension: null, etag: null };
      drawWaypoints(dimension);
    }
    const jobs = [];
    if (available.world !== false) jobs.push(loadWorld(dimension, turn));
    if (available.waypoints !== false) jobs.push(loadWaypoints(dimension, turn));
    const redrawn = await Promise.all(jobs);
    if (turn !== asked) return;
    paint();
    if (redrawn.some(Boolean)) {
      aside();
      stack();
    }
  }

  function sync() {
    clearTimeout(timer);
    timer = setTimeout(refresh, SETTLE_MS);
  }

  function zoomed() {
    const now = map.getZoom() >= PICTURE_ZOOM;
    if (now === near) return;
    near = now;
    dressAll(['beds', 'containers']);
  }

  // A tooltip already open says the old name until it is told.
  function renamed() {
    for (const group of Object.values(layers)) if (group.isTooltipOpen()) group.getTooltip().update();
    paint();
  }

  // A click or a tap on a named mob's mark, or on its name, is a question
  // about that mob, and is not also a click on the map under it.
  layers.mobs.on('click', (e) => {
    L.DomEvent.stopPropagation(e);
    const entry = named.find((other) => other.marker === e.layer);
    if (entry) examine(entry, false);
  });

  zoomed();
  paint();
  map.on('zoomend', zoomed);
  document.addEventListener('mcmap:icons', () => dressAll(Object.keys(KINDS)));
  document.addEventListener('mcmap:pictures', () => dressAll(Object.keys(KINDS)));
  document.addEventListener('mcmap:names', renamed);
  document.addEventListener('mcmap:live', aside);
  document.addEventListener('mcmap:inspect', current);
  document.addEventListener('mcmap:view', sync);
  document.addEventListener('visibilitychange', sync);
  sync();
})();
