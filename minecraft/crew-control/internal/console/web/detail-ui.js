// The unit detail page: what the world save and the live poll hold of one
// unit — vitals, effects, what it carries — read from the detail route.
// detail.js holds the logic; this file only fetches and paints. A read that
// changes nothing repaints nothing, and a section that did change is remade
// whole with the operator's place in it put back, so a poll never takes
// focus, an open box or the inspect line away.
import {
  ARMOUR_NAMES, ARMOUR_CAPTIONS, freshness, effectLabel, effectTime, slotLabel, inventoryRows, armourSlots, iconLabel,
  chestState, nextSlot, iconStyle, iconRect, ageText, durability, contentsText, slotTitle,
} from './detail.js';
import { createWhyPanel } from './insights-ui.js';

// A page reads its own answer this often; the save behind it is itself read
// no more than every 5 s, so anything shorter only burns the round trip.
const POLL_MS = 10_000;

// Hyphenated keys (aria-*, data-*) are not DOM properties, so assigning
// them silently does nothing; they must go through setAttribute. So does
// role, which not every browser reflects as a property.
function el(tag, props = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k.includes('-') || k === 'role') e.setAttribute(k, v);
    else e[k] = v;
  }
  e.append(...kids);
  return e;
}

const unitPath = (id, suffix) => `/units/${encodeURIComponent(id)}${suffix}`;

// host: { statuses, state(), operator(), api(method, path, body) }
export function initDetail(host) {
  const why = createWhyPanel();
  const root = el('section', { className: 'dt' });
  root.setAttribute('aria-label', 'Unit detail');
  root.setAttribute('data-theme', 'dark');

  let mounted = false;
  let unit = '';
  let token = 0;
  let sheet = null; // { doc, url } or null
  let sheetRead = null; // the one read of the item sheet this mount makes
  let timer = 0;
  let view = null; // the page now on screen: { title, fresh, stale, page, nodes, keys }
  let seenAt = 0; // when the answer on screen was read
  // Grids on screen, by key, so a repaint can find where focus was; and the
  // state of each that outlives the nodes: its tab stop, its open box and
  // the slot its inspect line speaks of.
  const grids = new Map();
  const ui = new Map();

  // ---- data ----

  async function load() {
    const id = unit;
    if (!id || !mounted) return;
    const my = ++token;
    sheetRead ||= loadSheet(id);
    const [r, s] = await Promise.all([
      // The answer is private to the session: no-store keeps the browser
      // from repainting it after sign-out, whatever the server says.
      host.api('GET', unitPath(id, '/detail'), undefined, { cache: 'no-store' }),
      sheetRead,
    ]);
    if (!mounted || my !== token) return;
    sheet = s;
    if (r.status === 401) paintAuth();
    else if (r.status === 404) paintNote('This unit is not in the crew roster.');
    // An outage is not an answer about the unit: what is drawn stays, marked
    // old, and only a page with nothing drawn yet shows the failure.
    else if (!r.ok || !r.data) {
      if (view) markStale();
      else paintRetry((r.data && r.data.error) || `Failed (${r.status}).`);
    } else paint(r.data);
  }

  // The item sheet is the camera's atlas for the same unit, read once per
  // mount: a unit's sheet does not change under an open page. A unit whose
  // camera has no atlas falls back to text labels; the page does not fail.
  async function loadSheet(id) {
    let doc = null;
    try {
      // Item faces are optional: a camera that never answers must not hold the page back.
      const res = await fetch(unitPath(id, '/v1/camera/items.json'), { signal: AbortSignal.timeout(4000) });
      if (!res.ok) return null;
      doc = await res.json();
    } catch {
      return null;
    }
    return { doc, url: unitPath(id, '/v1/camera/items.png') };
  }

  // ---- states ----

  // Anything but the page itself ends the page: what was drawn is no longer
  // true of this session, and nothing of it may be patched into later.
  function forget() {
    view = null;
    grids.clear();
    ui.clear();
  }

  function showState(...kids) {
    forget();
    root.replaceChildren(...kids);
  }

  function paintNote(text) {
    showState(el('div', { className: 'dt-state' }, el('h2', { textContent: text })));
  }

  function paintAuth() {
    showState(el('div', { className: 'dt-state' },
      el('h2', { textContent: "Sign in to see a unit's inventory" }),
      el('p', {}, 'Unit inventory and the ender chest are operator-only. ', el('a', { href: '/signin', textContent: 'Sign in' }), '.')));
  }

  function paintRetry(text) {
    const retry = el('button', { type: 'button', textContent: 'Try again' });
    retry.addEventListener('click', load);
    showState(el('div', { className: 'dt-state' },
      el('h2', { textContent: 'Could not read this unit.' }),
      el('p', { textContent: text }),
      retry));
  }

  // ---- paint ----

  // Each section with what of the answer it is drawn from: a section is
  // remade only when that changed, so the poller's clock and a neighbour's
  // numbers never disturb a grid someone is standing in.
  const SECTIONS = [
    ['vitals', d => [d.health, d.hunger, d.air, d.xp], vitals],
    ['chips', d => [d.gameMode, d.dimension, d.online], chips],
    ['effects', d => d.effects, effects],
    ['armour', d => [d.armor, d.mainhand, d.offhand], armour],
    ['carry', d => [d.inventory, d.save && d.save.state], carry],
    ['chest', d => [d.enderChest, d.save && d.save.state], enderChest],
  ];

  function paint(d) {
    seenAt = Date.now();
    if (!view) {
      const title = el('h2');
      const fresh = el('p');
      const stale = el('p', { className: 'dt-stale', hidden: true });
      stale.setAttribute('role', 'status');
      const page = el('div', { className: 'dt-page' }, el('header', { className: 'dt-head' }, title, fresh, stale));
      view = { title, fresh, stale, page, nodes: new Map(), keys: new Map() };
      root.replaceChildren(page);
    }
    view.title.textContent = d.name || d.id || unit;
    const fresh = freshness(d, seenAt);
    view.fresh.className = `dt-fresh ${fresh.kind}`;
    view.fresh.textContent = fresh.text;
    view.stale.hidden = true;
    for (const [id, keyOf, build] of SECTIONS) {
      const key = JSON.stringify([keyOf(d), Boolean(sheet)]);
      if (view.keys.get(id) === key) continue;
      view.keys.set(id, key);
      const held = heldSlot();
      const node = build(d);
      const old = view.nodes.get(id);
      if (old) old.replaceWith(node);
      else view.page.append(node);
      view.nodes.set(id, node);
      if (held) restoreSlot(held);
    }
  }

  function markStale() {
    const ago = ageText(new Date(seenAt).toISOString());
    view.stale.textContent = `Stale: could not refresh. This is what was read ${ago}.`;
    view.stale.hidden = false;
  }

  // Where focus is among the grids, before a section is remade, and back
  // there afterwards: a remade grid is new nodes, and a browser drops focus
  // with the node that held it.
  function heldSlot() {
    const active = document.activeElement;
    if (!active) return null;
    for (const [key, g] of grids) {
      const at = g.indexOf(active);
      if (at >= 0) return { key, at, from: g };
    }
    return null;
  }

  function restoreSlot({ key, at, from }) {
    const g = grids.get(key);
    if (g && g !== from) g.focusSlot(at, true);
  }

  function vitals(d) {
    const bar = (label, cur, max, cls) => {
      const known = typeof cur === 'number';
      const fill = el('i');
      if (known) fill.style.width = `${Math.max(0, Math.min(100, (cur / max) * 100))}%`;
      const box = el('div', { className: `dt-gauge ${cls}` + (known && cur <= 6 ? ' low' : '') },
        el('span', { className: 'dt-glabel', textContent: label }),
        el('span', { className: 'dt-track' }, fill),
        el('span', { className: 'dt-gval', textContent: known ? String(Math.round(cur)) : '-' }));
      box.title = known ? `${label} ${Math.round(cur)} of ${max}` : `${label} unknown`;
      return box;
    };
    const box = el('div', { className: 'dt-vitals' }, bar('Health', d.health ? d.health.current : null, d.health ? d.health.max : 20, 'hp'),
      bar('Hunger', d.hunger ? d.hunger.current : null, 20, 'food'));
    if (typeof d.air === 'number') box.append(bar('Air', d.air, 300, 'air'));
    const xp = d.xp || {};
    const level = typeof xp.level === 'number';
    const fill = el('i');
    fill.style.width = `${Math.round(Math.max(0, Math.min(1, xp.progress ?? 0)) * 100)}%`;
    const xpBox = el('div', { className: 'dt-gauge xpl' },
      el('span', { className: 'dt-glabel', textContent: 'XP' }),
      el('span', { className: 'dt-track' }, fill),
      el('span', { className: 'dt-gval', textContent: level ? `Lv ${xp.level}` : '-' }));
    xpBox.title = level ? `Level ${xp.level}` : 'XP unknown';
    box.append(xpBox);
    return box;
  }

  function chips(d) {
    const list = [];
    if (d.gameMode) list.push([d.gameMode, 'mode']);
    if (d.dimension) list.push([d.dimension, 'dim']);
    list.push([d.online ? 'online' : 'offline', d.online ? 'on' : 'off']);
    return el('div', { className: 'dt-chips' }, ...list.map(([t, cls]) => el('span', { className: `dt-chip ${cls}`, textContent: t })));
  }

  function effects(d) {
    const box = el('section', { className: 'dt-sec' }, el('h3', { textContent: 'Effects' }));
    if (!(d.effects || []).length) box.append(el('p', { className: 'dt-none', textContent: 'no effects on this unit' }));
    else box.append(el('div', { className: 'dt-chips' }, ...d.effects.map(e => el('span', { className: 'dt-chip fx', title: `Amplifier ${e.amplifier}` }, `${effectLabel(e)} · ${effectTime(e.duration)}`, ))));
    return box;
  }

  // ---- slots ----

  function slotCell(item, opts = {}) {
    const b = el('button', {
      type: 'button',
      className: 'dt-slot' + (item ? ' filled' : ' empty'),
      tabIndex: -1,
      'aria-label': slotLabel(item, opts),
      title: slotTitle(item, opts),
    });
    const icon = item && itemIcon(item);
    if (icon) b.append(icon);
    else if (item) b.append(el('span', { className: 'dt-noicon', 'aria-hidden': 'true', textContent: iconLabel(item.name) }));
    if (item) {
      if (item.count > 1) b.append(el('span', { className: 'dt-count', textContent: String(item.count) }));
      if (item.enchantments && item.enchantments.length) b.append(el('span', { className: 'dt-mark ench', title: 'enchanted', 'aria-hidden': 'true', textContent: '✦' }));
      if (item.contents && item.contents.length) b.append(el('span', { className: 'dt-mark boxl', title: 'shulker box with contents', 'aria-hidden': 'true', textContent: '▣' }));
      const left = durability(item);
      if (left !== null) {
        const bar = el('i', { className: 'dt-dur' + (left <= .2 ? ' low' : '') });
        bar.style.setProperty('--f', `${Math.round(Math.max(0, Math.min(1, left)) * 100)}%`);
        b.append(bar);
      }
    }
    return b;
  }

  // One cropped tile of the camera's item sheet as a background. The crop
  // is percentages of the element, so every slot size serves from one rule.
  function itemIcon(item) {
    if (!sheet || !sheet.doc || !sheet.url) return null;
    const rect = iconRect(sheet.doc, item.name);
    const st = iconStyle(rect);
    if (!st) return null;
    const i = el('i', { className: 'dt-icon', 'aria-hidden': 'true' });
    for (const [k, v] of Object.entries(st)) i.style[k] = v;
    i.style.backgroundImage = `url('${sheet.url}')`;
    return i;
  }

  // A grid is one tab stop: its slots rove the tabindex and arrows walk
  // it, and the focused slot's story is spelled out beneath for every
  // pointer, keyboard and screen reader alike. Its place in the page is its
  // key, so a grid remade by a poll takes up where the last one was left.
  // The slots are a labelled group, not an ARIA grid: they have no rows or
  // cells to navigate by, only the arrows this file adds, and each slot's
  // own label. Where a slot has a place of its own, `places` names it and
  // captions it beneath.
  function gridBox(key, slots, label, cls, emptyLabel, places = null) {
    const emptyFor = i => {
      if (places) return undefined;
      return typeof emptyLabel === 'function' ? emptyLabel(i) : `${label}: ${emptyLabel}`;
    };
    const cells = slots.map((item, i) => wrapCell(
      slotCell(item, { emptyLabel: emptyFor(i), slotName: places && places.names[i] }),
      places && places.captions[i]));
    const grid = el('div', { className: `dt-grid ${cls}`, role: 'group', 'aria-label': label }, ...cells);
    const line = el('p', { className: 'dt-inspect', role: 'status', 'aria-live': 'polite' });
    // What outlives a remade grid: the tab stop, the one box held open and
    // the slot the line beneath last spoke of. Expansion is a per-slot
    // toggle and closes another slot's, so two boxes never shout at once.
    if (!ui.has(key)) ui.set(key, { rove: -1, expanded: -1, said: -1 });
    const mem = ui.get(key);
    const slotAt = i => (i >= 0 && i < cells.length ? cells[i].firstElementChild : null);
    const indexOf = s => cells.findIndex(c => c.firstElementChild === s);
    const focusSlot = (i, quiet = false) => {
      const s = slotAt(i);
      if (s) s.focus({ preventScroll: quiet });
    };
    const insideText = i => contentsText(slots[i]);
    const describe = at => {
      const s = slotAt(at);
      if (!s) return;
      mem.said = at;
      line.textContent = s.getAttribute('aria-label') + (mem.expanded === at ? insideText(at) : '');
    };
    grid.addEventListener('focusin', ev => {
      const s = ev.target && ev.target.closest && ev.target.closest('.dt-slot');
      if (!s) return;
      if (slotAt(mem.rove)) slotAt(mem.rove).tabIndex = -1;
      const at = indexOf(s);
      mem.rove = at;
      s.tabIndex = 0;
      // The tooltip a pointer gets from title is stated here too, so a
      // focused slot tells its whole story however it was reached.
      describe(at);
    });
    grid.addEventListener('click', ev => {
      const s = ev.target && ev.target.closest && ev.target.closest('.dt-slot');
      if (!s) return;
      const at = indexOf(s);
      // A box slot's click is "open it", not a selection look-alike.
      if (insideText(at)) mem.expanded = mem.expanded === at ? -1 : at;
      focusSlot(at);
      describe(at);
    });
    grid.addEventListener('keydown', ev => {
      const s = ev.target && ev.target.closest && ev.target.closest('.dt-slot');
      if (!s) return;
      const at = indexOf(s);
      if (at < 0) return;
      if (ev.key === 'Enter' || ev.key === ' ') {
        // Enter and Space already mean something on a button; on a slot
        // they open a box instead of doing nothing.
        if (!insideText(at)) return;
        ev.preventDefault();
        mem.expanded = mem.expanded === at ? -1 : at;
        describe(at);
        return;
      }
      const next = nextSlot(at, slots.length, ev.key);
      if (next !== null && next !== at) {
        ev.preventDefault();
        focusSlot(next);
        describe(next);
      }
    });
    // A fresh grid's first stop lands on something filled when the grid
    // holds anything; an all-empty one starts at its first slot. A remade
    // grid keeps the stop it had, and a box that has since emptied closes.
    if (!insideText(mem.expanded)) mem.expanded = -1;
    if (!slotAt(mem.rove)) mem.rove = Math.max(0, slots.findIndex(Boolean));
    slotAt(mem.rove).tabIndex = 0;
    if (mem.said >= 0) describe(mem.said);
    grids.set(key, { indexOf: s => indexOf(s), focusSlot });
    return { grid, line };
  }

  // The slot must stay the cell's first child: the grid finds its slots so.
  function wrapCell(cell, caption) {
    if (!caption) return el('div', { className: 'dt-cell' }, cell);
    return el('div', { className: 'dt-cell cap' }, cell, el('span', { className: 'dt-cap', 'aria-hidden': 'true', textContent: caption }));
  }

  function gridZone(parts) {
    return el('div', { className: 'dt-zone' }, parts.grid, parts.line);
  }

  // ---- sections ----

  function armour(d) {
    const box = el('section', { className: 'dt-sec' }, el('h3', { textContent: 'Armour and held' }));
    const slots = armourSlots(d);
    if (!slots.some(Boolean)) box.append(el('p', { className: 'dt-none', textContent: 'no armour worn and nothing held' }));
    else box.append(gridZone(gridBox('armour', slots, 'Armour and held', 'dt-armourgrid', '', { names: ARMOUR_NAMES, captions: ARMOUR_CAPTIONS })));
    return box;
  }

  function carry(d) {
    const box = el('section', { className: 'dt-sec' });
    const { main, hotbar } = inventoryRows(d.inventory || []);
    const saveState = d.save && d.save.state;
    box.append(el('h3', { textContent: 'What the unit carries' }));
    // Crew units usually carry in the hotbar alone, so both rows decide.
    if (!main.some(Boolean) && !hotbar.some(Boolean)) {
      box.append(el('p', { className: 'dt-none', textContent: saveState === 'known' ? 'nothing carried' : saveState === 'none' ? "nothing read yet: this unit's record is not saved" : 'nothing carried' }));
      return box;
    }
    box.append(gridZone(gridBox('main', main, 'Main inventory', 'dt-main', 'empty')));
    box.append(el('h4', { className: 'dt-sub', textContent: 'Hotbar' }));
    box.append(gridZone(gridBox('hotbar', hotbar, 'Hotbar', 'dt-hotbar', 'empty')));
    return box;
  }

  // The chest is only ever known from the save: it is private to the unit,
  // so the live poll says nothing about it. The state of the save is the
  // state of the section, and an all-empty chest is its own honest note.
  function enderChest(d) {
    const box = el('section', { className: 'dt-sec' });
    const st = chestState(d.enderChest);
    const h = el('h3', { textContent: 'Ender chest' });
    if (st.kind === 'filled') h.append(el('span', { className: 'dt-chestcount', textContent: ` ${st.filled} of ${st.total} filled` }));
    box.append(h);
    if (st.kind === 'unsaved') {
      box.append(el('p', { className: 'dt-none', textContent:
        d.save && d.save.state === 'off'
          ? 'the ender chest is only known from the world save: world save not connected'
          : d.save && d.save.state === 'pending'
            ? 'the ender chest is only known from the world save: reading the world save'
            : "this unit's record is not saved yet, so its ender chest is not known" }));
      return box;
    }
    if (st.kind === 'empty') {
      box.append(el('p', { className: 'dt-none', textContent: 'the chest is empty — crew units do not use theirs' }));
      return box;
    }
    box.append(gridZone(gridBox('chest', st.chest, 'Ender chest', 'dt-chest', i => `slot ${i + 1}: empty`)));
    return box;
  }

  // A tab that comes back reads at once instead of waiting out the tick,
  // since a hidden one skips its polls.
  function onVisible() {
    if (mounted && document.visibilityState === 'visible') load();
  }

  return {
    mount(into, id) {
      why.stop();
      root.remove();
      clearInterval(timer);
      document.removeEventListener('visibilitychange', onVisible);
      mounted = true;
      unit = id || '';
      token++;
      sheet = null;
      sheetRead = null;
      forget();
      into.append(root);
      if (unit) {
        into.append(why.el);
        why.start(unit);
      }
      if (!unit) {
        paintNote('No unit is picked.');
        return;
      }
      root.replaceChildren(el('p', { className: 'dt-loading', textContent: `Reading ${unit}…` }));
      load();
      timer = setInterval(() => {
        if (document.visibilityState !== 'visible') return;
        load();
      }, POLL_MS);
      document.addEventListener('visibilitychange', onVisible);
    },
    unmount() {
      if (!mounted) return;
      mounted = false;
      token++;
      clearInterval(timer);
      document.removeEventListener('visibilitychange', onVisible);
      why.stop();
      root.remove();
    },
    // A session change changes what the route answers, so an open page is
    // re-read even when nothing else about it changed. Signing out clears
    // the page now: the private answer must not sit on screen until the
    // next read finds a 401, and a read already in flight must not bring it
    // back.
    onOperator(op = host.operator()) {
      if (!mounted) return;
      if (!op) {
        token++;
        if (unit) paintAuth();
        return;
      }
      load();
    },
  };
}
