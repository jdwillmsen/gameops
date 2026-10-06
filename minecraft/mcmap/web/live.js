'use strict';

// Players and mobs, drawn where they are now. The server streams one whole
// picture of the current dimension about once a second; this keeps a marker
// per entity and moves it, so a thousand of them cost a thousand position
// updates and one repaint. A mob is drawn as its icon and a player as their
// head where the server has one to give, and as a dot or an arrow where it
// does not.
//
// The viewer chooses how often the picture is redrawn, and may pause it.
// A slower pace keeps the stream and draws the newest frame when one is
// due; a pause closes the stream, so that a paused tab costs the server
// nothing, and leaves the last picture on the map marked as frozen.
(() => {
  const app = window.mcmap;
  // The page and its scripts are cached apart for a few minutes, so just
  // after a release this can meet a page that has no panel yet.
  if (!app || !app.layers || !app.layers.register) return;
  const { map } = app;

  const CONTROL_KEY = 'mcmap.liveControl';
  // Where the layer's one on-and-off switch was kept before it could be
  // paused.
  const OLD_KEY = 'mcmap.live';
  // Seconds between redraws that the viewer may choose from. The first is
  // the server's own pace, and means every frame.
  const INTERVALS = [1, 2, 5, 10, 30];
  // Frames come about a second apart and never exactly, so one arriving
  // this much before it is due is drawn, and a frame already held is kept
  // this much past due in case a newer one is about to arrive.
  const CADENCE_SLACK_MS = 150;
  // How long to leave it after the browser gives up on the stream before
  // asking whether the session is still good.
  const GIVE_UP_RETRY_MS = 5000;
  const READOUT_MS = 250;
  // A player who leaves the picture is looked for in the other dimensions
  // this many times, this far apart: the game takes a few seconds to put
  // someone through a portal, and they are in no list meanwhile.
  const SEEK_TRIES = 3;
  const SEEK_RETRY_MS = 3000;
  // How often to ask which markers have a picture. A player who joins is
  // asked about sooner than this; see wanted().
  const ICONS_MS = 30_000;
  const ICONS_SOON_MS = 5000;

  // Sizes in CSS pixels. A mob's icon is 16 texture pixels drawn one to
  // one inside a ring; a head is 8 or 16 drawn at a whole multiple, so
  // neither is ever blurred. The head is the larger of the two by design:
  // players are what the map is looked at for.
  const DOT_RADIUS = 3.5;
  const MOB_ICON = 16;
  const MOB_RADIUS = 11;
  const HEAD = 24;
  const HEAD_RADIUS = 15;
  // How far past the head the pointer showing a player's heading reaches.
  const POINTER = 11;
  const INK = '#0b0c0e';
  // The side of the card's picture, which is the largest sprite and its
  // edge.
  const PORTRAIT = 32;
  // A finger is given this much around a marker to land on; a dot is too
  // small to tap otherwise.
  const TOUCH_TOLERANCE = 8;

  const CATEGORIES = {
    players: '#ffffff',
    hostile: '#e5534b',
    passive: '#5cc8f0',
    villager: '#d9a441',
    other: '#9aa3ad',
  };
  const ME = '#6ecf7a';

  // Type ids as the server reports them, without the minecraft: prefix.
  // Anything not listed is "other", which is where a mob added by a later
  // game version lands until it is put here.
  const TYPES = {
    hostile: `zombie husk drowned zombie_villager zombie_villager_v2 skeleton stray bogged wither_skeleton
      creeper spider cave_spider enderman endermite silverfish witch slime magma_cube blaze ghast phantom
      guardian elder_guardian shulker pillager vindicator evocation_illager ravager vex hoglin zoglin piglin
      piglin_brute zombie_pigman wither ender_dragon warden breeze creaking`,
    passive: `cow mooshroom pig sheep chicken horse donkey mule skeleton_horse zombie_horse llama trader_llama
      camel rabbit wolf cat ocelot fox parrot bat bee turtle dolphin cod salmon pufferfish tropicalfish squid
      glow_squid axolotl frog tadpole goat panda polar_bear strider allay sniffer armadillo iron_golem
      snow_golem copper_golem happy_ghast`,
    villager: 'villager villager_v2 wandering_trader',
  };
  const categoryOf = new Map();
  for (const [category, ids] of Object.entries(TYPES)) {
    for (const id of ids.split(/\s+/)) categoryOf.set(id, category);
  }

  const LAYERS = [
    ['players', 'Players'],
    ['hostile', 'Hostile'],
    ['passive', 'Passive'],
    ['villager', 'Villagers'],
    ['other', 'Other'],
  ];

  const el = {
    control: document.getElementById('live-control'),
    pause: document.getElementById('live-pause'),
    interval: document.getElementById('live-interval'),
    readout: document.getElementById('live'),
    frozen: document.getElementById('frozen'),
    frozenWhat: document.getElementById('frozen-what'),
    frozenAge: document.getElementById('frozen-age'),
  };

  const card = {
    root: document.getElementById('inspect'),
    picture: document.getElementById('inspect-picture'),
    name: document.getElementById('inspect-name'),
    kind: document.getElementById('inspect-kind'),
    coords: document.getElementById('inspect-coords'),
    x: document.getElementById('inspect-x'),
    y: document.getElementById('inspect-y'),
    z: document.getElementById('inspect-z'),
    note: document.getElementById('inspect-note'),
    what: document.getElementById('inspect-what'),
    when: document.getElementById('inspect-when'),
    follow: document.getElementById('inspect-follow'),
    copy: document.getElementById('inspect-copy'),
    close: document.getElementById('inspect-close'),
  };

  const control = { paused: false, interval: INTERVALS[0] };
  try {
    const saved = JSON.parse(localStorage.getItem(CONTROL_KEY) || 'null');
    if (saved && typeof saved === 'object') {
      if (typeof saved.paused === 'boolean') control.paused = saved.paused;
      if (INTERVALS.includes(saved.interval)) control.interval = saved.interval;
    } else {
      // Whoever had the layer switched off still gets a page that opens
      // no stream.
      const old = JSON.parse(localStorage.getItem(OLD_KEY) || '{}');
      control.paused = Boolean(old) && old.on === false;
    }
  } catch { /* a browser that refuses storage still gets the defaults */ }

  // The layer's rows in the panel, by category, while the service has a
  // live layer at all; null while it does not.
  let rows = null;
  const shown = (category) => rows !== null && rows[category].enabled;

  // One canvas for every marker. Leaflet's default draws each as its own
  // SVG element, which is fine for a grid and not for a thousand mobs moving
  // every second. Only this layer is on the canvas: switching the whole map
  // over would change how everything else on it draws.
  const renderer = L.canvas({ padding: 0.5, tolerance: matchMedia('(pointer: coarse)').matches ? TOUCH_TOLERANCE : 0 });

  // Leaflet's canvas draws at twice the size on a dense screen, so a
  // picture prepared for it is prepared at that size too.
  const DENSITY = L.Browser.retina ? 2 : 1;

  // A picture ready to be stamped onto the canvas: the icon or head, its
  // ring in the category's colour and its backing, composed once. Drawing a
  // marker is then one drawImage, whatever is in the picture.
  function sprite(bitmap, radius, paint) {
    const canvas = document.createElement('canvas');
    canvas.width = canvas.height = radius * 2 * DENSITY;
    const ctx = canvas.getContext('2d');
    ctx.scale(DENSITY, DENSITY);
    // Pixel art scaled by a whole number stays as its author drew it.
    ctx.imageSmoothingEnabled = false;
    paint(ctx, bitmap);
    return canvas;
  }

  function paintMob(colour) {
    return (ctx, bitmap) => {
      const c = MOB_RADIUS;
      ctx.beginPath();
      ctx.arc(c, c, c - 1, 0, Math.PI * 2);
      ctx.fillStyle = 'rgba(11, 12, 14, 0.8)';
      ctx.fill();
      ctx.drawImage(bitmap, c - MOB_ICON / 2, c - MOB_ICON / 2, MOB_ICON, MOB_ICON);
      // The ring is what the filters are read by, so it goes on last and
      // nothing in the icon can cover it.
      ctx.lineWidth = 1.75;
      ctx.strokeStyle = colour;
      ctx.stroke();
    };
  }

  function paintHead(colour) {
    return (ctx, bitmap) => {
      const c = HEAD_RADIUS;
      ctx.fillStyle = INK;
      ctx.fillRect(0, 0, c * 2, c * 2);
      ctx.fillStyle = colour;
      ctx.fillRect(1, 1, c * 2 - 2, c * 2 - 2);
      ctx.drawImage(bitmap, c - HEAD / 2, c - HEAD / 2, HEAD, HEAD);
    };
  }

  // Draws a marker's picture where Leaflet would have drawn its circle.
  // _renderer, _ctx, _point and _drawing are Leaflet internals, which is
  // safe only because Leaflet is vendored at a fixed version.
  function stamp(layer) {
    const ctx = layer._renderer._ctx;
    const p = layer._point;
    const size = layer._radius;
    // Leaflet leaves the last shape's opacity set on the context.
    ctx.globalAlpha = 1;
    ctx.drawImage(layer.options.sprite, Math.round(p.x) - size, Math.round(p.y) - size, size * 2, size * 2);
  }

  // A mob is its icon in a ring, or, with no icon to draw, the dot it has
  // always been.
  const Mob = L.CircleMarker.extend({
    _updatePath() {
      if (!this.options.sprite) {
        L.CircleMarker.prototype._updatePath.call(this);
        return;
      }
      if (this._renderer._drawing && !this._empty()) stamp(this);
    },
  });

  // A player's marker points the way they face. Leaflet's canvas has no
  // such shape, so this draws into the renderer's own context, the way its
  // built-in circle does. With a head to show, the head is the marker and
  // the heading is a pointer at its edge; without one it is the arrow.
  const Arrow = L.CircleMarker.extend({
    // The pointer reaches past the radius, and the canvas only repaints
    // inside the bounds a marker claims.
    _updateBounds() {
      const reach = this._radius + (this.options.sprite ? POINTER : 0) + this._clickTolerance();
      this._pxBounds = L.bounds(this._point.subtract([reach, reach]), this._point.add([reach, reach]));
    },
    _updatePath() {
      const r = this._renderer;
      if (!r._drawing || this._empty()) return;
      const ctx = r._ctx;
      const p = this._point;
      const size = this._radius;
      if (this.options.sprite) {
        if (Number.isFinite(this.options.yaw)) {
          const near = size + 2;
          ctx.beginPath();
          ctx.save();
          ctx.translate(p.x, p.y);
          ctx.rotate((this.options.yaw * Math.PI) / 180);
          ctx.moveTo(0, size + POINTER);
          ctx.lineTo(7, near);
          ctx.lineTo(-7, near);
          ctx.closePath();
          ctx.restore();
          r._fillStroke(ctx, this);
        }
        stamp(this);
        return;
      }
      ctx.beginPath();
      if (Number.isFinite(this.options.yaw)) {
        // Yaw 0 faces south, which is down the map, and turns clockwise
        // seen from above, as a canvas rotation does.
        ctx.save();
        ctx.translate(p.x, p.y);
        ctx.rotate((this.options.yaw * Math.PI) / 180);
        ctx.moveTo(0, size);
        ctx.lineTo(size * 0.75, -size * 0.75);
        ctx.lineTo(0, -size * 0.3);
        ctx.lineTo(-size * 0.75, -size * 0.75);
        ctx.closePath();
        ctx.restore();
      } else {
        ctx.arc(p.x, p.y, size * 0.6, 0, Math.PI * 2, false);
      }
      r._fillStroke(ctx, this);
    },
  });

  // Names reach here from the game, where players choose them, and Leaflet
  // treats a string given to a tooltip as HTML. An element holding text is
  // the form it cannot interpret.
  const text = (s) => {
    const span = document.createElement('span');
    span.textContent = s;
    return span;
  };

  const mobLayer = L.featureGroup();
  const playerLayer = L.featureGroup();
  mobLayer.bindTooltip((marker) => text(marker.options.label), { sticky: true, direction: 'top', className: 'live-tip' });

  // key -> { marker, category, x, y, z, yaw, name, type, pic }, where pic is
  // the address of the picture the marker is wearing, or null for none.
  const entities = new Map();

  // Which markers have a picture, as the server last said: the mob types
  // with an icon, and the players with a head, by gamertag in lower case.
  // Empty is the state before the answer, the state when the server has
  // none to offer, and the state when it could not fetch any; in all three
  // every marker is drawn as it was before there were pictures.
  let pictures = { version: '', types: new Set(), heads: {}, me: '' };
  let picturesAt = 0;
  let picturesOff = typeof createImageBitmap !== 'function';
  let picturesTimer = null;
  // Gamertags on more than one marker in the current frame. A head is
  // asked for by gamertag, so two markers under one cannot both be right.
  let twins = new Set();
  // address -> ImageBitmap once decoded, null while loading or if it
  // could not be. An address carries its picture's version, so a changed
  // picture is a new entry and is fetched once.
  const bitmaps = new Map();
  // address + colour -> the composed sprite.
  const sprites = new Map();
  let dressing = false;

  let me = null;
  let source = null;
  let streamDimension = null;
  // The dimension the markers on the map are of, which outlives the stream
  // when it is paused.
  let pictured = null;
  let retryTimer = null;
  // The newest frame not yet drawn, when the viewer's pace is slower than
  // the server's, and when the last one was.
  let latest = null;
  let drawnAt = 0;
  let cadenceTimer = null;
  // Server clock minus this one, taken from the first frame of a stream.
  let clockOffset = 0;
  let awaitingFirst = false;
  let frameAt = null;
  let frameSeen = 0;
  let ttlMs = 10_000;
  let stale = true;
  let more = 0;

  // The entity the card is about, or null while the card is shut:
  // { key, category, name, type, x, y, z, dimension, seenAt, state, follow,
  // went }. state is live while it is in the picture, waiting while there
  // is no picture to say either way, lost once a frame of its dimension
  // came without it, and away while the map shows another dimension. went
  // is the dimension a lost player was found in, or null.
  let picked = null;
  let seeking = [];
  let seekTimer = null;
  let cardShape = '';

  const fmt = (n) => n.toLocaleString('en-US');
  const layerOf = (category) => (category === 'players' ? playerLayer : mobLayer);

  const readable = (type) => (type || 'unknown').replace(/_/g, ' ');

  function mobLabel(e) {
    return e.n ? `${e.n} (${readable(e.t)})` : readable(e.t);
  }

  // Fetches and decodes a picture once. The marker that wanted it is drawn
  // plain meanwhile and dressed when it arrives.
  function load(address) {
    bitmaps.set(address, null);
    fetch(address)
      .then((res) => (res.ok ? res.blob() : Promise.reject(new Error(String(res.status)))))
      .then((blob) => createImageBitmap(blob))
      .then((bitmap) => {
        bitmaps.set(address, bitmap);
        if (dressing) return;
        // Icons arrive in a burst; one pass dresses all that have.
        dressing = true;
        requestAnimationFrame(dress);
      })
      .catch(() => { /* stays plain; a new version is a new address */ });
  }

  // The sprite for a picture in a colour, or null if the picture is not
  // decoded yet, in which case it is on its way.
  function spriteOf(address, colour, radius, paint) {
    if (!address) return null;
    if (!bitmaps.has(address)) load(address);
    const bitmap = bitmaps.get(address);
    if (!bitmap) return null;
    const key = `${address}|${colour}`;
    let made = sprites.get(key);
    if (!made) {
      made = sprite(bitmap, radius, paint(colour));
      sprites.set(key, made);
    }
    return made;
  }

  const fold = (name) => (typeof name === 'string' ? name.toLowerCase() : '');

  // The address of the picture a marker should wear, or null for none.
  function pictureOf(e, category) {
    if (picturesOff) return null;
    if (category !== 'players') {
      return pictures.types.has(e.t) ? `api/icons/mob/${encodeURIComponent(e.t)}?v=${pictures.version}` : null;
    }
    const key = fold(e.n);
    const version = Object.hasOwn(pictures.heads, key) ? pictures.heads[key] : '';
    // Two markers under one gamertag: neither gets a head, since either
    // could be given the other's.
    if (!version || twins.has(key)) return null;
    return `api/icons/head?name=${encodeURIComponent(key)}&v=${encodeURIComponent(version)}`;
  }

  // The session's own gamertag as the server matched it by XUID, which
  // stays right through a change of gamertag; failing that, the one the
  // session was issued under.
  const isMe = (name) => {
    const mine = pictures.me || me;
    return Boolean(mine) && fold(name) === mine;
  };

  function make(e, category, pic) {
    if (category === 'players') {
      const mine = isMe(e.n);
      const colour = mine ? ME : CATEGORIES.players;
      const worn = spriteOf(pic, colour, HEAD_RADIUS, paintHead);
      const marker = new Arrow([e.z, e.x], {
        renderer,
        radius: worn ? HEAD_RADIUS : (mine ? 10 : 8),
        yaw: e.r,
        color: INK,
        weight: 1.5,
        fillColor: colour,
        fillOpacity: 1,
        sprite: worn,
      });
      marker.bindTooltip(text(e.n || 'Player'), {
        permanent: true,
        direction: 'top',
        offset: [0, worn ? -HEAD_RADIUS : -8],
        className: mine ? 'live-name me' : 'live-name',
      });
      return marker;
    }
    const worn = spriteOf(pic, CATEGORIES[category], MOB_RADIUS, paintMob);
    return new Mob([e.z, e.x], {
      renderer,
      radius: worn ? MOB_RADIUS : DOT_RADIUS,
      color: INK,
      weight: 1,
      fillColor: CATEGORIES[category],
      fillOpacity: 1,
      label: mobLabel(e),
      sprite: worn,
    });
  }

  // Replaces a player's marker with one made from what is held of them.
  function remake(held, pic) {
    playerLayer.removeLayer(held.marker);
    held.pic = pic;
    held.marker = make({ n: held.name, x: held.x, z: held.z, r: held.yaw }, 'players', pic);
    if (shown('players')) playerLayer.addLayer(held.marker);
  }

  // Brings every marker in line with the pictures there are now: called
  // when the list of them changes and when one finishes decoding.
  function dress() {
    dressing = false;
    for (const [key, held] of [...entities]) {
      if (held.category === 'players') {
        // A player's label sits above whatever the marker is, so the
        // marker is made again, in place; there are few of them.
        const pic = pictureOf({ n: held.name }, 'players');
        if (pic !== held.pic || (pic && !held.marker.options.sprite && bitmaps.get(pic))) remake(held, pic);
        continue;
      }
      const pic = pictureOf({ t: held.type }, held.category);
      const worn = spriteOf(pic, CATEGORIES[held.category], MOB_RADIUS, paintMob);
      held.pic = pic;
      if (worn === held.marker.options.sprite) continue;
      held.marker.options.sprite = worn;
      held.marker.setRadius(worn ? MOB_RADIUS : DOT_RADIUS);
    }
  }

  // Asks the server which markers have a picture. Failing to find out
  // changes nothing: what was known stays, and what was plain stays plain.
  async function refreshPictures() {
    if (picturesOff) return;
    picturesAt = Date.now();
    let next;
    try {
      const res = await fetch('api/icons', { cache: 'no-cache' });
      if (res.status === 404) {
        // This server has none to offer, and will not until it restarts.
        picturesOff = true;
        return;
      }
      if (!res.ok) return;
      next = await res.json();
    } catch {
      return;
    }
    const mobs = next.mobs || {};
    const mine = typeof next.me === 'string' ? next.me : '';
    const renamed = mine !== pictures.me;
    pictures = {
      version: String(mobs.version || ''),
      types: new Set(Array.isArray(mobs.types) ? mobs.types : []),
      heads: next.heads && typeof next.heads === 'object' ? next.heads : {},
      me: mine,
    };
    // Sprites of pictures no marker can ask for any more would otherwise
    // pile up for as long as the page is open.
    if (bitmaps.size > 1024) {
      bitmaps.clear();
      sprites.clear();
    }
    if (renamed) for (const held of entities.values()) if (held.category === 'players') remake(held, pictureOf({ n: held.name }, 'players'));
    dress();
  }

  // A player on the map with no head listed has probably just joined, so
  // the list is asked for again soon rather than at its usual interval:
  // once per gamertag, since a player whose skin gives no head stays
  // unlisted for as long as they are online.
  const askedAfter = new Set();
  function wanted(players) {
    if (picturesOff || Date.now() - picturesAt < ICONS_SOON_MS) return;
    let ask = false;
    for (const e of players) {
      const name = fold(e.n);
      if (Object.hasOwn(pictures.heads, name) || askedAfter.has(name)) continue;
      askedAfter.add(name);
      ask = true;
    }
    if (askedAfter.size > 1024) askedAfter.clear();
    if (ask) refreshPictures();
  }

  function forget(key) {
    const held = entities.get(key);
    layerOf(held.category).removeLayer(held.marker);
    entities.delete(key);
  }

  function clear() {
    mobLayer.clearLayers();
    playerLayer.clearLayers();
    entities.clear();
    stale = true;
    more = 0;
    frameAt = null;
    count();
    track(false);
  }

  function draw(frame) {
    const seen = new Set();
    let added = false;
    const names = new Set();
    twins = new Set();
    for (const e of frame.players || []) {
      const name = fold(e.n);
      if (names.has(name)) twins.add(name);
      names.add(name);
    }
    const place = (e, category, prefix) => {
      const key = prefix + e.i;
      seen.add(key);
      let held = entities.get(key);
      // A player is drawn under their gamertag and may be wearing the head
      // that goes with it, so a marker whose gamertag has changed, or which
      // now shares it, is made again rather than moved.
      if (held && (held.category !== category
        || (category === 'players' && (held.name !== e.n || held.pic !== pictureOf(e, category))))) {
        forget(key);
        held = null;
      }
      if (!held) {
        const pic = pictureOf(e, category);
        held = { marker: make(e, category, pic), category, x: e.x, y: e.y, z: e.z, yaw: e.r, name: e.n, type: e.t, pic };
        entities.set(key, held);
        if (shown(category)) layerOf(category).addLayer(held.marker);
        added = true;
        return;
      }
      held.y = e.y;
      held.name = e.n;
      if (held.x !== e.x || held.z !== e.z) {
        held.x = e.x;
        held.z = e.z;
        held.marker.setLatLng([e.z, e.x]);
      }
      if (category === 'players') {
        if (held.yaw !== e.r) {
          held.yaw = e.r;
          held.marker.options.yaw = e.r;
          held.marker.redraw();
        }
      } else {
        held.marker.options.label = mobLabel(e);
      }
    };
    for (const e of frame.players || []) place(e, 'players', 'p:');
    for (const e of frame.mobs || []) place(e, categoryOf.get(e.t) || 'other', 'm:');
    for (const key of [...entities.keys()]) if (!seen.has(key)) forget(key);
    wanted(frame.players || []);
    // The canvas paints in the order markers were added; players stay on
    // top of a mob that arrived after them.
    if (added) playerLayer.eachLayer((marker) => marker.bringToFront());
    count();
    // A stale frame is an empty one because nothing recent is known, which
    // is not the same as the entity being gone.
    track(!stale);
  }

  function count() {
    const totals = { players: 0, hostile: 0, passive: 0, villager: 0, other: 0 };
    if (rows === null) return;
    for (const held of entities.values()) totals[held.category] += 1;
    for (const [category, n] of Object.entries(totals)) rows[category].setCount(stale ? null : n);
  }

  function onFrame(frame) {
    if (awaitingFirst) {
      awaitingFirst = false;
      clockOffset = Date.parse(frame.serverNow) - Date.now();
    }
    frameSeen = Date.now();
    if (Number.isFinite(frame.ttlSeconds) && frame.ttlSeconds > 0) ttlMs = frame.ttlSeconds * 1000;
    // Every frame is the whole picture, so one that was never drawn is
    // simply replaced.
    latest = frame;
    present();
  }

  // Draws the newest frame if one is due at the viewer's pace, and
  // otherwise comes back when it is. At the server's own pace every frame
  // is due.
  function present() {
    clearTimeout(cadenceTimer);
    if (latest === null) return;
    const due = control.interval > INTERVALS[0] ? drawnAt + control.interval * 1000 : 0;
    const now = Date.now();
    if (now < due - CADENCE_SLACK_MS) {
      cadenceTimer = setTimeout(present, due + CADENCE_SLACK_MS - now);
      return;
    }
    const frame = latest;
    latest = null;
    drawnAt = Date.now();
    stale = Boolean(frame.stale);
    more = frame.more || 0;
    frameAt = frame.at ? Date.parse(frame.at) : null;
    draw(frame);
    readout();
  }

  function close() {
    clearTimeout(retryTimer);
    clearTimeout(cadenceTimer);
    clearInterval(picturesTimer);
    picturesTimer = null;
    latest = null;
    if (source) source.close();
    source = null;
    streamDimension = null;
  }

  function open(dimension) {
    close();
    // Resuming, the frozen picture stays until the first frame replaces
    // it; another dimension's is wrong here, not merely old.
    if (dimension !== pictured) clear();
    pictured = dimension;
    streamDimension = dimension;
    awaitingFirst = true;
    drawnAt = 0;
    // What is on the map is given its full time to be replaced before it
    // is taken for abandoned.
    frameSeen = Date.now();
    refreshPictures();
    picturesTimer = setInterval(refreshPictures, ICONS_MS);
    const es = new EventSource(`api/live?dimension=${encodeURIComponent(dimension)}`);
    source = es;
    es.onmessage = (ev) => {
      if (source !== es) return;
      let frame;
      try { frame = JSON.parse(ev.data); } catch { return; }
      onFrame(frame);
    };
    es.onerror = () => {
      if (source !== es) return;
      // A dropped connection the browser retries by itself, and says so by
      // staying in the connecting state.
      if (es.readyState !== EventSource.CLOSED) return;
      // Closed means the answer was not a stream: the session has ended, or
      // the service is refusing new streams. Reloading the map tells which.
      // Logged out, it shows the login and this stays shut; otherwise it
      // announces the view again and the stream is reopened.
      close();
      retryTimer = setTimeout(() => app.reload(), GIVE_UP_RETRY_MS);
    };
  }

  // Brings the stream in line with what should be open now: the current
  // dimension's, and only while there is someone logged in and looking.
  function sync() {
    const available = app.live();
    panel(available);
    el.control.hidden = !available;
    el.readout.hidden = !available;
    const locked = document.body.classList.contains('locked');
    const here = available && !locked ? app.dimension() : null;
    const want = here && !control.paused && !document.hidden ? here : null;
    if (!want) {
      close();
      stopSeeking();
      // Paused, the last picture stays for as long as it is of the
      // dimension being looked at. Hidden or logged out, nothing stays.
      if (!(control.paused && here && here === pictured)) clear();
    } else if (want !== streamDimension) {
      open(want);
      if (me === null && !locked) identify();
    }
    readout();
  }

  // Which marker is the visitor's own. Without a login there is no answer
  // and no marker is singled out.
  async function identify() {
    me = '';
    try {
      const res = await fetch('api/me', { cache: 'no-store' });
      if (!res.ok) return;
      me = String((await res.json()).gamertag || '').toLowerCase();
    } catch { /* the highlight is decoration */ }
    // Players drawn before the answer came are redrawn by the next frame,
    // this time with the visitor's own picked out.
    for (const [key, held] of [...entities]) if (held.category === 'players') forget(key);
  }

  // A length of time as it is said, to the second: how old a frozen
  // picture is has to be readable at a glance.
  function span(seconds) {
    const s = Math.max(0, Math.floor(seconds));
    if (s < 60) return `${s} s`;
    if (s < 3600) return `${Math.floor(s / 60)} min ${s % 60} s`;
    return `${Math.floor(s / 3600)} h ${Math.floor((s % 3600) / 60)} min`;
  }

  function readout() {
    paintCard();
    const age = frameAt === null || stale ? null : Math.max(0, (Date.now() + clockOffset - frameAt) / 1000);
    let state;
    if (control.paused) {
      state = 'paused';
    } else if (!source) {
      state = 'reconnecting';
    } else if (Date.now() - frameSeen > ttlMs && entities.size) {
      // Nothing has arrived for as long as a position is good for, so what
      // is on screen is no longer where anyone is.
      clear();
      state = 'reconnecting';
    } else if (source.readyState !== EventSource.OPEN) {
      state = 'connecting';
    } else if (age === null) {
      state = 'no data';
    } else {
      state = `${age.toFixed(1)} s`;
      if (control.interval > INTERVALS[0]) state += ` · every ${control.interval} s`;
    }
    let line = `live · ${state}`;
    if (more > 0 && !stale) {
      const drawn = [...entities.values()].filter((held) => held.category !== 'players').length;
      line += ` · showing ${fmt(drawn)} of ${fmt(drawn + more)} mobs`;
    }
    el.readout.textContent = line;

    // Said on the map itself as well: a paused picture looks exactly like
    // a current one.
    const frozen = control.paused && !el.readout.hidden;
    el.frozen.hidden = !frozen;
    if (!frozen) return;
    const what = 'Live updates are paused.';
    if (el.frozenWhat.textContent !== what) el.frozenWhat.textContent = what;
    el.frozenAge.textContent = age !== null && entities.size
      ? `Positions are from ${span(age)} ago.`
      : 'No positions are shown.';
  }

  function saveControl() {
    try { localStorage.setItem(CONTROL_KEY, JSON.stringify(control)); } catch { /* not kept, still applied */ }
  }

  function paintControl() {
    el.pause.textContent = control.paused ? 'Resume live' : 'Pause live';
    el.pause.classList.toggle('paused', control.paused);
    el.interval.value = String(control.interval);
  }

  function toggle(category, on) {
    for (const held of entities.values()) {
      if (held.category !== category) continue;
      if (on) layerOf(category).addLayer(held.marker); else layerOf(category).removeLayer(held.marker);
    }
    if (on) playerLayer.eachLayer((marker) => marker.bringToFront());
    ring();
  }

  // Puts the layer's rows in the panel while the service has a live layer
  // and takes them out while it does not.
  function panel(available) {
    if (available === (rows !== null)) return;
    if (!available) {
      for (const row of Object.values(rows)) row.remove();
      rows = null;
      return;
    }
    rows = {};
    LAYERS.forEach(([id, label], at) => {
      rows[id] = app.layers.register({ group: 'live', id, label, order: (at + 1) * 10, swatch: `dot ${id}` });
      rows[id].onToggle((on) => toggle(id, on));
    });
  }

  // --- inspecting and following ------------------------------------------
  //
  // A click on a marker opens a card about that one entity. It is found
  // again in each frame by the id the game gives it, which a player and a
  // mob alike keep for as long as they exist, and never by where it is: an
  // id missing from a whole frame is an entity no longer tracked, and the
  // card says so rather than settle on whatever is nearest.

  const INSPECTED = { color: '#ffffff', weight: 2, dashArray: '4 4' };
  const FOLLOWED = { color: ME, weight: 3, dashArray: null };
  const halo = L.circleMarker([0, 0], { renderer, interactive: false, fill: false, opacity: 1, ...INSPECTED });

  const say = (node, s) => {
    if (node.textContent !== s) node.textContent = s;
  };
  const labelOf = (dimension) => (app.label ? app.label(dimension) : dimension);
  const tenths = (n) => n.toFixed(1);

  // The marker as the map draws it, so that the card and the map plainly
  // show the same thing.
  function portrait(held) {
    const ctx = card.picture.getContext('2d');
    ctx.setTransform(DENSITY, 0, 0, DENSITY, 0, 0);
    ctx.clearRect(0, 0, PORTRAIT, PORTRAIT);
    const worn = held.marker.options.sprite;
    if (worn) {
      const side = worn.width / DENSITY;
      ctx.imageSmoothingEnabled = false;
      ctx.drawImage(worn, (PORTRAIT - side) / 2, (PORTRAIT - side) / 2, side, side);
      return;
    }
    ctx.beginPath();
    ctx.arc(PORTRAIT / 2, PORTRAIT / 2, held.category === 'players' ? 8 : DOT_RADIUS * 2, 0, Math.PI * 2);
    ctx.fillStyle = held.marker.options.fillColor;
    ctx.fill();
    ctx.lineWidth = 1.5;
    ctx.strokeStyle = INK;
    ctx.stroke();
  }

  function ring() {
    const held = picked && picked.state === 'live' ? entities.get(picked.key) : null;
    if (!held) {
      halo.remove();
      return;
    }
    const r = held.marker.getRadius();
    // A head is square, and its corners reach past its radius.
    const reach = held.category === 'players' && held.marker.options.sprite ? r * Math.SQRT2 : r;
    halo.setLatLng([held.z, held.x]);
    halo.setRadius(Math.ceil(reach) + 4);
    halo.setStyle(picked.follow ? FOLLOWED : INSPECTED);
    if (!map.hasLayer(halo)) halo.addTo(map);
    halo.bringToFront();
  }

  function centre() {
    if (!picked || !picked.follow || picked.state !== 'live') return;
    // Moving the view in the middle of a zoom cuts the zoom short. Its end
    // comes back here. _animatingZoom is a Leaflet internal, like those
    // above.
    if (map._animatingZoom) return;
    map.panTo([picked.z, picked.x], { animate: false });
  }

  function stopSeeking() {
    clearTimeout(seekTimer);
    for (const es of seeking) es.close();
    seeking = [];
  }

  // A frame is one dimension's picture, so a player missing from it has
  // either left the game or changed dimension, and only the other
  // dimensions' pictures can say which. Each is asked for once, since a
  // stream opens with the current picture, and shut on its first frame.
  function seek(who, tries) {
    stopSeeking();
    if (picked !== who || who.state !== 'lost' || tries <= 0 || !app.dimensions) return;
    for (const dimension of app.dimensions()) {
      if (dimension === who.dimension) continue;
      const es = new EventSource(`api/live?dimension=${encodeURIComponent(dimension)}`);
      seeking.push(es);
      es.onerror = () => es.close();
      es.onmessage = (ev) => {
        es.close();
        let frame;
        try { frame = JSON.parse(ev.data); } catch { return; }
        if (picked !== who || who.state !== 'lost') return;
        if (!(frame.players || []).some((e) => `p:${e.i}` === who.key)) return;
        who.went = dimension;
        stopSeeking();
        paintCard();
      };
    }
    seekTimer = setTimeout(() => seek(who, tries - 1), SEEK_RETRY_MS);
  }

  // Brings the card in line with the picture. whole says the picture is a
  // frame that lists everything there is, so that an entity not in it is
  // gone; a picture that was merely cleared says nothing about anyone.
  function track(whole) {
    if (!picked) return;
    const held = entities.get(picked.key);
    if (held) {
      if (picked.state === 'lost') stopSeeking();
      Object.assign(picked, {
        state: 'live', went: null, dimension: pictured, seenAt: Date.now(),
        category: held.category, name: held.name, type: held.type, x: held.x, y: held.y, z: held.z,
      });
      portrait(held);
      centre();
    } else if (app.dimension() !== picked.dimension) {
      picked.state = 'away';
      picked.follow = false;
    } else if (!whole) {
      if (picked.state !== 'lost') picked.state = 'waiting';
    } else if (picked.state !== 'lost') {
      picked.state = 'lost';
      picked.follow = false;
      if (picked.category === 'players') seek(picked, SEEK_TRIES);
    }
    ring();
    paintCard();
  }

  function paintCard() {
    if (!card.root) return;
    card.root.hidden = picked === null;
    document.body.classList.toggle('inspecting', picked !== null);
    if (picked === null) return;
    const player = picked.category === 'players';
    // Always as text: a gamertag and a name tag are both a player's choice.
    say(card.name, picked.name || (player ? 'Player' : readable(picked.type)));
    const kind = player ? (isMe(picked.name) ? 'Player (you)' : 'Player') : readable(picked.type);
    say(card.kind, `${kind} · ${labelOf(picked.dimension)}`);
    say(card.x, tenths(picked.x));
    say(card.y, tenths(picked.y));
    say(card.z, tenths(picked.z));

    const ago = span((Date.now() - picked.seenAt) / 1000);
    let what = '';
    let when = `Last seen ${ago} ago.`;
    if (picked.state === 'lost') {
      what = picked.went ? `Left for another dimension: ${labelOf(picked.went)}.` : 'No longer tracked.';
    } else if (picked.state === 'away') {
      what = `Not tracked: the map is on another dimension, ${labelOf(app.dimension())}.`;
    } else if (picked.state === 'waiting') {
      what = 'Waiting for live positions.';
    } else if (control.paused) {
      what = 'Paused.';
      when = `Position is from ${ago} ago.`;
    }
    card.root.classList.toggle('adrift', what !== '');
    card.note.hidden = what === '';
    say(card.what, what);
    say(card.when, what === '' ? '' : when);
    card.follow.disabled = picked.state === 'lost' || picked.state === 'away';
    card.follow.setAttribute('aria-pressed', String(picked.follow));
    // The layer panel stops short of the card on a narrow screen, and the
    // card is as tall as what it has to say.
    if (what !== cardShape) {
      cardShape = what;
      document.body.style.setProperty('--inspect-height', `${card.root.offsetHeight}px`);
    }
  }

  function pick(key) {
    if (!card.root || !entities.has(key)) return;
    stopSeeking();
    picked = { key, follow: false, state: 'live', went: null, dimension: pictured };
    cardShape = null;
    track(false);
  }

  function shut() {
    if (!picked) return;
    const within = card.root.contains(document.activeElement);
    picked = null;
    stopSeeking();
    ring();
    paintCard();
    // Focus left on a hidden button is focus lost to the keyboard.
    if (within) map.getContainer().focus();
  }

  function unfollow() {
    if (!picked || !picked.follow) return;
    picked.follow = false;
    ring();
    paintCard();
  }

  // Leaflet does not report a click on a marker at the end of a drag that
  // began on it, so this is only ever a click or a tap.
  function onPick(e) {
    for (const [key, held] of entities) {
      if (held.marker !== e.layer) continue;
      pick(key);
      return;
    }
  }

  async function copyPosition() {
    if (!picked) return;
    let said = 'Copied';
    try {
      await navigator.clipboard.writeText([picked.x, picked.y, picked.z].map(tenths).join(' '));
    } catch {
      // No clipboard to write to, as on a page not served securely. The
      // numbers on the card are the same text, so they are selected and
      // copied the old way, or left selected for the viewer to copy.
      getSelection().selectAllChildren(card.coords);
      let done = false;
      try { done = document.execCommand('copy'); } catch { /* left selected */ }
      if (!done) said = 'Selected: copy it';
    }
    card.copy.textContent = said;
    setTimeout(() => { card.copy.textContent = 'Copy x y z'; }, 2000);
  }

  if (card.root) {
    card.picture.width = card.picture.height = PORTRAIT * DENSITY;
    mobLayer.on('click', onPick);
    playerLayer.on('click', onPick);
    card.close.addEventListener('click', shut);
    card.copy.addEventListener('click', copyPosition);
    card.follow.addEventListener('click', () => {
      if (!picked) return;
      picked.follow = !picked.follow;
      centre();
      ring();
      paintCard();
    });
    document.addEventListener('keydown', (e) => {
      if (e.key === 'Escape') shut();
    });
    // The viewer taking the map somewhere else is the viewer no longer
    // following. A zoom keeps the entity in the middle and is not that.
    map.on('dragstart', unfollow);
    map.on('keydown', (e) => {
      if (e.originalEvent.key.startsWith('Arrow')) unfollow();
    });
    map.on('zoomend', centre);
  }

  el.pause.addEventListener('click', () => {
    control.paused = !control.paused;
    saveControl();
    paintControl();
    sync();
  });

  el.interval.addEventListener('change', () => {
    const seconds = Number(el.interval.value);
    if (!INTERVALS.includes(seconds)) return;
    control.interval = seconds;
    saveControl();
    // A frame held for the old pace may be due at the new one.
    present();
    readout();
  });

  mobLayer.addTo(map);
  playerLayer.addTo(map);
  paintControl();
  document.addEventListener('mcmap:view', sync);
  document.addEventListener('visibilitychange', sync);
  setInterval(readout, READOUT_MS);
  sync();
})();
