'use strict';

// Structures, as two layers that are never drawn alike. A recorded one is a
// fact: the world's own save says it is there, and it is drawn solid, with
// the box it occupies. A predicted one is a calculation from the seed for
// chunks nobody has generated yet, and is drawn hollow and dashed. The
// server keeps the seed and only predicts once the calculation has been
// seen to agree with what the world recorded.
//
// The world spawn comes with the overworld's structures and has a row of
// its own: it is neither recorded as a structure nor predicted.
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
  };
  // The rows in the panel: the two layers, then a filter per kind that
  // applies to both. A kind's row is named by the game's word for it once
  // that is known.
  const SORTS = ['recorded', 'predicted'];
  const ROWS = [
    ['recorded', 'Known', 'key recorded'],
    ['predicted', 'Predicted', 'key predicted'],
    ['fortress', 'Fortresses', 'dot fortress'],
    ['monument', 'Monuments', 'dot monument'],
    ['outpost', 'Outposts', 'dot outpost'],
    ['witch_hut', 'Witch huts', 'dot witch-hut'],
    ['village', 'Villages', 'dot village'],
    ['spawn', 'World spawn', 'key spawn'],
  ];
  const UNKNOWN = { letter: '?', color: '#9aa3ad' };

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
  function mark(latlng, drawn, label) {
    const marker = L.marker(latlng, { icon: drawn })
      .bindTooltip(label, { direction: 'top', offset: [0, -8], className: 'live-tip' });
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
  const groups = { recorded: new Map(), predicted: new Map() };
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
  let counts = { recorded: 0, predicted: 0 };
  let more = { recorded: 0, predicted: 0 };
  // How many of each kind the two layers hold between them.
  let kinds = {};

  function clear() {
    for (const sort of Object.values(groups)) for (const group of sort.values()) group.clearLayers();
    spawnLayer.clearLayers();
    spawned = false;
    shown = null;
    held = null;
    surveyed = false;
    fetchedAt = 0;
    counts = { recorded: 0, predicted: 0 };
    more = { recorded: 0, predicted: 0 };
    kinds = {};
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
      const label = tip(
        `${names.structure(s.kind)} · recorded by the world`,
        `X ${fmt(s.minX)} to ${fmt(s.maxX)}, Z ${fmt(s.minZ)} to ${fmt(s.maxZ)}`,
        ...(Number.isFinite(s.minY) && Number.isFinite(s.maxY) ? [`Y ${fmt(s.minY)} to ${fmt(s.maxY)}`] : []),
        ...villageLines(s.village),
      );
      // The box is what the world recorded, to the block. A block's far
      // edge is one past its coordinate.
      L.rectangle([[s.minZ, s.minX], [s.maxZ + 1, s.maxX + 1]], {
        color: k.color, weight: 2, fillColor: k.color, fillOpacity: 0.18, interactive: false,
      }).addTo(group);
      // And a mark that stays the same size, since a box 58 blocks wide is
      // less than a pixel from far out.
      mark([(s.minZ + s.maxZ + 1) / 2, (s.minX + s.maxX + 1) / 2], icon(s.kind, 'recorded'), label).addTo(group);
    }
    for (const p of predicted) {
      const title = `${names.structure(p.kind)} · predicted from the seed`;
      const label = p.generated
        ? tip(title, `around X ${fmt(p.x)}, Z ${fmt(p.z)}`,
          'This area is already generated and the world recorded none here.')
        : tip(title, `around X ${fmt(p.x)}, Z ${fmt(p.z)}`,
          'Not generated yet: nobody has been here.');
      mark([p.z + 0.5, p.x + 0.5], icon(p.kind, p.generated ? 'predicted doubted' : 'predicted'), label)
        .addTo(groupOf('predicted', p.kind));
    }
    const spawn = data.spawn;
    if (spawn && Number.isFinite(spawn.x) && Number.isFinite(spawn.z)) {
      spawned = true;
      // A world that has not resolved its spawn height sends none.
      const where = Number.isFinite(spawn.y) ? `X ${fmt(spawn.x)}, Y ${fmt(spawn.y)}, Z ${fmt(spawn.z)}` : `X ${fmt(spawn.x)}, Z ${fmt(spawn.z)}`;
      const drawn = L.divIcon({ html: document.createElement('span'), className: 'structure spawn', iconSize: [22, 22], iconAnchor: [11, 11] });
      mark([spawn.z + 0.5, spawn.x + 0.5], drawn, tip('World spawn', where)).addTo(spawnLayer);
    }
    counts = { recorded: recorded.length, predicted: predicted.length };
    more = { recorded: count(data.recordedMore), predicted: count(data.predictedMore) };
    for (const s of [...recorded, ...predicted]) {
      if (Object.hasOwn(KINDS, s.kind)) kinds[s.kind] = (kinds[s.kind] || 0) + 1;
    }
    apply();
  }

  // Puts each group on the map or takes it off, by the filters.
  function apply() {
    for (const [sort, kinds] of Object.entries(groups)) {
      for (const [kind, group] of kinds) {
        // A kind with no row of its own has no filter to be hidden by.
        const want = on(sort) && (!Object.hasOwn(KINDS, kind) || on(kind));
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
      const picture = Object.hasOwn(KINDS, id) ? icons.picture(icons.keyOf('structure', { kind: id })) : null;
      const row = app.layers.register({ group: 'structures', id, label, order: (at + 1) * 10, swatch, picture });
      row.onToggle(apply);
      rows.set(id, row);
    });
  }

  function paint() {
    panel();
    if (!available) return;
    for (const sort of SORTS) {
      const row = rows.get(sort);
      row.setCount(surveyed ? counts[sort] : null);
      const why = sort === 'predicted' && surveyed ? WHY_NOT[state] : '';
      row.setNote(why || (more[sort] > 0 ? `Showing ${fmt(counts[sort])} of ${fmt(counts[sort] + more[sort])}` : ''));
    }
    // With neither layer on there is nothing for a kind to filter.
    const filtering = SORTS.some(on);
    for (const kind of Object.keys(KINDS)) {
      rows.get(kind).setLabel(names.plural(names.structure(kind)));
      rows.get(kind).setCount(surveyed ? kinds[kind] || 0 : null);
      rows.get(kind).setAvailable(filtering);
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
      }
    } catch {
      fetchedAt = Date.now() - REFRESH_MS + RETRY_MS;
    } finally {
      if (pending === dimension) pending = null;
    }
  }

  // Brings the layers in line with the view: the current dimension's
  // structures, and none at all while logged out.
  function sync() {
    const locked = document.body.classList.contains('locked');
    const dimension = app.dimension();
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

  paint();
  // Every tooltip was written with the names there were then.
  document.addEventListener('mcmap:names', () => {
    if (shown === null || held === null) {
      paint();
      return;
    }
    const at = fetchedAt;
    draw(shown, held);
    fetchedAt = at;
  });
  document.addEventListener('mcmap:view', sync);
  document.addEventListener('visibilitychange', sync);
  setInterval(sync, RETRY_MS);
  sync();
})();
