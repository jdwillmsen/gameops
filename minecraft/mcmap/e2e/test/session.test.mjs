import assert from 'node:assert/strict';
import { chmod, mkdtemp, writeFile } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';

import { SESSION_COOKIE, browserCookie, exchange, parseSecretRef, parseSetCookie, redact } from '../lib/session.mjs';

// Made up, like every credential in these tests.
const SECRET = 'a-made-up-secret-for-the-runner-tests-0123456789';
const GOOD = `${SESSION_COOKIE}=payload.signature; Path=/; Max-Age=600; HttpOnly; Secure; SameSite=Lax`;

const answer = (status, { cookies = [GOOD], identity = 'service:e2e' } = {}) => {
  const headers = new Headers();
  for (const cookie of cookies) headers.append('Set-Cookie', cookie);
  return new Response(JSON.stringify({ identity, ttlSeconds: 600, expiresAt: '2026-01-01T00:00:00Z' }), { status, headers });
};

async function secretFile() {
  const dir = await mkdtemp(path.join(tmpdir(), 'mcmap-e2e-test-'));
  const file = path.join(dir, 'secret');
  await writeFile(file, `${SECRET}\n`, { mode: 0o600 });
  return { dir, file };
}

test('a cookie is read as a browser reads it', () => {
  const cookie = parseSetCookie(GOOD);
  assert.equal(cookie.name, SESSION_COOKIE);
  assert.equal(cookie.value, 'payload.signature');
  assert.deepEqual(cookie.attributes, { path: '/', 'max-age': '600', httponly: true, secure: true, samesite: 'Lax' });
});

test('the cookie is installed for the public host with what the server set, and nothing weaker', () => {
  const cookie = browserCookie(GOOD, 'https://map.example.test');
  assert.deepEqual({ ...cookie, expires: 0 }, { name: SESSION_COOKIE, value: 'payload.signature', url: 'https://map.example.test/', httpOnly: true, secure: true, sameSite: 'Lax', expires: 0 });
  assert.ok(!('domain' in cookie), 'bound to the host by address, never by a domain');
  for (const [weaker, what] of [
    [GOOD.replace('; Secure', ''), 'Secure'],
    [GOOD.replace('; HttpOnly', ''), 'HttpOnly'],
    [GOOD.replace('SameSite=Lax', 'SameSite=None'), 'SameSite'],
    [GOOD.replace('; SameSite=Lax', ''), 'SameSite'],
    [GOOD.replace('Path=/', 'Path=/api'), 'Path'],
    [`${GOOD}; Domain=example.test`, 'Domain'],
    [GOOD.replace(SESSION_COOKIE, 'mcmap_session'), 'its name'],
    [GOOD.replace('Max-Age=600', 'Max-Age=604800'), 'Max-Age'],
    [GOOD.replace('; Max-Age=600', ''), 'Max-Age'],
  ]) {
    assert.throws(() => browserCookie(weaker, 'https://map.example.test'), new RegExp(what), `a cookie without ${what} was accepted`);
  }
});

test('a Secret is named by three plain parts and nothing else', () => {
  assert.deepEqual(parseSecretRef('games/map-checks/secret'), { namespace: 'games', name: 'map-checks', key: 'secret' });
  for (const bad of ['', 'a/b', 'a/b/c/d', 'a/b/c d', 'a/-o yaml/c', 'a/b/$(x)', '../b/c']) assert.throws(() => parseSecretRef(bad));
});

test('the secret goes in a header, to an address with nothing in it, and comes back in nothing', async () => {
  const { file } = await secretFile();
  const calls = [];
  const got = await exchange({
    internalUrl: 'http://map-internal.test:9090', publicUrl: 'https://map.example.test', userAgent: 'test', secretFile: file,
    fetchImpl: async (url, init) => { calls.push({ url: String(url), init }); return answer(200); },
  });
  assert.equal(calls.length, 1);
  assert.equal(calls[0].url, 'http://map-internal.test:9090/internal/v1/service-sessions');
  assert.equal(calls[0].init.method, 'POST');
  assert.equal(calls[0].init.headers.Authorization, `Bearer ${SECRET}`);
  assert.equal(calls[0].init.body, undefined);
  assert.ok(!JSON.stringify(got).includes(SECRET));
  assert.equal(got.cookie.url, 'https://map.example.test/');
  // Whatever is written afterwards has both taken out of it.
  assert.equal(redact(`x ${SECRET} y payload.signature z`), 'x [redacted] y [redacted] z');
});

test('a refusal says its status and never what was presented', async () => {
  const { file } = await secretFile();
  const base = { internalUrl: 'http://map-internal.test:9090', publicUrl: 'https://map.example.test', userAgent: 'test', secretFile: file };
  const cases = [
    [async () => answer(401), /answered 401/],
    [async () => answer(429), /answered 429/],
    [async () => answer(200, { cookies: [] }), /0 cookies/],
    [async () => answer(200, { cookies: [GOOD, GOOD] }), /2 cookies/],
    [async () => answer(200, { identity: 'Steve' }), /not the service identity/],
    [async () => answer(200, { cookies: [GOOD.replace('; Secure', '')] }), /Secure/],
    [async () => { throw Object.assign(new TypeError(`fetch failed for Bearer ${SECRET}`), { cause: { code: 'ECONNREFUSED' } }); }, /ECONNREFUSED/],
  ];
  for (const [fetchImpl, expected] of cases) {
    await assert.rejects(exchange({ ...base, fetchImpl }), (err) => {
      assert.match(err.message, expected);
      assert.ok(!err.message.includes(SECRET) && !String(err.stack).includes(SECRET), 'the secret is in the error');
      return true;
    });
  }
  await assert.rejects(exchange({ ...base, secretFile: undefined, fetchImpl: async () => answer(200) }), /no secret source/);
});

test('the secret is read from the cluster by a child whose output is kept', async () => {
  const dir = await mkdtemp(path.join(tmpdir(), 'mcmap-e2e-test-'));
  const kubectl = path.join(dir, 'kubectl');
  const log = path.join(dir, 'args');
  // Stands in for kubectl: says what it was asked and answers as kubectl
  // does, with the value in base64.
  await writeFile(kubectl, `#!/bin/sh\nprintf '%s\\n' "$*" > ${log}\nprintf '%s' '${Buffer.from(SECRET).toString('base64')}'\n`);
  await chmod(kubectl, 0o755);
  let presented;
  await exchange({
    internalUrl: 'http://127.0.0.1:1', publicUrl: 'https://map.example.test', userAgent: 'test', secretRef: 'games/map-checks/secret', kubectl,
    fetchImpl: async (url, init) => { presented = init.headers.Authorization; return answer(200); },
  });
  assert.equal(presented, `Bearer ${SECRET}`);
  const { readFile } = await import('node:fs/promises');
  // A read, and of the one key: nothing that changes the cluster.
  assert.equal((await readFile(log, 'utf8')).trim(), 'get secret map-checks -n games -o jsonpath={.data.secret}');

  await writeFile(kubectl, `#!/bin/sh\necho "error: ${SECRET}" >&2\nexit 1\n`);
  await assert.rejects(exchange({ internalUrl: 'http://127.0.0.1:1', publicUrl: 'https://map.example.test', userAgent: 'test', secretRef: 'games/map-checks/secret', kubectl, fetchImpl: async () => answer(200) }), (err) => {
    assert.ok(!err.message.includes(SECRET), 'what kubectl complained of is in the error');
    return true;
  });
});
