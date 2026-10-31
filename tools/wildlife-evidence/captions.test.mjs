// node --test tools/wildlife-evidence
import { test } from 'node:test';
import assert from 'node:assert/strict';
import { captions, parseTimeline, toAss, toSrt } from './captions.mjs';

const tl = [
  '{"t_ms": 500, "source": "harness", "scenario": "tame", "event": "tame_requested", "animal": "tiger"}',
  '{"t_ms": 2500, "source": "server", "scenario": "tame", "event": "tamed", "owner": "cam"}',
  '{"t_ms": 3000, "source": "bot", "scenario": "other", "event": "ignored"}',
  '{"t_ms": 9000, "source": "harness", "scenario": "tame", "event": "late"}',
  '{"t_ms": 400',
  'not json',
].join('\n');

test('a timeline is read in time order and a torn last line is skipped', () => {
  const ev = parseTimeline(tl);
  assert.deepEqual(ev.map(e => e.t_ms), [500, 2500, 3000, 9000]);
});

test('captions keep one scenario, shift to the clip origin and end at the next event', () => {
  const caps = captions(parseTimeline(tl), { scenario: 'tame', originMs: 1000, durationMs: 6000 });
  assert.equal(caps.length, 2);
  assert.equal(caps[0].text, '[harness] tame_requested animal=tiger');
  assert.equal(caps[0].start, 0);
  assert.equal(caps[0].end, 1500);
  assert.equal(caps[1].start, 1500);
  assert.equal(caps[1].text, '[server] tamed owner=cam');
  assert.ok(caps[1].end <= 6000 && caps[1].end - caps[1].start >= 1500);
});

test('events outside the clip are dropped', () => {
  assert.equal(captions(parseTimeline(tl), { scenario: 'tame', originMs: 0, durationMs: 2000 }).length, 1);
});

test('srt and ass carry the text, and the ass always carries the banner', () => {
  const caps = [{ start: 61500, end: 63000, text: 'a {b} c' }];
  assert.equal(toSrt(caps), '1\n00:01:01,500 --> 00:01:03,000\na {b} c\n');
  const ass = toAss(caps, { width: 1280, height: 720, banner: 'software re-render', title: 'tiger', durationMs: 70000 });
  assert.match(ass, /Banner,,0,0,0,,software re-render/);
  assert.match(ass, /Title,,0,0,0,,tiger/);
  assert.match(ass, /0:01:01\.50,0:01:03\.00,Cap,,0,0,0,,a  b  c/);
});
