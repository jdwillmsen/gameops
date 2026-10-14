'use strict';

// What the nether's levels do, checked by doing it: app.js, the record's
// script, the picker's script and the views' are run in a page of the kind
// dom.cjs makes,
// on a map that keeps Leaflet's rules for a tile layer's loading, against a
// server that lists the levels it has drawn. Run by `go test ./web/` where
// there is a node to run it with, or by hand:
// `node web/testdata/levels.test.cjs`.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { page } = require('./dom.cjs');
const { leaflet } = require('./leaflet.cjs');

// What the page's script makes comes from another realm, which strict
// deep equality would tell apart from a literal that looks the same.
const same = (got, want, said) => assert.equal(JSON.stringify(got), JSON.stringify(want), said);
const tick = () => new Promise((done) => setTimeout(done, 0));
const tests = [];
const test = (name, fn) => tests.push([name, fn]);

const EXTENT = { rendered: true, renderedAt: '2026-10-14T09:00:00Z', format: 'webp', minZoom: -5, maxZoom: 0, minRegionX: -4, maxRegionX: 3, minRegionZ: -4, maxRegionZ: 3 };
const LEVELS = [{ id: 'default', y: 100, label: 'Top' }, { id: '66', y: 66, label: 'Mid' }, { id: '34', y: 34, label: 'Low' }];
const THEN = { day: '2026-09-01', dimensions: [{ id: 'nether', format: 'webp', minZoom: -2, maxZoom: 0, minRegionX: 0, maxRegionX: 0, minRegionZ: 0, maxRegionZ: 0 }] };
const KEY = 'mcmap.settings';
const ORIGIN = 'http://map.test';
// A time so many minutes before now, as the server writes one.
const minutesAgo = (n) => new Date(Date.now() - n * 60_000).toISOString();
const drawnAt = (levels, times) => levels.map((l) => ({ ...l, renderedAt: times[l.id] }));

// A page opened at the place its address names. levels is what the server
// says the nether has, storage what the browser already holds, and link a
// view the address carries after its place.
async function load(hash, { levels = LEVELS, storage = new Map(), link = '' } = {}) {
  const p = page(storage, { files: ['settings.js'] });
  const { win, doc } = p;
  const found = doc.getElementById;
  const asked = new Map();
  doc.getElementById = (id) => {
    if (found(id)) return found(id);
    if (!asked.has(id)) asked.set(id, doc.createElement('div'));
    return asked.get(id);
  };
  doc.getElementById('login').hidden = true;
  doc.hidden = false;
  // The views' list hides the two boxes of its form by their labels.
  for (const id of ['views-place', 'views-look']) doc.createElement('label').append(doc.getElementById(id));
  const dialog = doc.getElementById('views');
  dialog.showModal = () => { dialog.open = true; };
  dialog.close = () => { dialog.open = false; };
  const { L, map } = leaflet();
  const state = { levels, renderedAt: EXTENT.renderedAt };
  Object.assign(win, {
    L,
    location: { hash, href: `${ORIGIN}/${hash}${link ? `//${link}` : ''}`, origin: ORIGIN, pathname: '/', search: '' },
    URL,
    TextEncoder,
    TextDecoder,
    Uint8Array,
    btoa,
    atob,
    history: { replaceState(_, __, to) { win.location.hash = to; } },
    navigator: {},
    setInterval: () => 0,
    clearInterval() {},
    fetch: async (address) => {
      const nether = { id: 'nether', ...EXTENT, renderedAt: state.renderedAt, ...(state.levels ? { levels: state.levels } : {}) };
      const body = address === 'api/map' ? { world: 'w', refreshSeconds: 600, dimensions: [{ id: 'overworld', ...EXTENT }, nether] } : {};
      return { ok: true, status: 200, headers: { get: () => '' }, json: async () => body, text: async () => JSON.stringify(body) };
    },
  });
  for (const file of ['app.js', 'layers.js', 'levels.js', 'views.js']) vm.runInContext(fs.readFileSync(path.join(__dirname, '..', file), 'utf8'), win, { filename: file });
  const settle = async () => { for (let i = 0; i < 6; i++) await tick(); };
  await settle();
  const terrain = () => [...map.layers].filter((l) => l.options.dimension);
  const bar = doc.getElementById('map').querySelector('.levels');
  const buttons = () => bar.children.map((b) => b.textContent);
  const pressed = () => bar.children.filter((b) => b.getAttribute('aria-pressed') === 'true').map((b) => b.textContent);
  const press = async (label) => {
    bar.children.find((b) => b.textContent === label).click();
    await settle();
  };
  const pick = (label) => win.mcmap.level.set(bar.children.find((b) => b.textContent === label).dataset.id);
  const url = (layer) => layer.getTileUrl({ z: -1, x: 2, y: 3 });
  const kept = () => JSON.parse(storage.get(KEY)).levels;
  const status = () => doc.getElementById('status').textContent;
  const click = async (id) => {
    doc.getElementById(id).click();
    await settle();
  };
  return { ...p, app: win.mcmap, map, state, settle, terrain, bar, buttons, pressed, press, pick, url, kept, status, click, storage };
}

// Long enough for tiles to have faded in: the page's waits are cut to a
// moment by dom.cjs.
const faded = () => new Promise((done) => setTimeout(done, 30));

test('the default level is drawn from the addresses there have always been', async () => {
  const p = await load('#nether/100/200/-1');
  assert.equal(p.url(p.terrain()[0]), 'tiles/nether/-1/2/3.webp');
});

test('in the nether with several levels there is a labelled row of buttons, the default one pressed', async () => {
  const p = await load('#nether/100/200/-1');
  assert.equal(p.bar.hidden, false);
  assert.equal(p.bar.getAttribute('role'), 'group');
  assert.ok(p.bar.getAttribute('aria-label'));
  same(p.buttons(), ['Top', 'Mid', 'Low']);
  same(p.pressed(), ['Top']);
  assert.ok(p.bar.children.every((b) => b.tagName === 'BUTTON' && b.tabIndex === 0), 'each is a button, reached by the keyboard');
});

test('choosing a level draws it from its own addresses, where the map was, and presses its button', async () => {
  const p = await load('#nether/-1500/1200/-3');
  p.terrain()[0].arrive();
  await p.press('Mid');
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp?level=66');
  same(p.map.getCenter(), { lat: 1200, lng: -1500 });
  assert.equal(p.map.getZoom(), -3);
  same(p.pressed(), ['Mid']);
  assert.equal(p.app.level.get(), '66');
  await p.press('Top');
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp');
});

test('the choice is kept, and the nether opens on it next time', async () => {
  const p = await load('#nether/100/200/-1');
  await p.press('Low');
  same(p.kept(), { nether: '34' });
  const again = await load('#nether/100/200/-1', { storage: p.storage });
  assert.equal(again.url(again.terrain()[0]), 'tiles/nether/-1/2/3.webp?level=34');
  same(again.pressed(), ['Low']);
  await again.press('Top');
  same(again.kept(), { nether: null });
});

test('the old version of the record is read, and a later one is left as it was', async () => {
  const old = await load('#nether/100/200/-1', { storage: new Map([[KEY, JSON.stringify({ v: 2, grid: { on: true } })]]) });
  same(old.pressed(), ['Top']);
  const later = JSON.stringify({ v: 99, levels: { nether: '66' } });
  const p = await load('#nether/100/200/-1', { storage: new Map([[KEY, later]]) });
  await p.press('Low');
  assert.equal(p.storage.get(KEY), later, 'a record of a later page is not written over');
});

test('a level that is kept but is no longer offered draws the default one', async () => {
  const p = await load('#nether/100/200/-1', { storage: new Map([[KEY, JSON.stringify({ v: 3, levels: { nether: '12' } })]]) });
  assert.equal(p.url(p.terrain()[0]), 'tiles/nether/-1/2/3.webp');
  same(p.pressed(), ['Top']);
});

test('no picker outside the nether, and it is there again on coming back', async () => {
  const p = await load('#overworld/100/200/-1');
  assert.equal(p.bar.hidden, true);
  same(p.app.level.list(), []);
  p.app.go('nether', 10, 20);
  await p.settle();
  assert.equal(p.bar.hidden, false);
  p.app.go('overworld', 10, 20);
  await p.settle();
  assert.equal(p.bar.hidden, true);
});

test('a level chosen in another dimension is not drawn there, and is drawn in the nether', async () => {
  const p = await load('#overworld/100/200/-1');
  assert.equal(p.app.level.set('66'), true, 'kept for the nether');
  assert.equal(p.url(p.terrain()[0]), 'tiles/overworld/-1/2/3.webp');
  p.app.go('nether', 10, 20);
  await p.settle();
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp?level=66');
});

test('no picker where the server offers one level, or none', async () => {
  for (const levels of [null, [], [LEVELS[0]]]) {
    const p = await load('#nether/100/200/-1', { levels });
    assert.equal(p.bar.hidden, true, JSON.stringify(levels));
    assert.equal(p.app.level.set('66'), false);
    assert.equal(p.url(p.terrain()[0]), 'tiles/nether/-1/2/3.webp');
  }
});

test('a level the server does not offer cannot be chosen', async () => {
  const p = await load('#nether/100/200/-1');
  for (const id of ['12', '', '../overworld', undefined, 66]) assert.equal(p.app.level.set(id), false, String(id));
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp');
  assert.equal(p.storage.get(KEY) === undefined || JSON.parse(p.storage.get(KEY)).levels.nether === null, true);
});

test('while a past day is shown the picker is away and the day is of the default cut, and the choice returns after it', async () => {
  const p = await load('#nether/100/200/-1');
  await p.press('Mid');
  p.terrain().at(-1).arrive();
  p.app.past.set(THEN);
  await p.settle();
  assert.equal(p.bar.hidden, true);
  const layer = p.terrain().at(-1);
  assert.equal(layer.getTileUrl({ z: -1, x: 2, y: 3 }), 'api/history/tiles/2026-09-01/nether/-1/2/3.webp');
  same(p.app.level.list(), []);
  assert.equal(p.app.level.set('34'), true, 'a choice made now is kept for after the day, and nothing is redrawn');
  assert.equal(p.terrain().at(-1), layer);
  p.app.past.set(null);
  await p.settle();
  assert.equal(p.bar.hidden, false);
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp?level=34');
  same(p.pressed(), ['Low']);
});

test('stepping through levels fast keeps one picture under the one coming, and never an empty map', async () => {
  const p = await load('#nether/100/200/-1');
  const [top] = p.terrain();
  top.arrive();
  p.pick('Mid');
  const mid = p.terrain().at(-1);
  p.pick('Low');
  const low = p.terrain().at(-1);
  assert.equal(p.map.hasLayer(mid), false, 'a level passed over must not go on asking for its tiles');
  assert.equal(p.map.hasLayer(top), true, 'what was there stays until what is wanted has come');
  p.pick('Top');
  const again = p.terrain().at(-1);
  assert.equal(p.map.hasLayer(low), false);
  assert.equal(p.map.hasLayer(top), true);
  assert.equal(p.terrain().length, 2, 'the one under and the one coming, and no more');
  again.arrive();
  await faded();
  assert.equal(p.terrain().length, 1);
  assert.equal(p.terrain()[0], again);
});

test('a level whose tiles never come does not keep the old ones for ever', async () => {
  const p = await load('#nether/100/200/-1');
  p.terrain()[0].arrive();
  p.pick('Mid');
  assert.equal(p.terrain().length, 2);
  await faded();
  assert.equal(p.terrain().length, 1);
});

test('the place carries the level, and takes the map to it', async () => {
  const p = await load('#nether/100/200/-1');
  assert.equal(p.app.place.get().level, undefined, 'the default cut is the absence of a level');
  await p.press('Mid');
  same(p.app.place.get(), { d: 'nether', x: 100, z: 200, zoom: -1, level: '66' });
  await p.press('Top');
  assert.equal(p.app.place.set({ d: 'nether', x: 10, z: 20, zoom: -2, level: '34' }), true);
  await p.settle();
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp?level=34');
  same(p.pressed(), ['Low']);
  // A place without a level is the default cut, and one for another
  // dimension leaves the choice alone.
  p.app.place.set({ d: 'nether', x: 10, z: 20, zoom: -2 });
  await p.settle();
  same(p.pressed(), ['Top']);
  p.app.place.set({ d: 'nether', x: 10, z: 20, zoom: -2, level: '66' });
  p.app.place.set({ d: 'overworld', x: 10, z: 20, zoom: -2 });
  await p.settle();
  assert.equal(p.app.level.get(), '66');
});

test('a saved view and a link may name the level, and nothing else of the kind', async () => {
  const p = await load('#nether/100/200/-1');
  const view = (level) => ({ name: 'v', layers: {}, place: { d: 'nether', x: 1, z: 2, zoom: 0, level } });
  assert.equal(p.settings.check.view(view('66')).place.level, '66');
  for (const bad of ['', '1234', '../x', '6 6', 66, null]) assert.equal(p.settings.check.view(view(bad)), null, JSON.stringify(bad));
  assert.equal(p.settings.check.record({ v: 3, levels: { nether: '34' } }).levels.nether, '34');
  assert.equal(p.settings.check.record({ v: 3, levels: { nether: 'x' } }), null);
});

// The levels are rendered after the dimensions, so the nether's own time
// changes before a level's tiles do: a layer rebuilt then would hold the
// level as it was a cycle ago, with nothing to say it should be asked again.
test('a level is drawn again when its own render lands, and not when only the nether\'s has', async () => {
  const levels = drawnAt(LEVELS, { default: minutesAgo(200), 66: minutesAgo(190), 34: minutesAgo(190) });
  const p = await load('#nether/100/200/-1', { levels });
  await p.press('Mid');
  const first = p.terrain().at(-1);
  first.arrive();
  await faded();

  p.state.renderedAt = minutesAgo(20);
  p.state.levels = drawnAt(LEVELS, { default: p.state.renderedAt, 66: levels[1].renderedAt, 34: levels[2].renderedAt });
  await p.app.reload();
  await p.settle();
  assert.equal(p.terrain().at(-1), first, 'the level has not been rendered again yet');

  const landed = minutesAgo(10);
  p.state.levels = drawnAt(LEVELS, { default: p.state.renderedAt, 66: landed, 34: levels[2].renderedAt });
  await p.app.reload();
  await p.settle();
  const second = p.terrain().at(-1);
  assert.notEqual(second, first, 'the level\'s new tiles are asked for');
  assert.equal(p.url(second), 'tiles/nether/-1/2/3.webp?level=66');

  second.arrive();
  await faded();
  p.state.levels = drawnAt(LEVELS, { default: p.state.renderedAt, 66: landed, 34: minutesAgo(5) });
  await p.app.reload();
  await p.settle();
  assert.equal(p.terrain().at(-1), second, 'another level\'s render is not this one\'s');
});

test('the default cut is still drawn again when the nether is rendered', async () => {
  const p = await load('#nether/100/200/-1', { levels: drawnAt(LEVELS, { default: minutesAgo(200), 66: minutesAgo(190), 34: minutesAgo(190) }) });
  const first = p.terrain().at(-1);
  first.arrive();
  await faded();
  p.state.renderedAt = minutesAgo(20);
  await p.app.reload();
  await p.settle();
  assert.notEqual(p.terrain().at(-1), first);
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp');
});

test('the status line says when the level drawn was rendered', async () => {
  const p = await load('#nether/100/200/-1', { levels: drawnAt(LEVELS, { default: minutesAgo(180), 66: minutesAgo(10) }) });
  p.state.renderedAt = minutesAgo(180);
  await p.app.reload();
  await p.settle();
  assert.equal(p.status(), 'updated 3 h ago');
  await p.press('Mid');
  assert.equal(p.status(), 'updated 10 min ago');
  await p.press('Low');
  assert.equal(p.status(), 'rendered before the last restart', 'a level with no time known does not borrow the nether\'s');
  await p.press('Top');
  assert.equal(p.status(), 'updated 3 h ago');
});

test('a level taken away while it is drawn gives way to the default cut, and comes back when it does', async () => {
  const p = await load('#nether/100/200/-1');
  await p.press('Mid');
  p.terrain().at(-1).arrive();
  await faded();

  p.state.levels = [LEVELS[0], LEVELS[2]];
  await p.app.reload();
  await p.settle();
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp', 'a level that is not offered is not asked for');
  same(p.buttons(), ['Top', 'Low']);
  same(p.pressed(), ['Top']);

  p.terrain().at(-1).arrive();
  await faded();
  p.state.levels = null;
  await p.app.reload();
  await p.settle();
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp');
  assert.equal(p.bar.hidden, true);

  p.state.levels = LEVELS;
  await p.app.reload();
  await p.settle();
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp?level=66', 'the choice was kept for when it is offered again');
  same(p.pressed(), ['Mid']);
});

// A link's view as the page itself writes one, for a page to open with.
async function linkOf(place) {
  const p = await load('#overworld/0/0/0');
  return p.app.views.pack({ name: 'shared', layers: {}, place });
}

test('leaving the preview of a link\'s view puts back the level that was drawn', async () => {
  const link = await linkOf({ d: 'nether', x: 1, z: 2, zoom: 0, level: '34' });
  const storage = new Map([[KEY, JSON.stringify({ v: 3, levels: { nether: '66' } })]]);
  const p = await load('#nether/100/200/-1', { storage, link });
  same(p.pressed(), ['Mid']);

  await p.click('views-offer-show');
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp?level=34', 'the view is previewed at its own level');
  same(p.pressed(), ['Low']);

  await p.click('preview-back');
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp?level=66');
  same(p.pressed(), ['Mid']);
  assert.equal(p.app.level.get(), '66');
  assert.equal(p.settings.get('levels').nether, '66', 'what is drawn is what is kept');
});

// A record whose default view was saved in the nether on a level, beside
// the level the picker last kept.
const opensOn = (level, chosen) => JSON.stringify({
  v: 3,
  levels: { nether: chosen },
  views: { list: [{ id: 'home', name: 'home', layers: {}, place: { d: 'nether', x: 10, z: 20, zoom: -1, ...(level ? { level } : {}) } }], order: ['everything', 'exploring', 'base', 'home'], start: 'home', hidden: [], active: null },
});

test('a default view saved on a level opens on it, and that level is then the one kept', async () => {
  const p = await load('', { storage: new Map([[KEY, opensOn('34', '66')]]) });
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp?level=34');
  same(p.pressed(), ['Low']);
  same(p.kept(), { nether: '34' });

  const top = await load('', { storage: new Map([[KEY, opensOn('', '66')]]) });
  assert.equal(top.url(top.terrain().at(-1)), 'tiles/nether/-1/2/3.webp');
  same(top.kept(), { nether: null });

  // An address that names a place is not the default view's place, and
  // leaves the level kept as it was.
  const named = await load('#nether/100/200/-1', { storage: new Map([[KEY, opensOn('34', '66')]]) });
  assert.equal(named.url(named.terrain().at(-1)), 'tiles/nether/-1/2/3.webp?level=66');
  same(named.pressed(), ['Mid']);
});

test('leaving a preview puts back the level a default view opened on', async () => {
  const link = await linkOf({ d: 'nether', x: 1, z: 2, zoom: 0, level: '66' });
  const p = await load('#-/0/0/0', { storage: new Map([[KEY, opensOn('34', '66')]]), link });
  same(p.pressed(), ['Low']);
  await p.click('views-offer-show');
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp?level=66');
  await p.click('preview-back');
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp?level=34');
  same(p.pressed(), ['Low']);
  assert.equal(p.settings.get('levels').nether, '34', 'what is drawn is what is kept');
});

test('a preview of a view with no level is left for the level that was drawn, too', async () => {
  const link = await linkOf({ d: 'nether', x: 1, z: 2, zoom: 0 });
  const storage = new Map([[KEY, JSON.stringify({ v: 3, levels: { nether: '66' } })]]);
  const p = await load('#nether/100/200/-1', { storage, link });
  await p.click('views-offer-show');
  same(p.pressed(), ['Top']);
  await p.click('preview-back');
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp?level=66');
  same(p.pressed(), ['Mid']);
});

test('a preview begun in another dimension does not leave its level behind in the nether', async () => {
  const link = await linkOf({ d: 'nether', x: 1, z: 2, zoom: 0, level: '34' });
  const storage = new Map([[KEY, JSON.stringify({ v: 3, levels: { nether: '66' } })]]);
  const p = await load('#overworld/100/200/-1', { storage, link });
  await p.click('views-offer-show');
  same(p.pressed(), ['Low']);
  await p.click('preview-back');
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/overworld/-1/2/3.webp');
  p.app.go('nether', 10, 20);
  await p.settle();
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp?level=66');
  same(p.pressed(), ['Mid']);
});

test('a record put in place of the page\'s own brings its level with it', async () => {
  const p = await load('#nether/100/200/-1');
  assert.equal(p.settings.replace({ v: 3, levels: { nether: '34' } }), true);
  await p.settle();
  assert.equal(p.url(p.terrain().at(-1)), 'tiles/nether/-1/2/3.webp?level=34');
  same(p.pressed(), ['Low']);
});

// The link's form is the README's: a place's level is `k`, beside the
// place and never without one, and a height of one to three digits.
test('a link carries a view\'s level as k, and one that is not a level beside a place is refused', async () => {
  const p = await load('#nether/100/200/-1');
  const { pack, unpack } = p.app.views;
  const part = (raw) => `v1.${Buffer.from(JSON.stringify(raw)).toString('base64url')}`;
  const sent = (packed) => JSON.parse(Buffer.from(packed.slice(3), 'base64url').toString());
  const place = { d: 'nether', x: 1, z: 2, zoom: 0 };

  const packed = pack({ name: 'v', layers: {}, place: { ...place, level: '66' } });
  assert.equal(sent(packed).k, '66');
  same(unpack(packed).view.place, { ...place, level: '66' });
  assert.equal(Object.hasOwn(sent(pack({ name: 'v', layers: {}, place })), 'k'), false, 'the default cut is the absence of a level');
  assert.equal(unpack(pack({ name: 'v', layers: {}, place })).view.place.level, undefined);

  const whole = sent(packed);
  same(unpack(part(whole)).view.place, { ...place, level: '66' }, 'a link written by hand in the same form is read');
  const { p: _, ...placeless } = whole;
  assert.ok(unpack(part(placeless)).error, 'a level with no place');
  for (const k of ['', '1234', '../x', '6 6', 66, null, ['66']]) assert.ok(unpack(part({ ...whole, k })).error, JSON.stringify(k));
});

(async () => {
  let failed = 0;
  for (const [name, fn] of tests) {
    try {
      await fn();
      console.log(`ok   ${name}`);
    } catch (err) {
      failed += 1;
      console.log(`FAIL ${name}\n     ${String(err && err.stack ? err.stack : err).split('\n').slice(0, 8).join('\n     ')}`);
    }
  }
  console.log(`${tests.length - failed} of ${tests.length} passed`);
  process.exit(failed ? 1 : 0);
})();
