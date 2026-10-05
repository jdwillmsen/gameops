'use strict';

// Structures, as two layers that are never drawn alike. A recorded one is a
// fact: the world's own save says it is there, and it is drawn solid, with
// the box it occupies. A predicted one is a calculation from the seed for
// chunks nobody has generated yet, and is drawn hollow and dashed. The
// server keeps the seed and only predicts once the calculation has been
// seen to agree with what the world recorded.
(() => {
  const app = window.mcmap;
  if (!app) return;
  const { map } = app;

  const SETTINGS_KEY = 'mcmap.structures';
  // Structures change only when chunks are generated, and the server looks
  // once per snapshot; asking more often than this finds nothing new.
  const REFRESH_MS = 5 * 60_000;
  const RETRY_MS = 30_000;

  const KINDS = {
    fortress: { label: 'Fortress', letter: 'F', color: '#e5534b' },
    monument: { label: 'Monument', letter: 'M', color: '#5cc8f0' },
    outpost: { label: 'Outpost', letter: 'O', color: '#d9a441' },
    witch_hut: { label: 'Witch hut', letter: 'H', color: '#b48ce0' },
  };
  const UNKNOWN = { label: 'Structure', letter: '?', color: '#9aa3ad' };

  // Why the predicted layer is empty, for the states in which it always is.
  const WHY_NOT = {
    unverified: 'Nothing is predicted yet: the world has recorded too few structures to check its seed against.',
    refuted: 'Nothing is predicted: the structures this world recorded are not where its seed would put them.',
    unknown: 'Nothing is predicted: the world’s seed could not be read.',
  };

  const el = { filters: document.getElementById('structure-filters') };
  if (!el.filters) return;
  const chips = new Map([...el.filters.querySelectorAll('button[data-structures]')].map((b) => [b.dataset.structures, b]));

  const settings = { on: true, recorded: true, predicted: true };
  for (const kind of Object.keys(KINDS)) settings[kind] = true;
  try {
    const saved = JSON.parse(localStorage.getItem(SETTINGS_KEY) || '{}');
    for (const key of Object.keys(settings)) if (typeof saved[key] === 'boolean') settings[key] = saved[key];
  } catch { /* a browser that refuses storage still gets the defaults */ }

  const fmt = (n) => n.toLocaleString('en-US');

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
  function mark(latlng, kind, sort, label) {
    const marker = L.marker(latlng, { icon: icon(kind, sort) })
      .bindTooltip(label, { direction: 'top', offset: [0, -8], className: 'live-tip' });
    marker.on('add', () => {
      const node = marker.getElement();
      if (node) node.setAttribute('aria-label', [...label.children].map((row) => row.textContent).join('. '));
    });
    return marker;
  }

  function icon(kind, sort) {
    const k = KINDS[kind] || UNKNOWN;
    const mark = document.createElement('span');
    mark.textContent = k.letter;
    mark.style.setProperty('--kind', k.color);
    return L.divIcon({ html: mark, className: `structure ${sort}`, iconSize: [18, 18], iconAnchor: [9, 9] });
  }

  // sort -> kind -> layer group. A filter is a group on or off the map.
  const groups = { recorded: new Map(), predicted: new Map() };
  const groupOf = (sort, kind) => {
    if (!groups[sort].has(kind)) groups[sort].set(kind, L.layerGroup());
    return groups[sort].get(kind);
  };

  let shown = null; // the dimension the layers hold
  let fetchedAt = 0;
  let pending = null;
  let available = true;
  let state = 'unknown';
  let counts = { recorded: 0, predicted: 0 };
  let more = { recorded: 0, predicted: 0 };

  function clear() {
    for (const sort of Object.values(groups)) for (const group of sort.values()) group.clearLayers();
    shown = null;
    fetchedAt = 0;
    counts = { recorded: 0, predicted: 0 };
    more = { recorded: 0, predicted: 0 };
  }

  function draw(dimension, data) {
    clear();
    shown = dimension;
    fetchedAt = Date.now();
    state = data.prediction || 'unknown';
    for (const s of data.recorded || []) {
      const k = KINDS[s.kind] || UNKNOWN;
      const group = groupOf('recorded', s.kind);
      const label = tip(
        `${k.label} · recorded by the world`,
        `X ${fmt(s.minX)} to ${fmt(s.maxX)}, Z ${fmt(s.minZ)} to ${fmt(s.maxZ)}`,
        `Y ${fmt(s.minY)} to ${fmt(s.maxY)}`,
      );
      // The box is what the world recorded, to the block. A block's far
      // edge is one past its coordinate.
      L.rectangle([[s.minZ, s.minX], [s.maxZ + 1, s.maxX + 1]], {
        color: k.color, weight: 2, fillColor: k.color, fillOpacity: 0.18, interactive: false,
      }).addTo(group);
      // And a mark that stays the same size, since a box 58 blocks wide is
      // less than a pixel from far out.
      mark([(s.minZ + s.maxZ + 1) / 2, (s.minX + s.maxX + 1) / 2], s.kind, 'recorded', label).addTo(group);
    }
    for (const p of data.predicted || []) {
      const k = KINDS[p.kind] || UNKNOWN;
      const label = p.generated
        ? tip(`${k.label} · predicted from the seed`, `around X ${fmt(p.x)}, Z ${fmt(p.z)}`,
          'This area is already generated and the world recorded none here.')
        : tip(`${k.label} · predicted from the seed`, `around X ${fmt(p.x)}, Z ${fmt(p.z)}`,
          'Not generated yet: nobody has been here.');
      mark([p.z + 0.5, p.x + 0.5], p.kind, p.generated ? 'predicted doubted' : 'predicted', label)
        .addTo(groupOf('predicted', p.kind));
    }
    counts = { recorded: (data.recorded || []).length, predicted: (data.predicted || []).length };
    more = { recorded: data.recordedMore || 0, predicted: data.predictedMore || 0 };
    apply();
  }

  // Puts each group on the map or takes it off, by the filters.
  function apply() {
    for (const [sort, kinds] of Object.entries(groups)) {
      for (const [kind, group] of kinds) {
        const known = Object.hasOwn(settings, kind);
        const want = settings.on && settings[sort] && (!known || settings[kind]);
        if (want && !map.hasLayer(group)) group.addTo(map);
        if (!want && map.hasLayer(group)) map.removeLayer(group);
      }
    }
    paint();
  }

  function paint() {
    el.filters.hidden = !available;
    for (const [key, chip] of chips) {
      chip.setAttribute('aria-pressed', String(settings[key]));
      if (key !== 'on') chip.disabled = !settings.on;
    }
    for (const sort of ['recorded', 'predicted']) {
      const chip = chips.get(sort);
      if (!chip) continue;
      const shownCount = shown ? fmt(counts[sort]) + (more[sort] > 0 ? '+' : '') : '';
      chip.querySelector('.count').textContent = shownCount;
      chip.title = more[sort] > 0 ? `Showing ${fmt(counts[sort])} of ${fmt(counts[sort] + more[sort])}` : '';
    }
    const predicted = chips.get('predicted');
    if (predicted && shown && WHY_NOT[state]) predicted.title = WHY_NOT[state];
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

  function save() {
    try { localStorage.setItem(SETTINGS_KEY, JSON.stringify(settings)); } catch { /* not kept, still applied */ }
  }

  for (const [key, chip] of chips) {
    chip.addEventListener('click', () => {
      settings[key] = !settings[key];
      save();
      apply();
    });
  }

  paint();
  document.addEventListener('mcmap:view', sync);
  document.addEventListener('visibilitychange', sync);
  setInterval(sync, RETRY_MS);
  sync();
})();
