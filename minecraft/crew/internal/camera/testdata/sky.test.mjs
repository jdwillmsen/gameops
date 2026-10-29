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

test('rain greys and dims the sky, hides the discs, and thunder goes further', () => {
  const clear = sky.skyState(6000), rain = sky.skyState(6000, 1, 0), storm = sky.skyState(6000, 1, 1);
  assert.deepEqual(sky.skyState(6000, 0, 0), clear);
  assert.equal(clear.disc, 1);
  assert.ok(rain.disc < .3, `disc ${rain.disc}`);
  const spread = c => Math.max(...c) - Math.min(...c);
  assert.ok(spread(rain.zenith) < spread(clear.zenith) / 3, 'rain sky is nearly grey');
  assert.ok(rain.zenith[2] < clear.zenith[2]);
  assert.ok(storm.zenith[2] < rain.zenith[2], 'a storm is darker than rain');
  assert.ok(storm.dark > rain.dark && rain.dark > clear.dark);
  assert.ok(storm.dark <= sky.MAX_SKY_DARK);
  assert.ok(storm.ambient < rain.ambient && rain.ambient < clear.ambient);
  assert.equal(sky.skyState(18000, 1, 1).dark, sky.MAX_SKY_DARK);
  assert.ok(sky.skyState(18000, 1, 0).stars < sky.skyState(18000).stars / 3);
});

test('the rain pool wraps around the camera and never writes outside its buffer', () => {
  const r = sky.createRain(100);
  assert.equal(r.count(0), 0);
  assert.equal(r.count(.5), 50);
  assert.equal(r.count(2), 100);
  for (const cam of [[0, 70, 0], [-1234.5, 12, 98765.25]]) {
    for (const t of [0, 1.7, 1000]) {
      const n = r.fill(r.count(1), cam[0], cam[1], cam[2], t);
      for (let i = 0; i < n; i++) {
        const o = i * 6, p = r.positions;
        assert.ok(Math.abs(p[o] - cam[0]) <= sky.RAIN_W / 2 + 1e-3, `x ${p[o]}`);
        assert.ok(Math.abs(p[o + 2] - cam[2]) <= sky.RAIN_W / 2 + 1e-3, `z ${p[o + 2]}`);
        assert.ok(p[o + 1] <= cam[1] + sky.RAIN_H * .35 + 1e-3 && p[o + 1] >= cam[1] - sky.RAIN_H * .65 - sky.RAIN_LEN - 1e-3, `y ${p[o + 1]}`);
        assert.ok(p[o + 1] - p[o + 4] > 1, 'a streak has length');
      }
    }
  }
  const a = r.positions.slice(0, 6); r.fill(1, 0, 70, 0, 0); const y0 = r.positions[1];
  r.fill(1, 0, 70, 0, .05);
  assert.ok(r.positions[1] < y0 || y0 - r.positions[1] > sky.RAIN_H / 2, 'streaks fall');
  assert.equal(r.positions.length, 600);
  void a;
});
