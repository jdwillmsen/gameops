import { test } from 'node:test';
import assert from 'node:assert/strict';
import { attach } from './attach-videos.mjs';

test('a scenario with a video gets it and its captions; one without is reported, not invented', () => {
  const have = new Set(['videos/tame.mp4', 'videos/tame.srt']);
  const report = { scenarios: [{ id: 'tame', artifacts: ['x.log'] }, { id: 'sit' }] };
  const r = attach(report, rel => have.has(rel));
  assert.deepEqual(r, { attached: ['tame'], missing: ['sit'] });
  assert.deepEqual(report.scenarios[0].artifacts, ['x.log', 'videos/tame.mp4', 'videos/tame.srt']);
  assert.deepEqual(report.scenarios[1].artifacts, []);
  attach(report, rel => have.has(rel));
  assert.equal(report.scenarios[0].artifacts.length, 3, 'attaching twice adds nothing');
});
