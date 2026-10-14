'use strict';

// The topography overlay, checked by doing it: its script is run in a page
// of the kind dom.cjs makes, beside the panel's own script and the biomes',
// with a map that only remembers its layers and a server that answers as
// these tests tell it to. Run by `go test ./web/` where there is a node to
// run it with, or by hand: `node web/testdata/topography.test.cjs`.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const { page } = require('./dom.cjs');

const tests = [];
const test = (name, fn) => tests.push([name, fn]);
const wait = (ms = 30) => new Promise((done) => setTimeout(done, ms));
// The page's clock, which a test moves on to make a listing old.
let now = Date.now();
Date.now = () => now;
const later = () => { now += 120_000; };

const BANDS = [
  { from: -32768, to: 61, colour: '#5c549c', label: '' },
  { from: 62, to: 62, colour: '', label: 'sea level' },
  { from: 63, to: 69, colour: '#96c660' },
  { from: 70, to: 89, colour: '#e8d670' },
  { from: 90, to: 109, colour: '#dea854' },
  { from: 110, to: 139, colour: '#be7046' },
  { from: 140, to: 179, colour: '#968088' },
  { from: 180, to: 32767, colour: '#f6f6fa' },
];
const listingOf = (over = {}) => ({
  offered: true,
  ready: true,
  version: 'v1',
  tiles: { minZoom: -5, maxZoom: 4, size: 256 },
  intervals: [5, 10, 20, 50, 100, 200],
  heavyEvery: 5,
  bands: BANDS,
  ...over,
});
// The server says nothing is ready or versioned for a dimension it does not
// draw; this one does, to show the page does not take it for a reason to.
const NETHER = { offered: false, ready: true, version: 'v1', tiles: { minZoom: -5, maxZoom: 4, size: 256 }, intervals: [], heavyEvery: 5, bands: [] };
const BIOMES = { extracted: true, version: 'abc', biomes: [{ name: 'plains', label: 'Plains', color: '#8db360', area: 10 }], tiles: { minZoom: -6, maxZoom: 2, size: 256 } };
const plains = { generated: true, biome: { name: 'plains', label: 'Plains' } };

// A page with the panel, the biomes' script and the topography's, and a
// server whose answers are given by what the test passes.
async function load({ dimension = 'overworld', listings = {}, at = () => ({ ...plains, surface: 71 }), scripts = ['biomes.js', 'topography.js'], status = {}, storage = new Map() } = {}) {
  const p = page(storage);
  const { win, doc } = p;
  const output = doc.createElement('output');
  output.hidden = true;
  const lookup = doc.getElementById;
  // What the views' script looks for on the page, made as it is asked for.
  const made = new Map();
  const dialog = (id) => {
    if (!made.has(id)) made.set(id, Object.assign(doc.createElement(id === 'grid' ? 'input' : 'div'), { id, checked: false, showModal() {}, close() {} }));
    return made.get(id);
  };
  doc.getElementById = (id) => (id === 'biome' ? output : lookup(id) || (/^(views|preview)(-|$)|^(grid|more-open)$/.test(id) ? dialog(id) : null));
  const heard = new Map();
  const state = { dimension, zoom: 0, asked: [], layers: [], removed: [], listings: { overworld: listingOf(), end: listingOf({ bands: BANDS.slice(0, 5) }), nether: NETHER, ...listings } };
  win.L = {
    TileLayer: {
      extend: (proto) => {
        class Layer {
          constructor(url, options) { this.options = options; this.redraws = 0; state.layers.push(this); }
          addTo() { return this; }
          setOpacity() {}
          redraw() { this.redraws += 1; }
        }
        Object.assign(Layer.prototype, proto);
        return Layer;
      },
    },
  };
  Object.assign(win.mcmap, {
    map: {
      on: (type, fn) => { if (!heard.has(type)) heard.set(type, []); heard.get(type).push(fn); },
      removeLayer: (layer) => state.removed.push(layer),
      getZoom: () => state.zoom,
    },
    dimension: () => state.dimension,
    extent: () => null,
  });
  win.AbortController = AbortController;
  Object.assign(win, { location: { href: 'http://map.example/', origin: 'http://map.example', pathname: '/', search: '' }, URL, TextEncoder, TextDecoder, Uint8Array, atob, btoa });
  win.mcmap.place = { get: () => null, set: () => true };
  win.setInterval = () => 0;
  win.fetch = async (address) => {
    state.asked.push(address);
    const wanted = /^api\/topography\?dimension=(\w+)/.exec(address);
    if (wanted && status.topography) return { ok: status.topography < 400, status: status.topography, json: async () => ({}) };
    let body;
    if (wanted) body = state.listings[wanted[1]];
    else if (address.startsWith('api/biomes/at?')) body = at(address);
    else body = BIOMES;
    return { ok: true, status: 200, json: async () => JSON.parse(JSON.stringify(body)) };
  };
  for (const script of scripts) vm.runInContext(fs.readFileSync(path.join(__dirname, '..', script), 'utf8'), win, { filename: script });
  await wait();

  const tree = () => doc.getElementById('layers-body').querySelector('.tree');
  const nameOf = (li) => {
    const row = li.children.find((child) => child.classList.contains('row'));
    const name = row ? row.children.find((child) => child.classList.contains('name')) : null;
    return name ? name.textContent : '';
  };
  const kidsOf = (li) => li.children.find((child) => child.classList.contains('kids'));
  const find = (...labels) => {
    let list = tree();
    let at = null;
    for (const label of labels) {
      at = list ? list.children.find((child) => child.classList.contains('node') && nameOf(child) === label) || null : null;
      if (!at) return null;
      list = kidsOf(at);
    }
    return at;
  };
  const part = (labels, name) => find(...labels).children.find((child) => child.classList.contains('row')).children.find((child) => child.classList.contains(name));
  const press = async (labels, name = 'check') => { part(labels, name).click(); await wait(); };
  const rest = (x, z) => { for (const fn of heard.get('mousemove')) fn({ latlng: { lat: z, lng: x } }); return wait(); };
  const view = async (to) => {
    if (to) state.dimension = to;
    doc.dispatchEvent({ type: 'mcmap:view' });
    await wait();
  };
  const zoom = async (z) => { state.zoom = z; for (const fn of heard.get('zoomend')) fn(); await wait(); };
  return {
    ...p,
    views: win.mcmap.views,
    state,
    output,
    find,
    part,
    press,
    rest,
    view,
    zoom,
    nameOf,
    kidsOf,
    on: (id) => press(['Topography', id]),
    layer: () => state.layers[state.layers.length - 1] || null,
    live: () => state.layers.filter((l) => !state.removed.includes(l)),
    note: (id) => find('Topography', id).children.find((child) => child.classList.contains('note')).textContent,
    asked: (prefix) => state.asked.filter((a) => a.startsWith(prefix)),
    checked: (...labels) => part(labels, 'check').getAttribute('aria-checked'),
    disabled: (...labels) => part(labels, 'check').getAttribute('aria-disabled') === 'true',
  };
}

const ROWS = ['Hillshade', 'Contour lines', 'Height tint'];

test('the panel has a Topography group of three rows, all off, and nothing is drawn or asked for', async () => {
  const p = await load();
  const group = p.find('Topography');
  assert.ok(group, 'there is no Topography section');
  assert.deepEqual(p.kidsOf(group).children.map(p.nameOf), ROWS);
  for (const row of ROWS) assert.equal(p.checked('Topography', row), 'false', `${row} is on to begin with`);
  assert.equal(p.state.layers.length, 0);
  assert.equal(p.asked('api/topography/').length, 0);
  // The section stands after the biomes' and before the overlays'.
  const order = p.find('Topography').parentNode.children.map(p.nameOf);
  assert.ok(order.indexOf('Biomes') < order.indexOf('Topography'), order.join(', '));
});

test('one layer is drawn for however many of the three are on, and a second changes its address', async () => {
  const p = await load();
  await p.on('Hillshade');
  assert.equal(p.live().length, 1);
  const layer = p.layer();
  assert.equal(layer.getTileUrl({ z: -2, x: 3, y: -4 }), 'api/topography/tiles/overworld/-2/3/-4.png?styles=hillshade&v=v1');
  await p.on('Contour lines');
  assert.equal(p.live().length, 1, 'a second layer was made');
  assert.equal(layer.redraws, 1);
  assert.match(layer.getTileUrl({ z: 0, x: 0, y: 0 }), /\?styles=hillshade,contours&v=v1$/);
  await p.on('Height tint');
  assert.equal(p.live().length, 1);
  assert.match(layer.getTileUrl({ z: 0, x: 0, y: 0 }), /\?styles=hillshade,contours,tint&v=v1$/);
  assert.equal(layer.redraws, 2);
  // Off, a drawing is taken out of the address; the last off takes the layer.
  await p.on('Hillshade');
  assert.match(layer.getTileUrl({ z: 0, x: 0, y: 0 }), /styles=contours,tint/);
  await p.on('Contour lines');
  await p.on('Height tint');
  assert.equal(p.state.removed.length, 1);
});

test('the layer is the terrain\'s grid: its zooms, finer than the terrain\'s, over the biomes and under the grid', async () => {
  const p = await load();
  await p.on('Height tint');
  const o = p.layer().options;
  assert.equal(o.tileSize, 256);
  assert.equal(o.minNativeZoom, -5);
  assert.equal(o.maxNativeZoom, 4);
  // Enlarged past that it is smoothed.
  assert.equal(o.className, 'topography');
  assert.equal(o.minZoom, -5);
  assert.equal(o.noWrap, true);
  // Biomes are at 2, the slime chunks at 4.
  assert.equal(o.zIndex, 3);
});

test('a new reading redraws the layer with its version', async () => {
  const p = await load();
  await p.on('Hillshade');
  const layer = p.layer();
  p.state.listings.overworld = listingOf({ version: 'v2' });
  // The listing is asked for again only when it is old; a viewer coming
  // back to the page does it.
  later();
  await p.view();
  assert.match(layer.getTileUrl({ z: 0, x: 0, y: 0 }), /&v=v2$/);
  assert.equal(layer.redraws, 1);
});

test('the legend says the height each colour is, lowest first, and the sea has none', async () => {
  const p = await load();
  await p.press(['Topography', 'Height tint'], 'twist');
  const kids = p.kidsOf(p.find('Topography', 'Height tint')).children;
  assert.deepEqual(kids.map(p.nameOf), ['Below 62', '62 · sea level', '63 to 69', '70 to 89', '90 to 109', '110 to 139', '140 to 179', '180 and up']);
  const swatches = kids.map((li) => li.querySelector('.pic').children[0]);
  assert.equal(swatches[0].style.backgroundColor, '#5c549c');
  assert.equal(swatches[2].style.backgroundColor, '#96c660');
  assert.ok(swatches[1].classList.contains('clear'), 'the band that is not drawn is not given a colour');
});

test('a colour is only ever taken as #rrggbb, and a label is only ever text', async () => {
  const p = await load({ listings: { overworld: listingOf({ bands: [{ from: 1, to: 2, colour: 'red; background: url(x)', label: '<img src=x onerror=alert(1)>' }] }) } });
  await p.press(['Topography', 'Height tint'], 'twist');
  const kids = p.kidsOf(p.find('Topography', 'Height tint')).children;
  assert.equal(kids.length, 1);
  assert.equal(p.nameOf(kids[0]), '1 to 2 · <img src=x onerror=alert(1)>');
  assert.equal(kids[0].querySelector('.pic').children[0].style.backgroundColor, undefined);
});

test('bands left out of the tint are left out of the address, and one shown alone leaves the rest out', async () => {
  const p = await load();
  await p.on('Height tint');
  await p.on('Hillshade');
  const layer = p.layer();
  assert.doesNotMatch(layer.getTileUrl({ z: 0, x: 0, y: 0 }), /hide/);
  p.layers.facet('topography', 'tint').solo('overworld:3');
  await wait();
  assert.match(layer.getTileUrl({ z: 0, x: 0, y: 0 }), /styles=hillshade,tint&hide=0,1,2,4,5,6,7&v=v1$/);
  p.layers.facet('topography', 'tint').solo(null);
  await wait();
  assert.doesNotMatch(layer.getTileUrl({ z: 0, x: 0, y: 0 }), /hide/);
  // Without the tint on, what is hidden of it is not part of the address.
  p.layers.facet('topography', 'tint').solo('overworld:3');
  await p.on('Height tint');
  assert.doesNotMatch(layer.getTileUrl({ z: 0, x: 0, y: 0 }), /hide/);
});

test('a band shown alone that another dimension does not have is let go of there, and the tint is whole', async () => {
  const p = await load();
  await p.on('Height tint');
  p.layers.facet('topography', 'tint').solo('overworld:3');
  await wait();
  await p.view('end');
  assert.equal(p.layer().options.dimension, 'end');
  assert.match(p.layer().getTileUrl({ z: 0, x: 0, y: 0 }), /styles=tint&v=v1$/, 'the end\'s tint is drawn with bands left out');
  assert.equal(p.layers.facet('topography', 'tint').only, null);
  // One of this dimension's own is kept when its listing is read again.
  p.layers.facet('topography', 'tint').solo('end:2');
  await wait();
  later();
  await p.view();
  assert.equal(p.asked('api/topography?dimension=end').length, 2);
  assert.match(p.layer().getTileUrl({ z: 0, x: 0, y: 0 }), /styles=tint&hide=0,1,3,4&v=v1$/);
});

test('a band of another dimension shown alone by a view is let go of as it comes', async () => {
  const p = await load({ scripts: ['biomes.js', 'topography.js', 'views.js'] });
  await p.on('Height tint');
  const view = p.views.capture(false, false);
  view.name = 'Elsewhere';
  view.items['topography#tint'] = { only: 'end:2', hidden: [] };
  assert.ok(p.views.apply(view), 'the view was refused');
  await wait();
  assert.equal(p.layers.facet('topography', 'tint').only, null);
  assert.doesNotMatch(p.layer().getTileUrl({ z: 0, x: 0, y: 0 }), /hide/);
});

// A link says which layers are on by their place in a list that is only
// ever added to at the end, and which of what a layer is made of by the
// place of the choice in another: the topography's are the 24th to 26th
// and the 6th, written here apart from the script's own lists.
test('a link carries the three rows and the bands left out, and puts them back on another page', async () => {
  const scripts = ['biomes.js', 'topography.js', 'views.js'];
  const p = await load({ scripts });
  await p.on('Height tint');
  await p.press(['Topography', 'Height tint'], 'twist');
  await p.press(['Topography', 'Height tint', '70 to 89']);
  assert.match(p.layer().getTileUrl({ z: 0, x: 0, y: 0 }), /styles=tint&hide=3&v=v1$/);
  const part = p.views.pack({ ...p.views.capture(false, false), name: 'Hills' });
  const written = JSON.parse(Buffer.from(part.slice(3), 'base64url').toString('utf8'));
  assert.equal(written.l.slice(23, 26), '001');
  assert.deepEqual(JSON.parse(JSON.stringify(written.y['5'])), { o: null, h: ['overworld:3'] });

  const q = await load({ scripts });
  const read = q.views.unpack(part);
  assert.ok(read.view, read.error);
  assert.ok(q.views.apply(read.view), 'the view was refused');
  await wait();
  // On, with a band of it left out.
  assert.equal(q.checked('Topography', 'Height tint'), 'mixed');
  assert.equal(q.checked('Topography', 'Hillshade'), 'false');
  assert.equal(q.checked('Topography', 'Contour lines'), 'false');
  assert.match(q.layer().getTileUrl({ z: 0, x: 0, y: 0 }), /styles=tint&hide=3&v=v1$/);
});

test('each view that comes with the page has the three rows off and every band of the tint shown', async () => {
  for (const id of ['everything', 'exploring', 'base']) {
    const p = await load({ scripts: ['biomes.js', 'topography.js', 'views.js'] });
    for (const row of ROWS) await p.on(row);
    await p.press(['Topography', 'Height tint'], 'twist');
    await p.press(['Topography', 'Height tint', '70 to 89']);
    // The biomes' layer, which one of the views has on, is not counted.
    const drawn = () => p.live().filter((l) => l.options.className === 'topography').length;
    assert.equal(drawn(), 1);
    assert.ok(p.views.apply(p.settings.view(id)), `${id} was refused`);
    await wait();
    for (const row of ROWS) assert.equal(p.checked('Topography', row), 'false', `${id} leaves ${row} on`);
    assert.equal(drawn(), 0, `${id} leaves the topography drawn`);
    await p.on('Height tint');
    assert.doesNotMatch(p.layer().getTileUrl({ z: 0, x: 0, y: 0 }), /hide/, `${id} leaves a band of the tint out`);
  }
});

test('the rows that are on and the bands left out are kept from one load to the next', async () => {
  const storage = new Map();
  const p = await load({ storage });
  await p.on('Height tint');
  await p.press(['Topography', 'Height tint'], 'twist');
  await p.press(['Topography', 'Height tint', '70 to 89']);
  await wait(60);
  const q = await load({ storage });
  assert.equal(q.checked('Topography', 'Height tint'), 'mixed');
  assert.match(q.layer().getTileUrl({ z: 0, x: 0, y: 0 }), /styles=tint&hide=3&v=v1$/);
});

test('the contours say how far apart they are at the zoom the map is at', async () => {
  const p = await load();
  assert.equal(p.note('Contour lines'), '');
  await p.on('Contour lines');
  assert.equal(p.note('Contour lines'), 'A line every 5 blocks at this zoom, every 5th heavier.');
  await p.zoom(-3);
  assert.equal(p.note('Contour lines'), 'A line every 50 blocks at this zoom, every 5th heavier.');
  await p.zoom(-4.4);
  assert.equal(p.note('Contour lines'), 'A line every 100 blocks at this zoom, every 5th heavier.');
  // Closer than a block to a pixel the interval is no smaller.
  await p.zoom(3);
  assert.equal(p.note('Contour lines'), 'A line every 5 blocks at this zoom, every 5th heavier.');
  await p.zoom(8);
  assert.equal(p.note('Contour lines'), 'A line every 5 blocks at this zoom, every 5th heavier.');
});

test('past the widest zoom there are no tiles, and the row that is on says to zoom in', async () => {
  const p = await load();
  await p.on('Hillshade');
  await p.zoom(-6);
  assert.equal(p.note('Hillshade'), 'Zoom in to see it.');
  assert.equal(p.note('Height tint'), '', 'a row that is off says it too');
});

test('it is not offered in the nether: the rows are greyed out, nothing is drawn and the way back is as it was', async () => {
  const p = await load();
  await p.on('Hillshade');
  assert.equal(p.live().length, 1);
  await p.view('nether');
  assert.equal(p.live().length, 0, 'the nether is drawn');
  for (const row of ROWS) {
    assert.equal(p.disabled('Topography', row), true, `${row} is not greyed out`);
    assert.match(p.note(row === 'Hillshade' ? 'Hillshade' : row === 'Contour lines' ? 'Contour lines' : 'Height tint'), /Not drawn in the Nether/);
  }
  assert.equal(p.checked('Topography', 'Hillshade'), 'true', 'the choice was kept');
  assert.equal(p.asked('api/topography/tiles').length, 0);
  // And back in the overworld, it is drawn again.
  await p.view('overworld');
  assert.equal(p.live().length, 1);
  assert.equal(p.disabled('Topography', 'Hillshade'), false);
});

test('the end is offered, with its own legend', async () => {
  const p = await load({ dimension: 'end' });
  await p.on('Height tint');
  assert.equal(p.layer().options.dimension, 'end');
  await p.press(['Topography', 'Height tint'], 'twist');
  assert.equal(p.kidsOf(p.find('Topography', 'Height tint')).children.length, 5);
});

test('nothing is drawn until the heights have been read, and the row says so', async () => {
  const p = await load({ listings: { overworld: listingOf({ ready: false, version: '' }) } });
  await p.on('Hillshade');
  assert.equal(p.live().length, 0);
  assert.equal(p.note('Hillshade'), 'The heights have not been read yet.');
  // It is asked for again soon, and the tiles come with the reading.
  p.state.listings.overworld = listingOf();
  later();
  await p.view();
  assert.equal(p.live().length, 1);
  assert.equal(p.note('Hillshade'), '');
});

test('a server without it has no group', async () => {
  const p = await load({ status: { topography: 404 } });
  assert.equal(p.find('Topography'), null);
  assert.equal(p.live().length, 0);
});

test('logged out nothing is drawn', async () => {
  const p = await load();
  await p.on('Hillshade');
  p.doc.body.classList.add('locked');
  await p.view();
  assert.equal(p.live().length, 0);
  p.doc.body.classList.remove('locked');
  await p.view();
  assert.equal(p.live().length, 1);
});

// --- the footer ------------------------------------------------------------

test('the footer says the height of the surface while any one row is on, and nothing while none is', async () => {
  for (const row of ROWS) {
    const p = await load();
    await p.rest(10.7, -3.2);
    assert.equal(p.asked('api/biomes/at?').length, 0, 'asked with everything off');
    assert.equal(p.output.hidden, true);
    await p.on(row);
    await p.rest(11.7, -3.2);
    assert.equal(p.output.hidden, false, row);
    assert.equal(p.output.textContent, 'Surface: 71', row);
    assert.match(p.asked('api/biomes/at?').pop(), /dimension=overworld&x=11&z=-4$/);
    await p.on(row);
    assert.equal(p.output.hidden, true, `${row} switched off leaves the line`);
    assert.equal(p.output.textContent, '');
  }
});

test('with both overlays on the footer is one line, the biome and the height, as the biome overlay alone says it', async () => {
  const p = await load();
  p.win.mcmap.biomes.show('plains');
  await wait();
  await p.rest(5, 5);
  assert.equal(p.output.textContent, 'Biome: Plains · surface 71');
  await p.on('Hillshade');
  assert.equal(p.output.hidden, false);
  await p.rest(6, 5);
  assert.equal(p.output.textContent, 'Biome: Plains · surface 71');
  // The topography off leaves the biome's, which is the same line.
  await p.on('Hillshade');
  await p.rest(7, 5);
  assert.equal(p.output.textContent, 'Biome: Plains · surface 71');
});

test('the height alone is said as such where there is none, or nothing is generated', async () => {
  const p = await load({ at: (a) => (/x=1&/.test(a) ? { generated: true, biome: plains.biome } : /x=2&/.test(a) ? { generated: false } : { ...plains, surface: -64 }) });
  await p.on('Height tint');
  await p.rest(1, 1);
  assert.equal(p.output.textContent, 'Surface: none here');
  await p.rest(2, 1);
  assert.equal(p.output.textContent, 'Surface: not generated here');
  await p.rest(3, 1);
  assert.equal(p.output.textContent, 'Surface: -64');
});

test('in the nether, where it is not offered, the footer says nothing of the surface', async () => {
  const p = await load();
  await p.on('Hillshade');
  await p.view('nether');
  await p.rest(5, 5);
  assert.equal(p.asked('api/biomes/at?').length, 0);
  assert.equal(p.output.hidden, true);
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
