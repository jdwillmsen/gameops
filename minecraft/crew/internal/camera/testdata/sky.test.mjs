// Pins the time-of-day curves. Run by TestSkyCurves with `node --test`.
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';

const sky = await import(pathToFileURL(process.env.SKY_JS));

test('noon is fully bright and midnight fully dark', () => {
  assert.equal(sky.skyState(6000).bright, 1);
  assert.equal(sky.skyState(6000).dark, 0);
  assert.equal(sky.skyState(18000).bright, 0);
  assert.equal(sky.skyState(18000).dark, sky.MAX_SKY_DARK);
});

test('the sun is overhead at noon and rises in the east', () => {
  const noon = sky.skyState(6000).sun;
  assert.ok(noon[1] > .99, `noon sun height ${noon[1]}`);
  const dawn = sky.skyState(23000).sun;
  assert.ok(dawn[0] > 0, `dawn sun x ${dawn[0]} should be east`);
  const dusk = sky.skyState(13000).sun;
  assert.ok(dusk[0] < 0, `dusk sun x ${dusk[0]} should be west`);
});

test('the moon is opposite the sun and stars show only at night', () => {
  const s = sky.skyState(15000);
  s.sun.forEach((v, i) => assert.ok(Math.abs(v + s.moon[i]) < 1e-9));
  assert.equal(sky.skyState(6000).stars, 0);
  assert.ok(sky.skyState(18000).stars > .4);
});

test('the horizon glows at dusk and dawn but not at noon or midnight', () => {
  assert.ok(sky.skyState(12800).glow[3] > .3);
  assert.ok(sky.skyState(23000).glow[3] > .3);
  assert.equal(sky.skyState(6000).glow[3], 0);
  assert.equal(sky.skyState(18000).glow[3], 0);
});

test('time wraps, and a negative tick is the same time of day', () => {
  assert.deepEqual(sky.skyState(6000 + 24000 * 3).horizon, sky.skyState(6000).horizon);
  assert.deepEqual(sky.skyState(-18000).zenith, sky.skyState(6000).zenith);
});

test('the fog at night keeps a faint floor so the edge is not pure black', () => {
  const h = sky.skyState(18000).horizon;
  assert.ok(h.every(v => v > .04 && v < .12), `night fog ${h}`);
});

test('the clock runs at twenty ticks a second unless stopped', () => {
  let t = 1000;
  const c = sky.createClock(() => t);
  assert.equal(c.known, false);
  c.set(100, false);
  t += 2000;
  assert.equal(c.ticks(), 140);
  c.set(5000, true);
  t += 60000;
  assert.equal(c.ticks(), 5000);
  assert.equal(c.known, true);
});

test('underwater light darkens the water colour but never to black', () => {
  const day = sky.underwaterFog([.2, .4, 1], 1), night = sky.underwaterFog([.2, .4, 1], 0);
  assert.deepEqual(day, [.2, .4, 1]);
  assert.ok(night[2] > .3 && night[2] < 1);
});
