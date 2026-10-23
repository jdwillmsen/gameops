import test from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { CHROME, DIVERGING_MID, OTHER, SEQUENTIAL, SERIES, STATUS, SURFACE } from './palette.js';

const css = readFileSync(new URL('./charts.css', import.meta.url), 'utf8');

// charts.css has the light tokens under .cc-root and the dark tokens twice
// (OS preference and forced).
const [light, darkMedia, darkForced] = (() => {
  const base = css.slice(css.indexOf('.cc-root {'), css.indexOf('@media (prefers-color-scheme: dark)'));
  const m = css.indexOf('@media (prefers-color-scheme: dark)');
  const f = css.indexOf('[data-theme="dark"] .cc-root');
  return [base, css.slice(m, f), css.slice(f, css.indexOf('.cc-s1 {'))];
})();

const tokens = block => Object.fromEntries([...block.matchAll(/--(cc-[a-z0-9-]+):\s*([^;]+);/g)].map(m => [m[1], m[2].trim()]));
const L = tokens(light);
const D = { ...L, ...tokens(darkForced) };

test('light tokens match palette.js', () => {
  SERIES.light.forEach((hex, i) => assert.equal(L[`cc-series-${i + 1}`], hex, `series ${i + 1}`));
  SEQUENTIAL.light.forEach((hex, i) => assert.equal(L[`cc-seq-${i + 1}`], hex, `seq ${i + 1}`));
  assert.equal(L['cc-surface'], SURFACE.light);
  assert.equal(L['cc-other'], OTHER);
  assert.equal(L['cc-div-mid'], DIVERGING_MID.light);
  assert.equal(L['cc-div-neg'], SERIES.light[0]);
  assert.equal(L['cc-div-pos'], SERIES.light[7]);
  for (const [k, v] of Object.entries(STATUS)) assert.equal(L[`cc-status-${k}`], v, k);
  const c = CHROME.light;
  assert.deepEqual([L['cc-ink'], L['cc-ink-2'], L['cc-muted'], L['cc-grid'], L['cc-axis']], [c.ink, c.ink2, c.muted, c.grid, c.axis]);
});

test('dark tokens match palette.js', () => {
  SERIES.dark.forEach((hex, i) => assert.equal(D[`cc-series-${i + 1}`], hex, `series ${i + 1}`));
  SEQUENTIAL.dark.forEach((hex, i) => assert.equal(D[`cc-seq-${i + 1}`], hex, `seq ${i + 1}`));
  assert.equal(D['cc-surface'], SURFACE.dark);
  assert.equal(D['cc-div-mid'], DIVERGING_MID.dark);
  assert.equal(D['cc-div-neg'], SERIES.dark[0]);
  assert.equal(D['cc-div-pos'], SERIES.dark[7]);
  const c = CHROME.dark;
  assert.deepEqual([D['cc-ink'], D['cc-ink-2'], D['cc-muted'], D['cc-grid'], D['cc-axis']], [c.ink, c.ink2, c.muted, c.grid, c.axis]);
});

test('the OS-dark and forced-dark blocks never drift apart', () => {
  assert.deepEqual(tokens(darkMedia), tokens(darkForced));
  assert.ok(Object.keys(tokens(darkForced)).length > 30);
});

test('categorical order is eight distinct hexes per mode, with no dark/light swap-in', () => {
  for (const mode of ['light', 'dark']) {
    assert.equal(new Set(SERIES[mode]).size, 8);
    assert.ok(SERIES[mode].every(h => /^#[0-9a-f]{6}$/.test(h)));
  }
});

const lin = v => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; };
const lum = hex => { const n = parseInt(hex.slice(1), 16); return 0.2126 * lin(n >> 16) + 0.7152 * lin((n >> 8) & 255) + 0.0722 * lin(n & 255); };
const contrast = (a, b) => { const [x, y] = [lum(a), lum(b)].sort((p, q) => q - p); return (x + 0.05) / (y + 0.05); };

test('text tokens read at 4.5:1 on their surface, in both modes', () => {
  for (const [mode, surf] of [['light', SURFACE.light], ['dark', SURFACE.dark]]) {
    const c = CHROME[mode];
    assert.ok(contrast(c.ink, surf) >= 4.5, `${mode} ink`);
    assert.ok(contrast(c.ink2, surf) >= 4.5, `${mode} ink-2`);
    assert.ok(contrast(c.muted, surf) >= 3, `${mode} muted axis text`);
  }
});

test('in-cell label ink reads at 3:1 on every sequential step', () => {
  for (const [mode, tok] of [['light', L], ['dark', D]]) {
    SEQUENTIAL[mode].forEach((hex, i) => assert.ok(contrast(hex, tok[`cc-seq-ink-${i + 1}`]) >= 3, `${mode} step ${i + 1}`));
  }
});
