import assert from 'node:assert/strict';
import { readFile, readdir } from 'node:fs/promises';
import { mkdtemp, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';

import { routeOf } from '../lib/browser.mjs';
import { CHECKS, MEASURES, REASONS, slug } from '../lib/fields.mjs';
import { FAIL, PASS, Report, SKIP } from '../lib/report.mjs';

// What must never leave in a log or a metric, in the shapes it would
// arrive in. None of it is real. The last is the one a scrubber of text
// cannot catch: a name is a word and a small coordinate is a small number.
const PRIVATE = ['-4321', '8765', '1111111111111111', 'Madeup', 'map.example.test', 'Invented Sheep', 'is at 5 -7'];
const SAID = 'Madeup is at 5 -7 near "Invented Sheep" on https://map.example.test/#overworld/-4321/8765/0 xuid 1111111111111111';

function fresh() {
  const lines = [];
  let at = 1_800_000_000_000;
  const report = new Report({ mode: 'local', version: '1.2.3', now: () => at, write: (line) => lines.push(line) });
  return { report, lines, later: (ms) => { at += ms; } };
}

test('there is no field a word off the page can be written in', () => {
  const { report, lines } = fresh();
  // Every place a check hands something to the reporter, handed page text.
  report.record('follow-mob', FAIL, 1234.6, SAID, { worst_pixels: 5.5, [SAID]: 1, where: 5, players: SAID, nested: { x: 1 }, nan: Number.NaN });
  report.record(SAID, PASS, 10);
  report.record('search', SAID, 10, '', {});
  report.stopped(SAID);
  report.begin(SAID);
  report.gauge('mcmap_e2e_face_offset_pixels', { 'face/villager_v2': 1.5, [SAID]: 2, 'face/Madeup': 3, 'head/madeup': 4, 'x"y': 5 });
  report.gauge('mcmap_e2e_api_seconds', { map: 0.1, [SAID]: 1 });
  report.gauge(SAID, { map: 1 });
  report.finish();
  report.summary({ note: SAID, evidence_files: SAID });
  const all = lines.join('\n') + report.metrics();
  for (const part of PRIVATE) assert.ok(!all.includes(part), `${JSON.stringify(part)} is in the output:\n${all}`);
  assert.deepEqual(JSON.parse(lines[0]), { time: '2027-01-15T08:00:00.000Z', event: 'check', mode: 'local', version: '1.2.3', check: 'follow-mob', status: 'fail', ms: 1235, reason: 'unlisted', measures: { worst_pixels: 5.5 } });
  assert.equal(JSON.parse(lines[1]).check, 'unlisted');
  assert.equal(JSON.parse(lines[2]).status, 'fail', 'a verdict that is not one is a failure, not a pass');
  // Each of those was counted, so a suite that tries it cannot pass quietly.
  assert.ok(report.dropped >= 12, `only ${report.dropped} counted as dropped`);
  assert.match(report.metrics(), /^mcmap_e2e_dropped_fields \d\d$/m);
  // Every line of the metrics is a comment or a sample Prometheus will take.
  for (const line of report.metrics().trim().split('\n')) assert.match(line, /^(# (HELP|TYPE) [a-z_0-9]+ .+|[a-z_0-9]+(\{[a-z_]+="[A-Za-z0-9_/.+-]*"(,[a-z_]+="[A-Za-z0-9_/.+-]*")*\})? -?[0-9.]+)$/);
});

// A check is handed the report. The one method that writes whatever it is
// given is not there for it to call.
test('a check cannot reach the line writer', () => {
  const { report, lines } = fresh();
  assert.equal(report.log, undefined);
  assert.equal(report['#log'], undefined);
  for (const name of Object.getOwnPropertyNames(Object.getPrototypeOf(report))) assert.ok(['constructor', 'measures', 'reason', 'begin', 'stopped', 'record', 'gauge', 'failed', 'finish', 'metrics', 'writeMetrics', 'summary'].includes(name), `${name} is a new way to write that this test has not been told of`);
  assert.equal(lines.length, 0);
});

test('a reason is one of the catalogue, written as its name', () => {
  const { report, lines } = fresh();
  report.record('live-stream', FAIL, 5, 'the live stream was cut and had to reconnect', { reconnects: 2 });
  report.record('player-heads', SKIP, 5, 'nobody is online in this dimension', { players: 0 });
  assert.equal(JSON.parse(lines[0]).reason, 'the_live_stream_was_cut_and_had_to_reconnect');
  assert.equal(JSON.parse(lines[1]).reason, 'nobody_is_online_in_this_dimension');
  assert.equal(report.dropped, 0);
  assert.ok(report.metrics().includes('mcmap_e2e_check_reason{check="live-stream",reason="the_live_stream_was_cut_and_had_to_reconnect"} 1\n'));
  for (const code of REASONS.keys()) assert.match(code, /^[a-z0-9_]{3,90}$/);
});

// The catalogue is closed in fact and not only in name: every reason and
// every measure the checks give is in it, written out, and none is put
// together from anything.
test('every reason and measure the checks use is in the closed sets', async () => {
  const dir = new URL('../checks/', import.meta.url);
  const files = [...(await readdir(dir)).map((name) => new URL(name, dir)), new URL('../run.mjs', import.meta.url)];
  let reasons = 0;
  for (const file of files) {
    const source = await readFile(file, 'utf8');
    assert.ok(!/(new Fail|skip)\(\s*`/.test(source), `${file.pathname} makes a reason up from pieces`);
    assert.ok(!/(new Fail|skip)\(\s*[a-zA-Z_$][\w$.]*\s*[,)+]/.test(source.replace(/skip\(kind === 'player' \?/g, '')), `${file.pathname} gives a reason that is not written out`);
    for (const [, , said] of source.matchAll(/(new Fail|skip)\(\s*'((?:[^'\\]|\\.)*)'/g)) {
      reasons += 1;
      assert.ok(REASONS.has(slug(said.replaceAll("\\'", "'"))), `${file.pathname}: ${JSON.stringify(said)} is not in the catalogue`);
    }
    for (const [, , said] of source.matchAll(/(new Fail|skip)\(\s*"((?:[^"\\]|\\.)*)"/g)) {
      reasons += 1;
      assert.ok(REASONS.has(slug(said)), `${file.pathname}: ${JSON.stringify(said)} is not in the catalogue`);
    }
    for (const [, name] of source.matchAll(/await run\('([a-z-]+)'/g)) assert.ok(CHECKS.includes(name), `${name} is not a listed check`);
  }
  assert.ok(reasons > 80, `only ${reasons} reasons were found to check`);
  assert.ok(MEASURES.size > 90);
});

test('a run that has begun and not finished says so, and is no success', async () => {
  const { report, later } = fresh();
  const dir = await mkdtemp(path.join(tmpdir(), 'mcmap-e2e-test-'));
  const file = path.join(dir, 'metrics.prom');
  assert.equal(await report.writeMetrics(file), true);
  const begun = await readFile(file, 'utf8');
  for (const want of ['mcmap_e2e_run_started_timestamp_seconds 1800000000', 'mcmap_e2e_run_finished_timestamp_seconds 0', 'mcmap_e2e_run_finished 0', 'mcmap_e2e_run_success 0', 'mcmap_e2e_run_failed 0']) {
    assert.ok(begun.includes(`${want}\n`), `no line ${want} in a run that has only begun:\n${begun}`);
  }
  report.record('search', PASS, 10, '', { hits: 2 });
  later(90_000);
  report.finish();
  await report.writeMetrics(file);
  const done = await readFile(file, 'utf8');
  for (const want of ['mcmap_e2e_run_finished_timestamp_seconds 1800000090', 'mcmap_e2e_run_finished 1', 'mcmap_e2e_run_success 1', 'mcmap_e2e_run_failed 0', 'mcmap_e2e_run_duration_seconds 90.0',
    'mcmap_e2e_checks{status="pass"} 1', 'mcmap_e2e_check_status{check="search"} 1', 'mcmap_e2e_measure{check="search",measure="hits"} 2', 'mcmap_e2e_run_info{version="1.2.3",mode="local"} 1']) {
    assert.ok(done.includes(`${want}\n`), `no line ${want} in:\n${done}`);
  }
  const failed = fresh();
  failed.report.record('search', FAIL, 10, 'a search for something the world holds found nothing');
  assert.ok(failed.report.metrics().includes('mcmap_e2e_run_failed 1\n'), 'a failed check is not told until the run ends');
  failed.report.finish();
  assert.ok(failed.report.metrics().includes('mcmap_e2e_run_success 0\n'));
});

test('a volume that cannot be written does not stop the run saying how it went', async () => {
  const { report, lines } = fresh();
  // Under a file, where nothing can be made: it stands in for a volume that is full.
  const blocked = path.join(await mkdtemp(path.join(tmpdir(), 'mcmap-e2e-test-')), 'file');
  await writeFile(blocked, 'x');
  assert.equal(await report.writeMetrics(path.join(blocked, 'metrics.prom')), false);
  report.finish();
  report.summary();
  assert.equal(JSON.parse(lines.at(-1)).finished, true);
});

test('an address is written as its route and never its query', () => {
  for (const [address, route] of [
    ['https://map.example.test/api/structures/detail?dimension=overworld&kind=village&x=-4321&z=8765', '/api/structures/detail'],
    ['https://map.example.test/tiles/overworld/0/37/-4.webp', '/tiles'],
    ['https://map.example.test/api/biomes/tiles/overworld/0/1/2.png?biome=plains', '/api/biomes/tiles'],
    ['https://map.example.test/api/icons/head?name=Madeup%20Player&v=1', '/api/icons/head'],
    ['https://map.example.test/api/icons/picture/face/cow?v=1', '/api/icons/picture'],
    ['https://map.example.test/live.js', '/live.js'],
    ['https://map.example.test/Madeup%20Player', 'unreadable'],
    ['not an address', 'unreadable'],
  ]) assert.equal(routeOf(address), route);
});
