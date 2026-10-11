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
  if (!app || !app.layers || !app.layers.register || !app.icons || !app.names) return;
  // Likewise a page from before the pace could be typed.
  if (!app.duration) return;
  const { map, icons, names, duration } = app;

  // The pace and the pause are kept in the page's one record. A page from
  // before the script that keeps it has none, and they are then read from
  // and written to the key they always had, exactly as they were.
  const settings = window.mcmapSettings || null;
  const CONTROL_KEY = 'mcmap.liveControl';
  // Where the layer's one on-and-off switch was kept before it could be
  // paused.
  const OLD_KEY = 'mcmap.live';
  const look = () => (settings ? settings.look() : {});
  const themed = (name, fallback) => (settings && settings.colour(name)) || fallback;
  // Seconds between redraws that the viewer is offered; any other length
  // between the two bounds may be typed. The shortest is the server's own
  // pace, and means every frame.
  const INTERVALS = [1, 2, 5, 10, 30, 60, 300];
  const MIN_INTERVAL = 1;
  const MAX_INTERVAL = 86_400;
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
  // Which markers have a picture is asked every so often by the script
  // that keeps the pictures. A player who joins is asked about sooner
  // than that, but no sooner than this after the last asking; see
  // wanted().
  const ICONS_SOON_MS = 5000;

  // Sizes in CSS pixels. A head is 8 or 16 texture pixels drawn at a
  // whole multiple, so it is never blurred, and is larger than a mob's
  // icon by design: players are what the map is looked at for.
  const DOT_RADIUS = 3.5;
  // Asked for when used: the screen's density can change during a visit.
  const density = () => (icons.density ? icons.density() : icons.DENSITY);
  // The sizes the viewer has chosen: a mob's ring, a head and its backing,
  // and a dot. Asked for when used, since the viewer may change them.
  const sizes = () => (icons.sizes ? icons.sizes() : { mob: icons.MOB_RADIUS, head: 24, headRadius: 15, scale: 1 });
  // How far outside a marker the ring round the inspected one is drawn,
  // which is the same for every layer's markers.
  const SELECTED_GAP = (icons.states && icons.states.selected.gap) || 4;
  // How far past the head the pointer showing a player's heading reaches.
  const POINTER = 11;
  // The side of the card's picture, which is the largest sprite and its
  // edge.
  const PORTRAIT = 32;
  // A finger is given this much around a marker to land on; a dot is too
  // small to tap otherwise.
  const TOUCH_TOLERANCE = 8;

  // The colours are the theme's, by the names the stylesheet gives them,
  // so that a row's key in the panel and its markers on the map are one
  // colour; the values here are what they were before there were themes.
  // INK is the outline every marker is drawn with, and NAMED what a name
  // tag is written in, on a mob that is loaded and on the mark the
  // snapshot left of one that is not.
  const CATEGORIES = {};
  let ME;
  let INK;
  let NAMED;
  function palette() {
    Object.assign(CATEGORIES, {
      players: themed('live-players', '#ffffff'),
      hostile: themed('live-hostile', '#e5534b'),
      passive: themed('live-passive', '#5cc8f0'),
      villager: themed('live-villager', '#d9a441'),
      other: themed('live-other', '#9aa3ad'),
    });
    ME = themed('live-me', '#6ecf7a');
    INK = themed('marker-ink', '#0b0c0e');
    NAMED = themed('named', '#4fd1c5');
  }
  palette();

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
    age: document.getElementById('live-age'),
    more: document.getElementById('live-more'),
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
    go: document.getElementById('inspect-go'),
    said: document.getElementById('inspect-said'),
    copy: document.getElementById('inspect-copy'),
    close: document.getElementById('inspect-close'),
  };

  const control = { paused: false, interval: MIN_INTERVAL };
  // Any length that was ever kept is still one: the menu's entries have
  // changed and may again.
  const paceOf = (kept) => (Number.isFinite(kept) ? Math.min(MAX_INTERVAL, Math.max(MIN_INTERVAL, Math.round(kept))) : MIN_INTERVAL);
  if (settings) {
    const saved = settings.get('live');
    control.paused = saved.paused === true;
    control.interval = paceOf(saved.interval);
  } else {
    try {
      const saved = JSON.parse(localStorage.getItem(CONTROL_KEY) || 'null');
      if (saved && typeof saved === 'object') {
        if (typeof saved.paused === 'boolean') control.paused = saved.paused;
        control.interval = paceOf(saved.interval);
      } else {
        // Whoever had the layer switched off still gets a page that opens
        // no stream.
        const old = JSON.parse(localStorage.getItem(OLD_KEY) || '{}');
        control.paused = Boolean(old) && old.on === false;
      }
    } catch { /* a browser that refuses storage still gets the defaults */ }
  }

  // The layer's rows in the panel, by category, while the service has a
  // live layer at all; null while it does not.
  let rows = null;
  const shown = (category) => rows !== null && rows[category].enabled;

  // --- which of them are drawn ---------------------------------------------
  //
  // Under the players' row is each player, and under each of the mobs'
  // rows each type it holds, and each of those can be hidden or shown
  // alone. There is one choice for the mobs, across their four rows, and
  // one for the players: "only creepers" means the creepers and no other
  // mob, whichever row the others are in. The panel keeps both and says
  // which of them are shown. A panel from before it did keeps the same
  // choices under the same names, which are then read once and followed.
  function choiceOf(name) {
    if (app.layers.facet) return app.layers.facet('live', name);
    const kept = app.layers.recall ? app.layers.recall('live', name) : null;
    const text = (v) => typeof v === 'string' && v.length <= 64;
    const only = kept && text(kept.only) ? kept.only : null;
    const hidden = new Set(kept && Array.isArray(kept.hidden) ? kept.hidden.filter(text) : []);
    return { only, shows: (sort) => (only !== null ? only === sort : !hidden.has(sort)), onChange() {} };
  }
  const choices = { mobs: choiceOf('mobs'), players: choiceOf('players') };
  const domainOf = (category) => (category === 'players' ? 'players' : 'mobs');
  // What an entity is filtered by: a mob by its type, a player by their
  // gamertag as the game compares it.
  const sortOf = (category, name, type) => (category === 'players' ? (typeof name === 'string' ? name.toLowerCase() : '') : (typeof type === 'string' ? type : ''));
  const passes = (category, sort) => choices[domainOf(category)].shows(sort);
  // Whether an entity is on the map: its row is on and its kind is not
  // filtered out. Two set lookups, which is all a frame of a thousand mobs
  // pays for the filters.
  const visible = (held) => shown(held.category) && passes(held.category, held.sort);

  // One canvas for every marker. Leaflet's default draws each as its own
  // SVG element, which is fine for a grid and not for a thousand mobs moving
  // every second. Only this layer and those that ask for its renderer are
  // on the canvas: switching the whole map over would change how everything
  // else on it draws.
  const renderer = L.canvas({ padding: 0.5, tolerance: matchMedia('(pointer: coarse)').matches ? TOUCH_TOLERANCE : 0 });
  // Leaflet stretches the canvas as one picture while a zoom is animated,
  // and a marker on it would swell and shrink with it; drawn through
  // this, it keeps its size as a marker that is an element does.
  const steady = icons.steady || ((layer, draw) => draw());
  if (icons.steadies) icons.steadies(renderer);

  // A head on its backing, bordered in the player's colour.
  function paintHead(border) {
    return (ctx, bitmap) => {
      const { head, headRadius: c } = sizes();
      ctx.fillStyle = INK;
      ctx.fillRect(0, 0, c * 2, c * 2);
      ctx.fillStyle = border;
      ctx.fillRect(1, 1, c * 2 - 2, c * 2 - 2);
      ctx.drawImage(bitmap, c - head / 2, c - head / 2, head, head);
    };
  }

  // A mob is its icon in a ring, or, with no icon to draw, the dot it has
  // always been, under its name where someone gave it one.
  const Mob = icons.Tagged;
  // Whether a named mob's name is written over it: while the named mobs'
  // own row is on, and the viewer has names always showing and not only
  // under the pointer, or never.
  let tagging = true;
  const naming = (what) => look()[what] || 'always';
  const tagOf = (name) => (tagging && name && naming('labelMobs') === 'always' ? icons.tag(name, themed('named-text', NAMED)) : null);
  // Every outline is heavier in a theme made for contrast.
  const heavy = () => (look().theme === 'contrast' ? 1 : 0);

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
      if (!this._renderer._drawing || this._empty()) return;
      steady(this, () => this._arrow());
    },
    _arrow() {
      const r = this._renderer;
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
        icons.stamp(this, this.options.sprite);
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
  // Said when it is asked for, so that a name which arrives later is the
  // one shown.
  mobLayer.bindTooltip((marker) => text(names.mob(naming('labelMobs') === 'never' ? '' : marker.options.name, marker.options.type)), { sticky: true, direction: 'top', className: 'live-tip' });

  // key -> { marker, category, x, y, z, yaw, name, type, pic }, where pic is
  // the address of the head a player's marker is wearing, or null for none.
  const entities = new Map();

  // The session's own gamertag as the server last matched it by XUID.
  let listedAs = '';
  // Gamertags on more than one marker in the current frame. A head is
  // asked for by gamertag, so two markers under one cannot both be right.
  let twins = new Set();

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
  // { key, category, name, type, baby, x, y, z, dimension, seenAt, state,
  // follow, went, savedAt, wasLive }. state is live while it is in the
  // picture, waiting while there is no picture to say either way, lost
  // once a frame of its dimension came without it, saved for a named mob
  // known only from the snapshot, and away while the map shows another
  // dimension. went is the dimension a lost player was found in, or null.
  // savedAt is when the snapshot that placed a named mob was taken, for one
  // chosen by its mark or from a list; wasLive is whether the card has
  // seen it in the picture since it opened. saved marks a named mob placed
  // by the snapshot. sought is when a search said it was live, for one
  // chosen from a search: the server has every dimension's newest picture
  // and this page only the last frame it drew, which is older while the
  // page is paused or paced slowly.
  let picked = null;
  let seeking = [];
  let seekTimer = null;
  let cardShape = '';

  const fmt = (n) => n.toLocaleString('en-US');
  const layerOf = (category) => (category === 'players' ? playerLayer : mobLayer);

  const fold = (name) => (typeof name === 'string' ? name.toLowerCase() : '');

  // The address of the head a player's marker should wear, or null for
  // none.
  function headOf(name) {
    const key = fold(name);
    const heads = icons.listing().heads;
    const version = Object.hasOwn(heads, key) ? heads[key] : '';
    // Two markers under one gamertag: neither gets a head, since either
    // could be given the other's.
    if (!version || twins.has(key)) return null;
    return `api/icons/head?name=${encodeURIComponent(key)}&v=${encodeURIComponent(version)}`;
  }

  // The session's own gamertag as the server matched it by XUID, which
  // stays right through a change of gamertag; failing that, the one the
  // session was issued under.
  const isMe = (name) => {
    const own = listedAs || me;
    return Boolean(own) && fold(name) === own;
  };

  // The colour a player is drawn in, which is also the colour of anything
  // else on the map that is theirs.
  const playerColour = (name) => (isMe(name) ? ME : CATEGORIES.players);

  function make(e, category, pic) {
    if (category === 'players') {
      const mine = isMe(e.n);
      const colour = playerColour(e.n);
      const size = sizes();
      const worn = look().picturesLive === false ? null : icons.sprite(pic, colour, size.headRadius, paintHead);
      const radius = worn ? size.headRadius : (mine ? 10 : 8) * size.scale;
      const marker = new Arrow([e.z, e.x], {
        renderer,
        radius,
        yaw: e.r,
        color: INK,
        weight: 1.5 + heavy(),
        fillColor: colour,
        fillOpacity: 1,
        sprite: worn,
      });
      // A gamertag over the marker always, only under the pointer, or not
      // at all, as the viewer has it.
      if (naming('labelPlayers') !== 'never') {
        marker.bindTooltip(text(e.n || 'Player'), {
          permanent: naming('labelPlayers') === 'always',
          direction: 'top',
          offset: [0, -radius],
          className: mine ? 'live-name me' : 'live-name',
        });
      }
      return marker;
    }
    const worn = icons.mob(e.t, CATEGORIES[category]);
    return new Mob([e.z, e.x], {
      renderer,
      radius: worn ? sizes().mob : DOT_RADIUS * sizes().scale,
      color: INK,
      weight: 1 + heavy(),
      fillColor: CATEGORIES[category],
      fillOpacity: 1,
      name: e.n,
      type: e.t,
      sprite: worn,
      tag: tagOf(e.n),
    });
  }

  // Replaces a player's marker with one made from what is held of them.
  function remake(held, pic) {
    playerLayer.removeLayer(held.marker);
    held.pic = pic;
    held.marker = make({ n: held.name, x: held.x, z: held.z, r: held.yaw }, 'players', pic);
    if (visible(held)) playerLayer.addLayer(held.marker);
  }

  // Brings every marker in line with the pictures there are now: called
  // when the list of them changes and when one finishes decoding.
  function dress() {
    for (const [key, held] of [...entities]) {
      if (held.category === 'players') {
        // A player's label sits above whatever the marker is, so the
        // marker is made again, in place; there are few of them.
        const pic = headOf(held.name);
        if (pic !== held.pic || (pic && !held.marker.options.sprite && look().picturesLive !== false && icons.bitmap(pic))) remake(held, pic);
        continue;
      }
      const worn = icons.mob(held.type, CATEGORIES[held.category]);
      if (worn === held.marker.options.sprite) continue;
      held.marker.options.sprite = worn;
      held.marker.setRadius(worn ? sizes().mob : DOT_RADIUS * sizes().scale);
    }
  }

  // The viewer has changed how the map looks: every marker is given the
  // theme's colours, the size chosen and its name or none, in place. No
  // marker is made again but a player's, whose label is part of it, and
  // nothing is asked of the server.
  function restyle() {
    palette();
    for (const held of entities.values()) {
      if (held.category === 'players') {
        remake(held, held.pic);
        continue;
      }
      const o = held.marker.options;
      o.color = INK;
      o.weight = 1 + heavy();
      o.fillColor = CATEGORIES[held.category];
      o.tag = tagOf(held.name);
      o.sprite = icons.mob(held.type, CATEGORIES[held.category]);
      held.marker.setRadius(o.sprite ? sizes().mob : DOT_RADIUS * sizes().scale);
    }
    INSPECTED.color = themed('live-players', '#ffffff');
    FOLLOWED.color = ME;
    // Every group's mark is made again in what is now chosen.
    for (const marker of heaps.values()) heapLayer.removeLayer(marker);
    heaps.clear();
    regroup();
    ring();
    if (picked) portrait(entities.get(picked.key) || picked);
    document.dispatchEvent(new CustomEvent('mcmap:live'));
  }

  // The list of pictures has changed: a head or an icon has come or gone,
  // or the session's player has another gamertag.
  function relist() {
    const now = icons.listing().me;
    const renamed = now !== listedAs;
    listedAs = now;
    if (renamed) for (const held of entities.values()) if (held.category === 'players') remake(held, headOf(held.name));
    dress();
  }

  // A player on the map with no head listed has probably just joined, so
  // the list is asked for again soon rather than at its usual interval:
  // once per gamertag, since a player whose skin gives no head stays
  // unlisted for as long as they are online.
  const askedAfter = new Set();
  function wanted(players) {
    if (!icons.decodes || Date.now() - icons.askedAt() < ICONS_SOON_MS) return;
    const heads = icons.listing().heads;
    let ask = false;
    for (const e of players) {
      const name = fold(e.n);
      if (Object.hasOwn(heads, name) || askedAfter.has(name)) continue;
      askedAfter.add(name);
      ask = true;
    }
    if (askedAfter.size > 1024) askedAfter.clear();
    if (ask) icons.refresh();
  }

  function forget(key) {
    const held = entities.get(key);
    layerOf(held.category).removeLayer(held.marker);
    entities.delete(key);
  }

  // --- groups ----------------------------------------------------------------
  //
  // From far out, where the viewer has it so, whatever falls in one
  // square of the screen is one mark that says how many there are and of
  // which families: the mobs here, and the marks any other layer offers.
  // A player is never one of several, nor a mob somebody named, nor the
  // one the card is about: each of those is somebody.

  const heapLayer = L.featureGroup();
  // A group's key -> its mark, and what the sorting remembers.
  const heaps = new Map();
  let heapMemory = null;
  const familyColour = (family) => {
    if (Object.hasOwn(CATEGORIES, family)) return CATEGORIES[family];
    const source = app.groups.sources.get(family);
    return (source && source.colour()) || CATEGORIES.other;
  };
  const familyName = (family) => {
    const row = LAYERS.find(([id]) => id === family);
    if (row) return row[1];
    const source = app.groups.sources.get(family);
    return source ? source.label : family;
  };
  const memberName = (family, type) => {
    const source = app.groups.sources.get(family);
    return source ? source.title(type) : names.entity(type);
  };

  // What a group holds, as lines of text: each family and how many, and
  // under it each type, most first.
  function heapLines(group, most) {
    const lines = [];
    for (const [family, n] of group.families) {
      lines.push({ text: `${fmt(n)} ${familyName(family)}`, family: true });
      for (const [of, type, count] of group.types) if (of === family) lines.push({ text: `${fmt(count)} × ${memberName(family, type)}`, of, type });
    }
    const types = lines.filter((line) => !line.family);
    if (types.length <= most) return lines;
    // Past what there is room for, the families alone.
    return [...lines.filter((line) => line.family), { text: `in ${fmt(types.length)} types`, family: true }];
  }

  function heapTip(marker) {
    const box = document.createElement('div');
    for (const line of heapLines(marker.options.heap, 8)) {
      const row = document.createElement('div');
      row.textContent = line.text;
      box.append(row);
    }
    return box;
  }
  heapLayer.bindTooltip(heapTip, { sticky: true, direction: 'top', className: 'live-tip' });

  // Brings the marks on the map in line with how everything now groups:
  // what is in a group is off the map and its group's mark on it.
  function regroup() {
    if (!app.groups || !app.groups.drawnBy || !icons.group) return;
    const zoom = map.getZoom();
    const on = icons.grouping() && app.groups.active(zoom);
    const items = [];
    if (on && !stale) {
      for (const [key, held] of entities) {
        if (held.category === 'players' || !visible(held)) continue;
        items.push({ id: key, x: held.x, z: held.z, family: held.category, type: held.type, alone: Boolean(held.name) || (picked !== null && picked.key === key) });
      }
    }
    if (on) for (const source of app.groups.sources.values()) for (const item of source.items()) items.push(item);
    const { groups, memory } = app.groups.cluster(items, zoom, heapMemory);
    heapMemory = memory;
    const within = new Set();
    const kept = new Set();
    let added = false;
    for (const g of groups) {
      kept.add(g.key);
      for (const m of g.members) within.add(m.id);
      const worn = icons.group(g.families.map(([family, n]) => [familyColour(family), n]), g.members.length);
      let marker = heaps.get(g.key);
      if (!marker) {
        marker = new Mob([g.z, g.x], { renderer, radius: worn.reach, color: INK, weight: 1, fillColor: INK, fillOpacity: 1, sprite: worn, heap: g });
        heaps.set(g.key, marker);
        heapLayer.addLayer(marker);
        // Under everything that is somebody or something by itself.
        marker.bringToBack();
        added = true;
        continue;
      }
      marker.options.heap = g;
      // The same square by number is another place at another zoom.
      const at = marker.getLatLng();
      if (at.lat !== g.z || at.lng !== g.x) marker.setLatLng([g.z, g.x]);
      if (marker.options.sprite !== worn) {
        marker.options.sprite = worn;
        marker.setRadius(worn.reach);
      }
    }
    for (const [key, marker] of heaps) {
      if (kept.has(key)) continue;
      heapLayer.removeLayer(marker);
      heaps.delete(key);
    }
    for (const [key, held] of entities) {
      if (held.category === 'players') continue;
      held.grouped = within.has(key);
      const want = visible(held) && !held.grouped;
      if (want === mobLayer.hasLayer(held.marker)) continue;
      if (want) mobLayer.addLayer(held.marker); else mobLayer.removeLayer(held.marker);
    }
    for (const source of app.groups.sources.values()) source.apply(within);
    // A line of where someone has been stays under the marks.
    if (added && app.trails && app.trails.back) app.trails.back();
    if (listed && !heaps.has(listed)) shutList();
  }

  // --- a group's members, to choose one from ------------------------------------
  //
  // A click or a tap on a group's mark takes the map to its members, and
  // no closer than a block to the pixel, where nothing is grouped. Where
  // that would not part them, since they stand within a few blocks of
  // each other, and when a group is reached from the keyboard, its
  // members are listed instead, and one can be chosen from the list.

  const list = document.createElement('section');
  list.className = 'heap-list';
  list.hidden = true;
  list.setAttribute('aria-label', 'Group of markers');
  const listTitle = document.createElement('h2');
  const listShut = document.createElement('button');
  listShut.type = 'button';
  listShut.textContent = 'Close';
  const listHead = document.createElement('div');
  listHead.className = 'heap-head';
  listHead.append(listTitle, listShut);
  const listBody = document.createElement('div');
  listBody.className = 'heap-body';
  list.append(listHead, listBody);
  (map.getContainer().parentNode || document.body).append(list);
  // The key of the group the list is of, or null while it is shut.
  let listed = null;

  function shutList() {
    if (listed === null) return;
    const marker = heaps.get(listed);
    if (marker) {
      marker._hovered = false;
      marker.redraw();
    }
    const within = list.contains(document.activeElement);
    listed = null;
    list.hidden = true;
    if (within) map.getContainer().focus();
  }

  function fit(group) {
    const bounds = L.latLngBounds([]);
    for (const m of group.members) bounds.extend([m.z, m.x]);
    if (bounds.isValid()) map.fitBounds(bounds, { padding: [60, 60], maxZoom: 0 });
  }

  // One of a group chosen: a mob's card is opened on it, which draws it
  // by itself, and anything else is gone to.
  function choose(group, family, type) {
    const member = group.members.find((m) => m.family === family && m.type === type);
    shutList();
    if (!member) return;
    if (typeof member.id === 'string' && entities.has(member.id)) pick(member.id);
    else map.setView([member.z, member.x], 0);
  }

  function openList(key) {
    const marker = heaps.get(key);
    if (!marker) return false;
    shutList();
    const group = marker.options.heap;
    listed = key;
    marker._hovered = true;
    marker.redraw();
    listTitle.textContent = `Group of ${fmt(group.members.length)}`;
    const rows = [];
    const zoom = document.createElement('button');
    zoom.type = 'button';
    zoom.textContent = 'Zoom to these';
    zoom.addEventListener('click', () => {
      shutList();
      fit(group);
    });
    rows.push(zoom);
    for (const line of heapLines(group, 40)) {
      if (line.family || line.type === undefined) {
        const head = document.createElement('h3');
        head.textContent = line.text;
        rows.push(head);
        continue;
      }
      const row = document.createElement('button');
      row.type = 'button';
      row.textContent = line.text;
      row.addEventListener('click', () => choose(group, line.of, line.type));
      rows.push(row);
    }
    listBody.replaceChildren(...rows);
    list.hidden = false;
    zoom.focus();
    return true;
  }
  listShut.addEventListener('click', shutList);
  list.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape') return;
    e.stopPropagation();
    shutList();
  });

  heapLayer.on('click', (e) => {
    L.DomEvent.stopPropagation(e);
    const group = e.layer.options.heap;
    if (app.groups.together(group.members)) openList(group.key);
    else fit(group);
  });

  // For the keyboard: the next group from the middle of the map outwards,
  // its members listed. False where nothing is grouped.
  function nextGroup() {
    if (heaps.size === 0) return false;
    const middle = map.getCenter();
    const order = [...heaps.values()]
      .map((marker) => [Math.hypot(marker.getLatLng().lat - middle.lat, marker.getLatLng().lng - middle.lng), marker.options.heap.key])
      .sort((a, b) => a[0] - b[0] || a[1].localeCompare(b[1]))
      .map(([, key]) => key);
    return openList(order[(order.indexOf(listed) + 1) % order.length]);
  }

  function clear() {
    mobLayer.clearLayers();
    playerLayer.clearLayers();
    entities.clear();
    stale = true;
    more = 0;
    frameAt = null;
    count();
    regroup();
    track(false);
    document.dispatchEvent(new CustomEvent('mcmap:live'));
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
        || (category === 'players' && (held.name !== e.n || held.pic !== headOf(e.n))))) {
        forget(key);
        held = null;
      }
      if (!held) {
        const pic = category === 'players' ? headOf(e.n) : null;
        held = { marker: make(e, category, pic), category, sort: sortOf(category, e.n, e.t), x: e.x, y: e.y, z: e.z, yaw: e.r, name: e.n, type: e.t, pic };
        entities.set(key, held);
        if (visible(held)) layerOf(category).addLayer(held.marker);
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
      } else if (held.marker.options.name !== e.n) {
        held.marker.options.name = e.n;
        held.marker.options.tag = tagOf(e.n);
        held.marker.redraw();
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
    regroup();
    // A stale frame is an empty one because nothing recent is known, which
    // is not the same as the entity being gone.
    track(!stale);
    // Where the players are, for a layer that draws where they have been.
    if (!stale) {
      document.dispatchEvent(new CustomEvent('mcmap:players', { detail: { dimension: pictured, players: frame.players || [] } }));
    }
    // And which mobs are loaded, for the layer that marks where the
    // snapshot left them.
    document.dispatchEvent(new CustomEvent('mcmap:live'));
  }

  // A player's head for their line in the panel, drawn once the picture
  // of it has arrived; until then it is hidden and their dot stands in.
  const faces = new Map();
  function face(name) {
    const address = headOf(name);
    if (!address) return null;
    let canvas = faces.get(address);
    if (!canvas) {
      if (faces.size > 256) faces.clear();
      canvas = document.createElement('canvas');
      canvas.className = 'picture';
      canvas.setAttribute('aria-hidden', 'true');
      canvas.hidden = true;
      faces.set(address, canvas);
    }
    // Made for another density, it is made again for this one.
    if (canvas.width !== 16 * density()) {
      canvas.width = canvas.height = 16 * density();
      canvas.hidden = true;
    }
    if (canvas.hidden) {
      const drawn = look().picturesLive === false ? null : icons.bitmap(address);
      if (drawn) {
        const ctx = canvas.getContext('2d');
        ctx.imageSmoothingEnabled = false;
        ctx.drawImage(drawn, 0, 0, canvas.width, canvas.height);
        canvas.hidden = false;
      }
    }
    return canvas;
  }

  // Tells the panel what each row holds: how many, and each player or
  // each type of mob as an item of its row, by name. The panel draws only
  // what has changed, so this is said with every frame. Every name goes
  // as text: a gamertag is a player's choice, and a type the server's word.
  function count() {
    if (rows === null) return;
    // category -> sort -> { n, name }, where name is a player's gamertag
    // as they write it.
    const tally = {};
    const totals = {};
    for (const [id] of LAYERS) {
      tally[id] = new Map();
      totals[id] = 0;
    }
    for (const held of entities.values()) {
      totals[held.category] += 1;
      const of = tally[held.category];
      const entry = of.get(held.sort);
      if (entry) entry.n += 1;
      else of.set(held.sort, { n: 1, name: held.name });
    }
    // What is shown alone is listed even while none of it is about, or
    // there would be nothing to say what the map is waiting for.
    const alone = choices.mobs.only;
    if (!stale && alone !== null && typeof alone === 'string') {
      const home = categoryOf.get(alone) || 'other';
      if (!tally[home].has(alone)) tally[home].set(alone, { n: 0 });
    }
    for (const [category] of LAYERS) {
      const row = rows[category];
      row.setCount(stale ? null : totals[category]);
      if (!row.setItems) continue;
      const listed = stale ? [] : [...tally[category]];
      const items = category === 'players'
        ? listed.map(([sort, entry]) => ({ id: sort, label: entry.name || sort || 'Player', picture: face(entry.name), swatch: 'dot players', detail: isMe(entry.name) ? 'you' : '', zoom: false }))
        : listed.map(([sort, entry]) => ({ id: sort, label: names.entity(sort), picture: `mob/${sort}`, swatch: `dot ${category}`, count: entry.n }));
      // By name, so that a line stays where it is while its count moves.
      row.setItems(items.sort((a, b) => a.label.localeCompare(b.label, undefined, { sensitivity: 'base' })));
    }
  }

  // Takes the map to everything of one row, or of one type in it, that is
  // in the picture now.
  function zoomTo(category, sort) {
    const bounds = L.latLngBounds([]);
    for (const held of entities.values()) {
      if (held.category === category && (sort === undefined || held.sort === sort)) bounds.extend([held.z, held.x]);
    }
    // No closer than one block to a pixel: one mob would otherwise fill
    // the screen with the block it stands on.
    if (bounds.isValid()) map.fitBounds(bounds, { padding: [40, 40], maxZoom: 0 });
  }

  function goToPlayer(sort) {
    for (const [key, held] of entities) {
      if (held.category !== 'players' || held.sort !== sort) continue;
      if (app.go) app.go(pictured, held.x, held.z);
      pick(key);
      return;
    }
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
    // Through a pinch the canvas is still of the zoom the pinch began at,
    // stretched, while the map is already of another: a marker moved now
    // would be placed by the one and painted on the other, somewhere else.
    // The end of the zoom comes back here. _zoom is a Leaflet internal,
    // like those above.
    if (renderer._zoom !== undefined && renderer._zoom !== map.getZoom()) return;
    const due = control.interval > MIN_INTERVAL ? drawnAt + control.interval * 1000 : 0;
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
    // What the footer says of the live picture: how old it is, or why
    // there is none, and after it whatever qualifies that.
    let state;
    const extras = [];
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
      // To the tenth while that means something, and in words once the
      // picture is as old as a slow pace lets it get.
      state = `${age < 60 ? `${age.toFixed(1)} s` : span(age)} old`;
      if (control.interval > MIN_INTERVAL) extras.push(`every ${duration.words(control.interval)}`);
    }
    if (more > 0 && !stale) {
      const drawn = [...entities.values()].filter((held) => held.category !== 'players').length;
      extras.push(`showing ${fmt(drawn)} of ${fmt(drawn + more)} mobs`);
    }
    say(el.age, state);
    say(el.more, extras.join(' · '));

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
    if (settings) {
      settings.set('live', control);
      return;
    }
    try { localStorage.setItem(CONTROL_KEY, JSON.stringify(control)); } catch { /* not kept, still applied */ }
  }

  function paintControl() {
    el.pause.textContent = control.paused ? 'Resume live' : 'Pause live';
    el.pause.classList.toggle('paused', control.paused);
  }

  // Puts on the map exactly the markers that should be there now, after a
  // row or a filter has changed. A frame never needs this: each entity is
  // placed once, when it first appears.
  function refilter() {
    let added = false;
    for (const held of entities.values()) {
      const layer = layerOf(held.category);
      const want = visible(held) && held.grouped !== true;
      if (want === layer.hasLayer(held.marker)) continue;
      if (want) layer.addLayer(held.marker); else layer.removeLayer(held.marker);
      added = added || want;
    }
    if (added) playerLayer.eachLayer((marker) => marker.bringToFront());
    count();
    regroup();
    ring();
    paintCard();
    document.dispatchEvent(new CustomEvent('mcmap:live'));
  }

  // A saved view has changed what is kept: the pace is read again. Which
  // rows and which of their items are shown is the panel's to read, and it
  // tells this layer once when any of that has changed.
  function adopt() {
    if (!settings) return;
    const kept = settings.get('live');
    if (paceOf(kept.interval) !== control.interval) {
      control.interval = paceOf(kept.interval);
      pace.set(control.interval);
      // A frame held for the old pace may be due at the new one.
      present();
      readout();
    }
    if (kept.paused !== control.paused) {
      control.paused = kept.paused;
      paintControl();
      sync();
    }
  }

  // Whether a type of mob is one this page has any reason to know: the
  // game's own, one the server has a picture for, or one on the map now.
  // A view saved long ago may name one that is none of these.
  const knows = (type) => categoryOf.has(type) || icons.listing().mobs.types.has(type) || sortsHere('mobs').has(type);

  // Every sort of a domain that is in the picture now.
  function sortsHere(domain) {
    const out = new Set();
    for (const held of entities.values()) if (domainOf(held.category) === domain) out.add(held.sort);
    return out;
  }

  // Puts the layer's rows in the panel while the service has a live layer
  // and takes them out while it does not: the players as a section of
  // their own, each of them an item of it, and the mobs as a section of
  // four rows with each type an item of its row.
  function panel(available) {
    if (available === (rows !== null)) return;
    if (!available) {
      for (const row of Object.values(rows)) row.remove();
      rows = null;
      return;
    }
    rows = {};
    LAYERS.forEach(([id, label], at) => {
      const players = id === 'players';
      rows[id] = app.layers.register({ group: 'live', id, label, order: (at + 1) * 10, swatch: `dot ${id}`,
        section: players ? 'players' : 'mobs',
        whole: players,
        facet: domainOf(id),
        actions: { zoom: (sort) => zoomTo(id, sort), ...(players ? { go: goToPlayer } : {}) },
      });
      rows[id].onToggle(refilter);
    });
  }

  // --- inspecting and following ------------------------------------------
  //
  // A click on a marker opens a card about that one entity. It is found
  // again in each frame by the id the game gives it, which a player and a
  // mob alike keep for as long as they exist, and never by where it is: an
  // id missing from a whole frame is an entity no longer tracked, and the
  // card says so rather than settle on whatever is nearest.

  const INSPECTED = { color: CATEGORIES.players, weight: 2, dashArray: '4 4' };
  const FOLLOWED = { color: ME, weight: 3, dashArray: null };
  const Ring = L.CircleMarker.extend({
    _updatePath() {
      steady(this, () => L.CircleMarker.prototype._updatePath.call(this));
    },
  });
  const halo = new Ring([0, 0], { renderer, interactive: false, fill: false, opacity: 1, ...INSPECTED });

  const say = (node, s) => {
    if (node.textContent !== s) node.textContent = s;
  };
  const labelOf = (dimension) => (app.label ? app.label(dimension) : dimension);
  // A record that came without a coordinate still has a card to fill.
  const tenths = (n) => (Number.isFinite(n) ? n.toFixed(1) : '—');

  // The marker as the map draws it, so that the card and the map plainly
  // show the same thing.
  function portrait(held) {
    const ctx = card.picture.getContext('2d');
    const DENSITY = density();
    if (card.picture.width !== PORTRAIT * DENSITY) card.picture.width = card.picture.height = PORTRAIT * DENSITY;
    ctx.setTransform(DENSITY, 0, 0, DENSITY, 0, 0);
    ctx.clearRect(0, 0, PORTRAIT, PORTRAIT);
    // With no marker in the picture, what the marker would be.
    const worn = held.marker ? held.marker.options.sprite : (held.category === 'players' ? null : icons.mob(held.type, CATEGORIES[held.category], held.baby === true));
    if (worn) {
      // At the largest size a marker is wider than the card's picture.
      const side = Math.min(PORTRAIT, worn.width / DENSITY);
      ctx.imageSmoothingEnabled = side < worn.width / DENSITY;
      ctx.drawImage(worn, (PORTRAIT - side) / 2, (PORTRAIT - side) / 2, side, side);
      return;
    }
    ctx.beginPath();
    ctx.arc(PORTRAIT / 2, PORTRAIT / 2, held.category === 'players' ? 8 : DOT_RADIUS * 2, 0, Math.PI * 2);
    ctx.fillStyle = held.marker ? held.marker.options.fillColor : (held.category === 'players' ? playerColour(held.name) : CATEGORIES[held.category]);
    ctx.fill();
    ctx.lineWidth = 1.5;
    ctx.strokeStyle = INK;
    ctx.stroke();
  }

  // A named mob the card knows only from the snapshot, which is on the map
  // as the mark the snapshot left.
  const savedOnly = () => picked !== null && picked.saved === true && !picked.wasLive && picked.state !== 'live';

  // One a search found live that no frame drawn since the search has yet
  // had the chance to show.
  const soughtOnly = () => picked !== null && Number.isFinite(picked.sought) && !picked.wasLive && drawnAt <= picked.sought;

  function ring() {
    const found = picked && picked.state === 'live' ? entities.get(picked.key) : null;
    // A layer switched off takes its markers off the map, and a ring left
    // behind would circle nothing. The card goes on reporting the entity.
    const held = found && visible(found) ? found : null;
    if (!held) {
      if (savedOnly() && picked.dimension === app.dimension() && Number.isFinite(picked.x) && Number.isFinite(picked.z)) {
        halo.setLatLng([picked.z, picked.x]);
        halo.setRadius(sizes().mob + SELECTED_GAP + 1);
        halo.setStyle(INSPECTED);
        if (!map.hasLayer(halo)) halo.addTo(map);
        halo.bringToFront();
        return;
      }
      halo.remove();
      return;
    }
    const r = held.marker.getRadius();
    // A head is square, and its corners reach past its radius.
    const reach = held.category === 'players' && held.marker.options.sprite ? r * Math.SQRT2 : r;
    halo.setLatLng([held.z, held.x]);
    halo.setRadius(Math.ceil(reach) + SELECTED_GAP);
    halo.setStyle(picked.follow ? FOLLOWED : INSPECTED);
    if (!map.hasLayer(halo)) halo.addTo(map);
    halo.bringToFront();
  }

  // Whoever is followed while they are in the picture, as the place the
  // map is kept on, or null.
  const followed = () => (picked && picked.follow && picked.state === 'live' ? [picked.z, picked.x] : null);
  // The centre that puts a place in the middle of the map that can be
  // seen, which is not the middle of its box while the panel, the sheet
  // or the card lies over a part of it.
  const aim = (place, zoom) => (app.room ? app.room.aim(place, zoom) : place);

  function centre() {
    const place = followed();
    // Moving the view in the middle of a zoom cuts the zoom short. Its end
    // comes back here. _animatingZoom is a Leaflet internal, like those
    // above.
    if (!place || map._animatingZoom) return;
    map.panTo(aim(place), { animate: false });
  }

  // A zoom is about the pointer, or about the middle of the map's box,
  // and either takes whoever is followed away from where they are kept
  // until the zoom is over and the map is brought back. While someone is
  // followed every zoom is about them instead, and they stay put. Leaflet
  // has no option for it, so these are its own two ways in, replaced on
  // this map alone; _limitZoom is an internal, like those above.
  const zoomAround = map.setZoomAround;
  const setZoom = map.setZoom;
  const about = (zoom, options) => {
    const place = followed();
    if (!place) return false;
    const to = map._limitZoom(zoom);
    map.setView(aim(place, to), to, { zoom: options });
    return true;
  };
  map.setZoomAround = (at, zoom, options) => (about(zoom, options) ? map : zoomAround.call(map, at, zoom, options));
  map.setZoom = (zoom, options) => (about(zoom, options) ? map : setZoom.call(map, zoom, options));
  // A pinch has no such way in: it moves the map itself with every step
  // of the fingers, about the point between them, and comes to rest on
  // the centre it last worked out. Each step is put about whoever is
  // followed, and the centre it will rest on with it. _move, touchZoom
  // and its _center are internals, like those above.
  const move = map._move;
  map._move = (centre, zoom, data, quiet) => {
    const place = data && data.pinch ? followed() : null;
    if (place && map.touchZoom) map.touchZoom._center = aim(place, map._limitZoom(zoom));
    return move.call(map, place ? aim(place, zoom) : centre, zoom, data, quiet);
  };

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
        state: 'live', went: null, dimension: pictured, seenAt: Date.now(), wasLive: true,
        category: held.category, name: held.name, type: held.type, x: held.x, y: held.y, z: held.z,
      });
      portrait(held);
      // The ring first: moving the map paints the canvas at once, and the
      // ring would be painted where the entity was.
      ring();
      centre();
    } else if (app.dimension() !== picked.dimension) {
      picked.state = 'away';
      picked.follow = false;
    } else if (!whole) {
      if (picked.state !== 'lost' && picked.state !== 'saved') picked.state = 'waiting';
    } else if (savedOnly()) {
      // Not in a whole picture of its dimension: its chunk is not loaded,
      // and where the snapshot left it is all that is known.
      picked.state = 'saved';
      picked.follow = false;
    } else if (soughtOnly()) {
      // The picture on the page is older than the search that found it,
      // and says nothing of whether it is still there. Only a frame drawn
      // since can say it has gone.
      picked.state = 'waiting';
    } else if (picked.state !== 'lost') {
      picked.state = 'lost';
      picked.follow = false;
      if (picked.category === 'players') seek(picked, SEEK_TRIES);
    }
    ring();
    paintCard();
  }

  // Whom the card last said it was following, or null: Follow is turned
  // off by a drag, by the entity going, and by a change of dimension as
  // well as by its button, and each is a change someone not looking at the
  // button has to be told of.
  let following = null;

  function announce() {
    const now = picked && picked.follow ? (typeof picked.name === 'string' && picked.name) || names.entity(picked.type) : null;
    if (now === following) return;
    if (card.said) card.said.textContent = now !== null ? `Following ${now}.` : `No longer following ${following}.`;
    following = now;
  }

  function paintCard() {
    if (!card.root) return;
    announce();
    card.root.hidden = picked === null;
    document.body.classList.toggle('inspecting', picked !== null);
    if (picked === null) return;
    const player = picked.category === 'players';
    const kind = player ? (isMe(picked.name) ? 'Player (you)' : 'Player') : names.kindOf(picked.type, picked.baby);
    // Always as text: a gamertag and a name tag are both a player's choice.
    const title = (typeof picked.name === 'string' && picked.name) || kind;
    say(card.name, title);
    // With no name of its own, what it is called is what it is, and that
    // is said once.
    say(card.kind, title === kind ? labelOf(picked.dimension) : `${kind} · ${labelOf(picked.dimension)}`);
    say(card.x, tenths(picked.x));
    say(card.y, tenths(picked.y));
    say(card.z, tenths(picked.z));

    const ago = span((Date.now() - picked.seenAt) / 1000);
    let what = '';
    let when = `Last seen ${ago} ago.`;
    if (savedOnly()) {
      what = picked.state === 'saved' ? 'Not loaded right now.'
        : picked.state === 'away' ? `Not tracked: the map is on another dimension, ${labelOf(app.dimension())}.` : 'Waiting for live positions.';
      // The snapshot's clock is the server's, which the page's own may
      // not agree with to the minute.
      when = Number.isFinite(picked.savedAt) ? `This is its last saved position, from ${age(picked.savedAt)}.` : 'This is its last saved position.';
    } else if (soughtOnly() && picked.state === 'waiting') {
      what = control.paused ? 'Live updates are paused, so this page has no picture of it yet.' : 'Waiting for the next live picture.';
      when = `This position is as of the search, ${ago} ago.`;
    } else if (picked.state === 'lost') {
      what = picked.went ? `Left for another dimension: ${labelOf(picked.went)}.` : 'No longer tracked.';
    } else if (picked.state === 'away') {
      what = `Not tracked: the map is on another dimension, ${labelOf(app.dimension())}.`;
    } else if (picked.state === 'waiting') {
      what = 'Waiting for live positions.';
    } else if (control.paused) {
      what = 'Paused.';
      when = `Position is from ${ago} ago.`;
    } else if (picked.state === 'live' && entities.has(picked.key) && !visible(entities.get(picked.key))) {
      // Still tracked, and still followed if it was: the card says why
      // there is nothing on the map where it points.
      what = shown(picked.category) ? `Hidden on the map by your ${picked.category === 'players' ? 'player' : 'type'} filter.` : 'Hidden on the map: its layer is switched off.';
      when = picked.follow ? 'Still followed.' : 'Still tracked.';
    }
    card.root.classList.toggle('adrift', what !== '');
    card.note.hidden = what === '';
    say(card.what, what);
    say(card.when, what === '' ? '' : when);
    card.follow.disabled = picked.state !== 'live' && picked.state !== 'waiting';
    if (card.go) card.go.disabled = !Number.isFinite(picked.x) || !Number.isFinite(picked.z);
    card.follow.setAttribute('aria-pressed', String(picked.follow));
    say(card.follow, picked.follow ? 'Following' : 'Follow');
    // The layer panel stops short of the card on a narrow screen, and the
    // card is as tall as what it has to say.
    if (`${what}|${when.length}` !== cardShape) {
      cardShape = `${what}|${when.length}`;
      document.body.style.setProperty('--inspect-height', `${card.root.offsetHeight}px`);
    }
  }

  // How long ago a snapshot was, to the minute, which is all a snapshot
  // taken every few minutes is good to.
  function age(at) {
    const minutes = Math.max(0, Math.round((Date.now() + clockOffset - at) / 60_000));
    if (minutes < 1) return 'under a minute ago';
    if (minutes < 120) return `${minutes} min ago`;
    return `${Math.round(minutes / 60)} h ago`;
  }

  // Whoever the card is about is drawn by themselves, and whoever it has
  // let go of may be one of several again.
  const told = () => {
    regroup();
    document.dispatchEvent(new CustomEvent('mcmap:inspect', { detail: { key: picked ? picked.key : null } }));
  };

  // Opens the card on one entity. known is what is said of it until the
  // picture has it, for one chosen from a list or by the mark the snapshot
  // left, and is not needed for one chosen in the picture.
  function pick(key, known) {
    if (!card.root || (!entities.has(key) && !known)) return false;
    stopSeeking();
    picked = { key, follow: false, state: 'waiting', went: null, dimension: pictured, seenAt: Date.now(), wasLive: false, ...known };
    cardShape = null;
    if (!entities.has(key)) portrait(picked);
    // A picture of this dimension that is on the map is all there is: an
    // entity not in it is not loaded.
    track(!stale && frameAt !== null && pictured === app.dimension());
    told();
    return true;
  }

  function shut() {
    if (!picked) return;
    const within = card.root.contains(document.activeElement);
    picked = null;
    stopSeeking();
    ring();
    paintCard();
    told();
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
    // The click was the marker's, and is not also one on the map under it.
    L.DomEvent.stopPropagation(e);
    for (const [key, held] of entities) {
      if (held.marker !== e.layer) continue;
      pick(key);
      return;
    }
  }

  // What another script may know of an entity before the picture has it.
  // Every field is checked: a hit from the search and a mark from the
  // snapshot are both answers from the server.
  function inspect(what) {
    if (!what || typeof what !== 'object') return false;
    const player = what.kind === 'player';
    const id = typeof what.id === 'string' && what.id !== '' && what.id.length <= 64 ? what.id : null;
    const num = (v) => (Number.isFinite(v) ? v : NaN);
    const dimension = typeof what.dimension === 'string' ? what.dimension : app.dimension();
    const type = typeof what.type === 'string' ? what.type : '';
    const known = {
      category: player ? 'players' : categoryOf.get(type) || 'other',
      name: typeof what.name === 'string' ? what.name : '',
      type,
      baby: what.baby === true,
      x: num(what.x), y: num(what.y), z: num(what.z),
      dimension,
    };
    // Found live by a search just now, which knows more than the picture
    // on this page may.
    if (what.live === true) known.sought = Date.now();
    // A mob placed by the snapshot, which may not be where it is now.
    if (!player && what.saved === true) {
      known.saved = true;
      if (Number.isFinite(what.savedAt)) known.savedAt = what.savedAt;
    }
    // With no id there is nothing to find it by in the picture, and the
    // key is one no entity has.
    const key = id ? `${player ? 'p' : 'm'}:${id}` : `s:${dimension}:${known.x}:${known.y}:${known.z}`;
    return pick(key, known);
  }

  function goTo() {
    if (!picked || !Number.isFinite(picked.x) || !Number.isFinite(picked.z)) return;
    if (app.go) app.go(picked.dimension, picked.x, picked.z);
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
    card.picture.width = card.picture.height = PORTRAIT * density();
    mobLayer.on('click', onPick);
    playerLayer.on('click', onPick);
    card.close.addEventListener('click', shut);
    card.copy.addEventListener('click', copyPosition);
    if (card.go) card.go.addEventListener('click', goTo);
    card.follow.addEventListener('click', () => {
      if (!picked) return;
      picked.follow = !picked.follow;
      centre();
      ring();
      paintCard();
    });
    document.addEventListener('keydown', (e) => {
      // An Escape pressed in a dialog is the dialog's: it shuts that, and
      // the card under it, and whatever it is following, stay.
      if (e.key !== 'Escape' || (e.target instanceof Element && e.target.closest('dialog[open]') !== null)) return;
      shut();
    });
    // The viewer taking the map somewhere else is the viewer no longer
    // following. A zoom keeps the entity in the middle and is not that.
    map.on('dragstart', unfollow);
    map.on('keydown', (e) => {
      if (e.originalEvent.key.startsWith('Arrow')) unfollow();
    });
    map.on('zoomend', centre);
    // The room to see the map in has changed, and its middle with it.
    // Leaflet is told of a change to the map's box in the frame after it
    // happens, and for that frame the middle it knows is not the middle
    // there is; while someone is kept there it is told at once.
    if (app.room) {
      app.room.watch(() => {
        if (!followed()) return;
        map.invalidateSize();
        centre();
      });
    }
  }

  el.pause.addEventListener('click', () => {
    control.paused = !control.paused;
    saveControl();
    paintControl();
    sync();
  });

  const pace = duration.picker({
    name: 'Redraw live positions every',
    presets: INTERVALS,
    unit: 's',
    min: MIN_INTERVAL,
    max: MAX_INTERVAL,
    minWhy: ', the server\'s own pace',
    say: (length) => `every ${length}`,
    value: control.interval,
    onChange(seconds) {
      control.interval = seconds;
      saveControl();
      // A frame held for the old pace may be due at the new one.
      present();
      readout();
    },
  });
  el.interval.replaceChildren(pace.node);

  app.playerColour = playerColour;
  app.isMe = isMe;
  // The session's own gamertag, in lower case, or '' while it is not known.
  app.myName = () => listedAs || me || '';
  app.inspect = {
    open: inspect,
    shut,
    // The key of the entity the card is about, or null.
    key: () => (picked ? picked.key : null),
    // Whether the mob with this id is on the map as a live marker now, so
    // that the mark the snapshot left of it can step aside.
    drawn(id) {
      const held = entities.get(`m:${id}`);
      return Boolean(held) && !stale && mobLayer.hasLayer(held.marker);
    },
    // The mobs in the picture that have a name, as { id, name, type }, for
    // the layer that has to find the snapshot's among them.
    named() {
      const out = [];
      if (stale) return out;
      for (const [key, held] of entities) {
        if (held.category !== 'players' && held.name) out.push({ id: key.slice(2), name: held.name, type: held.type });
      }
      return out;
    },
    // How long ago a snapshot was, by the server's clock.
    age,
    // Where the mob with this id is, while it is in the picture.
    where(id) {
      const held = entities.get(`m:${id}`);
      return held && !stale ? { x: held.x, y: held.y, z: held.z } : null;
    },
    // Who and what the live picture has inside a box of blocks now: each
    // player with a way to open their card, and the mobs counted by type.
    // Null while there is no picture to count from.
    within(box) {
      if (stale || frameAt === null) return null;
      const players = [];
      const mobs = new Map();
      for (const [key, held] of entities) {
        const x = Math.floor(held.x);
        const z = Math.floor(held.z);
        if (x < box.minX || x > box.maxX || z < box.minZ || z > box.maxZ) continue;
        if (held.category === 'players') {
          players.push({ name: held.name || 'Player', open: () => pick(key) });
        } else {
          const title = names.entity(held.type);
          mobs.set(title, (mobs.get(title) || 0) + 1);
        }
      }
      return { players, mobs };
    },
    // Where the session's own player is, while they are in the picture.
    me() {
      if (stale) return null;
      for (const held of entities.values()) if (held.category === 'players' && isMe(held.name)) return { x: held.x, z: held.z };
      return null;
    },
    // Whether named mobs wear their names.
    labels(on) {
      if (tagging === Boolean(on)) return;
      tagging = Boolean(on);
      for (const held of entities.values()) {
        if (held.category === 'players' || !held.name) continue;
        held.marker.options.tag = tagOf(held.name);
        held.marker.redraw();
      }
    },
    // The key of the entity being followed, or null.
    following: () => (picked && picked.follow ? picked.key : null),
    // Turns Follow on or off as its button would, for something that is
    // about to take the map elsewhere or has put it back, and gives back
    // the name of whoever was being followed until then, or ''.
    follow(on) {
      if (!picked) return '';
      const was = picked.follow ? (typeof picked.name === 'string' && picked.name) || names.entity(picked.type) : '';
      const can = picked.state === 'live' || picked.state === 'waiting';
      if (picked.follow !== Boolean(on) && (!on || can)) {
        picked.follow = Boolean(on);
        centre();
        ring();
        paintCard();
      }
      return was;
    },
    // For a saved view: what is kept is read again, and the map brought
    // in line with it once.
    adopt,
    knows,
  };
  // For a layer that has to sit under these markers and still be hovered:
  // only what is on the same canvas can be both.
  app.liveRenderer = renderer;
  // The canvas paints in the order markers were added, and the topmost is
  // the one a click lands on. A layer that adds its own calls this after,
  // so that what moves stays over what does not.
  app.liveToFront = () => {
    mobLayer.eachLayer((marker) => marker.bringToFront());
    playerLayer.eachLayer((marker) => marker.bringToFront());
    if (map.hasLayer(halo)) halo.bringToFront();
  };

  heapLayer.addTo(map);
  mobLayer.addTo(map);
  playerLayer.addTo(map);
  // How far out the map is decides whether anything is grouped, and how.
  map.on('zoomend', regroup);
  if (app.groups && app.groups.drawnBy) {
    app.groups.drawnBy(regroup);
    app.groups.next = nextGroup;
    regroup();
  }
  paintControl();
  document.addEventListener('mcmap:icons', relist);
  document.addEventListener('mcmap:pictures', () => {
    dress();
    regroup();
    // A head that has arrived is drawn on its player's line.
    count();
  });
  for (const choice of Object.values(choices)) choice.onChange(refilter);
  // A tooltip already open says the old name until it is told.
  document.addEventListener('mcmap:names', () => {
    if (mobLayer.isTooltipOpen()) mobLayer.getTooltip().update();
    paintCard();
    count();
  });
  const styledAs = () => {
    const { theme, size, text, labelMobs, labelPlayers, picturesLive, group } = look();
    return [theme, size, text, labelMobs, labelPlayers, picturesLive, group, icons.composedAs ? icons.composedAs() : ''].join('|');
  };
  let styled = styledAs();
  document.addEventListener('mcmap:settings', (e) => {
    if (!e.detail || !e.detail.sections.includes('look') || styled === styledAs()) return;
    styled = styledAs();
    restyle();
  });
  document.addEventListener('mcmap:view', sync);
  document.addEventListener('visibilitychange', sync);
  // After the canvas has been drawn for the zoom the map came to rest at.
  map.on('zoomend', () => setTimeout(present, 0));
  setInterval(readout, READOUT_MS);
  sync();
})();
