// The quality table the camera page renders from. The console keeps its own
// copy of the level names because the two bundles ship apart; this test also
// compares them when the console file is passed in, so the copies cannot drift.
import test from 'node:test';
import assert from 'node:assert/strict';
import { pathToFileURL } from 'node:url';

const camera = await import(pathToFileURL(process.env.QUALITY_JS).href);
const console_ = process.env.CONSOLE_QUALITY_JS
  ? await import(pathToFileURL(process.env.CONSOLE_QUALITY_JS).href)
  : null;

test('levels are ordered cheapest first', () => {
  assert.deepEqual(camera.QUALITIES, ['low', 'med', 'high']);
  assert.equal(camera.DEFAULT_QUALITY, 'high');
});

test('each level costs what the ticket says', () => {
  assert.deepEqual(camera.QUALITY.low, { pixelRatio: .5, antialias: false, radius: 2, fps: 10, entities: false, items: false, hud: false });
  assert.deepEqual(camera.QUALITY.med, { pixelRatio: 1, antialias: false, radius: 3, fps: 20, entities: true, items: false, hud: false });
  assert.deepEqual(camera.QUALITY.high, { pixelRatio: 2, antialias: true, radius: 4, fps: 0, entities: true, items: true, hud: true });
});

test('only the exact level names are recognised', () => {
  for (const q of ['low', 'med', 'high']) {
    assert.equal(camera.qualityOf(q), q);
    assert.equal(camera.qualitySettings(q), camera.QUALITY[q]);
  }
  for (const q of ['', 'LOW', 'ultra', null, undefined]) {
    assert.equal(camera.qualityOf(q), 'high', String(q));
    assert.equal(camera.qualitySettings(q), camera.QUALITY.high);
  }
});

test('the console names the same levels and default', { skip: !console_ }, () => {
  assert.deepEqual(console_.QUALITIES, camera.QUALITIES);
  assert.equal(console_.DEFAULT_QUALITY, camera.DEFAULT_QUALITY);
  assert.equal(console_.qualityOf('bogus'), camera.DEFAULT_QUALITY);
  for (const q of camera.QUALITIES) assert.equal(console_.qualityOf(q), q);
});
