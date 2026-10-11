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
      assert.ok(placed.x >= edge - 0.01 && placed.x + placed.w <= sprite.width - edge + 0.01, `across, ${size} at density ${dpr}`);
      assert.ok(placed.y >= edge - 0.01 && placed.y + placed.h <= sprite.height - edge + 0.01, `down, ${size} at density ${dpr}`);
    }
  }
});

test('a server that says nothing of heads has its faces drawn by the whole picture, as before', async () => {
  const s = await stage({ look: { style: 'plates', size: 'normal' } });
  const { sprite, at: d } = await composed(s, 'villager_v2');
  near(d.x + d.w / 2, sprite.width / 2, 'across');
  near(d.y + d.h / 2, sprite.height / 2, 'down');
  near(d.w, 16, 'stretched to its box');
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

// --- groups -------------------------------------------------------------------

// A crowd of one family in a patch of the world, each with an id.
const crowd = (n, family, type, x0, z0, spread = 30) => Array.from({ length: n }, (_, i) => ({ id: `${family}${type}${i}`, x: x0 + ((i * 37) % spread), z: z0 + ((i * 61) % spread), family, type }));

test('whatever heaps up in a square is one group, and every item is counted once', async () => {
  const s = await stage({});
  const { cluster, active } = s.app.groups;
  const items = [...crowd(40, 'hostile', 'zombie', 0, 0), ...crowd(25, 'passive', 'cow', 0, 0), ...crowd(3, 'hostile', 'creeper', 0, 0), ...crowd(9, 'beds', 'red', 0, 0),
    { id: 'far', x: 5000, z: 5000, family: 'hostile', type: 'zombie' }, { id: 'named', x: 3, z: 3, family: 'passive', type: 'cow', alone: true },
    { id: 'nowhere', x: NaN, z: 0, family: 'passive', type: 'cow' }];
  for (const zoom of [-1, -2, -3, -6]) {
    assert.ok(active(zoom));
    const { groups, single } = cluster(items, zoom, null);
    const counted = groups.reduce((n, g) => n + g.members.length, 0) + single.length;
    assert.equal(counted, items.length, `every item once at zoom ${zoom}`);
    assert.equal(new Set(groups.map((g) => g.key)).size, groups.length, 'one mark to a square');
    const side = 48 / 2 ** zoom;
    for (const g of groups) {
      assert.ok(g.members.length >= 2, 'one is never a group');
      assert.ok(!g.members.some((m) => m.alone), 'whoever is somebody is never one of several');
      assert.equal(g.families.reduce((n, [, of]) => n + of, 0), g.members.length, 'its families add up to it');
      assert.equal(g.types.reduce((n, [, , of]) => n + of, 0), g.members.length, 'its types add up to it');
      // In the middle of its square, so no two marks are nearer than a square apart.
      near(((g.x / side) % 1 + 1) % 1, 0.5, 'across its square');
      near(((g.z / side) % 1 + 1) % 1, 0.5, 'down its square');
    }
    for (const a of groups) for (const b of groups) if (a !== b) assert.ok(Math.max(Math.abs(a.x - b.x), Math.abs(a.z - b.z)) >= side - 0.001, 'two marks a square apart or more');
    const ids = single.map((m) => m.id);
    for (const id of ['far', 'named', 'nowhere']) assert.ok(ids.includes(id), `${id} is drawn by itself at zoom ${zoom}`);
  }
  // From three steps out the whole patch is one square, and one mark, whatever is in it.
  const far = cluster(items, -3, null).groups;
  assert.equal(far.length, 1);
  assert.equal(far[0].members.length, 77);
  assert.equal(JSON.stringify(far[0].families), JSON.stringify([['hostile', 43], ['passive', 25], ['beds', 9]]));
  assert.equal(JSON.stringify(far[0].types), JSON.stringify([['hostile', 'zombie', 40], ['passive', 'cow', 25], ['beds', 'red', 9], ['hostile', 'creeper', 3]]));
  // The widest mark leaves room between it and the next square's.
  for (const n of [2, 24, 25, 199, 200, 5000]) assert.ok(s.app.groups.widthOf(n) <= 48 - 10, `a mark of ${n} is ${s.app.groups.widthOf(n)} wide`);
  // Whether coming closer would part them.
  assert.equal(s.app.groups.together(crowd(5, 'hostile', 'zombie', 0, 0, 10)), true);
  assert.equal(s.app.groups.together(crowd(5, 'hostile', 'zombie', 0, 0, 30)), false);
});

test('from a block to the pixel inwards nothing is grouped', async () => {
  const s = await stage({});
  const items = crowd(50, 'hostile', 'zombie', 0, 0, 5);
  for (const zoom of [0, 1, 3]) {
    const { groups, single } = s.app.groups.cluster(items, zoom, null);
    assert.equal(groups.length, 0);
    assert.equal(single.length, 50);
  }
});

test('a mob that wanders over the edge of its square stays in its group', async () => {
  const s = await stage({});
  const { cluster } = s.app.groups;
  // At zoom -1 a square is 96 blocks: one mob near its edge, the rest well inside.
  const herd = crowd(6, 'passive', 'cow', 20, 20, 10);
  const walker = { id: 'walker', x: 94, z: 30, family: 'passive', type: 'cow' };
  let was = null;
  const sizes = [];
  for (const x of [94, 97, 95, 99, 93, 101, 96, 98]) {
    walker.x = x;
    const { groups, memory } = cluster([...herd, walker], -1, was);
    was = memory;
    sizes.push(groups.length === 1 ? groups[0].members.length : -1);
  }
  assert.equal(JSON.stringify(sizes), JSON.stringify([7, 7, 7, 7, 7, 7, 7, 7]), 'the group does not blink as it steps back and forth');
  // Well past the edge it has left, and is by itself in the next square.
  walker.x = 130;
  const gone = cluster([...herd, walker], -1, was);
  assert.equal(gone.groups[0].members.length, 6);
  assert.ok(gone.single.some((m) => m.id === 'walker'));
  // The same items sorted twice come out the same.
  assert.equal(JSON.stringify(cluster(herd, -2, null).groups.map((g) => [g.key, g.x, g.z])), JSON.stringify(cluster([...herd].reverse(), -2, null).groups.map((g) => [g.key, g.x, g.z])));
  // Memory of another zoom is of other squares, and is not used.
  assert.equal(cluster([...herd, walker], -2, was).groups.length, 1);
});

const zombie = (i, x, z, n) => ({ i: String(i), t: 'zombie', x, y: 64, z, ...(n ? { n } : {}) });
const marks = (s) => [...s.map.layers].flatMap((layer) => (layer.getLayers ? layer.getLayers() : []));

test('on the map a heap is one mark, and players, named mobs and the inspected one are their own', async () => {
  const s = await stage({ look: { group: 'on' } });
  await s.zoomTo(-2);
  const mobs = [...Array.from({ length: 12 }, (_, i) => zombie(100 + i, 10 + i, 10 + (i % 3))), zombie(200, 12, 12, 'Bob'), { i: '50', t: 'cow', x: 11, y: 64, z: 11 }, { i: '51', t: 'cow', x: 13, y: 64, z: 12 }];
  await s.send({ players: [steve(11, 11)], mobs });
  const heap = () => marks(s).filter((m) => m.options.heap);
  const own = () => marks(s).filter((m) => !m.options.heap);
  assert.equal(heap().length, 1, 'the zombies and the cows together');
  assert.equal(heap()[0].options.heap.members.length, 14);
  assert.equal(JSON.stringify(heap()[0].options.heap.families), JSON.stringify([['hostile', 12], ['passive', 2]]));
  assert.equal(own().length, 2, 'the player and the named mob');
  assert.ok(own().some((m) => m.options.name === 'Bob'));
  // The one the card is about is drawn by itself.
  assert.ok(s.app.inspect.open({ kind: 'mob', id: '105', type: 'zombie', x: 15, y: 64, z: 12 }));
  assert.equal(heap()[0].options.heap.members.length, 13);
  assert.equal(own().length, 3);
  s.app.inspect.shut();
  assert.equal(heap()[0].options.heap.members.length, 14);
  // Coming closer dissolves it, and going out again makes it.
  await s.zoomTo(0);
  assert.equal(heap().length, 0);
  assert.equal(own().length, 16);
  await s.zoomTo(-2);
  assert.equal(heap().length, 1);
});

const clickOn = (s, marker) => s.map.layers.forEach((layer) => { if (layer.getLayers && layer.getLayers().includes(marker)) layer.fire('click', { layer: marker }); });
const titleOf = (list) => list.children[0].children[0].textContent;
const listOf = (s) => s.doc.getElementById('map').parentNode.children.find((node) => node.classList.contains('heap-list'));

test('a click on a group goes to its members, or lists them where coming closer would not part them', async () => {
  const s = await stage({});
  await s.zoomTo(-2);
  // Spread over a hundred blocks, and a knot within a few.
  await s.send({ players: [], mobs: [...Array.from({ length: 6 }, (_, i) => zombie(100 + i, 10 + i * 20, 10)), ...Array.from({ length: 5 }, (_, i) => zombie(300 + i, 400 + i, 400)), { i: '350', t: 'creeper', x: 401, y: 64, z: 402 }] });
  const heaps = marks(s).filter((m) => m.options.heap);
  assert.equal(heaps.length, 2);
  const wide = heaps.find((m) => m.options.heap.members.length === 6 && m.options.heap.members[0].x < 200);
  const knot = heaps.find((m) => m !== wide);
  clickOn(s, wide);
  const went = s.map.went.at(-1);
  assert.equal(went.how, 'fitBounds');
  assert.equal(went.bounds.length, 6);
  assert.equal(went.options.maxZoom, 0, 'no closer than where nothing is grouped');
  assert.equal(listOf(s).hidden, true);
  clickOn(s, knot);
  const list = listOf(s);
  assert.equal(list.hidden, false, 'the knot is listed');
  const buttons = list.querySelectorAll('button').map((b) => b.textContent);
  assert.equal(JSON.stringify(buttons), JSON.stringify(['Close', 'Zoom to these', '5 × Zombie', '1 × Creeper']));
  // One chosen from the list is the one the card is about, and is drawn by itself.
  list.querySelectorAll('button').find((b) => b.textContent === '1 × Creeper').click();
  assert.equal(s.app.inspect.key(), 'm:350');
  assert.equal(list.hidden, true);
  assert.equal(marks(s).filter((m) => m.options.heap).find((m) => m.options.heap.key === knot.options.heap.key).options.heap.members.length, 5);
});

test('from the keyboard each group in turn is listed, nearest the middle first', async () => {
  const s = await stage({});
  assert.equal(s.app.groups.next(), false, 'nothing is grouped yet');
  await s.zoomTo(-2);
  await s.send({ players: [], mobs: [...Array.from({ length: 3 }, (_, i) => zombie(100 + i, 20 + i, 20)), ...Array.from({ length: 4 }, (_, i) => zombie(300 + i, 900 + i, 900))] });
  const list = listOf(s);
  assert.equal(s.app.groups.next(), true);
  assert.equal(list.hidden, false);
  assert.equal(titleOf(list), 'Group of 3');
  assert.equal(s.doc.activeElement.textContent, 'Zoom to these', 'the focus is in the list');
  assert.equal(s.app.groups.next(), true);
  assert.equal(titleOf(list), 'Group of 4');
  assert.equal(s.app.groups.next(), true);
  assert.equal(titleOf(list), 'Group of 3', 'and round again');
  list.dispatchEvent({ type: 'keydown', key: 'Escape' });
  assert.equal(list.hidden, true);
});

test('what another layer offers is grouped with the mobs, and told which of it was taken', async () => {
  const s = await stage({});
  await s.zoomTo(-2);
  const beds = Array.from({ length: 5 }, (_, i) => ({ id: { bed: i }, x: 12 + i, z: 14, family: 'beds', type: 'red' }));
  const lone = { id: { bed: 'far' }, x: 3000, z: 3000, family: 'beds', type: 'blue' };
  let taken = null;
  s.app.groups.offer('beds', { label: 'Beds', colour: () => '#f277b5', title: (sort) => `${sort} bed`, items: () => [...beds, lone], apply: (set) => { taken = set; } });
  await s.send({ players: [], mobs: Array.from({ length: 3 }, (_, i) => zombie(100 + i, 10 + i, 10)) });
  const heaps = marks(s).filter((m) => m.options.heap);
  assert.equal(heaps.length, 1, 'one mark for the mobs and the beds of a square');
  assert.equal(JSON.stringify(heaps[0].options.heap.families), JSON.stringify([['beds', 5], ['hostile', 3]]));
  assert.ok(beds.every((bed) => taken.has(bed.id)), 'the beds in it are taken off the map');
  assert.ok(!taken.has(lone.id), 'and the one by itself is not');
  // With grouping off nothing is taken.
  s.settings.set('look', { ...s.settings.get('look'), group: 'off' });
  await s.settle();
  assert.equal(marks(s).filter((m) => m.options.heap).length, 0);
  assert.equal(taken.size, 0);
});

test('with grouping off, or a type filtered, nothing hidden is counted in a group', async () => {
  const off = await stage({ look: { group: 'off' } });
  await off.zoomTo(-2);
  await off.send({ players: [], mobs: Array.from({ length: 6 }, (_, i) => zombie(100 + i, 10 + i, 10)) });
  assert.equal(marks(off).filter((m) => m.options.heap).length, 0);
  assert.equal(marks(off).length, 6);
  const s = await stage({});
  await s.zoomTo(-2);
  await s.send({ players: [], mobs: [...Array.from({ length: 6 }, (_, i) => zombie(100 + i, 10 + i, 10)), ...Array.from({ length: 4 }, (_, i) => ({ i: String(300 + i), t: 'creeper', x: 12 + i, y: 64, z: 11 }))] });
  const heap = () => marks(s).find((m) => m.options.heap);
  assert.equal(heap().options.heap.members.length, 10);
  // The zombies shown alone in the panel: the group is the zombies.
  s.win.mcmap.layers.facet('live', 'mobs').solo('zombie');
  await s.settle();
  assert.equal(heap().options.heap.members.length, 6);
  assert.ok(heap().options.heap.members.every((m) => m.type === 'zombie'));
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
