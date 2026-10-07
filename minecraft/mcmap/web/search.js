'use strict';

// One box that looks a piece of text up in everything on the map that has
// a name. The server does the looking, across every dimension, and answers
// nearest first from the middle of the view; this shows the answer as a
// list worked by keyboard or pointer, and takes the map to whatever is
// chosen from it.
(() => {
  const app = window.mcmap;
  // The page and its scripts are cached apart for a few minutes, so just
  // after a release this can meet a map that cannot be sent anywhere yet.
  if (!app || !app.go || !app.icons || !app.names) return;
  const { map, icons, names } = app;

  const el = {
    form: document.getElementById('search'),
    box: document.getElementById('search-box'),
    clear: document.getElementById('search-clear'),
    panel: document.getElementById('search-panel'),
    list: document.getElementById('search-results'),
    note: document.getElementById('search-note'),
  };
  if (!el.form || !el.box || !el.panel || !el.list || !el.note) return;

  // A search is asked for this long after the last keystroke, so a word
  // typed is one request and not one a letter.
  const SETTLE_MS = 300;
  const LIMIT = 20;
  // How long the mark stays on the place that was chosen.
  const FOUND_MS = 20_000;

  const KINDS = {
    biome: 'Biome',
    structure: 'Structure',
    spawn: 'World spawn',
    bed: 'Bed',
    container: 'Container',
    mob: 'Named mob',
    waypoint: 'Waypoint',
    player: 'Player',
    coordinates: 'Coordinates',
    chunk: 'Chunk',
  };
  const CHUNK = 16;

  // What is said after a structure the world has not recorded: the seed
  // puts one there, or puts a site there in terrain not generated yet,
  // where the biome will decide.
  const UNSURE = { predicted: 'predicted', candidate: 'possible site' };

  let timer = null;
  let request = null;
  // What the list shows: the hits, and which of them the keyboard is on.
  let hits = [];
  let shownFor = '';
  let active = -1;
  let found = null;
  let foundTimer = null;

  const fmt = (n) => n.toLocaleString('en-US');
  const str = (v) => (typeof v === 'string' ? v : '');

  // Everything a hit says is set as text. A container's, a mob's and a
  // waypoint's name are a player's choice, and the rest is the server's.
  const text = (tag, className, s) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    node.textContent = s;
    return node;
  };

  const same = (a, b) => a.toLowerCase() === b.toLowerCase();

  // What a hit is listed as: the most particular thing known of it first,
  // which is the name a player gave it, with what it is after, or failing
  // that what it is alone. The server sends a mob's type, a container's
  // kind and a structure's as the game's identifiers, and each is said by
  // the game's own name for it, as everywhere else on the page.
  function titleOf(hit) {
    const name = str(hit.name);
    const detail = str(hit.detail);
    // A gamertag, which is the player's choice and is only ever text.
    if (hit.kind === 'player') return name || KINDS.player;
    if (hit.kind === 'coordinates') return Number.isFinite(hit.y) ? `X ${fmt(hit.x)}, Y ${fmt(hit.y)}, Z ${fmt(hit.z)}` : `X ${fmt(hit.x)}, Z ${fmt(hit.z)}`;
    if (hit.kind === 'chunk') return `Chunk ${fmt(hit.cx)}, ${fmt(hit.cz)}`;
    if (hit.kind === 'bed') return names.bed(hit.colour);
    if (hit.kind === 'mob') return names.mob(name, detail, hit.baby);
    if (hit.kind === 'structure') {
      const what = detail ? names.structure(detail) : name || KINDS.structure;
      // One worked out from the seed is never listed as one the world has.
      return Object.hasOwn(UNSURE, hit.certainty) ? `${what} (${UNSURE[hit.certainty]})` : what;
    }
    if (hit.kind === 'container') {
      // One nobody named is sent under what it is.
      const what = names.holder(detail, hit.colour, hit.trapped);
      return name && !same(name, what) ? `${name} (${what})` : what;
    }
    // A biome answers to its older identifier as well, so that is said.
    const also = hit.kind === 'biome' && detail ? names.tidy(detail) : '';
    if (!name) return also || KINDS[hit.kind] || 'Place';
    return also && !same(also, name) ? `${name} (${also})` : name;
  }

  // The small label beside it, or none where the title already says it: a
  // bed is listed as "Red Bed", and not as a bed twice.
  function kindOf(hit, title) {
    const kind = KINDS[hit.kind] || 'Place';
    return title.toLowerCase().includes(kind.toLowerCase()) ? '' : kind;
  }

  const SORTS = new Set(['bed', 'container', 'mob', 'structure', 'waypoint']);
  // The picture the map draws it with, for the kinds that have one.
  function pictureOf(hit) {
    if (!SORTS.has(hit.kind)) return null;
    return icons.picture(icons.keyOf(hit.kind, { kind: str(hit.detail), colour: hit.colour, trapped: hit.trapped }));
  }

  // What a position is worth, for a hit that moves: where a player or a
  // loaded mob is now, or where the last snapshot left a mob.
  function standingOf(hit) {
    if (hit.live === true) return 'live · ';
    return hit.kind === 'mob' ? 'last saved · ' : '';
  }

  function whereOf(hit) {
    // What the viewer typed is where it is; all that is left to say is
    // which dimension the map would go to it in.
    if (hit.kind === 'coordinates') return `in ${app.label(hit.dimension)}`;
    if (hit.kind === 'chunk') {
      return `blocks X ${fmt(hit.cx * CHUNK)} to ${fmt(hit.cx * CHUNK + CHUNK - 1)}, Z ${fmt(hit.cz * CHUNK)} to ${fmt(hit.cz * CHUNK + CHUNK - 1)} · in ${app.label(hit.dimension)}`;
    }
    const at = standingOf(hit) + (Number.isFinite(hit.y) ? `${fmt(hit.x)}, ${fmt(hit.y)}, ${fmt(hit.z)}` : `${fmt(hit.x)}, ${fmt(hit.z)}`);
    // A distance is given only within the dimension asked from: no walk
    // leads to another.
    if (hit.dimension !== app.dimension()) return `${at} · in ${app.label(hit.dimension)}`;
    return Number.isFinite(hit.distance) ? `${at} · ${fmt(hit.distance)} blocks away` : at;
  }

  function open(shown) {
    if (el.panel.hidden === !shown) return;
    el.panel.hidden = !shown;
    el.box.setAttribute('aria-expanded', String(shown));
    // On a narrow screen the list takes its room from the map instead of
    // covering it, and Leaflet has to be told the map changed size.
    map.invalidateSize();
  }

  function mark(at) {
    active = at;
    [...el.list.children].forEach((item, i) => item.setAttribute('aria-selected', String(i === at)));
    if (at < 0) {
      el.box.removeAttribute('aria-activedescendant');
      return;
    }
    el.box.setAttribute('aria-activedescendant', el.list.children[at].id);
    el.list.children[at].scrollIntoView({ block: 'nearest' });
  }

  function itemOf(hit, i) {
    const item = document.createElement('li');
    item.id = `search-hit-${i}`;
    item.setAttribute('role', 'option');
    const title = titleOf(hit);
    const kind = kindOf(hit, title);
    if (kind) item.append(text('span', 'kind', kind));
    else item.className = 'plain';
    const name = text('span', 'name', title);
    const picture = pictureOf(hit);
    if (picture) name.prepend(picture);
    item.append(name, text('span', 'where', whereOf(hit)));
    item.addEventListener('click', () => choose(i));
    return item;
  }

  function show(list, note, query) {
    hits = list;
    if (query !== undefined) shownFor = query;
    el.list.replaceChildren(...list.map(itemOf));
    el.list.hidden = list.length === 0;
    el.note.textContent = note;
    el.note.hidden = note === '';
    mark(-1);
    open(true);
  }

  function reset() {
    clearTimeout(timer);
    if (request) request.abort();
    request = null;
    hits = [];
    shownFor = '';
    el.list.replaceChildren();
    open(false);
    mark(-1);
  }

  // --- shorthand -----------------------------------------------------------
  //
  // A few things typed in the box are not a name to look up: coordinates
  // and a chunk are places already, and spawn, me and @name say which one
  // kind of thing is wanted. Each is read here and answered from what the
  // page or the server already offers. None of them is a command for the
  // game, and nothing here writes one.

  const myName = () => (app.myName ? app.myName() : '');

  // What was typed, as { hits } for a place that needs no looking up,
  // { note } for shorthand that cannot be answered, { query, kind } for a
  // search kept to one kind, or null for an ordinary search.
  function shorthand(typed) {
    const dimension = app.dimension();
    const at = app.coordinates ? app.coordinates(typed) : null;
    if (at) {
      const hit = { kind: 'coordinates', dimension, x: Math.floor(at.x), z: Math.floor(at.z) };
      if (Number.isFinite(at.y)) hit.y = Math.floor(at.y);
      return { hits: [hit] };
    }
    const chunk = /^chunk\s+([+-]?\d{1,7})[\s,]+([+-]?\d{1,7})$/i.exec(typed);
    if (chunk) {
      const cx = Number(chunk[1]);
      const cz = Number(chunk[2]);
      // Listed at its middle, which is where the map goes.
      return { hits: [{ kind: 'chunk', dimension, cx, cz, x: cx * CHUNK + CHUNK / 2, z: cz * CHUNK + CHUNK / 2 }] };
    }
    if (/^chunk\b/i.test(typed)) return { note: 'For a chunk, type its two chunk coordinates: chunk 7 -21.' };
    if (/^spawn$/i.test(typed)) return { query: 'world spawn', kind: 'spawn', none: 'This world’s spawn is not known yet.' };
    if (/^me$/i.test(typed)) {
      const name = myName();
      if (!name) return { note: 'The map does not know which player you are, so it cannot find you.' };
      return { query: name, kind: 'player', mine: true, none: 'You are not online in the game right now.' };
    }
    if (typed.startsWith('@')) {
      const name = typed.slice(1).trim();
      if (name === '') return { note: 'Type a gamertag after the @ to look among the players online.' };
      return { query: name, kind: 'player', none: `No player online is called “${name}”.` };
    }
    return null;
  }

  // Asks the server, for everything or for one kind, and gives back the
  // hits and the answer they came in, or null if the request was replaced
  // or failed.
  async function ask(query, kind, mine) {
    const dimension = app.dimension();
    if (!dimension) return null;
    const c = map.getCenter();
    const address = `api/search?q=${encodeURIComponent(query)}&dimension=${encodeURIComponent(dimension)}`
      + `&x=${Math.floor(c.lng)}&z=${Math.floor(c.lat)}&limit=${LIMIT}${kind ? `&kind=${encodeURIComponent(kind)}` : ''}`;
    const res = await fetch(address, { cache: 'no-store', signal: mine.signal });
    if (!res.ok) throw new Error(String(res.status));
    const data = await res.json();
    const list = (Array.isArray(data.hits) ? data.hits : [])
      .filter((h) => h && str(h.dimension) !== '' && Number.isFinite(h.x) && Number.isFinite(h.z));
    return { list, data };
  }

  async function run(typed) {
    // The answer to an earlier query is no longer wanted, and must not
    // arrive after this one's and replace it.
    if (request) request.abort();
    request = null;
    const short = shorthand(typed);
    if (short && short.hits) {
      show(short.hits, '', typed);
      return;
    }
    if (short && short.note) {
      show([], short.note, typed);
      return;
    }
    const query = short ? short.query : typed;
    const dimension = app.dimension();
    if (!dimension) return;
    const mine = new AbortController();
    request = mine;
    let answer;
    try {
      answer = await ask(query, short ? short.kind : '', mine);
    } catch {
      if (request !== mine) return; // superseded
      request = null;
      show([], 'The search could not be run. Try again in a moment.', typed);
      return;
    }
    if (request !== mine || !answer) return;
    request = null;
    const { data } = answer;
    // "me" is the one player the session is, and nobody whose gamertag
    // merely holds the same letters.
    const list = short && short.mine ? answer.list.filter((h) => app.isMe && app.isMe(h.name)) : answer.list;
    const notes = [];
    if (list.length === 0) notes.push(short ? short.none : `Nothing on the map is called “${typed}”.`);
    if (data.more > 0 && !(short && short.mine)) notes.push(`${fmt(data.more)} more not shown. Type more to narrow it down.`);
    if (data.waypoints === 'unavailable') notes.push('Your waypoints could not be searched just now.');
    show(list, notes.join(' '), typed);
  }

  function unmark() {
    clearTimeout(foundTimer);
    if (found) found.layer.remove();
    found = null;
  }

  // Whether there is anything a clearing would take away.
  const dirty = () => el.box.value !== '' || !el.panel.hidden || found !== null;

  // Shown only while there is something typed: an empty box has nothing
  // to clear, and a button that does nothing is one more stop for Tab.
  function offer() {
    if (el.clear) el.clear.hidden = el.box.value === '';
  }

  // Empties the box, shuts the list and takes the mark off the map, and
  // leaves the cursor in the box, ready for the next search.
  function clear() {
    el.box.value = '';
    reset();
    unmark();
    offer();
    el.box.focus();
  }

  // A ring on the place chosen, with its name, for long enough to see
  // where the map went.
  function point(hit) {
    unmark();
    const layer = L.marker([hit.z + 0.5, hit.x + 0.5], {
      icon: L.divIcon({ className: 'found', iconSize: [28, 28], iconAnchor: [14, 14] }),
      interactive: false,
      keyboard: false,
      zIndexOffset: 1000,
    }).bindTooltip(text('span', '', titleOf(hit)), { permanent: true, direction: 'top', offset: [0, -14], className: 'marker-tip' });
    layer.addTo(map);
    found = { layer, hit, dimension: hit.dimension };
    foundTimer = setTimeout(unmark, FOUND_MS);
  }

  function choose(at) {
    const hit = hits[at];
    if (!hit) return;
    open(false);
    if (hit.kind === 'chunk' && app.chunk && hit.dimension === app.dimension()) {
      // The chunk's own script goes there, pins it and shows the grid.
      unmark();
      app.chunk.go(hit.cx, hit.cz);
      map.getContainer().focus();
      return;
    }
    // The middle of the block, not its north-west corner.
    const middle = hit.kind === 'chunk' ? 0 : 0.5;
    if (!app.go(hit.dimension, hit.x + middle, hit.z + middle)) {
      show(hits, `${app.label(hit.dimension)} has not been rendered yet, so the map cannot go there.`);
      return;
    }
    // A player or a mob moves, so it is marked by the card that tracks it
    // and not by a ring on where it was when the list was made.
    if ((hit.kind === 'player' || hit.kind === 'mob') && app.inspect) {
      unmark();
      app.inspect.open({
        kind: hit.kind, id: str(hit.id) || null, name: str(hit.name), type: str(hit.detail), baby: hit.baby === true,
        x: hit.x + 0.5, y: hit.y, z: hit.z + 0.5, dimension: hit.dimension,
        saved: hit.kind === 'mob' && hit.live !== true, savedAt: app.markers ? app.markers.savedAt() : null,
      });
    } else {
      point(hit);
    }
    // The stretch found is the one to look at, so the rest is dimmed.
    if (hit.kind === 'biome' && app.biomes) app.biomes.show(str(hit.detail));
    // A structure is shown in full, as a click on its mark would show it.
    if (hit.kind === 'structure' && app.structures) {
      app.structures.show({ kind: str(hit.detail), recorded: !Object.hasOwn(UNSURE, hit.certainty), x: hit.x, z: hit.z, dimension: hit.dimension });
      return;
    }
    // The keyboard goes back to the map, and a phone puts its own away.
    map.getContainer().focus();
  }

  el.box.addEventListener('input', () => {
    offer();
    clearTimeout(timer);
    const query = el.box.value.trim();
    if (query === '') {
      reset();
      return;
    }
    // The answer still on its way is to what was typed before, and would
    // otherwise land in the pause before this is asked. What is listed
    // stays until its replacement arrives, so the list does not blink.
    if (request) request.abort();
    request = null;
    timer = setTimeout(() => run(query), SETTLE_MS);
  });

  el.box.addEventListener('focus', () => {
    if (hits.length > 0 || el.note.textContent !== '') open(el.box.value.trim() !== '');
  });

  el.box.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      // The first press clears the search and stays in the box; with
      // nothing left to clear the next one leaves it. Neither is also an
      // Escape for the card about a mob, which stays open.
      e.stopPropagation();
      e.preventDefault();
      if (dirty()) clear();
      else map.getContainer().focus();
    } else if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
      if (hits.length === 0) return;
      e.preventDefault();
      open(true);
      const step = e.key === 'ArrowDown' ? 1 : -1;
      mark(active < 0 ? (step > 0 ? 0 : hits.length - 1) : (active + step + hits.length) % hits.length);
    }
  });

  el.form.addEventListener('submit', (e) => {
    e.preventDefault();
    const query = el.box.value.trim();
    if (el.panel.hidden || hits.length === 0 || query !== shownFor) {
      // Enter before the pause is up asks at once, rather than choose
      // from the answer to what was typed before.
      clearTimeout(timer);
      if (query !== '') run(query);
      return;
    }
    choose(Math.max(active, 0));
  });

  if (el.clear) {
    el.clear.addEventListener('click', clear);
    // Pressing the button must not take the focus from the box first: on a
    // phone that would put the keyboard away and bring it back.
    el.clear.addEventListener('pointerdown', (e) => e.preventDefault());
  }
  offer();

  document.addEventListener('pointerdown', (e) => {
    if (!el.form.contains(e.target)) open(false);
  });

  // Escape anywhere but in a box clears a search that has left something
  // on the page, before it closes anything else: one press, one thing.
  // Heard on the way down, so that the card's own Escape does not also
  // hear it.
  document.addEventListener('keydown', (e) => {
    if (e.key !== 'Escape' || e.defaultPrevented || !dirty()) return;
    if (e.target instanceof Element && (e.target.matches('input, textarea, select') || e.target.closest('dialog[open]'))) return;
    e.stopPropagation();
    el.box.value = '';
    reset();
    unmark();
    offer();
  }, true);

  // For the shortcuts: the box, and the two places that are gone to
  // without choosing from a list.
  app.search = {
    focus() {
      if (el.form.hidden) return;
      el.box.focus();
      el.box.select();
    },
    async jump(what) {
      const short = shorthand(what);
      if (!short || !short.query) {
        if (short && short.note && app.tell) app.tell(short.note);
        return;
      }
      const mine = new AbortController();
      let answer = null;
      try { answer = await ask(short.query, short.kind, mine); } catch { /* said below */ }
      if (!answer) {
        if (app.tell) app.tell('That could not be looked up just now. Try again in a moment.');
        return;
      }
      const list = short.mine ? answer.list.filter((h) => app.isMe && app.isMe(h.name)) : answer.list;
      if (list.length === 0) {
        if (app.tell) app.tell(short.none);
        return;
      }
      hits = list;
      choose(0);
      hits = [];
    },
  };

  // What is listed, and the mark on the place chosen, were titled with the
  // names there were then.
  document.addEventListener('mcmap:names', () => {
    if (hits.length > 0) {
      const at = active;
      el.list.replaceChildren(...hits.map(itemOf));
      mark(at);
    }
    if (found) found.layer.setTooltipContent(text('span', '', titleOf(found.hit)));
  });

  document.addEventListener('mcmap:view', () => {
    const locked = document.body.classList.contains('locked');
    el.form.hidden = locked || !app.dimension();
    if (locked) {
      // Logged out: nothing of the last player's stays on the page.
      el.box.value = '';
      reset();
      unmark();
      offer();
    } else if (found && found.dimension !== app.dimension()) {
      unmark();
    }
  });
})();
