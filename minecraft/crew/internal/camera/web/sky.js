// Time of day to sky, fog and light, as pure functions so a node test can pin
// them. Colours are sRGB triples in 0..1; the page converts them. The curves
// are the game's own (a day is 24000 ticks, tick 0 is sunrise), with the sun
// on an east-west arc.
export const DAY = 24000;
export const TICKS_PER_SECOND = 20;
// How many levels of sky light the darkest night removes.
export const MAX_SKY_DARK = 11;

const BASE_ZENITH = [0x78 / 255, 0xa7 / 255, 1];
const BASE_FOG = [0xc0 / 255, 0xd8 / 255, 1];

const clamp01 = v => Math.min(1, Math.max(0, v));

// The sun's turn through the day, 0 at noon. It is eased so dusk and dawn
// linger, as they do in the game.
export function celestialAngle(ticks) {
  const t = ((ticks % DAY) + DAY) % DAY;
  const d = (t / DAY - .25 + 1) % 1;
  const e = .5 - Math.cos(d * Math.PI) / 2;
  return d + (e - d) / 3;
}

export function skyState(ticks, rain = 0, thunder = 0) {
  const a = celestialAngle(ticks), turn = a * 2 * Math.PI;
  const c = Math.cos(turn);
  const bright = clamp01(c * 2 + .5);
  const fogK = [bright * .94 + .06, bright * .94 + .06, bright * .91 + .09];
  const stars = clamp01(1 - (c * 2 + .25));

  let glow = [0, 0, 0, 0];
  if (c >= -.4 && c <= .4) {
    const g = c / .4 * .5 + .5;
    const alpha = 1 - (1 - Math.sin(g * Math.PI)) * .99;
    glow = [g * .3 + .7, g * g * .7 + .2, .2, alpha * alpha];
  }
  // x grows to the east: the sun rises there and sets in the west.
  const sun = [-Math.sin(turn), c, 0];
  const w = weatherSky(rain, thunder);
  const toGrey = (col, k) => {
    const grey = (col[0] * .3 + col[1] * .59 + col[2] * .11) * k;
    return col.map(v => v + (grey - v) * w.grey);
  };
  return {
    angle: a,
    bright: bright * w.light,
    dark: Math.min(MAX_SKY_DARK, (1 - bright) * MAX_SKY_DARK + w.darken),
    zenith: toGrey(BASE_ZENITH.map(v => v * bright), w.sky),
    horizon: toGrey(BASE_FOG.map((v, i) => v * fogK[i]), w.sky),
    glow: [glow[0], glow[1], glow[2], glow[3] * (1 - w.grey)],
    sun,
    moon: sun.map(v => -v),
    stars: stars * stars * .5 * (1 - w.grey),
    // How strongly the sun and moon discs show through cloud.
    disc: 1 - w.grey,
    // How strongly entities, which carry no baked light, are lit.
    ambient: (.3 + .7 * bright) * w.light,
    wet: w.wet,
  };
}

// How rain (0..1) and thunder (0..1) change the sky: the colours drain to a
// darker grey, the discs and stars are hidden, and sky light drops a few levels.
export function weatherSky(rain, thunder) {
  const r = clamp01(rain), t = clamp01(thunder);
  const wet = Math.max(r, t);
  return {
    wet,
    grey: wet * .8,
    sky: 1 - .35 * r - .3 * t,
    light: 1 - .3 * r - .3 * t,
    darken: 2 * r + 2 * t,
  };
}

// A clock the page keeps between the server's time frames: it runs on at the
// game's rate unless the server says the day cycle is stopped.
export function createClock(now) {
  let base = 6000, at = 0, stopped = true, known = false;
  return {
    set(ticks, isStopped) {
      base = ticks; at = now(); stopped = isStopped; known = true;
    },
    ticks() {
      if (stopped) return base;
      return base + (now() - at) * TICKS_PER_SECOND / 1000;
    },
    get known() { return known; },
  };
}

// The tint of everything seen from inside water: the biome's colour, darkened
// by the light that reaches the camera, with the fog closing in a few blocks.
export const UNDERWATER_FAR = 24;
export function underwaterFog(water, bright) {
  const k = .35 + .65 * bright;
  return water.map(v => v * k);
}

// Rain is a fixed pool of streaks laid out once; each frame only rewrites the
// vertex buffer. A streak's column is a fixed point of a lattice around the
// camera, so it stays put in the world as the camera moves, and it falls by
// wrapping through a box of RAIN_H blocks.
export const RAIN_MAX = 600;
export const RAIN_W = 28;
export const RAIN_H = 20;
export const RAIN_SPEED = 22;
export const RAIN_LEN = 1.4;
const RAIN_SLANT = .12;

export function createRain(max = RAIN_MAX) {
  const ox = new Float32Array(max), oy = new Float32Array(max), oz = new Float32Array(max), v = new Float32Array(max);
  // A fixed generator so the layout does not depend on page load.
  let s = 12345;
  const rnd = () => (s = (s * 1664525 + 1013904223) >>> 0) / 4294967296;
  for (let i = 0; i < max; i++) { ox[i] = rnd() * RAIN_W; oz[i] = rnd() * RAIN_W; oy[i] = rnd() * RAIN_H; v[i] = .8 + rnd() * .4; }
  const wrap = (x, size) => ((x % size) + size) % size;
  return {
    max,
    positions: new Float32Array(max * 6),
    // How many streaks a rain level draws.
    count(level) { return Math.round(max * clamp01(level)); },
    // Writes n streaks around (cx, cy, cz) at time t seconds; two vertices each.
    fill(n, cx, cy, cz, t) {
      const p = this.positions;
      for (let i = 0; i < n; i++) {
        const x = cx + wrap(ox[i] - cx, RAIN_W) - RAIN_W / 2;
        const z = cz + wrap(oz[i] - cz, RAIN_W) - RAIN_W / 2;
        const y = cy + RAIN_H * .35 - wrap(oy[i] - cy + RAIN_SPEED * v[i] * t, RAIN_H);
        const o = i * 6;
        p[o] = x; p[o + 1] = y; p[o + 2] = z;
        p[o + 3] = x - RAIN_SLANT * RAIN_LEN; p[o + 4] = y - RAIN_LEN; p[o + 5] = z;
      }
      return n;
    },
  };
}
