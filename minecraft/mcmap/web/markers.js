'use strict';

// What stays where it is: the player's own waypoints, and the beds,
// containers and named mobs read from the world at the last snapshot. They
// change at most every few minutes, so they are fetched, not streamed. Each
// is drawn as its own picture on a square plate, where the live layer's are
// round, and as the ring it used to be while the server has no picture for
// it. A named mob is its type's icon under its name, and is listed by name
// under its row, since a name is what it is looked for by. While the same
// mob is loaded the live layer draws it where it is, under the same name,
// and the mark the snapshot left steps aside: one animal, one marker. The
// mark is of where the mob was saved, which may be a quarter of an hour
// and a long walk ago, so it is drawn faded in a broken ring and says how
// old it is: it must never pass for the marker of a mob that is there.
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
  const { DENSITY } = icons;
  // What the viewer has chosen for how the map looks, where the page keeps
  // such a thing.
  const settings = window.mcmapSettings || null;
  const look = () => (settings ? settings.look() : {});
  const themed = (name, fallback) => (settings && settings.colour(name)) || fallback;
  const sizes = () => (icons.sizes ? icons.sizes() : { mob: icons.MOB_RADIUS, baby: icons.BABY_RADIUS, plate: icons.PLATE_RADIUS, scale: 1 });
  const naming = (what) => look()[what] || 'always';

  // picture is the one that stands for the whole row in the panel, where
  // a single one can. A kind's colour is the theme's, by the name the
  // stylesheet gives it, and so is the dark every ring is filled with and
  // the colour a name is written in; what is here is what they were
  // before there were themes.
  const KINDS = {
    waypoints: { label: 'Waypoints', color: '#b48cf2', radius: 6, picture: 'marker/waypoint' },
    beds: { label: 'Beds', color: '#f277b5', radius: 4, picture: 'bed/red' },
    containers: { label: 'Containers', color: '#f08a3c', radius: 4, picture: 'container/chest' },
    mobs: { label: 'Named mobs', color: '#4fd1c5', radius: 5 },
  };
  let INK;
  let NAMED;
  function palette() {
    for (const kind of Object.keys(KINDS)) KINDS[kind].color = themed(`marker-${kind}`, KINDS[kind].color);
    INK = themed('marker-ink', '#0b0c0e');
    NAMED = themed('named-text', KINDS.mobs.color);
  }
  palette();
  // What a saved position is drawn at, of the full strength a live marker
  // has, and the ring broken around it.
  const SAVED_ALPHA = 0.55;
  const SAVED_DASH = [3, 3];
  const SAVED_RING = 3;
  // picture -> the same picture faded. The pictures are made once and
  // kept by whoever made them, so each is faded once.
  const faded = new WeakMap();
  function fade(worn) {
    if (!worn) return null;
    let pale = faded.get(worn);
    if (!pale) {
      pale = document.createElement('canvas');
      pale.width = worn.width;
      pale.height = worn.height;
      const ctx = pale.getContext('2d');
      ctx.globalAlpha = SAVED_ALPHA;
      ctx.drawImage(worn, 0, 0);
      faded.set(worn, pale);
    }
    return pale;
  }
  const tagOf = (name) => (naming('labelMobs') === 'always' ? fade(icons.tag(name, NAMED)) : null);
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

  // How old a named mob's mark is, as its tooltip says it. The live layer
  // tells the age, since it knows how far this clock is from the server's.
  function savedSaid() {
    const card = inspect();
    return snapshotAt !== null && card && card.age ? `saved ${card.age(snapshotAt)}` : 'last saved position';
  }

  // Said when it is asked for, so that a name which arrives after the
  // marker was drawn is the one shown.
  function tip(marker) {
    const { kind, data } = marker.options;
    // With names never shown, a named mob and a waypoint are said by what
    // they are.
    const quiet = (kind === 'mobs' && naming('labelMobs') === 'never') || (kind === 'waypoints' && naming('labelWaypoints') === 'never');
    const title = !quiet ? titleOf(kind, data) : kind === 'mobs' ? names.kindOf(data.k, data.b) : 'Waypoint';
    const box = text(kind === 'mobs' ? `${title} · ${savedSaid()} · ${at(data)}` : `${title} · ${at(data)}`);
    box.prepend(icons.picture(pictureOf(kind, data)));
    return box;
  }

  // A marker with, for a named mob, its name over it.
  const Pin = icons.Tagged;

  // A named mob's mark: a saved position, in a broken ring. _renderer,
  // _ctx, _point, _radius and _drawing are Leaflet internals, which is
  // safe only because Leaflet is vendored at a fixed version.
  const Saved = Pin.extend({
    // The canvas only repaints inside the bounds a marker claims.
    _updateBounds() {
      Pin.prototype._updateBounds.call(this);
      const reach = this._radius + SAVED_RING + 2;
      this._pxBounds.extend(this._point.subtract([reach, reach]));
      this._pxBounds.extend(this._point.add([reach, reach]));
    },
    _updatePath() {
      Pin.prototype._updatePath.call(this);
      // Without a picture it is a circle, which Leaflet breaks itself.
      if (!this.options.sprite || !this._renderer._drawing || this._empty()) return;
      const ctx = this._renderer._ctx;
      ctx.save();
      ctx.globalAlpha = 1;
      ctx.beginPath();
      ctx.arc(this._point.x, this._point.y, this._radius + SAVED_RING, 0, Math.PI * 2);
      ctx.setLineDash(SAVED_DASH);
      ctx.lineWidth = 1.5;
      ctx.strokeStyle = this.options.color;
      ctx.stroke();
      ctx.restore();
    },
  });

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
    const size = sizes();
    let worn = null;
    let radius = style.radius * size.scale;
    if (kind === 'mobs') {
      const baby = data.b === true;
      worn = fade(icons.mob(str(data.k), style.color, baby));
      if (worn) radius = baby ? size.baby : size.mob;
      else if (baby) radius = BABY_RADIUS * size.scale;
    } else if (near || kind === 'waypoints') {
      worn = icons.plate(pictureOf(kind, data), style.color);
      if (worn) radius = size.plate;
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
    const saved = kind === 'mobs';
    const marker = new (saved ? Saved : Pin)([m.z + 0.5, m.x + 0.5], {
      renderer,
      radius: style.radius,
      color: style.color,
      weight: look().theme === 'contrast' ? 3 : 2,
      fillColor: INK,
      fillOpacity: saved ? 0.4 : 0.75,
      ...(saved ? { saved: true, opacity: SAVED_ALPHA, dashArray: SAVED_DASH.join(' ') } : {}),
      kind,
      data: m,
      sprite: null,
      tag: kind === 'mobs' ? tagOf(m.n) : null,
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
      if (kind === 'waypoints' && naming('labelWaypoints') === 'always') {
        // A waypoint is the one marker a player put there by name, so the
        // name is always showing unless the viewer has it otherwise; the
        // layer's own tooltip says it under the pointer either way.
        marker.bindTooltip(text(str(m.name) || 'Waypoint'), { permanent: true, direction: 'right', offset: [sizes().plate + 2, 0], className: 'marker-name' });
      }
      layers[kind].addLayer(marker);
      made.push({ data: m, marker, live: null });
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
  // The id the live layer has the entry's mob under now, or null while it
  // is not in the picture.
  const liveOf = (entry) => (entry.live ? entry.live.id : null);
  // What the card is opened under for it, which is how the list knows
  // which of its entries the card is about.
  const keyOf = (entry) => {
    const { data } = entry;
    const id = liveOf(entry) || idOf(data);
    return id ? `m:${id}` : `s:${drawn.dimension}:${data.x + 0.5}:${data.y}:${data.z + 0.5}`;
  };

  const sameAs = (name, type) => `${str(type)}\u0000${str(name)}`;

  // Finds each of the snapshot's mobs in the live picture. The game's id
  // says which it is, and where the snapshot has one nothing else is
  // asked: a loaded mob of the same name under another id is another
  // animal. Only a mob the world gave no id is looked for by its name and
  // type, a guess, and one taken only while it cannot be wrong about
  // which: one mob of that name and type saved, and one loaded.
  function pair() {
    const card = inspect();
    const loaded = card && card.named ? card.named() : [];
    const byId = new Map(loaded.map((mob) => [mob.id, mob]));
    const tally = (list, of) => {
      const counts = new Map();
      for (const item of list) counts.set(of(item), (counts.get(of(item)) || 0) + 1);
      return counts;
    };
    const saved = tally(named, (entry) => sameAs(entry.data.n, entry.data.k));
    const there = tally(loaded, (mob) => sameAs(mob.name, mob.type));
    const claimed = new Set(named.map((entry) => idOf(entry.data)));
    for (const entry of named) {
      const id = idOf(entry.data);
      if (id) {
        entry.live = byId.get(id) || null;
        continue;
      }
      const same = sameAs(entry.data.n, entry.data.k);
      const guess = saved.get(same) === 1 && there.get(same) === 1 ? loaded.find((mob) => sameAs(mob.name, mob.type) === same) : null;
      entry.live = guess && !claimed.has(guess.id) ? guess : null;
    }
  }

  // Opens the card about a named mob: live where the live layer has it,
  // and otherwise where the snapshot left it, said as that.
  function examine(entry, go) {
    const card = inspect();
    const { data } = entry;
    const dimension = drawn.dimension || app.dimension();
    const id = liveOf(entry) || idOf(data);
    const now = card && id ? card.where(id) : null;
    // The middle of the block, not its north-west corner.
    const at = now || { x: data.x + 0.5, y: data.y, z: data.z + 0.5 };
    if (go && dimension) app.go(dimension, at.x, at.z);
    if (!card) return;
    card.open({ kind: 'mob', id, name: nameOf(entry), type: str(data.k), baby: data.b === true, ...at, dimension, saved: true, savedAt: snapshotAt });
  }

  // The name a mob goes by: the one it has now while it is loaded, which
  // may not be the one it was saved under.
  const nameOf = (entry) => (entry.live && str(entry.live.name)) || str(entry.data.n);

  // Brings a mob's entry in the list in line with whether it is loaded.
  function listed(entry) {
    if (!entry.button) return;
    const { data } = entry;
    const name = nameOf(entry) || names.entity(data.k);
    if (entry.label.textContent !== name) entry.label.textContent = name;
    const title = entry.live ? 'Loaded now' : `Last saved position: ${fmt(data.x)}, ${fmt(data.y)}, ${fmt(data.z)}`;
    if (entry.button.title !== title) entry.button.title = title;
    const key = keyOf(entry);
    if (entry.button.dataset.key === key) return;
    entry.button.dataset.key = key;
    // The card may be about this one, under the key it has now.
    current();
  }

  // Takes the mark of a mob the live layer is drawing off the map, and
  // puts back the mark of one it no longer is. The name stays on the map
  // either way: the live marker wears it.
  function aside() {
    const card = inspect();
    if (!card) return;
    pair();
    let back = false;
    for (const entry of named) {
      listed(entry);
      const live = entry.live !== null && card.drawn(entry.live.id);
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
      entry.button = button;
      entry.label = name;
      listed(entry);
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
    // The list's buttons belong to the entries they were built from.
    rosterOf = '';
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
      rosterOf = '';
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

  // The viewer has changed how the map looks: every mark is given the
  // theme's colours, the size chosen and its name or none, in place, from
  // what was already fetched.
  function restyle() {
    palette();
    for (const kind of Object.keys(KINDS)) {
      const marks = kind === 'mobs' ? named.map((entry) => entry.marker) : layers[kind].getLayers();
      for (const marker of marks) {
        const o = marker.options;
        o.color = KINDS[kind].color;
        o.weight = look().theme === 'contrast' ? 3 : 2;
        o.fillColor = INK;
        if (kind === 'mobs') o.tag = tagOf(o.data.n);
        // Dressing redraws a mark only if its picture or size changed.
        dress(marker);
        marker.redraw();
      }
    }
    // A waypoint's name is a label of its own, there or not.
    drawWaypoints(drawn.dimension || app.dimension());
    if (map.hasLayer(layers.waypoints)) stack();
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

  // Whether a marker's block is inside a box of blocks, edges included.
  const inside = (box, d) => d.x >= box.minX && d.x <= box.maxX && d.z >= box.minZ && d.z <= box.maxZ
    && (!Number.isFinite(box.minY) || !Number.isFinite(box.maxY) || (d.y >= box.minY && d.y <= box.maxY));

  app.markers = {
    // For whoever else opens the card on a mob the snapshot placed.
    savedAt: () => snapshotAt,
    // What the snapshot's markers hold inside a box, from what is already
    // loaded for this dimension: beds and containers counted by what they
    // are called, and each named mob with a way to open its card. Null
    // while there are no markers to count.
    within(box) {
      if (drawn.dimension === null) return null;
      const tally = (kind) => {
        const counts = new Map();
        layers[kind].eachLayer((marker) => {
          const { data } = marker.options;
          if (!inside(box, data)) return;
          const title = kind === 'beds' ? names.bed(data.c) : names.holder(data.k, data.c, data.t);
          counts.set(title, (counts.get(title) || 0) + 1);
        });
        return counts;
      };
      return {
        beds: tally('beds'),
        containers: tally('containers'),
        mobs: named.filter((entry) => inside(box, entry.data)).map((entry) => ({
          name: str(entry.data.n) || names.entity(entry.data.k),
          what: names.kindOf(entry.data.k, entry.data.b),
          open: () => examine(entry, true),
        })),
      };
    },
  };

  zoomed();
  paint();
  map.on('zoomend', zoomed);
  document.addEventListener('mcmap:icons', () => dressAll(Object.keys(KINDS)));
  document.addEventListener('mcmap:pictures', () => dressAll(Object.keys(KINDS)));
  document.addEventListener('mcmap:names', renamed);
  document.addEventListener('mcmap:live', aside);
  document.addEventListener('mcmap:inspect', current);
  const styledAs = () => {
    const { theme, size, text, labelMobs, labelWaypoints, picturesLive, picturesMarkers } = look();
    return [theme, size, text, labelMobs, labelWaypoints, picturesLive, picturesMarkers].join('|');
  };
  let styled = styledAs();
  document.addEventListener('mcmap:settings', (e) => {
    if (!e.detail || !e.detail.sections.includes('look') || styled === styledAs()) return;
    styled = styledAs();
    restyle();
  });
  document.addEventListener('mcmap:view', sync);
  document.addEventListener('visibilitychange', sync);
  sync();
})();
