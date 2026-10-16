'use strict';

// Everything the map knows of one player, in a panel beside the map: what
// the world save holds (vitals, what they carry, where they died and
// sleep) and what the trails saw (how far they went, how long they were
// online) and what the pack has counted since the map began counting. The
// save is rewritten only when the world saves and when the player logs out,
// so that half says when it is from, and health and level are replaced by
// the live ones while the player is online. The panel opens from
// the player's card and does not take the page: the map stays alive under
// it, and the card stays on the map.
(() => {
  const app = window.mcmap;
  // The page and its scripts are cached apart for a few minutes, so just
  // after a release this can meet a page that has no panel yet.
  if (!app || !app.icons || !app.names) return;
  const { icons, names } = app;

  const view = {
    dialog: document.getElementById('player'),
    name: document.getElementById('player-title'),
    sub: document.getElementById('player-sub'),
    body: document.getElementById('player-body'),
    said: document.getElementById('player-said'),
    close: document.getElementById('player-close'),
  };
  if (!view.dialog || !view.body || !view.close || typeof view.dialog.show !== 'function') return;

  // The save changes with a snapshot, every few minutes; the trails every
  // second. A minute keeps the trail figures current without asking more
  // often than the save could have changed.
  const REFRESH_MS = 60_000;
  // While the player is online the vitals are a second old at most on the
  // server, so the panel asks as often as it is worth drawing.
  const LIVE_REFRESH_MS = 5_000;
  const LIST_ROWS = 10;
  const MAX_AIR = 300;
  const TICKS_PER_SECOND = 20;
  const HOTBAR = 9;
  const INVENTORY = 36;
  const ENDER = 27;
  // The pictures are 16 and 9 pixels a side and are drawn at twice that, so
  // that every pixel of them is two on the screen and none is a pixel and a half.
  const SLOT_ICON = 32;
  const MARK_ICON = 18;
  // More marks than this is a health or hunger that is not the game's: a bar
  // says it better.
  const MAX_MARKS = 20;
  // What fills the mark of each, by whole and by half, over the same container.
  const MARKS = {
    health: ['ui/heart_container', 'ui/heart', 'ui/heart_half'],
    hunger: ['ui/hunger_container', 'ui/hunger', 'ui/hunger_half'],
    absorption: ['ui/heart_container', 'ui/absorption', 'ui/absorption_half'],
  };
  const SHULKER = 27;
  // How many containers deep the panel opens, which is as deep as the map
  // reads them; a container past it is shown as the item it is.
  const MAX_OPEN = 3;
  const ARMOUR = ['Head', 'Chest', 'Legs', 'Feet'];
  // The sixteen dyes, in the game's order and the spelling its ids use.
  const COLOURS = ['white', 'orange', 'magenta', 'light_blue', 'yellow', 'lime', 'pink', 'gray', 'light_gray', 'cyan', 'purple', 'blue', 'brown', 'green', 'red', 'black'];
  const ROMAN = ['', 'I', 'II', 'III', 'IV', 'V', 'VI', 'VII', 'VIII', 'IX', 'X'];
  const GAME_MODES = { survival: 'Survival', creative: 'Creative', adventure: 'Adventure', spectator: 'Spectator', default: 'The world’s default' };

  let subject = null;
  let ask = null;
  let timer = null;
  let said = null;
  // The last answer, drawn again when the names change.
  let last = null;
  // The picture of it that was drawn, so that an answer that has not
  // changed does not rebuild what the viewer is holding focus in.
  let drawn = '';
  // The live note of what is drawn, whose age is the one part of an online
  // player's answer that changes every time it is asked.
  let liveLine = null;
  // The slot whose details are being read, as its grid and place in it.
  let pickedPos = '';
  // The container being looked into: the grid it is in and the slot of each
  // container on the way down. It is kept by slot so that a fresh answer
  // finds the same container again.
  let inside = null;
  // The quick refresh, which only runs for a player who is online.
  let liveTimer = null;

  const fmt = (n) => Math.round(n).toLocaleString('en-US');
  const one = (n) => (Math.round(n * 10) / 10).toLocaleString('en-US', { maximumFractionDigits: 1 });
  const text = (v) => (typeof v === 'string' ? v : '');
  const num = (v) => (Number.isFinite(v) ? v : null);
  const listOf = (v) => (Array.isArray(v) ? v.filter((item) => item && typeof item === 'object') : []);
  const labelOf = (dimension) => (app.label ? app.label(dimension) : dimension);

  const el = (tag, className, content) => {
    const node = document.createElement(tag);
    if (className) node.className = className;
    if (content !== undefined) node.textContent = content;
    return node;
  };

  function button(label, onClick, className = 'mini') {
    const b = el('button', className, label);
    b.type = 'button';
    b.addEventListener('click', onClick);
    return b;
  }

  function span(seconds) {
    const s = Math.max(0, Math.round(seconds));
    if (s < 60) return `${s} s`;
    if (s < 3600) return `${Math.round(s / 60)} min`;
    const h = Math.floor(s / 3600);
    const m = Math.round((s % 3600) / 60);
    return m === 60 ? `${h + 1} h` : `${h} h${m > 0 ? ` ${m} min` : ''}`;
  }

  function ago(at) {
    const seconds = (Date.now() - at) / 1000;
    return seconds < 90 ? 'under 2 min ago' : `${span(seconds)} ago`;
  }

  // A heading and what is under it, left out when there is nothing under.
  function facts(rows) {
    const kept = rows.filter((row) => row && row[1] !== null && row[1] !== undefined && row[1] !== '');
    if (kept.length === 0) return null;
    const list = el('dl', 'facts');
    for (const [label, value] of kept) {
      const dd = el('dd');
      if (value instanceof Node) dd.append(value); else dd.textContent = String(value);
      list.append(el('dt', '', label), dd);
    }
    return list;
  }

  function meter(fraction, label, what) {
    const bar = el('span', 'meter');
    const fill = el('span', 'fill');
    const f = Math.min(1, Math.max(0, Number.isFinite(fraction) ? fraction : 0));
    fill.style.width = `${(f * 100).toFixed(1)}%`;
    bar.append(fill);
    bar.setAttribute('role', 'meter');
    bar.setAttribute('aria-label', what);
    bar.setAttribute('aria-valuemin', '0');
    bar.setAttribute('aria-valuemax', '1');
    bar.setAttribute('aria-valuenow', String(f));
    bar.setAttribute('aria-valuetext', label);
    const row = el('span', 'gauge');
    row.append(bar, el('span', '', label));
    return row;
  }

  const gauge = (g, what, say) => (g && Number.isFinite(g.current) && Number.isFinite(g.max) && g.max > 0 ? meter(g.current / g.max, say(g), what) : null);

  // A row of the game's marks for a gauge counted in halves: a container for
  // each pair of points, filled whole, by half or not at all. It is a picture
  // of what the words beside it say, and the page leaves it out for them.
  function marks(kind, current, max) {
    const [container, whole, half] = MARKS[kind];
    const count = Math.ceil(max / 2);
    if (!(count >= 1 && count <= MAX_MARKS)) return null;
    const row = el('span', 'marks');
    row.setAttribute('aria-hidden', 'true');
    for (let i = 0; i < count; i++) {
      const left = current - i * 2;
      const mark = el('span', 'mark');
      mark.append(icons.picture(container, { side: MARK_ICON }));
      if (left > 0) mark.append(icons.picture(left >= 2 ? whole : half, { side: MARK_ICON }));
      row.append(mark);
    }
    return row;
  }

  // A gauge drawn as marks where the server has them, and as a bar where it
  // has not: the bar goes when the first mark is drawn.
  function marked(row, kind, g) {
    if (!row) return row;
    const drawn = marks(kind, g.current, kind === 'absorption' ? g.current : g.max);
    if (drawn) row.prepend(drawn);
    return row;
  }

  // --- items -------------------------------------------------------------

  // The picture the map already has for what an item is, where it has one:
  // containers, shulker boxes, beds and spawn eggs. Every other item has the
  // picture of its own texture if the server made one, and is its initials
  // where it did not.
  function pictureOf(name) {
    if (!/^[a-z0-9_]{1,64}$/.test(name)) return '';
    if (name === 'chest' || name === 'trapped_chest' || name === 'barrel') return icons.keyOf('container', { kind: name === 'trapped_chest' ? 'chest' : name, trapped: name === 'trapped_chest' });
    if (name === 'shulker_box') return icons.keyOf('container', { kind: 'shulker' });
    // Only a colour the map has a picture of: a bed of any other name is an
    // item like the rest, and not a bed of a colour that is not there.
    const shulker = /^(.+)_shulker_box$/.exec(name);
    if (shulker && (shulker[1] === 'undyed' || COLOURS.includes(shulker[1]))) return icons.keyOf('container', { kind: 'shulker', colour: shulker[1] });
    const bed = /^(.+)_bed$/.exec(name);
    if (bed && COLOURS.includes(bed[1])) return icons.keyOf('bed', { colour: bed[1] });
    const egg = /^(.+)_spawn_egg$/.exec(name);
    return egg ? icons.keyOf('mob', { kind: egg[1] }) : icons.keyOf('item', { kind: name });
  }

  function initials(label) {
    const words = label.split(/\s+/).filter(Boolean);
    return (words.length > 1 ? words[0][0] + words[1][0] : label.slice(0, 2)).toUpperCase();
  }

  const enchantment = (e) => `${text(e.name) || `Enchantment ${e.id}`} ${ROMAN[e.level] || e.level}`;

  function durability(item) {
    const max = num(item.maxDamage);
    const used = num(item.damage);
    if (max !== null && max > 0 && used !== null) return { left: Math.max(0, max - used), max };
    return null;
  }

  // What an item is, in a sentence, for a tooltip and for the line under a
  // grid that a touch screen reads it from.
  function describe(item) {
    const kind = names.tidy(item.name);
    const parts = [item.customName ? `${item.customName} (${kind})` : kind];
    if (item.count > 1) parts[0] += ` × ${fmt(item.count)}`;
    const e = listOf(item.enchantments);
    if (e.length > 0) parts.push(e.map(enchantment).join(', '));
    const d = durability(item);
    if (d) parts.push(`${fmt(d.left)} of ${fmt(d.max)} uses left`);
    else if (num(item.damage) > 0) parts.push(`${fmt(item.damage)} damage taken`);
    return parts.join(' · ');
  }

  // One stack in a box: its picture or its initials, how many, and how worn.
  function slot(item, pos, onPick) {
    const box = el('button', 'slot');
    box.type = 'button';
    box.dataset.pos = pos;
    if (!item) {
      box.className = 'slot empty';
      box.disabled = true;
      box.setAttribute('aria-label', 'Empty');
      return box;
    }
    const sentence = describe(item);
    box.title = sentence;
    box.setAttribute('aria-label', sentence);
    const key = pictureOf(item.name);
    const enchanted = listOf(item.enchantments).length > 0;
    if (key) box.append(icons.picture(key, { side: SLOT_ICON, glint: enchanted }));
    box.append(el('span', 'abbr', initials(item.customName || names.tidy(item.name))));
    if (item.count > 1) box.append(el('span', 'count', fmt(item.count)));
    if (enchanted) box.classList.add('enchanted');
    const d = durability(item);
    if (d && d.left < d.max) {
      const bar = el('span', 'wear');
      const fill = el('span', 'fill');
      fill.style.width = `${((d.left / d.max) * 100).toFixed(1)}%`;
      bar.append(fill);
      box.append(bar);
    }
    box.addEventListener('click', () => onPick(sentence, pos));
    return box;
  }

  // The line under slots that says what the one chosen holds, and what a slot
  // does when it is chosen.
  function reading() {
    const line = el('p', 'note slot-said', 'Choose an item for its details.');
    line.setAttribute('role', 'status');
    const pick = (sentence, pos) => {
      pickedPos = pos;
      line.textContent = sentence;
    };
    return { line, pick };
  }

  // A slot, which is read again from a new answer if it is the one chosen.
  // A slot that opens something is given what to do in place of choosing.
  function stocked(item, pos, { line, pick }, open) {
    const cell = slot(item, pos, open || pick);
    if (item && pos === pickedPos) line.textContent = cell.title;
    return cell;
  }

  const contentsOf = (item) => listOf(item && item.contents);

  // The containers a path of slots goes through, outermost first, or null
  // where the answer no longer has one of them.
  function descend(by, path) {
    const trail = [];
    let level = by;
    for (const at of path) {
      const item = level.get(at);
      if (!item || contentsOf(item).length === 0) return null;
      trail.push(item);
      level = new Map(contentsOf(item).map((s) => [s.slot, s]));
    }
    return trail;
  }

  function look(label, path, focusPos) {
    inside = path.length > 0 ? { label, path } : null;
    if (!last) return;
    show(last, true);
    const target = focusPos
      ? Array.from(view.body.querySelectorAll('button')).find((b) => b.dataset.pos === focusPos && !b.disabled)
      : view.body.querySelector('.nested-bar button');
    if (target) target.focus();
  }

  // A container's contents in place of the grid it was opened from, with the
  // way back to the one outside it.
  function nested(label, path, trail) {
    const inner = trail.map((item) => item.customName || names.tidy(item.name));
    const title = [label, ...inner].join(' › ');
    const bar = el('div', 'nested-bar');
    const parent = path.slice(0, -1);
    const slotOfThis = `${[label, ...parent].join('/')}|${path[path.length - 1]}`;
    bar.append(button('Back', () => look(label, parent, slotOfThis)), el('strong', 'nested-title', title));
    const wrap = el('div', 'nested');
    wrap.append(bar, grids(contentsOf(trail[trail.length - 1]), [[0, SHULKER, '']], label, path, title));
    return wrap;
  }

  // Grids of slots, 9 across, one for each range of slots given, in the
  // order given and one under the next. A slot holding a container opens it,
  // down to MAX_OPEN containers.
  function grids(stacks, ranges, label, path = [], title = label) {
    const by = new Map(stacks.map((s) => [s.slot, s]));
    if (path.length === 0 && inside && inside.label === label) {
      const trail = descend(by, inside.path);
      if (trail) return nested(label, inside.path, trail);
      inside = null;
    }
    const read = reading();
    const key = [label, ...path].join('/');
    const wrap = el('div', 'grids');
    for (const [from, to, name] of ranges) {
      const out = el('div', 'grid');
      out.setAttribute('role', 'group');
      out.setAttribute('aria-label', name ? `${title}, ${name}` : title);
      for (let i = from; i < to; i++) {
        const stack = by.get(i);
        const opens = stack && path.length < MAX_OPEN && contentsOf(stack).length > 0;
        const cell = stocked(stack, `${key}|${i}`, read, opens ? () => look(label, [...path, i]) : undefined);
        out.append(cell);
        if (opens) {
          const hint = `opens ${contentsOf(stack).length} ${contentsOf(stack).length === 1 ? 'stack' : 'stacks'}`;
          cell.classList.add('container');
          cell.title = `${cell.title} · ${hint}`;
          cell.setAttribute('aria-label', `${cell.getAttribute('aria-label')}, ${hint}`);
        }
      }
      wrap.append(out);
    }
    wrap.append(read.line);
    return wrap;
  }

  // Worn and held items as slots like the rest, each with its place named
  // under it, since a slot of armour alone does not say which it is.
  function equipment(record) {
    const worn = new Map(listOf(record.armor).map((s) => [s.slot, s]));
    const read = reading();
    const group = (label, kind, places) => {
      const out = el('div', `grid ${kind}`);
      out.setAttribute('role', 'group');
      out.setAttribute('aria-label', label);
      places.forEach(([place, item], i) => {
        const box = stocked(item, `${label}|${i}`, read);
        box.setAttribute('aria-label', `${place}: ${item ? box.title : 'Empty'}`);
        const cell = el('div', 'gear');
        cell.append(box, el('span', 'place', place));
        out.append(cell);
      });
      return out;
    };
    const wrap = el('div', 'equipment');
    wrap.append(
      group('Worn', 'worn', ARMOUR.map((place, i) => [place, worn.get(i) || null])),
      group('Held', 'held', [['Main hand', record.mainhand || null], ['Off hand', record.offhand || null]]),
      read.line,
    );
    return wrap;
  }

  // --- the save ----------------------------------------------------------

  function place(p, where) {
    if (!p || ![p.x, p.y, p.z].every(Number.isFinite)) return null;
    const row = el('span', 'where');
    row.append(`X ${fmt(p.x)}, Y ${fmt(p.y)}, Z ${fmt(p.z)} · ${labelOf(text(p.dimension))} `);
    row.append(button('Go to', () => {
      const ok = app.go ? app.go(text(p.dimension), p.x + 0.5, p.z + 0.5) : false;
      note(ok ? '' : `The map has not drawn ${labelOf(text(p.dimension))} yet.`);
    }));
    row.title = where;
    return row;
  }

  // The pack's last sample of an online player, as the saved record's own
  // shapes, so the same gauges draw it.
  function liveVitals(live) {
    if (!live) return {};
    const out = {};
    const health = num(live.health);
    const max = num(live.maxHealth);
    if (health !== null && max !== null && max > 0) out.health = { current: health, max };
    const level = num(live.level);
    if (level !== null) out.level = level;
    return out;
  }

  function vitals(saved, live) {
    const now = liveVitals(live);
    // Experience progress is not in the live sample, so a live level has no
    // bar of its own and the saved progress, which belongs to an older
    // level, is not drawn beside it.
    const r = { ...saved, ...now };
    if (now.level !== undefined) delete r.progress;
    const hearts = marked(gauge(r.health, 'Health', (g) => `${one(g.current / 2)} of ${one(g.max / 2)} hearts`), 'health', r.health);
    const hunger = marked(gauge(r.hunger, 'Hunger', (g) => `${one(g.current)} of ${one(g.max)}`), 'hunger', r.hunger);
    const sat = gauge(r.saturation, 'Saturation', (g) => one(g.current));
    const absorb = r.absorption && r.absorption.current > 0 ? marked(gauge(r.absorption, 'Absorption', (g) => `${one(g.current / 2)} hearts`), 'absorption', r.absorption) : 'None';
    const air = num(r.air);
    let breath = null;
    if (air !== null) breath = meter(air / MAX_AIR, air >= MAX_AIR ? 'Full' : `${one(Math.max(0, air) / TICKS_PER_SECOND)} s of breath`, 'Air');
    const level = num(r.level);
    const progress = num(r.progress);
    const experience = level === null ? null : meter(progress === null ? 0 : progress, `Level ${fmt(level)}${progress === null ? '' : `, ${Math.round(progress * 100)}% to the next`}`, 'Experience');
    return facts([['Health', hearts], ['Hunger', hunger], ['Saturation', sat], ['Absorption', absorb], ['Air', breath], ['Experience', experience]]);
  }

  // Which of the figures above are live, said in words.
  function liveSentence(live) {
    const now = liveVitals(live);
    const what = [now.health ? 'health' : '', now.level !== undefined ? 'level' : ''].filter(Boolean).join(' and ');
    if (!what) return '';
    const at = live.at ? Date.parse(live.at) : NaN;
    return `Live: the ${what} below ${what.includes(' and ') ? 'are' : 'is'} from the game right now${Number.isFinite(at) ? ` (${Date.now() - at < 15_000 ? 'a moment ago' : ago(at)})` : ''}; the rest is from the save.`;
  }

  function liveNote(live) {
    const sentence = liveSentence(live);
    if (!sentence) return null;
    liveLine = el('p', 'note live-note', sentence);
    return liveLine;
  }

  function savedParts(saved, live) {
    const out = [];
    const state = text(saved && saved.state);
    const at = saved && saved.asOf ? Date.parse(saved.asOf) : NaN;
    out.push(el('h3', '', 'From the world save'));
    if (state === 'pending') {
      out.push(el('p', 'note', 'The map has not read a snapshot of the world yet.'));
      return out;
    }
    if (state !== 'known' || !saved.record) {
      // Only real live figures make a section: with none, vitals() would
      // still draw its fallback rows.
      const labelled = liveNote(live);
      if (labelled) out.push(el('h3', '', 'Right now'), labelled, vitals({}, live));
      out.push(el('p', 'note', `No saved record for this player yet. The game writes one when the world next saves after a player joins, and the map reads it from the snapshot after that${Number.isFinite(at) ? `; the last snapshot was ${ago(at)}` : ''}.`));
      return out;
    }
    const r = saved.record;
    out.push(el('p', 'note', Number.isFinite(at)
      ? `As of the snapshot ${ago(at)}. The game rewrites this when the world saves and when the player logs out, so it can be older than what they are doing now.`
      : 'The game rewrites this when the world saves and when the player logs out, so it can be older than what they are doing now.'));

    const status = vitals(r, live);
    const labelled = liveNote(live);
    if (labelled) out.push(labelled);
    if (status) out.push(status);
    out.push(...[facts([
      ['Game mode', GAME_MODES[text(r.gameMode)] || null],
      ['Dimension when saved', text(r.dimension) ? labelOf(r.dimension) : null],
    ])].filter(Boolean));

    out.push(el('h3', '', 'Worn and held'), equipment(r));
    // As the game lays it out: the backpack's three rows, the hotbar under them.
    out.push(el('h3', '', 'Inventory'), grids(listOf(r.inventory), [[HOTBAR, INVENTORY, 'backpack'], [0, HOTBAR, 'hotbar']], 'Inventory'));
    out.push(el('h3', '', 'Ender chest'), grids(listOf(r.enderChest), [[0, ENDER, '']], 'Ender chest'));

    out.push(el('h3', '', 'Places and progress'));
    out.push(facts([
      ['Last death', r.death ? place(r.death, 'Where the player last died') : 'No death in the save'],
      ['Respawn point', r.spawn ? place(r.spawn, 'The bed or anchor the player respawns at') : 'None set: they respawn at the world spawn'],
      ['Recipes unlocked', num(r.recipesUnlocked) !== null ? fmt(r.recipesUnlocked) : null],
      ['Has seen the credits', r.seenCredits === true ? 'Yes' : 'No'],
      ['Warden threat', num(r.wardenThreat) !== null ? `Level ${fmt(r.wardenThreat)}` : null],
    ]));
    return out;
  }

  // --- the trails --------------------------------------------------------

  function dayName(date) {
    const at = new Date(`${date}T00:00:00Z`);
    return Number.isNaN(at.getTime()) ? date : at.toLocaleDateString('en-GB', { weekday: 'short', day: 'numeric', month: 'short', timeZone: 'UTC' });
  }

  function tile(value, label) {
    const t = el('div', 'tile');
    t.append(el('strong', '', value), el('span', '', label));
    return t;
  }

  function trailParts(trail) {
    const out = [el('h3', '', 'From the trails')];
    const state = text(trail && trail.state);
    if (state === 'off' || !trail) {
      out.push(el('p', 'note', 'This map is not keeping trails, so there is nothing to say of where this player has been.'));
      return out;
    }
    const s = trail.stats;
    if (state !== 'known' || !s) {
      out.push(el('p', 'note', 'The map has not seen this player since it started keeping trails.'));
      return out;
    }
    const window = num(s.windowSeconds) || 0;
    const visited = (Array.isArray(s.dimensions) ? s.dimensions : []).filter((d) => typeof d === 'string').map(labelOf).join(', ');
    out.push(el('p', 'note', `The last ${span(window)}, from the positions the map has seen. Time online counts the gaps of up to ${span(num(s.gapSeconds) || 0)} between positions; days are UTC. A restart of the map forgets it.`));
    const tiles = el('div', 'tiles');
    tiles.append(
      tile(`${fmt(s.blocks)} blocks`, 'travelled'),
      tile(span(s.onlineSeconds), 'online'),
      tile(Number.isFinite(s.lastSeen) ? ago(s.lastSeen * 1000) : '—', `last seen${text(s.lastDimension) ? ` in ${labelOf(s.lastDimension)}` : ''}`),
      tile(visited || '—', 'dimensions visited'),
    );
    out.push(tiles);
    const days = listOf(s.days);
    if (days.length > 0) {
      const most = Math.max(1, ...days.map((d) => num(d.onlineSeconds) || 0));
      const list = el('ul', 'days');
      for (const d of days) {
        const row = el('li');
        row.append(el('span', 'day', dayName(text(d.date))), meter((num(d.onlineSeconds) || 0) / most, span(d.onlineSeconds || 0), `Online on ${dayName(text(d.date))}`), el('span', 'dim', `${fmt(d.blocks || 0)} blocks`));
        list.append(row);
      }
      out.push(list);
    }
    return out;
  }

  // --- the counters ------------------------------------------------------

  const sinceDate = (iso) => {
    const at = Date.parse(iso);
    return Number.isNaN(at) ? '' : new Date(at).toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric', timeZone: 'UTC' });
  };

  // "entityAttack" and "entity_attack" are both read as words.
  const causeName = (c) => {
    const words = String(c).replace(/([a-z0-9])([A-Z])/g, '$1 $2').replace(/_/g, ' ').toLowerCase();
    return words.charAt(0).toUpperCase() + words.slice(1);
  };

  const tallyOf = (v) => (v && typeof v === 'object' ? Object.entries(v).filter(([k, n]) => k && Number.isFinite(n) && n > 0).sort((a, b) => b[1] - a[1] || (a[0] < b[0] ? -1 : 1)) : []);
  const total = (rows) => rows.reduce((n, [, c]) => n + c, 0);

  // A heading's worth of figures: the biggest first, and how many were left
  // off the end.
  function tally(rows, nameOf) {
    const list = el('ul', 'tally');
    for (const [name, n] of rows.slice(0, LIST_ROWS)) {
      const row = el('li');
      row.append(el('span', 'name', nameOf(name)), el('span', 'dim', fmt(n)));
      list.append(row);
    }
    if (rows.length > LIST_ROWS) list.append(el('li', 'dim', `and ${fmt(rows.length - LIST_ROWS)} more`));
    return list;
  }

  function counterParts(counters) {
    const state = text(counters && counters.state);
    if (state === 'off' || !counters) return [];
    const out = [];
    if (state !== 'known') {
      out.push(el('h3', '', 'Counted by the map'), el('p', 'note', 'Nothing has been counted for this player yet. The game keeps no statistics, so the map counts from when its script pack started reporting; the first count appears once the player has done something.'));
      return out;
    }
    const since = sinceDate(text(counters.since));
    const when = since ? `Counting since ${since} (UTC)` : 'Counting since the map began';
    const kills = tallyOf(counters.kills);
    const deaths = tallyOf(counters.deaths);
    const broken = tallyOf(counters.brokenTypes);
    const placed = tallyOf(counters.placedTypes);

    out.push(el('h3', '', 'Combat'), el('p', 'note', `${when}. The game keeps no statistics, so nothing before that is counted.`));
    const combat = el('div', 'tiles');
    combat.append(tile(fmt(total(kills)), 'mobs killed'), tile(fmt(total(deaths)), 'deaths'));
    out.push(combat);
    if (kills.length > 0) out.push(el('h4', '', 'Mobs killed'), tally(kills, (k) => names.tidy(k)));
    if (deaths.length > 0) out.push(el('h4', '', 'Deaths by cause'), tally(deaths, causeName));

    out.push(el('h3', '', 'Building'), el('p', 'note', `${when}. The lists name the kinds most often broken and placed, so they can add up to less than the totals.`));
    const building = el('div', 'tiles');
    building.append(tile(fmt(num(counters.broken) || 0), 'blocks broken'), tile(fmt(num(counters.placed) || 0), 'blocks placed'));
    out.push(building);
    if (broken.length > 0) out.push(el('h4', '', 'Broken'), tally(broken, (k) => names.tidy(k)));
    if (placed.length > 0) out.push(el('h4', '', 'Placed'), tally(placed, (k) => names.tidy(k)));

    out.push(el('h3', '', 'Sessions'), el('p', 'note', `${when}. Time online is added as it passes, so a server restart loses at most a few seconds.`));
    const sessions = el('div', 'tiles');
    sessions.append(tile(fmt(num(counters.joins) || 0), 'joins'), tile(span(num(counters.onlineSeconds) || 0), 'online'));
    out.push(sessions);
    return out;
  }

  // --- opening -----------------------------------------------------------

  function note(content) {
    if (said) clearTimeout(said);
    view.said.textContent = content;
    said = content === '' ? null : setTimeout(() => note(''), 4000);
  }

  // What in the body has focus, named by where it is so that it can be
  // found again after the body is drawn anew.
  function focused() {
    const at = document.activeElement;
    if (!at || !view.body.contains(at)) return null;
    if (at.dataset && at.dataset.pos) return `pos:${at.dataset.pos}`;
    return `button:${Array.from(view.body.querySelectorAll('button')).indexOf(at)}`;
  }

  function refocus(was) {
    if (was === null) return;
    const buttons = Array.from(view.body.querySelectorAll('button'));
    const found = was.startsWith('pos:') ? buttons.find((b) => b.dataset.pos === was.slice(4) && !b.disabled) : buttons[Number(was.slice(7))];
    // What had focus has gone, or is now an empty slot that a browser will
    // not focus, so focus stays in the panel, where Escape still shuts it.
    (found && !found.disabled ? found : view.close).focus();
  }

  function show(data, force) {
    last = data;
    const now = JSON.stringify({ ...data, live: data.live && { ...data.live, at: undefined } });
    if (!force && now === drawn) {
      if (liveLine && data.live) liveLine.textContent = liveSentence(data.live);
      return;
    }
    liveLine = null;
    drawn = now;
    const was = focused();
    view.body.replaceChildren(...savedParts(data.saved, data.live), ...counterParts(data.counters), ...trailParts(data.trails));
    icons.paint(view.body);
    refocus(was);
    const own = text(data.name);
    if (own && subject && !subject.name) view.name.textContent = own;
  }

  function fail(content) {
    view.body.replaceChildren(el('p', 'note', content));
  }

  async function fetchIt(first) {
    if (!subject) return;
    if (ask) ask.abort();
    const mine = new AbortController();
    ask = mine;
    const named = subject.name ? `?name=${encodeURIComponent(subject.name)}` : '';
    try {
      const res = await fetch(`api/players/${encodeURIComponent(subject.id)}${named}`, { cache: 'no-store', signal: mine.signal });
      if (ask !== mine) return;
      if (res.ok) {
        show(await res.json());
        return;
      }
      if (first) fail(res.status === 404 ? 'This map does not keep player details.' : 'The details could not be fetched. Open them again to try once more.');
    } catch {
      if (ask === mine && first) fail('The details could not be fetched. Open them again to try once more.');
    }
  }

  function open(who) {
    if (!who || typeof who.id !== 'string' || !/^-?\d{1,20}$/.test(who.id)) return false;
    subject = { id: who.id, name: text(who.name) };
    last = null;
    drawn = '';
    pickedPos = '';
    inside = null;
    clearInterval(timer);
    clearInterval(liveTimer);
    view.name.textContent = subject.name || 'Player';
    view.sub.textContent = 'Everything the world save and the trails know of this player';
    view.body.replaceChildren(el('p', 'note', 'Reading what the map knows…'));
    note('');
    if (!view.dialog.open) view.dialog.show();
    document.body.classList.add('player-shown');
    view.dialog.scrollTop = 0;
    timer = setInterval(() => fetchIt(false), REFRESH_MS);
    // Quick while the player is online, which only the answer says. It goes
    // through the same drawing, so an unchanged answer redraws nothing.
    liveTimer = setInterval(() => {
      if (last && last.live) fetchIt(false);
    }, LIVE_REFRESH_MS);
    fetchIt(true);
    return true;
  }

  function shut() {
    subject = null;
    if (ask) ask.abort();
    ask = null;
    clearInterval(timer);
    clearInterval(liveTimer);
    if (view.dialog.open) view.dialog.close();
  }

  view.close.addEventListener('click', shut);
  view.dialog.addEventListener('close', () => {
    subject = null;
    if (ask) ask.abort();
    ask = null;
    clearInterval(timer);
    clearInterval(liveTimer);
    document.body.classList.remove('player-shown');
  });
  // A non-modal dialog is not shut by Escape on its own.
  view.dialog.addEventListener('keydown', (e) => {
    if (e.key === 'Escape') {
      e.stopPropagation();
      shut();
    }
  });
  // A picture that arrived while the panel was open.
  document.addEventListener('mcmap:pictures', () => {
    if (view.dialog.open) icons.paint(view.body);
  });
  // The names were written with the table there was then.
  document.addEventListener('mcmap:names', () => {
    if (view.dialog.open && last) show(last, true);
  });

  app.playerDetails = { open, shut, isOpen: () => view.dialog.open === true };
})();
