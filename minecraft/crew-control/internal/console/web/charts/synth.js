// Seeded synthetic data. The gallery doubles as a visual regression fixture,
// so the same seed must always draw the same pixels.

export function rng(seed) {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

export const NOW = Date.UTC(2026, 9, 23, 12, 0, 0);
export const STEP = 5 * 60 * 1000;

export const UNITS = ['Agate', 'Basalt', 'Cinnabar', 'Diorite', 'Epidote', 'Feldspar', 'Garnet', 'Hematite', 'Jasper', 'Kyanite', 'Lazurite', 'Malachite', 'Nephrite', 'Obsidian', 'Peridot', 'Quartz', 'Rhodonite', 'Sphalerite', 'Topaz', 'Unakite'];

export function times(n, step = STEP, end = NOW) {
  return Array.from({ length: n }, (_, i) => end - (n - 1 - i) * step);
}

// A bounded random walk with a slow daily swell.
export function walk(rand, n, { base = 50, vol = 6, min = 0, max = Infinity, swell = 0 } = {}) {
  let v = base;
  return Array.from({ length: n }, (_, i) => {
    v += (rand() - 0.5) * vol + (base - v) * 0.04;
    v = Math.max(min, Math.min(max, v));
    return Math.round((v + swell * Math.sin((i / n) * Math.PI * 2)) * 10) / 10;
  });
}

export function series(rand, names, n, opts) {
  return names.map((name, i) => ({ key: name.toLowerCase(), name, values: walk(rand, n, { ...opts, base: (opts?.base ?? 50) * (0.5 + rand()) , vol: (opts?.vol ?? 6) * (0.6 + rand() * 0.8) }) }));
}

export function grid(rand, rows, cols, peak = 100) {
  return Array.from({ length: rows }, (_, r) => Array.from({ length: cols }, (_, c) => {
    const day = Math.sin(((c - 6) / 24) * Math.PI * 2) * 0.5 + 0.5;
    return Math.round(peak * Math.max(0, day * (0.5 + rand() * 0.5) * (r > 4 ? 0.6 : 1)));
  }));
}
