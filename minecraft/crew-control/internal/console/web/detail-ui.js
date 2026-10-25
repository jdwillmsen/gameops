// The unit detail page: what the world save and the live poll hold of one
// unit — vitals, effects, what it carries — read from the detail route.
// detail.js holds the logic; this file only fetches and paints. The whole
// page is remade on each read: it answers for its unit as one thing, and
// nothing here needs to patch in place the way a fleet table does.
import {
  ARMOR_SLOTS, freshness, effectLabel, effectTime, slotLabel, inventoryRows, armorRow,
  chestState, nextSlot, iconStyle, iconRect,
} from './detail.js';

// A page reads its own answer at most this often; the route is cached for
// 5 s anyway, so anything shorter only burns the round trip.
const POLL_MS = 10_000;

// Hyphenated keys (aria-*, data-*) are not DOM properties, so assigning
// them silently does nothing; they must go through setAttribute.
function el(tag, props = {}, ...kids) {
  const e = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k.includes('-')) e.setAttribute(k, v);
    else e[k] = v;
  }
  e.append(...kids);
  return e;
}

const unitPath = (id, suffix) => `/units/${encodeURIComponent(id)}${suffix}`;

// host: { statuses, state(), operator(), api(method, path, body) }
export function initDetail(host) {
  const root = el('section', { className: 'dt' });
  root.setAttribute('aria-label', 'Unit detail');
  root.setAttribute('data-theme', 'dark');

  let mounted = false;
  let unit = '';
  let token = 0;
  let sheet = null; // { doc, url } or null
  let timer = 0;

  // ---- data ----

  async function load() {
    const id = unit;
    if (!id || !mounted) return;
    const my = ++token;
    const [r, s] = await Promise.all([
      host.api('GET', unitPath(id, '/detail')),
      loadSheet(id),
    ]);
    if (!mounted || my !== token) return;
    sheet = s;
    if (r.status === 401) paintAuth();
    else if (r.status === 404) paintNote('This unit is not in the crew roster.');
    else if (!r.ok) paintRetry((r.data && r.data.error) || `Failed (${r.status}).`);
    else paint(r.data);
  }

  // The item sheet is the camera's atlas for the same unit. A unit whose
  // camera has no atlas falls back to text labels; the page does not fail.
  async function loadSheet(id) {
    let doc = null;
    try {
      const res = await fetch(unitPath(id, '/v1/camera/items.json'));
      if (!res.ok) return null;
      doc = await res.json();
    } catch {
      return null;
    }
    return { doc, url: unitPath(id, '/v1/camera/items.png') };
  }

  // ---- states ----

  function paintNote(text) {
    root.replaceChildren(el('div', { className: 'dt-state' }, el('h2', { textContent: text })));
  }

  function paintAuth() {
    root.replaceChildren(el('div', { className: 'dt-state' },
      el('h2', { textContent: "Sign in to see a unit's inventory" }),
      el('p', {}, 'Unit inventory and the ender chest are operator-only. ', el('a', { href: '/signin', textContent: 'Sign in' }), '.')));
  }

  function paintRetry(text) {
    const box = el('div', { className: 'dt-state' },
      el('h2', { textContent: 'Could not read this unit.' }),
      el('p', { textContent: text }),
      el('button', { type: 'button', textContent: 'Try again' }));
    box.lastChild.addEventListener('click', load);
    root.replaceChildren(box);
  }

  // ---- paint ----

  function paint(d) {
    const fresh = freshness(d, Date.now());
    const head = el('header', { className: 'dt-head' },
      el('h2', { text: undefined, textContent: d.name || d.id || unit }),
      el('p', { className: `dt-fresh ${fresh.kind}`, textContent: fresh.text }));
    const page = el('div', { className: 'dt-page' }, head);
    root.replaceChildren(page);
    for (const sec of [vitals(d), chips(d), effects(d), armour(d), carry(d), enderChest(d)]) {
      if (sec) page.append(sec);
    }
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
      title: item ? slotLabel(item) : opts.emptyLabel,
    });
    const icon = item && itemIcon(item);
    if (icon) b.append(icon);
    else b.append(el('span', { className: 'dt-noicon', textContent: item ? item.name.replace(/^item_/, '').slice(0, 2) : '' }));
    if (item) {
      if (item.count > 1) b.append(el('span', { className: 'dt-count', textContent: String(item.count) }));
      if (item.enchantments && item.enchantments.length) b.append(el('span', { className: 'dt-mark ench', title: 'enchanted', 'aria-hidden': 'true', textContent: '✦' }));
      if (item.contents && item.contents.length) b.append(el('span', { className: 'dt-mark boxl', title: 'shulker box with contents', 'aria-hidden': 'true', textContent: '▣' }));
      if (item.maxDamage && item.damage < item.maxDamage) {
        const d = (item.maxDamage - item.damage) / item.maxDamage;
        const bar = el('i', { className: 'dt-dur' + (d <= .2 ? ' low' : '') });
        bar.style.setProperty('--f', `${Math.round(d * 100)}%`);
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
  // pointer, keyboard and screen reader alike.
  function gridBox(slots, label, cls, emptyLabel) {
    const emptyFor = typeof emptyLabel === 'function' ? i => emptyLabel(i) : i => `${label}: ${emptyLabel}`;
    const cells = slots.map((item, i) => wrapCell(slotCell(item, { emptyLabel: emptyFor(i) })));
    const grid = el('div', { className: `dt-grid ${cls}`, role: 'grid', 'aria-label': label }, ...cells);
    const line = el('p', { className: 'dt-inspect', role: 'status', 'aria-live': 'polite' });
    // A slot with something inside it says so on the pointer's tooltip and,
    // expanded, under the grid; expansion is a per-slot toggle, and closes
    // another slot's expansion, so two boxes never shout at once.
    const rove = { at: -1 };
    const expanded = { at: -1 };
    const slotAt = i => (i >= 0 && i < cells.length ? cells[i].firstElementChild : null);
    const focusSlot = i => {
      const s = slotAt(i);
      if (s) s.focus({ preventScroll: false });
    };
    const insideText = i => {
      const c = slots[i] && slots[i].contents;
      if (!c || !c.length) return '';
      return ' — inside: ' + c.map(s => `${s.name}${s.count > 1 ? ` x${s.count}` : ''}`).join(', ');
    };
    const describe = at => {
      const s = slotAt(at);
      if (!s) return;
      line.textContent = s.getAttribute('aria-label') + (expanded.at === at ? insideText(at) : '');
    };
    grid.addEventListener('focusin', ev => {
      const s = ev.target && ev.target.closest && ev.target.closest('.dt-slot');
      if (!s) return;
      if (rove.at >= 0 && slotAt(rove.at)) slotAt(rove.at).tabIndex = -1;
      const at = cells.findIndex(c => c.firstElementChild === s);
      rove.at = at;
      s.tabIndex = 0;
      // The tooltip a pointer gets from title is stated here too, so a
      // focused slot tells its whole story however it was reached.
      describe(at);
    });
    grid.addEventListener('click', ev => {
      const s = ev.target && ev.target.closest && ev.target.closest('.dt-slot');
      if (!s) return;
      const at = cells.findIndex(c => c.firstElementChild === s);
      // A box slot's click is "open it", not a selection look-alike.
      if (insideText(at)) expanded.at = expanded.at === at ? -1 : at;
      focusSlot(at);
      describe(at);
    });
    grid.addEventListener('keydown', ev => {
      const s = ev.target && ev.target.closest && ev.target.closest('.dt-slot');
      if (!s) return;
      const at = cells.findIndex(c => c.firstElementChild === s);
      if (at < 0) return;
      if (ev.key === 'Enter' || ev.key === ' ') {
        // Enter and Space already mean something on a button; on a slot
        // they open a box instead of doing nothing.
        if (!insideText(at)) return;
        ev.preventDefault();
        expanded.at = expanded.at === at ? -1 : at;
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
    // The first stop of a fresh grid lands on something filled when the
    // grid holds anything; an all-empty one starts at its first slot.
    const first = slots.findIndex(Boolean);
    if (first >= 0) slotAt(first).tabIndex = 0;
    return { grid, line };
  }

  function wrapCell(cell) {
    return el('div', { className: 'dt-cell', role: 'presentation' }, cell);
  }

  function gridZone(parts) {
    return el('div', { className: 'dt-zone' }, parts.grid, parts.line);
  }

  // ---- sections ----

  function armour(d) {
    const box = el('section', { className: 'dt-sec' }, el('h3', { textContent: 'Armour and held' }));
    const slots = [...armorRow(d.armor || [])];
    if (d.mainhand) slots.push(d.mainhand);
    if (d.offhand) slots.push(d.offhand);
    if (!slots.some(Boolean)) box.append(el('p', { className: 'dt-none', textContent: 'no armour worn and nothing held' }));
    else box.append(gridZone(gridBox(slots, 'Armour and held', 'dt-armourgrid',
      i => (i < ARMOR_SLOTS.length ? `no armour on ${ARMOR_SLOTS[i]}` : i === ARMOR_SLOTS.length ? 'main hand: empty' : 'off hand: empty'))));
    return box;
  }

  function carry(d) {
    const box = el('section', { className: 'dt-sec' });
    const { main, hotbar } = inventoryRows(d.inventory || []);
    const saveState = d.save && d.save.state;
    box.append(el('h3', { textContent: 'What the unit carries' }));
    if (!d.inventory || !main.some(Boolean)) {
      box.append(el('p', { className: 'dt-none', textContent: saveState === 'known' ? 'nothing carried' : saveState === 'none' ? "nothing read yet: this unit's record is not saved" : 'nothing carried' }));
      return box;
    }
    box.append(gridZone(gridBox(main, 'Main inventory', 'dt-main', 'empty')));
    box.append(el('h4', { className: 'dt-sub', textContent: 'Hotbar' }));
    box.append(gridZone(gridBox(hotbar, 'Hotbar', 'dt-hotbar', 'empty')));
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
    box.append(gridZone(gridBox(st.chest, 'Ender chest', 'dt-chest', i => `slot ${i + 1}: empty`)));
    return box;
  }

  return {
    mount(into, id) {
      root.remove();
      mounted = true;
      unit = id || '';
      into.append(root);
      if (!unit) {
        paintNote('No unit is picked.');
        return;
      }
      root.replaceChildren(el('p', { className: 'dt-loading', textContent: `Reading ${unit}…` }));
      load();
      clearInterval(timer);
      timer = setInterval(() => {
        if (document.visibilityState !== 'visible') return;
        load();
      }, POLL_MS);
    },
    unmount() {
      if (!mounted) return;
      mounted = false;
      token++;
      clearInterval(timer);
      root.remove();
    },
    // A session change changes what the route answers, so an open page is
    // re-read even when nothing else about it changed.
    onOperator() {
      if (mounted) load();
    },
  };
}
