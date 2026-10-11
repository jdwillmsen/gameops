'use strict';

// What the map's markers do, checked by doing it: the script that keeps
// the pictures, the one that finds the middle of the map that can be seen
// and the live layer are run on the page stage.cjs makes, frames are sent
// as the server sends them, and what was drawn and where the map was
// taken are read back. Run by `go test ./web/` where there is a node to
// run it with, or by hand: `node web/testdata/markers.test.cjs`.
const assert = require('node:assert/strict');
const { stage } = require('./stage.cjs');

const tests = [];
const test = (name, fn) => tests.push([name, fn]);
const near = (got, want, what) => assert.ok(Math.abs(got - want) <= 0.01, `${what}: ${got}, not ${want}`);

const STYLES = ['dots', 'plates', 'large'];
const SIZES = ['small', 'normal', 'large', 'xlarge'];
const DENSITIES = [1, 1.25, 1.5, 2, 3];
const ZOOMS = [-6, -5, -4, -3, -2, -1, 0, 1, 2, 3];

// What the settings ask for, written out from their definition and not
// taken from the script: the side in CSS pixels of a mob's marker and of
// a container's, or null where a plain mark is drawn.
function asked(style, size, dpr, zoom) {
  const dense = dpr > 1;
  const box = { small: 12, normal: 16, large: dense ? 24 : 32, xlarge: dense ? 32 : 48 }[size];
  const bare = { small: 24, normal: 32, large: 48, xlarge: 64 }[size];
  const sides = style === 'large' ? { mob: bare + 6, chest: bare + 6 } : { mob: box + 8, chest: box + 6 };
  return { mob: style === 'dots' || zoom < -1 ? null : sides.mob, chest: style === 'dots' || zoom < -2 ? null : sides.chest };
}

// A marker of the script's own class, on a canvas stretched as Leaflet
// stretches it part way through a zoom, drawn as Leaflet would draw it.
function drawn(s, Class, sprite, stretch, options = {}) {
  const renderer = s.L.canvas({});
  renderer._drawing = true;
  renderer._steady = 1 / stretch;
  const marker = new Class([0, 0], { renderer, radius: 12, sprite, ...options });
  marker._point = s.L.point(300, 200);
  marker._updatePath();
  return renderer._ctx;
}

test('a picture is the size chosen in every style, at every size, density and zoom', async () => {
  for (const dpr of DENSITIES) {
    for (const style of STYLES) {
      for (const size of SIZES) {
        const s = await stage({ dpr, look: { style, size } });
        const density = s.icons.density();
        assert.equal(density, dpr > 1 ? 2 : 1);
        for (const zoom of ZOOMS) {
          s.map.zoom = zoom;
          const want = asked(style, size, dpr, zoom);
          // Asked for twice: the first asking sends for the picture.
          s.icons.mob('cow', '#f00');
          s.icons.plate('container/chest', '#f00');
          await s.settle();
          const mob = s.icons.mob('cow', '#f00');
          const chest = s.icons.plate('container/chest', '#f00');
          const at = `${style} ${size} at density ${dpr}, zoom ${zoom}`;
          assert.equal(mob ? mob.width / density : null, want.mob, `a mob, ${at}`);
          assert.equal(chest ? chest.width / density : null, want.chest, `a chest, ${at}`);
          if (mob) assert.equal(s.icons.sizes().mob * 2, want.mob, `a mob's reach, ${at}`);
        }
      }
    }
  }
});

test('through a zoom a marker stays the size it was, however far the canvas is stretched', async () => {
  for (const dpr of [1, 2]) {
    for (const style of STYLES) {
      for (const size of SIZES) {
        const s = await stage({ dpr, look: { style, size } });
        s.icons.mob('cow', '#f00');
        await s.settle();
        const sprite = s.icons.mob('cow', '#f00');
        const density = s.icons.density();
        // A canvas of twice the pixels is under a transform of two, as Leaflet puts it.
        for (const stretch of [1, 2 ** 0.37, 2, 4, 8, 0.5, 0.25]) {
          const at = `${style} ${size} at density ${dpr}, stretched ${stretch}`;
          for (const Class of [s.icons.Stamped, s.icons.Tagged]) {
            const ctx = drawn(s, Class, sprite, stretch);
            if (sprite) {
              assert.equal(ctx.drawn.length, 1, at);
              const [d] = ctx.drawn;
              near(d.w * stretch, sprite.width / density, `width on the screen, ${at}`);
              near(d.x + d.w / 2, 300, `still about its own point, ${at}`);
              near(d.y + d.h / 2, 200, `still about its own point, ${at}`);
            } else {
              assert.equal(ctx.arcs.length, 1, at);
              near(ctx.arcs[0].r * stretch, 12, `a dot's radius on the screen, ${at}`);
              near(ctx.arcs[0].x, 300, `a dot about its own point, ${at}`);
            }
          }
        }
      }
    }
  }
});

test('a name over a marker keeps its size and its distance through a zoom', async () => {
  const s = await stage({ look: { style: 'plates' } });
  s.icons.mob('cow', '#f00');
  await s.settle();
  const sprite = s.icons.mob('cow', '#f00');
  const tag = s.icons.tag('Daisy', '#fff');
  for (const stretch of [1, 2, 0.5]) {
    const ctx = drawn(s, s.icons.Tagged, sprite, stretch, { tag });
    assert.equal(ctx.drawn.length, 2);
    const [picture, name] = ctx.drawn;
    near(name.w * stretch, tag.width, `the name's width, stretched ${stretch}`);
    near((picture.y + picture.h / 2 - (name.y + name.h / 2)) * stretch, 12 + 3 + tag.height / 2, `how far over the marker, stretched ${stretch}`);
  }
});

test('a stretched canvas is drawn again each frame, and once more at rest when the zoom ends', async () => {
  const s = await stage({});
  const renderer = s.L.canvas({});
  s.icons.steadies(renderer);
  s.map._animatingZoom = true;
  s.map.fire('zoomanim');
  for (const by of [1.2, 1.7, 2]) {
    s.stretched.set(renderer._container, by);
    const before = renderer.redraws;
    s.frame();
    near(renderer._steady, 1 / by, `stretched ${by}`);
    assert.equal(renderer.redraws, before + 1);
  }
  // The same stretch a second frame running is nothing to draw again.
  const settled = renderer.redraws;
  s.frame();
  assert.equal(renderer.redraws, settled);
  s.map._animatingZoom = false;
  s.map.fire('zoomend');
  assert.equal(renderer._steady, 1);
  s.stretched.delete(renderer._container);
  s.frame();
  s.frame();
  assert.equal(renderer._steady, 1);
});

test('through a pinch the canvas is drawn for each step as it is taken, by how far the map has zoomed', async () => {
  const s = await stage({});
  const renderer = s.L.canvas({});
  s.icons.steadies(renderer);
  for (const zoom of [0.3, 0.9, 1.6, -0.7]) {
    s.map.zoom = zoom;
    const before = renderer.redraws;
    s.map.fire('zoom');
    near(renderer._steady, 1 / 2 ** zoom, `at zoom ${zoom}`);
    assert.equal(renderer.redraws, before + 1, 'in the step, not a frame after it');
  }
  s.map.zoom = 1;
  renderer._zoom = 1;
  s.map.fire('zoomend');
  assert.equal(renderer._steady, 1);
});

// --- a face on its plate ------------------------------------------------------

// The villager's picture as the server makes it: a head 8 by 10 with a row
// of nose under it, squared to 11 with the odd column on the right. The
// fox's head is the lower six rows of its eight, under its ears, and the
// shulker's the lower half of it.
const HEADS = { 'face/villager_v2': [1, 0, 8, 10], 'face/fox': [0, 2, 8, 6], 'face/shulker': [0, 8, 16, 8] };

// Where a mob's picture was drawn on its marker, in the marker's pixels.
async function composed(s, type) {
  s.icons.mob(type, '#f00');
  await s.settle();
  const sprite = s.icons.mob(type, '#f00');
  const drawn = sprite.getContext('2d').drawn.filter((d) => !('getContext' in d.image));
  assert.equal(drawn.length, 1, `${type} is one picture on its marker`);
  return { sprite, at: drawn[0], per: drawn[0].w / drawn[0].image.width };
}

test('a face sits on its plate by its head, at a whole number of pixels to the pixel', async () => {
  for (const dpr of DENSITIES) {
    for (const size of SIZES) {
      const s = await stage({ dpr, look: { style: 'plates', size }, boxes: HEADS });
      const at = `${size} at density ${dpr}`;
      const { sprite, at: d, per } = await composed(s, 'villager_v2');
      assert.ok(Number.isInteger(per) && per >= 1, `a villager is ${per} to the pixel, ${at}`);
      near(d.x + (1 + 4) * per, sprite.width / 2, `the villager's head across, ${at}`);
      near(d.y + 5 * per, sprite.height / 2, `the villager's head down, ${at}`);
      // Much the size of its box: never under three quarters of it, never over by more than a third.
      const room = { small: 12, normal: 16, large: dpr > 1 ? 24 : 32, xlarge: dpr > 1 ? 32 : 48 }[size] * s.icons.density();
      assert.ok(10 * per >= 0.75 * room && 10 * per <= (4 / 3) * room, `the villager's head is ${10 * per} in a box of ${room}, ${at}`);
      // A fox that fits its box whole keeps its ears, over a head a pixel low; one that does not is by its head.
      const fox = await composed(s, 'fox');
      near(fox.at.y + (8 * fox.per <= room ? 4 : 5) * fox.per, sprite.height / 2, `a fox, ${at}`);
      // Half of a shulker is lid, and it is drawn by the whole of it.
      const shulker = await composed(s, 'shulker');
      near(shulker.at.y + 8 * shulker.per, sprite.height / 2, `a shulker by the whole of it, ${at}`);
    }
  }
});

test('with no plate to hide what spills, the whole of a face is in its box and its head as near the middle as that leaves', async () => {
  for (const dpr of [1, 2]) {
    for (const size of SIZES) {
      const s = await stage({ dpr, look: { style: 'large', size }, boxes: HEADS });
      const room = { small: 24, normal: 32, large: 48, xlarge: 64 }[size] * s.icons.density();
      s.icons.mob('villager_v2', '#f00');
      await s.settle();
      const sprite = s.icons.mob('villager_v2', '#f00');
      // Composed on a canvas of its own first, and that is where the picture is placed.
      const placed = s.made.flatMap((canvas) => canvas.getContext('2d').drawn).filter((d) => d.image.width === 11 && !('getContext' in d.image)).at(-1);
      const edge = (sprite.width - room) / 2;
      // What is drawn of it: eight columns, one in from the left of its square.
      const per = placed.w / 11;
      assert.ok(placed.x + per >= edge - 0.01 && placed.x + 9 * per <= sprite.width - edge + 0.01, `across, ${size} at density ${dpr}`);
      assert.ok(placed.y >= edge - 0.01 && placed.y + placed.h <= sprite.height - edge + 0.01, `down, ${size} at density ${dpr}`);
    }
  }
});

test('a face the server says nothing of is set by what is drawn of it, not by its square', async () => {
  for (const dpr of DENSITIES) {
    for (const [style, size] of [['plates', 'normal'], ['plates', 'xlarge'], ['large', 'normal']]) {
      const s = await stage({ dpr, look: { style, size } });
      s.icons.mob('villager_v2', '#f00');
      await s.settle();
      const sprite = s.icons.mob('villager_v2', '#f00');
      const d = s.made.flatMap((canvas) => canvas.getContext('2d').drawn).filter((at) => at.image.width === 11 && at.w > 11 && !('getContext' in at.image)).at(-1);
      const per = d.w / 11;
      const at = `${style} ${size} at density ${dpr}`;
      // The head's eight columns start one in: their middle is on the plate's, to half a screen pixel.
      assert.ok(Math.abs(d.x + 5 * per - sprite.width / 2) <= 0.5, `across, ${at}: ${d.x + 5 * per - sprite.width / 2}`);
      // Sized as it was before any server said where heads are: nothing spills.
      assert.ok(11 * per <= { normal: style === 'large' ? 32 : 16, xlarge: dpr > 1 ? 32 : 48 }[size] * s.icons.density() + 0.01, `in its box, ${at}`);
    }
  }
  // A picture that is all drawn, and one that is no face, are set as they were.
  const s = await stage({});
  const zombie = await composed(s, 'zombie');
  near(zombie.at.x + zombie.at.w / 2, zombie.sprite.width / 2, 'a head that fills its picture');
});

test('a village wears the mark of its biome where the server has it, and the plains one where not', async () => {
  const s = await stage({});
  const key = (of) => s.icons.keyOf('structure', of);
  assert.equal(key({ kind: 'village' }), 'structure/village');
  assert.equal(key({ kind: 'village', biome: 'desert' }), 'structure/village_desert');
  assert.equal(key({ kind: 'village', biome: 'desert_hills' }), 'structure/village_desert');
  assert.equal(key({ kind: 'village', biome: 'plains' }), 'structure/village');
  // The stage's server lists no savanna mark.
  assert.equal(key({ kind: 'village', biome: 'savanna' }), 'structure/village');
  assert.equal(key({ kind: 'outpost', biome: 'desert' }), 'structure/outpost');
  assert.equal(key({ kind: 'village', biome: '<img src=x>' }), 'structure/village');
});

// --- the middle of the map that can be seen ---------------------------------

const BOX = { left: 0, top: 0, right: 1000, bottom: 600 };

test('the room to see the map in is its box less what lies across its middle', async () => {
  const s = await stage({});
  const free = (covers) => s.app.room.free(BOX, covers);
  const same = (got, want, what) => assert.deepEqual({ ...got }, want, what);
  same(free([]), BOX, 'nothing over it');
  same(free([{ left: 1000, top: 0, right: 1340, bottom: 600 }]), BOX, 'a panel docked beside it');
  same(free([{ left: 660, top: 0, right: 1000, bottom: 600 }]), { ...BOX, right: 660 }, 'a panel floating over its right edge');
  same(free([{ left: 0, top: 300, right: 1000, bottom: 600 }]), { ...BOX, bottom: 300 }, 'a sheet at half height');
  same(free([{ left: 0, top: 520, right: 1000, bottom: 600 }]), { ...BOX, bottom: 520 }, 'a sheet peeking');
  same(free([{ left: 0, top: 0, right: 1000, bottom: 600 }]), BOX, 'a sheet over all of it');
  same(free([{ left: 10, top: 440, right: 306, bottom: 590 }]), BOX, 'a card in a corner');
  same(free([{ left: 8, top: 440, right: 992, bottom: 592 }]), { ...BOX, bottom: 440 }, 'a card across the foot of a phone');
  same(free([{ left: 660, top: 0, right: 1000, bottom: 600 }, { left: 10, top: 440, right: 306, bottom: 590 }]), { ...BOX, right: 660 }, 'a floating panel and a card clear of the middle of what is left');
  same(free([{ left: 500, top: 0, right: 1000, bottom: 600 }, { left: 10, top: 440, right: 306, bottom: 590 }]), { left: 0, top: 0, right: 500, bottom: 440 }, 'a wide panel, and a card across the middle of what is left');
  same(free([{ left: 10, top: 440, right: 306, bottom: 590 }, { left: 500, top: 0, right: 1000, bottom: 600 }]), { left: 0, top: 0, right: 500, bottom: 440 }, 'in whichever order they are given');
  same(s.app.room.offset(BOX, [{ left: 660, top: 0, right: 1000, bottom: 600 }]), { x: -170, y: 0 }, 'how far the middle moves for a panel');
  same(s.app.room.offset(BOX, [{ left: 0, top: 300, right: 1000, bottom: 600 }]), { x: 0, y: -150 }, 'and for a sheet');
});

test('a place aimed for is in the middle of the room, at any zoom', async () => {
  const s = await stage({});
  const covers = { nothing: {}, 'a floating panel': { '#layers .panel': { left: 660, top: 0, right: 1000, bottom: 600 } }, 'a sheet': { '#layers .panel': { left: 0, top: 300, right: 1000, bottom: 600 } }, 'a card across the foot': { '#inspect': { left: 8, top: 440, right: 992, bottom: 592 } } };
  for (const [what, over] of Object.entries(covers)) {
    for (const key of ['#layers .panel', '#inspect']) delete s.boxes[key];
    Object.assign(s.boxes, over);
    const room = s.app.room.free(BOX, Object.values(over));
    for (const zoom of [-3, 0, 2]) {
      s.map.zoom = zoom;
      s.map.centre = s.app.room.aim([40, -70], zoom);
      const at = s.map.onScreen([40, -70]);
      near(at.x, (room.left + room.right) / 2, `across, under ${what} at zoom ${zoom}`);
      near(at.y, (room.top + room.bottom) / 2, `down, under ${what} at zoom ${zoom}`);
    }
  }
});

// --- following ----------------------------------------------------------------

const steve = (x, z) => ({ i: '1', n: 'Steve', x, y: 64, z, r: 0 });
const cow = (x, z) => ({ i: '50', t: 'cow', x, y: 64, z });

// A page following one entity, which is in the frame already drawn.
async function following(key, options = {}) {
  const s = await stage(options);
  // Every frame of a follow check comes through the pipeline, as it does in service.
  s.send = (world) => s.sample(world);
  await s.send({ players: [steve(10, 20)], mobs: [cow(-30, 5)] });
  assert.ok(s.app.inspect.open({ kind: key === 'p:1' ? 'player' : 'mob', id: key.slice(2), type: 'cow', name: key === 'p:1' ? 'Steve' : '', x: 0, y: 64, z: 0 }), 'the card opens');
  s.doc.getElementById('inspect-follow').click();
  return s;
}

test('a frame with a player whose head the server has is drawn whole', async () => {
  const s = await stage({ heads: { steve: 'h1' } });
  let heard = 0;
  s.doc.addEventListener('mcmap:players', () => { heard += 1; });
  await s.send({ players: [steve(10, 20)], mobs: [cow(-30, 5)] });
  await s.send({ players: [steve(12, 20)], mobs: [cow(-30, 5)] });
  assert.equal(heard, 2, 'whoever draws where the players have been is told of each frame');
});

for (const heads of [{}, { steve: 'h1' }]) {
  const which = Object.keys(heads).length ? 'with a head' : 'with none';
  test(`a followed player ${which} is kept in the middle, ringed, and said to be followed`, async () => {
    const s = await following('p:1', { heads });
    const button = s.doc.getElementById('inspect-follow');
    for (const [x, z] of [[14, 22], [47, 25], [80.5, 28.25]]) {
      await s.send({ players: [steve(x, z)], mobs: [cow(-30, 5)] });
      const at = s.map.onScreen([z, x]);
      near(at.x, 500, 'across');
      near(at.y, 300, 'down');
      const ring = [...s.map.layers].find((layer) => layer.options && layer.options.interactive === false);
      assert.ok(ring, 'the ring is on the map');
      assert.deepEqual(ring.getLatLng(), { lat: z, lng: x }, 'the ring is where they are');
      assert.equal(button.textContent, 'Following');
      assert.equal(button.getAttribute('aria-pressed'), 'true');
      assert.equal(s.app.inspect.following(), 'p:1');
    }
  });
}

test('a followed mob is kept in the middle of what a panel leaves of the map', async () => {
  const s = await following('m:50');
  s.boxes['#layers .panel'] = { left: 660, top: 0, right: 1000, bottom: 600 };
  await s.send({ players: [steve(10, 20)], mobs: [cow(-60, 9)] });
  const at = s.map.onScreen([9, -60]);
  near(at.x, 330, 'across');
  near(at.y, 300, 'down');
  // The sheet raised: the room changes with nobody having moved.
  s.boxes['#layers .panel'] = { left: 0, top: 300, right: 1000, bottom: 600 };
  s.map.fire('resize');
  const then = s.map.onScreen([9, -60]);
  near(then.x, 500, 'across, under the sheet');
  near(then.y, 150, 'down, under the sheet');
});

test('a zoom while following is about whoever is followed, and about the pointer once it is not', async () => {
  const s = await following('p:1');
  s.boxes['#inspect'] = { left: 8, top: 440, right: 992, bottom: 592 };
  await s.send({ players: [steve(33, -12)], mobs: [] });
  for (const zoom of [() => s.map.setZoomAround(s.L.point(900, 50), 2), () => s.map.setZoom(-1), () => s.map.setZoomAround(s.L.point(5, 5), 99)]) {
    zoom();
    const last = s.map.went.at(-1);
    assert.equal(last.how, 'setView');
    assert.ok(last.zoom >= -6 && last.zoom <= 3, 'no further than the map goes');
    const at = s.map.onScreen([-12, 33]);
    near(at.x, 500, `across at zoom ${last.zoom}`);
    near(at.y, 220, `down at zoom ${last.zoom}`);
  }
  s.doc.getElementById('inspect-follow').click();
  assert.equal(s.app.inspect.following(), null);
  s.map.setZoomAround(s.L.point(900, 50), 1);
  assert.equal(s.map.went.at(-1).how, 'setZoomAround');
  s.map.setZoom(0);
  assert.equal(s.map.went.at(-1).how, 'setZoom');
});

test('a frame that comes while the canvas is of another zoom waits for the zoom to end', async () => {
  const s = await following('p:1');
  const marker = () => [...s.map.layers].flatMap((layer) => (layer.getLayers ? layer.getLayers() : [])).find((layer) => layer.tip);
  const before = marker().getLatLng();
  // A pinch: the map is between zooms and the canvas still of the one it began at.
  s.map.zoom = 0.4;
  await s.send({ players: [steve(90, 90)], mobs: [] });
  assert.deepEqual(marker().getLatLng(), before, 'nothing is moved on a canvas of another zoom');
  s.map.zoom = 0;
  s.map.fire('zoomend');
  await s.settle();
  assert.deepEqual(marker().getLatLng(), { lat: 90, lng: 90 }, 'and is once the zoom is over');
});

const marks = (s) => [...s.map.layers].flatMap((layer) => (layer.getLayers ? layer.getLayers() : []));

// --- following through what the pipeline does to a mob --------------------------

// A herd round a player, and one mob of it that is followed.
const herdOf = (n) => Array.from({ length: n }, (_, i) => ({ i: String(1000 + i), t: i % 3 ? 'zombie' : 'villager_v2', x: (i % 20) * 6 - 60, y: 64, z: Math.floor(i / 20) * 6 - 30 }));
const followed = (x, z) => ({ i: '77', t: 'zombie', x, y: 64, z });
async function followingInAHerd(options = {}) {
  const s = await stage({ heads: { steve: 'h1' }, ...options });
  const world = (x, z, player = steve(0, 0)) => ({ players: [player], mobs: [...herdOf(120), followed(x, z)] });
  await s.sample(world(5, 5));
  assert.ok(s.app.inspect.open({ kind: 'mob', id: '77', type: 'zombie', x: 5, y: 64, z: 5 }));
  s.doc.getElementById('inspect-follow').click();
  const button = s.doc.getElementById('inspect-follow');
  const centred = (x, z, what) => { const at = s.map.onScreen([z, x]); near(at.x, 500, `${what}: across`); near(at.y, 300, `${what}: down`); };
  return { s, world, button, centred, what: () => s.doc.getElementById('inspect-what').textContent };
}

test('a generation comes as several lines, and a frame is a whole list or the one before', async () => {
  const { s, world } = await followingInAHerd();
  assert.ok(s.pipe.parts >= 5, `${s.pipe.parts} lines to the generation`);
  const mobs = () => marks(s).filter((m) => m.options.type).length;
  assert.equal(mobs(), 121);
  // A line of the mob list lost: the list before stands, whole.
  s.pipe.loses = (line) => line.kind === 'mobs' && line.part === 1;
  await s.sample(world(9, 9));
  assert.equal(mobs(), 121, 'no part of a list is drawn without the rest');
  s.pipe.loses = () => false;
});

test('a followed mob left out of a sample is still followed, and the map takes up with it again', async () => {
  const { s, world, button, centred, what } = await followingInAHerd();
  await s.sample(world(8, 6));
  centred(8, 6, 'followed');
  // Unloaded as it was read, or past the cap for a sample: not in the list.
  s.pipe.skips = (mob, gen) => mob.i === '77' && gen % 3 === 0;
  let missed = 0;
  for (let n = 0; n < 12; n += 1) {
    const [x, z] = [10 + n * 2, 6 + n];
    await s.sample(world(x, z), n % 4 === 0 ? 2000 : 1000);
    assert.equal(button.textContent, 'Following', `sample ${n}`);
    assert.equal(button.getAttribute('aria-pressed'), 'true');
    assert.equal(s.app.inspect.following(), 'm:77');
    if (s.pipe.gen % 3 === 0) {
      missed += 1;
      assert.equal(what(), 'Not in the live list just now.');
      assert.equal(button.disabled, false, 'and can still be let go of');
    } else {
      assert.equal(what(), '');
      centred(x, z, `back in sample ${n}`);
    }
  }
  assert.ok(missed >= 3);
});

test('a followed mob the cap leaves out while a player walks off is followed again when it is back inside', async () => {
  const { s, world, button, centred } = await followingInAHerd();
  s.pipe.cap = 40;
  // The first player walks away, so the forty nearest them are others, then comes back.
  for (const [n, px] of [0, 400, 400, 400, 0, 0].entries()) {
    await s.sample(world(5 + n, 5, steve(px, 0)));
    assert.equal(button.textContent, 'Following', `with the player at ${px}`);
    assert.equal(marks(s).some((m) => m.options.type === 'zombie' && m.getLatLng().lng === 5 + n), px === 0, `on the map with the player at ${px}`);
    if (px === 0) centred(5 + n, 5, `with the player at ${px}`);
  }
});

test('a followed mob lost to a missing line stays where it was drawn and is followed on', async () => {
  const { s, world, button, centred } = await followingInAHerd();
  s.pipe.loses = (line, gen) => line.kind === 'mobs' && line.part === 2 && gen % 2 === 0;
  for (let n = 0; n < 6; n += 1) {
    await s.sample(world(5 + n * 3, 5));
    assert.equal(button.textContent, 'Following');
    // Centred on wherever the last whole list had it.
    if (s.pipe.gen % 2 === 1) centred(5 + n * 3, 5, `sample ${n}`);
  }
});

test('a picture that says nothing, while a followed mob is missing or after, neither lets go of it nor starts its time afresh', async () => {
  const { s, world, button, centred } = await followingInAHerd();
  const nothing = async () => { s.advance(1000); await s.send({ stale: true }); };
  s.pipe.skips = (mob) => mob.i === '77';
  await s.sample(world(5, 5));
  // The stream stalls and what is drawn is cleared, then frames come again without it.
  await nothing();
  for (let n = 0; n < 5; n += 1) {
    await s.sample(world(5, 5));
    assert.equal(button.textContent, 'Following', `${n + 3} s without it`);
  }
  s.pipe.skips = () => false;
  await s.sample(world(30, 12));
  centred(30, 12, 'back');
  // Cleared while it was in the picture, and not in the first frame after: still given its time.
  await nothing();
  s.pipe.skips = (mob) => mob.i === '77';
  await s.sample(world(30, 12));
  assert.equal(button.textContent, 'Following');
  // But the time it has been missing is counted through a picture that says nothing.
  for (let n = 0; n < 7; n += 1) await s.sample(world(30, 12));
  await nothing();
  for (let n = 0; n < 8; n += 1) await s.sample(world(30, 12));
  assert.equal(button.textContent, 'Follow', 'seventeen seconds gone');
});

test('a mob gone for longer than it is given is let go of, and the card says so', async () => {
  const { s, world, button, what } = await followingInAHerd();
  s.pipe.skips = (mob) => mob.i === '77';
  for (let n = 0; n < 14; n += 1) {
    await s.sample(world(5, 5));
    assert.equal(button.textContent, 'Following', `${n + 1} s without it`);
  }
  await s.sample(world(5, 5), 2000);
  assert.equal(button.textContent, 'Follow');
  assert.equal(button.getAttribute('aria-pressed'), 'false');
  assert.equal(s.app.inspect.following(), null);
  assert.equal(what(), 'No longer tracked.');
  assert.equal(s.doc.getElementById('inspect-said').textContent, 'No longer following Zombie.');
  // Back after that, it is tracked again and not followed: the viewer was told it was let go.
  s.pipe.skips = () => false;
  await s.sample(world(50, 50));
  assert.equal(what(), '');
  assert.equal(button.textContent, 'Follow');
});

test('a player is given a few seconds and no more, and a drag still lets go at once', async () => {
  const s = await stage({ heads: { steve: 'h1' } });
  await s.sample({ players: [steve(1, 1)], mobs: [] });
  assert.ok(s.app.inspect.open({ kind: 'player', id: '1', name: 'Steve', x: 1, y: 64, z: 1 }));
  const button = s.doc.getElementById('inspect-follow');
  button.click();
  for (let n = 0; n < 3; n += 1) {
    await s.sample({ players: [], mobs: [] });
    assert.equal(button.textContent, 'Following', `${n + 1} s without them`);
  }
  await s.sample({ players: [steve(4, 4)], mobs: [] });
  near(s.map.onScreen([4, 4]).x, 500, 'followed again');
  s.map.fire('dragstart');
  assert.equal(button.textContent, 'Follow');
  button.click();
  for (let n = 0; n < 5; n += 1) await s.sample({ players: [], mobs: [] });
  assert.equal(button.textContent, 'Follow', 'gone past four seconds');
});

test('with ?debug=1 the card says what it knows of its entity, and without it has no such line', async () => {
  const plain = await followingInAHerd();
  assert.equal(plain.s.doc.getElementById('inspect').children.filter((node) => node.classList.contains('inspect-debug')).length, 0);
  const { s, world } = await followingInAHerd({ search: '?debug=1' });
  s.pipe.skips = (mob, gen) => mob.i === '77' && gen % 2 === 0;
  for (let n = 0; n < 4; n += 1) await s.sample(world(5, 5));
  const line = s.doc.getElementById('inspect').children.find((node) => node.classList.contains('inspect-debug')).textContent;
  assert.match(line, /^m:77 · (live|missing) · last update [0-9.]+ s ago · in 3 of the last 5 frames · follow on: its button$/);
});

async function main() {
  let failed = 0;
  for (const [name, fn] of tests) {
    try {
      await fn();
      console.log(`ok   ${name}`);
    } catch (err) {
      failed += 1;
      console.log(`FAIL ${name}\n     ${String(err && err.stack ? err.stack : err).split('\n').slice(0, 6).join('\n     ')}`);
    }
  }
  console.log(`${tests.length - failed} of ${tests.length} passed`);
  process.exitCode = failed === 0 ? 0 : 1;
}

main();
