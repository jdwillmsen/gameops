import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtemp, readFile, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';

import { Fail, sleep, within } from '../lib/check.mjs';

const runner = new URL('../run.mjs', import.meta.url).pathname;

async function scratch() {
  const dir = await mkdtemp(path.join(tmpdir(), 'mcmap-e2e-test-'));
  const secret = path.join(dir, 'secret');
  await writeFile(secret, 'a-made-up-secret-for-the-runner-tests-0123456789');
  return { dir, secret, metrics: path.join(dir, 'metrics.prom') };
}

// The runner, run to its end: what it printed, what it complained of, how
// it exited.
function run(args, { env = {}, node = [] } = {}) {
  return new Promise((done) => {
    const child = spawn(process.execPath, [...node, runner, ...args], { stdio: ['ignore', 'pipe', 'pipe'], env: { ...process.env, ...env } });
    let out = '';
    let err = '';
    child.stdout.on('data', (chunk) => { out += chunk; });
    child.stderr.on('data', (chunk) => { err += chunk; });
    child.on('exit', (code, signal) => done({ code, signal, err, lines: out.trim().split('\n').filter(Boolean).map((line) => JSON.parse(line)) }));
  });
}

// Nothing is listening at either address. This needs the real browser: it
// is the browser failing to reach the page that has to be told properly.
test('a map that cannot be reached is a verdict, not a crash', async () => {
  const { secret, metrics } = await scratch();
  const got = await run(['--public-url', 'https://127.0.0.1:9', '--internal-url', 'http://127.0.0.1:9', '--secret-file', secret, '--insecure-local-tls', '--metrics-file', metrics]);
  assert.equal(got.code, 1, `exit ${got.code} ${got.signal}`);
  // Nothing of what the browser or the network said of it: an error's text
  // holds the address that was asked for.
  assert.equal(got.err, '');
  const of = (name) => got.lines.find((line) => line.event === 'check' && line.check === name);
  assert.deepEqual([of('login-screen').status, of('login-screen').reason], ['fail', 'the_map_could_not_be_reached']);
  assert.deepEqual([of('exchange').status, of('exchange').reason], ['fail', 'the_exchange_could_not_be_reached']);
  assert.equal(got.lines.at(-1).event, 'run');
  assert.deepEqual([got.lines.at(-1).finished, got.lines.at(-1).success, got.lines.at(-1).dropped_fields], [true, false, 0]);
  assert.ok(!got.lines.some((line) => line.reason === 'unlisted' || line.reason === 'the_check_itself_broke'), 'a fault with a name of its own was told as a broken check');
  const said = await readFile(metrics, 'utf8');
  for (const want of ['mcmap_e2e_run_finished 1', 'mcmap_e2e_run_success 0', 'mcmap_e2e_run_failed 1', 'mcmap_e2e_check_status{check="login-screen"} 0']) assert.ok(said.includes(`${want}\n`), `no line ${want} in:\n${said}`);
});

// A promise nobody is waiting on fails while the run is under way. The
// browser here is a stand-in that never comes up, so the run is waiting.
test('a fault nothing caught ends the run with its verdict and none of its words', async () => {
  const { dir, secret, metrics } = await scratch();
  const stuck = path.join(dir, 'browser');
  await writeFile(stuck, '#!/bin/sh\nexec sleep 15\n', { mode: 0o755 });
  const got = await run(['--public-url', 'https://127.0.0.1:9', '--internal-url', 'http://127.0.0.1:9', '--secret-file', secret, '--insecure-local-tls', '--metrics-file', metrics],
    { env: { MCMAP_E2E_CHROMIUM: stuck }, node: ['--import', new URL('./fixtures/unanswered.mjs', import.meta.url).href] });
  assert.equal(got.code, 2, `exit ${got.code} ${got.signal}`);
  assert.equal(got.err, '');
  assert.ok(!JSON.stringify(got.lines).includes('RAW-FAULT') && !JSON.stringify(got.lines).includes('4321'));
  const broke = got.lines.find((line) => line.event === 'check' && line.check === 'run');
  assert.deepEqual([broke.status, broke.reason], ['fail', 'the_run_itself_broke']);
  assert.deepEqual([got.lines.at(-1).event, got.lines.at(-1).finished, got.lines.at(-1).success], ['run', true, false]);
  const said = await readFile(metrics, 'utf8');
  for (const want of ['mcmap_e2e_run_finished 1', 'mcmap_e2e_run_success 0', 'mcmap_e2e_run_failed 1']) assert.ok(said.includes(`${want}\n`), `no line ${want} in:\n${said}`);
});

test('only an http or https address is taken for the internal listener', async () => {
  const { secret } = await scratch();
  for (const bad of ['file:///etc/passwd', 'ftp://map.example.test', 'javascript:alert(1)', 'map.example.test:9090', 'http://map.example.test/a/path', 'http://']) {
    const got = await run(['--public-url', 'https://127.0.0.1:9', '--internal-url', bad, '--secret-file', secret, '--insecure-local-tls']);
    assert.equal(got.code, 2, `${bad} was taken`);
    assert.match(got.err, /--internal-url must be/);
    assert.equal(got.lines.length, 0);
  }
});

test('the secret is sent in the clear only to this machine or into the cluster', async () => {
  const { secret } = await scratch();
  for (const far of ['http://map.example.test:9090', 'http://203.0.113.9:9090', 'http://map-internal.example.svc.evil.test']) {
    const got = await run(['--public-url', 'https://127.0.0.1:9', '--internal-url', far, '--secret-file', secret, '--insecure-local-tls']);
    assert.equal(got.code, 2, `${far} was taken`);
    assert.match(got.err, /must be https/);
    assert.equal(got.lines.length, 0);
  }
  // Each of these is taken, and the run goes on to fail for want of a map.
  const stuck = path.join((await scratch()).dir, 'browser');
  await writeFile(stuck, '#!/bin/sh\nexit 1\n', { mode: 0o755 });
  for (const near of ['http://127.0.0.1:9', 'http://map:9090', 'http://map.games.svc:9090', 'http://map.games.svc.cluster.local:9090', 'https://map.example.test']) {
    const got = await run(['--public-url', 'https://127.0.0.1:9', '--internal-url', near, '--secret-file', secret, '--insecure-local-tls'], { env: { MCMAP_E2E_CHROMIUM: stuck } });
    assert.ok(!/internal-url/.test(got.err), `${near} was refused`);
    assert.ok(got.lines.length > 0);
  }
});

test('a check past its time is stopped before the next begins', async () => {
  const state = { working: true, steps: 0, stopped: 0 };
  // A check that goes on pressing things for as long as its page is there.
  const check = async () => {
    while (state.working) { state.steps += 1; await sleep(5); }
    throw new Error('the page was closed under it');
  };
  const stop = async () => { state.stopped += 1; state.working = false; };
  await assert.rejects(within(check, 0.05, stop), (err) => err instanceof Fail && err.message === 'the check did not finish in its time' && err.measures.limit_seconds === 0.05);
  assert.equal(state.stopped, 1);
  // Stopped, and waited for: by the time the next check would begin it is
  // doing nothing more.
  const then = state.steps;
  await sleep(40);
  assert.equal(state.steps, then, 'the check went on working after it was stopped');

  // One that will not stop is waited for only so long, and what it does
  // next is answered and not left to fail the run.
  const deaf = within(() => new Promise((_, reject) => { setTimeout(() => reject(new Error('late')), 80); }), 0.01, async () => {}, 20);
  await assert.rejects(deaf, Fail);
  await sleep(100);

  // In time, its answer is its own, either way.
  assert.deepEqual(await within(async () => ({ measures: { hits: 1 } }), 1, stop), { measures: { hits: 1 } });
  await assert.rejects(within(async () => { throw new Fail('the layer panel did not open'); }, 1, stop), /the layer panel did not open/);
  assert.equal(state.stopped, 1);
});
