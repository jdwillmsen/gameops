import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdir, mkdtemp, readFile, readdir, utimes, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';

import { Evidence, prune } from '../lib/evidence.mjs';
import { SESSION_COOKIE, browserCookie } from '../lib/session.mjs';

const scratch = () => mkdtemp(path.join(tmpdir(), 'mcmap-e2e-test-'));
// A page as the runner uses one: a picture, and the HTML it is showing.
const page = (html, shot = Buffer.alloc(1000, 1)) => ({ isClosed: () => false, screenshot: async () => shot, content: async () => html });

test('by default a failure leaves a picture and nothing else', async () => {
  const dir = path.join(await scratch(), 'run');
  const evidence = new Evidence(dir, { pages: false, maxBytes: 1 << 20 });
  await evidence.of('search', page('<html>where everyone lives</html>'), new Error('what went wrong, with a name in it'));
  assert.deepEqual(await readdir(dir), ['search.png']);
  assert.equal(evidence.files, 1);
});

test('the session cookie is in nothing that is written, asked for or not', async () => {
  // Held from here on, as it is once an exchange has answered.
  const { value } = browserCookie(`${SESSION_COOKIE}=cookie-payload-0123456789.signature-0123456789; Path=/; Max-Age=600; HttpOnly; Secure; SameSite=Lax`, 'https://map.example.test');
  const dir = path.join(await scratch(), 'run');
  const evidence = new Evidence(dir, { pages: true, maxBytes: 1 << 20 });
  await evidence.of('session', page(`<html>document.cookie = "${SESSION_COOKIE}=${value}"</html>`), new Error(`Cookie: ${SESSION_COOKIE}=${value}`));
  await evidence.put('seen.json', JSON.stringify([{ kind: 'response', url: `https://map.example.test/?c=${value}` }]));
  const names = (await readdir(dir)).sort();
  assert.deepEqual(names, ['seen.json', 'session.html', 'session.png', 'session.txt']);
  for (const name of names) assert.ok(!(await readFile(path.join(dir, name), 'latin1')).includes(value), `${name} holds the session cookie`);
  assert.ok((await readFile(path.join(dir, 'session.html'), 'utf8')).includes('[redacted]'));
});

test('evidence is bounded, and a volume that will not take it costs only the evidence', async () => {
  const dir = path.join(await scratch(), 'run');
  const capped = new Evidence(dir, { pages: true, maxBytes: 1500 });
  await capped.of('search', page('x'.repeat(5000)), new Error('short'));
  assert.deepEqual((await readdir(dir)).sort(), ['search.png', 'search.txt'], 'the page that would pass the limit was written');
  assert.equal(capped.errors, 1);

  // A directory that cannot be made stands in for one that is full.
  const blocked = path.join(await scratch(), 'file');
  await writeFile(blocked, 'x');
  const full = new Evidence(path.join(blocked, 'run'), { pages: true, maxBytes: 1 << 20 });
  await full.of('search', page('<html></html>'), new Error('short'));
  assert.equal(full.files, 0);
  assert.equal(full.errors, 3);
  const broken = new Evidence(dir, { pages: false, maxBytes: 1 << 20 });
  await broken.of('themes', { isClosed: () => false, screenshot: async () => { throw new Error('no picture'); }, content: async () => '' }, new Error('short'));
  assert.equal(broken.errors, 1);
  // With no directory nothing is kept at all.
  const off = new Evidence(null, { pages: true, maxBytes: 1 << 20 });
  await off.of('search', page('<html></html>'), new Error('short'));
  assert.equal(off.files + off.errors, 0);
});

test('only the newest few failed runs are kept, and none past their time', async () => {
  const dir = await scratch();
  const day = 86_400_000;
  for (const [name, age] of [['run-20260101T000000Z', 10 * day], ['run-20260108T000000Z', 3 * day], ['run-20260109T000000Z', 2 * day], ['run-20260110T000000Z', day], ['notes', 30 * day]]) {
    await mkdir(path.join(dir, name));
    await writeFile(path.join(dir, name, 'x.png'), 'x');
    const at = new Date(Date.now() - age);
    await utimes(path.join(dir, name), at, at);
  }
  await prune(dir, 3, 7);
  // Room for this run's, so one fewer than the limit; what is not a run's is not touched.
  assert.deepEqual((await readdir(dir)).sort(), ['notes', 'run-20260109T000000Z', 'run-20260110T000000Z']);
});

// The whole runner, stopped the way a deadline stops a pod. Nothing here
// needs a browser or a map: the run is stopped before it gets to either.
test('a run that is stopped leaves its verdict, and one that is killed leaves no success standing', async () => {
  const dir = await scratch();
  const secret = path.join(dir, 'secret');
  await writeFile(secret, 'a-made-up-secret-for-the-runner-tests-0123456789');
  const metrics = path.join(dir, 'metrics.prom');
  // What the run before left: a success, which must not outlive this run's start.
  await writeFile(metrics, 'mcmap_e2e_run_success 1\nmcmap_e2e_run_finished 1\n');
  // In place of a browser, something that never comes up: the run is left
  // waiting on it, which is where a deadline would find it.
  const stuck = path.join(dir, 'browser');
  await writeFile(stuck, '#!/bin/sh\nexec sleep 15\n', { mode: 0o755 });
  const start = () => spawn(process.execPath, [new URL('../run.mjs', import.meta.url).pathname, '--public-url', 'https://127.0.0.1:9', '--internal-url', 'http://127.0.0.1:9',
    '--secret-file', secret, '--insecure-local-tls', '--metrics-file', metrics], { stdio: ['ignore', 'pipe', 'ignore'], env: { ...process.env, MCMAP_E2E_CHROMIUM: stuck } });
  const begun = async (child) => {
    for await (const chunk of child.stdout) if (String(chunk).includes('"event":"start"')) return;
  };
  const read = () => readFile(metrics, 'utf8');
  const ended = (child) => new Promise((done) => { child.on('exit', (code, signal) => done({ code, signal })); });

  const killed = start();
  await begun(killed);
  killed.kill('SIGKILL');
  await ended(killed);
  const left = await read();
  assert.ok(left.includes('mcmap_e2e_run_success 0\n') && left.includes('mcmap_e2e_run_finished 0\n'), `a killed run left:\n${left}`);
  assert.ok(/mcmap_e2e_run_started_timestamp_seconds \d{10}\n/.test(left));

  const stopped = start();
  const lines = [];
  stopped.stdout.on('data', (chunk) => lines.push(String(chunk)));
  await new Promise((done) => { const wait = setInterval(() => { if (lines.join('').includes('"event":"start"')) { clearInterval(wait); done(); } }, 20); });
  stopped.kill('SIGTERM');
  assert.deepEqual(await ended(stopped), { code: 2, signal: null });
  const said = await read();
  for (const want of ['mcmap_e2e_run_finished 1', 'mcmap_e2e_run_success 0', 'mcmap_e2e_run_failed 1', 'mcmap_e2e_check_reason{check="run",reason="the_run_was_stopped_before_it_finished"} 1']) {
    assert.ok(said.includes(`${want}\n`), `no line ${want} after a stopped run:\n${said}`);
  }
  assert.match(lines.join(''), /"event":"run".*"finished":true.*"success":false/);
});
