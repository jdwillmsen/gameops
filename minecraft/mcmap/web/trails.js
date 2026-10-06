'use strict';

// Where each player has recently been, a line per stretch walked. The
// viewer's own trail is the green the live layer draws them in, and every
// other player's is a colour of their own, the same on every visit. The
// server keeps the trails and is asked for them once a minute; between
// answers the lines are carried forward from the live frames the page is
// already getting, by the server's own rules, and the next answer replaces
// whatever was drawn that way.
(() => {
  const app = window.mcmap;
  // The page and its scripts are cached apart for a few minutes, so just
  // after a release this can meet a page that has no panel yet.
  if (!app || !app.layers || !app.layers.register) return;
  const { map } = app;

  const WINDOW_KEY = 'mcmap.trails';
  // Hours of trail the viewer may ask for. The server keeps a day unless
  // it is set to keep less, and says how long in every answer.
  const WINDOWS = [1, 6, 24];
  const REFRESH_MS = 60_000;
  const CHECK_MS = 30_000;
  // The server's rules for a trail, for carrying one forward between
  // answers: a point every four blocks moved, and a new line after half a
  // minute unseen or a jump no walk makes.
  const STEP_BLOCKS = 4;
  const BREAK_BLOCKS = 256;
  const BREAK_MS = 30_000;
  const INK = '#0b0c0e';
  // What the other players' trails are drawn in: far apart from each
  // other, and from the green that is the viewer's own, and light enough
  // to read over dark ground with the dark line under each for light. A
  // player's is picked by their gamertag, so it is theirs on every visit,
  // unless someone else on the map was given it first. With more players
  // than colours some share one, and the list under the row and the name
  // on hover still tell them apart.
  const PALETTE = ['#ff5c5c', '#ff9f1c', '#ffe14d', '#4d9fff', '#b97aff', '#ff66c4', '#ffffff', '#9be7ff'];

  // The live layer's markers are on one canvas, which takes every pointer
  // event over the map. A line on that same canvas, behind the markers, is
  // under them and can still be hovered, and a click on a marker is still
  // the marker's. Without that canvas the lines go under it in a pane of
  // their own, where they cannot be hovered and the list names them.
  const shared = app.liveRenderer || null;
  if (!shared) map.createPane('trails').style.zIndex = 390;
  const drawing = shared ? { renderer: shared } : { renderer: L.svg({ pane: 'trails', padding: 0.5 }), pane: 'trails' };

  // A gamertag is a player's choice, and Leaflet treats a string given to
  // a tooltip as HTML. An element holding text is the form it cannot
  // interpret.
  const text = (s) => {
    const span = document.createElement('span');
    span.textContent = s;
    return span;
  };

  const lines = L.featureGroup();
  lines.bindTooltip((line) => text(line.options.label), { sticky: true, direction: 'top', className: 'live-tip' });

  let hours = WINDOWS[0];
  try {
    const saved = JSON.parse(localStorage.getItem(WINDOW_KEY) || 'null');
    if (saved && WINDOWS.includes(saved.hours)) hours = saved.hours;
  } catch { /* a browser that refuses storage still gets the default */ }

  // Whether the service keeps trails at all; null until it has answered.
  let available = null;
  let row = null;
  // What the lines on the map are of, as "<dimension>|<hours>", or null.
  let drawn = null;
  let fetchedAt = 0;
  let pending = null;
  let limits = { more: 0, maxAgeSeconds: 0 };
  let bodyShown = false;
  // gamertag in lower case -> { name, colour, all, line, casing, from,
  // last, seenAt }: all is every line of theirs, and line and casing the
  // pair still growing.
  const trails = new Map();
  // What the list of players was last built from.
  let listed = '';

  const body = document.createElement('div');
  body.className = 'trail-body';
  const players = document.createElement('ul');
  players.className = 'legend trail-players';
  players.setAttribute('aria-label', 'Players with a trail here. Choose one to see the whole of it.');
  const picker = document.createElement('label');
  picker.className = 'trail-window';
  const select = document.createElement('select');
  for (const h of WINDOWS) {
    const option = document.createElement('option');
    option.value = String(h);
    option.textContent = `${h} h`;
    select.append(option);
  }
  select.value = String(hours);
  picker.append('Last ', select);
  body.append(picker, players);

  const on = () => row !== null && row.enabled;
  const clock = (seconds) => new Date(seconds * 1000).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' });

  // FNV-1a over the gamertag as the game compares it, without regard to
  // case.
  function hash(name) {
    let h = 0x811c9dc5;
    for (const c of name.toLowerCase()) h = Math.imul(h ^ c.codePointAt(0), 0x01000193);
    return h >>> 0;
  }

  // The viewer's own colour is the live layer's to give, so that the trail
  // and the marker at the end of it are one colour. Anyone else gets the
  // colour their gamertag picks, or the next one along that nobody on the
  // map has yet.
  function colourOf(name) {
    if (app.isMe && app.playerColour && app.isMe(name)) return app.playerColour(name);
    const taken = new Set([...trails.values()].map((trail) => trail.colour));
    const first = hash(name) % PALETTE.length;
    for (let i = 0; i < PALETTE.length; i++) {
      const colour = PALETTE[(first + i) % PALETTE.length];
      if (!taken.has(colour)) return colour;
    }
    return PALETTE[first];
  }
  // The middle of the block, not its north-west corner.
  const place = (x, z) => [z + 0.5, x + 0.5];

  // Starts a line for a player at one point. A dark line under the
  // coloured one keeps a light trail readable over snow and sand, and
  // being the wider of the two it is the one the pointer is tested
  // against.
  function begin(trail, t, x, z) {
    const casing = L.polyline([place(x, z)], { ...drawing, color: INK, weight: 5.5, opacity: 0.6, interactive: Boolean(shared), label: '' });
    const line = L.polyline([place(x, z)], { ...drawing, color: trail.colour, weight: 2.5, opacity: 0.95, interactive: false });
    lines.addLayer(casing);
    lines.addLayer(line);
    // Behind every marker already on the canvas; one added later is drawn
    // over these as it is.
    if (shared && map.hasLayer(lines)) {
      line.bringToBack();
      casing.bringToBack();
    }
    Object.assign(trail, { casing, line, from: t, last: { x, z } });
    trail.all.push(casing);
    extend(trail, t, x, z, false);
  }

  function extend(trail, t, x, z, add = true) {
    if (add) {
      trail.casing.addLatLng(place(x, z));
      trail.line.addLatLng(place(x, z));
      trail.last = { x, z };
    }
    const span = clock(trail.from) === clock(t) ? clock(t) : `${clock(trail.from)} to ${clock(t)}`;
    trail.casing.options.label = `${trail.name} · ${span}`;
  }

  // The players the lines belong to, each with their colour, as buttons
  // that take the map to the whole of that player's trail. A gamertag is a
  // player's choice and is set as text.
  function list() {
    const sorted = [...trails.values()].filter((trail) => trail.all.length > 0)
      .sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: 'base' }));
    const key = sorted.map((trail) => `${trail.name}\n${trail.colour}`).join('\n');
    if (key === listed) return;
    listed = key;
    players.replaceChildren(...sorted.map((trail) => {
      const button = document.createElement('button');
      button.type = 'button';
      button.title = 'Show the whole trail';
      const swatch = document.createElement('i');
      swatch.style.backgroundColor = trail.colour;
      const name = document.createElement('span');
      name.className = 'name';
      name.textContent = trail.name;
      button.append(swatch, name);
      button.addEventListener('click', () => {
        const held = trails.get(trail.name.toLowerCase());
        if (!held) return;
        const bounds = L.latLngBounds([]);
        for (const line of held.all) bounds.extend(line.getBounds());
        // No closer than one block to a pixel: a trail of a few steps
        // would otherwise fill the screen with four blocks.
        if (bounds.isValid()) map.fitBounds(bounds, { padding: [40, 40], maxZoom: 0 });
      });
      const item = document.createElement('li');
      item.append(button);
      return item;
    }));
  }

  function clear() {
    lines.clearLayers();
    trails.clear();
    drawn = null;
    fetchedAt = 0;
    limits = { more: 0, maxAgeSeconds: 0 };
  }

  function draw(key, data) {
    clear();
    drawn = key;
    fetchedAt = Date.now();
    limits = { more: Number.isFinite(data.more) ? data.more : 0, maxAgeSeconds: Number.isFinite(data.maxAgeSeconds) ? data.maxAgeSeconds : 0 };
    // In one order whatever order the server sends them in, so that who
    // gets a colour two players both pick does not change between answers.
    const served = (Array.isArray(data.players) ? data.players : [])
      .filter((player) => player && typeof player.name === 'string' && Array.isArray(player.segments))
      .sort((a, b) => (a.name.toLowerCase() < b.name.toLowerCase() ? -1 : 1));
    for (const player of served) {
      const trail = { name: player.name, colour: colourOf(player.name), all: [], seenAt: 0 };
      for (const segment of player.segments) {
        const points = (Array.isArray(segment) ? segment : []).filter((p) => Array.isArray(p) && [p[0], p[1], p[3]].every(Number.isFinite));
        if (points.length === 0) continue;
        begin(trail, points[0][0], points[0][1], points[0][3]);
        for (const p of points.slice(1)) extend(trail, p[0], p[1], p[3]);
      }
      if (trail.all.length > 0) trails.set(player.name.toLowerCase(), trail);
    }
  }

  // Carries the lines forward from a live frame. What this draws is a
  // guess at what the server recorded, good until its next answer.
  function follow(e) {
    const frame = e.detail;
    if (!on() || !frame || drawn !== `${frame.dimension}|${hours}` || !Array.isArray(frame.players)) return;
    const now = Date.now();
    for (const p of frame.players) {
      if (!p || typeof p.n !== 'string' || !Number.isFinite(p.x) || !Number.isFinite(p.z)) continue;
      const key = p.n.toLowerCase();
      const x = Math.floor(p.x);
      const z = Math.floor(p.z);
      let trail = trails.get(key);
      if (!trail) {
        trail = { name: p.n, colour: colourOf(p.n), all: [], seenAt: 0 };
        trails.set(key, trail);
      }
      const moved = trail.last ? Math.hypot(x - trail.last.x, z - trail.last.z) : Infinity;
      const away = trail.seenAt > 0 && now - trail.seenAt > BREAK_MS;
      if (away || moved > BREAK_BLOCKS) begin(trail, now / 1000, x, z);
      else if (moved >= STEP_BLOCKS) extend(trail, now / 1000, x, z);
      trail.seenAt = now;
    }
    row.setCount(trails.size);
    list();
  }

  function span(seconds) {
    const h = seconds / 3600;
    return h >= 1 ? `${Math.round(h)} h` : `${Math.max(1, Math.round(seconds / 60))} min`;
  }

  function paint() {
    if (available !== true) {
      if (row) row.remove();
      row = null;
      bodyShown = false;
    } else if (!row) {
      row = app.layers.register({ group: 'overlays', id: 'trails', label: 'Trails', enabled: false, order: 20, swatch: 'key trail' });
      row.onToggle(sync);
    }
    const showing = on() && drawn !== null;
    if (showing && !map.hasLayer(lines)) {
      lines.addTo(map);
      // Last first, so that they end up in the order they were made: each
      // dark line under its coloured one.
      if (shared) lines.getLayers().reverse().forEach((line) => line.bringToBack());
    }
    if (!showing && map.hasLayer(lines)) map.removeLayer(lines);
    if (!row) return;
    list();
    // Only when it changes: putting the window's menu back in the page
    // would shut it under the viewer's hand.
    if (on() !== bodyShown) {
      bodyShown = on();
      row.setBody(bodyShown ? body : null);
    }
    row.setCount(showing ? trails.size : null);
    const notes = [];
    if (showing && limits.maxAgeSeconds > 0 && limits.maxAgeSeconds < hours * 3600) notes.push(`The server keeps ${span(limits.maxAgeSeconds)} of trail.`);
    if (showing && limits.more > 0) notes.push('Only the newest part of each trail is shown.');
    row.setNote(notes.join(' '));
  }

  // Asks for the trails, or, with the layer off, only whether there are
  // any to ask for: a moment's worth is the smallest answer there is.
  async function load(dimension) {
    const wanted = on();
    const key = `${dimension}|${wanted ? hours : 0}`;
    if (pending === key) return;
    pending = key;
    const since = Math.floor(Date.now() / 1000) - (wanted ? hours * 3600 : 0);
    try {
      const res = await fetch(`api/trails?dimension=${encodeURIComponent(dimension)}&since=${since}`, { cache: 'no-store' });
      if (pending !== key) return; // the view moved on while this was out
      if (res.status === 404) {
        // The service is running without trails, and will be until it
        // restarts.
        available = false;
        clear();
        paint();
        return;
      }
      // Logged out: the map's own check shows the login, and the view is
      // announced again once there is a session.
      if (!res.ok) throw new Error(String(res.status));
      const data = await res.json();
      if (pending !== key) return;
      available = true;
      if (wanted && on()) draw(key, data);
      paint();
    } catch {
      // Tried again at the next check rather than in a minute.
      fetchedAt = Date.now() - REFRESH_MS + CHECK_MS;
    } finally {
      if (pending === key) pending = null;
    }
    // The first answer is what makes the row, and a row the viewer left on
    // last time wants its lines now, not at the next check.
    if (!wanted && on() && drawn === null) sync();
  }

  // Brings the lines in line with the view: the current dimension's, over
  // the window chosen, and none at all while off or logged out.
  function sync() {
    if (available === false) return;
    const locked = document.body.classList.contains('locked');
    const dimension = app.dimension();
    if (locked || !dimension) {
      // An answer still on its way belongs to the view that asked for it.
      pending = null;
      clear();
      paint();
      return;
    }
    if (available === null) {
      load(dimension);
      return;
    }
    if (!on()) {
      pending = null;
      clear();
      paint();
      return;
    }
    if (drawn !== `${dimension}|${hours}`) {
      clear();
      paint();
      load(dimension);
    } else if (Date.now() - fetchedAt > REFRESH_MS && !document.hidden) {
      load(dimension);
    }
  }

  select.addEventListener('change', () => {
    const chosen = Number(select.value);
    if (!WINDOWS.includes(chosen)) return;
    hours = chosen;
    try { localStorage.setItem(WINDOW_KEY, JSON.stringify({ hours })); } catch { /* not kept, still applied */ }
    sync();
  });

  document.addEventListener('mcmap:players', follow);
  document.addEventListener('mcmap:view', sync);
  document.addEventListener('visibilitychange', sync);
  setInterval(sync, CHECK_MS);
  sync();
})();
