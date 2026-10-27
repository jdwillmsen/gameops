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

export function skyState(ticks) {
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
  return {
    angle: a,
    bright,
    dark: (1 - bright) * MAX_SKY_DARK,
    zenith: BASE_ZENITH.map(v => v * bright),
    horizon: BASE_FOG.map((v, i) => v * fogK[i]),
    glow,
    sun,
    moon: sun.map(v => -v),
    stars: stars * stars * .5,
    // How strongly entities, which carry no baked light, are lit.
    ambient: .3 + .7 * bright,
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
