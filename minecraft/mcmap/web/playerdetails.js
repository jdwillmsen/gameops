'use strict';

// Everything the map knows of one player, in a panel beside the map: what
// the world save holds (vitals, what they carry, where they died and
// sleep) and what the trails saw (how far they went, how long they were
// online). The save is rewritten only when the world saves and when the
// player logs out, so that half says when it is from. The panel opens from
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
  const MAX_AIR = 300;
  const TICKS_PER_SECOND = 20;
  const HOTBAR = 9;
  const INVENTORY = 36;
  const ENDER = 27;
  const ARMOUR = ['Head', 'Chest', 'Legs', 'Feet'];
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
  // The slot whose details are being read, as its grid and place in it.
  let pickedPos = '';

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

  // --- items -------------------------------------------------------------

  // The picture the map already has for what an item is, where it has one:
  // containers, shulker boxes, beds and spawn eggs. Every other item is its
  // initials, which is all the page can draw of it.
  function pictureOf(name) {
    if (!/^[a-z0-9_]{1,64}$/.test(name)) return '';
    if (name === 'chest' || name === 'trapped_chest' || name === 'barrel') return icons.keyOf('container', { kind: name === 'trapped_chest' ? 'chest' : name, trapped: name === 'trapped_chest' });
    if (name === 'shulker_box') return icons.keyOf('container', { kind: 'shulker' });
    const shulker = /^([a-z]+)_shulker_box$/.exec(name);
    if (shulker) return icons.keyOf('container', { kind: 'shulker', colour: shulker[1] });
    const bed = /^([a-z]+)_bed$/.exec(name);
    if (bed) return icons.keyOf('bed', { colour: bed[1] });
    const egg = /^(.+)_spawn_egg$/.exec(name);
    return egg ? icons.keyOf('mob', { kind: egg[1] }) : '';
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
    if (key) box.append(icons.picture(key));
    box.append(el('span', 'abbr', initials(item.customName || names.tidy(item.name))));
    if (item.count > 1) box.append(el('span', 'count', fmt(item.count)));
    if (listOf(item.enchantments).length > 0) box.classList.add('enchanted');
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

  // Grids of slots, 9 across, one for each range of slots given. The
  // hotbar is the first row of an inventory and is parted from the rest.
  function grids(stacks, ranges, label) {
    const by = new Map(stacks.map((s) => [s.slot, s]));
    const line = el('p', 'note slot-said', 'Choose an item for its details.');
    line.setAttribute('role', 'status');
    const pick = (sentence, pos) => {
      pickedPos = pos;
      line.textContent = sentence;
    };
    const wrap = el('div', 'grids');
    for (const [from, to, name] of ranges) {
      const out = el('div', 'grid');
      out.setAttribute('role', 'group');
      out.setAttribute('aria-label', name ? `${label}, ${name}` : label);
      for (let i = from; i < to; i++) {
        const pos = `${label}|${i}`;
        const cell = slot(by.get(i), pos, pick);
        out.append(cell);
        // The item being read is read again from the new answer.
        if (pos === pickedPos && by.has(i)) line.textContent = cell.title;
      }
      wrap.append(out);
    }
    wrap.append(line);
    return wrap;
  }

  // Worn and held items, a line each, since each has more to say than a box
  // can: the enchantments and the wear.
  function equipment(record) {
    const worn = new Map(listOf(record.armor).map((s) => [s.slot, s]));
    const rows = ARMOUR.map((place, i) => [place, worn.get(i) || null]);
    rows.push(['Main hand', record.mainhand || null], ['Off hand', record.offhand || null]);
    const list = el('dl', 'facts');
    for (const [place, item] of rows) {
      const dd = el('dd');
      if (!item) {
        dd.textContent = 'Nothing';
        dd.className = 'dim';
      } else {
        const picture = pictureOf(item.name);
        if (picture) dd.append(icons.picture(picture), ' ');
        dd.append(describe(item));
      }
      list.append(el('dt', '', place), dd);
    }
    return list;
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

  function vitals(r) {
    const hearts = gauge(r.health, 'Health', (g) => `${one(g.current / 2)} of ${one(g.max / 2)} hearts`);
    const hunger = gauge(r.hunger, 'Hunger', (g) => `${one(g.current)} of ${one(g.max)}`);
    const sat = gauge(r.saturation, 'Saturation', (g) => one(g.current));
    const absorb = r.absorption && r.absorption.current > 0 ? gauge(r.absorption, 'Absorption', (g) => `${one(g.current / 2)} hearts`) : 'None';
    const air = num(r.air);
    let breath = null;
    if (air !== null) breath = meter(air / MAX_AIR, air >= MAX_AIR ? 'Full' : `${one(Math.max(0, air) / TICKS_PER_SECOND)} s of breath`, 'Air');
    const level = num(r.level);
    const progress = num(r.progress);
    const experience = level === null ? null : meter(progress === null ? 0 : progress, `Level ${fmt(level)}${progress === null ? '' : `, ${Math.round(progress * 100)}% to the next`}`, 'Experience');
    return facts([['Health', hearts], ['Hunger', hunger], ['Saturation', sat], ['Absorption', absorb], ['Air', breath], ['Experience', experience]]);
  }

  function savedParts(saved) {
    const out = [];
    const state = text(saved && saved.state);
    const at = saved && saved.asOf ? Date.parse(saved.asOf) : NaN;
    out.push(el('h3', '', 'From the world save'));
    if (state === 'pending') {
      out.push(el('p', 'note', 'The map has not read a snapshot of the world yet.'));
      return out;
    }
    if (state !== 'known' || !saved.record) {
      out.push(el('p', 'note', `No saved record for this player yet. The game writes one when the world next saves after a player joins, and the map reads it from the snapshot after that${Number.isFinite(at) ? `; the last snapshot was ${ago(at)}` : ''}.`));
      return out;
    }
    const r = saved.record;
    out.push(el('p', 'note', Number.isFinite(at)
      ? `As of the snapshot ${ago(at)}. The game rewrites this when the world saves and when the player logs out, so it can be older than what they are doing now.`
      : 'The game rewrites this when the world saves and when the player logs out, so it can be older than what they are doing now.'));

    const status = vitals(r);
    if (status) out.push(status);
    out.push(...[facts([
      ['Game mode', GAME_MODES[text(r.gameMode)] || null],
      ['Dimension when saved', text(r.dimension) ? labelOf(r.dimension) : null],
    ])].filter(Boolean));

    out.push(el('h3', '', 'Worn and held'), equipment(r));
    out.push(el('h3', '', 'Inventory'), grids(listOf(r.inventory), [[0, HOTBAR, 'hotbar'], [HOTBAR, INVENTORY, 'backpack']], 'Inventory'));
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
    // What had focus has gone, so focus stays in the panel, where Escape
    // still shuts it.
    (found || view.close).focus();
  }

  function show(data, force) {
    last = data;
    const now = JSON.stringify(data);
    if (!force && now === drawn) return;
    drawn = now;
    const was = focused();
    view.body.replaceChildren(...savedParts(data.saved), ...trailParts(data.trails));
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
    clearInterval(timer);
    view.name.textContent = subject.name || 'Player';
    view.sub.textContent = 'Everything the world save and the trails know of this player';
    view.body.replaceChildren(el('p', 'note', 'Reading what the map knows…'));
    note('');
    if (!view.dialog.open) view.dialog.show();
    document.body.classList.add('player-shown');
    view.dialog.scrollTop = 0;
    timer = setInterval(() => fetchIt(false), REFRESH_MS);
    fetchIt(true);
    return true;
  }

  function shut() {
    subject = null;
    if (ask) ask.abort();
    ask = null;
    clearInterval(timer);
    if (view.dialog.open) view.dialog.close();
  }

  view.close.addEventListener('click', shut);
  view.dialog.addEventListener('close', () => {
    subject = null;
    if (ask) ask.abort();
    ask = null;
    clearInterval(timer);
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
