// The map and its controls, used as a visitor uses them. Everything
// pressed here changes only the browser's own settings, which are thrown
// away with the browser: nothing a visitor can press on this page writes
// to the server but the login and the logout, and neither is touched.

import { Fail, skip, sleep, until } from '../lib/check.mjs';
import { openContext, openMap } from '../lib/browser.mjs';

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
  if (measures.toggled === 0) return skip('no mobs, structures or biomes here to switch', measures);
  delete measures.row;
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

// Each of the three themes is applied and the map is still drawn under it.
export async function themes({ page }) {
  const was = await page.evaluate(() => window.mcmapSettings.get('look').theme);
  await page.locator('#appearance-open').click();
  const seen = [];
  for (const theme of ['dark', 'light', 'contrast']) {
    await page.locator('#look-theme').selectOption(theme);
    await sleep(300);
    seen.push(await page.evaluate(() => ({
      theme: document.documentElement.dataset.theme,
      ground: getComputedStyle(document.body).backgroundColor,
      ink: getComputedStyle(document.body).color,
      tiles: document.querySelectorAll('#map img.leaflet-tile-loaded').length,
      canvas: document.querySelectorAll('#map canvas.leaflet-zoom-animated').length,
    })));
  }
  await page.locator('#look-theme').selectOption(was);
  await page.locator('#appearance-close').click();
  const measures = { themes: seen.length, distinct: new Set(seen.map((s) => `${s.ground}|${s.ink}`)).size };
  if (seen.some((s, n) => s.theme !== ['dark', 'light', 'contrast'][n])) throw new Fail('choosing a theme did not apply it', measures);
  if (measures.distinct !== 3) throw new Fail('two themes are drawn in the same colours', measures);
  if (seen.some((s) => s.tiles === 0 || s.canvas === 0)) throw new Fail('the map was not drawn under a theme', measures);
  return { measures };
}

// On a phone the panel is a sheet from the bottom edge, as wide as the
// screen, that changes height when its handle is pressed.
export async function phoneSheet({ browser, cfg, watch }) {
  const ctx = await openContext(browser, { cfg, watch, cookie: cfg.session.cookie, viewport: { width: 390, height: 780 }, dpr: 2, touch: true });
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
