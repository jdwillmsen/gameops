'use strict';

// Structures, as layers that are never drawn alike. A recorded one is a
// fact: the world's own save says it is there, and it is drawn solid, with
// the box it occupies. A predicted one is a calculation from the seed, and
// is drawn hollow and dashed. A possible one is fainter still: a place the
// seed says the generator will try, in terrain nobody has generated, where
// the biome will decide whether anything is built. The server keeps the
// seed and only offers a kind once its calculation has been seen to agree
// with what the world recorded of that kind.
//
// In the panel each of the three is a row, and under it every kind of
// structure it holds is an item that can be hidden there alone: the known
// villages and the predicted ones are two switches. The world spawn comes
// with the overworld's structures and has a row of its own among the
// overlays: it is neither recorded as a structure nor predicted.
(() => {
  const app = window.mcmap;
  // The page and its scripts are cached apart for a few minutes, so just
  // after a release this can meet a page that has no panel yet.
  if (!app || !app.layers || !app.layers.register || !app.icons || !app.names) return;
  const { map, icons, names } = app;

  // Structures change only when chunks are generated, and the server looks
  // once per snapshot; asking more often than this finds nothing new.
  const REFRESH_MS = 5 * 60_000;
  const RETRY_MS = 30_000;

  // What a kind is drawn in. Its name is the game's, asked for when it is
  // said, and its picture the server's; the letter is what stands in the
  // picture's place until there is one.
  const KINDS = {
    fortress: { letter: 'F', color: '#e5534b' },
    monument: { letter: 'M', color: '#5cc8f0' },
    outpost: { letter: 'O', color: '#d9a441' },
    witch_hut: { letter: 'H', color: '#b48ce0' },
    village: { letter: 'V', color: '#6bbf59' },
    stronghold: { letter: 'S', color: '#58c4a4' },
    trial_chamber: { letter: 'T', color: '#e08a4a' },
  };
  // The rows in the panel: the three layers, each listing its kinds, and
  // the world spawn. A kind is named by the game's word for it once that
  // is known.
  const SORTS = ['recorded', 'predicted', 'candidate'];
  const ROWS = [
    ['recorded', 'Known', 'key recorded'],
    ['predicted', 'Predicted', 'key predicted'],
    ['candidate', 'Possible', 'key candidate'],
    ['spawn', 'World spawn', 'key spawn'],
  ];
  const UNKNOWN = { letter: '?', color: '#9aa3ad' };
  // Kinds drawn and searched for only once the viewer has asked.
  const OPT_IN = new Set(['stronghold']);
  const ASK = 'Off until you turn it on, and left out of the search: this one is a thing to find for yourself.';
  // Which kinds each layer shows, which the panel keeps. A panel from
  // before it listed any shows every kind but those that are asked for.
  const choices = {};
  for (const sort of ['recorded', 'predicted', 'candidate']) {
    choices[sort] = app.layers.facet ? app.layers.facet('structures', sort, { off: [...OPT_IN] }) : { shows: (kind) => !OPT_IN.has(kind), onChange() {} };
  }
  // A kind with no item of its own has nothing to be hidden by.
  const kindOn = (sort, kind) => !Object.hasOwn(KINDS, kind) || choices[sort].shows(kind);

  const DETAILS_HINT = 'Click for details';

  // A kind the world keeps no record of is found by the blocks only it is
  // generated with, and says how many: its box is the box around those.
  const found = (s) => Number.isFinite(s.evidence) && s.evidence > 0;

  // What the world keeps about a village, for its tooltip. A village the
  // game has a record for but has not run yet has no counts, and saying so
  // is more use than a row of zeroes.
  function villageLines(v) {
    if (!v) return [];
    if (!v.counted) return ['Not counted by the game yet'];
    // A count the answer does not carry is left out, not shown as nothing.
    const n = (count, one, many) => (Number.isFinite(count) ? [`${fmt(count)} ${count === 1 ? one : many}`] : []);
    return [
      [...n(v.villagers, 'villager', 'villagers'), ...n(v.golems, 'iron golem', 'iron golems'), ...n(v.cats, 'cat', 'cats')].join(', '),
      [...n(v.beds, 'bed', 'beds'), ...n(v.bells, 'bell', 'bells'), ...n(v.jobSites, 'job site', 'job sites')].join(', '),
    ].filter(Boolean);
  }

  // Why the predicted layer is empty, for the states in which it always is.
  const WHY_NOT = {
    unverified: 'Nothing is predicted yet: the world has recorded too few structures to check its seed against.',
    refuted: 'Nothing is predicted: the structures this world recorded are not where its seed would put them.',
    unknown: 'Nothing is predicted: the world’s seed could not be read.',
  };

  // What the Possible row says of itself when it has nothing else to say:
  // the word alone does not tell a viewer how much to expect of one.
  const WHAT_POSSIBLE = 'Where the seed puts a site in terrain not generated yet. The biome there decides whether one is built.';

  // Why a kind is not predicted while the seed itself is trusted: each
  // kind's rule is checked against the world's own structures of that kind.
  const WHY_NOT_KIND = {
    unverified: 'Not predicted: this world has recorded too few to check the rule by.',
    refuted: 'Not predicted: the ones this world recorded are not where the rule puts them.',
  };

  // The game keeps a record of a village only once it has run it, so a
  // site with none may still hold one. Every other kind is recorded with
  // the chunk, and a finished site without one has none.
  const RECORDED_LATE = new Set(['village']);

  // What is said under a prediction's name and place.
  function standing(p) {
    if (p.candidate) {
      return ['The seed puts a site here. Whether one is built depends on the biome, and this terrain is not generated yet.'];
    }
    if (p.generated && RECORDED_LATE.has(p.kind)) {
      return ['This area is generated and its biome suits one, but the game has no record of one here: it keeps one only for a village a player has been near.'];
    }
    if (p.generated) return ['This area is already generated and the world recorded none here.'];
    return ['Not generated yet: nobody has been here.'];
  }

  // Each row's handle, while the service has structures; empty while it
  // does not.
  const rows = new Map();
  const on = (key) => rows.has(key) && rows.get(key).enabled;

  const fmt = (n) => n.toLocaleString('en-US');
  const count = (n) => (Number.isFinite(n) && n > 0 ? n : 0);

  // Leaflet treats a string given to a tooltip as HTML. Nothing here comes
  // from a player, but an element holding text is the form that cannot be
  // interpreted whatever a later version of the server sends.
  function tip(...lines) {
    const box = document.createElement('div');
    for (const line of lines) {
      const row = document.createElement('div');
      row.textContent = line;
      box.append(row);
    }
    return box;
  }

  // A marker that can be reached with the keyboard, saying where it is to
  // whoever cannot see the tooltip that focus opens.
  function mark(latlng, drawn, label, subject) {
    const marker = L.marker(latlng, { icon: drawn })
      .bindTooltip(label, { direction: 'top', offset: [0, -8], className: 'live-tip' });
    // A click, a tap, or Enter on the focused mark opens everything that
    // is known of it; the tooltip only says what it is.
    if (subject) marker.on('click', () => detail(subject));
    marker.on('add', () => {
      const node = marker.getElement();
      if (!node) return;
      node.setAttribute('aria-label', [...label.children].map((row) => row.textContent).join('. '));
      // A picture that arrived while this was off the map.
      icons.paint(node);
    });
    return marker;
  }

  function icon(kind, sort) {
    const k = KINDS[kind] || UNKNOWN;
    const mark = document.createElement('span');
    mark.style.setProperty('--kind', k.color);
    const letter = document.createElement('b');
    letter.textContent = k.letter;
    mark.append(icons.picture(icons.keyOf('structure', { kind })), letter);
    return L.divIcon({ html: mark, className: `structure ${sort}`, iconSize: [22, 22], iconAnchor: [11, 11] });
  }

  // sort -> kind -> layer group. A filter is a group on or off the map.
  const groups = { recorded: new Map(), predicted: new Map(), candidate: new Map() };
  const groupOf = (sort, kind) => {
    if (!groups[sort].has(kind)) groups[sort].set(kind, L.layerGroup());
    return groups[sort].get(kind);
  };

  const spawnLayer = L.layerGroup();
  // Whether the dimension shown has the world spawn in it.
  let spawned = false;

  let shown = null; // the dimension the layers hold
  // The answer the layers were drawn from, to draw them again when what
  // things are called changes.
  let held = null;
  // Whether the server has looked at this dimension yet. Until it has,
  // there is no count to give and no reason for an empty layer.
  let surveyed = false;
  let fetchedAt = 0;
  let pending = null;
  let available = true;
  let state = 'unknown';
  const none = () => ({ recorded: 0, predicted: 0, candidate: 0 });
  let counts = none();
  let more = none();
  // How many of each kind each layer holds, and how each kind's rule has
  // fared against the world, for the kinds the server said so of.
  let kinds = {};
  // The kinds the world keeps no record of, and which were found by their
  // blocks.
  let foundKinds = new Set();
  let checks = {};
  // And how many of the world's own each rule agreed and disagreed with.
  let rules = {};

  function clear() {
    for (const sort of Object.values(groups)) for (const group of sort.values()) group.clearLayers();
    spawnLayer.clearLayers();
    spawned = false;
    shown = null;
    held = null;
    surveyed = false;
    fetchedAt = 0;
    counts = none();
    more = none();
    kinds = {};
    foundKinds = new Set();
    checks = {};
    rules = {};
  }

  function draw(dimension, data) {
    clear();
    shown = dimension;
    held = data;
    surveyed = true;
    fetchedAt = Date.now();
    state = data.prediction || 'unknown';
    const recorded = (data.recorded || []).filter((s) => s && [s.minX, s.maxX, s.minZ, s.maxZ].every(Number.isFinite));
    const predicted = (data.predicted || []).filter((p) => p && Number.isFinite(p.x) && Number.isFinite(p.z));
    for (const s of recorded) {
      const k = KINDS[s.kind] || UNKNOWN;
      const group = groupOf('recorded', s.kind);
      const label = tip(`${names.structure(s.kind)} · ${found(s) ? (s.partial === true ? 'found by its blocks, in part' : 'found by its blocks') : 'recorded by the world'}`, ...villageLines(s.village).slice(0, 1), DETAILS_HINT);
      // The box is what the world recorded, to the block. A block's far
      // edge is one past its coordinate. One round the blocks a kind was
      // found by is dashed: the structure is there, and its edge is not.
      L.rectangle([[s.minZ, s.minX], [s.maxZ + 1, s.maxX + 1]], {
        color: k.color, weight: 2, fillColor: k.color, fillOpacity: found(s) ? 0.08 : 0.18, dashArray: found(s) ? '5 5' : null, interactive: false,
      }).addTo(group);
      // And a mark that stays the same size, since a box 58 blocks wide is
      // less than a pixel from far out.
      mark([(s.minZ + s.maxZ + 1) / 2, (s.minX + s.maxX + 1) / 2], icon(s.kind, 'recorded'), label, { recorded: s }).addTo(group);
    }
    for (const p of predicted) {
      const sort = p.candidate ? 'candidate' : 'predicted';
      const title = `${names.structure(p.kind)} · ${p.candidate ? 'possible here' : 'predicted from the seed'}`;
      const label = tip(title, `around X ${fmt(p.x)}, Z ${fmt(p.z)}`, DETAILS_HINT);
      // Struck through only where the world has been asked and said no.
      const doubted = p.generated && !RECORDED_LATE.has(p.kind);
      mark([p.z + 0.5, p.x + 0.5], icon(p.kind, `predicted${p.candidate ? ' candidate' : ''}${doubted ? ' doubted' : ''}`), label, { predicted: p })
        .addTo(groupOf(sort, p.kind));
    }
    const spawn = data.spawn;
    if (spawn && Number.isFinite(spawn.x) && Number.isFinite(spawn.z)) {
      spawned = true;
      // A world that has not resolved its spawn height sends none.
      const where = Number.isFinite(spawn.y) ? `X ${fmt(spawn.x)}, Y ${fmt(spawn.y)}, Z ${fmt(spawn.z)}` : `X ${fmt(spawn.x)}, Z ${fmt(spawn.z)}`;
      const drawn = L.divIcon({ html: document.createElement('span'), className: 'structure spawn', iconSize: [22, 22], iconAnchor: [11, 11] });
      mark([spawn.z + 0.5, spawn.x + 0.5], drawn, tip('World spawn', where)).addTo(spawnLayer);
    }
    const possible = predicted.filter((p) => p.candidate).length;
    counts = { recorded: recorded.length, predicted: predicted.length - possible, candidate: possible };
    // What the server left out of the seed's sites is not told apart. It
    // keeps the predicted before the possible, so the possible are what
    // was cut wherever there are any.
    const cut = count(data.predictedMore);
    more = { recorded: count(data.recordedMore), predicted: possible > 0 ? 0 : cut, candidate: possible > 0 ? cut : 0 };
    const tally = (list, sort) => {
      for (const s of list) {
        if (!Object.hasOwn(KINDS, s.kind)) continue;
        kinds[s.kind] = kinds[s.kind] || none();
        kinds[s.kind][sort] += 1;
      }
    };
    tally(recorded, 'recorded');
    foundKinds = new Set(recorded.filter(found).map((s) => s.kind));
    tally(predicted.filter((p) => !p.candidate), 'predicted');
    tally(predicted.filter((p) => p.candidate), 'candidate');
    const sent = data.kinds && typeof data.kinds === 'object' ? data.kinds : {};
    for (const kind of Object.keys(KINDS)) {
      if (Object.hasOwn(sent, kind) && sent[kind] && typeof sent[kind].state === 'string') {
        checks[kind] = sent[kind].state;
        rules[kind] = { agree: count(sent[kind].agree), disagree: count(sent[kind].disagree) };
      }
    }
    apply();
    if (wanted === null) fromLink();
    wantedNow();
  }

  // Puts each group on the map or takes it off, by the filters.
  function apply() {
    for (const [sort, kinds] of Object.entries(groups)) {
      for (const [kind, group] of kinds) {
        const want = on(sort) && kindOn(sort, kind);
        if (want && !map.hasLayer(group)) group.addTo(map);
        if (!want && map.hasLayer(group)) map.removeLayer(group);
      }
    }
    if (on('spawn') && !map.hasLayer(spawnLayer)) spawnLayer.addTo(map);
    if (!on('spawn') && map.hasLayer(spawnLayer)) map.removeLayer(spawnLayer);
    paint();
  }

  // Puts the rows in the panel while the service has structures and
  // takes them out while it does not.
  function panel() {
    if (available === rows.size > 0) return;
    if (!available) {
      for (const row of rows.values()) row.remove();
      rows.clear();
      return;
    }
    ROWS.forEach(([id, label, swatch], at) => {
      // The possible sites are the newest row. A viewer who had turned the
      // predicted ones off has not asked for fainter ones.
      const enabled = id !== 'candidate' || rows.get('predicted').enabled;
      const row = app.layers.register({ group: 'structures', id, label, enabled, swatch,
        // The spawn is shown among the overlays, and kept where it was.
        ...(id === 'spawn' ? { order: 40, section: 'overlays', actions: { zoom: toSpawn } } : { order: (at + 1) * 10, facet: id, actions: { zoom: (kind) => zoomTo(id, kind) } }),
      });
      row.onToggle(apply);
      rows.set(id, row);
    });
  }

  // Takes the map to everything one layer holds, or to one kind of it.
  function zoomTo(sort, kind) {
    const bounds = L.latLngBounds([]);
    for (const [held, group] of groups[sort]) {
      if (kind !== undefined && held !== kind) continue;
      group.eachLayer((layer) => bounds.extend(layer.getBounds ? layer.getBounds() : layer.getLatLng()));
    }
    // No closer than one block to a pixel.
    if (bounds.isValid()) map.fitBounds(bounds, { padding: [40, 40], maxZoom: 0 });
  }

  function toSpawn() {
    const spawn = held && held.spawn;
    if (spawn && Number.isFinite(spawn.x) && Number.isFinite(spawn.z)) app.go(shown, spawn.x + 0.5, spawn.z + 0.5);
  }

  // What one layer is made of, for the panel: each kind it holds, with the
  // game's picture of it and how many. A kind that is asked for is listed
  // among the known ones whether or not any is, so that it can be; and a
  // kind held back from prediction is listed there to say why.
  function kindsOf(sort) {
    const items = [];
    for (const kind of Object.keys(KINDS)) {
      const n = (kinds[kind] || none())[sort];
      const asked = OPT_IN.has(kind);
      const why = sort === 'predicted' && surveyed && state === 'verified' ? WHY_NOT_KIND[checks[kind]] || '' : '';
      if (n === 0 && !(asked && sort === 'recorded') && why === '') continue;
      items.push({
        id: kind,
        label: names.plural(names.structure(kind)),
        picture: icons.keyOf('structure', { kind }),
        swatch: `dot ${kind.split('_').join('-')}`,
        count: surveyed ? n : null,
        detail: sort === 'recorded' && foundKinds.has(kind) ? 'found by blocks' : '',
        note: why || (asked && !choices[sort].shows(kind) && sort === 'recorded' ? ASK : ''),
        off: asked,
        disabled: why !== '' && n === 0,
      });
    }
    return items;
  }

  function paint() {
    panel();
    if (!available) return;
    for (const sort of SORTS) {
      const row = rows.get(sort);
      row.setCount(surveyed ? counts[sort] : null);
      const why = sort === 'predicted' && surveyed ? WHY_NOT[state] : '';
      const what = sort === 'candidate' && surveyed && state === 'verified' ? WHAT_POSSIBLE : '';
      row.setNote(why || (more[sort] > 0 ? `Showing ${fmt(counts[sort])} of ${fmt(counts[sort] + more[sort])}` : what));
      if (row.setItems) row.setItems(kindsOf(sort));
    }
    // Greyed out in a dimension the spawn is not in.
    rows.get('spawn').setAvailable(!surveyed || spawned);
  }

  async function load(dimension) {
    if (pending === dimension) return;
    pending = dimension;
    try {
      const res = await fetch(`api/structures?dimension=${encodeURIComponent(dimension)}`, { cache: 'no-store' });
      if (pending !== dimension) return; // the view moved on while this was out
      if (res.status === 404) {
        // The service is running without structures.
        available = false;
        clear();
        paint();
        unwant('');
        return;
      }
      // Logged out: the map's own check shows the login, and the view is
      // announced again once there is a session.
      if (!res.ok) throw new Error(String(res.status));
      const data = await res.json();
      if (pending !== dimension) return;
      available = true;
      if (data.surveyed) {
        draw(dimension, data);
      } else {
        // Before the first snapshot of this run has been read. Ask again
        // soon rather than in five minutes.
        clear();
        shown = dimension;
        fetchedAt = Date.now() - REFRESH_MS + RETRY_MS;
        paint();
        unwant('The map has not read this world’s structures yet.');
      }
    } catch {
      fetchedAt = Date.now() - REFRESH_MS + RETRY_MS;
      unwant('The structure’s details could not be fetched. Choose it again to try once more.');
    } finally {
      if (pending === dimension) pending = null;
    }
  }

  // Brings the layers in line with the view: the current dimension's
  // structures, and none at all while logged out.
  function sync() {
    const locked = document.body.classList.contains('locked');
    const dimension = app.dimension();
    // The map has left the dimension the one asked for is in.
    if (wanted && (locked || dimension !== wanted.dimension)) unwant('');
    // What a structure holds is asked for as one player, and says what a
    // village thinks of them. It is not kept past their session, for
    // whoever logs in next on this page, and nor is a sheet left open
    // showing it.
    if (locked) {
      forgetDetail();
      if (sheet && view.dialog.open) view.dialog.close();
    }
    if (locked || !dimension || !available) {
      // An answer still on its way belongs to the view that asked for it.
      pending = null;
      clear();
      paint();
      return;
    }
    if (dimension !== shown) {
      clear();
      paint();
      load(dimension);
    } else if (Date.now() - fetchedAt > REFRESH_MS && !document.hidden) {
      load(dimension);
    }
  }

  // --- everything known of one structure --------------------------------
  //
  // A click on a structure opens a sheet over the page that says all the
  // page can honestly say of it: what the server sent, and what the layers
  // already loaded hold inside it. Nothing in it is worked out from
  // anything the page was not given, and every value is set as text.

  const view = {
    dialog: document.getElementById('structure'),
    picture: document.getElementById('structure-picture'),
    title: document.getElementById('structure-title'),
    standing: document.getElementById('structure-standing'),
    body: document.getElementById('structure-body'),
    go: document.getElementById('structure-go'),
    copy: document.getElementById('structure-copy'),
    link: document.getElementById('structure-link'),
    said: document.getElementById('structure-said'),
    close: document.getElementById('structure-close'),
  };
  const sheet = Object.values(view).every(Boolean) && typeof view.dialog.showModal === 'function';

  const CHUNK = 16;
  // A box of more chunks than this is not gone through for slime chunks.
  const MAX_SLIME_CHUNKS = 4096;
  const WHAT = {
    recorded: 'Recorded: the world’s own save says this structure is here, and this is the box it occupies.',
    found: 'Found: the world keeps no record of this kind, but its save holds blocks only this kind is generated with. The box is the box around those blocks; the structure itself reaches further.',
    partial: 'Partly generated, most likely: it was found by fewer blocks than a finished one ever is, so the rest of it is in chunks the world has not generated yet.',
    predicted: 'Predicted: worked out from the world’s seed, not read from the world. Nothing has recorded one here.',
    candidate: 'Possible site: the seed puts a site here, in terrain nobody has generated. The biome there will decide whether anything is built.',
  };
  const COMPASS = ['east', 'south-east', 'south', 'south-west', 'west', 'north-west', 'north', 'north-east'];

  // The one the sheet is about, as { recorded } or { predicted }, with
  // the dimension it is in; null while the sheet is shut.
  let open = null;
  // One asked for before its dimension's answer had come: by a search, or
  // by an address that names it. It is a request to open a sheet now, so
  // it does not outlive the moment: it is dropped if the answer fails, if
  // the map leaves its dimension, and after WANTED_MS whatever happens,
  // or a sheet would open over whatever the viewer had moved on to.
  let wanted = null;
  let wantedTimer = null;
  const WANTED_MS = 8000;
  let biomeAsk = null;
  let saidTimer = null;

  const el = (tag, className, text) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (text !== undefined) node.textContent = text;
    return node;
  };

  // Where a structure is taken to be: the middle of a recorded one's box,
  // as the search gives it, and the block a predicted one is put at.
  const middle = (subject) => {
    const s = subject.recorded;
    if (!s) return { x: subject.predicted.x, z: subject.predicted.z };
    const at = { x: s.minX + Math.floor((s.maxX - s.minX) / 2), z: s.minZ + Math.floor((s.maxZ - s.minZ) / 2) };
    if (Number.isFinite(s.minY) && Number.isFinite(s.maxY)) at.y = s.minY + Math.floor((s.maxY - s.minY) / 2);
    return at;
  };
  const kindOf = (subject) => (subject.recorded || subject.predicted).kind;
  const sortOf = (subject) => (subject.recorded ? 'recorded' : subject.predicted.candidate ? 'candidate' : 'predicted');
  const said = (at) => (Number.isFinite(at.y) ? `${at.x} ${at.y} ${at.z}` : `${at.x} ${at.z}`);
  // How the address names it: its kind, whether the world recorded it, and
  // its middle. Tildes, since a coordinate may begin with a minus.
  const linkOf = (subject) => {
    const at = middle(subject);
    return `structure~${kindOf(subject)}~${subject.recorded ? 'r' : 'p'}~${at.x}~${at.z}`;
  };

  function find(want) {
    if (!held || !want) return null;
    for (const s of held.recorded || []) {
      if (!want.recorded || !s || s.kind !== want.kind || ![s.minX, s.maxX, s.minZ, s.maxZ].every(Number.isFinite)) continue;
      const at = middle({ recorded: s });
      if (at.x === want.x && at.z === want.z) return { recorded: s };
    }
    for (const p of held.predicted || []) {
      if (!want.recorded && p && p.kind === want.kind && p.x === want.x && p.z === want.z) return { predicted: p };
    }
    return null;
  }

  function away(from, to) {
    const dx = to.x - from.x;
    const dz = to.z - from.z;
    const blocks = Math.round(Math.hypot(dx, dz));
    if (blocks === 0) return 'here';
    // South is down the map and east to the right, as an angle from east
    // turning clockwise.
    const way = COMPASS[((Math.round(Math.atan2(dz, dx) / (Math.PI / 4)) % 8) + 8) % 8];
    return `${fmt(blocks)} ${blocks === 1 ? 'block' : 'blocks'} ${way}`;
  }

  // A list of facts: each a label and what is said of it, which is text
  // or an element this script built.
  function facts(heading, rows) {
    const kept = rows.filter((row) => row && row[1] !== null && row[1] !== undefined && row[1] !== '');
    if (kept.length === 0) return [];
    const list = el('dl', 'facts');
    for (const [label, value] of kept) {
      const dd = el('dd');
      if (value instanceof Node) dd.append(value); else dd.textContent = String(value);
      list.append(el('dt', '', label), dd);
    }
    return [el('h3', '', heading), list];
  }

  const tallied = (counts) => [...counts].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([what, n]) => `${what} ${fmt(n)}`).join(', ');

  // Buttons that shut the sheet and open the card about one player or mob.
  function people(list) {
    const row = el('span', 'people');
    for (const who of list) {
      const button = el('button', 'mini', who.what ? `${who.name} (${who.what})` : who.name);
      button.type = 'button';
      button.addEventListener('click', () => {
        view.dialog.close();
        who.open();
      });
      row.append(button);
    }
    return row;
  }

  function slime(box) {
    if (shown !== 'overworld' || !app.isSlimeChunk) return null;
    const x0 = Math.floor(box.minX / CHUNK);
    const x1 = Math.floor(box.maxX / CHUNK);
    const z0 = Math.floor(box.minZ / CHUNK);
    const z1 = Math.floor(box.maxZ / CHUNK);
    const total = (x1 - x0 + 1) * (z1 - z0 + 1);
    if (total > MAX_SLIME_CHUNKS) return null;
    let n = 0;
    for (let cz = z0; cz <= z1; cz++) for (let cx = x0; cx <= x1; cx++) if (app.isSlimeChunk(cx, cz)) n += 1;
    if (total === 1) return n === 1 ? 'Its chunk is a slime chunk' : 'Its chunk is not a slime chunk';
    return `${fmt(n)} of its ${fmt(total)} chunks`;
  }

  function fill(subject) {
    const kind = kindOf(subject);
    const sort = sortOf(subject);
    const at = middle(subject);
    const s = subject.recorded;
    const p = subject.predicted;
    view.picture.replaceChildren(icons.picture(icons.keyOf('structure', { kind })));
    view.title.textContent = names.structure(kind);
    const lines = [s && found(s) ? WHAT.found : WHAT[sort]];
    if (s && found(s) && s.partial === true) lines.push(WHAT.partial);
    if (p) {
      lines.push(...standing(p));
      const rule = rules[kind];
      if (rule) {
        lines.push(`Its rule was checked against this world’s own ${names.plural(names.structure(kind)).toLowerCase()}: ${fmt(rule.agree)} recorded where it puts one, ${fmt(rule.disagree)} not.`);
      }
    }
    view.standing.replaceChildren(...lines.map((line) => el('p', '', line)));

    const out = [];
    const range = (lo, hi) => `${fmt(lo)} to ${fmt(hi)} (${fmt(hi - lo + 1)} ${hi - lo === 0 ? 'block' : 'blocks'})`;
    const chunks = (lo, hi) => (Math.floor(lo / CHUNK) === Math.floor(hi / CHUNK) ? fmt(Math.floor(lo / CHUNK)) : `${fmt(Math.floor(lo / CHUNK))} to ${fmt(Math.floor(hi / CHUNK))}`);
    const height = s && Number.isFinite(s.minY) && Number.isFinite(s.maxY);
    out.push(...facts('Where', [
      ['Dimension', app.label(shown)],
      [s ? 'Centre' : 'Around', Number.isFinite(at.y) ? `X ${fmt(at.x)}, Y ${fmt(at.y)}, Z ${fmt(at.z)}` : `X ${fmt(at.x)}, Z ${fmt(at.z)}`],
      s && ['Blocks X', range(s.minX, s.maxX)],
      height && ['Blocks Y', range(s.minY, s.maxY)],
      s && ['Blocks Z', range(s.minZ, s.maxZ)],
      s ? ['Chunks', `X ${chunks(s.minX, s.maxX)}, Z ${chunks(s.minZ, s.maxZ)}`] : ['Chunk', `${fmt(Math.floor(at.x / CHUNK))}, ${fmt(Math.floor(at.z / CHUNK))}`],
      ['From the middle of the map', away({ x: Math.floor(map.getCenter().lng), z: Math.floor(map.getCenter().lat) }, at)],
      app.inspect && app.inspect.me() ? ['From you', away({ x: Math.floor(app.inspect.me().x), z: Math.floor(app.inspect.me().z) }, at)] : null,
    ]));

    if (s && s.village) {
      const v = s.village;
      const n = (value) => (Number.isFinite(value) ? fmt(value) : null);
      out.push(...facts('What the game counts in it', v.counted ? [
        ['Villagers', n(v.villagers)], ['Iron golems', n(v.golems)], ['Cats', n(v.cats)],
        ['Beds claimed', n(v.beds)], ['Bells claimed', n(v.bells)], ['Job sites claimed', n(v.jobSites)],
      ] : [['Counts', 'Not counted by the game yet: it has a record of this village and has not run it, so its box is a first guess.']]));
    } else if (s && Number.isFinite(s.areas) && s.areas > 0) {
      out.push(...facts('What the world recorded', [['Spawn areas', `${fmt(s.areas)}, joined into this one box`]]));
    } else if (s && found(s)) {
      out.push(...facts('What it was found by', [['Blocks', `${fmt(s.evidence)} ${FOUND_BY[kind] || 'blocks only this kind has'}`]]));
    }
    if (s) {
      // What the save holds in it is asked for, and is put here when it
      // comes: the rest of the sheet does not wait for it.
      const held = el('div', 'held');
      out.push(held);
      askDetail(subject, held);
    } else {
      out.push(el('p', 'note', 'Only a kind and a place can be said of a site the seed gives. The save holds nothing of it to count until the world generates it.'));
    }

    const land = [];
    if (app.biomes) {
      const biome = el('span', '', 'Looking…');
      land.push(['Biome at its middle', biome]);
      askBiome(at, biome);
    }
    const box = s || { minX: at.x, maxX: at.x, minZ: at.z, maxZ: at.z };
    land.push(['Slime chunks', slime(box)]);
    out.push(...facts('The land', land));

    if (s) {
      const kept = app.markers ? app.markers.within(s) : null;
      const live = app.inspect ? app.inspect.within(s) : null;
      const inside = [];
      if (kept) {
        inside.push(['Beds', kept.beds.size > 0 ? tallied(kept.beds) : 'None']);
        inside.push(['Containers', kept.containers.size > 0 ? tallied(kept.containers) : 'None']);
        inside.push(['Named mobs', kept.mobs.length > 0 ? people(kept.mobs) : 'None']);
      }
      if (live) {
        inside.push(['Players here now', live.players.length > 0 ? people(live.players) : 'None']);
        inside.push(['Mobs here now', live.mobs.size > 0 ? tallied(live.mobs) : 'None']);
      }
      const parts = facts('On the map inside its box', inside);
      if (parts.length > 0) {
        out.push(...parts, el('p', 'note', 'Counted from the layers loaded now: beds, containers and named mobs as of the last snapshot, players and mobs as of the last live frame, and only those the map was sent.'));
      }
    }
    view.body.replaceChildren(...out);
  }

  // --- what the save holds in one structure ------------------------------
  //
  // Asked of the server one structure at a time, when its sheet opens. Every
  // value is checked for what it should be and set as text: a name tag is a
  // player's, and a count from a damaged world is whatever it says.

  const FOUND_BY = { trial_chamber: 'trial spawners and vaults', stronghold: 'of its portal room: the silverfish spawner, or the end portal once lit' };
  const LEVELS = ['novice', 'apprentice', 'journeyman', 'expert', 'master'];
  // The block that gives each profession, which is what a job site of it
  // is. The village's record names the profession.
  const WORKSTATIONS = {
    armorer: 'Blast furnace', butcher: 'Smoker', cartographer: 'Cartography table', cleric: 'Brewing stand', farmer: 'Composter',
    fisherman: 'Barrel', fletcher: 'Fletching table', leatherworker: 'Cauldron', librarian: 'Lectern', mason: 'Stonecutter',
    shepherd: 'Loom', toolsmith: 'Smithing table', weaponsmith: 'Grindstone',
  };
  const NOT_RECORDED = 'Not recorded by the game';
  const NONE_SAVED = 'None in the save';
  // How many spawners a sheet gives the place of.
  const MAX_PLACES = 12;
  const DETAIL_FRESH_MS = 60_000;

  const text = (v) => (typeof v === 'string' ? v : '');
  const listOf = (v) => (Array.isArray(v) ? v.filter((item) => item && typeof item === 'object') : []);
  const some = (n, one, many) => `${fmt(n)} ${n === 1 ? one : many}`;

  // A length of time to the unit worth saying it in.
  function about(seconds) {
    if (seconds < 90) return 'under 2 min';
    if (seconds < 2 * 3600) return `${fmt(Math.round(seconds / 60))} min`;
    if (seconds < 2 * 86_400) return `${fmt(Math.round(seconds / 3600))} h`;
    return `${fmt(Math.round(seconds / 86_400))} days`;
  }

  // Counted things, each with its picture where the game has one. In
  // lines where each is a sentence of its own.
  function tally(rows, lines) {
    const list = el('ul', lines ? 'tally lines' : 'tally');
    for (const row of rows) {
      const item = el('li');
      if (row.picture) item.append(icons.picture(row.picture));
      item.append(el('span', '', row.text));
      list.append(item);
    }
    return list;
  }

  const mobPicture = (kind) => (/^[a-z0-9_]{1,64}$/.test(kind) ? `mob/${kind}` : '');

  function standingOf(standing) {
    const state = standing && typeof standing === 'object' ? text(standing.state) : '';
    if (state === 'known' && Number.isFinite(standing.value)) {
      return `${standing.value > 0 ? '+' : ''}${fmt(standing.value)}. The game’s own number for what this village thinks of you: it starts at 0, rises as you trade here and falls when you hurt a villager.`;
    }
    if (state === 'none') return 'None: this village has no record of you.';
    if (state === 'pending') return 'Not yet. The map has seen you in the game only since its last snapshot, and says which record is yours from a snapshot taken after it saw you. Look again after the next one.';
    return 'Not known. The map learns which of the world’s players you are by seeing you in the game, and has not in the last half hour.';
  }

  function villageParts(s, v, standing) {
    const out = [];
    const professions = listOf(v.professions).filter((p) => count(p.count) > 0).map((p) => {
      const name = text(p.profession);
      const levels = Array.isArray(p.levels) ? LEVELS.map((level, i) => [level, count(p.levels[i])]).filter(([, n]) => n > 0) : [];
      const at = levels.length > 0 ? `: ${levels.map(([level, n]) => `${fmt(n)} ${level}`).join(', ')}` : '';
      return { text: name ? `${names.tidy(name)} ${fmt(p.count)}${at}` : `No profession ${fmt(p.count)} (unemployed or nitwit: the record does not say which)` };
    });
    const listed = (n, of) => (Number.isFinite(of) && of !== n ? `${fmt(n)} in the save, of the ${fmt(of)} it lists` : `${fmt(n)} in the save`);
    out.push(...facts('Its villagers, from their own records', [
      ['By profession', professions.length > 0 ? tally(professions, true) : 'No grown villager of it is in the save'],
      ['Babies', count(v.babies) > 0 ? fmt(v.babies) : 'None'],
      count(v.missing) > 0 ? ['Not in the save', `${fmt(v.missing)} it lists, with no record of their own`] : null,
      count(v.notLookedUp) > 0 ? ['Not looked up', `${fmt(v.notLookedUp)} more than the map looks up`] : null,
      ['Iron golems', listed(count(v.golems), s.village.golems)],
      ['Cats', listed(count(v.cats), s.village.cats)],
    ]));
    const sites = listOf(v.jobSites).filter((j) => count(j.count) > 0).map((j) => {
      const name = text(j.profession);
      const block = Object.hasOwn(WORKSTATIONS, name) ? WORKSTATIONS[name] : '';
      return { text: block ? `${block} (${names.tidy(name).toLowerCase()}) ${fmt(j.count)}` : `${names.tidy(name)} ${fmt(j.count)}` };
    });
    out.push(...facts('Its job sites', [['Claimed, by block', sites.length > 0 ? tally(sites) : 'None claimed']]));
    const raid = v.raid && typeof v.raid === 'object' ? v.raid : null;
    const raided = raid
      ? `Wave ${fmt(count(raid.wave))} of ${fmt(count(raid.waves))}, ${some(count(raid.raiders), 'raider', 'raiders')} listed${Number.isFinite(raid.idleSeconds) ? `, last run ${about(raid.idleSeconds)} of game time before the snapshot` : ''}. The game keeps this after a raid is over, so it is how far one got and not that one is on.`
      : 'No raid record';
    out.push(...facts('What the game keeps of it', [
      ['Last run by the game', Number.isFinite(v.idleSeconds) ? `${about(v.idleSeconds)} of game time before the snapshot` : NOT_RECORDED],
      ['Raid', raided],
      ['Your standing', standingOf(standing)],
      ['Players it has met', fmt(count(v.met))],
    ]));
    return out;
  }

  // What is said of one kind that is not said of every kind.
  function kindParts(s, d, mobs, standing) {
    const of = (type) => count((mobs.find((m) => m.kind === type) || {}).count);
    const saved = (n) => (n > 0 ? `${fmt(n)} in the save` : NONE_SAVED);
    const blocks = d.blocks && typeof d.blocks === 'object' ? d.blocks : {};
    const block = (name) => (Object.hasOwn(blocks, name) ? count(blocks[name]) : 0);
    const spawners = listOf(d.spawnerCounts);
    const spawning = (type, trial) => spawners.filter((c) => c.mob === type && Boolean(c.trial) === trial).reduce((n, c) => n + count(c.count), 0);
    switch (s.kind) {
      case 'village':
        return d.village && typeof d.village === 'object' && s.village ? villageParts(s, d.village, standing) : [];
      case 'monument': {
        const elders = Number.isFinite(d.elders) ? count(d.elders) : null;
        const said = elders === null ? NOT_RECORDED
          : elders === 0 ? 'None in the save inside its box. A monument is generated with three.'
            : elders <= 3 ? `${fmt(elders)} of the three a monument is generated with ${elders === 1 ? 'is' : 'are'} in the save inside its box`
              : `${fmt(elders)} in the save`;
        return facts('Its guardians', [['Elder guardians', said], ['Guardians', saved(of('guardian'))]]);
      }
      case 'outpost': {
        const captains = count((mobs.find((m) => m.kind === 'pillager') || {}).captains);
        return [...facts('Its pillagers', [
          ['Pillagers', of('pillager') > 0 ? `${saved(of('pillager'))}${captains > 0 ? `, ${fmt(captains)} of them ${captains === 1 ? 'a captain' : 'captains'}` : ''}` : NONE_SAVED],
          ['Allays', saved(of('allay'))],
          ['Iron golems', saved(of('iron_golem'))],
        ]), el('p', 'note', 'The game saves a mob with the chunk it stood in. Pillagers that had wandered off, or were not there when the chunk was last saved, are not in its box to count.')];
      }
      case 'witch_hut':
        return facts('Its witch', [['Witch', saved(of('witch'))], ['Cat', saved(of('cat'))], ['Cauldron', block('cauldron') > 0 ? 'There' : 'None in the save']]);
      case 'fortress':
        return facts('Its mobs and spawners', [
          ['Blazes', saved(of('blaze'))],
          ['Wither skeletons', saved(of('wither_skeleton'))],
          ['Blaze spawners', spawning('blaze', false) > 0 ? `${fmt(spawning('blaze', false))}, listed below` : 'None in the save: none generated in the part recorded, or broken'],
        ]);
      case 'stronghold': {
        const portal = block('end_portal');
        return facts('Its portal room', [
          ['End portal', portal >= 9 ? 'Lit: its nine portal blocks are in the save' : portal > 0 ? `${some(portal, 'portal block', 'portal blocks')} in the save` : 'Not lit when the world was saved'],
          ['Silverfish spawner', spawning('silverfish', false) > 0 ? 'In the save' : 'None in the save: broken, or never saved'],
        ]);
      }
      case 'trial_chamber': {
        const trials = spawners.filter((c) => c.trial === true && count(c.count) > 0)
          .map((c) => ({ picture: mobPicture(text(c.mob)), text: `${text(c.mob) === 'unknown' ? 'Not set' : names.entity(text(c.mob))} ${fmt(c.count)}` }));
        return facts('Its trials', [
          ['Trial spawners', trials.length > 0 ? tally(trials) : 'None in the save'],
          ['Vaults', `${fmt(block('vault'))}, and ${fmt(block('ominous_vault'))} ominous`],
        ]);
      }
      default:
        return [];
    }
  }

  // Everything the answer holds, as the parts of the sheet it becomes.
  function savedParts(s, data) {
    const d = data && data.detail && typeof data.detail === 'object' ? data.detail : null;
    if (!d) return [el('h3', '', 'In the save inside its box'), el('p', 'note', 'What the save holds here could not be worked out at the last snapshot.')];
    const mobs = listOf(d.mobs).filter((m) => typeof m.kind === 'string' && count(m.count) > 0);
    const out = kindParts(s, d, mobs, data.standing);
    const at = Date.parse(text(data.at));
    const ago = Number.isFinite(at) ? Math.max(0, Math.round((Date.now() - at) / 60_000)) : null;
    const when = ago === null ? '' : ago < 1 ? ', under a minute ago' : ago < 120 ? `, ${fmt(ago)} min ago` : `, ${fmt(Math.round(ago / 60))} h ago`;
    const read = `Read from the world’s save as of the last snapshot${when}.`;
    // Too little of it is known to say where to look for what it holds,
    // and a count of none would say it holds nothing.
    if (d.uncounted === true) {
      out.push(el('p', 'note', `What else it holds is not counted: only this one room of it was found, and the rest could be anywhere round it. ${read}`));
      return out;
    }
    // A kind found by its blocks is counted a stated way past them.
    const reach = count(d.reach);
    const where = reach > 0 ? `within ${fmt(reach)} blocks of what it was found by` : 'inside its box';
    const none = reach > 0 ? `None in the save ${where}` : NONE_SAVED;

    const counted = mobs.map((m) => ({
      picture: mobPicture(m.kind),
      text: `${names.entity(m.kind)} ${fmt(m.count)}${count(m.babies) > 0 ? ` (${fmt(m.babies)} young)` : ''}`,
    }));
    if (count(d.mobKindsMore) > 0) counted.push({ text: `and ${some(d.mobKindsMore, 'more type', 'more types')}` });
    const named = listOf(d.named).filter((m) => text(m.name) !== '').map((m) => {
      const job = text(m.profession) ? `, ${names.tidy(m.profession).toLowerCase()}${Number.isFinite(m.level) && LEVELS[m.level - 1] ? `, ${LEVELS[m.level - 1]}` : ''}` : '';
      return { picture: mobPicture(text(m.kind)), text: `${m.name} (${names.kindOf(text(m.kind), m.baby === true)}${job})` };
    });
    if (count(d.namedMore) > 0) named.push({ text: `and ${fmt(d.namedMore)} more` });

    const spawners = listOf(d.spawnerCounts).filter((c) => count(c.count) > 0).map((c) => ({
      picture: mobPicture(text(c.mob)),
      text: `${text(c.mob) === 'unknown' ? 'Not set' : names.entity(text(c.mob))}${c.trial === true ? ' (trial)' : ''} ${fmt(c.count)}`,
    }));
    const placed = listOf(d.spawners).filter((p) => [p.x, p.y, p.z].every(Number.isFinite));
    const places = placed.slice(0, MAX_PLACES).map((p) => ({
      text: `X ${fmt(p.x)}, Y ${fmt(p.y)}, Z ${fmt(p.z)} · ${text(p.mob) === 'unknown' ? 'not set' : names.entity(text(p.mob))}${p.trial === true ? ' (trial)' : ''}`,
    }));
    const unplaced = placed.length - places.length + count(d.spawnersMore);
    if (unplaced > 0) places.push({ text: `and ${fmt(unplaced)} more` });

    const containers = listOf(d.containers).map((c) => {
      const parts = [
        count(c.unopened) > 0 ? `${fmt(c.unopened)} not yet opened` : '',
        count(c.holding) > 0 ? `${fmt(c.holding)} with something in ${c.holding === 1 ? 'it' : 'them'}` : '',
        count(c.empty) > 0 ? `${fmt(c.empty)} empty` : '',
      ].filter(Boolean);
      const kind = text(c.kind);
      return parts.length > 0 ? { picture: `container/${kind}`, text: `${names.container(kind)}: ${parts.join(', ')}` } : null;
    }).filter(Boolean);
    const unopened = listOf(d.containers).some((c) => count(c.unopened) > 0);
    // A pot and a dispenser hold things too, and are not something a
    // player has or has not opened: each is counted as the block it is.
    const held = d.blocks && typeof d.blocks === 'object' ? d.blocks : {};
    const blocks = (name) => (Object.hasOwn(held, name) ? count(held[name]) : 0);

    out.push(...facts(`In the save ${where}`, [
      ['Mobs', counted.length > 0 ? tally(counted) : none],
      named.length > 0 ? ['Named', tally(named, true)] : null,
      ['Spawners', spawners.length > 0 ? tally(spawners) : none],
      places.length > 0 ? ['Where they are', tally(places, true)] : null,
      ['Containers', containers.length > 0 ? tally(containers, true) : none],
      blocks('unbroken_pot') > 0 ? ['Decorated pots', `${fmt(blocks('unbroken_pot'))} unbroken, as the world generated ${blocks('unbroken_pot') === 1 ? 'it' : 'them'}`] : null,
      blocks('dispenser') > 0 ? ['Dispensers', fmt(blocks('dispenser'))] : null,
      blocks('dropper') > 0 ? ['Droppers', fmt(blocks('dropper'))] : null,
    ]));
    if (unopened) {
      out.push(el('p', 'note', 'Not yet opened: the game fills a generated chest or barrel the first time it is opened, and has not filled these. What a container holds is not read.'));
    }
    out.push(el('p', 'note', `${read} The game saves a mob with its chunk, so one that has since moved, died or despawned is still counted until the next.`));
    return out;
  }

  // The last answer, kept for a moment: the sheet is filled again whenever
  // the names change, and that is not a reason to ask again.
  let detailHeld = null;
  let detailAsk = null;

  function forgetDetail() {
    detailHeld = null;
    if (detailAsk) detailAsk.abort();
    detailAsk = null;
  }

  async function askDetail(subject, into) {
    if (detailAsk) detailAsk.abort();
    detailAsk = null;
    const key = `${shown}~${linkOf(subject)}`;
    const show = (data) => {
      into.replaceChildren(...savedParts(subject.recorded, data));
      icons.paint(into);
    };
    if (detailHeld && detailHeld.key === key && Date.now() - detailHeld.at < DETAIL_FRESH_MS) {
      show(detailHeld.data);
      return;
    }
    const mine = new AbortController();
    detailAsk = mine;
    into.replaceChildren(el('h3', '', 'In the save inside its box'), el('p', 'note', 'Reading what the save holds here…'));
    const at = middle(subject);
    let said = 'What the save holds here could not be fetched. Open it again to try once more.';
    try {
      const res = await fetch(`api/structures/detail?dimension=${encodeURIComponent(shown)}&kind=${encodeURIComponent(subject.recorded.kind)}&x=${at.x}&z=${at.z}`, { cache: 'no-store', signal: mine.signal });
      if (detailAsk !== mine) return;
      if (res.ok) {
        const data = await res.json();
        if (detailAsk !== mine) return;
        detailHeld = { key, at: Date.now(), data };
        show(data);
        return;
      }
      // A server from before this was asked for, or a structure that has
      // gone since the list was fetched.
      if (res.status === 404) said = 'The map has nothing more on this one: it is not among the structures of the last snapshot.';
    } catch {
      if (detailAsk !== mine) return;
    }
    into.replaceChildren(el('h3', '', 'In the save inside its box'), el('p', 'note', said));
  }

  // The biome is the one other thing asked of the server for the sheet,
  // and the answer is for the sheet that asked.
  async function askBiome(at, into) {
    if (biomeAsk) biomeAsk.abort();
    const mine = new AbortController();
    biomeAsk = mine;
    let text = 'Not known';
    try {
      const res = await fetch(`api/biomes/at?dimension=${encodeURIComponent(shown)}&x=${at.x}&z=${at.z}`, { cache: 'no-store', signal: mine.signal });
      if (res.ok) {
        const data = await res.json();
        const biome = data && data.generated && data.biome ? data.biome : null;
        text = biome ? (typeof biome.label === 'string' && biome.label) || names.tidy(biome.name) : 'Not generated here';
      }
    } catch {
      if (biomeAsk !== mine) return;
    }
    if (biomeAsk === mine) into.textContent = text;
  }

  function note(text) {
    clearTimeout(saidTimer);
    view.said.textContent = text;
    if (text !== '') saidTimer = setTimeout(() => note(''), 4000);
  }

  function detail(subject) {
    if (!sheet || shown === null) return;
    open = subject;
    note('');
    fill(subject);
    if (app.link) app.link.set(linkOf(subject));
    if (!view.dialog.open) view.dialog.showModal();
    view.dialog.scrollTop = 0;
  }

  // Opens one that was asked for by what it is and where, once the
  // dimension's structures are here to find it among.
  function want(next) {
    clearTimeout(wantedTimer);
    wanted = next;
    wantedTimer = setTimeout(() => unwant('The structure’s details could not be fetched in time. Choose it again to try once more.'), WANTED_MS);
    wantedNow();
  }

  // Lets go of the one asked for, saying why if it was the viewer who
  // asked and there is something to say.
  function unwant(why) {
    clearTimeout(wantedTimer);
    const missed = wanted;
    wanted = null;
    if (missed && missed.said && why && app.tell) app.tell(why);
  }

  function wantedNow() {
    if (!wanted || shown !== wanted.dimension || !held) return;
    const found = find(wanted);
    if (found) {
      unwant('');
      detail(found);
    } else {
      unwant('That structure is not among the ones the map has now.');
    }
  }

  function fromLink() {
    if (!sheet || !app.link) return;
    const m = /^structure~([a-z0-9_]{1,40})~([rp])~(-?\d{1,9})~(-?\d{1,9})$/.exec(app.link.get());
    if (!m) return;
    if (open && linkOf(open) === m[0]) return;
    want({ kind: m[1], recorded: m[2] === 'r', x: Number(m[3]), z: Number(m[4]), dimension: app.dimension() });
  }

  async function copy(text, done) {
    let result = done;
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      result = `Copy it from here: ${text}`;
    }
    note(result);
  }

  if (sheet) {
    view.close.addEventListener('click', () => view.dialog.close());
    // A click on the backdrop is a click on the dialog itself, outside
    // everything in it.
    view.dialog.addEventListener('click', (e) => {
      if (e.target === view.dialog) view.dialog.close();
    });
    view.dialog.addEventListener('close', () => {
      open = null;
      if (biomeAsk) biomeAsk.abort();
      biomeAsk = null;
      if (detailAsk) detailAsk.abort();
      detailAsk = null;
      if (app.link && app.link.get().startsWith('structure~')) app.link.set('');
    });
    view.go.addEventListener('click', () => {
      if (!open) return;
      const subject = open;
      view.dialog.close();
      const s = subject.recorded;
      if (s) map.fitBounds([[s.minZ, s.minX], [s.maxZ + 1, s.maxX + 1]], { padding: [60, 60], maxZoom: 2 });
      else app.go(shown, subject.predicted.x + 0.5, subject.predicted.z + 0.5);
    });
    view.copy.addEventListener('click', () => {
      if (open) copy(said(middle(open)), 'Coordinates copied');
    });
    view.link.addEventListener('click', () => {
      if (open && app.link) copy(app.link.href(), 'Link copied');
    });
    document.addEventListener('mcmap:link', fromLink);
  }

  // For the search: one chosen there is shown in full once the map is on
  // its dimension.
  for (const choice of Object.values(choices)) choice.onChange(apply);

  app.structures = {
    // Whether the viewer has a kind's known structures on the map.
    shows: (kind) => on('recorded') && kindOn('recorded', kind),
    show(asked) {
      if (!sheet || !asked || typeof asked.kind !== 'string' || !Number.isFinite(asked.x) || !Number.isFinite(asked.z)) return;
      want({ kind: asked.kind, recorded: asked.recorded === true, x: asked.x, z: asked.z, dimension: asked.dimension, said: true });
    },
  };

  paint();
  // Every tooltip was written with the names there were then.
  document.addEventListener('mcmap:names', () => {
    if (shown === null || held === null) {
      paint();
      return;
    }
    const at = fetchedAt;
    const was = open ? { kind: kindOf(open), recorded: Boolean(open.recorded), ...middle(open) } : null;
    draw(shown, held);
    fetchedAt = at;
    // The sheet is about an entry of the answer that was just drawn again.
    const again = was && view.dialog.open ? find(was) : null;
    if (again) {
      open = again;
      fill(again);
    }
  });
  // A picture that arrived while the sheet was open.
  document.addEventListener('mcmap:pictures', () => {
    if (sheet && view.dialog.open) icons.paint(view.body);
  });
  document.addEventListener('mcmap:view', sync);
  document.addEventListener('visibilitychange', sync);
  setInterval(sync, RETRY_MS);
  sync();
})();
