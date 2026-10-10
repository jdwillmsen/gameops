'use strict';

// The pictures the map is drawn with: which ones the server has, and each
// of them fetched and decoded once for every layer that draws it. A mob's
// icon, a player's head, a bed, a chest and a structure's mark all come
// through here, so there is one list asked for, one copy of each picture,
// and one way to fall back: whoever asks is told there is no picture yet
// and draws what it drew before there were any, until it is told one has
// arrived.
(() => {
  const app = window.mcmap;
  if (!app || !app.map) return;

  // How often to ask which pictures there are.
  const ICONS_MS = 30_000;
  // A picture that could not be fetched is asked for again this long
  // after, so one lost to a dropped connection is not lost for the visit.
  const RETRY_FAILED_MS = 5 * 60_000;
  // Leaflet's canvas draws at twice the size on a dense screen, so a
  // picture prepared for it is prepared at that size too.
  // It is not fixed for the visit: a window dragged to another screen, or
  // a page zoomed, changes it, and everything prepared is prepared again.
  let DENSITY = L.Browser.retina ? 2 : 1;

  // Sizes in CSS pixels. A marker's picture is drawn into a box, and how
  // large the box is is the viewer's choice of size and nothing else; what
  // is drawn round it is their choice of style. A picture is only ever
  // enlarged by a whole number of screen pixels to a texture pixel, so a
  // face 8 pixels a side fills a 16 pixel box and one 6 a side stands 12
  // in it: pixel art is never drawn at a pixel and a half.
  const ICON = 16;
  const MOB_RADIUS = 12;
  const BABY_RADIUS = 10;
  const PLATE_RADIUS = 11;
  // Larger is the next whole number of screen pixels to a 16 pixel
  // texture, which on a dense screen is one and a half times the size and
  // on any other twice. Smaller is 12, which blends an egg, since a
  // picture cannot be made smaller than it was drawn without losing
  // pixels, and draws a face at one screen pixel to each of its own.
  const big = () => (DENSITY === 2 ? 24 : 32);
  const huge = () => (DENSITY === 2 ? 32 : 48);
  // The box by size: a grown mob's or a marker's, a baby's, a player's
  // head, and how far a plain dot or ring is scaled.
  const SIZES = ['small', 'normal', 'large', 'xlarge'];
  const boxes = () => ({
    small: { icon: 12, babyIcon: 8, head: 16, scale: 0.8 },
    normal: { icon: ICON, babyIcon: 12, head: 24, scale: 1 },
    large: { icon: big(), babyIcon: ICON, head: 32, scale: 1.4 },
    xlarge: { icon: huge(), babyIcon: big(), head: 48, scale: 1.8 },
  });
  // With no plate the picture is the whole marker, so its box is larger:
  // a 32 pixel block one to one at the usual size, and a face at 4.
  const BARE = { small: 24, normal: 32, large: 48, xlarge: 64 };
  const BARE_BABY = { small: 16, normal: 24, large: 32, xlarge: 48 };
  // What is drawn round a picture on a plate: the ring in the layer's
  // colour, which is what the filters are read by, inside a dark casing
  // that sets the marker off from any terrain, light or dark.
  const RING = 2;
  const CASING = 1;
  const PLATE_PAD = RING + CASING + 1;
  const STYLES = ['dots', 'plates', 'large'];
  // How far out the map may be and still have pictures drawn on it, and
  // names over them. Zoom 0 is a block to the pixel, and each step out
  // halves it. A mob is where a player is, a few hundred to a hundred
  // blocks square, so from two steps out their plates are a heap; a bed
  // or a chest is spread over the world, and keeps its picture a step
  // further. A name is three or four plates wide, and is drawn only
  // where a plate has that much room: a block to the pixel or closer.
  const PICTURES_FROM = { live: -1, markers: -2 };
  const LABELS_FROM = 0;
  // How a marker is drawn when it is not simply there: the one the
  // pointer is on, the one whose card is open, and one that is only a
  // record of where something was. Every layer takes these from here.
  const STATES = {
    hovered: { gap: 2, weight: 1.5 },
    selected: { gap: 4, weight: 2 },
    dimmed: { alpha: 0.55, dash: [3, 3] },
  };
  // A name tag's letters and the plate behind them, by the label size
  // chosen.
  const TAGS = {
    small: { font: 11, height: 16 },
    normal: { font: 12, height: 18 },
    large: { font: 15, height: 23 },
  };
  // What the viewer has chosen for how the map looks, where the page keeps
  // such a thing; a page from before it did draws everything as it was.
  const settings = window.mcmapSettings || null;
  const look = () => (settings ? settings.look() : {});
  const colour = (name, fallback) => (settings && settings.colour(name)) || fallback;
  // The style chosen. A page whose settings are from before there was a
  // choice of three says only pictures or plain, for mobs and for
  // markers apart, and is drawn as it says.
  function styleOf(domain) {
    const chosen = look().style;
    if (STYLES.includes(chosen)) return chosen;
    return look()[domain === 'live' ? 'picturesLive' : 'picturesMarkers'] === false ? 'dots' : 'plates';
  }
  const sizeOf = () => (SIZES.includes(look().size) ? look().size : 'normal');
  // Whether a mob is drawn as its face where it has one, or as its egg.
  const faces = () => look().mobPicture !== 'eggs';
  // How far out the map is: pictures and names, pictures, or neither.
  function tier(domain = 'live') {
    const zoom = app.map.getZoom();
    if (zoom < PICTURES_FROM[domain === 'live' ? 'live' : 'markers']) return 'dot';
    return zoom >= LABELS_FROM ? 'label' : 'picture';
  }
  // The sizes a layer needs to know a marker's reach by, at the size and
  // in the style chosen: asked for when used, since either may change.
  function sizes() {
    const box = boxes()[sizeOf()];
    const bare = styleOf('live') === 'large';
    const size = sizeOf();
    return {
      ...box,
      mob: bare ? BARE[size] / 2 + RING + CASING : box.icon / 2 + PLATE_PAD,
      baby: bare ? BARE_BABY[size] / 2 + RING + CASING : box.babyIcon / 2 + PLATE_PAD,
      plate: styleOf('markers') === 'large' ? BARE[size] / 2 + RING + CASING : box.icon / 2 + PLATE_PAD - 1,
      headRadius: box.head / 2 + 3,
    };
  }
  // A theme made for contrast draws every outline heavier.
  const heavy = () => (look().theme === 'contrast' ? 1 : 0);
  const KEY = /^[a-z0-9_]+\/[a-z0-9_]+$/;

  const decodes = typeof createImageBitmap === 'function';
  const nothing = () => ({ mobs: { version: '', types: new Set() }, pictures: { version: '', keys: new Set() }, names: '', heads: {}, me: '' });

  // What the server last said there is. Empty is the state before the
  // answer, the state when the server has none to offer, and the state
  // when it could not fetch any; in all three everything is drawn as it
  // was before there were pictures.
  let listing = nothing();
  // The last answer as it was sent, to tell an unchanged one cheaply.
  let answer = '';
  let askedAt = 0;
  // A server with no pictures at all answers 404 until it restarts.
  let gone = false;
  let timer = null;

  // address -> ImageBitmap once decoded, null while loading or if it
  // could not be. An address carries its picture's version, so a changed
  // picture is a new entry and is fetched once.
  const bitmaps = new Map();
  // address -> when it failed.
  const failed = new Map();
  // address + how it is drawn -> the composed sprite.
  const sprites = new Map();
  let arriving = false;

  const str = (v) => (typeof v === 'string' ? v : '');
  const tell = (name, detail) => document.dispatchEvent(new CustomEvent(name, { detail }));

  // Tells every layer that a picture it was waiting for can be drawn.
  // Pictures arrive in a burst; one pass dresses all that have.
  function arrived() {
    if (arriving) return;
    arriving = true;
    requestAnimationFrame(() => {
      arriving = false;
      paint(document);
      tell('mcmap:pictures');
    });
  }

  // Fetches and decodes a picture once. Whatever wanted it is drawn plain
  // meanwhile and dressed when it arrives.
  function load(address) {
    bitmaps.set(address, null);
    fetch(address)
      .then((res) => (res.ok ? res.blob() : Promise.reject(new Error(String(res.status)))))
      .then((blob) => createImageBitmap(blob))
      .then((bitmap) => {
        bitmaps.set(address, bitmap);
        arrived();
      })
      .catch(() => {
        // Stays plain; a new version is a new address.
        failed.set(address, Date.now());
      });
  }

  // The decoded picture at an address, or null if it is not there yet, in
  // which case it is on its way.
  function bitmap(address) {
    if (!address || !decodes) return null;
    if (!bitmaps.has(address)) load(address);
    return bitmaps.get(address);
  }

  function retry() {
    let any = false;
    for (const [address, when] of failed) {
      if (Date.now() - when < RETRY_FAILED_MS) continue;
      failed.delete(address);
      bitmaps.delete(address);
      any = true;
    }
    // Nothing is waiting on a picture that failed, so everything that
    // might want one is asked to look again.
    if (any) arrived();
  }

  // A picture ready to be stamped onto a canvas: a head on its backing,
  // composed once by whoever asks. Drawing a marker is then one
  // drawImage, whatever is in the picture. Null while the picture is not
  // decoded.
  function sprite(address, colour, radius, painter, shape = '') {
    const drawn = bitmap(address);
    if (!drawn) return null;
    const key = `${address}|${colour}|${shape}`;
    let made = sprites.get(key);
    if (!made) {
      made = document.createElement('canvas');
      made.width = made.height = Math.round(radius * 2 * DENSITY);
      const ctx = made.getContext('2d');
      ctx.scale(DENSITY, DENSITY);
      // Pixel art scaled by a whole number stays as its author drew it.
      ctx.imageSmoothingEnabled = false;
      painter(colour)(ctx, drawn);
      sprites.set(key, made);
    }
    return made;
  }

  // The backing is dark in every theme: the pictures were drawn to be
  // seen on the game's own dark slots.
  const backing = () => colour('marker-backing', 'rgba(11, 12, 14, 0.8)');
  const casing = () => colour('marker-ink', '#0b0c0e');

  // How large a picture is drawn in a box, in screen pixels along its
  // longer side: the most whole screen pixels to each of its own that
  // fit, or, for one larger than its box, the box, blended, since a
  // picture cannot be made smaller without losing pixels. A block is
  // already drawn on the slant and has no grid to keep, so where a whole
  // number would leave it well short of its box it is stretched to it,
  // unblended.
  function fit(native, box, slanted = false) {
    const target = box * DENSITY;
    const whole = Math.floor(target / native);
    if (whole >= 1 && (!slanted || whole * native >= 0.75 * target)) return { side: whole * native, smooth: false };
    return { side: target, smooth: target < native };
  }

  // Draws a picture in the middle of a square of screen pixels, on whole
  // pixels, at the size fit gives it.
  function centred(ctx, drawn, side, box, slanted) {
    const longer = Math.max(drawn.width, drawn.height);
    const { side: across, smooth } = fit(longer, box, slanted);
    const w = Math.max(1, Math.round((drawn.width * across) / longer));
    const h = Math.max(1, Math.round((drawn.height * across) / longer));
    ctx.imageSmoothingEnabled = smooth;
    ctx.drawImage(drawn, Math.round((side - w) / 2), Math.round((side - h) / 2), w, h);
  }

  // A picture on a plate: round for what moves and square for what stays
  // put. From the outside in, the dark casing, the ring in the layer's
  // colour, the dark backing, and the picture kept inside the ring, so
  // that nothing in it can cover the colour the filters are read by.
  function plated(drawn, ring, box, round) {
    const made = document.createElement('canvas');
    const side = (box + 2 * PLATE_PAD - (round ? 0 : 2)) * DENSITY;
    made.width = made.height = side;
    const ctx = made.getContext('2d');
    const shape = (inset) => {
      const by = inset * DENSITY;
      ctx.beginPath();
      if (round) ctx.arc(side / 2, side / 2, side / 2 - by, 0, Math.PI * 2);
      else if (ctx.roundRect) ctx.roundRect(by, by, side - 2 * by, side - 2 * by, Math.max(1, 3 - inset) * DENSITY);
      else ctx.rect(by, by, side - 2 * by, side - 2 * by);
    };
    const band = RING + heavy();
    shape(0);
    ctx.fillStyle = casing();
    ctx.fill();
    shape(CASING);
    ctx.fillStyle = ring;
    ctx.fill();
    shape(CASING + band);
    ctx.globalCompositeOperation = 'destination-out';
    ctx.fill();
    ctx.globalCompositeOperation = 'source-over';
    ctx.fillStyle = backing();
    ctx.fill();
    ctx.save();
    ctx.clip();
    centred(ctx, drawn, side, box, false);
    ctx.restore();
    return made;
  }

  // A picture with no plate: the picture itself, larger, cased round its
  // own outline in the layer's colour and then in the dark, so that a
  // chest is a chest's shape and still reads on grass and on snow.
  function bare(drawn, ring, box, slanted) {
    const edge = RING + heavy() + CASING;
    const side = (box + 2 * edge) * DENSITY;
    const picture = document.createElement('canvas');
    picture.width = picture.height = side;
    centred(picture.getContext('2d'), drawn, side, box, slanted);
    // The picture's shape filled with one colour, to be stamped round it.
    const tinted = (fill) => {
      const flat = document.createElement('canvas');
      flat.width = flat.height = side;
      const ctx = flat.getContext('2d');
      ctx.drawImage(picture, 0, 0);
      ctx.globalCompositeOperation = 'source-in';
      ctx.fillStyle = fill;
      ctx.fillRect(0, 0, side, side);
      return flat;
    };
    const made = document.createElement('canvas');
    made.width = made.height = side;
    const ctx = made.getContext('2d');
    const around = (flat, reach) => {
      const steps = Math.max(8, Math.ceil(reach * 6));
      for (let n = 0; n < steps; n += 1) {
        const turn = (n / steps) * Math.PI * 2;
        ctx.drawImage(flat, Math.round(Math.cos(turn) * reach), Math.round(Math.sin(turn) * reach));
      }
    };
    around(tinted(casing()), edge * DENSITY);
    around(tinted(ring), (edge - CASING) * DENSITY);
    ctx.drawImage(picture, 0, 0);
    return made;
  }

  // --- the registry -----------------------------------------------------
  //
  // Everything the map draws a picture for has one key here: mob/<type>,
  // villager/<profession>, bed/<colour>, shulker/<colour>,
  // container/<kind>, structure/<kind>, marker/waypoint, block/<kind>.
  // A key has the renditions the server has listed for it, and which of
  // them is drawn is decided here and nowhere else, so the map, the
  // panel, a card and a search result cannot disagree.

  const address = (key) => (listing.pictures.keys.has(key) && KEY.test(key)
    ? `api/icons/picture/${key}?v=${encodeURIComponent(listing.pictures.version)}` : null);
  const egg = (type) => (listing.mobs.types.has(type)
    ? `api/icons/mob/${encodeURIComponent(type)}?v=${encodeURIComponent(listing.mobs.version)}` : null);
  const WAYPOINT = 'marker/waypoint';
  // The villager whose professions have faces of their own.
  const VILLAGER = 'villager_v2';
  // The block a marker's flat picture is one side of.
  function blockOf(key) {
    const [group, name] = key.split('/');
    if (group === 'container') return `block/${name}`;
    if (group === 'shulker') return `block/shulker_${name}`;
    if (group === 'bed') return `block/bed_${name}`;
    return '';
  }
  const living = (key) => key.startsWith('mob/') || key.startsWith('villager/');

  // Every rendition there is of a key, by what it is: a mob's face and
  // egg, anything else's flat picture and its block. One the server has
  // not listed is absent, and only what it lists is ever asked for.
  function renditions(key) {
    const of = str(key);
    const name = of.slice(of.indexOf('/') + 1);
    if (of.startsWith('mob/')) return { face: address(`face/${name}`), egg: egg(name) };
    if (of.startsWith('villager/')) return { face: address(of) || address(`face/${VILLAGER}`), egg: egg(VILLAGER) };
    return { flat: address(of), block: address(blockOf(of)) };
  }

  // Where the picture a key is drawn with is asked for, in the style
  // chosen, or null for none: plain marks are no picture at all, a mob is
  // its face unless the viewer would rather its egg, and falls back to
  // whichever of the two there is; a block is drawn as one only where
  // there is no plate to fit it on.
  function chosen(key, style = styleOf(living(str(key)) ? 'live' : 'markers')) {
    if (style === 'dots') return null;
    const has = renditions(key);
    if (living(str(key))) return (faces() ? has.face || has.egg : has.egg || has.face) || null;
    return (style === 'large' ? has.block || has.flat : has.flat || has.block) || null;
  }
  // Either sort by one key, as the page's own pictures ask.
  const addressOf = (key) => chosen(key);

  // The key of the picture a thing is drawn with, wherever on the page it
  // is drawn: sort is bed, container, mob, structure or waypoint, and kind
  // a container's kind, a mob's type or a structure's kind. A bed whose
  // colour the world does not say is the red one the game itself falls
  // back to, and a shulker box likewise the undyed one. A villager whose
  // profession is known is drawn in it.
  function keyOf(sort, { kind, colour, trapped, profession } = {}) {
    if (sort === 'waypoint') return WAYPOINT;
    if (sort === 'bed') return `bed/${str(colour) || 'red'}`;
    if (sort === 'mob') return kind === VILLAGER && /^[a-z_]{1,32}$/.test(str(profession)) ? `villager/${profession}` : `mob/${str(kind)}`;
    if (sort === 'structure') return `structure/${str(kind)}`;
    if (sort !== 'container') return '';
    if (kind === 'shulker') return `shulker/${str(colour) || 'undyed'}`;
    return `container/${kind === 'chest' && trapped === true ? 'trapped_chest' : str(kind)}`;
  }

  // The marker a key is drawn as on the map, composed once per picture,
  // colour and shape: one drawing routine for every layer, in whichever
  // style is chosen. Null where there is none to draw, which is when the
  // caller draws its plain mark: with plain marks chosen, from too far
  // out, and while the picture has not arrived.
  function marker(key, ring, { baby = false, always = false } = {}) {
    const alive = living(str(key));
    const domain = alive ? 'live' : 'markers';
    const style = styleOf(domain);
    if (style === 'dots' || (!always && tier(domain) === 'dot')) return null;
    const from = chosen(key, style);
    const drawn = bitmap(from);
    if (!drawn) return null;
    const size = sizeOf();
    const shape = `${style}${alive ? 'r' : 's'}${baby ? 'b' : ''}`;
    const held = `${from}|${ring}|${shape}`;
    let made = sprites.get(held);
    if (!made) {
      made = style === 'large'
        ? bare(drawn, ring, (baby ? BARE_BABY : BARE)[size], from.includes('/block/'))
        : plated(drawn, ring, boxes()[size][baby ? 'babyIcon' : 'icon'], alive);
      sprites.set(held, made);
    }
    return made;
  }

  const mob = (type, ring, baby = false) => marker(`mob/${str(type)}`, ring, { baby });
  // A waypoint is a place put there by name to be found again, and there
  // are few: it keeps its picture from however far out.
  const plate = (of, ring) => marker(of, ring, { always: of === WAYPOINT });

  // Draws a picture into an element of the page, and says whether there
  // was one to draw. Without one the element is hidden, and whatever
  // stands beside it as the fallback shows.
  function fill(canvas) {
    const from = addressOf(canvas.dataset.picture);
    const drawn = bitmap(from);
    canvas.hidden = !drawn;
    // Made for another density, it is made again for this one.
    if (canvas.width !== ICON * DENSITY) {
      canvas.width = canvas.height = ICON * DENSITY;
      delete canvas.dataset.drawn;
    }
    if (!drawn || canvas.dataset.drawn === from) return;
    const ctx = canvas.getContext('2d');
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    centred(ctx, drawn, canvas.width, canvas.width / DENSITY, from.includes('/block/'));
    canvas.dataset.drawn = from;
  }

  // A picture for the page itself, outside the map: a panel row, a search
  // result, a tooltip. It is the one the map draws for the same key, and
  // appears by itself once the server has it.
  function picture(key) {
    const canvas = document.createElement('canvas');
    canvas.className = 'picture';
    canvas.width = canvas.height = ICON * DENSITY;
    canvas.dataset.picture = str(key);
    canvas.setAttribute('aria-hidden', 'true');
    fill(canvas);
    return canvas;
  }

  // Brings every picture under an element in line with what there is now.
  function paint(root) {
    if (!root) return;
    for (const canvas of root.querySelectorAll('canvas[data-picture]')) fill(canvas);
  }

  // Draws a marker's picture where Leaflet would have drawn its circle.
  // _renderer, _ctx, _point and _drawing are Leaflet internals, which is
  // safe only because Leaflet is vendored at a fixed version.
  function stamp(layer, worn, lift = 0) {
    const ctx = layer._renderer._ctx;
    const p = layer._point;
    const w = worn.width / DENSITY;
    const h = worn.height / DENSITY;
    // Leaflet leaves the last shape's opacity set on the context.
    ctx.globalAlpha = 1;
    ctx.drawImage(worn, Math.round(p.x - w / 2), Math.round(p.y - h / 2 - lift), w, h);
  }

  // The ring round a marker in one of its states: light on dark, so it
  // shows on any terrain.
  function halo(ctx, x, y, radius, state) {
    const { gap, weight } = STATES[state];
    ctx.save();
    ctx.globalAlpha = 1;
    ctx.beginPath();
    ctx.arc(x, y, radius + gap, 0, Math.PI * 2);
    ctx.lineWidth = weight + 2;
    ctx.strokeStyle = casing();
    ctx.stroke();
    ctx.lineWidth = weight;
    ctx.strokeStyle = colour('live-players', '#ffffff');
    ctx.stroke();
    ctx.restore();
  }
  const HOVER_REACH = STATES.hovered.gap + STATES.hovered.weight + 2;

  // A marker that is its picture, or, with none to draw, the circle it
  // has always been. Under the pointer it is ringed, picture or circle.
  const Stamped = L.CircleMarker.extend({
    onAdd(map) {
      L.CircleMarker.prototype.onAdd.call(this, map);
      this.on('mouseover', this._point_at, this);
      this.on('mouseout', this._point_off, this);
    },
    onRemove(map) {
      this.off('mouseover', this._point_at, this);
      this.off('mouseout', this._point_off, this);
      this._hovered = false;
      L.CircleMarker.prototype.onRemove.call(this, map);
    },
    _point_at() {
      this._hovered = true;
      this.redraw();
    },
    _point_off() {
      this._hovered = false;
      this.redraw();
    },
    // The canvas only repaints inside the bounds a marker claims, and the
    // ring reaches past the radius.
    _updateBounds() {
      L.CircleMarker.prototype._updateBounds.call(this);
      this._pxBounds.extend(this._point.subtract([this._radius + HOVER_REACH, this._radius + HOVER_REACH]));
      this._pxBounds.extend(this._point.add([this._radius + HOVER_REACH, this._radius + HOVER_REACH]));
    },
    _updatePath() {
      if (!this.options.sprite) L.CircleMarker.prototype._updatePath.call(this);
      else if (this._renderer._drawing && !this._empty()) stamp(this, this.options.sprite);
      if (this._hovered && this._renderer._drawing && !this._empty()) halo(this._renderer._ctx, this._point.x, this._point.y, this._radius, 'hovered');
    },
  });

  // A name tag may be 64 characters; the label on the map shows this many
  // and the tooltip, the list and the card show them all.
  const TAG_LENGTH = 24;
  const TAG_GAP = 3;
  const tagSize = () => TAGS[look().text] || TAGS.normal;
  // name + colour -> the label, drawn once however many frames stamp it.
  const tags = new Map();

  // A mob's name tag, drawn once to be stamped over its marker. Text put
  // on a canvas is drawn and never parsed.
  function tag(name, ink) {
    const letters = [...str(name)];
    if (letters.length === 0) return null;
    const { font, height } = tagSize();
    const face = `600 ${font}px system-ui, -apple-system, "Segoe UI", sans-serif`;
    const key = `${ink}|${str(name)}`;
    if (tags.has(key)) return tags.get(key);
    const said = letters.length > TAG_LENGTH ? `${letters.slice(0, TAG_LENGTH - 1).join('')}…` : letters.join('');
    const canvas = document.createElement('canvas');
    const ctx = canvas.getContext('2d');
    ctx.font = face;
    const width = Math.ceil(ctx.measureText(said).width) + 10;
    canvas.width = width * DENSITY;
    canvas.height = height * DENSITY;
    ctx.scale(DENSITY, DENSITY);
    ctx.beginPath();
    if (ctx.roundRect) ctx.roundRect(0.5, 0.5, width - 1, height - 1, 3);
    else ctx.rect(0.5, 0.5, width - 1, height - 1);
    // The plate is the theme's, as a tooltip's is, and the letters the
    // colour the caller reads on it.
    ctx.fillStyle = colour('label-plate', 'rgba(20, 22, 26, 0.88)');
    ctx.fill();
    ctx.lineWidth = 1 + heavy();
    ctx.strokeStyle = ink;
    ctx.stroke();
    // Sizing the canvas reset the font.
    ctx.font = face;
    ctx.fillStyle = ink;
    ctx.textAlign = 'center';
    ctx.textBaseline = 'middle';
    ctx.fillText(said, width / 2, height / 2 + 0.5);
    // Names are players' to choose, so there is no end of them.
    if (tags.size > 2048) tags.clear();
    tags.set(key, canvas);
    return canvas;
  }

  // A marker with, where it has one, a name over it. The name is part of
  // the marker to the pointer as it is to the eye: a click on the label is
  // a click on what it labels. From further out than names are drawn at,
  // it is held and not drawn, and is back when the map comes closer.
  const Tagged = Stamped.extend({
    _worn() {
      return tier('live') === 'label' ? this.options.tag : null;
    },
    // The canvas only repaints inside the bounds a marker claims, and the
    // name reaches past the radius.
    _updateBounds() {
      Stamped.prototype._updateBounds.call(this);
      const worn = this._worn();
      if (!worn) return;
      const half = worn.width / DENSITY / 2 + 1;
      this._pxBounds.extend(this._point.subtract([half, this._radius + TAG_GAP + worn.height / DENSITY + 1]));
      this._pxBounds.extend(this._point.add([half, 0]));
    },
    _updatePath() {
      Stamped.prototype._updatePath.call(this);
      const worn = this._worn();
      if (worn && this._renderer._drawing && !this._empty()) stamp(this, worn, this._radius + TAG_GAP + worn.height / DENSITY / 2);
    },
    _containsPoint(p) {
      if (L.CircleMarker.prototype._containsPoint.call(this, p)) return true;
      const worn = this._worn();
      if (!worn) return false;
      const reach = this._clickTolerance();
      const tall = worn.height / DENSITY;
      const top = this._point.y - this._radius - TAG_GAP - tall;
      return Math.abs(p.x - this._point.x) <= worn.width / DENSITY / 2 + reach && p.y >= top - reach && p.y <= top + tall + TAG_GAP + reach;
    },
  });

  // Pictures of versions nothing can ask for any more would otherwise pile
  // up for as long as the page is open: a head has a new version with every
  // change of skin. Those of a version still listed are kept, so nothing
  // on the map is fetched and decoded a second time. Every address ends in
  // its version.
  function prune() {
    const listed = new Set([listing.mobs.version, listing.pictures.version, ...Object.values(listing.heads)].map((v) => encodeURIComponent(v)));
    const stale = (address) => !listed.has(address.slice(address.lastIndexOf('v=') + 2));
    for (const address of [...bitmaps.keys()]) {
      if (!stale(address)) continue;
      bitmaps.delete(address);
      failed.delete(address);
    }
    for (const key of [...sprites.keys()]) if (stale(key.slice(0, key.indexOf('|')))) sprites.delete(key);
  }

  // Asks the server which pictures there are. Failing to find out changes
  // nothing: what was known stays, and what was plain stays plain.
  async function refresh() {
    if (gone) return;
    askedAt = Date.now();
    let body;
    let next;
    try {
      const res = await fetch('api/icons', { cache: 'no-cache' });
      if (res.status === 404) {
        gone = true;
        return;
      }
      if (!res.ok) return;
      body = await res.text();
      next = body === answer ? null : JSON.parse(body);
    } catch {
      return;
    }
    retry();
    if (next === null || typeof next !== 'object') return;
    answer = body;
    const mobs = next.mobs || {};
    const pictures = next.pictures || {};
    listing = {
      mobs: { version: str(mobs.version), types: new Set(Array.isArray(mobs.types) ? mobs.types : []) },
      pictures: { version: str(pictures.version), keys: new Set(Array.isArray(pictures.keys) ? pictures.keys : []) },
      names: str(next.names && next.names.version),
      heads: next.heads && typeof next.heads === 'object' ? next.heads : {},
      me: str(next.me),
    };
    if (bitmaps.size > 1024) prune();
    paint(document);
    tell('mcmap:icons', listing);
  }

  // Asked only while there is someone logged in and looking.
  function sync() {
    const idle = gone || document.hidden || document.body.classList.contains('locked') || !app.dimension();
    if (idle) {
      clearInterval(timer);
      timer = null;
    } else if (timer === null) {
      refresh();
      timer = setInterval(refresh, ICONS_MS);
    }
  }

  // The style and size chosen, where the stylesheet can read them: the
  // structures' marks are elements of the page, not strokes on a canvas.
  function dressPage() {
    const root = document.documentElement;
    root.dataset.markers = styleOf('markers');
    root.dataset.markerSize = sizeOf();
    // Which of the two sets of sizes the canvas is using, so that the
    // stylesheet's are the same ones and not its own guess at the screen.
    root.toggleAttribute('data-dense', DENSITY === 2);
  }
  dressPage();

  // Everything a layer, a panel or a card needs to draw what the map
  // draws, by key.
  const registry = {
    STYLES,
    SIZES,
    STATES,
    PICTURES_FROM,
    LABELS_FROM,
    style: styleOf,
    size: sizeOf,
    faces,
    tier,
    keyOf,
    // Which renditions the server has of a key, as addresses or null.
    renditions,
    // The address of the one drawn in the style chosen, or null.
    chosen,
    // The marker as the map draws it, or null where it draws a plain one.
    marker,
    // The same picture as an element of the page.
    picture,
    halo,
  };

  app.icons = {
    get DENSITY() {
      return DENSITY;
    },
    density: () => DENSITY,
    MOB_RADIUS,
    BABY_RADIUS,
    PLATE_RADIUS,
    // The same, and a head's and a dot's, at the size and in the style
    // the viewer has chosen: asked for when used, since either may change.
    sizes,
    // Whether this browser can draw a picture at all.
    decodes,
    listing: () => listing,
    askedAt: () => askedAt,
    refresh,
    keyOf,
    bitmap,
    sprite,
    mob,
    plate,
    picture,
    paint,
    stamp,
    Stamped,
    tag,
    Tagged,
    registry,
    states: STATES,
    // Whether a kind of thing is drawn as pictures at all, as the viewer
    // has it: live for mobs and players, markers for everything placed.
    pictured: (domain) => styleOf(domain) !== 'dots',
    // What a layer that draws from here must make its markers anew for.
    composedAs: () => composedAs(),
  };

  // A sprite and a name tag are composed in the theme's colours, in the
  // style and at the size chosen, so a change to any makes them all anew;
  // the pictures they are composed from are kept, and nothing is fetched
  // again. The layers that wear them hear the same change after this has,
  // and dress their markers from what is made here then.
  const composedAs = () => {
    const { theme, size, text, style, mobPicture, picturesLive, picturesMarkers } = look();
    return [theme, size, text, style, mobPicture, picturesLive, picturesMarkers, DENSITY].join('|');
  };
  let composed = composedAs();
  document.addEventListener('mcmap:settings', (e) => {
    if (!e.detail || !e.detail.sections.includes('look') || composed === composedAs()) return;
    composed = composedAs();
    sprites.clear();
    tags.clear();
    paint(document);
    dressPage();
  });

  // The screen's density changes when the window is moved to another
  // screen or the page is zoomed. Leaflet decides once, at load, whether
  // its canvas is drawn at twice the size; it is told again here, and
  // everything composed for the old density is composed for the new
  // through the same door a change of size comes in by. A query matches
  // one density, so each change is listened for afresh.
  function rescale() {
    const dense = window.devicePixelRatio > 1;
    if (dense === (DENSITY === 2)) return;
    L.Browser.retina = dense;
    DENSITY = dense ? 2 : 1;
    document.dispatchEvent(new CustomEvent('mcmap:settings', { detail: { sections: ['look'] } }));
    const told = new Set();
    app.map.eachLayer((layer) => {
      const renderer = layer._renderer;
      if (!renderer || told.has(renderer) || typeof renderer._update !== 'function') return;
      told.add(renderer);
      renderer._update();
    });
    tell('mcmap:pictures');
  }
  function watchDensity() {
    if (typeof matchMedia !== 'function') return;
    const query = matchMedia(`(resolution: ${window.devicePixelRatio}dppx)`);
    if (!query.addEventListener) return;
    query.addEventListener('change', () => {
      rescale();
      watchDensity();
    }, { once: true });
  }
  watchDensity();
  // Not every browser tells a query of the change, and every one that
  // changes the density changes the page's size in its own pixels.
  window.addEventListener('resize', rescale);

  // Coming closer or going further out changes what is drawn only where
  // it crosses from dots to pictures or from pictures to names, and the
  // layers are told then and not at every step.
  const tiers = () => `${tier('live')}|${tier('markers')}`;
  let tiered = tiers();
  app.map.on('zoomend', () => {
    if (tiered === tiers()) return;
    tiered = tiers();
    tell('mcmap:pictures');
  });

  document.addEventListener('mcmap:view', sync);
  document.addEventListener('visibilitychange', sync);
})();
