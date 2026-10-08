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
  const DENSITY = L.Browser.retina ? 2 : 1;

  // Sizes in CSS pixels. A mob's icon is 16 texture pixels drawn one to
  // one inside a ring, and a marker's picture the same inside a square
  // plate: round is what moves and square is what stays put. A baby is
  // the same icon drawn smaller.
  const ICON = 16;
  const MOB_RADIUS = 11;
  const BABY_ICON = 12;
  const BABY_RADIUS = 8;
  const PLATE_RADIUS = 10;
  // The viewer may have every marker smaller or larger. Larger is the next
  // whole number of screen pixels to a texture pixel, which on a dense
  // screen is one and a half times the size and on any other twice: pixel
  // art is only ever enlarged whole. Smaller is the size a baby has always
  // been drawn at, and is blended the way a baby is, since a picture
  // cannot be made smaller than it was drawn without losing pixels. A
  // head is 8 texture pixels a side, so each of its sizes is whole.
  const BIG = DENSITY === 2 ? 24 : 32;
  const SIZES = {
    small: { icon: BABY_ICON, mob: 9, babyIcon: 8, baby: 6, plate: 8, head: 16, headRadius: 10, scale: 0.8 },
    normal: { icon: ICON, mob: MOB_RADIUS, babyIcon: BABY_ICON, baby: BABY_RADIUS, plate: PLATE_RADIUS, head: 24, headRadius: 15, scale: 1 },
    large: { icon: BIG, mob: BIG / 2 + 3, babyIcon: ICON, baby: MOB_RADIUS, plate: BIG / 2 + 2, head: 32, headRadius: 19, scale: 1.4 },
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
  const sizes = () => SIZES[look().size] || SIZES.normal;
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

  // A picture ready to be stamped onto a canvas: the icon or head, its
  // ring in the layer's colour and its backing, composed once. Drawing a
  // marker is then one drawImage, whatever is in the picture. Null while
  // the picture is not decoded.
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
  const ringed = (baby) => (ring) => (ctx, drawn) => {
    const size = sizes();
    const radius = baby ? size.baby : size.mob;
    const icon = baby ? size.babyIcon : size.icon;
    ctx.beginPath();
    ctx.arc(radius, radius, radius - 1, 0, Math.PI * 2);
    ctx.fillStyle = backing();
    ctx.fill();
    // Made smaller than it was drawn, it reads better blended than with
    // pixels dropped.
    ctx.imageSmoothingEnabled = icon * DENSITY < drawn.width && icon < ICON;
    ctx.drawImage(drawn, radius - icon / 2, radius - icon / 2, icon, icon);
    // The ring is what the filters are read by, so it goes on last and
    // nothing in the icon can cover it.
    ctx.lineWidth = 1.75 + heavy();
    ctx.strokeStyle = ring;
    ctx.stroke();
  };
  const paintMob = ringed(false);
  const paintBaby = ringed(true);

  const paintPlate = (ring) => (ctx, drawn) => {
    const size = sizes();
    const side = size.plate * 2;
    ctx.beginPath();
    if (ctx.roundRect) ctx.roundRect(1, 1, side - 2, side - 2, 3);
    else ctx.rect(1, 1, side - 2, side - 2);
    ctx.fillStyle = backing();
    ctx.fill();
    ctx.imageSmoothingEnabled = size.icon * DENSITY < drawn.width && size.icon < ICON;
    ctx.drawImage(drawn, size.plate - size.icon / 2, size.plate - size.icon / 2, size.icon, size.icon);
    ctx.lineWidth = 1.5 + heavy();
    ctx.strokeStyle = ring;
    ctx.stroke();
  };

  // Where a picture is asked for, or null for one the server has not
  // listed: only what it lists is ever requested.
  // Nor is one the viewer has chosen plain dots in place of: a mob's by
  // one choice, and a bed's, a container's and a waypoint's by another. A
  // structure's is neither's.
  const mobAddress = (type) => (listing.mobs.types.has(type) && look().picturesLive !== false
    ? `api/icons/mob/${encodeURIComponent(type)}?v=${encodeURIComponent(listing.mobs.version)}` : null);
  const pictureAddress = (key) => (listing.pictures.keys.has(key) && KEY.test(key) && (look().picturesMarkers !== false || key.startsWith('structure/'))
    ? `api/icons/picture/${key}?v=${encodeURIComponent(listing.pictures.version)}` : null);
  // Either sort by one key: a mob's icon is mob/<type>.
  const addressOf = (key) => (str(key).startsWith('mob/') ? mobAddress(key.slice(4)) : pictureAddress(str(key)));

  // The key of the picture a thing is drawn with, wherever on the page it
  // is drawn: sort is bed, container, mob, structure or waypoint, and kind
  // a container's kind, a mob's type or a structure's kind. A bed whose
  // colour the world does not say is the red one the game itself falls
  // back to, and a shulker box likewise the undyed one.
  function keyOf(sort, { kind, colour, trapped } = {}) {
    if (sort === 'waypoint') return 'marker/waypoint';
    if (sort === 'bed') return `bed/${str(colour) || 'red'}`;
    if (sort === 'mob') return `mob/${str(kind)}`;
    if (sort === 'structure') return `structure/${str(kind)}`;
    if (sort !== 'container') return '';
    if (kind === 'shulker') return `shulker/${str(colour) || 'undyed'}`;
    return `container/${kind === 'chest' && trapped === true ? 'trapped_chest' : str(kind)}`;
  }

  const mob = (type, ring, baby = false) => sprite(
    mobAddress(type), ring, baby ? sizes().baby : sizes().mob, baby ? paintBaby : paintMob, baby ? 'baby' : '');
  const plate = (key, ring) => sprite(pictureAddress(key), ring, sizes().plate, paintPlate, 'plate');

  // Draws a picture into an element of the page, and says whether there
  // was one to draw. Without one the element is hidden, and whatever
  // stands beside it as the fallback shows.
  function fill(canvas) {
    const address = addressOf(canvas.dataset.picture);
    const drawn = bitmap(address);
    canvas.hidden = !drawn;
    if (!drawn || canvas.dataset.drawn === address) return;
    const ctx = canvas.getContext('2d');
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    ctx.imageSmoothingEnabled = false;
    ctx.drawImage(drawn, 0, 0, canvas.width, canvas.height);
    canvas.dataset.drawn = address;
  }

  // A picture for the page itself, outside the map: a panel row, a search
  // result, a tooltip. It appears by itself once the server has it.
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

  // A marker that is its picture, or, with none to draw, the circle it
  // has always been.
  const Stamped = L.CircleMarker.extend({
    _updatePath() {
      if (!this.options.sprite) {
        L.CircleMarker.prototype._updatePath.call(this);
        return;
      }
      if (this._renderer._drawing && !this._empty()) stamp(this, this.options.sprite);
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
  // a click on what it labels.
  const Tagged = Stamped.extend({
    // The canvas only repaints inside the bounds a marker claims, and the
    // name reaches past the radius.
    _updateBounds() {
      L.CircleMarker.prototype._updateBounds.call(this);
      const worn = this.options.tag;
      if (!worn) return;
      const half = worn.width / DENSITY / 2 + 1;
      this._pxBounds.extend(this._point.subtract([half, this._radius + TAG_GAP + worn.height / DENSITY + 1]));
      this._pxBounds.extend(this._point.add([half, 0]));
    },
    _updatePath() {
      Stamped.prototype._updatePath.call(this);
      const worn = this.options.tag;
      if (worn && this._renderer._drawing && !this._empty()) stamp(this, worn, this._radius + TAG_GAP + worn.height / DENSITY / 2);
    },
    _containsPoint(p) {
      if (L.CircleMarker.prototype._containsPoint.call(this, p)) return true;
      const worn = this.options.tag;
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

  app.icons = {
    DENSITY,
    MOB_RADIUS,
    BABY_RADIUS,
    PLATE_RADIUS,
    // The same, and a head's and a dot's, at the size the viewer has
    // chosen: asked for when used, since the choice may change.
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
  };

  // A sprite and a name tag are composed in the theme's colours at the
  // size chosen, so a change to either makes them all anew; the pictures
  // they are composed from are kept, and nothing is fetched again. The
  // layers that wear them hear the same change after this has, and dress
  // their markers from what is made here then.
  const composedAs = () => {
    const { theme, size, text, picturesLive, picturesMarkers } = look();
    return [theme, size, text, picturesLive, picturesMarkers].join('|');
  };
  let composed = composedAs();
  document.addEventListener('mcmap:settings', (e) => {
    if (!e.detail || !e.detail.sections.includes('look') || composed === composedAs()) return;
    composed = composedAs();
    sprites.clear();
    tags.clear();
    paint(document);
  });

  document.addEventListener('mcmap:view', sync);
  document.addEventListener('visibilitychange', sync);
})();
