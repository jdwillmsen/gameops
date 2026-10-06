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
  if (!app || !app.go) return;
  const { map } = app;

  const el = {
    form: document.getElementById('search'),
    box: document.getElementById('search-box'),
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
  };

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
  // which is the name a player gave it, or failing that what sort of
  // thing it is. The server sends a mob's type and a container's kind as
  // the game's identifiers; a structure's says only what its name does.
  function titleOf(hit) {
    const name = str(hit.name);
    const type = hit.kind === 'structure' ? '' : str(hit.detail).replace(/_/g, ' ');
    if (!name) return type || KINDS[hit.kind] || 'Place';
    return type && !same(type, name) ? `${name} (${type})` : name;
  }

  // The small label beside it, or none where it would only repeat the
  // title: a bed is listed as "Bed", once.
  function kindOf(hit, title) {
    const kind = KINDS[hit.kind] || 'Place';
    return same(kind, title) ? '' : kind;
  }

  function whereOf(hit) {
    const at = Number.isFinite(hit.y) ? `${fmt(hit.x)}, ${fmt(hit.y)}, ${fmt(hit.z)}` : `${fmt(hit.x)}, ${fmt(hit.z)}`;
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

  function show(list, note, query) {
    hits = list;
    if (query !== undefined) shownFor = query;
    el.list.replaceChildren(...list.map((hit, i) => {
      const item = document.createElement('li');
      item.id = `search-hit-${i}`;
      item.setAttribute('role', 'option');
      const title = titleOf(hit);
      const kind = kindOf(hit, title);
      if (kind) item.append(text('span', 'kind', kind));
      else item.className = 'plain';
      item.append(text('span', 'name', title), text('span', 'where', whereOf(hit)));
      item.addEventListener('click', () => choose(i));
      return item;
    }));
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

  async function run(query) {
    // The answer to an earlier query is no longer wanted, and must not
    // arrive after this one's and replace it.
    if (request) request.abort();
    const dimension = app.dimension();
    if (!dimension) return;
    const mine = new AbortController();
    request = mine;
    const c = map.getCenter();
    const address = `api/search?q=${encodeURIComponent(query)}&dimension=${encodeURIComponent(dimension)}`
      + `&x=${Math.floor(c.lng)}&z=${Math.floor(c.lat)}&limit=${LIMIT}`;
    let data;
    try {
      const res = await fetch(address, { cache: 'no-store', signal: mine.signal });
      if (!res.ok) throw new Error(String(res.status));
      data = await res.json();
    } catch {
      if (request !== mine) return; // superseded
      request = null;
      show([], 'The search could not be run. Try again in a moment.', query);
      return;
    }
    if (request !== mine) return;
    request = null;
    const list = (Array.isArray(data.hits) ? data.hits : [])
      .filter((h) => h && str(h.dimension) !== '' && Number.isFinite(h.x) && Number.isFinite(h.z));
    const notes = [];
    if (list.length === 0) notes.push(`Nothing on the map is called “${query}”.`);
    if (data.more > 0) notes.push(`${fmt(data.more)} more not shown. Type more to narrow it down.`);
    if (data.waypoints === 'unavailable') notes.push('Your waypoints could not be searched just now.');
    show(list, notes.join(' '), query);
  }

  function unmark() {
    clearTimeout(foundTimer);
    if (found) found.layer.remove();
    found = null;
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
    found = { layer, dimension: hit.dimension };
    foundTimer = setTimeout(unmark, FOUND_MS);
  }

  function choose(at) {
    const hit = hits[at];
    if (!hit) return;
    open(false);
    // The middle of the block, not its north-west corner.
    if (!app.go(hit.dimension, hit.x + 0.5, hit.z + 0.5)) {
      show(hits, `${app.label(hit.dimension)} has not been rendered yet, so the map cannot go there.`);
      return;
    }
    point(hit);
    // The stretch found is the one to look at, so the rest is dimmed.
    if (hit.kind === 'biome' && app.biomes) app.biomes.show(str(hit.detail));
    // The keyboard goes back to the map, and a phone puts its own away.
    map.getContainer().focus();
  }

  el.box.addEventListener('input', () => {
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
      if (el.panel.hidden) return;
      // Only the list is closed: the card about a mob stays open, and so
      // does what was typed, which a search box would otherwise clear.
      e.stopPropagation();
      e.preventDefault();
      open(false);
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

  document.addEventListener('pointerdown', (e) => {
    if (!el.form.contains(e.target)) open(false);
  });

  document.addEventListener('mcmap:view', () => {
    const locked = document.body.classList.contains('locked');
    el.form.hidden = locked || !app.dimension();
    if (locked) {
      // Logged out: nothing of the last player's stays on the page.
      el.box.value = '';
      reset();
      unmark();
    } else if (found && found.dimension !== app.dimension()) {
      unmark();
    }
  });
})();
