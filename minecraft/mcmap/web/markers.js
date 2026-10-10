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
//
// Each kind's row in the panel lists what it is made of, and each of
// those can be hidden: the containers by what they are, the beds by
// their colour, and every named mob and every waypoint by itself.
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
  const PICTURE_ZOOM = icons.registry ? icons.registry.PICTURES_FROM.markers : -2;
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
  const DIMMED = (icons.states && icons.states.dimmed) || { alpha: 0.55, dash: [3, 3] };
  const SAVED_ALPHA = DIMMED.alpha;
  const SAVED_DASH = DIMMED.dash;
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
  // A saved mob's picture for the panel, one element a mob for as long
  // as its picture is the same one, so that a list built again finds it
  // unchanged and draws nothing.
  const savedPictures = new Map();
  function savedPicture(item, key) {
    let made = savedPictures.get(item);
    if (!made || made.dataset.picture !== key) {
      if (savedPictures.size > 512) savedPictures.clear();
      made = icons.picture(key);
      made.classList.add('saved');
      savedPictures.set(item, made);
    }
    return made;
  }
  const WORLD_KINDS = ['beds', 'containers', 'mobs'];
  const BABY_RADIUS = 4;

  // Each kind's row in the panel, once the service is known to have that
  // kind. One it does not have gets no row.
  const rows = new Map();
  // Which of a kind's items are shown, which the panel keeps. A panel from
  // before it listed any shows them all.
  const choices = {};
  for (const kind of Object.keys(KINDS)) {
    choices[kind] = app.layers.facet ? app.layers.facet('markers', kind) : { shows: () => true, onChange() {} };
  }
  // The colour a bed's line in the panel is keyed by: the game's dyes.
  const DYES = {
    white: '#f9fffe', orange: '#f9801d', magenta: '#c74ebd', light_blue: '#3ab3da', yellow: '#fed83d', lime: '#80c71f',
    pink: '#f38baa', gray: '#474f52', light_gray: '#9d9d97', cyan: '#169c9c', purple: '#8932b8', blue: '#3c44aa',
    brown: '#835432', green: '#5e7c16', red: '#b02e26', black: '#1d1d21',
  };

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

  // The item of its row a marker belongs to: a container's by what it is,
  // a shulker box's by its colour as well, a bed's by its colour, and a
  // named mob or a waypoint by itself. Never a name: a name is a player's
  // to choose, and this is kept.
  const spot = (m) => `${m.x},${m.y},${m.z}`;
  function itemOf(kind, m) {
    if (kind === 'beds') return str(m.c) || 'red';
    if (kind === 'containers') {
      if (m.k === 'shulker') return `shulker-${str(m.c) || 'undyed'}`;
      return m.k === 'chest' && m.t === true ? 'trapped_chest' : str(m.k) || 'unknown';
    }
    if (kind === 'mobs') return typeof m.i === 'string' && m.i !== '' && m.i.length < 60 ? `id:${m.i}` : spot(m);
    return spot(m);
  }

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

  // Every mark made for this dimension, as { data, marker, item }, by
  // kind, whether or not it is on the map; the named mobs in the order
  // listed, each with the mob it is in the live picture, or null.
  const held = { waypoints: [], beds: [], containers: [], mobs: [] };
  // When the snapshot the world's markers were read from was taken, in
  // milliseconds, or null where the answer did not say.
  let snapshotAt = null;
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
      // A mark that is off the map is dressed too, for when it is back.
      held[kind].forEach((entry) => dress(entry.marker));
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
      made.push({ data: m, marker, item: itemOf(kind, m), live: null });
    }
    totals[kind] = made.length;
    held[kind] = kind === 'mobs' ? made.sort((a, b) => str(a.data.n).localeCompare(str(b.data.n)) || a.data.x - b.data.x || a.data.z - b.data.z) : made;
    if (kind === 'mobs') pair();
    place(kind);
  }

  // Puts on the map exactly the marks of a kind that should be there:
  // those whose item is not hidden, less any mob the live layer is drawing
  // where it is now, whose name stays on the map on the live marker.
  function place(kind) {
    const card = kind === 'mobs' ? inspect() : null;
    const group = layers[kind];
    let back = false;
    for (const entry of held[kind]) {
      const want = choices[kind].shows(entry.item) && !(card && entry.live !== null && card.drawn(entry.live.id));
      if (want === group.hasLayer(entry.marker)) continue;
      if (want) group.addLayer(entry.marker); else group.removeLayer(entry.marker);
      back = back || want;
    }
    // A mark put back was added last, and would paint over what moves.
    if (back && map.hasLayer(group)) stack();
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
    const saved = tally(held.mobs, (entry) => sameAs(entry.data.n, entry.data.k));
    const there = tally(loaded, (mob) => sameAs(mob.name, mob.type));
    const claimed = new Set(held.mobs.map((entry) => idOf(entry.data)));
    for (const entry of held.mobs) {
      const id = idOf(entry.data);
      if (id) {
        // The id is enough: a loaded mob the game reports without its
        // name is still that mob, under the name it was saved with.
        entry.live = byId.get(id) || (card && card.where(id) ? { id, name: '', type: str(entry.data.k) } : null);
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

  // --- what each row is made of ----------------------------------------------
  //
  // Said to the panel as plain lists, which it draws. Every name goes as
  // text: a name tag and a waypoint's name are players' to choose.

  const HOLDERS = ['chest', 'trapped_chest', 'barrel'];
  function listOf(kind) {
    if (kind === 'beds' || kind === 'containers') {
      const tally = new Map();
      for (const { data, item } of held[kind]) {
        const entry = tally.get(item);
        if (entry) entry.count += 1;
        else tally.set(item, { id: item, count: 1, data });
      }
      const items = [...tally.values()].map(({ id, count, data }) => (kind === 'beds'
        ? { id, count, label: names.bed(data.c), colour: DYES[id] || '', swatch: 'ring beds' }
        : { id, count, label: names.holder(data.k, data.c, data.t), picture: pictureOf(kind, data), swatch: 'ring containers' }));
      const rank = (item) => (HOLDERS.includes(item.id) ? HOLDERS.indexOf(item.id) : item.id.startsWith('shulker-') ? HOLDERS.length : HOLDERS.length + 1);
      return items.sort((a, b) => rank(a) - rank(b) || a.label.localeCompare(b.label));
    }
    if (kind === 'mobs') {
      return held.mobs.map((entry) => {
        const { data, item } = entry;
        const loaded = entry.live !== null;
        return {
          id: item,
          label: nameOf(entry) || names.entity(data.k),
          // A mob the live layer is not drawing is in the panel as it is
          // on the map: its picture faded, in a broken ring.
          picture: loaded ? pictureOf('mobs', data) : savedPicture(item, pictureOf('mobs', data)),
          swatch: 'ring mobs',
          detail: `${names.kindOf(data.k, data.b)} · ${loaded ? 'loaded' : 'saved'}`,
        };
      });
    }
    return held.waypoints
      .map(({ data, item }) => ({ id: item, label: str(data.name) || 'Waypoint', picture: 'marker/waypoint', swatch: 'ring waypoints' }))
      .sort((a, b) => a.label.localeCompare(b.label));
  }

  // Takes the map to every mark of a kind, or to those of one item of it.
  function zoomTo(kind, item) {
    const bounds = L.latLngBounds([]);
    for (const entry of held[kind]) if (item === undefined || entry.item === item) bounds.extend(entry.marker.getLatLng());
    // No closer than one block to a pixel.
    if (bounds.isValid()) map.fitBounds(bounds, { padding: [40, 40], maxZoom: 0 });
  }

  // Goes to one named mob, opening its card, or to one waypoint.
  function goTo(kind, item) {
    const entry = held[kind].find((other) => other.item === item);
    if (!entry) return;
    if (kind === 'mobs') examine(entry, true);
    else app.go(drawn.dimension || app.dimension(), entry.data.x + 0.5, entry.data.z + 0.5);
  }

  const aside = () => {
    pair();
    place('mobs');
    // Loaded or only saved is said on each one's line.
    if (rows.has('mobs') && rows.get('mobs').setItems) rows.get('mobs').setItems(listOf('mobs'));
  };

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
        const single = kind === 'mobs' || kind === 'waypoints';
        const row = app.layers.register({ group: 'markers', id: kind, label: KINDS[kind].label, order: (at + 1) * 10, swatch: `ring ${kind}`, picture,
          facet: kind,
          actions: { zoom: (item) => zoomTo(kind, item), ...(single ? { go: (item) => goTo(kind, item) } : {}) },
        });
        row.onToggle(paint);
        rows.set(kind, row);
      }
      const was = map.hasLayer(layers[kind]);
      show(kind);
      added = added || (!was && map.hasLayer(layers[kind]));
      if (!rows.has(kind)) return;
      rows.get(kind).setCount(totals[kind]);
      rows.get(kind).setNote(more[kind] > 0 ? `${fmt(more[kind])} more are not shown` : '');
      if (rows.get(kind).setItems) rows.get(kind).setItems(listOf(kind));
    });
    if (added) stack();
    // With the named mobs off, a loaded one goes without its name too.
    if (inspect()) inspect().labels(!rows.has('mobs') || rows.get('mobs').enabled);
  }

  function clear() {
    for (const kind of Object.keys(KINDS)) {
      layers[kind].clearLayers();
      held[kind] = [];
      totals[kind] = null;
      more[kind] = 0;
    }
    drawn = { dimension: null, etag: null };
    waypoints = [];
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
      for (const kind of WORLD_KINDS) {
        layers[kind].clearLayers();
        held[kind] = [];
      }
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
      for (const { marker } of held[kind]) {
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
    const entry = held.mobs.find((other) => other.marker === e.layer);
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
        for (const { data } of held[kind]) {
          if (!inside(box, data)) continue;
          const title = kind === 'beds' ? names.bed(data.c) : names.holder(data.k, data.c, data.t);
          counts.set(title, (counts.get(title) || 0) + 1);
        }
        return counts;
      };
      return {
        beds: tally('beds'),
        containers: tally('containers'),
        mobs: held.mobs.filter((entry) => inside(box, entry.data)).map((entry) => ({
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
  for (const kind of Object.keys(KINDS)) choices[kind].onChange(() => place(kind));
  const styledAs = () => {
    const { theme, size, text, labelMobs, labelWaypoints, picturesLive, picturesMarkers } = look();
    return [theme, size, text, labelMobs, labelWaypoints, picturesLive, picturesMarkers, icons.composedAs ? icons.composedAs() : ''].join('|');
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
