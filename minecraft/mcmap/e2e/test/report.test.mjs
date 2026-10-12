import assert from 'node:assert/strict';
import test from 'node:test';

import { routeOf } from '../lib/browser.mjs';
import { FAIL, PASS, Report, SKIP, scrub } from '../lib/report.mjs';

// What must never leave in a log or a metric, in the shapes it would
// arrive in. None of it is real.
const PRIVATE = ['-2575', '1830', '2535412345678901', 'Steve Builds', 'map.example.test', 'Lamb Chop'];

test('text is scrubbed of addresses, coordinates, ids and anything quoted', () => {
  const said = scrub('TimeoutError: page.goto https://map.example.test/#overworld/-2575/1830/0 waiting for "Steve Builds" at x=-2575 z=1830 xuid 2535412345678901 \'Lamb Chop\' #overworld/-2575/1830');
  for (const part of PRIVATE) assert.ok(!said.includes(part), `${part} survived: ${said}`);
  // What says where a fault is survives: a script's line, a small count.
  assert.equal(scrub('threw at live.js:503, 3 times'), 'threw at live.js:503, 3 times');
  assert.equal(scrub('héllo‮world'), 'h?llo?world');
  assert.ok(scrub('x'.repeat(5000)).length <= 300);
});

test('an address is written as its route and never its query', () => {
  for (const [address, route] of [
    ['https://map.example.test/api/structures/detail?dimension=overworld&kind=village&x=-2575&z=1830', '/api/structures/detail'],
    ['https://map.example.test/tiles/overworld/0/37/-4.webp', '/tiles'],
    ['https://map.example.test/api/biomes/tiles/overworld/0/1/2.png?biome=plains', '/api/biomes/tiles'],
    ['https://map.example.test/api/icons/head?name=Steve%20Builds&v=1', '/api/icons/head'],
    ['https://map.example.test/api/icons/picture/face/cow?v=1', '/api/icons/picture'],
    ['https://map.example.test/api/trails?dimension=overworld&player=Steve', '/api/trails'],
    ['https://map.example.test/live.js', '/live.js'],
    ['https://map.example.test/Steve%20Builds', 'unreadable'],
    ['not an address', 'unreadable'],
  ]) assert.equal(routeOf(address), route);
});

test('a result is a verdict, a time and numbers', () => {
  const lines = [];
  const report = new Report({ mode: 'local', version: '1.2.3', now: () => 1_800_000_000_000, write: (line) => lines.push(line) });
  report.record('follow-mob', FAIL, 1234.6, 'lost "Steve Builds" at -2575, 1830 on https://map.example.test/#overworld/-2575/1830/0', { worst_pixels: 5.5, where: 'x=-2575', 'Steve Builds': 1, nested: { x: 1 }, nan: Number.NaN });
  report.record('search', PASS, 10);
  report.record('player-heads', SKIP, 5, 'nobody is online in this dimension', { players: 0 });
  report.log('note', { name: 'Steve Builds', list: ['Steve Builds'], fn: () => 'Steve Builds', n: 3, version: '1.2.3' });
  report.gauge('mcmap_e2e_face_offset_pixels', 'help', 'face', { 'face/villager_v2': 1.5, 'Steve Builds': 2, 'x"y': 3 });
  report.summary();
  const metrics = report.metrics();
  const all = lines.join('\n') + metrics;
  for (const part of PRIVATE) assert.ok(!all.includes(part), `${part} is in the output`);
  assert.deepEqual(JSON.parse(lines[0]).measures, { worst_pixels: 5.5 });
  assert.equal(JSON.parse(lines[3]).n, 3);
  assert.equal(JSON.parse(lines[3]).version, '1.2.3');
  // A field this suite did not name is not written, whatever it holds.
  for (const key of ['name', 'list', 'fn']) assert.ok(!(key in JSON.parse(lines[3])), `${key} was logged`);
  assert.equal(report.failed, 1);
  for (const want of [
    'mcmap_e2e_run_success 0', 'mcmap_e2e_run_timestamp_seconds 1800000000', 'mcmap_e2e_run_info{version="1.2.3",mode="local"} 1',
    'mcmap_e2e_checks{status="pass"} 1', 'mcmap_e2e_checks{status="fail"} 1', 'mcmap_e2e_checks{status="skip"} 1',
    'mcmap_e2e_check_status{check="follow-mob"} 0', 'mcmap_e2e_check_status{check="search"} 1', 'mcmap_e2e_check_status{check="player-heads"} -1',
    'mcmap_e2e_measure{check="follow-mob",measure="worst_pixels"} 5.5', 'mcmap_e2e_face_offset_pixels{face="face/villager_v2"} 1.5',
  ]) assert.ok(metrics.includes(`${want}\n`), `no line ${want} in:\n${metrics}`);
  // Every line is a comment or a sample Prometheus will take.
  for (const line of metrics.trim().split('\n')) assert.match(line, /^(# (HELP|TYPE) [a-z_0-9]+ .+|[a-z_0-9]+(\{[a-z_]+="[A-Za-z0-9_/.+-]*"(,[a-z_]+="[A-Za-z0-9_/.+-]*")*\})? -?[0-9.]+)$/);
});
