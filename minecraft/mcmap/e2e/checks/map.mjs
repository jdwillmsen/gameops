// The map and its controls, used as a visitor uses them. Everything
// pressed here changes only the browser's own settings, which are thrown
// away with the browser: nothing a visitor can press on this page writes
// to the server but the login and the logout, and neither is touched.

import { Fail, skip, sleep, until } from '../lib/check.mjs';
import { openContext, openMap, routeOf } from '../lib/browser.mjs';

// Each dimension's tab shows terrain. One never rendered, as a dimension
// nobody has been to is, has no tab to press and is counted and left.
export async function dimensions({ page }) {
  const tabs = await page.evaluate(() => [...document.querySelectorAll('#dimensions button')].map((b) => b.disabled));
  const home = await page.evaluate(() => window.mcmap.dimension());
  const measures = { rendered: 0, unrendered: 0, tiles: 0, slowest_ms: 0 };
  for (const [index, disabled] of tabs.entries()) {
    if (disabled) { measures.unrendered += 1; continue; }
    const began = Date.now();
    await page.locator('#dimensions button').nth(index).click();
    const id = await until(() => page.evaluate((n) => (window.mcmap.dimension() === window.mcmap.dimensions()[n] ? window.mcmap.dimension() : null), index));
    if (!id) throw new Fail('a dimension tab did not change the dimension', measures);
    // Drawn, and not an empty square: a tile that decoded and has ink.
    const tiles = await until(() => page.evaluate((dimension) => {
      const loaded = [...document.querySelectorAll('#map img.leaflet-tile-loaded')].filter((img) => img.src.includes(`/tiles/${dimension}/`) && img.naturalWidth > 0);
      if (loaded.length === 0) return 0;
      window.__probe.quiet = true;
      try {
        const scratch = document.createElement('canvas');
        scratch.width = scratch.height = 16;
        const ctx = scratch.getContext('2d');
        for (const img of loaded) {
          ctx.clearRect(0, 0, 16, 16);
          ctx.drawImage(img, 0, 0, 16, 16);
          if (ctx.getImageData(0, 0, 16, 16).data.some((v, n) => n % 4 === 3 && v > 0)) return loaded.length;
        }
      } finally { window.__probe.quiet = false; }
      return 0;
    }, id), { timeoutMs: 20_000 });
    if (!tiles) throw new Fail('a rendered dimension drew no terrain', measures);
    measures.rendered += 1;
    measures.tiles += tiles;
    measures.slowest_ms = Math.max(measures.slowest_ms, Date.now() - began);
  }
  if (measures.rendered === 0) throw new Fail('no dimension is rendered', measures);
  await page.evaluate((id) => { const n = window.mcmap.dimensions().indexOf(id); document.querySelectorAll('#dimensions button')[n].click(); }, home);
  await until(() => page.evaluate((id) => window.mcmap.dimension() === id, home));
  return { measures };
}

// Where the nether is drawn at more than one level, the picker is there in
// the nether and nowhere else, and choosing a level draws tiles from it. A
// deployment with one cut of the nether is not an error: there is nothing
// to choose between.
export async function netherLevels({ page }) {
  const home = await page.evaluate(() => window.mcmap.dimension());
  const put = (id) => page.evaluate((to) => window.mcmap.go(to, 0, 0), id);
  const shown = () => page.evaluate(() => { const bar = document.querySelector('.levels'); return Boolean(bar) && !bar.hidden; });
  const back = async () => {
    await page.evaluate(() => window.mcmap.level.set('default'));
    await page.evaluate((id) => window.mcmap.go(id, 0, 0), home);
  };
  if (!(await put('nether'))) return skip('no nether with more than one level here');
  await until(() => page.evaluate(() => window.mcmap.dimension() === 'nether'));
  const offered = await page.evaluate(() => window.mcmap.level.list().length);
  if (offered < 2) {
    await back();
    return skip('no nether with more than one level here');
  }
  const measures = { levels: offered, tiles: 0 };
  const buttons = page.locator('.levels button.level');
  if (!(await until(shown))) throw new Fail('the nether has levels and no picker is shown', measures);
  await buttons.nth(1).click();
  measures.tiles = await until(() => page.evaluate(() => [...document.querySelectorAll('#map img.leaflet-tile-loaded')].filter((img) => img.src.includes('/tiles/nether/') && img.src.includes('level=') && img.naturalWidth > 0).length), { timeoutMs: 20_000 });
  if (!measures.tiles) throw new Fail('choosing a nether level drew no tiles from it', measures);
  if (await buttons.nth(1).getAttribute('aria-pressed') !== 'true') throw new Fail('the picker does not show the level that is drawn', measures);
  await put('overworld');
  if (await shown()) throw new Fail('the nether level picker is shown outside the nether', measures);
  await back();
  return { measures };
}

// Takes the view to where there is something drawn as a picture: a live
// entity if the world has one, else a marker the snapshot placed. Answers
// how many of each the dimension holds, and never where.
export async function goToSomething(page) {
  return page.evaluate(async () => {
    const app = window.mcmap;
    // Not before the stream has said who is here: a world asked too soon
    // looks like a quiet one.
    for (let n = 0; n < 80 && app.live() && !window.__probe.frame; n += 1) await new Promise((done) => setTimeout(done, 100));
    const frame = window.__probe.frame || { players: [], mobs: [] };
    const res = await fetch(`api/markers?dimension=${encodeURIComponent(app.dimension())}`, { cache: 'no-store' });
    const doc = res.ok ? await res.json() : {};
    const placed = [...(doc.containers || []), ...(doc.beds || [])];
    const at = (frame.players || [])[0] || (frame.mobs || [])[0] || placed[0];
    if (at) app.map.setView([at.z, at.x], 0, { animate: false });
    return { players: (frame.players || []).length, mobs: (frame.mobs || []).length, placed: placed.length };
  });
}

// Through every zoom level and back, each marker's picture stays the size
// it was made at: while the map is being zoomed and once it has settled.
export async function zoomSizes({ page }) {
  const there = await goToSomething(page);
  if (there.players + there.mobs + there.placed === 0) return skip('nothing in this dimension is drawn as a picture');
  await sleep(1500);
  const got = await page.evaluate(async () => {
    const map = window.mcmap.map;
    const P = window.__probe;
    const levels = [];
    for (let z = map.getMaxZoom(); z >= map.getMinZoom(); z -= 1) levels.push(z);
    for (let z = map.getMinZoom() + 1; z <= map.getMaxZoom(); z += 1) levels.push(z);
    const out = { levels: 0, samples: 0, worst: 1, off: 0, moving: 0 };
    const take = (moving) => {
      for (const { painted, made } of P.sizes()) {
        if (!(made > 0) || !(painted > 0)) continue;
        out.samples += 1;
        if (moving) out.moving += 1;
        const ratio = painted / made;
        if (Math.abs(Math.log(ratio)) > Math.abs(Math.log(out.worst))) out.worst = ratio;
        if (Math.abs(painted - made) > Math.max(1, made * 0.04)) out.off += 1;
      }
    };
    const frame = () => new Promise((done) => requestAnimationFrame(done));
    for (const z of levels) {
      map.setZoom(z);
      const end = performance.now() + 450;
      while (performance.now() < end) { await frame(); take(true); }
      await new Promise((done) => setTimeout(done, 250));
      take(false);
      out.levels += 1;
    }
    map.setZoom(0, { animate: false });
    return out;
  });
  const measures = { levels: got.levels, samples: got.samples, samples_zooming: got.moving, worst_ratio: Math.round(got.worst * 1000) / 1000, wrong_size: got.off };
  if (got.samples === 0) return skip('no marker picture was painted at any zoom', measures);
  if (got.off > 0) throw new Fail('a marker was painted at a size it was not made at', measures);
  return { measures };
}

// The layer panel opens, is made narrower and wider, folds to a rail and
// comes back, and the map takes the room each time.
export async function layerPanel({ page }) {
  const state = () => page.evaluate(() => {
    const el = document.getElementById('layers');
    return { open: el.classList.contains('open'), width: document.querySelector('#layers .panel').getBoundingClientRect().width, map: document.getElementById('map').getBoundingClientRect().width };
  });
  if (!(await state()).open) await page.locator('#layers-toggle').click();
  const first = await until(async () => { const s = await state(); return s.open ? s : null; });
  if (!first) throw new Fail('the layer panel did not open');
  const sizer = page.locator('.panel-sizer');
  if (await sizer.count() === 0) throw new Fail('the layer panel has no handle to size it by');
  await sizer.focus();
  for (let n = 0; n < 6; n += 1) await page.keyboard.press('ArrowLeft');
  await sleep(200);
  const wider = await state();
  for (let n = 0; n < 12; n += 1) await page.keyboard.press('ArrowRight');
  await sleep(200);
  const narrower = await state();
  if (Math.abs(wider.width - narrower.width) < 8) throw new Fail('sizing the layer panel did not change its width', { width_change: Math.abs(wider.width - narrower.width) });
  await page.locator('.panel-shut').click();
  const folded = await until(async () => { const s = await state(); return s.open ? null : s; });
  if (!folded) throw new Fail('the layer panel did not fold');
  if (folded.map <= narrower.map) throw new Fail('the map did not take the room a folded panel left');
  await page.locator('#layers-toggle').click();
  if (!(await until(async () => (await state()).open))) throw new Fail('the layer panel did not open again');
  return { measures: { width_change: Math.round(Math.abs(wider.width - narrower.width)), map_gain: Math.round(folded.map - narrower.map) } };
}

// In the page: the panel's rows, by the top row they are under.
const tree = () => {
  const rows = [];
  for (const top of document.querySelectorAll('#layers-list > li.node')) {
    const group = top.querySelector(':scope > .row .name').textContent;
    for (const li of [top, ...top.querySelectorAll('li.node')]) {
      const row = li.querySelector(':scope > .row');
      rows.push({
        li, group, depth: Number(/\bd(\d)\b/.exec(li.className)[1]), leaf: li.classList.contains('leaf'),
        off: li.classList.contains('unavailable'), check: row.querySelector('.check'), only: row.querySelector('.only'),
        on: row.querySelector('.check').getAttribute('aria-checked') === 'true', count: Number.parseInt(row.querySelector('.count').textContent, 10) || 0,
      });
    }
  }
  return rows;
};

// How many things are on the map now: every layer Leaflet holds.
const drawn = () => { let n = 0; window.mcmap.map.eachLayer(() => { n += 1; }); return n; };

async function toggleRow(page, group, pick) {
  const before = await page.evaluate(`(() => { const rows = (${tree})(); const row = rows.find((r) => r.group === ${JSON.stringify(group)} && (${pick})(r)); if (!row) return null; window.__row = row; const n = (${drawn})(); row.check.click(); return n; })()`);
  if (before === null) return null;
  await sleep(700);
  const during = await page.evaluate(`(() => { const off = window.__row.check.getAttribute('aria-checked') !== 'true'; return { off, n: (${drawn})() }; })()`);
  await page.evaluate(() => window.__row.check.click());
  await sleep(700);
  const after = await page.evaluate(`(${drawn})()`);
  return { before, during: during.n, off: during.off, after };
}

// Switching off a type of mob, a kind of structure and the biome overlay
// each changes what is drawn, and switching it back brings it back.
export async function toggles({ page }) {
  await goToSomething(page);
  await sleep(800);
  const measures = { toggled: 0, nothing_to_toggle: 0 };
  for (const [row, name, group, pick] of [
    [1, 'mob_type', 'Mobs', '(r) => r.depth >= 2 && r.count > 0 && r.on && !r.off'],
    [2, 'structure_kind', 'Structures', '(r) => r.depth === 2 && r.count > 0 && r.on && !r.off'],
  ]) {
    const got = await toggleRow(page, group, pick);
    if (got === null) { measures.nothing_to_toggle += 1; continue; }
    // Which row: 1 a type of mob, 2 a kind of structure.
    measures.row = row;
    if (!got.off) throw new Fail('pressing a row of the panel did not switch it off', measures);
    measures[`${name}_removed`] = got.before - got.during;
    if (got.during >= got.before) throw new Fail('switching a row off removed nothing from the map', measures);
    if (got.after <= got.during) throw new Fail('switching a row back on drew nothing', measures);
    measures.toggled += 1;
  }
  // The overlay is tiles: on, they are asked for; with one biome picked
  // out, they are asked for again as another picture.
  const biome = await page.evaluate(`(async () => {
    const rows = (${tree})();
    const top = rows.find((r) => r.group === 'Biomes' && r.depth === 1);
    const one = rows.find((r) => r.group === 'Biomes' && r.depth === 2 && r.only);
    if (!top || top.off || !one) return null;
    const tiles = () => [...document.querySelectorAll('#map img.leaflet-tile')].filter((img) => img.src.includes('api/biomes/tiles/'));
    const wait = (ms) => new Promise((done) => setTimeout(done, ms));
    const was = top.on;
    if (!was) top.check.click();
    await wait(1500);
    const whole = tiles().length;
    const picked = (list) => list.filter((img) => /[?&](biome|biomes|except)=/.test(img.src)).length;
    const plain = picked(tiles());
    one.only.click();
    await wait(1500);
    const alone = picked(tiles());
    one.only.click();
    await wait(300);
    if (!was) top.check.click();
    await wait(500);
    return { whole, plain, alone, left: was ? -1 : tiles().length };
  })()`);
  if (biome === null) { measures.nothing_to_toggle += 1; } else {
    measures.biome_tiles = biome.whole;
    if (biome.whole === 0) throw new Fail('switching the biome overlay on drew no tiles', measures);
    if (biome.alone <= biome.plain) throw new Fail('picking out one biome did not change the overlay', measures);
    if (biome.left > 0) throw new Fail('switching the biome overlay off left its tiles on the map', measures);
    measures.toggled += 1;
  }
  // The topography is tiles too, in one picture for however many of its
  // three drawings are on: all three on ask for each tile once, with the
  // three named, and off take them away.
  const land = await page.evaluate(`(async () => {
    const rows = (${tree})().filter((r) => r.group === 'Topography' && r.depth === 2);
    if (rows.length !== 3 || rows.some((r) => r.off || r.on)) return null;
    // Rows that are there may still have nothing to draw, and say why: the
    // heights are not read yet, or the map is wider than the widest tile.
    const said = await fetch('api/topography?dimension=' + encodeURIComponent(window.mcmap.dimension()), { cache: 'no-store' }).then((res) => (res.ok ? res.json() : null), () => null);
    if (!said || !said.offered || !said.ready || Math.round(window.mcmap.map.getZoom()) < said.tiles.minZoom) return null;
    const tiles = () => [...document.querySelectorAll('#map img.leaflet-tile')].filter((img) => img.src.includes('api/topography/tiles/'));
    const wait = (ms) => new Promise((done) => setTimeout(done, ms));
    rows[0].check.click();
    await wait(1200);
    const one = tiles().filter((img) => /[?&]styles=hillshade&/.test(img.src)).length;
    rows[1].check.click();
    rows[2].check.click();
    await wait(1500);
    const all = tiles();
    const three = all.filter((img) => /[?&]styles=hillshade,contours,tint&/.test(img.src)).length;
    for (const row of rows) row.check.click();
    await wait(800);
    return { one, total: all.length, three, left: tiles().length };
  })()`);
  if (land === null) { measures.nothing_to_toggle += 1; } else {
    measures.topography_tiles = land.total;
    if (land.one === 0 || land.total === 0) throw new Fail('switching the topography on drew no tiles', measures);
    if (land.three !== land.total) throw new Fail('with all three drawings on the map asked for tiles of other drawings too', measures);
    if (land.left > 0) throw new Fail('switching the topography off left its tiles on the map', measures);
    measures.toggled += 1;
  }
  if (measures.toggled === 0) return skip('no mobs, structures or biomes here to switch', measures);
  delete measures.row;
  return { measures };
}

// Hiding a height band takes what is in it off the map, and showing it
// again brings it back. The band with the most in it is the one pressed.
export async function heightBands({ page }) {
  await goToSomething(page);
  await sleep(800);
  const found = await page.evaluate(`(async () => {
    const wait = (ms) => new Promise((done) => setTimeout(done, ms));
    const row = (${tree})().find((r) => r.group === 'Height' && r.depth === 2);
    if (!row) return null;
    if (row.li.querySelector(':scope > .row .twist').getAttribute('aria-expanded') !== 'true') row.li.querySelector(':scope > .row .twist').click();
    await wait(600);
    const bands = (${tree})().filter((r) => r.group === 'Height' && r.depth === 3);
    const pick = bands.filter((r) => r.on && r.count > 0).sort((a, b) => b.count - a.count)[0];
    if (!pick) return { bands: bands.length, none: true };
    const before = (${drawn})();
    pick.check.click();
    await wait(800);
    const off = pick.check.getAttribute('aria-checked') !== 'true';
    const during = (${drawn})();
    pick.check.click();
    await wait(800);
    return { bands: bands.length, off, before, during, after: (${drawn})() };
  })()`);
  if (found === null) return skip('this page has no height bands');
  const measures = { bands: found.bands };
  if (found.none) throw new Fail('the panel lists height bands but none of them has anything in it', measures);
  if (!found.off) throw new Fail('pressing a height band did not switch it off', measures);
  measures.band_removed = found.before - found.during;
  if (found.during >= found.before) throw new Fail('switching a height band off removed nothing from the map', measures);
  if (found.after <= found.during) throw new Fail('switching a height band back on drew nothing', measures);
  return { measures };
}

// A search for something this world has finds it. What is searched for is
// a biome's or a structure's name taken from the page, never written down.
export async function search({ page }) {
  const term = await page.evaluate(`(() => { const rows = (${tree})(); const row = rows.find((r) => (r.group === 'Biomes' && r.depth === 2) || (r.group === 'Structures' && r.depth === 2 && r.count > 0)); return row ? row.li.querySelector('.name').textContent.slice(0, 5) : null; })()`);
  if (!term) return skip('no biome or structure here to search for');
  const began = Date.now();
  await page.evaluate(() => window.mcmap.search.focus());
  await page.locator('#search-box').fill(term);
  const hits = await until(() => page.evaluate(() => document.querySelectorAll('#search-results [role=option]').length), { timeoutMs: 8000 });
  const ms = Date.now() - began;
  await page.locator('#search-box').fill('');
  await page.keyboard.press('Escape');
  if (!hits) throw new Fail('a search for something the world holds found nothing', { answer_ms: ms });
  return { measures: { hits, answer_ms: ms } };
}

// A structure's sheet opens on a structure the world recorded, with what
// the server says of it and no standing, which is a player's own.
export async function structureSheet({ page }) {
  const asked = await page.evaluate(async () => {
    const dimension = window.mcmap.dimension();
    const res = await fetch(`api/structures?dimension=${encodeURIComponent(dimension)}&kinds=all`, { cache: 'no-store' });
    if (!res.ok) return { served: false };
    const first = ((await res.json()).recorded || [])[0];
    if (!first) return { served: true, none: true };
    const x = first.minX + Math.floor((first.maxX - first.minX) / 2);
    const z = first.minZ + Math.floor((first.maxZ - first.minZ) / 2);
    const detail = await fetch(`api/structures/detail?dimension=${encodeURIComponent(dimension)}&kind=${encodeURIComponent(first.kind)}&x=${x}&z=${z}`, { cache: 'no-store' });
    const said = detail.ok ? await detail.json() : {};
    window.mcmap.structures.show({ kind: first.kind, recorded: true, x, z, dimension });
    return { served: true, status: detail.status, standing: 'standing' in said };
  });
  if (!asked.served) return skip('structures are not served');
  if (asked.none) return skip('this dimension has no recorded structure');
  if (asked.status !== 200) throw new Fail('the detail of a listed structure was not given', { status: asked.status });
  if (asked.standing) throw new Fail('a structure\'s detail gave a service session a standing');
  const began = Date.now();
  const open = await until(() => page.evaluate(() => document.getElementById('structure').open && document.getElementById('structure-title').textContent.trim().length > 0 && document.getElementById('structure-body').children.length > 0));
  if (!open) throw new Fail('the structure sheet did not open with anything in it');
  const ms = Date.now() - began;
  await page.locator('#structure-close').click();
  if (!(await until(() => page.evaluate(() => !document.getElementById('structure').open)))) throw new Fail('the structure sheet did not close');
  return { measures: { open_ms: ms } };
}

// A saved view applies: the built-in one with no mobs takes the mobs off
// the map, and the one with everything puts them back. Views are kept in
// the browser.
export async function savedViews({ page }) {
  const mobsOn = () => page.evaluate(() => { const s = window.mcmap.layers.states(); return ['live/hostile', 'live/passive', 'live/villager', 'live/other'].filter((key) => s[key]).length; });
  const pick = async (id) => {
    if (!(await page.evaluate(() => document.getElementById('views').open))) await page.locator('#views-open').click();
    const button = page.locator(`#views-list button.view-pick[data-id="${id}"]`);
    if (await button.count() === 0) return false;
    await button.click();
    await sleep(500);
    return true;
  };
  if (!(await pick('everything'))) return skip('the built-in views are hidden in this browser');
  const before = await mobsOn();
  if (!(await pick('exploring'))) return skip('the built-in views are hidden in this browser');
  const during = await mobsOn();
  await pick('everything');
  const after = await mobsOn();
  // Whatever the view offered to keep is let go of: nothing is saved.
  await page.evaluate(() => { const back = document.getElementById('preview-back'); if (back && !document.getElementById('preview').hidden) back.click(); const views = document.getElementById('views'); if (views.open) views.close(); });
  const measures = { mob_layers_before: before, mob_layers_in_view: during, mob_layers_after: after };
  if (before === 0) throw new Fail('the view with everything in it shows no mobs', measures);
  if (during !== 0) throw new Fail('the view with no mobs left mobs switched on', measures);
  if (after !== before) throw new Fail('going back to the view with everything did not bring the mobs back', measures);
  return { measures };
}

// What a control is drawn in, as the browser paints it, in contrast to
// what is behind it, by WCAG 2's relative luminance: the text of what is
// chosen, a dimension and a nether level, and the text of a box typed in;
// and the edge of that box, a ticked checkbox's edge and its tick, and the
// ring of the focus. Each is found on the page, and one that is not there
// is not measured.
const drawnContrast = () => {
  const parse = (value) => {
    const n = (value.match(/[\d.]+/g) || []).map(Number);
    return { r: n[0] || 0, g: n[1] || 0, b: n[2] || 0, a: n.length > 3 ? n[3] : 1 };
  };
  const over = (top, under) => {
    const a = top.a + under.a * (1 - top.a);
    if (a === 0) return { r: 0, g: 0, b: 0, a: 0 };
    const mix = (k) => (top[k] * top.a + under[k] * under.a * (1 - top.a)) / a;
    return { r: mix('r'), g: mix('g'), b: mix('b'), a };
  };
  const ground = (node) => {
    const layers = [];
    for (let at = node; at; at = at.parentElement) {
      const c = parse(getComputedStyle(at).backgroundColor);
      layers.push(c);
      if (c.a >= 1) break;
    }
    return layers.reverse().reduce((under, top) => over(top, under), { r: 255, g: 255, b: 255, a: 1 });
  };
  const lum = (c) => {
    const lin = (v) => { const s = v / 255; return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4; };
    return 0.2126 * lin(c.r) + 0.7152 * lin(c.g) + 0.0722 * lin(c.b);
  };
  const ratio = (fg, bg) => {
    const [hi, lo] = [lum(over(fg, bg)), lum(bg)].sort((a, b) => b - a);
    return (hi + 0.05) / (lo + 0.05);
  };
  const visible = (node) => node && node.getClientRects().length > 0;
  const text = [];
  const edge = [];
  for (const node of [document.querySelector('#dimensions button[aria-pressed="true"]'), document.querySelector('.levels button[aria-pressed="true"]'), document.getElementById('search-box')]) {
    if (visible(node)) text.push(ratio(parse(getComputedStyle(node).color), ground(node)));
  }
  const field = document.getElementById('search-box');
  if (visible(field)) edge.push(ratio(parse(getComputedStyle(field).borderTopColor), ground(field.parentElement)));
  const check = [...document.querySelectorAll('#layers .check[aria-checked="true"]')].find(visible);
  if (check) {
    edge.push(ratio(parse(getComputedStyle(check).borderTopColor), ground(check.parentElement)));
    edge.push(ratio(parse(getComputedStyle(check, '::after').borderRightColor), ground(check)));
  }
  const focused = document.activeElement;
  if (visible(focused) && focused.matches(':focus-visible')) edge.push(ratio(parse(getComputedStyle(focused).outlineColor), ground(focused.parentElement)));
  return { text, edge };
};

// Each of the three themes is applied and the map is still drawn under it,
// and in each what is chosen is drawn at four and a half to one or more
// against what is behind it, and an edge, a tick or the ring of the focus
// at three to one or more.
export async function themes({ page }) {
  const was = await page.evaluate(() => window.mcmapSettings.get('look').theme);
  const home = await page.evaluate(() => window.mcmap.dimension());
  // The nether, at a level that is not its first, so that a level is drawn
  // chosen; and the layer panel open, so that a checkbox is drawn ticked.
  if (await page.evaluate(() => window.mcmap.go('nether', 0, 0))) {
    await until(() => page.evaluate(() => window.mcmap.dimension() === 'nether'));
    const levels = await page.evaluate(() => window.mcmap.level.list().map((l) => l.id));
    if (levels.length > 1) await page.evaluate((id) => window.mcmap.level.set(id), levels[1]);
  }
  if (!(await page.evaluate(() => document.getElementById('layers').classList.contains('open')))) await page.locator('#layers-toggle').click();
  await page.locator('#appearance-open').click();
  const seen = [];
  for (const theme of ['dark', 'light', 'contrast']) {
    await page.locator('#look-theme').selectOption(theme);
    await page.locator('#look-theme').focus();
    await page.keyboard.press('Shift+Tab');
    await sleep(300);
    seen.push({
      ...(await page.evaluate(() => ({
        theme: document.documentElement.dataset.theme,
        ground: getComputedStyle(document.body).backgroundColor,
        ink: getComputedStyle(document.body).color,
        tiles: document.querySelectorAll('#map img.leaflet-tile-loaded').length,
        canvas: document.querySelectorAll('#map canvas.leaflet-zoom-animated').length,
      }))),
      ...(await page.evaluate(drawnContrast)),
    });
  }
  await page.locator('#look-theme').selectOption(was);
  await page.locator('#appearance-close').click();
  await page.evaluate(() => window.mcmap.level.set('default'));
  await page.evaluate((id) => window.mcmap.go(id, 0, 0), home);
  const round = (n) => Math.round(n * 100) / 100;
  const measures = {
    themes: seen.length, distinct: new Set(seen.map((s) => `${s.ground}|${s.ink}`)).size,
    texts_measured: seen.reduce((n, s) => n + s.text.length, 0), edges_measured: seen.reduce((n, s) => n + s.edge.length, 0),
    lowest_text_ratio: round(Math.min(...seen.flatMap((s) => s.text))), lowest_edge_ratio: round(Math.min(...seen.flatMap((s) => s.edge))),
  };
  if (seen.some((s, n) => s.theme !== ['dark', 'light', 'contrast'][n])) throw new Fail('choosing a theme did not apply it', measures);
  if (measures.distinct !== 3) throw new Fail('two themes are drawn in the same colours', measures);
  if (seen.some((s) => s.tiles === 0 || s.canvas === 0)) throw new Fail('the map was not drawn under a theme', measures);
  if (measures.lowest_text_ratio < 4.5) throw new Fail('a control is written too faint to read under a theme', measures);
  if (measures.lowest_edge_ratio < 3) throw new Fail("a control's edge, its tick or the focus is drawn too faint under a theme", measures);
  return { measures };
}

// The map as it was: History opens, the newest day kept is drawn from that
// day's own tiles where today's were, at the same place and zoom, with that
// day's markers where it kept them and with no request for them where it
// did not, Compare draws today's beside it and takes them away again, and
// Now draws today's again. A map that keeps no days has
// nothing here to press.
export async function history({ page, cfg }) {
  const listed = await page.evaluate(async () => {
    const res = await fetch('api/history', { cache: 'no-store' });
    const days = res.ok ? (await res.json()).days : null;
    const newest = Array.isArray(days) && days.length > 0 ? days[days.length - 1] : null;
    const shown = window.mcmap.dimension();
    return {
      status: res.status, days: Array.isArray(days) ? days.length : 0,
      drawn: Boolean(newest && Array.isArray(newest.dimensions) && newest.dimensions.some((d) => d && d.id === shown && d.tiles > 0)),
      marked: Boolean(newest && Array.isArray(newest.dimensions) && newest.dimensions.some((d) => d && d.id === shown && d.markers === true)),
      // A page whose markers follow the day says which day they are of.
      follows: Boolean(window.mcmap.markers && window.mcmap.markers.day),
    };
  });
  if (listed.status === 404 && !cfg.keepsHistory) return skip('this map keeps no history');
  if (listed.status !== 200) throw new Fail('the days the map keeps were not listed', { status: listed.status });
  const measures = { days: listed.days, day_tiles: 0, day_pictures: 0, day_markers: 0 };
  if (listed.days === 0) return skip('no day has been kept yet', measures);
  // A day with no terrain of the dimension shown is drawn as nothing, which
  // is right and leaves no tile to wait for.
  if (!listed.drawn) return skip('the newest day kept nothing of the dimension shown', measures);

  const state = () => page.evaluate(() => {
    const part = (name) => document.querySelector(`.timeline-${name}`);
    const bar = document.querySelector('.timeline');
    if (!bar || bar.hidden) return { shown: false };
    // The scrubber is a slider over the days kept, with now its last step.
    return {
      shown: true, open: !part('body').hidden, at: Number(part('range').getAttribute('aria-valuenow')), last: Number(part('range').getAttribute('aria-valuemax')),
      past: document.body.classList.contains('past'), day: window.mcmap.past.get() !== '',
    };
  });
  // How far the view is from where it was when the check began, which is
  // kept in the page: where that is stays there.
  const moved = () => page.evaluate(() => {
    const map = window.mcmap.map;
    const was = window.__view;
    return { pixels: map.latLngToContainerPoint(was.centre).distanceTo(map.latLngToContainerPoint(map.getCenter())), zoom: Math.abs(map.getZoom() - was.zoom) };
  });
  // The terrain on the map by where it was fetched from: a day's tiles, and
  // today's. A picture counts once it has decoded.
  const terrain = () => page.evaluate(() => {
    const out = { day: 0, today: 0, coming: 0 };
    for (const img of document.querySelectorAll('#map img.leaflet-tile')) {
      const path = new URL(img.src, location.href).pathname;
      const kind = path.startsWith('/api/history/tiles/') ? 'day' : path.startsWith('/tiles/') ? 'today' : null;
      if (!kind) continue;
      if (img.classList.contains('leaflet-tile-loaded') && img.naturalWidth > 0) out[kind] += 1;
      else if (kind === 'day') out.coming += 1;
    }
    return out;
  });

  if (!(await until(async () => (await state()).shown, { timeoutMs: 5000 }))) throw new Fail('days are kept and the page shows no History', measures);
  let markersAsked = 0;
  const seen = (response) => {
    if (routeOf(response.url()) === '/api/history/markers') {
      markersAsked += 1;
      if (response.ok()) measures.day_markers += 1;
    }
    if (routeOf(response.url()) !== '/api/history/tiles') return;
    measures.day_tiles += 1;
    if (response.ok() && /^image\//.test(response.headers()['content-type'] || '')) measures.day_pictures += 1;
  };
  page.on('response', seen);
  try {
    if (!(await state()).open) await page.locator('.timeline-open').click();
    if (!(await until(async () => (await state()).open))) throw new Fail('the History row did not open', measures);
    const follow = page.locator('#inspect-follow');
    if (await follow.getAttribute('aria-pressed') === 'true') await follow.click();
    let still = 0;
    let previous = null;
    if (!(await until(async () => {
      const center = await page.evaluate(() => {
        const point = window.mcmap.map.project(window.mcmap.map.getCenter());
        return { x: point.x, y: point.y };
      });
      const moved = previous ? Math.hypot(center.x - previous.x, center.y - previous.y) : Infinity;
      previous = center;
      still = moved < 0.1 ? still + 1 : 0;
      return still >= 3;
    }, { timeoutMs: 5000 }))) throw new Fail('choosing a day moved the map', measures);
    await page.evaluate(() => { const map = window.mcmap.map; window.__view = { centre: map.getCenter(), zoom: map.getZoom() }; });

    // One back from now is the newest day kept.
    await page.locator('.timeline-earlier').click();
    const chosen = await until(async () => { const s = await state(); return s.past && s.day && s.at === s.last - 1 ? s : null; });
    if (!chosen) throw new Fail('stepping back from now did not choose the newest day', measures);
    const drawn = await until(async () => (measures.day_pictures > 0 && (await terrain()).day > 0), { timeoutMs: 20_000 });
    if (!drawn && measures.day_tiles === 0) throw new Fail('choosing a day asked for none of its tiles', measures);
    if (!drawn) throw new Fail('no tile of the day came as a picture', measures);
    // The markers over the day are the day's, which the page says once it
    // has drawn them; a day that kept none is not asked for any.
    if (listed.follows && listed.marked) {
      const theirs = await until(async () => measures.day_markers > 0 && (await page.evaluate(() => window.mcmap.markers.day() === window.mcmap.past.get())), { timeoutMs: 10_000 });
      if (!theirs) throw new Fail('the day kept its markers and the page did not draw them', measures);
    } else if (markersAsked > 0) {
      throw new Fail('the page asked for the markers of a day that kept none', measures);
    }
    const there = await moved();
    measures.moved_pixels = Math.round(there.pixels * 100) / 100;
    if (there.pixels > 1 || there.zoom > 0.001) throw new Fail('choosing a day moved the map', measures);

    // Compare puts today's terrain beside the day's, right of a divider,
    // and leaving it takes today's away again. A page from before there
    // was one has no button for it.
    if (await page.locator('.timeline-compare:not([hidden])').count() > 0) {
      await page.locator('.timeline-compare').click();
      const split = await until(async () => {
        const t = await terrain();
        const shown = await page.evaluate(() => window.mcmap.past.compare.on() && !document.querySelector('#map .timeline-divide').hidden && document.querySelector('#map .leaflet-beside-pane').style.clipPath !== '');
        return shown && t.day > 0 && t.today > 0;
      }, { timeoutMs: 20_000 });
      if (!split) throw new Fail('Compare did not draw now beside the day', measures);
      // Taken to the map's right edge, where the layer panel's own edge is
      // dragged, the divider is still caught by a press on what shows of
      // its grip, and the panel keeps its width.
      const divider = page.locator('.timeline-divider');
      await divider.focus();
      await page.keyboard.press('End');
      const box = await page.locator('#map').boundingBox();
      const grip = await page.locator('.timeline-grip').boundingBox();
      const right = Math.min(grip.x + grip.width, box.x + box.width);
      await page.mouse.move((Math.max(grip.x, box.x) + right) / 2, grip.y + grip.height / 2);
      await page.mouse.down();
      await page.mouse.move(box.x + box.width / 2, grip.y + grip.height / 2, { steps: 4 });
      await page.mouse.up();
      const freed = Number(await divider.getAttribute('aria-valuenow')) < 100 && (await page.locator('#map').boundingBox()).width === box.width;
      if (!freed) throw new Fail('the divider at the map\'s right edge could not be dragged back', measures);
      await page.locator('.timeline-compare').click();
      const whole = await until(async () => {
        const t = await terrain();
        return !(await page.evaluate(() => window.mcmap.past.compare.on())) && (await state()).day && t.today === 0 && t.day > 0;
      }, { timeoutMs: 20_000 });
      if (!whole) throw new Fail('leaving Compare left the map in two', measures);
      const beside = await moved();
      measures.moved_pixels = Math.max(measures.moved_pixels, Math.round(beside.pixels * 100) / 100);
      if (beside.pixels > 1 || beside.zoom > 0.001) throw new Fail('choosing a day moved the map', measures);
    }

    await page.locator('.timeline-now').click();
    // Today's tiles on the map, and the day's gone from under them.
    const back = await until(async () => {
      const s = await state();
      const t = await terrain();
      return !s.past && !s.day && s.at === s.last && t.today > 0 && t.day + t.coming === 0;
    }, { timeoutMs: 20_000 });
    if (!back) throw new Fail('going back to now left the map on the day', measures);
    const after = await moved();
    measures.moved_pixels = Math.max(measures.moved_pixels, Math.round(after.pixels * 100) / 100);
    if (after.pixels > 1 || after.zoom > 0.001) throw new Fail('going back to now moved the map', measures);

    await page.locator('.timeline-close').click();
    if (!(await until(async () => !(await state()).open))) throw new Fail('the History row did not close', measures);
    return { measures };
  } finally {
    page.off('response', seen);
    // Whatever went wrong, the checks after this one are of the map as
    // it is now.
    await page.evaluate(() => { const now = document.querySelector('.timeline-now'); if (now && !now.disabled) now.click(); }).catch(() => {});
  }
}

// On a phone the panel is a sheet from the bottom edge, as wide as the
// screen, that changes height when its handle is pressed.
export async function phoneSheet({ browser, cfg, watch, closers = [] }) {
  const ctx = await openContext(browser, { cfg, watch, cookie: cfg.session.cookie, viewport: { width: 390, height: 780 }, dpr: 2, touch: true });
  closers.push(() => ctx.close());
  try {
    const page = await openMap(ctx, cfg);
    const state = () => page.evaluate(() => {
      const el = document.getElementById('layers');
      const box = document.querySelector('#layers .panel').getBoundingClientRect();
      return { open: el.classList.contains('open'), detent: (/detent-(\w+)/.exec(document.querySelector('#layers .panel').className + ' ' + el.className) || [])[1] || '', width: box.width, height: box.height, bottom: box.bottom, view: window.innerHeight, across: window.innerWidth };
    });
    if (!(await state()).open) await page.locator('#layers-toggle').tap();
    const up = await until(async () => { const s = await state(); return s.open && s.height > 40 ? s : null; });
    if (!up) throw new Fail('the layer sheet did not open on a phone');
    // The page's own bar is under it, so the sheet's foot is a bar's
    // height above the screen's and no more.
    if (Math.abs(up.width - up.across) > 2 || up.view - up.bottom > 80 || up.bottom > up.view + 2) throw new Fail('the layer sheet is not across the bottom of the screen', { width: Math.round(up.width), bottom_gap: Math.round(up.view - up.bottom) });
    const heights = new Set([Math.round(up.height)]);
    for (let n = 0; n < 3; n += 1) {
      await page.locator('.panel-grab').tap();
      await sleep(400);
      heights.add(Math.round((await state()).height));
    }
    if (heights.size < 2) throw new Fail('pressing the sheet\'s handle did not change its height');
    const tiles = await page.evaluate(() => document.querySelectorAll('#map img.leaflet-tile-loaded').length);
    if (tiles === 0) throw new Fail('the map drew no terrain on a phone');
    return { measures: { heights: heights.size, tiles } };
  } finally {
    await ctx.close();
  }
}

// The bar is one row on a wide screen, with every control in it on the
// screen, as large as anything pressed is, and doing what it is for when
// pressed; on a phone each of them is in More, at the size a finger needs.
// Log out is measured and never pressed.
export async function pageBar({ browser, cfg, watch, closers = [] }) {
  const IDS = ['search-box', 'goto-x', 'goto-z', 'grid', 'live-pause', 'views-open', 'appearance-open', 'copy', 'help-open', 'logout'];
  const seen = (page, scope) => page.evaluate(([ids, within]) => {
    const hit = parseFloat(getComputedStyle(document.documentElement).getPropertyValue('--hit'));
    const out = { controls: 0, off_screen: 0, too_small: 0, tops: [] };
    const tabs = [...document.querySelectorAll('#dimensions button')];
    for (const node of [...tabs, ...ids.map((id) => document.getElementById(id))]) {
      // A control its own script has not offered, as the live layer's
      // pause on a map with none, is not on the page to measure.
      if (!node || node.hidden || node.closest('[hidden]') || (within && !node.closest(within))) continue;
      out.controls += 1;
      const box = node.getBoundingClientRect();
      const style = getComputedStyle(node);
      if (box.width === 0 || style.visibility === 'hidden' || box.left < 0 || box.right > innerWidth + 0.5 || box.top < 0 || box.bottom > innerHeight + 0.5) out.off_screen += 1;
      // A checkbox is pressed through its label, which is what is measured.
      const pressed = node.type === 'checkbox' && node.closest('label') ? node.closest('label').getBoundingClientRect() : box;
      if (pressed.width < hit - 0.5 || pressed.height < hit - 0.5) out.too_small += 1;
      out.tops.push(Math.round(box.top + box.height / 2));
    }
    return out;
  }, [IDS, scope]);
  const shut = (page, id) => page.evaluate((dialog) => { const node = document.getElementById(dialog); if (node.open) node.close(); }, id);
  const opens = async (page, button, dialog) => {
    await page.locator(`#${button}`).click();
    const open = await until(() => page.evaluate((id) => document.getElementById(id).open, dialog), { timeoutMs: 3000 });
    await shut(page, dialog);
    return Boolean(open);
  };

  const wideCtx = await openContext(browser, { cfg, watch, cookie: cfg.session.cookie, viewport: { width: 1440, height: 900 } });
  closers.push(() => wideCtx.close());
  const measures = {};
  let outside = 0;
  try {
    const page = await openMap(wideCtx, cfg);
    await until(() => page.evaluate(() => !document.getElementById('appearance-open').hidden && !document.getElementById('views-open').hidden));
    const wide = await seen(page, null);
    // Everything but the dimensions and the search box, which keep their
    // own places on a phone.
    outside = wide.controls - (await page.evaluate(() => document.querySelectorAll('#dimensions button').length)) - 1;
    const centres = [...new Set(wide.tops)].sort((a, b) => a - b);
    measures.controls = wide.controls;
    measures.rows = centres.length === 0 ? 0 : 1 + centres.filter((y, n) => n > 0 && y - centres[n - 1] > 16).length;
    measures.off_screen = wide.off_screen;
    measures.too_small = wide.too_small;
    if (wide.off_screen > 0) throw new Fail('a control in the bar is off the screen', measures);
    if (wide.too_small > 0) throw new Fail('a control in the bar is smaller than anything pressed should be', measures);
    if (measures.rows > 1) throw new Fail('the bar takes more than one row on a wide screen', measures);
    let working = 0;
    if (await opens(page, 'help-open', 'help')) working += 1;
    if (await opens(page, 'appearance-open', 'appearance')) working += 1;
    if (await opens(page, 'views-open', 'views')) working += 1;
    // Copy link answers in a word beside its picture, whether or not the
    // browser let it at the clipboard.
    await page.locator('#copy').click();
    if (await until(() => page.evaluate(() => document.getElementById('copy').classList.contains('told') && document.getElementById('copy-word').getBoundingClientRect().width > 1), { timeoutMs: 3000 })) working += 1;
    measures.working = working;
    if (working < 4) throw new Fail('a control in the bar did not do what it is for', measures);
  } finally {
    await wideCtx.close();
  }

  const phoneCtx = await openContext(browser, { cfg, watch, cookie: cfg.session.cookie, viewport: { width: 390, height: 844 }, dpr: 2, touch: true });
  closers.push(() => phoneCtx.close());
  try {
    const page = await openMap(phoneCtx, cfg);
    await until(() => page.evaluate(() => !document.getElementById('appearance-open').hidden));
    await page.locator('#more-open').tap();
    if (!(await until(() => page.evaluate(() => document.body.classList.contains('more-shown'))))) throw new Fail('a control in the bar is not in More on a phone', measures);
    const more = await seen(page, '#more');
    measures.in_more = more.controls;
    if (more.controls < outside) throw new Fail('a control in the bar is not in More on a phone', measures);
    if (more.too_small > 0) throw new Fail('a control in the bar is smaller than anything pressed should be', { ...measures, too_small: more.too_small });
    await page.locator('#help-open').tap();
    if (!(await until(() => page.evaluate(() => document.getElementById('help').open), { timeoutMs: 3000 }))) throw new Fail('a control in the bar did not do what it is for', measures);
    await shut(page, 'help');
    return { measures };
  } finally {
    await phoneCtx.close();
  }
}
